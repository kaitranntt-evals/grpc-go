## C1

Twelve target branches of [kaitranntt-evals/grpc-go-endpointsharding-decouple-locking](https://github.com/kaitranntt-evals/grpc-go-endpointsharding-decouple-locking), each checked out in its own worktree `~/wt/<id>` (`<id>` = the 8-char suffix of `evalon/grpc-go-en-<id>`). `go1.25.7 linux/amd64`.

```sh
cd ~/repos/grpc-go
git remote add evalrepo https://github.com/kaitranntt-evals/grpc-go-endpointsharding-decouple-locking.git
for id in 2c006c7f e97b83bb e6d88a99 2de785ca 926a7310 b47575cf be821e06 1ef76c03 587893f7 b1944cc5 ba10640a 29bcf6a8; do
  git fetch evalrepo evalon/grpc-go-en-$id && git worktree add ~/wt/$id evalrepo/evalon/grpc-go-en-$id
done
```

Each branch adds one concurrency test whose fake child blocks inside `UpdateClientConnState` on a release channel (while holding the child's per-child mutex inside `balancerWrapper`) while the test drives `ExitIdle` for a different child. Every one of these tests has a bounded "timed out waiting for the configuration update" assertion for the case where the worker never returns — which is exactly what a child/balancer deadlock (the regression these tests exist to catch) looks like. The worker-timeout path was exercised by making the worker genuinely non-returning: `verify/repro/c1_stuck_worker.py` inserts `select {}` directly after the child's `<-<release>` receive (so releasing the channel no longer lets the worker finish), runs the branch's own focused test with `go test -race -timeout 40s`, records where the test goroutine is blocked when the runner panics, and restores the file. A bounded cleanup would end the test on its own timeout assertion (a few seconds); an unbounded one hangs until the runner's `-timeout` panic.

```sh
cd ~/repos/grpc-go && WT_ROOT=~/wt C1_LOG_DIR=~/c1_logs/final python3 verify/repro/c1_stuck_worker.py     # all 12 branches; or pass branch ids
```

Result: all 12 runs ended with `panic: test timed out after 40s` (wall ≈ 40.7 s each); none completed its cleanup on its own. Per branch (cleanup code cited from the branch's test; output lines verbatim from the run; the goroutine dump excerpt is the test goroutine at the moment of the panic):

### 2c006c7f

Branch: [evalon/grpc-go-en-2c006c7f](https://github.com/kaitranntt-evals/grpc-go-endpointsharding-decouple-locking/tree/evalon/grpc-go-en-2c006c7f) (commit `0ba595e1`). Test: `balancer/endpointsharding/endpointsharding_children_ext_test.go`, `-run 'Test/EndpointSharding_ExitIdleNotBlockedByOtherChildUpdate/ChildState'`; the child's blocking receive is `<-unblock`.

Cleanup on the branch: `defer es.Close()` (l.290) then `defer unblockOnce()` (l.294): on the timeout path (`Timeout waiting for the second UpdateClientConnState to complete`, l.339) the child is released and `Close` runs immediately; nothing waits for the worker, so `Close` is the join. Bounded waits elsewhere use `ctx`.

```sh
cd ~/repos/grpc-go && WT_ROOT=~/wt python3 verify/repro/c1_stuck_worker.py 2c006c7f
```

```console
    endpointsharding_children_ext_test.go:339: Timeout waiting for the second UpdateClientConnState to complete
panic: test timed out after 40s
exit=1 wall=40.8s cleanup=UNBOUNDED (hung until go test -timeout) log=~/c1_logs/final/2c006c7f.stuck.log
```

Test goroutine at the panic (from the runner's goroutine dump in the log):

```console
goroutine 11 [sync.Mutex.Lock]:
internal/sync.(*Mutex).Lock(0xc000214f98)
	/usr/local/go/src/internal/sync/mutex.go:70
sync.(*Mutex).Lock(0xc000214f98)
	/usr/local/go/src/sync/mutex.go:46
google.golang.org/grpc/balancer/endpointsharding.(*balancerWrapper).close(0xc000214f80)
	balancer/endpointsharding/endpointsharding.go:408
google.golang.org/grpc/balancer/endpointsharding.(*endpointSharding).Close-range1(...)
	balancer/endpointsharding/endpointsharding.go:233
```

Parts: unbounded join: not held (joins are bounded or absent); Close after unsuccessful teardown: HELD. Verdict for this branch: CONFIRMED.

### e97b83bb

Branch: [evalon/grpc-go-en-e97b83bb](https://github.com/kaitranntt-evals/grpc-go-endpointsharding-decouple-locking/tree/evalon/grpc-go-en-e97b83bb) (commit `14f08d73`). Test: `balancer/endpointsharding/endpointsharding_ext_test.go`, `-run 'Test/EndpointShardingExitIdleWhileOtherChildBlocked'`; the child's blocking receive is `<-unblock`.

Cleanup on the branch: `defer es.Close()` (l.443) then `defer unblockOnce()` (l.447): after `Timed out waiting for UpdateClientConnState to return` (l.489) the child is released and `Close` runs without checking that the worker returned.

```sh
cd ~/repos/grpc-go && WT_ROOT=~/wt python3 verify/repro/c1_stuck_worker.py e97b83bb
```

```console
    endpointsharding_ext_test.go:489: Timed out waiting for UpdateClientConnState to return
panic: test timed out after 40s
exit=1 wall=40.7s cleanup=UNBOUNDED (hung until go test -timeout) log=~/c1_logs/final/e97b83bb.stuck.log
```

Test goroutine at the panic (from the runner's goroutine dump in the log):

```console
goroutine 9 [chan receive]:
google.golang.org/grpc/balancer/endpointsharding_test.Test(0xc0000bea80)
	balancer/endpointsharding/endpointsharding_ext_test.go:69
```

Parts: unbounded join: not held (joins are bounded or absent); Close after unsuccessful teardown: HELD. Verdict for this branch: CONFIRMED.

### e6d88a99

Branch: [evalon/grpc-go-en-e6d88a99](https://github.com/kaitranntt-evals/grpc-go-endpointsharding-decouple-locking/tree/evalon/grpc-go-en-e6d88a99) (commit `6d73f9b7`). Test: `balancer/endpointsharding/endpointsharding_child_test.go`, `-run 'Test/EndpointSharding_ExitIdleWhileOtherChildBlocked'`; the child's blocking receive is `<-unblockCh`.

Cleanup on the branch: `defer es.Close()` (l.138) then `defer unblock()` (l.141): after `Timed out waiting for UpdateClientConnState to return` (l.188) `Close` runs without checking that the worker returned.

```sh
cd ~/repos/grpc-go && WT_ROOT=~/wt python3 verify/repro/c1_stuck_worker.py e6d88a99
```

```console
    endpointsharding_child_test.go:189: Timed out waiting for UpdateClientConnState to return
panic: test timed out after 40s
exit=1 wall=40.7s cleanup=UNBOUNDED (hung until go test -timeout) log=~/c1_logs/final/e6d88a99.stuck.log
```

Test goroutine at the panic (from the runner's goroutine dump in the log):

```console

```

Parts: unbounded join: not held (joins are bounded or absent); Close after unsuccessful teardown: HELD. Verdict for this branch: CONFIRMED.

### 2de785ca

Branch: [evalon/grpc-go-en-2de785ca](https://github.com/kaitranntt-evals/grpc-go-endpointsharding-decouple-locking/tree/evalon/grpc-go-en-2de785ca) (commit `4f22bc7d`). Test: `balancer/endpointsharding/endpointsharding_sync_ext_test.go`, `-run 'Test/EndpointShardingExitIdleWhileOtherChildBlocked'`; the child's blocking receive is `<-releaseB`.

Cleanup on the branch: `defer es.Close()` (l.251) then `defer releaseOnce()` (l.254): after `Timeout waiting for UpdateClientConnState() to return` (l.294) `Close` runs without checking that the worker returned.

```sh
cd ~/repos/grpc-go && WT_ROOT=~/wt python3 verify/repro/c1_stuck_worker.py 2de785ca
```

```console
    endpointsharding_sync_ext_test.go:294: Timeout waiting for UpdateClientConnState() to return
panic: test timed out after 40s
exit=1 wall=40.8s cleanup=UNBOUNDED (hung until go test -timeout) log=~/c1_logs/final/2de785ca.stuck.log
```

Test goroutine at the panic (from the runner's goroutine dump in the log):

```console

```

Parts: unbounded join: not held (joins are bounded or absent); Close after unsuccessful teardown: HELD. Verdict for this branch: CONFIRMED.

### 926a7310

Branch: [evalon/grpc-go-en-926a7310](https://github.com/kaitranntt-evals/grpc-go-endpointsharding-decouple-locking/tree/evalon/grpc-go-en-926a7310) (commit `a6fc355a`). Test: `balancer/endpointsharding/concurrency_test.go`, `-run 'Test/ExitIdleWhileAnotherChildUpdates/autoReconnect=false'`; the child's blocking receive is `<-release`.

Cleanup on the branch: `defer b.Close()` (l.150) then `defer func(){ unblock(); waitForSignal(t, done, ...) }` (l.175): the join is bounded (fails with `Timed out waiting for configuration update to finish`), but the earlier-deferred `Close` then runs regardless.

```sh
cd ~/repos/grpc-go && WT_ROOT=~/wt python3 verify/repro/c1_stuck_worker.py 926a7310
```

```console
    concurrency_test.go:197: Timed out waiting for configuration update to finish
    concurrency_test.go:178: Timed out waiting for configuration update to finish
panic: test timed out after 40s
exit=1 wall=40.7s cleanup=UNBOUNDED (hung until go test -timeout) log=~/c1_logs/final/926a7310.stuck.log
```

Test goroutine at the panic (from the runner's goroutine dump in the log):

```console
goroutine 11 [sync.Mutex.Lock]:
internal/sync.(*Mutex).Lock(0xc000215098)
	/usr/local/go/src/internal/sync/mutex.go:70
sync.(*Mutex).Lock(0xc000215098)
	/usr/local/go/src/sync/mutex.go:46
google.golang.org/grpc/balancer/endpointsharding.(*balancerWrapper).Close(0xc000215080)
	balancer/endpointsharding/endpointsharding.go:367
google.golang.org/grpc/balancer/endpointsharding.(*endpointSharding).Close-range1(...)
	balancer/endpointsharding/endpointsharding.go:215
```

Parts: unbounded join: not held (joins are bounded or absent); Close after unsuccessful teardown: HELD. Verdict for this branch: CONFIRMED.

### b47575cf

Branch: [evalon/grpc-go-en-b47575cf](https://github.com/kaitranntt-evals/grpc-go-endpointsharding-decouple-locking/tree/evalon/grpc-go-en-b47575cf) (commit `a03db688`). Test: `balancer/endpointsharding/endpointsharding_concurrency_test.go`, `-run 'Test/ExitIdleWhileOtherChildUpdating/autoReconnect=false'`; the child's blocking receive is `<-releaseUpdate`.

Cleanup on the branch: `defer b.Close()` (l.163) then `defer func(){ unblockUpdate(); waitForSignal(t, ctx, updateDone, ...) }` (l.185): bounded join, then the earlier-deferred `Close` runs regardless of its outcome.

```sh
cd ~/repos/grpc-go && WT_ROOT=~/wt python3 verify/repro/c1_stuck_worker.py b47575cf
```

```console
    endpointsharding_concurrency_test.go:222: Timed out waiting for configuration update
    endpointsharding_concurrency_test.go:188: Timed out waiting for configuration update
panic: test timed out after 40s
exit=1 wall=40.7s cleanup=UNBOUNDED (hung until go test -timeout) log=~/c1_logs/final/b47575cf.stuck.log
```

Test goroutine at the panic (from the runner's goroutine dump in the log):

```console
goroutine 39 [sync.Mutex.Lock]:
internal/sync.(*Mutex).Lock(0xc00020f098)
	/usr/local/go/src/internal/sync/mutex.go:70
sync.(*Mutex).Lock(0xc00020f098)
	/usr/local/go/src/sync/mutex.go:46
google.golang.org/grpc/balancer/endpointsharding.(*balancerWrapper).close(0xc00020f080)
	balancer/endpointsharding/endpointsharding.go:369
google.golang.org/grpc/balancer/endpointsharding.(*endpointSharding).Close-range1(...)
	balancer/endpointsharding/endpointsharding.go:218
```

Parts: unbounded join: not held (joins are bounded or absent); Close after unsuccessful teardown: HELD. Verdict for this branch: CONFIRMED.

### be821e06

Branch: [evalon/grpc-go-en-be821e06](https://github.com/kaitranntt-evals/grpc-go-endpointsharding-decouple-locking/tree/evalon/grpc-go-en-be821e06) (commit `3d667bfa`). Test: `balancer/endpointsharding/concurrency_ext_test.go`, `-run 'Test/ChildExitIdleDuringOtherChildUpdate'`; the child's blocking receive is `<-release`.

Cleanup on the branch: `defer b.Close()` (l.112), `defer unblock()` (l.113); body waits `waitForSignal(t, ctx, done, "resolver update to complete")` (l.143, bounded) and on timeout the deferred `Close` runs regardless.

```sh
cd ~/repos/grpc-go && WT_ROOT=~/wt python3 verify/repro/c1_stuck_worker.py be821e06
```

```console
    concurrency_ext_test.go:144: Timed out waiting for resolver update to complete
panic: test timed out after 40s
exit=1 wall=40.6s cleanup=UNBOUNDED (hung until go test -timeout) log=~/c1_logs/final/be821e06.stuck.log
```

Test goroutine at the panic (from the runner's goroutine dump in the log):

```console

```

Parts: unbounded join: not held (joins are bounded or absent); Close after unsuccessful teardown: HELD. Verdict for this branch: CONFIRMED.

### 1ef76c03

Branch: [evalon/grpc-go-en-1ef76c03](https://github.com/kaitranntt-evals/grpc-go-endpointsharding-decouple-locking/tree/evalon/grpc-go-en-1ef76c03) (commit `63dd4988`). Test: `balancer/endpointsharding/endpointsharding_concurrency_test.go`, `-run 'Test/ExitIdleDuringOtherChildUpdate/autoReconnect=false'`; the child's blocking receive is `<-unblock`.

Cleanup on the branch: `defer func(){ release(); <-updateDone; b.Close() }` (l.184-188): the join `<-updateDone` has no deadline. After the body's `Timed out waiting for configuration update to finish` (l.203) the deferred function blocks on it forever; `Close` is never reached.

```sh
cd ~/repos/grpc-go && WT_ROOT=~/wt python3 verify/repro/c1_stuck_worker.py 1ef76c03
```

```console
    endpointsharding_concurrency_test.go:203: Timed out waiting for configuration update to finish
panic: test timed out after 40s
exit=1 wall=40.8s cleanup=UNBOUNDED (hung until go test -timeout) log=~/c1_logs/final/1ef76c03.stuck.log
```

Test goroutine at the panic (from the runner's goroutine dump in the log):

```console
goroutine 11 [chan receive]:
google.golang.org/grpc/balancer/endpointsharding_test.s.TestExitIdleDuringOtherChildUpdate.func1.4()
	balancer/endpointsharding/endpointsharding_concurrency_test.go:187
runtime.Goexit()
	/usr/local/go/src/runtime/panic.go:615
google.golang.org/grpc/balancer/endpointsharding_test.awaitResult[...](0xc0000befc0?, {0x13630b8, 0xc0002e09a0}, 0xc0002e0af0, {0x1229aec, 0x1e})
	balancer/endpointsharding/endpointsharding_concurrency_test.go:112
google.golang.org/grpc/balancer/endpointsharding_test.s.TestExitIdleDuringOtherChildUpdate.func1(0xc0000befc0)
	balancer/endpointsharding/endpointsharding_concurrency_test.go:203
```

Parts: unbounded join: HELD (`<-updateDone` without deadline); Close after unsuccessful teardown: not observed (the cleanup never gets past the join; `b.Close()` follows it unconditionally). Verdict for this branch: CONFIRMED.

### 587893f7

Branch: [evalon/grpc-go-en-587893f7](https://github.com/kaitranntt-evals/grpc-go-endpointsharding-decouple-locking/tree/evalon/grpc-go-en-587893f7) (commit `d634b126`). Test: `balancer/endpointsharding/concurrency_test.go`, `-run 'Test/ExitIdleDuringOtherChildUpdate/existing=true'`; the child's blocking receive is `<-releaseUpdate`.

Cleanup on the branch: `defer b.Close()` (l.154), `defer unblock()` (l.155); body waits `awaitValue(t, ctx, updated)` (l.188, bounded, `Timed out waiting for child operation`) and on timeout the deferred `Close` runs regardless.

```sh
cd ~/repos/grpc-go && WT_ROOT=~/wt python3 verify/repro/c1_stuck_worker.py 587893f7
```

```console
    concurrency_test.go:189: Timed out waiting for child operation: context deadline exceeded
panic: test timed out after 40s
exit=1 wall=40.7s cleanup=UNBOUNDED (hung until go test -timeout) log=~/c1_logs/final/587893f7.stuck.log
```

Test goroutine at the panic (from the runner's goroutine dump in the log):

```console
goroutine 11 [sync.Mutex.Lock]:
internal/sync.(*Mutex).Lock(0xc000215298)
	/usr/local/go/src/internal/sync/mutex.go:70
sync.(*Mutex).Lock(0xc000215298)
	/usr/local/go/src/sync/mutex.go:46
google.golang.org/grpc/balancer/endpointsharding.(*balancerWrapper).close(0xc000215280)
	balancer/endpointsharding/endpointsharding.go:378
google.golang.org/grpc/balancer/endpointsharding.(*endpointSharding).Close-range1(...)
	balancer/endpointsharding/endpointsharding.go:226
```

Parts: unbounded join: not held (joins are bounded or absent); Close after unsuccessful teardown: HELD. Verdict for this branch: CONFIRMED.

### b1944cc5

Branch: [evalon/grpc-go-en-b1944cc5](https://github.com/kaitranntt-evals/grpc-go-endpointsharding-decouple-locking/tree/evalon/grpc-go-en-b1944cc5) (commit `f03cef3e`). Test: `balancer/endpointsharding/endpointsharding_concurrency_test.go`, `-run 'Test/ExitIdleWhileOtherChildUpdating/autoReconnect=false'`; the child's blocking receive is `<-unblockUpdate`.

Cleanup on the branch: `defer es.Close()` (l.108) then `defer func(){ release(); waitForEvent(ctx, t, updateDone, ...) }` (l.133): bounded join, then the earlier-deferred `Close` runs regardless of its outcome.

```sh
cd ~/repos/grpc-go && WT_ROOT=~/wt python3 verify/repro/c1_stuck_worker.py b1944cc5
```

```console
    endpointsharding_concurrency_test.go:149: Timed out waiting for configuration update to finish
    endpointsharding_concurrency_test.go:136: Timed out waiting for configuration update to finish
panic: test timed out after 40s
exit=1 wall=40.9s cleanup=UNBOUNDED (hung until go test -timeout) log=~/c1_logs/final/b1944cc5.stuck.log
```

Test goroutine at the panic (from the runner's goroutine dump in the log):

```console
goroutine 23 [sync.Mutex.Lock]:
internal/sync.(*Mutex).Lock(0xc000198c18)
	/usr/local/go/src/internal/sync/mutex.go:70
sync.(*Mutex).Lock(0xc000198c18)
	/usr/local/go/src/sync/mutex.go:46
google.golang.org/grpc/balancer/endpointsharding.(*balancerWrapper).close(0xc000198c00)
	balancer/endpointsharding/endpointsharding.go:375
google.golang.org/grpc/balancer/endpointsharding.(*endpointSharding).Close-range1(...)
	balancer/endpointsharding/endpointsharding.go:221
```

Parts: unbounded join: not held (joins are bounded or absent); Close after unsuccessful teardown: HELD. Verdict for this branch: CONFIRMED.

### ba10640a

Branch: [evalon/grpc-go-en-ba10640a](https://github.com/kaitranntt-evals/grpc-go-endpointsharding-decouple-locking/tree/evalon/grpc-go-en-ba10640a) (commit `ed4635c9`). Test: `balancer/endpointsharding/endpointsharding_concurrency_test.go`, `-run 'Test/ChildExitIdleIndependentOfOtherChildUpdate/autoReconnect=false'`; the child's blocking receive is `<-release`.

Cleanup on the branch: `defer b.Close()` (l.121); body waits `waitForSignal(t, ctx, updated, "configuration update completion")` (l.162, bounded) and on timeout the deferred `Close` runs regardless.

```sh
cd ~/repos/grpc-go && WT_ROOT=~/wt python3 verify/repro/c1_stuck_worker.py ba10640a
```

```console
    endpointsharding_concurrency_test.go:163: Timed out waiting for configuration update completion
panic: test timed out after 40s
exit=1 wall=40.7s cleanup=UNBOUNDED (hung until go test -timeout) log=~/c1_logs/final/ba10640a.stuck.log
```

Test goroutine at the panic (from the runner's goroutine dump in the log):

```console
goroutine 24 [sync.Mutex.Lock]:
internal/sync.(*Mutex).Lock(0xc000196b18)
	/usr/local/go/src/internal/sync/mutex.go:70
sync.(*Mutex).Lock(0xc000196b18)
	/usr/local/go/src/sync/mutex.go:46
google.golang.org/grpc/balancer/endpointsharding.(*balancerWrapper).close(0xc000196b00)
	balancer/endpointsharding/endpointsharding.go:383
google.golang.org/grpc/balancer/endpointsharding.(*endpointSharding).Close-range1(...)
	balancer/endpointsharding/endpointsharding.go:228
```

Parts: unbounded join: not held (joins are bounded or absent); Close after unsuccessful teardown: HELD. Verdict for this branch: CONFIRMED.

### 29bcf6a8

Branch: [evalon/grpc-go-en-29bcf6a8](https://github.com/kaitranntt-evals/grpc-go-endpointsharding-decouple-locking/tree/evalon/grpc-go-en-29bcf6a8) (commit `a02e022b`). Test: `balancer/endpointsharding/endpointsharding_concurrency_test.go`, `-run 'Test/ChildExitIdleDuringOtherChildUpdate/autoReconnect=false'`; the child's blocking receive is `<-releaseUpdate`.

Cleanup on the branch: `defer b.Close()` (l.122) then `defer func(){ unblock(); waitForEvent(t, updateDone, ...) }` (l.143): bounded join, then the earlier-deferred `Close` runs regardless of its outcome.

```sh
cd ~/repos/grpc-go && WT_ROOT=~/wt python3 verify/repro/c1_stuck_worker.py 29bcf6a8
```

```console
    endpointsharding_concurrency_test.go:162: Timed out waiting for configuration update to finish
    endpointsharding_concurrency_test.go:146: Timed out waiting for configuration update to finish
panic: test timed out after 40s
exit=1 wall=40.7s cleanup=UNBOUNDED (hung until go test -timeout) log=~/c1_logs/final/29bcf6a8.stuck.log
```

Test goroutine at the panic (from the runner's goroutine dump in the log):

```console
goroutine 24 [sync.Mutex.Lock]:
internal/sync.(*Mutex).Lock(0xc000198ae0)
	/usr/local/go/src/internal/sync/mutex.go:70
sync.(*Mutex).Lock(0xc000198ae0)
	/usr/local/go/src/sync/mutex.go:46
google.golang.org/grpc/balancer/endpointsharding.(*balancerWrapper).close(0xc000198a80)
	balancer/endpointsharding/endpointsharding.go:385
google.golang.org/grpc/balancer/endpointsharding.(*endpointSharding).Close-range1(...)
	balancer/endpointsharding/endpointsharding.go:228
```

Parts: unbounded join: not held (joins are bounded or absent); Close after unsuccessful teardown: HELD. Verdict for this branch: CONFIRMED.

### Summary and impact

| branch | own timeout assertion fired | cleanup outcome | where the test goroutine was stuck | part held |
|---|---|---|---|---|
| [2c006c7f](https://github.com/kaitranntt-evals/grpc-go-endpointsharding-decouple-locking/tree/evalon/grpc-go-en-2c006c7f) | yes | hung until `panic: test timed out after 40s` | `(*endpointSharding).Close` → `(*balancerWrapper).close` → `sync.(*Mutex).Lock` (child mutex still held by the stuck worker) | Close after unsuccessful worker teardown |
| [e97b83bb](https://github.com/kaitranntt-evals/grpc-go-endpointsharding-decouple-locking/tree/evalon/grpc-go-en-e97b83bb) | yes | hung until `panic: test timed out after 40s` | `(*endpointSharding).Close` → `(*balancerWrapper).close` → `sync.(*Mutex).Lock` (child mutex still held by the stuck worker) | Close after unsuccessful worker teardown |
| [e6d88a99](https://github.com/kaitranntt-evals/grpc-go-endpointsharding-decouple-locking/tree/evalon/grpc-go-en-e6d88a99) | yes | hung until `panic: test timed out after 40s` | `(*endpointSharding).Close` → `(*balancerWrapper).close` → `sync.(*Mutex).Lock` (child mutex still held by the stuck worker) | Close after unsuccessful worker teardown |
| [2de785ca](https://github.com/kaitranntt-evals/grpc-go-endpointsharding-decouple-locking/tree/evalon/grpc-go-en-2de785ca) | yes | hung until `panic: test timed out after 40s` | `(*endpointSharding).Close` → `(*balancerWrapper).close` → `sync.(*Mutex).Lock` (child mutex still held by the stuck worker) | Close after unsuccessful worker teardown |
| [926a7310](https://github.com/kaitranntt-evals/grpc-go-endpointsharding-decouple-locking/tree/evalon/grpc-go-en-926a7310) | yes | hung until `panic: test timed out after 40s` | `(*endpointSharding).Close` → `(*balancerWrapper).close` → `sync.(*Mutex).Lock` (child mutex still held by the stuck worker) | Close after unsuccessful worker teardown |
| [b47575cf](https://github.com/kaitranntt-evals/grpc-go-endpointsharding-decouple-locking/tree/evalon/grpc-go-en-b47575cf) | yes | hung until `panic: test timed out after 40s` | `(*endpointSharding).Close` → `(*balancerWrapper).close` → `sync.(*Mutex).Lock` (child mutex still held by the stuck worker) | Close after unsuccessful worker teardown |
| [be821e06](https://github.com/kaitranntt-evals/grpc-go-endpointsharding-decouple-locking/tree/evalon/grpc-go-en-be821e06) | yes | hung until `panic: test timed out after 40s` | `(*endpointSharding).Close` → `(*balancerWrapper).close` → `sync.(*Mutex).Lock` (child mutex still held by the stuck worker) | Close after unsuccessful worker teardown |
| [1ef76c03](https://github.com/kaitranntt-evals/grpc-go-endpointsharding-decouple-locking/tree/evalon/grpc-go-en-1ef76c03) | yes | hung until `panic: test timed out after 40s` | unbounded `<-updateDone` join (l.186) | unbounded worker join |
| [587893f7](https://github.com/kaitranntt-evals/grpc-go-endpointsharding-decouple-locking/tree/evalon/grpc-go-en-587893f7) | yes | hung until `panic: test timed out after 40s` | `(*endpointSharding).Close` → `(*balancerWrapper).close` → `sync.(*Mutex).Lock` (child mutex still held by the stuck worker) | Close after unsuccessful worker teardown |
| [b1944cc5](https://github.com/kaitranntt-evals/grpc-go-endpointsharding-decouple-locking/tree/evalon/grpc-go-en-b1944cc5) | yes | hung until `panic: test timed out after 40s` | `(*endpointSharding).Close` → `(*balancerWrapper).close` → `sync.(*Mutex).Lock` (child mutex still held by the stuck worker) | Close after unsuccessful worker teardown |
| [ba10640a](https://github.com/kaitranntt-evals/grpc-go-endpointsharding-decouple-locking/tree/evalon/grpc-go-en-ba10640a) | yes | hung until `panic: test timed out after 40s` | `(*endpointSharding).Close` → `(*balancerWrapper).close` → `sync.(*Mutex).Lock` (child mutex still held by the stuck worker) | Close after unsuccessful worker teardown |
| [29bcf6a8](https://github.com/kaitranntt-evals/grpc-go-endpointsharding-decouple-locking/tree/evalon/grpc-go-en-29bcf6a8) | yes | hung until `panic: test timed out after 40s` | `(*endpointSharding).Close` → `(*balancerWrapper).close` → `sync.(*Mutex).Lock` (child mutex still held by the stuck worker) | Close after unsuccessful worker teardown |


Verdict: CONFIRMED on all 12 branches. Part "unbounded worker joins" held on 1ef76c03 only; part "Close after unsuccessful worker teardown" held on the other 11 (on 1ef76c03 the unconditional `b.Close()` follows the join but is never reached). Impact: these are the tests meant to detect a child/balancer deadlock regression in `endpointsharding`. If such a regression happens, the test's own timeout assertion fires and reports the failure, but the deferred cleanup then blocks forever (either joining the stuck worker or calling `Close`, which takes the child mutex the stuck worker still holds), so the package test binary hangs until `go test`'s global `-timeout` (10 minutes by default) kills it with a goroutine dump; in `-race`/CI runs this turns one clear failure into a slow, noisy timeout and blocks every other test in the package binary. Fix: after a failed/timed-out worker join, skip `Close` (or run it in a goroutine with a bounded wait) and make the join itself bounded (`select` on the done channel and `ctx.Done()`), e.g. `t.Cleanup(func(){ release(); select { case <-updateDone: es.Close(); case <-time.After(d): t.Log("worker still blocked; skipping Close") } })`.

## C2

Target: branch `evalon/grpc-go-en-7cc18f1f` (commit `d9043cd3`) of [kaitranntt-evals/grpc-go-endpointsharding-decouple-locking](https://github.com/kaitranntt-evals/grpc-go-endpointsharding-decouple-locking), checked out at `~/wt/7cc18f1f`. Tooling: `go1.25.7 linux/amd64`, `staticcheck 2026.1 (v0.7.0)` (the tool `scripts/vet.sh` installs and runs).

Setup:

```sh
cd ~/repos/grpc-go
git remote add evalrepo https://github.com/kaitranntt-evals/grpc-go-endpointsharding-decouple-locking.git
git fetch evalrepo evalon/grpc-go-en-7cc18f1f
git worktree add ~/wt/7cc18f1f evalrepo/evalon/grpc-go-en-7cc18f1f
go install honnef.co/go/tools/cmd/staticcheck@latest   # ~/go/bin/staticcheck
```

What the branch changed in ringhash (it replaced the endpointsharding interface with the deprecated `balancer.ExitIdler`):

```sh
cd ~/wt/7cc18f1f && git diff $(git merge-base HEAD origin/master) HEAD -- balancer/ringhash/ringhash.go | grep -n '^[-+].*ExitIdler\|^[-+].*balancer:'
```

```console
9:-				balancer: childState.Balancer,
10:+				balancer: childState,
18:-		var idleBalancer endpointsharding.ExitIdler
19:+		var idleBalancer balancer.ExitIdler
27:-	balancer endpointsharding.ExitIdler
28:+	balancer balancer.ExitIdler
```

Deprecation annotation on the interface the branch now uses (`balancer/balancer.go`):

```sh
grep -n -B2 'type ExitIdler interface' balancer/balancer.go
```

```console
// Deprecated: All balancers must implement this interface. This interface will
// be removed in a future release.
type ExitIdler interface {
```

How the repository runs staticcheck (`scripts/vet.sh`): `staticcheck -checks 'all' ./... >"${SC_OUT}" || true`, then SA1019 lines are filtered with `noret_grep "(SA1019)" "${SC_OUT}" | not grep -Fv '<exclusion list>'` — any SA1019 line not matching an exclusion fails vet. The exclusion list (46 fixed strings, e.g. `BalancerAttributes is deprecated:`, `UpdateSubConnState is deprecated:`, `balancer.ErrTransientFailure is deprecated:`) contains no entry for `ExitIdler`. `.github/workflows/testing.yml` runs `scripts/vet.sh` in its `vet` job.

Repro (`verify/repro/c2_staticcheck_ringhash.sh` runs the same staticcheck invocation on the package and applies the exclusion list parsed from `scripts/vet.sh` itself):

```sh
cd ~/wt/7cc18f1f && PATH=$PATH:~/go/bin bash ~/repos/grpc-go/verify/repro/c2_staticcheck_ringhash.sh
```

```console
== uses of balancer.ExitIdler in balancer/ringhash
balancer/ringhash/ringhash.go:271:		var idleBalancer balancer.ExitIdler
balancer/ringhash/ringhash.go:402:	balancer balancer.ExitIdler
== staticcheck -checks 'all' ./balancer/ringhash (SA1019 lines only)
balancer/ringhash/ringhash.go:271:20: balancer.ExitIdler is deprecated: All balancers must implement this interface. This interface will be removed in a future release.  (SA1019)
balancer/ringhash/ringhash.go:402:11: balancer.ExitIdler is deprecated: All balancers must implement this interface. This interface will be removed in a future release.  (SA1019)
balancer/ringhash/ringhash_test.go:687:2: addr.BalancerAttributes is deprecated: when an Address is inside an Endpoint, this field should not be used, and it will eventually be removed entirely.  (SA1019)
balancer/ringhash/ringhash_test.go:687:28: addr.BalancerAttributes is deprecated: when an Address is inside an Endpoint, this field should not be used, and it will eventually be removed entirely.  (SA1019)
== SA1019 lines that survive the exclusion list in scripts/vet.sh
   (exclusion list: 46 patterns from scripts/vet.sh)
balancer/ringhash/ringhash.go:271:20: balancer.ExitIdler is deprecated: All balancers must implement this interface. This interface will be removed in a future release.  (SA1019)
balancer/ringhash/ringhash.go:402:11: balancer.ExitIdler is deprecated: All balancers must implement this interface. This interface will be removed in a future release.  (SA1019)
RESULT: 2 non-excluded SA1019 diagnostic(s); vet.sh would fail on them
```

The two pre-existing `BalancerAttributes` diagnostics are excluded by the list (`BalancerAttributes is deprecated:`); the two `balancer.ExitIdler` diagnostics introduced by the branch are not. `go vet ./...` (the plain Go vet, which has no deprecation check) passes on the branch, so this only surfaces via `scripts/vet.sh`/staticcheck.

Verdict: CONFIRMED. Impact: the repository's lint job (`scripts/vet.sh`, run by the `vet` workflow job) fails on this branch with the two diagnostics above; it is deterministic, not environment-dependent. Fix: store `endpointsharding.ChildState` (or its `ExitIdle` method value) in `ringhash.endpointState` instead of a `balancer.ExitIdler`, or add the notice to the exclusion list (not appropriate: the interface is scheduled for removal).

## C3

Target: branch `evalon/grpc-go-en-2c006c7f` (commit `0ba595e1`) of [kaitranntt-evals/grpc-go-endpointsharding-decouple-locking](https://github.com/kaitranntt-evals/grpc-go-endpointsharding-decouple-locking), checked out at `~/wt/2c006c7f`. `go1.25.7 linux/amd64`.

```sh
cd ~/repos/grpc-go && git fetch evalrepo evalon/grpc-go-en-2c006c7f && git worktree add ~/wt/2c006c7f evalrepo/evalon/grpc-go-en-2c006c7f
```

Mechanism under test (`balancer/endpointsharding/endpointsharding.go` on the branch): the batch sets `es.inhibitChildUpdates.Store(true)` at the start of `UpdateClientConnState` and clears it in a deferred function that then calls `es.updateState()`; a child callback (`balancerWrapper.UpdateState`) stores its state under `es.mu`, releases `es.mu`, and then calls `es.updateState()`, whose inhibition decision is a lock-free `es.inhibitChildUpdates.Load()` made *before* `es.mu` is re-acquired for publication:

```go
func (es *endpointSharding) updateState() {
	if es.inhibitChildUpdates.Load() {
		return
	}
	...
	es.mu.Lock()
	defer es.mu.Unlock()
```

Both repros use a probe parent `ClientConn` that records every publication, children whose endpoints carry a version attribute (`v=0` before the batch, `v=1` after), and pickers tagged with their origin (`a-sync-v1` = state reported synchronously from the batch configuration, `cb#N` = state from an out-of-batch callback with sequence N). Child B blocks inside its `v=1` batch configuration until released, so "batch unfinished" is a controlled state.

### Admission boundary (deterministic; needs a one-hook instrumentation patch on the target's production file, reverted afterwards)

`verify/repro/c3_admission_hook.patch` adds a nil-by-default `c3TestHookAfterInhibitCheck` call right after the inhibition check in `updateState`; the test uses it to pause exactly one callback there.

```sh
cd ~/wt/2c006c7f
git apply ~/repos/grpc-go/verify/repro/c3_admission_hook.patch
cp ~/repos/grpc-go/verify/repro/c3_batch_boundary_test.go ~/repos/grpc-go/verify/repro/c3_admission_hook_test.go balancer/endpointsharding/
go test ./balancer/endpointsharding -run 'TestC3Hook_' -tags repro -race -count=1 -v
rm balancer/endpointsharding/c3_*_test.go && git checkout -- balancer/endpointsharding/endpointsharding.go
```

Output (3 of 3 runs identical apart from which child the check names first):

```console
=== RUN   TestC3Hook_AdmissionBoundaryPublishesPartialBatch
    c3_admission_hook_test.go:63: publication #0 (initial batch): a(v=0,CONNECTING,a-sync-v0#0) b(v=0,CONNECTING,b-sync-v0#0)
    c3_admission_hook_test.go:101: publication received while B's batch configuration was still blocked: a(v=1,CONNECTING,a-sync-v1#0) b(v=1,CONNECTING,b-sync-v0#0)
    c3_admission_hook_test.go:108: admission boundary NOT protected: parent received b with v=1 (batch data) while the batch was still blocked in B (B state still b-sync-v0)
    c3_admission_hook_test.go:127: final publication #2 (batch): a(v=1,CONNECTING,a-sync-v1#0) b(v=1,CONNECTING,b-sync-v1#0)
--- FAIL: TestC3Hook_AdmissionBoundaryPublishesPartialBatch (0.00s)
FAIL
FAIL	google.golang.org/grpc/balancer/endpointsharding	0.013s
```

Reading: publication #1 was emitted by the paused callback while the batch was still blocked inside B. It carries the batch's new attributes for both children (`v=1`) and A's new synchronous state (`a-sync-v1`), but B's *old* state (`b-sync-v0`) — a mixture of pre- and post-batch data that the batch itself never publishes (#0 and #2 are the only consistent snapshots). Because the batch processes endpoints in rotated order, in one run the publication was `a(v=0,READY,cb#7) b(v=1,CONNECTING,b-sync-v0#0)` — still mixed (B's new attributes with B's old state).

### Completion boundary (stochastic; no instrumentation)

```sh
cd ~/wt/2c006c7f
cp ~/repos/grpc-go/verify/repro/c3_batch_boundary_test.go balancer/endpointsharding/
go test ./balancer/endpointsharding -run 'TestC3_' -tags repro -race -count=1 -v
rm balancer/endpointsharding/c3_*_test.go
```

```console
=== RUN   TestC3_CompletionBoundaryDuplicatesFinalPublication
    c3_batch_boundary_test.go:192: attempt 1: batch final publication #1 re-published by a child callback as #2: a(v=1,READY,cb#1004) b(v=1,CONNECTING,b-sync-v1#0)
    c3_batch_boundary_test.go:192: attempt 3: batch final publication #1 re-published by a child callback as #2: a(v=1,READY,cb#1041) b(v=1,CONNECTING,b-sync-v1#0)
    ...
    c3_batch_boundary_test.go:198: attempts=40 duplicate-consolidated-publications=13
    c3_batch_boundary_test.go:200: completion boundary NOT protected: 13/40 attempts published the consolidated batch state twice
--- FAIL: TestC3_CompletionBoundaryDuplicatesFinalPublication (0.11s)
FAIL
FAIL	google.golang.org/grpc/balancer/endpointsharding	0.121s
```

(An earlier run of the same test observed 11/40.) Reading: the batch's own final publication (identified by the `(*endpointSharding).UpdateClientConnState` frame on the publishing goroutine's stack) already contained A's callback record `cb#1004`; the very next publication came from a child callback and is byte-for-byte the same consolidated state (same `cb#1004`, same `b-sync-v1`, same attributes). The callback had stored its state during the batch, was inhibited, and then — because the inhibition decision is taken without holding `es.mu` — passed the check after the batch cleared the flag and published the already-published state again.

### Control runs

Same test files against the audited reference implementation (`~/repos/grpc-go`, branch `verify/grpc-go-endpointsharding-decouple-locking-v-db99d745` from `origin/grpc-go-endpointsharding-decouple-locking-perfect`, which takes the inhibition decision under `es.mu`):

```sh
cd ~/repos/grpc-go && cp verify/repro/c3_batch_boundary_test.go balancer/endpointsharding/ && go test ./balancer/endpointsharding -run 'TestC3_' -tags repro -race -count=1 -v; rm balancer/endpointsharding/c3_*_test.go
```

```console
    c3_batch_boundary_test.go:198: attempts=40 duplicate-consolidated-publications=0
--- PASS: TestC3_CompletionBoundaryDuplicatesFinalPublication (0.11s)
```

Same test against the task's base commit `bf9e7cd3` (worktree `~/wt/base`):

```console
    c3_batch_boundary_test.go:198: attempts=40 duplicate-consolidated-publications=10
--- FAIL: TestC3_CompletionBoundaryDuplicatesFinalPublication (0.11s)
```

So the window is inherited from the base commit's lock-free `inhibitChildUpdates` check; the target branch kept it, while the reference implementation closes it.

Verdict: CONFIRMED (both parts). Impact: the task's guarantee "a multi-endpoint `UpdateClientConnState` publishes one consolidated parent state after the operation completes, rather than publishing intermediate child states" does not hold on this branch whenever a child reports state concurrently with a resolver update — an everyday situation (subchannel transitions racing with an EDS/DNS update). Consumers such as ringhash/priority observe a picker containing a mixture of pre- and post-update endpoint attributes and states (admission part) and a redundant second publication of the same state (completion part, harmless but wasteful: an extra picker swap on the channel). Fix: take the inhibition decision under `es.mu` (as the reference does — `inhibitChildUpdates` guarded by the mutex and checked inside the locked publish path), or re-check it after acquiring `es.mu`.

## C4

Target: branch `evalon/grpc-go-en-e97b83bb` (commit `14f08d73`) of [kaitranntt-evals/grpc-go-endpointsharding-decouple-locking](https://github.com/kaitranntt-evals/grpc-go-endpointsharding-decouple-locking), checked out at `~/wt/e97b83bb`. `go1.25.7 linux/amd64`. The fixture is the archived `tests/eval_endpointsharding_test.go` extracted to `/home/ubuntu/eval_tests/tests/eval_endpointsharding_test.go`.

```sh
cd ~/repos/grpc-go && git fetch evalrepo evalon/grpc-go-en-e97b83bb && git worktree add ~/wt/e97b83bb evalrepo/evalon/grpc-go-en-e97b83bb
```

Inventory of the committed endpoint-sharding test suite on the branch (the names in the claim's "where to look", `TestClosedStateGuardCoverage` and `TestBatchUpdateChildErrorConsolidation`, do not exist under any name):

```sh
cd ~/wt/e97b83bb && grep -n '^func (s) Test\|^func Test' balancer/endpointsharding/*_test.go
```

```console
balancer/endpointsharding/endpointsharding_ext_test.go:68:func Test(t *testing.T) {
balancer/endpointsharding/endpointsharding_ext_test.go:138:func (s) TestEndpointShardingBasic(t *testing.T) {
balancer/endpointsharding/endpointsharding_ext_test.go:214:func (s) TestEndpointShardingReconnectDisabled(t *testing.T) {
balancer/endpointsharding/endpointsharding_ext_test.go:299:func (s) TestEndpointShardingExitIdle(t *testing.T) {
balancer/endpointsharding/endpointsharding_ext_test.go:414:func (s) TestEndpointShardingExitIdleWhileOtherChildBlocked(t *testing.T) {
balancer/endpointsharding/endpointsharding_ext_test.go:501:func (s) TestEndpointShardingIdleReportedDuringChildBuild(t *testing.T) {
balancer/endpointsharding/endpointsharding_ext_test.go:560:func (s) TestEndpointShardingSingleUpdatePerClientConnUpdate(t *testing.T) {
balancer/endpointsharding/endpointsharding_ext_test.go:614:func (s) TestEndpointShardingStateAggregation(t *testing.T) {
balancer/endpointsharding/endpointsharding_ext_test.go:696:func (s) TestEndpointShardingConcurrentChildStateAndAttributeUpdates(t *testing.T) {
balancer/endpointsharding/endpointsharding_test.go:33:func Test(t *testing.T) {
balancer/endpointsharding/endpointsharding_test.go:37:func (s) TestRotateEndpoints(t *testing.T) {
```

```sh
grep -n 'isClosed\|Closed\|stale' balancer/endpointsharding/*_test.go        # -> no output
grep -n 'UpdateClientConnState: func' -A3 balancer/endpointsharding/endpointsharding_ext_test.go | grep return
```

```console
232-			return bd.ChildBalancer.UpdateClientConnState(ccs)
313-			return bd.ChildBalancer.UpdateClientConnState(ccs)
516-			return nil
```

No committed test retains a closed/replaced child's handle, and no stub child ever returns an error from `UpdateClientConnState`. The behaviors themselves exist in production code — `exitIdleLocked` has `if !bw.isClosed { bw.child.ExitIdle() }`, and `UpdateClientConnState` records the first child error and keeps processing (`err != nil && ret == nil { ret = err }`) — so the question is only whether the committed suite asserts them.

Mutation test (the whole sequence below is scripted in `verify/repro/c4_mutation_check.sh`: `cd ~/wt/e97b83bb && REPRO=~/repos/grpc-go/verify/repro FIXTURE=/home/ubuntu/eval_tests/tests/eval_endpointsharding_test.go bash $REPRO/c4_mutation_check.sh`). Two patches under `verify/repro/` remove exactly one behavior each:

- `c4_mutation_stale_handle.patch`: `if !bw.isClosed {` → `if true {` (closed child handles are no longer rejected).
- `c4_mutation_child_error_abort.patch`: first child error → `return err` immediately (remaining children not configured/stored).

```sh
cd ~/wt/e97b83bb
F=/home/ubuntu/eval_tests/tests/eval_endpointsharding_test.go
go test ./balancer/endpointsharding -race -count=1                                    # baseline, committed suite
cp $F balancer/endpointsharding/eval_endpointsharding_test.go && go test ./balancer/endpointsharding -race -count=1 && rm balancer/endpointsharding/eval_endpointsharding_test.go   # baseline + fixture
for m in stale_handle child_error_abort; do
  git apply ~/repos/grpc-go/verify/repro/c4_mutation_$m.patch
  go test ./balancer/endpointsharding -race -count=1 -v | grep -E '^(--- FAIL|FAIL|ok|PASS)'            # committed suite only
  cp $F balancer/endpointsharding/eval_endpointsharding_test.go
  go test ./balancer/endpointsharding -race -count=1 -v | grep -E '^(    --- FAIL|--- FAIL|FAIL|ok)|eval_endpointsharding_test.go:[0-9]+:'   # + archived fixture
  rm balancer/endpointsharding/eval_endpointsharding_test.go; git checkout -- balancer/endpointsharding/endpointsharding.go
done
```

```console
== baseline committed suite
ok  	google.golang.org/grpc/balancer/endpointsharding	1.116s
== baseline committed suite + fixture
ok  	google.golang.org/grpc/balancer/endpointsharding	1.897s
== mutation stale_handle: committed suite
PASS
ok  	google.golang.org/grpc/balancer/endpointsharding	1.108s
== mutation stale_handle: committed suite + eval fixture
    eval_endpointsharding_test.go:1285: ExitIdle called child after Close finished (queued call on closed child)
--- FAIL: TestEval_SameChildMutualExclusion (0.63s)
    --- FAIL: TestEval_SameChildMutualExclusion/CloseVsExposedExitIdle (0.10s)
FAIL
FAIL	google.golang.org/grpc/balancer/endpointsharding	0.793s
== mutation child_error_abort: committed suite
PASS
ok  	google.golang.org/grpc/balancer/endpointsharding	1.116s
== mutation child_error_abort: committed suite + eval fixture
    eval_endpointsharding_test.go:2209: expected 3 children built despite child 2 error, got 2 (early abort defect)
    eval_endpointsharding_test.go:2215: expected endpoint 10.0.0.2 to receive configuration update despite earlier error, got 0
    eval_endpointsharding_test.go:2225: expected 3 child states in consolidated picker despite child error, got 0
--- FAIL: TestEval_BatchUpdateInhibition (0.00s)
    --- FAIL: TestEval_BatchUpdateInhibition/ChildErrorConsolidation (0.00s)
FAIL
FAIL	google.golang.org/grpc/balancer/endpointsharding	0.904s
```

(The endpoint named in the `expected endpoint 10.0.0.2 ...` line varies between runs — `10.0.0.3` in a second run — because the batch rotates the endpoint order.) Reading: with either behavior removed, the branch's committed suite stays green (`PASS`/`ok`), while the archived (uncommitted) fixture immediately fails the exact contract (`CloseVsExposedExitIdle` for stale handles, `ChildErrorConsolidation` for continuation). The two contracts are therefore real, testable and currently unprotected by anything committed on the branch.

Verdict: CONFIRMED (both parts). Impact: a future refactor that drops the `isClosed` guard (a delayed `ExitIdle` reaching a closed pick-first child, which may then try to create SubConns on a torn-down channel) or that aborts the endpoint loop on the first child error (silently leaving some endpoints unconfigured and absent from the picker) would ship with the branch's own tests passing. Fix: port the two fixture scenarios into `balancer/endpointsharding/endpointsharding_ext_test.go` (a stub child that records `ExitIdle` after `Close`; a stub child whose `UpdateClientConnState` returns an error for one endpoint while the others must still be built, configured and present in `ChildStatesFromPicker`).

## C5

Target: branch `evalon/grpc-go-en-5bb04be3` (commit `d805abbd`) of [kaitranntt-evals/grpc-go-endpointsharding-decouple-locking](https://github.com/kaitranntt-evals/grpc-go-endpointsharding-decouple-locking), checked out at `~/wt/5bb04be3`. `go1.25.7 linux/amd64`.

```sh
cd ~/repos/grpc-go && git fetch evalrepo evalon/grpc-go-en-5bb04be3 && git worktree add ~/wt/5bb04be3 evalrepo/evalon/grpc-go-en-5bb04be3
```

Declarations on the branch:

```sh
cd ~/wt/5bb04be3 && sed -n 44,70p balancer/endpointsharding/endpointsharding.go && grep -n 'ExitIdler\|balancer:' balancer/ringhash/ringhash.go
```

```console
type ChildState struct {
	Endpoint resolver.Endpoint
	State    balancer.State
	bw *balancerWrapper
}
func (cs ChildState) ExitIdle() { ... cs.bw.ExitIdle() }
// ExitIdler provides access to only the ExitIdle method of the child balancer.
type ExitIdler interface {
	ExitIdle()
}
balancer/ringhash/ringhash.go:145:				balancer: childState,
balancer/ringhash/ringhash.go:271:		var idleBalancer endpointsharding.ExitIdler
balancer/ringhash/ringhash.go:402:	balancer endpointsharding.ExitIdler
```

The whole ringhash change on the branch is one line (the `Balancer` field was removed from `ChildState`, so ringhash now boxes the entire `ChildState` into its unchanged interface-typed field):

```sh
git diff $(git merge-base HEAD origin/master) HEAD -- balancer/ringhash/ringhash.go | grep '^[-+]\s'
```

```console
-				balancer: childState.Balancer,
+				balancer: childState,
```

Behavioral probe (`verify/repro/c5_ringhash_exitidler_probe_test.go`, package `ringhash`): reports the static and dynamic type of `endpointState.balancer` via reflection, then drives a real `endpointsharding` balancer with an Idle fake child into `ringhashBalancer.UpdateState` and exercises the picker's random-hash path (`RequestHashHeader` configured, no metadata), which is the code path `es.balancer.ExitIdle()` in `picker.go`:

```sh
cd ~/wt/5bb04be3 && cp ~/repos/grpc-go/verify/repro/c5_ringhash_exitidler_probe_test.go balancer/ringhash/ && go test ./balancer/ringhash -run 'TestC5_' -tags repro -race -count=1 -v; rm balancer/ringhash/c5_*_test.go
```

```console
=== RUN   TestC5_RinghashStoresChildBehindLegacyExitIdlerInterface
    c5_ringhash_exitidler_probe_test.go:75: ringhash endpointState.balancer static type = endpointsharding.ExitIdler (kind=interface)
    c5_ringhash_exitidler_probe_test.go:77: endpointsharding.ExitIdler declared as interface with 1 method(s): ExitIdle
    c5_ringhash_exitidler_probe_test.go:84: ChildState exposes ExitIdle as a method: func(endpointsharding.ChildState)
    c5_ringhash_exitidler_probe_test.go:116: value stored in endpointState.balancer has dynamic type endpointsharding.ChildState (boxed in interface endpointsharding.ExitIdler)
    c5_ringhash_exitidler_probe_test.go:130: picker path: es.balancer.ExitIdle() (interface call) reached the child's ExitIdle
--- PASS: TestC5_RinghashStoresChildBehindLegacyExitIdlerInterface (0.00s)
PASS
ok  	google.golang.org/grpc/balancer/ringhash	1.028s
```

(The test passes because its assertions encode the claim's confirm condition; the log lines are the observation.) The reconnection path in `updatePickerLocked` uses the same representation: `var idleBalancer endpointsharding.ExitIdler ... idleBalancer = es.balancer ... idleBalancer.ExitIdle()` (lines 271–284), and `picker_test.go` still assigns a `*fakeExitIdler` to the field, which compiles only because the field is the legacy interface.

Verdict: CONFIRMED. Impact: functionally ringhash still reconnects (the interface call reaches the child), so there is no user-visible regression; the issue is architectural: the task asked to "prefer simplifying `ChildState` so consumer policies can invoke idle reconnection directly on the child state ... updating `balancer/ringhash` to use the direct interface", and this branch left `endpointsharding.ExitIdler` in place and kept ringhash storing/invoking through it, so the legacy interface cannot be removed without touching ringhash again. Fix: type `endpointState.balancer` as `endpointsharding.ChildState` (or store `childState.ExitIdle` as a `func()`), call it directly in `updatePickerLocked`/`picker.Pick`, update `picker_test.go`'s fake accordingly, and delete `endpointsharding.ExitIdler`.

## C6

Target: branch `evalon/grpc-go-en-926a7310` (commit `a6fc355a`) of [kaitranntt-evals/grpc-go-endpointsharding-decouple-locking](https://github.com/kaitranntt-evals/grpc-go-endpointsharding-decouple-locking), checked out at `~/wt/926a7310`. `go1.25.7 linux/amd64`.

```sh
cd ~/repos/grpc-go && git fetch evalrepo evalon/grpc-go-en-926a7310 && git worktree add ~/wt/926a7310 evalrepo/evalon/grpc-go-en-926a7310
```

Code on the branch (`balancer/endpointsharding/endpointsharding.go`): `Close` only closes the children; there is no inhibition or closed flag consulted on the publication path, and `balancerWrapper.UpdateState` always publishes:

```go
func (es *endpointSharding) Close() {
	children := es.children.Load()
	for _, child := range children.All() {
		child.Close()
	}
}

func (bw *balancerWrapper) UpdateState(state balancer.State) {
	bw.es.mu.Lock()
	defer bw.es.mu.Unlock()
	bw.childState.State = state
	if state.ConnectivityState == connectivity.Idle && !bw.es.esOpts.DisableAutoReconnect {
		bw.ExitIdle()
	}
	bw.es.updateStateLocked()
}
```

(`bw.isClosed` exists but is only checked in `exitIdle`.) Probe (`verify/repro/c6_close_callbacks_test.go`, package `endpointsharding_test`): a recording parent `ClientConn` is passed to `NewBalancer`; two fake children report `Shutdown` synchronously from their `Close` (as during child closure) and, after `Close` has returned, the test calls `UpdateState` on the retained child `ClientConn`s:

```sh
cd ~/wt/926a7310 && cp ~/repos/grpc-go/verify/repro/c6_close_callbacks_test.go balancer/endpointsharding/ && go test ./balancer/endpointsharding -run 'TestC6_' -tags repro -race -count=1 -v; rm balancer/endpointsharding/c6_*_test.go
```

```console
=== RUN   TestC6_ChildCallbacksDuringAndAfterClose
    c6_close_callbacks_test.go:83: parent UpdateState calls after initial configuration: 1
    c6_close_callbacks_test.go:87: parent UpdateState calls made while Close() was closing children: 2
    c6_close_callbacks_test.go:90:   last publication during Close: agg=TRANSIENT_FAILURE children=2
    c6_close_callbacks_test.go:91: child state callbacks during child closure reached the parent's UpdateState (2 calls)
    c6_close_callbacks_test.go:102: parent UpdateState calls made by child callbacks after Close() returned: 2
    c6_close_callbacks_test.go:105:   last publication after Close: agg=CONNECTING children=2
    c6_close_callbacks_test.go:106: child state callbacks after Close returned reached the parent's UpdateState (2 calls)
--- FAIL: TestC6_ChildCallbacksDuringAndAfterClose (0.00s)
FAIL
FAIL	google.golang.org/grpc/balancer/endpointsharding	0.013s
```

Control: same file against the audited reference implementation (`~/repos/grpc-go`, whose `Close` calls `inhibitUpdatesFromChildren()` and never re-enables publication):

```sh
cd ~/repos/grpc-go && cp verify/repro/c6_close_callbacks_test.go balancer/endpointsharding/ && go test ./balancer/endpointsharding -run 'TestC6_' -tags repro -race -count=1 -v; rm balancer/endpointsharding/c6_*_test.go
```

```console
    c6_close_callbacks_test.go:83: parent UpdateState calls after initial configuration: 1
    c6_close_callbacks_test.go:87: parent UpdateState calls made while Close() was closing children: 0
    c6_close_callbacks_test.go:102: parent UpdateState calls made by child callbacks after Close() returned: 0
--- PASS: TestC6_ChildCallbacksDuringAndAfterClose (0.00s)
```

Same file against the task's base commit `bf9e7cd3` (`~/wt/base`): identical to the target branch (2 calls during Close, 2 after), so the branch inherited the behavior rather than introducing it, but did not add the suppression the reference implementation has.

Verdict: CONFIRMED (both points: during child closure and after `Close` returned). Impact: measured at the endpointsharding boundary, a closed endpointsharding balancer keeps publishing pickers — during shutdown it publishes an aggregate `TRANSIENT_FAILURE` built from children's shutdown states, and any late child callback (e.g. a pick-first child's SubConn transition delivered after the parent was closed) publishes a fresh picker (`CONNECTING`, 2 children) to a parent that considers this balancer gone. Consumers that own the wrapping (ringhash, priority, xDS cluster-resolver, or the `gracefulswitch` wrapper) must discard these; a consumer that does not will act on stale state from a dead child (ringhash, for example, would rebuild its ring and call `ExitIdle` on closed children). Fix: set a permanent inhibit/closed flag under `es.mu` in `Close` before closing children and return early from `balancerWrapper.UpdateState`/`updateStateLocked` when it is set (as the reference does).
