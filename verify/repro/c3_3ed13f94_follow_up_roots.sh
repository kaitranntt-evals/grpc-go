#!/usr/bin/env bash
# Run from the repo root: bash verify/repro/c3_3ed13f94_follow_up_roots.sh  (needs network access to the claims repository)
exec bash "$(dirname "${BASH_SOURCE[0]}")/lib/run_follow_up_roots_mutation.sh" 3ed13f94 ./internal/xds/balancer/clusterimpl/tests/ '^Test$/SecurityConfigUpdate_ReplacedDuringHandshake$'
