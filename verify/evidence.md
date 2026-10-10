Run ID `v-6feee980`. Audited checkout: `grpc-go-xds-rds-interceptor-lifecycle-leak-perfect` @ `614cb739`. Claim branches were fetched from `https://github.com/kaitranntt-evals/grpc-go-xds-rds-interceptor-lifecycle-leak` into detached worktrees (`~/wt/<suffix>`): `evalon/grpc-go-xd-cc13b651` @ `ca679133`, `evalon/grpc-go-xd-40d89f46` @ `303eae0f`, `evalon/grpc-go-xd-8ef80169` @ `a3cd06f2`, `evalon/grpc-go-xd-2ba766a0` @ `bb499589`. Go 1.25.7 linux/amd64. No production code was changed on any branch; every mutation below was temporary, applied in a worktree and reverted with `git checkout -- .`.

```sh
cd ~/repos/grpc-go
git remote add claims https://github.com/kaitranntt-evals/grpc-go-xds-rds-interceptor-lifecycle-leak
for b in cc13b651 40d89f46 8ef80169 2ba766a0; do
  git fetch claims evalon/grpc-go-xd-$b:refs/remotes/claims/$b
  git worktree add ~/wt/$b claims/$b
done
```

## C1

Verdict: REFUTED on all three branches (cc13b651, 40d89f46, 8ef80169).

### What the three implementations do (read, then observed below)

All three add the same ownership model: `getOrCreateServerFilterWithMap` takes **two** references when it creates a filter (one kept by the listener cache `listenerWrapper.httpFilters`, one handed to the caller), each `usableRouteConfiguration` records the references it acquired in `serverFilters`, and retiring a configuration (`unref`/`close` on cc13b651, `release`/`close` on 40d89f46, `release` on 8ef80169) closes its interceptors and then calls `sf.Close()` on each recorded reference. The cache's own reference is dropped only on listener `Close()`, LDS resource error, or when a later LDS update no longer names the filter.

### Step 1 — the eval fixture, byte-exact, on each branch

```sh
cd ~/wt/$b
cp ~/eval/tests/eval_rds_error_after_success_test.go internal/xds/server/
go test -race -v -count=1 -run '^Test$/^Eval_ServerSideXDS_ErrorUpdateReleasesServerFilterReferences$' ./internal/xds/server
```

Identical outcome on cc13b651, 40d89f46 and 8ef80169 (only the final assertion fails; the two earlier ones — 1 interceptor closed after route1 error, 0 filters destroyed while route2 live, 2 interceptors closed after route2 error — pass):

```console
    eval_rds_error_after_success_test.go:225: after route2 error: 0 server filters destroyed, want 1 (server filter references leaked)
--- FAIL: Test (1.06s)
    --- FAIL: Test/Eval_ServerSideXDS_ErrorUpdateReleasesServerFilterReferences (1.05s)
FAIL	google.golang.org/grpc/internal/xds/server	1.102s
```

The fixture measures only "the filter is still alive while the listener is still up". The claim's refute condition says that continued filter lifetime alone does not establish retained configuration ownership, so this failure does not decide the claim; step 2 traces who owns the surviving reference.

### Step 2 — ownership trace

Probe: `verify/instrumentation/c1/verify_c1_ownership_probe_test.go.txt` (same xDS setup as the fixture: one Listener, two filter chains `fc-1`→`route1`, `fc-2`→`route2`, both naming HTTP filter `count`, plus the router filter). It reads `listenerWrapper.httpFilters[*].refCnt`, every filter chain's current configuration and the retired configuration objects directly, at each stage. Per-branch field accessors are in `verify_c1_helpers_<branch>_test.go.txt`.

```sh
I=~/repos/grpc-go/verify/instrumentation/c1
cd ~/wt/$b
cp $I/verify_c1_ownership_probe_test.go.txt internal/xds/server/verify_c1_ownership_probe_test.go
cp $I/verify_c1_helpers_${b}_test.go.txt   internal/xds/server/verify_c1_helpers_test.go
go test -race -v -count=1 -run '^Test$/^VerifyC1_FilterReferenceOwnershipTrace$' ./internal/xds/server
```

Output (cc13b651; the TRACE lines on 40d89f46 and 8ef80169 are identical apart from map iteration order):

