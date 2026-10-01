// Run: cp verify/repro/verify_harness_test.go test/xds/ && go test -tags verify_audit -race -count=1 -v -run '^Test$/^VerifyH_' ./test/xds
//
// Audit harness (run v-1387f992). Black-box lifecycle probes for the xDS server:
// a tracking HTTP filter records, in one ordered log, when the underlying
// ServerFilter and each interceptor are built, invoked and closed. It only
// uses the public xds server plus the e2e management server, so the same file
// runs unmodified on every audited branch.

//go:build verify_audit

package xds_test

import (
	"context"
	"fmt"
	"net"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/connectivity"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/internal/envconfig"
	"google.golang.org/grpc/internal/resolver"
	"google.golang.org/grpc/internal/testutils"
	"google.golang.org/grpc/internal/testutils/xds/e2e"
	"google.golang.org/grpc/internal/testutils/xds/e2e/setup"
	"google.golang.org/grpc/internal/xds/httpfilter"
	"google.golang.org/grpc/xds"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/anypb"
	"google.golang.org/protobuf/types/known/structpb"

	v3xdsxdstypepb "github.com/cncf/xds/go/xds/type/v3"
	v3corepb "github.com/envoyproxy/go-control-plane/envoy/config/core/v3"
	v3listenerpb "github.com/envoyproxy/go-control-plane/envoy/config/listener/v3"
	v3routepb "github.com/envoyproxy/go-control-plane/envoy/config/route/v3"
	v3routerpb "github.com/envoyproxy/go-control-plane/envoy/extensions/filters/http/router/v3"
	v3httppb "github.com/envoyproxy/go-control-plane/envoy/extensions/filters/network/http_connection_manager/v3"
	testgrpc "google.golang.org/grpc/interop/grpc_testing"
	testpb "google.golang.org/grpc/interop/grpc_testing"
)

// ---------------------------------------------------------------- event log

type vfyLog struct {
	mu sync.Mutex
	ev []string
}

func (l *vfyLog) add(format string, args ...any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.ev = append(l.ev, fmt.Sprintf(format, args...))
}

func (l *vfyLog) snapshot() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]string(nil), l.ev...)
}

// index returns the position of the first event containing every substring,
// or -1.
func (l *vfyLog) index(subs ...string) int {
	for i, e := range l.snapshot() {
		ok := true
		for _, s := range subs {
			if !strings.Contains(e, s) {
				ok = false
				break
			}
		}
		if ok {
			return i
		}
	}
	return -1
}

func (l *vfyLog) count(subs ...string) int {
	n := 0
	for _, e := range l.snapshot() {
		ok := true
		for _, s := range subs {
			if !strings.Contains(e, s) {
				ok = false
				break
			}
		}
		if ok {
			n++
		}
	}
	return n
}

func (l *vfyLog) waitFor(d time.Duration, subs ...string) bool {
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if l.index(subs...) >= 0 {
			return true
		}
		time.Sleep(5 * time.Millisecond)
	}
	return l.index(subs...) >= 0
}

func (l *vfyLog) dump(t *testing.T, title string) {
	t.Helper()
	t.Logf("---- event log: %s ----", title)
	for i, e := range l.snapshot() {
		t.Logf("  [%02d] %s", i, e)
	}
}

// ---------------------------------------------------------------- filter

type vfyCfg struct {
	httpfilter.FilterConfig
	path string
}

func vfyParse(cfg proto.Message) (httpfilter.FilterConfig, error) {
	ts, ok := cfg.(*v3xdsxdstypepb.TypedStruct)
	if !ok {
		return nil, fmt.Errorf("unsupported filter config type %T", cfg)
	}
	ret := vfyCfg{}
	if v := ts.GetValue().GetFields()["path"]; v != nil {
		ret.path = v.GetStringValue()
	}
	return ret, nil
}

type vfyBuilder struct {
	name       string
	typeURL    string
	log        *vfyLog
	nFilters   atomic.Int32
	closeDelay time.Duration
	// allowHook, if set, runs inside AllowRPC after the invocation is logged.
	allowHook func(ctx context.Context, ic *vfyInterceptor)
}

