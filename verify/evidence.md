# Evidence — run v-8fab51c9

Audited branch: `verify/grpc-go-endpointsharding-decouple-locking-v-8fab51c9` (from `origin/grpc-go-endpointsharding-decouple-locking-perfect`, 81201fc0). Claim branches fetched from `https://github.com/kaitranntt-evals/grpc-go-endpointsharding-decouple-locking.git` into separate worktrees. Go 1.25.7, linux/amd64. Every mutation lives only in a throwaway worktree; the patches are in `verify/repro/`. `verify/repro/run_mutation.sh <branch> <patch|none> <TestMethod> [reps]` builds `go test -c -race` and runs `^Test$/^<Method>$` with `-test.timeout=60s`; `process_timeout=1` means the Go test binary panicked at its 60 s deadline.

Eval fixture (byte-exact from eval_tests.zip) on the audited branch:

```console
$ cp .../eval_tests/tests/eval_endpointsharding_test.go balancer/endpointsharding/eval_endpointsharding_test.go && cmp <same two files> && echo byte-exact
byte-exact
$ go test -race -count=1 ./balancer/endpointsharding/ ./balancer/ringhash/
ok  	google.golang.org/grpc/balancer/endpointsharding	4.720s
ok  	google.golang.org/grpc/balancer/ringhash	13.496s
```
(fixture removed afterwards; not committed)

## C1

Claim: no changed cross-child test observes completion of child B's actual operation (including its synchronous `cc.UpdateState` callback) before releasing held child A.

Method: mutate production code so that child B's synchronous `UpdateState` callback issued from inside `ExitIdle` cannot return until the batch that holds child A ends (i.e. B's operation can only complete after A is released). A test that observes B's completion before releasing A must then fail/time out; a test that releases A on an earlier signal still passes. Patches: `verify/repro/c1_6fac7ab6.patch`, `verify/repro/c1_1d4033f5.patch` (they add `inExitIdle` tracking and, in `balancerWrapper.UpdateState`, `for inhibitChildUpdates { sleep 1ms }` then print `MUTATION C1: ...` and return).

### evalon/grpc-go-en-6fac7ab6 — `TestEndpointShardingExitIdleWhileOtherChildUpdateBlocked` (endpointsharding_ext_test.go)

```console
$ verify/repro/run_mutation.sh evalon/grpc-go-en-6fac7ab6 none TestEndpointShardingExitIdleWhileOtherChildUpdateBlocked 1
--- PASS: Test (0.04s)
$ verify/repro/run_mutation.sh evalon/grpc-go-en-6fac7ab6 verify/repro/c1_6fac7ab6.patch TestEndpointShardingExitIdleWhileOtherChildUpdateBlocked 3
run1: MUTATION C1: ExitIdle UpdateState callback completed only after batch ended at Sep 24 17:09:15.291546   --- PASS: Test (0.04s)
run2: MUTATION C1: ExitIdle UpdateState callback completed only after batch ended at Sep 24 17:09:16.351506   --- PASS: Test (0.04s)
run3: MUTATION C1: ExitIdle UpdateState callback completed only after batch ended at Sep 24 17:09:17.414710   --- PASS: Test (0.04s)
```
Replay with the committed script (1 + 4 more runs):
```console
$ verify/repro/run_mutation.sh evalon/grpc-go-en-6fac7ab6 verify/repro/c1_6fac7ab6.patch TestEndpointShardingExitIdleWhileOtherChildUpdateBlocked 1
run1 rc=2 elapsed=60017ms process_timeout=1
    endpointsharding_ext_test.go:589: Timeout waiting for UpdateClientConnState to return
$ verify/repro/run_mutation.sh evalon/grpc-go-en-6fac7ab6 verify/repro/c1_6fac7ab6.patch TestEndpointShardingExitIdleWhileOtherChildUpdateBlocked 4
run1 rc=2 elapsed=60018ms process_timeout=1
    endpointsharding_ext_test.go:589: Timeout waiting for UpdateClientConnState to return
run2 rc=0 elapsed=1061ms  MUTATION C1: ExitIdle UpdateState callback completed only after batch ended at Sep 24 17:25:50.812593
run3 rc=0 elapsed=1061ms  MUTATION C1: ExitIdle UpdateState callback completed only after batch ended at Sep 24 17:25:51.879930
run4 rc=0 elapsed=1060ms  MUTATION C1: ExitIdle UpdateState callback completed only after batch ended at Sep 24 17:25:52.942970
```
Total 6 PASS / 8 mutated runs. The 2 hangs are the rotation order in which the batch, after A is released, needs B's lock held by B's delayed callback (a mutation-induced deadlock detected only after the release, at line 589). The test releases A after receiving the `exitIdleCh` signal, which is emitted before B's active `cc.UpdateState` callback finishes; in every passing run B's callback completed only after A was released.