```console
TRACE after-install          chain route=route1 currentRC{err=<nil>, heldFilterRefs=2, rcRefs=1}
TRACE after-install          chain route=route2 currentRC{err=<nil>, heldFilterRefs=2, rcRefs=1}
TRACE after-install          cache[count].refCnt=3 = liveConfigHolders(2) + 1
TRACE after-install          cache[router].refCnt=3 = liveConfigHolders(2) + 1
TRACE after-install          interceptors created=2 closed=0 filterDestroyed=0
TRACE retired route1 configuration: rcRefs=0 heldFilterRefs(recorded)=2
TRACE after-route1-error     chain route=route1 currentRC{err=verify: route1 withdrawn, heldFilterRefs=0, rcRefs=1}
TRACE after-route1-error     chain route=route2 currentRC{err=<nil>, heldFilterRefs=2, rcRefs=1}
TRACE after-route1-error     cache[count].refCnt=2 = liveConfigHolders(1) + 1
TRACE after-route1-error     cache[router].refCnt=2 = liveConfigHolders(1) + 1
TRACE after-route1-error     interceptors created=2 closed=1 filterDestroyed=0
TRACE retired route2 configuration: rcRefs=0 heldFilterRefs(recorded)=2
TRACE after-route2-error     chain route=route1 currentRC{err=verify: route1 withdrawn, heldFilterRefs=0, rcRefs=1}
TRACE after-route2-error     chain route=route2 currentRC{err=verify: route2 withdrawn, heldFilterRefs=0, rcRefs=1}
TRACE after-route2-error     cache[router].refCnt=1 = liveConfigHolders(0) + 1
TRACE after-route2-error     cache[count].refCnt=1 = liveConfigHolders(0) + 1
TRACE after-route2-error     interceptors created=2 closed=2 filterDestroyed=0
TRACE after-50-cycles        chain route=route1 currentRC{err=verify: route1 withdrawn, heldFilterRefs=0, rcRefs=1}
TRACE after-50-cycles        chain route=route2 currentRC{err=verify: route2 withdrawn, heldFilterRefs=0, rcRefs=1}
TRACE after-50-cycles        cache[count].refCnt=1 = liveConfigHolders(0) + 1
TRACE after-50-cycles        cache[router].refCnt=1 = liveConfigHolders(0) + 1
TRACE after-50-cycles        interceptors created=102 closed=102 filterDestroyed=0
TRACE after-listener-Close         sharedFilter.refCnt=0 filterDestroyed=1
--- PASS: Test (0.02s)
    --- PASS: Test/VerifyC1_FilterReferenceOwnershipTrace (0.02s)
ok  	google.golang.org/grpc/internal/xds/server	1.085s
```

Per-branch result lines:

```console
######## cc13b651   ok  	google.golang.org/grpc/internal/xds/server	1.085s
######## 40d89f46   ok  	google.golang.org/grpc/internal/xds/server	1.124s
######## 8ef80169   ok  	google.golang.org/grpc/internal/xds/server	1.131s
```

Reading the trace:

- Install: refCnt 3 = 1 (cache) + 1 per configuration.
- Each error update drops refCnt by exactly one, the retired configuration object reaches `rcRefs=0` (its cleanup ran) and its interceptor is closed.
- After both error updates no live configuration holds a filter reference (`heldFilterRefs=0` on both chains) and refCnt is exactly 1.
- 50 further install→error cycles per route (100 more retirements) leave refCnt at 1 and interceptors created == closed == 102: nothing accumulates per retirement.
- `lw.Close()` — which on these branches releases only the cache entries plus the (already empty) current configurations — takes refCnt 1→0 and destroys the filter exactly once. The single surviving reference was therefore the listener cache's.

### Step 3 — the probe does detect a retired-configuration leak (mutation control)

On each branch, the `sf.Close()` inside the retired configuration's cleanup was temporarily replaced by `_ = sf`, the probe re-run, and the file restored (`git checkout -- internal/xds/server/filter_chain_manager.go`).

```console
######## cc13b651 (mutant: retired configuration skips sf.Close)
-		sf.Close()
+		_ = sf // VERIFY MUTANT
after route1 error: shared filter refCnt=3, want 2 (cache + route2 config)
TRACE after-route2-error     cache[count].refCnt=3 = liveConfigHolders(0) + 3
after-route2-error: cache[count] has 3 refs beyond live configurations, want exactly 1 (the listener cache's own)
after 50 cycles: cacheEntries=2 refCnt=103, want 2/1
after listener Close: filter destroyed 0 times, want 1
```

Same failing lines (refCnt 3 after route2 error, 103 after 50 cycles, 0 destroyed after Close) on 40d89f46 and 8ef80169. So the green probe in step 2 is evidence that the unmutated branches release the retired configurations' references.

