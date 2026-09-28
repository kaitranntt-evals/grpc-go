// Run: cp verify/repro/verify_listener_wrapper_test.go internal/xds/server/ && go test -race -count=1 -v -run 'TestVerify_' ./internal/xds/server ; rm internal/xds/server/verify_listener_wrapper_test.go
//
// Unit-level probes of listenerWrapper / filterChain lifecycle used by the
// v-eb02180b audit (claims C1, C3, C4, C5, C7, C8). Every probe drives the
// real handleRDSUpdate / Close code paths with an instrumented HTTP filter that
// records, per instance, when filters and interceptors are built and closed.
package server

import (
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"context"
	"google.golang.org/grpc/connectivity"
	"google.golang.org/grpc/internal/grpcsync"
	iresolver "google.golang.org/grpc/internal/resolver"
	"google.golang.org/grpc/internal/testutils"
	"google.golang.org/grpc/internal/xds/httpfilter"
	"google.golang.org/grpc/internal/xds/xdsclient/xdsresource"
	"google.golang.org/protobuf/proto"
)

type vfyCfg struct {
	httpfilter.FilterConfig
	fail bool // when set as an override, BuildServerInterceptor fails.
}

type vfyBuilder struct {
	httpfilter.Builder
	mu           sync.Mutex
	filters      []*vfyFilter
	interceptors []*vfyInterceptor
	events       []string
}

func (b *vfyBuilder) TypeURLs() []string { return []string{"verify.filter"} }
func (b *vfyBuilder) IsTerminal() bool   { return false }
func (b *vfyBuilder) ParseFilterConfig(proto.Message) (httpfilter.FilterConfig, error) {
	return vfyCfg{}, nil
}
func (b *vfyBuilder) ParseFilterConfigOverride(proto.Message) (httpfilter.FilterConfig, error) {
	return vfyCfg{}, nil
}
func (b *vfyBuilder) record(format string, args ...any) {
	b.events = append(b.events, fmt.Sprintf(format, args...))
}
func (b *vfyBuilder) BuildServerFilter() httpfilter.ServerFilter {
	b.mu.Lock()
	defer b.mu.Unlock()
	f := &vfyFilter{b: b, id: len(b.filters) + 1}
	b.filters = append(b.filters, f)
	b.record("filter#%d-build", f.id)
	return f
}
func (b *vfyBuilder) eventLog() []string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]string(nil), b.events...)
}
func (b *vfyBuilder) snapshotInterceptors() []*vfyInterceptor {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]*vfyInterceptor(nil), b.interceptors...)
}
func (b *vfyBuilder) snapshotFilters() []*vfyFilter {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]*vfyFilter(nil), b.filters...)
}

var _ httpfilter.ServerFilterBuilder = &vfyBuilder{}

type vfyFilter struct {
	b      *vfyBuilder
	id     int
	closes atomic.Int32
}

func (f *vfyFilter) Close() {
	n := f.closes.Add(1)
	f.b.mu.Lock()
	defer f.b.mu.Unlock()
	f.b.record("filter#%d-close(#%d)", f.id, n)
}

func (f *vfyFilter) BuildServerInterceptor(config, override httpfilter.FilterConfig) (iresolver.ServerInterceptor, error) {
	if o, ok := override.(vfyCfg); ok && o.fail {
		f.b.mu.Lock()
		f.b.record("interceptor-build-FAILED(filter#%d)", f.id)
		f.b.mu.Unlock()
		return nil, fmt.Errorf("injected interceptor construction failure")
	}
	f.b.mu.Lock()
	defer f.b.mu.Unlock()
	i := &vfyInterceptor{b: f.b, filter: f, id: len(f.b.interceptors) + 1, builtOnClosedFilter: f.closes.Load() > 0}
	f.b.interceptors = append(f.b.interceptors, i)
	if i.builtOnClosedFilter {
		f.b.record("interceptor#%d-build-ON-CLOSED-filter#%d", i.id, f.id)
	} else {
		f.b.record("interceptor#%d-build(filter#%d)", i.id, f.id)
	}
	return i, nil
}

type vfyInterceptor struct {
	b                   *vfyBuilder
	filter              *vfyFilter
	id                  int
	builtOnClosedFilter bool
	closes              atomic.Int32
}

