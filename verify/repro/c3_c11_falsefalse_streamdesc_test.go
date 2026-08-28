// Run: cd ~/repos/grpc-go && go test ./verify/repro -run 'TestC3C11' -v -count=1
// C3/C11: registers a genuine StreamDesc with ClientStreams=false and
// ServerStreams=false alongside a stream interceptor, invokes it, and reports
// whether the stream interceptor wrapped the handler.
package repro

import (
	"context"
	"net"
	"sync/atomic"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	testpb "google.golang.org/grpc/interop/grpc_testing"
)

func TestC3C11FalseFalseStreamDescBypassesStreamInterceptor(t *testing.T) {
	var streamIntCalled, handlerCalled atomic.Int32

	handler := func(_ any, stream grpc.ServerStream) error {
		handlerCalled.Add(1)
		req := new(testpb.Empty)
		if err := stream.RecvMsg(req); err != nil {
			return err
		}
		return stream.SendMsg(&testpb.Empty{})
	}

	svcDesc := &grpc.ServiceDesc{
		ServiceName: "eval.FalseFalseService",
		HandlerType: (*any)(nil),
		Streams: []grpc.StreamDesc{{
			StreamName:    "Call",
			Handler:       handler,
			ClientStreams: false,
			ServerStreams: false,
		}},
	}

	streamInt := func(srv any, ss grpc.ServerStream, info *grpc.StreamServerInfo, h grpc.StreamHandler) error {
		streamIntCalled.Add(1)
		return h(srv, ss)
	}

	s := grpc.NewServer(grpc.StreamInterceptor(streamInt))
	s.RegisterService(svcDesc, struct{}{})
	lis, err := net.Listen("tcp", "localhost:0")
	if err != nil {
		t.Fatal(err)
	}
	go s.Serve(lis)
	defer s.Stop()

	cc, err := grpc.NewClient(lis.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	defer cc.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	clientDesc := &grpc.StreamDesc{StreamName: "Call", ClientStreams: false, ServerStreams: false}
	cs, err := cc.NewStream(ctx, clientDesc, "/eval.FalseFalseService/Call")
	if err != nil {
		t.Fatalf("NewStream failed: %v", err)
	}
	if err := cs.SendMsg(&testpb.Empty{}); err != nil {
		t.Fatalf("SendMsg failed: %v", err)
	}
	if err := cs.CloseSend(); err != nil {
		t.Fatalf("CloseSend failed: %v", err)
	}
	resp := new(testpb.Empty)
	if err := cs.RecvMsg(resp); err != nil {
		t.Fatalf("RecvMsg failed: %v", err)
	}

	t.Logf("handler called: %d, stream interceptor called: %d", handlerCalled.Load(), streamIntCalled.Load())
	if handlerCalled.Load() == 1 && streamIntCalled.Load() == 0 {
		t.Log("RESULT: false-false StreamDesc handler ran WITHOUT the stream interceptor (claim behavior observed)")
	}
	if streamIntCalled.Load() >= 1 {
		t.Log("RESULT: stream interceptor wrapped the false-false StreamDesc handler (claim behavior refuted)")
	}
}
