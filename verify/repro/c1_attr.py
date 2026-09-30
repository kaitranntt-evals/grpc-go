#!/usr/bin/env python3
# Run: python3 verify/repro/c1_attr.py <go-test-v-log>   (used by verify/repro/c1_run.sh to attribute C1-MUTANT-HIT lines to tests)
"""Attribute C1-MUTANT-HIT lines in `go test -v` output to the running test."""
import sys, re, collections
cur = None; hits = collections.Counter(); res = collections.Counter()
for line in open(sys.argv[1], errors='replace'):
    m = re.match(r'=== (RUN|CONT)\s+(\S+)', line)
    if m: cur = m.group(2); continue
    if 'C1-MUTANT-HIT' in line:
        hits[(cur, line.strip().split('kind=')[1])] += 1
    m = re.match(r'\s*--- (PASS|FAIL): (\S+)', line)
    if m and m.group(2) != 'Test': res[(m.group(2), m.group(1))] += 1
tests = sorted({t for t, _ in res})
for t in tests:
    h = {k: v for (tt, k), v in hits.items() if tt == t}
    print(f"  {t}: PASS={res[(t,'PASS')]} FAIL={res[(t,'FAIL')]} post-consumption-put buffers: data={h.get('data',0)} terminal={h.get('terminal',0)}")
