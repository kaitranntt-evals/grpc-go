// Run: cp verify/repro/c5_c11_closed_filter_reuse_test.go test/xds/verify_c5_c11_closed_filter_reuse_test.go && go test -race -count=1 -v -run '^Test$/^VerifyC5C11_' ./test/xds
//
// Audit repro for C5 and C11 (run v-a332a9ce). On a live xDS-enabled gRPC
// server the Listener (LDS) is never changed; only the RouteConfiguration of
// the same name is updated through RDS:
//   EnableDisableEnable: filter enabled -> disabled on every route -> enabled again
//   EnableEmptyEnable:   routes present -> no virtual hosts        -> routes present again
// The test FAILS when the re-enabling update builds an interceptor on the
// ServerFilter instance whose Close() completed during the disabling update.

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
	"google.golang.org/protobuf/types/known/structpb"
)

type vc5Cfg struct{ httpfilter.FilterConfig }

type vc5Builder struct {
	httpfilter.Builder
	t       *testing.T
	typeURL string

	mu      sync.Mutex
	filters []*vc5Filter
}

func (b *vc5Builder) IsTerminal() bool   { return false }
func (b *vc5Builder) TypeURLs() []string { return []string{b.typeURL} }
func (*vc5Builder) ParseFilterConfig(proto.Message) (httpfilter.FilterConfig, error) {
	return vc5Cfg{}, nil
}
func (*vc5Builder) ParseFilterConfigOverride(proto.Message) (httpfilter.FilterConfig, error) {
	return vc5Cfg{}, nil
}
func (b *vc5Builder) BuildServerFilter() httpfilter.ServerFilter {
	b.mu.Lock()
	defer b.mu.Unlock()
	f := &vc5Filter{b: b, id: len(b.filters) + 1}
	b.filters = append(b.filters, f)
	b.t.Logf("EVENT filter F%d built", f.id)
	return f
}

func (b *vc5Builder) snapshot() []*vc5Filter {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]*vc5Filter(nil), b.filters...)
}

type vc5Filter struct {
	b                *vc5Builder
	id               int
	closes           atomic.Int32
	builds           atomic.Int32
	buildsAfterClose atomic.Int32
	allowsAfterClose atomic.Int32
}

func (f *vc5Filter) BuildServerInterceptor(_, _ httpfilter.FilterConfig) (resolver.ServerInterceptor, error) {
	n := f.builds.Add(1)
	if f.closes.Load() > 0 {
		f.buildsAfterClose.Add(1)
		f.b.t.Logf("EVENT filter F%d: BuildServerInterceptor #%d called AFTER the filter's Close() completed", f.id, n)
	} else {
		f.b.t.Logf("EVENT filter F%d: BuildServerInterceptor #%d", f.id, n)
	}
	return &vc5Icpt{f: f}, nil
}

func (f *vc5Filter) Close() {
	f.b.t.Logf("EVENT filter F%d: Close() completed (close #%d)", f.id, f.closes.Add(1))
}

type vc5Icpt struct{ f *vc5Filter }

func (i *vc5Icpt) AllowRPC(context.Context) error {
	if i.f.closes.Load() > 0 {
		i.f.allowsAfterClose.Add(1)
	}
	return nil
}
func (i *vc5Icpt) Close() {}

const vc5FilterName, vc5RouteName = "vreuse", "routeName"

type vc5Mode int

const (
	vc5Enabled vc5Mode = iota
	vc5DisabledEverywhere
	vc5NoVirtualHosts
)

func vc5Routes(t *testing.T, mode vc5Mode) *v3routepb.RouteConfiguration {
	rc := &v3routepb.RouteConfiguration{Name: vc5RouteName}
	if mode == vc5NoVirtualHosts {
		return rc
	}
	vh := &v3routepb.VirtualHost{Domains: []string{"*"}, Routes: []*v3routepb.Route{{
		Match:  &v3routepb.RouteMatch{PathSpecifier: &v3routepb.RouteMatch_Prefix{Prefix: "/"}},
		Action: &v3routepb.Route_NonForwardingAction{},
	}}}
	if mode == vc5DisabledEverywhere {
		vh.TypedPerFilterConfig = map[string]*anypb.Any{vc5FilterName: testutils.MarshalAny(t, &v3routepb.FilterConfig{Disabled: true})}
	}
	rc.VirtualHosts = []*v3routepb.VirtualHost{vh}
	return rc
}

