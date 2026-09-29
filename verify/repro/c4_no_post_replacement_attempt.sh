#!/usr/bin/env bash
# Run from a worktree of evalon/grpc-go-xd-{e709f9c1,55c54f73,48dd6ba3}: bash <path-to>/verify/repro/c4_no_post_replacement_attempt.sh  (every added test PASSES while any connection initiated after replacement is refused)
set -uo pipefail
V="$(cd "$(dirname "$0")/.." && pwd)"
revert(){ git checkout -q credentials/xds/xds.go; rm -f credentials/xds/verify_mutant.go credentials/xds/mutant_sanity_test.go; }
bash "$V/instrument/apply_post_replacement_mutant.sh" || exit 1
go test ./internal/xds/balancer/clusterimpl/tests -run 'Test/SecurityConfigUpdate_DuringHandshake$' -count=1 -v 2>&1 | grep -E '^\s*--- |^(ok|FAIL)'
go test ./credentials/xds -run 'Test/ClientCreds(ProviderReplacedDuringHandshake|ProviderClosedDuringHandshake|HandshakeWithConcurrentProviderReplacement)$' -count=1 -v 2>&1 | grep -E '^\s*--- |^(ok|FAIL)'
go test ./internal/xds/balancer/clusterimpl -run 'Test/(SecurityConfigUpdate_ProvidersReplacedOnlyOnChange|SecurityConfigUpdate_CertProviderLifecycle|HandleSecurityConfig_.*)$' -count=1 -v 2>&1 | grep -E '^\s*--- |^(ok|FAIL)'
cp "$V/instrument/mutant_sanity_test.go" credentials/xds/
echo "## mutant sanity (must FAIL)"; go test -tags verify_repro ./credentials/xds -run '^TestVerifyMutantSanity' -count=1 -v 2>&1 | grep -E '_test.go:|^(ok|FAIL)'
revert