### Conclusion

On every branch each retired configuration releases its filter references when its retirement cleanup completes; the one reference left after the error updates is the independently live listener cache's and is released by listener teardown. The fixture's final assertion fails on all three only because these branches deliberately keep an LDS-configured filter alive across RDS errors — a design difference from the fixture's expectation, not the leak the claim describes.

## C2

Verdict: REFUTED (both parts) on `evalon/grpc-go-xd-40d89f46`.

Lifecycle tests added by the solution (`git diff --stat 4ee6ac46 HEAD -- '*_test.go'`):

```console
 internal/xds/server/route_configuration_test.go    | 332 +++++++++++++++++++++
 test/xds/xds_server_filter_state_retention_test.go | 121 ++++++++
```

i.e. `TestRouteConfigurationConstructionFailureCleanup`, `TestRDSUpdateResourceCleanup`, `TestRouteConfigurationCleanupWithDrainingConnection`, `TestRouteConfigurationCleanupWithActiveRPC` (package `server`) and `TestServerSideXDS_FilterStateRetention_AcrossRDSUpdates` (package `xds_test`).

### Part 1 — lifecycle assertion synchronization

Every non-initial lifecycle assertion and what establishes completion before it runs:

| Test (line) | Assertion | Completion guarantee |
|---|---|---|
| ConstructionFailureCleanup (112–121) | every filter / interceptor closed once | `constructUsableRouteConfiguration` called on the test goroutine; cleanup happens before it returns |
| RDSUpdateResourceCleanup (188–213) | `checkClosed(...)`, `filterCloses` | `l.handleRDSUpdate` / `l.Close()` called directly on the test goroutine, no RPCs, no other goroutines; retirement runs inside the call under `l.mu` |
| DrainingConnection (247–256) | filter not closed before drain; interceptor/filter closed once after | `RouteAndProcess`, `l.Close()`, `cw.Close()` all called synchronously on the test goroutine |
| ActiveRPC (302) | active interceptor **not** closed during update | preceded by `<-entered` (completion event, ctx-bounded): the RPC holds its reference before the update runs |
| ActiveRPC (311–315) | replaced interceptor closed once, filter not closed | second `RouteAndProcess` and `l.Close()` are synchronous on the test goroutine |
| ActiveRPC (326–330) | active interceptor closed once, filter closed once | after `<-done` (completion event, ctx-bounded); `done <- RouteAndProcess(ctx)` sends only after `RouteAndProcess` returned, i.e. after its deferred `rc.release()` |
| AcrossRDSUpdates (265–275) | created == i+1, destroyed == i, 1 filter, 0 destroyed | `waitForPath(path)`: deadline-bounded polling until an RPC is served by the **new** interceptor. `safeRouteConfiguration.acquire()` and `update()` share `s.mu`, and `update` releases the old configuration before unlocking, so an RPC can only see the new configuration after the old one was released; an RPC that grabbed the old one first releases it in `RouteAndProcess`'s defer, which `xdsUnaryInterceptor` runs **before** the handler/response, and the test's RPCs are sequential |
| AcrossRDSUpdates (279–284) | all interceptors + filter destroyed after shutdown | `stopServer()` → `Server.Stop()` closes the server transports synchronously (`connWrapper.Close` → `removeConn` → release) before returning |

Observed evidence — baseline repetition:

```sh
cd ~/wt/40d89f46
go test -race -count=30 -run '^Test$/^(RouteConfigurationConstructionFailureCleanup|RDSUpdateResourceCleanup|RouteConfigurationCleanupWithDrainingConnection|RouteConfigurationCleanupWithActiveRPC)$' ./internal/xds/server
go test -race -count=15 -run '^Test$/^ServerSideXDS_FilterStateRetention_AcrossRDSUpdates$' ./test/xds
GOMAXPROCS=1 go test -race -count=10 -run '^Test$/^ServerSideXDS_FilterStateRetention_AcrossRDSUpdates$' ./test/xds
GOMAXPROCS=1 go test -race -count=20 -run '^Test$/^(RDSUpdateResourceCleanup|RouteConfigurationCleanupWithDrainingConnection|RouteConfigurationCleanupWithActiveRPC)$' ./internal/xds/server
```

```console
ok  	google.golang.org/grpc/internal/xds/server	1.604s
ok  	google.golang.org/grpc/test/xds	1.731s
ok  	google.golang.org/grpc/test/xds	2.210s
ok  	google.golang.org/grpc/internal/xds/server	1.393s
```

