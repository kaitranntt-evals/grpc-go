// Run: copy from verify/repro/testdata/ to internal/xds/server/ on branch evalon/grpc-go-xd-e43b07e4, then `go test -race -count=1 -v -run '^Test$/^VerifyC1_' ./internal/xds/server 2>&1 | grep -E 'VERIFY-C1|^\s*--- |^(ok|FAIL|PASS)'`

package server

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"google.golang.org/grpc/connectivity"
	iresolver "google.golang.org/grpc/internal/resolver"
	"google.golang.org/grpc/internal/testutils"
	"google.golang.org/grpc/internal/testutils/xds/e2e"
	"google.golang.org/grpc/internal/xds/clients/xdsclient"
	"google.golang.org/grpc/internal/xds/httpfilter"
	"google.golang.org/grpc/internal/xds/xdsclient/xdsresource"
	"google.golang.org/grpc/internal/xds/xdsclient/xdsresource/version"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/structpb"

	v3xdsxdstypepb "github.com/cncf/xds/go/xds/type/v3"
	v3corepb "github.com/envoyproxy/go-control-plane/envoy/config/core/v3"
	v3listenerpb "github.com/envoyproxy/go-control-plane/envoy/config/listener/v3"
	v3routepb "github.com/envoyproxy/go-control-plane/envoy/config/route/v3"
	v3httppb "github.com/envoyproxy/go-control-plane/envoy/extensions/filters/network/http_connection_manager/v3"
)

// verifyC1Log is an ordered log of Close() calls on server filters and on the
// interceptors they built.
type verifyC1Log struct {
	mu     sync.Mutex
	events []string
}

func (l *verifyC1Log) add(e string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.events = append(l.events, e)
}

func (l *verifyC1Log) snapshot() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]string(nil), l.events...)
}

type verifyC1Cfg struct{ httpfilter.FilterConfig }

type verifyC1Builder struct {
	httpfilter.Builder
	typeURL string
	log     *verifyC1Log
	nf      atomic.Int32
	ni      atomic.Int32
}

func (b *verifyC1Builder) TypeURLs() []string { return []string{b.typeURL} }
func (b *verifyC1Builder) IsTerminal() bool   { return false }
func (b *verifyC1Builder) ParseFilterConfig(proto.Message) (httpfilter.FilterConfig, error) {
	return verifyC1Cfg{}, nil
}
func (b *verifyC1Builder) ParseFilterConfigOverride(proto.Message) (httpfilter.FilterConfig, error) {
	return verifyC1Cfg{}, nil
}
func (b *verifyC1Builder) BuildServerFilter() httpfilter.ServerFilter {
	return &verifyC1Filter{b: b, id: fmt.Sprintf("F%d", b.nf.Add(1))}
}

type verifyC1Filter struct {
	b      *verifyC1Builder
	id     string
	closed atomic.Bool
}

func (f *verifyC1Filter) BuildServerInterceptor(httpfilter.FilterConfig, httpfilter.FilterConfig) (iresolver.ServerInterceptor, error) {
	return &verifyC1Interceptor{f: f, id: fmt.Sprintf("%s/I%d", f.id, f.b.ni.Add(1))}, nil
}

func (f *verifyC1Filter) Close() {
	f.closed.Store(true)
	f.b.log.add("filter-close " + f.id)
}

type verifyC1Interceptor struct {
	f  *verifyC1Filter
	id string
}

func (*verifyC1Interceptor) AllowRPC(context.Context) error { return nil }

// Close records whether the parent filter had already been closed when this
// dependent interceptor was closed.
func (i *verifyC1Interceptor) Close() {
	i.f.b.log.add(fmt.Sprintf("icpt-close %s (parent-filter-already-closed=%v)", i.id, i.f.closed.Load()))
}

// verifyC1XDSClient wraps the real xDS client and remembers the RDS watchers
// registered by the listener wrapper, so that the test can deliver a
// ResourceError to them the way the xDS client does for a resource that no
// longer exists.
type verifyC1XDSClient struct {
	XDSClient
	mu       sync.Mutex
	watchers map[string]xdsclient.ResourceWatcher
}

func (c *verifyC1XDSClient) WatchResource(typeURL, name string, w xdsclient.ResourceWatcher) func() {
	if typeURL == version.V3RouteConfigURL {
		c.mu.Lock()
		c.watchers[name] = w
		c.mu.Unlock()
	}
	return c.XDSClient.WatchResource(typeURL, name, w)
}

