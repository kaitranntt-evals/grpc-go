## Setup

Audited branch: `grpc-go-xds-rds-interceptor-lifecycle-leak-perfect` (work branch `verify/grpc-go-xds-rds-interceptor-lifecycle-leak-v-d14d0307`). Every claim targets a branch in a second repository, fetched as remote `claims` and checked out in separate worktrees. Go 1.25.7, linux/amd64, 8 CPUs.

```sh
cd ~/repos/grpc-go
git fetch origin grpc-go-xds-rds-interceptor-lifecycle-leak-perfect
git checkout -b verify/grpc-go-xds-rds-interceptor-lifecycle-leak-v-d14d0307 origin/grpc-go-xds-rds-interceptor-lifecycle-leak-perfect
git remote add claims https://github.com/kaitranntt-evals/grpc-go-xds-rds-interceptor-lifecycle-leak
for s in c08e0d0f bf39e226 8251bfa4; do
  git fetch claims evalon/grpc-go-xd-$s:refs/remotes/claims/evalon/grpc-go-xd-$s
  git worktree add ~/wt-$s claims/evalon/grpc-go-xd-$s
done
```

Heads: `evalon/grpc-go-xd-c08e0d0f` = 5c26291ceabe5b010046e146269e2eb2219016ca, `evalon/grpc-go-xd-bf39e226` = f8cd4aad439003eb776bbc49d01ca64c2d836e4d, `evalon/grpc-go-xd-8251bfa4` = 36737eba6a7017efac22afd095159a25453442d8.

grpctest note: a method `TestFoo` on `s` runs as `Test/Foo`, so `-run` patterns below omit the `Test` prefix of the method name.

## C1

Verdict: CONFIRMED. Branch `evalon/grpc-go-xd-c08e0d0f`, test `TestServerSideXDS_FilterStateRetention_AcrossUpdates_RouteConfigChange` in `test/xds/xds_server_filter_state_retention_test.go`.

### What the test does (lines as on the branch)

```go
	const wantLiveInterceptors = 2
	waitForLiveInterceptors := func() {
		t.Helper()
		for ; ctx.Err() == nil; <-time.After(defaultTestShortTimeout) {
			if interceptorsCreated.Load()-interceptorsDestroyed.Load() == wantLiveInterceptors {   // line 817
				return
			}
		}
		...
	}
	...
	const numUpdates = 10
	for i := 1; i <= numUpdates; i++ {
		...
		waitForPath(path)
		waitForLiveInterceptors()
	}

	if got, want := interceptorsCreated.Load(), int32(wantLiveInterceptors*(numUpdates+1)); got != want {   // line 841, want = 22
		t.Fatalf("Created %d interceptor instances, want: %d", got, want)
	}
```

The listener has two filter chains (`v4-wildcard`, `v6-wildcard`) sharing one RDS route. The branch's `handleRDSUpdate` updates them one after the other under `l.mu`:

```go
		for _, fc := range l.activeFilterChainManager.filterChains {
			...
			urc = fc.constructUsableRouteConfiguration(*rcu.data, l.getOrCreateServerFilterLocked)   // created +1
			...
			fc.usableRouteConfiguration.Swap(urc).closeInterceptors()                                 // destroyed +1
		}
```

During the tenth update the counters therefore pass through 20/18 → 21/18 → **21/19** → 22/19 → 22/20. In the 21/19 state the chain serving the test's connection already reports the new path (so `waitForPath` returns) and 21−19 equals the wanted live count of 2 (so `waitForLiveInterceptors` returns). RPCs read `fc.usableRouteConfiguration` through an atomic pointer and never take `l.mu`, and the test takes no lock or signal tied to the second chain. Nothing holds the `want: 22` assertion until the second chain is updated.

### Controlled execution

Instrumentation (audit only): `verify/repro/c1_pause_between_chains.patch`. It adds an env-controlled sleep in `handleRDSUpdate` before the second and later filter chains of one chosen call, a call trace, and one `t.Logf` in the poll printing the counters at release. Replay with `bash verify/repro/c1_run.sh`.

