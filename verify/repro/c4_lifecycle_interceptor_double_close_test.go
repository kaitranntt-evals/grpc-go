// Expected on the audited branch: test FAILS with "MASKED: second Close left every counter the cleanup assertions read unchanged".
// Run: cp verify/repro/c4_lifecycle_interceptor_double_close_test.go internal/xds/server/ && go test -race -v -count=1 -run '^Test$/^VerifyC4_' ./internal/xds/server   (on branch evalon/grpc-go-xd-e02f4bfe)
// Mutation companion (shows the branch's own test stays green on a production double close): verify/repro/c4_double_close_mutation.sh

package server

import "testing"

// Calls Close twice on the branch's lifecycleTrackingInterceptor and prints
// every piece of state the branch's cleanup assertions (verifyCounts) consume.
func (s) TestVerifyC4_LifecycleInterceptorDoubleClose(t *testing.T) {
	tf := &lifecycleTrackingFilter{}
	tf.BuildServerFilter()
	icpt, err := tf.BuildServerInterceptor(nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	snap := func() [4]int32 {
		return [4]int32{tf.interceptorsBuilt.Load(), tf.interceptorsClosed.Load(), tf.filtersBuilt.Load(), tf.filtersClosed.Load()}
	}
	t.Logf("OBSERVED before Close:      [interceptorsBuilt interceptorsClosed filtersBuilt filtersClosed] = %v", snap())
	icpt.Close()
	one := snap()
	t.Logf("OBSERVED after 1st Close(): [interceptorsBuilt interceptorsClosed filtersBuilt filtersClosed] = %v", one)
	icpt.Close()
	two := snap()
	t.Logf("OBSERVED after 2nd Close(): [interceptorsBuilt interceptorsClosed filtersBuilt filtersClosed] = %v", two)
	t.Logf("OBSERVED interceptor.closed flag after 2nd Close(): %v (a bool; cannot distinguish 1 call from 2)", icpt.(*lifecycleTrackingInterceptor).closed.Load())
	if one == two {
		t.Errorf("MASKED: second Close left every counter the cleanup assertions read unchanged (%v == %v)", one, two)
	}
}
