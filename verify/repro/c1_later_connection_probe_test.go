// C1 probe: copy to credentials/xds/c1_later_connection_probe_test.go on branch
// evalon/grpc-go-xd-8220779b and run
//   go test ./credentials/xds -run 'Test/VerifyC1LaterConnectionError$' -count=1 -v
// It repeats the solution test's scenario but prints the error of the later
// connection and asserts a distinguishable certificate-trust outcome. Without
// the C1 mutation it passes (x509 unknown authority); with the mutation applied
// it fails, while the solution's own test stays green.
// Remove the go:build ignore line below after copying the file into the target package.

//go:build ignore

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

func (s) TestVerifyC1LaterConnectionError(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), defaultTestTimeout)
	defer cancel()
	ts := newTestServerWithHandshakeFunc(ctx, testServerTLSHandshake)
	defer ts.stop()

	creds, err := NewClientCredentials(ClientOptions{FallbackCreds: makeFallbackClientCreds(t)})
	if err != nil {
		t.Fatalf("NewClientCredentials failed: %v", err)
	}
	root1 := newBlockingRootProvider(t, "x509/server_ca_cert.pem")
	close(root1.unblock)
	sanMatchers := []matcher.StringMatcher{matcher.NewExactStringMatcher(defaultTestCertSAN, false)}
	hi1 := xdsinternal.NewHandshakeInfo(root1, nil, sanMatchers, false, "", false, false)
	var hiPtr atomic.Pointer[xdsinternal.HandshakeInfo]
	hiPtr.Store(hi1)
	addr := xdsinternal.SetHandshakeInfo(resolver.Address{}, &hiPtr)
	ctx = icredentials.NewClientHandshakeInfoContext(ctx, credentials.ClientHandshakeInfo{Attributes: addr.Attributes})

	conn, err := net.Dial("tcp", ts.address)
	if err != nil {
		t.Fatalf("net.Dial failed: %v", err)
	}
	defer conn.Close()
	if _, _, err := creds.ClientHandshake(ctx, authority, conn); err != nil {
		t.Fatalf("first ClientHandshake failed: %v", err)
	}

	// Replace the configuration with roots that do not trust the server.
	root2 := makeRootProvider(t, "x509/client_ca_cert.pem")
	hi2 := xdsinternal.NewHandshakeInfo(root2, nil, sanMatchers, false, "", false, false)
	hiPtr.Swap(hi2).Release()

	conn2, err := net.Dial("tcp", ts.address)
	if err != nil {
		t.Fatalf("net.Dial failed: %v", err)
	}
	defer conn2.Close()
	_, _, err = creds.ClientHandshake(ctx, authority, conn2)
	t.Logf("later connection ClientHandshake error: %v", err)
	if err == nil || !strings.Contains(err.Error(), "x509: certificate signed by unknown authority") {
		t.Fatalf("later connection error = %v, want x509 unknown authority (replacement roots)", err)
	}
}
