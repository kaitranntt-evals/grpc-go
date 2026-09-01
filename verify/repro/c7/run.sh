#!/usr/bin/env bash
# Run: bash verify/repro/c7/run.sh [worktree-dir]   (default ~/wt/b7c0f5e6, a checkout of evalon/grpc-go-xd-b7c0f5e6)
# Inserts a test hook in singleCloseWrappedProvider.Close between `w.mu.Unlock()`
# and `w.closeProvider()`, runs c7_probe_test.go in credentials/tls/certprovider, then reverts.
set -euo pipefail
wt=${1:-$HOME/wt/b7c0f5e6}
here=$(cd "$(dirname "$0")" && pwd)
cd "$wt"
file=credentials/tls/certprovider/store.go
git diff --quiet -- "$file" || { echo "$file has local changes; refusing" >&2; exit 1; }
awk 'inClose && !done && $0 == "\tw.mu.Unlock()" { print; print "\tif h := C7TestHookBeforeCloseProvider; h != nil {"; print "\t\th()"; print "\t}"; done=1; next }
     $0 ~ /^func \(w \*singleCloseWrappedProvider\) Close\(\)/ { inClose=1 }
     { print } END { if (!done) { print "no unlock line matched" > "/dev/stderr"; exit 1 } }' "$file" > "$file.c7tmp"
mv "$file.c7tmp" "$file"
printf '\n// C7TestHookBeforeCloseProvider is verification instrumentation (verify/repro/c7); nil in production.\nvar C7TestHookBeforeCloseProvider func()\n' >> "$file"
echo "== instrumentation applied:"; git --no-pager diff -- "$file"
cp "$here/c7_probe_test.go" credentials/tls/certprovider/
cp "$here/c7_server_exposure_probe_test.go" internal/xds/server/
cleanup() { rm -f credentials/tls/certprovider/c7_probe_test.go internal/xds/server/c7_server_exposure_probe_test.go; git checkout -- "$file"; }
trap cleanup EXIT
echo "== go test -tags verifyrepro ./credentials/tls/certprovider ./internal/xds/server -run 'Test/C7Probe' -count=1 -v"
go test -tags verifyrepro ./credentials/tls/certprovider ./internal/xds/server -run 'Test/C7Probe' -count=1 -v 2>&1 | grep -E 'C7PROBE|--- |^(PASS|FAIL|ok)|panic|\.go:[0-9]+:' || true
