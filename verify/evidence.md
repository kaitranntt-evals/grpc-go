## Setup shared by all sections

Claim branches live in a second repository; each was fetched into its own worktree. Go 1.25.7, linux/amd64. Repro files under `verify/repro/` are copied into the claim branch's package directory to run.

```sh
cd ~/repos/grpc-go
git fetch origin grpc-go-xds-rds-interceptor-lifecycle-leak-perfect
git checkout -b verify/grpc-go-xds-rds-interceptor-lifecycle-leak-v-a1bfaf10 origin/grpc-go-xds-rds-interceptor-lifecycle-leak-perfect
git remote add claims https://github.com/kaitranntt-evals/grpc-go-xds-rds-interceptor-lifecycle-leak
for b in 03f6ebcd 576de4cd 3100ee45 e02f4bfe aa7e5dee; do
  git fetch claims evalon/grpc-go-xd-$b:refs/remotes/claims/evalon/grpc-go-xd-$b
  git worktree add ~/wt/$b claims/evalon/grpc-go-xd-$b
done
git worktree add ~/wt/base 4ee6ac46   # task base commit, used as a comparison in C2
```

Commits audited: C1 `28738564`, C2 `701a1fe7`, C3 `3d553ac8`, C4 `8b956c41`, C5 `c4d988df`.

## C1

