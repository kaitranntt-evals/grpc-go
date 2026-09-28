// Run: cp verify/repro/c1_filter_release_order_test.go test/xds/ && go test -race -count=1 -v -run '^TestVerify_C1_FilterReleaseOrder$' ./test/xds ; rm test/xds/c1_filter_release_order_test.go
//
// End-to-end probe for claims C1/C6 (and the disable/re-enable path of C3/C8)
// of the v-eb02180b audit. A real xDS management server publishes a server
// Listener whose HCM carries the instrumented filter "tracker" and an RDS
// route configuration. The route configuration is then replaced in place by
// one that disables every use of the filter, so releasing the retired
// configuration's filter reference drops the refcount to zero and physically
// closes the filter. The event log shows whether that physical close happened
// before or after the retired interceptor that depends on the filter was
// closed. Finally the filter is re-enabled to see whether construction reuses
// the already-closed filter instance.
package xds_test

import (
	"context"
	"fmt"
	"net"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/connectivity"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/internal/envconfig"
	"google.golang.org/grpc/internal/resolver"
	"google.golang.org/grpc/internal/testutils"
	"google.golang.org/grpc/internal/testutils/xds/e2e"
	"google.golang.org/grpc/internal/testutils/xds/e2e/setup"
	"google.golang.org/grpc/internal/xds/httpfilter"
	"google.golang.org/grpc/xds"

	v3xdsxdstypepb "github.com/cncf/xds/go/xds/type/v3"
	v3corepb "github.com/envoyproxy/go-control-plane/envoy/config/core/v3"
	v3listenerpb "github.com/envoyproxy/go-control-plane/envoy/config/listener/v3"
	v3routepb "github.com/envoyproxy/go-control-plane/envoy/config/route/v3"
	v3routerpb "github.com/envoyproxy/go-control-plane/envoy/extensions/filters/http/router/v3"
	v3httppb "github.com/envoyproxy/go-control-plane/envoy/extensions/filters/network/http_connection_manager/v3"
	testgrpc "google.golang.org/grpc/interop/grpc_testing"
	testpb "google.golang.org/grpc/interop/grpc_testing"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/anypb"
	"google.golang.org/protobuf/types/known/wrapperspb"
)

type vc1Cfg struct{ httpfilter.FilterConfig }

type vc1Builder struct {
	httpfilter.Builder
	typeURL      string
	mu           sync.Mutex
	events       []string
	filters      []*vc1Filter
	interceptors []*vc1Interceptor
}

func (b *vc1Builder) IsTerminal() bool   { return false }
func (b *vc1Builder) TypeURLs() []string { return []string{b.typeURL} }
func (b *vc1Builder) ParseFilterConfig(proto.Message) (httpfilter.FilterConfig, error) {
	return vc1Cfg{}, nil
}
func (b *vc1Builder) ParseFilterConfigOverride(proto.Message) (httpfilter.FilterConfig, error) {
	return vc1Cfg{}, nil
}
func (b *vc1Builder) record(format string, args ...any) {
	b.events = append(b.events, fmt.Sprintf(format, args...))
}
func (b *vc1Builder) BuildServerFilter() httpfilter.ServerFilter {
	b.mu.Lock()
	defer b.mu.Unlock()
	f := &vc1Filter{b: b, id: len(b.filters) + 1}
	b.filters = append(b.filters, f)
	b.record("filter#%d-build", f.id)
	return f
}
func (b *vc1Builder) eventLog() []string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]string(nil), b.events...)
}
func (b *vc1Builder) has(ev string) bool {
	for _, e := range b.eventLog() {
		if e == ev {
			return true
		}
	}
	return false
}
func (b *vc1Builder) numInterceptors() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.interceptors)
}

var _ httpfilter.ServerFilterBuilder = &vc1Builder{}

type vc1Filter struct {
	b      *vc1Builder
	id     int
	closes atomic.Int32
}

func (f *vc1Filter) Close() {
	n := f.closes.Add(1)
	f.b.mu.Lock()
	defer f.b.mu.Unlock()
	f.b.record("filter#%d-close(#%d)", f.id, n)
}

func (f *vc1Filter) BuildServerInterceptor(config, override httpfilter.FilterConfig) (resolver.ServerInterceptor, error) {
	f.b.mu.Lock()
	defer f.b.mu.Unlock()
	i := &vc1Interceptor{b: f.b, filter: f, id: len(f.b.interceptors) + 1, builtOnClosedFilter: f.closes.Load() > 0}
	f.b.interceptors = append(f.b.interceptors, i)
	if i.builtOnClosedFilter {
		f.b.record("interceptor#%d-build-ON-CLOSED-filter#%d", i.id, f.id)
	} else {
		f.b.record("interceptor#%d-build(filter#%d)", i.id, f.id)
	}
	return i, nil
}

