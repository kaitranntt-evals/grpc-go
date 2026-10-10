Observations only. Environment: go1.25.7 linux/amd64. Claim-target branches were fetched from `https://github.com/kaitranntt-evals/grpc-go-xds-rds-interceptor-lifecycle-leak` (remote `claims`) into separate worktrees; base commit is `4ee6ac46`. Repro sources are stored as `*.go.txt` under `verify/repro/` so they do not become part of the Go build of this repository; each has a header line saying where to copy it.

```sh
cd ~/repos/grpc-go
git remote add claims https://github.com/kaitranntt-evals/grpc-go-xds-rds-interceptor-lifecycle-leak
git fetch claims evalon/grpc-go-xd-73f6da56 evalon/grpc-go-xd-8410daeb
git worktree add ~/wt-73f claims/evalon/grpc-go-xd-73f6da56   # HEAD ed3cfa07
git worktree add ~/wt-841 claims/evalon/grpc-go-xd-8410daeb   # HEAD 358ddbea
git worktree add ~/wt-base 4ee6ac46
unzip eval_tests.zip -d ~/eval_tests
```

## C1

Target: `evalon/grpc-go-xd-73f6da56` (HEAD `ed3cfa07`), worktree `~/wt-73f`.

### Relevant code order on the branch (context for the runs, not the evidence itself)

`handleRDSUpdate` (holding `l.mu`) calls `fc.constructUsableRouteConfiguration(...)` and then `fc.updateUsableRouteConfiguration(urc)`.

