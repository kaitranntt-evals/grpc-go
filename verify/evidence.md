# Evidence — behavioral audit `grpc-go-xds-rds-interceptor-lifecycle-leak`, run `v-cf340e45`

Observations only (commands and verbatim output). Go `go1.25.7 linux/amd64`. Every probe below is under `verify/repro/`; raw, unabridged logs of every run are under `verify/logs/`.

## Setup

```sh
cd ~/repos/grpc-go
git fetch origin grpc-go-xds-rds-interceptor-lifecycle-leak-perfect
git checkout -b verify/grpc-go-xds-rds-interceptor-lifecycle-leak-v-cf340e45 origin/grpc-go-xds-rds-interceptor-lifecycle-leak-perfect   # audited branch, HEAD 614cb739; used as the control
# The claim-target branches are not on origin; they live in the repository named in the claim URLs.
git remote add claims https://github.com/kaitranntt-evals/grpc-go-xds-rds-interceptor-lifecycle-leak
git fetch claims '+refs/heads/evalon/*:refs/remotes/claims/evalon/*'
for b in 00c5530a 13d57ae3 1463306c 18fa6ce5 272d6cf6 2aa192a0 2e29516a 379dc377 3a746ce5 5e2ca2e9 720be0d6 820eccb4 8755f744 8a4cfab2 941a85ec 955d1b6e 96cbe156 ae7b032e beb780c9 d67b2e69 dbde929d de9096e7 e422b156 e6df146d eb7df664 fea61a52; do
  git worktree add --detach ~/wt/$b claims/evalon/grpc-go-xd-$b
done
unzip eval_tests.zip -d ~/eval_fixture    # tests/eval_xds_server_interceptor_leak_test.go, sha256 09122ab416a9858c3f7bf37ce2ee150f742b799a4e71fab1878e0532c99ebb56
```

All target branches are a single commit on top of base `4ee6ac46fada69c06576cee108b009689a000520`. No production code change is committed anywhere; probes that need instrumentation apply it to the target worktree, run, and restore it (each script prints `git status --short` after restoring).

Probe files are copied into a worktree under a `zz_verify_*` name, run, and removed, e.g. `cp verify/repro/c1_refcount_probe_test.go ~/wt/<b>/internal/xds/server/zz_verify_c1_probe_test.go`. In the commands below `$R` is `~/repos/grpc-go/verify/repro`.

### Eval fixture results (archived bytes, run at `test/xds/eval_xds_server_interceptor_leak_test.go`)

```sh
for b in <every worktree>; do $R/run_eval_fixture.sh ~/wt/$b ~/eval_fixture/tests/eval_xds_server_interceptor_leak_test.go; done   # go test -v -run '^Test$/^Eval_' ./test/xds -race -count=1
```

Columns: `InterceptorLeak_RDSUpdate / InterceptorSwapOrder / InterceptorLeak_MultiGenerationRDSUpdate / PartialRouteFailure_ClosesInterceptors / PartialVirtualHostFailure_ClosesInterceptors`.

| branch | HEAD | fixture result |
|---|---|---|
| 00c5530a | bd043022 | PASS / PASS / PASS / PASS / PASS |
| 13d57ae3 | 30174e28 | PASS / PASS / PASS / FAIL / FAIL |
| 1463306c | 4d2584c8 | PASS / PASS / PASS / FAIL / FAIL |
| 18fa6ce5 | 39804296 | PASS / PASS / PASS / FAIL / PASS |
| 272d6cf6 | 8992ab4d | PASS / PASS / PASS / FAIL / FAIL |
| 2aa192a0 | 6d526791 | PASS / PASS / PASS / PASS / PASS |
| 2e29516a | 5f8a4c40 | PASS / PASS / PASS / FAIL / FAIL |
| 379dc377 | 422f3382 | PASS / PASS / PASS / FAIL / FAIL |
| 3a746ce5 | a9907225 | PASS / PASS / PASS / PASS / PASS |
| 5e2ca2e9 | 02d467ea | PASS / PASS / PASS / FAIL / FAIL |
| 720be0d6 | 7a293e2b | PASS / PASS / PASS / FAIL / FAIL |
| 820eccb4 | b3c4818e | PASS / PASS / PASS / PASS / PASS |
| 8755f744 | bbaa67dc | PASS / PASS / PASS / PASS / PASS |
| 8a4cfab2 | 55c52288 | PASS / PASS / PASS / PASS / PASS |
| 941a85ec | 1941a65a | PASS / PASS / PASS / FAIL / FAIL |
| 955d1b6e | 7af9afed | PASS / PASS / PASS / FAIL / PASS |
| 96cbe156 | 73052814 | PASS / PASS / PASS / FAIL / FAIL |
| ae7b032e | 124222ff | PASS / PASS / PASS / FAIL / FAIL |
| beb780c9 | 4034f809 | PASS / PASS / PASS / FAIL / FAIL |
| d67b2e69 | d7426c1e | PASS / PASS / PASS / FAIL / FAIL |
| dbde929d | 87ece900 | PASS / PASS / PASS / FAIL / FAIL |
| de9096e7 | 20a2842d | PASS / PASS / PASS / FAIL / FAIL |
| e422b156 | 432ec458 | PASS / PASS / PASS / FAIL / FAIL |
| e6df146d | d6d7ef67 | PASS / PASS / PASS / PASS / PASS |
| eb7df664 | e97654c8 | PASS / FAIL / PASS / PASS / PASS |
| fea61a52 | 915c769c | PASS / PASS / PASS / FAIL / FAIL |
| audited branch (control) | 614cb739 | PASS / PASS / PASS / PASS / PASS |

Failure messages, verbatim (identical on every branch that fails the respective check):

```console
eval_xds_server_interceptor_leak_test.go:1131: Partial route conversion failure leaked interceptor: destroyed=1, want=2 (created=2)
eval_xds_server_interceptor_leak_test.go:1315: Partial virtual host conversion failure leaked interceptor: destroyed=1, want=2 (created=2)
eval_xds_server_interceptor_leak_test.go:680: Interceptor instance was closed while only 2 interceptor instances had been created (initial configuration created 2); the replacement configuration must be constructed before the replaced configuration is stopped
```

These fixture failures measure other behaviours (interceptor cleanup after a partially failed conversion; build-before-close order) and are not used for any verdict below. What matters for the claims: the three in-place-update checks are green on every C1/C5 target and on the C3 target, i.e. the fixture does not observe the orderings C1, C3 and C5 are about.

## C1

**Claim:** in-place route-configuration replacement releases retired server-filter references during replacement construction, before all interceptors that depend on those references finish closing. Adjudicated independently on 15 branches.

**Probe:** `verify/repro/c1_refcount_probe_test.go` (package `server`). It builds a `listenerWrapper` + filter chain with one HTTP filter, delivers generation 1 and then an in-place replacement through the production `listenerWrapper.handleRDSUpdate`, and logs every `ServerFilter`/interceptor build and close with the production call stack and the value of the shared `refCountedServerFilter.refCnt` at that instant. Two cases: an ordinary replacement that keeps using the filter, and a replacement whose only route disables the filter. The probe changes no production code and never fails on ordering.

```sh
for b in 00c5530a 13d57ae3 379dc377 de9096e7 fea61a52 941a85ec 2e29516a 5e2ca2e9 96cbe156 e422b156 8755f744 8a4cfab2 ae7b032e beb780c9 720be0d6; do
  (cd ~/wt/$b && cp $R/c1_refcount_probe_test.go internal/xds/server/zz_verify_c1_probe_test.go &&
   go test -race -count=1 -v -run 'Test/VerifyC1' ./internal/xds/server/; rm internal/xds/server/zz_verify_c1_probe_test.go) > verify/logs/c1_$b.log 2>&1
done
# control: same two commands in ~/repos/grpc-go (audited branch) -> verify/logs/c1_CONTROL_perfect.log
```