Observed evidence — adverse-ordering attempt. `verify/instrumentation/c2/c2_perturbation_40d89f46.patch` (temporary) adds `time.Sleep(30ms)` at the top of `usableRouteConfiguration.release()` (delays **every** retirement: update-time, RPC-defer and shutdown) and `time.Sleep(200ms)` before `c.urc.removeConn()` in `connWrapper.Close` (delays drain-time retirement). If any assertion merely raced with retirement, widening the window by 30–200 ms would make it fail.

```sh
cd ~/wt/40d89f46 && git apply ~/repos/grpc-go/verify/instrumentation/c2/c2_perturbation_40d89f46.patch
go test -race -v -count=1 -run '^Test$/^ServerSideXDS_FilterStateRetention_AcrossRDSUpdates$' ./test/xds
go test -race -count=5 -run '^Test$/^ServerSideXDS_FilterStateRetention_AcrossRDSUpdates$' ./test/xds
go test -race -v -count=3 -run '^Test$/^(RouteConfigurationConstructionFailureCleanup|RDSUpdateResourceCleanup|RouteConfigurationCleanupWithDrainingConnection|RouteConfigurationCleanupWithActiveRPC)$' ./internal/xds/server
git checkout -- .
```

```console
    xds_server_filter_state_retention_test.go:192: VERIFY C2: servingCh send #1, len(servingCh)=0 cap=1 before send
    xds_server_filter_state_retention_test.go:188: VERIFY C2: servingCh sends=1; RPCs=20; max len(pathCh) before RPC=0, after RPC=1 (cap=1)
--- PASS: Test (1.02s)
    --- PASS: Test/ServerSideXDS_FilterStateRetention_AcrossRDSUpdates (1.01s)
ok  	google.golang.org/grpc/test/xds	2.065s
ok  	google.golang.org/grpc/test/xds	5.826s
--- PASS: Test (0.70s)
    --- PASS: Test/RDSUpdateResourceCleanup (0.25s)
    --- PASS: Test/RouteConfigurationCleanupWithActiveRPC (0.15s)
    --- PASS: Test/RouteConfigurationCleanupWithDrainingConnection (0.29s)
    --- PASS: Test/RouteConfigurationConstructionFailureCleanup (0.00s)
ok  	google.golang.org/grpc/internal/xds/server	3.174s
```

(3/3 iterations of the unit tests passed; durations of 0.15–0.29 s show the injected delays were on the exercised paths. `RPCs=20` for 11 `waitForPath` calls shows RPCs did overlap in-progress updates and were served by the old configuration, and the destroyed-count assertions still held.) No ordering was found in which an assertion runs before the creation/retirement it assumes.

### Part 2 — blocking-operation bounds

