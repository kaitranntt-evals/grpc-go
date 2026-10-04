#!/bin/sh
# Run: sh verify/repro/c5_gofmt.sh   (from a grpc-go checkout of the verify branch; needs go and network access to the claim repo)
# C5: non-writing gofmt checks on the changed Go files of evalon/grpc-go-en-66daeaed, using the scripts/vet.sh invocation.
set -u
BR=evalon/grpc-go-en-66daeaed
. "$(dirname "$0")/_worktree.sh"
BASE=bf9e7cd3430df40d0732ba42eb88bd5f2cc63407
echo "### scripts/vet.sh formatting invocation"; grep -n 'gofmt' scripts/vet.sh
echo "### changed Go files"; git diff --name-only $BASE HEAD -- '*.go'
echo "### gofmt -s -d -l <changed files>"; gofmt -s -d -l $(git diff --name-only $BASE HEAD -- '*.go'); echo "exit=$? output_bytes=$(gofmt -s -d -l $(git diff --name-only $BASE HEAD -- '*.go') 2>&1 | wc -c)"
echo "### gofmt -l balancer/endpointsharding balancer/ringhash"; gofmt -l balancer/endpointsharding balancer/ringhash; echo "exit=$? output_bytes=$(gofmt -l balancer/endpointsharding balancer/ringhash 2>&1 | wc -c)"
echo "### gofmt -d balancer/endpointsharding/endpointsharding_ext_test.go"; gofmt -d balancer/endpointsharding/endpointsharding_ext_test.go; echo "exit=$? output_bytes=$(gofmt -d balancer/endpointsharding/endpointsharding_ext_test.go 2>&1 | wc -c)"
echo "### gofmt -s -d -l .   (exactly as scripts/vet.sh, whole repo)"; gofmt -s -d -l . 2>&1 | head; echo "output_bytes=$(gofmt -s -d -l . 2>&1 | wc -c)"
echo "### testChild declarations (tabs shown as ^I)"; sed -n 357,370p balancer/endpointsharding/endpointsharding_ext_test.go | cat -A | sed 's/\$$//'
echo "### sensitivity check: the same command does flag a deliberately misformatted copy"
sed 's/^func (c \*testChild) Close() \+{}/func (c *testChild) Close() {}/' balancer/endpointsharding/endpointsharding_ext_test.go > "$TMP/misformatted_test.go"; gofmt -s -l "$TMP/misformatted_test.go" | sed "s|$TMP/||"
