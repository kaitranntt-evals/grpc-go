// Shared harness for the C1 probes. Copied into credentials/xds/ of a C1 target
//go:build verifyrepro

// branch worktree by verify/repro/c1/run.sh; not meant to be run from here.
package xds

import (
	"context"
	"crypto/x509"
	"errors"
	"fmt"
	"net"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/tls/certprovider"
	icredentials "google.golang.org/grpc/internal/credentials"
	xdsinternal "google.golang.org/grpc/internal/credentials/xds"
	"google.golang.org/grpc/resolver"
	"google.golang.org/grpc/testdata"
)

// c1Provider is a fake certificate provider that serves a fixed root pool and
// records how it is used. After Close, KeyMaterial fails the way the
// certprovider store's closed wrapper does ("provider instance is closed").
type c1Provider struct {
	name  string
	roots *x509.CertPool

	mu           sync.Mutex
	closed       bool
	closeCount   int
	kmCalls      int
	kmAfterClose int
}

func (p *c1Provider) KeyMaterial(context.Context) (*certprovider.KeyMaterial, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.kmCalls++
	if p.closed {
		p.kmAfterClose++
		return nil, errors.New("provider instance is closed")
	}
	return &certprovider.KeyMaterial{Roots: p.roots}, nil
}

func (p *c1Provider) Close() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.closeCount++
	p.closed = true
}

func (p *c1Provider) String() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return fmt.Sprintf("%s{closed=%v closeCount=%d keyMaterialCalls=%d keyMaterialCallsAfterClose=%d}", p.name, p.closed, p.closeCount, p.kmCalls, p.kmAfterClose)
}

func c1Roots(t *testing.T, caPath string) *x509.CertPool {
	t.Helper()
	pemData, err := os.ReadFile(testdata.Path(caPath))
	if err != nil {
		t.Fatal(err)
	}
	roots := x509.NewCertPool()
	roots.AppendCertsFromPEM(pemData)
	return roots
}

// c1RootA trusts the test server's certificate; c1RootB does not.
func c1RootA(t *testing.T) *c1Provider {
	return &c1Provider{name: "A", roots: c1Roots(t, "x509/server_ca_cert.pem")}
}
func c1RootB(t *testing.T) *c1Provider {
	return &c1Provider{name: "B", roots: c1Roots(t, "x509/client_ca_cert.pem")}
}

// c1Context mimics what the transport does: the atomic HandshakeInfo pointer
// published by clusterimpl travels in the address attributes.
func c1Context(parent context.Context, hiPtr *atomic.Pointer[xdsinternal.HandshakeInfo]) context.Context {
	addr := xdsinternal.SetHandshakeInfo(resolver.Address{}, hiPtr)
	return icredentials.NewClientHandshakeInfoContext(parent, credentials.ClientHandshakeInfo{Attributes: addr.Attributes})
}

// c1Handshake dials the test server and runs the real xDS ClientHandshake.
func c1Handshake(t *testing.T, ctx context.Context, ts *testServer) error {
	t.Helper()
	creds, err := NewClientCredentials(ClientOptions{FallbackCreds: makeFallbackClientCreds(t)})
	if err != nil {
		t.Fatal(err)
	}
	conn, err := net.Dial("tcp", ts.address)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_, _, err = creds.ClientHandshake(ctx, authority, conn)
	return err
}

func c1Name(hi, hi1, hi2 *xdsinternal.HandshakeInfo) string {
	switch hi {
	case hi1:
		return "hi1(A)"
	case hi2:
		return "hi2(B)"
	case nil:
		return "nil"
	}
	return "other"
}

// c1Report logs the observations and fails the test iff the handshake used a
// closed selected provider (C1 confirm condition). A handshake that never
// touched A's material and retried on the replacement is reported, not failed.
func c1Report(t *testing.T, err error, loads []string, rootA, rootB *c1Provider) {
	t.Helper()
	t.Logf("C1PROBE handshake error: %v", err)
	t.Logf("C1PROBE loads observed by hook: %v", loads)
	t.Logf("C1PROBE rootA=%s", rootA)
	t.Logf("C1PROBE rootB=%s", rootB)
	rootA.mu.Lock()
	aAfterClose := rootA.kmAfterClose
	aCalls := rootA.kmCalls
	rootA.mu.Unlock()
	rootB.mu.Lock()
	bCalls := rootB.kmCalls
	rootB.mu.Unlock()
	switch {
	case aAfterClose > 0:
		t.Errorf("C1PROBE RESULT: handshake read key material from CLOSED selected provider A (%d call(s) after Close); error=%v", aAfterClose, err)
	case err != nil && strings.Contains(err.Error(), "provider instance is closed"):
		t.Errorf("C1PROBE RESULT: handshake proceeded with its CLOSED selected provider A and failed before loading material; error=%v", err)
	case aCalls > 0:
		t.Logf("C1PROBE RESULT: handshake used provider A (still open) — replacement did not invalidate it")
	case bCalls > 0:
		t.Logf("C1PROBE RESULT: A was never read; handshake did not acquire A and proceeded with replacement B (error=%v)", err)
	default:
		t.Logf("C1PROBE RESULT: no provider was read; handshake ended with error=%v", err)
	}
}
