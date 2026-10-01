#!/bin/bash
# Run: verify/repro/c2_delay_retired_close.sh <worktree of a C2 target branch> [delay, default 300ms]
#
# C2 (retirement_assertion_ordering) probe. Runs the branch's own added
# test/xds lifecycle test(s) twice: unmodified, and with a benign scheduling
# delay inserted at the top of the production (*interceptorList).Close (the one
# place every interceptor is closed from). The delay changes no behaviour, it
# only widens the window between "replacement configuration is serving
# traffic" and "retired interceptors have finished closing". A test whose
# destruction assertions wait for closure stays green; a test that asserts
# right after observing replacement traffic fails. Production code is restored
# afterwards.
set -u
wt=$1; delay=${2:-300ms}
cd "$wt" || exit 2
tests=$(git diff 4ee6ac46 HEAD -- 'test/xds/*_test.go' | sed -n 's/^+func (s) Test\([A-Za-z0-9_]*\)(.*/\1/p' | paste -sd'|')
re="^Test\$/^($tests)\$"
filter='tlogger.go|^=== (RUN|PAUSE|CONT)|logging.go'
echo "### branch HEAD: $(git log --oneline -1)"
echo "### added test/xds tests: $tests"
echo "### [unmodified] go test -race -count=1 -v -run '$re' ./test/xds/"
go test -race -count=1 -v -run "$re" ./test/xds/ 2>&1 | grep -Ev "$filter" | grep -E '_test.go:[0-9]+: |^(---|    ---|ok|FAIL|PASS|panic)'
f=internal/xds/server/filter_chain_manager.go
cat > internal/xds/server/zz_verify_c2_delay.go <<'GO'
package server

import (
	"os"
	"time"
)

// verifyC2Delay is audit instrumentation: a scheduling delay before
// interceptors are closed, taken from VERIFY_C2_CLOSE_DELAY.
func verifyC2Delay() {
	if d, err := time.ParseDuration(os.Getenv("VERIFY_C2_CLOSE_DELAY")); err == nil {
		time.Sleep(d)
	}
}
GO
sed -i 's/^func (il \*interceptorList) Close() {$/&\n\tverifyC2Delay()/' $f
echo "### instrumentation:"; git diff -U1 -- $f | grep -E '^[+-][^+-]|^@@'
echo "### [close delayed by $delay] VERIFY_C2_CLOSE_DELAY=$delay go test -race -count=1 -v -run '$re' ./test/xds/"
VERIFY_C2_CLOSE_DELAY=$delay go test -race -count=1 -v -run "$re" ./test/xds/ 2>&1 | grep -Ev "$filter" | grep -E '_test.go:[0-9]+: |^(---|    ---|ok|FAIL|PASS|panic)'
git checkout -- $f; rm internal/xds/server/zz_verify_c2_delay.go
echo "### restored; git status --short: [$(git status --short)]"
