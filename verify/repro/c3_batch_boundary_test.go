//go:build repro

// Run (branch evalon/grpc-go-en-2c006c7f of kaitranntt-evals/grpc-go-endpointsharding-decouple-locking):
//
//	cp verify/repro/c3_batch_boundary_test.go balancer/endpointsharding/ && go test ./balancer/endpointsharding -run 'TestC3_' -tags repro -race -count=1 -v
//
// C3 repro, completion boundary (no production instrumentation needed): the
// parent ClientConn records every publication it receives; child pickers carry
// a sequence number so that two publications with identical content can be
// recognised. Shared helpers (c3ProbeCC, c3Child, c3Describe) are also used by
// c3_admission_hook_test.go.
package endpointsharding

import (
	"fmt"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"google.golang.org/grpc/attributes"
	"google.golang.org/grpc/balancer"
	"google.golang.org/grpc/connectivity"
	"google.golang.org/grpc/resolver"
)

type c3Picker struct {
	seq int64
	tag string
}

func (p *c3Picker) Pick(balancer.PickInfo) (balancer.PickResult, error) {
	return balancer.PickResult{}, nil
}

type c3ProbeCC struct {
	balancer.ClientConn
	mu      sync.Mutex
	updates []balancer.State
	// fromBatch[i] is true when updates[i] was published from within
	// endpointSharding.UpdateClientConnState (the batch's own publication).
	fromBatch []bool
}

func (cc *c3ProbeCC) UpdateState(s balancer.State) {
	buf := make([]byte, 1<<14)
	n := runtime.Stack(buf, false)
	fromBatch := strings.Contains(string(buf[:n]), "(*endpointSharding).UpdateClientConnState")
	cc.mu.Lock()
	cc.updates = append(cc.updates, s)
	cc.fromBatch = append(cc.fromBatch, fromBatch)
	cc.mu.Unlock()
}

func (cc *c3ProbeCC) snapshot() []balancer.State {
	cc.mu.Lock()
	defer cc.mu.Unlock()
	return append([]balancer.State(nil), cc.updates...)
}

type c3Child struct {
	cc       balancer.ClientConn
	onUpdate func(cc balancer.ClientConn, ccs balancer.ClientConnState)
}

func (c *c3Child) UpdateClientConnState(ccs balancer.ClientConnState) error {
	c.onUpdate(c.cc, ccs)
	return nil
}
func (c *c3Child) ResolverError(error)                                        {}
func (c *c3Child) UpdateSubConnState(balancer.SubConn, balancer.SubConnState) {}
func (c *c3Child) Close()                                                     {}
func (c *c3Child) ExitIdle()                                                  {}

type c3VersionKey struct{}

func c3Endpoint(addr string, v int) resolver.Endpoint {
	return resolver.Endpoint{Addresses: []resolver.Address{{Addr: addr}}, Attributes: attributes.New(c3VersionKey{}, v)}
}

// describe renders a publication as "A(v=<attr>,<tag>#<seq>) B(v=<attr>,<tag>#<seq>)".
func c3Describe(s balancer.State) string {
	var parts []string
	for _, cs := range ChildStatesFromPicker(s.Picker) {
		v, _ := cs.Endpoint.Attributes.Value(c3VersionKey{}).(int)
		tag, seq := "<nil>", int64(-1)
		if p, ok := cs.State.Picker.(*c3Picker); ok {
			tag, seq = p.tag, p.seq
		}
		parts = append(parts, fmt.Sprintf("%s(v=%d,%s,%s#%d)", cs.Endpoint.Addresses[0].Addr, v, cs.State.ConnectivityState, tag, seq))
	}
	sort.Strings(parts) // child order in the picker is map-iteration order
	return strings.Join(parts, " ")
}

// Completion boundary: a callback that recorded its state while the batch was
// in flight (and was therefore inhibited from publishing) later publishes the
// same consolidated state again after the batch's final publication, so the
// parent sees the consolidated batch state twice with no new child state in
// between.
func TestC3_CompletionBoundaryDuplicatesFinalPublication(t *testing.T) {
	const attempts = 40
	dups := 0
	for attempt := 1; attempt <= attempts; attempt++ {
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

		batchDone := make(chan error, 1)
		go func() {
			batchDone <- es.UpdateClientConnState(balancer.ClientConnState{ResolverState: resolver.State{Endpoints: []resolver.Endpoint{c3Endpoint("a", 1), c3Endpoint("b", 1)}}})
		}()
		select {
		case <-bEntered:
		case <-time.After(5 * time.Second):
			t.Fatal("batch never reached child B")
		}
		// Batch is in flight (B blocked). Spinners record A's state now; they
		// are inhibited from publishing while the batch is in flight.
		var seq atomic.Int64
		stop := make(chan struct{})
		var wg sync.WaitGroup
		for i := 0; i < 4; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for {
					select {
					case <-stop:
						return
					default:
					}
					a.UpdateState(balancer.State{ConnectivityState: connectivity.Ready, Picker: &c3Picker{seq: seq.Add(1), tag: "cb"}})
				}
			}()
		}
		time.Sleep(time.Millisecond)
		close(bRelease) // batch finishes and publishes its consolidated state
		select {
		case err := <-batchDone:
			if err != nil {
				t.Fatalf("batch UpdateClientConnState: %v", err)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("batch did not finish")
		}
		time.Sleep(time.Millisecond)
		close(stop)
		wg.Wait()

		// Locate the batch's own final publication (published from the
		// deferred function of UpdateClientConnState, identified by the stack
		// recorded by the probe) and check whether the publication right
		// after it is identical: same A record (seq), same attributes, same B
		// state. A correct implementation publishes each child record at most
		// once, so the callback publication following the batch must carry a
		// newer A record.
		all := cc.snapshot()
		for i, s := range all {
			if !cc.fromBatch[i] || !containsBatchData(s) {
				continue // the initial (v=0) batch publication, or a callback
			}
			if i+1 < len(all) && c3Describe(all[i+1]) == c3Describe(s) && !cc.fromBatch[i+1] {
				dups++
				t.Logf("attempt %d: batch final publication #%d re-published by a child callback as #%d: %s", attempt, i, i+1, c3Describe(s))
			}
			break
		}
		es.Close()
	}
	t.Logf("attempts=%d duplicate-consolidated-publications=%d", attempts, dups)
	if dups > 0 {
		t.Errorf("completion boundary NOT protected: %d/%d attempts published the consolidated batch state twice", dups, attempts)
	}
}

func containsBatchData(s balancer.State) bool {
	for _, cs := range ChildStatesFromPicker(s.Picker) {
		if cs.Endpoint.Addresses[0].Addr == "b" {
			if p, ok := cs.State.Picker.(*c3Picker); ok && p.tag == "b-sync-v1" {
				return true
			}
		}
	}
	return false
}
