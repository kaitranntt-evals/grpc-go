## Environment and conventions

Observations only; verdicts are delivered separately. Everything below was run on linux/amd64 with `go version go1.25.7`, 8 CPUs.

- Audited branch: [grpc-go-xds-rds-interceptor-lifecycle-leak-perfect](https://github.com/kaitranntt-evals/grpc-go/tree/grpc-go-xds-rds-interceptor-lifecycle-leak-perfect) at `614cb7398c43bf270878ac15f34b68f5a9a4159d`, checked out as `verify/grpc-go-xds-rds-interceptor-lifecycle-leak-v-20a07696` in `~/repos/grpc-go`. Production code is untouched; everything added lives under `verify/`.
- Claim-target branches live in a second repository and were fetched into detached worktrees `~/wt/<id>`:

```sh
cd ~/repos/grpc-go
git remote add claims https://github.com/kaitranntt-evals/grpc-go-xds-rds-interceptor-lifecycle-leak
for id in 89904dd8 b66819a5 6d02dfd3 8520d18f df8b80c6 a7a2d47b 0628e30a d36e1629 6c895192 3e71ce68 edda3c72 e5960f86 20ecf5d0 adbe1cae; do
  git fetch claims "evalon/grpc-go-xd-$id:refs/remotes/claims/$id"
  git worktree add --detach ~/wt/$id claims/$id
done
git worktree add --detach ~/wt/base 4ee6ac46fada69c06576cee108b009689a000520   # task base commit, used as a control
```

Commits audited:

```console
perfect   614cb7398c43bf270878ac15f34b68f5a9a4159d
89904dd8  d5f4f3129a60ba7de53a7f57d619c0c96077b765
b66819a5  f750a99976365d4580516d573f7288e147b8e32c
6d02dfd3  55cdffa49c9b85cf6b7e60208db2c2fb0be474e6
8520d18f  a7aeb5dfc06f92ebdfbf35666cf8ebdf82753ea5
df8b80c6  71bf0d891365a49b3ed27fe518788cf88d925dbb
a7a2d47b  fee7fb580572ab15edee25063608274d3e1efd2e
0628e30a  9ddf958c021be14ec0444b02523532809da7a2db
d36e1629  ec7aec2d421b929dde8b67ccbef0af69e36d9f05
6c895192  22c792e6058b5850372d685dc082d0d44b70bb8f
3e71ce68  e41391f5f5d98da5e5e6e5ba437dd62840e0cb9f
edda3c72  75d7e436818b5c0369740ae476faaca691fdcb53
e5960f86  00125c8930c4aa039c2886191db8fce35a0f12dd
20ecf5d0  7694180ad6ed8b3fa63c1c9dd898d7c47a615092
adbe1cae  a8858d4ab2dc761df406377e776a971abc8bdd72
```

- Eval fixture: `eval_tests.zip` extracted to `~/eval/`, file `~/eval/tests/eval_xds_server_interceptor_leak_test.go` used byte-exact (copied to `test/xds/eval_xds_server_interceptor_leak_test.go` for a run, removed afterwards).
- Probes (all test-only; copied into the package under test for a run and removed afterwards; the leading underscore keeps them out of `go build ./...` while they sit under `verify/`):
  - `verify/repro/_verify_e2e_probe_test.go` — end-to-end probes through a real `xds.NewGRPCServer`, a real management server and a real client. A registered HTTP filter records an ordered event log: `filter-build Fn`, `icpt-build Fn/Im(mode) filterAlreadyClosed=…`, `allow-rpc Fn/Im interceptorAlreadyClosed=… filterAlreadyClosed=…`, `icpt-close Fn/Im`, `filter-close Fn`.
  - `verify/repro/_verify_c2_rds_error_release_test.go` — drives `listenerWrapper.handleRDSUpdate` (the RDS watcher's entry point) with a success followed by a resource error, and prints the server-filter cache reference counts.
  - `verify/repro/_verify_closed_filter_reacquire_test.go` — direct call sequence on `getOrCreateServerFilterWithMap`.
  - `verify/repro/c1_controlled_pause.sh` + `verify/instrumentation/c1_pause.py`, `verify/instrumentation/c1_0628e30a_modes.py` — controlled pauses / withheld resources injected into the branches' own test doubles (test files only).
- In outputs below, long lines are cut at ~300 characters and `=== RUN` / gRPC log noise is filtered out; nothing else is edited.

## C1

Claim: the added server lifecycle tests contain an assertion or blocking operation whose prerequisite asynchronous state is not established by bounded synchronization. Adjudicated per branch.

### Method

Every branch's added end-to-end lifecycle test uses the pre-existing test double `trackingHTTPFilterBuilder` / `trackingInterceptor` in `test/xds/xds_server_filter_state_retention_test.go` (counters `interceptorsCreated`, `interceptorsDestroyed`). For each branch three runs of the branch's own added test were made, changing only that test double:

- `baseline` — untouched;
- `close` — `time.Sleep(150ms)` at the top of `trackingInterceptor.Close()`, i.e. retirement of a superseded interceptor is still in progress for 150ms;
- `build` — `time.Sleep(150ms)` at the top of `BuildServerInterceptor()`, i.e. construction of a filter chain's new configuration is still in progress for 150ms.

All tests have a 10s context deadline (`defaultTestTimeout`). A test whose assertions are synchronized must either pass (it waited) or fail at ~10s with a timeout message. A test that fails within a fraction of a second with a count mismatch asserted before the asynchronous work finished.

```sh
# per branch <id>, test <T> (mapping below); what verify/repro/c1_controlled_pause.sh <id> <close|build> automates
cd ~/wt/<id>
python3 ~/repos/grpc-go/verify/instrumentation/c1_pause.py <close|build> test/xds/xds_server_filter_state_retention_test.go
go test -race -v -count=1 ./test/xds -run '^Test$/^<T>$'
git checkout -- test/xds/xds_server_filter_state_retention_test.go
```

Summary of the 33 runs (`Test` wall time as printed by `go test`):

| branch | added test | baseline | `close` pause | `build` pause |
|---|---|---|---|---|

| [evalon/grpc-go-xd-89904dd8](https://github.com/kaitranntt-evals/grpc-go-xds-rds-interceptor-lifecycle-leak/tree/evalon/grpc-go-xd-89904dd8) | `TestServerSideXDS_FilterStateRetention_RepeatedRouteConfigUpdates` | PASS in 0.28s | FAIL in 0.34s | PASS in 3.32s |

| [evalon/grpc-go-xd-b66819a5](https://github.com/kaitranntt-evals/grpc-go-xds-rds-interceptor-lifecycle-leak/tree/evalon/grpc-go-xd-b66819a5) | `TestServerSideXDS_FilterStateRetention_RepeatedRDSUpdates` | PASS in 0.24s | FAIL in 0.34s | PASS in 3.34s |

| [evalon/grpc-go-xd-6d02dfd3](https://github.com/kaitranntt-evals/grpc-go-xds-rds-interceptor-lifecycle-leak/tree/evalon/grpc-go-xd-6d02dfd3) | `TestServerSideXDS_RDSUpdates_ReleaseSupersededInterceptors` | PASS in 0.16s | FAIL in 0.65s | FAIL in 0.64s |

| [evalon/grpc-go-xd-8520d18f](https://github.com/kaitranntt-evals/grpc-go-xds-rds-interceptor-lifecycle-leak/tree/evalon/grpc-go-xd-8520d18f) | `TestServerSideXDS_FilterStateRetention_AcrossRDSUpdates` | PASS in 0.15s | FAIL in 0.35s | PASS in 1.76s |

| [evalon/grpc-go-xd-df8b80c6](https://github.com/kaitranntt-evals/grpc-go-xds-rds-interceptor-lifecycle-leak/tree/evalon/grpc-go-xd-df8b80c6) | `TestServerSideXDS_FilterStateRetention_AcrossRouteConfigUpdates` | PASS in 0.15s | FAIL in 0.35s | PASS in 1.80s |

| [evalon/grpc-go-xd-a7a2d47b](https://github.com/kaitranntt-evals/grpc-go-xds-rds-interceptor-lifecycle-leak/tree/evalon/grpc-go-xd-a7a2d47b) | `TestServerSideXDS_RepeatedRDSUpdates_ReleaseSupersededInterceptors` | PASS in 0.27s | FAIL in 0.35s | PASS in 3.33s |

| [evalon/grpc-go-xd-0628e30a](https://github.com/kaitranntt-evals/grpc-go-xds-rds-interceptor-lifecycle-leak/tree/evalon/grpc-go-xd-0628e30a) | `TestServerSideXDS_FilterStateRetention_AcrossUpdates_RouteConfigChange` | PASS in 0.11s | PASS in 1.90s | PASS in 1.86s |

| [evalon/grpc-go-xd-d36e1629](https://github.com/kaitranntt-evals/grpc-go-xds-rds-interceptor-lifecycle-leak/tree/evalon/grpc-go-xd-d36e1629) | `TestServerSideXDS_FilterStateRetention_AcrossUpdates_RouteConfigChange` | PASS in 0.09s | FAIL in 0.70s | FAIL in 0.64s |

| [evalon/grpc-go-xd-6c895192](https://github.com/kaitranntt-evals/grpc-go-xds-rds-interceptor-lifecycle-leak/tree/evalon/grpc-go-xd-6c895192) | `TestServerSideXDS_FilterStateRetention_AcrossUpdates_RouteConfigChange` | PASS in 0.07s | FAIL in 0.35s | PASS in 0.65s |

| [evalon/grpc-go-xd-3e71ce68](https://github.com/kaitranntt-evals/grpc-go-xds-rds-interceptor-lifecycle-leak/tree/evalon/grpc-go-xd-3e71ce68) | `TestServerSideXDS_FilterStateRetention_AcrossUpdates_RouteConfigChange` | PASS in 0.10s | FAIL in 0.64s | FAIL in 0.64s |

| [evalon/grpc-go-xd-edda3c72](https://github.com/kaitranntt-evals/grpc-go-xds-rds-interceptor-lifecycle-leak/tree/evalon/grpc-go-xd-edda3c72) | `TestServerSideXDS_RouteConfigUpdate_ReleasesSupersededResources` | PASS in 0.11s | FAIL in 0.34s | PASS in 0.99s |



### C1 — [evalon/grpc-go-xd-89904dd8](https://github.com/kaitranntt-evals/grpc-go-xds-rds-interceptor-lifecycle-leak/tree/evalon/grpc-go-xd-89904dd8)

Commit `d5f4f3129a60ba7de53a7f57d619c0c96077b765`, test `TestServerSideXDS_FilterStateRetention_RepeatedRouteConfigUpdates`.

```console
$ go test -race -v -count=1 ./test/xds -run '^Test$/^ServerSideXDS_FilterStateRetention_RepeatedRouteConfigUpdates$'   [branch 89904dd8, instrumentation: close]
    xds_server_filter_state_retention_test.go:786: After 1 route configuration updates, 2 interceptors created and 0 destroyed; got 2 live interceptors, want 1
--- FAIL: Test (0.34s)
    --- FAIL: Test/ServerSideXDS_FilterStateRetention_RepeatedRouteConfigUpdates (0.33s)
FAIL
FAIL	google.golang.org/grpc/test/xds	0.372s
FAIL
go test exit=1
```

```console
$ go test -race -v -count=1 ./test/xds -run '^Test$/^ServerSideXDS_FilterStateRetention_RepeatedRouteConfigUpdates$'   [branch 89904dd8, instrumentation: build]
--- PASS: Test (3.32s)
    --- PASS: Test/ServerSideXDS_FilterStateRetention_RepeatedRouteConfigUpdates (3.31s)
PASS
ok  	google.golang.org/grpc/test/xds	4.351s
go test exit=0
```

Uninstrumented repeat run (no pause at all):

```console
$ go test -race -count=150 ./test/xds -run '^Test$/^ServerSideXDS_FilterStateRetention_RepeatedRouteConfigUpdates$'   [branch 89904dd8, no instrumentation]
top-level PASS=150 FAIL=0 exit=0
```

The assertion that fired, as delivered (`sed -n 773,786p test/xds/xds_server_filter_state_retention_test.go` in `~/wt/89904dd8`); the only synchronization before it is the replacement-traffic loop (`waitForPath` / `WaitForUpdatedConfig`: send `EmptyCall` until the interceptor reports the new path):

```go
	for i := 1; i <= numUpdates; i++ {
		path := fmt.Sprintf("path-%d", i)
		resources.Routes = append(clientRoutes, serverRouteConfig(path))
		if err := managementServer.Update(ctx, resources); err != nil {
			t.Fatal(err)
		}
		waitForPath(path)

		// Only the interceptor for the currently active route configuration
		// should remain alive.
		created, destroyed := interceptorsCreated.Load(), interceptorsDestroyed.Load()
		if live := created - destroyed; live != 1 {
			t.Fatalf("After %d route configuration updates, %d interceptors created and %d destroyed; got %d live interceptors, want 1", i, created, destroyed, live)
		}
```

Observation: with retirement paused for 150ms the test fails in ~0.35s on a destroyed/live-count mismatch — it did not wait for closure and did not run into its 10s deadline. With only construction paused the test passes.

### C1 — [evalon/grpc-go-xd-b66819a5](https://github.com/kaitranntt-evals/grpc-go-xds-rds-interceptor-lifecycle-leak/tree/evalon/grpc-go-xd-b66819a5)

Commit `f750a99976365d4580516d573f7288e147b8e32c`, test `TestServerSideXDS_FilterStateRetention_RepeatedRDSUpdates`.

```console
$ go test -race -v -count=1 ./test/xds -run '^Test$/^ServerSideXDS_FilterStateRetention_RepeatedRDSUpdates$'   [branch b66819a5, instrumentation: close]
    xds_server_filter_state_retention_test.go:788: After 1 RDS updates, 2 interceptor instances are alive (created: 2, destroyed: 0), want: 1
--- FAIL: Test (0.34s)
    --- FAIL: Test/ServerSideXDS_FilterStateRetention_RepeatedRDSUpdates (0.34s)
FAIL
FAIL	google.golang.org/grpc/test/xds	0.383s
FAIL
go test exit=1
```

```console
$ go test -race -v -count=1 ./test/xds -run '^Test$/^ServerSideXDS_FilterStateRetention_RepeatedRDSUpdates$'   [branch b66819a5, instrumentation: build]
--- PASS: Test (3.34s)
    --- PASS: Test/ServerSideXDS_FilterStateRetention_RepeatedRDSUpdates (3.33s)
PASS
ok  	google.golang.org/grpc/test/xds	4.373s
go test exit=0
```

Uninstrumented repeat run (no pause at all):

```console
$ go test -race -count=150 ./test/xds -run '^Test$/^ServerSideXDS_FilterStateRetention_RepeatedRDSUpdates$'   [branch b66819a5, no instrumentation]
top-level PASS=149 FAIL=1 exit=1
      1 xds_server_filter_state_retention_test.go:787: After 9 RDS updates, 2 interceptor instances are alive (created: 10, destroyed: 8), want: 1
```

The assertion that fired, as delivered (`sed -n 774,789p test/xds/xds_server_filter_state_retention_test.go` in `~/wt/b66819a5`); the only synchronization before it is the replacement-traffic loop (`waitForPath` / `WaitForUpdatedConfig`: send `EmptyCall` until the interceptor reports the new path):

```go
	for i := 1; i <= numUpdates; i++ {
		path := fmt.Sprintf("update-%d", i)
		resources.Routes = append(clientRoutes, serverRouteConfig(path))
		if err := managementServer.Update(ctx, resources); err != nil {
			t.Fatal(err)
		}
		waitForPath(path)

		created, destroyed := interceptorsCreated.Load(), interceptorsDestroyed.Load()
		if got, want := created, int32(i+1); got != want {
			t.Fatalf("After %d RDS updates, created %d interceptor instances, want: %d", i, got, want)
		}
		if got, want := created-destroyed, int32(1); got != want {
			t.Fatalf("After %d RDS updates, %d interceptor instances are alive (created: %d, destroyed: %d), want: %d", i, got, created, destroyed, want)
		}
	}
```

Observation: with retirement paused for 150ms the test fails in ~0.35s on a destroyed/live-count mismatch — it did not wait for closure and did not run into its 10s deadline. With only construction paused the test passes.

This branch also failed once in 150 uninstrumented runs with the same assertion (`After 9 RDS updates, 2 interceptor instances are alive (created: 10, destroyed: 8), want: 1`), i.e. the window is reachable without any injected pause.

### C1 — [evalon/grpc-go-xd-6d02dfd3](https://github.com/kaitranntt-evals/grpc-go-xds-rds-interceptor-lifecycle-leak/tree/evalon/grpc-go-xd-6d02dfd3)

Commit `55cdffa49c9b85cf6b7e60208db2c2fb0be474e6`, test `TestServerSideXDS_RDSUpdates_ReleaseSupersededInterceptors`.

```console
$ go test -race -v -count=1 ./test/xds -run '^Test$/^ServerSideXDS_RDSUpdates_ReleaseSupersededInterceptors$'   [branch 6d02dfd3, instrumentation: close]
    xds_server_rds_update_cleanup_test.go:195: After update 1: created 3 interceptor instances, want 4
--- FAIL: Test (0.65s)
    --- FAIL: Test/ServerSideXDS_RDSUpdates_ReleaseSupersededInterceptors (0.64s)
FAIL
FAIL	google.golang.org/grpc/test/xds	0.686s
FAIL
go test exit=1
```

```console
$ go test -race -v -count=1 ./test/xds -run '^Test$/^ServerSideXDS_RDSUpdates_ReleaseSupersededInterceptors$'   [branch 6d02dfd3, instrumentation: build]
    xds_server_rds_update_cleanup_test.go:195: After update 1: created 3 interceptor instances, want 4
--- FAIL: Test (0.64s)
    --- FAIL: Test/ServerSideXDS_RDSUpdates_ReleaseSupersededInterceptors (0.63s)
FAIL
FAIL	google.golang.org/grpc/test/xds	0.675s
FAIL
go test exit=1
```

On this branch the listener has two filter chains, so the first premature assertion hit above is the aggregate creation count. To isolate the retirement assertion that follows it, the pause was restricted to every second retired interceptor's `Close()` (`verify/repro/c1_controlled_pause.sh 6d02dfd3 close2`):

```console
$ go test -race -v -count=1 ./test/xds -run '^Test$/^ServerSideXDS_RDSUpdates_ReleaseSupersededInterceptors$'   [branch 6d02dfd3, instrumentation: 150ms pause in every second retired interceptor's Close()]
    xds_server_rds_update_cleanup_test.go:198: After update 1: 3 interceptor instances alive (created 4, destroyed 1), want 2
--- FAIL: Test (0.34s)
    --- FAIL: Test/ServerSideXDS_RDSUpdates_ReleaseSupersededInterceptors (0.33s)
FAIL
FAIL	google.golang.org/grpc/test/xds	0.376s
FAIL
go test exit=1
```

Uninstrumented repeat run (no pause at all):

```console
$ go test -race -count=150 ./test/xds -run '^Test$/^ServerSideXDS_RDSUpdates_ReleaseSupersededInterceptors$'   [branch 6d02dfd3, no instrumentation]
top-level PASS=150 FAIL=0 exit=0
```

The assertion that fired, as delivered (`sed -n 182,199p test/xds/xds_server_rds_update_cleanup_test.go` in `~/wt/6d02dfd3`); the only synchronization before it is the replacement-traffic loop (`waitForPath` / `WaitForUpdatedConfig`: send `EmptyCall` until the interceptor reports the new path):

```go
	for i := 1; i <= numUpdates; i++ {
		path := fmt.Sprintf("path-%d", i)
		resources.Routes[len(resources.Routes)-1] = serverRouteConfigWithFilterOverride(t, routeName, filterName, testFilterTypeURL, path)
		if err := managementServer.Update(ctx, resources); err != nil {
			t.Fatal(err)
		}
		waitForPath(path)

		// Each RouteConfiguration update creates one interceptor per filter
		// chain. Only the interceptors of the current configuration must be
		// alive; all the others must have been closed.
		created, destroyed := interceptorsCreated.Load(), interceptorsDestroyed.Load()
		if got, want := created, int32(numFilterChains*(i+1)); got != want {
			t.Fatalf("After update %d: created %d interceptor instances, want %d", i, got, want)
		}
		if got, want := created-destroyed, int32(numFilterChains); got != want {
			t.Fatalf("After update %d: %d interceptor instances alive (created %d, destroyed %d), want %d", i, got, created, destroyed, want)
		}
```

Observation: this branch's listener has two filter chains. With either pause the test fails in ~0.65s on the aggregate creation count (3, want 4) — traffic on the client's connection reported the new path while the other filter chain was still being processed; with the pause confined to the second retirement the creation count is complete and the test fails in 0.34s on the live-count (retirement) assertion (created 4, destroyed 1). In neither case did it wait for the asynchronous work or run into its 10s deadline.

### C1 — [evalon/grpc-go-xd-8520d18f](https://github.com/kaitranntt-evals/grpc-go-xds-rds-interceptor-lifecycle-leak/tree/evalon/grpc-go-xd-8520d18f)

Commit `a7aeb5dfc06f92ebdfbf35666cf8ebdf82753ea5`, test `TestServerSideXDS_FilterStateRetention_AcrossRDSUpdates`.

```console
$ go test -race -v -count=1 ./test/xds -run '^Test$/^ServerSideXDS_FilterStateRetention_AcrossRDSUpdates$'   [branch 8520d18f, instrumentation: close]
    xds_server_filter_state_retention_test.go:782: After 1 RDS updates, 2 interceptors created and 0 destroyed; got 2 live interceptors, want 1
--- FAIL: Test (0.35s)
    --- FAIL: Test/ServerSideXDS_FilterStateRetention_AcrossRDSUpdates (0.34s)
FAIL
FAIL	google.golang.org/grpc/test/xds	0.381s
FAIL
go test exit=1
```

```console
$ go test -race -v -count=1 ./test/xds -run '^Test$/^ServerSideXDS_FilterStateRetention_AcrossRDSUpdates$'   [branch 8520d18f, instrumentation: build]
--- PASS: Test (1.76s)
    --- PASS: Test/ServerSideXDS_FilterStateRetention_AcrossRDSUpdates (1.76s)
PASS
ok  	google.golang.org/grpc/test/xds	2.805s
go test exit=0
```

Uninstrumented repeat run (no pause at all):

```console
$ go test -race -count=150 ./test/xds -run '^Test$/^ServerSideXDS_FilterStateRetention_AcrossRDSUpdates$'   [branch 8520d18f, no instrumentation]
top-level PASS=150 FAIL=0 exit=0
```

The assertion that fired, as delivered (`sed -n 768,783p test/xds/xds_server_filter_state_retention_test.go` in `~/wt/8520d18f`); the only synchronization before it is the replacement-traffic loop (`waitForPath` / `WaitForUpdatedConfig`: send `EmptyCall` until the interceptor reports the new path):

```go
	for i := 1; i <= numUpdates; i++ {
		path := fmt.Sprintf("path-%d", i)
		resources.Routes = append(clientRoutes, serverRouteConfig(path))
		if err := managementServer.Update(ctx, resources); err != nil {
			t.Fatal(err)
		}
		waitForPath(path)

		// Only the interceptor for the current RouteConfiguration should be
		// alive. Every interceptor built for a superseded configuration must
		// have been closed.
		created, destroyed := interceptorsCreated.Load(), interceptorsDestroyed.Load()
		if got, want := created-destroyed, int32(1); got != want {
			t.Fatalf("After %d RDS updates, %d interceptors created and %d destroyed; got %d live interceptors, want %d", i, created, destroyed, got, want)
		}
	}
```

Observation: with retirement paused for 150ms the test fails in ~0.35s on a destroyed/live-count mismatch — it did not wait for closure and did not run into its 10s deadline. With only construction paused the test passes.

### C1 — [evalon/grpc-go-xd-df8b80c6](https://github.com/kaitranntt-evals/grpc-go-xds-rds-interceptor-lifecycle-leak/tree/evalon/grpc-go-xd-df8b80c6)

Commit `71bf0d891365a49b3ed27fe518788cf88d925dbb`, test `TestServerSideXDS_FilterStateRetention_AcrossRouteConfigUpdates`.

```console
$ go test -race -v -count=1 ./test/xds -run '^Test$/^ServerSideXDS_FilterStateRetention_AcrossRouteConfigUpdates$'   [branch df8b80c6, instrumentation: close]
    xds_server_filter_state_retention_test.go:800: After 1 route config updates, destroyed 0 interceptor instances, want: 1
--- FAIL: Test (0.35s)
    --- FAIL: Test/ServerSideXDS_FilterStateRetention_AcrossRouteConfigUpdates (0.34s)
FAIL
FAIL	google.golang.org/grpc/test/xds	0.388s
FAIL
go test exit=1
```

```console
$ go test -race -v -count=1 ./test/xds -run '^Test$/^ServerSideXDS_FilterStateRetention_AcrossRouteConfigUpdates$'   [branch df8b80c6, instrumentation: build]
--- PASS: Test (1.80s)
    --- PASS: Test/ServerSideXDS_FilterStateRetention_AcrossRouteConfigUpdates (1.79s)
PASS
ok  	google.golang.org/grpc/test/xds	2.835s
go test exit=0
```

Uninstrumented repeat run (no pause at all):

```console
$ go test -race -count=150 ./test/xds -run '^Test$/^ServerSideXDS_FilterStateRetention_AcrossRouteConfigUpdates$'   [branch df8b80c6, no instrumentation]
top-level PASS=150 FAIL=0 exit=0
```

The assertion that fired, as delivered (`sed -n 787,801p test/xds/xds_server_filter_state_retention_test.go` in `~/wt/df8b80c6`); the only synchronization before it is the replacement-traffic loop (`waitForPath` / `WaitForUpdatedConfig`: send `EmptyCall` until the interceptor reports the new path):

```go
	for i := 1; i <= numUpdates; i++ {
		path := fmt.Sprintf("path-%d", i)
		resources.Routes = append(clientRoutes, serverRouteConfig(path))
		if err := managementServer.Update(ctx, resources); err != nil {
			t.Fatal(err)
		}
		waitForPath(path)

		if got, want := interceptorsCreated.Load(), int32(i+1); got != want {
			t.Fatalf("After %d route config updates, created %d interceptor instances, want: %d", i, got, want)
		}
		if got, want := interceptorsDestroyed.Load(), int32(i); got != want {
			t.Fatalf("After %d route config updates, destroyed %d interceptor instances, want: %d", i, got, want)
		}
	}
```

Observation: with retirement paused for 150ms the test fails in ~0.35s on a destroyed/live-count mismatch — it did not wait for closure and did not run into its 10s deadline. With only construction paused the test passes.

### C1 — [evalon/grpc-go-xd-a7a2d47b](https://github.com/kaitranntt-evals/grpc-go-xds-rds-interceptor-lifecycle-leak/tree/evalon/grpc-go-xd-a7a2d47b)

Commit `fee7fb580572ab15edee25063608274d3e1efd2e`, test `TestServerSideXDS_RepeatedRDSUpdates_ReleaseSupersededInterceptors`.

```console
$ go test -race -v -count=1 ./test/xds -run '^Test$/^ServerSideXDS_RepeatedRDSUpdates_ReleaseSupersededInterceptors$'   [branch a7a2d47b, instrumentation: close]
    xds_server_rds_update_test.go:206: After update 1: 2 interceptor instances alive (created: 2, destroyed: 0), want 1
--- FAIL: Test (0.35s)
    --- FAIL: Test/ServerSideXDS_RepeatedRDSUpdates_ReleaseSupersededInterceptors (0.34s)
FAIL
FAIL	google.golang.org/grpc/test/xds	0.395s
FAIL
go test exit=1
```

```console
$ go test -race -v -count=1 ./test/xds -run '^Test$/^ServerSideXDS_RepeatedRDSUpdates_ReleaseSupersededInterceptors$'   [branch a7a2d47b, instrumentation: build]
--- PASS: Test (3.33s)
    --- PASS: Test/ServerSideXDS_RepeatedRDSUpdates_ReleaseSupersededInterceptors (3.33s)
PASS
ok  	google.golang.org/grpc/test/xds	4.366s
go test exit=0
```

Uninstrumented repeat run (no pause at all):

```console
$ go test -race -count=150 ./test/xds -run '^Test$/^ServerSideXDS_RepeatedRDSUpdates_ReleaseSupersededInterceptors$'   [branch a7a2d47b, no instrumentation]
top-level PASS=150 FAIL=0 exit=0
```

The assertion that fired, as delivered (`sed -n 195,207p test/xds/xds_server_rds_update_test.go` in `~/wt/a7a2d47b`); the only synchronization before it is the replacement-traffic loop (`waitForPath` / `WaitForUpdatedConfig`: send `EmptyCall` until the interceptor reports the new path):

```go
	for i := 1; i <= numUpdates; i++ {
		path := fmt.Sprintf("path-%d", i)
		resources.Routes = append(clientRoutes, serverRouteConfig(path))
		if err := managementServer.Update(ctx, resources); err != nil {
			t.Fatal(err)
		}
		waitForPath(path)

		// Only the interceptor for the route in the latest configuration is
		// expected to be alive.
		if got, want := interceptorsCreated.Load()-interceptorsDestroyed.Load(), int32(1); got != want {
			t.Fatalf("After update %d: %d interceptor instances alive (created: %d, destroyed: %d), want %d", i, got, interceptorsCreated.Load(), interceptorsDestroyed.Load(), want)
		}
```

Observation: with retirement paused for 150ms the test fails in ~0.35s on a destroyed/live-count mismatch — it did not wait for closure and did not run into its 10s deadline. With only construction paused the test passes.

### C1 — [evalon/grpc-go-xd-0628e30a](https://github.com/kaitranntt-evals/grpc-go-xds-rds-interceptor-lifecycle-leak/tree/evalon/grpc-go-xd-0628e30a)

Commit `9ddf958c021be14ec0444b02523532809da7a2db`, test `TestServerSideXDS_FilterStateRetention_AcrossUpdates_RouteConfigChange`.

```console
$ go test -race -v -count=1 ./test/xds -run '^Test$/^ServerSideXDS_FilterStateRetention_AcrossUpdates_RouteConfigChange$'   [branch 0628e30a, instrumentation: close]
--- PASS: Test (1.90s)
    --- PASS: Test/ServerSideXDS_FilterStateRetention_AcrossUpdates_RouteConfigChange (1.89s)
PASS
ok  	google.golang.org/grpc/test/xds	2.936s
go test exit=0
```

```console
$ go test -race -v -count=1 ./test/xds -run '^Test$/^ServerSideXDS_FilterStateRetention_AcrossUpdates_RouteConfigChange$'   [branch 0628e30a, instrumentation: build]
--- PASS: Test (1.86s)
    --- PASS: Test/ServerSideXDS_FilterStateRetention_AcrossUpdates_RouteConfigChange (1.86s)
PASS
ok  	google.golang.org/grpc/test/xds	2.896s
go test exit=0
```

Uninstrumented repeat run (no pause at all):

```console
$ go test -race -count=150 ./test/xds -run '^Test$/^ServerSideXDS_FilterStateRetention_AcrossUpdates_RouteConfigChange$'   [branch 0628e30a, no instrumentation]
top-level PASS=150 FAIL=0 exit=0
```

Both pauses are absorbed: the test takes 1.9s instead of 0.1s and passes. The suspicion for this branch is a blocking listener acquisition without a bounded deadline or failure release, so the three tests this branch adds (`TestFilterChain_SetUsableRouteConfiguration_ReleasesSupersededResources`, `TestListenerWrapper_RouteConfigUpdates_ReleaseSupersededResources` in `internal/xds/server/listener_wrapper_test.go`, and the e2e test above) were additionally run with the asynchronous prerequisite removed altogether (`verify/instrumentation/c1_0628e30a_modes.py`, test files only):

- `int-nolistener` / `e2e-nolistener`: the Listener resource is never published, so the listener wrapper / server never reaches SERVING;
- `int-noretire` / `e2e-noretire`: retired interceptors never report closure;
- `int-close` / `int-build`: 150ms pauses in the unit-test double.

```sh
cd ~/wt/0628e30a
python3 ~/repos/grpc-go/verify/instrumentation/c1_0628e30a_modes.py <mode>
go test -race -v -count=1 -timeout 60s <./internal/xds/server | ./test/xds> -run '<regex shown in each output below>'
git checkout -- internal/xds/server/listener_wrapper_test.go test/xds/xds_server_filter_state_retention_test.go
```

```console
$ go test -race -v -count=1 -timeout 60s ./internal/xds/server ./test/xds -run '^Test$/^(FilterChain_SetUsableRouteConfiguration_ReleasesSupersededResources|ListenerWrapper_RouteConfigUpdates_ReleaseSupersededResources|ServerSideXDS_FilterStateRetention_AcrossUpdates_RouteConfigChange)$'   [branch 0628e30a, instrumentation: baseline]
    listener_wrapper_test.go:225: Created new snapshot cache...
--- PASS: Test (0.08s)
    --- PASS: Test/FilterChain_SetUsableRouteConfiguration_ReleasesSupersededResources (0.00s)
    --- PASS: Test/ListenerWrapper_RouteConfigUpdates_ReleaseSupersededResources (0.08s)
PASS
ok  	google.golang.org/grpc/internal/xds/server	1.117s
--- PASS: Test (0.12s)
    --- PASS: Test/ServerSideXDS_FilterStateRetention_AcrossUpdates_RouteConfigChange (0.10s)
PASS
ok  	google.golang.org/grpc/test/xds	1.156s
go test exit=0
```

```console
$ go test -race -v -count=1 -timeout 60s ./internal/xds/server -run '^Test$/^(FilterChain_SetUsableRouteConfiguration_ReleasesSupersededResources|ListenerWrapper_RouteConfigUpdates_ReleaseSupersededResources)$'   [branch 0628e30a, instrumentation: int-close]
    listener_wrapper_test.go:226: Created new snapshot cache...
--- PASS: Test (2.31s)
    --- PASS: Test/FilterChain_SetUsableRouteConfiguration_ReleasesSupersededResources (0.45s)
    --- PASS: Test/ListenerWrapper_RouteConfigUpdates_ReleaseSupersededResources (1.85s)
PASS
ok  	google.golang.org/grpc/internal/xds/server	3.340s
go test exit=0
```

```console
$ go test -race -v -count=1 -timeout 60s ./internal/xds/server -run '^Test$/^(FilterChain_SetUsableRouteConfiguration_ReleasesSupersededResources|ListenerWrapper_RouteConfigUpdates_ReleaseSupersededResources)$'   [branch 0628e30a, instrumentation: int-build]
    listener_wrapper_test.go:226: Created new snapshot cache...
--- PASS: Test (2.31s)
    --- PASS: Test/FilterChain_SetUsableRouteConfiguration_ReleasesSupersededResources (0.45s)
    --- PASS: Test/ListenerWrapper_RouteConfigUpdates_ReleaseSupersededResources (1.86s)
PASS
ok  	google.golang.org/grpc/internal/xds/server	3.347s
go test exit=0
```

```console
$ go test -race -v -count=1 -timeout 60s ./internal/xds/server -run '^Test$/^(FilterChain_SetUsableRouteConfiguration_ReleasesSupersededResources|ListenerWrapper_RouteConfigUpdates_ReleaseSupersededResources)$'   [branch 0628e30a, instrumentation: int-nolistener]
    listener_wrapper_test.go:225: Created new snapshot cache...
    listener_wrapper_test.go:327: Timeout waiting for the listener wrapper to start serving
--- FAIL: Test (10.06s)
    --- PASS: Test/FilterChain_SetUsableRouteConfiguration_ReleasesSupersededResources (0.00s)
    --- FAIL: Test/ListenerWrapper_RouteConfigUpdates_ReleaseSupersededResources (10.06s)
FAIL
FAIL	google.golang.org/grpc/internal/xds/server	10.095s
FAIL
go test exit=1
```

```console
$ go test -race -v -count=1 -timeout 60s ./internal/xds/server -run '^Test$/^(FilterChain_SetUsableRouteConfiguration_ReleasesSupersededResources|ListenerWrapper_RouteConfigUpdates_ReleaseSupersededResources)$'   [branch 0628e30a, instrumentation: int-noretire]
    listener_wrapper_test.go:200: Unexpected filter counts: got {filtersCreated:1 filtersDestroyed:0 interceptorsCreated:2 interceptorsDestroyed:0}, want {filtersCreated:1 filtersDestroyed:0 interceptorsCreated:2 interceptorsDestroyed:1}
    listener_wrapper_test.go:225: Created new snapshot cache...
    listener_wrapper_test.go:345: Timeout waiting for filter counts: got {filtersCreated:1 filtersDestroyed:0 interceptorsCreated:4 interceptorsDestroyed:0}, want {filtersCreated:1 filtersDestroyed:0 interceptorsCreated:4 interceptorsDestroyed:2}
--- FAIL: Test (10.02s)
    --- FAIL: Test/FilterChain_SetUsableRouteConfiguration_ReleasesSupersededResources (0.00s)
    --- FAIL: Test/ListenerWrapper_RouteConfigUpdates_ReleaseSupersededResources (10.01s)
FAIL
FAIL	google.golang.org/grpc/internal/xds/server	10.047s
FAIL
go test exit=1
```

```console
$ go test -race -v -count=1 -timeout 60s ./test/xds -run '^Test$/^ServerSideXDS_FilterStateRetention_AcrossUpdates_RouteConfigChange$'   [branch 0628e30a, instrumentation: e2e-nolistener]
    xds_server_filter_state_retention_test.go:767: Timeout waiting for server to enter SERVING mode
--- FAIL: Test (10.02s)
    --- FAIL: Test/ServerSideXDS_FilterStateRetention_AcrossUpdates_RouteConfigChange (10.01s)
FAIL
FAIL	google.golang.org/grpc/test/xds	10.051s
FAIL
go test exit=1
```

```console
$ go test -race -v -count=1 -timeout 60s ./test/xds -run '^Test$/^ServerSideXDS_FilterStateRetention_AcrossUpdates_RouteConfigChange$'   [branch 0628e30a, instrumentation: e2e-noretire]
    xds_server_filter_state_retention_test.go:823: Timeout waiting for interceptor counts: created 4, destroyed 0, want created 4, destroyed 2
--- FAIL: Test (10.03s)
    --- FAIL: Test/ServerSideXDS_FilterStateRetention_AcrossUpdates_RouteConfigChange (10.02s)
FAIL
FAIL	google.golang.org/grpc/test/xds	10.060s
FAIL
go test exit=1
```

The waits in question, as delivered (`sed -n 322,328p internal/xds/server/listener_wrapper_test.go` and the `waitForCounts` helper at 137-146):

```go
	case mode := <-modeCh:
		if mode != connectivity.ServingModeServing {
			t.Fatalf("Listener wrapper switched to mode %v, want %v", mode, connectivity.ServingModeServing)
		}
	case <-ctx.Done():
		t.Fatal("Timeout waiting for the listener wrapper to start serving")
	}
	t.Helper()
	var got counts
	for ; ctx.Err() == nil; <-time.After(defaultTestShortTimeout) {
		got = fb.counts()
		if got == want {
			return
		}
	}
	t.Fatalf("Timeout waiting for filter counts: got %+v, want %+v", got, want)
}
```

Observations: every wait on this branch is released by the 10s test context — withholding the Listener ends both tests at 10.0s with `Timeout waiting for the listener wrapper to start serving` / `Timeout waiting for server to enter SERVING mode`; suppressing retirement ends them at 10.0s with `Timeout waiting for … counts`; pauses make the tests wait and pass. The only assertion that is not polled, `listener_wrapper_test.go:200` in `TestFilterChain_SetUsableRouteConfiguration_…`, follows a synchronous call on the same goroutine (it passes with `int-close`/`int-build` pauses, 0.45s). The local listening socket itself is obtained with `testutils.LocalTCPListener()` (`net.Listen` on `localhost:0`), which has no asynchronous prerequisite. No premature assertion or unreleased wait was observed; 150/150 uninstrumented runs pass.

### C1 — [evalon/grpc-go-xd-d36e1629](https://github.com/kaitranntt-evals/grpc-go-xds-rds-interceptor-lifecycle-leak/tree/evalon/grpc-go-xd-d36e1629)

Commit `ec7aec2d421b929dde8b67ccbef0af69e36d9f05`, test `TestServerSideXDS_FilterStateRetention_AcrossUpdates_RouteConfigChange`.

```console
$ go test -race -v -count=1 ./test/xds -run '^Test$/^ServerSideXDS_FilterStateRetention_AcrossUpdates_RouteConfigChange$'   [branch d36e1629, instrumentation: close]
    xds_server_filter_state_retention_test.go:864: After route config update 1: created 3 interceptor instances, want: 4
--- FAIL: Test (0.70s)
    --- FAIL: Test/ServerSideXDS_FilterStateRetention_AcrossUpdates_RouteConfigChange (0.70s)
FAIL
FAIL	google.golang.org/grpc/test/xds	0.746s
FAIL
go test exit=1
```

```console
$ go test -race -v -count=1 ./test/xds -run '^Test$/^ServerSideXDS_FilterStateRetention_AcrossUpdates_RouteConfigChange$'   [branch d36e1629, instrumentation: build]
    xds_server_filter_state_retention_test.go:864: After route config update 1: created 3 interceptor instances, want: 4
--- FAIL: Test (0.64s)
    --- FAIL: Test/ServerSideXDS_FilterStateRetention_AcrossUpdates_RouteConfigChange (0.63s)
FAIL
FAIL	google.golang.org/grpc/test/xds	0.675s
FAIL
go test exit=1
```

Uninstrumented repeat run (no pause at all):

```console
$ go test -race -count=150 ./test/xds -run '^Test$/^ServerSideXDS_FilterStateRetention_AcrossUpdates_RouteConfigChange$'   [branch d36e1629, no instrumentation]
top-level PASS=150 FAIL=0 exit=0
```

The assertion that fired, as delivered (`sed -n 856,868p test/xds/xds_server_filter_state_retention_test.go` in `~/wt/d36e1629`); the only synchronization before it is the replacement-traffic loop (`waitForPath` / `WaitForUpdatedConfig`: send `EmptyCall` until the interceptor reports the new path):

```go
		if got, want := filtersCreated.Load(), int32(1); got != want {
			t.Fatalf("After route config update %d: created %d filter instances, want: %d", i, got, want)
		}
		if got, want := filtersDestroyed.Load(), int32(0); got != want {
			t.Fatalf("After route config update %d: destroyed %d filter instances, want: %d", i, got, want)
		}
		if got, want := interceptorsCreated.Load(), int32((i+1)*interceptorsPerUpdate); got != want {
			t.Fatalf("After route config update %d: created %d interceptor instances, want: %d", i, got, want)
		}
		// The superseded configuration is released right after the new one
		// is made visible to RPCs, so this may trail the RPC by a moment.
		waitForCounter(ctx, t, &interceptorsDestroyed, int32(i*interceptorsPerUpdate), fmt.Sprintf("interceptors destroyed after route config update %d", i))
	}
```

Observation: the listener on this branch's test has two filter chains (`interceptorsPerUpdate` = 2). Traffic on the client's connection reports the new path as soon as the first filter chain has swapped; with construction (or the preceding retirement) of the other filter chain paused, the aggregate `interceptorsCreated` assertion runs at 3 and fails at 0.6s instead of waiting. The `interceptorsDestroyed` check right after it does use deadline-bounded polling (`waitForCounter`).

### C1 — [evalon/grpc-go-xd-6c895192](https://github.com/kaitranntt-evals/grpc-go-xds-rds-interceptor-lifecycle-leak/tree/evalon/grpc-go-xd-6c895192)

Commit `22c792e6058b5850372d685dc082d0d44b70bb8f`, test `TestServerSideXDS_FilterStateRetention_AcrossUpdates_RouteConfigChange`.

```console
$ go test -race -v -count=1 ./test/xds -run '^Test$/^ServerSideXDS_FilterStateRetention_AcrossUpdates_RouteConfigChange$'   [branch 6c895192, instrumentation: close]
    xds_server_filter_state_retention_test.go:823: After update 1: destroyed 0 interceptor instances, want: 1
--- FAIL: Test (0.35s)
    --- FAIL: Test/ServerSideXDS_FilterStateRetention_AcrossUpdates_RouteConfigChange (0.34s)
FAIL
FAIL	google.golang.org/grpc/test/xds	0.380s
FAIL
go test exit=1
```

```console
$ go test -race -v -count=1 ./test/xds -run '^Test$/^ServerSideXDS_FilterStateRetention_AcrossUpdates_RouteConfigChange$'   [branch 6c895192, instrumentation: build]
--- PASS: Test (0.65s)
    --- PASS: Test/ServerSideXDS_FilterStateRetention_AcrossUpdates_RouteConfigChange (0.64s)
PASS
ok  	google.golang.org/grpc/test/xds	1.692s
go test exit=0
```

Uninstrumented repeat run (no pause at all):

```console
$ go test -race -count=150 ./test/xds -run '^Test$/^ServerSideXDS_FilterStateRetention_AcrossUpdates_RouteConfigChange$'   [branch 6c895192, no instrumentation]
top-level PASS=150 FAIL=0 exit=0
```

The assertion that fired, as delivered (`sed -n 812,824p test/xds/xds_server_filter_state_retention_test.go` in `~/wt/6c895192`); the only synchronization before it is the replacement-traffic loop (`waitForPath` / `WaitForUpdatedConfig`: send `EmptyCall` until the interceptor reports the new path):

```go
		if got, want := filtersCreated.Load(), int32(1); got != want {
			t.Fatalf("After update %d: created %d filter instances, want: %d", i, got, want)
		}
		if got, want := filtersDestroyed.Load(), int32(0); got != want {
			t.Fatalf("After update %d: destroyed %d filter instances, want: %d", i, got, want)
		}
		if got, want := interceptorsCreated.Load(), int32(i+1); got != want {
			t.Fatalf("After update %d: created %d interceptor instances, want: %d", i, got, want)
		}
		if got, want := interceptorsDestroyed.Load(), int32(i); got != want {
			t.Fatalf("After update %d: destroyed %d interceptor instances, want: %d", i, got, want)
		}
	}
```

Observation: with retirement paused for 150ms the test fails in ~0.35s on a destroyed/live-count mismatch — it did not wait for closure and did not run into its 10s deadline. With only construction paused the test passes.

### C1 — [evalon/grpc-go-xd-3e71ce68](https://github.com/kaitranntt-evals/grpc-go-xds-rds-interceptor-lifecycle-leak/tree/evalon/grpc-go-xd-3e71ce68)

Commit `e41391f5f5d98da5e5e6e5ba437dd62840e0cb9f`, test `TestServerSideXDS_FilterStateRetention_AcrossUpdates_RouteConfigChange`.

```console
$ go test -race -v -count=1 ./test/xds -run '^Test$/^ServerSideXDS_FilterStateRetention_AcrossUpdates_RouteConfigChange$'   [branch 3e71ce68, instrumentation: close]
    xds_server_filter_state_retention_test.go:854: After update 1, created 3 interceptor instances, want: 4
--- FAIL: Test (0.64s)
    --- FAIL: Test/ServerSideXDS_FilterStateRetention_AcrossUpdates_RouteConfigChange (0.64s)
FAIL
FAIL	google.golang.org/grpc/test/xds	0.675s
FAIL
go test exit=1
```

```console
$ go test -race -v -count=1 ./test/xds -run '^Test$/^ServerSideXDS_FilterStateRetention_AcrossUpdates_RouteConfigChange$'   [branch 3e71ce68, instrumentation: build]
    xds_server_filter_state_retention_test.go:854: After update 1, created 3 interceptor instances, want: 4
--- FAIL: Test (0.64s)
    --- FAIL: Test/ServerSideXDS_FilterStateRetention_AcrossUpdates_RouteConfigChange (0.63s)
FAIL
FAIL	google.golang.org/grpc/test/xds	0.680s
FAIL
go test exit=1
```

Uninstrumented repeat run (no pause at all):

```console
$ go test -race -count=150 ./test/xds -run '^Test$/^ServerSideXDS_FilterStateRetention_AcrossUpdates_RouteConfigChange$'   [branch 3e71ce68, no instrumentation]
top-level PASS=150 FAIL=0 exit=0
```

The assertion that fired, as delivered (`sed -n 851,858p test/xds/xds_server_filter_state_retention_test.go` in `~/wt/3e71ce68`); the only synchronization before it is the replacement-traffic loop (`waitForPath` / `WaitForUpdatedConfig`: send `EmptyCall` until the interceptor reports the new path):

```go

		if got, want := interceptorsCreated.Load(), int32((i+1)*interceptorsPerUpdate); got != want {
			t.Fatalf("After update %d, created %d interceptor instances, want: %d", i, got, want)
		}
		// The superseded interceptors are released right after the new
		// configuration is swapped in; wait briefly for that to happen.
		waitForCounter(ctx, t, &interceptorsDestroyed, int32(i*interceptorsPerUpdate), fmt.Sprintf("interceptors destroyed after update %d", i))
		// The filter instance must be retained across route configuration
```

Observation: the listener on this branch's test has two filter chains (`interceptorsPerUpdate` = 2). Traffic on the client's connection reports the new path as soon as the first filter chain has swapped; with construction (or the preceding retirement) of the other filter chain paused, the aggregate `interceptorsCreated` assertion runs at 3 and fails at 0.6s instead of waiting. The `interceptorsDestroyed` check right after it does use deadline-bounded polling (`waitForCounter`).

### C1 — [evalon/grpc-go-xd-edda3c72](https://github.com/kaitranntt-evals/grpc-go-xds-rds-interceptor-lifecycle-leak/tree/evalon/grpc-go-xd-edda3c72)

Commit `75d7e436818b5c0369740ae476faaca691fdcb53`, test `TestServerSideXDS_RouteConfigUpdate_ReleasesSupersededResources`.

```console
$ go test -race -v -count=1 ./test/xds -run '^Test$/^ServerSideXDS_RouteConfigUpdate_ReleasesSupersededResources$'   [branch edda3c72, instrumentation: close]
    xds_server_route_config_update_test.go:251: After 1 RouteConfiguration updates, destroyed 0 interceptor instances, want: 1
--- FAIL: Test (0.34s)
    --- FAIL: Test/ServerSideXDS_RouteConfigUpdate_ReleasesSupersededResources (0.34s)
FAIL
FAIL	google.golang.org/grpc/test/xds	0.382s
FAIL
go test exit=1
```

```console
$ go test -race -v -count=1 ./test/xds -run '^Test$/^ServerSideXDS_RouteConfigUpdate_ReleasesSupersededResources$'   [branch edda3c72, instrumentation: build]
--- PASS: Test (0.99s)
    --- PASS: Test/ServerSideXDS_RouteConfigUpdate_ReleasesSupersededResources (0.97s)
PASS
ok  	google.golang.org/grpc/test/xds	2.023s
go test exit=0
```

Uninstrumented repeat run (no pause at all):

```console
$ go test -race -count=150 ./test/xds -run '^Test$/^ServerSideXDS_RouteConfigUpdate_ReleasesSupersededResources$'   [branch edda3c72, no instrumentation]
top-level PASS=150 FAIL=0 exit=0
```

The assertion that fired, as delivered (`sed -n 243,252p test/xds/xds_server_route_config_update_test.go` in `~/wt/edda3c72`); the only synchronization before it is the replacement-traffic loop (`waitForPath` / `WaitForUpdatedConfig`: send `EmptyCall` until the interceptor reports the new path):

```go
		if ctx.Err() != nil {
			t.Fatalf("Timeout waiting for RouteConfiguration update %d to be applied: %v", i, ctx.Err())
		}

		if got, want := interceptorsCreated.Load(), int32(i+1); got != want {
			t.Fatalf("After %d RouteConfiguration updates, created %d interceptor instances, want: %d", i, got, want)
		}
		if got, want := interceptorsDestroyed.Load(), int32(i); got != want {
			t.Fatalf("After %d RouteConfiguration updates, destroyed %d interceptor instances, want: %d", i, got, want)
		}
```

Observation: with retirement paused for 150ms the test fails in ~0.35s on a destroyed/live-count mismatch — it did not wait for closure and did not run into its 10s deadline. With only construction paused the test passes.

## C2

Claim: an RDS error following a successfully published configuration does not release the retired configuration's ownership references to its server filters. Adjudicated per branch.

### Method

`verify/repro/_verify_c2_rds_error_release_test.go` builds a `listenerWrapper` with one filter chain whose HCM has one probe HTTP filter (so the filter chain's configuration is the only owner of the server filter), then calls the production RDS entry point:

1. `lw.handleRDSUpdate(routeName, rdsWatcherUpdate{data: rc})` — successful publication;
2. `lw.handleRDSUpdate(routeName, rdsWatcherUpdate{err: …})` — resource error (the path taken for a removed RouteConfiguration / NACK-before-update), checked to have been published (`urc.err` is the error);
3. `lw.activeFilterChainManager.stop()` — shutdown.

After each step it prints the event log and the reference counts in the listener wrapper's server-filter cache (`lw.httpFilters[...].refCnt`). A second test runs success → error → error → success → shutdown.

```sh
# per branch <id> in 6d02dfd3 e5960f86 89904dd8 3e71ce68 edda3c72
cd ~/wt/<id>
cp ~/repos/grpc-go/verify/repro/_verify_c2_rds_error_release_test.go internal/xds/server/verify_c2_rds_error_release_test.go
go test -race -v -count=1 ./internal/xds/server -run '^Test$/^Verify_C2_'      # and again with -count=5
rm internal/xds/server/verify_c2_rds_error_release_test.go
```

### C2 — [evalon/grpc-go-xd-6d02dfd3](https://github.com/kaitranntt-evals/grpc-go-xds-rds-interceptor-lifecycle-leak/tree/evalon/grpc-go-xd-6d02dfd3)

Commit `55cdffa49c9b85cf6b7e60208db2c2fb0be474e6`.

```console
    verify_c2_rds_error_release_test.go:191: OBSERVATION cache refcounts after success: probe=1
    verify_c2_rds_error_release_test.go:199: ---- after the RDS error transition completed (3 events) ----
    verify_c2_rds_error_release_test.go:199:   #00 filter-build F1
    verify_c2_rds_error_release_test.go:199:   #01 icpt-build F1/I1 filterAlreadyClosed=false
    verify_c2_rds_error_release_test.go:199:   #02 icpt-close F1/I1
    verify_c2_rds_error_release_test.go:202: OBSERVATION after error transition: retired interceptors closed=1, server filter Close() calls=0, cache refcounts: probe=1
    verify_c2_rds_error_release_test.go:206: OBSERVATION after shutdown: server filter Close() calls=1, cache refcounts: probe=0
    verify_c2_rds_error_release_test.go:209: PROBLEM REPRODUCED: the RDS error transition completed without releasing the retired configuration's hold on its server filter (sole owner; filter Close() only happened later: total=1)
    verify_c2_rds_error_release_test.go:221: OBSERVATION cache refcounts after success #1: probe=1
    verify_c2_rds_error_release_test.go:223: OBSERVATION cache refcounts after error #1:   probe=1 (filter Close() calls=0)
    verify_c2_rds_error_release_test.go:225: OBSERVATION cache refcounts after error #2:   probe=1 (filter Close() calls=0)
    verify_c2_rds_error_release_test.go:228: OBSERVATION cache refcounts after success #2: probe=1 (filter Close() calls=0)
    verify_c2_rds_error_release_test.go:230: OBSERVATION cache refcounts after shutdown:   probe=0 (filter Close() calls=1)
    verify_c2_rds_error_release_test.go:233: PROBLEM REPRODUCED: the retired configuration's server filter hold stayed unreleased for as long as the route configuration was in the error state
--- FAIL: Test (0.00s)
    --- FAIL: Test/Verify_C2_RDSErrorAfterSuccess_ReleasesFilterHold (0.00s)
    --- FAIL: Test/Verify_C2_RDSErrorAfterSuccess_ThenRecovery (0.00s)
FAIL
FAIL	google.golang.org/grpc/internal/xds/server	0.031s
FAIL
go test exit=1
```

Repeat with `-count=5`: 5 of 5 iterations print `PROBLEM REPRODUCED: the RDS error transition completed without releasing …`, 5 of 5 print `retired interceptors closed=1, server filter Close() calls=0, cache refcounts: probe=1`; `exit=1`.

Observation: the error transition closes the retired interceptor (`icpt-close F1/I1`) but the cache reference count stays at 1 and the filter's `Close()` is not called; the hold survives a second error and is only dropped by the next successful update (which acquires its own reference first, so the filter stays alive) or by shutdown (`filter-close F1`, refcount 0).

### C2 — [evalon/grpc-go-xd-e5960f86](https://github.com/kaitranntt-evals/grpc-go-xds-rds-interceptor-lifecycle-leak/tree/evalon/grpc-go-xd-e5960f86)

Commit `00125c8930c4aa039c2886191db8fce35a0f12dd`.

```console
    verify_c2_rds_error_release_test.go:191: OBSERVATION cache refcounts after success: probe=1
    verify_c2_rds_error_release_test.go:199: ---- after the RDS error transition completed (3 events) ----
    verify_c2_rds_error_release_test.go:199:   #00 filter-build F1
    verify_c2_rds_error_release_test.go:199:   #01 icpt-build F1/I1 filterAlreadyClosed=false
    verify_c2_rds_error_release_test.go:199:   #02 icpt-close F1/I1
    verify_c2_rds_error_release_test.go:202: OBSERVATION after error transition: retired interceptors closed=1, server filter Close() calls=0, cache refcounts: probe=1
    verify_c2_rds_error_release_test.go:206: OBSERVATION after shutdown: server filter Close() calls=1, cache refcounts: probe=0
    verify_c2_rds_error_release_test.go:209: PROBLEM REPRODUCED: the RDS error transition completed without releasing the retired configuration's hold on its server filter (sole owner; filter Close() only happened later: total=1)
    verify_c2_rds_error_release_test.go:221: OBSERVATION cache refcounts after success #1: probe=1
    verify_c2_rds_error_release_test.go:223: OBSERVATION cache refcounts after error #1:   probe=1 (filter Close() calls=0)
    verify_c2_rds_error_release_test.go:225: OBSERVATION cache refcounts after error #2:   probe=1 (filter Close() calls=0)
    verify_c2_rds_error_release_test.go:228: OBSERVATION cache refcounts after success #2: probe=1 (filter Close() calls=0)
    verify_c2_rds_error_release_test.go:230: OBSERVATION cache refcounts after shutdown:   probe=0 (filter Close() calls=1)
    verify_c2_rds_error_release_test.go:233: PROBLEM REPRODUCED: the retired configuration's server filter hold stayed unreleased for as long as the route configuration was in the error state
--- FAIL: Test (0.00s)
    --- FAIL: Test/Verify_C2_RDSErrorAfterSuccess_ReleasesFilterHold (0.00s)
    --- FAIL: Test/Verify_C2_RDSErrorAfterSuccess_ThenRecovery (0.00s)
FAIL
FAIL	google.golang.org/grpc/internal/xds/server	0.030s
FAIL
go test exit=1
```

Repeat with `-count=5`: 5 of 5 iterations print `PROBLEM REPRODUCED: the RDS error transition completed without releasing …`, 5 of 5 print `retired interceptors closed=1, server filter Close() calls=0, cache refcounts: probe=1`; `exit=1`.

Observation: the error transition closes the retired interceptor (`icpt-close F1/I1`) but the cache reference count stays at 1 and the filter's `Close()` is not called; the hold survives a second error and is only dropped by the next successful update (which acquires its own reference first, so the filter stays alive) or by shutdown (`filter-close F1`, refcount 0).

### C2 — [evalon/grpc-go-xd-89904dd8](https://github.com/kaitranntt-evals/grpc-go-xds-rds-interceptor-lifecycle-leak/tree/evalon/grpc-go-xd-89904dd8)

Commit `d5f4f3129a60ba7de53a7f57d619c0c96077b765`.

```console
    verify_c2_rds_error_release_test.go:191: OBSERVATION cache refcounts after success: probe=1
    verify_c2_rds_error_release_test.go:199: ---- after the RDS error transition completed (3 events) ----
    verify_c2_rds_error_release_test.go:199:   #00 filter-build F1
    verify_c2_rds_error_release_test.go:199:   #01 icpt-build F1/I1 filterAlreadyClosed=false
    verify_c2_rds_error_release_test.go:199:   #02 icpt-close F1/I1
    verify_c2_rds_error_release_test.go:202: OBSERVATION after error transition: retired interceptors closed=1, server filter Close() calls=0, cache refcounts: probe=1
    verify_c2_rds_error_release_test.go:206: OBSERVATION after shutdown: server filter Close() calls=1, cache refcounts: probe=0
    verify_c2_rds_error_release_test.go:209: PROBLEM REPRODUCED: the RDS error transition completed without releasing the retired configuration's hold on its server filter (sole owner; filter Close() only happened later: total=1)
    verify_c2_rds_error_release_test.go:221: OBSERVATION cache refcounts after success #1: probe=1
    verify_c2_rds_error_release_test.go:223: OBSERVATION cache refcounts after error #1:   probe=1 (filter Close() calls=0)
    verify_c2_rds_error_release_test.go:225: OBSERVATION cache refcounts after error #2:   probe=1 (filter Close() calls=0)
    verify_c2_rds_error_release_test.go:228: OBSERVATION cache refcounts after success #2: probe=1 (filter Close() calls=0)
    verify_c2_rds_error_release_test.go:230: OBSERVATION cache refcounts after shutdown:   probe=0 (filter Close() calls=1)
    verify_c2_rds_error_release_test.go:233: PROBLEM REPRODUCED: the retired configuration's server filter hold stayed unreleased for as long as the route configuration was in the error state
--- FAIL: Test (0.00s)
    --- FAIL: Test/Verify_C2_RDSErrorAfterSuccess_ReleasesFilterHold (0.00s)
    --- FAIL: Test/Verify_C2_RDSErrorAfterSuccess_ThenRecovery (0.00s)
FAIL
FAIL	google.golang.org/grpc/internal/xds/server	0.034s
FAIL
go test exit=1
```

Repeat with `-count=5`: 5 of 5 iterations print `PROBLEM REPRODUCED: the RDS error transition completed without releasing …`, 5 of 5 print `retired interceptors closed=1, server filter Close() calls=0, cache refcounts: probe=1`; `exit=1`.

Observation: the error transition closes the retired interceptor (`icpt-close F1/I1`) but the cache reference count stays at 1 and the filter's `Close()` is not called; the hold survives a second error and is only dropped by the next successful update (which acquires its own reference first, so the filter stays alive) or by shutdown (`filter-close F1`, refcount 0).

### C2 — [evalon/grpc-go-xd-3e71ce68](https://github.com/kaitranntt-evals/grpc-go-xds-rds-interceptor-lifecycle-leak/tree/evalon/grpc-go-xd-3e71ce68)

Commit `e41391f5f5d98da5e5e6e5ba437dd62840e0cb9f`.

```console
    verify_c2_rds_error_release_test.go:191: OBSERVATION cache refcounts after success: probe=1
    verify_c2_rds_error_release_test.go:199: ---- after the RDS error transition completed (3 events) ----
    verify_c2_rds_error_release_test.go:199:   #00 filter-build F1
    verify_c2_rds_error_release_test.go:199:   #01 icpt-build F1/I1 filterAlreadyClosed=false
    verify_c2_rds_error_release_test.go:199:   #02 icpt-close F1/I1
    verify_c2_rds_error_release_test.go:202: OBSERVATION after error transition: retired interceptors closed=1, server filter Close() calls=0, cache refcounts: probe=1
    verify_c2_rds_error_release_test.go:206: OBSERVATION after shutdown: server filter Close() calls=1, cache refcounts: probe=0
    verify_c2_rds_error_release_test.go:209: PROBLEM REPRODUCED: the RDS error transition completed without releasing the retired configuration's hold on its server filter (sole owner; filter Close() only happened later: total=1)
    verify_c2_rds_error_release_test.go:221: OBSERVATION cache refcounts after success #1: probe=1
    verify_c2_rds_error_release_test.go:223: OBSERVATION cache refcounts after error #1:   probe=1 (filter Close() calls=0)
    verify_c2_rds_error_release_test.go:225: OBSERVATION cache refcounts after error #2:   probe=1 (filter Close() calls=0)
    verify_c2_rds_error_release_test.go:228: OBSERVATION cache refcounts after success #2: probe=1 (filter Close() calls=0)
    verify_c2_rds_error_release_test.go:230: OBSERVATION cache refcounts after shutdown:   probe=0 (filter Close() calls=1)
    verify_c2_rds_error_release_test.go:233: PROBLEM REPRODUCED: the retired configuration's server filter hold stayed unreleased for as long as the route configuration was in the error state
--- FAIL: Test (0.00s)
    --- FAIL: Test/Verify_C2_RDSErrorAfterSuccess_ReleasesFilterHold (0.00s)
    --- FAIL: Test/Verify_C2_RDSErrorAfterSuccess_ThenRecovery (0.00s)
FAIL
FAIL	google.golang.org/grpc/internal/xds/server	0.031s
FAIL
go test exit=1
```

Repeat with `-count=5`: 5 of 5 iterations print `PROBLEM REPRODUCED: the RDS error transition completed without releasing …`, 5 of 5 print `retired interceptors closed=1, server filter Close() calls=0, cache refcounts: probe=1`; `exit=1`.

Observation: the error transition closes the retired interceptor (`icpt-close F1/I1`) but the cache reference count stays at 1 and the filter's `Close()` is not called; the hold survives a second error and is only dropped by the next successful update (which acquires its own reference first, so the filter stays alive) or by shutdown (`filter-close F1`, refcount 0).

### C2 — [evalon/grpc-go-xd-edda3c72](https://github.com/kaitranntt-evals/grpc-go-xds-rds-interceptor-lifecycle-leak/tree/evalon/grpc-go-xd-edda3c72)

Commit `75d7e436818b5c0369740ae476faaca691fdcb53`.

```console
    verify_c2_rds_error_release_test.go:191: OBSERVATION cache refcounts after success: probe=1
    verify_c2_rds_error_release_test.go:199: ---- after the RDS error transition completed (3 events) ----
    verify_c2_rds_error_release_test.go:199:   #00 filter-build F1
    verify_c2_rds_error_release_test.go:199:   #01 icpt-build F1/I1 filterAlreadyClosed=false
    verify_c2_rds_error_release_test.go:199:   #02 icpt-close F1/I1
    verify_c2_rds_error_release_test.go:202: OBSERVATION after error transition: retired interceptors closed=1, server filter Close() calls=0, cache refcounts: probe=1
    verify_c2_rds_error_release_test.go:206: OBSERVATION after shutdown: server filter Close() calls=1, cache refcounts: probe=0
    verify_c2_rds_error_release_test.go:209: PROBLEM REPRODUCED: the RDS error transition completed without releasing the retired configuration's hold on its server filter (sole owner; filter Close() only happened later: total=1)
    verify_c2_rds_error_release_test.go:221: OBSERVATION cache refcounts after success #1: probe=1
    verify_c2_rds_error_release_test.go:223: OBSERVATION cache refcounts after error #1:   probe=1 (filter Close() calls=0)
    verify_c2_rds_error_release_test.go:225: OBSERVATION cache refcounts after error #2:   probe=1 (filter Close() calls=0)
    verify_c2_rds_error_release_test.go:228: OBSERVATION cache refcounts after success #2: probe=1 (filter Close() calls=0)
    verify_c2_rds_error_release_test.go:230: OBSERVATION cache refcounts after shutdown:   probe=0 (filter Close() calls=1)
    verify_c2_rds_error_release_test.go:233: PROBLEM REPRODUCED: the retired configuration's server filter hold stayed unreleased for as long as the route configuration was in the error state
--- FAIL: Test (0.00s)
    --- FAIL: Test/Verify_C2_RDSErrorAfterSuccess_ReleasesFilterHold (0.00s)
    --- FAIL: Test/Verify_C2_RDSErrorAfterSuccess_ThenRecovery (0.00s)
FAIL
FAIL	google.golang.org/grpc/internal/xds/server	0.032s
FAIL
go test exit=1
```

Repeat with `-count=5`: 5 of 5 iterations print `PROBLEM REPRODUCED: the RDS error transition completed without releasing …`, 5 of 5 print `retired interceptors closed=1, server filter Close() calls=0, cache refcounts: probe=1`; `exit=1`.

Observation: the error transition closes the retired interceptor (`icpt-close F1/I1`) but the cache reference count stays at 1 and the filter's `Close()` is not called; the hold survives a second error and is only dropped by the next successful update (which acquires its own reference first, so the filter stays alive) or by shutdown (`filter-close F1`, refcount 0).

### C2 — control on the audited branch

Same probe on [grpc-go-xds-rds-interceptor-lifecycle-leak-perfect](https://github.com/kaitranntt-evals/grpc-go/tree/grpc-go-xds-rds-interceptor-lifecycle-leak-perfect) (`614cb7398c43bf270878ac15f34b68f5a9a4159d`), which is not a target of this claim, to show the probe can tell the two behaviors apart:

```console
    verify_c2_rds_error_release_test.go:191: OBSERVATION cache refcounts after success: probe=1
    verify_c2_rds_error_release_test.go:199: ---- after the RDS error transition completed (4 events) ----
    verify_c2_rds_error_release_test.go:199:   #00 filter-build F1
    verify_c2_rds_error_release_test.go:199:   #01 icpt-build F1/I1 filterAlreadyClosed=false
    verify_c2_rds_error_release_test.go:199:   #02 icpt-close F1/I1
    verify_c2_rds_error_release_test.go:199:   #03 filter-close F1
    verify_c2_rds_error_release_test.go:202: OBSERVATION after error transition: retired interceptors closed=1, server filter Close() calls=1, cache refcounts: probe=0
    verify_c2_rds_error_release_test.go:206: OBSERVATION after shutdown: server filter Close() calls=1, cache refcounts: probe=0
    verify_c2_rds_error_release_test.go:221: OBSERVATION cache refcounts after success #1: probe=1
    verify_c2_rds_error_release_test.go:223: OBSERVATION cache refcounts after error #1:   probe=0 (filter Close() calls=1)
    verify_c2_rds_error_release_test.go:225: OBSERVATION cache refcounts after error #2:   probe=0 (filter Close() calls=1)
    verify_c2_rds_error_release_test.go:228: OBSERVATION cache refcounts after success #2: probe=1 (filter Close() calls=1)
    verify_c2_rds_error_release_test.go:230: OBSERVATION cache refcounts after shutdown:   probe=0 (filter Close() calls=2)
    verify_c2_rds_error_release_test.go:231:   #00 filter-build F1
    verify_c2_rds_error_release_test.go:231:   #01 icpt-build F1/I1 filterAlreadyClosed=false
    verify_c2_rds_error_release_test.go:231:   #02 icpt-close F1/I1
    verify_c2_rds_error_release_test.go:231:   #03 filter-close F1
    verify_c2_rds_error_release_test.go:231:   #04 icpt-build F1/I2 filterAlreadyClosed=true
    verify_c2_rds_error_release_test.go:231:   #05 icpt-close F1/I2
    verify_c2_rds_error_release_test.go:231:   #06 filter-close F1
--- PASS: Test (0.00s)
    --- PASS: Test/Verify_C2_RDSErrorAfterSuccess_ReleasesFilterHold (0.00s)
    --- PASS: Test/Verify_C2_RDSErrorAfterSuccess_ThenRecovery (0.00s)
PASS
ok  	google.golang.org/grpc/internal/xds/server	1.042s
go test exit=0
```

On the control the error transition drops the count to 0 and closes the filter (`filter-close F1` inside the error transition). Note the last block: after that release the next successful update builds its interceptor from the same, already closed filter (`icpt-build F1/I2 filterAlreadyClosed=true`) — the behavior examined under C4/C9. On the five C2 target branches the retained hold is exactly what keeps the filter alive across the error state.

### Impact reasoning (C2)

What is observable on the five target branches: after a served RouteConfiguration turns into an error (resource removed / error before a usable update), the filter chain keeps one reference per server filter of the configuration it just retired. The amount retained is bounded (one reference per filter per filter chain, not growing with further errors or updates — refcount stayed 1 across two errors) and it is released by the next successful update or by listener shutdown. A filter whose state should be torn down when no configuration uses it stays alive for as long as the route configuration remains in error. It is not the per-update heap growth the task is about.

## C3

Claim: same-name RDS replacement causes an active RPC to invoke an interceptor after that interceptor has been closed. Parts: *retirement mechanism* (`applyConfiguration` closes a retired configuration's interceptors without waiting for active traversals) and *active traversal trigger* (an RPC paused in the first interceptor resumes into a second interceptor that retirement already closed). Target: [grpc-go-xds-rds-interceptor-lifecycle-leak-perfect](https://github.com/kaitranntt-evals/grpc-go/tree/grpc-go-xds-rds-interceptor-lifecycle-leak-perfect).

### Retirement mechanism (source trace + observation)

`sed -n 449,464p internal/xds/server/filter_chain_manager.go` and `sed -n 190,198p …` on [grpc-go-xds-rds-interceptor-lifecycle-leak-perfect](https://github.com/kaitranntt-evals/grpc-go/tree/grpc-go-xds-rds-interceptor-lifecycle-leak-perfect) (`614cb7398c43bf270878ac15f34b68f5a9a4159d`):

```go
func (fc *filterChain) applyConfiguration(urc *usableRouteConfiguration, serverFilters []httpfilter.ServerFilter) {
	// Swap in the new configuration first so new RPCs use it immediately.
	oldURC := fc.usableRouteConfiguration.Swap(urc)
	oldFilters := fc.serverFilters
	fc.serverFilters = serverFilters

	// Stop the old interceptors before releasing the filters they might depend on.
	if oldURC != nil {
		oldURC.stop()
	}

	// Release references to old server filters.
	for _, sf := range oldFilters {
		sf.Close()
	}
}
func (rc *usableRouteConfiguration) stop() {
	for _, vh := range rc.vhs {
		for _, r := range vh.routes {
			if r.interceptor != nil {
				r.interceptor.Close()
			}
		}
	}
}
```

`RouteAndProcess` (`internal/xds/server/routing.go:46`) takes a snapshot with `rc := cw.urc.Load()` and later calls `rwi.interceptor.AllowRPC(ctx)` (line 95), which is `interceptorList.AllowRPC` iterating the route's interceptors in order. There is no reference count, lock, or wait between an in-flight traversal of the old snapshot and `oldURC.stop()`.

### Runtime trigger

`TestVerify_PausedRPC_SameNameRDSReplacement` in `verify/repro/_verify_e2e_probe_test.go`: the server's HCM has two probe HTTP filters, `first` (its interceptor blocks inside `AllowRPC` on a gate) and `second`. One RPC is started and held inside the first interceptor; a new version of the RouteConfiguration with the same name is pushed; the test waits until the replacement's interceptors are built, gives retirement up to 3s to close the old ones, then releases the gate.

```sh
cd ~/repos/grpc-go    # verify/grpc-go-xds-rds-interceptor-lifecycle-leak-v-20a07696 == origin/grpc-go-xds-rds-interceptor-lifecycle-leak-perfect + verify/
cp verify/repro/_verify_e2e_probe_test.go test/xds/verify_e2e_probe_test.go
go test -race -v -count=1 ./test/xds -run '^Test$/^Verify_PausedRPC_SameNameRDSReplacement$'
rm test/xds/verify_e2e_probe_test.go
```

```console
    verify_e2e_probe_test.go:351: RPC paused inside interceptor F1/I1(gate)
    verify_e2e_probe_test.go:360: ---- events while the RPC is still paused in the first interceptor (13 events) ----
    verify_e2e_probe_test.go:360:   #00 filter-build F1
    verify_e2e_probe_test.go:360:   #01 icpt-build F1/I1(gate) filterAlreadyClosed=false
    verify_e2e_probe_test.go:360:   #02 filter-build F2
    verify_e2e_probe_test.go:360:   #03 icpt-build F2/I1(plain) filterAlreadyClosed=false
    verify_e2e_probe_test.go:360:   #04 allow-rpc F1/I1(gate) interceptorAlreadyClosed=false filterAlreadyClosed=false
    verify_e2e_probe_test.go:360:   #05 allow-rpc F2/I1(plain) interceptorAlreadyClosed=false filterAlreadyClosed=false
    verify_e2e_probe_test.go:360:   #06 allow-rpc F1/I1(gate) interceptorAlreadyClosed=false filterAlreadyClosed=false
    verify_e2e_probe_test.go:360:   #07 icpt-build F1/I2(gate) filterAlreadyClosed=false
    verify_e2e_probe_test.go:360:   #08 icpt-build F2/I2(plain) filterAlreadyClosed=false
    verify_e2e_probe_test.go:360:   #09 icpt-build F1/I3(gate) filterAlreadyClosed=false
    verify_e2e_probe_test.go:360:   #10 icpt-build F2/I3(plain) filterAlreadyClosed=false
    verify_e2e_probe_test.go:360:   #11 icpt-close F1/I1(gate)
    verify_e2e_probe_test.go:360:   #12 icpt-close F2/I1(plain)
    verify_e2e_probe_test.go:361: OBSERVATION retirement mechanism: old interceptors closed while RPC paused = true
    verify_e2e_probe_test.go:367: paused RPC finished with err=<nil>
    verify_e2e_probe_test.go:373: ---- events after the RPC resumed (15 events) ----
    verify_e2e_probe_test.go:373:   #00 filter-build F1
    verify_e2e_probe_test.go:373:   #01 icpt-build F1/I1(gate) filterAlreadyClosed=false
    verify_e2e_probe_test.go:373:   #02 filter-build F2
    verify_e2e_probe_test.go:373:   #03 icpt-build F2/I1(plain) filterAlreadyClosed=false
    verify_e2e_probe_test.go:373:   #04 allow-rpc F1/I1(gate) interceptorAlreadyClosed=false filterAlreadyClosed=false
    verify_e2e_probe_test.go:373:   #05 allow-rpc F2/I1(plain) interceptorAlreadyClosed=false filterAlreadyClosed=false
    verify_e2e_probe_test.go:373:   #06 allow-rpc F1/I1(gate) interceptorAlreadyClosed=false filterAlreadyClosed=false
    verify_e2e_probe_test.go:373:   #07 icpt-build F1/I2(gate) filterAlreadyClosed=false
    verify_e2e_probe_test.go:373:   #08 icpt-build F2/I2(plain) filterAlreadyClosed=false
    verify_e2e_probe_test.go:373:   #09 icpt-build F1/I3(gate) filterAlreadyClosed=false
    verify_e2e_probe_test.go:373:   #10 icpt-build F2/I3(plain) filterAlreadyClosed=false
    verify_e2e_probe_test.go:373:   #11 icpt-close F1/I1(gate)
    verify_e2e_probe_test.go:373:   #12 icpt-close F2/I1(plain)
    verify_e2e_probe_test.go:373:   #13 allow-rpc-resume F1/I1(gate)
    verify_e2e_probe_test.go:373:   #14 allow-rpc F2/I1(plain) interceptorAlreadyClosed=true filterAlreadyClosed=false
    verify_e2e_probe_test.go:379: OBSERVATION active traversal trigger: allow-rpc F2/I1(plain) interceptorAlreadyClosed=true filterAlreadyClosed=false
    verify_e2e_probe_test.go:383: PROBLEM REPRODUCED: an interceptor's AllowRPC was invoked after its Close()
    --- FAIL: Test/Verify_PausedRPC_SameNameRDSReplacement (0.03s)
```

Reading the log: `F1/I1(gate)` and `F2/I1(plain)` are the two interceptors of the route on the client's filter chain in the original configuration. While the RPC is still parked inside `F1/I1` (#06), the replacement is built (#07–#10) and both old interceptors are closed (#11, #12). When the gate is released the same RPC proceeds to the second interceptor of the old snapshot: `#14 allow-rpc F2/I1(plain) interceptorAlreadyClosed=true`. The RPC returned `err=<nil>` (the probe interceptor does not enforce anything on use-after-close; it only records it).

Repeat (`-count=20` with `-run '^Test$/^Verify_(DisableThenReenableFilter|EmptyRouteConfigThenRestore|PausedRPC_SameNameRDSReplacement)$'`): `20 × verify_e2e_probe_test.go:383: PROBLEM REPRODUCED: an interceptor's AllowRPC was invoked after its Close()`, `20 × --- FAIL: Test/Verify_PausedRPC_SameNameRDSReplacement`, 0 `DATA RACE` reports.

Control on the task base commit `4ee6ac46` (`~/wt/base`, same probe, same command):

```console
    verify_e2e_probe_test.go:351: RPC paused inside interceptor F1/I1(gate)
    verify_e2e_probe_test.go:361: OBSERVATION retirement mechanism: old interceptors closed while RPC paused = false
    verify_e2e_probe_test.go:367: paused RPC finished with err=<nil>
    verify_e2e_probe_test.go:373:   #11 allow-rpc-resume F1/I1(gate)
    verify_e2e_probe_test.go:373:   #12 allow-rpc F2/I1(plain) interceptorAlreadyClosed=false filterAlreadyClosed=false
    verify_e2e_probe_test.go:385: OBSERVATION: no AllowRPC call was made on a closed interceptor
    --- PASS: Test/Verify_PausedRPC_SameNameRDSReplacement (5.04s)
```

(The test takes 5s there because the probe waits its full 3s window for a retirement that never happens.) On the base commit nothing is ever closed (that is the leak the task fixes), so no use-after-close occurs there; the behavior is introduced together with the retirement of superseded interceptors.

### Impact reasoning (C3)

Trigger: an RPC that is inside (or between) the per-route interceptors at the moment an RDS update for the route configuration it is using is applied — an ordinary situation on a busy server receiving route updates, needing no unusual configuration beyond a route with at least one interceptor that has not run yet. Effect observed: the remaining interceptors of the old configuration are invoked after their `Close()`. `internal/resolver.ServerInterceptor` documents `Close` as "Once called, no new calls … are accepted. Ongoing calls … are allowed to complete." The in-tree server interceptors (`rbac`, and `router` which builds none) have empty `Close()` methods (`grep -rn "func (.*) Close()" internal/xds/httpfilter`), so with today's filters the RPC still gets a correct answer; any interceptor that releases state in `Close()` would be used after release, and the authorization decision of that RPC would come from a closed object. The eval fixture's six `Eval_ServerSideXDS_*` tests pass on this branch, so none of them exercises this ordering. No workaround at the configuration level was observed.

## C4

Claim: a successful RDS disable-and-re-enable sequence constructs interceptors from a server filter that has already been closed. Parts: *cache reuse mechanism* (`getOrCreateServerFilterWithMap` returns a cached filter after its final release and closure because `incRef` accepts the closed entry) and *reactivation trigger* (the sequence leaves a closed filter in the cache and retrieves it for the re-enabled configuration). Target: [grpc-go-xds-rds-interceptor-lifecycle-leak-perfect](https://github.com/kaitranntt-evals/grpc-go/tree/grpc-go-xds-rds-interceptor-lifecycle-leak-perfect).

### Cache lookup / reference acquisition (direct lookup, recorded separately)

`sed -n 408,421p internal/xds/server/listener_wrapper.go` and `sed -n 562,573p internal/xds/server/filter_chain_manager.go` on [grpc-go-xds-rds-interceptor-lifecycle-leak-perfect](https://github.com/kaitranntt-evals/grpc-go/tree/grpc-go-xds-rds-interceptor-lifecycle-leak-perfect) (`614cb7398c43bf270878ac15f34b68f5a9a4159d`):

```go
// custom map.
func getOrCreateServerFilterWithMap(httpFilters map[serverFilterKey]*refCountedServerFilter, builder httpfilter.ServerFilterBuilder, key serverFilterKey) httpfilter.ServerFilter {
	serverFilter, ok := httpFilters[key]
	if ok {
		serverFilter.incRef()
		return serverFilter
	}

	sf := builder.BuildServerFilter()
	serverFilter = &refCountedServerFilter{ServerFilter: sf}
	httpFilters[key] = serverFilter
	serverFilter.incRef()
	return serverFilter
}
}

func (il *interceptorList) Close() {
	for _, i := range il.interceptors {
		i.Close()
	}
}

// refCountedServerFilter wraps a ServerFilter along with a reference count.
// This is used to manage server filters that are shared across filter chains
// within a filter chain manager.
type refCountedServerFilter struct {
```

The final `Close()` closes the wrapped filter but nothing removes the map entry, and the lookup path does not check for a zero count before `incRef()`. Direct probe (`verify/repro/_verify_closed_filter_reacquire_test.go`):

```sh
cd ~/repos/grpc-go
cp verify/repro/_verify_closed_filter_reacquire_test.go internal/xds/server/verify_closed_filter_reacquire_test.go
go test -race -v -count=1 ./internal/xds/server -run '^Test$/^Verify_ClosedCacheEntryReacquired$'
rm internal/xds/server/verify_closed_filter_reacquire_test.go
```

```console
    verify_closed_filter_reacquire_test.go:64: OBSERVATION after first acquisition: refCnt=1 closed=false filtersBuilt=1
    verify_closed_filter_reacquire_test.go:68: OBSERVATION after final release:     refCnt=0 closed=true entryStillInCache=true
    verify_closed_filter_reacquire_test.go:73: OBSERVATION after second acquisition: sameInstance=true refCnt=1 closed=true filtersBuilt=1
    verify_closed_filter_reacquire_test.go:76: PROBLEM REPRODUCED: lookup re-acquired the cached entry after its final release; the returned server filter is already closed (no new filter was built: filtersBuilt=1)
--- FAIL: Test (0.00s)
    --- FAIL: Test/Verify_ClosedCacheEntryReacquired (0.00s)
FAIL
FAIL	google.golang.org/grpc/internal/xds/server	0.036s
FAIL
go test exit=1
```

The same direct probe prints the same four lines on the task base commit `4ee6ac46` (`~/wt/base`): the lookup primitive is unchanged by the solution.

### Disable → re-enable through RDS (runtime trigger)

`verify/repro/_verify_e2e_probe_test.go`, real xDS server + management server + client, one probe HTTP filter named `probe` in the HCM, RouteConfiguration delivered through RDS:

- `TestVerify_DisableThenReenableFilter` — update 1 disables the filter on all routes with a virtual-host-level `typed_per_filter_config` of `envoy.config.route.v3.FilterConfig{disabled: true}` (honoured under `GRPC_EXPERIMENTAL_XDS_EXT_PROC_ON_CLIENT`, the same switch the repo's own `TestServerSideXDS_FilterOverride_Disabled` sets); the test waits until an RPC succeeds without reaching the probe interceptor and the retired interceptor is closed; update 2 removes the override again and the test waits until an RPC is served through the probe filter. Both updates are accepted and RPCs succeed after each.
- `TestVerify_EmptyRouteConfigThenRestore` — same shape, but the filter's last reference is dropped by an RDS update with no virtual hosts (no experimental switch).

```sh
cd ~/repos/grpc-go
cp verify/repro/_verify_e2e_probe_test.go test/xds/verify_e2e_probe_test.go
go test -race -v -count=1 ./test/xds -run '^Test$/^Verify_(DisableThenReenableFilter|EmptyRouteConfigThenRestore)$'
rm test/xds/verify_e2e_probe_test.go
```

Output of `TestVerify_DisableThenReenableFilter`:

```console
    verify_e2e_probe_test.go:421: ---- after initial configuration + 1 RPC (3 events) ----
    verify_e2e_probe_test.go:453: ---- after the RDS update that removes the filter from every route (5 events) ----
    verify_e2e_probe_test.go:453:   #00 filter-build F1
    verify_e2e_probe_test.go:453:   #01 icpt-build F1/I1(plain) filterAlreadyClosed=false
    verify_e2e_probe_test.go:453:   #02 allow-rpc F1/I1(plain) interceptorAlreadyClosed=false filterAlreadyClosed=false
    verify_e2e_probe_test.go:453:   #03 icpt-close F1/I1(plain)
    verify_e2e_probe_test.go:453:   #04 filter-close F1
    verify_e2e_probe_test.go:454: OBSERVATION disable transition closed the server filter = true
    verify_e2e_probe_test.go:465: OBSERVATION ordering: first filter-close at #4, first retired icpt-close at #3, filterDestroyedBeforeInterceptorClosed=false
    verify_e2e_probe_test.go:484: ---- after the RDS update re-enabling the filter + 1 RPC (8 events) ----
    verify_e2e_probe_test.go:490: OBSERVATION reactivation: icpt-build F1/I2(plain) filterAlreadyClosed=true
    verify_e2e_probe_test.go:490: OBSERVATION reactivation: icpt-build F1/I3(plain) filterAlreadyClosed=true
    verify_e2e_probe_test.go:493: OBSERVATION filters built in total = 1
    verify_e2e_probe_test.go:496: ---- after server stop (11 events) ----
    verify_e2e_probe_test.go:496:   #00 filter-build F1
    verify_e2e_probe_test.go:496:   #01 icpt-build F1/I1(plain) filterAlreadyClosed=false
    verify_e2e_probe_test.go:496:   #02 allow-rpc F1/I1(plain) interceptorAlreadyClosed=false filterAlreadyClosed=false
    verify_e2e_probe_test.go:496:   #03 icpt-close F1/I1(plain)
    verify_e2e_probe_test.go:496:   #04 filter-close F1
    verify_e2e_probe_test.go:496:   #05 icpt-build F1/I2(plain) filterAlreadyClosed=true
    verify_e2e_probe_test.go:496:   #06 icpt-build F1/I3(plain) filterAlreadyClosed=true
    verify_e2e_probe_test.go:496:   #07 allow-rpc F1/I3(plain) interceptorAlreadyClosed=false filterAlreadyClosed=true
    verify_e2e_probe_test.go:496:   #08 icpt-close F1/I2(plain)
    verify_e2e_probe_test.go:496:   #09 icpt-close F1/I3(plain)
    verify_e2e_probe_test.go:496:   #10 filter-close F1
    verify_e2e_probe_test.go:502: PROBLEM REPRODUCED (closed filter reuse): the re-enabled configuration built interceptors from a server filter that had already been closed
    --- FAIL: Test/Verify_DisableThenReenableFilter (0.05s)
```

Output of `TestVerify_EmptyRouteConfigThenRestore` (final event log and observations):

```console
    verify_e2e_probe_test.go:454: OBSERVATION disable transition closed the server filter = true
    verify_e2e_probe_test.go:465: OBSERVATION ordering: first filter-close at #5, first retired icpt-close at #4, filterDestroyedBeforeInterceptorClosed=false
    verify_e2e_probe_test.go:490: OBSERVATION reactivation: icpt-build F1/I2(plain) filterAlreadyClosed=true
    verify_e2e_probe_test.go:490: OBSERVATION reactivation: icpt-build F1/I3(plain) filterAlreadyClosed=true
    verify_e2e_probe_test.go:493: OBSERVATION filters built in total = 1
    verify_e2e_probe_test.go:496: ---- after server stop (12 events) ----
    verify_e2e_probe_test.go:496:   #00 filter-build F1
    verify_e2e_probe_test.go:496:   #01 icpt-build F1/I1(plain) filterAlreadyClosed=false
    verify_e2e_probe_test.go:496:   #02 allow-rpc F1/I1(plain) interceptorAlreadyClosed=false filterAlreadyClosed=false
    verify_e2e_probe_test.go:496:   #03 allow-rpc F1/I1(plain) interceptorAlreadyClosed=false filterAlreadyClosed=false
    verify_e2e_probe_test.go:496:   #04 icpt-close F1/I1(plain)
    verify_e2e_probe_test.go:496:   #05 filter-close F1
    verify_e2e_probe_test.go:496:   #06 icpt-build F1/I2(plain) filterAlreadyClosed=true
    verify_e2e_probe_test.go:496:   #07 icpt-build F1/I3(plain) filterAlreadyClosed=true
    verify_e2e_probe_test.go:496:   #08 allow-rpc F1/I3(plain) interceptorAlreadyClosed=false filterAlreadyClosed=true
    verify_e2e_probe_test.go:496:   #09 icpt-close F1/I2(plain)
    verify_e2e_probe_test.go:496:   #10 icpt-close F1/I3(plain)
    verify_e2e_probe_test.go:496:   #11 filter-close F1
    verify_e2e_probe_test.go:502: PROBLEM REPRODUCED (closed filter reuse): the re-enabled configuration built interceptors from a server filter that had already been closed
    --- FAIL: Test/Verify_EmptyRouteConfigThenRestore (0.04s)
```

Reading the log: the disable transition closes the retired interceptor and then the filter (`icpt-close F1/I1`, `filter-close F1`; interceptor first, so no ordering problem on this branch). The re-enable transition builds no new filter (`filters built in total = 1`): it retrieves `F1` again and builds both filter chains' interceptors from it while it is closed (`icpt-build F1/I2(plain) filterAlreadyClosed=true`, `icpt-build F1/I3(plain) filterAlreadyClosed=true`), and RPCs are then served through them (`allow-rpc F1/I3(plain) … filterAlreadyClosed=true`). At server stop `F1.Close()` is called a second time (`filter-close F1` appears twice).

Repeat (`-count=20`): `40 × verify_e2e_probe_test.go:502: PROBLEM REPRODUCED (closed filter reuse)…` (20 per test), `20 × --- FAIL` for each of the two tests, 0 `DATA RACE` reports.

### Impact reasoning (C4)

Trigger: a filter present in the Listener's HCM loses its last reference through an RDS update (disabled on every route, or a RouteConfiguration with no virtual hosts / an RDS resource error, see the C2 control run where the audited branch shows `icpt-build F1/I2 filterAlreadyClosed=true` after error → success) and a later accepted RDS update uses it again. No LDS update is involved in the sequence. From then on (as far as the probe ran) every interceptor for that filter on that listener is built by a filter object whose `Close()` has already run, the filter never gets a fresh instance, and `Close()` runs again at shutdown (double close). The in-tree server filters (`rbac`, `router`) have empty `Close()` methods, so nothing visible happens with them today; a stateful filter that releases resources in `Close()` — the case the reference-counted cache exists for — would serve RPCs from released state. The lookup primitive is the same on the base commit; what the solution adds is the release of filter references on these RDS transitions without invalidating the cache entry. The eval fixture's six tests pass on this branch, so none covers this sequence.

## C5

Claim: forced server shutdown deadlocks when an active route interceptor waits for its RPC context to be canceled. Parts: *routing lock retention* and *shutdown cancellation dependency*. Target: [evalon/grpc-go-xd-20ecf5d0](https://github.com/kaitranntt-evals/grpc-go-xds-rds-interceptor-lifecycle-leak/tree/evalon/grpc-go-xd-20ecf5d0) (`7694180ad6ed8b3fa63c1c9dd898d7c47a615092`, worktree `~/wt/20ecf5d0`).

### Source trace on the target branch

`sed -n 46,47p internal/xds/server/routing.go; sed -n 95,97p internal/xds/server/routing.go; sed -n 413,421p internal/xds/server/filter_chain_manager.go; sed -n 1946,1965p server.go`:

```go
	cw.urc.mu.RLock()
	defer cw.urc.mu.RUnlock()
	// ...
		return rc.statusErrWithNodeID(codes.Unavailable, "the incoming RPC did not match a configured Route")
	}
	if err := rwi.interceptor.AllowRPC(ctx); err != nil {

func (fc *filterChain) stop() {
	// Retain the routing table for connections that are being drained after an
	// LDS update. Such connections can still receive RPCs until draining takes
	// effect, and must not fail them because the filter chain has stopped.
	routing := fc.usableRouteConfiguration
	routing.mu.Lock()
	defer routing.mu.Unlock()
	routing.config.close()
}

func (s *Server) stop(graceful bool) {
	s.quit.Fire()
	defer s.done.Fire()

	s.channelzRemoveOnce.Do(func() { channelz.RemoveEntry(s.channelz.ID) })
	s.mu.Lock()
	s.closeListenersLocked()
	// Wait for serving threads to be ready to exit.  Only then can we be sure no
	// new conns will be created.
	s.mu.Unlock()
	s.serveWG.Wait()

	s.mu.Lock()
	defer s.mu.Unlock()

	if graceful {
		s.drainAllServerTransportsLocked()
	} else {
		s.closeServerTransportsLocked()
	}
```

### Runtime

`TestVerify_ForcedStopWithContextWaitingInterceptor` in `verify/repro/_verify_e2e_probe_test.go`: one probe HTTP filter whose interceptor, once armed, blocks in `AllowRPC` on `<-ctx.Done()` of the RPC context. A client RPC with no deadline shorter than the test is started; once it is inside the interceptor, `xds.GRPCServer.Stop()` (forced, not graceful) is called on another goroutine with an external deadline of 10s. If `Stop()` has not returned by then the test dumps all goroutine stacks and then cancels the RPC from the client side to see whether that releases shutdown.

```sh
cd ~/wt/20ecf5d0
cp ~/repos/grpc-go/verify/repro/_verify_e2e_probe_test.go test/xds/verify_e2e_probe_test.go
VERIFY_STACK_FILE=/tmp/c5_stacks.txt go test -race -v -count=1 ./test/xds -run '^Test$/^Verify_ForcedStopWithContextWaitingInterceptor$'    # and again with -count=3
rm test/xds/verify_e2e_probe_test.go
```

```console
    verify_e2e_probe_test.go:532: RPC is inside interceptor F1/I1(ctxwait), waiting for ctx.Done()
    verify_e2e_probe_test.go:559: PROBLEM REPRODUCED: forced Stop() still blocked after external deadline of 10s
    verify_e2e_probe_test.go:570: ---- events while Stop() is blocked (4 events) ----
    verify_e2e_probe_test.go:570:   #00 filter-build F1
    verify_e2e_probe_test.go:570:   #01 icpt-build F1/I1(ctxwait) filterAlreadyClosed=false
    verify_e2e_probe_test.go:570:   #02 allow-rpc F1/I1(ctxwait) interceptorAlreadyClosed=false filterAlreadyClosed=false
    verify_e2e_probe_test.go:570:   #03 allow-rpc F1/I1(ctxwait) interceptorAlreadyClosed=false filterAlreadyClosed=false
    verify_e2e_probe_test.go:577: OBSERVATION: Stop() returned 10.048s after start, only once the CLIENT cancelled the RPC
    verify_e2e_probe_test.go:581: ---- final events (7 events) ----
    verify_e2e_probe_test.go:581:   #00 filter-build F1
    verify_e2e_probe_test.go:581:   #01 icpt-build F1/I1(ctxwait) filterAlreadyClosed=false
    verify_e2e_probe_test.go:581:   #02 allow-rpc F1/I1(ctxwait) interceptorAlreadyClosed=false filterAlreadyClosed=false
    verify_e2e_probe_test.go:581:   #03 allow-rpc F1/I1(ctxwait) interceptorAlreadyClosed=false filterAlreadyClosed=false
    verify_e2e_probe_test.go:581:   #04 allow-rpc-ctx-done F1/I1(ctxwait) err=context canceled
    verify_e2e_probe_test.go:581:   #05 icpt-close F1/I1(ctxwait)
    verify_e2e_probe_test.go:581:   #06 filter-close F1
--- FAIL: Test (10.09s)
    --- FAIL: Test/Verify_ForcedStopWithContextWaitingInterceptor (10.08s)
FAIL
FAIL	google.golang.org/grpc/test/xds	10.117s
FAIL
go test exit=1
```

The two goroutines that form the cycle, from the stack dump taken while `Stop()` was blocked (complete, unedited frames):

```console
goroutine 86 [chan receive]:
google.golang.org/grpc/test/xds_test.(*vpInterceptor).AllowRPC(0xc000683dd0, {0x1f6b910, 0xc0005fc750})
	/home/ubuntu/wt/20ecf5d0/test/xds/verify_e2e_probe_test.go:164 +0x570
google.golang.org/grpc/internal/xds/server.(*interceptorList).AllowRPC(0xc0005a8d98, {0x1f6b910, 0xc0005fc750})
	/home/ubuntu/wt/20ecf5d0/internal/xds/server/filter_chain_manager.go:536 +0xa2
google.golang.org/grpc/internal/xds/server.RouteAndProcess({0x1f6b910, 0xc0005fc750})
	/home/ubuntu/wt/20ecf5d0/internal/xds/server/routing.go:97 +0xba3
google.golang.org/grpc/xds.xdsUnaryInterceptor({0x1f6b910, 0xc0005fc750}, {0x1b9ed40, 0xc0005fc780}, 0xc0005fc750?, 0xc0005a9f38)
	/home/ubuntu/wt/20ecf5d0/xds/server.go:236 +0x47
google.golang.org/grpc/interop/grpc_testing._TestService_EmptyCall_Handler({0x1ccbc00, 0xc00050ee40}, {0x1f6b910, 0xc0005fc750}, 0xc00079c000, 0x1dc8320)
	/home/ubuntu/wt/20ecf5d0/interop/grpc_testing/test_grpc.pb.go:293 +0x1e7
google.golang.org/grpc.(*Server).processUnaryRPC(0xc000521688, {0x1f6b910, 0xc0005fc6c0}, 0xc0006661a0, 0xc00003f3e0, 0x2b0bca0, 0x0)
	/home/ubuntu/wt/20ecf5d0/server.go:1441 +0x1a6a
google.golang.org/grpc.(*Server).handleStream(0xc000521688, {0x1f708f8, 0xc0004369c0}, 0xc0006661a0)
	/home/ubuntu/wt/20ecf5d0/server.go:1850 +0x10b3
google.golang.org/grpc.(*Server).serveStreams.func2.1()
	/home/ubuntu/wt/20ecf5d0/server.go:1076 +0x14a
created by google.golang.org/grpc.(*Server).serveStreams.func2 in goroutine 72
	/home/ubuntu/wt/20ecf5d0/server.go:1087 +0x213

goroutine 47 [sync.RWMutex.Lock]:
sync.runtime_SemacquireRWMutex(0xc0000ad4b4?, 0x1?, 0x1499ac5?)
	/usr/local/go/src/runtime/sema.go:105 +0x25
sync.(*RWMutex).Lock(0xc0000ad4a0)
	/usr/local/go/src/sync/rwmutex.go:155 +0x89
google.golang.org/grpc/internal/xds/server.(*filterChain).stop(0xc0006bcb80)
	/home/ubuntu/wt/20ecf5d0/internal/xds/server/filter_chain_manager.go:418 +0x4a
google.golang.org/grpc/internal/xds/server.(*filterChainManager).stop(...)
	/home/ubuntu/wt/20ecf5d0/internal/xds/server/filter_chain_manager.go:125
google.golang.org/grpc/internal/xds/server.(*listenerWrapper).Close(0xc00050efc0)
	/home/ubuntu/wt/20ecf5d0/internal/xds/server/listener_wrapper.go:371 +0x185
google.golang.org/grpc.(*listenSocket).Close(0xc0000120f0)
	/home/ubuntu/wt/20ecf5d0/server.go:862 +0x4b
google.golang.org/grpc.(*Server).closeListenersLocked(...)
	/home/ubuntu/wt/20ecf5d0/server.go:2014
google.golang.org/grpc.(*Server).stop(0xc000521688, 0x0)
	/home/ubuntu/wt/20ecf5d0/server.go:1952 +0x25a
google.golang.org/grpc.(*Server).Stop(0xc000521688)
	/home/ubuntu/wt/20ecf5d0/server.go:1936 +0x29
google.golang.org/grpc/xds.(*GRPCServer).Stop(0xc000517880)
	/home/ubuntu/wt/20ecf5d0/xds/server.go:216 +0x8f
google.golang.org/grpc/test/xds_test.setupGRPCServer.func4()
	/home/ubuntu/wt/20ecf5d0/test/xds/xds_server_integration_test.go:147 +0x48
google.golang.org/grpc/test/xds_test.s.TestVerify_ForcedStopWithContextWaitingInterceptor.func2()
	/home/ubuntu/wt/20ecf5d0/test/xds/verify_e2e_probe_test.go:540 +0x4d
created by google.golang.org/grpc/test/xds_test.s.TestVerify_ForcedStopWithContextWaitingInterceptor in goroutine 9
	/home/ubuntu/wt/20ecf5d0/test/xds/verify_e2e_probe_test.go:539 +0x96b
```

Reading it:

- *routing lock retention* — goroutine 86 is the RPC: `RouteAndProcess` (`routing.go:97`, after `cw.urc.mu.RLock()` at line 46 with the unlock deferred) → `interceptorList.AllowRPC` → the interceptor, parked in `chan receive` on `ctx.Done()`.
- *shutdown cancellation dependency* — goroutine 47 is `Server.Stop` → `Server.stop` (`server.go:1952`, `s.closeListenersLocked()`) → `listenSocket.Close` → `listenerWrapper.Close` → `filterChainManager.stop` → `filterChain.stop` (`filter_chain_manager.go:418`, `routing.mu.Lock()`), state `sync.RWMutex.Lock`. `closeServerTransportsLocked()`, which is what cancels the stream contexts, is at `server.go:1964`, after the call that is blocked.
- Before the client cancels, the event log has no `allow-rpc-ctx-done`, `icpt-close` or `filter-close` entry. `Stop()` returned at 10.048s, i.e. only after the test cancelled the RPC from the client side at the 10s mark (`#04 allow-rpc-ctx-done … err=context canceled`, then `icpt-close`, `filter-close`).

Repeat with `-count=3`:

```console
    verify_e2e_probe_test.go:559: PROBLEM REPRODUCED: forced Stop() still blocked after external deadline of 10s
    verify_e2e_probe_test.go:577: OBSERVATION: Stop() returned 10.046s after start, only once the CLIENT cancelled the RPC
--- FAIL: Test (10.09s)
    verify_e2e_probe_test.go:559: PROBLEM REPRODUCED: forced Stop() still blocked after external deadline of 10s
    verify_e2e_probe_test.go:577: OBSERVATION: Stop() returned 10.048s after start, only once the CLIENT cancelled the RPC
--- FAIL: Test (10.08s)
    verify_e2e_probe_test.go:559: PROBLEM REPRODUCED: forced Stop() still blocked after external deadline of 10s
    verify_e2e_probe_test.go:577: OBSERVATION: Stop() returned 10.005s after start, only once the CLIENT cancelled the RPC
--- FAIL: Test (10.03s)
FAIL	google.golang.org/grpc/test/xds	30.223s
exit=1
```

Control — the identical probe on [grpc-go-xds-rds-interceptor-lifecycle-leak-perfect](https://github.com/kaitranntt-evals/grpc-go/tree/grpc-go-xds-rds-interceptor-lifecycle-leak-perfect) (`614cb7398c43bf270878ac15f34b68f5a9a4159d`), where `RouteAndProcess` reads the configuration with an atomic load and takes no lock:

```console
    verify_e2e_probe_test.go:532: RPC is inside interceptor F1/I1(ctxwait), waiting for ctx.Done()
    verify_e2e_probe_test.go:547: OBSERVATION: forced Stop() returned after 1ms
    verify_e2e_probe_test.go:550: client RPC ended with err=rpc error: code = Unavailable desc = error reading from server: EOF
    verify_e2e_probe_test.go:554:   #04 icpt-close F1/I1(ctxwait)
    verify_e2e_probe_test.go:554:   #05 filter-close F1
    verify_e2e_probe_test.go:554:   #06 allow-rpc-ctx-done F1/I1(ctxwait) err=context canceled
--- PASS: Test (0.04s)
    --- PASS: Test/Verify_ForcedStopWithContextWaitingInterceptor (0.04s)
PASS
ok  	google.golang.org/grpc/test/xds	1.073s
go test exit=0
```

### Impact reasoning (C5)

On the target branch, `Stop()` on an xDS-enabled server cannot complete while any RPC is blocked inside a route interceptor's `AllowRPC`, because the write lock it needs is held (shared) by that RPC and the cancellation that would unblock the RPC is sequenced after the blocked call. In the probe the hang lasted exactly as long as the external deadline and ended only through client-side cancellation; with a client that never cancels and sets no deadline nothing on the server side breaks the cycle. The in-tree `rbac` interceptor returns immediately, so the trigger needs an interceptor that waits on the RPC context (or otherwise blocks for long), and a forced stop during that window. (Whether the same read lock also delays RDS/LDS-driven retirement was not exercised.) The audited branch does not have this behavior (control: `Stop()` returned after 1ms).

## C6

Claim: the delivered `internal/xds/server/listener_wrapper_test.go` differs from the output of `gofmt -s`. Target: [evalon/grpc-go-xd-3e71ce68](https://github.com/kaitranntt-evals/grpc-go-xds-rds-interceptor-lifecycle-leak/tree/evalon/grpc-go-xd-3e71ce68) (`e41391f5f5d98da5e5e6e5ba437dd62840e0cb9f`, worktree `~/wt/3e71ce68`, file unmodified).

```console
$ git -C ~/wt/3e71ce68 rev-parse HEAD
e41391f5f5d98da5e5e6e5ba437dd62840e0cb9f
$ git status --short internal/xds/server/listener_wrapper_test.go   (empty = unmodified)
$ gofmt -s -d -l internal/xds/server/listener_wrapper_test.go; echo "exit=$?"
exit=0
$ gofmt -s -l internal/xds/server/listener_wrapper_test.go | wc -l
0
$ gofmt -l internal/xds/server/listener_wrapper_test.go | wc -l   (without -s)
0
$ gofmt -s -l internal/xds/server test/xds | wc -l
0
$ git diff --stat 4ee6ac46 -- internal/xds/server/listener_wrapper_test.go
 internal/xds/server/listener_wrapper_test.go | 295 +++++++++++++++++++++++++++
 1 file changed, 295 insertions(+)
$ grep -n "countingFilterBuilder struct\|func newListenerWrapperForRDSTesting" internal/xds/server/listener_wrapper_test.go
38:type countingFilterBuilder struct {
118:func newListenerWrapperForRDSTesting(routeName string, filters []xdsresource.HTTPFilter) (*listenerWrapper, *filterChain) {
/usr/local/bin/gofmt
go version go1.25.7 linux/amd64
```

Control showing that this `gofmt` binary does list and diff a file that needs `-s` simplification and formatting:

```console
$ printf 'package x\n\nvar a = []struct{ b int }{struct{ b int }{1}}\nfunc  f( ) {}\n' > /tmp/gofmt_ctl.go; gofmt -s -d -l /tmp/gofmt_ctl.go
/tmp/gofmt_ctl.go
diff /tmp/gofmt_ctl.go.orig /tmp/gofmt_ctl.go
--- /tmp/gofmt_ctl.go.orig
+++ /tmp/gofmt_ctl.go
@@ -1,4 +1,5 @@
 package x
 
-var a = []struct{ b int }{struct{ b int }{1}}
-func  f( ) {}
+var a = []struct{ b int }{{1}}
+
+func f() {}
```

Observation: on the delivered file `gofmt -s -d -l` prints nothing (no file listing, no diff) and exits 0; the same holds for every Go file under `internal/xds/server` and `test/xds` on that branch. The symbols the claim points at (`countingFilterBuilder`, `newListenerWrapperForRDSTesting`) exist in the file at lines 38 and 118.

## C7

Claim: route configuration replacement destroys server filters before closing the retired interceptors that depend on them. Parts: *construction release ordering* and *destruction trigger*. Target: [evalon/grpc-go-xd-adbe1cae](https://github.com/kaitranntt-evals/grpc-go-xds-rds-interceptor-lifecycle-leak/tree/evalon/grpc-go-xd-adbe1cae) (`a8858d4ab2dc761df406377e776a971abc8bdd72`, worktree `~/wt/adbe1cae`).

### Construction release ordering (source trace)

`sed -n 411,438p internal/xds/server/filter_chain_manager.go` on the target branch — the previous configuration's filter references are released inside construction, before the caller publishes the new configuration and retires the old one's interceptors:

```go
func (fc *filterChain) constructUsableRouteConfiguration(config xdsresource.RouteConfigUpdate, provider serverFilterProvider) *usableRouteConfiguration {
	vhs := make([]virtualHostWithInterceptors, 0, len(config.VirtualHosts))
	var serverFilters []httpfilter.ServerFilter
	for _, vh := range config.VirtualHosts {
		vhwi, sfs, err := fc.convertVirtualHost(vh, provider)
		if err != nil {
			for _, vh := range vhs {
				vh.closeInterceptors()
			}
			for _, sf := range serverFilters {
				sf.Close()
			}
			// Non nil if (lds + rds) fails, shouldn't happen since validated by
			// xDS Client, treat as L7 error but shouldn't happen.
			return &usableRouteConfiguration{err: fmt.Errorf("virtual host construction: %v", err)}
		}
		vhs = append(vhs, vhwi)
		serverFilters = append(serverFilters, sfs...)
	}

	// Release references to old server filters before replacing with new ones.
	for _, sf := range fc.serverFilters {
		sf.Close()
	}
	fc.serverFilters = serverFilters

	return &usableRouteConfiguration{vhs: vhs}
}
```

### Destruction trigger (runtime)

Same end-to-end probes as used for C4/C9 (`verify/repro/_verify_e2e_probe_test.go`): a published configuration uses the `probe` filter and is its only owner; it is replaced through RDS by a configuration that no longer uses the filter (disabled on all routes / no virtual hosts). The probe records the order of `filter-close` and `icpt-close`.

```sh
cd ~/wt/adbe1cae
cp ~/repos/grpc-go/verify/repro/_verify_e2e_probe_test.go test/xds/verify_e2e_probe_test.go
go test -race -v -count=1 ./test/xds -run '^Test$/^Verify_(DisableThenReenableFilter|EmptyRouteConfigThenRestore)$'     # and again with -count=5
rm test/xds/verify_e2e_probe_test.go
```

```console
    verify_e2e_probe_test.go:421: ---- after initial configuration + 1 RPC (3 events) ----
    verify_e2e_probe_test.go:453: ---- after the RDS update that removes the filter from every route (6 events) ----
    verify_e2e_probe_test.go:453:   #00 filter-build F1
    verify_e2e_probe_test.go:453:   #01 icpt-build F1/I1(plain) filterAlreadyClosed=false
    verify_e2e_probe_test.go:453:   #02 allow-rpc F1/I1(plain) interceptorAlreadyClosed=false filterAlreadyClosed=false
    verify_e2e_probe_test.go:453:   #03 allow-rpc F1/I1(plain) interceptorAlreadyClosed=false filterAlreadyClosed=false
    verify_e2e_probe_test.go:453:   #04 filter-close F1
    verify_e2e_probe_test.go:453:   #05 icpt-close F1/I1(plain)
    verify_e2e_probe_test.go:454: OBSERVATION disable transition closed the server filter = true
    verify_e2e_probe_test.go:465: OBSERVATION ordering: first filter-close at #4, first retired icpt-close at #5, filterDestroyedBeforeInterceptorClosed=true
    ...
    verify_e2e_probe_test.go:499: PROBLEM REPRODUCED (ordering): server filter was closed before the retired interceptor that depends on it
    verify_e2e_probe_test.go:499: PROBLEM REPRODUCED (ordering): server filter was closed before the retired interceptor that depends on it
--- FAIL: Test (0.10s)
    --- FAIL: Test/Verify_DisableThenReenableFilter (0.06s)
    --- FAIL: Test/Verify_EmptyRouteConfigThenRestore (0.04s)
FAIL
FAIL	google.golang.org/grpc/test/xds	0.136s
FAIL
go test exit=1
```

Repeat with `-count=5`:

```console
      4 verify_e2e_probe_test.go:465: OBSERVATION ordering: first filter-close at #3, first retired icpt-close at #4, filterDestroyedBeforeInterceptorClosed=true
      1 verify_e2e_probe_test.go:465: OBSERVATION ordering: first filter-close at #3, first retired icpt-close at #5, filterDestroyedBeforeInterceptorClosed=true
      5 verify_e2e_probe_test.go:465: OBSERVATION ordering: first filter-close at #4, first retired icpt-close at #5, filterDestroyedBeforeInterceptorClosed=true
     10 verify_e2e_probe_test.go:499: PROBLEM REPRODUCED (ordering): server filter was closed before the retired interceptor that depends on it
```

The eval fixture measures the same ordering directly; on this branch (fixture copied byte-exact to `test/xds/eval_xds_server_interceptor_leak_test.go`, `go test -race -v -count=1 ./test/xds -run '^Test$/^Eval_ServerSideXDS_'`):

```console
    eval_xds_server_interceptor_leak_test.go:1620: dependency order violation: filter closed at event #0 before retired interceptor closed at event #1 (events: [{kind:filter-close id:F1} {kind:icpt-close id:F1/I1}])
--- FAIL: Test (0.19s)
    --- FAIL: Test/Eval_ServerSideXDS_InterceptorBeforeFilterCloseOrder (0.04s)
    --- PASS: Test/Eval_ServerSideXDS_InterceptorLeak_MultiGenerationRDSUpdate (0.03s)
    --- PASS: Test/Eval_ServerSideXDS_InterceptorLeak_RDSUpdate (0.03s)
    --- PASS: Test/Eval_ServerSideXDS_InterceptorSwapOrder (0.03s)
    --- PASS: Test/Eval_ServerSideXDS_PartialRouteFailure_ClosesInterceptors (0.03s)
    --- PASS: Test/Eval_ServerSideXDS_PartialVirtualHostFailure_ClosesInterceptors (0.03s)
FAIL
FAIL	google.golang.org/grpc/test/xds	0.225s
FAIL
go test exit=1
```

Control — same probe and same fixture on [grpc-go-xds-rds-interceptor-lifecycle-leak-perfect](https://github.com/kaitranntt-evals/grpc-go/tree/grpc-go-xds-rds-interceptor-lifecycle-leak-perfect) (`614cb7398c43bf270878ac15f34b68f5a9a4159d`): `OBSERVATION ordering: first filter-close at #4, first retired icpt-close at #3, filterDestroyedBeforeInterceptorClosed=false`, and all six `Eval_ServerSideXDS_*` tests pass.

Reading it: in every iteration `filter-close F1` precedes the `icpt-close` of the interceptor built from `F1` (the filter has no other owner: one filter, cache refcount dropped to zero by construction of the replacement). No other ownership hold prevents the destruction — the filter's `Close()` actually runs first. (The second `PROBLEM REPRODUCED (closed filter reuse)` line the probe prints on this branch is the C4/C9 behavior and is not part of this claim.)

### Impact reasoning (C7)

Whenever an RDS update removes the last use of a server filter on the target branch, the filter is closed while interceptors built from it are still published and can be serving RPCs (the old configuration is still the live one during construction of the new one), and only afterwards are those interceptors closed. For a filter that owns shared state used by its interceptors this is a use-after-close window on a live configuration plus a wrong teardown order; with the in-tree filters (`rbac`, `router`: empty `Close()`) nothing visible happens. The eval fixture `Eval_ServerSideXDS_InterceptorBeforeFilterCloseOrder` fails on this branch for exactly this reason.

## C8

Claim: route replacement between two interceptor calls causes an active request to invoke the second interceptor after closure. Parts: *immediate retirement mechanism* (`applyConfiguration` calls `oldURC.stop()` without preserving retired interceptors for active traversals) and *between-interceptor replacement trigger* (a request paused between interceptor invocations advances to an interceptor the replacement has already closed). Target: [grpc-go-xds-rds-interceptor-lifecycle-leak-perfect](https://github.com/kaitranntt-evals/grpc-go/tree/grpc-go-xds-rds-interceptor-lifecycle-leak-perfect). The evidence is the same run as C3, repeated here in full.

### Retirement mechanism (source trace + observation)

`sed -n 449,464p internal/xds/server/filter_chain_manager.go` and `sed -n 190,198p …` on [grpc-go-xds-rds-interceptor-lifecycle-leak-perfect](https://github.com/kaitranntt-evals/grpc-go/tree/grpc-go-xds-rds-interceptor-lifecycle-leak-perfect) (`614cb7398c43bf270878ac15f34b68f5a9a4159d`):

```go
func (fc *filterChain) applyConfiguration(urc *usableRouteConfiguration, serverFilters []httpfilter.ServerFilter) {
	// Swap in the new configuration first so new RPCs use it immediately.
	oldURC := fc.usableRouteConfiguration.Swap(urc)
	oldFilters := fc.serverFilters
	fc.serverFilters = serverFilters

	// Stop the old interceptors before releasing the filters they might depend on.
	if oldURC != nil {
		oldURC.stop()
	}

	// Release references to old server filters.
	for _, sf := range oldFilters {
		sf.Close()
	}
}
func (rc *usableRouteConfiguration) stop() {
	for _, vh := range rc.vhs {
		for _, r := range vh.routes {
			if r.interceptor != nil {
				r.interceptor.Close()
			}
		}
	}
}
```

`RouteAndProcess` (`internal/xds/server/routing.go:46`) takes a snapshot with `rc := cw.urc.Load()` and later calls `rwi.interceptor.AllowRPC(ctx)` (line 95), which is `interceptorList.AllowRPC` iterating the route's interceptors in order. There is no reference count, lock, or wait between an in-flight traversal of the old snapshot and `oldURC.stop()`.

### Runtime trigger

`TestVerify_PausedRPC_SameNameRDSReplacement` in `verify/repro/_verify_e2e_probe_test.go`: the server's HCM has two probe HTTP filters, `first` (its interceptor blocks inside `AllowRPC` on a gate) and `second`. One RPC is started and held inside the first interceptor; a new version of the RouteConfiguration with the same name is pushed; the test waits until the replacement's interceptors are built, gives retirement up to 3s to close the old ones, then releases the gate.

```sh
cd ~/repos/grpc-go    # verify/grpc-go-xds-rds-interceptor-lifecycle-leak-v-20a07696 == origin/grpc-go-xds-rds-interceptor-lifecycle-leak-perfect + verify/
cp verify/repro/_verify_e2e_probe_test.go test/xds/verify_e2e_probe_test.go
go test -race -v -count=1 ./test/xds -run '^Test$/^Verify_PausedRPC_SameNameRDSReplacement$'
rm test/xds/verify_e2e_probe_test.go
```

```console
    verify_e2e_probe_test.go:351: RPC paused inside interceptor F1/I1(gate)
    verify_e2e_probe_test.go:360: ---- events while the RPC is still paused in the first interceptor (13 events) ----
    verify_e2e_probe_test.go:360:   #00 filter-build F1
    verify_e2e_probe_test.go:360:   #01 icpt-build F1/I1(gate) filterAlreadyClosed=false
    verify_e2e_probe_test.go:360:   #02 filter-build F2
    verify_e2e_probe_test.go:360:   #03 icpt-build F2/I1(plain) filterAlreadyClosed=false
    verify_e2e_probe_test.go:360:   #04 allow-rpc F1/I1(gate) interceptorAlreadyClosed=false filterAlreadyClosed=false
    verify_e2e_probe_test.go:360:   #05 allow-rpc F2/I1(plain) interceptorAlreadyClosed=false filterAlreadyClosed=false
    verify_e2e_probe_test.go:360:   #06 allow-rpc F1/I1(gate) interceptorAlreadyClosed=false filterAlreadyClosed=false
    verify_e2e_probe_test.go:360:   #07 icpt-build F1/I2(gate) filterAlreadyClosed=false
    verify_e2e_probe_test.go:360:   #08 icpt-build F2/I2(plain) filterAlreadyClosed=false
    verify_e2e_probe_test.go:360:   #09 icpt-build F1/I3(gate) filterAlreadyClosed=false
    verify_e2e_probe_test.go:360:   #10 icpt-build F2/I3(plain) filterAlreadyClosed=false
    verify_e2e_probe_test.go:360:   #11 icpt-close F1/I1(gate)
    verify_e2e_probe_test.go:360:   #12 icpt-close F2/I1(plain)
    verify_e2e_probe_test.go:361: OBSERVATION retirement mechanism: old interceptors closed while RPC paused = true
    verify_e2e_probe_test.go:367: paused RPC finished with err=<nil>
    verify_e2e_probe_test.go:373: ---- events after the RPC resumed (15 events) ----
    verify_e2e_probe_test.go:373:   #00 filter-build F1
    verify_e2e_probe_test.go:373:   #01 icpt-build F1/I1(gate) filterAlreadyClosed=false
    verify_e2e_probe_test.go:373:   #02 filter-build F2
    verify_e2e_probe_test.go:373:   #03 icpt-build F2/I1(plain) filterAlreadyClosed=false
    verify_e2e_probe_test.go:373:   #04 allow-rpc F1/I1(gate) interceptorAlreadyClosed=false filterAlreadyClosed=false
    verify_e2e_probe_test.go:373:   #05 allow-rpc F2/I1(plain) interceptorAlreadyClosed=false filterAlreadyClosed=false
    verify_e2e_probe_test.go:373:   #06 allow-rpc F1/I1(gate) interceptorAlreadyClosed=false filterAlreadyClosed=false
    verify_e2e_probe_test.go:373:   #07 icpt-build F1/I2(gate) filterAlreadyClosed=false
    verify_e2e_probe_test.go:373:   #08 icpt-build F2/I2(plain) filterAlreadyClosed=false
    verify_e2e_probe_test.go:373:   #09 icpt-build F1/I3(gate) filterAlreadyClosed=false
    verify_e2e_probe_test.go:373:   #10 icpt-build F2/I3(plain) filterAlreadyClosed=false
    verify_e2e_probe_test.go:373:   #11 icpt-close F1/I1(gate)
    verify_e2e_probe_test.go:373:   #12 icpt-close F2/I1(plain)
    verify_e2e_probe_test.go:373:   #13 allow-rpc-resume F1/I1(gate)
    verify_e2e_probe_test.go:373:   #14 allow-rpc F2/I1(plain) interceptorAlreadyClosed=true filterAlreadyClosed=false
    verify_e2e_probe_test.go:379: OBSERVATION active traversal trigger: allow-rpc F2/I1(plain) interceptorAlreadyClosed=true filterAlreadyClosed=false
    verify_e2e_probe_test.go:383: PROBLEM REPRODUCED: an interceptor's AllowRPC was invoked after its Close()
    --- FAIL: Test/Verify_PausedRPC_SameNameRDSReplacement (0.03s)
```

Reading the log: `F1/I1(gate)` and `F2/I1(plain)` are the two interceptors of the route on the client's filter chain in the original configuration. While the RPC is still parked inside `F1/I1` (#06), the replacement is built (#07–#10) and both old interceptors are closed (#11, #12). When the gate is released the same RPC proceeds to the second interceptor of the old snapshot: `#14 allow-rpc F2/I1(plain) interceptorAlreadyClosed=true`. The RPC returned `err=<nil>` (the probe interceptor does not enforce anything on use-after-close; it only records it).

Repeat (`-count=20` with `-run '^Test$/^Verify_(DisableThenReenableFilter|EmptyRouteConfigThenRestore|PausedRPC_SameNameRDSReplacement)$'`): `20 × verify_e2e_probe_test.go:383: PROBLEM REPRODUCED: an interceptor's AllowRPC was invoked after its Close()`, `20 × --- FAIL: Test/Verify_PausedRPC_SameNameRDSReplacement`, 0 `DATA RACE` reports.

Control on the task base commit `4ee6ac46` (`~/wt/base`, same probe, same command):

```console
    verify_e2e_probe_test.go:351: RPC paused inside interceptor F1/I1(gate)
    verify_e2e_probe_test.go:361: OBSERVATION retirement mechanism: old interceptors closed while RPC paused = false
    verify_e2e_probe_test.go:367: paused RPC finished with err=<nil>
    verify_e2e_probe_test.go:373:   #11 allow-rpc-resume F1/I1(gate)
    verify_e2e_probe_test.go:373:   #12 allow-rpc F2/I1(plain) interceptorAlreadyClosed=false filterAlreadyClosed=false
    verify_e2e_probe_test.go:385: OBSERVATION: no AllowRPC call was made on a closed interceptor
    --- PASS: Test/Verify_PausedRPC_SameNameRDSReplacement (5.04s)
```

(The test takes 5s there because the probe waits its full 3s window for a retirement that never happens.) On the base commit nothing is ever closed (that is the leak the task fixes), so no use-after-close occurs there; the behavior is introduced together with the retirement of superseded interceptors.

### Impact reasoning (C8)

Trigger: an RPC that is inside (or between) the per-route interceptors at the moment an RDS update for the route configuration it is using is applied — an ordinary situation on a busy server receiving route updates, needing no unusual configuration beyond a route with at least one interceptor that has not run yet. Effect observed: the remaining interceptors of the old configuration are invoked after their `Close()`. `internal/resolver.ServerInterceptor` documents `Close` as "Once called, no new calls … are accepted. Ongoing calls … are allowed to complete." The in-tree server interceptors (`rbac`, and `router` which builds none) have empty `Close()` methods (`grep -rn "func (.*) Close()" internal/xds/httpfilter`), so with today's filters the RPC still gets a correct answer; any interceptor that releases state in `Close()` would be used after release, and the authorization decision of that RPC would come from a closed object. The eval fixture's six `Eval_ServerSideXDS_*` tests pass on this branch, so none of them exercises this ordering. No workaround at the configuration level was observed.

## C9

Claim: re-enabling a filter after disabling it on all routes reuses a closed server filter to construct interceptors. Parts: *closed-entry reacquisition* (`getOrCreateServerFilterWithMap` and `incRef` allow acquisition of a cached filter whose reference count reached zero and whose resources were closed) and *all-routes reactivation trigger* (disabling a filter on all routes leaves its closed cache entry available for a later successful RDS update). Target: [grpc-go-xds-rds-interceptor-lifecycle-leak-perfect](https://github.com/kaitranntt-evals/grpc-go/tree/grpc-go-xds-rds-interceptor-lifecycle-leak-perfect). The evidence is the same runs as C4, repeated here in full.

### Cache lookup / reference acquisition (direct lookup, recorded separately)

`sed -n 408,421p internal/xds/server/listener_wrapper.go` and `sed -n 562,573p internal/xds/server/filter_chain_manager.go` on [grpc-go-xds-rds-interceptor-lifecycle-leak-perfect](https://github.com/kaitranntt-evals/grpc-go/tree/grpc-go-xds-rds-interceptor-lifecycle-leak-perfect) (`614cb7398c43bf270878ac15f34b68f5a9a4159d`):

```go
// custom map.
func getOrCreateServerFilterWithMap(httpFilters map[serverFilterKey]*refCountedServerFilter, builder httpfilter.ServerFilterBuilder, key serverFilterKey) httpfilter.ServerFilter {
	serverFilter, ok := httpFilters[key]
	if ok {
		serverFilter.incRef()
		return serverFilter
	}

	sf := builder.BuildServerFilter()
	serverFilter = &refCountedServerFilter{ServerFilter: sf}
	httpFilters[key] = serverFilter
	serverFilter.incRef()
	return serverFilter
}
}

func (il *interceptorList) Close() {
	for _, i := range il.interceptors {
		i.Close()
	}
}

// refCountedServerFilter wraps a ServerFilter along with a reference count.
// This is used to manage server filters that are shared across filter chains
// within a filter chain manager.
type refCountedServerFilter struct {
```

The final `Close()` closes the wrapped filter but nothing removes the map entry, and the lookup path does not check for a zero count before `incRef()`. Direct probe (`verify/repro/_verify_closed_filter_reacquire_test.go`):

```sh
cd ~/repos/grpc-go
cp verify/repro/_verify_closed_filter_reacquire_test.go internal/xds/server/verify_closed_filter_reacquire_test.go
go test -race -v -count=1 ./internal/xds/server -run '^Test$/^Verify_ClosedCacheEntryReacquired$'
rm internal/xds/server/verify_closed_filter_reacquire_test.go
```

```console
    verify_closed_filter_reacquire_test.go:64: OBSERVATION after first acquisition: refCnt=1 closed=false filtersBuilt=1
    verify_closed_filter_reacquire_test.go:68: OBSERVATION after final release:     refCnt=0 closed=true entryStillInCache=true
    verify_closed_filter_reacquire_test.go:73: OBSERVATION after second acquisition: sameInstance=true refCnt=1 closed=true filtersBuilt=1
    verify_closed_filter_reacquire_test.go:76: PROBLEM REPRODUCED: lookup re-acquired the cached entry after its final release; the returned server filter is already closed (no new filter was built: filtersBuilt=1)
--- FAIL: Test (0.00s)
    --- FAIL: Test/Verify_ClosedCacheEntryReacquired (0.00s)
FAIL
FAIL	google.golang.org/grpc/internal/xds/server	0.036s
FAIL
go test exit=1
```

The same direct probe prints the same four lines on the task base commit `4ee6ac46` (`~/wt/base`): the lookup primitive is unchanged by the solution.

### Disable → re-enable through RDS (runtime trigger)

`verify/repro/_verify_e2e_probe_test.go`, real xDS server + management server + client, one probe HTTP filter named `probe` in the HCM, RouteConfiguration delivered through RDS:

- `TestVerify_DisableThenReenableFilter` — update 1 disables the filter on all routes with a virtual-host-level `typed_per_filter_config` of `envoy.config.route.v3.FilterConfig{disabled: true}` (honoured under `GRPC_EXPERIMENTAL_XDS_EXT_PROC_ON_CLIENT`, the same switch the repo's own `TestServerSideXDS_FilterOverride_Disabled` sets); the test waits until an RPC succeeds without reaching the probe interceptor and the retired interceptor is closed; update 2 removes the override again and the test waits until an RPC is served through the probe filter. Both updates are accepted and RPCs succeed after each.
- `TestVerify_EmptyRouteConfigThenRestore` — same shape, but the filter's last reference is dropped by an RDS update with no virtual hosts (no experimental switch).

```sh
cd ~/repos/grpc-go
cp verify/repro/_verify_e2e_probe_test.go test/xds/verify_e2e_probe_test.go
go test -race -v -count=1 ./test/xds -run '^Test$/^Verify_(DisableThenReenableFilter|EmptyRouteConfigThenRestore)$'
rm test/xds/verify_e2e_probe_test.go
```

Output of `TestVerify_DisableThenReenableFilter`:

```console
    verify_e2e_probe_test.go:421: ---- after initial configuration + 1 RPC (3 events) ----
    verify_e2e_probe_test.go:453: ---- after the RDS update that removes the filter from every route (5 events) ----
    verify_e2e_probe_test.go:453:   #00 filter-build F1
    verify_e2e_probe_test.go:453:   #01 icpt-build F1/I1(plain) filterAlreadyClosed=false
    verify_e2e_probe_test.go:453:   #02 allow-rpc F1/I1(plain) interceptorAlreadyClosed=false filterAlreadyClosed=false
    verify_e2e_probe_test.go:453:   #03 icpt-close F1/I1(plain)
    verify_e2e_probe_test.go:453:   #04 filter-close F1
    verify_e2e_probe_test.go:454: OBSERVATION disable transition closed the server filter = true
    verify_e2e_probe_test.go:465: OBSERVATION ordering: first filter-close at #4, first retired icpt-close at #3, filterDestroyedBeforeInterceptorClosed=false
    verify_e2e_probe_test.go:484: ---- after the RDS update re-enabling the filter + 1 RPC (8 events) ----
    verify_e2e_probe_test.go:490: OBSERVATION reactivation: icpt-build F1/I2(plain) filterAlreadyClosed=true
    verify_e2e_probe_test.go:490: OBSERVATION reactivation: icpt-build F1/I3(plain) filterAlreadyClosed=true
    verify_e2e_probe_test.go:493: OBSERVATION filters built in total = 1
    verify_e2e_probe_test.go:496: ---- after server stop (11 events) ----
    verify_e2e_probe_test.go:496:   #00 filter-build F1
    verify_e2e_probe_test.go:496:   #01 icpt-build F1/I1(plain) filterAlreadyClosed=false
    verify_e2e_probe_test.go:496:   #02 allow-rpc F1/I1(plain) interceptorAlreadyClosed=false filterAlreadyClosed=false
    verify_e2e_probe_test.go:496:   #03 icpt-close F1/I1(plain)
    verify_e2e_probe_test.go:496:   #04 filter-close F1
    verify_e2e_probe_test.go:496:   #05 icpt-build F1/I2(plain) filterAlreadyClosed=true
    verify_e2e_probe_test.go:496:   #06 icpt-build F1/I3(plain) filterAlreadyClosed=true
    verify_e2e_probe_test.go:496:   #07 allow-rpc F1/I3(plain) interceptorAlreadyClosed=false filterAlreadyClosed=true
    verify_e2e_probe_test.go:496:   #08 icpt-close F1/I2(plain)
    verify_e2e_probe_test.go:496:   #09 icpt-close F1/I3(plain)
    verify_e2e_probe_test.go:496:   #10 filter-close F1
    verify_e2e_probe_test.go:502: PROBLEM REPRODUCED (closed filter reuse): the re-enabled configuration built interceptors from a server filter that had already been closed
    --- FAIL: Test/Verify_DisableThenReenableFilter (0.05s)
```

Output of `TestVerify_EmptyRouteConfigThenRestore` (final event log and observations):

```console
    verify_e2e_probe_test.go:454: OBSERVATION disable transition closed the server filter = true
    verify_e2e_probe_test.go:465: OBSERVATION ordering: first filter-close at #5, first retired icpt-close at #4, filterDestroyedBeforeInterceptorClosed=false
    verify_e2e_probe_test.go:490: OBSERVATION reactivation: icpt-build F1/I2(plain) filterAlreadyClosed=true
    verify_e2e_probe_test.go:490: OBSERVATION reactivation: icpt-build F1/I3(plain) filterAlreadyClosed=true
    verify_e2e_probe_test.go:493: OBSERVATION filters built in total = 1
    verify_e2e_probe_test.go:496: ---- after server stop (12 events) ----
    verify_e2e_probe_test.go:496:   #00 filter-build F1
    verify_e2e_probe_test.go:496:   #01 icpt-build F1/I1(plain) filterAlreadyClosed=false
    verify_e2e_probe_test.go:496:   #02 allow-rpc F1/I1(plain) interceptorAlreadyClosed=false filterAlreadyClosed=false
    verify_e2e_probe_test.go:496:   #03 allow-rpc F1/I1(plain) interceptorAlreadyClosed=false filterAlreadyClosed=false
    verify_e2e_probe_test.go:496:   #04 icpt-close F1/I1(plain)
    verify_e2e_probe_test.go:496:   #05 filter-close F1
    verify_e2e_probe_test.go:496:   #06 icpt-build F1/I2(plain) filterAlreadyClosed=true
    verify_e2e_probe_test.go:496:   #07 icpt-build F1/I3(plain) filterAlreadyClosed=true
    verify_e2e_probe_test.go:496:   #08 allow-rpc F1/I3(plain) interceptorAlreadyClosed=false filterAlreadyClosed=true
    verify_e2e_probe_test.go:496:   #09 icpt-close F1/I2(plain)
    verify_e2e_probe_test.go:496:   #10 icpt-close F1/I3(plain)
    verify_e2e_probe_test.go:496:   #11 filter-close F1
    verify_e2e_probe_test.go:502: PROBLEM REPRODUCED (closed filter reuse): the re-enabled configuration built interceptors from a server filter that had already been closed
    --- FAIL: Test/Verify_EmptyRouteConfigThenRestore (0.04s)
```

Reading the log: the disable transition closes the retired interceptor and then the filter (`icpt-close F1/I1`, `filter-close F1`; interceptor first, so no ordering problem on this branch). The re-enable transition builds no new filter (`filters built in total = 1`): it retrieves `F1` again and builds both filter chains' interceptors from it while it is closed (`icpt-build F1/I2(plain) filterAlreadyClosed=true`, `icpt-build F1/I3(plain) filterAlreadyClosed=true`), and RPCs are then served through them (`allow-rpc F1/I3(plain) … filterAlreadyClosed=true`). At server stop `F1.Close()` is called a second time (`filter-close F1` appears twice).

Repeat (`-count=20`): `40 × verify_e2e_probe_test.go:502: PROBLEM REPRODUCED (closed filter reuse)…` (20 per test), `20 × --- FAIL` for each of the two tests, 0 `DATA RACE` reports.

### Impact reasoning (C9)

Trigger: a filter present in the Listener's HCM loses its last reference through an RDS update (disabled on every route, or a RouteConfiguration with no virtual hosts / an RDS resource error, see the C2 control run where the audited branch shows `icpt-build F1/I2 filterAlreadyClosed=true` after error → success) and a later accepted RDS update uses it again. No LDS update is involved in the sequence. From then on (as far as the probe ran) every interceptor for that filter on that listener is built by a filter object whose `Close()` has already run, the filter never gets a fresh instance, and `Close()` runs again at shutdown (double close). The in-tree server filters (`rbac`, `router`) have empty `Close()` methods, so nothing visible happens with them today; a stateful filter that releases resources in `Close()` — the case the reference-counted cache exists for — would serve RPCs from released state. The lookup primitive is the same on the base commit; what the solution adds is the release of filter references on these RDS transitions without invalidating the cache entry. The eval fixture's six tests pass on this branch, so none covers this sequence.

## C10

Claim: the focused server-side xDS integration tests do not complete successfully under Go's race detector in the solution's provided environment. Target: [grpc-go-xds-rds-interceptor-lifecycle-leak-perfect](https://github.com/kaitranntt-evals/grpc-go/tree/grpc-go-xds-rds-interceptor-lifecycle-leak-perfect) (`614cb7398c43bf270878ac15f34b68f5a9a4159d`), run in `~/repos/grpc-go` with nothing but the untracked `verify/` directory added (no probe copied into `test/xds`).

```sh
cd ~/repos/grpc-go
git status --short          # prints only: ?? verify/
go test -race -v ./test/xds -run '^Test$/^ServerSideXDS_' -count=1      # run three times
go test -race -v ./test/xds -run '^Test$/^ServerSideXDS_' -count=30
```

Test selection: the package uses a `grpctest` suite (`func Test(t *testing.T)` running methods of `s`), so the regex selects subtests `Test/ServerSideXDS_*`; `grep -c "^func (s) TestServerSideXDS_" test/xds/*.go` gives 2+2+4+1+1 = 10 methods across `xds_server_filter_override_test.go`, `xds_server_filter_state_retention_test.go`, `xds_server_integration_test.go`, `xds_server_interceptor_leak_test.go`, `xds_server_rbac_test.go`, and 10 subtests ran.

Run 1 (first run includes compilation, `real 0m27.146s`):

```console
--- PASS: Test (1.44s)
    --- PASS: Test/ServerSideXDS_Fallback (0.03s)
    --- PASS: Test/ServerSideXDS_FileWatcherCerts (0.08s)
    --- PASS: Test/ServerSideXDS_FileWatcherCertsSPIFFE (0.05s)
    --- PASS: Test/ServerSideXDS_FilterOverride_Disabled (0.05s)
    --- PASS: Test/ServerSideXDS_FilterOverride_Enabled (0.06s)
    --- PASS: Test/ServerSideXDS_FilterStateRetention_AcrossUpdates_FilterChainsChange (0.03s)
    --- PASS: Test/ServerSideXDS_FilterStateRetention_AcrossUpdates_FilterConfigChange (0.03s)
    --- PASS: Test/ServerSideXDS_InterceptorLeak_RDSUpdate (0.03s)
    --- PASS: Test/ServerSideXDS_RouteConfiguration (0.02s)
    --- PASS: Test/ServerSideXDS_SecurityConfigChange (1.05s)
PASS
ok  	google.golang.org/grpc/test/xds	2.475s
EXIT=0
```

Runs 2 and 3:

```console
--- PASS: Test (1.51s)
ok  	google.golang.org/grpc/test/xds	2.542s
real	0m3.840s
EXIT=0
run 2: subtests PASS=10 FAIL=0 SKIP=0 DATA_RACE=0
--- PASS: Test (1.46s)
ok  	google.golang.org/grpc/test/xds	2.497s
real	0m3.984s
EXIT=0
run 3: subtests PASS=10 FAIL=0 SKIP=0 DATA_RACE=0
```

`-count=30`:

```console
top-level Test PASS=30 FAIL=0 subtest PASS=300 subtest FAIL=0 DATA_RACE=0
PASS
ok  	google.golang.org/grpc/test/xds	47.303s
EXIT=0
```

Related commands from the environment reference, same checkout:

```console
$ go build ./...; echo "build exit=$?"
build exit=0
$ go test -race ./internal/xds/server/... -count=1; echo "test exit=$?"
ok  	google.golang.org/grpc/internal/xds/server	1.101s
test exit=0
$ go vet ./test/xds/ ./internal/xds/server/...; echo "vet exit=$?"
vet exit=0
```

And the eval fixture on this branch (`cp ~/eval/tests/eval_xds_server_interceptor_leak_test.go test/xds/ && go test -race -v -count=1 ./test/xds -run '^Test$/^Eval_ServerSideXDS_'`, copy removed afterwards):

```console
--- PASS: Test (0.22s)
    --- PASS: Test/Eval_ServerSideXDS_InterceptorBeforeFilterCloseOrder (0.06s)
    --- PASS: Test/Eval_ServerSideXDS_InterceptorLeak_MultiGenerationRDSUpdate (0.04s)
    --- PASS: Test/Eval_ServerSideXDS_InterceptorLeak_RDSUpdate (0.03s)
    --- PASS: Test/Eval_ServerSideXDS_InterceptorSwapOrder (0.03s)
    --- PASS: Test/Eval_ServerSideXDS_PartialRouteFailure_ClosesInterceptors (0.03s)
    --- PASS: Test/Eval_ServerSideXDS_PartialVirtualHostFailure_ClosesInterceptors (0.03s)
PASS
ok  	google.golang.org/grpc/test/xds	1.253s
go test exit=0
```

Observation: the package builds with `-race`, the 10 selected tests execute and pass in ~1.5s (default 10m test timeout never approached), 0 failures and 0 `DATA RACE` reports across 3 single runs and 30 repeated iterations (330 subtest executions in total).
