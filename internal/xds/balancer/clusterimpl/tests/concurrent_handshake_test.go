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
	"time"

	"github.com/google/uuid"
	"google.golang.org/grpc/credentials/tls/certprovider"
	"google.golang.org/grpc/internal/testutils"
	"google.golang.org/grpc/internal/testutils/xds/e2e"
	"google.golang.org/grpc/internal/xds/bootstrap"
	"google.golang.org/grpc/testdata"

	v3clusterpb "github.com/envoyproxy/go-control-plane/envoy/config/cluster/v3"
	v3corepb "github.com/envoyproxy/go-control-plane/envoy/config/core/v3"
	v3tlspb "github.com/envoyproxy/go-control-plane/envoy/extensions/transport_sockets/tls/v3"
	testgrpc "google.golang.org/grpc/interop/grpc_testing"
	testpb "google.golang.org/grpc/interop/grpc_testing"
)

func calMinClientTLSCluster(t *testing.T, clusterName, serviceName, providerInstance string) *v3clusterpb.Cluster {
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

func calMinLoadServerCACertPool(t *testing.T) *x509.CertPool {
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

type calMinRootProvider struct {
	roots     *x509.CertPool
	entered   bool
	release   chan struct{}
	closed    chan struct{}
	mu        sync.Mutex
	isClosed  bool
	closeOnce sync.Once
}

func (p *calMinRootProvider) KeyMaterial(ctx context.Context) (*certprovider.KeyMaterial, error) {
	p.mu.Lock()
	p.entered = true
	p.mu.Unlock()
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

func (p *calMinRootProvider) hasEntered() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.entered
}

func (p *calMinRootProvider) Close() {
	p.closeOnce.Do(func() {
		p.mu.Lock()
		p.isClosed = true
		p.mu.Unlock()
		close(p.closed)
	})
}

type calMinProviderBuilder struct {
	name     string
	provider certprovider.Provider
	built    chan struct{}
}

func (b *calMinProviderBuilder) Name() string { return b.name }

func (b *calMinProviderBuilder) ParseConfig(any) (*certprovider.BuildableConfig, error) {
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

// TestSecurityConfigUpdate_ConcurrentHandshake checks that a client handshake
// that started loading validation roots keeps its selected Cluster security
// configuration usable after replacement.
func TestSecurityConfigUpdate_ConcurrentHandshake(t *testing.T) {
	const (
		instanceA = "handshake-lifetime-root-a"
		instanceB = "handshake-lifetime-root-b"
		target    = "test.service"
	)
	roots := calMinLoadServerCACertPool(t)

	providerA := &calMinRootProvider{
		roots:   roots,
		release: make(chan struct{}),
		closed:  make(chan struct{}),
	}
	var releaseOnce sync.Once
	releaseA := func() {
		releaseOnce.Do(func() { close(providerA.release) })
	}
	t.Cleanup(releaseA)

	providerB := &calMinRootProvider{
		roots:  roots,
		closed: make(chan struct{}),
	}
	builtB := make(chan struct{}, 1)
	builderA := &calMinProviderBuilder{
		name:     fmt.Sprintf("handshake-lifetime-a-%s", uuid.New()),
		provider: providerA,
	}
	builderB := &calMinProviderBuilder{
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

	cc, serverAddress := setupForSecurityTests(t, bootstrapContents, xdsClientCredsWithInsecureFallback(t), tlsServerCreds(t))
	resources := e2e.DefaultClientResources(e2e.ResourceParams{
		DialTarget: target,
		NodeID:     nodeID,
		Host:       "localhost",
		Port:       testutils.ParsePort(t, serverAddress),
		SecLevel:   e2e.SecurityLevelNone,
	})
	clusterName := resources.Clusters[0].Name
	serviceName := resources.Endpoints[0].ClusterName
	resources.Clusters[0] = calMinClientTLSCluster(t, clusterName, serviceName, instanceA)

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

	for i := 0; i < 1000 && !providerA.hasEntered(); i++ {
		time.Sleep(time.Millisecond)
	}
	if !providerA.hasEntered() {
		t.Fatal("Provider A did not start loading validation roots")
	}

	resources.Clusters[0] = calMinClientTLSCluster(t, clusterName, serviceName, instanceB)
	if err := mgmtServer.Update(ctx, resources); err != nil {
		t.Fatalf("Failed to update management server with the replacement Cluster: %v", err)
	}
	replacementApplied := false
	for i := 0; i < 1000 && !replacementApplied; i++ {
		select {
		case <-builtB:
			replacementApplied = true
		default:
			time.Sleep(time.Millisecond)
		}
	}
	if !replacementApplied {
		t.Fatal("Replacement Cluster security configuration was not applied")
	}

	releaseA()
	select {
	case err := <-rpcErr:
		if err != nil {
			t.Fatalf("Active RPC failed after the Cluster security configuration was replaced: %v", err)
		}
	case <-ctx.Done():
		t.Fatalf("Timed out waiting for the active RPC to finish: %v", ctx.Err())
	}

	if providerB.hasEntered() {
		t.Fatal("Active handshake switched from its selected provider A to provider B")
	}
}
