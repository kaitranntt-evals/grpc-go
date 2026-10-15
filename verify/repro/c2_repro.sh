#!/usr/bin/env bash
# Run: bash verify/repro/c2_repro.sh   (needs git + go; checks out the C2 claim branch into a throwaway worktree, applies the test-only instrumentation patch, and probes listener/filter state after the regression exits with and without a fatal assertion)
branch=evalon/grpc-go-xd-9207949e
set -uo pipefail
here=$(cd "$(dirname "$0")" && pwd)
repo=${CLAIMS_REPO_URL:-https://github.com/kaitranntt-evals/grpc-go-xds-rds-interceptor-lifecycle-leak}
wt=$(mktemp -d)/wt
git -C "$here" fetch -q "$repo" "$branch" && git -C "$here" worktree add -q --detach "$wt" FETCH_HEAD || exit 1
trap 'git -C "$here" worktree remove --force "$wt"' EXIT
cd "$wt" && echo "worktree: $wt @ $(git rev-parse HEAD)"
filter='VERIFY-C2|listener_wrapper_test.go|xds_server_filter_state_retention_test.go|^\s*--- |^(ok|FAIL|PASS)'
git apply "$here/testdata/c2_test_instrumentation.patch" || exit 1
cp "$here/testdata/c2_fatal_cleanup_probe_test.go" internal/xds/server/
echo "=== control: regression passes"
go test -race -count=1 -v -run '^Test$/^VerifyC2_' ./internal/xds/server 2>&1 | grep -E "$filter" | cut -c1-600
echo "=== injected t.Fatalf after the first update"
VERIFY_C2_INJECT_FATAL=1 go test -race -count=1 -v -run '^Test$/^VerifyC2_' ./internal/xds/server 2>&1 | grep -E "$filter" | cut -c1-600
git apply "$here/testdata/c2_mutation_reintroduce_leak.patch" || exit 1
echo "=== production mutation (leak reintroduced): the regression's own t.Fatalf fires"
go test -race -count=1 -v -run '^Test$/^VerifyC2_' ./internal/xds/server 2>&1 | grep -E "$filter" | cut -c1-600
echo "=== same mutation, end-to-end regression (uses defer stopServer())"
go test -race -count=1 -v -run '^Test$/^ServerSideXDS_FilterStateRetention_AcrossRDSUpdates$' ./test/xds 2>&1 | grep -E "$filter" | cut -c1-600
