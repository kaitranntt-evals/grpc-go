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
	"sync"
	"testing"

	"google.golang.org/grpc/balancer"
	"google.golang.org/grpc/resolver"
)

type testProbeConn1 struct {
	balancer.ClientConn
	mu          sync.Mutex
	state       balancer.State
	updateCount int
}

func (r *testProbeConn1) UpdateState(s balancer.State) {
	r.mu.Lock()
	r.state = s
	r.updateCount++
	r.mu.Unlock()
}

func (s) TestProbeEndpointSelection1(t *testing.T) {
	cc := &testProbeConn1{}
	childBuilder := func(cc balancer.ClientConn, _ balancer.BuildOptions) balancer.Balancer {
		return &maintainedChild{cc: cc}
	}
	lb := NewBalancer(cc, balancer.BuildOptions{}, childBuilder, Options{})
	defer lb.Close()
	ep1 := resolver.Endpoint{Addresses: []resolver.Address{{Addr: "10.0.0.1"}}}
	ep2 := resolver.Endpoint{Addresses: []resolver.Address{{Addr: "10.0.0.2"}}}
	_ = lb.UpdateClientConnState(balancer.ClientConnState{
		ResolverState: resolver.State{Endpoints: []resolver.Endpoint{ep1, ep2}},
	})
	st := cc.state
	cs := ChildStatesFromPicker(st.Picker)
	if len(cs) != 2 {
		t.Fatalf("expected 2 child states, got %d", len(cs))
	}
}

func (s) TestWorkerLackingDeferredCleanup(t *testing.T) {
	var wg sync.WaitGroup
	holdCh := make(chan struct{})
	wg.Add(1)
	go func() {
		defer wg.Done()
		<-holdCh
	}()
	// Unbounded deferred wait: if an assertion fails before close(holdCh),
	// deferred wg.Wait() executes and hangs indefinitely waiting on holdCh
	defer wg.Wait()

	var assertionCheck = true
	if !assertionCheck {
		t.Fatal("assertion failure without deferred release")
	}
	close(holdCh)
}
