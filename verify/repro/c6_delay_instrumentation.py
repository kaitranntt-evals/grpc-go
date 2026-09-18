#!/usr/bin/env python3
# Run (in a checkout of evalon/grpc-go-xd-332d40dd): python3 verify/repro/c6_delay_instrumentation.py [delay_ms] && go test ./internal/xds/balancer/clusterimpl/tests/ -run 'Test/SecurityConfigUpdate_DuringHandshake' -count=5 -v 2>&1 | grep -E 'AUDIT|^--- |^ok|^FAIL'
#
# Audit instrumentation for claim C6. It (1) logs, with a timestamp, when the
# clusterimpl balancer applies a replacement security configuration
# (replaceHandshakeInfo, i.e. Swap + old.Release) and when the test closes
# rootLoadUnblock, and (2) optionally injects a delay (default 0) between
# provider construction and application, i.e. between the `built` event the
# test waits for and the actual replacement of the HandshakeInfo. Restore the
# tree with `git checkout -- .` afterwards.
import sys

delay_ms = int(sys.argv[1]) if len(sys.argv) > 1 else 0

p = "internal/xds/balancer/clusterimpl/clusterimpl.go"
s = open(p).read()
old = """func (b *clusterImplBalancer) replaceHandshakeInfo(hi *xds.HandshakeInfo) {
	old := b.xdsHIPtr.Swap(hi)
	old.Release()
}
"""
new = """func (b *clusterImplBalancer) replaceHandshakeInfo(hi *xds.HandshakeInfo) {
	if %d > 0 {
		time.Sleep(%d * time.Millisecond)
	}
	old := b.xdsHIPtr.Swap(hi)
	old.Release()
	fmt.Fprintf(os.Stderr, "AUDIT replaceHandshakeInfo applied (old released) at %%d\\n", time.Now().UnixNano())
}
""" % (delay_ms, delay_ms)
assert old in s, "replaceHandshakeInfo not found"
s = s.replace(old, new)
if '\t"os"\n' not in s:
    s = s.replace("import (\n", 'import (\n\t"os"\n', 1)
open(p, "w").write(s)

p = "internal/xds/balancer/clusterimpl/tests/clusterimpl_security_test.go"
s = open(p).read()
old = "\tclose(ctrl.rootLoadUnblock)\n"
new = '\tfmt.Fprintf(os.Stderr, "AUDIT test unblocking root load at %d\\n", time.Now().UnixNano())\n\tclose(ctrl.rootLoadUnblock)\n'
assert s.count(old) == 1, "rootLoadUnblock close not found exactly once"
s = s.replace(old, new)
open(p, "w").write(s)
print("instrumented with delay_ms=%d" % delay_ms)
