#!/bin/bash
# Exact SA1019 exemption filter copied verbatim from scripts/vet.sh (branch evalon/grpc-go-en-5a85e94a).
# Usage: SC_OUT=<staticcheck output file> bash c6_vet_sa1019_filter.sh   (exit 0 = no non-exempt SA1019 diagnostics)
set -o pipefail
cd "$(dirname "$0")/../.." 2>/dev/null || true
not() { ! "$@"; }
noret_grep() { grep "$@" || true; }
: "${SC_OUT:?}"
  noret_grep "(SA1019)" "${SC_OUT}" | noret_grep "internal.Logger is deprecated:" | not grep -v -e "grpclog/logger.go"

  # Only ignore the following deprecated types/fields/functions and exclude
  # generated code.
  noret_grep "(SA1019)" "${SC_OUT}" | not grep -Fv 'XXXXX PleaseIgnoreUnused
XXXXX Protobuf related deprecation errors:
"github.com/golang/protobuf
.pb.go:
grpc_testing_not_regenerated
: ptypes.
proto.RegisterType
XXXXX gRPC internal usage deprecation errors:
"google.golang.org/grpc
: grpc.
: v1alpha.
: v1alphareflectionpb.
BalancerAttributes is deprecated:
CredsBundle is deprecated:
GetMetadata is deprecated:
internal.Logger is deprecated:
Metadata is deprecated: use Attributes instead.
NewAddress is deprecated:
NewSubConn is deprecated:
OverrideServerName is deprecated:
RemoveSubConn is deprecated:
SecurityVersion is deprecated:
.ServerName is deprecated:
stats.PickerUpdated is deprecated:
Target is deprecated: Use the Target field in the BuildOptions instead.
UpdateAddresses is deprecated:
UpdateSubConnState is deprecated:
balancer.ErrTransientFailure is deprecated:
grpc/reflection/v1alpha/reflection.proto
SwitchTo is deprecated:
XXXXX xDS deprecated fields we support
.ContainsMatch
.ExactMatch
.PrefixMatch
.SafeRegexMatch
.SuffixMatch
GetContainsMatch
GetExactMatch
GetMatchSubjectAltNames
GetPrefixMatch
GetSafeRegexMatch
GetSuffixMatch
GetSourceIp
GetTlsCertificateCertificateProviderInstance
GetValidationContextCertificateProviderInstance
XXXXX PleaseIgnoreUnused'
