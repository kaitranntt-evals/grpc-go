// Run (C3/C1/C5, branch evalon/grpc-go-xd-4df8a32a): cp verify/repro/c3_load_entry_not_awaited_test.go <4df8a32a-worktree>/internal/xds/balancer/clusterimpl/ && cd <4df8a32a-worktree> && go test ./internal/xds/balancer/clusterimpl/ -run 'Test/VerifyC3' -count=1 -v
package clusterimpl

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"google.golang.org/grpc/balancer"
	"google.golang.org/grpc/balancer/roundrobin"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/credentials/tls/certprovider"
	xdscreds "google.golang.org/grpc/credentials/xds"
	"google.golang.org/grpc/internal/credentials/xds"
	internalserviceconfig "google.golang.org/grpc/internal/serviceconfig"
	"google.golang.org/grpc/internal/testutils"
	"google.golang.org/grpc/internal/xds/bootstrap"
	"google.golang.org/grpc/internal/xds/testutils/fakeclient"
)

// c3Provider wraps the branch's blockingProvider and records KeyMaterial entry
// (an event the original test does not have).
type c3Provider struct {
	*blockingProvider
	once    sync.Once
	entered chan struct{}
}

func (p *c3Provider) KeyMaterial(ctx context.Context) (*certprovider.KeyMaterial, error) {
	p.once.Do(func() { close(p.entered) })
	return p.blockingProvider.KeyMaterial(ctx)
}

// Replays the exact sequence of TestSecurityConfigUpdate_ProviderKeptAliveDuringHandshake
// ("replace security config"), with every original assertion, and records whether
// validation-root load entry happened before retirement. delay only postpones the
// goroutine's ClientSideTLSConfig call; the original test's assertions still pass.
func (s) TestVerifyC3_RetirementBeforeLoadEntryStillPasses(t *testing.T) {
	var enteredBefore, notEnteredBefore atomic.Int64
	for _, delay := range []time.Duration{0, 200 * time.Millisecond} {
		for i := 0; i < 20; i++ {
			ctx, cancel := context.WithTimeout(context.Background(), defaultTestTimeout)
			providers := map[string]*c3Provider{}
			origBuildProvider := buildProvider
			buildProvider = func(_ map[string]*certprovider.BuildableConfig, _, certName string, _, _ bool) (certprovider.Provider, error) {
				p := &c3Provider{blockingProvider: newBlockingProvider(), entered: make(chan struct{})}
				providers[certName] = p
				return p, nil
			}
			xdsCreds, _ := xdscreds.NewClientCredentials(xdscreds.ClientOptions{FallbackCreds: insecure.NewCredentials()})
			cc := testutils.NewBalancerClientConn(t)
			b := balancer.Get(Name).Build(cc, balancer.BuildOptions{DialCreds: xdsCreds})
			xdsC := fakeclient.NewClient()
			xdsC.SetBootstrapConfig(&bootstrap.Config{})
			lbCfg := &LBConfig{Cluster: testClusterName, ChildPolicy: &internalserviceconfig.BalancerConfig{Name: roundrobin.Name}}
			if err := b.UpdateClientConnState(balancer.ClientConnState{ResolverState: securityTestState(xdsC, "root-1"), BalancerConfig: lbCfg}); err != nil {
				t.Fatal(err)
			}
			root1 := providers["root-1"]
			addrs := <-cc.NewSubConnAddrsCh
			hiPtr := xds.HandshakeInfoFromAttributes(addrs[0].Attributes)
			hi := hiPtr.Load()
			if !hi.Acquire() {
				t.Fatal("Acquire() returned false")
			}
			tlsCfgErrCh := make(chan error, 1)
			go func() {
				time.Sleep(delay)
				_, err := hi.ClientSideTLSConfig(ctx, "")
				tlsCfgErrCh <- err
			}()
			// Original test: retire immediately after launching the goroutine.
			if err := b.UpdateClientConnState(balancer.ClientConnState{ResolverState: securityTestState(xdsC, "root-2"), BalancerConfig: lbCfg}); err != nil {
				t.Fatal(err)
			}
			select {
			case <-root1.entered:
				enteredBefore.Add(1)
			default:
				notEnteredBefore.Add(1)
			}
			// Original assertions, unchanged.
			if root1.isClosed() {
				t.Fatal("root provider closed while a handshake using it was still in progress")
			}
			close(root1.unblock)
			if err := <-tlsCfgErrCh; err != nil {
				t.Fatalf("ClientSideTLSConfig() failed: %v", err)
			}
			if root1.isClosed() {
				t.Fatal("root provider closed before the handshake released it")
			}
			hi.Release()
			if !root1.isClosed() {
				t.Fatal("root provider not closed after the last handshake released it")
			}
			if hi.Acquire() {
				t.Fatal("Acquire() on a retired HandshakeInfo returned true")
			}
			b.Close()
			buildProvider = origBuildProvider
			cancel()
		}
		t.Logf("delay=%v: iterations where validation-root load had entered before retirement=%d, had NOT entered=%d (all original assertions passed)", delay, enteredBefore.Swap(0), notEnteredBefore.Swap(0))
	}
}
