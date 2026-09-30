#!/usr/bin/env bash
# Usage: verify/probe_fixture.sh <worktree-dir> [--no-close|--defer-close]
# Runs the archived hidden fixture (eval_handshake_lifetime_test.go, byte-exact copy provisioned in the
# worktree) against the tree instrumented by verify/instrument.py and prints the VERIFY-PROBE lines
# (start/end of the selected validation-root load, balancer retiring the replaced provider) plus the
# go test verdict. Reverts the instrumentation afterwards. VERIFY_STORE=1 also applies verify/instrument_store.py.
set -u
wt=$1; mode=${2:-}
here=$(cd "$(dirname "$0")" && pwd)
cd "$wt" || exit 1
export GOENV=off GOWORK=off
python3 "$here/instrument.py" . $mode
tmp=$(mktemp -d .evaltools/lifetime_XXXXXX)
if [ "${VERIFY_STORE:-}" = 1 ]; then python3 "$here/instrument_store.py" .; fi
trap 'rm -rf "$tmp"; git checkout -q -- internal/credentials/xds/handshake_info.go internal/xds/balancer/clusterimpl/clusterimpl.go credentials/tls/certprovider/store.go' EXIT
cp internal/xds/balancer/clusterimpl/tests/eval_handshake_lifetime_test.go "$tmp/"
go test "./$tmp" -run '^TestEval_SecurityConfigUpdate_ActiveHandshakeKeepsProvider$' -count=1 -v -timeout 60s 2>&1 \
  | grep -E 'VERIFY-PROBE|^\s*(--- |PASS|FAIL|ok )|eval_handshake_lifetime_test.go:(2[6-9][0-9]|3[0-9][0-9]): ' | cut -c1-420
