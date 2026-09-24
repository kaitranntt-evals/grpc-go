#!/usr/bin/env bash
# Run: verify/repro/c5_run.sh <commit-ish>  (repo root) — scratch worktree; adds VerifyC5AfterBuild hook after construction unlocks childMu, runs TestC5Controlled
set -u
rev=$1; root=$(git rev-parse --show-toplevel)
wt=$(mktemp -d "$HOME/c5wt.XXXX"); git -C "$root" worktree add -q --detach "$wt" "$rev"; cd "$wt"
F=balancer/endpointsharding/endpointsharding.go
sed '1,2d' "$root/verify/repro/c5_hook.go.txt" > balancer/endpointsharding/verify_c5_hook.go
cp "$root/verify/repro/c5_controlled_test.go" balancer/endpointsharding/
perl -0pi -e 's/(\t\t\tchildBalancer\.childMu\.Unlock\(\)\n)/$1\t\t\tif VerifyC5AfterBuild != nil {\n\t\t\t\tVerifyC5AfterBuild()\n\t\t\t}\n/' $F
echo "instrumented:"; grep -n -B3 -A3 'VerifyC5AfterBuild()' $F
timeout 120 go test -race ./balancer/endpointsharding/ -count=1 -timeout 60s -run '^TestC5Controlled$' -v 2>&1 | grep -E -- '^(=== RUN|--- |ok|FAIL)|c5_controlled_test.go'
echo "exit=${PIPESTATUS[0]}"
cd "$root"; git worktree remove --force "$wt"
