//go:build verify

// Run: cp verify/repro/verify_lifecycle_internal_test.go internal/xds/server/ && go test -tags verify -race -count=1 -v -run 'Test/Verify_' ./internal/xds/server; rm internal/xds/server/verify_lifecycle_internal_test.go
//
// Package-local lifecycle instrumentation: drives listenerWrapper.handleRDSUpdate
// directly (the exact path an RDS update takes) with a tracking HTTP filter and
// records, in order, server-filter destruction (refcount reaching zero),
// interceptor creation/closure and the server-filter refcount observed at the
// moment each retired interceptor is closed. Each test asserts the *correct*
// ordering, so a failure demonstrates the suspected problem.
package server

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"

	"google.golang.org/grpc/internal/grpcsync"
	iresolver "google.golang.org/grpc/internal/resolver"
	"google.golang.org/grpc/internal/xds/httpfilter"
	"google.golang.org/grpc/internal/xds/xdsclient/xdsresource"
	"google.golang.org/protobuf/proto"
)

const vfyFilterName = "vfy-tracker"

type vfyCfg struct{ httpfilter.FilterConfig }

// vfyTrace is the shared ordered event log for one test.
type vfyTrace struct {
	events []string
	lw     *listenerWrapper
	key    serverFilterKey
}

func (tr *vfyTrace) refCnt() int32 {
	if f, ok := tr.lw.httpFilters[tr.key]; ok {
		return f.refCnt.Load()
	}
	return -1
}

func (tr *vfyTrace) add(format string, args ...any) {
	tr.events = append(tr.events, fmt.Sprintf(format, args...))
}

// vfyBuilder is a server-side HTTP filter builder whose BuildServerFilter
// returns a fresh vfyServerFilter; destruction of that filter (refcount zero)
// is recorded in the trace.
type vfyBuilder struct {
	httpfilter.Builder
	tr           *vfyTrace
	built        int
	destroyed    int
	interceptors []*vfyInterceptor
}

func (b *vfyBuilder) TypeURLs() []string { return []string{"vfy.tracker"} }
func (b *vfyBuilder) IsTerminal() bool   { return false }
func (b *vfyBuilder) ParseFilterConfig(proto.Message) (httpfilter.FilterConfig, error) {
	return vfyCfg{}, nil
}
func (b *vfyBuilder) ParseFilterConfigOverride(proto.Message) (httpfilter.FilterConfig, error) {
	return vfyCfg{}, nil
}
func (b *vfyBuilder) BuildServerFilter() httpfilter.ServerFilter {
	b.built++
	b.tr.add("filter_built#%d", b.built)
	return &vfyServerFilter{b: b, id: b.built}
}

type vfyServerFilter struct {
	b  *vfyBuilder
	id int
}

func (f *vfyServerFilter) BuildServerInterceptor(config, override httpfilter.FilterConfig) (iresolver.ServerInterceptor, error) {
	i := &vfyInterceptor{b: f.b, id: len(f.b.interceptors) + 1, filterID: f.id}
	f.b.interceptors = append(f.b.interceptors, i)
	f.b.tr.add("interceptor_created#%d(filter#%d)", i.id, f.id)
	return i, nil
}

func (f *vfyServerFilter) Close() {
	f.b.destroyed++
	f.b.tr.add("FILTER_DESTROYED#%d", f.id)
}

type vfyInterceptor struct {
	b          *vfyBuilder
	id         int
	filterID   int
	closeCount atomic.Int32
	// refCntAtClose is the tracker filter's reference count observed while
	// this interceptor was being closed; destroyedAtClose is how many times
	// the tracker filter had been destroyed by then.
	refCntAtClose    int32
	destroyedAtClose int
}

func (i *vfyInterceptor) AllowRPC(context.Context) error { return nil }
func (i *vfyInterceptor) Close() {
	i.closeCount.Add(1)
	i.refCntAtClose = i.b.tr.refCnt()
	i.destroyedAtClose = i.b.destroyed
	i.b.tr.add("interceptor_closed#%d(filter#%d) refCnt=%d destroyed=%d", i.id, i.filterID, i.refCntAtClose, i.destroyedAtClose)
}

