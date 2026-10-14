#!/bin/bash
# Run: verify/repro/c1_controls.sh <wt-of-evalon/grpc-go-xd-ec59ed30> <wt-of-evalon/grpc-go-xd-6f67b5d7>   (positive controls: make cleanup asynchronous, show the probe catches it; restores worktrees)
set -uo pipefail
HERE=$(cd "$(dirname "$0")" && pwd)
F=test/xds/xds_server_filter_state_retention_test.go
P=internal/xds/server/filter_chain_manager.go
FILT='^(--- |\s+--- |FAIL|ok|PASS)|_test.go:[0-9]+: '
instrument() {
  cp "$HERE/c1_close_probe_test.go.txt" test/xds/zz_c1_close_probe_test.go
  sed -i 's/^\ti.parent.interceptorsDestroyed.Add(1)$/\tc1Probe("interceptor"); i.parent.interceptorsDestroyed.Add(1)/; s/^\tt.filtersDestroyed.Add(1)$/\tc1Probe("filter"); t.filtersDestroyed.Add(1)/' $F
}
restore() { git checkout -- $F $P; rm -f test/xds/zz_c1_close_probe_test.go; git status --short; }
run() { C1_DELAY_ONLY=interceptor C1_DELAY_MS=150 go test -count=1 -v -run "^Test\$/^$1\$" ./test/xds 2>&1 | grep -E "$FILT"; }

cd "${1:?}"; instrument
echo "##### ec59ed30 control A: routingConfig.update closes the old config asynchronously (old.close() -> go old.close())"
sed -i 's/^\told.close()$/\tgo old.close()/' $P; git diff -U0 -- $P | grep '^[+-]\s'
run ServerSideXDS_FilterStateRetention_RDSUpdates
git checkout -- $P
echo "##### ec59ed30 control B: routingConfig.stop closes asynchronously (rc.config.close() -> go rc.config.close())"
sed -i 's/^\trc.config.close()$/\tgo rc.config.close()/' $P; git diff -U0 -- $P | grep '^[+-]\s'
run ServerSideXDS_FilterStateRetention_RDSUpdates
restore

cd "${2:?}"; instrument
echo "##### 6f67b5d7 control C: retire() closes interceptors asynchronously (urc.closeInterceptors() -> go urc.closeInterceptors())"
sed -i 's/^\t\t\t\turc.closeInterceptors()$/\t\t\t\tgo urc.closeInterceptors()/' $P; git diff -U0 -- $P | grep '^[+-]\s'
run ServerSideXDS_RDSUpdates_ReleaseSupersededInterceptors
restore
