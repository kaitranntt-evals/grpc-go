//go:build ignore

// Audit probes for C3/C4/C5/C8. Run: go test -v -run '^TestVerify' ./internal/transport -count=1
package transport

import (
	"bytes"
	"context"
	"io"
	"runtime"
	"testing"
	"time"

	"google.golang.org/grpc/mem"
)

type verifyTrackedBuffer struct {
	mem.Buffer
	frees int
}

func (b *verifyTrackedBuffer) Free() { b.frees++; b.Buffer.Free() }

// C3: 1024 queued one-byte payloads followed by a 16 KiB pooled frame.
func TestVerifyC3_LargeFrameAfterSmallFrames(t *testing.T) {
	pool := mem.DefaultBufferPool()
	b := &recvBuffer{}
	b.init(pool)
	for i := 0; i < 1025; i++ { // first goes to channel, 1024 land in backlog
		b.put(recvMsg{buffer: mem.Copy([]byte{byte(i)}, pool)})
	}
	heap := b.uncompactedSuffixLen*recvMsgSize + b.uncompactedBytes
	t.Logf("before large: backlog=%d suffixLen=%d suffixBytes=%d heapEstimate=%d compactionThreshold=%d recvMsgSize=%d",
		len(b.backlog), b.uncompactedSuffixLen, b.uncompactedBytes, heap, compactionThreshold, recvMsgSize)
	raw := pool.Get(16 * 1024)
	for i := range *raw {
		(*raw)[i] = byte(i % 251)
	}
	large := &verifyTrackedBuffer{Buffer: mem.NewBuffer(raw, pool)}
	origPtr := &large.ReadOnlyData()[0]
	projectedHeap := (b.uncompactedSuffixLen+1)*recvMsgSize + b.uncompactedBytes + large.Len()
	t.Logf("projected heapEstimate with large frame=%d (threshold %d, utilization check limit %d)", projectedHeap, compactionThreshold, utilizationFactor*(b.uncompactedBytes+large.Len()))
	b.put(recvMsg{buffer: large})
	last := b.backlog[len(b.backlog)-1]
	t.Logf("after large: backlog=%d lastLen=%d lastPtrSameAsLarge=%v large.frees=%d",
		len(b.backlog), last.buffer.Len(), &last.buffer.ReadOnlyData()[0] == origPtr, large.frees)
	if last.buffer.Len() == 16*1024 && &last.buffer.ReadOnlyData()[0] == origPtr {
		t.Logf("RESULT: large frame queued as received (not copied)")
	} else {
		t.Logf("RESULT: large frame was COPIED into consolidated storage")
	}
	// drain & verify data intact
	var got []byte
	for len(got) < 1025+16*1024 {
		select {
		case m := <-b.c:
			got = append(got, m.buffer.ReadOnlyData()...)
			m.buffer.Free()
		default:
		}
		b.load()
	}
	want := make([]byte, 0)
	for i := 0; i < 1025; i++ {
		want = append(want, byte(i))
	}
	for i := 0; i < 16*1024; i++ {
		want = append(want, byte(i%251))
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("payload mismatch")
	}
}

// C4: terminal error then a second nil-buffer terminal message.
func TestVerifyC4_DoubleErrorPut(t *testing.T) {
	b := &recvBuffer{}
	b.init(mem.DefaultBufferPool())
	b.put(recvMsg{err: io.EOF})
	func() {
		defer func() {
			if r := recover(); r != nil {
				t.Logf("RESULT: second put(recvMsg{err}) PANICKED: %v", r)
				return
			}
			t.Logf("RESULT: second put completed without panic")
		}()
		b.put(recvMsg{err: io.EOF})
	}()
}

