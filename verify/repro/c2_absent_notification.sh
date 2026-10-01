#!/bin/bash
# Run: verify/repro/c2_absent_notification.sh <checkout of the branch under test> [binary timeout=60s]   (C2 bounded-wait part: makes interceptor notifications absent and shows whether the branch's added test/xds tests fail on their own deadline or hang until the go test binary timeout)
set -uo pipefail
cd "$1"; to="${2:-60s}"
f=internal/xds/server/filter_chain_manager.go
base=4ee6ac46fada69c06576cee108b009689a000520
# Mutation (simulated regression): interceptorList.AllowRPC returns nil without invoking any interceptor.
perl -0pi -e 's/(func \(il \*interceptorList\) AllowRPC\(ctx context\.Context\) error \{\n)/$1\treturn nil \/\/ audit mutation: notification absent\n/' $f
grep -n "audit mutation" $f || { echo "mutation not applied"; exit 1; }
names=$(git diff $base HEAD -- 'test/xds/*_test.go' | grep -oE '^\+func \(s\) Test[A-Za-z0-9_]+' | sed 's/.*Test//' | sort -u | paste -sd'|')
start=$(date +%s)
go test -race -count=1 -timeout "$to" -v -run "^Test\$/^($names)\$" ./test/xds 2>&1 | grep -E 'panic: test timed out|running tests:|^\s+Test/.* \([0-9]+s\)|_test\.go:[0-9]+: .*([Tt]imeout|timed out|want)|^\s+--- (PASS|FAIL): Test/|_test\.go:[0-9]+ \+0x|^(ok|FAIL)' | grep -v 'tlogger.go' | sed -E 's/^\s+//' | head -20
echo "elapsed=$(( $(date +%s) - start ))s (binary timeout $to)"
git checkout -q -- $f
