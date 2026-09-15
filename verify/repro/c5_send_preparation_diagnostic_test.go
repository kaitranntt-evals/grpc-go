// Run (on evalon/grpc-go-se-880ceb42, repo root): cp verify/repro/c5_send_preparation_diagnostic_test.go test/ && go test -tags verify_repro ./test -run 'Test/^Verify_C5_SendPreparationDiagnostics$' -count=1 -v
//
// Probe for C5: triggers the response-preparation failure branches (codec
// Marshal failure and compressor Compress failure) for a unary and a streaming
// RPC and captures every grpclog line. A healthy diagnostic carries the owning
// server identity ("[Server #N]"); the suspected defect is that the unified
// serverStream.SendMsg logs without it.

//go:build verify_repro

package test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log"
	"strings"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/encoding"
	"google.golang.org/grpc/grpclog"
	"google.golang.org/grpc/internal/stubserver"
	"google.golang.org/grpc/mem"
	"google.golang.org/grpc/status"

	testgrpc "google.golang.org/grpc/interop/grpc_testing"
	testpb "google.golang.org/grpc/interop/grpc_testing"
)

type verifyC5FailMarshalCodec struct{ encoding.CodecV2 }

func (verifyC5FailMarshalCodec) Name() string { return "verify_c5_fail_marshal" }
func (verifyC5FailMarshalCodec) Marshal(any) (mem.BufferSlice, error) {
	return nil, errors.New("verify: synthetic marshal failure")
}

type verifyC5FailCompressor struct{}

func (verifyC5FailCompressor) Name() string { return "verify_c5_fail_compress" }
func (verifyC5FailCompressor) Compress(io.Writer) (io.WriteCloser, error) {
	return nil, errors.New("verify: synthetic compress failure")
}
func (verifyC5FailCompressor) Decompress(r io.Reader) (io.Reader, error) { return r, nil }

func init() { encoding.RegisterCompressor(verifyC5FailCompressor{}) }

// verifyC5CaptureLogs routes grpclog to a buffer for the duration of the test.
// (grpctest re-installs its test logger at the start of the next test.)
func verifyC5CaptureLogs(t *testing.T) *bytes.Buffer {
	buf := &bytes.Buffer{}
	grpclog.SetLoggerV2(grpclog.NewLoggerV2(io.Discard, io.Discard, log.New(buf, "", 0).Writer()))
	t.Cleanup(func() { grpclog.SetLoggerV2(grpclog.NewLoggerV2(io.Discard, io.Discard, io.Discard)) })
	return buf
}

func verifyC5Run(t *testing.T, name string, sopts []grpc.ServerOption, callOpts ...grpc.CallOption) {
	t.Run(name, func(t *testing.T) {
		buf := verifyC5CaptureLogs(t)
		ss := &stubserver.StubServer{
			UnaryCallF: func(context.Context, *testpb.SimpleRequest) (*testpb.SimpleResponse, error) {
				return &testpb.SimpleResponse{Payload: &testpb.Payload{Body: []byte("x")}}, nil
			},
			FullDuplexCallF: func(stream testgrpc.TestService_FullDuplexCallServer) error {
				if _, err := stream.Recv(); err != nil {
					return err
				}
				return stream.Send(&testpb.StreamingOutputCallResponse{Payload: &testpb.Payload{Body: []byte("x")}})
			},
		}
		if err := ss.Start(sopts); err != nil {
			t.Fatalf("start: %v", err)
		}
		defer ss.Stop()
		ctx, cancel := context.WithTimeout(context.Background(), defaultTestTimeout)
		defer cancel()

		_, uerr := ss.Client.UnaryCall(ctx, &testpb.SimpleRequest{}, callOpts...)
		t.Logf("unary  status: %v", uerr)
		stream, err := ss.Client.FullDuplexCall(ctx, callOpts...)
		if err != nil {
			t.Fatalf("FullDuplexCall: %v", err)
		}
		if err := stream.Send(&testpb.StreamingOutputCallRequest{}); err != nil {
			t.Fatalf("Send: %v", err)
		}
		_, serr := stream.Recv()
		t.Logf("stream status: %v", serr)
		if status.Code(uerr) != codes.Internal || status.Code(serr) != codes.Internal {
			t.Fatalf("expected codes.Internal for both RPCs")
		}

		var diag []string
		for _, line := range strings.Split(buf.String(), "\n") {
			if strings.Contains(line, "failed to encode response") || strings.Contains(line, "failed to compress response") {
				diag = append(diag, line)
			}
		}
		if len(diag) == 0 {
			t.Fatalf("no server-side response-preparation diagnostic was logged; full log:\n%s", buf.String())
		}
		for _, d := range diag {
			t.Logf("diagnostic: %s", d)
			if !strings.Contains(d, "[Server #") {
				t.Errorf("diagnostic lacks owning server identity ([Server #N]): %q", d)
			}
		}
	})
}

func (s) TestVerify_C5_SendPreparationDiagnostics(t *testing.T) {
	verifyC5Run(t, "MarshalFailure", []grpc.ServerOption{grpc.ForceServerCodecV2(verifyC5FailMarshalCodec{encoding.GetCodecV2("proto")})})
	verifyC5Run(t, "CompressFailure", nil, grpc.UseCompressor(verifyC5FailCompressor{}.Name()))
}
