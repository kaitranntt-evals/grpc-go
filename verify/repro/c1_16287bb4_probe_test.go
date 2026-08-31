//go:build verify_repro

/*
 * Verify probe for claim C1 on branch evalon/grpc-go-xd-16287bb4.
 * Run: go test ./credentials/xds/ -run TestVerifyC1 -count=1 -v
 *
 * Simulates the production interleaving in which the balancer (whose
 * handleSecurityConfig on this branch closes the old providers BEFORE storing
 * the replacement HandshakeInfo) releases its reference on the providers of
 * the currently published HandshakeInfo before a handshake retains them. The
 * retry loop in retainHandshakeInfo sees the same HandshakeInfo twice and
 * proceeds with the closed snapshot.
 */

package xds

import (
	"context"
	"net"
	"strings"
	"sync/atomic"
	"testing"

	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/tls/certprovider"
	icredentials "google.golang.org/grpc/internal/credentials"
	xdsinternal "google.golang.org/grpc/internal/credentials/xds"
	"google.golang.org/grpc/internal/xds/matcher"
	"google.golang.org/grpc/resolver"
)

func TestVerifyC1_FailedRetainUsesClosedSnapshot(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), defaultTestTimeout)
	defer cancel()
	ts := newTestServerWithHandshakeFunc(ctx, testServerTLSHandshake)
	defer ts.stop()

	opts := ClientOptions{FallbackCreds: makeFallbackClientCreds(t)}
	creds, err := NewClientCredentials(opts)
	if err != nil {
		t.Fatalf("NewClientCredentials(%v) failed: %v", opts, err)
	}

	conn, err := net.Dial("tcp", ts.address)
	if err != nil {
		t.Fatalf("net.Dial(%s) failed: %v", ts.address, err)
	}
	defer conn.Close()

	// Build the root provider through the production path so that it is a
	// retainable singleCloseWrappedProvider, with trusted validation roots.
	fake := makeRootProvider(t, "x509/server_ca_cert.pem")
	cfg := certprovider.NewBuildableConfig("verify-c1", nil, func(certprovider.BuildOptions) certprovider.Provider { return fake })
	prov, err := cfg.Build(certprovider.BuildOptions{})
	if err != nil {
		t.Fatalf("BuildableConfig.Build() failed: %v", err)
	}

	sms := []matcher.StringMatcher{matcher.NewExactStringMatcher(defaultTestCertSAN, false)}
	hi := xdsinternal.NewHandshakeInfo(prov, nil, sms, false, "", false, false)
	var hiPtr atomic.Pointer[xdsinternal.HandshakeInfo]
	hiPtr.Store(hi)
	addr := xdsinternal.SetHandshakeInfo(resolver.Address{}, &hiPtr)
	hctx := icredentials.NewClientHandshakeInfoContext(ctx, credentials.ClientHandshakeInfo{Attributes: addr.Attributes})

	// Cluster security update: the balancer releases its reference on the old
	// provider before the replacement HandshakeInfo is stored (base
	// handleSecurityConfig ordering, unchanged on this branch), so the pointer
	// still holds the old HandshakeInfo whose providers are fully released.
	prov.Close()

	if _, ok := certprovider.Retain(prov); ok {
		t.Fatal("Retain succeeded on a fully released provider; test premise broken")
	}
	t.Log("Retain() on the selected provider fails (snapshot cannot be retained)")

	_, _, err = creds.ClientHandshake(hctx, authority, conn)
	if err == nil {
		t.Fatal("REFUTED: ClientHandshake succeeded; handshake did not use the closed snapshot")
	}
	t.Logf("ClientHandshake() error: %v", err)
	if strings.Contains(err.Error(), "provider instance is closed") || strings.Contains(err.Error(), "closed") {
		t.Log("CONFIRMED: after the failed retain, the handshake proceeded with the closed snapshot and failed with a closed-provider error")
	} else {
		t.Fatalf("handshake failed with an unexpected error: %v", err)
	}
}
