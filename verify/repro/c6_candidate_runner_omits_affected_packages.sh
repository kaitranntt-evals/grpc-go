#!/usr/bin/env bash
# Run (in a checkout of evalon/grpc-go-se-c075c936): bash verify/repro/c6_candidate_runner_omits_affected_packages.sh [path/to/eval_tests/tests]  -- with the fixture dir argument it also runs the real run_candidate_tests.sh
#
# C6: run_candidate_tests.sh derives candidate packages ONLY from changed
# *_test.go paths (git diff ... -- "*_test.go" "**/*_test.go"), so a change set
# that edits server.go/stream.go/rpc_util.go (root package) but authors its
# tests under test/ selects only ./test; the root, encoding and binarylog
# suites that exercise the changed code are never run.
set -euo pipefail

BASE_COMMIT="0c51461d27177d997e14c642fe18c11668fc09a3"   # value hard-coded in run_candidate_tests.sh
cd "$(git rev-parse --show-toplevel)"

echo "==> HEAD: $(git rev-parse --short HEAD)"
echo "==> production (non-test) files changed since BASE_COMMIT:"
git diff --name-only --diff-filter=ACMR "$BASE_COMMIT" HEAD | grep -v '_test\.go$' | sed 's/^/    /'

echo "==> test files the runner looks at (same git commands as run_candidate_tests.sh):"
changed_tests=$( {
  git diff --name-only --diff-filter=ACMR "$BASE_COMMIT" HEAD -- "*_test.go" "**/*_test.go"
  git diff --name-only --diff-filter=ACMR HEAD -- "*_test.go" "**/*_test.go"
  git ls-files --others --exclude-standard -- "*_test.go" "**/*_test.go"
} | sort -u )
echo "$changed_tests" | sed 's/^/    /'

echo "==> packages the runner would select (directory of each changed test file):"
selected=$(echo "$changed_tests" | xargs -n1 dirname | sort -u | sed 's#^\.$#.#; s#^\([^.]\)#./\1#')
echo "$selected" | sed 's/^/    /'

echo "==> affected packages with existing suites that are NOT selected:"
for pkg in . ./encoding ./binarylog ./test; do
  if ! echo "$selected" | grep -qx "$pkg"; then
    n=$(cat "$pkg"/*_test.go 2>/dev/null | grep -c '^func (s) Test\|^func Test' || true)
    echo "    $pkg  (omitted; $n test functions in package)"
  fi
done

if [ -n "${1:-}" ] && [ -f "$1/run_candidate_tests.sh" ]; then
  echo "==> running the real fixture runner from $1"
  mkdir -p .evaltools
  cp "$1/candidate_test_inventory.go" .evaltools/
  cp "$1/run_candidate_tests.sh" test/run_candidate_tests.sh
  trap 'rm -rf .evaltools test/run_candidate_tests.sh' EXIT
  bash ./test/run_candidate_tests.sh 2>&1 | grep -E '^==>|@package|^\[(PASS|FAIL)\]|^    ' || true
fi
