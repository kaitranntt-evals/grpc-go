#!/usr/bin/env bash
# C3 repro: run from a checkout of branch evalon/grpc-go-xd-dcaf16f3 (kaitranntt-evals/grpc-go-xds-certificate-provider-closure-race):
#   bash verify/repro/c3_retry_branch_coverage.sh
# Shows that TestAcquireHandshakeInfoRetriesOnStaleValue never executes the
# "retry with the replacement" statement (hi = cur) of AcquireHandshakeInfo, and
# that replacing that statement with `return nil` keeps the test green.
set -euo pipefail
unset GOFLAGS GOTOOLCHAIN GOWORK GOCACHE GOENV
export GOENV=off GOWORK=off

cov=$(mktemp)
go test ./internal/credentials/xds -run 'Test/AcquireHandshakeInfoRetriesOnStaleValue$' -count=1 -coverprofile="$cov" -v 2>&1 | grep -E '^\s*(--- |ok|FAIL)'
echo "== coverage blocks of AcquireHandshakeInfo (last column = execution count)"
grep -n 'func AcquireHandshakeInfo' internal/credentials/xds/handshake_info.go
grep -E 'handshake_info.go:(19[5-9]|20[0-6])\.' "$cov"
rm -f "$cov"

echo "== mutate 'hi = cur' -> 'return nil' and re-run the test"
line=$(grep -n '^		hi = cur$' internal/credentials/xds/handshake_info.go | cut -d: -f1)
cp internal/credentials/xds/handshake_info.go "$cov.bak"
sed -i "${line}s/hi = cur/return nil \/\/ MUTATION/" internal/credentials/xds/handshake_info.go
go test ./internal/credentials/xds -run 'Test/AcquireHandshakeInfoRetriesOnStaleValue$' -count=1 -v 2>&1 | grep -E '^\s*(--- |ok|FAIL)'
go test ./credentials/xds -run 'Test/ClientCredsProviderReplacedDuringHandshake$' -count=1 -v 2>&1 | grep -E '^\s*(--- |ok|FAIL)'
mv "$cov.bak" internal/credentials/xds/handshake_info.go
