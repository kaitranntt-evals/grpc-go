// Run: cp verify/repro/c3_c12_unary_decode_error_message_test.go test/ && cd test && go test -v -run 'TestVerifyC3UnaryDecodeErrorMessage' -count=1 .
//
// Invokes a unary RPC against a server whose codec fails while decoding the
// request and prints the exact client-visible status description.
package test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/encoding"
	"google.golang.org/grpc/internal/stubserver"
	"google.golang.org/grpc/mem"

	testpb "google.golang.org/grpc/interop/grpc_testing"
)

type verifyC3ErrCodec struct {
	decodingErr error
}

func (c *verifyC3ErrCodec) Marshal(v any) (mem.BufferSlice, error) {
	return encoding.GetCodecV2("proto").Marshal(v)
}

func (c *verifyC3ErrCodec) Unmarshal(_ mem.BufferSlice, _ any) error {
	return c.decodingErr
}

func (c *verifyC3ErrCodec) Name() string { return "proto" }

func TestVerifyC3UnaryDecodeErrorMessage(t *testing.T) {
	decodingErr := errors.New("verify-decoding-failed")
	ec := &verifyC3ErrCodec{decodingErr: decodingErr}
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
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	t.Logf("client-visible unary decode error: %v", err)
	switch {
	case strings.Contains(err.Error(), "grpc: error unmarshalling request"):
		t.Log("RESULT: legacy unary description preserved (claim REFUTED)")
	case strings.Contains(err.Error(), "grpc: failed to unmarshal the received message"):
		t.Log("RESULT: unary decode failure exposes streaming-path description (claim CONFIRMED)")
	default:
		t.Log("RESULT: neither expected description observed")
	}
}
