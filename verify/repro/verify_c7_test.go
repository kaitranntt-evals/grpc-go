// Repro for C7: copy into test/ of branch evalon/grpc-go-se-c53e1aa0, then: go test ./test -run TestVerifyC7 -count=1 -v
package test

import (
	"bytes"
	"context"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/grpclog"
	"google.golang.org/grpc/internal/channelz"
	"google.golang.org/grpc/stats"
	"google.golang.org/grpc/status"

	testgrpc "google.golang.org/grpc/interop/grpc_testing"
	testpb "google.golang.org/grpc/interop/grpc_testing"
)

type c7StatsHandler struct {
	mu   sync.Mutex
	ends []*stats.End
}

func (h *c7StatsHandler) TagRPC(ctx context.Context, _ *stats.RPCTagInfo) context.Context {
	return ctx
}
func (h *c7StatsHandler) HandleRPC(_ context.Context, s stats.RPCStats) {
	if e, ok := s.(*stats.End); ok {
		h.mu.Lock()
		h.ends = append(h.ends, e)
		h.mu.Unlock()
	}
}
func (h *c7StatsHandler) TagConn(ctx context.Context, _ *stats.ConnTagInfo) context.Context {
	return ctx
}
func (h *c7StatsHandler) HandleConn(context.Context, stats.ConnStats) {}

type c7Svc struct {
	testgrpc.UnimplementedTestServiceServer
	started     chan struct{}
	proceed     chan struct{}
	handlerDone chan struct{}
	retErr      error
}

// FullDuplexCall receives one message, waits for the test to kill the client
// connection, then returns retErr, so the terminal WriteStatus is the only
// transport write that fails.
func (s *c7Svc) FullDuplexCall(stream testgrpc.TestService_FullDuplexCallServer) error {
	if _, err := stream.Recv(); err != nil {
		return err
	}
	close(s.started)
	<-s.proceed
	defer close(s.handlerDone)
	return s.retErr
}

func runC7(t *testing.T, retErr error) (endErr error, logs string, succeeded, failed int64) {
	var logBuf bytes.Buffer
	grpclog.SetLoggerV2(grpclog.NewLoggerV2(&logBuf, &logBuf, &logBuf))
	defer grpclog.SetLoggerV2(grpclog.NewLoggerV2(nil, nil, nil))

	channelz.TurnOn()

	sh := &c7StatsHandler{}
	svc := &c7Svc{
		started:     make(chan struct{}),
		proceed:     make(chan struct{}),
		handlerDone: make(chan struct{}),
		retErr:      retErr,
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
	servers, _ := channelz.GetServers(0, 0)
	for _, s := range servers {
		succeeded += s.ServerMetrics.CallsSucceeded.Load()
		failed += s.ServerMetrics.CallsFailed.Load()
	}
	return sh.ends[0].Error, logBuf.String(), succeeded, failed
}

func TestVerifyC7AppStatusWriteFailure(t *testing.T) {
	endErr, logs, _, _ := runC7(t, status.Error(codes.Internal, "app boom"))
	t.Logf("stats.End.Error = %v", endErr)
	t.Logf("log contains 'failed to write status': %v", strings.Contains(logs, "failed to write status"))
}

func TestVerifyC7StatusOKWriteFailure(t *testing.T) {
	endErr, logs, succeeded, failed := runC7(t, nil)
	t.Logf("stats.End.Error = %v", endErr)
	t.Logf("channelz succeeded=%d failed=%d", succeeded, failed)
	t.Logf("log contains 'failed to write status': %v", strings.Contains(logs, "failed to write status"))
}
