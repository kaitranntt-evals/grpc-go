#!/bin/bash
# Run: verify/repro/c7_mutations.sh   (from repo root; removes the closed-handle guard, first-child-error propagation and ResolverError inhibition on evalon/grpc-go-en-abc06f48 — maintained package tests still pass; control mutation deadlocks ResolverError callbacks and TestEndpointShardingBasic fails)
set -u
repo=$(git rev-parse --show-toplevel); url=https://github.com/kaitranntt-evals/grpc-go-endpointsharding-decouple-locking.git
git -C "$repo" fetch -q "$url" evalon/grpc-go-en-abc06f48
wt=$(mktemp -d)/wt; git -C "$repo" worktree add -q --detach "$wt" FETCH_HEAD
git -C "$wt" apply "$repo/verify/repro/c7_abc_guards_removed.patch"
(cd "$wt" && go vet ./balancer/endpointsharding/ && go test -race -count=1 ./balancer/endpointsharding/; echo "guards-removed rc=$?")
git -C "$repo" worktree remove --force "$wt"
"$(dirname "$0")/run_mutation.sh" evalon/grpc-go-en-abc06f48 verify/repro/c7_abc_resolvererror_deadlock.patch TestEndpointShardingBasic 1
