#!/usr/bin/env python3
# C2 driver: for each target branch worktree, hold the idle-exit worker inside the
# fake child forever (inserted before the child signals the test), run the changed
# "ExitIdle while another child's update is blocked" test with a hard -timeout, and
# record whether the failure path terminates or hangs until the go test timeout.
#   C2_WT=~/wt C2_OUT=./c2_out python3 verify/repro/run_c2.py [case ...]
# C2_WT must hold one worktree per branch suffix ($C2_WT/<suffix> checked out at evalon/grpc-go-en-<suffix>);
# logs go to $C2_OUT/<case>.txt. Summarize with: C2_OUT=./c2_out python3 verify/repro/summarize_c2.py
import subprocess, sys, os, re, pathlib

WT = pathlib.Path(os.path.expanduser(os.environ.get("C2_WT", "~/wt")))
OUT = pathlib.Path(os.path.expanduser(os.environ.get("C2_OUT", "./c2_out")))
OUT.mkdir(parents=True, exist_ok=True)
INJ = "<-make(chan struct{}) // INJECT(C2): idle-exit worker never finishes\n"

# case -> (test file, -run regex, anchor text; injection inserted right AFTER anchor)
# The case name is the branch suffix (worktree $C2_WT/<suffix>), optionally followed by "-<label>" when
# more than one changed test on the same branch is exercised.
CASES = {
    "f1dbdc2b": ("balancer/endpointsharding/endpointsharding_ext_test.go",
                 "Test/EndpointShardingExitIdleNotBlockedByOtherChild$",
                 "onExitIdle: func(addr string) { "),
    "2805a453": ("balancer/endpointsharding/endpointsharding_ext_test.go",
                 "Test/EndpointShardingExitIdleNotBlockedByOtherChildUpdate$",
                 "ExitIdle: func(bd *stub.BalancerData) {\n\t\t\taddr, _ := endpointAddrs.Load(bd)\n"),
    "fd6b3403": ("balancer/endpointsharding/endpointsharding_ext_test.go",
                 "Test/EndpointShardingExitIdleWithBlockedSibling$",
                 "ExitIdle: func(bd *stub.BalancerData) {\n\t\t\tmu.Lock()\n\t\t\taddr := addrs[bd]\n\t\t\tmu.Unlock()\n"),
    "08e6c604": ("balancer/endpointsharding/endpointsharding_ext_test.go",
                 "Test/EndpointShardingChildExitIdleNotBlockedByOtherChildUpdate$",
                 "func (b *testChildBalancer) ExitIdle() {\n\tb.enter(\"ExitIdle\")\n\tdefer b.exit()\n"),
    "f0a4f7e8": ("balancer/endpointsharding/endpointsharding_ext_test.go",
                 "Test/EndpointShardingExitIdleWhileOtherChildUpdateBlocked$",
                 "func (b *testChildBalancer) ExitIdle() {\n\tb.enter()\n\tdefer b.exit()\n"),
    "417e24dc": ("balancer/endpointsharding/endpointsharding_ext_test.go",
                 "Test/EndpointShardingExitIdleWhileChildUpdateBlocked$",
                 "ExitIdle: func(*stub.BalancerData) {\n"),
    "26ec74be": ("balancer/endpointsharding/endpointsharding_ext_test.go",
                 "Test/EndpointShardingExitIdleWithBlockedChildUpdate$",
                 "func (c *testChild) ExitIdle() {\n"),
    "bf5cd862": ("balancer/endpointsharding/endpointsharding_ext_test.go",
                 "Test/ChildExitIdleIndependentOfOtherChildUpdate$",
                 "ExitIdle: func(bd *stub.BalancerData) {\n\t\t\tbd.ClientConn.UpdateState(balancer.State{ConnectivityState: connectivity.Connecting, Picker: base.NewErrPicker(balancer.ErrNoSubConnAvailable)})\n"),
    "f563f1eb": ("balancer/endpointsharding/concurrency_test.go",
                 "Test/ExitIdleDuringOtherChildUpdate$",
                 "ExitIdle: func(bd *stub.BalancerData) {\n"),
    "1b022fbb": ("balancer/endpointsharding/endpointsharding_concurrency_test.go",
                 "Test/ChildExitIdleDuringUpdate",
                 "ExitIdle: func(bd *stub.BalancerData) {\n"),
    "ae479324": ("balancer/endpointsharding/endpointsharding_concurrency_test.go",
                 "Test/ChildExitIdleDuringOtherChildUpdate$",
                 "ExitIdle: func(bd *stub.BalancerData) {\n"),
    "71865894": ("balancer/endpointsharding/endpointsharding_concurrency_test.go",
                 "Test/ChildExitIdleDuringOtherChildUpdate$",
                 "exitIdle: func() {\n"),
    "1eca0068": ("balancer/endpointsharding/endpointsharding_concurrency_test.go",
                 "Test/EndpointShardingChildExitIdleDuringUpdate$",
                 "exitIdle: func() {\n"),
    "dae0ce93": ("balancer/endpointsharding/endpointsharding_concurrency_test.go",
                 "Test/ChildExitIdleDuringOtherChildUpdate$",
                 "onExitIdle: func() {\n"),
    "3d4a2ba9": ("balancer/endpointsharding/endpointsharding_concurrency_test.go",
                 "Test/ChildExitIdleDuringOtherChildUpdate",
                 "exitIdle: func() {\n"),
    # second changed test on the same branch: the queued child.ExitIdle() worker (not joined by the
    # deferred cleanup) is held before it signals `entered`.
    "3d4a2ba9-serialized": ("balancer/endpointsharding/endpointsharding_concurrency_test.go",
                 "Test/ChildCallsSerialized/ResolverError$",
                 "exitIdle:      func() {"),
    "f93e7e52": ("balancer/endpointsharding/concurrency_test.go",
                 "Test/ExitIdleDuringOtherChildUpdate",
                 "exitIdle: func() {\n"),
}

TIMEOUT = os.environ.get("C2_TIMEOUT", "40s")
only = sys.argv[1:] or list(CASES)
summary = []
for b in only:
    f, run, anchor = CASES[b]
    wt = WT / b.split("-")[0]
    path = wt / f
    src = path.read_text()
    assert src.count(anchor) >= 1, (b, anchor)
    patched = src.replace(anchor, anchor + INJ, 1)
    path.write_text(patched)
    try:
        cmd = ["go", "test", "./balancer/endpointsharding", "-run", run, "-race", "-count=1",
               "-timeout", TIMEOUT, "-v"]
        p = subprocess.run(cmd, cwd=wt, capture_output=True, text=True)
        out = p.stdout + p.stderr
    finally:
        subprocess.run(["git", "checkout", "--", f], cwd=wt, check=True)
    (OUT / f"{b}.txt").write_text("$ " + " ".join(cmd) + "\n" + out)
    hung = "panic: test timed out" in out
    fatal_lines = [l for l in out.splitlines() if re.search(r"_test\.go:\d+: ", l)]
    status = "HUNG-UNTIL-GO-TIMEOUT" if hung else ("TERMINATED exit=%d" % p.returncode)
    summary.append(f"{b}: {status}; first failure: {fatal_lines[0].strip() if fatal_lines else '(none)'}")
    print(summary[-1], flush=True)
with (OUT / "driver_summary.txt").open("a") as fh:
    fh.write("\n".join(summary) + "\n")
