## Setup (shared by all sections)

Audited branch: `verify/grpc-go-xds-rds-interceptor-lifecycle-leak-v-4671e799` = `origin/grpc-go-xds-rds-interceptor-lifecycle-leak-perfect` (HEAD `614cb739`, base `4ee6ac46`). Go `go1.25.7 linux/amd64`.

Claim-target branches live in a second repository and were fetched as remote `claims`; each was checked out in its own worktree under `/home/ubuntu/wt/<suffix>`:

```sh
cd ~/repos/grpc-go
git fetch origin grpc-go-xds-rds-interceptor-lifecycle-leak-perfect
git checkout -b verify/grpc-go-xds-rds-interceptor-lifecycle-leak-v-4671e799 origin/grpc-go-xds-rds-interceptor-lifecycle-leak-perfect
git remote add claims https://github.com/kaitranntt-evals/grpc-go-xds-rds-interceptor-lifecycle-leak
git fetch claims 'refs/heads/evalon/*:refs/remotes/claims/evalon/*'
for b in 52bc3ca4 ae161d92 28e4cebc ac3befb6 e3f8a75a ba023a60 b06155db d73ed9de 6187fd04 bbbc38f5 dffb91ff 28103245 8a08d046 08348126 da859e63 eb95b492 918a9f98 67c4e0f4 430d1275 954a2fa4; do
  git worktree add /home/ubuntu/wt/$b claims/evalon/grpc-go-xd-$b
done
```

Branch heads (all based on `4ee6ac46`): 52bc3ca4=`cf89b11c` ae161d92=`c6663b5e` 28e4cebc=`cc56f965` ac3befb6=`821e464a` e3f8a75a=`d42d088a` ba023a60=`d8b1794e` b06155db=`5339cbd5` d73ed9de=`8a65db07` 6187fd04=`13409f2d` bbbc38f5=`465b84e6` dffb91ff=`0fb1297d` 28103245=`73f3fc44` 8a08d046=`e55a3a09` 08348126=`3d6af435` da859e63=`d731aa32` eb95b492=`27e7e073` 918a9f98=`8890910e` 67c4e0f4=`177697ae` 430d1275=`a0159981` 954a2fa4=`972b4f19`.

Two instrumentation tests were written (both under `verify/repro/`; each asserts the *correct* lifecycle order, so a FAIL demonstrates the suspected problem). Both carry a `//go:build verify` constraint so they never affect `go build/vet/test ./...` from the repo root; run them with `-tags verify` after copying them into place (the per-branch runs below were executed before the tag was added, from otherwise byte-identical files):

