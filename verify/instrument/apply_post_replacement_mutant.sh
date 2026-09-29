#!/usr/bin/env bash
# Run from a branch worktree root: bash <verify>/instrument/apply_post_replacement_mutant.sh  (revert: git checkout credentials/xds/xds.go && rm credentials/xds/verify_mutant.go)
set -euo pipefail
here="$(cd "$(dirname "$0")" && pwd)"
cp "$here/verify_mutant.go" credentials/xds/verify_mutant.go
python3 - <<'PY'
p='credentials/xds/xds.go'
s=open(p).read()
anchor='hostname := xdsinternal.Hostname(chi.Attributes)\n'
assert s.count(anchor)==1
s=s.replace(anchor, anchor+'\tif err := verifyPostReplacementStart(hiPtr, hiPtr.Load()); err != nil {\n\t\treturn nil, nil, err\n\t}\n')
open(p,'w').write(s)
PY
gofmt -l credentials/xds || true
go build ./credentials/xds
