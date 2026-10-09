## Setup used for every section

```sh
cd ~/repos/grpc-go
git fetch origin grpc-go-xds-rds-interceptor-lifecycle-leak-perfect
git checkout -b verify/grpc-go-xds-rds-interceptor-lifecycle-leak-v-0b68211e origin/grpc-go-xds-rds-interceptor-lifecycle-leak-perfect   # HEAD 614cb739
# Claim-target branches live in a sibling repository; one worktree per branch:
git remote add claims https://github.com/kaitranntt-evals/grpc-go-xds-rds-interceptor-lifecycle-leak
for b in d256da9b 09480319 002f760c 2c20b0cb; do
  git fetch claims evalon/grpc-go-xd-$b && git worktree add -f ~/wt/$b FETCH_HEAD
done
# HEADs: d256da9b=46d687b9  09480319=1e9d655c  002f760c=0d5f4777  2c20b0cb=b9af5dc9   (go1.25.7 linux/amd64)
# Eval fixture (byte-exact from eval_tests.zip) provisioned where assessment puts it:
unzip eval_tests.zip   # -> tests/eval_xds_server_interceptor_leak_test.go
for b in d256da9b 09480319 002f760c 2c20b0cb; do cp tests/eval_xds_server_interceptor_leak_test.go ~/wt/$b/test/xds/; done
```

Files in this directory:

- `repro/c2_stop_deadlock_test.go`, `repro/c3_replacement_rpc_blocked_test.go` — self-contained end-to-end tests (real `xds.NewGRPCServer`, in-process management server, real client) for `test/xds`.
- `repro/c4_rds_error_after_success_test.go` — in-package test for `internal/xds/server` that performs the sequence the C4 branch's tests lack.
- `probes/c1_publication_trace_test.go` — in-package trace test for `internal/xds/server` (C1).
- `probes/c1_control_mutation.patch`, `probes/c4_mutation.patch` — throw-away production mutations used as controls; never committed to any branch.

Fixture baseline on the four claim branches (`go test -race -run '^Test$/^Eval_' ./test/xds -count=1 -timeout 5m -v`), for orientation only:

```console
== d256da9b
    --- FAIL: Test/Eval_ServerSideXDS_InterceptorBeforeFilterCloseOrder (0.12s)
    --- PASS: Test/Eval_ServerSideXDS_InterceptorLeak_MultiGenerationRDSUpdate (0.04s)
    --- PASS: Test/Eval_ServerSideXDS_InterceptorLeak_RDSUpdate (0.04s)
    --- PASS: Test/Eval_ServerSideXDS_InterceptorSwapOrder (0.03s)
    --- FAIL: Test/Eval_ServerSideXDS_PartialRouteFailure_ClosesInterceptors (2.03s)
    --- FAIL: Test/Eval_ServerSideXDS_PartialVirtualHostFailure_ClosesInterceptors (2.05s)
    --- PASS: Test/Eval_ServerSideXDS_ReplacementRPCWhileRetiredCloseBlocked (0.02s)
    --- PASS: Test/Eval_ServerSideXDS_StopWithAllowRPCAwaitingContextCancel (0.02s)
== 09480319
    --- PASS: (all other Eval_ tests)
    --- FAIL: Test/Eval_ServerSideXDS_StopWithAllowRPCAwaitingContextCancel (5.10s)
== 002f760c
    --- PASS: (all other Eval_ tests)
    --- FAIL: Test/Eval_ServerSideXDS_ReplacementRPCWhileRetiredCloseBlocked (3.02s)
    --- FAIL: Test/Eval_ServerSideXDS_StopWithAllowRPCAwaitingContextCancel (5.05s)
== 2c20b0cb
    --- FAIL: Test/Eval_ServerSideXDS_InterceptorBeforeFilterCloseOrder (0.07s)
    --- FAIL: Test/Eval_ServerSideXDS_PartialRouteFailure_ClosesInterceptors (2.03s)
    --- FAIL: Test/Eval_ServerSideXDS_PartialVirtualHostFailure_ClosesInterceptors (2.04s)
    --- PASS: (all other Eval_ tests)
```

The failures on d256da9b / 2c20b0cb (filter-close ordering, partial-construction failure) are outside the five claims and were not adjudicated.

## C1

Target: `evalon/grpc-go-xd-d256da9b` (HEAD 46d687b9). Verdict: **REFUTED** (both parts).

