# Evidence — run v-bf9ddc36

Audited implementation: origin/grpc-go-endpointsharding-decouple-locking-perfect @ 81201fc085e37de0ffb23505ac7831350b326c33. Base (original coarse locking): bf9e7cd3430df40d0732ba42eb88bd5f2cc63407. Claim-target branches are `evalon/grpc-go-en-<suffix>` in https://github.com/kaitranntt-evals/grpc-go-endpointsharding-decouple-locking. Every repro script under verify/repro creates its own detached worktree (in $TMPDIR), applies only the listed audit instrumentation there, and never touches the checkout it is run from. Toolchain: go1.25.7 linux/amd64 with -race.

Baseline: on every target branch, the unmodified packages pass:
```sh
go test -race -count=1 ./balancer/endpointsharding/ ./balancer/ringhash/
```
```console
ok  	google.golang.org/grpc/balancer/endpointsharding	(all 20 worktrees)
ok  	google.golang.org/grpc/balancer/ringhash	(all 20 worktrees)
```
On 072542f0 the attached eval fixture (eval_tests/tests/eval_endpointsharding_test.go, copied byte-exact into balancer/endpointsharding/) passes all eight TestEval_* tests (`ok  google.golang.org/grpc/balancer/endpointsharding 4.447s`).

## C1

Target: evalon/grpc-go-en-2c1919e2 (5284873f6f4191fae2c940d049294e802f1eb5c6). Candidate regression: `TestChildExitIdleDuringOtherChildUpdate` in balancer/endpointsharding/endpointsharding_concurrency_test.go.

