#!/bin/bash
# Run (C1): verify/repro/c1_stall/run_c1.sh <checkout-of-claim-branch> <SubtestName> <update:N|exitidle:N>
#   e.g. run_c1.sh ~/wt/d5658fb5 ChildExitIdleDuringOtherChildUpdate update:2
# Injects a permanent stall (while the per-child lock is held) into the Nth UpdateClientConnState / ExitIdle
# call of a child, then runs the branch's own subtest with a 40s binary timeout. "panic: test timed out" with the
# test goroutine under runtime.Goexit -> (*endpointSharding).Close / (*balancerWrapper).close means failure-path
# cleanup blocked on the stalled worker's lock.
set -e
here=$(cd "$(dirname "$0")" && pwd)
cd "$1"
cp "$here/verify_stall.go.txt" balancer/endpointsharding/verify_stall.go
cp "$here/ptr.go.txt" balancer/endpointsharding/ptr.go
sed -i 's/return bw\.child\.UpdateClientConnState(ccs)/return verifyUpdate(bw, bw.child.UpdateClientConnState(ccs))/; s/^\(\s*\)bw\.child\.ExitIdle()$/\1verifyPreExitIdle(bw); bw.child.ExitIdle(); verifyExitIdle(bw)/' balancer/endpointsharding/endpointsharding.go
VERIFY_STALL="$3" go test ./balancer/endpointsharding -run "^Test\$/^$2\$" -count=1 -timeout 40s -v || true
