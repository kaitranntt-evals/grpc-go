// Run (from a checkout of evalon/grpc-go-xd-a31d723d):
//   cp verify/repro/c3_paused_release_test.go internal/credentials/xds/ && go test ./internal/credentials/xds -run '^Test$/^C3PausedRelease_OwnerClosesDuringRootLoad$' -count=1 -v
//
// This is TestAcquireHandshakeInfo_OwnerClosesDuringRootLoad from
// internal/credentials/xds/handshake_info_test.go with ONE change: the
// handshake goroutine's deferred release() is paused for 50ms after the result
// has been sent on cfgCh (the Go scheduler is free to introduce this pause on
// its own). The test's closure assertion runs as soon as cfgCh is readable, so
// it observes the providers still open and fails deterministically.

package xds

import (
	"context"
	"crypto/x509"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"google.golang.org/grpc/credentials/tls/certprovider"
)

type c3BlockingProvider struct {
	loading chan struct{}
	unblock chan struct{}
	closed  chan struct{}
}

func newC3BlockingProvider() *c3BlockingProvider {
	return &c3BlockingProvider{
		loading: make(chan struct{}),
		unblock: make(chan struct{}),
		closed:  make(chan struct{}),
	}
}

func (p *c3BlockingProvider) KeyMaterial(ctx context.Context) (*certprovider.KeyMaterial, error) {
	select {
	case <-p.loading:
	default:
		close(p.loading)
	}
	select {
	case <-p.closed:
		return nil, fmt.Errorf("provider instance is closed")
	default:
	}
	select {
	case <-p.unblock:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	return &certprovider.KeyMaterial{Roots: x509.NewCertPool()}, nil
}

func (p *c3BlockingProvider) Close() { close(p.closed) }

func (p *c3BlockingProvider) isClosed() bool {
	select {
	case <-p.closed:
		return true
	default:
		return false
	}
}

func (s) TestC3PausedRelease_OwnerClosesDuringRootLoad(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	oldRoot, oldID := newC3BlockingProvider(), newC3BlockingProvider()
	close(oldID.unblock)
	oldHI := NewHandshakeInfo(oldRoot, oldID, nil, false, "", false, false)
	var hiPtr atomic.Pointer[HandshakeInfo]
	hiPtr.Store(oldHI)

	hi, release := AcquireHandshakeInfo(&hiPtr)
	if hi != oldHI {
		t.Fatalf("AcquireHandshakeInfo() returned %p, want %p", hi, oldHI)
	}
	cfgCh := make(chan error, 1)
	go func() {
		// Identical to the candidate test's `defer release()`, except the
		// goroutine is paused between sending the result and releasing.
		defer func() {
			time.Sleep(50 * time.Millisecond)
			release()
		}()
		_, err := hi.ClientSideTLSConfig(ctx, "")
		cfgCh <- err
	}()
	select {
	case <-oldRoot.loading:
	case <-ctx.Done():
		t.Fatal("Timed out waiting for the root provider to be queried")
	}

	newRoot := newC3BlockingProvider()
	close(newRoot.unblock)
	newHI := NewHandshakeInfo(newRoot, nil, nil, false, "", false, false)
	hiPtr.Swap(newHI).Close()

	sCtx, sCancel := context.WithTimeout(ctx, 10*time.Millisecond)
	defer sCancel()
	select {
	case <-oldRoot.closed:
		t.Fatal("Root provider closed while a handshake was still loading from it")
	case <-oldID.closed:
		t.Fatal("Identity provider closed while a handshake was still using it")
	case <-sCtx.Done():
	}

	close(oldRoot.unblock)
	select {
	case err := <-cfgCh:
		if err != nil {
			t.Fatalf("ClientSideTLSConfig() failed after configuration was replaced: %v", err)
		}
	case <-ctx.Done():
		t.Fatal("Timed out waiting for the handshake to finish")
	}
	// Same assertion, same position as handshake_info_test.go:727.
	if !oldRoot.isClosed() || !oldID.isClosed() {
		t.Fatalf("Replaced providers not closed after the last handshake released them: root closed = %v, identity closed = %v", oldRoot.isClosed(), oldID.isClosed())
	}
}
