#!/bin/bash
# Run from a checkout of branch evalon/grpc-go-xd-f9c33357: bash verify/repro/c3_staticcheck_sa4006.sh  (exits 1 and prints the SA4006 line when the problem is present)
set -uo pipefail
ROOT="$(git rev-parse --show-toplevel)"
BIN="$(mktemp -d)"
# Same install as `scripts/vet.sh -install`: version pinned by test/tools/go.mod (honnef.co/go/tools v0.7.0).
(cd "$ROOT/test/tools" && GOBIN="$BIN" go install honnef.co/go/tools/cmd/staticcheck) || exit 2
"$BIN/staticcheck" -version
SC_OUT="$(mktemp)"
# Same invocation as scripts/vet.sh line 121, narrowed to the package under audit.
(cd "$ROOT" && "$BIN/staticcheck" -checks 'all' ./internal/xds/server/... >"$SC_OUT")
echo "--- raw staticcheck output"; cat "$SC_OUT"
echo "--- after scripts/vet.sh line 124 exclusions (anything printed here fails vet.sh)"
if grep -v "(ST1000)" "$SC_OUT" | grep -v "(SA1019)" | grep -v "(ST1003)" | grep -v "(ST1019)\|\(other import of\)" | grep -v "(SA4000)"; then
  exit 1
fi
echo "(nothing)"
