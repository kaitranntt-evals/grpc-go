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
 */

package server

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/internal/resolver"
	"google.golang.org/grpc/internal/transport"
	"google.golang.org/grpc/internal/xds/httpfilter"
	"google.golang.org/grpc/internal/xds/xdsclient/xdsresource"
	"google.golang.org/grpc/metadata"
)

type cleanupFilterBuilder struct {
	httpfilter.Builder
	t            *testing.T
	buildCalls   int
	failOnBuild  int
	filters      []*cleanupServerFilter
	interceptors []*cleanupInterceptor
}

func (*cleanupFilterBuilder) TypeURLs() []string { return []string{"cleanup-filter"} }

func (b *cleanupFilterBuilder) BuildServerFilter() httpfilter.ServerFilter {
	f := &cleanupServerFilter{builder: b}
	b.filters = append(b.filters, f)
	return f
}

type cleanupServerFilter struct {
	builder *cleanupFilterBuilder
	closes  int
}

func (f *cleanupServerFilter) BuildServerInterceptor(httpfilter.FilterConfig, httpfilter.FilterConfig) (resolver.ServerInterceptor, error) {
	if f.closes != 0 {
		f.builder.t.Error("Building an interceptor on a closed filter")
	}
	f.builder.buildCalls++
	if f.builder.buildCalls == f.builder.failOnBuild {
		return nil, errors.New("interceptor construction failed")
	}
	i := &cleanupInterceptor{filter: f}
	f.builder.interceptors = append(f.builder.interceptors, i)
	return i, nil
}

func (f *cleanupServerFilter) Close() { f.closes++ }

type cleanupInterceptor struct {
	filter *cleanupServerFilter
	closes int
}

func (*cleanupInterceptor) AllowRPC(context.Context) error { return nil }

func (i *cleanupInterceptor) Close() {
	if i.filter.closes != 0 {
		i.filter.builder.t.Error("Closing an interceptor after its filter was closed")
	}
	i.closes++
}

func (s) TestRouteConfigurationCleanup(t *testing.T) {
	for _, test := range []struct {
		name        string
		failOnBuild int
		resourceErr bool
		providerErr bool
		empty       bool
	}{
		{name: "replacement"},
		{name: "empty", empty: true},
		{name: "resource_error", resourceErr: true},
		{name: "first_route_error", failOnBuild: 2},
		{name: "later_route_error", failOnBuild: 4},
		{name: "later_virtual_host_error", failOnBuild: 8},
		{name: "provider_error", providerErr: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			builder := &cleanupFilterBuilder{t: t}
			fc := (&filterChainManager{}).filterChainFromConfig(&xdsresource.NetworkFilterChainConfig{
				HTTPConnMgr: &xdsresource.HTTPConnectionManagerConfig{
					RouteConfigName: "route",
					HTTPFilters: []xdsresource.HTTPFilter{
						{Name: "first", Filter: builder},
						{Name: "second", Filter: builder},
					},
				},
			})
			fcm := &filterChainManager{filterChains: []*filterChain{fc}}
			defer fcm.stop()
			l := &listenerWrapper{
				activeFilterChainManager: fcm,
				httpFilters:              make(map[serverFilterKey]*refCountedServerFilter),
				rdsHandler:               &rdsHandler{},
			}
			route := &xdsresource.Route{Prefix: newStringP("/")}
			config := &xdsresource.RouteConfigUpdate{VirtualHosts: []*xdsresource.VirtualHost{
				{Domains: []string{"*"}, Routes: []*xdsresource.Route{route, route}},
				{Domains: []string{"unused.example.com"}, Routes: []*xdsresource.Route{route, route}},
			}}
			l.handleRDSUpdate("route", rdsWatcherUpdate{data: config})
			if got := len(builder.interceptors); got != 8 {
				t.Fatalf("Created %d interceptors, want 8", got)
			}
			initialInterceptors := builder.interceptors

			update := rdsWatcherUpdate{data: config}
			switch {
			case test.resourceErr:
				update = rdsWatcherUpdate{err: errors.New("route removed")}
			case test.empty:
				update.data = &xdsresource.RouteConfigUpdate{}
			case test.failOnBuild != 0:
				builder.failOnBuild = builder.buildCalls + test.failOnBuild
			case test.providerErr:
				fc.httpFilters = append(fc.httpFilters, xdsresource.HTTPFilter{Name: "unsupported", Filter: &filterBuilderWithoutServerSupport{}})
			}
			l.handleRDSUpdate("route", update)
			for n, i := range initialInterceptors {
				if i.closes != 1 {
					t.Errorf("Superseded interceptor %d closed %d times, want 1", n, i.closes)
				}
			}
			wantErr := test.resourceErr || test.failOnBuild != 0 || test.providerErr
			if got := fc.usableRouteConfiguration.Load().err; (got != nil) != wantErr {
				t.Fatalf("Route configuration error = %v, want error: %v", got, wantErr)
			}
			if wantErr || test.empty {
				// This also checks interceptors built before a later route or
				// virtual host failed, and references held by the old config.
				for n, i := range builder.interceptors {
					if i.closes != 1 {
						t.Errorf("Interceptor %d closed %d times, want 1", n, i.closes)
					}
				}
				for n, f := range builder.filters {
					if f.closes != 1 {
						t.Errorf("Filter %d closed %d times, want 1", n, f.closes)
					}
				}
			} else {
				for n, f := range builder.filters {
					if f.closes != 0 {
						t.Errorf("Retained filter %d closed %d times, want 0", n, f.closes)
					}
				}
			}

			// Recover from an empty or erroneous configuration. Closed filter
			// instances must not be reused when routing resumes.
			fc.httpFilters = fc.httpFilters[:2]
			l.handleRDSUpdate("route", rdsWatcherUpdate{data: config})
			fcm.stop()
			fcm.stop()
			for n, i := range builder.interceptors {
				if i.closes != 1 {
					t.Errorf("After stop: interceptor %d closed %d times, want 1", n, i.closes)
				}
			}
			for n, f := range builder.filters {
				if f.closes != 1 {
					t.Errorf("After stop: filter %d closed %d times, want 1", n, f.closes)
				}
			}
		})
	}
}

