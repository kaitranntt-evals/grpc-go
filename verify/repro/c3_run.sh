#!/bin/bash
# Run: verify/repro/c3_run.sh <path to a checkout of branch evalon/grpc-go-xd-8410daeb>   (mutates test files in that checkout; `git checkout -- . && git clean -fd` there afterwards)
set -euo pipefail
WT="$1"; HERE="$(cd "$(dirname "$0")" && pwd)"
FORCE='\tif verifyC3ForceFail {\n\t\tt.Fatalf("verify: forced intermediate terminating assertion")\n\t}'
# Unit tests: force a t.Fatalf right after newListenerWrapperForTesting() acquired the TCP listener.
sed -i "s|^\tfc := l.activeFilterChainManager.filterChains\[0\]\$|&\n\t_ = fc\n$FORCE|" "$WT/internal/xds/server/listener_wrapper_test.go"
# e2e test: force a t.Fatalf after server, client and a first RPC are up, long before the normal-path stopServer().
python3 - "$WT/test/xds/xds_server_filter_state_retention_test.go" <<'PY'
import sys
p=sys.argv[1]; s=open(p).read()
needle='\twaitForPath("path-0")\n'
assert s.count(needle)==1
s=s.replace(needle, needle+'\tif verifyC3ForceFail {\n\t\tt.Fatalf("verify: forced intermediate terminating assertion")\n\t}\n')
open(p,'w').write(s)
PY
sed 's/^package PKG$/package server/' "$HERE/c3_listener_census.go.txt" > "$WT/internal/xds/server/verify_c3_census_test.go"
sed 's/^package PKG$/package xds_test/' "$HERE/c3_listener_census.go.txt" > "$WT/test/xds/verify_c3_census_test.go"
cp "$HERE/c3_unit_test.go.txt" "$WT/internal/xds/server/verify_c3_test.go"
cp "$HERE/c3_e2e_test.go.txt" "$WT/test/xds/verify_c3_test.go"
cd "$WT"
git --no-pager diff --stat
F='verify_c3|^(---|===|ok|FAIL|PASS)|^\s+---|forced intermediate'
go test -v -race -count=1 -run '^TestVerifyC3' ./internal/xds/server 2>&1 | grep -E "$F" || true
go test -v -race -count=1 -run '^TestVerifyC3' ./test/xds 2>&1 | grep -E "$F" || true
