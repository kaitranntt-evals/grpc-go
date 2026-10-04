Observations only; verdict report is delivered separately. All commands were run on Linux, go1.25.7, from `~/repos/grpc-go` on the verify branch (based on [grpc-go-endpointsharding-decouple-locking-perfect](https://github.com/kaitranntt-evals/grpc-go/tree/grpc-go-endpointsharding-decouple-locking-perfect)). Every claim targets a branch in a second repository, [kaitranntt-evals/grpc-go-endpointsharding-decouple-locking](https://github.com/kaitranntt-evals/grpc-go-endpointsharding-decouple-locking); each script below fetches its claim branch from there into a throwaway `git worktree`, so nothing in this checkout is modified. Task base commit: `bf9e7cd3430df40d0732ba42eb88bd5f2cc63407`. `EVAL_FIXTURE` is the byte-exact `tests/eval_endpointsharding_test.go` from the attached `eval_tests.zip`.

Test names: both packages run their tests through `grpctest.RunSubTests`, so `func (s) TestFoo` is selected with `-run 'Test/Foo$'`.

## C1

Branch: [evalon/grpc-go-en-873d5e04](https://github.com/kaitranntt-evals/grpc-go-endpointsharding-decouple-locking/tree/evalon/grpc-go-en-873d5e04) at `2b78624a`. Verdict basis: both parts observed (cleanup_wait, failure_path).

Changed concurrency tests on the branch (`git diff --stat bf9e7cd3 HEAD`): `TestChildExitIdleWhileAnotherChildIsBlocked` (4 sub-tests) in `balancer/endpointsharding/endpointsharding_ext_test.go` and `TestPickConnectsWhileAnotherEndpointUpdateIsBlocked` in `balancer/ringhash/ringhash_test.go`. Each starts a worker goroutine (`go func() { ... operationDone <- err }()` / `go func() { updateDone <- b.UpdateClientConnState(state) }()`) and registers a deferred cleanup that closes the fixture barrier `release` and then performs an untimed receive from the worker-completion channel. No statement before that receive observes the worker's completion; the test body's only waits are `select`s on `entered` / `exitedIdle` / `NewSubConnCh` / `ConnectCh` versus `ctx.Done()`, and every `t.Fatal`/`t.Fatalf` after the `defer` runs the cleanup via `runtime.Goexit`.

Command (replays everything below; about 4 minutes):

```sh
sh verify/repro/c1_run.sh
```

What it does: (1) prints the cleanup code; (2) runs both tests unmodified; (3) applies `verify/repro/c1_mutation_hold_mu_across_child_update.patch` (production-only regression: `endpointSharding.UpdateClientConnState` keeps `es.mu` locked across the call into an existing child, so the worker self-deadlocks in the child's synchronous `UpdateState`) and runs the unmodified tests with a shortened binary timeout; (4) additionally applies `verify/repro/c1_control_bounded_cleanup.patch` (test-only: the join and `b.Close()` get a `time.After(defaultTestTimeout)` bound) and reruns.

Output:

```console
### worktree of evalon/grpc-go-en-873d5e04 at 2b78624a9f80fa2fb0e37c693a7cf629e4f22100
### 1. cleanup code under audit
452-			defer func() {
453-				armed.Store(false)
454-				close(release)
455:				if err := <-operationDone; err != nil {
456-					t.Errorf("%s failed: %v", operation, err)
457-				}
458-			}()
459-
460-			select {
461-			case <-entered:
809-	defer func() {
810-		close(release)
811:		if err := <-updateDone; err != nil {
812-			t.Errorf("UpdateClientConnState() failed: %v", err)
813-		}
814-	}()
815-	select {
816-	case <-entered:
### 2. unmodified branch: both tests pass
ok  	google.golang.org/grpc/balancer/endpointsharding	1.020s
ok  	google.golang.org/grpc/balancer/ringhash	1.039s
### 3. production regression (es.mu held across the call into an existing child), tests unmodified
--- endpointsharding (go test -timeout 60s; the test's own deadline is 10s)
=== RUN   Test
--- PASS: Test (0.00s)
=== RUN   Test
=== RUN   Test/ChildExitIdleWhileAnotherChildIsBlocked
=== RUN   Test/ChildExitIdleWhileAnotherChildIsBlocked/UpdateClientConnState
    balancer.go:193: testutils.BalancerClientConn: UpdateState({IDLE 0xc000135c00})
    endpointsharding_ext_test.go:472: Idle child could not exit idle while another child's UpdateClientConnState was blocked
panic: test timed out after 1m0s
		Test (1m0s)
		Test/ChildExitIdleWhileAnotherChildIsBlocked (1m0s)
		Test/ChildExitIdleWhileAnotherChildIsBlocked/UpdateClientConnState (1m0s)
FAIL	google.golang.org/grpc/balancer/endpointsharding	60.092s
FAIL
--- test goroutine at the time of the panic:
google.golang.org/grpc/balancer/endpointsharding_test.s.TestChildExitIdleWhileAnotherChildIsBlocked.func1.3()
	/tmp/tmp.NOPpfHuhSs/wt/balancer/endpointsharding/endpointsharding_ext_test.go:455 +0x99
runtime.Goexit()
testing.(*common).FailNow(0xc000103880)
--- ringhash (go test -timeout 45s; the test's own deadline is 10s)
=== RUN   Test
=== RUN   Test/PickConnectsWhileAnotherEndpointUpdateIsBlocked
    balancer.go:193: testutils.BalancerClientConn: UpdateState({IDLE 0xc0000d7f20})
    ringhash_test.go:830: Idle endpoint did not create a SubConn while another endpoint's update was blocked
panic: test timed out after 45s
		Test (45s)
		Test/PickConnectsWhileAnotherEndpointUpdateIsBlocked (45s)
FAIL	google.golang.org/grpc/balancer/ringhash	45.039s
FAIL
--- test goroutine at the time of the panic:
google.golang.org/grpc/balancer/ringhash.s.TestPickConnectsWhileAnotherEndpointUpdateIsBlocked.func4()
	/tmp/tmp.NOPpfHuhSs/wt/balancer/ringhash/ringhash_test.go:811 +0x5e
runtime.Goexit()
testing.(*common).FailNow(0xc000003dc0)
	/tmp/tmp.NOPpfHuhSs/wt/balancer/ringhash/ringhash_test.go:830 +0x16ae
### 4. control: same regression, endpointsharding cleanup bounded (join and Close)
=== RUN   Test
--- PASS: Test (0.00s)
=== RUN   Test
=== RUN   Test/ChildExitIdleWhileAnotherChildIsBlocked
=== RUN   Test/ChildExitIdleWhileAnotherChildIsBlocked/UpdateClientConnState
    balancer.go:193: testutils.BalancerClientConn: UpdateState({IDLE 0xc000135c00})
    endpointsharding_ext_test.go:478: Timed out waiting for blocked child's UpdateClientConnState
    endpointsharding_ext_test.go:471: UpdateClientConnState worker did not finish within 10s of release
    endpointsharding_ext_test.go:417: Close did not return within 10s
    grpctest.go:45: Leaked goroutine: goroutine 25 [sync.Mutex.Lock]:
    grpctest.go:45: Leaked goroutine: goroutine 35 [sync.Mutex.Lock]:
    grpctest.go:77: Goroutine leak check disabled for future tests
--- FAIL: Test (40.14s)
    --- FAIL: Test/ChildExitIdleWhileAnotherChildIsBlocked (40.14s)
        --- FAIL: Test/ChildExitIdleWhileAnotherChildIsBlocked/UpdateClientConnState (30.10s)
FAIL
FAIL	google.golang.org/grpc/balancer/endpointsharding	40.167s
FAIL
panic lines in control run: 0
```

Reading of the output:

- *cleanup_wait:* lines 455 and 811 are plain `<-operationDone` / `<-updateDone` receives inside the deferred cleanup, preceded only by `close(release)`.
- *failure_path:* with the regression in place each test reports its assertion failure at its own 10 s deadline (`endpointsharding_ext_test.go:472`, `ringhash_test.go:830`) and then never returns; the binary is killed by `panic: test timed out`, and the dumped test goroutine is `t.Fatalf -> FailNow -> runtime.Goexit -> deferred cleanup (func1.3 at :455 / func4 at :811) [chan receive]`. Which assertion fires depends on `rotateEndpoints`' random order: an earlier run of the same command hit the other failure path, the `entered` timeout, with the identical hang:

```console
    endpointsharding_ext_test.go:463: Timed out waiting for blocked child's UpdateClientConnState
panic: test timed out after 1m0s
goroutine 11 [chan receive]:
google.golang.org/grpc/balancer/endpointsharding_test.s.TestChildExitIdleWhileAnotherChildIsBlocked.func1.3()
	/home/ubuntu/wt/873d5e04/balancer/endpointsharding/endpointsharding_ext_test.go:455 +0x99
runtime.Goexit()
testing.(*common).FailNow(0xc0000befc0)
testing.(*common).Fatalf(0xc0000befc0, {0x1228df3, 0x28}, {0xc0000f7d88, 0x1, 0x1})
google.golang.org/grpc/balancer/endpointsharding_test.s.TestChildExitIdleWhileAnotherChildIsBlocked.func1(0xc0000befc0)
	/home/ubuntu/wt/873d5e04/balancer/endpointsharding/endpointsharding_ext_test.go:463 +0x10fb
```

- *control:* with the cleanup bounded, the same regression produces an ordinary `--- FAIL` with three diagnostics and zero `panic: test timed out` lines. Bounding only the join was not enough: in an intermediate run the join timed out cleanly and the test then hung in the earlier-registered `defer b.Close()`, which queues behind the same stuck worker on the child's mutex — so the control bounds both.

Impact reasoning: when everything works the cleanup is harmless (step 2 passes). The receive only matters when the worker cannot finish after `release` is closed — i.e. exactly when the code under test has a locking regression, which is what these tests exist to catch. In that case the test does not end at its 10 s deadline: it blocks until `go test`'s binary timeout (10 minutes by default; 60 s / 45 s here), the whole package binary is aborted by a panic, and every test scheduled after it in the package produces no result. The assertion message is still printed under `-v`, so the failure is diagnosable, but slowly and at the cost of the rest of the package run. Not triggered: the case where the worker is merely parked at `<-release` (a plain "idle child blocked" regression with a shared lock) — there `close(release)` lets the worker finish and the receive completes.

## C2

Branch: [evalon/grpc-go-en-c91051b2](https://github.com/kaitranntt-evals/grpc-go-endpointsharding-decouple-locking/tree/evalon/grpc-go-en-c91051b2) at `c9b5ef3e`.

Synchronization comments added or changed by the branch in `balancer/endpointsharding/` and the assertion each makes that is in the claim's scope (lock ownership, protected data, permitted acquisition order):

| Comment (location) | In-scope assertion | Runtime rule |
|---|---|---|
| `endpointSharding.updateMu` | serializes UpdateClientConnState / ResolverError / Close; never acquired when operating on a single child | R4, R4b, R7, R8 |
| `endpointSharding.mu` | must not be held during calls into a child; do not acquire any `balancerWrapper.mu` while holding it | R1, R2 |
| `balancerWrapper.mu` | synchronizes all calls into the child; guards `child`, `isClosed`; do not acquire while holding `es.mu` | R1, R3, R5 |
| `balancerWrapper.childState` | guarded by `es.mu` | R6 |
| `blockingChild.addr` (test file) | only accessed from calls made by endpointsharding, serialized per child | R3 + race detector |

Method: `verify/instrumentation/c2/instrument.py` rewrites `endpointsharding.go` in a throwaway worktree so the three mutexes become tracked types that record, per goroutine, which locks are held; it inserts a check before every access to `bw.child` / `bw.isClosed` (9 sites) and to a published `childState` (3 sites), and wraps every child balancer so each call into it (including the builder call) is checked on entry. The rules are in `verify/instrumentation/c2/audit_lockcheck.go.txt`. The workload is the branch's own endpointsharding tests, the eval fixture, an added probe, and the whole ringhash package (real `lazy`+`pickfirst` children with synchronous `UpdateState` callbacks), all under `-race`. `childState` writes inside `newChild` happen before the wrapper is published and are not checked.

Command:

```sh
EVAL_FIXTURE=~/eval/tests/eval_endpointsharding_test.go sh verify/instrumentation/c2/c2_lockcheck.sh
```

Output:

```console
### worktree of evalon/grpc-go-en-c91051b2 at c9b5ef3e31934ff4adb94004276cf1ab4f8eda9c
### 1. added/changed synchronization comments (diff vs task base bf9e7cd3, comment lines only)
+// asynchronously and never waits for operations on other children of the
+	// updateMu serializes the parent's operations that act on all children
+	// children. It is never acquired when operating on a single child, so
+	// operations on one child (e.g. exiting idle) do not wait for operations
+	// deadlock. To avoid deadlocks, do not acquire the mutex of any child
+	// (balancerWrapper.mu) while holding mu.
+// independently, so a child does not wait for operations on other children.
+	// mu synchronizes all calls into the child balancer and guards the fields
+	// does not block operations on other children. To avoid deadlocks, do not
+	// acquire mu while holding es.mu.
+	// methods on balancerWrapper to ensure proper synchronization.
+	// childState is guarded by es.mu.
+// only waits for operations on this child to complete.
### 2. instrument (mutex types -> tracked, checks before guarded accesses and child calls)
instrumented: bw data sites = 9
eval fixture installed
 balancer/endpointsharding/endpointsharding.go | 23 ++++++++++++++++++-----
 1 file changed, 18 insertions(+), 5 deletions(-)
### 3. endpointsharding package (branch tests + probe + eval fixture if given), -race
--- PASS: Test (0.00s)
--- PASS: TestEval_ChildExitIdleWhileAnotherChildBlocked (0.00s)
--- PASS: TestEval_SameChildMutualExclusion (0.74s)
--- PASS: TestEval_SynchronousChildUpdateDeadlock (0.00s)
--- PASS: TestEval_ConstructionIdleCallbackSafety (0.05s)
--- PASS: TestEval_ExistingChildIdleAttributeRace (0.05s)
--- FAIL: TestEval_ChildStateExitIdleCallback (0.00s)
--- FAIL: TestEval_BatchUpdateInhibition (0.01s)
--- PASS: TestEval_ResolverErrorConsolidatedNotification (0.00s)
--- PASS: TestEval_PickerStateAggregationPrecedence (0.00s)
    audit_c2_probe_test.go:53: OBSERVED: parent ExitIdle() delivered ExitIdle to child B within 200ms while child A's update was blocked in 10 of 20 trials
--- PASS: Test (2.13s)
FAIL
AUDIT-C2 checks=1231   violations=0    R0 es.mu acquisitions observed (bw.mu -> es.mu and updateMu -> es.mu are the permitted orders)
AUDIT-C2 checks=622    violations=0    R0b es.mu acquired while holding balancerWrapper.mu (synchronous child callback; permitted order exercised)
AUDIT-C2 checks=565    violations=0    R1 order: balancerWrapper.mu is not acquired while holding es.mu
AUDIT-C2 checks=71     violations=0    R2 ownership: es.mu is not held during a call into a child (Build)
AUDIT-C2 checks=70     violations=0    R2 ownership: es.mu is not held during a call into a child (Close)
AUDIT-C2 checks=198    violations=0    R2 ownership: es.mu is not held during a call into a child (ExitIdle)
AUDIT-C2 checks=6      violations=0    R2 ownership: es.mu is not held during a call into a child (ResolverError)
AUDIT-C2 checks=219    violations=0    R2 ownership: es.mu is not held during a call into a child (UpdateClientConnState)
AUDIT-C2 checks=71     violations=0    R3 ownership: call into child (Build) made with that child's balancerWrapper.mu held
AUDIT-C2 checks=70     violations=0    R3 ownership: call into child (Close) made with that child's balancerWrapper.mu held
AUDIT-C2 checks=198    violations=0    R3 ownership: call into child (ExitIdle) made with that child's balancerWrapper.mu held
AUDIT-C2 checks=6      violations=0    R3 ownership: call into child (ResolverError) made with that child's balancerWrapper.mu held
AUDIT-C2 checks=219    violations=0    R3 ownership: call into child (UpdateClientConnState) made with that child's balancerWrapper.mu held
AUDIT-C2 checks=206    violations=0    R4 order: updateMu is not acquired while holding any balancerWrapper.mu (single-child operation)
AUDIT-C2 checks=206    violations=0    R4b order: updateMu is not acquired while holding es.mu
AUDIT-C2 checks=1128   violations=0    R5 data: balancerWrapper.child/isClosed accessed with that wrapper's mu held
AUDIT-C2 checks=1352   violations=0    R6 data: published balancerWrapper.childState accessed with es.mu held
AUDIT-C2 checks=71     violations=0    R7 ownership: all-children operation (Build) reaches the child with updateMu held
AUDIT-C2 checks=70     violations=0    R7 ownership: all-children operation (Close) reaches the child with updateMu held
AUDIT-C2 checks=6      violations=0    R7 ownership: all-children operation (ResolverError) reaches the child with updateMu held
AUDIT-C2 checks=219    violations=0    R7 ownership: all-children operation (UpdateClientConnState) reaches the child with updateMu held
AUDIT-C2 checks=198    violations=0    R8 ownership: single-child ExitIdle reaches the child without updateMu held
AUDIT-C2 checks=565    violations=0    R9 ownership: bw.mu is unlocked by the goroutine that locked it
AUDIT-C2 checks=1231   violations=0    R9 ownership: es.mu is unlocked by the goroutine that locked it
AUDIT-C2 checks=206    violations=0    R9 ownership: updateMu is unlocked by the goroutine that locked it
AUDIT-C2 TOTAL VIOLATIONS: 0
FAIL	google.golang.org/grpc/balancer/endpointsharding	3.008s
FAIL
### 4. ringhash package, -race
--- PASS: Test (0.14s)
--- PASS: Test (13.86s)
PASS
AUDIT-C2 checks=589    violations=0    R0 es.mu acquisitions observed (bw.mu -> es.mu and updateMu -> es.mu are the permitted orders)
AUDIT-C2 checks=175    violations=0    R0b es.mu acquired while holding balancerWrapper.mu (synchronous child callback; permitted order exercised)
AUDIT-C2 checks=381    violations=0    R1 order: balancerWrapper.mu is not acquired while holding es.mu
AUDIT-C2 checks=115    violations=0    R2 ownership: es.mu is not held during a call into a child (Build)
AUDIT-C2 checks=100    violations=0    R2 ownership: es.mu is not held during a call into a child (Close)
AUDIT-C2 checks=29     violations=0    R2 ownership: es.mu is not held during a call into a child (ExitIdle)
AUDIT-C2 checks=137    violations=0    R2 ownership: es.mu is not held during a call into a child (UpdateClientConnState)
AUDIT-C2 checks=115    violations=0    R3 ownership: call into child (Build) made with that child's balancerWrapper.mu held
AUDIT-C2 checks=100    violations=0    R3 ownership: call into child (Close) made with that child's balancerWrapper.mu held
AUDIT-C2 checks=29     violations=0    R3 ownership: call into child (ExitIdle) made with that child's balancerWrapper.mu held
AUDIT-C2 checks=137    violations=0    R3 ownership: call into child (UpdateClientConnState) made with that child's balancerWrapper.mu held
AUDIT-C2 checks=88     violations=0    R4 order: updateMu is not acquired while holding any balancerWrapper.mu (single-child operation)
AUDIT-C2 checks=88     violations=0    R4b order: updateMu is not acquired while holding es.mu
AUDIT-C2 checks=747    violations=0    R5 data: balancerWrapper.child/isClosed accessed with that wrapper's mu held
AUDIT-C2 checks=1113   violations=0    R6 data: published balancerWrapper.childState accessed with es.mu held
AUDIT-C2 checks=115    violations=0    R7 ownership: all-children operation (Build) reaches the child with updateMu held
AUDIT-C2 checks=100    violations=0    R7 ownership: all-children operation (Close) reaches the child with updateMu held
AUDIT-C2 checks=137    violations=0    R7 ownership: all-children operation (UpdateClientConnState) reaches the child with updateMu held
AUDIT-C2 checks=29     violations=0    R8 ownership: single-child ExitIdle reaches the child without updateMu held
AUDIT-C2 checks=381    violations=0    R9 ownership: bw.mu is unlocked by the goroutine that locked it
AUDIT-C2 checks=589    violations=0    R9 ownership: es.mu is unlocked by the goroutine that locked it
AUDIT-C2 checks=88     violations=0    R9 ownership: updateMu is unlocked by the goroutine that locked it
AUDIT-C2 TOTAL VIOLATIONS: 0
ok  	google.golang.org/grpc/balancer/ringhash	15.060s
### 5. positive control: hold es.mu across the call into an existing child; the checker must flag it
AUDIT-C2 VIOLATION: R1 order: balancerWrapper.mu is not acquired while holding es.mu
AUDIT-C2 VIOLATION: R2 ownership: es.mu is not held during a call into a child (UpdateClientConnState)
panic: test timed out after 20s
FAIL	google.golang.org/grpc/balancer/endpointsharding	20.041s
FAIL
```

Reading of the output:

- 0 violations over every rule in both packages, with each rule exercised (non-zero `checks`), including the synchronous-callback order `balancerWrapper.mu -> es.mu` (R0b: 622 + 175 times) that the comments permit. No `WARNING: DATA RACE` lines.
- The checker is live: step 5 (positive control, `es.mu` held across the child call in `UpdateClientConnState`) is flagged as R1 and R2 violations before the resulting deadlock.
- The two `TestEval_*` failures in step 3 are unrelated to this claim: `TestEval_ChildStateExitIdleCallback` rejects the retained `ChildState.Balancer` field, and `TestEval_BatchUpdateInhibition` reports an intermediate parent update mid-batch (it passed in another run of the same binary, so it is timing-dependent on this branch). Neither concerns a comment.
- Out of the claim's stated scope (a waiting/scheduling promise, not lock ownership, protected data or acquisition order), but observed: the comment added on `endpointSharding.ExitIdle` — "Each child is acted upon independently, so a child does not wait for operations on other children" — does not hold for the synchronous parent `ExitIdle()`: it calls `bw.exitIdleSync()` on the children one after another, so when the blocked child comes first in map order the other child is not reached until the blocked one finishes (probe: reached in 10 of 20 trials here, 12 of 20 in an earlier run). `ChildState.ExitIdle()` (the path ringhash uses) is unaffected.

## C3

Branch: [evalon/grpc-go-en-cb3ba169](https://github.com/kaitranntt-evals/grpc-go-endpointsharding-decouple-locking/tree/evalon/grpc-go-en-cb3ba169) at `96932419`.

On this branch `ringhashBalancer.UpdateState` stores the reconnection handle as `balancer: childState` — the whole `endpointsharding.ChildState` value boxed into the `endpointsharding.ExitIdler` interface field `endpointState.balancer` — only when the endpoint is first seen. The branch for an already-known endpoint refreshes `es.state` (and weight / hash key) but never `es.balancer`.

Command:

```sh
sh verify/repro/c3_run.sh
```

The probe (`verify/repro/c3_stale_snapshot_test.go.txt`, an internal `package ringhash` test) builds a real ringhash balancer with one endpoint of weight 1, takes it IDLE -> CONNECTING -> READY through a pick and SubConn state updates, then sends a resolver update changing the endpoint weight to 7. After each step it type-asserts `endpointState.balancer` and prints what it references next to the current `endpointState.state`.

Output:

```console
### worktree of evalon/grpc-go-en-cb3ba169 at 96932419e313262bafd330ec05c208f8ee5885c2
### 1. where the handle is stored / refreshed
145:				balancer: childState,
165:			es.state = childState.State
### 2. probe
    audit_c3_stale_snapshot_test.go:36: [after initial update] handle dynamic type=endpointsharding.ChildState
    audit_c3_stale_snapshot_test.go:40: [after initial update] handle snapshot: State.ConnectivityState=IDLE State.Picker=0xc000138188 weight=1 | current es.state: ConnectivityState=IDLE Picker=0xc000138188 es.weight=1
    audit_c3_stale_snapshot_test.go:36: [after child went READY] handle dynamic type=endpointsharding.ChildState
    audit_c3_stale_snapshot_test.go:40: [after child went READY] handle snapshot: State.ConnectivityState=IDLE State.Picker=0xc000138188 weight=1 | current es.state: ConnectivityState=READY Picker=0xc0000d2600 es.weight=1
    audit_c3_stale_snapshot_test.go:36: [after weight 1 -> 7 resolver update] handle dynamic type=endpointsharding.ChildState
    audit_c3_stale_snapshot_test.go:40: [after weight 1 -> 7 resolver update] handle snapshot: State.ConnectivityState=IDLE State.Picker=0xc000138188 weight=1 | current es.state: ConnectivityState=READY Picker=0xc0000d2600 es.weight=7
    audit_c3_stale_snapshot_test.go:84: STALE: after READY, handle still references State{IDLE, picker 0xc000138188}; current is State{READY, picker 0xc0000d2600}
    audit_c3_stale_snapshot_test.go:89: STALE: after resolver update, handle still references Endpoint with weight 1; current weight is 7
    audit_c3_stale_snapshot_test.go:93: STALE: after resolver update, handle picker 0xc000138188 != current picker 0xc0000d2600
    audit_c3_stale_snapshot_test.go:96: RESULT: reconnection handle retains a superseded endpointsharding.ChildState snapshot
--- FAIL: Test (0.01s)
    --- FAIL: Test/AuditC3StaleChildStateSnapshot (0.01s)
--- PASS: Test (0.00s)
FAIL
FAIL	google.golang.org/grpc/balancer/ringhash	0.065s
FAIL
### 3. the branch's own ringhash suite (does not notice)
ok  	google.golang.org/grpc/balancer/ringhash	14.675s
```

Reading of the output: the handle's dynamic type is `endpointsharding.ChildState`. After two later child-state updates it still holds `State{IDLE, Picker 0xc000138188}` and an `Endpoint` whose weight attribute is 1, while the current state is `READY` with picker `0xc0000d2600` and weight 7. The handle is never replaced, so the first snapshot (its picker and its endpoint/attributes) stays reachable from the balancer's `endpointStates` map — and from every `picker` built from it — for as long as the endpoint exists.

Impact reasoning: this is on the ordinary path — every ringhash endpoint, from its first state change onward. What was observed is retention and staleness of data, not misbehaviour: `ChildState.ExitIdle()` on this branch dispatches through the snapshot's unexported `bw` wrapper pointer, which is stable, and the branch's whole ringhash suite passes under `-race` (step 3). The cost is one superseded child picker plus one superseded `resolver.Endpoint` (addresses and attributes) pinned per endpoint; it does not grow with further updates because the snapshot is the first one and is never replaced. The hazard is latent: any future reader of `es.balancer` that looks at `.State` or `.Endpoint` gets creation-time data that looks valid.

## C4

Branch: [evalon/grpc-go-en-c60230da](https://github.com/kaitranntt-evals/grpc-go-endpointsharding-decouple-locking/tree/evalon/grpc-go-en-c60230da) at `a2a817c0`. Verdict basis: both parts observed (callback_publication, shutdown_trigger).

On this branch `balancerWrapper.UpdateState` stores the child's state and calls `es.updateState()`, which returns early only if `inhibitChildUpdates` is set; `endpointSharding.Close` closes each child and never sets `inhibitChildUpdates` or any other shutdown guard.

Command:

```sh
sh verify/repro/c4_run.sh
```

The probe (`verify/repro/c4_close_publication_test.go.txt`) uses a parent `ClientConn` that counts `UpdateState` calls and two children built through `endpointsharding.NewBalancer`. Three phases: a child callback while the balancer is open (precondition); children that call `UpdateState` synchronously from their own `Close`; a child that calls `UpdateState` after `Close()` has returned.

Output:

```console
### worktree of evalon/grpc-go-en-c60230da at a2a817c0aacab1e9c30dee4d77aa05e9ddc29c79
### 1. Close on the claim branch
func (es *endpointSharding) Close() {
	children := es.children.Load()
	for _, child := range children.All() {
		child.close()
	}
}
### 2. probe on the claim branch
--- PASS: Test (0.00s)
    audit_c4_close_publication_test.go:104: OBSERVED: parent notifications produced by a child callback after Close returned = 1 (last aggregate state READY)
    audit_c4_close_publication_test.go:106: post-Close child callback published 1 aggregate-state notifications to the parent, want 0 (suppressed)
    audit_c4_close_publication_test.go:91: OBSERVED: parent notifications produced while Close was closing 2 children = 2 (last aggregate state TRANSIENT_FAILURE)
    audit_c4_close_publication_test.go:93: Close published 2 aggregate-state notifications to the parent, want 0 (suppressed)
    audit_c4_close_publication_test.go:79: OBSERVED: parent notifications caused by one child callback while open = 1 (last aggregate state READY)
--- FAIL: Test (0.01s)
    --- FAIL: Test/AuditC4CallbackAfterClose (0.00s)
    --- FAIL: Test/AuditC4CallbackDuringClose (0.00s)
    --- PASS: Test/AuditC4CallbackPublishesWhenNotInhibited (0.00s)
FAIL
FAIL	google.golang.org/grpc/balancer/endpointsharding	0.021s
FAIL
### 3. the branch's own endpointsharding suite (does not notice)
ok  	google.golang.org/grpc/balancer/endpointsharding	1.119s
```

Reading of the output:

- *callback_publication:* with inhibition disabled, one child callback produces exactly one parent notification.
- *shutdown_trigger:* `Close` published 2 aggregate states (one per closing child, ending in `TRANSIENT_FAILURE`), and a callback after `Close` returned published 1 more (`READY`). Nothing is suppressed.

Context runs with the same probe file (copied to `balancer/endpointsharding/audit_c4_close_publication_test.go`, `go test ./balancer/endpointsharding -run 'Test/AuditC4' -race -count=1 -v`):

```console
=== task base commit bf9e7cd3 (git worktree)
    audit_c4_close_publication_test.go:105: OBSERVED: parent notifications produced by a child callback after Close returned = 1 (last aggregate state READY)
    audit_c4_close_publication_test.go:92: OBSERVED: parent notifications produced while Close was closing 2 children = 2 (last aggregate state TRANSIENT_FAILURE)
    audit_c4_close_publication_test.go:80: OBSERVED: parent notifications caused by one child callback while open = 1 (last aggregate state READY)
    --- FAIL: Test/AuditC4CallbackAfterClose (0.00s)
    --- FAIL: Test/AuditC4CallbackDuringClose (0.00s)
    --- PASS: Test/AuditC4CallbackPublishesWhenNotInhibited (0.00s)
=== grpc-go-endpointsharding-decouple-locking-perfect (this checkout)
    audit_c4_close_publication_test.go:105: OBSERVED: parent notifications produced by a child callback after Close returned = 0 (last aggregate state CONNECTING)
    audit_c4_close_publication_test.go:92: OBSERVED: parent notifications produced while Close was closing 2 children = 0 (last aggregate state CONNECTING)
    audit_c4_close_publication_test.go:80: OBSERVED: parent notifications caused by one child callback while open = 1 (last aggregate state READY)
    --- PASS: Test/AuditC4CallbackAfterClose (0.00s)
    --- PASS: Test/AuditC4CallbackDuringClose (0.00s)
    --- PASS: Test/AuditC4CallbackPublishesWhenNotInhibited (0.00s)
ok  	google.golang.org/grpc/balancer/endpointsharding	1.036s
```

Impact reasoning: a parent of endpointsharding (ringhash, or any policy that embeds it) keeps receiving `UpdateState` calls — new aggregate states and pickers — from a balancer it has already closed, both while `Close` is tearing children down and afterwards whenever a closed child reports state. The trigger is ordinary for the first phase: a child that reports a state change while being closed. The branch's own endpointsharding suite passes (step 3), so nothing on the branch notices. The behaviour is identical on the task base commit, so the branch did not introduce it; it left it in place, whereas the audited reference branch sets `inhibitUpdatesFromChildren()` at the top of `Close` and publishes nothing in either phase. What a given parent does with a post-close notification was not measured here.

## C5

Branch: [evalon/grpc-go-en-66daeaed](https://github.com/kaitranntt-evals/grpc-go-endpointsharding-decouple-locking/tree/evalon/grpc-go-en-66daeaed) at `7e991a34`.

Command:

```sh
sh verify/repro/c5_gofmt.sh
```

Output:

```console
### worktree of evalon/grpc-go-en-66daeaed at 7e991a34f4d37fa17d5c19e56fc0c207446db94b
### scripts/vet.sh formatting invocation
99:# - gofmt, goimports, go vet, go mod tidy.
105:  gofmt -s -d -l . 2>&1 | fail_on_output
### changed Go files
balancer/endpointsharding/endpointsharding.go
balancer/endpointsharding/endpointsharding_ext_test.go
balancer/ringhash/ringhash.go
### gofmt -s -d -l <changed files>
exit=0 output_bytes=0
### gofmt -l balancer/endpointsharding balancer/ringhash
exit=0 output_bytes=0
### gofmt -d balancer/endpointsharding/endpointsharding_ext_test.go
exit=0 output_bytes=0
### gofmt -s -d -l .   (exactly as scripts/vet.sh, whole repo)
output_bytes=0
### testChild declarations (tabs shown as ^I)
// testChild is a minimal child balancer whose behavior is driven by callbacks
// supplied by the test.
type testChild struct {
^IonUpdateClientConnState func(balancer.ClientConnState) error
^IonExitIdle              func()
}

func (c *testChild) UpdateClientConnState(ccs balancer.ClientConnState) error {
^Ireturn c.onUpdateClientConnState(ccs)
}
func (c *testChild) ResolverError(error)                                        {}
func (c *testChild) UpdateSubConnState(balancer.SubConn, balancer.SubConnState) {}
func (c *testChild) Close()                                                     {}
func (c *testChild) ExitIdle()                                                  { c.onExitIdle() }
### sensitivity check: the same command does flag a deliberately misformatted copy
misformatted_test.go
```

Reading of the output: `scripts/vet.sh` line 105 requires `gofmt -s -d -l .` to print nothing. On the branch that exact command prints 0 bytes for the whole repository, as do `gofmt -s -d -l` on the three changed Go files, `gofmt -l balancer/endpointsharding balancer/ringhash`, and `gofmt -d balancer/endpointsharding/endpointsharding_ext_test.go`. The added `testChild` one-line methods (lines 367-370, the lines the claim points at) are column-aligned exactly as gofmt emits them. The last step shows the command is sensitive: collapsing the alignment of one of those methods in a scratch copy makes `gofmt -s -l` list the file.
