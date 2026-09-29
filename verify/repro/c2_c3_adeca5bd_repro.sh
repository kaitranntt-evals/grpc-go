#!/usr/bin/env bash
# Run from the grpc-go repo root: `bash verify/repro/c2_c3_adeca5bd_repro.sh` (EVAL_TESTS=<dir of extracted eval_tests.zip>/tests; default ~/eval_tests/tests).
# C2/C3 on evalon/grpc-go-xd-adeca5bd: TestSecurityConfigUpdate_HandshakeInProgress never performs a connection attempt after
# the replacement; mutation M2 (ClientHandshake pins the first HandshakeInfo forever) keeps it green while the eval fixture fails.
source "$(dirname "$0")/common.sh"
BR=evalon/grpc-go-xd-adeca5bd
ensure_evalrepo "$BR"
WT=$(mktemp -d "${TMPDIR:-/tmp}/verify_c2c3_XXXXXX")
trap 'cleanup_worktrees "$WT/branch"; rm -rf "$WT"' EXIT
new_worktree "$WT/branch" "evalrepo/$BR"
SEL='^Test$/^(SecurityConfigUpdate_HandshakeInProgress|HandshakeSafeProvider)$'
PKG=./internal/xds/balancer/clusterimpl
TESTFILE=internal/xds/balancer/clusterimpl/balancer_test.go
FILTER='balancer_test.go:[0-9]+: [A-Z]|^(--- |    --- |ok|FAIL|PASS)'

echo "### 1. adeca5bd tests, unmodified (expect PASS)"
(cd "$WT/branch" && go test "$PKG" -run "$SEL" -count=1)
echo "### 2. eval fixture on unmodified adeca5bd (expect pass)"
run_fixture "$WT/branch"
echo "### 3. connection-attempt vocabulary in the ADDED test lines of $TESTFILE (ClientHandshake/Dial/NewClient/net.Pipe/tls.Client/EmptyCall/UnknownAuthority):"
(cd "$WT/branch" && git diff "$BASE_COMMIT" HEAD -- "$TESTFILE" | grep -E '^\+' | grep -cE 'ClientHandshake\(|grpc\.Dial|grpc\.NewClient|net\.Pipe|tls\.Client|EmptyCall|UnknownAuthority|unknown authority' || true)
echo "### 3b. what the follow-up assertions actually are:"
(cd "$WT/branch" && git diff "$BASE_COMMIT" HEAD -- "$TESTFILE" | grep -nE 'ClientSideTLSConfig\(|RootCAs\.Equal' | sed 's/^/    /')
echo "### 4. apply mutation M2: credentials/xds ClientHandshake reuses the first HandshakeInfo it ever saw (replacement never governs any connection)"
git -C "$WT/branch" apply "$REPO_ROOT/verify/repro/m2_sticky_handshakeinfo_adeca5bd.patch"
echo "### 5. adeca5bd tests + M2 (expect PASS: the tests never go through ClientHandshake, so they cannot notice)"
(cd "$WT/branch" && go test "$PKG" -run "$SEL" -count=1 -v 2>&1 | grep -E "$FILTER" || true)
echo "### 6. eval fixture + M2 (expect FAIL: the follow-up RPC never reaches the replacement provider)"
run_fixture "$WT/branch"
echo "### 7. revert M2, apply mutation M3: ClientSideTLSConfig pins the first root pool ever fetched"
git -C "$WT/branch" checkout -q -- credentials/xds/xds.go
git -C "$WT/branch" apply "$REPO_ROOT/verify/repro/m3_sticky_roots_adeca5bd.patch"
echo "### 8. adeca5bd tests + M3 (expect FAIL at the RootCAs.Equal(roots2) helper check: the helper-equality assertion does see root substitution, but through no connection)"
(cd "$WT/branch" && go test "$PKG" -run "$SEL" -count=1 -v 2>&1 | grep -E "$FILTER" || true)
