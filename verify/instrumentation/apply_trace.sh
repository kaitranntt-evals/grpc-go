#!/bin/bash
# Run: verify/instrumentation/apply_trace.sh <path-to-grpc-go-checkout>
# Adds a trace hook to refCountedServerFilter.incRef/Close (no behavior change when the hook is nil).
set -euo pipefail
here="$(cd "$(dirname "$0")" && pwd)"
wt="$1"
f="$wt/internal/xds/server/filter_chain_manager.go"
cp "$here/verify_trace.go.txt" "$wt/internal/xds/server/verify_trace.go"
grep -q 'verifyTrace("ref-release"' "$f" && { echo "already instrumented: $wt"; exit 0; }
perl -0pi -e 's/(func \(rsf \*refCountedServerFilter\) incRef\(\) \{\n)/$1\tverifyTrace("ref-acquire", rsf.ServerFilter, rsf.refCnt.Load())\n/; s/(func \(rsf \*refCountedServerFilter\) Close\(\) \{\n)/$1\tverifyTrace("ref-release", rsf.ServerFilter, rsf.refCnt.Load())\n/' "$f"
n=$(grep -c 'verifyTrace("ref-' "$f")
[ "$n" = 2 ] || { echo "instrumentation failed on $wt (matched $n of 2 sites)"; exit 1; }
echo "instrumented: $wt"
