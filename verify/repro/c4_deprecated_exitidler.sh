#!/bin/bash
# Run: verify/repro/c4_deprecated_exitidler.sh <worktree of evalon/grpc-go-en-940a22c3>   (needs staticcheck on PATH or in ~/work/bin: GOBIN=~/work/bin go install honnef.co/go/tools/cmd/staticcheck@latest)
set -uo pipefail
cd "$1"
export PATH="$PATH:$HOME/work/bin"
echo "### added lines referencing balancer.ExitIdler (eval check without the leading '!')"
git diff -U0 bf9e7cd3430df40d0732ba42eb88bd5f2cc63407 -- '*.go' ':(exclude)**/eval_*_test.go' | grep -nE '^\+[^+].*balancer\.ExitIdler'; echo "grep exit=$?"
echo "### declaration of balancer.ExitIdler"
grep -n -B3 "type ExitIdler" balancer/balancer.go
echo "### staticcheck SA1019 on the changed packages"
out="$(mktemp)"; allow="$(mktemp)"
staticcheck -checks 'SA1019' ./balancer/ringhash/ ./balancer/endpointsharding/ > "$out" 2>&1; cat "$out"
awk '/noret_grep "\(SA1019\)" "\$\{SC_OUT\}" \| not grep -Fv/{f=1; sub(/.*-Fv \x27/,""); print; next} f&&/PleaseIgnoreUnused\x27/{print "XXXXX PleaseIgnoreUnused"; f=0} f{print}' scripts/vet.sh > "$allow"
echo "--- SA1019 lines NOT covered by scripts/vet.sh allowlist (vet.sh fails if any):"
grep "(SA1019)" "$out" | grep -Fv -f "$allow"; echo "exit=$?"