Baseline with the patch applied, no pause:

```sh
cd ~/wt-c08e0d0f && git apply ~/repos/grpc-go/verify/repro/c1_pause_between_chains.patch
T='^Test$/^ServerSideXDS_FilterStateRetention_AcrossUpdates_RouteConfigChange$'
VERIFY_RDS_TRACE=1 go test -race -count=1 -v -run "$T" ./test/xds 2>&1 | grep -E "VERIFY|^(---|===|ok|FAIL|PASS)|Created|want"
```

```console
VERIFY: handleRDSUpdate call #1 route="server-route" activeFCM=false
    xds_server_filter_state_retention_test.go:826: VERIFY: live-count poll released with created=2 destroyed=0
VERIFY: handleRDSUpdate call #2 route="server-route" activeFCM=true
    xds_server_filter_state_retention_test.go:839: VERIFY: live-count poll released with created=4 destroyed=2
...
VERIFY: handleRDSUpdate call #10 route="server-route" activeFCM=true
    xds_server_filter_state_retention_test.go:839: VERIFY: live-count poll released with created=20 destroyed=18
VERIFY: handleRDSUpdate call #11 route="server-route" activeFCM=true
    xds_server_filter_state_retention_test.go:839: VERIFY: live-count poll released with created=22 destroyed=20
--- PASS: Test (0.17s)
ok  	google.golang.org/grpc/test/xds	1.208s
```

Call #11 is the tenth update. Pausing 500ms before its second filter chain:

```sh
VERIFY_RDS_PAUSE=500ms VERIFY_RDS_PAUSE_CALL=11 go test -race -count=1 -v -run "$T" ./test/xds 2>&1 | grep -E "VERIFY|^(---|===|ok|FAIL|PASS)|Created|want|Timeout|EmptyCall" | tail -12
```

```console
    xds_server_filter_state_retention_test.go:839: VERIFY: live-count poll released with created=20 destroyed=18
    server.go:229: Created new resource snapshot...
VERIFY: handleRDSUpdate call #11: pausing 500ms before updating filter chain index 1
    xds_server_filter_state_retention_test.go:839: VERIFY: live-count poll released with created=21 destroyed=19
    xds_server_filter_state_retention_test.go:843: Created 21 interceptor instances, want: 22
VERIFY: handleRDSUpdate call #11: resuming, updating filter chain index 1
--- FAIL: Test (0.66s)
FAIL
FAIL	google.golang.org/grpc/test/xds	0.708s
```

The poll released at exactly created=21 destroyed=19 and the assertion ran before the second chain was updated ("resuming" is printed after the failure). Five further repeats gave the same line each time:

```console
    xds_server_filter_state_retention_test.go:843: Created 21 interceptor instances, want: 22 FAIL	google.golang.org/grpc/test/xds	0.753s
    xds_server_filter_state_retention_test.go:843: Created 21 interceptor instances, want: 22 FAIL	google.golang.org/grpc/test/xds	0.755s
    xds_server_filter_state_retention_test.go:843: Created 21 interceptor instances, want: 22 FAIL	google.golang.org/grpc/test/xds	0.722s
    xds_server_filter_state_retention_test.go:843: Created 21 interceptor instances, want: 22 FAIL	google.golang.org/grpc/test/xds	0.695s
    xds_server_filter_state_retention_test.go:843: Created 21 interceptor instances, want: 22 FAIL	google.golang.org/grpc/test/xds	0.693s
```

### How often it bites without the pause

Unmodified branch, no instrumentation:

```sh
cd ~/wt-c08e0d0f && git checkout -q . && go test -race -count=150 -timeout=250s -run "$T" ./test/xds
```

```console
ok  	google.golang.org/grpc/test/xds	22.451s
```

150 of 150 runs passed on an idle 8-CPU machine.

### Impact reasoning

