#!/usr/bin/env bash
# Run from the grpc-go repo root: bash verify/repro/c3/run.sh   (needs remote `evalrepo` = https://github.com/kaitranntt-evals/grpc-go-server-unify-unary-stream-rpc.git)
#
# Shows that server.go on evalon/grpc-go-se-b4918163 still says
# "See comment in processUnaryRPC on defers." inside processRPC while no
# processUnaryRPC function and no defer-rationale text (stack usage / ~56-64
# bytes / ordering of tracing, stats, channelz) exist anywhere in the branch.
# Exit status 1 == stale reference confirmed.
set -uo pipefail
REPO_ROOT="$(cd "$(dirname "$0")/../../.." && pwd)"
B=evalon/grpc-go-se-b4918163
BASE=0c51461d27177d997e14c642fe18c11668fc09a3
git -C "$REPO_ROOT" fetch -q evalrepo "$B"
cd "$REPO_ROOT"

echo "--- cross-reference in processRPC on $B:"
git grep -n 'See comment in processUnaryRPC' "evalrepo/$B" -- server.go
echo "--- func processUnaryRPC definitions on $B (expect none):"
git grep -n 'func (s \*Server) processUnaryRPC' "evalrepo/$B" -- '*.go' || echo "(none)"
echo "--- defer-rationale text on $B (expect none):"
git grep -n -i -E 'reduce stack usage|56-64|stack re-allocation|tracing first, stats' "evalrepo/$B" -- '*.go' || echo "(none)"
echo "--- the same rationale at base $BASE (what the reference used to point at):"
git grep -n -E 'reduce stack usage|56-64|tracing first, stats' "$BASE" -- server.go

stale=0
git grep -q 'See comment in processUnaryRPC' "evalrepo/$B" -- server.go && \
  ! git grep -q 'func (s \*Server) processUnaryRPC' "evalrepo/$B" -- '*.go' && \
  ! git grep -q -i -E 'reduce stack usage|56-64|stack re-allocation' "evalrepo/$B" -- '*.go' && stale=1
if [ "$stale" = 1 ]; then echo "RESULT: stale processUnaryRPC defer-rationale reference CONFIRMED"; exit 1; fi
echo "RESULT: reference resolves (not stale)"
