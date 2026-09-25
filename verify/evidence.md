## Setup shared by all sections

Audit run `v-4d987d4d`. Main checkout `~/repos/grpc-go` on branch `verify/grpc-go-endpointsharding-decouple-locking-v-4d987d4d`, created from `origin/grpc-go-endpointsharding-decouple-locking-perfect` (HEAD `10830332 test(endpointsharding): bound worker join in TestSameChildMutualExclusion`). Every claim targets a branch of `https://github.com/kaitranntt-evals/grpc-go-endpointsharding-decouple-locking`, fetched via a second remote and checked out as a detached worktree `~/wt/<8-char id>`:

```sh
cd ~/repos/grpc-go
git remote add evalrepo https://github.com/kaitranntt-evals/grpc-go-endpointsharding-decouple-locking.git
git fetch evalrepo
for b in ec452674 7342a4ed 0799fdea d1f27009 f01d9196 b9ec3844 74ae6210 ebd19ec9 c58d8af6 8e9bfa2b 59bf0e19 8e09516b b936e282 00a0b773 735d3aa5; do
  git worktree add --detach ~/wt/$b evalrepo/evalon/grpc-go-en-$b
done
git worktree add --detach ~/wt/base bf9e7cd3430df40d0732ba42eb88bd5f2cc63407   # earlier coarse-lock implementation
git worktree add --detach ~/wt/7342clean d01075385ecb219bc2539f10a09d353e1708c228  # second clean copy of 7342a4ed for mutation runs
```

Worktree HEADs: ec452674=`52c46e84`, 7342a4ed=`d0107538`, 0799fdea=`fdb7ae26`, d1f27009=`1fa2c0a3`, f01d9196=`26c2b9f2`, b9ec3844=`5371ad8d`, 74ae6210=`91c34ac4`, ebd19ec9=`e19bdf00`, c58d8af6=`8b0298fa`, 8e9bfa2b=`90c37bdd`, 59bf0e19=`a238caf8`, 8e09516b=`b4b447df`, b936e282=`621cebcd`, 00a0b773=`7b20ea46`, 735d3aa5=`2c8acd87`, base=`bf9e7cd3`.

The eval fixture archive was extracted to `~/eval_tests/tests/eval_endpointsharding_test.go` and used byte-exact (a byte-exact copy is committed as `verify/repro/c5_missing_coverage/eval_endpointsharding_test.go.txt` (the `.txt` suffix only keeps `go vet ./...` from compiling it in place); `cmp` against the extracted file reports no difference). Go toolchain: the one in `/usr/local/go` used by the environment; all test runs use `-race`. Goroutine dumps below have pointer arguments elided (`(0x...)` removed) for readability; nothing else is edited.

## C1

**Target:** `evalon/grpc-go-en-ec452674` (`~/wt/ec452674`). **Verdict: REFUTED** — at least one changed test observes B's completion while A is held and releases A only afterwards.

The changed test is `TestEndpointShardingChildExitIdleWhileOtherChildUpdateBlocked` in `balancer/endpointsharding/endpointsharding_child_ext_test.go` (lines 97–193). Held operation "A" = child `addr-b`'s `UpdateClientConnState`; distinct operation "B" = `ExitIdle` on child `addr-a`. Relevant assertion text:

```go
// child addr-b signals from INSIDE its UpdateClientConnState, then blocks:
if addr == addrB && blockChildB.Load() {
    childBBlocked <- struct{}{}
    select {
    case <-unblockChildB:
    case <-ctx.Done():
    }
}
...
ExitIdle: func(bd *stub.BalancerData) { ...; exitIdleCalled <- addr },   // signal is the configured child callback itself
...
select {                       // operation-specific indication that A is held
case <-childBBlocked:
case <-ctx.Done():
    t.Fatalf("Timeout waiting for child %q to start processing its update", addrB)
}
childA.ExitIdle()
select {                       // observe B's actual completion (the stub's ExitIdle ran) while A is still held
case got := <-exitIdleCalled:
    if got != addrA { t.Fatalf(...) }
case <-ctx.Done():
    t.Fatalf("Timeout waiting for ExitIdle on child %q while the update of child %q is blocked", addrA, addrB)
}
unblockChildBFn()              // A released only after the observation (or, on failure, via `defer unblockChildBFn()`)
```

Demonstration run on the target branch:

```sh
cd ~/wt/ec452674
go test ./balancer/endpointsharding -run 'Test/EndpointShardingChildExitIdleWhileOtherChildUpdateBlocked$' -race -count=3 -v
```

```console
=== RUN   Test/EndpointShardingChildExitIdleWhileOtherChildUpdateBlocked
--- PASS: Test (0.00s)
    --- PASS: Test/EndpointShardingChildExitIdleWhileOtherChildUpdateBlocked (0.00s)
(x3)
PASS
ok  	google.golang.org/grpc/balancer/endpointsharding	1.021s
```

Control (does the test actually detect the coarse lock?): the same file with only line 174 changed from `childA.ExitIdle()` to `childA.Balancer.ExitIdle()` (the earlier API; `diff` shows exactly that one line) was dropped into the coarse-lock base worktree as `c1_adapted_ext_test.go`:

```sh
cd ~/wt/base   # bf9e7cd3
go test ./balancer/endpointsharding -run 'Test/EndpointShardingChildExitIdleWhileOtherChildUpdateBlocked$' -race -count=3 -v -timeout 90s
```

```console
    c1_adapted_ext_test.go:181: Timeout waiting for ExitIdle on child "addr-a" while the update of child "addr-b" is blocked
--- FAIL: Test (10.05s)
    c1_adapted_ext_test.go:181: Timeout waiting for ExitIdle on child "addr-a" while the update of child "addr-b" is blocked
--- FAIL: Test (10.00s)
    c1_adapted_ext_test.go:181: Timeout waiting for ExitIdle on child "addr-a" while the update of child "addr-b" is blocked
--- FAIL: Test (10.00s)
FAIL	google.golang.org/grpc/balancer/endpointsharding	30.070s
```

3/3 runs fail deterministically at the progress assertion (and the test then terminates cleanly because `defer unblockChildBFn()` releases child B before `defer es.Close()`). On the shared-deadline question: child B's block and the progress assertion share `ctx`, but the test goroutine is already parked in the `select` when `ctx.Done()` closes, so it is woken on the `ctx.Done()` case — a late `ExitIdle` completion cannot satisfy the assertion, as the 3/3 failures show. Hence the regression has an operation-specific hold indication, observes B's actual completion (the configured synchronous `ExitIdle` callback) while A is held, and releases A only after that observation or on the failed assertion. The claim's precondition ("no such regression") does not hold.

## C2

**Verdict: CONFIRMED on all 12 branches.** Method (identical on every branch): the natural ExitIdle-while-another-child-is-blocked test was run once unmodified (baseline PASS), then an env-gated stall was injected into `balancerWrapper.updateClientConnState` so that the n-th and later child configuration calls sleep 120 s *while still holding the per-child lock* (`bw.mu`), and the test was rerun with `-timeout 45s`. In every case: the test's bounded wait times out → `t.Fatal` → the deferred / `t.Cleanup` `Close` runs anyway → `endpointSharding.Close` → `balancerWrapper.close` → `sync.Mutex.Lock` on the stalled child's lock and never returns (the goroutine dump at 45 s shows the cleanup goroutine in `sync.Mutex.Lock` and the worker in `sleep` inside `updateClientConnState`). No branch skips `Close` after an unsuccessful join. Script (committed): `verify/repro/c2_close_without_join/c2_stall_repro.sh <worktree> '<-run regex>' [EVAL_STALL_AT]`; it prints the baseline, the failure lines, the blocked cleanup goroutine and the stalled worker, then reverts the injection. The injected diff is:

```diff
 func (bw *balancerWrapper) updateClientConnState(ccs balancer.ClientConnState) error {
 	bw.mu.Lock()
 	defer bw.mu.Unlock()
-	return bw.child.UpdateClientConnState(ccs)
+	err := bw.child.UpdateClientConnState(ccs)
+	evalMaybeStall()   // EVAL_STALL_AT=n: n-th and later calls sleep 120s here, lock still held
+	return err
 }
```

Cleanup shape found by tracing each test (line numbers are in the branch's test file): every one registers `Close` unconditionally (`defer es.Close()` / `defer b.Close()` / `t.Cleanup(b.Close)`), most also register an unblock/release, and the join of the update worker — where present — is `select { case <-updateDone: ...; case <-ctx.Done(): t.Fatal(...) }` or `awaitEvent(...)`, i.e. bounded but with `Close` still executing after the bound is exceeded. Per-branch runs follow.

### C2 — [evalon/grpc-go-en-7342a4ed](https://github.com/kaitranntt-evals/grpc-go-endpointsharding-decouple-locking/tree/evalon/grpc-go-en-7342a4ed) (HEAD `d0107538`) — CONFIRMED

Test traced: `TestEndpointShardingExitIdleWhileOtherChildBlocked` in `balancer/endpointsharding/endpointsharding_child_ext_test.go`. Cleanup shape: `defer es.Close()` (l.175) then `defer unblock()` (l.178); join `case <-updateDone` / `case <-ctx.Done(): t.Fatalf(...)` (l.211–216).

```sh
verify/repro/c2_close_without_join/c2_stall_repro.sh ~/wt/7342a4ed 'Test/EndpointShardingExitIdleWhileOtherChildBlocked$' 1
```

```console
### baseline (no stall):
ok  	google.golang.org/grpc/balancer/endpointsharding	1.031s
### EVAL_STALL_AT=1 go test ./balancer/endpointsharding -run 'Test/EndpointShardingExitIdleWhileOtherChildBlocked$' -race -count=1 -v -timeout 45s
exit=1
    endpointsharding_child_ext_test.go:216: Timed out waiting for UpdateClientConnState() to return
panic: test timed out after 45s
FAIL	google.golang.org/grpc/balancer/endpointsharding	45.119s
FAIL
### cleanup goroutine blocked in Close:
goroutine 10 [sync.Mutex.Lock]:
google.golang.org/grpc/balancer/endpointsharding.(*balancerWrapper).close
google.golang.org/grpc/balancer/endpointsharding.(*endpointSharding).Close-range1(...)
google.golang.org/grpc/balancer/endpointsharding.(*endpointSharding).Close.(*EndpointMap[...]).All.func1(...)
google.golang.org/grpc/balancer/endpointsharding.(*endpointSharding).Close
testing.(*common).FailNow
testing.(*common).Fatalf
### stalled worker still holding the child lock:
goroutine 11 [sleep]:
google.golang.org/grpc/balancer/endpointsharding.evalMaybeStall()
google.golang.org/grpc/balancer/endpointsharding.(*balancerWrapper).updateClientConnState
```

### C2 — [evalon/grpc-go-en-0799fdea](https://github.com/kaitranntt-evals/grpc-go-endpointsharding-decouple-locking/tree/evalon/grpc-go-en-0799fdea) (HEAD `fdb7ae26`) — CONFIRMED

Test traced: `TestEndpointShardingExitIdleNotBlockedByOtherChild` in `balancer/endpointsharding/endpointsharding_ext_test.go`. Cleanup shape: `defer es.Close()` (l.417) then `defer unblock()` (l.421); join `case <-updateDone` / `case <-ctx.Done(): t.Fatal(...)` (l.467–469).

```sh
verify/repro/c2_close_without_join/c2_stall_repro.sh ~/wt/0799fdea 'Test/EndpointShardingExitIdleNotBlockedByOtherChild$' 1
```

```console
### baseline (no stall):
ok  	google.golang.org/grpc/balancer/endpointsharding	1.032s
### EVAL_STALL_AT=1 go test ./balancer/endpointsharding -run 'Test/EndpointShardingExitIdleNotBlockedByOtherChild$' -race -count=1 -v -timeout 45s
exit=1
    endpointsharding_ext_test.go:453: Timed out waiting for the blocking child to receive its update
panic: test timed out after 45s
FAIL	google.golang.org/grpc/balancer/endpointsharding	45.102s
FAIL
### cleanup goroutine blocked in Close:
goroutine 26 [sync.Mutex.Lock]:
google.golang.org/grpc/balancer/endpointsharding.(*balancerWrapper).close
google.golang.org/grpc/balancer/endpointsharding.(*endpointSharding).Close-range1(...)
google.golang.org/grpc/balancer/endpointsharding.(*endpointSharding).Close.(*EndpointMap[...]).All.func1(...)
google.golang.org/grpc/balancer/endpointsharding.(*endpointSharding).Close
testing.(*common).FailNow
testing.(*common).Fatal
### stalled worker still holding the child lock:
goroutine 27 [sleep]:
google.golang.org/grpc/balancer/endpointsharding.evalMaybeStall()
google.golang.org/grpc/balancer/endpointsharding.(*balancerWrapper).updateClientConnState
```

### C2 — [evalon/grpc-go-en-d1f27009](https://github.com/kaitranntt-evals/grpc-go-endpointsharding-decouple-locking/tree/evalon/grpc-go-en-d1f27009) (HEAD `1fa2c0a3`) — CONFIRMED

Test traced: `TestExitIdleWhileOtherChildUpdateBlocked` in `balancer/endpointsharding/endpointsharding_child_ext_test.go`. Cleanup shape: `defer es.Close()` (l.130) then `defer unblockChild()` (l.132); join `case <-updateDone` / `case <-ctx.Done(): t.Fatal(...)` (l.173–178).

```sh
verify/repro/c2_close_without_join/c2_stall_repro.sh ~/wt/d1f27009 'Test/ExitIdleWhileOtherChildUpdateBlocked$' 3
```

```console
### baseline (no stall):
ok  	google.golang.org/grpc/balancer/endpointsharding	1.030s
### EVAL_STALL_AT=3 go test ./balancer/endpointsharding -run 'Test/ExitIdleWhileOtherChildUpdateBlocked$' -race -count=1 -v -timeout 45s
exit=1
    endpointsharding_child_ext_test.go:154: Timeout waiting for the child to start processing the configuration update
panic: test timed out after 45s
FAIL	google.golang.org/grpc/balancer/endpointsharding	45.032s
FAIL
### cleanup goroutine blocked in Close:
goroutine 23 [sync.Mutex.Lock]:
google.golang.org/grpc/balancer/endpointsharding.(*balancerWrapper).close
google.golang.org/grpc/balancer/endpointsharding.(*endpointSharding).Close-range1(...)
google.golang.org/grpc/balancer/endpointsharding.(*endpointSharding).Close.(*EndpointMap[...]).All.func1(...)
google.golang.org/grpc/balancer/endpointsharding.(*endpointSharding).Close
testing.(*common).FailNow
testing.(*common).Fatal
### stalled worker still holding the child lock:
goroutine 24 [sleep]:
google.golang.org/grpc/balancer/endpointsharding.evalMaybeStall()
google.golang.org/grpc/balancer/endpointsharding.(*balancerWrapper).updateClientConnState
```

### C2 — [evalon/grpc-go-en-f01d9196](https://github.com/kaitranntt-evals/grpc-go-endpointsharding-decouple-locking/tree/evalon/grpc-go-en-f01d9196) (HEAD `26c2b9f2`) — CONFIRMED

Test traced: `TestExitIdleDuringOtherChildUpdate` in `balancer/endpointsharding/concurrency_ext_test.go`. Cleanup shape: `t.Cleanup(b.Close)` (l.138) then `t.Cleanup(unblock)` (l.140); no join of the update goroutine before Close.

```sh
verify/repro/c2_close_without_join/c2_stall_repro.sh ~/wt/f01d9196 'Test/ExitIdleDuringOtherChildUpdate/autoReconnect=false$' 3
```

```console
### baseline (no stall):
ok  	google.golang.org/grpc/balancer/endpointsharding	1.023s
### EVAL_STALL_AT=3 go test ./balancer/endpointsharding -run 'Test/ExitIdleDuringOtherChildUpdate/autoReconnect=false$' -race -count=1 -v -timeout 45s
exit=1
    concurrency_ext_test.go:175: Timed out waiting for child balancer
panic: test timed out after 45s
FAIL	google.golang.org/grpc/balancer/endpointsharding	45.117s
FAIL
### cleanup goroutine blocked in Close:
goroutine 40 [sync.Mutex.Lock]:
google.golang.org/grpc/balancer/endpointsharding.(*balancerWrapper).close
google.golang.org/grpc/balancer/endpointsharding.(*endpointSharding).Close-range1(...)
google.golang.org/grpc/balancer/endpointsharding.(*endpointSharding).Close.(*EndpointMap[...]).All.func1(...)
google.golang.org/grpc/balancer/endpointsharding.(*endpointSharding).Close
testing.tRunner.func2()
### stalled worker still holding the child lock:
goroutine 41 [sleep]:
google.golang.org/grpc/balancer/endpointsharding.evalMaybeStall()
google.golang.org/grpc/balancer/endpointsharding.(*balancerWrapper).updateClientConnState
```

### C2 — [evalon/grpc-go-en-b9ec3844](https://github.com/kaitranntt-evals/grpc-go-endpointsharding-decouple-locking/tree/evalon/grpc-go-en-b9ec3844) (HEAD `5371ad8d`) — CONFIRMED

Test traced: `TestExitIdleDuringOtherChildUpdate` in `balancer/endpointsharding/endpointsharding_concurrency_test.go`. Cleanup shape: `t.Cleanup(b.Close)` registered in `newStubSharder` (l.65); `t.Cleanup(func(){ unblock(); awaitEvent(t, ctx, updateDone, ...) })` (l.157–160) — the cleanup join is bounded and fatal on timeout, but the earlier-registered `b.Close` cleanup still runs afterwards.

```sh
verify/repro/c2_close_without_join/c2_stall_repro.sh ~/wt/b9ec3844 'Test/ExitIdleDuringOtherChildUpdate/autoReconnect=false$' 3
```

```console
### baseline (no stall):
ok  	google.golang.org/grpc/balancer/endpointsharding	1.045s
### EVAL_STALL_AT=3 go test ./balancer/endpointsharding -run 'Test/ExitIdleDuringOtherChildUpdate/autoReconnect=false$' -race -count=1 -v -timeout 45s
exit=1
    endpointsharding_concurrency_test.go:185: Timed out waiting for resolver update completion
    endpointsharding_concurrency_test.go:159: Timed out waiting for resolver update completion
panic: test timed out after 45s
FAIL	google.golang.org/grpc/balancer/endpointsharding	45.065s
FAIL
### cleanup goroutine blocked in Close:
goroutine 24 [sync.Mutex.Lock]:
google.golang.org/grpc/balancer/endpointsharding.(*balancerWrapper).close
google.golang.org/grpc/balancer/endpointsharding.(*endpointSharding).Close-range1(...)
google.golang.org/grpc/balancer/endpointsharding.(*endpointSharding).Close.(*EndpointMap[...]).All.func1(...)
google.golang.org/grpc/balancer/endpointsharding.(*endpointSharding).Close
goroutine 5 [sync.Mutex.Lock]:
### stalled worker still holding the child lock:
goroutine 25 [sleep]:
google.golang.org/grpc/balancer/endpointsharding.evalMaybeStall()
google.golang.org/grpc/balancer/endpointsharding.(*balancerWrapper).updateClientConnState
goroutine 5 [sync.Mutex.Lock]:
```

### C2 — [evalon/grpc-go-en-74ae6210](https://github.com/kaitranntt-evals/grpc-go-endpointsharding-decouple-locking/tree/evalon/grpc-go-en-74ae6210) (HEAD `91c34ac4`) — CONFIRMED

Test traced: `TestEndpointShardingIndependentExitIdle` in `balancer/endpointsharding/concurrency_test.go`. Cleanup shape: `defer b.Close()` (l.96) then `defer unblock()` (l.98); join `case <-done` / `case <-ctx.Done(): t.Fatal(...)` (l.133–139).

```sh
verify/repro/c2_close_without_join/c2_stall_repro.sh ~/wt/74ae6210 'Test/EndpointShardingIndependentExitIdle$' 3
```

```console
### baseline (no stall):
ok  	google.golang.org/grpc/balancer/endpointsharding	1.034s
### EVAL_STALL_AT=3 go test ./balancer/endpointsharding -run 'Test/EndpointShardingIndependentExitIdle$' -race -count=1 -v -timeout 45s
exit=1
    concurrency_test.go:121: Timed out waiting for blocked child update
panic: test timed out after 45s
FAIL	google.golang.org/grpc/balancer/endpointsharding	45.131s
FAIL
### cleanup goroutine blocked in Close:
goroutine 20 [sync.Mutex.Lock]:
google.golang.org/grpc/balancer/endpointsharding.(*balancerWrapper).close
google.golang.org/grpc/balancer/endpointsharding.(*endpointSharding).Close-range1(...)
google.golang.org/grpc/balancer/endpointsharding.(*endpointSharding).Close.(*EndpointMap[...]).All.func1(...)
google.golang.org/grpc/balancer/endpointsharding.(*endpointSharding).Close
testing.(*common).FailNow
testing.(*common).Fatal
### stalled worker still holding the child lock:
goroutine 21 [sleep]:
google.golang.org/grpc/balancer/endpointsharding.evalMaybeStall()
google.golang.org/grpc/balancer/endpointsharding.(*balancerWrapper).updateClientConnState
```

### C2 — [evalon/grpc-go-en-ebd19ec9](https://github.com/kaitranntt-evals/grpc-go-endpointsharding-decouple-locking/tree/evalon/grpc-go-en-ebd19ec9) (HEAD `e19bdf00`) — CONFIRMED

Test traced: `TestExitIdleWhileOtherChildUpdating` in `balancer/endpointsharding/concurrency_test.go`. Cleanup shape: `defer b.Close()` (l.151) then `defer releaseUpdate()` (l.152) and a deferred bounded wait for `updateDone` that Fatalfs on timeout (l.177); Close still runs after that Fatalf.

```sh
verify/repro/c2_close_without_join/c2_stall_repro.sh ~/wt/ebd19ec9 'Test/ExitIdleWhileOtherChildUpdating/explicit$' 3
```

```console
### baseline (no stall):
ok  	google.golang.org/grpc/balancer/endpointsharding	1.018s
### EVAL_STALL_AT=3 go test ./balancer/endpointsharding -run 'Test/ExitIdleWhileOtherChildUpdating/explicit$' -race -count=1 -v -timeout 45s
exit=1
    concurrency_test.go:185: Timed out waiting for blocked child update
    concurrency_test.go:177: Timed out waiting for resolver update completion during cleanup
panic: test timed out after 45s
FAIL	google.golang.org/grpc/balancer/endpointsharding	45.078s
FAIL
### cleanup goroutine blocked in Close:
goroutine 7 [sync.Mutex.Lock]:
google.golang.org/grpc/balancer/endpointsharding.(*balancerWrapper).close
google.golang.org/grpc/balancer/endpointsharding.(*endpointSharding).Close-range1(...)
google.golang.org/grpc/balancer/endpointsharding.(*endpointSharding).Close.(*EndpointMap[...]).All.func1(...)
google.golang.org/grpc/balancer/endpointsharding.(*endpointSharding).Close
testing.(*common).FailNow
testing.(*common).Fatalf
### stalled worker still holding the child lock:
goroutine 8 [sleep]:
google.golang.org/grpc/balancer/endpointsharding.evalMaybeStall()
google.golang.org/grpc/balancer/endpointsharding.(*balancerWrapper).updateClientConnState
```

### C2 — [evalon/grpc-go-en-c58d8af6](https://github.com/kaitranntt-evals/grpc-go-endpointsharding-decouple-locking/tree/evalon/grpc-go-en-c58d8af6) (HEAD `8b0298fa`) — CONFIRMED

Test traced: `TestExitIdleIndependentOfOtherChildUpdate` in `balancer/endpointsharding/concurrency_test.go`. Cleanup shape: `defer b.Close()` (l.125) then `defer unblock()` (l.126); join bounded by ctx with `t.Fatal` (l.154–155).

```sh
verify/repro/c2_close_without_join/c2_stall_repro.sh ~/wt/c58d8af6 'Test/ExitIdleIndependentOfOtherChildUpdate$' 3
```

```console
### baseline (no stall):
ok  	google.golang.org/grpc/balancer/endpointsharding	1.017s
### EVAL_STALL_AT=3 go test ./balancer/endpointsharding -run 'Test/ExitIdleIndependentOfOtherChildUpdate$' -race -count=1 -v -timeout 45s
exit=1
    concurrency_test.go:154: Timed out waiting for child balancer: context deadline exceeded
panic: test timed out after 45s
FAIL	google.golang.org/grpc/balancer/endpointsharding	45.132s
FAIL
### cleanup goroutine blocked in Close:
goroutine 10 [sync.Mutex.Lock]:
google.golang.org/grpc/balancer/endpointsharding.(*balancerWrapper).close
google.golang.org/grpc/balancer/endpointsharding.(*endpointSharding).Close-range1(...)
google.golang.org/grpc/balancer/endpointsharding.(*endpointSharding).Close.(*EndpointMap[...]).All.func1(...)
google.golang.org/grpc/balancer/endpointsharding.(*endpointSharding).Close
testing.(*common).FailNow
testing.(*common).Fatalf
goroutine 13 [sync.Mutex.Lock]:
### stalled worker still holding the child lock:
goroutine 11 [sleep]:
google.golang.org/grpc/balancer/endpointsharding.evalMaybeStall()
google.golang.org/grpc/balancer/endpointsharding.(*balancerWrapper).updateClientConnState
goroutine 13 [sync.Mutex.Lock]:
```

### C2 — [evalon/grpc-go-en-8e9bfa2b](https://github.com/kaitranntt-evals/grpc-go-endpointsharding-decouple-locking/tree/evalon/grpc-go-en-8e9bfa2b) (HEAD `90c37bdd`) — CONFIRMED

Test traced: `TestExitIdleDuringOtherChildUpdate` in `balancer/endpointsharding/concurrency_test.go`. Cleanup shape: `defer b.Close()` inside the subtest (l.122) plus a deferred bounded wait for the update that Fatalfs on timeout (l.147); Close still runs after that Fatalf.

```sh
verify/repro/c2_close_without_join/c2_stall_repro.sh ~/wt/8e9bfa2b 'Test/ExitIdleDuringOtherChildUpdate/autoReconnect=false$' 3
```

```console
### baseline (no stall):
ok  	google.golang.org/grpc/balancer/endpointsharding	1.020s
### EVAL_STALL_AT=3 go test ./balancer/endpointsharding -run 'Test/ExitIdleDuringOtherChildUpdate/autoReconnect=false$' -race -count=1 -v -timeout 45s
exit=1
    concurrency_test.go:155: Timed out waiting for blocked child's update
    concurrency_test.go:147: Timed out waiting for resolver update cleanup
panic: test timed out after 45s
FAIL	google.golang.org/grpc/balancer/endpointsharding	45.054s
FAIL
### cleanup goroutine blocked in Close:
goroutine 11 [sync.Mutex.Lock]:
google.golang.org/grpc/balancer/endpointsharding.(*balancerWrapper).close
google.golang.org/grpc/balancer/endpointsharding.(*endpointSharding).Close-range1(...)
google.golang.org/grpc/balancer/endpointsharding.(*endpointSharding).Close.(*EndpointMap[...]).All.func1(...)
google.golang.org/grpc/balancer/endpointsharding.(*endpointSharding).Close
testing.(*common).FailNow
testing.(*common).Fatalf
### stalled worker still holding the child lock:
goroutine 12 [sleep]:
google.golang.org/grpc/balancer/endpointsharding.evalMaybeStall()
google.golang.org/grpc/balancer/endpointsharding.(*balancerWrapper).updateClientConnState
```

### C2 — [evalon/grpc-go-en-59bf0e19](https://github.com/kaitranntt-evals/grpc-go-endpointsharding-decouple-locking/tree/evalon/grpc-go-en-59bf0e19) (HEAD `a238caf8`) — CONFIRMED

Test traced: `TestExitIdleDuringOtherChildUpdate` in `balancer/endpointsharding/endpointsharding_concurrency_test.go`. Cleanup shape: `t.Cleanup(b.Close)` (l.123) plus a cleanup wait for the parent update that Fatalfs on timeout (l.142); Close still runs after that Fatalf.

```sh
verify/repro/c2_close_without_join/c2_stall_repro.sh ~/wt/59bf0e19 'Test/ExitIdleDuringOtherChildUpdate/autoReconnect=false$' 3
```

```console
### baseline (no stall):
ok  	google.golang.org/grpc/balancer/endpointsharding	1.023s
### EVAL_STALL_AT=3 go test ./balancer/endpointsharding -run 'Test/ExitIdleDuringOtherChildUpdate/autoReconnect=false$' -race -count=1 -v -timeout 45s
exit=1
    endpointsharding_concurrency_test.go:150: Timed out waiting for blocked child update
    endpointsharding_concurrency_test.go:142: Timed out waiting for parent update cleanup
panic: test timed out after 45s
FAIL	google.golang.org/grpc/balancer/endpointsharding	45.123s
FAIL
### cleanup goroutine blocked in Close:
goroutine 24 [sync.Mutex.Lock]:
google.golang.org/grpc/balancer/endpointsharding.(*balancerWrapper).close
google.golang.org/grpc/balancer/endpointsharding.(*endpointSharding).Close-range1(...)
google.golang.org/grpc/balancer/endpointsharding.(*endpointSharding).Close.(*EndpointMap[...]).All.func1(...)
google.golang.org/grpc/balancer/endpointsharding.(*endpointSharding).Close
testing.tRunner.func2()
### stalled worker still holding the child lock:
goroutine 25 [sleep]:
google.golang.org/grpc/balancer/endpointsharding.evalMaybeStall()
google.golang.org/grpc/balancer/endpointsharding.(*balancerWrapper).updateClientConnState
```

### C2 — [evalon/grpc-go-en-8e09516b](https://github.com/kaitranntt-evals/grpc-go-endpointsharding-decouple-locking/tree/evalon/grpc-go-en-8e09516b) (HEAD `b4b447df`) — CONFIRMED

Test traced: `TestChildExitIdleDuringBlockedUpdate` in `balancer/endpointsharding/endpointsharding_concurrency_test.go`. Cleanup shape: `defer b.Close()` (l.127) plus a deferred bounded wait for the configuration update that Fatalfs on timeout (l.152); Close still runs after that Fatalf.

```sh
verify/repro/c2_close_without_join/c2_stall_repro.sh ~/wt/8e09516b 'Test/ChildExitIdleDuringBlockedUpdate/autoReconnect=false$' 3
```

```console
### baseline (no stall):
ok  	google.golang.org/grpc/balancer/endpointsharding	1.034s
### EVAL_STALL_AT=3 go test ./balancer/endpointsharding -run 'Test/ChildExitIdleDuringBlockedUpdate/autoReconnect=false$' -race -count=1 -v -timeout 45s
exit=1
    endpointsharding_concurrency_test.go:154: Timed out waiting for blocked child's configuration update
    endpointsharding_concurrency_test.go:152: Timed out waiting for configuration update to finish
panic: test timed out after 45s
FAIL	google.golang.org/grpc/balancer/endpointsharding	45.048s
FAIL
### cleanup goroutine blocked in Close:
goroutine 24 [sync.Mutex.Lock]:
google.golang.org/grpc/balancer/endpointsharding.(*balancerWrapper).close
google.golang.org/grpc/balancer/endpointsharding.(*endpointSharding).Close-range1(...)
google.golang.org/grpc/balancer/endpointsharding.(*endpointSharding).Close.(*EndpointMap[...]).All.func1(...)
google.golang.org/grpc/balancer/endpointsharding.(*endpointSharding).Close
testing.(*common).FailNow
testing.(*common).Fatalf
### stalled worker still holding the child lock:
goroutine 25 [sleep]:
google.golang.org/grpc/balancer/endpointsharding.evalMaybeStall()
google.golang.org/grpc/balancer/endpointsharding.(*balancerWrapper).updateClientConnState
```

### C2 — [evalon/grpc-go-en-b936e282](https://github.com/kaitranntt-evals/grpc-go-endpointsharding-decouple-locking/tree/evalon/grpc-go-en-b936e282) (HEAD `621cebcd`) — CONFIRMED

Test traced: `TestEndpointShardingIndependentExitIdle` in `balancer/endpointsharding/endpointsharding_concurrency_test.go`. Cleanup shape: `t.Cleanup(b.Close)` registered in `newStubSharder` (l.65) then `t.Cleanup(release)` (l.120); join `case err := <-done` / `case <-ctx.Done(): t.Fatal(...)` (l.160–166).

```sh
verify/repro/c2_close_without_join/c2_stall_repro.sh ~/wt/b936e282 'Test/EndpointShardingIndependentExitIdle/explicit$' 3
```

```console
### baseline (no stall):
ok  	google.golang.org/grpc/balancer/endpointsharding	1.022s
### EVAL_STALL_AT=3 go test ./balancer/endpointsharding -run 'Test/EndpointShardingIndependentExitIdle/explicit$' -race -count=1 -v -timeout 45s
exit=1
    endpointsharding_concurrency_test.go:166: Timed out waiting for UpdateClientConnState to complete
panic: test timed out after 45s
FAIL	google.golang.org/grpc/balancer/endpointsharding	45.119s
FAIL
### cleanup goroutine blocked in Close:
goroutine 11 [sync.Mutex.Lock]:
google.golang.org/grpc/balancer/endpointsharding.(*balancerWrapper).close
google.golang.org/grpc/balancer/endpointsharding.(*endpointSharding).Close-range1(...)
google.golang.org/grpc/balancer/endpointsharding.(*endpointSharding).Close.(*EndpointMap[...]).All.func1(...)
google.golang.org/grpc/balancer/endpointsharding.(*endpointSharding).Close
testing.tRunner.func2()
### stalled worker still holding the child lock:
goroutine 12 [sleep]:
google.golang.org/grpc/balancer/endpointsharding.evalMaybeStall()
google.golang.org/grpc/balancer/endpointsharding.(*balancerWrapper).updateClientConnState
```


## C3

**Target:** `evalon/grpc-go-en-00a0b773` (`~/wt/00a0b773`). **Verdict: CONFIRMED.**

The delivered `balancer/ringhash/ringhash.go` replaced the base's `endpointsharding.ExitIdler` with `balancer.ExitIdler` (`git diff bf9e7cd3 -- balancer/ringhash/ringhash.go`):

```diff
-		var idleBalancer endpointsharding.ExitIdler
+		var idleBalancer balancer.ExitIdler
...
-	balancer endpointsharding.ExitIdler
+	// balancer is used to request the child balancer for this endpoint to exit
+	// the IDLE state. It is the endpointsharding.ChildState of the endpoint.
+	balancer balancer.ExitIdler
```

`balancer/balancer.go` (lines 374–376) declares `// Deprecated: All balancers must implement this interface. This interface will be removed in a future release.` on `type ExitIdler interface`. `scripts/vet.sh` runs `staticcheck -checks 'all' ./... >"${SC_OUT}"` (line 129) and then fails on any `(SA1019)` line not matched by its fixed-string allowlist (`noret_grep "(SA1019)" "${SC_OUT}" | not grep -Fv '...'`, lines 168–200); the allowlist contains no `ExitIdler` entry (`grep -n ExitIdler scripts/vet.sh` → nothing). Staticcheck was installed from the pinned `test/tools/go.mod` (`honnef.co/go/tools v0.7.0`):

```sh
cd ~/wt/00a0b773/test/tools && go install honnef.co/go/tools/cmd/staticcheck && ~/go/bin/staticcheck --version
# staticcheck 2026.1 (v0.7.0)
cd ~/wt/00a0b773 && ~/go/bin/staticcheck -checks 'all' ./... > /tmp/c3_sc_full.txt; grep ExitIdler /tmp/c3_sc_full.txt
```

```console
balancer/ringhash/ringhash.go:271:20: balancer.ExitIdler is deprecated: All balancers must implement this interface. This interface will be removed in a future release.  (SA1019)
balancer/ringhash/ringhash.go:404:11: balancer.ExitIdler is deprecated: All balancers must implement this interface. This interface will be removed in a future release.  (SA1019)
```

Applying vet.sh's allowlist to the full `./...` output (`grep "(SA1019)" /tmp/c3_sc_full.txt | grep -Fv -f <allowlist extracted from scripts/vet.sh>`) leaves exactly those two lines, so `scripts/vet.sh` fails. Committed repro `verify/repro/c3_staticcheck_exitidler/c3_staticcheck.sh ~/wt/00a0b773` output:

```console
staticcheck 2026.1 (v0.7.0)
### grep -n 'balancer.ExitIdler' balancer/ringhash/ringhash.go
271:		var idleBalancer balancer.ExitIdler
404:	balancer balancer.ExitIdler
### /home/ubuntu/go/bin/staticcheck -checks 'all' ./balancer/ringhash/...   (vet.sh runs: staticcheck -checks 'all' ./...)
balancer/ringhash/ringhash.go:271:20: balancer.ExitIdler is deprecated: All balancers must implement this interface. This interface will be removed in a future release.  (SA1019)
balancer/ringhash/ringhash.go:404:11: balancer.ExitIdler is deprecated: All balancers must implement this interface. This interface will be removed in a future release.  (SA1019)
balancer/ringhash/ringhash_test.go:687:2: addr.BalancerAttributes is deprecated: ...  (SA1019)
balancer/ringhash/ringhash_test.go:687:28: addr.BalancerAttributes is deprecated: ...  (SA1019)
### after vet.sh's SA1019 allowlist (the 'not grep -Fv' list in scripts/vet.sh):
balancer/ringhash/ringhash.go:271:20: balancer.ExitIdler is deprecated: All balancers must implement this interface. This interface will be removed in a future release.  (SA1019)
balancer/ringhash/ringhash.go:404:11: balancer.ExitIdler is deprecated: All balancers must implement this interface. This interface will be removed in a future release.  (SA1019)
RESULT: unallowed SA1019 diagnostics remain -> scripts/vet.sh would fail
```

Control: the same staticcheck on the base worktree's `./balancer/ringhash/` reports 0 `ExitIdler` diagnostics (`~/wt/base$ ~/go/bin/staticcheck -checks 'all' ./balancer/ringhash/ | grep -c ExitIdler` → `0`). Impact: the repository's `vet.sh` gate (run by `.github/workflows/testing.yml`) rejects the branch; the fix is to use the non-deprecated `endpointsharding.ExitIdler` (or `interface{ ExitIdle() }`) for the field/local type, as the base did.

## C4

**Target:** `evalon/grpc-go-en-7342a4ed` (clean worktree `~/wt/7342clean`, HEAD `d0107538`). **Verdict: CONFIRMED (both parts).**

`updateState` decides publication eligibility with `if es.inhibitChildUpdates.Load() { return }` and only afterwards takes `es.mu` to build the picker; `balancerWrapper.UpdateState` records `bw.childState.State` under `es.mu` and then calls `updateState()`. Neither step is ordered against `UpdateClientConnState`'s `inhibitChildUpdates.Store(true)` … `Store(false); es.updateState()` sequence. To exercise the two interleavings, two nil-by-default hooks were added (`verify/repro/c4_batch_boundary/c4_hooks.patch`): `evalAfterInhibitCheck()` right after the inhibit check in `updateState`, and `evalAfterRecordState(bw)` right after the state is recorded in `balancerWrapper.UpdateState`. The test `verify/repro/c4_batch_boundary/c4_batch_boundary_test.go.txt` (copied in as `c4_batch_boundary_test.go`; package `endpointsharding`) records every `ClientConn.UpdateState` publication and renders each child as `addr=<picker tag>/v<endpoint attribute version>`.

- *entry_boundary* (`TestC4_EntryBoundary`): child A's callback (`callback`, READY) passes the inhibit check and is paused; a version-2 batch starts and is held at its second child *before that child reports*; the callback is resumed while the batch is still held. It publishes a picker mixing batch-updated and pre-batch children.
- *completion_boundary* (`TestC4_CompletionBoundary`): a batch is held; child A's callback (`callback-READY`) records its state and is paused before the inhibit check; the batch is released and its consolidated publication already contains `A=callback-READY`; the callback is resumed with nothing else running. It publishes again, identical to the consolidated publication.

```sh
verify/repro/c4_batch_boundary/run_c4.sh ~/wt/7342clean
# == git apply c4_hooks.patch; cp c4_batch_boundary_test.go balancer/endpointsharding/; go test ./balancer/endpointsharding -run 'TestC4_' -race -count=1 -v
```

```console
=== RUN   TestC4_EntryBoundary
    c4_batch_boundary_test.go:135: initial publication: A=batch-v1/v1 B=batch-v1/v1
    c4_batch_boundary_test.go:177: publications after resuming callback (batch still held, 2 total):
    c4_batch_boundary_test.go:179:   [0] agg=CONNECTING children: A=batch-v1/v1 B=batch-v1/v1
    c4_batch_boundary_test.go:179:   [1] agg=READY children: A=callback/v2 B=batch-v2/v2
    c4_batch_boundary_test.go:191: ENTRY-BOUNDARY LEAK: mid-batch publication with partially updated children: A=callback/v2 B=batch-v2/v2
    c4_batch_boundary_test.go:203: consolidated publication after batch: A=batch-v2/v2 B=batch-v2/v2  (total publications 3)
--- PASS: TestC4_EntryBoundary (0.00s)
=== RUN   TestC4_CompletionBoundary
    c4_batch_boundary_test.go:289: consolidated publication: agg=READY A=callback-READY/v2 B=batch-v2/v2
    c4_batch_boundary_test.go:297:   [0] agg=CONNECTING children: A=batch-v1/v1 B=batch-v1/v1
    c4_batch_boundary_test.go:297:   [1] agg=READY children: A=callback-READY/v2 B=batch-v2/v2
    c4_batch_boundary_test.go:297:   [2] agg=READY children: A=callback-READY/v2 B=batch-v2/v2
    c4_batch_boundary_test.go:305: COMPLETION-BOUNDARY DUPLICATE: callback re-published state already in consolidated publication: A=callback-READY/v2 B=batch-v2/v2
--- PASS: TestC4_CompletionBoundary (0.00s)
PASS
ok  	google.golang.org/grpc/balancer/endpointsharding	1.015s
```

The entry-boundary leak's content depends on which child the batch processes first (endpoint-map order): another run printed `[1] agg=CONNECTING children: A=batch-v2/v2 B=batch-v1/v2` (child B's endpoint already replaced by the v2 endpoint while its state is still the v1 one) — either way a publication *during* the batch whose child states are only partially updated, which `UpdateClientConnState`'s inhibit is meant to prevent. `go test ./balancer/endpointsharding -run 'TestC4_' -race -count=20` passed in three consecutive invocations (the interleavings are forced, not raced). The tests themselves pass (they assert the leak/duplicate); a fixed implementation would make them fail at lines 187/300. Impact: a mid-batch parent publication exposes a picker with a stale/mixed set of children (e.g. an endpoint's new attributes paired with its pre-update state, or a partially applied endpoint set) to the channel for the duration of the batch; the duplicate publishes an identical picker twice, causing a redundant picker swap. Both require a child callback to race a resolver update — an ordinary event when subchannels change state while a resolver update is being applied.

