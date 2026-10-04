#!/bin/sh
# Run: sh verify/repro/c1_run.sh   (from a grpc-go checkout of the verify branch; ~4 min; needs go and network access to the claim repo)
# C1: the regression tests added on evalon/grpc-go-en-873d5e04 join their worker with an untimed receive in a deferred cleanup.
set -u
BR=evalon/grpc-go-en-873d5e04
. "$(dirname "$0")/_worktree.sh"
F='^=== RUN|^\s+[a-z_]+\.go:[0-9]+: |^panic: test timed out|^\s+Test.*\([0-9]+m?[0-9.]*s\)$|^(--- |ok|FAIL|PASS)|^\s+--- '
ST='ChildIsBlocked\.func1\.3\(\)|UpdateIsBlocked\.func4\(\)|_test\.go:(455|463|478|811|830|836) |runtime\.Goexit|FailNow|^goroutine [0-9]+ \[chan receive'

echo "### 1. cleanup code under audit"
grep -n -B3 -A6 'if err := <-operationDone' balancer/endpointsharding/endpointsharding_ext_test.go
grep -n -B2 -A5 'if err := <-updateDone' balancer/ringhash/ringhash_test.go

echo "### 2. unmodified branch: both tests pass"
go test ./balancer/endpointsharding -run 'Test/ChildExitIdleWhileAnotherChildIsBlocked$' -race -count=1 2>&1 | tail -1
go test ./balancer/ringhash -run 'Test/PickConnectsWhileAnotherEndpointUpdateIsBlocked$' -race -count=1 2>&1 | tail -1

echo "### 3. production regression (es.mu held across the call into an existing child), tests unmodified"
git apply "$VERIFY/repro/c1_mutation_hold_mu_across_child_update.patch"
echo "--- endpointsharding (go test -timeout 60s; the test's own deadline is 10s)"
go test ./balancer/endpointsharding -run 'Test/ChildExitIdleWhileAnotherChildIsBlocked/UpdateClientConnState$' -race -count=1 -timeout 60s -v > "$TMP/es.log" 2>&1
grep -E "$F" "$TMP/es.log" | cut -c1-200
echo "--- test goroutine at the time of the panic:"
grep -E "$ST" -A1 "$TMP/es.log" | grep -E 'ChildIsBlocked|Goexit|FailNow|_test\.go' | cut -c1-200 | head -12
echo "--- ringhash (go test -timeout 45s; the test's own deadline is 10s)"
go test ./balancer/ringhash -run 'Test/PickConnectsWhileAnotherEndpointUpdateIsBlocked$' -race -count=1 -timeout 45s -v > "$TMP/rh.log" 2>&1
grep -E "$F" "$TMP/rh.log" | cut -c1-200
echo "--- test goroutine at the time of the panic:"
grep -E "$ST" -A1 "$TMP/rh.log" | grep -E 'UpdateIsBlocked|Goexit|FailNow|_test\.go' | cut -c1-200 | head -12

echo "### 4. control: same regression, endpointsharding cleanup bounded (join and Close)"
git apply "$VERIFY/repro/c1_control_bounded_cleanup.patch"
go test ./balancer/endpointsharding -run 'Test/ChildExitIdleWhileAnotherChildIsBlocked/UpdateClientConnState$' -race -count=1 -timeout 60s -v > "$TMP/ctl.log" 2>&1
grep -E "$F" "$TMP/ctl.log" | cut -c1-200
echo "panic lines in control run: $(grep -c '^panic: test timed out' "$TMP/ctl.log")"
