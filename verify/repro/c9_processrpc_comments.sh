# Run: bash c9_processrpc_comments.sh <path-to-worktree-of evalon/grpc-go-se-f6c7d66c> — prints every comment/defer inside processRPC; only stack-size context appears.
set -euo pipefail
cd "$1"
start=$(grep -n 'func (s \*Server) processRPC' server.go | cut -d: -f1)
end=$((start + 200))
sed -n "${start},${end}p" server.go | grep -n 'defer\|//' | grep -i 'defer\|order\|panic\|reverse\|stack' || true