func (i *vfyInterceptor) AllowRPC(context.Context) error { return nil }
func (i *vfyInterceptor) Close() {
	n := i.closes.Add(1)
	i.b.mu.Lock()
	defer i.b.mu.Unlock()
	i.b.record("interceptor#%d-close(#%d)", i.id, n)
}

const vfyRouteName = "verify-route"

// newVerifyListenerWrapper builds a listenerWrapper that is SERVING a single
// default filter chain whose route configuration comes from RDS (vfyRouteName)
// and which carries the instrumented filter "tracker".
func newVerifyListenerWrapper(t *testing.T, fb *vfyBuilder) (*listenerWrapper, *filterChain) {
	t.Helper()
	lis, err := testutils.LocalTCPListener()
	if err != nil {
		t.Fatalf("LocalTCPListener: %v", err)
	}
	t.Cleanup(func() { lis.Close() })
	fcm := newFilterChainManager(nil, &xdsresource.NetworkFilterChainConfig{
		HTTPConnMgr: &xdsresource.HTTPConnectionManagerConfig{
			RouteConfigName: vfyRouteName,
			HTTPFilters:     []xdsresource.HTTPFilter{{Name: "tracker", Filter: fb, Config: vfyCfg{}}},
		},
	})
	if len(fcm.filterChains) != 1 {
		t.Fatalf("got %d filter chains, want 1", len(fcm.filterChains))
	}
	l := &listenerWrapper{
		Listener:                 lis,
		name:                     "verify-listener",
		xdsNodeID:                "verify-node",
		mode:                     connectivity.ServingModeServing,
		closed:                   grpcsync.NewEvent(),
		conns:                    make(map[*connWrapper]bool),
		httpFilters:              make(map[serverFilterKey]*refCountedServerFilter),
		rdsHandler:               &rdsHandler{updates: make(map[string]rdsWatcherUpdate), cancels: make(map[string]func())},
		activeFilterChainManager: fcm,
	}
	return l, fcm.filterChains[0]
}

func vfyRoute(prefix string, override map[string]httpfilter.FilterConfig) *xdsresource.Route {
	p := prefix
	return &xdsresource.Route{Prefix: &p, ActionType: xdsresource.RouteActionNonForwardingAction, HTTPFilterConfigOverride: override}
}

func vfyRouteConfig(numRoutes int) *xdsresource.RouteConfigUpdate {
	var routes []*xdsresource.Route
	for i := 1; i <= numRoutes; i++ {
		routes = append(routes, vfyRoute(fmt.Sprintf("/%d", i), nil))
	}
	return &xdsresource.RouteConfigUpdate{VirtualHosts: []*xdsresource.VirtualHost{{Domains: []string{"*"}, Routes: routes}}}
}

// vfyRouteConfigDisabled disables every use of "tracker" via a route override.
func vfyRouteConfigDisabled() *xdsresource.RouteConfigUpdate {
	return &xdsresource.RouteConfigUpdate{VirtualHosts: []*xdsresource.VirtualHost{{Domains: []string{"*"}, Routes: []*xdsresource.Route{
		vfyRoute("/1", map[string]httpfilter.FilterConfig{"tracker": httpfilter.DisabledFilterConfig{}}),
	}}}}
}

func vfyDumpFilters(t *testing.T, l *listenerWrapper) {
	for k, f := range l.httpFilters {
		t.Logf("  httpFilters[%v] = wrapper %p refCnt=%d underlying=%p", k, f, f.refCnt.Load(), f.ServerFilter)
	}
	t.Logf("  len(l.httpFilters)=%d", len(l.httpFilters))
}

func vfyLogState(t *testing.T, label string, fb *vfyBuilder) {
	t.Logf("%s: events=%v", label, fb.eventLog())
	for _, f := range fb.snapshotFilters() {
		t.Logf("  filter#%d closes=%d", f.id, f.closes.Load())
	}
	for _, i := range fb.snapshotInterceptors() {
		t.Logf("  interceptor#%d filter#%d closes=%d builtOnClosedFilter=%v", i.id, i.filter.id, i.closes.Load(), i.builtOnClosedFilter)
	}
}

