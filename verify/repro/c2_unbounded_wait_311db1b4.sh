#!/bin/bash
# Run: verify/repro/c2_unbounded_wait_311db1b4.sh <checkout of evalon/grpc-go-xd-311db1b4>
# Shows that the bare `l.Accept()` in TestFilterChainCleanupAfterConnectionClose has no deadline or
# cancellation: when the wrapper drops the dialed connection (as it does in not-serving mode or on a
# filter-chain mismatch) no connection is ever delivered and the test blocks until the go test binary
# timeout kills it.
set -uo pipefail
cd "$1"
f=internal/xds/server/listener_wrapper.go
cp $f /tmp/c2_311db1b4.orig
# Mutation (simulated regression): Accept treats every connection as arriving in not-serving mode.
perl -0pi -e 's/if l\.mode == connectivity\.ServingModeNotServing \{\n(\t\t\t\/\/ Close connections as soon as we accept them)/if true || l.mode == connectivity.ServingModeNotServing { \/\/ audit mutation: connection absent\n$1/' $f
grep -n "audit mutation" $f
start=$(date +%s)
go test -race -count=1 -timeout 40s -run '^Test$/^FilterChainCleanupAfterConnectionClose$' ./internal/xds/server 2>&1 | grep -E "panic: test timed out|running tests:|Test/FilterChainCleanupAfterConnectionClose \(|listenerWrapper\)\.Accept|filter_chain_lifecycle_test.go:[0-9]+ \+|^FAIL|^ok" | head -20
echo "elapsed=$(( $(date +%s) - start ))s (binary timeout 40s)"
cp /tmp/c2_311db1b4.orig $f
