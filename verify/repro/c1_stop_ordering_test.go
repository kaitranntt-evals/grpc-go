//go:build verifyc1

// Run (C1 repro): on a checkout of branch evalon/grpc-go-xd-dd46dda9 of kaitranntt-evals/grpc-go-xds-rds-interceptor-lifecycle-leak, `cp verify/repro/c1_stop_ordering_test.go test/xds/verify_c1_stop_ordering_test.go && go test -race -tags verifyc1 -count=1 -v -run '^Test$/^VerifyC1_' ./test/xds` (see verify/repro/c1_stress.sh for the stress runs).
//
// This file adds no production code. It reuses the solution's own test fixtures
// (liveInterceptorFilterBuilder, routeConfigWithFilterOverride) from
// test/xds/xds_server_route_config_update_test.go, so it only compiles on the
// audited solution branch.
//
//   - VerifyC1_InFlightHandlerAtStop_Default: deterministic. Holds one server
//     handler inside AllowRPC (i.e. while RouteAndProcess holds its
//     usableRouteConfiguration reference), then performs the solution test's
//     final sequence: cancel client worker -> join worker -> Stop() -> read
//     the live-interceptor count. FAILS (problem reproduced) when the count
//     is non-zero right after Stop() returns.
//   - VerifyC1_InFlightHandlerAtStop_WaitForHandlers: same, but the server is
//     built with grpc.WaitForHandlers(true). PASSES: Stop() only returns once
//     the handler has released its reference.
//   - VerifyC1_SolutionLifecycle_Diagnostics: the body of the solution's
//     TestServerSideXDS_RouteConfigurationUpdates_ReleaseSupersededResources,
//     unchanged except that a non-zero post-Stop count is polled to report
//     how long after Stop() the deferred cleanup completed. Meant for stress.
//   - VerifyC1_SolutionLifecycle_WaitForHandlers: same body, server built with
//     grpc.WaitForHandlers(true). Control for the stress runs.

package xds_test

import (
	"context"
	"fmt"
	"net"
	"runtime"
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
	testgrpc "google.golang.org/grpc/interop/grpc_testing"
	testpb "google.golang.org/grpc/interop/grpc_testing"
	"google.golang.org/grpc/xds"
	"google.golang.org/protobuf/proto"

	v3corepb "github.com/envoyproxy/go-control-plane/envoy/config/core/v3"
	v3listenerpb "github.com/envoyproxy/go-control-plane/envoy/config/listener/v3"
	v3routerpb "github.com/envoyproxy/go-control-plane/envoy/extensions/filters/http/router/v3"
	v3httppb "github.com/envoyproxy/go-control-plane/envoy/extensions/filters/network/http_connection_manager/v3"
)

// verifyC1GatedBuilder is a counting server filter whose interceptor can park
// exactly one RPC inside AllowRPC until the gate is opened.
type verifyC1GatedBuilder struct {
	httpfilter.Builder
	typeURL          string
	liveFilters      atomic.Int32
	liveInterceptors atomic.Int32
	armed            atomic.Bool
	entered          chan struct{}
	gate             chan struct{}
}

func (*verifyC1GatedBuilder) IsTerminal() bool     { return false }
func (b *verifyC1GatedBuilder) TypeURLs() []string { return []string{b.typeURL} }
func (*verifyC1GatedBuilder) ParseFilterConfig(cfg proto.Message) (httpfilter.FilterConfig, error) {
	return filterConfigFromProto(cfg)
}
func (*verifyC1GatedBuilder) ParseFilterConfigOverride(cfg proto.Message) (httpfilter.FilterConfig, error) {
	return filterConfigFromProto(cfg)
}
func (b *verifyC1GatedBuilder) BuildServerFilter() httpfilter.ServerFilter {
	b.liveFilters.Add(1)
	return &verifyC1GatedFilter{parent: b}
}

type verifyC1GatedFilter struct{ parent *verifyC1GatedBuilder }

func (f *verifyC1GatedFilter) Close() { f.parent.liveFilters.Add(-1) }
func (f *verifyC1GatedFilter) BuildServerInterceptor(_, _ httpfilter.FilterConfig) (resolver.ServerInterceptor, error) {
	f.parent.liveInterceptors.Add(1)
	return &verifyC1GatedInterceptor{parent: f.parent}, nil
}

