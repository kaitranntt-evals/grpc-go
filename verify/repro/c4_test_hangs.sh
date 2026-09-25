#!/usr/bin/env bash
# Run: verify/repro/c4_test_hangs.sh [commit, default 81201fc0]   (C4: stalled setup / stalled idle-exit worker vs TestSameChildMutualExclusion and TestDecoupledChildProgress)
source "$(dirname "$0")/lib.sh"
wt=$(mkwt_ref "${1:-81201fc085e37de0ffb23505ac7831350b326c33}"); cd "$wt"
git apply "$REPRO_DIR/c4-instr.diff"; addgo "$REPRO_DIR/c4_zz_audit.go" balancer/endpointsharding/zz_audit.go
run() { local env=$1 test=$2 log; log=$(mktemp)
  env "$env=1" timeout 90 go test -race -count=1 -timeout 30s -v -run "^Test\$/^$test\$" ./balancer/endpointsharding/ >"$log" 2>&1 && rc=0 || rc=$?; echo "== $env $test rc=$rc"
  grep -E 'AUDIT|endpointsharding_test.go:[0-9]+:|panic: test timed out|^\s+/.*endpointsharding(_test)?\.go:[0-9]+' "$log" | head -8 || true; }
go test -race -count=1 -run '^Test$/^(SameChildMutualExclusion|DecoupledChildProgress)$' ./balancer/endpointsharding/
run ES_SETUP_DEADLOCK SameChildMutualExclusion
run ES_SETUP_DEADLOCK DecoupledChildProgress
run ES_EXITIDLE_STALL_AFTER SameChildMutualExclusion
run ES_EXITIDLE_STALL_BEFORE SameChildMutualExclusion
run ES_EXITIDLE_STALL_BEFORE DecoupledChildProgress
