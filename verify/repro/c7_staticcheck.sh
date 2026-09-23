#!/bin/bash
# C7 repro: cd <worktree of evalon/grpc-go-en-212e3582> && PATH=$PATH:$(go env GOPATH)/bin bash verify/repro/c7_staticcheck.sh
# Runs the repo-pinned staticcheck (test/tools/go.mod: honnef.co/go/tools v0.7.0) with the exact scripts/vet.sh
# invocation (-checks 'all') on balancer/ringhash, then applies vet.sh's own SA1019 exclusion list. Any line that
# survives the filter is what makes the `not grep -Fv` stage of vet.sh fail.
set -u
cd "$(git rev-parse --show-toplevel)"
command -v staticcheck >/dev/null || (cd test/tools && go install honnef.co/go/tools/cmd/staticcheck)
echo "== staticcheck -version: $(staticcheck -version)"
echo "== pinned: $(grep honnef.co/go/tools test/tools/go.mod)"
SC_OUT=$(mktemp)
staticcheck -checks 'all' ./balancer/ringhash/... >"$SC_OUT" || true
echo "== raw SA1019 diagnostics in balancer/ringhash:"
grep "(SA1019)" "$SC_OUT"
# vet.sh's exclusion list (the quoted block after `noret_grep "(SA1019)" ... | not grep -Fv '`)
awk '/noret_grep "\(SA1019\)" "\$\{SC_OUT\}" \| not grep -Fv/{f=1; sub(/.*-Fv \x27/,""); print; next} f{ if ($0 ~ /\x27/) {sub(/\x27.*/,""); print; f=0} else print }' scripts/vet.sh >"$SC_OUT.excl"
echo "== SA1019 lines that survive scripts/vet.sh's exclusion filter (non-empty => vet.sh fails):"
grep "(SA1019)" "$SC_OUT" | grep -Fv -f "$SC_OUT.excl"
echo "== exit of vet.sh stage (0 = would fail, 1 = clean): $?"
rm -f "$SC_OUT" "$SC_OUT.excl"
