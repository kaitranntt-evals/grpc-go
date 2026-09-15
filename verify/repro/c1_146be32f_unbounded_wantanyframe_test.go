// Run (on evalon/grpc-go-se-146be32f, repo root): cp verify/repro/c1_146be32f_unbounded_wantanyframe_test.go test/ && go test -tags verify_repro ./test -run 'Test/^Verify_C1_UnboundedWantAnyFrame$' -count=1 -v -timeout 8s
//
// Probe for C1 (branch 146be32f): replays the raw-frame read loop that the
// changed testClientRequestBodyErrorUnexpectedEOF uses (`for { f :=
// st.wantAnyFrame() ... }`) against a server whose handler never completes the
// stream. If the read is bounded (deadline / finite iteration / cancellation
// closing the connection) the test fails on its own; if it is unbounded the
// process is killed by `go test -timeout` with the goroutine parked inside
// wantAnyFrame -> http2.Framer.ReadFrame.

//go:build verify_repro

package test

import (
	"context"
	"testing"
	"time"

	"golang.org/x/net/http2"

	testgrpc "google.golang.org/grpc/interop/grpc_testing"
	testpb "google.golang.org/grpc/interop/grpc_testing"
)

func (s) TestVerify_C1_UnboundedWantAnyFrame(t *testing.T) {
	te := newTest(t, tcpClearRREnv)
	ts := &funcServer{
		unaryCall: func(context.Context, *testpb.SimpleRequest) (*testpb.SimpleResponse, error) {
			return &testpb.SimpleResponse{}, nil
		},
		fullDuplexCall: func(stream testgrpc.TestService_FullDuplexCallServer) error {
			<-stream.Context().Done() // never completes on its own
			return stream.Context().Err()
		},
	}
	te.startServer(ts)
	defer te.tearDown()

	start := time.Now()
	te.withServerTester(func(st *serverTester) {
		st.writeHeadersGRPC(1, "/grpc.testing.TestService/FullDuplexCall", false)
		// A well-formed but incomplete request: no END_STREAM, so the server
		// legitimately keeps waiting and never sends status trailers.
		st.writeData(1, false, []byte{0, 0, 0, 0, 0})
		for {
			f := st.wantAnyFrame()
			t.Logf("t=%v got frame %T", time.Since(start).Round(time.Millisecond), f)
			switch f := f.(type) {
			case *http2.MetaHeadersFrame:
				if !f.StreamEnded() {
					continue
				}
				return
			case *http2.DataFrame, *http2.RSTStreamFrame:
				t.Fatalf("received %T, want status headers", f)
			}
		}
	})
}
