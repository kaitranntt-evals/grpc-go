// Run (on evalon/grpc-go-xd-1463306c): cp verify/repro/c6_handle_rds_error_after_success_probe_test.go internal/xds/server/zz_verify_c6_probe_test.go && go test -race -count=1 -v -run 'Test/VerifyC6' ./internal/xds/server/
//
// The test the branch does NOT have: success followed by an error delivered
// through the production listenerWrapper.handleRDSUpdate, asserting that the
// published configuration preserves the delivered error. It passes on the
// unmodified branch and fails under
// verify/repro/c6_mutation_handleRDSUpdate_ignore_error.patch, whereas every
// test the branch added stays green under that mutation.

package server

import (
	"errors"
	"testing"

	"google.golang.org/grpc/internal/grpcsync"
	"google.golang.org/grpc/internal/xds/xdsclient/xdsresource"
)

func (s) TestVerifyC6_HandleRDSUpdate_ErrorAfterSuccess(t *testing.T) {
	const route = "route-1"
	fcm := &filterChainManager{}
	fc := fcm.filterChainFromConfig(&xdsresource.NetworkFilterChainConfig{
		HTTPConnMgr: &xdsresource.HTTPConnectionManagerConfig{RouteConfigName: route},
	})
	fcm.filterChains = []*filterChain{fc}
	l := &listenerWrapper{
		closed:                   grpcsync.NewEvent(),
		activeFilterChainManager: fcm,
		httpFilters:              make(map[serverFilterKey]*refCountedServerFilter),
		rdsHandler: &rdsHandler{
			updates: make(map[string]rdsWatcherUpdate),
			cancels: map[string]func(){route: func() {}},
		},
	}
	prefix := "/"
	cfg := &xdsresource.RouteConfigUpdate{VirtualHosts: []*xdsresource.VirtualHost{{
		Domains: []string{"*"},
		Routes:  []*xdsresource.Route{{Prefix: &prefix, ActionType: xdsresource.RouteActionNonForwardingAction}},
	}}}

	l.handleRDSUpdate(route, rdsWatcherUpdate{data: cfg})
	if got := fc.usableRouteConfiguration.Load(); got == nil || got.err != nil || len(got.vhs) != 1 {
		t.Fatalf("after success: published configuration = %+v, want 1 virtual host and nil error", got)
	}
	t.Logf("PROBE after success: published err=%v vhs=%d", fc.usableRouteConfiguration.Load().err, len(fc.usableRouteConfiguration.Load().vhs))

	wantErr := errors.New("verify: rds resource error after success")
	l.handleRDSUpdate(route, rdsWatcherUpdate{err: wantErr})
	got := fc.usableRouteConfiguration.Load()
	t.Logf("PROBE after error: published err=%v vhs=%d", got.err, len(got.vhs))
	if got.err != wantErr {
		t.Fatalf("after error: published configuration err = %v, want %v", got.err, wantErr)
	}
}
