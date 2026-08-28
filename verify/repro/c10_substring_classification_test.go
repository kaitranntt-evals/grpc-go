// Run: cd <repo-on-branch-evalon/grpc-go-se-33a77a50> && go test ./verify/repro -run 'TestC10' -v -count=1
package repro

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/grpclog"
	"google.golang.org/grpc/internal/stubserver"
	testpb "google.golang.org/grpc/interop/grpc_testing"
	"google.golang.org/protobuf/proto"
)

type c10CapLogger struct {
	mu    sync.Mutex
	lines []string
}

func (l *c10CapLogger) add(args ...any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.lines = append(l.lines, fmt.Sprint(args...))
}
func (l *c10CapLogger) Info(args ...any)                    { l.add(args...) }
func (l *c10CapLogger) Infoln(args ...any)                  { l.add(args...) }
func (l *c10CapLogger) Infof(f string, args ...any)         { l.add(fmt.Sprintf(f, args...)) }
func (l *c10CapLogger) Warning(args ...any)                 { l.add(args...) }
func (l *c10CapLogger) Warningln(args ...any)               { l.add(args...) }
func (l *c10CapLogger) Warningf(f string, args ...any)      { l.add(fmt.Sprintf(f, args...)) }
func (l *c10CapLogger) Error(args ...any)                   { l.add(args...) }
func (l *c10CapLogger) Errorln(args ...any)                 { l.add(args...) }
func (l *c10CapLogger) Errorf(f string, args ...any)        { l.add(fmt.Sprintf(f, args...)) }
func (l *c10CapLogger) Fatal(args ...any)                   { l.add(args...) }
func (l *c10CapLogger) Fatalln(args ...any)                 { l.add(args...) }
func (l *c10CapLogger) Fatalf(f string, args ...any)        { l.add(fmt.Sprintf(f, args...)) }
func (l *c10CapLogger) V(int) bool                          { return true }
func (l *c10CapLogger) matching(sub string) (out []string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, s := range l.lines {
		if strings.Contains(s, sub) {
			out = append(out, s)
		}
	}
	return out
}

type c10ProtoCodec struct{}

func (c10ProtoCodec) Marshal(v any) ([]byte, error)      { return proto.Marshal(v.(proto.Message)) }
func (c10ProtoCodec) Unmarshal(data []byte, v any) error { return proto.Unmarshal(data, v.(proto.Message)) }
func (c10ProtoCodec) String() string                     { return "proto" }

// c10EvalCodec fails Marshal on the server response with an error message that
// contains the compression substring even though no compression is involved.
type c10EvalCodec struct{ grpc.Codec }

func (c c10EvalCodec) Marshal(v any) ([]byte, error) {
	if r, ok := v.(*testpb.SimpleResponse); ok && r.GetUsername() == "trigger-encode-failure" {
		return nil, fmt.Errorf("synthetic encoding failure: error while compressing lookalike text")
	}
	return c.Codec.Marshal(v)
}

func TestC10SubstringClassification(t *testing.T) {
	lg := &c10CapLogger{}
	grpclog.SetLoggerV2(lg)

	ss := &stubserver.StubServer{
		UnaryCallF: func(context.Context, *testpb.SimpleRequest) (*testpb.SimpleResponse, error) {
			return &testpb.SimpleResponse{Username: "trigger-encode-failure"}, nil
		},
	}
	codec := c10EvalCodec{Codec: c10ProtoCodec{}}
	if err := ss.Start([]grpc.ServerOption{grpc.CustomCodec(codec)}, grpc.WithCodec(codec)); err != nil {
		t.Fatalf("start: %v", err)
	}
	defer ss.Stop()

	_, err := ss.Client.UnaryCall(context.Background(), &testpb.SimpleRequest{})
	t.Logf("client error: %v", err)

	compressLogs := lg.matching("server failed to compress response")
	encodeLogs := lg.matching("server failed to encode response")
	t.Logf("compress-classified logs: %d %v", len(compressLogs), compressLogs)
	t.Logf("encode-classified logs: %d %v", len(encodeLogs), encodeLogs)
	if len(compressLogs) > 0 && len(encodeLogs) == 0 {
		t.Logf("RESULT: encoding failure misclassified as compression failure via substring match (claim behavior observed)")
	}
}
