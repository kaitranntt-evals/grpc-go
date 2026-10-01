#!/bin/sh
# Run: sh verify/repro/c4_three_one_byte_frames.sh   (from anywhere inside the repo; needs go + network access to the claim repo; ~1 min)
# C4: on a fresh recvBuffer the third unread one-byte frame makes compact() allocate a 4 KiB compaction buffer, so 3 payload bytes retain 4,097 bytes of backing capacity.
. "$(git rev-parse --show-toplevel)/verify/repro/_common.sh"
claim_worktree evalon/grpc-go-tr-ad9c633e

echo "== 1. retained capacity after three unread one-byte frames (component level, production handleData path, live heap)"
cp "$REPRO/c4_three_one_byte_frames_test.go" internal/transport/zz_verify_c4_test.go
go test -tags verify_repro -run 'TestVerifyC4' -v ./internal/transport -count=1 2>&1 || true

echo "== 2. the eval fixture (byte-exact from eval_tests.zip, pass its path as FIXTURE=...) on this branch"
if [ -n "${FIXTURE:-}" ]; then
	run sha256sum "$FIXTURE"
	cp "$FIXTURE" internal/transport/eval_recv_buffer_compaction_test.go
	go test -run '^TestEval_' -v ./internal/transport -race -count=1 2>&1 | grep -E '^(--- |ok|FAIL|PASS)' || true
else
	echo "(skipped: FIXTURE not set)"
fi
