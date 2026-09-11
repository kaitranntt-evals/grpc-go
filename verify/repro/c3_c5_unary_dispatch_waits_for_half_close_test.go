// Run: cp verify/repro/c3_c5_unary_dispatch_waits_for_half_close_test.go test/ && go test -tags verifyrepro ./test -run '^Test$/^C3C5Repro' -count=1 -v; rm test/c3_c5_unary_dispatch_waits_for_half_close_test.go
// The test FAILS when the problem is present (audited branch) and PASSES on the base commit, where the
// handler is dispatched and the response arrives while the client's send side is still open.

//go:build verifyrepro

package test

import (
	"context"
	"runtime"
	"strings"
	"testing"
	"time"

	"golang.org/x/net/http2"
	testpb "google.golang.org/grpc/interop/grpc_testing"
)

// C3/C5: a raw HTTP/2 client sends HEADERS plus exactly one complete unary
// request message and keeps its send side open (no END_STREAM). On the audited
// server the unary handler is not dispatched and no response arrives until the
// client half-closes, because serverStream.RecvMsg performs a second recv()
// (waiting for io.EOF) before returning the first message to the handler.
func (s) TestC3C5Repro_UnaryDispatchWaitsForHalfClose(t *testing.T) {
	te := newTest(t, tcpClearEnv)
	dispatched := make(chan time.Duration, 1)
	start := time.Now()
	ts := &funcServer{unaryCall: func(context.Context, *testpb.SimpleRequest) (*testpb.SimpleResponse, error) {
		dispatched <- time.Since(start)
		return &testpb.SimpleResponse{}, nil
	}}
	te.startServer(ts)
	defer te.tearDown()

	te.withServerTester(func(st *serverTester) {
		// Single frame reader; it exits when withServerTester closes st.cc.
		frames := make(chan http2.Frame, 64)
		go func() {
			defer close(frames)
			for {
				f, err := st.fr.ReadFrame()
				if err != nil {
					return
				}
				frames <- f
			}
		}()

		st.writeHeadersGRPC(1, "/grpc.testing.TestService/UnaryCall", false)
		// One complete gRPC message: 5-byte prefix (uncompressed, length 0) = empty SimpleRequest.
		st.writeData(1, false, []byte{0, 0, 0, 0, 0})
		t.Logf("sent one complete request at +%v; client send side left open", time.Since(start))

		const clientDeadline = 3 * time.Second
		deadline := time.After(clientDeadline)
		dispatchedBeforeHalfClose := false
		trailersBeforeHalfClose := false
	wait:
		for {
			select {
			case d := <-dispatched:
				dispatchedBeforeHalfClose = true
				t.Logf("unary handler dispatched at +%v while client send side still open", d)
			case f := <-frames:
				if mh, ok := f.(*http2.MetaHeadersFrame); ok && mh.StreamID == 1 && mh.StreamEnded() {
					trailersBeforeHalfClose = true
					t.Logf("response trailers received at +%v while client send side still open", time.Since(start))
					break wait
				}
			case <-deadline:
				break wait
			}
		}
		t.Logf("before half-close: handler dispatched=%v, response trailers received=%v (client deadline %v)",
			dispatchedBeforeHalfClose, trailersBeforeHalfClose, clientDeadline)

		if !dispatchedBeforeHalfClose {
			// Trace where the server side is parked: the second recv inside RecvMsg.
			buf := make([]byte, 1<<20)
			n := runtime.Stack(buf, true)
			for _, g := range strings.Split(string(buf[:n]), "\n\n") {
				if strings.Contains(g, "wrapUnaryHandler") {
					for _, line := range strings.Split(g, "\n") {
						if strings.Contains(line, "google.golang.org/grpc.") || strings.Contains(line, "stream.go:") || strings.Contains(line, "rpc_util.go:") || strings.Contains(line, "server.go:") {
							t.Logf("server goroutine: %s", strings.TrimSpace(line))
						}
					}
				}
			}
		}

		st.writeData(1, true, nil) // half-close
		t.Logf("client half-closed at +%v", time.Since(start))
		after := time.After(clientDeadline)
	drain:
		for !trailersBeforeHalfClose {
			select {
			case d := <-dispatched:
				t.Logf("unary handler dispatched at +%v (after half-close)", d)
			case f := <-frames:
				if mh, ok := f.(*http2.MetaHeadersFrame); ok && mh.StreamID == 1 && mh.StreamEnded() {
					t.Logf("response trailers received at +%v (after half-close)", time.Since(start))
					break drain
				}
			case <-after:
				break drain
			}
		}
		if !dispatchedBeforeHalfClose || !trailersBeforeHalfClose {
			t.Errorf("C3/C5 CONFIRMED: unary dispatch waited for client half-close (handler dispatched before half-close=%v, response before half-close=%v; base commit gives true, true)",
				dispatchedBeforeHalfClose, trailersBeforeHalfClose)
		}
	})
}
