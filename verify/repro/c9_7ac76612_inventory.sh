#!/usr/bin/env bash
# Repro for C9: inventory the tests added on evalon/grpc-go-xd-7ac76612 and run them.
# The only live-xDS e2e test (SecurityConfigUpdate_ValidationRootsReplaced) completes its
# first handshake BEFORE the Cluster update — no handshake is in flight during the update
# and no provider-cleanup assertion exists; the RefCountedProvider tests are unit-level.
set -euo pipefail
# Run from a checkout of evalon/grpc-go-xd-7ac76612.
git diff cc234554 HEAD --stat
git diff cc234554 HEAD | grep '^+func (s) Test'
go test ./internal/xds/balancer/clusterimpl/tests/ -run 'Test/SecurityConfigUpdate_ValidationRootsReplaced' -count=1 -v
go test ./internal/credentials/xds/ -run 'Test/RefCountedProvider' -count=1 -v
