// Run: on evalon/grpc-go-xd-a0b59987, cp this file to test/xds/verify_c2_parent_filter_closed_before_interceptor_test.go and run `go test -race -count=1 -v -run '^Test$/^VerifyC2_' ./test/xds` (the RemoveFilter subtests FAIL while the problem is present; KeepFilter is the control).

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
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/internal/envconfig"
	"google.golang.org/grpc/internal/resolver"
	"google.golang.org/grpc/internal/testutils"
	"google.golang.org/grpc/internal/testutils/xds/e2e"
	"google.golang.org/grpc/internal/testutils/xds/e2e/setup"
	"google.golang.org/grpc/internal/xds/httpfilter"

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
	"google.golang.org/protobuf/types/known/structpb"
)

type vc2Cfg struct {
	httpfilter.FilterConfig
	path string
}

func vc2Parse(cfg proto.Message) (httpfilter.FilterConfig, error) {
	ts, ok := cfg.(*v3xdsxdstypepb.TypedStruct)
	if !ok {
		return nil, fmt.Errorf("unsupported filter config type: %T", cfg)
	}
	return vc2Cfg{path: ts.GetValue().GetFields()["path"].GetStringValue()}, nil
}

// vc2Builder builds a distinct parent filter object per BuildServerFilter call
// and records lifecycle events in order.
type vc2Builder struct {
	typeURL      string
	allowEntered chan struct{}
	releaseAllow chan struct{}
	allowOnce    sync.Once

	mu       sync.Mutex
	events   []string
	nFilters int
}

func (b *vc2Builder) record(format string, args ...any) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.events = append(b.events, fmt.Sprintf(format, args...))
}
func (b *vc2Builder) snapshot() []string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]string(nil), b.events...)
}

func (b *vc2Builder) IsTerminal() bool   { return false }
func (b *vc2Builder) TypeURLs() []string { return []string{b.typeURL} }
func (*vc2Builder) ParseFilterConfig(cfg proto.Message) (httpfilter.FilterConfig, error) {
	return vc2Parse(cfg)
}
func (*vc2Builder) ParseFilterConfigOverride(cfg proto.Message) (httpfilter.FilterConfig, error) {
	return vc2Parse(cfg)
}
func (b *vc2Builder) BuildServerFilter() httpfilter.ServerFilter {
	b.mu.Lock()
	b.nFilters++
	id := b.nFilters
	b.events = append(b.events, fmt.Sprintf("filter#%d built", id))
	b.mu.Unlock()
	return &vc2Filter{b: b, id: id}
}

type vc2Filter struct {
	b  *vc2Builder
	id int
}

func (f *vc2Filter) Close() { f.b.record("filter#%d Close (PARENT FILTER CLOSED)", f.id) }
func (f *vc2Filter) BuildServerInterceptor(config, override httpfilter.FilterConfig) (resolver.ServerInterceptor, error) {
	cfg := config
	if override != nil {
		cfg = override
	}
	path := cfg.(vc2Cfg).path
	f.b.record("interceptor(%s) built from filter#%d", path, f.id)
	return &vc2Interceptor{f: f, path: path}, nil
}

type vc2Interceptor struct {
	f    *vc2Filter
	path string
}

func (i *vc2Interceptor) AllowRPC(ctx context.Context) error {
	if i.path != "path1" {
		return nil
	}
	b := i.f.b
	b.record("interceptor(path1) AllowRPC entered (RPC active in old interceptor)")
	b.allowOnce.Do(func() { close(b.allowEntered) })
	select {
	case <-b.releaseAllow:
	case <-ctx.Done():
		return ctx.Err()
	}
	b.record("interceptor(path1) AllowRPC returning")
	return nil
}
func (i *vc2Interceptor) Close() {
	i.f.b.record("interceptor(%s) of filter#%d Close entered", i.path, i.f.id)
	i.f.b.record("interceptor(%s) of filter#%d Close completed", i.path, i.f.id)
}

