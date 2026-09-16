#!/usr/bin/env bash
# Run: bash verify/repro/c9_d4d6777c_run.sh <worktree of evalon/grpc-go-se-d4d6777c>
# Runs c9_d4d6777c_recv_buffer_retention_test.go against the branch as delivered (A), then with
# serverStream.RecvMsg mutated to free the decoded buffer right after Unmarshal (B), to attribute
# the outstanding pooled buffers observed during the cardinality lookahead to RecvMsg's deferred Free.
set -uo pipefail
WT=$1; HERE=$(cd "$(dirname "$0")" && pwd)
cd "$WT"
cp "$HERE/c9_d4d6777c_recv_buffer_retention_test.go" ./c9_verify_ext_test.go
echo "== A: as delivered (defer data.Free() at RecvMsg level)"
grep -n "defer data.Free()" stream.go
go test -count=1 -v . -run '^TestC9_' 2>&1 | grep -E "C9PROBE|--- |^ok|^FAIL"
cp stream.go /tmp/c9_stream.go.bak
python3 - <<'EOF'
p='stream.go'; s=open(p).read()
old='''	defer data.Free()
	if err := ss.codec.Unmarshal(data, m); err != nil {
		format := "grpc: failed to unmarshal the received message: %v"
'''
assert s.count(old)==1
s=s.replace(old,'''	if err := ss.codec.Unmarshal(data, m); err != nil {
		data.Free() // C9 MUTATION: release immediately after unmarshalling
		format := "grpc: failed to unmarshal the received message: %v"
''')
old2='''		return status.Errorf(codes.Internal, format, err)
	}
	ss.recvFirstMsg = true
'''
assert s.count(old2)==1
s=s.replace(old2,'''		return status.Errorf(codes.Internal, format, err)
	}
	data.Free() // C9 MUTATION: release immediately after unmarshalling
	ss.recvFirstMsg = true
''')
open(p,'w').write(s)
EOF
echo "== B: mutated to free immediately after Unmarshal"
go test -count=1 -v . -run '^TestC9_' 2>&1 | grep -E "C9PROBE|--- |^ok|^FAIL"
cp /tmp/c9_stream.go.bak stream.go
rm -f c9_verify_ext_test.go
git status --short
