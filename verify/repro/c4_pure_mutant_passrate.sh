#!/bin/sh
# Run (cwd = clean checkout of evalon/grpc-go-en-30a9d20e): sh <path-to>/verify/repro/c4_pure_mutant_passrate.sh [runs, default 100]
#
# Assertion-outcome repro for C4 with no instrumentation at all. Removes the one
# line that makes a removed child ignore ExitIdle (bw.isClosed = true), builds
# the branch's own test binary with -race, and counts how often the branch's
# own TestEndpointSharding_ChildExitIdleAfterRemoval still reports PASS. The
# same loop is run first against the unmutated branch as a control.
set -u
n=${1:-100}
here=$(cd "$(dirname "$0")" && pwd)
count() { # $1 = test binary, $2 = label
  p=0; f=0; i=0
  while [ $i -lt "$n" ]; do
    if "$1" -test.run 'Test/EndpointSharding_ChildExitIdleAfterRemoval' -test.count=1 >/dev/null 2>&1; then p=$((p+1)); else f=$((f+1)); fi
    i=$((i+1))
  done
  echo "$2: runs=$n PASS=$p FAIL=$f"
}
go test -c -race -o /tmp/c4_unmutated.test ./balancer/endpointsharding || exit 2
count /tmp/c4_unmutated.test "unmutated branch (removed child A never receives ExitIdle)"
git apply "$here/c4_guard_lost_pure_mutant.patch" || exit 2
git diff --stat
go test -c -race -o /tmp/c4_pure_mutant.test ./balancer/endpointsharding
git apply -R "$here/c4_guard_lost_pure_mutant.patch"
count /tmp/c4_pure_mutant.test "guard-lost mutant (removed child A receives ExitIdle on every run)"
