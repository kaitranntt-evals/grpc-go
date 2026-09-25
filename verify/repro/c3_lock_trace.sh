#!/usr/bin/env bash
# Run: verify/repro/c3_lock_trace.sh 7d4c90b4|2642de3c   (C3: TryLock-based lock-ownership trace at the commented lock sites)
source "$(dirname "$0")/lib.sh"
wt=$(mkwt_branch "$1"); cd "$wt"
git apply "$REPRO_DIR/c3-$1-instr.diff"
case $1 in
  7d4c90b4) addgo "$REPRO_DIR/c3_7d4c90b4_zz_audit.go" balancer/endpointsharding/zz_audit.go; run='^Test$/^EndpointShardingSynchronousChildUpdates$' ;;
  2642de3c) run='^Test$/^EndpointShardingExitIdleNotBlockedByOtherChild$' ;;
esac
grep -n -B2 -A4 -E 'must not be held while holding|be acquired while holding childMu' balancer/endpointsharding/endpointsharding.go | head -20 || true
go test -race -count=1 -v -run "$run" ./balancer/endpointsharding/ 2>&1 | grep -E 'AUDIT|^\s+google.golang.org/grpc/balancer/endpointsharding|--- (PASS|FAIL)|^(ok|FAIL)' | head -30 || true
