Observations only; verdict report is delivered separately. Claim branches live in the repository
`kaitranntt-evals/grpc-go-xds-rds-interceptor-lifecycle-leak` (not on `origin`), fetched via a second remote:

```sh
cd ~/repos/grpc-go
git remote add claims https://github.com/kaitranntt-evals/grpc-go-xds-rds-interceptor-lifecycle-leak
for b in ec59ed30 6f67b5d7 eaa55089 eb3713e8 b1537b5b 6826d1fb bd84b6f8; do
  git fetch claims evalon/grpc-go-xd-$b:refs/remotes/claims/$b && git worktree add ~/wt/$b claims/$b
done
```

Heads audited: ec59ed30=d1c28a48, 6f67b5d7=02334d87, eaa55089=0c970988, eb3713e8=d8366628, b1537b5b=4676ada0, 6826d1fb=93e14263, bd84b6f8=b900a9eb. Base: 4ee6ac46. Go 1.25.7 linux/amd64.

## C1

Claim: the added lifecycle tests assert retirement/shutdown destruction counts without establishing completion of the cleanup. Observed: on none of the six branches.

### Method

For each branch, three things were run against the branch's own added e2e test in `test/xds` (script: `verify/repro/c1_probe.sh <worktree> <test> <unit-regex>`):

1. the unmodified test, `-race -count=20`, plus the added unit lifecycle tests in `internal/xds/server`, `-race -count=50`;
2. a trace run: `verify/repro/c1_close_probe_test.go.txt` is dropped into `test/xds` and one call `c1Probe(...)` is inserted (by `sed`) at the top of the shared test helpers `trackingInterceptor.Close` / `trackingHTTPFilterBuilder.Close`, i.e. *before* the counters `interceptorsDestroyed` / `filtersDestroyed` move. With `C1_TRACE=1` it prints which goroutine and call path performs each cleanup;
3. a delay run: `C1_DELAY_MS=150` makes every Close sleep 150ms before the counter moves. Any destruction-count assertion that is not ordered after the cleanup by a lock, synchronous call, completion event or polling then reads a stale count and fails.

Production code was never modified for these runs. Positive controls (`verify/repro/c1_controls.sh`) then make the production cleanup asynchronous (`go ...close()`) to show the delay run does detect an unsynchronised assertion.

Shared facts used below (read from source, then confirmed by the traces): `xds.GRPCServer.Stop` -> `grpc.Server.Stop` calls `closeListenersLocked` (-> `listenerWrapper.Close` -> `filterChainManager.stop`) and `closeServerTransportsLocked` (-> `http2Server.Close` -> `t.conn.Close()`) on the calling goroutine, and waits for `len(s.conns)==0` before returning; `xdsUnaryInterceptor` calls `server.RouteAndProcess` (whose deferred release/unref/RUnlock runs on return) before invoking the handler, so a unary RPC that has returned to the client no longer holds a route-configuration reference.

### Per-branch result summary

