#!/usr/bin/env bash
# Run: verify/repro/c7_mutations.sh <commit-ish of a68de200>  (repo root) — applies one production mutation at a time in a scratch worktree and runs the committed endpointsharding suite under -race
set -u
rev=$1; root=$(git rev-parse --show-toplevel)
F=balancer/endpointsharding/endpointsharding.go
declare -A M=(
 [0_baseline]='s/VERIFY_NO_MATCH//'
 [1_same_child_contention]='s/(func \(bw \*balancerWrapper\) exitIdleSync\(\) \{\n)\tbw\.mu\.Lock\(\)\n\tdefer bw\.mu\.Unlock\(\)\n/$1/'
 [2_retained_closed_handles]='s/(func \(bw \*balancerWrapper\) exitIdleSync\(\) \{\n\tbw\.mu\.Lock\(\)\n\tdefer bw\.mu\.Unlock\(\)\n\tif bw\.child == nil) \|\| bw\.isClosed/$1/'
 [3_child_error_continuation]='s/\}\); err != nil && ret == nil \{/}); err != nil {\n\t\t\treturn err\n\t\t} else if false {/'
 [4_synchronous_lifecycle_callbacks]='s/(func \(bw \*balancerWrapper\) UpdateState\(state balancer\.State\) \{\n)/$1\tbw.mu.Lock()\n\tbw.mu.Unlock()\n/'
 [5_complete_publication_observation]='s/if es\.inhibitChildUpdates\.Load\(\) \{/if false \&\& es.inhibitChildUpdates.Load() {/'
)
for m in $(printf '%s\n' "${!M[@]}" | sort); do
  wt=$(mktemp -d "$HOME/c7wt.XXXX"); git -C "$root" worktree add -q --detach "$wt" "$rev"
  ( cd "$wt"; perl -0pi -e "${M[$m]}" $F
    echo "### mutation $m"; git diff --stat | tail -1; git diff -U0 | grep '^[-+][^-+]' | head -8
    timeout 150 go test -race ./balancer/endpointsharding/ -count=1 -timeout 60s -v 2>&1 \
      | grep -E '^(ok|FAIL|panic: test timed out|WARNING: DATA RACE)|^\s*--- FAIL|_test\.go:[0-9]+: |testing\.go:[0-9]+: race|^\s+Test/|^=== RUN' | grep -v '^=== RUN' | head -14
    echo "exit=${PIPESTATUS[0]}" )
  git -C "$root" worktree remove --force "$wt"
done
