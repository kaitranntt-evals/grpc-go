// Run: cp verify/repro/c2_c11_writestatus_success_accounting_test.go test/ && cd test && go test -v -run 'TestVerifyC2WriteStatusFailureRecordedAsSuccess' -count=1 .
//
// Forces the final statusOK WriteStatus to fail (client hard-closes the TCP
// connection while the unary handler is blocked) and inspects what the
// deferred stats handler (stats.End) and channelz call counters record.
package test

import (
	"context"
	"net"
	"sync"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/internal/channelz"
	"google.golang.org/grpc/stats"

	testgrpc "google.golang.org/grpc/interop/grpc_testing"
	testpb "google.golang.org/grpc/interop/grpc_testing"
)

type verifyC2StatsHandler struct {
	mu   sync.Mutex
	ends []*stats.End
}

func (h *verifyC2StatsHandler) TagRPC(ctx context.Context, _ *stats.RPCTagInfo) context.Context {
	return ctx
}
func (h *verifyC2StatsHandler) HandleRPC(_ context.Context, s stats.RPCStats) {
	if e, ok := s.(*stats.End); ok {
		h.mu.Lock()
		h.ends = append(h.ends, e)
		h.mu.Unlock()
	}
}
func (h *verifyC2StatsHandler) TagConn(ctx context.Context, _ *stats.ConnTagInfo) context.Context {
	return ctx
}
func (h *verifyC2StatsHandler) HandleConn(context.Context, stats.ConnStats) {}

type verifyC2Server struct {
	testgrpc.UnimplementedTestServiceServer
	proceed chan struct{}
	entered chan struct{}
}

func (s *verifyC2Server) UnaryCall(ctx context.Context, _ *testpb.SimpleRequest) (*testpb.SimpleResponse, error) {
	close(s.entered)
	<-s.proceed
	return &testpb.SimpleResponse{}, nil
}

func (s *verifyC2Server) FullDuplexCall(stream testgrpc.TestService_FullDuplexCallServer) error {
	stream.Recv()
	close(s.entered)
	<-s.proceed
	return nil // successful handler return; only the final statusOK WriteStatus remains
}

func TestVerifyC2WriteStatusFailureRecordedAsSuccess(t *testing.T) {
	channelz.TurnOn()

	sh := &verifyC2StatsHandler{}
	srv := &verifyC2Server{proceed: make(chan struct{}), entered: make(chan struct{})}
	s := grpc.NewServer(grpc.StatsHandler(sh))
	testgrpc.RegisterTestServiceServer(s, srv)
	lis, err := net.Listen("tcp", "localhost:0")
	if err != nil {
		t.Fatal(err)
	}
	go s.Serve(lis)
	defer s.Stop()

	var rawConn net.Conn
	var rawMu sync.Mutex
	dialer := func(ctx context.Context, addr string) (net.Conn, error) {
		c, err := (&net.Dialer{}).DialContext(ctx, "tcp", addr)
		if err == nil {
			rawMu.Lock()
			rawConn = c
			rawMu.Unlock()
		}
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

	done := make(chan error, 1)
	go func() {
		stream, err := client.FullDuplexCall(context.Background())
		if err != nil {
			done <- err
			return
		}
		stream.Send(&testpb.StreamingOutputCallRequest{})
		_, err = stream.Recv()
		done <- err
	}()

	<-srv.entered
	// Hard-close the client TCP connection so the server's final
	// statusOK WriteStatus fails.
	rawMu.Lock()
	if c, ok := rawConn.(*net.TCPConn); ok {
		c.SetLinger(0)
	}
	rawConn.Close()
	rawMu.Unlock()
	time.Sleep(200 * time.Millisecond) // let the server transport observe the close
	close(srv.proceed)

	<-done

	// Wait for deferred stats/channelz accounting.
	var end *stats.End
	for i := 0; i < 50; i++ {
		sh.mu.Lock()
		if len(sh.ends) > 0 {
			end = sh.ends[0]
		}
		sh.mu.Unlock()
		if end != nil {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if end == nil {
		t.Fatal("no stats.End recorded")
	}
	t.Logf("stats.End.Error = %v", end.Error)

	ss, _ := channelz.GetServers(0, 0)
	if len(ss) == 0 {
		t.Fatal("no channelz server")
	}
	m := &ss[len(ss)-1].ServerMetrics
	t.Logf("channelz: started=%d succeeded=%d failed=%d", m.CallsStarted.Load(), m.CallsSucceeded.Load(), m.CallsFailed.Load())

	if end.Error == nil && m.CallsSucceeded.Load() == 1 && m.CallsFailed.Load() == 0 {
		t.Log("RESULT: RPC recorded as SUCCESS despite failed final WriteStatus (claim CONFIRMED)")
	} else {
		t.Log("RESULT: RPC recorded as FAILURE (claim REFUTED)")
	}
}
