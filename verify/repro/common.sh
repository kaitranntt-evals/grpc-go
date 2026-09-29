# Shared helpers for the verify/repro scripts. Sourced, not run directly.
# Requires: `go` 1.25+, the extracted eval fixtures in $EVAL_TESTS (default ~/eval_tests/tests),
# and network access to fetch the claim branch from the evalon repository.
set -euo pipefail
REPO_ROOT=$(git rev-parse --show-toplevel)
EVAL_TESTS="${EVAL_TESTS:-$HOME/eval_tests/tests}"
EVALREPO_URL="${EVALREPO_URL:-https://github.com/kaitranntt-evals/grpc-go-xds-certificate-provider-closure-race}"
BASE_COMMIT=cc234554fb363aea445a838b341bb8a65c8305b0
unset GOFLAGS GOTOOLCHAIN GOWORK GOCACHE GOENV

ensure_evalrepo() {
  git -C "$REPO_ROOT" remote get-url evalrepo >/dev/null 2>&1 || git -C "$REPO_ROOT" remote add evalrepo "$EVALREPO_URL"
  git -C "$REPO_ROOT" fetch -q evalrepo "$1"
}

# new_worktree <dir> <commit-ish>: creates a worktree and provisions the eval fixtures into it.
new_worktree() {
  git -C "$REPO_ROOT" worktree add -q --detach "$1" "$2"
  mkdir -p "$1/.evaltools"
  cp "$EVAL_TESTS/candidate_test_inventory.go" "$1/.evaltools/"
  cp "$EVAL_TESTS/eval_handshake_lifetime_test.go" "$1/internal/xds/balancer/clusterimpl/tests/"
  cp "$EVAL_TESTS/run_eval_xds_test_group.sh" "$1/test/"
}

cleanup_worktrees() {
  for w in "$@"; do git -C "$REPO_ROOT" worktree remove --force "$w" 2>/dev/null || true; done
}

# run_fixture <worktree>: runs the eval handshake-lifetime fixture, prints verdict + failure lines only.
run_fixture() {
  (cd "$1" && bash ./test/run_eval_xds_test_group.sh handshake-lifetime 2>&1 \
    | grep -E '"Action":"(pass|fail)","Package":"[^"]*","Test"|eval_handshake_lifetime_test.go:[0-9]+: [A-Za-z]' \
    | grep -vE 'snapshot cache|Discovery Service|serving at' \
    | sed -E 's/.*"Action":"(pass|fail)".*"Test":"([^"]*)".*/fixture \2: \1/; s/.*"Output":"    (eval_handshake_lifetime_test.go:[0-9]+: .*)\\n"\}$/    \1/') || true
}
