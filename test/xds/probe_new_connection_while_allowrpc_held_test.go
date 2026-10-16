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

// Probe: while an in-flight RPC holds AllowRPC on the retired configuration
// and the replacement has been published, a brand-new connection to the same
// listener must be accepted and served by the replacement route.
func (s) TestProbe_ServerSideXDS_ReplacementRPCWhileAllowRPCHeld_NewConnection(t *testing.T) {
	pathCh := make(chan string, 64)
	allowEntered := make(chan struct{})
	releaseAllow := make(chan struct{})
	var allowOnce, releaseOnce sync.Once
	doRelease := func() { releaseOnce.Do(func() { close(releaseAllow) }) }
	defer doRelease()

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
	t.Cleanup(func() { httpfilter.UnregisterForTesting(fb.typeURL) })

	managementServer, nodeID, bootstrapContents, xdsResolver := setup.ManagementServerAndResolver(t)
	lis, stopServer := setupGRPCServer(t, bootstrapContents)
	t.Cleanup(func() { doRelease(); stopServer() })

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
	defer cancel()
	if err := managementServer.Update(ctx, resources); err != nil {
		t.Fatal(err)
	}

	cc, err := grpc.NewClient(fmt.Sprintf("xds:///%s", serviceName), grpc.WithTransportCredentials(insecure.NewCredentials()), grpc.WithResolvers(xdsResolver))
	if err != nil {
		t.Fatalf("grpc.NewClient() failed: %v", err)
	}
	defer cc.Close()
	client := testgrpc.NewTestServiceClient(cc)

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

	// Start the in-flight RPC that holds AllowRPC open on path1.
	heldDone := make(chan error, 1)
	go func() {
		_, err := client.EmptyCall(ctx, &testpb.Empty{}, grpc.WaitForReady(true))
		heldDone <- err
	}()
	select {
	case <-allowEntered:
	case <-ctx.Done():
		t.Fatal("timeout waiting for in-flight AllowRPC(path1) to enter")
	}

	// Publish the replacement while AllowRPC(path1) is held.
	resources.Routes = []*v3routepb.RouteConfiguration{clientRoute, evalC2C3RouteConfig(t, fb.typeURL, "path2")}
	if err := managementServer.Update(ctx, resources); err != nil {
		t.Fatal(err)
	}

	// Step 1 (same as the existing blocker subtest): the established connection
	// must be served by path2 while AllowRPC(path1) is still held.
	probeDeadline := time.Now().Add(3 * time.Second)
	replacementServed := false
	for time.Now().Before(probeDeadline) && !replacementServed {
		pctx, pcancel := context.WithTimeout(ctx, 300*time.Millisecond)
		_, err := client.EmptyCall(pctx, &testpb.Empty{})
		pcancel()
		for _, p := range drain() {
			if err == nil && p == "path2" {
				replacementServed = true
			}
		}
		if !replacementServed {
			time.Sleep(20 * time.Millisecond)
		}
	}
	if !replacementServed {
		t.Fatal("EXISTING-CONNECTION STALL: replacement configuration was not usable on the established connection while AllowRPC(path1) was held")
	}

	// Step 2 (the probe): a brand-new connection must be accepted and served by
	// path2 while AllowRPC(path1) is still held on the retired configuration.
	cc2, err := grpc.NewClient("passthrough:///"+net.JoinHostPort(host, strconv.Itoa(int(port))), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("grpc.NewClient() for second connection failed: %v", err)
	}
	defer cc2.Close()
	nctx, ncancel := context.WithTimeout(ctx, 3*time.Second)
	defer ncancel()
	drain()
	if _, err := testgrpc.NewTestServiceClient(cc2).EmptyCall(nctx, &testpb.Empty{}, grpc.WaitForReady(true)); err != nil {
		t.Fatalf("NEW-CONNECTION STALL: RPC on a new connection failed while AllowRPC(path1) was held on the retired configuration: %v", err)
	}
	for _, p := range drain() {
		if p != "path2" {
			t.Fatalf("RPC on new connection was served by %q, want path2", p)
		}
	}

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