func (b *vfyBuilder) TypeURLs() []string { return []string{b.typeURL} }
func (b *vfyBuilder) IsTerminal() bool   { return false }
func (b *vfyBuilder) ParseFilterConfig(cfg proto.Message) (httpfilter.FilterConfig, error) {
	return vfyParse(cfg)
}
func (b *vfyBuilder) ParseFilterConfigOverride(cfg proto.Message) (httpfilter.FilterConfig, error) {
	return vfyParse(cfg)
}
func (b *vfyBuilder) BuildServerFilter() httpfilter.ServerFilter {
	f := &vfyFilter{b: b, id: b.nFilters.Add(1)}
	b.log.add("FILTER %s#%d BUILT (BuildServerFilter)", b.name, f.id)
	return f
}

type vfyFilter struct {
	b      *vfyBuilder
	id     int32
	closed atomic.Bool
	nIcpt  atomic.Int32
}

// vfyVia returns the chain of internal/xds/server functions that led to the
// current call, innermost first (e.g. which function released a filter ref).
func vfyVia() string {
	pcs := make([]uintptr, 32)
	frames := runtime.CallersFrames(pcs[:runtime.Callers(2, pcs)])
	var out []string
	for {
		fr, more := frames.Next()
		const pkg = "google.golang.org/grpc/internal/xds/server."
		if strings.HasPrefix(fr.Function, pkg) && len(out) < 5 {
			out = append(out, strings.TrimPrefix(fr.Function, pkg))
		}
		if !more {
			break
		}
	}
	return strings.Join(out, " < ")
}

func (f *vfyFilter) Close() {
	f.closed.Store(true)
	f.b.log.add("FILTER %s#%d CLOSED (underlying ServerFilter.Close ran) via %s", f.b.name, f.id, vfyVia())
}

func (f *vfyFilter) BuildServerInterceptor(config, override httpfilter.FilterConfig) (resolver.ServerInterceptor, error) {
	var c vfyCfg
	if override != nil {
		c = override.(vfyCfg)
	} else if config != nil {
		c = config.(vfyCfg)
	}
	ic := &vfyInterceptor{f: f, n: f.nIcpt.Add(1), path: c.path}
	f.b.log.add("INTERCEPTOR %s BUILT from FILTER %s#%d filterClosed=%v", ic, f.b.name, f.id, f.closed.Load())
	return ic, nil
}

type vfyInterceptor struct {
	f      *vfyFilter
	n      int32
	path   string
	closed atomic.Bool
}

func (ic *vfyInterceptor) String() string {
	return fmt.Sprintf("%s#%d/i%d[%s]", ic.f.b.name, ic.f.id, ic.n, ic.path)
}

func (ic *vfyInterceptor) AllowRPC(ctx context.Context) error {
	ic.f.b.log.add("INTERCEPTOR %s AllowRPC selfClosed=%v filterClosed=%v", ic, ic.closed.Load(), ic.f.closed.Load())
	if h := ic.f.b.allowHook; h != nil {
		h(ctx, ic)
	}
	return nil
}

func (ic *vfyInterceptor) Close() {
	ic.f.b.log.add("INTERCEPTOR %s CLOSE-START filterClosed=%v via %s", ic, ic.f.closed.Load(), vfyVia())
	time.Sleep(ic.f.b.closeDelay)
	ic.closed.Store(true)
	ic.f.b.log.add("INTERCEPTOR %s CLOSE-END filterClosed=%v", ic, ic.f.closed.Load())
}

// ---------------------------------------------------------------- xDS setup

const vfyRouteName = "vfyRoute"

func vfyHTTPFilter(t *testing.T, name, typeURL, path string) *v3httppb.HttpFilter {
	return &v3httppb.HttpFilter{
		Name: name,
		ConfigType: &v3httppb.HttpFilter_TypedConfig{
			TypedConfig: testutils.MarshalAny(t, &v3xdsxdstypepb.TypedStruct{
				TypeUrl: typeURL,
				Value:   &structpb.Struct{Fields: map[string]*structpb.Value{"path": structpb.NewStringValue(path)}},
			}),
		},
	}
}

