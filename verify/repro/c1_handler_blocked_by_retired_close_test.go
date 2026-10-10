// Run: on evalon/grpc-go-xd-7dc0ea45, cp this file to test/xds/verify_c1_handler_blocked_by_retired_close_test.go and run `go test -race -count=1 -v -run '^Test$/^VerifyC1_' ./test/xds` (FAILS while the problem is present).

package xds_test

import (
	"context"
	"fmt"
	"net"
	"runtime"
	"strconv"
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
	"google.golang.org/protobuf/types/known/anypb"
	"google.golang.org/protobuf/types/known/structpb"
)

type vc1Cfg struct {
	httpfilter.FilterConfig
	path string
}

func vc1Parse(cfg proto.Message) (httpfilter.FilterConfig, error) {
	ts, ok := cfg.(*v3xdsxdstypepb.TypedStruct)
	if !ok {
		return nil, fmt.Errorf("unsupported filter config type: %T", cfg)
	}
	return vc1Cfg{path: ts.GetValue().GetFields()["path"].GetStringValue()}, nil
}

type vc1Builder struct {
	typeURL  string
	allowRPC func(ctx context.Context, path string) error
	onClose  func(path string)
}

func (b *vc1Builder) IsTerminal() bool   { return false }
func (b *vc1Builder) TypeURLs() []string { return []string{b.typeURL} }
func (*vc1Builder) ParseFilterConfig(cfg proto.Message) (httpfilter.FilterConfig, error) {
	return vc1Parse(cfg)
}
func (*vc1Builder) ParseFilterConfigOverride(cfg proto.Message) (httpfilter.FilterConfig, error) {
	return vc1Parse(cfg)
}
func (b *vc1Builder) BuildServerFilter() httpfilter.ServerFilter { return b }
func (b *vc1Builder) Close()                                     {}
func (b *vc1Builder) BuildServerInterceptor(config, override httpfilter.FilterConfig) (resolver.ServerInterceptor, error) {
	cfg := config
	if override != nil {
		cfg = override
	}
	return &vc1Interceptor{b: b, path: cfg.(vc1Cfg).path}, nil
}

type vc1Interceptor struct {
	b    *vc1Builder
	path string
}

func (i *vc1Interceptor) AllowRPC(ctx context.Context) error { return i.b.allowRPC(ctx, i.path) }
func (i *vc1Interceptor) Close()                             { i.b.onClose(i.path) }

func vc1TypedStruct(t *testing.T, typeURL, path string) *anypb.Any {
	return testutils.MarshalAny(t, &v3xdsxdstypepb.TypedStruct{
		TypeUrl: typeURL,
		Value: &structpb.Struct{Fields: map[string]*structpb.Value{
			"path": {Kind: &structpb.Value_StringValue{StringValue: path}},
		}},
	})
}

func vc1RouteConfig(t *testing.T, typeURL, overridePath string) *v3routepb.RouteConfiguration {
	r := &v3routepb.Route{
		Match:  &v3routepb.RouteMatch{PathSpecifier: &v3routepb.RouteMatch_Prefix{Prefix: "/"}},
		Action: &v3routepb.Route_NonForwardingAction{},
	}
	if overridePath != "" {
		r.TypedPerFilterConfig = map[string]*anypb.Any{"vc1-filter": vc1TypedStruct(t, typeURL, overridePath)}
	}
	return &v3routepb.RouteConfiguration{
		Name:         "vc1RouteName",
		VirtualHosts: []*v3routepb.VirtualHost{{Domains: []string{"*"}, Routes: []*v3routepb.Route{r}}},
	}
}

