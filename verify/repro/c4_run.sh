#!/usr/bin/env bash
# Run: [VERIFY_C4_SKIP=<n>] verify/repro/c4_run.sh <audited commit-ish> <setup|exit_before|exit_after> <TestName>  (repo root) — stalls one production operation and runs the maintained test under a 30s go-test timeout
set -u
rev=$1; point=$2; test=$3; root=$(git rev-parse --show-toplevel)
wt=$(mktemp -d "$HOME/c4wt.XXXX"); git -C "$root" worktree add -q --detach "$wt" "$rev"; cd "$wt"
F=balancer/endpointsharding/endpointsharding.go
sed '1,2d' "$root/verify/repro/c4_hook.go.txt" > balancer/endpointsharding/verify_c4_hook.go
perl -0pi -e 's/(func \(es \*endpointState\) updateClientConnStateLocked\(state balancer\.ClientConnState\) error \{\n)/$1\tverifyC4("setup")\n/; s/(func \(es \*endpointState\) exitIdle\(\) \{\n\tes\.childMu\.Lock\(\)\n)/$1\tverifyC4("exit_before")\n/; s/(\t\tes\.childLB\.ExitIdle\(\)\n)/$1\t\tverifyC4("exit_after")\n/' $F
echo "instrumented:"; grep -n 'verifyC4(' $F
VERIFY_C4=$point timeout 90 go test ./balancer/endpointsharding/ -count=1 -timeout 30s -run "^Test\$/^${test#Test}\$" -v 2>&1 \
  | grep -E '^\s*(--- |panic: test timed out|FAIL|ok )|_test\.go:[0-9]+: |verifyC4:|^goroutine [0-9]+ \[|endpointsharding\.\(\*|endpointsharding_test\.go:[0-9]+ \+' | head -45
echo "exit=${PIPESTATUS[0]}"
cd "$root"; git worktree remove --force "$wt"
