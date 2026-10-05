#!/bin/bash
# Run: verify/repro/run_probe.sh <worktree-of-branch-under-test> '<go test -run regex>' [ENV=VAL ...]   e.g. verify/repro/run_probe.sh ~/wt/5401c189 'TestVerifyProbe_C7'
set -u
here=$(cd "$(dirname "$0")" && pwd)
wt=$1; pat=$2; shift 2
cp "$here/verify_probe_test.go" "$wt/internal/transport/zz_verify_probe_test.go"
(cd "$wt" && env "$@" go test -tags verify_probe -v -run "$pat" ./internal/transport -count=1 -timeout 120s 2>&1 | grep -vE "^=== (RUN|PAUSE|CONT)" | sed -E 's/^\s+zz_verify_probe_test.go:[0-9]+: /    /' | cut -c1-600)
rm -f "$wt/internal/transport/zz_verify_probe_test.go"
