// Run: copy from verify/repro/testdata/ to test/xds/ on branch evalon/grpc-go-xd-e43b07e4, then `go test -race -count=1 -v -run '^Test$/^VerifyC1E2E_' ./test/xds 2>&1 | grep -E 'VERIFY-C1|^\s*--- |^(ok|FAIL|PASS)'`

package xds_test

import (
	"context"
	"fmt"
	"net"
	"strconv"
	"strings"
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
	testgrpc "google.golang.org/grpc/interop/grpc_testing"
	testpb "google.golang.org/grpc/interop/grpc_testing"
	"google.golang.org/grpc/xds"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/anypb"
	"google.golang.org/protobuf/types/known/structpb"

	v3xdsxdstypepb "github.com/cncf/xds/go/xds/type/v3"
	v3corepb "github.com/envoyproxy/go-control-plane/envoy/config/core/v3"
	v3listenerpb "github.com/envoyproxy/go-control-plane/envoy/config/listener/v3"
	v3routepb "github.com/envoyproxy/go-control-plane/envoy/config/route/v3"
	v3routerpb "github.com/envoyproxy/go-control-plane/envoy/extensions/filters/http/router/v3"
	v3httppb "github.com/envoyproxy/go-control-plane/envoy/extensions/filters/network/http_connection_manager/v3"
)

type verifyC1E2ELog struct {
	mu     sync.Mutex
	events []string
}

func (l *verifyC1E2ELog) add(e string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.events = append(l.events, e)
}

func (l *verifyC1E2ELog) snapshot() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]string(nil), l.events...)
}

func (l *verifyC1E2ELog) has(prefix string) bool {
	for _, e := range l.snapshot() {
		if strings.HasPrefix(e, prefix) {
			return true
		}
	}
	return false
}

type verifyC1E2ECfg struct{ httpfilter.FilterConfig }

type verifyC1E2EBuilder struct {
	typeURL string
	log     *verifyC1E2ELog
	nf, ni  atomic.Int32
	// allow is invoked from AllowRPC with the interceptor's id.
	allow func(ctx context.Context, id string) error
}

func (b *verifyC1E2EBuilder) IsTerminal() bool   { return false }
func (b *verifyC1E2EBuilder) TypeURLs() []string { return []string{b.typeURL} }
func (*verifyC1E2EBuilder) ParseFilterConfig(proto.Message) (httpfilter.FilterConfig, error) {
	return verifyC1E2ECfg{}, nil
}
func (*verifyC1E2EBuilder) ParseFilterConfigOverride(proto.Message) (httpfilter.FilterConfig, error) {
	return verifyC1E2ECfg{}, nil
}
func (b *verifyC1E2EBuilder) BuildServerFilter() httpfilter.ServerFilter {
	return &verifyC1E2EFilter{b: b, id: fmt.Sprintf("F%d", b.nf.Add(1))}
}

type verifyC1E2EFilter struct {
	b      *verifyC1E2EBuilder
	id     string
	closed atomic.Bool
}

func (f *verifyC1E2EFilter) BuildServerInterceptor(_, _ httpfilter.FilterConfig) (resolver.ServerInterceptor, error) {
	i := &verifyC1E2EInterceptor{f: f, id: fmt.Sprintf("%s/I%d", f.id, f.b.ni.Add(1))}
	f.b.log.add("icpt-built " + i.id)
	return i, nil
}

func (f *verifyC1E2EFilter) Close() {
	f.closed.Store(true)
	f.b.log.add("filter-close " + f.id)
}

type verifyC1E2EInterceptor struct {
	f  *verifyC1E2EFilter
	id string
}

func (i *verifyC1E2EInterceptor) AllowRPC(ctx context.Context) error {
	if i.f.b.allow == nil {
		return nil
	}
	return i.f.b.allow(ctx, i.id)
}

func (i *verifyC1E2EInterceptor) Close() {
	i.f.b.log.add(fmt.Sprintf("icpt-close %s (parent-filter-already-closed=%v)", i.id, i.f.closed.Load()))
}

func verifyC1E2EOrderViolation(events []string) (string, bool) {
	filterClosed := map[string]bool{}
	for _, e := range events {
		fields := strings.Fields(e)
		switch fields[0] {
		case "filter-close":
			filterClosed[fields[1]] = true
		case "icpt-close":
			parent := strings.SplitN(fields[1], "/", 2)[0]
			if filterClosed[parent] {
				return fmt.Sprintf("interceptor %s closed after its parent filter %s", fields[1], parent), true
			}
		}
	}
	return "", false
}

const (
	verifyC1E2ERouteName  = "verify-c1-server-route"
	verifyC1E2EFilterName = "verify-c1-trace"
)

type verifyC1E2EEnv struct {
	ctx    context.Context
	client testgrpc.TestServiceClient
	stop   func()
	// updateRoute replaces the server-side RouteConfiguration resource.
	updateRoute func(*v3routepb.RouteConfiguration)
}

