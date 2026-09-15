// Run (on evalon/grpc-go-se-315cd083, repo root): cp verify/repro/c7_stale_err_binding_test.go test/ && go test -tags verify_repro ./test -run 'Test/^(ServerUnaryRespondsBeforeHalfClose|Verify_C7_StaleErrBinding)$' -count=1 -v
//
// Probe for C7: replays TestServerUnaryRespondsBeforeHalfClose verbatim and,
// at the final `status.FromError(err)` line, logs which value `err` holds.
// If it is the (nil) NewStream error rather than the second RecvMsg result,
// the final status assertion is vacuous.

//go:build verify_repro

package test

import (
	"context"
	"io"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/internal/stubserver"
	"google.golang.org/grpc/status"

	testpb "google.golang.org/grpc/interop/grpc_testing"
)

func (s) TestVerify_C7_StaleErrBinding(t *testing.T) {
	ss := &stubserver.StubServer{
		UnaryCallF: func(_ context.Context, in *testpb.SimpleRequest) (*testpb.SimpleResponse, error) {
			return &testpb.SimpleResponse{Payload: &testpb.Payload{Body: make([]byte, in.GetResponseSize())}}, nil
		},
	}
	if err := ss.Start(nil); err != nil {
		t.Fatalf("Error starting endpoint server: %v", err)
	}
	defer ss.Stop()

	ctx, cancel := context.WithTimeout(context.Background(), defaultTestTimeout)
	defer cancel()

	desc := &grpc.StreamDesc{StreamName: "UnaryCall", ClientStreams: true, ServerStreams: false}
	stream, err := ss.CC.NewStream(ctx, desc, unaryCallMethod)
	newStreamErr := err
	if err != nil {
		t.Fatalf("NewStream(%q) = %v, want <nil>", unaryCallMethod, err)
	}
	if err := stream.SendMsg(&testpb.SimpleRequest{ResponseSize: 5}); err != nil {
		t.Fatalf("SendMsg() = %v, want <nil>", err)
	}
	resp := new(testpb.SimpleResponse)
	if err := stream.RecvMsg(resp); err != nil {
		t.Fatalf("RecvMsg() = %v, want <nil>; unary RPC did not respond before client half-close", err)
	}
	var secondRecvErr error
	if err := stream.RecvMsg(resp); err != io.EOF {
		t.Fatalf("second RecvMsg() = %v, want io.EOF", err)
	} else {
		secondRecvErr = err
	}
	// Same expression as server_test.go:968 in TestServerUnaryRespondsBeforeHalfClose.
	st, ok := status.FromError(err)
	t.Logf("at final check: err=%v  (err == NewStream err: %v; err == second RecvMsg err (io.EOF): %v)", err, err == newStreamErr, err == secondRecvErr)
	t.Logf("status.FromError(err) -> st=%v ok=%v code=%v", st, ok, st.Code())
	if err != newStreamErr || err == secondRecvErr {
		t.Fatalf("unexpected binding")
	}
	if ok && st.Code() != codes.OK {
		t.Fatalf("stream finished with status %v, want OK", st)
	}
	t.Errorf("final status.FromError(err) evaluates the stale NewStream error (nil), not the RPC completion error; the assertion cannot fail")
}
