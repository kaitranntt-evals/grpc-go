// Run: copy this file to balancer/endpointsharding/ on branch evalon/grpc-go-en-385e4622, then `go test -tags verifyrepro ./balancer/endpointsharding -run '^TestC2LockNestingProbe$' -race -count=1 -v`

//go:build verifyrepro

package endpointsharding

import (
	"testing"

	"google.golang.org/grpc/balancer"
	"google.golang.org/grpc/balancer/base"
	"google.golang.org/grpc/connectivity"
	"google.golang.org/grpc/internal/testutils"
	"google.golang.org/grpc/resolver"
)

// probeChild reports CONNECTING synchronously from UpdateClientConnState, which
// is a supported child behaviour. Before it does so it records whether the
// wrapper's per-child mutex is held; bw.UpdateState then acquires es.mu while
// that per-child mutex is still held by updateClientConnState.
type probeChild struct {
	bw           *balancerWrapper
	childMuHeld  bool
	parentMuFree bool
}

func (p *probeChild) UpdateClientConnState(balancer.ClientConnState) error {
	// TryLock fails iff bw.mu is currently held (by bw.updateClientConnState).
	p.childMuHeld = !p.bw.mu.TryLock()
	if !p.childMuHeld {
		p.bw.mu.Unlock()
	}
	// es.mu must be free here: bw.UpdateState below acquires it while bw.mu is
	// still held, i.e. both mutexes are owned simultaneously.
	p.parentMuFree = p.bw.es.mu.TryLock()
	if p.parentMuFree {
		p.bw.es.mu.Unlock()
	}
	p.bw.UpdateState(balancer.State{ConnectivityState: connectivity.Connecting, Picker: base.NewErrPicker(balancer.ErrNoSubConnAvailable)})
	return nil
}
func (p *probeChild) ResolverError(error)                                        {}
func (p *probeChild) UpdateSubConnState(balancer.SubConn, balancer.SubConnState) {}
func (p *probeChild) Close()                                                     {}
func (p *probeChild) ExitIdle()                                                  {}

func TestC2LockNestingProbe(t *testing.T) {
	var child *probeChild
	builder := func(cc balancer.ClientConn, _ balancer.BuildOptions) balancer.Balancer {
		child = &probeChild{bw: cc.(*balancerWrapper)}
		return child
	}
	es := NewBalancer(testutils.NewBalancerClientConn(t), balancer.BuildOptions{}, builder, Options{DisableAutoReconnect: true})
	defer es.Close()
	ccs := balancer.ClientConnState{ResolverState: resolver.State{Endpoints: []resolver.Endpoint{{Addresses: []resolver.Address{{Addr: "a"}}}}}}
	if err := es.UpdateClientConnState(ccs); err != nil {
		t.Fatalf("UpdateClientConnState() = %v", err)
	}
	t.Logf("during synchronous child UpdateState callback: balancerWrapper.mu held = %v, es.mu free before callback = %v", child.childMuHeld, child.parentMuFree)
	if !child.childMuHeld || !child.parentMuFree {
		t.Fatalf("expected bw.mu held and es.mu free at callback entry; got bw.mu held=%v es.mu free=%v", child.childMuHeld, child.parentMuFree)
	}
	// bw.UpdateState returned, so it acquired es.mu (endpointsharding.go: bw.es.mu.Lock())
	// while bw.mu was held: simultaneous ownership bw.mu -> es.mu occurred.
	t.Log("bw.UpdateState acquired es.mu while bw.mu was held (nested ownership bw.mu -> es.mu observed)")
}
