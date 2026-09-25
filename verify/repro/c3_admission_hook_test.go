//go:build repro

// Run (branch evalon/grpc-go-en-2c006c7f of kaitranntt-evals/grpc-go-endpointsharding-decouple-locking), after applying
// the instrumentation patch verify/repro/c3_admission_hook.patch (adds a pause hook after the inhibition check):
//
//	git apply verify/repro/c3_admission_hook.patch && cp verify/repro/c3_batch_boundary_test.go verify/repro/c3_admission_hook_test.go balancer/endpointsharding/ && go test ./balancer/endpointsharding -run 'TestC3Hook_' -tags repro -race -count=1 -v
//
// C3 admission boundary, deterministic schedule: a child callback passes the
// inhibition check (inhibit=false), is paused, a batch starts and configures
// child A (new attributes, new synchronous state) and blocks in child B; the
// paused callback then resumes and publishes.
package endpointsharding

import (
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"google.golang.org/grpc/balancer"
	"google.golang.org/grpc/connectivity"
	"google.golang.org/grpc/resolver"
)

func TestC3Hook_AdmissionBoundaryPublishesPartialBatch(t *testing.T) {
	var pauseArmed atomic.Bool
	pauseEntered := make(chan struct{}, 1)
	pauseRelease := make(chan struct{})
	c3TestHookAfterInhibitCheck = func() {
		if pauseArmed.CompareAndSwap(true, false) {
			pauseEntered <- struct{}{}
			<-pauseRelease
		}
	}
	defer func() { c3TestHookAfterInhibitCheck = nil }()

	cc := &c3ProbeCC{}
	bEntered := make(chan struct{}, 1)
	bRelease := make(chan struct{})
	var aCC atomic.Pointer[balancer.ClientConn]
	builder := func(childCC balancer.ClientConn, _ balancer.BuildOptions) balancer.Balancer {
		return &c3Child{cc: childCC, onUpdate: func(childCC balancer.ClientConn, ccs balancer.ClientConnState) {
			ep := ccs.ResolverState.Endpoints[0]
			v := ep.Attributes.Value(c3VersionKey{}).(int)
			switch ep.Addresses[0].Addr {
			case "a":
				aCC.Store(&childCC)
				childCC.UpdateState(balancer.State{ConnectivityState: connectivity.Connecting, Picker: &c3Picker{tag: "a-sync-v" + strconv.Itoa(v)}})
			case "b":
				if v == 1 {
					bEntered <- struct{}{}
					<-bRelease
				}
				childCC.UpdateState(balancer.State{ConnectivityState: connectivity.Connecting, Picker: &c3Picker{tag: "b-sync-v" + strconv.Itoa(v)}})
			}
		}}
	}
	es := NewBalancer(cc, balancer.BuildOptions{}, builder, Options{DisableAutoReconnect: true})
	if err := es.UpdateClientConnState(balancer.ClientConnState{ResolverState: resolver.State{Endpoints: []resolver.Endpoint{c3Endpoint("a", 0), c3Endpoint("b", 0)}}}); err != nil {
		t.Fatalf("initial UpdateClientConnState: %v", err)
	}
	a := *aCC.Load()
	t.Logf("publication #0 (initial batch): %s", c3Describe(cc.snapshot()[0]))

	// 1. A child callback (from a running child, outside any batch) passes the
	// inhibition check and is paused before it acquires es.mu.
	pauseArmed.Store(true)
	cbDone := make(chan struct{})
	go func() {
		defer close(cbDone)
		a.UpdateState(balancer.State{ConnectivityState: connectivity.Ready, Picker: &c3Picker{seq: 7, tag: "cb"}})
	}()
	select {
	case <-pauseEntered:
	case <-time.After(5 * time.Second):
		t.Fatal("callback never reached the hook")
	}

	// 2. A batch starts: inhibit=true, A's endpoint attributes are updated to
	// v=1 and A is configured (it reports state synchronously); B blocks.
	batchDone := make(chan error, 1)
	go func() {
		batchDone <- es.UpdateClientConnState(balancer.ClientConnState{ResolverState: resolver.State{Endpoints: []resolver.Endpoint{c3Endpoint("a", 1), c3Endpoint("b", 1)}}})
	}()
	select {
	case <-bEntered:
	case <-time.After(5 * time.Second):
		t.Fatal("batch never reached child B")
	}
	before := len(cc.snapshot())

	// 3. The paused callback resumes while B's configuration is unfinished.
	close(pauseRelease)
	select {
	case <-cbDone:
	case <-time.After(5 * time.Second):
		t.Fatal("callback did not complete")
	}
	during := cc.snapshot()[before:]
	for _, s := range during {
		t.Logf("publication received while B's batch configuration was still blocked: %s", c3Describe(s))
		// The batch processes endpoints in a rotated (random) order, so
		// either child may already carry the batch's v=1 attributes while
		// B is blocked. Any v=1 record published here is batch data that
		// escaped before the batch completed (B's own state is still v0).
		for _, cs := range ChildStatesFromPicker(s.Picker) {
			if cs.Endpoint.Attributes.Value(c3VersionKey{}) == 1 {
				t.Errorf("admission boundary NOT protected: parent received %s with v=1 (batch data) while the batch was still blocked in B (B state still b-sync-v0)", cs.Endpoint.Addresses[0].Addr)
				break
			}
		}
	}
	if len(during) == 0 {
		t.Logf("no publication crossed the admission boundary")
	}

	close(bRelease)
	select {
	case err := <-batchDone:
		if err != nil {
			t.Fatalf("batch UpdateClientConnState: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("batch did not finish")
	}
	all := cc.snapshot()
	t.Logf("final publication #%d (batch): %s", len(all)-1, c3Describe(all[len(all)-1]))
	es.Close()
}
