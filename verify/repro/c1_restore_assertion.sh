#!/usr/bin/env bash
# Repro for C1: restore the baseline `Message: []uint8{}` assertion on each claim branch's logging_test.go and show the focused test now fails (run from a worktree of the target branch: bash c1_restore_assertion.sh <worktree-dir>).
set -euo pipefail
wt="${1:?usage: c1_restore_assertion.sh <worktree-dir>}"
cd "$wt/gcp/observability"

echo "== as-changed run (passes) =="
go test -run 'Test/ServerRPCEventsLogAll' -count=1 . || true

echo "== restoring baseline assertion =="
if grep -qE 'Payload:\s+payload\{\},' logging_test.go; then
  # branches 105181d0/9d6b90e7/e2fa5dad/a291a4cc style: empty payload{} literal
  perl -0pi -e 's/Payload:\s+payload\{\},/Payload: payload{\n\t\t\t\tMessage: []uint8{},\n\t\t\t},/ if !$done++' logging_test.go
  gofmt -w logging_test.go
else
  # branch 538c6009 style: Message: nil at the server-message entry (line 435)
  sed -i '435s/Message: nil,/Message: []uint8{},/' logging_test.go
fi

echo "== restored run (fails with nil-vs-[]uint8{} diff) =="
go test -run 'Test/ServerRPCEventsLogAll' -count=1 . || true

git checkout -- logging_test.go
