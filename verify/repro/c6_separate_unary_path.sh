#!/usr/bin/env bash
# Run: bash verify/repro/c6_separate_unary_path.sh  (needs worktree of evalrepo branch evalon/grpc-go-se-c6b55476 and the eval fixtures)
# Shows the distinct unary registry + unary-specific dispatch branch on the claim branch, then exercises both RPC kinds via the eval fixtures.
set -euo pipefail
cd "$(git rev-parse --show-toplevel)"
git worktree add /tmp/c6-branch evalrepo/evalon/grpc-go-se-c6b55476 --detach -q 2>/dev/null || true
cd /tmp/c6-branch
echo "=== serviceInfo keeps two registries ==="
grep -n "methods     map\[string\]\*MethodDesc\|streams     map\[string\]\*StreamDesc" server.go
echo "=== dispatch keeps a unary-specific lookup branch ==="
grep -n "srv.methods\[method\]\|srv.streams\[method\]" server.go
echo "=== exercising both RPC kinds via eval fixtures ==="
cp ~/eval_tests/tests/eval_unary_roundtrip_test.go ~/eval_tests/tests/eval_interceptor_segregation_test.go test/
(cd test && go test -v -run 'Test(Eval_UnaryRoundTrip|Eval_InterceptorSegregation)' -count=1 .)