Code under test (`internal/xds/server/filter_chain_manager.go` on that branch) — the only in-place replacement path, used by `handleRDSUpdate`:

```go
func (fc *filterChain) updateUsableRouteConfiguration(urc *usableRouteConfiguration) {
	old := fc.usableRouteConfiguration.Swap(urc)
	old.close()
}
```

### Trace through the production update path

`probes/c1_publication_trace_test.go` builds a real `listenerWrapper` with a real xDS client and management server, pushes 3 in-place RouteConfiguration updates (so `rdsWatcher.ResourceChanged` → `listenerWrapper.handleRDSUpdate` runs), and makes every interceptor `Close()` record the value of `fc.usableRouteConfiguration.Load()` at the instant it is called. "Replacement complete" is detected by acquiring `lw.mu`, which `handleRDSUpdate` holds across the whole swap+retire.

```sh
cd ~/wt/d256da9b
cp ~/repos/grpc-go/verify/probes/c1_publication_trace_test.go internal/xds/server/
go test -race -v -count=1 -run '^Test$/^Verify_C1_' ./internal/xds/server 2>&1 | grep -E "TRACE|EVENT|^\s*--- |^(ok|FAIL)"
```

```console
TRACE gen1 published routePointer=0xc00051d240 closeCounts=[0]
TRACE replacement -> gen2 complete: published routePointer=0xc00006d240 closeCounts=[1 0]
TRACE   interceptor#1 Close() saw routePointer == gen2 (replacement already published)
TRACE replacement -> gen3 complete: published routePointer=0xc00051d6c0 closeCounts=[1 1 0]
TRACE   interceptor#2 Close() saw routePointer == gen3 (replacement already published)
TRACE replacement -> gen4 complete: published routePointer=0xc0001c0580 closeCounts=[1 1 1 0]
TRACE   interceptor#3 Close() saw routePointer == gen4 (replacement already published)
TRACE listener Close(): closeCounts before=[1 1 1 0] after=[1 1 1 1]
TRACE second listener Close(): closeCounts=[1 1 1 2]
EVENT create interceptor#1
EVENT create interceptor#2
EVENT close  interceptor#1 (call 1) routePointer=0xc00006d240
EVENT create interceptor#3
EVENT close  interceptor#2 (call 1) routePointer=0xc00051d6c0
EVENT create interceptor#4
EVENT close  interceptor#3 (call 1) routePointer=0xc0001c0580
EVENT close  interceptor#4 (call 1) routePointer=0xc0001c0580
EVENT close  interceptor#4 (call 2) routePointer=0xc0001c0580
    --- PASS: Test/Verify_C1_PublicationOrderAndCloseCounts (0.13s)
ok  	google.golang.org/grpc/internal/xds/server	1.196s
```

