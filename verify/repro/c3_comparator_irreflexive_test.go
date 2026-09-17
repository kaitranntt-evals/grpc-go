// Run: cp verify/repro/c3_comparator_irreflexive_test.go . && go test . -run 'Test/Verify_C3_' -count=1 -v
//
// Exercises the ordering comparator used by interceptorRecorder.check in
// server_rpc_ext_test.go (branch evalon/grpc-go-se-2e6a7cdc) with the same
// record supplied as both operands.  A strict ordering must report
// less(x, x) == false; the comparator's final `return !a.IsServerStream`
// reports true for any record with IsServerStream == false.

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

// verifyC3Less is a verbatim copy of the comparator passed to
// cmpopts.SortSlices in interceptorRecorder.check.
func verifyC3Less(a, b verifyC3Record) bool {
	if a.Method != b.Method {
		return a.Method < b.Method
	}
	if a.IsClientStream != b.IsClientStream {
		return !a.IsClientStream
	}
	return !a.IsServerStream
}

func (s) TestVerify_C3_ComparatorIsIrreflexive(t *testing.T) {
	records := []verifyC3Record{
		{Method: "/grpc.testing.TestService/StreamingInputCall", IsClientStream: true, IsServerStream: false},
		{Method: "/grpc.testing.TestService/UnaryCall", IsClientStream: false, IsServerStream: false},
		{Method: "/grpc.testing.TestService/StreamingOutputCall", IsClientStream: false, IsServerStream: true},
		{Method: "/grpc.testing.TestService/FullDuplexCall", IsClientStream: true, IsServerStream: true},
	}
	for _, r := range records {
		got := verifyC3Less(r, r)
		t.Logf("less(x, x) for %+v = %v", r, got)
		if got {
			t.Errorf("comparator reports %+v as less than itself (IsServerStream=%v); a strict ordering must return false", r, r.IsServerStream)
		}
	}
}
