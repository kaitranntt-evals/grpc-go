// Run (after verify/repro/setup_worktrees.sh): cp verify/repro/c7_two_frame_burst_test.go /tmp/claims/5368e0cb/internal/transport/ && (cd /tmp/claims/5368e0cb && go test ./internal/transport -run '^TestVerifyC7' -count=1 -v); rm /tmp/claims/5368e0cb/internal/transport/c7_two_frame_burst_test.go
package transport

import (
	"runtime"
	"testing"
	"unsafe"

	"google.golang.org/grpc/internal/envconfig"
	"google.golang.org/grpc/mem"
)

// backing returns the capacity and first-byte address of the storage that a
// queued recvMsg retains.
func c7Backing(t *testing.T, buf mem.Buffer) (capacity int, first *byte, typ string) {
	switch v := buf.(type) {
	case mem.SliceBuffer:
		return cap(v), unsafe.SliceData(v), "mem.SliceBuffer"
	case *mem.SliceBuffer:
		return cap(*v), unsafe.SliceData(*v), "*mem.SliceBuffer"
	}
	t.Fatalf("unexpected buffer type %T", buf)
	return 0, nil, ""
}

// TestVerifyC7_BurstRetainedStorage submits bursts of separately backed
// one-byte payloads (exactly what http2Client/http2Server.handleData build via
// mem.Copy for a 1-byte DATA frame) with no consumption and inspects the
// storage each queued entry retains.
func TestVerifyC7_BurstRetainedStorage(t *testing.T) {
	orig := envconfig.EnableReceiveBufferCompaction
	defer func() { envconfig.EnableReceiveBufferCompaction = orig }()
	pool := mem.DefaultBufferPool()

	for _, frames := range []int{2, 4} {
		for _, enabled := range []bool{true, false} {
			envconfig.EnableReceiveBufferCompaction = enabled
			var b recvBuffer
			b.init()
			var payloads []mem.Buffer
			for i := range frames {
				p := mem.Copy([]byte{byte(i + 1)}, pool)
				payloads = append(payloads, p)
				b.put(recvMsg{buffer: p})
			}
			t.Logf("frames=%d compaction=%v: len(b.c)=%d len(b.backlog)=%d", frames, enabled, len(b.c), len(b.backlog))
			total := 0
			for i, m := range b.backlog {
				c, first, typ := c7Backing(t, m.buffer)
				total += c
				origIdx := -1
				for j, p := range payloads {
					if _, pf, _ := c7Backing(t, p); pf == first {
						origIdx = j
					}
				}
				t.Logf("  backlog[%d]: type=%s len=%d cap=%d sameStorageAsOriginalFrame=%d", i, typ, m.buffer.Len(), c, origIdx)
				if frames == 2 {
					if enabled && (c != 1024 || origIdx != -1) {
						t.Errorf("enabled: second frame retained cap=%d orig=%d, claim expects a fresh 1024-byte destination", c, origIdx)
					}
					if !enabled && (c != 1 || origIdx != 1) {
						t.Errorf("disabled: second frame retained cap=%d orig=%d, claim expects the original 1-byte storage", c, origIdx)
					}
				}
			}
			t.Logf("  total backing capacity retained by backlog = %d bytes for %d queued payload byte(s)", total, frames-1)
		}
	}
}

// TestVerifyC7_HeapRetained measures the live heap retained by 20000
// independent receive buffers, each holding an unread burst.
func TestVerifyC7_HeapRetained(t *testing.T) {
	orig := envconfig.EnableReceiveBufferCompaction
	defer func() { envconfig.EnableReceiveBufferCompaction = orig }()
	pool := mem.DefaultBufferPool()
	const streams = 20000
	for _, frames := range []int{1, 2, 4, 8, 16, 32, 64} {
		var perStream [2]uint64
		for k, enabled := range []bool{true, false} {
			envconfig.EnableReceiveBufferCompaction = enabled
			bufs := make([]recvBuffer, streams)
			for i := range bufs {
				bufs[i].init()
			}
			var before, after runtime.MemStats
			runtime.GC()
			runtime.ReadMemStats(&before)
			for i := range bufs {
				for j := range frames {
					bufs[i].put(recvMsg{buffer: mem.Copy([]byte{byte(j)}, pool)})
				}
			}
			runtime.GC()
			runtime.ReadMemStats(&after)
			runtime.KeepAlive(bufs)
			perStream[k] = (after.HeapAlloc - before.HeapAlloc) / streams
		}
		t.Logf("burst of %d one-byte frames, unread: heap retained per stream: compaction enabled=%d B, disabled=%d B", frames, perStream[0], perStream[1])
	}
}
