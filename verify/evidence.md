## Environment

All commands were run with `go version go1.26.3 linux/amd64` on Linux. Each claim targets its own branch in [kaitranntt-evals/grpc-go-xds-rds-interceptor-lifecycle-leak](https://github.com/kaitranntt-evals/grpc-go-xds-rds-interceptor-lifecycle-leak); every claim was adjudicated in a detached worktree of that branch, with production code untouched except where a section says a mutation patch was applied (and reverted).

The scripts under `verify/repro/` replay the evidence end to end: each one fetches its claim branch, creates a throwaway worktree, copies the files it needs from `verify/repro/testdata/` and prints the filtered `go test` output shown below. A non-zero exit status from a script only reflects that the last `go test` it ran reported a failing test, which for these repros is the observation itself.

```sh
# from the root of this branch's checkout
bash verify/repro/c1_repro.sh
bash verify/repro/c2_repro.sh
bash verify/repro/c3_repro.sh
# if github.com is not directly reachable, point the scripts at a mirror of the claims repository:
#   CLAIMS_REPO_URL=<url> bash verify/repro/c1_repro.sh
```

The archived eval fixtures (`eval_tests.zip`) were extracted and copied byte-exact to the paths the audit prompt names. They are not committed here; their SHA-256 sums are:

```console
653e04aede25e114b0c9acb3b7e553dd5a68b48513a29481791f82d38537ec50  tests/eval_rds_error_after_success_test.go
2c37e0e73dade5842b549daf0471ddca6405b19554af1e084b261ff7c215319e  tests/eval_xds_server_interceptor_leak_test.go
```

## C1

Target: [evalon/grpc-go-xd-e43b07e4](https://github.com/kaitranntt-evals/grpc-go-xds-rds-interceptor-lifecycle-leak/tree/evalon/grpc-go-xd-e43b07e4) at commit `9212de947a6061139ddff7207683d468e764d4bc`.

### What the code under test does

`internal/xds/server/filter_chain_manager.go` on the claim branch (there is no `applyConfiguration` on this branch; the equivalent entry points are `listenerWrapper.handleRDSUpdate`, `filterChain.constructUsableRouteConfiguration` and `filterChain.storeUsableRouteConfiguration`):

```go
func (fc *filterChain) storeUsableRouteConfiguration(urc *usableRouteConfiguration) {
	if urc.err != nil {
		fc.releaseServerFilters()
	}
	if prev := fc.usableRouteConfiguration.Swap(urc); prev != nil {
		prev.close()
	}
}

func (fc *filterChain) stop() {
	if urc := fc.usableRouteConfiguration.Load(); urc != nil {
		urc.close()
	}
	fc.releaseServerFilters()
}
```

`constructUsableRouteConfiguration` also calls `fc.releaseServerFilters()` on the references held for the previous configuration before it returns the new configuration, i.e. before `storeUsableRouteConfiguration` closes the previous configuration's interceptors.

### Instrumentation

Two trace tests, both using a server HTTP filter whose `Close` and whose interceptors' `Close` append to an ordered event log (`filter-close F1`, `icpt-close F1/I1 (parent-filter-already-closed=<bool>)`):

- [testdata/c1_cleanup_order_trace_test.go](https://github.com/kaitranntt-evals/grpc-go/blob/verify/grpc-go-xds-rds-interceptor-lifecycle-leak-v-71219211/verify/repro/testdata/c1_cleanup_order_trace_test.go) (package `server`): starts a real xDS management server and `listenerWrapper`, waits for a good LDS + RDS configuration (one filter chain, two routes, so two interceptors and two references on one shared filter), then (a) invokes the captured RDS watcher's `ResourceError` callback, (b) calls `handleRDSUpdate` directly with an error and no data, (c) closes the listener while the route configuration is healthy. It also reads the filter's reference count from the listener's filter cache.
- [testdata/c1_e2e_trace_test.go](https://github.com/kaitranntt-evals/grpc-go/blob/verify/grpc-go-xds-rds-interceptor-lifecycle-leak-v-71219211/verify/repro/testdata/c1_e2e_trace_test.go) (package `xds_test`): a real `xds.NewGRPCServer` plus management server and client. One test pushes an RDS update whose route disables the filter through a per-route `FilterConfig{Disabled: true}` override (honoured only with `GRPC_EXPERIMENTAL_XDS_EXT_PROC_ON_CLIENT=true`, which the test sets the same way the archived fixture does). The other parks an RPC inside `AllowRPC` of the generation-1 interceptor until its context is cancelled, pushes an RDS update that retires generation 1, and then stops the server while that RPC is still parked.

### Command and output

```sh
bash verify/repro/c1_repro.sh
```

```console
worktree: /tmp/tmp.HlCxnrjH18/wt @ 9212de947a6061139ddff7207683d468e764d4bc
=== internal/xds/server: RDS no-data error after success, and listener shutdown
    c1_cleanup_order_trace_test.go:293: VERIFY-C1 before listener Close: filter-refcount=2 events=[]
    c1_cleanup_order_trace_test.go:296: VERIFY-C1 after listener Close: filter-refcount=0
    c1_cleanup_order_trace_test.go:298: VERIFY-C1 event #0: icpt-close F1/I1 (parent-filter-already-closed=false)
    c1_cleanup_order_trace_test.go:298: VERIFY-C1 event #1: icpt-close F1/I2 (parent-filter-already-closed=false)
    c1_cleanup_order_trace_test.go:298: VERIFY-C1 event #2: filter-close F1
    c1_cleanup_order_trace_test.go:245: VERIFY-C1 before error update: interceptors-built=2 filter-refcount=2 (references held only by the filter chain; cache entry holds none) events=[]
    c1_cleanup_order_trace_test.go:258: VERIFY-C1 after no-data error update: filter-refcount=0
    c1_cleanup_order_trace_test.go:260: VERIFY-C1 event #0: filter-close F1
    c1_cleanup_order_trace_test.go:260: VERIFY-C1 event #1: icpt-close F1/I1 (parent-filter-already-closed=true)
    c1_cleanup_order_trace_test.go:260: VERIFY-C1 event #2: icpt-close F1/I2 (parent-filter-already-closed=true)
    c1_cleanup_order_trace_test.go:265: VERIFY-C1 usable route configuration error now: verify: route1 removed
    c1_cleanup_order_trace_test.go:267: VERIFY-C1 ORDER VIOLATION on no-data error update: interceptor F1/I1 closed after its parent filter F1
    c1_cleanup_order_trace_test.go:275: VERIFY-C1 before error update: filter-refcount=2 events=[]
    c1_cleanup_order_trace_test.go:280: VERIFY-C1 after no-data error update: filter-refcount=0
    c1_cleanup_order_trace_test.go:282: VERIFY-C1 event #0: filter-close F1
    c1_cleanup_order_trace_test.go:282: VERIFY-C1 event #1: icpt-close F1/I1 (parent-filter-already-closed=true)
    c1_cleanup_order_trace_test.go:282: VERIFY-C1 event #2: icpt-close F1/I2 (parent-filter-already-closed=true)
    c1_cleanup_order_trace_test.go:285: VERIFY-C1 ORDER VIOLATION on no-data error update: interceptor F1/I1 closed after its parent filter F1
--- FAIL: Test (0.04s)
    --- PASS: Test/VerifyC1_ListenerClose_CloseOrder (0.02s)
    --- FAIL: Test/VerifyC1_NoDataErrorUpdate_CloseOrder (0.01s)
    --- FAIL: Test/VerifyC1_NoDataErrorUpdate_DirectHandleRDSUpdate_CloseOrder (0.01s)
FAIL
FAIL	google.golang.org/grpc/internal/xds/server	0.076s
FAIL
=== test/xds: live xDS server, RDS update that disables the filter, and shutdown with a parked retired reader
    c1_e2e_trace_test.go:269: VERIFY-C1 events after initial RPC: ["icpt-built F1/I1"]
    c1_e2e_trace_test.go:278: VERIFY-C1 events after RDS update disabling the filter (server still running): ["icpt-built F1/I1" "filter-close F1" "icpt-close F1/I1 (parent-filter-already-closed=true)"]
    c1_e2e_trace_test.go:283: VERIFY-C1 event #0: icpt-built F1/I1
    c1_e2e_trace_test.go:283: VERIFY-C1 event #1: filter-close F1
    c1_e2e_trace_test.go:283: VERIFY-C1 event #2: icpt-close F1/I1 (parent-filter-already-closed=true)
    c1_e2e_trace_test.go:286: VERIFY-C1 ORDER VIOLATION on RDS update: interceptor F1/I1 closed after its parent filter F1
    c1_e2e_trace_test.go:319: VERIFY-C1 RPC parked in AllowRPC of interceptor F1/I1
    c1_e2e_trace_test.go:332: VERIFY-C1 before Stop, reader still parked: retired interceptor F1/I1 already closed=true, parent filter closed=false
    c1_e2e_trace_test.go:333: VERIFY-C1 events before Stop: ["icpt-built F1/I1" "allowrpc-enter F1/I1" "icpt-built F1/I2" "icpt-close F1/I1 (parent-filter-already-closed=false)"]
    c1_e2e_trace_test.go:356: VERIFY-C1 event #0: icpt-built F1/I1
    c1_e2e_trace_test.go:356: VERIFY-C1 event #1: allowrpc-enter F1/I1
    c1_e2e_trace_test.go:356: VERIFY-C1 event #2: icpt-built F1/I2
    c1_e2e_trace_test.go:356: VERIFY-C1 event #3: icpt-close F1/I1 (parent-filter-already-closed=false)
    c1_e2e_trace_test.go:356: VERIFY-C1 event #4: server-stop-begin
    c1_e2e_trace_test.go:356: VERIFY-C1 event #5: icpt-close F1/I2 (parent-filter-already-closed=false)
    c1_e2e_trace_test.go:356: VERIFY-C1 event #6: filter-close F1
    c1_e2e_trace_test.go:356: VERIFY-C1 event #7: allowrpc-return F1/I1
    c1_e2e_trace_test.go:356: VERIFY-C1 event #8: server-stop-end
    c1_e2e_trace_test.go:361: VERIFY-C1 no interceptor was closed after its parent filter during shutdown
--- FAIL: Test (0.08s)
    --- FAIL: Test/VerifyC1E2E_RDSUpdateDisablingFilter_CloseOrder (0.04s)
    --- PASS: Test/VerifyC1E2E_ShutdownWithCancellationHeldRetiredReader_CloseOrder (0.03s)
FAIL
FAIL	google.golang.org/grpc/test/xds	0.141s
FAIL
```

### Archived fixtures on the same branch

```sh
cp tests/eval_rds_error_after_success_test.go     <worktree>/internal/xds/server/
cp tests/eval_xds_server_interceptor_leak_test.go <worktree>/test/xds/
cd <worktree>
go test -race -v -count=1 -run '^Test$/^Eval_ServerSideXDS_ErrorUpdateReleasesServerFilterReferences$' ./internal/xds/server
go test -v -run '^Test$/^Eval_ServerSideXDS_InterceptorBeforeFilterCloseOrder$' ./test/xds -race -count=1
go test -race -v -count=1 -timeout=60s -run '^Test$/^Eval_ServerSideXDS_StopWith' ./test/xds
go test -race -v -count=1 -timeout=120s -run '^Test$/^Eval_' ./test/xds
```

Key lines (filtered with `grep -E '^\s*--- |^(ok|FAIL|PASS)|eval_.*_test.go:[0-9]+'`), in command order:

```console
--- PASS: Test (0.03s)
    --- PASS: Test/Eval_ServerSideXDS_ErrorUpdateReleasesServerFilterReferences (0.03s)
PASS
ok  	google.golang.org/grpc/internal/xds/server	1.071s
    eval_xds_server_interceptor_leak_test.go:1664: dependency order violation: filter closed at event #0 before retired interceptor closed at event #2 (events: [{kind:filter-close id:F1} {kind:icpt-close id:F1/I1} {kind:icpt-close id:F1/I2}])
--- FAIL: Test (0.12s)
    --- FAIL: Test/Eval_ServerSideXDS_InterceptorBeforeFilterCloseOrder (0.11s)
FAIL
FAIL	google.golang.org/grpc/test/xds	0.172s
FAIL
--- PASS: Test (0.58s)
    --- PASS: Test/Eval_ServerSideXDS_StopWithAllowRPCAwaitingContextCancel (0.04s)
    --- PASS: Test/Eval_ServerSideXDS_StopWithConnectionAcceptanceOverlap (0.01s)
    --- PASS: Test/Eval_ServerSideXDS_StopWithSupersededInterceptorCleanupBlocked (0.53s)
PASS
ok  	google.golang.org/grpc/test/xds	1.617s
--- FAIL: Test (5.21s)
    --- FAIL: Test/Eval_ServerSideXDS_InterceptorBeforeFilterCloseOrder (0.06s)
    --- PASS: Test/Eval_ServerSideXDS_InterceptorLeak_MultiGenerationRDSUpdate (0.04s)
    --- PASS: Test/Eval_ServerSideXDS_InterceptorLeak_RDSUpdate (0.03s)
    --- PASS: Test/Eval_ServerSideXDS_InterceptorSwapOrder (0.03s)
    --- FAIL: Test/Eval_ServerSideXDS_PartialRouteFailure_ClosesInterceptors (2.03s)
    --- FAIL: Test/Eval_ServerSideXDS_PartialVirtualHostFailure_ClosesInterceptors (2.03s)
    --- PASS: Test/Eval_ServerSideXDS_ReplacementRPCWhileAllowRPCHeld (0.02s)
    --- PASS: Test/Eval_ServerSideXDS_ReplacementRPCWhileRetiredCloseBlocked (0.04s)
    --- PASS: Test/Eval_ServerSideXDS_ReplacementRPCWhileRetiredCloseBlocksAcceptedRPC (0.33s)
    --- FAIL: Test/Eval_ServerSideXDS_ReplacementRPCWhileServerFilterCloseHeld (0.02s)
    --- PASS: Test/Eval_ServerSideXDS_StopWithAllowRPCAwaitingContextCancel (0.02s)
    --- PASS: Test/Eval_ServerSideXDS_StopWithConnectionAcceptanceOverlap (0.02s)
    --- PASS: Test/Eval_ServerSideXDS_StopWithSupersededInterceptorCleanupBlocked (0.52s)
FAIL
FAIL	google.golang.org/grpc/test/xds	5.243s
FAIL
```

The three other failing fixtures in the last run (`PartialRouteFailure`, `PartialVirtualHostFailure`, `ReplacementRPCWhileServerFilterCloseHeld`) measure behaviors outside this claim and were not investigated.

### Part: cleanup_order

Observed. In three independent runs a parent filter's `Close` ran before the `Close` of the retired interceptors built from it:

- actual RDS `ResourceError` after a good configuration: `event #0: filter-close F1`, then `event #1: icpt-close F1/I1 (parent-filter-already-closed=true)`, `event #2: icpt-close F1/I2 (parent-filter-already-closed=true)`;
- live server, RDS update that disables the filter on the route: `event #1: filter-close F1`, then `event #2: icpt-close F1/I1 (parent-filter-already-closed=true)`;
- archived fixture `Eval_ServerSideXDS_InterceptorBeforeFilterCloseOrder`: `dependency order violation: filter closed at event #0 before retired interceptor closed at event #2`.

The filter's reference count went from 2 to 0 in the same step (`filter-refcount=2` before, `filter-refcount=0` after), so it is the final parent-filter reference that is released ahead of the interceptors.

### Part: no_data_error_trigger

Observed. Before the error, `interceptors-built=2 filter-refcount=2 (references held only by the filter chain; cache entry holds none)`: the two references are exactly the two taken by the filter chain's routes, so the listener's filter cache keeps no reference of its own. Delivering an error with no usable data, both through the xDS client's `ResourceError` callback and through a direct `handleRDSUpdate` call, dropped the count to 0, closed the filter and only then closed the two retired interceptors (`VERIFY-C1 ORDER VIOLATION on no-data error update: interceptor F1/I1 closed after its parent filter F1`). The usable route configuration afterwards carried the error (`usable route configuration error now: verify: route1 removed`).

### Part: shutdown_trigger

Not observed; the stated retention condition does not arise on this branch. With an RPC parked in `AllowRPC` of the retired interceptor `F1/I1` (held until its context is cancelled):

- the RDS update closed the retired interceptor immediately, while its reader was still parked: `before Stop, reader still parked: retired interceptor F1/I1 already closed=true, parent filter closed=false`;
- at `server-stop-begin` there was therefore no retired interceptor awaiting cleanup; shutdown closed the current interceptor and then the filter: `event #5: icpt-close F1/I2 (parent-filter-already-closed=false)`, `event #6: filter-close F1`;
- the parked reader returned afterwards (`event #7: allowrpc-return F1/I1`), and no interceptor `Close` followed the filter's `Close` (`no interceptor was closed after its parent filter during shutdown`).

Listener close on a healthy configuration shows the same order (`icpt-close F1/I1`, `icpt-close F1/I2`, then `filter-close F1`, every interceptor reporting `parent-filter-already-closed=false`), and the three archived `Eval_ServerSideXDS_StopWith*` fixtures pass. What the shutdown run does show is that the parked RPC was still inside `AllowRPC` of an interceptor after both that interceptor and its parent filter had been closed; that is a different property from the one this part states.

### Impact reasoning

A server filter is the owner of state shared by the interceptors it builds, and the code base's own teardown (`filterChain.stop`) closes interceptors first and the filter last. On this branch two update paths invert that: the interceptors' `Close` runs against a parent whose `Close` has already returned. Any filter whose interceptors touch filter-owned state while closing (a shared cache, a client, a metrics handle) sees that state already torn down.

How ordinary the trigger is: the no-data error path needs only a RouteConfiguration that was served successfully and then reports a resource error with no data, for example the control plane removing the route configuration. The disabling-update path needs `GRPC_EXPERIMENTAL_XDS_EXT_PROC_ON_CLIENT=true`: with the variable switched off in the same test, the xDS client NACKed the update (`Sending NACK for resource type: "type.googleapis.com/envoy.config.route.v3.RouteConfiguration" ... filter override "verify-c1-trace": no filter implementation found for ""`) and the filter was not closed until shutdown. Ordinary RDS replacements that keep the filter enabled, and server shutdown, were observed to close interceptors before the filter.

No configuration workaround was found; a filter author can only make interceptor `Close` tolerate a closed parent.

## C2

Target: [evalon/grpc-go-xd-9207949e](https://github.com/kaitranntt-evals/grpc-go-xds-rds-interceptor-lifecycle-leak/tree/evalon/grpc-go-xd-9207949e) at commit `58a0d1a638e776ee99b423f1dae6d8692598ea65`.

### Naming drift

`test/xds/xds_server_interceptor_leak_test.go` does not exist on this branch. The branch adds two lifecycle regressions:

- `TestListenerWrapper_RDSUpdatesReleaseSupersededRouteConfiguration` in `internal/xds/server/listener_wrapper_test.go` (named by the claim);
- `TestServerSideXDS_FilterStateRetention_AcrossRDSUpdates` in `test/xds/xds_server_filter_state_retention_test.go` (the nearest equivalent of the `stopServer()` reference).

### Cleanup registration (source)

`TestListenerWrapper_RDSUpdatesReleaseSupersededRouteConfiguration` creates a real TCP listener with `testutils.LocalTCPListener()`, wraps it in a `listenerWrapper`, and has no `defer` and no `t.Cleanup` for either. Its only cleanup is the `lw.Close()` call near the end of the function, after seven `t.Fatalf` sites that follow resource creation (two of them inside the `verifyConnUsable` helper):

```go
	// Recover and then close the listener, which should release everything.
	sendUpdate(rdsWatcherUpdate{data: routeConfigWithRoutes(numRoutes)})
	verifyConnUsable()
	if err := lw.Close(); err != nil {
		t.Fatalf("listenerWrapper.Close() failed: %v", err)
	}
```

`TestServerSideXDS_FilterStateRetention_AcrossRDSUpdates` registers cleanup up front:

```go
	lis, stopServer := setupGRPCServer(t, bootstrapContents, opt)
	defer stopServer()
```

### Instrumentation

- [testdata/c2_test_instrumentation.patch](https://github.com/kaitranntt-evals/grpc-go/blob/verify/grpc-go-xds-rds-interceptor-lifecycle-leak-v-71219211/verify/repro/testdata/c2_test_instrumentation.patch): test-only. In the unit regression it stores `lis` and the counting filter builder in package variables and adds an opt-in `t.Fatalf` right after the first update. In the end-to-end regression it adds one deferred log line, registered before `defer stopServer()` so that it runs after it, printing the tracker counters and the result of dialing the server's listener.
- [testdata/c2_fatal_cleanup_probe_test.go](https://github.com/kaitranntt-evals/grpc-go/blob/verify/grpc-go-xds-rds-interceptor-lifecycle-leak-v-71219211/verify/repro/testdata/c2_fatal_cleanup_probe_test.go): runs the unit regression as a subtest and, once that subtest has fully exited (its defers and `t.Cleanup` functions included), dials the listener's address, calls `Close()` on the listener (a nil error means nobody had closed it before), and prints the filter and interceptor counters.
- [testdata/c2_mutation_reintroduce_leak.patch](https://github.com/kaitranntt-evals/grpc-go/blob/verify/grpc-go-xds-rds-interceptor-lifecycle-leak-v-71219211/verify/repro/testdata/c2_mutation_reintroduce_leak.patch): a one-line production mutation that stops `updateUsableRouteConfiguration` from closing the superseded configuration, so that each regression's own `t.Fatalf` fires without any injected assertion. Applied only inside the throwaway worktree.

### Command and output

```sh
bash verify/repro/c2_repro.sh
```

```console
worktree: /tmp/tmp.giUIgsCurQ/wt @ 58a0d1a638e776ee99b423f1dae6d8692598ea65
=== control: regression passes
    c2_fatal_cleanup_probe_test.go:30: VERIFY-C2 regression subtest exited: passed=true injectFatal=false
    c2_fatal_cleanup_probe_test.go:33: VERIFY-C2 filters: created=1 closed=1 (still open=0)
    c2_fatal_cleanup_probe_test.go:34: VERIFY-C2 interceptors: created=306 closed=306 (still open=0)
    c2_fatal_cleanup_probe_test.go:41: VERIFY-C2 dial listener 127.0.0.1:33349 after subtest exit: err=dial tcp 127.0.0.1:33349: connect: connection refused (nil => listening socket still open)
    c2_fatal_cleanup_probe_test.go:45: VERIFY-C2 listener.Close() from probe: err=close tcp 127.0.0.1:33349: use of closed network connection (nil => the regression left the listener open)
    c2_fatal_cleanup_probe_test.go:49: VERIFY-C2 all resources were closed by the time the regression exited
--- PASS: Test (0.01s)
    --- PASS: Test/VerifyC2_ResourcesAfterRegressionExits (0.01s)
        --- PASS: Test/VerifyC2_ResourcesAfterRegressionExits/regression (0.00s)
PASS
ok  	google.golang.org/grpc/internal/xds/server	1.045s
=== injected t.Fatalf after the first update
    listener_wrapper_test.go:159: VERIFY-C2 injected fatal assertion after update 1
    c2_fatal_cleanup_probe_test.go:30: VERIFY-C2 regression subtest exited: passed=false injectFatal=true
    c2_fatal_cleanup_probe_test.go:33: VERIFY-C2 filters: created=1 closed=0 (still open=1)
    c2_fatal_cleanup_probe_test.go:34: VERIFY-C2 interceptors: created=3 closed=0 (still open=3)
    c2_fatal_cleanup_probe_test.go:41: VERIFY-C2 dial listener 127.0.0.1:43841 after subtest exit: err=<nil> (nil => listening socket still open)
    c2_fatal_cleanup_probe_test.go:45: VERIFY-C2 listener.Close() from probe: err=<nil> (nil => the regression left the listener open)
    c2_fatal_cleanup_probe_test.go:47: VERIFY-C2 RESOURCES LEFT OPEN after the regression exited: listenerStillOpen=true filtersStillOpen=1
--- FAIL: Test (0.01s)
    --- FAIL: Test/VerifyC2_ResourcesAfterRegressionExits (0.01s)
        --- FAIL: Test/VerifyC2_ResourcesAfterRegressionExits/regression (0.00s)
FAIL
FAIL	google.golang.org/grpc/internal/xds/server	0.053s
FAIL
=== production mutation (leak reintroduced): the regression's own t.Fatalf fires
    listener_wrapper_test.go:167: After 2 updates, 6 interceptors are still open, want 3
    c2_fatal_cleanup_probe_test.go:30: VERIFY-C2 regression subtest exited: passed=false injectFatal=false
    c2_fatal_cleanup_probe_test.go:33: VERIFY-C2 filters: created=1 closed=0 (still open=1)
    c2_fatal_cleanup_probe_test.go:34: VERIFY-C2 interceptors: created=6 closed=0 (still open=6)
    c2_fatal_cleanup_probe_test.go:41: VERIFY-C2 dial listener 127.0.0.1:33079 after subtest exit: err=<nil> (nil => listening socket still open)
    c2_fatal_cleanup_probe_test.go:45: VERIFY-C2 listener.Close() from probe: err=<nil> (nil => the regression left the listener open)
    c2_fatal_cleanup_probe_test.go:47: VERIFY-C2 RESOURCES LEFT OPEN after the regression exited: listenerStillOpen=true filtersStillOpen=1
--- FAIL: Test (0.01s)
    --- FAIL: Test/VerifyC2_ResourcesAfterRegressionExits (0.01s)
        --- FAIL: Test/VerifyC2_ResourcesAfterRegressionExits/regression (0.00s)
FAIL
FAIL	google.golang.org/grpc/internal/xds/server	0.100s
FAIL
=== same mutation, end-to-end regression (uses defer stopServer())
    xds_server_filter_state_retention_test.go:784: Timeout after RDS update 1: created 2 interceptors (want 2), destroyed 0 (want 1)
    xds_server_filter_state_retention_test.go:646: VERIFY-C2 e2e at test exit (failed=true): filters created=1 destroyed=1, interceptors created=2 destroyed=1, dial listener 127.0.0.1:38837 err=dial tcp 127.0.0.1:38837: connect: connection refused
--- FAIL: Test (10.04s)
    --- FAIL: Test/ServerSideXDS_FilterStateRetention_AcrossRDSUpdates (10.03s)
FAIL
FAIL	google.golang.org/grpc/test/xds	10.120s
FAIL
```

### What this shows

- Control: when the unit regression passes, the listener is closed (`connection refused`, `use of closed network connection`) and `filters: created=1 closed=1`.
- Early fatal assertion after resource creation, injected (`VERIFY-C2 injected fatal assertion after update 1`): after the regression exited, `dial listener ... err=<nil>` and `listener.Close() from probe: err=<nil>`, so the TCP listener was still open and accepting, and `filters: created=1 closed=0`, `interceptors: created=3 closed=0`.
- Early fatal assertion fired by the regression itself under the mutation (`After 2 updates, 6 interceptors are still open, want 3`): same result, listener still open, `filters: created=1 closed=0`, `interceptors: created=6 closed=0`.
- The end-to-end regression under the same mutation failed with its own `t.Fatalf` (`Timeout after RDS update 1: ...`), and its deferred `stopServer()` still ran: `filters created=1 destroyed=1`, listener dial `connection refused`. That half of the claim does not hold: its cleanup is not confined to the successful path.

### Impact reasoning

The effect is confined to the test binary and to runs in which `TestListenerWrapper_RDSUpdatesReleaseSupersededRouteConfiguration` already fails: one listening TCP socket on an ephemeral loopback port, one filter instance and the live interceptors stay open until the `go test` process for `internal/xds/server` exits. The unfiltered output of the injected-fatal run contained no line mentioning a leak, so nothing in the package's own checks flags it. No effect on a later test was observed. The practical cost is a failure-path hygiene gap in a regression whose purpose is to prove that resources are released.

## C3

Target: [evalon/grpc-go-xd-12356fc9](https://github.com/kaitranntt-evals/grpc-go-xds-rds-interceptor-lifecycle-leak/tree/evalon/grpc-go-xd-12356fc9) at commit `6cadcb95fdd564d05d74a56f72dd556768980048`.

### The two trackers and the assertions that consume them (source)

The branch's lifecycle regressions use two different interceptor trackers.

`closeTrackingInterceptor`, added by this branch in `internal/xds/server/filter_chain_manager_test.go` and consumed by `TestFilterChain_UpdateUsableRouteConfiguration_ReleasesSupersededResources`:

```go
func (ci *closeTrackingInterceptor) Close() {
	if ci.closed.Swap(true) {
		return
	}
	ci.parent.interceptorsClosed.Add(1)
	ci.parent.liveInterceptorsCount.Add(-1)
}
```

```go
		if got, want := fb.interceptorsClosed.Load(), int32(routesPerConfig*(i-1)); got != want {
			t.Fatalf("After update %d: closed %d interceptors, want %d", i, got, want)
		}
		if got, want := fb.liveInterceptorsCount.Load(), int32(routesPerConfig); got != want {
			t.Fatalf("After update %d: %d live interceptors, want %d", i, got, want)
		}
```

`trackingInterceptor`, which already existed in `test/xds/xds_server_filter_state_retention_test.go` and is consumed by the branch's new end-to-end regression `TestServerSideXDS_RouteConfigurationUpdates_ReleaseSupersededResources` in `test/xds/xds_server_rds_update_cleanup_test.go`:

```go
func (i *trackingInterceptor) Close() {
	i.parent.interceptorsDestroyed.Add(1)
}
```

```go
		if got, want := interceptorsDestroyed.Load(), int32(i); got != want {
			t.Fatalf("After update %d: destroyed %d interceptor instances, want: %d", i, got, want)
		}
```

### Instrumentation

- [testdata/c3_double_close_unit_probe_test.go](https://github.com/kaitranntt-evals/grpc-go/blob/verify/grpc-go-xds-rds-interceptor-lifecycle-leak-v-71219211/verify/repro/testdata/c3_double_close_unit_probe_test.go): builds one `closeTrackingInterceptor`, calls `Close()` twice on that instance, prints the counters after each call, then applies the regression's exact-count assertions for one expected close.
- [testdata/c3_double_close_e2e_probe_test.go](https://github.com/kaitranntt-evals/grpc-go/blob/verify/grpc-go-xds-rds-interceptor-lifecycle-leak-v-71219211/verify/repro/testdata/c3_double_close_e2e_probe_test.go): the same for one `trackingInterceptor`.
- [testdata/c3_mutation_double_close.patch](https://github.com/kaitranntt-evals/grpc-go/blob/verify/grpc-go-xds-rds-interceptor-lifecycle-leak-v-71219211/verify/repro/testdata/c3_mutation_double_close.patch): production mutation making `updateUsableRouteConfiguration` call `close()` twice on the superseded configuration, so that every retired interceptor really receives a duplicate `Close`. Applied only inside the throwaway worktree.

### Command and output

```sh
bash verify/repro/c3_repro.sh
```

```console
worktree: /tmp/tmp.zxnOiDojIt/wt @ 6cadcb95fdd564d05d74a56f72dd556768980048
=== double Close() on closeTrackingInterceptor (unit regression's tracker)
    c3_double_close_unit_probe_test.go:16: VERIFY-C3 unit tracker, after build:        interceptorsCreated=1 interceptorsClosed=0 liveInterceptorsCount=1
    c3_double_close_unit_probe_test.go:18: VERIFY-C3 unit tracker, after 1st Close():  interceptorsCreated=1 interceptorsClosed=1 liveInterceptorsCount=0
    c3_double_close_unit_probe_test.go:20: VERIFY-C3 unit tracker, after 2nd Close():  interceptorsCreated=1 interceptorsClosed=1 liveInterceptorsCount=0
    c3_double_close_unit_probe_test.go:36: VERIFY-C3 unit tracker: no exact-count assertion fired after a duplicate Close() on the same instance
--- PASS: Test (0.00s)
    --- PASS: Test/VerifyC3_CloseTrackingInterceptor_DoubleClose (0.00s)
PASS
ok  	google.golang.org/grpc/internal/xds/server	1.084s
=== double Close() on trackingInterceptor (end-to-end regression's tracker)
    c3_double_close_e2e_probe_test.go:27: VERIFY-C3 e2e tracker, after build:        interceptorsCreated=1 interceptorsDestroyed=0
    c3_double_close_e2e_probe_test.go:29: VERIFY-C3 e2e tracker, after 1st Close():  interceptorsCreated=1 interceptorsDestroyed=1
    c3_double_close_e2e_probe_test.go:31: VERIFY-C3 e2e tracker, after 2nd Close():  interceptorsCreated=1 interceptorsDestroyed=2
    c3_double_close_e2e_probe_test.go:34: VERIFY-C3 exact-count assertion FIRED: Destroyed 2 interceptor instances, want: 1
--- FAIL: Test (0.01s)
    --- FAIL: Test/VerifyC3_TrackingInterceptor_DoubleClose (0.00s)
FAIL
FAIL	google.golang.org/grpc/test/xds	0.036s
FAIL
=== production mutation (superseded configuration closed twice): unit regression
--- PASS: Test (0.00s)
    --- PASS: Test/FilterChain_UpdateUsableRouteConfiguration_ReleasesSupersededResources (0.00s)
PASS
ok  	google.golang.org/grpc/internal/xds/server	1.044s
=== production mutation (superseded configuration closed twice): end-to-end regression
    xds_server_rds_update_cleanup_test.go:248: After update 1: destroyed 2 interceptor instances, want: 1
--- FAIL: Test (0.06s)
    --- FAIL: Test/ServerSideXDS_RouteConfigurationUpdates_ReleaseSupersededResources (0.06s)
FAIL
FAIL	google.golang.org/grpc/test/xds	0.103s
FAIL
```

### What this shows

- `closeTrackingInterceptor`: the second `Close()` left every asserted total unchanged (`interceptorsClosed=1 liveInterceptorsCount=0` after both the first and the second call) and no exact-count assertion fired.
- With production code closing each superseded configuration twice, `TestFilterChain_UpdateUsableRouteConfiguration_ReleasesSupersededResources` still passed.
- `trackingInterceptor`: the second `Close()` changed the asserted total (`interceptorsDestroyed=1` then `2`) and the assertion expecting one fired (`Destroyed 2 interceptor instances, want: 1`).
- Under the same mutation the end-to-end regression failed on its exact-count assertion: `After update 1: destroyed 2 interceptor instances, want: 1`.

So the suppression is real for the tracker this branch added for its unit-level regression, and it is not present in the tracker its end-to-end regression consumes.

### Impact reasoning

The branch documents `usableRouteConfiguration.close()` as "must be called at most once per configuration", and the unit regression is the test that exercises `updateUsableRouteConfiguration` and `filterChainManager.stop` directly. Because its tracker swallows repeat calls, that test reports the same totals whether a retired interceptor is closed once or twice, and it stayed green when production code was changed to close twice. On this branch the gap is covered by the end-to-end regression, which fails on the same mutation; it becomes an undetected regression only for someone who runs `./internal/xds/server` alone, or for a double close on a path the end-to-end test does not drive.
