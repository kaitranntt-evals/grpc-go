//go:build repro

// Run (branch evalon/grpc-go-en-926a7310 of kaitranntt-evals/grpc-go-endpointsharding-decouple-locking):
//
//	cp verify/repro/c6_close_callbacks_test.go balancer/endpointsharding/ && go test ./balancer/endpointsharding -run 'TestC6_' -tags repro -race -count=1 -v
//
// C6 repro: child state callbacks issued while the endpointsharding balancer is
// closing its children, and after Close has returned, measured at the
// endpointsharding boundary (the balancer.ClientConn passed to NewBalancer).
package endpointsharding_test

import (
	"sync"
	"testing"

	"google.golang.org/grpc/balancer"
	"google.golang.org/grpc/balancer/endpointsharding"
	"google.golang.org/grpc/connectivity"
	"google.golang.org/grpc/resolver"
)

type c6ProbeCC struct {
	balancer.ClientConn
	mu      sync.Mutex
	updates []balancer.State
}

func (cc *c6ProbeCC) UpdateState(s balancer.State) {
	cc.mu.Lock()
	defer cc.mu.Unlock()
	cc.updates = append(cc.updates, s)
}

func (cc *c6ProbeCC) count() int {
	cc.mu.Lock()
	defer cc.mu.Unlock()
	return len(cc.updates)
}

type c6Picker struct{ tag string }

func (p *c6Picker) Pick(balancer.PickInfo) (balancer.PickResult, error) {
	return balancer.PickResult{}, nil
}

type c6Child struct {
	cc balancer.ClientConn
}

func (c *c6Child) UpdateClientConnState(balancer.ClientConnState) error {
	c.cc.UpdateState(balancer.State{ConnectivityState: connectivity.Ready, Picker: &c6Picker{tag: "configured"}})
	return nil
}
func (c *c6Child) ResolverError(error)                                        {}
func (c *c6Child) UpdateSubConnState(balancer.SubConn, balancer.SubConnState) {}
func (c *c6Child) ExitIdle()                                                  {}

// Close reports a state synchronously while the child is being closed, as a
// real child may do when it shuts down its SubConns.
func (c *c6Child) Close() {
	c.cc.UpdateState(balancer.State{ConnectivityState: connectivity.Shutdown, Picker: &c6Picker{tag: "during-close"}})
}

func TestC6_ChildCallbacksDuringAndAfterClose(t *testing.T) {
	var children []*c6Child
	var mu sync.Mutex
	builder := func(cc balancer.ClientConn, _ balancer.BuildOptions) balancer.Balancer {
		c := &c6Child{cc: cc}
		mu.Lock()
		children = append(children, c)
		mu.Unlock()
		return c
	}
	cc := &c6ProbeCC{}
	es := endpointsharding.NewBalancer(cc, balancer.BuildOptions{}, builder, endpointsharding.Options{DisableAutoReconnect: true})
	if err := es.UpdateClientConnState(balancer.ClientConnState{ResolverState: resolver.State{Endpoints: []resolver.Endpoint{
		{Addresses: []resolver.Address{{Addr: "a"}}},
		{Addresses: []resolver.Address{{Addr: "b"}}},
	}}}); err != nil {
		t.Fatalf("UpdateClientConnState: %v", err)
	}
	before := cc.count()
	t.Logf("parent UpdateState calls after initial configuration: %d", before)

	es.Close()
	duringClose := cc.count() - before
	t.Logf("parent UpdateState calls made while Close() was closing children: %d", duringClose)
	if duringClose > 0 {
		last := cc.updates[len(cc.updates)-1]
		t.Logf("  last publication during Close: agg=%v children=%d", last.ConnectivityState, len(endpointsharding.ChildStatesFromPicker(last.Picker)))
		t.Errorf("child state callbacks during child closure reached the parent's UpdateState (%d calls)", duringClose)
	}

	afterMark := cc.count()
	mu.Lock()
	cs := append([]*c6Child(nil), children...)
	mu.Unlock()
	for _, c := range cs {
		c.cc.UpdateState(balancer.State{ConnectivityState: connectivity.Connecting, Picker: &c6Picker{tag: "after-close"}})
	}
	afterClose := cc.count() - afterMark
	t.Logf("parent UpdateState calls made by child callbacks after Close() returned: %d", afterClose)
	if afterClose > 0 {
		last := cc.updates[len(cc.updates)-1]
		t.Logf("  last publication after Close: agg=%v children=%d", last.ConnectivityState, len(endpointsharding.ChildStatesFromPicker(last.Picker)))
		t.Errorf("child state callbacks after Close returned reached the parent's UpdateState (%d calls)", afterClose)
	}
}
