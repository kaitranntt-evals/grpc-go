#!/bin/bash
# C4 driver: on each C4 target worktree ($C4_WT/<suffix>, checked out at evalon/grpc-go-en-<suffix>),
# provision the byte-exact fixture at balancer/endpointsharding/eval_endpointsharding_test.go plus the
# external-consumer probe, run both tests with -race, then remove the copies again.
#   C4_WT=~/wt FIXTURE=<extracted eval_tests.zip>/tests/eval_endpointsharding_test.go PROBE=$PWD/verify/repro/c4_external_exitidle_test.go.txt bash verify/repro/run_c4.sh
set -u
FIXTURE=${FIXTURE:-$HOME/eval_tests/tests/eval_endpointsharding_test.go}
PROBE=${PROBE:-$(cd "$(dirname "$0")" && pwd)/c4_external_exitidle_test.go.txt}
cd "${C4_WT:-$HOME/wt}"
for b in 2805a453 6ab5ed6f 7935c7b4 28870ca2 349fa040 212e3582 a7641ce6 83039173 9849d323 15d00ee8 bd3553ea fd6b3403; do
  echo "=== branch evalon/grpc-go-en-$b ($(cd $b && git rev-parse --short HEAD))"
  had_fixture=0
  [ -e $b/balancer/endpointsharding/eval_endpointsharding_test.go ] && had_fixture=1
  cp "$FIXTURE" $b/balancer/endpointsharding/eval_endpointsharding_test.go
  cmp -s "$FIXTURE" $b/balancer/endpointsharding/eval_endpointsharding_test.go && echo "fixture byte-identical to archive: yes"
  cp "$PROBE" $b/balancer/endpointsharding/zz_c4_external_exitidle_test.go
  (cd $b && go test -v ./balancer/endpointsharding -run '^(TestEval_ChildStateExitIdleCallback|TestVerifyC4_ExternalConsumerDirectExitIdle)$' -race -count=1 2>&1 | grep -E "^(=== RUN|--- |PASS|FAIL|ok|    .*:)" )
  rm -f $b/balancer/endpointsharding/zz_c4_external_exitidle_test.go
  [ $had_fixture = 0 ] && rm -f $b/balancer/endpointsharding/eval_endpointsharding_test.go
  (cd $b && git status --short | sed 's/^/  leftover: /')
done
