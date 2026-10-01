#!/usr/bin/env bash
# Run: bash verify/repro/apply_instrumentation.sh <worktree-of-claim-branch>   (adds env-gated trace + seeded-defect hooks to credentials/xds; neutral unless VERIFY_TRACE / VERIFY_MUTATE are set)
set -euo pipefail
wt="${1:?usage: $0 <worktree>}"
here="$(cd "$(dirname "$0")" && pwd)"
f="$wt/credentials/xds/xds.go"
grep -q 'func (c \*credsImpl) ClientHandshake(' "$f"
grep -q '^	conn := tls.Client(rawConn, cfg)$' "$f"
grep -q 'hiPtr := xdsinternal.HandshakeInfoFromAttributes(chi.Attributes)' "$f"
sed -i 's/^func (c \*credsImpl) ClientHandshake(/func (c *credsImpl) clientHandshakeImpl(/' "$f"
sed -i 's/^	conn := tls.Client(rawConn, cfg)$/	cfg = verifyHookTLSConfig(ctx, hiPtr, cfg)\n	conn := tls.Client(rawConn, cfg)/' "$f"
cp "$here/instrument/verify_hook.go.txt" "$wt/credentials/xds/verify_hook.go"
gofmt -l "$wt/credentials/xds" || true
(cd "$wt" && git diff --stat -- credentials/xds/xds.go && go build ./credentials/xds/)
