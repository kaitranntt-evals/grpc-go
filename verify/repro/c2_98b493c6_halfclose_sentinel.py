#!/usr/bin/env python3
# Run: python3 verify/repro/c2_98b493c6_halfclose_sentinel.py <worktree of evalon/grpc-go-se-98b493c6>
# Shows that the final-status assertion in TestServer_UnaryRPC_RespondsBeforeClientHalfClose
# reads the NewStream-scoped `err` (nil setup result), not the later RecvMsg result: after the
# NewStream nil-check we overwrite that variable with a sentinel status; the RPC itself still
# succeeds (RecvMsg -> io.EOF passes) yet the "stream status" assertion reports the sentinel.
import os
import subprocess
import sys

wt = sys.argv[1]
p = os.path.join(wt, "test/server_test.go")
orig = open(p).read()
s = orig
old = '\tif err != nil {\n\t\tt.Fatalf("NewStream() = _, %v; want success", err)\n\t}\n'
assert s.count(old) == 1
s = s.replace(old, old + '\terr = status.Error(codes.Internal, "C2 sentinel: this is the NewStream-scoped err variable, not an RPC result")\n')
old2 = '\tif st := status.Convert(err); st.Code() != codes.OK {\n'
assert s.count(old2) == 1
s = s.replace(old2, '\tt.Logf("C2PROBE: err passed to status.Convert = %v", err)\n' + old2)
open(p, "w").write(s)
try:
    cmd = ["go", "test", "-count=1", "-v", "./test", "-run", "^Test$/^Server_UnaryRPC_RespondsBeforeClientHalfClose$"]
    print("+", " ".join(cmd), flush=True)
    r = subprocess.run(cmd, cwd=wt, capture_output=True, text=True)
    for line in (r.stdout + r.stderr).splitlines():
        if "C2PROBE" in line or "stream status" in line or "--- " in line or line.startswith(("ok", "FAIL", "PASS")):
            print(line)
    print("exit=%d" % r.returncode)
finally:
    open(p, "w").write(orig)
