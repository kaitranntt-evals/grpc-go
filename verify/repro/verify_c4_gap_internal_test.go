// Repro for C4 part 1 (branch evalon/grpc-go-en-93bc5f0b): the child mutex childMu is released after the
// builder returns and only re-acquired for the initial UpdateClientConnState, so an independent goroutine
// can acquire it in between (configured=false at acquisition) and the initial configuration then waits for it.
// Run: cp verify/repro/verify_c4_gap_internal_test.go balancer/endpointsharding/ && go test ./balancer/endpointsharding -run '^TestVerifyC4Gap_' -race -count=1 -v
package endpointsharding

import (
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"google.golang.org/grpc/balancer"
	"google.golang.org/grpc/internal/balancer/stub"
	"google.golang.org/grpc/internal/testutils"
	"google.golang.org/grpc/resolver"
)

func TestVerifyC4Gap_ChildMuReleasedBetweenBuildAndInitialConfig(t *testing.T) {
	const iterations = 20
	gapHits := 0
	for i := 0; i < iterations; i++ {
		var configured atomic.Bool
		var heldDuringBuild, heldDuringConfig atomic.Bool
		acquired := make(chan bool, 1) // value: configured at the time the probe acquired childMu
		probeReleased := make(chan time.Time, 1)
		configStarted := make(chan time.Time, 1)
		name := strings.ToLower(t.Name()) + fmt.Sprintf("-%d", i)
		stub.Register(name, stub.BalancerFuncs{
			Init: func(bd *stub.BalancerData) {
				bw := bd.ClientConn.(*balancerWrapper)
				if bw.childMu.TryLock() {
					bw.childMu.Unlock()
				} else {
					heldDuringBuild.Store(true)
				}
				go func() {
					deadline := time.Now().Add(5 * time.Second)
					for time.Now().Before(deadline) {
						if bw.childMu.TryLock() {
							acquired <- configured.Load()
							time.Sleep(50 * time.Millisecond) // hold the child mutex; initial configuration must wait
							probeReleased <- time.Now()
							bw.childMu.Unlock()
							return
						}
					}
				}()
			},
			UpdateClientConnState: func(bd *stub.BalancerData, _ balancer.ClientConnState) error {
				select {
				case configStarted <- time.Now():
				default:
				}
				configured.Store(true)
				bw := bd.ClientConn.(*balancerWrapper)
				if bw.childMu.TryLock() {
					bw.childMu.Unlock()
				} else {
					heldDuringConfig.Store(true)
				}
				return nil
			},
		})
		tcc := testutils.NewBalancerClientConn(t)
		es := NewBalancer(tcc, balancer.BuildOptions{}, balancer.Get(name).Build, Options{DisableAutoReconnect: true})
		if err := es.UpdateClientConnState(balancer.ClientConnState{ResolverState: resolver.State{Endpoints: []resolver.Endpoint{{Addresses: []resolver.Address{{Addr: "endpoint"}}}}}}); err != nil {
			t.Fatalf("UpdateClientConnState() failed: %v", err)
		}
		var configuredAtAcquire bool
		select {
		case configuredAtAcquire = <-acquired:
		case <-time.After(5 * time.Second):
			t.Fatal("probe never acquired childMu")
		}
		released := <-probeReleased
		started := <-configStarted
		if !configuredAtAcquire {
			gapHits++
			t.Logf("iteration %d: probe acquired childMu AFTER construction and BEFORE initial configuration (childMu held during build=%v, during config=%v); initial UpdateClientConnState started %v after the probe released childMu",
				i, heldDuringBuild.Load(), heldDuringConfig.Load(), started.Sub(released).Round(time.Millisecond))
		} else {
			t.Logf("iteration %d: probe acquired childMu only after initial configuration", i)
		}
		es.Close()
	}
	t.Logf("childMu was acquirable between construction and initial configuration in %d/%d iterations", gapHits, iterations)
	if gapHits > 0 {
		t.Errorf("exclusion gap observed in %d/%d iterations", gapHits, iterations)
	}
}
