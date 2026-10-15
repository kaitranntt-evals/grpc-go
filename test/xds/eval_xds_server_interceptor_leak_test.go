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
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/connectivity"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/internal/channelz"
	"google.golang.org/grpc/internal/envconfig"
	"google.golang.org/grpc/internal/resolver"
	"google.golang.org/grpc/internal/stubserver"
	"google.golang.org/grpc/internal/testutils"
	"google.golang.org/grpc/internal/testutils/xds/e2e"
	"google.golang.org/grpc/internal/testutils/xds/e2e/setup"
	"google.golang.org/grpc/internal/xds/httpfilter"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
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
	"google.golang.org/protobuf/types/known/wrapperspb"
)

const evalFilterCfgPathFieldName = "path"

type evalTestFilterCfg struct {
	httpfilter.FilterConfig
	path string
}

func evalFilterConfigFromProto(cfg proto.Message) (httpfilter.FilterConfig, error) {
	ts, ok := cfg.(*v3xdsxdstypepb.TypedStruct)
	if !ok {
		return nil, fmt.Errorf("unsupported filter config type: %T, want %T", cfg, &v3xdsxdstypepb.TypedStruct{})
	}
	if ts.GetValue() == nil {
		return evalTestFilterCfg{}, nil
	}
	ret := evalTestFilterCfg{}
	if v := ts.GetValue().GetFields()[evalFilterCfgPathFieldName]; v != nil {
		ret.path = v.GetStringValue()
	}
	return ret, nil
}

type evalTrackingHTTPFilterBuilder struct {
	httpfilter.Builder
	filtersCreated        *atomic.Int32
	filtersDestroyed      *atomic.Int32
	interceptorsCreated   *atomic.Int32
	interceptorsDestroyed *atomic.Int32
	typeURL               string
	pathCh                chan string
}

func (t *evalTrackingHTTPFilterBuilder) IsTerminal() bool   { return false }
func (t *evalTrackingHTTPFilterBuilder) TypeURLs() []string { return []string{t.typeURL} }
func (*evalTrackingHTTPFilterBuilder) ParseFilterConfig(cfg proto.Message) (httpfilter.FilterConfig, error) {
	return evalFilterConfigFromProto(cfg)
}
func (*evalTrackingHTTPFilterBuilder) ParseFilterConfigOverride(cfg proto.Message) (httpfilter.FilterConfig, error) {
	return evalFilterConfigFromProto(cfg)
}
func (t *evalTrackingHTTPFilterBuilder) BuildServerFilter() httpfilter.ServerFilter {
	t.filtersCreated.Add(1)
	return t
}
func (t *evalTrackingHTTPFilterBuilder) Close() {
	t.filtersDestroyed.Add(1)
}
func (t *evalTrackingHTTPFilterBuilder) BuildServerInterceptor(config, override httpfilter.FilterConfig) (resolver.ServerInterceptor, error) {
	t.interceptorsCreated.Add(1)
	var effectiveCfg evalTestFilterCfg
	if override != nil {
		effectiveCfg = override.(evalTestFilterCfg)
	} else if config != nil {
		effectiveCfg = config.(evalTestFilterCfg)
	} else {
		return nil, fmt.Errorf("unexpected missing config")
	}
	return &evalTrackingInterceptor{
		parent:   t,
		pathCh:   t.pathCh,
		basePath: effectiveCfg.path,
	}, nil
}

type evalTrackingInterceptor struct {
	parent   *evalTrackingHTTPFilterBuilder
	pathCh   chan string
	basePath string
}

func (i *evalTrackingInterceptor) AllowRPC(context.Context) error {
	i.pathCh <- i.basePath
	return nil
}
func (i *evalTrackingInterceptor) Close() {
	i.parent.interceptorsDestroyed.Add(1)
}

func evalNewHTTPFilter(t *testing.T, name, typeURL, path string) *v3httppb.HttpFilter {
	return &v3httppb.HttpFilter{
		Name: name,
		ConfigType: &v3httppb.HttpFilter_TypedConfig{
			TypedConfig: testutils.MarshalAny(t, &v3xdsxdstypepb.TypedStruct{
				TypeUrl: typeURL,
				Value: &structpb.Struct{
					Fields: map[string]*structpb.Value{
						evalFilterCfgPathFieldName: {Kind: &structpb.Value_StringValue{StringValue: path}},
					},
				},
			}),
		},
	}
}

// Tests that interceptor instances are properly closed when a new RDS
// configuration is received for the same route configuration name.
func (s) TestEval_ServerSideXDS_InterceptorLeak_RDSUpdate(t *testing.T) {
	// Register a custom httpFilter builder for the test.
	var filtersCreated, filtersDestroyed, interceptorsCreated, interceptorsDestroyed atomic.Int32
	pathCh := make(chan string, 1)
	testFilterTypeURL := t.Name()
	fb := &evalTrackingHTTPFilterBuilder{
		filtersCreated:        &filtersCreated,
		filtersDestroyed:      &filtersDestroyed,
		interceptorsCreated:   &interceptorsCreated,
		interceptorsDestroyed: &interceptorsDestroyed,
		typeURL:               testFilterTypeURL,
		pathCh:                pathCh,
	}
	httpfilter.Register(fb)
	defer httpfilter.UnregisterForTesting(fb.typeURL)

	managementServer, nodeID, bootstrapContents, xdsResolver := setup.ManagementServerAndResolver(t)

	// Wait for the server to enter SERVING mode before making RPCs to avoid
	// flakes due to the server closing connections.
	servingCh := make(chan struct{})
	opt := xds.ServingModeCallback(func(_ net.Addr, args xds.ServingModeChangeArgs) {
		if args.Mode == connectivity.ServingModeServing {
			close(servingCh)
		}
	})
	lis, stopServer := setupGRPCServer(t, bootstrapContents, opt)
	defer stopServer()

	// Add xDS resources for the client.
	host, port, err := hostPortFromListener(lis)
	if err != nil {
		t.Fatalf("failed to retrieve host and port of server: %v", err)
	}
	const serviceName = "my-service"
	resources := e2e.DefaultClientResources(e2e.ResourceParams{
		DialTarget: serviceName,
		NodeID:     nodeID,
		Host:       host,
		Port:       port,
		SecLevel:   e2e.SecurityLevelNone,
	})

	// Add xDS resources for the server.
	vhs := []*v3routepb.VirtualHost{{
		Domains: []string{"*"},
		Routes: []*v3routepb.Route{{
			Match: &v3routepb.RouteMatch{
				PathSpecifier: &v3routepb.RouteMatch_Prefix{Prefix: "/grpc.testing.TestService/EmptyCall"},
			},
			Action: &v3routepb.Route_NonForwardingAction{},
		}},
	}}
	const routeConfigName = "routeName"
	rds1 := &v3routepb.RouteConfiguration{
		Name:         routeConfigName,
		VirtualHosts: vhs,
	}
	networkFilters := []*v3listenerpb.Filter{{
		Name: "hcm",
		ConfigType: &v3listenerpb.Filter_TypedConfig{
			TypedConfig: testutils.MarshalAny(t, &v3httppb.HttpConnectionManager{
				HttpFilters: []*v3httppb.HttpFilter{
					evalNewHTTPFilter(t, "tracker", testFilterTypeURL, "path1"),
					e2e.HTTPFilter("router", &v3routerpb.Router{}),
				},
				RouteSpecifier: &v3httppb.HttpConnectionManager_Rds{
					Rds: &v3httppb.Rds{
						ConfigSource: &v3corepb.ConfigSource{
							ConfigSourceSpecifier: &v3corepb.ConfigSource_Ads{Ads: &v3corepb.AggregatedConfigSource{}},
						},
						RouteConfigName: routeConfigName,
					},
				},
			}),
		},
	}}
	inboundLis := &v3listenerpb.Listener{
		Name: fmt.Sprintf(e2e.ServerListenerResourceNameTemplate, net.JoinHostPort(host, strconv.Itoa(int(port)))),
		Address: &v3corepb.Address{
			Address: &v3corepb.Address_SocketAddress{
				SocketAddress: &v3corepb.SocketAddress{
					Address: host,
					PortSpecifier: &v3corepb.SocketAddress_PortValue{
						PortValue: port,
					},
				},
			},
		},
		FilterChains: []*v3listenerpb.FilterChain{
			{
				Name: "v4-wildcard",
				FilterChainMatch: &v3listenerpb.FilterChainMatch{
					PrefixRanges: []*v3corepb.CidrRange{{
						AddressPrefix: "0.0.0.0",
						PrefixLen:     &wrapperspb.UInt32Value{Value: uint32(0)},
					}},
					SourceType: v3listenerpb.FilterChainMatch_SAME_IP_OR_LOOPBACK,
					SourcePrefixRanges: []*v3corepb.CidrRange{{
						AddressPrefix: "0.0.0.0",
						PrefixLen:     &wrapperspb.UInt32Value{Value: uint32(0)},
					}},
				},
				Filters: networkFilters,
			},
			{
				Name: "v6-wildcard",
				FilterChainMatch: &v3listenerpb.FilterChainMatch{
					PrefixRanges: []*v3corepb.CidrRange{{
						AddressPrefix: "::",
						PrefixLen:     &wrapperspb.UInt32Value{Value: uint32(0)},
					}},
					SourceType: v3listenerpb.FilterChainMatch_SAME_IP_OR_LOOPBACK,
					SourcePrefixRanges: []*v3corepb.CidrRange{{
						AddressPrefix: "::",
						PrefixLen:     &wrapperspb.UInt32Value{Value: uint32(0)},
					}},
				},
				Filters: networkFilters,
			},
		},
	}
	resources.Listeners = append(resources.Listeners, inboundLis)
	resources.Routes = append(resources.Routes, rds1)

	ctx, cancel := context.WithTimeout(context.Background(), defaultTestTimeout)
	defer cancel()
	if err := managementServer.Update(ctx, resources); err != nil {
		t.Fatal(err)
	}

	cc, err := grpc.NewClient(fmt.Sprintf("xds:///%s", serviceName), grpc.WithTransportCredentials(insecure.NewCredentials()), grpc.WithResolvers(xdsResolver))
	if err != nil {
		t.Fatalf("grpc.NewClient() failed: %v", err)
	}
	defer cc.Close()

	select {
	case <-servingCh:
	case <-ctx.Done():
		t.Fatalf("Timeout waiting for server to enter SERVING mode")
	}

	// Make an RPC and verify that one filter and two interceptors are created
	// (one per filter chain).
	client := testgrpc.NewTestServiceClient(cc)
	if _, err := client.EmptyCall(ctx, &testpb.Empty{}); err != nil {
		t.Fatalf("EmptyCall() failed: %v", err)
	}

	select {
	case cfg := <-pathCh:
		if got, want := cfg, "path1"; got != want {
			t.Fatalf("Unexpected config sent to filter, got: %q, want: %q", got, want)
		}
	case <-ctx.Done():
		t.Fatalf("Timeout waiting for interceptor to be invoked")
	}
	if got, want := filtersCreated.Load(), int32(1); got != want {
		t.Fatalf("Created %d filter instances, want: %d", got, want)
	}
	if got, want := interceptorsCreated.Load(), int32(2); got != want {
		t.Fatalf("Created %d interceptor instances, want: %d", got, want)
	}

	// Update the RDS configuration with a new route for the same route configuration
	// name. This should result in a new interceptor instance being created for
	// the new route.
	rds2 := &v3routepb.RouteConfiguration{
		Name: routeConfigName,
		VirtualHosts: []*v3routepb.VirtualHost{{
			Domains: []string{"*"},
			Routes: []*v3routepb.Route{
				{
					Name: "EmptyCall",
					Match: &v3routepb.RouteMatch{
						PathSpecifier: &v3routepb.RouteMatch_Prefix{Prefix: "/grpc.testing.TestService/EmptyCall"},
					},
					Action: &v3routepb.Route_NonForwardingAction{},
					TypedPerFilterConfig: map[string]*anypb.Any{
						"tracker": testutils.MarshalAny(t, &v3xdsxdstypepb.TypedStruct{
							TypeUrl: testFilterTypeURL,
							Value: &structpb.Struct{
								Fields: map[string]*structpb.Value{
									evalFilterCfgPathFieldName: {Kind: &structpb.Value_StringValue{StringValue: "path2"}},
								},
							},
						}),
					},
				},
				{
					Name: "Wildcard",
					Match: &v3routepb.RouteMatch{
						PathSpecifier: &v3routepb.RouteMatch_Prefix{Prefix: "/"},
					},
					Action: &v3routepb.Route_NonForwardingAction{},
				},
			},
		}},
	}
	resources.Routes = []*v3routepb.RouteConfiguration{resources.Routes[0], rds2}
	if err := managementServer.Update(ctx, resources); err != nil {
		t.Fatal(err)
	}

	// Wait for the updated config to be applied. Since the new configuration has
	// two routes, we expect two interceptor instances to be created for each
	// filter chain, for a total of 6 interceptor instances. We also wait for
	// the old interceptors (one per filter chain, two total) to be destroyed,
	// since destruction happens after the new interceptors are created within
	// the same RDS update callback. Breaking only on interceptorsCreated==6
	// would race with the subsequent interceptorsDestroyed increment.
	sawReplacementPath := false
	for ; ctx.Err() == nil; <-time.After(defaultTestShortTimeout) {
		if _, err := client.EmptyCall(ctx, &testpb.Empty{}); err != nil {
			t.Fatalf("EmptyCall() failed: %v", err)
		}
		select {
		case p := <-pathCh:
			if p == "path2" {
				sawReplacementPath = true
			}
		default:
		}
		if interceptorsCreated.Load() == 6 && interceptorsDestroyed.Load() == 2 && sawReplacementPath {
			break
		}
	}
	if ctx.Err() != nil {
		t.Fatalf("Timeout when waiting for updated config to be applied: %v", ctx.Err())
	}

	// Verify the filter instance is retained, while the interceptor instances
	// are replaced with the updated config.
	if got, want := filtersCreated.Load(), int32(1); got != want {
		t.Fatalf("Created %d filter instances, want: %d", got, want)
	}

	stopServer()
	for range 100 {
		if filtersDestroyed.Load() >= 1 && interceptorsDestroyed.Load() >= 6 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if got, want := filtersDestroyed.Load(), int32(1); got != want {
		t.Fatalf("Destroyed %d filter instances, want: %d", got, want)
	}
	if got, want := interceptorsDestroyed.Load(), int32(6); got != want {
		t.Fatalf("Destroyed %d interceptor instances, want: %d", got, want)
	}
}

// evalLifecycleFilterBuilder is a server-side HTTP filter builder that records the
// order in which interceptor instances are created and closed. It lets a test
// observe whether the configuration replacing an active one was fully
// constructed before the replaced configuration was stopped, and whether any
// interceptor instance was closed more than once.
type evalLifecycleFilterBuilder struct {
	httpfilter.Builder
	typeURL string

	mu           sync.Mutex
	events       []evalLifecycleEvent
	created      int
	chainCreated map[string]int
	interceptors []*evalLifecycleInterceptor
}

// evalLifecycleEvent records a single interceptor creation or closure together with
// the number of interceptor instances that had been created at that point.
type evalLifecycleEvent struct {
	kind            string // "create" or "close".
	chainID         string // Filter chain identifier (e.g. "v4" or "v6").
	createdSoFar    int
	createdForChain int
}

func (b *evalLifecycleFilterBuilder) IsTerminal() bool { return false }

func (b *evalLifecycleFilterBuilder) TypeURLs() []string { return []string{b.typeURL} }

func (*evalLifecycleFilterBuilder) ParseFilterConfig(cfg proto.Message) (httpfilter.FilterConfig, error) {
	return evalFilterConfigFromProto(cfg)
}

func (*evalLifecycleFilterBuilder) ParseFilterConfigOverride(cfg proto.Message) (httpfilter.FilterConfig, error) {
	return evalFilterConfigFromProto(cfg)
}

func (b *evalLifecycleFilterBuilder) BuildServerFilter() httpfilter.ServerFilter { return b }

func (b *evalLifecycleFilterBuilder) Close() {}

func (b *evalLifecycleFilterBuilder) BuildServerInterceptor(config, override httpfilter.FilterConfig) (resolver.ServerInterceptor, error) {
	chainID := "default"
	if config != nil {
		if c, ok := config.(evalTestFilterCfg); ok && c.path != "" {
			chainID = c.path
		}
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.created++
	if b.chainCreated == nil {
		b.chainCreated = make(map[string]int)
	}
	b.chainCreated[chainID]++
	forChain := b.chainCreated[chainID]
	interceptor := &evalLifecycleInterceptor{builder: b, chainID: chainID}
	b.events = append(b.events, evalLifecycleEvent{
		kind:            "create",
		chainID:         chainID,
		createdSoFar:    b.created,
		createdForChain: forChain,
	})
	b.interceptors = append(b.interceptors, interceptor)
	return interceptor, nil
}

func (b *evalLifecycleFilterBuilder) createdCount() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.created
}

func (b *evalLifecycleFilterBuilder) closedCount() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	total := 0
	for _, i := range b.interceptors {
		total += int(i.closeCount.Load())
	}
	return total
}

