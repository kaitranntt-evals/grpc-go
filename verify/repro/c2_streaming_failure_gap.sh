#!/usr/bin/env bash
# C2 repro: run `bash c2_streaming_failure_gap.sh <branch-checkout-dir>` — lists streaming-failure assertions in the branch's changed test files (base commit 0c51461d27177d997e14c642fe18c11668fc09a3).
set -euo pipefail
BASE=0c51461d27177d997e14c642fe18c11668fc09a3
cd "$1"
files=$(git diff --name-only "$BASE" HEAD -- '*_test.go' | grep -v '^gcp/' || true)
echo "changed test files: $files"
for f in $files; do
  echo "== $f: failure-status assertions =="
  grep -n 'ResourceExhausted\|codes\.Internal\|status\.Code' "$f" || echo "(none)"
  echo "== $f: streaming failure assertions (ResourceExhausted/Internal asserted on a stream call) =="
  grep -n -B4 'ResourceExhausted\|codes\.Internal' "$f" | grep -i 'stream\|CloseAndRecv\|Recv()' || echo "(none: only unary failures are asserted)"
done
