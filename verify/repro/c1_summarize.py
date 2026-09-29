import re,glob
for f in sorted(glob.glob('/home/ubuntu/c1/runs/*.txt')):
    s=open(f).read()
    hdr=s.splitlines()[0]
    timedout='panic: test timed out' in s
    build='build failed' in s
    sub=[l for l in s.splitlines() if re.match(r'^\s+--- (PASS|FAIL)',l)]
    fin=[l for l in s.splitlines() if re.match(r'^(ok|FAIL\t)',l)]
    exitl=[l for l in s.splitlines() if l.startswith('### exit=')]
    logs=[l.strip() for l in s.splitlines() if re.match(r'^\s+\w+_test\.go:\d+: ',l)][:4]
    print('=====',f.split('/')[-1])
    print(' timed_out=%s build_failed=%s exit=%s'%(timedout,build,exitl[-1] if exitl else '?'))
    for l in sub+fin: print('  ',l)
    for l in logs: print('  LOG',l)
    if timedout:
        tf=hdr.split('file=')[1].split()[0].split('/')[-1]
        gs=re.split(r'\n\ngoroutine ',s)
        for g in gs[1:]:
            lines=g.split('\n')
            if not any('balancer/endpointsharding/' in l for l in lines): continue
            fr=[re.sub(r'.*/balancer/endpointsharding/','',l.strip()).split(' +0x')[0] for l in lines if ('balancer/endpointsharding/' in l)]
            print('  GOROUTINE',lines[0][:40],'|',' <- '.join(fr[:6]))
