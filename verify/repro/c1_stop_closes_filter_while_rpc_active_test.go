// Run: git checkout evalon/grpc-go-xd-e8b11ebf (repo kaitranntt-evals/grpc-go-xds-rds-interceptor-lifecycle-leak); cp verify/repro/c1_stop_closes_filter_while_rpc_active_test.go test/xds/ && go test -race -count=1 -v -run '^Test$/^VerifyC1_' ./test/xds

package xds_test

import (
	"context"
	"fmt"
	"net"
	"runtime/debug"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/internal/resolver"
	"google.golang.org/grpc/internal/stubserver"
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

// vc1Log is an ordered, timestamped event log shared by the fixtures.
type vc1Log struct {
	mu     sync.Mutex
	start  time.Time
	events []string
	kinds  []string
}

func (l *vc1Log) add(kind, format string, a ...any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.kinds = append(l.kinds, kind)
	l.events = append(l.events, fmt.Sprintf("%02d +%7.1fms %-22s %s", len(l.events)+1, float64(time.Since(l.start).Microseconds())/1000, kind, fmt.Sprintf(format, a...)))
}

func (l *vc1Log) index(kind string) int {
	l.mu.Lock()
	defer l.mu.Unlock()
	for i, k := range l.kinds {
		if k == kind {
			return i
		}
	}
	return -1
}

func (l *vc1Log) dump(t *testing.T) {
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, e := range l.events {
		t.Log(e)
	}
}

type vc1Cfg struct{ httpfilter.FilterConfig }

// vc1Builder is an HTTP filter whose ServerFilter and ServerInterceptor both
// have observable Close() implementations.
type vc1Builder struct {
	typeURL string
	log     *vc1Log
	// blockInAllow makes AllowRPC wait for the RPC context to be cancelled.
	blockInAllow bool
	allowEntered chan struct{}
	// releaseHandler lets the FullDuplexCall handler finish normally.
	releaseHandler chan struct{}
	gracefulStop   func()

	mu     sync.Mutex
	rpcCtx context.Context // context of the RPC currently held active
}

func (b *vc1Builder) IsTerminal() bool   { return false }
func (b *vc1Builder) TypeURLs() []string { return []string{b.typeURL} }
func (*vc1Builder) ParseFilterConfig(proto.Message) (httpfilter.FilterConfig, error) {
	return vc1Cfg{}, nil
}
func (*vc1Builder) ParseFilterConfigOverride(proto.Message) (httpfilter.FilterConfig, error) {
	return vc1Cfg{}, nil
}
func (b *vc1Builder) BuildServerFilter() httpfilter.ServerFilter {
	b.log.add("ServerFilter.Build", "")
	return &vc1Filter{b: b}
}

func (b *vc1Builder) rpcCtxState() string {
	b.mu.Lock()
	ctx := b.rpcCtx
	b.mu.Unlock()
	if ctx == nil {
		return "no RPC seen"
	}
	return fmt.Sprintf("activeRPC ctx.Err()=%v", ctx.Err())
}

func vc1Stack() string {
	var keep []string
	for _, ln := range strings.Split(string(debug.Stack()), "\n") {
		if strings.HasPrefix(ln, "google.golang.org/grpc") && !strings.Contains(ln, "vc1") {
			keep = append(keep, strings.TrimPrefix(ln[:strings.LastIndex(ln, "(")], "google.golang.org/grpc"))
		}
	}
	return strings.Join(keep, " <- ")
}

type vc1Filter struct{ b *vc1Builder }

func (f *vc1Filter) BuildServerInterceptor(_, _ httpfilter.FilterConfig) (resolver.ServerInterceptor, error) {
	f.b.log.add("Interceptor.Build", "")
	return &vc1Interceptor{b: f.b}, nil
}

func (f *vc1Filter) Close() {
	f.b.log.add("ServerFilter.Close", "%s | stack: %s", f.b.rpcCtxState(), vc1Stack())
	// Give a concurrent transport-context cancellation every chance to land
	// before sampling again.
	time.Sleep(300 * time.Millisecond)
	f.b.log.add("ServerFilter.Close+300ms", "%s", f.b.rpcCtxState())
}

type vc1Interceptor struct{ b *vc1Builder }

func (i *vc1Interceptor) AllowRPC(ctx context.Context) error {
	i.b.mu.Lock()
	i.b.rpcCtx = ctx
	i.b.mu.Unlock()
	i.b.log.add("AllowRPC.enter", "")
	select {
	case i.b.allowEntered <- struct{}{}:
	default:
	}
	if i.b.blockInAllow {
		<-ctx.Done()
		i.b.log.add("AllowRPC.return", "ctx.Err()=%v", ctx.Err())
		return ctx.Err()
	}
	i.b.log.add("AllowRPC.return", "nil (RPC proceeds to handler)")
	return nil
}

func (i *vc1Interceptor) Close() {
	i.b.log.add("Interceptor.Close", "%s | stack: %s", i.b.rpcCtxState(), vc1Stack())
}

func vc1Setup(t *testing.T, fb *vc1Builder) (testgrpc.TestServiceClient, func(), context.Context) {
	httpfilter.Register(fb)
	t.Cleanup(func() { httpfilter.UnregisterForTesting(fb.typeURL) })

	managementServer, nodeID, bootstrapContents, xdsResolver := setup.ManagementServerAndResolver(t)
	// Same wiring as the package's setupGRPCServer helper, but with a
	// FullDuplexCall handler that stays active until its context is done.
	stub := &stubserver.StubServer{
		EmptyCallF: func(context.Context, *testpb.Empty) (*testpb.Empty, error) { return &testpb.Empty{}, nil },
		FullDuplexCallF: func(stream testgrpc.TestService_FullDuplexCallServer) error {
			fb.log.add("Handler.enter", "")
			select {
			case <-stream.Context().Done():
			case <-fb.releaseHandler:
			}
			fb.log.add("Handler.return", "ctx.Err()=%v", stream.Context().Err())
			return stream.Context().Err()
		},
	}
	var err error
	if stub.S, err = xds.NewGRPCServer(grpc.Creds(insecure.NewCredentials()), testModeChangeServerOption(t), xds.BootstrapContentsForTesting(bootstrapContents)); err != nil {
		t.Fatalf("Failed to create an xDS enabled gRPC server: %v", err)
	}
	lis, err := testutils.LocalTCPListener()
	if err != nil {
		t.Fatalf("testutils.LocalTCPListener() failed: %v", err)
	}
	stub.Listener = lis
	stubserver.StartTestService(t, stub)
	stopServer := func() { stub.S.Stop() }
	fb.gracefulStop = stub.S.GracefulStop
	host, port, err := hostPortFromListener(lis)
	if err != nil {
		t.Fatalf("failed to retrieve host and port of server: %v", err)
	}
	const serviceName = "my-service"
	const routeName = "vc1RouteName"
	resources := e2e.DefaultClientResources(e2e.ResourceParams{DialTarget: serviceName, NodeID: nodeID, Host: host, Port: port, SecLevel: e2e.SecurityLevelNone})
	filterCfg := testutils.MarshalAny(t, &v3xdsxdstypepb.TypedStruct{TypeUrl: fb.typeURL, Value: &structpb.Struct{}})
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
						{Name: "vc1-filter", ConfigType: &v3httppb.HttpFilter_TypedConfig{TypedConfig: filterCfg}},
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
	resources.Routes = append(resources.Routes, &v3routepb.RouteConfiguration{
		Name: routeName,
		VirtualHosts: []*v3routepb.VirtualHost{{Domains: []string{"*"}, Routes: []*v3routepb.Route{{
			Match:  &v3routepb.RouteMatch{PathSpecifier: &v3routepb.RouteMatch_Prefix{Prefix: "/"}},
			Action: &v3routepb.Route_NonForwardingAction{},
		}}}},
	})

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
	var once sync.Once
	stop := func() { once.Do(stopServer) }
	t.Cleanup(stop)
	return testgrpc.NewTestServiceClient(cc), stop, ctx
}

// vc1Report prints the event log and the ordering verdicts. It deliberately
// does not fail the test: it reports observations for both claim parts.
func vc1Report(t *testing.T, log *vc1Log, rpcEnd string) {
	log.dump(t)
	stop, sf, ic, end := log.index("Stop.call"), log.index("ServerFilter.Close"), log.index("Interceptor.Close"), log.index(rpcEnd)
	t.Logf("OBSERVED order indexes: Stop.call=%d ServerFilter.Close=%d Interceptor.Close=%d %s=%d", stop+1, sf+1, ic+1, rpcEnd, end+1)
	t.Logf("OBSERVED ServerFilter.Close ran before the active RPC ended: %v", sf >= 0 && sf < end)
	t.Logf("OBSERVED Interceptor.Close ran before the active RPC ended: %v", ic >= 0 && ic < end)
}

// Scenario A: the RPC is held inside the configuration interceptor's AllowRPC
// (it only returns when the RPC/transport context is cancelled).
func (s) TestVerifyC1_StopWhileRPCInAllowRPC(t *testing.T) {
	log := &vc1Log{start: time.Now()}
	fb := &vc1Builder{typeURL: t.Name(), log: log, blockInAllow: true, allowEntered: make(chan struct{}, 1)}
	client, stop, ctx := vc1Setup(t, fb)

	rpcDone := make(chan error, 1)
	go func() {
		_, err := client.EmptyCall(ctx, &testpb.Empty{}, grpc.WaitForReady(true))
		rpcDone <- err
	}()
	select {
	case <-fb.allowEntered:
	case <-ctx.Done():
		t.Fatal("timeout waiting for AllowRPC")
	}
	log.add("Stop.call", "%s", fb.rpcCtxState())
	stop()
	log.add("Stop.return", "%s", fb.rpcCtxState())
	select {
	case err := <-rpcDone:
		log.add("Client.RPCDone", "err=%v", err)
	case <-ctx.Done():
		t.Fatal("timeout waiting for RPC to end")
	}
	time.Sleep(200 * time.Millisecond)
	vc1Report(t, log, "AllowRPC.return")
}

// Scenario B: the RPC has passed AllowRPC and its handler is running (a
// bidi stream held open by the client).
func (s) TestVerifyC1_StopWhileRPCInHandler(t *testing.T) {
	log := &vc1Log{start: time.Now()}
	fb := &vc1Builder{typeURL: t.Name(), log: log, allowEntered: make(chan struct{}, 1)}
	client, stop, ctx := vc1Setup(t, fb)

	stream, err := client.FullDuplexCall(ctx, grpc.WaitForReady(true))
	if err != nil {
		t.Fatalf("FullDuplexCall() failed: %v", err)
	}
	if err := stream.Send(&testpb.StreamingOutputCallRequest{}); err != nil {
		t.Fatalf("Send() failed: %v", err)
	}
	select {
	case <-fb.allowEntered:
	case <-ctx.Done():
		t.Fatal("timeout waiting for AllowRPC")
	}
	for log.index("Handler.enter") < 0 && ctx.Err() == nil {
		time.Sleep(5 * time.Millisecond)
	}
	log.add("Stop.call", "%s", fb.rpcCtxState())
	stop()
	log.add("Stop.return", "%s", fb.rpcCtxState())
	_, err = stream.Recv()
	log.add("Client.RPCDone", "err=%v", err)
	time.Sleep(200 * time.Millisecond)
	vc1Report(t, log, "Handler.return")
}

// Scenario C: same as B but with GracefulStop: the RPC is not cancelled at
// all and completes successfully after its filter resources were closed.
func (s) TestVerifyC1_GracefulStopWhileRPCInHandler(t *testing.T) {
	log := &vc1Log{start: time.Now()}
	fb := &vc1Builder{typeURL: t.Name(), log: log, allowEntered: make(chan struct{}, 1), releaseHandler: make(chan struct{})}
	client, _, ctx := vc1Setup(t, fb)

	stream, err := client.FullDuplexCall(ctx, grpc.WaitForReady(true))
	if err != nil {
		t.Fatalf("FullDuplexCall() failed: %v", err)
	}
	if err := stream.Send(&testpb.StreamingOutputCallRequest{}); err != nil {
		t.Fatalf("Send() failed: %v", err)
	}
	for log.index("Handler.enter") < 0 && ctx.Err() == nil {
		time.Sleep(5 * time.Millisecond)
	}
	log.add("Stop.call", "GracefulStop; %s", fb.rpcCtxState())
	stopped := make(chan struct{})
	go func() { fb.gracefulStop(); log.add("Stop.return", "%s", fb.rpcCtxState()); close(stopped) }()
	// Hold the RPC active for a full second after GracefulStop was called.
	time.Sleep(time.Second)
	log.add("Handler.release", "%s", fb.rpcCtxState())
	close(fb.releaseHandler)
	_, err = stream.Recv()
	log.add("Client.RPCDone", "err=%v", err)
	<-stopped
	vc1Report(t, log, "Handler.return")
}
