#!/usr/bin/env bash
# Run (C2): bash verify/repro/c2_mutant.sh <worktree-of-18d5930f|d5a6c3b1|b14e384a>  — injects a follow-up-handshake mutant, runs the branch's changed follow-up tests with it, then reverts.
set -uo pipefail
here="$(cd "$(dirname "$0")" && pwd)"
wt="$1"; cd "$wt"
cp "$here/c2_mutant_insecure_followup.go" credentials/xds/zz_verify_mutant.go
sed -i 's|^\tdefer hi.Release()$|\tdefer hi.Release()\n\tif err := verifyMutantCheck(); err != nil {\n\t\treturn nil, nil, err\n\t}|' credentials/xds/xds.go
git --no-pager diff --stat
case "$(git rev-parse --short=8 HEAD)" in
  ab780524) tests=("./internal/xds/balancer/clusterimpl/tests/ Test/SecurityConfigUpdate_DuringHandshake$" "./internal/xds/balancer/clusterimpl/ Test/SecurityConfig" "./internal/credentials/xds/ Test/HandshakeInfoReferenceCounting$");;
  e970be25) tests=("./test/xds/ Test/ClientSideXDS_SecurityConfigReplacedWithUntrustedRoots$" "./internal/credentials/xds/ Test/HandshakeInfo_");;
  da20fb35) tests=("./credentials/xds/ Test/ClientCredsHandshakeInfo" "./internal/xds/balancer/clusterimpl/ Test/SecurityConfigUpdate_ProvidersRetainedByInProgressHandshake$" "./internal/credentials/xds/ Test/(HandshakeInfo_|AcquireHandshakeInfo_|ClientSideTLSConfig_ProvidersReplaced)");;
esac
for m in "mutant: follow-up handshake failed for an unrelated non-trust reason" "x509: certificate is valid for other.example.com, not test.example.com"; do
  echo "##### VERIFY_MUTANT_ERR=\"$m\""
  for t in "${tests[@]}"; do
    set -- $t
    VERIFY_MUTANT_ERR="$m" go test "$1" -run "$2" -count=1 -v 2>&1 | grep -E '^(--- |ok|FAIL|panic)|want|failed' | grep -v '^=== '
  done
done
sed -i '/verifyMutantCheck/,+2d' credentials/xds/xds.go
rm -f credentials/xds/zz_verify_mutant.go
git --no-pager diff --stat; echo "reverted"
