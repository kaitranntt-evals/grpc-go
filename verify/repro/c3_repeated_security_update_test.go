// Run: cp verify/repro/c3_repeated_security_update_test.go internal/xds/balancer/clusterimpl/ && go test ./internal/xds/balancer/clusterimpl/ -run 'Test/VerifyC3' -count=1 -v   (on branch evalon/grpc-go-xd-1f4e4c50)

package clusterimpl

import (
	"testing"

	"google.golang.org/grpc/balancer"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/credentials/tls/certprovider"
	"google.golang.org/grpc/internal/testutils"
	"google.golang.org/grpc/internal/xds/testutils/fakeclient"
	"google.golang.org/grpc/internal/xds/xdsclient/xdsresource"
	"google.golang.org/grpc/resolver"
)

// Applies the same security configuration three times (initial, repeated
// Cluster update, endpoint-only update) and counts provider constructions.
func (s) TestVerifyC3_UnchangedSecurityConfigReacquiresProviders(t *testing.T) {
	var built []*testCertProvider
	origBuildProvider := buildProvider
	buildProvider = func(map[string]*certprovider.BuildableConfig, string, string, bool, bool) (certprovider.Provider, error) {
		p := newTestCertProvider()
		built = append(built, p)
		return p, nil
	}
	defer func() { buildProvider = origBuildProvider }()

	cc := testutils.NewBalancerClientConn(t)
	b := balancer.Get(Name).Build(cc, balancer.BuildOptions{DialCreds: xdsCredsForTest{insecure.NewCredentials()}})
	defer b.Close()
	xdsC := fakeclient.NewClient()
	secCfg := func() *xdsresource.SecurityConfig {
		return &xdsresource.SecurityConfig{RootInstanceName: "root-instance", RootCertName: "cert-1"}
	}

	if err := b.UpdateClientConnState(clientConnStateWithSecurityConfig(xdsC, secCfg())); err != nil {
		t.Fatal(err)
	}
	t.Logf("after initial update: providers built = %d", len(built))

	// Repeated update with an equal security configuration.
	if err := b.UpdateClientConnState(clientConnStateWithSecurityConfig(xdsC, secCfg())); err != nil {
		t.Fatal(err)
	}
	t.Logf("after repeated identical update: providers built = %d", len(built))

	// Endpoint-only update: same security configuration, different endpoints.
	st := clientConnStateWithSecurityConfig(xdsC, secCfg())
	st.ResolverState.Endpoints = []resolver.Endpoint{{Addresses: []resolver.Address{{Addr: "2.2.2.2:2"}}}}
	if err := b.UpdateClientConnState(st); err != nil {
		t.Fatal(err)
	}
	t.Logf("after endpoint-only update: providers built = %d", len(built))
	for i, p := range built {
		t.Logf("provider[%d] closed=%v", i, p.isClosed())
	}
	if len(built) != 1 {
		t.Fatalf("root provider constructed %d times for an unchanged security configuration, want 1 (unchanged updates reacquire providers)", len(built))
	}
}
