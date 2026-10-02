Environment for every section: Ubuntu VM, 8 CPUs, `go version go1.25.7 linux/amd64`. Claim branches live in the repository named in each claim URL (not `origin`), fetched as remote `claims`; each was checked out in its own worktree under `~/wt/<short-id>`. The eval fixture was used byte-exact from the archive (`sha256 e8d6eddf69de21aa83534b24b8c30cd061ad8bc93f8974a8d99f606c6a4a242c`). Production code on every branch was left untouched; the mutations in C1 are applied and reverted by the repro script inside the throw-away worktrees. Raw/filtered logs of every run quoted below are committed under `verify/logs/`. `verify/go.mod` only makes `verify/` a separate module so that `go build ./...` / `go vet ./...` at the repository root ignore the repro test file; it is not used by any run.

## C1

**Claim:** the solution's tests assert interceptor destruction/closure counts after observing replacement traffic or route publication without synchronizing with completion of interceptor retirement. Adjudicated independently on five branches. **Observed result: holds on all five.**

### Setup (self-contained)

```sh
cd ~/repos/grpc-go
git fetch origin grpc-go-xds-rds-interceptor-lifecycle-leak-perfect
git checkout -b verify/grpc-go-xds-rds-interceptor-lifecycle-leak-v-389b3566 origin/grpc-go-xds-rds-interceptor-lifecycle-leak-perfect
git remote add claims https://github.com/kaitranntt-evals/grpc-go-xds-rds-interceptor-lifecycle-leak   # in this session: the same repo via the VM's git proxy
for b in 8de853fe 1ea7dd6e 8f0663b7 bbe58e09 295929ad 423f7ef0; do
  git fetch claims evalon/grpc-go-xd-$b:refs/remotes/claims/evalon/grpc-go-xd-$b
  git worktree add --detach ~/wt/$b claims/evalon/grpc-go-xd-$b
done
git worktree add --detach ~/wt/perfect origin/grpc-go-xds-rds-interceptor-lifecycle-leak-perfect   # control
git worktree add --detach ~/wt/base 4ee6ac46fada69c06576cee108b009689a000520                      # control (task base commit)
mkdir -p ~/eval && unzip -o eval_tests.zip -d ~/eval    # fixture: ~/eval/tests/eval_xds_server_interceptor_leak_test.go
```

### Method

For each branch, `verify/repro/c1/run_c1.sh <worktree> <short-id> 125` (helper: `verify/repro/c1/mutate.py`) does the following and restores the worktree afterwards:

0. Baseline: the branch's own added route-replacement integration test, unmodified, `-race -count=1`; plus the eval fixture on the unmodified branch (reference for step 2).
1. Natural stress, **unmodified code and unmodified test**: build the `-race` test binary once and run 24 concurrent processes x 125 iterations (3000 runs) of just that test on the 8-CPU VM, then tally failure messages.
2. Mutation B (production, scheduling-only): `time.Sleep(100ms)` between `usableRouteConfiguration.Swap(new)` (publication) and closing the retired configuration's interceptors. Run the branch's test, then the eval fixture (whose replacement tests poll the destruction counter with a deadline) under the same mutation.
3. Mutation A (test double only, production untouched): `trackingInterceptor.Close` sleeps 100ms before it increments `interceptorsDestroyed`.
4. Mutation A2 (test double only; only for the three branches whose test uses two filter chains): only every second `Close` (the last one of each update) is slow, so the *created*-count assertion that precedes the destruction assertion is satisfied and the destruction assertion is isolated.

Why this is the right probe: on all five branches the production code publishes the replacement with `atomic.Pointer.Swap` and only then closes the retired interceptors, on the xDS callback goroutine. An RPC served by the replacement interceptor therefore proves only that `Swap` happened, not that `Close` of the retired interceptors has run. A test that asserts a destruction count immediately after such an RPC is correct only if it wins a race against the callback goroutine.

Common synchronization shape on every branch (the only wait before the assertion is "an RPC was served by the interceptor configured with the new path"; there is no polling of `interceptorsDestroyed` and no destruction-completion event):

### Branch [`evalon/grpc-go-xd-8de853fe`](https://github.com/kaitranntt-evals/grpc-go-xds-rds-interceptor-lifecycle-leak/tree/evalon/grpc-go-xd-8de853fe)

Added test `TestServerSideXDS_FilterStateRetention_RepeatedRouteConfigUpdates` in `test/xds/xds_server_filter_state_retention_test.go` (single default filter chain). Synchronization and assertion, verbatim with line numbers:

```go
756: 	waitForPath := func(want string) {
757: 		t.Helper()
758: 		for ; ctx.Err() == nil; <-time.After(defaultTestShortTimeout) {
759: 			if _, err := client.EmptyCall(ctx, &testpb.Empty{}); err != nil {
760: 				t.Fatalf("EmptyCall() failed: %v", err)
761: 			}
762: 			select {
763: 			case got := <-pathCh:
764: 				if got == want {
765: 					return
766: 				}
767: 			case <-ctx.Done():
768: 			}
769: 		}
770: 		t.Fatalf("Timeout waiting for interceptor with config %q to be invoked", want)
771: 	}
// ...
775: 	for i := 1; i <= numUpdates; i++ {
776: 		path := fmt.Sprintf("path-%d", i)
777: 		resources.Routes = append(clientRoutes, serverRouteConfig(path))
778: 		if err := managementServer.Update(ctx, resources); err != nil {
779: 			t.Fatal(err)
780: 		}
781: 		waitForPath(path)
782: 
783: 		// Only the interceptor for the single route in the current
784: 		// configuration is expected to be live, and the filter instance is
785: 		// expected to be retained across updates.
786: 		if got, want := interceptorsCreated.Load()-interceptorsDestroyed.Load(), int32(1); got != want {
787: 			t.Fatalf("After %d route configuration updates, %d live interceptor instances, want: %d", i, got, want)
788: 		}
```

Production ordering on this branch (`internal/xds/server/filter_chain_manager.go`): publish with `Swap`, then close the retired interceptors:

```go
386:func (fc *filterChain) setUsableRouteConfiguration(urc *usableRouteConfiguration) {
387-	if old := fc.usableRouteConfiguration.Swap(urc); old != urc {
388-		old.closeInterceptors()
389-	}
390-}
391-
392-// serverFilterProvider is used to get a ServerFilter.
```

Run and output (`verify/logs/c1_8de853fe.log`):

```sh
FIXTURE=~/eval/tests/eval_xds_server_interceptor_leak_test.go bash verify/repro/c1/run_c1.sh ~/wt/8de853fe 8de853fe 125
```

```console
>> branch evalon/grpc-go-xd-8de853fe @ 57f508e0, test Test/ServerSideXDS_FilterStateRetention_RepeatedRouteConfigUpdates
>> step 0: baseline (unmodified)
$ go test -run '^Test$/^ServerSideXDS_FilterStateRetention_RepeatedRouteConfigUpdates$' ./test/xds -race -count=1
--- PASS: Test (0.17s)
    --- PASS: Test/ServerSideXDS_FilterStateRetention_RepeatedRouteConfigUpdates (0.16s)
PASS
ok  	google.golang.org/grpc/test/xds	1.205s
$ go test -run '^Test$/^Eval_' ./test/xds -race -count=1   # eval fixture, unmodified branch (reference for step 2)
--- FAIL: Test (4.27s)
    --- FAIL: Test/Eval_ServerSideXDS_InterceptorBeforeFilterCloseOrder (0.05s)
    --- PASS: Test/Eval_ServerSideXDS_InterceptorLeak_MultiGenerationRDSUpdate (0.04s)
    --- PASS: Test/Eval_ServerSideXDS_InterceptorLeak_RDSUpdate (0.03s)
    --- PASS: Test/Eval_ServerSideXDS_InterceptorSwapOrder (0.03s)
    --- FAIL: Test/Eval_ServerSideXDS_PartialRouteFailure_ClosesInterceptors (2.07s)
    --- FAIL: Test/Eval_ServerSideXDS_PartialVirtualHostFailure_ClosesInterceptors (2.02s)
FAIL
FAIL	google.golang.org/grpc/test/xds	4.300s
FAIL
>> step 1: natural stress (unmodified): 24 concurrent processes x 125 runs each of the -race test binary
$ go test -race -c -o /tmp/xds_8de853fe.test ./test/xds && (cd test/xds && for i in $(seq 24); do /tmp/xds_8de853fe.test -test.run '^Test$/^ServerSideXDS_FilterStateRetention_RepeatedRouteConfigUpdates$' -test.count=125 -test.v > 
passes: 2996  failures: 4
failure messages (count, message):
      1 xds_server_filter_state_retention_test.go:787: After 1 route configuration updates, 2 live interceptor instances, want: 1
      1 xds_server_filter_state_retention_test.go:787: After 4 route configuration updates, 2 live interceptor instances, want: 1
      1 xds_server_filter_state_retention_test.go:787: After 5 route configuration updates, 2 live interceptor instances, want: 1
      1 xds_server_filter_state_retention_test.go:787: After 8 route configuration updates, 2 live interceptor instances, want: 1
>> step 2: mutation B (production: 100ms sleep between Swap() and closing the retired interceptors)
 internal/xds/server/filter_chain_manager.go | 2 ++
 1 file changed, 2 insertions(+)
+	"time"
+		time.Sleep(100 * time.Millisecond) // VERIFY mutation B: scheduling delay only
$ go test -run '^Test$/^ServerSideXDS_FilterStateRetention_RepeatedRouteConfigUpdates$' ./test/xds -race -count=1   # branch's own test
    xds_server_filter_state_retention_test.go:787: After 1 route configuration updates, 2 live interceptor instances, want: 1
--- FAIL: Test (0.23s)
    --- FAIL: Test/ServerSideXDS_FilterStateRetention_RepeatedRouteConfigUpdates (0.22s)
FAIL
FAIL	google.golang.org/grpc/test/xds	0.258s
FAIL
$ go test -run '^Test$/^Eval_' ./test/xds -race -count=1   # eval fixture (polls the destruction counter), same mutation
--- FAIL: Test (5.95s)
    --- FAIL: Test/Eval_ServerSideXDS_InterceptorBeforeFilterCloseOrder (0.23s)
    --- PASS: Test/Eval_ServerSideXDS_InterceptorLeak_MultiGenerationRDSUpdate (0.63s)
    --- PASS: Test/Eval_ServerSideXDS_InterceptorLeak_RDSUpdate (0.43s)
    --- PASS: Test/Eval_ServerSideXDS_InterceptorSwapOrder (0.41s)
    --- FAIL: Test/Eval_ServerSideXDS_PartialRouteFailure_ClosesInterceptors (2.12s)
    --- FAIL: Test/Eval_ServerSideXDS_PartialVirtualHostFailure_ClosesInterceptors (2.13s)
FAIL
FAIL	google.golang.org/grpc/test/xds	5.984s
FAIL
>> step 3: mutation A (test double only: trackingInterceptor.Close sleeps 100ms before counting itself destroyed)
 test/xds/xds_server_filter_state_retention_test.go | 1 +
 1 file changed, 1 insertion(+)
+	time.Sleep(100 * time.Millisecond) // VERIFY mutation A: slow Close
$ go test -run '^Test$/^ServerSideXDS_FilterStateRetention_RepeatedRouteConfigUpdates$' ./test/xds -race -count=1
    xds_server_filter_state_retention_test.go:788: After 1 route configuration updates, 2 live interceptor instances, want: 1
--- FAIL: Test (0.24s)
    --- FAIL: Test/ServerSideXDS_FilterStateRetention_RepeatedRouteConfigUpdates (0.24s)
FAIL
FAIL	google.golang.org/grpc/test/xds	0.275s
FAIL
>> step 4: mutation A2 (test double only: every 2nd trackingInterceptor.Close -- the last one of each two-filter-chain update -- sleeps 100ms before counting itself destroyed)
(not applicable: this branch's test uses a single filter chain, so mutation A already isolates the destruction assertion)
(worktree restored to branch head)
```

### Branch [`evalon/grpc-go-xd-1ea7dd6e`](https://github.com/kaitranntt-evals/grpc-go-xds-rds-interceptor-lifecycle-leak/tree/evalon/grpc-go-xd-1ea7dd6e)

Added test `TestServerSideXDS_RouteConfigurationUpdates_ReleaseSupersededResources` in `test/xds/xds_server_route_config_update_test.go` (two filter chains (v4 + v6 wildcard)). Synchronization and assertion, verbatim with line numbers:

