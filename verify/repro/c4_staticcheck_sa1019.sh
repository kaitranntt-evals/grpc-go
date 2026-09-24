#!/bin/bash
# Run from a checkout of evalon/grpc-go-en-aa3a022f: `bash verify/repro/c4_staticcheck_sa1019.sh` — exits 1 (and prints the offending lines) if any SA1019 line in balancer/ringhash survives scripts/vet.sh's SA1019 allow-list.
set -u
export PATH="$PATH:$(go env GOPATH)/bin"
(cd test/tools && go install honnef.co/go/tools/cmd/staticcheck) || exit 2
echo "staticcheck: $(staticcheck -version)"
out=$(mktemp)
staticcheck -checks 'all' ./balancer/ringhash/... >"$out" 2>&1 || true
# Extract vet.sh's SA1019 allow-list (the `not grep -Fv '...'` block) and apply it exactly as vet.sh does.
allow=$(sed -n '/^  noret_grep "(SA1019)" "${SC_OUT}" | not grep -Fv /,/^XXXXX PleaseIgnoreUnused.$/p' scripts/vet.sh | sed '1s/.*grep -Fv .//' | sed '$s/.$//')
if grep "(SA1019)" "$out" | grep -Fv "$allow"; then
  echo "FAIL: unexcluded SA1019 diagnostics above would fail scripts/vet.sh"
  exit 1
fi
echo "OK: no unexcluded SA1019 diagnostics in balancer/ringhash"
