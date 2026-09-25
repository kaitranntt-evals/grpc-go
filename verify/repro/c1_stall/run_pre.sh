#!/bin/bash
# (C1 helper) batch runner/classifier used to produce verify/evidence.md#c1; run_c1.sh is the single-case repro.
b=$1; cd ~/wt/$b; out=~/pre_logs/$b; mkdir -p $out
tests=$(git diff bf9e7cd3 HEAD -- "*_test.go" | grep -o "^+func (s) Test[A-Za-z_0-9]*" | sed "s/^+func (s) Test//")
for t in $tests; do for m in preexitidle:1 preexitidle:2; do
  s=$(date +%s); VERIFY_STALL=$m go test ./balancer/endpointsharding -run "^Test\$/^$t\$" -count=1 -timeout 150s -v > $out/$t.$m.log 2>&1
  if grep -q "panic: test timed out" $out/$t.$m.log; then r=HANG; elif grep -q "^--- FAIL\|^FAIL" $out/$t.$m.log; then r=FAIL; else r=PASS; fi
  echo "$t $m $r $(( $(date +%s)-s ))s" >> $out/summary.txt
done; done; echo DONE >> $out/summary.txt
