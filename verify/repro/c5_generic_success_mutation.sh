#!/usr/bin/env bash
# C5 repro: run `bash c5_generic_success_mutation.sh <checkout-of-evalon/grpc-go-se-e5ed98e5>` — corrupts every stream response payload in the changed test's handlers; the tests still pass because stream assertions only require generic success.
set -euo pipefail
cd "$1"
f=test/server_unified_rpc_test.go
cp "$f" "$f.bak"
trap 'mv "$f.bak" "$f"' EXIT
sed -i 's/stream\.SendMsg(in)/stream.SendMsg(wrapperspb.Bytes([]byte("CORRUPTED")))/g' "$f"
go test ./test -run '^Test$/^ServerUnifiedRPCProcessing$' -v -count=1
