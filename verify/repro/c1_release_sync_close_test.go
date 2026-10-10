// Run: on evalon/grpc-go-xd-7dc0ea45, cp this file to internal/xds/server/verify_c1_release_sync_close_test.go and run `go test -race -count=1 -v -run '^Test$/^VerifyC1_' ./internal/xds/server` (FAILS while the problem is present).

package server

import (
	"context"
	"testing"
	"time"
)

type vc1BlockingInterceptor struct {
	entered chan struct{}
	unblock chan struct{}
}

func (*vc1BlockingInterceptor) AllowRPC(context.Context) error { return nil }
func (i *vc1BlockingInterceptor) Close() {
	close(i.entered)
	<-i.unblock
}

// The last user's release() of a retired (closed) configuration is observed
// against an interceptor whose Close blocks.
func (s) TestVerifyC1_ReleaseOfLastUserBlocksOnInterceptorClose(t *testing.T) {
	icpt := &vc1BlockingInterceptor{entered: make(chan struct{}), unblock: make(chan struct{})}
	rc := &usableRouteConfiguration{vhs: []virtualHostWithInterceptors{{routes: []routeWithInterceptors{{interceptor: icpt}}}}}

	// One RPC is using the configuration (what RouteAndProcess does).
	rc.mu.Lock()
	rc.users++
	rc.mu.Unlock()

	// The configuration is retired (what updateRoutingConfig does).
	closeDone := make(chan struct{})
	go func() { rc.close(); close(closeDone) }()
	select {
	case <-closeDone:
		t.Logf("rc.close() returned immediately while 1 user is active (cleanup deferred to last user)")
	case <-time.After(2 * time.Second):
		t.Fatal("rc.close() blocked")
	}
	select {
	case <-icpt.entered:
		t.Fatal("interceptor closed while in use")
	default:
	}

	// The last user releases.
	start := time.Now()
	releaseDone := make(chan struct{})
	go func() { rc.release(); close(releaseDone) }()
	select {
	case <-icpt.entered:
		t.Logf("interceptor.Close entered %v after release() was called", time.Since(start).Round(time.Millisecond))
	case <-time.After(2 * time.Second):
		t.Fatal("release() of last user did not call interceptor.Close")
	}
	blocked := false
	select {
	case <-releaseDone:
		t.Logf("release() returned while interceptor.Close is still blocked")
	case <-time.After(2 * time.Second):
		blocked = true
		t.Logf("release() has NOT returned 2s after being called; interceptor.Close still blocked")
	}
	close(icpt.unblock)
	select {
	case <-releaseDone:
		t.Logf("release() returned %v after start, only after interceptor.Close was unblocked", time.Since(start).Round(time.Millisecond))
	case <-time.After(2 * time.Second):
		t.Fatal("release() never returned")
	}
	if blocked {
		t.Errorf("PROBLEM PRESENT: release() of the last user synchronously waits for the retired interceptor's Close")
	}
}
