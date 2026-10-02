// Run: cp verify/repro/verify_c2_c3_routing_lock_test.go test/xds/ && go test -v -run '^Test$/^Verify_(C2|C3)_' ./test/xds -race -count=1   (in a checkout of evalon/grpc-go-xd-423f7ef0; a FAIL is the repro)

/*
 * Behavioral-audit repro for claims C2 and C3 (run v-389b3566).
 *
 * Both tests drive a real xds.NewGRPCServer through a real xDS management
 * server and a real gRPC client, with a custom server-side HTTP filter whose
 * interceptor behaviour is controlled by the test:
 *
 *   - Verify_C2_*: AllowRPC waits only for its RPC context to be cancelled,
 *     Close returns immediately, and Stop() is called once.
 *   - Verify_C3_*: the retired interceptor's Close is held on a channel while
 *     an RPC is issued against the already-published replacement route.
 *
 * Each test FAILS when the suspected problem is present and PASSES when it is
 * not, and prints the relevant goroutine stacks while the wait is in progress.
 */

package xds_test

import (
	"bytes"
	"context"
	"fmt"
	"net"
	"os"
	"runtime/pprof"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
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

// Bounded observation of Stop() (C2) and bounded deadline of the replacement
// RPC (C3). Override with e.g. VERIFY_WINDOW=20s to show the wait is not merely
// slow.
var (
	verifyObserveWindow = verifyWindow(5 * time.Second)
	verifyRPCDeadline   = verifyWindow(3 * time.Second)
)

func verifyWindow(def time.Duration) time.Duration {
	if d, err := time.ParseDuration(os.Getenv("VERIFY_WINDOW")); err == nil && d > 0 {
		return d
	}
	return def
}

const (
	verifyTestTimeout = 120 * time.Second
	verifyFilterName  = "verify-filter"
	verifyRouteName   = "routeName"
	verifyPathField   = "path"
)

type verifyFilterCfg struct {
	httpfilter.FilterConfig
	path string
}

func verifyParseCfg(cfg proto.Message) (httpfilter.FilterConfig, error) {
	ts, ok := cfg.(*v3xdsxdstypepb.TypedStruct)
	if !ok {
		return nil, fmt.Errorf("unsupported filter config type: %T", cfg)
	}
	ret := verifyFilterCfg{}
	if v := ts.GetValue().GetFields()[verifyPathField]; v != nil {
		ret.path = v.GetStringValue()
	}
	return ret, nil
}

// verifyFilterBuilder is a server HTTP filter whose interceptors delegate
// AllowRPC and Close to test-supplied hooks, keyed by the configured path.
type verifyFilterBuilder struct {
	typeURL  string
	allowRPC func(ctx context.Context, path string) error
	onClose  func(path string)
}

func (b *verifyFilterBuilder) IsTerminal() bool   { return false }
func (b *verifyFilterBuilder) TypeURLs() []string { return []string{b.typeURL} }
func (*verifyFilterBuilder) ParseFilterConfig(cfg proto.Message) (httpfilter.FilterConfig, error) {
	return verifyParseCfg(cfg)
}
func (*verifyFilterBuilder) ParseFilterConfigOverride(cfg proto.Message) (httpfilter.FilterConfig, error) {
	return verifyParseCfg(cfg)
}
func (b *verifyFilterBuilder) BuildServerFilter() httpfilter.ServerFilter { return b }
func (b *verifyFilterBuilder) Close()                                     {}
func (b *verifyFilterBuilder) BuildServerInterceptor(config, override httpfilter.FilterConfig) (resolver.ServerInterceptor, error) {
	cfg := config
	if override != nil {
		cfg = override
	}
	c, ok := cfg.(verifyFilterCfg)
	if !ok {
		return nil, fmt.Errorf("unexpected config %T", cfg)
	}
	return &verifyInterceptor{b: b, path: c.path}, nil
}

type verifyInterceptor struct {
	b    *verifyFilterBuilder
	path string
}

func (i *verifyInterceptor) AllowRPC(ctx context.Context) error { return i.b.allowRPC(ctx, i.path) }
func (i *verifyInterceptor) Close()                             { i.b.onClose(i.path) }

func verifyTypedStruct(t *testing.T, typeURL, path string) *anypb.Any {
	return testutils.MarshalAny(t, &v3xdsxdstypepb.TypedStruct{
		TypeUrl: typeURL,
		Value: &structpb.Struct{Fields: map[string]*structpb.Value{
			verifyPathField: {Kind: &structpb.Value_StringValue{StringValue: path}},
		}},
	})
}

// verifyRouteConfig returns a RouteConfiguration with a single catch-all
// route. A non-empty overridePath attaches a per-route filter config override,
// which is what makes the replacement distinguishable from the original.
func verifyRouteConfig(t *testing.T, typeURL, overridePath string) *v3routepb.RouteConfiguration {
	r := &v3routepb.Route{
		Match:  &v3routepb.RouteMatch{PathSpecifier: &v3routepb.RouteMatch_Prefix{Prefix: "/"}},
		Action: &v3routepb.Route_NonForwardingAction{},
	}
	if overridePath != "" {
		r.TypedPerFilterConfig = map[string]*anypb.Any{verifyFilterName: verifyTypedStruct(t, typeURL, overridePath)}
	}
	return &v3routepb.RouteConfiguration{
		Name:         verifyRouteName,
		VirtualHosts: []*v3routepb.VirtualHost{{Domains: []string{"*"}, Routes: []*v3routepb.Route{r}}},
	}
}

// verifyServerListener returns a server Listener with a single catch-all
// filter chain (so that exactly one routing configuration is in play), the
// test filter configured with "path1", and routes fetched over RDS.
func verifyServerListener(t *testing.T, host string, port uint32, typeURL string) *v3listenerpb.Listener {
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
						{Name: verifyFilterName, ConfigType: &v3httppb.HttpFilter_TypedConfig{TypedConfig: verifyTypedStruct(t, typeURL, "path1")}},
						e2e.HTTPFilter("router", &v3routerpb.Router{}),
					},
					RouteSpecifier: &v3httppb.HttpConnectionManager_Rds{Rds: &v3httppb.Rds{
						ConfigSource:    &v3corepb.ConfigSource{ConfigSourceSpecifier: &v3corepb.ConfigSource_Ads{Ads: &v3corepb.AggregatedConfigSource{}}},
						RouteConfigName: verifyRouteName,
					}},
				})},
			}},
		}},
	}
}

