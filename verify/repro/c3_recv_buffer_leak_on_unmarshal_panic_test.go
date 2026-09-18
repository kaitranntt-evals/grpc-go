// Run: cp verify/repro/c3_recv_buffer_leak_on_unmarshal_panic_test.go test/ && go test ./test -run 'Test/VerifyC3_' -tags verify_repro -count=1 -race -v ; rm test/c3_recv_buffer_leak_on_unmarshal_panic_test.go
//
// A bidirectional streaming RPC is served with a codec whose Unmarshal panics
// and a stream interceptor that recovers the panic and returns an Internal
// status.  A counting mem.BufferPool is installed on the server; every buffer
// the transport takes from the pool for the request message must be returned
// (Put) once the RPC is over.  A control run with a codec whose Unmarshal
// returns a plain error shows the expected balanced accounting.
//go:build verify_repro

package test

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/encoding"
	"google.golang.org/grpc/encoding/proto"
	"google.golang.org/grpc/experimental"
	"google.golang.org/grpc/internal/stubserver"
	"google.golang.org/grpc/mem"
	"google.golang.org/grpc/status"

	testgrpc "google.golang.org/grpc/interop/grpc_testing"
	testpb "google.golang.org/grpc/interop/grpc_testing"
)

// countingPool counts Get/Put calls made against it by the server.
type countingPool struct {
	mu   sync.Mutex
	gets int
	puts int
	pool mem.BufferPool
}

func (p *countingPool) Get(length int) *[]byte {
	p.mu.Lock()
	p.gets++
	p.mu.Unlock()
	return p.pool.Get(length)
}

func (p *countingPool) Put(b *[]byte) {
	p.mu.Lock()
	p.puts++
	p.mu.Unlock()
	p.pool.Put(b)
}

func (p *countingPool) counts() (int, int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.gets, p.puts
}

// faultyCodec is a proto codec whose Unmarshal either panics or fails.
type faultyCodec struct {
	encoding.CodecV2
	panics bool
}

func (c *faultyCodec) Unmarshal(mem.BufferSlice, any) error {
	if c.panics {
		panic("verify: codec.Unmarshal panics")
	}
	return fmt.Errorf("verify: codec.Unmarshal fails")
}

func (c *faultyCodec) Name() string { return "proto" }

func runRecvFault(t *testing.T, panics bool) (gets, puts int, rpcErr error) {
	t.Helper()
	pool := &countingPool{pool: mem.DefaultBufferPool()}
	reached := make(chan struct{}, 1)
	codec := &faultyCodec{CodecV2: encoding.GetCodecV2(proto.Name), panics: panics}

	ss := &stubserver.StubServer{
		FullDuplexCallF: func(stream testgrpc.TestService_FullDuplexCallServer) error {
			reached <- struct{}{}
			_, err := stream.Recv() // -> serverStream.RecvMsg -> codec.Unmarshal
			return err
		},
	}
	sopts := []grpc.ServerOption{
		experimental.BufferPool(pool),
		grpc.ForceServerCodecV2(codec),
		grpc.StreamInterceptor(func(srv any, st grpc.ServerStream, _ *grpc.StreamServerInfo, handler grpc.StreamHandler) (err error) {
			defer func() {
				if r := recover(); r != nil {
					err = status.Errorf(codes.Internal, "recovered: %v", r)
				}
			}()
			return handler(srv, st)
		}),
	}
	if err := ss.Start(sopts); err != nil {
		t.Fatalf("Error starting server: %v", err)
	}
	defer ss.Stop()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	stream, err := ss.Client.FullDuplexCall(ctx)
	if err != nil {
		t.Fatalf("FullDuplexCall() failed: %v", err)
	}
	// > 1 KiB so the transport takes the request bytes from the buffer pool.
	if err := stream.Send(&testpb.StreamingOutputCallRequest{Payload: &testpb.Payload{Body: make([]byte, 8192)}}); err != nil {
		t.Fatalf("Send() failed: %v", err)
	}
	_, rpcErr = stream.Recv()
	select {
	case <-reached:
	case <-time.After(5 * time.Second):
		t.Fatalf("streaming handler was never reached")
	}
	ss.Stop()
	time.Sleep(200 * time.Millisecond) // let transport teardown finish any deferred frees
	gets, puts = pool.counts()
	return gets, puts, rpcErr
}

func (s) TestVerifyC3_RecvBufferReleasedWhenUnmarshalFails_Control(t *testing.T) {
	gets, puts, err := runRecvFault(t, false)
	t.Logf("control (Unmarshal returns error): status=%v msg=%q pool gets=%d puts=%d", status.Code(err), status.Convert(err).Message(), gets, puts)
	if status.Code(err) != codes.Internal {
		t.Errorf("RPC error = %v, want code Internal", err)
	}
	if gets != puts {
		t.Errorf("control run: pool gets=%d puts=%d, want balanced", gets, puts)
	}
}

func (s) TestVerifyC3_RecvBufferLeakWhenUnmarshalPanics(t *testing.T) {
	gets, puts, err := runRecvFault(t, true)
	t.Logf("panic run (Unmarshal panics, interceptor recovers): status=%v msg=%q pool gets=%d puts=%d", status.Code(err), status.Convert(err).Message(), gets, puts)
	if status.Code(err) != codes.Internal || status.Convert(err).Message() != "recovered: verify: codec.Unmarshal panics" {
		t.Errorf("recoverable_streaming_rpc_path: RPC error = %v, want Internal status produced by the recovering interceptor", err)
	}
	if gets != puts {
		t.Errorf("receive_buffer_release: %d buffer(s) taken from the pool were never returned after codec.Unmarshal panicked (gets=%d puts=%d)", gets-puts, gets, puts)
	}
}
