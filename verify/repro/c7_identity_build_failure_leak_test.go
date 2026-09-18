//go:build ignore

// Run (branch evalon/grpc-go-xd-96fc0a0c): sed '/^\/\/go:build ignore$/d' verify/repro/c7_identity_build_failure_leak_test.go > internal/xds/balancer/clusterimpl/audit_c7_test.go && go test ./internal/xds/balancer/clusterimpl/ -run 'Test/AuditC7' -count=1 -v
//
// Audit repro for claim C7: when the identity provider fails to build after
// the root provider was successfully acquired, handleSecurityConfig returns
// the error without releasing the root provider handle.

package clusterimpl

import (
	"context"
	"crypto/x509"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"

	"google.golang.org/grpc/balancer"
	"google.golang.org/grpc/balancer/roundrobin"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/credentials/tls/certprovider"
	xdscreds "google.golang.org/grpc/credentials/xds"
	internalserviceconfig "google.golang.org/grpc/internal/serviceconfig"
	"google.golang.org/grpc/internal/testutils"
	"google.golang.org/grpc/internal/xds/testutils/fakeclient"
	"google.golang.org/grpc/internal/xds/xdsclient"
	"google.golang.org/grpc/internal/xds/xdsclient/xdsresource"
	"google.golang.org/grpc/resolver"
)

type auditC7Provider struct {
	name      string
	closed    chan struct{}
	closeOnce sync.Once
}

func (p *auditC7Provider) KeyMaterial(context.Context) (*certprovider.KeyMaterial, error) {
	return &certprovider.KeyMaterial{Roots: x509.NewCertPool()}, nil
}
func (p *auditC7Provider) Close() { p.closeOnce.Do(func() { close(p.closed) }) }
func (p *auditC7Provider) isClosed() bool {
	select {
	case <-p.closed:
		return true
	default:
		return false
	}
}

func (s) TestAuditC7_IdentityBuildFailureLeaksRootProvider(t *testing.T) {
	var mu sync.Mutex
	var built []*auditC7Provider
	errIdentity := errors.New("audit: injected identity provider construction failure")
	origBuildProvider := buildProvider
	buildProvider = func(_ map[string]*certprovider.BuildableConfig, instanceName, _ string, wantIdentity, wantRoot bool) (certprovider.Provider, error) {
		if wantIdentity {
			fmt.Printf("AUDIT buildProvider(%q, identity) -> injected error\n", instanceName)
			return nil, errIdentity
		}
		mu.Lock()
		defer mu.Unlock()
		p := &auditC7Provider{name: instanceName, closed: make(chan struct{})}
		built = append(built, p)
		fmt.Printf("AUDIT buildProvider(%q, root=%v) -> handle #%d acquired\n", instanceName, wantRoot, len(built))
		return p, nil
	}
	defer func() { buildProvider = origBuildProvider }()

	creds, err := xdscreds.NewClientCredentials(xdscreds.ClientOptions{FallbackCreds: insecure.NewCredentials()})
	if err != nil {
		t.Fatalf("Failed to create xDS credentials: %v", err)
	}
	cc := testutils.NewBalancerClientConn(t)
	b := balancer.Get(Name).Build(cc, balancer.BuildOptions{DialCreds: creds})

	xdsC := fakeclient.NewClient()
	state := xdsclient.SetClient(resolver.State{Endpoints: testBackendEndpoints}, xdsC)
	state = xdsresource.SetXDSConfig(state, &xdsresource.XDSConfig{
		Clusters: map[string]*xdsresource.ClusterResult{
			testClusterName: {
				Config: xdsresource.ClusterConfig{
					Cluster: &xdsresource.ClusterUpdate{
						ClusterType:    xdsresource.ClusterTypeEDS,
						ClusterName:    testClusterName,
						EDSServiceName: testServiceName,
						SecurityCfg: &xdsresource.SecurityConfig{
							RootInstanceName:     "root-instance",
							IdentityInstanceName: "identity-instance",
						},
					},
					EndpointConfig: &xdsresource.EndpointConfig{EDSUpdate: &xdsresource.EndpointsUpdate{}},
				},
			},
		},
	})
	err = b.UpdateClientConnState(balancer.ClientConnState{
		ResolverState:  state,
		BalancerConfig: &LBConfig{Cluster: testClusterName, ChildPolicy: &internalserviceconfig.BalancerConfig{Name: roundrobin.Name}},
	})
	fmt.Printf("AUDIT UpdateClientConnState error: %v\n", err)
	if err == nil || !strings.Contains(err.Error(), errIdentity.Error()) {
		t.Fatalf("UpdateClientConnState returned %v, want injected identity error", err)
	}

	mu.Lock()
	if len(built) != 1 {
		mu.Unlock()
		t.Fatalf("Expected exactly one root provider handle to be acquired, got %d", len(built))
	}
	root := built[0]
	mu.Unlock()
	fmt.Printf("AUDIT root provider %q closed after failed update: %v\n", root.name, root.isClosed())
	if root.isClosed() {
		t.Log("RESULT: root provider released on identity construction failure (claim C7 refuted)")
		b.Close()
		return
	}
	// Even tearing down the balancer does not reach the unpublished handle.
	b.Close()
	fmt.Printf("AUDIT root provider %q closed after balancer Close(): %v\n", root.name, root.isClosed())
	t.Logf("RESULT: root provider handle %q LEAKED (never closed) after identity provider construction failed (claim C7 confirmed)", root.name)
}
