// Run: git checkout evalon/grpc-go-xd-2f2a2d0c (repo kaitranntt-evals/grpc-go-xds-rds-interceptor-lifecycle-leak); cp verify/repro/c2_held_allowrpc_blocks_replacement_test.go test/xds/ && go test -race -count=1 -v -run '^Test$/^VerifyC2_' ./test/xds

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

type vc2Log struct {
	mu     sync.Mutex
	start  time.Time
	events []string
	kinds  []string
}

func (l *vc2Log) add(kind, format string, a ...any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.kinds = append(l.kinds, kind)
	l.events = append(l.events, fmt.Sprintf("%02d +%7.1fms %-28s %s", len(l.events)+1, float64(time.Since(l.start).Microseconds())/1000, kind, fmt.Sprintf(format, a...)))
}

func (l *vc2Log) has(kind string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, k := range l.kinds {
		if k == kind {
			return true
		}
	}
	return false
}

type vc2Cfg struct {
	httpfilter.FilterConfig
	gen string
}

func vc2Parse(cfg proto.Message) (httpfilter.FilterConfig, error) {
	ts, ok := cfg.(*v3xdsxdstypepb.TypedStruct)
	if !ok {
		return nil, fmt.Errorf("unsupported filter config type: %T", cfg)
	}
	return vc2Cfg{gen: ts.GetValue().GetFields()["gen"].GetStringValue()}, nil
}

// vc2Builder builds interceptors tagged with the "gen" of the route
// configuration that created them. The first AllowRPC on gen "old" is held
// open until release is closed.
type vc2Builder struct {
	typeURL  string
	log      *vc2Log
	release  chan struct{}
	holdOnce sync.Once
	held     chan struct{}
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
	gen := cfg.(vc2Cfg).gen
	b.log.add("Interceptor.Build("+gen+")", "server constructed the %q route configuration", gen)
	return &vc2Interceptor{b: b, gen: gen}, nil
}

type vc2Interceptor struct {
	b   *vc2Builder
	gen string
}

func (i *vc2Interceptor) AllowRPC(context.Context) error {
	hold := false
	if i.gen == "old" {
		i.b.holdOnce.Do(func() { hold = true })
	}
	if hold {
		i.b.log.add("AllowRPC("+i.gen+").enter", "HELD OPEN")
		close(i.b.held)
		<-i.b.release
		i.b.log.add("AllowRPC("+i.gen+").return", "held call returns")
		return nil
	}
	i.b.log.add("AllowRPC("+i.gen+")", "new RPC routed through %q configuration", i.gen)
	return nil
}

func (i *vc2Interceptor) Close() { i.b.log.add("Interceptor.Close("+i.gen+")", "") }

func vc2Any(t *testing.T, typeURL, gen string) *anypb.Any {
	return testutils.MarshalAny(t, &v3xdsxdstypepb.TypedStruct{TypeUrl: typeURL, Value: &structpb.Struct{Fields: map[string]*structpb.Value{
		"gen": {Kind: &structpb.Value_StringValue{StringValue: gen}},
	}}})
}

func vc2Route(t *testing.T, typeURL, gen string) *v3routepb.RouteConfiguration {
	return &v3routepb.RouteConfiguration{
		Name: "vc2RouteName",
		VirtualHosts: []*v3routepb.VirtualHost{{Domains: []string{"*"}, Routes: []*v3routepb.Route{{
			Match:                &v3routepb.RouteMatch{PathSpecifier: &v3routepb.RouteMatch_Prefix{Prefix: "/"}},
			Action:               &v3routepb.Route_NonForwardingAction{},
			TypedPerFilterConfig: map[string]*anypb.Any{"vc2-filter": vc2Any(t, typeURL, gen)},
		}}}},
	}
}

// vc2ServerGoroutines returns the stacks of goroutines currently inside the
// xDS server routing / configuration-update paths.
func vc2ServerGoroutines() string {
	buf := make([]byte, 4<<20)
	buf = buf[:runtime.Stack(buf, true)]
	var out []string
	for _, g := range strings.Split(string(buf), "\n\n") {
		if !strings.Contains(g, "internal/xds/server.RouteAndProcess") && !strings.Contains(g, "handleRDSUpdate") {
			continue
		}
		var fns []string
		for _, ln := range strings.Split(g, "\n") {
			if strings.HasPrefix(ln, "goroutine ") {
				fns = append(fns, ln)
			} else if !strings.HasPrefix(ln, "\t") && strings.Contains(ln, "(") {
				fns = append(fns, "    "+ln[:strings.LastIndex(ln, "(")])
			}
		}
		if len(fns) > 9 {
			fns = fns[:9]
		}
		out = append(out, strings.Join(fns, "\n"))
	}
	return strings.Join(out, "\n")
}

