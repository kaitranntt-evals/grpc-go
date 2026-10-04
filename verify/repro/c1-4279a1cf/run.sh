#!/usr/bin/env bash
# Run: verify/repro/c1-4279a1cf/run.sh [hang|control|matrix|baseline]  (default hang; expects "panic: test timed out" after the test's own Fatal).
HERE="$(cd "$(dirname "$0")" && pwd)"; source "$HERE/../common.sh"
MODE="${1:-hang}"; WT="$(prepare 4279a1cf)"; cd "$WT"
case "$MODE" in
  baseline) ;;
  hang|matrix) git apply "$HERE/mutation-sync-exitidle.patch" ;;
  control) git apply "$HERE/mutation-sync-exitidle.patch" "$HERE/control-bounded-cleanup-wait.patch" ;;
  *) echo "unknown mode $MODE" >&2; exit 2 ;;
esac
T='^Test$/^ChildExitIdleDuringBuild$'; TO=40s; [ "$MODE" = matrix ] && { T='^Test$/^ChildOperationsIndependent$'; TO=120s; }
time go test -v ./balancer/endpointsharding -run "$T" -race -count=1 -timeout "$TO"
