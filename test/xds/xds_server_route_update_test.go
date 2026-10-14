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

package xds_test

import (
	"context"
	"fmt"
	"net"
	"sync/atomic"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/connectivity"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/internal/testutils"
	"google.golang.org/grpc/internal/testutils/xds/e2e"
	"google.golang.org/grpc/internal/testutils/xds/e2e/setup"
	"google.golang.org/grpc/internal/xds/httpfilter"
	testgrpc "google.golang.org/grpc/interop/grpc_testing"
	testpb "google.golang.org/grpc/interop/grpc_testing"
	"google.golang.org/grpc/xds"
	"google.golang.org/protobuf/types/known/anypb"

	v3listenerpb "github.com/envoyproxy/go-control-plane/envoy/config/listener/v3"
	v3routepb "github.com/envoyproxy/go-control-plane/envoy/config/route/v3"
	v3httppb "github.com/envoyproxy/go-control-plane/envoy/extensions/filters/network/http_connection_manager/v3"
)

// Route-only updates must release the previous interceptors while retaining the
// filter instances and continuing to serve RPCs on the existing connection.
func (s) TestServerSideXDS_FilterStateRetention_RouteUpdates(t *testing.T) {
	var filtersCreated, filtersDestroyed, interceptorsCreated, interceptorsDestroyed atomic.Int32
	pathCh := make(chan string, 1)
	fb := &trackingHTTPFilterBuilder{
		filtersCreated:        &filtersCreated,
		filtersDestroyed:      &filtersDestroyed,
		interceptorsCreated:   &interceptorsCreated,
		interceptorsDestroyed: &interceptorsDestroyed,
		typeURL:               t.Name(),
		pathCh:                pathCh,
	}
	httpfilter.Register(fb)
	defer httpfilter.UnregisterForTesting(fb.typeURL)

	managementServer, nodeID, bootstrapContents, _ := setup.ManagementServerAndResolver(t)
	servingCh := make(chan struct{})
	lis, stopServer := setupGRPCServer(t, bootstrapContents, xds.ServingModeCallback(func(_ net.Addr, args xds.ServingModeChangeArgs) {
		if args.Mode == connectivity.ServingModeServing {
			close(servingCh)
		}
	}))
	defer stopServer()
	host, port, err := hostPortFromListener(lis)
	if err != nil {
		t.Fatal(err)
	}
	const routeName = "server-route"
	listener := e2e.DefaultServerListenerWithRouteConfigName(host, port, e2e.SecurityLevelNone, routeName)
	for _, fc := range listener.FilterChains {
		hcm := &v3httppb.HttpConnectionManager{}
		if err := fc.Filters[0].GetTypedConfig().UnmarshalTo(hcm); err != nil {
			t.Fatal(err)
		}
		hcm.HttpFilters = append([]*v3httppb.HttpFilter{newHTTPFilter(t, "tracker", fb.typeURL, "base")}, hcm.HttpFilters...)
		fc.Filters[0].ConfigType = &v3listenerpb.Filter_TypedConfig{TypedConfig: testutils.MarshalAny(t, hcm)}
	}
	resources := e2e.UpdateOptions{NodeID: nodeID, Listeners: []*v3listenerpb.Listener{listener}}
	ctx, cancel := context.WithTimeout(context.Background(), defaultTestTimeout)
	defer cancel()

	// Count connection attempts to ensure RDS updates do not drain the listener.
	var dials atomic.Int32
	cc, err := grpc.NewClient(lis.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()), grpc.WithContextDialer(func(ctx context.Context, addr string) (net.Conn, error) {
		dials.Add(1)
		return (&net.Dialer{}).DialContext(ctx, "tcp", addr)
	}))
	if err != nil {
		t.Fatal(err)
	}
	defer cc.Close()
	client := testgrpc.NewTestServiceClient(cc)
	const updates = 10
	for update := range updates {
		path := fmt.Sprintf("route-update-%d", update)
		route := e2e.RouteConfigNonForwardingAction(routeName)
		route.VirtualHosts[0].Routes[0].TypedPerFilterConfig = map[string]*anypb.Any{
			"tracker": newHTTPFilter(t, "tracker", fb.typeURL, path).GetTypedConfig(),
		}
		resources.Routes = []*v3routepb.RouteConfiguration{route}
		if err := managementServer.Update(ctx, resources); err != nil {
			t.Fatal(err)
		}
		if update == 0 {
			select {
			case <-servingCh:
			case <-ctx.Done():
				t.Fatal("Timeout waiting for server to enter SERVING mode")
			}
		}
		for {
			if _, err := client.EmptyCall(ctx, &testpb.Empty{}); err != nil {
				t.Fatalf("EmptyCall() during route update %d failed: %v", update, err)
			}
			select {
			case got := <-pathCh:
				if got != path {
					continue
				}
			case <-ctx.Done():
				t.Fatal("Timeout waiting for interceptor invocation")
			}
			break
		}
		// Seeing the new interceptor does not guarantee that cleanup, which
		// follows publication of the new configuration, has finished yet.
		wantClosed := int32(update * len(listener.FilterChains))
		for interceptorsDestroyed.Load() != wantClosed && ctx.Err() == nil {
			time.Sleep(time.Millisecond)
		}
		if got := interceptorsDestroyed.Load(); got != wantClosed {
			t.Fatalf("After route update %d, closed %d interceptors, want %d", update, got, wantClosed)
		}
		if got, want := interceptorsCreated.Load(), int32((update+1)*len(listener.FilterChains)); got != want {
			t.Fatalf("Created %d interceptors, want %d", got, want)
		}
		if got := filtersCreated.Load(); got != 1 {
			t.Fatalf("Created %d filters, want 1", got)
		}
		if got := filtersDestroyed.Load(); got != 0 {
			t.Fatalf("Closed %d filters while still serving, want 0", got)
		}
	}
	if got := dials.Load(); got != 1 {
		t.Errorf("Made %d connection attempts, want 1", got)
	}
	stopServer()
	if got, want := interceptorsDestroyed.Load(), interceptorsCreated.Load(); got != want {
		t.Errorf("Closed %d interceptors after shutdown, want %d", got, want)
	}
	if got := filtersDestroyed.Load(); got != 1 {
		t.Errorf("Closed %d filters after shutdown, want 1", got)
	}
}
