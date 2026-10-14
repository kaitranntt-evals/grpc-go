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
	"sync/atomic"
	"testing"
	"time"

	"google.golang.org/grpc"
	iresolver "google.golang.org/grpc/internal/resolver"
	"google.golang.org/grpc/internal/transport"
	"google.golang.org/grpc/internal/xds/httpfilter"
	"google.golang.org/grpc/internal/xds/xdsclient/xdsresource"
	"google.golang.org/grpc/metadata"
)

type cleanupTestFilter struct {
	build func() (iresolver.ServerInterceptor, error)
	close func()
}

func (f *cleanupTestFilter) BuildServerInterceptor(httpfilter.FilterConfig, httpfilter.FilterConfig) (iresolver.ServerInterceptor, error) {
	return f.build()
}

func (f *cleanupTestFilter) Close() { f.close() }

type cleanupTestInterceptor struct {
	allow func(context.Context) error
	close func()
}

func (i *cleanupTestInterceptor) AllowRPC(ctx context.Context) error { return i.allow(ctx) }
func (i *cleanupTestInterceptor) Close()                             { i.close() }

// A failure can occur after constructing other filters on the same route,
// other routes in the same virtual host, or other virtual hosts. All resources
// created before the failure must be released exactly once.
func (s) TestHTTPFilterCleanupOnConstructionFailure(t *testing.T) {
	config := xdsresource.RouteConfigUpdate{}
	for range 2 {
		config.VirtualHosts = append(config.VirtualHosts, &xdsresource.VirtualHost{
			Domains: []string{"*"},
			Routes: []*xdsresource.Route{
				{Prefix: newStringP("/")},
				{Prefix: newStringP("/")},
			},
		})
	}
	for _, failure := range []string{"provider", "interceptor"} {
		for failAt := 1; failAt <= 8; failAt++ {
			t.Run(fmt.Sprintf("%s/%d", failure, failAt), func(t *testing.T) {
				fc := &filterChain{httpFilters: []xdsresource.HTTPFilter{{Name: "first"}, {Name: "second"}}}
				calls := 0
				closed := make(map[string]int)
				provider := func(xdsresource.HTTPFilter) (httpfilter.ServerFilter, error) {
					calls++
					n := calls
					if failure == "provider" && n == failAt {
						return nil, errors.New("provider failed")
					}
					filterName := fmt.Sprintf("filter-%d", n)
					interceptorName := fmt.Sprintf("interceptor-%d", n)
					closed[filterName] = 0
					return &cleanupTestFilter{
						build: func() (iresolver.ServerInterceptor, error) {
							if failure == "interceptor" && n == failAt {
								return nil, errors.New("interceptor failed")
							}
							closed[interceptorName] = 0
							return &cleanupTestInterceptor{close: func() { closed[interceptorName]++ }}, nil
						},
						close: func() {
							if count, ok := closed[interceptorName]; ok && count != 1 {
								t.Errorf("Closing %s before its interceptor was closed", filterName)
							}
							closed[filterName]++
						},
					}, nil
				}
				rc := fc.constructUsableRouteConfiguration(config, provider)
				if rc.err == nil {
					t.Fatal("Route configuration construction succeeded, want error")
				}
				for name, count := range closed {
					if count != 1 {
						t.Errorf("%s closed %d times, want 1", name, count)
					}
				}
			})
		}
	}
}

type cleanupTestFilterBuilder struct {
	filterBuilder
	build func() httpfilter.ServerFilter
}

func (b *cleanupTestFilterBuilder) BuildServerFilter() httpfilter.ServerFilter { return b.build() }