func vc2TypedStruct(t *testing.T, typeURL, path string) *anypb.Any {
	return testutils.MarshalAny(t, &v3xdsxdstypepb.TypedStruct{
		TypeUrl: typeURL,
		Value: &structpb.Struct{Fields: map[string]*structpb.Value{
			"path": {Kind: &structpb.Value_StringValue{StringValue: path}},
		}},
	})
}

func vc2RouteConfig(routes ...*v3routepb.Route) *v3routepb.RouteConfiguration {
	return &v3routepb.RouteConfiguration{
		Name:         "vc2RouteName",
		VirtualHosts: []*v3routepb.VirtualHost{{Domains: []string{"*"}, Routes: routes}},
	}
}

func vc2Route(override *anypb.Any) *v3routepb.Route {
	r := &v3routepb.Route{
		Match:  &v3routepb.RouteMatch{PathSpecifier: &v3routepb.RouteMatch_Prefix{Prefix: "/"}},
		Action: &v3routepb.Route_NonForwardingAction{},
	}
	if override != nil {
		r.TypedPerFilterConfig = map[string]*anypb.Any{"vc2-filter": override}
	}
	return r
}

func vc2Run(t *testing.T, replacement func(typeURL string) *v3routepb.RouteConfiguration, wantRemoved bool) {
	fb := &vc2Builder{typeURL: t.Name(), allowEntered: make(chan struct{}), releaseAllow: make(chan struct{})}
	httpfilter.Register(fb)
	t.Cleanup(func() { httpfilter.UnregisterForTesting(fb.typeURL) })

	managementServer, nodeID, bootstrapContents, xdsResolver := setup.ManagementServerAndResolver(t)
	lis, stopServer := setupGRPCServer(t, bootstrapContents)
	var stopOnce sync.Once
	stop := func() { stopOnce.Do(stopServer) }
	defer stop()
	host, port, err := hostPortFromListener(lis)
	if err != nil {
		t.Fatal(err)
	}

	const serviceName = "my-service"
	resources := e2e.DefaultClientResources(e2e.ResourceParams{DialTarget: serviceName, NodeID: nodeID, Host: host, Port: port, SecLevel: e2e.SecurityLevelNone})
	clientRoute := resources.Routes[0]
	resources.Listeners = append(resources.Listeners, &v3listenerpb.Listener{
		Name: fmt.Sprintf(e2e.ServerListenerResourceNameTemplate, net.JoinHostPort(host, strconv.Itoa(int(port)))),
		Address: &v3corepb.Address{Address: &v3corepb.Address_SocketAddress{SocketAddress: &v3corepb.SocketAddress{
			Address: host, PortSpecifier: &v3corepb.SocketAddress_PortValue{PortValue: port},
		}}},
		FilterChains: []*v3listenerpb.FilterChain{{
			Name: "catch-all",
			Filters: []*v3listenerpb.Filter{{
				Name: "hcm",
				ConfigType: &v3listenerpb.Filter_TypedConfig{TypedConfig: testutils.MarshalAny(t, &v3httppb.HttpConnectionManager{
					HttpFilters: []*v3httppb.HttpFilter{
						{Name: "vc2-filter", ConfigType: &v3httppb.HttpFilter_TypedConfig{TypedConfig: vc2TypedStruct(t, fb.typeURL, "path1")}},
						e2e.HTTPFilter("router", &v3routerpb.Router{}),
					},
					RouteSpecifier: &v3httppb.HttpConnectionManager_Rds{Rds: &v3httppb.Rds{
						ConfigSource:    &v3corepb.ConfigSource{ConfigSourceSpecifier: &v3corepb.ConfigSource_Ads{Ads: &v3corepb.AggregatedConfigSource{}}},
						RouteConfigName: "vc2RouteName",
					}},
				})},
			}},
		}},
	})
	resources.Routes = []*v3routepb.RouteConfiguration{clientRoute, vc2RouteConfig(vc2Route(nil))}

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
	client := testgrpc.NewTestServiceClient(cc)

	// Hold an RPC in the old interceptor's AllowRPC.
	heldDone := make(chan error, 1)
	go func() {
		_, err := client.EmptyCall(ctx, &testpb.Empty{}, grpc.WaitForReady(true))
		heldDone <- err
	}()
	select {
	case <-fb.allowEntered:
	case <-ctx.Done():
		t.Fatal("timeout waiting for AllowRPC(path1)")
	}

	// Replace the route configuration while the RPC is held.
	fb.record("--- test: sending replacement RouteConfiguration ---")
	resources.Routes = []*v3routepb.RouteConfiguration{clientRoute, replacement(fb.typeURL)}
	if err := managementServer.Update(ctx, resources); err != nil {
		t.Fatal(err)
	}
	// Give the server a bounded window to apply the update while the RPC is
	// still held in AllowRPC, then record what happened in that window.
	time.Sleep(2 * time.Second)
	fb.record("--- test: 2s after replacement; RPC still held in AllowRPC(path1); releasing it now ---")
	close(fb.releaseAllow)
	select {
	case err := <-heldDone:
		fb.record("--- test: held RPC completed, err=%v ---", err)
	case <-ctx.Done():
		t.Fatal("held RPC never completed")
	}
	time.Sleep(500 * time.Millisecond)
	fb.record("--- test: stopping server ---")
	stop()
	time.Sleep(500 * time.Millisecond)

	events := fb.snapshot()
	t.Logf("lifecycle events in order:\n  %s", strings.Join(events, "\n  "))

	idx := func(sub string) int {
		for i, e := range events {
			if strings.Contains(e, sub) {
				return i
			}
		}
		return -1
	}
	filterClose := idx("filter#1 Close (PARENT FILTER CLOSED)")
	icptCloseDone := idx("interceptor(path1) of filter#1 Close completed")
	allowReturn := idx("AllowRPC returning")
	if filterClose == -1 || icptCloseDone == -1 || allowReturn == -1 {
		t.Fatalf("missing events: filterClose=%d icptCloseDone=%d allowReturn=%d", filterClose, icptCloseDone, allowReturn)
	}
	t.Logf("event index: parent filter#1 Close=#%d, AllowRPC(path1) returning=#%d, old interceptor Close completed=#%d", filterClose, allowReturn, icptCloseDone)
	if filterClose < icptCloseDone {
		if !wantRemoved {
			t.Errorf("control violated: parent filter closed before old interceptor although filter was kept")
		}
		t.Errorf("PROBLEM PRESENT: parent filter#1 was closed (event #%d) before the old interceptor's Close completed (event #%d); it was closed while an RPC was still inside that interceptor's AllowRPC (AllowRPC returned at event #%d)", filterClose, icptCloseDone, allowReturn)
	} else {
		t.Logf("parent filter#1 stayed open until the old interceptor's Close completed")
	}
}

// Replacement disables the filter on the only route (per-route FilterConfig{Disabled: true}).
func (s) TestVerifyC2_RemoveFilter_DisabledOverride(t *testing.T) {
	testutils.SetEnvConfig(t, &envconfig.XDSClientExtProcEnabled, true)
	vc2Run(t, func(string) *v3routepb.RouteConfiguration {
		return vc2RouteConfig(vc2Route(testutils.MarshalAny(t, &v3routepb.FilterConfig{Disabled: true})))
	}, true)
}

// Replacement has a virtual host with no routes, so nothing references the filter.
func (s) TestVerifyC2_RemoveFilter_NoRoutes(t *testing.T) {
	vc2Run(t, func(string) *v3routepb.RouteConfiguration { return vc2RouteConfig() }, true)
}

// Control: replacement keeps the filter (override path2).
func (s) TestVerifyC2_KeepFilter_Control(t *testing.T) {
	vc2Run(t, func(typeURL string) *v3routepb.RouteConfiguration {
		return vc2RouteConfig(vc2Route(vc2TypedStruct(t, typeURL, "path2")))
	}, false)
}
