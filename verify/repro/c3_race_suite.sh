#!/bin/sh
# Run: sh verify/repro/c3_race_suite.sh  (from a checkout of the verify branch; needs git, go and read access to the claim repository; takes a few minutes)
set -eu
HERE=$(cd "$(dirname "$0")" && pwd)
REPO=$(git -C "$HERE" rev-parse --show-toplevel)
# The claim branch lives in a different repository than origin. CLAIM_REPO may be a URL or the name of a remote.
CLAIM_REPO=${CLAIM_REPO:-https://github.com/kaitranntt-evals/grpc-go-transport-restrict-memory-overhead}
BRANCH=evalon/grpc-go-tr-eebab2d1
COMMIT=ea19718738ad4259aaa9c9693291fc1babdce279
WT=${WT:-$(mktemp -d)/eebab2d1}
git -C "$REPO" fetch --quiet "$CLAIM_REPO" "$BRANCH"
[ "$(git -C "$REPO" rev-parse FETCH_HEAD)" = "$COMMIT" ] || echo "note: $BRANCH has moved since the audit (audited $COMMIT)"
git -C "$REPO" worktree add --quiet --detach "$WT" "$COMMIT"
cd "$WT"
echo "== worktree $WT at $(git rev-parse HEAD) ($BRANCH); $(go version)"

BASE=c92e985770b7194d4a4f433c84d42c6c195e8ce5
git status --short
rc() { echo "exit status: $?"; }
set -x
# 1. The command from the claim, on the delivered tree, unmodified.
go test -race ./internal/transport -count=1 || rc
# 2. Only the failing test, verbose, three times, to show the assertion and that it is not a flake.
go test -race ./internal/transport -count=3 -v -run '^Test$/^RecvBufferCompaction$' || rc
# 3. Same test without the race detector.
go test ./internal/transport -count=1 -run '^Test$/^RecvBufferCompaction$' || rc
# 4. Everything else in the package under -race (the one failing test skipped from the command line).
go test -race ./internal/transport -count=1 -skip '^Test$/^RecvBufferCompaction$' || rc
# 5. The base commit the solution started from, same command as 1.
git worktree add --quiet --detach "$WT-base" "$BASE"
cd "$WT-base"
go test -race ./internal/transport -count=1 || rc
# 6. Why the assertion fails: replay the failing case with and without the test-owned large.Ref().
cd "$WT"
cp "$HERE/testdata/verify_c3_rootcause_test.go" internal/transport/
go test -race ./internal/transport -count=1 -v -run '^Test$/^VerifyC3_LargeBufferPoolReturn$' || rc
