#!/usr/bin/env bash
# Run (in a checkout of evalon/grpc-go-se-604865b6): bash verify/repro/c9_duplicate_end2end_cases.sh
#
# C9: shows the pre-existing test and the added case that share setup
# (stubserver + grpc.MaxRecvMsgSize), stimulus (unary RPC whose compressed
# request decompresses above the limit) and assertion (status code
# ResourceExhausted), then runs both so the duplicate coverage is observed.
# Also prints the two added files' same-named handwritten-descriptor cases,
# which duplicate each other inside the change set.
set -euo pipefail
cd "$(git rev-parse --show-toplevel)"

echo "==> PAIR 1: pre-existing encoding.TestDecompressionExceedsMaxMessageSize"
awk '/^func \(s\) TestDecompressionExceedsMaxMessageSize/,/^}/' encoding/compressor_test.go | grep -n 'StubServer\|MaxRecvMsgSize\|UnaryCall(\|codes.ResourceExhausted' | sed 's/^/    /'
echo "==> PAIR 1: added test.TestServerPipeline_MaxReceiveMessageSizeAfterDecompression (unary/oversized after decompression)"
awk '/^func \(s\) TestServerPipeline_MaxReceiveMessageSizeAfterDecompression/,/^}/' test/server_pipeline_test.go | grep -n 'StubServer\|MaxRecvMsgSize\|UnaryCall(\|codes.ResourceExhausted' | sed 's/^/    /'

echo "==> PAIR 2 (inside the change set): same-name handwritten-descriptor cases in both added files"
grep -n 'same name\|without streaming flags\|wins over\|stays streaming\|precedence\|flagless' server_rpc_ext_test.go test/server_pipeline_test.go | sed 's/^/    /'

echo "==> running the pre-existing test and the added cases"
go test ./encoding -run '^Test$/^DecompressionExceedsMaxMessageSize$' -count=1 -v 2>&1 | grep -E '^(---|ok|FAIL|\s+---)'
go test ./test -run '^Test$/^ServerPipeline_MaxReceiveMessageSizeAfterDecompression$' -count=1 -v 2>&1 | grep -E '^(---|ok|FAIL|\s+---)'
go test . -run '^Test$/^Server_(HandwrittenServiceDesc|RecvLimitAppliesAfterDecompression)$' -count=1 -v 2>&1 | grep -E '^(---|ok|FAIL|\s+---)'
go test ./test -run '^Test$/^ServerPipeline_HandwrittenServiceDesc$' -count=1 -v 2>&1 | grep -E '^(---|ok|FAIL|\s+---)'
