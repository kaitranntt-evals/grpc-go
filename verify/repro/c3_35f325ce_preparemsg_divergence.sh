#!/usr/bin/env bash
# Run: bash verify/repro/c3_35f325ce_preparemsg_divergence.sh <worktree of evalon/grpc-go-se-35f325ce>
# Mutates the PreparedMsg branch of the shared package-level prepareMsg (stream.go) to fail.
# If the server send path delegated to it, TestPreloaderSenderSend (server sends a PreparedMsg)
# would fail like TestPreloaderClientSend does. It keeps passing because serverStream.prepareMsg
# re-implements PreparedMsg selection / encode / compress / msgHeader on its own.
set -uo pipefail
WT=$1
cd "$WT"
echo "== live preparation implementations:"
grep -n "func prepareMsg\|func (ss \*serverStream) prepareMsg\|prepareMsg(m\|m.(\*PreparedMsg)" stream.go
cp stream.go /tmp/c3_stream.go.bak
python3 - <<'EOF'
p='stream.go'; s=open(p).read()
old='''func prepareMsg(m any, codec baseCodec, cp Compressor, comp encoding.Compressor, pool mem.BufferPool) (hdr []byte, data, payload mem.BufferSlice, pf payloadFormat, err error) {
	if preparedMsg, ok := m.(*PreparedMsg); ok {
		return preparedMsg.hdr, preparedMsg.encodedData, preparedMsg.payload, preparedMsg.pf, nil
	}'''
assert s.count(old)==1
new='''func prepareMsg(m any, codec baseCodec, cp Compressor, comp encoding.Compressor, pool mem.BufferPool) (hdr []byte, data, payload mem.BufferSlice, pf payloadFormat, err error) {
	if _, ok := m.(*PreparedMsg); ok {
		return nil, nil, nil, 0, status.Error(codes.Internal, "C3 MUTATION: shared prepareMsg PreparedMsg branch disabled")
	}'''
open(p,'w').write(s.replace(old,new))
EOF
echo "== with shared prepareMsg's PreparedMsg branch disabled:"
go test -count=1 -v ./test -run '^Test$/^Preloader(ClientSend|SenderSend)$' 2>&1 | grep -E "^\s*--- (PASS|FAIL)|C3 MUTATION|^ok|^FAIL" | cut -c1-200 | sort | uniq -c
cp /tmp/c3_stream.go.bak stream.go
git status --short
