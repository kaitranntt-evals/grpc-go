#!/usr/bin/env bash
# Run: from a CLEAN checkout of grpc-go-xds-certificate-provider-closure-race-perfect (vet.sh runs `git reset --hard HEAD` on exit):  bash verify/repro/c5_run_vet.sh
set -uo pipefail
./scripts/vet.sh -install
./scripts/vet.sh 2>&1 | grep -v '^+ grep -L'   # first failing gate prints the offending file, then the script exits 1
echo "vet.sh exit=${PIPESTATUS[0]}"
