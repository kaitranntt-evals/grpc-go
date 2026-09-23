#!/bin/bash
# Repro for claim C2 (run v-618a6113): from the root of a checkout of claims/evalon/grpc-go-xd-d0190a30, run: bash verify/repro/c2_vet_timeout_context_predicate.sh  (exit 1 + two printed lines = predicate rejects them)
# Runs the timeout-context predicate from scripts/vet.sh (line 80) verbatim, with common.sh's `not`.
set -o pipefail
source ./scripts/common.sh
git grep -e 'context.Background()' --or -e 'context.TODO()' -- "*_test.go" | grep -v "benchmark/primitives/context_test.go" | grep -v 'context.WithTimeout(' | not grep -v 'context.WithCancel('
echo "predicate exit=$?"
