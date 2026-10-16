#!/bin/bash
# Run from the repo root: bash verify/repro/c2_repro.sh   (C2: unit lifecycle tests exit with live fake filters/interceptors; needs network access to github.com)
set -uo pipefail
REPRO="$(cd "$(dirname "$0")" && pwd)"
WT="$(mktemp -d)/c2"
git fetch -q "${CLAIM_REPO:-https://github.com/kaitranntt-evals/grpc-go-xds-rds-interceptor-lifecycle-leak}" evalon/grpc-go-xd-bc3c04de
git worktree add -q --detach "$WT" FETCH_HEAD
cd "$WT"
# Adds a t.Cleanup to each lifecycle test that logs the live-resource state after the test body and its defers ran.
git apply "$REPRO/c2_exit_state_instrumentation.patch"
F='^\s*(--- |ok|FAIL|PASS|\S+_test.go|panic)'
UNIT='^Test$/^UsableRouteConfiguration_'
E2E='^Test$/^ServerSideXDS_FilterStateRetention_RepeatedRDSUpdatesReleaseInterceptors'

echo "### 1. Normal exit, production untouched"
go test -race -count=1 -v -run "$UNIT" ./internal/xds/server 2>&1 | grep -E "$F"
go test -race -count=1 -v -run "$E2E" ./test/xds 2>&1 | grep -E "$F"

echo "### 2. Fatal exit, e2e test (injected t.Fatalf after the update loop)"
VERIFY_FORCE_FATAL=1 go test -race -count=1 -v -run "$E2E" ./test/xds 2>&1 | grep -E "$F"

echo "### 3. Fatal exit, unit tests, mutation M1 (superseded configuration never retired)"
git apply "$REPRO/c2_mutation_m1_no_retire.patch"
go test -race -count=1 -v -run "$UNIT" ./internal/xds/server 2>&1 | grep -E "$F"
git apply -R "$REPRO/c2_mutation_m1_no_retire.patch"

echo "### 4. Fatal exit, unit tests, mutation M2 (retire closes immediately, ignoring in-flight references)"
git apply "$REPRO/c2_mutation_m2_early_close.patch"
go test -race -count=1 -v -run "$UNIT" ./internal/xds/server 2>&1 | grep -E "$F"
git apply -R "$REPRO/c2_mutation_m2_early_close.patch"
