#!/bin/sh
# Run (cwd = clean checkout of evalon/grpc-go-en-<id>): sh <path-to>/verify/repro/c2_cleanup_hang.sh <f7aa6335|0fd80b14> [go-test-timeout, default 45s]   (from the root of a checkout of evalon/grpc-go-en-<id>; needs verify/repro/*.patch from the verify branch next to this script)
#
# Claim C2: applies a production mutant in which an idle exit deadlocks (es.mu
# held across the call into the child), runs the branch's own concurrency test,
# and shows that after the test's 10s assertion timeout fires, its deferred
# cleanup `<-done` waits forever for the production worker: the binary is only
# ended by go test's global -timeout panic. Restores the file afterwards.
set -u
id=$1; timeout=${2:-45s}
here=$(cd "$(dirname "$0")" && pwd)
case $id in
  f7aa6335) run='Test/ChildOperationsIndependent/UpdateClientConnState/exitAll=false' ;;
  0fd80b14) run='Test/ChildExitIdleIndependent/UpdateClientConnState' ;;
  *) echo "unknown branch id $id" >&2; exit 2 ;;
esac
git apply "$here/c2_${id}_deadlock_mutant.patch" || exit 2
start=$(date +%s)
go test ./balancer/endpointsharding -run "$run" -race -count=1 -v -timeout "$timeout"
rc=$?
echo "go test exit=$rc after $(( $(date +%s) - start ))s (test's own assertion deadline is 10s; go test -timeout was $timeout)"
git apply -R "$here/c2_${id}_deadlock_mutant.patch"
