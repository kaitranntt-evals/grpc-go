//go:build ignore

// Run: copy this file (without the go:build line) into a checkout of evalon/grpc-go-xd-808a7060 as credentials/xds/c2_808a7060_client_handshake_stress_test.go,
// then run: go test ./credentials/xds -run 'TestVerify_C2_ClientHandshakeStress' -count=1 -v
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

var errVerifyKM = errors.New("verify: KeyMaterial sentinel")

type verifyFastFailProvider struct{ kmCalls atomic.Int64 }

func (p *verifyFastFailProvider) KeyMaterial(context.Context) (*certprovider.KeyMaterial, error) {
	p.kmCalls.Add(1)
	return nil, errVerifyKM
}
func (p *verifyFastFailProvider) Close() {}

// Drives the real credsImpl.ClientHandshake concurrently with an owner that
// keeps replacing (Swap + Close) the published HandshakeInfo, exactly as
// clusterImplBalancer.swapHandshakeInfo does. Every handshake either reads
// KeyMaterial (sentinel error) or aborts with the "replaced before the TLS
// handshake could start" error produced by the second Acquire in
// ClientSideTLSConfig. A nonzero abort count means C2 is reachable through
// the production entry point.
func TestVerify_C2_ClientHandshakeStress(t *testing.T) {
	creds, err := NewClientCredentials(ClientOptions{FallbackCreds: insecure.NewCredentials()})
	if err != nil {
		t.Fatal(err)
	}
	prov := &verifyFastFailProvider{}
	var hiPtr atomic.Pointer[xdsinternal.HandshakeInfo]
	hiPtr.Store(xdsinternal.NewHandshakeInfo(prov, nil, nil, false, "", false, false))
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
			old := hiPtr.Swap(xdsinternal.NewHandshakeInfo(prov, nil, nil, false, "", false, false))
			old.Close()
			swaps.Add(1)
		}
	}()

	var kmErrs, replacedErrs, other atomic.Int64
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
				case err != nil && strings.Contains(err.Error(), errVerifyKM.Error()):
					kmErrs.Add(1)
				case err != nil && strings.Contains(err.Error(), "security configuration was replaced before the TLS handshake could start"):
					replacedErrs.Add(1)
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
	t.Logf("handshakes=%d  owner swaps=%d", workers*iters, swaps.Load())
	t.Logf("reached KeyMaterial (sentinel err): %d", kmErrs.Load())
	t.Logf("aborted by second Acquire on retired state (\"replaced before the TLS handshake could start\"): %d", replacedErrs.Load())
	t.Logf("other: %d", other.Load())
	if replacedErrs.Load() == 0 {
		t.Log("no abort observed in this run (window not hit); unit test TestVerify_C2_SecondAcquireRejectsRetiredHandshakeInfo is deterministic")
	}
}
