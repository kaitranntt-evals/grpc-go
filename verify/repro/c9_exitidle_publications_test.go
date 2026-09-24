// Run: cp verify/repro/c9_exitidle_publications_test.go balancer/endpointsharding/ && go test -race -count=1 -run 'TestC9' -v ./balancer/endpointsharding/
package endpointsharding_test

import (
	"sync"
	"testing"

	"google.golang.org/grpc/balancer"
	"google.golang.org/grpc/balancer/endpointsharding"
	"google.golang.org/grpc/connectivity"
	"google.golang.org/grpc/resolver"
)

type c9Child struct {
	cc balancer.ClientConn
}

func (c *c9Child) UpdateClientConnState(balancer.ClientConnState) error {
	c.cc.UpdateState(balancer.State{ConnectivityState: connectivity.Idle, Picker: c9Picker{}})
	return nil
}
func (c *c9Child) ResolverError(error)                                        {}
func (c *c9Child) UpdateSubConnState(balancer.SubConn, balancer.SubConnState) {}
func (c *c9Child) Close()                                                     {}

// ExitIdle synchronously reports CONNECTING to the parent.
func (c *c9Child) ExitIdle() {
	c.cc.UpdateState(balancer.State{ConnectivityState: connectivity.Connecting, Picker: c9Picker{}})
}

type c9Picker struct{}

func (c9Picker) Pick(balancer.PickInfo) (balancer.PickResult, error) {
	return balancer.PickResult{}, balancer.ErrNoSubConnAvailable
}

type c9CC struct {
	balancer.ClientConn
	mu     sync.Mutex
	states []connectivity.State
}

func (c *c9CC) UpdateState(s balancer.State) {
	c.mu.Lock()
	c.states = append(c.states, s.ConnectivityState)
	c.mu.Unlock()
}

func (c *c9CC) snapshot() []connectivity.State {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]connectivity.State(nil), c.states...)
}

func TestC9ParentExitIdlePublications(t *testing.T) {
	cc := &c9CC{}
	builder := func(cc balancer.ClientConn, _ balancer.BuildOptions) balancer.Balancer { return &c9Child{cc: cc} }
	es := endpointsharding.NewBalancer(cc, balancer.BuildOptions{}, builder, endpointsharding.Options{DisableAutoReconnect: true})
	defer es.Close()
	if err := es.UpdateClientConnState(balancer.ClientConnState{ResolverState: resolver.State{Endpoints: []resolver.Endpoint{
		{Addresses: []resolver.Address{{Addr: "a"}}},
		{Addresses: []resolver.Address{{Addr: "b"}}},
	}}}); err != nil {
		t.Fatalf("UpdateClientConnState: %v", err)
	}
	before := len(cc.snapshot())
	es.ExitIdle()
	after := cc.snapshot()
	during := after[before:]
	t.Logf("parent publications during one ExitIdle call: %d %v", len(during), during)
	if len(during) > 1 {
		t.Errorf("one parent ExitIdle produced %d aggregate publications, want <= 1", len(during))
	}
}
