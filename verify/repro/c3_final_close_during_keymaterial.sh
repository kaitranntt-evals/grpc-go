#!/usr/bin/env bash
# Usage: verify/repro/c3_final_close_during_keymaterial.sh   (branch 03538fd6; needs FIXTURES_DIR, see setup_worktree.sh)
# C3 repro. Runs c3_final_close_during_keymaterial_test.go against the production store wrapper of the branch.
# The tests PASS when the problem is present (final-owner Close invalidates the underlying provider while an
# admitted KeyMaterial call is still running) and FAIL with "NOT REPRODUCED" otherwise.
set -uo pipefail
here=$(cd "$(dirname "$0")" && pwd)
wt=$("$here/setup_worktree.sh" "${1:-03538fd6}") || exit 1
cd "$wt"; mkdir -p .evaltools/c3; trap 'rm -rf "$wt/.evaltools/c3"' EXIT
cp "$here/c3_final_close_during_keymaterial_test.go" .evaltools/c3/
GOENV=off GOWORK=off go test -tags verify_repro ./.evaltools/c3 -run TestC3 -count=1 -v 2>&1
