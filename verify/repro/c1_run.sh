#!/bin/bash
# Run (after setup_worktrees.sh): verify/repro/c1_run.sh /tmp/claims/<suffix> [extra-test-names-regex]   -- baseline, then the C1 post-consumption mutant under 3 schedules; restores the worktree
# Optional: EVAL_FIXTURE=<path to eval_recv_buffer_compaction_test.go> also runs the eval fixture under the mutant. Logs go to ${OUT:-/tmp/c1-out}/<suffix>/.
here=$(cd "$(dirname "$0")" && pwd)
wt=$1; b=$(basename "$wt"); out=${OUT:-/tmp/c1-out}/$b; mkdir -p "$out"
cd "$wt" || exit 1
git checkout -q -- . ; rm -f internal/transport/eval_recv_buffer_compaction_test.go
tests=$(grep -ho '^func (s) Test[A-Za-z0-9_]*' internal/transport/recv_buffer_test.go | sed 's/func (s) Test//' | paste -sd'|'); [ -n "$2" ] && tests="$tests|$2"
run="^Test\$/^($tests)\$"
{
echo "BRANCH evalon/grpc-go-tr-$b @ $(git rev-parse --short HEAD)"
echo "TESTS: $tests"
go test ./internal/transport -run "$run" -count=1 -v > "$out/baseline.log" 2>&1; echo "### baseline (unmutated), -count=1: exit=$?"; python3 "$here/c1_attr.py" "$out/baseline.log"
python3 "$here/c1_mutant.py" internal/transport/transport.go
gofmt -w internal/transport/transport.go
git diff > "$out/mutant.patch"
go vet ./internal/transport 2>&1 | head -5
go test ./internal/transport -run "$run" -count=10 -v > "$out/mutant_default.log" 2>&1; echo "### mutant, default GOMAXPROCS, -count=10: exit=$?"; python3 "$here/c1_attr.py" "$out/mutant_default.log"
go test ./internal/transport -run "$run" -count=10 -v -cpu 1 > "$out/mutant_cpu1.log" 2>&1; echo "### mutant, -cpu 1, -count=10: exit=$?"; python3 "$here/c1_attr.py" "$out/mutant_cpu1.log"
C1_READER_DELAY_MS=300 go test ./internal/transport -run "$run" -count=5 -v > "$out/mutant_delay.log" 2>&1; echo "### mutant, C1_READER_DELAY_MS=300 (reader's first read delayed), -count=5: exit=$?"; python3 "$here/c1_attr.py" "$out/mutant_delay.log"
if [ -n "$EVAL_FIXTURE" ]; then
  cp "$EVAL_FIXTURE" internal/transport/eval_recv_buffer_compaction_test.go
  go test ./internal/transport -run '^TestEval_' -count=1 -v > "$out/mutant_fixture.log" 2>&1; echo "### mutant, eval fixture (archive copy), -count=1: exit=$?"; python3 "$here/c1_attr.py" "$out/mutant_fixture.log"
  rm -f internal/transport/eval_recv_buffer_compaction_test.go
fi
git checkout -q -- .
} > "$out/summary.txt" 2>&1
cat "$out/summary.txt"
