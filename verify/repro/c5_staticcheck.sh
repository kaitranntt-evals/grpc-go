#!/bin/bash
# Run: verify/repro/c5_staticcheck.sh   (from repo root; needs staticcheck from `scripts/vet.sh -install`; checks out evalon/grpc-go-en-1aafe684 in a throwaway worktree and runs scripts/vet.sh)
repo=$(git rev-parse --show-toplevel); wt=$(mktemp -d)/wt
git -C "$repo" fetch -q https://github.com/kaitranntt-evals/grpc-go-endpointsharding-decouple-locking.git evalon/grpc-go-en-1aafe684
git -C "$repo" worktree add -q --detach "$wt" FETCH_HEAD
cd "$wt" && PATH=$HOME/go/bin:$PATH ./scripts/vet.sh 2>&1 | grep -a 'ExitIdler is deprecated'; echo "vet.sh exit=${PIPESTATUS[0]}"
git -C "$repo" worktree remove --force "$wt"
