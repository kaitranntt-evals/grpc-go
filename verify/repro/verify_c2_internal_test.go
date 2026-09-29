// Repro for C2 (branch evalon/grpc-go-en-db4d0728): the synchronous child callback path
// (balancerWrapper.updateClientConnState -> child.UpdateClientConnState -> balancerWrapper.UpdateState)
// acquires the parent mutex es.mu while the child mutex bw.mu is held, which the changed comment on
// balancerWrapper.mu ("do not acquire es.mu while holding mu") prohibits.
// Run: cp verify/repro/verify_c2_internal_test.go balancer/endpointsharding/ && go test ./balancer/endpointsharding -run '^TestVerifyC2_' -race -count=1 -v
package endpointsharding

import (
	"strings"
	"testing"
	"time"

	"google.golang.org/grpc/balancer"
	"google.golang.org/grpc/balancer/base"
	"google.golang.org/grpc/connectivity"
	"google.golang.org/grpc/internal/balancer/stub"
	"google.golang.org/grpc/internal/testutils"
	"google.golang.org/grpc/resolver"
)

func TestVerifyC2_SyncCallbackAcquiresParentMuWhileChildMuHeld(t *testing.T) {
	const hold = 300 * time.Millisecond
	type observation struct {
		childMuHeld       bool
		waitedForParentMu time.Duration
	}
	result := make(chan observation, 1)
	name := strings.ToLower(t.Name())
	stub.Register(name, stub.BalancerFuncs{
		UpdateClientConnState: func(bd *stub.BalancerData, _ balancer.ClientConnState) error {
			bw := bd.ClientConn.(*balancerWrapper)
			var o observation
			if bw.mu.TryLock() {
				bw.mu.Unlock()
			} else {
				o.childMuHeld = true
			}
			// Hold the parent mutex from another goroutine for `hold`, then time the synchronous
			// callback: if it blocks for ~hold, it acquires es.mu while bw.mu is still held.
			bw.es.mu.Lock()
			go func() {
				time.Sleep(hold)
				bw.es.mu.Unlock()
			}()
			start := time.Now()
			bd.ClientConn.UpdateState(balancer.State{ConnectivityState: connectivity.Connecting, Picker: base.NewErrPicker(balancer.ErrNoSubConnAvailable)})
			o.waitedForParentMu = time.Since(start)
			result <- o
			return nil
		},
	})
	tcc := testutils.NewBalancerClientConn(t)
	es := NewBalancer(tcc, balancer.BuildOptions{}, balancer.Get(name).Build, Options{DisableAutoReconnect: true})
	defer es.Close()
	if err := es.UpdateClientConnState(balancer.ClientConnState{ResolverState: resolver.State{Endpoints: []resolver.Endpoint{{Addresses: []resolver.Address{{Addr: "endpoint"}}}}}}); err != nil {
		t.Fatalf("UpdateClientConnState() failed: %v", err)
	}
	o := <-result
	t.Logf("inside synchronous child UpdateClientConnState callback: child bw.mu held=%v; bd.ClientConn.UpdateState blocked %v waiting for es.mu (held elsewhere for %v)", o.childMuHeld, o.waitedForParentMu.Round(time.Millisecond), hold)
	if !o.childMuHeld {
		t.Errorf("child mutex was not held during the synchronous callback")
	}
	if o.waitedForParentMu < hold-50*time.Millisecond {
		t.Errorf("callback did not block on es.mu; nested acquisition not observed")
	}
}
