// Run: cp verify/repro/c2_c5_handler_backing_retention_test.go internal/transport/zz_verify_c2_c5_test.go && go test -v -count=1 -run '^TestVerifyC2C5' ./internal/transport ; rm internal/transport/zz_verify_c2_c5_test.go

package transport

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"runtime"
	"testing"
	"time"
	"unsafe"

	"golang.org/x/net/http2"
	"golang.org/x/net/http2/hpack"
	"google.golang.org/grpc/mem"
)

// verifyQueued is a snapshot of everything a recvBuffer holds for the reader:
// the message parked in the delivery channel plus the backlog.
type verifyQueued struct {
	msgs         int // data messages queued (channel + backlog)
	backlogLen   int
	payload      int // sum of Len()
	distinct     int // distinct backing arrays
	distinctCap  int // sum of cap() over distinct backing arrays
	suffixLen    int // recvBuffer.uncompactedSuffixLen
	suffixBytes  int // recvBuffer.uncompactedBytes
	payloadBytes []byte
}

// verifySnapshot inspects b without consuming it (the channel message is put
// back).
func verifySnapshot(b *recvBuffer) verifyQueued {
	b.mu.Lock()
	defer b.mu.Unlock()
	var q verifyQueued
	seen := map[uintptr]int{}
	add := func(m recvMsg) {
		if m.buffer == nil {
			return
		}
		d := m.buffer.ReadOnlyData()
		q.msgs++
		q.payload += len(d)
		q.payloadBytes = append(q.payloadBytes, d...)
		p := uintptr(unsafe.Pointer(unsafe.SliceData(d)))
		if cap(d) > seen[p] {
			seen[p] = cap(d)
		}
	}
	select {
	case m := <-b.c:
		add(m)
		b.c <- m
	default:
	}
	for _, m := range b.backlog {
		add(m)
	}
	q.backlogLen = len(b.backlog)
	q.distinct = len(seen)
	for _, c := range seen {
		q.distinctCap += c
	}
	q.suffixLen, q.suffixBytes = b.uncompactedSuffixLen, b.uncompactedBytes
	return q
}

func verifyWaitQueued(t *testing.T, b *recvBuffer, wantPayload int) verifyQueued {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		q := verifySnapshot(b)
		if q.payload >= wantPayload {
			return q
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %d queued payload bytes, have %d", wantPayload, q.payload)
		}
		time.Sleep(50 * time.Microsecond)
	}
}

func verifyHeapAlloc() int64 {
	runtime.GC()
	runtime.GC()
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	return int64(ms.HeapAlloc)
}

// TestVerifyC2C5_HandleStreamsPipeBody drives the delivered
// serverHandlerTransport.HandleStreams reader goroutine with a request body
// that yields `reads` consecutive short reads of `size` bytes, never consumes
// the stream, and measures what the stream's recvBuffer retains.
func TestVerifyC2C5_HandleStreamsPipeBody(t *testing.T) {
	for _, tc := range []struct{ size, reads int }{
		{64, 512}, {100, 512}, {128, 512}, {57, 512}, {56, 2048}, {1, 512}, {1, 2048},
	} {
		t.Run(fmt.Sprintf("size=%d/reads=%d", tc.size, tc.reads), func(t *testing.T) {
			st := newHandleStreamTest(t, nil)
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			streamCh := make(chan *ServerStream, 1)
			hsDone := make(chan struct{})
			before := verifyHeapAlloc()
			go func() {
				defer close(hsDone)
				st.ht.HandleStreams(ctx, func(s *ServerStream) { streamCh <- s })
			}()
			s := <-streamCh

			var want []byte
			states := map[string]int{}
			maxDistinct, maxDistinctCap := 0, 0
			for i := 0; i < tc.reads; i++ {
				p := bytes.Repeat([]byte{byte(i%251 + 1)}, tc.size)
				want = append(want, p...)
				// io.Pipe hands each Write to exactly one Body.Read here
				// (the 16 KiB read buffer is larger than the write).
				if _, err := st.bodyw.Write(p); err != nil {
					t.Fatalf("body write %d: %v", i, err)
				}
				q := verifyWaitQueued(t, &s.buf, len(want))
				if i > 0 { // message 0 goes straight to the channel
					states[fmt.Sprintf("suffixLen=%d,suffixBytes=%d", q.suffixLen, q.suffixBytes)]++
				}
				if q.distinct > maxDistinct {
					maxDistinct, maxDistinctCap = q.distinct, q.distinctCap
				}
			}
			q := verifySnapshot(&s.buf)
			after := verifyHeapAlloc()
			if !bytes.Equal(q.payloadBytes, want) {
				t.Errorf("queued payload differs from written payload")
			}
			resets := states["suffixLen=0,suffixBytes=0"]
			t.Logf("RESULT size=%d reads=%d: queued msgs=%d (backlog=%d + channel=%d) payload=%d B | distinct backing allocations=%d, total backing capacity=%d B (%.1f KiB, %.0fx payload) | peak during run: %d allocations / %d B | puts that left tracking reset (suffixLen=0,bytes=0): %d of %d backlog puts | HeapAlloc delta=%d B",
				tc.size, tc.reads, q.msgs, q.backlogLen, q.msgs-q.backlogLen, q.payload, q.distinct, q.distinctCap, float64(q.distinctCap)/1024, float64(q.distinctCap)/float64(q.payload), maxDistinct, maxDistinctCap, resets, tc.reads-1, after-before)
			runtime.KeepAlive(s)
			st.bodyw.Close()
			st.ht.Close(nil)
			<-hsDone
		})
	}
}

