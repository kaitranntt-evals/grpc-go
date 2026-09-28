// Run (in a worktree of evalon/grpc-go-xd-554a9276):
//
//	git apply verify/repro/c2_helper_fatal_injection.patch && cp verify/repro/c2_cleanup_order_test.go internal/xds/server/ && go test -count=1 -v -run 'Test/TestVerify_C2' ./internal/xds/server
//
// Probe for claim C2 part 1 on branch evalon/grpc-go-xd-554a9276: the added
// helper newListenerWrapperForRDSTest creates a TCP listener, then runs a
// t.Fatalf assertion, and only afterwards registers t.Cleanup(lis.Close).
// The patch makes that assertion fire (and records the listener address); this
// test then dials the address after the helper's subtest has finished. A
// successful dial means the kernel still has the socket listening, i.e. the
// listener was leaked by the fatal path.
package server

import (
	"net"
	"testing"
	"time"
)

var (
	verifyC2LisAddr     string
	verifyC2InjectFatal = true // set false for the control run (helper assertion passes, cleanup registered)
)

func (s) TestVerify_C2_HelperFatalLeavesListenerOpen(t *testing.T) {
	fb := &closeTrackingFilterBuilder{}
	ok := t.Run("helper", func(t *testing.T) {
		newListenerWrapperForRDSTest(t, "verify-route", fb)
	})
	t.Logf("helper subtest passed=%v; listener address recorded=%q", ok, verifyC2LisAddr)
	if verifyC2LisAddr == "" {
		t.Fatalf("helper did not record its listener address; is the patch applied?")
	}
	conn, err := net.DialTimeout("tcp", verifyC2LisAddr, time.Second)
	if err == nil {
		conn.Close()
		t.Errorf("C2 part 1 CONFIRMED: listener %s still accepts connections after the helper's subtest ended (cleanup was never registered because Fatalf ran first)", verifyC2LisAddr)
		return
	}
	t.Logf("C2 part 1 REFUTED: dial to %s failed after the helper's subtest ended: %v (listener was closed)", verifyC2LisAddr, err)
}
