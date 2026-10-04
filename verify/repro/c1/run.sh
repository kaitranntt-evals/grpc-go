#!/bin/bash
# Run: verify/repro/c1/run.sh <worktree-dir-of-claim-branch>   (e.g. ~/wt/3e360711; worktree must be clean)
# For one C1 branch: (A) run its added tests unmodified, (B) neutralise compaction in production
# (enabled mode behaves like disabled), (C) B + make the enabled-mode "before" heap snapshot include a
# ballast that is dropped before "after" (=> nonpositive enabled-mode heap delta). Restores the worktree.
set -u
wt=$1; here=$(cd "$(dirname "$0")" && pwd)
cd "$wt" || exit 1
tf=$(ls internal/transport/recv_buffer*_test.go)
sub=$(grep -oE '^func \(s\) Test[A-Za-z0-9_]+' "$tf" | sed 's/^func (s) Test//' | paste -sd'|')
top=$(grep -oE '^func Test[A-Za-z0-9_]+' "$tf" | sed 's/^func //' | paste -sd'|')
run() {
  echo "### $1"
  [ -n "$sub" ] && go test google.golang.org/grpc/internal/transport -race -count=1 -v -run "^Test\$/^($sub)\$" 2>&1 | grep -E '^\s*(--- (FAIL|PASS)|ok|FAIL|PASS)|retain|[Hh]eap|outstanding|panic|\[build failed\]|\.go:[0-9]+:[0-9]+:' | grep -v '=== ' 
  [ -n "$top" ] && go test google.golang.org/grpc/internal/transport -race -count=1 -v -run "^($top)\$" 2>&1 | grep -E '^\s*(--- (FAIL|PASS)|ok|FAIL|PASS)|retain|[Hh]eap|outstanding|panic|\[build failed\]|\.go:[0-9]+:[0-9]+:' | grep -v '=== '
}
echo "===== branch worktree $wt @ $(git rev-parse --short HEAD); test file $tf"
run "A: unmodified"
sed -i 's/envconfig.EnableReceiveBufferCompaction \&\&/false \&\& envconfig.EnableReceiveBufferCompaction \&\&/' internal/transport/transport.go
git diff --stat -- internal/transport/transport.go | tail -1
run "B: production compaction neutralised (enabled mode == legacy buffering)"
cp "$here/zz_c1_ballast_test.go.txt" internal/transport/zz_c1_ballast_test.go
sed -i -E 's/runtime\.ReadMemStats\(&before\)/c1ReadBefore(\&before)/; s/before := heapInUse\(\)/before := c1Before(heapInUse)/; s/before := liveHeapBytes\(\)/before := c1Before(liveHeapBytes)/' "$tf"
git diff -U0 -- "$tf" | grep -E '^[+-][^+-]'
run "C: B + enabled-mode heap sample made nonpositive (ballast released between snapshots)"
git checkout -q -- . && rm -f internal/transport/zz_c1_ballast_test.go
git status --short | grep -v eval_ | head
