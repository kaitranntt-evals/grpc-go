//go:build verify_repro

/*
 * Verify probe for claim C1 on branch evalon/grpc-go-xd-23014fd5.
 * Run: go test ./credentials/xds/ -run TestVerifyC1 -count=1 -v
 *
 * Simulates a Cluster security update racing with a handshake: the handshake
 * selects the currently published HandshakeInfo (whose validation roots would
 * REJECT this server), and just before it can pin the providers, the update
 * publishes a replacement HandshakeInfo (whose roots TRUST this server) and
 * closes the old providers. On this branch the stale-retry loop in
 * ClientHandshake reloads the pointer and completes the handshake under the
 * REPLACEMENT validation roots instead of the ones the handshake selected.
 */

package xds

import (
	"context"
	"net"
	"sync"
	"sync/atomic"
	"testing"

	"google.golang.org/grpc/credentials"
	icredentials "google.golang.org/grpc/internal/credentials"
	xdsinternal "google.golang.org/grpc/internal/credentials/xds"
	"google.golang.org/grpc/internal/xds/matcher"
	"google.golang.org/grpc/resolver"
)

func TestVerifyC1_SwitchesToReplacementRoots(t *testing.T) {
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

	sms := []matcher.StringMatcher{matcher.NewExactStringMatcher(defaultTestCertSAN, false)}

	// Selected (old) HandshakeInfo: roots that would REJECT the test server.
	oldRoot := xdsinternal.NewRefCountedProvider(makeRootProvider(t, "x509/client_ca_cert.pem"))
	oldHI := xdsinternal.NewHandshakeInfo(oldRoot, nil, sms, false, "", false, false)

	// Replacement HandshakeInfo: roots that TRUST the test server.
	newRoot := xdsinternal.NewRefCountedProvider(makeRootProvider(t, "x509/server_ca_cert.pem"))
	newHI := xdsinternal.NewHandshakeInfo(newRoot, nil, sms, false, "", false, false)

	var hiPtr atomic.Pointer[xdsinternal.HandshakeInfo]
	hiPtr.Store(oldHI)
	addr := xdsinternal.SetHandshakeInfo(resolver.Address{}, &hiPtr)
	hctx := icredentials.NewClientHandshakeInfoContext(ctx, credentials.ClientHandshakeInfo{Attributes: addr.Attributes})

	// Just before the handshake pins the providers of the HandshakeInfo it
	// selected, a Cluster security update publishes the replacement and
	// closes (releases the owner reference of) the old provider.
	var once sync.Once
	xdsinternal.TestOnlyBeforeAcquireProvidersHook = func() {
		once.Do(func() {
			hiPtr.Store(newHI)
			oldRoot.Close()
		})
	}
	defer func() { xdsinternal.TestOnlyBeforeAcquireProvidersHook = nil }()

	_, _, err = creds.ClientHandshake(hctx, authority, conn)
	if err != nil {
		t.Fatalf("ClientHandshake() failed: %v (handshake did NOT silently switch to replacement roots)", err)
	}
	t.Log("CONFIRMED: handshake selected the old HandshakeInfo (roots that reject this server), failed to retain it, and completed successfully under the REPLACEMENT validation roots")
}
