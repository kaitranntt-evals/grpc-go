#!/usr/bin/env bash
# Repro for C9: run from a checkout of branch evalon/grpc-go-se-c02bf7c2 with the eval fixtures
# (.evaltools/candidate_test_inventory.go and test/run_candidate_tests.sh) in place: bash verify/repro/repro_c9.sh
# Inventories and runs the solution-authored tests: the only test is the
# success-path TestServerInterceptorSegregation; no failure path is exercised.
set -euo pipefail
cd "$(git rev-parse --show-toplevel)"
bash test/run_candidate_tests.sh
git diff --name-only 0c51461d27177d997e14c642fe18c11668fc09a3 HEAD -- '*_test.go' '**/*_test.go'
