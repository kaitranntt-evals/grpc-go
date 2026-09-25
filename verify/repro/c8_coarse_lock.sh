#!/usr/bin/env bash
# Run: verify/repro/c8_coarse_lock.sh   (C8: 2642de3c's independent-progress test, one-line API adaptation, on original coarse-lock base bf9e7cd3)
source "$(dirname "$0")/lib.sh"
wt=$(mkwt_ref bf9e7cd3430df40d0732ba42eb88bd5f2cc63407); cd "$wt"
git apply "$REPRO_DIR/c8-test-adaptation.diff"
log=$(mktemp); start=$(date +%s)
timeout 120 go test -race -count=1 -timeout 45s -v -run '^Test$/^EndpointShardingExitIdleNotBlockedByOtherChild$' ./balancer/endpointsharding/ >"$log" 2>&1 && rc=0 || rc=$?; echo "rc=$rc duration=$(( $(date +%s)-start ))s"
grep -E '_test.go:[0-9]+: Timed out|panic: test timed out' "$log" || true
awk '/^goroutine [0-9]+ \[/{if(b ~ /_test\.go/) print b; b=$0"\n"; next} /^$/{if(b ~ /_test\.go/) print b; b=""; next} {if(b!="") b=b $0 "\n"}' "$log" | grep -E '^goroutine|endpointsharding[._]|\.go:[0-9]+ ' | grep -vE 'runtime/|testing/|grpctest' | head -20 || true