type verifyC1GatedInterceptor struct {
	parent *verifyC1GatedBuilder
	closed atomic.Bool
}

func (i *verifyC1GatedInterceptor) AllowRPC(context.Context) error {
	if i.parent.armed.CompareAndSwap(true, false) {
		close(i.parent.entered)
		<-i.parent.gate
	}
	return nil
}

func (i *verifyC1GatedInterceptor) Close() {
	if !i.closed.Swap(true) {
		i.parent.liveInterceptors.Add(-1)
	}
}

// verifyC1ServerListener mirrors the listener built by the solution's test: all
// filter chains use RDS and contain the given HTTP filter.
func verifyC1ServerListener(t *testing.T, host string, port uint32, routeName, filterName, typeURL string) *v3listenerpb.Listener {
	inboundLis := e2e.DefaultServerListenerWithRouteConfigName(host, port, e2e.SecurityLevelNone, routeName)
	hcm := testutils.MarshalAny(t, &v3httppb.HttpConnectionManager{
		HttpFilters: []*v3httppb.HttpFilter{
			newHTTPFilter(t, filterName, typeURL, "default-path"),
			e2e.HTTPFilter("router", &v3routerpb.Router{}),
		},
		RouteSpecifier: &v3httppb.HttpConnectionManager_Rds{
			Rds: &v3httppb.Rds{
				ConfigSource: &v3corepb.ConfigSource{
					ConfigSourceSpecifier: &v3corepb.ConfigSource_Ads{Ads: &v3corepb.AggregatedConfigSource{}},
				},
				RouteConfigName: routeName,
			},
		},
	})
	setHCM := func(fc *v3listenerpb.FilterChain) {
		if fc == nil {
			return
		}
		fc.Filters = []*v3listenerpb.Filter{{
			Name:       "hcm",
			ConfigType: &v3listenerpb.Filter_TypedConfig{TypedConfig: hcm},
		}}
	}
	for _, fc := range inboundLis.GetFilterChains() {
		setHCM(fc)
	}
	setHCM(inboundLis.GetDefaultFilterChain())
	return inboundLis
}

