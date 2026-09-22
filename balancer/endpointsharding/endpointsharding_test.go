/*
 *
 * Copyright 2025 gRPC authors.
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

package endpointsharding

import (
	"errors"
	"fmt"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"google.golang.org/grpc/balancer"
	"google.golang.org/grpc/connectivity"
	"google.golang.org/grpc/internal/grpctest"
	"google.golang.org/grpc/resolver"
)

type s struct {
	grpctest.Tester
}

func Test(t *testing.T) {
	grpctest.RunSubTests(t, s{})
}

func (s) TestRotateEndpoints(t *testing.T) {
	ep := func(addr string) resolver.Endpoint {
		return resolver.Endpoint{Addresses: []resolver.Address{{Addr: addr}}}
	}
	endpoints := []resolver.Endpoint{ep("1"), ep("2"), ep("3"), ep("4"), ep("5")}
	testCases := []struct {
		rval int
		want []resolver.Endpoint
	}{
		{
			rval: 0,
			want: []resolver.Endpoint{ep("1"), ep("2"), ep("3"), ep("4"), ep("5")},
		},
		{
			rval: 1,
			want: []resolver.Endpoint{ep("2"), ep("3"), ep("4"), ep("5"), ep("1")},
		},
		{
			rval: 2,
			want: []resolver.Endpoint{ep("3"), ep("4"), ep("5"), ep("1"), ep("2")},
		},
		{
			rval: 3,
			want: []resolver.Endpoint{ep("4"), ep("5"), ep("1"), ep("2"), ep("3")},
		},
		{
			rval: 4,
			want: []resolver.Endpoint{ep("5"), ep("1"), ep("2"), ep("3"), ep("4")},
		},
	}
	for _, tc := range testCases {
		t.Run(fmt.Sprintf("rval=%d", tc.rval), func(t *testing.T) {
			origRandIntN := randIntN
			defer func() { randIntN = origRandIntN }()
			randIntN = func(n int) int { return tc.rval }
			got := rotateEndpoints(endpoints)
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("rotateEndpoints(%v) = %v, want %v", endpoints, got, tc.want)
			}
		})
	}
}

type maintainedChild struct {
	balancer.Balancer
	mu              sync.Mutex
	cc              balancer.ClientConn
	enterUpdate     chan struct{}
	releaseBlock    chan struct{}
	enterExit       chan struct{}
	doneExit        chan struct{}
	onUpdate        func(ccs balancer.ClientConnState)
	onResolverError func(err error)
	updateErr       error
	shouldBlock     bool
	activeCalls     int32
	maxSeen         int32
}

func (c *maintainedChild) enterCall() {
	cur := atomic.AddInt32(&c.activeCalls, 1)
	for {
		oldMax := atomic.LoadInt32(&c.maxSeen)
		if cur <= oldMax || atomic.CompareAndSwapInt32(&c.maxSeen, oldMax, cur) {
			break
		}
	}
}

func (c *maintainedChild) exitCall() {
	atomic.AddInt32(&c.activeCalls, -1)
}

func (c *maintainedChild) UpdateClientConnState(ccs balancer.ClientConnState) error {
	c.enterCall()
	defer c.exitCall()

	c.mu.Lock()
	block := c.shouldBlock
	cc := c.cc
	onUp := c.onUpdate
	c.mu.Unlock()

	if onUp != nil {
		onUp(ccs)
	}
	if cc != nil {
		cc.UpdateState(balancer.State{
			ConnectivityState: connectivity.Ready,
		})
	}
	if block {
		if c.enterUpdate != nil {
			select {
			case c.enterUpdate <- struct{}{}:
			default:
			}
		}
		if c.releaseBlock != nil {
			<-c.releaseBlock
		}
	}

	c.mu.Lock()
	err := c.updateErr
	c.mu.Unlock()
	return err
}

func (c *maintainedChild) ResolverError(err error) {
	c.enterCall()
	defer c.exitCall()
	c.mu.Lock()
	onErr := c.onResolverError
	c.mu.Unlock()
	if onErr != nil {
		onErr(err)
	}
}

func (c *maintainedChild) UpdateSubConnState(sc balancer.SubConn, state balancer.SubConnState) {
	c.enterCall()
	defer c.exitCall()
}

func (c *maintainedChild) Close() {
	c.enterCall()
	defer c.exitCall()
}

func (c *maintainedChild) ExitIdle() {
	c.enterCall()
	defer c.exitCall()
	if c.enterExit != nil {
		select {
		case c.enterExit <- struct{}{}:
		default:
		}
	}
	if c.doneExit != nil {
		select {
		case c.doneExit <- struct{}{}:
		default:
		}
	}
}

type maintainedProbeCC struct {
	balancer.ClientConn
	mu          sync.Mutex
	state       balancer.State
	updateCount int
}

func (r *maintainedProbeCC) UpdateState(s balancer.State) {
	r.mu.Lock()
	r.state = s
	r.updateCount++
	r.mu.Unlock()
}

func (r *maintainedProbeCC) getState() balancer.State {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.state
}

func (r *maintainedProbeCC) getUpdateCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.updateCount
}

// 1. Decoupled Progress: Child 2 exits idle independently while Child 1 update is blocked
func (s) TestDecoupledChildProgress(t *testing.T) {
	child1Blocked := make(chan struct{}, 1)
	child1Release := make(chan struct{})
	child2Entered := make(chan struct{}, 1)
	child2Done := make(chan struct{}, 1)

	var (
		mu         sync.Mutex
		child1Addr string
		c1, c2     *maintainedChild
	)

	childBuilder := func(cc balancer.ClientConn, opts balancer.BuildOptions) balancer.Balancer {
		mu.Lock()
		defer mu.Unlock()
		if c1 == nil {
			c1 = &maintainedChild{
				cc:           cc,
				enterUpdate:  child1Blocked,
				releaseBlock: child1Release,
			}
			c1.onUpdate = func(ccs balancer.ClientConnState) {
				if len(ccs.ResolverState.Endpoints) > 0 && len(ccs.ResolverState.Endpoints[0].Addresses) > 0 {
					mu.Lock()
					child1Addr = ccs.ResolverState.Endpoints[0].Addresses[0].Addr
					mu.Unlock()
				}
			}
			return c1
		}
		c2 = &maintainedChild{
			cc:        cc,
			enterExit: child2Entered,
			doneExit:  child2Done,
		}
		return c2
	}

	cc := &maintainedProbeCC{}
	lb := NewBalancer(cc, balancer.BuildOptions{}, childBuilder, Options{})

	ep1 := resolver.Endpoint{Addresses: []resolver.Address{{Addr: "10.0.0.1"}}}
	ep2 := resolver.Endpoint{Addresses: []resolver.Address{{Addr: "10.0.0.2"}}}

	if err := lb.UpdateClientConnState(balancer.ClientConnState{
		ResolverState: resolver.State{Endpoints: []resolver.Endpoint{ep1, ep2}},
	}); err != nil {
		t.Fatalf("initial UpdateClientConnState failed: %v", err)
	}

	mu.Lock()
	blockedAddr := child1Addr
	c1.mu.Lock()
	c1.shouldBlock = true
	c1.mu.Unlock()
	mu.Unlock()

	var (
		wg          sync.WaitGroup
		releaseOnce sync.Once
		workerDone  = make(chan struct{})
	)
	releaseAndWait := func() bool {
		releaseOnce.Do(func() {
			close(child1Release)
		})
		select {
		case <-workerDone:
			return true
		case <-time.After(2 * time.Second):
			t.Error("worker goroutine timed out or deadlocked during child1 release")
			return false
		}
	}
	defer func() {
		if releaseAndWait() {
			lb.Close()
		}
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()
		_ = lb.UpdateClientConnState(balancer.ClientConnState{
			ResolverState: resolver.State{Endpoints: []resolver.Endpoint{ep1, ep2}},
		})
	}()
	go func() {
		wg.Wait()
		close(workerDone)
	}()

	select {
	case <-child1Blocked:
	case <-time.After(2 * time.Second):
		t.Fatal("child 1 never entered blocked update")
	}

	st := cc.getState()
	childStates := ChildStatesFromPicker(st.Picker)
	if len(childStates) != 2 {
		t.Fatalf("expected 2 child states, got %d", len(childStates))
	}

	var child2CS *ChildState
	for i := range childStates {
		if len(childStates[i].Endpoint.Addresses) > 0 && childStates[i].Endpoint.Addresses[0].Addr != blockedAddr {
			child2CS = &childStates[i]
			break
		}
	}
	if child2CS == nil {
		t.Fatal("child 2 state not found in picker")
	}

	go child2CS.Balancer.ExitIdle()

	select {
	case <-child2Done:
		// Succeeded!
	case <-time.After(300 * time.Millisecond):
		t.Fatal("child 2 ExitIdle blocked while child 1 was updating (coarse locking defect)")
	}
}

// 2. Same-Child Mutual Exclusion: Multiple operations on same child execute sequentially
func (s) TestSameChildMutualExclusion(t *testing.T) {
	updateHold := make(chan struct{})
	updateEntered := make(chan struct{}, 1)
	exitDone := make(chan struct{}, 1)
	var (
		childMu sync.Mutex
		child   *maintainedChild
	)

	childBuilder := func(cc balancer.ClientConn, opts balancer.BuildOptions) balancer.Balancer {
		c := &maintainedChild{
			cc:           cc,
			enterUpdate:  updateEntered,
			releaseBlock: updateHold,
			doneExit:     exitDone,
			shouldBlock:  false, // Start unblocked so initial update publishes child
		}
		childMu.Lock()
		child = c
		childMu.Unlock()
		return c
	}

	cc := &maintainedProbeCC{}
	lb := NewBalancer(cc, balancer.BuildOptions{}, childBuilder, Options{})

	ep := resolver.Endpoint{Addresses: []resolver.Address{{Addr: "10.0.0.1"}}}

	// 1. Publish the child with an unblocked initial update
	if err := lb.UpdateClientConnState(balancer.ClientConnState{
		ResolverState: resolver.State{Endpoints: []resolver.Endpoint{ep}},
	}); err != nil {
		t.Fatalf("initial UpdateClientConnState failed: %v", err)
	}

	// 2. Obtain its published ChildState handle (must exist; fail if missing)
	st := cc.getState()
	childStates := ChildStatesFromPicker(st.Picker)
	if len(childStates) == 0 {
		t.Fatal("expected published child state in picker, got none")
	}
	targetChildState := childStates[0]

	// 3. Enable blocking for a second update
	childMu.Lock()
	c := child
	childMu.Unlock()
	if c == nil {
		t.Fatal("child was nil after initial configuration")
	}
	c.mu.Lock()
	c.shouldBlock = true
	c.mu.Unlock()

	var (
		wg          sync.WaitGroup
		releaseOnce sync.Once
		workerDone  = make(chan struct{})
	)
	releaseAndWait := func() bool {
		releaseOnce.Do(func() {
			close(updateHold)
		})
		select {
		case <-workerDone:
			return true
		case <-time.After(2 * time.Second):
			t.Error("worker goroutine timed out or deadlocked during release")
			return false
		}
	}
	defer func() {
		if releaseAndWait() {
			lb.Close()
		}
	}()

	// 4. Start second update on that known handle and require entered signal
	wg.Add(1)
	go func() {
		defer wg.Done()
		_ = lb.UpdateClientConnState(balancer.ClientConnState{
			ResolverState: resolver.State{Endpoints: []resolver.Endpoint{ep}},
		})
	}()
	go func() {
		wg.Wait()
		close(workerDone)
	}()

	select {
	case <-updateEntered:
	case <-time.After(2 * time.Second):
		t.Fatal("second update never entered child")
	}

	// 5. Invoke ExitIdle on the published handle while second update is held
	callDone := make(chan struct{})
	go func() {
		targetChildState.Balancer.ExitIdle()
		close(callDone)
	}()

	// 6. Prove it CANNOT finish before release (must remain blocked behind childMu)
	select {
	case <-exitDone:
		t.Fatal("ExitIdle completed concurrently while UpdateClientConnState was in flight on same child")
	case <-time.After(150 * time.Millisecond):
		// Expected: ExitIdle is held waiting for update to release childMu
	}

	// 7. Release update and require ExitIdle finishes after release
	if !releaseAndWait() {
		t.Fatal("worker release timed out")
	}

	select {
	case <-exitDone:
		// Succeeded: finished after release
	case <-time.After(2 * time.Second):
		t.Fatal("ExitIdle never finished after update released")
	}

	<-callDone

	// 8. Verify maxSeen <= 1 (zero overlap)
	if max := atomic.LoadInt32(&c.maxSeen); max > 1 {
		t.Fatalf("observed concurrent overlapping calls on same child (maxSeen=%d, want 1)", max)
	}
}

// 3. Synchronous Construction Idle Callback Safety
func (s) TestSynchronousConstructionIdleCallback(t *testing.T) {
	reconnectCalled := make(chan struct{}, 1)
	buildEntered := make(chan struct{}, 1)
	buildRelease := make(chan struct{})
	constructionDone := make(chan struct{})

	var releaseBuildOnce sync.Once
	releaseBuildAndWait := func() bool {
		releaseBuildOnce.Do(func() {
			close(buildRelease)
		})
		select {
		case <-constructionDone:
			return true
		case <-time.After(2 * time.Second):
			t.Error("childBuilder timed out completing construction during cleanup")
			return false
		}
	}

	childBuilder := func(cc balancer.ClientConn, opts balancer.BuildOptions) balancer.Balancer {
		cc.UpdateState(balancer.State{
			ConnectivityState: connectivity.Idle,
		})
		select {
		case buildEntered <- struct{}{}:
		default:
		}
		<-buildRelease
		return &maintainedChild{
			cc:        cc,
			enterExit: reconnectCalled,
		}
	}

	cc := &maintainedProbeCC{}
	lb := NewBalancer(cc, balancer.BuildOptions{}, childBuilder, Options{
		DisableAutoReconnect: false,
	})
	defer func() {
		if releaseBuildAndWait() {
			lb.Close()
		}
	}()

	ep := resolver.Endpoint{Addresses: []resolver.Address{{Addr: "10.0.0.1"}}}
	updateDone := make(chan error, 1)
	go func() {
		err := lb.UpdateClientConnState(balancer.ClientConnState{
			ResolverState: resolver.State{Endpoints: []resolver.Endpoint{ep}},
		})
		updateDone <- err
		close(constructionDone)
	}()

	select {
	case <-buildEntered:
	case <-time.After(2 * time.Second):
		t.Fatal("childBuilder never entered construction")
	}

	if !releaseBuildAndWait() {
		t.Fatal("build release timed out")
	}

	select {
	case err := <-updateDone:
		if err != nil {
			t.Fatalf("UpdateClientConnState failed: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("UpdateClientConnState timed out completing construction")
	}

	select {
	case <-reconnectCalled:
		// Reconnect callback safely reached initialized child
	case <-time.After(2 * time.Second):
		t.Fatal("reconnect was dropped or never delivered after construction-time Idle report")
	}
}

// 4. Batch Update Consolidated Notification
func (s) TestBatchUpdateConsolidatedNotification(t *testing.T) {
	child3Entered := make(chan struct{}, 1)
	child3Hold := make(chan struct{})
	var childCount int32

	childBuilder := func(cc balancer.ClientConn, opts balancer.BuildOptions) balancer.Balancer {
		cNum := atomic.AddInt32(&childCount, 1)
		c := &maintainedChild{cc: cc}
		if cNum == 3 {
			c.shouldBlock = true
			c.enterUpdate = child3Entered
			c.releaseBlock = child3Hold
		}
		return c
	}

	cc := &maintainedProbeCC{}
	lb := NewBalancer(cc, balancer.BuildOptions{}, childBuilder, Options{})

	var (
		wg          sync.WaitGroup
		releaseOnce sync.Once
		batchDone   = make(chan struct{})
	)
	releaseAndWait := func() bool {
		releaseOnce.Do(func() {
			close(child3Hold)
		})
		select {
		case <-batchDone:
			return true
		case <-time.After(2 * time.Second):
			t.Error("batch worker timed out or deadlocked during batch release")
			return false
		}
	}
	defer func() {
		if releaseAndWait() {
			lb.Close()
		}
	}()

	eps := []resolver.Endpoint{
		{Addresses: []resolver.Address{{Addr: "10.0.0.1"}}},
		{Addresses: []resolver.Address{{Addr: "10.0.0.2"}}},
		{Addresses: []resolver.Address{{Addr: "10.0.0.3"}}},
	}

	wg.Add(1)
	go func() {
		defer wg.Done()
		_ = lb.UpdateClientConnState(balancer.ClientConnState{
			ResolverState: resolver.State{Endpoints: eps},
		})
	}()
	go func() {
		wg.Wait()
		close(batchDone)
	}()

	select {
	case <-child3Entered:
	case <-time.After(2 * time.Second):
		t.Fatal("third child never entered update")
	}

	// While batch is held on 3rd child, parent updateCount must remain 0
	if count := cc.getUpdateCount(); count != 0 {
		t.Fatalf("expected 0 parent updates while batch in flight, got %d", count)
	}

	// Release 3rd child; safeRelease joins the worker goroutine
	if !releaseAndWait() {
		t.Fatal("batch worker release timed out")
	}

	// After release, exactly 1 consolidated update with all 3 endpoints
	if count := cc.getUpdateCount(); count != 1 {
		t.Fatalf("expected exactly 1 consolidated update after batch complete, got %d", count)
	}
	st := cc.getState()
	cs := ChildStatesFromPicker(st.Picker)
	if len(cs) != 3 {
		t.Fatalf("expected 3 endpoints in final picker, got %d", len(cs))
	}
}

// 5. White-Box Closed-State Guard Coverage: Closed endpointState drops exitIdle without entering closed child
type internalExitIdler interface {
	exitIdle()
}

func (s) TestClosedStateGuardCoverage(t *testing.T) {
	var (
		childrenMu sync.Mutex
		children   = make(map[string]*maintainedChild)
		idlers     = make(map[string]internalExitIdler)
	)

	childBuilder := func(cc balancer.ClientConn, opts balancer.BuildOptions) balancer.Balancer {
		c := &maintainedChild{
			cc:        cc,
			enterExit: make(chan struct{}, 1),
		}
		c.onUpdate = func(ccs balancer.ClientConnState) {
			if len(ccs.ResolverState.Endpoints) > 0 && len(ccs.ResolverState.Endpoints[0].Addresses) > 0 {
				addr := ccs.ResolverState.Endpoints[0].Addresses[0].Addr
				childrenMu.Lock()
				children[addr] = c
				if idler, ok := cc.(internalExitIdler); ok {
					idlers[addr] = idler
				}
				childrenMu.Unlock()
			}
		}
		return c
	}

	cc := &maintainedProbeCC{}
	lb := NewBalancer(cc, balancer.BuildOptions{}, childBuilder, Options{})
	var closeOnce sync.Once
	safeClose := func() {
		closeOnce.Do(func() {
			lb.Close()
		})
	}
	defer safeClose()

	ep1 := resolver.Endpoint{Addresses: []resolver.Address{{Addr: "10.0.0.1"}}}
	ep2 := resolver.Endpoint{Addresses: []resolver.Address{{Addr: "10.0.0.2"}}}

	_ = lb.UpdateClientConnState(balancer.ClientConnState{
		ResolverState: resolver.State{Endpoints: []resolver.Endpoint{ep1, ep2}},
	})

	childrenMu.Lock()
	c1 := children["10.0.0.1"]
	c2 := children["10.0.0.2"]
	idler1 := idlers["10.0.0.1"]
	idler2 := idlers["10.0.0.2"]
	childrenMu.Unlock()

	if c1 == nil || c2 == nil {
		t.Fatal("children not mapped during initial update")
	}
	if idler1 == nil || idler2 == nil {
		t.Skip("internalExitIdler interface not implemented on this codebase version")
	}

	// Remove 10.0.0.1 by updating endpoints to only 10.0.0.2
	_ = lb.UpdateClientConnState(balancer.ClientConnState{
		ResolverState: resolver.State{Endpoints: []resolver.Endpoint{ep2}},
	})

	// 1. Invoke exitIdle() synchronously on removedEPState to deterministically
	// verify the closed-state guard without detached goroutine scheduling delay.
	// (White-box verification of closed guard; excludes detached public handle invocation)
	idler1.exitIdle()

	select {
	case <-c1.enterExit:
		t.Fatal("exitIdle entered closed child after endpoint was removed")
	default:
		// Expected: closed guard rejected child invocation
	}

	// 2. Close the entire balancer (which marks activeEPState.closed = true)
	safeClose()

	// Invoke exitIdle() synchronously on activeEPState after Close()
	idler2.exitIdle()

	select {
	case <-c2.enterExit:
		t.Fatal("exitIdle entered closed child after balancer Close()")
	default:
		// Expected: closed guard rejected child invocation on previously active child
	}
}

// 6. Child-Update Error Consolidation: Error from one child is returned while valid endpoints consolidate
func (s) TestBatchUpdateChildErrorConsolidation(t *testing.T) {
	expectedErr := errors.New("child-2 update failed")
	var childCount int32

	childBuilder := func(cc balancer.ClientConn, opts balancer.BuildOptions) balancer.Balancer {
		cNum := atomic.AddInt32(&childCount, 1)
		c := &maintainedChild{cc: cc}
		if cNum == 2 {
			c.updateErr = expectedErr
		}
		return c
	}

	cc := &maintainedProbeCC{}
	lb := NewBalancer(cc, balancer.BuildOptions{}, childBuilder, Options{})
	defer lb.Close()

	eps := []resolver.Endpoint{
		{Addresses: []resolver.Address{{Addr: "10.0.0.1"}}},
		{Addresses: []resolver.Address{{Addr: "10.0.0.2"}}},
		{Addresses: []resolver.Address{{Addr: "10.0.0.3"}}},
	}

	err := lb.UpdateClientConnState(balancer.ClientConnState{
		ResolverState: resolver.State{Endpoints: eps},
	})

	// 1. First error from any failing child must be returned
	if !errors.Is(err, expectedErr) {
		t.Fatalf("UpdateClientConnState returned %v, want %v", err, expectedErr)
	}

	// 2. Final picker must still consolidate all endpoints rather than dropping remaining children
	st := cc.getState()
	if st.Picker == nil {
		t.Fatal("picker is nil after child error update")
	}
	csList := ChildStatesFromPicker(st.Picker)
	if len(csList) != 3 {
		t.Fatalf("expected 3 child states in consolidated picker despite child error, got %d", len(csList))
	}
}

// 7. Synchronous Lifecycle Callback Safety: Child calling cc.UpdateState synchronously during ResolverError
func (s) TestSynchronousLifecycleCallbackSafety(t *testing.T) {
	callbackEntered := make(chan struct{}, 1)
	childBuilder := func(cc balancer.ClientConn, opts balancer.BuildOptions) balancer.Balancer {
		c := &maintainedChild{cc: cc}
		c.onResolverError = func(err error) {
			// Synchronous child-to-parent callback during ResolverError lifecycle operation
			cc.UpdateState(balancer.State{
				ConnectivityState: connectivity.TransientFailure,
			})
			select {
			case callbackEntered <- struct{}{}:
			default:
			}
		}
		return c
	}

	cc := &maintainedProbeCC{}
	lb := NewBalancer(cc, balancer.BuildOptions{}, childBuilder, Options{})

	ep := resolver.Endpoint{Addresses: []resolver.Address{{Addr: "10.0.0.1"}}}
	if err := lb.UpdateClientConnState(balancer.ClientConnState{
		ResolverState: resolver.State{Endpoints: []resolver.Endpoint{ep}},
	}); err != nil {
		lb.Close()
		t.Fatalf("UpdateClientConnState failed: %v", err)
	}

	callDone := make(chan struct{})
	go func() {
		lb.ResolverError(errors.New("resolver error"))
		close(callDone)
	}()

	select {
	case <-callDone:
		// Succeeded: ResolverError completed without deadlocking on synchronous callback
		lb.Close()
	case <-time.After(2 * time.Second):
		// On timeout, fail immediately without calling lb.Close(), avoiding secondary deadlock
		t.Fatal("ResolverError deadlocked on synchronous child-to-parent UpdateState callback")
	}

	select {
	case <-callbackEntered:
		// Verified callback was called synchronously
	default:
		t.Fatal("synchronous callback was not called during ResolverError")
	}
}