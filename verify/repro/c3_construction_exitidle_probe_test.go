// Run: copy this file to balancer/endpointsharding/ on branch evalon/grpc-go-en-a9c81aee, then `go test -tags verifyrepro ./balancer/endpointsharding -run '^TestC3ConstructionExitIdleProbe$' -race -count=1 -v`

//go:build verifyrepro

package endpointsharding_test

import (
	"sync/atomic"
	"testing"
	"time"

	"google.golang.org/grpc/balancer"
	"google.golang.org/grpc/balancer/base"
	"google.golang.org/grpc/balancer/endpointsharding"
	"google.golang.org/grpc/connectivity"
	"google.golang.org/grpc/internal/testutils"
	"google.golang.org/grpc/resolver"
)

// c3Child reports IDLE synchronously from its builder (before it is returned),
// and can only "reconnect" once it has received its initial configuration.
// ExitIdle records whether the child was configured when it ran.
type c3Child struct {
	cc         balancer.ClientConn
	configured atomic.Bool
	exitIdles  chan bool // value = configured at the time ExitIdle ran
}

func (c *c3Child) UpdateClientConnState(balancer.ClientConnState) error {
	c.configured.Store(true)
	return nil
}
func (c *c3Child) ResolverError(error)                                        {}
func (c *c3Child) UpdateSubConnState(balancer.SubConn, balancer.SubConnState) {}
func (c *c3Child) Close()                                                     {}
func (c *c3Child) ExitIdle()                                                  { c.exitIdles <- c.configured.Load() }

func TestC3ConstructionExitIdleProbe(t *testing.T) {
	const iterations = 200
	var premature, prematureRetried, afterConfig, none int
	for i := 0; i < iterations; i++ {
		child := &c3Child{exitIdles: make(chan bool, 10)}
		builder := func(cc balancer.ClientConn, _ balancer.BuildOptions) balancer.Balancer {
			child.cc = cc
			// Synchronous IDLE report during construction queues an automatic ExitIdle.
			cc.UpdateState(balancer.State{ConnectivityState: connectivity.Idle, Picker: base.NewErrPicker(balancer.ErrNoSubConnAvailable)})
			// Give the queued ExitIdle goroutine time to start and wait on the
			// child's mutex while construction is still in progress.
			time.Sleep(5 * time.Millisecond)
			return child
		}
		es := endpointsharding.NewBalancer(testutils.NewBalancerClientConn(t), balancer.BuildOptions{}, builder, endpointsharding.Options{})
		ccs := balancer.ClientConnState{ResolverState: resolver.State{Endpoints: []resolver.Endpoint{{Addresses: []resolver.Address{{Addr: "a"}}}}}}
		if err := es.UpdateClientConnState(ccs); err != nil {
			t.Fatalf("UpdateClientConnState() = %v", err)
		}
		// The initial configuration has been delivered. Collect the ExitIdle
		// calls the child receives.
		var calls []bool
	collect:
		for {
			select {
			case c := <-child.exitIdles:
				calls = append(calls, c)
			case <-time.After(100 * time.Millisecond):
				break collect
			}
		}
		es.Close()
		switch {
		case len(calls) == 0:
			none++
		case !calls[0] && len(calls) == 1:
			premature++
		case !calls[0]:
			prematureRetried++
		default:
			afterConfig++
		}
	}
	t.Logf("iterations=%d ExitIdle-after-config=%d ExitIdle-before-config-not-retried=%d ExitIdle-before-config-then-retried=%d no-ExitIdle=%d",
		iterations, afterConfig, premature, prematureRetried, none)
	if premature > 0 {
		t.Fatalf("%d/%d runs: automatic ExitIdle ran on the unconfigured child and was never retried after configuration", premature, iterations)
	}
}
