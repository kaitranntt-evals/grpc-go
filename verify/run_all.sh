#!/bin/bash
# Replays every command behind verify/evidence.md and writes raw output to verify/logs/.
# Usage (from the grpc-go checkout, after verify/setup_worktrees.sh):
#   FIXTURE=/path/to/extracted/tests/eval_recv_buffer_compaction_test.go bash verify/run_all.sh
set -u
V=$(cd "$(dirname "$0")" && pwd); L=$V/logs; WT=${WT:-$HOME/wt}; mkdir -p "$L"
: "${FIXTURE:?set FIXTURE to the extracted eval fixture}"
PKG=google.golang.org/grpc/internal/transport
f() { grep -E '^\s*---|_test.go:[0-9]+:|^(ok|FAIL|PASS|panic|exit|signal)|^\s+(acquisitions|delivered|returned|NOT returned)|ThreadSanitizer|fatal error|out of memory' ; }
clean() { git -C "$1" checkout -q -- . && git -C "$1" clean -fdq internal/; }

# ---------- C3 C4 C5 C9 (evalon/grpc-go-tr-9253f1d4)
w=$WT/9253f1d4; clean $w; cd $w
cp "$FIXTURE" internal/transport/
go test -v -run '^TestEval_' $PKG -race -count=1 2>&1 | f > $L/c345_fixture_unmodified.log
rm internal/transport/eval_recv_buffer_compaction_test.go
cp $V/probes/c345_prod_construction_fixture_test.go $V/probes/c345_measure_test.go $V/repro/c9_paths_test.go internal/transport/
go test -tags verify_audit -v -run '^TestProd_' $PKG -race -count=1 2>&1 | f > $L/c345_prod_construction_fixture.log
go test -tags verify_audit -v -run '^TestC345_' $PKG -race -count=1 2>&1 | f > $L/c345_measure.log
go test -tags verify_audit -v -run '^TestC9_' $PKG -race -count=1 2>&1 | f > $L/c9_paths.log
GRPC_GO_EXPERIMENTAL_ENABLE_RECEIVE_BUFFER_COMPACTION=false go test -tags verify_audit -v -run '^TestC9_' $PKG -race -count=1 2>&1 | f > $L/c9_paths_env_false.log
clean $w

# ---------- C2 (evalon/grpc-go-tr-8b3e4b01)
w=$WT/8b3e4b01; clean $w; cd $w
go test -v -run '^Test$/^ReceiveBufferCompactionConcurrent$' $PKG -race -count=3 2>&1 | f > $L/c2_original_test.log
cp $V/repro/c2_join_test.go internal/transport/
go test -tags verify_audit -v -run '^TestC2_JoinWithHealthyWriter$' $PKG -count=1 -timeout 40s 2>&1 | f > $L/c2_healthy_writer.log
{ time go test -tags verify_audit -v -run '^TestC2_JoinWithStalledWriter$' $PKG -count=1 -timeout 40s ; } 2>&1 | grep -v '^\s*$' > $L/c2_stalled_writer_full.log
clean $w

# ---------- C6 (evalon/grpc-go-tr-a3b171be)
w=$WT/a3b171be; clean $w; cd $w
cp "$FIXTURE" internal/transport/
go test -c -race -o /tmp/c6_fixture.test ./internal/transport
for sub in UnpooledConsolidation SliceGrowthPoolOwnership ExactCapacitySmallDestination; do
  echo "### TestEval_RecvBufferConfiguredPoolAcquisition/$sub (ulimit -v 6000000, timeout 20s)"
  ( ulimit -v 6000000; GOMEMLIMIT=1GiB timeout 20 /tmp/c6_fixture.test -test.v -test.run "^TestEval_RecvBufferConfiguredPoolAcquisition\$/^$sub\$" -test.timeout 15s 2>&1 | f | head -20; echo "exit=${PIPESTATUS[0]}" )
