// Run: sh verify/repro/c4_mixed_capacity.sh  (copies this file to internal/transport/ of evalon/grpc-go-tr-93d15123 and runs it)

package transport

import (
	"bytes"
	"context"
	"fmt"
	"net"
	"os"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/net/http2"
	"golang.org/x/net/http2/hpack"
	"google.golang.org/grpc/internal/envconfig"
	"google.golang.org/grpc/internal/testutils"
	"google.golang.org/grpc/mem"
)

// c4RecordingPool wraps a BufferPool and records the capacity of every buffer
// that has been obtained from it and not yet returned.
type c4RecordingPool struct {
	inner mem.BufferPool

	mu          sync.Mutex
	outstanding map[*[]byte]int
	gets, puts  int
}

func newC4RecordingPool(inner mem.BufferPool) *c4RecordingPool {
	return &c4RecordingPool{inner: inner, outstanding: make(map[*[]byte]int)}
}

func (p *c4RecordingPool) Get(length int) *[]byte {
	buf := p.inner.Get(length)
	p.mu.Lock()
	p.outstanding[buf] = cap(*buf)
	p.gets++
	p.mu.Unlock()
	return buf
}

func (p *c4RecordingPool) Put(buf *[]byte) {
	p.mu.Lock()
	delete(p.outstanding, buf)
	p.puts++
	p.mu.Unlock()
	p.inner.Put(buf)
}

// stats returns the number of outstanding buffers and their total capacity.
func (p *c4RecordingPool) stats() (buffers, capacity int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, c := range p.outstanding {
		capacity += c
	}
	return len(p.outstanding), capacity
}

type c4Usage struct {
	entries  int
	payload  int      // bytes of payload waiting to be read
	capacity int      // capacity of the backing arrays holding that payload
	detail   []string // "len/cap" per queued entry, in queue order
}

// c4Measure reports what a recvBuffer holds: the entry parked in the channel,
// the backlog entries and the compaction buffer (tail).
func c4Measure(b *recvBuffer) c4Usage {
	b.mu.Lock()
	defer b.mu.Unlock()
	var u c4Usage
	add := func(l, c int) {
		u.entries++
		u.payload += l
		u.capacity += c
		u.detail = append(u.detail, fmt.Sprintf("%d/%d", l, c))
	}
	select {
	case m := <-b.c:
		// Put the message back after inspecting it; b.mu is held so nothing
		// else can fill the channel in between.
		if m.buffer != nil {
			add(m.buffer.Len(), cap(m.buffer.ReadOnlyData()))
		}
		b.c <- m
	default:
	}
	for _, m := range b.backlog {
		if m.buffer != nil {
			add(m.buffer.Len(), cap(m.buffer.ReadOnlyData()))
		}
	}
	if b.tail != nil {
		add(len(b.tail), cap(b.tail))
	}
	return u
}

func c4KiB(n int) string { return fmt.Sprintf("%d B = %.1f KiB", n, float64(n)/1024) }

const (
	c4Pairs     = 31
	c4LargeSize = 2048
	c4Limit     = 590 * 1024
)

