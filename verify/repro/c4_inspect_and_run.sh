#!/usr/bin/env bash
# Repro for C4: run the cleanup tests added on each target branch and inspect them —
# each has at most one active handshake/owner overlapping one update; none creates two
# simultaneous owners nor asserts exactly-once cleanup after the last of multiple owners.
set -euo pipefail
# In a checkout of evalon/grpc-go-xd-8f1458a1:
go test ./credentials/xds/ ./internal/credentials/xds/ -run 'Test/(ClientCredsProviderSwitchGoodToBad|HandshakeInfoStoreProviderLifetime)' -count=1 -v
# In a checkout of evalon/grpc-go-xd-48212db3:
# go test ./credentials/xds/ -run 'Test/ClientCredsProviderReplacementDuringRootLoad' -count=1 -v
# In a checkout of evalon/grpc-go-xd-4114f548:
# go test ./credentials/xds/ ./internal/credentials/xds/ -run 'Test/(ClientCredsProviderSwitchDuringRootLoad|HandshakeInfoPointerRetainsSelectedProviders)' -count=1 -v
