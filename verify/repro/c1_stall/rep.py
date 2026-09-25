# (C1 helper) run: python3 rep.py  -- prints one representative failure-path cleanup hang per branch
import re,glob,os
seen=set()
for log in sorted(glob.glob(os.path.expanduser('~/stall_logs/*/*.log')), key=lambda p:(p.split('/')[-2], 'exitidle' in p)):
    b=log.split('/')[-2]
    if b in seen: continue
    txt=open(log).read()
    if 'panic: test timed out' not in txt: continue
    for g in re.split(r'\n\n(?=goroutine \d+ \[)',txt):
        st=g.split('\ncreated by')[0]
        if 'runtime.Goexit' not in st: continue
        head=g.split('\n')[0]
        if 'select' in head and 'no cases' not in head: continue
        frames=[re.sub(r'\(0x.*|\(\{\{.*','',l) for l in st.split('\n')[1:] if l and not l.startswith('\t')]
        blk=next((f for f in frames if 'endpointsharding' in f and not f.startswith('runtime')),'')
        tl=[l.strip().split('/')[-1] for l in st.split('\n') if l.startswith('\t') and '_test.go' in l]
        errs=list(dict.fromkeys(re.findall(r'\n\s+(\w+_test\.go:\d+: [^\n]{0,90})',txt)))[:3]
        print(f"{b} | {os.path.basename(log)[:-4]} | {head[:40]} | {blk} | test frames {tl[:3]} | errors {errs}")
        seen.add(b); break
