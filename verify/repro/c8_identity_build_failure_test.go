// Run: cp verify/repro/c8_identity_build_failure_test.go internal/xds/balancer/clusterimpl/ && go test ./internal/xds/balancer/clusterimpl/ -run 'Test/VerifyC8' -count=1 -v   (on branch evalon/grpc-go-xd-a0c92a72)

package clusterimpl

import (
	"context"
	"errors"
	"testing"

	"google.golang.org/grpc/balancer"
	"google.golang.org/grpc/balancer/roundrobin"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/credentials/tls/certprovider"
	internalserviceconfig "google.golang.org/grpc/internal/serviceconfig"
	"google.golang.org/grpc/internal/testutils"
	"google.golang.org/grpc/internal/xds/testutils/fakeclient"
	"google.golang.org/grpc/internal/xds/xdsclient"
	"google.golang.org/grpc/internal/xds/xdsclient/xdsresource"
	"google.golang.org/grpc/resolver"
)

type c8XDSCreds struct{ credentials.TransportCredentials }

func (c8XDSCreds) UsesXDS() bool { return true }

type c8Provider struct{ closed bool }

func (p *c8Provider) KeyMaterial(context.Context) (*certprovider.KeyMaterial, error) {
	return &certprovider.KeyMaterial{}, nil
}
func (p *c8Provider) Close() { p.closed = true }

// Root-provider construction succeeds, identity-provider construction fails.
func (s) TestVerifyC8_RootProviderLeakedOnIdentityBuildFailure(t *testing.T) {
	var root *c8Provider
	origBuildProvider := buildProvider
	buildProvider = func(_ map[string]*certprovider.BuildableConfig, _, _ string, wantIdentity, _ bool) (certprovider.Provider, error) {
		if wantIdentity {
			return nil, errors.New("injected identity provider build failure")
		}
		root = &c8Provider{}
		return root, nil
	}
	defer func() { buildProvider = origBuildProvider }()

	cc := testutils.NewBalancerClientConn(t)
	b := balancer.Get(Name).Build(cc, balancer.BuildOptions{DialCreds: c8XDSCreds{insecure.NewCredentials()}})
	xdsC := fakeclient.NewClient()
	state := xdsclient.SetClient(resolver.State{Endpoints: testBackendEndpoints}, xdsC)
	state = xdsresource.SetXDSConfig(state, &xdsresource.XDSConfig{
		Clusters: map[string]*xdsresource.ClusterResult{
			testClusterName: {Config: xdsresource.ClusterConfig{
				Cluster: &xdsresource.ClusterUpdate{
					ClusterType: xdsresource.ClusterTypeEDS, ClusterName: testClusterName, EDSServiceName: testServiceName,
					SecurityCfg: &xdsresource.SecurityConfig{
						RootInstanceName: "root", RootCertName: "c", IdentityInstanceName: "identity", IdentityCertName: "c",
					},
				},
				EndpointConfig: &xdsresource.EndpointConfig{EDSUpdate: &xdsresource.EndpointsUpdate{}},
			}},
		},
	})
	err := b.UpdateClientConnState(balancer.ClientConnState{
		ResolverState:  state,
		BalancerConfig: &LBConfig{Cluster: testClusterName, ChildPolicy: &internalserviceconfig.BalancerConfig{Name: roundrobin.Name}},
	})
	t.Logf("UpdateClientConnState() error = %v", err)
	if root == nil {
		t.Fatal("root provider was never constructed")
	}
	t.Logf("root provider closed when the operation returned: %v", root.closed)
	b.Close()
	t.Logf("root provider closed after balancer Close(): %v", root.closed)
	if !root.closed {
		t.Fatal("root provider built for the failed update was never released")
	}
}
