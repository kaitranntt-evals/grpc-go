#!/bin/bash
# Run: verify/repro/c2_sched_probe.sh <checkout of the branch under test> [close|construct] [delay=150ms] [count=1]   (C2: runs the branch's added tests unperturbed, then with one env-controlled sleep)
# close:     sleep at the top of interceptorList.Close           (retirement of an interceptor finishes later)
# construct: sleep at the top of constructUsableRouteConfiguration (each filter chain is updated later)
set -uo pipefail
here="$(cd "$(dirname "$0")" && pwd)"
wt="$(cd "$1" && pwd)"; mode="${2:-close}"; d="${3:-150ms}"; n="${4:-1}"
"$here/../instrumentation/apply_sched_probes.sh" "$wt" || exit 1
cd "$wt"
base=4ee6ac46fada69c06576cee108b009689a000520
names=$(git diff $base HEAD -- '*_test.go' | grep -oE '^\+func \(s\) Test[A-Za-z0-9_]+' | sed 's/.*Test//' | sort -u | paste -sd'|')
re="^Test\$/^($names)\$"
echo "added tests: $names"
run() { # label, env assignment
  echo "--- $1"
  env $2 go test -race -count=$n -timeout 300s -v -run "$re" ./internal/xds/server ./test/xds 2>&1 \
    | grep -E '^\s+--- (PASS|FAIL): Test/|_test\.go:[0-9]+: .*(want|Timeout|timeout)|^(ok|FAIL|panic)' | grep -v 'tlogger.go' | sed -E 's/^\s+//'
}
run "unperturbed" "VERIFY_NOOP=1"
if [ "$mode" = close ]; then run "VERIFY_CLOSE_DELAY=$d" "VERIFY_CLOSE_DELAY=$d"; else run "VERIFY_CONSTRUCT_DELAY=$d" "VERIFY_CONSTRUCT_DELAY=$d"; fi
