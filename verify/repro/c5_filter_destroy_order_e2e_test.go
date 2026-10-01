// Run: cp verify/repro/c5_filter_destroy_order_e2e_test.go test/xds/zz_verify_c5_e2e_test.go && go test -race -count=1 -v -run 'Test/VerifyC5' ./test/xds/
//
// End-to-end probe for C5 (and C1): a real xDS-enabled gRPC server receives an
// in-place RouteConfiguration replacement, through the management server, in
// which the previously used HTTP filter is no longer used by any route. The
// probe logs the order (and production call stacks) of ServerFilter.Close and
// the retired interceptors' Close. It never fails on ordering; read the
// "PROBE" lines.

package xds_test

import (
	"context"
	"fmt"
	"net"
	"runtime"
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
)

type verifyC5Cfg struct{ httpfilter.FilterConfig }

type verifyC5Builder struct {
	httpfilter.Builder
	typeURL string

	mu           sync.Mutex
	events       []string
	nextID       int
	filterClosed bool
	// closedAfterFilterDestroyed counts interceptors whose Close ran when the
	// parent filter had already been destroyed.
	closedAfterFilterDestroyed int
	closedBeforeFilterDestroy  int
}

func (b *verifyC5Builder) IsTerminal() bool   { return false }
func (b *verifyC5Builder) TypeURLs() []string { return []string{b.typeURL} }
func (*verifyC5Builder) ParseFilterConfig(proto.Message) (httpfilter.FilterConfig, error) {
	return verifyC5Cfg{}, nil
}
func (*verifyC5Builder) ParseFilterConfigOverride(proto.Message) (httpfilter.FilterConfig, error) {
	return verifyC5Cfg{}, nil
}

func verifyC5Stack() string {
	pcs := make([]uintptr, 32)
	n := runtime.Callers(3, pcs)
	frames := runtime.CallersFrames(pcs[:n])
	var fns []string
	for {
		f, more := frames.Next()
		if strings.Contains(f.Function, "internal/xds/server.") {
			fns = append(fns, f.Function[strings.LastIndex(f.Function, "/")+1:])
		}
		if !more {
			break
		}
	}
	return strings.Join(fns, " <- ")
}

func (b *verifyC5Builder) logf(format string, args ...any) {
	b.events = append(b.events, fmt.Sprintf(format, args...))
}

func (b *verifyC5Builder) BuildServerFilter() httpfilter.ServerFilter {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.logf("ServerFilter.Build")
	return b
}

func (b *verifyC5Builder) Close() {
	st := verifyC5Stack()
	b.mu.Lock()
	defer b.mu.Unlock()
	b.filterClosed = true
	b.logf("ServerFilter.Close (filter DESTROYED) stack=[%s]", st)
}

func (b *verifyC5Builder) BuildServerInterceptor(_, _ httpfilter.FilterConfig) (resolver.ServerInterceptor, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.nextID++
	b.logf("Interceptor#%d.Build", b.nextID)
	return &verifyC5Interceptor{b: b, id: b.nextID}, nil
}

type verifyC5Interceptor struct {
	b  *verifyC5Builder
	id int
}

func (i *verifyC5Interceptor) AllowRPC(context.Context) error { return nil }
func (i *verifyC5Interceptor) Close() {
	st := verifyC5Stack()
	i.b.mu.Lock()
	defer i.b.mu.Unlock()
	if i.b.filterClosed {
		i.b.closedAfterFilterDestroyed++
	} else {
		i.b.closedBeforeFilterDestroy++
	}
	i.b.logf("Interceptor#%d.Close parentFilterAlreadyDestroyed=%v stack=[%s]", i.id, i.b.filterClosed, st)
}

