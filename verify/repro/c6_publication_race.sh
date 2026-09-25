#!/usr/bin/env bash
# Run: verify/repro/c6_publication_race.sh   (C6 on cc0c22d2: pause a child callback after the inhibit check, run a batch, observe a partial publication)
source "$(dirname "$0")/lib.sh"
wt=$(mkwt_branch cc0c22d2); cd "$wt"
git apply "$REPRO_DIR/c6-hook.diff"
addgo "$REPRO_DIR/c6_zz_audit_hook.go" balancer/endpointsharding/zz_audit_hook.go
addgo "$REPRO_DIR/c6_audit_test.go" balancer/endpointsharding/audit_c6_test.go
go test -race -count=3 -v -run '^TestAuditC6' ./balancer/endpointsharding/ 2>&1 | grep -E 'audit_c6_test.go|--- (PASS|FAIL)|^(ok|FAIL)' || true
