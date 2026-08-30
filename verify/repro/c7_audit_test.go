// Audit probe for claim C7 on branch evalon/grpc-go-se-5cc804bc.
// Run: go test . -run TestAuditC7 -count=1 -v
package grpc_test

import (
	"context"
	"fmt"
	"testing"

	"google.golang.org/grpc"
)

func TestAuditC7_GetServiceInfoOrdering(t *testing.T) {
	desc := &grpc.ServiceDesc{
		ServiceName: "audit.C7",
		HandlerType: (*any)(nil),
		Methods: []grpc.MethodDesc{
			{MethodName: "U1", Handler: func(any, context.Context, func(any) error, grpc.UnaryServerInterceptor) (any, error) { return nil, nil }},
			{MethodName: "U2", Handler: func(any, context.Context, func(any) error, grpc.UnaryServerInterceptor) (any, error) { return nil, nil }},
			{MethodName: "U3", Handler: func(any, context.Context, func(any) error, grpc.UnaryServerInterceptor) (any, error) { return nil, nil }},
		},
		Streams: []grpc.StreamDesc{
			{StreamName: "S1", ServerStreams: true, Handler: func(any, grpc.ServerStream) error { return nil }},
			{StreamName: "S2", ClientStreams: true, Handler: func(any, grpc.ServerStream) error { return nil }},
			{StreamName: "S3", ClientStreams: true, ServerStreams: true, Handler: func(any, grpc.ServerStream) error { return nil }},
		},
	}
	srv := grpc.NewServer()
	srv.RegisterService(desc, struct{}{})
	defer srv.Stop()

	violations := 0
	var sample string
	for i := 0; i < 1000; i++ {
		info := srv.GetServiceInfo()["audit.C7"]
		seenStream := false
		for _, m := range info.Methods {
			isStream := m.IsClientStream || m.IsServerStream
			if isStream {
				seenStream = true
			} else if seenStream {
				violations++
				sample = fmt.Sprintf("%v", info.Methods)
				break
			}
		}
	}
	t.Logf("orderings with a streaming descriptor before a unary descriptor: %d/1000", violations)
	if violations > 0 {
		t.Logf("sample violating order: %s", sample)
	} else {
		t.Errorf("no violation observed in 1000 invocations; claim expects unordered output")
	}
}