## C5

**Target:** `evalon/grpc-go-en-7342a4ed` (`~/wt/7342a4ed` for the instrumented run, `~/wt/7342clean` for mutation runs). **Verdict: CONFIRMED — 2 of 4 parts held** (`child_configuration_error_continuation` and `same_child_serialization` are missing; `synchronous_resolver_error_callback` and `publication_count_observation` are covered).

Maintained tests on the branch: `balancer/endpointsharding/endpointsharding_test.go` (`TestRotateEndpoints`), `endpointsharding_ext_test.go` (`TestEndpointShardingBasic`, `TestEndpointShardingReconnectDisabled`, `TestEndpointShardingExitIdle`), `endpointsharding_child_ext_test.go` (`TestEndpointShardingExitIdleWhileOtherChildBlocked`, `TestEndpointShardingExitIdleAfterSynchronousIdle`, `TestEndpointShardingSingleUpdateAndAggregation`, `TestEndpointShardingConcurrentAttributeUpdatesAndReconnects`). None of the names in the claim (`TestSameChildMutualExclusion`, `TestClosedStateGuardCoverage`, `TestBatchUpdateChildErrorConsolidation`, `TestSynchronousLifecycleCallbackSafety`, `maintainedProbeCC`) exist on this branch; the nearest equivalents are the tests above. Baseline: `go test ./balancer/endpointsharding -run '^Test$' -race -count=1 -v` → `PASS`, `ok ... 1.114s`; archived fixture on the same tree: `go test ./balancer/endpointsharding -run 'TestEval_' -race -count=1 -v` → `PASS`, `ok ... 1.801s`.

