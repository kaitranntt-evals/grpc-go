// Run (on evalon/grpc-go-se-5fd20000): cp verify/repro/c8_recvmsg_bypasses_shared_recv_test.go encoding/ && go test -tags verifyrepro ./encoding -run '^TestC8Repro' -count=1 -v; rm encoding/c8_recvmsg_bypasses_shared_recv_test.go
// The test FAILS when the problem is present (the server-side codec Unmarshal is reached from serverStream.RecvMsg
// directly, not through the shared grpc.recv helper, and the error text differs from recv's) and PASSES on the base
// commit 0c51461d, where grpc.recv is on the Unmarshal call stack.

//go:build verifyrepro

package encoding_test

import (
	"context"
	"errors"
	"runtime"
	"strings"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/encoding"
	"google.golang.org/grpc/encoding/proto"
	"google.golang.org/grpc/internal/stubserver"
	"google.golang.org/grpc/mem"
	"google.golang.org/grpc/status"

	testgrpc "google.golang.org/grpc/interop/grpc_testing"
	testpb "google.golang.org/grpc/interop/grpc_testing"
)

// c8TracingCodec records, for every server-side Unmarshal, whether the shared
// receive helper google.golang.org/grpc.recv is on the call stack, and returns
// a decode error so the error-conversion text can be compared too.
type c8TracingCodec struct {
	stacks chan string
}

func (c *c8TracingCodec) Marshal(v any) (mem.BufferSlice, error) {
	return encoding.GetCodecV2(proto.Name).Marshal(v)
}

func (c *c8TracingCodec) Unmarshal(mem.BufferSlice, any) error {
	buf := make([]byte, 1<<16)
	n := runtime.Stack(buf, false)
	c.stacks <- string(buf[:n])
	return errors.New("c8 decode failure")
}

func (c *c8TracingCodec) Name() string { return "c8tracing" }

func TestC8Repro_ServerStreamRecvMsgBypassesSharedRecv(t *testing.T) {
	codec := &c8TracingCodec{stacks: make(chan string, 8)}
	server := &stubserver.StubServer{
		FullDuplexCallF: func(stream testgrpc.TestService_FullDuplexCallServer) error {
			_, err := stream.Recv()
			return err
		},
	}
	if err := server.Start([]grpc.ServerOption{grpc.ForceServerCodecV2(codec)}, grpc.WithTransportCredentials(insecure.NewCredentials())); err != nil {
		t.Fatalf("Start() failed: %v", err)
	}
	defer server.Stop()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	stream, err := server.Client.FullDuplexCall(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := stream.Send(&testpb.StreamingOutputCallRequest{}); err != nil {
		t.Fatal(err)
	}
	_, err = stream.Recv()
	msg := status.Convert(err).Message()
	t.Logf("C8: streaming Recv status code=%v desc=%q", status.Code(err), msg)

	var serverStack string
	select {
	case serverStack = <-codec.stacks:
	case <-time.After(2 * time.Second):
		t.Fatal("server-side Unmarshal was not observed")
	}
	viaRecv := strings.Contains(serverStack, "google.golang.org/grpc.recv(")
	for _, line := range strings.Split(serverStack, "\n") {
		if strings.Contains(line, "google.golang.org/grpc.") && !strings.Contains(line, "encoding_test") {
			t.Logf("C8: server Unmarshal caller: %s", strings.TrimSpace(line))
		}
	}
	t.Logf("C8: server-side Unmarshal reached through shared grpc.recv helper = %v", viaRecv)
	if !viaRecv || !strings.Contains(msg, "grpc: failed to unmarshal the received message") {
		t.Errorf("C8 CONFIRMED: serverStream.RecvMsg unmarshals/converts errors itself (via recv=%v, desc=%q) instead of delegating to grpc.recv", viaRecv, msg)
	}
}
