// Run (against branch evalon/grpc-go-xd-6b4db8a0 in a worktree):
//   cp verify/repro/c2_acquire_stress_manager_test.go <worktree>/internal/credentials/xds/ && (cd <worktree> && go test -race -tags verify_repro ./internal/credentials/xds/ -run '^Test$/^VerifyC2_AcquireNeverFailsWhilePublished$' -count=1 -v)
//
// Probe for C2: while a publisher goroutine keeps replacing the configuration
// via HandshakeInfoManager.Update, many handshake goroutines call Acquire. A
// "failed initial hold" would show up as Acquire returning nil while a non-nil
// configuration is always published, or returning a HandshakeInfo whose root
// provider is already closed (or gets closed before release is called).

//go:build verify_repro

package xds

import (
	"context"
	"crypto/x509"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"google.golang.org/grpc/credentials/tls/certprovider"
)

type verifyC2Provider struct {
	closed atomic.Bool
}

func (p *verifyC2Provider) KeyMaterial(context.Context) (*certprovider.KeyMaterial, error) {
	return &certprovider.KeyMaterial{Roots: x509.NewCertPool()}, nil
}
func (p *verifyC2Provider) Close() { p.closed.Store(true) }

func (s) TestVerifyC2_AcquireNeverFailsWhilePublished(t *testing.T) {
	var m HandshakeInfoManager
	m.Update(NewHandshakeInfo(&verifyC2Provider{}, nil, nil, false, "", false, false))

	var (
		nilHolds, closedAtAcquire, closedBeforeRelease, holds atomic.Int64
		stop                                                  = make(chan struct{})
		wg                                                    sync.WaitGroup
	)
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				hi, release := m.Acquire()
				holds.Add(1)
				if hi == nil {
					nilHolds.Add(1)
					release()
					continue
				}
				p := hi.rootProvider.(*verifyC2Provider)
				if p.closed.Load() {
					closedAtAcquire.Add(1)
				}
				_, _ = hi.rootProvider.KeyMaterial(context.Background())
				if p.closed.Load() {
					closedBeforeRelease.Add(1)
				}
				release()
			}
		}()
	}
	replacements := 0
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		m.Update(NewHandshakeInfo(&verifyC2Provider{}, nil, nil, false, "", false, false))
		replacements++
	}
	close(stop)
	wg.Wait()
	t.Logf("replacements=%d acquires=%d nilHolds=%d closedAtAcquire=%d closedBeforeRelease=%d", replacements, holds.Load(), nilHolds.Load(), closedAtAcquire.Load(), closedBeforeRelease.Load())
	if nilHolds.Load() != 0 || closedAtAcquire.Load() != 0 || closedBeforeRelease.Load() != 0 {
		t.Fatal("observed a failed initial hold of a published configuration")
	}
}