type vc1Interceptor struct {
	b                   *vc1Builder
	filter              *vc1Filter
	id                  int
	builtOnClosedFilter bool
	closes              atomic.Int32
}

func (i *vc1Interceptor) AllowRPC(context.Context) error { return nil }
func (i *vc1Interceptor) Close() {
	n := i.closes.Add(1)
	i.b.mu.Lock()
	defer i.b.mu.Unlock()
	i.b.record("interceptor#%d-close(#%d)", i.id, n)
}

func vc1RouteConfig(t *testing.T, name string, disableTracker bool) *v3routepb.RouteConfiguration {
	route := &v3routepb.Route{
		Match:  &v3routepb.RouteMatch{PathSpecifier: &v3routepb.RouteMatch_Prefix{Prefix: "/grpc.testing.TestService/EmptyCall"}},
		Action: &v3routepb.Route_NonForwardingAction{},
	}
	if disableTracker {
		route.TypedPerFilterConfig = map[string]*anypb.Any{
			"tracker": testutils.MarshalAny(t, &v3routepb.FilterConfig{Disabled: true}),
		}
	}
	return &v3routepb.RouteConfiguration{Name: name, VirtualHosts: []*v3routepb.VirtualHost{{Domains: []string{"*"}, Routes: []*v3routepb.Route{route}}}}
}

