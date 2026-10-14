// Run: copy to test/xds/ on the claim-target branch, then: go test -race -v -count=1 -timeout=120s -run '^Test$/^Verify_C3_NewConnBlockedWhileAllowRPCHeld$' ./test/xds

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
	"google.golang.org/grpc/status"

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

type vc3Cfg struct {
	httpfilter.FilterConfig
	path string
}

type vc3Builder struct {
	typeURL string
	allow   func(ctx context.Context, path string) error
}

func vc3Parse(cfg proto.Message) (httpfilter.FilterConfig, error) {
	ts, ok := cfg.(*v3xdsxdstypepb.TypedStruct)
	if !ok {
		return nil, fmt.Errorf("unsupported filter config type: %T", cfg)
	}
	return vc3Cfg{path: ts.GetValue().GetFields()["path"].GetStringValue()}, nil
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
	return &vc3Interceptor{b: b, path: cfg.(vc3Cfg).path}, nil
}

type vc3Interceptor struct {
	b    *vc3Builder
	path string
}

func (i *vc3Interceptor) AllowRPC(ctx context.Context) error { return i.b.allow(ctx, i.path) }
func (i *vc3Interceptor) Close()                             {}

func vc3Any(t *testing.T, typeURL, path string) *anypb.Any {
	return testutils.MarshalAny(t, &v3xdsxdstypepb.TypedStruct{
		TypeUrl: typeURL,
		Value: &structpb.Struct{Fields: map[string]*structpb.Value{
			"path": {Kind: &structpb.Value_StringValue{StringValue: path}},
		}},
	})
}

func vc3Route(t *testing.T, typeURL, overridePath string) *v3routepb.RouteConfiguration {
	r := &v3routepb.Route{
		Match:  &v3routepb.RouteMatch{PathSpecifier: &v3routepb.RouteMatch_Prefix{Prefix: "/"}},
		Action: &v3routepb.Route_NonForwardingAction{},
	}
	if overridePath != "" {
		r.TypedPerFilterConfig = map[string]*anypb.Any{"vc3-filter": vc3Any(t, typeURL, overridePath)}
	}
	return &v3routepb.RouteConfiguration{
		Name:         "vc3RouteName",
		VirtualHosts: []*v3routepb.VirtualHost{{Domains: []string{"*"}, Routes: []*v3routepb.Route{r}}},
	}
}

// vc3Stacks returns the stacks of all goroutines whose stack contains any of
// the given substrings.
func vc3Stacks(subs ...string) []string {
	buf := make([]byte, 8<<20)
	buf = buf[:runtime.Stack(buf, true)]
	var out []string
	for _, g := range strings.Split(string(buf), "\n\n") {
		for _, s := range subs {
			if strings.Contains(g, s) {
				out = append(out, g)
				break
			}
		}
	}
	return out
}

