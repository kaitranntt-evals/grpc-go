// Repro for C5: copy into test/ of branch evalon/grpc-go-se-7551960f, then: go test ./test -run TestVerifyC5 -count=1 -v
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

type failUnmarshalCodecC5 struct{ inner encoding.CodecV2 }

func (c failUnmarshalCodecC5) Marshal(v any) (mem.BufferSlice, error) { return c.inner.Marshal(v) }
func (c failUnmarshalCodecC5) Unmarshal(data mem.BufferSlice, v any) error {
	return fmt.Errorf("boom: forced unmarshal failure")
}
func (c failUnmarshalCodecC5) Name() string { return "failunmarshalc5" }

func TestVerifyC5StreamingUnmarshalText(t *testing.T) {
	encoding.RegisterCodecV2(failUnmarshalCodecC5{inner: encoding.GetCodecV2("proto")})

	ss := &stubserver.StubServer{
		FullDuplexCallF: func(stream testgrpc.TestService_FullDuplexCallServer) error {
			_, err := stream.Recv()
			return err
		},
	}
	if err := ss.Start(nil); err != nil {
		t.Fatalf("failed to start server: %v", err)
	}
	defer ss.Stop()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	stream, err := ss.Client.FullDuplexCall(ctx, grpc.ForceCodecV2(failUnmarshalCodecC5{inner: encoding.GetCodecV2("proto")}))
	if err != nil {
		t.Fatal(err)
	}
	if err := stream.Send(&testpb.StreamingOutputCallRequest{}); err != nil {
		t.Fatal(err)
	}
	_, err = stream.Recv()
	if err == nil {
		t.Fatalf("expected error")
	}
	st, _ := status.FromError(err)
	t.Logf("code=%v message=%q", st.Code(), st.Message())
	if st.Code() != codes.Internal {
		t.Errorf("code = %v, want Internal", st.Code())
	}
}
