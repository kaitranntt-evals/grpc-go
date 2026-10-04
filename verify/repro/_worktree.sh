# Sourced by the c*_run.sh scripts (not run directly): creates a throwaway worktree $WT of claim branch $BR.
REPO=$(git rev-parse --show-toplevel)
VERIFY="$REPO/verify"
CLAIM_REPO=${CLAIM_REPO:-https://github.com/kaitranntt-evals/grpc-go-endpointsharding-decouple-locking}
TMP=$(mktemp -d)
WT="$TMP/wt"
git -C "$REPO" fetch -q "$CLAIM_REPO" "$BR"
git -C "$REPO" worktree add -q --detach "$WT" FETCH_HEAD
trap 'git -C "$REPO" worktree remove --force "$WT"; rm -rf "$TMP"' EXIT
cd "$WT"
echo "### worktree of $BR at $(git rev-parse HEAD)"
