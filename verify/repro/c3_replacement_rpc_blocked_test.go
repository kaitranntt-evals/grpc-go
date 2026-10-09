// Run: cp verify/repro/c3_replacement_rpc_blocked_test.go test/xds/ && go test -race -v -count=1 -run '^Test$/^Verify_C3_' ./test/xds   (on the C3 target branch; FAIL = stall reproduced)

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

// vc3Stacks returns the stacks of all goroutines whose stack mentions one of
// the given substrings.
func vc3Stacks(match ...string) string {
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

type vc3Cfg struct {
	httpfilter.FilterConfig
	path string
}

func vc3Parse(cfg proto.Message) (httpfilter.FilterConfig, error) {
	ts, ok := cfg.(*v3xdsxdstypepb.TypedStruct)
	if !ok {
		return nil, fmt.Errorf("unsupported filter config type: %T", cfg)
	}
	ret := vc3Cfg{}
	if v := ts.GetValue().GetFields()["path"]; v != nil {
		ret.path = v.GetStringValue()
	}
	return ret, nil
}

// vc3Builder is an HTTP filter whose interceptors delegate AllowRPC and Close
// to test-provided hooks; each interceptor is tagged with the "path" value of
// the route-level override (or the listener-level config, "path1").
type vc3Builder struct {
	typeURL  string
	allowRPC func(ctx context.Context, path string) error
	onClose  func(path string)
}

func (b *vc3Builder) IsTerminal() bool   { return false }
func (b *vc3Builder) TypeURLs() []string { return []string{b.typeURL} }
func (*vc3Builder) ParseFilterConfig(cfg proto.Message) (httpfilter.FilterConfig, error) {
	return vc3Parse(cfg)
}
func (*vc3Builder) ParseFilterConfigOverride(cfg proto.Message) (httpfilter.FilterConfig, error) {
	return vc3Parse(cfg)
}
func (b *vc3Builder) BuildServerFilter() httpfilter.ServerFilter { return b }
func (b *vc3Builder) Close()                                     {}
func (b *vc3Builder) BuildServerInterceptor(config, override httpfilter.FilterConfig) (resolver.ServerInterceptor, error) {
	cfg := config
	if override != nil {
		cfg = override
	}
	c, ok := cfg.(vc3Cfg)
	if !ok {
		return nil, fmt.Errorf("unexpected config %T", cfg)
	}
	return &vc3Interceptor{b: b, path: c.path}, nil
}

type vc3Interceptor struct {
	b    *vc3Builder
	path string
}

func (i *vc3Interceptor) AllowRPC(ctx context.Context) error { return i.b.allowRPC(ctx, i.path) }
func (i *vc3Interceptor) Close()                             { i.b.onClose(i.path) }

func vc3TypedStruct(t *testing.T, typeURL, path string) *anypb.Any {
	return testutils.MarshalAny(t, &v3xdsxdstypepb.TypedStruct{
		TypeUrl: typeURL,
		Value: &structpb.Struct{Fields: map[string]*structpb.Value{
			"path": {Kind: &structpb.Value_StringValue{StringValue: path}},
		}},
	})
}

func vc3RouteConfig(t *testing.T, typeURL, overridePath string) *v3routepb.RouteConfiguration {
	r := &v3routepb.Route{
		Match:  &v3routepb.RouteMatch{PathSpecifier: &v3routepb.RouteMatch_Prefix{Prefix: "/"}},
		Action: &v3routepb.Route_NonForwardingAction{},
	}
	if overridePath != "" {
		r.TypedPerFilterConfig = map[string]*anypb.Any{"probe-filter": vc3TypedStruct(t, typeURL, overridePath)}
	}
	return &v3routepb.RouteConfiguration{
		Name:         "probeRouteName",
		VirtualHosts: []*v3routepb.VirtualHost{{Domains: []string{"*"}, Routes: []*v3routepb.Route{r}}},
	}
}

type vc3Env struct {
	client testgrpc.TestServiceClient
	stop   func()
	update func(*v3routepb.RouteConfiguration)
	ctx    context.Context
}

// vc3Setup starts a real xds.NewGRPCServer (via the package's setupGRPCServer
// helper) against an in-process management server, with one catch-all filter
// chain whose HCM uses RDS and the probe filter (listener-level path "path1").
func vc3Setup(t *testing.T, fb *vc3Builder) *vc3Env {
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
						{Name: "probe-filter", ConfigType: &v3httppb.HttpFilter_TypedConfig{TypedConfig: vc3TypedStruct(t, fb.typeURL, "path1")}},
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
	resources.Routes = []*v3routepb.RouteConfiguration{clientRoute, vc3RouteConfig(t, fb.typeURL, "")}

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
	return &vc3Env{
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

// TestVerify_C3_NewRPCWhileRetiredCloseBlocked blocks Close() of the retired
// interceptor ("path1") during an in-place RDS replacement, then issues a new
// RPC (no deadline shorter than the hold) and records whether it completes
// while Close is still blocked, and which interceptor served it.
func (s) TestVerify_C3_NewRPCWhileRetiredCloseBlocked(t *testing.T) {
	pathCh := make(chan string, 16)
	closeEntered := make(chan struct{}, 4)
	releaseClose := make(chan struct{})
	var once sync.Once
	release := func() { once.Do(func() { close(releaseClose) }) }
	defer release()

	fb := &vc3Builder{
		typeURL: t.Name(),
		allowRPC: func(_ context.Context, path string) error {
			pathCh <- path
			return nil
		},
		onClose: func(path string) {
			if path != "path1" {
				return
			}
			closeEntered <- struct{}{}
			<-releaseClose
		},
	}
	e := vc3Setup(t, fb)
	defer func() { release(); e.stop() }()

	if _, err := e.client.EmptyCall(e.ctx, &testpb.Empty{}, grpc.WaitForReady(true)); err != nil {
		t.Fatalf("EmptyCall() on original route failed: %v", err)
	}
	t.Logf("TRACE initial RPC served by interceptor %q", <-pathCh)

	e.update(vc3RouteConfig(t, fb.typeURL, "path2"))
	select {
	case <-closeEntered:
	case <-e.ctx.Done():
		t.Fatal("timeout waiting for retired interceptor Close to be called")
	}
	start := time.Now()
	t.Logf("TRACE t=0s        retired interceptor path1 Close() entered and held; issuing new RPC")
	t.Logf("GOROUTINE performing the replacement (shows whether the route pointer swap already ran):\n%s", vc3Stacks("handleRDSUpdate"))

	type res struct {
		err error
		at  time.Time
	}
	rpcDone := make(chan res, 1)
	go func() {
		_, err := e.client.EmptyCall(e.ctx, &testpb.Empty{})
		rpcDone <- res{err, time.Now()}
	}()

	stalled := false
	select {
	case r := <-rpcDone:
		if r.err != nil {
			t.Fatalf("new RPC failed: %v", r.err)
		}
		t.Logf("TRACE t=%v new RPC completed while retired Close() still blocked; served by %q", r.at.Sub(start).Round(time.Millisecond), <-pathCh)
	case <-time.After(5 * time.Second):
		stalled = true
		t.Logf("TRACE t=5s        new RPC still pending; retired Close() still blocked")
		t.Logf("GOROUTINES at t=5s (routing / replacement):\n%s", vc3Stacks("RouteAndProcess", "handleRDSUpdate"))
		release()
		rel := time.Since(start)
		t.Logf("TRACE t=%v released retired Close()", rel.Round(time.Millisecond))
		select {
		case r := <-rpcDone:
			if r.err != nil {
				t.Fatalf("new RPC failed after release: %v", r.err)
			}
			t.Logf("TRACE t=%v new RPC completed %v after release; served by %q", r.at.Sub(start).Round(time.Millisecond), (r.at.Sub(start) - rel).Round(time.Millisecond), <-pathCh)
		case <-time.After(5 * time.Second):
			t.Fatal("new RPC still pending 5s after Close() was released")
		}
	}
	if stalled {
		t.Fatal("STALL: new RPC could not use the published replacement route configuration until the retired interceptor's Close() returned")
	}
}
