#!/usr/bin/env bash
# Run: verify/repro/run-all.sh  (replays every probe for C1 and C2, writing one log per mode to verify/logs/).
cd "$(dirname "$0")/../.."
R=verify/repro; L="$PWD/verify/logs"; mkdir -p "$L"
run() { local name="$1"; shift; WT_ROOT="$HOME/verify-wt/$name" "$@" >"$L/$name.log" 2>&1; echo "$name exit=$?"; }
run c1-4279a1cf-baseline $R/c1-4279a1cf/run.sh baseline &
run c1-4279a1cf-hang $R/c1-4279a1cf/run.sh hang &
run c1-4279a1cf-control $R/c1-4279a1cf/run.sh control &
run c1-eb9cd093-baseline $R/c1-eb9cd093/run.sh baseline &
wait
run c1-4279a1cf-matrix $R/c1-4279a1cf/run.sh matrix &
run c1-eb9cd093-hang $R/c1-eb9cd093/run.sh hang &
run c1-eb9cd093-leak $R/c1-eb9cd093/run.sh leak &
run c1-eb9cd093-control $R/c1-eb9cd093/run.sh control &
run c2-89e1da1a-baseline $R/c2-89e1da1a/run.sh baseline &
wait
run c2-89e1da1a-trace $R/c2-89e1da1a/run.sh trace &
run c2-89e1da1a-m2-sibling $R/c2-89e1da1a/run.sh m2-sibling &
wait
run c2-89e1da1a-m2 $R/c2-89e1da1a/run.sh m2 80
run c2-89e1da1a-probeA $R/c2-89e1da1a/run.sh probeA 80
