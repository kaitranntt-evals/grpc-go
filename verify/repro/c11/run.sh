#!/bin/bash
# Run: verify/repro/c11/run.sh   (from anywhere inside the audited checkout; needs a clean internal/transport)
set -u
here=$(cd "$(dirname "$0")" && pwd); cd "$here/../../.." || exit 1
cp "$here/zz_c11_probe_test.go.txt" internal/transport/zz_c11_probe_test.go
go test google.golang.org/grpc/internal/transport -count=1 -v -run '^TestProbeC11_' 2>&1 | grep -vE '^=== |^\s*$'
rm -f internal/transport/zz_c11_probe_test.go; git status --short internal/
