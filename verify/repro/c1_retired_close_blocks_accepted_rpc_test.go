// Expected on the audited branch: VerifyC1_RetiredCloseBlocked FAILS with "STALL", VerifyC1_RetiredCloseNotBlocked passes.
// Run: cp verify/repro/c1_retired_close_blocks_accepted_rpc_test.go test/xds/ && go test -race -v -count=1 -run '^Test$/^VerifyC1_' ./test/xds   (on branch evalon/grpc-go-xd-03f6ebcd)

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
	"google.golang.org/grpc/metadata"

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

func vc1ServerListener(t *testing.T, host string, port uint32, typeURL string) *v3listenerpb.Listener {
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
						{Name: "vc1-filter", ConfigType: &v3httppb.HttpFilter_TypedConfig{TypedConfig: vc1TypedStruct(t, typeURL, "path1")}},
						e2e.HTTPFilter("router", &v3routerpb.Router{}),
					},
					RouteSpecifier: &v3httppb.HttpConnectionManager_Rds{Rds: &v3httppb.Rds{
						ConfigSource:    &v3corepb.ConfigSource{ConfigSourceSpecifier: &v3corepb.ConfigSource_Ads{Ads: &v3corepb.AggregatedConfigSource{}}},
						RouteConfigName: "vc1RouteName",
					}},
				})},
			}},
		}},
	}
}

// vc1Run drives one accepted unary RPC (tagged "held") through AllowRPC on the
// original route configuration, replaces the route configuration while that
// RPC is still inside AllowRPC, then lets AllowRPC return while the retired
// interceptor's Close is (blockClose=true) or is not (blockClose=false) held.
// It reports whether the application handler chain was entered for the held
// RPC and whether the RPC completed, within 3s of AllowRPC returning.
func vc1Run(t *testing.T, blockClose bool) {
	allowEntered := make(chan struct{})
	releaseAllow := make(chan struct{})
	closeEntered := make(chan string, 1) // carries the stack of the goroutine calling Close(path1)
	releaseClose := make(chan struct{})
	handlerEntered := make(chan struct{})
	path2Served := make(chan struct{}, 16)
	var allowOnce, relAllowOnce, relCloseOnce, handlerOnce sync.Once
	doReleaseAllow := func() { relAllowOnce.Do(func() { close(releaseAllow) }) }
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
			buf := make([]byte, 16<<10)
			buf = buf[:runtime.Stack(buf, false)]
			select {
			case closeEntered <- string(buf):
			default:
			}
			if blockClose {
				<-releaseClose
			}
		},
	}
	httpfilter.Register(fb)
	t.Cleanup(func() { httpfilter.UnregisterForTesting(fb.typeURL) })

	// Runs after xdsUnaryInterceptor in the chain, i.e. it is the "handler"
	// that xdsUnaryInterceptor invokes once RouteAndProcess has returned.
	probe := grpc.ChainUnaryInterceptor(func(ctx context.Context, req any, _ *grpc.UnaryServerInfo, h grpc.UnaryHandler) (any, error) {
		if md, _ := metadata.FromIncomingContext(ctx); len(md.Get("rpc-id")) == 1 && md.Get("rpc-id")[0] == "held" {
			handlerOnce.Do(func() { close(handlerEntered) })
		}
		return h(ctx, req)
	})

	managementServer, nodeID, bootstrapContents, xdsResolver := setup.ManagementServerAndResolver(t)
	lis, stopServer := setupGRPCServer(t, bootstrapContents, probe)
	defer func() { doReleaseAllow(); doReleaseClose(); stopServer() }()

	host, port, err := hostPortFromListener(lis)
	if err != nil {
		t.Fatal(err)
	}
	const serviceName = "my-service"
	resources := e2e.DefaultClientResources(e2e.ResourceParams{DialTarget: serviceName, NodeID: nodeID, Host: host, Port: port, SecLevel: e2e.SecurityLevelNone})
	clientRoute := resources.Routes[0]
	resources.Listeners = append(resources.Listeners, vc1ServerListener(t, host, port, fb.typeURL))
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

	// 1. Accepted RPC enters AllowRPC on the original configuration (path1).
	heldDone := make(chan error, 1)
	go func() {
		_, err := client.EmptyCall(metadata.AppendToOutgoingContext(ctx, "rpc-id", "held"), &testpb.Empty{}, grpc.WaitForReady(true))
		heldDone <- err
	}()
	select {
	case <-allowEntered:
	case <-ctx.Done():
		t.Fatal("timeout waiting for held RPC to enter AllowRPC(path1)")
	}
	t.Logf("step 1: held RPC is inside AllowRPC(path1)")

	// 2. Replace the route configuration and wait until it is published.
	resources.Routes = []*v3routepb.RouteConfiguration{clientRoute, vc1RouteConfig(t, fb.typeURL, "path2")}
	if err := managementServer.Update(ctx, resources); err != nil {
		t.Fatal(err)
	}
	published := false
	for dl := time.Now().Add(5 * time.Second); time.Now().Before(dl) && !published; time.Sleep(20 * time.Millisecond) {
		pctx, pcancel := context.WithTimeout(ctx, 300*time.Millisecond)
		_, err := client.EmptyCall(pctx, &testpb.Empty{})
		pcancel()
		select {
		case <-path2Served:
			published = err == nil
		default:
		}
	}
	if !published {
		t.Fatal("replacement configuration (path2) never served a probe RPC")
	}
	select {
	case <-closeEntered:
		t.Fatal("retired interceptor Close(path1) ran while the held RPC was still inside AllowRPC")
	case <-handlerEntered:
		t.Fatal("handler entered before AllowRPC was released")
	default:
	}
	t.Logf("step 2: replacement (path2) is serving new RPCs; Close(path1) not yet called; held RPC still in AllowRPC")

	// 3. Let AllowRPC return. The RPC is now accepted.
	doReleaseAllow()
	t.Logf("step 3: AllowRPC(path1) released (returns nil => RPC accepted)")

	var stack string
	select {
	case stack = <-closeEntered:
	case <-time.After(3 * time.Second):
		t.Fatal("Close(path1) was not called within 3s of AllowRPC returning")
	}
	var frames []string
	for _, l := range strings.Split(stack, "\n") {
		if strings.HasPrefix(l, "google.golang.org/grpc") || strings.HasPrefix(l, "goroutine ") {
			frames = append(frames, l)
		}
	}
	t.Logf("Close(path1) called from:\n  %s", strings.Join(frames, "\n  "))
	onRPCGoroutine := strings.Contains(stack, "xds.xdsUnaryInterceptor") && strings.Contains(stack, "server.RouteAndProcess")
	t.Logf("OBSERVED Close(path1) runs synchronously under RouteAndProcess/xdsUnaryInterceptor: %v", onRPCGoroutine)

	handler, completed := false, false
	timeout := time.After(3 * time.Second)
