#!/bin/bash
# Run: ./run_c6.sh <clean worktree of evalon/grpc-go-en-735d3aa5> <clean worktree of the earlier coarse-lock base bf9e7cd3430df40d0732ba42eb88bd5f2cc63407>
# Part 1 (cleanup_mechanism): on the claim branch, delay balancerWrapper.exitIdle (c6_stall_exitidle.patch, EVAL_STALL_EXITIDLE=1)
#   so the progress assertion of TestEndpointShardingExitIdleWhileOtherChildBlocked times out; show deferred es.Close stuck on
#   child B's lock while B still waits on unblockB.
# Part 2 (reverse_execution_trigger): copy that test file onto the base worktree, changing only `childA.ExitIdle()` to
#   `childA.Balancer.ExitIdle()`, and run it against the coarse-lock implementation.
set -uo pipefail
br="$1"; base="$2"; here=$(cd "$(dirname "$0")" && pwd)
T='Test/EndpointShardingExitIdleWhileOtherChildBlocked$'
show() { # $1 = go test output
  echo "$1" | grep -E "^(--- FAIL|FAIL|ok)|_test.go:[0-9]+: |^panic: test timed out"
  echo "### cleanup goroutine (deferred es.Close after t.Fatal):"
  echo "$1" | grep -A24 "^goroutine [0-9]* \[sync.Mutex.Lock\]" | grep -E "^goroutine|endpointsharding\.\(\*(endpointSharding|balancerWrapper)\)\.(Close|close)|endpointsharding.go:[0-9]+|_test.go:[0-9]+|testing\.\(\*common\)\.Fatal"
  echo "### child B still blocked on <-unblockB inside its UpdateClientConnState:"
  echo "$1" | grep -A6 "^goroutine [0-9]* \[chan receive\]" | grep -E "^goroutine|_test.go:4[0-9][0-9]"
}
for wt in "$br" "$base"; do
  if [ -n "$(git -C "$wt" status --porcelain -- balancer/endpointsharding)" ]; then echo "$wt not clean; refusing" >&2; exit 2; fi
done
echo "##### Part 1: claim branch, exitIdle delayed so the progress assertion times out"
cd "$br"
git apply "$here/c6_stall_exitidle.patch" || exit 1
echo "### EVAL_STALL_EXITIDLE=1 go test ./balancer/endpointsharding -run '$T' -race -count=1 -v -timeout 40s"
out=$(EVAL_STALL_EXITIDLE=1 go test ./balancer/endpointsharding -run "$T" -race -count=1 -v -timeout 40s 2>&1); echo "exit=$?"
show "$out"
git checkout -- balancer/endpointsharding/endpointsharding.go
echo
echo "##### Part 2: authored test on the coarse-lock base, only childA.ExitIdle() -> childA.Balancer.ExitIdle()"
cd "$base"
cp balancer/endpointsharding/endpointsharding_ext_test.go /tmp/c6_base_ext_test.go.orig
sed 's/^\tchildA\.ExitIdle()$/\tchildA.Balancer.ExitIdle()/' "$br/balancer/endpointsharding/endpointsharding_ext_test.go" > balancer/endpointsharding/endpointsharding_ext_test.go
diff <(sed 's/childA.Balancer.ExitIdle()/childA.ExitIdle()/' balancer/endpointsharding/endpointsharding_ext_test.go) "$br/balancer/endpointsharding/endpointsharding_ext_test.go" && echo "(only the idle-exit call differs from the authored test)"
echo "### go test ./balancer/endpointsharding -run '$T' -race -count=1 -v -timeout 40s"
out=$(go test ./balancer/endpointsharding -run "$T" -race -count=1 -v -timeout 40s 2>&1); echo "exit=$?"
show "$out"
git checkout -- balancer/endpointsharding/endpointsharding_ext_test.go