**Part synchronous_resolver_error_callback — does not hold (covered).** Audit-only stderr logging (`~/wt/out/c5_instrumentation.patch`, not committed: an `evalInResolverError` flag set inside `balancerWrapper.resolverError`, checked in `balancerWrapper.UpdateState`) during the maintained suite:

```sh
cd ~/wt/7342a4ed && go test ./balancer/endpointsharding -run '^Test$' -race -count=1 -v 2>&1 | grep -n -B2 'EVAL: SYNC'
```

```console
53-    tlogger.go:129: WARNING resolver_wrapper.go:157 [core] [Channel #4] ccResolverWrapper: reporting error to cc: test error  (t=+18.12047ms)
54:EVAL: SYNC-CALLBACK-DURING-RESOLVERERROR child=0xc00026c5a0 state=TRANSIENT_FAILURE
55:EVAL: SYNC-CALLBACK-DURING-RESOLVERERROR child=0xc00026c6c0 state=TRANSIENT_FAILURE
    --- PASS: Test/EndpointShardingBasic (0.02s)
```

`TestEndpointShardingBasic` (`endpointsharding_ext_test.go:189`, `mr.CC().ReportError(errors.New("test error"))`) drives `endpointSharding.ResolverError`, during which both pickfirst children synchronously call back with TRANSIENT_FAILURE, and the test then asserts the resulting picker returns the resolver error. The synchronous callback during ResolverError is exercised.

