#!/usr/bin/env bash
# Run: bash verify/repro/c12/run.sh [worktree-dir]   (default ~/wt/29066cc7, a checkout of evalon/grpc-go-xd-29066cc7)
# 1) Inventories the tests that touch the custom ownership state machine
#    (acquire/release/retire with the high retiredBit in internal/credentials/xds).
# 2) Applies two state-machine mutations that a focused unit test would catch and
#    shows the branch's test suite stays green for both:
#    M1: acquire() no longer rejects a retired HandshakeInfo.
#    M2: release() closes the providers on every post-retirement release
#        (cleanup no longer exactly-once).
set -euo pipefail
wt=${1:-$HOME/wt/29066cc7}
cd "$wt"
file=internal/credentials/xds/handshake_info.go
git diff --quiet -- "$file" || { echo "$file has local changes; refusing" >&2; exit 1; }
pkgs="./internal/credentials/xds ./credentials/xds ./internal/xds/balancer/clusterimpl/..."

echo "== inventory: test files referencing the state machine API"
grep -rln 'AcquireHandshakeInfo\|UpdateHandshakeInfo\|\.retire()\|\.acquire()\|\.release()\|retiredBit' --include='*_test.go' . || echo "(none)"
echo "== inventory: dedicated tests in internal/credentials/xds"
ls internal/credentials/xds/*_test.go 2>/dev/null || echo "(no *_test.go in internal/credentials/xds)"
echo "== inventory: tests changed vs base cc234554 mentioning retire/acquire/release/underflow/concurrent"
git diff cc234554fb363aea445a838b341bb8a65c8305b0 -- '*_test.go' | grep -E '^\+.*(retire|acquire|release|underflow|concurrent|twice|exactly)' || echo "(no direct assertions on these states)"

run() { go test $pkgs -count=1 2>&1 | grep -E '^(ok|FAIL|---|panic)' || true; }
cleanup() { git checkout -- "$file"; }
trap cleanup EXIT

echo "== baseline run"; run

echo "== M1: acquire() accepts retired HandshakeInfo"
awk '!done && $0 ~ /^\t\tif refs&retiredBit != 0 \{$/ { print "\t\tif false { // C12 mutation M1"; done=1; next } { print } END { if (!done) exit 1 }' "$file" > "$file.tmp" && mv "$file.tmp" "$file"
git --no-pager diff -- "$file" | grep -E '^[-+]\s' ; run
git checkout -- "$file"

echo "== M2: release() closes providers on every post-retirement release"
awk '!done && $0 ~ /^\tif refs == retiredBit \{$/ { print "\tif refs&retiredBit != 0 { // C12 mutation M2"; done=1; next } { print } END { if (!done) exit 1 }' "$file" > "$file.tmp" && mv "$file.tmp" "$file"
git --no-pager diff -- "$file" | grep -E '^[-+]\s' ; run
