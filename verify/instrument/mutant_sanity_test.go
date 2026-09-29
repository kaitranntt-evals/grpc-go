// Run (after apply_post_replacement_mutant.sh): cp verify/instrument/mutant_sanity_test.go credentials/xds/ && go test -tags verify_repro ./credentials/xds -run '^TestVerifyMutantSanity' -count=1 -v   (must FAIL under the mutant, PASS without it)
//go:build verify_repro

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

// A connection initiated after the HandshakeInfo is replaced must still succeed.
func TestVerifyMutantSanity_PostReplacementConnection(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), defaultTestTimeout)
	defer cancel()
	creds, err := NewClientCredentials(ClientOptions{FallbackCreds: makeFallbackClientCreds(t)})
	if err != nil {
		t.Fatal(err)
	}
	sans := []matcher.StringMatcher{matcher.NewExactStringMatcher(defaultTestCertSAN, false)}
	var hiPtr atomic.Pointer[xdsinternal.HandshakeInfo]
	addr := xdsinternal.SetHandshakeInfo(resolver.Address{}, &hiPtr)
	hsCtx := icredentials.NewClientHandshakeInfoContext(ctx, credentials.ClientHandshakeInfo{Attributes: addr.Attributes})
	for i := 0; i < 2; i++ {
		hiPtr.Store(xdsinternal.NewHandshakeInfo(makeRootProvider(t, "x509/server_ca_cert.pem"), nil, sans, false, "", false, false))
		ts := newTestServerWithHandshakeFunc(ctx, testServerTLSHandshake)
		conn, err := net.Dial("tcp", ts.address)
		if err != nil {
			t.Fatal(err)
		}
		_, _, err = creds.ClientHandshake(hsCtx, authority, conn)
		conn.Close()
		ts.stop()
		t.Logf("connection %d (after %d replacements): err=%v", i+1, i, err)
		if err != nil {
			t.Fatalf("connection %d failed: %v", i+1, err)
		}
	}
}