// vfyOverride returns a per-route override for one filter: either disabled, or
// enabled with the given path.
func vfyOverride(t *testing.T, typeURL string, disabled bool, path string) *anypb.Any {
	cfg := &v3routepb.FilterConfig{Disabled: disabled}
	if !disabled {
		cfg.Config = testutils.MarshalAny(t, &v3xdsxdstypepb.TypedStruct{
			TypeUrl: typeURL,
			Value:   &structpb.Struct{Fields: map[string]*structpb.Value{"path": structpb.NewStringValue(path)}},
		})
	}
	return testutils.MarshalAny(t, cfg)
}

func vfyRoute(overrides map[string]*anypb.Any) *v3routepb.RouteConfiguration {
	return &v3routepb.RouteConfiguration{
		Name: vfyRouteName,
		VirtualHosts: []*v3routepb.VirtualHost{{
			Domains: []string{"*"},
			Routes: []*v3routepb.Route{{
				Match:                &v3routepb.RouteMatch{PathSpecifier: &v3routepb.RouteMatch_Prefix{Prefix: "/"}},
				Action:               &v3routepb.Route_NonForwardingAction{},
				TypedPerFilterConfig: overrides,
			}},
		}},
	}
}

type vfyEnv struct {
	ctx        context.Context
	ms         *e2e.ManagementServer
	resources  e2e.UpdateOptions
	inboundLis *v3listenerpb.Listener
	clientRt   *v3routepb.RouteConfiguration
	client     testgrpc.TestServiceClient
	stopServer func()
}

func (e *vfyEnv) setRoute(t *testing.T, rc *v3routepb.RouteConfiguration) {
	t.Helper()
	e.resources.Routes = []*v3routepb.RouteConfiguration{e.clientRt, rc}
	if err := e.ms.Update(e.ctx, e.resources); err != nil {
		t.Fatal(err)
	}
}

// vfySetup starts an xDS-enabled server with ONE (default) filter chain using
// the given HTTP filters (router appended) and an RDS route named vfyRoute.
func vfySetup(t *testing.T, filters []*v3httppb.HttpFilter, rc *v3routepb.RouteConfiguration, serverOpts ...grpc.ServerOption) *vfyEnv {
	t.Helper()
	ms, nodeID, bootstrapContents, xdsResolver := setup.ManagementServerAndResolver(t)

	servingCh := make(chan struct{})
	var once sync.Once
	opt := xds.ServingModeCallback(func(_ net.Addr, args xds.ServingModeChangeArgs) {
		if args.Mode == connectivity.ServingModeServing {
			once.Do(func() { close(servingCh) })
		}
	})
	lis, stopServer := setupGRPCServer(t, bootstrapContents, append([]grpc.ServerOption{opt}, serverOpts...)...)
	var stopOnce sync.Once
	stop := func() { stopOnce.Do(stopServer) }
	t.Cleanup(stop)

	host, port, err := hostPortFromListener(lis)
	if err != nil {
		t.Fatal(err)
	}
	const serviceName = "my-service"
	resources := e2e.DefaultClientResources(e2e.ResourceParams{
		DialTarget: serviceName, NodeID: nodeID, Host: host, Port: port, SecLevel: e2e.SecurityLevelNone,
	})
	hcm := &v3httppb.HttpConnectionManager{
		HttpFilters: append(append([]*v3httppb.HttpFilter{}, filters...), e2e.HTTPFilter("router", &v3routerpb.Router{})),
		RouteSpecifier: &v3httppb.HttpConnectionManager_Rds{Rds: &v3httppb.Rds{
			ConfigSource:    &v3corepb.ConfigSource{ConfigSourceSpecifier: &v3corepb.ConfigSource_Ads{Ads: &v3corepb.AggregatedConfigSource{}}},
			RouteConfigName: vfyRouteName,
		}},
	}
	inboundLis := &v3listenerpb.Listener{
		Name: fmt.Sprintf(e2e.ServerListenerResourceNameTemplate, net.JoinHostPort(host, strconv.Itoa(int(port)))),
		Address: &v3corepb.Address{Address: &v3corepb.Address_SocketAddress{SocketAddress: &v3corepb.SocketAddress{
			Address: host, PortSpecifier: &v3corepb.SocketAddress_PortValue{PortValue: port},
		}}},
		DefaultFilterChain: &v3listenerpb.FilterChain{Filters: []*v3listenerpb.Filter{{
			Name:       "hcm",
			ConfigType: &v3listenerpb.Filter_TypedConfig{TypedConfig: testutils.MarshalAny(t, hcm)},
		}}},
	}
	resources.Listeners = append(resources.Listeners, inboundLis)
	clientRt := resources.Routes[0]
	resources.Routes = append(resources.Routes, rc)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)
	if err := ms.Update(ctx, resources); err != nil {
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
		t.Fatal("timeout waiting for SERVING")
	}
	return &vfyEnv{ctx: ctx, ms: ms, resources: resources, inboundLis: inboundLis, clientRt: clientRt, client: testgrpc.NewTestServiceClient(cc), stopServer: stop}
}