**Part child_configuration_error_continuation — holds (missing).** Mutation `verify/repro/c5_missing_coverage/m3_child_error_abort.patch` makes `UpdateClientConnState` return on the first child error instead of continuing (`if err != nil { return err }` after `newChildren.Set`). The maintained suite does not notice; the archived fixture does:

```sh
verify/repro/c5_missing_coverage/run_c5.sh ~/wt/7342clean     # section "mutation m3_child_error_abort"
```

```console
### maintained suite: go test ./balancer/endpointsharding -run '^Test$' -race -count=3
      1 ok  	google.golang.org/grpc/balancer/endpointsharding	1.277s
### archived fixture: go test ./balancer/endpointsharding -run '^TestEval_' -race -count=1 -v
    eval_endpointsharding_test.go:2185: expected 3 children built despite child 2 error, got 2 (early abort defect)
    eval_endpointsharding_test.go:2191: expected endpoint 10.0.0.1 to receive configuration update despite earlier error, got 0
    eval_endpointsharding_test.go:2201: expected 3 child states in consolidated picker despite child error, got 0
--- FAIL: TestEval_BatchUpdateInhibition (0.00s)
    --- FAIL: TestEval_BatchUpdateInhibition/ChildErrorConsolidation (0.00s)
FAIL	google.golang.org/grpc/balancer/endpointsharding	0.808s
```

