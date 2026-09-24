#!/usr/bin/env bash
# Run: bash verify/repro/c5_integration_gap.sh   (from a checkout of evalon/grpc-go-xd-b2906ada; lists added regressions and shows none uses a management server)
set -u
B=cc234554fb363aea445a838b341bb8a65c8305b0
echo "== added regressions"; git diff $B -- '*_test.go' | grep '^+func (s) Test'
echo "== management-server usages in added test code: $(git diff $B -- '*_test.go' | grep -c 'StartManagementServer\|mgmtServer')"
echo "== credential regression replaces via raw pointer swap:"; git diff $B -- credentials/xds/xds_client_test.go | grep -n 'hiPtr.Swap'
echo "== balancer regression provider KeyMaterial never blocks:"; git diff $B -- internal/xds/balancer/clusterimpl/balancer_test.go | sed -n '/func (p \*closeTrackingProvider) KeyMaterial/,/^+}/p'
go test ./credentials/xds/ ./internal/xds/balancer/clusterimpl/ -count=1 -v \
  -run 'Test/(ClientCredsProviderReplacedDuringHandshake|ClientCredsHandshakeInfoReleasedBeforeHandshake|SecurityConfigUpdateKeepsProvidersOpenForInProgressHandshake)$' 2>&1 | grep -E '^\s*--- |^ok|FAIL'
