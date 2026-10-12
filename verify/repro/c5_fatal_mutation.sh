#!/bin/bash
# Run from the root of a checkout of branch evalon/grpc-go-xd-aa7e5dee: bash c5_fatal_mutation.sh
# 1) control: runs the probes against unmodified production code (branch test passes, nothing left open)
# 2) reintroduces the leak the branch test guards against, so one of its t.Fatalf assertions fires
#    after the TCP listener is created and before the explicit lw.Close(); re-runs the probes. Restores the file.
set -u
here=$(cd "$(dirname "$0")" && pwd)
cp "$here/c5_fatal_before_close_leaks_listener_test.go" internal/xds/server/
run() { go test -race -v -count=1 -run '^Test$/^VerifyC5_' ./internal/xds/server 2>&1 | grep -E '^\s*(--- |PASS|FAIL|ok )|OBSERVED|_test.go:[0-9]+: (Update|After)'; }
echo "### grep for cleanup registrations in the branch test file (defer / t.Cleanup / lw.Close / lis.Close):"
grep -nE 'defer|Cleanup|\.Close\(\)|LocalTCPListener|t\.Fatalf' internal/xds/server/listener_wrapper_test.go | grep -vE '^\s*[0-9]+:\s*//' 
echo "### 1) control: unmodified production code"
run
f=internal/xds/server/listener_wrapper.go
cp "$f" /tmp/c5_lw.orig
python3 - "$f" <<'PY'
import sys
p=sys.argv[1]; s=open(p).read()
old="			fc.usableRouteConfiguration.Swap(urc).close()\n"
i=s.index("func (l *listenerWrapper) handleRDSUpdate")
j=s.index(old,i)
s=s[:j]+"			fc.usableRouteConfiguration.Swap(urc) // MUTATION: superseded interceptors no longer closed\n"+s[j+len(old):]
open(p,"w").write(s)
PY
echo "### 2) mutation applied:"
git --no-pager diff -- "$f" | grep '^[+-]'
run
cp /tmp/c5_lw.orig "$f"
git status --short
