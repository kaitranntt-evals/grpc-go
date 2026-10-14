Observations only. Audited branch `grpc-go-xds-rds-interceptor-lifecycle-leak-perfect` at `614cb739` (repo `kaitranntt-evals/grpc-go`). Claim-target branches were fetched from `kaitranntt-evals/grpc-go-xds-rds-interceptor-lifecycle-leak` into separate worktrees: `evalon/grpc-go-xd-92fef2df` = `fc6a6130`, `evalon/grpc-go-xd-96f828ed` = `2ad598af`, `evalon/grpc-go-xd-11f88c29` = `f3e4860d`, `evalon/grpc-go-xd-2c0e5501` = `d8ebfe31`. Go 1.25.7 linux/amd64. The eval fixtures (byte-exact from `eval_tests.zip`) are stored as `verify/fixtures/*.txt`.

Common setup:

```sh
cd ~/repos/grpc-go
git remote add task https://github.com/kaitranntt-evals/grpc-go-xds-rds-interceptor-lifecycle-leak
for b in 92fef2df 96f828ed 11f88c29 2c0e5501; do
  git fetch task evalon/grpc-go-xd-$b
  git worktree add ~/wt/$b task/evalon/grpc-go-xd-$b
done
```

## C1

Branch `evalon/grpc-go-xd-92fef2df` (worktree `~/wt/92fef2df`).

Step 1, fixtures byte-exact:

```sh
cd ~/wt/92fef2df
cp ~/repos/grpc-go/verify/fixtures/eval_xds_server_interceptor_leak_test.go.txt test/xds/eval_xds_server_interceptor_leak_test.go
cp ~/repos/grpc-go/verify/fixtures/eval_rds_error_after_success_test.go.txt internal/xds/server/eval_rds_error_after_success_test.go
go test -race -v -count=1 -timeout=120s -run '^Test$/^Eval_ServerSideXDS_ReplacementRPCWhile' ./test/xds ./internal/xds/server
```

```console
--- PASS: Test (0.73s)
    --- PASS: Test/Eval_ServerSideXDS_ReplacementRPCWhileAllowRPCHeld (0.36s)
    --- PASS: Test/Eval_ServerSideXDS_ReplacementRPCWhileRetiredCloseBlocked (0.02s)
    --- PASS: Test/Eval_ServerSideXDS_ReplacementRPCWhileRetiredCloseBlocksAcceptedRPC (0.32s)
    --- PASS: Test/Eval_ServerSideXDS_ReplacementRPCWhileServerFilterCloseHeld (0.02s)
PASS
ok  	google.golang.org/grpc/test/xds	1.765s
=== RUN   Test/Eval_ServerSideXDS_ReplacementRPCWhileTwoChainsActive
--- FAIL: Test (0.00s)
    --- FAIL: Test/Eval_ServerSideXDS_ReplacementRPCWhileTwoChainsActive (0.00s)
panic: runtime error: invalid memory address or nil pointer dereference [recovered, repanicked]
[signal SIGSEGV: segmentation violation code=0x1 addr=0x0 pc=0x14672ec]
google.golang.org/grpc/internal/grpcsync.(*Event).HasFired(...)
	/home/ubuntu/wt/92fef2df/internal/grpcsync/event.go:52
google.golang.org/grpc/internal/xds/server.(*listenerWrapper).handleRDSUpdate(0xc0003e9e00, {0x185fd0c, 0x5}, {0xc000417e00?, {0x0?, 0x0?}})
	/home/ubuntu/wt/92fef2df/internal/xds/server/listener_wrapper.go:207 +0xac
google.golang.org/grpc/internal/xds/server.s.TestEval_ServerSideXDS_ReplacementRPCWhileTwoChainsActive({{}}, 0xc0000db6c0)
	/home/ubuntu/wt/92fef2df/internal/xds/server/eval_rds_error_after_success_test.go:361 +0x12fb
FAIL	google.golang.org/grpc/internal/xds/server	0.040s
```

The two-chain fixture panics at its very first `l.handleRDSUpdate` call (fixture line 361, the *initial* generation-1 install, before any RPC is held or any replacement is delivered). The fixture builds `&listenerWrapper{activeFilterChainManager: ..., httpFilters: ..., rdsHandler: &rdsHandler{}}` by struct literal, leaving `closed` nil; this branch's `handleRDSUpdate` begins with `if l.closed.HasFired()`. The production constructor `NewListenerWrapper` always sets `closed`. So this failure measures the hand-built struct, not replacement behaviour.

