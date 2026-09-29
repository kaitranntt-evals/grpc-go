#!/usr/bin/env bash
# Run from a branch worktree root (8069941c / d3a76037): removes provider cleanup from clusterImplBalancer.Close() (final-owner release). Revert: git checkout internal/xds/balancer/clusterimpl/clusterimpl.go
set -euo pipefail
python3 - <<'PY'
p='internal/xds/balancer/clusterimpl/clusterimpl.go'
s=open(p).read()
old='\tb.closeCachedProviders()\n\tb.logger.Infof("Shutdown")'
assert s.count(old)==1
s=s.replace(old,'\t// VERIFY-MUTANT: providers leaked on final release\n\tb.logger.Infof("Shutdown")')
open(p,'w').write(s)
PY
go build ./internal/xds/balancer/clusterimpl
