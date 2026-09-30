#!/usr/bin/env python3
# Usage: python3 verify/instrument.py <repo-root> [--no-close|--defer-close]
# Evidence-only instrumentation (never part of the audited solution):
#  * internal/credentials/xds/handshake_info.go: log start/end of the client validation-root
#    KeyMaterial load (HandshakeInfo pointer + returned error) to stderr as "VERIFY-PROBE ..." lines.
#  * internal/xds/balancer/clusterimpl/clusterimpl.go: log every b.cachedRoot.Close() (the replaced
#    root provider being retired by the balancer). With --no-close the Close() call is skipped instead
#    (a "never invalidate the selected provider" mutant). With --defer-close the balancer's Close() of the
#    replaced root provider is postponed until every client validation-root load already running on that
#    provider has returned, and is then performed (a "selected load is preserved, cleanup still happens"
#    reference mutant: what a lifetime-preserving implementation looks like to the tests).
import re, sys
root = sys.argv[1]
no_close = "--no-close" in sys.argv[2:]
defer_close = "--defer-close" in sys.argv[2:]
p = root + "/internal/credentials/xds/handshake_info.go"
s = open(p).read()
if "VERIFY-PROBE" not in s:
    s = s.replace('import (\n', 'import (\n\t"os"\n\t"sync"\n', 1)
    old = "\tkm, err := rootProv.KeyMaterial(ctx)\n\tif err != nil {\n\t\treturn nil, fmt.Errorf(\"xds: fetching trusted roots"
    assert s.count(old) == 1, s.count(old)
    new = ("\tverifyAcquire(rootProv)\n\tdefer verifyRelease(rootProv)\n"
           "\tverifySeq := verifyProbeSeq.Add(1)\n"
           "\tfmt.Fprintf(os.Stderr, \"VERIFY-PROBE load#%d client root KeyMaterial START hi=%p provider=%T\\n\", verifySeq, hi, rootProv)\n"
           "\tkm, err := rootProv.KeyMaterial(ctx)\n"
           "\tfmt.Fprintf(os.Stderr, \"VERIFY-PROBE load#%d client root KeyMaterial END   hi=%p err=%v\\n\", verifySeq, hi, err)\n"
           "\tif err != nil {\n\t\treturn nil, fmt.Errorf(\"xds: fetching trusted roots")
    s = s.replace(old, new)
    s += """
var verifyProbeSeq atomic.Int64

// Evidence-only bookkeeping used by the --defer-close reference mutant.
var (
	verifyMu       sync.Mutex
	verifyInflight = map[certprovider.Provider]int{}
	verifyPending  = map[certprovider.Provider]bool{}
)

func verifyAcquire(p certprovider.Provider) {
	verifyMu.Lock()
	verifyInflight[p]++
	verifyMu.Unlock()
}

func verifyRelease(p certprovider.Provider) {
	verifyMu.Lock()
	verifyInflight[p]--
	doClose := verifyInflight[p] == 0 && verifyPending[p]
	if doClose {
		delete(verifyPending, p)
	}
	verifyMu.Unlock()
	if doClose {
		fmt.Fprintf(os.Stderr, "VERIFY-PROBE deferred Close of replaced root provider runs now (its last running load returned)\\n")
		p.Close()
	}
}

// VerifyCloseWhenIdle closes p once no client validation-root load is running on it.
func VerifyCloseWhenIdle(p certprovider.Provider) {
	verifyMu.Lock()
	if verifyInflight[p] > 0 {
		verifyPending[p] = true
		verifyMu.Unlock()
		fmt.Fprintf(os.Stderr, "VERIFY-PROBE balancer retires replaced root provider: Close DEFERRED, a validation-root load is still running on it\\n")
		return
	}
	verifyMu.Unlock()
	fmt.Fprintf(os.Stderr, "VERIFY-PROBE balancer CLOSES replaced cached root provider (no load running on it)\\n")
	p.Close()
}
"""
    open(p, "w").write(s)
p = root + "/internal/xds/balancer/clusterimpl/clusterimpl.go"
s = open(p).read()
if "VERIFY-PROBE" not in s:
    if defer_close:
        s2, n = re.subn(r'(?m)^(\s*)b\.cachedRoot\.Close\(\)', r'\1xds.VerifyCloseWhenIdle(b.cachedRoot)', s)
    elif no_close:
        s2, n = re.subn(r'(?m)^(\s*)b\.cachedRoot\.Close\(\)', r'\1println("VERIFY-PROBE balancer SKIPS Close of replaced cached root provider (no-close mutant)")', s)
    else:
        s2, n = re.subn(r'(?m)^(\s*)b\.cachedRoot\.Close\(\)', r'\1println("VERIFY-PROBE balancer CLOSES replaced cached root provider"); b.cachedRoot.Close()', s)
    print("clusterimpl.go: instrumented %d cachedRoot.Close() sites" % n, file=sys.stderr)
    open(p, "w").write(s2)