func verifyC1OrderViolation(events []string) (string, bool) {
	filterClosed := map[string]bool{}
	for _, e := range events {
		fields := strings.Fields(e)
		switch fields[0] {
		case "filter-close":
			filterClosed[fields[1]] = true
		case "icpt-close":
			parent := strings.SplitN(fields[1], "/", 2)[0]
			if filterClosed[parent] {
				return fmt.Sprintf("interceptor %s closed after its parent filter %s", fields[1], parent), true
			}
		}
	}
	return "", false
}

// verifyC1Setup starts a management server and a listener wrapper serving a
// single filter chain whose routes come from the RDS resource route1 (two
// routes, so two interceptors of the tracing filter).
func verifyC1Setup(t *testing.T) (*listenerWrapper, *verifyC1XDSClient, *verifyC1Log, *refCountedServerFilter) {
	t.Helper()
	log := &verifyC1Log{}
	fb := &verifyC1Builder{typeURL: t.Name(), log: log}
	httpfilter.Register(fb)
	t.Cleanup(func() { httpfilter.UnregisterForTesting(fb.typeURL) })

	mgmtServer, nodeID, _, _, realXDSC := xdsSetupForTests(t)
	xdsC := &verifyC1XDSClient{XDSClient: realXDSC, watchers: map[string]xdsclient.ResourceWatcher{}}
	lis, err := testutils.LocalTCPListener()
	if err != nil {
		t.Fatal(err)
	}
	host, p, _ := net.SplitHostPort(lis.Addr().String())
	port, _ := strconv.Atoi(p)
	modeCh := make(chan connectivity.ServingMode, 1)
	lw := NewListenerWrapper(ListenerWrapperParams{
		Listener:             lis,
		ListenerResourceName: listenerName,
		XDSClient:            xdsC,
		ModeCallback: func(_ net.Addr, mode connectivity.ServingMode, _ error) {
			select {
			case modeCh <- mode:
			default:
			}
		},
	}).(*listenerWrapper)
	t.Cleanup(func() { lw.Close() })

	ctx, cancel := context.WithTimeout(context.Background(), defaultTestTimeout)
	t.Cleanup(cancel)
	resources := e2e.UpdateOptions{
		NodeID: nodeID,
		Listeners: []*v3listenerpb.Listener{{
			Name: listenerName,
			Address: &v3corepb.Address{Address: &v3corepb.Address_SocketAddress{SocketAddress: &v3corepb.SocketAddress{
				Address: host, PortSpecifier: &v3corepb.SocketAddress_PortValue{PortValue: uint32(port)},
			}}},
			FilterChains: []*v3listenerpb.FilterChain{{
				Name: "fc",
				Filters: []*v3listenerpb.Filter{{
					Name: "hcm",
					ConfigType: &v3listenerpb.Filter_TypedConfig{TypedConfig: testutils.MarshalAny(t, &v3httppb.HttpConnectionManager{
						RouteSpecifier: &v3httppb.HttpConnectionManager_Rds{Rds: &v3httppb.Rds{
							ConfigSource:    &v3corepb.ConfigSource{ConfigSourceSpecifier: &v3corepb.ConfigSource_Ads{Ads: &v3corepb.AggregatedConfigSource{}}},
							RouteConfigName: route1,
						}},
						HttpFilters: []*v3httppb.HttpFilter{
							{Name: "trace", ConfigType: &v3httppb.HttpFilter_TypedConfig{TypedConfig: testutils.MarshalAny(t, &v3xdsxdstypepb.TypedStruct{TypeUrl: fb.typeURL, Value: &structpb.Struct{}})}},
							e2e.RouterHTTPFilter,
						},
					})},
				}},
			}},
		}},
		Routes: []*v3routepb.RouteConfiguration{{
			Name: route1,
			VirtualHosts: []*v3routepb.VirtualHost{{
				Domains: []string{"*"},
				Routes: []*v3routepb.Route{
					{Match: &v3routepb.RouteMatch{PathSpecifier: &v3routepb.RouteMatch_Prefix{Prefix: "/a"}}, Action: &v3routepb.Route_NonForwardingAction{}},
					{Match: &v3routepb.RouteMatch{PathSpecifier: &v3routepb.RouteMatch_Prefix{Prefix: "/"}}, Action: &v3routepb.Route_NonForwardingAction{}},
				},
			}},
		}},
		SkipValidation: true,
	}
	if err := mgmtServer.Update(ctx, resources); err != nil {
		t.Fatal(err)
	}
	select {
	case mode := <-modeCh:
		if mode != connectivity.ServingModeServing {
			t.Fatalf("listener mode = %v, want SERVING", mode)
		}
	case <-ctx.Done():
		t.Fatal("timeout waiting for SERVING")
	}
	for ; fb.ni.Load() < 2 && ctx.Err() == nil; <-time.After(defaultTestShortTimeout) {
	}

	lw.mu.Lock()
	defer lw.mu.Unlock()
	var rsf *refCountedServerFilter
	for k, f := range lw.httpFilters {
		if k.name == "trace" {
			rsf = f
		}
	}
	if rsf == nil {
		t.Fatal("tracing server filter not found in the listener wrapper's filter cache")
	}
	return lw, xdsC, log, rsf
}

