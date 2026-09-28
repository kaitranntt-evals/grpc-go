## Setup

Audited branch: `grpc-go-xds-rds-interceptor-lifecycle-leak-perfect` (HEAD `614cb739`, base `4ee6ac46`), checked out as `verify/grpc-go-xds-rds-interceptor-lifecycle-leak-v-eb02180b`. Claim-target branches live in the sibling repository `kaitranntt-evals/grpc-go-xds-rds-interceptor-lifecycle-leak`; they were fetched as a second remote and each checked out in its own worktree:

```sh
cd ~/repos/grpc-go
git remote add evalrepo https://github.com/kaitranntt-evals/grpc-go-xds-rds-interceptor-lifecycle-leak
git fetch evalrepo 'refs/heads/evalon/*:refs/remotes/evalrepo/evalon/*'
for b in 8c607a23 c740ea2d bfa838ff a697ff50 be49ddea 81821b4d da10a3be 554a9276 c9e12354 a9737ee8 236e58b9 29a75b35 ff88f27f 1101a0b3 08e05fc6 29299069 611a4098 b5e97ca4 1957f499 31eb0f5f; do
  git worktree add /home/ubuntu/wt/$b evalrepo/evalon/grpc-go-xd-$b
done
```

Every target branch is exactly one commit on top of the same base `4ee6ac46` (`git merge-base 4ee6ac46 <branch>` = `4ee6ac46`, `git rev-list --count 4ee6ac46..<branch>` = 1). Toolchain: `go version go1.25.7 linux/amd64`, 8 CPUs.

Probes used (all under `verify/repro/`, copied into the package directory only for the run and removed afterwards; production code untouched):

- `verify_listener_wrapper_test.go` — package `server` unit probes (`Test/Verify_*`) that drive the real `listenerWrapper.handleRDSUpdate` / `Close` with an instrumented HTTP filter recording, per instance, filter builds/closes and interceptor builds/closes (and whether an interceptor was built on an already-closed filter).
- `c1_filter_release_order_test.go` — package `xds_test` end-to-end probe (`TestVerify_C1_FilterReleaseOrder`) using a real management server, an RDS-served listener, an in-place replacement that disables the filter, then a re-enable.
- `c2_cleanup_order_test.go`, `c2_helper_fatal_injection.patch`, `c2_delay_retirement.patch` — probes for the tests added on branch `evalon/grpc-go-xd-554a9276`.

Fixture sanity check on the audited branch (fixture copied to `test/xds/eval_xds_server_interceptor_leak_test.go`):

```console
$ go test -race -count=1 -v -run 'TestEval_' ./test/xds | grep -E '^(--- |ok|FAIL)'
--- PASS: TestEval_ServerSideXDS_InterceptorLeak_RDSUpdate (0.04s)
--- PASS: TestEval_ServerSideXDS_InterceptorSwapOrder (0.01s)
--- PASS: TestEval_ServerSideXDS_InterceptorLeak_MultiGenerationRDSUpdate (0.02s)
--- PASS: TestEval_ServerSideXDS_PartialRouteFailure_ClosesInterceptors (0.02s)
--- PASS: TestEval_ServerSideXDS_PartialVirtualHostFailure_ClosesInterceptors (0.02s)
ok  	google.golang.org/grpc/test/xds	1.150s
```

## C1

Claim: replacing a published configuration releases the old configuration's server-filter references before closing the retired interceptors that depend on them. Adjudicated per branch on the 19 target branches.

Method: publish a route configuration with one route using filter `tracker` (refcount 1, one interceptor), then replace it in place with a configuration whose only route carries a `FilterConfig{disabled: true}` override for `tracker`. The replacement acquires no reference, so releasing the old reference drops the refcount to 0 and physically closes the filter (`refCountedServerFilter.Close` → `ServerFilter.Close`). The relative order of `filter#1-close` and `interceptor#1-close` in the recorded event log is therefore the order of "release retired filter reference" vs "close retired interceptor".

Commands (run in each worktree `/home/ubuntu/wt/<b>`; unit and end-to-end):

```sh
cp ~/repos/grpc-go/verify/repro/verify_listener_wrapper_test.go internal/xds/server/
go test -race -count=1 -v -run 'Test/Verify_C1' ./internal/xds/server
cp ~/repos/grpc-go/verify/repro/c1_filter_release_order_test.go test/xds/
go test -race -count=1 -v -run '^TestVerify_C1_FilterReleaseOrder$' ./test/xds
```

