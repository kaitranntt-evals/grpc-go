#!/bin/bash
# Run: from a checkout of evalon/grpc-go-xd-7b3f0c09 with the eval fixture copied to test/xds/eval_xds_server_interceptor_leak_test.go: `bash /path/to/verify/repro/c3_run.sh` (applies each mutation patch next to this script, runs the authored tests and the fixture, then reverts).
set -u
HERE="$(cd "$(dirname "$0")" && pwd)"
F='^(=== RUN|---|    ---|FAIL|ok|PASS)|_test.go:[0-9]+: (Created|Destroyed|Got|Closed|After|Timeout)'
run() {
  echo "## authored e2e test"
  go test -race -count=1 -v -run '^Test$/^ServerSideXDS_RouteConfigurationUpdates_ReleaseSupersededInterceptors$' ./test/xds 2>&1 | grep -E "$F"
  echo "## authored unit tests"
  go test -race -count=1 -v -run '^Test$/^UsableRouteConfiguration_' ./internal/xds/server 2>&1 | grep -E "$F"
  echo "## eval fixture"
  go test -race -count=1 -v -run '^Test$/^Eval_ServerSideXDS_InterceptorLeak_RDSUpdate$' ./test/xds 2>&1 | grep -E "$F"
}
echo "#### BASELINE (no mutation)"; run
for m in construction destruction; do
  echo "#### MUTATION: $m"
  git apply "$HERE/c3_mutation_$m.patch" && git diff --stat | cat
  run
  git apply -R "$HERE/c3_mutation_$m.patch"
done
git status --short internal
