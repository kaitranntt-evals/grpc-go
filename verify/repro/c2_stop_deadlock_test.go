// Run: cp verify/repro/c2_stop_deadlock_test.go test/xds/ && go test -race -v -count=1 -run '^Test$/^Verify_C2_' ./test/xds   (on the C2 target branch; FAIL = deadlock reproduced)

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
	"google.golang.org/grpc/credentials/insecure"
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

// vc2Stacks returns the stacks of all goroutines whose stack mentions one of
// the given substrings.
func vc2Stacks(match ...string) string {
	buf := make([]byte, 4<<20)
	buf = buf[:runtime.Stack(buf, true)]
	var out []string
	for _, g := range strings.Split(string(buf), "\n\n") {
		for _, m := range match {
			if strings.Contains(g, m) {
				out = append(out, g)
				break
			}
		}
	}
	return strings.Join(out, "\n\n")
}

type vc2Cfg struct {
	httpfilter.FilterConfig
	path string
}

func vc2Parse(cfg proto.Message) (httpfilter.FilterConfig, error) {
	ts, ok := cfg.(*v3xdsxdstypepb.TypedStruct)
	if !ok {
		return nil, fmt.Errorf("unsupported filter config type: %T", cfg)
	}
	ret := vc2Cfg{}
	if v := ts.GetValue().GetFields()["path"]; v != nil {
		ret.path = v.GetStringValue()
	}
	return ret, nil
}

// vc2Builder is an HTTP filter whose interceptors delegate AllowRPC and Close
// to test-provided hooks; each interceptor is tagged with the "path" value of
// the route-level override (or the listener-level config, "path1").
type vc2Builder struct {
	typeURL  string
	allowRPC func(ctx context.Context, path string) error
	onClose  func(path string)
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
	c, ok := cfg.(vc2Cfg)
	if !ok {
		return nil, fmt.Errorf("unexpected config %T", cfg)
	}
	return &vc2Interceptor{b: b, path: c.path}, nil
}

type vc2Interceptor struct {
	b    *vc2Builder
	path string
}

func (i *vc2Interceptor) AllowRPC(ctx context.Context) error { return i.b.allowRPC(ctx, i.path) }
func (i *vc2Interceptor) Close()                             { i.b.onClose(i.path) }

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
		r.TypedPerFilterConfig = map[string]*anypb.Any{"probe-filter": vc2TypedStruct(t, typeURL, overridePath)}
	}
	return &v3routepb.RouteConfiguration{
		Name:         "probeRouteName",
		VirtualHosts: []*v3routepb.VirtualHost{{Domains: []string{"*"}, Routes: []*v3routepb.Route{r}}},
	}
}

type vc2Env struct {
	client testgrpc.TestServiceClient
	stop   func()
	update func(*v3routepb.RouteConfiguration)
	ctx    context.Context
}

// vc2Setup starts a real xds.NewGRPCServer (via the package's setupGRPCServer
// helper) against an in-process management server, with one catch-all filter
// chain whose HCM uses RDS and the probe filter (listener-level path "path1").
func vc2Setup(t *testing.T, fb *vc2Builder) *vc2Env {
	httpfilter.Register(fb)
	t.Cleanup(func() { httpfilter.UnregisterForTesting(fb.typeURL) })

	managementServer, nodeID, bootstrapContents, xdsResolver := setup.ManagementServerAndResolver(t)
	lis, stopServer := setupGRPCServer(t, bootstrapContents)
	host, port, err := hostPortFromListener(lis)
	if err != nil {
		t.Fatalf("failed to retrieve host and port of server: %v", err)
	}
	const serviceName = "my-service"
	resources := e2e.DefaultClientResources(e2e.ResourceParams{
		DialTarget: serviceName, NodeID: nodeID, Host: host, Port: port, SecLevel: e2e.SecurityLevelNone,
	})
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
						{Name: "probe-filter", ConfigType: &v3httppb.HttpFilter_TypedConfig{TypedConfig: vc2TypedStruct(t, fb.typeURL, "path1")}},
						e2e.HTTPFilter("router", &v3routerpb.Router{}),
					},
					RouteSpecifier: &v3httppb.HttpConnectionManager_Rds{Rds: &v3httppb.Rds{
						ConfigSource:    &v3corepb.ConfigSource{ConfigSourceSpecifier: &v3corepb.ConfigSource_Ads{Ads: &v3corepb.AggregatedConfigSource{}}},
						RouteConfigName: "probeRouteName",
					}},
				})},
			}},
		}},
	})
	resources.Routes = []*v3routepb.RouteConfiguration{clientRoute, vc2RouteConfig(t, fb.typeURL, "")}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	t.Cleanup(cancel)
	if err := managementServer.Update(ctx, resources); err != nil {
		t.Fatal(err)
	}
	cc, err := grpc.NewClient(fmt.Sprintf("xds:///%s", serviceName), grpc.WithTransportCredentials(insecure.NewCredentials()), grpc.WithResolvers(xdsResolver))
	if err != nil {
		t.Fatalf("grpc.NewClient() failed: %v", err)
	}
	t.Cleanup(func() { cc.Close() })

	var stopOnce sync.Once
	return &vc2Env{
		client: testgrpc.NewTestServiceClient(cc),
		stop:   func() { stopOnce.Do(stopServer) },
		update: func(rc *v3routepb.RouteConfiguration) {
			resources.Routes = []*v3routepb.RouteConfiguration{clientRoute, rc}
			if err := managementServer.Update(ctx, resources); err != nil {
				t.Fatal(err)
			}
		},
		ctx: ctx,
	}
}

