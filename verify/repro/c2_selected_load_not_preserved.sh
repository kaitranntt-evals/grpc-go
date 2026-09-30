#!/usr/bin/env bash
# Usage: verify/repro/c2_selected_load_not_preserved.sh <short-branch-id>...   (03538fd6 c9d55e2d 49f3c746; needs FIXTURES_DIR, see setup_worktree.sh)
# C2 repro. Runs the archived hidden fixture (Cluster security update replaces provider plugin A by a
# different plugin B = a different store cache entry, while a handshake is blocked in A's KeyMaterial) with
# store/load/balancer probes. Shows: no store handle is acquired between handshake start and the KeyMaterial
# call (entry A refCount stays 1 = the balancer's handle), the balancer's Close drops it to 0, the underlying
# provider is closed while load#1 is running, load#1 ENDs with "provider instance is closed" and the RPC fails.
set -uo pipefail
here=$(cd "$(dirname "$0")" && pwd)
for id in "$@"; do
  wt=$("$here/setup_worktree.sh" "$id") || exit 1
  echo "##### $id"
  VERIFY_STORE=1 "$here/../probe_fixture.sh" "$wt" 2>&1 | sed 's/VERIFY-PROBE //'
done
