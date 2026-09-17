// Run: cp verify/repro/c1_post_handler_record_race_test.go test/ && go test ./test -run 'Test/Verify_C1_' -count=1 -v
//
// Demonstrates that, for a streaming RPC whose failure is produced by a
// receive error (oversized message after decompression), the client observes
// the RPC status *before* a post-handler stream interceptor gets to record its
// invocation.  A test that reads the recorder right after the client sees the
// error (as the candidate tests on evalon/grpc-go-se-82014a80 and
// evalon/grpc-go-se-2ea61f68 do) therefore races with the append.

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

// Verify_C1_ClientSeesRecvErrorBeforePostHandlerRecord gates the interceptor's
// post-handler record on a channel the client closes only after its Recv has
// returned the ResourceExhausted status.  If the status could only reach the
// client after the interceptor returned, the client's Recv would block until
// the interceptor's bounded wait expires and the test reports "timeout".
func (s) TestVerify_C1_ClientSeesRecvErrorBeforePostHandlerRecord(t *testing.T) {
	const maxRecvSize = 1024
	clientSawError := make(chan struct{})
	recorded := make(chan string, 1)

	streamInterceptor := func(srv any, ss grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
		err := handler(srv, ss)
		// Simulates the interceptor goroutine being descheduled between the
		// handler returning and the record being appended.
		select {
		case <-clientSawError:
			recorded <- "client observed the error BEFORE the post-handler record was appended"
		case <-time.After(5 * time.Second):
			recorded <- "timeout: client did not observe the error while the record was pending"
		}
		return err
	}

	ss := &stubserver.StubServer{
		FullDuplexCallF: func(stream testgrpc.TestService_FullDuplexCallServer) error {
			for {
				if _, err := stream.Recv(); err != nil {
					return err
				}
			}
		},
		StreamingInputCallF: func(stream testgrpc.TestService_StreamingInputCallServer) error {
			for {
				if _, err := stream.Recv(); err != nil {
					return err
				}
			}
		},
	}
	if err := ss.Start([]grpc.ServerOption{grpc.StreamInterceptor(streamInterceptor), grpc.MaxRecvMsgSize(maxRecvSize)}); err != nil {
		t.Fatalf("Error starting server: %v", err)
	}
	defer ss.Stop()

	ctx, cancel := context.WithTimeout(context.Background(), defaultTestTimeout)
	defer cancel()
	stream, err := ss.Client.FullDuplexCall(ctx, grpc.UseCompressor(gzip.Name))
	if err != nil {
		t.Fatalf("FullDuplexCall() failed: %v", err)
	}
	if err := stream.Send(&testpb.StreamingOutputCallRequest{Payload: &testpb.Payload{Body: make([]byte, 16*maxRecvSize)}}); err != nil {
		t.Fatalf("Send() failed: %v", err)
	}
	start := time.Now()
	_, err = stream.Recv()
	elapsed := time.Since(start)
	if status.Code(err) != codes.ResourceExhausted {
		t.Fatalf("Recv() = %v, want code %v", err, codes.ResourceExhausted)
	}
	t.Logf("client Recv() returned %v after %v", status.Code(err), elapsed)
	close(clientSawError)
	t.Logf("interceptor: %s", <-recorded)
	if elapsed > 4*time.Second {
		t.Fatalf("client only saw the error after the interceptor's bounded wait; recording precedes the client-visible event")
	}
}

// Verify_C1_DelayedRecordBreaksPostErrorAssertion reproduces the candidate
// tests' recorder shape (record after handler returns, guarded by a mutex) and
// their assertion (read the recorder right after the client sees the error),
// with a small scheduling delay inserted before the append.  The assertion the
// candidate tests make ("interceptor ran once with ResourceExhausted") fails.
func (s) TestVerify_C1_DelayedRecordBreaksPostErrorAssertion(t *testing.T) {
	const maxRecvSize = 1024
	rec := &serverInterceptorRecorderC1{}
	streamInterceptor := func(srv any, ss grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
		err := handler(srv, ss)
		time.Sleep(200 * time.Millisecond) // scheduling delay before the append
		rec.mu.Lock()
		rec.streamErrs = append(rec.streamErrs, err)
		rec.mu.Unlock()
		return err
	}
	ss := &stubserver.StubServer{
		StreamingInputCallF: func(stream testgrpc.TestService_StreamingInputCallServer) error {
			for {
				if _, err := stream.Recv(); err != nil {
					return err
				}
			}
		},
	}
	if err := ss.Start([]grpc.ServerOption{grpc.StreamInterceptor(streamInterceptor), grpc.MaxRecvMsgSize(maxRecvSize)}); err != nil {
		t.Fatalf("Error starting server: %v", err)
	}
	defer ss.Stop()

	ctx, cancel := context.WithTimeout(context.Background(), defaultTestTimeout)
	defer cancel()
	stream, err := ss.Client.StreamingInputCall(ctx, grpc.UseCompressor(gzip.Name))
	if err != nil {
		t.Fatalf("StreamingInputCall() failed: %v", err)
	}
	if err := stream.Send(&testpb.StreamingInputCallRequest{Payload: &testpb.Payload{Body: make([]byte, 16*maxRecvSize)}}); err != nil {
		t.Fatalf("Send() failed: %v", err)
	}
	if _, err := stream.CloseAndRecv(); status.Code(err) != codes.ResourceExhausted {
		t.Fatalf("CloseAndRecv() = %v, want code %v", err, codes.ResourceExhausted)
	}

	// Exactly what the candidate tests do next: lock the recorder and assert.
	rec.mu.Lock()
	got := len(rec.streamErrs)
	rec.mu.Unlock()
	if got != 1 {
		t.Errorf("interceptor ran %d time(s) as observed right after the client error, want 1 (record not yet appended)", got)
	}
}

type serverInterceptorRecorderC1 struct {
	mu         sync.Mutex
	streamErrs []error
}
