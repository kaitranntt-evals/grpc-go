# Sourced by the run.sh scripts: prepares a clean detached worktree of a claim-target branch (usage: prepare <branch-suffix>).
set -euo pipefail
REPO_ROOT="$(git -C "$(dirname "${BASH_SOURCE[0]}")" rev-parse --show-toplevel)"
CLAIMS_URL="${CLAIMS_URL:-https://github.com/kaitranntt-evals/grpc-go-endpointsharding-decouple-locking}"
prepare() {
  local suffix="$1" branch="evalon/grpc-go-en-$1" wt
  wt="${WT_ROOT:-$HOME/verify-wt}/$suffix"
  # A dedicated local ref per branch (not FETCH_HEAD) keeps concurrent runs from racing.
  git -C "$REPO_ROOT" fetch -q "$CLAIMS_URL" "+$branch:refs/verify-probe/$suffix"
  local sha; sha="$(git -C "$REPO_ROOT" rev-parse "refs/verify-probe/$suffix")"
  if [ -d "$wt" ]; then git -C "$wt" checkout -q -f --detach "$sha"; else git -C "$REPO_ROOT" worktree add -q --detach "$wt" "$sha"; fi
  git -C "$wt" reset -q --hard "$sha"
  echo "$wt"
}