Every channel operation in the solution's lifecycle tests (`grep -n 'wg\.\|WaitGroup\|<-\|make(chan'`); there is no `sync.WaitGroup`/worker join in either file:

| Location | Operation | Bound |
|---|---|---|
| route_configuration_test.go:267–272 | `close(entered)`; `select { <-unblock; <-ctx.Done() }` | `ctx` has `defaultTestTimeout`; `defer release()` closes `unblock` on any exit |
| :294 | `done <- RouteAndProcess(ctx)` | `done` has capacity 1 and exactly one sender |
| :295–298, :318–324 | `select { <-entered / <-done; <-ctx.Done() }` | ctx timeout |
| retention_test.go AcrossRDSUpdates `servingCh <- struct{}{}` | send in serving-mode callback | capacity 1; observed exactly one send, into an empty buffer (`servingCh send #1, len(servingCh)=0 cap=1`, `servingCh sends=1`) — the test never drives the listener out of SERVING before shutdown |
| `select { <-servingCh; <-ctx.Done() }` | receive | ctx timeout |
| `i.pathCh <- i.basePath` (`trackingInterceptor.AllowRPC`, pre-existing helper reused by the new test) | send | capacity 1; the test issues RPCs sequentially and drains `pathCh` after every successful RPC. Observed over 20 RPCs: `max len(pathCh) before RPC=0, after RPC=1 (cap=1)` — the buffer is always empty when the single send per RPC happens |
| `select { got := <-pathCh; <-ctx.Done() }` | receive | ctx timeout |
| `client.EmptyCall(ctx, …)`, `managementServer.Update(ctx, …)` | blocking calls | same deadline-bounded `ctx` |

All are bounded or cannot block; none was observed to block across the runs above (including under the injected delays).

## C3

Verdict: CONFIRMED (both parts) on `evalon/grpc-go-xd-2ba766a0`.

Naming note: the claim's "where to look" also lists `test/xds/xds_server_interceptor_leak_test.go`; that file does not exist on this branch (`ls test/xds | grep -i leak` → nothing). The helper and its users are all in `internal/xds/server/listener_wrapper_test.go`.

### What the test file contains

```sh
cd ~/wt/2ba766a0
grep -n 'l.Close()\|defer\|t.Cleanup\|newTestListenerWrapperWithRDS(t' internal/xds/server/listener_wrapper_test.go
```

```console
102:func newTestListenerWrapperWithRDS(t *testing.T, routeName string, filters []xdsresource.HTTPFilter) *listenerWrapper {
109:	t.Cleanup(func() { lis.Close() })
149:	l := newTestListenerWrapperWithRDS(t, routeName, filters)
153:	defer cancel()
252:	l.Close()
263:	l := newTestListenerWrapperWithRDS(t, routeName, filters)
267:	l.Close()
```

The helper's only registered cleanup closes the raw `net.Listener`. Neither test defers or registers `l.Close()`. In `TestListenerWrapper_RouteConfigUpdate_ReleasesSupersededResources`, the first `l.handleRDSUpdate` (line 201) acquires a server filter reference and interceptors, and between it and the only `l.Close()` (line 252) there are 12 fatal-assertion sites: three calls to `verifyActiveRouteConfig` (lines 202, 211, 243; it uses `t.Fatalf` at lines 162–172), four calls to `verifyCounts` (lines 204, 213, 235, 245; it ends in `t.FailNow()` at line 191), and direct `t.Fatalf` at lines 220, 223, 233, 237, 247. `TestListenerWrapper_Close_FilterChainInErrorState` has no fatal assertion between acquisition and `l.Close()` (lines 265–267), so only the first test has the failure path.

### Cleanup part — exercise the helper's cleanup with the explicit wrapper Close skipped

Repro `verify/repro/c3_wrapper_teardown_leak_test.go.txt` (uses the solution's own `newTestListenerWrapperWithRDS`, `countingFilterBuilder`, `routeConfigWithRoutes`; the body runs in a subtest so the parent can inspect state after all of the subtest's cleanups have run).

```sh
cd ~/wt/2ba766a0
cp ~/repos/grpc-go/verify/repro/c3_wrapper_teardown_leak_test.go.txt internal/xds/server/verify_c3_wrapper_teardown_leak_test.go
go test -race -v -count=1 -run '^Test$/^VerifyC3_' ./internal/xds/server
```

```console
    verify_c3_wrapper_teardown_leak_test.go:40: right after helper returned: filtersCreated=0 interceptorsBuilt=0 len(l.httpFilters)=0
    verify_c3_wrapper_teardown_leak_test.go:43: after first RDS update: filtersCreated=1 interceptorsBuilt=3 sharedFilter.refCnt=3
    verify_c3_wrapper_teardown_leak_test.go:46: cleanup-part: after ALL registered cleanups ran: rawListenerClosed=true wrapper.closed.HasFired=false | filtersCreated=1 filtersClosed=0 interceptorsBuilt=3 interceptorsClosed=0 sharedFilter.refCnt=3
    verify_c3_wrapper_teardown_leak_test.go:47: LEAK (cleanup part): helper cleanup closed only the raw listener; wrapper-owned filter + interceptors still live
    verify_c3_wrapper_teardown_leak_test.go:53: control, after explicit l.Close(): filtersClosed=1 interceptorsClosed=3 sharedFilter.refCnt=0
    verify_c3_wrapper_teardown_leak_test.go:75: failure-path: after ALL registered cleanups ran: rawListenerClosed=true wrapper.closed.HasFired=false | filtersCreated=1 filtersClosed=0 interceptorsBuilt=3 interceptorsClosed=0 sharedFilter.refCnt=3
    verify_c3_wrapper_teardown_leak_test.go:76: LEAK (failure path): test goroutine exited before l.Close(); no failure-safe wrapper teardown ran
--- PASS: Test (0.01s)
    --- PASS: Test/VerifyC3_CleanupWithoutExplicitWrapperClose (0.01s)
        --- PASS: Test/VerifyC3_CleanupWithoutExplicitWrapperClose/body (0.00s)
    --- PASS: Test/VerifyC3_GoexitBeforeExplicitWrapperClose (0.00s)
        --- SKIP: Test/VerifyC3_GoexitBeforeExplicitWrapperClose/body (0.00s)
ok  	google.golang.org/grpc/internal/xds/server	1.045s
```

The helper itself acquires no filter resources (`filtersCreated=0` when it returns); they are acquired by the test's first RDS update. After the helper's cleanup ran, the raw listener is closed but the wrapper was never closed and 1 filter (refCnt 3) and 3 interceptors remain live; the explicit `l.Close()` is what releases them (control line).

### Failure-path part — a real fatal assertion in the solution's own test

Two temporary patches on the worktree, reverted afterwards:

- `verify/instrumentation/c3/c3_observer_2ba766a0.patch`: adds one `t.Cleanup` at the top of the solution's test (registered before the helper's, so it runs after it) that logs the filter builder's counters.
- `verify/instrumentation/c3/c3_mutant_reintroduce_leak_2ba766a0.patch`: `old.close()` → `_ = old` in `storeUsableRouteConfiguration`, i.e. the very regression this test exists to catch.

