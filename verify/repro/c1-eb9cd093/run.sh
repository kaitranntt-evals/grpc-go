#!/usr/bin/env bash
# Run: verify/repro/c1-eb9cd093/run.sh [hang|leak|control|baseline]  (default hang; expects "panic: test timed out" with the worker at <-fc.ctrl.unblockUpdate).
HERE="$(cd "$(dirname "$0")" && pwd)"; source "$HERE/../common.sh"
MODE="${1:-hang}"; WT="$(prepare eb9cd093)"; cd "$WT"
case "$MODE" in
  baseline) ;;
  leak)    git apply "$HERE/sched-probe-late-update.patch" ;;
  hang)    git apply "$HERE/sched-probe-late-update-under-child-lock.patch" ;;
  control) git apply "$HERE/sched-probe-late-update-under-child-lock.patch" "$HERE/control-release-on-timeout.patch" ;;
  *) echo "unknown mode $MODE" >&2; exit 2 ;;
esac
time go test -v ./balancer/endpointsharding -run '^Test$/^EndpointSharding_ChildStateExitIdleNotBlockedByOtherChild$' -race -count=1 -timeout 60s