| Branch | Retirement assertion ordered after cleanup by | Shutdown assertion ordered after cleanup by | Delay run |
|---|---|---|---|
| [evalon/grpc-go-xd-ec59ed30](https://github.com/kaitranntt-evals/grpc-go-xds-rds-interceptor-lifecycle-leak/tree/evalon/grpc-go-xd-ec59ed30) | `routingConfig.update` swaps and runs `old.close()` inside one `mu.Lock()` section; RPCs read the config under `mu.RLock()`, so an RPC that reports the new path acquired the lock after `old.close()` returned. No polling, none needed. | `stopServer()` runs `routingConfig.stop` -> `close()` on the test goroutine | 5/5 pass |
| [evalon/grpc-go-xd-6f67b5d7](https://github.com/kaitranntt-evals/grpc-go-xds-rds-interceptor-lifecycle-leak/tree/evalon/grpc-go-xd-6f67b5d7) | bounded polling `waitForCount(ctx, ...)` | `retire()` closes inline (no RPC in flight) inside `Stop` on the test goroutine | 5/5 pass |
| [evalon/grpc-go-xd-eaa55089](https://github.com/kaitranntt-evals/grpc-go-xds-rds-interceptor-lifecycle-leak/tree/evalon/grpc-go-xd-eaa55089) | bounded polling `waitForLiveInterceptors(1)` | `unref()` closes inline inside `Stop` on the test goroutine | 5/5 pass |
| [evalon/grpc-go-xd-eb3713e8](https://github.com/kaitranntt-evals/grpc-go-xds-rds-interceptor-lifecycle-leak/tree/evalon/grpc-go-xd-eb3713e8) | bounded polling `waitForCounter(ctx, ...)` | `fc.stop()` -> `urc.close()` inline inside `Stop` on the test goroutine | 5/5 pass |
| [evalon/grpc-go-xd-b1537b5b](https://github.com/kaitranntt-evals/grpc-go-xds-rds-interceptor-lifecycle-leak/tree/evalon/grpc-go-xd-b1537b5b) | bounded polling loop on `interceptorsDestroyed` (`ctx.Err()==nil`) | unused chain: inline in `listenerWrapper.Close`; chain with the live connection: `connWrapper.Close` -> `removeConn` -> `close()`, still inside `Stop` on the test goroutine | 5/5 pass |
| [evalon/grpc-go-xd-6826d1fb](https://github.com/kaitranntt-evals/grpc-go-xds-rds-interceptor-lifecycle-leak/tree/evalon/grpc-go-xd-6826d1fb) | bounded polling loop on created/destroyed with `ctx.Done()` | `fc.stop()` -> `updateRoutingConfiguration(errCfg)` -> `old.close()` inline inside `Stop` on the test goroutine | 5/5 pass |

Unit tests (`internal/xds/server`) on all six branches: assertions follow direct synchronous calls, or — where a goroutine is involved (ec59ed30 `TestRouteConfigurationUpdateDuringRPC`, 6f67b5d7 `TestFilterChain_ConcurrentRPCsAndRouteConfigurationUpdates`, b1537b5b `TestRouteConfigurationCleanupDuringRPC`, 6826d1fb `TestRoutingConfigurationUpdateDuringRPC`) — a completion event (`<-updated`, `wg.Wait()`, `<-rpcDone`/`<-done` sent after `RouteAndProcess` returned, i.e. after its deferred release ran).

### Raw output per branch

These six runs used the probe file before the optional `C1_DELAY_ONLY` switch was added for the controls; with that variable unset the probe behaves identically.

#### evalon/grpc-go-xd-ec59ed30

```sh
verify/repro/c1_probe.sh ~/wt/ec59ed30 ServerSideXDS_FilterStateRetention_RDSUpdates 'RouteConfiguration'
```

```console
##### branch d1c28a48 test ServerSideXDS_FilterStateRetention_RDSUpdates
===== 1. unmodified: go test -race -count=20 -run '^Test$/^ServerSideXDS_FilterStateRetention_RDSUpdates$' ./test/xds
      1 ok  	google.golang.org/grpc/test/xds	1.582s
===== 1b. unmodified unit lifecycle tests: go test -race -count=50 -run '^Test$/^(RouteConfiguration)' ./internal/xds/server
      1 ok  	google.golang.org/grpc/internal/xds/server	1.733s
 test/xds/xds_server_filter_state_retention_test.go | 4 ++--
===== 2. trace of who runs the cleanup (C1_TRACE=1, -count=1)
      9 C1-PROBE interceptor.Close goroutine N onTestGoroutine=false viaGRPCServerStop=false viaListenerClose=false viaConnClose=false viaHandleRDSUpdate=true viaRouteAndProcess=false
      1 C1-PROBE interceptor.Close goroutine N onTestGoroutine=true viaGRPCServerStop=true viaListenerClose=true viaConnClose=false viaHandleRDSUpdate=false viaRouteAndProcess=false
      1 C1-PROBE filter.Close goroutine N onTestGoroutine=true viaGRPCServerStop=true viaListenerClose=true viaConnClose=false viaHandleRDSUpdate=false viaRouteAndProcess=false
      1 --- PASS: Test (0.09s)
      1     --- PASS: Test/ServerSideXDS_FilterStateRetention_RDSUpdates (0.08s)
      1 PASS
      1 ok  	google.golang.org/grpc/test/xds	1.119s
===== 3. every Close() delayed 150ms before the counter moves (C1_DELAY_MS=150, -race -count=5)
      2     --- PASS: Test/ServerSideXDS_FilterStateRetention_RDSUpdates (1.68s)
      1     --- PASS: Test/ServerSideXDS_FilterStateRetention_RDSUpdates (1.69s)
      1     --- PASS: Test/ServerSideXDS_FilterStateRetention_RDSUpdates (1.73s)
      1     --- PASS: Test/ServerSideXDS_FilterStateRetention_RDSUpdates (1.74s)
      1 --- PASS: Test (1.68s)
      2 --- PASS: Test (1.69s)
      2 --- PASS: Test (1.74s)
      1 PASS
      1 ok  	google.golang.org/grpc/test/xds	9.565s
```

#### evalon/grpc-go-xd-6f67b5d7

```sh
verify/repro/c1_probe.sh ~/wt/6f67b5d7 ServerSideXDS_RDSUpdates_ReleaseSupersededInterceptors 'FilterChain_'
```

```console
##### branch 02334d87 test ServerSideXDS_RDSUpdates_ReleaseSupersededInterceptors
===== 1. unmodified: go test -race -count=20 -run '^Test$/^ServerSideXDS_RDSUpdates_ReleaseSupersededInterceptors$' ./test/xds
      1 ok  	google.golang.org/grpc/test/xds	3.809s
===== 1b. unmodified unit lifecycle tests: go test -race -count=50 -run '^Test$/^(FilterChain_)' ./internal/xds/server
      1 ok  	google.golang.org/grpc/internal/xds/server	1.558s
 test/xds/xds_server_filter_state_retention_test.go | 4 ++--
===== 2. trace of who runs the cleanup (C1_TRACE=1, -count=1)
     20 C1-PROBE interceptor.Close goroutine N onTestGoroutine=false viaGRPCServerStop=false viaListenerClose=false viaConnClose=false viaHandleRDSUpdate=true viaRouteAndProcess=false
      2 C1-PROBE interceptor.Close goroutine N onTestGoroutine=true viaGRPCServerStop=true viaListenerClose=true viaConnClose=false viaHandleRDSUpdate=false viaRouteAndProcess=false
      1 C1-PROBE filter.Close goroutine N onTestGoroutine=true viaGRPCServerStop=true viaListenerClose=true viaConnClose=false viaHandleRDSUpdate=false viaRouteAndProcess=false
      1 --- PASS: Test (0.14s)
      1     --- PASS: Test/ServerSideXDS_RDSUpdates_ReleaseSupersededInterceptors (0.13s)
      1 PASS
      1 ok  	google.golang.org/grpc/test/xds	1.175s
===== 3. every Close() delayed 150ms before the counter moves (C1_DELAY_MS=150, -race -count=5)
      3     --- PASS: Test/ServerSideXDS_RDSUpdates_ReleaseSupersededInterceptors (3.50s)
      1     --- PASS: Test/ServerSideXDS_RDSUpdates_ReleaseSupersededInterceptors (3.53s)
      1     --- PASS: Test/ServerSideXDS_RDSUpdates_ReleaseSupersededInterceptors (3.55s)
      2 --- PASS: Test (3.50s)
      1 --- PASS: Test (3.51s)
      1 --- PASS: Test (3.54s)
      1 --- PASS: Test (3.56s)
      1 PASS
      1 ok  	google.golang.org/grpc/test/xds	18.638s
```

#### evalon/grpc-go-xd-eaa55089

```sh
verify/repro/c1_probe.sh ~/wt/eaa55089 ServerSideXDS_FilterStateRetention_AcrossRDSUpdates 'UsableRouteConfiguration_ReleasedOnReplace'
```

```console
##### branch 0c970988 test ServerSideXDS_FilterStateRetention_AcrossRDSUpdates
===== 1. unmodified: go test -race -count=20 -run '^Test$/^ServerSideXDS_FilterStateRetention_AcrossRDSUpdates$' ./test/xds
      1 ok  	google.golang.org/grpc/test/xds	3.570s
===== 1b. unmodified unit lifecycle tests: go test -race -count=50 -run '^Test$/^(UsableRouteConfiguration_ReleasedOnReplace)' ./internal/xds/server
      1 ok  	google.golang.org/grpc/internal/xds/server	1.101s
 test/xds/xds_server_filter_state_retention_test.go | 4 ++--
===== 2. trace of who runs the cleanup (C1_TRACE=1, -count=1)
     10 C1-PROBE interceptor.Close goroutine N onTestGoroutine=false viaGRPCServerStop=false viaListenerClose=false viaConnClose=false viaHandleRDSUpdate=true viaRouteAndProcess=false
      1 C1-PROBE interceptor.Close goroutine N onTestGoroutine=true viaGRPCServerStop=true viaListenerClose=true viaConnClose=false viaHandleRDSUpdate=false viaRouteAndProcess=false
      1 C1-PROBE filter.Close goroutine N onTestGoroutine=true viaGRPCServerStop=true viaListenerClose=true viaConnClose=false viaHandleRDSUpdate=false viaRouteAndProcess=false
      1 --- PASS: Test (0.15s)
      1     --- PASS: Test/ServerSideXDS_FilterStateRetention_AcrossRDSUpdates (0.15s)
      1 PASS
      1 ok  	google.golang.org/grpc/test/xds	1.183s
===== 3. every Close() delayed 150ms before the counter moves (C1_DELAY_MS=150, -race -count=5)
      1     --- PASS: Test/ServerSideXDS_FilterStateRetention_AcrossRDSUpdates (1.87s)
      3     --- PASS: Test/ServerSideXDS_FilterStateRetention_AcrossRDSUpdates (1.88s)
      1     --- PASS: Test/ServerSideXDS_FilterStateRetention_AcrossRDSUpdates (1.90s)
      3 --- PASS: Test (1.88s)
      1 --- PASS: Test (1.89s)
      1 --- PASS: Test (1.91s)
      1 PASS
      1 ok  	google.golang.org/grpc/test/xds	10.473s
```

#### evalon/grpc-go-xd-eb3713e8

```sh
verify/repro/c1_probe.sh ~/wt/eb3713e8 ServerSideXDS_FilterStateRetention_AcrossUpdates_RouteConfigChange 'UsableRouteConfigurationUpdate_ReleasesSupersededResources'
```

```console
##### branch d8366628 test ServerSideXDS_FilterStateRetention_AcrossUpdates_RouteConfigChange
===== 1. unmodified: go test -race -count=20 -run '^Test$/^ServerSideXDS_FilterStateRetention_AcrossUpdates_RouteConfigChange$' ./test/xds
      1 ok  	google.golang.org/grpc/test/xds	2.158s
===== 1b. unmodified unit lifecycle tests: go test -race -count=50 -run '^Test$/^(UsableRouteConfigurationUpdate_ReleasesSupersededResources)' ./internal/xds/server
      1 ok  	google.golang.org/grpc/internal/xds/server	1.110s
 test/xds/xds_server_filter_state_retention_test.go | 4 ++--
===== 2. trace of who runs the cleanup (C1_TRACE=1, -count=1)
      3 C1-PROBE interceptor.Close goroutine N onTestGoroutine=false viaGRPCServerStop=false viaListenerClose=false viaConnClose=false viaHandleRDSUpdate=true viaRouteAndProcess=false
      1 C1-PROBE interceptor.Close goroutine N onTestGoroutine=true viaGRPCServerStop=true viaListenerClose=true viaConnClose=false viaHandleRDSUpdate=false viaRouteAndProcess=false
      1 C1-PROBE filter.Close goroutine N onTestGoroutine=true viaGRPCServerStop=true viaListenerClose=true viaConnClose=false viaHandleRDSUpdate=false viaRouteAndProcess=false
      1 --- PASS: Test (0.07s)
      1     --- PASS: Test/ServerSideXDS_FilterStateRetention_AcrossUpdates_RouteConfigChange (0.07s)
      1 PASS
      1 ok  	google.golang.org/grpc/test/xds	1.104s
===== 3. every Close() delayed 150ms before the counter moves (C1_DELAY_MS=150, -race -count=5)
      4     --- PASS: Test/ServerSideXDS_FilterStateRetention_AcrossUpdates_RouteConfigChange (0.79s)
      1     --- PASS: Test/ServerSideXDS_FilterStateRetention_AcrossUpdates_RouteConfigChange (0.80s)
      2 --- PASS: Test (0.79s)
      2 --- PASS: Test (0.80s)
      1 --- PASS: Test (0.81s)
      1 PASS
      1 ok  	google.golang.org/grpc/test/xds	5.023s
```

#### evalon/grpc-go-xd-b1537b5b

```sh
verify/repro/c1_probe.sh ~/wt/b1537b5b ServerSideXDS_FilterStateRetention_RouteUpdates 'RouteConfiguration'
```

```console
##### branch 4676ada0 test ServerSideXDS_FilterStateRetention_RouteUpdates
===== 1. unmodified: go test -race -count=20 -run '^Test$/^ServerSideXDS_FilterStateRetention_RouteUpdates$' ./test/xds
      1 ok  	google.golang.org/grpc/test/xds	1.609s
===== 1b. unmodified unit lifecycle tests: go test -race -count=50 -run '^Test$/^(RouteConfiguration)' ./internal/xds/server
      1 ok  	google.golang.org/grpc/internal/xds/server	1.252s
 test/xds/xds_server_filter_state_retention_test.go | 4 ++--
===== 2. trace of who runs the cleanup (C1_TRACE=1, -count=1)
     18 C1-PROBE interceptor.Close goroutine N onTestGoroutine=false viaGRPCServerStop=false viaListenerClose=false viaConnClose=false viaHandleRDSUpdate=true viaRouteAndProcess=false
      1 C1-PROBE interceptor.Close goroutine N onTestGoroutine=true viaGRPCServerStop=true viaListenerClose=true viaConnClose=false viaHandleRDSUpdate=false viaRouteAndProcess=false
      1 C1-PROBE interceptor.Close goroutine N onTestGoroutine=true viaGRPCServerStop=true viaListenerClose=false viaConnClose=true viaHandleRDSUpdate=false viaRouteAndProcess=false
      1 C1-PROBE filter.Close goroutine N onTestGoroutine=true viaGRPCServerStop=true viaListenerClose=false viaConnClose=true viaHandleRDSUpdate=false viaRouteAndProcess=false
      1 --- PASS: Test (0.04s)
      1     --- PASS: Test/ServerSideXDS_FilterStateRetention_RouteUpdates (0.03s)
      1 PASS
      1 ok  	google.golang.org/grpc/test/xds	1.071s
===== 3. every Close() delayed 150ms before the counter moves (C1_DELAY_MS=150, -race -count=5)
      1     --- PASS: Test/ServerSideXDS_FilterStateRetention_RouteUpdates (3.18s)
      2     --- PASS: Test/ServerSideXDS_FilterStateRetention_RouteUpdates (3.19s)
      1     --- PASS: Test/ServerSideXDS_FilterStateRetention_RouteUpdates (3.20s)
      1     --- PASS: Test/ServerSideXDS_FilterStateRetention_RouteUpdates (3.24s)
      3 --- PASS: Test (3.19s)
      1 --- PASS: Test (3.20s)
      1 --- PASS: Test (3.24s)
      1 PASS
      1 ok  	google.golang.org/grpc/test/xds	17.053s
```

#### evalon/grpc-go-xd-6826d1fb

```sh
verify/repro/c1_probe.sh ~/wt/6826d1fb ServerSideXDS_FilterStateRetention_AcrossRouteUpdates 'RoutingConfiguration|RDSUpdate'
```

```console
##### branch 93e14263 test ServerSideXDS_FilterStateRetention_AcrossRouteUpdates
===== 1. unmodified: go test -race -count=20 -run '^Test$/^ServerSideXDS_FilterStateRetention_AcrossRouteUpdates$' ./test/xds
      1 ok  	google.golang.org/grpc/test/xds	1.616s
===== 1b. unmodified unit lifecycle tests: go test -race -count=50 -run '^Test$/^(RoutingConfiguration|RDSUpdate)' ./internal/xds/server
      1 ok  	google.golang.org/grpc/internal/xds/server	1.291s
 test/xds/xds_server_filter_state_retention_test.go | 4 ++--
===== 2. trace of who runs the cleanup (C1_TRACE=1, -count=1)
     18 C1-PROBE interceptor.Close goroutine N onTestGoroutine=false viaGRPCServerStop=false viaListenerClose=false viaConnClose=false viaHandleRDSUpdate=true viaRouteAndProcess=false
      2 C1-PROBE interceptor.Close goroutine N onTestGoroutine=true viaGRPCServerStop=true viaListenerClose=true viaConnClose=false viaHandleRDSUpdate=false viaRouteAndProcess=false
      1 C1-PROBE filter.Close goroutine N onTestGoroutine=true viaGRPCServerStop=true viaListenerClose=true viaConnClose=false viaHandleRDSUpdate=false viaRouteAndProcess=false
      1 --- PASS: Test (0.04s)
      1     --- PASS: Test/ServerSideXDS_FilterStateRetention_AcrossRouteUpdates (0.03s)
      1 PASS
      1 ok  	google.golang.org/grpc/test/xds	1.068s
===== 3. every Close() delayed 150ms before the counter moves (C1_DELAY_MS=150, -race -count=5)
      1     --- PASS: Test/ServerSideXDS_FilterStateRetention_AcrossRouteUpdates (3.18s)
      2     --- PASS: Test/ServerSideXDS_FilterStateRetention_AcrossRouteUpdates (3.19s)
      1     --- PASS: Test/ServerSideXDS_FilterStateRetention_AcrossRouteUpdates (3.20s)
      1     --- PASS: Test/ServerSideXDS_FilterStateRetention_AcrossRouteUpdates (3.24s)
      2 --- PASS: Test (3.19s)
      1 --- PASS: Test (3.20s)
      1 --- PASS: Test (3.21s)
      1 --- PASS: Test (3.25s)
      1 PASS
      1 ok  	google.golang.org/grpc/test/xds	17.064s
```

Reading the trace lines: `viaHandleRDSUpdate=true` = retirement performed by the xDS watcher callback goroutine (asynchronous to the test; this is what the polling loops, or on ec59ed30 the RWMutex, wait for). `onTestGoroutine=true viaGRPCServerStop=true` = shutdown cleanup executed on the test's own goroutine inside `stopServer()`, so it has finished before the next statement (the assertion) runs. No cleanup was ever performed from `RouteAndProcess` (no RPC in flight at replacement/stop in these tests).

### Positive controls (sensitivity of the delay run)

```sh
verify/repro/c1_controls.sh ~/wt/ec59ed30 ~/wt/6f67b5d7
```

```console
##### ec59ed30 control A: routingConfig.update closes the old config asynchronously (old.close() -> go old.close())
-	old.close()
+	go old.close()
    xds_server_filter_state_retention_test.go:721: After update 1, destroyed 0 interceptors, want 1
--- FAIL: Test (0.16s)
    --- FAIL: Test/ServerSideXDS_FilterStateRetention_RDSUpdates (0.16s)
FAIL
FAIL	google.golang.org/grpc/test/xds	0.168s
FAIL
##### ec59ed30 control B: routingConfig.stop closes asynchronously (rc.config.close() -> go rc.config.close())
-	rc.config.close()
+	go rc.config.close()
    xds_server_filter_state_retention_test.go:735: After shutdown, destroyed 9 interceptors, created 10
    xds_server_filter_state_retention_test.go:738: After shutdown, destroyed 0 filters, want 1
--- FAIL: Test (1.52s)
    --- FAIL: Test/ServerSideXDS_FilterStateRetention_RDSUpdates (1.52s)
FAIL
FAIL	google.golang.org/grpc/test/xds	1.530s
FAIL
##### 6f67b5d7 control C: retire() closes interceptors asynchronously (urc.closeInterceptors() -> go urc.closeInterceptors())
-				urc.closeInterceptors()
+				go urc.closeInterceptors()
    xds_server_rds_update_test.go:244: Destroyed 20 interceptor instances after server stop, want: 22
--- FAIL: Test (1.69s)
    --- FAIL: Test/ServerSideXDS_RDSUpdates_ReleaseSupersededInterceptors (1.69s)
FAIL
FAIL	google.golang.org/grpc/test/xds	1.699s
FAIL
```

Controls run with `C1_DELAY_ONLY=interceptor C1_DELAY_MS=150` (a first attempt at control C delaying filter closes too passed by coincidence: the synchronous 150ms filter Close in `fc.stop()` gave the asynchronous interceptor closes time to finish; restricting the delay to interceptors removes that masking).

- Control A: once `old.close()` is moved out of the write-locked section on ec59ed30, the un-polled retirement assertion fails immediately ("After update 1, destroyed 0 interceptors, want 1"). With the real code the same delay passes 5/5, so the RWMutex ordering is what establishes completion there.
- Controls B and C: the shutdown assertions have no polling; they fail as soon as stop-time cleanup is made asynchronous. With the real code on every branch the cleanup is synchronous inside `Stop` (trace) and the delay run passes.

## C2

Claim: in [evalon/grpc-go-xd-bd84b6f8](https://github.com/kaitranntt-evals/grpc-go-xds-rds-interceptor-lifecycle-leak/tree/evalon/grpc-go-xd-bd84b6f8), an early fatal assertion in the added unit test `TestHandleRDSUpdate_ReleasesSupersededRouteConfiguration` (`internal/xds/server/listener_wrapper_test.go`) leaves its TCP listener open.

Source facts (added test, file line numbers on the branch):

```console
$ grep -n "LocalTCPListener\|lw.Close()\|defer\|Cleanup\|t.Fatal" internal/xds/server/listener_wrapper_test.go
101:	lis, err := testutils.LocalTCPListener()
103:		t.Fatalf("testutils.LocalTCPListener() failed: %v", err)
128:			t.Fatalf("Got counts (interceptors built, interceptors closed, filters closed) = %+v, want %+v", got, want)
148:		t.Fatalf("Usable route configuration has unexpected error: %v", urc.err)
151:		t.Fatalf("Usable route configuration has %d virtual hosts, want %d", got, want)
154:		t.Fatalf("Usable route configuration has %d routes, want %d", got, want)
163:		t.Fatal("Usable route configuration does not carry an error after RDS error update")
169:	lw.Close()
$ grep -n "func LocalTCPListener" -A2 internal/testutils/local_listener.go
24:func LocalTCPListener() (net.Listener, error) {
25:	return net.Listen("tcp", "localhost:0")
26:}
```

A real listener is opened with `testutils.LocalTCPListener()`; the only close is `lw.Close()` on the last-but-one line of the test; there is no `defer` and no `t.Cleanup`; between the two sit three `checkCounts(...)` call sites (each a `t.Fatalf` at line 128; the one in the loop runs three times) and four direct `t.Fatal`/`t.Fatalf` sites (lines 148, 151, 154, 163).

Exercise (script `verify/repro/c2_unclosed_listener.sh`): a probe test `TestZZZ_C2ListenerProbe` (file `verify/repro/c2_listener_probe_test.go.txt`, copied into the package) runs right after the audited test in the same process, lists every fd of the process that is a socket in LISTEN state (`SO_ACCEPTCONN`), and dials it. Run A is the unmodified test; run B forces the first assertion after listener creation to fail by changing one expected value in the test (`built: 2` -> `built: 3`), production code untouched.

```sh
verify/repro/c2_unclosed_listener.sh ~/wt/bd84b6f8
```

```console
===== A: unmodified test (passes, reaches lw.Close()) =====
=== RUN   Test
=== RUN   Test/HandleRDSUpdate_ReleasesSupersededRouteConfiguration
=== RUN   Test/ZZZ_C2ListenerProbe
    zz_c2_listener_probe_test.go:44: C2-PROBE listening sockets still open after previous test: 0 []
    zz_c2_listener_probe_test.go:61: C2-PROBE listening sockets after 3 forced GC cycles: 0 []
--- PASS: Test (0.16s)
    --- PASS: Test/HandleRDSUpdate_ReleasesSupersededRouteConfiguration (0.00s)
    --- PASS: Test/ZZZ_C2ListenerProbe (0.15s)
PASS
ok  	google.golang.org/grpc/internal/xds/server	0.164s
===== B: first post-listener assertion forced to fail (built: 2 -> built: 3) =====
 internal/xds/server/listener_wrapper_test.go | 2 +-
 1 file changed, 1 insertion(+), 1 deletion(-)
=== RUN   Test
=== RUN   Test/HandleRDSUpdate_ReleasesSupersededRouteConfiguration
    listener_wrapper_test.go:135: Got counts (interceptors built, interceptors closed, filters closed) = {built:2 closed:0 filtersClosed:0}, want {built:3 closed:0 filtersClosed:0}
=== RUN   Test/ZZZ_C2ListenerProbe
    zz_c2_listener_probe_test.go:44: C2-PROBE listening sockets still open after previous test: 1 [fd=4 127.0.0.1:34125]
    zz_c2_listener_probe_test.go:50: C2-PROBE dial 127.0.0.1:34125 -> err=<nil> (nil means the port is still being listened on)
    zz_c2_listener_probe_test.go:61: C2-PROBE listening sockets after 3 forced GC cycles: 0 []
--- FAIL: Test (0.16s)
    --- FAIL: Test/HandleRDSUpdate_ReleasesSupersededRouteConfiguration (0.00s)
    --- PASS: Test/ZZZ_C2ListenerProbe (0.16s)
FAIL
FAIL	google.golang.org/grpc/internal/xds/server	0.168s
FAIL
```

Observed: after the test function has completely finished via the fatal path, its listener is still in LISTEN state and still accepts TCP connections; in the passing run nothing is left. Nothing in the test or its helpers closed it.

Impact reasoning: the leak only occurs when this test fails (that is exactly when a regression in the code under test is present), and it is confined to the test binary: one fd and one ephemeral loopback port stay open after the test function returns. It is not unbounded: the Go runtime's finalizer on the socket closed it once the unreachable listener was garbage collected (last probe line, after forced GCs), and process exit releases it regardless. It does not mask or alter the test's verdict. The grpctest goroutine leak checker does not flag it (no goroutine is parked on the listener — nobody calls Accept). Severity: test hygiene, low. Fix: `defer lis.Close()` / `t.Cleanup(func() { lw.Close() })` right after the listener is created (`listenerWrapper.Close` calling `Listener.Close` twice is harmless, the second returns an error that is ignored).
