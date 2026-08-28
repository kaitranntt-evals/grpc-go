#!/usr/bin/env bash
# Run: bash verify/repro/c10_compression_test_mutation.sh  (needs worktree of evalrepo branch evalon/grpc-go-se-2238ef47)
# Mutates the claim branch so the server can never compress responses, then shows
# TestUnaryRPCPipelineResponseCompression still passes.
set -euo pipefail
cd "$(git rev-parse --show-toplevel)"
git worktree add /tmp/c10-branch evalrepo/evalon/grpc-go-se-2238ef47 --detach -q 2>/dev/null || true
cd /tmp/c10-branch
python3 - <<'EOF'
old = """	} else if rc := stream.RecvCompress(); rc != "" && rc != encoding.Identity {
		// Legacy compressor not specified; attempt to respond with same encoding.
		ss.compressorV1 = encoding.GetCompressor(rc)
		if ss.compressorV1 != nil {
			ss.sendCompressorName = rc
		}
	}
"""
src = open('server.go').read()
assert old in src, "mutation target not found"
open('server.go', 'w').write(src.replace(old, "\t}\n"))
print("mutation applied: server never negotiates a response compressor")
EOF
(cd test && go test -v -run 'Test/UnaryRPCPipelineResponseCompression' -count=1 .)
git checkout -- server.go
