// Run: cp verify/repro/c7_writestatus_warning_test.go <worktree>/test/ && cd <worktree>/test && go test -v -run 'TestVerifyC7WriteStatusWarning' -count=1 .
//
// Forces the terminal WriteStatus to fail on (a) the application-error
// completion path and (b) the successful completion path, capturing all
// grpclog output and reporting whether any warning identifies the failed
// status write.
package test

import (
	"context"
	"fmt"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/grpclog"
	"google.golang.org/grpc/status"

	testgrpc "google.golang.org/grpc/interop/grpc_testing"
	testpb "google.golang.org/grpc/interop/grpc_testing"
)

type verifyC7Logger struct {
	mu    sync.Mutex
	lines []string
}

func (l *verifyC7Logger) log(args ...any) {
	l.mu.Lock()
	l.lines = append(l.lines, fmt.Sprint(args...))
	l.mu.Unlock()
}
func (l *verifyC7Logger) Info(args ...any)               { l.log(args...) }
func (l *verifyC7Logger) Infoln(args ...any)             { l.log(args...) }
func (l *verifyC7Logger) Infof(f string, args ...any)    { l.log(fmt.Sprintf(f, args...)) }
func (l *verifyC7Logger) Warning(args ...any)            { l.log(args...) }
func (l *verifyC7Logger) Warningln(args ...any)          { l.log(args...) }
func (l *verifyC7Logger) Warningf(f string, args ...any) { l.log(fmt.Sprintf(f, args...)) }
func (l *verifyC7Logger) Error(args ...any)              { l.log(args...) }
func (l *verifyC7Logger) Errorln(args ...any)            { l.log(args...) }
func (l *verifyC7Logger) Errorf(f string, args ...any)   { l.log(fmt.Sprintf(f, args...)) }
func (l *verifyC7Logger) Fatal(args ...any)              { l.log(args...) }
func (l *verifyC7Logger) Fatalln(args ...any)            { l.log(args...) }
func (l *verifyC7Logger) Fatalf(f string, args ...any)   { l.log(fmt.Sprintf(f, args...)) }
func (l *verifyC7Logger) V(int) bool                     { return true }

type verifyC7Server struct {
	testgrpc.UnimplementedTestServiceServer
	proceed chan struct{}
	entered chan struct{}
	appErr  error
}

func (s *verifyC7Server) FullDuplexCall(stream testgrpc.TestService_FullDuplexCallServer) error {
	stream.Recv()
	close(s.entered)
	<-s.proceed
	return s.appErr
}

func (s *verifyC7Server) UnaryCall(ctx context.Context, _ *testpb.SimpleRequest) (*testpb.SimpleResponse, error) {
	close(s.entered)
	<-s.proceed
	return nil, s.appErr
}

func verifyC7Run(t *testing.T, appErr error, unary bool) []string {
	lg := &verifyC7Logger{}
	grpclog.SetLoggerV2(lg)

	srv := &verifyC7Server{proceed: make(chan struct{}), entered: make(chan struct{}), appErr: appErr}
	s := grpc.NewServer()
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

	done := make(chan struct{})
	go func() {
		if unary {
			client.UnaryCall(context.Background(), &testpb.SimpleRequest{})
		} else {
			stream, err := client.FullDuplexCall(context.Background())
			if err == nil {
				stream.Send(&testpb.StreamingOutputCallRequest{})
				stream.Recv()
			}
		}
		close(done)
	}()

	<-srv.entered
	rawMu.Lock()
	if c, ok := rawConn.(*net.TCPConn); ok {
		c.SetLinger(0)
	}
	rawConn.Close()
	rawMu.Unlock()
	time.Sleep(200 * time.Millisecond)
	close(srv.proceed)
	<-done
	time.Sleep(500 * time.Millisecond)

	lg.mu.Lock()
	defer lg.mu.Unlock()
	return append([]string(nil), lg.lines...)
}

func TestVerifyC7WriteStatusWarning(t *testing.T) {
	for _, tc := range []struct {
		name   string
		appErr error
		unary  bool
	}{
		{"application-error-unary", status.Errorf(13, "app failure"), true},
		{"application-error-streaming", status.Errorf(13, "app failure"), false},
		{"success-streaming", nil, false},
	} {
		name := tc.name
		lines := verifyC7Run(t, tc.appErr, tc.unary)
		var found []string
		for _, ln := range lines {
			low := strings.ToLower(ln)
			if strings.Contains(low, "write status") || strings.Contains(low, "writestatus") || (strings.Contains(low, "status") && strings.Contains(low, "fail")) {
				found = append(found, ln)
			}
		}
		if len(found) == 0 {
			t.Logf("%s path: NO warning identifying the failed status write (%d log lines total)", name, len(lines))
		} else {
			t.Logf("%s path: warning(s) found: %q", name, found)
		}
	}
}
