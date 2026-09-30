#!/usr/bin/env bash
# Usage: verify/probe_branch.sh <worktree-dir> [--no-close|--defer-close]
# Runs every new/changed candidate test (eval inventory helper) one at a time against the instrumented
# tree and prints the VERIFY-PROBE lines plus the go test verdict lines. Reverts the instrumentation after.
set -u
wt=$1; mode=${2:-}
BASE=cc234554fb363aea445a838b341bb8a65c8305b0
here=$(cd "$(dirname "$0")" && pwd)
cd "$wt" || exit 1
export GOENV=off GOWORK=off
files=$(git diff --name-only $BASE HEAD -- "*_test.go" "**/*_test.go" | sort -u)
inventory=$(go run .evaltools/candidate_test_inventory.go $BASE $files)
python3 "$here/instrument.py" . $mode
trap 'git checkout -q -- internal/credentials/xds/handshake_info.go internal/xds/balancer/clusterimpl/clusterimpl.go' EXIT
while IFS=$'\t' read -r test_file test_name; do
  [ -n "$test_file" ] || continue
  dir=$(dirname "$test_file")
  if [[ "$test_name" == *"/"* ]]; then parent="${test_name%/*}"; sub="${test_name##*/}"; sub="${sub#Test}"; selector="^${parent}$/^${sub}$"
  elif [[ "$test_name" == *.* ]]; then selector="^Test$/^${test_name##*.Test}$"
  else selector="^${test_name}$"; fi
  echo "##### $test_file :: $test_name   (go test ./$dir -run '$selector' -count=1 -v -timeout 60s $mode)"
  go test "./$dir" -run "$selector" -count=1 -v -timeout 60s 2>&1 | grep -E 'VERIFY-PROBE|^\s*(--- |PASS|FAIL|ok |panic: test timed out)|_test.go:[0-9]+: ' | cut -c1-400
done <<<"$inventory"