// verifyC1E2ESetup starts an xDS-enabled gRPC server whose only filter chain
// fetches its routes via RDS and carries the tracing HTTP filter, and returns
// once the server is SERVING.
func verifyC1E2ESetup(t *testing.T, fb *verifyC1E2EBuilder, initial *v3routepb.RouteConfiguration) *verifyC1E2EEnv {
	httpfilter.Register(fb)
	t.Cleanup(func() { httpfilter.UnregisterForTesting(fb.typeURL) })

	managementServer, nodeID, bootstrapContents, xdsResolver := setup.ManagementServerAndResolver(t)
	servingCh := make(chan struct{})
	var once sync.Once
	lis, stopServer := setupGRPCServer(t, bootstrapContents, xds.ServingModeCallback(func(_ net.Addr, args xds.ServingModeChangeArgs) {
		if args.Mode == connectivity.ServingModeServing {
			once.Do(func() { close(servingCh) })
		}
	}))
	var stopOnce sync.Once
	stop := func() { stopOnce.Do(stopServer) }
	t.Cleanup(stop)

	host, port, err := hostPortFromListener(lis)
	if err != nil {
		t.Fatal(err)
	}
	const serviceName = "my-service"
	resources := e2e.DefaultClientResources(e2e.ResourceParams{
		DialTarget: serviceName,
		NodeID:     nodeID,
		Host:       host,
		Port:       port,
		SecLevel:   e2e.SecurityLevelNone,
	})
	clientRoute := resources.Routes[0]
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
						{Name: verifyC1E2EFilterName, ConfigType: &v3httppb.HttpFilter_TypedConfig{TypedConfig: testutils.MarshalAny(t, &v3xdsxdstypepb.TypedStruct{TypeUrl: fb.typeURL, Value: &structpb.Struct{}})}},
						e2e.HTTPFilter("router", &v3routerpb.Router{}),
					},
					RouteSpecifier: &v3httppb.HttpConnectionManager_Rds{Rds: &v3httppb.Rds{
						ConfigSource:    &v3corepb.ConfigSource{ConfigSourceSpecifier: &v3corepb.ConfigSource_Ads{Ads: &v3corepb.AggregatedConfigSource{}}},
						RouteConfigName: verifyC1E2ERouteName,
					}},
				})},
			}},
		}},
	})
	resources.Routes = []*v3routepb.RouteConfiguration{clientRoute, initial}

	ctx, cancel := context.WithTimeout(context.Background(), defaultTestTimeout)
	t.Cleanup(cancel)
	if err := managementServer.Update(ctx, resources); err != nil {
		t.Fatal(err)
	}
	cc, err := grpc.NewClient(fmt.Sprintf("xds:///%s", serviceName), grpc.WithTransportCredentials(insecure.NewCredentials()), grpc.WithResolvers(xdsResolver))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cc.Close() })
	select {
	case <-servingCh:
	case <-ctx.Done():
		t.Fatal("timeout waiting for SERVING mode")
	}
	return &verifyC1E2EEnv{
		ctx:    ctx,
		client: testgrpc.NewTestServiceClient(cc),
		stop:   stop,
		updateRoute: func(rc *v3routepb.RouteConfiguration) {
			resources.Routes = []*v3routepb.RouteConfiguration{clientRoute, rc}
			if err := managementServer.Update(ctx, resources); err != nil {
				t.Fatal(err)
			}
		},
	}
}

func verifyC1E2ERoute(domains []string, perFilter map[string]*anypb.Any) *v3routepb.RouteConfiguration {
	return &v3routepb.RouteConfiguration{
		Name: verifyC1E2ERouteName,
		VirtualHosts: []*v3routepb.VirtualHost{{
			Domains: domains,
			Routes: []*v3routepb.Route{{
				Match:  &v3routepb.RouteMatch{PathSpecifier: &v3routepb.RouteMatch_Prefix{Prefix: "/"}},
				Action: &v3routepb.Route_NonForwardingAction{},
			}},
			TypedPerFilterConfig: perFilter,
		}},
	}
}

// A RouteConfiguration update, delivered by the management server, disables the
// tracing filter on every route of a route configuration the server is already
// serving. The retired route configuration's interceptor then has no successor
// holding a reference on the parent filter.
func (s) TestVerifyC1E2E_RDSUpdateDisablingFilter_CloseOrder(t *testing.T) {
	// Per-route "disabled" overrides are only honoured when this experimental
	// env var (GRPC_EXPERIMENTAL_XDS_EXT_PROC_ON_CLIENT) is on.
	testutils.SetEnvConfig(t, &envconfig.XDSClientExtProcEnabled, true)
	verifyC1E2ERunDisablingUpdate(t, map[string]*anypb.Any{
		verifyC1E2EFilterName: testutils.MarshalAny(t, &v3routepb.FilterConfig{Disabled: true}),
	})
}

