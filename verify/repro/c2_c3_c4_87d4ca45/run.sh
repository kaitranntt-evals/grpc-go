#!/bin/bash
# Run from a checkout of branch evalon/grpc-go-tr-87d4ca45: bash <path>/verify/repro/c2_c3_c4_87d4ca45/run.sh   (copies the byte-exact eval fixture into internal/transport, runs the three eval checks, removes the copy)
# The fixture's initRecvBufferForTest only recognises init(mem.BufferPool) / init(); this branch's recvBuffer.init(compact bool)
# matches neither, so the buffer is left at its zero value (compact=false) and never compacts -> backlog 1025/1073/1123 entries.
set -u
D="$(cd "$(dirname "$0")" && pwd)"
cp "$D/eval_recv_buffer_compaction_test.go.txt" internal/transport/eval_recv_buffer_compaction_test.go
trap 'rm -f internal/transport/eval_recv_buffer_compaction_test.go' EXIT
go test -v -run '^TestEval_RecvBufferCompaction$' google.golang.org/grpc/internal/transport -race -count=1
go test -v -run '^TestEval_RecvBufferCompaction_MixedFrames$' google.golang.org/grpc/internal/transport -race -count=1
go test -v -run '^TestEval_RecvBufferCompaction_MultiCycleMemoryBound$' google.golang.org/grpc/internal/transport -race -count=1
