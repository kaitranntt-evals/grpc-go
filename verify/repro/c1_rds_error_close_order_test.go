// Run: cp verify/repro/c1_rds_error_close_order_test.go internal/xds/server/ && go test -race -v -count=1 -run '^Test$/^VerifyC1_' ./internal/xds/server 2>&1 | grep -E 'C1-TRACE|^(---|FAIL|ok|PASS)|    ---'

/*
 *
 * Copyright 2026 gRPC authors.
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 *
 */

package server

import (
	"context"
	"errors"
	"net"
	"strconv"
	"sync"
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

type verifyC1Cfg struct{ httpfilter.FilterConfig }

// verifyC1Filter is both the filter builder and the (single) server filter
// instance. It records the order of filter/interceptor Close calls.
type verifyC1Filter struct {
	httpfilter.Builder
	typeURL string
	mu      sync.Mutex
	events  []string
	nIcpt   int
	closed  bool // server filter Close() has run
}

func (f *verifyC1Filter) TypeURLs() []string { return []string{f.typeURL} }
func (f *verifyC1Filter) IsTerminal() bool   { return false }
func (f *verifyC1Filter) ParseFilterConfig(proto.Message) (httpfilter.FilterConfig, error) {
	return verifyC1Cfg{}, nil
}
func (f *verifyC1Filter) ParseFilterConfigOverride(proto.Message) (httpfilter.FilterConfig, error) {
	return verifyC1Cfg{}, nil
}
func (f *verifyC1Filter) BuildServerFilter() httpfilter.ServerFilter {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.events = append(f.events, "filter-build")
	f.closed = false
	return f
}
func (f *verifyC1Filter) Close() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.events = append(f.events, "filter-close")
	f.closed = true
}
func (f *verifyC1Filter) BuildServerInterceptor(httpfilter.FilterConfig, httpfilter.FilterConfig) (iresolver.ServerInterceptor, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.nIcpt++
	id := f.nIcpt
	f.events = append(f.events, "icpt-build#"+strconv.Itoa(id))
	return &verifyC1Interceptor{f: f, id: id}, nil
}

type verifyC1Interceptor struct {
	f  *verifyC1Filter
	id int
}

func (i *verifyC1Interceptor) AllowRPC(context.Context) error { return nil }
func (i *verifyC1Interceptor) Close() {
	i.f.mu.Lock()
	defer i.f.mu.Unlock()
	ev := "icpt-close#" + strconv.Itoa(i.id)
	if i.f.closed {
		ev += "(parent-filter-ALREADY-CLOSED)"
	}
	i.f.events = append(i.f.events, ev)
}

// TestVerifyC1_RDSErrorAfterUsableConfig_CloseOrder installs a usable RDS route
// configuration on a single filter chain (one filter, two routes => two
// interceptors), then delivers an RDS error update carrying no route data, and
// traces the order in which the old filter and old interceptors are closed.
func (s) TestVerifyC1_RDSErrorAfterUsableConfig_CloseOrder(t *testing.T) {
	filter := &verifyC1Filter{typeURL: t.Name()}
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
	defer lw.Close()

	ctx, cancel := context.WithTimeout(context.Background(), defaultTestTimeout)
	defer cancel()

	hcm := &v3httppb.HttpConnectionManager{
		RouteSpecifier: &v3httppb.HttpConnectionManager_Rds{Rds: &v3httppb.Rds{
			ConfigSource:    &v3corepb.ConfigSource{ConfigSourceSpecifier: &v3corepb.ConfigSource_Ads{Ads: &v3corepb.AggregatedConfigSource{}}},
			RouteConfigName: route1,
		}},
		HttpFilters: []*v3httppb.HttpFilter{
			{Name: "count", ConfigType: &v3httppb.HttpFilter_TypedConfig{TypedConfig: testutils.MarshalAny(t, &v3xdsxdstypepb.TypedStruct{TypeUrl: filter.typeURL, Value: &structpb.Struct{}})}},
			e2e.RouterHTTPFilter,
		},
	}
	mkRoute := func(prefix string) *v3routepb.Route {
		return &v3routepb.Route{
			Match:  &v3routepb.RouteMatch{PathSpecifier: &v3routepb.RouteMatch_Prefix{Prefix: prefix}},
			Action: &v3routepb.Route_NonForwardingAction{},
		}
	}
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
					Name:       "hcm-1",
					ConfigType: &v3listenerpb.Filter_TypedConfig{TypedConfig: testutils.MarshalAny(t, hcm)},
				}},
			}},
		}},
		Routes: []*v3routepb.RouteConfiguration{{
			Name: route1,
			VirtualHosts: []*v3routepb.VirtualHost{{
				Domains: []string{"*"},
				Routes:  []*v3routepb.Route{mkRoute("/a"), mkRoute("/b")},
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
	built := func() int {
		filter.mu.Lock()
		defer filter.mu.Unlock()
		return filter.nIcpt
	}
	for ; built() < 2 && ctx.Err() == nil; <-time.After(defaultTestShortTimeout) {
	}
	if got := built(); got != 2 {
		t.Fatalf("built %d interceptors, want 2", got)
	}

	filter.mu.Lock()
	t.Logf("C1-TRACE after usable config installed: %v", filter.events)
	mark := len(filter.events)
	filter.mu.Unlock()

	// RDS error update with no route data, after a usable configuration.
	rwu := rdsWatcherUpdate{err: errors.New("verify: route1 withdrawn")}
	lw.rdsHandler.updates[route1] = rwu
	lw.handleRDSUpdate(route1, rwu)

	filter.mu.Lock()
	evs := append([]string(nil), filter.events[mark:]...)
	filter.mu.Unlock()
	t.Logf("C1-TRACE cleanup events during handleRDSUpdate(err, no data): %v", evs)

	filterIdx, lastIcptIdx, icptCloses := -1, -1, 0
	for i, ev := range evs {
		if ev == "filter-close" && filterIdx == -1 {
			filterIdx = i
		}
		if len(ev) >= 10 && ev[:10] == "icpt-close" {
			lastIcptIdx = i
			icptCloses++
		}
	}
	t.Logf("C1-TRACE filter-close index=%d, interceptor closes=%d, last icpt-close index=%d", filterIdx, icptCloses, lastIcptIdx)
	if filterIdx != -1 && filterIdx < lastIcptIdx {
		t.Errorf("C1-TRACE ORDER VIOLATION: old filter released (event #%d) before old interceptors finished closing (last at event #%d)", filterIdx, lastIcptIdx)
	}
}