// Replacing a valid route with an error must release its resources. Recovery
// must create a usable filter instead of reusing the closed cached instance.
func (s) TestRouteConfigurationCleanupOnError(t *testing.T) {
	for _, constructionError := range []bool{false, true} {
		t.Run(fmt.Sprintf("constructionError=%v", constructionError), func(t *testing.T) {
			var filtersCreated, filtersClosed, interceptorsClosed int
			failBuild := false
			builder := &cleanupTestFilterBuilder{build: func() httpfilter.ServerFilter {
				filtersCreated++
				closed := false
				return &cleanupTestFilter{
					build: func() (iresolver.ServerInterceptor, error) {
						if failBuild {
							return nil, errors.New("interceptor construction failed")
						}
						if closed {
							t.Error("Building interceptor on a closed filter")
						}
						return &cleanupTestInterceptor{close: func() { interceptorsClosed++ }}, nil
					},
					close: func() {
						closed = true
						filtersClosed++
					},
				}
			}}
			fcm := newFilterChainManager(nil, &xdsresource.NetworkFilterChainConfig{
				HTTPConnMgr: &xdsresource.HTTPConnectionManagerConfig{
					RouteConfigName: "route",
					HTTPFilters:     []xdsresource.HTTPFilter{{Name: "filter", Filter: builder}},
				},
			})
			lw := &listenerWrapper{
				activeFilterChainManager: fcm,
				httpFilters:              make(map[serverFilterKey]*refCountedServerFilter),
				rdsHandler:               &rdsHandler{},
			}
			defer fcm.stop()
			update := rdsWatcherUpdate{data: &xdsresource.RouteConfigUpdate{
				VirtualHosts: []*xdsresource.VirtualHost{{
					Domains: []string{"*"},
					Routes:  []*xdsresource.Route{{Prefix: newStringP("/")}},
				}},
			}}
			lw.handleRDSUpdate("route", update)
			if constructionError {
				failBuild = true
				lw.handleRDSUpdate("route", update)
				failBuild = false
			} else {
				lw.handleRDSUpdate("route", rdsWatcherUpdate{err: errors.New("resource not found")})
			}
			if filtersClosed != 1 || interceptorsClosed != 1 {
				t.Fatalf("After error: closed %d filters and %d interceptors, want 1 each", filtersClosed, interceptorsClosed)
			}
			lw.handleRDSUpdate("route", update)
			if filtersCreated != 2 {
				t.Fatalf("After recovery: created %d filters, want 2", filtersCreated)
			}
			fcm.stop()
			fcm.stop()
			if filtersClosed != 2 || interceptorsClosed != 2 {
				t.Fatalf("After stop: closed %d filters and %d interceptors, want 2 each", filtersClosed, interceptorsClosed)
			}
		})
	}
}

type routingTestStream struct{ grpc.ServerTransportStream }

func (routingTestStream) Method() string { return "/service/method" }

// An update must not close an interceptor while RouteAndProcess is using it.
// The new configuration must remain available to other requests in the meantime.
func (s) TestRouteConfigurationUpdateDuringRPC(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), defaultTestTimeout)
	defer cancel()
	entered := make(chan struct{})
	release := make(chan struct{}, 1)
	defer close(release)
	var closed atomic.Bool
	oldInterceptor := &cleanupTestInterceptor{
		allow: func(ctx context.Context) error {
			close(entered)
			select {
			case <-release:
			case <-ctx.Done():
				return ctx.Err()
			}
			if closed.Load() {
				return errors.New("interceptor closed while processing RPC")
			}
			return nil
		},
		close: func() { closed.Store(true) },
	}
	newConfig := func(i iresolver.ServerInterceptor) *usableRouteConfiguration {
		return &usableRouteConfiguration{vhs: []virtualHostWithInterceptors{{
			domains: []string{"*"},
			routes: []routeWithInterceptors{{
				matcher:     xdsresource.RouteToMatcher(&xdsresource.Route{Prefix: newStringP("/")}),
				actionType:  xdsresource.RouteActionNonForwardingAction,
				interceptor: i,
			}},
		}}}
	}
	fc := &filterChain{usableRouteConfiguration: &atomic.Pointer[usableRouteConfiguration]{}}
	fc.usableRouteConfiguration.Store(newConfig(oldInterceptor))
	ctx = transport.SetConnection(ctx, &connWrapper{urc: fc.usableRouteConfiguration})
	ctx = grpc.NewContextWithServerTransportStream(ctx, routingTestStream{})
	ctx = metadata.NewIncomingContext(ctx, metadata.Pairs(":authority", "server"))
	rpcDone := make(chan error, 1)
	go func() { rpcDone <- RouteAndProcess(ctx) }()
	select {
	case <-entered:
	case <-ctx.Done():
		t.Fatal("Timeout waiting for RPC to enter interceptor")
	}
	updated := make(chan struct{})
	newRC := newConfig(&cleanupTestInterceptor{allow: func(context.Context) error { return nil }, close: func() {}})
	go func() {
		fc.updateUsableRouteConfiguration(newRC)
		close(updated)
	}()
	for fc.usableRouteConfiguration.Load() != newRC && ctx.Err() == nil {
		time.Sleep(time.Millisecond)
	}
	if ctx.Err() != nil {
		t.Fatal("Timeout waiting for new route configuration")
	}
	if err := RouteAndProcess(ctx); err != nil {
		t.Fatalf("RPC on new configuration failed: %v", err)
	}
	select {
	case <-updated:
		t.Fatal("Update closed old interceptor while RPC was still using it")
	case <-time.After(defaultTestShortTimeout):
	}
	release <- struct{}{}
	select {
	case err := <-rpcDone:
		if err != nil {
			t.Fatalf("RPC on old configuration failed: %v", err)
		}
	case <-ctx.Done():
		t.Fatal("Timeout waiting for RPC completion")
	}
	select {
	case <-updated:
	case <-ctx.Done():
		t.Fatal("Timeout waiting for update completion")
	}
	if !closed.Load() {
		t.Fatal("Old interceptor was not closed after RPC completed")
	}
	fc.stop()
}
