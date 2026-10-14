// Run: cp verify/repro/verify_c2_fresh_conn_admission_test.go test/xds/ && go test -race -v -count=1 -timeout=120s -run '^Test$/^Verify_C2_FreshConnAdmissionWhileAllowRPCHeld$' ./test/xds   (on the branch under test; FAILs when a fresh connection is not admitted while an old RPC is held in AllowRPC across an RDS update)

package xds_test

import (
	"context"
	"fmt"
	"io"
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

type vfyC2Cfg struct {
	httpfilter.FilterConfig
	path string
}

func vfyC2Parse(cfg proto.Message) (httpfilter.FilterConfig, error) {
	ts, ok := cfg.(*v3xdsxdstypepb.TypedStruct)
	if !ok {
		return nil, fmt.Errorf("unsupported filter config type: %T", cfg)
	}
	return vfyC2Cfg{path: ts.GetValue().GetFields()["path"].GetStringValue()}, nil
}

type vfyC2Builder struct {
	typeURL  string
	allowRPC func(ctx context.Context, path string) error
	onClose  func(path string)
}

func (b *vfyC2Builder) IsTerminal() bool   { return false }
func (b *vfyC2Builder) TypeURLs() []string { return []string{b.typeURL} }
func (*vfyC2Builder) ParseFilterConfig(cfg proto.Message) (httpfilter.FilterConfig, error) {
	return vfyC2Parse(cfg)
}
func (*vfyC2Builder) ParseFilterConfigOverride(cfg proto.Message) (httpfilter.FilterConfig, error) {
	return vfyC2Parse(cfg)
}
func (b *vfyC2Builder) BuildServerFilter() httpfilter.ServerFilter { return b }
func (b *vfyC2Builder) Close()                                     {}
func (b *vfyC2Builder) BuildServerInterceptor(config, override httpfilter.FilterConfig) (resolver.ServerInterceptor, error) {
	cfg := config
	if override != nil {
		cfg = override
	}
	return &vfyC2Interceptor{b: b, path: cfg.(vfyC2Cfg).path}, nil
}

type vfyC2Interceptor struct {
	b    *vfyC2Builder
	path string
}

func (i *vfyC2Interceptor) AllowRPC(ctx context.Context) error { return i.b.allowRPC(ctx, i.path) }
func (i *vfyC2Interceptor) Close()                             { i.b.onClose(i.path) }

func vfyC2TS(t *testing.T, typeURL, path string) *anypb.Any {
	return testutils.MarshalAny(t, &v3xdsxdstypepb.TypedStruct{
		TypeUrl: typeURL,
		Value: &structpb.Struct{Fields: map[string]*structpb.Value{
			"path": {Kind: &structpb.Value_StringValue{StringValue: path}},
		}},
	})
}

func vfyC2Route(t *testing.T, typeURL, overridePath string) *v3routepb.RouteConfiguration {
	r := &v3routepb.Route{
		Match:  &v3routepb.RouteMatch{PathSpecifier: &v3routepb.RouteMatch_Prefix{Prefix: "/"}},
		Action: &v3routepb.Route_NonForwardingAction{},
	}
	if overridePath != "" {
		r.TypedPerFilterConfig = map[string]*anypb.Any{"vfy-filter": vfyC2TS(t, typeURL, overridePath)}
	}
	return &v3routepb.RouteConfiguration{
		Name:         "vfyRouteName",
		VirtualHosts: []*v3routepb.VirtualHost{{Domains: []string{"*"}, Routes: []*v3routepb.Route{r}}},
	}
}

// vfyC2Stacks returns the stacks of all goroutines whose stack mentions any of
// the given substrings.
func vfyC2Stacks(subs ...string) []string {
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

// vfyC2RawProbe opens a raw TCP connection, sends the HTTP/2 client preface and
// reports whether the server sent any bytes (its SETTINGS frame) within d. A
// server that has admitted the connection starts its HTTP/2 handshake at once.
func vfyC2RawProbe(addr string, d time.Duration) (connected bool, serverBytes int, err error) {
	c, err := net.DialTimeout("tcp", addr, d)
	if err != nil {
		return false, 0, err
	}
	defer c.Close()
	if _, err := io.WriteString(c, "PRI * HTTP/2.0\r\n\r\nSM\r\n\r\n\x00\x00\x00\x04\x00\x00\x00\x00\x00"); err != nil {
		return true, 0, err
	}
	c.SetReadDeadline(time.Now().Add(d))
	n, err := c.Read(make([]byte, 64))
	return true, n, err
}

func (s) TestVerify_C2_FreshConnAdmissionWhileAllowRPCHeld(t *testing.T) {
	start := time.Now()
	logf := func(format string, args ...any) {
		t.Logf("[+%7.3fs] %s", time.Since(start).Seconds(), fmt.Sprintf(format, args...))
	}

	allowEntered := make(chan struct{})
	releaseAllow := make(chan struct{})
	path1Closed := make(chan struct{})
	path2Served := make(chan struct{}, 64)
	var allowOnce, releaseOnce, closeOnce sync.Once
	doRelease := func() { releaseOnce.Do(func() { close(releaseAllow) }) }

	fb := &vfyC2Builder{
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
			if path == "path1" {
				closeOnce.Do(func() { close(path1Closed) })
			}
		},
	}
	httpfilter.Register(fb)
	t.Cleanup(func() { httpfilter.UnregisterForTesting(fb.typeURL) })

	managementServer, nodeID, bootstrapContents, xdsResolver := setup.ManagementServerAndResolver(t)
	lis, stopServer := setupGRPCServer(t, bootstrapContents)
	defer func() {
		doRelease()
		stopServer()
	}()
	host, port, err := hostPortFromListener(lis)
	if err != nil {
		t.Fatal(err)
	}
	addr := net.JoinHostPort(host, strconv.Itoa(int(port)))

	const serviceName = "my-service"
	resources := e2e.DefaultClientResources(e2e.ResourceParams{
		DialTarget: serviceName, NodeID: nodeID, Host: host, Port: port, SecLevel: e2e.SecurityLevelNone,
	})
	clientRoute := resources.Routes[0]
	resources.Listeners = append(resources.Listeners, &v3listenerpb.Listener{
		Name: fmt.Sprintf(e2e.ServerListenerResourceNameTemplate, addr),
		Address: &v3corepb.Address{Address: &v3corepb.Address_SocketAddress{SocketAddress: &v3corepb.SocketAddress{
			Address: host, PortSpecifier: &v3corepb.SocketAddress_PortValue{PortValue: port},
		}}},
		FilterChains: []*v3listenerpb.FilterChain{{
			Name: "catch-all",
			Filters: []*v3listenerpb.Filter{{
				Name: "hcm",
				ConfigType: &v3listenerpb.Filter_TypedConfig{TypedConfig: testutils.MarshalAny(t, &v3httppb.HttpConnectionManager{
					HttpFilters: []*v3httppb.HttpFilter{
						{Name: "vfy-filter", ConfigType: &v3httppb.HttpFilter_TypedConfig{TypedConfig: vfyC2TS(t, fb.typeURL, "path1")}},
						e2e.HTTPFilter("router", &v3routerpb.Router{}),
					},
					RouteSpecifier: &v3httppb.HttpConnectionManager_Rds{Rds: &v3httppb.Rds{
						ConfigSource:    &v3corepb.ConfigSource{ConfigSourceSpecifier: &v3corepb.ConfigSource_Ads{Ads: &v3corepb.AggregatedConfigSource{}}},
						RouteConfigName: "vfyRouteName",
					}},
				})},
			}},
		}},
	})
	resources.Routes = []*v3routepb.RouteConfiguration{clientRoute, vfyC2Route(t, fb.typeURL, "")}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	if err := managementServer.Update(ctx, resources); err != nil {
		t.Fatal(err)
	}

	cc, err := grpc.NewClient("xds:///"+serviceName, grpc.WithTransportCredentials(insecure.NewCredentials()), grpc.WithResolvers(xdsResolver))
	if err != nil {
		t.Fatal(err)
	}
	defer cc.Close()
	client := testgrpc.NewTestServiceClient(cc)

	// 1. Hold an RPC inside AllowRPC of the original route configuration.
	heldDone := make(chan error, 1)
	go func() {
		_, err := client.EmptyCall(ctx, &testpb.Empty{}, grpc.WaitForReady(true))
		heldDone <- err
	}()
	select {
	case <-allowEntered:
		logf("held RPC is inside AllowRPC(path1) on connection #1")
	case <-ctx.Done():
		t.Fatal("timeout waiting for held RPC to enter AllowRPC")
	}

	// Baseline: before any RDS update, a fresh connection is admitted.
	conn, n, perr := vfyC2RawProbe(addr, 2*time.Second)
	logf("BASELINE raw fresh TCP connection before RDS update: connected=%v serverHandshakeBytes=%d err=%v", conn, n, perr)

	// 2. RDS update replacing the route configuration.
	resources.Routes = []*v3routepb.RouteConfiguration{clientRoute, vfyC2Route(t, fb.typeURL, "path2")}
	if err := managementServer.Update(ctx, resources); err != nil {
		t.Fatal(err)
	}
	logf("RDS update (path1 -> path2) sent to management server")

	// Wait until the server has applied it: an RPC on the EXISTING connection
	// is served by the replacement interceptor.
	applied := false
	for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline) && !applied; {
		pctx, pcancel := context.WithTimeout(ctx, 300*time.Millisecond)
		_, err := client.EmptyCall(pctx, &testpb.Empty{})
		pcancel()
		select {
		case <-path2Served:
			applied = err == nil
		default:
		}
		if !applied {
			time.Sleep(20 * time.Millisecond)
		}
	}
	if !applied {
		t.Fatal("replacement route configuration never served an RPC on the existing connection")
	}
	logf("RDS update applied: RPC on EXISTING connection #1 served by replacement interceptor (path2)")
	time.Sleep(200 * time.Millisecond)

	select {
	case <-path1Closed:
		logf("retired interceptor (path1) Close(): ALREADY CALLED while RPC still held")
	default:
		logf("retired interceptor (path1) Close(): NOT yet called while RPC still held")
	}

	// 3. Fresh connections while the original RPC is still held.
	conn, n, perr = vfyC2RawProbe(addr, 2*time.Second)
	logf("HELD raw fresh TCP connection: connected=%v serverHandshakeBytes=%d err=%v", conn, n, perr)
	rawAdmitted := n > 0

	cc2, err := grpc.NewClient("passthrough:///"+addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	defer cc2.Close()
	fresh := testgrpc.NewTestServiceClient(cc2)
	fctx, fcancel := context.WithTimeout(ctx, 3*time.Second)
	_, ferr := fresh.EmptyCall(fctx, &testpb.Empty{}, grpc.WaitForReady(true))
	fcancel()
	logf("HELD RPC over fresh gRPC connection #2 (3s deadline): code=%v err=%v", status.Code(ferr), ferr)

	for _, g := range vfyC2Stacks("listenerWrapper).Accept", "listenerWrapper).handleRDSUpdate", "usableRouteConfiguration).close", "vfyC2Interceptor).AllowRPC") {
		logf("GOROUTINE (while RPC held):\n%s", g)
	}
	select {
	case err := <-heldDone:
		t.Fatalf("held RPC finished prematurely: %v", err)
	default:
		logf("original RPC is still held inside AllowRPC(path1)")
	}

	// 4. Release the held RPC; the fresh connection must now work.
	doRelease()
	logf("held RPC released")
	select {
	case err := <-heldDone:
		logf("held RPC completed: err=%v", err)
	case <-time.After(5 * time.Second):
		t.Error("held RPC did not complete after release")
	}
	select {
	case <-path1Closed:
		logf("retired interceptor (path1) Close() called after release")
	case <-time.After(5 * time.Second):
		t.Error("retired interceptor never closed after release")
	}
	conn, n, perr = vfyC2RawProbe(addr, 2*time.Second)
	logf("RELEASED raw fresh TCP connection: connected=%v serverHandshakeBytes=%d err=%v", conn, n, perr)
	fctx, fcancel = context.WithTimeout(ctx, 3*time.Second)
	_, aerr := fresh.EmptyCall(fctx, &testpb.Empty{}, grpc.WaitForReady(true))
	fcancel()
	logf("RELEASED RPC over fresh gRPC connection #2: code=%v err=%v", status.Code(aerr), aerr)

	if !rawAdmitted || ferr != nil {
		t.Errorf("fresh connection was NOT admitted while the original RPC was held in AllowRPC across an RDS update (raw handshake bytes admitted=%v, fresh RPC err=%v)", rawAdmitted, ferr)
	}
}
