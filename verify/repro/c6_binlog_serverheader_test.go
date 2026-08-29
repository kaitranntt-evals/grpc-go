//go:build ignore

package binarylog_test

// C6 repro: copy into the repo's `binarylog/` directory, delete the `//go:build ignore` line, then run `go test ./binarylog -run '^TestC6UnaryFailedSendServerHeader$' -v -count=1`.
import (
	"context"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/internal/stubserver"
	"google.golang.org/grpc/status"

	binlogpb "google.golang.org/grpc/binarylog/grpc_binarylog_v1"
	testgrpc "google.golang.org/grpc/interop/grpc_testing"
	testpb "google.golang.org/grpc/interop/grpc_testing"
)

func TestC6UnaryFailedSendServerHeader(t *testing.T) {
	defer testSink.clear()
	ss := &stubserver.StubServer{
		UnaryCallF: func(context.Context, *testpb.SimpleRequest) (*testpb.SimpleResponse, error) {
			// Response is larger than the server's max send size, so the
			// outbound send fails before any successful transport write.
			// No header is set.
			return &testpb.SimpleResponse{Payload: &testpb.Payload{Body: make([]byte, 1024)}}, nil
		},
	}
	if err := ss.Start([]grpc.ServerOption{grpc.MaxSendMsgSize(16)}); err != nil {
		t.Fatalf("Error starting endpoint server: %v", err)
	}
	defer ss.Stop()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	client := testgrpc.NewTestServiceClient(ss.CC)
	_, err := client.UnaryCall(ctx, &testpb.SimpleRequest{})
	if status.Code(err) != codes.ResourceExhausted {
		t.Fatalf("UnaryCall() = _, %v; want ResourceExhausted", err)
	}
	time.Sleep(300 * time.Millisecond)
	entries := testSink.logEntries(false) // server-side entries
	sawServerHeader := false
	for i, e := range entries {
		t.Logf("server binlog entry %d: %v", i, e.GetType())
		if e.GetType() == binlogpb.GrpcLogEntry_EVENT_TYPE_SERVER_HEADER {
			sawServerHeader = true
		}
	}
	if sawServerHeader {
		t.Log("RESULT: ServerHeader event WAS logged for failed unary send with empty header")
	} else {
		t.Log("RESULT: ServerHeader event OMITTED for failed unary send with empty header")
	}
}
