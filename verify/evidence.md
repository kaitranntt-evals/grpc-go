## C2

Claim: an RDS route-configuration update while an existing RPC is held inside `AllowRPC` blocks admission of a new connection until that RPC is released.

Target branch: [evalon/grpc-go-xd-1c231e85](https://github.com/kaitranntt-evals/grpc-go-xds-rds-interceptor-lifecycle-leak/tree/evalon/grpc-go-xd-1c231e85) at commit `e02344cb656a5a852a9c907ec3dd8f1558727237` (fetched from the repository named in the claim URL, checked out as a detached worktree at `~/work/claim`). Go 1.25.7, linux/amd64.

### Setup

```sh
cd ~/repos/grpc-go
git remote add claim https://github.com/kaitranntt-evals/grpc-go-xds-rds-interceptor-lifecycle-leak
git fetch claim evalon/grpc-go-xd-1c231e85
git worktree add ~/work/claim FETCH_HEAD
cp verify/repro/verify_c2_fresh_conn_admission_test.go ~/work/claim/test/xds/
```

### Code under test (read for orientation only; line numbers on the claim branch)

- `internal/xds/server/routing.go:48-57` — `RouteAndProcess` takes `rc.mu.RLock()` and `defer rc.mu.RUnlock()`, so the read lock spans the `rwi.interceptor.AllowRPC(ctx)` call at line 106.
- `internal/xds/server/filter_chain_manager.go:190` — `usableRouteConfiguration.close` takes `rc.mu.Lock()`; `:415-416` — `updateUsableRouteConfiguration` does `Swap(rc)` then `old.close()`.
- `internal/xds/server/listener_wrapper.go:206-223` — `handleRDSUpdate` holds `l.mu` from line 206 to 223 around `fc.updateUsableRouteConfiguration(urc)` (line 220).
- `internal/xds/server/listener_wrapper.go:298` — `Accept` takes `l.mu.Lock()` for every accepted connection.

### Experiment

`verify/repro/verify_c2_fresh_conn_admission_test.go` (no production code modified) starts a real `xds.NewGRPCServer` against the in-process management server and:

1. sends an RPC over connection #1 that blocks inside the filter's `AllowRPC` (route override absent, base filter config `path1`);
2. probes a raw fresh TCP connection (baseline: server sends its HTTP/2 SETTINGS bytes);
3. pushes an RDS update for the same route configuration (override `path2`) and waits until an RPC on the *existing* connection is served by the `path2` interceptor (update applied);
4. with the first RPC still held: raw fresh TCP probe (2s), then an RPC over a brand-new gRPC channel dialled straight at the server address (3s deadline, wait-for-ready), then dumps the goroutines in `listenerWrapper.Accept`, `handleRDSUpdate`, `usableRouteConfiguration.close` and the held `AllowRPC`;
5. releases the held RPC and repeats both fresh-connection probes.

### Run on the claim branch

```sh
cd ~/work/claim
go test -race -v -count=1 -timeout=120s -run '^Test$/^Verify_C2_FreshConnAdmissionWhileAllowRPCHeld$' ./test/xds
```

Key output (test-log lines only, verbatim; exit status 1):

```console
    verify_c2_fresh_conn_admission_test.go:144: [+  0.024s] held RPC is inside AllowRPC(path1) on connection #1
    verify_c2_fresh_conn_admission_test.go:144: [+  0.025s] BASELINE raw fresh TCP connection before RDS update: connected=true serverHandshakeBytes=15 err=<nil>
    verify_c2_fresh_conn_admission_test.go:144: [+  0.026s] RDS update (path1 -> path2) sent to management server
    verify_c2_fresh_conn_admission_test.go:144: [+  0.027s] RDS update applied: RPC on EXISTING connection #1 served by replacement interceptor (path2)
    verify_c2_fresh_conn_admission_test.go:144: [+  0.227s] retired interceptor (path1) Close(): NOT yet called while RPC still held
    verify_c2_fresh_conn_admission_test.go:144: [+  2.236s] HELD raw fresh TCP connection: connected=true serverHandshakeBytes=0 err=read tcp 127.0.0.1:44790->127.0.0.1:37365: i/o timeout
    verify_c2_fresh_conn_admission_test.go:144: [+  5.251s] HELD RPC over fresh gRPC connection #2 (3s deadline): code=DeadlineExceeded err=rpc error: code = DeadlineExceeded desc = context deadline exceeded while waiting for connections to become ready
    verify_c2_fresh_conn_admission_test.go:144: [+  5.253s] GOROUTINE (while RPC held):
    verify_c2_fresh_conn_admission_test.go:144: [+  5.254s] GOROUTINE (while RPC held):
    verify_c2_fresh_conn_admission_test.go:144: [+  5.254s] GOROUTINE (while RPC held):
    verify_c2_fresh_conn_admission_test.go:144: [+  5.254s] original RPC is still held inside AllowRPC(path1)
    verify_c2_fresh_conn_admission_test.go:144: [+  5.254s] held RPC released
    verify_c2_fresh_conn_admission_test.go:144: [+  5.254s] held RPC completed: err=<nil>
    verify_c2_fresh_conn_admission_test.go:144: [+  5.254s] retired interceptor (path1) Close() called after release
    verify_c2_fresh_conn_admission_test.go:144: [+  5.255s] RELEASED raw fresh TCP connection: connected=true serverHandshakeBytes=15 err=<nil>
    verify_c2_fresh_conn_admission_test.go:144: [+  5.255s] RELEASED RPC over fresh gRPC connection #2: code=OK err=<nil>
--- FAIL: Test (5.27s)
    --- FAIL: Test/Verify_C2_FreshConnAdmissionWhileAllowRPCHeld (5.26s)
FAIL
FAIL	google.golang.org/grpc/test/xds	5.298s
FAIL
```

Goroutine dump taken at +5.25s while the first RPC was still held (verbatim):

```console
    verify_c2_fresh_conn_admission_test.go:144: [+  5.253s] GOROUTINE (while RPC held):
        goroutine 21 [sync.RWMutex.Lock]:
        sync.runtime_SemacquireRWMutex(0xc0002a620c?, 0x1?, 0x1493ef9?)
        	/usr/local/go/src/runtime/sema.go:105 +0x25
        sync.(*RWMutex).Lock(0xc0002a61f8)
        	/usr/local/go/src/sync/rwmutex.go:155 +0x89
        google.golang.org/grpc/internal/xds/server.(*usableRouteConfiguration).close(0xc0002a61c0)
        	/home/ubuntu/work/claim/internal/xds/server/filter_chain_manager.go:190 +0x4a
        google.golang.org/grpc/internal/xds/server.(*filterChain).updateUsableRouteConfiguration(0xc00013a840, 0xc0002f8070)
        	/home/ubuntu/work/claim/internal/xds/server/filter_chain_manager.go:416 +0x59
        google.golang.org/grpc/internal/xds/server.(*listenerWrapper).handleRDSUpdate(0xc0005292c0, {0xc000200564, 0xc}, {0xc0007cc210?, {0x0?, 0x0?}})
        	/home/ubuntu/work/claim/internal/xds/server/listener_wrapper.go:220 +0x4f6
        google.golang.org/grpc/internal/xds/server.(*rdsWatcher).ResourceChanged(0xc00044b5c0, 0xc0007cc210, 0xc0003b2078)
        	/home/ubuntu/work/claim/internal/xds/server/rds_handler.go:153 +0x2fe
        google.golang.org/grpc/internal/xds/xdsclient/xdsresource.(*delegatingRouteConfigWatcher).ResourceChanged(0xc000806ed0, {0x1f64e08, 0xc0007cc210}, 0xc0003b2078)
        	/home/ubuntu/work/claim/internal/xds/xdsclient/xdsresource/route_config_resource_type.go:106 +0x77
        google.golang.org/grpc/internal/xds/clients/xdsclient.(*authority).handleADSResourceUpdate.func5({0xc0002d8cc0?, 0xc0004445a0?})
        	/home/ubuntu/work/claim/internal/xds/clients/xdsclient/authority.go:420 +0x67
        google.golang.org/grpc/internal/xds/clients/internal/syncutil.(*CallbackSerializer).run(0xc000521240, {0x1f67d48, 0xc0004445a0})
        	/home/ubuntu/work/claim/internal/xds/clients/internal/syncutil/callback_serializer.go:90 +0x1cf
        created by google.golang.org/grpc/internal/xds/clients/internal/syncutil.NewCallbackSerializer in goroutine 18
        	/home/ubuntu/work/claim/internal/xds/clients/internal/syncutil/callback_serializer.go:52 +0x205
    verify_c2_fresh_conn_admission_test.go:144: [+  5.254s] GOROUTINE (while RPC held):
        goroutine 24 [sync.Mutex.Lock]:
        internal/sync.runtime_SemacquireMutex(0x100000000?, 0x88?, 0xc000195988?)
        	/usr/local/go/src/runtime/sema.go:95 +0x25
        internal/sync.(*Mutex).lockSlow(0xc000529348)
        	/usr/local/go/src/internal/sync/mutex.go:149 +0x210
        internal/sync.(*Mutex).Lock(0xc000529348)
        	/usr/local/go/src/internal/sync/mutex.go:70 +0x55
        sync.(*Mutex).Lock(0xc000529348)
        	/usr/local/go/src/sync/mutex.go:46 +0x29
        google.golang.org/grpc/internal/xds/server.(*listenerWrapper).Accept(0xc0005292c0)
        	/home/ubuntu/work/claim/internal/xds/server/listener_wrapper.go:298 +0x136
        google.golang.org/grpc.(*Server).Serve(0xc000439208, {0x1f66f70, 0xc0005292c0})
        	/home/ubuntu/work/claim/server.go:932 +0x78e
        google.golang.org/grpc/xds.(*GRPCServer).Serve(0xc000518a40, {0x1f65620, 0xc00051b940})
        	/home/ubuntu/work/claim/xds/server.go:207 +0x47e
        created by google.golang.org/grpc/internal/stubserver.(*StubServer).StartServer in goroutine 18
        	/home/ubuntu/work/claim/internal/stubserver/stubserver.go:214 +0x13a
    verify_c2_fresh_conn_admission_test.go:144: [+  5.254s] GOROUTINE (while RPC held):
        goroutine 63 [select]:
        google.golang.org/grpc/test/xds_test.s.TestVerify_C2_FreshConnAdmissionWhileAllowRPCHeld.func3({0x1f67d10, 0xc0007562a0}, {0xc00020058b, 0x5})
        	/home/ubuntu/work/claim/test/xds/verify_c2_fresh_conn_admission_test.go:159 +0x16f
        google.golang.org/grpc/test/xds_test.(*vfyC2Interceptor).AllowRPC(0xc000544bb8, {0x1f67d10, 0xc0007562a0})
        	/home/ubuntu/work/claim/test/xds/verify_c2_fresh_conn_admission_test.go:81 +0x94
        google.golang.org/grpc/internal/xds/server.(*interceptorList).AllowRPC(0xc000544be8, {0x1f67d10, 0xc0007562a0})
        	/home/ubuntu/work/claim/internal/xds/server/filter_chain_manager.go:532 +0xa2
        google.golang.org/grpc/internal/xds/server.RouteAndProcess({0x1f67d10, 0xc0007562a0})
        	/home/ubuntu/work/claim/internal/xds/server/routing.go:106 +0xbc6
        google.golang.org/grpc/xds.xdsUnaryInterceptor({0x1f67d10, 0xc0007562a0}, {0x1b9afe0, 0xc0007562d0}, 0xc0007562a0?, 0xc000711440)
        	/home/ubuntu/work/claim/xds/server.go:236 +0x47
        google.golang.org/grpc/interop/grpc_testing._TestService_EmptyCall_Handler({0x1cc7e60, 0xc000529140}, {0x1f67d10, 0xc0007562a0}, 0xc00073e700, 0x1dc5478)
        	/home/ubuntu/work/claim/interop/grpc_testing/test_grpc.pb.go:293 +0x1e7
        google.golang.org/grpc.(*Server).processUnaryRPC(0xc000439208, {0x1f67d10, 0xc000756210}, 0xc0007401a0, 0xc00037a030, 0x2b07cc0, 0x0)
        	/home/ubuntu/work/claim/server.go:1441 +0x1a6a
        google.golang.org/grpc.(*Server).handleStream(0xc000439208, {0x1f6ccf8, 0xc00081a000}, 0xc0007401a0)
        	/home/ubuntu/work/claim/server.go:1850 +0x10b3
        google.golang.org/grpc.(*Server).serveStreams.func2.1()
        	/home/ubuntu/work/claim/server.go:1076 +0x14a
        created by google.golang.org/grpc.(*Server).serveStreams.func2 in goroutine 132
        	/home/ubuntu/work/claim/server.go:1087 +0x213
```

Reading of the observations, per part:

- *Route-configuration retirement:* goroutine 26 is parked in `sync.RWMutex.Lock` inside `usableRouteConfiguration.close` (`filter_chain_manager.go:190`), called from `updateUsableRouteConfiguration` (`:416`) from `handleRDSUpdate` (`listener_wrapper.go:220`); the held RPC's goroutine is inside `RouteAndProcess` (`routing.go:106`) -> `AllowRPC`, i.e. under the `RLock` taken at `routing.go:48`. The retired interceptor's `Close()` had not been called at +0.227s and was called only after release (+5.254s). Cleanup waits for the held RPC's read lock.
- *Connection-admission coupling:* in the same dump the `Serve` goroutine is parked in `sync.Mutex.Lock` inside `listenerWrapper.Accept` (`listener_wrapper.go:298`) on mutex `0xc000392688` (the `l.mu` of listener wrapper `0xc000392600`, the receiver of both `Accept` and `handleRDSUpdate`), while `handleRDSUpdate` is at line 220, between its `l.mu.Lock()` (206) and `l.mu.Unlock()` (223).
- *Overall behaviour:* while held, the raw fresh TCP connection completed the kernel handshake (`connected=true`) but received 0 bytes from the server in 2s (baseline: 15 bytes of SETTINGS immediately), and the RPC over a fresh gRPC channel failed after 3s with `DeadlineExceeded ... while waiting for connections to become ready`. Immediately after release, the raw probe got 15 bytes and the same fresh channel's RPC returned `OK`.

Stability: `go test -race -count=3 -timeout=200s -run '^Test$/^Verify_C2_FreshConnAdmissionWhileAllowRPCHeld$' ./test/xds 2>&1 | grep -c 'fresh connection was NOT admitted'` printed `3` (3 of 3 runs blocked).

### Control: same test on the audited reference branch

Same file copied into `test/xds/` of [grpc-go-xds-rds-interceptor-lifecycle-leak-perfect](https://github.com/kaitranntt-evals/grpc-go/tree/grpc-go-xds-rds-interceptor-lifecycle-leak-perfect) (commit `614cb7398c43bf270878ac15f34b68f5a9a4159d`), same command (exit status 0):

```console
    verify_c2_fresh_conn_admission_test.go:144: [+  0.026s] held RPC is inside AllowRPC(path1) on connection #1
    verify_c2_fresh_conn_admission_test.go:144: [+  0.026s] BASELINE raw fresh TCP connection before RDS update: connected=true serverHandshakeBytes=15 err=<nil>
    verify_c2_fresh_conn_admission_test.go:144: [+  0.027s] RDS update (path1 -> path2) sent to management server
    verify_c2_fresh_conn_admission_test.go:144: [+  0.350s] RDS update applied: RPC on EXISTING connection #1 served by replacement interceptor (path2)
    verify_c2_fresh_conn_admission_test.go:144: [+  0.551s] retired interceptor (path1) Close(): ALREADY CALLED while RPC still held
    verify_c2_fresh_conn_admission_test.go:144: [+  0.551s] HELD raw fresh TCP connection: connected=true serverHandshakeBytes=24 err=<nil>
    verify_c2_fresh_conn_admission_test.go:144: [+  0.554s] HELD RPC over fresh gRPC connection #2 (3s deadline): code=OK err=<nil>
    verify_c2_fresh_conn_admission_test.go:144: [+  0.559s] GOROUTINE (while RPC held):
    verify_c2_fresh_conn_admission_test.go:144: [+  0.559s] GOROUTINE (while RPC held):
    verify_c2_fresh_conn_admission_test.go:144: [+  0.559s] original RPC is still held inside AllowRPC(path1)
    verify_c2_fresh_conn_admission_test.go:144: [+  0.559s] held RPC released
    verify_c2_fresh_conn_admission_test.go:144: [+  0.559s] held RPC completed: err=<nil>
    verify_c2_fresh_conn_admission_test.go:144: [+  0.559s] retired interceptor (path1) Close() called after release
    verify_c2_fresh_conn_admission_test.go:144: [+  0.560s] RELEASED raw fresh TCP connection: connected=true serverHandshakeBytes=15 err=<nil>
    verify_c2_fresh_conn_admission_test.go:144: [+  0.560s] RELEASED RPC over fresh gRPC connection #2: code=OK err=<nil>
--- PASS: Test (0.57s)
    --- PASS: Test/Verify_C2_FreshConnAdmissionWhileAllowRPCHeld (0.56s)
PASS
ok  	google.golang.org/grpc/test/xds	1.599s
```

There the fresh raw connection gets 24 handshake bytes and the fresh-channel RPC returns `OK` while the original RPC is still held, so the test harness itself does not cause the stall; the stall is specific to the claim branch.

### Eval fixtures do not catch it

Archived fixtures copied byte-exact to `test/xds/eval_xds_server_interceptor_leak_test.go` and `internal/xds/server/eval_rds_error_after_success_test.go` in the claim worktree:

```sh
cd ~/work/claim
go test -race -v -count=1 -run '^Test$/^Eval_ServerSideXDS_ReplacementRPCWhile' ./test/xds
```

```console
--- PASS: Test (0.72s)
    --- PASS: Test/Eval_ServerSideXDS_ReplacementRPCWhileAllowRPCHeld (0.36s)
    --- PASS: Test/Eval_ServerSideXDS_ReplacementRPCWhileRetiredCloseBlocked (0.02s)
    --- PASS: Test/Eval_ServerSideXDS_ReplacementRPCWhileRetiredCloseBlocksAcceptedRPC (0.32s)
    --- PASS: Test/Eval_ServerSideXDS_ReplacementRPCWhileServerFilterCloseHeld (0.02s)
PASS
ok  	google.golang.org/grpc/test/xds	1.751s
```

`TestEval_ServerSideXDS_ReplacementRPCWhileAllowRPCHeld` sends its probes over the same `ClientConn` (already-admitted connection) as the held RPC, so it never exercises `Accept` and stays green.

### Impact reasoning

Any xDS-enabled server on the claim branch that has one RPC sitting inside an HTTP filter's `AllowRPC` (e.g. an authorization filter doing a slow check) when an RDS update for that route configuration arrives stops admitting new connections on that listener: `handleRDSUpdate` holds `l.mu` while `old.close()` waits for the write lock, and `Accept` needs `l.mu` for every connection. TCP connects still succeed (kernel backlog) but the server never starts the HTTP/2 handshake, so new clients hang until their own deadline. The xDS client's callback serializer goroutine is the one blocked, so further xDS updates for that client are also queued behind it. `removeConn` and `Close` take `l.mu` too. Existing connections keep working (the new configuration is published before `close()`), which is why this looks healthy from already-connected clients. The stall lasts exactly as long as the slowest in-flight `AllowRPC` on the superseded configuration; there is no workaround other than bounding `AllowRPC` duration. RDS updates to an already-served route configuration are the everyday path the original task is about.
