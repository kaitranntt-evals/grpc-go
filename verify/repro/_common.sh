# Sourced by the c*.sh repro scripts (not run directly): prepares a scratch worktree of a claim-target branch.
# Usage: . verify/repro/_common.sh; claim_worktree <branch>   -> sets $WT and cds into it.
set -eu
REPO_ROOT=$(git rev-parse --show-toplevel)
REPRO="$REPO_ROOT/verify/repro"
# Repository that hosts the claim-target branches (override when going through a proxy/mirror).
CLAIMS_URL=${CLAIMS_URL:-https://github.com/kaitranntt-evals/grpc-go-transport-restrict-memory-overhead}
claim_worktree() {
	branch=$1
	WT=$(mktemp -d)/wt
	git -C "$REPO_ROOT" fetch -q "$CLAIMS_URL" "$branch"
	sha=$(git -C "$REPO_ROOT" rev-parse FETCH_HEAD)
	git -C "$REPO_ROOT" worktree add -q --detach "$WT" "$sha"
	echo "== worktree $WT = $branch @ $sha"
	cd "$WT"
}
run() {
	echo "\$ $*"
	"$@" || echo "(exit status $?)"
}
