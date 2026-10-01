// Run: cp verify/repro/c4_c10_closed_interceptor_invoked_test.go test/xds/verify_c4_c10_closed_interceptor_invoked_test.go && go test -race -count=1 -v -run '^Test$/^VerifyC4C10_' ./test/xds
//
// Audit repro for C4 and C10 (run v-a332a9ce). A live xDS-enabled gRPC server
// has two HTTP filters ("gate", "probe") ahead of the router. An RPC is paused
// inside the gate interceptor (i.e. after RouteAndProcess selected the old
// usable route configuration and before the probe interceptor is invoked), the
// RouteConfiguration is replaced in place through RDS, and the RPC is resumed.
// The test FAILS when the probe interceptor of the old configuration has
// finished Close() before the paused RPC invokes its AllowRPC().

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
	"google.golang.org/protobuf/types/known/structpb"
)

type vc4Cfg struct{ httpfilter.FilterConfig }

// vc4Builder builds one ServerFilter whose interceptors are either "gate"
// interceptors (block the armed RPC) or "probe" interceptors (record whether
// they are invoked after Close completed).
type vc4Builder struct {
	httpfilter.Builder
	t       *testing.T
	typeURL string
	gate    bool

	armed   atomic.Bool
	entered chan struct{}
	resume  chan struct{}

	mu    sync.Mutex
	icpts []*vc4Icpt
}

func (b *vc4Builder) IsTerminal() bool   { return false }
func (b *vc4Builder) TypeURLs() []string { return []string{b.typeURL} }
func (*vc4Builder) ParseFilterConfig(proto.Message) (httpfilter.FilterConfig, error) {
	return vc4Cfg{}, nil
}
func (*vc4Builder) ParseFilterConfigOverride(proto.Message) (httpfilter.FilterConfig, error) {
	return vc4Cfg{}, nil
}
func (b *vc4Builder) BuildServerFilter() httpfilter.ServerFilter { return b }
func (b *vc4Builder) Close()                                     {}
func (b *vc4Builder) BuildServerInterceptor(_, _ httpfilter.FilterConfig) (resolver.ServerInterceptor, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	i := &vc4Icpt{b: b, id: len(b.icpts) + 1}
	b.icpts = append(b.icpts, i)
	return i, nil
}

func (b *vc4Builder) snapshot() []*vc4Icpt {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]*vc4Icpt(nil), b.icpts...)
}

type vc4Icpt struct {
	b                 *vc4Builder
	id                int
	closed            atomic.Bool
	allowedAfterClose atomic.Int32
	allowed           atomic.Int32
}

func (i *vc4Icpt) AllowRPC(ctx context.Context) error {
	if i.b.gate {
		if i.b.armed.CompareAndSwap(true, false) {
			close(i.b.entered)
			select {
			case <-i.b.resume:
			case <-ctx.Done():
				return ctx.Err()
			}
		}
		return nil
	}
	i.allowed.Add(1)
	if i.closed.Load() {
		i.allowedAfterClose.Add(1)
		i.b.t.Logf("probe interceptor #%d: AllowRPC invoked AFTER its Close() completed", i.id)
	}
	return nil
}

func (i *vc4Icpt) Close() {
	i.closed.Store(true)
	if !i.b.gate {
		i.b.t.Logf("probe interceptor #%d: Close() completed", i.id)
	}
}

func vc4HTTPFilter(t *testing.T, name, typeURL string) *v3httppb.HttpFilter {
	return &v3httppb.HttpFilter{Name: name, ConfigType: &v3httppb.HttpFilter_TypedConfig{TypedConfig: testutils.MarshalAny(t, &v3xdsxdstypepb.TypedStruct{
		TypeUrl: typeURL, Value: &structpb.Struct{},
	})}}
}

func vc4Routes(name string, n int) *v3routepb.RouteConfiguration {
	var routes []*v3routepb.Route
	routes = append(routes, &v3routepb.Route{
		Match:  &v3routepb.RouteMatch{PathSpecifier: &v3routepb.RouteMatch_Prefix{Prefix: "/"}},
		Action: &v3routepb.Route_NonForwardingAction{},
	})
	for i := 1; i < n; i++ {
		routes = append(routes, &v3routepb.Route{
			Match:  &v3routepb.RouteMatch{PathSpecifier: &v3routepb.RouteMatch_Prefix{Prefix: fmt.Sprintf("/never-%d/", i)}},
			Action: &v3routepb.Route_NonForwardingAction{},
		})
	}
	return &v3routepb.RouteConfiguration{Name: name, VirtualHosts: []*v3routepb.VirtualHost{{Domains: []string{"*"}, Routes: routes}}}
}

