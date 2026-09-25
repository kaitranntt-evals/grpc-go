#!/usr/bin/env bash
# Run from a checkout of evalon/grpc-go-xd-ab2ea750 (repo kaitranntt-evals/grpc-go-xds-certificate-provider-closure-race):
#   bash /path/to/verify/repro/c1_test_strength.sh
# Runs the six candidate-authored tests at baseline, then under two solution mutations
# (m1: replacement publishes a config without a root provider; m2: validation roots pinned to
# the first load), and reports which tests stay green. Also runs the eval fixture
# eval_handshake_lifetime_test.go (must be present in internal/xds/balancer/clusterimpl/tests/)
# for contrast. Patches are reverted on exit.
set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
m1="$here/m1_replacement_breaks_handshake.patch"
m2="$here/m2_pin_first_roots.patch"

unit_ci='^Test$/^(HandleSecurityConfig_ReplacedDuringRootLoad|HandleSecurityConfig_ReplacedBeforeRootLoad)$'
unit_cx='^Test$/^(OwnedHandshakeInfo_ReplacedDuringLoad|OwnedHandshakeInfo_AcquireAfterRelease|UnownedHandshakeInfo_AcquireRelease)$'
e2e='^Test$/^SecurityConfigUpdate_ReplacedRootsRejectServer$'
fixture='^TestEval_SecurityConfigUpdate_ActiveHandshakeKeepsProvider$'

run() { # label, pkg, pattern
  echo "== $1 =="
  go test -count=1 -v -run "$3" "$2" 2>&1 | grep -E '^\s*(--- |ok|FAIL\s|\s+[a-z_]+_test.go:[0-9]+: )' | grep -v -E 'snapshot cache|Discovery Service|management server serving' || true
}

suite() { # label
  run "$1: unit clusterimpl" ./internal/xds/balancer/clusterimpl/ "$unit_ci"
  run "$1: unit credentials/xds" ./internal/credentials/xds/ "$unit_cx"
  run "$1: e2e candidate" ./internal/xds/balancer/clusterimpl/tests/ "$e2e"
  if [ -f internal/xds/balancer/clusterimpl/tests/eval_handshake_lifetime_test.go ]; then
    run "$1: eval fixture" ./internal/xds/balancer/clusterimpl/tests/ "$fixture"
  fi
}

applied=""
cleanup() { [ -n "$applied" ] && git apply -R "$applied" || true; }
trap cleanup EXIT

suite baseline

applied="$m1"; git apply "$m1"
suite "M1 (replacement publishes nil root provider)"
git apply -R "$m1"; applied=""

applied="$m2"; git apply "$m2"
suite "M2 (validation roots pinned to first load)"
git apply -R "$m2"; applied=""
