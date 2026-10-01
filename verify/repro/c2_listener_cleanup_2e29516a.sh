#!/bin/bash
# Run: verify/repro/c2_listener_cleanup_2e29516a.sh <worktree of evalon/grpc-go-xd-2e29516a>
#
# C2 (listener_cleanup_ordering) probe for TestListenerWrapper_RDSUpdateClosesRetiredInterceptors.
# Adds observation only: a t.Cleanup (runs after the test body and all its
# defers) that dials the listener the test created and reports whether it is
# still open. Then runs the test twice: as written, and with the setup helper
# serverListenerUpdate (called between listener creation and `defer lw.Close()`)
# forced to t.Fatalf. Test files are restored afterwards.
set -u
cd "$1" || exit 2
f=internal/xds/server/listener_wrapper_test.go
echo "### control flow between listener creation and cleanup registration ($f):"
grep -n -A24 '^func (s) TestListenerWrapper_RDSUpdateClosesRetiredInterceptors' $f | grep -E 'LocalTCPListener\(\)|newFakeXDSClient\(t\)|serverListenerUpdate\(t|defer |t\.Cleanup'
echo "### fatal exits inside the helpers called in that span:"
awk '/^func newFakeXDSClient/,/^}/' $f | grep -n 't.Fatal' | sed 's/^/newFakeXDSClient: /'
awk '/^func serverListenerUpdate/,/^}/' $f | grep -n 't.Fatal' | sed 's/^/serverListenerUpdate: /'
cat > internal/xds/server/zz_verify_c2_lis_probe_test.go <<'GO'
package server

import (
	"net"
	"os"
	"testing"
	"time"
)

var verifyC2InjectFatal = os.Getenv("VERIFY_C2_INJECT_FATAL") != ""

// verifyC2WatchListener reports, after the test body and all of its deferred
// calls have run, whether the listener is still accepting connections.
func verifyC2WatchListener(t *testing.T, lis net.Listener) {
	t.Cleanup(func() {
		c, err := net.DialTimeout("tcp", lis.Addr().String(), time.Second)
		if err != nil {
			t.Logf("PROBE after test exit: listener is closed (dial error: %v)", err)
			return
		}
		c.Close()
		t.Logf("PROBE after test exit: listener %v is STILL OPEN (dial succeeded; nothing closed it)", lis.Addr())
		lis.Close()
	})
}
GO
sed -i 's/^\txdsC := newFakeXDSClient(t)$/\tverifyC2WatchListener(t, lis)\n&/' $f
ln=$(grep -n '^func serverListenerUpdate' $f | cut -d: -f1)
sed -i "$((ln+1))s/\$/\n\tif verifyC2InjectFatal {\n\t\tt.Fatalf(\"verify: injected setup-helper failure\")\n\t}/" $f
echo "### instrumentation (test file only):"; git diff -U0 -- $f | grep -E '^[+-][^+-]'
filter='tlogger.go:1[0-9][0-9]|^=== (RUN|PAUSE|CONT)'
re='^Test$/^ListenerWrapper_RDSUpdateClosesRetiredInterceptors$'
echo "### [helper succeeds] go test -race -count=1 -v -run '$re' ./internal/xds/server/"
go test -race -count=1 -v -run "$re" ./internal/xds/server/ 2>&1 | grep -Ev "$filter" | grep -E 'PROBE|verify:|^(---|    ---|ok|FAIL|PASS)' | sed 's/^ *tlogger.go:[0-9]*: INFO //'
echo "### [helper fatals]   VERIFY_C2_INJECT_FATAL=1 go test -race -count=1 -v -run '$re' ./internal/xds/server/"
VERIFY_C2_INJECT_FATAL=1 go test -race -count=1 -v -run "$re" ./internal/xds/server/ 2>&1 | grep -Ev "$filter" | grep -E 'PROBE|verify:|^(---|    ---|ok|FAIL|PASS)' | sed 's/^ *tlogger.go:[0-9]*: INFO //'
git checkout -- $f; rm internal/xds/server/zz_verify_c2_lis_probe_test.go
echo "### restored; git status --short: [$(git status --short)]"