func (b *evalLifecycleFilterBuilder) eventLog() []evalLifecycleEvent {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]evalLifecycleEvent(nil), b.events...)
}

func (b *evalLifecycleFilterBuilder) interceptorCloseCounts() []int32 {
	b.mu.Lock()
	defer b.mu.Unlock()
	counts := make([]int32, 0, len(b.interceptors))
	for _, i := range b.interceptors {
		counts = append(counts, i.closeCount.Load())
	}
	return counts
}

var _ httpfilter.ServerFilterBuilder = &evalLifecycleFilterBuilder{}

// evalLifecycleInterceptor is an HTTP filter interceptor that records its own
// closure with the builder that created it.
type evalLifecycleInterceptor struct {
	builder    *evalLifecycleFilterBuilder
	chainID    string
	closeCount atomic.Int32
}

func (*evalLifecycleInterceptor) AllowRPC(context.Context) error { return nil }

func (i *evalLifecycleInterceptor) Close() {
	i.closeCount.Add(1)
	i.builder.mu.Lock()
	defer i.builder.mu.Unlock()
	forChain := 0
	if i.builder.chainCreated != nil {
		forChain = i.builder.chainCreated[i.chainID]
	}
	i.builder.events = append(i.builder.events, evalLifecycleEvent{
		kind:            "close",
		chainID:         i.chainID,
		createdSoFar:    i.builder.created,
		createdForChain: forChain,
	})
}

var _ resolver.ServerInterceptor = &evalLifecycleInterceptor{}