func verifyC1InFlightHandlerAtStop(t *testing.T, waitForHandlers bool) {
	const (
		filterName = "tracker"
		routeName  = "routeName"
	)
	fb := &verifyC1GatedBuilder{typeURL: t.Name(), entered: make(chan struct{}), gate: make(chan struct{})}
	httpfilter.Register(fb)
	defer httpfilter.UnregisterForTesting(fb.typeURL)
	var gateOnce sync.Once
	openGate := func() { gateOnce.Do(func() { close(fb.gate) }) }
	defer openGate()

	managementServer, nodeID, bootstrapContents, xdsResolver := setup.ManagementServerAndResolver(t)

	servingCh := make(chan struct{})
	var servingOnce sync.Once
	opts := []grpc.ServerOption{xds.ServingModeCallback(func(_ net.Addr, args xds.ServingModeChangeArgs) {
		if args.Mode == connectivity.ServingModeServing {
			servingOnce.Do(func() { close(servingCh) })
		}
	})}
	if waitForHandlers {
		opts = append(opts, grpc.WaitForHandlers(true))
	}
	lis, stopServer := setupGRPCServer(t, bootstrapContents, opts...)
	var stopOnce sync.Once
	defer stopOnce.Do(stopServer)

	host, port, err := hostPortFromListener(lis)
	if err != nil {
		t.Fatalf("Failed to retrieve host and port of server: %v", err)
	}
	const serviceName = "my-service"
	resources := e2e.DefaultClientResources(e2e.ResourceParams{
		DialTarget: serviceName,
		NodeID:     nodeID,
		Host:       host,
		Port:       port,
		SecLevel:   e2e.SecurityLevelNone,
	})
	resources.Listeners = append(resources.Listeners, verifyC1ServerListener(t, host, port, routeName, filterName, fb.typeURL))
	resources.Routes = append(resources.Routes, routeConfigWithFilterOverride(t, routeName, filterName, fb.typeURL, "path-0"))

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
		t.Fatal("Timeout waiting for server to enter SERVING mode")
	}
	client := testgrpc.NewTestServiceClient(cc)
	if _, err := client.EmptyCall(ctx, &testpb.Empty{}, grpc.WaitForReady(true)); err != nil {
		t.Fatalf("EmptyCall() failed: %v", err)
	}
	baseline := fb.liveInterceptors.Load()
	t.Logf("verify: live interceptors while serving (baseline) = %d, live filters = %d", baseline, fb.liveFilters.Load())

	// Same shape as the solution test's worker: one client goroutine making an
	// RPC under a cancellable context, joined through a WaitGroup.
	rpcCtx, rpcCancel := context.WithCancel(ctx)
	var wg sync.WaitGroup
	wg.Add(1)
	fb.armed.Store(true)
	go func() {
		defer wg.Done()
		client.EmptyCall(rpcCtx, &testpb.Empty{})
	}()
	select {
	case <-fb.entered:
	case <-ctx.Done():
		t.Fatal("Timeout waiting for the server handler to enter AllowRPC")
	}

	// The solution test's final sequence: cancel worker, join worker, Stop,
	// read counters.
	rpcCancel()
	wg.Wait()
	t.Logf("verify: client worker cancelled and joined; server handler is still inside RouteAndProcess/AllowRPC; live interceptors = %d", fb.liveInterceptors.Load())

	if waitForHandlers {
		// Stop() blocks on the handler; let it go after a visible delay.
		time.AfterFunc(300*time.Millisecond, openGate)
	}
	stopStart := time.Now()
	stopOnce.Do(stopServer)
	stopTook := time.Since(stopStart)
	gotInterceptors, gotFilters := fb.liveInterceptors.Load(), fb.liveFilters.Load()
	t.Logf("verify: Stop() returned after %v (WaitForHandlers=%v); immediately after Stop(): live interceptors = %d, live filters = %d", stopTook, waitForHandlers, gotInterceptors, gotFilters)

	// Let the parked handler finish and observe the deferred cleanup.
	openGate()
	released := time.Now()
	settled := false
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(50 * time.Microsecond) {
		if fb.liveInterceptors.Load() == 0 && fb.liveFilters.Load() == 0 {
			settled = true
			break
		}
	}
	t.Logf("verify: after releasing the parked handler: live interceptors = %d, live filters = %d (settled=%v, %v after the handler was released)", fb.liveInterceptors.Load(), fb.liveFilters.Load(), settled, time.Since(released))
	if !settled {
		t.Errorf("verify: counters never reached zero: this would be a real leak, not an ordering problem")
	}
	if gotInterceptors != 0 {
		t.Errorf("%d live interceptors after server stop, want 0", gotInterceptors)
	}
	if gotFilters != 0 {
		t.Errorf("%d live filter instances after server stop, want 0", gotFilters)
	}
}

func (s) TestVerifyC1_InFlightHandlerAtStop_Default(t *testing.T) {
	verifyC1InFlightHandlerAtStop(t, false)
}

func (s) TestVerifyC1_InFlightHandlerAtStop_WaitForHandlers(t *testing.T) {
	verifyC1InFlightHandlerAtStop(t, true)
}

