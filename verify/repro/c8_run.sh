#!/usr/bin/env bash
# Run: verify/repro/c8_run.sh <d1fc592e commit-ish>  (repo root) — coarse-lock base bf9e7cd3 + the branch's ext test with only ep1State.ExitIdle() adapted; external timeout + goroutine trace
set -u
rev=$1; root=$(git rev-parse --show-toplevel)
wt=$(mktemp -d "$HOME/c8wt.XXXX"); git -C "$root" worktree add -q --detach "$wt" bf9e7cd3430df40d0732ba42eb88bd5f2cc63407; cd "$wt"
T=balancer/endpointsharding/endpointsharding_ext_test.go
git show "$rev:$T" > $T
sed -i 's/\tep1State\.ExitIdle()/\tep1State.Balancer.ExitIdle()/' $T
echo "test adaptation vs $rev:"; diff <(git -C "$root" show "$rev:$T") $T
echo "production diff vs coarse-lock base:"; git diff --stat -- balancer/endpointsharding/endpointsharding.go; echo "(none)"
timeout 120 go test ./balancer/endpointsharding/ -count=1 -timeout 45s -run '^Test$/^EndpointShardingExitIdleWhileOtherChildBlocked$' -v 2>&1 \
  | grep -E '^\s*(--- |panic: test timed out|FAIL|ok )|_test\.go:[0-9]+: |^goroutine [0-9]+ \[|endpointsharding\.\(\*endpointSharding\)\.|endpointsharding\.\(\*balancerWrapper\)\.|endpointsharding_ext_test\.go:[0-9]+ \+|sync\.\(\*Mutex\)\.Lock' | head -60
echo "exit=${PIPESTATUS[0]}"
cd "$root"; git worktree remove --force "$wt"
