#!/usr/bin/env python3
# Usage: python3 verify/summarize_probes.py <logs-dir> <branch-id>...
# Condenses the verify/probe_branch.sh logs (probe.log = branch as written, probe_noclose.log = mutant that
# never closes the replaced root provider, probe_defer.log = mutant that closes it only after the loads
# already running on it returned) into one line per new/changed candidate test.
import re, sys, os
def parse(path):
    out = {}
    if not os.path.exists(path): return out
    cur = None
    for line in open(path, errors="replace"):
        m = re.match(r'##### (\S+) :: (\S+)', line)
        if m:
            cur = m.group(1).split('/')[-1] + "::" + m.group(2); out[cur] = dict(v="NO-RESULT", overlap=0, ends=[], ); open_loads = {}; closed_during = set(); continue
        if cur is None: continue
        d = out[cur]
        m = re.search(r'VERIFY-PROBE load#(\d+) client root KeyMaterial START', line)
        if m: open_loads[m.group(1)] = True; continue
        if 'VERIFY-PROBE balancer' in line and ('CLOSES' in line or 'SKIPS' in line or 'DEFERRED' in line):
            if open_loads: d["overlap"] += 1
            continue
        m = re.search(r'VERIFY-PROBE load#(\d+) client root KeyMaterial END\s+hi=\S+ err=(.*)', line)
        if m:
            open_loads.pop(m.group(1), None); d["ends"].append(m.group(2).strip()); continue
        m = re.match(r'^(ok|FAIL|PASS)\b', line)
        if m and d["v"] == "NO-RESULT": d["v"] = {"ok": "PASS", "PASS": "PASS", "FAIL": "FAIL"}[m.group(1)]
        if re.match(r'\s*--- FAIL', line): d["v"] = "FAIL"
    return out
logs = sys.argv[1]
for b in sys.argv[2:]:
    a, n, f = (parse(f"{logs}/{b}/{x}") for x in ("probe.log", "probe_noclose.log", "probe_defer.log"))
    print(f"== {b}")
    for t in a:
        e = a[t]["ends"]; bad = [x for x in e if x != "<nil>"]
        print(f"  {t}: as-written={a[t]['v']} loads={len(e)} failed-loads={len(bad)}{' ['+bad[0][:60]+']' if bad else ''} balancer-close-during-load={a[t]['overlap']}"
              f" | no-close-mutant={n.get(t,{}).get('v','-')} | defer-close-mutant={f.get(t,{}).get('v','-')}")