func vfyNewBuilder(t *testing.T, name string, log *vfyLog, closeDelay time.Duration) *vfyBuilder {
	b := &vfyBuilder{name: name, typeURL: t.Name() + "/" + name, log: log, closeDelay: closeDelay}
	httpfilter.Register(b)
	t.Cleanup(func() { httpfilter.UnregisterForTesting(b.typeURL) })
	return b
}

// setServerHCMFilters replaces the server-side Listener resource (an LDS
// update) with one whose single default filter chain uses the given filters.
func (e *vfyEnv) setServerHCMFilters(t *testing.T, filters []*v3httppb.HttpFilter) {
	t.Helper()
	hcm := &v3httppb.HttpConnectionManager{
		HttpFilters: append(append([]*v3httppb.HttpFilter{}, filters...), e2e.HTTPFilter("router", &v3routerpb.Router{})),
		RouteSpecifier: &v3httppb.HttpConnectionManager_Rds{Rds: &v3httppb.Rds{
			ConfigSource:    &v3corepb.ConfigSource{ConfigSourceSpecifier: &v3corepb.ConfigSource_Ads{Ads: &v3corepb.AggregatedConfigSource{}}},
			RouteConfigName: vfyRouteName,
		}},
	}
	e.inboundLis.DefaultFilterChain = &v3listenerpb.FilterChain{Filters: []*v3listenerpb.Filter{{
		Name:       "hcm",
		ConfigType: &v3listenerpb.Filter_TypedConfig{TypedConfig: testutils.MarshalAny(t, hcm)},
	}}}
	if err := e.ms.Update(e.ctx, e.resources); err != nil {
		t.Fatal(err)
	}
}

func (e *vfyEnv) rpc(t *testing.T) {
	t.Helper()
	if _, err := e.client.EmptyCall(e.ctx, &testpb.Empty{}); err != nil {
		t.Fatalf("EmptyCall() failed: %v", err)
	}
}

// ---------------------------------------------------------------- probes

