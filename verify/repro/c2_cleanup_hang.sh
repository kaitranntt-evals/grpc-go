#!/bin/bash
# Run: verify/repro/c2_cleanup_hang.sh   (from repo root; injects an idle-exit/batch coupling defect (M2) per branch; "process_timeout=1" plus a Close/close frame = cleanup hung after the test's own timeout)
d=$(dirname "$0")
while read -r b t; do
  echo "== $b $t"; "$d/run_mutation.sh" evalon/grpc-go-en-$b verify/repro/c2_m2_$b.patch "$t" 4
done <<'L'
abc06f48 TestEndpointShardingExitIdleNotBlockedByOtherChild
4d9a9b72 TestExitIdleNotBlockedByOtherChildUpdate
9d0e1576 TestEndpointSharding_ExitIdleNotBlockedByOtherChildUpdate
1e3912e1 TestExitIdleIndependentOfOtherChildUpdate
7c856312 TestExitIdleDuringOtherChildUpdate
a32002ab TestEndpointShardingExitIdleDuringOtherChildUpdate
2e629b7c TestEndpointShardingIndependentExitIdle
895e4fc3 TestChildExitIdleDuringAnotherChildUpdate
525ac3bd TestExitIdleDuringOtherChildUpdate
6dbb257d TestEndpointShardingExitIdleDuringBlockedUpdate
L
echo "== 6dbb257d permanently stuck idle-exit worker, forced [B,A] order"; "$d/run_mutation.sh" evalon/grpc-go-en-6dbb257d verify/repro/c2_m3m6_6dbb257d.patch TestEndpointShardingExitIdleDuringBlockedUpdate 3
