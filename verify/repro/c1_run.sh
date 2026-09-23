#!/bin/bash
# C1 repro: cd <worktree of evalon/grpc-go-en-28870ca2> && bash verify/repro/c1_run.sh   (needs verify/repro/c1_coarse_lock_mutation.patch next to it)
# Applies a coarse parent-wide lock (ExitIdle on child A waits behind child B's in-flight update), then
# (1) runs the changed test 20x as a race-built binary: it passes whenever the stale `updateCalled` signal
#     from the initial update lets the assertion run before the background update holds the lock;
# (2) drains that stale signal (a 1-line test edit) and reruns once: the same mutation now fails deterministically
#     and deferred es.Close() blocks until the Go test timeout.
set -u
cd "$(git rev-parse --show-toplevel)"
PATCH="$(dirname "$0")/c1_coarse_lock_mutation.patch"
TEST=balancer/endpointsharding/endpointsharding_ext_test.go
PROD=balancer/endpointsharding/endpointsharding.go
RUN='Test/EndpointShardingExitIdleDuringOtherChildUpdate$'
cleanup() { git checkout -- "$PROD" "$TEST"; rm -f /tmp/c1_mutated.test; }
trap cleanup EXIT
git apply "$PATCH" || exit 1
echo "== mutated production code (git diff --stat):"; git diff --stat -- "$PROD"
go test -c -race -o /tmp/c1_mutated.test ./balancer/endpointsharding || exit 1
pass=0; fail=0
for i in $(seq 1 20); do
  if timeout 30s /tmp/c1_mutated.test -test.run "$RUN" -test.count=1 -test.timeout 10s >/tmp/c1_run_$i.txt 2>&1; then
    pass=$((pass+1)); echo "run $i: PASS"
  else
    fail=$((fail+1)); echo "run $i: FAIL ($(grep -m1 -o 'Timed out[^"]*\|panic: test timed out[^,]*' /tmp/c1_run_$i.txt))"
  fi
done
echo "== 20 runs with coarse lock: PASS=$pass FAIL=$fail"
# (2) drain the stale updateCalled signal left by the initial UpdateClientConnState, then rerun once.
python3 - "$TEST" <<'EOF'
import sys
p=sys.argv[1]; s=open(p).read()
anchor="\tupdateDone := make(chan error, 1)\n"
assert s.count(anchor)==1
s=s.replace(anchor,"\tfor len(otherChild.updateCalled) > 0 {\n\t\t<-otherChild.updateCalled // C1 probe: drop the signal from the initial update\n\t}\n"+anchor)
open(p,"w").write(s)
EOF
echo "== test edit (git diff):"; git diff -- "$TEST"
echo "== drained rerun:"
go test ./balancer/endpointsharding -run "$RUN" -race -count=1 -timeout 30s -v 2>&1 | grep -v '^\s*/\|^internal/\|^sync\.\|^runtime\.\|^testing\.\|^created by\|^main\.\|^google.golang.org/grpc/internal' | head -60
