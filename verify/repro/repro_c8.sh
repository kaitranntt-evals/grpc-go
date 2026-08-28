#!/usr/bin/env bash
# Repro for C8: run from a checkout of branch evalon/grpc-go-se-7551960f: bash verify/repro/repro_c8.sh
# Shows serverStream.SendMsg reimplementing PreparedMsg handling, encode,
# compress, and msgHeader that prepareMsg in the same file already implements.
set -euo pipefail
cd "$(git rev-parse --show-toplevel)"
awk '/func \(ss \*serverStream\) SendMsg/,/^}/' stream.go | grep -n -E 'PreparedMsg|encode\(|compress\(|msgHeader\('
awk '/^func prepareMsg/,/^}/' stream.go | grep -n -E 'PreparedMsg|encode\(|compress\(|msgHeader\('
