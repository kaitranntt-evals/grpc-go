#!/usr/bin/env bash
# Repro for C6: run the scripts/vet.sh test-context rule (line 80) on branch evalon/grpc-go-tr-c70b9e5f. Run from a checkout of that branch: bash verify/repro/c6_c70b9e5f_vet_context_rule.sh
# Exit status 1 == the rule reports a violation (vet.sh wraps this pipeline in `not`, so any output fails the script).
set -u
echo "== scripts/vet.sh rule:"; sed -n 78,80p scripts/vet.sh
echo "== violations:"
out=$(git grep -e 'context.Background()' --or -e 'context.TODO()' -- "*_test.go" | grep -v "benchmark/primitives/context_test.go" | grep -v 'context.WithTimeout(' | grep -v 'context.WithCancel(')
if [ -n "$out" ]; then echo "$out"; echo "RESULT: vet.sh context rule FAILS"; exit 1; fi
echo "RESULT: no violations"