The production code on this branch is not at fault here; the defect is in the branch's own regression test. Its final count assertion is not ordered after the second filter chain's update, so the test can fail with "Created 21 interceptor instances, want: 22" although the server behaves correctly. Without the pause the window is the time between the first chain's close and the second chain's construct, and a full RPC round trip plus two polls must fit inside it. I did not observe a natural failure in 150 runs, so this is a latent flake that needs the serializer goroutine to be descheduled at that point (loaded CI machine), not a routine failure. The same shape applies to every loop iteration (2k+1 created, 2k−1 destroyed), but earlier iterations only delay rather than fail because no count assertion follows them.

## C2

Verdict: REFUTED (worker-termination part refuted; join-invocation part holds). Branch `evalon/grpc-go-xd-bf39e226`.

### Where the join is

```sh
cd ~/wt-bf39e226 && grep -n 'wg\.Wait\|ctx\.Done\|cancel(\|wg\.Add\|wg\.Done\|go func' test/xds/xds_server_rds_update_test.go internal/xds/server/routing_test.go
```

```console
test/xds/xds_server_rds_update_test.go:161:	defer cancel()
test/xds/xds_server_rds_update_test.go:175:	case <-ctx.Done():
test/xds/xds_server_rds_update_test.go:193:			case <-ctx.Done():
internal/xds/server/routing_test.go:208:		wg.Add(1)
internal/xds/server/routing_test.go:209:		go func() {
internal/xds/server/routing_test.go:210:			defer wg.Done()
internal/xds/server/routing_test.go:233:	wg.Wait()
```

The only worker join in the tests this branch adds is in `TestAcquireRouteConfiguration_ConcurrentReplacements` (`internal/xds/server/routing_test.go`):

```go
	done := make(chan struct{})
	var wg sync.WaitGroup
	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-done:
					return
				default:
				}
				rc, release := acquireRouteConfiguration(p)
				... // reads atomics, t.Errorf on mismatch
				release()
			}
		}()
	}
	for i := 1; i <= numUpdates; i++ {
		p.Swap(newUsableRouteConfigurationForTesting(cis[i])).release()
	}
	close(done)
	wg.Wait()
```

Each worker iteration checks `done` and otherwise performs only non-blocking atomic operations (`acquire` is a CAS loop; `acquireRouteConfiguration` returns as soon as the pointer stops changing). `close(done)` runs unconditionally right before `wg.Wait()`; there is no `t.Fatal` or early return between worker start and the close.

### Part: join invocation — holds

The mutation run below times out with the test goroutine parked in `sync.(*WaitGroup).Wait` at `routing_test.go:233`, so execution does reach the join.

### Part: worker termination — refuted

Unmodified test, race detector on, 300 iterations at default and at one P:

```sh
T='^Test$/^AcquireRouteConfiguration_ConcurrentReplacements$'
go test -race -count=300 -timeout=120s -run "$T" ./internal/xds/server
GOMAXPROCS=1 go test -race -count=300 -timeout=120s -run "$T" ./internal/xds/server
```

```console
ok  	google.golang.org/grpc/internal/xds/server	1.844s
ok  	google.golang.org/grpc/internal/xds/server	1.985s
```

Join latency, measured with `verify/instrumentation/c2_latency.patch` (adds a timer around `wg.Wait()`), 200 runs:

```sh
git apply ~/repos/grpc-go/verify/instrumentation/c2_latency.patch
go test -race -count=200 -v -timeout=120s -run "$T" ./internal/xds/server 2>&1 | grep -o "VERIFY: wg.Wait() returned [^ ]*"   # then min/median/max
git checkout -q .
```

```console
runs 200 min_us 4.427 median_us 8.233 max_us 852.317
```

Mutation proving `close(done)` is the effective cancellation path (`verify/instrumentation/c2_mutation.patch` deletes that one line):

```sh
git apply ~/repos/grpc-go/verify/instrumentation/c2_mutation.patch
go test -race -count=1 -timeout=15s -run "$T" ./internal/xds/server 2>&1 | grep -E "panic: test timed out|running tests|ConcurrentReplacements \(|sync.\(\*WaitGroup\).Wait|routing_test.go:2[0-9]+"
git checkout -q .
```

