#!/usr/bin/env bash
# Run: [ONLY=<test-regex>] [VERIFY_C1_EXIT_STALL=1] verify/repro/c1_run.sh <commit-ish>  (repo root; stalls a held child update, or with VERIFY_C1_EXIT_STALL every child ExitIdle, and runs the branch's added tests)
set -u
rev=$1
root=$(git rev-parse --show-toplevel)
wt=$(mktemp -d "$HOME/c1wt.XXXX")
git -C "$root" worktree add -q --detach "$wt" "$rev"
cd "$wt"
sed '1,2d' "$root/verify/repro/c1_stall_hook.go.txt" > balancer/endpointsharding/verify_c1_hook.go
sed -i 's/bw\.child\.UpdateClientConnState(ccs)/verifyC1Wrap(bw.child.UpdateClientConnState)(ccs)/' balancer/endpointsharding/endpointsharding.go
perl -pi -e 's/(\b[a-z]\w*\.(?:child|childLB|childBalancer))\.ExitIdle\(\)/verifyC1ExitWrap($1.ExitIdle)()/' balancer/endpointsharding/endpointsharding.go
echo "hooked call sites:"; grep -n 'verifyC1Wrap\|verifyC1ExitWrap' balancer/endpointsharding/endpointsharding.go
files=$(git diff --name-only bf9e7cd3 -- balancer/endpointsharding | grep '_test.go$')
tests=$(for f in $files; do git diff bf9e7cd3 -- "$f" | sed -nE 's/^\+func (\(s\) )?(Test[A-Za-z0-9_]+)\(.*/\2/p'; done)
for t in $tests; do
  [ "$t" = Test ] && continue
  [ -n "${ONLY:-}" ] && ! [[ $t =~ $ONLY ]] && continue
  run="^Test\$/^${t#Test}\$"
  grep -q "^func $t(" $files 2>/dev/null && run="^$t\$"
  echo "### $t"
  VERIFY_C1_STALL=${VERIFY_C1_STALL:-200us} timeout 120 go test ./balancer/endpointsharding/ -count=1 -timeout 45s -run "$run" -v 2>&1 \
    | grep -E '^\s*(--- |=== RUN|panic: test timed out|FAIL|ok )|_test.go:[0-9]+:|endpointSharding\)\.Close\(|balancerWrapper\)\.close\(|endpointState\)\.close\(|verifyC1Wrap|verifyC1:|_test\.go:[0-9]+ \+' | head -40
  echo "exit=${PIPESTATUS[0]}"
done
cd "$root"; git worktree remove --force "$wt"
