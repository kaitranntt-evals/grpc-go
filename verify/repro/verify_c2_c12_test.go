// Repro for C2/C12: go test ./verify/repro -run TestVerifyC2C12StatusOKWriteFailure -count=1 -v (audited branch)
package test

import (
	"bytes"
	"context"
	"fmt"
	"net"
	"sync"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/grpclog"
	"google.golang.org/grpc/internal/channelz"
	"google.golang.org/grpc/stats"

	testgrpc "google.golang.org/grpc/interop/grpc_testing"
	testpb "google.golang.org/grpc/interop/grpc_testing"
)

type recordingStatsHandler struct {
	mu   sync.Mutex
	ends []*stats.End
}

func (h *recordingStatsHandler) TagRPC(ctx context.Context, _ *stats.RPCTagInfo) context.Context {
	return ctx
}
func (h *recordingStatsHandler) HandleRPC(_ context.Context, s stats.RPCStats) {
	if e, ok := s.(*stats.End); ok {
		h.mu.Lock()
		h.ends = append(h.ends, e)
		h.mu.Unlock()
	}
}
func (h *recordingStatsHandler) TagConn(ctx context.Context, _ *stats.ConnTagInfo) context.Context {
	return ctx
}
func (h *recordingStatsHandler) HandleConn(context.Context, stats.ConnStats) {}

type verifySvc struct {
	testgrpc.UnimplementedTestServiceServer
	started     chan struct{}
	proceed     chan struct{}
	handlerDone chan struct{}
}

// FullDuplexCall receives one message, waits for the test to kill the
// client connection, then returns success without sending anything, so
// the final WriteStatus(statusOK) is the only transport write that fails.
func (s *verifySvc) FullDuplexCall(stream testgrpc.TestService_FullDuplexCallServer) error {
	if _, err := stream.Recv(); err != nil {
		return err
	}
	close(s.started)
	<-s.proceed
	defer close(s.handlerDone)
	return nil
}

func TestVerifyC2C12StatusOKWriteFailure(t *testing.T) {
	var logBuf bytes.Buffer
	grpclog.SetLoggerV2(grpclog.NewLoggerV2(&logBuf, &logBuf, &logBuf))
	defer grpclog.SetLoggerV2(grpclog.NewLoggerV2(nil, nil, nil))

	channelz.TurnOn()

	sh := &recordingStatsHandler{}
	svc := &verifySvc{
		started:     make(chan struct{}),
		proceed:     make(chan struct{}),
		handlerDone: make(chan struct{}),
	}
	srv := grpc.NewServer(grpc.StatsHandler(sh))
	testgrpc.RegisterTestServiceServer(srv, svc)
	lis, err := net.Listen("tcp", "localhost:0")
	if err != nil {
		t.Fatal(err)
	}
	go srv.Serve(lis)
	defer srv.Stop()

	var clientConn net.Conn
	dialer := func(ctx context.Context, addr string) (net.Conn, error) {
		c, err := (&net.Dialer{}).DialContext(ctx, "tcp", addr)
		clientConn = c
		return c, err
	}
	cc, err := grpc.NewClient(lis.Addr().String(),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithContextDialer(dialer))
	if err != nil {
		t.Fatal(err)
	}
	defer cc.Close()

	client := testgrpc.NewTestServiceClient(cc)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	stream, err := client.FullDuplexCall(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := stream.Send(&testpb.StreamingOutputCallRequest{}); err != nil {
		t.Fatal(err)
	}

	<-svc.started
	// Kill the client's TCP connection so the server's terminal
	// WriteStatus(statusOK) fails after the handler returns success.
	clientConn.Close()
	time.Sleep(500 * time.Millisecond)
	close(svc.proceed)
	<-svc.handlerDone
	time.Sleep(1 * time.Second)

	sh.mu.Lock()
	defer sh.mu.Unlock()
	if len(sh.ends) != 1 {
		t.Fatalf("got %d stats.End events, want 1", len(sh.ends))
	}
	t.Logf("stats.End.Error = %v", sh.ends[0].Error)

	servers, _ := channelz.GetServers(0, 0)
	for _, s := range servers {
		m := s.ServerMetrics.CopyFrom
		_ = m
		fmt.Println()
		t.Logf("channelz server %d: started=%d succeeded=%d failed=%d",
			s.ID, s.ServerMetrics.CallsStarted.Load(), s.ServerMetrics.CallsSucceeded.Load(), s.ServerMetrics.CallsFailed.Load())
	}
	t.Logf("grpclog output:\n%s", logBuf.String())
}
