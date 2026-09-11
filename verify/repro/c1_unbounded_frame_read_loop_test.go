// Run: cp verify/repro/c1_unbounded_frame_read_loop_test.go test/ && go test -tags verifyrepro ./test -run '^Test$/^C1Repro' -count=1 -v; rm test/c1_unbounded_frame_read_loop_test.go
// The sub-test for the wantAnyFrame loop FAILS when the problem is present (the loop is still parked in
// ReadFrame 5s after the last frame, i.e. it has no finite read bound); the readFrame loop sub-test PASSES.

//go:build verifyrepro

package test

import (
	"context"
	"runtime"
	"strings"
	"testing"
	"time"

	"golang.org/x/net/http2"
	testgrpc "google.golang.org/grpc/interop/grpc_testing"
	testpb "google.golang.org/grpc/interop/grpc_testing"
)

// C1: the changed raw-frame test on evalon/grpc-go-se-3c6bea32 and
// evalon/grpc-go-se-0649e9b2 loops `for { switch st.wantAnyFrame().(type) ... }`
// until a trailer for stream 1 arrives. wantAnyFrame blocks on st.fr.ReadFrame()
// with no deadline, so a peer that does not send the trailer parks the test
// forever (only `go test -timeout` ends it). evalon/grpc-go-se-91d5a035 uses
// st.readFrame(), whose 2s timer makes the same loop terminate. Both loops are
// reproduced here against a handler that is held back for 5s; the bounded loop
// gives up at ~2s, the unbounded one is still blocked when the 5s mark passes
// and only returns once the handler is released and the trailer is sent.
func (s) TestC1Repro_FrameReadLoopBound(t *testing.T) {
	for _, tc := range []struct {
		name    string
		bounded bool
	}{
		{name: "readFrame_loop_terminates_at_2s_bound", bounded: true},
		{name: "wantAnyFrame_loop_has_no_read_bound", bounded: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			te := newTest(t, tcpClearEnv)
			unblock := make(chan struct{})
			unblockOnce := func() {
				select {
				case <-unblock:
				default:
					close(unblock)
				}
			}
			t.Cleanup(unblockOnce)
			ts := &funcServer{
				unaryCall: func(context.Context, *testpb.SimpleRequest) (*testpb.SimpleResponse, error) {
					return nil, nil
				},
				streamingInputCall: func(testgrpc.TestService_StreamingInputCallServer) error {
					<-unblock // the peer does not send the trailer until released
					return nil
				},
			}
			te.startServer(ts)
			defer te.tearDown()
			te.withServerTester(func(st *serverTester) {
				st.writeHeadersGRPC(1, "/grpc.testing.TestService/StreamingInputCall", false)
				st.writeData(1, true, []byte{0, 0, 0, 0, 5})
				start := time.Now()
				done := make(chan string, 1)
				go func() {
					for {
						var frame http2.Frame
						if tc.bounded {
							f, err := st.readFrame()
							if err != nil {
								done <- "readFrame returned error: " + err.Error()
								return
							}
							frame = f
						} else {
							frame = st.wantAnyFrame()
						}
						if mh, ok := frame.(*http2.MetaHeadersFrame); ok && mh.StreamID == 1 && mh.StreamEnded() {
							done <- "trailer for stream 1 received"
							return
						}
					}
				}()

				const bound = 5 * time.Second
				select {
				case why := <-done:
					t.Logf("loop terminated after %v: %s", time.Since(start), why)
				case <-time.After(bound):
					buf := make([]byte, 1<<20)
					n := runtime.Stack(buf, true)
					for _, g := range strings.Split(string(buf[:n]), "\n\n") {
						if strings.Contains(g, "wantAnyFrame") {
							for _, line := range strings.Split(g, "\n") {
								if strings.Contains(line, "servertester.go:") || strings.Contains(line, "ReadFrame") {
									t.Logf("blocked reader: %s", strings.TrimSpace(line))
								}
							}
						}
					}
					t.Errorf("C1 CONFIRMED: frame read loop still blocked %v after the last frame; no finite read bound", bound)
					unblockOnce() // release the handler so the trailer arrives and the loop ends
					t.Logf("loop terminated after %v: %s", time.Since(start), <-done)
				}
			})
		})
	}
}
