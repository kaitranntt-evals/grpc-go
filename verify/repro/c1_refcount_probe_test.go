// Run: cp verify/repro/c1_refcount_probe_test.go internal/xds/server/zz_verify_c1_probe_test.go && go test -race -count=1 -v -run 'Test/VerifyC1' ./internal/xds/server/
//
// Probe for C1/C5 (in package `server`, drives the production handleRDSUpdate):
// records, for an in-place RouteConfiguration replacement, (a) the shared
// server filter's reference count at the moment each retired interceptor is
// closed, and (b) the relative order + call stacks of ServerFilter.Close and
// the retired interceptor's Close when the replacement disables the filter on
// every route. The probe never fails on the ordering; it prints PROBE lines.

package server

import (
	"context"
	"fmt"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"google.golang.org/grpc/internal/grpcsync"
	"google.golang.org/grpc/internal/resolver"
	"google.golang.org/grpc/internal/xds/httpfilter"
	"google.golang.org/grpc/internal/xds/xdsclient/xdsresource"
)

type verifyProbeEvent struct {
	kind   string
	id     int
	refCnt int32 // -1 when the refcounted wrapper is not yet known.
	stack  string
}

type verifyProbeBuilder struct {
	httpfilter.Builder

	rsf atomic.Pointer[refCountedServerFilter]

	mu     sync.Mutex
	events []verifyProbeEvent
	nextID int
}

func (b *verifyProbeBuilder) TypeURLs() []string { return []string{"verify.c1.probe"} }
func (b *verifyProbeBuilder) IsTerminal() bool   { return false }

func (b *verifyProbeBuilder) record(kind string, id int) {
	ref := int32(-1)
	if r := b.rsf.Load(); r != nil {
		ref = r.refCnt.Load()
	}
	pcs := make([]uintptr, 32)
	n := runtime.Callers(3, pcs)
	frames := runtime.CallersFrames(pcs[:n])
	var fns []string
	for {
		f, more := frames.Next()
		if strings.Contains(f.Function, "xds/server.") && !strings.Contains(f.Function, "verifyProbe") && !strings.Contains(f.Function, "TestVerify") {
			fns = append(fns, f.Function[strings.LastIndex(f.Function, "/")+1:])
		}
		if !more {
			break
		}
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.events = append(b.events, verifyProbeEvent{kind: kind, id: id, refCnt: ref, stack: strings.Join(fns, " <- ")})
}

func (b *verifyProbeBuilder) snapshot() []verifyProbeEvent {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]verifyProbeEvent(nil), b.events...)
}

func (b *verifyProbeBuilder) BuildServerFilter() httpfilter.ServerFilter {
	b.record("ServerFilter.Build", 0)
	return &verifyProbeFilter{b: b}
}

type verifyProbeFilter struct{ b *verifyProbeBuilder }

func (f *verifyProbeFilter) BuildServerInterceptor(_, _ httpfilter.FilterConfig) (resolver.ServerInterceptor, error) {
	f.b.mu.Lock()
	f.b.nextID++
	id := f.b.nextID
	f.b.mu.Unlock()
	f.b.record("Interceptor.Build", id)
	return &verifyProbeInterceptor{b: f.b, id: id}, nil
}

func (f *verifyProbeFilter) Close() { f.b.record("ServerFilter.Close(destroyed)", 0) }

type verifyProbeInterceptor struct {
	b  *verifyProbeBuilder
	id int
}

func (i *verifyProbeInterceptor) AllowRPC(context.Context) error { return nil }
func (i *verifyProbeInterceptor) Close()                         { i.b.record("Interceptor.Close", i.id) }

func verifyProbeRoute(override map[string]httpfilter.FilterConfig) *xdsresource.RouteConfigUpdate {
	return &xdsresource.RouteConfigUpdate{
		VirtualHosts: []*xdsresource.VirtualHost{{
			Domains: []string{"*"},
			Routes: []*xdsresource.Route{{
				Prefix:                   newStringP("/"),
				ActionType:               xdsresource.RouteActionNonForwardingAction,
				HTTPFilterConfigOverride: override,
			}},
		}},
	}
}

