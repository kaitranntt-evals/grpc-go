#!/bin/bash
# Run: verify/repro/c6_run.sh <worktree of evalon/grpc-go-xd-1463306c>   (applies the mutation, runs the branch's added tests + packages, reverts)
set -u
here=$(cd "$(dirname "$0")" && pwd)
cd "$1" || exit 2
filter='tlogger.go|^=== (RUN|PAUSE|CONT)'
echo '### added/changed test functions (git diff 4ee6ac46 HEAD -- "*_test.go")'
git diff 4ee6ac46 HEAD -- '*_test.go' | grep -E '^\+func \(s\) Test'
echo '### references to the production handler / watcher callbacks in added test lines'
git diff 4ee6ac46 HEAD -- '*_test.go' | grep -nE '^\+.*(handleRDSUpdate|ResourceError|ResourceChanged|AmbientError|rdsWatcherUpdate)' || echo '(none)'
echo '### error-state coverage in the added tests'
git diff 4ee6ac46 HEAD -- '*_test.go' | grep -nE '^\+.*(wantErr|\.err\b)'
added='Test/(UsableRouteConfiguration_InterceptorLifecycle|ServerSideXDS_FilterStateRetention_InPlaceRDSUpdate)$'
run() { # label
  echo "### [$1] added tests"
  go test -race -count=1 -v -run "$added" ./internal/xds/server/ ./test/xds/ 2>&1 | grep -Ev "$filter" | grep -E '^(---|    ---|ok|FAIL|PASS|panic)'
  echo "### [$1] feasibility probe (not part of the branch)"
  cp "$here/c6_handle_rds_error_after_success_probe_test.go" internal/xds/server/zz_verify_c6_probe_test.go
  go test -race -count=1 -v -run 'Test/VerifyC6' ./internal/xds/server/ 2>&1 | grep -Ev "$filter" | grep -E 'PROBE|after error|^(---|    ---|ok|FAIL|PASS)' | sed 's/^ *zz_verify_c6_probe_test.go:[0-9]*: //'
  rm internal/xds/server/zz_verify_c6_probe_test.go
}
run unmodified
git apply "$here/c6_mutation_handleRDSUpdate_ignore_error.patch" && echo '### mutation applied:' && git diff --stat
run MUTATED
echo '### [MUTATED] full packages (pre-existing tests included)'
go test -race -count=1 ./internal/xds/server/... ./test/xds/... 2>&1 | grep -Ev "$filter" | grep -E '^(---|    ---|ok|FAIL|PASS|panic)'
git apply -R "$here/c6_mutation_handleRDSUpdate_ignore_error.patch"; echo "### reverted; git status --short:"; git status --short
