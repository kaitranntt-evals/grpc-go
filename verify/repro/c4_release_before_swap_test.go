//go:build verify_repro

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

// Run: cp verify/repro/c4_release_before_swap_test.go internal/xds/balancer/clusterimpl/tests/ && go test -tags verify_repro ./internal/xds/balancer/clusterimpl/tests/ -run '^TestVerifyC4_ReleaseBeforeSwap$' -count=3 -v
//
// Same scenario and synchronization as TestSecurityConfigUpdate_ConcurrentHandshake
// (wait for builtB, then releaseA), except provider B's build function parks
// after signalling builtB. handleSecurityConfig cannot Swap xdsHIPtr or release
// the previous owner until that build returns, so if the blocked KeyMaterial
// call resumes and returns while the build is still parked, the original
// test's synchronization does not require replacement completion.
// (The RPC itself cannot complete while the balancer is parked, because the
// picker update is serialized behind handleSecurityConfig; so the observable
// used here is the KeyMaterial return, which is what the claim is about.)

package clusterimpl_test

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"testing"

	"github.com/google/uuid"
	"google.golang.org/grpc/credentials/tls/certprovider"
	"google.golang.org/grpc/internal/testutils"
	"google.golang.org/grpc/internal/testutils/xds/e2e"
	"google.golang.org/grpc/internal/xds/bootstrap"

	testgrpc "google.golang.org/grpc/interop/grpc_testing"
	testpb "google.golang.org/grpc/interop/grpc_testing"
)

type verifyC4ObservedProvider struct {
	*calPerfectRootProvider
	returned chan struct{}
}

func (p *verifyC4ObservedProvider) KeyMaterial(ctx context.Context) (*certprovider.KeyMaterial, error) {
	km, err := p.calPerfectRootProvider.KeyMaterial(ctx)
	select {
	case p.returned <- struct{}{}:
	default:
	}
	return km, err
}

type verifyC4ParkingBuilder struct {
	name     string
	provider certprovider.Provider
	built    chan struct{}
	hold     chan struct{}
	returned chan struct{}
}

func (b *verifyC4ParkingBuilder) Name() string { return b.name }

func (b *verifyC4ParkingBuilder) ParseConfig(any) (*certprovider.BuildableConfig, error) {
	return certprovider.NewBuildableConfig(b.name, nil, func(certprovider.BuildOptions) certprovider.Provider {
		select {
		case b.built <- struct{}{}:
		default:
		}
		<-b.hold
		select {
		case b.returned <- struct{}{}:
		default:
		}
		return b.provider
	}), nil
}

