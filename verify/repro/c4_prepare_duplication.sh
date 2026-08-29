#!/usr/bin/env bash
# C4 repro: run `bash c4_prepare_duplication.sh <branch-checkout-dir>` — shows the server-only message-preparation helper duplicating the shared prepareMsg decision tree in stream.go.
set -euo pipefail
cd "$1"
echo "== shared prepareMsg =="
grep -n -A16 '^func prepareMsg' stream.go
echo "== server-only duplicate (serverStream method or SendMsg inline) =="
grep -n -B2 -A18 'func (ss \*serverStream) prepareMsg\|server failed to encode response' stream.go server.go 2>/dev/null | head -60
