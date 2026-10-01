// Run: cp verify/repro/_verify_e2e_probe_test.go test/xds/verify_e2e_probe_test.go && go test -race -v -count=1 ./test/xds -run '^Test$/^Verify_'   (works on the audited branch and on every claim-target branch; FAIL lines starting with PROBLEM REPRODUCED name the behavior; remove the copy afterwards. The leading underscore only keeps this file out of `go build ./...` while it sits under verify/.)

package xds_test

import (
	"context"
	"fmt"
	"net"
	"os"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/connectivity"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/internal/envconfig"
	"google.golang.org/grpc/internal/resolver"
	"google.golang.org/grpc/internal/testutils"
	"google.golang.org/grpc/internal/testutils/xds/e2e"
	"google.golang.org/grpc/internal/testutils/xds/e2e/setup"
	"google.golang.org/grpc/internal/xds/httpfilter"
	"google.golang.org/grpc/status"
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

// ---------------------------------------------------------------------------
// Probe filter: records every filter/interceptor lifecycle event in order.
// ---------------------------------------------------------------------------

type vpCfg struct {
	httpfilter.FilterConfig
	mode string
}

func vpParse(cfg proto.Message) (httpfilter.FilterConfig, error) {
	ts, ok := cfg.(*v3xdsxdstypepb.TypedStruct)
	if !ok {
		return nil, fmt.Errorf("unsupported filter config type: %T", cfg)
	}
	ret := vpCfg{}
	if v := ts.GetValue().GetFields()["mode"]; v != nil {
		ret.mode = v.GetStringValue()
	}
	return ret, nil
}

type vpBuilder struct {
	httpfilter.Builder
	typeURL string

	mu     sync.Mutex
	events []string

	nf atomic.Int32

	// gateArmed makes the next AllowRPC call on a "gate"/"ctxwait" interceptor
	// pause. Other calls pass straight through.
	gateArmed atomic.Bool
	entered   chan string   // receives the id of the interceptor that paused
	release   chan struct{} // closed to release a paused "gate" interceptor
}

func newVPBuilder(typeURL string) *vpBuilder {
	return &vpBuilder{typeURL: typeURL, entered: make(chan string, 4), release: make(chan struct{})}
}

func (b *vpBuilder) record(format string, args ...any) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.events = append(b.events, fmt.Sprintf(format, args...))
}

func (b *vpBuilder) snapshot() []string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]string(nil), b.events...)
}

func (b *vpBuilder) count(prefix string) int {
	n := 0
	for _, e := range b.snapshot() {
		if strings.HasPrefix(e, prefix) {
			n++
		}
	}
	return n
}

func (b *vpBuilder) IsTerminal() bool   { return false }
func (b *vpBuilder) TypeURLs() []string { return []string{b.typeURL} }
func (*vpBuilder) ParseFilterConfig(cfg proto.Message) (httpfilter.FilterConfig, error) {
	return vpParse(cfg)
}
func (*vpBuilder) ParseFilterConfigOverride(cfg proto.Message) (httpfilter.FilterConfig, error) {
	return vpParse(cfg)
}
func (b *vpBuilder) BuildServerFilter() httpfilter.ServerFilter {
	f := &vpFilter{b: b, id: fmt.Sprintf("F%d", b.nf.Add(1))}
	b.record("filter-build %s", f.id)
	return f
}

type vpFilter struct {
	b      *vpBuilder
	id     string
	ni     atomic.Int32
	closed atomic.Bool
}

func (f *vpFilter) BuildServerInterceptor(config, override httpfilter.FilterConfig) (resolver.ServerInterceptor, error) {
	cfg, _ := config.(vpCfg)
	if o, ok := override.(vpCfg); ok {
		cfg = o
	}
	i := &vpInterceptor{f: f, mode: cfg.mode, id: fmt.Sprintf("%s/I%d(%s)", f.id, f.ni.Add(1), cfg.mode)}
	f.b.record("icpt-build %s filterAlreadyClosed=%v", i.id, f.closed.Load())
	return i, nil
}

