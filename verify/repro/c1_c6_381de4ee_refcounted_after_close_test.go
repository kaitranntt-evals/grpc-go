// Run: cp this file into internal/xds/balancer/clusterimpl/ of branch evalon/grpc-go-xd-381de4ee, then `go test ./internal/xds/balancer/clusterimpl -run TestVerifyC1_ -v -count=1`
package clusterimpl

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

// TestVerifyC1_AcquireAndKeyMaterialAdmittedAfterClose: Close() while a handshake
// reference remains, then start a FRESH Acquire and a FRESH KeyMaterial.
func TestVerifyC1_AcquireAndKeyMaterialAdmittedAfterClose(t *testing.T) {
	underlying := &c1CountingProvider{}
	p := newRefCountedProvider(underlying)
	if !p.Acquire() { // previously admitted handshake reference
		t.Fatal("initial Acquire failed")
	}
	p.Close() // cache owner initiates shutdown
	t.Logf("after Close: refs=%d underlying.closed=%v", p.refs.Load(), underlying.closed)

	freshAcquire := p.Acquire()
	t.Logf("fresh Acquire() after Close() -> %v (refs=%d)", freshAcquire, p.refs.Load())
	if freshAcquire {
		p.Release()
	}
	_, err := p.KeyMaterial(context.Background())
	t.Logf("fresh KeyMaterial() after Close() -> err=%v underlyingCalls=%d", err, underlying.kmCalls)
	if !freshAcquire || err != nil {
		t.Fatalf("expected fresh work to be ADMITTED after Close while another ref remains; acquire=%v err=%v", freshAcquire, err)
	}
	p.Release() // last handshake ref
	t.Logf("after last Release: underlying.closed=%v", underlying.closed)
}

// TestVerifyC1_RefusedOnlyAfterLastRelease: with no remaining reference, fresh work is refused.
func TestVerifyC1_RefusedOnlyAfterLastRelease(t *testing.T) {
	underlying := &c1CountingProvider{}
	p := newRefCountedProvider(underlying)
	p.Close()
	_, err := p.KeyMaterial(context.Background())
	t.Logf("KeyMaterial after Close with no other ref -> err=%v acquire=%v closed=%v", err, p.Acquire(), underlying.closed)
	if err == nil {
		t.Fatal("expected refusal")
	}
}