```go
231: 	waitForPath := func(want string) {
232: 		t.Helper()
233: 		for ; ctx.Err() == nil; <-time.After(defaultTestShortTimeout) {
234: 			if _, err := client.EmptyCall(ctx, &testpb.Empty{}); err != nil {
235: 				t.Fatalf("EmptyCall() failed: %v", err)
236: 			}
237: 			select {
238: 			case got := <-pathCh:
239: 				if got == want {
240: 					return
241: 				}
242: 			case <-ctx.Done():
243: 			}
244: 		}
245: 		t.Fatalf("Timeout waiting for interceptor with path %q to be invoked", want)
246: 	}
// ...
260: 	for i := 1; i <= numUpdates; i++ {
261: 		resources.Routes = append(clientRoutes, serverRouteConfig(fmt.Sprintf("path-%d", i)))
262: 		if err := managementServer.Update(ctx, resources); err != nil {
263: 			t.Fatal(err)
264: 		}
265: 		waitForPath(fmt.Sprintf("path-%d", i))
266: 
267: 		if got, want := filtersCreated.Load(), int32(1); got != want {
268: 			t.Fatalf("After RouteConfiguration update %d: created %d filter instances, want: %d", i, got, want)
269: 		}
270: 		if got, want := interceptorsCreated.Load(), int32(2*(i+1)); got != want {
271: 			t.Fatalf("After RouteConfiguration update %d: created %d interceptor instances, want: %d", i, got, want)
272: 		}
273: 		if got, want := interceptorsDestroyed.Load(), int32(2*i); got != want {
274: 			t.Fatalf("After RouteConfiguration update %d: destroyed %d interceptor instances, want: %d", i, got, want)
275: 		}
```

Production ordering on this branch (`internal/xds/server/filter_chain_manager.go`): publish with `Swap`, then close the retired interceptors:

```go
399:func (fc *filterChain) storeUsableRouteConfiguration(urc *usableRouteConfiguration) {
400-	fc.usableRouteConfiguration.Swap(urc).close()
401-}
402-
403-// stop releases the resources held by this filter chain: the interceptors
404-// instantiated for its current usable route configuration, and its references
405-// to the shared server filters.
```

Run and output (`verify/logs/c1_1ea7dd6e.log`):

```sh
FIXTURE=~/eval/tests/eval_xds_server_interceptor_leak_test.go bash verify/repro/c1/run_c1.sh ~/wt/1ea7dd6e 1ea7dd6e 125
```

```console
>> branch evalon/grpc-go-xd-1ea7dd6e @ 8f07f444, test Test/ServerSideXDS_RouteConfigurationUpdates_ReleaseSupersededResources
>> step 0: baseline (unmodified)
$ go test -run '^Test$/^ServerSideXDS_RouteConfigurationUpdates_ReleaseSupersededResources$' ./test/xds -race -count=1
--- PASS: Test (0.07s)
    --- PASS: Test/ServerSideXDS_RouteConfigurationUpdates_ReleaseSupersededResources (0.07s)
PASS
ok  	google.golang.org/grpc/test/xds	1.105s
$ go test -run '^Test$/^Eval_' ./test/xds -race -count=1   # eval fixture, unmodified branch (reference for step 2)
--- FAIL: Test (4.18s)
    --- FAIL: Test/Eval_ServerSideXDS_InterceptorBeforeFilterCloseOrder (0.03s)
    --- PASS: Test/Eval_ServerSideXDS_InterceptorLeak_MultiGenerationRDSUpdate (0.04s)
    --- PASS: Test/Eval_ServerSideXDS_InterceptorLeak_RDSUpdate (0.03s)
    --- PASS: Test/Eval_ServerSideXDS_InterceptorSwapOrder (0.03s)
    --- FAIL: Test/Eval_ServerSideXDS_PartialRouteFailure_ClosesInterceptors (2.02s)
    --- FAIL: Test/Eval_ServerSideXDS_PartialVirtualHostFailure_ClosesInterceptors (2.02s)
FAIL
FAIL	google.golang.org/grpc/test/xds	4.209s
FAIL
>> step 1: natural stress (unmodified): 24 concurrent processes x 125 runs each of the -race test binary
$ go test -race -c -o /tmp/xds_1ea7dd6e.test ./test/xds && (cd test/xds && for i in $(seq 24); do /tmp/xds_1ea7dd6e.test -test.run '^Test$/^ServerSideXDS_RouteConfigurationUpdates_ReleaseSupersededResources$' -test.count=125 -test
passes: 2995  failures: 5
failure messages (count, message):
      1 xds_server_route_config_update_test.go:271: After RouteConfiguration update 1: created 3 interceptor instances, want: 4
      1 xds_server_route_config_update_test.go:271: After RouteConfiguration update 3: created 7 interceptor instances, want: 8
      2 xds_server_route_config_update_test.go:271: After RouteConfiguration update 4: created 9 interceptor instances, want: 10
      1 xds_server_route_config_update_test.go:274: After RouteConfiguration update 1: destroyed 1 interceptor instances, want: 2
>> step 2: mutation B (production: 100ms sleep between Swap() and closing the retired interceptors)
 internal/xds/server/filter_chain_manager.go | 5 ++++-
 1 file changed, 4 insertions(+), 1 deletion(-)
+	"time"
-	fc.usableRouteConfiguration.Swap(urc).close()
+	old := fc.usableRouteConfiguration.Swap(urc)
+	time.Sleep(100 * time.Millisecond) // VERIFY mutation B: scheduling delay only
+	old.close()
$ go test -run '^Test$/^ServerSideXDS_RouteConfigurationUpdates_ReleaseSupersededResources$' ./test/xds -race -count=1   # branch's own test
    xds_server_route_config_update_test.go:271: After RouteConfiguration update 1: created 3 interceptor instances, want: 4
--- FAIL: Test (0.43s)
    --- FAIL: Test/ServerSideXDS_RouteConfigurationUpdates_ReleaseSupersededResources (0.42s)
FAIL
FAIL	google.golang.org/grpc/test/xds	0.461s
FAIL
$ go test -run '^Test$/^Eval_' ./test/xds -race -count=1   # eval fixture (polls the destruction counter), same mutation
--- FAIL: Test (5.94s)
    --- FAIL: Test/Eval_ServerSideXDS_InterceptorBeforeFilterCloseOrder (0.23s)
    --- PASS: Test/Eval_ServerSideXDS_InterceptorLeak_MultiGenerationRDSUpdate (0.62s)
    --- PASS: Test/Eval_ServerSideXDS_InterceptorLeak_RDSUpdate (0.42s)
    --- PASS: Test/Eval_ServerSideXDS_InterceptorSwapOrder (0.42s)
    --- FAIL: Test/Eval_ServerSideXDS_PartialRouteFailure_ClosesInterceptors (2.12s)
    --- FAIL: Test/Eval_ServerSideXDS_PartialVirtualHostFailure_ClosesInterceptors (2.12s)
FAIL
FAIL	google.golang.org/grpc/test/xds	5.976s
FAIL
>> step 3: mutation A (test double only: trackingInterceptor.Close sleeps 100ms before counting itself destroyed)
 test/xds/xds_server_filter_state_retention_test.go | 1 +
 1 file changed, 1 insertion(+)
+	time.Sleep(100 * time.Millisecond) // VERIFY mutation A: slow Close
$ go test -run '^Test$/^ServerSideXDS_RouteConfigurationUpdates_ReleaseSupersededResources$' ./test/xds -race -count=1
    xds_server_route_config_update_test.go:271: After RouteConfiguration update 1: created 3 interceptor instances, want: 4
--- FAIL: Test (0.44s)
    --- FAIL: Test/ServerSideXDS_RouteConfigurationUpdates_ReleaseSupersededResources (0.44s)
FAIL
FAIL	google.golang.org/grpc/test/xds	0.477s
FAIL
>> step 4: mutation A2 (test double only: every 2nd trackingInterceptor.Close -- the last one of each two-filter-chain update -- sleeps 100ms before counting itself destroyed)
 test/xds/xds_server_filter_state_retention_test.go | 5 +++++
 1 file changed, 5 insertions(+)
+var verifyCloseCalls atomic.Int32
+
+	if verifyCloseCalls.Add(1)%2 == 0 {
+		time.Sleep(100 * time.Millisecond) // VERIFY mutation A2: last Close of the update is slow
+	}
$ go test -run '^Test$/^ServerSideXDS_RouteConfigurationUpdates_ReleaseSupersededResources$' ./test/xds -race -count=5
    xds_server_route_config_update_test.go:274: After RouteConfiguration update 1: destroyed 1 interceptor instances, want: 2
--- FAIL: Test (0.24s)
    --- FAIL: Test/ServerSideXDS_RouteConfigurationUpdates_ReleaseSupersededResources (0.24s)
    xds_server_route_config_update_test.go:274: After RouteConfiguration update 1: destroyed 1 interceptor instances, want: 2
--- FAIL: Test (0.23s)
    --- FAIL: Test/ServerSideXDS_RouteConfigurationUpdates_ReleaseSupersededResources (0.22s)
    xds_server_route_config_update_test.go:274: After RouteConfiguration update 1: destroyed 1 interceptor instances, want: 2
--- FAIL: Test (0.23s)
    --- FAIL: Test/ServerSideXDS_RouteConfigurationUpdates_ReleaseSupersededResources (0.22s)
    xds_server_route_config_update_test.go:274: After RouteConfiguration update 1: destroyed 1 interceptor instances, want: 2
--- FAIL: Test (0.23s)
    --- FAIL: Test/ServerSideXDS_RouteConfigurationUpdates_ReleaseSupersededResources (0.22s)
    xds_server_route_config_update_test.go:274: After RouteConfiguration update 1: destroyed 1 interceptor instances, want: 2
--- FAIL: Test (0.23s)
    --- FAIL: Test/ServerSideXDS_RouteConfigurationUpdates_ReleaseSupersededResources (0.22s)
FAIL
FAIL	google.golang.org/grpc/test/xds	1.180s
FAIL
(worktree restored to branch head)
```

### Branch [`evalon/grpc-go-xd-8f0663b7`](https://github.com/kaitranntt-evals/grpc-go-xds-rds-interceptor-lifecycle-leak/tree/evalon/grpc-go-xd-8f0663b7)

Added test `TestServerSideXDS_RouteConfigUpdate_ReleasesSupersededResources` in `test/xds/xds_server_route_config_update_test.go` (two filter chains (v4 + v6 wildcard)). Synchronization and assertion, verbatim with line numbers:

```go
137: func waitForRouteConfigPath(ctx context.Context, t *testing.T, client testgrpc.TestServiceClient, pathCh chan string, wantPath string) {
138: 	t.Helper()
139: 
140: 	for ; ctx.Err() == nil; <-time.After(defaultTestShortTimeout) {
141: 		if _, err := client.EmptyCall(ctx, &testpb.Empty{}); err != nil {
142: 			t.Fatalf("EmptyCall() failed: %v", err)
143: 		}
144: 		select {
145: 		case path := <-pathCh:
146: 			if path == wantPath {
147: 				return
148: 			}
149: 		case <-ctx.Done():
150: 		}
151: 	}
152: 	t.Fatalf("Timeout waiting for route configuration with path %q to be applied by the server", wantPath)
153: }
// ...
256: 	for i := 2; i <= numUpdates; i++ {
257: 		path := fmt.Sprintf("path-%d", i)
258: 		resources.Routes[serverRouteConfigIdx] = routeConfigWithFilterOverride(t, routeName, filterName, testFilterTypeURL, path)
259: 		if err := managementServer.Update(ctx, resources); err != nil {
260: 			t.Fatal(err)
261: 		}
262: 		waitForRouteConfigPath(ctx, t, client, pathCh, path)
263: 
264: 		if got, want := filtersCreated.Load(), int32(1); got != want {
265: 			t.Fatalf("Update %d: created %d filter instances, want: %d", i, got, want)
266: 		}
267: 		if got, want := filtersDestroyed.Load(), int32(0); got != want {
268: 			t.Fatalf("Update %d: destroyed %d filter instances, want: %d", i, got, want)
269: 		}
270: 		if got, want := interceptorsCreated.Load(), int32(interceptorsPerUpdate*i); got != want {
271: 			t.Fatalf("Update %d: created %d interceptor instances, want: %d", i, got, want)
272: 		}
273: 		if got, want := interceptorsDestroyed.Load(), int32(interceptorsPerUpdate*(i-1)); got != want {
274: 			t.Fatalf("Update %d: destroyed %d interceptor instances, want: %d", i, got, want)
275: 		}
```

