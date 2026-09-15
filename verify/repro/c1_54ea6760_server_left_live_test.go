// Run (on evalon/grpc-go-se-54ea6760, repo root): cp verify/repro/c1_54ea6760_server_left_live_test.go test/ && go test -tags verify_repro ./test -run 'Test/^(ServerPipeline_HandwrittenServiceDesc|Verify_C1_ChannelzServersAfterHandwrittenServiceDesc)$' -count=1 -v
//
// Probe for C1 (branch 54ea6760): records the channelz server registry before
// and after TestServerPipeline_HandwrittenServiceDesc runs (grpctest runs
// subtests in name order, so this test executes after it). A grpc.Server that
// is created with grpc.NewServer() and never Stop()ped stays registered in
// channelz for the lifetime of the process.

//go:build verify_repro

package test

import (
	"testing"

	"google.golang.org/grpc/internal/channelz"
)

func (s) TestVerify_C1_ChannelzServersAfterHandwrittenServiceDesc(t *testing.T) {
	servers, _ := channelz.GetServers(0, 0)
	t.Logf("channelz servers still registered after preceding tests: %d", len(servers))
	for _, srv := range servers {
		t.Logf("  live %s (listen sockets: %d)", srv, len(srv.ListenSockets()))
	}
	if len(servers) != 0 {
		t.Errorf("%d grpc.Server(s) left live (never Stop()ped) by preceding tests", len(servers))
	}
}