func vc5Run(t *testing.T, middle vc5Mode) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	fb := &vc5Builder{t: t, typeURL: t.Name()}
	httpfilter.Register(fb)
	defer httpfilter.UnregisterForTesting(fb.typeURL)

	ms, nodeID, bootstrapContents, xdsResolver := setup.ManagementServerAndResolver(t)
	servingCh := make(chan struct{}, 10)
	lis, stopServer := setupGRPCServer(t, bootstrapContents, xds.ServingModeCallback(func(_ net.Addr, args xds.ServingModeChangeArgs) {
		if args.Mode == connectivity.ServingModeServing {
			select {
			case servingCh <- struct{}{}:
			default:
			}
		}
	}))
	defer stopServer()
	host, port, err := hostPortFromListener(lis)
	if err != nil {
		t.Fatal(err)
	}
	const serviceName = "my-service"
	resources := e2e.DefaultClientResources(e2e.ResourceParams{DialTarget: serviceName, NodeID: nodeID, Host: host, Port: port, SecLevel: e2e.SecurityLevelNone})
	// The Listener is built exactly once and never modified afterwards.
	resources.Listeners = append(resources.Listeners, &v3listenerpb.Listener{
		Name: fmt.Sprintf(e2e.ServerListenerResourceNameTemplate, net.JoinHostPort(host, strconv.Itoa(int(port)))),
		Address: &v3corepb.Address{Address: &v3corepb.Address_SocketAddress{SocketAddress: &v3corepb.SocketAddress{
			Address: host, PortSpecifier: &v3corepb.SocketAddress_PortValue{PortValue: port}}}},
		FilterChains: []*v3listenerpb.FilterChain{{
			Name: "only-chain",
			Filters: []*v3listenerpb.Filter{{
				Name: "hcm",
				ConfigType: &v3listenerpb.Filter_TypedConfig{TypedConfig: testutils.MarshalAny(t, &v3httppb.HttpConnectionManager{
					HttpFilters: []*v3httppb.HttpFilter{
						{Name: vc5FilterName, ConfigType: &v3httppb.HttpFilter_TypedConfig{TypedConfig: testutils.MarshalAny(t, &v3xdsxdstypepb.TypedStruct{TypeUrl: fb.typeURL, Value: &structpb.Struct{}})}},
						e2e.HTTPFilter("router", &v3routerpb.Router{}),
					},
					RouteSpecifier: &v3httppb.HttpConnectionManager_Rds{Rds: &v3httppb.Rds{
						ConfigSource:    &v3corepb.ConfigSource{ConfigSourceSpecifier: &v3corepb.ConfigSource_Ads{Ads: &v3corepb.AggregatedConfigSource{}}},
						RouteConfigName: vc5RouteName,
					}},
				})},
			}},
		}},
	})
	clientRoutes := resources.Routes
	setRoutes := func(mode vc5Mode) {
		resources.Routes = append(append([]*v3routepb.RouteConfiguration{}, clientRoutes...), vc5Routes(t, mode))
		if err := ms.Update(ctx, resources); err != nil {
			t.Fatal(err)
		}
	}
	waitFor := func(desc string, cond func() bool) {
		deadline := time.Now().Add(5 * time.Second)
		for time.Now().Before(deadline) {
			if cond() {
				return
			}
			time.Sleep(10 * time.Millisecond)
		}
		t.Logf("NOTE: %q not observed within 5s", desc)
	}

	t.Logf("STEP 1: RDS enables the filter")
	setRoutes(vc5Enabled)
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
	if _, err := client.EmptyCall(ctx, &testpb.Empty{}, grpc.WaitForReady(true)); err != nil {
		t.Fatalf("EmptyCall() failed: %v", err)
	}
	filters := fb.snapshot()
	if len(filters) != 1 {
		t.Fatalf("got %d filter instances after step 1, want 1", len(filters))
	}
	f1 := filters[0]
	buildsStep1 := f1.builds.Load()

	t.Logf("STEP 2: RDS update of the same name removes every use of the filter (LDS unchanged)")
	setRoutes(middle)
	waitFor("filter F1 closed", func() bool { return f1.closes.Load() > 0 })
	t.Logf("after step 2: F1 closes=%d, filter instances built=%d", f1.closes.Load(), len(fb.snapshot()))

	t.Logf("STEP 3: RDS update of the same name enables the filter again (LDS unchanged)")
	setRoutes(vc5Enabled)
	waitFor("an interceptor built after step 2", func() bool {
		for _, f := range fb.snapshot() {
			if f != f1 && f.builds.Load() > 0 {
				return true
			}
		}
		return f1.builds.Load() > buildsStep1
	})
	rpcErr := error(nil)
	waitFor("RPC routed through re-enabled filter", func() bool {
		rctx, rcancel := context.WithTimeout(ctx, time.Second)
		defer rcancel()
		_, rpcErr = client.EmptyCall(rctx, &testpb.Empty{})
		return rpcErr == nil
	})
	filters = fb.snapshot()
	t.Logf("RESULT: filter instances built in total=%d; F1 closes=%d; interceptors built on F1 after its Close()=%d; RPCs allowed by interceptors of closed F1=%d; RPC after re-enable err=%v",
		len(filters), f1.closes.Load(), f1.buildsAfterClose.Load(), f1.allowsAfterClose.Load(), rpcErr)
	if f1.closes.Load() > 0 && f1.buildsAfterClose.Load() > 0 {
		t.Errorf("re-enabling reused the ServerFilter instance closed during disabling: %d interceptor(s) built on closed filter F1 and no new filter instance was built (instances=%d)", f1.buildsAfterClose.Load(), len(filters))
	}
}

func (s) TestVerifyC5C11_EnableDisableEnable(t *testing.T) {
	// Per-route `FilterConfig{disabled: true}` is honored only with this flag
	// (GRPC_EXPERIMENTAL_XDS_EXT_PROC_ON_CLIENT=true).
	testutils.SetEnvConfig(t, &envconfig.XDSClientExtProcEnabled, true)
	vc5Run(t, vc5DisabledEverywhere)
}

func (s) TestVerifyC5C11_EnableEmptyEnable(t *testing.T) {
	// No experimental flag: the middle update simply has no virtual hosts.
	vc5Run(t, vc5NoVirtualHosts)
}
