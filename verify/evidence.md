## C1

Claim: processing an RDS error update with no route data, after a usable route configuration is installed, releases an old filter before all old interceptors that depend on it have finished closing.

Target branch: `evalon/grpc-go-xd-42ea6cb2` in `kaitranntt-evals/grpc-go-xds-rds-interceptor-lifecycle-leak` (commit `aced3eed`). Control: audited branch `grpc-go-xds-rds-interceptor-lifecycle-leak-perfect` (commit `614cb739`).

### Setup

```sh
cd ~/repos/grpc-go
git remote add claimrepo https://github.com/kaitranntt-evals/grpc-go-xds-rds-interceptor-lifecycle-leak
git fetch claimrepo evalon/grpc-go-xd-42ea6cb2
git worktree add ~/work/claim FETCH_HEAD        # HEAD is now at aced3eed
mkdir -p ~/work && cd ~/work && unzip -o eval_tests.zip   # byte-exact fixtures in ~/work/tests
```

### Code path on the claim branch (context only, not the evidence)

`internal/xds/server/listener_wrapper.go`, `updateRouteConfigurationLocked`:

```go
	if rcu.data == nil {
		fc.stop()                                   // releases the chain's server-filter references first
		urc = &usableRouteConfiguration{err: rcu.err}
	} else {
		urc = fc.constructUsableRouteConfiguration(*rcu.data, l.getOrCreateServerFilterLocked)
	}
	urc.nodeID = l.xdsNodeID
	fc.swapUsableRouteConfiguration(urc)            // only now closes the old interceptors
```

### Observation 1 — direct trace, single filter chain (verify/repro/c1_rds_error_close_order_test.go)

One listener, one filter chain using RDS route `route1`, one custom HTTP filter, two routes (so two interceptors built by that filter). After the listener is SERVING, `handleRDSUpdate(route1, rdsWatcherUpdate{err: ...})` (no data) is delivered. The filter and interceptors append to a shared event log on build/close; each interceptor also records whether its parent filter's `Close()` had already run.

```sh
cp ~/repos/grpc-go/verify/repro/c1_rds_error_close_order_test.go ~/work/claim/internal/xds/server/
cd ~/work/claim
go test -race -v -count=1 -run '^Test$/^VerifyC1_' ./internal/xds/server 2>&1 | grep -E 'C1-TRACE|^(---|FAIL|ok|PASS)|    ---'
```

```console
c1_rds_error_close_order_test.go:200: C1-TRACE after usable config installed: [filter-build icpt-build#1 icpt-build#2]
c1_rds_error_close_order_test.go:212: C1-TRACE cleanup events during handleRDSUpdate(err, no data): [filter-close icpt-close#1(parent-filter-ALREADY-CLOSED) icpt-close#2(parent-filter-ALREADY-CLOSED)]
c1_rds_error_close_order_test.go:224: C1-TRACE filter-close index=0, interceptor closes=2, last icpt-close index=2
c1_rds_error_close_order_test.go:226: C1-TRACE ORDER VIOLATION: old filter released (event #0) before old interceptors finished closing (last at event #2)
--- FAIL: Test (0.03s)
--- FAIL: Test/VerifyC1_RDSErrorAfterUsableConfig_CloseOrder (0.02s)
FAIL
FAIL	google.golang.org/grpc/internal/xds/server	0.053s
```

The server filter's `Close()` ran first; both interceptors it built were still open and were closed afterwards.

### Observation 2 — eval fixture, byte-exact, on the claim branch

```sh
cp ~/work/tests/eval_rds_error_after_success_test.go ~/work/claim/internal/xds/server/
cd ~/work/claim
go test -race -v -count=1 -run '^Test$/^Eval_ServerSideXDS_ErrorUpdateReleasesServerFilterReferences$' ./internal/xds/server
```

```console
    eval_rds_error_after_success_test.go:292: dependency order violation upon error update: parent filter closed at event #1 before interceptor closed at event #2 (events: [icpt-close filter-close icpt-close])
--- FAIL: Test (0.02s)
    --- FAIL: Test/Eval_ServerSideXDS_ErrorUpdateReleasesServerFilterReferences (0.02s)
FAIL
FAIL	google.golang.org/grpc/internal/xds/server	0.042s
```

The fixture has two filter chains sharing one ref-counted filter. The first error update only decrements the count (2→1), so the first `icpt-close` precedes any real close; the second error update drops the count to 0, so the filter is destroyed before the second chain's interceptor closes. No ref-count or leak assertion of the fixture fired — only the ordering assertion.

