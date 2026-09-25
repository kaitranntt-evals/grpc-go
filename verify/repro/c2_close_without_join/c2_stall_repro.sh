#!/bin/bash
# Run: ./c2_stall_repro.sh <worktree-of-claim-branch> '<go test -run regex>' [EVAL_STALL_AT, default 3]
#   e.g. ./c2_stall_repro.sh ~/wt/7342a4ed 'Test/EndpointShardingExitIdleWhileOtherChildBlocked$'
# Injects an env-gated stall into balancerWrapper.updateClientConnState (the n-th and later
# child configuration calls sleep 120s while still holding the per-child lock), runs the
# branch's ExitIdle-while-another-child-is-blocked test with -timeout 45s, prints the
# failure line and the cleanup goroutine that is stuck in endpointSharding.Close, then
# reverts the injection.  Per-branch test regexes / stall points used in the audit:
#   7342a4ed 'Test/EndpointShardingExitIdleWhileOtherChildBlocked$'            1
#   0799fdea 'Test/EndpointShardingExitIdleNotBlockedByOtherChild$'            1
#   d1f27009 'Test/ExitIdleWhileOtherChildUpdateBlocked$'                      3
#   f01d9196 'Test/ExitIdleDuringOtherChildUpdate/autoReconnect=false$'        3
#   b9ec3844 'Test/ExitIdleDuringOtherChildUpdate/autoReconnect=false$'        3
#   74ae6210 'Test/EndpointShardingIndependentExitIdle$'                       3
#   ebd19ec9 'Test/ExitIdleWhileOtherChildUpdating/explicit$'                  3
#   c58d8af6 'Test/ExitIdleIndependentOfOtherChildUpdate$'                     3
#   8e9bfa2b 'Test/ExitIdleDuringOtherChildUpdate/autoReconnect=false$'        3
#   59bf0e19 'Test/ExitIdleDuringOtherChildUpdate/autoReconnect=false$'        3
#   8e09516b 'Test/ChildExitIdleDuringBlockedUpdate/autoReconnect=false$'      3
#   b936e282 'Test/EndpointShardingIndependentExitIdle/explicit$'              3
set -uo pipefail
wt="$1"; run="$2"; stall="${3:-3}"
f="$wt/balancer/endpointsharding/endpointsharding.go"
cd "$wt"
if [ -n "$(git status --porcelain -- balancer/endpointsharding)" ]; then
  echo "balancer/endpointsharding is not clean in $wt; refusing to inject" >&2; exit 2
fi
python3 - "$f" <<'EOF'
import re, sys
p = sys.argv[1]
s = open(p).read()
m = re.search(r'func \(bw \*balancerWrapper\) updateClientConnState\(ccs balancer\.ClientConnState\) error \{.*?\n\}\n', s, re.S)
assert m, "updateClientConnState not found"
body = m.group(0)
assert "return bw.child.UpdateClientConnState(ccs)" in body
s = s.replace(body, body.replace("return bw.child.UpdateClientConnState(ccs)",
    "err := bw.child.UpdateClientConnState(ccs)\n\tevalMaybeStall()\n\treturn err"))
open(p, "w").write(s)
EOF
cat > balancer/endpointsharding/eval_stall.go <<'EOF'
package endpointsharding

import (
	"os"
	"strconv"
	"sync/atomic"
	"time"
)

var evalStallCalls atomic.Int32

// evalMaybeStall is audit instrumentation: when EVAL_STALL_AT=n is set, the
// n-th and later child configuration calls sleep for 120s while the caller
// still holds the per-child lock, simulating a worker that never completes
// within the test deadline.
func evalMaybeStall() {
	n, err := strconv.Atoi(os.Getenv("EVAL_STALL_AT"))
	if err != nil || n <= 0 {
		return
	}
	if int(evalStallCalls.Add(1)) >= n {
		time.Sleep(120 * time.Second)
	}
}
EOF
echo "### baseline (no stall):"
go test ./balancer/endpointsharding -run "$run" -race -count=1 2>&1 | tail -1
echo "### EVAL_STALL_AT=$stall go test ./balancer/endpointsharding -run '$run' -race -count=1 -v -timeout 45s"
out=$(EVAL_STALL_AT="$stall" go test ./balancer/endpointsharding -run "$run" -race -count=1 -v -timeout 45s 2>&1)
echo "exit=$?"
echo "$out" | grep -E "^(--- FAIL|FAIL|ok)|_test.go:[0-9]+: |^panic: test timed out"
echo "### cleanup goroutine blocked in Close:"
echo "$out" | grep -A22 "^goroutine [0-9]* \[sync.Mutex.Lock\]" | grep -E "^goroutine|endpointsharding\.\(\*(endpointSharding|balancerWrapper)\)\.(Close|close)|_test.go:[0-9]+|testing\.\(\*common\)\.(Fatal|FailNow)|tRunner" 
echo "### stalled worker still holding the child lock:"
echo "$out" | grep -A14 "^goroutine [0-9]* \[sleep\]" | grep -E "^goroutine|evalMaybeStall|balancerWrapper\)\.updateClientConnState"
rm balancer/endpointsharding/eval_stall.go
git checkout -- balancer/endpointsharding/endpointsharding.go
