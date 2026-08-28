// Run: cd ~/repos/grpc-go && go test ./verify/repro -run 'TestC2' -v -count=1
// C2: observes unary protocol-error status descriptions on the audited branch.
package repro

import (
	"context"
	"errors"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/encoding"
	"google.golang.org/grpc/encoding/proto"
	"google.golang.org/grpc/internal/stubserver"
	"google.golang.org/grpc/mem"
	"google.golang.org/grpc/status"

	testpb "google.golang.org/grpc/interop/grpc_testing"
)

type errCodec struct {
	name        string
	decodingErr error
}

func (c *errCodec) Marshal(v any) (mem.BufferSlice, error) {
	return encoding.GetCodecV2(proto.Name).Marshal(v)
}

func (c *errCodec) Unmarshal(data mem.BufferSlice, v any) error {
	if c.decodingErr != nil {
		return c.decodingErr
	}
	return encoding.GetCodecV2(proto.Name).Unmarshal(data, v)
}

func (c *errCodec) Name() string { return c.name }

func TestC2UnaryUnmarshalErrorText(t *testing.T) {
	decodingErr := errors.New("decoding failed")
	ec := &errCodec{name: t.Name(), decodingErr: decodingErr}
	ss := &stubserver.StubServer{
		EmptyCallF: func(context.Context, *testpb.Empty) (*testpb.Empty, error) { return &testpb.Empty{}, nil },
	}
	if err := ss.Start([]grpc.ServerOption{grpc.ForceServerCodecV2(ec)}, grpc.WithTransportCredentials(insecure.NewCredentials())); err != nil {
		t.Fatal(err)
	}
	defer ss.Stop()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, err := ss.Client.EmptyCall(ctx, &testpb.Empty{})
	t.Logf("unary unmarshal failure status: code=%v msg=%q", status.Code(err), status.Convert(err).Message())
}

func TestC2UnaryMaxSendSizeErrorText(t *testing.T) {
	ss := &stubserver.StubServer{
		UnaryCallF: func(ctx context.Context, in *testpb.SimpleRequest) (*testpb.SimpleResponse, error) {
			return &testpb.SimpleResponse{Payload: &testpb.Payload{Body: make([]byte, 1024)}}, nil
		},
	}
	if err := ss.Start([]grpc.ServerOption{grpc.MaxSendMsgSize(16)}); err != nil {
		t.Fatal(err)
	}
	defer ss.Stop()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, err := ss.Client.UnaryCall(ctx, &testpb.SimpleRequest{})
	t.Logf("unary max-send-size failure status: code=%v msg=%q", status.Code(err), status.Convert(err).Message())
}
