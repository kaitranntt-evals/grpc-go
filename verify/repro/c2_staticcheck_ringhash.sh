#!/usr/bin/env bash
# C2 repro: staticcheck SA1019 diagnostics for the deprecated balancer.ExitIdler
# in ringhash, using the same invocation scripts/vet.sh uses
# (staticcheck -checks 'all') and vet.sh's own SA1019 exclusion list, which is
# read from scripts/vet.sh at run time so the two cannot drift apart.
#
# Run from a checkout of branch evalon/grpc-go-en-7cc18f1f of
# kaitranntt-evals/grpc-go-endpointsharding-decouple-locking:
#   bash verify/repro/c2_staticcheck_ringhash.sh
#
# Requires staticcheck on PATH (go install honnef.co/go/tools/cmd/staticcheck@latest).
set -uo pipefail

echo "== uses of balancer.ExitIdler in balancer/ringhash"
grep -n 'balancer\.ExitIdler' balancer/ringhash/*.go

echo "== staticcheck -checks 'all' ./balancer/ringhash (SA1019 lines only)"
SC_OUT="$(mktemp)"
staticcheck -checks 'all' ./balancer/ringhash >"${SC_OUT}" 2>&1 || true
grep '(SA1019)' "${SC_OUT}"

echo "== SA1019 lines that survive the exclusion list in scripts/vet.sh"
# The exclusion list is the quoted argument of the final
#   noret_grep "(SA1019)" "${SC_OUT}" | not grep -Fv '...'
# in scripts/vet.sh.
EXCL="$(mktemp)"
awk '/noret_grep "\(SA1019\)" "\$\{SC_OUT\}" \| not grep -Fv/ {flag=1; sub(/.*-Fv \x27/, ""); if ($0 ~ /\x27$/) {sub(/\x27$/, ""); print; flag=0} else print; next}
     flag && /\x27$/ {sub(/\x27$/, ""); print; flag=0; next}
     flag {print}' scripts/vet.sh >"${EXCL}"
echo "   (exclusion list: $(wc -l <"${EXCL}" | tr -d ' ') patterns from scripts/vet.sh)"
SURVIVORS="$(grep '(SA1019)' "${SC_OUT}" | grep -Fv -f "${EXCL}")"
echo "${SURVIVORS}"
rm -f "${SC_OUT}" "${EXCL}"

if [ -n "${SURVIVORS}" ]; then
  echo "RESULT: $(echo "${SURVIVORS}" | wc -l | tr -d ' ') non-excluded SA1019 diagnostic(s); vet.sh would fail on them"
  exit 1
fi
echo "RESULT: no non-excluded SA1019 diagnostics"
