// Run (on branch evalon/grpc-go-en-a9c81aee): `git apply verify/repro/c3_construction_gap_hook_a9c81aee.patch && cp verify/repro/c3_construction_gap_deterministic_test.go balancer/endpointsharding/ && go test -tags verifyrepro ./balancer/endpointsharding -run '^TestC3ConstructionGapDeterministic$' -race -count=1 -v`

//go:build verifyrepro

package endpointsharding

import (
	"sync/atomic"
	"testing"
	"time"

	"google.golang.org/grpc/balancer"
	"google.golang.org/grpc/balancer/base"
	"google.golang.org/grpc/connectivity"
	"google.golang.org/grpc/internal/testutils"
	"google.golang.org/grpc/resolver"
)

// c3GapChild reports IDLE synchronously from its builder and records whether it
// had been configured each time ExitIdle is delivered.
type c3GapChild struct {
	configured atomic.Bool
	exitIdles  chan bool
}

func (c *c3GapChild) UpdateClientConnState(balancer.ClientConnState) error {
	c.configured.Store(true)
	return nil
}
func (c *c3GapChild) ResolverError(error)                                        {}
func (c *c3GapChild) UpdateSubConnState(balancer.SubConn, balancer.SubConnState) {}
func (c *c3GapChild) Close()                                                     {}
func (c *c3GapChild) ExitIdle()                                                  { c.exitIdles <- c.configured.Load() }

// TestC3ConstructionGapDeterministic pauses the parent in the window between
// releasing the new child's mutex after construction and delivering the
// child's initial configuration, and observes whether the construction-time
// ExitIdle request is executed in that window and whether it is retried once
// configuration completes.
func TestC3ConstructionGapDeterministic(t *testing.T) {
	child := &c3GapChild{exitIdles: make(chan bool, 10)}
	var exitIdleInGap bool
	testHookAfterChildBuilt = func() {
		// Parent is between childMu.Unlock() (post-construction) and
		// updateClientConnState(). Wait to see whether the queued ExitIdle
		// runs against the still-unconfigured child.
		select {
		case configured := <-child.exitIdles:
			exitIdleInGap = true
			t.Logf("in construction gap: child.ExitIdle() delivered with configured=%v", configured)
		case <-time.After(2 * time.Second):
			t.Logf("in construction gap: no ExitIdle delivered within 2s")
		}
	}
	defer func() { testHookAfterChildBuilt = nil }()

	builder := func(cc balancer.ClientConn, _ balancer.BuildOptions) balancer.Balancer {
		cc.UpdateState(balancer.State{ConnectivityState: connectivity.Idle, Picker: base.NewErrPicker(balancer.ErrNoSubConnAvailable)})
		return child
	}
	es := NewBalancer(testutils.NewBalancerClientConn(t), balancer.BuildOptions{}, builder, Options{})
	defer es.Close()
	ccs := balancer.ClientConnState{ResolverState: resolver.State{Endpoints: []resolver.Endpoint{{Addresses: []resolver.Address{{Addr: "a"}}}}}}
	if err := es.UpdateClientConnState(ccs); err != nil {
		t.Fatalf("UpdateClientConnState() = %v", err)
	}
	if !child.configured.Load() {
		t.Fatal("child never configured")
	}
	// Configuration is complete. Is the request retried?
	select {
	case configured := <-child.exitIdles:
		t.Logf("after configuration: child.ExitIdle() delivered with configured=%v", configured)
		if exitIdleInGap {
			t.Log("request executed early AND retried after configuration")
		}
	case <-time.After(2 * time.Second):
		if exitIdleInGap {
			t.Fatal("construction-time ExitIdle executed before initial configuration (no-op on unconfigured child) and was NOT retried after configuration")
		}
		t.Fatal("no ExitIdle delivered at all")
	}
}
