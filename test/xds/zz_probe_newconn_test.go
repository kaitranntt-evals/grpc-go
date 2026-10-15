package xds_test

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/internal/testutils/xds/e2e"
	"google.golang.org/grpc/internal/testutils/xds/e2e/setup"
	"google.golang.org/grpc/internal/xds/httpfilter"

	v3routepb "github.com/envoyproxy/go-control-plane/envoy/config/route/v3"
	testgrpc "google.golang.org/grpc/interop/grpc_testing"
	testpb "google.golang.org/grpc/interop/grpc_testing"
)

func (s) TestEvalProbe_NewConnWhileAllowRPCHeldDuringUpdate(t *testing.T) {
	allowEntered := make(chan struct{})
	releaseAllow := make(chan struct{})
	pathCh := make(chan string, 16)
	var allowOnce, releaseOnce sync.Once
	doRelease := func() { releaseOnce.Do(func() { close(releaseAllow) }) }
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
			case pathCh <- path:
			default:
			}
			return nil
		},
		onClose: func(string) {},
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
	cc, err := grpc.NewClient(fmt.Sprintf("xds:///%s", serviceName), grpc.WithTransportCredentials(insecure.NewCredentials()), grpc.WithResolvers(xdsResolver))
	if err != nil {
		t.Fatal(err)
	}
	defer cc.Close()
	client := testgrpc.NewTestServiceClient(cc)

	heldDone := make(chan error, 1)
	go func() {
		_, err := client.EmptyCall(ctx, &testpb.Empty{}, grpc.WaitForReady(true))
		heldDone <- err
	}()
	select {
	case <-allowEntered:
	case <-ctx.Done():
		t.Fatal("timeout waiting for AllowRPC(path1)")
	}

	resources.Routes = []*v3routepb.RouteConfiguration{clientRoute, evalC2C3RouteConfig(t, fb.typeURL, "path2")}
	if err := managementServer.Update(ctx, resources); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(3 * time.Second)
	served := false
	for !served && time.Now().Before(deadline) {
		pctx, pcancel := context.WithTimeout(ctx, 300*time.Millisecond)
		_, perr := client.EmptyCall(pctx, &testpb.Empty{})
		pcancel()
		for len(pathCh) > 0 {
			if <-pathCh == "path2" && perr == nil {
				served = true
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	if !served {
		doRelease()
		t.Fatalf("existing connection never served by path2 while AllowRPC(path1) held")
	}
	t.Logf("EXISTING connection served by path2 while AllowRPC(path1) held")

	cc2, err := grpc.NewClient(lis.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	defer cc2.Close()
	rctx, rcancel := context.WithTimeout(ctx, 3*time.Second)
	defer rcancel()
	_, err = testgrpc.NewTestServiceClient(cc2).EmptyCall(rctx, &testpb.Empty{}, grpc.WaitForReady(true))
	doRelease()
	if err != nil {
		t.Fatalf("NEW CONNECTION STALL: RPC on new connection failed while AllowRPC(path1) was held: %v", err)
	}
	t.Logf("NEW connection RPC succeeded while AllowRPC(path1) held")
	select {
	case <-heldDone:
	case <-time.After(3 * time.Second):
		t.Fatal("held RPC did not finish")
	}
}
