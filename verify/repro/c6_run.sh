#!/usr/bin/env bash
# Run: verify/repro/c6_run.sh <commit-ish>  (from the repo root) — instruments updateState with a pause hook and runs TestC6
set -u
rev=$1; root=$(git rev-parse --show-toplevel); wt=$(mktemp -d "$HOME/c6wt.XXXX")
git -C "$root" worktree add -q --detach "$wt" "$rev"; cd "$wt"
sed '1,2d' "$root/verify/repro/c6_hook.go.txt" > balancer/endpointsharding/verify_c6_hook.go
perl -0pi -e 's/(\tif es\.inhibitChildUpdates\.Load\(\) \{\n\t\treturn\n\t\})/$1\n\tverifyC6AfterAdmission()/' balancer/endpointsharding/endpointsharding.go
echo "instrumented:"; grep -n -B3 'verifyC6AfterAdmission()' balancer/endpointsharding/endpointsharding.go
cp "$root/verify/repro/c6_admitted_callback_test.go" balancer/endpointsharding/
go test -race -count=1 -run TestC6 -v ./balancer/endpointsharding/ 2>&1 | grep -vE '^=== RUN   Test$|^--- PASS: Test \('
cd "$root"; git worktree remove --force "$wt"
