// Run: cp this file into internal/credentials/xds/ of branch evalon/grpc-go-xd-7f9f59d2, then `go test ./internal/credentials/xds -run TestVerifyC8_ -v -count=1`
package xds

import (
	"context"
	"testing"

	"google.golang.org/grpc/credentials/tls/certprovider"
)

// c8Provider's Close callback probes whether hi.mu is held when it runs.
type c8Provider struct {
	hi            *HandshakeInfo
	closeCalls    int
	muHeldAtClose bool
}

func (p *c8Provider) KeyMaterial(context.Context) (*certprovider.KeyMaterial, error) {
	return &certprovider.KeyMaterial{}, nil
}

func (p *c8Provider) Close() {
	p.closeCalls++
	if p.hi.mu.TryLock() {
		p.hi.mu.Unlock()
		return
	}
	p.muHeldAtClose = true
}

// Owner Close with no active handshake: providers closed inside Close.
func TestVerifyC8_CloseInvokesProviderCloseUnderMu(t *testing.T) {
	root, id := &c8Provider{}, &c8Provider{}
	hi := NewHandshakeInfo(root, id, nil, false, "", false, false)
	root.hi, id.hi = hi, hi
	hi.Close()
	t.Logf("Close path: root.Close calls=%d muHeld=%v; identity.Close calls=%d muHeld=%v", root.closeCalls, root.muHeldAtClose, id.closeCalls, id.muHeldAtClose)
	if root.closeCalls != 1 || id.closeCalls != 1 {
		t.Fatalf("providers not closed exactly once")
	}
	if !root.muHeldAtClose || !id.muHeldAtClose {
		t.Fatal("REFUTED: hi.mu was released before provider Close callbacks")
	}
}

// Owner Close while a handshake is active, then final Release: providers closed inside Release.
func TestVerifyC8_FinalReleaseInvokesProviderCloseUnderMu(t *testing.T) {
	root, id := &c8Provider{}, &c8Provider{}
	hi := NewHandshakeInfo(root, id, nil, false, "", false, false)
	root.hi, id.hi = hi, hi
	if !hi.Acquire() {
		t.Fatal("Acquire failed")
	}
	hi.Close()
	t.Logf("after Close with active handshake: root.Close calls=%d", root.closeCalls)
	hi.Release()
	t.Logf("Release path: root.Close calls=%d muHeld=%v; identity.Close calls=%d muHeld=%v", root.closeCalls, root.muHeldAtClose, id.closeCalls, id.muHeldAtClose)
	if root.closeCalls != 1 || id.closeCalls != 1 {
		t.Fatalf("providers not closed exactly once")
	}
	if !root.muHeldAtClose || !id.muHeldAtClose {
		t.Fatal("REFUTED: hi.mu was released before provider Close callbacks")
	}
}
