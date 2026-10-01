#!/bin/bash
# Run: verify/repro/run_eval_fixture.sh <worktree> <path to extracted tests/eval_xds_server_interceptor_leak_test.go>
# Provisions the archived eval fixture byte-exact at test/xds/, runs its five checks with -race, removes it again.
set -u
cd "$1" || exit 2
cp "$2" test/xds/eval_xds_server_interceptor_leak_test.go
echo "### fixture sha256: $(sha256sum < test/xds/eval_xds_server_interceptor_leak_test.go | cut -d' ' -f1)   branch HEAD: $(git log --oneline -1 | cut -c1-70)"
echo "### go test -v -run '^Test\$/^Eval_' ./test/xds -race -count=1"
go test -v -run '^Test$/^Eval_' ./test/xds -race -count=1 2>&1 | grep -Ev 'tlogger.go|^=== |logging.go' | grep -E '_test.go:[0-9]+: |^(---|    ---|ok|FAIL|PASS|panic)' | cut -c1-400
rm test/xds/eval_xds_server_interceptor_leak_test.go
echo "### removed; git status --short: [$(git status --short | grep -v '^?? verify/')]"
