#!/usr/bin/env bash
# Run from the repo root: bash verify/repro/c1_replay.sh <fb96a820|6959f0d0|a1703b1b|ce9652b1> <dir holding the extracted eval_tests.zip "tests/" files>   (exit 0 = C1 reproduced on that branch)
#
# What it does, in a throwaway worktree of the claim branch:
#   1. provisions the eval fixtures byte-exact and lists/runs the branch's new-or-modified tests (run_candidate_tests.sh)
#   1b. runs the eval fixture eval_handshake_lifetime_test.go via run_eval_xds_test_group.sh (context only)
#   2. applies the env-gated instrumentation (apply_instrumentation.sh) and copies in the follow-up probe
#   3. probe, no mutation      -> must PASS (the branch itself behaves correctly)
#   4. probe, seeded defect    -> must FAIL (the defect really breaks "replacement roots govern the follow-up connection")
#   5. branch's tests, seeded defect -> all still PASS (they carry no evidence about the follow-up connection)
#   6. prints the per-attempt trace of every new test (one ClientHandshake attempt per test/subtest, or none)
set -uo pipefail
id="${1:?branch id}"; fixtures="$(cd "${2:?fixtures dir}" && pwd)"
BASE=cc234554fb363aea445a838b341bb8a65c8305b0
here="$(cd "$(dirname "$0")" && pwd)"
git remote get-url claims >/dev/null 2>&1 || git remote add claims "${CLAIMS_REMOTE:-https://github.com/kaitranntt-evals/grpc-go-xds-certificate-provider-closure-race}"
git fetch -q claims "evalon/grpc-go-xd-$id:refs/remotes/claims/evalon/grpc-go-xd-$id"
wt="$(mktemp -d)/wt-$id"
git worktree add -q --detach "$wt" "claims/evalon/grpc-go-xd-$id"
trap 'git worktree remove --force "$wt" >/dev/null 2>&1' EXIT
mkdir -p "$wt/.evaltools"
cp "$fixtures/candidate_test_inventory.go" "$wt/.evaltools/"
cp "$fixtures/run_candidate_tests.sh" "$fixtures/run_eval_xds_test_group.sh" "$wt/test/"
cd "$wt"
export CHANGED_TEST_FILES="$(git diff --name-only $BASE HEAD -- '*_test.go')"
summ() { grep -E '^(    |\[PASS\]|\[FAIL\])'; }
probe() { go test -count=1 -v -run '^TestVerify_FollowUpConnectionUsesReplacementRoots$' ./verify/repro/followup_probe/ 2>&1 | grep -E 'VERIFY-TRACE|followup_probe_test.go:(29|34)|^(---|ok|FAIL)' | cut -c1-300; return "${PIPESTATUS[0]}"; }

echo "### [$id] 1. new-or-modified tests, pristine branch"
bash ./test/run_candidate_tests.sh > /tmp/c1_$id.base.log 2>&1; base_rc=$?
summ < /tmp/c1_$id.base.log; echo "exit=$base_rc"

echo "### [$id] 1b. (context, not gating) eval fixture eval_handshake_lifetime_test.go, pristine branch"
cp "$fixtures/eval_handshake_lifetime_test.go" internal/xds/balancer/clusterimpl/tests/
bash ./test/run_eval_xds_test_group.sh handshake-lifetime > /tmp/c1_$id.fixture.log 2>&1; fx_rc=$?
rm -f internal/xds/balancer/clusterimpl/tests/eval_handshake_lifetime_test.go
grep -oE '(eval_handshake_lifetime_test\.go:[0-9]+: [^\\]*\\"[^}]*|--- (PASS|FAIL): TestEval[^\\]*)' /tmp/c1_$id.fixture.log | cut -c1-330; echo "exit=$fx_rc"

echo "### [$id] 2. apply instrumentation + probe"
bash "$here/apply_instrumentation.sh" "$wt" >/dev/null
mkdir -p verify/repro && cp -r "$here/followup_probe" verify/repro/

echo "### [$id] 3. probe, no mutation (expect PASS)"
VERIFY_TRACE=1 probe; probe_rc=$?; echo "exit=$probe_rc"

echo "### [$id] 4. probe, VERIFY_MUTATE=pin-first-roots (expect FAIL)"
VERIFY_TRACE=1 VERIFY_MUTATE=pin-first-roots probe; probe_mut_rc=$?; echo "exit=$probe_mut_rc"

echo "### [$id] 5. new-or-modified tests, VERIFY_MUTATE=pin-first-roots (C1 predicts: all still PASS)"
VERIFY_MUTATE=pin-first-roots bash ./test/run_candidate_tests.sh > /tmp/c1_$id.mut.log 2>&1; mut_rc=$?
grep -E '^\[(PASS|FAIL)\]' /tmp/c1_$id.mut.log; echo "exit=$mut_rc"

echo "### [$id] 6. per-attempt trace of the tests added on the branch (seeded defect active)"
new_tests=$(git diff -U0 $BASE HEAD -- '*_test.go' | sed -nE 's/^\+func \(s\) Test([A-Za-z0-9_]+)\(.*/\1/p' | paste -sd'|')
for pkg in $(for f in $CHANGED_TEST_FILES; do echo "./$(dirname "$f")"; done | sort -u); do
  VERIFY_TRACE=1 VERIFY_MUTATE=pin-first-roots go test -count=1 -v -run "^Test\$/^($new_tests)\$" "$pkg" 2>&1 | grep -E 'VERIFY-TRACE|^\s*(=== RUN|--- (PASS|FAIL))|^(ok|FAIL)' | cut -c1-220
done

if [ $base_rc -eq 0 ] && [ $probe_rc -eq 0 ] && [ $probe_mut_rc -ne 0 ] && [ $mut_rc -eq 0 ]; then
  echo "### [$id] C1 REPRODUCED: the seeded follow-up-roots defect is real (probe red) yet every new-or-modified test stays green"
  exit 0
fi
echo "### [$id] C1 NOT reproduced (base=$base_rc probe=$probe_rc probe_mut=$probe_mut_rc mutated_tests=$mut_rc)"
exit 1
