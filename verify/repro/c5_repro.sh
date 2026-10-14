#!/bin/sh
# Run from the repo root on the audited branch: sh verify/repro/c5_repro.sh   (C5: eval fixtures stay green while the C4 cross-chain stall is reproduced in the same run)
set -eu
cp verify/fixtures/eval_xds_server_interceptor_leak_test.go.txt test/xds/eval_xds_server_interceptor_leak_test.go
cp verify/fixtures/eval_rds_error_after_success_test.go.txt internal/xds/server/eval_rds_error_after_success_test.go
cp verify/repro/c4_cross_chain_publication_stall_test.go.txt internal/xds/server/verify_c4_test.go
trap 'rm -f test/xds/eval_xds_server_interceptor_leak_test.go internal/xds/server/eval_rds_error_after_success_test.go internal/xds/server/verify_c4_test.go' EXIT
go test -race -v -count=1 -timeout 300s -run '^Test$/^(VerifyC4_Part2|Eval_)' ./internal/xds/server ./test/xds 2>&1 |
  grep -E 'verify_c4_test.go:[0-9]+: (OBS|RESULT)|^(---|\s+---|ok|FAIL|PASS|panic)'
echo '--- Close implementation of the two-chain fixture interceptor:'
grep -n 'func (i \*evalC4Interceptor) Close' internal/xds/server/eval_rds_error_after_success_test.go