func TestVerifyC4_ReleaseBeforeSwap(t *testing.T) {
	const (
		instanceA = "verify-c4-root-a"
		instanceB = "verify-c4-root-b"
		target    = "test.service"
	)
	roots := calPerfectLoadServerCACertPool(t)

	providerA := &verifyC4ObservedProvider{
		calPerfectRootProvider: &calPerfectRootProvider{
			roots:   roots,
			entered: make(chan struct{}, 1),
			release: make(chan struct{}),
			closed:  make(chan struct{}),
		},
		returned: make(chan struct{}, 1),
	}
	var releaseOnce sync.Once
	releaseA := func() { releaseOnce.Do(func() { close(providerA.release) }) }
	t.Cleanup(releaseA)

	providerB := &calPerfectRootProvider{
		roots:   roots,
		entered: make(chan struct{}, 1),
		closed:  make(chan struct{}),
	}
	builtB := make(chan struct{}, 1)
	holdB := make(chan struct{})
	var holdOnce sync.Once
	unparkB := func() { holdOnce.Do(func() { close(holdB) }) }
	returnedB := make(chan struct{}, 1)

	builderA := &calPerfectProviderBuilder{name: fmt.Sprintf("verify-c4-a-%s", uuid.New()), provider: providerA}
	builderB := &verifyC4ParkingBuilder{name: fmt.Sprintf("verify-c4-b-%s", uuid.New()), provider: providerB, built: builtB, hold: holdB, returned: returnedB}
	certprovider.Register(builderA)
	certprovider.Register(builderB)

	mgmtServer := e2e.StartManagementServer(t, e2e.ManagementServerOptions{})
	nodeID := uuid.New().String()
	providerCfg := func(plugin string) json.RawMessage {
		return json.RawMessage(fmt.Sprintf(`{"plugin_name": %q, "config": {}}`, plugin))
	}
	bootstrapContents, err := bootstrap.NewContentsForTesting(bootstrap.ConfigOptionsForTesting{
		Servers: []byte(fmt.Sprintf(`[{
			"server_uri": "passthrough:///%s",
			"channel_creds": [{"type": "insecure"}],
			"server_features": ["trusted_xds_server"]
		}]`, mgmtServer.Address)),
		Node: []byte(fmt.Sprintf(`{"id": "%s"}`, nodeID)),
		CertificateProviders: map[string]json.RawMessage{
			instanceA: providerCfg(builderA.name),
			instanceB: providerCfg(builderB.name),
		},
		ServerListenerResourceNameTemplate: e2e.ServerListenerResourceNameTemplate,
	})
	if err != nil {
		t.Fatalf("Failed to create bootstrap configuration: %v", err)
	}

	cc, serverAddress := setupForSecurityTests(t, bootstrapContents, xdsClientCredsWithInsecureFallback(t), tlsServerCreds(t))
	// Registered after setup so the balancer is unparked before cc.Close runs.
	t.Cleanup(unparkB)
	resources := e2e.DefaultClientResources(e2e.ResourceParams{
		DialTarget: target,
		NodeID:     nodeID,
		Host:       "localhost",
		Port:       testutils.ParsePort(t, serverAddress),
		SecLevel:   e2e.SecurityLevelNone,
	})
	clusterName := resources.Clusters[0].Name
	serviceName := resources.Endpoints[0].ClusterName
	resources.Clusters[0] = calPerfectClientTLSCluster(t, clusterName, serviceName, instanceA)

	ctx, cancel := context.WithTimeout(context.Background(), defaultTestTimeout)
	defer cancel()
	if err := mgmtServer.Update(ctx, resources); err != nil {
		t.Fatalf("Failed to update management server with the initial Cluster: %v", err)
	}

	client := testgrpc.NewTestServiceClient(cc)
	rpcErr := make(chan error, 1)
	go func() {
		_, err := client.EmptyCall(ctx, &testpb.Empty{})
		rpcErr <- err
	}()
	select {
	case <-providerA.entered:
	case err := <-rpcErr:
		t.Fatalf("RPC ended before provider A KeyMaterial started: %v", err)
	case <-ctx.Done():
		t.Fatalf("Timed out waiting for provider A KeyMaterial: %v", ctx.Err())
	}

	resources.Clusters[0] = calPerfectClientTLSCluster(t, clusterName, serviceName, instanceB)
	if err := mgmtServer.Update(ctx, resources); err != nil {
		t.Fatalf("Failed to update management server with the replacement Cluster: %v", err)
	}
	// Identical to the audited test: the only synchronization before releasing
	// the blocked KeyMaterial call is builtB.
	calPerfectWaitForChan(ctx, t, builtB, "timed out waiting for replacement provider to be built")
	t.Log("event: builtB received (provider B build function entered, still parked)")

	releaseA()
	t.Log("event: releaseA() called")
	calPerfectWaitForChan(ctx, t, providerA.returned, "timed out waiting for provider A KeyMaterial to return")
	t.Log("event: blocked provider A KeyMaterial call resumed and RETURNED")

	select {
	case <-returnedB:
		t.Fatal("provider B build returned before we unparked it; cannot reason about ordering")
	default:
	}
	select {
	case <-providerA.closed:
		t.Fatal("provider A closed while handleSecurityConfig was still inside buildProviders (impossible: swap happens after build)")
	default:
		t.Log("observed: provider A NOT closed, handleSecurityConfig still parked inside buildProviders -> xdsHIPtr not yet swapped, previous owner not yet released")
	}

	unparkB()
	t.Log("event: provider B build unparked")
	calPerfectWaitForChan(ctx, t, returnedB, "timed out waiting for provider B build to return")
	select {
	case err := <-rpcErr:
		if err != nil {
			t.Fatalf("Active RPC failed: %v", err)
		}
		t.Log("event: active RPC completed successfully")
	case <-ctx.Done():
		t.Fatalf("Timed out waiting for the active RPC to finish: %v", ctx.Err())
	}
	calPerfectWaitForChan(ctx, t, providerA.closed, "timed out waiting for provider A to close after swap")
	t.Log("observed: provider A closed only after the build returned (swap + previous-owner release happened after KeyMaterial had already returned)")
}
