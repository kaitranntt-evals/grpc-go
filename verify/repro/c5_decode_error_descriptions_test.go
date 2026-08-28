// Run: cp verify/repro/c5_decode_error_descriptions_test.go <worktree>/test/ && cd <worktree>/test && go test -v -run 'TestVerifyC5DecodeErrorDescriptions' -count=1 .
//
// Prints the client-visible status description for a server-side request
// decode failure on a unary RPC and on a bidi-streaming RPC.
package test

import (
	"context"
	"errors"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/encoding"
	"google.golang.org/grpc/internal/stubserver"
	"google.golang.org/grpc/mem"

	testpb "google.golang.org/grpc/interop/grpc_testing"
)

type verifyC5ErrCodec struct {
	decodingErr error
}

func (c *verifyC5ErrCodec) Marshal(v any) (mem.BufferSlice, error) {
	return encoding.GetCodecV2("proto").Marshal(v)
}

func (c *verifyC5ErrCodec) Unmarshal(_ mem.BufferSlice, _ any) error {
	return c.decodingErr
}

func (c *verifyC5ErrCodec) Name() string { return "proto" }

func TestVerifyC5DecodeErrorDescriptions(t *testing.T) {
	ec := &verifyC5ErrCodec{decodingErr: errors.New("verify-decoding-failed")}
	backend := stubserver.StubServer{
		EmptyCallF: func(context.Context, *testpb.Empty) (*testpb.Empty, error) { return &testpb.Empty{}, nil },
		FullDuplexCallF: func(stream testpb.TestService_FullDuplexCallServer) error {
			_, err := stream.Recv()
			return err
		},
	}
	if err := backend.Start([]grpc.ServerOption{grpc.ForceServerCodecV2(ec)}, grpc.WithTransportCredentials(insecure.NewCredentials())); err != nil {
		t.Fatal(err)
	}
	defer backend.Stop()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	_, uErr := backend.Client.EmptyCall(ctx, &testpb.Empty{})
	t.Logf("UNARY decode error: %v", uErr)

	stream, err := backend.Client.FullDuplexCall(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := stream.Send(&testpb.StreamingOutputCallRequest{}); err != nil {
		t.Fatal(err)
	}
	_, sErr := stream.Recv()
	t.Logf("STREAMING decode error: %v", sErr)
}
