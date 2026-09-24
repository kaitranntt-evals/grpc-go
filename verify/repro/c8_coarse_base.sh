#!/bin/bash
# Run: verify/repro/c8_coarse_base.sh   (from repo root; copies evalon/grpc-go-en-53e8c596's endpointsharding_child_test.go onto its coarse-lock parent bf9e7cd3, adapts only ChildState.ExitIdle() -> .Balancer.ExitIdle(), runs the independent-progress test with a 60s deadline)
set -u
repo=$(git rev-parse --show-toplevel); url=https://github.com/kaitranntt-evals/grpc-go-endpointsharding-decouple-locking.git
git -C "$repo" fetch -q "$url" evalon/grpc-go-en-53e8c596
wt=$(mktemp -d)/wt; git -C "$repo" worktree add -q --detach "$wt" FETCH_HEAD~1   # bf9e7cd3, coarse-lock base
git -C "$repo" show FETCH_HEAD:balancer/endpointsharding/endpointsharding_child_test.go \
  | sed -E 's/(childState[12])\.ExitIdle\(\)/\1.Balancer.ExitIdle()/' > "$wt/balancer/endpointsharding/endpointsharding_child_test.go"
git -C "$repo" show FETCH_HEAD:balancer/endpointsharding/endpointsharding_child_test.go | diff - "$wt/balancer/endpointsharding/endpointsharding_child_test.go"
cd "$wt" && go test -c -race -o /tmp/c8.test ./balancer/endpointsharding || exit 1
cd balancer/endpointsharding; s=$(date +%s)
/tmp/c8.test -test.run '^Test$/^EndpointSharding_ExitIdleNotBlockedByOtherChildUpdate$' -test.count=1 -test.timeout=60s -test.v > /tmp/c8.log 2>&1
echo "rc=$? elapsed=$(( $(date +%s)-s ))s"; grep -E '_test\.go:[0-9]+: |^panic|^goroutine .*Mutex|endpointsharding(_child_test)?\.go:[0-9]+' /tmp/c8.log | head -20
git -C "$repo" worktree remove --force "$wt"