- `constructUsableRouteConfiguration` ends with `for _, sf := range fc.serverFilters { sf.Close() }` (releasing the superseded configuration's server-filter references; `refCountedServerFilter.Close` calls the real `ServerFilter.Close()` when the count reaches 0) and only then returns the new configuration.
- `updateUsableRouteConfiguration` is `fc.usableRouteConfiguration.Swap(urc).closeInterceptors()`: publish first, then close retired interceptors.

So a retired *interceptor's* `Close()` runs after publication, while a retired *server filter's* `Close()` runs before it. A server filter is only retired by an RDS replacement when the replacement no longer references it (no routes, or the filter disabled on every route); a replacement that still uses the filter takes new references first and the count never reaches 0.

### Run 1 — end to end, RPCs on the existing connection (xds.NewGRPCServer + management server)

Uses the eval fixture's own harness (`evalC2C3Setup`, `evalC2C3FilterBuilder`) from `tests/eval_xds_server_interceptor_leak_test.go`.

```sh
cd ~/wt-73f
cp ~/eval_tests/tests/eval_xds_server_interceptor_leak_test.go test/xds/
cp ~/repos/grpc-go/verify/repro/c1c2_server_filter_close_blocked_test.go.txt test/xds/verify_c1c2_test.go
go test -v -race -count=1 -run '^Test$/^Verify_C1C2_' ./test/xds 2>&1 | grep -E "verify_c1c2|^(---|===|ok|FAIL|PASS)|^\s+---|panic"
```

```console
=== RUN   Test/Verify_C1C2_Control_InterceptorCloseHeld_OrdinaryReplacement
    verify_c1c2_test.go:90: [23ms] BEFORE replacement: served by "path1", status msg ""
    verify_c1c2_test.go:93: [24ms] replacement RouteConfiguration pushed to management server
    verify_c1c2_test.go:50: [24ms] interceptor(path1).Close() entered
    verify_c1c2_test.go:103: [24ms] WHILE Close() HELD probe #0: served by "path2", status msg ""
    verify_c1c2_test.go:103: [326ms] WHILE Close() HELD probe #1: served by "path2", status msg ""
    verify_c1c2_test.go:103: [628ms] WHILE Close() HELD probe #2: served by "path2", status msg ""
    verify_c1c2_test.go:103: [930ms] WHILE Close() HELD probe #3: served by "path2", status msg ""
    verify_c1c2_test.go:103: [1.232s] WHILE Close() HELD probe #4: served by "path2", status msg ""
    verify_c1c2_test.go:111: [1.533s] Close() released
    verify_c1c2_test.go:54: [1.533s] interceptor(path1).Close() returning
    verify_c1c2_test.go:116: [1.534s] AFTER release: served by "path2", status msg ""
    verify_c1c2_test.go:124: NO STALL: replacement was routing RPCs while Close() was pending
    verify_c1c2_test.go:50: [1.535s] interceptor(path2).Close() entered
=== RUN   Test/Verify_C1C2_Control_ServerFilterCloseNotInvoked_OrdinaryReplacement
    verify_c1c2_test.go:90: [15ms] BEFORE replacement: served by "path1", status msg ""
    verify_c1c2_test.go:93: [15ms] replacement RouteConfiguration pushed to management server
    verify_c1c2_test.go:50: [16ms] interceptor(path1).Close() entered
    verify_c1c2_test.go:97: [10.016s] the held Close() was never invoked for this replacement
    verify_c1c2_test.go:50: [10.017s] interceptor(path2).Close() entered
    verify_c1c2_test.go:60: [10.017s] ServerFilter.Close() entered
    verify_c1c2_test.go:63: [10.017s] ServerFilter.Close() returning
=== RUN   Test/Verify_C1C2_ServerFilterCloseHeld_ReplacementDisablesFilter
    verify_c1c2_test.go:90: [14ms] BEFORE replacement: served by "path1", status msg ""
    verify_c1c2_test.go:93: [14ms] replacement RouteConfiguration pushed to management server
    verify_c1c2_test.go:60: [15ms] ServerFilter.Close() entered
    verify_c1c2_test.go:103: [16ms] WHILE Close() HELD probe #0: served by "path1", status msg ""
    verify_c1c2_test.go:103: [318ms] WHILE Close() HELD probe #1: served by "path1", status msg ""
    verify_c1c2_test.go:103: [620ms] WHILE Close() HELD probe #2: served by "path1", status msg ""
    verify_c1c2_test.go:103: [921ms] WHILE Close() HELD probe #3: served by "path1", status msg ""
    verify_c1c2_test.go:103: [1.224s] WHILE Close() HELD probe #4: served by "path1", status msg ""
    verify_c1c2_test.go:111: [1.524s] Close() released
    verify_c1c2_test.go:63: [1.524s] ServerFilter.Close() returning
    verify_c1c2_test.go:50: [1.524s] interceptor(path1).Close() entered
    verify_c1c2_test.go:116: [1.524s] AFTER release: served by "<no interceptor>", status msg ""
    verify_c1c2_test.go:122: STALL: RPCs on the existing connection were still routed by the superseded route configuration (interceptor path1) while Close() was pending
=== RUN   Test/Verify_C1C2_ServerFilterCloseHeld_ReplacementWithoutRoutes
    verify_c1c2_test.go:90: [13ms] BEFORE replacement: served by "path1", status msg ""
    verify_c1c2_test.go:93: [13ms] replacement RouteConfiguration pushed to management server
    verify_c1c2_test.go:60: [14ms] ServerFilter.Close() entered
    verify_c1c2_test.go:103: [15ms] WHILE Close() HELD probe #0: served by "path1", status msg ""
    verify_c1c2_test.go:103: [316ms] WHILE Close() HELD probe #1: served by "path1", status msg ""
    verify_c1c2_test.go:103: [617ms] WHILE Close() HELD probe #2: served by "path1", status msg ""
    verify_c1c2_test.go:103: [918ms] WHILE Close() HELD probe #3: served by "path1", status msg ""
    verify_c1c2_test.go:103: [1.22s] WHILE Close() HELD probe #4: served by "path1", status msg ""
    verify_c1c2_test.go:111: [1.521s] Close() released
    verify_c1c2_test.go:63: [1.521s] ServerFilter.Close() returning
    verify_c1c2_test.go:50: [1.521s] interceptor(path1).Close() entered
    verify_c1c2_test.go:116: [1.522s] AFTER release: served by "<no interceptor>", status msg "[xDS node id: 845163e8-71ce-4d46-a980-da14e4d65861]: the incoming RPC did not match a configured Route"
    verify_c1c2_test.go:122: STALL: RPCs on the existing connection were still routed by the superseded route configuration (interceptor path1) while Close() was pending
--- FAIL: Test (14.67s)
    --- PASS: Test/Verify_C1C2_Control_InterceptorCloseHeld_OrdinaryReplacement (1.59s)
    --- FAIL: Test/Verify_C1C2_Control_ServerFilterCloseNotInvoked_OrdinaryReplacement (10.02s)
    --- FAIL: Test/Verify_C1C2_ServerFilterCloseHeld_ReplacementDisablesFilter (1.53s)
    --- FAIL: Test/Verify_C1C2_ServerFilterCloseHeld_ReplacementWithoutRoutes (1.53s)
FAIL	google.golang.org/grpc/test/xds	14.698s
```

What the four cases show:

- `ServerFilterCloseHeld_ReplacementWithoutRoutes` (default configuration, no env vars): the replacement retires the server filter; while its `Close()` is held, all 5 RPCs on the existing connection are still served by the superseded configuration's interceptor `path1`; the replacement's behaviour ("did not match a configured Route") appears 1 ms after `Close()` is released. Stall.
- `ServerFilterCloseHeld_ReplacementDisablesFilter` (`GRPC_EXPERIMENTAL_XDS_EXT_PROC_ON_CLIENT` behaviour enabled so `FilterConfig{disabled:true}` is honoured): same stall; RPCs keep succeeding but through the old `path1` interceptor until release.
- Both stall cases also show `ServerFilter.Close()` being entered *before* `interceptor(path1).Close()`, and `path1` continuing to authorize RPCs after its parent filter's `Close()` was entered.
- `Control_InterceptorCloseHeld_OrdinaryReplacement`: replacement still uses the filter (path1 -> path2); the retired *interceptor's* `Close()` is held; every RPC during the hold is served by `path2`. No stall.
- `Control_ServerFilterCloseNotInvoked_OrdinaryReplacement`: with the same ordinary replacement, `ServerFilter.Close()` is not invoked at all during the replacement (the test's 10 s wait expires; it is only invoked at server stop). This "FAIL" is the expected outcome of the control.

### Run 2 — the route pointer itself (`fc.usableRouteConfiguration`) around `handleRDSUpdate`

```sh
cd ~/wt-73f
cp ~/eval_tests/tests/eval_rds_error_after_success_test.go internal/xds/server/
cp ~/repos/grpc-go/verify/repro/c1c2_route_pointer_test.go.txt internal/xds/server/verify_c1c2_pointer_test.go
go test -v -race -count=1 -run '^Test$/^Verify_C1C2_RoutePointer' ./internal/xds/server 2>&1 | grep -E "verify_c1c2|^(---|===|ok|FAIL|PASS)|^\s+---|panic"
```

```console
=== RUN   Test/Verify_C1C2_RoutePointer_Control_InterceptorCloseHeld
    verify_c1c2_pointer_test.go:138: active route pointer before replacement: 0xc00052cc80 (routes in vh[0]: 1)
    verify_c1c2_pointer_test.go:153: route pointer 500ms into held Close(): 0xc000128080 (routes in vh[0]: 1); published replacement = true
    verify_c1c2_pointer_test.go:158: handleRDSUpdate still blocked while Close() held
    verify_c1c2_pointer_test.go:163: route pointer after Close() released: 0xc000128080 (routes in vh[0]: 1); published replacement = true
=== RUN   Test/Verify_C1C2_RoutePointer_ServerFilterCloseHeld
    verify_c1c2_pointer_test.go:138: active route pointer before replacement: 0xc0002951c0 (routes in vh[0]: 1)
    verify_c1c2_pointer_test.go:153: route pointer 500ms into held Close(): 0xc0002951c0 (routes in vh[0]: 1); published replacement = false
    verify_c1c2_pointer_test.go:158: handleRDSUpdate still blocked while Close() held
    verify_c1c2_pointer_test.go:163: route pointer after Close() released: 0xc0003ffb00 (routes in vh[0]: 0); published replacement = true
    verify_c1c2_pointer_test.go:165: STALL: replacement route pointer was not published while Close() was pending
--- FAIL: Test (1.08s)
    --- PASS: Test/Verify_C1C2_RoutePointer_Control_InterceptorCloseHeld (0.57s)
    --- FAIL: Test/Verify_C1C2_RoutePointer_ServerFilterCloseHeld (0.51s)
FAIL	google.golang.org/grpc/internal/xds/server	1.103s
```

With the server filter's `Close()` held, the pointer is still the pre-replacement object 500 ms into the hold and changes only after release. With the interceptor's `Close()` held, the pointer already is the replacement.

### Run 3 — eval fixture `TestEval_ServerSideXDS_ReplacementRPCWhileRetiredCloseBlocked` (holds the retired *interceptor's* Close)

```sh
cd ~/wt-73f && go test -v -run '^Test$/^Eval_ServerSideXDS_ReplacementRPCWhileRetiredCloseBlocked$' ./test/xds -race -count=1
```

```console
--- PASS: Test (0.04s)
    --- PASS: Test/Eval_ServerSideXDS_ReplacementRPCWhileRetiredCloseBlocked (0.03s)
PASS
ok  	google.golang.org/grpc/test/xds	1.065s
```

The fixture passes: it measures interceptor cleanup, which is post-publication on this branch, and does not exercise the server-filter path.

### Run 4 — same pointer test at the base commit `4ee6ac46`

```sh
cd ~/wt-base   # same two cp commands as Run 2
go test -v -race -count=1 -run '^Test$/^Verify_C1C2_RoutePointer_ServerFilterCloseHeld' ./internal/xds/server
```

```console
    verify_c1c2_pointer_test.go:153: route pointer 500ms into held Close(): 0xc00078aa00 (routes in vh[0]: 1); published replacement = false
    verify_c1c2_pointer_test.go:163: route pointer after Close() released: 0xc0006c6200 (routes in vh[0]: 0); published replacement = true
    verify_c1c2_pointer_test.go:165: STALL: replacement route pointer was not published while Close() was pending
    --- FAIL: Test/Verify_C1C2_RoutePointer_ServerFilterCloseHeld (0.52s)
```

The server-filter-before-publication order is inherited from the base commit; the branch did not introduce it and did not change it.

### Impact reasoning

- The claim's scenario ("retired server filter's Close() pending during one RDS replacement") is only reachable when the replacement stops referencing the filter. In exactly that scenario both observations the claim asks for hold: pointer unpublished, and RPCs on the existing connection keep using the superseded configuration until release.
- The ordinary replacement (routes keep using the same filters) is not affected: no server filter is retired, and retired interceptors are closed after publication.
- A stall needs a `ServerFilter.Close()` that takes time; the duration of the stall equals the duration of that call. `handleRDSUpdate` holds `l.mu` throughout, and `Accept()` takes `l.mu`, so new connections on the listener wait as well (not measured here).
- Side observation from Run 1: in the retiring replacement the server filter is closed before, and while RPCs are still authorized by, the interceptors it built.

## C2

Target: `evalon/grpc-go-xd-73f6da56` (HEAD `ed3cfa07`), worktree `~/wt-73f`. C2 states the same dependency as C1 (publication waits for retired server-filter cleanup); the same runs adjudicate it. Naming drift: there is no `applyConfiguration` on this branch; the equivalent sequence is `handleRDSUpdate` -> `constructUsableRouteConfiguration` -> `updateUsableRouteConfiguration` (`Swap(urc).closeInterceptors()`).

### Publication sequence, observed on the route pointer

```sh
cd ~/wt-73f
cp ~/eval_tests/tests/eval_rds_error_after_success_test.go internal/xds/server/
cp ~/repos/grpc-go/verify/repro/c1c2_route_pointer_test.go.txt internal/xds/server/verify_c1c2_pointer_test.go
go test -v -race -count=1 -run '^Test$/^Verify_C1C2_RoutePointer' ./internal/xds/server 2>&1 | grep -E "verify_c1c2|^(---|===|ok|FAIL|PASS)|^\s+---|panic"
```

```console
=== RUN   Test/Verify_C1C2_RoutePointer_Control_InterceptorCloseHeld
    verify_c1c2_pointer_test.go:138: active route pointer before replacement: 0xc00052cc80 (routes in vh[0]: 1)
    verify_c1c2_pointer_test.go:153: route pointer 500ms into held Close(): 0xc000128080 (routes in vh[0]: 1); published replacement = true
    verify_c1c2_pointer_test.go:158: handleRDSUpdate still blocked while Close() held
    verify_c1c2_pointer_test.go:163: route pointer after Close() released: 0xc000128080 (routes in vh[0]: 1); published replacement = true
=== RUN   Test/Verify_C1C2_RoutePointer_ServerFilterCloseHeld
    verify_c1c2_pointer_test.go:138: active route pointer before replacement: 0xc0002951c0 (routes in vh[0]: 1)
    verify_c1c2_pointer_test.go:153: route pointer 500ms into held Close(): 0xc0002951c0 (routes in vh[0]: 1); published replacement = false
    verify_c1c2_pointer_test.go:158: handleRDSUpdate still blocked while Close() held
    verify_c1c2_pointer_test.go:163: route pointer after Close() released: 0xc0003ffb00 (routes in vh[0]: 0); published replacement = true
    verify_c1c2_pointer_test.go:165: STALL: replacement route pointer was not published while Close() was pending
    --- PASS: Test/Verify_C1C2_RoutePointer_Control_InterceptorCloseHeld (0.57s)
    --- FAIL: Test/Verify_C1C2_RoutePointer_ServerFilterCloseHeld (0.51s)
```

### Routing through an existing connection

```sh
cd ~/wt-73f
cp ~/eval_tests/tests/eval_xds_server_interceptor_leak_test.go test/xds/
cp ~/repos/grpc-go/verify/repro/c1c2_server_filter_close_blocked_test.go.txt test/xds/verify_c1c2_test.go
go test -v -race -count=1 -run '^Test$/^Verify_C1C2_' ./test/xds 2>&1 | grep -E "verify_c1c2|^(---|===|ok|FAIL|PASS)|^\s+---|panic"
```

Key lines (default configuration, replacement without routes, `ServerFilter.Close()` held):

```console
=== RUN   Test/Verify_C1C2_ServerFilterCloseHeld_ReplacementWithoutRoutes
    verify_c1c2_test.go:90: [13ms] BEFORE replacement: served by "path1", status msg ""
    verify_c1c2_test.go:93: [13ms] replacement RouteConfiguration pushed to management server
    verify_c1c2_test.go:60: [14ms] ServerFilter.Close() entered
    verify_c1c2_test.go:103: [15ms] WHILE Close() HELD probe #0: served by "path1", status msg ""
    verify_c1c2_test.go:103: [316ms] WHILE Close() HELD probe #1: served by "path1", status msg ""
    verify_c1c2_test.go:103: [617ms] WHILE Close() HELD probe #2: served by "path1", status msg ""
    verify_c1c2_test.go:103: [918ms] WHILE Close() HELD probe #3: served by "path1", status msg ""
    verify_c1c2_test.go:103: [1.22s] WHILE Close() HELD probe #4: served by "path1", status msg ""
    verify_c1c2_test.go:111: [1.521s] Close() released
    verify_c1c2_test.go:63: [1.521s] ServerFilter.Close() returning
    verify_c1c2_test.go:50: [1.521s] interceptor(path1).Close() entered
    verify_c1c2_test.go:116: [1.522s] AFTER release: served by "<no interceptor>", status msg "[xDS node id: 845163e8-71ce-4d46-a980-da14e4d65861]: the incoming RPC did not match a configured Route"
    verify_c1c2_test.go:122: STALL: RPCs on the existing connection were still routed by the superseded route configuration (interceptor path1) while Close() was pending
```

Key lines (filter disabled per route, `ServerFilter.Close()` held):

```console
=== RUN   Test/Verify_C1C2_ServerFilterCloseHeld_ReplacementDisablesFilter
    verify_c1c2_test.go:60: [15ms] ServerFilter.Close() entered
    verify_c1c2_test.go:103: [16ms] WHILE Close() HELD probe #0: served by "path1", status msg ""
    verify_c1c2_test.go:103: [1.224s] WHILE Close() HELD probe #4: served by "path1", status msg ""
    verify_c1c2_test.go:111: [1.524s] Close() released
    verify_c1c2_test.go:116: [1.524s] AFTER release: served by "<no interceptor>", status msg ""
    verify_c1c2_test.go:122: STALL: RPCs on the existing connection were still routed by the superseded route configuration (interceptor path1) while Close() was pending
```

Controls (ordinary replacement that still uses the filter):

```console
=== RUN   Test/Verify_C1C2_Control_InterceptorCloseHeld_OrdinaryReplacement
    verify_c1c2_test.go:50: [24ms] interceptor(path1).Close() entered
    verify_c1c2_test.go:103: [24ms] WHILE Close() HELD probe #0: served by "path2", status msg ""
    verify_c1c2_test.go:103: [1.232s] WHILE Close() HELD probe #4: served by "path2", status msg ""
    verify_c1c2_test.go:124: NO STALL: replacement was routing RPCs while Close() was pending
=== RUN   Test/Verify_C1C2_Control_ServerFilterCloseNotInvoked_OrdinaryReplacement
    verify_c1c2_test.go:97: [10.016s] the held Close() was never invoked for this replacement
--- FAIL: Test (14.67s)
    --- PASS: Test/Verify_C1C2_Control_InterceptorCloseHeld_OrdinaryReplacement (1.59s)
    --- FAIL: Test/Verify_C1C2_Control_ServerFilterCloseNotInvoked_OrdinaryReplacement (10.02s)
    --- FAIL: Test/Verify_C1C2_ServerFilterCloseHeld_ReplacementDisablesFilter (1.53s)
    --- FAIL: Test/Verify_C1C2_ServerFilterCloseHeld_ReplacementWithoutRoutes (1.53s)
```

### The fixture named by the claim

```sh
cd ~/wt-73f && go test -v -run '^Test$/^Eval_ServerSideXDS_ReplacementRPCWhileRetiredCloseBlocked$' ./test/xds -race -count=1
```

```console
    --- PASS: Test/Eval_ServerSideXDS_ReplacementRPCWhileRetiredCloseBlocked (0.03s)
ok  	google.golang.org/grpc/test/xds	1.065s
```

It holds the retired interceptor's `Close()` (`onClose` for `path1`), not the server filter's (`onFilterClose` is unset), so it stays green while the server-filter dependency exists.

### Base commit

The pointer test `Verify_C1C2_RoutePointer_ServerFilterCloseHeld` fails identically at `4ee6ac46` (`published replacement = false` during the hold), i.e. the dependency predates the branch.

### Impact reasoning

Publication depends on retired server-filter cleanup, but not on retired interceptor cleanup. The dependency is exercised only by replacements that drop the last reference to a filter (replacement without routes; filter disabled on every route), and is visible only for as long as that filter's `Close()` runs. The branch's own change (Swap, then close interceptors) is ordered correctly; the remaining pre-publication cleanup is the loop at the end of `constructUsableRouteConfiguration`.

## C3

Target: `evalon/grpc-go-xd-8410daeb` (HEAD `358ddbea`), worktree `~/wt-841`.

Lifecycle tests added by the branch:

- `internal/xds/server/listener_wrapper_test.go`: `TestListenerWrapper_RDSUpdatesReleaseSupersededInterceptors`, `TestListenerWrapper_RDSErrorReleasesSupersededResources`. Both call `newListenerWrapperForTesting`, which opens a real TCP listener with `testutils.LocalTCPListener()`. `grep -n "t.Cleanup\|defer " internal/xds/server/listener_wrapper_test.go` prints only `163:	defer cancel()`; the only release of the listener is the normal-path `l.Close()` near the end of each test, after many `t.Fatalf` assertions.
- `test/xds/xds_server_filter_state_retention_test.go`: `TestServerSideXDS_RDSUpdates_ReleaseSupersededInterceptors` (the claim calls it `TestServerSideXDS_InterceptorLeak_RDSUpdate`; no test of that name exists on the branch). It registers `defer httpfilter.UnregisterForTesting`, `defer stopServer()`, `defer cancel()`, `defer cc.Close()`.

### Run — forced intermediate `t.Fatalf` + census of the process's listening sockets after each test ends

`verify/repro/c3_run.sh` inserts `if verifyC3ForceFail { t.Fatalf("verify: forced intermediate terminating assertion") }` right after resource acquisition in each of the three tests (11 inserted lines, test files only), then runs each test as a subtest with and without the flag. After the subtest has ended (deferred functions and cleanups done) it lists every socket of the test process in LISTEN state (`SO_ACCEPTCONN` over `/proc/self/fd`) and diffs against the list taken before the subtest.

```sh
~/repos/grpc-go/verify/repro/c3_run.sh ~/wt-841
```

```console
 internal/xds/server/listener_wrapper_test.go       | 8 ++++++++
 test/xds/xds_server_filter_state_retention_test.go | 3 +++
 2 files changed, 11 insertions(+)
=== RUN   TestVerifyC3_Unit_Control_NoForcedFailure
    verify_c3_test.go:27: C3 RDSUpdatesReleaseSupersededInterceptors force=false: subtest failed=false; listening sockets still open after the test ended: 0 []
    verify_c3_test.go:27: C3 RDSErrorReleasesSupersededResources force=false: subtest failed=false; listening sockets still open after the test ended: 0 []
--- PASS: TestVerifyC3_Unit_Control_NoForcedFailure (0.40s)
=== RUN   TestVerifyC3_Unit_ForcedIntermediateFailure
=== RUN   TestVerifyC3_Unit_ForcedIntermediateFailure/RDSUpdatesReleaseSupersededInterceptors
    listener_wrapper_test.go:163: verify: forced intermediate terminating assertion
    verify_c3_test.go:27: C3 RDSUpdatesReleaseSupersededInterceptors force=true: subtest failed=true; listening sockets still open after the test ended: 1 [127.0.0.1:38393]
    verify_c3_test.go:30: C3 RDSUpdatesReleaseSupersededInterceptors: dial 127.0.0.1:38393 after test end -> err=<nil> (nil err == listener still accepting in kernel)
=== RUN   TestVerifyC3_Unit_ForcedIntermediateFailure/RDSErrorReleasesSupersededResources
    listener_wrapper_test.go:222: verify: forced intermediate terminating assertion
    verify_c3_test.go:27: C3 RDSErrorReleasesSupersededResources force=true: subtest failed=true; listening sockets still open after the test ended: 1 [127.0.0.1:44369]
    verify_c3_test.go:30: C3 RDSErrorReleasesSupersededResources: dial 127.0.0.1:44369 after test end -> err=<nil> (nil err == listener still accepting in kernel)
--- FAIL: TestVerifyC3_Unit_ForcedIntermediateFailure (0.40s)
FAIL	google.golang.org/grpc/internal/xds/server	0.830s
=== RUN   TestVerifyC3_E2E_Control_NoForcedFailure
    verify_c3_test.go:21: C3 e2e force=false: subtest failed=false; listening sockets still open after the test ended: 0 []
--- PASS: TestVerifyC3_E2E_Control_NoForcedFailure (0.74s)
=== RUN   TestVerifyC3_E2E_ForcedIntermediateFailure
    xds_server_filter_state_retention_test.go:773: verify: forced intermediate terminating assertion
    verify_c3_test.go:21: C3 e2e force=true: subtest failed=true; listening sockets still open after the test ended: 0 []
--- FAIL: TestVerifyC3_E2E_ForcedIntermediateFailure (0.52s)
FAIL	google.golang.org/grpc/test/xds	1.284s
```

(The `FAIL` statuses are the forced failures propagating to the parent test; the evidence is the census lines.)

Per part:

- Unit test `TestListenerWrapper_RDSUpdatesReleaseSupersededInterceptors`: a TCP listener (127.0.0.1:38393) is still open and accepting connections after the failed test ended. Leak.
- Unit test `TestListenerWrapper_RDSErrorReleasesSupersededResources`: same (127.0.0.1:44369). Leak.
- e2e test `TestServerSideXDS_RDSUpdates_ReleaseSupersededInterceptors`: 0 listening sockets left after the forced failure; its deferred `stopServer()` / `cc.Close()` and the helpers' cleanups release everything. No leak.
- Controls without the forced failure: 0 sockets left in all three tests.

### Impact reasoning

Only test hygiene, and only on a failing run: each of the two unit tests leaves one listening TCP socket (plus the `listenerWrapper`, its filter and interceptors, which `l.Close()` would have released) open for the remainder of the `go test` process. No goroutine is attached to the socket, so the package's leak checker does not flag it. Production code is unaffected. Fix is a `t.Cleanup(func() { l.Close() })` (or closing `lis`) inside `newListenerWrapperForTesting`; `listenerWrapper.Close` would then be called twice on the normal path, so either drop the normal-path call's exclusivity or make the cleanup close only the raw listener.
