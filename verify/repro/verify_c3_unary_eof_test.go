// Run: cd <repo> && go test -v ./test -run '^TestVerify_UnaryHandlerReturnsEOF$' -count=1
// Copy into the target branch's test/ directory and add -tags verify_repro to the go test command.

//go:build verify_repro

package test

import (
	"context"
	"io"
	"testing"
	"time"

	"google.golang.org/grpc/internal/channelz"
	"google.golang.org/grpc/internal/stubserver"
	"google.golang.org/grpc/status"

	testpb "google.golang.org/grpc/interop/grpc_testing"
)

func TestVerify_UnaryHandlerReturnsEOF(t *testing.T) {
	channelz.TurnOn()
	ss := &stubserver.StubServer{
		UnaryCallF: func(ctx context.Context, in *testpb.SimpleRequest) (*testpb.SimpleResponse, error) {
			return nil, io.EOF
		},
	}
	if err := ss.Start(nil); err != nil {
		t.Fatalf("start: %v", err)
	}
	defer ss.Stop()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_, err := ss.Client.UnaryCall(ctx, &testpb.SimpleRequest{})
	st, _ := status.FromError(err)
	t.Logf("client observed: err=%v code=%v msg=%q", err, st.Code(), st.Message())

	time.Sleep(100 * time.Millisecond)
	svrs, _ := channelz.GetServers(0, 1)
	for _, s := range svrs {
		t.Logf("channelz server metrics: started=%d succeeded=%d failed=%d",
			s.ServerMetrics.CallsStarted.Load(), s.ServerMetrics.CallsSucceeded.Load(), s.ServerMetrics.CallsFailed.Load())
	}
}