```console
panic: test timed out after 15s
	running tests:
		Test/AcquireRouteConfiguration_ConcurrentReplacements (15s)
sync.(*WaitGroup).Wait(0xc00034e5e0)
	/home/ubuntu/wt-bf39e226/internal/xds/server/routing_test.go:233 +0x76a
	/home/ubuntu/wt-bf39e226/internal/xds/server/routing_test.go:217 +0xf6
	/home/ubuntu/wt-bf39e226/internal/xds/server/routing_test.go:209 +0x385
```

With the close in place the join returned within 0.9ms in all 200 measured runs and 600 further runs completed; removing it is what makes the join hang. The join has an effective cancellation path, so it does not permit indefinite waiting. It has no timeout of its own, which is immaterial given the above.

## C3

Verdict: CONFIRMED (both parts). Branch `evalon/grpc-go-xd-8251bfa4`.

### Part: map-access synchronization — holds

The branch adds two lines to `rdsHandler.close` (`git diff 4ee6ac46 HEAD -- internal/xds/server/rds_handler.go`):

```diff
@@ -117,6 +117,8 @@ func (rh *rdsHandler) close() {
 	for _, cancel := range rh.cancels {
 		cancel()
 	}
+	clear(rh.cancels)
+	clear(rh.updates)
 }
```

`close` holds `rh.mu`. The other accessors do not take it:

```go
func (rh *rdsHandler) determineRouteConfigurationReady() bool {
	return len(rh.updates) == len(rh.cancels)            // line 108, no lock
}
func (rw *rdsWatcher) ResourceChanged(update *xdsresource.RouteConfigUpdate, onDone func()) {
	defer onDone()
	rw.mu.Lock()
	if rw.canceled { rw.mu.Unlock(); return }
	rw.mu.Unlock()                                        // watcher's own mutex, released before the write
	...
	rw.parent.updates[routeName] = rwu                    // line 154, no rh.mu
	rw.parent.callback(routeName, rwu)                    // line 155 -> handleRDSUpdate -> line 108
}
```

The cancel funcs wait on the authority's `xdsClientSerializer` (`authority.unwatchResource`), while watcher callbacks run on the separate `watcherCallbackSerializer`, so cancelling does not wait for a callback that already passed its `canceled` check. `listenerWrapper.Close` calls `l.rdsHandler.close()` from the goroutine calling `Server.Stop()`, before taking `l.mu`.

### Part: shutdown concurrency — holds

Repro `verify/repro/c3_rds_close_race_test.go`: no hooks and no production changes. An xDS-enabled server with an RDS-backed listener reaches SERVING, the management server keeps pushing new versions of the served RouteConfiguration every millisecond, and the server is stopped with plain `Stop()`; 30 iterations per run.

```sh
cd ~/wt-8251bfa4 && cp ~/repos/grpc-go/verify/repro/c3_rds_close_race_test.go test/xds/
go test -race -count=1 -run '^Test$/^Verify_RDSHandlerCloseRacesWithRDSUpdates$' ./test/xds
```

