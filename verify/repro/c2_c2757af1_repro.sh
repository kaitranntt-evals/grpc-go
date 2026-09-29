#!/usr/bin/env bash
# Run from the grpc-go repo root: `bash verify/repro/c2_c2757af1_repro.sh` (EVAL_TESTS=<dir of extracted eval_tests.zip>/tests; default ~/eval_tests/tests).
# C2 on evalon/grpc-go-xd-c2757af1: TestSecurityConfigUpdateDuringHandshake's follow-up attempt only proves the replacement
# provider was invoked (its KeyMaterial error propagates); mutation M3 (replacement roots ignored) keeps it green while the eval fixture fails.
source "$(dirname "$0")/common.sh"
BR=evalon/grpc-go-xd-c2757af1
ensure_evalrepo "$BR"
WT=$(mktemp -d "${TMPDIR:-/tmp}/verify_c2b_XXXXXX")
trap 'cleanup_worktrees "$WT/branch"; rm -rf "$WT"' EXIT
new_worktree "$WT/branch" "evalrepo/$BR"
SEL='^Test$/^SecurityConfigUpdateDuringHandshake$'
PKG=./internal/xds/balancer/clusterimpl
TESTFILE=internal/xds/balancer/clusterimpl/security_test.go
FILTER='security_test.go:[0-9]+: [A-Z]|^panic:|security_test.go:[0-9]+ \+|^(--- |    --- |        --- |ok|FAIL|PASS)'

echo "### 1. c2757af1 test, unmodified (expect PASS)"
(cd "$WT/branch" && go test "$PKG" -run "$SEL" -count=1)
echo "### 2. eval fixture on unmodified c2757af1 (expect pass)"
run_fixture "$WT/branch"
echo "### 3. the follow-up assertions in $TESTFILE:"
(cd "$WT/branch" && grep -nE 'newProviderError|nextClientConn\)|AuthType\(\)|UnknownAuthority|unknown authority' "$TESTFILE" | sed 's/^/    /')
echo "### 4. apply mutation M2: ClientHandshake reuses the first HandshakeInfo it ever saw"
git -C "$WT/branch" apply "$REPO_ROOT/verify/repro/m2_sticky_handshakeinfo_c2757af1.patch"
echo "### 5. c2757af1 test + M2 (expect FAIL: the follow-up ClientHandshake does reach the replacement provider - provider invocation IS established)"
(cd "$WT/branch" && go test "$PKG" -run "$SEL" -count=1 -v 2>&1 | grep -E "$FILTER" || true)
echo "### 6. revert M2, apply mutation M3: ClientSideTLSConfig pins the first root pool ever fetched (replacement roots never govern a handshake)"
git -C "$WT/branch" checkout -q -- credentials/xds/xds.go
git -C "$WT/branch" apply "$REPO_ROOT/verify/repro/m3_sticky_roots_c2757af1.patch"
echo "### 7. c2757af1 test + M3 (expect PASS: the replacement provider errors before any roots are used, so root substitution is invisible)"
(cd "$WT/branch" && go test "$PKG" -run "$SEL" -count=1 -v 2>&1 | grep -E "$FILTER" || true)
echo "### 8. eval fixture + M3 (expect FAIL: follow-up RPC succeeds instead of x509 unknown authority)"
run_fixture "$WT/branch"
