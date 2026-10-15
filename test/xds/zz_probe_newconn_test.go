package xds_test

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	v3routepb "github.com/envoyproxy/go-control-plane/envoy/config/route/v3"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/internal/testutils/xds/e2e"
	"google.golang.org/grpc/internal/testutils/xds/e2e/setup"
	"google.golang.org/grpc/internal/xds/httpfilter"
	testgrpc "google.golang.org/grpc/interop/grpc_testing"
	testpb "google.golang.org/grpc/interop/grpc_testing"
)

func (s) TestProbe_NewConnectionWhileAllowRPCHeld(t *testing.T) {
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
	httpfilter.Register(fb)
	t.Cleanup(func() { httpfilter.UnregisterForTesting(fb.typeURL) })

	managementServer, nodeID, bootstrapContents, xdsResolver := setup.ManagementServerAndResolver(t)
	lis, stopServer := setupGRPCServer(t, bootstrapContents)
	defer func() { doRelease(); stopServer() }()
	host, port, err := hostPortFromListener(lis)
	if err != nil {
		t.Fatal(err)
	}
	const serviceName = "my-service"
	resources := e2e.DefaultClientResources(e2e.ResourceParams{DialTarget: serviceName, NodeID: nodeID, Host: host, Port: port, SecLevel: e2e.SecurityLevelNone})
	clientRoute := resources.Routes[0]
	resources.Listeners = append(resources.Listeners, evalC2C3ServerListener(t, host, port, fb.typeURL))
	resources.Routes = []*v3routepb.RouteConfiguration{clientRoute, evalC2C3RouteConfig(t, fb.typeURL, "")}
	ctx, cancel := context.WithTimeout(context.Background(), defaultTestTimeout)
	defer cancel()
	if err := managementServer.Update(ctx, resources); err != nil {
		t.Fatal(err)
	}
	ccA, err := grpc.NewClient(fmt.Sprintf("xds:///%s", serviceName), grpc.WithTransportCredentials(insecure.NewCredentials()), grpc.WithResolvers(xdsResolver))
	if err != nil {
		t.Fatal(err)
	}
	defer ccA.Close()
	clientA := testgrpc.NewTestServiceClient(ccA)

	heldDone := make(chan error, 1)
	go func() {
		_, err := clientA.EmptyCall(ctx, &testpb.Empty{}, grpc.WaitForReady(true))
		heldDone <- err
	}()
	select {
	case <-allowEntered:
	case <-ctx.Done():
		t.Fatal("timeout waiting for AllowRPC(path1) to enter")
	}
	// Push replacement while AllowRPC(path1) is held.
	resources.Routes = []*v3routepb.RouteConfiguration{clientRoute, evalC2C3RouteConfig(t, fb.typeURL, "path2")}
	if err := managementServer.Update(ctx, resources); err != nil {
		t.Fatal(err)
	}
	// Give the update time to reach the server.
	time.Sleep(500 * time.Millisecond)

	// Open a NEW connection B and issue an RPC with a 3s deadline.
	ccB, err := grpc.NewClient(lis.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	defer ccB.Close()
	clientB := testgrpc.NewTestServiceClient(ccB)
	bctx, bcancel := context.WithTimeout(ctx, 3*time.Second)
	defer bcancel()
	start := time.Now()
	_, errB := clientB.EmptyCall(bctx, &testpb.Empty{}, grpc.WaitForReady(true))
	t.Logf("PROBE: new-connection RPC while AllowRPC(path1) held: err=%v after %v", errB, time.Since(start))
	doRelease()
	select {
	case err := <-heldDone:
		t.Logf("PROBE: held RPC completed: err=%v", err)
	case <-time.After(5 * time.Second):
		t.Fatal("held RPC did not complete after release")
	}
	if errB != nil {
		t.Fatalf("STALL: RPC on new connection failed while AllowRPC on retired config was held: %v", errB)
	}
	served := ""
	select {
	case served = <-pathCh:
	default:
	}
	t.Logf("PROBE: new-connection RPC served by %q", served)
}
