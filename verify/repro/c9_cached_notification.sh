#!/usr/bin/env bash
# Run: verify/repro/c9_cached_notification.sh   (C9 on 866a4ecf: forbidden removed-child ExitIdle before (mode 1) vs after (mode 2) child A's expected one)
source "$(dirname "$0")/lib.sh"
wt=$(mkwt_branch 866a4ecf); cd "$wt"
git apply "$REPRO_DIR/c9-deliver-closed.diff"
for mode in 1 2; do echo "== ES_DELIVER_CLOSED=$mode"
  ES_DELIVER_CLOSED=$mode go test -race -count=20 -v -run '^Test$/^EndpointShardingExitIdleAfterChildRemoved$' ./balancer/endpointsharding/ 2>&1 \
    | grep -E 'AUDIT: delivering|_test.go:[0-9]+:|--- (PASS|FAIL): Test/' | sed -E 's/\([0-9.]+s\)//' | sort | uniq -c
done