### evalon/grpc-go-en-1d4033f5 — `TestEndpointShardingChildExitIdleDuringUnrelatedChildUpdate` (endpointsharding_child_test.go)

```console
$ verify/repro/run_mutation.sh evalon/grpc-go-en-1d4033f5 none TestEndpointShardingChildExitIdleDuringUnrelatedChildUpdate 1
--- PASS: Test (0.00s)
$ verify/repro/run_mutation.sh evalon/grpc-go-en-1d4033f5 verify/repro/c1_1d4033f5.patch TestEndpointShardingChildExitIdleDuringUnrelatedChildUpdate 3
run1: MUTATION C1: ExitIdle UpdateState callback completed only after batch ended at Sep 24 17:09:15.238633   --- PASS
run2: endpointsharding_child_test.go:293: Timeout waiting for UpdateClientConnState to return
      panic: test timed out after 1m0s
run3: MUTATION C1: ExitIdle UpdateState callback completed only after batch ended at Sep 24 17:10:16.279755   --- PASS
```
Run 2 is the rotation order where the held batch next needs B's lock (B's delayed callback holds it) — a mutation-induced deadlock detected only after the release, at line 293. In runs 1 and 3 the test released A on the ExitIdle entry signal and passed although B's operation had not completed. The test never waits for B's operation to return before `close(...)` of A's barrier.

Impact reasoning: these are the only cross-child regression tests on each branch; a regression in which B's operation is still coupled to A (B's callback/return stuck until A is released) passes them, so the decoupling they are meant to guard is not actually guarded.

## C2

Claim: a timeout in a changed concurrency test reaches an unbounded cleanup wait (deferred `Close`) on unfinished work.

Method: inject a synchronization defect of the kind these tests exist to catch — M2, the idle-exit worker spins (holding the child lock) until the batch's inhibit flag clears (`for bw.es.inhibitChildUpdates.Load() { time.Sleep(time.Millisecond) } // MUTATION M2`, patches `verify/repro/c2_m2_<branch>.patch`). Each test's own ExitIdle wait then times out; we observe whether the test ends with a bounded failure or whether cleanup hangs until the 60 s process deadline, and where the goroutines are parked.

```console
$ verify/repro/c2_cleanup_hang.sh      # 4 runs per branch
```
Per-branch outcome (process_timeout count / 4; first test failure line; Close frame in the hang trace):

