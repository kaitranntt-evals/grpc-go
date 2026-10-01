#!/bin/bash
# Run: verify/repro/c2_counters_after_traffic_18fa6ce5.sh <worktree of evalon/grpc-go-xd-18fa6ce5>
#
# C2 (retirement_assertion_ordering) supplement for 18fa6ce5, whose delayed run
# trips the creation assertion first. Adds one t.Logf (observation only) at the
# point where the replacement-traffic loop exits, printing the counters the
# following assertions compare, then runs the test with the same 300ms delay
# before production (*interceptorList).Close. Everything is restored afterwards.
set -u
cd "$1" || exit 2
t=test/xds/xds_server_filter_state_retention_test.go
f=internal/xds/server/filter_chain_manager.go
ln=$(grep -n 'Created %d filter instances after %d in-place RDS update' $t | cut -d: -f1)
echo "### assertions that follow the replacement-traffic loop ($t):"; sed -n "$((ln-1)),$((ln+11))p" $t
sed -i "$((ln-1))s/^/\t\tt.Logf(\"PROBE replacement traffic observed for update %d: interceptorsCreated=%d (asserted next: %d) interceptorsDestroyed=%d (asserted next: %d)\", i, interceptorsCreated.Load(), 2*(i+1), interceptorsDestroyed.Load(), 2*i)\n/" $t
cat > internal/xds/server/zz_verify_c2_delay.go <<'GO'
package server

import (
	"os"
	"time"
)

func verifyC2Delay() {
	if d, err := time.ParseDuration(os.Getenv("VERIFY_C2_CLOSE_DELAY")); err == nil {
		time.Sleep(d)
	}
}
GO
sed -i 's/^func (il \*interceptorList) Close() {$/&\n\tverifyC2Delay()/' $f
re='^Test$/^ServerSideXDS_FilterStateRetention_AcrossUpdates_RouteConfigChange$'
for d in 0s 300ms; do
  echo "### VERIFY_C2_CLOSE_DELAY=$d go test -race -count=1 -v -run '$re' ./test/xds/"
  VERIFY_C2_CLOSE_DELAY=$d go test -race -count=1 -v -run "$re" ./test/xds/ 2>&1 | grep -Ev 'tlogger.go|^=== |logging.go' | grep -E 'PROBE|_test.go:[0-9]+: (Created|Destroyed)|^(---|    ---|ok|FAIL|PASS)'
done
git checkout -- $t $f; rm internal/xds/server/zz_verify_c2_delay.go
echo "### restored; git status --short: [$(git status --short)]"
