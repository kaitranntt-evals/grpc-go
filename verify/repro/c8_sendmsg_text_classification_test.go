// Run: cp verify/repro/c8_sendmsg_text_classification_test.go <worktree>/test/ && cd <worktree>/test && go test -v -run 'TestVerifyC8SendMsgTextClassification' -count=1 .
//
// Demonstrates that serverStream.SendMsg classifies prepareMsg failures by
// matching rendered error text: a codec whose Marshal (encode) error merely
// *contains* the substring "error while compressing" is logged by the server
// as a failure to "compress" the response instead of "encode".
package test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/encoding"
	"google.golang.org/grpc/grpclog"
	"google.golang.org/grpc/internal/stubserver"
	"google.golang.org/grpc/mem"

	testpb "google.golang.org/grpc/interop/grpc_testing"
)

type verifyC8Logger struct {
	mu    sync.Mutex
	lines []string
}

func (l *verifyC8Logger) log(args ...any) {
	l.mu.Lock()
	l.lines = append(l.lines, fmt.Sprint(args...))
	l.mu.Unlock()
}
func (l *verifyC8Logger) Info(args ...any)               { l.log(args...) }
func (l *verifyC8Logger) Infoln(args ...any)             { l.log(args...) }
func (l *verifyC8Logger) Infof(f string, args ...any)    { l.log(fmt.Sprintf(f, args...)) }
func (l *verifyC8Logger) Warning(args ...any)            { l.log(args...) }
func (l *verifyC8Logger) Warningln(args ...any)          { l.log(args...) }
func (l *verifyC8Logger) Warningf(f string, args ...any) { l.log(fmt.Sprintf(f, args...)) }
func (l *verifyC8Logger) Error(args ...any)              { l.log(args...) }
func (l *verifyC8Logger) Errorln(args ...any)            { l.log(args...) }
func (l *verifyC8Logger) Errorf(f string, args ...any)   { l.log(fmt.Sprintf(f, args...)) }
func (l *verifyC8Logger) Fatal(args ...any)              { l.log(args...) }
func (l *verifyC8Logger) Fatalln(args ...any)            { l.log(args...) }
func (l *verifyC8Logger) Fatalf(f string, args ...any)   { l.log(fmt.Sprintf(f, args...)) }
func (l *verifyC8Logger) V(int) bool                     { return true }

type verifyC8EncodeErrCodec struct {
	marshalErr error
	calls      int
}

func (c *verifyC8EncodeErrCodec) Marshal(v any) (mem.BufferSlice, error) {
	c.calls++
	return nil, c.marshalErr // server-side Marshal is only invoked for the response
}

func (c *verifyC8EncodeErrCodec) Unmarshal(data mem.BufferSlice, v any) error {
	return encoding.GetCodecV2("proto").Unmarshal(data, v)
}

func (c *verifyC8EncodeErrCodec) Name() string { return "proto" }

func TestVerifyC8SendMsgTextClassification(t *testing.T) {
	lg := &verifyC8Logger{}
	grpclog.SetLoggerV2(lg)

	// An ENCODE failure whose message happens to contain the compression phrase.
	ec := &verifyC8EncodeErrCodec{marshalErr: errors.New("proto marshal failed (error while compressing field table)")}
	backend := stubserver.StubServer{
		EmptyCallF: func(context.Context, *testpb.Empty) (*testpb.Empty, error) { return &testpb.Empty{}, nil },
	}
	if err := backend.Start([]grpc.ServerOption{grpc.ForceServerCodecV2(ec)}, grpc.WithTransportCredentials(insecure.NewCredentials())); err != nil {
		t.Fatal(err)
	}
	defer backend.Stop()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_, err := backend.Client.EmptyCall(ctx, &testpb.Empty{})
	t.Logf("client error: %v", err)
	time.Sleep(300 * time.Millisecond)

	lg.mu.Lock()
	defer lg.mu.Unlock()
	for _, ln := range lg.lines {
		if strings.Contains(ln, "failed to") && strings.Contains(ln, "response") {
			t.Logf("server log: %s", ln)
			if strings.Contains(ln, "compress") {
				t.Log("RESULT: encode failure misclassified as COMPRESS via err.Error() text match (claim CONFIRMED)")
			} else {
				t.Log("RESULT: classified as encode (no text-match misclassification observed)")
			}
		}
	}
}
