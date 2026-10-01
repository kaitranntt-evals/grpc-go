#!/bin/bash
# Run: verify/repro/c2_creation_ordering_eb7df664.sh <worktree of evalon/grpc-go-xd-eb7df664> [delay, default 300ms]
#
# C2 (creation_assertion_ordering) probe for eb7df664. This branch retires the
# old configuration first and only then builds its replacement, per filter
# chain (updateRouteConfiguration: close(); constructUsableRouteConfiguration()).
# The added test polls interceptorsDestroyed and then immediately asserts
# interceptorsCreated. The probe (a) logs both counters at the instant the
# destruction poll finishes and (b) inserts a benign scheduling delay at the
# top of the production constructUsableRouteConfiguration, i.e. between "old
# interceptor closed" and "replacement interceptor created". No behaviour is
# changed. All edits are reverted afterwards.
set -u
wt=$1; delay=${2:-300ms}
cd "$wt" || exit 2
tf=test/xds/xds_server_filter_state_retention_test.go
pf=internal/xds/server/filter_chain_manager.go
re='^Test$/^ServerSideXDS_FilterStateRetention_AcrossUpdates_RDS$'
filter='tlogger.go|^=== (RUN|PAUSE|CONT)|logging.go'
show() { grep -Ev "$filter" | grep -E '_test.go:[0-9]+: |^(---|    ---|ok|FAIL|PASS|panic)'; }
echo "### branch HEAD: $(git log --oneline -1)"
echo "### test code under probe ($tf):"; sed -n '722,732p' $tf
# (a) counter checkpoint right after the destruction poll's own assertion.
sed -i '730s/^\t\tif got, want := interceptorsCreated.Load()/\t\tt.Logf("PROBE destruction poll finished (update %d): interceptorsDestroyed=%d interceptorsCreated=%d (asserted next: %d)", update, interceptorsDestroyed.Load(), interceptorsCreated.Load(), (update+1)*len(inboundLis.FilterChains))\n&/' $tf
cat > internal/xds/server/zz_verify_c2_delay.go <<'GO'
package server

import (
	"os"
	"time"
)

// verifyC2Delay is audit instrumentation: a scheduling delay taken from
// VERIFY_C2_CREATE_DELAY.
func verifyC2Delay() {
	if d, err := time.ParseDuration(os.Getenv("VERIFY_C2_CREATE_DELAY")); err == nil {
		time.Sleep(d)
	}
}
GO
sed -i 's/^func (fc \*filterChain) constructUsableRouteConfiguration(.*{$/&\n\tverifyC2Delay()/' $pf
echo "### instrumentation:"; git diff -U0 -- $tf $pf | grep -E '^[+-][^+-]|^@@'
echo "### [no delay] go test -race -count=1 -v -run '$re' ./test/xds/"
go test -race -count=1 -v -run "$re" ./test/xds/ 2>&1 | show
echo "### [replacement construction delayed by $delay] VERIFY_C2_CREATE_DELAY=$delay go test -race -count=1 -v -run '$re' ./test/xds/"
VERIFY_C2_CREATE_DELAY=$delay go test -race -count=1 -v -run "$re" ./test/xds/ 2>&1 | show
git checkout -- $tf $pf; rm internal/xds/server/zz_verify_c2_delay.go
echo "### restored; git status --short: [$(git status --short)]"
