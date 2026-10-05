#!/bin/bash
# Run from a checkout of evalon/grpc-go-tr-73225c34: bash <path>/verify/repro/c1/run_c1.sh   (applies each production-code patch in turn, runs the branch's own tests, restores the tree)
set -u
HERE="$(cd "$(dirname "$0")" && pwd)"
for p in trace_err_put mutation1_error_overtakes_pending mutation2_error_dropped_while_buffering mutation3_error_discards_pending; do
  echo "=================== $p"
  git apply "$HERE/$p.patch" || exit 1
  go test -count=1 -timeout 120s -v -run 'Test/(ClientReceivesManyTinyFrames|RecvBufferCompactionPreservesByteStream|RecvBufferCompactionTinyFramesMemory)$' ./internal/transport 2>&1 \
    | grep -E 'VERIFY-C1|^\s*(---|ok|FAIL|PASS)|_test.go|panic' | head -30
  git checkout -- internal/transport/transport.go
done
