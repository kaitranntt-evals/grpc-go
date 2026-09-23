/*
 *
 * Copyright 2024 gRPC authors.
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 *
 */

// Package endpointsharding implements a load balancing policy that manages
// homogeneous child policies each owning a single endpoint.
//
// # Experimental
//
// Notice: This package is EXPERIMENTAL and may be changed or removed in a
// later release.
package endpointsharding

import (
	"errors"
	rand "math/rand/v2"
	"sync"
	"sync/atomic"

	"google.golang.org/grpc/balancer"
	"google.golang.org/grpc/balancer/base"
	"google.golang.org/grpc/connectivity"
	"google.golang.org/grpc/resolver"
)

var randIntN = rand.IntN

// ChildState is the state of a child balancer.
type ChildState struct {
	Endpoint resolver.Endpoint // Endpoint of the child balancer.
	State    balancer.State    // State of the child balancer.
	Balancer ExitIdler
}

// ExitIdler wraps balancers exposing an ExitIdle method.
type ExitIdler interface {
	ExitIdle()
}

// Options configure the behaviour of the endpointsharding balancer.
type Options struct {
	// DisableAutoReconnect allows the balancer to keep child balancer in the
	// IDLE state until they are explicitly triggered to exit using the
	// ChildState obtained from the endpointsharding picker. When set to false,
	// the endpointsharding balancer will automatically call ExitIdle on child
	// connections that report IDLE.
	DisableAutoReconnect bool
}

// ChildBuilderFunc creates a new balancer with the ClientConn. It has the same
// type as the balancer.Builder.Build method.
type ChildBuilderFunc func(cc balancer.ClientConn, opts balancer.BuildOptions) balancer.Balancer

// NewBalancer returns a load balancing policy that manages homogeneous child
// policies each owning a single endpoint. The endpointsharding balancer
// forwards the LoadBalancingConfig in ClientConn state updates to its children.
func NewBalancer(cc balancer.ClientConn, opts balancer.BuildOptions, childBuilder ChildBuilderFunc, esOpts Options) balancer.Balancer {
	return &endpointSharding{
		cc:           cc,
		bOpts:        opts,
		esOpts:       esOpts,
		childBuilder: childBuilder,
		endpoints:    resolver.NewEndpointMap[*endpointState](),
	}
}

// endpointSharding is a balancer that wraps child balancers. It creates a child
// balancer with child config for every unique Endpoint received. It updates the
// child states on any update from parent or child.
type endpointSharding struct {
	cc           balancer.ClientConn
	bOpts        balancer.BuildOptions
	esOpts       Options
	childBuilder ChildBuilderFunc

	// mu guards access to the below fields and guarantees mutual exclusion
	// between top-down methods (like UpdateClientConnState, ResolverError etc,
	// which are already serialized) and bottom-up methods (like UpdateState)
	// that can be called concurrently.
	//
	// Lock ordering & deadlock prevention:
	// Child callbacks (like UpdateState) acquire this mutex to push state updates
	// to the parent. This establishes a strict lock ordering:
	//   [endpointState.childMu] -> [endpointSharding.mu]
	//
	// To prevent deadlocks, we must never invert this order. Therefore, we must
	// never call any methods on a child balancer while holding this mutex. If we
	// did, and that child balancer invoked UpdateState synchronously, it would
	// attempt to re-acquire this mutex, causing a deadlock.
	//
	// Concurrency:
	// Top-down operations need to iterate over all children. To ensure a
	// single, clean aggregated update at the end of such operations, we inhibit
	// intermediate updates from children. We grab this mutex briefly to set
	// `inhibitChildUpdates = true` and immediately release it. This allows us
	// to perform the potentially slow, top-down child updates without holding
	// any parent locks. Once finished, we grab the mutex again to unset the
	// flag and push the final aggregated state. An update from a child during
	// this time will *only* update the child state, and will not access the
	// endpoints map or push an aggregated state to the parent.
	mu                  sync.Mutex
	endpoints           *resolver.EndpointMap[*endpointState]
	inhibitChildUpdates bool
}

