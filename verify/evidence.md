## Setup used for every section

Observations only. Go `go1.25.7 linux/amd64`. Claim branches live in `kaitranntt-evals/grpc-go-endpointsharding-decouple-locking` (not on `origin`), so they were fetched from a second remote into one worktree each. The eval fixture was extracted byte-exact from `eval_tests.zip` to `~/work/tests/eval_endpointsharding_test.go` and copied to `balancer/endpointsharding/eval_endpointsharding_test.go` when run.

```sh
cd ~/repos/grpc-go
git fetch origin grpc-go-endpointsharding-decouple-locking-perfect
git checkout -b verify/grpc-go-endpointsharding-decouple-locking-v-463e4e3e origin/grpc-go-endpointsharding-decouple-locking-perfect
git remote add claims https://github.com/kaitranntt-evals/grpc-go-endpointsharding-decouple-locking
for b in df44e560 e94a93f0 904ab4c7 79fc04cb 940a22c3 8e58d9ff; do
  git fetch claims evalon/grpc-go-en-$b:refs/remotes/claims/evalon/grpc-go-en-$b
  git worktree add -f ~/wt/$b claims/evalon/grpc-go-en-$b
done
mkdir -p ~/work && (cd ~/work && unzip -o eval_tests.zip)
```

Branch heads audited:

```console
df44e560 3400524aaec9ca16634c9c038b0ea73c575a1e69
e94a93f0 adf0c3dc506f4cce55a2cabf789bb1586f771cae
904ab4c7 599979f72d370fdaf8c93605233c8cbb25a77459
79fc04cb 031e52b347b68d6e7d6e1febf96e9477a4731b42
940a22c3 c3208b2781a8b192d899ba285e3133cb58aa9830
8e58d9ff f29aaeae9705f7e904d0c8d6fd27055225064a8c
```

All files under `verify/repro/` named `*.go.txt` are Go test/instrumentation sources stored with a `.txt` suffix so they do not compile into the audited branch; each header line says where to copy it. No production file was changed on any branch (mutations were applied in worktrees and reverted with `git checkout -- .`).

## C1

Claim: an added/changed synchronization comment in `balancer/endpointsharding/` contradicts the implementation's concurrent visibility, lock ownership, or permitted acquisition order. Adjudicated per branch.

### C1 on evalon/grpc-go-en-df44e560 — comment contradicted

Comments under test (`balancer/endpointsharding/endpointsharding.go` on the branch):

```go
// newChild builds a new child balancer for the given endpoint. The returned
// wrapper is not yet visible to other goroutines, so no locking is required.
func (es *endpointSharding) newChild(endpoint resolver.Endpoint) *balancerWrapper {
	bw := &balancerWrapper{ ... }
	bw.childState.balancer = bw
	// Hold the child's mutex while building it so that any synchronous call
	// back into the wrapper during Build observes a consistent wrapper.
	bw.mu.Lock()
	bw.child = es.childBuilder(bw, es.bOpts)
	bw.mu.Unlock()
	return bw
}
...
	// child contains the wrapped balancer. Access its methods only through
	// methods on balancerWrapper to ensure proper synchronization. It is set
	// once, before the wrapper is published, and is read-only after that.
	child balancer.Balancer
```

Path: `childBuilder(bw, ...)` → child's Build calls `cc.UpdateState(IDLE)` → `balancerWrapper.UpdateState` → (auto-reconnect on) `bw.exitIdle()` → `go bw.exitIdleSync()`. That goroutine holds `bw` and blocks on `bw.mu` before `newChild` returns and while `bw.child` is still nil.

Observation 1 — another goroutine is inside the wrapper before `newChild` returns:

```sh
cp verify/repro/c1_df44e560_construction_visibility_test.go.txt ~/wt/df44e560/balancer/endpointsharding/verify_c1_test.go
cd ~/wt/df44e560 && go test ./balancer/endpointsharding -run '^TestVerifyC1_ConstructionVisibility$' -race -count=1 -v
```

