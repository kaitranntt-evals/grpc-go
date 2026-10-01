#!/bin/bash
# Run (from the root of the checkout to test): bash <path>/verify/repro/c8_vet_context.sh
# Runs the "context usages are done with timeout" check of scripts/vet.sh verbatim, using the repo's own helpers
# (scripts/common.sh: not/fail_on_output). Prints the offending lines and exits 1 when the check fails.
set -eo pipefail
source scripts/common.sh
git grep -e 'context.Background()' --or -e 'context.TODO()' -- "*_test.go" | grep -v "benchmark/primitives/context_test.go" | grep -v 'context.WithTimeout(' | not grep -v 'context.WithCancel('
echo "vet context check: PASSED"
