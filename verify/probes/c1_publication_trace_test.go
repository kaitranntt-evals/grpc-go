// Run: cp verify/probes/c1_publication_trace_test.go internal/xds/server/ && go test -race -v -count=1 -run '^Test$/^Verify_C1_' ./internal/xds/server   (on the C1 target branch)

package server

import (
	"context"
	"fmt"
	"net"
	"strconv"
	"sync"
	"testing"
	"time"

	"google.golang.org/grpc/connectivity"
	iresolver "google.golang.org/grpc/internal/resolver"
	"google.golang.org/grpc/internal/testutils"
	"google.golang.org/grpc/internal/testutils/xds/e2e"
	"google.golang.org/grpc/internal/xds/httpfilter"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/structpb"

	v3xdsxdstypepb "github.com/cncf/xds/go/xds/type/v3"
	v3corepb "github.com/envoyproxy/go-control-plane/envoy/config/core/v3"
	v3listenerpb "github.com/envoyproxy/go-control-plane/envoy/config/listener/v3"
	v3routepb "github.com/envoyproxy/go-control-plane/envoy/config/route/v3"
	v3httppb "github.com/envoyproxy/go-control-plane/envoy/extensions/filters/network/http_connection_manager/v3"
)

type vc1Cfg struct{ httpfilter.FilterConfig }

// vc1Filter records, for every interceptor Close(), the value of the filter
// chain's published route configuration pointer at the instant of the call.
type vc1Filter struct {
	httpfilter.Builder
	typeURL string

	mu        sync.Mutex
	created   []*vc1Interceptor
	events    []string
	published func() *usableRouteConfiguration // reads fc.usableRouteConfiguration.Load()
}

func (f *vc1Filter) TypeURLs() []string { return []string{f.typeURL} }
func (f *vc1Filter) IsTerminal() bool   { return false }
func (f *vc1Filter) ParseFilterConfig(proto.Message) (httpfilter.FilterConfig, error) {
	return vc1Cfg{}, nil
}
func (f *vc1Filter) ParseFilterConfigOverride(proto.Message) (httpfilter.FilterConfig, error) {
	return vc1Cfg{}, nil
}
func (f *vc1Filter) BuildServerFilter() httpfilter.ServerFilter { return f }
func (f *vc1Filter) Close()                                     {}
func (f *vc1Filter) BuildServerInterceptor(httpfilter.FilterConfig, httpfilter.FilterConfig) (iresolver.ServerInterceptor, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	i := &vc1Interceptor{f: f, id: len(f.created) + 1}
	f.created = append(f.created, i)
	f.events = append(f.events, fmt.Sprintf("create interceptor#%d", i.id))
	return i, nil
}

type vc1Interceptor struct {
	f      *vc1Filter
	id     int
	closes int
	// publishedAtClose is the route pointer value seen at each Close() call.
	publishedAtClose []*usableRouteConfiguration
}

func (i *vc1Interceptor) AllowRPC(context.Context) error { return nil }
func (i *vc1Interceptor) Close() {
	p := i.f.published()
	i.f.mu.Lock()
	defer i.f.mu.Unlock()
	i.closes++
	i.publishedAtClose = append(i.publishedAtClose, p)
	i.f.events = append(i.f.events, fmt.Sprintf("close  interceptor#%d (call %d) routePointer=%p", i.id, i.closes, p))
}

func (f *vc1Filter) counts() []int {
	f.mu.Lock()
	defer f.mu.Unlock()
	var c []int
	for _, i := range f.created {
		c = append(c, i.closes)
	}
	return c
}

