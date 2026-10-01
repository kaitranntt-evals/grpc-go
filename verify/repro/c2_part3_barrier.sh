#!/bin/bash
# Run: verify/repro/c2_part3_barrier.sh     (targets evalon/grpc-go-xd-d5ce5e46)
# C2 "failure teardown synchronization" probe (injected failure). A t.Fatal is injected in
# TestServerSideXDS_RouteConfigurationUpdate_InFlightRPCKeepsInterceptor at the point where the server-side RPC is
# parked in blockingInterceptor.AllowRPC behind releaseCh (same position as the test's own t.Fatalf "Destroyed %d
# interceptor instances while an RPC is using one of them"). stopServer is additionally timed. File restored afterwards.
source "$(dirname "$0")/common.sh"
d=$(dir_of d5ce5e46); cd "$d" || exit 2
f=test/xds/xds_server_rds_update_test.go
cp "$f" "$OUT/c2p3.d5ce5e46.orig"
log="$OUT/c2p3.d5ce5e46.log"
perl -0pi -e 's/(\tenv\.pushRouteConfigUpdate\(ctx, t, 1\)\n\tenv\.waitForInterceptorCounts\(ctx, t, 2\*perConfig, perConfig-1\)\n)/$1\tt.Fatal("VERIFY-MUTATION: injected failure while the RPC is parked behind releaseCh")\n/; s/\tt\.Cleanup\(stopServer\)\n/\tt.Cleanup(func() {\n\t\tstart := time.Now() \/\/ VERIFY-MUTATION: timing only\n\t\tdone := make(chan struct{})\n\t\tgo func() { stopServer(); close(done) }()\n\t\tselect {\n\t\tcase <-done:\n\t\t\tt.Logf("VERIFY-TIMING: stopServer returned after %v", time.Since(start).Round(time.Millisecond))\n\t\tcase <-time.After(5 * time.Second):\n\t\t\tt.Logf("VERIFY-TIMING: stopServer STILL BLOCKED after 5s")\n\t\t}\n\t})\n/' "$f"
echo "mutation lines applied: $(grep -c VERIFY- "$f")" > "$log"
go test -count=1 -timeout 60s -v -run '^Test$/^ServerSideXDS_RouteConfigurationUpdate_InFlightRPCKeepsInterceptor$' ./test/xds >> "$log" 2>&1; echo "exit=$?" >> "$log"
cp "$OUT/c2p3.d5ce5e46.orig" "$f"
echo "worktree dirty files after restore: $(git status --short | grep -vc '^??')" >> "$log"
grep -E "^mutation lines|VERIFY-|Leaked goroutine|blockingInterceptor\)\.AllowRPC|xds_server_rds_update_test\.go:[0-9]+ \+|Goroutine leak check disabled|^\s*--- |^exit=|worktree dirty" "$log" | grep -v "tlogger.go"
