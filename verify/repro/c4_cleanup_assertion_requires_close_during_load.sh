#!/usr/bin/env bash
# Usage: verify/repro/c4_cleanup_assertion_requires_close_during_load.sh   (branch b7a066a6; needs FIXTURES_DIR, see setup_worktree.sh)
# C4 repro. The only new/changed test on b7a066a6 that observes Close of a replaced provider is
# TestSecurityConfigUpdate_ProviderReplacementDuringHandshake. (1) As written: the balancer closes the replaced
# root provider while load#1 is still running on it, and the test passes. (2) Against the --defer-close
# reference mutant (provider closed only after the load running on it returned, i.e. cleanup after the final
# dependent load): the test FAILS with "Timeout waiting for the old certificate providers to be closed".
set -uo pipefail
here=$(cd "$(dirname "$0")" && pwd); v=$here/..
wt=$("$here/setup_worktree.sh" b7a066a6) || exit 1
sel='^Test$/^SecurityConfigUpdate_ProviderReplacementDuringHandshake$'
run() { (cd "$wt" && export GOENV=off GOWORK=off && python3 "$v/instrument.py" . $1 >/dev/null && { go test ./internal/xds/balancer/clusterimpl/tests -run "$sel" -count=1 -v -timeout 60s 2>&1 | grep -E 'VERIFY-PROBE|--- (PASS|FAIL): Test/|_test.go:[0-9]+: (Timeout|EmptyCall)' | sed 's/VERIFY-PROBE //'; git checkout -q -- internal/credentials/xds/handshake_info.go internal/xds/balancer/clusterimpl/clusterimpl.go; }); }
echo "##### as written";            run ""
echo "##### --defer-close mutant";  run --defer-close
echo "##### --no-close mutant";     run --no-close
