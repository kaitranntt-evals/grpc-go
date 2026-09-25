#!/bin/bash
# Run (C7): verify/repro/c7_mutations.sh <checkout of evalon/grpc-go-en-7442a345>
# Applies two production mutations one at a time and runs the branch's full committed endpointsharding suite;
# both staying green shows no committed test asserts child config-error propagation or removed-child ExitIdle handling.
set -e
cd "$1"; f=balancer/endpointsharding/endpointsharding.go; cp $f /tmp/c7_orig.go
echo "== baseline"; go test ./balancer/endpointsharding -count=1 -race 2>&1 | tail -1
sed -i 's/^\t\t\tret = err$/\t\t\t_ = err \/\/ M1: child config errors dropped/' $f
echo "== M1 (child UpdateClientConnState errors never returned)"; git diff --stat; go test ./balancer/endpointsharding -count=1 -race 2>&1 | tail -1
cp /tmp/c7_orig.go $f
python3 - "$f" <<'PY'
import sys
p=sys.argv[1]; s=open(p).read()
old="\tif !bw.isClosed {\n\t\tbw.child.ExitIdle()\n\t}"
assert old in s
open(p,'w').write(s.replace(old,"\tbw.child.ExitIdle() // M2: closed-child guard removed"))
PY
echo "== M2 (ExitIdle on a retained handle reaches closed/removed child)"; git diff --stat; go test ./balancer/endpointsharding -count=1 -race 2>&1 | tail -1
cp /tmp/c7_orig.go $f
