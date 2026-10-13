Observations only. Workspace: `~/repos/grpc-go` (origin = kaitranntt-evals/grpc-go), work branch `verify/grpc-go-xds-rds-interceptor-lifecycle-leak-v-6ecd5cb2` (base 614cb739). Go 1.25.7 linux/amd64.

Common setup used by every section (claim branches live in a second repository, not on origin):

```sh
cd ~/repos/grpc-go
git remote add claims https://github.com/kaitranntt-evals/grpc-go-xds-rds-interceptor-lifecycle-leak
git fetch claims evalon/grpc-go-xd-73a502c4 evalon/grpc-go-xd-f02ebbb2
git worktree add ~/wt-a claims/evalon/grpc-go-xd-73a502c4   # HEAD 65adf6078f06ce5053aef4d997b4f272774a168b
git worktree add ~/wt-b claims/evalon/grpc-go-xd-f02ebbb2   # HEAD 7ea86fa7029876ebc818f6b165f68b385e540404
mkdir -p ~/eval && cd ~/eval && unzip -o eval_tests.zip
# sha256 77fb92bf…15537  tests/eval_rds_error_after_success_test.go
# sha256 7deabe02…fdb5a  tests/eval_xds_server_interceptor_leak_test.go
for w in a b; do
  cp ~/eval/tests/eval_xds_server_interceptor_leak_test.go ~/wt-$w/test/xds/
  cp ~/eval/tests/eval_rds_error_after_success_test.go ~/wt-$w/internal/xds/server/
done
```

Repro files under `verify/repro/` carry `//go:build verify_repro` so they do not affect `go vet ./...` / `go test ./...` in this repo; copy them into `internal/xds/server/` of the target worktree and pass `-tags verify_repro`.

## C1

Claim: route construction returns an error for a later route without closing the interceptor already built for an earlier route. Target branch `evalon/grpc-go-xd-73a502c4` (worktree `~/wt-a`). **Verdict: CONFIRMED.**

### Direct probe of the construction path (unit level)

`verify/repro/c1_c2_partial_failure_test.go` builds a `filterChain` with one HTTP filter and calls the branch's real `constructUsableRouteConfiguration` with one virtual host whose route 1 builds an interceptor and whose route 2 fails in `BuildServerInterceptor`. It then publishes the returned error configuration exactly as `handleRDSUpdate` does (`fc.updateUsableRouteConfiguration`) and finally calls `filterChainManager.stop()`, printing the close count of every interceptor built at each step.

```sh
cp ~/repos/grpc-go/verify/repro/c1_c2_*_test.go ~/wt-a/internal/xds/server/
cd ~/wt-a && go test -tags verify_repro -race -count=1 -v -run 'Test/VerifyPartial' ./internal/xds/server
```

```console
=== RUN   Test/VerifyPartialRouteFailure
    c1_c2_partial_failure_test.go:63: constructUsableRouteConfiguration returned err=virtual host construction: error constructing filter: injected interceptor construction failure, vhs=0
    c1_c2_partial_failure_test.go:69: after error return: interceptor #0 closed 0 time(s)
    c1_c2_partial_failure_test.go:69: after publishing error config: interceptor #0 closed 0 time(s)
    c1_c2_partial_failure_test.go:69: after filterChainManager.stop(): interceptor #0 closed 0 time(s)
    c1_c2_partial_failure_test.go:89: LEAK: unclosed earlier interceptors: afterError=1 afterPublish=1 afterStop=1 (of 1 built)
    --- FAIL: Test/VerifyPartialRouteFailure (0.00s)
```

The earlier route's interceptor is not closed when the error is returned, not when the error configuration is published, and not at shutdown: it is never closed.

### Evaluation fixture against the live xDS server (end to end)

```sh
cd ~/wt-a && go test -v -run '^Test$/^Eval_ServerSideXDS_PartialRouteFailure_ClosesInterceptors$' ./test/xds -race -count=1
```

