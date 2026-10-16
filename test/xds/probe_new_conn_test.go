package xds_test

import (
	"context"
	"sync"
	"sync/atomic"
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

// Holds AllowRPC on the original configuration, pushes replacements, and
// checks that fresh client connections are still accepted and served.
func (s) TestProbe_NewConnWhileRetiredAllowRPCHeld(t *testing.T) {
	allowEntered := make(chan struct{}, 1)
	release := make(chan struct{})
	var releaseOnce sync.Once
	doRelease := func() { releaseOnce.Do(func() { close(release) }) }
	defer doRelease()
	var held atomic.Bool
	pathCh := make(chan string, 256)

	fb := &evalC2C3FilterBuilder{
		typeURL: t.Name(),
		allowRPC: func(ctx context.Context, path string) error {
			if path == "path1" && held.CompareAndSwap(false, true) {
				allowEntered <- struct{}{}
				select {
				case <-release:
				case <-ctx.Done():
				}
				return nil
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

	go func() { client.EmptyCall(ctx, &testpb.Empty{}, grpc.WaitForReady(true)) }()
	select {
	case <-allowEntered:
	case <-time.After(defaultTestTimeout):
		t.Fatal("probe: timeout waiting for AllowRPC to be held")
	}

	update := func(p string) {
		resources.Routes = []*v3routepb.RouteConfiguration{clientRoute, evalC2C3RouteConfig(t, fb.typeURL, p)}
		if err := managementServer.Update(ctx, resources); err != nil {
			t.Fatal(err)
		}
	}

	// waitPath issues RPCs until one reports the wanted path or the deadline passes.
	waitPath := func(c testgrpc.TestServiceClient, want string, d time.Duration) bool {
		deadline := time.Now().Add(d)
		for time.Now().Before(deadline) {
			rctx, rcancel := context.WithTimeout(ctx, time.Second)
			c.EmptyCall(rctx, &testpb.Empty{})
			rcancel()
			for {
				select {
				case p := <-pathCh:
					if p == want {
						return true
					}
					continue
				default:
				}
				break
			}
			time.Sleep(20 * time.Millisecond)
		}
		return false
	}

	update("path2")
	if !waitPath(client, "path2", 10*time.Second) {
		t.Fatal("probe: replacement path2 never served on the established connection")
	}
	t.Log("probe: path2 served on established connection")

	freshConn := func(label string) testgrpc.TestServiceClient {
		cc2, err := grpc.NewClient("passthrough:///"+lis.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()))
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { cc2.Close() })
		c2 := testgrpc.NewTestServiceClient(cc2)
		rctx, rcancel := context.WithTimeout(ctx, 3*time.Second)
		defer rcancel()
		if _, err := c2.EmptyCall(rctx, &testpb.Empty{}, grpc.WaitForReady(true)); err != nil {
			t.Errorf("probe: %s: new-connection RPC failed while retired AllowRPC is held: %v", label, err)
		} else {
			t.Logf("probe: %s: new-connection RPC succeeded", label)
		}
		return c2
	}

	freshConn("after first replacement")
	update("path3")
	c3 := freshConn("after second replacement")
	if !waitPath(c3, "path3", 3*time.Second) {
		t.Error("probe: second replacement path3 not served within 3s while first AllowRPC is held")
	} else {
		t.Log("probe: path3 served")
	}
}