Synchronization traced from the test source (lines 73-160):
```go
// child A ("blocked") holds the contested per-child mutex inside UpdateClientConnState:
if addr == "blocked" {
    bd.ClientConn.UpdateState(... Ready ...)
    close(updateStarted)
    select {
    case <-releaseUpdate:   // released only by unblock()
    case <-ctx.Done():      // shared deadline (defaultTestTimeout = 10s)
    }
}
// child B ("idle"): ExitIdle runs its synchronous UpdateState callback, then signals.
ExitIdle: func(bd *stub.BalancerData) {
    bd.ClientConn.UpdateState(... Connecting ...)
    exitedIdle <- struct{}{}
},
...
defer b.Close()
defer unblock()                       // failure path releases A before Close
...
waitForSignal(ctx, t, updateStarted, "blocked child update")
idleChild.ExitIdle()                  // async: ChildState.ExitIdle -> go bw.exitIdle()
waitForSignal(ctx, t, exitedIdle, "unrelated child to exit idle while update is blocked")
...
unblock()                             // A released only after B's completion is observed
```
So A is released either by `unblock()` after `exitedIdle` (sent after B's synchronous callback) is received, or by the shared `ctx` deadline, which is the same deadline on which the progress assertion `waitForSignal(exitedIdle)` fails. `idleChild.ExitIdle()` returns immediately (asynchronous dispatch), so the test goroutine is already parked in the `exitedIdle` select when the deadline fires, and wakes on `ctx.Done()`.

Execution: the regression under a coarse shared child mutex (the original locking model: every per-child `bw.childMu` acquisition replaced by one package-level mutex; diff in verify/repro/c1-coarse.diff):
```sh
verify/repro/c1_coarse_lock.sh
```
```console
      2     --- FAIL: Test/ChildExitIdleDuringOtherChildUpdate (10.00s)
      1     --- FAIL: Test/ChildExitIdleDuringOtherChildUpdate (10.01s)
      1     --- FAIL: Test/ChildExitIdleDuringOtherChildUpdate (10.04s)
      4     --- FAIL: Test/ChildExitIdleDuringOtherChildUpdate (10.05s)
      8     endpointsharding_concurrency_test.go:147: Timed out waiting for unrelated child to exit idle while update is blocked
      2 FAIL
      1 FAIL	google.golang.org/grpc/balancer/endpointsharding	80.275s
```
All 8 runs reached the deadline and the progress assertion failed (line 147); none accepted B's completion after a deadline release of A. The unmodified branch passes (baseline above). I could not produce an execution where the shared deadline released A and the test then accepted B's completion.

## C2

Method (per branch): take the branch's changed independent-progress concurrency test and inject a stalled production worker so that its failure path is exercised: `ES_STALL=1 ES_STALL_MS=3` makes a child `UpdateClientConnState` that the fixture held for >3ms never return (it keeps holding the per-child lock the production code holds around that call), and `ES_DROP_EXITIDLE=1` drops child ExitIdle deliveries so the progress assertion times out. The instrumentation is verify/repro/c2_zz_mut.go plus a sed on endpointsharding.go (see verify/repro/c2_stalled_worker.sh). An indefinite wait shows up as `panic: test timed out` with the test goroutine's stack.

Replay (per branch):
```sh
verify/repro/c2_stalled_worker.sh <suffix> '<test regex>'   # uses -timeout 60s
```
Recorded runs used the same instrumentation with `go test -race -count=1 -timeout 150s -v -run '<test>' ./balancer/endpointsharding/` under `timeout 200`.

### 6a9bc89d

```console
$ ES_STALL=1 ES_STALL_MS=3 ES_DROP_EXITIDLE=1 go test -race -count=1 -timeout 150s -v -run '^Test$/^EndpointShardingExitIdleWhileOtherChildBlocked$' ./balancer/endpointsharding/
# S2 ^Test$/^EndpointShardingExitIdleWhileOtherChildBlocked$ rc=1 dur=21s timedout=0 leak=2 stallfired=0
    endpointsharding_ext_test.go:481: Timed out waiting for ExitIdle to be called on child A while another child is blocked
    grpctest.go:45: Leaked goroutine: goroutine 11 [chan receive]:
# test-file frames still on a stack at exit:
    balancer/endpointsharding/endpointsharding_ext_test.go:384 +0x3ca
    balancer/endpointsharding/endpointsharding_ext_test.go:461 +0x1271
    balancer/endpointsharding/endpointsharding_ext_test.go:462 +0x2e7
```
The run terminated (21s) but the goroutine-leak checker reports the fixture goroutine still parked at endpointsharding_ext_test.go:384 (`<-b.updateUnblock` inside `blockingChild.UpdateClientConnState` for child C). Source: `close(updateUnblock)` is only reached after the ExitIdle and no-picker assertions; every `t.Fatal` after the blocked update is started (lines 469, 478, 481) returns through `defer es.Close()` only, with no release of `updateUnblock` and no independent deadline in the fixture. Replay with verify/repro/c2_stalled_worker.sh gave the same result:
```console
duration=22s log=<log>
    endpointsharding_ext_test.go:481: Timed out waiting for ExitIdle to be called on child A while another child is blocked
    grpctest.go:45: Leaked goroutine: goroutine 11 [chan receive]:
--- goroutines with test frames at exit:
# leaked goroutine 11 frames: balancer/endpointsharding/endpointsharding_ext_test.go:384, :461, :462
```

### 7d4c90b4

```console
$ ES_STALL=1 ES_STALL_MS=3 ES_DROP_EXITIDLE=1 go test -race -count=1 -timeout 150s -v -run '^Test$/^EndpointShardingExitIdleNotBlockedByOtherChild$' ./balancer/endpointsharding/
# S2 ^Test$/^EndpointShardingExitIdleNotBlockedByOtherChild$ rc=1 dur=150s timedout=1 leak=0 stallfired=1
    endpointsharding_ext_test.go:471: Timed out waiting for ExitIdle to be delivered to the idle child
panic: test timed out after 2m30s
goroutine 9 [chan receive]:
google.golang.org/grpc/balancer/endpointsharding_test.Test(...)
goroutine 10 [chan receive]:
google.golang.org/grpc/balancer/endpointsharding_test.s.TestEndpointShardingExitIdleNotBlockedByOtherChild.func4()
google.golang.org/grpc/balancer/endpointsharding_test.s.TestEndpointShardingExitIdleNotBlockedByOtherChild({{}}, 0xc0000bec40)
goroutine 11 [chan receive]:
google.golang.org/grpc/balancer/endpointsharding.stallIfSlow(...)
google.golang.org/grpc/balancer/endpointsharding.(*balancerWrapper).updateClientConnState(...)
google.golang.org/grpc/balancer/endpointsharding.(*endpointSharding).UpdateClientConnState(...)
google.golang.org/grpc/balancer/endpointsharding_test.s.TestEndpointShardingExitIdleNotBlockedByOtherChild.func3()
# test-file frames still on a stack at exit:
    balancer/endpointsharding/endpointsharding_ext_test.go:448 +0x15c5
    balancer/endpointsharding/endpointsharding_ext_test.go:450 +0x124
    balancer/endpointsharding/endpointsharding_ext_test.go:454 +0x45
    balancer/endpointsharding/endpointsharding_ext_test.go:471 +0x1985
    balancer/endpointsharding/endpointsharding_ext_test.go:68 +0x35
```
The test goroutine is parked in the deferred func4 at endpointsharding_ext_test.go:454, i.e. `defer func() { close(unblock); <-updateDone }()`: an unbounded receive whose completion depends on the stalled production update. Replay:
```console
duration=62s log=<log>
    endpointsharding_ext_test.go:471: Timed out waiting for ExitIdle to be delivered to the idle child
AUDIT: stalling update worker after held child call (10.023727899s)
panic: test timed out after 1m0s
--- goroutines with test frames at exit:
goroutine 37 [chan receive]:
google.golang.org/grpc/balancer/endpointsharding_test.Test(0xc000102e00)
	balancer/endpointsharding/endpointsharding_ext_test.go:68 +0x35
goroutine 38 [chan receive]:
google.golang.org/grpc/balancer/endpointsharding_test.s.TestEndpointShardingExitIdleNotBlockedByOtherChild.func4()
	balancer/endpointsharding/endpointsharding_ext_test.go:454 +0x45
google.golang.org/grpc/balancer/endpointsharding_test.s.TestEndpointShardingExitIdleNotBlockedByOtherChild({{}}, 0xc000102fc0)
	balancer/endpointsharding/endpointsharding_ext_test.go:471 +0x1985
```

### cc0c22d2

```console
$ ES_STALL=1 ES_STALL_MS=3 ES_DROP_EXITIDLE=1 go test -race -count=1 -timeout 150s -v -run '^Test$/^EndpointShardingExitIdleWhileOtherChildBlocked$' ./balancer/endpointsharding/
# S2 ^Test$/^EndpointShardingExitIdleWhileOtherChildBlocked$ rc=1 dur=150s timedout=1 leak=0 stallfired=1
    endpointsharding_ext_test.go:482: Timed out waiting for ExitIdle to be called on child A while child B is blocked
panic: test timed out after 2m30s
goroutine 22 [chan receive]:
google.golang.org/grpc/balancer/endpointsharding_test.Test(...)
goroutine 23 [sync.Mutex.Lock]:
google.golang.org/grpc/balancer/endpointsharding.(*endpointSharding).Close(...)
google.golang.org/grpc/balancer/endpointsharding_test.s.TestEndpointShardingExitIdleWhileOtherChildBlocked({{}}, 0xc000103500)
goroutine 24 [chan receive]:
google.golang.org/grpc/balancer/endpointsharding.stallIfSlow(...)
google.golang.org/grpc/balancer/endpointsharding.(*balancerWrapper).updateClientConnState(...)
google.golang.org/grpc/balancer/endpointsharding.(*endpointSharding).UpdateClientConnState(...)
google.golang.org/grpc/balancer/endpointsharding_test.s.TestEndpointShardingExitIdleWhileOtherChildBlocked.func4()
# test-file frames still on a stack at exit:
    balancer/endpointsharding/endpointsharding_ext_test.go:470 +0x1105
    balancer/endpointsharding/endpointsharding_ext_test.go:470 +0x9e
    balancer/endpointsharding/endpointsharding_ext_test.go:482 +0x12b0
    balancer/endpointsharding/endpointsharding_ext_test.go:67 +0x35
```
After the progress assertion times out, the test's deferred cleanup calls `Close()` without establishing that the stalled update worker has settled; `Close` blocks on the per-child mutex (`balancerWrapper.close` / `endpointSharding.Close` in state `sync.Mutex.Lock`) held by that worker until the test binary's timeout.
Replay:
```console
duration=63s log=<log>
    endpointsharding_ext_test.go:482: Timed out waiting for ExitIdle to be called on child A while child B is blocked
AUDIT: stalling update worker after held child call (10.022899179s)
panic: test timed out after 1m0s
--- goroutines with test frames at exit:
goroutine 9 [chan receive]:
google.golang.org/grpc/balancer/endpointsharding_test.Test(0xc0000bea80)
	balancer/endpointsharding/endpointsharding_ext_test.go:67 +0x35
goroutine 10 [sync.Mutex.Lock]:
google.golang.org/grpc/balancer/endpointsharding.(*endpointSharding).Close(0xc000236640)
	balancer/endpointsharding/endpointsharding.go:235 +0x4f
google.golang.org/grpc/balancer/endpointsharding_test.s.TestEndpointShardingExitIdleWhileOtherChildBlocked({{}}, 0xc0000bec40)
	balancer/endpointsharding/endpointsharding_ext_test.go:482 +0x12b0
```

### e7a946ea

```console
$ ES_STALL=1 ES_STALL_MS=3 ES_DROP_EXITIDLE=1 go test -race -count=1 -timeout 150s -v -run '^Test$/^EndpointShardingExitIdleNotBlockedByOtherChildUpdate$' ./balancer/endpointsharding/
# S2 ^Test$/^EndpointShardingExitIdleNotBlockedByOtherChildUpdate$ rc=1 dur=151s timedout=1 leak=0 stallfired=1
    endpointsharding_ext_test.go:548: Timed out waiting for ExitIdle on child "addr-a" while child "addr-b" was blocked in UpdateClientConnState
panic: test timed out after 2m30s
goroutine 9 [chan receive]:
google.golang.org/grpc/balancer/endpointsharding_test.Test(...)
goroutine 10 [sync.Mutex.Lock]:
google.golang.org/grpc/balancer/endpointsharding.(*balancerWrapper).close(...)
google.golang.org/grpc/balancer/endpointsharding.(*endpointSharding).Close-range1(...)
google.golang.org/grpc/balancer/endpointsharding.(*endpointSharding).Close.(*EndpointMap[...]).All.func1(...)
google.golang.org/grpc/balancer/endpointsharding.(*endpointSharding).Close(...)
google.golang.org/grpc/balancer/endpointsharding_test.s.TestEndpointShardingExitIdleNotBlockedByOtherChildUpdate({{}}, 0xc0000bec40)
goroutine 11 [chan receive]:
google.golang.org/grpc/balancer/endpointsharding.stallIfSlow(...)
# test-file frames still on a stack at exit:
    balancer/endpointsharding/endpointsharding_ext_test.go:532 +0x9e
    balancer/endpointsharding/endpointsharding_ext_test.go:532 +0xfe9
    balancer/endpointsharding/endpointsharding_ext_test.go:548 +0x1319
    balancer/endpointsharding/endpointsharding_ext_test.go:68 +0x35
```
After the progress assertion times out, the test's deferred cleanup calls `Close()` without establishing that the stalled update worker has settled; `Close` blocks on the per-child mutex (`balancerWrapper.close` / `endpointSharding.Close` in state `sync.Mutex.Lock`) held by that worker until the test binary's timeout.

### 866a4ecf

```console
$ ES_STALL=1 ES_STALL_MS=3 ES_DROP_EXITIDLE=1 go test -race -count=1 -timeout 150s -v -run '^Test$/^EndpointShardingExitIdleDuringBlockedChildUpdate$' ./balancer/endpointsharding/
# S2 ^Test$/^EndpointShardingExitIdleDuringBlockedChildUpdate$ rc=1 dur=150s timedout=1 leak=0 stallfired=1
    endpointsharding_sync_ext_test.go:288: Timeout waiting for ExitIdle on child "addr-a"
panic: test timed out after 2m30s
goroutine 9 [chan receive]:
google.golang.org/grpc/balancer/endpointsharding_test.Test(...)
goroutine 10 [sync.Mutex.Lock]:
google.golang.org/grpc/balancer/endpointsharding.(*balancerWrapper).close(...)
google.golang.org/grpc/balancer/endpointsharding.(*endpointSharding).Close-range1(...)
google.golang.org/grpc/balancer/endpointsharding.(*endpointSharding).Close.(*EndpointMap[...]).All.func1(...)
google.golang.org/grpc/balancer/endpointsharding.(*endpointSharding).Close(...)
google.golang.org/grpc/balancer/endpointsharding_test.(*childTracker).awaitExitIdle(...)
google.golang.org/grpc/balancer/endpointsharding_test.s.TestEndpointShardingExitIdleDuringBlockedChildUpdate({{}}, 0xc0000bec40)
goroutine 12 [chan receive]:
# test-file frames still on a stack at exit:
    balancer/endpointsharding/endpointsharding_ext_test.go:65 +0x35
    balancer/endpointsharding/endpointsharding_sync_ext_test.go:129 +0x30e
    balancer/endpointsharding/endpointsharding_sync_ext_test.go:274 +0x13cd
    balancer/endpointsharding/endpointsharding_sync_ext_test.go:275 +0x2fd
    balancer/endpointsharding/endpointsharding_sync_ext_test.go:288 +0x1537
```
After the progress assertion times out, the test's deferred cleanup calls `Close()` without establishing that the stalled update worker has settled; `Close` blocks on the per-child mutex (`balancerWrapper.close` / `endpointSharding.Close` in state `sync.Mutex.Lock`) held by that worker until the test binary's timeout.

### 87f30813

```console
$ ES_STALL=1 ES_STALL_MS=3 ES_DROP_EXITIDLE=1 go test -race -count=1 -timeout 150s -v -run '^Test$/^EndpointShardingExitIdleNotBlockedByOtherChild$' ./balancer/endpointsharding/
# S2 ^Test$/^EndpointShardingExitIdleNotBlockedByOtherChild$ rc=1 dur=151s timedout=1 leak=0 stallfired=1
    endpointsharding_ext_test.go:581: Timeout waiting for ExitIdle on the first child while the second child's update is blocked
panic: test timed out after 2m30s
goroutine 22 [chan receive]:
google.golang.org/grpc/balancer/endpointsharding_test.Test(...)
goroutine 23 [sync.Mutex.Lock]:
google.golang.org/grpc/balancer/endpointsharding.(*balancerWrapper).close(...)
google.golang.org/grpc/balancer/endpointsharding.(*endpointSharding).Close-range1(...)
google.golang.org/grpc/balancer/endpointsharding.(*endpointSharding).Close.(*EndpointMap[...]).All.func1(...)
google.golang.org/grpc/balancer/endpointsharding.(*endpointSharding).Close(...)
google.golang.org/grpc/balancer/endpointsharding_test.s.TestEndpointShardingExitIdleNotBlockedByOtherChild({{}}, 0xc000103500)
goroutine 24 [chan receive]:
google.golang.org/grpc/balancer/endpointsharding.stallIfSlow(...)
# test-file frames still on a stack at exit:
    balancer/endpointsharding/endpointsharding_ext_test.go:569 +0x905
    balancer/endpointsharding/endpointsharding_ext_test.go:569 +0x9e
    balancer/endpointsharding/endpointsharding_ext_test.go:581 +0xb1a
    balancer/endpointsharding/endpointsharding_ext_test.go:66 +0x35
```
After the progress assertion times out, the test's deferred cleanup calls `Close()` without establishing that the stalled update worker has settled; `Close` blocks on the per-child mutex (`balancerWrapper.close` / `endpointSharding.Close` in state `sync.Mutex.Lock`) held by that worker until the test binary's timeout.

### fa97a924

```console
$ ES_STALL=1 ES_STALL_MS=3 ES_DROP_EXITIDLE=1 go test -race -count=1 -timeout 150s -v -run '^Test$/^EndpointShardingExitIdleWhileOtherChildBlocked$' ./balancer/endpointsharding/
# S2 ^Test$/^EndpointShardingExitIdleWhileOtherChildBlocked$ rc=1 dur=151s timedout=1 leak=0 stallfired=1
    endpointsharding_ext_test.go:512: Timeout waiting for ExitIdle to be delivered to child 1 while child 2 is blocked
panic: test timed out after 2m30s
goroutine 9 [chan receive]:
google.golang.org/grpc/balancer/endpointsharding_test.Test(...)
goroutine 10 [sync.Mutex.Lock]:
google.golang.org/grpc/balancer/endpointsharding.(*balancerWrapper).close(...)
google.golang.org/grpc/balancer/endpointsharding.(*endpointSharding).Close-range1(...)
google.golang.org/grpc/balancer/endpointsharding.(*endpointSharding).Close.(*EndpointMap[...]).All.func1(...)
google.golang.org/grpc/balancer/endpointsharding.(*endpointSharding).Close(...)
google.golang.org/grpc/balancer/endpointsharding_test.s.TestEndpointShardingExitIdleWhileOtherChildBlocked({{}}, 0xc0000bec40)
goroutine 11 [chan receive]:
google.golang.org/grpc/balancer/endpointsharding.stallIfSlow(...)
# test-file frames still on a stack at exit:
    balancer/endpointsharding/endpointsharding_ext_test.go:496 +0x9e
    balancer/endpointsharding/endpointsharding_ext_test.go:496 +0xce5
    balancer/endpointsharding/endpointsharding_ext_test.go:512 +0xf9e
    balancer/endpointsharding/endpointsharding_ext_test.go:67 +0x35
```
After the progress assertion times out, the test's deferred cleanup calls `Close()` without establishing that the stalled update worker has settled; `Close` blocks on the per-child mutex (`balancerWrapper.close` / `endpointSharding.Close` in state `sync.Mutex.Lock`) held by that worker until the test binary's timeout.

### 32618793

```console
$ ES_STALL=1 ES_STALL_MS=3 ES_DROP_EXITIDLE=1 go test -race -count=1 -timeout 150s -v -run '^Test$/^EndpointShardingExitIdleNotBlockedByOtherChild$' ./balancer/endpointsharding/
# S2 ^Test$/^EndpointShardingExitIdleNotBlockedByOtherChild$ rc=1 dur=151s timedout=1 leak=0 stallfired=1
    endpointsharding_ext_test.go:564: Timeout waiting for ExitIdle to be called on child A while the update to child B is blocked
panic: test timed out after 2m30s
goroutine 22 [chan receive]:
google.golang.org/grpc/balancer/endpointsharding_test.Test(...)
goroutine 23 [sync.Mutex.Lock]:
google.golang.org/grpc/balancer/endpointsharding.(*balancerWrapper).close(...)
google.golang.org/grpc/balancer/endpointsharding.(*endpointSharding).Close-range1(...)
google.golang.org/grpc/balancer/endpointsharding.(*endpointSharding).Close.(*EndpointMap[...]).All.func1(...)
google.golang.org/grpc/balancer/endpointsharding.(*endpointSharding).Close(...)
google.golang.org/grpc/balancer/endpointsharding_test.s.TestEndpointShardingExitIdleNotBlockedByOtherChild({{}}, 0xc000183500)
goroutine 24 [chan receive]:
google.golang.org/grpc/balancer/endpointsharding.stallIfSlow(...)
# test-file frames still on a stack at exit:
    balancer/endpointsharding/endpointsharding_ext_test.go:548 +0x9e
    balancer/endpointsharding/endpointsharding_ext_test.go:548 +0xf65
    balancer/endpointsharding/endpointsharding_ext_test.go:564 +0x127c
    balancer/endpointsharding/endpointsharding_ext_test.go:67 +0x35
```
After the progress assertion times out, the test's deferred cleanup calls `Close()` without establishing that the stalled update worker has settled; `Close` blocks on the per-child mutex (`balancerWrapper.close` / `endpointSharding.Close` in state `sync.Mutex.Lock`) held by that worker until the test binary's timeout.

### 494f1fb1

```console
$ ES_STALL=1 ES_STALL_MS=3 ES_DROP_EXITIDLE=1 go test -race -count=1 -timeout 150s -v -run '^Test$/^ChildExitIdleDuringOtherChildUpdate$' ./balancer/endpointsharding/
# S2 ^Test$/^ChildExitIdleDuringOtherChildUpdate$ rc=1 dur=150s timedout=1 leak=0 stallfired=1
    endpointsharding_concurrency_test.go:174: Timed out waiting for child operation
panic: test timed out after 2m30s
goroutine 9 [chan receive]:
google.golang.org/grpc/balancer/endpointsharding_test.Test(...)
goroutine 10 [sync.Mutex.Lock]:
google.golang.org/grpc/balancer/endpointsharding.(*balancerWrapper).close(...)
google.golang.org/grpc/balancer/endpointsharding.(*endpointSharding).Close-range1(...)
google.golang.org/grpc/balancer/endpointsharding.(*endpointSharding).Close.(*EndpointMap[...]).All.func1(...)
google.golang.org/grpc/balancer/endpointsharding.(*endpointSharding).Close(...)
google.golang.org/grpc/balancer/endpointsharding_test.awaitValue[...](...)
google.golang.org/grpc/balancer/endpointsharding_test.s.TestChildExitIdleDuringOtherChildUpdate({{}}, 0xc0000bec40)
goroutine 11 [chan receive]:
# test-file frames still on a stack at exit:
    balancer/endpointsharding/endpointsharding_concurrency_test.go:103 +0x125
    balancer/endpointsharding/endpointsharding_concurrency_test.go:171 +0x9e
    balancer/endpointsharding/endpointsharding_concurrency_test.go:171 +0xec5
    balancer/endpointsharding/endpointsharding_concurrency_test.go:174 +0xf0e
    balancer/endpointsharding/endpointsharding_ext_test.go:65 +0x35
```
After the progress assertion times out, the test's deferred cleanup calls `Close()` without establishing that the stalled update worker has settled; `Close` blocks on the per-child mutex (`balancerWrapper.close` / `endpointSharding.Close` in state `sync.Mutex.Lock`) held by that worker until the test binary's timeout.

### f092f60c

```console
$ ES_STALL=1 ES_STALL_MS=3 ES_DROP_EXITIDLE=1 go test -race -count=1 -timeout 150s -v -run '^Test$/^IndependentExitIdle$' ./balancer/endpointsharding/
# S2 ^Test$/^IndependentExitIdle$ rc=1 dur=31s timedout=0 leak=3 stallfired=2
    concurrency_test.go:153: Timed out waiting for the unrelated child to exit idle
    concurrency_test.go:142: Timed out waiting for the resolver update before cleanup
    grpctest.go:45: Leaked goroutine: goroutine 12 [chan receive]:
    grpctest.go:45: Leaked goroutine: goroutine 20 [chan receive]:
# test-file frames still on a stack at exit:
    balancer/endpointsharding/concurrency_test.go:134 +0x110b
    balancer/endpointsharding/concurrency_test.go:136 +0x13d
```
The run terminated (31s) without an indefinite wait. Source (concurrency_test.go:140-144): the deferred cleanup is `release(); waitForEvent(ctx, t, updateDone, "the resolver update before cleanup"); b.Close()`. `release` is a `sync.OnceFunc` closing the fixture channel, the join is bounded by `ctx`, and when the join times out `t.Fatalf` inside the deferred function calls runtime.Goexit, so `b.Close()` is skipped. The leaked goroutines are only the injected stalled production worker (concurrency_test.go:136 -> stallIfSlow). The same pattern is used by TestSynchronousIdleReport (lines 229-233) and TestChildCallsSerialized (lines 317-322). A second experiment stalls the idle-exit worker itself while it holds the child lock (`ES_STALL_EXITIDLE=1`):
```sh
verify/repro/c2_stalled_worker.sh f092f60c '^Test$/^IndependentExitIdle$' ES_STALL_EXITIDLE=1   # 3 runs
```
```console
duration=33s log=<log>
AUDIT: stalling idle-exit worker inside child ExitIdle
    concurrency_test.go:153: Timed out waiting for the unrelated child to exit idle
    concurrency_test.go:142: Timed out waiting for the resolver update before cleanup
AUDIT: stalling idle-exit worker inside child ExitIdle
    concurrency_test.go:153: Timed out waiting for the unrelated child to exit idle
    concurrency_test.go:142: Timed out waiting for the resolver update before cleanup
    grpctest.go:45: Leaked goroutine: goroutine 12 [sync.Mutex.Lock]:
    grpctest.go:45: Leaked goroutine: goroutine 13 [chan receive]:
--- goroutines with test frames at exit:
```
All 3 runs ended in 32-33s: the bounded join failed and Close was skipped. Other S1 runs (update stall only) on TestSynchronousIdleReport and TestChildCallsSerialized also terminated in 20-21s (`concurrency_test.go:231: Timed out waiting for the initial update before cleanup`, `concurrency_test.go:319: Timed out waiting for the child call before cleanup`).

### 2910a0bd

```console
$ ES_STALL=1 ES_STALL_MS=3 ES_DROP_EXITIDLE=1 go test -race -count=1 -timeout 150s -v -run '^Test$/^ChildExitIdleDuringUpdate$' ./balancer/endpointsharding/
# S2 ^Test$/^ChildExitIdleDuringUpdate$ rc=1 dur=151s timedout=1 leak=0 stallfired=1
    concurrency_test.go:176: unrelated child's idle exit blocked behind a configuration update
panic: test timed out after 2m30s
goroutine 9 [chan receive]:
google.golang.org/grpc/balancer/endpointsharding_test.Test(...)
goroutine 10 [sync.Mutex.Lock]:
google.golang.org/grpc/balancer/endpointsharding.(*balancerWrapper).close(...)
google.golang.org/grpc/balancer/endpointsharding.(*endpointSharding).Close-range1(...)
google.golang.org/grpc/balancer/endpointsharding.(*endpointSharding).Close.(*EndpointMap[...]).All.func1(...)
google.golang.org/grpc/balancer/endpointsharding.(*endpointSharding).Close(...)
google.golang.org/grpc/balancer/endpointsharding_test.s.TestChildExitIdleDuringUpdate({{}}, 0xc0000bec40)
goroutine 11 [chan receive]:
google.golang.org/grpc/balancer/endpointsharding.stallIfSlow(...)
# test-file frames still on a stack at exit:
    balancer/endpointsharding/concurrency_test.go:161 +0x9e
    balancer/endpointsharding/concurrency_test.go:161 +0xe65
    balancer/endpointsharding/concurrency_test.go:176 +0x11b1
    balancer/endpointsharding/endpointsharding_ext_test.go:65 +0x35
```
After the progress assertion times out, the test's deferred cleanup calls `Close()` without establishing that the stalled update worker has settled; `Close` blocks on the per-child mutex (`balancerWrapper.close` / `endpointSharding.Close` in state `sync.Mutex.Lock`) held by that worker until the test binary's timeout.

### a2a8173a

```console
$ ES_STALL=1 ES_STALL_MS=3 ES_DROP_EXITIDLE=1 go test -race -count=1 -timeout 150s -v -run '^Test$/^ChildExitIdleDuringUpdate$' ./balancer/endpointsharding/
# S2 ^Test$/^ChildExitIdleDuringUpdate$ rc=1 dur=150s timedout=1 leak=0 stallfired=1
    concurrency_test.go:160: Timed out waiting for independent child to exit idle while the update is blocked
    concurrency_test.go:150: Timed out waiting for resolver update to finish
panic: test timed out after 2m30s
goroutine 9 [chan receive]:
google.golang.org/grpc/balancer/endpointsharding_test.Test(...)
goroutine 10 [sync.Mutex.Lock]:
google.golang.org/grpc/balancer/endpointsharding.(*balancerWrapper).close(...)
google.golang.org/grpc/balancer/endpointsharding.(*endpointSharding).Close-range1(...)
google.golang.org/grpc/balancer/endpointsharding.(*endpointSharding).Close.(*EndpointMap[...]).All.func1(...)
google.golang.org/grpc/balancer/endpointsharding.(*endpointSharding).Close(...)
google.golang.org/grpc/balancer/endpointsharding_test.waitForSignal(...)
google.golang.org/grpc/balancer/endpointsharding_test.s.TestChildExitIdleDuringUpdate.func5()
google.golang.org/grpc/balancer/endpointsharding_test.waitForSignal(...)
# test-file frames still on a stack at exit:
    balancer/endpointsharding/concurrency_test.go:140 +0xceb
    balancer/endpointsharding/concurrency_test.go:142 +0x13d
    balancer/endpointsharding/concurrency_test.go:150 +0xba
    balancer/endpointsharding/concurrency_test.go:160 +0x137a
    balancer/endpointsharding/concurrency_test.go:70 +0x150
    balancer/endpointsharding/endpointsharding_ext_test.go:66 +0x35
```
After the progress assertion times out, the test's deferred cleanup calls `Close()` without establishing that the stalled update worker has settled; `Close` blocks on the per-child mutex (`balancerWrapper.close` / `endpointSharding.Close` in state `sync.Mutex.Lock`) held by that worker until the test binary's timeout.
Separately, with only `ES_DROP_EXITIDLE=1` (no injected stall), `TestChildCallsSerialized` on this branch ran into the 40s test timeout (`go test -race -count=1 -timeout 40s -v -run '^Test$/^ChildCallsSerialized$'`: `concurrency_test.go:344: Timed out waiting for child ExitIdle` followed by `panic: test timed out after 40s`).

### d829eac9

```console
$ ES_STALL=1 ES_STALL_MS=3 ES_DROP_EXITIDLE=1 go test -race -count=1 -timeout 150s -v -run '^Test$/^ChildExitIdleDuringOtherChildUpdate$' ./balancer/endpointsharding/
# S2 ^Test$/^ChildExitIdleDuringOtherChildUpdate$ rc=1 dur=150s timedout=1 leak=0 stallfired=1
    endpointsharding_concurrency_test.go:149: Idle exit waited for an unrelated child's update
panic: test timed out after 2m30s
goroutine 22 [chan receive]:
google.golang.org/grpc/balancer/endpointsharding_test.Test(...)
goroutine 23 [chan receive]:
google.golang.org/grpc/balancer/endpointsharding_test.s.TestChildExitIdleDuringOtherChildUpdate({{}}, 0xc000103500)
goroutine 24 [sync.Mutex.Lock]:
google.golang.org/grpc/balancer/endpointsharding.(*balancerWrapper).close(...)
google.golang.org/grpc/balancer/endpointsharding.(*endpointSharding).Close-range1(...)
google.golang.org/grpc/balancer/endpointsharding.(*endpointSharding).Close.(*EndpointMap[...]).All.func1(...)
google.golang.org/grpc/balancer/endpointsharding.(*endpointSharding).Close(...)
google.golang.org/grpc/balancer/endpointsharding_test.s.TestChildExitIdleDuringOtherChildUpdate.func1(...)
# test-file frames still on a stack at exit:
    balancer/endpointsharding/endpointsharding_concurrency_test.go:128 +0x9e
    balancer/endpointsharding/endpointsharding_concurrency_test.go:128 +0xa85
    balancer/endpointsharding/endpointsharding_concurrency_test.go:149 +0xf65
    balancer/endpointsharding/endpointsharding_concurrency_test.go:77 +0xec
    balancer/endpointsharding/endpointsharding_ext_test.go:65 +0x35
```
After the progress assertion times out, the test's deferred cleanup calls `Close()` without establishing that the stalled update worker has settled; `Close` blocks on the per-child mutex (`balancerWrapper.close` / `endpointSharding.Close` in state `sync.Mutex.Lock`) held by that worker until the test binary's timeout.

### 39f0f15b

```console
$ ES_STALL=1 ES_STALL_MS=3 ES_DROP_EXITIDLE=1 go test -race -count=1 -timeout 150s -v -run '^Test$/^ExitIdleDuringOtherChildUpdate$' ./balancer/endpointsharding/
# S2 ^Test$/^ExitIdleDuringOtherChildUpdate$ rc=1 dur=151s timedout=1 leak=0 stallfired=1
    concurrency_test.go:155: Timed out waiting for child balancer call
panic: test timed out after 2m30s
goroutine 9 [chan receive]:
google.golang.org/grpc/balancer/endpointsharding_test.Test(...)
goroutine 10 [sync.Mutex.Lock]:
google.golang.org/grpc/balancer/endpointsharding.(*balancerWrapper).close(...)
google.golang.org/grpc/balancer/endpointsharding.(*endpointSharding).Close-range1(...)
google.golang.org/grpc/balancer/endpointsharding.(*endpointSharding).Close.(*EndpointMap[...]).All.func1(...)
google.golang.org/grpc/balancer/endpointsharding.(*endpointSharding).Close(...)
google.golang.org/grpc/balancer/endpointsharding_test.awaitSignal(...)
google.golang.org/grpc/balancer/endpointsharding_test.s.TestExitIdleDuringOtherChildUpdate({{}}, 0xc0000bec40)
goroutine 11 [chan receive]:
# test-file frames still on a stack at exit:
    balancer/endpointsharding/concurrency_test.go:152 +0x1625
    balancer/endpointsharding/concurrency_test.go:152 +0x9e
    balancer/endpointsharding/concurrency_test.go:155 +0x1687
    balancer/endpointsharding/concurrency_test.go:72 +0x10e
    balancer/endpointsharding/endpointsharding_ext_test.go:65 +0x35
```
After the progress assertion times out, the test's deferred cleanup calls `Close()` without establishing that the stalled update worker has settled; `Close` blocks on the per-child mutex (`balancerWrapper.close` / `endpointSharding.Close` in state `sync.Mutex.Lock`) held by that worker until the test binary's timeout.
Separately, with only `ES_DROP_EXITIDLE=1` (no injected stall), `TestChildCallsSerialized` on this branch ran into the 40s test timeout (`go test -race -count=1 -timeout 40s -v -run '^Test$/^ChildCallsSerialized$'`: `concurrency_test.go:286: Timed out waiting for child balancer call` followed by `panic: test timed out after 40s`).

### 2c1919e2

```console
$ ES_STALL=1 ES_STALL_MS=3 ES_DROP_EXITIDLE=1 go test -race -count=1 -timeout 150s -v -run '^Test$/^ChildExitIdleDuringOtherChildUpdate$' ./balancer/endpointsharding/
# S2 ^Test$/^ChildExitIdleDuringOtherChildUpdate$ rc=1 dur=150s timedout=1 leak=0 stallfired=1
    endpointsharding_concurrency_test.go:147: Timed out waiting for unrelated child to exit idle while update is blocked
panic: test timed out after 2m30s
goroutine 22 [chan receive]:
google.golang.org/grpc/balancer/endpointsharding_test.Test(...)
goroutine 23 [sync.Mutex.Lock]:
google.golang.org/grpc/balancer/endpointsharding.(*balancerWrapper).close(...)
google.golang.org/grpc/balancer/endpointsharding.(*endpointSharding).Close-range1(...)
google.golang.org/grpc/balancer/endpointsharding.(*endpointSharding).Close.(*EndpointMap[...]).All.func1(...)
google.golang.org/grpc/balancer/endpointsharding.(*endpointSharding).Close(...)
google.golang.org/grpc/balancer/endpointsharding_test.waitForSignal(...)
google.golang.org/grpc/balancer/endpointsharding_test.s.TestChildExitIdleDuringOtherChildUpdate({{}}, 0xc000183500)
goroutine 24 [chan receive]:
# test-file frames still on a stack at exit:
    balancer/endpointsharding/endpointsharding_concurrency_test.go:139 +0x11cb
    balancer/endpointsharding/endpointsharding_concurrency_test.go:141 +0x13d
    balancer/endpointsharding/endpointsharding_concurrency_test.go:147 +0x127c
    balancer/endpointsharding/endpointsharding_concurrency_test.go:66 +0x150
    balancer/endpointsharding/endpointsharding_ext_test.go:66 +0x35
```
After the progress assertion times out, the test's deferred cleanup calls `Close()` without establishing that the stalled update worker has settled; `Close` blocks on the per-child mutex (`balancerWrapper.close` / `endpointSharding.Close` in state `sync.Mutex.Lock`) held by that worker until the test binary's timeout.

### a60b2fb7

```console
$ ES_STALL=1 ES_STALL_MS=3 ES_DROP_EXITIDLE=1 go test -race -count=1 -timeout 150s -v -run '^Test$/^EndpointShardingIndependentExitIdle$' ./balancer/endpointsharding/
# S2 ^Test$/^EndpointShardingIndependentExitIdle$ rc=1 dur=150s timedout=1 leak=0 stallfired=1
# S2 ^Test$/^EndpointShardingIndependentExitIdle$ rc=1 dur=151s timedout=1 leak=0 stallfired=1
    concurrency_test.go:154: Timed out waiting for independent child to exit idle
    concurrency_test.go:143: Timed out waiting for configuration update to finish
panic: test timed out after 2m30s
goroutine 38 [chan receive]:
google.golang.org/grpc/balancer/endpointsharding_test.Test(...)
goroutine 39 [chan receive]:
google.golang.org/grpc/balancer/endpointsharding_test.s.TestEndpointShardingIndependentExitIdle({{}}, 0xc000181180)
goroutine 40 [sync.Mutex.Lock]:
google.golang.org/grpc/balancer/endpointsharding.(*balancerWrapper).close(...)
google.golang.org/grpc/balancer/endpointsharding.(*endpointSharding).Close-range1(...)
google.golang.org/grpc/balancer/endpointsharding.(*endpointSharding).Close.(*EndpointMap[...]).All.func1(...)
google.golang.org/grpc/balancer/endpointsharding.(*endpointSharding).Close(...)
google.golang.org/grpc/balancer/endpointsharding_test.waitForSignal(...)
# test-file frames still on a stack at exit:
    balancer/endpointsharding/concurrency_test.go:135 +0x13eb
    balancer/endpointsharding/concurrency_test.go:137 +0x13d
    balancer/endpointsharding/concurrency_test.go:143 +0x75
    balancer/endpointsharding/concurrency_test.go:154 +0x1665
    balancer/endpointsharding/concurrency_test.go:66 +0x150
    balancer/endpointsharding/concurrency_test.go:75 +0x11e
```
After the progress assertion times out, the test's deferred cleanup calls `Close()` without establishing that the stalled update worker has settled; `Close` blocks on the per-child mutex (`balancerWrapper.close` / `endpointSharding.Close` in state `sync.Mutex.Lock`) held by that worker until the test binary's timeout.

### 7aa1c803

```console
$ ES_STALL=1 ES_STALL_MS=3 ES_DROP_EXITIDLE=1 go test -race -count=1 -timeout 150s -v -run '^Test$/^ChildExitIdleDuringOtherChildUpdate$' ./balancer/endpointsharding/
# S2 ^Test$/^ChildExitIdleDuringOtherChildUpdate$ rc=1 dur=150s timedout=1 leak=0 stallfired=1
    endpointsharding_concurrency_test.go:129: Timed out waiting for balancer callback
panic: test timed out after 2m30s
goroutine 9 [chan receive]:
google.golang.org/grpc/balancer/endpointsharding_test.Test(...)
goroutine 10 [chan receive]:
google.golang.org/grpc/balancer/endpointsharding_test.s.TestChildExitIdleDuringOtherChildUpdate({{}}, 0xc0000bec40)
goroutine 11 [sync.Mutex.Lock]:
google.golang.org/grpc/balancer/endpointsharding.(*balancerWrapper).close(...)
google.golang.org/grpc/balancer/endpointsharding.(*endpointSharding).Close-range1(...)
google.golang.org/grpc/balancer/endpointsharding.(*endpointSharding).Close.(*EndpointMap[...]).All.func1(...)
google.golang.org/grpc/balancer/endpointsharding.(*endpointSharding).Close(...)
google.golang.org/grpc/balancer/endpointsharding_test.receive[...](...)
# test-file frames still on a stack at exit:
    balancer/endpointsharding/endpointsharding_concurrency_test.go:122 +0x9e
    balancer/endpointsharding/endpointsharding_concurrency_test.go:122 +0xa05
    balancer/endpointsharding/endpointsharding_concurrency_test.go:129 +0xbae
    balancer/endpointsharding/endpointsharding_concurrency_test.go:66 +0x145
    balancer/endpointsharding/endpointsharding_concurrency_test.go:81 +0xde
    balancer/endpointsharding/endpointsharding_ext_test.go:65 +0x35
```
After the progress assertion times out, the test's deferred cleanup calls `Close()` without establishing that the stalled update worker has settled; `Close` blocks on the per-child mutex (`balancerWrapper.close` / `endpointSharding.Close` in state `sync.Mutex.Lock`) held by that worker until the test binary's timeout.

## C3

Method: TryLock probes at the lock sites named by changed comments (verify/repro/c3-<suffix>-instr.diff), run with the branch's own tests.

### 7d4c90b4

Changed comment on `balancerWrapper.mu`: "It must not be held while holding es.mu." The probe in `balancerWrapper.UpdateState` checks, right after `bw.es.mu.Lock()`, whether `bw.mu` is held.
```sh
verify/repro/c3_lock_trace.sh 7d4c90b4
```
```console
354-	// child is being built so that no call into the child can be attempted
355:	// before it is fully initialized. It must not be held while holding
356-	// es.mu.
357-	mu sync.Mutex
358-	// child contains the wrapped balancer. Access its methods only through
359-	// methods on balancerWrapper to ensure proper synchronization. Guarded by
360-	// mu.
361-	child    balancer.Balancer
--- PASS: Test (0.00s)
AUDIT: UpdateState: es.mu acquired while bw.mu HELD (simultaneous ownership); caller stack:
    google.golang.org/grpc/balancer/endpointsharding_test.s.TestEndpointShardingSynchronousChildUpdates.func1
    google.golang.org/grpc/balancer/endpointsharding.(*balancerWrapper).build
    google.golang.org/grpc/balancer/endpointsharding.(*endpointSharding).UpdateClientConnState
    google.golang.org/grpc/balancer/endpointsharding_test.s.TestEndpointShardingSynchronousChildUpdates
AUDIT: UpdateState: es.mu acquired while bw.mu HELD (simultaneous ownership); caller stack:
    google.golang.org/grpc/balancer/endpointsharding_test.s.TestEndpointShardingSynchronousChildUpdates.func2
    google.golang.org/grpc/balancer/endpointsharding.(*balancerWrapper).updateClientConnState
    google.golang.org/grpc/balancer/endpointsharding.(*endpointSharding).UpdateClientConnState
    google.golang.org/grpc/balancer/endpointsharding_test.s.TestEndpointShardingSynchronousChildUpdates
AUDIT: UpdateState: es.mu acquired while bw.mu HELD (simultaneous ownership); caller stack:
    google.golang.org/grpc/balancer/endpointsharding_test.s.TestEndpointShardingSynchronousChildUpdates.func1
    google.golang.org/grpc/balancer/endpointsharding.(*balancerWrapper).build
    google.golang.org/grpc/balancer/endpointsharding.(*endpointSharding).UpdateClientConnState
    google.golang.org/grpc/balancer/endpointsharding_test.s.TestEndpointShardingSynchronousChildUpdates
AUDIT: UpdateState: es.mu acquired while bw.mu HELD (simultaneous ownership); caller stack:
    google.golang.org/grpc/balancer/endpointsharding_test.s.TestEndpointShardingSynchronousChildUpdates.func2
    google.golang.org/grpc/balancer/endpointsharding.(*balancerWrapper).updateClientConnState
    google.golang.org/grpc/balancer/endpointsharding.(*endpointSharding).UpdateClientConnState
    google.golang.org/grpc/balancer/endpointsharding_test.s.TestEndpointShardingSynchronousChildUpdates
--- PASS: Test (0.00s)
    --- PASS: Test/EndpointShardingSynchronousChildUpdates (0.00s)
ok  	google.golang.org/grpc/balancer/endpointsharding	1.015s
```
Both locks are held at the same time on every synchronous child callback during `build` and `updateClientConnState`: `bw.mu` is held while `es.mu` is acquired and held. The comment says `mu` must not be held while `es.mu` is held, which rules out holding both at once, not only parent-then-child acquisition order. (The branch's other changed comment, "do not acquire childrenMu or a child's mu while holding mu", is an acquisition-order rule and is respected.)

### 2642de3c

Changed comment on `endpointSharding.childMu`: "Calls into an individual child are additionally serialized by that child's balancerWrapper.mu, which must be acquired while holding childMu (never the other way around)." The probe in `balancerWrapper.exitIdle` (the asynchronous worker started by `ExitIdle() { go bw.exitIdle() }`) checks whether `es.childMu` is held after it takes `bw.mu`.
```sh
verify/repro/c3_lock_trace.sh 2642de3c
```
```console
108-	// swapping of the children map. Calls into an individual child are
109-	// additionally serialized by that child's balancerWrapper.mu, which must
110:	// be acquired while holding childMu (never the other way around). To avoid
111-	// deadlocks, do not acquire childMu or balancerWrapper.mu while holding mu.
112-	childMu  sync.Mutex
113-	children atomic.Pointer[resolver.EndpointMap[*balancerWrapper]]
114-
--- PASS: Test (0.00s)
AUDIT: exitIdle worker acquired bw.mu WITHOUT holding es.childMu (childMu held by another goroutine)
--- PASS: Test (0.00s)
    --- PASS: Test/EndpointShardingExitIdleNotBlockedByOtherChild (0.00s)
ok  	google.golang.org/grpc/balancer/endpointsharding	1.030s
```
The idle-exit worker takes `bw.mu` without holding `childMu` while another goroutine (the blocked parent UpdateClientConnState) holds `childMu`. That is the asynchronous dispatch the test depends on, and it contradicts "must be acquired while holding childMu".

## C4

Target: audited implementation 81201fc085e37de0ffb23505ac7831350b326c33 (balancer/endpointsharding/endpointsharding_test.go). Instrumentation (verify/repro/c4-instr.diff + c4_zz_audit.go), env-gated: `ES_SETUP_DEADLOCK=1` makes the child update take `es.parent.mu` while the child's synchronous `UpdateState` also needs it, so setup stalls; `ES_EXITIDLE_STALL_BEFORE/AFTER=1` stalls the asynchronous idle-exit worker while it holds `childMu`, before or after the child's ExitIdle.
```sh
verify/repro/c4_test_hangs.sh
```
```console
ok  	google.golang.org/grpc/balancer/endpointsharding	1.177s
== ES_SETUP_DEADLOCK SameChildMutualExclusion rc=1
panic: test timed out after 30s
	balancer/endpointsharding/endpointsharding_test.go:41 +0x35
	balancer/endpointsharding/endpointsharding.go:356 +0x65
	balancer/endpointsharding/endpointsharding_test.go:131 +0x1ac
	balancer/endpointsharding/endpointsharding.go:380 +0x1da
	balancer/endpointsharding/endpointsharding.go:179 +0xac5
	balancer/endpointsharding/endpointsharding_test.go:373 +0x6a5
== ES_SETUP_DEADLOCK DecoupledChildProgress rc=1
panic: test timed out after 30s
	balancer/endpointsharding/endpointsharding_test.go:41 +0x35
	balancer/endpointsharding/endpointsharding.go:356 +0x65
	balancer/endpointsharding/endpointsharding_test.go:131 +0x1ac
	balancer/endpointsharding/endpointsharding.go:380 +0x1da
	balancer/endpointsharding/endpointsharding.go:179 +0xac5
	balancer/endpointsharding/endpointsharding_test.go:262 +0x8c5
== ES_EXITIDLE_STALL_AFTER SameChildMutualExclusion rc=1
AUDIT: ES_EXITIDLE_STALL_AFTER: stalling ExitIdle worker while holding childMu
panic: test timed out after 30s
	balancer/endpointsharding/endpointsharding_test.go:41 +0x35
	balancer/endpointsharding/endpointsharding.go:390 +0x3a
	balancer/endpointsharding/endpointsharding.go:228
	balancer/endpointsharding/endpointsharding.go:227 +0x18f
	balancer/endpointsharding/endpointsharding_test.go:417 +0x5f
	balancer/endpointsharding/endpointsharding_test.go:473 +0x133c
== ES_EXITIDLE_STALL_BEFORE SameChildMutualExclusion rc=1
AUDIT: ES_EXITIDLE_STALL_BEFORE: stalling ExitIdle worker while holding childMu
    endpointsharding_test.go:464: ExitIdle never finished after update released
panic: test timed out after 30s
	balancer/endpointsharding/endpointsharding_test.go:41 +0x35
	balancer/endpointsharding/endpointsharding.go:390 +0x3a
	balancer/endpointsharding/endpointsharding.go:228
	balancer/endpointsharding/endpointsharding.go:227 +0x18f
	balancer/endpointsharding/endpointsharding_test.go:417 +0x5f
== ES_EXITIDLE_STALL_BEFORE DecoupledChildProgress rc=1
AUDIT: ES_EXITIDLE_STALL_BEFORE: stalling ExitIdle worker while holding childMu
    endpointsharding_test.go:339: child 2 ExitIdle blocked while child 1 was updating (coarse locking defect)
panic: test timed out after 30s
	balancer/endpointsharding/endpointsharding_test.go:41 +0x35
	balancer/endpointsharding/endpointsharding.go:390 +0x3a
	balancer/endpointsharding/endpointsharding.go:228
	balancer/endpointsharding/endpointsharding.go:227 +0x18f
	balancer/endpointsharding/endpointsharding_test.go:294 +0x5f
```
Full stack for the last case (recorded run): the test goroutine is parked at endpointsharding.go:390 (`endpointState.close` -> `childMu.Lock`) called from `lb.Close()` at endpointsharding_test.go:294, reached from the `t.Fatal` at line 339.

- synchronous-setup: both tests block in the initial `lb.UpdateClientConnState` (endpointsharding_test.go:262 and :373). No local deadline applies, so the bounded assertions are never reached. Only the 30s binary timeout ends the run.
- unbounded-operation-join: `callDone` is closed right after `targetChildState.ExitIdle()` returns, and `ChildState.ExitIdle` is `func() { go epState.exitIdle() }` (endpointsharding.go:267), which returns without blocking. With the idle-exit worker stalled (`ES_EXITIDLE_STALL_AFTER`), the test got past `<-callDone` (line 467) and blocked later, in `lb.Close()` (line 417). This part did not hold.
- teardown-lock-dependency: `Close` -> `endpointState.close` locks `childMu` (endpointsharding.go:390), which the stalled idle-exit worker holds. Observed hang at line 417 (SameChild) and line 294 (Decoupled).
- idle-timeout-cleanup-trigger: `releaseAndWait()` joins only the update worker (`workerDone`). After the idle-exit timeouts (`:464 ExitIdle never finished after update released`, `:339 child 2 ExitIdle blocked ...`) it returns true, so `lb.Close()` runs and blocks on the unsettled idle-exit worker's `childMu`.

## C5

Target: evalon/grpc-go-en-5c408aa1 (7eee1984fbe6d65f689e170c281368e6427cc91f). scripts/vet.sh installs staticcheck from test/tools/go.mod and runs `staticcheck -checks 'all' ./...`. It then fails on any SA1019 line not matched by its `grep -Fv` allowlist (the block starting `noret_grep "(SA1019)" "${SC_OUT}" | not grep -Fv 'XXXXX PleaseIgnoreUnused`).
```sh
verify/repro/c5_staticcheck.sh
```
```console
staticcheck 2026.1 (v0.7.0)
	honnef.co/go/tools v0.7.0
allowlist entries: 46; entries mentioning ExitIdler: 0
SA1019 findings surviving the vet.sh allowlist:
balancer/ringhash/ringhash.go:271:20: balancer.ExitIdler is deprecated: All balancers must implement this interface. This interface will be removed in a future release.  (SA1019)
balancer/ringhash/ringhash.go:405:11: balancer.ExitIdler is deprecated: All balancers must implement this interface. This interface will be removed in a future release.  (SA1019)
```
Changed lines in ringhash.go: `var idleBalancer balancer.ExitIdler` (271) and the field `balancer balancer.ExitIdler` (405). The allowlist has no `ExitIdler` entry, so vet.sh would report both diagnostics.

## C6

Target: evalon/grpc-go-en-cc0c22d2 (4f5da4be44d62386d1cd166cbf362727bf4d55e4). Source (`endpointSharding.updateState`):
```go
func (es *endpointSharding) updateState() {
	if es.inhibitChildUpdates.Load() {   // eligibility check, no lock
		return
	}
	...
	es.mu.Lock()                         // acquired afterwards; eligibility not rechecked
	defer es.mu.Unlock()
	... aggregate all children, es.cc.UpdateState(...)
```
Instrumentation: verify/repro/c6-hook.diff inserts a nil-by-default hook `auditAfterInhibitCheck()` between the check and `es.mu.Lock()`. The probe (verify/repro/c6_audit_test.go, package-internal) pauses child A's callback there, starts a configuration batch in which child B reports TRANSIENT_FAILURE before A is reconfigured, holds the batch after B, then resumes A's callback.
```sh
verify/repro/c6_publication_race.sh
```
```console
    audit_c6_test.go:96: initial update: 1 publication(s), last: agg=READY children=map[a:READY b:READY]
    audit_c6_test.go:110: child A's callback passed the inhibit check (inhibit=false) and is paused before es.mu
    audit_c6_test.go:122: batch in progress (inhibit=true): b processed, a not yet; publications so far: 0
    audit_c6_test.go:127: publication 1: duringBatch=true agg=CONNECTING children=map[a:CONNECTING b:TRANSIENT_FAILURE]
    audit_c6_test.go:129: parent published a partially updated aggregate during the configuration batch: agg=CONNECTING children=map[a:CONNECTING b:TRANSIENT_FAILURE]
    audit_c6_test.go:127: publication 2: duringBatch=false agg=TRANSIENT_FAILURE children=map[a:TRANSIENT_FAILURE b:TRANSIENT_FAILURE]
--- FAIL: TestAuditC6PublicationDuringBatch (0.05s)
    audit_c6_test.go:96: initial update: 1 publication(s), last: agg=READY children=map[a:READY b:READY]
    audit_c6_test.go:110: child A's callback passed the inhibit check (inhibit=false) and is paused before es.mu
    audit_c6_test.go:122: batch in progress (inhibit=true): b processed, a not yet; publications so far: 0
    audit_c6_test.go:127: publication 1: duringBatch=true agg=CONNECTING children=map[a:CONNECTING b:TRANSIENT_FAILURE]
    audit_c6_test.go:129: parent published a partially updated aggregate during the configuration batch: agg=CONNECTING children=map[a:CONNECTING b:TRANSIENT_FAILURE]
    audit_c6_test.go:127: publication 2: duringBatch=false agg=TRANSIENT_FAILURE children=map[a:TRANSIENT_FAILURE b:TRANSIENT_FAILURE]
--- FAIL: TestAuditC6PublicationDuringBatch (0.05s)
    audit_c6_test.go:96: initial update: 1 publication(s), last: agg=READY children=map[a:READY b:READY]
    audit_c6_test.go:110: child A's callback passed the inhibit check (inhibit=false) and is paused before es.mu
    audit_c6_test.go:122: batch in progress (inhibit=true): b processed, a not yet; publications so far: 0
    audit_c6_test.go:127: publication 1: duringBatch=true agg=CONNECTING children=map[a:CONNECTING b:TRANSIENT_FAILURE]
    audit_c6_test.go:129: parent published a partially updated aggregate during the configuration batch: agg=CONNECTING children=map[a:CONNECTING b:TRANSIENT_FAILURE]
    audit_c6_test.go:127: publication 2: duringBatch=false agg=TRANSIENT_FAILURE children=map[a:TRANSIENT_FAILURE b:TRANSIENT_FAILURE]
--- FAIL: TestAuditC6PublicationDuringBatch (0.05s)
FAIL
FAIL	google.golang.org/grpc/balancer/endpointsharding	0.167s
FAIL
```
All 3 runs: the parent published `CONNECTING` built from a half-applied batch (B new, A stale) while `inhibitChildUpdates` was true, then published the consolidated state. TestEval_BatchUpdateInhibition passes on this branch because it never pauses a callback at this boundary.

## C7

Target: maintained tests on the audited implementation 81201fc085e37de0ffb23505ac7831350b326c33. Each part was checked by applying a targeted violation to endpointsharding.go and running the maintained suite (diffs in verify/repro/c7-*.diff):
```diff
# samechild: exitIdle no longer takes childMu
-	es.childMu.Lock()
 	if !es.closed {
 		es.childLB.ExitIdle()
 	}
-	es.childMu.Unlock()
# stalehandle: closed guard removed
-	if !es.closed {
+	{
# configerror: stop processing remaining endpoints after a child error
+		if err != nil {
+			retErr = err
+			break
+		}
```
```sh
verify/repro/c7_mutations.sh
```
```console
== mutation samechild
    endpointsharding_test.go:450: ExitIdle completed concurrently while UpdateClientConnState was in flight on same child
FAIL	google.golang.org/grpc/balancer/endpointsharding	0.024s
== mutation stalehandle
    endpointsharding_test.go:720: exitIdle entered closed child after endpoint was removed
    --- FAIL: Test/ClosedStateGuardCoverage (0.00s)
FAIL	google.golang.org/grpc/balancer/endpointsharding	0.245s
== mutation configerror
    endpointsharding_test.go:779: expected 3 child states in consolidated picker despite child error, got 2
    --- FAIL: Test/BatchUpdateChildErrorConsolidation (0.00s)
FAIL	google.golang.org/grpc/balancer/endpointsharding	0.248s
```
- same-child-contention: TestSameChildMutualExclusion holds a second UpdateClientConnState inside the child, calls the published `ChildState.ExitIdle`, and asserts no ExitIdle completes before release (line 450) and `maxSeen <= 1`. It caught the violation.
- stale-child-handles: TestClosedStateGuardCoverage keeps the removed child's wrapper, calls `exitIdle()` after removal and after Close, and asserts the child is not entered (lines 720/733). It caught the violation.
- configuration-error-retention: TestBatchUpdateChildErrorConsolidation makes child 2 return an error and asserts the returned error and 3 children in the consolidated picker (line 779). It caught the violation.

## C8

Target: evalon/grpc-go-en-2642de3c (425622d6867709513d38576878df13d29e0ee4cd), authored test `TestEndpointShardingExitIdleNotBlockedByOtherChild`. It was ported to the original coarse-locking base bf9e7cd3 with a one-line test-side API adaptation. The only difference from the branch's test diff:
```diff
-	childA.ExitIdle()
+	childA.Balancer.ExitIdle()
```
Cleanup order in the test: `defer es.Close()` is registered before the fixture. `close(unblock)` is only reached after the progress select, so the `t.Fatal` at the progress timeout (line 461) runs deferred `es.Close()` with `unblock` still open.
```sh
verify/repro/c8_coarse_lock.sh   # go test ... -timeout 45s under an external `timeout 120` watchdog
```
```console
rc=1 duration=48s
    endpointsharding_ext_test.go:461: Timed out waiting for ExitIdle on child A while child B's update is blocked
panic: test timed out after 45s
goroutine 9 [chan receive]:
google.golang.org/grpc/balancer/endpointsharding_test.Test(0xc0000bea80)
	balancer/endpointsharding/endpointsharding_ext_test.go:67 +0x35
goroutine 10 [sync.Mutex.Lock]:
	/usr/local/go/src/internal/sync/mutex.go:149 +0x210
	/usr/local/go/src/internal/sync/mutex.go:70 +0x55
	/usr/local/go/src/sync/mutex.go:46 +0x29
google.golang.org/grpc/balancer/endpointsharding.(*endpointSharding).Close(0xc00023a3c0)
	balancer/endpointsharding/endpointsharding.go:224 +0x4f
google.golang.org/grpc/balancer/endpointsharding_test.s.TestEndpointShardingExitIdleNotBlockedByOtherChild({{}}, 0xc0000bec40)
	balancer/endpointsharding/endpointsharding_ext_test.go:461 +0xf65
goroutine 11 [chan receive]:
google.golang.org/grpc/balancer/endpointsharding_test.(*blockingChild).UpdateClientConnState(0xc0001f7170, {{{0x0, 0x0, 0x0}, {0xc000093220, 0x1, 0x1}, 0x0, 0x0}, {0x0, ...}})
	balancer/endpointsharding/endpointsharding_ext_test.go:377 +0x165
google.golang.org/grpc/balancer/endpointsharding_test.(*lazyChild).UpdateClientConnState(0xc000093160, {{{0x0, 0x0, 0x0}, {0xc000093220, 0x1, 0x1}, 0x0, 0x0}, {0x0, ...}})
	balancer/endpointsharding/endpointsharding_ext_test.go:493 +0x2f9
google.golang.org/grpc/balancer/endpointsharding.(*balancerWrapper).updateClientConnStateLocked(...)
	balancer/endpointsharding/endpointsharding.go:376
google.golang.org/grpc/balancer/endpointsharding.(*endpointSharding).UpdateClientConnState(0xc00023a3c0, {{{0x0, 0x0, 0x0}, {0xc000053a40, 0x2, 0x2}, 0x0, 0x0}, {0x0, ...}})
	balancer/endpointsharding/endpointsharding.go:175 +0xbc2
```
The progress assertion timed out after 10s. Deferred `es.Close()` then blocked on `childMu` (endpointsharding.go:224), held by UpdateClientConnState, whose child B is parked in the fixture at endpointsharding_ext_test.go:377 (`<-b.unblock`). The run ended only at the 45s test timeout.

## C9

Target: evalon/grpc-go-en-866a4ecf (d220addf372c30ddf7bba209c1cc9a0eb9cf2ff3). `awaitExitIdle` (endpointsharding_sync_ext_test.go) caches any non-matching notification in `pendingExitIdle[got]++` and keeps waiting. The final negative assertion of `TestEndpointShardingExitIdleAfterChildRemoved` only does `select { case got := <-ct.exitIdleCh: t.Fatalf("Received unexpected ExitIdle on child %q", got) ... }` (line 593) and never reads `pendingExitIdle`.
Injection (verify/repro/c9-deliver-closed.diff): when the removed child B is asked to exit idle, deliver ExitIdle to B anyway: mode 1 synchronously, before A's expected delivery; mode 2 3ms later, after A's.
```sh
verify/repro/c9_cached_notification.sh
```
```console
== ES_DELIVER_CLOSED=1
     20     --- PASS: Test/EndpointShardingExitIdleAfterChildRemoved 
     20 AUDIT: delivering ExitIdle to CLOSED child (before A)
== ES_DELIVER_CLOSED=2
     20     --- FAIL: Test/EndpointShardingExitIdleAfterChildRemoved 
     20     endpointsharding_sync_ext_test.go:593: Received unexpected ExitIdle on child "addr-b"
     20 AUDIT: delivering ExitIdle to CLOSED child (late)
EXIT=1
```
B-before-A: the forbidden notification was delivered in 20/20 runs and the test passed 20/20 (`awaitExitIdle(addrA)` cached it). B-after-A: the same forbidden notification was detected 20/20.

## C10

Target: evalon/grpc-go-en-a2a8173a (82ba80e851d6324f72210c2cec9c2cff08be0c8d). Probe verify/repro/c10_audit_test.go: 3 stub children. ExitIdle synchronously reports CONNECTING and Close synchronously reports TRANSIENT_FAILURE. It records every parent-facing `UpdateState`.
```sh
verify/repro/c10_lifecycle.sh a2a8173a
```
```console
    audit_c10_test.go:64: UpdateClientConnState (3 children) publications: [IDLE]
    audit_c10_test.go:67: parent ExitIdle (3 children) publications: [CONNECTING CONNECTING CONNECTING]
    audit_c10_test.go:70: parent Close (3 children) publications: [CONNECTING CONNECTING TRANSIENT_FAILURE]
    audit_c10_test.go:72: idle-exit-publication: parent ExitIdle forwarded 3 separate publications, want at most 1 consolidated
    audit_c10_test.go:75: close-publication: parent Close forwarded 3 publications during teardown, want 0
--- FAIL: TestAuditC10LifecyclePublications (0.00s)
FAIL
FAIL	google.golang.org/grpc/balancer/endpointsharding	0.014s
FAIL
```
- idle-exit-publication: parent ExitIdle forwarded one publication per child callback (3), not one consolidated publication after fan-out.
- close-publication: parent Close forwarded 3 publications during teardown, including TRANSIENT_FAILURE after the balancer was closing.
Control, same probe on the audited implementation 81201fc085e37de0ffb23505ac7831350b326c33: `parent ExitIdle (3 children) publications: [CONNECTING]`, `parent Close (3 children) publications: []`.

## C11

Target: evalon/grpc-go-en-072542f0 (e0ed8ed3643384c8aab98c9f21a7b77257a68fda). Source:
```go
func newBalancerWrapper(es *endpointSharding, endpoint resolver.Endpoint) *balancerWrapper {
	...
	bw.mu.Lock()
	bw.child = es.childBuilder(bw, es.bOpts)   // child may report IDLE -> go bw.exitIdle()
	bw.mu.Unlock()                              // released here
	return bw
}
// UpdateClientConnState: childBalancer = newBalancerWrapper(es, endpoint); ... childBalancer.updateClientConnState(...)
func (bw *balancerWrapper) updateClientConnState(ccs balancer.ClientConnState) error {
	bw.mu.Lock()                                // reacquired for initial configuration
	defer bw.mu.Unlock()
	return bw.child.UpdateClientConnState(ccs)
}
```
There is no retained or pending reconnect flag: `exitIdle` just locks `bw.mu` and calls `child.ExitIdle()` if not closed. Scheduling control (verify/repro/c11-gap-sleep.diff, no locking change): `ES_GAP_SLEEP=1` sleeps 50ms between `newBalancerWrapper` and `updateClientConnState`, so the queued worker runs in the gap. Probe (verify/repro/c11_audit_test.go): the child reports IDLE in Build (auto-reconnect on), counts an ExitIdle as effective only once configured, and reports nothing new after configuration.
```sh
verify/repro/c11_construction_gap.sh
```
```console
== ES_GAP_SLEEP=1
    audit_c11_test.go:60: ExitIdle before configuration (ineffective): 1, after configuration (effective): 0
    audit_c11_test.go:62: no effective reconnection request after initial configuration (early=1)
--- FAIL: TestAuditC11ConstructionReconnect (0.25s)
    audit_c11_test.go:60: ExitIdle before configuration (ineffective): 1, after configuration (effective): 0
    audit_c11_test.go:62: no effective reconnection request after initial configuration (early=1)
--- FAIL: TestAuditC11ConstructionReconnect (0.25s)
    audit_c11_test.go:60: ExitIdle before configuration (ineffective): 1, after configuration (effective): 0
    audit_c11_test.go:62: no effective reconnection request after initial configuration (early=1)
--- FAIL: TestAuditC11ConstructionReconnect (0.25s)
FAIL
FAIL	google.golang.org/grpc/balancer/endpointsharding	0.767s
FAIL
== ES_GAP_SLEEP=0
    audit_c11_test.go:60: ExitIdle before configuration (ineffective): 0, after configuration (effective): 1
--- PASS: TestAuditC11ConstructionReconnect (0.20s)
    audit_c11_test.go:60: ExitIdle before configuration (ineffective): 0, after configuration (effective): 1
--- PASS: TestAuditC11ConstructionReconnect (0.20s)
    audit_c11_test.go:60: ExitIdle before configuration (ineffective): 0, after configuration (effective): 1
--- PASS: TestAuditC11ConstructionReconnect (0.20s)
ok  	google.golang.org/grpc/balancer/endpointsharding	1.617s
```
With the worker scheduled in the gap, the only reconnection request was consumed before configuration and none followed (3/3). With default scheduling, the worker happened to run after configuration (3/3). The eval fixture TestEval_ConstructionIdleCallbackSafety passes on this branch.