func verifyC1E2ERunDisablingUpdate(t *testing.T, perFilter map[string]*anypb.Any) {
	log := &verifyC1E2ELog{}
	fb := &verifyC1E2EBuilder{typeURL: t.Name(), log: log}
	e := verifyC1E2ESetup(t, fb, verifyC1E2ERoute([]string{"*"}, nil))

	if _, err := e.client.EmptyCall(e.ctx, &testpb.Empty{}, grpc.WaitForReady(true)); err != nil {
		t.Fatalf("EmptyCall on initial route configuration failed: %v", err)
	}
	t.Logf("VERIFY-C1 events after initial RPC: %q", log.snapshot())

	e.updateRoute(verifyC1E2ERoute([]string{"*", "gen2.example.com"}, perFilter))
	for !log.has("icpt-close") && e.ctx.Err() == nil {
		time.Sleep(10 * time.Millisecond)
	}
	if _, err := e.client.EmptyCall(e.ctx, &testpb.Empty{}, grpc.WaitForReady(true)); err != nil {
		t.Fatalf("EmptyCall after RDS update failed: %v", err)
	}
	t.Logf("VERIFY-C1 events after RDS update disabling the filter (server still running): %q", log.snapshot())
	e.stop()

	events := log.snapshot()
	for i, ev := range events {
		t.Logf("VERIFY-C1 event #%d: %s", i, ev)
	}
	if msg, bad := verifyC1E2EOrderViolation(events); bad {
		t.Errorf("VERIFY-C1 ORDER VIOLATION on RDS update: %s", msg)
	}
}

// An RPC is parked inside AllowRPC of the interceptor of route configuration
// generation 1 until its context is cancelled. Generation 1 is then retired by
// an RDS update, and the server is stopped while that RPC is still parked.
func (s) TestVerifyC1E2E_ShutdownWithCancellationHeldRetiredReader_CloseOrder(t *testing.T) {
	log := &verifyC1E2ELog{}
	entered := make(chan string, 4)
	fb := &verifyC1E2EBuilder{typeURL: t.Name(), log: log}
	fb.allow = func(ctx context.Context, id string) error {
		log.add("allowrpc-enter " + id)
		entered <- id
		<-ctx.Done() // Held until the RPC is cancelled.
		log.add("allowrpc-return " + id)
		return ctx.Err()
	}
	e := verifyC1E2ESetup(t, fb, verifyC1E2ERoute([]string{"*"}, nil))

	rpcCtx, cancelRPC := context.WithTimeout(context.Background(), defaultTestTimeout)
	defer cancelRPC()
	rpcDone := make(chan error, 1)
	go func() {
		_, err := e.client.EmptyCall(rpcCtx, &testpb.Empty{}, grpc.WaitForReady(true))
		rpcDone <- err
	}()
	var heldID string
	select {
	case heldID = <-entered:
	case <-e.ctx.Done():
		t.Fatal("timeout waiting for the RPC to be parked in AllowRPC")
	}
	t.Logf("VERIFY-C1 RPC parked in AllowRPC of interceptor %s", heldID)

	// Retire generation 1.
	e.updateRoute(verifyC1E2ERoute([]string{"*", "gen2.example.com"}, nil))
	for !log.has("icpt-built F1/I2") && e.ctx.Err() == nil {
		time.Sleep(10 * time.Millisecond)
	}
	// Give the server a bounded window to close (or not) the retired
	// interceptor while its reader is still parked.
	deadline := time.Now().Add(time.Second)
	for !log.has("icpt-close "+heldID) && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	t.Logf("VERIFY-C1 before Stop, reader still parked: retired interceptor %s already closed=%v, parent filter closed=%v", heldID, log.has("icpt-close "+heldID), log.has("filter-close"))
	t.Logf("VERIFY-C1 events before Stop: %q", log.snapshot())

	log.add("server-stop-begin")
	stopDone := make(chan struct{})
	go func() { e.stop(); close(stopDone) }()
	select {
	case <-stopDone:
	case <-time.After(5 * time.Second):
		cancelRPC()
		t.Fatal("Stop() did not return within 5s")
	}
	log.add("server-stop-end")
	select {
	case <-rpcDone:
	case <-time.After(5 * time.Second):
		t.Fatal("parked RPC did not finish after Stop()")
	}
	for !log.has("allowrpc-return") && e.ctx.Err() == nil {
		time.Sleep(10 * time.Millisecond)
	}

	events := log.snapshot()
	for i, ev := range events {
		t.Logf("VERIFY-C1 event #%d: %s", i, ev)
	}
	if msg, bad := verifyC1E2EOrderViolation(events); bad {
		t.Errorf("VERIFY-C1 ORDER VIOLATION on shutdown: %s", msg)
	} else {
		t.Logf("VERIFY-C1 no interceptor was closed after its parent filter during shutdown")
	}
}
