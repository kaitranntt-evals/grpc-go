// Run: verify/repro/c11_construction_gap.sh   (C11 probe; FAILS when no effective ExitIdle follows initial configuration)

//go:build ignore

package endpointsharding_test

import (
	"sync"
	"testing"
	"time"

	"google.golang.org/grpc/balancer"
	"google.golang.org/grpc/balancer/base"
	"google.golang.org/grpc/balancer/endpointsharding"
	"google.golang.org/grpc/connectivity"
	"google.golang.org/grpc/internal/balancer/stub"
	"google.golang.org/grpc/resolver"
)

type auditC11CC struct{ balancer.ClientConn }

func (auditC11CC) UpdateState(balancer.State) {}

// A child that reports IDLE synchronously during construction (auto-reconnect
// enabled) and can only reconnect once configured: ExitIdle before its first
// UpdateClientConnState is ineffective.
func TestAuditC11ConstructionReconnect(t *testing.T) {
	name := "auditc11construction"
	var mu sync.Mutex
	configured := false
	var early, effective int
	stub.Register(name, stub.BalancerFuncs{
		Init: func(bd *stub.BalancerData) {
			bd.ClientConn.UpdateState(balancer.State{ConnectivityState: connectivity.Idle, Picker: base.NewErrPicker(balancer.ErrNoSubConnAvailable)})
		},
		UpdateClientConnState: func(bd *stub.BalancerData, _ balancer.ClientConnState) error {
			mu.Lock()
			configured = true
			mu.Unlock()
			// Stays IDLE-waiting-for-ExitIdle: reports nothing new, so no new request.
			return nil
		},
		ExitIdle: func(*stub.BalancerData) {
			mu.Lock()
			defer mu.Unlock()
			if configured {
				effective++
			} else {
				early++
			}
		},
	})
	es := endpointsharding.NewBalancer(auditC11CC{}, balancer.BuildOptions{}, balancer.Get(name).Build, endpointsharding.Options{})
	defer es.Close()
	if err := es.UpdateClientConnState(balancer.ClientConnState{ResolverState: resolver.State{Endpoints: []resolver.Endpoint{{Addresses: []resolver.Address{{Addr: "a"}}}}}}); err != nil {
		t.Fatal(err)
	}
	time.Sleep(200 * time.Millisecond)
	mu.Lock()
	defer mu.Unlock()
	t.Logf("ExitIdle before configuration (ineffective): %d, after configuration (effective): %d", early, effective)
	if effective == 0 {
		t.Errorf("no effective reconnection request after initial configuration (early=%d)", early)
	}
}
