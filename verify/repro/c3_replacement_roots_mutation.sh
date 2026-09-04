#!/usr/bin/env bash
# Run: bash verify/repro/c3_replacement_roots_mutation.sh <worktree-dir-of-branch-ab232d20>
# Runs the two changed credentials/xds tests on the ab232d20 branch as written, then with the
# replacement roots swapped for the prior roots inside each test, to see whether the asserted
# follow-up handshake outcome actually depends on which roots the replacement provider supplies.
set -u
wt="$1"
f="$wt/credentials/xds/xds_client_test.go"
cp "$f" /tmp/c3_cred.bak
trap 'cp /tmp/c3_cred.bak "$f"' EXIT

run() {
  local re="$1"
  echo "--- credentials/xds -run $re"
  (cd "$wt" && go test ./credentials/xds -run "$re" -count=1 -v 2>&1 | grep -vE '^=== (RUN|PAUSE|CONT)')
}

# swap_root2 <TestName> <from> <to>: change the replacement provider's roots file inside one test only.
swap_root2() {
  TEST="$1" FROM="$2" TO="$3" python3 - "$f" <<'EOF'
import os, re, sys
fn, frm, to = os.environ['TEST'], os.environ['FROM'], os.environ['TO']
s = open(sys.argv[1]).read()
m = re.search(r'func \(s\) ' + re.escape(fn) + r'\(t \*testing\.T\) \{.*?\n\}\n', s, re.S)
body = m.group(0)
new = body.replace('root2 := makeRootProvider(t, "%s")' % frm, 'root2 := makeRootProvider(t, "%s")' % to)
assert new != body, "mutation did not apply"
open(sys.argv[1], 'w').write(s[:m.start()] + new + s[m.end():])
EOF
  echo "--- mutated line:"; diff /tmp/c3_cred.bak "$f" | grep '^>'
}

echo "===== 1. baseline (tests as written)"
run 'Test/ClientCredsHandshakeInfoClosedBeforeAcquire$'
run 'Test/ClientCredsProviderReplacedDuringHandshake$'

echo "===== 2. instrumented: log the follow-up handshake error in ProviderReplacedDuringHandshake"
python3 - "$f" <<'EOF'
import re, sys
s = open(sys.argv[1]).read()
s = re.sub(r'(if _, _, err := creds\.ClientHandshake\(hsCtx, authority, conn2\); err == nil)( \{)',
           r'\1 || func() bool { t.Logf("VERIFY follow-up handshake err: %v", err); return false }()\2', s)
open(sys.argv[1], 'w').write(s)
EOF
run 'Test/ClientCredsProviderReplacedDuringHandshake$'
cp /tmp/c3_cred.bak "$f"

echo "===== 3. mutated ClosedBeforeAcquire: replacement roots := prior roots (server_ca -> client_ca)"
swap_root2 TestClientCredsHandshakeInfoClosedBeforeAcquire x509/server_ca_cert.pem x509/client_ca_cert.pem
run 'Test/ClientCredsHandshakeInfoClosedBeforeAcquire$'
cp /tmp/c3_cred.bak "$f"

echo "===== 4. mutated ProviderReplacedDuringHandshake: replacement roots := prior roots (client_ca -> server_ca)"
swap_root2 TestClientCredsProviderReplacedDuringHandshake x509/client_ca_cert.pem x509/server_ca_cert.pem
run 'Test/ClientCredsProviderReplacedDuringHandshake$'
