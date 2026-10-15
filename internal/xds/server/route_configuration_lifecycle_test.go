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
	"fmt"
	"net"
	"sync/atomic"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/internal/grpcsync"
	"google.golang.org/grpc/internal/resolver"
	"google.golang.org/grpc/internal/transport"
	"google.golang.org/grpc/internal/xds/httpfilter"
	"google.golang.org/grpc/internal/xds/xdsclient/xdsresource"
	"google.golang.org/grpc/metadata"
)

type lifecycleFilterBuilder struct {
	httpfilter.Builder
	filters    []*lifecycleServerFilter
	buildCalls int
	failAt     int
}

func (*lifecycleFilterBuilder) TypeURLs() []string { return []string{"lifecycle-filter"} }

func (b *lifecycleFilterBuilder) BuildServerFilter() httpfilter.ServerFilter {
	f := &lifecycleServerFilter{builder: b}
	b.filters = append(b.filters, f)
	return f
}

type lifecycleServerFilter struct {
	builder      *lifecycleFilterBuilder
	interceptors []*lifecycleInterceptor
	closed       int
}

func (f *lifecycleServerFilter) BuildServerInterceptor(httpfilter.FilterConfig, httpfilter.FilterConfig) (resolver.ServerInterceptor, error) {
	f.builder.buildCalls++
	if f.closed != 0 {
		return nil, errors.New("building interceptor on a closed filter")
	}
	if f.builder.buildCalls == f.builder.failAt {
		return nil, errors.New("interceptor construction failed")
	}
	i := &lifecycleInterceptor{filter: f}
	f.interceptors = append(f.interceptors, i)
	return i, nil
}

func (f *lifecycleServerFilter) Close() { f.closed++ }

type lifecycleInterceptor struct {
	filter            *lifecycleServerFilter
	closed            int
	closedAfterFilter bool
}

func (*lifecycleInterceptor) AllowRPC(context.Context) error { return nil }

func (i *lifecycleInterceptor) Close() {
	i.closed++
	i.closedAfterFilter = i.filter.closed != 0
}

func (b *lifecycleFilterBuilder) checkClosed(t *testing.T) {
	t.Helper()
	for n, f := range b.filters {
		if f.closed != 1 {
			t.Errorf("Filter %d closed %d times, want 1", n, f.closed)
		}
		for j, i := range f.interceptors {
			if i.closed != 1 {
				t.Errorf("Filter %d interceptor %d closed %d times, want 1", n, j, i.closed)
			}
			if i.closedAfterFilter {
				t.Errorf("Filter %d interceptor %d closed after its filter", n, j)
			}
		}
	}
}

func lifecycleRouteConfig() *xdsresource.RouteConfigUpdate {
	config := &xdsresource.RouteConfigUpdate{}
	for range 2 {
		vh := &xdsresource.VirtualHost{Domains: []string{"*"}}
		for range 2 {
			vh.Routes = append(vh.Routes, &xdsresource.Route{Prefix: newStringP("/"), ActionType: xdsresource.RouteActionNonForwardingAction})
		}
		config.VirtualHosts = append(config.VirtualHosts, vh)
	}
	return config
}

func lifecycleFilterChain(b *lifecycleFilterBuilder) *filterChain {
	fc := &filterChain{
		routeConfigName: "route",
		httpFilters: []xdsresource.HTTPFilter{
			{Name: "first", Filter: b},
			{Name: "second", Filter: b},
		},
		usableRouteConfiguration: &atomic.Pointer[usableRouteConfiguration]{},
	}
	fc.usableRouteConfiguration.Store(&usableRouteConfiguration{})
	return fc
}

// Failed construction must release interceptors from earlier filters in the
// same route, earlier routes, and earlier virtual hosts.
func (s) TestRouteConfigurationConstructionFailureCleanup(t *testing.T) {
	for _, failAt := range []int{2, 4, 6, 8} {
		t.Run(fmt.Sprintf("interceptor_%d", failAt), func(t *testing.T) {
			b := &lifecycleFilterBuilder{failAt: failAt}
			fc := lifecycleFilterChain(b)
			l := &listenerWrapper{httpFilters: make(map[serverFilterKey]*refCountedServerFilter)}
			rc := fc.constructUsableRouteConfiguration(*lifecycleRouteConfig(), l.getOrCreateServerFilterLocked)
			if rc.err == nil {
				t.Fatal("Route configuration construction succeeded, want error")
			}
			b.checkClosed(t)
		})
	}
}

// Replacing a working route configuration with an error or an empty config
// must release its resources. A subsequent valid update must use live filters,
// and stopping the filter chain manager must release everything exactly once.
func (s) TestRouteConfigurationErrorUpdateCleanup(t *testing.T) {
	for _, name := range []string{"resource_error", "construction_error", "empty_config"} {
		t.Run(name, func(t *testing.T) {
			b := &lifecycleFilterBuilder{}
			fc := lifecycleFilterChain(b)
			fcm := &filterChainManager{filterChains: []*filterChain{fc}}
			l := &listenerWrapper{
				closed:                   grpcsync.NewEvent(),
				activeFilterChainManager: fcm,
				httpFilters:              make(map[serverFilterKey]*refCountedServerFilter),
				rdsHandler:               &rdsHandler{},
			}
			config := lifecycleRouteConfig()
			l.handleRDSUpdate("route", rdsWatcherUpdate{data: config})
			if err := fc.usableRouteConfiguration.Load().err; err != nil {
				t.Fatalf("Initial route configuration failed: %v", err)
			}
			var update rdsWatcherUpdate
			switch name {
			case "resource_error":
				update.err = errors.New("route resource removed")
			case "construction_error":
				b.failAt = b.buildCalls + 6
				update.data = config
			case "empty_config":
				update.data = &xdsresource.RouteConfigUpdate{}
			}
			l.handleRDSUpdate("route", update)
			if err := fc.usableRouteConfiguration.Load().err; (err != nil) != (name != "empty_config") {
				t.Fatalf("Updated route configuration error = %v, want error: %v", err, name != "empty_config")
			}
			b.checkClosed(t)

			l.handleRDSUpdate("route", rdsWatcherUpdate{data: config})
			if err := fc.usableRouteConfiguration.Load().err; err != nil {
				t.Fatalf("Route configuration recovery failed: %v", err)
			}
			fcm.stop()
			fcm.stop()
			b.checkClosed(t)
		})
	}
}

