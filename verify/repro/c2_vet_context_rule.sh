#!/usr/bin/env bash
# Run: bash verify/repro/c2_vet_context_rule.sh   (from a checkout of evalon/grpc-go-xd-d0190a30; prints the vet.sh test-context rule violations, exits 1 if any)
set -u
git grep -n -e 'context.Background()' --or -e 'context.TODO()' -- '*_test.go' \
  | grep -v benchmark/primitives/context_test.go \
  | grep -v 'context.WithTimeout(' \
  | grep -v 'context.WithCancel(' && exit 1
echo "no violations"
