#!/bin/sh
# Run from a checkout of branch evalon/grpc-go-xd-2c0e5501 (repo grpc-go-xds-rds-interceptor-lifecycle-leak): sh /path/to/verify/repro/c6_repro.sh /path/to/verify   (C6: authored lifecycle tests stay green with duplicate interceptor Close)
set -eu
V=${1:?usage: c6_repro.sh /path/to/verify}
AUTH='^Test$/^(FilterChain_RouteConfigUpdatesReleaseSupersededResources|FilterChain_SupersededRouteConfigReleasedAfterInFlightRPC|ServerSideXDS_RouteConfigUpdatesReleaseSupersededInterceptors)$'
F='^(---|\s+---|ok|FAIL|PASS|panic)|_test.go:[0-9]+: Destroyed'
echo '== baseline, authored tests'
go test -race -count=1 -v -run "$AUTH" ./internal/xds/server ./test/xds 2>&1 | grep -E "$F"
echo '== mutation A (every interceptor Close issued twice, replacement + shutdown), authored tests'
git apply "$V/repro/c6_double_close_mutation.patch"
go test -race -count=1 -v -run "$AUTH" ./internal/xds/server ./test/xds 2>&1 | grep -E "$F" || true
echo '== mutation A, pre-existing (not authored) retention tests'
go test -race -count=1 -v -run '^Test$/^ServerSideXDS_FilterStateRetention' ./test/xds 2>&1 | grep -E "$F" || true
git apply -R "$V/repro/c6_double_close_mutation.patch"
echo '== mutation B (duplicate Close only when an RDS update supersedes a config), full packages + duplicate-close observer'
git apply "$V/repro/c6_double_close_rds_replacement_mutation.patch"
go test -race -count=1 -timeout=5m ./internal/xds/server/... ./test/xds 2>&1 | tail -3 || true
git apply "$V/instrumentation/c6_duplicate_close_observer.patch"
go test -race -count=1 -v -run '^Test$/^ServerSideXDS_RouteConfigUpdatesReleaseSupersededInterceptors$' ./test/xds 2>&1 |
  grep -E "C6OBS|$F" | awk '/C6OBS/{n++; if(n<=2)print; next}{print} END{print "total C6OBS duplicate-close lines:", n+0}'
git apply -R "$V/instrumentation/c6_duplicate_close_observer.patch"
git apply -R "$V/repro/c6_double_close_rds_replacement_mutation.patch"
