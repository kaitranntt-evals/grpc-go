// Run (on the audited branch, repo root): cp verify/repro/c2_stats_payload_method_test.go test/ && go test -tags verify_repro ./test -run 'Test/^Verify_C2_StatsPayloadMethod$' -count=1 -v
//
// Probe for C2: a stats.Handler records, for every server-side RPC stats
// callback, what grpc.Method(ctx) and grpc.ServerTransportStreamFromContext(ctx)
// return. For a unary RPC both InPayload and OutPayload must carry the method
// and the server transport stream.

//go:build verify_repro

package test

import (
	"context"
	"sync"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/internal/stubserver"
	"google.golang.org/grpc/stats"

	testgrpc "google.golang.org/grpc/interop/grpc_testing"
	testpb "google.golang.org/grpc/interop/grpc_testing"
)

type verifyC2StatsHandler struct {
	mu   sync.Mutex
	seen map[string]string // event type -> "<method>|<ok>|<hasServerTransportStream>"
}

func (h *verifyC2StatsHandler) TagRPC(ctx context.Context, _ *stats.RPCTagInfo) context.Context {
	return ctx
}
func (h *verifyC2StatsHandler) TagConn(ctx context.Context, _ *stats.ConnTagInfo) context.Context {
	return ctx
}
func (h *verifyC2StatsHandler) HandleConn(context.Context, stats.ConnStats) {}
func (h *verifyC2StatsHandler) HandleRPC(ctx context.Context, s stats.RPCStats) {
	var name string
	switch s.(type) {
	case *stats.InPayload:
		name = "InPayload"
	case *stats.OutPayload:
		name = "OutPayload"
	case *stats.Begin:
		name = "Begin"
	case *stats.End:
		name = "End"
	case *stats.InHeader:
		name = "InHeader"
	default:
		return
	}
	m, ok := grpc.Method(ctx)
	sts := grpc.ServerTransportStreamFromContext(ctx)
	h.mu.Lock()
	defer h.mu.Unlock()
	h.seen[name] = m + "|" + boolStr(ok) + "|" + boolStr(sts != nil)
}

func boolStr(b bool) string {
	if b {
		return "true"
	}
	return "false"
}

func (s) TestVerify_C2_StatsPayloadMethod(t *testing.T) {
	h := &verifyC2StatsHandler{seen: map[string]string{}}
	ss := &stubserver.StubServer{
		UnaryCallF: func(context.Context, *testpb.SimpleRequest) (*testpb.SimpleResponse, error) {
			return &testpb.SimpleResponse{}, nil
		},
		FullDuplexCallF: func(stream testgrpc.TestService_FullDuplexCallServer) error {
			if _, err := stream.Recv(); err != nil {
				return err
			}
			return stream.Send(&testpb.StreamingOutputCallResponse{})
		},
	}
	if err := ss.Start([]grpc.ServerOption{grpc.StatsHandler(h)}); err != nil {
		t.Fatalf("start: %v", err)
	}
	defer ss.Stop()
	ctx, cancel := context.WithTimeout(context.Background(), defaultTestTimeout)
	defer cancel()
	if _, err := ss.Client.UnaryCall(ctx, &testpb.SimpleRequest{}); err != nil {
		t.Fatalf("UnaryCall: %v", err)
	}
	h.mu.Lock()
	for _, ev := range []string{"InHeader", "Begin", "InPayload", "OutPayload", "End"} {
		t.Logf("unary %-10s grpc.Method(ctx) = method|ok|hasServerTransportStream = %s", ev, h.seen[ev])
	}
	for _, ev := range []string{"InPayload", "OutPayload"} {
		if h.seen[ev] != "/grpc.testing.TestService/UnaryCall|true|true" {
			t.Errorf("unary %s: grpc.Method unavailable / ServerTransportStream missing: %s", ev, h.seen[ev])
		}
	}
	h.mu.Unlock()

	// Streaming, for comparison only (not asserted).
	stream, err := ss.Client.FullDuplexCall(ctx)
	if err != nil {
		t.Fatalf("FullDuplexCall: %v", err)
	}
	if err := stream.Send(&testpb.StreamingOutputCallRequest{}); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if _, err := stream.Recv(); err != nil {
		t.Fatalf("Recv: %v", err)
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, ev := range []string{"InPayload", "OutPayload"} {
		t.Logf("stream %-10s grpc.Method(ctx) = method|ok|hasServerTransportStream = %s", ev, h.seen[ev])
	}
}
