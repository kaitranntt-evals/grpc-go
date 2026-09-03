#!/usr/bin/env bash
# Run: from a checkout of branch evalon/grpc-go-xd-9c0c4257, `bash verify/repro/c9_9c0c4257_time_after_assertion.sh`
# Shows the two provider-replacement handshake tests treating expiry of a fixed
# time.After(defaultTestShortTimeout) window as proof that the replaced provider
# is still open, then runs both tests to show they pass with that assertion.
set -euo pipefail

echo "== defaultTestShortTimeout values"
grep -n "defaultTestShortTimeout =" credentials/xds/xds_client_test.go internal/xds/balancer/clusterimpl/tests/balancer_test.go

echo "== time.After used as 'still open' evidence"
grep -n -B4 -A1 "time.After(defaultTestShortTimeout)" \
  credentials/xds/xds_client_test.go \
  internal/xds/balancer/clusterimpl/tests/clusterimpl_security_test.go

echo "== demonstration runs"
go test ./credentials/xds -run '^Test$/^ClientCredsProviderReplacedDuringHandshake$' -v -count=1
go test ./internal/xds/balancer/clusterimpl/tests -run '^Test$/^SecurityConfigUpdate_ReplacedDuringHandshake$' -v -count=1
