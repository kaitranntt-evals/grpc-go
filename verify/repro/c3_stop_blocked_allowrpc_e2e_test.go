// Run (on evalon/grpc-go-xd-e6df146d; runs unchanged on the audited branch as a control): cp verify/repro/c3_stop_blocked_allowrpc_e2e_test.go test/xds/zz_verify_c3_e2e_test.go && go test -race -count=1 -v -timeout 120s -run 'Test/VerifyC3' ./test/xds/
//
// Bounded end-to-end probe for C3: a real xDS-enabled gRPC server whose HTTP
// filter interceptor blocks in AllowRPC until the RPC context is cancelled.
// While one RPC is parked in AllowRPC, the server's Stop() is called. The probe
// waits a bounded time for Stop to return on its own, dumps the Stop and
// AllowRPC goroutines, and only then releases AllowRPC by cancelling the RPC
// from the client. It never fails on the outcome; read the "PROBE" lines.

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
)

// How long Stop() is given to return without any help.
const verifyC3StopBound = 5 * time.Second

type verifyC3Cfg struct{ httpfilter.FilterConfig }

type verifyC3Builder struct {
	httpfilter.Builder
	typeURL string

	block   chan struct{} // closed => subsequent AllowRPC calls block until ctx is done
	entered chan struct{} // closed when a blocking AllowRPC is entered
	once    sync.Once

	mu          sync.Mutex
	blockedCtx  context.Context
	allowExited time.Time
}

func (b *verifyC3Builder) IsTerminal() bool   { return false }
func (b *verifyC3Builder) TypeURLs() []string { return []string{b.typeURL} }
func (*verifyC3Builder) ParseFilterConfig(proto.Message) (httpfilter.FilterConfig, error) {
	return verifyC3Cfg{}, nil
}
func (*verifyC3Builder) ParseFilterConfigOverride(proto.Message) (httpfilter.FilterConfig, error) {
	return verifyC3Cfg{}, nil
}
func (b *verifyC3Builder) BuildServerFilter() httpfilter.ServerFilter { return b }
func (b *verifyC3Builder) Close()                                     {}
func (b *verifyC3Builder) BuildServerInterceptor(_, _ httpfilter.FilterConfig) (resolver.ServerInterceptor, error) {
	return &verifyC3Interceptor{b: b}, nil
}

type verifyC3Interceptor struct{ b *verifyC3Builder }

func (i *verifyC3Interceptor) Close() {}

// AllowRPC waits for RPC-context cancellation once blocking has been armed.
func (i *verifyC3Interceptor) AllowRPC(ctx context.Context) error {
	select {
	case <-i.b.block:
	default:
		return nil
	}
	i.b.mu.Lock()
	i.b.blockedCtx = ctx
	i.b.mu.Unlock()
	i.b.once.Do(func() { close(i.b.entered) })
	<-ctx.Done()
	i.b.mu.Lock()
	i.b.allowExited = time.Now()
	i.b.mu.Unlock()
	return ctx.Err()
}

// verifyC3Goroutines returns the function-name frames of every goroutine
// whose stack contains one of the given markers.
func verifyC3Goroutines(markers ...string) []string {
	buf := make([]byte, 4<<20)
	buf = buf[:runtime.Stack(buf, true)]
	var out []string
	for _, g := range strings.Split(string(buf), "\n\n") {
		match := false
		for _, m := range markers {
			if strings.Contains(g, m) {
				match = true
			}
		}
		if !match {
			continue
		}
		var frames []string
		for n, line := range strings.Split(g, "\n") {
			if n == 0 {
				frames = append(frames, line)
				continue
			}
			if strings.HasPrefix(line, "\t") || strings.HasPrefix(line, "created by") {
				continue
			}
			if p := strings.LastIndex(line, "("); p > 0 {
				line = line[:p]
			}
			frames = append(frames, "    "+line)
		}
		out = append(out, strings.Join(frames, "\n"))
	}
	return out
}

