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

type lifecycleFilterBuilder struct {
	httpfilter.Builder
	filters      []*lifecycleFilter
	interceptors []*lifecycleInterceptor
	attempts     int
	failAt       int
	nilAt        int
}

func (*lifecycleFilterBuilder) TypeURLs() []string { return []string{"lifecycle-filter"} }

func (b *lifecycleFilterBuilder) BuildServerFilter() httpfilter.ServerFilter {
	f := &lifecycleFilter{builder: b}
	b.filters = append(b.filters, f)
	return f
}

type lifecycleFilter struct {
	builder *lifecycleFilterBuilder
	closed  int
}

func (f *lifecycleFilter) BuildServerInterceptor(httpfilter.FilterConfig, httpfilter.FilterConfig) (resolver.ServerInterceptor, error) {
	if f.closed != 0 {
		return nil, errors.New("building interceptor on a closed filter")
	}
	b := f.builder
	b.attempts++
	if b.attempts == b.failAt {
		return nil, errors.New("interceptor construction failed")
	}
	if b.attempts == b.nilAt {
		return nil, nil
	}
	i := &lifecycleInterceptor{}
	b.interceptors = append(b.interceptors, i)
	return i, nil
}

func (f *lifecycleFilter) Close() { f.closed++ }

type lifecycleInterceptor struct {
	closed atomic.Int32
	allow  func(context.Context) error
}

func (i *lifecycleInterceptor) AllowRPC(ctx context.Context) error {
	if i.closed.Load() != 0 {
		return errors.New("using a closed interceptor")
	}
	if i.allow != nil {
		return i.allow(ctx)
	}
	return nil
}

func (i *lifecycleInterceptor) Close() { i.closed.Add(1) }

// Verify cleanup of both the superseded configuration and partially built
// replacements, including errors in later routes and later virtual hosts.
func (s) TestRouteConfigurationLifecycle(t *testing.T) {
	for _, test := range []struct {
		name          string
		failAt        int
		nilAt         int
		resourceError bool
	}{
		{name: "replacement"},
		{name: "nil_interceptor", nilAt: 2},
		{name: "first_filter_error", failAt: 1},
		{name: "later_filter_error", failAt: 2},
		{name: "later_route_error", failAt: 4},
		{name: "later_virtual_host_error", failAt: 6},
		{name: "resource_error", resourceError: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			builder := &lifecycleFilterBuilder{}
			fc := &filterChain{
				routeConfigName: "route",
				httpFilters: []xdsresource.HTTPFilter{
					{Name: "first", Filter: builder},
					{Name: "second", Filter: builder},
				},
				usableRouteConfiguration: &atomic.Pointer[usableRouteConfiguration]{},
			}
			fc.usableRouteConfiguration.Store(&usableRouteConfiguration{})
			fcm := &filterChainManager{filterChains: []*filterChain{fc}}
			l := &listenerWrapper{
				activeFilterChainManager: fcm,
				httpFilters:              make(map[serverFilterKey]*refCountedServerFilter),
				rdsHandler:               &rdsHandler{},
			}
			defer fcm.stop()
			prefix := "/"
			route := &xdsresource.Route{Prefix: &prefix, ActionType: xdsresource.RouteActionNonForwardingAction}
			config := &xdsresource.RouteConfigUpdate{VirtualHosts: []*xdsresource.VirtualHost{
				{Domains: []string{"first"}, Routes: []*xdsresource.Route{route, route}},
				{Domains: []string{"second"}, Routes: []*xdsresource.Route{route, route}},
			}}
			l.handleRDSUpdate("route", rdsWatcherUpdate{data: config})
			if err := fc.usableRouteConfiguration.Load().err; err != nil {
				t.Fatalf("Initial configuration failed: %v", err)
			}
			oldInterceptors := len(builder.interceptors)
			if test.failAt != 0 {
				builder.failAt = builder.attempts + test.failAt
			}
			if test.nilAt != 0 {
				builder.nilAt = builder.attempts + test.nilAt
			}
			update := rdsWatcherUpdate{data: config}
			if test.resourceError {
				update = rdsWatcherUpdate{err: errors.New("route resource removed")}
			}
			l.handleRDSUpdate("route", update)
			wantError := test.failAt != 0 || test.resourceError
			if err := fc.usableRouteConfiguration.Load().err; (err != nil) != wantError {
				t.Fatalf("Replacement error = %v, want error: %v", err, wantError)
			}
			for n, i := range builder.interceptors {
				var want int32
				if n < oldInterceptors || wantError {
					want = 1
				}
				if got := i.closed.Load(); got != want {
					t.Errorf("Interceptor %d closed %d times after replacement, want %d", n, got, want)
				}
			}
			for n, f := range builder.filters {
				want := 0
				if wantError {
					want = 1
				}
				if f.closed != want {
					t.Errorf("Filter %d closed %d times after replacement, want %d", n, f.closed, want)
				}
			}
			// Recover from an error without reusing a filter that was closed
			// when the last usable routing configuration was released.
			l.handleRDSUpdate("route", rdsWatcherUpdate{data: config})
			if err := fc.usableRouteConfiguration.Load().err; err != nil {
				t.Fatalf("Recovery configuration failed: %v", err)
			}
			fcm.stop()
			fcm.stop()
			for n, i := range builder.interceptors {
				if got := i.closed.Load(); got != 1 {
					t.Errorf("Interceptor %d closed %d times after stop, want 1", n, got)
				}
			}
			for n, f := range builder.filters {
				if f.closed != 1 {
					t.Errorf("Filter %d closed %d times after stop, want 1", n, f.closed)
				}
			}
		})
	}
}

