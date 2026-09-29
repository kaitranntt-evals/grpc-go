#!/usr/bin/env bash
# Usage (from a branch worktree root): bash <verify>/instrument/run_branch_probes.sh '<clusterimpl-tests regex>' '<credentials/xds regex>' '<clusterimpl pkg regex or empty>' [c2]
# Phase T: trace; Phase M: post-replacement-connection mutant (+ BadToGood sanity); Phase C (c2 only): no-final-close mutant.
set -uo pipefail
V="$(cd "$(dirname "$0")" && pwd)"
E2E="$1"; UNIT="$2"; PKG="${3:-}"; C2="${4:-}"
revert(){ git checkout -q credentials/xds/xds.go internal/credentials/xds/handshake_info.go internal/xds/balancer/clusterimpl/clusterimpl.go; rm -f credentials/xds/verify_mutant.go; }
run(){ go test ./internal/xds/balancer/clusterimpl/tests -run "$E2E" -count=1 -v 2>&1 | grep -E 'VERIFY-|^\s*--- |^(ok|FAIL|panic)|_test.go:[0-9]+:'
  go test ./credentials/xds -run "$UNIT" -count=1 -v 2>&1 | grep -E 'VERIFY-|^\s*--- |^(ok|FAIL|panic)|_test.go:[0-9]+:'
  [ -n "$PKG" ] && go test ./internal/xds/balancer/clusterimpl -run "$PKG" -count=1 -v 2>&1 | grep -E 'VERIFY-|^\s*--- |^(ok|FAIL|panic)|_test.go:[0-9]+:'; true; }
echo "##### $(git rev-parse --abbrev-ref HEAD) $(git rev-parse --short HEAD)"
echo "##### PHASE T: trace"; bash "$V/apply_trace.sh" && run; revert
echo "##### PHASE M: post-replacement connection mutant"; bash "$V/apply_post_replacement_mutant.sh" && { run; echo "## mutant sanity (pre-existing BadToGood must FAIL)"; go test ./internal/xds/balancer/clusterimpl/tests -run 'Test/SecurityConfigUpdate_BadToGood$' -count=1 -v 2>&1 | grep -E 'VERIFY-|^\s*--- |^(ok|FAIL)|_test.go:[0-9]+:' | head -8; }; revert
if [ "$C2" = c2 ]; then echo "##### PHASE C: no cleanup on final release"; bash "$V/apply_no_final_close_mutant.sh" && run; revert; fi
git status --short
