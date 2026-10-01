#!/bin/bash
# Run: verify/repro/setup_worktrees.sh   -- fetches every claim-target branch and creates one detached worktree per branch under $WT.
source "$(dirname "$0")/common.sh"
cd "$REPO" || exit 2
git remote get-url claims >/dev/null 2>&1 || git remote add claims https://github.com/kaitranntt-evals/grpc-go-xds-rds-interceptor-lifecycle-leak
mkdir -p "$WT"
for id in 4e0dad18 b158dd8d df007aa8 b7d20b57 ee3cc2af a0b82600 5e4946c0 e14bf49d e1dc717f 2c8abc16 43ae1ade c47f6009 52de0075 \
          d33210d2 2bdc2970 d5ce5e46 e5dfd84c 3d609e1d 55cd92cd 545b9364 afc3b15e c5d0b2f7 dc4ed267 20c60584 cd6e69ae; do
  [ -d "$WT/$id" ] && continue
  git fetch -q claims "evalon/grpc-go-xd-$id" && git worktree add -q --detach "$WT/$id" FETCH_HEAD && echo "$id $(git -C "$WT/$id" rev-parse HEAD)"
done
