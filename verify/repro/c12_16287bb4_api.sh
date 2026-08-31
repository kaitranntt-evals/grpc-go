#!/usr/bin/env bash
# Repro for C12: evalon/grpc-go-xd-16287bb4 exports certprovider.Retain, whose only
# production call site is internal xDS handshake lifetime management.
set -euo pipefail
# Run from a checkout of evalon/grpc-go-xd-16287bb4.
go doc google.golang.org/grpc/credentials/tls/certprovider Retain
grep -rn 'certprovider.Retain(' --include='*.go' . | grep -v _test.go
