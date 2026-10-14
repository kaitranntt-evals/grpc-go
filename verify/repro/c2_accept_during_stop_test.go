// Run (branch evalon/grpc-go-xd-c3af8974): cp verify/repro/c2_accept_during_stop_test.go test/xds/ && go test -race -count=1 -v -timeout 120s -run '^Test$/^VerifyC2_' ./test/xds  (problem present => Stop() hangs; the timeout dump shows a panic in filterChainManager.lookup(0x0, ...))

/*
 *
 * Copyright 2026 gRPC authors.
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

package xds_test

import (
	"context"
	"net"
	"os"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/connectivity"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/internal/stubserver"
	"google.golang.org/grpc/internal/testutils"
	"google.golang.org/grpc/internal/testutils/xds/e2e"
	"google.golang.org/grpc/internal/testutils/xds/e2e/setup"
	testgrpc "google.golang.org/grpc/interop/grpc_testing"
	testpb "google.golang.org/grpc/interop/grpc_testing"
	"google.golang.org/grpc/xds"

	v3listenerpb "github.com/envoyproxy/go-control-plane/envoy/config/listener/v3"
)

// verifyC2Listener is a user-supplied net.Listener. When armed, a connection
// that the kernel already handed to Accept() is returned to the caller only
// after Close() has been called on the listener plus a short delay. This
// models the Serve goroutine being descheduled between the inner Accept()
// returning and listenerWrapper.Accept() taking its mutex.
type verifyC2Listener struct {
	net.Listener
	armed       atomic.Bool
	accepted    chan struct{}
	closeCalled chan struct{}
	closeOnce   sync.Once
	delay       time.Duration
}

func (l *verifyC2Listener) Accept() (net.Conn, error) {
	c, err := l.Listener.Accept()
	if err == nil && l.armed.CompareAndSwap(true, false) {
		close(l.accepted)
		<-l.closeCalled
		time.Sleep(l.delay)
	}
	return c, err
}

func (l *verifyC2Listener) Close() error {
	l.closeOnce.Do(func() { close(l.closeCalled) })
	return l.Listener.Close()
}

func verifyC2StartServer(t *testing.T, ctx context.Context, ms *e2e.ManagementServer, nodeID string, bootstrapContents []byte, wrap func(net.Listener) net.Listener) (*stubserver.StubServer, string) {
	t.Helper()
	servingCh := make(chan struct{}, 8)
	opt := xds.ServingModeCallback(func(_ net.Addr, args xds.ServingModeChangeArgs) {
		if args.Mode == connectivity.ServingModeServing {
			servingCh <- struct{}{}
		}
	})
	stub := &stubserver.StubServer{
		EmptyCallF: func(context.Context, *testpb.Empty) (*testpb.Empty, error) { return &testpb.Empty{}, nil },
	}
	var err error
	if stub.S, err = xds.NewGRPCServer(grpc.Creds(insecure.NewCredentials()), opt, xds.BootstrapContentsForTesting(bootstrapContents)); err != nil {
		t.Fatalf("xds.NewGRPCServer: %v", err)
	}
	lis, err := testutils.LocalTCPListener()
	if err != nil {
		t.Fatal(err)
	}
	host, port, err := hostPortFromListener(lis)
	if err != nil {
		t.Fatal(err)
	}
	if err := ms.Update(ctx, e2e.UpdateOptions{
		NodeID:    nodeID,
		Listeners: []*v3listenerpb.Listener{e2e.DefaultServerListener(host, port, e2e.SecurityLevelNone, "routeName")},
	}); err != nil {
		t.Fatal(err)
	}
	stub.Listener = wrap(lis)
	stubserver.StartTestService(t, stub)
	select {
	case <-servingCh:
	case <-ctx.Done():
		t.Fatal("timeout waiting for SERVING")
	}
	return stub, net.JoinHostPort(host, strconv.Itoa(int(port)))
}

// Deterministic: a connection is accepted by the user's listener just before
// Stop(), and handed to listenerWrapper.Accept() just after Close() ran.
// On the audited branch the Serve goroutine panics with a nil pointer
// dereference in filterChainManager.lookup (called from listenerWrapper.Accept)
// while holding listenerWrapper.mu, and Stop() never returns.
func (s) TestVerifyC2_AcceptOverlappingStop_Gated(t *testing.T) {
	ms, nodeID, bootstrapContents, _ := setup.ManagementServerAndResolver(t)
	ctx, cancel := context.WithTimeout(context.Background(), defaultTestTimeout)
	defer cancel()

	gl := &verifyC2Listener{accepted: make(chan struct{}), closeCalled: make(chan struct{}), delay: 100 * time.Millisecond}
	stub, addr := verifyC2StartServer(t, ctx, ms, nodeID, bootstrapContents, func(l net.Listener) net.Listener { gl.Listener = l; return gl })

	// Sanity: the server serves RPCs.
	cc, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	defer cc.Close()
	if _, err := testgrpc.NewTestServiceClient(cc).EmptyCall(ctx, &testpb.Empty{}, grpc.WaitForReady(true)); err != nil {
		t.Fatalf("EmptyCall: %v", err)
	}
	t.Logf("sanity RPC OK; arming listener and stopping server")

	gl.armed.Store(true)
	c, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	select {
	case <-gl.accepted:
	case <-ctx.Done():
		t.Fatal("timeout waiting for accept")
	}
	stub.S.Stop() // normal shutdown; returns only if Serve goroutine survives
	t.Logf("Stop() returned without a panic")
}

// Uninstrumented stress: plain TCP listener, clients connecting while Stop()
// is called. Iterations are controlled by VERIFY_C2_ITERS (default 300).
func (s) TestVerifyC2_AcceptOverlappingStop_Stress(t *testing.T) {
	ms, nodeID, bootstrapContents, _ := setup.ManagementServerAndResolver(t)
	iters := 300
	if v, err := strconv.Atoi(os.Getenv("VERIFY_C2_ITERS")); err == nil {
		iters = v
	}
	for i := 0; i < iters; i++ {
		ctx, cancel := context.WithTimeout(context.Background(), defaultTestTimeout)
		stub, addr := verifyC2StartServer(t, ctx, ms, nodeID, bootstrapContents, func(l net.Listener) net.Listener { return l })
		stop := make(chan struct{})
		var wg sync.WaitGroup
		for j := 0; j < 16; j++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for {
					select {
					case <-stop:
						return
					default:
					}
					if c, err := net.Dial("tcp", addr); err == nil {
						c.Close()
					}
				}
			}()
		}
		time.Sleep(time.Duration(i%7) * time.Millisecond)
		stub.S.Stop()
		close(stop)
		wg.Wait()
		cancel()
	}
	t.Logf("%d iterations completed without a panic", iters)
}
