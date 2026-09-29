// Repro for C5 (branch evalon/grpc-go-en-93bc5f0b). Requires verify/repro/c5_instrumentation.patch
// (adds the testHookAfterInhibitCheck pause point between the inhibitChildUpdates check and es.mu.Lock()).
// Run: git apply verify/repro/c5_instrumentation.patch && cp verify/repro/verify_c5_internal_test.go balancer/endpointsharding/ &&
//
//	go test ./balancer/endpointsharding -run '^TestVerifyC5_' -race -count=1 -v
package endpointsharding

import (
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"google.golang.org/grpc/balancer"
	"google.golang.org/grpc/balancer/base"
	"google.golang.org/grpc/connectivity"
	"google.golang.org/grpc/internal/balancer/stub"
	"google.golang.org/grpc/internal/testutils"
	"google.golang.org/grpc/resolver"
)

func describePicker(p balancer.Picker) string {
	var parts []string
	for _, cs := range ChildStatesFromPicker(p) {
		parts = append(parts, cs.Endpoint.Addresses[0].Addr+"="+cs.State.ConnectivityState.String())
	}
	return strings.Join(parts, ",")
}

func TestVerifyC5_PreBatchDecisionPublishesDuringBatch(t *testing.T) {
	const addrA, addrB = "a", "b"
	var batchArmed atomic.Bool
	var batchCalls atomic.Int32
	secondChildBlocked := make(chan string, 1)
	releaseSecond := make(chan struct{})
	errPicker := base.NewErrPicker(balancer.ErrNoSubConnAvailable)

	name := strings.ToLower(t.Name())
	stub.Register(name, stub.BalancerFuncs{
		UpdateClientConnState: func(bd *stub.BalancerData, ccs balancer.ClientConnState) error {
			addr := ccs.ResolverState.Endpoints[0].Addresses[0].Addr
			if !batchArmed.Load() {
				bd.ClientConn.UpdateState(balancer.State{ConnectivityState: connectivity.Idle, Picker: errPicker})
				return nil
			}
			if batchCalls.Add(1) == 1 {
				// First child of the batch: report CONNECTING (stored, publication inhibited).
				bd.ClientConn.UpdateState(balancer.State{ConnectivityState: connectivity.Connecting, Picker: errPicker})
				return nil
			}
			// Second child of the batch: hold the batch open until released.
			secondChildBlocked <- addr
			<-releaseSecond
			bd.ClientConn.UpdateState(balancer.State{ConnectivityState: connectivity.Ready, Picker: errPicker})
			return nil
		},
	})

	tcc := testutils.NewBalancerClientConn(t)
	es := NewBalancer(tcc, balancer.BuildOptions{}, balancer.Get(name).Build, Options{DisableAutoReconnect: true}).(*endpointSharding)
	defer es.Close()
	ccs := balancer.ClientConnState{ResolverState: resolver.State{Endpoints: []resolver.Endpoint{
		{Addresses: []resolver.Address{{Addr: addrA}}},
		{Addresses: []resolver.Address{{Addr: addrB}}},
	}}}
	if err := es.UpdateClientConnState(ccs); err != nil {
		t.Fatalf("initial UpdateClientConnState() failed: %v", err)
	}
	select {
	case p := <-tcc.NewPickerCh:
		t.Logf("pre-batch picker: %s", describePicker(p))
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for the initial picker")
	}
	var bwA *balancerWrapper
	for ep, bw := range es.children.Load().All() {
		if ep.Addresses[0].Addr == addrA {
			bwA = bw
		}
	}

	// Step 1: a child callback reads inhibitChildUpdates (false -> decides to publish) and is paused
	// before it acquires es.mu.
	paused := make(chan struct{})
	resume := make(chan struct{})
	var pauseArmed atomic.Bool
	testHookAfterInhibitCheck = func() {
		if pauseArmed.CompareAndSwap(true, false) {
			close(paused)
			<-resume
		}
	}
	defer func() { testHookAfterInhibitCheck = nil }()
	pauseArmed.Store(true)
	callbackDone := make(chan struct{})
	go func() {
		defer close(callbackDone)
		bwA.UpdateState(balancer.State{ConnectivityState: connectivity.Idle, Picker: errPicker})
	}()
	select {
	case <-paused:
		t.Logf("child callback for %q passed the inhibitChildUpdates check (inhibit=%v) and is paused before es.mu.Lock()", addrA, es.inhibitChildUpdates.Load())
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for the child callback to reach the pause point")
	}

	// Step 2: enter and hold a configuration batch.
	batchArmed.Store(true)
	batchDone := make(chan error, 1)
	go func() { batchDone <- es.UpdateClientConnState(ccs) }()
	var heldAddr string
	select {
	case heldAddr = <-secondChildBlocked:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for the batch to reach its second child")
	}
	t.Logf("batch active: first child reported CONNECTING, child %q held inside UpdateClientConnState, inhibitChildUpdates=%v", heldAddr, es.inhibitChildUpdates.Load())
	select {
	case p := <-tcc.NewPickerCh:
		t.Fatalf("unexpected publication before resuming the paused callback: %s", describePicker(p))
	case <-time.After(200 * time.Millisecond):
		t.Log("no publication while the batch is held and the callback is paused (inhibition working so far)")
	}

	// Step 3: resume the paused callback while the batch is still held.
	close(resume)
	select {
	case p := <-tcc.NewPickerCh:
		select {
		case err := <-batchDone:
			t.Fatalf("batch completed (err=%v) before the publication was observed; inconclusive", err)
		default:
		}
		var agg string
		select {
		case st := <-tcc.NewStateCh:
			agg = st.String()
		default:
			agg = "<no state>"
		}
		t.Errorf("INTERMEDIATE PUBLICATION DURING ACTIVE BATCH: aggregate=%s children=[%s] (batch still held on child %q, inhibitChildUpdates=%v)", agg, describePicker(p), heldAddr, es.inhibitChildUpdates.Load())
	case <-time.After(3 * time.Second):
		t.Log("no publication after resuming the callback while the batch was held")
	}

	// Step 4: finish the batch and observe the final publication.
	close(releaseSecond)
	select {
	case err := <-batchDone:
		if err != nil {
			t.Fatalf("UpdateClientConnState() failed: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for the batch to complete")
	}
	<-callbackDone
	select {
	case p := <-tcc.NewPickerCh:
		t.Logf("final (end-of-batch) picker: %s", describePicker(p))
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for the end-of-batch picker")
	}
}