// TestEval_ServerSideXDS_InterceptorSwapOrder verifies that an in-place RDS
// update constructs the replacement route configuration, including its
// interceptor instances, before the configuration being replaced is stopped. It
// also verifies that every interceptor instance is closed exactly once across
// the replacement and the final server shutdown.
func (s) TestEval_ServerSideXDS_InterceptorSwapOrder(t *testing.T) {
	fb := &evalLifecycleFilterBuilder{typeURL: t.Name()}
	httpfilter.Register(fb)
	defer httpfilter.UnregisterForTesting(fb.typeURL)

	managementServer, nodeID, bootstrapContents, xdsResolver := setup.ManagementServerAndResolver(t)

	// Wait for the server to enter SERVING mode before making RPCs to avoid
	// flakes due to the server closing connections.
	servingCh := make(chan struct{})
	opt := xds.ServingModeCallback(func(_ net.Addr, args xds.ServingModeChangeArgs) {
		if args.Mode == connectivity.ServingModeServing {
			close(servingCh)
		}
	})
	lis, stopServer := setupGRPCServer(t, bootstrapContents, opt)
	defer stopServer()

	host, port, err := hostPortFromListener(lis)
	if err != nil {
		t.Fatalf("failed to retrieve host and port of server: %v", err)
	}
	const serviceName = "my-service"
	resources := e2e.DefaultClientResources(e2e.ResourceParams{
		DialTarget: serviceName,
		NodeID:     nodeID,
		Host:       host,
		Port:       port,
		SecLevel:   e2e.SecurityLevelNone,
	})

	const routeConfigName = "routeName"
	rds1 := &v3routepb.RouteConfiguration{
		Name: routeConfigName,
		VirtualHosts: []*v3routepb.VirtualHost{{
			Domains: []string{"*"},
			Routes: []*v3routepb.Route{{
				Match: &v3routepb.RouteMatch{
					PathSpecifier: &v3routepb.RouteMatch_Prefix{Prefix: "/grpc.testing.TestService/EmptyCall"},
				},
				Action: &v3routepb.Route_NonForwardingAction{},
			}},
		}},
	}
	mkNetworkFilters := func(chainTag string) []*v3listenerpb.Filter {
		return []*v3listenerpb.Filter{{
			Name: "hcm",
			ConfigType: &v3listenerpb.Filter_TypedConfig{
				TypedConfig: testutils.MarshalAny(t, &v3httppb.HttpConnectionManager{
					HttpFilters: []*v3httppb.HttpFilter{
						evalNewHTTPFilter(t, "tracker", fb.typeURL, chainTag),
						e2e.HTTPFilter("router", &v3routerpb.Router{}),
					},
					RouteSpecifier: &v3httppb.HttpConnectionManager_Rds{
						Rds: &v3httppb.Rds{
							ConfigSource: &v3corepb.ConfigSource{
								ConfigSourceSpecifier: &v3corepb.ConfigSource_Ads{Ads: &v3corepb.AggregatedConfigSource{}},
							},
							RouteConfigName: routeConfigName,
						},
					},
				}),
			},
		}}
	}
	inboundLis := &v3listenerpb.Listener{
		Name: fmt.Sprintf(e2e.ServerListenerResourceNameTemplate, net.JoinHostPort(host, strconv.Itoa(int(port)))),
		Address: &v3corepb.Address{
			Address: &v3corepb.Address_SocketAddress{
				SocketAddress: &v3corepb.SocketAddress{
					Address: host,
					PortSpecifier: &v3corepb.SocketAddress_PortValue{
						PortValue: port,
					},
				},
			},
		},
		FilterChains: []*v3listenerpb.FilterChain{
			{
				Name: "v4-wildcard",
				FilterChainMatch: &v3listenerpb.FilterChainMatch{
					PrefixRanges: []*v3corepb.CidrRange{{
						AddressPrefix: "0.0.0.0",
						PrefixLen:     &wrapperspb.UInt32Value{Value: uint32(0)},
					}},
					SourceType: v3listenerpb.FilterChainMatch_SAME_IP_OR_LOOPBACK,
					SourcePrefixRanges: []*v3corepb.CidrRange{{
						AddressPrefix: "0.0.0.0",
						PrefixLen:     &wrapperspb.UInt32Value{Value: uint32(0)},
					}},
				},
				Filters: mkNetworkFilters("v4"),
			},
			{
				Name: "v6-wildcard",
				FilterChainMatch: &v3listenerpb.FilterChainMatch{
					PrefixRanges: []*v3corepb.CidrRange{{
						AddressPrefix: "::",
						PrefixLen:     &wrapperspb.UInt32Value{Value: uint32(0)},
					}},
					SourceType: v3listenerpb.FilterChainMatch_SAME_IP_OR_LOOPBACK,
					SourcePrefixRanges: []*v3corepb.CidrRange{{
						AddressPrefix: "::",
						PrefixLen:     &wrapperspb.UInt32Value{Value: uint32(0)},
					}},
				},
				Filters: mkNetworkFilters("v6"),
			},
		},
	}
	resources.Listeners = append(resources.Listeners, inboundLis)
	resources.Routes = append(resources.Routes, rds1)

	ctx, cancel := context.WithTimeout(context.Background(), defaultTestTimeout)
	defer cancel()
	if err := managementServer.Update(ctx, resources); err != nil {
		t.Fatal(err)
	}

	cc, err := grpc.NewClient(fmt.Sprintf("xds:///%s", serviceName), grpc.WithTransportCredentials(insecure.NewCredentials()), grpc.WithResolvers(xdsResolver))
	if err != nil {
		t.Fatalf("grpc.NewClient() failed: %v", err)
	}
	defer cc.Close()

	select {
	case <-servingCh:
	case <-ctx.Done():
		t.Fatalf("Timeout waiting for server to enter SERVING mode")
	}

	// The initial configuration holds one route, so one interceptor instance is
	// created per filter chain.
	client := testgrpc.NewTestServiceClient(cc)
	if _, err := client.EmptyCall(ctx, &testpb.Empty{}); err != nil {
		t.Fatalf("EmptyCall() failed: %v", err)
	}
	initialCreated := fb.createdCount()
	if got, want := initialCreated, 2; got != want {
		t.Fatalf("Created %d interceptor instances for the initial configuration, want %d", got, want)
	}

	// Replace the route configuration in place with one holding two routes.
	rds2 := &v3routepb.RouteConfiguration{
		Name: routeConfigName,
		VirtualHosts: []*v3routepb.VirtualHost{{
			Domains: []string{"*"},
			Routes: []*v3routepb.Route{
				{
					Name: "EmptyCall",
					Match: &v3routepb.RouteMatch{
						PathSpecifier: &v3routepb.RouteMatch_Prefix{Prefix: "/grpc.testing.TestService/EmptyCall"},
					},
					Action: &v3routepb.Route_NonForwardingAction{},
				},
				{
					Name: "Wildcard",
					Match: &v3routepb.RouteMatch{
						PathSpecifier: &v3routepb.RouteMatch_Prefix{Prefix: "/"},
					},
					Action: &v3routepb.Route_NonForwardingAction{},
				},
			},
		}},
	}
	resources.Routes = []*v3routepb.RouteConfiguration{resources.Routes[0], rds2}
	if err := managementServer.Update(ctx, resources); err != nil {
		t.Fatal(err)
	}

	// Wait for the replacement to be fully applied: two routes across two
	// filter chains create four interceptor instances on top of the initial
	// two, and the two replaced instances are closed.
	for ; ctx.Err() == nil; <-time.After(defaultTestShortTimeout) {
		if _, err := client.EmptyCall(ctx, &testpb.Empty{}); err != nil {
			t.Fatalf("EmptyCall() failed: %v", err)
		}
		if fb.createdCount() >= 6 && fb.closedCount() >= 2 {
			break
		}
	}
	if ctx.Err() != nil {
		t.Fatalf("Timeout when waiting for updated config to be applied: %v", ctx.Err())
	}

	// A close observed while only the initial configuration's interceptor
	// instances existed would mean the replaced configuration was stopped before
	// its replacement had been constructed.
	// 1. Per-chain readiness: each chain starts with 1 interceptor and creates
	// 2 replacement routes on update. Therefore, for each chain, at least
	// 1 + 2 = 3 interceptor instances must have been created for that specific
	// chain before its active interceptor is retired.
	// 2. Global swap order: the first chain replacement brings total created
	// to at least 4 before retiring its interceptor; the second brings total
	// created to at least 6 before retiring its interceptor.
	closeIdx := 0
	for _, ev := range fb.eventLog() {
		if ev.kind != "close" {
			continue
		}
		closeIdx++
		if ev.createdForChain < 3 {
			t.Errorf("Close #%d (chain %q) observed when only %d interceptor instances had been created for that specific chain (expected >= 3); each filter chain must construct its own replacement routes before retiring its active interceptor", closeIdx, ev.chainID, ev.createdForChain)
		}
		minCreated := initialCreated + closeIdx*2
		if ev.createdSoFar < minCreated {
			t.Errorf("Close #%d observed when only %d interceptor instances had been created globally (expected >= %d); replacement configuration must be fully constructed before retiring active interceptors", closeIdx, ev.createdSoFar, minCreated)
		}
	}

	stopServer()
	for range 100 {
		if fb.closedCount() >= 6 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if got, want := fb.closedCount(), 6; got != want {
		t.Fatalf("Destroyed %d interceptor instances after server shutdown, want %d", got, want)
	}
	for i, count := range fb.interceptorCloseCounts() {
		if count != 1 {
			t.Errorf("Interceptor instance %d was closed %d times, want exactly 1", i, count)
		}
	}
}

// TestEval_ServerSideXDS_InterceptorLeak_MultiGenerationRDSUpdate verifies that
// interceptors are closed across multiple successive in-place RDS updates,
// ensuring that intermediate generations do not leak and are closed exactly once.
func (s) TestEval_ServerSideXDS_InterceptorLeak_MultiGenerationRDSUpdate(t *testing.T) {
	fb := &evalLifecycleFilterBuilder{typeURL: t.Name()}
	httpfilter.Register(fb)
	defer httpfilter.UnregisterForTesting(fb.typeURL)

	managementServer, nodeID, bootstrapContents, xdsResolver := setup.ManagementServerAndResolver(t)

	servingCh := make(chan struct{})
	opt := xds.ServingModeCallback(func(_ net.Addr, args xds.ServingModeChangeArgs) {
		if args.Mode == connectivity.ServingModeServing {
			close(servingCh)
		}
	})
	lis, stopServer := setupGRPCServer(t, bootstrapContents, opt)
	defer stopServer()

	host, port, err := hostPortFromListener(lis)
	if err != nil {
		t.Fatalf("failed to retrieve host and port of server: %v", err)
	}
	const serviceName = "my-service"
	resources := e2e.DefaultClientResources(e2e.ResourceParams{
		DialTarget: serviceName,
		NodeID:     nodeID,
		Host:       host,
		Port:       port,
		SecLevel:   e2e.SecurityLevelNone,
	})

	const routeConfigName = "routeName"
	mkRouteConfig := func(prefix string) *v3routepb.RouteConfiguration {
		return &v3routepb.RouteConfiguration{
			Name: routeConfigName,
			VirtualHosts: []*v3routepb.VirtualHost{{
				Domains: []string{"*"},
				Routes: []*v3routepb.Route{{
					Match: &v3routepb.RouteMatch{
						PathSpecifier: &v3routepb.RouteMatch_Prefix{Prefix: prefix},
					},
					Action: &v3routepb.Route_NonForwardingAction{},
				}},
			}},
		}
	}

	networkFilters := []*v3listenerpb.Filter{{
		Name: "hcm",
		ConfigType: &v3listenerpb.Filter_TypedConfig{
			TypedConfig: testutils.MarshalAny(t, &v3httppb.HttpConnectionManager{
				HttpFilters: []*v3httppb.HttpFilter{
					evalNewHTTPFilter(t, "tracker", fb.typeURL, "config"),
					e2e.HTTPFilter("router", &v3routerpb.Router{}),
				},
				RouteSpecifier: &v3httppb.HttpConnectionManager_Rds{
					Rds: &v3httppb.Rds{
						ConfigSource: &v3corepb.ConfigSource{
							ConfigSourceSpecifier: &v3corepb.ConfigSource_Ads{Ads: &v3corepb.AggregatedConfigSource{}},
						},
						RouteConfigName: routeConfigName,
					},
				},
			}),
		},
	}}

	inboundLis := &v3listenerpb.Listener{
		Name: fmt.Sprintf(e2e.ServerListenerResourceNameTemplate, net.JoinHostPort(host, strconv.Itoa(int(port)))),
		Address: &v3corepb.Address{
			Address: &v3corepb.Address_SocketAddress{
				SocketAddress: &v3corepb.SocketAddress{
					Address: host,
					PortSpecifier: &v3corepb.SocketAddress_PortValue{
						PortValue: port,
					},
				},
			},
		},
		FilterChains: []*v3listenerpb.FilterChain{
			{
				Name: "v4-wildcard",
				FilterChainMatch: &v3listenerpb.FilterChainMatch{
					PrefixRanges: []*v3corepb.CidrRange{{
						AddressPrefix: "0.0.0.0",
						PrefixLen:     &wrapperspb.UInt32Value{Value: uint32(0)},
					}},
					SourceType: v3listenerpb.FilterChainMatch_SAME_IP_OR_LOOPBACK,
					SourcePrefixRanges: []*v3corepb.CidrRange{{
						AddressPrefix: "0.0.0.0",
						PrefixLen:     &wrapperspb.UInt32Value{Value: uint32(0)},
					}},
				},
				Filters: networkFilters,
			},
			{
				Name: "v6-wildcard",
				FilterChainMatch: &v3listenerpb.FilterChainMatch{
					PrefixRanges: []*v3corepb.CidrRange{{
						AddressPrefix: "::",
						PrefixLen:     &wrapperspb.UInt32Value{Value: uint32(0)},
					}},
					SourceType: v3listenerpb.FilterChainMatch_SAME_IP_OR_LOOPBACK,
					SourcePrefixRanges: []*v3corepb.CidrRange{{
						AddressPrefix: "::",
						PrefixLen:     &wrapperspb.UInt32Value{Value: uint32(0)},
					}},
				},
				Filters: networkFilters,
			},
		},
	}
	resources.Listeners = append(resources.Listeners, inboundLis)
	resources.Routes = append(resources.Routes, mkRouteConfig("/grpc.testing.TestService/EmptyCall"))

	ctx, cancel := context.WithTimeout(context.Background(), defaultTestTimeout)
	defer cancel()
	if err := managementServer.Update(ctx, resources); err != nil {
		t.Fatal(err)
	}

	cc, err := grpc.NewClient(fmt.Sprintf("xds:///%s", serviceName), grpc.WithTransportCredentials(insecure.NewCredentials()), grpc.WithResolvers(xdsResolver))
	if err != nil {
		t.Fatalf("grpc.NewClient() failed: %v", err)
	}
	defer cc.Close()

	select {
	case <-servingCh:
	case <-ctx.Done():
		t.Fatalf("Timeout waiting for server to enter SERVING mode")
	}

	client := testgrpc.NewTestServiceClient(cc)
	if _, err := client.EmptyCall(ctx, &testpb.Empty{}); err != nil {
		t.Fatalf("EmptyCall() failed: %v", err)
	}
	if got, want := fb.createdCount(), 2; got != want {
		t.Fatalf("Created %d interceptor instances initially, want %d", got, want)
	}

	// Update 1: rds1 -> rds2
	resources.Routes = []*v3routepb.RouteConfiguration{resources.Routes[0], mkRouteConfig("/grpc.testing.TestService/UnaryCall")}
	if err := managementServer.Update(ctx, resources); err != nil {
		t.Fatal(err)
	}
	// Wait for replacement interceptors to be created.
	for ; ctx.Err() == nil; <-time.After(defaultTestShortTimeout) {
		client.EmptyCall(ctx, &testpb.Empty{})
		if fb.createdCount() >= 4 {
			break
		}
	}
	if ctx.Err() != nil {
		t.Fatalf("Timeout waiting for replacement interceptors to be created; createdCount=%d, want >= 4", fb.createdCount())
	}
	// Wait for retired interceptors of the replaced configuration to be closed.
	for ; ctx.Err() == nil; <-time.After(defaultTestShortTimeout) {
		if fb.closedCount() >= 2 {
			break
		}
	}
	if got, want := fb.closedCount(), 2; got < want {
		t.Fatalf("Replaced interceptors were not closed after in-place RDS update: closedCount=%d, want >= %d (createdCount=%d)", got, want, fb.createdCount())
	}

	// Update 2: rds2 -> rds3
	resources.Routes = []*v3routepb.RouteConfiguration{resources.Routes[0], mkRouteConfig("/grpc.testing.TestService/FullDuplexCall")}
	if err := managementServer.Update(ctx, resources); err != nil {
		t.Fatal(err)
	}
	// Wait for second replacement interceptors to be created.
	for ; ctx.Err() == nil; <-time.After(defaultTestShortTimeout) {
		client.EmptyCall(ctx, &testpb.Empty{})
		if fb.createdCount() >= 6 {
			break
		}
	}
	if ctx.Err() != nil {
		t.Fatalf("Timeout waiting for second replacement interceptors to be created; createdCount=%d, want >= 6", fb.createdCount())
	}
	// Wait for retired interceptors of the second replaced configuration to be closed.
	for ; ctx.Err() == nil; <-time.After(defaultTestShortTimeout) {
		if fb.closedCount() >= 4 {
			break
		}
	}
	if got, want := fb.closedCount(), 4; got < want {
		t.Fatalf("Replaced interceptors were not closed after second in-place RDS update: closedCount=%d, want >= %d (createdCount=%d)", got, want, fb.createdCount())
	}

	// Shutdown server and verify all 6 interceptors were closed exactly once.
	stopServer()
	for range 100 {
		if fb.closedCount() >= 6 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if got, want := fb.closedCount(), 6; got != want {
		t.Fatalf("Destroyed %d interceptor instances after shutdown, want %d", got, want)
	}
	for i, count := range fb.interceptorCloseCounts() {
		if count != 1 {
			t.Errorf("Interceptor %d closed %d times, want 1", i, count)
		}
	}
}

type evalPartialFailFilterBuilder struct {
	httpfilter.Builder
	typeURL               string
	interceptorsCreated   atomic.Int32
	interceptorsDestroyed atomic.Int32
}

func (b *evalPartialFailFilterBuilder) IsTerminal() bool   { return false }
func (b *evalPartialFailFilterBuilder) TypeURLs() []string { return []string{b.typeURL} }
func (*evalPartialFailFilterBuilder) ParseFilterConfig(cfg proto.Message) (httpfilter.FilterConfig, error) {
	return evalFilterConfigFromProto(cfg)
}
func (*evalPartialFailFilterBuilder) ParseFilterConfigOverride(cfg proto.Message) (httpfilter.FilterConfig, error) {
	fc, ok := cfg.(*v3routepb.FilterConfig)
	if ok && fc.Config != nil {
		return evalFilterConfigFromProto(fc.Config)
	}
	return evalFilterConfigFromProto(cfg)
}
func (b *evalPartialFailFilterBuilder) BuildServerFilter() httpfilter.ServerFilter { return b }
func (b *evalPartialFailFilterBuilder) Close()                                     {}

func (b *evalPartialFailFilterBuilder) BuildServerInterceptor(config, override httpfilter.FilterConfig) (resolver.ServerInterceptor, error) {
	var effectiveCfg evalTestFilterCfg
	if override != nil {
		effectiveCfg = override.(evalTestFilterCfg)
	} else if config != nil {
		effectiveCfg = config.(evalTestFilterCfg)
	}
	if effectiveCfg.path == "fail-interceptor" {
		return nil, fmt.Errorf("injected route interceptor construction failure")
	}
	b.interceptorsCreated.Add(1)
	return &evalPartialFailInterceptor{builder: b}, nil
}

type evalPartialFailInterceptor struct {
	builder *evalPartialFailFilterBuilder
}

func (*evalPartialFailInterceptor) AllowRPC(context.Context) error { return nil }
func (i *evalPartialFailInterceptor) Close() {
	i.builder.interceptorsDestroyed.Add(1)
}

func makeFilterOverride(t *testing.T, typeURL, path string) *anypb.Any {
	cfg := &v3routepb.FilterConfig{
		Config: testutils.MarshalAny(t, &v3xdsxdstypepb.TypedStruct{
			TypeUrl: typeURL,
			Value: &structpb.Struct{
				Fields: map[string]*structpb.Value{
					"path": {Kind: &structpb.Value_StringValue{StringValue: path}},
				},
			},
		}),
	}
	return testutils.MarshalAny(t, cfg)
}

// TestEval_ServerSideXDS_PartialRouteFailure_ClosesInterceptors verifies that
// when constructing a route configuration fails partway through converting its
// routes, any interceptor instances created for earlier routes of that virtual
// host are closed and not leaked.
func (s) TestEval_ServerSideXDS_PartialRouteFailure_ClosesInterceptors(t *testing.T) {
	fb := &evalPartialFailFilterBuilder{typeURL: t.Name()}
	httpfilter.Register(fb)
	defer httpfilter.UnregisterForTesting(fb.typeURL)

	managementServer, nodeID, bootstrapContents, xdsResolver := setup.ManagementServerAndResolver(t)

	servingCh := make(chan struct{})
	opt := xds.ServingModeCallback(func(_ net.Addr, args xds.ServingModeChangeArgs) {
		if args.Mode == connectivity.ServingModeServing {
			close(servingCh)
		}
	})
	lis, stopServer := setupGRPCServer(t, bootstrapContents, opt)
	defer stopServer()

	host, port, err := hostPortFromListener(lis)
	if err != nil {
		t.Fatalf("failed to retrieve host and port of server: %v", err)
	}
	const serviceName = "my-service"
	resources := e2e.DefaultClientResources(e2e.ResourceParams{
		DialTarget: serviceName,
		NodeID:     nodeID,
		Host:       host,
		Port:       port,
		SecLevel:   e2e.SecurityLevelNone,
	})

	const routeConfigName = "routeName"
	rds1 := &v3routepb.RouteConfiguration{
		Name: routeConfigName,
		VirtualHosts: []*v3routepb.VirtualHost{{
			Domains: []string{"*"},
			Routes: []*v3routepb.Route{{
				Match: &v3routepb.RouteMatch{
					PathSpecifier: &v3routepb.RouteMatch_Prefix{Prefix: "/grpc.testing.TestService/EmptyCall"},
				},
				Action: &v3routepb.Route_NonForwardingAction{},
			}},
		}},
	}
	networkFilters := []*v3listenerpb.Filter{{
		Name: "hcm",
		ConfigType: &v3listenerpb.Filter_TypedConfig{
			TypedConfig: testutils.MarshalAny(t, &v3httppb.HttpConnectionManager{
				HttpFilters: []*v3httppb.HttpFilter{
					evalNewHTTPFilter(t, "tracker", fb.typeURL, "normal"),
					e2e.HTTPFilter("router", &v3routerpb.Router{}),
				},
				RouteSpecifier: &v3httppb.HttpConnectionManager_Rds{
					Rds: &v3httppb.Rds{
						ConfigSource: &v3corepb.ConfigSource{
							ConfigSourceSpecifier: &v3corepb.ConfigSource_Ads{Ads: &v3corepb.AggregatedConfigSource{}},
						},
						RouteConfigName: routeConfigName,
					},
				},
			}),
		},
	}}
	inboundLis := &v3listenerpb.Listener{
		Name: fmt.Sprintf(e2e.ServerListenerResourceNameTemplate, net.JoinHostPort(host, strconv.Itoa(int(port)))),
		Address: &v3corepb.Address{
			Address: &v3corepb.Address_SocketAddress{
				SocketAddress: &v3corepb.SocketAddress{
					Address: host,
					PortSpecifier: &v3corepb.SocketAddress_PortValue{
						PortValue: port,
					},
				},
			},
		},
		FilterChains: []*v3listenerpb.FilterChain{
			{
				Name: "v4-wildcard",
				FilterChainMatch: &v3listenerpb.FilterChainMatch{
					PrefixRanges: []*v3corepb.CidrRange{{
						AddressPrefix: "0.0.0.0",
						PrefixLen:     &wrapperspb.UInt32Value{Value: uint32(0)},
					}},
					SourceType: v3listenerpb.FilterChainMatch_SAME_IP_OR_LOOPBACK,
					SourcePrefixRanges: []*v3corepb.CidrRange{{
						AddressPrefix: "0.0.0.0",
						PrefixLen:     &wrapperspb.UInt32Value{Value: uint32(0)},
					}},
				},
				Filters: networkFilters,
			},
		},
	}
	resources.Listeners = append(resources.Listeners, inboundLis)
	resources.Routes = append(resources.Routes, rds1)

	ctx, cancel := context.WithTimeout(context.Background(), defaultTestTimeout)
	defer cancel()
	if err := managementServer.Update(ctx, resources); err != nil {
		t.Fatal(err)
	}

	cc, err := grpc.NewClient(fmt.Sprintf("xds:///%s", serviceName), grpc.WithTransportCredentials(insecure.NewCredentials()), grpc.WithResolvers(xdsResolver))
	if err != nil {
		t.Fatalf("grpc.NewClient() failed: %v", err)
	}
	defer cc.Close()

	select {
	case <-servingCh:
	case <-ctx.Done():
		t.Fatalf("Timeout waiting for server to enter SERVING mode")
	}

	client := testgrpc.NewTestServiceClient(cc)
	if _, err := client.EmptyCall(ctx, &testpb.Empty{}); err != nil {
		t.Fatalf("EmptyCall() failed: %v", err)
	}
	initialCreated := fb.interceptorsCreated.Load()
	if initialCreated == 0 {
		t.Fatalf("Expected initial interceptors to be created, got 0")
	}

	// Now deliver rds2: Route 1 succeeds, Route 2 fails with override.
	rds2 := &v3routepb.RouteConfiguration{
		Name: routeConfigName,
		VirtualHosts: []*v3routepb.VirtualHost{{
			Domains: []string{"*"},
			Routes: []*v3routepb.Route{
				{
					Name: "Route1-Normal",
					Match: &v3routepb.RouteMatch{
						PathSpecifier: &v3routepb.RouteMatch_Prefix{Prefix: "/grpc.testing.TestService/Route1"},
					},
					Action: &v3routepb.Route_NonForwardingAction{},
				},
				{
					Name: "Route2-Fail",
					Match: &v3routepb.RouteMatch{
						PathSpecifier: &v3routepb.RouteMatch_Prefix{Prefix: "/grpc.testing.TestService/Route2"},
					},
					Action: &v3routepb.Route_NonForwardingAction{},
					TypedPerFilterConfig: map[string]*anypb.Any{
						"tracker": makeFilterOverride(t, fb.typeURL, "fail-interceptor"),
					},
				},
			},
		}},
	}
	resources.Routes = []*v3routepb.RouteConfiguration{resources.Routes[0], rds2}
	if err := managementServer.Update(ctx, resources); err != nil {
		t.Fatal(err)
	}

	// When Route 2 fails, Route 1's interceptor must be closed immediately.
	pollCtx, pollCancel := context.WithTimeout(ctx, 2*time.Second)
	defer pollCancel()
	for ; pollCtx.Err() == nil; <-time.After(defaultTestShortTimeout) {
		created := fb.interceptorsCreated.Load()
		destroyed := fb.interceptorsDestroyed.Load()
		if created > initialCreated && destroyed >= 2 {
			break
		}
	}

	created := fb.interceptorsCreated.Load()
	destroyed := fb.interceptorsDestroyed.Load()
	if created <= initialCreated {
		t.Fatalf("Route 1 interceptor was not created during rds2 conversion: created=%d, initial=%d", created, initialCreated)
	}
	if got, want := int(destroyed), 2; got != want {
		t.Fatalf("Partial route conversion failure leaked interceptor: destroyed=%d, want=%d (created=%d)", got, want, created)
	}
}

// TestEval_ServerSideXDS_PartialVirtualHostFailure_ClosesInterceptors verifies that
// when constructing a route configuration fails partway through converting its
// virtual hosts, any interceptor instances created for earlier virtual hosts in
// that update attempt are closed and not leaked.
func (s) TestEval_ServerSideXDS_PartialVirtualHostFailure_ClosesInterceptors(t *testing.T) {
	fb := &evalPartialFailFilterBuilder{typeURL: t.Name()}
	httpfilter.Register(fb)
	defer httpfilter.UnregisterForTesting(fb.typeURL)

	managementServer, nodeID, bootstrapContents, xdsResolver := setup.ManagementServerAndResolver(t)

	servingCh := make(chan struct{})
	opt := xds.ServingModeCallback(func(_ net.Addr, args xds.ServingModeChangeArgs) {
		if args.Mode == connectivity.ServingModeServing {
			close(servingCh)
		}
	})
	lis, stopServer := setupGRPCServer(t, bootstrapContents, opt)
	defer stopServer()

	host, port, err := hostPortFromListener(lis)
	if err != nil {
		t.Fatalf("failed to retrieve host and port of server: %v", err)
	}
	const serviceName = "my-service"
	resources := e2e.DefaultClientResources(e2e.ResourceParams{
		DialTarget: serviceName,
		NodeID:     nodeID,
		Host:       host,
		Port:       port,
		SecLevel:   e2e.SecurityLevelNone,
	})

	const routeConfigName = "routeName"
	rds1 := &v3routepb.RouteConfiguration{
		Name: routeConfigName,
		VirtualHosts: []*v3routepb.VirtualHost{{
			Domains: []string{"*"},
			Routes: []*v3routepb.Route{{
				Match: &v3routepb.RouteMatch{
					PathSpecifier: &v3routepb.RouteMatch_Prefix{Prefix: "/grpc.testing.TestService/EmptyCall"},
				},
				Action: &v3routepb.Route_NonForwardingAction{},
			}},
		}},
	}
	networkFilters := []*v3listenerpb.Filter{{
		Name: "hcm",
		ConfigType: &v3listenerpb.Filter_TypedConfig{
			TypedConfig: testutils.MarshalAny(t, &v3httppb.HttpConnectionManager{
				HttpFilters: []*v3httppb.HttpFilter{
					evalNewHTTPFilter(t, "tracker", fb.typeURL, "normal"),
					e2e.HTTPFilter("router", &v3routerpb.Router{}),
				},
				RouteSpecifier: &v3httppb.HttpConnectionManager_Rds{
					Rds: &v3httppb.Rds{
						ConfigSource: &v3corepb.ConfigSource{
							ConfigSourceSpecifier: &v3corepb.ConfigSource_Ads{Ads: &v3corepb.AggregatedConfigSource{}},
						},
						RouteConfigName: routeConfigName,
					},
				},
			}),
		},
	}}
	inboundLis := &v3listenerpb.Listener{
		Name: fmt.Sprintf(e2e.ServerListenerResourceNameTemplate, net.JoinHostPort(host, strconv.Itoa(int(port)))),
		Address: &v3corepb.Address{
			Address: &v3corepb.Address_SocketAddress{
				SocketAddress: &v3corepb.SocketAddress{
					Address: host,
					PortSpecifier: &v3corepb.SocketAddress_PortValue{
						PortValue: port,
					},
				},
			},
		},
		FilterChains: []*v3listenerpb.FilterChain{
			{
				Name: "v4-wildcard",
				FilterChainMatch: &v3listenerpb.FilterChainMatch{
					PrefixRanges: []*v3corepb.CidrRange{{
						AddressPrefix: "0.0.0.0",
						PrefixLen:     &wrapperspb.UInt32Value{Value: uint32(0)},
					}},
					SourceType: v3listenerpb.FilterChainMatch_SAME_IP_OR_LOOPBACK,
					SourcePrefixRanges: []*v3corepb.CidrRange{{
						AddressPrefix: "0.0.0.0",
						PrefixLen:     &wrapperspb.UInt32Value{Value: uint32(0)},
					}},
				},
				Filters: networkFilters,
			},
		},
	}
	resources.Listeners = append(resources.Listeners, inboundLis)
	resources.Routes = append(resources.Routes, rds1)

	ctx, cancel := context.WithTimeout(context.Background(), defaultTestTimeout)
	defer cancel()
	if err := managementServer.Update(ctx, resources); err != nil {
		t.Fatal(err)
	}

	cc, err := grpc.NewClient(fmt.Sprintf("xds:///%s", serviceName), grpc.WithTransportCredentials(insecure.NewCredentials()), grpc.WithResolvers(xdsResolver))
	if err != nil {
		t.Fatalf("grpc.NewClient() failed: %v", err)
	}
	defer cc.Close()

	select {
	case <-servingCh:
	case <-ctx.Done():
		t.Fatalf("Timeout waiting for server to enter SERVING mode")
	}

	client := testgrpc.NewTestServiceClient(cc)
	if _, err := client.EmptyCall(ctx, &testpb.Empty{}); err != nil {
		t.Fatalf("EmptyCall() failed: %v", err)
	}
	initialCreated := fb.interceptorsCreated.Load()
	if initialCreated == 0 {
		t.Fatalf("Expected initial interceptors to be created, got 0")
	}

	// Now deliver rds2 with 2 virtual hosts:
	// VirtualHost 1 succeeds.
	// VirtualHost 2 fails with override.
	rds2 := &v3routepb.RouteConfiguration{
		Name: routeConfigName,
		VirtualHosts: []*v3routepb.VirtualHost{
			{
				Name:    "VH1-Normal",
				Domains: []string{"domain1.example.com"},
				Routes: []*v3routepb.Route{{
					Name: "Route-Normal",
					Match: &v3routepb.RouteMatch{
						PathSpecifier: &v3routepb.RouteMatch_Prefix{Prefix: "/grpc.testing.TestService/Route1"},
					},
					Action: &v3routepb.Route_NonForwardingAction{},
				}},
			},
			{
				Name:    "VH2-Fail",
				Domains: []string{"domain2.example.com"},
				Routes: []*v3routepb.Route{{
					Name: "Route-Fail",
					Match: &v3routepb.RouteMatch{
						PathSpecifier: &v3routepb.RouteMatch_Prefix{Prefix: "/grpc.testing.TestService/Route2"},
					},
					Action: &v3routepb.Route_NonForwardingAction{},
					TypedPerFilterConfig: map[string]*anypb.Any{
						"tracker": makeFilterOverride(t, fb.typeURL, "fail-interceptor"),
					},
				}},
			},
		},
	}
	resources.Routes = []*v3routepb.RouteConfiguration{resources.Routes[0], rds2}
	if err := managementServer.Update(ctx, resources); err != nil {
		t.Fatal(err)
	}

	// When VirtualHost 2 fails, VirtualHost 1's interceptor must be closed immediately.
	pollCtx, pollCancel := context.WithTimeout(ctx, 2*time.Second)
	defer pollCancel()
	for ; pollCtx.Err() == nil; <-time.After(defaultTestShortTimeout) {
		created := fb.interceptorsCreated.Load()
		destroyed := fb.interceptorsDestroyed.Load()
		if created > initialCreated && destroyed >= 2 {
			break
		}
	}

	created := fb.interceptorsCreated.Load()
	destroyed := fb.interceptorsDestroyed.Load()
	if created <= initialCreated {
		t.Fatalf("VirtualHost 1 interceptor was not created during rds2 conversion: created=%d, initial=%d", created, initialCreated)
	}
	if got, want := int(destroyed), 2; got != want {
		t.Fatalf("Partial virtual host conversion failure leaked interceptor: destroyed=%d, want=%d (created=%d)", got, want, created)
	}
}

type evalOrderEvent struct {
	kind string // "icpt-close" or "filter-close"
	id   string
}

type evalOrderLog struct {
	mu     sync.Mutex
	events []evalOrderEvent
}

func (l *evalOrderLog) record(kind, id string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.events = append(l.events, evalOrderEvent{kind: kind, id: id})
}

func (l *evalOrderLog) snapshot() []evalOrderEvent {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]evalOrderEvent(nil), l.events...)
}