```console
    eval_xds_server_interceptor_leak_test.go:1194: Partial route conversion failure leaked interceptor: destroyed=1, want=2 (created=2)
--- FAIL: Test (2.11s)
    --- FAIL: Test/Eval_ServerSideXDS_PartialRouteFailure_ClosesInterceptors (2.10s)
FAIL	google.golang.org/grpc/test/xds	2.141s
```

`created=2` = the initial configuration's interceptor + route 1 of the failing update; `destroyed=1` = only the superseded initial interceptor. Route 1's interceptor of the failed update stays open.

### Control (the probe is not biased)

The same probe file on `evalon/grpc-go-xd-f02ebbb2` (`~/wt-b`, with the one-line publish helper calling that branch's `updateRoutingConfiguration` instead) passes, every interceptor closed exactly once:

```console
=== RUN   Test/VerifyPartialRouteFailure
    c1_c2_partial_failure_test.go:69: after error return: interceptor #0 closed 1 time(s)
    --- PASS: Test/VerifyPartialRouteFailure (0.00s)
```

and the fixture `Eval_ServerSideXDS_PartialRouteFailure_ClosesInterceptors` passes there (`--- PASS … (0.03s)`).

### Source location (for the fix, not as evidence)

On the target branch `internal/xds/server/filter_chain_manager.go` `convertVirtualHost` has a deferred cleanup that closes only `serverFilters` on error; `rs[0..i-1].interceptor` are dropped. The branch's diff did not touch this function.

### Impact reasoning

Trigger: a RouteConfiguration (inline or RDS) in which some route's interceptor construction fails after an earlier route in the same virtual host succeeded. The xDS client validates filter configs up front, so this needs a filter whose `BuildServerInterceptor` fails at build time (e.g. RBAC policy compilation, custom filters) — uncommon, and the code comment itself says "shouldn't happen". When it happens the listener correctly serves the error configuration (RPCs fail UNAVAILABLE), but each such update permanently leaks one interceptor per already-built route; a control plane that keeps re-sending the bad resource leaks on every push — the heap growth the original task set out to remove. No workaround short of not sending such configs.

## C2

Claim: virtual-host construction returns an error for a later virtual host without closing the route interceptors already built for an earlier virtual host. Target branch `evalon/grpc-go-xd-73a502c4` (worktree `~/wt-a`). **Verdict: CONFIRMED.**

### Direct probe of the construction path (unit level)

Same file as C1 (`verify/repro/c1_c2_partial_failure_test.go`), second test: virtual host 1 has two routes that both build interceptors, virtual host 2's only route fails.

```sh
cp ~/repos/grpc-go/verify/repro/c1_c2_*_test.go ~/wt-a/internal/xds/server/
cd ~/wt-a && go test -tags verify_repro -race -count=1 -v -run 'Test/VerifyPartial' ./internal/xds/server
```

```console
=== RUN   Test/VerifyPartialVirtualHostFailure
    c1_c2_partial_failure_test.go:63: constructUsableRouteConfiguration returned err=virtual host construction: error constructing filter: injected interceptor construction failure, vhs=0
    c1_c2_partial_failure_test.go:69: after error return: interceptor #0 closed 0 time(s)
    c1_c2_partial_failure_test.go:69: after error return: interceptor #1 closed 0 time(s)
    c1_c2_partial_failure_test.go:69: after publishing error config: interceptor #0 closed 0 time(s)
    c1_c2_partial_failure_test.go:69: after publishing error config: interceptor #1 closed 0 time(s)
    c1_c2_partial_failure_test.go:69: after filterChainManager.stop(): interceptor #0 closed 0 time(s)
    c1_c2_partial_failure_test.go:69: after filterChainManager.stop(): interceptor #1 closed 0 time(s)
    c1_c2_partial_failure_test.go:89: LEAK: unclosed earlier interceptors: afterError=2 afterPublish=2 afterStop=2 (of 2 built)
    --- FAIL: Test/VerifyPartialVirtualHostFailure (0.00s)
FAIL	google.golang.org/grpc/internal/xds/server	0.030s
```

Both interceptors of the fully built earlier virtual host are never closed.

### Evaluation fixture against the live xDS server (end to end)

```sh
cd ~/wt-a && go test -v -run '^Test$/^Eval_ServerSideXDS_Partial(Route|VirtualHost)Failure_ClosesInterceptors$' ./test/xds -race -count=1
```

```console
    eval_xds_server_interceptor_leak_test.go:1378: Partial virtual host conversion failure leaked interceptor: destroyed=1, want=2 (created=2)
--- FAIL: Test (4.06s)
    --- FAIL: Test/Eval_ServerSideXDS_PartialRouteFailure_ClosesInterceptors (2.03s)
    --- FAIL: Test/Eval_ServerSideXDS_PartialVirtualHostFailure_ClosesInterceptors (2.02s)
FAIL	google.golang.org/grpc/test/xds	4.091s
```

### Control

Same probe on `evalon/grpc-go-xd-f02ebbb2` (`~/wt-b`): 

```console
=== RUN   Test/VerifyPartialVirtualHostFailure
    c1_c2_partial_failure_test.go:69: after error return: interceptor #0 closed 1 time(s)
    c1_c2_partial_failure_test.go:69: after error return: interceptor #1 closed 1 time(s)
    --- PASS: Test/VerifyPartialVirtualHostFailure (0.00s)
```

and the fixture `Eval_ServerSideXDS_PartialVirtualHostFailure_ClosesInterceptors` passes there (`--- PASS … (0.03s)`).

### Source location (for the fix, not as evidence)

On the target branch `constructUsableRouteConfiguration`'s error branch closes the accumulated `serverFilters` and returns `&usableRouteConfiguration{err: …}`; the already appended `vhs` (holding the interceptors) are discarded unclosed.

### Impact reasoning

Same trigger class as C1 (an interceptor build failure inside a RouteConfiguration), one level up: any failure in virtual host N leaks every interceptor of virtual hosts 1..N-1, so the leak per bad update is larger than in C1 (all routes of all earlier virtual hosts). Leaked interceptors are unreachable from any later cleanup path, including server stop. The filter reference counts are released correctly, so a filter instance can be closed while its leaked interceptors are still open.

## C3

Claim: the solution's lifecycle test asserts the initial construction count after the first RPC without synchronization guaranteeing both filter chains finished construction. Target branch `evalon/grpc-go-xd-73a502c4` (worktree `~/wt-a`). **Verdict: REFUTED.**

Test under discussion: `test/xds/xds_server_filter_state_retention_test.go`, `TestServerSideXDS_FilterStateRetention_AcrossUpdates_RouteConfigChange` (two RDS filter chains; waits for `servingCh`, makes `EmptyCall`, then asserts `interceptorsCreated == 2`). The claim names no test; this is the only test on the branch matching "initial construction-count assertion following the first RPC" with a SERVING notification.

Assertion text (target branch):

```go
	if _, err := client.EmptyCall(ctx, &testpb.Empty{}); err != nil {
		t.Fatalf("EmptyCall() failed: %v", err)
	}
	...
	if got, want := interceptorsCreated.Load(), int32(interceptorsPerConfig); got != want {
		t.Fatalf("Created %d interceptor instances, want: %d", got, want)
	}
```

### Trace by instrumentation

`verify/c3_instrumentation.patch` (applies to `~/wt-a`, evidence-only) prints timestamps for: the SERVING mode callback, the start of each chain's construction in `instantiateFilterChainRoutingConfigurationsLocked`, completion of all chains, and the two `l.mu` points in `Accept`. With `VERIFY_C3_DELAY=locked` it sleeps 2 s between chain 0 and chain 1 while keeping `l.mu`; with `VERIFY_C3_DELAY=unlocked` it releases `l.mu` during that sleep (counterfactual).

```sh
cd ~/wt-a && git apply ~/repos/grpc-go/verify/c3_instrumentation.patch
T='^Test$/^ServerSideXDS_FilterStateRetention_AcrossUpdates_RouteConfigChange$'
for mode in "" locked unlocked; do
  VERIFY_C3_DELAY=$mode go test -v -run "$T" ./test/xds -race -count=1 2>&1 | grep -n "VERIFY-C3\|retention_test.go\|^--- \|^    --- \|^FAIL\|^ok"
done
```

Unmodified timing:

```console
66:VERIFY-C3 18:11:44.508 modeCallback(SERVING) invoked (l.mu held)
67:VERIFY-C3 18:11:44.508 chain[0] construction starting (l.mu held)
68:VERIFY-C3 18:11:44.508 chain[1] construction starting (l.mu held)
69:VERIFY-C3 18:11:44.508 all 2 chains constructed; interceptor construction done, l.mu still held
172:VERIFY-C3 18:11:44.521 Accept: raw conn accepted, waiting for l.mu
173:VERIFY-C3 18:11:44.521 Accept: connection admitted under l.mu (mode=SERVING)
292:    --- PASS: Test/ServerSideXDS_FilterStateRetention_AcrossUpdates_RouteConfigChange (0.07s)
```

Adversarial ordering attempt — 2 s delay between the two chains, lock kept (`VERIFY_C3_DELAY=locked`). The SERVING notification fires and the client's connection reaches `Accept` *before* chain 1 is built, yet the connection is not admitted (so no RPC can run or complete) until both chains are built; the test still passes with count 2:

```console
66:VERIFY-C3 18:11:46.581 modeCallback(SERVING) invoked (l.mu held)
67:VERIFY-C3 18:11:46.581 chain[0] construction starting (l.mu held)
169:VERIFY-C3 18:11:46.594 Accept: raw conn accepted, waiting for l.mu
171:VERIFY-C3 18:11:48.581 chain[1] construction starting (l.mu held)
172:VERIFY-C3 18:11:48.581 all 2 chains constructed; interceptor construction done, l.mu still held
173:VERIFY-C3 18:11:48.582 Accept: connection admitted under l.mu (mode=SERVING)
301:    --- PASS: Test/ServerSideXDS_FilterStateRetention_AcrossUpdates_RouteConfigChange (2.06s)
303:ok  	google.golang.org/grpc/test/xds	3.098s
```

Counterfactual — same delay but with `l.mu` released during it (`VERIFY_C3_DELAY=unlocked`). Only now does the ordering the claim describes occur, and the assertion catches it:

```console
66:VERIFY-C3 18:11:50.652 modeCallback(SERVING) invoked (l.mu held)
67:VERIFY-C3 18:11:50.652 chain[0] construction starting (l.mu held)
167:VERIFY-C3 18:11:50.667 Accept: raw conn accepted, waiting for l.mu
168:VERIFY-C3 18:11:50.667 Accept: connection admitted under l.mu (mode=SERVING)
180:    xds_server_filter_state_retention_test.go:812: Created 1 interceptor instances, want: 2
215:VERIFY-C3 18:11:52.653 chain[1] construction starting (l.mu held)
220:    --- FAIL: Test/ServerSideXDS_FilterStateRetention_AcrossUpdates_RouteConfigChange (2.07s)
```

Stress run without delay:

```sh
cd ~/wt-a && go test -run '^Test$/^ServerSideXDS_FilterStateRetention_AcrossUpdates_RouteConfigChange$' ./test/xds -race -count=30
```

```console
ok  	google.golang.org/grpc/test/xds	2.850s
```

### Why the ordering is forced

`maybeUpdateFilterChains` takes `l.mu`, calls `switchModeLocked(SERVING)` (the only place the mode becomes SERVING; the callback that closes `servingCh` fires here), swaps in the filter chain manager and runs `instantiateFilterChainRoutingConfigurationsLocked()` for **all** chains, and only then unlocks. `Accept` must take the same `l.mu` and see `mode == SERVING` before it returns a connection; before that every connection is closed. An RPC can therefore only complete on a connection admitted after the critical section that built both chains. The SERVING notification itself is not the guarantee (it fires before construction) — the RPC's dependence on `Accept`'s `l.mu` acquisition is.

## C4

Claim: a route update leaves later active filter chains on their old configuration while waiting for an AllowRPC reader of the first chain's retired configuration, even with nonblocking Close callbacks. Target branch `evalon/grpc-go-xd-f02ebbb2` (worktree `~/wt-b`). **Verdict: CONFIRMED (both parts, and the combined behavior).**

`verify/repro/c4_reader_blocks_later_chain_test.go` uses the branch's real `listenerWrapper.handleRDSUpdate`, `filterChain.updateRoutingConfiguration` and `RouteAndProcess`. Two active filter chains (`chainA`, `chainB`) reference RDS route `route`; each chain has its own tracking HTTP filter. Interceptor `Close()` only logs and returns. One RPC on chain A is parked inside `AllowRPC` of generation 1.

```sh
cp ~/repos/grpc-go/verify/repro/c4_reader_blocks_later_chain_test.go ~/wt-b/internal/xds/server/
cd ~/wt-b && go test -tags verify_repro -race -count=1 -v -run 'Test/VerifyC4' ./internal/xds/server
```

### Part: reader-dependent retirement — CONFIRMED

```console
=== RUN   Test/VerifyC4_ReaderDependentRetirement
    c4_reader_blocks_later_chain_test.go:32: +   0ms chainA: interceptor gen1 built
    c4_reader_blocks_later_chain_test.go:32: +   0ms chainB: interceptor gen1 built
    c4_reader_blocks_later_chain_test.go:32: +   0ms chainA: AllowRPC served by gen1
    c4_reader_blocks_later_chain_test.go:32: +   1ms updateRoutingConfiguration(chainA) called
    c4_reader_blocks_later_chain_test.go:32: +2002ms 2s later: update call still blocked; published=true, old.closed=false
    c4_reader_blocks_later_chain_test.go:32: +2002ms chainA: held AllowRPC reader on gen1 released
    c4_reader_blocks_later_chain_test.go:32: +2002ms chainA: interceptor gen1 Close() called (returns immediately)
    c4_reader_blocks_later_chain_test.go:32: +2002ms updateRoutingConfiguration(chainA) returned
    c4_reader_blocks_later_chain_test.go:166: CONFIRMED (reader-dependent retirement): updateRoutingConfiguration blocked >=2s on a held AllowRPC reader although interceptor Close() is nonblocking
```

The update call publishes the replacement at once (`published=true`) but does not return for the whole 2 s the reader is held; the interceptor `Close()` callback has not even been invoked yet during the block (`old.closed=false`, Close logged only after release), so the wait is on the reader, not on the callback. It returns in the same millisecond the reader is released.

### Part: sequential publication trigger — CONFIRMED, and combined behavior demonstrated

```console
=== RUN   Test/VerifyC4_LaterChainWaitsForFirstChainReader
    c4_reader_blocks_later_chain_test.go:32: +   0ms chainA: interceptor gen1 built
    c4_reader_blocks_later_chain_test.go:32: +   0ms chainB: interceptor gen1 built
    c4_reader_blocks_later_chain_test.go:32: +   0ms chainA: AllowRPC served by gen1
    c4_reader_blocks_later_chain_test.go:32: +   0ms handleRDSUpdate(route) called (generation 2)
    c4_reader_blocks_later_chain_test.go:32: +   0ms chainA: interceptor gen2 built
    c4_reader_blocks_later_chain_test.go:32: +2009ms 2s later, reader still held: chainA replacement published=true, chainB replacement published=false
    c4_reader_blocks_later_chain_test.go:32: +2009ms chainB: AllowRPC served by gen1
    c4_reader_blocks_later_chain_test.go:32: +2009ms listener mutex (needed by Accept to admit new connections) free=false
    c4_reader_blocks_later_chain_test.go:32: +2009ms chainA: held AllowRPC reader on gen1 released
    c4_reader_blocks_later_chain_test.go:32: +2009ms chainA: interceptor gen1 Close() called (returns immediately)
    c4_reader_blocks_later_chain_test.go:32: +2009ms chainB: interceptor gen2 built
    c4_reader_blocks_later_chain_test.go:32: +2009ms chainB: interceptor gen1 Close() called (returns immediately)
    c4_reader_blocks_later_chain_test.go:32: +2009ms handleRDSUpdate(route) returned
    c4_reader_blocks_later_chain_test.go:32: +2009ms after release: chainB replacement published=true
    c4_reader_blocks_later_chain_test.go:32: +2009ms chainB: AllowRPC served by gen2
    c4_reader_blocks_later_chain_test.go:225: CONFIRMED (sequential publication + combined): chain B stayed on its old configuration (aPublished=true, bPublished=false, listenerMuFree=false) until chain A's AllowRPC reader was released
--- FAIL: Test (4.01s)
    --- FAIL: Test/VerifyC4_LaterChainWaitsForFirstChainReader (2.01s)
    --- FAIL: Test/VerifyC4_ReaderDependentRetirement (2.00s)
FAIL	google.golang.org/grpc/internal/xds/server	4.068s
```

Trace: within one `handleRDSUpdate`, chain A's generation 2 is built and published at +0 ms; chain B's generation 2 is not even constructed (`chainB: interceptor gen2 built` appears only at +2009 ms, after `chainA: … reader … released` and chain A's close), i.e. the loop does not reach chain B's `updateRoutingConfiguration` until chain A's returns. While the reader is held an RPC on chain B is served by the *old* generation 1, and `l.mu` is held by the stuck `handleRDSUpdate` (`free=false`).

### Evaluation fixtures on this branch (live server)

```sh
cd ~/wt-b && go test -race -v -count=1 -timeout=120s -run '^Test$/^Eval_ServerSideXDS_' ./test/xds
```

```console
    eval_xds_server_interceptor_leak_test.go:1920: Stop() did not return within 5s while AllowRPC was waiting for context cancellation (shutdown deadlock defect)
    --- PASS: Test/Eval_ServerSideXDS_ReplacementRPCWhileAllowRPCHeld (0.02s)
    --- PASS: Test/Eval_ServerSideXDS_ReplacementRPCWhileRetiredCloseBlocked (0.01s)
    --- PASS: Test/Eval_ServerSideXDS_ReplacementRPCWhileRetiredCloseBlocksAcceptedRPC (0.02s)
    --- PASS: Test/Eval_ServerSideXDS_ReplacementRPCWhileServerFilterCloseHeld (0.01s)
    --- FAIL: Test/Eval_ServerSideXDS_StopWithAllowRPCAwaitingContextCancel (5.02s)
    --- PASS: Test/Eval_ServerSideXDS_StopWithSupersededInterceptorCleanupBlocked (0.51s)
FAIL	google.golang.org/grpc/test/xds	5.866s
```

`ReplacementRPCWhileAllowRPCHeld` passes because it uses a single chain and an existing connection: the first (only) chain's replacement is published before the update call blocks, so the fixture does not observe the later-chain stall. `StopWithAllowRPCAwaitingContextCancel` failing on the live server is the same reader-dependent wait surfacing in `Stop()`.

### Source location (for the fix, not as evidence)

`routing.go` `RouteAndProcess` holds `rc.mu.RLock()` across `interceptor.AllowRPC`; `usableRouteConfiguration.close()` takes `rc.mu.Lock()`; `filterChain.updateRoutingConfiguration` is `Swap(rc).close()`; `handleRDSUpdate` calls it per chain in a loop while holding `l.mu`.

### Impact reasoning

Trigger: an RDS update (or an RDS error / resource-removed update, which goes through the same loop) for a route shared by more than one filter chain, while any RPC on an earlier chain is inside an interceptor's `AllowRPC`. `AllowRPC` is on every RPC's path; it only needs to be slow (external authz call, rate limiter, a filter waiting on context) for the window to be long. For that whole time: (1) later chains keep enforcing the superseded route configuration — e.g. a tightened RBAC policy is not applied to them — while chain A already uses the new one, so the listener is in a mixed state that looks updated; (2) the listener mutex is held, so `Accept` cannot admit new connections and further RDS/LDS updates queue behind it; (3) with an `AllowRPC` that only returns on context cancellation, `Stop()` does not return (fixture failure above). Shared RDS across the IPv4/IPv6 wildcard chains is the default layout in the e2e resources, so the multi-chain precondition is ordinary. No workaround at the configuration level other than making every filter's `AllowRPC` fast.
