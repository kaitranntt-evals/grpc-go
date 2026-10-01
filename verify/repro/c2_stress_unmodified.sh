#!/bin/bash
# Run: verify/repro/c2_stress_unmodified.sh <worktree of a C2 target branch>   (read-only: 300 runs of the branch's added test/xds lifecycle test(s), nothing modified)
cd "$1" || exit 2
tests=$(git diff 4ee6ac46 HEAD -- 'test/xds/*_test.go' | sed -n 's/^+func (s) Test\([A-Za-z0-9_]*\)(.*/\1/p' | paste -sd'|')
re="^Test\$/^($tests)\$"
echo "### go test -race -count=150 -cpu 1,4 -v -run '$re' ./test/xds/   (unmodified branch $(git log --oneline -1 | cut -c1-8))"
out=$(go test -race -count=150 -cpu 1,4 -v -run "$re" ./test/xds/ 2>&1)
echo "subtest PASS lines: $(echo "$out" | grep -c '^    --- PASS')   subtest FAIL lines: $(echo "$out" | grep -c '^    --- FAIL')"
echo "$out" | grep -E '^(ok|FAIL)\s'
