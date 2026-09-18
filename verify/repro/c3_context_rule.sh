#!/usr/bin/env bash
# Run (in a checkout of evalon/grpc-go-xd-332d40dd): bash verify/repro/c3_context_rule.sh   # exit 1 == the scripts/vet.sh test-context rule rejects the tree
#
# Audit repro for claim C3: executes the bare-context rule from scripts/vet.sh
# verbatim (using the repo's own `not` helper from scripts/common.sh) against
# the working tree, then against the base commit, and prints the offending
# lines. The rule fails (exit 1) whenever `git grep` finds a bare
# context.Background()/context.TODO() in a *_test.go file that is not wrapped in
# context.WithTimeout( / context.WithCancel(.
set -u
cd "$(git rev-parse --show-toplevel)"
source scripts/common.sh

echo "== added ClientSideTLSConfig call(s) in balancer_test.go:"
grep -n 'ClientSideTLSConfig(context.Background()' internal/xds/balancer/clusterimpl/balancer_test.go || true

echo "== scripts/vet.sh context rule on the working tree:"
# scripts/vet.sh line 80, verbatim:
git grep -e 'context.Background()' --or -e 'context.TODO()' -- "*_test.go" | grep -v "benchmark/primitives/context_test.go" | grep -v 'context.WithTimeout(' | not grep -v 'context.WithCancel('
echo "RULE_EXIT=$?"

BASE="${BASE_COMMIT:-cc234554fb363aea445a838b341bb8a65c8305b0}"
echo "== same rule on the base commit $BASE:"
git grep -e 'context.Background()' --or -e 'context.TODO()' "$BASE" -- "*_test.go" | grep -v "benchmark/primitives/context_test.go" | grep -v 'context.WithTimeout(' | not grep -v 'context.WithCancel('
echo "BASE_RULE_EXIT=$?"