### Observation 3 — control on the audited branch (same two tests, commit 614cb739)

```sh
cd ~/repos/grpc-go
cp verify/repro/c1_rds_error_close_order_test.go ~/work/tests/eval_rds_error_after_success_test.go internal/xds/server/
go test -race -v -count=1 -run '^Test$/^VerifyC1_' ./internal/xds/server 2>&1 | grep -E 'C1-TRACE|^(---|FAIL|ok|PASS)|    ---'
go test -race -v -count=1 -run '^Test$/^Eval_ServerSideXDS_ErrorUpdateReleasesServerFilterReferences$' ./internal/xds/server 2>&1 | grep -E '^(---|FAIL|ok|PASS)|    ---|violation'
rm internal/xds/server/c1_rds_error_close_order_test.go internal/xds/server/eval_rds_error_after_success_test.go
```

```console
c1_rds_error_close_order_test.go:200: C1-TRACE after usable config installed: [filter-build icpt-build#1 icpt-build#2]
c1_rds_error_close_order_test.go:212: C1-TRACE cleanup events during handleRDSUpdate(err, no data): [icpt-close#1 icpt-close#2 filter-close]
c1_rds_error_close_order_test.go:224: C1-TRACE filter-close index=2, interceptor closes=2, last icpt-close index=1
--- PASS: Test (0.01s)
--- PASS: Test/VerifyC1_RDSErrorAfterUsableConfig_CloseOrder (0.01s)
PASS
ok  	google.golang.org/grpc/internal/xds/server	1.041s
--- PASS: Test (0.02s)
    --- PASS: Test/Eval_ServerSideXDS_ErrorUpdateReleasesServerFilterReferences (0.01s)
PASS
ok  	google.golang.org/grpc/internal/xds/server	1.045s
```

So the repro distinguishes the two orderings and is not failing for an unrelated reason.

### Observation 4 — reorder experiment on the claim branch (not committed to production code)

Applying `verify/repro/c1_reorder_experiment.diff` in the claim worktree (swap in the error configuration, which closes the old interceptors, and only then call `fc.stop()`), then rerunning both tests:

```sh
cd ~/work/claim && git apply ~/repos/grpc-go/verify/repro/c1_reorder_experiment.diff
go test -race -v -count=1 -run '^Test$/^(VerifyC1_|Eval_ServerSideXDS_ErrorUpdateReleasesServerFilterReferences$)' ./internal/xds/server 2>&1 | grep -E 'C1-TRACE|^(---|FAIL|ok|PASS)|    ---|violation'
git checkout internal/xds/server/listener_wrapper.go
```

```console
c1_rds_error_close_order_test.go:211: C1-TRACE cleanup events during handleRDSUpdate(err, no data): [icpt-close#1 icpt-close#2 filter-close]
c1_rds_error_close_order_test.go:223: C1-TRACE filter-close index=2, interceptor closes=2, last icpt-close index=1
--- PASS: Test (0.03s)
--- PASS: Test/Eval_ServerSideXDS_ErrorUpdateReleasesServerFilterReferences (0.03s)
--- PASS: Test/VerifyC1_RDSErrorAfterUsableConfig_CloseOrder (0.01s)
PASS
ok  	google.golang.org/grpc/internal/xds/server	1.062s
```

This pins the cause to the `fc.stop()`-before-swap ordering in the `rcu.data == nil` branch. (Line numbers differ by one from Observation 1 because that run preceded `gofmt` of the repro file.)

### Verdict and impact reasoning

CONFIRMED. On the claim branch, an RDS error update with no data after a usable configuration drops the filter chain's server-filter references before closing the interceptors of the replaced configuration. When that reference is the last one (a single chain using the filter, or the last of several chains to receive the error), the filter's `Close()` runs while every interceptor it built for that chain is still open.

What was and was not observed: the ordering inversion itself, deterministically, in both the single-chain trace and the eval fixture. No crash, race report or leak was observed with the test filter, whose interceptors do not touch filter state in `Close()`; counts were correct (all interceptors closed, filter destroyed exactly once). The consequence for a real filter depends on whether its interceptors use filter-owned resources during `Close()`; that was not exercised with any in-tree filter. The trigger is an RDS resource being removed/not found (or an error with no cached data) after it had been served — reachable only through `handleRDSUpdate`, since the same branch is also used at initial instantiation where there is no previous configuration.
