#!/bin/bash
# Run (after setup_worktrees.sh): verify/repro/c2_run.sh <f70d0754|80a05d05|6fe210bc|8ac37769>   -- induces the stall for that branch's added test(s), prints the log, restores the worktree. Logs: ${OUT:-/tmp/c2-out}.
here=$(cd "$(dirname "$0")" && pwd)
WT=${WT:-/tmp/claims}; out=${OUT:-/tmp/c2-out}; mkdir -p "$out"
b=$1; cd "$WT/$b" || exit 1
git checkout -q -- .
case $b in
f70d0754)
  # Stalled connection attempt: loopback SYNs are dropped, so net.Dial never completes on its own.
  go test -c -o "$out/f70d0754.test" ./internal/transport
  (time "$here/c2_blackhole.sh" "$out/f70d0754.test" '^Test$/^ServerCompactsSmallDataFrames$' "${T:-45s}") > "$out/c2_f70d0754_blackhole.log" 2>&1
  cat "$out/c2_f70d0754_blackhole.log" ;;
80a05d05)
  (time go test ./internal/transport -run '^Test$/^ClientReceivesManySmallDataFrames$' -count=1 -v -timeout 45s) > "$out/c2_80a05d05_nostall.log" 2>&1
  git apply "$here/c2_80a05d05_handler_stall.patch"
  (time VERIFY_C2_STALL_HANDLER=1 go test ./internal/transport -run '^Test$/^ClientReceivesManySmallDataFrames$' -count=1 -v -timeout 45s) > "$out/c2_80a05d05_stall.log" 2>&1
  cat "$out/c2_80a05d05_nostall.log" "$out/c2_80a05d05_stall.log" ;;
6fe210bc)
  # (a) the client read loop stalls: does the helper's 10s context interrupt the test's `<-stream.Done()`?
  git apply "$here/c2_6fe210bc_reader_stall.patch"
  (time VERIFY_C2_STALL_PUT=1 go test ./internal/transport -run '^Test$/^ClientReceivesManySmallDataFrames$' -count=1 -v -timeout 40s) > "$out/c2_6fe210bc_readerstall.log" 2>&1
  git checkout -q -- .
  # (b) the peer stalls instead (never sends END_STREAM): same helper, same bare wait.
  cp "$here/c2_6fe210bc_peer_stall_test.go" internal/transport/
  (time go test ./internal/transport -run '^Test$/^VerifyC2_PeerWithholdsEndStream$' -count=1 -v -timeout 60s) > "$out/c2_6fe210bc_peerstall.log" 2>&1
  rm -f internal/transport/c2_6fe210bc_peer_stall_test.go
  # (c) terminal message lost: unit tests (timeout context) and the added benchmark (cancel-only context; killed by `timeout` after 60s).
  python3 "$here/c2_lose_terminal_mutant.py" internal/transport/transport.go
  (time VERIFY_C2_LOSE_TERMINAL=1 go test ./internal/transport -run '^Test$/^RecvBufferCompaction_' -count=1 -v -timeout 120s) > "$out/c2_6fe210bc_unit.log" 2>&1
  (time VERIFY_C2_LOSE_TERMINAL=1 timeout -s QUIT 60s go test ./internal/transport -run '^$' -bench 'RecvBufferSmallFrames' -benchtime 1x -timeout 25s) > "$out/c2_6fe210bc_bench.log" 2>&1
  cat "$out/c2_6fe210bc_readerstall.log" "$out/c2_6fe210bc_peerstall.log" "$out/c2_6fe210bc_unit.log" "$out/c2_6fe210bc_bench.log" ;;
8ac37769)
  python3 "$here/c2_lose_terminal_mutant.py" internal/transport/transport.go
  (time VERIFY_C2_LOSE_TERMINAL=1 go test ./internal/transport -run '^Test$/^ReceiveBufferCompactionOwnership$' -count=1 -v -timeout 40s) > "$out/c2_8ac37769_ownership.log" 2>&1
  (time VERIFY_C2_LOSE_TERMINAL=1 go test ./internal/transport -run '^Test$/^ReceiveBufferCompactionReadWhileWriting$' -count=1 -v -timeout 40s) > "$out/c2_8ac37769_bounded.log" 2>&1
  cat "$out/c2_8ac37769_ownership.log" "$out/c2_8ac37769_bounded.log" ;;
*) echo "unknown branch $b"; exit 2 ;;
esac
git checkout -q -- .
