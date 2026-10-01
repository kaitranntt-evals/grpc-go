#!/bin/sh
# Run: sh verify/repro/c4_mixed_capacity.sh  (from a checkout of the verify branch; needs git, go and read access to the claim repository)
set -eu
HERE=$(cd "$(dirname "$0")" && pwd)
REPO=$(git -C "$HERE" rev-parse --show-toplevel)
# The claim branch lives in a different repository than origin. CLAIM_REPO may be a URL or the name of a remote.
CLAIM_REPO=${CLAIM_REPO:-https://github.com/kaitranntt-evals/grpc-go-transport-restrict-memory-overhead}
BRANCH=evalon/grpc-go-tr-93d15123
COMMIT=fd919bf37d244d9e9fb4addd0c561a1259eb77c1
WT=${WT:-$(mktemp -d)/93d15123}
git -C "$REPO" fetch --quiet "$CLAIM_REPO" "$BRANCH"
[ "$(git -C "$REPO" rev-parse FETCH_HEAD)" = "$COMMIT" ] || echo "note: $BRANCH has moved since the audit (audited $COMMIT)"
git -C "$REPO" worktree add --quiet --detach "$WT" "$COMMIT"
cd "$WT"
echo "== worktree $WT at $(git rev-parse HEAD) ($BRANCH); $(go version)"

cp "$HERE/testdata/verify_c4_capacity_test.go" internal/transport/
git status --short
set -x
go vet ./internal/transport
# mixed_traffic_capacity on a bare recvBuffer (variants A-F) and through a real http2Server stream; partial_read_retention.
go test ./internal/transport -count=1 -v -run '^Test$/^VerifyC4_(MixedTrafficCapacity|MixedTrafficCapacity_ServerTransport|PartialReadRetention)$'
# The escape hatch, through the real environment variable (the test above overrides the setting in-process).
go test ./internal/transport -count=1 -v -run '^Test$/^VerifyC4_MixedTrafficCapacity_ProcessEnv$'
GRPC_GO_EXPERIMENTAL_ENABLE_RECEIVE_BUFFER_COMPACTION=false go test ./internal/transport -count=1 -v -run '^Test$/^VerifyC4_MixedTrafficCapacity_ProcessEnv$'
