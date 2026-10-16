## C1

Verdict: CONFIRMED (holds for the `internal/xds/server` tracker `countingInterceptor`; does not hold for the `test/xds` tracker `trackingInterceptor`).

Target: [evalon/grpc-go-xd-8e055bd7](https://github.com/kaitranntt-evals/grpc-go-xds-rds-interceptor-lifecycle-leak/tree/evalon/grpc-go-xd-8e055bd7) at commit `8047fc9f802560e3398c122af25e1c68a7126006`. The branch is not on `origin`; it was fetched from the repository named in the claim URL and checked out in a separate worktree. All commands ran with go1.26.3 linux/amd64.

Tracker source on that branch (`internal/xds/server/listener_wrapper_test.go`):

```go
func (i *countingInterceptor) Close() {
	if i.closed.Swap(true) {
		return
	}
	i.parent.interceptorsClosed.Add(1)
}
```

and `test/xds/xds_server_filter_state_retention_test.go`:

```go
func (i *trackingInterceptor) Close() {
	i.parent.interceptorsDestroyed.Add(1)
}
```

The assertions built on the first counter are `fb.liveInterceptors()` (`interceptorsCreated - interceptorsClosed`) comparisons in `TestListenerWrapper_RepeatedRDSUpdates_ReleaseSupersededResources` and `TestListenerWrapper_RDSErrorAfterUpdate_ReleasesSupersededResources`. The assertions built on the second are `interceptorsDestroyed` / `interceptorsCreated-interceptorsDestroyed` comparisons in the three `TestServerSideXDS_FilterStateRetention_*` tests.

Everything below is the verbatim output of one command, run from the root of this branch:

```sh
bash verify/repro/c1_repro.sh
```

The script (1) copies two probe tests into the claim worktree that call each tracker's `Close` twice and print the counters, (2) applies `verify/repro/c1_mutation_double_close.patch` (every `r.interceptor.Close()` in `virtualHostWithInterceptors.closeInterceptors` is issued twice) and runs the branch's lifecycle tests, (3) applies `verify/repro/c1_mutation_error_path_double_close.patch` (the superseded configuration is closed twice only when an RDS error replaces it) and runs the branch's complete `./internal/xds/server/...` and `./test/xds` suites with `-race`.

```console
### 1. Close() twice on each tracker
=== RUN   TestVerifyC1_CountingInterceptorDoubleClose
    verify_c1_double_close_test.go:15: after build:        interceptorsCreated=1 interceptorsClosed=0 liveInterceptors()=1
    verify_c1_double_close_test.go:17: after 1st Close():  interceptorsCreated=1 interceptorsClosed=1 liveInterceptors()=0
    verify_c1_double_close_test.go:19: after 2nd Close():  interceptorsCreated=1 interceptorsClosed=1 liveInterceptors()=0
    verify_c1_double_close_test.go:21: SUPPRESSED: interceptorsClosed=1 after two Close() calls; duplicate cleanup is invisible to the count assertions
--- FAIL: TestVerifyC1_CountingInterceptorDoubleClose (0.00s)
FAIL
FAIL	google.golang.org/grpc/internal/xds/server	0.024s
=== RUN   TestVerifyC1_TrackingInterceptorDoubleClose
    verify_c1_double_close_test.go:18: after build:        interceptorsCreated=1 interceptorsDestroyed=0
    verify_c1_double_close_test.go:20: after 1st Close():  interceptorsCreated=1 interceptorsDestroyed=1
    verify_c1_double_close_test.go:22: after 2nd Close():  interceptorsCreated=1 interceptorsDestroyed=2
--- PASS: TestVerifyC1_TrackingInterceptorDoubleClose (0.00s)
PASS
ok  	google.golang.org/grpc/test/xds	1.025s
FAIL
### 2. Production mutated to close every interceptor twice; branch lifecycle tests
--- PASS: Test (0.02s)
    --- PASS: Test/ListenerWrapper_RDSErrorAfterUpdate_ReleasesSupersededResources (0.01s)
    --- PASS: Test/ListenerWrapper_RepeatedRDSUpdates_ReleaseSupersededResources (0.00s)
PASS
ok  	google.golang.org/grpc/internal/xds/server	1.044s
    xds_server_filter_state_retention_test.go:610: Destroyed 2 interceptor instances, want: 1
    xds_server_filter_state_retention_test.go:380: Destroyed 4 interceptor instances, want: 2
    xds_server_filter_state_retention_test.go:824: After 1 RDS updates, 0 interceptor instances alive, want: 2
--- FAIL: Test (0.15s)
    --- FAIL: Test/ServerSideXDS_FilterStateRetention_AcrossUpdates_FilterChainsChange (0.04s)
    --- FAIL: Test/ServerSideXDS_FilterStateRetention_AcrossUpdates_FilterConfigChange (0.03s)
    --- FAIL: Test/ServerSideXDS_FilterStateRetention_RepeatedRDSUpdates (0.08s)
FAIL
FAIL	google.golang.org/grpc/test/xds	0.178s
FAIL
### 3. Production mutated to double-close only on the RDS-error path; full branch suites
ok  	google.golang.org/grpc/internal/xds/server	1.142s
ok  	google.golang.org/grpc/test/xds	13.320s
```

What this shows:

- Step 1: `countingInterceptor.Close()` twice leaves `interceptorsClosed=1` and `liveInterceptors()=0`, identical to a single close. The probe's own `SUPPRESSED` failure is the expected signal. `trackingInterceptor.Close()` twice yields `interceptorsDestroyed=2`, so that tracker does count each call.
- Step 2: with production closing every interceptor twice, both `ListenerWrapper_*` tests still pass, while all three `ServerSideXDS_FilterStateRetention_*` tests fail on their count assertions (`Destroyed 4 interceptor instances, want: 2`, etc.). The suppression is specific to the `internal/xds/server` tracker.
- Step 3: with the double close limited to the RDS-error path, which only `TestListenerWrapper_RDSErrorAfterUpdate_ReleasesSupersededResources` exercises with a counting tracker, both complete packages are green. A duplicate-cleanup regression on that path is not detected by any test on the branch.

Baseline (unmutated branch, same `-run` filter as step 2), run separately:

```sh
go test -race -count=1 -v -run '^Test$/^(ListenerWrapper_R|ServerSideXDS_FilterStateRetention)' ./internal/xds/server ./test/xds 2>&1 | grep -E "^\s*(--- |ok|FAIL|PASS|\S+_test.go)"
```

```console
--- PASS: Test (0.01s)
    --- PASS: Test/ListenerWrapper_RDSErrorAfterUpdate_ReleasesSupersededResources (0.01s)
    --- PASS: Test/ListenerWrapper_RepeatedRDSUpdates_ReleaseSupersededResources (0.00s)
PASS
ok  	google.golang.org/grpc/internal/xds/server	1.037s
--- PASS: Test (0.31s)
    --- PASS: Test/ServerSideXDS_FilterStateRetention_AcrossUpdates_FilterChainsChange (0.04s)
    --- PASS: Test/ServerSideXDS_FilterStateRetention_AcrossUpdates_FilterConfigChange (0.03s)
    --- PASS: Test/ServerSideXDS_FilterStateRetention_RepeatedRDSUpdates (0.23s)
PASS
ok  	google.golang.org/grpc/test/xds	1.336s
```

Impact reasoning: the affected code is test-only, so nothing a server operator runs is changed by it. The cost is lost coverage. The unit tests in `internal/xds/server` are the only ones on the branch that drive the RDS-error replacement path and stop-after-error with a counting tracker, and their tracker deliberately de-duplicates `Close`, so "closed twice" and "closed once" produce the same asserted numbers. Real `ServerInterceptor` implementations are not required to tolerate a second `Close`, so a double-close regression is a plausible bug class for exactly this change (ownership of interceptors moves between the usable route configuration, `filterChain.stop` and the construction error paths). The end-to-end tests in `test/xds` do catch a double close on the replacement and server-stop paths, which limits the gap to paths only the unit tests reach. The unmutated branch itself was not observed to double-close anything.

## C2

Verdict: CONFIRMED for both parts, limited to the unit tests in `internal/xds/server`; the end-to-end test in `test/xds` releases everything on both exit paths.

- normal_exit_cleanup: CONFIRMED (2 of the 3 unit tests return with 1 interceptor and 1 filter still live).
- fatal_assertion_cleanup: CONFIRMED (all 3 unit tests leave filters and interceptors live when one of their own `t.Fatalf` assertions fires).

Target: [evalon/grpc-go-xd-bc3c04de](https://github.com/kaitranntt-evals/grpc-go-xds-rds-interceptor-lifecycle-leak/tree/evalon/grpc-go-xd-bc3c04de) at commit `c3d10291b87704a18bcc693c30c32bd7f5bf17a4`, fetched from the repository named in the claim URL into a separate worktree. Naming drift: the claim's `internal/xds/server/listener_wrapper_test.go` is `internal/xds/server/usable_route_configuration_test.go` on this branch, the "replacement-and-stop test" is `TestUsableRouteConfiguration_ReleasedOnReplacementAndStop`, and `test/xds/xds_server_interceptor_leak_test.go` is the new test `TestServerSideXDS_FilterStateRetention_RepeatedRDSUpdatesReleaseInterceptors` in `test/xds/xds_server_filter_state_retention_test.go`.

Ownership and cleanup registration, from the branch source:

- Each unit test creates `fb := &countingFilterBuilder{}` and a `filterChain` through `newFilterChainForTesting`, then builds route configurations that take a reference on a server filter and create interceptors. None of the three registers `t.Cleanup` or a deferred `fc.stop()`. The only deferred cleanup is `defer release()` on a route-configuration reference, at the very end of two of the tests. `TestUsableRouteConfiguration_InFlightRPCDelaysRelease` and `TestUsableRouteConfiguration_ConcurrentUpdatesAndRPCs` never stop the filter chain at all.
- The end-to-end test uses `defer httpfilter.UnregisterForTesting(fb.typeURL)`, `defer stopServer()`, `defer cancel()` and `defer cc.Close()`, which run on `t.Fatalf` as well as on return.

Instrumentation (`verify/repro/c2_exit_state_instrumentation.patch`, test files only): a `t.Cleanup` registered right after the tracker is created logs a `VERIFY-EXIT` line after the test body and its defers have finished. For the unit tests it prints `liveInterceptors` and `liveFilters`. For the end-to-end test it prints the created/destroyed counters, whether the filter type URL is still registered, the client connection state, and the result of dialing the server's listener address; it also adds an env-gated `t.Fatalf` after the update loop (`VERIFY_FORCE_FATAL=1`).

Fatal exits in the unit tests were triggered by making the tests' own assertions fire through production mutations, applied one at a time:

- M1 (`verify/repro/c2_mutation_m1_no_retire.patch`): `setUsableRouteConfiguration` no longer retires the superseded configuration.
- M2 (`verify/repro/c2_mutation_m2_early_close.patch`): `retire` closes interceptors immediately instead of dropping a reference.

Everything below is the verbatim output of one command, run from the root of this branch:

```sh
bash verify/repro/c2_repro.sh
```

```console
### 1. Normal exit, production untouched
    usable_route_configuration_test.go:218: VERIFY-EXIT Test/UsableRouteConfiguration_ConcurrentUpdatesAndRPCs failed=false liveInterceptors=1 liveFilters=1
    usable_route_configuration_test.go:178: VERIFY-EXIT Test/UsableRouteConfiguration_InFlightRPCDelaysRelease failed=false liveInterceptors=1 liveFilters=1
    usable_route_configuration_test.go:116: VERIFY-EXIT Test/UsableRouteConfiguration_ReleasedOnReplacementAndStop failed=false liveInterceptors=0 liveFilters=0
--- PASS: Test (0.02s)
    --- PASS: Test/UsableRouteConfiguration_ConcurrentUpdatesAndRPCs (0.02s)
    --- PASS: Test/UsableRouteConfiguration_InFlightRPCDelaysRelease (0.00s)
    --- PASS: Test/UsableRouteConfiguration_ReleasedOnReplacementAndStop (0.00s)
PASS
ok  	google.golang.org/grpc/internal/xds/server	1.045s
    xds_server_filter_state_retention_test.go:645: VERIFY-EXIT Test/ServerSideXDS_FilterStateRetention_RepeatedRDSUpdatesReleaseInterceptors failed=false filtersCreated=1 filtersDestroyed=1 interceptorsCreated=42 interceptorsDestroyed=42
    xds_server_filter_state_retention_test.go:646: VERIFY-EXIT filter still registered=false
    xds_server_filter_state_retention_test.go:648: VERIFY-EXIT client conn state=SHUTDOWN
    xds_server_filter_state_retention_test.go:655: VERIFY-EXIT dial server listener 127.0.0.1:33389: err=dial tcp 127.0.0.1:33389: connect: connection refused
--- PASS: Test (0.28s)
    --- PASS: Test/ServerSideXDS_FilterStateRetention_RepeatedRDSUpdatesReleaseInterceptors (0.27s)
PASS
ok  	google.golang.org/grpc/test/xds	1.312s
### 2. Fatal exit, e2e test (injected t.Fatalf after the update loop)
    xds_server_filter_state_retention_test.go:838: VERIFY forced fatal assertion after server, client and filter were acquired and 20 updates served
    xds_server_filter_state_retention_test.go:645: VERIFY-EXIT Test/ServerSideXDS_FilterStateRetention_RepeatedRDSUpdatesReleaseInterceptors failed=true filtersCreated=1 filtersDestroyed=1 interceptorsCreated=42 interceptorsDestroyed=42
    xds_server_filter_state_retention_test.go:646: VERIFY-EXIT filter still registered=false
    xds_server_filter_state_retention_test.go:648: VERIFY-EXIT client conn state=SHUTDOWN
    xds_server_filter_state_retention_test.go:655: VERIFY-EXIT dial server listener 127.0.0.1:44681: err=dial tcp 127.0.0.1:44681: connect: connection refused
--- FAIL: Test (0.28s)
    --- FAIL: Test/ServerSideXDS_FilterStateRetention_RepeatedRDSUpdatesReleaseInterceptors (0.27s)
FAIL
FAIL	google.golang.org/grpc/test/xds	0.309s
FAIL
### 3. Fatal exit, unit tests, mutation M1 (superseded configuration never retired)
    usable_route_configuration_test.go:260: After updates, 1001 interceptors are live, want 1
    usable_route_configuration_test.go:218: VERIFY-EXIT Test/UsableRouteConfiguration_ConcurrentUpdatesAndRPCs failed=true liveInterceptors=1001 liveFilters=1
    usable_route_configuration_test.go:199: After in-flight RPC completed, 2 interceptors are live, want 1
    usable_route_configuration_test.go:178: VERIFY-EXIT Test/UsableRouteConfiguration_InFlightRPCDelaysRelease failed=true liveInterceptors=2 liveFilters=1
    usable_route_configuration_test.go:130: After 2 updates, 6 interceptors are live, want 3
    usable_route_configuration_test.go:116: VERIFY-EXIT Test/UsableRouteConfiguration_ReleasedOnReplacementAndStop failed=true liveInterceptors=6 liveFilters=1
--- FAIL: Test (0.01s)
    --- FAIL: Test/UsableRouteConfiguration_ConcurrentUpdatesAndRPCs (0.01s)
    --- FAIL: Test/UsableRouteConfiguration_InFlightRPCDelaysRelease (0.00s)
    --- FAIL: Test/UsableRouteConfiguration_ReleasedOnReplacementAndStop (0.00s)
FAIL
FAIL	google.golang.org/grpc/internal/xds/server	0.037s
FAIL
### 4. Fatal exit, unit tests, mutation M2 (retire closes immediately, ignoring in-flight references)
    usable_route_configuration_test.go:256: RPC failed during route configuration updates: interceptor used after being closed
    usable_route_configuration_test.go:218: VERIFY-EXIT Test/UsableRouteConfiguration_ConcurrentUpdatesAndRPCs failed=true liveInterceptors=1 liveFilters=1
    usable_route_configuration_test.go:189: With an in-flight RPC on the old configuration, 1 interceptors are live, want 2
    usable_route_configuration_test.go:178: VERIFY-EXIT Test/UsableRouteConfiguration_InFlightRPCDelaysRelease failed=true liveInterceptors=1 liveFilters=1
    usable_route_configuration_test.go:116: VERIFY-EXIT Test/UsableRouteConfiguration_ReleasedOnReplacementAndStop failed=false liveInterceptors=0 liveFilters=0
--- FAIL: Test (0.01s)
    --- FAIL: Test/UsableRouteConfiguration_ConcurrentUpdatesAndRPCs (0.01s)
    --- FAIL: Test/UsableRouteConfiguration_InFlightRPCDelaysRelease (0.00s)
    --- PASS: Test/UsableRouteConfiguration_ReleasedOnReplacementAndStop (0.00s)
FAIL
FAIL	google.golang.org/grpc/internal/xds/server	0.037s
FAIL
```

What this shows:

- Step 1, normal exit, production untouched: `ConcurrentUpdatesAndRPCs` and `InFlightRPCDelaysRelease` pass and exit with `liveInterceptors=1 liveFilters=1`. `ReleasedOnReplacementAndStop` exits with `0/0`. The end-to-end test exits with `filtersCreated=1 filtersDestroyed=1 interceptorsCreated=42 interceptorsDestroyed=42`, the filter unregistered, the client connection `SHUTDOWN` and the listener refusing connections.
- Step 2, fatal exit in the end-to-end test: the same fully released state is observed after the `t.Fatalf`. This fatal is injected by the instrumentation rather than produced by one of the test's own assertions.
- Step 3, M1: the tests' own assertions fire (`usable_route_configuration_test.go:130`, `:199`, `:260` in the instrumented file) and each test exits with `liveFilters=1` and 2 to 1001 live interceptors. The interceptor numbers include the leak the mutation itself introduces; the filter reference is unaffected by M1 and is still left unreleased, because nothing stops the filter chain after the fatal.
- Step 4, M2: `InFlightRPCDelaysRelease` fails at the assertion that precedes its `release()` call (`With an in-flight RPC on the old configuration, 1 interceptors are live, want 2`), so the acquired route-configuration reference is never released, and it exits with `liveInterceptors=1 liveFilters=1`. `ConcurrentUpdatesAndRPCs` fails with `interceptor used after being closed` and exits with `liveInterceptors=1 liveFilters=1`.

Impact reasoning: the unreleased objects are in-memory test fakes (`countingFilter`, `countingInterceptor`, a `filterChain` and a `usableRouteConfiguration` with an outstanding reference). They hold no sockets, goroutines or files, each test owns a private `countingFilterBuilder`, and all three unit tests pass repeatedly under `-race`, so no cross-test interference or resource exhaustion was observed. The practical consequences are narrower: two of the three tests end without ever stopping the filter chain, so they never assert that the final configuration's interceptor and filter are released, and on a fatal assertion the reported live counts describe a half-torn-down state. The end-to-end test, which owns the real resources (xDS server, listener, client connection, global filter registration), is clean on both paths.