// mixed_traffic_capacity, measured on a bare recvBuffer: 31 pairs of a 1-byte
// payload followed by a 2-KiB payload are queued and nothing is read.
func (s) TestVerifyC4_MixedTrafficCapacity(t *testing.T) {
	large := bytes.Repeat([]byte{'L'}, c4LargeSize)
	for _, tc := range []struct {
		name       string
		compaction bool
		// recvPool is what the transports assign to recvBuffer.pool
		// (s.Stream.buf.pool = t.bufferPool).
		recvPool func(def mem.BufferPool) mem.BufferPool
		// pooledPayloads selects how incoming payload buffers are created:
		// true mirrors framer.readDataFrame (pool.Get(len) for payloads above
		// the pooling threshold, make() otherwise); false uses exactly sized
		// heap slices.
		pooledPayloads bool
		// pairs and largeSize default to the claim's workload (31 pairs,
		// 2 KiB) when zero.
		pairs, largeSize int
	}{
		{name: "A default pool everywhere (what a transport does)", compaction: true, recvPool: func(d mem.BufferPool) mem.BufferPool { return d }, pooledPayloads: true},
		{name: "B default pool for compaction, exactly sized payload slices", compaction: true, recvPool: func(d mem.BufferPool) mem.BufferPool { return d }, pooledPayloads: false},
		{name: "C recvBuffer.pool unset (how the eval fixture initialises it)", compaction: true, recvPool: func(mem.BufferPool) mem.BufferPool { return nil }, pooledPayloads: true},
		{name: "D default pool everywhere, compaction disabled (baseline)", compaction: false, recvPool: func(d mem.BufferPool) mem.BufferPool { return d }, pooledPayloads: true},
		// Not part of the claim: the smallest payload that bypasses compaction
		// (maxCompactedPayloadSize+1), as many pairs as fit in a 65535-byte window.
		{name: "E like A, 63 pairs of 1 byte + 1025 bytes", compaction: true, recvPool: func(d mem.BufferPool) mem.BufferPool { return d }, pooledPayloads: true, pairs: 63, largeSize: maxCompactedPayloadSize + 1},
		{name: "F like E, compaction disabled (baseline)", compaction: false, recvPool: func(d mem.BufferPool) mem.BufferPool { return d }, pooledPayloads: true, pairs: 63, largeSize: maxCompactedPayloadSize + 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			pairs, large := c4Pairs, large
			if tc.pairs != 0 {
				pairs, large = tc.pairs, bytes.Repeat([]byte{'L'}, tc.largeSize)
			}
			testutils.SetEnvConfig(t, &envconfig.EnableReceiveBufferCompaction, tc.compaction)
			// The default pool, observed through a pass-through recorder.
			pool := newC4RecordingPool(mem.DefaultBufferPool())
			var b recvBuffer
			b.init()
			b.pool = tc.recvPool(pool)

			newPayload := func(data []byte) mem.Buffer {
				if tc.pooledPayloads {
					return mem.Copy(data, pool)
				}
				return mem.SliceBuffer(append([]byte(nil), data...))
			}
			for i := 0; i < pairs; i++ {
				b.put(recvMsg{buffer: newPayload([]byte{byte(i)})})
				b.put(recvMsg{buffer: newPayload(large)})
			}

			u := c4Measure(&b)
			bufs, pooledCap := pool.stats()
			t.Logf("VERIFY-C4 mixed [%s] %d pairs of (1 byte, %d bytes)", tc.name, pairs, len(large))
			t.Logf("VERIFY-C4   queued entries (len/cap): %s", strings.Join(u.detail, " "))
			t.Logf("VERIFY-C4   entries=%d payload=%s backing capacity=%s (%.2fx payload); of which outstanding from the default pool: %d buffers, %s",
				u.entries, c4KiB(u.payload), c4KiB(u.capacity), float64(u.capacity)/float64(u.payload), bufs, c4KiB(pooledCap))
			t.Logf("VERIFY-C4   backing capacity > 590 KiB (%d B)? %v", c4Limit, u.capacity > c4Limit)

			// Drain and release everything.
			ctx, cancel := context.WithTimeout(context.Background(), defaultTestTimeout)
			defer cancel()
			r := &recvBufferReader{ctx: ctx, ctxDone: ctx.Done(), recv: &b}
			for read := 0; read < u.payload; {
				buf, err := r.Read(u.payload - read)
				if err != nil {
					t.Fatalf("Read() failed after %d bytes: %v", read, err)
				}
				read += buf.Len()
				buf.Free()
			}
			if bufs, _ := pool.stats(); bufs != 0 {
				t.Errorf("%d pooled buffers outstanding after reading everything", bufs)
			}
		})
	}
}

