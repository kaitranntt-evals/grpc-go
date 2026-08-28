// Repro for C11: copy into test/ of branch evalon/grpc-go-se-008dcfa6, then: go test ./test -run TestVerifyC11 -count=1 -v
package test

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"sync"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/encoding"
	"google.golang.org/grpc/grpclog"
	"google.golang.org/grpc/internal/stubserver"
	"google.golang.org/grpc/mem"
	"google.golang.org/grpc/status"

	testgrpc "google.golang.org/grpc/interop/grpc_testing"
	testpb "google.golang.org/grpc/interop/grpc_testing"
)

type c11Codec struct {
	inner  encoding.CodecV2
	errMsg string
}

func (c c11Codec) Marshal(v any) (mem.BufferSlice, error) { return c.inner.Marshal(v) }
func (c c11Codec) Unmarshal(data mem.BufferSlice, v any) error {
	return fmt.Errorf("%s", c.errMsg)
}
func (c c11Codec) Name() string { return c.inner.Name() }

// c11Compressor fails on Compress writes with a configurable error text.
type c11Compressor struct {
	errMsg string
}

type c11FailWriter struct{ errMsg string }

func (w c11FailWriter) Write(p []byte) (int, error) { return 0, fmt.Errorf("%s", w.errMsg) }
func (w c11FailWriter) Close() error                { return nil }

func (c c11Compressor) Compress(w io.Writer) (io.WriteCloser, error) {
	return c11FailWriter{errMsg: c.errMsg}, nil
}
func (c c11Compressor) Decompress(r io.Reader) (io.Reader, error) { return r, nil }
func (c c11Compressor) Name() string                              { return "c11comp" }

type c11LogBuf struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (l *c11LogBuf) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.buf.Write(p)
}
func (l *c11LogBuf) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.buf.String()
}

// Receive path: a plain codec Unmarshal error is wrapped with the fixed
// streaming prefix by recvAndDecompress and then rewritten by RecvMsg's
// strings.HasPrefix match into the unary wording.
func TestVerifyC11RecvRewriteByText(t *testing.T) {
	ss := &stubserver.StubServer{
		EmptyCallF: func(ctx context.Context, in *testpb.Empty) (*testpb.Empty, error) {
			return &testpb.Empty{}, nil
		},
	}
	codec := c11Codec{inner: encoding.GetCodecV2("proto"), errMsg: "boom"}
	if err := ss.Start([]grpc.ServerOption{grpc.ForceServerCodecV2(codec)}); err != nil {
		t.Fatalf("start: %v", err)
	}
	defer ss.Stop()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	client := testgrpc.NewTestServiceClient(ss.CC)
	_, err := client.EmptyCall(ctx, &testpb.Empty{})
	t.Logf("unary status message: %q", status.Convert(err).Message())
}

// Send path: a compression-stage failure whose error text contains the word
// "marshaling" is classified by strings.Contains as an encoding failure and
// logged as "failed to encode response"; the same failure without that word
// is logged as "failed to compress response".
func TestVerifyC11SendClassifyByText(t *testing.T) {
	run := func(errMsg string) string {
		logBuf := &c11LogBuf{}
		grpclog.SetLoggerV2(grpclog.NewLoggerV2(logBuf, logBuf, logBuf))
		defer grpclog.SetLoggerV2(grpclog.NewLoggerV2(io.Discard, io.Discard, io.Discard))
		comp := c11Compressor{errMsg: errMsg}
		encoding.RegisterCompressor(comp)
		ss := &stubserver.StubServer{
			UnaryCallF: func(ctx context.Context, in *testpb.SimpleRequest) (*testpb.SimpleResponse, error) {
				if err := grpc.SetSendCompressor(ctx, "c11comp"); err != nil {
					t.Fatalf("SetSendCompressor: %v", err)
				}
				return &testpb.SimpleResponse{Payload: &testpb.Payload{Body: []byte("x")}}, nil
			},
		}
		if err := ss.Start(nil); err != nil {
			t.Fatalf("start: %v", err)
		}
		defer ss.Stop()
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		client := testgrpc.NewTestServiceClient(ss.CC)
		_, err := client.UnaryCall(ctx, &testpb.SimpleRequest{})
		_ = err
		time.Sleep(200 * time.Millisecond)
		return logBuf.String()
	}
	logsA := run("marshaling buffer failed")
	logsB := run("buffer write failed")
	for _, line := range []struct{ name, logs string }{{"contains-marshaling", logsA}, {"plain", logsB}} {
		t.Logf("[%s] encode-classified=%v compress-classified=%v", line.name,
			bytes.Contains([]byte(line.logs), []byte("failed to encode response")),
			bytes.Contains([]byte(line.logs), []byte("failed to compress response")))
	}
}
