#!/usr/bin/env bash
# Run: bash verify/repro/c5_a0b58cfd_fresh_tagrpc_panic.sh <worktree of evalon/grpc-go-se-a0b58cfd> <path to tests/eval_stats_context_encode_test.go>
# Copies the eval fixture (stats handler whose TagRPC returns context.Background(), codec whose
# Marshal fails) into test/ and runs the unary and streaming cases separately. On this branch the
# server process panics in serverStream.SendMsg's encode-failure diagnostic
# (serverFromContext(ss.ctx).channelz with no *Server in the fresh context).
set -uo pipefail
WT=$1; FIXTURE=$2
cd "$WT"
echo "== diagnostic site:"; grep -n "serverFromContext(ss.ctx)" stream.go
echo "== context plumbing in handleStream (TagRPC result replaces ctx):"; grep -n "contextWithServer(ctx, s)\|TagRPC(ctx" server.go
cp "$FIXTURE" test/eval_stats_context_encode_test.go
for t in TestEval_StatsHandlerFreshContextEncode TestEval_StatsHandlerFreshContextEncodeStreaming; do
  echo "=== $t"
  go test -count=1 -v ./test -run "^$t\$" 2>&1 | grep -v "tlogger.go" | grep -E -A12 "^panic:" | head -16
  go test -count=1 -v ./test -run "^$t\$" 2>&1 | grep -E "^(ok|FAIL|--- )"
done
rm -f test/eval_stats_context_encode_test.go
git status --short
