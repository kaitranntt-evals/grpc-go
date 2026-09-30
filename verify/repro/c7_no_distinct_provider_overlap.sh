#!/usr/bin/env bash
# Usage: verify/repro/c7_no_distinct_provider_overlap.sh   (branch c9d55e2d; needs FIXTURES_DIR, see setup_worktree.sh)
# C7 repro. Runs the three new tests on c9d55e2d with store/attempt/load probes. The only test that overlaps
# an active validation-root load with replacement (TestClientCredsProviderReplacementDuringHandshake) replaces
# with the SAME cache entry ("store Build REUSES entry ... refCount now 2") and the replacement handle keeps
# the entry alive; the distinct-provider test (TestSecurityConfigUnchanged_ProvidersNotRebuilt) performs no
# handshake/load at all; the store test performs no client load. Then runs the archived hidden fixture
# (distinct provider, no retaining handle): it FAILs on this branch, so the uncovered case is a live failure.
set -uo pipefail
here=$(cd "$(dirname "$0")" && pwd); p=$here/../probe_attempts.sh
wt=$("$here/setup_worktree.sh" c9d55e2d) || exit 1
export VERIFY_STORE=1
echo "##### credentials/xds TestClientCredsProviderReplacementDuringHandshake"; "$p" "$wt" credentials/xds '^Test$/^ClientCredsProviderReplacementDuringHandshake$'
echo "##### clusterimpl TestSecurityConfigUnchanged_ProvidersNotRebuilt"; "$p" "$wt" internal/xds/balancer/clusterimpl '^Test$/^SecurityConfigUnchanged_ProvidersNotRebuilt$'
echo "##### certprovider TestStoreProviderReplacement"; "$p" "$wt" credentials/tls/certprovider '^Test$/^StoreProviderReplacement$'
echo "##### archived hidden fixture (distinct provider A -> B)"; "$here/../probe_fixture.sh" "$wt" 2>&1 | sed 's/VERIFY-PROBE //'
