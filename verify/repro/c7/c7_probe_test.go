// Run: bash verify/repro/c7/run.sh   (instruments a worktree of evalon/grpc-go-xd-b7c0f5e6 with a hook between Close's unlock and closeProvider, then runs this test)
//go:build verifyrepro

package certprovider

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

type c7Provider struct {
	entered chan struct{}
	release chan struct{}
	closed  atomic.Bool
}

func (p *c7Provider) KeyMaterial(ctx context.Context) (*KeyMaterial, error) {
	close(p.entered)
	select {
	case <-p.release:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	if p.closed.Load() {
		return nil, errors.New("underlying provider was closed while this KeyMaterial call was in progress")
	}
	return &KeyMaterial{}, nil
}
func (p *c7Provider) Close() { p.closed.Store(true) }

// TestC7Probe_CloseAdmitsLoadThenClosesUnderneath drives the interleaving:
// Close observes loads==0 and unlocks; before closeProvider runs, a KeyMaterial
// call is admitted (loads++) and enters the underlying provider; closeProvider
// then closes the underlying provider while that load is still in flight.
func (s) TestC7Probe_CloseAdmitsLoadThenClosesUnderneath(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	inner := &c7Provider{entered: make(chan struct{}), release: make(chan struct{})}
	w := newSingleCloseWrappedProvider(inner)

	type res struct {
		km  *KeyMaterial
		err error
	}
	resCh := make(chan res, 1)
	C7TestHookBeforeCloseProvider = func() {
		// Close has observed zero active loads and dropped the lock. Admit a
		// load now, and wait until it is inside the underlying provider.
		go func() {
			km, err := w.KeyMaterial(ctx)
			resCh <- res{km, err}
		}()
		select {
		case <-inner.entered:
		case <-ctx.Done():
			t.Error("timed out waiting for KeyMaterial to enter the underlying provider")
		}
	}
	defer func() { C7TestHookBeforeCloseProvider = nil }()

	w.Close()
	closedDuringLoad := inner.closed.Load()
	t.Logf("C7PROBE after Close() returned: underlying provider closed=%v while KeyMaterial still in progress", closedDuringLoad)
	close(inner.release)
	r := <-resCh
	t.Logf("C7PROBE admitted KeyMaterial returned km=%v err=%v", r.km, r.err)
	w.mu.Lock()
	t.Logf("C7PROBE wrapper state: loads=%d closePending=%v", w.loads, w.closePending)
	w.mu.Unlock()
	if closedDuringLoad || r.err != nil {
		t.Errorf("C7PROBE RESULT: a KeyMaterial call admitted after Close observed zero loads had its provider closed underneath it")
	} else {
		t.Logf("C7PROBE RESULT: load completed before the underlying provider was closed")
	}
}