type lifecycleListener struct{ net.Listener }

func (lifecycleListener) Close() error { return nil }

// An RDS callback already queued when the listener stops must not create new
// resources after shutdown cleanup has completed.
func (s) TestRouteConfigurationUpdateAfterListenerClose(t *testing.T) {
	b := &lifecycleFilterBuilder{}
	fc := lifecycleFilterChain(b)
	l := &listenerWrapper{
		Listener:                 lifecycleListener{},
		closed:                   grpcsync.NewEvent(),
		activeFilterChainManager: &filterChainManager{filterChains: []*filterChain{fc}},
		httpFilters:              make(map[serverFilterKey]*refCountedServerFilter),
		rdsHandler:               &rdsHandler{},
	}
	update := rdsWatcherUpdate{data: lifecycleRouteConfig()}
	l.handleRDSUpdate("route", update)
	if err := l.Close(); err != nil {
		t.Fatal(err)
	}
	l.handleRDSUpdate("route", update)
	if got := b.buildCalls; got != 8 {
		t.Errorf("Built %d interceptors, want 8 from the initial configuration only", got)
	}
	b.checkClosed(t)
}

type blockingLifecycleInterceptor struct {
	entered chan struct{}
	unblock chan struct{}
	closed  chan struct{}
}

func (i *blockingLifecycleInterceptor) AllowRPC(ctx context.Context) error {
	close(i.entered)
	select {
	case <-i.unblock:
	case <-ctx.Done():
		return ctx.Err()
	}
	select {
	case <-i.closed:
		return errors.New("interceptor closed while processing RPC")
	default:
		return nil
	}
}

func (i *blockingLifecycleInterceptor) Close() { close(i.closed) }

type lifecycleTransportStream struct{ grpc.ServerTransportStream }

func (lifecycleTransportStream) Method() string { return "/test/service" }

// An update must publish the new configuration without closing interceptors
// that an RPC is still using from the old configuration.
func (s) TestRouteConfigurationUpdateDuringRPC(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), defaultTestTimeout)
	defer cancel()
	i := &blockingLifecycleInterceptor{
		entered: make(chan struct{}),
		unblock: make(chan struct{}, 1),
		closed:  make(chan struct{}),
	}
	defer close(i.unblock)
	config := func(interceptor resolver.ServerInterceptor) *usableRouteConfiguration {
		return &usableRouteConfiguration{vhs: []virtualHostWithInterceptors{{
			domains: []string{"*"},
			routes: []routeWithInterceptors{{
				matcher:     xdsresource.RouteToMatcher(&xdsresource.Route{Prefix: newStringP("/")}),
				actionType:  xdsresource.RouteActionNonForwardingAction,
				interceptor: interceptor,
			}},
		}}}
	}
	fc := lifecycleFilterChain(&lifecycleFilterBuilder{})
	fc.updateRouteConfiguration(config(i))
	ctx = transport.SetConnection(ctx, &connWrapper{urc: fc.usableRouteConfiguration})
	ctx = grpc.NewContextWithServerTransportStream(ctx, lifecycleTransportStream{})
	ctx = metadata.NewIncomingContext(ctx, metadata.Pairs(":authority", "test"))
	rpcDone := make(chan error, 1)
	go func() { rpcDone <- RouteAndProcess(ctx) }()
	select {
	case <-i.entered:
	case <-ctx.Done():
		t.Fatal("Timeout waiting for RPC to enter interceptor")
	}
	updated := config(&interceptorList{})
	updateDone := make(chan struct{})
	go func() {
		fc.updateRouteConfiguration(updated)
		close(updateDone)
	}()
	for fc.usableRouteConfiguration.Load() != updated {
		select {
		case <-time.After(time.Millisecond):
		case <-ctx.Done():
			t.Fatal("Timeout waiting for new routing configuration")
		}
	}
	if err := RouteAndProcess(ctx); err != nil {
		t.Fatalf("RPC using updated route failed: %v", err)
	}
	select {
	case <-i.closed:
		t.Error("Old interceptor closed while still processing an RPC")
	default:
	}
	i.unblock <- struct{}{}
	select {
	case err := <-rpcDone:
		if err != nil {
			t.Errorf("RPC using old route failed: %v", err)
		}
	case <-ctx.Done():
		t.Fatal("Timeout waiting for RPC using old route")
	}
	select {
	case <-updateDone:
	case <-ctx.Done():
		t.Fatal("Timeout waiting for route update to finish")
	}
	select {
	case <-i.closed:
	default:
		t.Error("Old interceptor was not closed after RPC finished")
	}
}
