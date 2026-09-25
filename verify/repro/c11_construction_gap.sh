#!/usr/bin/env bash
# Run: verify/repro/c11_construction_gap.sh   (C11 on 072542f0: queued construction-time ExitIdle with/without a widened construction->configuration gap)
source "$(dirname "$0")/lib.sh"
wt=$(mkwt_branch 072542f0); cd "$wt"
git apply "$REPRO_DIR/c11-gap-sleep.diff"
addgo "$REPRO_DIR/c11_audit_test.go" balancer/endpointsharding/audit_c11_test.go
for gap in 1 0; do echo "== ES_GAP_SLEEP=$gap"
  ES_GAP_SLEEP=$gap go test -race -count=3 -v -run '^TestAuditC11' ./balancer/endpointsharding/ 2>&1 | grep -E 'audit_c11_test.go|--- (PASS|FAIL)|^(ok|FAIL)' || true
done
