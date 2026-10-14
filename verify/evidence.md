## Environment

- go1.25.7 linux/amd64. Claim branches fetched from `https://github.com/kaitranntt-evals/grpc-go-xds-rds-interceptor-lifecycle-leak` (remote `claims`), one worktree each; base commit `4ee6ac46fada69c06576cee108b009689a000520` checked out as a control worktree.
- Branch heads: `evalon/grpc-go-xd-e015c56e` = `d0b18075`, `evalon/grpc-go-xd-c3af8974` = `46490539`, `evalon/grpc-go-xd-f9c33357` = `b86d46f9`.

```sh
cd ~/repos/grpc-go
git remote add claims https://github.com/kaitranntt-evals/grpc-go-xds-rds-interceptor-lifecycle-leak
git fetch claims evalon/grpc-go-xd-e015c56e evalon/grpc-go-xd-c3af8974 evalon/grpc-go-xd-f9c33357
for b in e015c56e c3af8974 f9c33357; do git worktree add ~/wt/$b claims/evalon/grpc-go-xd-$b; done
git worktree add ~/wt/base 4ee6ac46
```

## C1

Branch `evalon/grpc-go-xd-e015c56e`. Verdict: REFUTED.

The solution-authored lifecycle tests with destruction-count assertions after a stop are:

- `test/xds/xds_server_rds_update_test.go`: `TestServerSideXDS_RDSUpdates_ReleaseSupersededInterceptors` — `stopServer()` at line 235 followed immediately by `filtersDestroyed == 1` and `interceptorsDestroyed == interceptorsCreated`.
- `internal/xds/server/usable_route_config_test.go`: `TestUsableRouteConfiguration_ReleasedOnReplaceAndStop` and `..._ConcurrentReplaceAndRoute` — call `fcm.stop()` directly on the test goroutine (after `done()` / `wg.Wait()`), no server involved.

What the code does (then observed below):

- An RPC's routing reference is taken and dropped inside `RouteAndProcess` (`rc, done := acquireUsableRouteConfiguration(cw.urc); defer done()`), which `xdsUnaryInterceptor` calls *before* the handler. The reference is therefore released before the server writes the response, so a client that has seen its unary RPC complete cannot have a server-side reference outstanding. All RPCs in the test are sequential blocking `EmptyCall`s that must succeed (`t.Fatalf` otherwise).
- `stopServer()` → `xds.GRPCServer.Stop` → `grpc.Server.stop` → `closeListenersLocked` → `listenerWrapper.Close` → `filterChainManager.stop` → `usableRouteConfiguration.release` → `unref` → `closeInterceptors`, then `fc.stop()` closes the filters — all synchronous on the caller's goroutine.

Observation 1 — unmodified tests, repeated under race:

```sh
cd ~/wt/e015c56e
go test -race -count=30 -run '^Test$/^ServerSideXDS_RDSUpdates_ReleaseSupersededInterceptors$' ./test/xds
go test -race -count=50 -run '^Test$/^UsableRouteConfiguration' ./internal/xds/server
```

```console
ok  	google.golang.org/grpc/test/xds	5.159s
ok  	google.golang.org/grpc/internal/xds/server	1.444s
```

Observation 2 — instrumented run. `verify/instrumentation/c1_trace.patch` (apply on the claim branch with `git apply`) (a) sleeps 200ms inside `RouteAndProcess` before the deferred reference release, widening any window in which an RPC could be "done" for the client while still holding a reference, (b) prints the reference count in `filterChainManager.stop` before/after release, and (c) prints the stack of every `closeInterceptors` call.

```sh
cd ~/wt/e015c56e && git apply ~/repos/grpc-go/verify/instrumentation/c1_trace.patch
go test -race -count=1 -v -run '^Test$/^ServerSideXDS_RDSUpdates_ReleaseSupersededInterceptors$' ./test/xds > ~/c1_trace.log 2>&1; echo exit=$?
grep -n "VERIFY-C1 fcm\|^--- \|^ok" ~/c1_trace.log
git checkout .
```

```console
655:VERIFY-C1 fcm.stop: before release refs=1 released=false
691:VERIFY-C1 fcm.stop: after release refs=0 (interceptors closed synchronously iff 0)
719:--- PASS: Test (4.04s)
722:ok  	google.golang.org/grpc/test/xds	5.074s
exit=0
```

Even with the 200ms hold on every RPC's reference the test passes (run time grows from ~0.2s to 4s because each client RPC only completes after the hold: the release precedes the response). At `Stop()` the only outstanding reference is the filter chain's own (`refs=1`), and it reaches 0 inside `stop`. The final interceptor close is on the test goroutine, underneath `Stop()`:

