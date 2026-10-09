#!/bin/bash
# Run: verify/repro/c1_run.sh /path/to/eval_tests/tests/eval_xds_server_interceptor_leak_test.go   (from the grpc-go checkout; needs go + network access to the claim repository)
#
# C1 repro. Builds a worktree of the claim branch (evalon/grpc-go-xd-acf71ed1 @ c0650988),
# applies the audit instrumentation (VERIFY_MODE-selected release order + XTRACE
# lifecycle trace), provisions the byte-exact fixture and the two config-only
# variants of its order test, and runs the matrix. Expected: the fixture's
# order test and the variants PASS under repl-mid / shutdown-early while
# XTRACE / STRICT-CHECK show the filter closed before a dependent interceptor.
set -euo pipefail
FIXTURE=${1:?path to the archived eval_xds_server_interceptor_leak_test.go}
HERE=$(cd "$(dirname "$0")" && pwd)
REPO=$(git -C "$HERE" rev-parse --show-toplevel)
WT=${WT:-$(mktemp -d)/claim}
CLAIM_URL=${CLAIM_URL:-https://github.com/kaitranntt-evals/grpc-go-xds-rds-interceptor-lifecycle-leak}
git -C "$REPO" fetch "$CLAIM_URL" evalon/grpc-go-xd-acf71ed1
git -C "$REPO" worktree add --detach "$WT" c0650988
cd "$WT"
git apply "$HERE/c1_instrumentation.patch"
cp "$FIXTURE" test/xds/eval_xds_server_interceptor_leak_test.go
cp "$HERE/c1_order_variants_test.go.txt" test/xds/verify_order_variants_test.go
sha256sum test/xds/eval_xds_server_interceptor_leak_test.go

runone() {
  local mode=$1 t=$2; [ "$mode" = "-" ] && mode=""
  echo "### VERIFY_MODE='$mode' go test -v -run '^Test\$/^$t\$' ./test/xds -race -count=1"
  VERIFY_MODE=$mode go test -v -run "^Test\$/^$t\$" ./test/xds -race -count=1 2>&1 \
    | grep -E '^XTRACE|STRICT-CHECK|dependency order violation|^\s*--- (PASS|FAIL)|^(ok|FAIL|PASS)\b|timed out|no interceptors|not observed' \
    | sed -E 's/^ +[a-z_]+\.go:[0-9]+: //' || true
  echo
}
F=Eval_ServerSideXDS_InterceptorBeforeFilterCloseOrder
R=Verify_OrderVariant_TwoRoutesReplacement
S=Verify_OrderVariant_FilterEnabledAtShutdown
for m in - control repl-mid shutdown-early; do runone $m $F; done
for m in - control repl-mid; do runone $m $R; done
for m in - control shutdown-early; do runone $m $S; done
