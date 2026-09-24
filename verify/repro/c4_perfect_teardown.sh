#!/bin/bash
# Run: verify/repro/c4_perfect_teardown.sh   (from repo root; M2 = idle-exit worker coupled to batch/Close inhibition, M4 = synchronous child callback deadlock; tests on the audited perfect branch)
set -u
repo=$(git rev-parse --show-toplevel); wt=$(mktemp -d)/wt
git -C "$repo" fetch -q origin grpc-go-endpointsharding-decouple-locking-perfect
for m in c4_m2_perfect:TestDecoupledChildProgress c4_m4_perfect:TestDecoupledChildProgress c4_m4_perfect:TestSameChildMutualExclusion; do
  p=${m%%:*}; t=${m#*:}
  git -C "$repo" worktree add -q --detach "$wt" FETCH_HEAD && git -C "$wt" apply "$repo/verify/repro/$p.patch"
  (cd "$wt" && go test -c -race -o /tmp/c4.test ./balancer/endpointsharding) || exit 1
  for i in 1 2 3 4; do
    s=$(date +%s); (cd "$wt/balancer/endpointsharding" && /tmp/c4.test -test.run "^Test\$/^${t#Test}\$" -test.count=1 -test.timeout=60s -test.v > /tmp/c4.log 2>&1); rc=$?
    echo "$p $t run$i rc=$rc elapsed=$(( $(date +%s)-s ))s"; grep -E '_test\.go:[0-9]+: |^panic' /tmp/c4.log | head -3; grep -E 'endpointsharding(_test)?\.go:[0-9]+' /tmp/c4.log | head -6
  done
  git -C "$repo" worktree remove --force "$wt"
done