func verifyC5Run(t *testing.T, rds2 func(name string) *v3routepb.RouteConfiguration) {
	fb := &verifyC5Builder{typeURL: t.Name()}
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
		t.Fatalf("failed to retrieve host and port of server: %v", err)
	}
	const serviceName = "my-service"
	const routeConfigName = "routeName"
	resources := e2e.DefaultClientResources(e2e.ResourceParams{
		DialTarget: serviceName, NodeID: nodeID, Host: host, Port: port, SecLevel: e2e.SecurityLevelNone,
	})
	rds1 := &v3routepb.RouteConfiguration{
		Name: routeConfigName,
		VirtualHosts: []*v3routepb.VirtualHost{{
			Domains: []string{"*"},
			Routes: []*v3routepb.Route{{
				Match:  &v3routepb.RouteMatch{PathSpecifier: &v3routepb.RouteMatch_Prefix{Prefix: "/"}},
				Action: &v3routepb.Route_NonForwardingAction{},
			}},
		}},
	}
	inboundLis := &v3listenerpb.Listener{
		Name: fmt.Sprintf(e2e.ServerListenerResourceNameTemplate, net.JoinHostPort(host, strconv.Itoa(int(port)))),
		Address: &v3corepb.Address{Address: &v3corepb.Address_SocketAddress{SocketAddress: &v3corepb.SocketAddress{
			Address: host, PortSpecifier: &v3corepb.SocketAddress_PortValue{PortValue: port},
		}}},
		// A single (default) filter chain, so exactly one interceptor is built
		// per route configuration generation.
		DefaultFilterChain: &v3listenerpb.FilterChain{
			Name: "default",
			Filters: []*v3listenerpb.Filter{{
				Name: "hcm",
				ConfigType: &v3listenerpb.Filter_TypedConfig{
					TypedConfig: testutils.MarshalAny(t, &v3httppb.HttpConnectionManager{
						HttpFilters: []*v3httppb.HttpFilter{
							{
								Name: "tracker",
								ConfigType: &v3httppb.HttpFilter_TypedConfig{
									TypedConfig: testutils.MarshalAny(t, &v3xdsxdstypepb.TypedStruct{TypeUrl: fb.typeURL}),
								},
							},
							e2e.HTTPFilter("router", &v3routerpb.Router{}),
						},
						RouteSpecifier: &v3httppb.HttpConnectionManager_Rds{Rds: &v3httppb.Rds{
							ConfigSource: &v3corepb.ConfigSource{
								ConfigSourceSpecifier: &v3corepb.ConfigSource_Ads{Ads: &v3corepb.AggregatedConfigSource{}},
							},
							RouteConfigName: routeConfigName,
						}},
					}),
				},
			}},
		},
	}
	resources.Listeners = append(resources.Listeners, inboundLis)
	resources.Routes = append(resources.Routes, rds1)

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
	if _, err := client.EmptyCall(ctx, &testpb.Empty{}, grpc.WaitForReady(true)); err != nil {
		t.Fatalf("EmptyCall() on generation 1 failed: %v", err)
	}
	fb.mu.Lock()
	fb.logf("--- generation 1 serving (RPC OK); sending in-place RDS replacement for %q ---", routeConfigName)
	fb.mu.Unlock()

	resources.Routes = []*v3routepb.RouteConfiguration{resources.Routes[0], rds2(routeConfigName)}
	if err := managementServer.Update(ctx, resources); err != nil {
		t.Fatal(err)
	}

	// Bounded wait for the retired generation-1 interceptor to be closed.
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		fb.mu.Lock()
		done := fb.closedAfterFilterDestroyed+fb.closedBeforeFilterDestroy >= 1
		fb.mu.Unlock()
		if done {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	fb.mu.Lock()
	fb.logf("--- replacement observed; stopping server ---")
	after, before := fb.closedAfterFilterDestroyed, fb.closedBeforeFilterDestroy
	fb.mu.Unlock()
	stopServer()

	fb.mu.Lock()
	defer fb.mu.Unlock()
	for n, e := range fb.events {
		t.Logf("PROBE event[%d] %s", n, e)
	}
	t.Logf("PROBE RESULT after in-place replacement: retired interceptors closed while parent filter still alive=%d, closed AFTER parent filter was destroyed=%d", before, after)
}

// Replacement whose only route disables the filter via
// typed_per_filter_config{FilterConfig{disabled:true}}. gRPC only honours the
// `disabled` bit when GRPC_EXPERIMENTAL_XDS_EXT_PROC_ON_CLIENT is enabled.
func (s) TestVerifyC5_AllRoutesDisableFilter(t *testing.T) {
	orig := envconfig.XDSClientExtProcEnabled
	envconfig.XDSClientExtProcEnabled = true
	defer func() { envconfig.XDSClientExtProcEnabled = orig }()

	verifyC5Run(t, func(name string) *v3routepb.RouteConfiguration {
		return &v3routepb.RouteConfiguration{
			Name: name,
			VirtualHosts: []*v3routepb.VirtualHost{{
				Domains: []string{"*"},
				Routes: []*v3routepb.Route{{
					Match:  &v3routepb.RouteMatch{PathSpecifier: &v3routepb.RouteMatch_Prefix{Prefix: "/"}},
					Action: &v3routepb.Route_NonForwardingAction{},
					TypedPerFilterConfig: map[string]*anypb.Any{
						"tracker": testutils.MarshalAny(t, &v3routepb.FilterConfig{Disabled: true}),
					},
				}},
			}},
		}
	})
}

// Replacement that simply has no routes left (no experimental flag needed):
// the retired configuration again holds the last reference to the filter.
func (s) TestVerifyC5_ReplacementWithNoRoutes(t *testing.T) {
	verifyC5Run(t, func(name string) *v3routepb.RouteConfiguration {
		return &v3routepb.RouteConfiguration{
			Name:         name,
			VirtualHosts: []*v3routepb.VirtualHost{{Domains: []string{"*"}}},
		}
	})
}