done > $L/c6_fixture_subtests.log 2>&1
rm internal/transport/eval_recv_buffer_compaction_test.go
cp $V/probes/c6_pool_lifecycle_test.go internal/transport/
go test -tags verify_audit -v -run '^TestC6_' $PKG -race -count=1 -timeout 120s 2>&1 | f > $L/c6_pool_lifecycle.log
clean $w

# ---------- C7 (evalon/grpc-go-tr-718bb10b)
w=$WT/718bb10b; clean $w; cd $w
cp "$FIXTURE" internal/transport/
go test -v -run '^TestEval_RecvBufferCompaction_MultiCycleMemoryBound$' $PKG -race -count=1 2>&1 | f > $L/c7_fixture_multicycle.log
rm internal/transport/eval_recv_buffer_compaction_test.go
cp $V/repro/c7_sustained_backlog_test.go internal/transport/
go test -tags verify_audit -v -run '^TestC7_' $PKG -race -count=1 2>&1 | f > $L/c7_default.log
GRPC_GO_EXPERIMENTAL_ENABLE_RECEIVE_BUFFER_COMPACTION=false go test -tags verify_audit -v -run '^TestC7_' $PKG -race -count=1 2>&1 | f > $L/c7_env_false.log
clean $w

# ---------- C8 (evalon/grpc-go-tr-37793f8a)
w=$WT/37793f8a; clean $w; cd $w
{
  echo "\$ go version"; go version
  echo "\$ git rev-parse HEAD; git status --short | wc -l"; git rev-parse HEAD; git status --short | wc -l
  echo "\$ gofmt -s -d -l internal/transport/recv_buffer_compaction_test.go; echo exit=\$?"
  gofmt -s -d -l internal/transport/recv_buffer_compaction_test.go; echo "exit=$?"
  echo "\$ gofmt -s -d -l internal/transport/recv_buffer_compaction_test.go | wc -c"
  gofmt -s -d -l internal/transport/recv_buffer_compaction_test.go | wc -c
  echo "\$ git show HEAD:internal/transport/recv_buffer_compaction_test.go | gofmt -s -d | wc -c"
  git show HEAD:internal/transport/recv_buffer_compaction_test.go | gofmt -s -d | wc -c
  echo "\$ gofmt -s -l internal/transport internal/envconfig internal/mem mem | wc -l"
  gofmt -s -l internal/transport internal/envconfig internal/mem mem | wc -l
  echo "\$ grep -n 'putTiny(100)\|put(large(' internal/transport/recv_buffer_compaction_test.go"
  grep -n 'putTiny(100)\|put(large(' internal/transport/recv_buffer_compaction_test.go
  echo "\$ # positive control: de-align the trailing comments on the putTiny lines in a scratch copy"
  mkdir -p /tmp/c8 && sed 's|^\(\s*putTiny(.*)\) *// |\1 // |' internal/transport/recv_buffer_compaction_test.go > /tmp/c8/ctl_test.go
  echo "\$ gofmt -s -d -l /tmp/c8/ctl_test.go"
  gofmt -s -d -l /tmp/c8/ctl_test.go
} > $L/c8_gofmt.log 2>&1

# ---------- C10 (evalon/grpc-go-tr-d4a3af3a)
w=$WT/d4a3af3a; clean $w; cd $w
cp "$FIXTURE" internal/transport/
go test -v -run '^TestEval_RecvBufferConfiguredPoolAcquisition$' $PKG -race -count=1 2>&1 | f > $L/c10_fixture.log
rm internal/transport/eval_recv_buffer_compaction_test.go
cp $V/repro/c10_cap1024_test.go internal/transport/
go test -tags verify_audit -v -run '^TestC10_' $PKG -race -count=1 2>&1 | f > $L/c10_cap1024.log
clean $w

# ---------- C1 (ten branches)
for b in 2b0ddded 5ccf77e7 ae31c146 2994fc5c d2e82310 d0aa7e9e 4c52c410 72e9069b 3e551c7b 17011fad; do
  bash $V/repro/c1_run.sh $WT/$b $L/c1/$b > /dev/null 2>&1
done
echo done