func (s) TestVerifyC3_StopWhileAllowRPCWaitsForContext(t *testing.T) {
	fb := &verifyC3Builder{typeURL: t.Name(), block: make(chan struct{}), entered: make(chan struct{})}
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

	host, port, err := hostPortFromListener(lis)
	if err != nil {
		t.Fatalf("failed to retrieve host and port of server: %v", err)
	}
	const serviceName = "my-service"
	const routeConfigName = "routeName"
	resources := e2e.DefaultClientResources(e2e.ResourceParams{
		DialTarget: serviceName, NodeID: nodeID, Host: host, Port: port, SecLevel: e2e.SecurityLevelNone,
	})
	resources.Listeners = append(resources.Listeners, &v3listenerpb.Listener{
		Name: fmt.Sprintf(e2e.ServerListenerResourceNameTemplate, net.JoinHostPort(host, strconv.Itoa(int(port)))),
		Address: &v3corepb.Address{Address: &v3corepb.Address_SocketAddress{SocketAddress: &v3corepb.SocketAddress{
			Address: host, PortSpecifier: &v3corepb.SocketAddress_PortValue{PortValue: port},
		}}},
		DefaultFilterChain: &v3listenerpb.FilterChain{
			Name: "default",
			Filters: []*v3listenerpb.Filter{{
				Name: "hcm",
				ConfigType: &v3listenerpb.Filter_TypedConfig{
					TypedConfig: testutils.MarshalAny(t, &v3httppb.HttpConnectionManager{
						HttpFilters: []*v3httppb.HttpFilter{
							{
								Name: "blocker",
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
	})
	resources.Routes = append(resources.Routes, &v3routepb.RouteConfiguration{
		Name: routeConfigName,
		VirtualHosts: []*v3routepb.VirtualHost{{
			Domains: []string{"*"},
			Routes: []*v3routepb.Route{{
				Match:  &v3routepb.RouteMatch{PathSpecifier: &v3routepb.RouteMatch_Prefix{Prefix: "/"}},
				Action: &v3routepb.Route_NonForwardingAction{},
			}},
		}},
	})

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
		t.Fatalf("warm-up EmptyCall() failed: %v", err)
	}
	t.Logf("PROBE warm-up RPC OK; arming AllowRPC to wait for RPC-context cancellation")

	// One RPC parks in AllowRPC. Its client context has no deadline: only the
	// probe's explicit cancel (or the server tearing the transport down) ends it.
	close(fb.block)
	rpcCtx, rpcCancel := context.WithCancel(context.Background())
	defer rpcCancel()
	rpcDone := make(chan error, 1)
	go func() {
		_, err := client.EmptyCall(rpcCtx, &testpb.Empty{})
		rpcDone <- err
	}()
	select {
	case <-fb.entered:
	case <-ctx.Done():
		t.Fatalf("Timeout waiting for the RPC to block in AllowRPC")
	}
	t.Logf("PROBE RPC is parked in AllowRPC (waiting on <-ctx.Done())")

	stopStart := time.Now()
	stopDone := make(chan struct{})
	go func() {
		stopServer() // (*grpc.Server).Stop on the xDS-enabled server
		close(stopDone)
	}()
	t.Logf("PROBE Stop() called; giving it %v to return with no help", verifyC3StopBound)

	select {
	case <-stopDone:
		t.Logf("PROBE RESULT Stop() returned on its own after %v (AllowRPC was not released by the probe)", time.Since(stopStart).Round(time.Millisecond))
		select {
		case err := <-rpcDone:
			t.Logf("PROBE parked RPC finished with: %v", err)
		case <-time.After(5 * time.Second):
			t.Logf("PROBE parked RPC still not finished 5s after Stop returned")
		}
		return
	case <-time.After(verifyC3StopBound):
	}

	fb.mu.Lock()
	bctx := fb.blockedCtx
	fb.mu.Unlock()
	t.Logf("PROBE RESULT Stop() has NOT returned %v after being called; server-side RPC ctx.Err()=%v (nil => transport cancellation has not happened)", verifyC3StopBound, bctx.Err())
	for _, g := range verifyC3Goroutines("grpc.(*Server).Stop", "verifyC3Interceptor).AllowRPC") {
		t.Logf("PROBE goroutine:\n%s", g)
	}

	t.Logf("PROBE now releasing AllowRPC manually by cancelling the RPC from the client")
	releaseAt := time.Now()
	rpcCancel()
	select {
	case <-stopDone:
		t.Logf("PROBE Stop() returned %v after the manual release (%v after it was called)", time.Since(releaseAt).Round(time.Millisecond), time.Since(stopStart).Round(time.Millisecond))
	case <-time.After(10 * time.Second):
		t.Logf("PROBE Stop() still blocked 10s after the manual release")
	}
}
