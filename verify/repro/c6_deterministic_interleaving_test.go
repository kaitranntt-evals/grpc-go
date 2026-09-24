// Run (on evalon/grpc-go-en-abc06f48): git apply verify/repro/c6_widen_window.patch && cp verify/repro/c6_hook_decl.go verify/repro/c6_deterministic_interleaving_test.go balancer/endpointsharding/ && go test ./balancer/endpointsharding -run '^TestVerifyC6DeterministicStaleInhibitCheck$' -count=1 -v

package endpointsharding_test

import (
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"google.golang.org/grpc/balancer"
	"google.golang.org/grpc/balancer/endpointsharding"
	"google.golang.org/grpc/connectivity"
	"google.golang.org/grpc/resolver"
	"google.golang.org/grpc/serviceconfig"
)

type d6Cfg struct {
	serviceconfig.LoadBalancingConfig
	gen int64
}

type d6Picker struct{ gen int64 }

func (d6Picker) Pick(balancer.PickInfo) (balancer.PickResult, error) {
	return balancer.PickResult{}, balancer.ErrNoSubConnAvailable
}

type d6Child struct {
	cc       balancer.ClientConn
	gen      atomic.Int64
	onUpdate func(gen int64) (after func())
}

func (c *d6Child) report() {
	c.cc.UpdateState(balancer.State{ConnectivityState: connectivity.Ready, Picker: d6Picker{gen: c.gen.Load()}})
}
func (c *d6Child) UpdateClientConnState(ccs balancer.ClientConnState) error {
	gen := ccs.BalancerConfig.(*d6Cfg).gen
	var after func()
	if c.onUpdate != nil {
		after = c.onUpdate(gen)
	}
	c.gen.Store(gen)
	c.report()
	if after != nil {
		after()
	}
	return nil
}
func (c *d6Child) ResolverError(error)                                        {}
func (c *d6Child) UpdateSubConnState(balancer.SubConn, balancer.SubConnState) {}
func (c *d6Child) Close()                                                     {}
func (c *d6Child) ExitIdle()                                                  {}

type d6ParentCC struct {
	balancer.ClientConn
	mu      sync.Mutex
	notices []string
}

func (p *d6ParentCC) UpdateState(s balancer.State) {
	desc := ""
	for _, cs := range endpointsharding.ChildStatesFromPicker(s.Picker) {
		desc += fmt.Sprintf("%s=gen%d ", cs.Endpoint.Addresses[0].Addr, cs.State.Picker.(d6Picker).gen)
	}
	p.mu.Lock()
	p.notices = append(p.notices, desc)
	p.mu.Unlock()
}

func TestVerifyC6DeterministicStaleInhibitCheck(t *testing.T) {
	var armed atomic.Bool
	passed := make(chan struct{})
	release := make(chan struct{})
	endpointsharding.VerifyC6AfterInhibitCheck = func() {
		if armed.CompareAndSwap(true, false) {
			close(passed)
			<-release
		}
	}
	defer func() { endpointsharding.VerifyC6AfterInhibitCheck = nil }()

	cc := &d6ParentCC{}
	var mu sync.Mutex
	children := map[string]*d6Child{}
	var applied2 atomic.Int32
	firstApplied := make(chan string, 1)
	secondBlocked := make(chan string, 1)
	unblockSecond := make(chan struct{})
	es := endpointsharding.NewBalancer(cc, balancer.BuildOptions{}, func(bcc balancer.ClientConn, _ balancer.BuildOptions) balancer.Balancer {
		c := &d6Child{cc: bcc}
		c.onUpdate = func(gen int64) func() {
			if gen != 2 {
				return nil
			}
			if applied2.Add(1) == 1 {
				return func() { firstApplied <- "" } // this child has applied gen2 and reported
			}
			secondBlocked <- ""
			<-unblockSecond // second child has not yet applied gen2: batch still open
			return nil
		}
		mu.Lock()
		children[fmt.Sprint(len(children))] = c
		mu.Unlock()
		return c
	}, endpointsharding.Options{DisableAutoReconnect: true})
	defer es.Close()

	eps := []resolver.Endpoint{{Addresses: []resolver.Address{{Addr: "a"}}}, {Addresses: []resolver.Address{{Addr: "b"}}}}
	upd := func(gen int64) error {
		return es.UpdateClientConnState(balancer.ClientConnState{BalancerConfig: &d6Cfg{gen: gen}, ResolverState: resolver.State{Endpoints: eps}})
	}
	if err := upd(1); err != nil {
		t.Fatal(err)
	}
	cc.mu.Lock()
	t.Logf("after batch gen1: %q", cc.notices)
	cc.notices = nil
	cc.mu.Unlock()

	// 1. Out-of-batch child callback passes the inhibition check, then pauses.
	armed.Store(true)
	mu.Lock()
	stale := children["0"]
	mu.Unlock()
	cbDone := make(chan struct{})
	go func() { stale.report(); close(cbDone) }()
	<-passed
	t.Log("step1: out-of-batch callback passed inhibitChildUpdates check (batch not yet entered)")

	// 2. Batch entry: gen2 is applied to one child; the other child's update is still in progress.
	batchDone := make(chan error, 1)
	go func() { batchDone <- upd(2) }()
	<-firstApplied
	<-secondBlocked
	t.Log("step2: batch gen2 entered; first child applied gen2, second child update still unfinished")

	// 3. The paused callback resumes and publishes while the batch is still open.
	close(release)
	<-cbDone
	cc.mu.Lock()
	midBatch := append([]string(nil), cc.notices...)
	cc.mu.Unlock()
	select {
	case <-batchDone:
		t.Fatal("batch finished early")
	default:
	}
	t.Logf("step3: parent notifications published while batch still open: %q", midBatch)

	close(unblockSecond)
	select {
	case err := <-batchDone:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("batch did not finish")
	}
	cc.mu.Lock()
	t.Logf("after batch gen2 completes: all notifications: %q", cc.notices)
	cc.mu.Unlock()
	if len(midBatch) > 0 {
		t.Errorf("parent notified mid-batch with partially updated children: %q", midBatch)
	}
}
