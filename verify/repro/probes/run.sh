#!/bin/bash
# Run: verify/repro/probes/run.sh <worktree> <TestRegexp> [ENV=VAL ...]   e.g. run.sh ~/wt/136a5793 '^TestProbeC3_' 
set -u
wt=$1; re=$2; shift 2; here=$(cd "$(dirname "$0")" && pwd); cd "$wt" || exit 1
cp "$here/${PROBE_FILE:-zz_probe_test.go.txt}" internal/transport/zz_probe_test.go
env "$@" go test google.golang.org/grpc/internal/transport -count=1 -v -run "$re" 2>&1 | grep -vE '^=== |^\s*$'
rm -f internal/transport/zz_probe_test.go; git status --short