func (f *vpFilter) Close() {
	f.closed.Store(true)
	f.b.record("filter-close %s", f.id)
}

type vpInterceptor struct {
	f      *vpFilter
	id     string
	mode   string
	closed atomic.Bool
}

func (i *vpInterceptor) AllowRPC(ctx context.Context) error {
	i.f.b.record("allow-rpc %s interceptorAlreadyClosed=%v filterAlreadyClosed=%v", i.id, i.closed.Load(), i.f.closed.Load())
	switch i.mode {
	case "gate":
		if i.f.b.gateArmed.CompareAndSwap(true, false) {
			i.f.b.entered <- i.id
			select {
			case <-i.f.b.release:
			case <-ctx.Done():
			}
			i.f.b.record("allow-rpc-resume %s", i.id)
		}
	case "ctxwait":
		if i.f.b.gateArmed.CompareAndSwap(true, false) {
			i.f.b.entered <- i.id
			<-ctx.Done()
			i.f.b.record("allow-rpc-ctx-done %s err=%v", i.id, ctx.Err())
			return ctx.Err()
		}
	}
	return nil
}

func (i *vpInterceptor) Close() {
	i.closed.Store(true)
	i.f.b.record("icpt-close %s", i.id)
}

func vpHTTPFilter(t *testing.T, name, typeURL, mode string) *v3httppb.HttpFilter {
	return &v3httppb.HttpFilter{
		Name: name,
		ConfigType: &v3httppb.HttpFilter_TypedConfig{
			TypedConfig: testutils.MarshalAny(t, &v3xdsxdstypepb.TypedStruct{
				TypeUrl: typeURL,
				Value: &structpb.Struct{Fields: map[string]*structpb.Value{
					"mode": {Kind: &structpb.Value_StringValue{StringValue: mode}},
				}},
			}),
		},
	}
}

// vpEnv is an xDS-enabled server with ONE filter chain whose HCM points at the
// RDS resource vpRouteName, plus an xDS client channel to it.
type vpEnv struct {
	t          *testing.T
	ctx        context.Context
	mgmt       *e2e.ManagementServer
	resources  e2e.UpdateOptions
	clientRDS  *v3routepb.RouteConfiguration
	client     testgrpc.TestServiceClient
	stopServer func()
}

const vpRouteName = "routeName"

