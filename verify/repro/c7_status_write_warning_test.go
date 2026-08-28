// Run: copy verify/repro into the target worktree, then
// go test ./verify/repro -run 'TestC7' -v -count=1
// C7: forces terminal WriteStatus failures (app-error and success paths) and
// reports whether a "Server.processRPC failed to write status" channelz
// warning is logged.
package repro

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/grpclog"
	"google.golang.org/grpc/internal/stubserver"
	"google.golang.org/grpc/stats"
	"google.golang.org/grpc/status"

	testgrpc "google.golang.org/grpc/interop/grpc_testing"
	testpb "google.golang.org/grpc/interop/grpc_testing"
)

type capLogger struct {
	mu    sync.Mutex
	lines []string
}

func (l *capLogger) add(args ...any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.lines = append(l.lines, fmt.Sprint(args...))
}

func (l *capLogger) matches(sub string) []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	var out []string
	for _, ln := range l.lines {
		if strings.Contains(ln, sub) {
			out = append(out, ln)
		}
	}
	return out
}

func (l *capLogger) Info(args ...any)                    {}
func (l *capLogger) Infoln(args ...any)                  {}
func (l *capLogger) Infof(format string, args ...any)    {}
func (l *capLogger) Warning(args ...any)                 { l.add(args...) }
func (l *capLogger) Warningln(args ...any)               { l.add(args...) }
func (l *capLogger) Warningf(format string, args ...any) { l.add(fmt.Sprintf(format, args...)) }
func (l *capLogger) Error(args ...any)                   { l.add(args...) }
func (l *capLogger) Errorln(args ...any)                 { l.add(args...) }
func (l *capLogger) Errorf(format string, args ...any)   { l.add(fmt.Sprintf(format, args...)) }
func (l *capLogger) Fatal(args ...any)                   {}
func (l *capLogger) Fatalln(args ...any)                 {}
func (l *capLogger) Fatalf(format string, args ...any)   {}
func (l *capLogger) V(l2 int) bool                       { return false }

type endStatsHandler struct {
	endCh chan *stats.End
}

func (*endStatsHandler) TagRPC(ctx context.Context, _ *stats.RPCTagInfo) context.Context {
	return ctx
}

func (h *endStatsHandler) HandleRPC(_ context.Context, e stats.RPCStats) {
	if end, ok := e.(*stats.End); ok {
		select {
		case h.endCh <- end:
		default:
		}
	}
}

func (*endStatsHandler) TagConn(ctx context.Context, _ *stats.ConnTagInfo) context.Context {
	return ctx
}

func (*endStatsHandler) HandleConn(context.Context, stats.ConnStats) {}

func runC7Scenario(t *testing.T, handlerErr error) (warnings []string, endErr error) {
	lg := &capLogger{}
	grpclog.SetLoggerV2(lg)

	sh := &endStatsHandler{endCh: make(chan *stats.End, 1)}
	ss := &stubserver.StubServer{
		FullDuplexCallF: func(stream testgrpc.TestService_FullDuplexCallServer) error {
			if err := stream.Send(&testpb.StreamingOutputCallResponse{}); err != nil {
				return err
			}
			<-stream.Context().Done()
			return handlerErr
		},
	}
	if err := ss.Start([]grpc.ServerOption{grpc.StatsHandler(sh)}, grpc.WithTransportCredentials(insecure.NewCredentials())); err != nil {
		t.Fatal(err)
	}
	defer ss.Stop()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	stream, err := ss.Client.FullDuplexCall(ctx)
	if err != nil {
		t.Fatalf("FullDuplexCall failed: %v", err)
	}
	if _, err := stream.Recv(); err != nil {
		t.Fatalf("Recv failed: %v", err)
	}
	ss.CC.Close()

	select {
	case end := <-sh.endCh:
		endErr = end.Error
	case <-time.After(10 * time.Second):
		t.Fatal("no stats.End observed")
	}
	time.Sleep(200 * time.Millisecond)
	return lg.matches("failed to write status"), endErr
}

func TestC7AppErrorStatusWriteWarning(t *testing.T) {
	warnings, endErr := runC7Scenario(t, status.Error(codes.Aborted, "app error"))
	t.Logf("stats.End.Error: %v", endErr)
	t.Logf("captured 'failed to write status' warnings: %q", warnings)
}

func TestC7SuccessStatusWriteWarning(t *testing.T) {
	warnings, endErr := runC7Scenario(t, nil)
	t.Logf("stats.End.Error: %v", endErr)
	t.Logf("captured 'failed to write status' warnings: %q", warnings)
}