type evalOrderBuilder struct {
	typeURL string
	log     *evalOrderLog
	nf      atomic.Int32
}

func (b *evalOrderBuilder) IsTerminal() bool   { return false }
func (b *evalOrderBuilder) TypeURLs() []string { return []string{b.typeURL} }
func (*evalOrderBuilder) ParseFilterConfig(cfg proto.Message) (httpfilter.FilterConfig, error) {
	return evalFilterConfigFromProto(cfg)
}
func (*evalOrderBuilder) ParseFilterConfigOverride(cfg proto.Message) (httpfilter.FilterConfig, error) {
	return evalFilterConfigFromProto(cfg)
}
func (b *evalOrderBuilder) BuildServerFilter() httpfilter.ServerFilter {
	return &evalOrderFilter{b: b, id: fmt.Sprintf("F%d", b.nf.Add(1))}
}

type evalOrderFilter struct {
	b      *evalOrderBuilder
	id     string
	ni     atomic.Int32
	closed atomic.Bool
}

func (f *evalOrderFilter) BuildServerInterceptor(_, _ httpfilter.FilterConfig) (resolver.ServerInterceptor, error) {
	return &evalOrderInterceptor{f: f, id: fmt.Sprintf("%s/I%d", f.id, f.ni.Add(1))}, nil
}

