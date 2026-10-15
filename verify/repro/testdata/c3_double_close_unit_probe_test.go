// Run: copy from verify/repro/testdata/ to internal/xds/server/ on branch evalon/grpc-go-xd-12356fc9, then `go test -race -count=1 -v -run '^Test$/^VerifyC3_' ./internal/xds/server 2>&1 | grep -E 'VERIFY-C3|^\s*--- |^(ok|FAIL|PASS)'`

package server

import "testing"

// Calls Close twice on one closeTrackingInterceptor (the tracker consumed by
// TestFilterChain_UpdateUsableRouteConfiguration_ReleasesSupersededResources)
// and then applies the same style of exact-count assertion that test uses.
func (s) TestVerifyC3_CloseTrackingInterceptor_DoubleClose(t *testing.T) {
	fb := &closeTrackingFilterBuilder{}
	i, err := fb.BuildServerFilter().BuildServerInterceptor(nil, nil)
	if err != nil {
		t.Fatalf("BuildServerInterceptor() failed: %v", err)
	}
	t.Logf("VERIFY-C3 unit tracker, after build:        interceptorsCreated=%d interceptorsClosed=%d liveInterceptorsCount=%d", fb.interceptorsCreated.Load(), fb.interceptorsClosed.Load(), fb.liveInterceptorsCount.Load())
	i.Close()
	t.Logf("VERIFY-C3 unit tracker, after 1st Close():  interceptorsCreated=%d interceptorsClosed=%d liveInterceptorsCount=%d", fb.interceptorsCreated.Load(), fb.interceptorsClosed.Load(), fb.liveInterceptorsCount.Load())
	i.Close()
	t.Logf("VERIFY-C3 unit tracker, after 2nd Close():  interceptorsCreated=%d interceptorsClosed=%d liveInterceptorsCount=%d", fb.interceptorsCreated.Load(), fb.interceptorsClosed.Load(), fb.liveInterceptorsCount.Load())

	failed := false
	if got, want := fb.interceptorsClosed.Load(), int32(1); got != want {
		failed = true
		t.Errorf("VERIFY-C3 exact-count assertion FIRED: closed %d interceptors, want %d", got, want)
	}
	if got, want := fb.liveInterceptorsCount.Load(), int32(0); got != want {
		failed = true
		t.Errorf("VERIFY-C3 exact-count assertion FIRED: %d live interceptors, want %d", got, want)
	}
	if got, want := fb.interceptorsClosed.Load(), fb.interceptorsCreated.Load(); got != want {
		failed = true
		t.Errorf("VERIFY-C3 exact-count assertion FIRED: closed %d interceptors, want %d (all created)", got, want)
	}
	if !failed {
		t.Logf("VERIFY-C3 unit tracker: no exact-count assertion fired after a duplicate Close() on the same instance")
	}
}
