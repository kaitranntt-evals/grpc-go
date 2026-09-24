// Run: verify/repro/c5_run.sh <commit-ish>  (repo root; installs c5_hook.go.txt and this test in a scratch worktree and runs TestC5Controlled)
package endpointsharding_test

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"google.golang.org/grpc/balancer"
	"google.golang.org/grpc/balancer/endpointsharding"
	"google.golang.org/grpc/connectivity"
	"google.golang.org/grpc/resolver"
)

type c5cChild struct {
	configured      atomic.Bool
	preConfigExits  atomic.Int32
	postConfigExits atomic.Int32
	exited          chan struct{}
	once            sync.Once
}

func (c *c5cChild) UpdateClientConnState(balancer.ClientConnState) error {
	c.configured.Store(true)
	return nil
}
func (c *c5cChild) ResolverError(error)                                        {}
func (c *c5cChild) UpdateSubConnState(balancer.SubConn, balancer.SubConnState) {}
func (c *c5cChild) Close()                                                     {}
func (c *c5cChild) ExitIdle() {
	if c.configured.Load() {
		c.postConfigExits.Add(1)
	} else {
		c.preConfigExits.Add(1)
	}
	c.once.Do(func() { close(c.exited) })
}

type c5cCC struct {
	balancer.ClientConn
	mu   sync.Mutex
	last balancer.State
}

func (c *c5cCC) UpdateState(s balancer.State) { c.mu.Lock(); c.last = s; c.mu.Unlock() }

type c5cPicker struct{}

func (c5cPicker) Pick(balancer.PickInfo) (balancer.PickResult, error) {
	return balancer.PickResult{}, balancer.ErrNoSubConnAvailable
}

func TestC5Controlled(t *testing.T) {
	child := &c5cChild{exited: make(chan struct{})}
	builder := func(cc balancer.ClientConn, _ balancer.BuildOptions) balancer.Balancer {
		cc.UpdateState(balancer.State{ConnectivityState: connectivity.Idle, Picker: c5cPicker{}})
		return child
	}
	// Scheduling control: after construction releases the child's mutex, hold
	// registration/configuration until the queued construction-time reconnect has run.
	endpointsharding.VerifyC5AfterBuild = func() {
		select {
		case <-child.exited:
			t.Logf("pre-configuration window: queued reconnect ran; configured=%v", child.configured.Load())
		case <-time.After(2 * time.Second):
			t.Logf("pre-configuration window: queued reconnect did not run within 2s")
		}
	}
	defer func() { endpointsharding.VerifyC5AfterBuild = nil }()
	cc := &c5cCC{}
	es := endpointsharding.NewBalancer(cc, balancer.BuildOptions{}, builder, endpointsharding.Options{})
	defer es.Close()
	if err := es.UpdateClientConnState(balancer.ClientConnState{ResolverState: resolver.State{Endpoints: []resolver.Endpoint{{Addresses: []resolver.Address{{Addr: "a"}}}}}}); err != nil {
		t.Fatalf("UpdateClientConnState: %v", err)
	}
	time.Sleep(500 * time.Millisecond)
	cc.mu.Lock()
	agg := cc.last.ConnectivityState
	cc.mu.Unlock()
	cs := endpointsharding.ChildStatesFromPicker(cc.last.Picker)
	t.Logf("after configuration + 500ms: preConfigExits=%d postConfigExits=%d aggregate=%v childStates=%d", child.preConfigExits.Load(), child.postConfigExits.Load(), agg, len(cs))
	if child.preConfigExits.Load() > 0 && child.postConfigExits.Load() == 0 {
		t.Errorf("construction-time reconnect consumed before configuration; no reconnect delivered after configuration")
	}
}