// mixed_traffic_capacity, measured end to end: a raw HTTP/2 client sends the
// same 31 pairs as DATA frames on one stream of a real http2Server that uses
// the default buffer pool and whose handler does not read. 31*(1+2048) = 63519
// bytes fit in the default 65535-byte stream and connection windows.
func (s) TestVerifyC4_MixedTrafficCapacity_ServerTransport(t *testing.T) {
	for _, compaction := range []bool{true, false} {
		t.Run(fmt.Sprintf("compaction_enabled=%v", compaction), func(t *testing.T) {
			testutils.SetEnvConfig(t, &envconfig.EnableReceiveBufferCompaction, compaction)
			server := setUpServerOnly(t, 0, &ServerConfig{BufferPool: mem.DefaultBufferPool()}, suspended)
			defer server.stop()

			mconn, err := net.Dial("tcp", server.lis.Addr().String())
			if err != nil {
				t.Fatalf("net.Dial() failed: %v", err)
			}
			defer mconn.Close()
			if _, err := mconn.Write(clientPreface); err != nil {
				t.Fatalf("Writing client preface failed: %v", err)
			}
			framer := http2.NewFramer(mconn, mconn)
			if err := framer.WriteSettings(); err != nil {
				t.Fatalf("Writing settings failed: %v", err)
			}
			var mu sync.Mutex
			go func() {
				for {
					frame, err := framer.ReadFrame()
					if err != nil {
						return
					}
					if ping, ok := frame.(*http2.PingFrame); ok && !ping.IsAck() {
						mu.Lock()
						framer.WritePing(true, ping.Data)
						mu.Unlock()
					}
				}
			}()

			var hbuf bytes.Buffer
			henc := hpack.NewEncoder(&hbuf)
			for _, hf := range []hpack.HeaderField{
				{Name: ":method", Value: "POST"},
				{Name: ":path", Value: "/foo/bar"},
				{Name: ":authority", Value: "localhost"},
				{Name: "content-type", Value: "application/grpc"},
			} {
				if err := henc.WriteField(hf); err != nil {
					t.Fatalf("Encoding header %v failed: %v", hf, err)
				}
			}
			large := bytes.Repeat([]byte{'L'}, c4LargeSize)
			mu.Lock()
			err = framer.WriteHeaders(http2.HeadersFrameParam{StreamID: 1, BlockFragment: hbuf.Bytes(), EndHeaders: true})
			for i := 0; i < c4Pairs && err == nil; i++ {
				if err = framer.WriteData(1, false, []byte{byte(i)}); err == nil {
					err = framer.WriteData(1, false, large)
				}
			}
			mu.Unlock()
			if err != nil {
				t.Fatalf("Writing frames failed: %v", err)
			}

			const wantPayload = c4Pairs * (1 + c4LargeSize)
			ctx, cancel := context.WithTimeout(context.Background(), defaultTestTimeout)
			defer cancel()
			var ss *ServerStream
			var u c4Usage
			for ; ctx.Err() == nil; <-time.After(time.Millisecond) {
				if ss == nil {
					ss = findServerStream(server, 1)
				}
				if ss == nil {
					continue
				}
				if u = c4Measure(&ss.buf); u.payload == wantPayload {
					break
				}
			}
			if ctx.Err() != nil {
				t.Fatalf("Timed out waiting for the server to receive all DATA frames (got %d of %d bytes)", u.payload, wantPayload)
			}

			t.Logf("VERIFY-C4 mixed e2e [http2Server, default pool, compaction=%v]", compaction)
			t.Logf("VERIFY-C4   queued entries (len/cap): %s", strings.Join(u.detail, " "))
			t.Logf("VERIFY-C4   entries=%d payload=%s backing capacity=%s (%.2fx payload)", u.entries, c4KiB(u.payload), c4KiB(u.capacity), float64(u.capacity)/float64(u.payload))
			t.Logf("VERIFY-C4   backing capacity > 590 KiB (%d B)? %v", c4Limit, u.capacity > c4Limit)

			data, err := ss.read(wantPayload)
			if err != nil {
				t.Fatalf("ss.read(%d) failed: %v", wantPayload, err)
			}
			data.Free()
		})
	}
}

// partial_read_retention: a burst of 1-byte messages is compacted into
// several chunks, then all but one byte is read and released.
func (s) TestVerifyC4_PartialReadRetention(t *testing.T) {
	// With the default pool (what a transport installs) and with
	// recvBuffer.pool unset (how the eval fixture initialises a recvBuffer; the
	// chunks are then plain heap slices and only the heap figure applies).
	for _, usePool := range []bool{true, false} {
		t.Run(fmt.Sprintf("recvBuffer.pool_set=%v", usePool), func(t *testing.T) {
			testVerifyC4PartialReadRetention(t, usePool)
		})
	}
}

