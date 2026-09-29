#!/usr/bin/env bash
# Run from a worktree of evalon/grpc-go-xd-8069941c or evalon/grpc-go-xd-d3a76037: bash <path-to>/verify/repro/c2_final_release_cleanup_untested.sh  (removes provider cleanup from clusterImplBalancer.Close(); 8069941c: all changed tests PASS; d3a76037: only the generic goroutine-leak checker trips, on the replacement pemfile providers)
set -uo pipefail
V="$(cd "$(dirname "$0")/.." && pwd)"
bash "$V/instrument/apply_no_final_close_mutant.sh" || exit 1
go test ./internal/xds/balancer/clusterimpl/tests -run 'Test/SecurityConfigUpdate_ProvidersReplacedDuringHandshake$' -count=1 -v 2>&1 | grep -E 'Leaked goroutine|created by|^\s*--- |^(ok|FAIL)'
go test ./credentials/xds -run 'Test/ClientCreds(ProviderReplaced|ProviderClosed)DuringHandshake$' -count=1 -v 2>&1 | grep -E '^\s*--- |^(ok|FAIL)'
git checkout -q internal/xds/balancer/clusterimpl/clusterimpl.go
