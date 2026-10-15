// Run: copy from verify/repro/testdata/ to test/xds/ on branch evalon/grpc-go-xd-12356fc9, then `go test -race -count=1 -v -run '^Test$/^VerifyC3_' ./test/xds 2>&1 | grep -E 'VERIFY-C3|^\s*--- |^(ok|FAIL|PASS)'` (this test is expected to FAIL: its failure is the tracker detecting the duplicate)

package xds_test

import (
	"sync/atomic"
	"testing"
)

// Calls Close twice on one trackingInterceptor (the tracker consumed by
// TestServerSideXDS_RDSUpdate_ClosesSupersededInterceptors and friends) and
// then applies the same style of exact-count assertion that test uses.
func (s) TestVerifyC3_TrackingInterceptor_DoubleClose(t *testing.T) {
	var filtersCreated, filtersDestroyed, interceptorsCreated, interceptorsDestroyed atomic.Int32
	fb := &trackingHTTPFilterBuilder{
		filtersCreated:        &filtersCreated,
		filtersDestroyed:      &filtersDestroyed,
		interceptorsCreated:   &interceptorsCreated,
		interceptorsDestroyed: &interceptorsDestroyed,
		typeURL:               t.Name(),
		pathCh:                make(chan string, 1),
	}
	i, err := fb.BuildServerFilter().BuildServerInterceptor(testFilterCfg{}, nil)
	if err != nil {
		t.Fatalf("BuildServerInterceptor() failed: %v", err)
	}
	t.Logf("VERIFY-C3 e2e tracker, after build:        interceptorsCreated=%d interceptorsDestroyed=%d", interceptorsCreated.Load(), interceptorsDestroyed.Load())
	i.Close()
	t.Logf("VERIFY-C3 e2e tracker, after 1st Close():  interceptorsCreated=%d interceptorsDestroyed=%d", interceptorsCreated.Load(), interceptorsDestroyed.Load())
	i.Close()
	t.Logf("VERIFY-C3 e2e tracker, after 2nd Close():  interceptorsCreated=%d interceptorsDestroyed=%d", interceptorsCreated.Load(), interceptorsDestroyed.Load())

	if got, want := interceptorsDestroyed.Load(), int32(1); got != want {
		t.Errorf("VERIFY-C3 exact-count assertion FIRED: Destroyed %d interceptor instances, want: %d", got, want)
	} else {
		t.Logf("VERIFY-C3 e2e tracker: no exact-count assertion fired after a duplicate Close() on the same instance")
	}
}
