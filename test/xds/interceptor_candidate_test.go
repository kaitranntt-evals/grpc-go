package xds_test

import (
	"context"
	"fmt"
	"net"
	"strconv"
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

	v3corepb "github.com/envoyproxy/go-control-plane/envoy/config/core/v3"
	v3listenerpb "github.com/envoyproxy/go-control-plane/envoy/config/listener/v3"
	v3routepb "github.com/envoyproxy/go-control-plane/envoy/config/route/v3"
	v3routerpb "github.com/envoyproxy/go-control-plane/envoy/extensions/filters/http/router/v3"
	v3httppb "github.com/envoyproxy/go-control-plane/envoy/extensions/filters/network/http_connection_manager/v3"
	"google.golang.org/protobuf/types/known/wrapperspb"
)

func (s) TestCandidateInterceptorUpdate(t *testing.T) {
	var filtersCreated, filtersDestroyed, interceptorsCreated, interceptorsDestroyed atomic.Int32
	pathCh := make(chan string, 1)
	testFilterTypeURL := t.Name()
	fb := &trackingHTTPFilterBuilder{
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
					newHTTPFilter(t, "tracker", fb.typeURL, "path1"),
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

	// Update RDS configuration in place.
	resources.Routes = []*v3routepb.RouteConfiguration{resources.Routes[0], mkRouteConfig("/grpc.testing.TestService/UnaryCall")}
	if err := managementServer.Update(ctx, resources); err != nil {
		t.Fatal(err)
	}

	// Wait for replacement interceptor to be created and replaced interceptor to be closed.
	for ; ctx.Err() == nil; <-time.After(defaultTestShortTimeout) {
		client.EmptyCall(ctx, &testpb.Empty{})
		if interceptorsCreated.Load() >= 2 && interceptorsDestroyed.Load() >= 1 {
			break
		}
	}
	if got := interceptorsDestroyed.Load(); got < 1 {
		t.Fatalf("Interceptors were not destroyed after in-place RDS update; destroyed: %d, want >= 1", got)
	}
}
