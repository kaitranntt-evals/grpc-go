#!/bin/bash
# Run: ./c3_staticcheck.sh <worktree of evalon/grpc-go-en-00a0b773>   (installs staticcheck pinned by test/tools/go.mod, runs the scripts/vet.sh invocation on balancer/ringhash, applies vet.sh's SA1019 allowlist)
set -uo pipefail
wt="$1"
cd "$wt"
(cd test/tools && go install honnef.co/go/tools/cmd/staticcheck) || exit 1
SC=$(go env GOPATH)/bin/staticcheck
"$SC" --version
echo "### grep -n 'balancer.ExitIdler' balancer/ringhash/ringhash.go"
grep -n 'balancer.ExitIdler' balancer/ringhash/ringhash.go
echo "### $SC -checks 'all' ./balancer/ringhash/...   (vet.sh runs: staticcheck -checks 'all' ./...)"
out=$("$SC" -checks 'all' ./balancer/ringhash/... 2>&1)
echo "$out" | grep "(SA1019)"
echo "### after vet.sh's SA1019 allowlist (the 'not grep -Fv' list in scripts/vet.sh):"
allow=$(awk "/not grep -Fv 'XXXXX PleaseIgnoreUnused/{f=1; sub(/.*not grep -Fv '/,\"\"); print; next} f&&/XXXXX PleaseIgnoreUnused'/{sub(/'.*/,\"\"); print; f=0; next} f{print}" scripts/vet.sh)
remaining=$(echo "$out" | grep "(SA1019)" | grep -Fv "$allow")
echo "$remaining"
if [ -n "$remaining" ]; then echo "RESULT: unallowed SA1019 diagnostics remain -> scripts/vet.sh would fail"; else echo "RESULT: no unallowed SA1019 diagnostics"; fi
