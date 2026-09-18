#!/usr/bin/env python3
# Run (in a checkout of evalon/grpc-go-xd-a31d723d):
#   go test ./internal/credentials/xds/ -run 'Test/AcquireHandshakeInfo_OwnerClosesDuringRootLoad' -count=20   # baseline
#   python3 verify/repro/c12_pause_before_release.py && go test ./internal/credentials/xds/ -run 'Test/AcquireHandshakeInfo_OwnerClosesDuringRootLoad' -count=3 -v 2>&1 | grep -E '^--- |^ok|^FAIL|_test.go:'
#   git checkout -- .
#
# Audit instrumentation for claim C12. It applies a controlled schedule to
# TestAcquireHandshakeInfo_OwnerClosesDuringRootLoad: the handshake goroutine
# is paused for 200ms *after* it sends its result on cfgCh but *before* its
# deferred release() runs. If the test's closure assertion is ordered after
# release completion by some synchronization edge, the pause is harmless; if
# not, the assertion runs before the release and the test fails.
p = "internal/credentials/xds/handshake_info_test.go"
s = open(p).read()
start = s.index("func (s) TestAcquireHandshakeInfo_OwnerClosesDuringRootLoad(")
old = """	go func() {
		defer release()
		_, err := hi.ClientSideTLSConfig(ctx, "")
		cfgCh <- err
	}()
"""
new = """	go func() {
		defer func() {
			time.Sleep(200 * time.Millisecond) // AUDIT: pause after the send, before the deferred release
			release()
		}()
		_, err := hi.ClientSideTLSConfig(ctx, "")
		cfgCh <- err
	}()
"""
idx = s.index(old, start)
s = s[:idx] + new + s[idx + len(old):]
open(p, "w").write(s)
print("instrumented", p)
