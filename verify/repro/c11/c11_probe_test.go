// Run: cp verify/repro/c11/c11_probe_test.go ~/wt/8f85e742/internal/credentials/xds/ && (cd ~/wt/8f85e742 && go test -tags verifyrepro ./internal/credentials/xds -run 'Test/C11Probe' -count=1 -v)
//go:build verifyrepro

package xds

import (
	"context"
	"sync/atomic"
	"testing"

	"google.golang.org/grpc/credentials/tls/certprovider"
)

type c11Provider struct{ closeCount atomic.Int32 }

func (p *c11Provider) KeyMaterial(context.Context) (*certprovider.KeyMaterial, error) {
	return &certprovider.KeyMaterial{}, nil
}
func (p *c11Provider) Close() { p.closeCount.Add(1) }

// TestC11Probe_UnderflowThenAcquire releases the owner reference (count 1->0,
// providers closed), then performs one extra Release (underflow to -1) and
// checks whether a subsequent Acquire succeeds on the retired HandshakeInfo.
func (s) TestC11Probe_UnderflowThenAcquire(t *testing.T) {
	rp := &c11Provider{}
	hi := NewHandshakeInfo(rp, nil, nil, false, "", false, false)
	t.Logf("C11PROBE initial refCount=%d", hi.refCount.Load())

	hi.Release()
	t.Logf("C11PROBE after owner Release: refCount=%d rootProvider.closeCount=%d", hi.refCount.Load(), rp.closeCount.Load())
	acqAtZero := hi.Acquire()
	t.Logf("C11PROBE Acquire at refCount 0 -> %v (refCount now %d)", acqAtZero, hi.refCount.Load())

	hi.Release() // extra release: underflow
	t.Logf("C11PROBE after extra Release (underflow): refCount=%d rootProvider.closeCount=%d", hi.refCount.Load(), rp.closeCount.Load())
	acqAfterUnderflow := hi.Acquire()
	t.Logf("C11PROBE Acquire after underflow -> %v (refCount now %d)", acqAfterUnderflow, hi.refCount.Load())
	if acqAfterUnderflow {
		t.Errorf("C11PROBE RESULT: Acquire succeeded on a HandshakeInfo whose providers were already closed (closeCount=%d) after a refcount underflow", rp.closeCount.Load())
	} else {
		t.Logf("C11PROBE RESULT: Acquire refused after underflow")
	}
	if acqAtZero {
		t.Errorf("C11PROBE Acquire at zero unexpectedly succeeded")
	}
}
