// Run: verify/repro/c6_run.sh <commit-ish>  (installs c6_hook.go.txt + this test into a scratch worktree and runs TestC6)
package endpointsharding

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"google.golang.org/grpc/balancer"
	"google.golang.org/grpc/connectivity"
	"google.golang.org/grpc/resolver"
	"google.golang.org/grpc/serviceconfig"
)

type c6BlockCfg struct {
	serviceconfig.LoadBalancingConfig
}

type c6Picker struct{}

func (c6Picker) Pick(balancer.PickInfo) (balancer.PickResult, error) {
	return balancer.PickResult{}, balancer.ErrNoSubConnAvailable
}

type c6CC struct {
	balancer.ClientConn
	mu     sync.Mutex
	states []balancer.State
}

func (c *c6CC) UpdateState(s balancer.State) {
	c.mu.Lock()
	c.states = append(c.states, s)
	c.mu.Unlock()
}

func (c *c6CC) snapshot() []balancer.State {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]balancer.State(nil), c.states...)
}

type c6Child struct {
	cc       balancer.ClientConn
	onUpdate func(*c6Child, balancer.ClientConnState) error
	addr     string
}

func (c *c6Child) UpdateClientConnState(s balancer.ClientConnState) error {
	c.addr = s.ResolverState.Endpoints[0].Addresses[0].Addr
	return c.onUpdate(c, s)
}
func (c *c6Child) ResolverError(error)                                        {}
func (c *c6Child) UpdateSubConnState(balancer.SubConn, balancer.SubConnState) {}
func (c *c6Child) Close()                                                     {}
func (c *c6Child) ExitIdle()                                                  {}

func TestC6AdmittedCallbackPublishesDuringBatch(t *testing.T) {
	var (
		mu        sync.Mutex
		childCCs  = map[string]balancer.ClientConn{}
		batchSeq  atomic.Int32
		entered   = make(chan string, 1)
		releaseCh = make(chan struct{})
	)
	onUpdate := func(c *c6Child, s balancer.ClientConnState) error {
		mu.Lock()
		childCCs[c.addr] = c.cc
		mu.Unlock()
		if _, ok := s.BalancerConfig.(c6BlockCfg); ok {
			if batchSeq.Add(1) == 2 {
				// Second child processed in the batch: hold it.
				entered <- c.addr
				<-releaseCh
				c.cc.UpdateState(balancer.State{ConnectivityState: connectivity.Ready, Picker: c6Picker{}})
				return nil
			}
			// First child processed in the batch becomes READY.
			c.cc.UpdateState(balancer.State{ConnectivityState: connectivity.Ready, Picker: c6Picker{}})
			return nil
		}
		c.cc.UpdateState(balancer.State{ConnectivityState: connectivity.Connecting, Picker: c6Picker{}})
		return nil
	}
	cc := &c6CC{}
	builder := func(cc balancer.ClientConn, _ balancer.BuildOptions) balancer.Balancer {
		return &c6Child{cc: cc, onUpdate: onUpdate}
	}
	es := NewBalancer(cc, balancer.BuildOptions{}, builder, Options{DisableAutoReconnect: true})
	endpoints := []resolver.Endpoint{{Addresses: []resolver.Address{{Addr: "A"}}}, {Addresses: []resolver.Address{{Addr: "B"}}}}
	if err := es.UpdateClientConnState(balancer.ClientConnState{ResolverState: resolver.State{Endpoints: endpoints}}); err != nil {
		t.Fatalf("initial UpdateClientConnState: %v", err)
	}
	t.Logf("after initial batch: %d publications, last=%v", len(cc.snapshot()), cc.snapshot()[len(cc.snapshot())-1].ConnectivityState)

	// Arm the hook: the next callback that passes the inhibition check pauses.
	var armed atomic.Bool
	armed.Store(true)
	admitted, resume := make(chan struct{}), make(chan struct{})
	verifyC6Hook = func() {
		if armed.CompareAndSwap(true, false) {
			close(admitted)
			<-resume
		}
	}
	defer func() { verifyC6Hook = nil }()

	// A child callback outside any batch (child "A" re-reports CONNECTING).
	mu.Lock()
	ccA := childCCs["A"]
	mu.Unlock()
	cbDone := make(chan struct{})
	go func() {
		ccA.UpdateState(balancer.State{ConnectivityState: connectivity.Connecting, Picker: c6Picker{}})
		close(cbDone)
	}()
	<-admitted
	t.Log("callback admitted (passed inhibition check) and paused before es.mu")

	// Start a configuration batch; the first processed child goes READY, the
	// second is held inside UpdateClientConnState.
	batchDone := make(chan error, 1)
	go func() {
		batchDone <- es.UpdateClientConnState(balancer.ClientConnState{BalancerConfig: c6BlockCfg{}, ResolverState: resolver.State{Endpoints: endpoints}})
	}()
	held := <-entered
	t.Logf("batch in progress: child %q held inside UpdateClientConnState", held)
	before := len(cc.snapshot())

	close(resume)
	select {
	case <-cbDone:
	case <-time.After(5 * time.Second):
		t.Fatal("paused callback did not finish")
	}
	during := cc.snapshot()[before:]
	select {
	case <-batchDone:
		t.Fatal("batch finished unexpectedly early")
	default:
	}
	for _, s := range during {
		var kids []string
		for _, cs := range ChildStatesFromPicker(s.Picker) {
			kids = append(kids, cs.Endpoint.Addresses[0].Addr+"="+cs.State.ConnectivityState.String())
		}
		t.Logf("publication while batch in progress: aggregate=%v children=%v", s.ConnectivityState, kids)
	}
	close(releaseCh)
	if err := <-batchDone; err != nil {
		t.Fatalf("batch UpdateClientConnState: %v", err)
	}
	es.Close()
	if len(during) > 0 {
		t.Errorf("admitted callback published %d aggregate(s) while the batch was still in progress", len(during))
	}
}
