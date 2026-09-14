//go:build ignore

// Run: copy this file (without the go:build line) into a checkout of evalon/grpc-go-xd-808a7060 as internal/credentials/xds/c2_808a7060_second_acquire_aborts_test.go,
// then run: go test ./internal/credentials/xds -run 'TestVerify_' -count=1 -v
package xds

import (
	"context"
	"crypto/x509"
	"sync/atomic"
	"testing"
	"time"

	"google.golang.org/grpc/credentials/tls/certprovider"
)

// countingProvider records KeyMaterial and Close calls.
type countingProvider struct {
	kmCalls    atomic.Int32
	closeCalls atomic.Int32
	kmErr      error
}

func (p *countingProvider) KeyMaterial(context.Context) (*certprovider.KeyMaterial, error) {
	p.kmCalls.Add(1)
	if p.kmErr != nil {
		return nil, p.kmErr
	}
	return &certprovider.KeyMaterial{Roots: x509.NewCertPool()}, nil
}

func (p *countingProvider) Close() { p.closeCalls.Add(1) }

// C2 / C1(808a7060): ClientHandshake acquires the old HandshakeInfo, the
// balancer retires it (Swap + Close) before ClientSideTLSConfig runs, and
// ClientSideTLSConfig performs a second Acquire.
func TestVerify_C2_SecondAcquireRejectsRetiredHandshakeInfo(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	root := &countingProvider{}
	hi := NewHandshakeInfo(root, nil, nil, false, "", false, false)

	// Step 1: what credentials/xds.ClientHandshake does after loading hiPtr.
	release, ok := hi.Acquire()
	if !ok {
		t.Fatal("first Acquire() failed on a fresh HandshakeInfo")
	}
	defer release()

	// Step 2: what clusterImplBalancer.swapHandshakeInfo does on replacement:
	// old := xdsHIPtr.Swap(new); old.Close()
	hi.Close()
	t.Logf("after owner Close(): provider Close() calls = %d (expect 0, handshake still holds a ref)", root.closeCalls.Load())

	// Step 3: what ClientHandshake does next: hi.ClientSideTLSConfig(ctx, hostname).
	cfg, err := hi.ClientSideTLSConfig(ctx, "")
	t.Logf("ClientSideTLSConfig() -> cfg=%v err=%v", cfg != nil, err)
	t.Logf("root provider KeyMaterial() calls = %d", root.kmCalls.Load())
	if err == nil {
		t.Fatalf("ClientSideTLSConfig() succeeded; C2 REFUTED (handshake kept and used protected old material)")
	}
	if root.kmCalls.Load() != 0 {
		t.Fatalf("KeyMaterial was read before the abort; C2 REFUTED")
	}
	t.Logf("CONFIRMED: handshake holding a valid ref aborted with %q before reading KeyMaterial", err)
}

// Same as above, but through the exported (retired-and-acquired) path with a
// second, fresh HandshakeInfo published in the pointer, showing that the
// abort happens even though a replacement is available and could have been
// re-read: ClientSideTLSConfig has no access to the pointer, it just fails.
func TestVerify_C1_808a_AbortWithoutReload(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	oldRoot := &countingProvider{}
	newRoot := &countingProvider{}
	oldHI := NewHandshakeInfo(oldRoot, nil, nil, false, "", false, false)
	newHI := NewHandshakeInfo(newRoot, nil, nil, false, "", false, false)
	var hiPtr atomic.Pointer[HandshakeInfo]
	hiPtr.Store(oldHI)

	release, ok := oldHI.Acquire()
	if !ok {
		t.Fatal("Acquire failed")
	}
	defer release()
	// Replacement is published before the old one is retired (balancer order).
	if got := hiPtr.Swap(newHI); got != oldHI {
		t.Fatal("unexpected swap result")
	}
	oldHI.Close()

	_, err := oldHI.ClientSideTLSConfig(ctx, "")
	t.Logf("ClientSideTLSConfig(old) err = %v", err)
	t.Logf("old KeyMaterial calls = %d, new KeyMaterial calls = %d", oldRoot.kmCalls.Load(), newRoot.kmCalls.Load())
	if err == nil || newRoot.kmCalls.Load() != 0 || oldRoot.kmCalls.Load() != 0 {
		t.Fatalf("unexpected: err=%v old=%d new=%d", err, oldRoot.kmCalls.Load(), newRoot.kmCalls.Load())
	}
}
