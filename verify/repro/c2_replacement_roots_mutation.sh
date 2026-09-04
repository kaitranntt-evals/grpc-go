#!/usr/bin/env bash
# Run: bash verify/repro/c2_replacement_roots_mutation.sh <worktree-dir> <cred-test-regex> <clusterimpl-pkg> <clusterimpl-test-regex> <clusterimpl-test-file>
#
# C2 probe. Runs the branch's new "replacement roots" tests three ways:
#   1. baseline           : unmodified tests (should pass),
#   2. instrumented       : same tests with the follow-up connection error logged
#                           (so the actual failure reason is visible),
#   3. mutated            : the *replacement* provider is given the SAME roots as the
#                           prior provider (server_ca_cert.pem instead of
#                           client_ca_cert.pem). If the follow-up assertion still passes,
#                           the asserted failure is not caused by the replacement roots
#                           (claim CONFIRMED); if it now fails, the assertion
#                           distinguishes replacement roots from prior roots (REFUTED).
set -uo pipefail
wt="$1"; credRe="$2"; ciPkg="$3"; ciRe="$4"; ciFile="$5"
cd "$wt"
credFile=credentials/xds/xds_client_test.go
cp "$credFile" /tmp/c2_cred.bak; cp "$ciFile" /tmp/c2_ci.bak
restore() { cp /tmp/c2_cred.bak "$credFile"; cp /tmp/c2_ci.bak "$ciFile"; }
trap restore EXIT

run() {
  echo "--- credentials/xds -run $credRe"
  go test ./credentials/xds -run "$credRe" -count=1 -v 2>&1 | grep -E "VERIFY|^(=== RUN|--- |ok|FAIL|PASS)|_test.go:[0-9]+:" | grep -v "tlogger"
  echo "--- $ciPkg -run $ciRe"
  go test "$ciPkg" -run "$ciRe" -count=1 -v 2>&1 | grep -E "VERIFY|^(--- |ok|FAIL|PASS)|_test.go:[0-9]+:" | grep -v "tlogger"
}

echo "===== 1. baseline"; run

echo "===== 2. instrumented (log follow-up error)"
ciFunc="${ciRe#Test/}"; ciFunc="${ciFunc%\$}"
CI_FUNC="$ciFunc" python3 - "$credFile" "$ciFile" <<'EOF'
import re, sys, os
fn = os.environ['CI_FUNC']
for f in sys.argv[1:]:
    s = open(f).read()
    s = re.sub(r'(if _, _, err := creds\.ClientHandshake\([^\n]*\); err == nil)( \{)',
               r'\1 || func() bool { t.Logf("VERIFY follow-up handshake err: %v", err); return false }()\2', s)
    s = re.sub(r'(if _, err := client\.EmptyCall\(ctx, &testpb\.Empty\{\}\); status\.Code\(err\) != codes\.Unavailable)( \{)',
               r'\1 || func() bool { t.Logf("VERIFY follow-up RPC err: %v", err); return false }()\2', s)
    m = re.search(r'func \(s\) Test' + re.escape(fn) + r'\(t \*testing\.T\) \{.*?\n\}\n', s, re.S)
    if m:
        body = re.sub(r'(\ttestutils\.AwaitState\(ctx, t, cc, connectivity\.TransientFailure\)\n)(\}\n)',
               r'\1\tif _, err := client.EmptyCall(ctx, &testpb.Empty{}); err != nil {\n\t\tt.Logf("VERIFY follow-up RPC err: %v", err)\n\t}\n\2', m.group(0))
        s = s[:m.start()] + body + s[m.end():]
    open(f, 'w').write(s)
EOF
run

echo "===== 3. mutated (replacement roots == prior roots: client_ca_cert.pem -> server_ca_cert.pem in replacement provider)"
sed -i '/ReadFile/! s#x509/client_ca_cert\.pem#x509/server_ca_cert.pem#g' "$credFile" "$ciFile"
echo "--- mutated lines:"; diff /tmp/c2_cred.bak "$credFile" | grep '^>' | grep server_ca; diff /tmp/c2_ci.bak "$ciFile" | grep '^>' | grep server_ca
run
