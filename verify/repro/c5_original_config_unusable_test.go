// Run: cp verify/repro/c5_original_config_unusable_test.go credentials/xds/ && go test -tags verify_repro ./credentials/xds -run '^TestVerifyC5' -count=1 -v   (on evalon/grpc-go-xd-89cb2288; PASS = each part of the problem is present)
//go:build verify_repro

package xds

import (
	"context"
	"net"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"google.golang.org/grpc/credentials"
	icredentials "google.golang.org/grpc/internal/credentials"
	xdsinternal "google.golang.org/grpc/internal/credentials/xds"
	"google.golang.org/grpc/internal/xds/matcher"
	"google.golang.org/grpc/resolver"
)

// Part 1: the original blockingProvider used by
// TestClientCredsProviderSwitchDuringHandshake never yields key material; it
// only returns once closed, and then with an error.
func TestVerifyC5_Part1_BlockingProviderSuppliesNoMaterial(t *testing.T) {
	p := newBlockingProvider()
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	km, err := p.KeyMaterial(ctx)
	t.Logf("open blockingProvider.KeyMaterial (500ms budget): km=%v err=%v", km, err)
	if err == nil {
		t.Fatalf("open blockingProvider returned usable material: %+v", km)
	}
	p.Close()
	km, err = p.KeyMaterial(context.Background())
	t.Logf("closed blockingProvider.KeyMaterial: km=%v err=%v", km, err)
	if err == nil {
		t.Fatalf("closed blockingProvider returned usable material: %+v", km)
	}
}

// Part 2: the original "wrong-SAN" matcher rejects the test peer
// (server1_cert.pem, SAN DNS:*.test.example.com) even with correct roots.
func TestVerifyC5_Part2_WrongSANMatcherRejectsPeer(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), defaultTestTimeout)
	defer cancel()
	ts := newTestServerWithHandshakeFunc(ctx, testServerTLSHandshake)
	defer ts.stop()
	creds, err := NewClientCredentials(ClientOptions{FallbackCreds: makeFallbackClientCreds(t)})
	if err != nil {
		t.Fatal(err)
	}
	conn, err := net.Dial("tcp", ts.address)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	var hiPtr atomic.Pointer[xdsinternal.HandshakeInfo]
	hiPtr.Store(xdsinternal.NewHandshakeInfo(makeRootProvider(t, "x509/server_ca_cert.pem"), nil, []matcher.StringMatcher{matcher.NewExactStringMatcher("wrong-SAN", false)}, false, "", false, false))
	addr := xdsinternal.SetHandshakeInfo(resolver.Address{}, &hiPtr)
	hsCtx := icredentials.NewClientHandshakeInfoContext(ctx, credentials.ClientHandshakeInfo{Attributes: addr.Attributes})
	_, _, err = creds.ClientHandshake(hsCtx, defaultTestCertSAN, conn)
	t.Logf("handshake with trusted roots + wrong-SAN matcher: err=%v", err)
	if err == nil || !strings.Contains(err.Error(), "SANs") {
		t.Fatalf("wrong-SAN matcher did not reject the peer; err=%v", err)
	}
}
