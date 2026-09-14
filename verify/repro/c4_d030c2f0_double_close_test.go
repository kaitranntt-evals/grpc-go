//go:build ignore

// Run: copy this file (without the go:build line) into a checkout of evalon/grpc-go-xd-d030c2f0 as internal/xds/balancer/clusterimpl/c4_d030c2f0_double_close_test.go,
// then run: go test ./internal/xds/balancer/clusterimpl -run 'Test/Verify_C4' -count=1 -v
package clusterimpl

import (
	"context"
	"fmt"
	"testing"

	"google.golang.org/grpc/balancer"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/credentials/tls/certprovider"
	xdscreds "google.golang.org/grpc/credentials/xds"
	xdscredsinternal "google.golang.org/grpc/internal/credentials/xds"
	"google.golang.org/grpc/internal/testutils"
	"google.golang.org/grpc/internal/xds/testutils/fakeclient"
)

// verifyC4Setup builds a clusterimpl balancer with a close-tracking root
// provider and returns the balancer, the provider and the published
// HandshakeInfo pointer. hiPtr is what the transport reads.
func verifyC4Setup(t *testing.T) (balancer.Balancer, *closeTrackingProvider, *xdscredsinternal.HandshakeInfo, func()) {
	prov := newCloseTrackingProvider()
	close(prov.unblockCh) // KeyMaterial never blocks in this test
	origBuildProvider := buildProvider
	buildProvider = func(_ map[string]*certprovider.BuildableConfig, instanceName, _ string, _, _ bool) (certprovider.Provider, error) {
		if instanceName != "root1" {
			return nil, fmt.Errorf("unknown provider instance %q", instanceName)
		}
		return prov, nil
	}
	restore := func() { buildProvider = origBuildProvider }

	xdsCreds, err := xdscreds.NewClientCredentials(xdscreds.ClientOptions{FallbackCreds: insecure.NewCredentials()})
	if err != nil {
		t.Fatalf("Failed to create xDS credentials: %v", err)
	}
	cc := testutils.NewBalancerClientConn(t)
	b := balancer.Get(Name).Build(cc, balancer.BuildOptions{DialCreds: xdsCreds})
	xdsC := fakeclient.NewClient()
	if err := b.UpdateClientConnState(securityConfigClientConnState(xdsC, "root1")); err != nil {
		t.Fatalf("unexpected error from UpdateClientConnState: %v", err)
	}
	<-cc.NewSubConnCh
	addrs := <-cc.NewSubConnAddrsCh
	hiPtr := xdscredsinternal.HandshakeInfoFromAttributes(addrs[0].Attributes)
	if hiPtr == nil {
		t.Fatal("SubConn address does not carry a HandshakeInfo")
	}
	return b, prov, hiPtr.Load(), restore
}

// Control: a single Close while a handshake holds a reference must NOT close
// the provider; it is closed only when the handshake releases.
func (s) TestVerify_C4_SingleClose_Control(t *testing.T) {
	b, prov, hi, restore := verifyC4Setup(t)
	defer restore()
	if !hi.Acquire() { // in-flight handshake
		t.Fatal("Acquire failed")
	}
	b.Close()
	t.Logf("after 1x Close with 1 in-flight handshake: provider closed = %v (want false)", prov.isClosed())
	if prov.isClosed() {
		t.Fatal("provider closed under an in-flight handshake after a single Close")
	}
	hi.Release()
	t.Logf("after handshake Release: provider closed = %v (want true)", prov.isClosed())
	if !prov.isClosed() {
		t.Fatal("provider not closed after last reference dropped")
	}
}

// C4: repeated Close on the same balancer releases the balancer's single
// owner reference more than once, so the in-flight handshake's reference is
// consumed and the provider is closed underneath the handshake.
func (s) TestVerify_C4_DoubleClose_ReleasesOwnerTwice(t *testing.T) {
	b, prov, hi, restore := verifyC4Setup(t)
	defer restore()
	if !hi.Acquire() { // in-flight handshake holds one reference (refs: 1 owner + 1 handshake)
		t.Fatal("Acquire failed")
	}
	b.Close()
	t.Logf("after 1st Close: provider closed = %v", prov.isClosed())
	b.Close()
	t.Logf("after 2nd Close (same instance, handshake still active): provider closed = %v", prov.isClosed())
	if prov.isClosed() {
		// The handshake still holds its reference, yet the provider is closed:
		// the owner reference was released twice.
		ctx, cancel := context.WithTimeout(context.Background(), defaultTestTimeout)
		defer cancel()
		_, err := hi.ClientSideTLSConfig(ctx, "")
		t.Logf("in-flight handshake ClientSideTLSConfig() err = %v", err)
		hi.Release()
		t.Fatalf("C4 CONFIRMED: second Close released the owner reference again and closed the provider under an active handshake")
	}
	hi.Release()
	t.Logf("after handshake Release: provider closed = %v", prov.isClosed())
}
