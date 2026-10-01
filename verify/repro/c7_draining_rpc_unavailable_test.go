// Run: cp verify/repro/c7_draining_rpc_unavailable_test.go test/xds/verify_c7_draining_rpc_unavailable_test.go && go test -race -count=1 -v -run '^Test$/^VerifyC7_' ./test/xds
//
// Audit repro for C7 (run v-a332a9ce). A live xDS-enabled gRPC server uses a
// router-only HTTP connection manager. One RPC is paused after the server
// admitted it (in a stats.Handler at stats.Begin, i.e. before the xDS
// interceptor calls RouteAndProcess). Then either GracefulStop is called or
// the Listener resource is replaced, and the RPC is resumed while its
// connection is still draining. The test FAILS when the resumed RPC is rejected.

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
	xdscreds "google.golang.org/grpc/credentials/xds"
	"google.golang.org/grpc/internal/testutils"
	"google.golang.org/grpc/internal/testutils/xds/e2e"
	"google.golang.org/grpc/internal/testutils/xds/e2e/setup"
	"google.golang.org/grpc/stats"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/xds"

	v3corepb "github.com/envoyproxy/go-control-plane/envoy/config/core/v3"
	v3listenerpb "github.com/envoyproxy/go-control-plane/envoy/config/listener/v3"
	v3routepb "github.com/envoyproxy/go-control-plane/envoy/config/route/v3"
	v3routerpb "github.com/envoyproxy/go-control-plane/envoy/extensions/filters/http/router/v3"
	v3httppb "github.com/envoyproxy/go-control-plane/envoy/extensions/filters/network/http_connection_manager/v3"
	testgrpc "google.golang.org/grpc/interop/grpc_testing"
	testpb "google.golang.org/grpc/interop/grpc_testing"
)

// vc7Pauser blocks the armed RPC at stats.Begin, which the server emits after
// admitting the stream and before running interceptors.
type vc7Pauser struct {
	armed   atomic.Bool
	entered chan struct{}
	resume  chan struct{}
}

func (*vc7Pauser) TagRPC(ctx context.Context, _ *stats.RPCTagInfo) context.Context { return ctx }
func (p *vc7Pauser) HandleRPC(_ context.Context, s stats.RPCStats) {
	if _, ok := s.(*stats.Begin); ok && p.armed.CompareAndSwap(true, false) {
		close(p.entered)
		select {
		case <-p.resume:
		case <-time.After(30 * time.Second):
		}
	}
}
func (*vc7Pauser) TagConn(ctx context.Context, _ *stats.ConnTagInfo) context.Context { return ctx }
func (*vc7Pauser) HandleConn(context.Context, stats.ConnStats)                       {}

type vc7Service struct {
	testgrpc.UnimplementedTestServiceServer
}

func (vc7Service) EmptyCall(context.Context, *testpb.Empty) (*testpb.Empty, error) {
	return &testpb.Empty{}, nil
}

func vc7Listener(t *testing.T, host string, port uint32, chainName, routeName string) *v3listenerpb.Listener {
	return &v3listenerpb.Listener{
		Name: fmt.Sprintf(e2e.ServerListenerResourceNameTemplate, net.JoinHostPort(host, strconv.Itoa(int(port)))),
		Address: &v3corepb.Address{Address: &v3corepb.Address_SocketAddress{SocketAddress: &v3corepb.SocketAddress{
			Address: host, PortSpecifier: &v3corepb.SocketAddress_PortValue{PortValue: port}}}},
		FilterChains: []*v3listenerpb.FilterChain{{
			Name: chainName,
			Filters: []*v3listenerpb.Filter{{
				Name: "hcm",
				ConfigType: &v3listenerpb.Filter_TypedConfig{TypedConfig: testutils.MarshalAny(t, &v3httppb.HttpConnectionManager{
					// Router-only: no other HTTP filter is configured.
					HttpFilters: []*v3httppb.HttpFilter{e2e.HTTPFilter("router", &v3routerpb.Router{})},
					RouteSpecifier: &v3httppb.HttpConnectionManager_Rds{Rds: &v3httppb.Rds{
						ConfigSource:    &v3corepb.ConfigSource{ConfigSourceSpecifier: &v3corepb.ConfigSource_Ads{Ads: &v3corepb.AggregatedConfigSource{}}},
						RouteConfigName: routeName,
					}},
				})},
			}},
		}},
	}
}

