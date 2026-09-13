// C4 repro: copy to internal/xds/balancer/clusterimpl/c4_fallback_leak_test.go on
// branch evalon/grpc-go-xd-f2d332f0 and run
//   go test ./internal/xds/balancer/clusterimpl -run 'Test/VerifyC4FallbackHandshakeLeaksReference$' -count=1 -v
// The cluster_impl balancer publishes the fallback HandshakeInfo (no security
// config in the Cluster); the production xDS client credentials perform N
// handshakes through it (delegating to the insecure fallback); after the
// balancer releases its own reference the test counts how many references are
// still outstanding. Expected 0; the test fails if any leaked.
// Remove the go:build ignore line below after copying the file into the target package.

//go:build ignore

package clusterimpl

import (
	"context"
	"net"
	"testing"

	"google.golang.org/grpc/balancer"
	"google.golang.org/grpc/balancer/roundrobin"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
	xdscreds "google.golang.org/grpc/credentials/xds"
	icredentials "google.golang.org/grpc/internal/credentials"
	xdscredsinternal "google.golang.org/grpc/internal/credentials/xds"
	internalserviceconfig "google.golang.org/grpc/internal/serviceconfig"
	"google.golang.org/grpc/internal/testutils"
	"google.golang.org/grpc/internal/xds/testutils/fakeclient"
	"google.golang.org/grpc/internal/xds/xdsclient"
	"google.golang.org/grpc/internal/xds/xdsclient/xdsresource"
	"google.golang.org/grpc/resolver"
)

func (s) TestVerifyC4FallbackHandshakeLeaksReference(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), defaultTestTimeout)
	defer cancel()

	builder := balancer.Get(Name)
	cc := testutils.NewBalancerClientConn(t)
	b := builder.Build(cc, balancer.BuildOptions{DialCreds: xdsCredsForTesting{insecure.NewCredentials()}})

	// Cluster without security configuration: the balancer publishes a
	// HandshakeInfo for which UseFallbackCreds() is true.
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
						SecurityCfg:    nil,
					},
					EndpointConfig: &xdsresource.EndpointConfig{EDSUpdate: &xdsresource.EndpointsUpdate{}},
				},
			},
		},
	})
	if err := b.UpdateClientConnState(balancer.ClientConnState{
		ResolverState:  state,
		BalancerConfig: &LBConfig{Cluster: testClusterName, ChildPolicy: &internalserviceconfig.BalancerConfig{Name: roundrobin.Name}},
	}); err != nil {
		t.Fatalf("UpdateClientConnState failed: %v", err)
	}
	var addrs []resolver.Address
	select {
	case addrs = <-cc.NewSubConnAddrsCh:
	case <-ctx.Done():
		t.Fatal("Timeout waiting for a SubConn to be created")
	}
	hiPtr := xdscredsinternal.HandshakeInfoFromAttributes(addrs[0].Attributes)
	if hiPtr == nil {
		t.Fatal("SubConn address attributes do not contain a HandshakeInfo")
	}
	hi := hiPtr.Load()
	if !hi.UseFallbackCreds() {
		t.Fatal("published HandshakeInfo does not use fallback creds, want fallback")
	}

	// Production xDS client credentials, as a user would configure them.
	creds, err := xdscreds.NewClientCredentials(xdscreds.ClientOptions{FallbackCreds: insecure.NewCredentials()})
	if err != nil {
		t.Fatalf("NewClientCredentials failed: %v", err)
	}
	hsCtx := icredentials.NewClientHandshakeInfoContext(ctx, credentials.ClientHandshakeInfo{Attributes: addrs[0].Attributes})
	const handshakes = 3
	for i := 0; i < handshakes; i++ {
		c1, c2 := net.Pipe()
		if _, _, err := creds.ClientHandshake(hsCtx, "test.server", c1); err != nil {
			t.Fatalf("ClientHandshake #%d via fallback failed: %v", i+1, err)
		}
		c1.Close()
		c2.Close()
	}

	// The balancer drops its own (only legitimate) reference.
	b.Close()

	// With a balanced count the retired HandshakeInfo cannot be acquired and a
	// handshake after Close fails; with leaked references it still succeeds.
	c1, c2 := net.Pipe()
	_, _, postCloseErr := creds.ClientHandshake(hsCtx, "test.server", c1)
	c1.Close()
	c2.Close()
	t.Logf("ClientHandshake after balancer Close: err=%v", postCloseErr)

	// Count references still outstanding: each successful Acquire+2 Releases
	// removes one leaked reference; Acquire fails once the count is zero.
	leaked := 0
	for hi.Acquire() {
		hi.Release()
		hi.Release()
		leaked++
		if leaked > handshakes+1 {
			break
		}
	}
	t.Logf("handshakes via fallback = %d, references still outstanding after balancer Close = %d", handshakes, leaked)
	if leaked != 0 {
		t.Fatalf("fallback handshakes leaked %d HandshakeInfo reference(s), want 0", leaked)
	}
}
