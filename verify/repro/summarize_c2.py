#!/usr/bin/env python3
# Summarize C2 logs: for each $C2_OUT/<case>.txt, report whether the injected run hung until
# the go test -timeout, the first assertion failure, and where a test goroutine was parked at that moment.
#   C2_OUT=./c2_out python3 verify/repro/summarize_c2.py [case ...]   (reads the logs written by run_c2.py)
# Note: for tests that use t.Run subtests / join a helper goroutine, the goroutine printed here may be the
# parent test goroutine (chan receive); read the full log for the goroutine parked in (*balancerWrapper).close.
import os, pathlib, re, sys

OUT = pathlib.Path(os.path.expanduser(os.environ.get("C2_OUT", "./c2_out")))
branches = sys.argv[1:] or ["f1dbdc2b", "2805a453", "fd6b3403", "08e6c604", "f0a4f7e8", "417e24dc", "26ec74be", "bf5cd862",
                            "f563f1eb", "1b022fbb", "ae479324", "71865894", "1eca0068", "dae0ce93", "3d4a2ba9",
                            "3d4a2ba9-serialized", "f93e7e52"]
for b in branches:
    text = (OUT / f"{b}.txt").read_text()
    lines = text.splitlines()
    cmd = lines[0]
    hung = "panic: test timed out" in text
    fails = [l.strip() for l in lines if re.search(r"_test\.go:\d+: ", l) and "Leaked goroutine" not in l]
    result = [l for l in lines if l.startswith("FAIL\t") or l.startswith("--- FAIL: Test (") or l.startswith("ok ")]
    print(f"=== {b}: {'HUNG until go test -timeout' if hung else 'TERMINATED'} | {result[-1] if result else '?'}")
    print(f"    cmd: {cmd}")
    print(f"    first failure: {fails[0] if fails else '(none)'}")
    if not hung:
        continue
    # goroutine dump: find the goroutine running the test body (frame is the test func, not a "created by")
    blocks = re.split(r"\n(?=goroutine \d+ \[)", text[text.index("panic: test timed out"):])
    for blk in blocks:
        frames = blk.splitlines()
        if not frames or not frames[0].startswith("goroutine"):
            continue
        body = "\n".join(frames)
        if not re.search(r"^google\.golang\.org/grpc/balancer/endpointsharding_test\.s\.Test\w+(\.func\d+(\.\d+)?)?\(", body, re.M):
            continue
        if "testing.tRunner" not in body:
            continue  # not the test goroutine itself
        interesting = [f.strip() for f in frames if re.search(r"^(goroutine|google\.golang\.org/grpc/balancer/endpointsharding|\s+\S+/balancer/endpointsharding/)", f)]
        print("    test goroutine at go-timeout:")
        for f in interesting[:10]:
            print("      " + f)
        break
