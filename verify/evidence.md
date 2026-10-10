Observations only. All commands were run on this VM (go1.25.7 linux/amd64). Claim branches live in the repository `kaitranntt-evals/grpc-go-xds-rds-interceptor-lifecycle-leak`, fetched as remote `claims`; each was checked out in its own worktree. Log files referenced below are committed under `verify/logs/` (noise from the gRPC test logger was filtered out with `grep -v`; nothing else was edited). Repro files are under `verify/repro/`.

Common setup (replayable):

```sh
cd ~/repos/grpc-go
git remote add claims https://github.com/kaitranntt-evals/grpc-go-xds-rds-interceptor-lifecycle-leak
for b in e8b11ebf 2f2a2d0c 7ea015db; do
  git fetch claims evalon/grpc-go-xd-$b:refs/remotes/claims/evalon/grpc-go-xd-$b
  git worktree add ~/wt-$b claims/evalon/grpc-go-xd-$b
done
git worktree add ~/wt-base 4ee6ac46fada69c06576cee108b009689a000520      # task base commit
git worktree add ~/wt-perfect origin/grpc-go-xds-rds-interceptor-lifecycle-leak-perfect   # audited reference branch
```

Commits adjudicated: C1 `8a4ad7468a945da32076456ea8321b9395ea7003`, C2 `b19fe7a006e1e5149916ca94b4238d83317bd160`, C3 `f6be604eca99ed301c953578a719ac08b07f39ed`.

## C1

Target: branch `evalon/grpc-go-xd-e8b11ebf` (commit `8a4ad746`). Verdict recorded: CONFIRMED (both parts).

Repro: `verify/repro/c1_stop_closes_filter_while_rpc_active_test.go`. It registers an HTTP filter whose `ServerFilter.Close()` and `ServerInterceptor.Close()` both append to a timestamped event log together with (a) `ctx.Err()` of the RPC that is being held active and (b) the gRPC call stack that invoked `Close()`. `ServerFilter.Close()` additionally sleeps 300ms and samples the RPC context again. Three scenarios against a real `xds.NewGRPCServer` + xDS management server:

- A `StopWhileRPCInAllowRPC`: unary RPC blocked inside the configuration interceptor's `AllowRPC` (returns only on context cancellation), then `Stop()`.
- B `StopWhileRPCInHandler`: bidi stream that passed `AllowRPC` and whose handler is running, then `Stop()`.
- C `GracefulStopWhileRPCInHandler`: same as B but `GracefulStop()`, handler released 1s later.

```sh
cp verify/repro/c1_stop_closes_filter_while_rpc_active_test.go ~/wt-e8b11ebf/test/xds/
cd ~/wt-e8b11ebf && go test -race -count=1 -v -run '^Test$/^VerifyC1_' ./test/xds     # -> verify/logs/c1_target_branch.log
```

Output (log lines truncated at 430 columns; full lines in `verify/logs/c1_target_branch.log`):

