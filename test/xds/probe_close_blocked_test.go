package xds_test

import (
	"context"
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

// Blocks the retired interceptor's Close, then checks that a fresh client
// connection is still accepted and served by the replacement.
func (s) TestProbe_NewConnWhileRetiredCloseBlocked(t *testing.T) {
	release := make(chan struct{})
	var releaseOnce sync.Once
	doRelease := func() { releaseOnce.Do(func() { close(release) }) }
	defer doRelease()
	closeEntered := make(chan struct{}, 4)
	pathCh := make(chan string, 256)

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
			select {
			case closeEntered <- struct{}{}:
			default:
			}
			<-release
		},
	}
	httpfilter.Register(fb)
	defer httpfilter.UnregisterForTesting(fb.typeURL)

	managementServer, nodeID, bootstrapContents, xdsResolver := setup.ManagementServerAndResolver(t)
	lis, stopServer := setupGRPCServer(t, bootstrapContents)
	var stopOnce sync.Once
	stop := func() { stopOnce.Do(stopServer) }
	defer func() { doRelease(); stop() }()

	host, port, err := hostPortFromListener(lis)
	if err != nil {
		t.Fatalf("hostPortFromListener: %v", err)
	}
	resources := e2e.DefaultClientResources(e2e.ResourceParams{
		DialTarget: "my-service",
		NodeID:     nodeID,
		Host:       host,
		Port:       port,
		SecLevel:   e2e.SecurityLevelNone,
	})
	clientRoute := resources.Routes[0]
	resources.Listeners = append(resources.Listeners, evalC2C3ServerListener(t, host, port, fb.typeURL))
	resources.Routes = []*v3routepb.RouteConfiguration{clientRoute, evalC2C3RouteConfig(t, fb.typeURL, "")}

	ctx, cancel := context.WithTimeout(context.Background(), defaultTestTimeout)
	defer cancel()
	if err := managementServer.Update(ctx, resources); err != nil {
		t.Fatal(err)
	}
	cc, err := grpc.NewClient("xds:///my-service", grpc.WithTransportCredentials(insecure.NewCredentials()), grpc.WithResolvers(xdsResolver))
	if err != nil {
		t.Fatal(err)
	}
	defer cc.Close()
	client := testgrpc.NewTestServiceClient(cc)
	if _, err := client.EmptyCall(ctx, &testpb.Empty{}, grpc.WaitForReady(true)); err != nil {
		t.Fatalf("probe: initial RPC failed: %v", err)
	}

	resources.Routes = []*v3routepb.RouteConfiguration{clientRoute, evalC2C3RouteConfig(t, fb.typeURL, "path2")}
	if err := managementServer.Update(ctx, resources); err != nil {
		t.Fatal(err)
	}
	select {
	case <-closeEntered:
		t.Log("probe: retired interceptor Close entered and is blocked")
	case <-time.After(10 * time.Second):
		t.Fatal("probe: retired interceptor Close was never called")
	}

	cc2, err := grpc.NewClient("passthrough:///"+lis.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	defer cc2.Close()
	rctx, rcancel := context.WithTimeout(ctx, 3*time.Second)
	defer rcancel()
	if _, err := testgrpc.NewTestServiceClient(cc2).EmptyCall(rctx, &testpb.Empty{}, grpc.WaitForReady(true)); err != nil {
		t.Errorf("probe: new-connection RPC failed while retired Close is blocked: %v", err)
	} else {
		t.Log("probe: new-connection RPC succeeded while retired Close is blocked")
	}
}
