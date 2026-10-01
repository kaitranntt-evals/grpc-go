#!/bin/bash
# Run: verify/repro/c2_unbounded_wait_2a01d623.sh <checkout of evalon/grpc-go-xd-2a01d623>
# Shows that `<-pathCh` in TestServerSideXDS_FilterStateRetention_AcrossRouteUpdates has no deadline:
# when the interceptor notification is absent (interceptors not invoked) the test never fails on its
# own 10s context; it stays blocked until the go test binary timeout kills it.
set -uo pipefail
cd "$1"
f=internal/xds/server/filter_chain_manager.go
cp $f /tmp/c2_2a01d623.orig
# Mutation (simulated regression): interceptorList.AllowRPC returns nil without invoking any interceptor.
perl -0pi -e 's/(func \(il \*interceptorList\) AllowRPC\(ctx context\.Context\) error \{\n)/$1\treturn nil \/\/ audit mutation: notification absent\n/' $f
grep -n "audit mutation" $f
go vet ./internal/xds/server >/dev/null 2>&1
start=$(date +%s)
go test -race -count=1 -timeout 40s -run '^Test$/^ServerSideXDS_FilterStateRetention_AcrossRouteUpdates$' ./test/xds 2>&1 | grep -E "panic: test timed out|running tests:|Test/ServerSideXDS_FilterStateRetention_AcrossRouteUpdates \(|chan receive|xds_server_filter_state_retention_test.go:[0-9]+ \+|^FAIL|^ok" | head -20
echo "elapsed=$(( $(date +%s) - start ))s (test context deadline is 10s; binary timeout 40s)"
cp /tmp/c2_2a01d623.orig $f