func (f *evalOrderFilter) Close() {
	f.closed.Store(true)
	f.b.log.record("filter-close", f.id)
}

type evalOrderInterceptor struct {
	f      *evalOrderFilter
	id     string
	closed atomic.Bool
}

func (*evalOrderInterceptor) AllowRPC(context.Context) error { return nil }
func (i *evalOrderInterceptor) Close() {
	i.closed.Store(true)
	i.f.b.log.record("icpt-close", i.id)
}

// TestEval_ServerSideXDS_InterceptorBeforeFilterCloseOrder verifies that when an
// in-place RDS replacement disables a server filter on all routes, all retired
// interceptors are closed before the server filter they depend on is closed.
func (s) TestEval_ServerSideXDS_InterceptorBeforeFilterCloseOrder(t *testing.T) {
	testutils.SetEnvConfig(t, &envconfig.XDSClientExtProcEnabled, true)
	orderLog := &evalOrderLog{}
	fb := &evalOrderBuilder{typeURL: t.Name(), log: orderLog}
	httpfilter.Register(fb)
	defer httpfilter.UnregisterForTesting(fb.typeURL)

	managementServer, nodeID, bootstrapContents, xdsResolver := setup.ManagementServerAndResolver(t)

	servingCh := make(chan struct{})
	var once sync.Once
	opt := xds.ServingModeCallback(func(_ net.Addr, args xds.ServingModeChangeArgs) {
		if args.Mode == connectivity.ServingModeServing {
			once.Do(func() { close(servingCh) })
		}
	})
	lis, stopServer := setupGRPCServer(t, bootstrapContents, opt)

	host, port, err := hostPortFromListener(lis)
	if err != nil {
		stopServer()
		t.Fatalf("failed to retrieve host/port: %v", err)
	}
	const serviceName = "my-service"
	resources := e2e.DefaultClientResources(e2e.ResourceParams{
		DialTarget: serviceName,
		NodeID:     nodeID,
		Host:       host,
		Port:       port,
		SecLevel:   e2e.SecurityLevelNone,
	})

	const routeConfigName = "routeName"
	const filterName = "orderTracker"

	resources.Listeners = append(resources.Listeners, &v3listenerpb.Listener{
		Name: fmt.Sprintf(e2e.ServerListenerResourceNameTemplate, net.JoinHostPort(host, fmt.Sprint(port))),
		Address: &v3corepb.Address{Address: &v3corepb.Address_SocketAddress{SocketAddress: &v3corepb.SocketAddress{
			Address: host, PortSpecifier: &v3corepb.SocketAddress_PortValue{PortValue: port}}}},
		FilterChains: []*v3listenerpb.FilterChain{{
			Name: "only-chain",
			Filters: []*v3listenerpb.Filter{{
				Name: "hcm",
				ConfigType: &v3listenerpb.Filter_TypedConfig{
					TypedConfig: testutils.MarshalAny(t, &v3httppb.HttpConnectionManager{
						HttpFilters: []*v3httppb.HttpFilter{
							evalNewHTTPFilter(t, filterName, fb.typeURL, "initial"),
							e2e.HTTPFilter("router", &v3routerpb.Router{}),
						},
						RouteSpecifier: &v3httppb.HttpConnectionManager_Rds{Rds: &v3httppb.Rds{
							ConfigSource:    &v3corepb.ConfigSource{ConfigSourceSpecifier: &v3corepb.ConfigSource_Ads{Ads: &v3corepb.AggregatedConfigSource{}}},
							RouteConfigName: routeConfigName,
						}},
					}),
				},
			}},
		}},
	})

	// Initial route config: filter enabled on two routes.
	resources.Routes = []*v3routepb.RouteConfiguration{resources.Routes[0], &v3routepb.RouteConfiguration{
		Name: routeConfigName,
		VirtualHosts: []*v3routepb.VirtualHost{{
			Domains: []string{"*"},
			Routes: []*v3routepb.Route{
				{
					Match:  &v3routepb.RouteMatch{PathSpecifier: &v3routepb.RouteMatch_Prefix{Prefix: "/route1"}},
					Action: &v3routepb.Route_NonForwardingAction{},
				},
				{
					Match:  &v3routepb.RouteMatch{PathSpecifier: &v3routepb.RouteMatch_Prefix{Prefix: "/"}},
					Action: &v3routepb.Route_NonForwardingAction{},
				},
			},
		}},
	}}

	ctx, cancel := context.WithTimeout(context.Background(), defaultTestTimeout)
	defer cancel()

	if err := managementServer.Update(ctx, resources); err != nil {
		stopServer()
		t.Fatal(err)
	}

	cc, err := grpc.NewClient(fmt.Sprintf("xds:///%s", serviceName), grpc.WithTransportCredentials(insecure.NewCredentials()), grpc.WithResolvers(xdsResolver))
	if err != nil {
		stopServer()
		t.Fatal(err)
	}
	defer cc.Close()

	select {
	case <-servingCh:
	case <-ctx.Done():
		stopServer()
		t.Fatal("timeout waiting for SERVING mode")
	}

	client := testgrpc.NewTestServiceClient(cc)
	if _, err := client.EmptyCall(ctx, &testpb.Empty{}, grpc.WaitForReady(true)); err != nil {
		stopServer()
		t.Fatalf("initial EmptyCall failed: %v", err)
	}

	// Step 2: Push replacement RDS update that disables the filter on all routes via TypedPerFilterConfig.
	// This retires the initial configuration's interceptors.
	disabledConfig := testutils.MarshalAny(t, &v3routepb.FilterConfig{Disabled: true})
	rds2 := &v3routepb.RouteConfiguration{
		Name: routeConfigName,
		VirtualHosts: []*v3routepb.VirtualHost{{
			Domains: []string{"*"},
			Routes: []*v3routepb.Route{{
				Match:  &v3routepb.RouteMatch{PathSpecifier: &v3routepb.RouteMatch_Prefix{Prefix: "/"}},
				Action: &v3routepb.Route_NonForwardingAction{},
			}},
			TypedPerFilterConfig: map[string]*anypb.Any{filterName: disabledConfig},
		}},
	}
	resources.Routes = []*v3routepb.RouteConfiguration{resources.Routes[0], rds2}
	if err := managementServer.Update(ctx, resources); err != nil {
		stopServer()
		t.Fatal(err)
	}

	// Poll until replacement route is active and retired interceptor closure is observed.
	pollCtx, pollCancel := context.WithTimeout(ctx, 5*time.Second)
	defer pollCancel()

	for {
		client.EmptyCall(pollCtx, &testpb.Empty{}, grpc.WaitForReady(true))
		events := orderLog.snapshot()
		var icptCloses int
		for _, e := range events {
			if e.kind == "icpt-close" {
				icptCloses++
			}
		}
		if icptCloses >= 2 {
			break
		}
		select {
		case <-pollCtx.Done():
			stopServer()
			t.Fatal("timed out waiting for retired interceptor closures upon RDS replacement (interceptor leak)")
		case <-time.After(20 * time.Millisecond):
		}
	}

	// Step 3: Now stop the server to trigger final listener and filter teardown (if filter was retained by LDS).
	stopServer()

	// Wait with bounded polling for deferred shutdown events to settle.
	for range 100 {
		events := orderLog.snapshot()
		hasFilterClose := false
		for _, e := range events {
			if e.kind == "filter-close" {
				hasFilterClose = true
				break
			}
		}
		if hasFilterClose {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	// Snapshot all events across replacement and server shutdown.
	events := orderLog.snapshot()
	firstFilterCloseIdx := -1
	lastRetiredIcptCloseIdx := -1
	icptCloseCount := 0

	for idx, e := range events {
		if e.kind == "filter-close" && firstFilterCloseIdx == -1 {
			firstFilterCloseIdx = idx
		}
		if e.kind == "icpt-close" {
			lastRetiredIcptCloseIdx = idx
			icptCloseCount++
		}
	}

	if icptCloseCount < 2 {
		t.Fatalf("expected at least 2 retired interceptors to be closed upon replacement, got %d", icptCloseCount)
	}
	if firstFilterCloseIdx < 0 {
		t.Fatal("filter-close was not observed even after server shutdown")
	}
	if firstFilterCloseIdx < lastRetiredIcptCloseIdx {
		t.Fatalf("dependency order violation: filter closed at event #%d before retired interceptor closed at event #%d (events: %+v)",
			firstFilterCloseIdx, lastRetiredIcptCloseIdx, events)
	}
}

// evalC2C3FilterCfg represents configuration for the C2/C3 concurrency test filter.
type evalC2C3FilterCfg struct {
	httpfilter.FilterConfig
	path string
}

func evalC2C3ParseCfg(cfg proto.Message) (httpfilter.FilterConfig, error) {
	ts, ok := cfg.(*v3xdsxdstypepb.TypedStruct)
	if !ok {
		return nil, fmt.Errorf("unsupported filter config type: %T", cfg)
	}
	ret := evalC2C3FilterCfg{}
	if v := ts.GetValue().GetFields()[evalFilterCfgPathFieldName]; v != nil {
		ret.path = v.GetStringValue()
	}
	return ret, nil
}

type evalC2C3FilterBuilder struct {
	typeURL         string
	allowRPC        func(ctx context.Context, path string) error
	onClose         func(path string)
	onFilterClose   func()
	icptConstructed atomic.Int32
}

func (b *evalC2C3FilterBuilder) IsTerminal() bool   { return false }
func (b *evalC2C3FilterBuilder) TypeURLs() []string { return []string{b.typeURL} }
func (*evalC2C3FilterBuilder) ParseFilterConfig(cfg proto.Message) (httpfilter.FilterConfig, error) {
	return evalC2C3ParseCfg(cfg)
}
func (*evalC2C3FilterBuilder) ParseFilterConfigOverride(cfg proto.Message) (httpfilter.FilterConfig, error) {
	return evalC2C3ParseCfg(cfg)
}
func (b *evalC2C3FilterBuilder) BuildServerFilter() httpfilter.ServerFilter { return b }
func (b *evalC2C3FilterBuilder) Close() {
	if b.onFilterClose != nil {
		b.onFilterClose()
	}
}
func (b *evalC2C3FilterBuilder) BuildServerInterceptor(config, override httpfilter.FilterConfig) (resolver.ServerInterceptor, error) {
	cfg := config
	if override != nil {
		cfg = override
	}
	c, ok := cfg.(evalC2C3FilterCfg)
	if !ok {
		return nil, fmt.Errorf("unexpected config %T", cfg)
	}
	b.icptConstructed.Add(1)
	return &evalC2C3Interceptor{b: b, path: c.path}, nil
}

type evalC2C3Interceptor struct {
	b    *evalC2C3FilterBuilder
	path string
}

func (i *evalC2C3Interceptor) AllowRPC(ctx context.Context) error { return i.b.allowRPC(ctx, i.path) }
func (i *evalC2C3Interceptor) Close()                             { i.b.onClose(i.path) }

func evalC2C3TypedStruct(t *testing.T, typeURL, path string) *anypb.Any {
	return testutils.MarshalAny(t, &v3xdsxdstypepb.TypedStruct{
		TypeUrl: typeURL,
		Value: &structpb.Struct{Fields: map[string]*structpb.Value{
			evalFilterCfgPathFieldName: {Kind: &structpb.Value_StringValue{StringValue: path}},
		}},
	})
}

func evalC2C3RouteConfig(t *testing.T, typeURL, overridePath string) *v3routepb.RouteConfiguration {
	r := &v3routepb.Route{
		Match:  &v3routepb.RouteMatch{PathSpecifier: &v3routepb.RouteMatch_Prefix{Prefix: "/"}},
		Action: &v3routepb.Route_NonForwardingAction{},
	}
	if overridePath != "" {
		r.TypedPerFilterConfig = map[string]*anypb.Any{"concurrency-filter": evalC2C3TypedStruct(t, typeURL, overridePath)}
	}
	return &v3routepb.RouteConfiguration{
		Name:         "concurrencyRouteName",
		VirtualHosts: []*v3routepb.VirtualHost{{Domains: []string{"*"}, Routes: []*v3routepb.Route{r}}},
	}
}

func evalC2C3ServerListener(t *testing.T, host string, port uint32, typeURL string) *v3listenerpb.Listener {
	return &v3listenerpb.Listener{
		Name: fmt.Sprintf(e2e.ServerListenerResourceNameTemplate, net.JoinHostPort(host, strconv.Itoa(int(port)))),
		Address: &v3corepb.Address{Address: &v3corepb.Address_SocketAddress{SocketAddress: &v3corepb.SocketAddress{
			Address:       host,
			PortSpecifier: &v3corepb.SocketAddress_PortValue{PortValue: port},
		}}},
		FilterChains: []*v3listenerpb.FilterChain{{
			Name: "catch-all",
			Filters: []*v3listenerpb.Filter{{
				Name: "hcm",
				ConfigType: &v3listenerpb.Filter_TypedConfig{TypedConfig: testutils.MarshalAny(t, &v3httppb.HttpConnectionManager{
					HttpFilters: []*v3httppb.HttpFilter{
						{Name: "concurrency-filter", ConfigType: &v3httppb.HttpFilter_TypedConfig{TypedConfig: evalC2C3TypedStruct(t, typeURL, "path1")}},
						e2e.HTTPFilter("router", &v3routerpb.Router{}),
					},
					RouteSpecifier: &v3httppb.HttpConnectionManager_Rds{Rds: &v3httppb.Rds{
						ConfigSource:    &v3corepb.ConfigSource{ConfigSourceSpecifier: &v3corepb.ConfigSource_Ads{Ads: &v3corepb.AggregatedConfigSource{}}},
						RouteConfigName: "concurrencyRouteName",
					}},
				})},
			}},
		}},
	}
}

type evalC2C3Env struct {
	client testgrpc.TestServiceClient
	stop   func()
	update func(*v3routepb.RouteConfiguration)
	ctx    context.Context
}

func evalC2C3Setup(t *testing.T, fb *evalC2C3FilterBuilder, serverOpts ...grpc.ServerOption) *evalC2C3Env {
	httpfilter.Register(fb)
	t.Cleanup(func() { httpfilter.UnregisterForTesting(fb.typeURL) })

	managementServer, nodeID, bootstrapContents, xdsResolver := setup.ManagementServerAndResolver(t)
	lis, stopServer := setupGRPCServer(t, bootstrapContents, serverOpts...)

	host, port, err := hostPortFromListener(lis)
	if err != nil {
		t.Fatalf("failed to retrieve host and port of server: %v", err)
	}
	const serviceName = "my-service"
	resources := e2e.DefaultClientResources(e2e.ResourceParams{
		DialTarget: serviceName,
		NodeID:     nodeID,
		Host:       host,
		Port:       port,
		SecLevel:   e2e.SecurityLevelNone,
	})
	clientRoute := resources.Routes[0]
	resources.Listeners = append(resources.Listeners, evalC2C3ServerListener(t, host, port, fb.typeURL))
	resources.Routes = []*v3routepb.RouteConfiguration{clientRoute, evalC2C3RouteConfig(t, fb.typeURL, "")}

	ctx, cancel := context.WithTimeout(context.Background(), defaultTestTimeout)
	t.Cleanup(cancel)
	if err := managementServer.Update(ctx, resources); err != nil {
		t.Fatal(err)
	}

	cc, err := grpc.NewClient(fmt.Sprintf("xds:///%s", serviceName), grpc.WithTransportCredentials(insecure.NewCredentials()), grpc.WithResolvers(xdsResolver))
	if err != nil {
		t.Fatalf("grpc.NewClient() failed: %v", err)
	}
	t.Cleanup(func() { cc.Close() })

	var stopOnce sync.Once
	return &evalC2C3Env{
		client: testgrpc.NewTestServiceClient(cc),
		stop:   func() { stopOnce.Do(stopServer) },
		update: func(rc *v3routepb.RouteConfiguration) {
			resources.Routes = []*v3routepb.RouteConfiguration{clientRoute, rc}
			if err := managementServer.Update(ctx, resources); err != nil {
				t.Fatal(err)
			}
		},
		ctx: ctx,
	}
}

// TestEval_ServerSideXDS_StopWithAllowRPCAwaitingContextCancel verifies claim C2:
// Forced Stop() must not hang waiting for a read lock held across an in-flight RPC,
// and all dependent interceptors must close before the parent server filter closes.
func (s) TestEval_ServerSideXDS_StopWithAllowRPCAwaitingContextCancel(t *testing.T) {
	allowEntered := make(chan struct{}, 1)
	var mu sync.Mutex
	var events []string
	record := func(kind string) {
		mu.Lock()
		events = append(events, kind)
		mu.Unlock()
	}

	fb := &evalC2C3FilterBuilder{
		typeURL: t.Name(),
		allowRPC: func(ctx context.Context, _ string) error {
			allowEntered <- struct{}{}
			<-ctx.Done() // Wait ONLY for the RPC context to be cancelled.
			return ctx.Err()
		},
		onClose: func(path string) {
			record("icpt-close")
		},
		onFilterClose: func() {
			record("filter-close")
		},
	}
	e := evalC2C3Setup(t, fb)

	rpcCtx, cancelRPC := context.WithTimeout(context.Background(), defaultTestTimeout)
	t.Cleanup(func() { cancelRPC(); e.stop() })
	rpcDone := make(chan error, 1)
	go func() {
		_, err := e.client.EmptyCall(rpcCtx, &testpb.Empty{}, grpc.WaitForReady(true))
		rpcDone <- err
	}()
	select {
	case <-allowEntered:
	case <-time.After(defaultTestTimeout):
		t.Fatal("timeout waiting for AllowRPC to be invoked")
	}

	stopDone := make(chan struct{})
	go func() {
		e.stop()
		close(stopDone)
	}()

	select {
	case <-stopDone:
		// Clean return from Stop().
		if err := rpcCtx.Err(); err != nil {
			t.Fatalf("client context cancelled prematurely during Stop(): %v", err)
		}
		select {
		case <-rpcDone:
		case <-time.After(defaultTestShortTimeout):
			t.Fatal("timeout waiting for in-flight RPC to complete after Stop()")
		}

		// Wait with bounded polling for deferred cleanup events to settle.
		constructedIcpts := fb.icptConstructed.Load()
		deadline := time.Now().Add(5 * time.Second)
		for time.Now().Before(deadline) {
			mu.Lock()
			hasFilterClose := false
			icptCloses := 0
			for _, ev := range events {
				if ev == "filter-close" {
					hasFilterClose = true
				}
				if ev == "icpt-close" {
					icptCloses++
				}
			}
			mu.Unlock()
			if hasFilterClose && icptCloses >= int(constructedIcpts) {
				break
			}
			time.Sleep(10 * time.Millisecond)
		}

		mu.Lock()
		snap := append([]string(nil), events...)
		mu.Unlock()

		filterCloseIdx := -1
		lastIcptCloseIdx := -1
		icptCloseCount := 0
		filterCloseCount := 0

		for idx, ev := range snap {
			if ev == "filter-close" {
				if filterCloseIdx == -1 {
					filterCloseIdx = idx
				}
				filterCloseCount++
			}
			if ev == "icpt-close" {
				lastIcptCloseIdx = idx
				icptCloseCount++
			}
		}

		if filterCloseCount == 0 {
			t.Fatal("parent server filter Close() was not observed during shutdown (filter cleanup omitted)")
		}
		if filterCloseCount > 1 {
			t.Fatalf("parent server filter was closed %d times, want exactly 1", filterCloseCount)
		}
		if icptCloseCount != int(constructedIcpts) {
			t.Fatalf("observed %d interceptor Close() events, want exactly %d (one per constructed dependent interceptor)",
				icptCloseCount, constructedIcpts)
		}
		if filterCloseIdx < lastIcptCloseIdx {
			t.Fatalf("dependency order violation during shutdown: parent filter closed at event #%d before interceptor closed at event #%d (events: %+v)",
				filterCloseIdx, lastIcptCloseIdx, snap)
		}
	case <-time.After(5 * time.Second):
		// Break the deadlock from client side so test doesn't hang forever.
		cancelRPC()
		t.Fatal("Stop() did not return within 5s while AllowRPC was waiting for context cancellation (shutdown deadlock defect)")
	}
}

// TestEval_ServerSideXDS_ReplacementRPCWhileRetiredCloseBlocked verifies claim C3:
// New RPCs on the published replacement route must succeed while the retired interceptor's Close() is blocked.
func (s) TestEval_ServerSideXDS_ReplacementRPCWhileRetiredCloseBlocked(t *testing.T) {
	pathCh := make(chan string, 16)
	closeEntered := make(chan struct{}, 4)
	releaseClose := make(chan struct{})
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(releaseClose) }) }
	defer release()

	fb := &evalC2C3FilterBuilder{
		typeURL: t.Name(),
		allowRPC: func(_ context.Context, path string) error {
			select {
			case pathCh <- path:
			default:
			}
			return nil
		},
		onClose: func(path string) {
			if path != "path1" {
				return
			}
			closeEntered <- struct{}{}
			<-releaseClose
		},
	}
	e := evalC2C3Setup(t, fb)
	defer func() { release(); e.stop() }()

	drain := func() (paths []string) {
		for {
			select {
			case p := <-pathCh:
				paths = append(paths, p)
			default:
				return paths
			}
		}
	}

	if _, err := e.client.EmptyCall(e.ctx, &testpb.Empty{}, grpc.WaitForReady(true)); err != nil {
		t.Fatalf("EmptyCall() on original route failed: %v", err)
	}
	drain()

	// Replace the route configuration; the replacement interceptor is "path2".
	e.update(evalC2C3RouteConfig(t, fb.typeURL, "path2"))
	select {
	case <-closeEntered:
	case <-e.ctx.Done():
		t.Fatal("timeout waiting for retired interceptor Close to be called")
	}

	// Issue an RPC with a bounded deadline while Close is held.
	rctx, cancel := context.WithTimeout(e.ctx, 3*time.Second)
	defer cancel()
	if _, err := e.client.EmptyCall(rctx, &testpb.Empty{}); err != nil {
		t.Fatalf("RPC on published replacement route failed while retired interceptor Close was blocked (traffic stall defect): %v", err)
	}

	served := drain()
	if len(served) != 1 || served[0] != "path2" {
		t.Fatalf("RPC issued after replacement was served by %v, want [path2]", served)
	}
}

