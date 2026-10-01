#!/usr/bin/env python3
# Run: python3 verify/instrumentation/c1_pause.py <close|build> <path/to/test/xds/xds_server_filter_state_retention_test.go> [pause]
# Inserts a controlled pause into the shared test double used by every branch's
# ServerSideXDS lifecycle test (trackingHTTPFilterBuilder / trackingInterceptor):
#   close: sleep in trackingInterceptor.Close() BEFORE the "destroyed" counter is bumped
#          (models interceptor retirement that is still in progress)
#   build: sleep in BuildServerInterceptor() BEFORE the "created" counter is bumped
#          (models filter-chain construction that is still in progress)
# Revert with: git checkout -- <file>
import sys
mode, path = sys.argv[1], sys.argv[2]
pause = sys.argv[3] if len(sys.argv) > 3 else "150 * time.Millisecond"
s = open(path).read()
if mode == "close":
    old = "func (i *trackingInterceptor) Close() {\n\ti.parent.interceptorsDestroyed.Add(1)\n"
    new = "func (i *trackingInterceptor) Close() {\n\ttime.Sleep(%s) // verify: controlled pause\n\ti.parent.interceptorsDestroyed.Add(1)\n" % pause
elif mode == "build":
    old = "(resolver.ServerInterceptor, error) {\n\tt.interceptorsCreated.Add(1)\n"
    new = "(resolver.ServerInterceptor, error) {\n\ttime.Sleep(%s) // verify: controlled pause\n\tt.interceptorsCreated.Add(1)\n" % pause
else:
    sys.exit("mode must be close|build")
if s.count(old) != 1:
    sys.exit("anchor not found exactly once in %s (found %d)" % (path, s.count(old)))
open(path, "w").write(s.replace(old, new))
print("patched %s: %s pause %s" % (path, mode, pause))
