#!/usr/bin/env bash
# Run: from a checkout of the target branch (see MUTANT/BRANCH table below):  bash verify/repro/c3_run_mutants.sh <mutant.diff>
# Applies one C3 mutant diff to the production code, runs the branch's own new
# tests (they should stay GREEN although the later connection never proves it
# used the replacement roots), and then reverts the mutant.
#
#   branch evalon/grpc-go-xd-70cc0d6c : c3_70cc0d6c_mutantA_stale_config.diff, c3_70cc0d6c_mutantB_nontrust_failure.diff
#   branch evalon/grpc-go-xd-833acc28 : c3_833acc28_mutantB_nontrust_failure.diff, c3_833acc28_mutantC_prefetch_plus_stale.diff
#
# Note: the mutants use a process-global sticky pointer, so run each test with
# -count=1 (a second in-process run would reuse state from the first).
set -euo pipefail
diff_file="$1"
git apply "$diff_file"
trap 'git checkout -- credentials/xds/xds.go internal/xds/balancer/clusterimpl/clusterimpl.go' EXIT
case "$diff_file" in
  *70cc0d6c*)
    go test ./credentials/xds -run '^Test$/^ClientCredsHandshakeInfoReplacedDuringHandshake$' -count=1 -v 2>&1 | grep -E '^(--- |ok|FAIL|VERIFY|\s+xds_client)' || true
    go test ./test/xds -run '^Test$/^ClientSideXDS_SecurityConfigReplacedDuringHandshake$' -count=1 -v 2>&1 | grep -E '^(--- |ok|FAIL|VERIFY|\s+xds_client)' || true
    ;;
  *833acc28*)
    go test ./credentials/xds -run '^Test$/^ClientCredsProviderReplacedDuringHandshake$' -count=1 -v 2>&1 | grep -E '^(--- |ok|FAIL|VERIFY|\s+xds_client)' || true
    go test ./internal/xds/balancer/clusterimpl/tests -run '^Test$/^SecurityConfigUpdate_ReplacedDuringHandshake$' -count=1 -v 2>&1 | grep -E '^(--- |ok|FAIL|VERIFY|\s+clusterimpl_sec)' || true
    ;;
esac
# Contrast: the eval fixture ties the later connection to the replacement roots and FAILS under the same mutant.
if [ -f internal/xds/balancer/clusterimpl/tests/eval_handshake_lifetime_test.go ]; then
  go test ./internal/xds/balancer/clusterimpl/tests -run '^TestEval_SecurityConfigUpdate_ActiveHandshakeKeepsProvider$' -count=1 -v 2>&1 | grep -E '^(--- |ok|FAIL|VERIFY|\s+eval_)' || true
fi
