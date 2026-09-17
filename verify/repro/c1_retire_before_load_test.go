// Run (against branch evalon/grpc-go-xd-0ce5f895 checked out in a worktree):
//   cp verify/repro/c1_retire_before_load_test.go <worktree>/internal/credentials/xds/ && (cd <worktree> && go test -tags verify_repro ./internal/credentials/xds/ -run '^Test$/^VerifyC1_RetireBeforeLoad$' -count=1 -v)
//
// Mirrors the synchronization of TestHandshakeInfoReleaseClosesProvidersAfterLastUser
// on that branch: Acquire(), spawn `go root.KeyMaterial(ctx)`, then Retire()
// immediately, with no event from the KeyMaterial path in between. The
// provider records whether KeyMaterial had been entered at the moment Retire()
// ran. Over many iterations this reports how often the "load in progress
// during replacement" ordering the test describes actually held.

//go:build verify_repro

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

type verifyC1Provider struct {
	entered atomic.Bool
	proceed chan struct{}
	closed  chan struct{}
}

func (p *verifyC1Provider) KeyMaterial(ctx context.Context) (*certprovider.KeyMaterial, error) {
	p.entered.Store(true)
	select {
	case <-p.closed:
		return nil, fmt.Errorf("provider instance is closed")
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-p.proceed:
		return &certprovider.KeyMaterial{Roots: x509.NewCertPool()}, nil
	}
}

func (p *verifyC1Provider) Close() { close(p.closed) }

func (s) TestVerifyC1_RetireBeforeLoad(t *testing.T) {
	const iterations = 2000
	retireBeforeEntered := 0
	for i := 0; i < iterations; i++ {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		root := &verifyC1Provider{proceed: make(chan struct{}), closed: make(chan struct{})}
		hi := NewHandshakeInfo(root, nil, nil, false, "", false, false)
		if !hi.Acquire() {
			t.Fatal("Acquire() on a live HandshakeInfo returned false")
		}
		kmCh := make(chan error, 1)
		go func() {
			_, err := root.KeyMaterial(ctx)
			kmCh <- err
		}()
		// Same as the audited test: Retire() right after spawning the load,
		// with no wait for an event from KeyMaterial.
		enteredAtRetire := root.entered.Load()
		hi.Retire()
		if !enteredAtRetire {
			retireBeforeEntered++
		}
		close(root.proceed)
		if err := <-kmCh; err != nil {
			t.Fatalf("iteration %d: KeyMaterial() = %v", i, err)
		}
		hi.Release()
		cancel()
	}
	t.Logf("Retire() ran BEFORE KeyMaterial() was entered in %d of %d iterations (%.1f%%)", retireBeforeEntered, iterations, 100*float64(retireBeforeEntered)/iterations)
	if retireBeforeEntered == 0 {
		t.Log("ordering held every time on this machine; the claim would then rest on source inspection only")
	}
}
