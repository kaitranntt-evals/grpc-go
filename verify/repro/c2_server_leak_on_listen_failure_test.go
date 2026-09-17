// Run: cp verify/repro/c2_server_leak_on_listen_failure_test.go <worktree of evalon/grpc-go-se-d4895981>/zz_verify_c2_test.go && go test . -run '^TestVerifyC2_' -count=1 -v
//
// Reproduces the startup sequence of TestServerStreamPipeline in
// server_stream_pipeline_test.go (branch evalon/grpc-go-se-d4895981, lines
// 179-185): an explicitly created *grpc.Server is handed to StubServer.S,
// ss.Start is called, and `defer ss.Stop()` is only reached after Start
// succeeds.  A net.Listen failure is induced through the exported
// StubServer.Network field.  The test then shows that on that exit path the
// server has not been stopped (its channelz entry is still registered) and that
// even calling ss.Stop() would not stop it, because StubServer only records
// S.Stop as a cleanup after net.Listen succeeds.
//
// The second test replays startPipelineServer from server_unified_test.go
// (branch evalon/grpc-go-se-6233a68e, lines 57-68), where t.Cleanup(ss.Stop) is
// registered before Start, to show the difference in cleanup registration.
package grpc_test

import (
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/internal/channelz"
	"google.golang.org/grpc/internal/stubserver"
)

func verifyC2ServerRegistered(id int64) bool {
	return channelz.GetServer(id) != nil
}

// verifyC2NewServer creates a *grpc.Server and returns its channelz ID, found
// by diffing the channelz server list around the NewServer call.
func verifyC2NewServer(t *testing.T, opts ...grpc.ServerOption) (*grpc.Server, int64) {
	t.Helper()
	channelz.TurnOn()
	before := map[int64]bool{}
	servers, _ := channelz.GetServers(0, 1<<20)
	for _, s := range servers {
		before[s.ID] = true
	}
	server := grpc.NewServer(opts...)
	servers, _ = channelz.GetServers(0, 1<<20)
	var id int64 = -1
	for _, s := range servers {
		if !before[s.ID] {
			id = s.ID
		}
	}
	if id < 0 {
		t.Fatal("grpc.NewServer did not register a channelz server entry")
	}
	return server, id
}

// TestVerifyC2_d4895981_ListenFailureLeavesServerUnstopped mirrors
// server_stream_pipeline_test.go:179-185 with net.Listen forced to fail.
func TestVerifyC2_d4895981_ListenFailureLeavesServerUnstopped(t *testing.T) {
	server, id := verifyC2NewServer(t)
	t.Logf("grpc.NewServer() registered channelz server %d", id)
	// Same as the audited test, except Network makes net.Listen fail.
	ss := &stubserver.StubServer{S: server, Network: "bogus-network"}
	err := ss.Start(nil)
	if err == nil {
		t.Fatal("ss.Start(nil) succeeded, want net.Listen failure")
	}
	t.Logf("ss.Start(nil) = %v (the audited test calls t.Fatal here, before `defer ss.Stop()` is reached)", err)

	if !verifyC2ServerRegistered(id) {
		t.Fatalf("server %d was stopped on the failure path (channelz entry removed)", id)
	}
	t.Logf("after failed Start: server %d still registered in channelz -> not stopped", id)

	// Even the cleanup the test would have deferred does not stop this server:
	// StubServer.setupServer appends S.Stop to its cleanups only after net.Listen
	// succeeds.
	ss.Stop()
	if !verifyC2ServerRegistered(id) {
		t.Fatalf("ss.Stop() stopped server %d; expected StubServer to have no cleanup registered", id)
	}
	t.Logf("after ss.Stop(): server %d still registered in channelz -> StubServer.Stop did not stop it", id)

	server.Stop()
	if verifyC2ServerRegistered(id) {
		t.Fatalf("server %d still registered after explicit server.Stop()", id)
	}
	t.Logf("after server.Stop(): server %d removed from channelz", id)
	t.Errorf("explicitly created server was left unstopped by the audited startup sequence on net.Listen failure")
}

// TestVerifyC2_6233a68e_CleanupRegisteredBeforeStart mirrors
// server_unified_test.go:57-68 (startPipelineServer) with net.Listen forced to
// fail; t.Cleanup(ss.Stop) is registered before Start there.
func TestVerifyC2_6233a68e_CleanupRegisteredBeforeStart(t *testing.T) {
	server, id := verifyC2NewServer(t)
	ss := &stubserver.StubServer{S: server, Network: "bogus-network"}
	cleanupRan := false
	// Cleanups run LIFO: this checker is registered first so it runs after
	// the ss.Stop cleanup below.
	t.Cleanup(func() {
		if !cleanupRan {
			t.Errorf("registered ss.Stop cleanup did not run")
		}
		if verifyC2ServerRegistered(id) {
			t.Logf("after registered ss.Stop cleanup ran: server %d still registered in channelz (StubServer.Stop has no S.Stop cleanup on the net.Listen failure path)", id)
			server.Stop()
		} else {
			t.Logf("after registered ss.Stop cleanup ran: server %d was stopped", id)
		}
	})
	t.Cleanup(func() { cleanupRan = true; ss.Stop() })
	err := ss.Start(nil)
	if err == nil {
		t.Fatal("ss.Start(nil) succeeded, want net.Listen failure")
	}
	t.Logf("ss.Start(nil) = %v; t.Cleanup(ss.Stop) was registered before Start", err)
}