```console
=== RUN   TestVerifyC1_ConstructionVisibility
    verify_c1_test.go:50: Build running on goroutine 18 ; newChild has not returned
    verify_c1_test.go:81: while Build was still running (bw.child == nil: true), another goroutine was already inside the wrapper:
        goroutine 19 [sync.Mutex.Lock]:
        internal/sync.runtime_SemacquireMutex(0x61935e?, 0x0?, 0x0?)
        	/usr/local/go/src/runtime/sema.go:95 +0x25
        internal/sync.(*Mutex).lockSlow(0xc0001c2198)
        	/usr/local/go/src/internal/sync/mutex.go:149 +0x210
        internal/sync.(*Mutex).Lock(0xc0001c2198)
        	/usr/local/go/src/internal/sync/mutex.go:70 +0x55
        sync.(*Mutex).Lock(0xc0001c2198)
        	/usr/local/go/src/sync/mutex.go:46 +0x29
        google.golang.org/grpc/balancer/endpointsharding.(*balancerWrapper).exitIdleSync(0xc0001c2180)
        	/home/ubuntu/wt/df44e560/balancer/endpointsharding/endpointsharding.go:396 +0x3a
        created by google.golang.org/grpc/balancer/endpointsharding.(*balancerWrapper).exitIdle in goroutine 18
        	/home/ubuntu/wt/df44e560/balancer/endpointsharding/endpointsharding.go:390 +0x8b
    verify_c1_test.go:85: child.ExitIdle() was then delivered on goroutine 19 
    verify_c1_test.go:90: PROBLEM REPRODUCED: wrapper was visible to (and locked on by) another goroutine before newChild returned; the bw.mu lock in newChild is what keeps it safe
--- FAIL: TestVerifyC1_ConstructionVisibility (0.00s)
```

(The test "fails" by design when the contradiction is observed.)

Observation 2 — taking the comment at its word ("no locking is required") and removing the lock in `newChild` crashes the eval fixture:

```sh
cd ~/wt/df44e560 && git apply ~/repos/grpc-go/verify/repro/c1_df44e560_no_lock_mutation.patch
cp ~/work/tests/eval_endpointsharding_test.go balancer/endpointsharding/
go test ./balancer/endpointsharding -run '^TestEval_ConstructionIdleCallbackSafety$' -race -count=1 -v
git checkout -- balancer/endpointsharding/endpointsharding.go
```

```console
=== RUN   TestEval_ConstructionIdleCallbackSafety
panic: runtime error: invalid memory address or nil pointer dereference
[signal SIGSEGV: segmentation violation code=0x1 addr=0x20 pc=0xba2066]

goroutine 10 [running]:
google.golang.org/grpc/balancer/endpointsharding.(*balancerWrapper).exitIdleSync(0xc000210600)
	/home/ubuntu/wt/df44e560/balancer/endpointsharding/endpointsharding.go:399 +0xc6
created by google.golang.org/grpc/balancer/endpointsharding.(*balancerWrapper).exitIdle in goroutine 9
	/home/ubuntu/wt/df44e560/balancer/endpointsharding/endpointsharding.go:388 +0x8b
FAIL	google.golang.org/grpc/balancer/endpointsharding	0.018s
```

Unmutated, the same fixture passes on this branch (`--- PASS: TestEval_ConstructionIdleCallbackSafety (0.05s)`, whole `TestEval_` set: `ok  google.golang.org/grpc/balancer/endpointsharding 1.803s`), so the code is safe only because it does the locking the comment calls unnecessary.

Impact reasoning: runtime behaviour on the branch is correct (the lock is taken). The defect is the documented invariant: the function comment says the wrapper is unexposed and needs no locking, and the `child` field comment says it is set before the wrapper is published; both are false whenever a child reports IDLE from its builder with auto-reconnect enabled (the default `Options{}`), which is exactly the case the eval fixture `TestEval_ConstructionIdleCallbackSafety` exercises. A maintainer who trusts the comment and drops or narrows the lock gets a nil-pointer crash (shown above). The other synchronization comments on this branch (lock order "do not acquire a balancerWrapper's mutex while holding mu") held under the lock-order instrumentation below (`bw.mu -> es.mu` only).

```sh
verify/repro/c1_lockorder.sh ~/wt/df44e560 ~/work/tests/eval_endpointsharding_test.go
```

