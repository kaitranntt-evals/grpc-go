#!/usr/bin/env bash
# Run: bash verify/repro/c3_repro.sh   (from the root of a grpc-go checkout of the verify branch; needs network access to fetch the claim branch)
# Reproduces C3 on evalon/grpc-go-xd-2ba766a0: tests using newTestListenerWrapperWithRDS never tear down the
# listenerWrapper when they exit before their explicit l.Close().
set -euo pipefail
ROOT="$(git rev-parse --show-toplevel)"
REMOTE_URL="${C3_REMOTE_URL:-https://github.com/kaitranntt-evals/grpc-go-xds-rds-interceptor-lifecycle-leak}"
WT="$(mktemp -d)/c3"
git fetch "$REMOTE_URL" evalon/grpc-go-xd-2ba766a0
git worktree add --detach "$WT" FETCH_HEAD
cd "$WT"

echo "### 1. cleanup part + goexit-before-Close (no production change)"
cp "$ROOT/verify/repro/c3_wrapper_teardown_leak_test.go.txt" internal/xds/server/verify_c3_wrapper_teardown_leak_test.go
go test -race -v -count=1 -run '^Test$/^VerifyC3_' ./internal/xds/server 2>&1 | grep -E 'verify_c3|^---|^\s+---|^ok|FAIL'
rm internal/xds/server/verify_c3_wrapper_teardown_leak_test.go

echo "### 2. the solution's own test, real fatal assertion induced by re-introducing the leak it guards against"
git apply "$ROOT/verify/instrumentation/c3/c3_observer_2ba766a0.patch"
git apply "$ROOT/verify/instrumentation/c3/c3_mutant_reintroduce_leak_2ba766a0.patch"
go test -race -v -count=1 -run '^Test$/^ListenerWrapper_RouteConfigUpdate_ReleasesSupersededResources$' ./internal/xds/server 2>&1 | grep -E 'listener_wrapper_test|^---|^\s+---|^ok|FAIL' || true
echo "(expected: FAIL at verifyCounts, then observer line with interceptorsClosed=0 -> l.Close() never ran)"

cd "$ROOT"
git worktree remove --force "$WT"