| branch | hangs | test's own timeout line | cleanup frame in trace |
|---|---|---|---|
| abc06f48 | 1/4 (3 bounded fails at 10 s) | `endpointsharding_ext_test.go:468: Timed out waiting for ExitIdle on child "free" while another child was blocked` | `(*balancerWrapper).close ... endpointsharding.go:410` from deferred Close at test line 468 |
| 4d9a9b72 | 4/4 | `endpointsharding_locking_test.go:201: Timeout waiting for ExitIdle on child 1 while child 2's update is blocked` | `(*balancerWrapper).close` / `(*endpointSharding).Close-range1` |
| 9d0e1576 | 1/4 (3 bounded fails at 10 s) | `endpointsharding_ext_test.go:496: Timed out waiting for ExitIdle to be delivered to the idle child while another child's update was blocked` | `(*endpointSharding).Close ... endpointsharding.go:247` [sync.Mutex.Lock]; update worker at `updateClientConnState endpointsharding.go:434` [sync.Mutex.Lock] |
| 1e3912e1 | 3/4 | `concurrency_ext_test.go:144: B's idle exit blocked behind A's update` | `(*balancerWrapper).close` |
| 7c856312 | 4/4 | `concurrency_test.go:125: Other child's ExitIdle did not complete while update was blocked` | `(*balancerWrapper).close` |
| a32002ab | 4/4 | `endpointsharding_concurrency_test.go:137: Timed out waiting for the other child to exit idle while the update remains blocked` | `(*balancerWrapper).close` |
| 2e629b7c | 4/4 | `endpointsharding_concurrency_test.go:138: Timed out waiting for event: context deadline exceeded` | `(*balancerWrapper).close` |
| 895e4fc3 | 3/4 (1 bounded fail at 20 s) | `concurrency_test.go:143: Timed out waiting for other child to exit idle while update remains blocked` | `(*balancerWrapper).close` |
| 525ac3bd | 3/4 (1 bounded fail at 20 s) | `endpointsharding_concurrency_test.go:149: Timed out waiting for ExitIdle while the sibling update is blocked` | `(*balancerWrapper).close` |
| 6dbb257d | 0/4 (bounded fails at 20–30 s) | `endpointsharding_concurrency_test.go:157: Timed out waiting for B to exit idle while A is blocked` then `:148: Timed out waiting for configuration update to finish` | none — Close not reached |

Representative full trace (abc06f48 run 4):
```console
    endpointsharding_ext_test.go:468: Timed out waiting for ExitIdle on child "free" while another child was blocked
panic: test timed out after 1m0s
goroutine 39 [sync.Mutex.Lock]:
  (*balancerWrapper).close(0xc000214a80)          endpointsharding.go:410
  (*endpointSharding).Close(0xc000202fc0)          endpointsharding.go:237
                                                   endpointsharding_ext_test.go:468
goroutine 40 [sync.Mutex.Lock]:
  (*balancerWrapper).updateClientConnState(0xc000214a80)  endpointsharding.go:400
  (*endpointSharding).UpdateClientConnState        endpointsharding.go:189   (worker started at test line 443)
goroutine 41 [sleep]:
  (*balancerWrapper).exitIdle(0xc000214a80)        endpointsharding.go:389   (idle-exit worker holding the free child's mutex)
```
The barrier-release defer had run (blocked child released), but the batch worker and the idle-exit worker were still unfinished; deferred `es.Close()` (registered at test line 426, no deadline) blocks on the same child mutex. The branch-specific "failure before release defer is registered" path was also driven (M5: 11 s sleep before the second update, `verify/repro` not needed): the test failed at `endpointsharding_ext_test.go:447: Timed out waiting for child to block` and ended bounded at 20 s in 2/2 runs because `Close` ran before the late worker reached the barrier (the worker then leaked at test line 413) — so the hang on abc06f48 is via the line-468 path, not that one.

9d0e1576 trace (run 4): deferred order is `defer es.Close()` then `defer release()`; after release the update worker still waits on the idle child's mutex held by the idle-exit worker (`exitIdleSync endpointsharding.go:428 [sleep]`), and `Close` (`endpointsharding.go:247`) blocks on the lock held by that update worker — Close is called without establishing that the update worker finished.

