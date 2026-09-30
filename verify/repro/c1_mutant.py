#!/usr/bin/env python3
# Run: python3 verify/repro/c1_mutant.py <claim-worktree>/internal/transport/transport.go   (applied and reverted automatically by verify/repro/c1_run.sh)
"""Apply the C1 'post-consumption' mutant to internal/transport/transport.go.

Consumption is detected precisely: only a reader removes messages from b.c, so
finding b.c empty on entry to load() or put() after at least one earlier put()
means a message was consumed (load() calls made while the channel is still
full are not consumption and are ignored). After that every later
data put() has its payload replaced by 0xEE bytes and every later terminal
put() has its error replaced by errC1Mutant. A test that asserts payload or
terminal state across receive -> consume -> receive/terminal on one live
buffer must fail under this mutant.
Usage: mutate_c1.py <path to transport.go>
"""
import re, sys
p = sys.argv[1]
s = open(p).read()
m = re.search(r'type recvBuffer struct \{\n', s)
assert m
s = s[:m.end()] + "\tmutConsumed bool // C1 mutant\n\tmutEverPut bool // C1 mutant\n\tmutReported [2]bool // C1 mutant\n" + s[m.end():]
m = re.search(r'func \(b \*recvBuffer\) put\((\w+) recvMsg\) \{\n(\s*)b\.mu\.Lock\(\)\n', s)
assert m, "put not found"
v = m.group(1)
ins = f"""\tif b.mutEverPut && len(b.c) == 0 {{ // C1 mutant: a reader has taken a message off b.c
\t\tb.mutConsumed = true
\t}}
\tif b.mutConsumed {{ // C1 mutant
\t\tif {v}.err != nil {{
\t\t\tc1MutantHit(&b.mutReported[1], "terminal")
\t\t\t{v}.err = errC1Mutant
\t\t}} else if {v}.buffer != nil {{
\t\t\tc1MutantHit(&b.mutReported[0], "data")
\t\t\tn := {v}.buffer.Len()
\t\t\t{v}.buffer.Free()
\t\t\tbad := make([]byte, n)
\t\t\tfor i := range bad {{
\t\t\t\tbad[i] = 0xEE
\t\t\t}}
\t\t\t{v}.buffer = mem.SliceBuffer(bad)
\t\t}}
\t}}
\tb.mutEverPut = true // C1 mutant
"""
s = s[:m.end()] + ins + s[m.end():]
m = re.search(r'func \(b \*recvBuffer\) load\(\) \{\n(\s*)b\.mu\.Lock\(\)\n', s)
assert m, "load not found"
s = s[:m.end()] + "\tif b.mutEverPut && len(b.c) == 0 { // C1 mutant: a reader has taken a message off b.c\n\t\tb.mutConsumed = true\n\t}\n" + s[m.end():]
s += '''
// C1 mutant support.
var errC1Mutant = c1MutantErr{}

type c1MutantErr struct{}

func (c1MutantErr) Error() string { return "C1 mutant: terminal put after consumption" }

// c1MutantHit reports the first post-consumption put of each kind per recvBuffer.
func c1MutantHit(reported *bool, kind string) {
	if !*reported {
		*reported = true
		println("C1-MUTANT-HIT post-consumption put on this buffer, kind=" + kind)
	}
}

// c1ReaderDelay models a legal schedule in which the reader goroutine is
// descheduled before its first read: with C1_READER_DELAY_MS set, the first
// Read/ReadMessageHeader on each recvBufferReader sleeps that long.
func c1ReaderDelay(r *recvBufferReader) {
	if r.mutDelayed {
		return
	}
	r.mutDelayed = true
	if ms, _ := strconv.Atoi(os.Getenv("C1_READER_DELAY_MS")); ms > 0 {
		time.Sleep(time.Duration(ms) * time.Millisecond)
	}
}
'''
for imp in ('"os"', '"strconv"', '"time"'):
    if '\t' + imp + '\n' not in s:
        s = s.replace('import (\n', 'import (\n\t' + imp + '\n', 1)
m = re.search(r'type recvBufferReader struct \{\n', s)
s = s[:m.end()] + "\tmutDelayed bool // C1 mutant\n" + s[m.end():]
for fn in (r'func \(r \*recvBufferReader\) ReadMessageHeader\(header \[\]byte\) \(n int, err error\) \{\n', r'func \(r \*recvBufferReader\) Read\(n int\) \(buf mem\.Buffer, err error\) \{\n'):
    m = re.search(fn, s)
    assert m, fn
    s = s[:m.end()] + "\tc1ReaderDelay(r) // C1 mutant\n" + s[m.end():]
open(p, 'w').write(s)
