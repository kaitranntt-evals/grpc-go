#!/bin/bash
# Run from a checkout of branch evalon/grpc-go-tr-60f32c9f: bash <path>/verify/repro/c12/run.sh   (replays the test-context rule from scripts/vet.sh; exit 1 == the rule rejects the file)
not() { ! "$@"; }
git grep -e 'context.Background()' --or -e 'context.TODO()' -- "*_test.go" |
  grep -v "benchmark/primitives/context_test.go" |
  grep -v 'context.WithTimeout(' |
  grep -v 'context.WithCancel(' |
  not grep -v 'context.WithCancel('
echo "exit=$?"
