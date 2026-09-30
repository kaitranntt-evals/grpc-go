#!/usr/bin/env python3
# Usage: python3 verify/scan_overlaps.py [-v] <probe-log>...   (-v lists each such load; logs written by verify/probe_branch.sh or verify/probe_attempts.sh)
# For every client validation-root load in the logs, reports whether any balancer/store probe event happened
# between its START and END ("overlap") or whether it ended with an error, and how each such load ended.
# Also tallies load results by root-provider type (client-level tests replace the HandshakeInfo themselves,
# which is not a probe event, so a preserved blocked load there would show as a blocking test provider type
# with result ok).
import collections, re, sys
loads = ok_overlap = 0
hits = []
by_type = collections.Counter()
verbose = "-v" in sys.argv
for path in [a for a in sys.argv[1:] if a != "-v"]:
    cur, open_loads, types = "", {}, {}
    for line in open(path, errors="replace"):
        line = line.rstrip().replace("VERIFY-PROBE ", "")
        if line.startswith("#####"):
            cur, open_loads, types = line[6:].split("   (")[0], {}, {}
            continue
        m = re.match(r"load#(\d+) client root KeyMaterial (START|END)\s+hi=\S+ (.*)", line)
        if m:
            n, kind, rest = m.groups()
            if kind == "START":
                open_loads[n] = []
                types[n] = rest.replace("provider=", "")
                loads += 1
            else:
                by_type[(types.pop(n, "?"), "ok" if "err=<nil>" in rest else "error")] += 1
                ev = [e for e in open_loads.pop(n, []) if e.startswith(("store", "balancer"))]
                if ev or "err=<nil>" not in rest:
                    hits.append((path, cur, n, rest, ev))
                    ok_overlap += "err=<nil>" in rest
            continue
        for v in open_loads.values():
            v.append(line[:170])
for path, cur, n, rest, ev in hits:
    if verbose:
        print(f"{cur} load#{n} END {rest}")
        for e in ev:
            print("     during the load:", e)
print(f"validation-root loads observed: {loads}")
print(f"loads that overlapped a balancer/store event or ended with an error: {len(hits)}")
print(f"  of which ended err=<nil>: {ok_overlap}")
print("load results by root-provider type:")
for (t, r), c in sorted(by_type.items()):
    print(f"  {c:3d}  {t}  {r}")
