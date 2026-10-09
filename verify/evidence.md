## C1

Claim: the dependency-release-order test (`TestEval_ServerSideXDS_InterceptorBeforeFilterCloseOrder` in the fixture `eval_xds_server_interceptor_leak_test.go`) leaves premature filter release undetected during replacement and during shutdown.

Target: claim branch `evalon/grpc-go-xd-acf71ed1` (commit `c0650988`) in `kaitranntt-evals/grpc-go-xds-rds-interceptor-lifecycle-leak`. Fixture used byte-exact from `eval_tests.zip` (sha256 `f45705924d980647d7d216c5c09996e7600c995585488239b4b8d97936748e00`), provisioned at `test/xds/eval_xds_server_interceptor_leak_test.go`. Go 1.25.7, linux/amd64.

### What the test collects and asserts (fixture lines 1378-1623)

- Events: `evalOrderFilter.Close` records `filter-close`, `evalOrderInterceptor.Close` records `icpt-close`.
- Configuration: one filter chain, one virtual host, **one route** (so one interceptor depends on the tracking filter). The replacement RDS update sets `TypedPerFilterConfig: {orderTracker: FilterConfig{Disabled: true}}`, so the replacement configuration builds **no** tracking interceptor; then the server is stopped.
- Assertion loop (verbatim):

```go
	for idx, e := range events {
		if e.kind == "filter-close" && firstFilterCloseIdx == -1 {
			firstFilterCloseIdx = idx
		}
		if e.kind == "icpt-close" && lastRetiredIcptCloseIdx == -1 {
			// First interceptor was the retired one from initial configuration.
			lastRetiredIcptCloseIdx = idx
			icptCloseCount++
		}
	}
	...
	if firstFilterCloseIdx < lastRetiredIcptCloseIdx {
		t.Fatalf("dependency order violation: ...
```

  Despite its name, `lastRetiredIcptCloseIdx` is only ever set to the index of the **first** `icpt-close`. The only ordering fact asserted is "first filter-close is not before first icpt-close".

### Method

