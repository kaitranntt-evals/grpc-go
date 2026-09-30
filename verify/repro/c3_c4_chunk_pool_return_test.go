// Run (after verify/repro/setup_worktrees.sh): cp verify/repro/c3_c4_chunk_pool_return_test.go /tmp/claims/d0520117/internal/transport/ && (cd /tmp/claims/d0520117 && go test ./internal/transport -run '^TestVerifyC3|^TestVerifyC4' -count=1 -v); rm /tmp/claims/d0520117/internal/transport/c3_c4_chunk_pool_return_test.go
package transport

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
	"unsafe"

	"golang.org/x/net/http2"
	"golang.org/x/net/http2/hpack"
	"google.golang.org/grpc/mem"
)

// idPool wraps a mem.BufferPool and tracks every acquisition by the identity
// of its backing array, recording whether that exact allocation is ever Put
// back and whether it was acquired by recvBuffer.newChunk.
type idPool struct {
	inner mem.BufferPool
	mu    sync.Mutex
	recs  []*idRec
	live  map[*byte]*idRec
}

type idRec struct {
	req, capacity int
	fromNewChunk  bool
	returned      bool
}

func newIDPool(inner mem.BufferPool) *idPool {
	return &idPool{inner: inner, live: map[*byte]*idRec{}}
}

func (p *idPool) Get(n int) *[]byte {
	h := p.inner.Get(n)
	pcs := make([]uintptr, 16)
	frames := runtime.CallersFrames(pcs[:runtime.Callers(2, pcs)])
	rec := &idRec{req: n, capacity: cap(*h)}
	for {
		f, more := frames.Next()
		if strings.HasSuffix(f.Function, "(*recvBuffer).newChunk") {
			rec.fromNewChunk = true
		}
		if !more {
			break
		}
	}
	p.mu.Lock()
	p.recs = append(p.recs, rec)
	p.live[unsafe.SliceData((*h)[:cap(*h)])] = rec
	p.mu.Unlock()
	return h
}

func (p *idPool) Put(h *[]byte) {
	p.mu.Lock()
	k := unsafe.SliceData((*h)[:cap(*h)])
	if rec := p.live[k]; rec != nil {
		rec.returned = true
		delete(p.live, k)
	}
	p.mu.Unlock()
	p.inner.Put(h)
}

// chunks returns the acquisitions made by recvBuffer.newChunk.
func (p *idPool) chunks() []idRec {
	p.mu.Lock()
	defer p.mu.Unlock()
	var out []idRec
	for _, r := range p.recs {
		if r.fromNewChunk {
			out = append(out, *r)
		}
	}
	return out
}

func logChunks(t *testing.T, label string, recs []idRec) (unreturned1024, unreturnedLarger, returnedLarger int) {
	t.Helper()
	for i, r := range recs {
		t.Logf("  %s: newChunk acquisition #%d: Get(%d) -> cap=%d returnedToPool=%v", label, i, r.req, r.capacity, r.returned)
		switch {
		case r.capacity <= 1024 && !r.returned:
			unreturned1024++
		case r.capacity > 1024 && !r.returned:
			unreturnedLarger++
		case r.capacity > 1024 && r.returned:
			returnedLarger++
		}
	}
	return
}

