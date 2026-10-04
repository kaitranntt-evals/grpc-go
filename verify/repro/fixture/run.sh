#!/bin/bash
# Run: verify/repro/fixture/run.sh <worktree> [rename]   — runs the supplied eval fixture (byte-exact copy next to this script) on a claim branch.
# "rename" renames the fixture's newTestRecvBuffer helper (needed on 136a5793, whose own recv_buffer_test.go declares the same name).
set -u
wt=$1; here=$(cd "$(dirname "$0")" && pwd); cd "$wt" || exit 1
rm -f internal/transport/eval_renamed_test.go
dst=internal/transport/eval_recv_buffer_compaction_test.go
cp "$here/eval_recv_buffer_compaction_test.go.txt" $dst
f() { grep -E '^\s*(--- (FAIL|PASS|SKIP)|ok|FAIL|PASS)|eval_recv_buffer_compaction_test.go|redeclared|build failed'; }
echo '$ go test -v -run '"'"'^TestEval_'"'"' google.golang.org/grpc/internal/transport -race -count=1'
go test -v -run '^TestEval_' google.golang.org/grpc/internal/transport -race -count=1 2>&1 | f
if [ "${2:-}" = rename ]; then
  sed -i 's/newTestRecvBuffer/evalNewTestRecvBuffer/g' $dst
  echo '$ (after sed -i s/newTestRecvBuffer/evalNewTestRecvBuffer/g on the fixture copy) same command'
  go test -v -run '^TestEval_' google.golang.org/grpc/internal/transport -race -count=1 2>&1 | f
fi
echo '$ GRPC_GO_EXPERIMENTAL_ENABLE_RECEIVE_BUFFER_COMPACTION=false go test -v -run '"'"'^TestEval_RecvBufferCompactionDisabled$'"'"' google.golang.org/grpc/internal/transport -race -count=1'
GRPC_GO_EXPERIMENTAL_ENABLE_RECEIVE_BUFFER_COMPACTION=false go test -v -run '^TestEval_RecvBufferCompactionDisabled$' google.golang.org/grpc/internal/transport -race -count=1 2>&1 | f
rm -f $dst; git status --short
