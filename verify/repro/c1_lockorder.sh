#!/bin/bash
# Run: verify/repro/c1_lockorder.sh <worktree of evalon/grpc-go-en-e94a93f0 or evalon/grpc-go-en-df44e560> [path/to/eval_endpointsharding_test.go]
# Swaps the two `mu sync.Mutex` fields in endpointsharding.go for order-recording wrappers (instrumentation only),
# runs the lock-order scenario, then the whole package under -race, and restores the worktree.
set -euo pipefail
here="$(cd "$(dirname "$0")" && pwd)"
wt="$1"; fixture="${2:-}"
pkg="$wt/balancer/endpointsharding"
cp "$pkg/endpointsharding.go" "$pkg/endpointsharding.go.verifybak"
trap 'mv "$pkg/endpointsharding.go.verifybak" "$pkg/endpointsharding.go"; rm -f "$pkg/zz_verify_lockorder.go" "$pkg/zz_verify_lockorder_test.go" "$pkg/zz_verify_lockorder_ext_test.go"' EXIT
# first `mu sync.Mutex` is endpointSharding.mu, second is balancerWrapper.mu
awk '/^\tmu sync\.Mutex$/ { n++; if (n==1) { print "\tmu esMutex"; next } if (n==2) { print "\tmu bwMutex"; next } } { print } END { print "\nvar _ sync.Mutex // keep the sync import used (instrumentation)" }' "$pkg/endpointsharding.go.verifybak" > "$pkg/endpointsharding.go"
(cd "$wt" && git diff --stat -- balancer/endpointsharding/endpointsharding.go && git diff -U0 -- balancer/endpointsharding/endpointsharding.go | grep -E '^[-+]\s')
cp "$here/c1_lockorder_instr.go.txt" "$pkg/zz_verify_lockorder.go"
cp "$here/c1_lockorder_test.go.txt" "$pkg/zz_verify_lockorder_test.go"
cp "$here/c1_lockorder_ext_test.go.txt" "$pkg/zz_verify_lockorder_ext_test.go"
if [ -n "$fixture" ]; then cp "$fixture" "$pkg/eval_endpointsharding_test.go"; fi
cd "$wt"
echo "### scenario test"
go test ./balancer/endpointsharding -run '^TestVerifyC1_LockOrder$' -race -count=1 -v 2>&1 | grep -vE '^=== '
echo "### whole package (existing tests + eval fixture if present), orders dumped by the last test"
go test ./balancer/endpointsharding -race -count=1 -v 2>&1 | grep -E '^(--- FAIL|FAIL|ok|PASS)|DATA RACE|ORDER VIOLATION|^(=== RUN|--- PASS:) +TestZZVerifyC1|^--- PASS: Test |zz_verify_lockorder_test.go:1[5-9][0-9]' | tail -n 12
