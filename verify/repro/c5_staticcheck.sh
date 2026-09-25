#!/usr/bin/env bash
# Run: verify/repro/c5_staticcheck.sh   (C5: staticcheck as configured by scripts/vet.sh on 5c408aa1; prints SA1019 ExitIdler findings surviving vet.sh's allowlist)
source "$(dirname "$0")/lib.sh"
wt=$(mkwt_branch 5c408aa1); cd "$wt"
export PATH="$(go env GOPATH)/bin:$PATH"; command -v staticcheck >/dev/null || (cd test/tools && go install honnef.co/go/tools/cmd/staticcheck)
staticcheck -version; grep 'honnef.co/go/tools' test/tools/go.mod
out=$(mktemp); staticcheck -checks 'all' ./balancer/ringhash/... >"$out" || true
allow=$(mktemp); awk '/noret_grep "\(SA1019\)" "\$\{SC_OUT\}" \| not grep -Fv/{on=1; sub(/.*grep -Fv .\x27?/,""); sub(/^\x27/,"")} on{ if ($0 ~ /\x27$/) {sub(/\x27$/,""); print; exit} print }' scripts/vet.sh >"$allow"
echo "allowlist entries: $(wc -l <"$allow"); entries mentioning ExitIdler: $(grep -c ExitIdler "$allow" || true)"
echo "SA1019 findings surviving the vet.sh allowlist:"; grep '(SA1019)' "$out" | grep -Fvf "$allow" || true
