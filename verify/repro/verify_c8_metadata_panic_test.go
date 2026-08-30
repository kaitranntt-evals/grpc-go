// Run: cd <repo> && go test -v ./test -run '^Test$/^Verify_UnaryMetadataAssertionKeyAbsent$' -count=1
// Copy into the target branch's test/ directory and add -tags verify_repro to the go test command.

//go:build verify_repro

// Exercises the exact assertion expression from TestServerUnaryContextAndMetadata
// (test/server_unified_rpc_test.go) with incoming metadata present but key "k" absent.
package test

import (
	"context"
	"testing"

	"google.golang.org/grpc/internal/stubserver"
	"google.golang.org/grpc/metadata"

	testpb "google.golang.org/grpc/interop/grpc_testing"
)

func (s) TestVerify_UnaryMetadataAssertionKeyAbsent(t *testing.T) {
	ss := &stubserver.StubServer{
		EmptyCallF: func(ctx context.Context, _ *testpb.Empty) (*testpb.Empty, error) {
			md, ok := metadata.FromIncomingContext(ctx)
			// Identical expression to server_unified_rpc_test.go:212.
			if !ok || md.Get("k")[0] != "v" {
				t.Errorf("Incoming metadata = %v, want it to contain k:v", md)
			}
			return &testpb.Empty{}, nil
		},
	}
	if err := ss.Start(nil); err != nil {
		t.Fatalf("Error starting server: %v", err)
	}
	defer ss.Stop()

	ctx, cancel := context.WithTimeout(context.Background(), defaultTestTimeout)
	defer cancel()
	// Metadata present, but key "k" absent.
	ctx = metadata.AppendToOutgoingContext(ctx, "other", "v")
	if _, err := ss.Client.EmptyCall(ctx, &testpb.Empty{}); err != nil {
		t.Fatalf("EmptyCall() failed: %v", err)
	}
}