// C5: message carrying both a buffer and an error crossing the compaction threshold.
func TestVerifyC5_ErrorWithBufferCompacted(t *testing.T) {
	pool := mem.DefaultBufferPool()
	b := &recvBuffer{}
	b.init(pool)
	for i := 0; i < 1025; i++ {
		b.put(recvMsg{buffer: mem.Copy([]byte{1}, pool)})
	}
	t.Logf("before: backlog=%d suffixLen=%d", len(b.backlog), b.uncompactedSuffixLen)
	b.put(recvMsg{buffer: mem.Copy([]byte{2}, pool), err: io.EOF})
	t.Logf("after: backlog=%d b.err=%v", len(b.backlog), b.err)
	for i, m := range b.backlog {
		t.Logf("backlog[%d]: len=%d err=%v", i, m.buffer.Len(), m.err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	r := &recvBufferReader{ctx: ctx, ctxDone: ctx.Done(), recv: b}
	var got []byte
	var readErr error
	for {
		buf, err := r.Read(1 << 20)
		if buf != nil {
			got = append(got, buf.ReadOnlyData()...)
			buf.Free()
		}
		if err != nil {
			readErr = err
			break
		}
	}
	t.Logf("consumed %d bytes (want 1026 incl. the byte attached to the error message); final read error = %v", len(got), readErr)
	if readErr == io.EOF {
		t.Logf("RESULT: terminal io.EOF preserved")
	} else {
		t.Logf("RESULT: terminal io.EOF LOST; reader observed %v instead", readErr)
	}
}

func verifyRetained(b *recvBuffer) (entries, capBytes, metaBytes int) {
	b.mu.Lock()
	defer b.mu.Unlock()
	seen := map[*byte]bool{}
	for _, m := range b.backlog {
		if m.buffer != nil {
			d := m.buffer.ReadOnlyData()
			if len(d) > 0 {
				p := &d[:cap(d)][0]
				if !seen[p] {
					seen[p] = true
					capBytes += cap(d)
				}
			}
		}
	}
	entries = len(b.backlog) + len(b.c)
	metaBytes = cap(b.backlog) * recvMsgSize
	return
}

// C8: retained storage across fragmentation, partial reads, drains and refills.
func TestVerifyC8_RetainedStorageBounded(t *testing.T) {
	pool := mem.DefaultBufferPool()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	for _, frag := range []int{1, 3, 7} {
		b := &recvBuffer{}
		b.init(pool)
		r := &recvBufferReader{ctx: ctx, ctxDone: ctx.Done(), recv: b}
		total := 256 << 10
		fill := func() {
			for off := 0; off < total; off += frag {
				n := min(frag, total-off)
				b.put(recvMsg{buffer: mem.Copy(make([]byte, n), pool)})
			}
		}
		drain := func(n int) int {
			got := 0
			for got < n {
				buf, err := r.Read(n - got)
				if err != nil {
					t.Fatalf("read: %v", err)
				}
				got += buf.Len()
				buf.Free()
			}
			return got
		}
		var ms runtime.MemStats
		heap := func() uint64 { runtime.GC(); runtime.ReadMemStats(&ms); return ms.HeapAlloc }
		base := heap()
		for cycle := 0; cycle < 4; cycle++ {
			fill()
			e, c, m := verifyRetained(b)
			t.Logf("frag=%d cycle=%d after fill(%d bytes unread): entries=%d uniqueCap=%d metaCap=%d heapDelta=%d", frag, cycle, total, e, c, m, int64(heap())-int64(base))
			drain(total / 3) // partial read
			e, c, m = verifyRetained(b)
			t.Logf("frag=%d cycle=%d after partial drain (%d unread): entries=%d uniqueCap=%d metaCap=%d heapDelta=%d", frag, cycle, total-total/3, e, c, m, int64(heap())-int64(base))
			drain(total - total/3) // complete nonterminal drain
			e, c, m = verifyRetained(b)
			t.Logf("frag=%d cycle=%d after full drain (0 unread): entries=%d uniqueCap=%d metaCap=%d heapDelta=%d", frag, cycle, e, c, m, int64(heap())-int64(base))
		}
		runtime.KeepAlive(b)
	}
	// increasing-volume probe
	for _, total := range []int{64 << 10, 256 << 10, 1 << 20} {
		b := &recvBuffer{}
		b.init(pool)
		for i := 0; i < total; i++ {
			b.put(recvMsg{buffer: mem.Copy([]byte{1}, pool)})
		}
		e, c, m := verifyRetained(b)
		t.Logf("volume=%d one-byte frames: entries=%d uniqueCap=%d (%.2fx payload) metaCap=%d", total, e, c, float64(c)/float64(total), m)
	}
}