wait:
	for !completed {
		select {
		case <-handlerEntered:
			handler = true
			handlerEntered = nil
		case err := <-heldDone:
			completed = true
			t.Logf("held RPC completed: err=%v", err)
			select {
			case <-handlerEntered:
				handler = true
			default:
			}
		case <-timeout:
			break wait
		}
	}
	t.Logf("OBSERVED blockClose=%v: within 3s of AllowRPC returning: handlerEntered=%v rpcCompleted=%v", blockClose, handler, completed)

	if blockClose {
		stalled := !completed && !handler
		// 4. Unblock Close: the RPC must now proceed, proving Close was the only thing holding it.
		doReleaseClose()
		select {
		case err := <-heldDone:
			select {
			case <-handlerEntered:
				handler = true
			default:
			}
			t.Logf("OBSERVED after releasing Close(path1): handlerEntered=%v rpcCompleted=true err=%v", handler, err)
		case <-time.After(3 * time.Second):
			t.Logf("OBSERVED after releasing Close(path1): held RPC still not complete after 3s")
		}
		if stalled {
			t.Errorf("STALL: accepted RPC did not enter its handler for 3s after AllowRPC returned, while retired interceptor Close was blocked")
		}
		return
	}
	if !completed || !handler {
		t.Errorf("control: held RPC did not complete with Close unblocked (handler=%v completed=%v)", handler, completed)
	}
}

func (s) TestVerifyC1_RetiredCloseBlocked(t *testing.T)    { vc1Run(t, true) }
func (s) TestVerifyC1_RetiredCloseNotBlocked(t *testing.T) { vc1Run(t, false) }
