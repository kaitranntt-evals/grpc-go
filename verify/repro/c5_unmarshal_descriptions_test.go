// Run: copy verify/repro to the target worktree, then
// go test ./verify/repro -run 'TestC5' -v -count=1
// C5: observes server-side unmarshal-failure status descriptions for unary
// and streaming RPCs.
package repro

import (
	"context"
	"errors"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/internal/stubserver"
	"google.golang.org/grpc/status"

	testgrpc "google.golang.org/grpc/interop/grpc_testing"
	testpb "google.golang.org/grpc/interop/grpc_testing"
)

func TestC5UnmarshalDescriptions(t *testing.T) {
	decodingErr := errors.New("decoding failed")
	ec := &errCodec{name: t.Name(), decodingErr: decodingErr}
	ss := &stubserver.StubServer{
		EmptyCallF: func(context.Context, *testpb.Empty) (*testpb.Empty, error) { return &testpb.Empty{}, nil },
		FullDuplexCallF: func(stream testgrpc.TestService_FullDuplexCallServer) error {
			for {
				if _, err := stream.Recv(); err != nil {
					return err
				}
			}
		},
	}
	if err := ss.Start([]grpc.ServerOption{grpc.ForceServerCodecV2(ec)}, grpc.WithTransportCredentials(insecure.NewCredentials())); err != nil {
		t.Fatal(err)
	}
	defer ss.Stop()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, err := ss.Client.EmptyCall(ctx, &testpb.Empty{})
	t.Logf("unary server unmarshal failure: code=%v msg=%q", status.Code(err), status.Convert(err).Message())

	stream, err := ss.Client.FullDuplexCall(ctx)
	if err != nil {
		t.Fatalf("FullDuplexCall failed: %v", err)
	}
	if err := stream.Send(&testpb.StreamingOutputCallRequest{Payload: &testpb.Payload{Body: []byte("x")}}); err != nil {
		t.Fatalf("Send failed: %v", err)
	}
	_, err = stream.Recv()
	t.Logf("streaming server unmarshal failure: code=%v msg=%q", status.Code(err), status.Convert(err).Message())
}
