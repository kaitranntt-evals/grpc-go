// Run: cd <repo> && go test -v ./test -run '^TestVerify_SameNameCollisionDispatch$' -count=1
// Copy into the target branch's test/ directory and add -tags verify_repro to the go test command.

//go:build verify_repro

package test

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/internal/stubserver"

	testpb "google.golang.org/grpc/interop/grpc_testing"
)

func TestVerify_SameNameCollisionDispatch(t *testing.T) {
	var unaryHandler, streamHandler, unaryInt, streamInt atomic.Bool

	desc := &grpc.ServiceDesc{
		ServiceName: "verify.Collide",
		HandlerType: (*any)(nil),
		Methods: []grpc.MethodDesc{{
			MethodName: "Call",
			Handler: func(srv any, ctx context.Context, dec func(any) error, interceptor grpc.UnaryServerInterceptor) (any, error) {
				in := new(testpb.Empty)
				if err := dec(in); err != nil {
					return nil, err
				}
				h := func(ctx context.Context, req any) (any, error) {
					unaryHandler.Store(true)
					return &testpb.Empty{}, nil
				}
				if interceptor != nil {
					return interceptor(ctx, in, &grpc.UnaryServerInfo{FullMethod: "/verify.Collide/Call"}, h)
				}
				return h(ctx, in)
			},
		}},
		Streams: []grpc.StreamDesc{{
			StreamName: "Call",
			Handler: func(_ any, stream grpc.ServerStream) error {
				streamHandler.Store(true)
				if err := stream.RecvMsg(&testpb.Empty{}); err != nil {
					return err
				}
				return stream.SendMsg(&testpb.Empty{})
			},
		}},
	}

	ss := &stubserver.StubServer{
		EmptyCallF: func(context.Context, *testpb.Empty) (*testpb.Empty, error) { return &testpb.Empty{}, nil },
	}
	reg := stubserver.RegisterServiceServerOption(func(r grpc.ServiceRegistrar) {
		r.RegisterService(desc, struct{}{})
	})
	uInt := func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		unaryInt.Store(true)
		return handler(ctx, req)
	}
	sInt := func(srv any, stream grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
		streamInt.Store(true)
		return handler(srv, stream)
	}
	if err := ss.Start([]grpc.ServerOption{reg, grpc.UnaryInterceptor(uInt), grpc.StreamInterceptor(sInt)}); err != nil {
		t.Fatalf("start: %v", err)
	}
	defer ss.Stop()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := ss.CC.Invoke(ctx, "/verify.Collide/Call", &testpb.Empty{}, &testpb.Empty{}); err != nil {
		t.Fatalf("Invoke: %v", err)
	}
	t.Logf("unaryHandler=%v streamHandler=%v unaryInterceptor=%v streamInterceptor=%v",
		unaryHandler.Load(), streamHandler.Load(), unaryInt.Load(), streamInt.Load())
}
