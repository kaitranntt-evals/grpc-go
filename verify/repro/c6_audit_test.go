// Run: verify/repro/c6_publication_race.sh   (C6 probe; FAILS when a partial aggregate is published during a batch)

//go:build ignore

package endpointsharding

import (
	"sync"
	"testing"
	"time"

	"google.golang.org/grpc/balancer"
	"google.golang.org/grpc/balancer/base"
	"google.golang.org/grpc/connectivity"
	"google.golang.org/grpc/resolver"
)

type auditC6Pub struct {
	agg         connectivity.State
	childs      map[string]connectivity.State
	duringBatch bool // inhibitChildUpdates was set when the parent published
	picker      balancer.Picker
}

type auditC6CC struct {
	balancer.ClientConn
	es   *endpointSharding
	mu   sync.Mutex
	pubs []auditC6Pub
}

func (c *auditC6CC) UpdateState(s balancer.State) {
	p := auditC6Pub{agg: s.ConnectivityState, childs: map[string]connectivity.State{}, duringBatch: c.es.inhibitChildUpdates.Load(), picker: s.Picker}
	for _, cs := range ChildStatesFromPicker(s.Picker) {
		p.childs[cs.Endpoint.Addresses[0].Addr] = cs.State.ConnectivityState
	}
	c.mu.Lock()
	c.pubs = append(c.pubs, p)
	c.mu.Unlock()
}

func (c *auditC6CC) take() []auditC6Pub {
	c.mu.Lock()
	defer c.mu.Unlock()
	p := c.pubs
	c.pubs = nil
	return p
}

type auditC6Child struct {
	cc       balancer.ClientConn
	addr     string
	updates  int
	bUpdated chan struct{}
}

func (c *auditC6Child) UpdateClientConnState(ccs balancer.ClientConnState) error {
	c.addr = ccs.ResolverState.Endpoints[0].Addresses[0].Addr
	c.updates++
	st := connectivity.Ready
	if c.updates == 2 {
		st = connectivity.TransientFailure // the configuration batch's new state
	}
	c.cc.UpdateState(balancer.State{ConnectivityState: st, Picker: base.NewErrPicker(balancer.ErrNoSubConnAvailable)})
	if c.addr == "b" && c.updates == 2 {
		close(c.bUpdated)
	}
	return nil
}
func (c *auditC6Child) ResolverError(error)                                        {}
func (c *auditC6Child) UpdateSubConnState(balancer.SubConn, balancer.SubConnState) {}
func (c *auditC6Child) Close()                                                     {}
func (c *auditC6Child) ExitIdle() {
	// Synchronous state callback from the idle-exit worker.
	c.cc.UpdateState(balancer.State{ConnectivityState: connectivity.Connecting, Picker: base.NewErrPicker(balancer.ErrNoSubConnAvailable)})
}

func TestAuditC6PublicationDuringBatch(t *testing.T) {
	orig := randIntN
	randIntN = func(int) int { return 0 } // process endpoints in the given order: b, then a
	defer func() { randIntN = orig }()

	bUpdated := make(chan struct{})
	cc := &auditC6CC{}
	b := NewBalancer(cc, balancer.BuildOptions{}, func(cc balancer.ClientConn, _ balancer.BuildOptions) balancer.Balancer {
		return &auditC6Child{cc: cc, bUpdated: bUpdated}
	}, Options{DisableAutoReconnect: true})
	cc.es = b.(*endpointSharding)
	defer b.Close()
	ccs := balancer.ClientConnState{ResolverState: resolver.State{Endpoints: []resolver.Endpoint{
		{Addresses: []resolver.Address{{Addr: "b"}}}, {Addresses: []resolver.Address{{Addr: "a"}}},
	}}}
	if err := b.UpdateClientConnState(ccs); err != nil {
		t.Fatal(err)
	}
	pubs := cc.take()
	t.Logf("initial update: %d publication(s), last: agg=%v children=%v", len(pubs), pubs[len(pubs)-1].agg, pubs[len(pubs)-1].childs)
	var childA ChildState
	for _, cs := range ChildStatesFromPicker(pubs[len(pubs)-1].picker) {
		if cs.Endpoint.Addresses[0].Addr == "a" {
			childA = cs
		}
	}

	// Pause the idle-exit worker's callback right after it passed the inhibit check.
	paused, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	auditAfterInhibitCheck = func() { once.Do(func() { close(paused); <-release }) }
	childA.ExitIdle()
	<-paused
	t.Logf("child A's callback passed the inhibit check (inhibit=%v) and is paused before es.mu", cc.es.inhibitChildUpdates.Load())

	// Begin a configuration batch; it updates b (-> TRANSIENT_FAILURE) and then blocks on a's child mutex.
	done := make(chan struct{})
	go func() { defer close(done); b.UpdateClientConnState(ccs) }()
	<-bUpdated
	time.Sleep(50 * time.Millisecond)
	select {
	case <-done:
		t.Fatal("batch completed unexpectedly early")
	default:
	}
	t.Logf("batch in progress (inhibit=%v): b processed, a not yet; publications so far: %d", cc.es.inhibitChildUpdates.Load(), len(cc.take()))
	close(release)
	<-done
	auditAfterInhibitCheck = nil
	for i, p := range cc.take() {
		t.Logf("publication %d: duringBatch=%v agg=%v children=%v", i+1, p.duringBatch, p.agg, p.childs)
		if p.duringBatch {
			t.Errorf("parent published a partially updated aggregate during the configuration batch: agg=%v children=%v", p.agg, p.childs)
		}
	}
}
