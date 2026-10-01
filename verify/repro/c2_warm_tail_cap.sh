#!/bin/sh
# Run: sh verify/repro/c2_warm_tail_cap.sh   (from anywhere inside the repo; needs go + network access to the claim repo; ~1 min)
# C2: after a 16,384-tiny-frame warm-up, each tiny frame of alternating tiny/2-KiB traffic gets its own 16 KiB destination because nextTailCap is only reset when the backlog empties.
. "$(git rev-parse --show-toplevel)/verify/repro/_common.sh"
claim_worktree evalon/grpc-go-tr-86436e27

echo "== 1. warmed vs matched fresh buffer, allocation tracing, counterfactual, fixture bound replay"
cp "$REPRO/c2_warm_tail_cap_test.go" internal/transport/zz_verify_c2_test.go
go test -tags verify_repro -run 'TestVerifyC2' -v ./internal/transport -count=1 2>&1 | cut -c1-420 || true

echo "== 2. the eval fixture (byte-exact from eval_tests.zip, pass its path as FIXTURE=...) on this branch"
if [ -n "${FIXTURE:-}" ]; then
	run sha256sum "$FIXTURE"
	cp "$FIXTURE" internal/transport/eval_recv_buffer_compaction_test.go
	go test -run '^TestEval_' -v ./internal/transport -race -count=1 2>&1 | grep -E '^(--- |ok|FAIL|PASS)' || true
else
	echo "(skipped: FIXTURE not set)"
fi