```console
VERIFY-C1 closeInterceptors called from:
goroutine 18 [running]:
...
google.golang.org/grpc/internal/xds/server.(*usableRouteConfiguration).closeInterceptors(0xc0007a8b00)
google.golang.org/grpc/internal/xds/server.(*usableRouteConfiguration).unref(0xc0007a8b00)
google.golang.org/grpc/internal/xds/server.(*usableRouteConfiguration).release(0xc0007a8b00)
google.golang.org/grpc/internal/xds/server.(*filterChainManager).stop(0xc0004bc440)
google.golang.org/grpc/internal/xds/server.(*listenerWrapper).Close(0xc0000f9c80)
google.golang.org/grpc.(*listenSocket).Close(0xc000446ca8)
google.golang.org/grpc.(*Server).closeListenersLocked(...)
google.golang.org/grpc.(*Server).stop(0xc0004b3208, 0x0)
google.golang.org/grpc.(*Server).Stop(0xc0004b3208)
google.golang.org/grpc/xds.(*GRPCServer).Stop(0xc0004bcc00)
google.golang.org/grpc/test/xds_test.setupGRPCServer.func4()
google.golang.org/grpc/test/xds_test.s.TestServerSideXDS_RDSUpdates_ReleaseSupersededInterceptors({{}}, 0xc000520700)
	/home/ubuntu/wt/e015c56e/test/xds/xds_server_rds_update_test.go:235 +0x3499
```

Superseded configurations are closed from the RPC path (`RouteAndProcess.func1` → `unref` → `closeInterceptors`) or from the RDS update, and the test polls for those with `waitForInt32` rather than asserting immediately.

Conclusion: with all preceding RPCs completed, no reference outlives the RPC, and cleanup at `Stop()` is synchronous on the calling goroutine, so the post-`Stop()` assertions cannot run before cleanup completes.

## C2

Branch `evalon/grpc-go-xd-c3af8974`. Verdict: CONFIRMED (both parts).

Relevant source on the branch (`internal/xds/server/listener_wrapper.go`):

- `Close()`: `l.closed.Fire(); l.Listener.Close(); cancelWatch(); l.rdsHandler.close(); l.mu.Lock(); ... l.activeFilterChainManager.stop(); l.activeFilterChainManager = nil` (the `= nil` is new on this branch; base keeps the stopped manager). `l.mode` is left at SERVING.
- `Accept()`: after the inner `l.Listener.Accept()` returns a conn it takes `l.mu`, checks only `l.mode == NotServing`, then calls `l.activeFilterChainManager.lookup(...)` with no nil check and no `l.closed` check. `lookup` reads `fcm.dstPrefixes` on its first line.

Repro file: `verify/repro/c2_accept_during_stop_test.go` (copy into `test/xds/`). It contains no production changes.

### Part: lookup mechanism — CONFIRMED

`TestVerifyC2_AcceptOverlappingStop_Gated`: a real `xds.NewGRPCServer` serving on a user-supplied `net.Listener` wrapper that hands one already-accepted TCP connection to `listenerWrapper.Accept()` 100ms after `Close()` was called on it (models the Serve goroutine being descheduled between the inner `Accept()` returning and `l.mu.Lock()`).

```sh
cd ~/wt/c3af8974 && cp ~/repos/grpc-go/verify/repro/c2_accept_during_stop_test.go test/xds/
go test -race -count=1 -v -timeout 60s -run '^Test$/^VerifyC2_AcceptOverlappingStop_Gated$' ./test/xds > ~/c2_gated.log 2>&1; echo exit=$?
```

