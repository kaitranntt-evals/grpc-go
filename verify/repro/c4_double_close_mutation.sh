#!/bin/bash
# Run from the root of a checkout of branch evalon/grpc-go-xd-e02f4bfe: bash c4_double_close_mutation.sh
# Mutates production code so every superseded route configuration's interceptors are closed TWICE,
# then runs the branch's own lifecycle tests. Restores the file afterwards.
set -u
f=internal/xds/server/filter_chain_manager.go
cp "$f" /tmp/c4_fcm.orig
python3 - "$f" <<'PY'
import sys
p=sys.argv[1]; s=open(p).read()
old="""	if old := fc.usableRouteConfiguration.Swap(urc); old != nil {
		old.close()
	}"""
assert s.count(old)==1
s=s.replace(old,"""	if old := fc.usableRouteConfiguration.Swap(urc); old != nil {
		old.close()
		old.close() // MUTATION: duplicate close of superseded interceptors
	}""")
open(p,"w").write(s)
PY
git --no-pager diff -- "$f"
echo "### unit: internal/xds/server TestRDSUpdate_ReleasesSupersededInterceptors (uses lifecycleTrackingInterceptor)"
go test -race -v -count=1 -run '^Test$/^RDSUpdate_ReleasesSupersededInterceptors$' ./internal/xds/server 2>&1 | grep -E '^\s*(--- |PASS|FAIL|ok )|listener_wrapper_test.go'
echo "### e2e: test/xds tests in xds_server_rds_update_cleanup_test.go (use trackingInterceptor, which does not dedupe)"
go test -race -v -count=1 -run "^Test\$/^($(grep -oE '^func \(s\) Test[A-Za-z0-9_]+' test/xds/xds_server_rds_update_cleanup_test.go | sed 's/func (s) Test//' | paste -sd'|'))\$" ./test/xds 2>&1 | grep -E '^\s*(--- |PASS|FAIL|ok )|xds_server_rds_update_cleanup_test.go:[0-9]+: [A-Z]' | cut -c1-300
cp /tmp/c4_fcm.orig "$f"
git status --short -- "$f"
