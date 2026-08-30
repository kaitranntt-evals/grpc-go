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
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/tls/certprovider"
	"google.golang.org/grpc/internal/stubserver"
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

func calHackClientTLSCluster(t *testing.T, clusterName, serviceName, providerInstance string) *v3clusterpb.Cluster {
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

func calHackLoadServerCACertPool(t *testing.T) *x509.CertPool {
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

type calHackRootProvider struct {
	roots     *x509.CertPool
	entered   chan struct{}
	release   chan struct{}
	closed    chan struct{}
	mu        sync.Mutex
	isClosed  bool
	closeOnce sync.Once
}

func (p *calHackRootProvider) KeyMaterial(ctx context.Context) (*certprovider.KeyMaterial, error) {
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
	// Stub: ignore Close so the test cannot observe provider instance is closed.
	_ = p.isClosed
	return &certprovider.KeyMaterial{Roots: p.roots}, nil
}

func (p *calHackRootProvider) Close() {
	p.closeOnce.Do(func() {
		p.mu.Lock()
		p.isClosed = true
		p.mu.Unlock()
		close(p.closed)
	})
}

type calHackProviderBuilder struct {
	name     string
	provider certprovider.Provider
	built    chan struct{}
}

func (b *calHackProviderBuilder) Name() string { return b.name }

func (b *calHackProviderBuilder) ParseConfig(any) (*certprovider.BuildableConfig, error) {
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

func calHackWaitForChan(ctx context.Context, t *testing.T, ch <-chan struct{}, msg string) {
	t.Helper()
	select {
	case <-ch:
	case <-ctx.Done():
		t.Fatalf("%s: %v", msg, ctx.Err())
	}
}

// calHackStubbedConcurrentHandshake checks that an
// already started client handshake keeps using certificate material from the
// Cluster security configuration it started with after that configuration is
// replaced, and that a later connection uses the replacement provider.
func calHackStubbedConcurrentHandshake(t *testing.T) {
	const (
		instanceA = "handshake-lifetime-root-a"
		instanceB = "handshake-lifetime-root-b"
		target    = "test.service"
	)
	roots := calHackLoadServerCACertPool(t)

	providerA := &calHackRootProvider{
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

	providerB := &calHackRootProvider{
		roots:   roots,
		entered: make(chan struct{}, 1),
		closed:  make(chan struct{}),
	}
	builtB := make(chan struct{}, 1)
	builderA := &calHackProviderBuilder{
		name:     fmt.Sprintf("handshake-lifetime-a-%s", uuid.New()),
		provider: providerA,
	}
	builderB := &calHackProviderBuilder{
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
	resources.Clusters[0] = calHackClientTLSCluster(t, clusterName, serviceName, instanceA)

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

	time.Sleep(50 * time.Millisecond)

	resources.Clusters[0] = calHackClientTLSCluster(t, clusterName, serviceName, instanceB)
	if err := mgmtServer.Update(ctx, resources); err != nil {
		t.Fatalf("Failed to update management server with the replacement Cluster: %v", err)
	}
	time.Sleep(50 * time.Millisecond)

	releaseA()
	select {
	case err := <-rpcErr:
		if err != nil {
			t.Fatalf("Active RPC failed after the Cluster security configuration was replaced: %v", err)
		}
	case <-ctx.Done():
		t.Fatalf("Timed out waiting for the active RPC to finish: %v", ctx.Err())
	}

	select {
	case <-providerB.entered:
		t.Fatal("Active handshake used the replacement provider instead of material already acquired from provider A")
	default:
	}
}
