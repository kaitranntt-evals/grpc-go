#!/bin/sh
# Run: sh verify/repro/c1_heap_test_pool_accounting.sh  (from a checkout of the verify branch; needs git, go and read access to the claim repository)
set -eu
HERE=$(cd "$(dirname "$0")" && pwd)
REPO=$(git -C "$HERE" rev-parse --show-toplevel)
# The claim branch lives in a different repository than origin. CLAIM_REPO may be a URL or the name of a remote.
CLAIM_REPO=${CLAIM_REPO:-https://github.com/kaitranntt-evals/grpc-go-transport-restrict-memory-overhead}
BRANCH=evalon/grpc-go-tr-09960403
COMMIT=ed7b3532e1dd13dd0bbcb644ba2fd9a42be45799
WT=${WT:-$(mktemp -d)/09960403}
git -C "$REPO" fetch --quiet "$CLAIM_REPO" "$BRANCH"
[ "$(git -C "$REPO" rev-parse FETCH_HEAD)" = "$COMMIT" ] || echo "note: $BRANCH has moved since the audit (audited $COMMIT)"
git -C "$REPO" worktree add --quiet --detach "$WT" "$COMMIT"
cd "$WT"
echo "== worktree $WT at $(git rev-parse HEAD) ($BRANCH); $(go version)"

# Test-only instrumentation: keeps the test's own countingPool in a variable and logs its Get/Put balance from t.Cleanup.
git apply "$HERE/testdata/c1_recv_buffer_test.patch"
git diff --stat
set -x
go test ./internal/transport -count=1 -v -run '^Test$/^RecvBuffer_SmallBuffersHeapUsage$'
# Contrast: the sibling test that does read and free what it queued ends with nothing outstanding (it asserts that).
go test ./internal/transport -count=1 -v -run '^Test$/^RecvBuffer_CompactionReleasesBuffers$'
# Counterfactual: give the same test the default pool, which the package's own leak checker (internal/leakcheck, run by
# grpctest at teardown) tracks; the checker then reports the chunks the test never frees.
git apply "$HERE/testdata/c1_default_pool_counterfactual.patch"
go test ./internal/transport -count=1 -v -run '^Test$/^RecvBuffer_SmallBuffersHeapUsage$'
