// Run: bash verify/repro/c4/run.sh   (instruments a worktree of the perfect branch with a hook after hiPtr.Load() in ClientSideTLSConfig and runs this test)
//go:build verifyrepro

package xds

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"google.golang.org/grpc/credentials/tls/certprovider"
	"google.golang.org/grpc/internal/grpcsync"
	"google.golang.org/grpc/testdata"
)

type c4Provider struct {
	name    string
	roots   *x509.CertPool
	onLoad  func()
	kmCalls atomic.Int32
	closed  atomic.Bool
}

func (p *c4Provider) KeyMaterial(context.Context) (*certprovider.KeyMaterial, error) {
	p.kmCalls.Add(1)
	if p.onLoad != nil {
		p.onLoad()
	}
	if p.closed.Load() {
		return nil, errors.New("provider instance is closed")
	}
	return &certprovider.KeyMaterial{Roots: p.roots}, nil
}
func (p *c4Provider) Close() { p.closed.Store(true) }

func c4Roots(t *testing.T, caPath string) *x509.CertPool {
	pemData, err := os.ReadFile(testdata.Path(caPath))
	if err != nil {
		t.Fatal(err)
	}
	roots := x509.NewCertPool()
	roots.AppendCertsFromPEM(pemData)
	return roots
}

// c4WhichRoots reports whether cfg verifies the test server certificate: roots
// A (server_ca) trust it, roots B (client_ca) do not.
func c4WhichRoots(t *testing.T, cfg *tls.Config) string {
	serverCert := loadCert(t, testdata.Path("x509/server1_cert.pem"), testdata.Path("x509/server1_key.pem"))
	if err := cfg.VerifyPeerCertificate(serverCert, nil); err != nil {
		return "B (server cert rejected: " + err.Error() + ")"
	}
	return "A (server cert accepted)"
}

func c4Setup(t *testing.T) (a, b *c4Provider, rc1, rc2 *grpcsync.RefCounted[HandshakeInfo], hiPtr *atomic.Pointer[grpcsync.RefCounted[HandshakeInfo]]) {
	a = &c4Provider{name: "A", roots: c4Roots(t, "x509/server_ca_cert.pem")}
	b = &c4Provider{name: "B", roots: c4Roots(t, "x509/client_ca_cert.pem")}
	rc1 = NewRefCountedHandshakeInfo(a, nil, nil, "", false, false, false)
	rc2 = NewRefCountedHandshakeInfo(b, nil, nil, "", false, false, false)
	hiPtr = &atomic.Pointer[grpcsync.RefCounted[HandshakeInfo]]{}
	hiPtr.Store(rc1)
	return
}

// clusterimpl replacement on the perfect branch: Swap, then Decrement the old.
func c4Replace(hiPtr *atomic.Pointer[grpcsync.RefCounted[HandshakeInfo]], rc2 *grpcsync.RefCounted[HandshakeInfo]) {
	if old := hiPtr.Swap(rc2); old != nil {
		old.Decrement()
	}
}

// TestC4Probe_ReplaceBetweenLoadAndTryIncrement replaces the security
// configuration after ClientSideTLSConfig has loaded rc1 and before it calls
// TryIncrement on it.
func (s) TestC4Probe_ReplaceBetweenLoadAndTryIncrement(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	a, b, rc1, rc2, hiPtr := c4Setup(t)

	var loads []string
	replaced := false
	C4TestHookAfterLoad = func(rc *grpcsync.RefCounted[HandshakeInfo]) {
		switch rc {
		case rc1:
			loads = append(loads, "rc1(A)")
		case rc2:
			loads = append(loads, "rc2(B)")
		default:
			loads = append(loads, "other")
		}
		if rc == rc1 && !replaced {
			replaced = true
			c4Replace(hiPtr, rc2)
		}
	}
	defer func() { C4TestHookAfterLoad = nil }()

	cfg, useFallback, done, err := ClientSideTLSConfig(ctx, hiPtr, "")
	t.Logf("C4PROBE loads=%v useFallback=%v err=%v", loads, useFallback, err)
	t.Logf("C4PROBE A: keyMaterialCalls=%d closed=%v; B: keyMaterialCalls=%d closed=%v", a.kmCalls.Load(), a.closed.Load(), b.kmCalls.Load(), b.closed.Load())
	if err != nil {
		t.Fatalf("ClientSideTLSConfig failed: %v", err)
	}
	t.Logf("C4PROBE roots in returned tls.Config: %s", c4WhichRoots(t, cfg))
	if a.kmCalls.Load() > 0 {
		t.Errorf("C4PROBE RESULT: A's material was read and the handshake then continued with other roots")
	} else {
		t.Logf("C4PROBE RESULT: A was never read (no roots selected before ownership); handshake bound to B after retry")
	}
	done()
}

// TestC4Probe_ReplaceAfterMaterialRead replaces the security configuration
// while ClientSideTLSConfig is inside A.KeyMaterial (ownership already taken).
func (s) TestC4Probe_ReplaceAfterMaterialRead(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	a, b, _, rc2, hiPtr := c4Setup(t)
	a.onLoad = func() { c4Replace(hiPtr, rc2) }

	cfg, useFallback, done, err := ClientSideTLSConfig(ctx, hiPtr, "")
	t.Logf("C4PROBE useFallback=%v err=%v", useFallback, err)
	t.Logf("C4PROBE A: keyMaterialCalls=%d closed=%v; B: keyMaterialCalls=%d closed=%v", a.kmCalls.Load(), a.closed.Load(), b.kmCalls.Load(), b.closed.Load())
	if err != nil {
		t.Fatalf("ClientSideTLSConfig failed: %v", err)
	}
	which := c4WhichRoots(t, cfg)
	t.Logf("C4PROBE roots in returned tls.Config: %s", which)
	if which[0] != 'A' {
		t.Errorf("C4PROBE RESULT: handshake switched away from the selected roots A")
	} else {
		t.Logf("C4PROBE RESULT: handshake remained bound to the selected provider A")
	}
	done()
	t.Logf("C4PROBE after done(): A closed=%v", a.closed.Load())
}
