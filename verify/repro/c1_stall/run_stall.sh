#!/bin/bash
# (C1 helper) batch runner/classifier used to produce verify/evidence.md#c1; run_c1.sh is the single-case repro.
# usage: run_stall.sh <branch>
b=$1; cd ~/wt/$b || exit 1
out=~/stall_logs/$b; mkdir -p $out
tests=$(git diff bf9e7cd3 HEAD -- "*_test.go" | grep -o "^+func (s) Test[A-Za-z_0-9]*" | sed "s/^+func (s) Test//")
# baseline
go test ./balancer/endpointsharding -count=1 -timeout 120s > $out/baseline.log 2>&1; echo "baseline $(tail -1 $out/baseline.log)" > $out/summary.txt
for t in $tests; do
  for m in update:1 update:2 update:3 exitidle:1 exitidle:2; do
    log=$out/$t.$m.log
    VERIFY_STALL=$m go test ./balancer/endpointsharding -run "^Test\$/^$t\$" -count=1 -timeout 40s -v > $log 2>&1
    if grep -q "panic: test timed out" $log; then r=HANG; elif grep -q "^--- FAIL\|^FAIL" $log; then r=FAIL; else r=PASS; fi
    echo "$t $m $r" >> $out/summary.txt
  done
done
echo DONE >> $out/summary.txt
