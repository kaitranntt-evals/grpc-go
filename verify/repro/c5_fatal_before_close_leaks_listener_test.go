// Expected on the audited branch, via verify/repro/c5_fatal_mutation.sh: with the regression reintroduced the branch's test dies on a
// fatal assertion and "OBSERVED ... listening sockets leaked=1", "filtersClosed=0 refCnt=2", "dial leaked listener: err=<nil>" are printed.
// Run: bash verify/repro/c5_fatal_mutation.sh   (from the root of a checkout of branch evalon/grpc-go-xd-aa7e5dee)

package server

import (
	"errors"
	"net"
	"os"
	"strconv"
	"syscall"
	"testing"

	"google.golang.org/grpc/connectivity"
	"google.golang.org/grpc/internal/grpcsync"
	"google.golang.org/grpc/internal/testutils"
	"google.golang.org/grpc/internal/xds/xdsclient/xdsresource"
)

// vc5ListeningSockets returns the number of fds of this process that are
// sockets in the listening state (SO_ACCEPTCONN == 1).
func vc5ListeningSockets(t *testing.T) int {
	ents, err := os.ReadDir("/proc/self/fd")
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, e := range ents {
		fd, err := strconv.Atoi(e.Name())
		if err != nil {
			continue
		}
		if v, err := syscall.GetsockoptInt(fd, syscall.SOL_SOCKET, syscall.SO_ACCEPTCONN); err == nil && v == 1 {
			n++
		}
	}
	return n
}

// Part A: runs the branch's own, unmodified test as a subtest and counts the
// process's listening sockets before it starts and after it (and all of its
// deferred functions and t.Cleanup registrations) has finished.
func (s) TestVerifyC5_A_BranchTestListenerAfterExit(t *testing.T) {
	before := vc5ListeningSockets(t)
	passed := t.Run("branch-test", func(t *testing.T) {
		s{}.TestListenerWrapper_HandleRDSUpdate_ReleasesSupersededResources(t)
	})
	after := vc5ListeningSockets(t)
	t.Logf("OBSERVED A: branch test passed=%v; listening sockets before=%d after=%d leaked=%d", passed, before, after, after-before)
}

// Part B: the branch test's setup, statement for statement (same listener
// creation, same listenerWrapper literal, same first assertions, same explicit
// lw.Close() with no defer / t.Cleanup), with the locals captured so the state
// left behind by an early t.Fatalf can be inspected.
func (s) TestVerifyC5_B_ResourcesAfterFatal(t *testing.T) {
	var (
		fb  *countingFilterBuilder
		lw  *listenerWrapper
		lis net.Listener
	)
	passed := t.Run("copy-of-branch-test", func(t *testing.T) {
		fb = &countingFilterBuilder{}
		httpFilters := []xdsresource.HTTPFilter{{Name: "counting", Filter: fb}}
		fcm := &filterChainManager{routeConfigNames: map[string]bool{route1: true}}
		fc := fcm.filterChainFromConfig(&xdsresource.NetworkFilterChainConfig{
			HTTPConnMgr: &xdsresource.HTTPConnectionManagerConfig{
				RouteConfigName: route1,
				HTTPFilters:     httpFilters,
			},
		})
		fcm.filterChains = []*filterChain{fc}

		var err error
		lis, err = testutils.LocalTCPListener()
		if err != nil {
			t.Fatalf("testutils.LocalTCPListener() failed: %v", err)
		}
		lw = &listenerWrapper{
			Listener:                 lis,
			logger:                   nil,
			name:                     listenerName,
			mode:                     connectivity.ServingModeServing,
			closed:                   grpcsync.NewEvent(),
			conns:                    make(map[*connWrapper]bool),
			httpFilters:              make(map[serverFilterKey]*refCountedServerFilter),
			activeFilterChainManager: fcm,
			rdsHandler: &rdsHandler{
				updates: make(map[string]rdsWatcherUpdate),
				cancels: make(map[string]func()),
			},
		}
		routeConfig := &xdsresource.RouteConfigUpdate{
			VirtualHosts: []*xdsresource.VirtualHost{{
				Domains: []string{"*"},
				Routes: []*xdsresource.Route{
					{Prefix: newStringP("/foo"), ActionType: xdsresource.RouteActionNonForwardingAction},
					{Prefix: newStringP("/bar"), ActionType: xdsresource.RouteActionNonForwardingAction},
				},
			}},
		}
		const interceptorsPerUpdate = 2
		const numUpdates = 5
		for i := 1; i <= numUpdates; i++ {
			lw.handleRDSUpdate(route1, rdsWatcherUpdate{data: routeConfig})
			if got, want := fb.interceptorsBuilt.Load(), int32(i*interceptorsPerUpdate); got != want {
				t.Fatalf("Update %d: built %d interceptors, want %d", i, got, want)
			}
			if got, want := fb.interceptorsClosed.Load(), int32((i-1)*interceptorsPerUpdate); got != want {
				t.Fatalf("Update %d: closed %d interceptors, want %d", i, got, want)
			}
		}
		lw.handleRDSUpdate(route1, rdsWatcherUpdate{err: errors.New("route configuration not found")})
		lw.Close()
	})

	refs := int32(-1)
	for _, f := range lw.httpFilters {
		refs = f.refCnt.Load()
	}
	t.Logf("OBSERVED B: copy passed=%v; after exit: lw.closed fired=%v filtersClosed=%d refCnt=%d interceptorsBuilt=%d interceptorsClosed=%d",
		passed, lw.closed.HasFired(), fb.filtersClosed.Load(), refs, fb.interceptorsBuilt.Load(), fb.interceptorsClosed.Load())
	c, err := net.Dial("tcp", lis.Addr().String())
	t.Logf("OBSERVED B: dial leaked listener %s after test exit: err=%v (nil => still listening)", lis.Addr(), err)
	if err == nil {
		c.Close()
	}
	// Tidy up what the test under audit left behind.
	lw.Close()
}
