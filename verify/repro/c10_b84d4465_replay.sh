#!/usr/bin/env bash
# Repro for C10: replaying the focused regression tests of evalon/grpc-go-xd-b84d4465
# against the pre-repair base (cc234554) stops on undefined newly introduced symbols
# (NewSharedProvider, AcquireHandshakeInfo) before any behavioral assertion executes.
set -euo pipefail
# $SOLUTION = checkout of evalon/grpc-go-xd-b84d4465; run from a checkout of cc234554.
cp "$SOLUTION"/internal/credentials/xds/handshake_info_acquire_test.go \
   "$SOLUTION"/internal/credentials/xds/provider_test.go \
   internal/credentials/xds/
go test ./internal/credentials/xds/ -run 'Test/(AcquireHandshakeInfo|SharedProvider)' -count=1  # build failed: undefined symbols