// C1 (unit level): replace a published configuration with one that disables
// every use of the filter, so that releasing the old configuration's filter
// reference drops the refcount to zero and physically closes the filter. The
// relative order of that physical close and the retired interceptor's close
// tells us whether the filter reference was released before or after the
// interceptor that depends on it was closed.
func (s) TestVerify_C1_FilterReleaseVsInterceptorCloseOrder(t *testing.T) {
	fb := &vfyBuilder{}
	l, _ := newVerifyListenerWrapper(t, fb)
	l.handleRDSUpdate(vfyRouteName, rdsWatcherUpdate{data: vfyRouteConfig(1)})
	l.handleRDSUpdate(vfyRouteName, rdsWatcherUpdate{data: vfyRouteConfigDisabled()})
	ev := fb.eventLog()
	t.Logf("C1 events after in-place update that disables the filter: %v", ev)
	fClose, iClose := -1, -1
	for idx, e := range ev {
		if e == "filter#1-close(#1)" {
			fClose = idx
		}
		if e == "interceptor#1-close(#1)" {
			iClose = idx
		}
	}
	switch {
	case iClose < 0:
		t.Errorf("C1: retired interceptor#1 was never closed by the in-place update")
	case fClose < 0:
		t.Logf("C1: filter#1 was not physically closed by the update (reference still held)")
	case fClose < iClose:
		t.Errorf("C1 CONFIRMED: filter#1 physically closed (event %d) BEFORE its dependent retired interceptor#1 closed (event %d)", fClose, iClose)
	default:
		t.Logf("C1 REFUTED: retired interceptor#1 closed (event %d) BEFORE filter#1 was physically closed (event %d)", iClose, fClose)
	}
	l.Close()
	vfyLogState(t, "C1 final", fb)
}

// C5: Close() twice on a listener whose active configuration holds interceptors.
func (s) TestVerify_C5_DoubleClose(t *testing.T) {
	fb := &vfyBuilder{}
	l, _ := newVerifyListenerWrapper(t, fb)
	l.handleRDSUpdate(vfyRouteName, rdsWatcherUpdate{data: vfyRouteConfig(2)})
	l.Close()
	vfyLogState(t, "C5 after first Close", fb)
	l.Close()
	vfyLogState(t, "C5 after second Close", fb)
	for _, i := range fb.snapshotInterceptors() {
		if n := i.closes.Load(); n != 1 {
			t.Errorf("C5 CONFIRMED: interceptor#%d closed %d times after two listenerWrapper.Close() calls, want 1", i.id, n)
		}
	}
	for _, f := range fb.snapshotFilters() {
		if n := f.closes.Load(); n != 1 {
			t.Errorf("C5: filter#%d closed %d times after two listenerWrapper.Close() calls, want 1", f.id, n)
		}
	}
}

// C4 part 1: handleRDSUpdate after Close() allocates interceptors that nobody
// owns (and re-closes the already-closed ones).
func (s) TestVerify_C4_PostShutdownAllocation(t *testing.T) {
	fb := &vfyBuilder{}
	l, _ := newVerifyListenerWrapper(t, fb)
	l.handleRDSUpdate(vfyRouteName, rdsWatcherUpdate{data: vfyRouteConfig(2)})
	l.Close()
	before := len(fb.snapshotInterceptors())
	l.handleRDSUpdate(vfyRouteName, rdsWatcherUpdate{data: vfyRouteConfig(2)})
	vfyLogState(t, "C4 after Close() followed by handleRDSUpdate", fb)
	all := fb.snapshotInterceptors()
	if len(all) == before {
		t.Logf("C4 REFUTED (allocation part): no interceptors were built after Close()")
	}
	for _, i := range all[before:] {
		if i.closes.Load() == 0 {
			t.Errorf("C4 CONFIRMED (allocation part): interceptor#%d built after Close() and never closed (no owner)", i.id)
		}
	}
	for _, i := range all[:before] {
		if n := i.closes.Load(); n > 1 {
			t.Errorf("C4: pre-shutdown interceptor#%d closed %d times (re-closed by post-shutdown update)", i.id, n)
		}
	}
	// Even a second Close() does not reclaim them.
	l.Close()
	for _, i := range all[before:] {
		t.Logf("C4: interceptor#%d closes after an extra Close()=%d", i.id, i.closes.Load())
	}
}

