//go:build ignore

// Run: copy this file (without the go:build line) into a checkout of evalon/grpc-go-xd-d150a14b as credentials/xds/c1_d150a14b_client_handshake_stress_test.go,
// then run: go test ./credentials/xds -run 'TestVerify_C1_ClientHandshakeStress' -count=1 -v
package xds

import (
	"context"
	"errors"
	"net"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/credentials/tls/certprovider"
	icredentials "google.golang.org/grpc/internal/credentials"
	xdsinternal "google.golang.org/grpc/internal/credentials/xds"
	"google.golang.org/grpc/resolver"
)

var errVerifyOpenKM = errors.New("verify: KeyMaterial read from OPEN provider")

type verifyClosableProvider struct{ closed atomic.Bool }

func (p *verifyClosableProvider) KeyMaterial(context.Context) (*certprovider.KeyMaterial, error) {
	if p.closed.Load() {
		return nil, errors.New("verify: KeyMaterial read from CLOSED provider")
	}
	return nil, errVerifyOpenKM
}
func (p *verifyClosableProvider) Close() { p.closed.Store(true) }

// Real credsImpl.ClientHandshake racing an owner that does
// Swap(new) + old.Release() (clusterImplBalancer.storeHandshakeInfo). Counts
// whether any handshake read KeyMaterial from a provider that was already
// closed, i.e. used a HandshakeInfo it failed to hold without re-reading.
func TestVerify_C1_ClientHandshakeStress(t *testing.T) {
	creds, err := NewClientCredentials(ClientOptions{FallbackCreds: insecure.NewCredentials()})
	if err != nil {
		t.Fatal(err)
	}
	newHI := func() *xdsinternal.HandshakeInfo {
		return xdsinternal.NewHandshakeInfo(&verifyClosableProvider{}, nil, nil, false, "", false, false)
	}
	var hiPtr atomic.Pointer[xdsinternal.HandshakeInfo]
	hiPtr.Store(newHI())
	addr := xdsinternal.SetHandshakeInfo(resolver.Address{}, &hiPtr)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	hsCtx := icredentials.NewClientHandshakeInfoContext(ctx, credentials.ClientHandshakeInfo{Attributes: addr.Attributes})

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
			old := hiPtr.Swap(newHI())
			old.Release()
			swaps.Add(1)
		}
	}()

	var open, closedReads, other atomic.Int64
	const workers, iters = 8, 20000
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < iters; i++ {
				c1, c2 := net.Pipe()
				_, _, err := creds.ClientHandshake(hsCtx, "authority", c1)
				c1.Close()
				c2.Close()
				switch {
				case err != nil && strings.Contains(err.Error(), "OPEN provider"):
					open.Add(1)
				case err != nil && strings.Contains(err.Error(), "CLOSED provider"):
					closedReads.Add(1)
				default:
					other.Add(1)
					t.Errorf("unexpected handshake result: %v", err)
				}
			}
		}()
	}
	wg.Wait()
	close(stop)
	swapWG.Wait()
	t.Logf("handshakes=%d owner swaps=%d", workers*iters, swaps.Load())
	t.Logf("KeyMaterial read from OPEN (held) provider: %d", open.Load())
	t.Logf("KeyMaterial read from CLOSED (unheld) provider: %d", closedReads.Load())
	if closedReads.Load() != 0 {
		t.Fatalf("C1 CONFIRMED on this branch: %d reads from closed provider", closedReads.Load())
	}
}
