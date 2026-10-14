#!/bin/bash
# Run: verify/repro/c2_unclosed_listener.sh <path-to-worktree-of-evalon/grpc-go-xd-bd84b6f8>   (leaves the worktree clean afterwards)
set -uo pipefail
WT=${1:?worktree of evalon/grpc-go-xd-bd84b6f8}
HERE=$(cd "$(dirname "$0")" && pwd)
T=internal/xds/server/listener_wrapper_test.go
RUN='^Test$/^(HandleRDSUpdate_ReleasesSupersededRouteConfiguration|ZZZ_C2ListenerProbe)$'
cd "$WT"
cp "$HERE/c2_listener_probe_test.go.txt" internal/xds/server/zz_c2_listener_probe_test.go
echo "===== A: unmodified test (passes, reaches lw.Close()) ====="
go test -count=1 -v -run "$RUN" ./internal/xds/server 2>&1 | grep -E 'C2-PROBE|^(=== RUN|--- |\s+--- |FAIL|ok|PASS)|listener_wrapper_test'
echo "===== B: first post-listener assertion forced to fail (built: 2 -> built: 3) ====="
sed -i 's/checkCounts(t, counts{built: 2, closed: 0, filtersClosed: 0})/checkCounts(t, counts{built: 3, closed: 0, filtersClosed: 0})/' $T
git diff --stat -- $T | cat
go test -count=1 -v -run "$RUN" ./internal/xds/server 2>&1 | grep -E 'C2-PROBE|^(=== RUN|--- |\s+--- |FAIL|ok|PASS)|listener_wrapper_test'
git checkout -- $T; rm internal/xds/server/zz_c2_listener_probe_test.go; git status --short