func TestVerify_C1_FilterReleaseOrder(t *testing.T) {
	testutils.SetEnvConfig(t, &envconfig.XDSClientExtProcEnabled, true) // needed for FilterConfig.disabled to be honoured
	fb := &vc1Builder{typeURL: t.Name()}
	httpfilter.Register(fb)
	defer httpfilter.UnregisterForTesting(fb.typeURL)

	managementServer, nodeID, bootstrapContents, xdsResolver := setup.ManagementServerAndResolver(t)
	servingCh := make(chan struct{})
	opt := xds.ServingModeCallback(func(_ net.Addr, args xds.ServingModeChangeArgs) {
		if args.Mode == connectivity.ServingModeServing {
			close(servingCh)
		}
	})
	lis, stopServer := setupGRPCServer(t, bootstrapContents, opt)
	defer stopServer()
	host, port, err := hostPortFromListener(lis)
	if err != nil {
		t.Fatalf("hostPortFromListener: %v", err)
	}
	const serviceName = "my-service"
	resources := e2e.DefaultClientResources(e2e.ResourceParams{DialTarget: serviceName, NodeID: nodeID, Host: host, Port: port, SecLevel: e2e.SecurityLevelNone})

	const routeConfigName = "routeName"
	hcm := testutils.MarshalAny(t, &v3httppb.HttpConnectionManager{
		HttpFilters: []*v3httppb.HttpFilter{
			{Name: "tracker", ConfigType: &v3httppb.HttpFilter_TypedConfig{TypedConfig: testutils.MarshalAny(t, &v3xdsxdstypepb.TypedStruct{TypeUrl: fb.typeURL})}},
			e2e.HTTPFilter("router", &v3routerpb.Router{}),
		},
		RouteSpecifier: &v3httppb.HttpConnectionManager_Rds{Rds: &v3httppb.Rds{
			ConfigSource:    &v3corepb.ConfigSource{ConfigSourceSpecifier: &v3corepb.ConfigSource_Ads{Ads: &v3corepb.AggregatedConfigSource{}}},
			RouteConfigName: routeConfigName,
		}},
	})
	inboundLis := &v3listenerpb.Listener{
		Name:    fmt.Sprintf(e2e.ServerListenerResourceNameTemplate, net.JoinHostPort(host, strconv.Itoa(int(port)))),
		Address: &v3corepb.Address{Address: &v3corepb.Address_SocketAddress{SocketAddress: &v3corepb.SocketAddress{Address: host, PortSpecifier: &v3corepb.SocketAddress_PortValue{PortValue: port}}}},
		FilterChains: []*v3listenerpb.FilterChain{{
			Name: "v4-wildcard",
			FilterChainMatch: &v3listenerpb.FilterChainMatch{
				PrefixRanges:       []*v3corepb.CidrRange{{AddressPrefix: "0.0.0.0", PrefixLen: &wrapperspb.UInt32Value{Value: 0}}},
				SourceType:         v3listenerpb.FilterChainMatch_SAME_IP_OR_LOOPBACK,
				SourcePrefixRanges: []*v3corepb.CidrRange{{AddressPrefix: "0.0.0.0", PrefixLen: &wrapperspb.UInt32Value{Value: 0}}},
			},
			Filters: []*v3listenerpb.Filter{{Name: "hcm", ConfigType: &v3listenerpb.Filter_TypedConfig{TypedConfig: hcm}}},
		}},
	}
	resources.Listeners = append(resources.Listeners, inboundLis)
	resources.Routes = append(resources.Routes, vc1RouteConfig(t, routeConfigName, false))

	ctx, cancel := context.WithTimeout(context.Background(), defaultTestTimeout)
	defer cancel()
	if err := managementServer.Update(ctx, resources); err != nil {
		t.Fatal(err)
	}
	cc, err := grpc.NewClient(fmt.Sprintf("xds:///%s", serviceName), grpc.WithTransportCredentials(insecure.NewCredentials()), grpc.WithResolvers(xdsResolver))
	if err != nil {
		t.Fatalf("grpc.NewClient() failed: %v", err)
	}
	defer cc.Close()
	select {
	case <-servingCh:
	case <-ctx.Done():
		t.Fatalf("Timeout waiting for server to enter SERVING mode")
	}
	client := testgrpc.NewTestServiceClient(cc)
	if _, err := client.EmptyCall(ctx, &testpb.Empty{}); err != nil {
		t.Fatalf("EmptyCall() failed: %v", err)
	}
	t.Logf("PHASE 1 (published, 1 route using tracker): events=%v", fb.eventLog())
	if !fb.has("interceptor#1-build(filter#1)") {
		t.Fatalf("initial configuration did not build interceptor#1 on filter#1")
	}

	// PHASE 2: in-place RDS replacement that disables every use of tracker.
	resources.Routes[len(resources.Routes)-1] = vc1RouteConfig(t, routeConfigName, true)
	if err := managementServer.Update(ctx, resources); err != nil {
		t.Fatal(err)
	}
	waitCtx, waitCancel := context.WithTimeout(ctx, 5*time.Second)
	defer waitCancel()
	for ; waitCtx.Err() == nil; <-time.After(defaultTestShortTimeout) {
		if fb.has("filter#1-close(#1)") && fb.has("interceptor#1-close(#1)") {
			break
		}
	}
	ev := fb.eventLog()
	t.Logf("PHASE 2 (replaced by config disabling tracker): events=%v", ev)
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
		t.Errorf("C1: retired interceptor#1 was NOT closed by the in-place update (filter close index=%d)", fClose)
	case fClose < 0:
		t.Logf("C1: filter#1 was not physically closed by the update within 5s")
	case fClose < iClose:
		t.Errorf("C1 CONFIRMED: filter#1 physically closed (event %d) BEFORE its dependent retired interceptor#1 was closed (event %d)", fClose, iClose)
	default:
		t.Logf("C1 REFUTED: retired interceptor#1 closed (event %d) BEFORE filter#1 was physically closed (event %d)", iClose, fClose)
	}

	// PHASE 3: re-enable tracker; does construction reuse the closed filter?
	resources.Routes[len(resources.Routes)-1] = vc1RouteConfig(t, routeConfigName, false)
	if err := managementServer.Update(ctx, resources); err != nil {
		t.Fatal(err)
	}
	waitCtx3, waitCancel3 := context.WithTimeout(ctx, 5*time.Second)
	defer waitCancel3()
	for ; waitCtx3.Err() == nil; <-time.After(defaultTestShortTimeout) {
		if fb.numInterceptors() >= 2 {
			break
		}
	}
	t.Logf("PHASE 3 (tracker re-enabled): events=%v", fb.eventLog())
	if fb.has("interceptor#2-build-ON-CLOSED-filter#1") {
		t.Errorf("C3/C8 CONFIRMED: after disable/re-enable, interceptor#2 was built on filter#1 which had already been physically closed (no new filter built)")
	} else if fb.has("interceptor#2-build(filter#2)") {
		t.Logf("C3/C8 REFUTED (disable/re-enable): a fresh filter#2 was built for interceptor#2")
	} else {
		t.Logf("PHASE 3 inconclusive: interceptor#2 not built within 5s")
	}
	if _, err := client.EmptyCall(ctx, &testpb.Empty{}); err != nil {
		t.Logf("EmptyCall after re-enable failed: %v", err)
	}

	stopServer()
	t.Logf("FINAL (after server stop): events=%v", fb.eventLog())
	fb.mu.Lock()
	defer fb.mu.Unlock()
	for _, f := range fb.filters {
		t.Logf("  filter#%d closes=%d", f.id, f.closes.Load())
		if f.closes.Load() > 1 {
			t.Errorf("filter#%d physically closed %d times", f.id, f.closes.Load())
		}
	}
	for _, i := range fb.interceptors {
		t.Logf("  interceptor#%d (filter#%d) closes=%d builtOnClosedFilter=%v", i.id, i.filter.id, i.closes.Load(), i.builtOnClosedFilter)
	}
}