```sh
cd ~/wt/2ba766a0
git apply ~/repos/grpc-go/verify/instrumentation/c3/c3_observer_2ba766a0.patch
go test -race -v -count=1 -run '^Test$/^ListenerWrapper_RouteConfigUpdate_ReleasesSupersededResources$' ./internal/xds/server   # control
git apply ~/repos/grpc-go/verify/instrumentation/c3/c3_mutant_reintroduce_leak_2ba766a0.patch
go test -race -v -count=1 -run '^Test$/^ListenerWrapper_RouteConfigUpdate_ReleasesSupersededResources$' ./internal/xds/server
git checkout -- .
```

```console
=== unmutated production + observer
    listener_wrapper_test.go:150: VERIFY C3 observer, after all other cleanups: test failed=false | filtersCreated=1 filtersClosed=1 interceptorsBuilt=17 interceptorsClosed=17
--- PASS: Test (0.00s)
    --- PASS: Test/ListenerWrapper_RouteConfigUpdate_ReleasesSupersededResources (0.00s)
ok  	google.golang.org/grpc/internal/xds/server	1.045s
=== mutant production + observer
    listener_wrapper_test.go:208: Closed 0 interceptor instances, want 3
    listener_wrapper_test.go:150: VERIFY C3 observer, after all other cleanups: test failed=true | filtersCreated=1 filtersClosed=0 interceptorsBuilt=6 interceptorsClosed=0
--- FAIL: Test (0.00s)
    --- FAIL: Test/ListenerWrapper_RouteConfigUpdate_ReleasesSupersededResources (0.00s)
FAIL	google.golang.org/grpc/internal/xds/server	0.049s
```

`verifyCounts` (called from line 204; line 208 with the observer patch applied) fails after the second update and calls `t.FailNow()`; the test exits and, after every registered cleanup, `interceptorsClosed=0` of 6 built. Had `l.Close()` (or any failure-safe wrapper teardown) run, the 3 interceptors of the then-active configuration would have been closed (the mutation only suppresses release of *superseded* configurations; `filterChainManager.stop()` still closes the active one), so wrapper teardown did not execute. `verify/repro/c3_repro.sh` replays both parts end to end.

### Impact reasoning

This is a test-hygiene defect, not a production defect. It has no effect while the test passes: the passing run ends with `filtersClosed=1`, `interceptorsClosed=17/17`. It only manifests when one of the 12 fatal-assertion sites between the first RDS update and line 252 fires — i.e. precisely when the regression test is doing its job — and what is left behind is in-process only: one `countingFilterBuilder` filter reference, the active configuration's `countingInterceptor`s, and a `listenerWrapper` whose `closed` event never fires. These fakes hold no goroutines, sockets or files (the raw listener *is* closed by the helper's cleanup; the hand-built `rdsHandler` has no watches to cancel), so there is no cross-test interference and nothing for the package's goroutine leak check to trip on today. The cost is latent: if the helper or the fakes later acquire real resources (xDS watches, cert providers, goroutines), a failing assertion would then also leak them and could surface as a secondary, misleading leak-check failure. Fix is a one-liner: have the helper register `t.Cleanup(func() { l.Close() })` (from reading `listenerWrapper.Close` on this branch, a second `Close` would run `filterChain.stop()` again and double-release, so the cleanup needs a once-guard or the explicit `l.Close()` calls must be reconciled with it; not exercised here).
