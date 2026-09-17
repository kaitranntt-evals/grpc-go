// Run: cp verify/repro/c1_halfclose_probe_test.go test/ && go test ./test -run '^TestVerifyC1_' -count=1 -v
//
// Probes whether a client stream opened with the descriptor used by the
// candidate test TestServer_UnaryRespondsBeforeHalfCloseAndPropagatesMetadata
// (grpc.StreamDesc{StreamName: "UnaryCall"}, ClientStreams=false) half-closes
// the request side (END_STREAM) as part of SendMsg, versus a descriptor with
// ClientStreams=true which keeps the request side open.
package test

import (
	"context"
	"io"
	"net"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	testpb "google.golang.org/grpc/interop/grpc_testing"
)

type c1Observation struct {
	firstErr  error
	secondErr error
	secondDur time.Duration
}

// c1Probe starts a bare server whose UnknownServiceHandler receives the first
// request message and then immediately performs a second RecvMsg. If the
// client already sent END_STREAM with its single message, the second RecvMsg
// returns io.EOF right away; otherwise it blocks until the handler context
// expires.
func c1Probe(t *testing.T, desc *grpc.StreamDesc) c1Observation {
	t.Helper()
	obs := make(chan c1Observation, 1)
	srv := grpc.NewServer(grpc.UnknownServiceHandler(func(_ any, ss grpc.ServerStream) error {
		var o c1Observation
		o.firstErr = ss.RecvMsg(&testpb.SimpleRequest{})
		start := time.Now()
		o.secondErr = ss.RecvMsg(&testpb.SimpleRequest{})
		o.secondDur = time.Since(start)
		obs <- o
		return ss.SendMsg(&testpb.SimpleResponse{})
	}))
	lis, err := net.Listen("tcp", "localhost:0")
	if err != nil {
		t.Fatal(err)
	}
	go srv.Serve(lis)
	t.Cleanup(srv.Stop)

	cc, err := grpc.NewClient(lis.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cc.Close() })

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	stream, err := cc.NewStream(ctx, desc, "/grpc.testing.TestService/UnaryCall")
	if err != nil {
		t.Fatalf("NewStream: %v", err)
	}
	if err := stream.SendMsg(&testpb.SimpleRequest{}); err != nil {
		t.Fatalf("SendMsg: %v", err)
	}
	// Do NOT call CloseSend: the request side is only closed if SendMsg did it.
	select {
	case o := <-obs:
		return o
	case <-time.After(5 * time.Second):
		t.Fatal("server handler never reported")
	}
	return c1Observation{}
}

// TestVerifyC1_CandidateDescriptorHalfClosesOnSend uses the exact descriptor
// from the candidate test. Expectation if the claim holds: second server-side
// RecvMsg returns io.EOF immediately, i.e. END_STREAM was sent automatically
// with the single request message.
func TestVerifyC1_CandidateDescriptorHalfClosesOnSend(t *testing.T) {
	o := c1Probe(t, &grpc.StreamDesc{StreamName: "UnaryCall"})
	t.Logf("ClientStreams=false: first RecvMsg err=%v; second RecvMsg err=%v after %v", o.firstErr, o.secondErr, o.secondDur)
	if o.firstErr != nil {
		t.Fatalf("first RecvMsg = %v, want nil", o.firstErr)
	}
	if o.secondErr != io.EOF {
		t.Fatalf("second RecvMsg = %v, want io.EOF (request side half-closed by SendMsg)", o.secondErr)
	}
	if o.secondDur > time.Second {
		t.Fatalf("second RecvMsg took %v; expected immediate EOF", o.secondDur)
	}
}

// TestVerifyC1_ClientStreamsDescriptorKeepsRequestOpen is the control: with
// ClientStreams=true the request side stays open, so the second server-side
// RecvMsg blocks until the RPC deadline expires.
func TestVerifyC1_ClientStreamsDescriptorKeepsRequestOpen(t *testing.T) {
	o := c1Probe(t, &grpc.StreamDesc{StreamName: "UnaryCall", ClientStreams: true})
	t.Logf("ClientStreams=true: first RecvMsg err=%v; second RecvMsg err=%v after %v", o.firstErr, o.secondErr, o.secondDur)
	if o.firstErr != nil {
		t.Fatalf("first RecvMsg = %v, want nil", o.firstErr)
	}
	if o.secondErr == io.EOF {
		t.Fatalf("second RecvMsg = io.EOF; request side was unexpectedly half-closed")
	}
	if o.secondDur < 2*time.Second {
		t.Fatalf("second RecvMsg returned after %v; expected it to block until the 3s deadline", o.secondDur)
	}
}
