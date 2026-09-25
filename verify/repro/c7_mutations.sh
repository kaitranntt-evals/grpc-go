#!/usr/bin/env bash
# Run: verify/repro/c7_mutations.sh [commit, default 81201fc0]   (C7: targeted violations vs the maintained endpointsharding tests; each should FAIL a named test)
source "$(dirname "$0")/lib.sh"
wt=$(mkwt_ref "${1:-81201fc085e37de0ffb23505ac7831350b326c33}"); cd "$wt"
for n in samechild stalehandle configerror; do
  git checkout -q -- balancer/endpointsharding/endpointsharding.go; git apply "$REPRO_DIR/c7-$n.diff"
  echo "== mutation $n"
  go test -race -count=1 -timeout 60s -v ./balancer/endpointsharding/ 2>&1 | grep -E -- '--- FAIL: Test/|endpointsharding_test.go:[0-9]+:|^(ok|FAIL)\s' | head -6 || true
done