Production ordering on this branch (`internal/xds/server/filter_chain_manager.go`): publish with `Swap`, then close the retired interceptors:

```go
401:func (fc *filterChain) storeUsableRouteConfiguration(urc *usableRouteConfiguration) {
402-	fc.usableRouteConfiguration.Swap(urc).close()
403-}
404-
405-// stop releases all resources held by this filter chain: the interceptors
406-// instantiated for the current route configuration, and the references held
407-// on the server filters used by this filter chain.
```

Run and output (`verify/logs/c1_8f0663b7.log`):

```sh
FIXTURE=~/eval/tests/eval_xds_server_interceptor_leak_test.go bash verify/repro/c1/run_c1.sh ~/wt/8f0663b7 8f0663b7 125
```

```console
>> branch evalon/grpc-go-xd-8f0663b7 @ 05bb602b, test Test/ServerSideXDS_RouteConfigUpdate_ReleasesSupersededResources
>> step 0: baseline (unmodified)
$ go test -run '^Test$/^ServerSideXDS_RouteConfigUpdate_ReleasesSupersededResources$' ./test/xds -race -count=1
--- PASS: Test (0.08s)
    --- PASS: Test/ServerSideXDS_RouteConfigUpdate_ReleasesSupersededResources (0.07s)
PASS
ok  	google.golang.org/grpc/test/xds	1.111s
$ go test -run '^Test$/^Eval_' ./test/xds -race -count=1   # eval fixture, unmodified branch (reference for step 2)
--- FAIL: Test (4.15s)
    --- FAIL: Test/Eval_ServerSideXDS_InterceptorBeforeFilterCloseOrder (0.04s)
    --- PASS: Test/Eval_ServerSideXDS_InterceptorLeak_MultiGenerationRDSUpdate (0.02s)
    --- PASS: Test/Eval_ServerSideXDS_InterceptorLeak_RDSUpdate (0.03s)
    --- PASS: Test/Eval_ServerSideXDS_InterceptorSwapOrder (0.02s)
    --- FAIL: Test/Eval_ServerSideXDS_PartialRouteFailure_ClosesInterceptors (2.02s)
    --- FAIL: Test/Eval_ServerSideXDS_PartialVirtualHostFailure_ClosesInterceptors (2.02s)
FAIL
FAIL	google.golang.org/grpc/test/xds	4.185s
FAIL
>> step 1: natural stress (unmodified): 24 concurrent processes x 125 runs each of the -race test binary
$ go test -race -c -o /tmp/xds_8f0663b7.test ./test/xds && (cd test/xds && for i in $(seq 24); do /tmp/xds_8f0663b7.test -test.run '^Test$/^ServerSideXDS_RouteConfigUpdate_ReleasesSupersededResources$' -test.count=125 -test.v > /t
passes: 3000  failures: 0
failure messages (count, message):
>> step 2: mutation B (production: 100ms sleep between Swap() and closing the retired interceptors)
 internal/xds/server/filter_chain_manager.go | 5 ++++-
 1 file changed, 4 insertions(+), 1 deletion(-)
+	"time"
-	fc.usableRouteConfiguration.Swap(urc).close()
+	old := fc.usableRouteConfiguration.Swap(urc)
+	time.Sleep(100 * time.Millisecond) // VERIFY mutation B: scheduling delay only
+	old.close()
$ go test -run '^Test$/^ServerSideXDS_RouteConfigUpdate_ReleasesSupersededResources$' ./test/xds -race -count=1   # branch's own test
    xds_server_route_config_update_test.go:271: Update 2: created 3 interceptor instances, want: 4
--- FAIL: Test (0.43s)
    --- FAIL: Test/ServerSideXDS_RouteConfigUpdate_ReleasesSupersededResources (0.42s)
FAIL
FAIL	google.golang.org/grpc/test/xds	0.465s
FAIL
$ go test -run '^Test$/^Eval_' ./test/xds -race -count=1   # eval fixture (polls the destruction counter), same mutation
--- FAIL: Test (5.92s)
    --- FAIL: Test/Eval_ServerSideXDS_InterceptorBeforeFilterCloseOrder (0.23s)
    --- PASS: Test/Eval_ServerSideXDS_InterceptorLeak_MultiGenerationRDSUpdate (0.62s)
    --- PASS: Test/Eval_ServerSideXDS_InterceptorLeak_RDSUpdate (0.42s)
    --- PASS: Test/Eval_ServerSideXDS_InterceptorSwapOrder (0.41s)
    --- FAIL: Test/Eval_ServerSideXDS_PartialRouteFailure_ClosesInterceptors (2.12s)
    --- FAIL: Test/Eval_ServerSideXDS_PartialVirtualHostFailure_ClosesInterceptors (2.11s)
FAIL
FAIL	google.golang.org/grpc/test/xds	5.956s
FAIL
>> step 3: mutation A (test double only: trackingInterceptor.Close sleeps 100ms before counting itself destroyed)
 test/xds/xds_server_filter_state_retention_test.go | 1 +
 1 file changed, 1 insertion(+)
+	time.Sleep(100 * time.Millisecond) // VERIFY mutation A: slow Close
$ go test -run '^Test$/^ServerSideXDS_RouteConfigUpdate_ReleasesSupersededResources$' ./test/xds -race -count=1
    xds_server_route_config_update_test.go:271: Update 2: created 3 interceptor instances, want: 4
--- FAIL: Test (0.44s)
    --- FAIL: Test/ServerSideXDS_RouteConfigUpdate_ReleasesSupersededResources (0.44s)
FAIL
FAIL	google.golang.org/grpc/test/xds	0.475s
FAIL
>> step 4: mutation A2 (test double only: every 2nd trackingInterceptor.Close -- the last one of each two-filter-chain update -- sleeps 100ms before counting itself destroyed)
 test/xds/xds_server_filter_state_retention_test.go | 5 +++++
 1 file changed, 5 insertions(+)
+var verifyCloseCalls atomic.Int32
+
+	if verifyCloseCalls.Add(1)%2 == 0 {
+		time.Sleep(100 * time.Millisecond) // VERIFY mutation A2: last Close of the update is slow
+	}
$ go test -run '^Test$/^ServerSideXDS_RouteConfigUpdate_ReleasesSupersededResources$' ./test/xds -race -count=5
    xds_server_route_config_update_test.go:274: Update 2: destroyed 1 interceptor instances, want: 2
--- FAIL: Test (0.24s)
    --- FAIL: Test/ServerSideXDS_RouteConfigUpdate_ReleasesSupersededResources (0.23s)
    xds_server_route_config_update_test.go:274: Update 2: destroyed 1 interceptor instances, want: 2
--- FAIL: Test (0.23s)
    --- FAIL: Test/ServerSideXDS_RouteConfigUpdate_ReleasesSupersededResources (0.22s)
    xds_server_route_config_update_test.go:274: Update 2: destroyed 1 interceptor instances, want: 2
--- FAIL: Test (0.22s)
    --- FAIL: Test/ServerSideXDS_RouteConfigUpdate_ReleasesSupersededResources (0.22s)
    xds_server_route_config_update_test.go:274: Update 2: destroyed 1 interceptor instances, want: 2
--- FAIL: Test (0.23s)
    --- FAIL: Test/ServerSideXDS_RouteConfigUpdate_ReleasesSupersededResources (0.22s)
    xds_server_route_config_update_test.go:274: Update 2: destroyed 1 interceptor instances, want: 2
--- FAIL: Test (0.23s)
    --- FAIL: Test/ServerSideXDS_RouteConfigUpdate_ReleasesSupersededResources (0.22s)
FAIL
FAIL	google.golang.org/grpc/test/xds	1.181s
FAIL
(worktree restored to branch head)
```

The 3000-run stress in this particular run did not happen to hit the destruction-count assertion, so the stress step alone was repeated with 24 x 500 = 12000 unmodified runs (`verify/logs/c1_8f0663b7_stress2.log`):

```sh
STEPS="1" bash verify/repro/c1/run_c1.sh ~/wt/8f0663b7 8f0663b7 500
```

```console
>> branch evalon/grpc-go-xd-8f0663b7 @ 05bb602b, test Test/ServerSideXDS_RouteConfigUpdate_ReleasesSupersededResources
>> step 1: natural stress (unmodified): 24 concurrent processes x 500 runs each of the -race test binary
passes: 11969  failures: 31
failure messages (count, message):
      5 xds_server_route_config_update_test.go:271: Update 2: created 3 interceptor instances, want: 4
      2 xds_server_route_config_update_test.go:271: Update 3: created 5 interceptor instances, want: 6
      2 xds_server_route_config_update_test.go:271: Update 4: created 7 interceptor instances, want: 8
      4 xds_server_route_config_update_test.go:271: Update 5: created 9 interceptor instances, want: 10
      8 xds_server_route_config_update_test.go:274: Update 2: destroyed 1 interceptor instances, want: 2
      3 xds_server_route_config_update_test.go:274: Update 3: destroyed 3 interceptor instances, want: 4
      3 xds_server_route_config_update_test.go:274: Update 4: destroyed 5 interceptor instances, want: 6
      4 xds_server_route_config_update_test.go:274: Update 5: destroyed 7 interceptor instances, want: 8
(worktree restored to branch head)
```

### Branch [`evalon/grpc-go-xd-bbe58e09`](https://github.com/kaitranntt-evals/grpc-go-xds-rds-interceptor-lifecycle-leak/tree/evalon/grpc-go-xd-bbe58e09)

Added test `TestServerSideXDS_FilterStateRetention_AcrossUpdates_RouteConfigChange` in `test/xds/xds_server_filter_state_retention_test.go` (two filter chains (v4 + v6 wildcard)). Synchronization and assertion, verbatim with line numbers:

```go
813: 	for i := 1; i <= numUpdates; i++ {
814: 		wantPath := fmt.Sprintf("path-%d", i)
815: 		resources.Routes[serverRouteConfigIdx] = serverRouteConfig(wantPath)
816: 		if err := managementServer.Update(ctx, resources); err != nil {
817: 			t.Fatal(err)
818: 		}
819: 
820: 		// Wait for the updated RouteConfiguration to be applied on the gRPC
821: 		// server. RPCs must keep succeeding while the update is applied, since
822: 		// the Listener resource is unchanged and connections are not drained.
823: 	WaitForUpdatedConfig:
824: 		for ; ctx.Err() == nil; <-time.After(defaultTestShortTimeout) {
825: 			if _, err := client.EmptyCall(ctx, &testpb.Empty{}); err != nil {
826: 				t.Fatalf("EmptyCall() failed: %v", err)
827: 			}
828: 			select {
829: 			case cfg := <-pathCh:
830: 				if cfg == wantPath {
831: 					break WaitForUpdatedConfig
832: 				}
833: 			case <-ctx.Done():
834: 				t.Fatalf("Timeout waiting for interceptor get updated config")
835: 			}
836: 		}
837: 		if ctx.Err() != nil {
838: 			t.Fatalf("Timeout when waiting for updated config to be applied: %v", ctx.Err())
839: 		}
// ...
850: 		if got, want := interceptorsCreated.Load(), int32(2*(i+1)); got != want {
851: 			t.Fatalf("Update %d: created %d interceptor instances, want: %d", i, got, want)
852: 		}
853: 		if got, want := interceptorsDestroyed.Load(), int32(2*i); got != want {
854: 			t.Fatalf("Update %d: destroyed %d interceptor instances, want: %d", i, got, want)
855: 		}
```

Production ordering on this branch (`internal/xds/server/filter_chain_manager.go`): publish with `Swap`, then close the retired interceptors:

```go
406:func (fc *filterChain) setUsableRouteConfiguration(urc *usableRouteConfiguration) {
407-	if old := fc.usableRouteConfiguration.Swap(urc); old != nil {
408-		old.close()
409-	}
410-}
411-
412-// serverFilterProvider is used to get a ServerFilter.
```

Run and output (`verify/logs/c1_bbe58e09.log`):

```sh
FIXTURE=~/eval/tests/eval_xds_server_interceptor_leak_test.go bash verify/repro/c1/run_c1.sh ~/wt/bbe58e09 bbe58e09 125
```

