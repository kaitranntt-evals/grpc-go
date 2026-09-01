// Run: cp verify/repro/c9/c9_probe_test.go ~/wt/69c43750/credentials/tls/certprovider/ && (cd ~/wt/69c43750 && go test -tags verifyrepro ./credentials/tls/certprovider -run 'Test/C9Probe' -count=1 -v)
//go:build verifyrepro

package certprovider

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

type c9Provider struct {
	km      *KeyMaterial
	kmCalls atomic.Int32
	closed  atomic.Bool
}

func (p *c9Provider) KeyMaterial(context.Context) (*KeyMaterial, error) {
	p.kmCalls.Add(1)
	if p.closed.Load() {
		return nil, errors.New("underlying provider closed")
	}
	return p.km, nil
}
func (p *c9Provider) Close() { p.closed.Store(true) }

// TestC9Probe_CachedMaterialServedAfterClose loads key material once through
// the wrapper, closes the wrapper with no loads in progress, and then makes a
// brand-new KeyMaterial call.
func (s) TestC9Probe_CachedMaterialServedAfterClose(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	inner := &c9Provider{km: &KeyMaterial{}}
	w := newSingleCloseWrappedProvider(inner)

	if _, err := w.KeyMaterial(ctx); err != nil {
		t.Fatalf("first KeyMaterial: %v", err)
	}
	w.Close()
	t.Logf("C9PROBE after Close(): underlying closed=%v (Close completed, no load in progress)", inner.closed.Load())

	km, err := w.KeyMaterial(ctx)
	t.Logf("C9PROBE new KeyMaterial call after Close: km=%p err=%v (errProviderClosed=%v); underlying KeyMaterial calls=%d", km, err, errors.Is(err, errProviderClosed), inner.kmCalls.Load())
	switch {
	case km != nil && km == inner.km && err == nil:
		t.Errorf("C9PROBE RESULT: wrapper served cached key material to a new caller after Close completed")
	case errors.Is(err, errProviderClosed):
		t.Logf("C9PROBE RESULT: new caller after Close got errProviderClosed")
	default:
		t.Logf("C9PROBE RESULT: unexpected outcome")
	}
}
