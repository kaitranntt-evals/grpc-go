## C1

**Claim.** The solution's Go lifecycle tests assert final interceptor destruction counts after server `Stop()` without establishing completion of server-side routing-reference release and deferred interceptor cleanup.

**Target.** Branch `evalon/grpc-go-xd-dd46dda9` of `kaitranntt-evals/grpc-go-xds-rds-interceptor-lifecycle-leak`, commit `89c922697727fb578b56ed556321bfa5ef786021` (one commit on top of base `4ee6ac46`). Production code was not modified for any run below.

**Verdict: CONFIRMED** for the solution's end-to-end lifecycle test `TestServerSideXDS_RouteConfigurationUpdates_ReleaseSupersededResources` (`test/xds/xds_server_route_config_update_test.go`). The three unit-level lifecycle tests in `internal/xds/server/usable_route_configuration_test.go` are not affected (they never go through a server `Stop()`; workers release their references in-goroutine before `wg.Wait()` returns).

### Setup

```sh
cd ~/repos/grpc-go
git remote add claimrepo https://github.com/kaitranntt-evals/grpc-go-xds-rds-interceptor-lifecycle-leak
git fetch claimrepo evalon/grpc-go-xd-dd46dda9
git worktree add ~/wt-claim claimrepo/evalon/grpc-go-xd-dd46dda9   # HEAD 89c92269
go version   # go1.26.3 linux/amd64, 8 CPUs
```

### 1. What the test and the shutdown path do (trace)

Tail of the solution's test (`test/xds/xds_server_route_config_update_test.go`, line numbers from the branch):

```go
267: 		for rpcCtx.Err() == nil {
268: 			if _, err := client.EmptyCall(rpcCtx, &testpb.Empty{}); err != nil && rpcCtx.Err() == nil {
...
284: 	rpcCancel()
285: 	wg.Wait()
...
296: 	// Stopping the server must release all remaining resources.
297: 	stopServer()
298: 	if got := fb.liveInterceptors.Load(); got != 0 {
299: 		t.Fatalf("%d live interceptors after server stop, want 0", got)
300: 	}
301: 	if got := fb.liveFilters.Load(); got != 0 {
302: 		t.Fatalf("%d live filter instances after server stop, want 0", got)
303: 	}
```

- `rpcCancel(); wg.Wait()` joins the *client* goroutine only. A cancelled `EmptyCall` returns on the client as soon as its context is done; the server-side handler for that RPC may still be running.
- `stopServer` is `stub.S.Stop()` (`test/xds/xds_server_integration_test.go`, `setupGRPCServer`). The server is built without `grpc.WaitForHandlers(true)`:

```console
$ grep -n "WaitForHandlers" test/xds/xds_server_route_config_update_test.go test/xds/xds_server_integration_test.go internal/xds/server/*_test.go
$ echo $?
1
```

- `grpc.Server.stop` (`server.go:1946`) closes the listeners first (`closeListenersLocked` -> `listenerWrapper.Close` -> `filterChainManager.stop` -> `filterChain.stop` -> `usableRouteConfiguration.retire`), then closes transports, and only waits for handlers `if graceful || s.opts.waitForHandlers`.
- On this branch `RouteAndProcess` (`internal/xds/server/routing.go:46-47`) holds a reference for the whole routing step, including `interceptor.AllowRPC` (line 97): `rc, release := acquireUsableRouteConfiguration(cw.urc); defer release()`.
- `retire()` (`internal/xds/server/filter_chain_manager.go:217`) closes interceptors immediately only `if s == 0`; otherwise closing is deferred to the last `release()`, which runs on the handler goroutine that `Stop()` does not wait for.

So lines 298/301 read the counters with no ordering against a handler that is inside `RouteAndProcess` when `Stop()` retires the configuration. There is no polling and no completion event (compare `waitForLiveInterceptors`, used for the earlier per-update checks, which does poll).

### 2. The unmodified solution test fails intermittently (observed)

Unmodified branch, unmodified test, `-race` binary, 16 concurrent test processes on 8 CPUs:

```sh
./verify/repro/c1_stress.sh ~/wt-claim 16 100
```

```console
[unmodified] runs: 1600  failures: 4
      4 xds_server_route_config_update_test.go:299: 1 live interceptors after server stop, want 0
      1 --- FAIL: Test/ServerSideXDS_RouteConfigurationUpdates_ReleaseSupersededResources (0.28s)
...
[diagnostics] runs: 1600  failures: 2
      2 verify_c1_stop_ordering_test.go:410: 1 live interceptors after server stop, want 0
      1 verify_c1_stop_ordering_test.go:407: VERIFY-DIAG baseline=2; immediately after Stop(): liveInterceptors=1 liveFilters=1; both reached 0 6.809556ms after Stop() returned (-1ns = never within 5s)
      1 verify_c1_stop_ordering_test.go:407: VERIFY-DIAG baseline=2; immediately after Stop(): liveInterceptors=1 liveFilters=1; both reached 0 2.750091ms after Stop() returned (-1ns = never within 5s)
...
[waitforhandlers] runs: 1600  failures: 0
```