6dbb257d (refutation): its cleanup is
```go
defer func() {
    release()
    awaitEvent(t, ctx, updateDone, "configuration update to finish") // t.Fatalf on ctx.Done()
    es.Close()
}()
```
After any ctx timeout, `awaitEvent` fails immediately (ctx already done) and `t.Fatalf` → `runtime.Goexit` skips `es.Close()`; on the success path `Close` runs only after `updateDone`. Stress with a permanently stuck idle-exit worker (M3, re-entrant `childMu.Lock()` in `exitIdle`) and with the rotation forced to [B, A] (M6) so B's update completes before A blocks:
```console
$ verify/repro/run_mutation.sh evalon/grpc-go-en-6dbb257d verify/repro/c2_m3m6_6dbb257d.patch TestEndpointShardingExitIdleDuringBlockedUpdate 3
run1 rc=1 elapsed=30150ms process_timeout=0
    endpointsharding_concurrency_test.go:157: Timed out waiting for B to exit idle while A is blocked
    endpointsharding_concurrency_test.go:148: Timed out waiting for configuration update to finish
    grpctest.go:45: Leaked goroutine: ... (*balancerWrapper).exitIdle ... endpointsharding.go:352
--- FAIL: Test/EndpointShardingExitIdleDuringBlockedUpdate/explicit (10.05s)
--- FAIL: Test/EndpointShardingExitIdleDuringBlockedUpdate/automatic (10.05s)
```
M3 alone (8 runs) and M2 (4 runs): all bounded (20–30 s), never a process timeout. Close is never reached with unfinished idle-exit work on a timeout path.

Impact reasoning: on 9 of 10 branches a genuine regression of the property under test converts a clear 10 s assertion failure into a whole-package 60 s (or CI-default 10 min) timeout panic with interleaved goroutine dumps, hiding which test failed first and blocking every later test in the package binary.

## C3

Claim (branch evalon/grpc-go-en-1d4033f5): the construction comment promises initial configuration precedes reconnection delivery, but ExitIdle can reach the child first.

Repro `verify/repro/c3_construction_order_test.go`: child builder synchronously reports IDLE (auto-reconnect on), the child records the order of `UpdateClientConnState` and `ExitIdle`; 20 000 fresh balancers.

```console
$ cp verify/repro/c3_construction_order_test.go balancer/endpointsharding/   # in the 1d4033f5 worktree
$ go test ./balancer/endpointsharding -run '^TestVerifyC3ReconnectBeforeInitialConfig$' -count=1 -v
=== RUN   TestVerifyC3ReconnectBeforeInitialConfig
    c3_construction_order_test.go:78: iteration 1270: child call order = [ExitIdle UpdateClientConnState]
    c3_construction_order_test.go:83: ExitIdle delivered before initial UpdateClientConnState in 6/20000 iterations
    c3_construction_order_test.go:85: construction comment's ordering promise violated 6 times
--- FAIL: TestVerifyC3ReconnectBeforeInitialConfig (0.04s)
```
Mechanism: the builder's IDLE report spawns the ExitIdle goroutine, which can take the child lock in the window between construction finishing and the initial update acquiring it.

Impact reasoning: a child policy receives `ExitIdle` before any configuration/addresses; for policies like pick_first that is a no-op or acts on empty state, so the reconnection request can be silently lost on construction — rare (≈0.03 %) but on the ordinary path of every new endpoint.

## C4

Claim (audited perfect branch): TestSameChildMutualExclusion / TestDecoupledChildProgress / construction test have unbounded teardown or operation waits.

Part 1 + 2 (teardown dependency, timeout-path reachability). M2 on the perfect branch (`verify/repro/c4_m2_perfect.patch`: idle-exit worker waits while `parent.inhibitChildUpdates` is set, holding the child lock):
```console
$ verify/repro/c4_perfect_teardown.sh   (M2, TestDecoupledChildProgress, 4 runs: 2 process timeouts, 2 bounded fails at 12.3 s)
    endpointsharding_test.go:339: child 2 ExitIdle blocked while child 1 was updating (coarse locking defect)
panic: test timed out after 1m0s
goroutine 38 [sync.Mutex.Lock]:
    endpointsharding.go:387      (child close lock)
    endpointsharding.go:228/229  (Close)
    endpointsharding_test.go:294 (deferred lb.Close after releaseAndWait()==true)
    endpointsharding_test.go:339
goroutine 42 [sleep]:
    endpointsharding.go:398      (separately dispatched idle-exit worker from `go child2CS.ExitIdle()`)
```
Cleanup joins only the configuration worker (`workerDone`, 2 s) and then calls `lb.Close()` without settling the `go child2CS.ExitIdle()` work; after the 300 ms idle-exit timeout, Close blocks forever on that worker's lock.

