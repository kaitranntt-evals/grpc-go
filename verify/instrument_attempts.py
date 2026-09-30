#!/usr/bin/env python3
# Usage: python3 verify/instrument_attempts.py <repo-root>
# Evidence-only instrumentation: wraps credsImpl.ClientHandshake in credentials/xds/xds.go so every client
# connection attempt logs "VERIFY-PROBE ClientHandshake#N START conn=<local>-><remote>" and "... END err=...".
# Together with the load#N lines from verify/instrument.py this shows which connection attempt each
# validation-root load belongs to, and whether any attempt was initiated after the replacement.
import sys
p = sys.argv[1] + "/credentials/xds/xds.go"
s = open(p).read()
if "VERIFY-PROBE" not in s:
    sig = "func (c *credsImpl) ClientHandshake(ctx context.Context, authority string, rawConn net.Conn) (net.Conn, credentials.AuthInfo, error) {"
    assert s.count(sig) == 1
    s = s.replace(sig, sig.replace("ClientHandshake(", "verifyInnerClientHandshake("))
    s = s.replace('import (\n', 'import (\n\tverifyfmt "fmt"\n\tverifyos "os"\n\tverifyatomic "sync/atomic"\n', 1)
    s += """
var verifyHandshakeSeq verifyatomic.Int64

func (c *credsImpl) ClientHandshake(ctx context.Context, authority string, rawConn net.Conn) (net.Conn, credentials.AuthInfo, error) {
	n := verifyHandshakeSeq.Add(1)
	if rawConn == nil { // some pre-existing tests pass a nil conn; do not dereference it
		verifyfmt.Fprintf(verifyos.Stderr, "VERIFY-PROBE ClientHandshake#%d START conn=<nil>\\n", n)
	} else {
		verifyfmt.Fprintf(verifyos.Stderr, "VERIFY-PROBE ClientHandshake#%d START conn=%v->%v\\n", n, rawConn.LocalAddr(), rawConn.RemoteAddr())
	}
	conn, ai, err := c.verifyInnerClientHandshake(ctx, authority, rawConn)
	verifyfmt.Fprintf(verifyos.Stderr, "VERIFY-PROBE ClientHandshake#%d END   err=%v\\n", n, err)
	return conn, ai, err
}
"""
    open(p, "w").write(s)