// verifyGoroutines returns the stacks of goroutines that involve the xDS
// server routing/cleanup paths or the test interceptor.
func verifyGoroutines() string {
	var buf bytes.Buffer
	pprof.Lookup("goroutine").WriteTo(&buf, 2)
	var out []string
	for _, g := range strings.Split(buf.String(), "\n\n") {
		if strings.Contains(g, "verifyGoroutines") {
			continue
		}
		for _, needle := range []string{
			"xds/server.RouteAndProcess",
			"xds/server.(*routingConfiguration)",
			"xds/server.(*usableRouteConfiguration)",
			"xds/server.(*filterChain)",
			"xds/server.(*filterChainManager)",
			"xds/server.(*listenerWrapper).Close",
			"xds/server.(*listenerWrapper).handleRDSUpdate",
			"grpc.(*Server).stop",
			"verifyInterceptor",
		} {
			if strings.Contains(g, needle) {
				out = append(out, g)
				break
			}
		}
	}
	return strings.Join(out, "\n\n")
}

type verifyEnv struct {
	client    testgrpc.TestServiceClient
	stop      func()
	update    func(*v3routepb.RouteConfiguration)
	ctx       context.Context
	startedAt time.Time
}

func (e *verifyEnv) since() string {
	return fmt.Sprintf("[t+%6.3fs]", time.Since(e.startedAt).Seconds())
}

func verifySetup(t *testing.T, fb *verifyFilterBuilder) *verifyEnv {
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
		DialTarget: serviceName,
		NodeID:     nodeID,
		Host:       host,
		Port:       port,
		SecLevel:   e2e.SecurityLevelNone,
	})
	clientRoute := resources.Routes[0]
	resources.Listeners = append(resources.Listeners, verifyServerListener(t, host, port, fb.typeURL))
	resources.Routes = []*v3routepb.RouteConfiguration{clientRoute, verifyRouteConfig(t, fb.typeURL, "")}

	ctx, cancel := context.WithTimeout(context.Background(), verifyTestTimeout)
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
	return &verifyEnv{
		client: testgrpc.NewTestServiceClient(cc),
		stop:   func() { stopOnce.Do(stopServer) },
		update: func(rc *v3routepb.RouteConfiguration) {
			resources.Routes = []*v3routepb.RouteConfiguration{clientRoute, rc}
			if err := managementServer.Update(ctx, resources); err != nil {
				t.Fatal(err)
			}
		},
		ctx:       ctx,
		startedAt: time.Now(),
	}
}

