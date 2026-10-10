# Evidence — run v-4881a3ef

Environment: Linux, go1.25.7. Audited branch `grpc-go-xds-rds-interceptor-lifecycle-leak-perfect` at 614cb739; every claim targets its own branch in the repository `kaitranntt-evals/grpc-go-xds-rds-interceptor-lifecycle-leak`, fetched as remote `claims` and checked out as detached worktrees. No production code was changed on any branch; the C3 mutations were applied temporarily in a worktree and reverted.

```sh
cd ~/repos/grpc-go
git remote add claims https://github.com/kaitranntt-evals/grpc-go-xds-rds-interceptor-lifecycle-leak
for b in 7dc0ea45 a0b59987 7b3f0c09; do
  git fetch claims evalon/grpc-go-xd-$b:refs/remotes/claims/evalon/grpc-go-xd-$b
  git worktree add ~/wt-$b claims/evalon/grpc-go-xd-$b
done
# 7dc0ea45 -> 4a18184f2ceadfd62000057113b2b9e002ad1f58
# a0b59987 -> d0ae30522e699f20d9bb32f59076daa342c6c81c
# 7b3f0c09 -> 459c1594ce499a087a3877ee64d3e63f9905b717
```

## C1

Branch: [evalon/grpc-go-xd-7dc0ea45](https://github.com/kaitranntt-evals/grpc-go-xds-rds-interceptor-lifecycle-leak/tree/evalon/grpc-go-xd-7dc0ea45) @ 4a18184f. Verdict: CONFIRMED (both parts).

Relevant code on that branch (read only to design the probes): `RouteAndProcess` does `rc.users++` then `defer rc.release()`; `release()` calls `closeResources()` inline when `rc.closed && rc.users == 0`; `closeResources()` calls each `interceptor.Close()`; `xdsUnaryInterceptor` calls `handler(ctx, req)` only after `server.RouteAndProcess(ctx)` returns.

### Part 1 — synchronous retirement cleanup: CONFIRMED

Repro: `verify/repro/c1_release_sync_close_test.go` (unit level, package `server`). It marks a configuration as used by one RPC, retires it with `rc.close()`, then calls `rc.release()` in a goroutine against an interceptor whose `Close` blocks.

```sh
cd ~/wt-7dc0ea45
cp ~/repos/grpc-go/verify/repro/c1_release_sync_close_test.go internal/xds/server/verify_c1_release_sync_close_test.go
go test -race -count=1 -v -run '^Test$/^VerifyC1_' ./internal/xds/server
```

```console
=== RUN   Test
=== RUN   Test/VerifyC1_ReleaseOfLastUserBlocksOnInterceptorClose
    verify_c1_release_sync_close_test.go:38: rc.close() returned immediately while 1 user is active (cleanup deferred to last user)
    verify_c1_release_sync_close_test.go:54: interceptor.Close entered 0s after release() was called
    verify_c1_release_sync_close_test.go:64: release() has NOT returned 2s after being called; interceptor.Close still blocked
    verify_c1_release_sync_close_test.go:69: release() returned 2.009s after start, only after interceptor.Close was unblocked
    verify_c1_release_sync_close_test.go:74: PROBLEM PRESENT: release() of the last user synchronously waits for the retired interceptor's Close
--- FAIL: Test (2.01s)
    --- FAIL: Test/VerifyC1_ReleaseOfLastUserBlocksOnInterceptorClose (2.01s)
FAIL
FAIL	google.golang.org/grpc/internal/xds/server	2.037s
FAIL
```

`release()` entered `interceptor.Close` immediately, had not returned 2s later, and returned only once `Close` was unblocked.

### Part 2 — cleanup on the handler invocation path: CONFIRMED

Repro: `verify/repro/c1_handler_blocked_by_retired_close_test.go` (end to end: real `xds.NewGRPCServer`, management server, xDS client channel). A unary `EmptyCall` is held in `AllowRPC` of the original interceptor (`path1`); the RouteConfiguration is replaced (`path2`) and confirmed serving via `UnaryCall` probes; `AllowRPC` then returns nil (RPC admitted). The retired interceptor's `Close` blocks on a channel and dumps the stack of its caller. The `EmptyCall` service handler signals when it runs.

```sh
cd ~/wt-7dc0ea45
cp ~/repos/grpc-go/verify/repro/c1_handler_blocked_by_retired_close_test.go test/xds/verify_c1_handler_blocked_by_retired_close_test.go
go test -race -count=1 -v -run '^Test$/^VerifyC1_' ./test/xds
```

Key output (xDS/tlogger noise lines removed, nothing else edited):

```console
    verify_c1_handler_blocked_by_retired_close_test.go:233: [22ms] EmptyCall is inside AllowRPC(path1) of the original route configuration
    verify_c1_handler_blocked_by_retired_close_test.go:256: [324ms] replacement route configuration (path2) is serving; old configuration retired while EmptyCall still in AllowRPC
    verify_c1_handler_blocked_by_retired_close_test.go:265: [325ms] AllowRPC(path1) released (returns nil => RPC admitted)
    verify_c1_handler_blocked_by_retired_close_test.go:269: [325ms] retired interceptor Close(path1) entered and is now blocked. Goroutine stack of the Close caller:
        goroutine 135 [running]:
        google.golang.org/grpc/test/xds_test.s.TestVerifyC1_HandlerWaitsForRetiredInterceptorClose.func4.1()
        	/home/ubuntu/wt-7dc0ea45/test/xds/verify_c1_handler_blocked_by_retired_close_test.go:149 +0x5f
        sync.(*Once).doSlow(0xc00053eb04, 0xc0008071d8)
        	/usr/local/go/src/sync/once.go:78 +0xd2
        sync.(*Once).Do(0xc00053eb04, 0xc0008071d8)
        	/usr/local/go/src/sync/once.go:69 +0x45
        google.golang.org/grpc/test/xds_test.s.TestVerifyC1_HandlerWaitsForRetiredInterceptorClose.func4({0xc00068863b, 0x5})
        	/home/ubuntu/wt-7dc0ea45/test/xds/verify_c1_handler_blocked_by_retired_close_test.go:147 +0xe5
        google.golang.org/grpc/test/xds_test.(*vc1Interceptor).Close(0xc0001a43a8)
        	/home/ubuntu/wt-7dc0ea45/test/xds/verify_c1_handler_blocked_by_retired_close_test.go:81 +0x82
        google.golang.org/grpc/internal/xds/server.(*interceptorList).Close(0xc0001a43d8)
        	/home/ubuntu/wt-7dc0ea45/internal/xds/server/filter_chain_manager.go:571 +0x79
        google.golang.org/grpc/internal/xds/server.(*virtualHostWithInterceptors).close(...)
        	/home/ubuntu/wt-7dc0ea45/internal/xds/server/filter_chain_manager.go:239
        google.golang.org/grpc/internal/xds/server.(*usableRouteConfiguration).closeResources(0xc0004a0460)
        	/home/ubuntu/wt-7dc0ea45/internal/xds/server/filter_chain_manager.go:222 +0x1f7
        google.golang.org/grpc/internal/xds/server.(*usableRouteConfiguration).release(0xc0004a0460)
        	/home/ubuntu/wt-7dc0ea45/internal/xds/server/filter_chain_manager.go:216 +0xc6
        google.golang.org/grpc/internal/xds/server.RouteAndProcess({0x1f67b10, 0xc0006858c0})
        	/home/ubuntu/wt-7dc0ea45/internal/xds/server/routing.go:117 +0xd78
        google.golang.org/grpc/xds.xdsUnaryInterceptor({0x1f67b10, 0xc0006858c0}, {0x1b9b140, 0xc000685aa0}, 0xc0006858c0?, 0xc0001146c0)
        google.golang.org/grpc/interop/grpc_testing._TestService_EmptyCall_Handler({0x1cc7ac0, 0xc00013e300}, {0x1f67b10, 0xc0006858c0}, 0xc0007b2d00, 0x1dc43a8)
        	/home/ubuntu/wt-7dc0ea45/interop/grpc_testing/test_grpc.pb.go:293 +0x1e7
        google.golang.org/grpc.(*Server).processUnaryRPC(0xc000499208, {0x1f67b10, 0xc0006857a0}, 0xc0001e8340, 0xc0002e77d0, 0x2b07ca0, 0x0)
        	/home/ubuntu/wt-7dc0ea45/server.go:1441 +0x1a6a
        google.golang.org/grpc.(*Server).handleStream(0xc000499208, {0x1f6caf8, 0xc0004ccd00}, 0xc0001e8340)
        	/home/ubuntu/wt-7dc0ea45/server.go:1850 +0x10b3
        google.golang.org/grpc.(*Server).serveStreams.func2.1()
        	/home/ubuntu/wt-7dc0ea45/server.go:1076 +0x14a
        created by google.golang.org/grpc.(*Server).serveStreams.func2 in goroutine 40
        	/home/ubuntu/wt-7dc0ea45/server.go:1087 +0x213
    verify_c1_handler_blocked_by_retired_close_test.go:284: [3.325s] service handler has NOT run 3s after admission; retired interceptor Close still blocked
    verify_c1_handler_blocked_by_retired_close_test.go:290: [3.325s] retired interceptor Close(path1) unblocked and returned
    verify_c1_handler_blocked_by_retired_close_test.go:293: [3.325s] service handler entered
    verify_c1_handler_blocked_by_retired_close_test.go:299: [3.326s] held EmptyCall completed, err=<nil>
    verify_c1_handler_blocked_by_retired_close_test.go:304: PROBLEM PRESENT: the admitted unary RPC's service handler did not run until the retired interceptor's blocked Close returned
--- FAIL: Test (3.33s)
    --- FAIL: Test/VerifyC1_HandlerWaitsForRetiredInterceptorClose (3.33s)
FAIL
FAIL	google.golang.org/grpc/test/xds	3.362s
FAIL
```

Observations:
- The stack shows the retired interceptor's `Close` being executed on the RPC's own goroutine: `xdsUnaryInterceptor` (xds/server.go:236) → `RouteAndProcess` (routing.go:117, the deferred call) → `usableRouteConfiguration.release` → `closeResources` → `interceptorList.Close` → test interceptor `Close`.
- The RPC was admitted at 325ms; the handler had not run at 3.325s; it ran in the same millisecond `Close` was unblocked, and the RPC then completed with `err=<nil>`.

Impact reasoning: the RPC that happens to be the last user of a superseded configuration pays the full duration of every retired interceptor's `Close` (and of `ServerFilter.Close` for filters whose last reference this drops — `closeResources` closes those too) before its handler starts, and that time counts against the RPC's deadline. With a `Close` that returns promptly the effect is a small latency bump on one RPC per update; with a `Close` that waits (e.g. for in-flight work or a remote resource) the client-visible RPC stalls or exceeds its deadline even though it was already authorised. The trigger is ordinary: any RDS update that lands while at least one RPC is inside `AllowRPC`. New RPCs on the replacement configuration are unaffected (fixture `Eval_ServerSideXDS_ReplacementRPCWhileRetiredCloseBlocked` passes). The same deferred release sits in front of `xdsStreamInterceptor`'s handler call.

Fixture context (the eval fixtures do not catch this; all pass on this branch):

```sh
cd ~/wt-7dc0ea45
cp ~/eval_tests/tests/eval_xds_server_interceptor_leak_test.go test/xds/
cp ~/eval_tests/tests/eval_rds_error_after_success_test.go internal/xds/server/
go test -race -count=1 -v -timeout=5m -run '^Test$/^Eval_' ./test/xds ./internal/xds/server
```

```console
### 7dc0ea45
--- PASS: Test (0.58s)
    --- PASS: Test/Eval_ServerSideXDS_InterceptorBeforeFilterCloseOrder (0.05s)
    --- PASS: Test/Eval_ServerSideXDS_InterceptorLeak_MultiGenerationRDSUpdate (0.03s)
    --- PASS: Test/Eval_ServerSideXDS_InterceptorLeak_RDSUpdate (0.03s)
    --- PASS: Test/Eval_ServerSideXDS_InterceptorSwapOrder (0.03s)
    --- PASS: Test/Eval_ServerSideXDS_PartialRouteFailure_ClosesInterceptors (0.03s)
    --- PASS: Test/Eval_ServerSideXDS_PartialVirtualHostFailure_ClosesInterceptors (0.03s)
    --- PASS: Test/Eval_ServerSideXDS_ReplacementRPCWhileAllowRPCHeld (0.34s)
    --- PASS: Test/Eval_ServerSideXDS_ReplacementRPCWhileRetiredCloseBlocked (0.01s)
    --- PASS: Test/Eval_ServerSideXDS_ReplacementRPCWhileServerFilterCloseHeld (0.01s)
    --- PASS: Test/Eval_ServerSideXDS_StopWithAllowRPCAwaitingContextCancel (0.01s)
PASS
ok  	google.golang.org/grpc/test/xds	1.606s
    eval_rds_error_after_success_test.go:86: Created new snapshot cache...
    eval_rds_error_after_success_test.go:86: Registered Aggregated Discovery Service (ADS)...
    eval_rds_error_after_success_test.go:86: xDS management server serving at: 127.0.0.1:41155...
--- PASS: Test (0.02s)
    --- PASS: Test/Eval_ServerSideXDS_ErrorUpdateReleasesServerFilterReferences (0.02s)
PASS
ok  	google.golang.org/grpc/internal/xds/server	1.043s
```

## C2

Branch: [evalon/grpc-go-xd-a0b59987](https://github.com/kaitranntt-evals/grpc-go-xds-rds-interceptor-lifecycle-leak/tree/evalon/grpc-go-xd-a0b59987) @ d0ae3052. Verdict: CONFIRMED.

Repro: `verify/repro/c2_parent_filter_closed_before_interceptor_test.go` (end to end). The test filter builder returns a distinct parent filter object per `BuildServerFilter` call and appends every lifecycle event (filter built/closed, interceptor built, `AllowRPC` entered/returning, interceptor `Close` entered/completed) to one ordered log. An `EmptyCall` is held in the old interceptor's `AllowRPC`; the RouteConfiguration is replaced; 2s later the RPC is released; then the server is stopped. Three replacements:
- `RemoveFilter_DisabledOverride`: the only route carries per-route `FilterConfig{Disabled: true}` for the filter (with `envconfig.XDSClientExtProcEnabled`, as the eval fixture does).
- `RemoveFilter_NoRoutes`: the virtual host has no routes, so nothing references the filter (no env flag needed).
- `KeepFilter_Control`: the route keeps the filter with a different override (`path2`).

```sh
cd ~/wt-a0b59987
cp ~/repos/grpc-go/verify/repro/c2_parent_filter_closed_before_interceptor_test.go test/xds/verify_c2_parent_filter_closed_before_interceptor_test.go
go test -race -count=1 -v -run '^Test$/^VerifyC2_' ./test/xds
```

Key output (only the test's own lines and result lines kept):

```console
=== RUN   Test
=== RUN   Test/VerifyC2_KeepFilter_Control
    verify_c2_parent_filter_closed_before_interceptor_test.go:248: lifecycle events in order:
          filter#1 built
          interceptor(path1) built from filter#1
          interceptor(path1) AllowRPC entered (RPC active in old interceptor)
          --- test: sending replacement RouteConfiguration ---
          interceptor(path2) built from filter#1
          --- test: 2s after replacement; RPC still held in AllowRPC(path1); releasing it now ---
          interceptor(path1) AllowRPC returning
          interceptor(path1) of filter#1 Close entered
          interceptor(path1) of filter#1 Close completed
          --- test: held RPC completed, err=<nil> ---
          --- test: stopping server ---
          interceptor(path2) of filter#1 Close entered
          interceptor(path2) of filter#1 Close completed
          filter#1 Close (PARENT FILTER CLOSED)
    verify_c2_parent_filter_closed_before_interceptor_test.go:264: event index: parent filter#1 Close=#13, AllowRPC(path1) returning=#6, old interceptor Close completed=#8
    verify_c2_parent_filter_closed_before_interceptor_test.go:271: parent filter#1 stayed open until the old interceptor's Close completed
=== RUN   Test/VerifyC2_RemoveFilter_DisabledOverride
    verify_c2_parent_filter_closed_before_interceptor_test.go:248: lifecycle events in order:
          filter#1 built
          interceptor(path1) built from filter#1
          interceptor(path1) AllowRPC entered (RPC active in old interceptor)
          --- test: sending replacement RouteConfiguration ---
          filter#1 Close (PARENT FILTER CLOSED)
          --- test: 2s after replacement; RPC still held in AllowRPC(path1); releasing it now ---
          interceptor(path1) AllowRPC returning
          interceptor(path1) of filter#1 Close entered
          interceptor(path1) of filter#1 Close completed
          --- test: held RPC completed, err=<nil> ---
          --- test: stopping server ---
    verify_c2_parent_filter_closed_before_interceptor_test.go:264: event index: parent filter#1 Close=#4, AllowRPC(path1) returning=#6, old interceptor Close completed=#8
    verify_c2_parent_filter_closed_before_interceptor_test.go:269: PROBLEM PRESENT: parent filter#1 was closed (event #4) before the old interceptor's Close completed (event #8); it was closed while an RPC was still inside that interceptor's AllowRPC (AllowRPC returned at event #6)
=== RUN   Test/VerifyC2_RemoveFilter_NoRoutes
    verify_c2_parent_filter_closed_before_interceptor_test.go:248: lifecycle events in order:
          filter#1 built
          interceptor(path1) built from filter#1
          interceptor(path1) AllowRPC entered (RPC active in old interceptor)
          --- test: sending replacement RouteConfiguration ---
          filter#1 Close (PARENT FILTER CLOSED)
          --- test: 2s after replacement; RPC still held in AllowRPC(path1); releasing it now ---
          interceptor(path1) AllowRPC returning
          interceptor(path1) of filter#1 Close entered
          interceptor(path1) of filter#1 Close completed
          --- test: held RPC completed, err=<nil> ---
          --- test: stopping server ---
    verify_c2_parent_filter_closed_before_interceptor_test.go:264: event index: parent filter#1 Close=#4, AllowRPC(path1) returning=#6, old interceptor Close completed=#8
    verify_c2_parent_filter_closed_before_interceptor_test.go:269: PROBLEM PRESENT: parent filter#1 was closed (event #4) before the old interceptor's Close completed (event #8); it was closed while an RPC was still inside that interceptor's AllowRPC (AllowRPC returned at event #6)
--- FAIL: Test (9.15s)
    --- PASS: Test/VerifyC2_KeepFilter_Control (3.04s)
    --- FAIL: Test/VerifyC2_RemoveFilter_DisabledOverride (3.08s)
    --- FAIL: Test/VerifyC2_RemoveFilter_NoRoutes (3.03s)
FAIL
FAIL	google.golang.org/grpc/test/xds	9.179s
FAIL
```

Observations:
- In both removal variants `filter#1 Close (PARENT FILTER CLOSED)` is recorded immediately after the replacement is sent, while the RPC is still inside `AllowRPC(path1)` — two events before `AllowRPC` returns and four before the old interceptor's `Close` completes.
- In the control the parent filter stays open through the old interceptor's `Close` and is closed last, at server stop, so the recorder distinguishes the two orders.
- Code path consistent with the observation: `updateUsableRouteConfiguration` calls `Swap(urc).close()` (which defers interceptor close while an RPC holds the configuration) and then unconditionally `sf.Close()` on every entry of `fc.serverFilters`; when the new configuration takes no reference, the ref count reaches zero and the real filter is closed.

Impact reasoning: a filter that owns shared state its interceptors rely on (connections, caches, background workers) has that state torn down while an RPC is still executing inside one of its interceptors, and the interceptor's own `Close` later runs against an already-closed parent. Nothing reports an error — the held RPC completed `err=<nil>` here because the test filter does not touch closed state; a real filter would hit use-after-close. Trigger: an RDS update that stops referencing a filter (per-route/per-vhost disable, or routes removed) while any RPC is in `AllowRPC` on the old configuration; the wider the `AllowRPC` window (e.g. an authorisation call-out), the more likely.

Fixture context on this branch (shutdown-path ordering and error-update ref counting also fail; the replacement path in this claim is not covered by a failing fixture):

```console
### a0b59987
eval_xds_server_interceptor_leak_test.go:1913: dependency order violation during shutdown: parent filter closed at event #0 before interceptor closed at event #1 (events: [filter-close icpt-close])
--- FAIL: Test (0.56s)
    --- PASS: Test/Eval_ServerSideXDS_InterceptorBeforeFilterCloseOrder (0.03s)
    --- PASS: Test/Eval_ServerSideXDS_InterceptorLeak_MultiGenerationRDSUpdate (0.04s)
    --- PASS: Test/Eval_ServerSideXDS_InterceptorLeak_RDSUpdate (0.02s)
    --- PASS: Test/Eval_ServerSideXDS_InterceptorSwapOrder (0.03s)
    --- PASS: Test/Eval_ServerSideXDS_PartialRouteFailure_ClosesInterceptors (0.02s)
    --- PASS: Test/Eval_ServerSideXDS_PartialVirtualHostFailure_ClosesInterceptors (0.03s)
    --- PASS: Test/Eval_ServerSideXDS_ReplacementRPCWhileAllowRPCHeld (0.34s)
    --- PASS: Test/Eval_ServerSideXDS_ReplacementRPCWhileRetiredCloseBlocked (0.01s)
    --- PASS: Test/Eval_ServerSideXDS_ReplacementRPCWhileServerFilterCloseHeld (0.02s)
    --- FAIL: Test/Eval_ServerSideXDS_StopWithAllowRPCAwaitingContextCancel (0.01s)
FAIL
FAIL	google.golang.org/grpc/test/xds	0.583s
    eval_rds_error_after_success_test.go:86: Created new snapshot cache...
    eval_rds_error_after_success_test.go:86: Registered Aggregated Discovery Service (ADS)...
    eval_rds_error_after_success_test.go:86: xDS management server serving at: 127.0.0.1:42797...
    eval_rds_error_after_success_test.go:224: after route1 error: filter ref count = 2, want 1 (route1 hold not released)
    eval_rds_error_after_success_test.go:241: after route2 error: filter ref count = 2, want 0 (route2 hold not released)
--- FAIL: Test (2.08s)
    --- FAIL: Test/Eval_ServerSideXDS_ErrorUpdateReleasesServerFilterReferences (2.08s)
FAIL
FAIL	google.golang.org/grpc/internal/xds/server	2.102s
FAIL
```

## C3

Branch: [evalon/grpc-go-xd-7b3f0c09](https://github.com/kaitranntt-evals/grpc-go-xds-rds-interceptor-lifecycle-leak/tree/evalon/grpc-go-xd-7b3f0c09) @ 459c1594. Verdict: CONFIRMED (both parts).

Naming drift: the branch has no `TestServerSideXDS_InterceptorLeak_RDSUpdate`. The authored interceptor-lifetime regression test is `TestServerSideXDS_RouteConfigurationUpdates_ReleaseSupersededInterceptors` in `test/xds/xds_server_rds_update_test.go`, plus two unit tests `TestUsableRouteConfiguration_InterceptorsClosedOn(Error)Replacement` in `internal/xds/server/filter_chain_manager_test.go`. No `filtersCreated`/`filtersDestroyed` counters exist.

Assertion text. Every `t.Fatal*` in the authored e2e test, then in the two authored unit tests:

```sh
cd ~/wt-7b3f0c09
grep -n "t.Fatal\|t.Error" test/xds/xds_server_rds_update_test.go
grep -n "t.Fatal\|t.Error" internal/xds/server/filter_chain_manager_test.go | awk -F: '$1>820'
grep -n "BuildServerFilter\|Builder) Close" test/xds/xds_server_rds_update_test.go internal/xds/server/filter_chain_manager_test.go
```

```console
129:		t.Fatalf("Failed to retrieve host and port of server: %v", err)
173:		t.Fatal(err)
178:		t.Fatalf("grpc.NewClient() failed: %v", err)
185:		t.Fatalf("Timeout waiting for server to enter SERVING mode")
190:		t.Fatalf("EmptyCall() failed: %v", err)
193:		t.Fatalf("Got %d live interceptors after initial configuration, want %d", got, wantLiveInterceptors)
217:			t.Fatal(err)
225:			t.Fatalf("Timeout after RouteConfiguration update %d: %d interceptors created, %d destroyed; want %d created and %d live", i, fb.interceptorsCreated.Load(), fb.interceptorsDestroyed.Load(), wantCreated, wantLiveInterceptors)
232:		t.Fatalf("RPC failed while RouteConfiguration updates were being applied: %v", *errp)
235:		t.Fatalf("%d RPCs were run through closed interceptors", got)
242:		t.Fatalf("Got %d live interceptors after server stop, want 0", got)
849:			t.Fatalf("After %d updates, %d interceptors are open, want %d", i+1, got, want)
858:		t.Fatalf("Closed %d interceptors while an RPC was in flight, want %d", got, want)
861:		t.Fatalf("AllowRPC() on in-flight configuration failed: %v", err)
865:		t.Fatalf("Closed %d interceptors after in-flight RPC completed, want %d", got, want)
871:		t.Fatalf("acquireRouteConfiguration() returned a stale configuration")
881:		t.Fatalf("After stop, closed %d interceptors, want %d", got, want)
907:		t.Fatalf("Closed %d interceptors after replacement with error, want %d", got, want)
914:		t.Fatalf("acquireRouteConfiguration() returned configuration without error, want error")
test/xds/xds_server_rds_update_test.go:73:func (b *lifecycleHTTPFilterBuilder) BuildServerFilter() httpfilter.ServerFilter { return b }
test/xds/xds_server_rds_update_test.go:75:func (*lifecycleHTTPFilterBuilder) Close() {}
internal/xds/server/filter_chain_manager_test.go:806:func (cfb *countingFilterBuilder) BuildServerFilter() httpfilter.ServerFilter { return cfb }
internal/xds/server/filter_chain_manager_test.go:808:func (*countingFilterBuilder) Close() {}
```

All assertions are about interceptors (created / destroyed / live / used-after-close) or RPC success. `BuildServerFilter` returns the builder without counting and both filter `Close` methods are empty, so no parent-filter construction or destruction total is recorded, let alone asserted.

Demonstration by mutation. Two single-purpose mutations of the solution, each applied alone, run against the authored tests and against the eval fixture `TestEval_ServerSideXDS_InterceptorLeak_RDSUpdate` (which does count filters):
- `verify/repro/c3_mutation_construction.patch`: `getOrCreateServerFilterWithMap` never reuses the cached filter, so a second parent filter is constructed on replacement.
- `verify/repro/c3_mutation_destruction.patch`: `refCountedServerFilter.Close` never calls the wrapped filter's `Close`, so the parent filter is never destroyed.

```sh
cd ~/wt-7b3f0c09
cp ~/eval_tests/tests/eval_xds_server_interceptor_leak_test.go test/xds/
bash ~/repos/grpc-go/verify/repro/c3_run.sh
```

```console
#### BASELINE (no mutation)
## authored e2e test
=== RUN   Test
=== RUN   Test/ServerSideXDS_RouteConfigurationUpdates_ReleaseSupersededInterceptors
--- PASS: Test (0.07s)
    --- PASS: Test/ServerSideXDS_RouteConfigurationUpdates_ReleaseSupersededInterceptors (0.06s)
PASS
ok  	google.golang.org/grpc/test/xds	1.100s
## authored unit tests
=== RUN   Test
=== RUN   Test/UsableRouteConfiguration_InterceptorsClosedOnErrorReplacement
=== RUN   Test/UsableRouteConfiguration_InterceptorsClosedOnReplacement
--- PASS: Test (0.00s)
    --- PASS: Test/UsableRouteConfiguration_InterceptorsClosedOnErrorReplacement (0.00s)
    --- PASS: Test/UsableRouteConfiguration_InterceptorsClosedOnReplacement (0.00s)
PASS
ok  	google.golang.org/grpc/internal/xds/server	1.030s
## eval fixture
=== RUN   Test
=== RUN   Test/Eval_ServerSideXDS_InterceptorLeak_RDSUpdate
--- PASS: Test (0.06s)
    --- PASS: Test/Eval_ServerSideXDS_InterceptorLeak_RDSUpdate (0.05s)
PASS
ok  	google.golang.org/grpc/test/xds	1.092s
#### MUTATION: construction
 internal/xds/server/listener_wrapper.go | 7 +++----
 1 file changed, 3 insertions(+), 4 deletions(-)
## authored e2e test
=== RUN   Test
=== RUN   Test/ServerSideXDS_RouteConfigurationUpdates_ReleaseSupersededInterceptors
--- PASS: Test (0.07s)
    --- PASS: Test/ServerSideXDS_RouteConfigurationUpdates_ReleaseSupersededInterceptors (0.07s)
PASS
ok  	google.golang.org/grpc/test/xds	1.101s
## authored unit tests
=== RUN   Test
=== RUN   Test/UsableRouteConfiguration_InterceptorsClosedOnErrorReplacement
=== RUN   Test/UsableRouteConfiguration_InterceptorsClosedOnReplacement
--- PASS: Test (0.00s)
    --- PASS: Test/UsableRouteConfiguration_InterceptorsClosedOnErrorReplacement (0.00s)
    --- PASS: Test/UsableRouteConfiguration_InterceptorsClosedOnReplacement (0.00s)
PASS
ok  	google.golang.org/grpc/internal/xds/server	1.028s
## eval fixture
=== RUN   Test
=== RUN   Test/Eval_ServerSideXDS_InterceptorLeak_RDSUpdate
    eval_xds_server_interceptor_leak_test.go:314: Created 2 filter instances, want: 1
--- FAIL: Test (0.04s)
    --- FAIL: Test/Eval_ServerSideXDS_InterceptorLeak_RDSUpdate (0.03s)
FAIL
FAIL	google.golang.org/grpc/test/xds	0.065s
FAIL
#### MUTATION: destruction
 internal/xds/server/filter_chain_manager.go | 5 ++---
 1 file changed, 2 insertions(+), 3 deletions(-)
## authored e2e test
=== RUN   Test
=== RUN   Test/ServerSideXDS_RouteConfigurationUpdates_ReleaseSupersededInterceptors
--- PASS: Test (0.08s)
    --- PASS: Test/ServerSideXDS_RouteConfigurationUpdates_ReleaseSupersededInterceptors (0.07s)
PASS
ok  	google.golang.org/grpc/test/xds	1.110s
## authored unit tests
=== RUN   Test
=== RUN   Test/UsableRouteConfiguration_InterceptorsClosedOnErrorReplacement
=== RUN   Test/UsableRouteConfiguration_InterceptorsClosedOnReplacement
--- PASS: Test (0.00s)
    --- PASS: Test/UsableRouteConfiguration_InterceptorsClosedOnErrorReplacement (0.00s)
    --- PASS: Test/UsableRouteConfiguration_InterceptorsClosedOnReplacement (0.00s)
PASS
ok  	google.golang.org/grpc/internal/xds/server	1.029s
## eval fixture
=== RUN   Test
=== RUN   Test/Eval_ServerSideXDS_InterceptorLeak_RDSUpdate
    eval_xds_server_interceptor_leak_test.go:395: Destroyed 0 filter instances, want: 1
--- FAIL: Test (0.04s)
    --- FAIL: Test/Eval_ServerSideXDS_InterceptorLeak_RDSUpdate (0.03s)
FAIL
FAIL	google.golang.org/grpc/test/xds	0.066s
FAIL
```

### Part 1 — parent-filter construction assertions: CONFIRMED

With the construction mutation the authored e2e test and unit tests still PASS, while the fixture fails with `Created 2 filter instances, want: 1`.

### Part 2 — parent-filter destruction assertions: CONFIRMED

With the destruction mutation the authored e2e test and unit tests still PASS, while the fixture fails with `Destroyed 0 filter instances, want: 1`.

Impact reasoning: the branch's own regression coverage would stay green if a later change rebuilt the shared parent filter on every RDS update (losing filter state the cache exists to retain) or never released it at shutdown (a leak of exactly the kind the task is about). The solution itself is currently correct on both counts — the unmutated baseline passes the fixture's `filtersCreated == 1` / `filtersDestroyed == 1` checks — so this is a coverage gap, not a behavioural defect.