// TestVerifyC1_HandlerWaitsForRetiredInterceptorClose holds a unary RPC
// (EmptyCall) in AllowRPC of the original interceptor ("path1"), replaces the
// route configuration ("path2"), lets AllowRPC return nil (RPC admitted), and
// observes whether the EmptyCall service handler runs while the retired
// interceptor's Close is still blocked.
func (s) TestVerifyC1_HandlerWaitsForRetiredInterceptorClose(t *testing.T) {
	start := time.Now()
	ts := func() string { return time.Since(start).Round(time.Millisecond).String() }

	allowEntered := make(chan struct{})
	releaseAllow := make(chan struct{})
	closeEntered := make(chan string, 1)
	closeReturned := make(chan struct{})
	releaseClose := make(chan struct{})
	handlerEntered := make(chan struct{})
	path2Served := make(chan struct{}, 16)
	var allowOnce, handlerOnce, closeOnce, relCloseOnce sync.Once
	doReleaseClose := func() { relCloseOnce.Do(func() { close(releaseClose) }) }

	fb := &vc1Builder{
		typeURL: t.Name(),
		allowRPC: func(ctx context.Context, path string) error {
			if path == "path1" {
				allowOnce.Do(func() { close(allowEntered) })
				select {
				case <-releaseAllow:
				case <-ctx.Done():
					return ctx.Err()
				}
				return nil
			}
			select {
			case path2Served <- struct{}{}:
			default:
			}
			return nil
		},
		onClose: func(path string) {
			if path != "path1" {
				return
			}
			closeOnce.Do(func() {
				buf := make([]byte, 16<<10)
				buf = buf[:runtime.Stack(buf, false)]
				closeEntered <- string(buf)
				<-releaseClose
				close(closeReturned)
			})
		},
	}
	httpfilter.Register(fb)
	t.Cleanup(func() { httpfilter.UnregisterForTesting(fb.typeURL) })

	managementServer, nodeID, bootstrapContents, xdsResolver := setup.ManagementServerAndResolver(t)

	stub := &stubserver.StubServer{
		EmptyCallF: func(context.Context, *testpb.Empty) (*testpb.Empty, error) {
			handlerOnce.Do(func() { close(handlerEntered) })
			return &testpb.Empty{}, nil
		},
		UnaryCallF: func(context.Context, *testpb.SimpleRequest) (*testpb.SimpleResponse, error) {
			return &testpb.SimpleResponse{}, nil
		},
	}
	var err error
	if stub.S, err = xds.NewGRPCServer(grpc.Creds(insecure.NewCredentials()), testModeChangeServerOption(t), xds.BootstrapContentsForTesting(bootstrapContents)); err != nil {
		t.Fatalf("xds.NewGRPCServer() failed: %v", err)
	}
	lis, err := testutils.LocalTCPListener()
	if err != nil {
		t.Fatal(err)
	}
	stub.Listener = lis
	stubserver.StartTestService(t, stub)
	t.Cleanup(func() { doReleaseClose(); stub.S.Stop() })

	host, p, _ := net.SplitHostPort(lis.Addr().String())
	port64, _ := strconv.ParseUint(p, 10, 32)
	port := uint32(port64)

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
						{Name: "vc1-filter", ConfigType: &v3httppb.HttpFilter_TypedConfig{TypedConfig: vc1TypedStruct(t, fb.typeURL, "path1")}},
						e2e.HTTPFilter("router", &v3routerpb.Router{}),
					},
					RouteSpecifier: &v3httppb.HttpConnectionManager_Rds{Rds: &v3httppb.Rds{
						ConfigSource:    &v3corepb.ConfigSource{ConfigSourceSpecifier: &v3corepb.ConfigSource_Ads{Ads: &v3corepb.AggregatedConfigSource{}}},
						RouteConfigName: "vc1RouteName",
					}},
				})},
			}},
		}},
	})
	resources.Routes = []*v3routepb.RouteConfiguration{clientRoute, vc1RouteConfig(t, fb.typeURL, "")}

	ctx, cancel := context.WithTimeout(context.Background(), defaultTestTimeout)
	defer cancel()
	if err := managementServer.Update(ctx, resources); err != nil {
		t.Fatal(err)
	}
	cc, err := grpc.NewClient(fmt.Sprintf("xds:///%s", serviceName), grpc.WithTransportCredentials(insecure.NewCredentials()), grpc.WithResolvers(xdsResolver))
	if err != nil {
		t.Fatal(err)
	}
	defer cc.Close()
	client := testgrpc.NewTestServiceClient(cc)

	// 1. Unary RPC enters AllowRPC of the original interceptor and is held.
	heldDone := make(chan error, 1)
	go func() {
		_, err := client.EmptyCall(ctx, &testpb.Empty{}, grpc.WaitForReady(true))
		heldDone <- err
	}()
	select {
	case <-allowEntered:
		t.Logf("[%s] EmptyCall is inside AllowRPC(path1) of the original route configuration", ts())
	case <-ctx.Done():
		t.Fatal("timeout waiting for AllowRPC(path1)")
	}

	// 2. Replace the route configuration and wait until it is serving.
	resources.Routes = []*v3routepb.RouteConfiguration{clientRoute, vc1RouteConfig(t, fb.typeURL, "path2")}
	if err := managementServer.Update(ctx, resources); err != nil {
		t.Fatal(err)
	}
	for served := false; !served; {
		pctx, pcancel := context.WithTimeout(ctx, 300*time.Millisecond)
		client.UnaryCall(pctx, &testpb.SimpleRequest{})
		pcancel()
		select {
		case <-path2Served:
			served = true
		case <-ctx.Done():
			t.Fatal("replacement route configuration never served")
		default:
		}
	}
	time.Sleep(300 * time.Millisecond) // Let the updater finish retiring the old configuration.
	t.Logf("[%s] replacement route configuration (path2) is serving; old configuration retired while EmptyCall still in AllowRPC", ts())
	select {
	case <-closeEntered:
		t.Fatalf("retired interceptor closed while an RPC was still inside its AllowRPC")
	default:
	}

	// 3. Admit the held RPC: AllowRPC returns nil.
	close(releaseAllow)
	t.Logf("[%s] AllowRPC(path1) released (returns nil => RPC admitted)", ts())

	select {
	case stack := <-closeEntered:
		t.Logf("[%s] retired interceptor Close(path1) entered and is now blocked. Goroutine stack of the Close caller:\n%s", ts(), stack)
	case <-time.After(5 * time.Second):
		t.Fatal("retired interceptor Close was not called within 5s of the last user finishing")
	}

	// 4. While Close is blocked, does the service handler run?
	const hold = 3 * time.Second
	handlerRanWhileCloseBlocked := false
	select {
	case <-handlerEntered:
		handlerRanWhileCloseBlocked = true
		t.Logf("[%s] service handler ran while retired interceptor Close was still blocked", ts())
	case err := <-heldDone:
		t.Fatalf("[%s] held RPC finished without handler signal: %v", ts(), err)
	case <-time.After(hold):
		t.Logf("[%s] service handler has NOT run %v after admission; retired interceptor Close still blocked", ts(), hold)
	}

	// 5. Unblock Close and observe the handler/RPC.
	doReleaseClose()
	<-closeReturned
	t.Logf("[%s] retired interceptor Close(path1) unblocked and returned", ts())
	select {
	case <-handlerEntered:
		t.Logf("[%s] service handler entered", ts())
	case <-time.After(5 * time.Second):
		t.Fatal("handler never ran")
	}
	select {
	case err := <-heldDone:
		t.Logf("[%s] held EmptyCall completed, err=%v", ts(), err)
	case <-time.After(5 * time.Second):
		t.Fatal("held RPC never completed")
	}
	if !handlerRanWhileCloseBlocked {
		t.Errorf("PROBLEM PRESENT: the admitted unary RPC's service handler did not run until the retired interceptor's blocked Close returned")
	}
}
