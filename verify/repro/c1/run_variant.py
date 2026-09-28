#!/usr/bin/env python3
# Run: python3 verify/repro/c1/run_variant.py <worktree> <test-file-relpath> <label> <go-test-run-regexp> <timeout> '<json [[old,new],...]>' [out-dir]
"""Apply a controlled stall / early-failure edit to a branch test file, run one
test with a hard `go test -timeout`, record the output, then restore the file.
A run that ends with `panic: test timed out after <timeout>` means the test had
no local completion bound; a run that ends at ~10s (defaultTestTimeout) or
earlier means a local deadline / cleanup path released it."""
import json
import os
import subprocess
import sys
import time

wt, relfile, label, runpat, timeout, edits = sys.argv[1], sys.argv[2], sys.argv[3], sys.argv[4], sys.argv[5], json.loads(sys.argv[6])
outdir = sys.argv[7] if len(sys.argv) > 7 else os.path.join(os.getcwd(), "c1-out")
os.makedirs(outdir, exist_ok=True)
path = os.path.join(wt, relfile)
orig = open(path).read()
src = orig
for old, new in edits:
    assert src.count(old) == 1, f"edit anchor not unique/found: {old!r} (count={src.count(old)})"
    src = src.replace(old, new)
open(path, "w").write(src)
out = os.path.join(outdir, f"{label}.txt")
cmd = ["go", "test", "-run", runpat, "./internal/transport", "-count=1", "-timeout", timeout, "-v"]
try:
    t0 = time.time()
    p = subprocess.run(cmd, cwd=wt, capture_output=True, text=True)
    dt = time.time() - t0
    with open(out, "w") as f:
        f.write("$ " + " ".join(cmd) + f"\n# edits applied to {relfile}:\n")
        for old, new in edits:
            f.write(f"#   - {old!r}\n#   + {new!r}\n")
        f.write(f"# exit={p.returncode} wall={dt:.1f}s\n")
        f.write(p.stdout + p.stderr)
    print(f"{label}: exit={p.returncode} wall={dt:.1f}s -> {out}")
finally:
    open(path, "w").write(orig)
