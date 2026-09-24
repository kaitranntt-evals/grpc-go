# Evidence — grpc-go endpoint-sharding decouple-locking audit (run v-811bb794)

Audited branch: `grpc-go-endpointsharding-decouple-locking-perfect` at `81201fc085e37de0ffb23505ac7831350b326c33` (origin `kaitranntt-evals/grpc-go`).
Coarse-lock base: `bf9e7cd3430df40d0732ba42eb88bd5f2cc63407`. Toolchain: `go1.25.7 linux/amd64`.
Claim-target branches were fetched from `kaitranntt-evals/grpc-go-endpointsharding-decouple-locking` (remote `evals`) and checked out in separate detached worktrees. Every repro script creates its own scratch worktree, so no production file in the audited checkout is modified.

Evaluator fixture check (audited branch, all commands from `verify_prompt.md`):

```sh
cmp /home/ubuntu/attachments/1dd1a439-9c5e-42e0-a1c1-3f9963dcbe0c/eval_tests/tests/eval_endpointsharding_test.go balancer/endpointsharding/eval_endpointsharding_test.go && echo "fixture byte-identical"
go test -v ./balancer/endpointsharding -run '^TestEval_<Name>$' -race -count=1   # for each of the 8 TestEval_* names
go test -v ./balancer/ringhash -race -count=1
go test -v ./balancer/endpointsharding -race -count=1
go build ./... ; go vet ./...
```

```console
fixture byte-identical
--- PASS: TestEval_ChildExitIdleWhileAnotherChildBlocked (0.00s)
--- PASS: TestEval_SameChildMutualExclusion (0.74s)
--- PASS: TestEval_SynchronousChildUpdateDeadlock (0.00s)
--- PASS: TestEval_ConstructionIdleCallbackSafety (2.70s)
--- PASS: TestEval_BatchUpdateInhibition (0.00s)
--- PASS: TestEval_ChildStateExitIdleCallback (0.00s)
--- PASS: TestEval_ResolverErrorConsolidatedNotification (0.00s)
--- PASS: TestEval_PickerStateAggregationPrecedence (0.00s)
ok  	google.golang.org/grpc/balancer/ringhash	13.724s
ok  	google.golang.org/grpc/balancer/endpointsharding	4.734s
build exit=0
vet exit=0
```

## C1

Claim: failure-path cleanup in the changed/added concurrency tests invokes lock-dependent teardown after a worker times out without establishing that the worker has settled. Parts: *teardown_guard*, *timeout_reachability*.

Method (identical on every branch). `verify/repro/c1_run.sh <commit>` creates a detached scratch worktree of the branch, installs `verify/repro/c1_stall_hook.go.txt` as `verify_c1_hook.go`, and rewrites every `<x>.child/childLB/childBalancer.ExitIdle()` call site in `endpointsharding.go` to `verifyC1ExitWrap(<x>.ExitIdle)()`. With `VERIFY_C1_EXIT_STALL=1` the wrapper parks forever *inside* the per-child critical section (the lock is already held by the caller), i.e. it models an idle-exit worker that has not settled by the test's deadline. It then runs the branch's added independent-progress test with `-timeout 45s` under an external `timeout 120`. `VERIFY_C1_STALL=off` disables the separate update-stall mode. The fixture barrier and the test context are left exactly as authored, so the run shows whether the authored release/cancel lets teardown proceed.

Commands (repo root; commit SHAs are the claim-branch heads):

```sh
VERIFY_C1_STALL=off VERIFY_C1_EXIT_STALL=1 ONLY='^TestEndpointShardingExitIdleNotBlockedByOtherChildUpdate$' verify/repro/c1_run.sh c7c140815bcdc7e6517f4ecd0c1eec9a6268e605   # a68de200
VERIFY_C1_STALL=off VERIFY_C1_EXIT_STALL=1 ONLY='^TestEndpointSharding_ExitIdleNotBlockedByOtherChildUpdate$' verify/repro/c1_run.sh 8d00b88b0a3285e0f216baf60cf81c26ad09efdb  # f45f5933
VERIFY_C1_STALL=off VERIFY_C1_EXIT_STALL=1 ONLY='^TestEndpointShardingExitIdleNotBlockedByOtherChild$' verify/repro/c1_run.sh e54531b9781304172a6f73cf882dffa30b78452c        # 46274ef5
VERIFY_C1_STALL=off VERIFY_C1_EXIT_STALL=1 ONLY='^TestEndpointShardingExitIdleDuringBlockedChildUpdate$' verify/repro/c1_run.sh 13299c8476c23da978677b8917e80ac97392480c    # d351eac1
VERIFY_C1_STALL=off VERIFY_C1_EXIT_STALL=1 ONLY='^TestEndpointSharding_ExitIdleWhileOtherChildBlocked$' verify/repro/c1_run.sh 6fad3d693de0055f8965829f2b27961f33a65822     # f309499f
VERIFY_C1_STALL=off VERIFY_C1_EXIT_STALL=1 ONLY='^TestEndpointShardingExitIdleWithBlockedChildUpdate$' verify/repro/c1_run.sh 18328bf31e26262e49b90d62f3c12d10fe215cf7      # 808ddbc2
VERIFY_C1_STALL=off VERIFY_C1_EXIT_STALL=1 ONLY='^TestEndpointShardingChildExitIdleDuringUpdate$' verify/repro/c1_run.sh b1b2616d0e30f4badafa3912d81dc816527683d0           # 122de44c
VERIFY_C1_STALL=off VERIFY_C1_EXIT_STALL=1 ONLY='^TestChildExitIdleDuringBlockedUpdate$' verify/repro/c1_run.sh 0bc4bcbcb5d9d12aa89f3ac28974c8f5239389cb                   # c0a5b0d3
VERIFY_C1_STALL=off VERIFY_C1_EXIT_STALL=1 ONLY='^TestExitIdleDuringOtherChildUpdate$' verify/repro/c1_run.sh 0a799bc15ed5bc028e072e3f4f20192fae9a6bab                     # c7493d5d
VERIFY_C1_STALL=off VERIFY_C1_EXIT_STALL=1 ONLY='^TestExitIdleWhileUpdatingAnotherChild$' verify/repro/c1_run.sh 7ea55c90168ea4818feef7e17fdf545f4ceed2b1                  # 6140be00
```

