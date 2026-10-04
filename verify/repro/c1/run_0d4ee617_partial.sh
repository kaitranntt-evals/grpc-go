#!/bin/bash
# Run: verify/repro/c1/run_0d4ee617_partial.sh ~/wt/0d4ee617   (clean worktree of evalon/grpc-go-tr-0d4ee617)
# Does the 3-buffer "outstanding pooled buffers = 1" assertion in TestReceiveBufferCompactionOwnership bound enabled-mode
# memory? Mutant: recvBufferCompactionLimit 4096 -> 11 (chunks never exceed 11 bytes). (D1) run the added tests;
# (D2) same mutant + nonpositive enabled-mode heap sample (ballast).
set -u
wt=$1; here=$(cd "$(dirname "$0")" && pwd); cd "$wt" || exit 1
tf=internal/transport/recv_buffer_test.go
run() { echo "### $1"; go test google.golang.org/grpc/internal/transport -race -count=1 -v -run '^Test$/^(ReceiveBufferTinyFrames|ReceiveBufferCompactionConcurrent|ReceiveBufferCompactionOwnership)$' 2>&1 | grep -E '^\s*(--- (FAIL|PASS)|ok|FAIL|PASS)|retain|outstanding|\.go:[0-9]+:[0-9]+:'; }
sed -i 's/^const recvBufferCompactionLimit = 4 \* 1024$/const recvBufferCompactionLimit = 11/' internal/transport/transport.go
git diff -U0 -- internal/transport/transport.go | grep -E '^[+-][^+-]'
run "D1: compaction chunks capped at 11 bytes"
cp "$here/zz_c1_ballast_test.go.txt" internal/transport/zz_c1_ballast_test.go
sed -i -E 's/runtime\.ReadMemStats\(&before\)/c1ReadBefore(\&before)/' "$tf"
git diff -U0 -- "$tf" | grep -E '^[+-][^+-]'
run "D2: D1 + enabled-mode heap sample made nonpositive (ballast released between snapshots)"
git checkout -q -- . && rm -f internal/transport/zz_c1_ballast_test.go; git status --short
