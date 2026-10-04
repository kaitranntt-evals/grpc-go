#!/bin/bash
# C1 repro driver. Usage: bash verify/repro/c1_run.sh <worktree-of-a-C1-claim-branch> <out-dir>   (needs c1_inject_test.go next to it)
#
# For the branch checked out (detached, clean) in <worktree>, runs every test the
# branch added in internal/transport/recv_buffer*_test.go under these variants:
#   baseline               : unmodified
#   inject 0 / -1048576    : test-only patch; the heap-growth sample taken while
#                            compaction is enabled is replaced by $C1_INJECT
#   neuter                 : production compaction made a no-op (the envconfig flag
#                            still reads "enabled"), real samples
#   neuter + inject        : both. If every added test still passes, nothing other
#                            than the heap sample bounds the buffering.
# The worktree is restored (git checkout/clean) at the end.
set -u
wt=$1; out=$2; mkdir -p "$out"; out=$(cd "$out" && pwd)
here=$(cd "$(dirname "$0")" && pwd)
cd "$wt" || exit 1
git checkout -q -- . && git clean -fdq internal/
tf=$(ls internal/transport/recv_buffer*_test.go)
names=$(grep -oE '^func \(s\) (Test[A-Za-z0-9_]+)' "$tf" | awk '{print $3}' | sed 's/^Test//' | paste -sd'|')
run="^Test\$/^($names)\$"
gt() { go test -tags verify_audit -v -run "$run" google.golang.org/grpc/internal/transport -race -count=1 2>&1 | grep -E '^\s*---|_test.go:[0-9]+:|^(ok|FAIL|PASS|panic)'; }
summ() { grep -E '^(ok|FAIL)\s' "$1" | tail -1; }

echo "branch HEAD: $(git rev-parse --short HEAD)  test file: $tf" > "$out/summary.txt"
echo "go test -run '$run'" >> "$out/summary.txt"
gt > "$out/baseline.log"; echo "baseline                              : $(summ "$out/baseline.log")" >> "$out/summary.txt"

# --- test-only injection patch
cp "$here/c1_inject_test.go" internal/transport/
perl -0pi -e 's/:= (int64\(after\.HeapAlloc\) - int64\(before\.HeapAlloc\))\n/:= c1Inject($1)\n/g; s/:= (heap(?:Alloc|Size)\(\) - before)\n/:= c1Inject($1)\n/g; s/(retained) := (heapInUse\(\) - before)\n/$1 := c1InjectAuto($2)\n/g' "$tf"
if grep -q 'func heapInUse() uint64' "$tf"; then sed -i 's/c1InjectAuto(/c1InjectU(/' "$tf"; else sed -i 's/c1InjectAuto(/c1Inject(/' "$tf"; fi
git diff -- "$tf" > "$out/inject.patch"
echo "injection sites patched               : $(grep -c '^+.*c1Inject' "$out/inject.patch")" >> "$out/summary.txt"
C1_INJECT=0 gt > "$out/inject_0.log"; echo "inject 0        (solution intact)     : $(summ "$out/inject_0.log")" >> "$out/summary.txt"
C1_INJECT=-1048576 gt > "$out/inject_neg.log"; echo "inject -1048576 (solution intact)     : $(summ "$out/inject_neg.log")" >> "$out/summary.txt"

# --- neuter production compaction
if grep -q 'if b.compact && r.err == nil' internal/transport/transport.go; then
  sed -i 's/if b.compact && r.err == nil/if false \&\& b.compact \&\& r.err == nil/' internal/transport/transport.go
else
  sed -i -E '/^\s*\/\//! s/(!?)envconfig\.EnableReceiveBufferCompaction/\1(envconfig.EnableReceiveBufferCompaction \&\& false)/' internal/transport/transport.go
fi
git diff -- internal/transport/transport.go > "$out/neuter.patch"
gt > "$out/neuter_only.log"; echo "compaction neutered, real samples     : $(summ "$out/neuter_only.log")" >> "$out/summary.txt"
C1_INJECT=0 gt > "$out/neuter_inject_0.log"; echo "compaction neutered + inject 0        : $(summ "$out/neuter_inject_0.log")" >> "$out/summary.txt"
C1_INJECT=-1048576 gt > "$out/neuter_inject_neg.log"; echo "compaction neutered + inject -1048576 : $(summ "$out/neuter_inject_neg.log")" >> "$out/summary.txt"
git checkout -q -- . && git clean -fdq internal/
cat "$out/summary.txt"
