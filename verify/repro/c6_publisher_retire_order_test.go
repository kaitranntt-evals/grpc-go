// Run (on branch 53e1cbd8): cp verify/repro/c6_publisher_retire_order_test.go internal/xds/balancer/clusterimpl/zz_verify_c6b_test.go && go test ./internal/xds/balancer/clusterimpl -run 'Test/VerifyC6_' -count=1 -v
//
// C6 "persistent trigger" probe against the production publisher: drives clusterImplBalancer
// through two security-config updates and Close(), and after each step checks whether the
// HandshakeInfo that was just retired (Close()d) is still the one published in b.xdsHIPtr.

package clusterimpl

import (
	"context"
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

type verifyC6Provider struct{ closed atomic.Int32 }

func (p *verifyC6Provider) KeyMaterial(context.Context) (*certprovider.KeyMaterial, error) {
	return &certprovider.KeyMaterial{}, nil
}
func (p *verifyC6Provider) Close() { p.closed.Add(1) }

func (s) TestVerifyC6_PublisherNeverLeavesRetiredSnapshotPublished(t *testing.T) {
	origBuildProvider := buildProvider
	buildProvider = func(_ map[string]*certprovider.BuildableConfig, _, _ string, _, _ bool) (certprovider.Provider, error) {
		return &verifyC6Provider{}, nil
	}
	defer func() { buildProvider = origBuildProvider }()

	xdsCreds, err := xdscreds.NewClientCredentials(xdscreds.ClientOptions{FallbackCreds: insecure.NewCredentials()})
	if err != nil {
		t.Fatalf("Failed to create xDS credentials: %v", err)
	}
	cc := testutils.NewBalancerClientConn(t)
	b := balancer.Get(Name).Build(cc, balancer.BuildOptions{DialCreds: xdsCreds}).(*clusterImplBalancer)
	xdsC := fakeclient.NewClient()

	ccsFor := func(rootCert string) balancer.ClientConnState {
		state := xdsclient.SetClient(resolver.State{Endpoints: testBackendEndpoints}, xdsC)
		state = xdsresource.SetXDSConfig(state, &xdsresource.XDSConfig{
			Clusters: map[string]*xdsresource.ClusterResult{
				testClusterName: {
					Config: xdsresource.ClusterConfig{
						Cluster: &xdsresource.ClusterUpdate{
							ClusterType:    xdsresource.ClusterTypeEDS,
							ClusterName:    testClusterName,
							EDSServiceName: testServiceName,
							SecurityCfg:    &xdsresource.SecurityConfig{RootInstanceName: "root-instance", RootCertName: rootCert},
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

	if err := b.UpdateClientConnState(ccsFor("roots-1")); err != nil {
		t.Fatalf("UpdateClientConnState(1) failed: %v", err)
	}
	hi1 := b.xdsHIPtr.Load()
	acq := hi1.Acquire()
	t.Logf("after update 1: published hi1 Acquire()=%v", acq)
	if acq {
		hi1.Release()
	}

	if err := b.UpdateClientConnState(ccsFor("roots-2")); err != nil {
		t.Fatalf("UpdateClientConnState(2) failed: %v", err)
	}
	hi2 := b.xdsHIPtr.Load()
	t.Logf("after update 2: published==hi1 is %v; hi1.Acquire()=%v (false => retired); published hi2 Acquire()=%v", hi2 == hi1, hi1.Acquire(), hi2.Acquire())
	if hi2 == hi1 {
		t.Errorf("retired snapshot hi1 is still published after a replacing security update")
	}

	b.Close()
	after := b.xdsHIPtr.Load()
	t.Logf("after balancer Close(): published==nil is %v; hi2.Acquire()=%v", after == nil, hi2.Acquire())
	if after == hi2 {
		t.Errorf("retired snapshot hi2 is still published after balancer Close()")
	}
}
