// Run: cp verify/repro/c1_selected_roots_lost_test.go credentials/xds/ && go test -tags verify_repro ./credentials/xds -run '^TestVerifyC1' -count=1 -v   (on evalon/grpc-go-xd-89cb2288; FAILS there = problem present)
//go:build verify_repro

package xds

import (
	"context"
	"errors"
	"net"
	"sync"
	"sync/atomic"
	"testing"

	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/tls/certprovider"
	icredentials "google.golang.org/grpc/internal/credentials"
	xdsinternal "google.golang.org/grpc/internal/credentials/xds"
	"google.golang.org/grpc/internal/xds/matcher"
	"google.golang.org/grpc/resolver"
)

// verifyGatedProvider holds valid roots, blocks in KeyMaterial until released,
// and fails once closed (like certprovider store wrappers after Close).
type verifyGatedProvider struct {
	km        *certprovider.KeyMaterial
	entered   chan struct{}
	release   chan struct{}
	mu        sync.Mutex
	closed    bool
	closeOnce sync.Once
}

func (p *verifyGatedProvider) KeyMaterial(ctx context.Context) (*certprovider.KeyMaterial, error) {
	select {
	case p.entered <- struct{}{}:
	default:
	}
	select {
	case <-p.release:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return nil, errors.New("provider instance is closed")
	}
	return p.km, nil
}

func (p *verifyGatedProvider) Close() {
	p.closeOnce.Do(func() { p.mu.Lock(); p.closed = true; p.mu.Unlock() })
}

// The handshake selects provider A (trusted roots) and enters its KeyMaterial.
// A Cluster security replacement then publishes provider B (untrusted roots)
// and closes A, as clusterimpl does. If the selection were preserved (hold /
// independent copy / transferred ownership), the handshake would finish with
// A's roots and succeed.
func TestVerifyC1_SelectedRootsSurviveReplacement(t *testing.T) {
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

	sans := []matcher.StringMatcher{matcher.NewExactStringMatcher(defaultTestCertSAN, false)}
	provA := &verifyGatedProvider{km: makeRootProvider(t, "x509/server_ca_cert.pem").km, entered: make(chan struct{}, 1), release: make(chan struct{})}
	provB := makeRootProvider(t, "x509/client_ca_cert.pem")
	var hiPtr atomic.Pointer[xdsinternal.HandshakeInfo]
	hiPtr.Store(xdsinternal.NewHandshakeInfo(provA, nil, sans, false, "", false, false))
	addr := xdsinternal.SetHandshakeInfo(resolver.Address{}, &hiPtr)
	hsCtx := icredentials.NewClientHandshakeInfoContext(ctx, credentials.ClientHandshakeInfo{Attributes: addr.Attributes})

	done := make(chan error, 1)
	go func() {
		_, _, err := creds.ClientHandshake(hsCtx, authority, conn)
		done <- err
	}()
	select {
	case <-provA.entered:
	case <-ctx.Done():
		t.Fatal("handshake never entered provider A KeyMaterial")
	}
	t.Log("handshake is inside provider A KeyMaterial; publishing replacement B and closing A")
	hiPtr.Store(xdsinternal.NewHandshakeInfo(provB, nil, sans, false, "", false, false))
	provA.Close()
	close(provA.release)
	if err := <-done; err != nil {
		t.Fatalf("handshake that selected provider A lost that selection during replacement: %v", err)
	}
	t.Log("handshake completed with the originally selected roots")
}
