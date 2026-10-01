#!/bin/sh
# Run: sh verify/repro/c3_heap_delta.sh   (from anywhere inside the repo; needs go + network access to the claim repo; ~1 min)
# C3: the branch's memory tests compute `growth := heapAllocBytes() - before` on uint64 process-wide samples; a lower second sample wraps to ~1.8e19 and trips the upper bound.
. "$(git rev-parse --show-toplevel)/verify/repro/_common.sh"
claim_worktree evalon/grpc-go-tr-8401755a

echo "== 0. the arithmetic under test"
run grep -n 'heapAllocBytes()\|maxGrowth :=\|func heapAllocBytes\|return ms.HeapAlloc' internal/transport/recv_buffer_test.go internal/transport/transport_test.go

echo "== 1. baseline: both memory tests pass on the unmodified branch"
go test -v -run 'Test/(RecvBuffer_CompactsTinyPayloads|ServerReceiveBufferCompaction_ManyTinyDataFrames)$' ./internal/transport -count=1 2>&1 | grep -E 'eap grew|^(ok|FAIL)|--- (PASS|FAIL)' || true

echo "== 2. natural trigger, branch files unmodified: 8 MiB of unrelated heap is freed between the two samples"
cp "$REPRO/c3_natural_heap_drop_test.go" internal/transport/zz_verify_c3_natural_test.go
run git status --short
go test -tags verify_repro -run 'TestVerifyC3Natural' -v ./internal/transport -count=1 2>&1 | grep -v '^=== ' || true

echo "== 3. injected samples through a verify-only hook in heapAllocBytes()"
git apply "$REPRO/c3_inject_heap_samples.patch"
cp "$REPRO/c3_injected_samples_test.go" internal/transport/zz_verify_c3_inject_test.go
run git diff --stat
go test -tags verify_repro,verify_c3_inject -run 'TestVerifyC3Inject' -v ./internal/transport -count=1 2>&1 | grep -v '^=== ' || true

echo "== 4. same injection with a real regression present (c3_disable_compaction.patch: queued tiny payloads are never compacted)"
git apply "$REPRO/c3_disable_compaction.patch"
run git diff --stat
go test -tags verify_repro,verify_c3_inject -run 'TestVerifyC3Inject/.*/(control_no_injection|both_samples_invalid_zero)$' -v ./internal/transport -count=1 2>&1 | grep -E 'eap grew|Draining|OBSERVED.*(control_no_injection|both_samples_invalid_zero)|^\s+--- |^(ok|FAIL)\s' || true
git checkout -q internal/transport/transport.go internal/transport/recv_buffer_test.go

echo "== 5. unmodified branch, whole package, ${FULL_RUNS:-0} runs (set FULL_RUNS=12 to replay; ~11 s per run)"
rm -f internal/transport/zz_verify_c3_*.go
run git status --short
i=1
while [ "$i" -le "${FULL_RUNS:-0}" ]; do
	echo "run $i"
	go test ./internal/transport -count=1 -v 2>&1 | grep -E 'eap grew|^(ok|FAIL)\s' || true
	i=$((i + 1))
done
