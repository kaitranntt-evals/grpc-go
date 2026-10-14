## C3

Claim: an RDS replacement performed while an existing RPC is held inside `AllowRPC` blocks admission of a new connection on the same listener until the held call is released. Target branch: `evalon/grpc-go-xd-1c231e85` in `kaitranntt-evals/grpc-go-xds-rds-interceptor-lifecycle-leak` (commit `e02344cb`).

Observed result: both parts hold on the target branch.

### Setup

```sh
cd ~/repos/grpc-go
git remote add target https://github.com/kaitranntt-evals/grpc-go-xds-rds-interceptor-lifecycle-leak
git fetch target evalon/grpc-go-xd-1c231e85
git worktree add ~/wt-c3 FETCH_HEAD      # HEAD is now at e02344cb chore: apply eval changes
cp verify/repro/c3_new_conn_blocked_test.go ~/wt-c3/test/xds/zz_verify_c3_test.go
cd ~/wt-c3
go test -race -v -count=1 -timeout=120s -run '^Test$/^Verify_C3_NewConnBlockedWhileAllowRPCHeld$' ./test/xds
```

The repro (`verify/repro/c3_new_conn_blocked_test.go`) starts an xDS server with one RDS-backed filter chain and a test HTTP filter, then:

1. sends an RPC that parks inside `AllowRPC` of the `path1` interceptor;
2. pushes an RDS update that switches the route to a `path2` interceptor, and waits until an RPC on the existing connection is served by `path2`;
3. opens a brand-new `grpc.ClientConn` straight to the listener address and issues an RPC with `WaitForReady` and a 3s deadline, dumping the goroutines that are in `handleRDSUpdate` / `listenerWrapper.Accept`;
4. releases the parked `AllowRPC` and repeats the new-connection RPC.

### Output on the target branch (key lines, verbatim)

```console
    zz_verify_c3_test.go:211: STEP1 existing RPC is held inside AllowRPC(path1)
    zz_verify_c3_test.go:235: STEP2 replacement route (path2) served on EXISTING connection while AllowRPC(path1) held: true
    zz_verify_c3_test.go:262: STEP3 new-connection RPC while AllowRPC(path1) held: err=rpc error: code = DeadlineExceeded desc = context deadline exceeded while waiting for connections to become ready code=DeadlineExceeded served_by="" elapsed=3s connState=CONNECTING
    zz_verify_c3_test.go:271: STEP4 released AllowRPC(path1)
    zz_verify_c3_test.go:274: STEP4 held RPC finished: err=<nil>
    zz_verify_c3_test.go:287: STEP4 new-connection RPC after release: err=<nil> served_by="path2" elapsed=1ms connState=READY
    zz_verify_c3_test.go:289: STEP4 goroutines still in handleRDSUpdate after release: 0
    zz_verify_c3_test.go:292: C3 REPRODUCED: new connection was not admitted while an existing RPC was held in AllowRPC during an RDS replacement (err=rpc error: code = DeadlineExceeded desc = context deadline exceeded while waiting for connections to become ready); admitted after release (err=<nil>)
--- FAIL: Test (3.42s)
    --- FAIL: Test/Verify_C3_NewConnBlockedWhileAllowRPCHeld (3.41s)
```

#### Part 1 — update locking mechanism

Goroutine dumped at STEP3, while `AllowRPC(path1)` is still held. `handleRDSUpdate` (which took `l.mu` at `listener_wrapper.go:206` and releases it at `:223`) is parked at `:220` inside `old.close()`, waiting for the write side of the old configuration's `RWMutex`:

```console
        goroutine 33 [sync.RWMutex.Lock]:
        sync.runtime_SemacquireRWMutex(0xc0002c874c?, 0x2?, 0x14ffe79?)
        	/usr/local/go/src/runtime/sema.go:105 +0x25
        sync.(*RWMutex).Lock(0xc0002c8738)
        	/usr/local/go/src/sync/rwmutex.go:155 +0x89
        google.golang.org/grpc/internal/xds/server.(*usableRouteConfiguration).close(0xc0002c8700)
        	/home/ubuntu/wt-c3/internal/xds/server/filter_chain_manager.go:190 +0x4a
        google.golang.org/grpc/internal/xds/server.(*filterChain).updateUsableRouteConfiguration(0xc00044d080, 0xc0002e25b0)
        	/home/ubuntu/wt-c3/internal/xds/server/filter_chain_manager.go:416 +0x59
        google.golang.org/grpc/internal/xds/server.(*listenerWrapper).handleRDSUpdate(0xc000465d40, {0xc0004906c4, 0xc}, {0xc0007c6600?, {0x0?, 0x0?}})
        	/home/ubuntu/wt-c3/internal/xds/server/listener_wrapper.go:220 +0x4f6
        google.golang.org/grpc/internal/xds/server.(*rdsWatcher).ResourceChanged(0xc00049b8c0, 0xc0007c6600, 0xc000012348)
```