func (s) TestVerify_C3_NewConnBlockedWhileAllowRPCHeld(t *testing.T) {
	pathCh := make(chan string, 64)
	allowEntered := make(chan struct{})
	releaseAllow := make(chan struct{})
	var allowOnce, releaseOnce sync.Once
	doRelease := func() { releaseOnce.Do(func() { close(releaseAllow) }) }

	fb := &vc3Builder{typeURL: t.Name()}
	fb.allow = func(ctx context.Context, path string) error {
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
		case pathCh <- path:
		default:
		}
		return nil
	}
	httpfilter.Register(fb)
	t.Cleanup(func() { httpfilter.UnregisterForTesting(fb.typeURL) })

	managementServer, nodeID, bootstrapContents, xdsResolver := setup.ManagementServerAndResolver(t)
	lis, stopServer := setupGRPCServer(t, bootstrapContents)
	defer stopServer()
	defer doRelease()

	host, port, err := hostPortFromListener(lis)
	if err != nil {
		t.Fatal(err)
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
						{Name: "vc3-filter", ConfigType: &v3httppb.HttpFilter_TypedConfig{TypedConfig: vc3Any(t, fb.typeURL, "path1")}},
						e2e.HTTPFilter("router", &v3routerpb.Router{}),
					},
					RouteSpecifier: &v3httppb.HttpConnectionManager_Rds{Rds: &v3httppb.Rds{
						ConfigSource:    &v3corepb.ConfigSource{ConfigSourceSpecifier: &v3corepb.ConfigSource_Ads{Ads: &v3corepb.AggregatedConfigSource{}}},
						RouteConfigName: "vc3RouteName",
					}},
				})},
			}},
		}},
	})
	resources.Routes = []*v3routepb.RouteConfiguration{clientRoute, vc3Route(t, fb.typeURL, "")}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
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

	// 1. Existing RPC held inside AllowRPC(path1).
	heldDone := make(chan error, 1)
	go func() {
		_, err := client.EmptyCall(ctx, &testpb.Empty{}, grpc.WaitForReady(true))
		heldDone <- err
	}()
	select {
	case <-allowEntered:
	case <-ctx.Done():
		t.Fatal("timeout waiting for held AllowRPC")
	}
	t.Logf("STEP1 existing RPC is held inside AllowRPC(path1)")

	// 2. RDS replacement path1 -> path2.
	resources.Routes = []*v3routepb.RouteConfiguration{clientRoute, vc3Route(t, fb.typeURL, "path2")}
	if err := managementServer.Update(ctx, resources); err != nil {
		t.Fatal(err)
	}

	// Wait until the replacement is live: an RPC on the EXISTING connection is
	// served by path2 (or 5s pass).
	replacementLive := false
	for end := time.Now().Add(5 * time.Second); time.Now().Before(end) && !replacementLive; {
		pctx, pcancel := context.WithTimeout(ctx, 300*time.Millisecond)
		_, err := client.EmptyCall(pctx, &testpb.Empty{})
		pcancel()
		if err == nil {
			select {
			case p := <-pathCh:
				replacementLive = p == "path2"
			default:
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Logf("STEP2 replacement route (path2) served on EXISTING connection while AllowRPC(path1) held: %v", replacementLive)
	for _, g := range vc3Stacks("handleRDSUpdate") {
		t.Logf("STEP2 goroutine in handleRDSUpdate while AllowRPC(path1) is held:\n%s", g)
	}

	// 3. New connection to the same listener while AllowRPC(path1) is held.
	raw, err := net.DialTimeout("tcp", lis.Addr().String(), 2*time.Second)
	if err != nil {
		t.Fatalf("raw TCP dial failed: %v", err)
	}
	defer raw.Close()
	cc2, err := grpc.NewClient("passthrough:///"+lis.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	defer cc2.Close()
	client2 := testgrpc.NewTestServiceClient(cc2)

	start := time.Now()
	nctx, ncancel := context.WithTimeout(ctx, 3*time.Second)
	_, newErr := client2.EmptyCall(nctx, &testpb.Empty{}, grpc.WaitForReady(true))
	ncancel()
	var newPath string
	select {
	case newPath = <-pathCh:
	default:
	}
	t.Logf("STEP3 new-connection RPC while AllowRPC(path1) held: err=%v code=%v served_by=%q elapsed=%v connState=%v",
		newErr, status.Code(newErr), newPath, time.Since(start).Round(time.Millisecond), cc2.GetState())
	for _, g := range vc3Stacks("handleRDSUpdate", "listenerWrapper).Accept") {
		t.Logf("STEP3 goroutine while new connection pending:\n%s", g)
	}
	blocked := newErr != nil

	// 4. Release AllowRPC(path1); new connection must now be admitted.
	doRelease()
	t.Logf("STEP4 released AllowRPC(path1)")
	select {
	case err := <-heldDone:
		t.Logf("STEP4 held RPC finished: err=%v", err)
	case <-time.After(5 * time.Second):
		t.Errorf("held RPC did not finish after release")
	}
	start = time.Now()
	actx, acancel := context.WithTimeout(ctx, 5*time.Second)
	_, afterErr := client2.EmptyCall(actx, &testpb.Empty{}, grpc.WaitForReady(true))
	acancel()
	newPath = ""
	select {
	case newPath = <-pathCh:
	default:
	}
	t.Logf("STEP4 new-connection RPC after release: err=%v served_by=%q elapsed=%v connState=%v",
		afterErr, newPath, time.Since(start).Round(time.Millisecond), cc2.GetState())
	t.Logf("STEP4 goroutines still in handleRDSUpdate after release: %d", len(vc3Stacks("handleRDSUpdate")))

	if blocked {
		t.Fatalf("C3 REPRODUCED: new connection was not admitted while an existing RPC was held in AllowRPC during an RDS replacement (err=%v); admitted after release (err=%v)", newErr, afterErr)
	}
	t.Logf("C3 NOT REPRODUCED: new connection admitted and served while AllowRPC(path1) was held")
}