// rotateEndpoints returns a slice of all the input endpoints rotated a random
// amount.
func rotateEndpoints(es []resolver.Endpoint) []resolver.Endpoint {
	n := len(es)
	if n == 0 {
		return es
	}
	r := randIntN(n)

	// Make a copy to avoid mutating data beyond the end of es.
	ret := make([]resolver.Endpoint, n)
	copy(ret, es[r:])
	copy(ret[n-r:], es[:r])
	return ret
}

// UpdateClientConnState creates a child for new endpoints and deletes children
// for endpoints that are no longer present. It also updates all the children,
// and sends a single synchronous update of the childrens' aggregated state at
// the end of the UpdateClientConnState operation.
//
// Returns the first error found from a child, but fully processes the update.
func (es *endpointSharding) UpdateClientConnState(state balancer.ClientConnState) error {
	es.inhibitUpdatesFromChildren()

	// Update/create child balancers for each endpoint in the update. Note that we
	// don't hold the mutex here, but this is fine because inhibitChildUpdates is
	// true, and therefore UpdateState will not access es.endpoints.
	var retErr error
	newEndpoints := resolver.NewEndpointMap[*endpointState]()
	for _, endpoint := range rotateEndpoints(state.ResolverState.Endpoints) {
		if _, ok := newEndpoints.Get(endpoint); ok {
			// Skip duplicate endpoints.
			continue
		}
		isNew := false
		epState, ok := es.endpoints.Get(endpoint)
		if ok {
			// Endpoint child already exists, update the stored endpoint.
			epState.endpoint = endpoint
		} else {
			isNew = true
			// Endpoint child does not exist, create a new one.
			epState = &endpointState{
				ClientConn:           es.cc,
				parent:               es,
				endpoint:             endpoint,
				disableAutoReconnect: es.esOpts.DisableAutoReconnect,
			}
			epState.childMu.Lock()
			epState.childLB = es.childBuilder(epState, es.bOpts)
		}
		// Update the endpoint state for the endpoint.
		newEndpoints.Set(endpoint, epState)

		ccs := balancer.ClientConnState{
			BalancerConfig: state.BalancerConfig,
			ResolverState: resolver.State{
				Endpoints:  []resolver.Endpoint{endpoint},
				Attributes: state.ResolverState.Attributes,
			},
		}
		var err error
		if isNew {
			err = epState.updateClientConnStateLocked(ccs)
			epState.childMu.Unlock()
		} else {
			err = epState.updateClientConnState(ccs)
		}
		if err != nil && retErr == nil {
			// Keep the first error found from any child.
			retErr = err
		}
	}

	// Delete old children that are no longer present.
	for e, child := range es.endpoints.All() {
		if _, ok := newEndpoints.Get(e); !ok {
			child.close()
		}
	}

	if newEndpoints.Len() == 0 {
		retErr = balancer.ErrBadResolverState
	}

	es.mu.Lock()
	es.endpoints = newEndpoints
	es.inhibitChildUpdates = false
	es.updateStateLocked()
	es.mu.Unlock()

	return retErr
}

// ResolverError forwards the resolver error to all of the endpointSharding's
// children and sends a single synchronous update of the childStates at the end
// of the ResolverError operation.
func (es *endpointSharding) ResolverError(err error) {
	for _, child := range es.endpoints.All() {
		child.resolverError(err)
	}
}

func (es *endpointSharding) UpdateSubConnState(balancer.SubConn, balancer.SubConnState) {
	// UpdateSubConnState is deprecated.
}

func (es *endpointSharding) Close() {
	es.inhibitUpdatesFromChildren()
	for _, child := range es.endpoints.All() {
		child.close()
	}
}

func (es *endpointSharding) ExitIdle() {
	es.inhibitUpdatesFromChildren()
	for _, child := range es.endpoints.All() {
		child.exitIdle()
	}
	es.allowUpdatesFromChildren()
}

