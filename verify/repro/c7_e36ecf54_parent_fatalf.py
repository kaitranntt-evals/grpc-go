#!/usr/bin/env python3
# Run: python3 verify/repro/c7_e36ecf54_parent_fatalf.py <worktree of evalon/grpc-go-se-e36ecf54>
# Triggers the existing `t.Fatalf("StreamingInputCall() failed: %v", err)` assertion inside the
# "client streaming" table callback of TestServer_AllRPCTypes_InterceptorSegregation by giving
# that one RPC an already-cancelled context. The callback only receives ctx, so `t` is the parent
# test's *testing.T captured by the closure, and the Fatalf runs inside a t.Run subtest.
import os
import subprocess
import sys

wt = sys.argv[1]
p = os.path.join(wt, "test/server_pipeline_test.go")
orig = open(p).read()
old = "\t\t\t\tstream, err := client.StreamingInputCall(ctx)\n\t\t\t\tif err != nil {\n\t\t\t\t\tt.Fatalf(\"StreamingInputCall() failed: %v\", err)\n"
assert orig.count(old) == 1
new = old.replace("stream, err := client.StreamingInputCall(ctx)",
                  "c7ctx, c7cancel := context.WithCancel(ctx)\n\t\t\t\tc7cancel() // C7 trigger\n\t\t\t\tstream, err := client.StreamingInputCall(c7ctx)")
open(p, "w").write(orig.replace(old, new))
try:
    cmd = ["go", "test", "-count=1", "-v", "./test", "-run", "^Test$/^Server_AllRPCTypes_InterceptorSegregation$"]
    print("+", " ".join(cmd), flush=True)
    r = subprocess.run(cmd, cwd=wt, capture_output=True, text=True)
    for line in (r.stdout + r.stderr).splitlines():
        if "tlogger.go" in line:
            continue
        if any(k in line for k in ("--- ", "FailNow", "Goexit", "StreamingInputCall() failed", "=== RUN", "=== CONT", "=== NAME")) or line.startswith(("ok", "FAIL", "PASS")):
            print(line)
    print("exit=%d" % r.returncode)
finally:
    open(p, "w").write(orig)