func vpSetup(t *testing.T, ctx context.Context, httpFilters []*v3httppb.HttpFilter, rds *v3routepb.RouteConfiguration) *vpEnv {
	t.Helper()
	managementServer, nodeID, bootstrapContents, xdsResolver := setup.ManagementServerAndResolver(t)

	servingCh := make(chan struct{})
	var once sync.Once
	opt := xds.ServingModeCallback(func(_ net.Addr, args xds.ServingModeChangeArgs) {
		if args.Mode == connectivity.ServingModeServing {
			once.Do(func() { close(servingCh) })
		}
	})
	lis, stopServer := setupGRPCServer(t, bootstrapContents, opt)
	host, port, err := hostPortFromListener(lis)
	if err != nil {
		t.Fatalf("failed to retrieve host/port: %v", err)
	}
	const serviceName = "my-service"
	resources := e2e.DefaultClientResources(e2e.ResourceParams{
		DialTarget: serviceName, NodeID: nodeID, Host: host, Port: port, SecLevel: e2e.SecurityLevelNone,
	})
	resources.Listeners = append(resources.Listeners, &v3listenerpb.Listener{
		Name: fmt.Sprintf(e2e.ServerListenerResourceNameTemplate, net.JoinHostPort(host, fmt.Sprint(port))),
		Address: &v3corepb.Address{Address: &v3corepb.Address_SocketAddress{SocketAddress: &v3corepb.SocketAddress{
			Address: host, PortSpecifier: &v3corepb.SocketAddress_PortValue{PortValue: port}}}},
		FilterChains: []*v3listenerpb.FilterChain{{
			Name: "only-chain",
			Filters: []*v3listenerpb.Filter{{
				Name: "hcm",
				ConfigType: &v3listenerpb.Filter_TypedConfig{
					TypedConfig: testutils.MarshalAny(t, &v3httppb.HttpConnectionManager{
						HttpFilters: append(httpFilters, e2e.HTTPFilter("router", &v3routerpb.Router{})),
						RouteSpecifier: &v3httppb.HttpConnectionManager_Rds{Rds: &v3httppb.Rds{
							ConfigSource:    &v3corepb.ConfigSource{ConfigSourceSpecifier: &v3corepb.ConfigSource_Ads{Ads: &v3corepb.AggregatedConfigSource{}}},
							RouteConfigName: vpRouteName,
						}},
					}),
				},
			}},
		}},
	})
	env := &vpEnv{t: t, ctx: ctx, mgmt: managementServer, clientRDS: resources.Routes[0], stopServer: stopServer}
	resources.Routes = []*v3routepb.RouteConfiguration{env.clientRDS, rds}
	env.resources = resources
	if err := managementServer.Update(ctx, resources); err != nil {
		t.Fatal(err)
	}
	cc, err := grpc.NewClient(fmt.Sprintf("xds:///%s", serviceName), grpc.WithTransportCredentials(insecure.NewCredentials()), grpc.WithResolvers(xdsResolver))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cc.Close() })
	select {
	case <-servingCh:
	case <-ctx.Done():
		t.Fatal("timeout waiting for SERVING mode")
	}
	env.client = testgrpc.NewTestServiceClient(cc)
	if _, err := env.client.EmptyCall(ctx, &testpb.Empty{}, grpc.WaitForReady(true)); err != nil {
		t.Fatalf("initial EmptyCall failed: %v", err)
	}
	return env
}

func (e *vpEnv) pushRDS(rds *v3routepb.RouteConfiguration) {
	e.t.Helper()
	e.resources.Routes = []*v3routepb.RouteConfiguration{e.clientRDS, rds}
	if err := e.mgmt.Update(e.ctx, e.resources); err != nil {
		e.t.Fatal(err)
	}
}

// vpRDS builds a server-side RouteConfiguration with n catch-all style routes.
func vpRDS(n int, vhOverride map[string]*anypb.Any) *v3routepb.RouteConfiguration {
	var routes []*v3routepb.Route
	for i := 0; i < n-1; i++ {
		routes = append(routes, &v3routepb.Route{
			Name:   fmt.Sprintf("extra-%d", i),
			Match:  &v3routepb.RouteMatch{PathSpecifier: &v3routepb.RouteMatch_Prefix{Prefix: fmt.Sprintf("/never-matched-%d/", i)}},
			Action: &v3routepb.Route_NonForwardingAction{},
		})
	}
	routes = append(routes, &v3routepb.Route{
		Name:   "catch-all",
		Match:  &v3routepb.RouteMatch{PathSpecifier: &v3routepb.RouteMatch_Prefix{Prefix: "/"}},
		Action: &v3routepb.Route_NonForwardingAction{},
	})
	return &v3routepb.RouteConfiguration{
		Name:         vpRouteName,
		VirtualHosts: []*v3routepb.VirtualHost{{Domains: []string{"*"}, Routes: routes, TypedPerFilterConfig: vhOverride}},
	}
}

// vpWait polls cond every 10ms for at most d; reports whether it became true.
func vpWait(d time.Duration, cond func() bool) bool {
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if cond() {
			return true
		}
		time.Sleep(10 * time.Millisecond)
	}
	return cond()
}

func vpDump(t *testing.T, title string, events []string) {
	t.Helper()
	t.Logf("---- %s (%d events) ----", title, len(events))
	for i, e := range events {
		t.Logf("  #%02d %s", i, e)
	}
}

