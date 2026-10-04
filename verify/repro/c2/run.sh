#!/bin/bash
# Run: verify/repro/c2/run.sh ~/wt/1ee92409   (clean worktree of evalon/grpc-go-tr-1ee92409)
# (1) probe of the helper stream's reader; (2) drop one byte in production compaction and run the branch's own tests
# with a 30s go-test timeout: the helper-stream read hangs until the global timeout; the ctx-bound one fails locally.
set -u
wt=$1; here=$(cd "$(dirname "$0")" && pwd); cd "$wt" || exit 1
cp "$here/c2_probe_test.go.txt" internal/transport/zz_c2_probe_test.go
echo "### 1: probe"; go test google.golang.org/grpc/internal/transport -count=1 -v -run '^TestC2Probe_' 2>&1 | grep -vE '^=== '
rm internal/transport/zz_c2_probe_test.go
sed -i 's|^\tb.compacted = append(b.compacted, buf.ReadOnlyData()...)$|\td := buf.ReadOnlyData()\n\tif len(b.compacted) == 4096 {\n\t\td = d[:0] // C2 MUTATION: lose one payload\n\t}\n\tb.compacted = append(b.compacted, d...)|' internal/transport/transport.go
git diff -U0 -- internal/transport/transport.go | grep -E '^[+-][^+-]'
echo "### 2a: TestRecvBuffer_CompactionReducesMemory with a lost byte (-timeout 30s)"
( time go test google.golang.org/grpc/internal/transport -count=1 -timeout 30s -run '^Test$/^RecvBuffer_CompactionReducesMemory$' 2>&1 | grep -E 'panic: test timed out|running tests|RecvBuffer_CompactionReducesMemory|recv_buffer_compaction_test.go|recvBufferReader\)\.read|readTo|^FAIL|^ok' | head -20 ) 2>&1
echo "### 2b: TestClientTransport_ManyTinyDataFrames/compaction=true with a lost byte (-timeout 30s)"
( time go test google.golang.org/grpc/internal/transport -count=1 -timeout 30s -v -run '^Test$/^ClientTransport_ManyTinyDataFrames$/compaction=true' 2>&1 | grep -E 'panic: test timed out|recv_buffer_compaction_test.go|--- |^FAIL|^ok' | head -20 ) 2>&1
git checkout -q -- .; git status --short