// C2: AllowRPC waits only for its RPC context to be cancelled; Close returns
// immediately; Stop() is invoked once. Stop() must cancel the RPC context and
// return within verifyObserveWindow.
func (s) TestVerify_C2_StopWithAllowRPCAwaitingContextCancel(t *testing.T) {
	allowEntered := make(chan struct{}, 1)
	rpcCtxDone := make(chan struct{})
	var rpcCtxDoneOnce sync.Once
	var e *verifyEnv
	var mu sync.Mutex
	var closeCalls []string
	fb := &verifyFilterBuilder{
		typeURL: t.Name(),
		allowRPC: func(ctx context.Context, _ string) error {
			allowEntered <- struct{}{}
			<-ctx.Done() // Wait ONLY for the RPC context to be cancelled.
			rpcCtxDoneOnce.Do(func() { close(rpcCtxDone) })
			return ctx.Err()
		},
		onClose: func(path string) { // Returns immediately.
			mu.Lock()
			closeCalls = append(closeCalls, path)
			mu.Unlock()
		},
	}
	e = verifySetup(t, fb)

	// The in-flight RPC. It has no deadline of its own shorter than the test,
	// so the only things that can cancel its server-side context are the
	// server tearing down the transport, or the explicit cancel below.
	rpcCtx, cancelRPC := context.WithCancel(e.ctx)
	defer func() { cancelRPC(); e.stop() }()
	rpcDone := make(chan error, 1)
	go func() {
		_, err := e.client.EmptyCall(rpcCtx, &testpb.Empty{}, grpc.WaitForReady(true))
		rpcDone <- err
	}()
	select {
	case <-allowEntered:
		t.Logf("%s AllowRPC entered; it is now waiting for its RPC context to be cancelled", e.since())
	case <-e.ctx.Done():
		t.Fatal("Timeout waiting for AllowRPC to be invoked")
	}

	stopDone := make(chan struct{})
	stopStart := time.Now()
	t.Logf("%s calling Stop() once", e.since())
	go func() {
		e.stop()
		close(stopDone)
	}()

	stuck := false
	select {
	case <-stopDone:
		t.Logf("%s Stop() returned after %v", e.since(), time.Since(stopStart).Round(time.Millisecond))
	case <-time.After(verifyObserveWindow):
		stuck = true
		ctxCancelled := false
		select {
		case <-rpcCtxDone:
			ctxCancelled = true
		default:
		}
		mu.Lock()
		cc := append([]string(nil), closeCalls...)
		mu.Unlock()
		t.Logf("%s OBSERVATION: Stop() still blocked %v after it was called; RPC context cancelled by server = %v; interceptor Close calls so far = %v", e.since(), verifyObserveWindow, ctxCancelled, cc)
		t.Logf("goroutines on the xDS routing / shutdown paths while Stop() is blocked:\n%s", verifyGoroutines())
	}

	if stuck {
		// Break the cycle from the outside (the client gives up on the RPC) so
		// the test can clean up, and show that this is what releases Stop().
		t.Logf("%s cancelling the RPC from the client side to break the wait cycle", e.since())
		cancelRPC()
		select {
		case <-stopDone:
			t.Logf("%s Stop() returned only after the client cancelled the RPC (%v after Stop() was called)", e.since(), time.Since(stopStart).Round(time.Millisecond))
		case <-time.After(10 * time.Second):
			t.Logf("%s Stop() still blocked 10s after the client cancelled the RPC", e.since())
		}
	}
	select {
	case err := <-rpcDone:
		t.Logf("%s in-flight RPC finished with: %v", e.since(), err)
	case <-time.After(10 * time.Second):
		t.Logf("%s in-flight RPC did not finish", e.since())
	}
	select {
	case <-rpcCtxDone:
		t.Logf("%s server-side RPC context was cancelled", e.since())
	default:
		t.Errorf("server-side RPC context was never cancelled")
	}
	if stuck {
		t.Fatalf("Stop() did not return within %v while AllowRPC was waiting for RPC context cancellation (Close returns immediately)", verifyObserveWindow)
	}
}