Part 3 (direct operation waits). M4 (`verify/repro/c4_m4_perfect.patch`: synchronous child `cc.UpdateState` callback blocks on the child mutex):
```console
TestDecoupledChildProgress:   panic: test timed out after 1m0s
   goroutine 24 [sync.Mutex.Lock]: endpointsharding.go:356 <- endpointsharding_test.go:131 (child UpdateState) <- endpointsharding.go:179 <- endpointsharding_test.go:262 (initial synchronous lb.UpdateClientConnState)
TestSameChildMutualExclusion: panic: test timed out after 1m0s
   goroutine 24 [sync.Mutex.Lock]: endpointsharding.go:356 <- endpointsharding_test.go:131 <- endpointsharding.go:179 <- endpointsharding_test.go:373 (initial synchronous lb.UpdateClientConnState)
TestSynchronousConstructionIdleCallback: bounded — endpointsharding_test.go:534: childBuilder never entered construction; :491 cleanup timed out; --- FAIL (14.02s)
```
Source: TestSameChildMutualExclusion also does `<-callDone` (line after "ExitIdle finishes after release") with no deadline.

Impact reasoning: the same deadlock the tests target is reported as a package-wide 60 s panic rather than within their 300 ms–2 s operation deadlines.

## C5

Claim (branch evalon/grpc-go-en-1aafe684): changed ringhash.go references deprecated `balancer.ExitIdler` → unsuppressed SA1019.

```console
$ git diff bf9e7cd3 HEAD -- balancer/ringhash/ringhash.go | grep ExitIdler
-		var idleBalancer endpointsharding.ExitIdler
+		var idleBalancer balancer.ExitIdler
-	balancer endpointsharding.ExitIdler
+	exitIdler balancer.ExitIdler
$ grep -n -B2 'type ExitIdler' balancer/balancer.go
374-// Deprecated: All balancers must implement this interface. This interface will
375-// be removed in a future release.
376:type ExitIdler interface {
$ ./scripts/vet.sh      (staticcheck from `scripts/vet.sh -install`)
+ grep '(SA1019)' /tmp/tmp.0qYyvv6GgS
balancer/ringhash/ringhash.go:271:20: balancer.ExitIdler is deprecated: All balancers must implement this interface. This interface will be removed in a future release.  (SA1019)
balancer/ringhash/ringhash.go:405:12: balancer.ExitIdler is deprecated: All balancers must implement this interface. This interface will be removed in a future release.  (SA1019)
exit=1
$ grep -c ExitIdler scripts/vet.sh
0
```
vet.sh runs `staticcheck -checks 'all' ./...` and fails on any SA1019 line not in its allow-list (lines 168+); `ExitIdler is deprecated:` is not listed.

Impact reasoning: the repository's lint gate (vet.sh, used by CI) fails on this branch.

## C6

Claim (branch evalon/grpc-go-en-abc06f48): a child callback crossing batch entry publishes a parent picker with partially updated children.

Trace (abc06f48 endpointsharding.go): `updateState()` does `if es.inhibitChildUpdates.Load() { return }` (line 251) **before** `es.mu.Lock()` (line 256); `UpdateClientConnState` sets `inhibitChildUpdates.Store(true)` (line 156) without `es.mu`; child states are written under `es.mu` in `balancerWrapper.UpdateState` (line 365). Contrast: evalon/grpc-go-en-1d4033f5 checks `es.inhibitChildUpdates` inside `updateStateLocked` under `es.mu` (lines 248–258).

