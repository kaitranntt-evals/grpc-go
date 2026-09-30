#!/usr/bin/env bash
# Usage: FIXTURES_DIR=<extracted eval_tests.zip>/tests verify/repro/setup_worktree.sh <short-branch-id e.g. b7a066a6>   (prints the worktree path; FIXTURES_DIR default ~/eval/tests, WT_ROOT default ~/wt)
# Fetches evalon/grpc-go-xd-<id> from the claims repository, checks it out in a detached worktree and
# provisions the archived eval fixtures byte-exact at the paths assessment uses.
set -euo pipefail
id=$1
repo=$(cd "$(dirname "$0")/../.." && pwd)
fx=${FIXTURES_DIR:-$HOME/eval/tests}
wt=${WT_ROOT:-$HOME/wt}/$id
cd "$repo"
git remote get-url claims >/dev/null 2>&1 || git remote add claims https://github.com/kaitranntt-evals/grpc-go-xds-certificate-provider-closure-race
if [ ! -d "$wt" ]; then
  git fetch -q claims "evalon/grpc-go-xd-$id"
  mkdir -p "$(dirname "$wt")"
  git worktree add -q --detach "$wt" FETCH_HEAD
fi
if [ ! -f "$fx/eval_handshake_lifetime_test.go" ]; then
  echo "setup_worktree.sh: $fx has no eval fixtures; only steps that run the branch's own tests will work" >&2
  echo "$wt"; exit 0
fi
mkdir -p "$wt/.evaltools" "$wt/test"
cp "$fx/candidate_test_inventory.go" "$wt/.evaltools/candidate_test_inventory.go"
cp "$fx/eval_handshake_lifetime_test.go" "$wt/internal/xds/balancer/clusterimpl/tests/eval_handshake_lifetime_test.go"
cp "$fx/run_eval_xds_test_group.sh" "$fx/run_candidate_tests.sh" "$wt/test/"
echo "$wt"
