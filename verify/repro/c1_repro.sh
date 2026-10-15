#!/usr/bin/env bash
# Run: bash verify/repro/c1_repro.sh   (needs git + go; checks out the C1 claim branch into a throwaway worktree, copies the two trace tests from verify/repro/testdata/ and runs them; the FAIL lines it prints are the finding)
branch=evalon/grpc-go-xd-e43b07e4
set -uo pipefail
here=$(cd "$(dirname "$0")" && pwd)
repo=${CLAIMS_REPO_URL:-https://github.com/kaitranntt-evals/grpc-go-xds-rds-interceptor-lifecycle-leak}
wt=$(mktemp -d)/wt
git -C "$here" fetch -q "$repo" "$branch" && git -C "$here" worktree add -q --detach "$wt" FETCH_HEAD || exit 1
trap 'git -C "$here" worktree remove --force "$wt"' EXIT
cd "$wt" && echo "worktree: $wt @ $(git rev-parse HEAD)"
filter='VERIFY-C1|^\s*--- |^(ok|FAIL|PASS)'
cp "$here/testdata/c1_cleanup_order_trace_test.go" internal/xds/server/
cp "$here/testdata/c1_e2e_trace_test.go" test/xds/
echo "=== internal/xds/server: RDS no-data error after success, and listener shutdown"
go test -race -count=1 -v -run '^Test$/^VerifyC1_' ./internal/xds/server 2>&1 | grep -E "$filter" | cut -c1-600
echo "=== test/xds: live xDS server, RDS update that disables the filter, and shutdown with a parked retired reader"
go test -race -count=1 -v -run '^Test$/^VerifyC1E2E_' ./test/xds 2>&1 | grep -E "$filter" | cut -c1-600