All runs are in a worktree of the claim branch. Replay with one command (from this branch's checkout):

```sh
verify/repro/c1_run.sh /path/to/tests/eval_xds_server_interceptor_leak_test.go
```

The script applies `verify/repro/c1_instrumentation.patch` to the claim branch. The patch adds (a) an independent lifecycle trace on stderr (`XTRACE`) emitted from production code at `interceptorList.Close`, `refCountedServerFilter.Close` (with resulting refcount and whether the underlying filter is closed), and at the start/end of replacement and of `filterChainManager.stop()`; (b) an env var `VERIFY_MODE` selecting a controlled release order:

| VERIFY_MODE | replacement order | shutdown order (`filterChainManager.stop()`) |
|---|---|---|
| unset | the solution's own (old filter refs released in `constructUsableRouteConfiguration`, then retired interceptors closed) | solution's own (interceptors, then `fc.stop()`) |
| `control` | all retired interceptors, then old filter refs | interceptors, then `fc.stop()` |
| `repl-mid` | first retired interceptor, **then old filter refs**, then remaining retired interceptors | as control |
| `shutdown-early` | as control | **`fc.stop()` first**, then interceptors |

Tests run:

- `Eval_ServerSideXDS_InterceptorBeforeFilterCloseOrder`: the fixture, byte-exact.
- `Verify_OrderVariant_TwoRoutesReplacement` and `Verify_OrderVariant_FilterEnabledAtShutdown` (`verify/repro/c1_order_variants_test.go.txt`): generated from the fixture's test function text; only the route configuration differs (two routes; or filter left enabled in the replacement), the event collection and assertions are the fixture's own. Each appends a non-failing independent check logged as `STRICT-CHECK` (every interceptor `Fn/Ik` must close before `Fn` closes).

Each trace shows two filter wrappers per chain: the tracking filter and the router filter (the router's wrapper is the one that stays referenced after the tracker is disabled).

### Output (verbatim, filtered to XTRACE / STRICT-CHECK / verdict lines by the script)

```console
### VERIFY_MODE='' go test -v -run '^Test$/^Eval_ServerSideXDS_InterceptorBeforeFilterCloseOrder$' ./test/xds -race -count=1
XTRACE replacement begin mode="" (solution order: old filter refs released first)
XTRACE replacement begin mode="" (solution order: old filter refs released first)
XTRACE filter-release filter=0xc000800dc8 refcnt-after=0 underlying-close=true
XTRACE filter-release filter=0xc000800de0 refcnt-after=1 underlying-close=false
XTRACE icpt-close list=0xc000800df8 members=1
XTRACE shutdown begin mode=""
XTRACE icpt-close list=0xc000810138 members=0
XTRACE filter-release filter=0xc000800de0 refcnt-after=0 underlying-close=true
dependency order violation: filter closed at event #0 before retired interceptor closed at event #1 (events: [{kind:filter-close id:F1} {kind:icpt-close id:F1/I1}])
--- FAIL: Test (0.05s)
    --- FAIL: Test/Eval_ServerSideXDS_InterceptorBeforeFilterCloseOrder (0.04s)
FAIL
FAIL	google.golang.org/grpc/test/xds	0.092s
FAIL

### VERIFY_MODE='control' go test -v -run '^Test$/^Eval_ServerSideXDS_InterceptorBeforeFilterCloseOrder$' ./test/xds -race -count=1
XTRACE replacement begin mode="control" retired-interceptors=1 retired-filter-refs=2
XTRACE icpt-close list=0xc000137428 members=1
XTRACE filter-release filter=0xc0001373f8 refcnt-after=0 underlying-close=true
XTRACE filter-release filter=0xc000137410 refcnt-after=1 underlying-close=false
XTRACE replacement end
XTRACE shutdown begin mode="control" active-interceptors=1 filter-refs=1
XTRACE icpt-close list=0xc000273c08 members=0
XTRACE filter-release filter=0xc000137410 refcnt-after=0 underlying-close=true
XTRACE shutdown end
--- PASS: Test (0.07s)
    --- PASS: Test/Eval_ServerSideXDS_InterceptorBeforeFilterCloseOrder (0.07s)
PASS
ok  	google.golang.org/grpc/test/xds	1.116s

### VERIFY_MODE='repl-mid' go test -v -run '^Test$/^Eval_ServerSideXDS_InterceptorBeforeFilterCloseOrder$' ./test/xds -race -count=1
XTRACE replacement begin mode="repl-mid" retired-interceptors=1 retired-filter-refs=2
XTRACE icpt-close list=0xc00029e0f0 members=1
XTRACE filter-release filter=0xc00029e0c0 refcnt-after=0 underlying-close=true
XTRACE filter-release filter=0xc00029e0d8 refcnt-after=1 underlying-close=false
XTRACE replacement end
XTRACE shutdown begin mode="repl-mid" active-interceptors=1 filter-refs=1
XTRACE icpt-close list=0xc00083a060 members=0
XTRACE filter-release filter=0xc00029e0d8 refcnt-after=0 underlying-close=true
XTRACE shutdown end
--- PASS: Test (0.07s)
    --- PASS: Test/Eval_ServerSideXDS_InterceptorBeforeFilterCloseOrder (0.06s)
PASS
ok  	google.golang.org/grpc/test/xds	1.122s

### VERIFY_MODE='shutdown-early' go test -v -run '^Test$/^Eval_ServerSideXDS_InterceptorBeforeFilterCloseOrder$' ./test/xds -race -count=1
XTRACE replacement begin mode="shutdown-early" retired-interceptors=1 retired-filter-refs=2
XTRACE icpt-close list=0xc0006014b8 members=1
XTRACE filter-release filter=0xc000601488 refcnt-after=0 underlying-close=true
XTRACE filter-release filter=0xc0006014a0 refcnt-after=1 underlying-close=false
XTRACE replacement end
XTRACE shutdown begin mode="shutdown-early" active-interceptors=1 filter-refs=1
XTRACE filter-release filter=0xc0006014a0 refcnt-after=0 underlying-close=true
XTRACE icpt-close list=0xc000700468 members=0
XTRACE shutdown end
--- PASS: Test (0.05s)
    --- PASS: Test/Eval_ServerSideXDS_InterceptorBeforeFilterCloseOrder (0.04s)
PASS
ok  	google.golang.org/grpc/test/xds	1.106s

### VERIFY_MODE='' go test -v -run '^Test$/^Verify_OrderVariant_TwoRoutesReplacement$' ./test/xds -race -count=1
XTRACE replacement begin mode="" (solution order: old filter refs released first)
XTRACE replacement begin mode="" (solution order: old filter refs released first)
XTRACE filter-release filter=0xc000524978 refcnt-after=1 underlying-close=false
XTRACE filter-release filter=0xc000524990 refcnt-after=3 underlying-close=false
XTRACE filter-release filter=0xc000524978 refcnt-after=0 underlying-close=true
XTRACE filter-release filter=0xc000524990 refcnt-after=2 underlying-close=false
XTRACE icpt-close list=0xc0005249a8 members=1
XTRACE icpt-close list=0xc0005249d8 members=1
XTRACE shutdown begin mode=""
XTRACE icpt-close list=0xc000119500 members=0
XTRACE icpt-close list=0xc000119530 members=0
XTRACE filter-release filter=0xc000524990 refcnt-after=1 underlying-close=false
XTRACE filter-release filter=0xc000524990 refcnt-after=0 underlying-close=true
dependency order violation: filter closed at event #0 before retired interceptor closed at event #1 (events: [{kind:filter-close id:F1} {kind:icpt-close id:F1/I1} {kind:icpt-close id:F1/I2}])
--- FAIL: Test (0.08s)
    --- FAIL: Test/Verify_OrderVariant_TwoRoutesReplacement (0.07s)
FAIL
FAIL	google.golang.org/grpc/test/xds	0.117s
FAIL

### VERIFY_MODE='control' go test -v -run '^Test$/^Verify_OrderVariant_TwoRoutesReplacement$' ./test/xds -race -count=1
XTRACE replacement begin mode="control" retired-interceptors=2 retired-filter-refs=4
XTRACE icpt-close list=0xc000012378 members=1
XTRACE icpt-close list=0xc0000123a8 members=1
XTRACE filter-release filter=0xc000012348 refcnt-after=1 underlying-close=false
XTRACE filter-release filter=0xc000012360 refcnt-after=3 underlying-close=false
XTRACE filter-release filter=0xc000012348 refcnt-after=0 underlying-close=true
XTRACE filter-release filter=0xc000012360 refcnt-after=2 underlying-close=false
XTRACE replacement end
XTRACE shutdown begin mode="control" active-interceptors=2 filter-refs=2
XTRACE icpt-close list=0xc0001293b0 members=0
XTRACE icpt-close list=0xc0001293e0 members=0
XTRACE filter-release filter=0xc000012360 refcnt-after=1 underlying-close=false
XTRACE filter-release filter=0xc000012360 refcnt-after=0 underlying-close=true
XTRACE shutdown end
STRICT-CHECK events: [{kind:icpt-close id:F1/I1} {kind:icpt-close id:F1/I2} {kind:filter-close id:F1}]
STRICT-CHECK violations=0
--- PASS: Test (0.07s)
    --- PASS: Test/Verify_OrderVariant_TwoRoutesReplacement (0.06s)
PASS
ok  	google.golang.org/grpc/test/xds	1.112s

### VERIFY_MODE='repl-mid' go test -v -run '^Test$/^Verify_OrderVariant_TwoRoutesReplacement$' ./test/xds -race -count=1
XTRACE replacement begin mode="repl-mid" retired-interceptors=2 retired-filter-refs=4
XTRACE icpt-close list=0xc0004b77a0 members=1
XTRACE filter-release filter=0xc0004b7770 refcnt-after=1 underlying-close=false
XTRACE filter-release filter=0xc0004b7788 refcnt-after=3 underlying-close=false
XTRACE filter-release filter=0xc0004b7770 refcnt-after=0 underlying-close=true
XTRACE filter-release filter=0xc0004b7788 refcnt-after=2 underlying-close=false
XTRACE icpt-close list=0xc0004b77d0 members=1
XTRACE replacement end
XTRACE shutdown begin mode="repl-mid" active-interceptors=2 filter-refs=2
XTRACE icpt-close list=0xc00090c228 members=0
XTRACE icpt-close list=0xc00090c258 members=0
XTRACE filter-release filter=0xc0004b7788 refcnt-after=1 underlying-close=false
XTRACE filter-release filter=0xc0004b7788 refcnt-after=0 underlying-close=true
XTRACE shutdown end
STRICT-CHECK events: [{kind:icpt-close id:F1/I1} {kind:filter-close id:F1} {kind:icpt-close id:F1/I2}]
STRICT-CHECK VIOLATION: filter F1 closed at event #1 while interceptor F1/I2 was still open (closed at event #2)
STRICT-CHECK violations=1
--- PASS: Test (0.05s)
    --- PASS: Test/Verify_OrderVariant_TwoRoutesReplacement (0.04s)
PASS
ok  	google.golang.org/grpc/test/xds	1.085s

### VERIFY_MODE='' go test -v -run '^Test$/^Verify_OrderVariant_FilterEnabledAtShutdown$' ./test/xds -race -count=1
XTRACE replacement begin mode="" (solution order: old filter refs released first)
XTRACE replacement begin mode="" (solution order: old filter refs released first)
XTRACE filter-release filter=0xc0003f0f90 refcnt-after=2 underlying-close=false
XTRACE filter-release filter=0xc0003f0fa8 refcnt-after=2 underlying-close=false
XTRACE icpt-close list=0xc0003f0fc0 members=1
XTRACE shutdown begin mode=""
XTRACE icpt-close list=0xc0003f0540 members=1
XTRACE icpt-close list=0xc0003f05e8 members=1
XTRACE filter-release filter=0xc0003f0f90 refcnt-after=1 underlying-close=false
XTRACE filter-release filter=0xc0003f0fa8 refcnt-after=1 underlying-close=false
XTRACE filter-release filter=0xc0003f0f90 refcnt-after=0 underlying-close=true
XTRACE filter-release filter=0xc0003f0fa8 refcnt-after=0 underlying-close=true
STRICT-CHECK events: [{kind:icpt-close id:F1/I1} {kind:icpt-close id:F1/I2} {kind:icpt-close id:F1/I3} {kind:filter-close id:F1}]
STRICT-CHECK violations=0
--- PASS: Test (0.07s)
    --- PASS: Test/Verify_OrderVariant_FilterEnabledAtShutdown (0.07s)
PASS
ok  	google.golang.org/grpc/test/xds	1.114s

### VERIFY_MODE='control' go test -v -run '^Test$/^Verify_OrderVariant_FilterEnabledAtShutdown$' ./test/xds -race -count=1
XTRACE replacement begin mode="control" retired-interceptors=1 retired-filter-refs=2
XTRACE icpt-close list=0xc000036138 members=1
XTRACE filter-release filter=0xc000036108 refcnt-after=2 underlying-close=false
XTRACE filter-release filter=0xc000036120 refcnt-after=2 underlying-close=false
XTRACE replacement end
XTRACE shutdown begin mode="control" active-interceptors=2 filter-refs=4
XTRACE icpt-close list=0xc00046e0f0 members=1
XTRACE icpt-close list=0xc00046e150 members=1
XTRACE filter-release filter=0xc000036108 refcnt-after=1 underlying-close=false
XTRACE filter-release filter=0xc000036120 refcnt-after=1 underlying-close=false
XTRACE filter-release filter=0xc000036108 refcnt-after=0 underlying-close=true
XTRACE filter-release filter=0xc000036120 refcnt-after=0 underlying-close=true
XTRACE shutdown end
STRICT-CHECK events: [{kind:icpt-close id:F1/I1} {kind:icpt-close id:F1/I2} {kind:icpt-close id:F1/I3} {kind:filter-close id:F1}]
STRICT-CHECK violations=0
--- PASS: Test (0.07s)
    --- PASS: Test/Verify_OrderVariant_FilterEnabledAtShutdown (0.06s)
PASS
ok  	google.golang.org/grpc/test/xds	1.110s

### VERIFY_MODE='shutdown-early' go test -v -run '^Test$/^Verify_OrderVariant_FilterEnabledAtShutdown$' ./test/xds -race -count=1
XTRACE replacement begin mode="shutdown-early" retired-interceptors=1 retired-filter-refs=2
XTRACE icpt-close list=0xc000436168 members=1
XTRACE filter-release filter=0xc000436138 refcnt-after=2 underlying-close=false
XTRACE filter-release filter=0xc000436150 refcnt-after=2 underlying-close=false
XTRACE replacement end
XTRACE shutdown begin mode="shutdown-early" active-interceptors=2 filter-refs=4
XTRACE filter-release filter=0xc000436138 refcnt-after=1 underlying-close=false
XTRACE filter-release filter=0xc000436150 refcnt-after=1 underlying-close=false
XTRACE filter-release filter=0xc000436138 refcnt-after=0 underlying-close=true
XTRACE filter-release filter=0xc000436150 refcnt-after=0 underlying-close=true
XTRACE icpt-close list=0xc000436720 members=1
XTRACE icpt-close list=0xc000436d80 members=1
XTRACE shutdown end
STRICT-CHECK events: [{kind:icpt-close id:F1/I1} {kind:filter-close id:F1} {kind:icpt-close id:F1/I2} {kind:icpt-close id:F1/I3}]
STRICT-CHECK VIOLATION: filter F1 closed at event #1 while interceptor F1/I2 was still open (closed at event #2)
STRICT-CHECK VIOLATION: filter F1 closed at event #1 while interceptor F1/I3 was still open (closed at event #3)
STRICT-CHECK violations=2
--- PASS: Test (0.07s)
    --- PASS: Test/Verify_OrderVariant_FilterEnabledAtShutdown (0.06s)
PASS
ok  	google.golang.org/grpc/test/xds	1.117s
```

Stability of the four decisive runs (`-count=10`, each prints a single `ok` line for 10 passes):

```console
## repl-mid Eval_ServerSideXDS_InterceptorBeforeFilterCloseOrder x10
      1 ok  	google.golang.org/grpc/test/xds	1.505s
## shutdown-early Eval_ServerSideXDS_InterceptorBeforeFilterCloseOrder x10
      1 ok  	google.golang.org/grpc/test/xds	1.623s
## repl-mid Verify_OrderVariant_TwoRoutesReplacement x10
      1 ok  	google.golang.org/grpc/test/xds	1.431s
## shutdown-early Verify_OrderVariant_FilterEnabledAtShutdown x10
      1 ok  	google.golang.org/grpc/test/xds	1.537s
```

### Reading the output

**replacement_observation - CONFIRMED.**

- Fixture as shipped, `repl-mid`: PASS. With one route there is only one retired interceptor, so "a later dependent interceptor" never exists; the scenario cannot occur in the fixture's configuration at all (trace: `retired-interceptors=1`).
- Two-route variant with the fixture's own assertions, `repl-mid`: trace shows `icpt-close` (route 1), then `filter-release ... refcnt-after=0 underlying-close=true`, then `icpt-close` (route 2), all inside `replacement begin/end`. Test event log: `[icpt-close F1/I1, filter-close F1, icpt-close F1/I2]`. `STRICT-CHECK VIOLATION: filter F1 closed at event #1 while interceptor F1/I2 was still open`. Result: **PASS**, 10/10.
- Controls: same variant under `control` gives `[icpt-close F1/I1, icpt-close F1/I2, filter-close F1]`, `violations=0`, PASS; under the solution's own order (filter before the *first* interceptor) the assertion fires. So the assertion is live, but only for a filter closed before the first interceptor close.

**shutdown_observation - CONFIRMED.**

- Fixture as shipped, `shutdown-early`: PASS. Trace inside `shutdown begin/end`: `filter-release ... underlying-close=true` precedes `icpt-close list=... members=0`. `members=0` is the point: the replacement disabled the tracking filter, so the active configuration holds no tracking interceptor and the tracking filter was already closed during replacement. Shutdown produces no tracked events whatever order is used.
- Variant that leaves the filter enabled (so two tracking interceptors are live at shutdown), fixture's own assertions, `shutdown-early`: trace inside `shutdown begin/end` shows both filter wrappers reach `refcnt-after=0 underlying-close=true` before the two `icpt-close ... members=1`. Test event log: `[icpt-close F1/I1, filter-close F1, icpt-close F1/I2, icpt-close F1/I3]`, `STRICT-CHECK violations=2`. Result: **PASS**, 10/10. The retired interceptor `F1/I1` closed during replacement satisfies "first filter-close not before first icpt-close", masking the shutdown inversion.
- Control: same variant under `control` and under the solution's own order gives `violations=0`, PASS.

### Impact reasoning

The test is the only fixture named for dependency ordering. It guards exactly one ordering: the filter must not close before the first retired interceptor closes. Two inversions pass it: (1) with more than one route per filter chain (the ordinary case for real RouteConfigurations), a filter released between retired-interceptor closes; (2) any inversion in `filterChainManager.stop()` / `fc.stop()` at server shutdown or LDS-driven filter chain replacement, because the test's replacement step disables the filter before shutdown. A solution with either defect would be scored as correct on this check. Filters such as ext_proc own resources (channels/streams) that their interceptors use in `Close`, so the inverted order is a use-after-release for those interceptors.

### Side observation (not part of the claim)

On the unmodified claim branch the fixture's order test **fails**: `dependency order violation: filter closed at event #0 before retired interceptor closed at event #1 (events: [{kind:filter-close id:F1} {kind:icpt-close id:F1/I1}])`. The branch's `constructUsableRouteConfiguration` releases the old filter references before `updateUsableRouteConfiguration` closes the retired interceptors. This is the one ordering the test does detect, and it is why the controlled modes above defer the old-filter release.
