# Shared settings for the audit scripts (run v-1387f992). Source, do not execute.
# REPO = audited checkout, WT = directory holding one worktree per claim-target branch
# (WT/<short id>, e.g. WT/ee3cc2af for evalon/grpc-go-xd-ee3cc2af), OUT = where raw logs go.
REPO=${REPO:-$HOME/repos/grpc-go}
WT=${WT:-$HOME/wt}
OUT=${OUT:-/tmp/verify-out}
BASE=4ee6ac46fada69c06576cee108b009689a000520   # the task's base commit (startup.sh)
mkdir -p "$OUT"
# dir_of <name>: "audited" -> $REPO, anything else -> $WT/<name>
dir_of() { if [ "$1" = audited ]; then echo "$REPO"; else echo "$WT/$1"; fi; }
# added_tests <dir>: names (without the Test prefix) of the test/xds tests the branch added on top of BASE
added_tests() { git -C "$1" diff $BASE HEAD -- 'test/xds/*_test.go' | grep -E '^\+func \(s\) Test' | sed -E 's/^\+func \(s\) Test([A-Za-z0-9_]+).*/\1/' | paste -sd'|'; }
