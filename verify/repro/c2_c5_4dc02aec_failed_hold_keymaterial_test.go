// Run: cp this file into credentials/xds/ of branch evalon/grpc-go-xd-4dc02aec, then `go test ./credentials/xds -run '^Test$/^VerifyC2C5' -v -count=1`
package xds

import (
	"context"
	"net"
	"sync/atomic"
	"testing"

	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/tls/certprovider"
	icredentials "google.golang.org/grpc/internal/credentials"
	xdsinternal "google.golang.org/grpc/internal/credentials/xds"
	"google.golang.org/grpc/internal/xds/matcher"
	"google.golang.org/grpc/resolver"
)

// c2Provider records KeyMaterial calls and whether Close ran before them.
type c2Provider struct {
	km           *certprovider.KeyMaterial
	closed       atomic.Bool
	kmCalls      atomic.Int32
	kmAfterClose atomic.Int32
}

func (p *c2Provider) KeyMaterial(context.Context) (*certprovider.KeyMaterial, error) {
	p.kmCalls.Add(1)
	if p.closed.Load() {
		p.kmAfterClose.Add(1)
	}
	return p.km, nil
}
func (p *c2Provider) Close() { p.closed.Store(true) }

// The owner published hi, then released its (only) reference WITHOUT replacing the
// publication (refs -> 0, providers closed). A handshake then selects hi: its hold
// fails, AcquireHandshakeInfo hands back the dead hi, and ClientHandshake proceeds
// to call KeyMaterial on the closed provider.
func (s) TestVerifyC2C5_FailedHoldStillInvokesKeyMaterial(t *testing.T) {
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

	root := &c2Provider{km: makeRootProvider(t, "x509/server_ca_cert.pem").km}
	sms := []matcher.StringMatcher{matcher.NewExactStringMatcher(defaultTestCertSAN, false)}
	var hiPtr atomic.Pointer[xdsinternal.HandshakeInfo]
	hi := xdsinternal.NewHandshakeInfo(root, nil, sms, false, "", false, false)
	hiPtr.Store(hi)
	hi.Release() // owner drops last reference; publication unchanged
	t.Logf("after owner Release(): root.closed=%v", root.closed.Load())

	// Part 1: AcquireHandshakeInfo returns the dead hi (not nil) with a no-op release.
	got, rel := xdsinternal.AcquireHandshakeInfo(&hiPtr)
	t.Logf("AcquireHandshakeInfo after failed hold -> same dead hi=%v", got == hi)
	rel()
	t.Logf("release() after failed hold is a no-op: root still closed=%v, kmCalls=%d", root.closed.Load(), root.kmCalls.Load())

	// Part 2: ClientHandshake invokes KeyMaterial on the closed provider.
	addr := xdsinternal.SetHandshakeInfo(resolver.Address{}, &hiPtr)
	hctx := icredentials.NewClientHandshakeInfoContext(ctx, credentials.ClientHandshakeInfo{Attributes: addr.Attributes})
	_, _, err = creds.ClientHandshake(hctx, authority, conn)
	t.Logf("ClientHandshake err=%v; root.kmCalls=%d kmAfterClose=%d", err, root.kmCalls.Load(), root.kmAfterClose.Load())
	if got != hi || root.kmAfterClose.Load() == 0 {
		t.Fatalf("expected handshake to invoke KeyMaterial on the dead selection; got hi==dead:%v kmAfterClose=%d", got == hi, root.kmAfterClose.Load())
	}
	if err == nil {
		t.Log("handshake SUCCEEDED using key material from an already-Closed provider")
		ts.hsResult.Receive(ctx)
	}
}