The read side is held by the parked RPC: `RouteAndProcess` in `routing.go` does `rc.mu.RLock()` and `defer rc.mu.RUnlock()`, and calls `rwi.interceptor.AllowRPC(ctx)` in between. After release, `STEP4 goroutines still in handleRDSUpdate after release: 0`.

#### Part 2 — connection-admission trigger

Same dump: the server's accept loop has taken the new TCP connection off the kernel queue and is parked on `l.mu` at `listener_wrapper.go:298` (same mutex address `0xc000465dc8` belongs to listenerWrapper `0xc000465d40`, the receiver of the `handleRDSUpdate` frame above):

```console
        goroutine 36 [sync.Mutex.Lock]:
        internal/sync.runtime_SemacquireMutex(0x100000000?, 0x98?, 0xc00017b988?)
        	/usr/local/go/src/runtime/sema.go:95 +0x25
        internal/sync.(*Mutex).lockSlow(0xc000465dc8)
        	/usr/local/go/src/internal/sync/mutex.go:149 +0x210
        internal/sync.(*Mutex).Lock(0xc000465dc8)
        	/usr/local/go/src/internal/sync/mutex.go:70 +0x55
        sync.(*Mutex).Lock(0xc000465dc8)
        	/usr/local/go/src/sync/mutex.go:46 +0x29
        google.golang.org/grpc/internal/xds/server.(*listenerWrapper).Accept(0xc000465d40)
        	/home/ubuntu/wt-c3/internal/xds/server/listener_wrapper.go:298 +0x136
        google.golang.org/grpc.(*Server).Serve(0xc000443208, {0x1f659a0, 0xc000465d40})
```

The new channel stayed `CONNECTING` for the whole 3s and its RPC failed with `DeadlineExceeded ... while waiting for connections to become ready`; 1ms after the held `AllowRPC` was released the same channel was `READY` and served by `path2`.

### Contrast: same repro on the audited reference branch

```sh
cd ~/repos/grpc-go     # origin/grpc-go-xds-rds-interceptor-lifecycle-leak-perfect
cp verify/repro/c3_new_conn_blocked_test.go test/xds/zz_verify_c3_test.go
go test -race -v -count=1 -timeout=120s -run '^Test$/^Verify_C3_NewConnBlockedWhileAllowRPCHeld$' ./test/xds
rm test/xds/zz_verify_c3_test.go
```

```console
    zz_verify_c3_test.go:235: STEP2 replacement route (path2) served on EXISTING connection while AllowRPC(path1) held: true
    zz_verify_c3_test.go:262: STEP3 new-connection RPC while AllowRPC(path1) held: err=<nil> code=OK served_by="path2" elapsed=2ms connState=READY
    zz_verify_c3_test.go:294: C3 NOT REPRODUCED: new connection admitted and served while AllowRPC(path1) was held
--- PASS: Test (0.43s)
    --- PASS: Test/Verify_C3_NewConnBlockedWhileAllowRPCHeld (0.42s)
```

So the repro itself does not manufacture the stall; it is specific to the target branch's locking.

### The attached eval fixture does not catch this

```sh
cd ~/wt-c3
cp ~/fx/tests/eval_xds_server_interceptor_leak_test.go test/xds/     # byte-exact from eval_tests.zip
go test -race -v -count=1 -run '^Test$/^Eval_ServerSideXDS_ReplacementRPCWhileAllowRPCHeld$' ./test/xds
```

```console
--- PASS: Test (0.37s)
    --- PASS: Test/Eval_ServerSideXDS_ReplacementRPCWhileAllowRPCHeld (0.36s)
PASS
ok  	google.golang.org/grpc/test/xds	1.411s
```

That fixture probes only through the already-established client connection, which reads the swapped-in configuration (the target branch swaps before closing the old one) and never goes through `Accept()`. Only this one fixture test was run on the target branch.

### Impact reasoning

- Trigger: any RDS update to a route configuration in use, arriving while at least one RPC on the old configuration is still inside an HTTP filter's `AllowRPC`. In the repro `AllowRPC` is parked artificially; how long real filters stay in `AllowRPC` was not measured. The stall lasts exactly as long as the slowest such call.
- Effect during the stall (observed): the listener accepts no new connections — the client channel sits in `CONNECTING` and RPCs on it fail at their deadline. RPCs on already-established connections keep working and use the new route.
- Not observed, inferred from the code only: `handleRDSUpdate` runs on the xDS client's callback serializer (visible in the stack: `CallbackSerializer.run`), so further xDS updates for that client are likely queued behind it; `removeConn`, `Close` and `maybeUpdateFilterChains` also take `l.mu`.
- No workaround was tested.
