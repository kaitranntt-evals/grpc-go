#!/usr/bin/env bash
# Repro for C2: mutate streamMethod on evalon/grpc-go-se-eedbaf01 so server-streaming RPCs skip the stream interceptor and bidi RPCs invoke it three times, then show TestUnifiedServerPipeline still passes (run: bash c2_mutation.sh <worktree-dir>).
set -euo pipefail
wt="${1:?usage: c2_mutation.sh <worktree-dir>}"
cd "$wt"

echo "== as-changed run (passes) =="
go test -v ./test -run 'Test/UnifiedServerPipeline' -count=1 || true

echo "== applying mutation =="
python3 - "$wt/server.go" <<'EOF'
import sys
p = sys.argv[1]
src = open(p).read()
old = """			if s.opts.streamInt == nil {
				return sd.Handler(srv, stream)
			}
			info := &StreamServerInfo{
				FullMethod:     stream.s.Method(),
				IsClientStream: sd.ClientStreams,
				IsServerStream: sd.ServerStreams,
			}
			return s.opts.streamInt(srv, stream, info, sd.Handler)"""
new = """			if s.opts.streamInt == nil {
				return sd.Handler(srv, stream)
			}
			if sd.ServerStreams && !sd.ClientStreams {
				return sd.Handler(srv, stream) // MUTATION: skip stream interceptor for server-streaming
			}
			info := &StreamServerInfo{
				FullMethod:     stream.s.Method(),
				IsClientStream: sd.ClientStreams,
				IsServerStream: sd.ServerStreams,
			}
			if sd.ServerStreams && sd.ClientStreams {
				// MUTATION: invoke the stream interceptor three times (nested) for bidi
				return s.opts.streamInt(srv, stream, info, func(srv any, ss ServerStream) error {
					return s.opts.streamInt(srv, ss, info, func(srv any, ss ServerStream) error {
						return s.opts.streamInt(srv, ss, info, sd.Handler)
					})
				})
			}
			return s.opts.streamInt(srv, stream, info, sd.Handler)"""
assert old in src, "streamMethod body not found"
open(p, "w").write(src.replace(old, new, 1))
EOF

echo "== mutated run (still passes: aggregate counters cannot detect it) =="
go test -v ./test -run 'Test/UnifiedServerPipeline' -count=1 || true

git checkout -- server.go
