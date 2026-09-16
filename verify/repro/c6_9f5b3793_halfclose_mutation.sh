#!/usr/bin/env bash
# Run: bash verify/repro/c6_9f5b3793_halfclose_mutation.sh <worktree of evalon/grpc-go-se-9f5b3793> <path to tests/eval_unary_halfclose_test.go>
# 1) Logs, from the client transport write, whether the authored test's single SendMsg carries
#    Last=true (END_STREAM, i.e. an automatic half-close) because its StreamDesc has ClientStreams=false.
# 2) Mutates the server so unary RecvMsg again waits for the client half-close (reverts
#    `|| ss.unary` in serverStream.RecvMsg). The authored test keeps passing (its request side is
#    already closed) while the eval fixture, which uses ClientStreams:true, hangs/fails.
set -uo pipefail
WT=$1; FIXTURE=$2
cd "$WT"
cp stream.go /tmp/c6_stream.go.bak
python3 - <<'EOF'
p='stream.go'; s=open(p).read()
old='\tif err := a.transportStream.Write(hdr, payld, &transport.WriteOptions{Last: !cs.desc.ClientStreams}); err != nil {\n'
assert s.count(old)==1
s=s.replace(old, '\tlogger.Warningf("C6PROBE client SendMsg method=%s ClientStreams=%v -> transport write Last=%v", cs.callHdr.Method, cs.desc.ClientStreams, !cs.desc.ClientStreams)\n'+old)
open(p,'w').write(s)
EOF
echo "== probe only: what does the authored test's SendMsg write?"
GRPC_GO_LOG_SEVERITY_LEVEL=warning go test -count=1 -v ./test -run '^Test$/^ServerPipeline_UnaryRespondsBeforeHalfClose$' 2>&1 | grep -E "C6PROBE|--- (PASS|FAIL)|^ok|^FAIL" | cut -c1-200
python3 - <<'EOF'
p='stream.go'; s=open(p).read()
old='\tif ss.desc.ClientStreams || ss.unary {\n'
assert s.count(old)==1
s=s.replace(old, '\tif ss.desc.ClientStreams { // C6 MUTATION: unary RecvMsg waits for client half-close again\n')
open(p,'w').write(s)
EOF
echo "== mutation: server unary RecvMsg waits for half-close; authored test:"
go test -count=1 -v ./test -run '^Test$/^ServerPipeline_UnaryRespondsBeforeHalfClose$' -timeout 60s 2>&1 | grep -E -e "--- (PASS|FAIL)|^ok|^FAIL|panic: test timed out" | cut -c1-200
echo "== same mutation; eval fixture TestEval_UnaryWithoutHalfClose (ClientStreams:true):"
cp "$FIXTURE" test/eval_unary_halfclose_test.go
go test -count=1 -v ./test -run '^TestEval_UnaryWithoutHalfClose$' -timeout 60s 2>&1 | grep -E -e "--- (PASS|FAIL)|^ok|^FAIL|panic: test timed out|eval_unary_halfclose_test.go" | cut -c1-200
rm -f test/eval_unary_halfclose_test.go
cp /tmp/c6_stream.go.bak stream.go
git status --short
