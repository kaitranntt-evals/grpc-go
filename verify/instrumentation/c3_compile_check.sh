#!/bin/bash
# Run: verify/instrumentation/c3_compile_check.sh <checkout of the branch under test> <path to eval_xds_server_interceptor_leak_test.go>   (C3: gofmt, vet, compile and run the branch's added/changed tests)
set -uo pipefail
cd "$1"; fixture="$2"
base=4ee6ac46fada69c06576cee108b009689a000520
echo "HEAD $(git rev-parse --short HEAD); $(grep -E '^go ' go.mod); $(go version)"
files=$(git diff --name-only $base HEAD -- '*_test.go' | paste -sd' ')
echo "added/changed test files: $files"
echo "\$ gofmt -l $files"; gofmt -l $files; echo "gofmt exit=$?"
echo "\$ go vet ./internal/xds/server/... ./test/xds/..."; go vet ./internal/xds/server/... ./test/xds/...; echo "vet exit=$?"
echo "\$ go test -race -count=1 -run '^\$' ./internal/xds/server/... ./test/xds/...   (compile only)"
go test -race -count=1 -run '^$' ./internal/xds/server/... ./test/xds/...; echo "compile exit=$?"
names=$(git diff $base HEAD -- '*_test.go' | grep -oE '^\+func \(s\) Test[A-Za-z0-9_]+' | sed 's/.*Test//' | sort -u | paste -sd'|')
echo "\$ go test -race -count=1 -v -run '^Test\$/^($names)\$' ./internal/xds/server ./test/xds | grep -E '^\s+--- |^(ok|FAIL)'"
go test -race -count=1 -v -run "^Test\$/^($names)\$" ./internal/xds/server ./test/xds 2>&1 | grep -E '^\s+--- (PASS|FAIL): Test/|^(ok|FAIL)'; echo "run exit=${PIPESTATUS[0]}"
echo "\$ cp <fixture> test/xds/eval_xds_server_interceptor_leak_test.go && cmp && go test -race -count=1 -run '^\$' ./test/xds"
cp "$fixture" test/xds/eval_xds_server_interceptor_leak_test.go && cmp "$fixture" test/xds/eval_xds_server_interceptor_leak_test.go && echo "cmp: identical"
go test -race -count=1 -run '^$' ./test/xds; echo "compile-with-fixture exit=$?"
rm -f test/xds/eval_xds_server_interceptor_leak_test.go
