// Run: cd ~/repos/grpc-go && go test -v ./test -run '^TestVerify_EmptyUnaryRequestStatus$' -count=1
// Copy into the target branch's test/ directory and add -tags verify_repro to the go test command.

//go:build verify_repro

package test

import (
	"context"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/internal/stubserver"
	"google.golang.org/grpc/status"

	testpb "google.golang.org/grpc/interop/grpc_testing"
)

func TestVerify_EmptyUnaryRequestStatus(t *testing.T) {
	ss := &stubserver.StubServer{
		UnaryCallF: func(ctx context.Context, in *testpb.SimpleRequest) (*testpb.SimpleResponse, error) {
			return &testpb.SimpleResponse{}, nil
		},
	}
	if err := ss.Start(nil); err != nil {
		t.Fatalf("start: %v", err)
	}
	defer ss.Stop()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	desc := &grpc.StreamDesc{StreamName: "UnaryCall", ClientStreams: true, ServerStreams: false}
	cs, err := ss.CC.NewStream(ctx, desc, "/grpc.testing.TestService/UnaryCall")
	if err != nil {
		t.Fatalf("NewStream: %v", err)
	}
	if err := cs.CloseSend(); err != nil {
		t.Fatalf("CloseSend: %v", err)
	}
	m := new(testpb.SimpleResponse)
	err = cs.RecvMsg(m)
	st, _ := status.FromError(err)
	t.Logf("RecvMsg err=%v code=%v msg=%q", err, st.Code(), st.Message())
}
