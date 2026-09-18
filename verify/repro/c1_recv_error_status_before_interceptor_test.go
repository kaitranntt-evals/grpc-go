// Run: cp verify/repro/c1_recv_error_status_before_interceptor_test.go test/ && go test ./test -run 'Test/VerifyC1_' -tags verify_repro -count=1 -race -v
//
// Demonstrates that when a streaming handler's Recv fails (oversized message
// after decompression), the client observes the final ResourceExhausted status
// *before* the stream interceptor has returned, i.e. before any post-handler
// recording done by the interceptor.  serverStream.RecvMsg writes the status
// to the transport in its deferred error path, so a test that reads
// interceptor records after the client-side failure has no completion signal
// covering the recording.  The interceptor is parked on a channel after the
// handler returns to emulate the scheduler preempting it at that point.
//go:build verify_repro

package test

import (
	"context"
	"sync"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/encoding/gzip"
	"google.golang.org/grpc/internal/stubserver"
	"google.golang.org/grpc/status"

	testgrpc "google.golang.org/grpc/interop/grpc_testing"
	testpb "google.golang.org/grpc/interop/grpc_testing"
)

func (s) TestVerifyC1_RecvErrorStatusReachesClientBeforeInterceptorRecords(t *testing.T) {
	const maxRecvSize = 1024
	handlerReturned := make(chan error, 1)
	release := make(chan struct{})
	var mu sync.Mutex
	var recorded []error

	ss := &stubserver.StubServer{
		FullDuplexCallF: func(stream testgrpc.TestService_FullDuplexCallServer) error {
			_, err := stream.Recv()
			return err
		},
	}
	sopts := []grpc.ServerOption{
		grpc.MaxRecvMsgSize(maxRecvSize),
		grpc.StreamInterceptor(func(srv any, st grpc.ServerStream, _ *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
			err := handler(srv, st)
			handlerReturned <- err
			<-release // emulate preemption between handler return and recording
			mu.Lock()
			recorded = append(recorded, err)
			mu.Unlock()
			return err
		}),
	}
	if err := ss.Start(sopts, grpc.WithDefaultCallOptions(grpc.UseCompressor(gzip.Name))); err != nil {
		t.Fatalf("Error starting server: %v", err)
	}
	defer ss.Stop()
	defer func() {
		select {
		case <-release:
		default:
			close(release)
		}
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	stream, err := ss.Client.FullDuplexCall(ctx)
	if err != nil {
		t.Fatalf("FullDuplexCall() failed: %v", err)
	}
	payload := &testpb.Payload{Body: make([]byte, 16*maxRecvSize)}
	if err := stream.Send(&testpb.StreamingOutputCallRequest{Payload: payload}); err != nil {
		t.Fatalf("Send() failed: %v", err)
	}
	start := time.Now()
	_, err = stream.Recv()
	elapsed := time.Since(start)
	if status.Code(err) != codes.ResourceExhausted {
		t.Fatalf("client Recv() = %v, want code %v", err, codes.ResourceExhausted)
	}
	t.Logf("client observed final status %v after %v", status.Code(err), elapsed)

	// The handler has returned (its Recv failed) ...
	select {
	case herr := <-handlerReturned:
		t.Logf("server handler returned %v", status.Code(herr))
	case <-time.After(5 * time.Second):
		t.Fatalf("server handler did not return")
	}
	// ... but the interceptor is still parked before recording.
	mu.Lock()
	n := len(recorded)
	mu.Unlock()
	if n == 0 {
		t.Errorf("client observed the RPC failure while the stream interceptor had recorded %d error(s); post-handler recording was still pending (no completion signal)", n)
	} else {
		t.Logf("interceptor recording (%d) completed before the client observed the failure", n)
	}
	close(release)
}
