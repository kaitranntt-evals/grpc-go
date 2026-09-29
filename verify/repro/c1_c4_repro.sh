#!/usr/bin/env bash
# Run from the grpc-go repo root: `bash verify/repro/c1_c4_repro.sh` (EVAL_TESTS=<dir of extracted eval_tests.zip>/tests; default ~/eval_tests/tests).
# C1/C4 on evalon/grpc-go-xd-ec8b5774: shows TestSecurityConfigUpdate_DuringClientHandshake releases the blocked
# root load on a construction signal + 100ms timer, not on applied replacement (mutation M1 keeps it green, even on the buggy base).
source "$(dirname "$0")/common.sh"
BR=evalon/grpc-go-xd-ec8b5774
ensure_evalrepo "$BR"
WT=$(mktemp -d "${TMPDIR:-/tmp}/verify_c1c4_XXXXXX")
trap 'cleanup_worktrees "$WT/branch" "$WT/base"; rm -rf "$WT"' EXIT
new_worktree "$WT/branch" "evalrepo/$BR"
new_worktree "$WT/base" "$BASE_COMMIT"
cp "$WT/branch/internal/xds/balancer/clusterimpl/tests/clusterimpl_security_test.go" "$WT/base/internal/xds/balancer/clusterimpl/tests/"
SEL='^Test$/^SecurityConfigUpdate_DuringClientHandshake$'
PKG=./internal/xds/balancer/clusterimpl/tests
FILTER='MUTATION|SubChannel #[0-9]+\] Subchannel Connectivity change to READY|clusterimpl_security_test.go:[0-9]+: (Old|Timeout|EmptyCall)|^(--- |    --- |ok|FAIL|PASS)'

echo "### 1. ec8b5774 test, unmodified branch (expect PASS)"
(cd "$WT/branch" && go test "$PKG" -run "$SEL" -count=1)
echo "### 2. ec8b5774 test on the buggy base commit $BASE_COMMIT (expect FAIL: it does detect the original premature close)"
(cd "$WT/base" && go test "$PKG" -run "$SEL" -count=1 -v 2>&1 | grep -E "$FILTER" | grep -v tlogger || true)
echo "### 3. the lifetime unit test TestHandshakeInfo_AcquireReleaseClose never loads validation roots (KeyMaterial occurrences in the test body):"
(cd "$WT/branch" && awk '/^func \(s\) TestHandshakeInfo_AcquireReleaseClose/,/^}/' internal/credentials/xds/handshake_info_test.go | grep -c KeyMaterial || true)
echo "### 4. apply mutation M1: balancer sleeps 500ms before applying (swapping/closing) the replacement security config"
git -C "$WT/branch" apply "$REPO_ROOT/verify/repro/m1_delay_replacement_ec8b5774.patch"
git -C "$WT/base"   apply "$REPO_ROOT/verify/repro/m1_delay_replacement_base.patch"
echo "### 5. ec8b5774 test + M1 on the branch (expect PASS; note SubChannel READY (handshake done) precedes the 2nd MUTATION line (replacement applied))"
(cd "$WT/branch" && go test "$PKG" -run "$SEL" -count=1 -v 2>&1 | grep -E "$FILTER" | sed -E 's/^ *tlogger.go:[0-9]+: INFO //' || true)
echo "### 6. ec8b5774 test + M1 on the BUGGY base (expect PASS: the test is green on code that still closes providers under a live handshake)"
(cd "$WT/base" && go test "$PKG" -run "$SEL" -count=1 -v 2>&1 | grep -E "$FILTER" | sed -E 's/^ *tlogger.go:[0-9]+: INFO //' || true)
echo "### 7. eval fixture + M1 on the BUGGY base (expect FAIL: it waits for resolver.ClientConn.UpdateState to return before releasing the load)"
run_fixture "$WT/base"
