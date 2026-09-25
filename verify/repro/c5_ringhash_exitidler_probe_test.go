//go:build repro

// Run (branch evalon/grpc-go-en-5bb04be3 of kaitranntt-evals/grpc-go-endpointsharding-decouple-locking):
//
//	cp verify/repro/c5_ringhash_exitidler_probe_test.go balancer/ringhash/ && go test ./balancer/ringhash -run 'TestC5_' -tags repro -race -count=1 -v
//
// C5 probe: how ringhash stores the child handle received in an
// endpointsharding ChildState and how it invokes idle exit from
// updatePickerLocked and from the picker.
package ringhash

import (
	"context"
	"reflect"
	"sync"
	"testing"
	"time"

	"google.golang.org/grpc/balancer"
	"google.golang.org/grpc/balancer/endpointsharding"
	"google.golang.org/grpc/connectivity"
	iringhash "google.golang.org/grpc/internal/ringhash"
	"google.golang.org/grpc/resolver"
)

type c5ProbeCC struct {
	balancer.ClientConn
	mu     sync.Mutex
	states []balancer.State
}

func (cc *c5ProbeCC) UpdateState(s balancer.State) {
	cc.mu.Lock()
	defer cc.mu.Unlock()
	cc.states = append(cc.states, s)
}

func (cc *c5ProbeCC) last() balancer.State {
	cc.mu.Lock()
	defer cc.mu.Unlock()
	return cc.states[len(cc.states)-1]
}

type c5Child struct {
	cc       balancer.ClientConn
	exitIdle chan struct{}
}

func (c *c5Child) UpdateClientConnState(balancer.ClientConnState) error {
	c.cc.UpdateState(balancer.State{ConnectivityState: connectivity.Idle, Picker: c5IdlePicker{}})
	return nil
}
func (c *c5Child) ResolverError(error)                                        {}
func (c *c5Child) UpdateSubConnState(balancer.SubConn, balancer.SubConnState) {}
func (c *c5Child) Close()                                                     {}
func (c *c5Child) ExitIdle() {
	select {
	case c.exitIdle <- struct{}{}:
	default:
	}
}

type c5IdlePicker struct{}

func (c5IdlePicker) Pick(balancer.PickInfo) (balancer.PickResult, error) {
	return balancer.PickResult{}, balancer.ErrNoSubConnAvailable
}

func TestC5_RinghashStoresChildBehindLegacyExitIdlerInterface(t *testing.T) {
	// 1. Static representation.
	field, ok := reflect.TypeOf(endpointState{}).FieldByName("balancer")
	if !ok {
		t.Fatal("endpointState has no field named balancer")
	}
	t.Logf("ringhash endpointState.balancer static type = %s (kind=%s)", field.Type, field.Type.Kind())
	exitIdlerT := reflect.TypeOf((*endpointsharding.ExitIdler)(nil)).Elem()
	t.Logf("endpointsharding.ExitIdler declared as %s with %d method(s): %s", exitIdlerT.Kind(), exitIdlerT.NumMethod(), exitIdlerT.Method(0).Name)
	if field.Type != exitIdlerT {
		t.Errorf("endpointState.balancer is %s, not endpointsharding.ExitIdler", field.Type)
	}
	if _, hasField := reflect.TypeOf(endpointsharding.ChildState{}).FieldByName("ExitIdle"); hasField {
		t.Logf("ChildState has an ExitIdle func field")
	} else if m, hasMethod := reflect.TypeOf(endpointsharding.ChildState{}).MethodByName("ExitIdle"); hasMethod {
		t.Logf("ChildState exposes ExitIdle as a method: %s", m.Type)
	}

	// 2. Dynamic representation and invocation path.
	parent := &c5ProbeCC{}
	rh := &ringhashBalancer{
		ClientConn:     parent,
		config:         &iringhash.LBConfig{MinRingSize: 1, MaxRingSize: 10, RequestHashHeader: "c5-hash"},
		endpointStates: resolver.NewEndpointMap[*endpointState](),
	}
	rh.logger = prefixLogger(rh)

	child := &c5Child{exitIdle: make(chan struct{}, 1)}
	builder := func(cc balancer.ClientConn, _ balancer.BuildOptions) balancer.Balancer {
		child.cc = cc
		return child
	}
	es := endpointsharding.NewBalancer(rh, balancer.BuildOptions{}, builder, endpointsharding.Options{DisableAutoReconnect: true})
	defer es.Close()
	if err := es.UpdateClientConnState(balancer.ClientConnState{ResolverState: resolver.State{Endpoints: []resolver.Endpoint{{Addresses: []resolver.Address{{Addr: "10.0.0.1:1"}}}}}}); err != nil {
		t.Fatalf("UpdateClientConnState: %v", err)
	}

	rh.mu.Lock()
	var stored *endpointState
	for _, s := range rh.endpointStates.All() {
		stored = s
	}
	rh.mu.Unlock()
	if stored == nil {
		t.Fatal("ringhash recorded no endpoint state")
	}
	t.Logf("value stored in endpointState.balancer has dynamic type %T (boxed in interface %s)", stored.balancer, field.Type)
	if _, isChildState := stored.balancer.(endpointsharding.ChildState); !isChildState {
		t.Errorf("stored value is %T, want endpointsharding.ChildState boxed in the ExitIdler interface", stored.balancer)
	}

	// Invocation through the picker: with a request hash header configured and
	// no metadata, the picker walks the ring from a random hash and calls
	// es.balancer.ExitIdle() (interface call) on the first Idle endpoint.
	pickInfo := balancer.PickInfo{Ctx: context.Background()}
	if _, err := parent.last().Picker.Pick(pickInfo); err != balancer.ErrNoSubConnAvailable {
		t.Fatalf("Pick() error = %v, want %v", err, balancer.ErrNoSubConnAvailable)
	}
	select {
	case <-child.exitIdle:
		t.Logf("picker path: es.balancer.ExitIdle() (interface call) reached the child's ExitIdle")
	case <-time.After(5 * time.Second):
		t.Fatal("picker path did not deliver ExitIdle to the child")
	}
}
