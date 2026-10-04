#!/bin/bash
# Run: verify/repro/c10/run.sh ~/wt/046d52ec   (clean worktree of evalon/grpc-go-tr-046d52ec)
# (1) runs the branch's TestReceiveBufferCompactionReads unmodified; (2) applies a production mutant that zeroes payload bytes
# compacted AFTER the first load() (i.e. only data that arrives after a read on the same live recvBuffer) and reruns it.
set -u
wt=$1; cd "$wt" || exit 1
run() { echo "### $1"; go test google.golang.org/grpc/internal/transport -race -count=1 -v -run '^Test$/^ReceiveBufferCompactionReads$' 2>&1 | grep -E '^\s*(--- (FAIL|PASS)|ok|FAIL|PASS)|recv_buffer_test.go'; }
run "1: unmodified"
python3 - <<'PY'
p='internal/transport/transport.go'
s=open(p).read()
a='\t\t\ttail.buffer = mem.SliceBuffer(append(data, r.buffer.ReadOnlyData()...))\n'
assert a in s
s=s.replace(a,'\t\t\tadd := r.buffer.ReadOnlyData()\n\t\t\tif b.c10ReadSeen {\n\t\t\t\tadd = make([]byte, len(add)) // C10 MUTATION: corrupt data compacted after a read\n\t\t\t}\n\t\t\ttail.buffer = mem.SliceBuffer(append(data, add...))\n')
a='func (b *recvBuffer) load() {\n\tb.mu.Lock()\n'
assert a in s
s=s.replace(a,a+'\tb.c10ReadSeen = true // C10 MUTATION\n')
a='\tbacklogTailOwned bool\n'
assert a in s
s=s.replace(a,a+'\tc10ReadSeen      bool // C10 MUTATION\n',1)
open(p,'w').write(s)
PY
git diff -U0 -- internal/transport/transport.go | grep -E '^[+-][^+-]'
run "2: mutant (only data put after a read is corrupted)"
git checkout -q -- .; git status --short