// tieredPoolWith1024 is a supported configured pool (public constructor
// mem.NewBinaryTieredBufferPool) that has a 1 KiB tier, so Get(1024) returns
// capacity-1024 storage.
func tieredPoolWith1024(t *testing.T) mem.BufferPool {
	p, err := mem.NewBinaryTieredBufferPool(8, 10, 12, 14, 15, 20)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

// C3 part "pool-return ownership" / C4 part "small-buffer release mechanism".
func TestVerifyC3_NewBufferDropsPoolReturnAtCap1024(t *testing.T) {
	for _, tc := range []struct {
		name  string
		inner mem.BufferPool
	}{
		{"binary tiered pool with 2^10 tier", tieredPoolWith1024(t)},
		{"mem.NewTieredBufferPool(256,1024,4096)", mem.NewTieredBufferPool(256, 1024, 4096)},
		{"exact-capacity mem.NopBufferPool", mem.NopBufferPool{}},
	} {
		for _, req := range []int{512, 1024, 1025, 2048} {
			pool := newIDPool(tc.inner)
			h := pool.Get(req)
			c := cap(*h)
			buf := mem.NewBuffer(h, pool)
			typ := fmt.Sprintf("%T", buf)
			buf.Free()
			returned := pool.recs[0].returned
			t.Logf("%s: Get(%d) -> cap=%d; mem.NewBuffer -> %s; after Free returnedToPool=%v", tc.name, req, c, typ, returned)
			if wantReturned := c > 1024; returned != wantReturned {
				t.Errorf("cap=%d: returned=%v, want %v", c, returned, wantReturned)
			}
		}
	}
}

// drain reads exactly n bytes and then expects io.EOF, freeing everything.
func drainStream(t *testing.T, st *Stream, want []byte) {
	t.Helper()
	got := make([]byte, 0, len(want))
	for len(got) < len(want) {
		p := make([]byte, min(7, len(want)-len(got)))
		if _, err := st.readTo(p); err != nil {
			t.Fatalf("readTo() failed after %d bytes: %v", len(got), err)
		}
		got = append(got, p...)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("payload mismatch")
	}
	if _, err := st.readTo(make([]byte, 1)); !errors.Is(err, io.EOF) {
		t.Fatalf("readTo() after all data = %v, want EOF", err)
	}
}

// burst queues n one-byte payloads (built exactly as handleData does, via
// mem.Copy with the transport pool) plus EOF on a fresh stream with no
// consumption, then consumes and frees everything.
func burst(t *testing.T, pool *idPool, n int) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), defaultTestTimeout)
	defer cancel()
	st := newTestRecvStream(ctx, pool) // the branch's own helper: s.buf.init(pool)
	want := make([]byte, n)
	for i := range want {
		want[i] = byte(i)
		st.write(recvMsg{buffer: mem.Copy(want[i:i+1], pool)})
	}
	st.write(recvMsg{err: io.EOF})
	drainStream(t, st, want)
}

// C3 part "compaction exposure", receive-buffer level.
func TestVerifyC3_CompactionChunkFromTieredPoolNeverReturned(t *testing.T) {
	for _, tc := range []struct {
		name            string
		inner           mem.BufferPool
		wantUnreturned1 bool
	}{
		{"configured pool with 1 KiB tier (NewBinaryTieredBufferPool(8,10,12,14,15,20))", tieredPoolWith1024(t), true},
		{"control: mem.DefaultBufferPool()", mem.DefaultBufferPool(), false},
	} {
		t.Logf("%s", tc.name)
		pool := newIDPool(tc.inner)
		burst(t, pool, 100) // 1 payload in the channel + 99 compacted
		recs := pool.chunks()
		un1024, unLarger, _ := logChunks(t, "burst=100", recs)
		if len(recs) == 0 {
			t.Errorf("compaction acquired no chunk")
		}
		if got := un1024 > 0; got != tc.wantUnreturned1 {
			t.Errorf("unreturned cap<=1024 chunks = %d, wantAny=%v", un1024, tc.wantUnreturned1)
		}
		if unLarger != 0 {
			t.Errorf("unreturned larger chunks = %d, want 0", unLarger)
		}
	}
}

// C4: exact-capacity pool, short bursts and a burst of 1025 queued payloads.
func TestVerifyC4_ExactCapacityPoolBursts(t *testing.T) {
	// n counts the payloads written; the first one goes straight to the
	// channel, so n-1 are queued/compacted.
	for _, n := range []int{2, 3, 10, 1026, 4000} {
		pool := newIDPool(mem.NopBufferPool{}) // Get(n) returns make([]byte, n): cap == n
		burst(t, pool, n)
		label := fmt.Sprintf("queued=%d", n-1)
		recs := pool.chunks()
		un1024, unLarger, retLarger := logChunks(t, label, recs)
		t.Logf("%s: chunks=%d unreturned(cap<=1024)=%d unreturned(cap>1024)=%d returned(cap>1024)=%d", label, len(recs), un1024, unLarger, retLarger)
		if un1024 != 1 {
			t.Errorf("%s: unreturned cap<=1024 chunks = %d, claim expects 1", label, un1024)
		}
		if unLarger != 0 {
			t.Errorf("%s: unreturned cap>1024 chunks = %d, want 0", label, unLarger)
		}
		if n-1 > 1024 && retLarger == 0 {
			t.Errorf("%s: expected a larger chunk returned normally", label)
		}
	}
}