type lifecycleTransportStream struct {
	grpc.ServerTransportStream
}

func (lifecycleTransportStream) Method() string { return "/service/method" }

// A route replacement must be usable immediately, while an RPC already being
// checked against the previous configuration must finish before it is closed.
func (s) TestRouteConfigurationUpdateDuringRPC(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), defaultTestTimeout)
	defer cancel()
	entered := make(chan struct{})
	unblock := make(chan struct{})
	builder := &lifecycleFilterBuilder{}
	fc := &filterChain{
		routeConfigName:          "route",
		httpFilters:              []xdsresource.HTTPFilter{{Name: "filter", Filter: builder}},
		usableRouteConfiguration: &atomic.Pointer[usableRouteConfiguration]{},
	}
	fc.usableRouteConfiguration.Store(&usableRouteConfiguration{})
	l := &listenerWrapper{
		activeFilterChainManager: &filterChainManager{filterChains: []*filterChain{fc}},
		httpFilters:              make(map[serverFilterKey]*refCountedServerFilter),
		rdsHandler:               &rdsHandler{},
	}
	prefix := "/"
	config := &xdsresource.RouteConfigUpdate{VirtualHosts: []*xdsresource.VirtualHost{{
		Domains: []string{"*"},
		Routes:  []*xdsresource.Route{{Prefix: &prefix, ActionType: xdsresource.RouteActionNonForwardingAction}},
	}}}
	l.handleRDSUpdate("route", rdsWatcherUpdate{data: config})
	oldConfig := fc.usableRouteConfiguration.Load()
	oldInterceptor := builder.interceptors[0]
	oldInterceptor.allow = func(ctx context.Context) error {
		close(entered)
		select {
		case <-unblock:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	ctx = transport.SetConnection(ctx, &connWrapper{urc: fc.usableRouteConfiguration})
	ctx = grpc.NewContextWithServerTransportStream(ctx, lifecycleTransportStream{})
	ctx = metadata.NewIncomingContext(ctx, metadata.Pairs(":authority", "server"))
	rpcDone := make(chan error, 1)
	go func() { rpcDone <- RouteAndProcess(ctx) }()
	select {
	case <-entered:
	case <-ctx.Done():
		t.Fatal("Timeout waiting for RPC to enter interceptor")
	}
	updated := make(chan struct{})
	go func() {
		l.handleRDSUpdate("route", rdsWatcherUpdate{data: config})
		close(updated)
	}()
	for fc.usableRouteConfiguration.Load() == oldConfig && ctx.Err() == nil {
		time.Sleep(time.Millisecond)
	}
	if ctx.Err() != nil {
		t.Fatal("Timeout waiting for route replacement")
	}
	if err := RouteAndProcess(ctx); err != nil {
		t.Errorf("RPC on replacement configuration failed: %v", err)
	}
	if got := oldInterceptor.closed.Load(); got != 0 {
		t.Errorf("Old interceptor closed %d times while in use, want 0", got)
	}
	close(unblock)
	select {
	case err := <-rpcDone:
		if err != nil {
			t.Errorf("RPC on previous configuration failed: %v", err)
		}
	case <-ctx.Done():
		t.Fatal("Timeout waiting for original RPC to finish")
	}
	select {
	case <-updated:
	case <-ctx.Done():
		t.Fatal("Timeout waiting for route update cleanup")
	}
	if got := oldInterceptor.closed.Load(); got != 1 {
		t.Errorf("Old interceptor closed %d times after RPC finished, want 1", got)
	}
	newInterceptor := builder.interceptors[1]
	if got := newInterceptor.closed.Load(); got != 0 {
		t.Errorf("New interceptor closed %d times while active, want 0", got)
	}
	fc.stop()
	if got := newInterceptor.closed.Load(); got != 1 {
		t.Errorf("New interceptor closed %d times after stop, want 1", got)
	}
}
