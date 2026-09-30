#!/usr/bin/env python3
# Usage: python3 verify/instrument_store.py <repo-root>
# Evidence-only instrumentation of credentials/tls/certprovider/store.go (branches that changed the store):
# logs every store handle acquisition (Build: NEW entry / REUSES entry + resulting refCount) and every
# handle release (refCount after release, and when the underlying provider's Close() is invoked).
import sys
p = sys.argv[1] + "/credentials/tls/certprovider/store.go"
s = open(p).read()
if "VERIFY-PROBE" not in s:
    s = s.replace('import (\n', 'import (\n\tverifyos "os"\n', 1)
    a = "\twp.refCount--\n\tif wp.refCount == 0 {\n"
    assert s.count(a) == 1
    s = s.replace(a, "\twp.refCount--\n"
        "\tfmt.Fprintf(verifyos.Stderr, \"VERIFY-PROBE store RELEASE handle on entry{name=%q cert=%q}: refCount now %d\\n\", wp.storeKey.name, wp.storeKey.opts.CertName, wp.refCount)\n"
        "\tif wp.refCount == 0 {\n"
        "\t\tfmt.Fprintf(verifyos.Stderr, \"VERIFY-PROBE store refCount==0 -> underlying provider %T.Close() called, entry{name=%q cert=%q} deleted\\n\", wp.Provider, wp.storeKey.name, wp.storeKey.opts.CertName)\n")
    b = "\t\twp.refCount++\n"
    assert s.count(b) == 1
    s = s.replace(b, b + "\t\tfmt.Fprintf(verifyos.Stderr, \"VERIFY-PROBE store Build REUSES entry{name=%q cert=%q}: refCount now %d\\n\", sk.name, sk.opts.CertName, wp.refCount)\n")
    c = "\tprovStore.providers[sk] = wp\n"
    assert s.count(c) == 1
    s = s.replace(c, c + "\tfmt.Fprintf(verifyos.Stderr, \"VERIFY-PROBE store Build NEW entry{name=%q cert=%q}: refCount now 1\\n\", sk.name, sk.opts.CertName)\n")
    open(p, "w").write(s)
