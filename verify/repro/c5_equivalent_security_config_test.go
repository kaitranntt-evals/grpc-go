//go:build ignore

// Run (branch evalon/grpc-go-xd-f55f758b): sed '/^\/\/go:build ignore$/d' verify/repro/c5_equivalent_security_config_test.go > internal/xds/balancer/clusterimpl/audit_c5_test.go && go test ./internal/xds/balancer/clusterimpl/ -run 'Test/AuditC5' -count=1 -v
//
// Audit repro for claim C5: applying a security configuration equivalent to
// the active one rebuilds provider handles and replaces the published
// HandshakeInfo instead of preserving the existing ownership state.

package clusterimpl

import (
	"context"
	"crypto/x509"
	"fmt"
	"sync"
	"testing"

	"google.golang.org/grpc/balancer"
	"google.golang.org/grpc/balancer/roundrobin"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/credentials/tls/certprovider"
	xdscreds "google.golang.org/grpc/credentials/xds"
	"google.golang.org/grpc/internal/credentials/xds"
	internalserviceconfig "google.golang.org/grpc/internal/serviceconfig"
	"google.golang.org/grpc/internal/testutils"
	"google.golang.org/grpc/internal/xds/testutils/fakeclient"
	"google.golang.org/grpc/internal/xds/xdsclient"
	"google.golang.org/grpc/internal/xds/xdsclient/xdsresource"
	"google.golang.org/grpc/resolver"
)

type auditC5Provider struct {
	id        int
	km        *certprovider.KeyMaterial
	closed    chan struct{}
	closeOnce sync.Once
}

func (p *auditC5Provider) KeyMaterial(context.Context) (*certprovider.KeyMaterial, error) {
	return p.km, nil
}
func (p *auditC5Provider) Close() { p.closeOnce.Do(func() { close(p.closed) }) }
func (p *auditC5Provider) isClosed() bool {
	select {
	case <-p.closed:
		return true
	default:
		return false
	}
}

func (s) TestAuditC5_EquivalentSecurityConfigRebuildsOwnership(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), defaultTestTimeout)
	defer cancel()

	var mu sync.Mutex
	var built []*auditC5Provider
	origBuildProvider := buildProvider
	buildProvider = func(_ map[string]*certprovider.BuildableConfig, instanceName, _ string, _, _ bool) (certprovider.Provider, error) {
		mu.Lock()
		defer mu.Unlock()
		p := &auditC5Provider{id: len(built) + 1, km: &certprovider.KeyMaterial{Roots: x509.NewCertPool()}, closed: make(chan struct{})}
		built = append(built, p)
		fmt.Printf("AUDIT buildProvider(%q) -> handle #%d\n", instanceName, p.id)
		return p, nil
	}
	defer func() { buildProvider = origBuildProvider }()

	creds, err := xdscreds.NewClientCredentials(xdscreds.ClientOptions{FallbackCreds: insecure.NewCredentials()})
	if err != nil {
		t.Fatalf("Failed to create xDS credentials: %v", err)
	}
	cc := testutils.NewBalancerClientConn(t)
	b := balancer.Get(Name).Build(cc, balancer.BuildOptions{DialCreds: creds})
	defer b.Close()

	xdsC := fakeclient.NewClient()
	ccState := func() balancer.ClientConnState {
		state := xdsclient.SetClient(resolver.State{Endpoints: testBackendEndpoints}, xdsC)
		state = xdsresource.SetXDSConfig(state, &xdsresource.XDSConfig{
			Clusters: map[string]*xdsresource.ClusterResult{
				testClusterName: {
					Config: xdsresource.ClusterConfig{
						Cluster: &xdsresource.ClusterUpdate{
							ClusterType:    xdsresource.ClusterTypeEDS,
							ClusterName:    testClusterName,
							EDSServiceName: testServiceName,
							SecurityCfg:    &xdsresource.SecurityConfig{RootInstanceName: "root-instance"},
						},
						EndpointConfig: &xdsresource.EndpointConfig{EDSUpdate: &xdsresource.EndpointsUpdate{}},
					},
				},
			},
		})
		return balancer.ClientConnState{
			ResolverState:  state,
			BalancerConfig: &LBConfig{Cluster: testClusterName, ChildPolicy: &internalserviceconfig.BalancerConfig{Name: roundrobin.Name}},
		}
	}

	// First (valid) security configuration.
	if err := b.UpdateClientConnState(ccState()); err != nil {
		t.Fatalf("UpdateClientConnState(#1) failed: %v", err)
	}
	var addrs []resolver.Address
	select {
	case addrs = <-cc.NewSubConnAddrsCh:
	case <-ctx.Done():
		t.Fatal("Timeout waiting for NewSubConn")
	}
	hiPtr := xds.HandshakeInfoFromAttributes(addrs[0].Attributes)
	hi1 := hiPtr.Load()
	mu.Lock()
	nBuilt1 := len(built)
	mu.Unlock()
	fmt.Printf("AUDIT after update #1: handles built=%d, HandshakeInfo=%p\n", nBuilt1, hi1)

	// Equivalent security configuration (identical SecurityConfig contents).
	cfg1 := ccState().ResolverState
	cfg2 := ccState().ResolverState
	sc1 := xdsresource.XDSConfigFromResolverState(cfg1).Clusters[testClusterName].Config.Cluster.SecurityCfg
	sc2 := xdsresource.XDSConfigFromResolverState(cfg2).Clusters[testClusterName].Config.Cluster.SecurityCfg
	fmt.Printf("AUDIT SecurityConfig.Equal(update #1, update #2) = %v\n", sc1.Equal(sc2))
	if err := b.UpdateClientConnState(ccState()); err != nil {
		t.Fatalf("UpdateClientConnState(#2) failed: %v", err)
	}
	hi2 := hiPtr.Load()
	mu.Lock()
	nBuilt2 := len(built)
	first := built[0]
	mu.Unlock()
	fmt.Printf("AUDIT after equivalent update #2: handles built=%d, HandshakeInfo=%p (same as #1: %v), handle #1 closed=%v\n", nBuilt2, hi2, hi2 == hi1, first.isClosed())

	if nBuilt2 == nBuilt1 && hi2 == hi1 && !first.isClosed() {
		t.Log("RESULT: equivalent update PRESERVED ownership state (claim C5 refuted)")
		return
	}
	t.Logf("RESULT: equivalent update REBUILT ownership state: %d new handle(s) acquired, snapshot replaced=%v, previous handle closed=%v (claim C5 confirmed)", nBuilt2-nBuilt1, hi2 != hi1, first.isClosed())
}
