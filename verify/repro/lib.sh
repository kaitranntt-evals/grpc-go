#!/usr/bin/env bash
# Helper sourced by the repro scripts in this directory (not run directly).
set -euo pipefail
EVALS_REPO=${EVALS_REPO:-https://github.com/kaitranntt-evals/grpc-go-endpointsharding-decouple-locking.git}
REPRO_DIR=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
REPO_ROOT=$(git -C "$REPRO_DIR" rev-parse --show-toplevel)
# mkwt_branch <suffix>: detached worktree of evalon/grpc-go-en-<suffix> from the evals repo.
mkwt_branch() {
  local dir="${TMPDIR:-/tmp}/verify-wt-$1-$$-$RANDOM"
  git -C "$REPO_ROOT" fetch -q "$EVALS_REPO" "+refs/heads/evalon/grpc-go-en-$1:refs/verify-tmp/$1"
  git -C "$REPO_ROOT" worktree add -q --detach "$dir" "refs/verify-tmp/$1"
  echo "$dir"
}
# mkwt_ref <commit>: detached worktree of a commit already present in this repository.
mkwt_ref() {
  local dir="${TMPDIR:-/tmp}/verify-wt-$1-$$-$RANDOM"
  git -C "$REPO_ROOT" worktree add -q --detach "$dir" "$1"
  echo "$dir"
}
# addgo <src> <dst>: copy an audit Go file, dropping its "//go:build ignore" guard.
addgo() { sed '/^\/\/go:build ignore$/d' "$1" > "$2"; }