// ---------------------------------------------------------------------------
// C3 / C8: an RPC is paused inside the FIRST of two interceptors while a
// same-name RDS replacement is delivered; is the SECOND interceptor invoked
// after it was closed?
// ---------------------------------------------------------------------------
func (s) TestVerify_PausedRPC_SameNameRDSReplacement(t *testing.T) {
	b := newVPBuilder(t.Name())
	httpfilter.Register(b)
	defer httpfilter.UnregisterForTesting(b.typeURL)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	env := vpSetup(t, ctx, []*v3httppb.HttpFilter{
		vpHTTPFilter(t, "first", b.typeURL, "gate"),
		vpHTTPFilter(t, "second", b.typeURL, "plain"),
	}, vpRDS(1, nil))
	defer env.stopServer()

	buildsBefore := b.count("icpt-build")

	// Start an RPC and pause it inside the first interceptor.
	b.gateArmed.Store(true)
	rpcErr := make(chan error, 1)
	go func() {
		_, err := env.client.EmptyCall(ctx, &testpb.Empty{})
		rpcErr <- err
	}()
	var pausedIn string
	select {
	case pausedIn = <-b.entered:
	case <-ctx.Done():
		t.Fatal("RPC never reached the first interceptor")
	}
	t.Logf("RPC paused inside interceptor %s", pausedIn)

	// Same-name RDS replacement while the RPC is paused.
	env.pushRDS(vpRDS(2, nil))
	if !vpWait(10*time.Second, func() bool { return b.count("icpt-build") > buildsBefore }) {
		t.Fatal("replacement route configuration was never constructed")
	}
	// Give retirement a bounded window to close the old interceptors.
	closedWhilePaused := vpWait(3*time.Second, func() bool { return b.count("icpt-close") >= 2 })
	vpDump(t, "events while the RPC is still paused in the first interceptor", b.snapshot())
	t.Logf("OBSERVATION retirement mechanism: old interceptors closed while RPC paused = %v", closedWhilePaused)

	// Resume the RPC.
	close(b.release)
	select {
	case err := <-rpcErr:
		t.Logf("paused RPC finished with err=%v", err)
	case <-ctx.Done():
		t.Fatal("paused RPC never finished")
	}
	vpWait(2*time.Second, func() bool { return b.count("icpt-close") >= 2 })
	events := b.snapshot()
	vpDump(t, "events after the RPC resumed", events)

	invokedAfterClose := false
	for _, e := range events {
		if strings.HasPrefix(e, "allow-rpc ") && strings.Contains(e, "interceptorAlreadyClosed=true") {
			invokedAfterClose = true
			t.Logf("OBSERVATION active traversal trigger: %s", e)
		}
	}
	if invokedAfterClose {
		t.Errorf("PROBLEM REPRODUCED: an interceptor's AllowRPC was invoked after its Close()")
	} else {
		t.Logf("OBSERVATION: no AllowRPC call was made on a closed interceptor")
	}
}

// ---------------------------------------------------------------------------
// C4 / C9 / C7: disable a filter on all routes via RDS, then re-enable it.
// Records (a) whether the disable closes the server filter, (b) the relative
// order of filter-close and retired interceptor close, and (c) whether the
// re-enabled configuration builds interceptors from an already-closed filter.
// ---------------------------------------------------------------------------
func (s) TestVerify_DisableThenReenableFilter(t *testing.T) {
	// The `disabled` bit of envoy.config.route.v3.FilterConfig is only honoured
	// when GRPC_EXPERIMENTAL_XDS_EXT_PROC_ON_CLIENT is set (same switch the
	// repo's own TestServerSideXDS_FilterOverride_Disabled and the eval fixture
	// flip).
	testutils.SetEnvConfig(t, &envconfig.XDSClientExtProcEnabled, true)
	vpDisableThenReenable(t, true)
}

