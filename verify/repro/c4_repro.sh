#!/bin/sh
# Run from the repo root on the audited branch: sh verify/repro/c4_repro.sh   (C4: cross-chain publication stall behind blocked retired-interceptor cleanup)
set -eu
cp verify/repro/c4_cross_chain_publication_stall_test.go.txt internal/xds/server/verify_c4_test.go
trap 'rm -f internal/xds/server/verify_c4_test.go' EXIT
go test -race -v -count=1 -timeout 120s -run '^Test$/^VerifyC4_' ./internal/xds/server 2>&1 |
  grep -E 'verify_c4_test.go:[0-9]+: (OBS|RESULT|baseline|earlier|oldURC)|^(---|\s+---|ok|FAIL|PASS|panic)'
