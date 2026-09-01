#!/usr/bin/env bash
# Run: bash verify/repro/c3/run.sh [worktree-dir]   (default ~/wt/aa94d3f0, a checkout of evalon/grpc-go-xd-aa94d3f0)
# Shows that TestStoreCloseWhileLoadInProgress establishes "KeyMaterial entered
# before Close" only via time.Sleep(defaultTestShortTimeout): delaying the
# KeyMaterial goroutine by 2x that interval (a scheduling hiccup) makes Close
# run first and the regression fails, while an event-based test would be immune.
set -euo pipefail
wt=${1:-$HOME/wt/aa94d3f0}
cd "$wt"
file=credentials/tls/certprovider/store_test.go
git diff --quiet -- "$file" || { echo "$file has local changes; refusing" >&2; exit 1; }
echo "== ordering evidence in the changed regression (only a sleep precedes Close):"
grep -n 'time.Sleep(defaultTestShortTimeout)' "$file"
echo "== unmutated run"
go test ./credentials/tls/certprovider -run 'Test/StoreCloseWhileLoadInProgress' -count=1 -v 2>&1 | grep -E '^(--- |=== RUN|PASS|FAIL|ok)|store_test.go' || true
# Mutation: the KeyMaterial goroutine is scheduled 2x defaultTestShortTimeout late.
awk '!done && $0 ~ /^\t\tkm, err := prov\.KeyMaterial\(ctx\)$/ { print "\t\ttime.Sleep(2 * defaultTestShortTimeout) // C3 mutation: late scheduling of the load goroutine"; done=1 } { print } END { if (!done) { print "no KeyMaterial line matched" > "/dev/stderr"; exit 1 } }' "$file" > "$file.c3tmp"
mv "$file.c3tmp" "$file"
cleanup() { git checkout -- "$file"; }
trap cleanup EXIT
echo "== mutation applied:"; git --no-pager diff -- "$file"
echo "== mutated run"
go test ./credentials/tls/certprovider -run 'Test/StoreCloseWhileLoadInProgress' -count=1 -v 2>&1 | grep -E '^(--- |    --- |=== RUN|PASS|FAIL|ok)|store_test.go' || true