// Same sequence, but the filter's last reference is dropped by an RDS update
// that carries no virtual hosts at all (no experimental switch involved).
func (s) TestVerify_EmptyRouteConfigThenRestore(t *testing.T) {
	vpDisableThenReenable(t, false)
}

func vpDisableThenReenable(t *testing.T, viaDisabledOverride bool) {
	b := newVPBuilder(t.Name())
	httpfilter.Register(b)
	defer httpfilter.UnregisterForTesting(b.typeURL)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	const filterName = "probe"
	env := vpSetup(t, ctx, []*v3httppb.HttpFilter{vpHTTPFilter(t, filterName, b.typeURL, "plain")}, vpRDS(1, nil))
	defer env.stopServer()
	vpDump(t, "after initial configuration + 1 RPC", b.snapshot())

	var ok bool
	if viaDisabledOverride {
		// Disable on all routes (virtual-host level override).
		disabled := map[string]*anypb.Any{filterName: testutils.MarshalAny(t, &v3routepb.FilterConfig{Disabled: true})}
		env.pushRDS(vpRDS(1, disabled))
		// The disabled configuration is live once an RPC succeeds without
		// reaching the probe interceptor and the retired interceptor is closed.
		ok = vpWait(10*time.Second, func() bool {
			before := b.count("allow-rpc ")
			if _, err := env.client.EmptyCall(ctx, &testpb.Empty{}); err != nil {
				return false
			}
			return b.count("allow-rpc ") == before && b.count("icpt-close") >= 1
		})
	} else {
		env.pushRDS(&v3routepb.RouteConfiguration{Name: vpRouteName})
		// The empty configuration is live once RPCs are rejected for lack of a
		// virtual host and the retired interceptor is closed.
		ok = vpWait(10*time.Second, func() bool {
			_, err := env.client.EmptyCall(ctx, &testpb.Empty{})
			return status.Code(err) == codes.Unavailable && b.count("icpt-close") >= 1
		})
	}
	if !ok {
		vpDump(t, "events", b.snapshot())
		t.Fatal("replacement configuration never took effect / retired interceptor never closed")
	}
	// Bounded settle window for a filter-close (it may legitimately never come).
	filterClosedOnDisable := vpWait(2*time.Second, func() bool { return b.count("filter-close") >= 1 })
	afterDisable := b.snapshot()
	vpDump(t, "after the RDS update that removes the filter from every route", afterDisable)
	t.Logf("OBSERVATION disable transition closed the server filter = %v", filterClosedOnDisable)
	idxFilterClose, idxIcptClose := -1, -1
	for i, e := range afterDisable {
		if strings.HasPrefix(e, "filter-close") && idxFilterClose < 0 {
			idxFilterClose = i
		}
		if strings.HasPrefix(e, "icpt-close") && idxIcptClose < 0 {
			idxIcptClose = i
		}
	}
	orderViolation := idxFilterClose >= 0 && idxFilterClose < idxIcptClose
	t.Logf("OBSERVATION ordering: first filter-close at #%d, first retired icpt-close at #%d, filterDestroyedBeforeInterceptorClosed=%v", idxFilterClose, idxIcptClose, orderViolation)

	// Re-enable.
	buildsBefore := b.count("icpt-build")
	env.pushRDS(vpRDS(2, nil))
	if !vpWait(10*time.Second, func() bool { return b.count("icpt-build") > buildsBefore }) {
		t.Fatal("re-enabled configuration was never constructed")
	}
	ok = vpWait(10*time.Second, func() bool {
		before := b.count("allow-rpc ")
		if _, err := env.client.EmptyCall(ctx, &testpb.Empty{}); err != nil {
			return false
		}
		return b.count("allow-rpc ") > before
	})
	if !ok {
		t.Fatal("re-enabled configuration never served an RPC through the probe filter")
	}
	events := b.snapshot()
	vpDump(t, "after the RDS update re-enabling the filter + 1 RPC", events)

	builtFromClosed := false
	for _, e := range events[len(afterDisable):] {
		if strings.HasPrefix(e, "icpt-build") && strings.Contains(e, "filterAlreadyClosed=true") {
			builtFromClosed = true
			t.Logf("OBSERVATION reactivation: %s", e)
		}
	}
	t.Logf("OBSERVATION filters built in total = %d", b.count("filter-build"))

	env.stopServer()
	vpDump(t, "after server stop", b.snapshot())

	if orderViolation {
		t.Errorf("PROBLEM REPRODUCED (ordering): server filter was closed before the retired interceptor that depends on it")
	}
	if builtFromClosed {
		t.Errorf("PROBLEM REPRODUCED (closed filter reuse): the re-enabled configuration built interceptors from a server filter that had already been closed")
	}
}

