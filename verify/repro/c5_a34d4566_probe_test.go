//go:build verify_repro

/*
 * Verify probe for claim C5 on branch evalon/grpc-go-xd-a34d4566.
 * Run: go test ./credentials/xds/ -run TestVerifyC5 -count=1 -v
 *
 * clusterImplBalancer.Close() publishes an empty HandshakeInfo (both
 * providers nil) to the shared pointer embedded in address attributes
 * (clusterimpl.go:508). This probe simulates a handshake started through such
 * a stale address after balancer shutdown and observes whether the client
 * invokes the fallback credentials instead of failing closed.
 */

package xds

import (
	"context"
	"net"
	"sync/atomic"
	"testing"

	"google.golang.org/grpc/credentials"
	icredentials "google.golang.org/grpc/internal/credentials"
	xdsinternal "google.golang.org/grpc/internal/credentials/xds"
	"google.golang.org/grpc/internal/xds/matcher"
	"google.golang.org/grpc/resolver"
)

type countingFallbackCreds struct {
	credentials.TransportCredentials
	handshakes atomic.Int32
}

func (c *countingFallbackCreds) ClientHandshake(ctx context.Context, authority string, conn net.Conn) (net.Conn, credentials.AuthInfo, error) {
	c.handshakes.Add(1)
	return c.TransportCredentials.ClientHandshake(ctx, authority, conn)
}

func TestVerifyC5_StaleAddressInvokesFallback(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), defaultTestTimeout)
	defer cancel()
	ts := newTestServerWithHandshakeFunc(ctx, testServerTLSHandshake)
	defer ts.stop()

	fallback := &countingFallbackCreds{TransportCredentials: makeFallbackClientCreds(t)}
	creds, err := NewClientCredentials(ClientOptions{FallbackCreds: fallback})
	if err != nil {
		t.Fatalf("NewClientCredentials() failed: %v", err)
	}

	// The address attributes hold the shared pointer, exactly as published by
	// the cluster_impl balancer while it was alive.
	sms := []matcher.StringMatcher{matcher.NewExactStringMatcher(defaultTestCertSAN, false)}
	live := xdsinternal.NewHandshakeInfo(makeRootProvider(t, "x509/server_ca_cert.pem"), nil, sms, false, "", false, false)
	var hiPtr atomic.Pointer[xdsinternal.HandshakeInfo]
	hiPtr.Store(live)
	addr := xdsinternal.SetHandshakeInfo(resolver.Address{}, &hiPtr)
	hctx := icredentials.NewClientHandshakeInfoContext(ctx, credentials.ClientHandshakeInfo{Attributes: addr.Attributes})

	// clusterImplBalancer.Close(): publishes an empty HandshakeInfo to the
	// same shared pointer (b.setHandshakeInfo(xds.NewHandshakeInfo(nil, nil,
	// nil, false, "", false, false)) at clusterimpl.go:508).
	hiPtr.Store(xdsinternal.NewHandshakeInfo(nil, nil, nil, false, "", false, false))

	conn, err := net.Dial("tcp", ts.address)
	if err != nil {
		t.Fatalf("net.Dial(%s) failed: %v", ts.address, err)
	}
	defer conn.Close()

	_, _, err = creds.ClientHandshake(hctx, authority, conn)
	if got := fallback.handshakes.Load(); got > 0 {
		t.Logf("CONFIRMED: fallback credentials were invoked %d time(s) through the stale post-shutdown address (handshake err=%v)", got, err)
	} else {
		t.Fatalf("REFUTED: fallback credentials were not invoked; ClientHandshake() = %v", err)
	}
}