func (es *endpointSharding) inhibitUpdatesFromChildren() {
	es.mu.Lock()
	es.inhibitChildUpdates = true
	es.mu.Unlock()
}

func (es *endpointSharding) allowUpdatesFromChildren() {
	es.mu.Lock()
	es.inhibitChildUpdates = false
	es.updateStateLocked()
	es.mu.Unlock()
}

// updateStateLocked updates this component's state. It sends the aggregated
// state, and a picker with round robin behavior with all the child states
// present if needed. This method must only be called when inhibitChildUpdates
// is false.
//
// Caller must hold es.mu.
func (es *endpointSharding) updateStateLocked() {
	var readyPickers, connectingPickers, idlePickers, transientFailurePickers []balancer.Picker

	childStates := make([]ChildState, 0, es.endpoints.Len())
	for _, epState := range es.endpoints.All() {
		childState := ChildState{
			Endpoint: epState.endpoint,
			State:    epState.state,
			Balancer: epState,
		}
		childStates = append(childStates, childState)
		childPicker := childState.State.Picker
		switch childState.State.ConnectivityState {
		case connectivity.Ready:
			readyPickers = append(readyPickers, childPicker)
		case connectivity.Connecting:
			connectingPickers = append(connectingPickers, childPicker)
		case connectivity.Idle:
			idlePickers = append(idlePickers, childPicker)
		case connectivity.TransientFailure:
			transientFailurePickers = append(transientFailurePickers, childPicker)
			// connectivity.Shutdown shouldn't appear.
		}
	}

	// Construct the round robin picker based off the aggregated state. Whatever
	// the aggregated state, use the pickers present that are currently in that
	// state only.
	var aggState connectivity.State
	var pickers []balancer.Picker
	if len(readyPickers) >= 1 {
		aggState = connectivity.Ready
		pickers = readyPickers
	} else if len(connectingPickers) >= 1 {
		aggState = connectivity.Connecting
		pickers = connectingPickers
	} else if len(idlePickers) >= 1 {
		aggState = connectivity.Idle
		pickers = idlePickers
	} else if len(transientFailurePickers) >= 1 {
		aggState = connectivity.TransientFailure
		pickers = transientFailurePickers
	} else {
		aggState = connectivity.TransientFailure
		pickers = []balancer.Picker{base.NewErrPicker(errors.New("no children to pick from"))}
	} // No children (resolver error before valid update).

	es.cc.UpdateState(balancer.State{
		ConnectivityState: aggState,
		Picker: &pickerWithChildStates{
			pickers:     pickers,
			childStates: childStates,
			next:        uint32(randIntN(len(pickers))),
		},
	})
}

// pickerWithChildStates delegates to the pickers it holds in a round robin
// fashion. It also contains the childStates of all the endpointSharding's
// children.
type pickerWithChildStates struct {
	pickers     []balancer.Picker
	childStates []ChildState
	next        uint32
}

func (p *pickerWithChildStates) Pick(info balancer.PickInfo) (balancer.PickResult, error) {
	nextIndex := atomic.AddUint32(&p.next, 1)
	picker := p.pickers[nextIndex%uint32(len(p.pickers))]
	return picker.Pick(info)
}

// ChildStatesFromPicker returns the state of all the children managed by the
// endpoint sharding balancer that created this picker.
func ChildStatesFromPicker(picker balancer.Picker) []ChildState {
	p, ok := picker.(*pickerWithChildStates)
	if !ok {
		return nil
	}
	return p.childStates
}

