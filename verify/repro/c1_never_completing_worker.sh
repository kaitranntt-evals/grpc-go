#!/bin/bash
# Usage: run_one.sh <branch> <testfile> <comma-separated line numbers> <TestNameWithoutTestPrefix>
# Simulates a worker that never completes: the fixture child's barrier wait (`<-release`-style line) is
# replaced by `select {}` in a scratch copy of the worktree, then the single test is run with a hard
# `go test -timeout` so that unbounded cleanup shows up as a "test timed out" panic with goroutine stacks.
set -u
b=$1; f=$2; lines=$3; name=$4
src=/home/ubuntu/wt/$b
scratch=/home/ubuntu/c1/scratch/$b-$name
rm -rf "$scratch"; mkdir -p "$(dirname "$scratch")"
git -C "$src" worktree list >/dev/null
cp -r "$src" "$scratch"
cd "$scratch"
for n in ${lines//,/ }; do
  sed -i "${n}s/^\(\s*\)<-\(\S*\)\s*$/\1_ = \2; select {} \/\/ VERIFY: worker never completes/" "$f"
done
{
  echo "### branch=$b test=$name file=$f mutated_lines=$lines"
  git --no-pager diff -- "$f"
  echo "### go test ./balancer/endpointsharding -run '^Test\$/^${name}\$' -count=1 -timeout 45s -v"
  timeout 120 go test ./balancer/endpointsharding -run "^Test\$/^${name}\$" -count=1 -timeout 45s -v 2>&1
  echo "### exit=$?"
} > /home/ubuntu/c1/runs/$b-$name.txt
rm -rf "$scratch"
