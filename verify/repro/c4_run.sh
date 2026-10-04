#!/bin/sh
# Run: sh verify/repro/c4_run.sh   (from a grpc-go checkout of the verify branch; needs go and network access to the claim repo)
# C4: on evalon/grpc-go-en-c60230da child UpdateState callbacks during/after endpointSharding.Close still notify the parent.
set -u
BR=evalon/grpc-go-en-c60230da
. "$(dirname "$0")/_worktree.sh"
echo "### 1. Close on the claim branch"
sed -n '/^func (es \*endpointSharding) Close/,/^}/p' balancer/endpointsharding/endpointsharding.go
echo "### 2. probe on the claim branch"
cp "$VERIFY/repro/c4_close_publication_test.go.txt" balancer/endpointsharding/audit_c4_close_publication_test.go
go test ./balancer/endpointsharding -run 'Test/AuditC4' -race -count=1 -v 2>&1 | grep -E 'audit_c4|^(--- |ok|FAIL|PASS)|^\s+--- ' | cut -c1-260
rm balancer/endpointsharding/audit_c4_close_publication_test.go
echo "### 3. the branch's own endpointsharding suite (does not notice)"
go test ./balancer/endpointsharding -race -count=1 2>&1 | tail -1
