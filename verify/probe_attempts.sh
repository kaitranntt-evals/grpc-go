#!/usr/bin/env bash
# Usage: verify/probe_attempts.sh <worktree-dir> <package-dir> <go-test-run-selector>
# Runs one candidate test with both verify/instrument.py and verify/instrument_attempts.py applied and prints
# the VERIFY-PROBE lines (connection attempts, validation-root loads, balancer retirements) + verdict lines.
# VERIFY_STORE=1 also applies verify/instrument_store.py (certprovider store handle acquire/release events).
set -u
wt=$1; pkg=$2; sel=$3
here=$(cd "$(dirname "$0")" && pwd)
cd "$wt" || exit 1
export GOENV=off GOWORK=off
python3 "$here/instrument.py" . >/dev/null
python3 "$here/instrument_attempts.py" .
if [ "${VERIFY_STORE:-}" = 1 ]; then python3 "$here/instrument_store.py" .; fi
trap 'git checkout -q -- internal/credentials/xds/handshake_info.go internal/xds/balancer/clusterimpl/clusterimpl.go credentials/xds/xds.go credentials/tls/certprovider/store.go' EXIT
go test "./$pkg" -run "$sel" -count=1 -v -timeout 60s 2>&1 | grep -E 'VERIFY-PROBE|^\s*(--- |PASS|FAIL|ok )' | sed -E 's/VERIFY-PROBE //' | cut -c1-300
