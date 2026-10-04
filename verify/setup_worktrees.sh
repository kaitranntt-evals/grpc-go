#!/bin/bash
# Fetches every claim-target branch and checks each out (detached) under $WT (default ~/wt). Run from the grpc-go checkout: bash verify/setup_worktrees.sh
set -eu
WT=${WT:-$HOME/wt}
git remote get-url claims >/dev/null 2>&1 || git remote add claims https://github.com/kaitranntt-evals/grpc-go-transport-restrict-memory-overhead
for b in 2b0ddded 5ccf77e7 ae31c146 2994fc5c d2e82310 d0aa7e9e 4c52c410 72e9069b 3e551c7b 17011fad 8b3e4b01 9253f1d4 a3b171be 718bb10b 37793f8a d4a3af3a; do
  git fetch -q claims "evalon/grpc-go-tr-$b:refs/remotes/claims/evalon/grpc-go-tr-$b"
  [ -d "$WT/$b" ] || git worktree add -q --detach "$WT/$b" "claims/evalon/grpc-go-tr-$b"
  echo "$b $(git -C "$WT/$b" rev-parse HEAD)"
done
