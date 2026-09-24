// Run (on branch evalon/grpc-go-en-f18f8972): `git apply verify/repro/c5_inhibit_hooks_f18f8972.patch && cp verify/repro/c5_batch_boundary_probe_test.go balancer/endpointsharding/ && go test -tags verifyrepro ./balancer/endpointsharding -run '^TestC5BatchBoundaryProbe$' -race -count=1 -v`

//go:build verifyrepro

package endpointsharding

import (
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"google.golang.org/grpc/balancer"
	"google.golang.org/grpc/connectivity"
	"google.golang.org/grpc/resolver"
)

// c5CC records every aggregate state published by endpointSharding.
type c5CC struct {
	balancer.ClientConn
	mu   sync.Mutex
	pubs []string
}

func (c *c5CC) UpdateState(s balancer.State) {
	desc := s.ConnectivityState.String() + "{"
	for _, cs := range ChildStatesFromPicker(s.Picker) {
		desc += fmt.Sprintf(" %s=%s", cs.Endpoint.Addresses[0].Addr, cs.State.ConnectivityState)
	}
	desc += " }"
	c.mu.Lock()
	c.pubs = append(c.pubs, desc)
	c.mu.Unlock()
}

func (c *c5CC) snapshot() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]string(nil), c.pubs...)
}

// c5Child is a child balancer whose UpdateClientConnState behaviour is scripted.
type c5Child struct {
	cc       balancer.ClientConn
	onUpdate func(c *c5Child)
}

func (c *c5Child) UpdateClientConnState(balancer.ClientConnState) error {
	if c.onUpdate != nil {
		c.onUpdate(c)
	}
	return nil
}
func (c *c5Child) ResolverError(error)                                        {}
func (c *c5Child) UpdateSubConnState(balancer.SubConn, balancer.SubConnState) {}
func (c *c5Child) Close()                                                     {}
func (c *c5Child) ExitIdle()                                                  {}

// pausePoint pauses exactly one updateState caller (the first one after arm()).
type pausePoint struct {
	armed   atomic.Bool
	paused  chan struct{}
	release chan struct{}
}

func newPausePoint() *pausePoint {
	return &pausePoint{paused: make(chan struct{}), release: make(chan struct{})}
}
func (p *pausePoint) hook() {
	if p.armed.CompareAndSwap(true, false) {
		close(p.paused)
		<-p.release
	}
}

func waitOrFatal(t *testing.T, ch <-chan struct{}, what string) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(3 * time.Second):
		t.Fatalf("timed out waiting for %s", what)
	}
}

var (
	c5EpA = resolver.Endpoint{Addresses: []resolver.Address{{Addr: "A"}}}
	c5EpB = resolver.Endpoint{Addresses: []resolver.Address{{Addr: "B"}}}
)

func c5Setup(t *testing.T) (*endpointSharding, *c5CC, map[string]*c5Child) {
	t.Helper()
	defer func(r func(int) int) { randIntN = r }(randIntN)
	randIntN = func(int) int { return 0 } // deterministic child order: B then A
	children := map[string]*c5Child{}
	var mu sync.Mutex
	builder := func(cc balancer.ClientConn, _ balancer.BuildOptions) balancer.Balancer {
		c := &c5Child{cc: cc}
		mu.Lock()
		children[cc.(*balancerWrapper).childState.Endpoint.Addresses[0].Addr] = c
		mu.Unlock()
		return c
	}
	cc := &c5CC{}
	es := NewBalancer(cc, balancer.BuildOptions{}, builder, Options{DisableAutoReconnect: true}).(*endpointSharding)
	if err := es.UpdateClientConnState(balancer.ClientConnState{ResolverState: resolver.State{Endpoints: []resolver.Endpoint{c5EpB, c5EpA}}}); err != nil {
		t.Fatalf("initial UpdateClientConnState: %v", err)
	}
	return es, cc, children
}

