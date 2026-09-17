/*
 *
 * Copyright 2024 gRPC authors.
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

package clusterimpl_test

import (
	"context"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sync"
	"testing"

	"github.com/google/uuid"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/tls/certprovider"
	"google.golang.org/grpc/internal"
	"google.golang.org/grpc/internal/stubserver"
	"google.golang.org/grpc/internal/testutils"
	"google.golang.org/grpc/internal/testutils/xds/e2e"
	"google.golang.org/grpc/internal/xds/bootstrap"
	"google.golang.org/grpc/internal/xds/xdsclient/xdsresource"
	"google.golang.org/grpc/peer"
	"google.golang.org/grpc/resolver"
	"google.golang.org/grpc/testdata"

	v3clusterpb "github.com/envoyproxy/go-control-plane/envoy/config/cluster/v3"
	v3corepb "github.com/envoyproxy/go-control-plane/envoy/config/core/v3"
	v3tlspb "github.com/envoyproxy/go-control-plane/envoy/extensions/transport_sockets/tls/v3"
	testgrpc "google.golang.org/grpc/interop/grpc_testing"
	testpb "google.golang.org/grpc/interop/grpc_testing"
)

func calPerfectClientTLSCluster(t *testing.T, clusterName, serviceName, providerInstance string) *v3clusterpb.Cluster {
	t.Helper()
	cluster := e2e.DefaultCluster(clusterName, serviceName, e2e.SecurityLevelNone)
	cluster.TransportSocket = &v3corepb.TransportSocket{
		Name: "envoy.transport_sockets.tls",
		ConfigType: &v3corepb.TransportSocket_TypedConfig{
			TypedConfig: testutils.MarshalAny(t, &v3tlspb.UpstreamTlsContext{
				CommonTlsContext: &v3tlspb.CommonTlsContext{
					ValidationContextType: &v3tlspb.CommonTlsContext_ValidationContextCertificateProviderInstance{
						ValidationContextCertificateProviderInstance: &v3tlspb.CommonTlsContext_CertificateProviderInstance{
							InstanceName: providerInstance,
						},
					},
				},
			}),
		},
	}
	return cluster
}

func calPerfectLoadServerCACertPool(t *testing.T) *x509.CertPool {
	t.Helper()
	pemData, err := os.ReadFile(testdata.Path("x509/server_ca_cert.pem"))
	if err != nil {
		t.Fatalf("Failed to read server CA cert: %v", err)
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(pemData) {
		t.Fatal("Failed to parse testdata/x509/server_ca_cert.pem")
	}
	return roots
}

type calPerfectRootProvider struct {
	roots     *x509.CertPool
	entered   chan struct{}
	release   chan struct{}
	closed    chan struct{}
	mu        sync.Mutex
	isClosed  bool
	closeOnce sync.Once
}

func (p *calPerfectRootProvider) KeyMaterial(ctx context.Context) (*certprovider.KeyMaterial, error) {
	if p.entered != nil {
		select {
		case p.entered <- struct{}{}:
		default:
		}
	}
	if p.release != nil {
		select {
		case <-p.release:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.isClosed {
		return nil, errors.New("provider instance is closed")
	}
	return &certprovider.KeyMaterial{Roots: p.roots}, nil
}

func (p *calPerfectRootProvider) Close() {
	p.closeOnce.Do(func() {
		p.mu.Lock()
		p.isClosed = true
		p.mu.Unlock()
		close(p.closed)
	})
}

type calPerfectProviderBuilder struct {
	name     string
	provider certprovider.Provider
	built    chan struct{}
}

func (b *calPerfectProviderBuilder) Name() string { return b.name }

func (b *calPerfectProviderBuilder) ParseConfig(any) (*certprovider.BuildableConfig, error) {
	return certprovider.NewBuildableConfig(b.name, nil, func(certprovider.BuildOptions) certprovider.Provider {
		if b.built != nil {
			select {
			case b.built <- struct{}{}:
			default:
			}
		}
		return b.provider
	}), nil
}

type calPerfectResolverBuilder struct {
	resolver.Builder
	replacementRoot string
	applied         chan struct{}
}

func (b *calPerfectResolverBuilder) Build(target resolver.Target, cc resolver.ClientConn, opts resolver.BuildOptions) (resolver.Resolver, error) {
	return b.Builder.Build(target, &calPerfectClientConn{
		ClientConn:      cc,
		replacementRoot: b.replacementRoot,
		applied:         b.applied,
	}, opts)
}

type calPerfectClientConn struct {
	resolver.ClientConn
	replacementRoot string
	applied         chan struct{}
}

func (cc *calPerfectClientConn) UpdateState(state resolver.State) error {
	err := cc.ClientConn.UpdateState(state)
	if err != nil || state.Attributes == nil {
		return err
	}
	config := xdsresource.XDSConfigFromResolverState(state)
	if config == nil {
		return nil
	}
	for _, result := range config.Clusters {
		if result == nil || result.Err != nil || result.Config.Cluster == nil || result.Config.Cluster.SecurityCfg == nil {
			continue
		}
		if result.Config.Cluster.SecurityCfg.RootInstanceName == cc.replacementRoot {
			select {
			case cc.applied <- struct{}{}:
			default:
			}
			break
		}
	}
	return nil
}

func calPerfectWaitForChan(ctx context.Context, t *testing.T, ch <-chan struct{}, msg string) {
	t.Helper()
	select {
	case <-ch:
	case <-ctx.Done():
		t.Fatalf("%s: %v", msg, ctx.Err())
	}
}

// TestSecurityConfigUpdate_ConcurrentHandshake checks that a client handshake
// that started loading validation roots keeps its selected Cluster security
// configuration usable after replacement, and that a later connection uses the
// replacement provider.
func TestSecurityConfigUpdate_ConcurrentHandshake(t *testing.T) {
	const (
		instanceA = "handshake-lifetime-root-a"
		instanceB = "handshake-lifetime-root-b"
		target    = "test.service"
	)
	roots := calPerfectLoadServerCACertPool(t)

	providerA := &calPerfectRootProvider{
		roots:   roots,
		entered: make(chan struct{}, 1),
		release: make(chan struct{}),
		closed:  make(chan struct{}),
	}
	var releaseOnce sync.Once
	releaseA := func() {
		releaseOnce.Do(func() { close(providerA.release) })
	}
	t.Cleanup(releaseA)

	providerB := &calPerfectRootProvider{
		roots:   roots,
		entered: make(chan struct{}, 1),
		closed:  make(chan struct{}),
	}
	builtB := make(chan struct{}, 1)
	builderA := &calPerfectProviderBuilder{
		name:     fmt.Sprintf("handshake-lifetime-a-%s", uuid.New()),
		provider: providerA,
	}
	builderB := &calPerfectProviderBuilder{
		name:     fmt.Sprintf("handshake-lifetime-b-%s", uuid.New()),
		provider: providerB,
		built:    builtB,
	}
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

	r, err := internal.NewXDSResolverWithConfigForTesting.(func([]byte) (resolver.Builder, error))(bootstrapContents)
	if err != nil {
		t.Fatalf("Failed to create xDS resolver for testing: %v", err)
	}
	replacementApplied := make(chan struct{}, 1)
	r = &calPerfectResolverBuilder{
		Builder:         r,
		replacementRoot: instanceB,
		applied:         replacementApplied,
	}
	cc, err := grpc.NewClient(r.Scheme()+":///"+target, grpc.WithTransportCredentials(xdsClientCredsWithInsecureFallback(t)), grpc.WithResolvers(r))
	if err != nil {
		t.Fatalf("grpc.NewClient() failed: %v", err)
	}
	cc.Connect()
	t.Cleanup(func() { cc.Close() })
	server := stubserver.StartTestService(t, nil, grpc.Creds(tlsServerCreds(t)))
	t.Cleanup(server.Stop)
	serverAddress := server.Address
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

	select {
	case <-builtB:
		t.Fatal("Replacement provider was built before the Cluster security configuration update")
	default:
	}
	select {
	case <-providerB.entered:
		t.Fatal("Replacement provider KeyMaterial ran before the Cluster security configuration update")
	default:
	}

	resources.Clusters[0] = calPerfectClientTLSCluster(t, clusterName, serviceName, instanceB)
	if err := mgmtServer.Update(ctx, resources); err != nil {
		t.Fatalf("Failed to update management server with the replacement Cluster: %v", err)
	}
	calPerfectWaitForChan(ctx, t, replacementApplied, "timed out waiting for replacement Cluster configuration to be applied")

	releaseA()
	select {
	case err := <-rpcErr:
		if err != nil {
			t.Fatalf("Active RPC failed after the Cluster security configuration was replaced: %v", err)
		}
	case <-ctx.Done():
		t.Fatalf("Timed out waiting for the active RPC to finish: %v", ctx.Err())
	}

	calPerfectWaitForChan(ctx, t, providerA.closed, "timed out waiting for replaced provider A to close")

	select {
	case <-providerB.entered:
		t.Fatal("Active handshake switched from its selected provider A to provider B")
	default:
	}
	secondServer := stubserver.StartTestService(t, nil, grpc.Creds(tlsServerCreds(t)))
	t.Cleanup(secondServer.Stop)
	resources.Endpoints[0] = e2e.DefaultEndpoint(serviceName, "localhost", []uint32{testutils.ParsePort(t, secondServer.Address)})
	if err := mgmtServer.Update(ctx, resources); err != nil {
		t.Fatalf("Failed to update management server with a new endpoint after the replacement: %v", err)
	}
	calPerfectWaitForChan(ctx, t, providerB.entered, "timed out waiting for replacement provider KeyMaterial")
	pr := &peer.Peer{}
	if _, err := client.EmptyCall(ctx, &testpb.Empty{}, grpc.WaitForReady(true), grpc.Peer(pr)); err != nil {
		t.Fatalf("Follow-up RPC failed after the replacement provider was used: %v", err)
	}
	if pr.Addr.String() != secondServer.Address {
		t.Fatalf("Follow-up RPC used %s, want replacement backend %s", pr.Addr, secondServer.Address)
	}
}
