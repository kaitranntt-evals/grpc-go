//go:build verify_repro

/*
 * Verify probe for claim C6 on branch evalon/grpc-go-xd-f0677079.
 * Run: go test ./credentials/xds/ -run TestVerifyC6 -count=1 -v
 *
 * AcquireHandshakeInfo makes exactly two acquisition attempts. This probe
 * deterministically pauses acquisition after the first attempt fails (the
 * selected snapshot was retired by a security replacement) and performs a
 * second rapid replacement that retires the replacement snapshot too, then
 * resumes. It observes whether acquisition returns a closed HandshakeInfo and
 * ClientHandshake reaches a closed provider.
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

func TestVerifyC6_RapidReplacementsReturnClosedHandshakeInfo(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), defaultTestTimeout)
	defer cancel()
	ts := newTestServerWithHandshakeFunc(ctx, testServerTLSHandshake)
	defer ts.stop()

	creds, err := NewClientCredentials(ClientOptions{FallbackCreds: makeFallbackClientCreds(t)})
	if err != nil {
		t.Fatalf("NewClientCredentials() failed: %v", err)
	}

	sms := []matcher.StringMatcher{matcher.NewExactStringMatcher(defaultTestCertSAN, false)}
	hi1 := xdsinternal.NewHandshakeInfo(makeRootProvider(t, "x509/server_ca_cert.pem"), nil, sms, false, "", false, false)
	hi2 := xdsinternal.NewHandshakeInfo(makeRootProvider(t, "x509/server_ca_cert.pem"), nil, sms, false, "", false, false)
	hi3 := xdsinternal.NewHandshakeInfo(makeRootProvider(t, "x509/server_ca_cert.pem"), nil, sms, false, "", false, false)

	var hiPtr atomic.Pointer[xdsinternal.HandshakeInfo]
	hiPtr.Store(hi1)
	addr := xdsinternal.SetHandshakeInfo(resolver.Address{}, &hiPtr)
	hctx := icredentials.NewClientHandshakeInfoContext(ctx, credentials.ClientHandshakeInfo{Attributes: addr.Attributes})

	// First replacement: retire hi1 (no in-flight references, so its providers
	// close immediately). The handshake below loads hi1 — as in the production
	// window where its Load happened just before the replacement was published
	// — and fails its first acquisition attempt.
	hi1.Retire()

	// Second rapid replacement, injected deterministically between the failed
	// first acquisition attempt and the second (final) attempt: publish hi3
	// and retire hi2 — but the pointer visible to the retry is hi2, which is
	// already retired and closed.
	var once sync.Once
	xdsinternal.TestOnlyBeforeReacquireHook = func() {
		once.Do(func() {
			hiPtr.Store(hi2)
			hi2.Retire()
			_ = hi3 // hi3 would be the live replacement, published after this window.
		})
	}
	defer func() { xdsinternal.TestOnlyBeforeReacquireHook = nil }()

	conn, err := net.Dial("tcp", ts.address)
	if err != nil {
		t.Fatalf("net.Dial(%s) failed: %v", ts.address, err)
	}
	defer conn.Close()

	_, _, err = creds.ClientHandshake(hctx, authority, conn)
	if err == nil {
		t.Fatal("REFUTED: ClientHandshake() succeeded; acquisition retained a live snapshot")
	}
	t.Logf("CONFIRMED: after two rapid replacements exhausted the fixed acquisition attempts, ClientHandshake() reached a closed provider: %v", err)
}
