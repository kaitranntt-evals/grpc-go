//go:build verify_repro

/*
 * Verify probe for claim C1 on branch evalon/grpc-go-xd-7ac76612.
 * Run: go test ./credentials/xds/ -run TestVerifyC1 -count=1 -v
 *
 * Simulates the production interleaving in which a Cluster security update
 * releases the balancer's (owner) reference on the certificate providers of
 * the currently published HandshakeInfo before a handshake that selected that
 * HandshakeInfo begins loading key material. On this branch,
 * handleSecurityConfig closes the old providers BEFORE storing the
 * replacement HandshakeInfo, so a handshake can load the old HandshakeInfo
 * whose providers are already fully closed.
 */

package xds

import (
	"context"
	"net"
	"strings"
	"sync/atomic"
	"testing"

	"google.golang.org/grpc/credentials"
	icredentials "google.golang.org/grpc/internal/credentials"
	xdsinternal "google.golang.org/grpc/internal/credentials/xds"
	"google.golang.org/grpc/internal/xds/matcher"
	"google.golang.org/grpc/resolver"
)

func TestVerifyC1_ClosedBeforeKeyMaterial(t *testing.T) {
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

	// The provider selected by the handshake: reference-counted, with trusted
	// validation roots, exactly as handleSecurityConfig builds it.
	root := xdsinternal.NewRefCountedProvider(makeRootProvider(t, "x509/server_ca_cert.pem"))
	sms := []matcher.StringMatcher{matcher.NewExactStringMatcher(defaultTestCertSAN, false)}
	hi := xdsinternal.NewHandshakeInfo(root, nil, sms, false, "", false, false)
	var hiPtr atomic.Pointer[xdsinternal.HandshakeInfo]
	hiPtr.Store(hi)
	addr := xdsinternal.SetHandshakeInfo(resolver.Address{}, &hiPtr)
	hctx := icredentials.NewClientHandshakeInfoContext(ctx, credentials.ClientHandshakeInfo{Attributes: addr.Attributes})

	// Cluster security update: the balancer releases its reference (Close)
	// on the old provider. On this branch that happens BEFORE the replacement
	// HandshakeInfo is stored, so the pointer still holds the old
	// HandshakeInfo. No KeyMaterial load has happened yet.
	root.Close()

	_, _, err = creds.ClientHandshake(hctx, authority, conn)
	if err == nil {
		t.Fatal("REFUTED: ClientHandshake succeeded; handshake retained the selected provider or its material")
	}
	t.Logf("ClientHandshake() error: %v", err)
	if strings.Contains(err.Error(), "certificate provider is closed") {
		t.Log("CONFIRMED: handshake failed with a closed-provider error before KeyMaterial ever began")
	} else {
		t.Fatalf("handshake failed with an unexpected error: %v", err)
	}
}
