#!/usr/bin/env bash
# Repro for C6: run from a checkout of branch evalon/grpc-go-se-c53e1aa0: bash verify/repro/repro_c6.sh
# Shows handleStream performing separate srv.methods / srv.streams lookups
# before invoking the common processRPC processor.
set -euo pipefail
cd "$(git rev-parse --show-toplevel)"
grep -n -A 3 'if md, ok := srv.methods\[method\]; ok' server.go
grep -n -A 3 'if sd, ok := srv.streams\[method\]; ok' server.go
grep -n 'func (s \*Server) processRPC' server.go
