#!/bin/bash
# Run: verify/repro/c2_part1_delay.sh <short-branch-id> [every-second]
# C2 "closure assertion synchronization" probe (controlled scheduling). The branch's added test/xds tests are run
# (a) unmodified and (b) with the test's own trackingInterceptor.Close made to take 300ms (or 50ms with CLOSE_DELAY_MS=50).
# With "every-second" only every second Close call is slowed (for tests with two filter chains, so that interceptor
# creation for both chains is done when replacement traffic is seen). The test file is restored afterwards.
source "$(dirname "$0")/common.sh"
b=$1; mode=${2:-all}; ms=${CLOSE_DELAY_MS:-300}
d=$(dir_of "$b"); cd "$d" || exit 2
tests=$(added_tests "$d")
f=$(grep -l 'func (i \*trackingInterceptor) Close() {' test/xds/*_test.go | head -1)
cp "$f" "$OUT/c2p1.$b.orig"
log="$OUT/c2p1.$b.$mode.$ms.log"
echo "branch=$b tests=$tests mutated-file=$f" > "$log"
go test -race -count=1 -timeout 240s -run "^Test\$/^($tests)\$" ./test/xds >> "$log" 2>&1; echo "baseline exit=$?" >> "$log"
if [ "$mode" = every-second ]; then
  perl -0pi -e 's/func \(i \*trackingInterceptor\) Close\(\) \{\n/var verifyCloseCalls atomic.Int32 \/\/ VERIFY-MUTATION\n\nfunc (i *trackingInterceptor) Close() {\n\tif verifyCloseCalls.Add(1)%2 == 0 {\n\t\ttime.Sleep('$ms' * time.Millisecond) \/\/ VERIFY-MUTATION: controlled scheduling\n\t}\n/' "$f"
else
  perl -0pi -e 's/func \(i \*trackingInterceptor\) Close\(\) \{\n/func (i *trackingInterceptor) Close() {\n\ttime.Sleep('$ms' * time.Millisecond) \/\/ VERIFY-MUTATION: controlled scheduling\n/' "$f"
fi
grep -q VERIFY-MUTATION "$f" || echo "MUTATION NOT APPLIED" >> "$log"
go test -race -count=1 -timeout 240s -v -run "^Test\$/^($tests)\$" ./test/xds >> "$log" 2>&1; echo "delayed exit=$?" >> "$log"
cp "$OUT/c2p1.$b.orig" "$f"
echo "worktree dirty files after restore: $(git status --short | grep -vc '^??')" >> "$log"
grep -E "^branch=|^ok|baseline exit|^\s+--- (FAIL|PASS)|_test\.go:[0-9]+: .*(want|got|[Tt]imeout)|delayed exit|MUTATION NOT|^panic:|worktree dirty" "$log" | grep -v tlogger
