//go:build verify

// Run: cp verify/repro/verify_e2e_filter_lifecycle_test.go test/xds/ && go test -tags verify -race -count=1 -v -run 'Test/Verify_E2E' ./test/xds; rm test/xds/verify_e2e_filter_lifecycle_test.go
//
// End-to-end through the public xDS server API: an in-place RDS replacement
// that disables the tracked HTTP filter on every route. The retired
// configuration's interceptors must be closed before the server filter they
// were built from is destroyed (its last reference released). The test asserts
// the correct order, so a failure demonstrates the suspected problem.
package xds_test

import (
	"context"
	"fmt"
	"net"
	"strconv"
	"strings"
	"sync"
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

type vfyE2ECfg struct{ httpfilter.FilterConfig }

type vfyE2EBuilder struct {
	httpfilter.Builder
	typeURL string

	mu           sync.Mutex
	events       []string
	built        int
	destroyed    int
	interceptors []*vfyE2EInterceptor
}

func (b *vfyE2EBuilder) IsTerminal() bool   { return false }
func (b *vfyE2EBuilder) TypeURLs() []string { return []string{b.typeURL} }
func (*vfyE2EBuilder) ParseFilterConfig(proto.Message) (httpfilter.FilterConfig, error) {
	return vfyE2ECfg{}, nil
}
func (*vfyE2EBuilder) ParseFilterConfigOverride(proto.Message) (httpfilter.FilterConfig, error) {
	return vfyE2ECfg{}, nil
}
func (b *vfyE2EBuilder) BuildServerFilter() httpfilter.ServerFilter {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.built++
	b.events = append(b.events, fmt.Sprintf("filter_built#%d", b.built))
	return &vfyE2EServerFilter{b: b, id: b.built}
}

type vfyE2EServerFilter struct {
	b  *vfyE2EBuilder
	id int
}

func (f *vfyE2EServerFilter) BuildServerInterceptor(config, override httpfilter.FilterConfig) (resolver.ServerInterceptor, error) {
	f.b.mu.Lock()
	defer f.b.mu.Unlock()
	i := &vfyE2EInterceptor{b: f.b, id: len(f.b.interceptors) + 1, filterID: f.id}
	f.b.interceptors = append(f.b.interceptors, i)
	f.b.events = append(f.b.events, fmt.Sprintf("interceptor_created#%d(filter#%d)", i.id, f.id))
	return i, nil
}

func (f *vfyE2EServerFilter) Close() {
	f.b.mu.Lock()
	defer f.b.mu.Unlock()
	f.b.destroyed++
	f.b.events = append(f.b.events, fmt.Sprintf("FILTER_DESTROYED#%d", f.id))
}

type vfyE2EInterceptor struct {
	b                *vfyE2EBuilder
	id, filterID     int
	closeCount       int
	destroyedAtClose int
}

func (*vfyE2EInterceptor) AllowRPC(context.Context) error { return nil }
func (i *vfyE2EInterceptor) Close() {
	i.b.mu.Lock()
	defer i.b.mu.Unlock()
	i.closeCount++
	i.destroyedAtClose = i.b.destroyed
	i.b.events = append(i.b.events, fmt.Sprintf("interceptor_closed#%d(filter#%d) filtersDestroyedSoFar=%d", i.id, i.filterID, i.destroyedAtClose))
}

func (b *vfyE2EBuilder) snapshot() (created, closed, destroyed int, events []string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, i := range b.interceptors {
		closed += i.closeCount
	}
	return len(b.interceptors), closed, b.destroyed, append([]string(nil), b.events...)
}

func vfyE2ERoute(t *testing.T, disabledFilter, typeURL string) *v3routepb.Route {
	r := &v3routepb.Route{
		Match:  &v3routepb.RouteMatch{PathSpecifier: &v3routepb.RouteMatch_Prefix{Prefix: "/grpc.testing.TestService/EmptyCall"}},
		Action: &v3routepb.Route_NonForwardingAction{},
	}
	if disabledFilter != "" {
		r.TypedPerFilterConfig = map[string]*anypb.Any{
			disabledFilter: testutils.MarshalAny(t, &v3routepb.FilterConfig{
				Disabled: true,
				Config:   testutils.MarshalAny(t, &v3xdsxdstypepb.TypedStruct{TypeUrl: typeURL}),
			}),
		}
	}
	return r
}

func (s) TestVerify_E2E_RDSReplaceDisablingFilter_InterceptorsCloseBeforeFilterDestroyed(t *testing.T) {
	// Route-level "disabled" filter overrides are only honoured behind this
	// env config (see processHTTPFilterOverrides).
	testutils.SetEnvConfig(t, &envconfig.XDSClientExtProcEnabled, true)
	fb := &vfyE2EBuilder{typeURL: t.Name()}
	httpfilter.Register(fb)
	defer httpfilter.UnregisterForTesting(fb.typeURL)

	managementServer, nodeID, bootstrapContents, xdsResolver := setup.ManagementServerAndResolver(t)

	servingCh := make(chan struct{})
	var once sync.Once
	opt := xds.ServingModeCallback(func(_ net.Addr, args xds.ServingModeChangeArgs) {
		if args.Mode == connectivity.ServingModeServing {
			once.Do(func() { close(servingCh) })
		}
	})
	lis, stopServer := setupGRPCServer(t, bootstrapContents, opt)
	defer stopServer()

	host, port, err := hostPortFromListener(lis)
	if err != nil {
		t.Fatal(err)
	}
	const serviceName = "my-service"
	resources := e2e.DefaultClientResources(e2e.ResourceParams{
		DialTarget: serviceName, NodeID: nodeID, Host: host, Port: port, SecLevel: e2e.SecurityLevelNone,
	})

	const routeConfigName = "routeName"
	const filterName = "vfy-tracker"
	rds := func(disabled bool) *v3routepb.RouteConfiguration {
		df := ""
		if disabled {
			df = filterName
		}
		return &v3routepb.RouteConfiguration{
			Name:         routeConfigName,
			VirtualHosts: []*v3routepb.VirtualHost{{Domains: []string{"*"}, Routes: []*v3routepb.Route{vfyE2ERoute(t, df, fb.typeURL)}}},
		}
	}
	networkFilters := []*v3listenerpb.Filter{{
		Name: "hcm",
		ConfigType: &v3listenerpb.Filter_TypedConfig{
			TypedConfig: testutils.MarshalAny(t, &v3httppb.HttpConnectionManager{
				HttpFilters: []*v3httppb.HttpFilter{
					{Name: filterName, ConfigType: &v3httppb.HttpFilter_TypedConfig{TypedConfig: testutils.MarshalAny(t, &v3xdsxdstypepb.TypedStruct{TypeUrl: fb.typeURL})}},
					e2e.HTTPFilter("router", &v3routerpb.Router{}),
				},
				RouteSpecifier: &v3httppb.HttpConnectionManager_Rds{Rds: &v3httppb.Rds{
					ConfigSource:    &v3corepb.ConfigSource{ConfigSourceSpecifier: &v3corepb.ConfigSource_Ads{Ads: &v3corepb.AggregatedConfigSource{}}},
					RouteConfigName: routeConfigName,
				}},
			}),
		},
	}}
	fcm := func(name, prefix string) *v3listenerpb.FilterChain {
		return &v3listenerpb.FilterChain{
			Name: name,
			FilterChainMatch: &v3listenerpb.FilterChainMatch{
				PrefixRanges:       []*v3corepb.CidrRange{{AddressPrefix: prefix, PrefixLen: &wrapperspb.UInt32Value{Value: 0}}},
				SourceType:         v3listenerpb.FilterChainMatch_SAME_IP_OR_LOOPBACK,
				SourcePrefixRanges: []*v3corepb.CidrRange{{AddressPrefix: prefix, PrefixLen: &wrapperspb.UInt32Value{Value: 0}}},
			},
			Filters: networkFilters,
		}
	}
	inboundLis := &v3listenerpb.Listener{
		Name: fmt.Sprintf(e2e.ServerListenerResourceNameTemplate, net.JoinHostPort(host, strconv.Itoa(int(port)))),
		Address: &v3corepb.Address{Address: &v3corepb.Address_SocketAddress{SocketAddress: &v3corepb.SocketAddress{
			Address: host, PortSpecifier: &v3corepb.SocketAddress_PortValue{PortValue: port},
		}}},
		FilterChains: []*v3listenerpb.FilterChain{fcm("v4-wildcard", "0.0.0.0"), fcm("v6-wildcard", "::")},
	}
	resources.Listeners = append(resources.Listeners, inboundLis)
	resources.Routes = append(resources.Routes, rds(false))

	ctx, cancel := context.WithTimeout(context.Background(), defaultTestTimeout)
	defer cancel()
	if err := managementServer.Update(ctx, resources); err != nil {
		t.Fatal(err)
	}
	cc, err := grpc.NewClient(fmt.Sprintf("xds:///%s", serviceName), grpc.WithTransportCredentials(insecure.NewCredentials()), grpc.WithResolvers(xdsResolver))
	if err != nil {
		t.Fatal(err)
	}
	defer cc.Close()
	select {
	case <-servingCh:
	case <-ctx.Done():
		t.Fatal("timeout waiting for SERVING")
	}
	client := testgrpc.NewTestServiceClient(cc)
	if _, err := client.EmptyCall(ctx, &testpb.Empty{}); err != nil {
		t.Fatalf("EmptyCall() failed: %v", err)
	}
	created, closed, destroyed, _ := fb.snapshot()
	if created != 2 || closed != 0 || destroyed != 0 {
		t.Fatalf("after initial config: created=%d closed=%d destroyed=%d, want 2/0/0", created, closed, destroyed)
	}

	// In-place replacement: same route, tracker filter disabled on every route.
	resources.Routes = []*v3routepb.RouteConfiguration{resources.Routes[0], rds(true)}
	if err := managementServer.Update(ctx, resources); err != nil {
		t.Fatal(err)
	}
	for ; ctx.Err() == nil; <-time.After(defaultTestShortTimeout) {
		if _, err := client.EmptyCall(ctx, &testpb.Empty{}); err != nil {
			t.Fatalf("EmptyCall() failed: %v", err)
		}
		if _, closed, _, _ := fb.snapshot(); closed >= 2 {
			break
		}
	}
	if ctx.Err() != nil {
		t.Fatalf("timeout waiting for retired interceptors to close: %v", ctx.Err())
	}
	created, closed, destroyed, events := fb.snapshot()
	t.Logf("after replacement: created=%d closed=%d filtersDestroyed=%d\n  %s", created, closed, destroyed, strings.Join(events, "\n  "))
	if destroyed != 1 {
		t.Errorf("filter destroyed %d times after replacement that disables it everywhere, want 1", destroyed)
	}
	fb.mu.Lock()
	for _, i := range fb.interceptors {
		if i.closeCount == 1 && i.destroyedAtClose != 0 {
			t.Errorf("interceptor#%d was closed AFTER the server filter it depends on had already been destroyed (filtersDestroyedSoFar=%d at close)", i.id, i.destroyedAtClose)
		}
	}
	fb.mu.Unlock()

	stopServer()
	_, closed, destroyed, _ = fb.snapshot()
	if closed != 2 || destroyed != 1 {
		t.Errorf("after shutdown: closed=%d destroyed=%d, want 2/1", closed, destroyed)
	}
}