Branch: [evalon/grpc-go-xd-03f6ebcd](https://github.com/kaitranntt-evals/grpc-go-xds-rds-interceptor-lifecycle-leak/tree/evalon/grpc-go-xd-03f6ebcd). Verdict: CONFIRMED (both parts).

Code path on the branch (`internal/xds/server/routing.go`, `filter_chain_manager.go`): `RouteAndProcess` takes a reference with `acquireUsableRouteConfiguration` and `defer rc.release()`; `release()` calls `rc.cleanup()` inline when it drops the last reference of a retired configuration; `cleanup()` calls every interceptor's `Close()` and then every server filter's `Close()`. `xdsUnaryInterceptor` calls `handler` only after `RouteAndProcess` returns.

Repro: `verify/repro/c1_retired_close_blocks_accepted_rpc_test.go`. A real xDS server (`xds.NewGRPCServer`, management server, xDS client channel). The test independently controls `AllowRPC` (held for the path1 interceptor) and the retired interceptor's `Close` (held or not). A second unary interceptor chained after `xdsUnaryInterceptor` records when the handler side of `xdsUnaryInterceptor` is entered for the RPC tagged `rpc-id: held`. Sequence: (1) held RPC enters `AllowRPC(path1)`; (2) RouteConfiguration replaced (path2) and proven published by a probe RPC served by path2, with `Close(path1)` not yet called; (3) `AllowRPC(path1)` returns nil.

```sh
cp verify/repro/c1_retired_close_blocks_accepted_rpc_test.go ~/wt/03f6ebcd/test/xds/
cd ~/wt/03f6ebcd
go test -race -v -count=1 -run '^Test$/^VerifyC1_' ./test/xds > /tmp/c1.log 2>&1
grep -nE "^\s*(--- |PASS|FAIL|ok )|OBSERVED|STALL|step |^\s+(goroutine|google\.golang\.org)" /tmp/c1.log
```

Key output (3 consecutive runs gave the same result):

```console
    c1_retired_close_blocks_accepted_rpc_test.go:232: step 1: held RPC is inside AllowRPC(path1)
    c1_retired_close_blocks_accepted_rpc_test.go:260: step 2: replacement (path2) is serving new RPCs; Close(path1) not yet called; held RPC still in AllowRPC
    c1_retired_close_blocks_accepted_rpc_test.go:264: step 3: AllowRPC(path1) released (returns nil => RPC accepted)
    c1_retired_close_blocks_accepted_rpc_test.go:278: Close(path1) called from:
          goroutine 87 [running]:
          google.golang.org/grpc/test/xds_test.vc1Run.func4({0xc0002d831b, 0x5})
          google.golang.org/grpc/test/xds_test.(*vc1Interceptor).Close(0xc00023ed50)
          google.golang.org/grpc/internal/xds/server.(*interceptorList).Close(0xc00023ede0)
          google.golang.org/grpc/internal/xds/server.(*usableRouteConfiguration).cleanup(0xc000512060)
          google.golang.org/grpc/internal/xds/server.(*usableRouteConfiguration).release(0xc000512060)
          google.golang.org/grpc/internal/xds/server.RouteAndProcess({0x1f99b28, 0xc000980210})
          google.golang.org/grpc/xds.xdsUnaryInterceptor({0x1f99b28, 0xc000980210}, {0x1bc4320, 0xc000980240}, 0xc00061aa40?, 0xc0004fe640)
          google.golang.org/grpc.NewServer.chainUnaryServerInterceptors.chainUnaryInterceptors.func1(...)
          google.golang.org/grpc/interop/grpc_testing._TestService_EmptyCall_Handler(...)
          google.golang.org/grpc.(*Server).processUnaryRPC(...)
    c1_retired_close_blocks_accepted_rpc_test.go:280: OBSERVED Close(path1) runs synchronously under RouteAndProcess/xdsUnaryInterceptor: true
    c1_retired_close_blocks_accepted_rpc_test.go:302: OBSERVED blockClose=true: within 3s of AllowRPC returning: handlerEntered=false rpcCompleted=false
    c1_retired_close_blocks_accepted_rpc_test.go:315: OBSERVED after releasing Close(path1): handlerEntered=true rpcCompleted=true err=<nil>
    c1_retired_close_blocks_accepted_rpc_test.go:320: STALL: accepted RPC did not enter its handler for 3s after AllowRPC returned, while retired interceptor Close was blocked
    ...
    c1_retired_close_blocks_accepted_rpc_test.go:302: OBSERVED blockClose=false: within 3s of AllowRPC returning: handlerEntered=true rpcCompleted=true
    --- FAIL: Test/VerifyC1_RetiredCloseBlocked (3.37s)
    --- PASS: Test/VerifyC1_RetiredCloseNotBlocked (0.41s)
```

Per part:

- *Cleanup mechanism* — holds. The stack shows `Close` of the retired interceptor running on the RPC's own goroutine under `RouteAndProcess -> release -> cleanup`, below `xdsUnaryInterceptor`. With Close held the RPC neither entered the handler nor completed for 3s; it did both immediately once Close was released, so the blocked Close was the only thing holding it.
- *RPC trigger* — holds. `Close(path1)` was not called while the RPC sat in `AllowRPC` after the replacement was live (step 2); it was called only after `AllowRPC` returned, by that RPC's release, before the handler was invoked.

The eval fixtures do not catch this. With `tests/eval_xds_server_interceptor_leak_test.go` copied to `test/xds/` on this branch:

```sh
cp ~/eval_tests/tests/eval_xds_server_interceptor_leak_test.go ~/wt/03f6ebcd/test/xds/
cd ~/wt/03f6ebcd && go test -race -v -count=1 -run '^Test$/^Eval_ServerSideXDS_ReplacementRPCWhile' ./test/xds
```

```console
    --- PASS: Test/Eval_ServerSideXDS_ReplacementRPCWhileAllowRPCHeld (0.35s)
    --- PASS: Test/Eval_ServerSideXDS_ReplacementRPCWhileRetiredCloseBlocked (0.02s)
    --- PASS: Test/Eval_ServerSideXDS_ReplacementRPCWhileServerFilterCloseHeld (0.02s)
ok  	google.golang.org/grpc/test/xds	1.419s
```

Impact reasoning: the trigger is an RDS update arriving while any RPC is between route lookup and the end of `AllowRPC` on the old configuration, which is routine on a busy server. The last such RPC then pays for the entire retired configuration's teardown (every route's interceptor Close plus the server filters' Close) inline, before its handler runs. With fast Close implementations this is a small latency blip on one RPC; with a filter whose Close waits on anything (flushing, joining a goroutine, a lock) an already-authorised request stalls for that long, up to its deadline. New RPCs on the replacement configuration are not affected (probe RPCs succeeded while Close was held). The same deferred release sits in the streaming path (`xdsStreamInterceptor` also calls `RouteAndProcess`); only the unary path was exercised here.

## C2

Branch: [evalon/grpc-go-xd-576de4cd](https://github.com/kaitranntt-evals/grpc-go-xds-rds-interceptor-lifecycle-leak/tree/evalon/grpc-go-xd-576de4cd). Verdict: CONFIRMED.

Code on the branch (`internal/xds/server/listener_wrapper.go`, `maybeUpdateFilterChains`): `l.switchModeLocked(connectivity.ServingModeServing, nil)` is the first statement after the early-return check; `l.instantiateFilterChainRoutingConfigurationsLocked()` runs several statements later. `switchModeLocked` invokes `l.modeCallback` synchronously.

Repro: `verify/repro/c2_serving_callback_before_interceptors_test.go`. A real `xds.NewGRPCServer` with a `ServingModeCallback` that, on the first SERVING notification, records how many route interceptors the registered test filter has built, then stays blocked for 1s while the test re-reads the count; the filter records its own `BuildServerInterceptor` calls and stack.

```sh
cp verify/repro/c2_serving_callback_before_interceptors_test.go ~/wt/576de4cd/test/xds/
cd ~/wt/576de4cd
go test -race -v -count=1 -run '^Test$/^VerifyC2_' ./test/xds > /tmp/c2.log 2>&1
grep -nE "^\s*(--- |PASS|FAIL|ok )|OBSERVED|ORDER|c2_serving|^\s+google\.golang" /tmp/c2.log
```

```console
    c2_serving_callback_before_interceptors_test.go:175: Serving mode for listener "127.0.0.1:39879" changed to "SERVING", err: <nil>
    c2_serving_callback_before_interceptors_test.go:227: serving callback stack:
          google.golang.org/grpc/xds.(*GRPCServer).Serve.func1
          google.golang.org/grpc/internal/xds/server.(*listenerWrapper).switchModeLocked
          google.golang.org/grpc/internal/xds/server.(*listenerWrapper).maybeUpdateFilterChains
          google.golang.org/grpc/internal/xds/server.(*listenerWrapper).handleRDSUpdate
          google.golang.org/grpc/internal/xds/server.(*rdsWatcher).ResourceChanged
    c2_serving_callback_before_interceptors_test.go:228: OBSERVED interceptors constructed when SERVING callback was entered: 0
    c2_serving_callback_before_interceptors_test.go:229: OBSERVED interceptors constructed after holding SERVING callback open for 1s: 0
    c2_serving_callback_before_interceptors_test.go:234: OBSERVED interceptors constructed after SERVING callback returned: 1
    c2_serving_callback_before_interceptors_test.go:235: BuildServerInterceptor stack:
          google.golang.org/grpc/internal/xds/server.(*filterChain).newInterceptor
          google.golang.org/grpc/internal/xds/server.(*filterChain).convertVirtualHost
          google.golang.org/grpc/internal/xds/server.(*filterChain).constructUsableRouteConfiguration
          google.golang.org/grpc/internal/xds/server.(*listenerWrapper).instantiateFilterChainRoutingConfigurationsLocked
          google.golang.org/grpc/internal/xds/server.(*listenerWrapper).maybeUpdateFilterChains
          google.golang.org/grpc/internal/xds/server.(*listenerWrapper).handleRDSUpdate
          google.golang.org/grpc/internal/xds/server.(*rdsWatcher).ResourceChanged
    c2_serving_callback_before_interceptors_test.go:247: OBSERVED event order: [serving-callback-enter serving-callback-return BuildServerInterceptor]
    c2_serving_callback_before_interceptors_test.go:251: ORDER: serving notification was emitted before construction of the listener update's route interceptors (0 built while callback ran, 1 built after it returned)
    --- FAIL: Test/VerifyC2_ServingCallbackBeforeInterceptorConstruction (1.05s)
```

Both stacks are inside the same `maybeUpdateFilterChains` call: the callback ran and returned first, the interceptor was constructed afterwards.

Comparison with the task's base commit `4ee6ac46` (same repro file, unchanged production code) — the ordering is inherited, not introduced by the branch:

```sh
cp verify/repro/c2_serving_callback_before_interceptors_test.go ~/wt/base/test/xds/
cd ~/wt/base && go test -race -v -count=1 -run '^Test$/^VerifyC2_' ./test/xds 2>&1 | grep -E "^\s*(--- |PASS|FAIL|ok )|OBSERVED|ORDER"
```

```console
    c2_serving_callback_before_interceptors_test.go:228: OBSERVED interceptors constructed when SERVING callback was entered: 0
    c2_serving_callback_before_interceptors_test.go:229: OBSERVED interceptors constructed after holding SERVING callback open for 1s: 0
    c2_serving_callback_before_interceptors_test.go:234: OBSERVED interceptors constructed after SERVING callback returned: 1
    c2_serving_callback_before_interceptors_test.go:247: OBSERVED event order: [serving-callback-enter serving-callback-return BuildServerInterceptor]
    --- FAIL: Test/VerifyC2_ServingCallbackBeforeInterceptorConstruction (1.05s)
```

Impact reasoning: an application told "SERVING" is told so before the routing configuration it will serve with exists; if route-interceptor construction then fails, the listener has already been announced as serving. The callback runs with `listenerWrapper.mu` held and the swap happens under the same lock, so this run did not show an RPC being routed against a half-built configuration — only the notification order was observed. The practical exposure is limited to callbacks that act on the notification synchronously, and the same order exists at the base commit.

## C3

Branch: [evalon/grpc-go-xd-3100ee45](https://github.com/kaitranntt-evals/grpc-go-xds-rds-interceptor-lifecycle-leak/tree/evalon/grpc-go-xd-3100ee45). Verdict: CONFIRMED (both parts).

Code on the branch: `handleRDSUpdate` builds the replacement under `l.mu` (`constructUsableRouteConfiguration` releases the filter chain's previous `fc.serverFilters` references and stores the replacement's), swaps it in, unlocks, and only then runs `urc.closeInterceptors()` on the superseded configurations. `listenerWrapper.Close` takes `l.mu` and calls `activeFilterChainManager.stop()`, which releases `fc.serverFilters`.

Repro: `verify/repro/c3_close_destroys_filter_during_superseded_cleanup_test.go` (package `server`, uses the same `xdsSetupForTests`/`NewListenerWrapper` harness as the eval fixture `eval_rds_error_after_success_test.go`). One filter chain, one shared server filter, RDS route. The first interceptor's `Close` is held open; `handleRDSUpdate` re-delivers the RouteConfiguration on one goroutine, `lw.Close()` is called on another while that Close is held.

```sh
cp verify/repro/c3_close_destroys_filter_during_superseded_cleanup_test.go ~/wt/3100ee45/internal/xds/server/
cd ~/wt/3100ee45
go test -race -v -count=1 -run '^Test$/^VerifyC3_' ./internal/xds/server > /tmp/c3.log 2>&1
grep -nE "^\s*(--- |PASS|FAIL|ok )|OBSERVED|LIFETIME|^\s+google\.golang|DATA RACE" /tmp/c3.log
```

```console
    c3_close_destroys_filter_during_superseded_cleanup_test.go:196: OBSERVED before replacement: interceptors built=1, shared filter refCnt=1
    c3_close_destroys_filter_during_superseded_cleanup_test.go:208: OBSERVED with superseded interceptor-1 Close held open: interceptors built=2, shared filter refCnt=1 (1 => only the replacement's reference remains; the superseded configuration's reference is already released), filter destroyed=0
    c3_close_destroys_filter_during_superseded_cleanup_test.go:226: OBSERVED after concurrent listenerWrapper.Close: Close returned=true, shared filter destroyed=1, refCnt=0, superseded interceptor-1 Close finished=false
    c3_close_destroys_filter_during_superseded_cleanup_test.go:228: shared filter destroyed from:
          google.golang.org/grpc/internal/xds/server.(*vc3Filter).Close
          google.golang.org/grpc/internal/xds/server.(*refCountedServerFilter).Close
          google.golang.org/grpc/internal/xds/server.(*filterChain).stop
          google.golang.org/grpc/internal/xds/server.(*filterChainManager).stop
          google.golang.org/grpc/internal/xds/server.(*listenerWrapper).Close
    c3_close_destroys_filter_during_superseded_cleanup_test.go:230: OBSERVED event order so far: [interceptor-1-built interceptor-2-built interceptor-1-close-begin interceptor-2-close-begin interceptor-2-close-end shared-filter-destroyed]
    c3_close_destroys_filter_during_superseded_cleanup_test.go:239: OBSERVED final event order: [interceptor-1-built interceptor-2-built interceptor-1-close-begin interceptor-2-close-begin interceptor-2-close-end shared-filter-destroyed interceptor-1-close-end]
    c3_close_destroys_filter_during_superseded_cleanup_test.go:242: LIFETIME: shared filter destroyed (1) by listenerWrapper.Close while superseded interceptor Close was still running
    --- FAIL: Test/VerifyC3_CloseDestroysSharedFilterDuringSupersededCleanup (0.02s)
```

Per part:

- *Dependency lifetime* — holds. While the superseded interceptor's Close was still open the shared filter's refCnt was 1 with two interceptors built: the superseded configuration's reference had already been dropped, only the replacement's remained. Nothing on behalf of the superseded interceptor kept the filter alive.
- *Shutdown trigger* — holds. `listenerWrapper.Close` returned, the filter's own `Close` ran (refCnt 0, stack through `filterChain.stop`), and the superseded interceptor's Close finished only afterwards: `shared-filter-destroyed` precedes `interceptor-1-close-end`. No data race was reported by `-race`.

For reference, the eval fixture for this package on this branch fails on a different measurement (error-update reference release), which is not the behaviour this claim is about and was not used for the verdict:

```sh
cp ~/eval_tests/tests/eval_rds_error_after_success_test.go ~/wt/3100ee45/internal/xds/server/
cd ~/wt/3100ee45 && go test -race -v -count=1 -run '^Test$/^Eval_ServerSideXDS_ErrorUpdateReleasesServerFilterReferences$' ./internal/xds/server
```

```console
    eval_rds_error_after_success_test.go:224: after route1 error: filter ref count = 2, want 1 (route1 hold not released)
    eval_rds_error_after_success_test.go:241: after route2 error: filter ref count = 2, want 0 (route2 hold not released)
    --- FAIL: Test/Eval_ServerSideXDS_ErrorUpdateReleasesServerFilterReferences (2.06s)
```

Impact reasoning: a filter whose interceptors use the parent filter's state during Close (the ordering the A83-style "interceptors before filter" contract exists for) can have that parent destroyed underneath an interceptor that is mid-Close. It needs a server stop to overlap an RDS update's post-unlock cleanup — a narrow window with fast Close implementations, wide when Close does real work — so it shows up at shutdown/redeploy rather than in steady state. The repro calls `handleRDSUpdate` directly (as the eval fixture does) rather than driving it through the management server.

## C4

Branch: [evalon/grpc-go-xd-e02f4bfe](https://github.com/kaitranntt-evals/grpc-go-xds-rds-interceptor-lifecycle-leak/tree/evalon/grpc-go-xd-e02f4bfe). Verdict: CONFIRMED.

Test code on the branch (`internal/xds/server/listener_wrapper_test.go`):

```go
func (i *lifecycleTrackingInterceptor) Close() {
	if i.closed.Swap(true) {
		return // Counted once; double closes are reported via a mismatch below.
	}
	i.parent.interceptorsClosed.Add(1)
}
```

The only state the test's `verifyCounts` reads is `interceptorsBuilt`, `interceptorsClosed`, `filtersBuilt`, `filtersClosed`.

Direct probe, `verify/repro/c4_lifecycle_interceptor_double_close_test.go`:

```sh
cp verify/repro/c4_lifecycle_interceptor_double_close_test.go ~/wt/e02f4bfe/internal/xds/server/
cd ~/wt/e02f4bfe
go test -race -v -count=1 -run '^Test$/^(VerifyC4_|RDSUpdate_ReleasesSupersededInterceptors$)' ./internal/xds/server
```

```console
    c4_lifecycle_interceptor_double_close_test.go:21: OBSERVED before Close:      [interceptorsBuilt interceptorsClosed filtersBuilt filtersClosed] = [1 0 1 0]
    c4_lifecycle_interceptor_double_close_test.go:24: OBSERVED after 1st Close(): [interceptorsBuilt interceptorsClosed filtersBuilt filtersClosed] = [1 1 1 0]
    c4_lifecycle_interceptor_double_close_test.go:27: OBSERVED after 2nd Close(): [interceptorsBuilt interceptorsClosed filtersBuilt filtersClosed] = [1 1 1 0]
    c4_lifecycle_interceptor_double_close_test.go:28: OBSERVED interceptor.closed flag after 2nd Close(): true (a bool; cannot distinguish 1 call from 2)
    c4_lifecycle_interceptor_double_close_test.go:30: MASKED: second Close left every counter the cleanup assertions read unchanged ([1 1 1 0] == [1 1 1 0])
    --- PASS: Test/RDSUpdate_ReleasesSupersededInterceptors (0.00s)
    --- FAIL: Test/VerifyC4_LifecycleInterceptorDoubleClose (0.00s)
```

Mutation, `verify/repro/c4_double_close_mutation.sh` (production code changed to close every superseded configuration's interceptors twice, then the branch's own tests are run, then the file is restored):

```sh
cd ~/wt/e02f4bfe && bash ~/repos/grpc-go/verify/repro/c4_double_close_mutation.sh
```

```console
@@ -401,6 +401,7 @@ func (fc *filterChain) stop() {
 func (fc *filterChain) setUsableRouteConfiguration(urc *usableRouteConfiguration) {
 	if old := fc.usableRouteConfiguration.Swap(urc); old != nil {
 		old.close()
+		old.close() // MUTATION: duplicate close of superseded interceptors
 	}
 }
### unit: internal/xds/server TestRDSUpdate_ReleasesSupersededInterceptors (uses lifecycleTrackingInterceptor)
--- PASS: Test (0.00s)
    --- PASS: Test/RDSUpdate_ReleasesSupersededInterceptors (0.00s)
PASS
ok  	google.golang.org/grpc/internal/xds/server	1.030s
### e2e: test/xds tests in xds_server_rds_update_cleanup_test.go (use trackingInterceptor, which does not dedupe)
    xds_server_rds_update_cleanup_test.go:291: Timeout waiting for number of destroyed interceptors to reach 2, current value: 4
--- FAIL: Test (10.02s)
    --- FAIL: Test/ServerSideXDS_RouteConfigUpdate_ReleasesSupersededInterceptors (10.02s)
FAIL
FAIL	google.golang.org/grpc/test/xds	10.054s
```

Reading: the second `Close` on `lifecycleTrackingInterceptor` records nothing, changes no counter and fails no assertion; the in-code comment that double closes "are reported via a mismatch below" is not true, and the unit test stays green with every superseded interceptor closed twice. The branch's end-to-end test uses `trackingInterceptor` (in `test/xds/xds_server_filter_state_retention_test.go`, increments on every Close) and does go red under the same mutation, so a double close is not invisible to the branch's whole suite — only to the listener-wrapper unit test the claim names.

Impact reasoning: the unit test is the fast, deterministic check for interceptor lifecycle in `internal/xds/server`; as written it cannot fail on an exactly-once violation, so anyone running only that package (`go test ./internal/xds/server/...`, one of the task's two required suites) gets a false green on a double-close regression. The slower `test/xds` e2e test is the only backstop.

## C5

Branch: [evalon/grpc-go-xd-aa7e5dee](https://github.com/kaitranntt-evals/grpc-go-xds-rds-interceptor-lifecycle-leak/tree/evalon/grpc-go-xd-aa7e5dee). Verdict: CONFIRMED.

Repro: `verify/repro/c5_fatal_mutation.sh` + `verify/repro/c5_fatal_before_close_leaks_listener_test.go`. Part A runs the branch's own unmodified `TestListenerWrapper_HandleRDSUpdate_ReleasesSupersededResources` as a subtest and counts this process's listening sockets (`SO_ACCEPTCONN`) before it and after it has completely finished (deferred functions and `t.Cleanup` included). Part B is the same setup statement for statement with the locals captured so the filter state left behind can be read. The script runs both against unmodified production code (control), then reintroduces the regression the test guards against so that one of the test's own `t.Fatalf` assertions fires after `testutils.LocalTCPListener()` and before `lw.Close()`.

```sh
cd ~/wt/aa7e5dee && bash ~/repos/grpc-go/verify/repro/c5_fatal_mutation.sh
```

```console
### grep for cleanup registrations in the branch test file (defer / t.Cleanup / lw.Close / lis.Close):
87:	lis, err := testutils.LocalTCPListener()
89:		t.Fatalf("testutils.LocalTCPListener() failed: %v", err)
126:			t.Fatalf("Update %d: usable route configuration has unexpected error: %v", i, urc.err)
... (10 more t.Fatalf lines, 129-164) ...
169:	lw.Close()
171:		t.Fatalf("After listener close: closed %d filter instances, want %d", got, want)
175:			t.Fatalf("After listener close: filter %s has reference count %d, want %d", key.String(), got, want)
179:		t.Fatalf("After listener close: closed %d interceptors, want %d (all built interceptors)", got, want)
### 1) control: unmodified production code
    c5_fatal_before_close_leaks_listener_test.go:50: OBSERVED A: branch test passed=true; listening sockets before=0 after=0 leaked=0
    c5_fatal_before_close_leaks_listener_test.go:122: OBSERVED B: copy passed=true; after exit: lw.closed fired=true filtersClosed=1 refCnt=0 interceptorsBuilt=10 interceptorsClosed=10
    c5_fatal_before_close_leaks_listener_test.go:125: OBSERVED B: dial leaked listener 127.0.0.1:34709 after test exit: err=dial tcp 127.0.0.1:34709: connect: connection refused (nil => still listening)
ok  	google.golang.org/grpc/internal/xds/server	1.030s
### 2) mutation applied:
-			fc.usableRouteConfiguration.Swap(urc).close()
+			fc.usableRouteConfiguration.Swap(urc) // MUTATION: superseded interceptors no longer closed
    listener_wrapper_test.go:135: Update 2: closed 0 interceptors, want 2
    c5_fatal_before_close_leaks_listener_test.go:50: OBSERVED A: branch test passed=false; listening sockets before=0 after=1 leaked=1
    c5_fatal_before_close_leaks_listener_test.go:111: Update 2: closed 0 interceptors, want 2
    c5_fatal_before_close_leaks_listener_test.go:122: OBSERVED B: copy passed=false; after exit: lw.closed fired=false filtersClosed=0 refCnt=2 interceptorsBuilt=4 interceptorsClosed=0
    c5_fatal_before_close_leaks_listener_test.go:125: OBSERVED B: dial leaked listener 127.0.0.1:34277 after test exit: err=<nil> (nil => still listening)
FAIL	google.golang.org/grpc/internal/xds/server	0.029s
```

Reading: the test file has no `defer` and no `t.Cleanup`; the single `lw.Close()` is at line 169, after 11 fatal assertions. When the branch's real assertion at line 135 fired, the unmodified test left one listening socket open after it had fully exited (Part A), and the faithful copy shows the wrapper never closed, the server filter never closed (`filtersClosed=0`, `refCnt=2`), no interceptor closed, and the port still accepting connections (Part B). In the control run everything is released.

Impact reasoning: this only matters when the test is already failing: the leaked socket and filter live until the test binary exits, so the cost is a port and some objects held for the rest of that `go test` process, and potential noise for later tests in the same package run. No effect on production code or on a passing run.