// TestEval_ServerSideXDS_ReplacementRPCWhileAllowRPCHeld verifies that an
// active in-flight RPC executing AllowRPC on an earlier route configuration does
// not block in-place RDS replacement publication or stall newly routed RPCs.
func (s) TestEval_ServerSideXDS_ReplacementRPCWhileAllowRPCHeld(t *testing.T) {
	pathCh := make(chan string, 16)
	allowEntered := make(chan struct{})
	releaseAllow := make(chan struct{})
	var allowOnce, releaseOnce sync.Once
	doRelease := func() { releaseOnce.Do(func() { close(releaseAllow) }) }

	fb := &evalC2C3FilterBuilder{
		typeURL: t.Name(),
		allowRPC: func(ctx context.Context, path string) error {
			if path == "path1" {
				allowOnce.Do(func() { close(allowEntered) })
				select {
				case <-releaseAllow:
				case <-ctx.Done():
					return ctx.Err()
				}
			} else {
				select {
				case pathCh <- path:
				default:
				}
			}
			return nil
		},
		onClose: func(_ string) {},
	}
	e := evalC2C3Setup(t, fb)
	defer func() {
		doRelease()
		e.stop()
	}()

	drain := func() (paths []string) {
		for {
			select {
			case p := <-pathCh:
				paths = append(paths, p)
			default:
				return paths
			}
		}
	}

	// Start in-flight RPC that holds AllowRPC open on path1.
	heldDone := make(chan error, 1)
	go func() {
		_, err := e.client.EmptyCall(e.ctx, &testpb.Empty{}, grpc.WaitForReady(true))
		heldDone <- err
	}()

	select {
	case <-allowEntered:
	case <-e.ctx.Done():
		t.Fatal("timeout waiting for in-flight AllowRPC to enter")
	}

	// While AllowRPC(path1) is HELD, submit the replacement configuration.
	e.update(evalC2C3RouteConfig(t, fb.typeURL, "path2"))

	// Probe with bounded retries for newly routed RPCs while AllowRPC(path1) is still held.
	// Because managementServer.Update is asynchronous, we probe until the replacement route
	// is published and serves an RPC, or fail if all probes stall behind the held AllowRPC.
	probeDeadline := time.Now().Add(3 * time.Second)
	replacementServed := false
	for time.Now().Before(probeDeadline) {
		pctx, pcancel := context.WithTimeout(e.ctx, 300*time.Millisecond)
		_, err := e.client.EmptyCall(pctx, &testpb.Empty{})
		pcancel()
		served := drain()
		// Require that the newly routed probe completes successfully (err == nil)
		// and was served by the replacement interceptor (path2).
		if err == nil {
			for _, p := range served {
				if p == "path2" {
					replacementServed = true
					break
				}
			}
		}
		if replacementServed {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	if !replacementServed {
		t.Fatalf("STALL: replacement configuration was not usable by newly routed RPCs while old AllowRPC was held")
	}

	// Release held AllowRPC and confirm it completes cleanly within a bounded deadline.
	doRelease()
	select {
	case err := <-heldDone:
		if err != nil {
			t.Errorf("held AllowRPC failed: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timeout waiting for held AllowRPC to complete")
	}
}

// An accepted RPC must reach the application handler even when releasing its
// retired route configuration starts an interceptor Close that cannot finish.
func (s) TestEval_ServerSideXDS_ReplacementRPCWhileRetiredCloseBlocksAcceptedRPC(t *testing.T) {
	allowEntered := make(chan struct{})
	releaseAllow := make(chan struct{})
	path2Served := make(chan struct{}, 1)
	closeEntered := make(chan struct{})
	releaseClose := make(chan struct{})
	closeFinished := make(chan struct{})
	handlerEntered := make(chan struct{})
	var allowOnce, releaseAllowOnce, closeOnce, releaseCloseOnce, closeFinishedOnce, handlerOnce sync.Once
	unblockAllow := func() { releaseAllowOnce.Do(func() { close(releaseAllow) }) }
	unblockClose := func() { releaseCloseOnce.Do(func() { close(releaseClose) }) }
	defer unblockAllow()
	defer unblockClose()

	fb := &evalC2C3FilterBuilder{
		typeURL: t.Name(),
		allowRPC: func(ctx context.Context, path string) error {
			if path == "path1" {
				allowOnce.Do(func() { close(allowEntered) })
				select {
				case <-releaseAllow:
					return nil
				case <-ctx.Done():
					return ctx.Err()
				}
			}
			select {
			case path2Served <- struct{}{}:
			default:
			}
			return nil
		},
		onClose: func(path string) {
			if path != "path1" {
				return
			}
			closeOnce.Do(func() { close(closeEntered) })
			<-releaseClose
			closeFinishedOnce.Do(func() { close(closeFinished) })
		},
	}
	probe := func(ctx context.Context, req any, _ *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		// grpc.UnaryServerInterceptor requires any for request and response.
		if md, ok := metadata.FromIncomingContext(ctx); ok {
			for _, id := range md.Get("rpc-id") {
				if id == "held" {
					handlerOnce.Do(func() { close(handlerEntered) })
					break
				}
			}
		}
		return handler(ctx, req)
	}
	e := evalC2C3Setup(t, fb, grpc.ChainUnaryInterceptor(probe))

	// Keep the held RPC alive independently of the management update context.
	rpcCtx, cancelRPC := context.WithTimeout(context.Background(), defaultTestTimeout)
	heldDone := make(chan error, 1)
	heldFinished := make(chan struct{})
	defer func() {
		unblockAllow()
		unblockClose()
		cancelRPC()
		stopDone := make(chan struct{})
		go func() {
			e.stop()
			close(stopDone)
		}()
		select {
		case <-stopDone:
		case <-time.After(5 * time.Second):
			t.Error("timeout stopping server after releasing held AllowRPC and Close")
		}
		select {
		case <-heldFinished:
		case <-time.After(3 * time.Second):
			t.Error("timeout waiting for held RPC goroutine to exit during cleanup")
		}
		select {
		case <-closeEntered:
			select {
			case <-closeFinished:
			case <-time.After(3 * time.Second):
				t.Error("timeout waiting for retired interceptor Close to finish during cleanup")
			}
		default:
		}
	}()
	go func() {
		defer close(heldFinished)
		ctx := metadata.AppendToOutgoingContext(rpcCtx, "rpc-id", "held")
		_, err := e.client.EmptyCall(ctx, &testpb.Empty{}, grpc.WaitForReady(true))
		heldDone <- err
	}()
	select {
	case <-allowEntered:
	case <-e.ctx.Done():
		t.Fatal("timeout waiting for held RPC to enter AllowRPC(path1)")
	}

	e.update(evalC2C3RouteConfig(t, fb.typeURL, "path2"))

	// An update acknowledgement alone does not prove that path2 is serving.
	probeCtx, cancelProbes := context.WithTimeout(e.ctx, 3*time.Second)
	defer cancelProbes()
	retry := time.NewTicker(20 * time.Millisecond)
	defer retry.Stop()
	replacementServed := false
	for !replacementServed && probeCtx.Err() == nil {
		select {
		case <-path2Served:
		default:
		}
		ctx, cancel := context.WithTimeout(probeCtx, 300*time.Millisecond)
		_, err := e.client.EmptyCall(ctx, &testpb.Empty{})
		cancel()
		if err == nil {
			select {
			case <-path2Served:
				replacementServed = true
			default:
			}
		}
		if !replacementServed {
			select {
			case <-retry.C:
			case <-probeCtx.Done():
			}
		}
	}
	if !replacementServed {
		t.Fatal("replacement route did not serve a successful probe while AllowRPC(path1) was held")
	}
	unblockAllow()
	select {
	case <-closeEntered:
	case <-time.After(3 * time.Second):
		t.Fatal("timeout waiting for retired interceptor Close to start")
	}
	if err := rpcCtx.Err(); err != nil {
		t.Fatalf("held RPC context expired before observing application handler progress: %v", err)
	}

	// Do not release Close until the accepted RPC reaches the handler and returns.
	progress := time.NewTimer(3 * time.Second)
	defer progress.Stop()
	select {
	case <-handlerEntered:
	case <-progress.C:
		t.Fatal("accepted RPC did not enter its application handler while retired interceptor Close was blocked")
	}
	select {
	case err := <-heldDone:
		if err != nil {
			t.Fatalf("accepted RPC failed while retired interceptor Close was blocked: %v", err)
		}
	case <-progress.C:
		t.Fatal("accepted RPC did not complete while retired interceptor Close was blocked")
	}
	unblockClose()
	select {
	case <-closeFinished:
	case <-time.After(3 * time.Second):
		t.Fatal("timeout waiting for retired interceptor Close to finish after release")
	}
}

// Shutdown must keep the parent filter alive until a superseded interceptor
// finishes closing, even when that cleanup overlaps server Stop.
func (s) TestEval_ServerSideXDS_StopWithSupersededInterceptorCleanupBlocked(t *testing.T) {
	testutils.SetEnvConfig(t, &envconfig.XDSClientExtProcEnabled, true)
	closeEntered := make(chan struct{})
	releaseClose := make(chan struct{})
	closeFinished := make(chan struct{})
	filterClosed := make(chan struct{})
	var closeOnce, releaseOnce, closeFinishedOnce, filterCloseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(releaseClose) }) }
	defer release()
	var mu sync.Mutex
	var events []string
	record := func(event string) {
		mu.Lock()
		events = append(events, event)
		mu.Unlock()
	}
	snapshot := func() []string {
		mu.Lock()
		defer mu.Unlock()
		return append([]string(nil), events...)
	}
	fb := &evalC2C3FilterBuilder{
		typeURL:  t.Name(),
		allowRPC: func(context.Context, string) error { return nil },
		onClose: func(path string) {
			if path != "path1" {
				return
			}
			record("icpt-close-begin")
			closeOnce.Do(func() { close(closeEntered) })
			<-releaseClose
			record("icpt-close-end")
			closeFinishedOnce.Do(func() { close(closeFinished) })
		},
		onFilterClose: func() {
			record("filter-close")
			filterCloseOnce.Do(func() { close(filterClosed) })
		},
	}
	e := evalC2C3Setup(t, fb)
	stopDone := make(chan struct{})
	stopStarted := make(chan struct{})
	var stopOnce sync.Once
	startStop := func() {
		stopOnce.Do(func() {
			go func() {
				close(stopStarted)
				e.stop()
				close(stopDone)
			}()
		})
	}
	defer func() {
		release()
		startStop()
		select {
		case <-stopDone:
		case <-time.After(5 * time.Second):
			t.Error("timeout stopping server after releasing superseded interceptor Close")
		}
		select {
		case <-closeEntered:
			select {
			case <-closeFinished:
			case <-time.After(3 * time.Second):
				t.Error("timeout waiting for superseded interceptor Close to finish during cleanup")
			}
		default:
		}
	}()

	if _, err := e.client.EmptyCall(e.ctx, &testpb.Empty{}, grpc.WaitForReady(true)); err != nil {
		t.Fatalf("EmptyCall on original route failed: %v", err)
	}
	e.update(evalC2C3RouteConfig(t, fb.typeURL, "path2"))
	select {
	case <-closeEntered:
	case <-e.ctx.Done():
		t.Fatal("timeout waiting for superseded interceptor Close to start")
	}
	startStop()
	select {
	case <-stopStarted:
	case <-e.ctx.Done():
		t.Fatal("timeout starting concurrent server Stop")
	}

	// Observe the forbidden event for a bounded interval without releasing Close.
	observation := time.NewTimer(500 * time.Millisecond)
	defer observation.Stop()
	select {
	case <-filterClosed:
		t.Fatalf("parent filter closed while superseded interceptor Close was blocked: %v", snapshot())
	case <-observation.C:
	case <-e.ctx.Done():
		t.Fatal("test context expired while observing shutdown with superseded cleanup blocked")
	}
	for _, event := range snapshot() {
		if event == "filter-close" {
			t.Fatalf("parent filter closed before superseded interceptor Close was released: %v", snapshot())
		}
	}

	release()
	select {
	case <-stopDone:
	case <-time.After(5 * time.Second):
		t.Fatal("server Stop did not finish after superseded interceptor Close was released")
	}
	select {
	case <-filterClosed:
	case <-time.After(5 * time.Second):
		t.Fatal("parent filter Close was not observed after server Stop and cleanup release")
	}
	snap := snapshot()
	icptCloseEndIdx, filterCloseIdx := -1, -1
	for idx, event := range snap {
		if event == "icpt-close-end" {
			icptCloseEndIdx = idx
		}
		if event == "filter-close" && filterCloseIdx == -1 {
			filterCloseIdx = idx
		}
	}
	if icptCloseEndIdx == -1 || filterCloseIdx <= icptCloseEndIdx {
		t.Fatalf("parent filter Close must follow superseded interceptor Close completion: %v", snap)
	}
}

