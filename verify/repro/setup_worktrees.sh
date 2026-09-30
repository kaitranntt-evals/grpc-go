#!/bin/bash
# Run (from the repo root): verify/repro/setup_worktrees.sh   -- fetches every claim-target branch and checks each out at ${WT:-/tmp/claims}/<suffix>
set -e
WT=${WT:-/tmp/claims}
git remote get-url claims >/dev/null 2>&1 || git remote add claims https://github.com/kaitranntt-evals/grpc-go-transport-restrict-memory-overhead
mkdir -p "$WT"
for s in fec69d29 86a3fb3a 3472ebaa 1b13d423 698fb805 ba102f71 d0520117 5ccaf207 8f035afd f70d0754 80a05d05 6fe210bc 8ac37769 97f9441e 5368e0cb; do
  git fetch -q claims "evalon/grpc-go-tr-$s"
  [ -d "$WT/$s" ] || git worktree add -q --detach "$WT/$s" FETCH_HEAD
  echo "$s $(git -C "$WT/$s" rev-parse --short HEAD)"
done