// Delivers a no-data RDS ResourceError (after a successful update) through the
// RDS watcher that the listener wrapper registered with the xDS client, and
// prints the order in which the retired interceptors and their parent filter
// are closed.
func (s) TestVerifyC1_NoDataErrorUpdate_CloseOrder(t *testing.T) {
	lw, xdsC, log, rsf := verifyC1Setup(t)

	t.Logf("VERIFY-C1 before error update: interceptors-built=2 filter-refcount=%d (references held only by the filter chain; cache entry holds none) events=%q", rsf.refCnt.Load(), log.snapshot())

	xdsC.mu.Lock()
	w := xdsC.watchers[route1]
	xdsC.mu.Unlock()
	if w == nil {
		t.Fatal("no RDS watcher captured for route1")
	}
	done := make(chan struct{})
	w.ResourceError(xdsresource.NewErrorf(xdsresource.ErrorTypeResourceNotFound, "verify: route1 removed"), func() { close(done) })
	<-done

	events := log.snapshot()
	t.Logf("VERIFY-C1 after no-data error update: filter-refcount=%d", rsf.refCnt.Load())
	for i, e := range events {
		t.Logf("VERIFY-C1 event #%d: %s", i, e)
	}
	lw.mu.Lock()
	urcErr := lw.activeFilterChainManager.filterChains[0].usableRouteConfiguration.Load().err
	lw.mu.Unlock()
	t.Logf("VERIFY-C1 usable route configuration error now: %v", urcErr)
	if msg, bad := verifyC1OrderViolation(events); bad {
		t.Errorf("VERIFY-C1 ORDER VIOLATION on no-data error update: %s", msg)
	}
}

// Same trigger as the eval fixture: hands the no-data error update directly to
// handleRDSUpdate.
func (s) TestVerifyC1_NoDataErrorUpdate_DirectHandleRDSUpdate_CloseOrder(t *testing.T) {
	lw, _, log, rsf := verifyC1Setup(t)
	t.Logf("VERIFY-C1 before error update: filter-refcount=%d events=%q", rsf.refCnt.Load(), log.snapshot())
	rwu := rdsWatcherUpdate{err: errors.New("verify: route1 withdrawn")}
	lw.rdsHandler.updates[route1] = rwu
	lw.handleRDSUpdate(route1, rwu)
	events := log.snapshot()
	t.Logf("VERIFY-C1 after no-data error update: filter-refcount=%d", rsf.refCnt.Load())
	for i, e := range events {
		t.Logf("VERIFY-C1 event #%d: %s", i, e)
	}
	if msg, bad := verifyC1OrderViolation(events); bad {
		t.Errorf("VERIFY-C1 ORDER VIOLATION on no-data error update: %s", msg)
	}
}

// Closes the listener wrapper (what server shutdown does) while the current
// route configuration is healthy, and prints the close order.
func (s) TestVerifyC1_ListenerClose_CloseOrder(t *testing.T) {
	lw, _, log, rsf := verifyC1Setup(t)
	t.Logf("VERIFY-C1 before listener Close: filter-refcount=%d events=%q", rsf.refCnt.Load(), log.snapshot())
	lw.Close()
	events := log.snapshot()
	t.Logf("VERIFY-C1 after listener Close: filter-refcount=%d", rsf.refCnt.Load())
	for i, e := range events {
		t.Logf("VERIFY-C1 event #%d: %s", i, e)
	}
	if msg, bad := verifyC1OrderViolation(events); bad {
		t.Errorf("VERIFY-C1 ORDER VIOLATION on listener Close: %s", msg)
	}
}