```console
    c2_accept_during_stop_test.go:136: sanity RPC OK; arming listener and stopping server
    tlogger.go:133: INFO server.go:864 [core] [Server #3 ListenSocket #5] ListenSocket deleted  (t=+19.065796ms)
panic: test timed out after 1m0s
		Test/VerifyC2_AcceptOverlappingStop_Gated (1m0s)
...
goroutine 16 [chan receive]:
google.golang.org/grpc.(*Server).Serve.func1()
	/home/ubuntu/wt/c3af8974/server.go:902 +0x9a
panic({0x1ab2ee0?, 0x2ad8470?})
	/usr/local/go/src/runtime/panic.go:783 +0x132
google.golang.org/grpc/internal/xds/server.(*filterChainManager).lookup(0x0, {0x0, {{0x0, 0xffff7f000001}, {0xc0000123a8}}, {{0x0, 0xffff7f000001}, {0xc0000123a8}}, 0x8502})
	/home/ubuntu/wt/c3af8974/internal/xds/server/filter_chain_manager.go:229 +0x79
google.golang.org/grpc/internal/xds/server.(*listenerWrapper).Accept(0xc0005a2000)
	/home/ubuntu/wt/c3af8974/internal/xds/server/listener_wrapper.go:321 +0x64c
google.golang.org/grpc.(*Server).Serve(0xc0004f5d48, {0x1f641f0, 0xc0005a2000})
	/home/ubuntu/wt/c3af8974/server.go:932 +0x78e
...
sync.(*Mutex).Lock(0xc0005a2088)
google.golang.org/grpc/internal/xds/server.(*listenerWrapper).removeConn(0xc0005a2000, 0xc0005d4000)
	/home/ubuntu/wt/c3af8974/internal/xds/server/listener_wrapper.go:352 +0x3d
google.golang.org/grpc/internal/xds/server.(*connWrapper).Close(0xc0005d4000)
google.golang.org/grpc/internal/transport.(*http2Server).Close(0xc0005ec000, {0x1f480a0, 0xc00062a6a0})
google.golang.org/grpc.(*Server).closeServerTransportsLocked(0xc0004f5d48)
google.golang.org/grpc.(*Server).stop(0xc0004f5d48, 0x0)
google.golang.org/grpc.(*Server).Stop(0xc0004f5d48)
google.golang.org/grpc/xds.(*GRPCServer).Stop(0xc000684300)
google.golang.org/grpc/test/xds_test.s.TestVerifyC2_AcceptOverlappingStop_Gated({{}}, 0xc000103340)
FAIL	google.golang.org/grpc/test/xds	60.096s
exit=1
```

`lookup` is entered with a nil receiver (`lookup(0x0, ...)`) from `listenerWrapper.Accept` (listener_wrapper.go:321) and panics; the connection is not rejected. Because the panic unwinds with `listenerWrapper.mu` still locked (no deferred unlock) and `grpc.Server.Serve`'s deferred function blocks on `<-s.done.Done()` once `quit` has fired, the panic does not terminate the process: instead `Stop()` deadlocks — here in `removeConn` waiting for `l.mu` while closing the existing client connection.