Step 2, same fixture with the one missing field supplied (only change):

```diff
@@ -20,6 +20,7 @@ package server
 import (
 	"sync"
+	"google.golang.org/grpc/internal/grpcsync"
 	"google.golang.org/grpc"
@@ -352,6 +353,7 @@ func (s) TestEval_ServerSideXDS_ReplacementRPCWhileTwoChainsActive(t *testing.T)
 		activeFilterChainManager: &filterChainManager{filterChains: []*filterChain{fcA, fcB}},
 		httpFilters:              make(map[serverFilterKey]*refCountedServerFilter),
 		rdsHandler:               &rdsHandler{},
+		closed:                   grpcsync.NewEvent(),
 	}
```

```sh
go test -race -v -count=1 -timeout=60s -run '^Test$/^Eval_ServerSideXDS_(ReplacementRPCWhile|ErrorUpdate)' ./internal/xds/server
```

```console
--- PASS: Test (1.02s)
    --- PASS: Test/Eval_ServerSideXDS_ErrorUpdateReleasesServerFilterReferences (0.02s)
    --- PASS: Test/Eval_ServerSideXDS_ReplacementRPCWhileTwoChainsActive (1.00s)
PASS
ok  	google.golang.org/grpc/internal/xds/server	2.061s
```

Step 3, the behaviour through the delivered constructor and update path (`NewListenerWrapper` + real xDS management server, one RDS resource shared by two filter chains, RPC held in `AllowRPC` on chain A's previous configuration, RDS replacement pushed by the management server). Test: `verify/instrumentation/c1_two_chain_real_wrapper_test.go.txt`.

```sh
cp ~/repos/grpc-go/verify/instrumentation/c1_two_chain_real_wrapper_test.go.txt internal/xds/server/verify_c1_test.go
go test -race -v -count=1 -run '^Test$/^VerifyC1_' ./internal/xds/server
```

```console
    verify_c1_test.go:224: baseline: chain A served by interceptor gen 1, chain B by gen 2
    verify_c1_test.go:237: old RPC is held inside AllowRPC of chain A gen 1
    verify_c1_test.go:266: after 21ms with old RPC STILL HELD: chain A replacement RPC served by gen 3, chain B replacement RPC served by gen 5
    verify_c1_test.go:274: old RPC released, returned err=<nil>
    verify_c1_test.go:279: interceptor generations closed so far: [1 2]
--- PASS: Test (0.04s)
    --- PASS: Test/VerifyC1_TwoChainReplacementViaRealListenerWrapper (0.04s)
PASS
ok  	google.golang.org/grpc/internal/xds/server	1.066s
```

(The replacement route config has two routes, so each chain builds two interceptors per update: chain A gets gens 3-4, chain B gens 5-6.) Replacement RPCs on both chains completed on the new generation 21ms after the update while the old RPC was still held; the 5s bound was not approached.

## C2

Branch `evalon/grpc-go-xd-96f828ed` (worktree `~/wt/96f828ed`). The authored tests with a closure-counter wait followed by a construction-count assertion:

- `internal/xds/server/listener_wrapper_test.go`, `TestListenerWrapper_RDSUpdates_ReleaseSupersededInterceptors`: `waitForCounter(ctx, t, "closed interceptors", &fb.interceptorsClosed, int32(i))` then `fb.interceptorsCreated.Load() == i+1`.
- same file, `TestListenerWrapper_LDSUpdate_ReleasesInterceptorsOfOldFilterChains`: `waitForCounter(... &fb.interceptorsClosed, 1)`, `waitForCounter(... &fb.filtersClosed, 1)` then `fb.interceptorsCreated.Load() == 2`.
- `test/xds/xds_server_rds_update_cleanup_test.go`, `TestServerSideXDS_RouteConfigUpdates_ReleaseSupersededInterceptors`: an RPC must first be served with `wantPath` (i.e. by the replacement interceptor), then `waitForInt32(ctx, t, "destroyed interceptors", &interceptorsDestroyed, int32(i))` then `interceptorsCreated.Load() == i+1`.

Production order on this branch: `handleRDSUpdate` calls `fc.constructUsableRouteConfiguration(...)` and then `fc.storeUsableRouteConfiguration(urc)`, which is `if old := fc.usableRouteConfiguration.Swap(urc); old != nil { old.close() }`; `maybeUpdateFilterChains` calls `l.instantiateFilterChainRoutingConfigurationsLocked()` and then `prevActive.stop()`. Both run on one goroutine under `l.mu`.

Observed with construction slowed by 300ms and every construct/close event traced (`verify/instrumentation/c2_slow_construction_trace.patch`, test files only):

```sh
cd ~/wt/96f828ed
git apply ~/repos/grpc-go/verify/instrumentation/c2_slow_construction_trace.patch
go test -race -count=5 -v -run '^Test$/^(ListenerWrapper_RDSUpdates_ReleaseSupersededInterceptors|ListenerWrapper_LDSUpdate_ReleasesInterceptorsOfOldFilterChains|ServerSideXDS_RouteConfigUpdates_ReleaseSupersededInterceptors)$' ./internal/xds/server ./test/xds > ~/c2_mut.log 2>&1
grep -E '^(ok|FAIL)|--- FAIL' ~/c2_mut.log
awk '/C2TRACE.*close/{match($0,/(closed|destroyed)=([0-9]+)/,a); match($0,/created=([0-9]+) at/,b); if (b[1]<a[2]) bad++; if (b[1]==a[2]) eq++; if (b[1]>a[2]) gt++} END{print "close events with created<closed:",bad+0,"; created==closed (shutdown close of last):",eq+0,"; created>closed (replacement already constructed):",gt+0}' ~/c2_mut.log
git checkout -- internal/xds/server/listener_wrapper_test.go test/xds/xds_server_filter_state_retention_test.go
```

```console
ok  	google.golang.org/grpc/internal/xds/server	13.608s
ok  	google.golang.org/grpc/test/xds	7.361s
close events with created<closed: 0 ; created==closed (shutdown close of last): 15 ; created>closed (replacement already constructed): 45
```

Trace excerpt (first RDS-update test iteration):

```console
=== RUN   Test/ListenerWrapper_RDSUpdates_ReleaseSupersededInterceptors
C2TRACE + 653ms construct-begin
C2TRACE + 954ms construct-end created=1
C2TRACE + 961ms construct-begin
C2TRACE +1262ms construct-end created=2
C2TRACE +1262ms close closed=1 (created=2 at this instant)
C2TRACE +1272ms construct-begin
C2TRACE +1573ms construct-end created=3
C2TRACE +1573ms close closed=2 (created=3 at this instant)
...
C2TRACE +2496ms close closed=5 (created=6 at this instant)
C2TRACE +2509ms close closed=6 (created=6 at this instant)
=== RUN   Test/ListenerWrapper_LDSUpdate_ReleasesInterceptorsOfOldFilterChains
C2TRACE +2528ms construct-begin
C2TRACE +2829ms construct-end created=1
C2TRACE +2832ms construct-begin
C2TRACE +3132ms construct-end created=2
C2TRACE +3132ms close closed=1 (created=2 at this instant)
C2TRACE +3138ms close closed=2 (created=2 at this instant)
```

In all 15 test executions (5 runs x 3 tests, 60 close events) the n-th closure was emitted only after `construct-end created=n+1`; no assertion fired early even with construction stretched to 300ms. The only closes with `created==closed` are the final shutdown closes, which no replacement-construction assertion follows.

## C3

Branch `evalon/grpc-go-xd-11f88c29` (worktree `~/wt/11f88c29`).

Fixture byte-exact does not compile on this branch:

```sh
cd ~/wt/11f88c29
cp ~/repos/grpc-go/verify/fixtures/eval_rds_error_after_success_test.go.txt internal/xds/server/eval_rds_error_after_success_test.go
go test -race -v -count=1 -run '^Test$/^Eval_ServerSideXDS_ErrorUpdateReleasesServerFilterReferences$' ./internal/xds/server
```

```console
# google.golang.org/grpc/internal/xds/server [google.golang.org/grpc/internal/xds/server.test]
internal/xds/server/eval_rds_error_after_success_test.go:345:30: cannot use &atomic.Pointer[usableRouteConfiguration]{} (value of type *"sync/atomic".Pointer[usableRouteConfiguration]) as *routingConfig value in struct literal
internal/xds/server/eval_rds_error_after_success_test.go:347:31: fc.usableRouteConfiguration.Store undefined (type *routingConfig has no field or method Store)
FAIL	google.golang.org/grpc/internal/xds/server [build failed]
```

Both errors are in the *other* test in the file (`TestEval_ServerSideXDS_ReplacementRPCWhileTwoChainsActive`, which hand-builds a `filterChain` with an `atomic.Pointer` field this branch replaced by `*routingConfig`). The build failure says nothing about error-update reference release.

Minimal modification: the fixture truncated just before `type evalC4Filter struct` (177 lines removed, all belonging to the two-chain test) and the five imports only that test used dropped; `TestEval_ServerSideXDS_ErrorUpdateReleasesServerFilterReferences` is untouched. Result stored as `verify/instrumentation/c3_fixture_trimmed_eval_rds_error_after_success_test.go.txt`.

```sh
cp ~/repos/grpc-go/verify/instrumentation/c3_fixture_trimmed_eval_rds_error_after_success_test.go.txt internal/xds/server/eval_rds_error_after_success_test.go
go test -race -v -count=1 -run '^Test$/^Eval_ServerSideXDS_ErrorUpdateReleasesServerFilterReferences$' ./internal/xds/server
```

```console
--- PASS: Test (0.08s)
    --- PASS: Test/Eval_ServerSideXDS_ErrorUpdateReleasesServerFilterReferences (0.08s)
PASS
ok  	google.golang.org/grpc/internal/xds/server	1.105s
```

Reference accounting printed directly (same test body plus `OBS` log lines, `verify/instrumentation/c3_error_update_ref_accounting_test.go.txt`; real `NewListenerWrapper`, two filter chains fc-1/route1 and fc-2/route2 sharing one server filter, successful config via the management server, then `handleRDSUpdate(routeN, rdsWatcherUpdate{err: ...})`):

```sh
cp ~/repos/grpc-go/verify/instrumentation/c3_error_update_ref_accounting_test.go.txt internal/xds/server/verify_c3_test.go
go test -race -v -count=1 -run '^Test$/^VerifyC3_' ./internal/xds/server
```

```console
    verify_c3_test.go:153: OBS after successful config: refCnt=3 interceptorsCreated=2 interceptorsClosed=0 filterDestroyed=0
    verify_c3_test.go:175: OBS after route1 RDS error: refCnt=2 interceptorsClosed=1 filterDestroyed=0
    verify_c3_test.go:193: OBS after route2 RDS error: refCnt=1 interceptorsClosed=2 filterDestroyed=0
    verify_c3_test.go:202: OBS after listener Close: refCnt=0 interceptorsClosed=2 filterDestroyed=1
--- PASS: Test (0.03s)
    --- PASS: Test/VerifyC3_ErrorUpdateRefAccounting (0.03s)
```

Each RDS error update synchronously closed the superseded configuration's interceptor and dropped exactly the one filter reference that configuration owned (3 -> 2 -> 1). The single remaining reference is the listener-level cache hold this branch adds in `getOrCreateServerFilterWithMap` (`serverFilter.incRef() // The map retains the filter across routing updates.`), not owned by any routing configuration; it is released on listener `Close` (refCnt 0, filter destroyed once).

## C4

Audited branch, `614cb739`. Relevant code (`internal/xds/server/filter_chain_manager.go`, `listener_wrapper.go`): `applyConfiguration` does `oldURC := fc.usableRouteConfiguration.Swap(urc)` and then, inline, `oldURC.stop()` (which calls every retired interceptor's `Close`); `handleRDSUpdate` takes `l.mu` and walks `l.activeFilterChainManager.filterChains` in order calling `fc.updateUsableRouteConfiguration(...)` for each chain using that route name.

Repro: `verify/repro/c4_cross_chain_publication_stall_test.go.txt`, run by `verify/repro/c4_repro.sh`. Part 1 calls `applyConfiguration` directly with an old config whose interceptor `Close` blocks. Part 2 uses the production path: `NewListenerWrapper`, a real xDS management server, a listener with two filter chains (`fc-A`, `fc-B`) both pointing at RDS resource `route1`; the test blocks `Close` of the earlier chain's first-generation interceptor, pushes a changed `route1` through the management server, and probes both chains with `RouteAndProcess` (same `connWrapper{urc: fc.usableRouteConfiguration}` wiring that `Accept` creates).

```sh
cd ~/repos/grpc-go
sh verify/repro/c4_repro.sh
```

```console
    verify_c4_test.go:139: oldURC.stop() is blocked inside old interceptor Close; new config already swapped in: true
    verify_c4_test.go:144: OBS: applyConfiguration has NOT returned after 2s while oldURC.stop() is blocked
    verify_c4_test.go:149: OBS: applyConfiguration returned only after the blocked Close was released
    verify_c4_test.go:263: baseline: earlier chain served by interceptor gen 1, later chain by gen 2
    verify_c4_test.go:280: earlier chain: Close of retired interceptor gen 1 is now blocked
    verify_c4_test.go:301: OBS after 3.004s with earlier-chain cleanup still blocked: earlier chain served by gen 3 (was 1), later chain served by gen 2 (was 2)
    verify_c4_test.go:310: OBS: listenerWrapper.mu NOT acquirable within 500ms while cleanup blocked (Accept path needs this lock)
    verify_c4_test.go:323: OBS 5ms after releasing earlier-chain cleanup: later chain served by gen 5
    verify_c4_test.go:326: RESULT: STALL REPRODUCED - later chain received its replacement only after earlier-chain cleanup was released
--- PASS: Test (6.54s)
    --- PASS: Test/VerifyC4_Part1_ApplyConfigurationWaitsForOldStop (2.00s)
    --- PASS: Test/VerifyC4_Part2_LaterChainPublicationStallsBehindEarlierChainCleanup (3.53s)
PASS
ok  	google.golang.org/grpc/internal/xds/server	7.572s
```

(The replacement route config has two routes, so the earlier chain builds gens 3-4 and the later chain gens 5-6.)

- Part "Synchronous retirement": `applyConfiguration` had not returned 2s after `oldURC.stop()` blocked, and returned once the block was released.
- Part "Cross-chain publication trigger": for the full 3.0s that the earlier chain's retired-interceptor `Close` was blocked, the earlier chain already served the replacement (gen 3) while the later chain kept serving the superseded generation (gen 2); 5ms after releasing the block the later chain served its replacement (gen 5).

Impact reasoning: during the stall, RPCs on connections matched to the later chain are still authorised by the superseded route configuration (state that looks healthy: RPCs succeed, just under old policy), for as long as any retired interceptor's `Close` on an earlier chain takes. `Close` is filter-implemented code, so its duration is not bounded by gRPC. The stall is held under `listenerWrapper.mu` (observed not acquirable), which `Accept` and `removeConn` also take, so new connections on the listener cannot be accepted for the same period. Trigger: any listener with two or more filter chains sharing an RDS route name, plus a filter whose interceptor `Close` is slow or blocks. With the built-in filters, whose `Close` is fast, the window is negligible. No workaround from xDS configuration; a filter author can only keep `Close` non-blocking.

## C5

Audited branch, `614cb739`. In one invocation: the C4 part-2 repro (which shows the cross-chain stall is present in this checkout) plus every eval fixture test, byte-exact.

```sh
cd ~/repos/grpc-go
sh verify/repro/c5_repro.sh
```

```console
    verify_c4_test.go:301: OBS after 3.002s with earlier-chain cleanup still blocked: earlier chain served by gen 3 (was 1), later chain served by gen 2 (was 2)
    verify_c4_test.go:310: OBS: listenerWrapper.mu NOT acquirable within 500ms while cleanup blocked (Accept path needs this lock)
    verify_c4_test.go:323: OBS 5ms after releasing earlier-chain cleanup: later chain served by gen 5
    verify_c4_test.go:326: RESULT: STALL REPRODUCED - later chain received its replacement only after earlier-chain cleanup was released
--- PASS: Test (4.54s)
    --- PASS: Test/Eval_ServerSideXDS_ErrorUpdateReleasesServerFilterReferences (0.01s)
    --- PASS: Test/Eval_ServerSideXDS_ReplacementRPCWhileTwoChainsActive (1.01s)
    --- PASS: Test/VerifyC4_Part2_LaterChainPublicationStallsBehindEarlierChainCleanup (3.52s)
PASS
ok  	google.golang.org/grpc/internal/xds/server	5.568s
--- PASS: Test (1.39s)
    --- PASS: Test/Eval_ServerSideXDS_InterceptorBeforeFilterCloseOrder (0.03s)
    --- PASS: Test/Eval_ServerSideXDS_InterceptorLeak_MultiGenerationRDSUpdate (0.04s)
    --- PASS: Test/Eval_ServerSideXDS_InterceptorLeak_RDSUpdate (0.03s)
    --- PASS: Test/Eval_ServerSideXDS_InterceptorSwapOrder (0.02s)
    --- PASS: Test/Eval_ServerSideXDS_PartialRouteFailure_ClosesInterceptors (0.03s)
    --- PASS: Test/Eval_ServerSideXDS_PartialVirtualHostFailure_ClosesInterceptors (0.03s)
    --- PASS: Test/Eval_ServerSideXDS_ReplacementRPCWhileAllowRPCHeld (0.34s)
    --- PASS: Test/Eval_ServerSideXDS_ReplacementRPCWhileRetiredCloseBlocked (0.01s)
    --- PASS: Test/Eval_ServerSideXDS_ReplacementRPCWhileRetiredCloseBlocksAcceptedRPC (0.32s)
    --- PASS: Test/Eval_ServerSideXDS_ReplacementRPCWhileServerFilterCloseHeld (0.02s)
    --- PASS: Test/Eval_ServerSideXDS_StopWithAllowRPCAwaitingContextCancel (0.01s)
    --- PASS: Test/Eval_ServerSideXDS_StopWithSupersededInterceptorCleanupBlocked (0.52s)
PASS
ok  	google.golang.org/grpc/test/xds	2.422s
--- Close implementation of the two-chain fixture interceptor:
298:func (i *evalC4Interceptor) Close() {}
```

Why the fixtures do not see it (assertion text):

- The only two-chain fixture, `TestEval_ServerSideXDS_ReplacementRPCWhileTwoChainsActive`, holds chain A in `AllowRPC` (`evalC4Interceptor.AllowRPC` waits on `release`) and its interceptor's `Close` is a no-op (`func (i *evalC4Interceptor) Close() {}`), so retired cleanup on the earlier chain never blocks; its bounded assertion (`STALL: chain B did not observe replacement configuration while chain A reader was held`) is only about a held reader.
- The blocked-cleanup fixtures (`ReplacementRPCWhileRetiredCloseBlocked`, `ReplacementRPCWhileRetiredCloseBlocksAcceptedRPC`, `ReplacementRPCWhileServerFilterCloseHeld`, `StopWithSupersededInterceptorCleanupBlocked`) all use `evalC2C3ServerListener`, a listener with exactly one filter chain (`FilterChains: []*v3listenerpb.FilterChain{{` at fixture line 1724), so there is no later chain to stall; on the single chain the swap precedes the blocked `Close`, so the replacement RPC is served.

Impact reasoning: the implementation under audit has the cross-chain stall (C4) and all 14 fixture tests are green in the same run, so a solution with this defect is assessed as passing; no fixture combines two active chains with a blocking retired-interceptor `Close`.

## C6

Branch `evalon/grpc-go-xd-2c0e5501` (worktree `~/wt/2c0e5501`). Authored tests: `internal/xds/server/route_config_lifecycle_test.go` (`TestFilterChain_RouteConfigUpdatesReleaseSupersededResources`, `TestFilterChain_SupersededRouteConfigReleasedAfterInFlightRPC`) and `test/xds/xds_server_rds_update_test.go` (`TestServerSideXDS_RouteConfigUpdatesReleaseSupersededInterceptors`). Their interceptors' `Close`:

```go
func (i *countingInterceptor) Close() {
	if i.closed.Swap(true) {
		return
	}
	i.parent.liveInterceptors.Add(-1)
}

func (i *liveInterceptor) Close() {
	if !i.closed.Swap(true) {
		i.parent.liveInterceptors.Add(-1)
	}
}
```

A second `Close` on the same instance is swallowed; the tests only assert on `liveInterceptors`.

```sh
cd ~/wt/2c0e5501
sh ~/repos/grpc-go/verify/repro/c6_repro.sh ~/repos/grpc-go/verify
```

Mutation A (`verify/repro/c6_double_close_mutation.patch`): in `closeVirtualHosts`, `r.interceptor.Close()` is issued twice (affects replacement and shutdown). Mutation B (`verify/repro/c6_double_close_rds_replacement_mutation.patch`): in `setUsableRouteConfiguration`, after `old.retire()`, `closeVirtualHosts(old.vhs)` is called again when no RPC is active (affects only supersession by a route update).

```console
== baseline, authored tests
--- PASS: Test (0.01s)
    --- PASS: Test/FilterChain_RouteConfigUpdatesReleaseSupersededResources (0.00s)
    --- PASS: Test/FilterChain_SupersededRouteConfigReleasedAfterInFlightRPC (0.00s)
PASS
ok  	google.golang.org/grpc/internal/xds/server	1.034s
--- PASS: Test (0.06s)
    --- PASS: Test/ServerSideXDS_RouteConfigUpdatesReleaseSupersededInterceptors (0.05s)
PASS
ok  	google.golang.org/grpc/test/xds	1.093s
== mutation A (every interceptor Close issued twice, replacement + shutdown), authored tests
--- PASS: Test (0.01s)
    --- PASS: Test/FilterChain_RouteConfigUpdatesReleaseSupersededResources (0.00s)
    --- PASS: Test/FilterChain_SupersededRouteConfigReleasedAfterInFlightRPC (0.00s)
PASS
ok  	google.golang.org/grpc/internal/xds/server	1.035s
--- PASS: Test (0.06s)
    --- PASS: Test/ServerSideXDS_RouteConfigUpdatesReleaseSupersededInterceptors (0.04s)
PASS
ok  	google.golang.org/grpc/test/xds	1.087s
== mutation A, pre-existing (not authored) retention tests
    xds_server_filter_state_retention_test.go:608: Destroyed 2 interceptor instances, want: 1
    xds_server_filter_state_retention_test.go:378: Destroyed 4 interceptor instances, want: 2
--- FAIL: Test (0.08s)
    --- FAIL: Test/ServerSideXDS_FilterStateRetention_AcrossUpdates_FilterChainsChange (0.04s)
    --- FAIL: Test/ServerSideXDS_FilterStateRetention_AcrossUpdates_FilterConfigChange (0.03s)
FAIL
FAIL	google.golang.org/grpc/test/xds	0.108s
FAIL
== mutation B (duplicate Close only when an RDS update supersedes a config), full packages + duplicate-close observer
ok  	google.golang.org/grpc/internal/xds/server	1.143s
ok  	google.golang.org/grpc/test/xds	14.144s
C6OBS: duplicate Close on interceptor 0xc00038c140 silently ignored by liveInterceptor.Close
C6OBS: duplicate Close on interceptor 0xc00038c360 silently ignored by liveInterceptor.Close
--- PASS: Test (0.06s)
    --- PASS: Test/ServerSideXDS_RouteConfigUpdatesReleaseSupersededInterceptors (0.05s)
PASS
ok  	google.golang.org/grpc/test/xds	1.084s
total C6OBS duplicate-close lines: 39
```

(The `C6OBS` lines come from `verify/instrumentation/c6_duplicate_close_observer.patch`, which only adds a print to the `else` branch of `liveInterceptor.Close`.)

Impact reasoning: all three authored tests stay green with every interceptor closed twice (mutation A). The pre-existing retention tests (unchanged by this branch; their `trackingInterceptor.Close` counts every call and they assert exact totals) do catch mutation A, but only because it also hits the LDS/shutdown paths they exercise. When the duplicate `Close` is confined to the path this change introduces, supersession by a route update (mutation B), the whole of `./internal/xds/server/...` and `./test/xds` passes under `-race` while 39 duplicate closes occur in the authored end-to-end test alone. A double-`Close` regression in the new RDS-replacement cleanup would therefore ship undetected; real interceptors whose `Close` is not idempotent (closing a channel, releasing a pooled resource) would panic or corrupt accounting.

## C7

Branch `evalon/grpc-go-xd-96f828ed` (worktree `~/wt/96f828ed`), `test/xds/xds_server_rds_update_cleanup_test.go`, `TestServerSideXDS_RouteConfigUpdates_ReleaseSupersededInterceptors`. Assertion text:

```go
	// Make an RPC and verify that it is processed with the initial
	// RouteConfiguration. Record the client address seen by the server, to
	// verify later that the same connection remains in use.
	client := testgrpc.NewTestServiceClient(cc)
	var initialPeer peer.Peer
	if _, err := client.EmptyCall(ctx, &testpb.Empty{}, grpc.Peer(&initialPeer)); err != nil {
...
		// The update must be applied on the existing connection, without
		// the connection being drained.
		if got, want := p.Addr.String(), initialPeer.Addr.String(); got != want {
			t.Fatalf("RPC after RouteConfiguration update %d used connection to %q, want the original connection to %q", i, got, want)
		}
```

`grpc.Peer` on a client call yields the remote (server listener) address, which is the same for every connection to that listener. No other assertion in the test concerns transport identity (`xds_server_interceptor_leak_test.go`, named in the claim, does not exist on this branch; this file is the nearest equivalent).

```sh
cd ~/wt/96f828ed
sh ~/repos/grpc-go/verify/repro/c7_repro.sh ~/repos/grpc-go/verify
```

Test mutation (`verify/repro/c7_force_reconnect_mutation.patch`): a `grpc.WithContextDialer` records each client transport to the listener, and before every RDS update the current transport is closed and the test waits for a new one. Production mutation (`verify/repro/c7_production_drain_mutation.patch`, test file untouched): `handleRDSUpdate` drains every accepted connection on each RDS update, i.e. exactly the regression the comment says the assertion guards against.

```console
== the assertion
251:		if got, want := p.Addr.String(), initialPeer.Addr.String(); got != want {
== test mutation: client transport forcibly closed before every RDS update
    xds_server_rds_update_cleanup_test.go:194: C7: client transport #1 dialed: local=127.0.0.1:39998 remote=127.0.0.1:45161
    xds_server_rds_update_cleanup_test.go:204: C7: forcibly closing client transport #1 (local=127.0.0.1:39998)
    xds_server_rds_update_cleanup_test.go:194: C7: client transport #2 dialed: local=127.0.0.1:40012 remote=127.0.0.1:45161
    xds_server_rds_update_cleanup_test.go:286: C7: continuity assertion operands after update 1: p.Addr="127.0.0.1:45161" initialPeer.Addr="127.0.0.1:45161" (server listener is "127.0.0.1:45161")
    xds_server_rds_update_cleanup_test.go:204: C7: forcibly closing client transport #2 (local=127.0.0.1:40012)
    xds_server_rds_update_cleanup_test.go:194: C7: client transport #3 dialed: local=127.0.0.1:40020 remote=127.0.0.1:45161
    xds_server_rds_update_cleanup_test.go:286: C7: continuity assertion operands after update 2: p.Addr="127.0.0.1:45161" initialPeer.Addr="127.0.0.1:45161" (server listener is "127.0.0.1:45161")
    xds_server_rds_update_cleanup_test.go:204: C7: forcibly closing client transport #3 (local=127.0.0.1:40020)
    xds_server_rds_update_cleanup_test.go:194: C7: client transport #4 dialed: local=127.0.0.1:40036 remote=127.0.0.1:45161
    xds_server_rds_update_cleanup_test.go:286: C7: continuity assertion operands after update 3: p.Addr="127.0.0.1:45161" initialPeer.Addr="127.0.0.1:45161" (server listener is "127.0.0.1:45161")
--- PASS: Test (0.70s)
    --- PASS: Test/ServerSideXDS_RouteConfigUpdates_ReleaseSupersededInterceptors (0.69s)
PASS
ok  	google.golang.org/grpc/test/xds	1.727s
== production mutation: handleRDSUpdate drains every accepted connection; test file untouched
C7MUT: RDS update for "server-route-config" draining 0 accepted connection(s)
C7MUT: RDS update for "server-route-config" draining 1 accepted connection(s)
C7MUT: RDS update for "server-route-config" draining 1 accepted connection(s)
C7MUT: RDS update for "server-route-config" draining 1 accepted connection(s)
--- PASS: Test (0.08s)
    --- PASS: Test/ServerSideXDS_RouteConfigUpdates_ReleaseSupersededInterceptors (0.07s)
PASS
ok  	google.golang.org/grpc/test/xds	1.107s
```

Impact reasoning: the compared value is the listener address on both sides (`127.0.0.1:45161` equals the server listener in every comparison), so the assertion is a tautology. It passed across four distinct client transports (local ports 39998, 40012, 40020, 40036), and it passed (3 of 3 additional `-count=3` runs during the audit as well) when production code was changed to drain the connection on every RDS update. The task requires updates "without dropping requests on active listeners"; this test reads as covering connection continuity but would not notice an implementation that drains connections on each RouteConfiguration update. The other checks in the test (RPCs keep succeeding, serving-mode callback fires once) do not observe drains either, because a graceful drain lets the client reconnect transparently.