A full failing log of the unmodified test is at `verify/repro/logs/unmodified_FAIL_w12_i49.log` (key lines):

```console
    xds_server_route_config_update_test.go:299: 1 live interceptors after server stop, want 0
--- FAIL: Test (0.25s)
    --- FAIL: Test/ServerSideXDS_RouteConfigurationUpdates_ReleaseSupersededResources (0.24s)
FAIL
```

All runs of the unmodified test (`-race` test binaries built from commit `89c92269`; each run is a separate process running only this test). The first-batch, 8-process and 4-process rows were produced with an ad hoc copy of the loop in `c1_stress.sh`; the equivalent replay command is `ONLY=unmodified ./verify/repro/c1_stress.sh ~/wt-claim <workers> <iters>`:

| Load | Runs | Failures | Failure message |
| --- | --- | --- | --- |
| 16 processes / 8 CPUs (first batch) | 800 | 4 | `xds_server_route_config_update_test.go:299: 1 live interceptors after server stop, want 0` |
| 16 processes / 8 CPUs (`c1_stress.sh`) | 1600 | 4 | same |
| 8 processes / 8 CPUs | 1600 | 2 | same |
| 4 processes / 8 CPUs | 800 | 0 | - |
| 1 process, `go test -race -count=800` | 800 | 0 | - |

Every one of the 10 failures of the unmodified test is the same line-299 assertion with a count of exactly 1. The rate is low (about 0.3% at 16 processes, 0.1% at 8) and I did not observe it on an idle machine (0 of 1600 at 4 processes or fewer), so this is a load-dependent flake, not a deterministic failure.

Sequential run command and output:

```sh
cd ~/wt-claim && go test -race -count=800 -timeout=20m -run '^Test$/^ServerSideXDS_RouteConfigurationUpdates_ReleaseSupersededResources$' ./test/xds
```

```console
ok  	google.golang.org/grpc/test/xds	190.434s
```

### 3. The non-zero count is unfinished cleanup, not a leak (observed)

`VerifyC1_SolutionLifecycle_Diagnostics` (`verify/repro/c1_stop_ordering_test.go`) is the solution's test body; the only change is that a non-zero post-`Stop()` reading is followed by polling to see if and when the counters reach zero. Both failures captured under stress (`verify/repro/logs/diagnostics_FAIL_*.log`):

```console
VERIFY-DIAG baseline=2; immediately after Stop(): liveInterceptors=1 liveFilters=1; both reached 0 6.809556ms after Stop() returned (-1ns = never within 5s)
VERIFY-DIAG baseline=2; immediately after Stop(): liveInterceptors=1 liveFilters=1; both reached 0 2.750091ms after Stop() returned (-1ns = never within 5s)
```

The interceptor and the filter were released a few milliseconds after `Stop()` returned. The implementation did not leak; the test read the counters before the last handler dropped its reference.

### 4. Deterministic demonstration of the mechanism, and of the server option that controls it (observed)

`VerifyC1_InFlightHandlerAtStop_*` park exactly one server handler inside `AllowRPC` (a test filter, no production change) and then execute the solution test's final sequence: cancel client worker -> `wg.Wait()` -> `Stop()` -> read counters.

```sh
cd ~/wt-claim
cp ~/repos/grpc-go/verify/repro/c1_stop_ordering_test.go test/xds/verify_c1_stop_ordering_test.go
go test -race -tags verifyc1 -count=1 -v -run '^Test$/^VerifyC1_InFlightHandlerAtStop' ./test/xds 2>&1 | grep -E "verify:|live interceptors after|live filter instances after|^(=== RUN|--- |\s+--- |PASS|FAIL|ok)"
```

