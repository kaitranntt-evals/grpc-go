// Run: cp verify/repro/c1_root_provider_leak_test.go internal/xds/balancer/clusterimpl/zz_verify_c1_test.go && go test ./internal/xds/balancer/clusterimpl -run 'Test/VerifyC1_' -count=1 -v
//
// Audit repro for C1: a Cluster security update whose root provider builds
// successfully but whose identity provider fails to build must not leak the
// freshly built root provider. This test drives the production
// clusterImplBalancer.UpdateClientConnState path with buildProvider overridden
// so the root build succeeds (returning a close-tracking provider) and the
// identity build fails, then reports whether the root provider was closed
// before UpdateClientConnState returned, and whether balancer Close() closes it.

package clusterimpl

import (
	"context"
	"errors"
	"sync/atomic"
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

type verifyC1Provider struct {
	closed atomic.Int32
}

func (p *verifyC1Provider) KeyMaterial(context.Context) (*certprovider.KeyMaterial, error) {
	return &certprovider.KeyMaterial{}, nil
}

func (p *verifyC1Provider) Close() { p.closed.Add(1) }

func (s) TestVerifyC1_IdentityBuildFailureLeaksRootProvider(t *testing.T) {
	var root *verifyC1Provider
	var identityBuilds int
	origBuildProvider := buildProvider
	buildProvider = func(_ map[string]*certprovider.BuildableConfig, instanceName, _ string, wantIdentity, wantRoot bool) (certprovider.Provider, error) {
		switch {
		case wantRoot && !wantIdentity:
			root = &verifyC1Provider{}
			return root, nil
		case wantIdentity:
			identityBuilds++
			return nil, errors.New("verify-c1: injected identity provider build failure")
		}
		return nil, errors.New("verify-c1: unexpected buildProvider call for " + instanceName)
	}
	defer func() { buildProvider = origBuildProvider }()

	xdsCreds, err := xdscreds.NewClientCredentials(xdscreds.ClientOptions{FallbackCreds: insecure.NewCredentials()})
	if err != nil {
		t.Fatalf("Failed to create xDS credentials: %v", err)
	}
	cc := testutils.NewBalancerClientConn(t)
	b := balancer.Get(Name).Build(cc, balancer.BuildOptions{DialCreds: xdsCreds})

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
							RootCertName:         "roots",
							IdentityInstanceName: "identity-instance",
							IdentityCertName:     "identity",
						},
					},
					EndpointConfig: &xdsresource.EndpointConfig{EDSUpdate: &xdsresource.EndpointsUpdate{}},
				},
			},
		},
	})
	ccs := balancer.ClientConnState{
		ResolverState: state,
		BalancerConfig: &LBConfig{
			Cluster:     testClusterName,
			ChildPolicy: &internalserviceconfig.BalancerConfig{Name: roundrobin.Name},
		},
	}

	err = b.UpdateClientConnState(ccs)
	t.Logf("UpdateClientConnState() returned: %v", err)
	if err == nil {
		t.Fatal("UpdateClientConnState() succeeded; expected the injected identity provider build failure to propagate")
	}
	if root == nil {
		t.Fatal("root provider was never built")
	}
	if identityBuilds != 1 {
		t.Fatalf("identity provider build attempts = %d, want 1", identityBuilds)
	}
	closedAfterUpdate := root.closed.Load()
	t.Logf("root provider Close() calls after failed UpdateClientConnState(): %d", closedAfterUpdate)

	b.Close()
	closedAfterBalancerClose := root.closed.Load()
	t.Logf("root provider Close() calls after balancer Close(): %d", closedAfterBalancerClose)

	if closedAfterUpdate == 0 {
		t.Errorf("LEAK: root provider built by the failed security update was not closed before UpdateClientConnState() returned (Close() calls=%d); after balancer Close() calls=%d", closedAfterUpdate, closedAfterBalancerClose)
	}
}
