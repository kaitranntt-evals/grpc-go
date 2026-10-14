#!/bin/sh
# Run from a checkout of branch evalon/grpc-go-xd-96f828ed (repo grpc-go-xds-rds-interceptor-lifecycle-leak): sh /path/to/verify/repro/c7_repro.sh /path/to/verify   (C7: peer-address equality passes across forced reconnects and across a drain-on-RDS-update regression)
set -eu
V=${1:?usage: c7_repro.sh /path/to/verify}
T='^Test$/^ServerSideXDS_RouteConfigUpdates_ReleaseSupersededInterceptors$'
echo '== the assertion'
grep -n 'p.Addr.String(), initialPeer.Addr.String()' test/xds/xds_server_rds_update_cleanup_test.go
echo '== test mutation: client transport forcibly closed before every RDS update'
git apply "$V/repro/c7_force_reconnect_mutation.patch"
go test -race -count=1 -v -run "$T" ./test/xds 2>&1 | grep -E 'C7:|^(---|\s+---|ok|FAIL|PASS|panic)' || true
git apply -R "$V/repro/c7_force_reconnect_mutation.patch"
echo '== production mutation: handleRDSUpdate drains every accepted connection; test file untouched'
git apply "$V/repro/c7_production_drain_mutation.patch"
go test -race -count=1 -v -run "$T" ./test/xds 2>&1 | grep -E 'C7MUT|^(---|\s+---|ok|FAIL|PASS|panic)' || true
git apply -R "$V/repro/c7_production_drain_mutation.patch"
