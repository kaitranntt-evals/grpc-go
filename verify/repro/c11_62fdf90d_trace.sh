#!/usr/bin/env bash
# Repro for C11: the post-update assertion in clusterimpl_security_test.go on
# evalon/grpc-go-xd-62fdf90d is `for ctx.Err() == nil { client.EmptyCall(...) }` with no
# event wait, sleep, backoff, or attempt bound — it spins until the context deadline.
set -euo pipefail
# Run from a checkout of evalon/grpc-go-xd-62fdf90d.
sed -n '/Verify that RPCs to the new backend fail/,/^}/p' internal/xds/balancer/clusterimpl/tests/clusterimpl_security_test.go
go test ./internal/xds/balancer/clusterimpl/tests/ -run 'Test/SecurityConfigUpdate_ReplacementRootsDoNotTrustServer' -count=1 -v
