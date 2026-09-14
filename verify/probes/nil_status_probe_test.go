// Run: cd ~/repos/grpc-go && go test -v ./verify/probes -count=1
//
// Probes for audit v-6e432ec6 (claims C1-C12): does passing a nil *status.Status to
// the transport's WriteStatus dereference nil, and does a successful unary /
// streaming RPC reach the client with a response, grpc-status OK and clean EOF?
package probes

import (
	"context"
	"io"
	"strconv"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	istatus "google.golang.org/grpc/internal/status"
	"google.golang.org/grpc/internal/stubserver"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	testgrpc "google.golang.org/grpc/interop/grpc_testing"
	testpb "google.golang.org/grpc/interop/grpc_testing"
)

// TestNilStatusAccessorsUsedByWriteStatus evaluates exactly the expressions
// http2Server.writeStatus and serverHandlerTransport.writeStatus apply to the
// status argument, with a nil *status.Status (what processRPC passes when the
// handler returns nil, because status.FromError(nil) == (nil, true)).
func TestNilStatusAccessorsUsedByWriteStatus(t *testing.T) {
	st, ok := status.FromError(nil)
	if st != nil || !ok {
		t.Fatalf("status.FromError(nil) = (%v, %v), want (nil, true)", st, ok)
	}
	// http2_server.go writeStatus:
	code := strconv.Itoa(int(st.Code()))
	msg := st.Message()
	p := istatus.RawStatusProto(st)
	details := len(p.GetDetails())
	// handler_server.go writeStatus:
	hp := st.Proto()
	t.Logf("nil status: Code()=%s Message()=%q RawStatusProto()=%v details=%d Proto()=%v", code, msg, p, details, hp)
	if code != "0" || msg != "" || p != nil || details != 0 || hp != nil {
		t.Fatalf("nil status accessors returned unexpected values")
	}
	if st.Code() != codes.OK {
		t.Fatalf("nil status code = %v, want OK", st.Code())
	}
}

// TestSuccessfulRPCsCompleteCleanly performs a unary call and a bidi stream
// whose handlers return nil and checks response, grpc-status OK (trailers
// present) and io.EOF on the client.
func TestSuccessfulRPCsCompleteCleanly(t *testing.T) {
	ss := &stubserver.StubServer{
		UnaryCallF: func(ctx context.Context, in *testpb.SimpleRequest) (*testpb.SimpleResponse, error) {
			grpc.SetTrailer(ctx, metadata.Pairs("probe-trailer", "ok"))
			return &testpb.SimpleResponse{Payload: &testpb.Payload{Body: append([]byte("echo:"), in.GetPayload().GetBody()...)}}, nil
		},
		FullDuplexCallF: func(stream testgrpc.TestService_FullDuplexCallServer) error {
			for {
				_, err := stream.Recv()
				if err == io.EOF {
					stream.SetTrailer(metadata.Pairs("probe-trailer", "ok"))
					return nil
				}
				if err != nil {
					return err
				}
				if err := stream.Send(&testpb.StreamingOutputCallResponse{Payload: &testpb.Payload{Body: []byte("stream-ok")}}); err != nil {
					return err
				}
			}
		},
	}
	if err := ss.Start(nil); err != nil {
		t.Fatalf("start: %v", err)
	}
	defer ss.Stop()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var tr metadata.MD
	resp, err := ss.Client.UnaryCall(ctx, &testpb.SimpleRequest{Payload: &testpb.Payload{Body: []byte("hi")}}, grpc.Trailer(&tr))
	if err != nil {
		t.Fatalf("UnaryCall error: %v", err)
	}
	t.Logf("unary: resp=%q err=%v status.Code(err)=%v trailer probe-trailer=%v", resp.GetPayload().GetBody(), err, status.Code(err), tr.Get("probe-trailer"))
	if string(resp.GetPayload().GetBody()) != "echo:hi" || tr.Get("probe-trailer") == nil {
		t.Fatalf("unary response/trailer mismatch")
	}

	stream, err := ss.Client.FullDuplexCall(ctx)
	if err != nil {
		t.Fatalf("FullDuplexCall: %v", err)
	}
	if err := stream.Send(&testpb.StreamingOutputCallRequest{}); err != nil {
		t.Fatalf("Send: %v", err)
	}
	m, err := stream.Recv()
	if err != nil {
		t.Fatalf("Recv: %v", err)
	}
	if err := stream.CloseSend(); err != nil {
		t.Fatalf("CloseSend: %v", err)
	}
	_, eof := stream.Recv()
	t.Logf("stream: msg=%q final Recv err=%v trailer probe-trailer=%v", m.GetPayload().GetBody(), eof, stream.Trailer().Get("probe-trailer"))
	if eof != io.EOF {
		t.Fatalf("final Recv = %v, want io.EOF", eof)
	}
	if stream.Trailer().Get("probe-trailer") == nil {
		t.Fatalf("stream trailer missing")
	}
}
