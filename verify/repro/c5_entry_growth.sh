#!/bin/sh
# Run: sh verify/repro/c5_entry_growth.sh  (from a checkout of the verify branch; needs git, go and read access to the claim repository)
set -eu
HERE=$(cd "$(dirname "$0")" && pwd)
REPO=$(git -C "$HERE" rev-parse --show-toplevel)
# The claim branch lives in a different repository than origin. CLAIM_REPO may be a URL or the name of a remote.
CLAIM_REPO=${CLAIM_REPO:-https://github.com/kaitranntt-evals/grpc-go-transport-restrict-memory-overhead}
BRANCH=evalon/grpc-go-tr-92749eaf
COMMIT=75122757d6130c546a3d3ed7448391ee9d842c1e
WT=${WT:-$(mktemp -d)/92749eaf}
git -C "$REPO" fetch --quiet "$CLAIM_REPO" "$BRANCH"
[ "$(git -C "$REPO" rev-parse FETCH_HEAD)" = "$COMMIT" ] || echo "note: $BRANCH has moved since the audit (audited $COMMIT)"
git -C "$REPO" worktree add --quiet --detach "$WT" "$COMMIT"
cd "$WT"
echo "== worktree $WT at $(git rev-parse HEAD) ($BRANCH); $(go version)"

echo "== the comment and the allocation logic as delivered"
grep -n -B8 -A12 '^func (b \*recvBuffer) compactLocked' internal/transport/transport.go
grep -n 'CompactionBufferSize *=' internal/transport/transport.go
cp "$HERE/testdata/verify_c5_entry_growth_test.go" internal/transport/
git status --short
set -x
go test ./internal/transport -count=1 -v -run '^Test$/^VerifyC5_QueueEntryGrowth$'
