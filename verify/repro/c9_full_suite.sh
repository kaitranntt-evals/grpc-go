#!/bin/bash
# Run: bash verify/repro/c9_full_suite.sh  (from the repo root; needs the eval fixture at /home/ubuntu/eval_tests/tests/)
# Runs the complete ./test/xds/... suite with -race -cpu 1,4 -timeout 7m, once with the eval fixture provisioned
# at test/xds/eval_xds_server_interceptor_leak_test.go (assessment layout) and once without it.
set -u
FIX=/home/ubuntu/eval_tests/tests/eval_xds_server_interceptor_leak_test.go
cp "$FIX" test/xds/eval_xds_server_interceptor_leak_test.go
echo "== with fixture, -cpu 1,4"; go test -race -cpu 1,4 -timeout 7m ./test/xds/... -count=1 2>&1 | grep -E '^(--- FAIL|panic:|ok|FAIL)'
echo "== with fixture, -cpu 4";   go test -race -cpu 4   -timeout 7m ./test/xds/... -count=1 2>&1 | grep -E '^(--- FAIL|panic:|ok|FAIL)'
rm test/xds/eval_xds_server_interceptor_leak_test.go
echo "== without fixture, -cpu 1,4"; go test -race -cpu 1,4 -timeout 7m ./test/xds/... -count=1 2>&1 | grep -E '^(--- FAIL|panic:|ok|FAIL)'
