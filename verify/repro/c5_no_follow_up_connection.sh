#!/usr/bin/env bash
# Usage: verify/repro/c5_no_follow_up_connection.sh   (branches 0ae111d1 d553852a b7a066a6 7089d1aa; needs FIXTURES_DIR, see setup_worktree.sh)
# C5 repro. Runs each branch's new replacement-during-handshake tests with connection-attempt probes. In every
# scenario there is exactly one ClientHandshake attempt, started BEFORE the replacement; the replacement
# provider's load (load#2) happens inside that same attempt. No attempt is initiated after the replacement.
set -uo pipefail
here=$(cd "$(dirname "$0")" && pwd); p=$here/../probe_attempts.sh
T=internal/xds/balancer/clusterimpl/tests
w() { "$here/setup_worktree.sh" "$1"; }
echo "##### 0ae111d1 $T";            "$p" "$(w 0ae111d1)" $T '^Test$/^SecurityConfigUpdate_DuringHandshake$'
echo "##### 0ae111d1 credentials/xds"; "$p" "$(w 0ae111d1)" credentials/xds '^Test$/^ClientCredsProvider(Closed|Replaced)DuringHandshake$'
echo "##### d553852a $T";            "$p" "$(w d553852a)" $T '^Test$/^SecurityConfigUpdate_ProvidersReplacedDuringHandshake$'
echo "##### d553852a credentials/xds"; "$p" "$(w d553852a)" credentials/xds '^Test$/^ClientCredsProvider(Closed|Replaced)DuringHandshake$'
echo "##### d553852a clusterimpl";   "$p" "$(w d553852a)" internal/xds/balancer/clusterimpl '^Test$/^SecurityConfigUpdate_ProviderLifecycle$'
echo "##### b7a066a6 $T";            "$p" "$(w b7a066a6)" $T '^Test$/^SecurityConfigUpdate_ProviderReplacementDuringHandshake$'
echo "##### b7a066a6 credentials/xds"; "$p" "$(w b7a066a6)" credentials/xds '^Test$/^ClientCredsProviderReplacementDuringHandshake$'
echo "##### 7089d1aa credentials/xds"; "$p" "$(w 7089d1aa)" credentials/xds '^Test$/^ClientCredsProviderSwitchDuringHandshake$'
