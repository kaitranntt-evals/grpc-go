#!/usr/bin/env bash
# Run: bash verify/repro/c4/run.sh [worktree-dir]   (default ~/wt/perfect, a checkout of the perfect branch)
# Inserts a test hook right after `hiRC := hiPtr.Load()` in ClientSideTLSConfig,
# runs verify/repro/c4/c4_probe_test.go in internal/credentials/xds, then reverts.
set -euo pipefail
wt=${1:-$HOME/wt/perfect}
here=$(cd "$(dirname "$0")" && pwd)
cd "$wt"
file=internal/credentials/xds/handshake_info.go
git diff --quiet -- "$file" || { echo "$file has local changes; refusing" >&2; exit 1; }
awk '!done && $0 ~ /^\t\thiRC := hiPtr\.Load\(\)$/ { print; print "\t\tif h := C4TestHookAfterLoad; h != nil {"; print "\t\t\th(hiRC)"; print "\t\t}"; done=1; next } { print } END { if (!done) { print "no load line matched" > "/dev/stderr"; exit 1 } }' "$file" > "$file.c4tmp"
mv "$file.c4tmp" "$file"
printf '\n// C4TestHookAfterLoad is verification instrumentation (verify/repro/c4); nil in production.\nvar C4TestHookAfterLoad func(*grpcsync.RefCounted[HandshakeInfo])\n' >> "$file"
echo "== instrumentation applied:"; git --no-pager diff -- "$file"
cp "$here/c4_probe_test.go" internal/credentials/xds/
cleanup() { rm -f internal/credentials/xds/c4_probe_test.go; git checkout -- "$file"; }
trap cleanup EXIT
echo "== go test -tags verifyrepro ./internal/credentials/xds -run 'Test/C4Probe' -count=1 -v"
go test -tags verifyrepro ./internal/credentials/xds -run 'Test/C4Probe' -count=1 -v 2>&1 | grep -E 'C4PROBE|--- |^(PASS|FAIL|ok)|panic|\.go:[0-9]+:' || true