Deterministic interleaving: `verify/repro/c6_widen_window.patch` + `c6_hook_decl.go` add a hook that runs after the (passed) inhibit check and before `es.mu.Lock()` — it widens the existing window, no decision changes. Test `c6_deterministic_interleaving_test.go`: (1) an out-of-batch child callback passes the check and pauses; (2) batch gen2 starts, the first child applies gen2 and reports (inhibited), the second child's update is held before applying; (3) the paused callback resumes.

```console
$ git apply verify/repro/c6_widen_window.patch && cp verify/repro/c6_hook_decl.go verify/repro/c6_deterministic_interleaving_test.go balancer/endpointsharding/
$ go test ./balancer/endpointsharding -run '^TestVerifyC6DeterministicStaleInhibitCheck$' -count=1 -v   (3 runs, all FAIL)
    after batch gen1: ["b=gen1 a=gen1 "]
    step1: out-of-batch callback passed inhibitChildUpdates check (batch not yet entered)
    step2: batch gen2 entered; first child applied gen2, second child update still unfinished
    step3: parent notifications published while batch still open: ["b=gen1 a=gen2 "]
    after batch gen2 completes: all notifications: ["b=gen1 a=gen2 " "a=gen2 b=gen2 "]
--- FAIL: TestVerifyC6DeterministicStaleInhibitCheck (0.00s)
(run 2: ["a=gen2 b=gen1 "], run 3: ["a=gen1 b=gen2 "])
```
Unwidened stress (5000 batches with concurrent out-of-batch child callbacks, no hook) observed 1 mixed-generation parent notification in one of the first two concurrent runs, 0 in 8 subsequent runs — the window exists but is narrow without widening.

Impact reasoning: a parent (e.g. ringhash/petiole) receives a picker where some endpoints are on the new config and some on the old, i.e. exactly the intermediate state the batch inhibition is meant to hide; it is followed by the correct consolidated notification, so the effect is a transient inconsistent picker.

## C7

Claim (branch evalon/grpc-go-en-abc06f48): maintained tests lack assertions for (a) closed/removed child handles, (b) child-update errors in a batch, (c) synchronous resolver-error callbacks.

Maintained tests on this branch: `endpointsharding_test.go` (TestRotateEndpoints) and `endpointsharding_ext_test.go` (Basic, ReconnectDisabled, ExitIdle, ExitIdleNotBlockedByOtherChild, SynchronousIdleDuringBuild, SingleUpdateForMultipleEndpoints). grep for ResolverError/errors/closed/stale shows no test calling a removed child's ChildState, and no child returning an error from UpdateClientConnState.

Mutation removing all three protections (`verify/repro/c7_abc_guards_removed.patch`: `if bw.child != nil { // MUTATION C7a: closed guard removed`, `_ = err // MUTATION C7b: child errors dropped`, `// MUTATION C7c: es.inhibitChildUpdates.Store(true)` in ResolverError):
```console
$ go vet ./balancer/endpointsharding/ && go test -race -count=1 ./balancer/endpointsharding/
ok  	google.golang.org/grpc/balancer/endpointsharding	1.172s
rc=0
```
Part (c) control — deadlock synchronous callbacks during ResolverError (`verify/repro/c7_abc_resolvererror_deadlock.patch`: hold `es.mu` across child `resolverError`):
```console
$ verify/repro/run_mutation.sh evalon/grpc-go-en-abc06f48 verify/repro/c7_abc_resolvererror_deadlock.patch TestEndpointShardingBasic 1
    endpointsharding_ext_test.go:203: Context timed out waiting for picker with resolver error.
panic: test timed out after 1m0s
  (*balancerWrapper).UpdateState  endpointsharding.go:367  [sync.Mutex.Lock]
  pickfirst.(*pickfirstBalancer).updateBalancerState  pickfirst.go:799
  pickfirst.(*pickfirstBalancer).resolverErrorLocked
  (*balancerWrapper).resolverError <- (*endpointSharding).ResolverError <- gracefulswitch <- ccBalancerWrapper.resolverError
```
So TestEndpointShardingBasic does drive pick_first's synchronous `UpdateState` during `ResolverError` and asserts the resulting error picker (a deadlock regression fails its assertion); it does not assert consolidation (C7c passes).

