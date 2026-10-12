// Expected on the audited branch: test FAILS with "LIFETIME: shared filter destroyed ... while superseded interceptor Close was still running".
// Run: cp verify/repro/c3_close_destroys_filter_during_superseded_cleanup_test.go internal/xds/server/ && go test -race -v -count=1 -run '^Test$/^VerifyC3_' ./internal/xds/server   (on branch evalon/grpc-go-xd-3100ee45)

package server

import (
	"context"
	"net"
	"runtime"
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
	"google.golang.org/grpc/internal/xds/httpfilter"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/structpb"

	v3xdsxdstypepb "github.com/cncf/xds/go/xds/type/v3"
	v3corepb "github.com/envoyproxy/go-control-plane/envoy/config/core/v3"
	v3listenerpb "github.com/envoyproxy/go-control-plane/envoy/config/listener/v3"
	v3routepb "github.com/envoyproxy/go-control-plane/envoy/config/route/v3"
	v3httppb "github.com/envoyproxy/go-control-plane/envoy/extensions/filters/network/http_connection_manager/v3"
)

type vc3Cfg struct{ httpfilter.FilterConfig }

// vc3Filter is both the builder and the (single, shared) server filter.
type vc3Filter struct {
	httpfilter.Builder
	typeURL string

	mu     sync.Mutex
	events []string

	created         atomic.Int32
	filterDestroyed atomic.Int32
	holdFirstClose  chan struct{} // closed to let interceptor #1's Close return
	firstCloseIn    chan struct{}
	firstCloseOut   chan struct{}
	destroyStack    atomic.Value
}

func (f *vc3Filter) record(e string) { f.mu.Lock(); f.events = append(f.events, e); f.mu.Unlock() }
func (f *vc3Filter) log() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.events...)
}

func (f *vc3Filter) TypeURLs() []string { return []string{f.typeURL} }
func (f *vc3Filter) IsTerminal() bool   { return false }
func (f *vc3Filter) ParseFilterConfig(proto.Message) (httpfilter.FilterConfig, error) {
	return vc3Cfg{}, nil
}
func (f *vc3Filter) ParseFilterConfigOverride(proto.Message) (httpfilter.FilterConfig, error) {
	return vc3Cfg{}, nil
}
func (f *vc3Filter) BuildServerFilter() httpfilter.ServerFilter { return f }
func (f *vc3Filter) Close() {
	f.filterDestroyed.Add(1)
	buf := make([]byte, 16<<10)
	buf = buf[:runtime.Stack(buf, false)]
	var frames []string
	for _, l := range strings.Split(string(buf), "\n") {
		if strings.HasPrefix(l, "google.golang.org/grpc/internal/xds/server.") {
			frames = append(frames, l[:strings.LastIndex(l, "(")])
		}
	}
	f.destroyStack.Store(strings.Join(frames, "\n  "))
	f.record("shared-filter-destroyed")
}
func (f *vc3Filter) BuildServerInterceptor(httpfilter.FilterConfig, httpfilter.FilterConfig) (iresolver.ServerInterceptor, error) {
	id := int(f.created.Add(1))
	f.record("interceptor-" + strconv.Itoa(id) + "-built")
	return &vc3Interceptor{f: f, id: id}, nil
}

type vc3Interceptor struct {
	f  *vc3Filter
	id int
}

func (i *vc3Interceptor) AllowRPC(context.Context) error { return nil }
func (i *vc3Interceptor) Close() {
	i.f.record("interceptor-" + strconv.Itoa(i.id) + "-close-begin")
	if i.id == 1 {
		close(i.f.firstCloseIn)
		<-i.f.holdFirstClose
	}
	i.f.record("interceptor-" + strconv.Itoa(i.id) + "-close-end")
	if i.id == 1 {
		close(i.f.firstCloseOut)
	}
}

