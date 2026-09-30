#!/usr/bin/env bash
# Usage: verify/repro/c6_overlap_test_accepts_invalidation.sh   (branch 5175e7e6; needs FIXTURES_DIR, see setup_worktree.sh)
# C6 repro. (1) Runs TestClientCredsProviderReplacedDuringHandshake as written with load probes: the original
# selected load ENDs with "provider instance is closed", a second load on the replacement succeeds, test PASSes.
# (2) Removes the test's oldRoot.Close() (the selected provider is no longer invalidated): the test FAILS with
# a timeout, i.e. it cannot pass unless the original load is invalidated. The test file is restored afterwards.
set -uo pipefail
here=$(cd "$(dirname "$0")" && pwd)
wt=$("$here/setup_worktree.sh" 5175e7e6) || exit 1
sel='^Test$/^ClientCredsProviderReplacedDuringHandshake$'
echo "##### as written"; "$here/../probe_attempts.sh" "$wt" credentials/xds "$sel"
echo "##### test mutation: oldRoot.Close() removed"
cd "$wt"; f=credentials/xds/xds_client_test.go
grep -n '^	oldRoot.Close()$' $f
sed -i 's/^\toldRoot.Close()$/\t\/\/ VERIFY mutation: oldRoot.Close() removed/' $f
GOENV=off GOWORK=off go test ./credentials/xds -run "$sel" -count=1 -v -timeout 60s 2>&1 | grep -E '^\s*(--- |PASS|FAIL|ok )|_test.go:[0-9]+: '
git checkout -q -- $f
