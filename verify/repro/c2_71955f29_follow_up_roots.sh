#!/usr/bin/env bash
# Run from the repo root: bash verify/repro/c2_71955f29_follow_up_roots.sh  (needs network access to the claims repository)
exec bash "$(dirname "${BASH_SOURCE[0]}")/lib/run_follow_up_roots_mutation.sh" 71955f29 ./credentials/xds/ '^Test$/ClientCredsSecurityConfigReplacedDuringHandshake$'
