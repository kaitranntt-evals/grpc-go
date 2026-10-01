#!/usr/bin/env python3
# Run (inside a worktree of evalon/grpc-go-xd-0628e30a): python3 c1_0628e30a_modes.py <mode>; revert with `git checkout -- internal/xds/server/listener_wrapper_test.go test/xds/xds_server_filter_state_retention_test.go`
# Test-file-only perturbations of the asynchronous work the three added lifecycle tests depend on:
#   int-close / int-build : 150ms pause in the unit-test double's interceptor Close() / BuildServerInterceptor() (before the counter bump)
#   int-nolistener        : the Listener resource is never published to the listener wrapper (listener acquisition never completes)
#   int-noretire          : retired interceptors never report closure
#   e2e-nolistener        : the server-side Listener resource is never published (server never reaches SERVING)
#   e2e-noretire          : retired interceptors never report closure
import sys
INT = "internal/xds/server/listener_wrapper_test.go"
E2E = "test/xds/xds_server_filter_state_retention_test.go"
SLEEP = "time.Sleep(150 * time.Millisecond) // verify: controlled pause\n\t"
M = {
 "int-close": (INT, "func (si *trackingServerInterceptor) Close() {\n\tsi.parent.interceptorsDestroyed.Add(1)",
                    "func (si *trackingServerInterceptor) Close() {\n\t" + SLEEP + "si.parent.interceptorsDestroyed.Add(1)"),
 "int-build": (INT, "(iresolver.ServerInterceptor, error) {\n\tsf.parent.interceptorsCreated.Add(1)",
                    "(iresolver.ServerInterceptor, error) {\n\t" + SLEEP + "sf.parent.interceptorsCreated.Add(1)"),
 "int-nolistener": (INT, "\t\tListeners:      []*v3listenerpb.Listener{ldsResource},\n", "\t\t// verify: Listener resource withheld\n"),
 "int-noretire": (INT, "func (si *trackingServerInterceptor) Close() {\n\tsi.parent.interceptorsDestroyed.Add(1)",
                       "func (si *trackingServerInterceptor) Close() {\n\t_ = si.parent // verify: closure never reported"),
 "e2e-nolistener": (E2E, "\tnumFilterChains := int32(len(inboundLis.GetFilterChains()))\n\tresources.Listeners = append(resources.Listeners, inboundLis)\n",
                         "\tnumFilterChains := int32(len(inboundLis.GetFilterChains()))\n\tresources.SkipValidation = true // verify: server-side Listener resource withheld\n"),
 "e2e-noretire": (E2E, "func (i *trackingInterceptor) Close() {\n\ti.parent.interceptorsDestroyed.Add(1)",
                       "func (i *trackingInterceptor) Close() {\n\t_ = i.parent // verify: closure never reported"),
}
path, old, new = M[sys.argv[1]]
s = open(path).read()
if s.count(old) != 1:
    sys.exit("anchor not found exactly once in %s (found %d)" % (path, s.count(old)))
open(path, "w").write(s.replace(old, new))
print("patched %s: %s" % (path, sys.argv[1]))