```console
### scenario test
    zz_verify_lockorder_test.go:140: child calls cc.UpdateState synchronously from Build with held=[bw.mu]
    zz_verify_lockorder_test.go:140: child calls cc.UpdateState synchronously from ExitIdle with held=[bw.mu]
    zz_verify_lockorder_test.go:140: child calls cc.UpdateState synchronously from ResolverError with held=[bw.mu]
    zz_verify_lockorder_test.go:140: child calls cc.UpdateState synchronously from UpdateClientConnState with held=[bw.mu]
    zz_verify_lockorder_test.go:149: parent cc.UpdateState entered with held=[bw.mu es.mu]  x11
    zz_verify_lockorder_test.go:149: parent cc.UpdateState entered with held=[es.mu]  x6
    zz_verify_lockorder_test.go:161: observed lock-acquisition orders (held -> acquiring): 1 distinct
    zz_verify_lockorder_test.go:163:   bw.mu -> es.mu  x35
--- PASS: TestVerifyC1_LockOrder (0.01s)
### whole package (existing tests + eval fixture if present), orders dumped by the last test
=== RUN   TestZZVerifyC1_LockOrderAllTests
    zz_verify_lockorder_test.go:161: observed lock-acquisition orders (held -> acquiring): 1 distinct
    zz_verify_lockorder_test.go:163:   bw.mu -> es.mu  x354
--- PASS: TestZZVerifyC1_LockOrderAllTests (0.00s)
PASS
ok  	google.golang.org/grpc/balancer/endpointsharding	1.930s
```

### C1 on evalon/grpc-go-en-e94a93f0 — comments agree with the implementation

Every added/changed synchronization comment in `endpointsharding.go` on this branch, and what it asserts:

```go
	// children ... The map is replaced (never mutated in place) by
	// UpdateClientConnState, so readers may load it without holding any lock.
	// Calls into a child are synchronized by that child's balancerWrapper.mu.
	// There is intentionally no balancer-wide lock around calls into children ...
	// (es.mu) ... mu must not be held during calls into a child since
	// synchronous calls back from the child may require taking mu, causing a
	// deadlock. To avoid deadlocks, do not acquire a balancerWrapper's mu
	// while holding mu.
	// (bw.mu) mu synchronizes all calls into the wrapped child balancer, including
	// its construction. It must be held for all calls into child. ...
	// Lock ordering: a child may synchronously call back into UpdateState
	// while mu is held, which acquires es.mu. Therefore mu may be acquired
	// before es.mu, but es.mu must never be held while acquiring mu.
	// child ... is nil until the first UpdateClientConnState call builds it. Guarded by mu.
	// exitIdleSync ... must not be called while holding bw.mu or es.mu.
```

The claim's suspicion is that a comment "excludes parent entry while holding a child lock". No comment on the branch says that: the lock-ordering comment states the opposite — that a child calls back into the parent (`UpdateState`, taking `es.mu`) while its own `mu` is held — and only forbids the reverse order (`es.mu` then `bw.mu`). Unlike df44e560 there is no "not yet visible / no locking" construction comment: the child is built inside `balancerWrapper.updateClientConnState` under `bw.mu`, and the comment there says so.

Observation — instrumented lock order. `verify/repro/c1_lockorder.sh` swaps the two `mu sync.Mutex` fields for wrappers that record, per goroutine, every (held → acquiring) pair, then runs (a) a scenario with children that call `cc.UpdateState` synchronously from Build, UpdateClientConnState, ResolverError and ExitIdle, covering auto/`ChildState`/parent `ExitIdle`, attribute change, removal, async callbacks and Close, and (b) the whole package including the eval fixture, all under `-race`:

```sh
verify/repro/c1_lockorder.sh ~/wt/e94a93f0 ~/work/tests/eval_endpointsharding_test.go
```