// Probe 1 (C1, C9, C5, C11): enabled -> disabled-on-every-route -> re-enabled,
// all through successful RDS updates on one filter chain.
func (s) TestVerifyH_DisableThenReenable(t *testing.T) {
	testutils.SetEnvConfig(t, &envconfig.XDSClientExtProcEnabled, true)
	log := &vfyLog{}
	b := vfyNewBuilder(t, "F", log, 200*time.Millisecond)

	env := vfySetup(t, []*v3httppb.HttpFilter{vfyHTTPFilter(t, "F", b.typeURL, "base")},
		vfyRoute(map[string]*anypb.Any{"F": vfyOverride(t, b.typeURL, false, "gen1")}))
	env.rpc(t)
	if !log.waitFor(5*time.Second, "[gen1] AllowRPC") {
		t.Fatal("gen1 interceptor never invoked")
	}

	// Successful replacement that disables F on every route.
	log.add("TEST: >>> publishing RDS with F disabled on every route")
	env.setRoute(t, vfyRoute(map[string]*anypb.Any{"F": vfyOverride(t, b.typeURL, true, "")}))
	// Wait until RPCs are no longer intercepted (replacement is being served).
	applied := false
	for end := time.Now().Add(5 * time.Second); time.Now().Before(end); time.Sleep(20 * time.Millisecond) {
		before := log.count("AllowRPC")
		env.rpc(t)
		if log.count("AllowRPC") == before {
			applied = true
			break
		}
	}
	if !applied {
		t.Fatal("disabled replacement never took effect")
	}
	log.add("TEST: RPC served by the replacement (F disabled) config")
	gen1Closed := log.waitFor(3*time.Second, "[gen1] CLOSE-END")
	time.Sleep(300 * time.Millisecond)
	log.dump(t, "after disable-on-every-route replacement")

	iFilterClosed := log.index("FILTER F#1 CLOSED")
	iIcptCloseEnd := log.index("[gen1] CLOSE-END")
	switch {
	case !gen1Closed:
		t.Logf("OBSERVATION(C1/C9 replacement): gen1 interceptor was never closed after replacement (filter closed idx=%d)", iFilterClosed)
	case iFilterClosed >= 0 && iFilterClosed < iIcptCloseEnd:
		t.Logf("OBSERVATION(C1/C9 replacement): FILTER-RELEASED-BEFORE-INTERCEPTOR-CLOSE: underlying filter Close at [%02d] precedes retired interceptor CLOSE-END at [%02d]", iFilterClosed, iIcptCloseEnd)
	case iFilterClosed >= 0:
		t.Logf("OBSERVATION(C1/C9 replacement): INTERCEPTOR-CLOSED-BEFORE-FILTER-RELEASE: retired interceptor CLOSE-END at [%02d] precedes underlying filter Close at [%02d]", iIcptCloseEnd, iFilterClosed)
	default:
		t.Logf("OBSERVATION(C1/C9 replacement): FILTER-STILL-LIVE: retired interceptor CLOSE-END at [%02d]; underlying filter F#1 not closed while disabled", iIcptCloseEnd)
	}

	// Successful replacement that re-enables F.
	log.add("TEST: >>> publishing RDS with F re-enabled (gen3)")
	env.setRoute(t, vfyRoute(map[string]*anypb.Any{"F": vfyOverride(t, b.typeURL, false, "gen3")}))
	if !log.waitFor(5*time.Second, "[gen3] BUILT") {
		t.Fatal("gen3 interceptor never built")
	}
	for end := time.Now().Add(5 * time.Second); time.Now().Before(end) && log.index("[gen3] AllowRPC") < 0; time.Sleep(20 * time.Millisecond) {
		env.rpc(t)
	}
	log.dump(t, "after re-enable")
	built := log.snapshot()[log.index("[gen3] BUILT")]
	allow := ""
	if i := log.index("[gen3] AllowRPC"); i >= 0 {
		allow = log.snapshot()[i]
	}
	t.Logf("OBSERVATION(C5/C11 re-enable): BuildServerFilter calls=%d; %q; %q", b.nFilters.Load(), built, allow)
	if strings.Contains(built, "filterClosed=true") {
		t.Logf("OBSERVATION(C5/C11 re-enable): CLOSED-FILTER-REUSED: re-enabled interceptor was built from an already-closed filter instance")
	} else {
		t.Logf("OBSERVATION(C5/C11 re-enable): LIVE-FILTER-USED: re-enabled interceptor was built from a live filter instance")
	}

	env.stopServer()
	log.dump(t, "after server Stop")
}

// Probe 2 (C1): shutdown ordering with the filter enabled.
func (s) TestVerifyH_ShutdownOrder(t *testing.T) {
	testutils.SetEnvConfig(t, &envconfig.XDSClientExtProcEnabled, true)
	log := &vfyLog{}
	b := vfyNewBuilder(t, "F", log, 200*time.Millisecond)
	env := vfySetup(t, []*v3httppb.HttpFilter{vfyHTTPFilter(t, "F", b.typeURL, "base")},
		vfyRoute(map[string]*anypb.Any{"F": vfyOverride(t, b.typeURL, false, "gen1")}))
	env.rpc(t)
	log.add("TEST: >>> stopping server")
	env.stopServer()
	log.waitFor(3*time.Second, "[gen1] CLOSE-END")
	time.Sleep(300 * time.Millisecond)
	log.dump(t, "after server Stop")
	iF, iI := log.index("FILTER F#1 CLOSED"), log.index("[gen1] CLOSE-END")
	switch {
	case iI < 0:
		t.Logf("OBSERVATION(C1 shutdown): interceptor never closed on Stop (filter closed idx=%d)", iF)
	case iF >= 0 && iF < iI:
		t.Logf("OBSERVATION(C1 shutdown): FILTER-RELEASED-BEFORE-INTERCEPTOR-CLOSE: filter Close [%02d] < interceptor CLOSE-END [%02d]", iF, iI)
	case iF >= 0:
		t.Logf("OBSERVATION(C1 shutdown): INTERCEPTOR-CLOSED-BEFORE-FILTER-RELEASE: interceptor CLOSE-END [%02d] < filter Close [%02d]", iI, iF)
	default:
		t.Logf("OBSERVATION(C1 shutdown): filter never closed on Stop; interceptor CLOSE-END [%02d]", iI)
	}
}

