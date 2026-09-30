// Run (after verify/repro/setup_worktrees.sh): cp verify/repro/c3_public_api_e2e_test.go /tmp/claims/d0520117/internal/transport/ && (cd /tmp/claims/d0520117 && go test ./internal/transport -run '^TestVerifyC3Public' -count=1 -v); rm /tmp/claims/d0520117/internal/transport/c3_public_api_e2e_test.go
package transport_test

import (
	"bytes"
	"encoding/binary"
	"net"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
	"unsafe"

	"golang.org/x/net/http2"
	"golang.org/x/net/http2/hpack"
	"google.golang.org/grpc"
	"google.golang.org/grpc/experimental"
	"google.golang.org/grpc/mem"
)

// pubPool tracks, by backing-array identity, every buffer that
// recvBuffer.newChunk takes from the server's configured pool.
type pubPool struct {
	inner mem.BufferPool
	mu    sync.Mutex
	recs  []*pubRec
	live  map[*byte]*pubRec
}

type pubRec struct {
	req, capacity int
	returned      bool
}

func (p *pubPool) Get(n int) *[]byte {
	h := p.inner.Get(n)
	pcs := make([]uintptr, 16)
	frames := runtime.CallersFrames(pcs[:runtime.Callers(2, pcs)])
	fromNewChunk := false
	for {
		f, more := frames.Next()
		if strings.HasSuffix(f.Function, "(*recvBuffer).newChunk") {
			fromNewChunk = true
		}
		if !more {
			break
		}
	}
	if fromNewChunk {
		rec := &pubRec{req: n, capacity: cap(*h)}
		p.mu.Lock()
		p.recs = append(p.recs, rec)
		p.live[unsafe.SliceData((*h)[:cap(*h)])] = rec
		p.mu.Unlock()
	}
	return h
}

func (p *pubPool) Put(h *[]byte) {
	p.mu.Lock()
	k := unsafe.SliceData((*h)[:cap(*h)])
	if rec := p.live[k]; rec != nil {
		rec.returned = true
		delete(p.live, k)
	}
	p.mu.Unlock()
	p.inner.Put(h)
}

func (p *pubPool) snapshot() []pubRec {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]pubRec, len(p.recs))
	for i, r := range p.recs {
		out[i] = *r
	}
	return out
}

type rawCodec struct{}

func (rawCodec) Marshal(v any) ([]byte, error) { return *(v.(*[]byte)), nil }
func (rawCodec) Unmarshal(data []byte, v any) error {
	*(v.(*[]byte)) = append([]byte(nil), data...)
	return nil
}
func (rawCodec) Name() string { return "raw" }

// A real grpc.Server configured through the public experimental.BufferPool
// server option; a raw HTTP/2 client fragments one gRPC message into one-byte
// DATA frames while the handler has not started reading; the handler then
// receives the whole message and returns.
func TestVerifyC3Public_GRPCServerWithConfiguredPool(t *testing.T) {
	for _, tc := range []struct {
		name           string
		inner          func() mem.BufferPool
		wantUnreturned bool
	}{
		{"mem.NewBinaryTieredBufferPool(8,10,12,14,15,20)", func() mem.BufferPool {
			p, err := mem.NewBinaryTieredBufferPool(8, 10, 12, 14, 15, 20)
			if err != nil {
				t.Fatal(err)
			}
			return p
		}, true},
		{"mem.NewTieredBufferPool(256,1024,4096,16384,32768,1048576)", func() mem.BufferPool {
			return mem.NewTieredBufferPool(256, 1024, 4096, 16384, 32768, 1048576)
		}, true},
		{"control: mem.DefaultBufferPool()", mem.DefaultBufferPool, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			pool := &pubPool{inner: tc.inner(), live: map[*byte]*pubRec{}}
			startRead := make(chan struct{})
			got := make(chan []byte, 1)
			handler := func(_ any, stream grpc.ServerStream) error {
				<-startRead
				var msg []byte
				if err := stream.RecvMsg(&msg); err != nil {
					return err
				}
				got <- msg
				return nil
			}
			srv := grpc.NewServer(experimental.BufferPool(pool), grpc.ForceServerCodec(rawCodec{}), grpc.UnknownServiceHandler(handler))
			lis, err := net.Listen("tcp", "localhost:0")
			if err != nil {
				t.Fatal(err)
			}
			go srv.Serve(lis)
			defer srv.Stop()

			conn, err := (&net.Dialer{Timeout: 5 * time.Second}).Dial("tcp", lis.Addr().String())
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			conn.SetDeadline(time.Now().Add(20 * time.Second))
			if _, err := conn.Write([]byte(http2.ClientPreface)); err != nil {
				t.Fatal(err)
			}
			framer := http2.NewFramer(conn, conn)
			framer.WriteSettings()
			trailers := make(chan struct{})
			go func() {
				for {
					f, err := framer.ReadFrame()
					if err != nil {
						return
					}
					if hf, ok := f.(*http2.HeadersFrame); ok && hf.StreamEnded() {
						close(trailers)
						return
					}
				}
			}()
			var hbuf bytes.Buffer
			henc := hpack.NewEncoder(&hbuf)
			for _, f := range []hpack.HeaderField{
				{Name: ":method", Value: "POST"},
				{Name: ":scheme", Value: "http"},
				{Name: ":path", Value: "/verify.Svc/Tiny"},
				{Name: ":authority", Value: "localhost"},
				{Name: "content-type", Value: "application/grpc"},
				{Name: "te", Value: "trailers"},
			} {
				henc.WriteField(f)
			}
			if err := framer.WriteHeaders(http2.HeadersFrameParam{StreamID: 1, BlockFragment: hbuf.Bytes(), EndHeaders: true}); err != nil {
				t.Fatal(err)
			}
			const payloadLen = 600
			payload := make([]byte, payloadLen)
			for i := range payload {
				payload[i] = byte(i)
			}
			msg := make([]byte, 5, 5+payloadLen)
			binary.BigEndian.PutUint32(msg[1:], payloadLen)
			msg = append(msg, payload...)
			for i := range msg {
				if err := framer.WriteData(1, i == len(msg)-1, msg[i:i+1]); err != nil {
					t.Fatal(err)
				}
			}
			// Let the server transport queue every frame before the handler reads.
			deadline := time.Now().Add(5 * time.Second)
			for len(pool.snapshot()) == 0 && time.Now().Before(deadline) {
				time.Sleep(time.Millisecond)
			}
			time.Sleep(200 * time.Millisecond)
			close(startRead)
			select {
			case m := <-got:
				if !bytes.Equal(m, payload) {
					t.Fatalf("handler received %d bytes, payload mismatch", len(m))
				}
			case <-time.After(10 * time.Second):
				t.Fatal("handler did not receive the message")
			}
			select {
			case <-trailers:
			case <-time.After(10 * time.Second):
				t.Fatal("no trailers from server")
			}
			srv.Stop() // every stream and transport is torn down

			recs := pool.snapshot()
			unreturned := 0
			for i, r := range recs {
				t.Logf("newChunk acquisition #%d: Get(%d) -> cap=%d returnedToPool=%v (after message delivered, RPC finished, server stopped)", i, r.req, r.capacity, r.returned)
				if !r.returned {
					unreturned++
				}
			}
			if len(recs) == 0 {
				t.Fatalf("compaction never acquired a chunk from the configured pool")
			}
			if (unreturned > 0) != tc.wantUnreturned {
				t.Errorf("unreturned newChunk allocations = %d, wantAny=%v", unreturned, tc.wantUnreturned)
			}
		})
	}
}