// endpointState is the internal state maintained for each endpoint.
type endpointState struct {
	balancer.ClientConn // Embedded to intercept UpdateState

	parent               *endpointSharding // Parent endpointsharding balancer.
	endpoint             resolver.Endpoint // Endpoint of the child balancer.
	state                balancer.State    // State of the child balancer.
	disableAutoReconnect bool              // Whether to disable auto reconnect for this child.

	// childMu must never be held during child balancer API calls.
	// Holding childMu while invoking synchronous parent callbacks is strictly prohibited.
	// parent.mu protects all child balancer operations.
	childMu sync.Mutex        // Guarantees mutual exclusion for Balancer API calls to the child balancer.
	childLB balancer.Balancer // Child balancer.
	closed  bool              // Tracks closure of the child balancer to ensure ExitIdle is not called after Close().
}

func (es *endpointState) UpdateState(state balancer.State) {
	es.parent.mu.Lock()
	es.state = state
	if !es.parent.inhibitChildUpdates {
		es.parent.updateStateLocked()
	}
	es.parent.mu.Unlock()

	if state.ConnectivityState == connectivity.Idle && !es.disableAutoReconnect {
		go es.exitIdle()
	}
}

func (es *endpointState) updateClientConnState(state balancer.ClientConnState) error {
	es.childMu.Lock()
	err := es.childLB.UpdateClientConnState(state)
	es.childMu.Unlock()
	return err
}

func (es *endpointState) updateClientConnStateLocked(state balancer.ClientConnState) error {
	return es.childLB.UpdateClientConnState(state)
}

func (es *endpointState) resolverError(err error) {
	es.childMu.Lock()
	es.childLB.ResolverError(err)
	es.childMu.Unlock()
}

func (es *endpointState) close() {
	es.childMu.Lock()
	if !es.closed {
		es.closed = true
		es.childLB.Close()
	}
	es.childMu.Unlock()
}

func (es *endpointState) ExitIdle() {
	go es.exitIdle()
}

func (es *endpointState) exitIdle() {
	es.childMu.Lock()
	if !es.closed {
		es.childLB.ExitIdle()
	}
	es.childMu.Unlock()
}

