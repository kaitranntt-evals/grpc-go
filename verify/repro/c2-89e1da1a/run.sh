#!/usr/bin/env bash
# Run: verify/repro/c2-89e1da1a/run.sh [trace|m2|m2-sibling|probeA|baseline] [runs]  (default trace; expects PASS although idle exit #4 is still blocked).
HERE="$(cd "$(dirname "$0")" && pwd)"; source "$HERE/../common.sh"
MODE="${1:-trace}"; RUNS="${2:-40}"; WT="$(prepare 89e1da1a)"; cd "$WT"
T='^Test$/^AutoReconnect_NotBlockedByOtherChildUpdate$'
many() { # build once, run $RUNS times 4 at a time, count outcomes
  local bin log; bin="$(mktemp)"; log="$(mktemp -d)"
  go test -c -race -o "$bin" ./balancer/endpointsharding
  for i in $(seq 1 "$RUNS"); do
    "$bin" -test.run "$T" -test.v -test.count=1 -test.timeout 12s >"$log/$i.log" 2>&1 &
    if [ $((i % 4)) -eq 0 ]; then wait; fi
  done; wait
  echo "PASS: $(grep -l -- '--- PASS: Test/AutoReconnect_NotBlockedByOtherChildUpdate' "$log"/*.log | wc -l) of $RUNS"
  echo "FAIL (Timeout waiting for ExitIdle to be called on child \"b\"): $(grep -l 'Timeout waiting for ExitIdle to be called on child "b"' "$log"/*.log | wc -l) of $RUNS"
}
case "$MODE" in
  baseline) go test -v ./balancer/endpointsharding -run "$T" -race -count=1 -timeout 40s ;;
  trace)    git apply "$HERE/mutation-M2-with-sched-probe-and-trace.patch"
            go test -v ./balancer/endpointsharding -run "$T" -race -count=1 -timeout 40s ;;
  m2-sibling) git apply "$HERE/mutation-M2.patch"   # shows M2 is a real violation: the sibling test without stale events fails
            go test -v ./balancer/endpointsharding -run '^Test$/^ChildExitIdle_NotBlockedByOtherChildUpdate$' -race -count=1 -timeout 25s 2>&1 | sed -n '1,12p' ;;
  m2)       git apply "$HERE/mutation-M2.patch"; many ;;
  probeA)   git apply "$HERE/probeA-remove-reportIdle.patch"; many ;;
  *) echo "unknown mode $MODE" >&2; exit 2 ;;
esac
