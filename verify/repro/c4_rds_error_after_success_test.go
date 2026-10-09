// Run: cp verify/repro/c4_rds_error_after_success_test.go internal/xds/server/ && go test -race -v -count=1 -run '^Test$/^Verify_C4_' ./internal/xds/server   (on the C4 target branch; this is the coverage the branch's committed tests lack - it passes on the unmodified branch and fails under verify/probes/c4_mutation.patch, while the committed tests stay green under that patch)

package server

import (
	"context"
	"errors"
	"net"
	"strconv"
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

type vc4Cfg struct{ httpfilter.FilterConfig }

type vc4Filter struct {
	httpfilter.Builder
	typeURL         string
	created, closed atomic.Int32
}

func (f *vc4Filter) TypeURLs() []string { return []string{f.typeURL} }
func (f *vc4Filter) IsTerminal() bool   { return false }
func (f *vc4Filter) ParseFilterConfig(proto.Message) (httpfilter.FilterConfig, error) {
	return vc4Cfg{}, nil
}
func (f *vc4Filter) ParseFilterConfigOverride(proto.Message) (httpfilter.FilterConfig, error) {
	return vc4Cfg{}, nil
}
func (f *vc4Filter) BuildServerFilter() httpfilter.ServerFilter { return f }
func (f *vc4Filter) Close()                                     {}
func (f *vc4Filter) BuildServerInterceptor(httpfilter.FilterConfig, httpfilter.FilterConfig) (iresolver.ServerInterceptor, error) {
	f.created.Add(1)
	return &vc4Interceptor{f: f}, nil
}

type vc4Interceptor struct{ f *vc4Filter }

func (i *vc4Interceptor) AllowRPC(context.Context) error { return nil }
func (i *vc4Interceptor) Close()                         { i.f.closed.Add(1) }

// TestVerify_C4_RDSNoDataErrorAfterSuccess installs a RouteConfiguration via
// the real xDS client, then delivers a no-data RDS error through
// listenerWrapper.handleRDSUpdate (exactly what rdsWatcher.ResourceError does)
// and asserts that the error configuration is installed and the superseded
// interceptor released.
func (s) TestVerify_C4_RDSNoDataErrorAfterSuccess(t *testing.T) {
	filter := &vc4Filter{typeURL: t.Name()}
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
		Listener: lis, ListenerResourceName: listenerName, XDSClient: xdsC,
		ModeCallback: func(_ net.Addr, mode connectivity.ServingMode, _ error) {
			select {
			case modeCh <- mode:
			default:
			}
		},
	}).(*listenerWrapper)
	defer lw.Close()

	ctx, cancel := context.WithTimeout(context.Background(), defaultTestTimeout)
	defer cancel()
	resources := e2e.UpdateOptions{
		NodeID: nodeID,
		Listeners: []*v3listenerpb.Listener{{
			Name: listenerName,
			Address: &v3corepb.Address{Address: &v3corepb.Address_SocketAddress{SocketAddress: &v3corepb.SocketAddress{
				Address: host, PortSpecifier: &v3corepb.SocketAddress_PortValue{PortValue: uint32(port)},
			}}},
			DefaultFilterChain: &v3listenerpb.FilterChain{Filters: []*v3listenerpb.Filter{{
				Name: "hcm",
				ConfigType: &v3listenerpb.Filter_TypedConfig{TypedConfig: testutils.MarshalAny(t, &v3httppb.HttpConnectionManager{
					RouteSpecifier: &v3httppb.HttpConnectionManager_Rds{Rds: &v3httppb.Rds{
						ConfigSource:    &v3corepb.ConfigSource{ConfigSourceSpecifier: &v3corepb.ConfigSource_Ads{Ads: &v3corepb.AggregatedConfigSource{}}},
						RouteConfigName: route1,
					}},
					HttpFilters: []*v3httppb.HttpFilter{
						{Name: "count", ConfigType: &v3httppb.HttpFilter_TypedConfig{TypedConfig: testutils.MarshalAny(t, &v3xdsxdstypepb.TypedStruct{TypeUrl: filter.typeURL, Value: &structpb.Struct{}})}},
						e2e.RouterHTTPFilter,
					},
				})},
			}}},
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
	for ; filter.created.Load() < 1 && ctx.Err() == nil; <-time.After(defaultTestShortTimeout) {
	}

	lw.mu.Lock()
	fc := lw.activeFilterChainManager.filterChains[0]
	lw.mu.Unlock()
	if urc := fc.usableRouteConfiguration.Load(); urc.err != nil || len(urc.vhs) != 1 {
		t.Fatalf("after successful RDS update: usable route configuration = {err: %v, vhs: %d}, want a usable one", urc.err, len(urc.vhs))
	}
	t.Logf("TRACE successful RDS update installed: created=%d closed=%d", filter.created.Load(), filter.closed.Load())

	// Same statements as rdsWatcher.ResourceError: record the no-data error
	// and hand it to the listener wrapper's production update handler.
	wantErr := errors.New("verify: route resource does not exist")
	rwu := rdsWatcherUpdate{err: wantErr}
	lw.rdsHandler.updates[route1] = rwu
	lw.handleRDSUpdate(route1, rwu)

	urc := fc.usableRouteConfiguration.Load()
	t.Logf("TRACE after no-data RDS error: installed err=%v vhs=%d created=%d closed=%d", urc.err, len(urc.vhs), filter.created.Load(), filter.closed.Load())
	if !errors.Is(urc.err, wantErr) {
		t.Errorf("after no-data RDS error: installed route configuration err = %v, want %v (error configuration NOT installed)", urc.err, wantErr)
	}
	if got := filter.closed.Load(); got != 1 {
		t.Errorf("after no-data RDS error: %d interceptors closed, want 1 (superseded configuration not released)", got)
	}
}
