#!/usr/bin/env bash
# Run: verify/repro/c10_lifecycle.sh [branch-suffix, default a2a8173a]   (C10: record parent-facing publications from parent ExitIdle and Close)
source "$(dirname "$0")/lib.sh"
wt=$(mkwt_branch "${1:-a2a8173a}"); cd "$wt"
addgo "$REPRO_DIR/c10_audit_test.go" balancer/endpointsharding/audit_c10_test.go
go test -race -count=1 -v -run '^TestAuditC10' ./balancer/endpointsharding/ 2>&1 | grep -E 'audit_c10_test.go|--- (PASS|FAIL)|^(ok|FAIL)' || true