```console
 balancer/endpointsharding/endpointsharding.go | 6 ++++--
-	mu sync.Mutex
+	mu esMutex
-	mu sync.Mutex
+	mu bwMutex
### scenario test
    zz_verify_lockorder_test.go:140: child calls cc.UpdateState synchronously from Build with held=[bw.mu]
    zz_verify_lockorder_test.go:140: child calls cc.UpdateState synchronously from ExitIdle with held=[bw.mu]
    zz_verify_lockorder_test.go:140: child calls cc.UpdateState synchronously from ResolverError with held=[bw.mu]
    zz_verify_lockorder_test.go:140: child calls cc.UpdateState synchronously from UpdateClientConnState with held=[bw.mu]
    zz_verify_lockorder_test.go:149: parent cc.UpdateState entered with held=[bw.mu es.mu]  x12
    zz_verify_lockorder_test.go:149: parent cc.UpdateState entered with held=[es.mu]  x6
    zz_verify_lockorder_test.go:161: observed lock-acquisition orders (held -> acquiring): 1 distinct
    zz_verify_lockorder_test.go:163:   bw.mu -> es.mu  x36
--- PASS: TestVerifyC1_LockOrder (0.01s)
PASS
ok  	google.golang.org/grpc/balancer/endpointsharding	1.019s
### whole package (existing tests + eval fixture if present), orders dumped by the last test
--- PASS: Test (0.10s)
=== RUN   TestZZVerifyC1_LockOrderAllTests
    zz_verify_lockorder_test.go:161: observed lock-acquisition orders (held -> acquiring): 1 distinct
    zz_verify_lockorder_test.go:163:   bw.mu -> es.mu  x468
--- PASS: TestZZVerifyC1_LockOrderAllTests (0.00s)
PASS
ok  	google.golang.org/grpc/balancer/endpointsharding	1.930s
```

Reading: the synchronous child callback does enter the parent (`cc.UpdateState`) with the child lock held (`held=[bw.mu es.mu]`, 12 times) — which is what the lock-ordering comment documents — and the only acquisition order ever observed across the scenario, the branch's own tests and the eval fixture is `bw.mu -> es.mu`; `es.mu -> bw.mu`, nested `bw.mu -> bw.mu` and any race report were never observed (the dump test fails on any of them). The eval fixture's `TestEval_` set also passes unmodified on this branch (`ok  google.golang.org/grpc/balancer/endpointsharding 1.804s`, including `TestEval_ConstructionIdleCallbackSafety` and `TestEval_SynchronousChildUpdateDeadlock`).

Limit of this evidence: it is a dynamic check over the exercised paths plus a read of every comment, not a proof over all executions. A parent that itself calls `Balancer.ExitIdle()` from inside `cc.UpdateState` would acquire `bw.mu` under `es.mu`; that is the caller breaking the documented rule, not the comment misdescribing the implementation.

## C2

Claim (branch evalon/grpc-go-en-904ab4c7): cleanup in an added concurrency test calls `b.Close()` after a bounded join of a lock-acquiring background worker times out, without confirming the worker completed.

Cleanup under test — `TestChildOperationsIndependent` in `balancer/endpointsharding/endpointsharding_ext_test.go` on the branch:

```go
				defer func() {
					close(release)
					if operationDone != nil {
						select {
						case err := <-operationDone:
							if err != nil {
								t.Errorf("%s failed: %v", operation, err)
							}
						case <-time.After(defaultTestTimeout):
							t.Errorf("Timed out waiting for %s to finish", operation)
						}
					}
					b.Close()
				}()
```

The worker is `go func() { ... err = b.UpdateClientConnState(ccs) ...; operationDone <- err }()`, which runs inside `balancerWrapper.updateClientConnState` holding that child's `childMu`. `TestChildExitIdleDuringClose` in the same file has the same shape (a deferred `select` that only `t.Error`s on timeout, followed by the earlier-registered `defer b.Close()`); that one was read, not exercised.

