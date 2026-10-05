#!/bin/bash
# Run from a checkout of evalon/grpc-go-tr-9e4507fd: bash <path>/verify/repro/c4/run_c4.sh   (applies the test-only patch, runs 4 modes, restores the tree)
set -u
HERE="$(cd "$(dirname "$0")" && pwd)"
git apply "$HERE/inject_early_failure.patch" || exit 1
cp "$HERE/zz_verify_c4_observer_test.go" internal/transport/
for mode in none after-connect header-forced header-real; do
  echo "=================== VERIFY_C4_INJECT=$mode"
  VERIFY_C4_INJECT=$mode go test -count=1 -v -run '^(Test|TestZZVerifyC4_Observer)$/^(ClientTransport_TinyDataFramesSlowReader)?$' ./internal/transport 2>&1 \
    | grep -vE '^\s*$' | grep -E 'VERIFY-C4|^\s*(---|ok|FAIL|PASS)|recv_buffer_test.go|Leaked goroutine|chan receive|serveTinyDataFrames|recv_buffer_test.go:[0-9]+ '
done
git checkout -- internal/transport/recv_buffer_test.go
rm -f internal/transport/zz_verify_c4_observer_test.go