// C3 end to end: a real http2Server transport configured with the pool via
// ServerConfig.BufferPool (what grpc.NewServer passes from
// experimental.BufferPool), a raw HTTP/2 client sending one-byte DATA frames
// to a handler that has not started reading, then full consumption.
func TestVerifyC3_EndToEndServerTransport(t *testing.T) {
	for _, tc := range []struct {
		name           string
		inner          mem.BufferPool
		wantUnreturned bool
	}{
		{"configured pool with 1 KiB tier", tieredPoolWith1024(t), true},
		{"control: mem.DefaultBufferPool()", mem.DefaultBufferPool(), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			const numFrames = 600
			ctx, cancel := context.WithTimeout(context.Background(), defaultTestTimeout)
			defer cancel()
			pool := newIDPool(tc.inner)
			server := setUpServerOnly(t, 0, &ServerConfig{BufferPool: pool}, suspended)
			defer server.stop()

			conn, err := (&net.Dialer{Timeout: 5 * time.Second}).Dial("tcp", server.lis.Addr().String())
			if err != nil {
				t.Fatalf("dial: %v", err)
			}
			defer conn.Close()
			conn.SetDeadline(time.Now().Add(defaultTestTimeout))
			if _, err := conn.Write(clientPreface); err != nil {
				t.Fatal(err)
			}
			framer := http2.NewFramer(conn, conn)
			if err := framer.WriteSettings(); err != nil {
				t.Fatal(err)
			}
			go func() {
				for {
					if _, err := framer.ReadFrame(); err != nil {
						return
					}
				}
			}()
			var hbuf bytes.Buffer
			henc := hpack.NewEncoder(&hbuf)
			for _, f := range []hpack.HeaderField{
				{Name: ":method", Value: "POST"},
				{Name: ":path", Value: "/foo.Tiny"},
				{Name: ":authority", Value: "localhost"},
				{Name: "content-type", Value: "application/grpc"},
				{Name: "te", Value: "trailers"},
			} {
				henc.WriteField(f)
			}
			if err := framer.WriteHeaders(http2.HeadersFrameParam{StreamID: 1, BlockFragment: hbuf.Bytes(), EndHeaders: true}); err != nil {
				t.Fatal(err)
			}
			want := make([]byte, numFrames)
			for i := range want {
				want[i] = byte(i)
				if err := framer.WriteData(1, false, want[i:i+1]); err != nil {
					t.Fatal(err)
				}
			}
			if err := framer.WriteData(1, true, nil); err != nil {
				t.Fatal(err)
			}
			var ss *ServerStream
			for ss == nil || ss.getState() != streamReadDone {
				if ss == nil {
					ss = findServerStream(server, 1)
				}
				select {
				case <-ctx.Done():
					t.Fatal("timed out waiting for END_STREAM")
				case <-time.After(time.Millisecond):
				}
			}
			t.Logf("server stream backlog entries before reading: %d (for %d one-byte DATA frames)", backlogLen(&ss.Stream), numFrames)
			drainStream(t, &ss.Stream, want)

			recs := pool.chunks()
			un1024, unLarger, _ := logChunks(t, "server stream", recs)
			if len(recs) == 0 {
				t.Errorf("compaction acquired no chunk from ServerConfig.BufferPool")
			}
			if got := un1024 > 0; got != tc.wantUnreturned {
				t.Errorf("unreturned cap<=1024 chunks = %d, wantAny=%v", un1024, tc.wantUnreturned)
			}
			if unLarger != 0 {
				t.Errorf("unreturned larger chunks = %d, want 0", unLarger)
			}
		})
	}
}
