# Run: bash verify/repro/c9_preparemsg_probe.sh <path-to-worktree-on-branch-evalon/grpc-go-se-6275649d> <path-to-eval_tests/tests>
# C9: temporarily instruments both prepareMsg implementations in stream.go and
# runs a unary round trip: the server response is prepared by the independent
# serverStream.prepareMsg method while the client uses package-level prepareMsg,
# proving two parallel message-preparation implementations are live.
set -x
WT="${1:?worktree path}"
FIX="${2:?fixtures path}"
cd "$WT"
python3 - <<'EOF'
src = open('stream.go').read()
src = src.replace("func (ss *serverStream) prepareMsg(m any) (hdr []byte, data, payload mem.BufferSlice, pf payloadFormat, err error) {",
"func (ss *serverStream) prepareMsg(m any) (hdr []byte, data, payload mem.BufferSlice, pf payloadFormat, err error) {\n\tfmt.Fprintln(os.Stderr, \"C9-PROBE: serverStream.prepareMsg called\")",1)
src = src.replace("func prepareMsg(m any, codec baseCodec, cp Compressor, comp encoding.Compressor, pool mem.BufferPool) (hdr []byte, data, payload mem.BufferSlice, pf payloadFormat, err error) {",
"func prepareMsg(m any, codec baseCodec, cp Compressor, comp encoding.Compressor, pool mem.BufferPool) (hdr []byte, data, payload mem.BufferSlice, pf payloadFormat, err error) {\n\tfmt.Fprintln(os.Stderr, \"C9-PROBE: package-level prepareMsg called\")",1)
src = src.replace('import (', 'import (\n\t"os"',1)
open('stream.go','w').write(src)
EOF
cp "$FIX/eval_unary_roundtrip_test.go" test/
go test ./test -run 'TestEval_UnaryRoundTrip' -count=1 -v 2>&1 | grep -E "C9-PROBE|^--- |^ok"
git checkout -- stream.go
rm test/eval_unary_roundtrip_test.go
