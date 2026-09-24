#!/bin/bash
# Run: verify/repro/c1_callback_after_signal.sh   (from repo root; baseline + mutation where child B's synchronous ExitIdle cc.UpdateState callback cannot finish until A's batch ends — the cross-child tests still PASS)
d=$(dirname "$0")
echo "== 6fac7ab6 baseline";  "$d/run_mutation.sh" evalon/grpc-go-en-6fac7ab6 none TestEndpointShardingExitIdleWhileOtherChildUpdateBlocked 1
echo "== 6fac7ab6 mutated";   "$d/run_mutation.sh" evalon/grpc-go-en-6fac7ab6 verify/repro/c1_6fac7ab6.patch TestEndpointShardingExitIdleWhileOtherChildUpdateBlocked 3
echo "== 1d4033f5 baseline";  "$d/run_mutation.sh" evalon/grpc-go-en-1d4033f5 none TestEndpointShardingChildExitIdleDuringUnrelatedChildUpdate 1
echo "== 1d4033f5 mutated";   "$d/run_mutation.sh" evalon/grpc-go-en-1d4033f5 verify/repro/c1_1d4033f5.patch TestEndpointShardingChildExitIdleDuringUnrelatedChildUpdate 3
