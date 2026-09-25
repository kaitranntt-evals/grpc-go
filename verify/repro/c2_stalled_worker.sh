#!/usr/bin/env bash
# Run: verify/repro/c2_stalled_worker.sh <branch-suffix> '<test regex>' [env, default "ES_STALL=1 ES_STALL_MS=3 ES_DROP_EXITIDLE=1"]   e.g. cc0c22d2 '^Test$/^EndpointShardingExitIdleWhileOtherChildBlocked$'
# Injects a stalled child update (held >3ms never returns) and dropped ExitIdle so the progress assertion times out, then shows where cleanup waits.
source "$(dirname "$0")/lib.sh"
wt=$(mkwt_branch "$1"); cd "$wt"; f=balancer/endpointsharding/endpointsharding.go
addgo "$REPRO_DIR/c2_zz_mut.go" balancer/endpointsharding/zz_mut.go
sed -i -E 's/return bw, bw\.child\.UpdateClientConnState\(ccs\)/return bw, stallIfSlow(func() error { return bw.child.UpdateClientConnState(ccs) })/; s/return bw\.child\.UpdateClientConnState\(ccs\)/return stallIfSlow(func() error { return bw.child.UpdateClientConnState(ccs) })/; s/^(\s*)bw\.child\.ExitIdle\(\)$/\1if !dropExitIdle() { stallExitIdle(); bw.child.ExitIdle() }/' "$f"
log=$(mktemp); start=$(date +%s)
env ${3:-ES_STALL=1 ES_STALL_MS=3 ES_DROP_EXITIDLE=1} timeout 200 go test -race -count=1 -timeout 60s -v -run "$2" ./balancer/endpointsharding/ >"$log" 2>&1 || true
echo "duration=$(( $(date +%s)-start ))s log=$log"
grep -E '_test.go:[0-9]+:|panic: test timed out|AUDIT: stall|Leaked goroutine' "$log" | head -8 || true
echo "--- goroutines with test frames at exit:"
awk '/^goroutine [0-9]+ \[/{if(b ~ /_test\.go/) print b; b=$0"\n"; next} /^$/{if(b ~ /_test\.go/) print b; b=""; next} {if(b!="") b=b $0 "\n"}' "$log" \
  | grep -E '^goroutine|endpointsharding[._]|_test\.go:[0-9]+' | grep -vE 'runtime/|testing/' | head -24 || true
echo "--- test-file frames in leaked/remaining goroutines (fixture code still blocked):"
grep -E '^\s+/.*_test\.go:[0-9]+ \+' "$log" | sed -E 's#^\s+/.*/balancer/#balancer/#' | sort | uniq -c | head -12 || true
