#!/usr/bin/env bash
# Run: bash verify/repro/c9_no_compressed_oversize_test.sh  (needs worktree of evalrepo branch evalon/grpc-go-se-c02bf7c2 and the eval fixtures)
# Inventories the solution's own tests on the claim branch and shows none exercises a compressed oversize RPC asserting ResourceExhausted.
set -euo pipefail
cd "$(git rev-parse --show-toplevel)"
git worktree add /tmp/c9-branch evalrepo/evalon/grpc-go-se-c02bf7c2 --detach -q 2>/dev/null || true
cd /tmp/c9-branch
mkdir -p .evaltools && cp ~/eval_tests/tests/candidate_test_inventory.go .evaltools/
cp ~/eval_tests/tests/run_candidate_tests.sh test/
echo "=== candidate test inventory + execution ==="
bash ./test/run_candidate_tests.sh || true   # exits 1 only because the fixture copy itself is untracked-but-not-a-test
echo "=== compression / size-limit references in the sole candidate test file ==="
grep -c "Compress\|gzip\|RecvMsgSize\|ResourceExhausted" test/server_test.go || echo "0 matches"