(Driver: `/home/ubuntu/run_c1_branches.sh`, logs in `/home/ubuntu/c1_results/<b>.{unit,integ}.log`. For `554a9276` the unit run was repeated after `git stash` of the C2 patches, which otherwise break compilation of the package's tests.)

Result — identical on all 19 branches (`8c607a23 c740ea2d bfa838ff a697ff50 be49ddea 81821b4d da10a3be 554a9276 c9e12354 a9737ee8 236e58b9 29a75b35 ff88f27f 1101a0b3 08e05fc6 29299069 611a4098 b5e97ca4 1957f499`):

```console
# unit probe, every branch
verify_listener_wrapper_test.go:208: C1 events after in-place update that disables the filter: [filter#1-build interceptor#1-build(filter#1) filter#1-close(#1) interceptor#1-close(#1)]
verify_listener_wrapper_test.go:224: C1 CONFIRMED: filter#1 physically closed (event 2) BEFORE its dependent retired interceptor#1 closed (event 3)
# end-to-end probe, every branch
c1_filter_release_order_test.go:241: PHASE 2 (replaced by config disabling tracker): events=[filter#1-build interceptor#1-build(filter#1) filter#1-close(#1) interceptor#1-close(#1)]
c1_filter_release_order_test.go:257: C1 CONFIRMED: filter#1 physically closed (event 2) BEFORE its dependent retired interceptor#1 was closed (event 3)
```

Control on the audited branch (`~/repos/grpc-go`, same two commands): the order is the other way round.

```console
verify_listener_wrapper_test.go:208: C1 events after in-place update that disables the filter: [filter#1-build interceptor#1-build(filter#1) interceptor#1-close(#1) filter#1-close(#1)]
verify_listener_wrapper_test.go:226: C1 REFUTED: retired interceptor#1 closed (event 2) BEFORE filter#1 was physically closed (event 3)
c1_filter_release_order_test.go:241: PHASE 2 (replaced by config disabling tracker): events=[filter#1-build interceptor#1-build(filter#1) interceptor#1-close(#1) filter#1-close(#1)]
c1_filter_release_order_test.go:259: C1 REFUTED: retired interceptor#1 closed (event 2) BEFORE filter#1 was physically closed (event 3)
```

Why (code, corroborating the observation): on every target branch `constructUsableRouteConfiguration` is the base version that still ends with `for _, sf := range fc.serverFilters { sf.Close() }; fc.serverFilters = serverFilters` — i.e. it releases the *old* configuration's filter references while building the new one — and only afterwards the caller does `fc.usableRouteConfiguration.Swap(urc)` + `old.close()/stop()/closeInterceptors()` (e.g. `git diff 4ee6ac46..evalrepo/evalon/grpc-go-xd-a9737ee8 -- internal/xds/server`). The audited branch instead moved both into `applyConfiguration`, which stops `oldURC` first and releases `oldFilters` second.

Impact reasoning: on the target branches, whenever an in-place RDS update stops using a filter (disable override, route removal), the shared `ServerFilter` is physically closed while interceptor instances built from it are still installed and still serving in-flight RPCs on the retired configuration; their own `Close()` then runs against an already-closed parent. With the filter still referenced by the new configuration nothing is observable, which is why the ordinary same-filter update path hides the ordering bug.

Additional per-branch observation (not part of C1's verdict, recorded for C3/C8 context): PHASE 3 of the end-to-end probe (`tracker` re-enabled) reported `C3/C8 CONFIRMED: after disable/re-enable, interceptor#2 was built on filter#1 which had already been physically closed (no new filter built)` on all 19 branches as well.

## C2

Target branch `evalon/grpc-go-xd-554a9276` (commit `5499b38a`), worktree `/home/ubuntu/wt/554a9276`.

### Part 1 — listener cleanup registration

Inspection (`internal/xds/server/listener_wrapper_test.go`, helper `newListenerWrapperForRDSTest`, lines 126-155 on the branch): `lis, err := testutils.LocalTCPListener()` (l.126) … `if len(fcm.filterChains) != 1 { t.Fatalf(...) }` (l.136-138) … `t.Cleanup(func() { lis.Close() })` (l.155). The fatal assertion sits between listener creation and cleanup registration.

Injection: `verify/repro/c2_helper_fatal_injection.patch` records `lis.Addr()` into a package variable and makes that same assertion fire (`|| verifyC2InjectFatal`). `verify/repro/c2_cleanup_order_test.go` calls the helper in a subtest and afterwards dials the recorded address.

```sh
cd /home/ubuntu/wt/554a9276
git apply ~/repos/grpc-go/verify/repro/c2_helper_fatal_injection.patch
cp ~/repos/grpc-go/verify/repro/c2_cleanup_order_test.go internal/xds/server/
go test -count=1 -v -run 'Test/.*Verify_C2' ./internal/xds/server
```

```console
=== RUN   Test/Verify_C2_HelperFatalLeavesListenerOpen/helper
    c2_cleanup_order_test.go:28: Filter chain manager has 1 filter chains, want 1
=== NAME  Test/Verify_C2_HelperFatalLeavesListenerOpen
    c2_cleanup_order_test.go:30: helper subtest passed=false; listener address recorded="127.0.0.1:42815"
    c2_cleanup_order_test.go:37: C2 part 1 CONFIRMED: listener 127.0.0.1:42815 still accepts connections after the helper's subtest ended (cleanup was never registered because Fatalf ran first)
```

Control (same files, `verifyC2InjectFatal = false` so the assertion passes and cleanup is registered):

```console
    c2_cleanup_order_test.go:30: helper subtest passed=true; listener address recorded="127.0.0.1:45453"
    c2_cleanup_order_test.go:40: C2 part 1 REFUTED: dial to 127.0.0.1:45453 failed after the helper's subtest ended: dial tcp 127.0.0.1:45453: connect: connection refused (listener was closed)
```

### Part 2 — retirement assertion synchronization

Inspection (`test/xds/xds_server_filter_state_retention_test.go`, added `TestServerSideXDS_FilterStateRetention_AcrossUpdates_RDSUpdate`): the loop breaks as soon as an RPC's interceptor reports `wantPath` (publication observed via `pathCh`), then immediately asserts `interceptorsDestroyed.Load() == i-1` (l.815). On this branch `handleRDSUpdate` publishes with `Swap` and closes the replaced interceptors afterwards on the xDS callback goroutine, while the RPC that observes the new configuration does not take `l.mu`.

Injection: `verify/repro/c2_delay_retirement.patch` adds `time.Sleep(300ms)` at the top of the test file's own `trackingInterceptor.Close()` (delaying retirement after publication).

```sh
cd /home/ubuntu/wt/554a9276
git apply ~/repos/grpc-go/verify/repro/c2_delay_retirement.patch
go test -race -count=1 -v -run 'Test/ServerSideXDS_FilterStateRetention_AcrossUpdates_RDSUpdate$' ./test/xds
```

```console
    xds_server_filter_state_retention_test.go:815: After RDS update 2: destroyed 0 interceptor instances, want: 1
--- FAIL: Test (0.64s)
    --- FAIL: Test/ServerSideXDS_FilterStateRetention_AcrossUpdates_RDSUpdate (0.64s)
FAIL	google.golang.org/grpc/test/xds	0.671s
```

Control (`git stash`, i.e. without the delay):

```console
--- PASS: Test (0.06s)
    --- PASS: Test/ServerSideXDS_FilterStateRetention_AcrossUpdates_RDSUpdate (0.06s)
ok  	google.golang.org/grpc/test/xds	1.087s
```

The assertion ran while the retirement was still in progress (`destroyed 0, want 1`); nothing in the test establishes retirement completion before asserting. Both parts CONFIRMED.

## C3

Audited branch. Probe `Test/Verify_C3C8_ClosedFilterReuse_{RDSError,DisableAllUses,ConstructionError}`: publish a one-route configuration (filter refcount 1), apply a transition that releases the last reference, dump `l.httpFilters`, then publish the one-route configuration again ("recovery").

```sh
cd ~/repos/grpc-go
cp verify/repro/verify_listener_wrapper_test.go internal/xds/server/
go test -race -count=1 -v -run 'Test/Verify_' ./internal/xds/server
```

```console
=== RUN   Test/Verify_C3C8_ClosedFilterReuse_RDSError
    verify_listener_wrapper_test.go:181:   httpFilters[{tracker verify.filter}] = wrapper 0xc0001183c0 refCnt=1 underlying=0xc0001183a8
    verify_listener_wrapper_test.go:360: C3/C8 rds-error: after transition: urc.err=injected rds error
    verify_listener_wrapper_test.go:181:   httpFilters[{tracker verify.filter}] = wrapper 0xc0001183c0 refCnt=0 underlying=0xc0001183a8
    verify_listener_wrapper_test.go:183:   len(l.httpFilters)=1
    verify_listener_wrapper_test.go:370: C3/C8 rds-error: filter#1 physically closed=true, wrapper retained in l.httpFilters=true
    verify_listener_wrapper_test.go:374: C3/C8 rds-error: after recovery: urc.err=<nil>
    verify_listener_wrapper_test.go:181:   httpFilters[{tracker verify.filter}] = wrapper 0xc0001183c0 refCnt=1 underlying=0xc0001183a8
    verify_listener_wrapper_test.go:187: C3/C8 rds-error after recovery: events=[filter#1-build interceptor#1-build(filter#1) interceptor#1-close(#1) filter#1-close(#1) interceptor#2-build-ON-CLOSED-filter#1]
    verify_listener_wrapper_test.go:380: C3/C8 rds-error CONFIRMED: recovery built interceptor#2 on filter#1 which had already been physically closed (filters built total=1)
    verify_listener_wrapper_test.go:187: C3/C8 rds-error after Close: events=[... interceptor#2-close(#1) filter#1-close(#2)]
    verify_listener_wrapper_test.go:388: C3/C8 rds-error: filter#1 physically closed 2 times
    verify_listener_wrapper_test.go:380: C3/C8 disable-all-uses CONFIRMED: recovery built interceptor#2 on filter#1 which had already been physically closed (filters built total=1)
    verify_listener_wrapper_test.go:380: C3/C8 construction-error CONFIRMED: recovery built interceptor#2 on filter#1 which had already been physically closed (filters built total=1)
```

Same wrapper pointer (`0xc0001183c0`) and same underlying filter (`0xc0001183a8`) before the transition, at refcount 0 after it, and again at refcount 1 after recovery: `getOrCreateServerFilterWithMap` found the cached zero-reference wrapper, incremented it and returned the closed filter (`BuildServerInterceptor` was invoked on a filter whose `Close()` had already run), and shutdown closed the filter a second time. All three recovery paths named by the claim (RDS error, disable/re-enable, construction error) reach the stale entry.

End-to-end confirmation of the disable/re-enable path through a real management server (`go test -race -count=1 -v -run '^TestVerify_C1_FilterReleaseOrder$' ./test/xds` on the audited branch):

```console
c1_filter_release_order_test.go:274: PHASE 3 (tracker re-enabled): events=[filter#1-build interceptor#1-build(filter#1) interceptor#1-close(#1) filter#1-close(#1) interceptor#2-build-ON-CLOSED-filter#1]
c1_filter_release_order_test.go:276: C3/C8 CONFIRMED: after disable/re-enable, interceptor#2 was built on filter#1 which had already been physically closed (no new filter built)
c1_filter_release_order_test.go:287: FINAL (after server stop): events=[filter#1-build interceptor#1-build(filter#1) interceptor#1-close(#1) filter#1-close(#1) interceptor#2-build-ON-CLOSED-filter#1 interceptor#2-close(#1) filter#1-close(#2)]
c1_filter_release_order_test.go:291:   filter#1 closes=2
```

Impact reasoning: the audited branch newly releases filter references on the error path (`applyConfiguration(urc, nil)`), which is what drives the refcount to 0 while the LDS-derived key is still "active" so `maybeUpdateFilterChains` never evicts the wrapper. Any recovery after an RDS error/NACK-before-update, after a construction failure, or after temporarily disabling a filter on all routes then serves RPCs through a `ServerFilter` that has already had `Close()` called (state torn down, e.g. ext_proc's connections), and `Close()` is called on it again at shutdown. Both parts CONFIRMED.

## C4

Audited branch, probes `Test/Verify_C4_PostShutdownAllocation` and `Test/Verify_C4_AdmittedCallbackReachesClosedListener` (same command as C3).

Part 1 — post-shutdown allocation: publish 2 routes, `l.Close()`, then call `l.handleRDSUpdate` again.

```console
=== RUN   Test/Verify_C4_PostShutdownAllocation
    verify_listener_wrapper_test.go:187: C4 after Close() followed by handleRDSUpdate: events=[filter#1-build interceptor#1-build(filter#1) interceptor#2-build(filter#1) interceptor#1-close(#1) interceptor#2-close(#1) filter#1-close(#1) interceptor#3-build-ON-CLOSED-filter#1 interceptor#4-build-ON-CLOSED-filter#1 interceptor#1-close(#2) interceptor#2-close(#2) filter#1-close(#2)]
    verify_listener_wrapper_test.go:192:   interceptor#3 filter#1 closes=0 builtOnClosedFilter=true
    verify_listener_wrapper_test.go:192:   interceptor#4 filter#1 closes=0 builtOnClosedFilter=true
    verify_listener_wrapper_test.go:269: C4 CONFIRMED (allocation part): interceptor#3 built after Close() and never closed (no owner)
    verify_listener_wrapper_test.go:269: C4 CONFIRMED (allocation part): interceptor#4 built after Close() and never closed (no owner)
    verify_listener_wrapper_test.go:274: C4: pre-shutdown interceptor#1 closed 2 times (re-closed by post-shutdown update)
    verify_listener_wrapper_test.go:274: C4: pre-shutdown interceptor#2 closed 2 times (re-closed by post-shutdown update)
```

`handleRDSUpdate` has no `closed.HasFired()` check and `Close()` leaves `activeFilterChainManager` non-nil, so the update runs on the stopped manager: it builds new interceptors on the already-closed filter, re-closes the already-closed ones, and nobody owns the new ones (only a *further* `Close()` call would close them: `interceptor#3 closes after an extra Close()=1`).

Part 2 — admitted callback reaches the closed listener. The `rdsHandler` callback is paused at the point right after `rdsWatcher.ResourceChanged` released `rw.mu` having seen `canceled == false` (the real callback is `handleRDSUpdate`; the probe wraps it), the listener is closed on another goroutine, then the callback resumes.

```console
=== RUN   Test/Verify_C4_AdmittedCallbackReachesClosedListener
    verify_listener_wrapper_test.go:328: C4: after Close(): watcher.canceled=true closed.HasFired=true
    verify_listener_wrapper_test.go:187: C4 after paused callback resumed post-Close: events=[filter#1-build interceptor#1-build(filter#1) interceptor#1-close(#1) filter#1-close(#1) interceptor#2-build-ON-CLOSED-filter#1 interceptor#1-close(#2) filter#1-close(#2)]
    verify_listener_wrapper_test.go:192:   interceptor#2 filter#1 closes=0 builtOnClosedFilter=true
    verify_listener_wrapper_test.go:343: C4 CONFIRMED (reachability part): admitted RDS callback entered handleRDSUpdate after Close() and built interceptor#2 which is never closed
```

`Close()` did set `canceled` and fire `closed`, yet the already-admitted callback still entered `handleRDSUpdate` and allocated. Code: `rdsWatcher.ResourceChanged` checks `canceled` under `rw.mu`, unlocks, then invokes `rw.parent.callback` with no lock held and no barrier; `listenerWrapper.Close` neither waits for in-flight callbacks nor is checked by `handleRDSUpdate`. Both parts CONFIRMED. (Contrast: several C1 target branches, e.g. `c740ea2d`, `81821b4d`, `31eb0f5f`, added `if l.closed.HasFired() { return }` to `handleRDSUpdate`; on `31eb0f5f` the same probes print `C4 REFUTED (allocation part)` / `C4 REFUTED (reachability part)`.)

## C5

Audited branch, probe `Test/Verify_C5_DoubleClose` (same command as C3): publish 2 routes, call `l.Close()` twice.

```console
=== RUN   Test/Verify_C5_DoubleClose
    verify_listener_wrapper_test.go:187: C5 after first Close: events=[filter#1-build interceptor#1-build(filter#1) interceptor#2-build(filter#1) interceptor#1-close(#1) interceptor#2-close(#1) filter#1-close(#1)]
    verify_listener_wrapper_test.go:187: C5 after second Close: events=[filter#1-build interceptor#1-build(filter#1) interceptor#2-build(filter#1) interceptor#1-close(#1) interceptor#2-close(#1) filter#1-close(#1) interceptor#1-close(#2) interceptor#2-close(#2)]
    verify_listener_wrapper_test.go:192:   interceptor#1 filter#1 closes=2 builtOnClosedFilter=false
    verify_listener_wrapper_test.go:192:   interceptor#2 filter#1 closes=2 builtOnClosedFilter=false
    verify_listener_wrapper_test.go:243: C5 CONFIRMED: interceptor#1 closed 2 times after two listenerWrapper.Close() calls, want 1
    verify_listener_wrapper_test.go:243: C5 CONFIRMED: interceptor#2 closed 2 times after two listenerWrapper.Close() calls, want 1
```

`Close()` calls `activeFilterChainManager.stop()` → `fc.usableRouteConfiguration.Load().stop()` each time; nothing clears the pointer or guards against repetition (`l.closed.Fire()` is not consulted). The filter wrapper itself is not double-closed (`fc.serverFilters` becomes nil in `applyConfiguration`… but `fc.stop()` releases the same slice both times; refcount goes negative silently: `filter#1 closes=1` because `refCnt.Add(-1)` only equals 0 once). CONFIRMED. Note on ordinariness: `grpc.Server.Stop`/`Serve` close each listener once (`s.lis` is nil-ed in `closeListenersLocked`), so the double call needs a second explicit `Close()`.

## C6

Target branch `evalon/grpc-go-xd-8c607a23` (commit `debc7ada`), worktree `/home/ubuntu/wt/8c607a23`. Same probes as C1 (unit + end-to-end), same commands run in the worktree.

```console
$ go test -race -count=1 -v -run 'Test/Verify_C1' ./internal/xds/server
    verify_listener_wrapper_test.go:208: C1 events after in-place update that disables the filter: [filter#1-build interceptor#1-build(filter#1) filter#1-close(#1) interceptor#1-close(#1)]
    verify_listener_wrapper_test.go:224: C1 CONFIRMED: filter#1 physically closed (event 2) BEFORE its dependent retired interceptor#1 closed (event 3)
$ go test -race -count=1 -v -run '^TestVerify_C1_FilterReleaseOrder$' ./test/xds
    c1_filter_release_order_test.go:241: PHASE 2 (replaced by config disabling tracker): events=[filter#1-build interceptor#1-build(filter#1) filter#1-close(#1) interceptor#1-close(#1)]
    c1_filter_release_order_test.go:257: C1 CONFIRMED: filter#1 physically closed (event 2) BEFORE its dependent retired interceptor#1 was closed (event 3)
```

`filter#1-close(#1)` is the physical `ServerFilter.Close()` (refcount reached 0 inside `constructUsableRouteConfiguration`'s `for _, sf := range fc.serverFilters { sf.Close() }`), and it precedes `interceptor#1-close(#1)`, which only happens in the caller's `fc.updateUsableRouteConfiguration(urc)` → `Swap(urc).close()`. CONFIRMED.

## C7

Target branch `evalon/grpc-go-xd-31eb0f5f` (commit `bcddabfd`), worktree `/home/ubuntu/wt/31eb0f5f`.

Fixture (exact archived bytes copied to `test/xds/eval_xds_server_interceptor_leak_test.go`):

```sh
cd /home/ubuntu/wt/31eb0f5f
cp /home/ubuntu/eval_tests/tests/eval_xds_server_interceptor_leak_test.go test/xds/eval_xds_server_interceptor_leak_test.go
go test -race -count=1 -v -run '^TestEval_ServerSideXDS_Partial(RouteFailure|VirtualHostFailure)_ClosesInterceptors$' ./test/xds
```

```console
    eval_xds_server_interceptor_leak_test.go:1131: Partial route conversion failure leaked interceptor: destroyed=1, want=2 (created=2)
--- FAIL: TestEval_ServerSideXDS_PartialRouteFailure_ClosesInterceptors (2.03s)
    eval_xds_server_interceptor_leak_test.go:1315: Partial virtual host conversion failure leaked interceptor: destroyed=1, want=2 (created=2)
--- FAIL: TestEval_ServerSideXDS_PartialVirtualHostFailure_ClosesInterceptors (2.02s)
FAIL	google.golang.org/grpc/test/xds	4.080s
```

Per-instance tracking (unit probe `Test/Verify_C7_PartialFailureRollback`: later route fails after two earlier routes built interceptors; later virtual host fails after an earlier virtual host built two):

```sh
cp ~/repos/grpc-go/verify/repro/verify_listener_wrapper_test.go internal/xds/server/
go test -race -count=1 -v -run 'Test/Verify_' ./internal/xds/server
```

```console
    verify_listener_wrapper_test.go:437: C7 later-route-fails: interceptor#2 built during the failed construction is still open after rollback
    verify_listener_wrapper_test.go:437: C7 later-route-fails: interceptor#3 built during the failed construction is still open after rollback
    verify_listener_wrapper_test.go:449: C7 CONFIRMED (later-route-fails): 2 interceptor(s) built before the injected failure were never closed, even after listener Close()
    verify_listener_wrapper_test.go:437: C7 later-virtual-host-fails: interceptor#2 built during the failed construction is still open after rollback
    verify_listener_wrapper_test.go:437: C7 later-virtual-host-fails: interceptor#3 built during the failed construction is still open after rollback
    verify_listener_wrapper_test.go:449: C7 CONFIRMED (later-virtual-host-fails): 2 interceptor(s) built before the injected failure were never closed, even after listener Close()
```

(The other probes on this branch print `C1 REFUTED`, `C3/C8 … REFUTED` ×3, `C4 REFUTED` ×2 and pass `Verify_C5_DoubleClose` — this branch guards `getOrCreateServerFilterWithMap` with `refCnt.Load() > 0`, checks `closed.HasFired()` in `handleRDSUpdate`, and makes `filterChainManager.stop` idempotent.)

Control on the audited branch (same unit probe): `C7 REFUTED (later-route-fails): all 2 interceptors built before the injected failure were closed during rollback` and likewise for `later-virtual-host-fails`; and the two fixture tests pass (see Setup).

Code: on `31eb0f5f` `convertVirtualHost`'s error path only closes `serverFilters` (the filter references), not the `rs[i].interceptor` already built, and `constructUsableRouteConfiguration`'s error path only closes `serverFilters`, not the interceptors in the already-converted `vhs`. Both parts CONFIRMED.

## C8

Audited branch. Same runs as C3 (the claim is the same mechanism observed from the cache's side). Key lines, repeated here so this section stands alone:

```console
$ go test -race -count=1 -v -run 'Test/Verify_C3C8' ./internal/xds/server
=== RUN   Test/Verify_C3C8_ClosedFilterReuse_RDSError
    verify_listener_wrapper_test.go:181:   httpFilters[{tracker verify.filter}] = wrapper 0xc0001183c0 refCnt=1 underlying=0xc0001183a8
    verify_listener_wrapper_test.go:360: C3/C8 rds-error: after transition: urc.err=injected rds error
    verify_listener_wrapper_test.go:181:   httpFilters[{tracker verify.filter}] = wrapper 0xc0001183c0 refCnt=0 underlying=0xc0001183a8
    verify_listener_wrapper_test.go:370: C3/C8 rds-error: filter#1 physically closed=true, wrapper retained in l.httpFilters=true
    verify_listener_wrapper_test.go:181:   httpFilters[{tracker verify.filter}] = wrapper 0xc0001183c0 refCnt=1 underlying=0xc0001183a8
    verify_listener_wrapper_test.go:187: C3/C8 rds-error after recovery: events=[filter#1-build interceptor#1-build(filter#1) interceptor#1-close(#1) filter#1-close(#1) interceptor#2-build-ON-CLOSED-filter#1]
    verify_listener_wrapper_test.go:380: C3/C8 rds-error CONFIRMED: recovery built interceptor#2 on filter#1 which had already been physically closed (filters built total=1)
    verify_listener_wrapper_test.go:388: C3/C8 rds-error: filter#1 physically closed 2 times
=== RUN   Test/Verify_C3C8_ClosedFilterReuse_DisableAllUses
    verify_listener_wrapper_test.go:380: C3/C8 disable-all-uses CONFIRMED: recovery built interceptor#2 on filter#1 which had already been physically closed (filters built total=1)
    verify_listener_wrapper_test.go:388: C3/C8 disable-all-uses: filter#1 physically closed 2 times
```

- Closed wrapper retained in cache: after the error transition / removal of all uses the underlying filter's `Close()` ran (`physically closed=true`) and the entry is still in `l.httpFilters` with `refCnt=0` (`len(l.httpFilters)=1`, same wrapper pointer). CONFIRMED.
- Closed wrapper returned for construction: the next lookup returned that wrapper (`refCnt` back to 1, no `filter#2-build`, `interceptor#2-build-ON-CLOSED-filter#1`), and the filter was closed a second time at shutdown. CONFIRMED.

End-to-end (real management server, disable then re-enable) on the audited branch: `c1_filter_release_order_test.go:276: C3/C8 CONFIRMED: after disable/re-enable, interceptor#2 was built on filter#1 which had already been physically closed (no new filter built)`; final `filter#1 closes=2`.

## C9

Audited branch, `~/repos/grpc-go`. The assessment provisions the fixture at `test/xds/eval_xds_server_interceptor_leak_test.go`; runs were done both with that file in place (assessment layout) and without it (branch as pushed).

With the fixture in place, exact command from the claim, run twice:

```console
$ cp /home/ubuntu/eval_tests/tests/eval_xds_server_interceptor_leak_test.go test/xds/eval_xds_server_interceptor_leak_test.go
$ time go test -race -cpu 1,4 -timeout 7m ./test/xds/... -count=1
--- FAIL: TestEval_ServerSideXDS_InterceptorLeak_RDSUpdate (0.00s)
    setup.go:45: Created new snapshot cache...
panic: Log in goroutine after Test/WrrLocality has completed: INFO server.go:732 [core] [Server #1165] Server created  (t=+5.645001801s)
	 [recovered, repanicked]
...
google.golang.org/grpc/internal/grpctest.(*tLogger).log(...)  /home/ubuntu/repos/grpc-go/internal/grpctest/tlogger.go:133
...
google.golang.org/grpc/test/xds_test.TestEval_ServerSideXDS_InterceptorLeak_RDSUpdate(0xc0008a61c0)
	/home/ubuntu/repos/grpc-go/test/xds/eval_xds_server_interceptor_leak_test.go:167 +0x57b
FAIL	google.golang.org/grpc/test/xds	12.177s
FAIL
real	0m13.081s
exit=1
```

Second run (`/home/ubuntu/c9_run3_fixture.log`): `panic: Log in goroutine after Test/WrrLocality has completed: INFO server.go:732 [core] [Server #1166] Server created  (t=+6.706039674s)` … `FAIL	google.golang.org/grpc/test/xds	13.301s`, `exit=1`. Deterministic 2/2.

Mechanism: the fixture's tests are top-level `TestEval_*` functions rather than `(s)` methods run under `grpctest.RunSubTests`. With `-cpu 1,4` the test binary runs the whole list twice; in the second pass the `grpctest` `tLogger` (the process-wide grpclog sink) still points at the last subtest of the first pass's `Test` (`Test/WrrLocality`), so the first grpclog line emitted by `TestEval_ServerSideXDS_InterceptorLeak_RDSUpdate` panics with "Log in goroutine after … has completed".

Diagnostics:

```console
$ time go test -race -cpu 4 -timeout 7m ./test/xds/... -count=1      # fixture present, single CPU setting
ok  	google.golang.org/grpc/test/xds	13.594s
real	0m14.524s
exit=0
$ rm test/xds/eval_xds_server_interceptor_leak_test.go
$ time go test -race -cpu 1,4 -timeout 7m ./test/xds/... -count=1    # branch as pushed, no fixture
ok  	google.golang.org/grpc/test/xds	25.667s
real	0m27.164s
exit=0
```

So: with the eval's fixture provisioned (the layout assessment executes) the complete suite with `-race -cpu 1,4 -timeout 7m` fails deterministically; the branch's own test files alone pass in 27 s, far inside the timeout, with no race reports. The failure is an interaction between the fixture's top-level tests and the repository's `grpctest` logger under multi-`-cpu` passes, not a defect in the solution's production code or its own tests. CONFIRMED for the command as stated in the assessment layout; the solution-only suite is green.
