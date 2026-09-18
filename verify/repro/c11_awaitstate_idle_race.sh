#!/usr/bin/env bash
# Run (in a checkout of evalon/grpc-go-xd-c697e37b): bash verify/repro/c11_awaitstate_idle_race.sh [count]   # prints per-run PASS/FAIL and the "got TRANSIENT_FAILURE; want IDLE" lines
#
# Audit repro for claim C11: runs TestSecurityConfigUpdate_ProviderReplacedDuringHandshake
# repeatedly under the race detector with several GOMAXPROCS values (the schedule
# that exposes the flake), then summarizes how many runs failed at the
# AwaitState(IDLE) expectation after the restartable listener is restarted.
set -u
cd "$(git rev-parse --show-toplevel)"
COUNT="${1:-30}"
OUT="$(mktemp)"
go test -race ./internal/xds/balancer/clusterimpl/tests/ \
  -run 'Test/SecurityConfigUpdate_ProviderReplacedDuringHandshake' \
  -count="$COUNT" -cpu 1,2,4 -v 2>&1 | tee "$OUT" | grep -E '^\s*--- (PASS|FAIL)|want IDLE|^ok|^FAIL'
echo "== summary"
echo "runs:   $(grep -cE '^\s*--- (PASS|FAIL): Test/SecurityConfigUpdate_ProviderReplacedDuringHandshake' "$OUT")"
echo "failed: $(grep -cE '^\s*--- FAIL: Test/SecurityConfigUpdate_ProviderReplacedDuringHandshake' "$OUT")"
echo "'got TRANSIENT_FAILURE; want IDLE' diagnostics: $(grep -c 'got TRANSIENT_FAILURE; want IDLE' "$OUT")"
rm -f "$OUT"
