#!/bin/sh
# Run: EVAL_FIXTURE=/path/to/tests/eval_endpointsharding_test.go sh verify/instrumentation/c2/c2_lockcheck.sh   (EVAL_FIXTURE optional; from a grpc-go checkout of the verify branch)
# C2: runtime check of every lock-ownership / protected-data / lock-order assertion in the synchronization comments of evalon/grpc-go-en-c91051b2.
set -u
BR=evalon/grpc-go-en-c91051b2
. "$(dirname "$0")/../../repro/_worktree.sh"
I="$VERIFY/instrumentation/c2"
echo "### 1. added/changed synchronization comments (diff vs task base bf9e7cd3, comment lines only)"
git diff -U0 bf9e7cd3430df40d0732ba42eb88bd5f2cc63407 HEAD -- balancer/endpointsharding/endpointsharding.go | grep -E '^\+\s*//' | grep -i -E 'mu\b|updateMu|lock|guard|serializ|synchron|hold|acquir|wait' 
echo "### 2. instrument (mutex types -> tracked, checks before guarded accesses and child calls)"
python3 "$I/instrument.py" balancer/endpointsharding/endpointsharding.go
cp "$I/audit_lockcheck.go.txt" balancer/endpointsharding/audit_lockcheck.go
cp "$I/audit_main_endpointsharding_test.go.txt" balancer/endpointsharding/audit_main_test.go
cp "$I/audit_main_ringhash_test.go.txt" balancer/ringhash/audit_main_test.go
cp "$I/c2_parent_exitidle_probe_test.go.txt" balancer/endpointsharding/audit_c2_probe_test.go
[ -n "${EVAL_FIXTURE:-}" ] && cp "$EVAL_FIXTURE" balancer/endpointsharding/eval_endpointsharding_test.go && echo "eval fixture installed"
git diff --stat
echo "### 3. endpointsharding package (branch tests + probe + eval fixture if given), -race"
go test ./balancer/endpointsharding -race -count=1 -v > "$TMP/es.log" 2>&1
grep -E '^(--- |ok|FAIL|PASS)|AUDIT-C2|WARNING: DATA RACE|OBSERVED' "$TMP/es.log" | cut -c1-200
echo "### 4. ringhash package, -race"
go test ./balancer/ringhash -race -count=1 -v > "$TMP/rh.log" 2>&1
grep -E '^(--- |ok|FAIL|PASS)|AUDIT-C2|WARNING: DATA RACE' "$TMP/rh.log" | cut -c1-200
echo "### 5. positive control: hold es.mu across the call into an existing child; the checker must flag it"
python3 "$I/positive_control_mutation.py" balancer/endpointsharding/endpointsharding.go
go test ./balancer/endpointsharding -run 'Test/EndpointShardingExitIdleNotBlockedByOtherChild$' -race -count=1 -timeout 20s 2>&1 | grep -E 'AUDIT-C2 VIOLATION|panic: test timed out|^(ok|FAIL)'
