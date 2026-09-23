#!/bin/bash
# C9 repro: cd <worktree of evalon/grpc-go-en-417e24dc> && bash verify/repro/c9_coverage_check.sh   (lists the tests the branch adds, greps their assertions for the three claimed coverage areas, then runs the added tests once)
set -u
cd "$(git rev-parse --show-toplevel)"
BASE=bf9e7cd3
echo "== tests added by the branch (vs merge base $BASE):"
git diff "$BASE" -- 'balancer/endpointsharding/*_test.go' | grep '^+func\|^+++ '
echo "== assertion lines added by the branch:"
git diff "$BASE" -- 'balancer/endpointsharding/*_test.go' | grep '^+.*\(t\.Fatal\|t\.Error\)'
echo "== claimed coverage areas (expect no matches):"
grep -rn 'TestSameChildMutualExclusion\|TestClosedStateGuardCoverage\|TestBatchUpdateChildErrorConsolidation' balancer/ ; echo "grep exit=$? (1 = no matches)"
echo "== demonstration run of the added tests:"
go test ./balancer/endpointsharding -run 'Test/EndpointSharding(ExitIdleWhileChildUpdateBlocked|SynchronousChildStateUpdates|SingleUpdateForMultipleEndpoints)$' -race -count=1 -v 2>&1 | grep -v BalancerClientConn