```console
>> branch evalon/grpc-go-xd-bbe58e09 @ e8c1fbc6, test Test/ServerSideXDS_FilterStateRetention_AcrossUpdates_RouteConfigChange
>> step 0: baseline (unmodified)
$ go test -run '^Test$/^ServerSideXDS_FilterStateRetention_AcrossUpdates_RouteConfigChange$' ./test/xds -race -count=1
--- PASS: Test (0.08s)
    --- PASS: Test/ServerSideXDS_FilterStateRetention_AcrossUpdates_RouteConfigChange (0.07s)
PASS
ok  	google.golang.org/grpc/test/xds	1.111s
$ go test -run '^Test$/^Eval_' ./test/xds -race -count=1   # eval fixture, unmodified branch (reference for step 2)
--- FAIL: Test (4.19s)
    --- FAIL: Test/Eval_ServerSideXDS_InterceptorBeforeFilterCloseOrder (0.06s)
    --- PASS: Test/Eval_ServerSideXDS_InterceptorLeak_MultiGenerationRDSUpdate (0.03s)
    --- PASS: Test/Eval_ServerSideXDS_InterceptorLeak_RDSUpdate (0.03s)
    --- PASS: Test/Eval_ServerSideXDS_InterceptorSwapOrder (0.02s)
    --- FAIL: Test/Eval_ServerSideXDS_PartialRouteFailure_ClosesInterceptors (2.02s)
    --- FAIL: Test/Eval_ServerSideXDS_PartialVirtualHostFailure_ClosesInterceptors (2.03s)
FAIL
FAIL	google.golang.org/grpc/test/xds	4.218s
FAIL
>> step 1: natural stress (unmodified): 24 concurrent processes x 125 runs each of the -race test binary
$ go test -race -c -o /tmp/xds_bbe58e09.test ./test/xds && (cd test/xds && for i in $(seq 24); do /tmp/xds_bbe58e09.test -test.run '^Test$/^ServerSideXDS_FilterStateRetention_AcrossUpdates_RouteConfigChange$' -test.count=125 -test
passes: 2993  failures: 7
failure messages (count, message):
      3 xds_server_filter_state_retention_test.go:851: Update 1: created 3 interceptor instances, want: 4
      1 xds_server_filter_state_retention_test.go:854: Update 2: destroyed 3 interceptor instances, want: 4
      3 xds_server_filter_state_retention_test.go:854: Update 3: destroyed 5 interceptor instances, want: 6
>> step 2: mutation B (production: 100ms sleep between Swap() and closing the retired interceptors)
 internal/xds/server/filter_chain_manager.go | 2 ++
 1 file changed, 2 insertions(+)
+	"time"
+		time.Sleep(100 * time.Millisecond) // VERIFY mutation B: scheduling delay only
$ go test -run '^Test$/^ServerSideXDS_FilterStateRetention_AcrossUpdates_RouteConfigChange$' ./test/xds -race -count=1   # branch's own test
    xds_server_filter_state_retention_test.go:851: Update 1: created 3 interceptor instances, want: 4
--- FAIL: Test (0.43s)
    --- FAIL: Test/ServerSideXDS_FilterStateRetention_AcrossUpdates_RouteConfigChange (0.42s)
FAIL
FAIL	google.golang.org/grpc/test/xds	0.462s
FAIL
$ go test -run '^Test$/^Eval_' ./test/xds -race -count=1   # eval fixture (polls the destruction counter), same mutation
--- FAIL: Test (5.94s)
    --- FAIL: Test/Eval_ServerSideXDS_InterceptorBeforeFilterCloseOrder (0.23s)
    --- PASS: Test/Eval_ServerSideXDS_InterceptorLeak_MultiGenerationRDSUpdate (0.62s)
    --- PASS: Test/Eval_ServerSideXDS_InterceptorLeak_RDSUpdate (0.42s)
    --- PASS: Test/Eval_ServerSideXDS_InterceptorSwapOrder (0.41s)
    --- FAIL: Test/Eval_ServerSideXDS_PartialRouteFailure_ClosesInterceptors (2.12s)
    --- FAIL: Test/Eval_ServerSideXDS_PartialVirtualHostFailure_ClosesInterceptors (2.12s)
FAIL
FAIL	google.golang.org/grpc/test/xds	5.970s
FAIL
>> step 3: mutation A (test double only: trackingInterceptor.Close sleeps 100ms before counting itself destroyed)
 test/xds/xds_server_filter_state_retention_test.go | 1 +
 1 file changed, 1 insertion(+)
+	time.Sleep(100 * time.Millisecond) // VERIFY mutation A: slow Close
$ go test -run '^Test$/^ServerSideXDS_FilterStateRetention_AcrossUpdates_RouteConfigChange$' ./test/xds -race -count=1
    xds_server_filter_state_retention_test.go:852: Update 1: created 3 interceptor instances, want: 4
--- FAIL: Test (0.44s)
    --- FAIL: Test/ServerSideXDS_FilterStateRetention_AcrossUpdates_RouteConfigChange (0.44s)
FAIL
FAIL	google.golang.org/grpc/test/xds	0.475s
FAIL
>> step 4: mutation A2 (test double only: every 2nd trackingInterceptor.Close -- the last one of each two-filter-chain update -- sleeps 100ms before counting itself destroyed)
 test/xds/xds_server_filter_state_retention_test.go | 5 +++++
 1 file changed, 5 insertions(+)
+var verifyCloseCalls atomic.Int32
+
+	if verifyCloseCalls.Add(1)%2 == 0 {
+		time.Sleep(100 * time.Millisecond) // VERIFY mutation A2: last Close of the update is slow
+	}
$ go test -run '^Test$/^ServerSideXDS_FilterStateRetention_AcrossUpdates_RouteConfigChange$' ./test/xds -race -count=5
    xds_server_filter_state_retention_test.go:859: Update 1: destroyed 1 interceptor instances, want: 2
--- FAIL: Test (0.25s)
    --- FAIL: Test/ServerSideXDS_FilterStateRetention_AcrossUpdates_RouteConfigChange (0.24s)
    xds_server_filter_state_retention_test.go:859: Update 1: destroyed 1 interceptor instances, want: 2
--- FAIL: Test (0.23s)
    --- FAIL: Test/ServerSideXDS_FilterStateRetention_AcrossUpdates_RouteConfigChange (0.22s)
    xds_server_filter_state_retention_test.go:859: Update 1: destroyed 1 interceptor instances, want: 2
--- FAIL: Test (0.23s)
    --- FAIL: Test/ServerSideXDS_FilterStateRetention_AcrossUpdates_RouteConfigChange (0.22s)
    xds_server_filter_state_retention_test.go:859: Update 1: destroyed 1 interceptor instances, want: 2
--- FAIL: Test (0.22s)
    --- FAIL: Test/ServerSideXDS_FilterStateRetention_AcrossUpdates_RouteConfigChange (0.22s)
    xds_server_filter_state_retention_test.go:859: Update 1: destroyed 1 interceptor instances, want: 2
--- FAIL: Test (0.23s)
    --- FAIL: Test/ServerSideXDS_FilterStateRetention_AcrossUpdates_RouteConfigChange (0.22s)
FAIL
FAIL	google.golang.org/grpc/test/xds	1.185s
FAIL
(worktree restored to branch head)
```

### Branch [`evalon/grpc-go-xd-295929ad`](https://github.com/kaitranntt-evals/grpc-go-xds-rds-interceptor-lifecycle-leak/tree/evalon/grpc-go-xd-295929ad)

Added test `TestServerSideXDS_FilterStateRetention_AcrossUpdates_RouteConfigChange` in `test/xds/xds_server_filter_state_retention_test.go` (single default filter chain). Synchronization and assertion, verbatim with line numbers:

```go
778: 	for i := 1; i <= numUpdates; i++ {
779: 		wantPath := fmt.Sprintf("path-%d", i)
780: 		resources.Routes[serverRouteConfigIdx] = routeConfigForPath(wantPath)
781: 		if err := managementServer.Update(ctx, resources); err != nil {
782: 			t.Fatal(err)
783: 		}
784: 
785: 		// Wait for the updated RouteConfiguration to be applied on the server.
786: 	WaitForUpdatedConfig:
787: 		for ; ctx.Err() == nil; <-time.After(defaultTestShortTimeout) {
788: 			if _, err := client.EmptyCall(ctx, &testpb.Empty{}); err != nil {
789: 				t.Fatalf("EmptyCall() failed: %v", err)
790: 			}
791: 			select {
792: 			case cfg := <-pathCh:
793: 				if cfg == wantPath {
794: 					break WaitForUpdatedConfig
795: 				}
796: 			case <-ctx.Done():
797: 				t.Fatalf("Timeout waiting for interceptor get updated config")
798: 			}
799: 		}
800: 		if ctx.Err() != nil {
801: 			t.Fatalf("Timeout when waiting for updated config to be applied: %v", ctx.Err())
802: 		}
// ...
813: 		if got, want := interceptorsCreated.Load(), int32(i+1); got != want {
814: 			t.Fatalf("Update %d: created %d interceptor instances, want: %d", i, got, want)
815: 		}
816: 		if got, want := interceptorsDestroyed.Load(), int32(i); got != want {
817: 			t.Fatalf("Update %d: destroyed %d interceptor instances, want: %d", i, got, want)
818: 		}
```

Production ordering on this branch (`internal/xds/server/filter_chain_manager.go`): publish with `Swap`, then close the retired interceptors:

```go
410:func (fc *filterChain) storeUsableRouteConfiguration(urc *usableRouteConfiguration) {
411-	fc.usableRouteConfiguration.Swap(urc).close()
412-}
413-
414-// serverFilterProvider is used to get a ServerFilter.
415-//
416-// This functionality is provided by the listener wrapper, which maintains a map
```

Run and output (`verify/logs/c1_295929ad.log`):

```sh
FIXTURE=~/eval/tests/eval_xds_server_interceptor_leak_test.go bash verify/repro/c1/run_c1.sh ~/wt/295929ad 295929ad 125
```

