// Run: copy verify/repro into the target worktree, then
// go test ./verify/repro -run 'TestC12' -v -count=1
// C12: registers unary and streaming descriptors and checks whether
// GetServiceInfo ever reports a streaming method before a unary method.
package repro

import (
	"context"
	"testing"

	"google.golang.org/grpc"
)

func TestC12GetServiceInfoOrdering(t *testing.T) {
	handler := func(any, grpc.ServerStream) error { return nil }
	unaryHandler := func(any, context.Context, func(any) error, grpc.UnaryServerInterceptor) (any, error) {
		return nil, nil
	}
	svcDesc := &grpc.ServiceDesc{
		ServiceName: "eval.OrderService",
		HandlerType: (*any)(nil),
		Methods: []grpc.MethodDesc{
			{MethodName: "U1", Handler: unaryHandler},
			{MethodName: "U2", Handler: unaryHandler},
			{MethodName: "U3", Handler: unaryHandler},
		},
		Streams: []grpc.StreamDesc{
			{StreamName: "S1", Handler: handler, ServerStreams: true},
			{StreamName: "S2", Handler: handler, ClientStreams: true},
			{StreamName: "S3", Handler: handler, ClientStreams: true, ServerStreams: true},
		},
	}
	s := grpc.NewServer()
	s.RegisterService(svcDesc, struct{}{})

	violations := 0
	for i := 0; i < 200; i++ {
		info := s.GetServiceInfo()
		methods := info["eval.OrderService"].Methods
		seenStream := false
		for _, m := range methods {
			isStream := m.IsClientStream || m.IsServerStream
			if isStream {
				seenStream = true
			} else if seenStream {
				violations++
				t.Logf("iteration %d: streaming descriptor precedes unary descriptor: %+v", i, methods)
				break
			}
		}
	}
	t.Logf("stream-before-unary orderings observed in 200 queries: %d", violations)
}
