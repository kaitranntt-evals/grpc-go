#!/usr/bin/env bash
# Run: verify/repro/c1_coarse_lock.sh   (C1: 2c1919e2 regression under a coarse shared child mutex; expect the progress assertion to FAIL every run)
source "$(dirname "$0")/lib.sh"
wt=$(mkwt_branch 2c1919e2); cd "$wt"
git apply "$REPRO_DIR/c1-coarse.diff"; addgo "$REPRO_DIR/c1_zz_coarse.go" balancer/endpointsharding/zz_coarse.go
go test -race -count=8 -timeout 300s -v -run '^Test$/^ChildExitIdleDuringOtherChildUpdate$' ./balancer/endpointsharding/ 2>&1 \
  | grep -E -- '--- (PASS|FAIL): Test/|_test.go:[0-9]+:|^(ok|FAIL)' | sort | uniq -c || true
