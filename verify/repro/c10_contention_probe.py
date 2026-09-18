#!/usr/bin/env python3
# Run (in a checkout of evalon/grpc-go-xd-1f161fb4):
#   python3 verify/repro/c10_contention_probe.py && go test ./credentials/xds/ ./internal/credentials/xds/ ./internal/xds/balancer/clusterimpl/... -count=1 -v 2>&1 | awk '/^AUDIT C10/{n++; if($5+0>max)max=$5+0} /^(ok|FAIL|---)/{print} END{print "AUDIT C10 total Acquire/Release calls=" n ", max concurrent callers on one HandshakeInfo=" max}'
#   git checkout -- .
#
# Every probed call prints one line "AUDIT C10 call inflight_on_this_hi= <n> ..."; the
# awk summary above counts the calls and reports the largest <n> seen.
# Audit instrumentation for claim C10. It wraps HandshakeInfo.Acquire/Release
# with a probe that counts how many goroutines are simultaneously inside an
# Acquire or Release call on the *same* HandshakeInfo and prints it on every
# call. A maximum of 1 means no test in the packages run ever produced
# contending acquisitions/releases on one HandshakeInfo's ownership counter.
p = "internal/credentials/xds/handshake_info.go"
s = open(p).read()

s = s.replace("""func (hi *HandshakeInfo) Acquire() bool {
	for {""", """func (hi *HandshakeInfo) Acquire() bool {
	defer auditProbe(hi)()
	for {""", 1)
s = s.replace("""func (hi *HandshakeInfo) Release() {
	if hi == nil {
		return
	}
""", """func (hi *HandshakeInfo) Release() {
	if hi == nil {
		return
	}
	defer auditProbe(hi)()
""", 1)
s += '''
var (
	auditMu       sync.Mutex
	auditInFlight = map[*HandshakeInfo]int32{}
	auditMax      int32
	auditCalls    int64
)

func auditProbe(hi *HandshakeInfo) func() {
	auditMu.Lock()
	auditCalls++
	auditInFlight[hi]++
	if auditInFlight[hi] > auditMax {
		auditMax = auditInFlight[hi]
	}
	fmt.Fprintf(os.Stderr, "AUDIT C10 call inflight_on_this_hi= %d hi=%p total_calls=%d max_so_far=%d\\n", auditInFlight[hi], hi, auditCalls, auditMax)
	auditMu.Unlock()
	// Widen the window so that truly concurrent callers overlap observably.
	time.Sleep(50 * time.Microsecond)
	return func() {
		auditMu.Lock()
		auditInFlight[hi]--
		auditMu.Unlock()
	}
}
'''
for imp in ('"os"', '"sync"', '"time"', '"fmt"'):
    if "\t%s\n" % imp not in s:
        s = s.replace("import (\n", "import (\n\t%s\n" % imp, 1)
open(p, "w").write(s)
print("instrumented", p)
