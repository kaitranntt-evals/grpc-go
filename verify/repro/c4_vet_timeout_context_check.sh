#!/bin/bash
# Run: verify/repro/c4_vet_timeout_context_check.sh <worktree of evalon/grpc-go-xd-beb780c9>   (read-only; changes nothing)
set -u
cd "$1" || exit 2
f=internal/xds/server/listener_wrapper_test.go
echo '$ git grep -n "SetConnection(" -- '$f
git grep -n "SetConnection(" -- $f
echo '$ git diff 4ee6ac46 HEAD -- '$f' | grep -n "SetConnection\|WithTimeout\|WithCancel\|^+func (s)"'
git diff 4ee6ac46 HEAD -- $f | grep -n "SetConnection\|WithTimeout\|WithCancel\|^+func (s)"
echo '$ sed -n 80p scripts/vet.sh'
sed -n 80p scripts/vet.sh
pipeline() { git grep -e 'context.Background()' --or -e 'context.TODO()' "$@" -- "*_test.go" | grep -v "benchmark/primitives/context_test.go" | grep -v 'context.WithTimeout(' | grep -v 'context.WithCancel('; }
echo '$ the same pipeline without the leading "not", on the branch working tree:'
pipeline; echo "pipeline exit=$? (0 => offending lines found => vet.sh's \"not grep\" fails)"
echo '$ the same pipeline at base 4ee6ac46:'
pipeline 4ee6ac46; echo "pipeline exit=$? (1 => clean)"
echo '$ go test -race -count=1 -v -run "Test/ListenerWrapper_RDSUpdateErrorAndClose" ./internal/xds/server/'
go test -race -count=1 -v -run "Test/ListenerWrapper_RDSUpdateErrorAndClose" ./internal/xds/server/ 2>&1 | grep -E '^(---|    ---|ok|FAIL|PASS)'