// verifyC1SolutionLifecycle is the body of the solution's
// TestServerSideXDS_RouteConfigurationUpdates_ReleaseSupersededResources. The
// only differences are the extra server options and the diagnostics around the
// final assertions.
func verifyC1SolutionLifecycle(t *testing.T, extraOpts ...grpc.ServerOption) {
	const (
		filterName = "tracker"
		routeName  = "routeName"
		numUpdates = 20
	)
	fb := &liveInterceptorFilterBuilder{typeURL: t.Name()}
	httpfilter.Register(fb)
	defer httpfilter.UnregisterForTesting(fb.typeURL)

	managementServer, nodeID, bootstrapContents, xdsResolver := setup.ManagementServerAndResolver(t)

	servingCh := make(chan struct{})
	var servingOnce sync.Once
	opt := xds.ServingModeCallback(func(_ net.Addr, args xds.ServingModeChangeArgs) {
		if args.Mode == connectivity.ServingModeServing {
			servingOnce.Do(func() { close(servingCh) })
		}
	})
	lis, stopServer := setupGRPCServer(t, bootstrapContents, append([]grpc.ServerOption{opt}, extraOpts...)...)
	defer stopServer()

	host, port, err := hostPortFromListener(lis)
	if err != nil {
		t.Fatalf("Failed to retrieve host and port of server: %v", err)
	}
	const serviceName = "my-service"
	resources := e2e.DefaultClientResources(e2e.ResourceParams{
		DialTarget: serviceName,
		NodeID:     nodeID,
		Host:       host,
		Port:       port,
		SecLevel:   e2e.SecurityLevelNone,
	})
	resources.Listeners = append(resources.Listeners, verifyC1ServerListener(t, host, port, routeName, filterName, fb.typeURL))
	serverRouteIdx := len(resources.Routes)
	resources.Routes = append(resources.Routes, routeConfigWithFilterOverride(t, routeName, filterName, fb.typeURL, "path-0"))

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
		t.Fatal("Timeout waiting for server to enter SERVING mode")
	}

	client := testgrpc.NewTestServiceClient(cc)
	waitForPath := func(want string) {
		t.Helper()
		for ; ctx.Err() == nil; <-time.After(defaultTestShortTimeout) {
			if _, err := client.EmptyCall(ctx, &testpb.Empty{}); err != nil {
				t.Fatalf("EmptyCall() failed: %v", err)
			}
			if got, _ := fb.lastPath.Load().(string); got == want {
				return
			}
		}
		t.Fatalf("Timeout waiting for route configuration with path %q to be applied", want)
	}
	waitForLiveInterceptors := func(want int32) {
		t.Helper()
		for ; ctx.Err() == nil; <-time.After(defaultTestShortTimeout) {
			if fb.liveInterceptors.Load() == want {
				return
			}
		}
		t.Fatalf("Timeout waiting for live interceptors to be %d, got %d", want, fb.liveInterceptors.Load())
	}

	waitForPath("path-0")
	baseline := fb.liveInterceptors.Load()
	if baseline == 0 {
		t.Fatal("No live interceptors after the server started serving")
	}

	rpcCtx, rpcCancel := context.WithCancel(ctx)
	rpcErrCh := make(chan error, 1)
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for rpcCtx.Err() == nil {
			if _, err := client.EmptyCall(rpcCtx, &testpb.Empty{}); err != nil && rpcCtx.Err() == nil {
				rpcErrCh <- err
				return
			}
		}
	}()

	for i := 1; i <= numUpdates; i++ {
		path := fmt.Sprintf("path-%d", i)
		resources.Routes[serverRouteIdx] = routeConfigWithFilterOverride(t, routeName, filterName, fb.typeURL, path)
		if err := managementServer.Update(ctx, resources); err != nil {
			t.Fatal(err)
		}
		waitForPath(path)
		waitForLiveInterceptors(baseline)
	}
	rpcCancel()
	wg.Wait()
	select {
	case err := <-rpcErrCh:
		t.Fatalf("EmptyCall() failed during route configuration updates: %v", err)
	default:
	}

	if got := fb.liveFilters.Load(); got != 1 {
		t.Fatalf("%d live filter instances after route configuration updates, want 1", got)
	}

	// Stopping the server must release all remaining resources.
	stopServer()
	stopReturned := time.Now()
	gotInterceptors, gotFilters := fb.liveInterceptors.Load(), fb.liveFilters.Load()
	if gotInterceptors != 0 || gotFilters != 0 {
		// verify diagnostics: is this a leak, or cleanup that had not finished?
		settledAfter := time.Duration(-1)
		for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); runtime.Gosched() {
			if fb.liveInterceptors.Load() == 0 && fb.liveFilters.Load() == 0 {
				settledAfter = time.Since(stopReturned)
				break
			}
		}
		t.Logf("VERIFY-DIAG baseline=%d; immediately after Stop(): liveInterceptors=%d liveFilters=%d; both reached 0 %v after Stop() returned (-1ns = never within 5s)", baseline, gotInterceptors, gotFilters, settledAfter)
	}
	if gotInterceptors != 0 {
		t.Fatalf("%d live interceptors after server stop, want 0", gotInterceptors)
	}
	if gotFilters != 0 {
		t.Fatalf("%d live filter instances after server stop, want 0", gotFilters)
	}
}

func (s) TestVerifyC1_SolutionLifecycle_Diagnostics(t *testing.T) {
	verifyC1SolutionLifecycle(t)
}

func (s) TestVerifyC1_SolutionLifecycle_WaitForHandlers(t *testing.T) {
	verifyC1SolutionLifecycle(t, grpc.WaitForHandlers(true))
}
