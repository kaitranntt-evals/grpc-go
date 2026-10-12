// Expected on the audited branch: test FAILS with "ORDER: serving notification was emitted before ... route interceptors".
// Run: cp verify/repro/c2_serving_callback_before_interceptors_test.go test/xds/ && go test -race -v -count=1 -run '^Test$/^VerifyC2_' ./test/xds   (on branch evalon/grpc-go-xd-576de4cd)

package xds_test

import (
	"context"
	"fmt"
	"net"
	"runtime"
	"strconv"
	"strings"
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

type vc2Builder struct {
	typeURL string
	onBuild func()
}

func (b *vc2Builder) IsTerminal() bool   { return false }
func (b *vc2Builder) TypeURLs() []string { return []string{b.typeURL} }
func (*vc2Builder) ParseFilterConfig(cfg proto.Message) (httpfilter.FilterConfig, error) {
	return vc2Parse(cfg)
}
func (*vc2Builder) ParseFilterConfigOverride(cfg proto.Message) (httpfilter.FilterConfig, error) {
	return vc2Parse(cfg)
}
func (b *vc2Builder) BuildServerFilter() httpfilter.ServerFilter { return b }
func (b *vc2Builder) Close()                                     {}
func (b *vc2Builder) BuildServerInterceptor(config, override httpfilter.FilterConfig) (resolver.ServerInterceptor, error) {
	cfg := config
	if override != nil {
		cfg = override
	}
	b.onBuild()
	return &vc2Interceptor{b: b, path: cfg.(vc2Cfg).path}, nil
}

type vc2Interceptor struct {
	b    *vc2Builder
	path string
}

func (i *vc2Interceptor) AllowRPC(context.Context) error { return nil }
func (i *vc2Interceptor) Close()                         {}

func vc2TypedStruct(t *testing.T, typeURL, path string) *anypb.Any {
	return testutils.MarshalAny(t, &v3xdsxdstypepb.TypedStruct{
		TypeUrl: typeURL,
		Value: &structpb.Struct{Fields: map[string]*structpb.Value{
			"path": {Kind: &structpb.Value_StringValue{StringValue: path}},
		}},
	})
}

func vc2RouteConfig(t *testing.T, typeURL, overridePath string) *v3routepb.RouteConfiguration {
	r := &v3routepb.Route{
		Match:  &v3routepb.RouteMatch{PathSpecifier: &v3routepb.RouteMatch_Prefix{Prefix: "/"}},
		Action: &v3routepb.Route_NonForwardingAction{},
	}
	if overridePath != "" {
		r.TypedPerFilterConfig = map[string]*anypb.Any{"vc2-filter": vc2TypedStruct(t, typeURL, overridePath)}
	}
	return &v3routepb.RouteConfiguration{
		Name:         "vc2RouteName",
		VirtualHosts: []*v3routepb.VirtualHost{{Domains: []string{"*"}, Routes: []*v3routepb.Route{r}}},
	}
}

func vc2ServerListener(t *testing.T, host string, port uint32, typeURL string) *v3listenerpb.Listener {
	return &v3listenerpb.Listener{
		Name: fmt.Sprintf(e2e.ServerListenerResourceNameTemplate, net.JoinHostPort(host, strconv.Itoa(int(port)))),
		Address: &v3corepb.Address{Address: &v3corepb.Address_SocketAddress{SocketAddress: &v3corepb.SocketAddress{
			Address:       host,
			PortSpecifier: &v3corepb.SocketAddress_PortValue{PortValue: port},
		}}},
		FilterChains: []*v3listenerpb.FilterChain{{
			Name: "catch-all",
			Filters: []*v3listenerpb.Filter{{
				Name: "hcm",
				ConfigType: &v3listenerpb.Filter_TypedConfig{TypedConfig: testutils.MarshalAny(t, &v3httppb.HttpConnectionManager{
					HttpFilters: []*v3httppb.HttpFilter{
						{Name: "vc2-filter", ConfigType: &v3httppb.HttpFilter_TypedConfig{TypedConfig: vc2TypedStruct(t, typeURL, "path1")}},
						e2e.HTTPFilter("router", &v3routerpb.Router{}),
					},
					RouteSpecifier: &v3httppb.HttpConnectionManager_Rds{Rds: &v3httppb.Rds{
						ConfigSource:    &v3corepb.ConfigSource{ConfigSourceSpecifier: &v3corepb.ConfigSource_Ads{Ads: &v3corepb.AggregatedConfigSource{}}},
						RouteConfigName: "vc2RouteName",
					}},
				})},
			}},
		}},
	}
}

func vc2Frames(skipPrefix string) string {
	buf := make([]byte, 16<<10)
	buf = buf[:runtime.Stack(buf, false)]
	var frames []string
	for _, l := range strings.Split(string(buf), "\n") {
		if strings.HasPrefix(l, "google.golang.org/grpc/internal/xds/server.") || strings.HasPrefix(l, "google.golang.org/grpc/xds.") {
			frames = append(frames, l[:strings.LastIndex(l, "(")])
		}
	}
	return strings.Join(frames, "\n  ")
}

// TestVerifyC2_ServingCallbackBeforeInterceptorConstruction holds the SERVING
// mode callback open and observes whether the route interceptors of the
// listener update that triggered it have been constructed yet.
func (s) TestVerifyC2_ServingCallbackBeforeInterceptorConstruction(t *testing.T) {
	var mu sync.Mutex
	var events []string
	record := func(e string) { mu.Lock(); events = append(events, e); mu.Unlock() }

	var built atomic.Int32
	var buildStack atomic.Value
	fb := &vc2Builder{typeURL: t.Name(), onBuild: func() {
		if built.Add(1) == 1 {
			buildStack.Store(vc2Frames(""))
		}
		record("BuildServerInterceptor")
	}}
	httpfilter.Register(fb)
	t.Cleanup(func() { httpfilter.UnregisterForTesting(fb.typeURL) })

	servingEntered := make(chan struct{})
	releaseServing := make(chan struct{})
	var servingOnce, relOnce sync.Once
	release := func() { relOnce.Do(func() { close(releaseServing) }) }
	defer release()
	var builtAtCallback atomic.Int32
	builtAtCallback.Store(-1)
	var cbStack atomic.Value
	modeOpt := xds.ServingModeCallback(func(addr net.Addr, args xds.ServingModeChangeArgs) {
		t.Logf("Serving mode for listener %q changed to %q, err: %v", addr.String(), args.Mode, args.Err)
		if args.Mode != connectivity.ServingModeServing {
			return
		}
		record("serving-callback-enter")
		servingOnce.Do(func() {
			builtAtCallback.Store(built.Load())
			cbStack.Store(vc2Frames(""))
			close(servingEntered)
			<-releaseServing
		})
		record("serving-callback-return")
	})

	managementServer, nodeID, bootstrapContents, xdsResolver := setup.ManagementServerAndResolver(t)

	// Provision resources first so the server's first listener update already
	// has its RouteConfiguration available.
	lis0, err := testutils.LocalTCPListener()
	if err != nil {
		t.Fatal(err)
	}
	host, port, err := hostPortFromListener(lis0)
	if err != nil {
		t.Fatal(err)
	}
	const serviceName = "my-service"
	resources := e2e.DefaultClientResources(e2e.ResourceParams{DialTarget: serviceName, NodeID: nodeID, Host: host, Port: port, SecLevel: e2e.SecurityLevelNone})
	resources.Listeners = append(resources.Listeners, vc2ServerListener(t, host, port, fb.typeURL))
	resources.Routes = append(resources.Routes, vc2RouteConfig(t, fb.typeURL, ""))
	ctx, cancel := context.WithTimeout(context.Background(), defaultTestTimeout)
	defer cancel()
	if err := managementServer.Update(ctx, resources); err != nil {
		t.Fatal(err)
	}

	srv, err := xds.NewGRPCServer(grpc.Creds(insecure.NewCredentials()), modeOpt, xds.BootstrapContentsForTesting(bootstrapContents))
	if err != nil {
		t.Fatal(err)
	}
	testgrpc.RegisterTestServiceServer(srv, &vc2Svc{})
	go srv.Serve(lis0)
	defer func() { release(); srv.Stop() }()

	select {
	case <-servingEntered:
	case <-ctx.Done():
		t.Fatal("timeout waiting for SERVING mode callback")
	}
	// Callback is held open. Give construction every chance to happen.
	time.Sleep(time.Second)
	heldBuilt := built.Load()
	t.Logf("serving callback stack:\n  %s", cbStack.Load())
	t.Logf("OBSERVED interceptors constructed when SERVING callback was entered: %d", builtAtCallback.Load())
	t.Logf("OBSERVED interceptors constructed after holding SERVING callback open for 1s: %d", heldBuilt)

	release()
	for dl := time.Now().Add(5 * time.Second); time.Now().Before(dl) && built.Load() == 0; time.Sleep(10 * time.Millisecond) {
	}
	t.Logf("OBSERVED interceptors constructed after SERVING callback returned: %d", built.Load())
	t.Logf("BuildServerInterceptor stack:\n  %s", buildStack.Load())

	// The server does work end to end once the callback returns.
	cc, err := grpc.NewClient(fmt.Sprintf("xds:///%s", serviceName), grpc.WithTransportCredentials(insecure.NewCredentials()), grpc.WithResolvers(xdsResolver))
	if err != nil {
		t.Fatal(err)
	}
	defer cc.Close()
	if _, err := testgrpc.NewTestServiceClient(cc).EmptyCall(ctx, &testpb.Empty{}, grpc.WaitForReady(true)); err != nil {
		t.Fatalf("EmptyCall failed: %v", err)
	}
	mu.Lock()
	t.Logf("OBSERVED event order: %v", events)
	mu.Unlock()

	if builtAtCallback.Load() == 0 && heldBuilt == 0 && built.Load() > 0 {
		t.Errorf("ORDER: serving notification was emitted before construction of the listener update's route interceptors (0 built while callback ran, %d built after it returned)", built.Load())
	}
}

type vc2Svc struct {
	testgrpc.UnimplementedTestServiceServer
}

func (*vc2Svc) EmptyCall(context.Context, *testpb.Empty) (*testpb.Empty, error) {
	return &testpb.Empty{}, nil
}
