#!/usr/bin/env bash
# Shared driver: bash verify/repro/lib/run_follow_up_roots_mutation.sh <branch-id> <go-package> <go-test-run-pattern>
#
# Checks out claim branch evalon/grpc-go-xd-<branch-id> from the claims repository into a
# temporary worktree, runs the branch's own test and the eval fixture unmodified (both must
# PASS), then applies mutation M5 to internal/credentials/xds/handshake_info.go and reruns both.
#
# Mutation M5: ClientSideTLSConfig still calls KeyMaterial() on the root/identity providers of
# the HandshakeInfo it acquired (so every "new provider's KeyMaterial was called" signal still
# fires), but the tls.Config is built from the key material returned by the FIRST successful
# call in the process. A follow-up connection is therefore NOT governed by the replacement
# validation roots. A test that has causal follow-up evidence must FAIL under M5 (the eval
# fixture does: "Follow-up RPC error = <nil>, want x509 unknown authority"). The script exits 0
# when the weakness reproduces, i.e. the branch test still PASSES while the fixture FAILS.
set -euo pipefail
id=$1; pkg=$2; pat=$3
here=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
repo=$(git -C "$here" rev-parse --show-toplevel)
url=https://github.com/kaitranntt-evals/grpc-go-xds-certificate-provider-closure-race.git
branch=evalon/grpc-go-xd-$id
wt=$(mktemp -d "${TMPDIR:-/tmp}/verify-$id-XXXXXX")
cleanup() { git -C "$repo" worktree remove --force "$wt" >/dev/null 2>&1 || true; }
trap cleanup EXIT

git -C "$repo" fetch -q "$url" "$branch"
git -C "$repo" worktree add -q --detach "$wt" FETCH_HEAD
echo "== $branch @ $(git -C "$wt" rev-parse --short HEAD) in $wt"
cp "$here/eval_handshake_lifetime_test.go.fixture" "$wt/internal/xds/balancer/clusterimpl/tests/eval_handshake_lifetime_test.go"

fixture_pkg=./internal/xds/balancer/clusterimpl/tests/
fixture_pat='^TestEval_SecurityConfigUpdate_ActiveHandshakeKeepsProvider$'
run() { # run <label> <pkg> <pattern>; prints PASS/FAIL lines and returns go test's status
  local label=$1 p=$2 r=$3 out status=0
  out=$(cd "$wt" && go test -race -count=1 -v -run "$r" "$p" 2>&1) || status=$?
  echo "-- $label: go test -race -count=1 -v -run '$r' $p"
  echo "$out" | grep -E '^(\s*--- (PASS|FAIL)|ok|FAIL|PASS)|_test.go:[0-9]+: [A-Z]' | grep -v 'Created new snapshot\|Registered Aggregated\|management server serving' || true
  return $status
}

echo "== control: unmodified branch"
run "branch test" "$pkg" "$pat"
run "eval fixture" "$fixture_pkg" "$fixture_pat"

echo "== applying mutation M5"
hi=$wt/internal/credentials/xds/handshake_info.go
cp "$here/m5_stale_key_material.go.txt" "$wt/internal/credentials/xds/eval_m5_mutation.go"
sed -i '0,/^\tkm, err := rootProv.KeyMaterial(ctx)$/s//\tkm, err := rootProv.KeyMaterial(ctx)\n\tkm, err = evalM5Stale(\&evalM5Roots, km, err)/' "$hi"
sed -i '0,/^\t\tkm, err := idProv.KeyMaterial(ctx)$/s//\t\tkm, err := idProv.KeyMaterial(ctx)\n\t\tkm, err = evalM5Stale(\&evalM5Certs, km, err)/' "$hi"
git -C "$wt" diff -- internal/credentials/xds/handshake_info.go | grep '^[+-][^+-]'

echo "== under mutation M5"
branch_status=0; fixture_status=0
run "branch test" "$pkg" "$pat" || branch_status=$?
run "eval fixture" "$fixture_pkg" "$fixture_pat" || fixture_status=$?
echo "== result"
if [ $branch_status -eq 0 ] && [ $fixture_status -ne 0 ]; then
  echo "WEAKNESS REPRODUCED: the branch test still PASSES when the follow-up connection is not governed by the replacement roots; the eval fixture FAILS on the same mutant."
  exit 0
fi
echo "not reproduced: branch test status=$branch_status fixture status=$fixture_status"
exit 1