func verifyProbeRun(t *testing.T, v2Override map[string]httpfilter.FilterConfig) []verifyProbeEvent {
	const routeName = "route-name"
	b := &verifyProbeBuilder{}
	fc := &filterChain{
		routeConfigName:          routeName,
		httpFilters:              []xdsresource.HTTPFilter{{Name: "probe", Filter: b}},
		usableRouteConfiguration: &atomic.Pointer[usableRouteConfiguration]{},
	}
	fc.usableRouteConfiguration.Store(&usableRouteConfiguration{})
	lw := &listenerWrapper{
		xdsNodeID:                "node-id",
		closed:                   grpcsync.NewEvent(),
		httpFilters:              make(map[serverFilterKey]*refCountedServerFilter),
		conns:                    make(map[*connWrapper]bool),
		activeFilterChainManager: &filterChainManager{filterChains: []*filterChain{fc}},
		rdsHandler: &rdsHandler{
			updates: make(map[string]rdsWatcherUpdate),
			cancels: make(map[string]func()),
		},
	}

	// Generation 1: the filter is enabled on the only route.
	lw.handleRDSUpdate(routeName, rdsWatcherUpdate{data: verifyProbeRoute(nil)})
	lw.mu.Lock()
	for _, rsf := range lw.httpFilters {
		b.rsf.Store(rsf)
	}
	lw.mu.Unlock()
	if b.rsf.Load() == nil {
		t.Fatalf("no refcounted server filter registered after first RDS update")
	}
	b.record(fmt.Sprintf("--- gen1 active; in-place replacement starts (override=%v) ---", v2Override), 0)

	// Generation 2: in-place replacement for the same route configuration name.
	lw.handleRDSUpdate(routeName, rdsWatcherUpdate{data: verifyProbeRoute(v2Override)})
	b.record("--- handleRDSUpdate(gen2) returned ---", 0)

	// Retirement may be asynchronous on some implementations; wait (bounded)
	// for the gen1 interceptor to be closed.
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		closed := false
		for _, e := range b.snapshot() {
			if e.kind == "Interceptor.Close" && e.id == 1 {
				closed = true
			}
		}
		if closed {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	evs := b.snapshot()
	for n, e := range evs {
		t.Logf("PROBE event[%d] %-34s id=%d filterRefCnt=%d stack=[%s]", n, e.kind, e.id, e.refCnt, e.stack)
	}
	return evs
}

// Ordinary replacement: the filter stays enabled. If the retired
// configuration's filter reference were retained until its interceptor is
// closed, the reference count seen inside the retired interceptor's Close
// would be 2 (retired + replacement). A value of 1 means the retired
// reference was already released.
func (s) TestVerifyC1_RefCountAtRetiredInterceptorClose(t *testing.T) {
	evs := verifyProbeRun(t, nil)
	for _, e := range evs {
		if e.kind == "Interceptor.Close" && e.id == 1 {
			t.Logf("PROBE RESULT ordinary-replacement: filterRefCnt observed inside retired interceptor Close = %d (2 => retired reference still held; 1 => retired reference already released)", e.refCnt)
			return
		}
	}
	t.Logf("PROBE RESULT ordinary-replacement: retired interceptor was never closed within 3s")
}

// Replacement in which every route disables the filter: the retired reference
// is the last one, so releasing it destroys the filter. Report whether the
// filter was destroyed before or after the retired interceptor was closed.
func (s) TestVerifyC1_AllRoutesDisabledDestroyOrder(t *testing.T) {
	evs := verifyProbeRun(t, map[string]httpfilter.FilterConfig{"probe": httpfilter.DisabledFilterConfig{}})
	filterClose, icClose := -1, -1
	for n, e := range evs {
		if e.kind == "ServerFilter.Close(destroyed)" && filterClose < 0 {
			filterClose = n
		}
		if e.kind == "Interceptor.Close" && e.id == 1 && icClose < 0 {
			icClose = n
		}
	}
	switch {
	case filterClose < 0 || icClose < 0:
		t.Logf("PROBE RESULT all-disabled: filterDestroyedAt=%d retiredInterceptorClosedAt=%d (-1 => never observed)", filterClose, icClose)
	case filterClose < icClose:
		t.Logf("PROBE RESULT all-disabled: FILTER DESTROYED BEFORE retired interceptor closed (event %d < event %d)", filterClose, icClose)
	default:
		t.Logf("PROBE RESULT all-disabled: filter destroyed AFTER retired interceptor closed (event %d > event %d)", filterClose, icClose)
	}
}
