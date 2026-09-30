#!/usr/bin/env python3
# Run: python3 verify/repro/c2_lose_terminal_mutant.py <claim-worktree>/internal/transport/transport.go, then VERIFY_C2_LOSE_TERMINAL=1 go test ... (see verify/repro/c2_run.sh)
"""C2 stall-injection mutant for internal/transport/transport.go.

With VERIFY_C2_LOSE_TERMINAL=1 in the environment, recvBuffer.put() never
enqueues the first terminal message (err != nil) nor anything put after it, so
a reader that has consumed all queued data stalls waiting for an EOF/error that
never arrives - a "terminal message lost" receive-path regression. A test read
with an effective local bound then fails on its own deadline; one without hangs
until the global `go test -timeout`.
Usage: mutate_c2.py <path to transport.go>
"""
import re, sys
p = sys.argv[1]
s = open(p).read()
m = re.search(r'type recvBuffer struct \{\n', s)
s = s[:m.end()] + "\tmutSilenced atomic.Bool // C2 mutant\n" + s[m.end():]
m = re.search(r'func \(b \*recvBuffer\) put\((\w+) recvMsg\) \{\n', s)
assert m, "put not found"
v = m.group(1)
s = s[:m.end()] + f"""\tif os.Getenv("VERIFY_C2_LOSE_TERMINAL") != "" && (b.mutSilenced.Load() || {v}.err != nil) {{ // C2 mutant
\t\tb.mutSilenced.Store(true)
\t\tif {v}.buffer != nil {{
\t\t\t{v}.buffer.Free()
\t\t}}
\t\treturn
\t}}
""" + s[m.end():]
for imp in ('"os"', '"sync/atomic"'):
    if '\t' + imp + '\n' not in s:
        s = s.replace('import (\n', 'import (\n\t' + imp + '\n', 1)
open(p, 'w').write(s)
