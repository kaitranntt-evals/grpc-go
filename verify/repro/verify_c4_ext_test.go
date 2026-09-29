// Repro for C4 (branch evalon/grpc-go-en-93bc5f0b): an automatic reconnect queued while the child
// is being built reaches the child's ExitIdle before its initial UpdateClientConnState.
// Run: cp verify/repro/verify_c4_ext_test.go balancer/endpointsharding/ && go test ./balancer/endpointsharding -run '^TestVerifyC4_' -race -count=1 -v
package endpointsharding_test

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"google.golang.org/grpc/balancer"
	"google.golang.org/grpc/balancer/base"
	"google.golang.org/grpc/balancer/endpointsharding"
	"google.golang.org/grpc/connectivity"
	"google.golang.org/grpc/internal/balancer/stub"
	"google.golang.org/grpc/internal/testutils"
	"google.golang.org/grpc/resolver"
)

// runC4 builds one child whose builder synchronously reports IDLE (queuing an
// automatic reconnect) and then keeps building for buildDelay. It returns
// whether the child's ExitIdle ran before its initial UpdateClientConnState.
func runC4(t *testing.T, name string, buildDelay time.Duration) (premature bool) {
	var configured atomic.Bool
	exitedIdle := make(chan bool, 1)
	stub.Register(name, stub.BalancerFuncs{
		Init: func(bd *stub.BalancerData) {
			bd.ClientConn.UpdateState(balancer.State{
				ConnectivityState: connectivity.Idle,
				Picker:            base.NewErrPicker(balancer.ErrNoSubConnAvailable),
			})
			time.Sleep(buildDelay)
		},
		UpdateClientConnState: func(*stub.BalancerData, balancer.ClientConnState) error {
			configured.Store(true)
			return nil
		},
		ExitIdle: func(*stub.BalancerData) {
			select {
			case exitedIdle <- configured.Load():
			default:
			}
		},
	})
	cc := testutils.NewBalancerClientConn(t)
	b := endpointsharding.NewBalancer(cc, balancer.BuildOptions{}, balancer.Get(name).Build, endpointsharding.Options{})
	defer b.Close()
	if err := b.UpdateClientConnState(balancer.ClientConnState{
		ResolverState: resolver.State{Endpoints: []resolver.Endpoint{{Addresses: []resolver.Address{{Addr: "endpoint"}}}}},
	}); err != nil {
		t.Fatalf("UpdateClientConnState() failed: %v", err)
	}
	select {
	case configuredAtExitIdle := <-exitedIdle:
		return !configuredAtExitIdle
	case <-time.After(5 * time.Second):
		t.Fatal("Timed out waiting for the automatic ExitIdle")
	}
	return false
}

func TestVerifyC4_AutoReconnectBeforeInitialConfig(t *testing.T) {
	for _, delay := range []time.Duration{0, 5 * time.Millisecond} {
		t.Run(fmt.Sprintf("buildDelay=%v", delay), func(t *testing.T) {
			iterations := 20
			if v, err := strconv.Atoi(os.Getenv("VERIFY_C4_ITERS")); err == nil && v > 0 {
				iterations = v
			}
			premature := 0
			for i := 0; i < iterations; i++ {
				name := strings.ReplaceAll(strings.ToLower(t.Name()), "/", "-") + fmt.Sprintf("-%d", i)
				if runC4(t, name, delay) {
					premature++
				}
			}
			t.Logf("buildDelay=%v: ExitIdle reached the child BEFORE its initial UpdateClientConnState in %d/%d runs", delay, premature, iterations)
			if premature > 0 {
				t.Errorf("automatic reconnect invoked an unconfigured child in %d/%d runs", premature, iterations)
			}
		})
	}
}
