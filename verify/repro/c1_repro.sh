#!/bin/bash
# Run from the repo root: bash verify/repro/c1_repro.sh   (C1: countingInterceptor.Close hides duplicate cleanup; needs network access to github.com)
set -uo pipefail
REPRO="$(cd "$(dirname "$0")" && pwd)"
WT="$(mktemp -d)/c1"
git fetch -q "${CLAIM_REPO:-https://github.com/kaitranntt-evals/grpc-go-xds-rds-interceptor-lifecycle-leak}" evalon/grpc-go-xd-8e055bd7
git worktree add -q --detach "$WT" FETCH_HEAD
cd "$WT"

echo "### 1. Close() twice on each tracker"
cp "$REPRO/c1_counting_interceptor_double_close_test.go.txt" internal/xds/server/verify_c1_double_close_test.go
cp "$REPRO/c1_tracking_interceptor_double_close_test.go.txt" test/xds/verify_c1_double_close_test.go
go test -race -count=1 -v -run 'TestVerifyC1_' ./internal/xds/server ./test/xds
rm internal/xds/server/verify_c1_double_close_test.go test/xds/verify_c1_double_close_test.go

echo "### 2. Production mutated to close every interceptor twice; branch lifecycle tests"
git apply "$REPRO/c1_mutation_double_close.patch"
go test -race -count=1 -v -run '^Test$/^(ListenerWrapper_R|ServerSideXDS_FilterStateRetention)' ./internal/xds/server ./test/xds 2>&1 | grep -E '^\s*(--- |ok|FAIL|PASS|\S+_test.go)'
git checkout -q -- .

echo "### 3. Production mutated to double-close only on the RDS-error path; full branch suites"
git apply "$REPRO/c1_mutation_error_path_double_close.patch"
go test -race -count=1 -timeout=5m ./internal/xds/server/... ./test/xds
git checkout -q -- .
