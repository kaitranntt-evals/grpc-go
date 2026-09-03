// Run: cp this file into internal/credentials/xds/ of branch evalon/grpc-go-xd-3a8572bc, then `go test ./internal/credentials/xds -run TestVerifyC1_ -v -count=1`
package xds

import (
	"context"
	"testing"

	"google.golang.org/grpc/credentials/tls/certprovider"
)

type c1CountingProvider struct {
	kmCalls int
	closed  bool
}

func (p *c1CountingProvider) KeyMaterial(context.Context) (*certprovider.KeyMaterial, error) {
	p.kmCalls++
	return &certprovider.KeyMaterial{}, nil
}
func (p *c1CountingProvider) Close() { p.closed = true }

// Close() while a handshake reference remains, then start a FRESH acquire and a FRESH KeyMaterial.
func TestVerifyC1_SharedProviderAdmitsWorkAfterClose(t *testing.T) {
	underlying := &c1CountingProvider{}
	sp := NewSharedProvider(underlying)
	release1, ok := acquireProvider(sp) // previously admitted handshake reference
	if !ok {
		t.Fatal("initial acquire failed")
	}
	sp.Close() // owner initiates shutdown
	t.Logf("after Close: refs=%d underlying.closed=%v", sp.refs, underlying.closed)

	release2, freshOK := acquireProvider(sp)
	t.Logf("fresh acquireProvider() after Close() -> %v (refs=%d)", freshOK, sp.refs)
	_, err := sp.KeyMaterial(context.Background())
	t.Logf("fresh KeyMaterial() after Close() -> err=%v underlyingCalls=%d", err, underlying.kmCalls)
	if !freshOK || err != nil {
		t.Fatalf("expected fresh work ADMITTED; acquire=%v err=%v", freshOK, err)
	}
	release2()
	release1()
	t.Logf("after last release: underlying.closed=%v", underlying.closed)
}

// Even with NO remaining reference (underlying already closed), direct KeyMaterial is still forwarded.
func TestVerifyC1_SharedProviderKeyMaterialAfterFullClose(t *testing.T) {
	underlying := &c1CountingProvider{}
	sp := NewSharedProvider(underlying)
	sp.Close()
	_, acqOK := acquireProvider(sp)
	_, err := sp.KeyMaterial(context.Background())
	t.Logf("after full Close: underlying.closed=%v acquire=%v KeyMaterial err=%v underlyingCalls=%d", underlying.closed, acqOK, err, underlying.kmCalls)
	if err == nil && underlying.closed {
		t.Log("CONFIRMED: KeyMaterial forwarded to an already-closed underlying provider")
	}
}
