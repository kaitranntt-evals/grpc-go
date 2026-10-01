// Run: verify/instrumentation/apply_trace.sh <checkout> && cp verify/repro/c1_filter_ref_order_test.go <checkout>/test/xds/verify_c1_filter_ref_order_test.go && (cd <checkout> && go test -race -count=1 -v -run '^Test$/^VerifyC1_' ./test/xds)
//
// Audit repro for C1/C8 (run v-a332a9ce). Traces, on a live xDS-enabled gRPC
// server with a single filter chain, the order of
//   ref-acquire / ref-release  (refCountedServerFilter.incRef / Close, via the audit hook),
//   icpt-build  / icpt-close   (interceptor instances of the traced filter),
//   filter-build / filter-close (the underlying ServerFilter instance),
// across an in-place RDS replacement that keeps the filter, an in-place RDS
// replacement that disables the filter on every route, an LDS replacement and
// Server.Stop. The test never fails on ordering; it prints the trace and a
// RELEASE_BEFORE_CLOSE=<bool> line per phase.

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
	xdsserver "google.golang.org/grpc/internal/xds/server"
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

type vc1Event struct {
	kind, obj, callers string
}

type vc1Log struct {
	mu     sync.Mutex
	events []vc1Event
}

func (l *vc1Log) add(kind, obj string) {
	pcs := make([]uintptr, 40)
	n := runtime.Callers(2, pcs)
	frames := runtime.CallersFrames(pcs[:n])
	var cs []string
	for {
		f, more := frames.Next()
		if i := strings.LastIndex(f.Function, "internal/xds/server."); i >= 0 {
			name := f.Function[i+len("internal/xds/server."):]
			if !strings.Contains(name, "refCountedServerFilter") && name != "verifyTrace" && !strings.Contains(name, "interceptorList") {
				cs = append(cs, name)
			}
		}
		if !more {
			break
		}
	}
	l.mu.Lock()
	l.events = append(l.events, vc1Event{kind: kind, obj: obj, callers: strings.Join(cs, " < ")})
	l.mu.Unlock()
}

func (l *vc1Log) len() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.events)
}

func (l *vc1Log) snapshot() []vc1Event {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]vc1Event(nil), l.events...)
}

type vc1Cfg struct {
	httpfilter.FilterConfig
	path string
}

func vc1CfgFromProto(cfg proto.Message) (httpfilter.FilterConfig, error) {
	ts, ok := cfg.(*v3xdsxdstypepb.TypedStruct)
	if !ok {
		return nil, fmt.Errorf("unsupported filter config type: %T", cfg)
	}
	ret := vc1Cfg{}
	if v := ts.GetValue().GetFields()["path"]; v != nil {
		ret.path = v.GetStringValue()
	}
	return ret, nil
}

type vc1Builder struct {
	httpfilter.Builder
	typeURL string
	log     *vc1Log
	nf      atomic.Int32
}

func (b *vc1Builder) IsTerminal() bool   { return false }
func (b *vc1Builder) TypeURLs() []string { return []string{b.typeURL} }
func (*vc1Builder) ParseFilterConfig(cfg proto.Message) (httpfilter.FilterConfig, error) {
	return vc1CfgFromProto(cfg)
}
func (*vc1Builder) ParseFilterConfigOverride(cfg proto.Message) (httpfilter.FilterConfig, error) {
	return vc1CfgFromProto(cfg)
}
func (b *vc1Builder) BuildServerFilter() httpfilter.ServerFilter {
	f := &vc1Filter{b: b, id: fmt.Sprintf("F%d", b.nf.Add(1))}
	b.log.add("filter-build", f.id)
	return f
}

type vc1Filter struct {
	b      *vc1Builder
	id     string
	ni     atomic.Int32
	closed atomic.Bool
}