// TestVerifyC2C5_CapacityIgnoredByAccounting feeds a bare recvBuffer the same
// 64-byte payload lengths twice: once backed by exact 64-byte slices and once
// backed by 16 KiB pooled allocations. Identical tracking state and backlog
// shape shows that backing capacity plays no part in the compaction decision.
func TestVerifyC2C5_CapacityIgnoredByAccounting(t *testing.T) {
	pool := mem.DefaultBufferPool()
	run := func(pooled16k bool) (trace string, q verifyQueued) {
		b := &recvBuffer{}
		b.init(pool)
		for i := 0; i < 512; i++ {
			var buf mem.Buffer
			if pooled16k {
				h := pool.Get(http2MaxFrameLen)
				*h = (*h)[:64]
				buf = mem.NewBuffer(h, pool)
			} else {
				buf = mem.SliceBuffer(make([]byte, 64))
			}
			b.put(recvMsg{buffer: buf})
			if i < 4 || i == 511 {
				trace += fmt.Sprintf("[put#%d backlog=%d suffixLen=%d suffixBytes=%d] ", i, len(b.backlog), b.uncompactedSuffixLen, b.uncompactedBytes)
			}
		}
		return trace, verifySnapshot(b)
	}
	t.Logf("recvMsgSize=%d utilizationFactor=%d compactionThreshold=%d  => for a 64-byte put: backlogHeapSize=%d, utilizationFactor*bytes=%d, reset=%v",
		recvMsgSize, utilizationFactor, compactionThreshold, recvMsgSize+64, utilizationFactor*64, recvMsgSize+64 <= utilizationFactor*64)
	for _, pooled := range []bool{false, true} {
		trace, q := run(pooled)
		t.Logf("RESULT pooled16KiB=%v: msgs=%d backlog=%d payload=%d distinct=%d backingCap=%d trace: %s", pooled, q.msgs, q.backlogLen, q.payload, q.distinct, q.distinctCap, trace)
	}
}

// TestVerifyC2C5_RealHTTP2Handler runs the same workload through a real
// golang.org/x/net/http2 server whose handler is the delivered
// serverHandlerTransport: a raw HTTP/2 client sends 512 legal 64-byte DATA
// frames, the gRPC stream is never read.
func TestVerifyC2C5_RealHTTP2Handler(t *testing.T) {
	const size, frames = 64, 512
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer lis.Close()
	streamCh := make(chan *ServerStream, 1)
	release := make(chan struct{})
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tr, err := NewServerHandlerTransport(w, r, nil, mem.DefaultBufferPool())
		if err != nil {
			t.Errorf("NewServerHandlerTransport: %v", err)
			return
		}
		go func() { <-release; tr.Close(nil) }()
		tr.HandleStreams(context.Background(), func(s *ServerStream) { streamCh <- s })
	})
	go func() {
		conn, err := lis.Accept()
		if err != nil {
			return
		}
		(&http2.Server{}).ServeConn(conn, &http2.ServeConnOpts{Handler: handler})
	}()

	conn, err := net.Dial("tcp", lis.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	go io.Copy(io.Discard, conn) // ignore server frames
	if _, err := conn.Write([]byte(http2.ClientPreface)); err != nil {
		t.Fatal(err)
	}
	fr := http2.NewFramer(conn, nil)
	if err := fr.WriteSettings(); err != nil {
		t.Fatal(err)
	}
	var hb bytes.Buffer
	enc := hpack.NewEncoder(&hb)
	for _, f := range [][2]string{{":method", "POST"}, {":scheme", "http"}, {":path", "/svc/Method"}, {":authority", "localhost"}, {"content-type", "application/grpc"}, {"te", "trailers"}} {
		enc.WriteField(hpack.HeaderField{Name: f[0], Value: f[1]})
	}
	if err := fr.WriteHeaders(http2.HeadersFrameParam{StreamID: 1, BlockFragment: hb.Bytes(), EndHeaders: true}); err != nil {
		t.Fatal(err)
	}
	var s *ServerStream
	select {
	case s = <-streamCh:
	case <-time.After(10 * time.Second):
		t.Fatal("timed out waiting for handler stream")
	}
	total := 0
	for i := 0; i < frames; i++ {
		if err := fr.WriteData(1, false, bytes.Repeat([]byte{byte(i%251 + 1)}, size)); err != nil {
			t.Fatalf("WriteData %d: %v", i, err)
		}
		total += size
		verifyWaitQueued(t, &s.buf, total) // pace: one DATA frame per Body.Read
	}
	q := verifySnapshot(&s.buf)
	t.Logf("RESULT real net/http2 handler, %d DATA frames x %d B: queued msgs=%d (backlog=%d) payload=%d B | distinct backing allocations=%d, total backing capacity=%d B (%.1f KiB, %.0fx payload) | suffixLen=%d suffixBytes=%d",
		frames, size, q.msgs, q.backlogLen, q.payload, q.distinct, q.distinctCap, float64(q.distinctCap)/1024, float64(q.distinctCap)/float64(q.payload), q.suffixLen, q.suffixBytes)
	close(release)
}