No maintained test returns an error from a child's `UpdateClientConnState`: every `onUpdateClientConnState` hook in `endpointsharding_child_ext_test.go` (lines 164, 253, 352, 420) and every stub `UpdateClientConnState` in `endpointsharding_ext_test.go` (lines 227, 308) returns `nil`, so nothing asserts continuation or the consolidated publication after a child error.

**Part same_child_serialization — holds (missing).** Mutation `m1_same_child_no_serialization.patch` releases `bw.mu` before calling the child in `updateClientConnState` (so `ExitIdle`/`Close`/`ResolverError` may overlap an in-flight update on the same child). Maintained suite still passes 3/3 under `-race`; the fixture's exclusion assertion fails:

```console
##### mutation m1_same_child_no_serialization
### maintained suite: go test ./balancer/endpointsharding -run '^Test$' -race -count=3
      1 ok  	google.golang.org/grpc/balancer/endpointsharding	1.278s
### archived fixture: go test ./balancer/endpointsharding -run '^TestEval_' -race -count=1 -v
    eval_endpointsharding_test.go:725: child ExitIdle entered concurrently while UpdateClientConnState was in flight
    eval_endpointsharding_test.go:765: observed concurrent overlapping calls on same child during update (maxSeen = 2)
--- FAIL: TestEval_SameChildMutualExclusion (0.73s)
    --- FAIL: TestEval_SameChildMutualExclusion/UpdateClientConnStateVsExitIdle (0.10s)
FAIL	google.golang.org/grpc/balancer/endpointsharding	0.802s
```

