#!/usr/bin/env bash
# Run: FIXTURE=<path to eval_xds_server_interceptor_leak_test.go> verify/repro/c1/run_c1.sh <worktree-of-claim-branch> <short-branch-id> [runs-per-process]   (e.g. run_c1.sh ~/wt/1ea7dd6e 1ea7dd6e 125)
#
# For one C1 branch this:
#   0. runs the branch's own route-replacement integration test unmodified (baseline),
#   1. stress-runs it unmodified under CPU contention (natural flake rate),
#   2. applies mutation B (production: sleep between publishing the replacement
#      and closing the retired interceptors -- a pure scheduling delay, no
#      semantic change) and re-runs the branch's test (expected: FAIL on a
#      destruction-count assertion) and the eval fixture, which polls the
#      destruction counter (expected: PASS),
#   3. reverts B, applies mutation A (test double only: trackingInterceptor.Close
#      sleeps before it increments the destruction counter) and re-runs the
#      branch's test (expected: FAIL on a destruction-count assertion),
#   4. on the branches whose test uses two filter chains, applies mutation A2
#      (test double only: only the last Close of each update is slow), which
#      lets the created-count assertion pass and isolates the destruction-count
#      assertion,
#   5. reverts everything.  STEPS="0 1 2 3 4" selects steps.
# The worktree is left byte-identical to the branch head.
set -uo pipefail
WT=$1; ID=$2; STRESS=${3:-125}; PROCS=${PROCS:-24}
HERE=$(cd "$(dirname "$0")" && pwd)
FIXTURE=${FIXTURE:-$HOME/eval/tests/eval_xds_server_interceptor_leak_test.go}
case $ID in
  8de853fe) T=ServerSideXDS_FilterStateRetention_RepeatedRouteConfigUpdates ;;
  1ea7dd6e) T=ServerSideXDS_RouteConfigurationUpdates_ReleaseSupersededResources ;;
  8f0663b7) T=ServerSideXDS_RouteConfigUpdate_ReleasesSupersededResources ;;
  bbe58e09) T=ServerSideXDS_FilterStateRetention_AcrossUpdates_RouteConfigChange ;;
  295929ad) T=ServerSideXDS_FilterStateRetention_AcrossUpdates_RouteConfigChange ;;
  *) echo "unknown branch id $ID"; exit 2 ;;
esac
cd "$WT" || exit 2
git status --short | grep -q . && { echo "worktree not clean"; exit 2; }
echo ">> branch evalon/grpc-go-xd-$ID @ $(git rev-parse --short HEAD), test Test/$T"
STEPS=${STEPS:-0 1 2 3 4}
want() { case " $STEPS " in *" $1 "*) return 0 ;; *) return 1 ;; esac; }
summ() { grep -E "^(=== RUN|--- |    --- |ok|FAIL|PASS|panic)|_test.go:[0-9]+: (After|Update|Destroyed|Created|EmptyCall|Timeout)" | grep -v "^=== RUN" | cut -c1-260; }

if want 0; then
echo; echo ">> step 0: baseline (unmodified)"
echo "\$ go test -run '^Test\$/^$T\$' ./test/xds -race -count=1"
go test -v -run "^Test\$/^$T\$" ./test/xds -race -count=1 2>&1 | summ

cp "$FIXTURE" test/xds/eval_xds_server_interceptor_leak_test.go
echo "\$ go test -run '^Test\$/^Eval_' ./test/xds -race -count=1   # eval fixture, unmodified branch (reference for step 2)"
go test -v -run '^Test$/^Eval_' ./test/xds -race -count=1 2>&1 | summ
rm -f test/xds/eval_xds_server_interceptor_leak_test.go
fi

if want 1; then
echo; echo ">> step 1: natural stress (unmodified): $PROCS concurrent processes x $STRESS runs each of the -race test binary"
echo "\$ go test -race -c -o /tmp/xds_$ID.test ./test/xds && (cd test/xds && for i in \$(seq $PROCS); do /tmp/xds_$ID.test -test.run '^Test\$/^$T\$' -test.count=$STRESS -test.v > /tmp/c1_stress_${ID}_\$i.log 2>&1 & done; wait)"
go test -race -c -o /tmp/xds_$ID.test ./test/xds || exit 1
rm -f /tmp/c1_stress_${ID}_*.log
(cd test/xds && for i in $(seq "$PROCS"); do /tmp/xds_$ID.test -test.run "^Test\$/^$T\$" -test.count="$STRESS" -test.v > /tmp/c1_stress_${ID}_$i.log 2>&1 & done; wait)
echo "passes: $(cat /tmp/c1_stress_${ID}_*.log | grep -c "^    --- PASS: Test/$T")  failures: $(cat /tmp/c1_stress_${ID}_*.log | grep -c "^    --- FAIL: Test/$T")"
echo "failure messages (count, message):"
cat /tmp/c1_stress_${ID}_*.log | grep -E "^    [a-z_]+_test.go:[0-9]+: " | sed -E 's/^ +//' | sort | uniq -c | cut -c1-260
fi

if want 2; then
echo; echo ">> step 2: mutation B (production: 100ms sleep between Swap() and closing the retired interceptors)"
python3 "$HERE/mutate.py" B "$ID" && git diff --stat | cat && git diff -U1 -- internal/xds/server/filter_chain_manager.go | grep '^[+-]' | grep -v '^+++\|^---'
echo "\$ go test -run '^Test\$/^$T\$' ./test/xds -race -count=1   # branch's own test"
go test -v -run "^Test\$/^$T\$" ./test/xds -race -count=1 2>&1 | summ
cp "$FIXTURE" test/xds/eval_xds_server_interceptor_leak_test.go
echo "\$ go test -run '^Test\$/^Eval_' ./test/xds -race -count=1   # eval fixture (polls the destruction counter), same mutation"
go test -v -run '^Test$/^Eval_' ./test/xds -race -count=1 2>&1 | summ
rm -f test/xds/eval_xds_server_interceptor_leak_test.go
git checkout -q -- . 
fi

if want 3; then
echo; echo ">> step 3: mutation A (test double only: trackingInterceptor.Close sleeps 100ms before counting itself destroyed)"
python3 "$HERE/mutate.py" A "$ID" && git diff --stat | cat && git diff -U1 -- test/xds | grep '^[+-]' | grep -v '^+++\|^---'
echo "\$ go test -run '^Test\$/^$T\$' ./test/xds -race -count=1"
go test -v -run "^Test\$/^$T\$" ./test/xds -race -count=1 2>&1 | summ
git checkout -q -- .
fi

if want 4; then
echo; echo ">> step 4: mutation A2 (test double only: every 2nd trackingInterceptor.Close -- the last one of each two-filter-chain update -- sleeps 100ms before counting itself destroyed)"
case $ID in
  1ea7dd6e|8f0663b7|bbe58e09)
    python3 "$HERE/mutate.py" A2 "$ID" && git diff --stat | cat && git diff -U1 -- test/xds | grep '^[+-]' | grep -v '^+++\|^---'
    echo "\$ go test -run '^Test\$/^$T\$' ./test/xds -race -count=5"
    go test -v -run "^Test\$/^$T\$" ./test/xds -race -count=5 2>&1 | summ
    git checkout -q -- . ;;
  *) echo "(not applicable: this branch's test uses a single filter chain, so mutation A already isolates the destruction assertion)" ;;
esac
fi

git status --short | grep -q . && echo "WARNING: worktree not clean" || echo "(worktree restored to branch head)"
