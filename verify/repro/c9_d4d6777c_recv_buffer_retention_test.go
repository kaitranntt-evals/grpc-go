// Run: cp verify/repro/c9_d4d6777c_recv_buffer_retention_test.go <worktree of evalon/grpc-go-se-d4d6777c>/c9_verify_ext_test.go && cd <worktree> && go test -count=1 -v . -run '^TestC9_' ; rm c9_verify_ext_test.go
//
// Registers a handwritten non-client-streaming StreamDesc (ServerStreams only) whose handler calls
// RecvMsg once. The client opens the stream with ClientStreams:true so the request side stays OPEN
// after the single 64 KiB message. A tracking mem.BufferPool counts pooled buffers handed out vs.
// returned. Expected observation on this branch: while the handler is blocked inside RecvMsg's
// cardinality lookahead (request side open, no stats handler, no binlog, default proto codec), the
// decoded request buffer is still outstanding (Get-Put > 0); it is returned only after CloseSend.

package grpc_test

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/experimental"
	"google.golang.org/grpc/internal/testutils"
	"google.golang.org/grpc/mem"

	testpb "google.golang.org/grpc/interop/grpc_testing"
)

type c9TrackingPool struct {
	inner mem.BufferPool
	gets  atomic.Int64
	puts  atomic.Int64
}

func (p *c9TrackingPool) Get(n int) *[]byte  { p.gets.Add(1); return p.inner.Get(n) }
func (p *c9TrackingPool) Put(b *[]byte)      { p.puts.Add(1); p.inner.Put(b) }
func (p *c9TrackingPool) outstanding() int64 { return p.gets.Load() - p.puts.Load() }

func TestC9_RecvMsgRetainsBufferDuringLookahead(t *testing.T) {
	pool := &c9TrackingPool{inner: mem.DefaultBufferPool()}
	recvReturned := make(chan struct{})
	var once sync.Once
	sd := &grpc.ServiceDesc{
		ServiceName: "c9.Service",
		HandlerType: (*any)(nil),
		Streams: []grpc.StreamDesc{{
			StreamName:    "ServerStream",
			ServerStreams: true, // ClientStreams=false => non-client-streaming registration
			Handler: func(_ any, stream grpc.ServerStream) error {
				req := &testpb.SimpleRequest{}
				err := stream.RecvMsg(req)
				once.Do(func() { close(recvReturned) })
				if err != nil {
					return err
				}
				return stream.SendMsg(&testpb.SimpleResponse{Payload: &testpb.Payload{Body: []byte("done")}})
			},
		}},
	}
	srv := grpc.NewServer(experimental.BufferPool(pool))
	srv.RegisterService(sd, struct{}{})
	lis, err := testutils.LocalTCPListener()
	if err != nil {
		t.Fatal(err)
	}
	go srv.Serve(lis)
	defer srv.Stop()

	cc, err := grpc.NewClient(lis.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	defer cc.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	// ClientStreams:true on the client side so SendMsg does NOT half-close.
	stream, err := cc.NewStream(ctx, &grpc.StreamDesc{StreamName: "ServerStream", ClientStreams: true, ServerStreams: true}, "/c9.Service/ServerStream")
	if err != nil {
		t.Fatal(err)
	}
	// 64 KiB body: well above the 1 KiB pooling threshold so the decoded request lives in pooled buffers.
	if err := stream.SendMsg(&testpb.SimpleRequest{Payload: &testpb.Payload{Body: make([]byte, 64<<10)}}); err != nil {
		t.Fatal(err)
	}

	// Give the server time to read + unmarshal the request. Then observe.
	time.Sleep(1500 * time.Millisecond)
	select {
	case <-recvReturned:
		t.Logf("C9PROBE RecvMsg RETURNED while request side still open (no lookahead wait)")
	default:
		t.Logf("C9PROBE RecvMsg still BLOCKED after 1.5s with request side open (cardinality lookahead)")
	}
	t.Logf("C9PROBE pooled buffers while blocked: gets=%d puts=%d outstanding=%d", pool.gets.Load(), pool.puts.Load(), pool.outstanding())

	if err := stream.CloseSend(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-recvReturned:
	case <-ctx.Done():
		t.Fatal("RecvMsg never returned after CloseSend")
	}
	resp := &testpb.SimpleResponse{}
	if err := stream.RecvMsg(resp); err != nil {
		t.Fatalf("RecvMsg(resp) = %v", err)
	}
	time.Sleep(200 * time.Millisecond)
	t.Logf("C9PROBE pooled buffers after CloseSend+response: gets=%d puts=%d outstanding=%d", pool.gets.Load(), pool.puts.Load(), pool.outstanding())
}
