#!/usr/bin/env bash
# Run: bash verify/repro/c2_2e8490f9_lognoise_probe.sh <worktree of evalon/grpc-go-se-2e8490f9> [<worktree of base 0c51461d>]
# Runs the two end2end tests whose declareLogNoise phrase was changed to
# "Server.handleStream failed to write status" with warning-level logging and no filtering,
# and greps for any "failed to write status" emission. Then temporarily instruments the unary
# completion WriteStatus in server.go to prove the same failure stage IS reached but emits nothing.
set -euo pipefail
WT=$1; BASE=${2:-}
TESTS='^Test$/^(ClientRequestBodyErrorCloseAfterLength|LargeTimeout)$'
run() { (cd "$1" && GRPC_GO_LOG_SEVERITY_LEVEL=warning go test -count=1 -v ./test -run "$TESTS" -args -verbose_logs 2>&1 | grep -E "failed to write status|C2PROBE|--- (PASS|FAIL)|^ok|^FAIL" | cut -c1-220 | sort | uniq -c); }
if [ -n "$BASE" ]; then echo "== base commit (old phrase is emitted):"; run "$BASE"; fi
echo "== 2e8490f9 as delivered:"; run "$WT"
echo "== 2e8490f9 with probe after 'err = ss.s.WriteStatus(appStatus)':"
cp "$WT/server.go" /tmp/c2_server.go.bak
python3 - "$WT/server.go" <<'EOF'
import sys
p=sys.argv[1]; s=open(p).read()
old="\terr = ss.s.WriteStatus(appStatus)\n"
assert s.count(old)==1
s=s.replace(old, old+"\tif err != nil {\n\t\tchannelz.Warningf(logger, s.channelz, \"C2PROBE: final WriteStatus failed at unary completion stage: %v\", err)\n\t}\n")
open(p,'w').write(s)
EOF
run "$WT" || true
cp /tmp/c2_server.go.bak "$WT/server.go"
echo "== emission sites of the replacement phrase on 2e8490f9:"
(cd "$WT" && grep -n "failed to write status" server.go)
