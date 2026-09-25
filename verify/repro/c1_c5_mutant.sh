#!/usr/bin/env bash
# Run (C1/C5, branch evalon/grpc-go-xd-4df8a32a): bash verify/repro/c1_c5_mutant.sh <4df8a32a-worktree>  — injects an eager-close mutant into handleSecurityConfig, runs both changed overlap tests without/with application delay, then reverts.
set -uo pipefail
here="$(cd "$(dirname "$0")" && pwd)"
cd "$1"
f=internal/xds/balancer/clusterimpl/clusterimpl.go
cp "$here/c1_c5_mutant_eager_close.go" internal/xds/balancer/clusterimpl/zz_verify_mutant.go
sed -i 's|^\thi := xds.NewHandshakeInfo(rootProvider, identityProvider|\tverifyMutantEagerClose(rootProvider)\n&|' "$f"
git --no-pager diff -U1 -- "$f"
run() {
  echo "##### $*"
  env "$@" go test ./internal/xds/balancer/clusterimpl/ -run 'Test/SecurityConfigUpdate_ProviderKeptAliveDuringHandshake' -count=1 -v 2>&1 | grep -E '^\s*--- |^ok|^FAIL|balancer_test.go:[0-9]+:'
  env "$@" go test ./internal/xds/balancer/clusterimpl/tests/ -run 'Test/SecurityConfigUpdate_ReplacedDuringRootLoad' -count=1 -v 2>&1 | grep -E '^\s*--- |^ok|^FAIL|clusterimpl_security_test.go:[0-9]+:'
}
run VERIFY_MUTANT_EAGER_CLOSE=0
run VERIFY_MUTANT_EAGER_CLOSE=1 VERIFY_MUTANT_DELAY_MS=0
run VERIFY_MUTANT_EAGER_CLOSE=1 VERIFY_MUTANT_DELAY_MS=300
sed -i '/^\tverifyMutantEagerClose(rootProvider)$/d' "$f"
rm -f internal/xds/balancer/clusterimpl/zz_verify_mutant.go
git --no-pager status --short -- internal/; echo reverted
