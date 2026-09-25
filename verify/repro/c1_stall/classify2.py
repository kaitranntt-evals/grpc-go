# (C1 helper) run: python3 classify2.py  -- classifies ~/stall_logs/*/*.log hangs as failure-path cleanup (runtime.Goexit) or not
import re,glob,os,sys
# For each HANG log: list goroutines that are in failure-path cleanup (contain runtime.Goexit) and what they block on.
for log in sorted(glob.glob(os.path.expanduser('~/stall_logs/*/*.log'))):
    txt=open(log).read()
    if 'panic: test timed out' not in txt: continue
    b=log.split('/')[-2]; t=os.path.basename(log)[:-4]
    out=[]
    for g in re.split(r'\n\n(?=goroutine \d+ \[)',txt):
        if not g.startswith('goroutine'): continue
        stack=g.split('\ncreated by')[0]
        if 'runtime.Goexit' not in stack: continue
        state=g.split('[',1)[1].split(']')[0]
        frames=[l for l in stack.split('\n')[1:] if l and not l.startswith('\t')]
        pre=[]
        for f in frames:
            if f.startswith('runtime.Goexit'): break
            if not f.startswith(('internal/sync','sync.runtime','runtime.','sync.(*Mutex)')): pre.append(re.sub(r'\(0x.*$|\(\{\{.*$','',f))
        out.append(f"[{state}] "+' <- '.join(pre[:3]))
    fails=sorted(set(re.findall(r'(\w+_test\.go:\d+): ([^\n]{0,70})',txt)))[:2]
    print(f"{b} {t}: CLEANUP-HANG {out}" if out else f"{b} {t}: body/other hang")