func (s) TestVerifyC2_HeldAllowRPCBlocksReplacement(t *testing.T) {
	log := &vc2Log{start: time.Now()}
	fb := &vc2Builder{typeURL: t.Name(), log: log, release: make(chan struct{}), held: make(chan struct{})}
	httpfilter.Register(fb)
	t.Cleanup(func() { httpfilter.UnregisterForTesting(fb.typeURL) })

	managementServer, nodeID, bootstrapContents, xdsResolver := setup.ManagementServerAndResolver(t)
	lis, stopServer := setupGRPCServer(t, bootstrapContents)
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(fb.release) }) }
	t.Cleanup(func() { release(); stopServer() })
	host, port, err := hostPortFromListener(lis)
	if err != nil {
		t.Fatalf("failed to retrieve host and port of server: %v", err)
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
						{Name: "vc2-filter", ConfigType: &v3httppb.HttpFilter_TypedConfig{TypedConfig: vc2Any(t, fb.typeURL, "listener")}},
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
	resources.Routes = []*v3routepb.RouteConfiguration{clientRoute, vc2Route(t, fb.typeURL, "old")}
	ctx, cancel := context.WithTimeout(context.Background(), defaultTestTimeout)
	t.Cleanup(cancel)
	if err := managementServer.Update(ctx, resources); err != nil {
		t.Fatal(err)
	}
	cc, err := grpc.NewClient(fmt.Sprintf("xds:///%s", serviceName), grpc.WithTransportCredentials(insecure.NewCredentials()), grpc.WithResolvers(xdsResolver))
	if err != nil {
		t.Fatalf("grpc.NewClient() failed: %v", err)
	}
	t.Cleanup(func() { cc.Close() })
	client := testgrpc.NewTestServiceClient(cc)

	// 1. Hold an AllowRPC call open on the old configuration.
	heldDone := make(chan error, 1)
	go func() {
		_, err := client.EmptyCall(ctx, &testpb.Empty{}, grpc.WaitForReady(true))
		heldDone <- err
	}()
	select {
	case <-fb.held:
	case <-ctx.Done():
		t.Fatal("timeout waiting for the held AllowRPC")
	}

	// 2. Submit the replacement configuration and wait until the server has
	// received and constructed it.
	resources.Routes = []*v3routepb.RouteConfiguration{clientRoute, vc2Route(t, fb.typeURL, "new")}
	log.add("Update.submit", "management server now serves the \"new\" route configuration")
	if err := managementServer.Update(ctx, resources); err != nil {
		t.Fatal(err)
	}
	for !log.has("Interceptor.Build(new)") {
		if ctx.Err() != nil {
			t.Fatal("timeout waiting for the server to construct the replacement configuration")
		}
		time.Sleep(5 * time.Millisecond)
	}
	time.Sleep(200 * time.Millisecond)
	log.add("Goroutines.whileHeld", "\n%s", vc2ServerGoroutines())

	// 3. While the old AllowRPC is still held, send newly routed RPCs for 3s.
	const probes = 6
	usedNewWhileHeld := false
	for i := 1; i <= probes; i++ {
		pctx, pcancel := context.WithTimeout(ctx, 500*time.Millisecond)
		before := log.has("AllowRPC(new)")
		_, err := client.EmptyCall(pctx, &testpb.Empty{})
		pcancel()
		after := log.has("AllowRPC(new)")
		usedNewWhileHeld = usedNewWhileHeld || after
		log.add("Probe.whileHeld", "#%d err=%v reachedNewConfig=%v", i, err, after && (!before || err == nil))
	}
	log.add("Goroutines.afterProbes", "\n%s", vc2ServerGoroutines())

	// 4. Let the old call return, then probe again.
	log.add("Release", "letting the held AllowRPC(old) return")
	release()
	if err := <-heldDone; err != nil {
		t.Errorf("held RPC failed: %v", err)
	}
	usedNewAfterRelease := false
	for i := 1; i <= 20 && !usedNewAfterRelease; i++ {
		pctx, pcancel := context.WithTimeout(ctx, 500*time.Millisecond)
		_, err := client.EmptyCall(pctx, &testpb.Empty{})
		pcancel()
		usedNewAfterRelease = log.has("AllowRPC(new)")
		log.add("Probe.afterRelease", "#%d err=%v reachedNewConfig=%v", i, err, usedNewAfterRelease)
	}

	log.mu.Lock()
	for _, e := range log.events {
		t.Log(e)
	}
	log.mu.Unlock()
	t.Logf("OBSERVED replacement used by a newly routed RPC while old AllowRPC was held: %v", usedNewWhileHeld)
	t.Logf("OBSERVED replacement used by a newly routed RPC after old AllowRPC returned: %v", usedNewAfterRelease)
}
