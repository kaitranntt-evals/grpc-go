// Run: cp verify/repro/c3_rds_close_race_test.go test/xds/ on branch evalon/grpc-go-xd-8251bfa4, then: go test -race -count=1 -run '^Test$/^Verify_RDSHandlerCloseRacesWithRDSUpdates$' ./test/xds
package xds_test

import (
	"context"
	"fmt"
	"net"
	"testing"
	"time"

	"google.golang.org/grpc/connectivity"
	"google.golang.org/grpc/internal/testutils/xds/e2e"
	"google.golang.org/grpc/internal/testutils/xds/e2e/setup"
	"google.golang.org/grpc/xds"

	v3listenerpb "github.com/envoyproxy/go-control-plane/envoy/config/listener/v3"
	v3routepb "github.com/envoyproxy/go-control-plane/envoy/config/route/v3"
)

// An xDS-enabled server whose listener resolves its route configuration via
// RDS is stopped with a plain Stop() while the management server keeps
// pushing new versions of that RouteConfiguration. No hooks, no
// instrumentation: the race detector reports whether rdsHandler.close() and
// the RDS watcher callbacks touch the handler's maps without synchronization.
func (s) TestVerify_RDSHandlerCloseRacesWithRDSUpdates(t *testing.T) {
	const iterations = 30
	for i := 0; i < iterations; i++ {
		verifyStopWhileRDSUpdates(t)
	}
}

func verifyStopWhileRDSUpdates(t *testing.T) {
	managementServer, nodeID, bootstrapContents, _ := setup.ManagementServerAndResolver(t)

	servingCh := make(chan struct{}, 1)
	opt := xds.ServingModeCallback(func(_ net.Addr, args xds.ServingModeChangeArgs) {
		if args.Mode == connectivity.ServingModeServing {
			select {
			case servingCh <- struct{}{}:
			default:
			}
		}
	})
	lis, stopServer := setupGRPCServer(t, bootstrapContents, opt)
	defer stopServer()

	host, port, err := hostPortFromListener(lis)
	if err != nil {
		t.Fatalf("failed to retrieve host and port of server: %v", err)
	}
	const routeName = "verify-route"
	route := func(i int) *v3routepb.RouteConfiguration {
		rc := e2e.RouteConfigNonForwardingAction(routeName)
		rc.VirtualHosts[0].Name = fmt.Sprintf("vh-%d", i)
		return rc
	}
	resources := e2e.UpdateOptions{
		NodeID:         nodeID,
		Listeners:      []*v3listenerpb.Listener{e2e.DefaultServerListenerWithRouteConfigName(host, port, e2e.SecurityLevelNone, routeName)},
		Routes:         []*v3routepb.RouteConfiguration{route(0)},
		SkipValidation: true,
	}
	ctx, cancel := context.WithTimeout(context.Background(), defaultTestTimeout)
	defer cancel()
	if err := managementServer.Update(ctx, resources); err != nil {
		t.Fatal(err)
	}
	select {
	case <-servingCh:
	case <-ctx.Done():
		t.Fatal("Timeout waiting for server to enter SERVING mode")
	}

	// Control plane keeps updating the RouteConfiguration being served.
	stopUpdates := make(chan struct{})
	updatesDone := make(chan struct{})
	go func() {
		defer close(updatesDone)
		for i := 1; ; i++ {
			select {
			case <-stopUpdates:
				return
			default:
			}
			resources.Routes[0] = route(i)
			if err := managementServer.Update(ctx, resources); err != nil {
				return
			}
			time.Sleep(time.Millisecond)
		}
	}()

	// Ordinary shutdown while updates are flowing.
	time.Sleep(30 * time.Millisecond)
	stopServer()
	close(stopUpdates)
	<-updatesDone
}
