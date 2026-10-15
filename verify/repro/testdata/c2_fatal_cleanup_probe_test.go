// Run: on branch evalon/grpc-go-xd-9207949e, `git apply verify/repro/testdata/c2_test_instrumentation.patch`, copy this file from verify/repro/testdata/ to internal/xds/server/, then `VERIFY_C2_INJECT_FATAL=1 go test -race -count=1 -v -run '^Test$/^VerifyC2_' ./internal/xds/server 2>&1 | grep -E 'VERIFY-C2|listener_wrapper_test.go|^\s*--- |^(ok|FAIL|PASS)'`

package server

import (
	"net"
	"os"
	"testing"
	"time"
)

// Set by the two instrumentation lines that c2_test_instrumentation.patch adds
// to TestListenerWrapper_RDSUpdatesReleaseSupersededRouteConfiguration.
var (
	verifyC2Lis         net.Listener
	verifyC2Cfb         *countingFilterBuilder
	verifyC2InjectFatal bool
)

// Runs the branch's lifecycle regression as a subtest and then, after that
// subtest has fully exited (including its defers and t.Cleanup functions),
// probes whether the TCP listener and the server filter it created were closed.
func (s) TestVerifyC2_ResourcesAfterRegressionExits(t *testing.T) {
	verifyC2InjectFatal = os.Getenv("VERIFY_C2_INJECT_FATAL") == "1"
	defer func() { verifyC2InjectFatal = false }()

	passed := t.Run("regression", func(t *testing.T) {
		s{}.TestListenerWrapper_RDSUpdatesReleaseSupersededRouteConfiguration(t)
	})
	t.Logf("VERIFY-C2 regression subtest exited: passed=%v injectFatal=%v", passed, verifyC2InjectFatal)

	cfb := verifyC2Cfb
	t.Logf("VERIFY-C2 filters: created=%d closed=%d (still open=%d)", cfb.filtersCreated.Load(), cfb.filtersClosed.Load(), cfb.filtersCreated.Load()-cfb.filtersClosed.Load())
	t.Logf("VERIFY-C2 interceptors: created=%d closed=%d (still open=%d)", cfb.interceptorsCreated.Load(), cfb.interceptorsClosed.Load(), cfb.interceptorsCreated.Load()-cfb.interceptorsClosed.Load())

	addr := verifyC2Lis.Addr().String()
	conn, dialErr := net.DialTimeout("tcp", addr, time.Second)
	if dialErr == nil {
		conn.Close()
	}
	t.Logf("VERIFY-C2 dial listener %s after subtest exit: err=%v (nil => listening socket still open)", addr, dialErr)
	// Closing an already-closed *net.TCPListener returns "use of closed network
	// connection"; a nil error means this call was the first close.
	closeErr := verifyC2Lis.Close()
	t.Logf("VERIFY-C2 listener.Close() from probe: err=%v (nil => the regression left the listener open)", closeErr)
	if closeErr == nil || cfb.filtersCreated.Load() != cfb.filtersClosed.Load() {
		t.Errorf("VERIFY-C2 RESOURCES LEFT OPEN after the regression exited: listenerStillOpen=%v filtersStillOpen=%d", closeErr == nil, cfb.filtersCreated.Load()-cfb.filtersClosed.Load())
	} else {
		t.Logf("VERIFY-C2 all resources were closed by the time the regression exited")
	}
}