// C4 part 2: an RDS callback that already passed rdsWatcher.ResourceChanged's
// canceled check is paused, the listener is closed (which cancels the watcher),
// and then the callback resumes and enters handleRDSUpdate.
func (s) TestVerify_C4_AdmittedCallbackReachesClosedListener(t *testing.T) {
	fb := &vfyBuilder{}
	l, _ := newVerifyListenerWrapper(t, fb)
	l.handleRDSUpdate(vfyRouteName, rdsWatcherUpdate{data: vfyRouteConfig(1)})

	admitted := make(chan struct{})
	resume := make(chan struct{})
	enteredHandle := make(chan struct{})
	rh := &rdsHandler{updates: make(map[string]rdsWatcherUpdate), cancels: make(map[string]func())}
	rh.callback = func(name string, u rdsWatcherUpdate) {
		// This is the point right after rdsWatcher.ResourceChanged released
		// rw.mu having seen canceled==false; the real callback is
		// listenerWrapper.handleRDSUpdate.
		close(admitted)
		<-resume
		close(enteredHandle)
		l.handleRDSUpdate(name, u)
	}
	w := &rdsWatcher{parent: rh, routeName: vfyRouteName}
	rh.cancels[vfyRouteName] = func() {
		w.mu.Lock()
		w.canceled = true
		w.mu.Unlock()
	}
	l.rdsHandler = rh

	done := make(chan struct{})
	go func() {
		defer close(done)
		w.ResourceChanged(vfyRouteConfig(1), func() {})
	}()
	select {
	case <-admitted:
	case <-time.After(5 * time.Second):
		t.Fatal("callback was not admitted")
	}
	before := len(fb.snapshotInterceptors())
	l.Close() // cancels the watcher (sets w.canceled) and stops the active manager
	w.mu.Lock()
	canceled := w.canceled
	w.mu.Unlock()
	t.Logf("C4: after Close(): watcher.canceled=%v closed.HasFired=%v", canceled, l.closed.HasFired())
	close(resume)
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("callback did not finish")
	}
	<-enteredHandle
	vfyLogState(t, "C4 after paused callback resumed post-Close", fb)
	all := fb.snapshotInterceptors()
	if len(all) == before {
		t.Logf("C4 REFUTED (reachability part): resumed callback built no interceptors after Close()")
	}
	for _, i := range all[before:] {
		if i.closes.Load() == 0 {
			t.Errorf("C4 CONFIRMED (reachability part): admitted RDS callback entered handleRDSUpdate after Close() and built interceptor#%d which is never closed", i.id)
		}
	}
}

// C3/C8: a transition that releases the last reference to a server filter
// (RDS error, disabling all uses, or construction failure) physically closes
// the filter; check whether its wrapper stays in l.httpFilters and whether the
// next construction reuses the closed filter instead of building a new one.
func vfyClosedFilterReuse(t *testing.T, transition rdsWatcherUpdate, label string) {
	fb := &vfyBuilder{}
	l, fc := newVerifyListenerWrapper(t, fb)
	l.handleRDSUpdate(vfyRouteName, rdsWatcherUpdate{data: vfyRouteConfig(1)})
	t.Logf("%s: after initial publish:", label)
	vfyDumpFilters(t, l)

	l.handleRDSUpdate(vfyRouteName, transition)
	t.Logf("%s: after transition: urc.err=%v", label, fc.usableRouteConfiguration.Load().err)
	vfyDumpFilters(t, l)
	filters := fb.snapshotFilters()
	if len(filters) != 1 {
		t.Fatalf("got %d filters built, want 1", len(filters))
	}
	if filters[0].closes.Load() != 1 {
		t.Logf("%s: filter#1 closes=%d after transition (last reference not released)", label, filters[0].closes.Load())
	}
	retained := len(l.httpFilters) == 1
	t.Logf("%s: filter#1 physically closed=%v, wrapper retained in l.httpFilters=%v", label, filters[0].closes.Load() == 1, retained)

	// Recovery: publish a configuration that uses the filter again.
	l.handleRDSUpdate(vfyRouteName, rdsWatcherUpdate{data: vfyRouteConfig(1)})
	t.Logf("%s: after recovery: urc.err=%v", label, fc.usableRouteConfiguration.Load().err)
	vfyDumpFilters(t, l)
	vfyLogState(t, label+" after recovery", fb)
	ints := fb.snapshotInterceptors()
	last := ints[len(ints)-1]
	if last.builtOnClosedFilter {
		t.Errorf("%s CONFIRMED: recovery built interceptor#%d on filter#%d which had already been physically closed (filters built total=%d)", label, last.id, last.filter.id, len(fb.snapshotFilters()))
	} else {
		t.Logf("%s REFUTED: recovery built interceptor#%d on a live filter#%d (filters built total=%d)", label, last.id, last.filter.id, len(fb.snapshotFilters()))
	}
	l.Close()
	vfyLogState(t, label+" after Close", fb)
	for _, f := range fb.snapshotFilters() {
		if n := f.closes.Load(); n > 1 {
			t.Errorf("%s: filter#%d physically closed %d times", label, f.id, n)
		}
	}
}

