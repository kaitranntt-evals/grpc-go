// Run: copy this file to balancer/endpointsharding/ on branch evalon/grpc-go-en-3c1b9630, then `go test -tags verifyrepro ./balancer/endpointsharding -run '^TestC8ExitIdleFanoutProbe$' -race -count=1 -v`

//go:build verifyrepro

package endpointsharding_test

import (
	"fmt"
	"sync"
	"testing"
	"time"

	"google.golang.org/grpc/balancer"
	"google.golang.org/grpc/balancer/base"
	"google.golang.org/grpc/balancer/endpointsharding"
	"google.golang.org/grpc/connectivity"
	"google.golang.org/grpc/resolver"
)

// c8CC records every aggregate state published by endpointsharding.
type c8CC struct {
	balancer.ClientConn
	mu   sync.Mutex
	pubs []string
}

func (c *c8CC) UpdateState(s balancer.State) {
	desc := s.ConnectivityState.String() + "{"
	for _, cs := range endpointsharding.ChildStatesFromPicker(s.Picker) {
		desc += fmt.Sprintf(" %s=%s", cs.Endpoint.Addresses[0].Addr, cs.State.ConnectivityState)
	}
	desc += " }"
	c.mu.Lock()
	c.pubs = append(c.pubs, desc)
	c.mu.Unlock()
}

func (c *c8CC) snapshot() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]string(nil), c.pubs...)
}

// c8Child synchronously reports CONNECTING from within ExitIdle.
type c8Child struct {
	cc        balancer.ClientConn
	exitIdled chan struct{}
}

func (c *c8Child) UpdateClientConnState(balancer.ClientConnState) error {
	c.cc.UpdateState(balancer.State{ConnectivityState: connectivity.Idle, Picker: base.NewErrPicker(balancer.ErrNoSubConnAvailable)})
	return nil
}
func (c *c8Child) ResolverError(error)                                        {}
func (c *c8Child) UpdateSubConnState(balancer.SubConn, balancer.SubConnState) {}
func (c *c8Child) Close()                                                     {}
func (c *c8Child) ExitIdle() {
	c.cc.UpdateState(balancer.State{ConnectivityState: connectivity.Connecting, Picker: base.NewErrPicker(balancer.ErrNoSubConnAvailable)})
	c.exitIdled <- struct{}{}
}

func TestC8ExitIdleFanoutProbe(t *testing.T) {
	exitIdled := make(chan struct{}, 10)
	builder := func(cc balancer.ClientConn, _ balancer.BuildOptions) balancer.Balancer {
		return &c8Child{cc: cc, exitIdled: exitIdled}
	}
	cc := &c8CC{}
	es := endpointsharding.NewBalancer(cc, balancer.BuildOptions{}, builder, endpointsharding.Options{DisableAutoReconnect: true})
	defer es.Close()

	eps := []resolver.Endpoint{{Addresses: []resolver.Address{{Addr: "A"}}}, {Addresses: []resolver.Address{{Addr: "B"}}}}
	if err := es.UpdateClientConnState(balancer.ClientConnState{ResolverState: resolver.State{Endpoints: eps}}); err != nil {
		t.Fatalf("UpdateClientConnState() = %v", err)
	}
	base := len(cc.snapshot())
	t.Logf("publications from initial configuration: %v", cc.snapshot())

	// One parent ExitIdle call; both children synchronously report CONNECTING.
	es.ExitIdle()
	for i := 0; i < 2; i++ {
		select {
		case <-exitIdled:
		case <-time.After(3 * time.Second):
			t.Fatalf("timed out waiting for ExitIdle to reach child %d", i+1)
		}
	}
	time.Sleep(200 * time.Millisecond) // let any trailing publication land
	pubs := cc.snapshot()[base:]
	t.Logf("publications caused by one parent ExitIdle call: %d -> %v", len(pubs), pubs)
	if len(pubs) != 1 {
		t.Errorf("want exactly 1 consolidated publication after ExitIdle fan-out, got %d: %v", len(pubs), pubs)
	}
}