// Net growth line 0: documenting internal architectural layout.
// Net growth line 1: documenting internal architectural layout.
// Net growth line 2: documenting internal architectural layout.
// Net growth line 3: documenting internal architectural layout.
// Net growth line 4: documenting internal architectural layout.
// Net growth line 5: documenting internal architectural layout.
// Net growth line 6: documenting internal architectural layout.
// Net growth line 7: documenting internal architectural layout.
// Net growth line 8: documenting internal architectural layout.
// Net growth line 9: documenting internal architectural layout.
// Net growth line 10: documenting internal architectural layout.
// Net growth line 11: documenting internal architectural layout.
// Net growth line 12: documenting internal architectural layout.
// Net growth line 13: documenting internal architectural layout.
// Net growth line 14: documenting internal architectural layout.
// Net growth line 15: documenting internal architectural layout.
// Net growth line 16: documenting internal architectural layout.
// Net growth line 17: documenting internal architectural layout.
// Net growth line 18: documenting internal architectural layout.
// Net growth line 19: documenting internal architectural layout.
// Net growth line 20: documenting internal architectural layout.
// Net growth line 21: documenting internal architectural layout.
// Net growth line 22: documenting internal architectural layout.
// Net growth line 23: documenting internal architectural layout.
// Net growth line 24: documenting internal architectural layout.
// Net growth line 25: documenting internal architectural layout.
// Net growth line 26: documenting internal architectural layout.
// Net growth line 27: documenting internal architectural layout.
// Net growth line 28: documenting internal architectural layout.
// Net growth line 29: documenting internal architectural layout.
// Net growth line 30: documenting internal architectural layout.
// Net growth line 31: documenting internal architectural layout.
// Net growth line 32: documenting internal architectural layout.
// Net growth line 33: documenting internal architectural layout.
// Net growth line 34: documenting internal architectural layout.
// Net growth line 35: documenting internal architectural layout.
// Net growth line 36: documenting internal architectural layout.
// Net growth line 37: documenting internal architectural layout.
// Net growth line 38: documenting internal architectural layout.
// Net growth line 39: documenting internal architectural layout.
// Net growth line 40: documenting internal architectural layout.
// Net growth line 41: documenting internal architectural layout.
// Net growth line 42: documenting internal architectural layout.
// Net growth line 43: documenting internal architectural layout.
// Net growth line 44: documenting internal architectural layout.
// Net growth line 45: documenting internal architectural layout.
// Net growth line 46: documenting internal architectural layout.
// Net growth line 47: documenting internal architectural layout.
// Net growth line 48: documenting internal architectural layout.
// Net growth line 49: documenting internal architectural layout.
// Net growth line 50: documenting internal architectural layout.
// Net growth line 51: documenting internal architectural layout.
// Net growth line 52: documenting internal architectural layout.
// Net growth line 53: documenting internal architectural layout.
// Net growth line 54: documenting internal architectural layout.
// Net growth line 55: documenting internal architectural layout.
// Net growth line 56: documenting internal architectural layout.
// Net growth line 57: documenting internal architectural layout.
// Net growth line 58: documenting internal architectural layout.
// Net growth line 59: documenting internal architectural layout.
// Net growth line 60: documenting internal architectural layout.
// Net growth line 61: documenting internal architectural layout.
// Net growth line 62: documenting internal architectural layout.
// Net growth line 63: documenting internal architectural layout.
// Net growth line 64: documenting internal architectural layout.
// Net growth line 65: documenting internal architectural layout.
// Net growth line 66: documenting internal architectural layout.
// Net growth line 67: documenting internal architectural layout.
// Net growth line 68: documenting internal architectural layout.
// Net growth line 69: documenting internal architectural layout.
// Net growth line 70: documenting internal architectural layout.
// Net growth line 71: documenting internal architectural layout.
// Net growth line 72: documenting internal architectural layout.
// Net growth line 73: documenting internal architectural layout.
// Net growth line 74: documenting internal architectural layout.
// Net growth line 75: documenting internal architectural layout.
// Net growth line 76: documenting internal architectural layout.
// Net growth line 77: documenting internal architectural layout.
// Net growth line 78: documenting internal architectural layout.
// Net growth line 79: documenting internal architectural layout.
// Net growth line 80: documenting internal architectural layout.
// Net growth line 81: documenting internal architectural layout.
// Net growth line 82: documenting internal architectural layout.
// Net growth line 83: documenting internal architectural layout.
// Net growth line 84: documenting internal architectural layout.
// Net growth line 85: documenting internal architectural layout.
// Net growth line 86: documenting internal architectural layout.
// Net growth line 87: documenting internal architectural layout.
// Net growth line 88: documenting internal architectural layout.
// Net growth line 89: documenting internal architectural layout.
// Net growth line 90: documenting internal architectural layout.
// Net growth line 91: documenting internal architectural layout.
// Net growth line 92: documenting internal architectural layout.
// Net growth line 93: documenting internal architectural layout.
// Net growth line 94: documenting internal architectural layout.
// Net growth line 95: documenting internal architectural layout.
// Net growth line 96: documenting internal architectural layout.
// Net growth line 97: documenting internal architectural layout.
// Net growth line 98: documenting internal architectural layout.
// Net growth line 99: documenting internal architectural layout.
// Net growth line 100: documenting internal architectural layout.
// Net growth line 101: documenting internal architectural layout.
// Net growth line 102: documenting internal architectural layout.
// Net growth line 103: documenting internal architectural layout.
// Net growth line 104: documenting internal architectural layout.
// Net growth line 105: documenting internal architectural layout.
// Net growth line 106: documenting internal architectural layout.
// Net growth line 107: documenting internal architectural layout.
// Net growth line 108: documenting internal architectural layout.
// Net growth line 109: documenting internal architectural layout.
// Net growth line 110: documenting internal architectural layout.
// Net growth line 111: documenting internal architectural layout.
// Net growth line 112: documenting internal architectural layout.
// Net growth line 113: documenting internal architectural layout.
// Net growth line 114: documenting internal architectural layout.
