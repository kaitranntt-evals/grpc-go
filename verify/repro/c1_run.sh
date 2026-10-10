#!/bin/bash
# Run: bash verify/repro/c1_run.sh  (from the repo root; needs network access to the claim repository on first use)
# Shows the lifecycle test's live-count poll releasing at created=21 destroyed=19 and the
# "want: 22" assertion failing when the second filter chain's update is delayed.
set -euo pipefail
ROOT="$(git rev-parse --show-toplevel)"
WT="${WT:-$HOME/wt-c1-repro}"
git remote get-url claims >/dev/null 2>&1 || git remote add claims https://github.com/kaitranntt-evals/grpc-go-xds-rds-interceptor-lifecycle-leak
git fetch claims evalon/grpc-go-xd-c08e0d0f:refs/remotes/claims/evalon/grpc-go-xd-c08e0d0f
[ -d "$WT" ] || git worktree add "$WT" claims/evalon/grpc-go-xd-c08e0d0f
cd "$WT"
git checkout -q .
git apply "$ROOT/verify/repro/c1_pause_between_chains.patch"
T='^Test$/^ServerSideXDS_FilterStateRetention_AcrossUpdates_RouteConfigChange$'
F='VERIFY|^(---|ok|FAIL|PASS)|Created .* want'
echo "### no pause (expected: PASS, final poll released at created=22 destroyed=20)"
go test -race -count=1 -v -run "$T" ./test/xds 2>&1 | grep -E "$F" | tail -4 || true
echo "### 500ms pause before the 2nd filter chain of the last RDS update (expected: FAIL, Created 21 want 22)"
VERIFY_RDS_PAUSE=500ms VERIFY_RDS_PAUSE_CALL=11 go test -race -count=1 -v -run "$T" ./test/xds 2>&1 | grep -E "$F" | tail -7 || true
git checkout -q .