```console
>> branch evalon/grpc-go-xd-295929ad @ 52eff9ad, test Test/ServerSideXDS_FilterStateRetention_AcrossUpdates_RouteConfigChange
>> step 0: baseline (unmodified)
$ go test -run '^Test$/^ServerSideXDS_FilterStateRetention_AcrossUpdates_RouteConfigChange$' ./test/xds -race -count=1
--- PASS: Test (0.09s)
    --- PASS: Test/ServerSideXDS_FilterStateRetention_AcrossUpdates_RouteConfigChange (0.09s)
PASS
ok  	google.golang.org/grpc/test/xds	1.128s
$ go test -run '^Test$/^Eval_' ./test/xds -race -count=1   # eval fixture, unmodified branch (reference for step 2)
--- FAIL: Test (2.18s)
    --- FAIL: Test/Eval_ServerSideXDS_InterceptorBeforeFilterCloseOrder (0.04s)
    --- PASS: Test/Eval_ServerSideXDS_InterceptorLeak_MultiGenerationRDSUpdate (0.03s)
    --- PASS: Test/Eval_ServerSideXDS_InterceptorLeak_RDSUpdate (0.03s)
    --- PASS: Test/Eval_ServerSideXDS_InterceptorSwapOrder (0.03s)
    --- FAIL: Test/Eval_ServerSideXDS_PartialRouteFailure_ClosesInterceptors (2.03s)
    --- PASS: Test/Eval_ServerSideXDS_PartialVirtualHostFailure_ClosesInterceptors (0.03s)
FAIL
FAIL	google.golang.org/grpc/test/xds	2.219s
FAIL
>> step 1: natural stress (unmodified): 24 concurrent processes x 125 runs each of the -race test binary
$ go test -race -c -o /tmp/xds_295929ad.test ./test/xds && (cd test/xds && for i in $(seq 24); do /tmp/xds_295929ad.test -test.run '^Test$/^ServerSideXDS_FilterStateRetention_AcrossUpdates_RouteConfigChange$' -test.count=125 -test
passes: 3000  failures: 0
failure messages (count, message):
>> step 2: mutation B (production: 100ms sleep between Swap() and closing the retired interceptors)
 internal/xds/server/filter_chain_manager.go | 5 ++++-
 1 file changed, 4 insertions(+), 1 deletion(-)
+	"time"
-	fc.usableRouteConfiguration.Swap(urc).close()
+	old := fc.usableRouteConfiguration.Swap(urc)
+	time.Sleep(100 * time.Millisecond) // VERIFY mutation B: scheduling delay only
+	old.close()
$ go test -run '^Test$/^ServerSideXDS_FilterStateRetention_AcrossUpdates_RouteConfigChange$' ./test/xds -race -count=1   # branch's own test
    xds_server_filter_state_retention_test.go:817: Update 1: destroyed 0 interceptor instances, want: 1
--- FAIL: Test (0.23s)
    --- FAIL: Test/ServerSideXDS_FilterStateRetention_AcrossUpdates_RouteConfigChange (0.22s)
FAIL
FAIL	google.golang.org/grpc/test/xds	0.259s
FAIL
$ go test -run '^Test$/^Eval_' ./test/xds -race -count=1   # eval fixture (polls the destruction counter), same mutation
--- FAIL: Test (4.04s)
    --- FAIL: Test/Eval_ServerSideXDS_InterceptorBeforeFilterCloseOrder (0.23s)
    --- PASS: Test/Eval_ServerSideXDS_InterceptorLeak_MultiGenerationRDSUpdate (0.62s)
    --- PASS: Test/Eval_ServerSideXDS_InterceptorLeak_RDSUpdate (0.42s)
    --- PASS: Test/Eval_ServerSideXDS_InterceptorSwapOrder (0.42s)
    --- FAIL: Test/Eval_ServerSideXDS_PartialRouteFailure_ClosesInterceptors (2.12s)
    --- PASS: Test/Eval_ServerSideXDS_PartialVirtualHostFailure_ClosesInterceptors (0.22s)
FAIL
FAIL	google.golang.org/grpc/test/xds	4.068s
FAIL
>> step 3: mutation A (test double only: trackingInterceptor.Close sleeps 100ms before counting itself destroyed)
 test/xds/xds_server_filter_state_retention_test.go | 1 +
 1 file changed, 1 insertion(+)
+	time.Sleep(100 * time.Millisecond) // VERIFY mutation A: slow Close
$ go test -run '^Test$/^ServerSideXDS_FilterStateRetention_AcrossUpdates_RouteConfigChange$' ./test/xds -race -count=1
    xds_server_filter_state_retention_test.go:818: Update 1: destroyed 0 interceptor instances, want: 1
--- FAIL: Test (0.24s)
    --- FAIL: Test/ServerSideXDS_FilterStateRetention_AcrossUpdates_RouteConfigChange (0.23s)
FAIL
FAIL	google.golang.org/grpc/test/xds	0.273s
FAIL
>> step 4: mutation A2 (test double only: every 2nd trackingInterceptor.Close -- the last one of each two-filter-chain update -- sleeps 100ms before counting itself destroyed)
(not applicable: this branch's test uses a single filter chain, so mutation A already isolates the destruction assertion)
(worktree restored to branch head)
```

The 3000-run stress in this particular run did not happen to hit the destruction-count assertion, so the stress step alone was repeated with 24 x 500 = 12000 unmodified runs (`verify/logs/c1_295929ad_stress2.log`):

```sh
STEPS="1" bash verify/repro/c1/run_c1.sh ~/wt/295929ad 295929ad 500
```

```console
>> branch evalon/grpc-go-xd-295929ad @ 52eff9ad, test Test/ServerSideXDS_FilterStateRetention_AcrossUpdates_RouteConfigChange
>> step 1: natural stress (unmodified): 24 concurrent processes x 500 runs each of the -race test binary
passes: 11994  failures: 6
failure messages (count, message):
      3 xds_server_filter_state_retention_test.go:817: Update 1: destroyed 0 interceptor instances, want: 1
      1 xds_server_filter_state_retention_test.go:817: Update 2: destroyed 1 interceptor instances, want: 2
      1 xds_server_filter_state_retention_test.go:817: Update 3: destroyed 2 interceptor instances, want: 3
      1 xds_server_filter_state_retention_test.go:817: Update 4: destroyed 3 interceptor instances, want: 4
(worktree restored to branch head)
```

### Reading of the C1 results

| Branch | Unsynchronized destruction assertion (file:line) | Natural failures of that assertion, unmodified code + test, under CPU contention | Deterministic failure of that assertion when retirement is delayed |
|---|---|---|---|
| `8de853fe` | `xds_server_filter_state_retention_test.go:786` (`created - destroyed == 1`) | 4 / 3000 | mutation B and mutation A |
| `1ea7dd6e` | `xds_server_route_config_update_test.go:273` (`destroyed == 2*i`) | 1 / 3000 (plus 4 on the adjacent created-count assertion, same missing wait) | mutation A2 (5/5 runs) |
| `8f0663b7` | `xds_server_route_config_update_test.go:273` (`destroyed == 2*(i-1)`) | 0 / 3000, then 18 / 12000 (plus 13 on the created-count assertion) | mutation A2 (5/5 runs) |
| `bbe58e09` | `xds_server_filter_state_retention_test.go:853` (`destroyed == 2*i`) | 4 / 3000 (plus 3 on the created-count assertion) | mutation A2 (5/5 runs) |
| `295929ad` | `xds_server_filter_state_retention_test.go:816` (`destroyed == i`) | 0 / 3000, then 6 / 12000 | mutation B and mutation A |

- On every branch the assertion text follows a replacement-traffic observation (`waitForPath` / `waitForRouteConfigPath` / the `WaitForUpdatedConfig` loop) with no deadline-bounded poll of the destruction counter and no destruction-completion event.
- On every branch that observation does not establish that retirement finished: the unmodified test fails on the destruction-count assertion on unmodified production code when the machine is busy, and fails deterministically as soon as retirement takes 100ms (either as a scheduling delay in production between `Swap` and `Close`, or as a slow `Close` in the test double).
- Control: under the same production delay (mutation B) the eval fixture's replacement tests (`Eval_ServerSideXDS_InterceptorLeak_RDSUpdate`, `..._MultiGenerationRDSUpdate`, `Eval_ServerSideXDS_InterceptorSwapOrder`), which poll the destruction counter until a deadline, pass on every branch. So the product behaviour is still correct under the delay; only the branch's own test's missing wait makes it fail. The fixture tests that fail on these branches (`InterceptorBeforeFilterCloseOrder`, `PartialRouteFailure`, and on four branches `PartialVirtualHostFailure`) fail identically at baseline without any mutation, so they are unrelated to C1 and were not investigated here.
- On the three two-chain branches, plain mutations A/B trip the *created*-count assertion one statement earlier (the second filter chain has not been rebuilt yet when traffic on the first chain is observed); that is the same missing wait, and mutation A2 / the natural runs show the destruction assertion itself failing.
- The unit tests added under `internal/xds/server` on these branches call `handleRDSUpdate` synchronously (or, on `1ea7dd6e`, poll with `waitForLifecycleCounts`), so they are not affected; the problem is confined to the one integration test per branch listed above.
- Uncontended single runs pass on every branch (step 0), which is why the gap is invisible in a normal local run.

**Impact reasoning.** The regression test each branch adds for the leak is flaky by construction: it passes locally, but on a busy runner it fails at a rate measured here between roughly 0.03% and 0.15% per run on the destruction assertion (3x CPU oversubscription, `-race`), with the message that interceptors were not destroyed -- i.e. it reports the very leak the change fixes when no leak exists. Anyone rerunning or bisecting on such a failure is sent after a non-existent product bug. The fix is local to each test: poll `interceptorsDestroyed` (and `interceptorsCreated`) until the expected value or a deadline, as the eval fixture does, instead of asserting immediately after `waitForPath`.

## C2