// C3: the retired interceptor's Close is held on a channel. Once the
// replacement is published, an RPC with a bounded deadline must be served by
// the replacement while Close is still blocked.
func (s) TestVerify_C3_ReplacementRPCWhileRetiredCloseBlocked(t *testing.T) {
	pathCh := make(chan string, 16)
	closeEntered := make(chan struct{}, 4)
	releaseClose := make(chan struct{})
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(releaseClose) }) }
	defer release()
	fb := &verifyFilterBuilder{
		typeURL: t.Name(),
		allowRPC: func(_ context.Context, path string) error {
			select {
			case pathCh <- path:
			default:
			}
			return nil
		},
		onClose: func(path string) {
			if path != "path1" { // Only the retired (original) interceptor is slow.
				return
			}
			closeEntered <- struct{}{}
			<-releaseClose
		},
	}
	e := verifySetup(t, fb)
	defer func() { release(); e.stop() }()
	drain := func() (paths []string) {
		for {
			select {
			case p := <-pathCh:
				paths = append(paths, p)
			default:
				return paths
			}
		}
	}

	if _, err := e.client.EmptyCall(e.ctx, &testpb.Empty{}, grpc.WaitForReady(true)); err != nil {
		t.Fatalf("EmptyCall() on original route failed: %v", err)
	}
	t.Logf("%s RPC on original route OK, served by interceptor(s) %v", e.since(), drain())

	// Replace the route configuration; the replacement's interceptor is "path2".
	e.update(verifyRouteConfig(t, fb.typeURL, "path2"))
	select {
	case <-closeEntered:
		t.Logf("%s retired interceptor (path1) Close() entered and is now held on a channel", e.since())
	case <-e.ctx.Done():
		t.Fatal("Timeout waiting for the retired interceptor's Close to be called")
	}

	// Issue an RPC with a bounded deadline while Close is held.
	type result struct {
		err     error
		elapsed time.Duration
	}
	resCh := make(chan result, 1)
	go func() {
		rctx, cancel := context.WithTimeout(e.ctx, verifyRPCDeadline)
		defer cancel()
		start := time.Now()
		_, err := e.client.EmptyCall(rctx, &testpb.Empty{})
		resCh <- result{err: err, elapsed: time.Since(start)}
	}()
	var res result
	select {
	case res = <-resCh:
	case <-time.After(verifyRPCDeadline / 2):
		t.Logf("%s RPC still pending %v after it was issued; goroutines on the xDS routing / retirement paths:\n%s", e.since(), verifyRPCDeadline/2, verifyGoroutines())
		res = <-resCh
	}
	served := drain()
	t.Logf("%s OBSERVATION: RPC issued while retired Close is blocked: err = %v, elapsed = %v (deadline %v), served by interceptor(s) %v", e.since(), res.err, res.elapsed.Round(time.Millisecond), verifyRPCDeadline, served)

	// Release Close and show routing proceeds.
	release()
	t.Logf("%s released the retired interceptor's Close()", e.since())
	rctx, cancel := context.WithTimeout(e.ctx, 10*time.Second)
	defer cancel()
	start := time.Now()
	_, err := e.client.EmptyCall(rctx, &testpb.Empty{})
	t.Logf("%s RPC after releasing Close: err = %v, elapsed = %v, served by interceptor(s) %v", e.since(), err, time.Since(start).Round(time.Millisecond), drain())

	if res.err != nil {
		t.Fatalf("RPC on the published replacement route failed while the retired interceptor's Close was blocked: code = %v, err = %v", status.Code(res.err), res.err)
	}
	if len(served) != 1 || served[0] != "path2" {
		t.Fatalf("RPC issued after replacement was served by %v, want [path2]", served)
	}
	if status.Code(err) != codes.OK {
		t.Fatalf("RPC after releasing Close failed: %v", err)
	}
}
