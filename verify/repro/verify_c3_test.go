// Repro for C3: go test ./verify/repro -run TestVerifyC3UnaryUnmarshalText -count=1 -v (audited branch)
package test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/encoding"
	"google.golang.org/grpc/internal/stubserver"
	"google.golang.org/grpc/mem"
	"google.golang.org/grpc/status"

	testgrpc "google.golang.org/grpc/interop/grpc_testing"
	testpb "google.golang.org/grpc/interop/grpc_testing"
)

type failUnmarshalCodec struct{ inner encoding.CodecV2 }

func (c failUnmarshalCodec) Marshal(v any) (mem.BufferSlice, error) { return c.inner.Marshal(v) }
func (c failUnmarshalCodec) Unmarshal(data mem.BufferSlice, v any) error {
	return fmt.Errorf("boom: forced unmarshal failure")
}
func (c failUnmarshalCodec) Name() string { return "failunmarshal" }

func TestVerifyC3UnaryUnmarshalText(t *testing.T) {
	encoding.RegisterCodecV2(failUnmarshalCodec{inner: encoding.GetCodecV2("proto")})

	ss := &stubserver.StubServer{
		UnaryCallF: func(ctx context.Context, in *testpb.SimpleRequest) (*testpb.SimpleResponse, error) {
			return &testpb.SimpleResponse{}, nil
		},
	}
	if err := ss.Start(nil); err != nil {
		t.Fatalf("failed to start server: %v", err)
	}
	defer ss.Stop()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	client := testgrpc.NewTestServiceClient(ss.CC)
	_, err := client.UnaryCall(ctx, &testpb.SimpleRequest{}, grpc.ForceCodecV2(failUnmarshalCodec{inner: encoding.GetCodecV2("proto")}))
	if err == nil {
		t.Fatalf("expected error")
	}
	st, _ := status.FromError(err)
	t.Logf("code=%v message=%q", st.Code(), st.Message())
	if st.Code() != codes.Internal {
		t.Errorf("code = %v, want Internal", st.Code())
	}
}
