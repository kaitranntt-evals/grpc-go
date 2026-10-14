#!/bin/bash
# Run: verify/repro/c1_probe.sh <worktree-of-claim-branch> <added-e2e-test-name-without-Test-prefix> [unit-test-regex]   (restores the worktree afterwards)
set -uo pipefail
WT=${1:?worktree}; NAME=${2:?test name}; UNIT=${3:-}
HERE=$(cd "$(dirname "$0")" && pwd)
F=test/xds/xds_server_filter_state_retention_test.go
RUN="^Test\$/^${NAME}\$"
FILT='C1-PROBE|^(--- |\s+--- |FAIL|ok|PASS)|_test.go:[0-9]+: |DATA RACE|panic'
cd "$WT"
echo "##### branch $(git rev-parse --short HEAD) test $NAME"
echo "===== 1. unmodified: go test -race -count=20 -run '$RUN' ./test/xds"
go test -race -count=20 -run "$RUN" ./test/xds 2>&1 | grep -E "$FILT" | sort | uniq -c
if [ -n "$UNIT" ]; then
  echo "===== 1b. unmodified unit lifecycle tests: go test -race -count=50 -run '^Test\$/^($UNIT)' ./internal/xds/server"
  go test -race -count=50 -run "^Test\$/^($UNIT)" ./internal/xds/server 2>&1 | grep -E "$FILT" | sort | uniq -c
fi
cp "$HERE/c1_close_probe_test.go.txt" test/xds/zz_c1_close_probe_test.go
sed -i 's/^\ti.parent.interceptorsDestroyed.Add(1)$/\tc1Probe("interceptor"); i.parent.interceptorsDestroyed.Add(1)/; s/^\tt.filtersDestroyed.Add(1)$/\tc1Probe("filter"); t.filtersDestroyed.Add(1)/' $F
git diff --stat -- $F | head -1
echo "===== 2. trace of who runs the cleanup (C1_TRACE=1, -count=1)"
C1_TRACE=1 go test -race -count=1 -v -run "$RUN" ./test/xds 2>&1 | grep -E "$FILT" | sed -E 's/\[?goroutine [0-9]+ \[running\]/goroutine N/' | uniq -c
echo "===== 3. every Close() delayed 150ms before the counter moves (C1_DELAY_MS=150, -race -count=5)"
C1_DELAY_MS=150 go test -race -count=5 -v -run "$RUN" ./test/xds 2>&1 | grep -E "$FILT" | sort | uniq -c
git checkout -- $F; rm test/xds/zz_c1_close_probe_test.go; git status --short