func (s) TestEval_ServerSideXDS_ReplacementRPCWhileServerFilterCloseHeld(t *testing.T) {
	testutils.SetEnvConfig(t, &envconfig.XDSClientExtProcEnabled, true)
	filterCloseEntered := make(chan struct{}, 4)
	releaseFilterClose := make(chan struct{})
	var releaseFilterOnce sync.Once
	releaseFilter := func() { releaseFilterOnce.Do(func() { close(releaseFilterClose) }) }
	defer releaseFilter()

	fb := &evalC2C3FilterBuilder{
		typeURL:  t.Name(),
		allowRPC: func(_ context.Context, _ string) error { return nil },
		onClose:  func(_ string) {},
		onFilterClose: func() {
			select {
			case filterCloseEntered <- struct{}{}:
			default:
			}
			<-releaseFilterClose
		},
	}
	e := evalC2C3Setup(t, fb)
	defer func() { releaseFilter(); e.stop() }()

	if _, err := e.client.EmptyCall(e.ctx, &testpb.Empty{}, grpc.WaitForReady(true)); err != nil {
		t.Fatalf("EmptyCall failed: %v", err)
	}

	// Replacement route disables the filter AND routes to /nonexistent.
	disabledConfig := testutils.MarshalAny(t, &v3routepb.FilterConfig{Disabled: true})
	r := &v3routepb.Route{
		Match:                &v3routepb.RouteMatch{PathSpecifier: &v3routepb.RouteMatch_Prefix{Prefix: "/nonexistent"}},
		Action:               &v3routepb.Route_NonForwardingAction{},
		TypedPerFilterConfig: map[string]*anypb.Any{"concurrency-filter": disabledConfig},
	}
	rc := &v3routepb.RouteConfiguration{
		Name:         "concurrencyRouteName",
		VirtualHosts: []*v3routepb.VirtualHost{{Domains: []string{"*"}, Routes: []*v3routepb.Route{r}}},
	}

	e.update(rc)

	select {
	case <-filterCloseEntered:
		// Concrete filter Close() was invoked during replacement.
		// While filter Close is STILL HELD, verify that new RPCs observe the replacement!
		// In the replacement, /grpc.testing.TestService/EmptyCall does not match /nonexistent.
		// If the replacement was published, EmptyCall must fail with the specific routing
		// rejection (Unavailable: did not match a configured Route).
		// A stall defect that causes DeadlineExceeded or a failure that serves the old route will fail!
		rctx, cancel := context.WithTimeout(e.ctx, 1*time.Second)
		defer cancel()
		_, err := e.client.EmptyCall(rctx, &testpb.Empty{})
		if status.Code(err) != codes.Unavailable || !strings.Contains(status.Convert(err).Message(), "did not match a configured Route") {
			t.Fatalf("expected routing rejection (Unavailable: did not match a configured Route) on replacement route while server filter Close held, got: %v", err)
		}
	case <-time.After(500 * time.Millisecond):
		// Filter cleanup was legitimately deferred (e.g. listener cache hold).
		// Poll with bounded retries to verify that the asynchronous update publishes
		// the replacement route and rejects unmatched calls without stalling.
		observedRejection := false
		pollDeadline := time.Now().Add(2 * time.Second)
		var lastErr error
		for time.Now().Before(pollDeadline) {
			pctx, pcancel := context.WithTimeout(e.ctx, 300*time.Millisecond)
			_, lastErr = e.client.EmptyCall(pctx, &testpb.Empty{})
			pcancel()
			if status.Code(lastErr) == codes.Unavailable && strings.Contains(status.Convert(lastErr).Message(), "did not match a configured Route") {
				observedRejection = true
				break
			}
			time.Sleep(20 * time.Millisecond)
		}
		if !observedRejection {
			t.Fatalf("expected routing rejection (Unavailable: did not match a configured Route) after update returned, got: %v", lastErr)
		}
	}
}

