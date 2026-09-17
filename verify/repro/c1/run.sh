#!/usr/bin/env bash
# Run from the grpc-go repo root: bash verify/repro/c1/run.sh [branch-suffix ...]  (default: all six C1 branches; needs remote `evalrepo` = https://github.com/kaitranntt-evals/grpc-go-server-unify-unary-stream-rpc.git)
#
# For each branch it creates a throwaway worktree, applies the one-line
# instrumentation patch (forces ss.Start to fail / counts waitForEnd polls),
# drops in the audit test, and runs it.  A FAIL from the audit test is the
# finding (a grpc.Server created by the fixture is never stopped); for
# e9ae7b4d the finding is the "AUDIT waitForEnd: returned after N poll
# iteration(s)" lines with N > 1.
set -euo pipefail
REPO_ROOT="$(cd "$(dirname "$0")/../../.." && pwd)"
HERE="$REPO_ROOT/verify/repro/c1"
branches=("$@")
[ ${#branches[@]} -gt 0 ] || branches=(e9ae7b4d d61f8cad 0e6bd0f2 295197d7 2c2de906 e2d06da9)

for b in "${branches[@]}"; do
  wt="$(mktemp -d)/wt-$b"
  echo "==================== $b"
  git -C "$REPO_ROOT" fetch -q evalrepo "evalon/grpc-go-se-$b"
  git -C "$REPO_ROOT" worktree add -q --detach "$wt" "evalrepo/evalon/grpc-go-se-$b"
  (
    cd "$wt"
    case "$b" in
      e9ae7b4d)
        git apply "$HERE/e9ae7b4d_instrument.patch"
        go test ./test -run 'Test/ServerPipeline_RPCTypes' -count=1 -v 2>&1 | grep -E 'AUDIT|^ok|^FAIL'
        ;;
      d61f8cad|2c2de906)
        git apply "$HERE/${b}_instrument.patch"
        cp "$HERE/${b}_audit_test.go.txt" test/audit_c1_test.go
        go test ./test -run 'Test/AuditC1' -count=1 -v 2>&1 | grep -E 'audit_c1_test|_test.go:[0-9]+: net.Listen|^--- |^ok|^FAIL' || true
        ;;
      0e6bd0f2)
        cp "$HERE/${b}_audit_test.go.txt" audit_c1_test.go
        go test . -run 'Test/AuditC1' -count=1 -v 2>&1 | grep -E 'audit_c1_test|^--- |^ok|^FAIL' || true
        ;;
      295197d7|e2d06da9)
        git apply "$HERE/${b}_instrument.patch"
        cp "$HERE/${b}_audit_test.go.txt" audit_c1_test.go
        go test . -run 'Test/AuditC1' -count=1 -v 2>&1 | grep -E 'audit_c1_test|_test.go:[0-9]+: net.Listen|^--- |^ok|^FAIL' || true
        ;;
    esac
  )
  git -C "$REPO_ROOT" worktree remove --force "$wt"
done
