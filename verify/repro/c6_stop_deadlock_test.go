// Run: cp verify/repro/c6_stop_deadlock_test.go test/xds/verify_c6_stop_deadlock_test.go && go test -race -count=1 -v -run '^Test$/^VerifyC6_' ./test/xds
//
// Audit repro for C6 (run v-a332a9ce). A live xDS-enabled gRPC server has an
// HTTP filter whose interceptor's AllowRPC blocks until the RPC context is
// cancelled (its Close is a no-op). While one RPC is inside that AllowRPC, the
// test calls Server.Stop() and observes, with a 5s bound, whether Stop cancels
// the RPC and returns. The test FAILS when Stop neither returns nor cancels the
// RPC within the bound, and prints the goroutines involved in the wait cycle.
// (After the observation an escape channel releases the interceptor so the
// test binary can exit.)

package xds_test

import (
	"context"
	"fmt"
	"net"
	"runtime"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/connectivity"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/internal/resolver"
	"google.golang.org/grpc/internal/testutils"
	"google.golang.org/grpc/internal/testutils/xds/e2e"
	"google.golang.org/grpc/internal/testutils/xds/e2e/setup"
	"google.golang.org/grpc/internal/xds/httpfilter"
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
	"google.golang.org/protobuf/types/known/structpb"
)

type vc6Cfg struct{ httpfilter.FilterConfig }

type vc6Builder struct {
	httpfilter.Builder
	typeURL   string
	armed     atomic.Bool
	entered   chan struct{}
	cancelled chan struct{} // closed when the armed AllowRPC observes ctx.Done()
	escape    chan struct{} // test-only release after the observation
}

func (b *vc6Builder) IsTerminal() bool   { return false }
func (b *vc6Builder) TypeURLs() []string { return []string{b.typeURL} }
func (*vc6Builder) ParseFilterConfig(proto.Message) (httpfilter.FilterConfig, error) {
	return vc6Cfg{}, nil
}
func (*vc6Builder) ParseFilterConfigOverride(proto.Message) (httpfilter.FilterConfig, error) {
	return vc6Cfg{}, nil
}
func (b *vc6Builder) BuildServerFilter() httpfilter.ServerFilter { return b }
func (b *vc6Builder) Close()                                     {}
func (b *vc6Builder) BuildServerInterceptor(_, _ httpfilter.FilterConfig) (resolver.ServerInterceptor, error) {
	return &vc6Icpt{b: b}, nil
}

type vc6Icpt struct{ b *vc6Builder }

func (i *vc6Icpt) AllowRPC(ctx context.Context) error {
	if !i.b.armed.CompareAndSwap(true, false) {
		return nil
	}
	close(i.b.entered)
	select {
	case <-ctx.Done(): // the behavior under audit: wait for RPC context cancellation
		close(i.b.cancelled)
		return ctx.Err()
	case <-i.b.escape:
		return fmt.Errorf("released by test escape hatch")
	}
}
func (i *vc6Icpt) Close() {} // nonblocking

// vc6Stacks returns the goroutine dumps that mention any of the given substrings.
func vc6Stacks(subs ...string) string {
	buf := make([]byte, 4<<20)
	buf = buf[:runtime.Stack(buf, true)]
	var out []string
	for _, g := range strings.Split(string(buf), "\n\n") {
		for _, s := range subs {
			if strings.Contains(g, s) {
				var keep []string
				for _, line := range strings.Split(g, "\n") {
					if strings.HasPrefix(line, "goroutine ") || (!strings.HasPrefix(line, "\t") && (strings.Contains(line, "grpc") || strings.Contains(line, "sync."))) {
						keep = append(keep, line)
					}
				}
				out = append(out, strings.Join(keep, "\n"))
				break
			}
		}
	}
	return strings.Join(out, "\n\n")
}

