#!/bin/sh
# Run: sh verify/repro/c2_stop_wait.sh  (from a checkout of the verify branch; needs git, go and read access to the claim repository; takes about a minute)
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

BASE=c92e985770b7194d4a4f433c84d42c6c195e8ce5
echo "== server.stop() as delivered"
grep -n -A11 '^func (s \*server) stop() {' internal/transport/transport_test.go
echo "== lines of transport_test.go changed by the solution relative to its base $BASE: $(git diff "$BASE" HEAD -- internal/transport/transport_test.go | wc -l)"
echo "== calls to server.stop() in the base test file: $(git show "$BASE":internal/transport/transport_test.go | grep -c 'server.stop()')"
echo "== cleanup of the added integration test"
grep -n -B1 -A6 '^func (s) TestServerStreamReceivesManyTinyDataFrames' internal/transport/recv_buffer_test.go

# Test-only instrumentation: prints the caller of stop() before and after the completion-channel receive.
git apply "$HERE/testdata/c2_transport_test.patch"
cp "$HERE/testdata/verify_c2_stop_test.go" internal/transport/
git status --short
set -x
# test_reachability: the added integration test reaches the receive from its deferred server.stop().
go test ./internal/transport -count=1 -v -run '^Test$/^ServerStreamReceivesManyTinyDataFrames$'
# cleanup_mechanism: with a serving goroutine still alive, stop() stays parked on the receive.
go test ./internal/transport -count=1 -v -run '^Test$/^VerifyC2_StopWaitHasNoLocalBound$'
# How long the receive takes in the added test in practice.
go test -race ./internal/transport -count=100 -v -run '^Test$/^ServerStreamReceivesManyTinyDataFrames$' > "$WT/../c2_stress.log" 2>&1 || true
set +x
tail -n 1 "$WT/../c2_stress.log"
echo "== 100 runs under -race: entered the receive $(grep -c 'recv_buffer_test.go:362: entering bare receive' "$WT/../c2_stress.log") times, completed $(grep -c 'recv_buffer_test.go:362: receive completed' "$WT/../c2_stress.log") times, longest wait $(sed -n 's/.*recv_buffer_test.go:362: receive completed.*(us=\([0-9]*\)).*/\1/p' "$WT/../c2_stress.log" | sort -n | tail -n 1) microseconds"
