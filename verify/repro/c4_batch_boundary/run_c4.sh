#!/bin/bash
# Run: ./run_c4.sh <clean worktree of evalon/grpc-go-en-7342a4ed>   (applies c4_hooks.patch, drops c4_batch_boundary_test.go.txt (renamed .go) into balancer/endpointsharding, runs TestC4_* under -race, then reverts)
set -uo pipefail
wt="$1"; here=$(cd "$(dirname "$0")" && pwd)
cd "$wt"
if [ -n "$(git status --porcelain -- balancer/endpointsharding)" ]; then
  echo "balancer/endpointsharding is not clean in $wt; refusing to patch" >&2; exit 2
fi
git apply "$here/c4_hooks.patch" || exit 1
cp "$here/c4_batch_boundary_test.go.txt" balancer/endpointsharding/c4_batch_boundary_test.go
echo "### go test ./balancer/endpointsharding -run 'TestC4_' -race -count=1 -v"
go test ./balancer/endpointsharding -run 'TestC4_' -race -count=1 -v
echo "exit=$?"
rm balancer/endpointsharding/c4_batch_boundary_test.go
git checkout -- balancer/endpointsharding/endpointsharding.go