```console
=== RUN   Test/VerifyC1_GracefulStopWhileRPCInHandler
01 +   12.9ms ServerFilter.Build     
02 +   13.0ms Interceptor.Build      
03 +   22.6ms AllowRPC.enter         
04 +   22.6ms AllowRPC.return        nil (RPC proceeds to handler)
05 +   22.6ms Handler.enter          
06 +   27.9ms Stop.call              GracefulStop; activeRPC ctx.Err()=<nil>
07 +   28.9ms Interceptor.Close      activeRPC ctx.Err()=<nil> | stack: /internal/xds/server.(*interceptorList).Close <- /internal/xds/server.(*usableRouteConfiguration).closeInterceptors <- /internal/xds/server.(*usableRouteConfiguration).retire <- /internal/xds/server.(*filterChain).stop <- /internal/xds/server.(*filterChainManager).stop <- /internal/xds/server.(*listenerWrapper).Close <- .(*listenSocket).Close <- .(*Server)
08 +   28.9ms ServerFilter.Close     activeRPC ctx.Err()=<nil> | stack: /internal/xds/server.(*refCountedServerFilter).Close <- /internal/xds/server.(*filterChain).stop <- /internal/xds/server.(*filterChainManager).stop <- /internal/xds/server.(*listenerWrapper).Close <- .(*listenSocket).Close <- .(*Server).closeListenersLocked <- .(*Server).stop <- .(*Server).GracefulStop <- /xds.(*GRPCServer).GracefulStop <- /test/xds_test.s
09 +  329.1ms ServerFilter.Close+300ms activeRPC ctx.Err()=<nil>
10 + 1030.5ms Handler.release        activeRPC ctx.Err()=<nil>
11 + 1030.5ms Handler.return         ctx.Err()=<nil>
12 + 1031.2ms Client.RPCDone         err=EOF
13 + 1031.5ms Stop.return            activeRPC ctx.Err()=context canceled
OBSERVED order indexes: Stop.call=6 ServerFilter.Close=8 Interceptor.Close=7 Handler.return=11
OBSERVED ServerFilter.Close ran before the active RPC ended: true
OBSERVED Interceptor.Close ran before the active RPC ended: true
=== RUN   Test/VerifyC1_StopWhileRPCInAllowRPC
01 +    6.0ms ServerFilter.Build     
02 +    6.0ms Interceptor.Build      
03 +   12.4ms AllowRPC.enter         
04 +   12.4ms Stop.call              activeRPC ctx.Err()=<nil>
05 +   13.3ms ServerFilter.Close     activeRPC ctx.Err()=<nil> | stack: /internal/xds/server.(*refCountedServerFilter).Close <- /internal/xds/server.(*filterChain).stop <- /internal/xds/server.(*filterChainManager).stop <- /internal/xds/server.(*listenerWrapper).Close <- .(*listenSocket).Close <- .(*Server).closeListenersLocked <- .(*Server).stop <- .(*Server).Stop <- /xds.(*GRPCServer).Stop <- /test/xds_test.s.TestVerifyC1_St
06 +  314.0ms ServerFilter.Close+300ms activeRPC ctx.Err()=<nil>
07 +  314.2ms AllowRPC.return        ctx.Err()=context canceled
08 +  314.3ms Interceptor.Close      activeRPC ctx.Err()=context canceled | stack: /internal/xds/server.(*interceptorList).Close <- /internal/xds/server.(*usableRouteConfiguration).closeInterceptors <- /internal/xds/server.(*usableRouteConfiguration).release <- /internal/xds/server.RouteAndProcess <- /xds.xdsUnaryInterceptor <- /interop/grpc_testing._TestService_EmptyCall_Handler <- .(*Server).processUnaryRPC <- .(*Server).han
09 +  314.5ms Stop.return            activeRPC ctx.Err()=context canceled
10 +  314.5ms Client.RPCDone         err=rpc error: code = Unavailable desc = error reading from server: EOF
OBSERVED order indexes: Stop.call=4 ServerFilter.Close=5 Interceptor.Close=8 AllowRPC.return=7
OBSERVED ServerFilter.Close ran before the active RPC ended: true
OBSERVED Interceptor.Close ran before the active RPC ended: false
=== RUN   Test/VerifyC1_StopWhileRPCInHandler
01 +    5.5ms ServerFilter.Build     
02 +    5.5ms Interceptor.Build      
03 +   11.0ms AllowRPC.enter         
04 +   11.1ms AllowRPC.return        nil (RPC proceeds to handler)
05 +   11.1ms Handler.enter          
06 +   11.1ms Stop.call              activeRPC ctx.Err()=<nil>
07 +   11.9ms Interceptor.Close      activeRPC ctx.Err()=<nil> | stack: /internal/xds/server.(*interceptorList).Close <- /internal/xds/server.(*usableRouteConfiguration).closeInterceptors <- /internal/xds/server.(*usableRouteConfiguration).retire <- /internal/xds/server.(*filterChain).stop <- /internal/xds/server.(*filterChainManager).stop <- /internal/xds/server.(*listenerWrapper).Close <- .(*listenSocket).Close <- .(*Server)
08 +   11.9ms ServerFilter.Close     activeRPC ctx.Err()=<nil> | stack: /internal/xds/server.(*refCountedServerFilter).Close <- /internal/xds/server.(*filterChain).stop <- /internal/xds/server.(*filterChainManager).stop <- /internal/xds/server.(*listenerWrapper).Close <- .(*listenSocket).Close <- .(*Server).closeListenersLocked <- .(*Server).stop <- .(*Server).Stop <- /xds.(*GRPCServer).Stop <- /test/xds_test.s.TestVerifyC1_St
09 +  312.6ms ServerFilter.Close+300ms activeRPC ctx.Err()=<nil>
10 +  312.7ms Handler.return         ctx.Err()=context canceled
11 +  312.9ms Stop.return            activeRPC ctx.Err()=context canceled
12 +  313.1ms Client.RPCDone         err=rpc error: code = Unavailable desc = error reading from server: EOF
OBSERVED order indexes: Stop.call=6 ServerFilter.Close=8 Interceptor.Close=7 Handler.return=10
OBSERVED ServerFilter.Close ran before the active RPC ended: true
OBSERVED Interceptor.Close ran before the active RPC ended: true
--- PASS: Test (2.12s)
    --- PASS: Test/VerifyC1_GracefulStopWhileRPCInHandler (1.03s)
    --- PASS: Test/VerifyC1_StopWhileRPCInAllowRPC (0.52s)
    --- PASS: Test/VerifyC1_StopWhileRPCInHandler (0.57s)
ok  	google.golang.org/grpc/test/xds	3.153s
```

