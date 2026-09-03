// Run: cp this file into credentials/xds/ of branch evalon/grpc-go-xd-3a8572bc or evalon/grpc-go-xd-9085e702, then `go test ./credentials/xds -run '^Test$/^VerifyC4' -v -count=1`
package xds

import (
	"context"
	"net"
	"sync/atomic"
	"testing"

	"google.golang.org/grpc/credentials"
	icredentials "google.golang.org/grpc/internal/credentials"
	xdsinternal "google.golang.org/grpc/internal/credentials/xds"
	"google.golang.org/grpc/resolver"
)

// A non-nil HandshakeInfo holder whose current snapshot is nil: does ClientHandshake
// panic, or does it use fallback credentials / return an error?
func (s) TestVerifyC4_NilCurrentSnapshotDoesNotPanic(t *testing.T) {
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

	var hiPtr atomic.Pointer[xdsinternal.HandshakeInfo] // holder exists, current snapshot nil
	addr := xdsinternal.SetHandshakeInfo(resolver.Address{}, &hiPtr)
	hctx := icredentials.NewClientHandshakeInfoContext(ctx, credentials.ClientHandshakeInfo{Attributes: addr.Attributes})

	var panicked any
	var ai credentials.AuthInfo
	func() {
		defer func() { panicked = recover() }()
		_, ai, err = creds.ClientHandshake(hctx, authority, conn)
	}()
	t.Logf("holder non-nil, Load()==nil: panicked=%v err=%v", panicked, err)
	if panicked != nil {
		t.Fatalf("CONFIRMED: ClientHandshake panicked: %v", panicked)
	}
	if err == nil {
		if cerr := compareAuthInfo(ctx, ts, ai); cerr != nil {
			t.Fatal(cerr)
		}
		t.Log("REFUTED: handshake completed via fallback credentials without panic")
	}
}
