#!/usr/bin/env bash
# Run from a worktree of evalon/grpc-go-xd-{8069941c,e709f9c1,48dd6ba3}: bash <path-to>/verify/repro/c3_post_replacement_roots_untested.sh  (trace shows one connection per test; new tests still PASS when every post-replacement connection is refused)
set -uo pipefail
V="$(cd "$(dirname "$0")/.." && pwd)"
revert(){ git checkout -q credentials/xds/xds.go internal/credentials/xds/handshake_info.go internal/xds/balancer/clusterimpl/clusterimpl.go; rm -f credentials/xds/verify_mutant.go credentials/xds/mutant_sanity_test.go; }
NEW_E2E='Test/SecurityConfigUpdate_(ProvidersReplacedDuringHandshake|DuringHandshake)$'
NEW_UNIT='Test/ClientCreds(ProviderReplaced|ProviderClosed)DuringHandshake$'
echo "## trace"; bash "$V/instrument/apply_trace.sh" && { go test ./internal/xds/balancer/clusterimpl/tests -run "$NEW_E2E" -count=1 -v 2>&1 | grep -E 'VERIFY-|^\s*--- |^(ok|FAIL)'; go test ./credentials/xds -run "$NEW_UNIT" -count=1 -v 2>&1 | grep -E 'VERIFY-|^\s*--- |^(ok|FAIL)'; }; revert
echo "## mutant: refuse connections initiated after replacement"; bash "$V/instrument/apply_post_replacement_mutant.sh" && { go test ./internal/xds/balancer/clusterimpl/tests -run "$NEW_E2E" -count=1 -v 2>&1 | grep -E '^\s*--- |^(ok|FAIL)'; go test ./credentials/xds -run "$NEW_UNIT" -count=1 -v 2>&1 | grep -E '^\s*--- |^(ok|FAIL)'; cp "$V/instrument/mutant_sanity_test.go" credentials/xds/; echo "## mutant sanity (must FAIL)"; go test -tags verify_repro ./credentials/xds -run '^TestVerifyMutantSanity' -count=1 -v 2>&1 | grep -E '_test.go:|^(ok|FAIL)'; }; revert
