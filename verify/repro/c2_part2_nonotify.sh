#!/bin/bash
# Run: verify/repro/c2_part2_nonotify.sh <short-branch-id> [pre-existing-test-name-as-control]
# C2 "bounded callback waits" probe (injected failure). internal/xds/server/routing.go is temporarily changed so
# routing no longer invokes the interceptor: RPCs still succeed but no interceptor/callback notification is ever sent.
# The branch's added test/xds tests are run with the test binary timeout at 35s. routing.go is restored afterwards.
source "$(dirname "$0")/common.sh"
b=$1; pre=$2
d=$(dir_of "$b"); cd "$d" || exit 2
tests=$(added_tests "$d")
f=internal/xds/server/routing.go
cp "$f" "$OUT/c2p2.$b.routing.orig"
log="$OUT/c2p2.$b.log"
echo "branch=$b tests=$tests" > "$log"
sed -i 's/if err := rwi.interceptor.AllowRPC(ctx); err != nil {/if err := error(nil); err != nil { \/\/ VERIFY-MUTATION: interceptor not invoked/' "$f"
grep -q VERIFY-MUTATION "$f" || echo "MUTATION NOT APPLIED" >> "$log"
go test -count=1 -timeout 35s -v -run "^Test\$/^($tests)\$" ./test/xds >> "$log" 2>&1; echo "exit=$?" >> "$log"
if [ -n "$pre" ]; then
  echo "control=$pre" > "$OUT/c2p2.$b.control.log"
  go test -count=1 -timeout 35s -v -run "^Test\$/^${pre}\$" ./test/xds >> "$OUT/c2p2.$b.control.log" 2>&1; echo "exit=$?" >> "$OUT/c2p2.$b.control.log"
fi
cp "$OUT/c2p2.$b.routing.orig" "$f"
echo "worktree dirty files after restore: $(git status --short | grep -vc '^??')" >> "$log"
echo "leaked goroutines reported: $(grep -c 'Leaked goroutine' "$log")" >> "$log"
grep -E "^branch=|^panic: test timed out|^\s+--- (FAIL|PASS)|_test\.go:[0-9]+: .*[Tt]imeout|^exit=|MUTATION NOT|worktree dirty|leaked goroutines" "$log" | grep -v tlogger
# the test goroutine, if the binary timeout fired
grep -A2 -E '^google.golang.org/grpc/test/xds_test\.s\.Test' "$log" | grep -B1 -E 'test/xds/[a-z_]+_test\.go:[0-9]+' | head -4
[ -n "$pre" ] && grep -E "^control=|^\s+--- (FAIL|PASS)|_test\.go:[0-9]+: .*[Tt]imeout|^exit=|^panic" "$OUT/c2p2.$b.control.log" | grep -v tlogger
true