var _ = testpb.Empty{}

// TestVerify_C2_StopWhileAllowRPCAwaitsContextCancel calls Server.Stop while
// an interceptor's AllowRPC is waiting for its RPC context to be cancelled.
// A healthy server cancels the RPC's transport, AllowRPC returns, and Stop
// returns promptly. The test holds the situation for 8s, dumps the goroutines
// involved, and only then cancels the RPC from the client to unblock.
func (s) TestVerify_C2_StopWhileAllowRPCAwaitsContextCancel(t *testing.T) {
	allowEntered := make(chan struct{}, 1)
	allowReturned := make(chan time.Time, 1)
	fb := &vc2Builder{
		typeURL: t.Name(),
		allowRPC: func(ctx context.Context, _ string) error {
			allowEntered <- struct{}{}
			<-ctx.Done() // Wait only for the server-side RPC context.
			allowReturned <- time.Now()
			return ctx.Err()
		},
		onClose: func(string) {},
	}
	e := vc2Setup(t, fb)

	rpcCtx, cancelRPC := context.WithCancel(e.ctx)
	defer func() { cancelRPC(); e.stop() }()
	go e.client.EmptyCall(rpcCtx, &testpb.Empty{}, grpc.WaitForReady(true))
	select {
	case <-allowEntered:
	case <-e.ctx.Done():
		t.Fatal("timeout waiting for AllowRPC to be invoked")
	}

	start := time.Now()
	t.Logf("TRACE t=0s        AllowRPC entered and waiting on RPC ctx; calling Server.Stop()")
	stopDone := make(chan time.Time, 1)
	go func() { e.stop(); stopDone <- time.Now() }()

	select {
	case ts := <-stopDone:
		t.Logf("TRACE t=%v Stop() returned (no deadlock)", ts.Sub(start).Round(time.Millisecond))
		select {
		case ar := <-allowReturned:
			t.Logf("TRACE t=%v AllowRPC observed ctx cancellation (cancelled by server shutdown)", ar.Sub(start).Round(time.Millisecond))
		case <-time.After(2 * time.Second):
			t.Logf("TRACE AllowRPC had not returned 2s after Stop() returned")
		}
		return
	case <-time.After(8 * time.Second):
	}

	select {
	case <-allowReturned:
		t.Logf("TRACE t=8s AllowRPC already returned")
	default:
		t.Logf("TRACE t=8s        Stop() still blocked; AllowRPC still waiting (its RPC ctx was never cancelled)")
	}
	t.Logf("GOROUTINES at t=8s (Stop / listener cleanup / routing):\n%s", vc2Stacks("grpc.(*Server).Stop", "RouteAndProcess", "listenerWrapper).Close"))

	// Break the cycle from outside: client cancels the RPC -> RST_STREAM ->
	// server-side ctx cancelled -> AllowRPC returns -> read lock released.
	cancelRPC()
	t.Logf("TRACE t=%v client cancelled the RPC", time.Since(start).Round(time.Millisecond))
	select {
	case ar := <-allowReturned:
		t.Logf("TRACE t=%v AllowRPC returned", ar.Sub(start).Round(time.Millisecond))
	case <-time.After(5 * time.Second):
		t.Logf("TRACE AllowRPC did not return after client cancel")
	}
	select {
	case ts := <-stopDone:
		t.Logf("TRACE t=%v Stop() returned only after AllowRPC finished", ts.Sub(start).Round(time.Millisecond))
	case <-time.After(5 * time.Second):
		t.Logf("TRACE Stop() still blocked 5s after client cancel")
	}
	t.Fatal("DEADLOCK: Server.Stop() did not return for 8s while AllowRPC waited for RPC context cancellation")
}
