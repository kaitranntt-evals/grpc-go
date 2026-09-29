#!/usr/bin/env bash
# Run from a branch worktree root: bash <verify>/instrument/apply_trace.sh  (revert: git checkout credentials/xds/xds.go internal/credentials/xds/handshake_info.go internal/xds/balancer/clusterimpl/clusterimpl.go)
# Adds stderr trace lines: handshake start, each ClientSideTLSConfig attempt (with HandshakeInfo address), and each HandshakeInfo publication.
set -euo pipefail
python3 - <<'PY'
import re
def edit(p, anchor, ins, before=False, allow_many=False):
    s=open(p).read()
    n=s.count(anchor)
    assert n>=1 and (allow_many or n==1), (p, anchor, n)
    s=s.replace(anchor, (ins+anchor) if before else (anchor+ins))
    open(p,'w').write(s)
edit('credentials/xds/xds.go','hostname := xdsinternal.Hostname(chi.Attributes)\n','\tprintln("VERIFY-TRACE ClientHandshake START (new connection attempt)")\n')
edit('internal/credentials/xds/handshake_info.go','func (hi *HandshakeInfo) ClientSideTLSConfig(ctx context.Context, hostname string) (*tls.Config, error) {\n','\tprintln("VERIFY-TRACE ClientSideTLSConfig attempt hi=", hi)\n')
edit('internal/xds/balancer/clusterimpl/clusterimpl.go','b.xdsHIPtr.Store(','println("VERIFY-TRACE clusterimpl publishes new HandshakeInfo")\n\t',before=True,allow_many=True)
PY
go build ./credentials/xds ./internal/credentials/xds ./internal/xds/balancer/clusterimpl
