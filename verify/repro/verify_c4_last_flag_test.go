// Run: cd <repo with VERIFY-C4 print in internal/transport/server_stream.go Write> && go test -v ./test -run '^TestVerify_UnaryWriteLastFlag$' -count=1
// Copy into the target branch's test/ directory and add -tags verify_repro to the go test command.

//go:build verify_repro

package test

import (
	"context"
	"io"
	"testing"
	"time"

	"google.golang.org/grpc/internal/stubserver"

	testgrpc "google.golang.org/grpc/interop/grpc_testing"
	testpb "google.golang.org/grpc/interop/grpc_testing"
)

func TestVerify_UnaryWriteLastFlag(t *testing.T) {
	ss := &stubserver.StubServer{
		UnaryCallF: func(ctx context.Context, in *testpb.SimpleRequest) (*testpb.SimpleResponse, error) {
			return &testpb.SimpleResponse{}, nil
		},
		StreamingOutputCallF: func(_ *testpb.StreamingOutputCallRequest, stream testgrpc.TestService_StreamingOutputCallServer) error {
			stream.Send(&testpb.StreamingOutputCallResponse{})
			return stream.Send(&testpb.StreamingOutputCallResponse{})
		},
	}
	if err := ss.Start(nil); err != nil {
		t.Fatalf("start: %v", err)
	}
	defer ss.Stop()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if _, err := ss.Client.UnaryCall(ctx, &testpb.SimpleRequest{}); err != nil {
		t.Fatalf("UnaryCall: %v", err)
	}
	s, err := ss.Client.StreamingOutputCall(ctx, &testpb.StreamingOutputCallRequest{})
	if err != nil {
		t.Fatalf("StreamingOutputCall: %v", err)
	}
	for {
		if _, err := s.Recv(); err == io.EOF {
			break
		} else if err != nil {
			t.Fatalf("Recv: %v", err)
		}
	}
}