The maintained `TestEndpointShardingConcurrentAttributeUpdatesAndReconnects` does produce same-child contention (the audit `evalLock` logging printed 99 `EVAL: SAME-CHILD-CONTENTION` lines during it) but asserts nothing about exclusion, and `TestEndpointShardingExitIdleWhileOtherChildBlocked` holds one child while exercising a *different* child. No maintained test holds a child operation and asserts that a second operation on the same child is excluded.

**Part publication_count_observation — does not hold (covered).** The maintained recorder appends every publication (`recordingClientConn.UpdateState` appends to `r.states`; `recordedStates()` returns the full slice, `endpointsharding_child_ext_test.go:114–119`) and `TestEndpointShardingSingleUpdateAndAggregation` asserts `len(states) != i` → `Fatalf("Got %d state updates after %d UpdateClientConnState() calls, want %d")` (line 379–381). Mutation `m2_duplicate_batch_publication.patch` (a second `es.updateState()` at batch completion) is caught:

```console
##### mutation m2_duplicate_batch_publication
### maintained suite: go test ./balancer/endpointsharding -run '^Test$' -race -count=3
     12             endpointsharding_child_ext_test.go:381: Got 2 state updates after 1 UpdateClientConnState() calls, want 1
      3     --- FAIL: Test/EndpointShardingSingleUpdateAndAggregation (0.00s)
      1 FAIL	google.golang.org/grpc/balancer/endpointsharding	0.276s
```

