// Run: cd <repo> && go test -v ./test -run '^TestVerify_ServerSendFailureDiagnostics$' -count=1 2>&1 | grep -E 'failed to (encode|compress) response|PASS|FAIL'
// Copy into the target branch's test/ directory and add -tags verify_repro to the go test command.

//go:build verify_repro

package test

import (
	"context"
	"errors"
	"io"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/encoding"
	"google.golang.org/grpc/encoding/proto"
	"google.golang.org/grpc/internal/stubserver"
	"google.golang.org/grpc/mem"

	testpb "google.golang.org/grpc/interop/grpc_testing"
)

type verifyMarshalErrCodec struct{}

func (verifyMarshalErrCodec) Marshal(v any) (mem.BufferSlice, error) {
	if _, ok := v.(*testpb.SimpleResponse); ok {
		return nil, errors.New("verify-c9 marshal failure")
	}
	return encoding.GetCodecV2(proto.Name).Marshal(v)
}
func (verifyMarshalErrCodec) Unmarshal(d mem.BufferSlice, v any) error {
	return encoding.GetCodecV2(proto.Name).Unmarshal(d, v)
}
func (verifyMarshalErrCodec) Name() string { return proto.Name }

type verifyBadCompressor struct{}

type verifyBadWriter struct{}

func (verifyBadWriter) Write(p []byte) (int, error) {
	return 0, errors.New("verify-c9 compress failure")
}
func (verifyBadWriter) Close() error { return nil }

func (verifyBadCompressor) Compress(w io.Writer) (io.WriteCloser, error) {
	return verifyBadWriter{}, nil
}
func (verifyBadCompressor) Decompress(r io.Reader) (io.Reader, error) { return r, nil }
func (verifyBadCompressor) Name() string                              { return "verify-bad" }

func TestVerify_ServerSendFailureDiagnostics(t *testing.T) {
	encoding.RegisterCompressor(verifyBadCompressor{})

	// Part 1: response encoding failure.
	ss := &stubserver.StubServer{
		UnaryCallF: func(ctx context.Context, in *testpb.SimpleRequest) (*testpb.SimpleResponse, error) {
			return &testpb.SimpleResponse{}, nil
		},
	}
	if err := ss.Start([]grpc.ServerOption{grpc.ForceServerCodecV2(verifyMarshalErrCodec{})}); err != nil {
		t.Fatalf("start: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_, err := ss.Client.UnaryCall(ctx, &testpb.SimpleRequest{})
	t.Logf("encoding-failure RPC err=%v", err)
	ss.Stop()

	// Part 2: response compression failure via server-set compressor.
	ss2 := &stubserver.StubServer{
		UnaryCallF: func(ctx context.Context, in *testpb.SimpleRequest) (*testpb.SimpleResponse, error) {
			if err := grpc.SetSendCompressor(ctx, "verify-bad"); err != nil {
				t.Errorf("SetSendCompressor: %v", err)
			}
			return &testpb.SimpleResponse{Payload: &testpb.Payload{Body: []byte("data")}}, nil
		},
	}
	if err := ss2.Start(nil); err != nil {
		t.Fatalf("start2: %v", err)
	}
	defer ss2.Stop()
	_, err = ss2.Client.UnaryCall(ctx, &testpb.SimpleRequest{})
	t.Logf("compression-failure RPC err=%v", err)
}
