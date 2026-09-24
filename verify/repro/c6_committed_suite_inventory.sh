#!/bin/bash
# Run from a checkout of evalon/grpc-go-en-f18f8972: `bash verify/repro/c6_committed_suite_inventory.sh` — lists the committed endpointsharding tests and exits 1 if none of them reference the four behaviors C6 says are untested.
set -u
dir=balancer/endpointsharding
echo "== committed tests in $dir =="
grep -n "^func (s) Test\|^func Test" "$dir"/*_test.go
fail=0
check() { # $1 = behavior label, $2 = regexp that a test asserting it would have to contain
  if grep -nE "$2" "$dir"/*_test.go >/dev/null; then
    echo "present : $1"; grep -nE "$2" "$dir"/*_test.go | head -5
  else
    echo "MISSING : $1  (no match for /$2/ in $dir/*_test.go)"; fail=1
  fi
}
check "same-child mutual exclusion (two ops on ONE child racing)"       'SameChild|sameChild|same child|MutualExclusion'
check "ExitIdle on a retained ChildState after Close is guarded"         'AfterClose|afterClose|closed.*ExitIdle|ExitIdle.*closed'
check "sibling children still configured after one child's config error" 'ChildError|childErr|errChild|config(uration)? error|returns? an? error'
check "synchronous callback from inside ResolverError"                    'ResolverError'
exit $fail
