#!/usr/bin/env bash
# Run: bash verify/repro/c3_repro.sh   (needs git + go; checks out the C3 claim branch into a throwaway worktree, calls Close twice on each tracker, then makes production code close superseded configurations twice and runs the branch's two regressions)
branch=evalon/grpc-go-xd-12356fc9
set -uo pipefail
here=$(cd "$(dirname "$0")" && pwd)
repo=${CLAIMS_REPO_URL:-https://github.com/kaitranntt-evals/grpc-go-xds-rds-interceptor-lifecycle-leak}
wt=$(mktemp -d)/wt
git -C "$here" fetch -q "$repo" "$branch" && git -C "$here" worktree add -q --detach "$wt" FETCH_HEAD || exit 1
trap 'git -C "$here" worktree remove --force "$wt"' EXIT
cd "$wt" && echo "worktree: $wt @ $(git rev-parse HEAD)"
filter='VERIFY-C3|_test.go:[0-9]+: (After|Destroyed|Created)|^\s*--- |^(ok|FAIL|PASS)'
cp "$here/testdata/c3_double_close_unit_probe_test.go" internal/xds/server/
cp "$here/testdata/c3_double_close_e2e_probe_test.go" test/xds/
echo "=== double Close() on closeTrackingInterceptor (unit regression's tracker)"
go test -race -count=1 -v -run '^Test$/^VerifyC3_' ./internal/xds/server 2>&1 | grep -E "$filter" | cut -c1-600
echo "=== double Close() on trackingInterceptor (end-to-end regression's tracker)"
go test -race -count=1 -v -run '^Test$/^VerifyC3_' ./test/xds 2>&1 | grep -E "$filter" | cut -c1-600
git apply "$here/testdata/c3_mutation_double_close.patch" || exit 1
echo "=== production mutation (superseded configuration closed twice): unit regression"
go test -race -count=1 -v -run '^Test$/^FilterChain_UpdateUsableRouteConfiguration_ReleasesSupersededResources$' ./internal/xds/server 2>&1 | grep -E "$filter" | cut -c1-600
echo "=== production mutation (superseded configuration closed twice): end-to-end regression"
go test -race -count=1 -v -run '^Test$/^ServerSideXDS_RouteConfigurationUpdates_ReleaseSupersededResources$' ./test/xds 2>&1 | grep -E "$filter" | cut -c1-600
