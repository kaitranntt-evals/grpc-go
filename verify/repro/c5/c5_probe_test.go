// Run: cp verify/repro/c5/c5_probe_test.go ~/wt/81241111/credentials/xds/ && (cd ~/wt/81241111 && go test -tags verifyrepro ./credentials/xds -run 'Test/C5Probe' -count=1 -v)
//go:build verifyrepro

package xds

import (
	"context"
	"net"
	"sync/atomic"
	"testing"
	"time"

	"google.golang.org/grpc/credentials"
	icredentials "google.golang.org/grpc/internal/credentials"
	xdsinternal "google.golang.org/grpc/internal/credentials/xds"
	"google.golang.org/grpc/resolver"
)

// TestC5Probe_NilHandshakeInfoInAtomicPointer runs ClientHandshake with
// attributes that carry a non-nil *atomic.Pointer[HandshakeInfo] whose current
// value is nil, and reports whether it panics.
func (s) TestC5Probe_NilHandshakeInfoInAtomicPointer(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	ts := newTestServerWithHandshakeFunc(ctx, testServerTLSHandshake)
	defer ts.stop()

	var hiPtr atomic.Pointer[xdsinternal.HandshakeInfo] // Load() == nil
	addr := xdsinternal.SetHandshakeInfo(resolver.Address{}, &hiPtr)
	hiCtx := icredentials.NewClientHandshakeInfoContext(ctx, credentials.ClientHandshakeInfo{Attributes: addr.Attributes})

	creds, err := NewClientCredentials(ClientOptions{FallbackCreds: makeFallbackClientCreds(t)})
	if err != nil {
		t.Fatal(err)
	}
	conn, err := net.Dial("tcp", ts.address)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	var panicked any
	var ai credentials.AuthInfo
	func() {
		defer func() { panicked = recover() }()
		_, ai, err = creds.ClientHandshake(hiCtx, authority, conn)
	}()
	t.Logf("C5PROBE panic=%v err=%v", panicked, err)
	if panicked != nil {
		t.Fatalf("C5PROBE RESULT: ClientHandshake panicked: %v", panicked)
	}
	if err != nil {
		t.Fatalf("C5PROBE RESULT: no panic, but handshake failed: %v", err)
	}
	t.Logf("C5PROBE RESULT: no panic; handshake completed via fallback credentials (authType=%s)", ai.AuthType())
}
