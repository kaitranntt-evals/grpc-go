#!/usr/bin/env python3
# Run: python3 verify/repro/c1_server_leak_probe.py <worktree-dir> <branch-id> [control|mutate]
# For each C1 target branch this script instruments the changed RPC test with a channelz probe
# (registered via t.Cleanup *before* grpc.NewServer) that reports how many channelz servers are
# still registered when the test finishes, then (in "mutate" mode) forces the early
# GetServiceInfo / net.Listen failure that precedes `defer srv.Stop()`. A server that is still
# registered after the test ends (after > before) was never stopped.
import os
import subprocess
import sys

PROBE = (
    "\tichannelz.TurnOn()\n"
    "\tc1Before, _ := ichannelz.GetServers(0, 0)\n"
    "\tt.Cleanup(func() {\n"
    "\t\tc1After, _ := ichannelz.GetServers(0, 0)\n"
    '\t\tt.Logf("C1PROBE registered channelz servers: before=%d after=%d (leaked=%d)", '
    "len(c1Before), len(c1After), len(c1After)-len(c1Before))\n"
    "\t})\n"
)

BRANCHES = {
    "98b493c6": dict(
        file="test/server_test.go",
        test="TestServer_HandwrittenServiceDesc_UnaryPrecedenceAndStreamFlags",
        anchor="\tsrv := grpc.NewServer(grpc.ChainUnaryInterceptor(rec.unary), grpc.ChainStreamInterceptor(rec.stream))\n",
        mutate=("if len(info.Methods) != len(wantMethods) {", "if len(info.Methods) != len(wantMethods)+1 {"),
    ),
    "58155825": dict(
        file="test/server_pipeline_test.go",
        test="TestServerPipeline_HandwrittenServiceDescRouting",
        anchor="\tsrv := grpc.NewServer(sopts...)\n",
        mutate=("if len(info.Methods) != len(wantMethods) {", "if len(info.Methods) != len(wantMethods)+1 {"),
    ),
    "14fb311d": dict(
        file="test/server_pipeline_test.go",
        test="TestServerPipeline_HandwrittenDescDispatch",
        anchor="\tsrv := grpc.NewServer(\n\t\tgrpc.UnaryInterceptor(rec.unaryInt),\n",
        mutate=("if len(info.Methods) != 3 || info.Methods[0]", "if len(info.Methods) != 4 || info.Methods[0]"),
    ),
    "bf72a067": dict(
        file="test/server_pipeline_test.go",
        test="TestServer_HandwrittenServiceDescRouting",
        anchor="\tsrv := grpc.NewServer(\n\t\tgrpc.UnaryInterceptor(rec.unary),\n",
        mutate=("if len(gotMethods) != 2 || gotMethods", "if len(gotMethods) != 3 || gotMethods"),
    ),
    "e36ecf54": dict(
        file="test/server_pipeline_test.go",
        test="TestServer_HandwrittenServiceDesc_Dispatch",
        anchor="\tserver := grpc.NewServer(\n\t\tgrpc.UnaryInterceptor(rec.unaryInterceptor),\n",
        mutate=("if len(info.Methods) == 0 {", "if len(info.Methods) != 0 {"),
    ),
    "92c7a4a1": dict(
        file="server_rpc_ext_test.go",
        test="TestServer_HandwrittenServiceDesc",
        anchor="\tsrv := grpc.NewServer(grpc.UnaryInterceptor(rec.unaryInterceptor), grpc.StreamInterceptor(rec.streamInterceptor))\n",
        # listener-setup failure: invalid port
        mutate=('lis, err := net.Listen("tcp", "localhost:0")\n\tif err != nil {\n\t\tt.Fatalf("net.Listen() failed: %v", err)',
                'lis, err := net.Listen("tcp", "localhost:-1")\n\tif err != nil {\n\t\tt.Fatalf("net.Listen() failed: %v", err)'),
    ),
    "851287e1": dict(
        file="server_pipeline_ext_test.go",
        test="TestServer_UnifiedPipeline",
        anchor="\t\t\tserver := grpc.NewServer(\n\t\t\t\tgrpc.MaxRecvMsgSize(512),\n",
        mutate=("if len(info.Methods) != len(desc.Methods)+len(desc.Streams) || info.Metadata",
                "if len(info.Methods) != len(desc.Methods)+len(desc.Streams)+1 || info.Metadata"),
    ),
}


def main():
    wt, bid, mode = sys.argv[1], sys.argv[2], (sys.argv[3] if len(sys.argv) > 3 else "mutate")
    cfg = BRANCHES[bid]
    path = os.path.join(wt, cfg["file"])
    orig = open(path).read()
    src = orig
    assert src.count(cfg["anchor"]) == 1, "anchor not unique"
    # indent probe to match anchor
    indent = cfg["anchor"][: len(cfg["anchor"]) - len(cfg["anchor"].lstrip("\t"))]
    probe = "".join(indent + line[1:] + "\n" for line in PROBE.rstrip("\n").split("\n"))
    src = src.replace(cfg["anchor"], probe + cfg["anchor"])
    src = src.replace("import (\n", 'import (\n\tichannelz "google.golang.org/grpc/internal/channelz"\n', 1)
    if mode == "mutate":
        old, new = cfg["mutate"]
        # restrict mutation to the target test function body
        start = src.index("Test" + cfg["test"][4:] + "(t *testing.T)")
        idx = src.index(old, start)
        src = src[:idx] + new + src[idx + len(old):]
    open(path, "w").write(src)
    pkg = "./" + os.path.dirname(cfg["file"]) if os.path.dirname(cfg["file"]) else "."
    cmd = ["go", "test", "-count=1", "-v", pkg, "-run", "^Test$/^%s$" % cfg["test"][len("Test"):]]
    print("+", " ".join(cmd), "(mode=%s)" % mode, flush=True)
    r = subprocess.run(cmd, cwd=wt, capture_output=True, text=True)
    out = r.stdout + r.stderr
    for line in out.splitlines():
        if "C1PROBE" in line or "--- " in line or line.startswith(("ok", "FAIL", "PASS")) or "GetServiceInfo()" in line or "net.Listen" in line:
            print(line)
    print("exit=%d" % r.returncode)
    open(path, "w").write(orig)  # restore the branch's own copy


if __name__ == "__main__":
    main()