func (s) TestVerifyC3_CloseDestroysSharedFilterDuringSupersededCleanup(t *testing.T) {
	filter := &vc3Filter{typeURL: t.Name(), holdFirstClose: make(chan struct{}), firstCloseIn: make(chan struct{}), firstCloseOut: make(chan struct{})}
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(filter.holdFirstClose) }) }
	defer release()
	httpfilter.Register(filter)
	defer httpfilter.UnregisterForTesting(filter.typeURL)

	mgmtServer, nodeID, _, _, xdsC := xdsSetupForTests(t)
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
	var closeOnce sync.Once
	closeLW := func() { closeOnce.Do(func() { lw.Close() }) }
	defer func() { release(); closeLW() }()

	ctx, cancel := context.WithTimeout(context.Background(), defaultTestTimeout)
	defer cancel()

	resources := e2e.UpdateOptions{
		NodeID: nodeID,
		Listeners: []*v3listenerpb.Listener{{
			Name: listenerName,
			Address: &v3corepb.Address{Address: &v3corepb.Address_SocketAddress{SocketAddress: &v3corepb.SocketAddress{
				Address: host, PortSpecifier: &v3corepb.SocketAddress_PortValue{PortValue: uint32(port)},
			}}},
			FilterChains: []*v3listenerpb.FilterChain{{
				Name: "fc-1",
				Filters: []*v3listenerpb.Filter{{
					Name: "hcm-1",
					ConfigType: &v3listenerpb.Filter_TypedConfig{TypedConfig: testutils.MarshalAny(t, &v3httppb.HttpConnectionManager{
						RouteSpecifier: &v3httppb.HttpConnectionManager_Rds{Rds: &v3httppb.Rds{
							ConfigSource:    &v3corepb.ConfigSource{ConfigSourceSpecifier: &v3corepb.ConfigSource_Ads{Ads: &v3corepb.AggregatedConfigSource{}}},
							RouteConfigName: route1,
						}},
						HttpFilters: []*v3httppb.HttpFilter{
							{Name: "shared", ConfigType: &v3httppb.HttpFilter_TypedConfig{TypedConfig: testutils.MarshalAny(t, &v3xdsxdstypepb.TypedStruct{TypeUrl: filter.typeURL, Value: &structpb.Struct{}})}},
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
				Routes: []*v3routepb.Route{{
					Match:  &v3routepb.RouteMatch{PathSpecifier: &v3routepb.RouteMatch_Prefix{Prefix: "/"}},
					Action: &v3routepb.Route_NonForwardingAction{},
				}},
			}},
		}},
		SkipValidation: true,
	}
	if err := mgmtServer.Update(ctx, resources); err != nil {
		t.Fatal(err)
	}
	select {
	case <-modeCh:
	case <-ctx.Done():
		t.Fatal("timeout waiting for SERVING")
	}
	for ; filter.created.Load() < 1 && ctx.Err() == nil; <-time.After(10 * time.Millisecond) {
	}

	lw.mu.Lock()
	var rsf *refCountedServerFilter
	for _, f := range lw.httpFilters {
		if f.ServerFilter == httpfilter.ServerFilter(filter) {
			rsf = f
		}
	}
	rcu := lw.rdsHandler.updates[route1]
	lw.mu.Unlock()
	if rsf == nil || rcu.data == nil {
		t.Fatalf("setup: shared filter %v / route update %v not found", rsf, rcu.data)
	}
	t.Logf("OBSERVED before replacement: interceptors built=%d, shared filter refCnt=%d", filter.created.Load(), rsf.refCnt.Load())

	// Replacement retirement: re-deliver the RouteConfiguration. Interceptor
	// #1 becomes superseded and its Close is held open.
	rdsDone := make(chan struct{})
	go func() { lw.handleRDSUpdate(route1, rcu); close(rdsDone) }()
	select {
	case <-filter.firstCloseIn:
	case <-ctx.Done():
		t.Fatal("timeout waiting for superseded interceptor Close")
	}
	refsDuringCleanup := rsf.refCnt.Load()
	t.Logf("OBSERVED with superseded interceptor-1 Close held open: interceptors built=%d, shared filter refCnt=%d (1 => only the replacement's reference remains; the superseded configuration's reference is already released), filter destroyed=%d", filter.created.Load(), refsDuringCleanup, filter.filterDestroyed.Load())

	// Concurrent shutdown.
	closeDone := make(chan struct{})
	go func() { closeLW(); close(closeDone) }()
	closeReturned := false
	select {
	case <-closeDone:
		closeReturned = true
	case <-time.After(2 * time.Second):
	}
	destroyedDuringCleanup := filter.filterDestroyed.Load()
	cleanupFinished := false
	select {
	case <-filter.firstCloseOut:
		cleanupFinished = true
	default:
	}
	t.Logf("OBSERVED after concurrent listenerWrapper.Close: Close returned=%v, shared filter destroyed=%d, refCnt=%d, superseded interceptor-1 Close finished=%v", closeReturned, destroyedDuringCleanup, rsf.refCnt.Load(), cleanupFinished)
	if s, ok := filter.destroyStack.Load().(string); ok {
		t.Logf("shared filter destroyed from:\n  %s", s)
	}
	t.Logf("OBSERVED event order so far: %v", filter.log())

	release()
	select {
	case <-rdsDone:
	case <-time.After(2 * time.Second):
		t.Error("handleRDSUpdate did not return after releasing superseded Close")
	}
	<-closeDone
	t.Logf("OBSERVED final event order: %v", filter.log())

	if refsDuringCleanup <= 1 && destroyedDuringCleanup > 0 && !cleanupFinished {
		t.Errorf("LIFETIME: shared filter destroyed (%d) by listenerWrapper.Close while superseded interceptor Close was still running", destroyedDuringCleanup)
	}
}
