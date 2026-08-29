#!/usr/bin/env bash
# C8 repro: from the audited repo root (with the eval fixtures copied in place) run `bash verify/repro/c8_diagnostics_removal.sh` — removes the encoding-stage and compression-stage server diagnostics one at a time and shows every provided check group still passes.
set -euo pipefail
run_checks() {
  bash ./test/run_eval_server_test_group.sh compatibility
  bash ./test/run_eval_server_test_group.sh lifecycle
  go test ./encoding -run '^TestEval_' -count=1
  go test ./test -run '^TestEval_' -count=1
  go test . -run '^TestEval_' -count=1
}
cp stream.go stream.go.bak
trap 'mv stream.go.bak stream.go' EXIT
echo "== Part A: remove encoding diagnostic =="
sed -i 's#channelz.Error(logger, ss.channelz, "grpc: server failed to encode response: ", err)#_ = err#' stream.go
run_checks
cp stream.go.bak stream.go
echo "== Part B: remove compression diagnostic =="
sed -i 's#channelz.Error(logger, ss.channelz, "grpc: server failed to compress response: ", err)#_ = err#' stream.go
run_checks
echo "RESULT: all provided checks green with either stage-specific diagnostic removed"