type filterBuilderWithoutServerSupport struct{ httpfilter.Builder }

type blockingCleanupInterceptor struct {
	entered chan struct{}
	resume  chan struct{}
	closes  atomic.Int32
}

func (i *blockingCleanupInterceptor) AllowRPC(ctx context.Context) error {
	close(i.entered)
	select {
	case <-i.resume:
	case <-ctx.Done():
		return ctx.Err()
	}
	if i.closes.Load() != 0 {
		return errors.New("interceptor closed while processing an RPC")
	}
	return nil
}

func (i *blockingCleanupInterceptor) Close() { i.closes.Add(1) }

type routingTestStream struct{ grpc.ServerTransportStream }

func (routingTestStream) Method() string { return "/service/method" }

// Cleanup of a superseded config must wait for RPCs using its interceptors,
// while allowing new RPCs to use the replacement config on the same connection.
func (s) TestRouteConfigurationCleanup_ConcurrentRPC(t *testing.T) {
	i := &blockingCleanupInterceptor{entered: make(chan struct{}), resume: make(chan struct{})}
	resume := sync.OnceFunc(func() { close(i.resume) })
	defer resume()
	config := &usableRouteConfiguration{vhs: []virtualHostWithInterceptors{{
		domains: []string{"*"},
		routes: []routeWithInterceptors{{
			matcher:     xdsresource.RouteToMatcher(&xdsresource.Route{Prefix: newStringP("/")}),
			actionType:  xdsresource.RouteActionNonForwardingAction,
			interceptor: i,
		}},
	}}}
	fc := &filterChain{usableRouteConfiguration: &atomic.Pointer[usableRouteConfiguration]{}}
	fc.usableRouteConfiguration.Store(config)
	next := &usableRouteConfiguration{vhs: []virtualHostWithInterceptors{{
		domains: []string{"*"},
		routes: []routeWithInterceptors{{
			matcher:     config.vhs[0].routes[0].matcher,
			actionType:  xdsresource.RouteActionNonForwardingAction,
			interceptor: &interceptorList{},
		}},
	}}}
	ctx, cancel := context.WithTimeout(context.Background(), defaultTestTimeout)
	defer cancel()
	ctx = transport.SetConnection(ctx, &connWrapper{urc: fc.usableRouteConfiguration})
	ctx = metadata.NewIncomingContext(ctx, metadata.Pairs(":authority", "example.com"))
	ctx = grpc.NewContextWithServerTransportStream(ctx, routingTestStream{})
	rpcDone := make(chan error, 1)
	go func() { rpcDone <- RouteAndProcess(ctx) }()
	select {
	case <-i.entered:
	case <-ctx.Done():
		t.Fatal("Timeout waiting for RPC to enter the interceptor")
	}
	updateDone := make(chan struct{})
	go func() {
		fc.updateUsableRouteConfiguration(next)
		close(updateDone)
	}()
	for fc.usableRouteConfiguration.Load() != next {
		select {
		case <-time.After(time.Millisecond):
		case <-ctx.Done():
			t.Fatal("Timeout waiting for replacement route configuration")
		}
	}
	if err := RouteAndProcess(ctx); err != nil {
		t.Errorf("RPC using replacement route configuration failed: %v", err)
	}
	if got := i.closes.Load(); got != 0 {
		t.Errorf("Interceptor closed %d times while an RPC was still using it", got)
	}
	resume()
	select {
	case err := <-rpcDone:
		if err != nil {
			t.Errorf("RPC using superseded route configuration failed: %v", err)
		}
	case <-ctx.Done():
		t.Fatal("Timeout waiting for RPC to finish")
	}
	select {
	case <-updateDone:
	case <-ctx.Done():
		t.Fatal("Timeout waiting for superseded route configuration cleanup")
	}
	if got := i.closes.Load(); got != 1 {
		t.Errorf("Interceptor closed %d times after RPC completed, want 1", got)
	}
	fc.stop()
}
