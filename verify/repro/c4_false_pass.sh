#!/bin/sh
# Run (cwd = clean checkout of evalon/grpc-go-en-30a9d20e): sh <path-to>/verify/repro/c4_false_pass.sh [runs, default 300]   (from the root of a checkout of evalon/grpc-go-en-30a9d20e; needs the verify/repro files from the verify branch next to this script)
#
# Claim C4: (1) calls the branch's awaitAddr with A's event queued before B's
# and shows A's event is gone afterwards; (2) applies a mutant that drops the
# closed-child guard (removed child A keeps receiving ExitIdle) plus delivery
# numbering, then tabulates delivery order against the PASS/FAIL result of the
# branch's TestEndpointSharding_ChildExitIdleAfterRemoval. Restores files afterwards.
set -u
n=${1:-300}
here=$(cd "$(dirname "$0")" && pwd)
cp "$here/c4_awaitaddr_probe_test.go.txt" balancer/endpointsharding/c4_awaitaddr_probe_test.go
go test ./balancer/endpointsharding -run '^TestVerifyC4_AwaitAddrDiscardsForbiddenEvent$' -count=1 -v
rm balancer/endpointsharding/c4_awaitaddr_probe_test.go
git apply "$here/c4_guard_lost_mutant.patch" || exit 2
go test -c -race -o /tmp/c4_mutant.test ./balancer/endpointsharding
git apply -R "$here/c4_guard_lost_mutant.patch"
sh "$here/c4_classify.sh" /tmp/c4_mutant.test "$n"
