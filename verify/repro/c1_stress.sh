#!/bin/bash
# Run (C1 stress): ./verify/repro/c1_stress.sh <checkout of evalon/grpc-go-xd-dd46dda9> [workers=16] [iters-per-worker=100] — builds test/xds with -race once, then runs (a) the solution's unmodified lifecycle test, (b) the diagnostics copy, (c) the WaitForHandlers control, each workers*iters times as separate processes, and prints failure counts. ONLY=unmodified|diagnostics|waitforhandlers runs one of them; OUT=<dir> keeps the logs there.
set -euo pipefail
src=$(cd "$(dirname "$0")" && pwd)
wt=${1:?path to a checkout of the claim branch}
workers=${2:-16}
iters=${3:-100}
out=${OUT:-$(mktemp -d)}
mkdir -p "$out"

cp "$src/c1_stop_ordering_test.go" "$wt/test/xds/verify_c1_stop_ordering_test.go"
(cd "$wt" && go test -c -race -tags verifyc1 -o "$out/xds.test" ./test/xds)

stress() { # <label> <run-regex>
  local label=$1 re=$2 dir="$out/$1"
  mkdir -p "$dir"
  for w in $(seq 1 "$workers"); do
    (
      for i in $(seq 1 "$iters"); do
        if ! "$out/xds.test" -test.run "$re" -test.count=1 -test.v >"$dir/w$w.log" 2>&1; then
          cp "$dir/w$w.log" "$dir/FAIL_w${w}_i$i.log"
        fi
        echo x >>"$dir/w$w.count"
      done
    ) &
  done
  wait
  echo "[$label] runs: $(cat "$dir"/w*.count | wc -l)  failures: $(ls "$dir" | grep -c '^FAIL_' || true)"
  grep -h -E 'after server stop, want 0|VERIFY-DIAG|^(---|\s+---) FAIL|DATA RACE|panic:' "$dir"/FAIL_* 2>/dev/null | sed -E 's/^\s+//' | sort | uniq -c | sort -rn | head -40 || true
}

only=${ONLY:-}
want() { [ -z "$only" ] || [ "$only" = "$1" ]; }
if want unmodified; then stress unmodified '^Test$/^ServerSideXDS_RouteConfigurationUpdates_ReleaseSupersededResources$'; fi
if want diagnostics; then stress diagnostics '^Test$/^VerifyC1_SolutionLifecycle_Diagnostics$'; fi
if want waitforhandlers; then stress waitforhandlers '^Test$/^VerifyC1_SolutionLifecycle_WaitForHandlers$'; fi
echo "logs: $out"
