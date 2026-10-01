#!/usr/bin/env bash
# Run: bash verify/run.sh [C1 C2 ... C7]   (from ~/repos/grpc-go on the verify branch; no args = all claims)
#
# Replays the evidence in verify/evidence.md. Claim-target branches live in a
# sibling repository; they are fetched into the `claims` remote and checked
# out as detached worktrees under $VERIFY_WT (default ~/wt). Probe files are
# copied in as zz_verify_*_test.go (build tag `verify`) and removed again, and
# every mutation patch is reverted, so the worktrees are left clean.
set -uo pipefail

REPO=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
V=$REPO/verify
WT=${VERIFY_WT:-$HOME/wt}
CLAIMS_URL=${CLAIMS_URL:-https://github.com/kaitranntt-evals/grpc-go-transport-restrict-memory-overhead}
FIXTURE=${EVAL_FIXTURE:-$HOME/eval/tests/eval_recv_buffer_compaction_test.go}
PKG=./internal/transport

worktree() { # $1 = branch suffix, e.g. fbdde71c; echoes the worktree path
  local id=$1 dir=$WT/$1
  if [ ! -d "$dir" ]; then
    git -C "$REPO" remote get-url claims >/dev/null 2>&1 || git -C "$REPO" remote add claims "$CLAIMS_URL"
    git -C "$REPO" fetch -q claims "evalon/grpc-go-tr-$id" >&2
    git -C "$REPO" worktree add -q --detach "$dir" FETCH_HEAD >&2
  fi
  echo "$dir"
}
clean() { rm -f "$1"/internal/transport/zz_verify_*_test.go "$1"/internal/transport/eval_recv_buffer_compaction_test.go; git -C "$1" checkout -q -- internal/transport; }
# Keep only the lines that carry evidence; strip file:line prefixes and timings.
keep() { grep -E "$1" | sed -E 's/^\s+[a-z0-9_]+_test\.go:[0-9]+: //; s/ \([0-9.]+s\)//; s/\t[0-9.]+s$//'; }
tally() { sort | uniq -c; }

C1() {
  local d; d=$(worktree fbdde71c); clean "$d"; cd "$d" || return
  echo ">>> C1 @ $(git log --oneline -1)"
  echo ">> tests added/changed by the branch"
  git diff --stat c92e985770b7194d4a4f433c84d42c6c195e8ce5 HEAD -- '*_test.go'
  grep -n '^func (s) Test' internal/transport/recv_buffer_test.go
  echo ">> every producer/consumer coordination point and every put/read call in that file"
  grep -nE 'ready|done|\.put\(|\.Read\(|ReadMessageHeader\(|handle\(df\)' internal/transport/recv_buffer_test.go
  cp "$V/repro/c1_read_then_put_test.go" internal/transport/zz_verify_c1_test.go
  for g in 1 ''; do
    echo ">> unmutated branch: where is the producer when TestRecvBufferCompactionConcurrentReads' consumer issues its first read? GOMAXPROCS=${g:-default} x10"
    GOMAXPROCS=$g go test -tags verify -count=10 -v -run '^Test$/^VerifyC1_ConcurrentReadsSchedule$' $PKG 2>&1 | keep 'SCHEDULE|^(ok|FAIL)' | tally
  done
  echo ">> unmutated branch: sequential read-then-put probe + the branch's own tests"
  go test -tags verify -count=1 -v -run '^Test$/^(VerifyC1_SequentialReadThenPut|RecvBuffer)' $PKG 2>&1 | keep '^\s*--- (PASS|FAIL): Test/[A-Za-z0-9_]+$|^\s*--- (PASS|FAIL): Test/[A-Za-z0-9_]+ |payload across|^(ok|FAIL)'
  echo ">> apply mutation: put() appends to a stale tail chunk after the reader consumed it (repro/c1_stale_tail_mutation.patch)"
  git apply "$V/repro/c1_stale_tail_mutation.patch" && git diff --stat
  echo ">> mutated: sequential read-then-put probe"
  go test -tags verify -count=1 -v -run '^Test$/^VerifyC1_SequentialReadThenPut$' $PKG 2>&1 | keep '^\s*--- (PASS|FAIL): Test/[A-Za-z0-9_]+ |payload across|^(ok|FAIL)'
  rm internal/transport/zz_verify_c1_test.go
  for g in 1 2 ''; do
    echo ">> mutated: the branch's own tests, unmodified, GOMAXPROCS=${g:-default} x30"
    GOMAXPROCS=$g go test -count=30 -v -run '^Test$/^RecvBuffer' $PKG 2>&1 | keep '^\s+--- (PASS|FAIL): Test/RecvBuffer[A-Za-z]+ |^(ok|FAIL)' | tally
  done
  clean "$d"; git status --short
}

C2() {
  local d; d=$(worktree 5acb5ef2); clean "$d"; cd "$d" || return
  echo ">>> C2 @ $(git log --oneline -1)"
  cp "$V/probes/c2_env_optout_probe_test.go" internal/transport/zz_verify_c2_test.go
  echo ">> GRPC_GO_EXPERIMENTAL_ENABLE_RECEIVE_BUFFER_COMPACTION=false: production-created client/server streams"
  GRPC_GO_EXPERIMENTAL_ENABLE_RECEIVE_BUFFER_COMPACTION=false go test -tags verify -race -count=1 -v -run '^Test$/^VerifyC2_' $PKG 2>&1 | keep 'process env|CLIENT|SERVER|HANDLER|VERDICT|^\s*--- (PASS|FAIL)|^(ok|FAIL)'
  echo ">> control, variable unset"
  env -u GRPC_GO_EXPERIMENTAL_ENABLE_RECEIVE_BUFFER_COMPACTION go test -tags verify -race -count=1 -v -run '^Test$/^VerifyC2_' $PKG 2>&1 | keep 'process env|CLIENT|SERVER|HANDLER|VERDICT|^\s*--- (PASS|FAIL)|^(ok|FAIL)'
  rm internal/transport/zz_verify_c2_test.go
  if [ -f "$FIXTURE" ]; then
    cp "$FIXTURE" internal/transport/eval_recv_buffer_compaction_test.go
    echo ">> eval fixture (byte-exact from eval_tests.zip), env=false"
    GRPC_GO_EXPERIMENTAL_ENABLE_RECEIVE_BUFFER_COMPACTION=false go test -v -run '^TestEval_RecvBufferCompactionDisabled$' google.golang.org/grpc/internal/transport -race -count=1 2>&1 | keep '^(---|ok|FAIL|\s+eval)'
    echo ">> the branch's own both-settings tests"
    go test -race -count=1 -v -run '^Test$/^(RecvBuffer|ClientStream|ServerStream).*' $PKG 2>&1 | keep '^\s+--- (PASS|FAIL): Test/[A-Za-z0-9_]+ |^(ok|FAIL)'
  fi
  clean "$d"; git status --short
}

probe() { # $1 claim, $2 branch id, $3 repro file, $4 test regexp
  local d; d=$(worktree "$2"); clean "$d"; cd "$d" || return
  echo ">>> $1 @ $(git log --oneline -1)"
  cp "$V/repro/verify_helpers_test.go" internal/transport/zz_verify_helpers_test.go
  cp "$V/repro/$3" internal/transport/zz_verify_claim_test.go
  go test -tags verify -race -count=1 -v -run "^Test\$/^$4" $PKG 2>&1 | grep -vE '^(=== |\s+--- PASS)' | sed -E 's/^(\s+)zz_verify_claim_test\.go:[0-9]+: /\1/; s/ \([0-9.]+s\)//; s/\t[0-9.]+s$//'
  clean "$d"; git status --short
}
C3() { probe C3 8d87df46 c3_4kib_frames_copied_test.go VerifyC3; }
C4() { probe C4 53b22674 c4_alternating_frames_16kib_test.go VerifyC4; }
C5() { probe C5 c63d3773 c5_short_burst_16kib_destination_test.go VerifyC5; }

C6() {
  local d; d=$(worktree 91caeccb); clean "$d"; cd "$d" || return
  echo ">>> C6 @ $(git log --oneline -1)"
  echo ">> the measurement harness and the assertion, as delivered (internal/transport/recv_buffer_test.go)"
  grep -nE 'func heapGrowth|return 0|func measureClientTinyDataFrames|NewStream\(|before := heapAllocAfterGC|<-pingAcked|after := heapAllocAfterGC|compacted\*4 > uncompacted|case \*http2.HeadersFrame|streamID = f.StreamID|framer.WriteData' internal/transport/recv_buffer_test.go
  for g in 1 ''; do
    echo ">> unmodified branch test TestClientStream_TinyDataFramesMemory, GOMAXPROCS=${g:-default} x15"
    GOMAXPROCS=$g go test -count=15 -v -run '^Test$/^ClientStream_TinyDataFramesMemory$' $PKG 2>&1 | keep 'Heap growth|^\s+--- (PASS|FAIL): Test/[A-Za-z_]+ |^(ok|FAIL)' | sed -E 's/, [1-9][0-9]* bytes without/, >0 bytes without/; s/: [1-9][0-9]* bytes with compaction/: >0 bytes with compaction/' | tally
  done
  cp "$V/repro/c6_baseline_after_workload_test.go" internal/transport/zz_verify_claim_test.go
  for g in 1 ''; do
    echo ">> instrumented copy of the harness: DATA bytes already received when the baseline sample returns, GOMAXPROCS=${g:-default} x3"
    GOMAXPROCS=$g go test -tags verify -count=3 -v -run '^Test$/^VerifyC6_BaselineVsWorkload$' $PKG 2>&1 | keep 'BASELINE|ASSERTION|^(ok|FAIL)'
  done
  echo ">> zero-versus-zero fed to the branch's heapGrowth and assertion directly"
  go test -tags verify -count=1 -v -run '^Test$/^VerifyC6_ZeroVersusZero$' $PKG 2>&1 | keep 'heapGrowth|ASSERTION|^(ok|FAIL)'
  rm internal/transport/zz_verify_claim_test.go
  echo ">> apply mutation: compaction never happens even when enabled (repro/c6_disable_compaction_mutation.patch)"
  git apply "$V/repro/c6_disable_compaction_mutation.patch" && git diff --stat
  for g in 1 ''; do
    echo ">> mutated: unmodified branch tests, GOMAXPROCS=${g:-default} x15"
    GOMAXPROCS=$g go test -count=15 -v -run '^Test$/^(ClientStream_TinyDataFramesMemory|RecvBuffer_TinyMessagesMemory)$' $PKG 2>&1 | keep '^\s+--- (PASS|FAIL): Test/[A-Za-z_]+ |^(ok|FAIL)|Heap growth for 65535 unread bytes in 65535 DATA frames: 0 bytes with compaction, 0 bytes without' | tally
  done
  clean "$d"; git status --short
}

C7() {
  cd "$REPO" || return
  echo ">>> C7 @ $(git log --oneline -1 origin/grpc-go-transport-restrict-memory-overhead-perfect)"
  # verify/ is audit material, not part of the delivered repository: run the
  # check in a pristine detached worktree of the delivered branch.
  local d=$WT/delivered
  [ -d "$d" ] || git worktree add -q --detach "$d" origin/grpc-go-transport-restrict-memory-overhead-perfect
  cd "$d" || return
  go version
  grep -n 'gofmt' scripts/vet.sh
  echo ">> the claim's command"
  bash -o pipefail -c 'source scripts/common.sh; gofmt -s -d -l . 2>&1 | fail_on_output'; echo "exit=$?"
  echo ">> gofmt -s -l . (listing only)"
  gofmt -s -l .; echo "exit=$? listed=$(gofmt -s -l . | wc -l) of $(git ls-files '*.go' | wc -l) tracked .go files"
  echo ">> the files the solution changed"
  git diff --name-only c92e985770b7194d4a4f433c84d42c6c195e8ce5 HEAD -- '*.go' | xargs gofmt -s -l; echo "exit=$?"
  echo ">> positive control: the same command on a deliberately unsimplified file"
  printf 'package x\n\nvar a = []int{1, 2}\nvar s = a[0:len(a)]\n' > zz_verify_control.go
  bash -o pipefail -c 'source scripts/common.sh; gofmt -s -d -l . 2>&1 | fail_on_output'; echo "exit=$?"
  rm zz_verify_control.go; git status --short
}

[ $# -eq 0 ] && set -- C1 C2 C3 C4 C5 C6 C7
for c in "$@"; do "$c"; echo; done
