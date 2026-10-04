#!/bin/sh
# Replays the C1 stall experiments. Usage: sh c1-run.sh <worktree-of-branch> <f05303a3|af1f6104> <outdir>
# Prereq: c1_stall_instrumentation_test.go.txt copied to <worktree>/internal/transport/verify_stall_test.go (source: verify/instrumentation/c1_stall_instrumentation_test.go.txt) and c1-<branch>.diff applied.
set -u
WT=$1; B=$2; OUT=$3; mkdir -p "$OUT"
case $B in
  f05303a3) RUN='^Test$/^ClientReceivesManyTinyDataFrames$/^compaction_enabled$' ;;
  af1f6104) RUN='^Test$/^ClientTinyDataFramesBoundReceiveMemory$/^compaction=true$' ;;
esac
(cd "$WT" && go test -c -o "$OUT/$B.test" ./internal/transport) || exit 1
for m in none accept read stream write; do
  ( cd "$WT/internal/transport" && s=$(date +%s.%N) && VERIFY_STALL=$( [ $m = none ] || echo $m ) "$OUT/$B.test" -test.run "$RUN" -test.count=1 -test.v -test.timeout 120s > "$OUT/$B-$m.log" 2>&1; echo "exit=$? wall=$(echo "$(date +%s.%N) - $s" | bc)s" >> "$OUT/$B-$m.log" ) &
done
wait
