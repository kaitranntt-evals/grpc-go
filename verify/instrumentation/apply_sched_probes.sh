#!/bin/bash
# Run: verify/instrumentation/apply_sched_probes.sh <path-to-grpc-go-checkout>
# Adds two env-controlled sleeps (pure scheduling perturbations, no-ops when the variables are unset):
#   VERIFY_CLOSE_DELAY=150ms      sleep at the top of interceptorList.Close
#   VERIFY_CONSTRUCT_DELAY=150ms  sleep at the top of filterChain.constructUsableRouteConfiguration (updateUsableRouteConfiguration(config ...) on the reference branch)
set -euo pipefail
here="$(cd "$(dirname "$0")" && pwd)"
wt="$1"
f="$wt/internal/xds/server/filter_chain_manager.go"
cp "$here/verify_probe.go.txt" "$wt/internal/xds/server/verify_probe.go"
grep -q 'verifyDelayClose()' "$f" || perl -0pi -e 's/(func \(il \*interceptorList\) Close\(\) \{\n)/$1\tverifyDelayClose()\n/' "$f"
grep -q 'verifyDelayConstruct()' "$f" || perl -0pi -e 's/(func \(fc \*filterChain\) (?:constructUsableRouteConfiguration|updateUsableRouteConfiguration)\((?:config )[^\n]*\{\n)/$1\tverifyDelayConstruct()\n/' "$f"
grep -q 'verifyDelayClose()' "$f" || { echo "close probe failed on $wt"; exit 1; }
grep -q 'verifyDelayConstruct()' "$f" || echo "note: no constructUsableRouteConfiguration on $wt (construct probe not applied)"
echo "probed: $wt"
