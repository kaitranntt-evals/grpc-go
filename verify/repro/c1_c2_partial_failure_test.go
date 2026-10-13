// Run (needs both c1_c2_*.go files): git worktree add ../wt-a <remote>/evalon/grpc-go-xd-73a502c4 && cp verify/repro/c1_c2_*_test.go ../wt-a/internal/xds/server/ && (cd ../wt-a && go test -tags verify_repro -race -count=1 -v -run 'Test/VerifyPartial' ./internal/xds/server) -- FAIL with 'LEAK:' lines reproduces C1/C2.

//go:build verify_repro

package server

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"

	iresolver "google.golang.org/grpc/internal/resolver"
	"google.golang.org/grpc/internal/xds/httpfilter"
	"google.golang.org/grpc/internal/xds/xdsclient/xdsresource"
)

type verifyFailCfg struct {
	httpfilter.FilterConfig
	fail bool
}

// verifyPartialFilter builds interceptors, failing when the override config
// asks it to. It records every interceptor built and whether it was closed.
type verifyPartialFilter struct {
	httpfilter.Builder
	typeURL string
	built   []*verifyPartialInterceptor
}

func (f *verifyPartialFilter) TypeURLs() []string                         { return []string{f.typeURL} }
func (f *verifyPartialFilter) IsTerminal() bool                           { return false }
func (f *verifyPartialFilter) BuildServerFilter() httpfilter.ServerFilter { return f }
func (f *verifyPartialFilter) Close()                                     {}
func (f *verifyPartialFilter) BuildServerInterceptor(_, override httpfilter.FilterConfig) (iresolver.ServerInterceptor, error) {
	if c, ok := override.(verifyFailCfg); ok && c.fail {
		return nil, errors.New("injected interceptor construction failure")
	}
	i := &verifyPartialInterceptor{id: len(f.built)}
	f.built = append(f.built, i)
	return i, nil
}

type verifyPartialInterceptor struct {
	id     int
	closed atomic.Int32
}

func (*verifyPartialInterceptor) AllowRPC(context.Context) error { return nil }
func (i *verifyPartialInterceptor) Close()                       { i.closed.Add(1) }

func verifyPartialRun(t *testing.T, rc xdsresource.RouteConfigUpdate) {
	filter := &verifyPartialFilter{typeURL: t.Name()}
	fc := &filterChain{
		httpFilters:              []xdsresource.HTTPFilter{{Name: "tracker", Filter: filter, Config: verifyFailCfg{}}},
		usableRouteConfiguration: &atomic.Pointer[usableRouteConfiguration]{},
	}
	fc.usableRouteConfiguration.Store(&usableRouteConfiguration{})
	fcm := &filterChainManager{filterChains: []*filterChain{fc}}
	provider := func(f xdsresource.HTTPFilter) (httpfilter.ServerFilter, error) {
		return f.Filter.(httpfilter.ServerFilterBuilder).BuildServerFilter(), nil
	}

	urc := fc.constructUsableRouteConfiguration(rc, provider)
	t.Logf("constructUsableRouteConfiguration returned err=%v, vhs=%d", urc.err, len(urc.vhs))
	if urc.err == nil {
		t.Fatalf("expected a construction error")
	}
	report := func(when string) (leaked int) {
		for _, i := range filter.built {
			t.Logf("%s: interceptor #%d closed %d time(s)", when, i.id, i.closed.Load())
			if i.closed.Load() == 0 {
				leaked++
			}
		}
		return leaked
	}
	if len(filter.built) == 0 {
		t.Fatalf("no earlier interceptor was constructed; scenario not exercised")
	}
	afterErr := report("after error return")

	// Mirror what handleRDSUpdate does next (publish the error configuration),
	// then shut the filter chain manager down, to see whether any later
	// cleanup path picks the earlier interceptor up.
	publishVerifyURC(fc, urc)
	afterPublish := report("after publishing error config")
	fcm.stop()
	afterStop := report("after filterChainManager.stop()")
	if afterErr+afterPublish+afterStop > 0 {
		t.Errorf("LEAK: unclosed earlier interceptors: afterError=%d afterPublish=%d afterStop=%d (of %d built)", afterErr, afterPublish, afterStop, len(filter.built))
	}
}

func strp(s string) *string { return &s }

// Route 1 of the virtual host builds an interceptor; route 2 fails.
func (s) TestVerifyPartialRouteFailure(t *testing.T) {
	verifyPartialRun(t, xdsresource.RouteConfigUpdate{VirtualHosts: []*xdsresource.VirtualHost{{
		Domains: []string{"*"},
		Routes: []*xdsresource.Route{
			{Prefix: strp("/a")},
			{Prefix: strp("/b"), HTTPFilterConfigOverride: map[string]httpfilter.FilterConfig{"tracker": verifyFailCfg{fail: true}}},
		},
	}}})
}

// Virtual host 1 (two routes) is fully built; virtual host 2 fails.
func (s) TestVerifyPartialVirtualHostFailure(t *testing.T) {
	verifyPartialRun(t, xdsresource.RouteConfigUpdate{VirtualHosts: []*xdsresource.VirtualHost{
		{Domains: []string{"one.example.com"}, Routes: []*xdsresource.Route{{Prefix: strp("/a")}, {Prefix: strp("/b")}}},
		{Domains: []string{"two.example.com"}, Routes: []*xdsresource.Route{
			{Prefix: strp("/c"), HTTPFilterConfigOverride: map[string]httpfilter.FilterConfig{"tracker": verifyFailCfg{fail: true}}},
		}},
	}})
}