func (s) TestVerify_C3C8_ClosedFilterReuse_RDSError(t *testing.T) {
	vfyClosedFilterReuse(t, rdsWatcherUpdate{err: fmt.Errorf("injected rds error")}, "C3/C8 rds-error")
}

func (s) TestVerify_C3C8_ClosedFilterReuse_DisableAllUses(t *testing.T) {
	vfyClosedFilterReuse(t, rdsWatcherUpdate{data: vfyRouteConfigDisabled()}, "C3/C8 disable-all-uses")
}

func (s) TestVerify_C3C8_ClosedFilterReuse_ConstructionError(t *testing.T) {
	bad := &xdsresource.RouteConfigUpdate{VirtualHosts: []*xdsresource.VirtualHost{{Domains: []string{"*"}, Routes: []*xdsresource.Route{
		vfyRoute("/1", map[string]httpfilter.FilterConfig{"tracker": vfyCfg{fail: true}}),
	}}}}
	vfyClosedFilterReuse(t, rdsWatcherUpdate{data: bad}, "C3/C8 construction-error")
}

// C7: construction fails on a later route / later virtual host after earlier
// interceptors were built; track each earlier interceptor individually.
func (s) TestVerify_C7_PartialFailureRollback(t *testing.T) {
	failOverride := map[string]httpfilter.FilterConfig{"tracker": vfyCfg{fail: true}}
	cases := []struct {
		name string
		rc   *xdsresource.RouteConfigUpdate
	}{
		{"later-route-fails", &xdsresource.RouteConfigUpdate{VirtualHosts: []*xdsresource.VirtualHost{
			{Domains: []string{"*"}, Routes: []*xdsresource.Route{vfyRoute("/a", nil), vfyRoute("/b", nil), vfyRoute("/fail", failOverride)}},
		}}},
		{"later-virtual-host-fails", &xdsresource.RouteConfigUpdate{VirtualHosts: []*xdsresource.VirtualHost{
			{Domains: []string{"one"}, Routes: []*xdsresource.Route{vfyRoute("/a", nil), vfyRoute("/b", nil)}},
			{Domains: []string{"two"}, Routes: []*xdsresource.Route{vfyRoute("/fail", failOverride)}},
		}}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			fb := &vfyBuilder{}
			l, fc := newVerifyListenerWrapper(t, fb)
			l.handleRDSUpdate(vfyRouteName, rdsWatcherUpdate{data: vfyRouteConfig(1)})
			before := len(fb.snapshotInterceptors())
			l.handleRDSUpdate(vfyRouteName, rdsWatcherUpdate{data: c.rc})
			t.Logf("C7 %s: urc.err after failed construction = %v", c.name, fc.usableRouteConfiguration.Load().err)
			vfyLogState(t, "C7 "+c.name+" after failed update", fb)
			leaked := 0
			for _, i := range fb.snapshotInterceptors()[before:] {
				if i.closes.Load() == 0 {
					leaked++
					t.Logf("C7 %s: interceptor#%d built during the failed construction is still open after rollback", c.name, i.id)
				}
			}
			l.Close()
			vfyLogState(t, "C7 "+c.name+" after Close", fb)
			stillLeaked := 0
			for _, i := range fb.snapshotInterceptors()[before:] {
				if i.closes.Load() == 0 {
					stillLeaked++
				}
			}
			if stillLeaked > 0 {
				t.Errorf("C7 CONFIRMED (%s): %d interceptor(s) built before the injected failure were never closed, even after listener Close()", c.name, stillLeaked)
			} else {
				t.Logf("C7 REFUTED (%s): all %d interceptors built before the injected failure were closed during rollback (leaked-before-Close=%d)", c.name, len(fb.snapshotInterceptors())-before, leaked)
			}
		})
	}
}