What this shows, per part:

- **resource_lifetime** — In all three scenarios `ServerFilter.Close` is logged while the held RPC's `ctx.Err()` is `<nil>` and before the RPC ends (`AllowRPC.return` / `Handler.return`). In B and C the configuration interceptor's `Close` is also logged before the handler returns. In A the interceptor close is deferred until `AllowRPC` returns (the branch's `inFlight`/`retire()` reference only spans `RouteAndProcess`, i.e. `AllowRPC`), but the server filter is still closed first, with nothing waiting on the RPC. Neither "retain both until the RPC completes" (the refute condition) holds in any scenario. In C the RPC is never cancelled: it runs for a further ~1s and completes with `err=EOF` (success) after its filter and interceptor were closed.
- **shutdown_trigger** — The recorded stack for `ServerFilter.Close` is `refCountedServerFilter.Close <- filterChain.stop <- filterChainManager.stop <- listenerWrapper.Close <- listenSocket.Close <- Server.closeListenersLocked <- Server.stop <- Server.Stop <- xds.GRPCServer.Stop`. At that moment, and again 300ms later while still inside `Close()` (`ServerFilter.Close+300ms`), the active RPC's context is not cancelled (`ctx.Err()=<nil>`); cancellation (`context canceled`) is only observed afterwards, i.e. `Server.stop()` runs filter-chain shutdown from `closeListenersLocked()` before `closeServerTransportsLocked()` cancels transport contexts.

Context needed to weigh the finding — the same repro on the task's base commit and on the audited reference branch:

```sh
cp verify/repro/c1_stop_closes_filter_while_rpc_active_test.go ~/wt-base/test/xds/    && (cd ~/wt-base    && go test -race -count=1 -v -run '^Test$/^VerifyC1_' ./test/xds)   # -> verify/logs/c1_base.log
cp verify/repro/c1_stop_closes_filter_while_rpc_active_test.go ~/wt-perfect/test/xds/ && (cd ~/wt-perfect && go test -race -count=1 -v -run '^Test$/^VerifyC1_' ./test/xds)   # -> verify/logs/c1_perfect.log
```

```console
## c1_base.log
OBSERVED order indexes: Stop.call=6 ServerFilter.Close=8 Interceptor.Close=7 Handler.return=11
OBSERVED ServerFilter.Close ran before the active RPC ended: true
OBSERVED Interceptor.Close ran before the active RPC ended: true
OBSERVED order indexes: Stop.call=4 ServerFilter.Close=6 Interceptor.Close=5 AllowRPC.return=8
OBSERVED ServerFilter.Close ran before the active RPC ended: true
OBSERVED Interceptor.Close ran before the active RPC ended: true
OBSERVED order indexes: Stop.call=6 ServerFilter.Close=8 Interceptor.Close=7 Handler.return=10
OBSERVED ServerFilter.Close ran before the active RPC ended: true
OBSERVED Interceptor.Close ran before the active RPC ended: true
--- PASS: Test (2.13s)
    --- PASS: Test/VerifyC1_GracefulStopWhileRPCInHandler (1.04s)
    --- PASS: Test/VerifyC1_StopWhileRPCInAllowRPC (0.57s)
    --- PASS: Test/VerifyC1_StopWhileRPCInHandler (0.52s)
ok  	google.golang.org/grpc/test/xds	3.156s
## c1_perfect.log
OBSERVED order indexes: Stop.call=6 ServerFilter.Close=8 Interceptor.Close=7 Handler.return=11
OBSERVED ServerFilter.Close ran before the active RPC ended: true
OBSERVED Interceptor.Close ran before the active RPC ended: true
OBSERVED order indexes: Stop.call=4 ServerFilter.Close=6 Interceptor.Close=5 AllowRPC.return=8
OBSERVED ServerFilter.Close ran before the active RPC ended: true
OBSERVED Interceptor.Close ran before the active RPC ended: true
OBSERVED order indexes: Stop.call=6 ServerFilter.Close=8 Interceptor.Close=7 Handler.return=10
OBSERVED ServerFilter.Close ran before the active RPC ended: true
OBSERVED Interceptor.Close ran before the active RPC ended: true
--- PASS: Test (2.13s)
    --- PASS: Test/VerifyC1_GracefulStopWhileRPCInHandler (1.09s)
    --- PASS: Test/VerifyC1_StopWhileRPCInAllowRPC (0.52s)
    --- PASS: Test/VerifyC1_StopWhileRPCInHandler (0.52s)
ok  	google.golang.org/grpc/test/xds	3.156s
```

So closing filter-chain resources from listener close while RPCs are still active is the pre-existing behavior of the base commit and is unchanged in the reference branch; the claim branch did not introduce it. What is specific to the claim branch is scenario A's ordering: the server filter is closed *before* the interceptor it built (base/reference close the interceptor first, both while the RPC is active).

Eval fixtures on the claim branch (archive bytes copied to the provisioned paths):

```sh
cp ~/eval_fixtures/tests/eval_xds_server_interceptor_leak_test.go ~/wt-e8b11ebf/test/xds/
cp ~/eval_fixtures/tests/eval_rds_error_after_success_test.go ~/wt-e8b11ebf/internal/xds/server/
cd ~/wt-e8b11ebf && go test -race -count=1 -v -timeout=5m -run '^Test$/^Eval_' ./test/xds ./internal/xds/server   # -> verify/logs/c1_fixtures_target.log
```

```console
    eval_xds_server_interceptor_leak_test.go:1624: dependency order violation: filter closed at event #0 before retired interceptor closed at event #2 (events: [{kind:filter-close id:F1} {kind:icpt-close id:F1/I1} {kind:icpt-close id:F1/I2}])
    eval_xds_server_interceptor_leak_test.go:1190: Partial route conversion failure leaked interceptor: destroyed=1, want=2 (created=2)
    eval_xds_server_interceptor_leak_test.go:1374: Partial virtual host conversion failure leaked interceptor: destroyed=1, want=2 (created=2)
--- FAIL: Test (4.20s)
    --- FAIL: Test/Eval_ServerSideXDS_InterceptorBeforeFilterCloseOrder (0.03s)
    --- PASS: Test/Eval_ServerSideXDS_InterceptorLeak_MultiGenerationRDSUpdate (0.03s)
    --- PASS: Test/Eval_ServerSideXDS_InterceptorLeak_RDSUpdate (0.03s)
    --- PASS: Test/Eval_ServerSideXDS_InterceptorSwapOrder (0.03s)
    --- FAIL: Test/Eval_ServerSideXDS_PartialRouteFailure_ClosesInterceptors (2.02s)
    --- FAIL: Test/Eval_ServerSideXDS_PartialVirtualHostFailure_ClosesInterceptors (2.02s)
    --- PASS: Test/Eval_ServerSideXDS_ReplacementRPCWhileRetiredCloseBlocked (0.02s)
    --- PASS: Test/Eval_ServerSideXDS_StopWithAllowRPCAwaitingContextCancel (0.02s)
FAIL
FAIL	google.golang.org/grpc/test/xds	4.234s
    eval_rds_error_after_success_test.go:224: after route1 error: filter ref count = 2, want 1 (route1 hold not released)
    eval_rds_error_after_success_test.go:241: after route2 error: filter ref count = 2, want 0 (route2 hold not released)
--- FAIL: Test (2.06s)
    --- FAIL: Test/Eval_ServerSideXDS_ErrorUpdateReleasesServerFilterReferences (2.06s)
FAIL
FAIL	google.golang.org/grpc/internal/xds/server	2.087s
FAIL
```

The forced-shutdown fixture (`Eval_ServerSideXDS_StopWithAllowRPCAwaitingContextCancel`) passes: it only measures that `Stop()` returns, and its filter's `Close()` is a no-op, so it does not observe the close ordering shown above. The other fixture failures measure different behaviors (close ordering on route replacement, partial-construction cleanup, filter ref counts) and are not used for this verdict.

Impact reasoning: any xDS-enabled server that is stopped (forced or graceful) while RPCs are in flight has its HTTP filter `ServerFilter` instances — and, for RPCs already in their handler, their per-route interceptors — closed underneath those RPCs. With `GracefulStop()` the RPCs are explicitly meant to finish, and were observed to run ~1s and complete successfully after their filter resources were closed. Whether that is harmful depends on what a filter's `Close()` releases; the built-in observable effect here is only the ordering. It is identical on the base commit and the reference branch, so it is not a regression of this change.

## C2

Target: branch `evalon/grpc-go-xd-2f2a2d0c` (commit `b19fe7a0`). Verdict recorded: CONFIRMED.

Repro: `verify/repro/c2_held_allowrpc_blocks_replacement_test.go`. A filter tags every interceptor with the generation (`old`/`new`) of the RouteConfiguration that built it and logs every `AllowRPC`. The first `AllowRPC` on `old` is held open. The test then (1) pushes the `new` RouteConfiguration through the management server, (2) waits until the server has built the `new` interceptor (proof the update reached `handleRDSUpdate`), (3) dumps the goroutines inside `RouteAndProcess` / `handleRDSUpdate`, (4) sends six new RPCs with a 500ms deadline each while the old call is still held, (5) releases the old call and probes again.

```sh
cp verify/repro/c2_held_allowrpc_blocks_replacement_test.go ~/wt-2f2a2d0c/test/xds/
cd ~/wt-2f2a2d0c && go test -race -count=1 -v -run '^Test$/^VerifyC2_' ./test/xds     # -> verify/logs/c2_2f2a2d0c.log
```

```console
=== RUN   Test/VerifyC2_HeldAllowRPCBlocksReplacement
01 +   19.3ms Interceptor.Build(old)       server constructed the "old" route configuration
02 +   33.3ms AllowRPC(old).enter          HELD OPEN
03 +   33.4ms Update.submit                management server now serves the "new" route configuration
04 +   34.4ms Interceptor.Build(new)       server constructed the "new" route configuration
05 +  242.8ms Goroutines.whileHeld         
        goroutine 15 [sync.RWMutex.Lock]:
            sync.runtime_SemacquireRWMutex
            sync.(*RWMutex).Lock
            google.golang.org/grpc/internal/xds/server.(*safeRouteConfiguration).update
            google.golang.org/grpc/internal/xds/server.(*listenerWrapper).handleRDSUpdate
            google.golang.org/grpc/internal/xds/server.(*rdsWatcher).ResourceChanged
            google.golang.org/grpc/internal/xds/xdsclient/xdsresource.(*delegatingRouteConfigWatcher).ResourceChanged
            google.golang.org/grpc/internal/xds/clients/xdsclient.(*authority).handleADSResourceUpdate.func5
            google.golang.org/grpc/internal/xds/clients/internal/syncutil.(*CallbackSerializer).run
        goroutine 30 [chan receive]:
            google.golang.org/grpc/test/xds_test.(*vc2Interceptor).AllowRPC
            google.golang.org/grpc/internal/xds/server.(*interceptorList).AllowRPC
            google.golang.org/grpc/internal/xds/server.RouteAndProcess
            google.golang.org/grpc/xds.xdsUnaryInterceptor
            google.golang.org/grpc/interop/grpc_testing._TestService_EmptyCall_Handler
            google.golang.org/grpc.(*Server).processUnaryRPC
            google.golang.org/grpc.(*Server).handleStream
            google.golang.org/grpc.(*Server).serveStreams.func2.1
06 +  743.1ms Probe.whileHeld              #1 err=rpc error: code = DeadlineExceeded desc = context deadline exceeded reachedNewConfig=false
07 + 1245.8ms Probe.whileHeld              #2 err=rpc error: code = DeadlineExceeded desc = context deadline exceeded reachedNewConfig=false
08 + 1747.7ms Probe.whileHeld              #3 err=rpc error: code = DeadlineExceeded desc = context deadline exceeded reachedNewConfig=false
09 + 2250.0ms Probe.whileHeld              #4 err=rpc error: code = DeadlineExceeded desc = context deadline exceeded reachedNewConfig=false
10 + 2752.5ms Probe.whileHeld              #5 err=rpc error: code = DeadlineExceeded desc = context deadline exceeded reachedNewConfig=false
11 + 3254.8ms Probe.whileHeld              #6 err=rpc error: code = DeadlineExceeded desc = context deadline exceeded reachedNewConfig=false
12 + 3255.7ms Goroutines.afterProbes       
        (same writer blocked in safeRouteConfiguration.update, the held AllowRPC, plus 6 goroutines of the probe RPCs each blocked in sync.(*RWMutex).RLock <- RouteAndProcess; full dump in verify/logs/c2_2f2a2d0c.log)
13 + 3255.8ms Release                      letting the held AllowRPC(old) return
14 + 3255.8ms AllowRPC(old).return         held call returns
15 + 3255.9ms Interceptor.Close(old)       
16 + 3255.9ms AllowRPC(new)                new RPC routed through "new" configuration
17 + 3255.9ms AllowRPC(new)                new RPC routed through "new" configuration
18 + 3256.0ms AllowRPC(new)                new RPC routed through "new" configuration
19 + 3256.0ms AllowRPC(new)                new RPC routed through "new" configuration
20 + 3256.0ms AllowRPC(new)                new RPC routed through "new" configuration
21 + 3256.1ms AllowRPC(new)                new RPC routed through "new" configuration
22 + 3256.8ms AllowRPC(new)                new RPC routed through "new" configuration
23 + 3257.5ms Probe.afterRelease           #1 err=<nil> reachedNewConfig=true
OBSERVED replacement used by a newly routed RPC while old AllowRPC was held: false
OBSERVED replacement used by a newly routed RPC after old AllowRPC returned: true
--- PASS: Test (3.32s)
    --- PASS: Test/VerifyC2_HeldAllowRPCBlocksReplacement (3.31s)
ok  	google.golang.org/grpc/test/xds	4.373s
```

What this shows: the server received and constructed the replacement at +~40ms (`Interceptor.Build(new)`), but for the whole 3.2s that the old `AllowRPC` stayed open no RPC reached the `new` configuration. The goroutine dump correlates this with the synchronization paths named in the claim: the xDS callback goroutine is parked in `sync.(*RWMutex).Lock <- safeRouteConfiguration.update <- listenerWrapper.handleRDSUpdate <- rdsWatcher.ResourceChanged`, because `RouteAndProcess` holds `mu.RLock()` (deferred `RUnlock`) across `AllowRPC`. Newly routed RPCs did not fall back to the old configuration either: all six failed with `DeadlineExceeded`, and the second dump shows them parked in `sync.(*RWMutex).RLock <- RouteAndProcess` behind the pending writer. Within 0.2ms of the old call returning, the old interceptor was closed and RPCs were routed through `new`.

Control — same repro on the audited reference branch (shows the repro can refute):

```sh
cp verify/repro/c2_held_allowrpc_blocks_replacement_test.go ~/wt-perfect/test/xds/
cd ~/wt-perfect && go test -race -count=1 -v -run '^Test$/^VerifyC2_' ./test/xds      # -> verify/logs/c2_perfect.log
```

```console
08 +  231.3ms Probe.whileHeld              #1 err=<nil> reachedNewConfig=true
10 +  232.0ms Probe.whileHeld              #2 err=<nil> reachedNewConfig=true
12 +  232.3ms Probe.whileHeld              #3 err=<nil> reachedNewConfig=true
14 +  232.7ms Probe.whileHeld              #4 err=<nil> reachedNewConfig=true
16 +  233.0ms Probe.whileHeld              #5 err=<nil> reachedNewConfig=true
18 +  233.3ms Probe.whileHeld              #6 err=<nil> reachedNewConfig=true
OBSERVED replacement used by a newly routed RPC while old AllowRPC was held: true
OBSERVED replacement used by a newly routed RPC after old AllowRPC returned: true
--- PASS: Test (0.24s)
    --- PASS: Test/VerifyC2_HeldAllowRPCBlocksReplacement (0.24s)
ok  	google.golang.org/grpc/test/xds	1.273s
```

Related fixture run on the claim branch (same lock, shutdown path):

```sh
cp ~/eval_fixtures/tests/eval_xds_server_interceptor_leak_test.go ~/wt-2f2a2d0c/test/xds/
cd ~/wt-2f2a2d0c && go test -race -count=1 -v -timeout=120s -run '^Test$/^Eval_ServerSideXDS_(StopWithAllowRPCAwaitingContextCancel|ReplacementRPCWhileRetiredCloseBlocked)$' ./test/xds   # -> verify/logs/c2_fixtures_target.log
```

```console
    eval_xds_server_interceptor_leak_test.go:1836: Stop() did not return within 5s while AllowRPC was waiting for context cancellation (shutdown deadlock defect)
--- FAIL: Test (5.13s)
    --- PASS: Test/Eval_ServerSideXDS_ReplacementRPCWhileRetiredCloseBlocked (0.03s)
    --- FAIL: Test/Eval_ServerSideXDS_StopWithAllowRPCAwaitingContextCancel (5.09s)
FAIL
FAIL	google.golang.org/grpc/test/xds	5.168s
FAIL
```

Impact reasoning: an RDS update for a RouteConfiguration that is being served is the everyday path this task is about. On this branch, a single RPC whose filter `AllowRPC` is slow (e.g. an authorization filter doing I/O, or one waiting on its context) delays publication of the new configuration for as long as that call lasts, and — because a pending `RWMutex` writer blocks new readers — every new RPC on that filter chain stalls for the same duration instead of being served by either configuration (observed: 6 of 6 probes `DeadlineExceeded`). `handleRDSUpdate` is blocked while holding the listener wrapper's mutex and on the xDS client's callback serializer. The fixture run shows the same lock also makes `Stop()` hang when `AllowRPC` only returns on context cancellation (`Stop() did not return within 5s`). No workaround from user code other than keeping `AllowRPC` non-blocking.

## C3

Target: branch `evalon/grpc-go-xd-7ea015db` (commit `f6be604e`). Verdict recorded: CONFIRMED (holds for the two listener-wrapper unit tests; does not hold for the added integration test).

Naming drift: the claim cites `test/xds/xds_server_interceptor_leak_test.go`; on this branch the added integration test is `TestServerSideXDS_FilterStateRetention_AcrossRDSUpdates` in `test/xds/xds_server_filter_state_retention_test.go`. The unit tests are `TestListenerWrapper_RDSUpdatesReleaseSupersededInterceptors` and `TestListenerWrapper_RDSErrorReleasesSupersededResources` in `internal/xds/server/listener_wrapper_test.go`, both callers of `newListenerWrapperForTesting`.

Inspection (what the test files do):

```sh
cd ~/wt-7ea015db && grep -n "LocalTCPListener\|Cleanup\|defer\|lw.Close()" internal/xds/server/listener_wrapper_test.go
grep -n "defer stopServer()\|defer cc.Close()\|defer cancel()" test/xds/xds_server_filter_state_retention_test.go | tail -3
```

```console
84:	lis, err := testutils.LocalTCPListener()
86:		t.Fatalf("testutils.LocalTCPListener() failed: %v", err)
153:	if err := lw.Close(); err != nil {
186:	if err := lw.Close(); err != nil {
656:	defer stopServer()
738:	defer cancel()
747:	defer cc.Close()
```

`newListenerWrapperForTesting` opens a real TCP listener (`testutils.LocalTCPListener()`) and registers no `t.Cleanup`/`defer`; each unit test only calls `lw.Close()` near its end, after several `t.Fatalf` assertions. The integration test uses `defer stopServer()`, `defer cc.Close()`, `defer cancel()`.

Exercise: make the tests' own assertions fail by reintroducing the leak the tests are written to catch (one-line mutation, `verify/repro/c3_mutation_reintroduce_leak.patch`: `setUsableRouteConfiguration` no longer closes the superseded interceptors). The repros `verify/repro/c3_unit_listener_leak_test.go` and `verify/repro/c3_integration_cleanup_test.go` run each added test as a subtest and, after the subtest has fully exited (deferred functions and cleanups included), list this process's TCP sockets from `/proc/self/fd` + `/proc/net/tcp{,6}`: ports still in LISTEN state that were not open before, and the total TCP socket count. A still-open listener is additionally dialed. Sampled immediately at exit and again after up to 3s.

```sh
cp verify/repro/c3_unit_listener_leak_test.go ~/wt-7ea015db/internal/xds/server/
cp verify/repro/c3_integration_cleanup_test.go ~/wt-7ea015db/test/xds/
cd ~/wt-7ea015db
go test -count=1 -v -run '^TestVerifyC3$' ./internal/xds/server ./test/xds                # control, unmutated -> verify/logs/c3_control.log
git apply ~/repos/grpc-go/verify/repro/c3_mutation_reintroduce_leak.patch
go test -count=1 -v -run '^TestVerifyC3$' ./internal/xds/server ./test/xds                # -> verify/logs/c3_mutated.log
GOGC=off go test -count=1 -v -run '^TestVerifyC3$' ./internal/xds/server ./test/xds       # -> verify/logs/c3_mutated_gcoff.log
git checkout internal/xds/server/filter_chain_manager.go
```

```console
## c3_control.log
VC3 TestListenerWrapper_RDSUpdatesReleaseSupersededInterceptors      passed=true  atExit: listenersStillOpen=[] tcpSockets(before=0 after=0)
VC3 TestListenerWrapper_RDSUpdatesReleaseSupersededInterceptors      passed=true  settled(<=3s): listenersStillOpen=[] tcpSockets(before=0 after=0)
VC3 TestListenerWrapper_RDSErrorReleasesSupersededResources          passed=true  atExit: listenersStillOpen=[] tcpSockets(before=0 after=0)
VC3 TestListenerWrapper_RDSErrorReleasesSupersededResources          passed=true  settled(<=3s): listenersStillOpen=[] tcpSockets(before=0 after=0)
--- PASS: TestVerifyC3 (0.01s)
ok  	google.golang.org/grpc/internal/xds/server	0.020s
VC3 TestServerSideXDS_FilterStateRetention_AcrossRDSUpdates          passed=true  atExit: listenersStillOpen=[] tcpSockets(before=0 after=0)
VC3 TestServerSideXDS_FilterStateRetention_AcrossRDSUpdates          passed=true  settled(<=3s): listenersStillOpen=[] tcpSockets(before=0 after=0)
--- PASS: TestVerifyC3 (0.11s)
ok  	google.golang.org/grpc/test/xds	0.113s
## c3_mutated.log
    listener_wrapper_test.go:137: After 2 RDS updates, 6 interceptors are live, want 3
VC3 TestListenerWrapper_RDSUpdatesReleaseSupersededInterceptors      passed=false atExit: listenersStillOpen=[38241] tcpSockets(before=0 after=1)
VC3   dial still-open listener 127.0.0.1:38241 after the test exited: err=<nil>
VC3 TestListenerWrapper_RDSUpdatesReleaseSupersededInterceptors      passed=false settled(<=3s): listenersStillOpen=[] tcpSockets(before=0 after=0)
    listener_wrapper_test.go:180: After RDS error, 2 interceptors are live, want 0
VC3 TestListenerWrapper_RDSErrorReleasesSupersededResources          passed=false atExit: listenersStillOpen=[41127] tcpSockets(before=0 after=1)
VC3   dial still-open listener 127.0.0.1:41127 after the test exited: err=<nil>
VC3 TestListenerWrapper_RDSErrorReleasesSupersededResources          passed=false settled(<=3s): listenersStillOpen=[] tcpSockets(before=0 after=0)
--- FAIL: TestVerifyC3 (2.47s)
FAIL
FAIL	google.golang.org/grpc/internal/xds/server	2.477s
    xds_server_filter_state_retention_test.go:794: After 1 RDS updates, 2 interceptor instances are live (created: 2, destroyed: 0), want 1
VC3 TestServerSideXDS_FilterStateRetention_AcrossRDSUpdates          passed=false atExit: listenersStillOpen=[] tcpSockets(before=0 after=0)
VC3 TestServerSideXDS_FilterStateRetention_AcrossRDSUpdates          passed=false settled(<=3s): listenersStillOpen=[] tcpSockets(before=0 after=0)
--- FAIL: TestVerifyC3 (0.03s)
FAIL
FAIL	google.golang.org/grpc/test/xds	0.034s
FAIL
## c3_mutated_gcoff.log
    listener_wrapper_test.go:137: After 2 RDS updates, 6 interceptors are live, want 3
VC3 TestListenerWrapper_RDSUpdatesReleaseSupersededInterceptors      passed=false atExit: listenersStillOpen=[37033] tcpSockets(before=0 after=1)
VC3   dial still-open listener 127.0.0.1:37033 after the test exited: err=<nil>
VC3 TestListenerWrapper_RDSUpdatesReleaseSupersededInterceptors      passed=false settled(<=3s): listenersStillOpen=[37033] tcpSockets(before=0 after=1)
    listener_wrapper_test.go:180: After RDS error, 2 interceptors are live, want 0
VC3 TestListenerWrapper_RDSErrorReleasesSupersededResources          passed=false atExit: listenersStillOpen=[41571] tcpSockets(before=1 after=2)
VC3   dial still-open listener 127.0.0.1:41571 after the test exited: err=<nil>
VC3 TestListenerWrapper_RDSErrorReleasesSupersededResources          passed=false settled(<=3s): listenersStillOpen=[41571] tcpSockets(before=1 after=2)
--- FAIL: TestVerifyC3 (6.06s)
FAIL
FAIL	google.golang.org/grpc/internal/xds/server	6.071s
    xds_server_filter_state_retention_test.go:794: After 1 RDS updates, 2 interceptor instances are live (created: 2, destroyed: 0), want 1
VC3 TestServerSideXDS_FilterStateRetention_AcrossRDSUpdates          passed=false atExit: listenersStillOpen=[] tcpSockets(before=0 after=0)
VC3 TestServerSideXDS_FilterStateRetention_AcrossRDSUpdates          passed=false settled(<=3s): listenersStillOpen=[] tcpSockets(before=0 after=0)
--- FAIL: TestVerifyC3 (0.01s)
FAIL
FAIL	google.golang.org/grpc/test/xds	0.021s
FAIL
```

What this shows:

- Unit tests (`internal/xds/server`): with the assertion failing after `newListenerWrapperForTesting` and before `lw.Close()`, the TCP listener acquired by each test is still open after the failing test exits (`listenersStillOpen=[port]`) and still accepts a connection (`dial ... err=<nil>`). With the garbage collector disabled it stays open for the rest of the test process and accumulates across tests (`tcpSockets(before=1 after=2)`). With the default GC it was reclaimed within the 3s settle window — Go's `net` package closes unreferenced sockets from a GC finalizer (the probe's own allocations trigger GCs), so the leak lasts until an unscheduled GC or process exit rather than being released by registered cleanup. In the unmutated control both tests pass and nothing remains open.
- Integration test (`test/xds`): with its assertion failing at line 794, no listener and no TCP socket acquired by the test remains open at exit (`listenersStillOpen=[] tcpSockets(before=0 after=0)`): the deferred `stopServer()` / `cc.Close()` and the helpers' `t.Cleanup`s run on `t.Fatalf`. The claim's premise does not hold for this test's server and connection.

Impact reasoning: test hygiene only, and only on a failing run of the two unit tests: one loopback listening socket per failing test is left open until GC or process exit. No effect on production code, on passing runs, or on other tests' correctness was observed (the ports are ephemeral). The unit tests never use the listener for traffic.
