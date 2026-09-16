#!/usr/bin/env python3
# Run: python3 verify/repro/c8_51f968cd_handler_ran_probe.py <worktree of evalon/grpc-go-se-51f968cd>
# Adds two t.Logf probes to TestServerStreamingRPC_ErrorFraming (no assertion changes): one inside
# StreamingOutputCallF (records that the handler executed and what it decoded) and one printing the
# status the malformed-input scenario receives. If the test still passes while the probe shows the
# handler ran, the scenario's `status.Code(err) == codes.Internal` check is satisfied by the
# handler's own Internal error rather than by a decode failure.
import os
import subprocess
import sys

wt = sys.argv[1]
p = os.path.join(wt, "test/server_rpc_pipeline_test.go")
orig = open(p).read()
s = orig
old = ('\t\tStreamingOutputCallF: func(_ *testpb.StreamingOutputCallRequest, stream testgrpc.TestService_StreamingOutputCallServer) error {\n'
       '\t\t\treturn status.Error(codes.Internal, "streaming handler must not run when the request cannot be decoded")\n')
assert s.count(old) == 1
new = ('\t\tStreamingOutputCallF: func(req *testpb.StreamingOutputCallRequest, stream testgrpc.TestService_StreamingOutputCallServer) error {\n'
       '\t\t\tt.Logf("C8PROBE StreamingOutputCallF RAN; decoded request = %v", req)\n'
       '\t\t\treturn status.Error(codes.Internal, "streaming handler must not run when the request cannot be decoded")\n')
s = s.replace(old, new)
old2 = '\terr = raw.RecvMsg(&testpb.StreamingOutputCallResponse{})\n\tif status.Code(err) != codes.Internal {\n'
assert s.count(old2) == 1
s = s.replace(old2, '\terr = raw.RecvMsg(&testpb.StreamingOutputCallResponse{})\n\tt.Logf("C8PROBE malformed-input scenario got: %v", err)\n\tif status.Code(err) != codes.Internal {\n')
open(p, "w").write(s)
try:
    cmd = ["go", "test", "-count=1", "-v", "./test", "-run", "^Test$/^ServerStreamingRPC_ErrorFraming$"]
    print("+", " ".join(cmd), flush=True)
    r = subprocess.run(cmd, cwd=wt, capture_output=True, text=True)
    for line in (r.stdout + r.stderr).splitlines():
        if "C8PROBE" in line or "--- " in line or line.startswith(("ok", "FAIL", "PASS")):
            print(line)
    print("exit=%d" % r.returncode)
finally:
    open(p, "w").write(orig)