func (f *vc1Filter) BuildServerInterceptor(_, _ httpfilter.FilterConfig) (resolver.ServerInterceptor, error) {
	i := &vc1Icpt{f: f, id: fmt.Sprintf("%s/I%d", f.id, f.ni.Add(1))}
	kind := "icpt-build"
	if f.closed.Load() {
		kind = "icpt-build-ON-CLOSED-FILTER"
	}
	f.b.log.add(kind, i.id)
	return i, nil
}

func (f *vc1Filter) Close() {
	f.closed.Store(true)
	f.b.log.add("filter-close", f.id)
}

type vc1Icpt struct {
	f      *vc1Filter
	id     string
	closed atomic.Bool
}

func (i *vc1Icpt) AllowRPC(context.Context) error { return nil }
func (i *vc1Icpt) Close() {
	i.closed.Store(true)
	i.f.b.log.add("icpt-close", i.id)
}

type vc1Env struct {
	t         *testing.T
	log       *vc1Log
	typeURL   string
	ms        *e2e.ManagementServer
	resources e2e.UpdateOptions
	client    testgrpc.TestServiceClient
	host      string
	port      uint32
	stop      func()
}

const vc1FilterName = "vtrace"
const vc1RouteName = "routeName"

func vc1Listener(t *testing.T, host string, port uint32, typeURL, basePath string) *v3listenerpb.Listener {
	return &v3listenerpb.Listener{
		Name: fmt.Sprintf(e2e.ServerListenerResourceNameTemplate, net.JoinHostPort(host, strconv.Itoa(int(port)))),
		Address: &v3corepb.Address{Address: &v3corepb.Address_SocketAddress{SocketAddress: &v3corepb.SocketAddress{
			Address: host, PortSpecifier: &v3corepb.SocketAddress_PortValue{PortValue: port}}}},
		FilterChains: []*v3listenerpb.FilterChain{{
			Name: "only-chain",
			Filters: []*v3listenerpb.Filter{{
				Name: "hcm",
				ConfigType: &v3listenerpb.Filter_TypedConfig{TypedConfig: testutils.MarshalAny(t, &v3httppb.HttpConnectionManager{
					HttpFilters: []*v3httppb.HttpFilter{
						{Name: vc1FilterName, ConfigType: &v3httppb.HttpFilter_TypedConfig{TypedConfig: testutils.MarshalAny(t, &v3xdsxdstypepb.TypedStruct{
							TypeUrl: typeURL,
							Value:   &structpb.Struct{Fields: map[string]*structpb.Value{"path": {Kind: &structpb.Value_StringValue{StringValue: basePath}}}},
						})}},
						e2e.HTTPFilter("router", &v3routerpb.Router{}),
					},
					RouteSpecifier: &v3httppb.HttpConnectionManager_Rds{Rds: &v3httppb.Rds{
						ConfigSource:    &v3corepb.ConfigSource{ConfigSourceSpecifier: &v3corepb.ConfigSource_Ads{Ads: &v3corepb.AggregatedConfigSource{}}},
						RouteConfigName: vc1RouteName,
					}},
				})},
			}},
		}},
	}
}

// vc1Routes builds a route configuration with n routes; if disabled, the
// filter is disabled on the virtual host (i.e. on every route).
func vc1Routes(t *testing.T, n int, disabled bool) *v3routepb.RouteConfiguration {
	var routes []*v3routepb.Route
	for i := 0; i < n-1; i++ {
		routes = append(routes, &v3routepb.Route{
			Match:  &v3routepb.RouteMatch{PathSpecifier: &v3routepb.RouteMatch_Prefix{Prefix: fmt.Sprintf("/never-%d/", i)}},
			Action: &v3routepb.Route_NonForwardingAction{},
		})
	}
	routes = append(routes, &v3routepb.Route{
		Match:  &v3routepb.RouteMatch{PathSpecifier: &v3routepb.RouteMatch_Prefix{Prefix: "/"}},
		Action: &v3routepb.Route_NonForwardingAction{},
	})
	vh := &v3routepb.VirtualHost{Domains: []string{"*"}, Routes: routes}
	if disabled {
		vh.TypedPerFilterConfig = map[string]*anypb.Any{vc1FilterName: testutils.MarshalAny(t, &v3routepb.FilterConfig{Disabled: true})}
	}
	return &v3routepb.RouteConfiguration{Name: vc1RouteName, VirtualHosts: []*v3routepb.VirtualHost{vh}}
}

