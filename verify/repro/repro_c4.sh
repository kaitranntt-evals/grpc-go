#!/usr/bin/env bash
# Repro for C4: run from a checkout of branch evalon/grpc-go-se-b1f3fdd3: bash verify/repro/repro_c4.sh
# The GCP observability binary-log test fails: the empty unary response is
# logged as Message: nil while the committed expectation is Message: []uint8{}.
set -euo pipefail
cd "$(git rev-parse --show-toplevel)/gcp/observability"
go test . -run 'Test/ServerRPCEventsLogAll' -count=1 -v
