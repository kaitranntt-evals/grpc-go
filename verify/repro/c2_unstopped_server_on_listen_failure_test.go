// Run: cp verify/repro/c2_unstopped_server_on_listen_failure_test.go . && go test . -run 'Test/VerifyC2_' -tags verify_repro -count=1 -v ; rm c2_unstopped_server_on_listen_failure_test.go
//
// Mirrors the explicit-server sequence in server_stream_pipeline_test.go
// (TestServerStreamPipeline_DescriptorRouting):
//
//	server := grpc.NewServer(...)
//	server.RegisterService(desc, impl)
//	ss := &stubserver.StubServer{S: server}
//	if err := ss.Start(nil); err != nil { t.Fatal(err) }
//	defer ss.Stop()
//
// with listener setup forced to fail (bogus network), and checks whether the
// explicitly created server is stopped on that failure path.  A stopped server
// makes Serve return grpc.ErrServerStopped immediately; an unstopped one
// blocks in Serve.
//go:build verify_repro

package grpc_test

import (
	"errors"
	"net"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/internal/stubserver"
)

// startExplicitServer is the test's sequence with t.Fatal replaced by a
// return so the outcome can be inspected.  ss.Stop is registered only after
// Start succeeds, exactly as in the changed test.
func startExplicitServer(t *testing.T, ss *stubserver.StubServer) (startErr error, stopRegistered bool) {
	if err := ss.Start(nil); err != nil {
		return err, false
	}
	t.Cleanup(ss.Stop)
	return nil, true
}

func (s) TestVerifyC2_ExplicitServerLeftUnstoppedWhenListenFails(t *testing.T) {
	server := grpc.NewServer()
	ss := &stubserver.StubServer{S: server, Network: "bogus-network", Address: "localhost:0"}

	startErr, stopRegistered := startExplicitServer(t, ss)
	t.Logf("ss.Start(nil) = %v; cleanup registered: %v", startErr, stopRegistered)
	if startErr == nil {
		t.Fatalf("expected listener setup to fail")
	}
	// Even an early ss.Stop() would not reach the server: StubServer only
	// appends ss.S.Stop to its cleanups after net.Listen succeeds.
	ss.Stop()

	lis, err := net.Listen("tcp", "localhost:0")
	if err != nil {
		t.Fatalf("net.Listen: %v", err)
	}
	defer lis.Close()
	served := make(chan error, 1)
	go func() { served <- server.Serve(lis) }()
	select {
	case err := <-served:
		if errors.Is(err, grpc.ErrServerStopped) {
			t.Logf("server was stopped on the failure path: Serve returned %v", err)
			return
		}
		t.Fatalf("Serve returned unexpected error %v", err)
	case <-time.After(500 * time.Millisecond):
		t.Errorf("explicitly created server was NOT stopped after listener setup failure: Serve is still running 500ms later")
		server.Stop()
		if err := <-served; !errors.Is(err, grpc.ErrServerStopped) && err != nil {
			t.Logf("Serve returned %v after explicit Stop", err)
		}
	}
}