func vc1Setup(ctx context.Context, t *testing.T) *vc1Env {
	// FilterConfig{disabled: true} overrides are honored only with
	// GRPC_EXPERIMENTAL_XDS_EXT_PROC_ON_CLIENT=true.
	testutils.SetEnvConfig(t, &envconfig.XDSClientExtProcEnabled, true)
	return vc1SetupNoFlag(ctx, t)
}

func vc1SetupNoFlag(ctx context.Context, t *testing.T) *vc1Env {
	env := &vc1Env{t: t, log: &vc1Log{}, typeURL: t.Name()}
	fb := &vc1Builder{typeURL: env.typeURL, log: env.log}
	httpfilter.Register(fb)
	t.Cleanup(func() { httpfilter.UnregisterForTesting(fb.typeURL) })
	xdsserver.VerifyTrace = func(ev string, filter any, refCnt int32) {
		if f, ok := filter.(*vc1Filter); ok {
			env.log.add(ev, fmt.Sprintf("%s (refCnt before=%d)", f.id, refCnt))
		}
	}
	t.Cleanup(func() { xdsserver.VerifyTrace = nil })

	ms, nodeID, bootstrapContents, xdsResolver := setup.ManagementServerAndResolver(t)
	env.ms = ms
	servingCh := make(chan struct{}, 10)
	opt := xds.ServingModeCallback(func(_ net.Addr, args xds.ServingModeChangeArgs) {
		if args.Mode == connectivity.ServingModeServing {
			select {
			case servingCh <- struct{}{}:
			default:
			}
		}
	})
	lis, stopServer := setupGRPCServer(t, bootstrapContents, opt)
	env.stop = stopServer
	t.Cleanup(stopServer)
	host, port, err := hostPortFromListener(lis)
	if err != nil {
		t.Fatal(err)
	}
	env.host, env.port = host, port
	const serviceName = "my-service"
	env.resources = e2e.DefaultClientResources(e2e.ResourceParams{DialTarget: serviceName, NodeID: nodeID, Host: host, Port: port, SecLevel: e2e.SecurityLevelNone})
	env.resources.Listeners = append(env.resources.Listeners, vc1Listener(t, host, port, env.typeURL, "base1"))
	env.resources.Routes = append(env.resources.Routes, vc1Routes(t, 1, false))
	if err := ms.Update(ctx, env.resources); err != nil {
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
	env.client = testgrpc.NewTestServiceClient(cc)
	if _, err := env.client.EmptyCall(ctx, &testpb.Empty{}, grpc.WaitForReady(true)); err != nil {
		t.Fatalf("EmptyCall() failed: %v", err)
	}
	return env
}

func (env *vc1Env) setRoutes(ctx context.Context, rc *v3routepb.RouteConfiguration) {
	env.resources.Routes = []*v3routepb.RouteConfiguration{env.resources.Routes[0], rc}
	if err := env.ms.Update(ctx, env.resources); err != nil {
		env.t.Fatal(err)
	}
}

// settle waits until at least one event was logged after start and the log has
// been unchanged for one second (bounded by 8s). If rpc is true an RPC is
// issued on every poll to keep traffic flowing.
func (env *vc1Env) settle(ctx context.Context, start int, rpc bool) {
	deadline := time.Now().Add(8 * time.Second)
	last, lastChange := env.log.len(), time.Now()
	for time.Now().Before(deadline) {
		if rpc {
			rctx, cancel := context.WithTimeout(ctx, time.Second)
			env.client.EmptyCall(rctx, &testpb.Empty{}, grpc.WaitForReady(true))
			cancel()
		}
		if n := env.log.len(); n != last {
			last, lastChange = n, time.Now()
		}
		if last > start && time.Since(lastChange) > time.Second {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// report prints the events of a phase and whether any reference to the traced
// filter was released before the last interceptor that existed at the start of
// the phase finished closing.
func (env *vc1Env) report(phase string, start int) {
	all := env.log.snapshot()
	open := map[string]bool{}
	for _, e := range all[:start] {
		switch {
		case strings.HasPrefix(e.kind, "icpt-build"):
			open[e.obj] = true
		case e.kind == "icpt-close":
			delete(open, e.obj)
		}
	}
	firstRelease, lastRetiredClose, firstFilterClose := -1, -1, -1
	env.t.Logf("PHASE %s: %d interceptor(s) open at phase start", phase, len(open))
	for i := start; i < len(all); i++ {
		e := all[i]
		env.t.Logf("PHASE %s: #%02d %-13s %-24s via %s", phase, i, e.kind, e.obj, e.callers)
		switch {
		case e.kind == "ref-release" && firstRelease < 0:
			firstRelease = i
		case e.kind == "filter-close" && firstFilterClose < 0:
			firstFilterClose = i
		case e.kind == "icpt-close" && open[e.obj]:
			lastRetiredClose = i
			delete(open, e.obj)
		}
	}
	env.t.Logf("PHASE %s: SUMMARY first ref-release=#%d first filter-close=#%d last retired icpt-close=#%d retired-interceptors-never-closed=%d RELEASE_BEFORE_CLOSE=%v FILTER_CLOSED_BEFORE_INTERCEPTORS=%v",
		phase, firstRelease, firstFilterClose, lastRetiredClose, len(open),
		firstRelease >= 0 && (lastRetiredClose > firstRelease || len(open) > 0),
		firstFilterClose >= 0 && (lastRetiredClose > firstFilterClose || len(open) > 0))
}

func (s) TestVerifyC1_ReplaceKeep_Then_DisableAll(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	env := vc1Setup(ctx, t)
	env.report("initial", 0)

	start := env.log.len()
	env.setRoutes(ctx, vc1Routes(t, 2, false))
	env.settle(ctx, start, true)
	env.report("rds-replace-filter-kept", start)

	start = env.log.len()
	env.setRoutes(ctx, vc1Routes(t, 2, true))
	env.settle(ctx, start, true)
	env.report("rds-replace-filter-disabled-on-every-route", start)
}

// Same as above without any experimental flag: the last replacement is a
// RouteConfiguration with no virtual hosts, which also removes every use of
// the filter.
func (s) TestVerifyC1_ReplaceKeep_Then_NoVirtualHosts(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	env := vc1SetupNoFlag(ctx, t)
	env.report("noflag-initial", 0)

	start := env.log.len()
	env.setRoutes(ctx, vc1Routes(t, 2, false))
	env.settle(ctx, start, true)
	env.report("noflag-rds-replace-filter-kept", start)

	start = env.log.len()
	env.setRoutes(ctx, &v3routepb.RouteConfiguration{Name: vc1RouteName})
	env.settle(ctx, start, true)
	env.report("noflag-rds-replace-no-virtual-hosts", start)
}

func (s) TestVerifyC1_LDSReplace_Then_Stop(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	env := vc1Setup(ctx, t)

	start := env.log.len()
	env.resources.Listeners[len(env.resources.Listeners)-1] = vc1Listener(t, env.host, env.port, env.typeURL, "base2")
	if err := env.ms.Update(ctx, env.resources); err != nil {
		t.Fatal(err)
	}
	env.settle(ctx, start, true)
	env.report("lds-replace", start)

	start = env.log.len()
	env.stop()
	env.settle(ctx, start, false)
	env.report("server-stop", start)
}