// TestVerify_C1_PublicationOrderAndCloseCounts drives real RDS updates through
// the xDS client into listenerWrapper.handleRDSUpdate and traces, for every
// in-place replacement and the final listener Close(), the order of route
// pointer publication vs. retirement and per-interceptor Close() counts.
func (s) TestVerify_C1_PublicationOrderAndCloseCounts(t *testing.T) {
	filter := &vc1Filter{typeURL: t.Name()}
	httpfilter.Register(filter)
	defer httpfilter.UnregisterForTesting(filter.typeURL)

	mgmtServer, nodeID, _, _, xdsC := xdsSetupForTests(t)
	lis, err := testutils.LocalTCPListener()
	if err != nil {
		t.Fatal(err)
	}
	host, p, _ := net.SplitHostPort(lis.Addr().String())
	port, _ := strconv.Atoi(p)

	modeCh := make(chan connectivity.ServingMode, 1)
	lw := NewListenerWrapper(ListenerWrapperParams{
		Listener: lis, ListenerResourceName: listenerName, XDSClient: xdsC,
		ModeCallback: func(_ net.Addr, mode connectivity.ServingMode, _ error) {
			select {
			case modeCh <- mode:
			default:
			}
		},
	}).(*listenerWrapper)
	defer lw.Close()
	// Close() hooks run on the goroutine that holds lw.mu, so this read is
	// unsynchronized on purpose.
	filter.published = func() *usableRouteConfiguration {
		return lw.activeFilterChainManager.filterChains[0].usableRouteConfiguration.Load()
	}

	ctx, cancel := context.WithTimeout(context.Background(), defaultTestTimeout)
	defer cancel()

	rc := func(prefix string) *v3routepb.RouteConfiguration {
		return &v3routepb.RouteConfiguration{
			Name: route1,
			VirtualHosts: []*v3routepb.VirtualHost{{
				Domains: []string{"*"},
				Routes: []*v3routepb.Route{{
					Match:  &v3routepb.RouteMatch{PathSpecifier: &v3routepb.RouteMatch_Prefix{Prefix: prefix}},
					Action: &v3routepb.Route_NonForwardingAction{},
				}},
			}},
		}
	}
	resources := e2e.UpdateOptions{
		NodeID: nodeID,
		Listeners: []*v3listenerpb.Listener{{
			Name: listenerName,
			Address: &v3corepb.Address{Address: &v3corepb.Address_SocketAddress{SocketAddress: &v3corepb.SocketAddress{
				Address: host, PortSpecifier: &v3corepb.SocketAddress_PortValue{PortValue: uint32(port)},
			}}},
			DefaultFilterChain: &v3listenerpb.FilterChain{Filters: []*v3listenerpb.Filter{{
				Name: "hcm",
				ConfigType: &v3listenerpb.Filter_TypedConfig{TypedConfig: testutils.MarshalAny(t, &v3httppb.HttpConnectionManager{
					RouteSpecifier: &v3httppb.HttpConnectionManager_Rds{Rds: &v3httppb.Rds{
						ConfigSource:    &v3corepb.ConfigSource{ConfigSourceSpecifier: &v3corepb.ConfigSource_Ads{Ads: &v3corepb.AggregatedConfigSource{}}},
						RouteConfigName: route1,
					}},
					HttpFilters: []*v3httppb.HttpFilter{
						{Name: "trace", ConfigType: &v3httppb.HttpFilter_TypedConfig{TypedConfig: testutils.MarshalAny(t, &v3xdsxdstypepb.TypedStruct{TypeUrl: filter.typeURL, Value: &structpb.Struct{}})}},
						e2e.RouterHTTPFilter,
					},
				})},
			}}},
		}},
		Routes:         []*v3routepb.RouteConfiguration{rc("/prefix-0")},
		SkipValidation: true,
	}
	if err := mgmtServer.Update(ctx, resources); err != nil {
		t.Fatal(err)
	}
	select {
	case <-modeCh:
	case <-ctx.Done():
		t.Fatal("timeout waiting for SERVING")
	}

	// waitGen waits for interceptor #n to exist and then takes lw.mu, which
	// handleRDSUpdate holds for the whole swap+retire, i.e. it returns only
	// once the replacement that created interceptor #n has completed.
	waitGen := func(n int) *usableRouteConfiguration {
		for ; ctx.Err() == nil; <-time.After(defaultTestShortTimeout) {
			filter.mu.Lock()
			got := len(filter.created)
			filter.mu.Unlock()
			if got >= n {
				lw.mu.Lock()
				p := lw.activeFilterChainManager.filterChains[0].usableRouteConfiguration.Load()
				lw.mu.Unlock()
				return p
			}
		}
		t.Fatalf("timeout waiting for interceptor #%d", n)
		return nil
	}

	gen := map[int]*usableRouteConfiguration{1: waitGen(1)}
	t.Logf("TRACE gen1 published routePointer=%p closeCounts=%v", gen[1], filter.counts())

	const updates = 3
	for n := 2; n <= updates+1; n++ {
		resources.Routes = []*v3routepb.RouteConfiguration{rc(fmt.Sprintf("/prefix-%d", n))}
		if err := mgmtServer.Update(ctx, resources); err != nil {
			t.Fatal(err)
		}
		gen[n] = waitGen(n)
		counts := filter.counts()
		t.Logf("TRACE replacement -> gen%d complete: published routePointer=%p closeCounts=%v", n, gen[n], counts)

		// publication_order: when the retired interceptor (#n-1) was closed,
		// the pointer must already have been the replacement (gen n).
		filter.mu.Lock()
		retired := filter.created[n-2]
		seen := append([]*usableRouteConfiguration(nil), retired.publishedAtClose...)
		filter.mu.Unlock()
		if len(seen) != 1 {
			t.Errorf("retired interceptor#%d has %d Close calls when replacement completed, want 1", n-1, len(seen))
			continue
		}
		switch seen[0] {
		case gen[n]:
			t.Logf("TRACE   interceptor#%d Close() saw routePointer == gen%d (replacement already published)", n-1, n)
		case gen[n-1]:
			t.Errorf("interceptor#%d Close() saw routePointer == gen%d: retirement started BEFORE publication", n-1, n-1)
		default:
			t.Errorf("interceptor#%d Close() saw unexpected routePointer %p", n-1, seen[0])
		}
		// retired_interceptor_lifecycle: every retired interceptor closed
		// exactly once, the live one not at all.
		for i, c := range counts {
			want := 1
			if i == n-1 {
				want = 0
			}
			if c != want {
				t.Errorf("after replacement -> gen%d: interceptor#%d close count = %d, want %d", n, i+1, c, want)
			}
		}
	}

	before := filter.counts()
	lw.Close()
	after := filter.counts()
	t.Logf("TRACE listener Close(): closeCounts before=%v after=%v", before, after)
	for i := range after {
		if i < len(after)-1 && after[i] != before[i] {
			t.Errorf("shutdown changed close count of retired interceptor#%d: %d -> %d", i+1, before[i], after[i])
		}
		if after[i] != 1 {
			t.Errorf("interceptor#%d close count after shutdown = %d, want 1", i+1, after[i])
		}
	}
	lw.Close() // A second Close() (deferred above) must not re-close anything either.
	t.Logf("TRACE second listener Close(): closeCounts=%v", filter.counts())

	filter.mu.Lock()
	defer filter.mu.Unlock()
	for _, e := range filter.events {
		t.Logf("EVENT %s", e)
	}
}
