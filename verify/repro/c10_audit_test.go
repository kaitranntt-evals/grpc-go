// Run: verify/repro/c10_lifecycle.sh   (C10 probe; FAILS when parent ExitIdle/Close forward intermediate child publications)

//go:build ignore

package endpointsharding_test

import (
	"fmt"
	"sync"
	"testing"

	"google.golang.org/grpc/balancer"
	"google.golang.org/grpc/balancer/base"
	"google.golang.org/grpc/balancer/endpointsharding"
	"google.golang.org/grpc/connectivity"
	"google.golang.org/grpc/internal/balancer/stub"
	"google.golang.org/grpc/resolver"
)

type auditC10CC struct {
	balancer.ClientConn
	mu     sync.Mutex
	states []connectivity.State
}

func (c *auditC10CC) UpdateState(s balancer.State) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.states = append(c.states, s.ConnectivityState)
}

func (c *auditC10CC) take() []connectivity.State {
	c.mu.Lock()
	defer c.mu.Unlock()
	s := c.states
	c.states = nil
	return s
}

func TestAuditC10LifecyclePublications(t *testing.T) {
	name := "auditc10lifecycle"
	errPicker := base.NewErrPicker(balancer.ErrNoSubConnAvailable)
	stub.Register(name, stub.BalancerFuncs{
		UpdateClientConnState: func(bd *stub.BalancerData, _ balancer.ClientConnState) error {
			bd.ClientConn.UpdateState(balancer.State{ConnectivityState: connectivity.Idle, Picker: errPicker})
			return nil
		},
		// Synchronous child callbacks from ExitIdle and Close.
		ExitIdle: func(bd *stub.BalancerData) {
			bd.ClientConn.UpdateState(balancer.State{ConnectivityState: connectivity.Connecting, Picker: errPicker})
		},
		Close: func(bd *stub.BalancerData) {
			bd.ClientConn.UpdateState(balancer.State{ConnectivityState: connectivity.TransientFailure, Picker: errPicker})
		},
	})
	cc := &auditC10CC{}
	es := endpointsharding.NewBalancer(cc, balancer.BuildOptions{}, balancer.Get(name).Build, endpointsharding.Options{DisableAutoReconnect: true})
	var eps []resolver.Endpoint
	for i := 0; i < 3; i++ {
		eps = append(eps, resolver.Endpoint{Addresses: []resolver.Address{{Addr: fmt.Sprintf("addr-%d", i)}}})
	}
	if err := es.UpdateClientConnState(balancer.ClientConnState{ResolverState: resolver.State{Endpoints: eps}}); err != nil {
		t.Fatal(err)
	}
	t.Logf("UpdateClientConnState (3 children) publications: %v", cc.take())
	es.ExitIdle()
	exitIdlePubs := cc.take()
	t.Logf("parent ExitIdle (3 children) publications: %v", exitIdlePubs)
	es.Close()
	closePubs := cc.take()
	t.Logf("parent Close (3 children) publications: %v", closePubs)
	if len(exitIdlePubs) > 1 {
		t.Errorf("idle-exit-publication: parent ExitIdle forwarded %d separate publications, want at most 1 consolidated", len(exitIdlePubs))
	}
	if len(closePubs) != 0 {
		t.Errorf("close-publication: parent Close forwarded %d publications during teardown, want 0", len(closePubs))
	}
}
