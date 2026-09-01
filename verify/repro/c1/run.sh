#!/usr/bin/env bash
# Run: bash verify/repro/c1/run.sh <branch-id> [worktree-dir]
#   e.g. bash verify/repro/c1/run.sh a827a5ed ~/wt/a827a5ed
# Applies a 3-line test hook right after the first HandshakeInfo Load() in the
# handshake path of the given C1 target branch (checked out in <worktree-dir>,
# default ~/wt/<branch-id>), copies the C1 probe test files into
# credentials/xds/, runs them, and then reverts the instrumentation.
set -euo pipefail
id=$1
wt=${2:-$HOME/wt/$id}
here=$(cd "$(dirname "$0")" && pwd)

cd "$wt"
git -C "$wt" diff --quiet -- credentials/xds/xds.go internal/credentials/xds/handshake_info.go || {
	echo "worktree $wt has local changes to the instrumented files; refusing" >&2
	exit 1
}

case $id in
7d3bd828|09997670|08e1c163)
	file=internal/credentials/xds/handshake_info.go
	hook=C1TestHookAfterLoad
	typ='*HandshakeInfo'
	;;
*)
	file=credentials/xds/xds.go
	hook=c1TestHookAfterLoad
	typ='*xdsinternal.HandshakeInfo'
	;;
esac

# Insert the hook call after the FIRST line that loads the HandshakeInfo
# snapshot into `hi` (retry loads use a different variable, `newHI`).
awk -v hook="$hook" '
	!done && ($0 ~ /^\thi := hiPtr\.Load\(\)$/ || $0 ~ /^\t\thi = hiPtr\.Load\(\)$/ || $0 ~ /^\thi := ptr\.Load\(\)$/ || $0 ~ /^\t\thi := hiPtr\.Load\(\)$/ || $0 ~ /^\thi := xdsinternal\.HandshakeInfoFromAttributes\(chi\.Attributes\)\.Load\(\)$/) {
		print; match($0, /^\t+/); ind = substr($0, RSTART, RLENGTH)
		print ind "if h := " hook "; h != nil {"
		print ind "\th(hi)"
		print ind "}"
		done = 1; next
	}
	{ print }
	END { if (!done) { print "no load line matched" > "/dev/stderr"; exit 1 } }
' "$file" > "$file.c1tmp"
mv "$file.c1tmp" "$file"
printf '\n// %s is verification instrumentation (verify/repro/c1); nil in production.\nvar %s func(%s)\n' "$hook" "$hook" "$typ" >> "$file"
echo "== instrumentation applied to $file:"
git -C "$wt" --no-pager diff -- "$file"

cp "$here/common/c1_probe_common_test.go" credentials/xds/
cp "$here/branches/c1_probe_${id}_test.go" credentials/xds/

cleanup() {
	rm -f credentials/xds/c1_probe_common_test.go "credentials/xds/c1_probe_${id}_test.go"
	git -C "$wt" checkout -- "$file"
}
trap cleanup EXIT

echo "== go test -tags verifyrepro ./credentials/xds -run 'Test/C1Probe' -count=1 -v"
go test -tags verifyrepro ./credentials/xds -run 'Test/C1Probe' -count=1 -v 2>&1 | grep -E 'C1PROBE|=== RUN|--- (PASS|FAIL)|^(PASS|FAIL|ok)|panic|closed' || true
