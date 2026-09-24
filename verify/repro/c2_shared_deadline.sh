#!/usr/bin/env bash
# Run: verify/repro/c2_shared_deadline.sh <commit-ish> '<per-child funcs>' <TestName> '<ExitIdle call line>' <runs>  (repo root) — coarse-lock model + a pause after the other child's ExitIdle request so the shared deadline fires first; counts runs whose assertion accepts B's signal
set -u
rev=$1; funcs=$2; test=$3; call=$4; n=$5; root=$(git rev-parse --show-toplevel)
wt=$(mktemp -d "$HOME/c2dwt.XXXX"); git -C "$root" worktree add -q --detach "$wt" "$rev"; cd "$wt"
F=balancer/endpointsharding/endpointsharding.go; T=balancer/endpointsharding/endpointsharding_ext_test.go
printf 'package endpointsharding\n\nimport "sync"\n\nvar verifyCoarseMu sync.Mutex\n' > balancer/endpointsharding/verify_coarse.go
perl -0pi -e "s/(func \\(bw \\*balancerWrapper\\) (?:$funcs)\\([^)]*\\)[^{\\n]*\\{\\n)/\$1\\tverifyCoarseMu.Lock()\\n\\tdefer verifyCoarseMu.Unlock()\\n/g" $F
CALL="$call" perl -0pi -e 's/^(\t\Q$ENV{CALL}\E\n)/$1\ttime.Sleep(defaultTestTimeout + time.Second) \/\/ verify: preemption until the shared deadline has fired\n/m' $T
echo "instrumented:"; grep -n -B1 'verifyCoarseMu.Lock' $F; grep -n -B1 -A1 'verify: preemption' $T
acc=0; rej=0
for i in $(seq 1 "$n"); do
  out=$(timeout 90 go test ./balancer/endpointsharding/ -count=1 -timeout 60s -run "^Test\$/^${test#Test}\$" -v 2>&1)
  if echo "$out" | grep -q "^ok"; then acc=$((acc+1)); echo "run $i: PASS (assertion accepted B's signal after the deadline released A)"; else rej=$((rej+1)); echo "run $i: FAIL"; echo "$out" | grep -E '_test\.go:[0-9]+: ' | head -2; fi
done
echo "shared-deadline interleaving under coarse lock: $acc/$n accepted, $rej/$n rejected"
cd "$root"; git worktree remove --force "$wt"
