#!/bin/bash
# Run: verify/repro/c1_c8_trace.sh <checkout of the branch under test>   (C1/C8: prints the ref-release / icpt-close / filter-close order per phase)
set -euo pipefail
here="$(cd "$(dirname "$0")" && pwd)"
wt="$(cd "$1" && pwd)"
"$here/../instrumentation/apply_trace.sh" "$wt"
cp "$here/c1_filter_ref_order_test.go" "$wt/test/xds/verify_c1_filter_ref_order_test.go"
cd "$wt"
go test -race -count=1 -v -run '^Test$/^VerifyC1_' ./test/xds 2>&1 | grep -E 'PHASE|--- (PASS|FAIL)|^(ok|FAIL|panic)' | sed -E 's/^ *verify_c1_filter_ref_order_test.go:[0-9]+: //'