func (s) TestVerifyC6_StopWithCancellationWaitingInterceptor(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	fb := &vc6Builder{typeURL: t.Name(), entered: make(chan struct{}), cancelled: make(chan struct{}), escape: make(chan struct{})}
	httpfilter.Register(fb)
	defer httpfilter.UnregisterForTesting(fb.typeURL)

	ms, nodeID, bootstrapContents, xdsResolver := setup.ManagementServerAndResolver(t)
	servingCh := make(chan struct{}, 10)
	lis, stopServer := setupGRPCServer(t, bootstrapContents, xds.ServingModeCallback(func(_ net.Addr, args xds.ServingModeChangeArgs) {
		if args.Mode == connectivity.ServingModeServing {
			select {
			case servingCh <- struct{}{}:
			default:
			}
		}
	}))
	host, port, err := hostPortFromListener(lis)
	if err != nil {
		t.Fatal(err)
	}
	const serviceName, routeName = "my-service", "routeName"
	resources := e2e.DefaultClientResources(e2e.ResourceParams{DialTarget: serviceName, NodeID: nodeID, Host: host, Port: port, SecLevel: e2e.SecurityLevelNone})
	resources.Listeners = append(resources.Listeners, &v3listenerpb.Listener{
		Name: fmt.Sprintf(e2e.ServerListenerResourceNameTemplate, net.JoinHostPort(host, strconv.Itoa(int(port)))),
		Address: &v3corepb.Address{Address: &v3corepb.Address_SocketAddress{SocketAddress: &v3corepb.SocketAddress{
			Address: host, PortSpecifier: &v3corepb.SocketAddress_PortValue{PortValue: port}}}},
		FilterChains: []*v3listenerpb.FilterChain{{
			Name: "only-chain",
			Filters: []*v3listenerpb.Filter{{
				Name: "hcm",
				ConfigType: &v3listenerpb.Filter_TypedConfig{TypedConfig: testutils.MarshalAny(t, &v3httppb.HttpConnectionManager{
					HttpFilters: []*v3httppb.HttpFilter{
						{Name: "waiter", ConfigType: &v3httppb.HttpFilter_TypedConfig{TypedConfig: testutils.MarshalAny(t, &v3xdsxdstypepb.TypedStruct{TypeUrl: fb.typeURL, Value: &structpb.Struct{}})}},
						e2e.HTTPFilter("router", &v3routerpb.Router{}),
					},
					RouteSpecifier: &v3httppb.HttpConnectionManager_Rds{Rds: &v3httppb.Rds{
						ConfigSource:    &v3corepb.ConfigSource{ConfigSourceSpecifier: &v3corepb.ConfigSource_Ads{Ads: &v3corepb.AggregatedConfigSource{}}},
						RouteConfigName: routeName,
					}},
				})},
			}},
		}},
	})
	resources.Routes = append(resources.Routes, &v3routepb.RouteConfiguration{Name: routeName, VirtualHosts: []*v3routepb.VirtualHost{{
		Domains: []string{"*"},
		Routes: []*v3routepb.Route{{
			Match:  &v3routepb.RouteMatch{PathSpecifier: &v3routepb.RouteMatch_Prefix{Prefix: "/"}},
			Action: &v3routepb.Route_NonForwardingAction{},
		}},
	}}})
	if err := ms.Update(ctx, resources); err != nil {
		t.Fatal(err)
	}
	cc, err := grpc.NewClient(fmt.Sprintf("xds:///%s", serviceName), grpc.WithTransportCredentials(insecure.NewCredentials()), grpc.WithResolvers(xdsResolver))
	if err != nil {
		t.Fatal(err)
	}
	defer cc.Close()
	select {
	case <-servingCh:
	case <-ctx.Done():
		t.Fatal("timeout waiting for SERVING")
	}
	client := testgrpc.NewTestServiceClient(cc)
	if _, err := client.EmptyCall(ctx, &testpb.Empty{}, grpc.WaitForReady(true)); err != nil {
		t.Fatalf("EmptyCall() failed: %v", err)
	}

	fb.armed.Store(true)
	rpcDone := make(chan error, 1)
	go func() {
		_, err := client.EmptyCall(ctx, &testpb.Empty{})
		rpcDone <- err
	}()
	select {
	case <-fb.entered:
		t.Logf("RPC is inside AllowRPC, waiting for its context to be cancelled")
	case <-ctx.Done():
		t.Fatal("timeout waiting for the RPC to enter the interceptor")
	}

	stopDone := make(chan struct{})
	start := time.Now()
	go func() {
		stopServer()
		close(stopDone)
	}()

	const bound = 5 * time.Second
	stopped, rpcCancelled := false, false
	timer := time.After(bound)
wait:
	for !(stopped && rpcCancelled) {
		select {
		case <-stopDone:
			stopped, stopDone = true, nil
			t.Logf("Server.Stop() returned after %v", time.Since(start).Round(time.Millisecond))
		case <-fb.cancelled:
			rpcCancelled, fb.cancelled = true, nil
			t.Logf("RPC context was cancelled %v after Stop() was called", time.Since(start).Round(time.Millisecond))
		case <-timer:
			break wait
		}
	}
	t.Logf("RESULT after %v bound: Stop returned=%v, RPC context cancelled=%v", bound, stopped, rpcCancelled)
	if !stopped || !rpcCancelled {
		t.Logf("goroutines in the wait cycle:\n%s", vc6Stacks("vc6Icpt).AllowRPC", "listenerWrapper).Close", "GRPCServer).Stop"))
		t.Errorf("Server.Stop() deadlocked: after %v Stop returned=%v and the RPC context was cancelled=%v", bound, stopped, rpcCancelled)
		// Release the interceptor so that the test binary can exit.
		close(fb.escape)
		if stopDone != nil {
			select {
			case <-stopDone:
				t.Logf("after releasing the interceptor through the test escape hatch, Server.Stop() returned (%v after it was called)", time.Since(start).Round(time.Millisecond))
			case <-time.After(bound):
				t.Logf("Server.Stop() still blocked %v after the interceptor was released", bound)
			}
		}
	}
	select {
	case err := <-rpcDone:
		t.Logf("client RPC finished with: %v", err)
	case <-time.After(bound):
		t.Logf("client RPC still pending")
	}
}