// vfySetup builds a listener wrapper with one active filter chain that points
// at routeName and carries the tracking HTTP filter.
func vfySetup(t *testing.T) (*listenerWrapper, *filterChain, *vfyBuilder, *vfyTrace) {
	t.Helper()
	const routeName = "vfy-route"
	tr := &vfyTrace{}
	b := &vfyBuilder{tr: tr}
	hf := xdsresource.HTTPFilter{Name: vfyFilterName, Filter: b, Config: vfyCfg{}}
	fc := &filterChain{
		routeConfigName:          routeName,
		httpFilters:              []xdsresource.HTTPFilter{hf},
		usableRouteConfiguration: &atomic.Pointer[usableRouteConfiguration]{},
	}
	fc.usableRouteConfiguration.Store(&usableRouteConfiguration{})
	lw := &listenerWrapper{
		xdsNodeID:                "vfy-node",
		closed:                   grpcsync.NewEvent(),
		httpFilters:              make(map[serverFilterKey]*refCountedServerFilter),
		activeFilterChainManager: &filterChainManager{filterChains: []*filterChain{fc}},
		rdsHandler: &rdsHandler{
			updates: make(map[string]rdsWatcherUpdate),
			cancels: make(map[string]func()),
		},
	}
	tr.lw = lw
	tr.key = newServerFilterKey(&hf)
	return lw, fc, b, tr
}

func vfyRouteConfig(routes int, disableTracker bool) *xdsresource.RouteConfigUpdate {
	var rs []*xdsresource.Route
	for i := 0; i < routes; i++ {
		p := fmt.Sprintf("/svc/m%d", i)
		r := &xdsresource.Route{Prefix: &p, ActionType: xdsresource.RouteActionNonForwardingAction}
		if disableTracker {
			r.HTTPFilterConfigOverride = map[string]httpfilter.FilterConfig{vfyFilterName: httpfilter.DisabledFilterConfig{}}
		}
		rs = append(rs, r)
	}
	return &xdsresource.RouteConfigUpdate{VirtualHosts: []*xdsresource.VirtualHost{{Domains: []string{"*"}, Routes: rs}}}
}

func vfyDump(t *testing.T, tr *vfyTrace) {
	t.Helper()
	t.Logf("trace:\n  %s", strings.Join(tr.events, "\n  "))
}

// TestVerify_C1_ReplaceKeepingFilter_RefsReleasedAfterInterceptorsClose covers
// the everyday in-place RDS replacement where the filter stays enabled. The
// retired configuration's interceptor must be closed while the filter still
// holds the retired reference (refCnt == 2: old ref + new ref); observing
// refCnt == 1 means the retired reference was released before the dependent
// interceptor closed.
func (s) TestVerify_C1_ReplaceKeepingFilter_RefsReleasedAfterInterceptorsClose(t *testing.T) {
	lw, _, b, tr := vfySetup(t)
	lw.handleRDSUpdate("vfy-route", rdsWatcherUpdate{data: vfyRouteConfig(1, false)})
	if got := tr.refCnt(); got != 1 {
		t.Fatalf("after first update refCnt = %d, want 1", got)
	}
	tr.add("--- in-place replacement (filter still enabled on every route) ---")
	lw.handleRDSUpdate("vfy-route", rdsWatcherUpdate{data: vfyRouteConfig(1, false)})
	vfyDump(t, tr)
	old := b.interceptors[0]
	if got := old.closeCount.Load(); got != 1 {
		t.Fatalf("retired interceptor closeCount = %d, want 1", got)
	}
	if old.refCntAtClose != 2 {
		t.Errorf("retired interceptor closed with tracker refCnt = %d, want 2 (retired filter reference was released BEFORE its dependent interceptor closed)", old.refCntAtClose)
	}
	if got := tr.refCnt(); got != 1 {
		t.Errorf("after replacement refCnt = %d, want 1", got)
	}
}

