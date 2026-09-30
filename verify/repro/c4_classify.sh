#!/bin/sh
# Run: sh verify/repro/c4_classify.sh <compiled-test-binary> [runs] [extra test flags]   (binary = go test -c of balancer/endpointsharding with c4_guard_lost_mutant.patch applied on evalon/grpc-go-en-30a9d20e)
# Runs TestEndpointSharding_ChildExitIdleAfterRemoval repeatedly and tabulates
# "which child saw ExitIdle first" against the test's own PASS/FAIL result.
bin=$1; n=${2:-300}; shift 2 2>/dev/null
i=0
while [ $i -lt $n ]; do
  out=$("$bin" -test.run 'Test/EndpointSharding_ChildExitIdleAfterRemoval' -test.v -test.count=1 "$@" 2>&1)
  first=$(printf '%s\n' "$out" | sed -n 's/^VERIFY-C4 delivery #1: ExitIdle reached child "\([^"]*\)".*/\1/p')
  a=$(printf '%s\n' "$out" | grep -c 'reached child "addr-a" (removed=true)')
  res=$(printf '%s\n' "$out" | sed -n 's/^    --- \([A-Z]*\): Test\/EndpointSharding_ChildExitIdleAfterRemoval.*/\1/p')
  echo "first=$first removedA_got_ExitIdle=$a result=$res"
  i=$((i+1))
done | sort | uniq -c