- *publication_order* — REFUTED. At the first retirement action for each generation (the retired interceptor's `Close()`), the route pointer already held the replacement (interceptor#1 saw gen2's pointer `0xc00006d240`, #2 saw gen3's, #3 saw gen4's). Publication precedes retirement.
- *retired_interceptor_lifecycle* — REFUTED. When each replacement completed, every retired interceptor had exactly one `Close()` (`[1 0]`, `[1 1 0]`, `[1 1 1 0]`); shutdown left the retired counts unchanged (`before=[1 1 1 0] after=[1 1 1 1]`).

Side observation, outside the claim (it concerns the *live*, never-retired interceptor and a direct double `Close()` of the listener wrapper, which `grpc.Server.Stop` does not do): a second `listenerWrapper.Close()` closed the live interceptor#4 again (`[1 1 1 2]`). Retired interceptors stayed at 1.

### Control: the probe does detect the claimed defect

```sh
cd ~/wt/d256da9b && git apply ~/repos/grpc-go/verify/probes/c1_control_mutation.patch   # Load().close() then Store(urc)
go test -race -v -count=1 -run '^Test$/^Verify_C1_' ./internal/xds/server 2>&1 | grep -E "BEFORE|^\s*--- |^(ok|FAIL)"
git checkout -- internal/xds/server/filter_chain_manager.go
```

```console
    c1_publication_trace_test.go:218: interceptor#1 Close() saw routePointer == gen1: retirement started BEFORE publication
    c1_publication_trace_test.go:218: interceptor#2 Close() saw routePointer == gen2: retirement started BEFORE publication
    c1_publication_trace_test.go:218: interceptor#3 Close() saw routePointer == gen3: retirement started BEFORE publication
    --- FAIL: Test/Verify_C1_PublicationOrderAndCloseCounts (0.11s)
FAIL	google.golang.org/grpc/internal/xds/server	0.170s
```

### End-to-end through a real `xds.NewGRPCServer` and `Server.Stop()`

```sh
cd ~/wt/d256da9b   # eval fixture already in test/xds
cp ~/repos/grpc-go/verify/repro/c3_replacement_rpc_blocked_test.go test/xds/
go test -race -v -count=1 -run '^Test$/^(Verify_C3_|Eval_ServerSideXDS_InterceptorSwapOrder$|Eval_ServerSideXDS_InterceptorLeak_MultiGenerationRDSUpdate$|Eval_ServerSideXDS_ReplacementRPCWhileRetiredCloseBlocked$)' ./test/xds 2>&1 | grep -E "TRACE|^\s*--- |^(ok|FAIL)|STALL"
```

```console
TRACE initial RPC served by interceptor "path1"
TRACE t=0s        retired interceptor path1 Close() entered and held; issuing new RPC
TRACE t=6ms new RPC completed while retired Close() still blocked; served by "path2"
    --- PASS: Test/Eval_ServerSideXDS_InterceptorLeak_MultiGenerationRDSUpdate (0.08s)
    --- PASS: Test/Eval_ServerSideXDS_InterceptorSwapOrder (0.03s)
    --- PASS: Test/Eval_ServerSideXDS_ReplacementRPCWhileRetiredCloseBlocked (0.02s)
    --- PASS: Test/Verify_C3_NewRPCWhileRetiredCloseBlocked (0.03s)
ok  	google.golang.org/grpc/test/xds	1.230s
```

A live RPC is served by the replacement's interceptor while the retired one's `Close()` has not yet returned (publication first), and the fixture's `InterceptorSwapOrder` / `MultiGenerationRDSUpdate` (which assert each of the created interceptors is closed exactly once after replacement plus `stopServer()`) pass.

## C2

Target: `evalon/grpc-go-xd-09480319` (HEAD 1e9d655c). Verdict: **CONFIRMED** (both parts).

Relevant code on that branch (`internal/xds/server/routing.go`, `filter_chain_manager.go`):

```go
func (r *routingConfig) update(rc *usableRouteConfiguration) {
	r.mu.Lock()          // waits for every in-flight RouteAndProcess
	...
func RouteAndProcess(ctx context.Context) error {
	...
	cw.urc.mu.RLock()
	defer cw.urc.mu.RUnlock()   // held through interceptor AllowRPC
	...
func (fc *filterChain) stop() {
	fc.usableRouteConfiguration.update(&usableRouteConfiguration{err: errors.New("filter chain stopped")})
}
```

and `grpc.(*Server).stop` (root `server.go:1949-1964`) closes listeners *before* it closes transports:

```go
	s.mu.Lock()
	s.closeListenersLocked()          // -> listenerWrapper.Close -> filterChainManager.stop
	s.mu.Unlock()
	s.serveWG.Wait()
	...
		s.closeServerTransportsLocked()   // what would cancel the RPC ctx
```

### Run

`repro/c2_stop_deadlock_test.go`: a real xDS-enabled server; the interceptor's `AllowRPC` blocks on `<-ctx.Done()` (the server-side RPC context only). The test calls `Server.Stop()`, holds for 8 s, dumps goroutines, then cancels the RPC from the client.

```sh
cd ~/wt/09480319
cp ~/repos/grpc-go/verify/repro/c2_stop_deadlock_test.go test/xds/
go test -race -v -count=1 -run '^Test$/^Verify_C2_' ./test/xds 2>&1 | grep -v "tlogger.go\|logging.go"
```

```console
    c2_stop_deadlock_test.go:239: TRACE t=0s        AllowRPC entered and waiting on RPC ctx; calling Server.Stop()
    c2_stop_deadlock_test.go:260: TRACE t=8s        Stop() still blocked; AllowRPC still waiting (its RPC ctx was never cancelled)
    c2_stop_deadlock_test.go:262: GOROUTINES at t=8s (Stop / listener cleanup / routing):
        goroutine 33 [chan receive]:
        google.golang.org/grpc/test/xds_test.s.TestVerify_C2_StopWhileAllowRPCAwaitsContextCancel.func1(...)
        	/home/ubuntu/wt/09480319/test/xds/c2_stop_deadlock_test.go:221 +0x5e
        google.golang.org/grpc/test/xds_test.(*vc2Interceptor).AllowRPC(...)
        	/home/ubuntu/wt/09480319/test/xds/c2_stop_deadlock_test.go:107 +0x94
        google.golang.org/grpc/internal/xds/server.(*interceptorList).AllowRPC(...)
        	/home/ubuntu/wt/09480319/internal/xds/server/filter_chain_manager.go:510 +0xa2
        google.golang.org/grpc/internal/xds/server.RouteAndProcess(...)
        	/home/ubuntu/wt/09480319/internal/xds/server/routing.go:116 +0xba3
        google.golang.org/grpc/xds.xdsUnaryInterceptor(...)
        	/home/ubuntu/wt/09480319/xds/server.go:236 +0x47
        ...
        goroutine 130 [sync.RWMutex.Lock]:
        sync.runtime_SemacquireRWMutex(0xc000110974?, 0x1?, 0x14928f8?)
        sync.(*RWMutex).Lock(0xc000110960)
        	/usr/local/go/src/sync/rwmutex.go:155 +0x89
        google.golang.org/grpc/internal/xds/server.(*routingConfig).update(0xc000110960, 0xc0005ed180)
        	/home/ubuntu/wt/09480319/internal/xds/server/routing.go:46 +0x32
        google.golang.org/grpc/internal/xds/server.(*filterChain).stop(...)
        	/home/ubuntu/wt/09480319/internal/xds/server/filter_chain_manager.go:394
        google.golang.org/grpc/internal/xds/server.(*filterChainManager).stop(0xc000516600)
        	/home/ubuntu/wt/09480319/internal/xds/server/filter_chain_manager.go:125 +0x78
        google.golang.org/grpc/internal/xds/server.(*listenerWrapper).Close(0xc00029acc0)
        	/home/ubuntu/wt/09480319/internal/xds/server/listener_wrapper.go:370 +0x13e
        google.golang.org/grpc.(*listenSocket).Close(0xc0004a20a8)
        	/home/ubuntu/wt/09480319/server.go:862 +0x4b
        google.golang.org/grpc.(*Server).closeListenersLocked(...)
        	/home/ubuntu/wt/09480319/server.go:2014
        google.golang.org/grpc.(*Server).stop(0xc00027cd88, 0x0)
        	/home/ubuntu/wt/09480319/server.go:1952 +0x25a
        google.golang.org/grpc.(*Server).Stop(0xc00027cd88)
        	/home/ubuntu/wt/09480319/server.go:1936 +0x29
        google.golang.org/grpc/xds.(*GRPCServer).Stop(0xc000517080)
        	/home/ubuntu/wt/09480319/xds/server.go:216 +0x8f
    c2_stop_deadlock_test.go:267: TRACE t=8.042s client cancelled the RPC
    c2_stop_deadlock_test.go:270: TRACE t=8.042s AllowRPC returned
    c2_stop_deadlock_test.go:276: TRACE t=8.043s Stop() returned only after AllowRPC finished
    c2_stop_deadlock_test.go:280: DEADLOCK: Server.Stop() did not return for 8s while AllowRPC waited for RPC context cancellation
    --- FAIL: Test/Verify_C2_StopWhileAllowRPCAwaitsContextCancel (8.16s)
FAIL	google.golang.org/grpc/test/xds	8.219s
```

- *shutdown_wait_dependency* — CONFIRMED. Goroutine 130 is `Server.Stop` parked in `routingConfig.update` → `RWMutex.Lock` at `server.go:1952` (`closeListenersLocked`), i.e. before `closeServerTransportsLocked` at `server.go:1963` can run; goroutine 33 holds the read lock inside `RouteAndProcess` → `AllowRPC`, waiting for the context that only transport close (or the client) would cancel.
- *production_trigger* — CONFIRMED. The path is entered by a plain `xds.(*GRPCServer).Stop()` on a real server with one in-flight RPC; `Stop()` stayed blocked for the whole 8 s hold and returned 1 ms after the client itself cancelled the RPC. With a client that does not cancel, the wait is unbounded.

The eval fixture measures the same thing and agrees: `--- FAIL: Test/Eval_ServerSideXDS_StopWithAllowRPCAwaitingContextCancel (5.10s)` on this branch.

### Control (same test on a healthy implementation, `grpc-go-xds-rds-interceptor-lifecycle-leak-perfect` @ 614cb739)

```console
    c2_stop_deadlock_test.go:239: TRACE t=0s        AllowRPC entered and waiting on RPC ctx; calling Server.Stop()
    c2_stop_deadlock_test.go:245: TRACE t=2ms Stop() returned (no deadlock)
    c2_stop_deadlock_test.go:248: TRACE t=1ms AllowRPC observed ctx cancellation (cancelled by server shutdown)
    --- PASS: Test/Verify_C2_StopWhileAllowRPCAwaitsContextCancel (0.09s)
```

### Impact reasoning

`Stop()` is the forced-shutdown API ("immediately closes all open connections"). On this branch it cannot return while any RPC is inside an HTTP-filter `AllowRPC` that waits on its context — and more generally it waits for every in-flight `RouteAndProcess`. A filter that blocks until its RPC is cancelled is a legitimate pattern (e.g. waiting on an external authorization call bound to the RPC context); a client that is slow or gone-but-not-reset keeps the process from shutting down. `GracefulStop()` takes the same `closeListenersLocked` first step, so it is exposed to the same wait. The same lock is also taken by every in-place RDS update (`routingConfig.update`), so the xDS callback goroutine can be held by one slow `AllowRPC`. No caller-side workaround other than cancelling RPCs from the client.

## C3

Target: `evalon/grpc-go-xd-002f760c` (HEAD 0d5f4777). Verdict: **CONFIRMED** (both parts).

Relevant code on that branch:

```go
// filter_chain_manager.go
func (fc *filterChain) updateRouteConfiguration(rc *usableRouteConfiguration) {
	fc.routingMu.Lock()
	defer fc.routingMu.Unlock()
	fc.usableRouteConfiguration.Swap(rc).close()   // line 411: publish, then run retired Close() under the write lock
}
// routing.go
	cw.filterChain.routingMu.RLock()               // line 46
	defer cw.filterChain.routingMu.RUnlock()
	rc := cw.urc.Load()
```

### Run

`repro/c3_replacement_rpc_blocked_test.go`: real xDS-enabled server; after one RPC on the original route ("path1" interceptor), the route config is replaced in place ("path2"); the retired interceptor's `Close()` is held. A new RPC is then issued with no deadline shorter than the hold.

```sh
cd ~/wt/002f760c
cp ~/repos/grpc-go/verify/repro/c3_replacement_rpc_blocked_test.go test/xds/
go test -race -v -count=1 -run '^Test$/^Verify_C3_' ./test/xds 2>&1 | grep -v "tlogger.go\|logging.go"
```

```console
    c3_replacement_rpc_blocked_test.go:241: TRACE initial RPC served by interceptor "path1"
    c3_replacement_rpc_blocked_test.go:250: TRACE t=0s        retired interceptor path1 Close() entered and held; issuing new RPC
    c3_replacement_rpc_blocked_test.go:251: GOROUTINE performing the replacement (shows whether the route pointer swap already ran):
        goroutine 23 [chan receive]:
        google.golang.org/grpc/test/xds_test.(*vc3Interceptor).Close(0xc0000242d0)
        	/home/ubuntu/wt/002f760c/test/xds/c3_replacement_rpc_blocked_test.go:108 +0x82
        google.golang.org/grpc/internal/xds/server.(*interceptorList).Close(0xc000024300)
        	/home/ubuntu/wt/002f760c/internal/xds/server/filter_chain_manager.go:535 +0x79
        google.golang.org/grpc/internal/xds/server.virtualHostWithInterceptors.close(...)
        	/home/ubuntu/wt/002f760c/internal/xds/server/filter_chain_manager.go:208 +0x9d
        google.golang.org/grpc/internal/xds/server.(*filterChain).updateRouteConfiguration.(*usableRouteConfiguration).close.func1()
        	/home/ubuntu/wt/002f760c/internal/xds/server/filter_chain_manager.go:190 +0xfa
        ...
        google.golang.org/grpc/internal/xds/server.(*filterChain).updateRouteConfiguration(0xc000111080, 0xc00089c600)
        	/home/ubuntu/wt/002f760c/internal/xds/server/filter_chain_manager.go:411 +0x105
        google.golang.org/grpc/internal/xds/server.(*listenerWrapper).handleRDSUpdate(...)
        	/home/ubuntu/wt/002f760c/internal/xds/server/listener_wrapper.go:220 +0x4f6
        google.golang.org/grpc/internal/xds/server.(*rdsWatcher).ResourceChanged(...)
        	/home/ubuntu/wt/002f760c/internal/xds/server/rds_handler.go:153 +0x2fe
    c3_replacement_rpc_blocked_test.go:272: TRACE t=5s        new RPC still pending; retired Close() still blocked
    c3_replacement_rpc_blocked_test.go:273: GOROUTINES at t=5s (routing / replacement):
        goroutine 23 [chan receive]:   (unchanged: still inside the retired Close() under updateRouteConfiguration:411)
        goroutine 115 [sync.RWMutex.RLock]:
        sync.runtime_SemacquireRWMutexR(0xc0001110c8?, 0x1?, 0xc000930ea0?)
        sync.(*RWMutex).RLock(0xc0001110b8)
        	/usr/local/go/src/sync/rwmutex.go:74 +0x5b
        google.golang.org/grpc/internal/xds/server.RouteAndProcess({0x1f944c8, 0xc00062a300})
        	/home/ubuntu/wt/002f760c/internal/xds/server/routing.go:46 +0xc5
        google.golang.org/grpc/xds.xdsUnaryInterceptor(...)
        	/home/ubuntu/wt/002f760c/xds/server.go:236 +0x47
        google.golang.org/grpc/interop/grpc_testing._TestService_EmptyCall_Handler(...)
    c3_replacement_rpc_blocked_test.go:276: TRACE t=5.004s released retired Close()
    c3_replacement_rpc_blocked_test.go:282: TRACE t=5.005s new RPC completed 0s after release; served by "path2"
    c3_replacement_rpc_blocked_test.go:288: STALL: new RPC could not use the published replacement route configuration until the retired interceptor's Close() returned
    --- FAIL: Test/Verify_C3_NewRPCWhileRetiredCloseBlocked (5.14s)
FAIL	google.golang.org/grpc/test/xds	5.223s
```

- *replacement_access_dependency* — CONFIRMED. The replacement is already published (goroutine 23 is executing the `.close()` half of `Swap(rc).close()` at `filter_chain_manager.go:411`, i.e. after the `Swap`), yet the request path (goroutine 115) is parked on `routingMu.RLock` at `routing.go:46`, which is released only when the retired `Close()` returns.
- *production_trigger* — CONFIRMED. A new RPC arriving through the real server after publication and before the retired `Close()` returned sat pending for the full 5 s hold and completed ~1 ms after release, served by the replacement ("path2").

The eval fixture agrees: `--- FAIL: Test/Eval_ServerSideXDS_ReplacementRPCWhileRetiredCloseBlocked (3.02s)` on this branch.

### Control (same test on `grpc-go-xds-rds-interceptor-lifecycle-leak-perfect` @ 614cb739, and on d256da9b)

```console
    c3_replacement_rpc_blocked_test.go:250: TRACE t=0s        retired interceptor path1 Close() entered and held; issuing new RPC
    c3_replacement_rpc_blocked_test.go:269: TRACE t=3ms new RPC completed while retired Close() still blocked; served by "path2"
    --- PASS: Test/Verify_C3_NewRPCWhileRetiredCloseBlocked (0.02s)
```

### Impact reasoning

Every RouteConfiguration update on a serving listener stalls *all* new RPCs on the affected filter chain for as long as the retired interceptors take to close; `Close()` on a filter that flushes or tears down a connection (or simply many routes' worth of interceptors) directly becomes request latency, and a `Close()` that hangs becomes a full traffic outage on that chain even though the new configuration is already installed. The original task explicitly required release "without dropping requests on active listeners". The same write lock is also taken in `filterChain.stop()`, which is why this branch additionally fails the fixture's Stop test (`Eval_ServerSideXDS_StopWithAllowRPCAwaitingContextCancel (5.05s)` FAIL) — not part of this claim, recorded for the maintainer.

## C4

Target: `evalon/grpc-go-xd-2c20b0cb` (HEAD b9af5dc9). Verdict: **CONFIRMED**.

### Inspection of the committed tests

```sh
cd ~/wt/2c20b0cb
git diff --name-only 4ee6ac46 HEAD -- '*_test.go'
grep -n "func (s) Test" internal/xds/server/listener_wrapper_test.go test/xds/xds_server_route_config_update_test.go
git grep -n -e 'handleRDSUpdate' -e 'rdsWatcherUpdate{' -e 'ResourceError' -e 'handleRouteUpdate' HEAD -- internal/xds/server/listener_wrapper_test.go test/xds/xds_server_route_config_update_test.go; echo "exit=$?"
git grep -n -e 'usableRouteConfiguration{err' HEAD -- 'internal/xds/server/*_test.go' 'test/xds/*_test.go'
grep -rn "TestHandleRDSUpdate\|ErrorAfterSuccess" --include=*.go .
```

```console
internal/xds/server/listener_wrapper_test.go
test/xds/xds_server_route_config_update_test.go
internal/xds/server/listener_wrapper_test.go:191:func (s) TestListenerWrapper_RouteConfigUpdate_ReleasesSupersededResources(t *testing.T) {
internal/xds/server/listener_wrapper_test.go:307:func (s) TestFilterChainManager_Stop_ReleasesFiltersWithErroredRouteConfig(t *testing.T) {
test/xds/xds_server_route_config_update_test.go:93:func (s) TestServerSideXDS_RouteConfigUpdate_ReleasesSupersededResources(t *testing.T) {
exit=1                                   <- no committed test references handleRDSUpdate / rdsWatcherUpdate{ / ResourceError
HEAD:internal/xds/server/listener_wrapper_test.go:346:	fc.usableRouteConfiguration.Swap(&usableRouteConfiguration{err: fmt.Errorf("resource not found")}).close()
(no output for TestHandleRDSUpdate / ErrorAfterSuccess: the test named in the claim does not exist on this branch)
```

The solution commits three tests. Two only ever push successful RouteConfiguration updates. The third, `TestFilterChainManager_Stop_ReleasesFiltersWithErroredRouteConfig`, never builds a `listenerWrapper`; it "simulates" the error by swapping an error configuration straight into the atomic pointer (line 346 above) — exactly the direct swap the claim says does not count.

### Demonstration: the error-after-success branch of `handleRDSUpdate` can be removed with every committed test green

`probes/c4_mutation.patch`:

```diff
@@ -216,7 +216,8 @@ func (l *listenerWrapper) handleRDSUpdate(routeName string, rcu rdsWatcherUpdate
 			if rcu.err != nil && rcu.data == nil { // Either NACK before update, or resource not found triggers this conditional.
-				urc = &usableRouteConfiguration{err: rcu.err}
+				// VERIFY MUTATION (C4): ignore a no-data RDS error on an active chain; keep serving the stale configuration.
+				continue
 			} else {
```

`repro/c4_rds_error_after_success_test.go` is the missing test: successful RDS install through the real xDS client, then a no-data error delivered through `listenerWrapper.handleRDSUpdate` (the same two statements `rdsWatcher.ResourceError` executes), asserting the error configuration is installed and the superseded interceptor closed.

```sh
cd ~/wt/2c20b0cb && rm -f test/xds/eval_xds_server_interceptor_leak_test.go   # committed tests only
cp ~/repos/grpc-go/verify/repro/c4_rds_error_after_success_test.go internal/xds/server/
R1='^Test$/^(ListenerWrapper_RouteConfigUpdate_ReleasesSupersededResources|FilterChainManager_Stop_ReleasesFiltersWithErroredRouteConfig|Verify_C4_RDSNoDataErrorAfterSuccess)$'
R2='^Test$/^ServerSideXDS_RouteConfigUpdate_ReleasesSupersededResources$'
go test -race -v -count=1 -run "$R1" ./internal/xds/server; go test -race -v -count=1 -run "$R2" ./test/xds      # unmutated
git apply ~/repos/grpc-go/verify/probes/c4_mutation.patch
go test -race -v -count=1 -run "$R1" ./internal/xds/server; go test -race -v -count=1 -run "$R2" ./test/xds      # mutated
go test -race -count=1 ./internal/xds/server/...; go test -race -count=1 -timeout=5m ./test/xds                   # mutated, whole packages
git checkout -- internal/xds/server/listener_wrapper.go
```

Unmutated:

```console
    c4_rds_error_after_success_test.go:138: TRACE successful RDS update installed: created=1 closed=0
    c4_rds_error_after_success_test.go:148: TRACE after no-data RDS error: installed err=verify: route resource does not exist vhs=0 created=1 closed=1
    --- PASS: Test/FilterChainManager_Stop_ReleasesFiltersWithErroredRouteConfig (0.01s)
    --- PASS: Test/ListenerWrapper_RouteConfigUpdate_ReleasesSupersededResources (0.07s)
    --- PASS: Test/Verify_C4_RDSNoDataErrorAfterSuccess (0.06s)
ok  	google.golang.org/grpc/internal/xds/server	1.222s
    --- PASS: Test/ServerSideXDS_RouteConfigUpdate_ReleasesSupersededResources (0.21s)
ok  	google.golang.org/grpc/test/xds	1.295s
```

Mutated:

```console
    c4_rds_error_after_success_test.go:148: TRACE after no-data RDS error: installed err=<nil> vhs=1 created=1 closed=0
    c4_rds_error_after_success_test.go:150: after no-data RDS error: installed route configuration err = <nil>, want verify: route resource does not exist (error configuration NOT installed)
    c4_rds_error_after_success_test.go:153: after no-data RDS error: 0 interceptors closed, want 1 (superseded configuration not released)
    --- PASS: Test/FilterChainManager_Stop_ReleasesFiltersWithErroredRouteConfig (0.02s)
    --- PASS: Test/ListenerWrapper_RouteConfigUpdate_ReleasesSupersededResources (0.13s)
    --- FAIL: Test/Verify_C4_RDSNoDataErrorAfterSuccess (0.01s)
FAIL	google.golang.org/grpc/internal/xds/server	0.201s
    --- PASS: Test/ServerSideXDS_RouteConfigUpdate_ReleasesSupersededResources (0.12s)
ok  	google.golang.org/grpc/test/xds	1.190s
# whole packages, mutated:
    --- FAIL: Test/Verify_C4_RDSNoDataErrorAfterSuccess (0.01s)     <- the only failure in ./internal/xds/server/...
FAIL	google.golang.org/grpc/internal/xds/server	0.298s
ok  	google.golang.org/grpc/test/xds	19.260s
```

With the error-after-success handling deleted, all three committed tests — and every other test in `internal/xds/server` and `test/xds` on the branch — stay green; only the added repro notices. (An earlier attempt at this run used a hand-written patch that `git apply` rejected as corrupt, so its "mutated" results were really unmutated and were discarded; the patch in `probes/` is the `git diff` of the mutation that produced the output above.)

### Impact reasoning

The branch's production code handles this sequence correctly today (unmutated run: error installed, superseded interceptor closed). The problem is coverage only: the branch rewrote this exact branch of `handleRDSUpdate` (it now shares the `Swap(urc).close()` tail with the success path), and a regression there — stale routes kept serving after the control plane reports the route resource gone, or the superseded interceptors leaking, which is the very leak the task is about — would ship with a green suite.

## C5

Target: the audited branch `grpc-go-xds-rds-interceptor-lifecycle-leak-perfect` (HEAD 614cb739). Verdict: **REFUTED**.

```sh
cd ~/repos/grpc-go && git rev-parse HEAD
gofmt -s -d -l internal/xds/server test/xds; echo "exit=$? bytes_of_output=$(gofmt -s -d -l internal/xds/server test/xds 2>&1 | wc -c)"
find internal/xds/server test/xds -name '*.go' | wc -l
```

```console
614cb7398c43bf270878ac15f34b68f5a9a4159d
exit=0 bytes_of_output=0
28
```

The command completes successfully over 28 Go files with no output. Positive control that this `gofmt` does report with these flags (`scripts/vet.sh:97` uses the same `gofmt -s -d -l`):

```sh
mkdir -p /tmp/gofmtctl && printf 'package x\n\nvar a = []int{\n\tint(1)}\nvar b = a[0:len(a)]\n' > /tmp/gofmtctl/x.go && gofmt -s -d -l /tmp/gofmtctl
```

```console
/tmp/gofmtctl/x.go
diff /tmp/gofmtctl/x.go.orig /tmp/gofmtctl/x.go
--- /tmp/gofmtctl/x.go.orig
+++ /tmp/gofmtctl/x.go
@@ -2,4 +2,4 @@
 
 var a = []int{
 	int(1)}
-var b = a[0:len(a)]
+var b = a[0:]
```
