#!/bin/bash
# Run: verify/repro/c9_bare_context.sh <checkout of evalon/grpc-go-xd-6cf08267>   (C9: prints the SetConnection call and runs the eval's bare-context check)
set -uo pipefail
cd "$1"
echo "\$ git grep -n 'SetConnection(' -- internal/xds/server/route_configuration_test.go"
git grep -n 'SetConnection(' -- internal/xds/server/route_configuration_test.go
echo "\$ ! git grep -e 'context.Background()' --or -e 'context.TODO()' -- 'internal/xds/server/*_test.go' 'test/xds/*_test.go' | grep -v 'context.WithTimeout(' | grep -v 'context.WithCancel(' | grep ."
! git grep -e 'context.Background()' --or -e 'context.TODO()' -- 'internal/xds/server/*_test.go' 'test/xds/*_test.go' | grep -v 'context.WithTimeout(' | grep -v 'context.WithCancel(' | grep .
echo "check exit=$? (0 = no bare context, 1 = bare context present)"
echo "\$ same check at the base commit 4ee6ac46"
! git grep -e 'context.Background()' --or -e 'context.TODO()' 4ee6ac46fada69c06576cee108b009689a000520 -- 'internal/xds/server/*_test.go' 'test/xds/*_test.go' | grep -v 'context.WithTimeout(' | grep -v 'context.WithCancel(' | grep .
echo "base check exit=$?"
echo "\$ git diff 4ee6ac46 HEAD --stat -- internal/xds/server/route_configuration_test.go"
git diff 4ee6ac46fada69c06576cee108b009689a000520 HEAD --stat -- internal/xds/server/route_configuration_test.go
echo "$ go test -race -count=1 -run '^Test$' ./internal/xds/server"
go test -race -count=1 -run '^Test$' ./internal/xds/server 2>&1 | tail -3
