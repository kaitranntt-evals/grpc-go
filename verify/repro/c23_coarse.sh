#!/usr/bin/env bash
# Run: verify/repro/c23_coarse.sh <commit-ish> '<exitIdleFunc|updateClientConnState>' <TestName> <count>  (repo root) — adds one package-wide mutex around the named per-child functions (coarse-lock model) and repeats the branch's independent-progress test
set -u
rev=$1; funcs=$2; test=$3; n=$4; root=$(git rev-parse --show-toplevel)
wt=$(mktemp -d "$HOME/c23wt.XXXX"); git -C "$root" worktree add -q --detach "$wt" "$rev"; cd "$wt"
F=balancer/endpointsharding/endpointsharding.go
printf 'package endpointsharding\n\nimport "sync"\n\nvar verifyCoarseMu sync.Mutex\n' > balancer/endpointsharding/verify_coarse.go
perl -0pi -e "s/(func \\(bw \\*balancerWrapper\\) (?:$funcs)\\([^)]*\\)[^{\\n]*\\{\\n)/\$1\\tverifyCoarseMu.Lock()\\n\\tdefer verifyCoarseMu.Unlock()\\n/g" $F
echo "instrumented:"; grep -n -B1 'verifyCoarseMu.Lock' $F
pass=0; fail=0
for i in $(seq 1 "$n"); do
  out=$(timeout 60 go test ./balancer/endpointsharding/ -count=1 -timeout 45s -run "^Test\$/^${test#Test}\$" -v 2>&1)
  if echo "$out" | grep -q "^ok"; then pass=$((pass+1)); echo "run $i: PASS"; else fail=$((fail+1)); echo "run $i: FAIL"; echo "$out" | grep -E '_test\.go:[0-9]+: ' | head -3; fi
done
echo "coarse-lock model: $pass/$n PASS, $fail/$n FAIL"
cd "$root"; git worktree remove --force "$wt"