Exercise: the patch makes the worker never complete after `release` (adds `select {}` to the test's `block` helper) and adds two log lines around `b.Close()`; the cleanup logic itself is untouched.

```sh
cd ~/wt/904ab4c7 && git apply ~/repos/grpc-go/verify/repro/c2_unjoined_worker_close.patch
go test ./balancer/endpointsharding -run 'Test/ChildOperationsIndependent/UpdateClientConnState/ChildState$' -race -count=1 -v -timeout 40s
git checkout -- .
```

```console
=== RUN   Test/ChildOperationsIndependent/UpdateClientConnState/ChildState
    balancer.go:193: testutils.BalancerClientConn: UpdateState({CONNECTING 0xc000135d00})
    endpointsharding_ext_test.go:433: Timed out waiting for UpdateClientConnState to finish
    endpointsharding_ext_test.go:436: VERIFY C2: cleanup now calling b.Close(); workerJoined=false
panic: test timed out after 40s
	running tests:
		Test (40s)
		Test/ChildOperationsIndependent (40s)
		Test/ChildOperationsIndependent/UpdateClientConnState/ChildState (40s)
...
google.golang.org/grpc/balancer/endpointsharding.(*balancerWrapper).close(0xc000195200)
	/home/ubuntu/wt/904ab4c7/balancer/endpointsharding/endpointsharding.go:370 +0x3a
google.golang.org/grpc/balancer/endpointsharding.(*endpointSharding).Close(0xc000182fc0)
	/home/ubuntu/wt/904ab4c7/balancer/endpointsharding/endpointsharding.go:212 +0x174
google.golang.org/grpc/balancer/endpointsharding_test.s.TestChildOperationsIndependent.func1.4()
	/home/ubuntu/wt/904ab4c7/balancer/endpointsharding/endpointsharding_ext_test.go:437 +0x3af
...
goroutine 25 [select (no cases)]:
google.golang.org/grpc/balancer/endpointsharding_test.s.TestChildOperationsIndependent.func1.2()
	/home/ubuntu/wt/904ab4c7/balancer/endpointsharding/endpointsharding_ext_test.go:371 +0x4a
FAIL	google.golang.org/grpc/balancer/endpointsharding	40.058s
```

Reading: the join timed out (line 433), cleanup went on to `b.Close()` with the worker unjoined (line 436, `workerJoined=false`), "b.Close() returned" was never logged, and the goroutine dump shows the cleanup defer parked in `balancerWrapper.close` → `childMu.Lock()` behind the still-running worker (goroutine 25) until the test binary's own timeout panicked.

Impact reasoning: test-only. On the passing path the cleanup is fine. On the path the timeout branch exists for (a worker stuck inside a child call), the cleanup reports the timeout and then blocks in `Close()` on the lock the stuck worker holds, so the subtest never returns: instead of one failed subtest with the "Timed out waiting ..." message, the whole test binary dies at `-timeout` (10 minutes by default) with a goroutine dump, and every later test in the package does not run.

## C3

Claim (branch evalon/grpc-go-en-79fc04cb): a callback from an already-updated child publishes parent state while another child's update is still blocked and the batch's endpoint map is not installed. Parts: suppression_lifetime, callback_trigger.

Implementation on the branch: the balancer-wide `inhibitChildUpdates` flag was replaced by a per-child `balancerWrapper.inhibitUpdates`, set and cleared inside `bw.updateClientConnState` (`bw.inhibitUpdates.Store(true); defer bw.inhibitUpdates.Store(false)`); `es.children.Store(newChildren)` still happens only after the loop over all endpoints.

Repro (internal test; blocks the third-updated child, sends the callback from the first-updated child, so the two are always distinct regardless of `rotateEndpoints`):

```sh
cp verify/repro/c3_midbatch_publication_test.go.txt ~/wt/79fc04cb/balancer/endpointsharding/verify_c3_test.go
cd ~/wt/79fc04cb && go test ./balancer/endpointsharding -run '^TestVerifyC3' -race -count=3 -v
```

```console
=== RUN   TestVerifyC3_MidBatchCallbackPublishes
    verify_c3_test.go:96: update order=[10.0.0.3 10.0.0.1 10.0.0.2]; blocked child=10.0.0.2; callback source=10.0.0.3 (distinct=true)
    verify_c3_test.go:101: suppression_lifetime: already-updated child 10.0.0.3 inhibitUpdates=false while installed endpoint map has Len()=0 (batch has 3 endpoints)
    verify_c3_test.go:103: parent updates before mid-batch callback: 0
    verify_c3_test.go:108: callback_trigger: parent updates after mid-batch callback from 10.0.0.3, with 10.0.0.2 still blocked: 1
    verify_c3_test.go:111:   mid-batch published state[0]: ConnectivityState=TRANSIENT_FAILURE childStates=0 pickErr=no children to pick from
    verify_c3_test.go:117: batch UpdateClientConnState still in flight (not returned)
    verify_c3_test.go:125: parent updates after batch completion: 2; final state=READY childStates=3
    verify_c3_test.go:128: PROBLEM REPRODUCED: 1 parent publication(s) leaked mid-batch before the complete endpoint map was installed
--- FAIL: TestVerifyC3_MidBatchCallbackPublishes (0.00s)
=== RUN   TestVerifyC3_MidBatchCallbackPublishes
    verify_c3_test.go:96: update order=[10.0.0.2 10.0.0.3 10.0.0.1]; blocked child=10.0.0.1; callback source=10.0.0.2 (distinct=true)
    verify_c3_test.go:101: suppression_lifetime: already-updated child 10.0.0.2 inhibitUpdates=false while installed endpoint map has Len()=0 (batch has 3 endpoints)
    verify_c3_test.go:108: callback_trigger: parent updates after mid-batch callback from 10.0.0.2, with 10.0.0.1 still blocked: 1
    verify_c3_test.go:111:   mid-batch published state[0]: ConnectivityState=TRANSIENT_FAILURE childStates=0 pickErr=no children to pick from
--- FAIL: TestVerifyC3_MidBatchCallbackPublishes (0.00s)
```

(3 of 3 iterations reproduced; the test "fails" by design when the leak is observed.)

- suppression_lifetime: observed — the already-updated child's `inhibitUpdates` is `false` while `es.children` still has `Len()=0` for a 3-endpoint batch.
- callback_trigger: observed — with a different child's update still blocked and `UpdateClientConnState` not returned, the callback produced one parent `UpdateState`, and what was published is built from the pre-batch map: on the first resolver update that is `TRANSIENT_FAILURE` with zero child states and a picker failing every RPC with `no children to pick from`, although the child that called back had just reported READY.

Eval fixture, unmodified, same branch and a control branch that keeps the balancer-wide flag:

```sh
cp ~/work/tests/eval_endpointsharding_test.go ~/wt/79fc04cb/balancer/endpointsharding/
cd ~/wt/79fc04cb && go test ./balancer/endpointsharding -run '^TestEval_BatchUpdateInhibition$' -race -count=30 -v 2>&1 | grep -E "^--- (PASS|FAIL)|eval_endpointsharding_test.go" | sort | uniq -c
cd ~/wt/e94a93f0 && go test ./balancer/endpointsharding -run '^TestEval_BatchUpdateInhibition$' -race -count=30 -v 2>&1 | grep -E "^--- (PASS|FAIL)" | sort | uniq -c
```

```console
     21     eval_endpointsharding_test.go:1980: Expected 0 parent updates after concurrent mid-batch child callback, got 1 (intermediate callback leaked mid-batch)
     21     eval_endpointsharding_test.go:2000: Expected exactly 1 consolidated update after batch completion, got 2
     21     eval_endpointsharding_test.go:2018: Expected exactly 2 updates after post-batch child state change, got 3
     21 --- FAIL: TestEval_BatchUpdateInhibition (0.00s)
      9 --- PASS: TestEval_BatchUpdateInhibition (0.00s)
```

Control (second command, evalon/grpc-go-en-e94a93f0):

```console
     30 --- PASS: TestEval_BatchUpdateInhibition (0.00s)
```

The fixture always calls back from `10.0.0.1` and blocks whichever child is built third; it passes on 79fc04cb only in the iterations where rotation makes those the same child (whose own flag is still set), which is why it is 21/30 red rather than 30/30.

Impact reasoning: `UpdateClientConnState` promises "a single synchronous update of the childrens' aggregated state at the end". On this branch any state change from a child that has already been updated (a SubConn connecting/failing, a pick-triggered reconnect) while a later child in the same resolver update is still inside its own `UpdateClientConnState` is pushed to the parent immediately, computed from the old endpoint map. During the first update that is an empty map, so the channel is told TRANSIENT_FAILURE with a picker that fails RPCs instead of queuing them; during later updates it is a picker that omits newly added endpoints and still lists removed ones. The window is as wide as the slowest child update, i.e. it grows in the very situation (a slow child) this change was written for. No caller-side workaround. Not observed, inferred from reading: `ringhash` consumes this stream through `ChildStatesFromPicker` to reconcile its endpoint set, so it would see the same truncated list; and `ResolverError` on this branch uses the same per-child flag.

## C4

Claim (branch evalon/grpc-go-en-940a22c3): `balancer/ringhash/ringhash.go` adds declarations that reference the deprecated `balancer.ExitIdler`.

All of the following is scripted in `verify/repro/c4_deprecated_exitidler.sh ~/wt/940a22c3`.

```sh
cd ~/wt/940a22c3
git diff bf9e7cd3 -- balancer/ringhash/
git diff -U0 bf9e7cd3430df40d0732ba42eb88bd5f2cc63407 -- '*.go' ':(exclude)**/eval_*_test.go' | grep -nE '^\+[^+].*balancer\.ExitIdler'; echo "grep exit=$?"
grep -n -B8 "type ExitIdler" balancer/balancer.go
```

```console
-		var idleBalancer endpointsharding.ExitIdler
+		var idleBalancer balancer.ExitIdler
...
-	balancer endpointsharding.ExitIdler
+	// balancer is used to request the child policy for this endpoint to exit
+	// the IDLE state. It is the endpointsharding.ChildState for the endpoint.
+	balancer balancer.ExitIdler

359:+		var idleBalancer balancer.ExitIdler
368:+	balancer balancer.ExitIdler
grep exit=0

374-// Deprecated: All balancers must implement this interface. This interface will
375-// be removed in a future release.
376:type ExitIdler interface {
```

(The second command is, without the leading `!`, the eval's own check from the environment reference; it matching means that check fails on this branch.)

Type resolution by the tool the repo's `scripts/vet.sh` uses (`staticcheck`, installed with `GOBIN=~/work/bin go install honnef.co/go/tools/cmd/staticcheck@latest`), then the allow-list from `scripts/vet.sh` ("Only ignore the following deprecated types/fields/functions") applied to the result:

```sh
~/work/bin/staticcheck -checks 'SA1019' ./balancer/ringhash/ ./balancer/endpointsharding/ > ~/work/c4_sc.out 2>&1; cat ~/work/c4_sc.out
awk '/noret_grep "\(SA1019\)" "\$\{SC_OUT\}" \| not grep -Fv/{f=1; sub(/.*-Fv \x27/,""); print; next} f&&/PleaseIgnoreUnused\x27/{print "XXXXX PleaseIgnoreUnused"; f=0} f{print}' scripts/vet.sh > ~/work/c4_allow.txt
grep "(SA1019)" ~/work/c4_sc.out | grep -Fv -f ~/work/c4_allow.txt; echo "exit=$?"
```

```console
balancer/ringhash/ringhash.go:271:20: google.golang.org/grpc/balancer.ExitIdler is deprecated: All balancers must implement this interface. This interface will be removed in a future release.  (SA1019)
balancer/ringhash/ringhash.go:404:11: google.golang.org/grpc/balancer.ExitIdler is deprecated: All balancers must implement this interface. This interface will be removed in a future release.  (SA1019)
balancer/ringhash/ringhash_test.go:687:2: (google.golang.org/grpc/resolver.Address).BalancerAttributes is deprecated: ...  (SA1019)
balancer/ringhash/ringhash_test.go:687:28: (google.golang.org/grpc/resolver.Address).BalancerAttributes is deprecated: ...  (SA1019)
--- SA1019 lines NOT covered by scripts/vet.sh allowlist (vet.sh fails if any):
balancer/ringhash/ringhash.go:271:20: google.golang.org/grpc/balancer.ExitIdler is deprecated: All balancers must implement this interface. This interface will be removed in a future release.  (SA1019)
balancer/ringhash/ringhash.go:404:11: google.golang.org/grpc/balancer.ExitIdler is deprecated: All balancers must implement this interface. This interface will be removed in a future release.  (SA1019)
exit=0
```

Impact reasoning: two added declarations (`var idleBalancer balancer.ExitIdler`, field `endpointState.balancer balancer.ExitIdler`) name an interface whose doc says it "will be removed in a future release". Runtime behaviour is unaffected today. Consequences observed: the eval's no-`balancer.ExitIdler` check matches (fails), and both lines are SA1019 findings that the SA1019 allow-list in `scripts/vet.sh` does not cover (the pre-existing `BalancerAttributes` findings are covered), so the `vet` job in `.github/workflows/testing.yml` (`./scripts/vet.sh -install && ./scripts/vet.sh`) would reject the change. I did not run the full `scripts/vet.sh`; the allow-list was extracted from it and applied to staticcheck's output for the two changed packages.

## C5

Claim (branch evalon/grpc-go-en-8e58d9ff): after a newer child-state update replaces `es.state`, ringhash keeps the initial child-state snapshot (original picker and endpoint attributes) in `es.balancer` for reconnection.

Code on the branch (`balancer/ringhash/ringhash.go`, `ringhashBalancer.UpdateState`): on first sight of an endpoint `es := &endpointState{balancer: childState, ..., state: childState.State}` stores the whole `endpointsharding.ChildState` value in the `endpointsharding.ExitIdler` interface field; in the "seen before" branch only `es.weight`, `es.hashKey` and `es.state = childState.State` are refreshed. `es.balancer` is used at `picker.go:110` and `ringhash.go:279` only to call `ExitIdle()`.

```sh
cp verify/repro/c5_stale_snapshot_test.go.txt ~/wt/8e58d9ff/balancer/ringhash/verify_c5_test.go
cd ~/wt/8e58d9ff && go test ./balancer/ringhash -run '^TestVerifyC5' -race -count=1 -v
```

```console
=== RUN   TestVerifyC5_StaleChildStateSnapshot
    verify_c5_test.go:42: after creation: es.balancer dynamic type=endpointsharding.ChildState (is ChildState value: true)
    verify_c5_test.go:43: after creation: es.state           = {IDLE, picker *lazy.idlePicker@0xc0002be078}
    verify_c5_test.go:44: after creation: es.balancer.State  = {IDLE, picker *lazy.idlePicker@0xc0002be078}
    verify_c5_test.go:45: after creation: es.balancer.Endpoint.Attributes[verifyC5Key] = attrs-v1
    balancer.go:193: testutils.BalancerClientConn: UpdateState({CONNECTING 0xc000613aa0})
    balancer.go:193: testutils.BalancerClientConn: UpdateState({READY 0xc000613c50})
    balancer.go:193: testutils.BalancerClientConn: UpdateState({READY 0xc000613d40})
    verify_c5_test.go:42: after updates : es.balancer dynamic type=endpointsharding.ChildState (is ChildState value: true)
    verify_c5_test.go:43: after updates : es.state           = {READY, picker *pickfirst.picker@0xc000613bf0}
    verify_c5_test.go:44: after updates : es.balancer.State  = {IDLE, picker *lazy.idlePicker@0xc0002be078}
    verify_c5_test.go:45: after updates : es.balancer.Endpoint.Attributes[verifyC5Key] = attrs-v1
    verify_c5_test.go:93: ringhash endpointStates.Len()=1
    verify_c5_test.go:99: es.state picker replaced: true; es.balancer still holds initial picker: true; es.balancer still holds initial endpoint attributes: true (snapshot state IDLE vs live READY)
    balancer.go:193: testutils.BalancerClientConn: UpdateState({IDLE 0xc000613f50})
    balancer.go:193: testutils.BalancerClientConn: UpdateState({CONNECTING 0xc0001f2420})
    verify_c5_test.go:117: functional: ExitIdle through the stale snapshot still triggered SubConn.Connect on the live child
    verify_c5_test.go:123: PROBLEM REPRODUCED: es.balancer retains the initial ChildState snapshot (original picker and endpoint attributes) after es.state was replaced
--- FAIL: TestVerifyC5_StaleChildStateSnapshot (0.06s)
```

Sequence in the test: one endpoint with attribute `attrs-v1`; reconnect via `es.balancer.ExitIdle()` and drive the SubConn to READY (replaces `es.state`); resolver update with the same address and attribute `attrs-v2`; then dump. `es.state` is `{READY, *pickfirst.picker}`, while `es.balancer` is still the first `ChildState` value: `{IDLE, *lazy.idlePicker@0xc0002be078}` (same pointer as at creation) with `attrs-v1`.

Impact reasoning: no functional fault was observed — `ExitIdle()` through the stale snapshot still reaches the live child, because the snapshot's private `child` wrapper pointer does not change (the "functional:" line). What is real is the retention and the trap: for as long as an endpoint stays in the ring, ringhash pins that endpoint's first child picker and its first `resolver.Endpoint` (addresses and `Attributes`), which never get refreshed or released when the child moves on, and `endpointState` carries two disagreeing states (`es.state` READY vs `es.balancer.State` IDLE). Any later code that reads `State` or `Endpoint` off `es.balancer` gets creation-time data. Cost is one obsolete picker plus one endpoint/attribute set per live endpoint.