// Probe 3 (C4, C10): an admitted request is paused inside the first
// interceptor (filter A) of its route while a normal RDS replacement is
// published; it then resumes and invokes the next interceptor (filter B).
func (s) TestVerifyH_PausedRequestReplacement(t *testing.T) {
	testutils.SetEnvConfig(t, &envconfig.XDSClientExtProcEnabled, true)
	log := &vfyLog{}
	a := vfyNewBuilder(t, "A", log, 0)
	b := vfyNewBuilder(t, "B", log, 0)
	gate := make(chan struct{})
	entered := make(chan struct{}, 1)
	a.allowHook = func(ctx context.Context, ic *vfyInterceptor) {
		if ic.path != "gen1" {
			return
		}
		select {
		case entered <- struct{}{}:
		default:
		}
		select {
		case <-gate:
		case <-ctx.Done():
		}
	}
	ov := func(gen string) map[string]*anypb.Any {
		return map[string]*anypb.Any{"A": vfyOverride(t, a.typeURL, false, gen), "B": vfyOverride(t, b.typeURL, false, gen)}
	}
	env := vfySetup(t, []*v3httppb.HttpFilter{vfyHTTPFilter(t, "A", a.typeURL, "base"), vfyHTTPFilter(t, "B", b.typeURL, "base")}, vfyRoute(ov("gen1")))

	rpcDone := make(chan error, 1)
	go func() {
		_, err := env.client.EmptyCall(env.ctx, &testpb.Empty{})
		rpcDone <- err
	}()
	select {
	case <-entered:
	case <-env.ctx.Done():
		t.Fatal("request never reached interceptor A")
	}
	log.add("TEST: request admitted and paused inside A[gen1].AllowRPC (B[gen1].AllowRPC not yet invoked)")

	log.add("TEST: >>> publishing replacement RDS (gen2) through the management server")
	env.setRoute(t, vfyRoute(ov("gen2")))
	closedWhilePaused := log.waitFor(3*time.Second, "B#1/", "[gen1] CLOSE-END")
	log.add("TEST: B[gen1] Close finished while request still paused = %v; resuming request", closedWhilePaused)
	close(gate)
	select {
	case err := <-rpcDone:
		log.add("TEST: paused RPC returned err=%v", err)
	case <-env.ctx.Done():
		t.Fatal("paused RPC never returned")
	}
	time.Sleep(300 * time.Millisecond)
	log.dump(t, "paused request across replacement")
	if i := log.index("B#1/", "[gen1] AllowRPC selfClosed=true"); i >= 0 {
		t.Logf("OBSERVATION(C4/C10): CLOSED-INTERCEPTOR-INVOKED: %s (after its CLOSE-END at [%02d])", log.snapshot()[i], log.index("B#1/", "[gen1] CLOSE-END"))
	} else if i := log.index("B#1/", "[gen1] AllowRPC selfClosed=false"); i >= 0 {
		t.Logf("OBSERVATION(C4/C10): LIVE-INTERCEPTOR-INVOKED: %s (closedWhilePaused=%v)", log.snapshot()[i], closedWhilePaused)
	} else {
		t.Logf("OBSERVATION(C4/C10): B[gen1].AllowRPC was not invoked by the resumed request (closedWhilePaused=%v)", closedWhilePaused)
	}
}
