#!/bin/bash
# Run: verify/repro/replay_all.sh <dir with one clean checkout per branch id (eababd58, ..., 'perfect' for the audited branch, 'base' for 4ee6ac46; see setup_worktrees.sh)> <path to eval_xds_server_interceptor_leak_test.go> <log dir>   (replays every probe in verify/evidence.md; ~35 min)
set -uo pipefail
here="$(cd "$(dirname "$0")" && pwd)"
W="$(cd "$1" && pwd)"; FIX="$2"; L="$3"; mkdir -p "$L"
reset() { git -C "$W/$1" checkout -q -- . && git -C "$W/$1" clean -fdq; }
gotest() { # checkout, repro file, package, run regex, log
  reset "$1"; cp "$here/$2" "$W/$1/$3/verify_$2"
  (cd "$W/$1" && go test -race -count=1 -v -run "$4" "./$3" 2>&1) > "$5"; echo "exit=$?" >> "$5"; reset "$1"
}
C1="eababd58 b7bc0d7f dab66e4f 415a74da 0723d2f4 0ed1f32c eb9c66bf e03d1e42 98faa6aa 2ffad480 dad62956 6851db1c 3e0a44dd eb19a38b"
C2="eababd58 dab66e4f 415a74da 0ed1f32c eb9c66bf e03d1e42 98faa6aa 2ffad480 74924778 dad62956 79540ac1 eb19a38b 2a01d623 311db1b4"
for b in $C1 perfect; do reset $b; "$here/c1_c8_trace.sh" "$W/$b" > "$L/c1_$b.log" 2>&1; reset $b; echo "c1 $b done"; done
for b in $C2 perfect; do reset $b; "$here/c2_sched_probe.sh" "$W/$b" close 150ms 1 > "$L/c2_close_$b.log" 2>&1; reset $b; echo "c2 close $b done"; done
for b in dab66e4f 2a01d623 311db1b4 perfect; do reset $b; "$here/c2_sched_probe.sh" "$W/$b" construct 150ms 3 > "$L/c2_construct_$b.log" 2>&1; reset $b; echo "c2 construct $b done"; done
for b in 2a01d623 311db1b4; do reset $b; "$here/c2_unbounded_wait_$b.sh" "$W/$b" > "$L/c2_unbounded_$b.log" 2>&1; reset $b; echo "c2 unbounded $b done"; done
for b in $C2; do reset $b; "$here/c2_absent_notification.sh" "$W/$b" 60s > "$L/c2_absent_$b.log" 2>&1; reset $b; echo "c2 absent $b done"; done
for b in bdc42e7c cf01ba8b; do reset $b; "$here/../instrumentation/c3_compile_check.sh" "$W/$b" "$FIX" > "$L/c3_$b.log" 2>&1; reset $b; echo "c3 $b done"; done
gotest perfect c4_c10_closed_interceptor_invoked_test.go test/xds '^Test$/^VerifyC4C10_' "$L/c4_c10_perfect.log"; echo "c4/c10 done"
gotest perfect c5_c11_cache_lookup_test.go internal/xds/server '^Test$/^VerifyC5C11_' "$L/c5_c11_cache_perfect.log"
gotest perfect c5_c11_closed_filter_reuse_test.go test/xds '^Test$/^VerifyC5C11_' "$L/c5_c11_rds_perfect.log"; echo "c5/c11 done"
gotest base c4_c10_closed_interceptor_invoked_test.go test/xds '^Test$/^VerifyC4C10_' "$L/c4_c10_base.log"
gotest base c5_c11_cache_lookup_test.go internal/xds/server '^Test$/^VerifyC5C11_' "$L/c5_c11_cache_base.log"
gotest base c5_c11_closed_filter_reuse_test.go test/xds '^Test$/^VerifyC5C11_' "$L/c5_c11_rds_base.log"; echo "base controls done"
for b in 57bb4302 perfect; do gotest $b c6_stop_deadlock_test.go test/xds '^Test$/^VerifyC6_' "$L/c6_$b.log"; done; echo "c6 done"
for b in 37fb43a6 perfect; do gotest $b c7_draining_rpc_unavailable_test.go test/xds '^Test$/^VerifyC7_' "$L/c7_$b.log"; done; echo "c7 done"
reset 6cf08267; "$here/c9_bare_context.sh" "$W/6cf08267" > "$L/c9_6cf08267.log" 2>&1; echo "c9 done"
for b in perfect b7bc0d7f 57bb4302 37fb43a6; do
  reset $b; cp "$FIX" "$W/$b/test/xds/eval_xds_server_interceptor_leak_test.go"
  (cd "$W/$b" && cmp "$FIX" test/xds/eval_xds_server_interceptor_leak_test.go && echo "cmp: identical" && go test -race -count=1 -v -run '^Test$/^Eval_' ./test/xds 2>&1 | grep -E '^\s+--- (PASS|FAIL): Test/|^(ok|FAIL)') > "$L/fixture_$b.log" 2>&1; reset $b
done; echo "fixture done"
