// Run (on evalon/grpc-go-se-cb95c9da, repo root): cp verify/repro/c3_tagrpc_fresh_ctx_panic_test.go test/ && go test -tags verify_repro ./test -run 'Test/^Verify_C3_TagRPCFreshContextEncodeFailure$' -count=1 -v
//
// Probe for C3: a stats.Handler whose TagRPC returns a fresh non-nil context
// combined with a server codec whose Marshal fails. Expected on a healthy
// server: the client sees codes.Internal. Suspected: the server goroutine
// panics in serverStream.SendMsg dereferencing serverFromContext(ss.ctx).

//go:build verify_repro

package test

import (
	"context"
	"errors"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/encoding"
	"google.golang.org/grpc/internal/grpctest"
	"google.golang.org/grpc/internal/stubserver"
	"google.golang.org/grpc/mem"
	"google.golang.org/grpc/stats"
	"google.golang.org/grpc/status"

	testpb "google.golang.org/grpc/interop/grpc_testing"
)

type verifyC3FreshCtxStatsHandler struct{}

func (verifyC3FreshCtxStatsHandler) TagRPC(context.Context, *stats.RPCTagInfo) context.Context {
	return context.Background() // fresh, non-nil, does not derive from the input
}
func (verifyC3FreshCtxStatsHandler) HandleRPC(context.Context, stats.RPCStats) {}
func (verifyC3FreshCtxStatsHandler) TagConn(ctx context.Context, _ *stats.ConnTagInfo) context.Context {
	return ctx
}
func (verifyC3FreshCtxStatsHandler) HandleConn(context.Context, stats.ConnStats) {}

type verifyC3FailEncodeCodec struct{ encoding.CodecV2 }

func (verifyC3FailEncodeCodec) Name() string { return "verify_c3_fail_encode" }
func (verifyC3FailEncodeCodec) Marshal(any) (mem.BufferSlice, error) {
	return nil, errors.New("verify: synthetic marshal failure")
}

func (s) TestVerify_C3_TagRPCFreshContextEncodeFailure(t *testing.T) {
	grpctest.ExpectError("grpc: server failed to encode response")
	ss := &stubserver.StubServer{
		UnaryCallF: func(context.Context, *testpb.SimpleRequest) (*testpb.SimpleResponse, error) {
			return &testpb.SimpleResponse{}, nil
		},
	}
	sopts := []grpc.ServerOption{
		grpc.StatsHandler(verifyC3FreshCtxStatsHandler{}),
		grpc.ForceServerCodecV2(verifyC3FailEncodeCodec{encoding.GetCodecV2("proto")}),
	}
	if err := ss.Start(sopts); err != nil {
		t.Fatalf("start: %v", err)
	}
	defer ss.Stop()
	ctx, cancel := context.WithTimeout(context.Background(), defaultTestTimeout)
	defer cancel()
	_, err := ss.Client.UnaryCall(ctx, &testpb.SimpleRequest{})
	t.Logf("UnaryCall error: %v", err)
	if status.Code(err) != codes.Internal {
		t.Errorf("UnaryCall status code = %v, want %v", status.Code(err), codes.Internal)
	}
}
