#!/usr/bin/env bash
# Run: bash verify/repro/c4_binlog_empty_payload.sh  (needs worktree of evalrepo branch evalon/grpc-go-se-b9676765)
# Runs the pre-existing GCP observability binary-log test on the claim branch and on the base commit.
set -euo pipefail
cd "$(git rev-parse --show-toplevel)"
git worktree add /tmp/c4-branch evalrepo/evalon/grpc-go-se-b9676765 --detach -q 2>/dev/null || true
git worktree add /tmp/c4-base 0c51461d27177d997e14c642fe18c11668fc09a3 --detach -q 2>/dev/null || true
echo "=== branch evalon/grpc-go-se-b9676765 (expect FAIL: Message logged as nil, want []uint8{}) ==="
(cd /tmp/c4-branch/gcp/observability && go test -run 'Test/ServerRPCEventsLogAll' -count=1 . || true)
echo "=== base 0c51461d (expect PASS) ==="
(cd /tmp/c4-base/gcp/observability && go test -run 'Test/ServerRPCEventsLogAll' -count=1 .)
