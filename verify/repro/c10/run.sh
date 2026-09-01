#!/usr/bin/env bash
# Run: bash verify/repro/c10/run.sh [worktree-dir]   (default ~/wt/a827a5ed, a checkout of evalon/grpc-go-xd-a827a5ed)
# Shows that TestHandshakeInfoRefs_CreatorReleasesWhileLoadIsBlocked "proves" the
# root load is blocked with a fixed 10ms timer only: if the handshake goroutine
# has not even entered ClientSideTLSConfig/KeyMaterial yet (mutation: 50ms delay
# before the call), the timer expires, the assertions pass, and the test is green
# without the load ever having been blocked inside KeyMaterial.
set -euo pipefail
wt=${1:-$HOME/wt/a827a5ed}
cd "$wt"
file=internal/credentials/xds/handshake_info_test.go
git diff --quiet -- "$file" || { echo "$file has local changes; refusing" >&2; exit 1; }
echo "== the 'blocked' assertion in the changed regression:"
grep -n 'case <-time.After(10 \* time.Millisecond):' "$file"
echo "== unmutated run"
go test ./internal/credentials/xds -run 'Test/HandshakeInfoRefs_CreatorReleasesWhileLoadIsBlocked' -count=1 -v 2>&1 | grep -E '^(--- |    --- |=== RUN|PASS|FAIL|ok)|handshake_info_test.go' || true
awk '!done && $0 ~ /^\t\tcfg, err := hi\.ClientSideTLSConfig\(ctx, ""\)$/ { print "\t\ttime.Sleep(50 * time.Millisecond) // C10 mutation: the load has not started when the 10ms timer fires"; done=1 } { print } END { if (!done) { print "no ClientSideTLSConfig line matched" > "/dev/stderr"; exit 1 } }' "$file" > "$file.c10tmp"
mv "$file.c10tmp" "$file"
cleanup() { git checkout -- "$file"; }
trap cleanup EXIT
echo "== mutation applied:"; git --no-pager diff -- "$file"
echo "== mutated run (x5)"
go test ./internal/credentials/xds -run 'Test/HandshakeInfoRefs_CreatorReleasesWhileLoadIsBlocked' -count=5 -v 2>&1 | grep -E '^(--- |    --- |=== RUN|PASS|FAIL|ok)|handshake_info_test.go' || true
