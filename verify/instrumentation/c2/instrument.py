# Audit instrumentation for claim C2: rewrites endpointsharding.go in place so
# its three mutexes are tracked and every guarded access / child call is checked.
import re, sys
p = sys.argv[1]
s = open(p).read()
def sub1(a, b):
    global s
    assert s.count(a) == 1, (a, s.count(a))
    s = s.replace(a, b)
sub1("\tupdateMu sync.Mutex\n", "\tupdateMu auditUpdateMu\n")
sub1("\t// (balancerWrapper.mu) while holding mu.\n\tmu sync.Mutex\n", "\t// (balancerWrapper.mu) while holding mu.\n\tmu auditEsMu\n")
sub1("\t// acquire mu while holding es.mu.\n\tmu sync.Mutex\n", "\t// acquire mu while holding es.mu.\n\tmu auditBwMu\n")
sub1("\tbw.child = es.childBuilder(bw, es.bOpts)\n",
     "\tauditBwData(bw)\n\tauditChildCall(bw, \"Build\")\n\tbw.child = &auditChild{Balancer: es.childBuilder(bw, es.bOpts), bw: bw}\n")
out = []
n_bw = 0
for line in s.split("\n"):
    st = line.strip()
    ind = line[:len(line) - len(line.lstrip())]
    if not st.startswith("//") and (re.search(r"\bbw\.isClosed\b", st) or re.search(r"\bbw\.child\.", st)):
        out.append(ind + "auditBwData(bw)")
        n_bw += 1
    out.append(line)
s = "\n".join(out)
sub1("\t\t\tchildBalancer.childState.Endpoint = endpoint\n", "\t\t\tauditEsData(es)\n\t\t\tchildBalancer.childState.Endpoint = endpoint\n")
sub1("\t\tchildState := child.childState\n", "\t\tauditEsData(es)\n\t\tchildState := child.childState\n")
sub1("\tbw.childState.State = state\n", "\tauditEsData(bw.es)\n\tbw.childState.State = state\n")
sub1('\t"sync"\n', "")
open(p, "w").write(s)
print("instrumented: bw data sites =", n_bw, file=sys.stderr)