// TestVerify_C1_C3_ReplaceDisablingFilter_DestroyAfterInterceptorsClose covers
// a replacement that disables the filter on every route: the replacement takes
// no reference, so releasing the retired references destroys the filter. The
// filter must not be destroyed before the interceptors built from it close.
func (s) TestVerify_C1_C3_ReplaceDisablingFilter_DestroyAfterInterceptorsClose(t *testing.T) {
	lw, fc, b, tr := vfySetup(t)
	lw.handleRDSUpdate("vfy-route", rdsWatcherUpdate{data: vfyRouteConfig(2, false)})
	if got := tr.refCnt(); got != 2 {
		t.Fatalf("after first update refCnt = %d, want 2", got)
	}
	tr.add("--- in-place replacement (filter disabled on every route) ---")
	lw.handleRDSUpdate("vfy-route", rdsWatcherUpdate{data: vfyRouteConfig(2, true)})
	vfyDump(t, tr)
	if fc.usableRouteConfiguration.Load().err != nil {
		t.Fatalf("replacement installed error: %v", fc.usableRouteConfiguration.Load().err)
	}
	if b.destroyed != 1 {
		t.Errorf("tracker filter destroyed %d times after replacement, want 1", b.destroyed)
	}
	for _, i := range b.interceptors {
		if got := i.closeCount.Load(); got != 1 {
			t.Errorf("interceptor#%d closeCount = %d, want 1", i.id, got)
		}
		if i.destroyedAtClose != 0 {
			t.Errorf("interceptor#%d was closed AFTER its server filter had already been destroyed (destroyed=%d, refCnt=%d at close)", i.id, i.destroyedAtClose, i.refCntAtClose)
		}
	}
}

// TestVerify_C2_ErrorAfterSuccess_ReleasesFilterRefs delivers an RDS error
// after a successful configuration. The error state must be installed, the
// retired interceptors closed, and the retired server-filter references
// released (refCnt back to 0, filter destroyed) as part of the error
// transition -- not only at server shutdown.
func (s) TestVerify_C2_ErrorAfterSuccess_ReleasesFilterRefs(t *testing.T) {
	lw, fc, b, tr := vfySetup(t)
	lw.handleRDSUpdate("vfy-route", rdsWatcherUpdate{data: vfyRouteConfig(2, false)})
	if got := tr.refCnt(); got != 2 {
		t.Fatalf("after first update refCnt = %d, want 2", got)
	}
	rdsErr := errors.New("vfy rds error")
	tr.add("--- RDS error after success ---")
	lw.handleRDSUpdate("vfy-route", rdsWatcherUpdate{err: rdsErr})
	tr.add("after error: refCnt=%d destroyed=%d len(fc.serverFilters)=%d", tr.refCnt(), b.destroyed, len(fc.serverFilters))
	vfyDump(t, tr)
	if !errors.Is(fc.usableRouteConfiguration.Load().err, rdsErr) {
		t.Errorf("error state not installed: urc.err = %v", fc.usableRouteConfiguration.Load().err)
	}
	for _, i := range b.interceptors {
		if got := i.closeCount.Load(); got != 1 {
			t.Errorf("interceptor#%d closeCount = %d, want 1", i.id, got)
		}
	}
	if got := tr.refCnt(); got != 0 {
		t.Errorf("after RDS error tracker refCnt = %d, want 0 (retired configuration's server-filter references were NOT released)", got)
	}
	if b.destroyed != 1 {
		t.Errorf("after RDS error tracker destroyed %d times, want 1", b.destroyed)
	}
	if len(fc.serverFilters) != 0 {
		t.Errorf("after RDS error len(fc.serverFilters) = %d, want 0", len(fc.serverFilters))
	}
	// Shutdown: whatever was still held must be released exactly once now.
	tr.add("--- filterChainManager.stop() ---")
	lw.activeFilterChainManager.stop()
	tr.add("after stop: refCnt=%d destroyed=%d", tr.refCnt(), b.destroyed)
	t.Logf("post-stop: %s", tr.events[len(tr.events)-1])
	if b.destroyed != 1 {
		t.Errorf("after stop tracker destroyed %d times, want exactly 1", b.destroyed)
	}
	for _, i := range b.interceptors {
		if got := i.closeCount.Load(); got != 1 {
			t.Errorf("after stop interceptor#%d closeCount = %d, want exactly 1", i.id, got)
		}
	}
}
