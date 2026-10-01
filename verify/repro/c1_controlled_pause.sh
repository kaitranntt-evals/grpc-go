#!/bin/bash
# Run (from the repo root): verify/repro/c1_controlled_pause.sh <branch-id> [close|build|close2]   e.g. `verify/repro/c1_controlled_pause.sh b66819a5 close` — checks out evalon/grpc-go-xd-<branch-id> into a worktree, inserts a 150ms pause into the test double of that branch's added lifecycle test, runs the test under -race, reverts. FAIL in well under the 10s test deadline = premature assertion.
#
# Modes (test-file-only edits, see verify/instrumentation/c1_pause.py):
#   close  : pause in trackingInterceptor.Close() before the "destroyed" counter is bumped (retirement still in progress)
#   build  : pause in BuildServerInterceptor() before the "created" counter is bumped (filter-chain construction still in progress)
#   close2 : pause only in every second retired interceptor's Close() (two-filter-chain listeners; used for 6d02dfd3)
set -u
b=${1:?branch id, e.g. b66819a5}; mode=${2:-close}
root=$(git rev-parse --show-toplevel)
repo=${CLAIMS_REPO:-https://github.com/kaitranntt-evals/grpc-go-xds-rds-interceptor-lifecycle-leak}
wt=${WT_DIR:-$HOME/wt}/$b
case $b in
  89904dd8) T=ServerSideXDS_FilterStateRetention_RepeatedRouteConfigUpdates;;
  b66819a5) T=ServerSideXDS_FilterStateRetention_RepeatedRDSUpdates;;
  6d02dfd3) T=ServerSideXDS_RDSUpdates_ReleaseSupersededInterceptors;;
  8520d18f) T=ServerSideXDS_FilterStateRetention_AcrossRDSUpdates;;
  df8b80c6) T=ServerSideXDS_FilterStateRetention_AcrossRouteConfigUpdates;;
  a7a2d47b) T=ServerSideXDS_RepeatedRDSUpdates_ReleaseSupersededInterceptors;;
  0628e30a|d36e1629|6c895192|3e71ce68) T=ServerSideXDS_FilterStateRetention_AcrossUpdates_RouteConfigChange;;
  edda3c72) T=ServerSideXDS_RouteConfigUpdate_ReleasesSupersededResources;;
  *) echo "unknown branch id $b"; exit 2;;
esac
if [ ! -d "$wt" ]; then
  git fetch "$repo" "evalon/grpc-go-xd-$b" && git worktree add --detach "$wt" FETCH_HEAD || exit 2
fi
f=test/xds/xds_server_filter_state_retention_test.go   # the tracking test double lives here on every branch
cd "$wt" || exit 2
echo "branch evalon/grpc-go-xd-$b @ $(git rev-parse HEAD), test $T, pause mode $mode"
if [ "$mode" = close2 ]; then
  python3 - "$f" <<'PY' || exit 2
import sys
p = sys.argv[1]; s = open(p).read()
old = "func (i *trackingInterceptor) Close() {\n\ti.parent.interceptorsDestroyed.Add(1)\n"
new = "func (i *trackingInterceptor) Close() {\n\tif i.parent.interceptorsDestroyed.Load()%2 == 1 {\n\t\ttime.Sleep(150 * time.Millisecond) // verify: controlled pause\n\t}\n\ti.parent.interceptorsDestroyed.Add(1)\n"
assert s.count(old) == 1, "anchor not found"
open(p, "w").write(s.replace(old, new))
PY
else
  python3 "$root/verify/instrumentation/c1_pause.py" "$mode" "$f" || exit 2
fi
go test -race -v -count=1 ./test/xds -run "^Test\$/^${T}\$" 2>&1 | grep -E "^\s+\S+_test.go:[0-9]+: (After|Timeout|Created|Destroyed|Unexpected|EmptyCall)|^\s*--- |^(PASS|FAIL|ok|panic)|DATA RACE"
ec=${PIPESTATUS[0]}
git checkout -- "$f"
echo "go test exit=$ec"
exit $ec
