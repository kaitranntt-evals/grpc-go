#!/usr/bin/env bash
# C4 repro: show that the committed endpointsharding test suite on branch
# evalon/grpc-go-en-e97b83bb (kaitranntt-evals/grpc-go-endpointsharding-decouple-locking)
# stays green when either the stale/closed-child ExitIdle guard or the
# "continue past a child's UpdateClientConnState error" behavior is removed,
# while the eval fixture (not committed on the branch) catches both.
#
# Run from a checkout of that branch, with REPRO pointing at this directory and
# FIXTURE at the archived tests/eval_endpointsharding_test.go:
#   REPRO=~/repos/grpc-go/verify/repro FIXTURE=~/eval_tests/tests/eval_endpointsharding_test.go bash $REPRO/c4_mutation_check.sh
set -uo pipefail
REPRO=${REPRO:-$(cd "$(dirname "$0")" && pwd)}
FIXTURE=${FIXTURE:?path to eval_endpointsharding_test.go}
PKG=./balancer/endpointsharding
DEST=balancer/endpointsharding/eval_endpointsharding_test.go

echo "== committed tests on this branch"
grep -n '^func (s) Test\|^func Test' balancer/endpointsharding/*_test.go
echo "== committed tests mentioning closed/stale handles (expect none)"
grep -n 'isClosed\|Closed\|stale' balancer/endpointsharding/*_test.go || echo "(none)"
echo "== stub children returning an error from UpdateClientConnState (expect none)"
grep -n 'UpdateClientConnState: func' -A3 balancer/endpointsharding/*_test.go | grep 'return' || echo "(none)"

echo "== baseline: committed suite"
go test $PKG -race -count=1 2>&1 | tail -1
echo "== baseline: committed suite + eval fixture"
cp "$FIXTURE" "$DEST" && go test $PKG -race -count=1 2>&1 | tail -1; rm -f "$DEST"

for m in stale_handle child_error_abort; do
  git apply "$REPRO/c4_mutation_$m.patch" || { echo "patch $m did not apply"; exit 2; }
  echo "== mutation $m: committed suite only"
  go test $PKG -race -count=1 -v 2>&1 | grep -E '^(--- FAIL|FAIL|ok|PASS)'
  echo "== mutation $m: committed suite + eval fixture"
  cp "$FIXTURE" "$DEST"
  go test $PKG -race -count=1 -v 2>&1 | grep -E '^(    --- FAIL|--- FAIL|FAIL|ok)|eval_endpointsharding_test.go:[0-9]+:' | head -12
  rm -f "$DEST"
  git checkout -- balancer/endpointsharding/endpointsharding.go
done
git status --short