```console
WARNING: DATA RACE
Write at 0x00c00017e7e0 by goroutine 18:
  runtime.mapclear()
      /usr/local/go/src/runtime/map_swiss.go:182 +0x0
  google.golang.org/grpc/internal/xds/server.(*rdsHandler).close()
      /home/ubuntu/wt-8251bfa4/internal/xds/server/rds_handler.go:120 +0x19c
  google.golang.org/grpc/internal/xds/server.(*listenerWrapper).Close()
      /home/ubuntu/wt-8251bfa4/internal/xds/server/listener_wrapper.go:362 +0xe9
  google.golang.org/grpc.(*listenSocket).Close()
      /home/ubuntu/wt-8251bfa4/server.go:862 +0x4a
  google.golang.org/grpc.(*Server).closeListenersLocked()
  google.golang.org/grpc.(*Server).stop()
      /home/ubuntu/wt-8251bfa4/server.go:1952 +0x1bb
  google.golang.org/grpc.(*Server).Stop()
      /home/ubuntu/wt-8251bfa4/server.go:1936 +0x28
  google.golang.org/grpc/xds.(*GRPCServer).Stop()
  ...

Previous read at 0x00c00017e7e0 by goroutine 92:
  google.golang.org/grpc/internal/xds/server.(*rdsHandler).determineRouteConfigurationReady()
      /home/ubuntu/wt-8251bfa4/internal/xds/server/rds_handler.go:108 +0x1a4
  google.golang.org/grpc/internal/xds/server.(*listenerWrapper).handleRDSUpdate()
      /home/ubuntu/wt-8251bfa4/internal/xds/server/listener_wrapper.go:229 +0x118
  google.golang.org/grpc/internal/xds/server.(*rdsWatcher).ResourceChanged()
      /home/ubuntu/wt-8251bfa4/internal/xds/server/rds_handler.go:155 +0x2fd
  google.golang.org/grpc/internal/xds/xdsclient/xdsresource.(*delegatingRouteConfigWatcher).ResourceChanged()
      /home/ubuntu/wt-8251bfa4/internal/xds/xdsclient/xdsresource/route_config_resource_type.go:106 +0x76
  google.golang.org/grpc/internal/xds/clients/xdsclient.(*authority).handleADSResourceUpdate.func5()
      /home/ubuntu/wt-8251bfa4/internal/xds/clients/xdsclient/authority.go:420 +0x66
  google.golang.org/grpc/internal/xds/clients/internal/syncutil.(*CallbackSerializer).run()
      /home/ubuntu/wt-8251bfa4/internal/xds/clients/internal/syncutil/callback_serializer.go:90 +0x1ce
```

Second report in the same run (frames filtered to the two files):

```console
Write at 0x00c00017e7b0 by goroutine 18:
      /home/ubuntu/wt-8251bfa4/internal/xds/server/rds_handler.go:121 +0x1c5
      /home/ubuntu/wt-8251bfa4/internal/xds/server/listener_wrapper.go:362 +0xe9
Previous write at 0x00c00017e7b0 by goroutine 92:
      /home/ubuntu/wt-8251bfa4/internal/xds/server/rds_handler.go:154 +0x246
```

```console
--- FAIL: Test (1.20s)
        testing.go:1617: race detected during execution of test
FAIL	google.golang.org/grpc/test/xds	1.254s
```

So `clear(rh.cancels)` (line 120) races with the readiness check's read (line 108), and `clear(rh.updates)` (line 121) races with `ResourceChanged`'s map write (line 154), both from an ordinary `Stop()`.

Frequency and control. Ten runs of the same command on the branch as-is, then ten with only the two `clear(...)` lines removed (`sed -i '/^\tclear(rh\.\(cancels\|updates\))$/d' internal/xds/server/rds_handler.go`):

```console
branch as-is: runs with DATA RACE = 5/10
control (clear lines removed): runs with DATA RACE = 0/10
```

An earlier set of six as-is runs reported the race in three, each time with exactly the line pairs 108/120 and 154/121; three earlier control runs were clean.

The attached fixtures were also run on this branch (`go test -race -run '^Test$/^Eval_' ./internal/xds/server` and `-run '^Test$/^(Eval_|ServerSideXDS_FilterStateRetention)' ./test/xds -count=3`). They failed for other reasons (for example `eval_rds_error_after_success_test.go:222: after route1 error: filter ref count = 2, want 1`) and printed no `DATA RACE`, so they do not measure this behaviour and the verdict does not rest on them.

### Impact reasoning

Trigger: stopping an xDS-enabled server (or closing its listener) while the control plane delivers a RouteConfiguration update for a route that listener serves. Both are ordinary events and nothing in user code can order them; in the repro a 1ms update cadence hit it in 8 of 16 runs of 30 stops each. The conflicting operations are an unsynchronized Go map clear against a map write and a map length read. That is undefined behaviour under the Go memory model, and the runtime may abort the whole process with `fatal error: concurrent map writes` when a clear and a write truly overlap; I observed the race reports, not that fatal error. A process that is stopping one server but keeps running (several servers, graceful restart, tests) is the exposed case. The control run shows the race is introduced by the two added `clear` lines: before them `close` only read `rh.cancels`. Workaround for users: none short of quiescing the control plane before `Stop()`.
