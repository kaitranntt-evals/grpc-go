//go:build ignore

// Run: copy this file (without the go:build line) into a checkout of evalon/grpc-go-xd-d150a14b as internal/credentials/xds/c1_d150a14b_reload_after_failed_hold_test.go,
// then run: go test ./internal/credentials/xds -run 'TestVerify_C1' -count=1 -v
package xds

import (
	"context"
	"crypto/x509"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"google.golang.org/grpc/credentials/tls/certprovider"
)

// closableProvider fails KeyMaterial once Close has been called.
type closableProvider struct {
	closed       atomic.Bool
	kmCalls      atomic.Int64
	kmAfterClose atomic.Int64
}

func (p *closableProvider) KeyMaterial(context.Context) (*certprovider.KeyMaterial, error) {
	p.kmCalls.Add(1)
	if p.closed.Load() {
		p.kmAfterClose.Add(1)
		return nil, errors.New("provider instance is closed")
	}
	return &certprovider.KeyMaterial{Roots: x509.NewCertPool()}, nil
}
func (p *closableProvider) Close() { p.closed.Store(true) }

// acquireNoReload is the hypothesised buggy variant of AcquireHandshakeInfo:
// it does NOT re-read hiPtr after a failed hold and returns the loaded
// (possibly released) HandshakeInfo as-is. Used only to show that the stress
// test below can distinguish "reloads" from "does not reload".
func acquireNoReload(hiPtr *atomic.Pointer[HandshakeInfo]) (*HandshakeInfo, func()) {
	hi := hiPtr.Load()
	if hi == nil {
		return nil, func() {}
	}
	if hi.tryRef() {
		return hi, hi.Release
	}
	return hi, func() {}
}

func runC1Stress(t *testing.T, name string, acquire func(*atomic.Pointer[HandshakeInfo]) (*HandshakeInfo, func())) (closedReturned int64) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	var hiPtr atomic.Pointer[HandshakeInfo]
	newHI := func() *HandshakeInfo {
		return NewHandshakeInfo(&closableProvider{}, nil, nil, false, "", false, false)
	}
	hiPtr.Store(newHI())

	stop := make(chan struct{})
	var swaps atomic.Int64
	var swapWG, wg sync.WaitGroup
	swapWG.Add(1)
	go func() {
		defer swapWG.Done()
		for {
			select {
			case <-stop:
				return
			default:
			}
			// clusterImplBalancer.storeHandshakeInfo: Swap first, then Release.
			old := hiPtr.Swap(newHI())
			old.Release()
			swaps.Add(1)
		}
	}()

	var total, badKM atomic.Int64
	const workers, iters = 8, 30000
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < iters; i++ {
				hi, release := acquire(&hiPtr)
				total.Add(1)
				// What ClientSideTLSConfig does next: read KeyMaterial.
				_, err := hi.rootProvider.KeyMaterial(ctx)
				if err != nil {
					badKM.Add(1)
				}
				release()
			}
		}()
	}
	wg.Wait()
	close(stop)
	swapWG.Wait()
	t.Logf("[%s] acquisitions=%d owner swaps=%d KeyMaterial-read-from-CLOSED-provider=%d", name, total.Load(), swaps.Load(), badKM.Load())
	return badKM.Load()
}

// C1 on d150a14b: does a handshake whose hold on the loaded HandshakeInfo
// fails re-read the published pointer before reading KeyMaterial?
// If it did not, some acquisitions would return the released HandshakeInfo
// and KeyMaterial would be read from a closed provider.
func TestVerify_C1_d150_ReloadAfterFailedHold(t *testing.T) {
	real := runC1Stress(t, "AcquireHandshakeInfo (production)", AcquireHandshakeInfo)
	mut := runC1Stress(t, "acquireNoReload (hypothesised bug)", acquireNoReload)
	if mut == 0 {
		t.Log("mutant did not surface any closed reads; stress not sensitive enough on this machine")
	}
	if real != 0 {
		t.Fatalf("production AcquireHandshakeInfo returned a released HandshakeInfo %d times: C1 CONFIRMED", real)
	}
	t.Logf("production: 0 closed reads; mutant without reload: %d closed reads -> reload after failed hold is observed", mut)
}

// Deterministic corner: the owner has shut down (Close with no replacement,
// pointer still points at the released HandshakeInfo). The re-read finds the
// same pointer and the released HandshakeInfo is returned; the handshake then
// reads KeyMaterial from the closed provider and fails.
func TestVerify_C1_d150_OwnerClosedNoReplacement(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	prov := &closableProvider{}
	hi := NewHandshakeInfo(prov, nil, nil, false, "", false, false)
	var hiPtr atomic.Pointer[HandshakeInfo]
	hiPtr.Store(hi)
	hi.Release() // clusterImplBalancer.Close(): b.xdsHIPtr.Load().Release()
	got, release := AcquireHandshakeInfo(&hiPtr)
	defer release()
	t.Logf("provider closed before acquire: %v; returned same released hi: %v", prov.closed.Load(), got == hi)
	_, err := got.ClientSideTLSConfig(ctx, "")
	t.Logf("ClientSideTLSConfig err = %v; KeyMaterial calls after Close = %d", err, prov.kmAfterClose.Load())
	if err == nil {
		t.Fatal("expected handshake failure on closed provider")
	}
}
