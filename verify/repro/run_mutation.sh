#!/bin/bash
# Run: verify/repro/run_mutation.sh <eval-branch> <patch-or-"none"> <TestMethodName> [reps]   (from the repo root; creates a throwaway worktree, applies the patch, runs the grpctest subtest under -race with a 60s process deadline)
set -u
branch=$1; patch=$2; test=$3; reps=${4:-1}
repo=$(git rev-parse --show-toplevel)
url=https://github.com/kaitranntt-evals/grpc-go-endpointsharding-decouple-locking.git
wt=$(mktemp -d)/wt
git -C "$repo" fetch -q "$url" "$branch"
git -C "$repo" worktree add -q --detach "$wt" FETCH_HEAD
[ "$patch" != none ] && git -C "$wt" apply "$repo/$patch"
cd "$wt" && go test -c -race -o /tmp/verify_mut.test ./balancer/endpointsharding || exit 1
cd "$wt/balancer/endpointsharding"
for i in $(seq 1 "$reps"); do
  s=$(date +%s%N)
  /tmp/verify_mut.test -test.run "^Test\$/^${test#Test}\$" -test.count=1 -test.timeout=60s -test.v > /tmp/verify_mut.$i.log 2>&1
  rc=$?
  echo "run$i rc=$rc elapsed=$(( ($(date +%s%N)-s)/1000000 ))ms process_timeout=$(grep -c 'panic: test timed out' /tmp/verify_mut.$i.log)"
  grep -E 'MUTATION C1|_test\.go:[0-9]+: |^panic' /tmp/verify_mut.$i.log | head -4
  grep -E '\((\*endpointSharding|\*balancerWrapper)\)\.(Close|close)\(' -A1 /tmp/verify_mut.$i.log | head -4
done
git -C "$repo" worktree remove --force "$wt"
