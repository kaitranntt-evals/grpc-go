// Run (on evalon/grpc-go-se-97870765): cp verify/repro/c7_streaming_max_send_grpc_prefix_test.go encoding/ && go test -tags verifyrepro ./encoding -run '^TestC7Repro' -count=1 -v; rm encoding/c7_streaming_max_send_grpc_prefix_test.go
// The test FAILS when the problem is present (the streaming ResourceExhausted description carries the unary-only
// "grpc: " prefix) and PASSES on the base commit 0c51461d, where the streaming description has no prefix.

//go:build verifyrepro

package encoding_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/internal/stubserver"
	"google.golang.org/grpc/status"

	testgrpc "google.golang.org/grpc/interop/grpc_testing"
	testpb "google.golang.org/grpc/interop/grpc_testing"
)

// Probe: oversized *streaming* server response; print the ResourceExhausted
// description text so the prefix can be compared with the unary text.
func TestC7Repro_StreamingMaxSendSizeDescription(t *testing.T) {
	server := &stubserver.StubServer{
		FullDuplexCallF: func(stream testgrpc.TestService_FullDuplexCallServer) error {
			if _, err := stream.Recv(); err != nil {
				return err
			}
			return stream.Send(&testpb.StreamingOutputCallResponse{Payload: &testpb.Payload{Body: make([]byte, 1024)}})
		},
		UnaryCallF: func(context.Context, *testpb.SimpleRequest) (*testpb.SimpleResponse, error) {
			return &testpb.SimpleResponse{Payload: &testpb.Payload{Body: make([]byte, 1024)}}, nil
		},
	}
	if err := server.Start([]grpc.ServerOption{grpc.MaxSendMsgSize(16)}); err != nil {
		t.Fatalf("Start() failed: %v", err)
	}
	defer server.Stop()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	stream, err := server.Client.FullDuplexCall(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := stream.Send(&testpb.StreamingOutputCallRequest{}); err != nil {
		t.Fatal(err)
	}
	_, err = stream.Recv()
	streamingDesc := status.Convert(err).Message()
	t.Logf("C7: streaming code=%v desc=%q", status.Code(err), streamingDesc)
	if strings.HasPrefix(streamingDesc, "grpc: trying to send message larger than max") {
		t.Errorf("C7 CONFIRMED: streaming ResourceExhausted description uses the unary-only \"grpc:\" prefix: %q", streamingDesc)
	}
	_, err = server.Client.UnaryCall(ctx, &testpb.SimpleRequest{})
	t.Logf("C7: unary     code=%v desc=%q", status.Code(err), status.Convert(err).Message())
}