func TestC5BatchBoundaryProbe(t *testing.T) {
	defer func(r func(int) int) { randIntN = r }(randIntN)
	randIntN = func(int) int { return 0 }

	t.Run("BatchEntry", func(t *testing.T) {
		after := newPausePoint()
		testHookAfterInhibitCheck = after.hook
		defer func() { testHookAfterInhibitCheck = nil }()

		es, cc, children := c5Setup(t)
		defer es.Close()
		base := len(cc.snapshot())

		aBlocked, releaseA := make(chan struct{}), make(chan struct{})
		children["A"].onUpdate = func(*c5Child) { close(aBlocked); <-releaseA }
		children["B"].onUpdate = func(c *c5Child) { c.cc.UpdateState(balancer.State{ConnectivityState: connectivity.Ready}) }

		// 1. A pre-batch child callback passes the inhibition check and pauses.
		after.armed.Store(true)
		bwB, _ := es.children.Load().Get(c5EpB)
		go bwB.UpdateState(balancer.State{ConnectivityState: connectivity.Connecting})
		waitOrFatal(t, after.paused, "child B callback to pass inhibition check")

		// 2. A configuration batch begins: B is updated (reports READY, inhibited), A blocks.
		batchDone := make(chan struct{})
		go func() {
			es.UpdateClientConnState(balancer.ClientConnState{ResolverState: resolver.State{Endpoints: []resolver.Endpoint{c5EpB, c5EpA}}})
			close(batchDone)
		}()
		waitOrFatal(t, aBlocked, "child A update to block inside batch")
		if got := cc.snapshot()[base:]; len(got) != 0 {
			t.Fatalf("unexpected publications before releasing callback: %v", got)
		}

		// 3. Release the paused callback while the batch is still blocked on A.
		close(after.release)
		deadline := time.Now().Add(2 * time.Second)
		for len(cc.snapshot()) == base && time.Now().Before(deadline) {
			time.Sleep(time.Millisecond)
		}
		select {
		case <-batchDone:
			t.Fatal("batch completed unexpectedly while A was blocked")
		default:
		}
		midBatch := cc.snapshot()[base:]
		t.Logf("publications while batch still blocked on A: %d -> %v", len(midBatch), midBatch)
		close(releaseA)
		waitOrFatal(t, batchDone, "batch to complete")
		t.Logf("all publications since batch start: %v", cc.snapshot()[base:])
		if len(midBatch) != 0 {
			t.Errorf("BATCH-ENTRY RACE: %d aggregate publication(s) during an inhibited batch with A still blocked: %v", len(midBatch), midBatch)
		}
	})

	t.Run("BatchCompletion", func(t *testing.T) {
		before := newPausePoint()
		testHookBeforeInhibitCheck = before.hook
		defer func() { testHookBeforeInhibitCheck = nil }()

		es, cc, children := c5Setup(t)
		defer es.Close()
		base := len(cc.snapshot())

		aBlocked, releaseA := make(chan struct{}), make(chan struct{})
		children["A"].onUpdate = func(*c5Child) { close(aBlocked); <-releaseA }

		// 1. A batch begins and blocks on A.
		batchDone := make(chan struct{})
		go func() {
			es.UpdateClientConnState(balancer.ClientConnState{ResolverState: resolver.State{Endpoints: []resolver.Endpoint{c5EpB, c5EpA}}})
			close(batchDone)
		}()
		waitOrFatal(t, aBlocked, "child A update to block inside batch")

		// 2. During the batch, child B records READY and pauses before its inhibition check.
		before.armed.Store(true)
		bwB, _ := es.children.Load().Get(c5EpB)
		go bwB.UpdateState(balancer.State{ConnectivityState: connectivity.Ready})
		waitOrFatal(t, before.paused, "child B callback to record state and pause")

		// 3. The batch completes and publishes the consolidated state (includes B=READY).
		close(releaseA)
		waitOrFatal(t, batchDone, "batch to complete")
		consolidated := cc.snapshot()[base:]
		t.Logf("consolidated publication(s) at batch completion: %v", consolidated)

		// 4. Release the held callback and see whether it publishes the same state again.
		close(before.release)
		deadline := time.Now().Add(2 * time.Second)
		for len(cc.snapshot()) == base+len(consolidated) && time.Now().Before(deadline) {
			time.Sleep(time.Millisecond)
		}
		all := cc.snapshot()[base:]
		t.Logf("publications after releasing held callback: %v", all)
		if len(all) > len(consolidated) {
			t.Errorf("BATCH-COMPLETION RACE: held callback re-published state already included in the consolidated update: %v", all[len(consolidated):])
		}
	})
}
