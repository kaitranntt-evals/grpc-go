// Run: cp verify/repro/c3_construction_order_test.go balancer/endpointsharding/ && go test ./balancer/endpointsharding -run '^TestVerifyC3ReconnectBeforeInitialConfig$' -count=1 -v   (on branch evalon/grpc-go-en-1d4033f5)

package endpointsharding_test

import (
	"sync"
	"testing"
	"time"

	"google.golang.org/grpc/balancer"
	"google.golang.org/grpc/balancer/base"
	"google.golang.org/grpc/balancer/endpointsharding"
	"google.golang.org/grpc/connectivity"
	"google.golang.org/grpc/resolver"
)

type c3ParentCC struct{ balancer.ClientConn }

func (c3ParentCC) UpdateState(balancer.State) {}

// c3Child records the order in which endpointsharding calls into it.
type c3Child struct {
	mu     sync.Mutex
	events []string
	done   chan struct{}
	once   sync.Once
}

func (c *c3Child) record(ev string) {
	c.mu.Lock()
	c.events = append(c.events, ev)
	c.mu.Unlock()
}

func (c *c3Child) UpdateClientConnState(balancer.ClientConnState) error {
	c.record("UpdateClientConnState")
	return nil
}
func (c *c3Child) ResolverError(error)                                        {}
func (c *c3Child) UpdateSubConnState(balancer.SubConn, balancer.SubConnState) {}
func (c *c3Child) Close()                                                     {}
func (c *c3Child) ExitIdle() {
	c.record("ExitIdle")
	c.once.Do(func() { close(c.done) })
}

// A child reports IDLE synchronously from its constructor (auto-reconnect on).
// The construction comment promises the resulting ExitIdle is delivered only
// after construction AND the initial update complete. Count iterations where
// ExitIdle reaches the child before its first UpdateClientConnState.
func TestVerifyC3ReconnectBeforeInitialConfig(t *testing.T) {
	const iterations = 20000
	violations := 0
	var first []string
	for i := 0; i < iterations; i++ {
		var child *c3Child
		builder := func(cc balancer.ClientConn, _ balancer.BuildOptions) balancer.Balancer {
			child = &c3Child{done: make(chan struct{})}
			cc.UpdateState(balancer.State{ConnectivityState: connectivity.Idle, Picker: base.NewErrPicker(balancer.ErrNoSubConnAvailable)})
			return child
		}
		es := endpointsharding.NewBalancer(c3ParentCC{}, balancer.BuildOptions{}, builder, endpointsharding.Options{})
		if err := es.UpdateClientConnState(balancer.ClientConnState{ResolverState: resolver.State{Endpoints: []resolver.Endpoint{{Addresses: []resolver.Address{{Addr: "a"}}}}}}); err != nil {
			t.Fatalf("UpdateClientConnState: %v", err)
		}
		select {
		case <-child.done:
		case <-time.After(5 * time.Second):
			t.Fatalf("iteration %d: ExitIdle never delivered", i)
		}
		child.mu.Lock()
		evs := append([]string(nil), child.events...)
		child.mu.Unlock()
		if evs[0] == "ExitIdle" {
			violations++
			if first == nil {
				first = evs
				t.Logf("iteration %d: child call order = %v", i, evs)
			}
		}
		es.Close()
	}
	t.Logf("ExitIdle delivered before initial UpdateClientConnState in %d/%d iterations", violations, iterations)
	if violations > 0 {
		t.Errorf("construction comment's ordering promise violated %d times", violations)
	}
}
