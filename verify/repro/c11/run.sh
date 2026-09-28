#!/bin/bash
# Run from the repo root (primary branch): bash verify/repro/c11/run.sh
set -u
D="$(cd "$(dirname "$0")" && pwd)"
cp "$D/c11_probe_test.go" internal/transport/c11_probe_test.go
trap 'rm -f internal/transport/c11_probe_test.go' EXIT
go test -tags c11probe -run '^TestC11Probe$' ./internal/transport -count=1 -v
