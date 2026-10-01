// Run (on a C3 target branch): cp verify/repro/verify_c3_rds_error_test.go internal/xds/server/ && go test -tags verify_audit -race -count=1 -v -run '^Test$/^VerifyC3_' ./internal/xds/server
//
// C3 probe: successful RDS update -> RDS resource error -> (error state served)
// and what happens to the underlying ServerFilter instance. Uses only the
// identifiers shared by evalon/grpc-go-xd-dc4ed267 and evalon/grpc-go-xd-20c60584.

//go:build verify_audit

package server

import (
	"context"
	"errors"
	"net"
	"sync/atomic"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/internal/grpcsync"
	"google.golang.org/grpc/internal/resolver"
	"google.golang.org/grpc/internal/transport"
	"google.golang.org/grpc/internal/xds/httpfilter"
	"google.golang.org/grpc/internal/xds/xdsclient/xdsresource"
	"google.golang.org/grpc/metadata"
)

type vc3Builder struct {
	httpfilter.Builder
	filters []*vc3Filter
}

func (*vc3Builder) TypeURLs() []string { return []string{"verify-c3-filter"} }
func (b *vc3Builder) BuildServerFilter() httpfilter.ServerFilter {
	f := &vc3Filter{}
	b.filters = append(b.filters, f)
	return f
}

type vc3Filter struct {
	closed       atomic.Int32
	interceptors []*vc3Interceptor
}

func (f *vc3Filter) BuildServerInterceptor(httpfilter.FilterConfig, httpfilter.FilterConfig) (resolver.ServerInterceptor, error) {
	i := &vc3Interceptor{f: f}
	f.interceptors = append(f.interceptors, i)
	return i, nil
}
func (f *vc3Filter) Close() { f.closed.Add(1) }

type vc3Interceptor struct {
	f      *vc3Filter
	closed atomic.Int32
	calls  atomic.Int32
}

func (i *vc3Interceptor) AllowRPC(context.Context) error { i.calls.Add(1); return nil }
func (i *vc3Interceptor) Close()                         { i.closed.Add(1) }

type vc3Stream struct{ grpc.ServerTransportStream }

func (vc3Stream) Method() string { return "/service/method" }

func (s) TestVerifyC3_RDSErrorFilterLiveness(t *testing.T) {
	builder := &vc3Builder{}
	hf := xdsresource.HTTPFilter{Name: "f", Filter: builder}
	fcm := newFilterChainManager(nil, &xdsresource.NetworkFilterChainConfig{
		HTTPConnMgr: &xdsresource.HTTPConnectionManagerConfig{
			RouteConfigName: "route",
			HTTPFilters:     []xdsresource.HTTPFilter{hf},
		},
	})
	fc := fcm.filterChains[0]
	lis, err := net.Listen("tcp", "localhost:0")
	if err != nil {
		t.Fatal(err)
	}
	l := &listenerWrapper{
		Listener:                 lis,
		closed:                   grpcsync.NewEvent(),
		activeFilterChainManager: fcm,
		httpFilters:              make(map[serverFilterKey]*refCountedServerFilter),
		rdsHandler:               &rdsHandler{},
	}
	t.Cleanup(func() { l.Close() })

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	// An "existing connection" on this filter chain: it shares the chain's
	// routing state exactly like conns created in listenerWrapper.Accept.
	ctx = transport.SetConnection(ctx, &connWrapper{urc: fc.usableRouteConfiguration})
	ctx = grpc.NewContextWithServerTransportStream(ctx, vc3Stream{})
	ctx = metadata.NewIncomingContext(ctx, metadata.Pairs(":authority", "service"))

	key := newServerFilterKey(&hf)
	report := func(stage string) {
		t.Helper()
		cached, ok := l.httpFilters[key]
		ref := int32(-1)
		if ok {
			ref = cached.refCnt.Load()
		}
		for n, f := range builder.filters {
			closedI := 0
			for _, i := range f.interceptors {
				if i.closed.Load() > 0 {
					closedI++
				}
			}
			t.Logf("C3 %-28s filter#%d underlyingCloseCalls=%d interceptorsBuilt=%d interceptorsClosed=%d | LDS cache entry present=%v refCnt=%d",
				stage, n+1, f.closed.Load(), len(f.interceptors), closedI, ok, ref)
		}
	}

	config := &xdsresource.RouteConfigUpdate{VirtualHosts: []*xdsresource.VirtualHost{{
		Domains: []string{"*"},
		Routes:  []*xdsresource.Route{{Prefix: newStringP("/"), ActionType: xdsresource.RouteActionNonForwardingAction}},
	}}}

	l.handleRDSUpdate("route", rdsWatcherUpdate{data: config})
	err = RouteAndProcess(ctx)
	t.Logf("C3 RouteAndProcess after successful RDS: err=%v", err)
	report("after successful RDS:")

	l.handleRDSUpdate("route", rdsWatcherUpdate{err: errors.New("verify: route resource removed")})
	err = RouteAndProcess(ctx)
	t.Logf("C3 RouteAndProcess while in RDS error state: err=%v", err)
	report("while serving RDS error:")
	if len(builder.filters) != 1 {
		t.Fatalf("built %d filters, want 1", len(builder.filters))
	}
	f := builder.filters[0]
	if f.closed.Load() == 0 {
		t.Logf("C3 OBSERVATION: FILTER-LIVE-DURING-ERROR-STATE: retired config's interceptors closed=%d/%d but underlying ServerFilter.Close calls=0", f.interceptors[0].closed.Load(), len(f.interceptors))
	} else {
		t.Logf("C3 OBSERVATION: FILTER-CLOSED-DURING-ERROR-STATE: underlying ServerFilter.Close calls=%d", f.closed.Load())
	}

	l.handleRDSUpdate("route", rdsWatcherUpdate{data: config})
	err = RouteAndProcess(ctx)
	t.Logf("C3 RouteAndProcess after recovery RDS: err=%v", err)
	report("after recovery RDS:")

	l.handleRDSUpdate("route", rdsWatcherUpdate{err: errors.New("verify: route resource removed again")})
	report("error state again:")
	l.Close()
	report("after listener Close:")
}
