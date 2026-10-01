#!/bin/bash
# Run: verify/repro/run_probe.sh <audited|short-branch-id> <pkg: test/xds|internal/xds/server> <run-regex> <probe files under verify/repro...>
# Copies the probe test files into the package of the chosen checkout, runs them with -race, removes them again.
# Raw log: $OUT/<label>.log where label = <first probe file without _test.go>.<branch>
source "$(dirname "$0")/common.sh"
b=$1; pkg=$2; rx=$3; shift 3
d=$(dir_of "$b"); cd "$d" || exit 2
label=$(basename "${@: -1}" _test.go).$b
for f in "$@"; do cp "$REPO/verify/repro/$f" "$pkg/"; done
go test -tags verify_audit -race -count=1 -v -timeout 180s -run "$rx" "./$pkg" > "$OUT/$label.log" 2>&1; echo "exit=$?" >> "$OUT/$label.log"
for f in "$@"; do rm -f "$pkg/$f"; done
# print only the probe's own lines
grep -E '^\s+verify_[a-z0-9_]+_test\.go:[0-9]+: |^\s*--- (PASS|FAIL)|^ok|^FAIL|^exit=|^ {16}\S|DATA RACE|^panic' "$OUT/$label.log" | sed -E 's/^\s+verify_[a-z0-9_]+_test\.go:[0-9]+: //'