func testVerifyC4PartialReadRetention(t *testing.T, usePool bool) {
	testutils.SetEnvConfig(t, &envconfig.EnableReceiveBufferCompaction, true)
	const numMsgs = 200000
	pool := newC4RecordingPool(mem.DefaultBufferPool())
	b := new(recvBuffer)
	b.init()
	if usePool {
		b.pool = pool
	}

	heap := func() int64 {
		var ms runtime.MemStats
		runtime.GC()
		runtime.GC()
		runtime.ReadMemStats(&ms)
		return int64(ms.HeapAlloc)
	}
	heapEmpty := heap()
	for i := 0; i < numMsgs; i++ {
		b.put(recvMsg{buffer: mem.SliceBuffer{byte(i)}})
	}
	heapFull := heap()

	full := c4Measure(b)
	fullBufs, fullPooled := pool.stats()
	t.Logf("VERIFY-C4 partial: after queueing %d 1-byte messages: entries=%d payload=%s backing capacity=%s; outstanding from pool: %d buffers, %s; heap in use grew by %s",
		numMsgs, full.entries, c4KiB(full.payload), c4KiB(full.capacity), fullBufs, c4KiB(fullPooled), c4KiB(int(heapFull-heapEmpty)))
	t.Logf("VERIFY-C4 partial:   queued entries (len/cap): %s", strings.Join(full.detail, " "))
	if full.entries < 3 {
		t.Fatalf("Burst spans %d entries, want several chunks", full.entries)
	}

	// Consume all but the last byte, the way a stream reader does, releasing
	// every buffer that is handed out.
	ctx, cancel := context.WithTimeout(context.Background(), defaultTestTimeout)
	defer cancel()
	r := &recvBufferReader{ctx: ctx, ctxDone: ctx.Done(), recv: b}
	read := 0
	for read < numMsgs-1 {
		buf, err := r.Read(min(1000, numMsgs-1-read))
		if err != nil {
			t.Fatalf("Read() failed after %d bytes: %v", read, err)
		}
		for _, c := range buf.ReadOnlyData() {
			if c != byte(read) {
				t.Fatalf("byte %d = %d, want %d", read, c, byte(read))
			}
			read++
		}
		buf.Free()
	}
	heapAfter := heap()

	rest := c4Measure(b)
	readerCap := 0
	if r.last != nil {
		readerCap = cap(r.last.ReadOnlyData())
		rest.detail = append(rest.detail, fmt.Sprintf("reader.last %d/%d", r.last.Len(), readerCap))
	}
	restBufs, restPooled := pool.stats()
	t.Logf("VERIFY-C4 partial: after reading %d of %d bytes (%.4f%%): entries left in recvBuffer=%d (capacity %s), remainder held by the reader: %s; outstanding from pool: %d buffers, %s (pool gets=%d puts=%d); heap in use vs. empty: %s",
		read, numMsgs, 100*float64(read)/numMsgs, rest.entries, c4KiB(rest.capacity), c4KiB(readerCap), restBufs, c4KiB(restPooled), pool.gets, pool.puts, c4KiB(int(heapAfter-heapEmpty)))
	t.Logf("VERIFY-C4 partial:   still held (len/cap): %s", strings.Join(rest.detail, " "))
	if usePool {
		t.Logf("VERIFY-C4 partial: pooled backing storage still retained is %.1f%% of the full burst's (%d B of %d B)",
			100*float64(restPooled)/float64(fullPooled), restPooled, fullPooled)
	}
	t.Logf("VERIFY-C4 partial: heap still in use is %.1f%% of what the full burst held (%d B of %d B)",
		100*float64(heapAfter-heapEmpty)/float64(heapFull-heapEmpty), heapAfter-heapEmpty, heapFull-heapEmpty)

	// Release the rest.
	buf, err := r.Read(1)
	if err != nil {
		t.Fatalf("Read() of the last byte failed: %v", err)
	}
	buf.Free()
	if bufs, _ := pool.stats(); bufs != 0 {
		t.Errorf("%d pooled buffers outstanding after reading everything", bufs)
	}
	runtime.KeepAlive(b)
}

// The claim's workload in configuration A, with compaction left exactly as the
// process environment configured it (no override), to observe the default and
// the GRPC_GO_EXPERIMENTAL_ENABLE_RECEIVE_BUFFER_COMPACTION=false escape hatch.
func (s) TestVerifyC4_MixedTrafficCapacity_ProcessEnv(t *testing.T) {
	large := bytes.Repeat([]byte{'L'}, c4LargeSize)
	pool := mem.DefaultBufferPool()
	var b recvBuffer
	b.init()
	b.pool = pool
	for i := 0; i < c4Pairs; i++ {
		b.put(recvMsg{buffer: mem.Copy([]byte{byte(i)}, pool)})
		b.put(recvMsg{buffer: mem.Copy(large, pool)})
	}
	u := c4Measure(&b)
	t.Logf("VERIFY-C4 mixed [process environment: GRPC_GO_EXPERIMENTAL_ENABLE_RECEIVE_BUFFER_COMPACTION=%q, envconfig.EnableReceiveBufferCompaction=%v]",
		os.Getenv("GRPC_GO_EXPERIMENTAL_ENABLE_RECEIVE_BUFFER_COMPACTION"), envconfig.EnableReceiveBufferCompaction)
	t.Logf("VERIFY-C4   entries=%d payload=%s backing capacity=%s (%.2fx payload)", u.entries, c4KiB(u.payload), c4KiB(u.capacity), float64(u.capacity)/float64(u.payload))

	// Drain and release everything.
	ctx, cancel := context.WithTimeout(context.Background(), defaultTestTimeout)
	defer cancel()
	b.put(recvMsg{err: context.Canceled})
	r := &recvBufferReader{ctx: ctx, ctxDone: ctx.Done(), recv: &b}
	for {
		buf, err := r.Read(1 << 20)
		if err != nil {
			break
		}
		buf.Free()
	}
}
