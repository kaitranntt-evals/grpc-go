// Run: cp verify/repro/c3_comparator_irreflexive_test.go <worktree of evalon/grpc-go-se-2e6a7cdc>/zz_verify_c3_test.go && go test . -run '^TestVerifyC3_' -count=1 -v
//
// Evaluates the ordering comparator that interceptorRecorder.check in
// server_rpc_ext_test.go (branch evalon/grpc-go-se-2e6a7cdc) passes to
// cmpopts.SortSlices.  The comparator body below is copied verbatim from that
// file (lines 92-100).  cmpopts.SortSlices requires the less function to be
// irreflexive (!less(x, x)); this test fails when the comparator violates that.
package grpc_test

import (
	"testing"
)

// verifyC3Record mirrors interceptorRecord from server_rpc_ext_test.go.
type verifyC3Record struct {
	Method         string
	IsClientStream bool
	IsServerStream bool
}

// verifyC3Less is the comparator from interceptorRecorder.check, verbatim.
func verifyC3Less(a, b verifyC3Record) bool {
	if a.Method != b.Method {
		return a.Method < b.Method
	}
	if a.IsClientStream != b.IsClientStream {
		return !a.IsClientStream
	}
	return !a.IsServerStream
}

func TestVerifyC3_ComparatorIrreflexive(t *testing.T) {
	cases := []verifyC3Record{
		{Method: "/grpc.testing.TestService/UnaryCall"},                                 // unary record: both flags false
		{Method: "/grpc.testing.TestService/StreamingInputCall", IsClientStream: true},  // IsServerStream=false
		{Method: "/grpc.testing.TestService/StreamingOutputCall", IsServerStream: true}, // IsServerStream=true
		{Method: "/grpc.testing.TestService/FullDuplexCall", IsClientStream: true, IsServerStream: true},
	}
	for _, x := range cases {
		got := verifyC3Less(x, x)
		t.Logf("less(x, x) for %+v = %v", x, got)
		if got {
			t.Errorf("less(x, x) = true for %+v; cmpopts.SortSlices requires !less(x, x)", x)
		}
	}
}
