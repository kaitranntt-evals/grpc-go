#!/usr/bin/env bash
# Repro for C3: on evalon/grpc-go-xd-70c131e8, the added e2e test still passes when the
# "replacement" cluster keeps the ORIGINAL trusted roots (no real root replacement),
# proving it cannot distinguish which roots governed the later connection.
set -euo pipefail
# Run from a checkout of evalon/grpc-go-xd-70c131e8.
go test ./test/xds/ -run 'Test/ClientSideXDS_SecurityConfigurationReplacement' -count=1 -v

# Mutation: replacement cluster uses the SAME trusted roots (line 628).
sed -i '628s/untrustedRootsInstance/trustedRootsInstance/' test/xds/xds_client_certificate_providers_test.go
go test ./test/xds/ -run 'Test/ClientSideXDS_SecurityConfigurationReplacement' -count=1 -v  # still PASSES
git checkout test/xds/xds_client_certificate_providers_test.go
