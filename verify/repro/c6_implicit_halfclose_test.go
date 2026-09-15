// Run (on evalon/grpc-go-se-32eb5f50, repo root): cp verify/repro/c6_implicit_halfclose_test.go test/ && go test -tags verify_repro ./test -run 'Test/^Verify_C6_' -count=1 -v
//
// Probe for C6: replays the exact client flow of
// TestServerPipeline_UnaryRespondsWithoutHalfClose (StreamDesc without
// ClientStreams, one SendMsg, no CloseSend) against a UnknownServiceHandler
// stream handler that reports whether the server already observed the client
// half-close (io.EOF on a second RecvMsg) before it responds. If the server
// sees EOF, the regression test's request send implicitly half-closed the
// stream and the test does not exercise "unary responds before half-close".

//go:build verify_repro

package test

import (
	"context"
	"io"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/internal/stubserver"

	testpb "google.golang.org/grpc/interop/grpc_testing"
)

func verifyC6Run(t *testing.T, desc *grpc.StreamDesc) (serverSawEOF bool) {
	sawEOF := make(chan bool, 1)
	ss := &stubserver.StubServer{}
	sopts := []grpc.ServerOption{grpc.UnknownServiceHandler(func(_ any, stream grpc.ServerStream) error {
		req := &testpb.SimpleRequest{}
		if err := stream.RecvMsg(req); err != nil {
			return err
		}
		// Second receive: returns io.EOF immediately iff the client already half-closed.
		got := make(chan error, 1)
		go func() { got <- stream.RecvMsg(&testpb.SimpleRequest{}) }()
		select {
		case err := <-got:
			sawEOF <- err == io.EOF
		case <-time.After(500 * time.Millisecond):
			sawEOF <- false
		}
		return stream.SendMsg(&testpb.SimpleResponse{Payload: req.GetPayload()})
	})}
	if err := ss.Start(sopts); err != nil {
		t.Fatalf("start: %v", err)
	}
	defer ss.Stop()
	ctx, cancel := context.WithTimeout(context.Background(), defaultTestTimeout)
	defer cancel()
	stream, err := ss.CC.NewStream(ctx, desc, "/verify.C6/UnaryCall")
	if err != nil {
		t.Fatalf("NewStream: %v", err)
	}
	if err := stream.SendMsg(&testpb.SimpleRequest{Payload: &testpb.Payload{Body: []byte("ping")}}); err != nil {
		t.Fatalf("SendMsg: %v", err)
	}
	// Deliberately no CloseSend, exactly like the regression test.
	resp := &testpb.SimpleResponse{}
	if err := stream.RecvMsg(resp); err != nil {
		t.Fatalf("RecvMsg: %v", err)
	}
	return <-sawEOF
}

func (s) TestVerify_C6_RegressionTestDescImplicitlyHalfCloses(t *testing.T) {
	// Exactly the desc used by TestServerPipeline_UnaryRespondsWithoutHalfClose.
	desc := &grpc.StreamDesc{StreamName: "UnaryCall"}
	saw := verifyC6Run(t, desc)
	t.Logf("desc=%+v: server observed client half-close before responding = %v", *desc, saw)
	if saw {
		t.Errorf("request SendMsg with ClientStreams=false implicitly half-closed the stream (transport Last=true); the regression test never exercises response-before-half-close")
	}
}

func (s) TestVerify_C6_ClientStreamsDescDoesNotHalfClose(t *testing.T) {
	desc := &grpc.StreamDesc{StreamName: "UnaryCall", ClientStreams: true}
	saw := verifyC6Run(t, desc)
	t.Logf("desc=%+v: server observed client half-close before responding = %v", *desc, saw)
	if saw {
		t.Errorf("unexpected half-close with ClientStreams=true")
	}
}