func vc7Run(t *testing.T, trigger string) {
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	ms, nodeID, bootstrapContents, xdsResolver := setup.ManagementServerAndResolver(t)

	pauser := &vc7Pauser{entered: make(chan struct{}), resume: make(chan struct{})}
	creds, err := xdscreds.NewServerCredentials(xdscreds.ServerOptions{FallbackCreds: insecure.NewCredentials()})
	if err != nil {
		t.Fatal(err)
	}
	servingCh := make(chan struct{}, 10)
	server, err := xds.NewGRPCServer(grpc.Creds(creds), xds.BootstrapContentsForTesting(bootstrapContents), grpc.StatsHandler(pauser),
		xds.ServingModeCallback(func(_ net.Addr, args xds.ServingModeChangeArgs) {
			if args.Mode == connectivity.ServingModeServing {
				select {
				case servingCh <- struct{}{}:
				default:
				}
			}
		}))
	if err != nil {
		t.Fatal(err)
	}
	defer server.Stop()
	testgrpc.RegisterTestServiceServer(server, vc7Service{})
	lis, err := testutils.LocalTCPListener()
	if err != nil {
		t.Fatal(err)
	}
	go server.Serve(lis)

	host, port, err := hostPortFromListener(lis)
	if err != nil {
		t.Fatal(err)
	}
	const serviceName, routeName = "my-service", "routeName"
	resources := e2e.DefaultClientResources(e2e.ResourceParams{DialTarget: serviceName, NodeID: nodeID, Host: host, Port: port, SecLevel: e2e.SecurityLevelNone})
	clientListeners := resources.Listeners
	resources.Listeners = append(append([]*v3listenerpb.Listener{}, clientListeners...), vc7Listener(t, host, port, "chain-v1", routeName))
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
	var dials atomic.Int32
	cc, err := grpc.NewClient(fmt.Sprintf("xds:///%s", serviceName), grpc.WithTransportCredentials(insecure.NewCredentials()), grpc.WithResolvers(xdsResolver),
		grpc.WithContextDialer(func(ctx context.Context, addr string) (net.Conn, error) {
			dials.Add(1)
			return (&net.Dialer{}).DialContext(ctx, "tcp", addr)
		}))
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
		t.Fatalf("EmptyCall() before the trigger failed: %v", err)
	}
	t.Logf("baseline RPC succeeded (connections dialed so far: %d)", dials.Load())

	// Pause one RPC after admission and before RouteAndProcess.
	pauser.armed.Store(true)
	rpcDone := make(chan error, 1)
	go func() {
		_, err := client.EmptyCall(ctx, &testpb.Empty{})
		rpcDone <- err
	}()
	select {
	case <-pauser.entered:
		t.Logf("RPC admitted by the server and paused before RouteAndProcess")
	case <-ctx.Done():
		t.Fatal("timeout waiting for the RPC to be admitted")
	}

	stopDone := make(chan struct{})
	switch trigger {
	case "GracefulStop":
		go func() {
			server.GracefulStop()
			close(stopDone)
		}()
		// GracefulStop closes the listeners first and then waits for the
		// admitted RPC. Wait (bounded) until the listening socket is closed.
		deadline := time.Now().Add(5 * time.Second)
		for time.Now().Before(deadline) {
			c, err := net.DialTimeout("tcp", lis.Addr().String(), 200*time.Millisecond)
			if err != nil {
				t.Logf("listening socket is closed (%v); GracefulStop is now waiting for the admitted RPC", err)
				break
			}
			c.Close()
			time.Sleep(20 * time.Millisecond)
		}
		time.Sleep(300 * time.Millisecond)
		select {
		case <-stopDone:
			t.Fatalf("GracefulStop returned while an admitted RPC was still paused")
		default:
		}
	case "ListenerReplacement":
		close(stopDone)
		resources.Listeners = append(append([]*v3listenerpb.Listener{}, clientListeners...), vc7Listener(t, host, port, "chain-v2", routeName))
		if err := ms.Update(ctx, resources); err != nil {
			t.Fatal(err)
		}
		// The replacement drains old connections (GOAWAY). Wait (bounded)
		// until the client had to dial a second connection for new RPCs.
		deadline := time.Now().Add(5 * time.Second)
		for time.Now().Before(deadline) && dials.Load() < 2 {
			rctx, rcancel := context.WithTimeout(ctx, time.Second)
			if _, err := client.EmptyCall(rctx, &testpb.Empty{}); err != nil {
				t.Logf("NOTE: non-paused RPC issued while the old connection drains failed: code=%v desc=%q", status.Code(err), status.Convert(err).Message())
			}
			rcancel()
			time.Sleep(20 * time.Millisecond)
		}
		t.Logf("listener replacement applied: client connections dialed=%d (old connection is draining with the paused RPC on it)", dials.Load())
		time.Sleep(300 * time.Millisecond)
	}

	close(pauser.resume)
	var rpcErr error
	select {
	case rpcErr = <-rpcDone:
	case <-ctx.Done():
		t.Fatal("timeout waiting for the paused RPC to finish")
	}
	t.Logf("RESULT [%s]: admitted RPC resumed during draining finished with code=%v desc=%q", trigger, status.Code(rpcErr), status.Convert(rpcErr).Message())
	if rpcErr != nil {
		t.Errorf("[%s] already-admitted router-only RPC was rejected during connection draining: %v", trigger, rpcErr)
	}
	select {
	case <-stopDone:
	case <-time.After(5 * time.Second):
		t.Errorf("GracefulStop did not return within 5s after the admitted RPC finished")
	}
}

func (s) TestVerifyC7_GracefulStop(t *testing.T)        { vc7Run(t, "GracefulStop") }
func (s) TestVerifyC7_ListenerReplacement(t *testing.T) { vc7Run(t, "ListenerReplacement") }