// ---------------------------------------------------------------------------
// C5: an interceptor waits for its RPC context to be cancelled; the server is
// then force-stopped (Stop, not GracefulStop). Does Stop return?
// ---------------------------------------------------------------------------
func (s) TestVerify_ForcedStopWithContextWaitingInterceptor(t *testing.T) {
	b := newVPBuilder(t.Name())
	httpfilter.Register(b)
	defer httpfilter.UnregisterForTesting(b.typeURL)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	env := vpSetup(t, ctx, []*v3httppb.HttpFilter{vpHTTPFilter(t, "probe", b.typeURL, "ctxwait")}, vpRDS(1, nil))

	// The client RPC has no deadline of its own shorter than the test: only
	// the server side can end it.
	rpcCtx, rpcCancel := context.WithCancel(ctx)
	defer rpcCancel()
	b.gateArmed.Store(true)
	rpcErr := make(chan error, 1)
	go func() {
		_, err := env.client.EmptyCall(rpcCtx, &testpb.Empty{})
		rpcErr <- err
	}()
	select {
	case id := <-b.entered:
		t.Logf("RPC is inside interceptor %s, waiting for ctx.Done()", id)
	case <-ctx.Done():
		t.Fatal("RPC never reached the interceptor")
	}

	stopDone := make(chan struct{})
	start := time.Now()
	go func() {
		env.stopServer() // xds.GRPCServer.Stop(): forced shutdown.
		close(stopDone)
	}()

	const externalDeadline = 10 * time.Second
	select {
	case <-stopDone:
		t.Logf("OBSERVATION: forced Stop() returned after %v", time.Since(start).Round(time.Millisecond))
		select {
		case err := <-rpcErr:
			t.Logf("client RPC ended with err=%v", err)
		case <-ctx.Done():
			t.Fatal("client RPC never ended")
		}
		vpDump(t, "events", b.snapshot())
		return
	case <-time.After(externalDeadline):
	}

	t.Errorf("PROBLEM REPRODUCED: forced Stop() still blocked after external deadline of %v", externalDeadline)
	buf := make([]byte, 4<<20)
	buf = buf[:runtime.Stack(buf, true)]
	if p := os.Getenv("VERIFY_STACK_FILE"); p != "" {
		os.WriteFile(p, buf, 0o644)
	}
	for _, g := range strings.Split(string(buf), "\n\n") {
		if strings.Contains(g, "RouteAndProcess") || strings.Contains(g, "listenerWrapper).Close") || strings.Contains(g, "filterChain).stop") {
			t.Logf("relevant goroutine:\n%s", g)
		}
	}
	vpDump(t, "events while Stop() is blocked", b.snapshot())

	// Break the cycle from the outside: cancel the client RPC so the stream
	// context is cancelled by RST_STREAM rather than by server shutdown.
	rpcCancel()
	select {
	case <-stopDone:
		t.Logf("OBSERVATION: Stop() returned %v after start, only once the CLIENT cancelled the RPC", time.Since(start).Round(time.Millisecond))
	case <-time.After(10 * time.Second):
		t.Errorf("Stop() still blocked 10s after the client cancelled the RPC")
	}
	vpDump(t, "final events", b.snapshot())
}