- `verify_lifecycle_internal_test.go` (package `server`, copied into `internal/xds/server/`): builds a `listenerWrapper` with one active `filterChain` carrying a tracking HTTP filter and drives `listenerWrapper.handleRDSUpdate` directly — the exact code path an RDS update takes on every branch. The tracking filter records, in order: server-filter construction, interceptor creation, interceptor closure (with the filter's `refCountedServerFilter.refCnt` and destroyed-count observed *at the moment of closure*), and server-filter destruction (`ServerFilter.Close()` called by `refCountedServerFilter.Close()` when the count reaches zero).
- `verify_e2e_filter_lifecycle_test.go` (package `xds_test`, copied into `test/xds/`): end-to-end through `xds.NewGRPCServer` + a real management server; an in-place RDS replacement that disables the tracked filter on every route (route `typed_per_filter_config` `FilterConfig{disabled:true}`, requires `envconfig.XDSClientExtProcEnabled`).

The fixture `tests/eval_xds_server_interceptor_leak_test.go` was copied byte-exact to `test/xds/eval_xds_server_interceptor_leak_test.go` and run on the audited branch (all 5 subtests pass). Its filter builders implement `Close() {}` as a no-op, so the fixture does not observe server-filter release/destruction order at all; the instrumentation above was needed for C1–C3.

```console
$ cp /home/ubuntu/eval_tests/tests/eval_xds_server_interceptor_leak_test.go test/xds/ && go test -v -run '^Test$/^Eval_' ./test/xds -race -count=1 | grep -E "^(=== RUN|--- |PASS|FAIL|ok)"
=== RUN   Test
=== RUN   Test/Eval_ServerSideXDS_InterceptorLeak_MultiGenerationRDSUpdate
=== RUN   Test/Eval_ServerSideXDS_InterceptorLeak_RDSUpdate
=== RUN   Test/Eval_ServerSideXDS_InterceptorSwapOrder
=== RUN   Test/Eval_ServerSideXDS_PartialRouteFailure_ClosesInterceptors
=== RUN   Test/Eval_ServerSideXDS_PartialVirtualHostFailure_ClosesInterceptors
--- PASS: Test (0.23s)
PASS
ok  	google.golang.org/grpc/test/xds	1.261s
```

## C1

**Claim:** during successful replacement of a published route configuration, retired server-filter references are released before the dependent interceptors close. **Verdict: CONFIRMED on all 20 branches.**

Mechanism (same on all 20 branches, verified by the runs below): every branch still calls the unchanged `filterChain.constructUsableRouteConfiguration`, which ends with `for _, sf := range fc.serverFilters { sf.Close() }; fc.serverFilters = serverFilters` — i.e. the retired configuration's `refCountedServerFilter` references are released *inside construction*. Only afterwards does the caller (`handleRDSUpdate` / `updateUsableRouteConfiguration` / `swapUsableRouteConfiguration` / `Swap(urc).stop()` etc., naming differs per branch) close the retired interceptors. The audited branch `614cb739` instead has `applyConfiguration`, which runs `oldURC.stop()` before `for _, sf := range oldFilters { sf.Close() }`.

### Commands

```sh
# on each claim worktree
cp ~/repos/grpc-go/verify/repro/verify_lifecycle_internal_test.go /home/ubuntu/wt/$b/internal/xds/server/
(cd /home/ubuntu/wt/$b && go test -tags verify -race -count=1 -v -run 'Test/Verify_' ./internal/xds/server)
rm /home/ubuntu/wt/$b/internal/xds/server/verify_lifecycle_internal_test.go

cp ~/repos/grpc-go/verify/repro/verify_e2e_filter_lifecycle_test.go /home/ubuntu/wt/$b/test/xds/
(cd /home/ubuntu/wt/$b && go test -tags verify -race -count=1 -v -run 'Test/Verify_E2E' ./test/xds)
rm /home/ubuntu/wt/$b/test/xds/verify_e2e_filter_lifecycle_test.go
```

### Control run on the audited branch (`614cb739`) — correct order, tests PASS

```console
$ go test -tags verify -race -count=1 -v -run 'Test/Verify_' ./internal/xds/server
=== RUN   Test/Verify_C1_C3_ReplaceDisablingFilter_DestroyAfterInterceptorsClose
    verify_lifecycle_internal_test.go:196: trace:
          filter_built#1
          interceptor_created#1(filter#1)
          interceptor_created#2(filter#1)
          --- in-place replacement (filter disabled on every route) ---
          interceptor_closed#1(filter#1) refCnt=2 destroyed=0
          interceptor_closed#2(filter#1) refCnt=2 destroyed=0
          FILTER_DESTROYED#1
=== RUN   Test/Verify_C1_ReplaceKeepingFilter_RefsReleasedAfterInterceptorsClose
    verify_lifecycle_internal_test.go:171: trace:
          filter_built#1
          interceptor_created#1(filter#1)
          --- in-place replacement (filter still enabled on every route) ---
          interceptor_created#2(filter#1)
          interceptor_closed#1(filter#1) refCnt=2 destroyed=0
    --- PASS: Test/Verify_C1_C3_ReplaceDisablingFilter_DestroyAfterInterceptorsClose (0.00s)
    --- PASS: Test/Verify_C1_ReplaceKeepingFilter_RefsReleasedAfterInterceptorsClose (0.00s)
    --- PASS: Test/Verify_C2_ErrorAfterSuccess_ReleasesFilterRefs (0.00s)
ok  	google.golang.org/grpc/internal/xds/server	1.028s

$ go test -tags verify -race -count=1 -v -run 'Test/Verify_E2E' ./test/xds
    verify_e2e_filter_lifecycle_test.go:253: after replacement: created=2 closed=2 filtersDestroyed=1
          filter_built#1
          interceptor_created#1(filter#1)
          interceptor_created#2(filter#1)
          interceptor_closed#1(filter#1) filtersDestroyedSoFar=0
          interceptor_closed#2(filter#1) filtersDestroyedSoFar=0
          FILTER_DESTROYED#1
    --- PASS: Test/Verify_E2E_RDSReplaceDisablingFilter_InterceptorsCloseBeforeFilterDestroyed (0.04s)
ok  	google.golang.org/grpc/test/xds	1.071s
```

### Claim branches — identical output on every one of the 20 (shown verbatim for `52bc3ca4`; the other 19 logs differ only in timings)

Everyday scenario (replacement keeps the filter enabled): the retired interceptor is closed while the filter's refcount is already back to 1, i.e. the retired reference was released *before* the dependent interceptor closed (correct order shows `refCnt=2`).

```console
=== RUN   Test/Verify_C1_ReplaceKeepingFilter_RefsReleasedAfterInterceptorsClose
    verify_lifecycle_internal_test.go:171: trace:
          filter_built#1
          interceptor_created#1(filter#1)
          --- in-place replacement (filter still enabled on every route) ---
          interceptor_created#2(filter#1)
          interceptor_closed#1(filter#1) refCnt=1 destroyed=0
    verify_lifecycle_internal_test.go:177: retired interceptor closed with tracker refCnt = 1, want 2 (retired filter reference was released BEFORE its dependent interceptor closed)
```

Replacement that disables the filter on every route: the last reference is released (filter destroyed) *before* either dependent interceptor is closed.

```console
=== RUN   Test/Verify_C1_C3_ReplaceDisablingFilter_DestroyAfterInterceptorsClose
    verify_lifecycle_internal_test.go:196: trace:
          filter_built#1
          interceptor_created#1(filter#1)
          interceptor_created#2(filter#1)
          --- in-place replacement (filter disabled on every route) ---
          FILTER_DESTROYED#1
          interceptor_closed#1(filter#1) refCnt=0 destroyed=1
          interceptor_closed#2(filter#1) refCnt=0 destroyed=1
    verify_lifecycle_internal_test.go:208: interceptor#1 was closed AFTER its server filter had already been destroyed (destroyed=1, refCnt=0 at close)
    verify_lifecycle_internal_test.go:208: interceptor#2 was closed AFTER its server filter had already been destroyed (destroyed=1, refCnt=0 at close)
--- FAIL: Test (0.00s)
    --- FAIL: Test/Verify_C1_C3_ReplaceDisablingFilter_DestroyAfterInterceptorsClose (0.00s)
    --- FAIL: Test/Verify_C1_ReplaceKeepingFilter_RefsReleasedAfterInterceptorsClose (0.00s)
    --- FAIL: Test/Verify_C2_ErrorAfterSuccess_ReleasesFilterRefs (0.00s)
FAIL	google.golang.org/grpc/internal/xds/server	0.027s
```

End-to-end through the public API (two filter chains, v4/v6 loopback, share one filter instance with refCnt=2; the second chain's release drops it to 0):

```console
    verify_e2e_filter_lifecycle_test.go:253: after replacement: created=2 closed=2 filtersDestroyed=1
          filter_built#1
          interceptor_created#1(filter#1)
          interceptor_created#2(filter#1)
          interceptor_closed#1(filter#1) filtersDestroyedSoFar=0
          FILTER_DESTROYED#1
          interceptor_closed#2(filter#1) filtersDestroyedSoFar=1
    verify_e2e_filter_lifecycle_test.go:260: interceptor#2 was closed AFTER the server filter it depends on had already been destroyed (filtersDestroyedSoFar=1 at close)
    --- FAIL: Test/Verify_E2E_RDSReplaceDisablingFilter_InterceptorsCloseBeforeFilterDestroyed (0.04s)
FAIL	google.golang.org/grpc/test/xds	0.068s
```

Per-branch summary, extracted with `grep -E "retired interceptor closed with tracker refCnt|was closed AFTER|^(ok|FAIL|EXIT)" /home/ubuntu/wt/results/{internal,e2e}_$b.log`:

| branch | internal: refCnt at retired-interceptor close (want 2) | internal: destroy before close | e2e: destroy before close | exit |
|---|---|---|---|---|
| 52bc3ca4 `cf89b11c` | 1 | yes (both interceptors) | yes (interceptor#2) | FAIL/FAIL |
| ae161d92 `c6663b5e` | 1 | yes | yes | FAIL/FAIL |
| 28e4cebc `cc56f965` | 1 | yes | yes | FAIL/FAIL |
| ac3befb6 `821e464a` | 1 | yes | yes | FAIL/FAIL |
| e3f8a75a `d42d088a` | 1 | yes | yes | FAIL/FAIL |
| ba023a60 `d8b1794e` | 1 | yes | yes | FAIL/FAIL |
| b06155db `5339cbd5` | 1 | yes | yes | FAIL/FAIL |
| d73ed9de `8a65db07` | 1 | yes | yes | FAIL/FAIL |
| 6187fd04 `13409f2d` | 1 | yes | yes | FAIL/FAIL |
| bbbc38f5 `465b84e6` | 1 | yes | yes | FAIL/FAIL |
| dffb91ff `0fb1297d` | 1 | yes | yes | FAIL/FAIL |
| 28103245 `73f3fc44` | 1 | yes | yes | FAIL/FAIL |
| 8a08d046 `e55a3a09` | 1 | yes | yes | FAIL/FAIL |
| 08348126 `3d6af435` | 1 | yes | yes | FAIL/FAIL |
| da859e63 `d731aa32` | 1 | yes | yes | FAIL/FAIL |
| eb95b492 `27e7e073` | 1 | yes | yes | FAIL/FAIL |
| 918a9f98 `8890910e` | 1 | yes | yes | FAIL/FAIL |
| 67c4e0f4 `177697ae` | 1 | yes | yes | FAIL/FAIL |
| 430d1275 `a0159981` | 1 | yes | yes | FAIL/FAIL |
| 954a2fa4 `972b4f19` | 1 | yes | yes | FAIL/FAIL |

Impact reasoning: `refCountedServerFilter.Close()` calls the real `ServerFilter.Close()` when the count hits zero. On every claim branch an in-place RDS update releases the retired references first, so whenever the replacement no longer references a filter (filter disabled on every route, or — with the same code — the filter chain being the last user), the filter is torn down while interceptors built from it are still installed and about to be closed; `interceptor.Close()` then runs against an already-destroyed filter. In the everyday case (filter still used) the refcount transiently dips (2→1 instead of staying at 2 until the old interceptor is closed), which is harmless for stateless filters but violates the ownership order the interceptors depend on. No public API workaround; the fix is the audited branch's ordering (`oldURC.stop()` before releasing `oldFilters`), i.e. move the `fc.serverFilters` release out of `constructUsableRouteConfiguration` into the swap step.

## C2

**Claim:** an RDS error that retires a previously published route configuration does not release that configuration's server-filter references. **Verdict: CONFIRMED on both target branches (430d1275 and b06155db).**

A ResourceError-after-success cannot be produced through the real xDS client in this tree (RDS is not a state-of-the-world type, a NACK after a valid update is delivered as an *ambient* error with cached data, and `fail_on_data_errors` is not implemented — `grep -rn FailOnDataErrors internal/xds` returns nothing), so the error transition was driven exactly the way the branches' own unit tests do (`listener_wrapper_test.go` on those branches, and the audited branch's `TestHandleRDSUpdate_ErrorAfterSuccess`): `lw.handleRDSUpdate(routeName, rdsWatcherUpdate{err: rdsErr})` after a successful `handleRDSUpdate(..., rdsWatcherUpdate{data: cfg})`.

```sh
cp ~/repos/grpc-go/verify/repro/verify_lifecycle_internal_test.go /home/ubuntu/wt/$b/internal/xds/server/
(cd /home/ubuntu/wt/$b && go test -tags verify -race -count=1 -v -run 'Test/Verify_C2' ./internal/xds/server)
rm /home/ubuntu/wt/$b/internal/xds/server/verify_lifecycle_internal_test.go
```

Control on the audited branch `614cb739` (error path calls `applyConfiguration(urc, nil)` which releases `oldFilters`):

```console
=== RUN   Test/Verify_C2_ErrorAfterSuccess_ReleasesFilterRefs
          --- RDS error after success ---
          interceptor_closed#1(filter#1) refCnt=2 destroyed=0
          interceptor_closed#2(filter#1) refCnt=2 destroyed=0
          FILTER_DESTROYED#1
          after error: refCnt=0 destroyed=1 len(fc.serverFilters)=0
    verify_lifecycle_internal_test.go:250: post-stop: after stop: refCnt=0 destroyed=1
    --- PASS: Test/Verify_C2_ErrorAfterSuccess_ReleasesFilterRefs (0.00s)
```

Branch `430d1275` (`a0159981`) — `/home/ubuntu/wt/results/internal_430d1275.log`:

```console
=== RUN   Test/Verify_C2_ErrorAfterSuccess_ReleasesFilterRefs
    verify_lifecycle_internal_test.go:228: trace:
          filter_built#1
          interceptor_created#1(filter#1)
          interceptor_created#2(filter#1)
          --- RDS error after success ---
          interceptor_closed#1(filter#1) refCnt=2 destroyed=0
          interceptor_closed#2(filter#1) refCnt=2 destroyed=0
          after error: refCnt=2 destroyed=0 len(fc.serverFilters)=2
    verify_lifecycle_internal_test.go:238: after RDS error tracker refCnt = 2, want 0 (retired configuration's server-filter references were NOT released)
    verify_lifecycle_internal_test.go:241: after RDS error tracker destroyed 0 times, want 1
    verify_lifecycle_internal_test.go:244: after RDS error len(fc.serverFilters) = 2, want 0
    verify_lifecycle_internal_test.go:250: post-stop: after stop: refCnt=0 destroyed=1
    --- FAIL: Test/Verify_C2_ErrorAfterSuccess_ReleasesFilterRefs (0.00s)
```

Branch `b06155db` (`5339cbd5`) — `/home/ubuntu/wt/results/internal_b06155db.log`:

```console
=== RUN   Test/Verify_C2_ErrorAfterSuccess_ReleasesFilterRefs
    verify_lifecycle_internal_test.go:228: trace:
          filter_built#1
          interceptor_created#1(filter#1)
          interceptor_created#2(filter#1)
          --- RDS error after success ---
          interceptor_closed#1(filter#1) refCnt=2 destroyed=0
          interceptor_closed#2(filter#1) refCnt=2 destroyed=0
          after error: refCnt=2 destroyed=0 len(fc.serverFilters)=2
    verify_lifecycle_internal_test.go:238: after RDS error tracker refCnt = 2, want 0 (retired configuration's server-filter references were NOT released)
    verify_lifecycle_internal_test.go:241: after RDS error tracker destroyed 0 times, want 1
    verify_lifecycle_internal_test.go:244: after RDS error len(fc.serverFilters) = 2, want 0
    verify_lifecycle_internal_test.go:250: post-stop: after stop: refCnt=0 destroyed=1
    --- FAIL: Test/Verify_C2_ErrorAfterSuccess_ReleasesFilterRefs (0.00s)
```

On both branches the error state is installed (`errors.Is(urc.err, rdsErr)` assertion did not fire) and the retired interceptors are closed exactly once, but the two `refCountedServerFilter` references stay in `fc.serverFilters` (`refCnt=2`, filter not destroyed) until `filterChainManager.stop()` at shutdown (`after stop: refCnt=0 destroyed=1`) — or until a later successful RDS update's `constructUsableRouteConfiguration` releases them. Interceptor closure is therefore the *only* cleanup performed by the error transition; the server-filter references are neither closed nor cleared. Impact: while the filter chain sits in the error state, the server filter instance (and any state it holds — e.g. an ext_proc stream/connection) is kept alive with no interceptor using it; the leak is bounded (released on next successful RDS or shutdown), so it is a bounded retention rather than an unbounded leak. Identical behaviour was observed on all other 18 claim branches (same `after error: refCnt=2 destroyed=0 len(fc.serverFilters)=2` line), but only the two listed are claim targets.

## C3

**Claim (branch `52bc3ca4`, HEAD `cf89b11c`):** a successful replacement that disables a filter on every route destroys the retired filter before closing the interceptors that depend on it. Parts: (a) *construction release order* — `constructUsableRouteConfiguration` releases the previous configuration's server-filter references before its dependent retired interceptors close; (b) *replacement trigger* — such a replacement reaches that release with no other live reference keeping the filter alive. **Verdict: CONFIRMED (both parts hold).**

On this branch `handleRDSUpdate` does `urc := fc.constructUsableRouteConfiguration(*rcu.data, l.getOrCreateServerFilterLocked)` (which internally runs `for _, sf := range fc.serverFilters { sf.Close() }`) and only then `fc.usableRouteConfiguration.Swap(urc).closeInterceptors()`.

```sh
cp ~/repos/grpc-go/verify/repro/verify_lifecycle_internal_test.go /home/ubuntu/wt/52bc3ca4/internal/xds/server/
(cd /home/ubuntu/wt/52bc3ca4 && go test -tags verify -race -count=1 -v -run 'Test/Verify_' ./internal/xds/server)
rm /home/ubuntu/wt/52bc3ca4/internal/xds/server/verify_lifecycle_internal_test.go
cp ~/repos/grpc-go/verify/repro/verify_e2e_filter_lifecycle_test.go /home/ubuntu/wt/52bc3ca4/test/xds/
(cd /home/ubuntu/wt/52bc3ca4 && go test -tags verify -race -count=1 -v -run 'Test/Verify_E2E' ./test/xds)
rm /home/ubuntu/wt/52bc3ca4/test/xds/verify_e2e_filter_lifecycle_test.go
```

Part (a) — construction release order (`/home/ubuntu/wt/results/internal_52bc3ca4.log`). With the filter still enabled, the retired interceptor is closed when the count has already dropped from 2 to 1, i.e. the retired reference was released during construction, before the interceptor closed:

```console
=== RUN   Test/Verify_C1_ReplaceKeepingFilter_RefsReleasedAfterInterceptorsClose
          interceptor_created#2(filter#1)
          interceptor_closed#1(filter#1) refCnt=1 destroyed=0
    verify_lifecycle_internal_test.go:177: retired interceptor closed with tracker refCnt = 1, want 2 (retired filter reference was released BEFORE its dependent interceptor closed)
```

Part (b) — trigger: a replacement disabling the filter on every route (route override `httpfilter.DisabledFilterConfig{}` → `newInterceptor` skips `provider(filter)`, so the replacement takes no reference); the construction-time release drops the count to 0 and `ServerFilter.Close()` (destruction) runs before the dependent interceptors are closed:

```console
=== RUN   Test/Verify_C1_C3_ReplaceDisablingFilter_DestroyAfterInterceptorsClose
    verify_lifecycle_internal_test.go:196: trace:
          filter_built#1
          interceptor_created#1(filter#1)
          interceptor_created#2(filter#1)
          --- in-place replacement (filter disabled on every route) ---
          FILTER_DESTROYED#1
          interceptor_closed#1(filter#1) refCnt=0 destroyed=1
          interceptor_closed#2(filter#1) refCnt=0 destroyed=1
    verify_lifecycle_internal_test.go:208: interceptor#1 was closed AFTER its server filter had already been destroyed (destroyed=1, refCnt=0 at close)
    verify_lifecycle_internal_test.go:208: interceptor#2 was closed AFTER its server filter had already been destroyed (destroyed=1, refCnt=0 at close)
    --- FAIL: Test/Verify_C1_C3_ReplaceDisablingFilter_DestroyAfterInterceptorsClose (0.00s)
```

Same trigger end-to-end through `xds.NewGRPCServer` and a management server (`/home/ubuntu/wt/results/e2e_52bc3ca4.log`; the filter is shared by the v4 and v6 filter chains, so destruction happens on the second chain's release and precedes that chain's interceptor closure):

```console
    verify_e2e_filter_lifecycle_test.go:253: after replacement: created=2 closed=2 filtersDestroyed=1
          filter_built#1
          interceptor_created#1(filter#1)
          interceptor_created#2(filter#1)
          interceptor_closed#1(filter#1) filtersDestroyedSoFar=0
          FILTER_DESTROYED#1
          interceptor_closed#2(filter#1) filtersDestroyedSoFar=1
    verify_e2e_filter_lifecycle_test.go:260: interceptor#2 was closed AFTER the server filter it depends on had already been destroyed (filtersDestroyedSoFar=1 at close)
    --- FAIL: Test/Verify_E2E_RDSReplaceDisablingFilter_InterceptorsCloseBeforeFilterDestroyed (0.04s)
FAIL	google.golang.org/grpc/test/xds	0.068s
EXIT=1
```

The same two tests PASS on the audited branch `614cb739` (trace `interceptor_closed#1 … interceptor_closed#2 … FILTER_DESTROYED#1`, see C1), so they discriminate the ordering. Impact reasoning: `l.httpFilters` holds no reference of its own (`getOrCreateServerFilterWithMap` creates with `refCnt` 0 and the first user `incRef`s to 1), so nothing else keeps the filter alive; on this branch a control-plane push that disables a filter (or stops using it) tears the filter instance down while its interceptors are still the live configuration for a moment and then `Close()`s those interceptors against a destroyed filter. Fix: perform the `fc.serverFilters` release after `closeInterceptors()` of the retired configuration (as `applyConfiguration` on the audited branch does).

## C4

**Claim:** `go test -race -cpu 1,4 -timeout 7m ./xds/... ./test/xds/... -count=1` fails on the audited branch. **Verdict: REFUTED.**

Run twice from a clean worktree of the audited HEAD `614cb739` (`git worktree add /home/ubuntu/wt/audited HEAD`), no fixture overlays, no `-run` filter:

```sh
cd /home/ubuntu/wt/audited && go test -race -cpu 1,4 -timeout 7m ./xds/... ./test/xds/... -count=1; echo EXIT=$?
```

Run 1 (`/home/ubuntu/wt/c4_run1.log`, module downloads elided):

```console
ok  	google.golang.org/grpc/xds	15.425s
ok  	google.golang.org/grpc/xds/bootstrap	1.017s
ok  	google.golang.org/grpc/xds/csds	2.646s
ok  	google.golang.org/grpc/xds/googledirectpath	3.115s
ok  	google.golang.org/grpc/xds/test	2.183s
ok  	google.golang.org/grpc/test/xds	26.550s
EXIT=0
```

Run 2 (`/home/ubuntu/wt/c4_run2.log`):

```console
ok  	google.golang.org/grpc/xds	16.745s
ok  	google.golang.org/grpc/xds/bootstrap	1.034s
ok  	google.golang.org/grpc/xds/csds	2.934s
ok  	google.golang.org/grpc/xds/googledirectpath	3.138s
ok  	google.golang.org/grpc/xds/test	2.199s
ok  	google.golang.org/grpc/test/xds	27.044s
EXIT=0
```

`grep -c "WARNING: DATA RACE" /home/ubuntu/wt/c4_run1.log /home/ubuntu/wt/c4_run2.log` → `0` and `0`. All selected packages pass under both CPU settings with race detection, well inside the 7-minute timeout.