```console
=== RUN   Test/VerifyC1_InFlightHandlerAtStop_Default
    verify_c1_stop_ordering_test.go:206: verify: live interceptors while serving (baseline) = 2, live filters = 1
    verify_c1_stop_ordering_test.go:228: verify: client worker cancelled and joined; server handler is still inside RouteAndProcess/AllowRPC; live interceptors = 2
    verify_c1_stop_ordering_test.go:238: verify: Stop() returned after 1.931101ms (WaitForHandlers=false); immediately after Stop(): live interceptors = 1, live filters = 1
    verify_c1_stop_ordering_test.go:250: verify: after releasing the parked handler: live interceptors = 0, live filters = 0 (settled=true, 223.688µs after the handler was released)
    verify_c1_stop_ordering_test.go:255: 1 live interceptors after server stop, want 0
    verify_c1_stop_ordering_test.go:258: 1 live filter instances after server stop, want 0
=== RUN   Test/VerifyC1_InFlightHandlerAtStop_WaitForHandlers
    verify_c1_stop_ordering_test.go:206: verify: live interceptors while serving (baseline) = 2, live filters = 1
    verify_c1_stop_ordering_test.go:228: verify: client worker cancelled and joined; server handler is still inside RouteAndProcess/AllowRPC; live interceptors = 2
    verify_c1_stop_ordering_test.go:238: verify: Stop() returned after 301.415208ms (WaitForHandlers=true); immediately after Stop(): live interceptors = 0, live filters = 0
    verify_c1_stop_ordering_test.go:250: verify: after releasing the parked handler: live interceptors = 0, live filters = 0 (settled=true, 1.495µs after the handler was released)
--- FAIL: Test (0.37s)
    --- FAIL: Test/VerifyC1_InFlightHandlerAtStop_Default (0.05s)
    --- PASS: Test/VerifyC1_InFlightHandlerAtStop_WaitForHandlers (0.32s)
FAIL
```

- Default server options (what the solution's test uses): the client worker is cancelled and joined, `Stop()` returns in about 2 ms, and one interceptor plus the filter are still alive. They are released 224 µs after the handler is let go. This is the same "1 live interceptors after server stop" reading as the natural failures in section 2.
- `grpc.WaitForHandlers(true)`: `Stop()` blocks for the 300 ms the handler is held and the counters are already zero when it returns.

### 5. Control: the same test body with `WaitForHandlers(true)` does not flake (observed)

`VerifyC1_SolutionLifecycle_WaitForHandlers` is the solution's test body with `grpc.WaitForHandlers(true)` added to the server options, run under the same 16-process load:

```sh
ONLY=waitforhandlers ./verify/repro/c1_stress.sh ~/wt-claim 16 200   # second batch; originally run through an ad hoc copy of the same loop
```

```console
[waitforhandlers] runs: 1600  failures: 0      (from c1_stress.sh)
runs: 3200  failures: 0                        (second batch)
```

0 failures in 4800 runs, against 10 failures in 4000 runs of the same body without the option at the same load (8 of 2400 unmodified, 2 of 1600 diagnostics copy). At the observed 0.25% rate about 12 failures would be expected in 4800 runs.

### 6. Scope: the unit-level lifecycle tests are ordered (observed)

`internal/xds/server/usable_route_configuration_test.go` calls `fc.stop()` directly after `wg.Wait()` on goroutines that call `release()` themselves, so every reference is dropped before the final assertions.

```sh
cd ~/wt-claim && go test -race -count=300 -run '^Test$/^FilterChain_(RouteConfigUpdates_ReleaseSupersededConfigurations|RouteConfigUpdate_InFlightRPCKeepsConfigurationAlive|ConcurrentRPCsAndRouteConfigUpdates)$' ./internal/xds/server
```

```console
ok  	google.golang.org/grpc/internal/xds/server	4.626s
```

### Impact reasoning

- The task requires that tests under `test/xds` pass with `-race`, and the eval runs `go test -race -v -timeout=5m ./test/xds -count=1`, which includes this solution test. When the test flakes, the whole `./test/xds` package run is reported as failed even though the implementation released everything (section 3).
- The trigger is ordinary for a CI machine: the test only needs CPU contention at the moment the worker is cancelled, so that the last cancelled RPC's server handler is between `acquire` and `release` when `Stop()` retires the configuration. Observed rate: about 0.3% per run at 2x CPU oversubscription, about 0.1% at 1x, none seen in 1600 runs at lower load.
- The failure message ("1 live interceptors after server stop, want 0") reads as a production leak, which is the bug the task is about, so a flake here sends the reader to the wrong place.
- The eval's own fixture avoids the problem by polling after `stopServer()` (`tests/eval_xds_server_interceptor_leak_test.go`, `for range 100 { ... time.Sleep(10 * time.Millisecond) }` before its destroyed-count assertions); the solution's test has no equivalent.
- Fix options, both test-only: poll for zero after `stopServer()` (the test already has `waitForLiveInterceptors`), or build the server with `grpc.WaitForHandlers(true)` (sections 4 and 5).

### Not established

- I did not observe a failure of the unmodified test on an idle machine (0 of 800 sequential, 0 of 800 at 4 processes), so the rate for a single quiet `go test ./test/xds` run is below what these samples can measure.
- I did not run the eval fixtures against this branch; the claim is about the solution's own tests.
