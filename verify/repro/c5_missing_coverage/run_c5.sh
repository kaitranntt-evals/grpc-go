#!/bin/bash
# Run: ./run_c5.sh <clean worktree of evalon/grpc-go-en-7342a4ed>   (for each m*.patch: apply the production mutation, run the maintained endpointsharding suite 3x under -race, then run the archived eval fixture eval_endpointsharding_test.go.txt (byte-exact copy, stored with .txt so go vet ./... skips it) under -race; revert)
# A mutation that passes the maintained suite but fails the fixture shows the maintained tests lack that coverage.
set -uo pipefail
wt="$1"; here=$(cd "$(dirname "$0")" && pwd)
cd "$wt"
if [ -n "$(git status --porcelain -- balancer/endpointsharding)" ]; then
  echo "balancer/endpointsharding is not clean in $wt; refusing to patch" >&2; exit 2
fi
for patch in "$here"/m*.patch; do
  name=$(basename "$patch" .patch)
  echo "##### mutation $name"
  git apply "$patch" || { echo "apply failed"; exit 1; }
  echo "### maintained suite: go test ./balancer/endpointsharding -run '^Test$' -race -count=3"
  go test ./balancer/endpointsharding -run '^Test$' -race -count=3 2>&1 | grep -E "^\s*--- FAIL|^(ok|FAIL)|_test.go:[0-9]+: |race detected" | sort | uniq -c
  cp "$here/eval_endpointsharding_test.go.txt" balancer/endpointsharding/eval_endpointsharding_test.go
  echo "### archived fixture: go test ./balancer/endpointsharding -run '^TestEval_' -race -count=1 -v"
  go test ./balancer/endpointsharding -run '^TestEval_' -race -count=1 -v 2>&1 | grep -E "^\s*--- FAIL|^(ok|FAIL)|_test.go:[0-9]+: (expected|observed|child|Expected|Got)"
  rm balancer/endpointsharding/eval_endpointsharding_test.go
  git checkout -- balancer/endpointsharding/endpointsharding.go
done