Control — same test on the base commit (manager is not nil'ed by `Close()` there):

```sh
cd ~/wt/base && cp ~/repos/grpc-go/verify/repro/c2_accept_during_stop_test.go test/xds/
go test -race -count=1 -v -timeout 60s -run '^Test$/^VerifyC2_AcceptOverlappingStop_Gated$' ./test/xds
```

```console
    c2_accept_during_stop_test.go:136: sanity RPC OK; arming listener and stopping server
    c2_accept_during_stop_test.go:150: Stop() returned without a panic
    --- PASS: Test/VerifyC2_AcceptOverlappingStop_Gated (0.13s)
ok  	google.golang.org/grpc/test/xds	1.178s
```

### Part: shutdown reachability — CONFIRMED

`TestVerifyC2_AcceptOverlappingStop_Stress`: no gating, no instrumentation — a plain `testutils.LocalTCPListener()`, 16 goroutines doing `net.Dial` + `Close` against the server, then an ordinary `Stop()`; repeated in a loop.

```sh
cd ~/wt/c3af8974
VERIFY_C2_ITERS=100000 go test -race -count=1 -v -timeout 240s -run '^Test$/^VerifyC2_AcceptOverlappingStop_Stress$' ./test/xds > ~/c2_stress.log 2>&1; echo exit=$?
grep -c "Created xds.GRPCServer" ~/c2_stress.log
```

```console
10
panic: test timed out after 4m0s
...
goroutine 6 [sync.WaitGroup.Wait, 3 minutes]:
sync.(*WaitGroup).Wait(0xc00073a898)
google.golang.org/grpc.(*Server).stop(0xc00073a6c8, 0x0)
	/home/ubuntu/wt/c3af8974/server.go:1956 +0x2da
google.golang.org/grpc.(*Server).Stop(0xc00073a6c8)
google.golang.org/grpc/xds.(*GRPCServer).Stop(0xc00072c5c0)
...
google.golang.org/grpc/internal/xds/server.(*listenerWrapper).removeConn(0xc000abcb40, 0xc000b9ca20)
...
goroutine 1405 [chan receive, 3 minutes]:
google.golang.org/grpc.(*Server).Serve.func1()
	/home/ubuntu/wt/c3af8974/server.go:902 +0x9a
panic({0x1ab2ee0?, 0x2ad8470?})
google.golang.org/grpc/internal/xds/server.(*filterChainManager).lookup(0x0, {0x0, {{0x0, 0xffff7f000001}, {0xc000130378}}, {{0x0, 0xffff7f000001}, {0xc000130378}}, 0xae08})
	/home/ubuntu/wt/c3af8974/internal/xds/server/filter_chain_manager.go:229 +0x79
google.golang.org/grpc/internal/xds/server.(*listenerWrapper).Accept(0xc000abcb40)
	/home/ubuntu/wt/c3af8974/internal/xds/server/listener_wrapper.go:321 +0x64c
google.golang.org/grpc.(*Server).Serve(0xc00073a6c8, {0x1f641f0, 0xc000abcb40})
FAIL	google.golang.org/grpc/test/xds	240.049s
exit=1
```

The race was hit naturally on the 10th server start/stop (within ~165ms of test time): the Serve goroutine's inner `Accept()` had returned a connection, `Stop()` → `listenerWrapper.Close()` set the manager to nil, and `Accept()` then ran filter-chain selection on nil. Nothing orders these: `Close()` closes the inner listener and nils the manager under `l.mu`, but `Accept()` acquires `l.mu` only after the inner accept returned, and neither `l.closed` nor a nil manager is checked. `Stop()` then hung for the remaining 4 minutes (`serveWG.Wait()`; a connection goroutine is stuck in `removeConn` on the mutex the panicking goroutine still holds).

Control — same stress on the base commit, 300 iterations:

```sh
cd ~/wt/base
VERIFY_C2_ITERS=300 go test -race -count=1 -v -timeout 250s -run '^Test$/^VerifyC2_AcceptOverlappingStop_Stress$' ./test/xds
```

```console
    c2_accept_during_stop_test.go:188: 300 iterations completed without a panic
    --- PASS: Test/VerifyC2_AcceptOverlappingStop_Stress (2.78s)
ok  	google.golang.org/grpc/test/xds	3.815s
```

### Impact reasoning

Any xDS-enabled server on this branch that is stopped (`Stop()`; `GracefulStop()` closes the listener through the same `listenerWrapper.Close()`) while a client happens to be connecting can hit this; a busy server being shut down is the everyday case, and the uninstrumented loop hit it in 10 tries. The observed outcome is a swallowed nil-pointer panic on the Serve goroutine plus a permanently blocked `Stop()` (shutdown hangs) — or, when nothing else contends on `listenerWrapper.mu` so that `Stop()` completes and fires `s.done`, the panic would resume and crash the process (inferred from `Serve`'s deferred function; not separately observed). The base commit does not have the problem because it never nils the manager. No workaround from user code.

## C3

Branch `evalon/grpc-go-xd-f9c33357`. Verdict: CONFIRMED.

Repository-provided configuration (`scripts/vet.sh`): Staticcheck is installed from `test/tools` (`go install honnef.co/go/tools/cmd/staticcheck`, pinned by `test/tools/go.mod` to `honnef.co/go/tools v0.7.0`), invoked as `staticcheck -checks 'all' ./...` (line 121), and line 124 fails on any diagnostic other than ST1000, SA1019, ST1003, ST1019/"other import of", SA4000. SA4006 is not excluded.

```sh
cd ~/wt/f9c33357 && bash ~/repos/grpc-go/verify/repro/c3_staticcheck_sa4006.sh; echo exit=$?
```

```console
staticcheck 2026.1 (v0.7.0)
--- raw staticcheck output
internal/xds/server/filter_chain_manager_test.go:860:2: this value of rcf is never used (SA4006)
--- after scripts/vet.sh line 124 exclusions (anything printed here fails vet.sh)
internal/xds/server/filter_chain_manager_test.go:860:2: this value of rcf is never used (SA4006)
exit=1
```

Source (`internal/xds/server/filter_chain_manager_test.go`, in `TestFilterChain_RouteConfigurationUpdate_ReleasesSupersededResources`, which starts at line 841):

```go
	fcm.filterChains = append(fcm.filterChains, fc)
	rcf := filters[newServerFilterKey(&httpFilter)]            // line 860

	// Install an initial route configuration with two routes. One reference
	// on the server filter is held per route.
	fc.storeUsableRouteConfiguration(fc.constructUsableRouteConfiguration(routeConfigWithNRoutes(2), provider))
	rcf = filters[newServerFilterKey(&httpFilter)]             // line 865
	if rcf == nil {
```

The value assigned at line 860 is overwritten at line 865 with no read in between.

Control — same Staticcheck binary and flags on the base commit for the same package:

```sh
cd ~/wt/base && ~/scbin/staticcheck -checks all ./internal/xds/server/...; echo base_sc_exit=$?
```

```console
base_sc_exit=0
```

Impact: `scripts/vet.sh` (the repository's lint gate) fails on this branch because of the test file the solution added; runtime behaviour is unaffected. The task's lint guidelines only name `gofmt` and `go vet`, which do not report this.