Impact reasoning: the closed-handle guard and first-error propagation can be deleted with a green maintained suite; only external fixtures cover them.

## C8

Claim: branch evalon/grpc-go-en-53e8c596's independent-progress test, with only idle-exit API calls adapted to the coarse-lock base, hangs in cleanup after its idle-exit timeout.

Base: `bf9e7cd3` = parent of 53e8c596 (`3e34cf90 chore: apply eval changes`), which changes endpointsharding.go, adds endpointsharding_child_test.go and touches ringhash.go. Adaptation (only diff):
```console
$ diff <53e8c596 test> <c8base test>
188c188
< 	childState1.ExitIdle()
---
> 	childState1.Balancer.ExitIdle()
357,358c357,358
< 	childState2.ExitIdle()
< 	childState1.ExitIdle()
---
> 	childState2.Balancer.ExitIdle()
> 	childState1.Balancer.ExitIdle()
```
Test structure: `defer es.Close()` (registered before the second update); addr2's update blocks on `<-unblockCh`; `waitForExitIdle(ctx, t, b, addr1)` calls `t.Fatalf` on ctx timeout; `close(unblockCh)` is only on the success path after it.

```console
$ verify/repro/c8_coarse_base.sh
rc=2 elapsed=60s
=== RUN   Test/EndpointSharding_ExitIdleNotBlockedByOtherChildUpdate
    endpointsharding_child_test.go:189: Timeout waiting for ExitIdle to be called on child for "addr1"
panic: test timed out after 1m0s
goroutine 10 [sync.Mutex.Lock]:   endpointsharding.go:224   (deferred es.Close on the coarse es.mu)
goroutine 12 [sync.Mutex.Lock]:   endpointsharding.go:364   created by ...balancerWrapper).ExitIdle in goroutine 10
```
Replay via the committed script (second run, same outcome; stack of the blocked update worker included):
```console
rc=2 elapsed=60s
    endpointsharding_child_test.go:189: Timeout waiting for ExitIdle to be called on child for "addr1"
panic: test timed out after 1m0s
goroutine 23 [sync.Mutex.Lock]:
	.../balancer/endpointsharding/endpointsharding.go:224 +0x4f
	.../balancer/endpointsharding/endpointsharding_child_test.go:135 +0x296
	.../balancer/endpointsharding/endpointsharding_child_test.go:189 +0x1059
	.../balancer/endpointsharding/endpointsharding_child_test.go:157 +0x131     (addr2 update parked on <-unblockCh)
	.../balancer/endpointsharding/endpointsharding_child_test.go:82 +0x19c
	.../balancer/endpointsharding/endpointsharding.go:376
	.../balancer/endpointsharding/endpointsharding.go:175 +0xbc2
	.../balancer/endpointsharding/endpointsharding_child_test.go:179 +0x9e       (second UpdateClientConnState worker)
goroutine 25 [sync.Mutex.Lock]:
	.../balancer/endpointsharding/endpointsharding.go:364 +0x50
	.../balancer/endpointsharding/endpointsharding.go:363 +0x8b
```
The worker started at test line 179 holds the coarse lock while parked at line 157 on `<-unblockCh`; after the line-189 fatal, `close(unblockCh)` never runs and the deferred Close (line 224 in the base) waits on that lock until the 60 s panic.

Impact reasoning: on the regression the test is designed to detect, it produces a 60 s package timeout panic instead of the intended 10 s assertion failure.
