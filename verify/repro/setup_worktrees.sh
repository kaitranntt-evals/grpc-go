#!/bin/bash
# Run: verify/repro/setup_worktrees.sh <dir>   (from the grpc-go checkout: creates <dir>/<branch id> for every claim-target branch, <dir>/perfect for the audited branch and <dir>/base for the base commit)
set -euo pipefail
W="$1"; mkdir -p "$W"
git remote get-url claims >/dev/null 2>&1 || git remote add claims https://github.com/kaitranntt-evals/grpc-go-xds-rds-interceptor-lifecycle-leak
for b in eababd58 b7bc0d7f dab66e4f 415a74da 0723d2f4 0ed1f32c eb9c66bf e03d1e42 98faa6aa 2ffad480 dad62956 6851db1c 3e0a44dd eb19a38b 74924778 79540ac1 2a01d623 311db1b4 bdc42e7c cf01ba8b 57bb4302 37fb43a6 6cf08267; do
  [ -d "$W/$b" ] && continue
  git fetch -q claims "evalon/grpc-go-xd-$b"
  git worktree add -q --detach "$W/$b" FETCH_HEAD
  echo "$b $(git -C "$W/$b" rev-parse --short HEAD) merge-base=$(git merge-base FETCH_HEAD 4ee6ac46fada69c06576cee108b009689a000520 | cut -c1-8)"
done
[ -d "$W/perfect" ] || { git fetch -q origin grpc-go-xds-rds-interceptor-lifecycle-leak-perfect; git worktree add -q --detach "$W/perfect" FETCH_HEAD; }
[ -d "$W/base" ] || git worktree add -q --detach "$W/base" 4ee6ac46fada69c06576cee108b009689a000520
echo "perfect $(git -C "$W/perfect" rev-parse --short HEAD); base $(git -C "$W/base" rev-parse --short HEAD)"