Full output on [evalon/grpc-go-xd-00c5530a](https://github.com/kaitranntt-evals/grpc-go-xds-rds-interceptor-lifecycle-leak/tree/evalon/grpc-go-xd-00c5530a) (ordinary replacement subtest):

```console
=== RUN   Test/VerifyC1_RefCountAtRetiredInterceptorClose
zz_verify_c1_probe_test.go:170: PROBE event[0] ServerFilter.Build                 id=0 filterRefCnt=-1 stack=[server.getOrCreateServerFilterWithMap <- server.(*listenerWrapper).getOrCreateServerFilterLocked <- server.(*filterChain).newInterceptor <- server.(*filterChain).convertVirtualHost <- server.(*filterChain).constructUsableRouteConfiguration <- server.(*listenerWrapper).handleRDSUpdate]
zz_verify_c1_probe_test.go:170: PROBE event[1] Interceptor.Build                  id=1 filterRefCnt=-1 stack=[server.(*filterChain).newInterceptor <- server.(*filterChain).convertVirtualHost <- server.(*filterChain).constructUsableRouteConfiguration <- server.(*listenerWrapper).handleRDSUpdate]
zz_verify_c1_probe_test.go:170: PROBE event[2] --- gen1 active; in-place replacement starts (override=map[]) --- id=0 filterRefCnt=1 stack=[]
zz_verify_c1_probe_test.go:170: PROBE event[3] Interceptor.Build                  id=2 filterRefCnt=2 stack=[server.(*filterChain).newInterceptor <- server.(*filterChain).convertVirtualHost <- server.(*filterChain).constructUsableRouteConfiguration <- server.(*listenerWrapper).handleRDSUpdate]
zz_verify_c1_probe_test.go:170: PROBE event[4] Interceptor.Close                  id=1 filterRefCnt=1 stack=[server.(*interceptorList).Close <- server.(*listenerWrapper).handleRDSUpdate.(*usableRouteConfiguration).close.func1 <- server.(*usableRouteConfiguration).close <- server.(*listenerWrapper).handleRDSUpdate]
zz_verify_c1_probe_test.go:170: PROBE event[5] --- handleRDSUpdate(gen2) returned --- id=0 filterRefCnt=1 stack=[]
zz_verify_c1_probe_test.go:184: PROBE RESULT ordinary-replacement: filterRefCnt observed inside retired interceptor Close = 1 (2 => retired reference still held; 1 => retired reference already released)
```

Reading: at the start of the replacement the filter has 1 reference (generation 1). The replacement interceptor is built (refCnt 2), then generation 1's reference is dropped inside `constructUsableRouteConfiguration`, and only afterwards the retired interceptor #1 is closed — it observes refCnt 1, i.e. its own configuration's reference is already gone.

Same subtest on the audited branch (control):

```console
zz_verify_c1_probe_test.go:170: PROBE event[3] Interceptor.Build                  id=2 filterRefCnt=2 stack=[server.(*filterChain).newInterceptor <- server.(*filterChain).convertVirtualHost <- server.(*filterChain).updateUsableRouteConfiguration <- server.(*listenerWrapper).handleRDSUpdate]
zz_verify_c1_probe_test.go:170: PROBE event[4] Interceptor.Close                  id=1 filterRefCnt=2 stack=[server.(*interceptorList).Close <- server.(*usableRouteConfiguration).stop <- server.(*filterChain).applyConfiguration <- server.(*filterChain).updateUsableRouteConfiguration <- server.(*listenerWrapper).handleRDSUpdate]
zz_verify_c1_probe_test.go:170: PROBE event[5] --- handleRDSUpdate(gen2) returned --- id=0 filterRefCnt=1 stack=[]
zz_verify_c1_probe_test.go:184: PROBE RESULT ordinary-replacement: filterRefCnt observed inside retired interceptor Close = 2 (2 => retired reference still held; 1 => retired reference already released)
```

All-routes-disabled subtest (filter no longer referenced by the replacement) on 00c5530a, then control:

```console
zz_verify_c1_probe_test.go:170: PROBE event[2] --- gen1 active; in-place replacement starts (override=map[probe:{}]) --- id=0 filterRefCnt=1 stack=[]
zz_verify_c1_probe_test.go:170: PROBE event[3] ServerFilter.Close(destroyed)      id=0 filterRefCnt=0 stack=[server.(*refCountedServerFilter).Close <- server.(*filterChain).constructUsableRouteConfiguration <- server.(*listenerWrapper).handleRDSUpdate]
zz_verify_c1_probe_test.go:170: PROBE event[4] Interceptor.Close                  id=1 filterRefCnt=0 stack=[server.(*interceptorList).Close <- server.(*listenerWrapper).handleRDSUpdate.(*usableRouteConfiguration).close.func1 <- server.(*usableRouteConfiguration).close <- server.(*listenerWrapper).handleRDSUpdate]
zz_verify_c1_probe_test.go:170: PROBE event[5] --- handleRDSUpdate(gen2) returned --- id=0 filterRefCnt=0 stack=[]
zz_verify_c1_probe_test.go:209: PROBE RESULT all-disabled: FILTER DESTROYED BEFORE retired interceptor closed (event 3 < event 4)
```

```console
zz_verify_c1_probe_test.go:170: PROBE event[3] Interceptor.Close                  id=1 filterRefCnt=1 stack=[server.(*interceptorList).Close <- server.(*usableRouteConfiguration).stop <- server.(*filterChain).applyConfiguration <- server.(*filterChain).updateUsableRouteConfiguration <- server.(*listenerWrapper).handleRDSUpdate]
zz_verify_c1_probe_test.go:170: PROBE event[4] ServerFilter.Close(destroyed)      id=0 filterRefCnt=0 stack=[server.(*refCountedServerFilter).Close <- server.(*filterChain).applyConfiguration <- server.(*filterChain).updateUsableRouteConfiguration <- server.(*listenerWrapper).handleRDSUpdate]
zz_verify_c1_probe_test.go:170: PROBE event[5] --- handleRDSUpdate(gen2) returned --- id=0 filterRefCnt=0 stack=[]
zz_verify_c1_probe_test.go:211: PROBE RESULT all-disabled: filter destroyed AFTER retired interceptor closed (event 4 > event 3)
```

Per-branch result lines (verbatim `PROBE RESULT` lines from `verify/logs/c1_<branch>.log`; `release in` is the production frame directly above `refCountedServerFilter.Close` in the logged stack, `retired close via` the frames above `interceptorList.Close`):

| branch | HEAD | refCnt seen inside retired interceptor `Close` (2 = still held, 1 = already released) | all-routes-disabled order | release in | retired close via | go test |
|---|---|---|---|---|---|---|
| 00c5530a | bd043022 | 1 | FILTER DESTROYED BEFORE retired interceptor closed (event 3 < event 4) | `(*filterChain).constructUsableRouteConfiguration` | `(*listenerWrapper).handleRDSUpdate.(*usableRouteConfiguration).close.func1 <- (*usableRouteConfiguration).close` | ok   |
| 13d57ae3 | 30174e28 | 1 | FILTER DESTROYED BEFORE retired interceptor closed (event 3 < event 4) | `(*filterChain).constructUsableRouteConfiguration` | `(*usableRouteConfiguration).closeInterceptors <- (*filterChain).updateUsableRouteConfiguration` | ok   |
| 379dc377 | 422f3382 | 1 | FILTER DESTROYED BEFORE retired interceptor closed (event 3 < event 4) | `(*filterChain).constructUsableRouteConfiguration` | `(*usableRouteConfiguration).close <- (*filterChain).updateUsableRouteConfiguration` | ok   |
| de9096e7 | 20a2842d | 1 | FILTER DESTROYED BEFORE retired interceptor closed (event 3 < event 4) | `(*filterChain).constructUsableRouteConfiguration` | `(*usableRouteConfiguration).close <- (*filterChain).updateUsableRouteConfiguration` | ok   |
| fea61a52 | 915c769c | 1 | FILTER DESTROYED BEFORE retired interceptor closed (event 3 < event 4) | `(*filterChain).constructUsableRouteConfiguration` | `(*usableRouteConfiguration).close <- (*filterChain).storeUsableRouteConfiguration` | ok   |
| 941a85ec | 1941a65a | 1 | FILTER DESTROYED BEFORE retired interceptor closed (event 3 < event 4) | `(*filterChain).constructUsableRouteConfiguration` | `(*usableRouteConfiguration).close <- (*filterChain).updateUsableRouteConfiguration` | ok   |
| 2e29516a | 5f8a4c40 | 1 | FILTER DESTROYED BEFORE retired interceptor closed (event 3 < event 4) | `(*filterChain).constructUsableRouteConfiguration` | `(*usableRouteConfiguration).close <- (*filterChain).updateUsableRouteConfiguration` | ok   |
| 5e2ca2e9 | 02d467ea | 1 | FILTER DESTROYED BEFORE retired interceptor closed (event 3 < event 4) | `(*filterChain).constructUsableRouteConfiguration` | `(*usableRouteConfiguration).close <- (*filterChain).storeUsableRouteConfiguration` | ok   |
| 96cbe156 | 73052814 | 1 | FILTER DESTROYED BEFORE retired interceptor closed (event 3 < event 4) | `(*filterChain).constructUsableRouteConfiguration` | `(*usableRouteConfiguration).close <- (*filterChain).updateUsableRouteConfiguration` | ok   |
| e422b156 | 432ec458 | 1 | FILTER DESTROYED BEFORE retired interceptor closed (event 3 < event 4) | `(*filterChain).constructUsableRouteConfiguration` | `(*usableRouteConfiguration).close` | ok   |
| 8755f744 | bbaa67dc | 1 | FILTER DESTROYED BEFORE retired interceptor closed (event 3 < event 4) | `(*filterChain).constructUsableRouteConfiguration` | `(*virtualHostWithInterceptors).closeInterceptors <- (*listenerWrapper).handleRDSUpdate.(*usableRouteConfiguration).closeInterceptors.func1 <- (*usableRouteConfiguration).closeInterceptors` | ok   |
| 8a4cfab2 | 55c52288 | 1 | FILTER DESTROYED BEFORE retired interceptor closed (event 3 < event 4) | `(*filterChain).constructUsableRouteConfiguration` | `(*virtualHostWithInterceptors).closeInterceptors <- (*usableRouteConfiguration).closeInterceptors <- (*usableRouteConfiguration).retire <- (*filterChain).updateRouteConfiguration` | ok   |
| ae7b032e | 124222ff | 1 | FILTER DESTROYED BEFORE retired interceptor closed (event 3 < event 4) | `(*filterChain).constructUsableRouteConfiguration` | `(*usableRouteConfiguration).close` | ok   |
| beb780c9 | 4034f809 | 1 | FILTER DESTROYED BEFORE retired interceptor closed (event 3 < event 4) | `(*filterChain).constructUsableRouteConfiguration` | `(*usableRouteConfiguration).close <- (*filterChain).updateRouteConfiguration` | ok   |
| 720be0d6 | 7a293e2b | 1 | FILTER DESTROYED BEFORE retired interceptor closed (event 3 < event 4) | `(*filterChain).constructUsableRouteConfiguration` | `(*usableRouteConfiguration).close` | ok   |
| audited branch (control) | 614cb739 | 2 | filter destroyed AFTER retired interceptor closed (event 4 > event 3) | `(*filterChain).applyConfiguration` | `(*usableRouteConfiguration).stop <- (*filterChain).applyConfiguration <- (*filterChain).updateUsableRouteConfiguration` | ok   |

**Observed on all 15 branches:** the retired configuration's filter reference is released from `constructUsableRouteConfiguration` (on 2e29516a reached via `usableRouteConfigurationLocked`), before the retired interceptors are closed by the branch's added retirement step; the control keeps the reference (refCnt 2) until the retired interceptor has been closed. No branch showed the refuting behaviour.

**Impact reasoning.** Base `4ee6ac46` already dropped the old `fc.serverFilters` at the end of `constructUsableRouteConfiguration` (`git show 4ee6ac46:internal/xds/server/filter_chain_manager.go`, lines 420-423), but it never closed retired interceptors, so the order did not matter. Each of these branches adds "close the retired configuration's interceptors" *after* construction, so for the time between the release and the close the retired interceptors are open without the reference that is supposed to keep their parent `ServerFilter` alive (`httpfilter.ServerFilter.Close`: "Close is called when the filter is no longer needed"). When the replacement still uses the filter, the new configuration's reference keeps the filter alive, so nothing is destroyed early (refCnt 1 above). When the replacement stops using the filter (all routes disable it, or the routes that used it are gone) the count reaches zero and the parent filter's `Close` runs while the dependent interceptor is still open — observed on all 15 branches (`FILTER DESTROYED BEFORE retired interceptor closed`), and end-to-end through a real xDS server in the C5 section. A filter whose interceptors use parent-owned state in `Close` (or in RPCs still running on the retired configuration) would touch that state after the parent released it. No in-tree server filter was observed to misbehave; the probe filter only records the order. The archived eval fixture is green on the three in-place-update checks on all 15 branches (table in Setup), so it does not detect this ordering.

## C2

**Claim:** the added or changed lifecycle tests omit ordering guarantees needed for lifecycle assertions or failure-safe resource teardown. Parts: `retirement_assertion_ordering`, `listener_cleanup_ordering`, `creation_assertion_ordering`. Adjudicated independently on 16 branches.

### Unmodified tests: 300 runs per branch

```sh
for b in 820eccb4 3a746ce5 dbde929d d67b2e69 955d1b6e 1463306c 2aa192a0 272d6cf6 379dc377 18fa6ce5 fea61a52 941a85ec 2e29516a 5e2ca2e9 96cbe156 eb7df664; do
  $R/c2_stress_unmodified.sh ~/wt/$b > verify/logs/c2_stress_$b.log 2>&1   # go test -race -count=150 -cpu 1,4 -v -run '^Test$/^(<branch's added test/xds test>)$' ./test/xds/
done
```

Every branch (16 of 16) printed:

```console
subtest PASS lines: 300   subtest FAIL lines: 0
```

So on this machine the added tests did not fail once in 300 unmodified `-race` runs per branch. What follows shows that this is a property of timing, not of the tests' synchronization.

### retirement_assertion_ordering (and creation counts asserted at the same point)

`verify/repro/c2_delay_retired_close.sh <worktree> [delay]` runs the branch's own added `test/xds` lifecycle test twice: unmodified, and with one line of instrumentation — a `time.Sleep` at the top of the production `(*interceptorList).Close` (the single place interceptors are closed from). The sleep changes no behaviour: the retired interceptors are still closed, exactly once, 300 ms later. A test that waits for closure stays green; a test that asserts counts right after seeing an RPC served by the replacement configuration fails.

```sh
for b in <the 16 branches above>; do $R/c2_delay_retired_close.sh ~/wt/$b 300ms > verify/logs/c2_delay_$b.log 2>&1; done
$R/c2_delay_retired_close.sh ~/repos/grpc-go 300ms > verify/logs/c2_delay_CONTROL_perfect.log 2>&1   # control
```

Full output on [evalon/grpc-go-xd-820eccb4](https://github.com/kaitranntt-evals/grpc-go-xds-rds-interceptor-lifecycle-leak/tree/evalon/grpc-go-xd-820eccb4):

```console
### branch HEAD: b3c4818e xds/server: close interceptors of replaced route configurations
### added test/xds tests: ServerSideXDS_FilterStateRetention_InPlaceRDSUpdate
### [unmodified] go test -race -count=1 -v -run '^Test$/^(ServerSideXDS_FilterStateRetention_InPlaceRDSUpdate)$' ./test/xds/
--- PASS: Test (0.07s)
    --- PASS: Test/ServerSideXDS_FilterStateRetention_InPlaceRDSUpdate (0.07s)
PASS
ok  	google.golang.org/grpc/test/xds	1.095s
### instrumentation:
@@ -543,2 +543,3 @@ func (il *interceptorList) AllowRPC(ctx context.Context) error {
+	verifyC2Delay()
### [close delayed by 300ms] VERIFY_C2_CLOSE_DELAY=300ms go test -race -count=1 -v -run '^Test$/^(ServerSideXDS_FilterStateRetention_InPlaceRDSUpdate)$' ./test/xds/
    xds_server_filter_state_retention_test.go:815: After update 1: destroyed 0 interceptor instances, want: 1
--- FAIL: Test (0.63s)
    --- FAIL: Test/ServerSideXDS_FilterStateRetention_InPlaceRDSUpdate (0.63s)
FAIL
FAIL	google.golang.org/grpc/test/xds	0.668s
FAIL
### restored; git status --short: []
```

Per-branch outcome (verbatim failing assertion from `verify/logs/c2_delay_<branch>.log`):

| branch | HEAD | added test (`Test/...`) | unmodified | close delayed 300 ms | failing assertion under delay |
|---|---|---|---|---|---|
| 820eccb4 | b3c4818e | `ServerSideXDS_FilterStateRetention_InPlaceRDSUpdate` | PASS | FAIL | `xds_server_filter_state_retention_test.go:815: After update 1: destroyed 0 interceptor instances, want: 1` |
| 3a746ce5 | a9907225 | `ServerSideXDS_FilterStateRetention_AcrossInPlaceRDSUpdates` | PASS | FAIL | `xds_server_filter_state_retention_test.go:829: After in-place RDS update 1: 3 interceptor instances open (created: 3, destroyed: 0), want 2` |
| dbde929d | 87ece900 | `ServerSideXDS_InPlaceRDSUpdate_ClosesReplacedInterceptors` | PASS | FAIL | `xds_server_filter_state_retention_test.go:790: After update 1: destroyed 0 interceptor instances, want: 1` |
| d67b2e69 | d7426c1e | `ServerSideXDS_FilterStateRetention_InPlaceRouteConfigUpdate` | PASS | FAIL | `xds_server_filter_state_retention_test.go:785: After 1 route configuration updates, 2 interceptor instances are open (created: 2, destroyed: 0), want 1` |
| 955d1b6e | 7af9afed | `ServerSideXDS_InPlaceRDSUpdate_ClosesReplacedInterceptors` | PASS | FAIL | `xds_server_filter_state_retention_test.go:817: After update 2: destroyed 0 interceptor instances, want: 1` |
| 1463306c | 4d2584c8 | `ServerSideXDS_FilterStateRetention_InPlaceRDSUpdate` | PASS | FAIL | `xds_server_filter_state_retention_test.go:811: After update 1: destroyed 0 interceptor instances, want: 1` |
| 2aa192a0 | 6d526791 | `ServerSideXDS_FilterStateRetention_AcrossUpdates_RouteConfigChange` | PASS | FAIL | `xds_server_filter_state_retention_test.go:818: Destroyed 0 interceptor instances, want: 1` |
| 272d6cf6 | 8992ab4d | `ServerSideXDS_FilterStateRetention_AcrossUpdates_RouteConfigChange` | PASS | FAIL | `xds_server_filter_state_retention_test.go:822: Destroyed 0 interceptor instances, want: 1` |
| 379dc377 | 422f3382 | `ServerSideXDS_RouteConfigUpdate_ClosesRetiredInterceptors` | PASS | FAIL | `xds_server_rds_interceptor_cleanup_test.go:254: Destroyed 0 interceptor instances after 1 route configuration updates, want: 1` |
| 18fa6ce5 | 39804296 | `ServerSideXDS_FilterStateRetention_AcrossUpdates_RouteConfigChange` | PASS | FAIL | `xds_server_filter_state_retention_test.go:844: Created 3 interceptor instances after 1 in-place RDS update(s), want: 4` |
| fea61a52 | 915c769c | `ServerSideXDS_FilterStateRetention_AcrossUpdates_RouteConfigChange` | PASS | FAIL | `xds_server_filter_state_retention_test.go:835: Destroyed 0 interceptor instances, want: 1` |
| 941a85ec | 1941a65a | `ServerSideXDS_FilterStateRetention_AcrossUpdates_RouteConfigChange` | PASS | FAIL | `xds_server_filter_state_retention_test.go:813: Destroyed 0 interceptor instances, want: 1` |
| 2e29516a | 5f8a4c40 | `ServerSideXDS_FilterStateRetention_AcrossUpdates_RouteConfigChange` | PASS | FAIL | `xds_server_filter_state_retention_test.go:856: Created 3 interceptor instances, want: 4` |
| 5e2ca2e9 | 02d467ea | `ServerSideXDS_FilterStateRetention_AcrossUpdates_RouteConfigChange` | PASS | FAIL | `xds_server_filter_state_retention_test.go:814: Destroyed 0 interceptor instances, want: 1` |
| 96cbe156 | 73052814 | `ServerSideXDS_FilterStateRetention_AcrossUpdates_RouteConfigChange` | PASS | FAIL | `xds_server_filter_state_retention_test.go:815: Destroyed 0 interceptor instances, want: 1` |
| eb7df664 | e97654c8 | `ServerSideXDS_FilterStateRetention_AcrossUpdates_RDS` | PASS | FAIL | `xds_server_filter_state_retention_test.go:731: Created 5 interceptors, want 6` |
| audited branch (control) | 614cb739 | `ServerSideXDS_InterceptorLeak_RDSUpdate` | PASS | PASS | (none — test stays green) |

On 13 branches the first assertion to fail is the destruction count (`destroyed 0 ... want 1`, or open = created − destroyed): the test has observed an RPC served by the replacement configuration, but the retired interceptor has not been closed yet, and nothing in the test waits for it. On 18fa6ce5 and 2e29516a (two filter chains) the first failing assertion is the creation count, because the second filter chain's replacement is only built after the first chain's delayed close; on eb7df664 the destruction count *is* polled, see below. The control's regression test stays green under the same delay (1.89 s instead of 0.03 s), i.e. it waits for the closure it asserts.

Counters at the instant replacement traffic is observed, [evalon/grpc-go-xd-18fa6ce5](https://github.com/kaitranntt-evals/grpc-go-xds-rds-interceptor-lifecycle-leak/tree/evalon/grpc-go-xd-18fa6ce5) (`$R/c2_counters_after_traffic_18fa6ce5.sh ~/wt/18fa6ce5`; adds one `t.Logf` immediately before the test's own count assertions, plus the same close delay):

```console
### assertions that follow the replacement-traffic loop (test/xds/xds_server_filter_state_retention_test.go):
		if got, want := filtersCreated.Load(), int32(1); got != want {
			t.Fatalf("Created %d filter instances after %d in-place RDS update(s), want: %d", got, i, want)
		}
		if got, want := filtersDestroyed.Load(), int32(0); got != want {
			t.Fatalf("Destroyed %d filter instances after %d in-place RDS update(s), want: %d", got, i, want)
		}
		if got, want := interceptorsCreated.Load(), int32(2*(i+1)); got != want {
			t.Fatalf("Created %d interceptor instances after %d in-place RDS update(s), want: %d", got, i, want)
		}
		if got, want := interceptorsDestroyed.Load(), int32(2*i); got != want {
			t.Fatalf("Destroyed %d interceptor instances after %d in-place RDS update(s), want: %d", got, i, want)
		}
	}
### VERIFY_C2_CLOSE_DELAY=0s go test -race -count=1 -v -run '^Test$/^ServerSideXDS_FilterStateRetention_AcrossUpdates_RouteConfigChange$' ./test/xds/
    xds_server_filter_state_retention_test.go:837: PROBE replacement traffic observed for update 1: interceptorsCreated=4 (asserted next: 4) interceptorsDestroyed=2 (asserted next: 2)
    xds_server_filter_state_retention_test.go:837: PROBE replacement traffic observed for update 2: interceptorsCreated=6 (asserted next: 6) interceptorsDestroyed=4 (asserted next: 4)
    xds_server_filter_state_retention_test.go:837: PROBE replacement traffic observed for update 3: interceptorsCreated=8 (asserted next: 8) interceptorsDestroyed=6 (asserted next: 6)
--- PASS: Test (0.07s)
    --- PASS: Test/ServerSideXDS_FilterStateRetention_AcrossUpdates_RouteConfigChange (0.07s)
PASS
ok  	google.golang.org/grpc/test/xds	1.100s
### VERIFY_C2_CLOSE_DELAY=300ms go test -race -count=1 -v -run '^Test$/^ServerSideXDS_FilterStateRetention_AcrossUpdates_RouteConfigChange$' ./test/xds/
    xds_server_filter_state_retention_test.go:837: PROBE replacement traffic observed for update 1: interceptorsCreated=3 (asserted next: 4) interceptorsDestroyed=0 (asserted next: 2)
    xds_server_filter_state_retention_test.go:845: Created 3 interceptor instances after 1 in-place RDS update(s), want: 4
--- FAIL: Test (1.24s)
    --- FAIL: Test/ServerSideXDS_FilterStateRetention_AcrossUpdates_RouteConfigChange (1.23s)
FAIL
FAIL	google.golang.org/grpc/test/xds	1.263s
FAIL
### restored; git status --short: []
```

### creation_assertion_ordering

[evalon/grpc-go-xd-eb7df664](https://github.com/kaitranntt-evals/grpc-go-xds-rds-interceptor-lifecycle-leak/tree/evalon/grpc-go-xd-eb7df664) retires the old configuration before building the replacement, per filter chain (`updateRouteConfiguration`: `fc.usableRouteConfiguration.close()` then `fc.constructUsableRouteConfiguration(...)`; this is also why the fixture's `InterceptorSwapOrder` check fails on this branch). Its test polls `interceptorsDestroyed` and then immediately asserts `interceptorsCreated`. `$R/c2_creation_ordering_eb7df664.sh ~/wt/eb7df664` logs both counters right after the destruction poll and inserts a scheduling delay at the top of the production `constructUsableRouteConfiguration` (between "old closed" and "replacement created"):

```console
### branch HEAD: e97654c8 xds/server: close interceptors when replacing route configurations
### test code under probe (test/xds/xds_server_filter_state_retention_test.go):
		// The second filter chain may still be updating when the RPC completes.
		wantClosed := int32(update * len(inboundLis.FilterChains))
		for interceptorsDestroyed.Load() != wantClosed && ctx.Err() == nil {
			time.Sleep(time.Millisecond)
		}
		if got := interceptorsDestroyed.Load(); got != wantClosed {
			t.Fatalf("After RDS update %d, closed %d interceptors, want %d", update, got, wantClosed)
		}
		if got, want := interceptorsCreated.Load(), int32((update+1)*len(inboundLis.FilterChains)); got != want {
			t.Fatalf("Created %d interceptors, want %d", got, want)
		}
### instrumentation:
@@ -428,0 +429 @@ func (fc *filterChain) constructUsableRouteConfiguration(config xdsresource.Rout
+	verifyC2Delay()
@@ -729,0 +730 @@ func (s) TestServerSideXDS_FilterStateRetention_AcrossUpdates_RDS(t *testing.T)
+		t.Logf("PROBE destruction poll finished (update %d): interceptorsDestroyed=%d interceptorsCreated=%d (asserted next: %d)", update, interceptorsDestroyed.Load(), interceptorsCreated.Load(), (update+1)*len(inboundLis.FilterChains))
### [no delay] go test -race -count=1 -v -run '^Test$/^ServerSideXDS_FilterStateRetention_AcrossUpdates_RDS$' ./test/xds/
    xds_server_filter_state_retention_test.go:730: PROBE destruction poll finished (update 1): interceptorsDestroyed=2 interceptorsCreated=4 (asserted next: 4)
    xds_server_filter_state_retention_test.go:730: PROBE destruction poll finished (update 2): interceptorsDestroyed=4 interceptorsCreated=6 (asserted next: 6)
    xds_server_filter_state_retention_test.go:730: PROBE destruction poll finished (update 3): interceptorsDestroyed=6 interceptorsCreated=8 (asserted next: 8)
--- PASS: Test (0.04s)
    --- PASS: Test/ServerSideXDS_FilterStateRetention_AcrossUpdates_RDS (0.03s)
PASS
ok  	google.golang.org/grpc/test/xds	1.064s
### [replacement construction delayed by 300ms] VERIFY_C2_CREATE_DELAY=300ms go test -race -count=1 -v -run '^Test$/^ServerSideXDS_FilterStateRetention_AcrossUpdates_RDS$' ./test/xds/
    xds_server_filter_state_retention_test.go:730: PROBE destruction poll finished (update 1): interceptorsDestroyed=2 interceptorsCreated=3 (asserted next: 4)
    xds_server_filter_state_retention_test.go:732: Created 3 interceptors, want 4
--- FAIL: Test (1.23s)
    --- FAIL: Test/ServerSideXDS_FilterStateRetention_AcrossUpdates_RDS (1.22s)
FAIL
FAIL	google.golang.org/grpc/test/xds	1.268s
FAIL
### restored; git status --short: []
```

The destruction poll completes with `interceptorsDestroyed=2` while `interceptorsCreated=3`; the test then asserts 4. The destruction condition does not guarantee the asserted creation state. (With only the close delay from the previous subsection the same assertion fails at update 2: `Created 5 interceptors, want 6`.) The same kind of unsynchronized creation assertion is what fails first on 18fa6ce5 and 2e29516a in the table above.

### listener_cleanup_ordering

[evalon/grpc-go-xd-2e29516a](https://github.com/kaitranntt-evals/grpc-go-xds-rds-interceptor-lifecycle-leak/tree/evalon/grpc-go-xd-2e29516a), `TestListenerWrapper_RDSUpdateClosesRetiredInterceptors` (added file `internal/xds/server/listener_wrapper_test.go`): the listener is created at line 348, the setup helpers `newFakeXDSClient(t)` and `serverListenerUpdate(t, ...)` (both contain `t.Fatalf`) are called at lines 352-353, and the only cleanup, `defer lw.Close()`, is registered at line 364. `$R/c2_listener_cleanup_2e29516a.sh ~/wt/2e29516a` adds a `t.Cleanup` observer that dials the listener after the test body and all defers have run, and runs the test as written and with `serverListenerUpdate` forced to `t.Fatalf` (test files restored afterwards):

```console
### control flow between listener creation and cleanup registration (internal/xds/server/listener_wrapper_test.go):
342-	defer cancel()
346-	defer httpfilter.UnregisterForTesting(fb.typeURL)
348-	lis, err := testutils.LocalTCPListener()
350-		t.Fatalf("testutils.LocalTCPListener() failed: %v", err)
352-	xdsC := newFakeXDSClient(t)
353-	lisName, lisUpdate := serverListenerUpdate(t, lis, xdsC.bc, fb.typeURL)
364-	defer lw.Close()
### fatal exits inside the helpers called in that span:
newFakeXDSClient: 11:		t.Fatalf("bootstrap.NewConfigFromContents() failed: %v", err)
serverListenerUpdate: 5:		t.Fatalf("net.SplitHostPort(%q) failed: %v", lis.Addr().String(), err)
serverListenerUpdate: 9:		t.Fatalf("strconv.Atoi(%q) failed: %v", portStr, err)
serverListenerUpdate: 47:		t.Fatalf("anypb.New() failed: %v", err)
serverListenerUpdate: 51:		t.Fatalf("Failed to decode listener resource: %v", err)
serverListenerUpdate: 55:		t.Fatalf("Decoded listener resource is not a valid server-side listener: %T", res.Resource)
### instrumentation (test file only):
+	if verifyC2InjectFatal {
+		t.Fatalf("verify: injected setup-helper failure")
+	}
+	verifyC2WatchListener(t, lis)
### [helper succeeds] go test -race -count=1 -v -run '^Test$/^ListenerWrapper_RDSUpdateClosesRetiredInterceptors$' ./internal/xds/server/
    zz_verify_c2_lis_probe_test.go:18: PROBE after test exit: listener is closed (dial error: dial tcp 127.0.0.1:36871: connect: connection refused)
--- PASS: Test (0.01s)
    --- PASS: Test/ListenerWrapper_RDSUpdateClosesRetiredInterceptors (0.01s)
PASS
ok  	google.golang.org/grpc/internal/xds/server	1.033s
### [helper fatals]   VERIFY_C2_INJECT_FATAL=1 go test -race -count=1 -v -run '^Test$/^ListenerWrapper_RDSUpdateClosesRetiredInterceptors$' ./internal/xds/server/
    listener_wrapper_test.go:357: verify: injected setup-helper failure
    zz_verify_c2_lis_probe_test.go:22: PROBE after test exit: listener 127.0.0.1:34665 is STILL OPEN (dial succeeded; nothing closed it)
--- FAIL: Test (0.00s)
    --- FAIL: Test/ListenerWrapper_RDSUpdateClosesRetiredInterceptors (0.00s)
FAIL
FAIL	google.golang.org/grpc/internal/xds/server	0.025s
FAIL
### restored; git status --short: []
```

After the fatal setup exit the TCP listener still accepts connections; no other owner closed it.

**Impact reasoning.** These are test-quality problems, not product behaviour. (1) Retirement/creation ordering: the assertions hold only because, in each branch's current implementation, the retired interceptors happen to be closed within microseconds of the replacement being published, on the xDS callback goroutine. Nothing in the tests waits for it. The tests did not flake in 300 unmodified runs per branch, so the practical exposure today is a scheduling stall between "replacement published" and "retired interceptors closed" on a loaded machine; but the tests would also reject a correct implementation that closes retired interceptors asynchronously (e.g. after in-flight RPCs drain) — a 300 ms delay with otherwise identical behaviour fails all 16. The control shows the fix: poll the destruction (and creation) counters with the test deadline before asserting. (2) Listener cleanup on 2e29516a: the listener leaks only when a setup helper fails, which with the static inputs used here should not happen; when it does, a bound TCP port stays open for the rest of the test binary. Fix: `t.Cleanup(func() { lis.Close() })` (or register `lw.Close` before calling the helpers).

## C3

**Claim:** Server Stop deadlocks when an active AllowRPC call waits for RPC-context cancellation because listener retirement waits for that call before shutdown cancels transports. Parts: `retirement_locking`, `shutdown_trigger`. Target: [evalon/grpc-go-xd-e6df146d](https://github.com/kaitranntt-evals/grpc-go-xds-rds-interceptor-lifecycle-leak/tree/evalon/grpc-go-xd-e6df146d) (HEAD d6d7ef67).

**Probe:** `verify/repro/c3_stop_blocked_allowrpc_e2e_test.go` — a real xDS-enabled gRPC server (management server, real client, `xds.NewGRPCServer`) with an HTTP filter whose interceptor blocks in `AllowRPC` until the RPC context is done. One RPC (no client deadline) is parked in `AllowRPC`, then `Stop()` is called. The probe waits 5 s for `Stop` to return unaided, dumps the two goroutines, and only then cancels the RPC from the client. No production code is modified.

```sh
cd ~/wt/e6df146d && cp $R/c3_stop_blocked_allowrpc_e2e_test.go test/xds/zz_verify_c3_e2e_test.go &&
  go test -race -count=1 -v -timeout 120s -run 'Test/VerifyC3' ./test/xds/ > ~/repos/grpc-go/verify/logs/c3_e6df146d.log 2>&1; rm test/xds/zz_verify_c3_e2e_test.go
# control: same in ~/repos/grpc-go (audited branch) -> verify/logs/c3_CONTROL_perfect.log
```

Target output (PROBE lines and the goroutine dumps, gRPC log lines removed):

```console
    zz_verify_c3_e2e_test.go:218: PROBE warm-up RPC OK; arming AllowRPC to wait for RPC-context cancellation
    zz_verify_c3_e2e_test.go:235: PROBE RPC is parked in AllowRPC (waiting on <-ctx.Done())
    zz_verify_c3_e2e_test.go:243: PROBE Stop() called; giving it 5s to return with no help
    zz_verify_c3_e2e_test.go:261: PROBE RESULT Stop() has NOT returned 5s after being called; server-side RPC ctx.Err()=<nil> (nil => transport cancellation has not happened)
    zz_verify_c3_e2e_test.go:263: PROBE goroutine:
        goroutine 46 [chan receive]:
            google.golang.org/grpc/test/xds_test.(*verifyC3Interceptor).AllowRPC
            google.golang.org/grpc/internal/xds/server.(*interceptorList).AllowRPC
            google.golang.org/grpc/internal/xds/server.RouteAndProcess
            google.golang.org/grpc/xds.xdsUnaryInterceptor
            google.golang.org/grpc/interop/grpc_testing._TestService_EmptyCall_Handler
            google.golang.org/grpc.(*Server).processUnaryRPC
            google.golang.org/grpc.(*Server).handleStream
            google.golang.org/grpc.(*Server).serveStreams.func2.1
    zz_verify_c3_e2e_test.go:263: PROBE goroutine:
        goroutine 47 [sync.RWMutex.Lock]:
            sync.runtime_SemacquireRWMutex
            sync.(*RWMutex).Lock
            google.golang.org/grpc/internal/xds/server.(*usableRouteConfiguration).stop
            google.golang.org/grpc/internal/xds/server.(*filterChain).updateRouteConfiguration
            google.golang.org/grpc/internal/xds/server.(*filterChain).stop
            google.golang.org/grpc/internal/xds/server.(*filterChainManager).stop
            google.golang.org/grpc/internal/xds/server.(*listenerWrapper).Close
            google.golang.org/grpc.(*listenSocket).Close
            google.golang.org/grpc.(*Server).closeListenersLocked
            google.golang.org/grpc.(*Server).stop
            google.golang.org/grpc.(*Server).Stop
            google.golang.org/grpc/xds.(*GRPCServer).Stop
            google.golang.org/grpc/test/xds_test.setupGRPCServer.func4
            google.golang.org/grpc/test/xds_test.s.TestVerifyC3_StopWhileAllowRPCWaitsForContext.func3
    zz_verify_c3_e2e_test.go:266: PROBE now releasing AllowRPC manually by cancelling the RPC from the client
    zz_verify_c3_e2e_test.go:271: PROBE Stop() returned 1ms after the manual release (5.023s after it was called)
--- PASS: Test/VerifyC3_StopWhileAllowRPCWaitsForContext (5.05s)
ok  	google.golang.org/grpc/test/xds	6.079s
```

Control output:

```console
    zz_verify_c3_e2e_test.go:218: PROBE warm-up RPC OK; arming AllowRPC to wait for RPC-context cancellation
    zz_verify_c3_e2e_test.go:235: PROBE RPC is parked in AllowRPC (waiting on <-ctx.Done())
    zz_verify_c3_e2e_test.go:243: PROBE Stop() called; giving it 5s to return with no help
    zz_verify_c3_e2e_test.go:247: PROBE RESULT Stop() returned on its own after 1ms (AllowRPC was not released by the probe)
    zz_verify_c3_e2e_test.go:250: PROBE parked RPC finished with: rpc error: code = Unavailable desc = error reading from server: EOF
```

Source on the target for the two parts (`internal/xds/server/routing.go`, `filter_chain_manager.go`, `server.go`):

```console
$ grep -n "AllowRPC\|RUnlock\|RLock" internal/xds/server/routing.go
49:		rc.mu.RLock()
53:		rc.mu.RUnlock()
57:	defer rc.mu.RUnlock()
106:	if err := rwi.interceptor.AllowRPC(ctx); err != nil {
$ sed -n 186,191p internal/xds/server/filter_chain_manager.go
func (rc *usableRouteConfiguration) stop() {
	if rc == nil {
		return
	}
	rc.mu.Lock()
	defer rc.mu.Unlock()
$ sed -n '1951,1952p;1964p' server.go
	s.mu.Lock()
	s.closeListenersLocked()
		s.closeServerTransportsLocked()
```

- *retirement_locking* — held: the dump shows the RPC goroutine in `RouteAndProcess -> interceptorList.AllowRPC` (the read lock taken at routing.go:49 is released only by the `defer` at line 57) and the `Stop` goroutine in `sync.(*RWMutex).Lock` inside `usableRouteConfiguration.stop`.
- *shutdown_trigger* — held: `Stop` is blocked in `Server.stop -> closeListenersLocked -> listenerWrapper.Close -> filterChainManager.stop -> filterChain.stop -> updateRouteConfiguration -> usableRouteConfiguration.stop`, which runs before `closeServerTransportsLocked`; 5 s after `Stop()` the server-side RPC context is still not cancelled (`ctx.Err()=<nil>`). `Stop` returned 1 ms after the probe cancelled the RPC from the client. On the control `Stop()` returned by itself after 1 ms and the parked RPC ended with `Unavailable`.

**Impact reasoning.** `Server.Stop()` is documented to close all connections and cancel pending RPCs; on this branch it instead waits, while holding `Server.mu`, for every RPC that is inside a server HTTP filter's `AllowRPC`. In the probe the RPC has no deadline; nothing on the server side ended it within the 5 s bound, and `Stop` returned only once the client cancelled the RPC. The trigger is an RPC that is inside `AllowRPC` when `Stop` is called and stays there (the probe's filter waits on the RPC context; a filter whose `AllowRPC` returns promptly only delays `Stop` by that call). The task statement requires that "server shutdown should remain safe". The eval fixture is 5/5 green on this branch, so it does not exercise this. The only release observed was ending the RPC from the client side.

## C4

**Claim:** an added test in `internal/xds/server/listener_wrapper_test.go` passes an unbounded `context.Background()` directly to `transport.SetConnection` instead of a timeout or cancellation context. Target: [evalon/grpc-go-xd-beb780c9](https://github.com/kaitranntt-evals/grpc-go-xds-rds-interceptor-lifecycle-leak/tree/evalon/grpc-go-xd-beb780c9) (HEAD 4034f809). This claim is about what a test file contains; evidence is the text plus demonstration runs.

```sh
$R/c4_vet_timeout_context_check.sh ~/wt/beb780c9 > verify/logs/c4_beb780c9.log 2>&1    # read-only
```

```console
$ git grep -n "SetConnection(" -- internal/xds/server/listener_wrapper_test.go
internal/xds/server/listener_wrapper_test.go:111:	ctx := transport.SetConnection(context.Background(), &connWrapper{urc: fc.usableRouteConfiguration})
$ git diff 4ee6ac46 HEAD -- internal/xds/server/listener_wrapper_test.go | grep -n "SetConnection\|WithTimeout\|WithCancel\|^+func (s)"
81:+func (s) TestListenerWrapper_RDSUpdateErrorAndClose(t *testing.T) {
117:+	ctx := transport.SetConnection(context.Background(), &connWrapper{urc: fc.usableRouteConfiguration})
$ sed -n 80p scripts/vet.sh
git grep -e 'context.Background()' --or -e 'context.TODO()' -- "*_test.go" | grep -v "benchmark/primitives/context_test.go" | grep -v 'context.WithTimeout(' | not grep -v 'context.WithCancel('
$ the same pipeline without the leading "not", on the branch working tree:
internal/xds/server/listener_wrapper_test.go:	ctx := transport.SetConnection(context.Background(), &connWrapper{urc: fc.usableRouteConfiguration})
pipeline exit=0 (0 => offending lines found => vet.sh's "not grep" fails)
$ the same pipeline at base 4ee6ac46:
pipeline exit=1 (1 => clean)
$ go test -race -count=1 -v -run "Test/ListenerWrapper_RDSUpdateErrorAndClose" ./internal/xds/server/
--- PASS: Test (0.00s)
    --- PASS: Test/ListenerWrapper_RDSUpdateErrorAndClose (0.00s)
PASS
ok  	google.golang.org/grpc/internal/xds/server	1.026s
```

The file is new on the branch (it does not exist at base `4ee6ac46`); the call is in the added `TestListenerWrapper_RDSUpdateErrorAndClose`, which creates no `context.WithTimeout`/`context.WithCancel` context at all (the diff grep above lists only the test function and the `SetConnection` line). The context is used as a value carrier for two `RouteAndProcess(ctx)` calls.

**Impact reasoning.** The repository's own static check (`scripts/vet.sh` line 80: test files must not use `context.Background()`/`context.TODO()` outside a `WithTimeout`/`WithCancel` call) matches this line on the branch and matches nothing at base, so `scripts/vet.sh` would fail at that step on this branch (I ran the check's pipeline, not the whole of `vet.sh`, which installs tools and is a CI entry point). The test itself passes in 0.00 s under `-race`; nothing in it blocks on the context, so there is no hang risk as written — the cost is a lint failure and a deviation from the repo's test convention (`context.WithTimeout(context.Background(), defaultTestTimeout)`).

## C5

**Claim:** replacing an active route configuration with routes that all disable a previously used filter destroys that filter before retired dependent interceptors finish closing. Parts: `reference_release_order`, `filter_destruction_trigger`. Target: [evalon/grpc-go-xd-00c5530a](https://github.com/kaitranntt-evals/grpc-go-xds-rds-interceptor-lifecycle-leak/tree/evalon/grpc-go-xd-00c5530a) (HEAD bd043022).

**Probe (live service):** `verify/repro/c5_filter_destroy_order_e2e_test.go` — a real xDS-enabled gRPC server and management server. Generation 1 uses an HTTP filter on its route and serves an RPC; then the same RouteConfiguration name is updated in place so that (a) the route disables the filter via `typed_per_filter_config` (`VerifyC5_AllRoutesDisableFilter`), or (b) the route using it is gone (`VerifyC5_ReplacementWithNoRoutes`). The probe logs `ServerFilter.Close` and each retired interceptor's `Close` with production call stacks. No production code is modified.

```sh
cd ~/wt/00c5530a && cp $R/c5_filter_destroy_order_e2e_test.go test/xds/zz_verify_c5_e2e_test.go &&
  go test -race -count=1 -v -run 'Test/VerifyC5' ./test/xds/ > ~/repos/grpc-go/verify/logs/c5_e2e_00c5530a.log 2>&1; rm test/xds/zz_verify_c5_e2e_test.go
# control: same in ~/repos/grpc-go (audited branch) -> verify/logs/c5_e2e_CONTROL_perfect.log
```

Target (both subtests print the same sequence; first shown, then both RESULT lines):

```console
zz_verify_c5_e2e_test.go:252: PROBE event[0] ServerFilter.Build
zz_verify_c5_e2e_test.go:252: PROBE event[1] Interceptor#1.Build
zz_verify_c5_e2e_test.go:252: PROBE event[2] --- generation 1 serving (RPC OK); sending in-place RDS replacement for "routeName" ---
zz_verify_c5_e2e_test.go:252: PROBE event[3] ServerFilter.Close (filter DESTROYED) stack=[server.(*refCountedServerFilter).Close <- server.(*filterChain).constructUsableRouteConfiguration <- server.(*listenerWrapper).handleRDSUpdate <- server.(*rdsWatcher).ResourceChanged]
zz_verify_c5_e2e_test.go:252: PROBE event[4] Interceptor#1.Close parentFilterAlreadyDestroyed=true stack=[server.(*interceptorList).Close <- server.(*listenerWrapper).handleRDSUpdate.(*usableRouteConfiguration).close.func1 <- server.(*usableRouteConfiguration).close <- server.(*listenerWrapper).handleRDSUpdate <- server.(*rdsWatcher).ResourceChanged]
zz_verify_c5_e2e_test.go:252: PROBE event[5] --- replacement observed; stopping server ---
zz_verify_c5_e2e_test.go:254: PROBE RESULT after in-place replacement: retired interceptors closed while parent filter still alive=0, closed AFTER parent filter was destroyed=1
zz_verify_c5_e2e_test.go:254: PROBE RESULT after in-place replacement: retired interceptors closed while parent filter still alive=0, closed AFTER parent filter was destroyed=1
--- PASS: Test/VerifyC5_AllRoutesDisableFilter (0.05s)
--- PASS: Test/VerifyC5_ReplacementWithNoRoutes (0.04s)
ok  	google.golang.org/grpc/test/xds	1.116s
```

Control:

```console
zz_verify_c5_e2e_test.go:252: PROBE event[0] ServerFilter.Build
zz_verify_c5_e2e_test.go:252: PROBE event[1] Interceptor#1.Build
zz_verify_c5_e2e_test.go:252: PROBE event[2] --- generation 1 serving (RPC OK); sending in-place RDS replacement for "routeName" ---
zz_verify_c5_e2e_test.go:252: PROBE event[3] Interceptor#1.Close parentFilterAlreadyDestroyed=false stack=[server.(*interceptorList).Close <- server.(*usableRouteConfiguration).stop <- server.(*filterChain).applyConfiguration <- server.(*filterChain).updateUsableRouteConfiguration <- server.(*listenerWrapper).handleRDSUpdate <- server.(*rdsWatcher).ResourceChanged]
zz_verify_c5_e2e_test.go:252: PROBE event[4] ServerFilter.Close (filter DESTROYED) stack=[server.(*refCountedServerFilter).Close <- server.(*filterChain).applyConfiguration <- server.(*filterChain).updateUsableRouteConfiguration <- server.(*listenerWrapper).handleRDSUpdate <- server.(*rdsWatcher).ResourceChanged]
zz_verify_c5_e2e_test.go:252: PROBE event[5] --- replacement observed; stopping server ---
zz_verify_c5_e2e_test.go:254: PROBE RESULT after in-place replacement: retired interceptors closed while parent filter still alive=1, closed AFTER parent filter was destroyed=0
zz_verify_c5_e2e_test.go:254: PROBE RESULT after in-place replacement: retired interceptors closed while parent filter still alive=1, closed AFTER parent filter was destroyed=0
```

Unit-level trace of the reference count on the target (`verify/repro/c1_refcount_probe_test.go`, run as in C1: `cp $R/c1_refcount_probe_test.go internal/xds/server/zz_verify_c1_probe_test.go && go test -race -count=1 -v -run 'Test/VerifyC1' ./internal/xds/server/`):

```console
zz_verify_c1_probe_test.go:170: PROBE event[2] --- gen1 active; in-place replacement starts (override=map[probe:{}]) --- id=0 filterRefCnt=1 stack=[]
zz_verify_c1_probe_test.go:170: PROBE event[3] ServerFilter.Close(destroyed)      id=0 filterRefCnt=0 stack=[server.(*refCountedServerFilter).Close <- server.(*filterChain).constructUsableRouteConfiguration <- server.(*listenerWrapper).handleRDSUpdate]
zz_verify_c1_probe_test.go:170: PROBE event[4] Interceptor.Close                  id=1 filterRefCnt=0 stack=[server.(*interceptorList).Close <- server.(*listenerWrapper).handleRDSUpdate.(*usableRouteConfiguration).close.func1 <- server.(*usableRouteConfiguration).close <- server.(*listenerWrapper).handleRDSUpdate]
zz_verify_c1_probe_test.go:170: PROBE event[5] --- handleRDSUpdate(gen2) returned --- id=0 filterRefCnt=0 stack=[]
zz_verify_c1_probe_test.go:209: PROBE RESULT all-disabled: FILTER DESTROYED BEFORE retired interceptor closed (event 3 < event 4)
zz_verify_c1_probe_test.go:170: PROBE event[2] --- gen1 active; in-place replacement starts (override=map[]) --- id=0 filterRefCnt=1 stack=[]
zz_verify_c1_probe_test.go:170: PROBE event[3] Interceptor.Build                  id=2 filterRefCnt=2 stack=[server.(*filterChain).newInterceptor <- server.(*filterChain).convertVirtualHost <- server.(*filterChain).constructUsableRouteConfiguration <- server.(*listenerWrapper).handleRDSUpdate]
zz_verify_c1_probe_test.go:170: PROBE event[4] Interceptor.Close                  id=1 filterRefCnt=1 stack=[server.(*interceptorList).Close <- server.(*listenerWrapper).handleRDSUpdate.(*usableRouteConfiguration).close.func1 <- server.(*usableRouteConfiguration).close <- server.(*listenerWrapper).handleRDSUpdate]
zz_verify_c1_probe_test.go:170: PROBE event[5] --- handleRDSUpdate(gen2) returned --- id=0 filterRefCnt=1 stack=[]
zz_verify_c1_probe_test.go:184: PROBE RESULT ordinary-replacement: filterRefCnt observed inside retired interceptor Close = 1 (2 => retired reference still held; 1 => retired reference already released)
```

- *reference_release_order* — held: in the ordinary replacement the retired reference is decremented (2 → 1) from `constructUsableRouteConfiguration` before the retired interceptor's `Close` runs (it observes refCnt 1); the control observes 2.
- *filter_destruction_trigger* — held: with the filter disabled on every route of the replacement, `ServerFilter.Close` is called from `refCountedServerFilter.Close <- constructUsableRouteConfiguration <- handleRDSUpdate` (event 3) and the retired interceptor is closed afterwards from `usableRouteConfiguration.close <- handleRDSUpdate` (event 4, `parentFilterAlreadyDestroyed=true`). On the control the order is reversed (`parentFilterAlreadyDestroyed=false`).

**Impact reasoning.** The trigger is an ordinary control-plane action: an in-place RDS update after which no route of that RouteConfiguration uses a filter any more (per-route `typed_per_filter_config` disabling it, or the routes removed), with no other filter chain holding the filter. On this branch the parent `ServerFilter` is then closed while the interceptors it built for the retired configuration are still open; those interceptors are closed a moment later on the same goroutine. Whether this is harmful depends on the filter: an interceptor whose `Close` (or an RPC still using the retired interceptor) touches parent-owned resources would use them after the parent released them. The probe filter only records order, so no crash was observed, and the eval fixture is 5/5 green on this branch — the inversion is silent. Expected order (and the control's order): close retired interceptors first, then release the retired configuration's filter references.

## C6

**Claim:** the added or changed tests do not exercise successful RDS delivery followed by error delivery through the production `handleRDSUpdate` handler and assert preservation of the delivered error in the published configuration. Target: [evalon/grpc-go-xd-1463306c](https://github.com/kaitranntt-evals/grpc-go-xds-rds-interceptor-lifecycle-leak/tree/evalon/grpc-go-xd-1463306c) (HEAD 4d2584c8). This claim is about what tests assert; evidence is the test text plus runs, including a mutation.

Naming drift: the branch has no `internal/xds/server/listener_wrapper_test.go` and no `TestHandleRDSUpdate_ErrorAfterSuccess` (`git grep -n TestHandleRDSUpdate_ErrorAfterSuccess` → no match). The added tests are `TestUsableRouteConfiguration_InterceptorLifecycle` (`internal/xds/server/filter_chain_manager_test.go`) and `TestServerSideXDS_FilterStateRetention_InPlaceRDSUpdate` (`test/xds/xds_server_filter_state_retention_test.go`).

```sh
$R/c6_run.sh ~/wt/1463306c > verify/logs/c6_1463306c.log 2>&1    # applies $R/c6_mutation_handleRDSUpdate_ignore_error.patch, runs, reverts
```

Mutation applied by the script (makes `handleRDSUpdate` ignore an RDS error and keep the old configuration — the exact behaviour the task statement forbids):

```diff
Run (on evalon/grpc-go-xd-1463306c): git apply verify/repro/c6_mutation_handleRDSUpdate_ignore_error.patch   -- applied and reverted automatically by verify/repro/c6_run.sh
--- a/internal/xds/server/listener_wrapper.go
+++ b/internal/xds/server/listener_wrapper.go
@@ -218,9 +218,7 @@
 				continue
 			}
 			if rcu.err != nil && rcu.data == nil { // Either NACK before update, or resource not found triggers this conditional.
-				urc := newUsableRouteConfiguration(nil, rcu.err)
-				urc.nodeID = l.xdsNodeID
-				fc.setUsableRouteConfiguration(urc)
+				// VERIFY MUTATION (C6): drop the delivered RDS error; keep serving the old configuration.
 				continue
 			}
 			urc := fc.constructUsableRouteConfiguration(*rcu.data, l.getOrCreateServerFilterLocked)
```

```console
### added/changed test functions (git diff 4ee6ac46 HEAD -- "*_test.go")
+func (s) TestUsableRouteConfiguration_InterceptorLifecycle(t *testing.T) {
+func (s) TestServerSideXDS_FilterStateRetention_InPlaceRDSUpdate(t *testing.T) {
### references to the production handler / watcher callbacks in added test lines
(none)
### error-state coverage in the added tests
67:+	wantErr := errors.New("rds resource error")
68:+	fc.setUsableRouteConfiguration(newUsableRouteConfiguration(nil, wantErr))
73:+	if got == nil || got.err != wantErr {
74:+		t.Fatalf("acquireUsableRouteConfiguration() returned %+v, want configuration with error %v", got, wantErr)
### [unmodified] added tests
--- PASS: Test (0.00s)
    --- PASS: Test/UsableRouteConfiguration_InterceptorLifecycle (0.00s)
PASS
ok  	google.golang.org/grpc/internal/xds/server	1.027s
--- PASS: Test (0.12s)
    --- PASS: Test/ServerSideXDS_FilterStateRetention_InPlaceRDSUpdate (0.12s)
PASS
ok  	google.golang.org/grpc/test/xds	1.148s
### [unmodified] feasibility probe (not part of the branch)
PROBE after success: published err=<nil> vhs=1
PROBE after error: published err=verify: rds resource error after success vhs=0
--- PASS: Test (0.00s)
    --- PASS: Test/VerifyC6_HandleRDSUpdate_ErrorAfterSuccess (0.00s)
PASS
ok  	google.golang.org/grpc/internal/xds/server	1.025s
### mutation applied:
 internal/xds/server/listener_wrapper.go | 4 +---
 1 file changed, 1 insertion(+), 3 deletions(-)
### [MUTATED] added tests
--- PASS: Test (0.00s)
    --- PASS: Test/UsableRouteConfiguration_InterceptorLifecycle (0.00s)
PASS
ok  	google.golang.org/grpc/internal/xds/server	1.025s
--- PASS: Test (0.07s)
    --- PASS: Test/ServerSideXDS_FilterStateRetention_InPlaceRDSUpdate (0.07s)
PASS
ok  	google.golang.org/grpc/test/xds	1.096s
### [MUTATED] feasibility probe (not part of the branch)
PROBE after success: published err=<nil> vhs=1
PROBE after error: published err=<nil> vhs=1
after error: published configuration err = <nil>, want verify: rds resource error after success
--- FAIL: Test (0.00s)
    --- FAIL: Test/VerifyC6_HandleRDSUpdate_ErrorAfterSuccess (0.00s)
FAIL
FAIL	google.golang.org/grpc/internal/xds/server	0.024s
FAIL
### [MUTATED] full packages (pre-existing tests included)
ok  	google.golang.org/grpc/internal/xds/server	1.094s
ok  	google.golang.org/grpc/test/xds	13.179s
### reverted; git status --short:
```

The only error-state coverage in the added tests installs the error configuration directly (`git diff 4ee6ac46 HEAD -- internal/xds/server/filter_chain_manager_test.go`):

```go
	wantErr := errors.New("rds resource error")
	fc.setUsableRouteConfiguration(newUsableRouteConfiguration(nil, wantErr))
	...
	got = acquireUsableRouteConfiguration(fc.usableRouteConfiguration)
	if got == nil || got.err != wantErr {
```

Eval fixture under the same mutation (`git apply $R/c6_mutation_handleRDSUpdate_ignore_error.patch; $R/run_eval_fixture.sh ~/wt/1463306c ~/eval_fixture/tests/eval_xds_server_interceptor_leak_test.go; git apply -R ...`), compared with the unmutated branch:

```console
unmutated: PASS / PASS / PASS / FAIL / FAIL
mutated:   PASS / PASS / PASS / FAIL / FAIL
```

(Columns as in Setup. The two `Partial*` failures are present with and without the mutation and are unrelated to it.)

**Observed:** no added test line references `handleRDSUpdate`, `ResourceError`, `ResourceChanged` or `rdsWatcherUpdate`; the unit test sets the error configuration with `fc.setUsableRouteConfiguration(newUsableRouteConfiguration(nil, wantErr))`; the integration test only performs successful in-place updates. With the production handler mutated to drop the error, both added tests, the complete `internal/xds/server` and `test/xds` packages, and the eval fixture's result are unchanged, while the probe test (success then error through `listenerWrapper.handleRDSUpdate`, asserting the published `err`) passes unmodified and fails under the mutation (`published err=<nil> vhs=1`). No test matching the Refute criterion exists on the branch.

**Impact reasoning.** The production behaviour is correct on this branch today (probe: after the error the published configuration is `err=verify: rds resource error after success vhs=0`). The gap is in coverage of a behaviour the task statement spells out ("If an RDS error arrives after a configuration has been serving successfully, subsequent RPCs should report that error rather than continue using the old configuration"): the branch rewrote the code that publishes the error configuration (`setUsableRouteConfiguration`), and a regression that keeps serving the old configuration after an RDS error would pass every test in both packages. The probe file is a ready-made unit test for it.