No latest-state-only channel is used for a publication-count assertion on this branch.

Impact of the two missing parts: the branch's decoupled per-child locking and its "continue past a failing child and publish once" behavior are exactly what the change introduces, and neither is guarded by a maintained test — the M1 and M3 mutations (each a one-line regression of that behavior) ship green through `go test ./balancer/endpointsharding -race`.

## C6

**Target:** `evalon/grpc-go-en-735d3aa5` (`~/wt/735d3aa5`, HEAD `2c8acd87`); earlier coarse-lock implementation `~/wt/base` (`bf9e7cd3`). **Verdict: CONFIRMED (both parts).**

The claim names `TestDecoupledChildProgress`, which does not exist; the authored progress regression on this branch is `TestEndpointShardingExitIdleWhileOtherChildBlocked` in `balancer/endpointsharding/endpointsharding_ext_test.go` (lines 432–508). Relevant lines:

```go
434:	defer cancel()
438:	unblockB := make(chan struct{})
448:					<-unblockB                      // child B blocks here inside UpdateClientConnState (no ctx case)
460:	defer es.Close()                        // unconditional; no deferred close(unblockB), no bounded join
482:	childA.ExitIdle()
489:		t.Fatal("Timed out waiting for ExitIdle to be delivered to child A while child B's update is blocked")
497:	close(unblockB)                         // only on the success path, after the assertion
```

Committed repro: `verify/repro/c6_cleanup_hang/run_c6.sh ~/wt/735d3aa5 ~/wt/base` (Part 1 applies `c6_stall_exitidle.patch`; Part 2 copies the authored test file onto the base changing only `childA.ExitIdle()` → `childA.Balancer.ExitIdle()`; the script's `diff` confirms that is the only difference).

**Part cleanup_mechanism — holds.** On the claim branch itself, `balancerWrapper.exitIdle` was delayed 120 s (env `EVAL_STALL_EXITIDLE=1`) so the progress assertion times out while child B is still held on `unblockB`:

```sh
cd ~/wt/735d3aa5 && git apply verify/repro/c6_cleanup_hang/c6_stall_exitidle.patch
EVAL_STALL_EXITIDLE=1 go test ./balancer/endpointsharding -run 'Test/EndpointShardingExitIdleWhileOtherChildBlocked$' -race -count=1 -v -timeout 40s
```

```console
    endpointsharding_ext_test.go:489: Timed out waiting for ExitIdle to be delivered to child A while child B's update is blocked
panic: test timed out after 40s
FAIL	google.golang.org/grpc/balancer/endpointsharding	40.017s
### cleanup goroutine (deferred es.Close after t.Fatal):
goroutine 10 [sync.Mutex.Lock]:
google.golang.org/grpc/balancer/endpointsharding.(*balancerWrapper).close
	/home/ubuntu/wt/735d3aa5/balancer/endpointsharding/endpointsharding.go:435 +0x3a
google.golang.org/grpc/balancer/endpointsharding.(*endpointSharding).Close
	/home/ubuntu/wt/735d3aa5/balancer/endpointsharding/endpointsharding.go:263 +0x174
testing.(*common).Fatal
	/home/ubuntu/wt/735d3aa5/balancer/endpointsharding/endpointsharding_ext_test.go:489 +0xe70
### child B still blocked on <-unblockB inside its UpdateClientConnState:
goroutine 11 [chan receive]:
	/home/ubuntu/wt/735d3aa5/balancer/endpointsharding/endpointsharding_ext_test.go:448 +0x298
```

The progress assertion fails at 10 s (`defaultTestTimeout`), `t.Fatal` runs the defers — `es.Close()` first (LIFO; `cancel()` is registered earlier and runs later, and child B's block has no ctx case anyway) — and `Close` waits on child B's `bw.mu`, which B holds while parked on `<-unblockB` (line 448) with `unblockB` still open. Cleanup waits without a bound until the `go test` process timeout kills it 30 s later.

**Part reverse_execution_trigger — holds.** Same test against the coarse-lock base with only the idle-exit call adapted:

```sh
cd ~/wt/base
sed 's/^\tchildA\.ExitIdle()$/\tchildA.Balancer.ExitIdle()/' ~/wt/735d3aa5/balancer/endpointsharding/endpointsharding_ext_test.go > balancer/endpointsharding/endpointsharding_ext_test.go
go test ./balancer/endpointsharding -run 'Test/EndpointShardingExitIdleWhileOtherChildBlocked$' -race -count=1 -v -timeout 40s
```

```console
(only the idle-exit call differs from the authored test)
    endpointsharding_ext_test.go:489: Timed out waiting for ExitIdle to be delivered to child A while child B's update is blocked
panic: test timed out after 40s
FAIL	google.golang.org/grpc/balancer/endpointsharding	40.118s
### cleanup goroutine (deferred es.Close after t.Fatal):
goroutine 23 [sync.Mutex.Lock]:
google.golang.org/grpc/balancer/endpointsharding.(*endpointSharding).Close
	/home/ubuntu/wt/base/balancer/endpointsharding/endpointsharding.go:224 +0x4f      // es.childMu.Lock()
testing.(*common).Fatal
	/home/ubuntu/wt/base/balancer/endpointsharding/endpointsharding_ext_test.go:489 +0xe09
goroutine 25 [sync.Mutex.Lock]:
	/home/ubuntu/wt/base/balancer/endpointsharding/endpointsharding.go:364 +0x50      // ExitIdle goroutine waiting for childMu
### child B still blocked on <-unblockB inside its UpdateClientConnState:
goroutine 24 [chan receive]:
	/home/ubuntu/wt/base/balancer/endpointsharding/endpointsharding_ext_test.go:448 +0x298
```

Under the coarse lock, `childA.Balancer.ExitIdle()` spawns a goroutine that waits for `es.childMu` (held by the blocked update of child B), so the progress assertion times out at line 489 *before* `close(unblockB)` at line 497 is reached; the deferred `es.Close()` then blocks on `es.childMu` forever, with `unblockB` still open. It compiles and runs (no compilation error is involved). The test therefore cannot serve as a regression check against the old implementation: instead of failing in 10 s it hangs the package's test binary until the harness timeout. Fix: `defer` a `sync.OnceFunc` that closes `unblockB` (registered *after* `defer es.Close()`, or replace the defers with `t.Cleanup` in the right order) and give child B's block a `ctx.Done()` case — as the branches audited in C1/C2 (e.g. `defer unblock()`) already do.
