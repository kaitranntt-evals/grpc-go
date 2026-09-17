#!/usr/bin/env bash
# Run from the grpc-go repo root: bash verify/repro/c2/run.sh [093900cf|307e9c8d ...]  (default: both; needs remote `evalrepo` = https://github.com/kaitranntt-evals/grpc-go-server-unify-unary-stream-rpc.git)
#
# 093900cf: the mutation makes the unary handler fail with FailedPrecondition and
#           lets only the terminal-status assertion judge the outcome; the test
#           still PASSES because `status.Code(err)` reads the NewStream() error.
# 307e9c8d: first run is the branch's own test with logging only -- the server
#           RecvMsg already SUCCEEDS; second run mutates the receive to the
#           correct message type so it must succeed -- the "receive failure"
#           subtest still PASSES.
set -euo pipefail
REPO_ROOT="$(cd "$(dirname "$0")/../../.." && pwd)"
HERE="$REPO_ROOT/verify/repro/c2"
branches=("$@")
[ ${#branches[@]} -gt 0 ] || branches=(093900cf 307e9c8d)

for b in "${branches[@]}"; do
  wt="$(mktemp -d)/wt-$b"
  echo "==================== $b"
  git -C "$REPO_ROOT" fetch -q evalrepo "evalon/grpc-go-se-$b"
  git -C "$REPO_ROOT" worktree add -q --detach "$wt" "evalrepo/evalon/grpc-go-se-$b"
  (
    cd "$wt"
    case "$b" in
      093900cf)
        git apply "$HERE/093900cf_mutation.patch"
        go test ./test -run 'Test/ServerPipeline_UnaryRespondsWithoutHalfClose' -count=1 -v 2>&1 | grep -E 'AUDIT|^--- |^ok|^FAIL'
        ;;
      307e9c8d)
        # logging-only hunks first (everything except the type swap)
        git apply --include='test/server_rpc_pipeline_test.go' "$HERE/307e9c8d_mutation.patch"
        sed -i 's/stream.RecvMsg(&testpb.StreamingInputCallRequest{}); err != nil { \/\/ AUDIT mutation: correct type, receive succeeds/stream.RecvMsg(\&testpb.StreamingOutputCallRequest{}); err != nil {/' test/server_rpc_pipeline_test.go
        echo "--- branch test as written (logging only):"
        go test ./test -run 'Test/ServerRPCPipeline_StreamingFailures/receive_failure' -count=1 -v 2>&1 | grep -E 'AUDIT|^--- |^ok|^FAIL'
        git checkout -q -- test/server_rpc_pipeline_test.go
        git apply "$HERE/307e9c8d_mutation.patch"
        echo "--- mutated so the server receive must succeed:"
        go test ./test -run 'Test/ServerRPCPipeline_StreamingFailures/receive_failure' -count=1 -v 2>&1 | grep -E 'AUDIT|^--- |^ok|^FAIL'
        ;;
    esac
  )
  git -C "$REPO_ROOT" worktree remove --force "$wt"
done
