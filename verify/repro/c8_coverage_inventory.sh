# Run: bash verify/repro/c8_coverage_inventory.sh <path-to-worktree-on-branch-evalon/grpc-go-se-ac1d7e1c>
# C8: inventories the tests the solution committed (diff vs the base commit),
# runs the solution's committed test, and shows the only decompression-limit
# test in the tree is unary — no compressed full-duplex request over
# MaxRecvMsgSize is asserted anywhere.
set -x
WT="${1:?worktree path}"
BASE=0c51461d27177d997e14c642fe18c11668fc09a3
cd "$WT"
git diff --name-only "$(git merge-base HEAD $BASE)"..HEAD -- '*_test.go'
git diff "$(git merge-base HEAD $BASE)"..HEAD -- '*_test.go' | grep -E '^\+func '
grep -n "Compress\|MaxRecv\|ResourceExhausted" server_ext_test.go || echo "committed test uses no compression/size-limit assertions"
grep -rn "TestStreamingDecompressionExceedsMaxMessageSize" encoding/ || echo "no streaming decompression-limit test exists"
grep -n -A3 "func (s) TestDecompressionExceedsMaxMessageSize" encoding/compressor_test.go
go test . -run 'Test/^Server_UnifiedRPCPipeline$' -v -count=1 | grep -E "^(=== RUN|--- |ok|FAIL)"