**Claim:** on [`evalon/grpc-go-xd-423f7ef0`](https://github.com/kaitranntt-evals/grpc-go-xds-rds-interceptor-lifecycle-leak/tree/evalon/grpc-go-xd-423f7ef0), `Stop()` deadlocks when an in-flight interceptor's `AllowRPC` waits for its RPC context to be cancelled, even though the interceptor's `Close` returns immediately. **Observed result: holds (both parts).**

### Setup (self-contained)

```sh
cd ~/repos/grpc-go
git fetch origin grpc-go-xds-rds-interceptor-lifecycle-leak-perfect
git checkout -b verify/grpc-go-xds-rds-interceptor-lifecycle-leak-v-389b3566 origin/grpc-go-xds-rds-interceptor-lifecycle-leak-perfect
git remote add claims https://github.com/kaitranntt-evals/grpc-go-xds-rds-interceptor-lifecycle-leak   # in this session: the same repo via the VM's git proxy
for b in 8de853fe 1ea7dd6e 8f0663b7 bbe58e09 295929ad 423f7ef0; do
  git fetch claims evalon/grpc-go-xd-$b:refs/remotes/claims/evalon/grpc-go-xd-$b
  git worktree add --detach ~/wt/$b claims/evalon/grpc-go-xd-$b
done
git worktree add --detach ~/wt/perfect origin/grpc-go-xds-rds-interceptor-lifecycle-leak-perfect   # control
git worktree add --detach ~/wt/base 4ee6ac46fada69c06576cee108b009689a000520                      # control (task base commit)
mkdir -p ~/eval && unzip -o eval_tests.zip -d ~/eval    # fixture: ~/eval/tests/eval_xds_server_interceptor_leak_test.go
```

### Relevant code on the branch (for orientation only; the verdict rests on the runs below)

`internal/xds/server/routing.go` -- `RouteAndProcess` holds the read lock for its whole body, including `AllowRPC`:

```go
46: 	// Keep the configuration's interceptors alive until route processing ends.
47: 	cw.urc.mu.RLock()
48: 	defer cw.urc.mu.RUnlock()
49: 	rc := cw.urc.current
// ...
98: 	if err := rwi.interceptor.AllowRPC(ctx); err != nil {
99: 		return rc.statusErrWithNodeID(codes.PermissionDenied, "Incoming RPC is not allowed: %v", err)
100: 	}
```

`internal/xds/server/filter_chain_manager.go` -- closing the routing configuration needs the write lock:

```go
123: func (fcm *filterChainManager) stop() {
124: 	for _, fc := range fcm.filterChains {
125: 		fc.usableRouteConfiguration.close()
126: 	}
127: }
// ...
178: func (rc *routingConfiguration) close() {
179: 	rc.mu.Lock()
180: 	defer rc.mu.Unlock()
181: 	rc.current.close()
182: }
```

`server.go` -- `Stop` closes listeners (which reaches `listenerWrapper.Close` -> `filterChainManager.stop`) *before* it closes server transports, and closing the transports is what cancels in-flight RPC contexts:

```go
1946: func (s *Server) stop(graceful bool) {
1947: 	s.quit.Fire()
1948: 	defer s.done.Fire()
1949: 
1950: 	s.channelzRemoveOnce.Do(func() { channelz.RemoveEntry(s.channelz.ID) })
1951: 	s.mu.Lock()
1952: 	s.closeListenersLocked()
1953: 	// Wait for serving threads to be ready to exit.  Only then can we be sure no
1954: 	// new conns will be created.
1955: 	s.mu.Unlock()
1956: 	s.serveWG.Wait()
1957: 
1958: 	s.mu.Lock()
1959: 	defer s.mu.Unlock()
1960: 
1961: 	if graceful {
1962: 		s.drainAllServerTransportsLocked()
1963: 	} else {
1964: 		s.closeServerTransportsLocked()
1965: 	}
```

### Repro

`verify/repro/verify_c2_c3_routing_lock_test.go`, test `Test/Verify_C2_StopWithAllowRPCAwaitingContextCancel`: a real `xds.NewGRPCServer` + xDS management server + gRPC client; a test HTTP filter whose interceptor's `AllowRPC` does only `<-ctx.Done()` and whose `Close` returns immediately. One RPC is started (no client deadline shorter than the test), the test waits until `AllowRPC` has been entered, calls `Stop()` exactly once, and observes for a bounded window (default 5s, `VERIFY_WINDOW` overrides). If `Stop()` is still blocked it dumps the goroutines on the routing/shutdown paths, then cancels the RPC from the client so the test can clean up.

```sh
cp verify/repro/verify_c2_c3_routing_lock_test.go ~/wt/423f7ef0/test/xds/
cd ~/wt/423f7ef0 && go test -v -run '^Test$/^Verify_C2_' ./test/xds -race -count=1
```

Output (gRPC `tlogger` INFO/WARNING noise removed; `verify/logs/c2_423f7ef0.filtered.log`):

```console
=== RUN   Test
=== RUN   Test/Verify_C2_StopWithAllowRPCAwaitingContextCancel
    xds_server_integration_test.go:61: Serving mode for listener "127.0.0.1:46037" changed to "SERVING", err: <nil>
    verify_c2_c3_routing_lock_test.go:319: [t+ 0.015s] AllowRPC entered; it is now waiting for its RPC context to be cancelled
    verify_c2_c3_routing_lock_test.go:326: [t+ 0.015s] calling Stop() once
    verify_c2_c3_routing_lock_test.go:347: [t+ 5.016s] OBSERVATION: Stop() still blocked 5s after it was called; RPC context cancelled by server = false; interceptor Close calls so far = []
    verify_c2_c3_routing_lock_test.go:348: goroutines on the xDS routing / shutdown paths while Stop() is blocked:
        goroutine 125 [sync.RWMutex.Lock]:
        sync.runtime_SemacquireRWMutex(0xc000826094?, 0x1?, 0x1499d05?)
        	/usr/local/go/src/runtime/sema.go:105 +0x25
        sync.(*RWMutex).Lock(0xc000826080)
        	/usr/local/go/src/sync/rwmutex.go:155 +0x89
        google.golang.org/grpc/internal/xds/server.(*routingConfiguration).close(0xc000826080)
        	/home/ubuntu/wt/423f7ef0/internal/xds/server/filter_chain_manager.go:179 +0x31
        google.golang.org/grpc/internal/xds/server.(*filterChainManager).stop(...)
        	/home/ubuntu/wt/423f7ef0/internal/xds/server/filter_chain_manager.go:125
        google.golang.org/grpc/internal/xds/server.(*listenerWrapper).Close(0xc000020600)
        	/home/ubuntu/wt/423f7ef0/internal/xds/server/listener_wrapper.go:361 +0x1a5
        google.golang.org/grpc.(*listenSocket).Close(0xc000012fd8)
        	/home/ubuntu/wt/423f7ef0/server.go:862 +0x4b
        google.golang.org/grpc.(*Server).closeListenersLocked(...)
        	/home/ubuntu/wt/423f7ef0/server.go:2014
        google.golang.org/grpc.(*Server).stop(0xc0000e6d88, 0x0)
        	/home/ubuntu/wt/423f7ef0/server.go:1952 +0x25a
        google.golang.org/grpc.(*Server).Stop(0xc0000e6d88)
        	/home/ubuntu/wt/423f7ef0/server.go:1936 +0x29
        google.golang.org/grpc/xds.(*GRPCServer).Stop(0xc0004dbc40)
        	/home/ubuntu/wt/423f7ef0/xds/server.go:216 +0x8f
        google.golang.org/grpc/test/xds_test.setupGRPCServer.func4()
        	/home/ubuntu/wt/423f7ef0/test/xds/xds_server_integration_test.go:147 +0x48
        sync.(*Once).doSlow(0xc0000ac964, 0xc00019e1a0)
        	/usr/local/go/src/sync/once.go:78 +0xd2
        sync.(*Once).Do(0xc0000ac964, 0xc00019e1a0)
        	/usr/local/go/src/sync/once.go:69 +0x45
        google.golang.org/grpc/test/xds_test.verifySetup.func3()
        	/home/ubuntu/wt/423f7ef0/test/xds/verify_c2_c3_routing_lock_test.go:269 +0x39
        google.golang.org/grpc/test/xds_test.s.TestVerify_C2_StopWithAllowRPCAwaitingContextCancel.func5()
        	/home/ubuntu/wt/423f7ef0/test/xds/verify_c2_c3_routing_lock_test.go:328 +0x48
        created by google.golang.org/grpc/test/xds_test.s.TestVerify_C2_StopWithAllowRPCAwaitingContextCancel in goroutine 7
        	/home/ubuntu/wt/423f7ef0/test/xds/verify_c2_c3_routing_lock_test.go:327 +0x8fd
        goroutine 133 [chan receive]:
        google.golang.org/grpc/test/xds_test.s.TestVerify_C2_StopWithAllowRPCAwaitingContextCancel.func1({0x1f69970, 0xc000333890}, {0xc0001b6700?, 0xc0001b670f?})
        	/home/ubuntu/wt/423f7ef0/test/xds/verify_c2_c3_routing_lock_test.go:295 +0x6b
        google.golang.org/grpc/test/xds_test.(*verifyInterceptor).AllowRPC(0xc00070a3a8, {0x1f69970, 0xc000333890})
        	/home/ubuntu/wt/423f7ef0/test/xds/verify_c2_c3_routing_lock_test.go:131 +0x94
        google.golang.org/grpc/internal/xds/server.(*interceptorList).AllowRPC(0xc00070a498, {0x1f69970, 0xc000333890})
        	/home/ubuntu/wt/423f7ef0/internal/xds/server/filter_chain_manager.go:536 +0xa2
        google.golang.org/grpc/internal/xds/server.RouteAndProcess({0x1f69970, 0xc000333890})
        	/home/ubuntu/wt/423f7ef0/internal/xds/server/routing.go:98 +0xbb7
        google.golang.org/grpc/xds.xdsUnaryInterceptor({0x1f69970, 0xc000333890}, {0x1b9d1c0, 0xc0003338c0}, 0xc000333890?, 0xc00070a150)
        	/home/ubuntu/wt/423f7ef0/xds/server.go:236 +0x47
        google.golang.org/grpc/interop/grpc_testing._TestService_EmptyCall_Handler({0x1cca1e0, 0xc000020480}, {0x1f69970, 0xc000333890}, 0xc000147000, 0x1dc7808)
        	/home/ubuntu/wt/423f7ef0/interop/grpc_testing/test_grpc.pb.go:293 +0x1e7
        google.golang.org/grpc.(*Server).processUnaryRPC(0xc0000e6d88, {0x1f69970, 0xc000333800}, 0xc0001ef860, 0xc000129680, 0x2b09ce0, 0x0)
        	/home/ubuntu/wt/423f7ef0/server.go:1441 +0x1a6a
        google.golang.org/grpc.(*Server).handleStream(0xc0000e6d88, {0x1f6e958, 0xc0006ac340}, 0xc0001ef860)
        	/home/ubuntu/wt/423f7ef0/server.go:1850 +0x10b3
        google.golang.org/grpc.(*Server).serveStreams.func2.1()
        	/home/ubuntu/wt/423f7ef0/server.go:1076 +0x14a
        created by google.golang.org/grpc.(*Server).serveStreams.func2 in goroutine 86
        	/home/ubuntu/wt/423f7ef0/server.go:1087 +0x213
    verify_c2_c3_routing_lock_test.go:354: [t+ 5.017s] cancelling the RPC from the client side to break the wait cycle
    verify_c2_c3_routing_lock_test.go:358: [t+ 5.018s] Stop() returned only after the client cancelled the RPC (5.003s after Stop() was called)
    verify_c2_c3_routing_lock_test.go:365: [t+ 5.018s] in-flight RPC finished with: rpc error: code = Canceled desc = context canceled
    verify_c2_c3_routing_lock_test.go:371: [t+ 5.018s] server-side RPC context was cancelled
    verify_c2_c3_routing_lock_test.go:376: Stop() did not return within 5s while AllowRPC was waiting for RPC context cancellation (Close returns immediately)
--- FAIL: Test (5.03s)
    --- FAIL: Test/Verify_C2_StopWithAllowRPCAwaitingContextCancel (5.03s)
FAIL
FAIL	google.golang.org/grpc/test/xds	5.066s
FAIL
exit=1
```

What the trace shows, per part:

- *routing_reader_wait*: goroutine `[sync.RWMutex.Lock]` is in `(*routingConfiguration).close` (`filter_chain_manager.go:179`), while the RPC goroutine is inside `RouteAndProcess` (`routing.go:98`) -> `(*interceptorList).AllowRPC` -> the test interceptor's `<-ctx.Done()`; i.e. the reader holds `RLock` for the whole of `AllowRPC` and the closer waits for it. `interceptor Close calls so far = []` -- `Close` was never even reached, so its speed is irrelevant.
- *shutdown_trigger*: the blocked goroutine's stack is `xds.(*GRPCServer).Stop` -> `grpc.(*Server).Stop` -> `(*Server).stop` (`server.go:1952`, `closeListenersLocked`) -> `(*listenerWrapper).Close` -> `(*filterChainManager).stop` -> `(*routingConfiguration).close`. It has not reached `closeServerTransportsLocked` (`server.go:1964`), and the test reports `RPC context cancelled by server = false` for the whole window. The cycle is: `Stop` waits for the routing write lock -> held off by the reader in `AllowRPC` -> which waits for RPC-context cancellation -> which `Stop` would only deliver after the step it is blocked in. `Stop()` returned 1ms after the *client* cancelled the RPC.

Longer window and repetition (same branch, same test; `verify/logs/c2c3_423f7ef0_30s.filtered.log`, `verify/logs/c2c3_423f7ef0_x3.filtered.log`):

```sh
cd ~/wt/423f7ef0 && VERIFY_WINDOW=30s go test -v -run '^Test$/^Verify_(C2|C3)_' ./test/xds -race -count=1
cd ~/wt/423f7ef0 && go test -v -run '^Test$/^Verify_(C2|C3)_' ./test/xds -race -count=3
```

```console
    verify_c2_c3_routing_lock_test.go:319: [t+ 0.020s] AllowRPC entered; it is now waiting for its RPC context to be cancelled
    verify_c2_c3_routing_lock_test.go:326: [t+ 0.021s] calling Stop() once
    verify_c2_c3_routing_lock_test.go:347: [t+30.021s] OBSERVATION: Stop() still blocked 30s after it was called; RPC context cancelled by server = false; interceptor Close calls so far = []
    verify_c2_c3_routing_lock_test.go:348: goroutines on the xDS routing / shutdown paths while Stop() is blocked:
        google.golang.org/grpc/test/xds_test.s.TestVerify_C2_StopWithAllowRPCAwaitingContextCancel.func1({0x1f907f0, 0xc0006c8450}, {0xc0003e5f40?, 0xc0003e5f4f?})
        google.golang.org/grpc/test/xds_test.s.TestVerify_C2_StopWithAllowRPCAwaitingContextCancel.func5()
        created by google.golang.org/grpc/test/xds_test.s.TestVerify_C2_StopWithAllowRPCAwaitingContextCancel in goroutine 8
    verify_c2_c3_routing_lock_test.go:354: [t+30.023s] cancelling the RPC from the client side to break the wait cycle
    verify_c2_c3_routing_lock_test.go:358: [t+30.024s] Stop() returned only after the client cancelled the RPC (30.003s after Stop() was called)
    verify_c2_c3_routing_lock_test.go:365: [t+30.024s] in-flight RPC finished with: rpc error: code = Canceled desc = context canceled
    verify_c2_c3_routing_lock_test.go:371: [t+30.024s] server-side RPC context was cancelled
    verify_c2_c3_routing_lock_test.go:376: Stop() did not return within 30s while AllowRPC was waiting for RPC context cancellation (Close returns immediately)
    --- FAIL: Test/Verify_C2_StopWithAllowRPCAwaitingContextCancel (30.04s)
# -count=3:
    verify_c2_c3_routing_lock_test.go:347: [t+ 5.035s] OBSERVATION: Stop() still blocked 5s after it was called; RPC context cancelled by server = false; interceptor Close calls so far = []
    --- FAIL: Test/Verify_C2_StopWithAllowRPCAwaitingContextCancel (5.05s)
    verify_c2_c3_routing_lock_test.go:347: [t+ 5.017s] OBSERVATION: Stop() still blocked 5s after it was called; RPC context cancelled by server = false; interceptor Close calls so far = []
    --- FAIL: Test/Verify_C2_StopWithAllowRPCAwaitingContextCancel (5.03s)
    verify_c2_c3_routing_lock_test.go:347: [t+ 5.010s] OBSERVATION: Stop() still blocked 5s after it was called; RPC context cancelled by server = false; interceptor Close calls so far = []
    --- FAIL: Test/Verify_C2_StopWithAllowRPCAwaitingContextCancel (5.02s)
```

Controls -- the identical test file on the task base commit `4ee6ac46` and on the audited reference branch [`grpc-go-xds-rds-interceptor-lifecycle-leak-perfect`](https://github.com/kaitranntt-evals/grpc-go/tree/grpc-go-xds-rds-interceptor-lifecycle-leak-perfect) (`verify/logs/c2_base.filtered.log`, `verify/logs/c2c3_perfect.filtered.log`):

```sh
cp verify/repro/verify_c2_c3_routing_lock_test.go ~/wt/base/test/xds/    && (cd ~/wt/base    && go test -v -run '^Test$/^Verify_C2_' ./test/xds -race -count=1)
cp verify/repro/verify_c2_c3_routing_lock_test.go ~/wt/perfect/test/xds/ && (cd ~/wt/perfect && go test -v -run '^Test$/^Verify_(C2|C3)_' ./test/xds -race -count=1)
```

```console
# base 4ee6ac46
    verify_c2_c3_routing_lock_test.go:319: [t+ 0.016s] AllowRPC entered; it is now waiting for its RPC context to be cancelled
    verify_c2_c3_routing_lock_test.go:326: [t+ 0.016s] calling Stop() once
    verify_c2_c3_routing_lock_test.go:335: [t+ 0.017s] Stop() returned after 1ms
    verify_c2_c3_routing_lock_test.go:365: [t+ 0.018s] in-flight RPC finished with: rpc error: code = Unavailable desc = error reading from server: EOF
    verify_c2_c3_routing_lock_test.go:371: [t+ 0.018s] server-side RPC context was cancelled
    --- PASS: Test/Verify_C2_StopWithAllowRPCAwaitingContextCancel (0.03s)
ok  	google.golang.org/grpc/test/xds	1.070s
exit=0
# grpc-go-xds-rds-interceptor-lifecycle-leak-perfect
    verify_c2_c3_routing_lock_test.go:319: [t+ 0.016s] AllowRPC entered; it is now waiting for its RPC context to be cancelled
    verify_c2_c3_routing_lock_test.go:326: [t+ 0.016s] calling Stop() once
    verify_c2_c3_routing_lock_test.go:335: [t+ 0.017s] Stop() returned after 1ms
    verify_c2_c3_routing_lock_test.go:365: [t+ 0.017s] in-flight RPC finished with: rpc error: code = Unavailable desc = error reading from server: EOF
    verify_c2_c3_routing_lock_test.go:371: [t+ 0.017s] server-side RPC context was cancelled
    --- PASS: Test/Verify_C2_StopWithAllowRPCAwaitingContextCancel (0.03s)
ok  	google.golang.org/grpc/test/xds	1.084s
exit=0
```

On both controls `Stop()` returns in 1ms and the in-flight RPC ends with `Unavailable ... error reading from server: EOF` (the server tore the transport down and thereby cancelled the RPC context). So the hang is introduced by this branch's `routingConfiguration` lock, not by the test harness.

The eval fixture does not exercise this path: all six fixture tests pass on this branch (`verify/logs/fixture_423f7ef0.filtered.log`), as does the branch's own unit suite:

```sh
cp ~/eval/tests/eval_xds_server_interceptor_leak_test.go ~/wt/423f7ef0/test/xds/
cd ~/wt/423f7ef0 && go test -v -run '^Test$/^Eval_' ./test/xds -race -count=1
cd ~/wt/423f7ef0 && go test -race ./internal/xds/server/... -count=1
```

```console
--- PASS: Test (0.27s)
    --- PASS: Test/Eval_ServerSideXDS_InterceptorBeforeFilterCloseOrder (0.11s)
    --- PASS: Test/Eval_ServerSideXDS_InterceptorLeak_MultiGenerationRDSUpdate (0.04s)
    --- PASS: Test/Eval_ServerSideXDS_InterceptorLeak_RDSUpdate (0.03s)
    --- PASS: Test/Eval_ServerSideXDS_InterceptorSwapOrder (0.02s)
    --- PASS: Test/Eval_ServerSideXDS_PartialRouteFailure_ClosesInterceptors (0.03s)
    --- PASS: Test/Eval_ServerSideXDS_PartialVirtualHostFailure_ClosesInterceptors (0.03s)
ok  	google.golang.org/grpc/test/xds	1.305s
exit=0
ok  	google.golang.org/grpc/internal/xds/server	1.112s
exit=0
```

**Impact reasoning.** `xds.(*GRPCServer).Stop` is documented (`xds/server.go:210`) as "It immediately closes all open connections. It cancels all active RPCs on the server side"; on this branch it instead waits for every in-flight `AllowRPC` to return on its own before it gets to the step that would cancel them (blocked for the full 5s and 30s windows observed, released only by the client giving up; nothing on the server side bounds the wait). The base commit and the reference branch do not behave this way. The trigger is an interceptor whose `AllowRPC` blocks until its context is cancelled (or simply blocks for a long time) at the moment `Stop()` is called. The only server-side HTTP filters registered in-tree at this commit are `rbac` and `router` (`grep -rl 'BuildServerFilter()' --include=*.go . | grep -v _test.go` -> `internal/xds/httpfilter/rbac/rbac.go`, `internal/xds/httpfilter/router/router.go`, plus the interface and `listener_wrapper.go`), and the filter registry is an `internal` package, so today the hang is reachable only through a server filter whose `AllowRPC` blocks; whether either stock filter can block was not exercised in this audit. What was observed is a shutdown hazard for any blocking server filter, demonstrated with a test filter. No workaround exists on the server side short of bounding `AllowRPC`; the fix is to not hold `routingConfiguration.mu` across `AllowRPC` (e.g. snapshot the configuration under the lock, or go back to an atomic pointer) or to not take the write lock on the listener-close path before transports are closed. A regression test equivalent to `Verify_C2_*` would have caught it.

## C3

**Claim:** on [`evalon/grpc-go-xd-423f7ef0`](https://github.com/kaitranntt-evals/grpc-go-xds-rds-interceptor-lifecycle-leak/tree/evalon/grpc-go-xd-423f7ef0), RPCs cannot use a published replacement route until the retired interceptor's `Close` returns, so slow retirement makes replacement RPCs exceed their deadlines. **Observed result: holds (both parts).** Naming drift: the claim's "where to look" names `applyConfiguration` / `oldURC.stop()`, which exist only on the reference branch; the equivalents on this branch are `(*filterChain).updateRouteConfiguration` / `previous.close()`.

### Setup (self-contained)

```sh
cd ~/repos/grpc-go
git fetch origin grpc-go-xds-rds-interceptor-lifecycle-leak-perfect
git checkout -b verify/grpc-go-xds-rds-interceptor-lifecycle-leak-v-389b3566 origin/grpc-go-xds-rds-interceptor-lifecycle-leak-perfect
git remote add claims https://github.com/kaitranntt-evals/grpc-go-xds-rds-interceptor-lifecycle-leak   # in this session: the same repo via the VM's git proxy
for b in 8de853fe 1ea7dd6e 8f0663b7 bbe58e09 295929ad 423f7ef0; do
  git fetch claims evalon/grpc-go-xd-$b:refs/remotes/claims/evalon/grpc-go-xd-$b
  git worktree add --detach ~/wt/$b claims/evalon/grpc-go-xd-$b
done
git worktree add --detach ~/wt/perfect origin/grpc-go-xds-rds-interceptor-lifecycle-leak-perfect   # control
git worktree add --detach ~/wt/base 4ee6ac46fada69c06576cee108b009689a000520                      # control (task base commit)
mkdir -p ~/eval && unzip -o eval_tests.zip -d ~/eval    # fixture: ~/eval/tests/eval_xds_server_interceptor_leak_test.go
```

### Relevant code on the branch (for orientation only; the verdict rests on the runs below)

`internal/xds/server/filter_chain_manager.go` -- the replacement is published and the retired configuration is closed under the same write lock:

```go
411: func (fc *filterChain) updateRouteConfiguration(rc *usableRouteConfiguration) {
412: 	routing := fc.usableRouteConfiguration
413: 	routing.mu.Lock()
414: 	defer routing.mu.Unlock()
415: 	previous := routing.current
416: 	routing.current = rc
417: 	previous.close()
418: }
```

`internal/xds/server/routing.go` -- every RPC takes the read lock first:

```go
46: 	// Keep the configuration's interceptors alive until route processing ends.
47: 	cw.urc.mu.RLock()
48: 	defer cw.urc.mu.RUnlock()
49: 	rc := cw.urc.current
```

### Repro

`verify/repro/verify_c2_c3_routing_lock_test.go`, test `Test/Verify_C3_ReplacementRPCWhileRetiredCloseBlocked`: real `xds.NewGRPCServer` + management server + client, a listener with a single catch-all filter chain (so exactly one routing configuration is in play) fetching routes over RDS. The interceptor built from the original configuration (`path1`) blocks in `Close` on a channel; `AllowRPC` never blocks and records which interceptor served the RPC. Sequence: RPC on the original route (served by `path1`) -> push a replacement RouteConfiguration whose route carries a per-route override `path2` -> wait until the retired interceptor's `Close` has been entered (on this branch that is after `routing.current = rc`, i.e. after publication) -> issue an RPC with a bounded deadline (default 3s) -> halfway through, dump goroutines if still pending -> release `Close` -> issue another RPC.

```sh
cp verify/repro/verify_c2_c3_routing_lock_test.go ~/wt/423f7ef0/test/xds/
cd ~/wt/423f7ef0 && go test -v -run '^Test$/^Verify_C3_' ./test/xds -race -count=1
```

Output (gRPC `tlogger` noise removed; `verify/logs/c3_423f7ef0.filtered.log`):

```console
=== RUN   Test
=== RUN   Test/Verify_C3_ReplacementRPCWhileRetiredCloseBlocked
    xds_server_integration_test.go:61: Serving mode for listener "127.0.0.1:33273" changed to "SERVING", err: <nil>
    verify_c2_c3_routing_lock_test.go:423: [t+ 0.016s] RPC on original route OK, served by interceptor(s) [path1]
    verify_c2_c3_routing_lock_test.go:429: [t+ 0.017s] retired interceptor (path1) Close() entered and is now held on a channel
    verify_c2_c3_routing_lock_test.go:451: [t+ 1.524s] RPC still pending 1.5s after it was issued; goroutines on the xDS routing / retirement paths:
        goroutine 10 [chan receive]:
        google.golang.org/grpc/test/xds_test.s.TestVerify_C3_ReplacementRPCWhileRetiredCloseBlocked.func3({0xc000491ebb, 0x5})
        	/home/ubuntu/wt/423f7ef0/test/xds/verify_c2_c3_routing_lock_test.go:404 +0x78
        google.golang.org/grpc/test/xds_test.(*verifyInterceptor).Close(0xc00008e3a8)
        	/home/ubuntu/wt/423f7ef0/test/xds/verify_c2_c3_routing_lock_test.go:132 +0x82
        google.golang.org/grpc/internal/xds/server.(*interceptorList).Close(0xc00008e3d8)
        	/home/ubuntu/wt/423f7ef0/internal/xds/server/filter_chain_manager.go:545 +0x79
        google.golang.org/grpc/internal/xds/server.(*filterChain).updateRouteConfiguration.(*usableRouteConfiguration).close.func1()
        	/home/ubuntu/wt/423f7ef0/internal/xds/server/filter_chain_manager.go:397 +0x182
        sync.(*Once).doSlow(0xc0000529d8, 0xc000805bd0)
        	/usr/local/go/src/sync/once.go:78 +0xd2
        sync.(*Once).Do(0xc0000529d8, 0xc000805bd0)
        	/usr/local/go/src/sync/once.go:69 +0x45
        google.golang.org/grpc/internal/xds/server.(*usableRouteConfiguration).close(...)
        	/home/ubuntu/wt/423f7ef0/internal/xds/server/filter_chain_manager.go:393
        google.golang.org/grpc/internal/xds/server.(*filterChain).updateRouteConfiguration(0xc0006b1c80, 0xc0001abb60)
        	/home/ubuntu/wt/423f7ef0/internal/xds/server/filter_chain_manager.go:417 +0x14a
        google.golang.org/grpc/internal/xds/server.(*listenerWrapper).handleRDSUpdate(0xc0000d9ec0, {0xc000491e97, 0x9}, {0xc0005c7410?, {0x0?, 0x0?}})
        	/home/ubuntu/wt/423f7ef0/internal/xds/server/listener_wrapper.go:220 +0x4f6
        google.golang.org/grpc/internal/xds/server.(*rdsWatcher).ResourceChanged(0xc0006c2000, 0xc0005c7410, 0xc000542990)
        	/home/ubuntu/wt/423f7ef0/internal/xds/server/rds_handler.go:153 +0x2fe
        google.golang.org/grpc/internal/xds/xdsclient/xdsresource.(*delegatingRouteConfigWatcher).ResourceChanged(0xc00068de90, {0x1f66a68, 0xc0005c7410}, 0xc000542990)
        	/home/ubuntu/wt/423f7ef0/internal/xds/xdsclient/xdsresource/route_config_resource_type.go:106 +0x77
        google.golang.org/grpc/internal/xds/clients/xdsclient.(*authority).handleADSResourceUpdate.func5({0xc000326a80?, 0xc0002c0b90?})
        	/home/ubuntu/wt/423f7ef0/internal/xds/clients/xdsclient/authority.go:420 +0x67
        google.golang.org/grpc/internal/xds/clients/internal/syncutil.(*CallbackSerializer).run(0xc0004ebe60, {0x1f699a8, 0xc0002c0b90})
        	/home/ubuntu/wt/423f7ef0/internal/xds/clients/internal/syncutil/callback_serializer.go:90 +0x1cf
        created by google.golang.org/grpc/internal/xds/clients/internal/syncutil.NewCallbackSerializer in goroutine 7
        	/home/ubuntu/wt/423f7ef0/internal/xds/clients/internal/syncutil/callback_serializer.go:52 +0x205
        goroutine 131 [sync.RWMutex.RLock]:
        sync.runtime_SemacquireRWMutexR(0xc0001b7df0?, 0x1?, 0xc000a88e10?)
        	/usr/local/go/src/runtime/sema.go:100 +0x25
        sync.(*RWMutex).RLock(0xc0001b7de0)
        	/usr/local/go/src/sync/rwmutex.go:74 +0x5b
        google.golang.org/grpc/internal/xds/server.RouteAndProcess({0x1f69970, 0xc0006c3080})
        	/home/ubuntu/wt/423f7ef0/internal/xds/server/routing.go:47 +0xc6
        google.golang.org/grpc/xds.xdsUnaryInterceptor({0x1f69970, 0xc0006c3080}, {0x1b9d1c0, 0xc0006c30b0}, 0xc0006c3080?, 0xc000622510)
        	/home/ubuntu/wt/423f7ef0/xds/server.go:236 +0x47
        google.golang.org/grpc/interop/grpc_testing._TestService_EmptyCall_Handler({0x1cca1e0, 0xc0000d9d40}, {0x1f69970, 0xc0006c3080}, 0xc00088c600, 0x1dc7808)
        	/home/ubuntu/wt/423f7ef0/interop/grpc_testing/test_grpc.pb.go:293 +0x1e7
        google.golang.org/grpc.(*Server).processUnaryRPC(0xc0000f18c8, {0x1f69970, 0xc0006c2ff0}, 0xc000714680, 0xc000326e70, 0x2b09ce0, 0x0)
        	/home/ubuntu/wt/423f7ef0/server.go:1441 +0x1a6a
        google.golang.org/grpc.(*Server).handleStream(0xc0000f18c8, {0x1f6e958, 0xc0001764e0}, 0xc000714680)
        	/home/ubuntu/wt/423f7ef0/server.go:1850 +0x10b3
        google.golang.org/grpc.(*Server).serveStreams.func2.1()
        	/home/ubuntu/wt/423f7ef0/server.go:1076 +0x14a
        created by google.golang.org/grpc.(*Server).serveStreams.func2 in goroutine 103
        	/home/ubuntu/wt/423f7ef0/server.go:1087 +0x213
    verify_c2_c3_routing_lock_test.go:455: [t+ 3.017s] OBSERVATION: RPC issued while retired Close is blocked: err = rpc error: code = DeadlineExceeded desc = context deadline exceeded, elapsed = 3s (deadline 3s), served by interceptor(s) []
    verify_c2_c3_routing_lock_test.go:459: [t+ 3.018s] released the retired interceptor's Close()
    verify_c2_c3_routing_lock_test.go:464: [t+ 3.018s] RPC after releasing Close: err = <nil>, elapsed = 1ms, served by interceptor(s) [path2 path2]
    verify_c2_c3_routing_lock_test.go:467: RPC on the published replacement route failed while the retired interceptor's Close was blocked: code = DeadlineExceeded, err = rpc error: code = DeadlineExceeded desc = context deadline exceeded
--- FAIL: Test (3.03s)
    --- FAIL: Test/Verify_C3_ReplacementRPCWhileRetiredCloseBlocked (3.03s)
FAIL
FAIL	google.golang.org/grpc/test/xds	3.066s
FAIL
exit=1
```

What the trace shows, per part:

- *retirement_lock*: the xDS callback goroutine is in `(*listenerWrapper).handleRDSUpdate` -> `(*filterChain).updateRouteConfiguration` (`filter_chain_manager.go:417`, the `previous.close()` line, i.e. after `routing.current = rc` on line 416 and with `routing.mu` write-locked since line 413) -> `(*usableRouteConfiguration).close` -> `(*interceptorList).Close` -> the test interceptor's blocked `Close`.
- *replacement_access*: the RPC goroutine is parked in `sync.(*RWMutex).RLock` called from `RouteAndProcess` (`routing.go:47`). The RPC fails with `DeadlineExceeded` after exactly its 3s deadline and was served by no interceptor (`served by interceptor(s) []`). After `Close` is released the next RPC succeeds in 1ms. The `[path2 path2]` on that line is the released handler of the timed-out RPC plus the new RPC, both now running through the replacement interceptor -- the replacement had been published all along but was unreachable.

With a 30s deadline the RPC waits the full 30s (same branch; `verify/logs/c2c3_423f7ef0_30s.filtered.log`), and the result is identical in 3/3 repetitions (`verify/logs/c2c3_423f7ef0_x3.filtered.log`):

```sh
cd ~/wt/423f7ef0 && VERIFY_WINDOW=30s go test -v -run '^Test$/^Verify_(C2|C3)_' ./test/xds -race -count=1
cd ~/wt/423f7ef0 && go test -v -run '^Test$/^Verify_(C2|C3)_' ./test/xds -race -count=3
```

```console
    verify_c2_c3_routing_lock_test.go:423: [t+ 0.009s] RPC on original route OK, served by interceptor(s) [path1]
    verify_c2_c3_routing_lock_test.go:429: [t+ 0.010s] retired interceptor (path1) Close() entered and is now held on a channel
    verify_c2_c3_routing_lock_test.go:451: [t+15.035s] RPC still pending 15s after it was issued; goroutines on the xDS routing / retirement paths:
        google.golang.org/grpc/test/xds_test.s.TestVerify_C3_ReplacementRPCWhileRetiredCloseBlocked.func3({0xc00038adab, 0x5})
    verify_c2_c3_routing_lock_test.go:455: [t+30.035s] OBSERVATION: RPC issued while retired Close is blocked: err = rpc error: code = DeadlineExceeded desc = context deadline exceeded, elapsed = 30.024s (deadline 30s), served by interceptor(s) []
    verify_c2_c3_routing_lock_test.go:459: [t+30.035s] released the retired interceptor's Close()
    verify_c2_c3_routing_lock_test.go:464: [t+30.035s] RPC after releasing Close: err = <nil>, elapsed = 1ms, served by interceptor(s) [path2 path2]
    verify_c2_c3_routing_lock_test.go:467: RPC on the published replacement route failed while the retired interceptor's Close was blocked: code = DeadlineExceeded, err = rpc error: code = DeadlineExceeded desc = context deadline exceeded
    --- FAIL: Test/Verify_C3_ReplacementRPCWhileRetiredCloseBlocked (30.04s)
# -count=3:
    verify_c2_c3_routing_lock_test.go:455: [t+ 3.012s] OBSERVATION: RPC issued while retired Close is blocked: err = rpc error: code = DeadlineExceeded desc = context deadline exceeded, elapsed = 3.001s (deadline 3s), served by interceptor(s) []
    --- FAIL: Test/Verify_C3_ReplacementRPCWhileRetiredCloseBlocked (3.02s)
    verify_c2_c3_routing_lock_test.go:455: [t+ 3.010s] OBSERVATION: RPC issued while retired Close is blocked: err = rpc error: code = DeadlineExceeded desc = context deadline exceeded, elapsed = 3s (deadline 3s), served by interceptor(s) []
    --- FAIL: Test/Verify_C3_ReplacementRPCWhileRetiredCloseBlocked (3.02s)
    verify_c2_c3_routing_lock_test.go:455: [t+ 3.011s] OBSERVATION: RPC issued while retired Close is blocked: err = rpc error: code = DeadlineExceeded desc = context deadline exceeded, elapsed = 3.001s (deadline 3s), served by interceptor(s) []
    --- FAIL: Test/Verify_C3_ReplacementRPCWhileRetiredCloseBlocked (3.02s)
```

Control -- the identical test on the audited reference branch [`grpc-go-xds-rds-interceptor-lifecycle-leak-perfect`](https://github.com/kaitranntt-evals/grpc-go/tree/grpc-go-xds-rds-interceptor-lifecycle-leak-perfect), which swaps an atomic pointer and closes the retired interceptors without holding a lock that RPCs need (`verify/logs/c2c3_perfect.filtered.log`):

```sh
cp verify/repro/verify_c2_c3_routing_lock_test.go ~/wt/perfect/test/xds/ && (cd ~/wt/perfect && go test -v -run '^Test$/^Verify_(C2|C3)_' ./test/xds -race -count=1)
```

```console
    verify_c2_c3_routing_lock_test.go:423: [t+ 0.008s] RPC on original route OK, served by interceptor(s) [path1]
    verify_c2_c3_routing_lock_test.go:429: [t+ 0.010s] retired interceptor (path1) Close() entered and is now held on a channel
    verify_c2_c3_routing_lock_test.go:455: [t+ 0.011s] OBSERVATION: RPC issued while retired Close is blocked: err = <nil>, elapsed = 1ms (deadline 3s), served by interceptor(s) [path2]
    verify_c2_c3_routing_lock_test.go:459: [t+ 0.011s] released the retired interceptor's Close()
    verify_c2_c3_routing_lock_test.go:464: [t+ 0.011s] RPC after releasing Close: err = <nil>, elapsed = 0s, served by interceptor(s) [path2]
    --- PASS: Test/Verify_C3_ReplacementRPCWhileRetiredCloseBlocked (0.02s)
ok  	google.golang.org/grpc/test/xds	1.084s
exit=0
```

There the RPC issued while the retired `Close` is still blocked succeeds in 1ms and is served by `[path2]`. (The base commit is not a usable control for C3: it never closes retired interceptors, which is the leak the task is about, so the test's "Close entered" precondition never occurs.)

The eval fixture does not exercise this path: all six fixture tests pass on this branch (`verify/logs/fixture_423f7ef0.filtered.log`):

```sh
cp ~/eval/tests/eval_xds_server_interceptor_leak_test.go ~/wt/423f7ef0/test/xds/
cd ~/wt/423f7ef0 && go test -v -run '^Test$/^Eval_' ./test/xds -race -count=1
```

```console
--- PASS: Test (0.27s)
    --- PASS: Test/Eval_ServerSideXDS_InterceptorBeforeFilterCloseOrder (0.11s)
    --- PASS: Test/Eval_ServerSideXDS_InterceptorLeak_MultiGenerationRDSUpdate (0.04s)
    --- PASS: Test/Eval_ServerSideXDS_InterceptorLeak_RDSUpdate (0.03s)
    --- PASS: Test/Eval_ServerSideXDS_InterceptorSwapOrder (0.02s)
    --- PASS: Test/Eval_ServerSideXDS_PartialRouteFailure_ClosesInterceptors (0.03s)
    --- PASS: Test/Eval_ServerSideXDS_PartialVirtualHostFailure_ClosesInterceptors (0.03s)
ok  	google.golang.org/grpc/test/xds	1.305s
exit=0
```

**Impact reasoning.** On this branch a RouteConfiguration update is not "publish, then clean up in the background of traffic": every RPC on the affected filter chain is stalled for as long as the retired interceptors take to close, and fails with `DeadlineExceeded` if that exceeds its deadline (3s and 30s observed), even though the replacement is already installed. This contradicts the task's requirement that superseded resources be released "without dropping requests on active listeners". The stall lasts exactly as long as the retired interceptor's `Close` takes (it ended 1ms after `Close` was released). It was demonstrated with a test filter whose `Close` is held; the `Close` duration of the in-tree server filters at this commit (`rbac`, `router`) was not measured in this audit. There is no caller-side workaround. The fix is to release `routing.mu` before calling `previous.close()` (swap under the lock, close after unlocking) or to use an atomic swap as the reference branch does. A regression test equivalent to `Verify_C3_*` would have caught it.