func (s) TestVerifyC4C10_InPlaceRDSReplacement_ClosesInterceptorOfInFlightRPC(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	gate := &vc4Builder{t: t, typeURL: t.Name() + "/gate", gate: true, entered: make(chan struct{}), resume: make(chan struct{})}
	probe := &vc4Builder{t: t, typeURL: t.Name() + "/probe"}
	httpfilter.Register(gate)
	httpfilter.Register(probe)
	defer httpfilter.UnregisterForTesting(gate.typeURL)
	defer httpfilter.UnregisterForTesting(probe.typeURL)

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
	const serviceName, routeName = "my-service", "routeName"
	resources := e2e.DefaultClientResources(e2e.ResourceParams{DialTarget: serviceName, NodeID: nodeID, Host: host, Port: port, SecLevel: e2e.SecurityLevelNone})
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
						vc4HTTPFilter(t, "gate", gate.typeURL),
						vc4HTTPFilter(t, "probe", probe.typeURL),
						e2e.HTTPFilter("router", &v3routerpb.Router{}),
					},
					RouteSpecifier: &v3httppb.HttpConnectionManager_Rds{Rds: &v3httppb.Rds{
						ConfigSource:    &v3corepb.ConfigSource{ConfigSourceSpecifier: &v3corepb.ConfigSource_Ads{Ads: &v3corepb.AggregatedConfigSource{}}},
						RouteConfigName: routeName,
					}},
				})},
			}},
		}},
	})
	clientRoutes := resources.Routes
	resources.Routes = append(append([]*v3routepb.RouteConfiguration{}, clientRoutes...), vc4Routes(routeName, 1))
	if err := ms.Update(ctx, resources); err != nil {
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
	if _, err := client.EmptyCall(ctx, &testpb.Empty{}, grpc.WaitForReady(true)); err != nil {
		t.Fatalf("EmptyCall() failed: %v", err)
	}
	old := probe.snapshot()
	if len(old) != 1 {
		t.Fatalf("got %d probe interceptors for the initial configuration, want 1", len(old))
	}
	oldProbe := old[0]
	allowedBefore := oldProbe.allowed.Load()

	// Pause an RPC inside the gate interceptor of the old configuration.
	gate.armed.Store(true)
	rpcDone := make(chan error, 1)
	go func() {
		_, err := client.EmptyCall(ctx, &testpb.Empty{})
		rpcDone <- err
	}()
	select {
	case <-gate.entered:
		t.Logf("RPC paused inside the gate interceptor of the old configuration (probe interceptor #%d not yet invoked for it)", oldProbe.id)
	case <-ctx.Done():
		t.Fatal("timeout waiting for the RPC to enter the gate interceptor")
	}

	// Replace the route configuration in place through RDS (LDS unchanged).
	resources.Routes = append(append([]*v3routepb.RouteConfiguration{}, clientRoutes...), vc4Routes(routeName, 2))
	if err := ms.Update(ctx, resources); err != nil {
		t.Fatal(err)
	}
	// Wait (bounded) for the replacement to be applied: new probe interceptors exist.
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) && len(probe.snapshot()) < 3 {
		time.Sleep(10 * time.Millisecond)
	}
	t.Logf("replacement applied: %d probe interceptors built in total", len(probe.snapshot()))
	// Give a closure that waits for the in-flight RPC a chance to show itself.
	for time.Now().Before(deadline) && !oldProbe.closed.Load() {
		time.Sleep(10 * time.Millisecond)
	}
	closedWhilePaused := oldProbe.closed.Load()
	t.Logf("OWNERSHIP: old probe interceptor #%d closed while the RPC that selected its configuration is still paused: %v", oldProbe.id, closedWhilePaused)

	close(gate.resume)
	select {
	case err := <-rpcDone:
		t.Logf("paused RPC finished with err=%v", err)
	case <-ctx.Done():
		t.Fatal("timeout waiting for the paused RPC to finish")
	}
	invoked := oldProbe.allowed.Load() - allowedBefore
	afterClose := oldProbe.allowedAfterClose.Load()
	t.Logf("CONCURRENT INVOCATION: resumed RPC invoked old probe interceptor #%d %d time(s); %d of them after its Close() completed", oldProbe.id, invoked, afterClose)
	if closedWhilePaused {
		t.Errorf("selecting a configuration did not retain its interceptors: probe interceptor #%d was closed while an RPC using its configuration was in flight", oldProbe.id)
	}
	if afterClose > 0 {
		t.Errorf("already-closed interceptor #%d was invoked by the in-flight RPC %d time(s)", oldProbe.id, afterClose)
	}
}
