#!/bin/sh
# Run: sh verify/repro/c3_run.sh   (from a grpc-go checkout of the verify branch; needs go and network access to the claim repo)
# C3: ringhash endpointState.balancer on evalon/grpc-go-en-cb3ba169 keeps the first endpointsharding.ChildState snapshot forever.
set -u
BR=evalon/grpc-go-en-cb3ba169
. "$(dirname "$0")/_worktree.sh"
echo "### 1. where the handle is stored / refreshed"
grep -n 'balancer: \+childState\|es\.balancer = \|es\.state = childState\.State' balancer/ringhash/ringhash.go
echo "### 2. probe"
cp "$VERIFY/repro/c3_stale_snapshot_test.go.txt" balancer/ringhash/audit_c3_stale_snapshot_test.go
go test ./balancer/ringhash -run 'Test/AuditC3StaleChildStateSnapshot$' -race -count=1 -v 2>&1 | grep -E 'audit_c3|^(--- |ok|FAIL|PASS)|^\s+--- ' | cut -c1-330
rm balancer/ringhash/audit_c3_stale_snapshot_test.go
echo "### 3. the branch's own ringhash suite (does not notice)"
go test ./balancer/ringhash -race -count=1 2>&1 | tail -1