Key output per branch (filtered by the script; `(*balancerWrapper).close` / `(*endpointSharding).Close` frames are from the goroutine dump printed when the 45 s go-test alarm fires, i.e. the test's deferred/cleanup `Close` is parked on the child mutex held by the stalled idle-exit worker):

### evalon/grpc-go-en-a68de200

```console
verifyC1: child ExitIdle worker stalled while holding the child lock
    endpointsharding_ext_test.go:525: Timeout waiting for ExitIdle on child "other-endpoint" while an update to child "blocking-endpoint" is blocked
panic: test timed out after 45s
google.golang.org/grpc/balancer/endpointsharding.(*balancerWrapper).close(...)
google.golang.org/grpc/balancer/endpointsharding.(*endpointSharding).Close(...)
FAIL	google.golang.org/grpc/balancer/endpointsharding	45.109s
exit=1
```

### evalon/grpc-go-en-f45f5933

```console
verifyC1: child ExitIdle worker stalled while holding the child lock
    endpointsharding_locking_test.go:223: Timeout waiting for ExitIdle to reach child A while child B's update is blocked
panic: test timed out after 45s
google.golang.org/grpc/balancer/endpointsharding.(*balancerWrapper).close(...)
google.golang.org/grpc/balancer/endpointsharding.(*endpointSharding).Close(...)
FAIL	google.golang.org/grpc/balancer/endpointsharding	45.009s
exit=1
```

### evalon/grpc-go-en-46274ef5

```console
verifyC1: child ExitIdle worker stalled while holding the child lock
    endpointsharding_ext_test.go:490: Timed out waiting for ExitIdle to be delivered to child A while child B was blocked
panic: test timed out after 45s
google.golang.org/grpc/balancer/endpointsharding.(*balancerWrapper).close(...)
google.golang.org/grpc/balancer/endpointsharding.(*endpointSharding).Close(...)
FAIL	google.golang.org/grpc/balancer/endpointsharding	45.008s
exit=1
```

### evalon/grpc-go-en-d351eac1

```console
verifyC1: child ExitIdle worker stalled while holding the child lock
    endpointsharding_ext_test.go:456: Timeout waiting for ExitIdle on child "other-endpoint" while another child's update is blocked
panic: test timed out after 45s
google.golang.org/grpc/balancer/endpointsharding.(*balancerWrapper).close(...)
google.golang.org/grpc/balancer/endpointsharding.(*endpointSharding).Close(...)
FAIL	google.golang.org/grpc/balancer/endpointsharding	45.105s
exit=1
```

### evalon/grpc-go-en-f309499f

```console
verifyC1: child ExitIdle worker stalled while holding the child lock
    endpointsharding_concurrency_ext_test.go:314: Timed out waiting for ExitIdle call 1 of 1 on child "addr2"
panic: test timed out after 45s
google.golang.org/grpc/balancer/endpointsharding.(*balancerWrapper).close(...)
google.golang.org/grpc/balancer/endpointsharding.(*endpointSharding).Close(...)
FAIL	google.golang.org/grpc/balancer/endpointsharding	45.078s
exit=1
```

### evalon/grpc-go-en-808ddbc2

```console
verifyC1: child ExitIdle worker stalled while holding the child lock
    endpointsharding_ext_test.go:517: Timeout waiting for ExitIdle() on child balancer while another child is blocked
panic: test timed out after 45s
google.golang.org/grpc/balancer/endpointsharding.(*balancerWrapper).close(...)
google.golang.org/grpc/balancer/endpointsharding.(*endpointSharding).Close(...)
FAIL	google.golang.org/grpc/balancer/endpointsharding	45.015s
exit=1
```

### evalon/grpc-go-en-122de44c

```console
verifyC1: child ExitIdle worker stalled while holding the child lock
    endpointsharding_concurrency_test.go:149: Timed out waiting for other child's ExitIdle while update is blocked
panic: test timed out after 45s
google.golang.org/grpc/balancer/endpointsharding.(*balancerWrapper).close(...)
google.golang.org/grpc/balancer/endpointsharding.(*endpointSharding).Close(...)
FAIL	google.golang.org/grpc/balancer/endpointsharding	45.100s
exit=1
```

### evalon/grpc-go-en-c0a5b0d3

```console
verifyC1: child ExitIdle worker stalled while holding the child lock
    concurrency_test.go:141: Child ExitIdle did not complete while the other child's update was blocked
panic: test timed out after 45s
google.golang.org/grpc/balancer/endpointsharding.(*balancerWrapper).close(...)
google.golang.org/grpc/balancer/endpointsharding.(*endpointSharding).Close(...)
FAIL	google.golang.org/grpc/balancer/endpointsharding	45.034s
exit=1
```

### evalon/grpc-go-en-c7493d5d

```console
verifyC1: child ExitIdle worker stalled while holding the child lock
    endpointsharding_concurrency_test.go:143: Independent child's ExitIdle blocked behind another child's update
panic: test timed out after 45s
google.golang.org/grpc/balancer/endpointsharding.(*balancerWrapper).close(...)
google.golang.org/grpc/balancer/endpointsharding.(*endpointSharding).Close(...)
FAIL	google.golang.org/grpc/balancer/endpointsharding	45.106s
exit=1
```

### evalon/grpc-go-en-6140be00

```console
verifyC1: child ExitIdle worker stalled while holding the child lock
    endpointsharding_concurrency_test.go:138: ExitIdle while B is blocked = (<nil>, context deadline exceeded), want (A, nil)
panic: test timed out after 45s
google.golang.org/grpc/balancer/endpointsharding.(*balancerWrapper).close(...)
google.golang.org/grpc/balancer/endpointsharding.(*endpointSharding).Close(...)
FAIL	google.golang.org/grpc/balancer/endpointsharding	45.105s
exit=1
```

Per-branch observations (identical shape on all ten branches):

- *timeout_reachability* — on every branch the idle-exit worker is still parked while holding the child lock when the test's progress assertion times out (the `_test.go:NNN:` timeout line), and the test then proceeds into its cleanup.
- *teardown_guard* — on every branch the cleanup path calls `Close` (deferred `Close`, or a cleanup that first cancels the context / closes the fixture barrier and then calls `Close`) without first waiting for the idle-exit worker to finish. Cancelling the context (d351eac1, 6140be00, …) or closing the fixture barrier releases only the *update* child; it does not release the stalled idle-exit worker, so `Close` blocks on the per-child mutex until the 45 s go-test alarm kills the binary (`panic: test timed out after 45s`, `exit=1`). The test run never reports the original assertion as a clean `--- FAIL`; it is reported as a whole-package timeout with a goroutine dump.

Impact: when a regression makes an idle-exit worker slow or stuck while holding the child lock (exactly the class of bug these tests exist to catch), each of these tests turns a precise assertion failure into a package-wide hang that is killed by the go-test timeout (default 10 min in CI). All other tests in the package lose their results in that run, and the diagnostic is a goroutine dump instead of the assertion message. Workaround: run with a short `-timeout`.

## C2

Claim: the changed/added tests on `evalon/grpc-go-en-d351eac1` lack a regression that requires child B's operation to complete while child A's contested operation remains held by the test.

Inventory of changed tests on d351eac1 (`13299c8476c23da978677b8917e80ac97392480c`):

```sh
cd <worktree of 13299c84> && for f in $(git diff --name-only bf9e7cd3 -- balancer | grep _test.go); do git diff bf9e7cd3 -- $f | grep -E '^\+func '; done
```

```console
+func childStateForAddr(t *testing.T, picker balancer.Picker, addr string) endpointsharding.ChildState {
+func (s) TestEndpointShardingExitIdleDuringBlockedChildUpdate(t *testing.T) {
+func (s) TestEndpointShardingChildReportsIdleSynchronously(t *testing.T) {
+func (cc *updateCountingCC) UpdateState(state balancer.State) {
+func (s) TestEndpointShardingSingleUpdateForClientConnState(t *testing.T) {
```

The only independent-progress test is `TestEndpointShardingExitIdleDuringBlockedChildUpdate` (`balancer/endpointsharding/endpointsharding_ext_test.go:378`). A's hold and B's assertion share one context deadline:

```go
ctx, cancel := context.WithTimeout(context.Background(), defaultTestTimeout)
...
if addr == blockingAddr && blockUpdates.Load() {
    updateBlocked <- struct{}{}
    select {
    case <-unblock:
    case <-ctx.Done():          // A is released by the shared deadline
    }
}
...
otherChild.ExitIdle()
select {
case got := <-exitIdleCh:        // B's signal
    ...
case <-ctx.Done():               // same deadline
    t.Fatalf("Timeout waiting for ExitIdle on child %q while another child's update is blocked", otherAddr)
}
close(unblock)
```

After B's signal the test does not check that A's update is still outstanding, so a B signal that arrives only because the deadline released A is accepted if the `select` picks it.

Control run — coarse-lock model (a package mutex taken by the per-child `exitIdleSync` and `updateClientConnState`), test otherwise unmodified:

```sh
verify/repro/c23_coarse.sh 13299c8476c23da978677b8917e80ac97392480c 'exitIdleSync|updateClientConnState' TestEndpointShardingExitIdleDuringBlockedChildUpdate 8
```

```console
instrumented:
400-func (bw *balancerWrapper) exitIdleSync() {
401:	verifyCoarseMu.Lock()
411-func (bw *balancerWrapper) updateClientConnState(ccs balancer.ClientConnState) error {
412:	verifyCoarseMu.Lock()
run 1: FAIL
    endpointsharding_ext_test.go:456: Timeout waiting for ExitIdle on child "other-endpoint" while another child's update is blocked
...
coarse-lock model: 0/8 PASS, 8/8 FAIL
```

Shared-deadline interleaving — same coarse-lock model plus a pause after `otherChild.ExitIdle()` until the shared deadline has fired (models the scheduler preempting the test goroutine; nothing else changed):

```sh
verify/repro/c2_shared_deadline.sh 13299c8476c23da978677b8917e80ac97392480c 'exitIdleSync|updateClientConnState' TestEndpointShardingExitIdleDuringBlockedChildUpdate 'otherChild.ExitIdle()' 8
```

```console
449-	otherChild.ExitIdle()
450:	time.Sleep(defaultTestTimeout + time.Second) // verify: preemption until the shared deadline has fired
run 1: PASS (assertion accepted B's signal after the deadline released A)
run 2: PASS (assertion accepted B's signal after the deadline released A)
run 3: PASS (assertion accepted B's signal after the deadline released A)
run 4: FAIL
    endpointsharding_ext_test.go:467: Timeout waiting for UpdateClientConnState to complete after unblocking child
run 5: FAIL
    endpointsharding_ext_test.go:457: Timeout waiting for ExitIdle on child "other-endpoint" while another child's update is blocked
...
shared-deadline interleaving under coarse lock: 3/8 accepted, 5/8 rejected
```

Observation: under a coarse-lock implementation, the interleaving "deadline releases A → B completes → assertion accepts B's signal" occurred in 3 of 8 runs and the regression passed. No changed test on this branch guarantees observing B's completion while A is still held.

Impact: a reintroduced coarse lock can pass this branch's only independent-progress regression whenever the test goroutine is delayed past `defaultTestTimeout` (overloaded CI runners), so the regression does not reliably guard the behavior it is named for. Next: give A's hold its own release (not the shared `ctx`) and assert, after B's signal, that `updateDone` is still empty before `close(unblock)`.

## C3

Claim: the changed/added tests on `evalon/grpc-go-en-745de200` lack an independent-child-progress regression with an operation-specific hold and terminal completion evidence before release.

Branch head `0c79b5be417ca395f6ab786120363624c4584892`. Added tests: `TestEndpointShardingExitIdleNotBlockedByOtherChild`, `TestEndpointShardingSynchronousChildUpdate`.

```sh
cd <worktree of 0c79b5be> && for f in $(git diff --name-only bf9e7cd3 -- balancer | grep _test.go); do git diff bf9e7cd3 -- $f | grep -E '^\+func '; done
sed -n 374,470p balancer/endpointsharding/endpointsharding_ext_test.go
```

Traced structure of `TestEndpointShardingExitIdleNotBlockedByOtherChild` (`endpointsharding_ext_test.go:374`):

```go
UpdateClientConnState: func(bd *stub.BalancerData, ccs balancer.ClientConnState) error {
    addr := endpointAddr(ccs)
    bd.ClientConn.UpdateState(balancer.State{ConnectivityState: connectivity.Idle, ...})
    // Block only on the second update to the child for blockedAddr.
    if addr == blockedAddr && childUpdates.Add(1) == 2 {   // counter only advances for blockedAddr (&& short-circuit)
        close(blockedEntered)
        select { case <-release: case <-ctx.Done(): }
    }
    return nil
},
ExitIdle: func(bd *stub.BalancerData) {
    select { case exitIdleCalled <- struct{}{}: default: }   // no configured work after the signal
},
...
idleChild.ExitIdle()
select {
case <-exitIdleCalled:
case <-ctx.Done():
    t.Fatal("Timed out waiting for ExitIdle on the idle child while another child was blocked")
}
select {
case err := <-updateDone:
    t.Fatalf("UpdateClientConnState() returned (%v) before the blocked child was released", err)
default:
}
close(release)
```

- The barrier belongs to the second update of `blockedAddr` only (the `&&` makes the counter count only that child's updates; `DisableAutoReconnect: true` so no other ExitIdle source).
- B's signal is the last statement of the stub `ExitIdle`; no configured callback follows it.
- After B's signal the test asserts A's update is still outstanding, and only then `close(release)`.

Control runs (coarse-lock model; and coarse-lock + shared-deadline preemption after `idleChild.ExitIdle()`):

```sh
verify/repro/c23_coarse.sh 0c79b5be417ca395f6ab786120363624c4584892 'exitIdle|updateClientConnState' TestEndpointShardingExitIdleNotBlockedByOtherChild 8
verify/repro/c2_shared_deadline.sh 0c79b5be417ca395f6ab786120363624c4584892 'exitIdle|updateClientConnState' TestEndpointShardingExitIdleNotBlockedByOtherChild 'idleChild.ExitIdle()' 8
```

```console
371-func (bw *balancerWrapper) exitIdle() {
372:	verifyCoarseMu.Lock()
386-func (bw *balancerWrapper) updateClientConnState(ccs balancer.ClientConnState) error {
387:	verifyCoarseMu.Lock()
run 1: FAIL
    endpointsharding_ext_test.go:458: Timed out waiting for ExitIdle on the idle child while another child was blocked
...
coarse-lock model: 0/8 PASS, 8/8 FAIL
```

```console
454-	idleChild.ExitIdle()
455:	time.Sleep(defaultTestTimeout + time.Second) // verify: preemption until the shared deadline has fired
run 1: FAIL
    endpointsharding_ext_test.go:459: Timed out waiting for ExitIdle on the idle child while another child was blocked
run 2: FAIL
    endpointsharding_ext_test.go:463: UpdateClientConnState() returned (<nil>) before the blocked child was released
...
shared-deadline interleaving under coarse lock: 0/8 accepted, 8/8 rejected
```

Observation: the test rejects a coarse-lock implementation in 8/8 plain runs and also in 8/8 runs of the shared-deadline interleaving (the post-signal `updateDone` check catches a deadline-released A). This branch does contain an independent-progress regression with an operation-specific hold and terminal completion evidence before release.

## C4

Claim: `TestDecoupledChildProgress` and `TestSameChildMutualExclusion` (audited branch, `balancer/endpointsharding/endpointsharding_test.go`) contain setup or failure-path waits that depend indefinitely on production operations. Parts adjudicated separately.

Method: `verify/repro/c4_run.sh <commit> <point> <Test>` installs `verify/repro/c4_hook.go.txt` in a scratch worktree of `81201fc0` and inserts `verifyC4("setup")` at the top of `endpointState.updateClientConnStateLocked`, `verifyC4("exit_before")` after `childMu.Lock()` in `endpointState.exitIdle`, and `verifyC4("exit_after")` after `childLB.ExitIdle()`. When `VERIFY_C4=<point>` matches (set by the script) the operation parks forever. Test runs with `-timeout 30s`.

```sh
verify/repro/c4_run.sh 81201fc085e37de0ffb23505ac7831350b326c33 setup TestDecoupledChildProgress
verify/repro/c4_run.sh 81201fc085e37de0ffb23505ac7831350b326c33 setup TestSameChildMutualExclusion
verify/repro/c4_run.sh 81201fc085e37de0ffb23505ac7831350b326c33 exit_before TestDecoupledChildProgress
verify/repro/c4_run.sh 81201fc085e37de0ffb23505ac7831350b326c33 exit_before TestSameChildMutualExclusion
verify/repro/c4_run.sh 81201fc085e37de0ffb23505ac7831350b326c33 exit_after TestSameChildMutualExclusion
```

### setup_wait — both tests call the initial `lb.UpdateClientConnState` synchronously with no deadline

```console
# setup / TestDecoupledChildProgress
verifyC4: production operation stalled at "setup"
panic: test timed out after 30s
goroutine 22 [select (no cases)]:
google.golang.org/grpc/balancer/endpointsharding.(*endpointState).updateClientConnStateLocked(...)
google.golang.org/grpc/balancer/endpointsharding.(*endpointSharding).UpdateClientConnState(...)
	.../balancer/endpointsharding/endpointsharding_test.go:262 +0x489
FAIL	google.golang.org/grpc/balancer/endpointsharding	30.032s
# setup / TestSameChildMutualExclusion
google.golang.org/grpc/balancer/endpointsharding.(*endpointState).updateClientConnStateLocked(...)
google.golang.org/grpc/balancer/endpointsharding.(*endpointSharding).UpdateClientConnState(...)
	.../balancer/endpointsharding/endpointsharding_test.go:373 +0x3a8
FAIL	google.golang.org/grpc/balancer/endpointsharding	30.092s
```

Both hang in the test goroutine's own initial update (lines 262 and 373) until the go-test alarm. **Held.**

### teardown_completion_check — `lb.Close` follows only an update-worker join

Source (both tests, lines 250–266 and 399–415):

```go
releaseAndWait := func() bool {
    releaseOnce.Do(func() { close(child1Release) })      // / close(updateHold)
    select {
    case <-workerDone:                                    // only the UpdateClientConnState worker
        return true
    case <-time.After(2 * time.Second):
        t.Error("worker goroutine timed out or deadlocked during child1 release")
        return false
    }
}
defer func() {
    if releaseAndWait() {
        lb.Close()
    }
}()
```

`ChildState.ExitIdle` is asynchronous in the audited code (`endpointsharding.go:267`: `ExitIdle: func() { go epState.exitIdle() }`), so the idle-exit operation is a separate goroutine that `workerDone` does not cover. Exercised by the `exit_after` run on `TestSameChildMutualExclusion`: the update worker finished, the test body completed, and the deferred `lb.Close()` (called from the function epilogue, line 473) blocked on the child mutex held by the unsettled `exitIdle` goroutine:

```console
verifyC4: production operation stalled at "exit_after"
panic: test timed out after 30s
goroutine 9 [sync.Mutex.Lock]:
google.golang.org/grpc/balancer/endpointsharding.(*endpointState).close(0xc000188c80)
google.golang.org/grpc/balancer/endpointsharding.(*endpointSharding).Close(0xc0000f6fc0)
	.../endpointsharding_test.go:417 +0x4f
	.../endpointsharding_test.go:473 +0xbe7
goroutine 19 [select (no cases)]:
google.golang.org/grpc/balancer/endpointsharding.(*endpointState).exitIdle(0xc000188c80)
FAIL	google.golang.org/grpc/balancer/endpointsharding	30.021s
```

**Held.**

### teardown_failure_path — an idle-exit timeout reaches deferred `lb.Close` while the idle-exit operation is unsettled

`exit_before` on `TestSameChildMutualExclusion` (full output):

```console
instrumented:
376:	verifyC4("setup")
397:	verifyC4("exit_before")
400:		verifyC4("exit_after")
verifyC4: production operation stalled at "exit_before" (skipped 0 earlier hits)
    endpointsharding_test.go:464: ExitIdle never finished after update released
panic: test timed out after 30s
goroutine 9 [sync.Mutex.Lock]:
google.golang.org/grpc/balancer/endpointsharding.(*endpointState).close(0xc000188c80)
google.golang.org/grpc/balancer/endpointsharding.(*endpointSharding).Close(0xc0000f6fc0)
	/home/ubuntu/c4wt.qAvs/balancer/endpointsharding/endpointsharding_test.go:417 +0x4f
	/home/ubuntu/c4wt.qAvs/balancer/endpointsharding/endpointsharding_test.go:464 +0xb65
goroutine 13 [select (no cases)]:
google.golang.org/grpc/balancer/endpointsharding.(*endpointState).exitIdle(0xc000188c80)
FAIL	google.golang.org/grpc/balancer/endpointsharding	30.040s
exit=1
```

The idle-exit timeout at line 464 (`t.Fatal`) runs the deferred cleanup; `releaseAndWait()` returns true (the update worker had already joined), so `lb.Close()` runs and blocks on `childMu` owned by the parked `exitIdle` goroutine. **Held** (on `TestSameChildMutualExclusion`). For contrast, `exit_before` on `TestDecoupledChildProgress` ended in 12.4 s with `endpointsharding_test.go:288: worker goroutine timed out or deadlocked during child1 release` and no hang — there the update worker also failed to join, so `Close` was skipped.

### completion_receive — `<-callDone` in `TestSameChildMutualExclusion`

```go
callDone := make(chan struct{})
go func() {
    targetChildState.ExitIdle()   // = go epState.exitIdle()  (endpointsharding.go:267)
    close(callDone)
}()
...
<-callDone
```

Because `ChildState.ExitIdle` only spawns the production operation, `callDone` closes immediately regardless of whether `endpointState.exitIdle` returns. Observed in the `exit_after` run above: with `exitIdle` parked forever inside the child lock, the test goroutine passed `<-callDone` and the maxSeen check and reached the function epilogue (deferred `Close` frame at line 473). **Did not hold** — this receive does not depend on the production operation.

Summary: 3 of 4 parts held (setup_wait, teardown_completion_check, teardown_failure_path); completion_receive refuted.

Impact: if a regression makes the initial child update or an idle-exit operation block, these maintained tests hang the whole package until the go-test timeout instead of failing with their assertion message; the setup hang occurs before any assertion runs. Next: bound the initial updates with a goroutine+select deadline, and make the deferred cleanup wait on (or skip `Close` for) outstanding idle-exit work, e.g. have the child stub signal ExitIdle return and join it with a deadline before `Close`.

## C5

Claim (branch `evalon/grpc-go-en-c0a5b0d3`, head `0bc4bcbcb5d9d12aa89f3ac28974c8f5239389cb`): a construction-time Idle notification can have its queued reconnect consumed before initial configuration is usable, with no reconnect delivered afterwards. Parts: *preconfiguration_execution*, *lost_reconnection_delivery*.

Method: `verify/repro/c5_run.sh` installs `verify/repro/c5_hook.go.txt` (exported `VerifyC5AfterBuild func()` hook) right after construction releases `childMu`, before `newChildren.Set` / initial configuration, and runs `verify/repro/c5_controlled_test.go`. The test child reports IDLE synchronously from its builder; the hook blocks until the queued reconnect has run, then lets configuration proceed; the test then waits 500 ms for any later reconnect.

```sh
verify/repro/c5_run.sh 0bc4bcbcb5d9d12aa89f3ac28974c8f5239389cb
```

```console
instrumented:
165-			childBalancer.child = es.childBuilder(childBalancer, es.bOpts)
166-			childBalancer.childMu.Unlock()
167-			if VerifyC5AfterBuild != nil {
168:				VerifyC5AfterBuild()
169-			}
170-		}
171-		newChildren.Set(endpoint, childBalancer)
=== RUN   TestC5Controlled
    c5_controlled_test.go:66: pre-configuration window: queued reconnect ran; configured=false
    c5_controlled_test.go:83: after configuration + 500ms: preConfigExits=1 postConfigExits=0 aggregate=IDLE childStates=1
    c5_controlled_test.go:85: construction-time reconnect consumed before configuration; no reconnect delivered after configuration
--- FAIL: TestC5Controlled (0.50s)
FAIL
FAIL	google.golang.org/grpc/balancer/endpointsharding	0.517s
exit=1
```

- *preconfiguration_execution*: `childMu.Unlock()` (line 166) happens before registration (line 171) and configuration; the queued reconnect ran with `configured=false`. **Held.**
- *lost_reconnection_delivery*: after configuration succeeded, `postConfigExits=0` and the aggregate stays `IDLE`. **Held.**

Impact: a child that reports IDLE while being built (e.g. a pick-first child without addresses yet) loses its only automatic reconnect; the endpoint stays IDLE after configuration until something else calls ExitIdle (a new resolver update or an RPC-triggered exit), so the channel does not start connecting on its own. The window is a real scheduling interleaving (the queued goroutine runs between unlock and configuration). Next: hold `childMu` across construction *and* the initial `UpdateClientConnState`, or record a pending-reconnect flag and deliver it once configuration completes.

## C6

Claim (branch `evalon/grpc-go-en-a68de200`, head `c7c140815bcdc7e6517f4ecd0c1eec9a6268e605`): a child-state callback admitted before a configuration batch publishes a partially updated aggregate while the batch is in progress. Parts: *inhibition_admission*, *batch_overlap*.

Source on the branch (`endpointsharding.go` `updateState`): the inhibition flag is read with an atomic load *before* `es.mu` is acquired and not rechecked afterwards.

```go
if es.inhibitChildUpdates.Load() {
    return
}
// ... es.mu.Lock(); build aggregate; es.cc.UpdateState(...)
```

Method: `verify/repro/c6_run.sh` inserts `verifyC6AfterAdmission()` (from `verify/repro/c6_hook.go.txt`) right after that check and runs `verify/repro/c6_admitted_callback_test.go`: child B's READY callback is admitted and paused; a batch starts and child A's update is held; the callback is resumed; every parent publication is recorded.

```sh
verify/repro/c6_run.sh c7c140815bcdc7e6517f4ecd0c1eec9a6268e605
```

```console
instrumented:
245-	if es.inhibitChildUpdates.Load() {
246-		return
247-	}
248:	verifyC6AfterAdmission()
=== RUN   TestC6AdmittedCallbackPublishesDuringBatch
    c6_admitted_callback_test.go:95: after initial batch: 1 publications, last=CONNECTING
    c6_admitted_callback_test.go:119: callback admitted (passed inhibition check) and paused before es.mu
    c6_admitted_callback_test.go:128: batch in progress: child "A" held inside UpdateClientConnState
    c6_admitted_callback_test.go:148: publication while batch in progress: aggregate=READY children=[B=READY A=CONNECTING]
    c6_admitted_callback_test.go:156: admitted callback published 1 aggregate(s) while the batch was still in progress
--- FAIL: TestC6AdmittedCallbackPublishesDuringBatch (0.00s)
FAIL
FAIL	google.golang.org/grpc/balancer/endpointsharding	0.014s
```

- *inhibition_admission*: admission check outside `es.mu`, no revalidation. **Held** (callback passed the check and later published).
- *batch_overlap*: while A was still inside `UpdateClientConnState`, the parent received `READY` with children `[B=READY A=CONNECTING]`. **Held.**

Impact: the parent ClientConn can receive an aggregate picker built from a half-applied configuration (children from the old config mixed with the new), defeating the batch's "one consolidated publication" guarantee; picks can go to endpoints the new config is removing. It requires a child callback racing the start of a resolver update — rare per event but routine at scale. Next: re-check `inhibitChildUpdates` after acquiring `es.mu` (or make the flag mutex-protected and set it under `es.mu`).

## C7

Claim (branch `evalon/grpc-go-en-a68de200`, head `c7c140815bcdc7e6517f4ecd0c1eec9a6268e605`): the committed endpoint-sharding suite lacks preservation assertions for five named behaviors.

Method: `verify/repro/c7_mutations.sh` applies one production mutation at a time in a scratch worktree (mutation 0 is a no-op baseline) and runs the whole committed package suite under `-race`. A part is refuted when the suite detects the mutation, confirmed when the suite still passes.

```sh
verify/repro/c7_mutations.sh c7c140815bcdc7e6517f4ecd0c1eec9a6268e605
```

```console
### mutation 0_baseline
ok  	google.golang.org/grpc/balancer/endpointsharding	1.126s
exit=0
### mutation 1_same_child_contention
-	bw.mu.Lock()
-	defer bw.mu.Unlock()
WARNING: DATA RACE
    testing.go:1617: race detected during execution of test
--- FAIL: Test (0.11s)
    --- FAIL: Test/EndpointShardingSynchronousChildStateUpdates (0.01s)
FAIL	google.golang.org/grpc/balancer/endpointsharding	0.131s
exit=1
### mutation 2_retained_closed_handles
-	if bw.child == nil || bw.isClosed {
+	if bw.child == nil {
ok  	google.golang.org/grpc/balancer/endpointsharding	1.132s
exit=0
### mutation 3_child_error_continuation
-		}); err != nil && ret == nil {
+		}); err != nil {
+			return err
+		} else if false {
ok  	google.golang.org/grpc/balancer/endpointsharding	1.136s
exit=0
### mutation 4_synchronous_lifecycle_callbacks
+	bw.mu.Lock()
+	bw.mu.Unlock()
panic: test timed out after 1m0s
		Test/EndpointShardingBasic (1m0s)
FAIL	google.golang.org/grpc/balancer/endpointsharding	60.079s
exit=1
### mutation 5_complete_publication_observation
-	if es.inhibitChildUpdates.Load() {
+	if false && es.inhibitChildUpdates.Load() {
FAIL	google.golang.org/grpc/balancer/endpointsharding	0.020s
exit=1
```

Per part:

- *same_child_contention*: removing the per-child mutex is detected (race + FAIL). **Did not hold.**
- *retained_closed_handles*: removing the `isClosed` guard used when operating through a retained handle after removal/closure — suite still passes. **Held.**
- *child_error_continuation*: returning on the first child update error (skipping the remaining children) — suite still passes. **Held.**
- *synchronous_lifecycle_callbacks*: re-acquiring the child mutex in the callback path deadlocks and the suite times out. **Did not hold.**
- *complete_publication_observation*: disabling batch inhibition makes the suite fail (extra publications counted). **Did not hold.**

Summary: 2 of 5 parts held.

Impact: two behaviors the change set relies on have no regression guard: (a) calls through a stale `ChildState` handle after its endpoint was removed/closed would reach a closed child balancer (use-after-close) without any test failing; (b) an early return on a child's config error would silently leave the remaining endpoints unconfigured without any test failing. Next: add a test that retains a `ChildState`, removes its endpoint, calls `ExitIdle`/updates via the handle and asserts the closed child is not invoked; add a test where one child's `UpdateClientConnState` returns an error and assert every other child still received its update and the error is returned.

## C8

Claim (branch `evalon/grpc-go-en-d1fc592e`, head `de830fa6440869eb415ea94ea50c986c9b1f9d8d`): the maintained independent-progress regression hangs in cleanup after its progress assertion fails under coarse-lock contention. Parts: *failure_path_release*, *cleanup_wait*, *coarse_lock_trigger*.

Method: `verify/repro/c8_run.sh` checks out the coarse-lock base `bf9e7cd3` in a scratch worktree, copies the branch's `endpointsharding_ext_test.go`, adapts only the idle-exit call (`ep1State.ExitIdle()` → `ep1State.Balancer.ExitIdle()`, the base's API), leaves cleanup untouched, and runs `TestEndpointShardingExitIdleWhileOtherChildBlocked` with `-timeout 45s` under external `timeout 120`.

```sh
verify/repro/c8_run.sh de830fa6440869eb415ea94ea50c986c9b1f9d8d
```

```console
test adaptation vs de830fa6440869eb415ea94ea50c986c9b1f9d8d:
467c467
<     ep1State.ExitIdle()
---
>     ep1State.Balancer.ExitIdle()
production diff vs coarse-lock base:
(none)
    endpointsharding_ext_test.go:471: Timed out waiting for ExitIdle to be delivered to the first child while the second child's update is blocked
panic: test timed out after 45s
goroutine 10 [sync.Mutex.Lock]:
google.golang.org/grpc/balancer/endpointsharding.(*endpointSharding).Close(...)
goroutine 11 [chan receive]:
google.golang.org/grpc/balancer/endpointsharding_test.s.TestEndpointShardingExitIdleWhileOtherChildBlocked.func1(...)
	/home/ubuntu/wt/c8-base/balancer/endpointsharding/endpointsharding_ext_test.go:411 +0x168
google.golang.org/grpc/balancer/endpointsharding.(*balancerWrapper).updateClientConnStateLocked(...)
google.golang.org/grpc/balancer/endpointsharding.(*endpointSharding).UpdateClientConnState(...)
goroutine 12 [sync.Mutex.Lock]:
google.golang.org/grpc/balancer/endpointsharding.(*balancerWrapper).ExitIdle.func1()
FAIL	google.golang.org/grpc/balancer/endpointsharding	45.048s
FAIL
exit 1
```

Test source lines (branch file): `411: <-blockEp2Update` (unconditional receive in the child stub), `432: defer es.Close()`, `471: t.Fatal("Timed out waiting for ExitIdle ...")`, `482: close(blockEp2Update)` — the only release, reached only after the assertion at 471 passes.

- *coarse_lock_trigger*: under the coarse lock the assertion at line 471 fails. **Held.**
- *failure_path_release*: goroutine 11 is still parked at line 411 on `blockEp2Update` after the failure; nothing on the failure path closes it. **Held.**
- *cleanup_wait*: the deferred `es.Close()` (goroutine 10) waits on the parent mutex held by the blocked update until the 45 s alarm. **Held.**

Impact: the regression that exists precisely to catch a coarse-lock reintroduction reports it as a package-wide timeout (10 min default in CI) with a goroutine dump rather than as its assertion failure, and suppresses the rest of the package's results. Next: `defer close(blockEp2Update)` (via `sync.Once`) registered after `defer es.Close()` so it runs first, or select on a context in the stub.

## C9

Claim (branch `evalon/grpc-go-en-6a89ba15`, head `27844e1d3c074da874f70b84e321540a9b998e1b`): one parent `ExitIdle` produces multiple aggregate publications when two children synchronously report state during it.

`verify/repro/c9_exitidle_publications_test.go` builds endpointsharding over two children whose `ExitIdle` synchronously calls `cc.UpdateState(CONNECTING)`, records every parent `UpdateState`, and counts publications during one `es.ExitIdle()`.

```sh
cd <worktree of 27844e1d> && cp <repo>/verify/repro/c9_exitidle_publications_test.go balancer/endpointsharding/ && go test -race -count=1 -run 'TestC9' -v ./balancer/endpointsharding/
```

```console
HEAD=27844e1d3c074da874f70b84e321540a9b998e1b
=== RUN   TestC9ParentExitIdlePublications
    c9_exitidle_publications_test.go:70: parent publications during one ExitIdle call: 2 [CONNECTING CONNECTING]
    c9_exitidle_publications_test.go:72: one parent ExitIdle produced 2 aggregate publications, want <= 1
--- FAIL: TestC9ParentExitIdlePublications (0.00s)
FAIL
FAIL	google.golang.org/grpc/balancer/endpointsharding	0.013s
exit=1
```

Same test on the audited branch (control, shows the test can pass):

```console
HEAD=81201fc085e37de0ffb23505ac7831350b326c33
=== RUN   TestC9ParentExitIdlePublications
    c9_exitidle_publications_test.go:70: parent publications during one ExitIdle call: 1 [CONNECTING]
--- PASS: TestC9ParentExitIdlePublications (0.00s)
PASS
ok  	google.golang.org/grpc/balancer/endpointsharding	1.014s
exit=0
```

Observation: on 6a89ba15 one parent `ExitIdle` with two synchronously reporting children emits two aggregate publications (one per child callback); the audited branch batches them into one.

Impact: each parent ExitIdle causes N picker rebuilds/publications for N endpoints (O(N) parent picker churn, each triggering picker swaps in the channel), and intermediate aggregates are visible. Functionally harmless per update but wasteful for large endpoint sets (ring-hash). Next: inhibit child publications around the child loop in `endpointSharding.ExitIdle` and publish once at the end, as the audited branch does.

## C10

Claim: the delivered endpoint-sharding / ring-hash changes fail at least one repository formatting, import, revive or staticcheck check.

Commands (isolated worktree of the audited commit, before any external/eval test was added; tools at the versions pinned by `scripts/vet.sh -install`):

```sh
git worktree add --detach ~/c10wt 81201fc085e37de0ffb23505ac7831350b326c33 && cd ~/c10wt
./scripts/vet.sh -install
gofmt -s -l balancer/endpointsharding balancer/ringhash
goimports -l balancer/endpointsharding balancer/ringhash
revive -set_exit_status=1 -formatter plain -config scripts/revive.toml ./balancer/endpointsharding/... ./balancer/ringhash/...
staticcheck -checks all ./balancer/endpointsharding/... ./balancer/ringhash/...
./scripts/vet.sh
```

```console
HEAD=81201fc085e37de0ffb23505ac7831350b326c33
+ gofmt -s -l balancer/endpointsharding balancer/ringhash
gofmt exit=0 (no files listed = clean)
+ goimports -l balancer/endpointsharding balancer/ringhash
goimports exit=0
+ revive -set_exit_status=1 -formatter plain -config scripts/revive.toml ./balancer/endpointsharding/... ./balancer/ringhash/...
revive exit=0
+ staticcheck -checks all ./balancer/endpointsharding/... ./balancer/ringhash/...
balancer/endpointsharding/endpointsharding_ext_test.go:51:2: package "google.golang.org/grpc/interop/grpc_testing" is being imported more than once (ST1019)
balancer/ringhash/ringhash_e2e_test.go:62:2: package "google.golang.org/grpc/interop/grpc_testing" is being imported more than once (ST1019)
balancer/ringhash/ringhash_test.go:689:2: addr.BalancerAttributes is deprecated: ... (SA1019)
balancer/ringhash/ringhash_test.go:689:28: addr.BalancerAttributes is deprecated: ... (SA1019)
staticcheck(raw, unfiltered) exit=1
+ ./scripts/vet.sh
SUCCESS
vet.sh exit=0
```

The raw (unfiltered) staticcheck diagnostics are pre-existing and are the ones `scripts/vet.sh` explicitly filters (`grep -v '(ST1019)\|\(other import of\)'`, SA1019 allow-list). Same command on the coarse-lock base:

```sh
cd <worktree of bf9e7cd3430df40d0732ba42eb88bd5f2cc63407> && staticcheck -checks all ./balancer/endpointsharding/... ./balancer/ringhash/... | grep -v 'other import'
```

```console
balancer/endpointsharding/endpointsharding_ext_test.go:51:2: package "google.golang.org/grpc/interop/grpc_testing" is being imported more than once (ST1019)
balancer/ringhash/ringhash_e2e_test.go:62:2: package "google.golang.org/grpc/interop/grpc_testing" is being imported more than once (ST1019)
balancer/ringhash/ringhash_test.go:687:2: addr.BalancerAttributes is deprecated: ... (SA1019)
balancer/ringhash/ringhash_test.go:687:28: addr.BalancerAttributes is deprecated: ... (SA1019)
base staticcheck exit=1
```

(The ringhash_test.go line shift 687→689 is the branch's 2 added lines; the diagnostic itself is unchanged.) Observation: gofmt, goimports, revive and the repository's filtered staticcheck all pass on both touched packages; `./scripts/vet.sh` prints `SUCCESS` and exits 0. No source-level violation introduced by the delivered changes.