// evalC2Listener is a user-supplied net.Listener. When armed, a connection
// that the kernel already handed to Accept() is held until listenerWrapper.Close()
// has finished teardown and unlinked the listen socket from channelz.
type evalC2Listener struct {
	net.Listener
	armed         atomic.Bool
	accepted      chan struct{}
	closeReturned chan struct{}
	closeOnce     sync.Once
	resumeAccept  chan struct{}
}

func (l *evalC2Listener) Accept() (net.Conn, error) {
	c, err := l.Listener.Accept()
	if err == nil && l.armed.CompareAndSwap(true, false) {
		close(l.accepted)
		<-l.resumeAccept
	}
	return c, err
}

func (l *evalC2Listener) Close() error {
	err := l.Listener.Close()
	l.closeOnce.Do(func() { close(l.closeReturned) })
	return err
}

// TestEval_ServerSideXDS_StopWithConnectionAcceptanceOverlap verifies claim C2:
// A concurrent connection accepted by the underlying listener just before Stop()
// and processed by listenerWrapper.Accept() after Close() ran must not panic on nil
// filter chain lookup or deadlock in Stop().
func (s) TestEval_ServerSideXDS_StopWithConnectionAcceptanceOverlap(t *testing.T) {
	channelz.TurnOn()
	ms, nodeID, bootstrapContents, _ := setup.ManagementServerAndResolver(t)
	ctx, cancel := context.WithTimeout(context.Background(), defaultTestTimeout)
	defer cancel()

	gl := &evalC2Listener{
		accepted:      make(chan struct{}),
		closeReturned: make(chan struct{}),
		resumeAccept:  make(chan struct{}),
	}
	servingCh := make(chan struct{}, 8)
	opt := xds.ServingModeCallback(func(_ net.Addr, args xds.ServingModeChangeArgs) {
		if args.Mode == connectivity.ServingModeServing {
			servingCh <- struct{}{}
		}
	})
	stub := &stubserver.StubServer{
		EmptyCallF: func(context.Context, *testpb.Empty) (*testpb.Empty, error) { return &testpb.Empty{}, nil },
	}
	var err error
	if stub.S, err = xds.NewGRPCServer(grpc.Creds(insecure.NewCredentials()), opt, xds.BootstrapContentsForTesting(bootstrapContents)); err != nil {
		t.Fatalf("xds.NewGRPCServer: %v", err)
	}
	lis, err := testutils.LocalTCPListener()
	if err != nil {
		t.Fatal(err)
	}
	host, port, err := hostPortFromListener(lis)
	if err != nil {
		t.Fatal(err)
	}
	if err := ms.Update(ctx, e2e.UpdateOptions{
		NodeID:    nodeID,
		Listeners: []*v3listenerpb.Listener{e2e.DefaultServerListener(host, port, e2e.SecurityLevelNone, "routeName")},
	}); err != nil {
		t.Fatal(err)
	}
	gl.Listener = lis
	stub.Listener = gl
	stubserver.StartTestService(t, stub)
	select {
	case <-servingCh:
	case <-ctx.Done():
		t.Fatal("timeout waiting for SERVING")
	}
	addr := net.JoinHostPort(host, strconv.Itoa(int(port)))

	// Sanity: the server serves RPCs.
	cc, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	defer cc.Close()
	if _, err := testgrpc.NewTestServiceClient(cc).EmptyCall(ctx, &testpb.Empty{}, grpc.WaitForReady(true)); err != nil {
		t.Fatalf("EmptyCall: %v", err)
	}

	// Identify server listen socket ID in channelz by matching local address.
	var socketID int64 = -1
	wantAddr := lis.Addr().String()
	servers, _ := channelz.GetServers(0, 100)
	for _, srv := range servers {
		for sockID := range srv.ListenSockets() {
			sock := channelz.GetSocket(sockID)
			if sock != nil && sock.LocalAddr != nil && sock.LocalAddr.String() == wantAddr {
				socketID = sockID
				break
			}
		}
		if socketID != -1 {
			break
		}
	}
	if socketID == -1 {
		t.Fatal("failed to identify server listen socket in channelz")
	}

	gl.armed.Store(true)
	dialer := &net.Dialer{Timeout: defaultTestShortTimeout}
	c, err := dialer.DialContext(ctx, "tcp", addr)
	if err != nil {
		t.Fatalf("DialContext: %v", err)
	}
	defer c.Close()
	select {
	case <-gl.accepted:
	case <-ctx.Done():
		t.Fatal("timeout waiting for accept")
	}

	stopDone := make(chan struct{})
	go func() {
		stub.S.Stop()
		close(stopDone)
	}()

	// Observable teardown barrier: wait until channelz observes that
	// listenSocket.Close() (wrapping listenerWrapper.Close()) has finished
	// and removed the socket entry.
	pollDeadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(pollDeadline) {
		if channelz.GetSocket(socketID) == nil {
			break
		}
		time.Sleep(1 * time.Millisecond)
	}
	if channelz.GetSocket(socketID) != nil {
		t.Fatal("timeout waiting for listenSocket to be removed from channelz")
	}

	// Teardown is proven complete. Resume Accept().
	close(gl.resumeAccept)

	select {
	case <-stopDone:
		// Clean return from Stop().
	case <-time.After(5 * time.Second):
		t.Fatal("timeout waiting for Stop() to return; server hung or deadlocked")
	}
}
