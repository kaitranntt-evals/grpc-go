Observations for audit run `v-a84c68d5`. Each section below stands on its own: it names the branch and commit, the commands, and the output lines the verdict rests on. Raw output of every run is in `verify/logs/`. The attached check fixture (`eval_endpointsharding_test.go`) was extracted but not run: both claims are about tests the target branches added themselves.

## C1

Claim: at least one changed or added concurrency test in `balancer/endpointsharding/` or `balancer/ringhash/` lacks a bounded termination path for a fixture-related wait after an assertion failure or timeout.

How the runs work: the target branch is not on `origin`; it is fetched from [kaitranntt-evals/grpc-go-endpointsharding-decouple-locking](https://github.com/kaitranntt-evals/grpc-go-endpointsharding-decouple-locking). `verify/repro/<dir>/run.sh <mode>` creates a clean detached worktree of the target branch under `~/verify-wt/`, applies that mode's patch(es) with `git apply`, and runs `go test -race`. Patches touch only the throwaway worktree. Run from the repository root of this branch. Environment: `go version go1.25.7 linux/amd64`, 8 CPUs. Tests on these branches run through `grpctest.RunSubTests`, so `TestFoo` is selected with `-run '^Test$/^Foo$'`; each excerpt shows the `=== RUN   Test/<name>` line to prove the test actually ran.

Audited commits: `evalon/grpc-go-en-4279a1cf` at `b16c89b0a169875a1747430e08a51f1a7fbbc39b`, `evalon/grpc-go-en-eb9cd093` at `5e07f940f1fa39d0814a7b9d75ff69cd3be5e1c6`. On both, the added concurrency tests are in `balancer/endpointsharding/endpointsharding_ext_test.go`.

### C1 on `evalon/grpc-go-en-4279a1cf`

Suspected: the added tests do bare channel receives during cleanup, with no timeout and without having observed the worker finish.

What the test file contains (`balancer/endpointsharding/endpointsharding_ext_test.go` at `b16c89b0`):

```go
// TestChildExitIdleDuringBuild, lines 507-525
	updateDone := make(chan struct{})
	go func() {
		defer close(updateDone)
		if err := b.UpdateClientConnState(...); err != nil { ... }
	}()
	// Unblock the builder and wait for the resolver update before closing b,
	// including when the test fails before construction finishes.
	defer func() { <-updateDone }()
	defer releaseBuild()
	select {
	case <-building:
	case <-ctx.Done():
		t.Fatal("Timed out waiting for the builder to report IDLE")
	}
```

```go
// TestChildOperationsIndependent, lines 444-447 and 473
				defer func() {
					close(release)
					<-operationDone
				}()
				...
				t.Cleanup(func() { <-reconnectDone })
```

All three cleanup waits are bare receives. They terminate only if the worker returns once the fixture barrier is released, which nothing in the test has observed.

Baseline (branch as committed) passes:

```sh
verify/repro/c1-4279a1cf/run.sh baseline
```

```console
=== RUN   Test/ChildExitIdleDuringBuild
--- PASS: Test (0.01s)
    --- PASS: Test/ChildExitIdleDuringBuild (0.01s)
ok  	google.golang.org/grpc/balancer/endpointsharding	1.026s
```

Probe: make the test fail the way it is written to fail. `TestChildExitIdleDuringBuild` exists to catch an idle exit that is attempted while the child is still being built. `mutation-sync-exitidle.patch` reintroduces that defect with a one-token change in `balancerWrapper.ExitIdle` (`go func() {` becomes `func() {`), so the builder's synchronous `UpdateState(IDLE)` re-enters the child mutex the builder already holds.

```sh
verify/repro/c1-4279a1cf/run.sh hang     # go test ... -run '^Test$/^ChildExitIdleDuringBuild$' -race -count=1 -timeout 120s
```

Excerpt of the output (binary timeout set to 40s by the script):

```console
=== RUN   Test/ChildExitIdleDuringBuild
    endpointsharding_ext_test.go:523: Timed out waiting for the builder to report IDLE
panic: test timed out after 40s
	running tests:
		Test (40s)
		Test/ChildExitIdleDuringBuild (40s)
...
goroutine 10 [chan receive]:
google.golang.org/grpc/balancer/endpointsharding_test.s.TestChildExitIdleDuringBuild.func5()
	.../balancer/endpointsharding/endpointsharding_ext_test.go:518 +0x31
runtime.Goexit()
testing.(*common).FailNow(...)
testing.(*common).Fatal(...)
google.golang.org/grpc/balancer/endpointsharding_test.s.TestChildExitIdleDuringBuild(...)
	.../balancer/endpointsharding/endpointsharding_ext_test.go:523 +0xb25
...
goroutine 11 [sync.Mutex.Lock]:
google.golang.org/grpc/balancer/endpointsharding.(*balancerWrapper).UpdateState.(*balancerWrapper).ExitIdle.func1(...)
	.../balancer/endpointsharding/endpointsharding.go:352 +0x3a
google.golang.org/grpc/balancer/endpointsharding.(*balancerWrapper).UpdateState(...)
	.../balancer/endpointsharding/endpointsharding.go:343 +0x125
google.golang.org/grpc/balancer/endpointsharding_test.s.TestChildExitIdleDuringBuild.func2(...)
	.../balancer/endpointsharding/endpointsharding_ext_test.go:495 +0xd4
google.golang.org/grpc/internal/balancer/stub.bb.Build(...)
google.golang.org/grpc/balancer/endpointsharding.(*endpointSharding).UpdateClientConnState(...)
	.../balancer/endpointsharding/endpointsharding.go:164 +0x8c7
real	0m40.627s
```

Reading: the test's own startup timeout fires at 10s and logs its message (line 523). `t.Fatal` then runs the deferred `<-updateDone` at line 518. The worker is not waiting on `release` (which the deferred `releaseBuild()` did close) but on the child mutex, so `updateDone` is never closed and the test sits in cleanup until the binary-wide timeout kills the whole package run. With the default `go test` timeout that is 10 minutes, and the result is a panic with a goroutine dump rather than a test failure.

Control: `control-bounded-cleanup-wait.patch` replaces only the bare receive with a `select` on `updateDone` / `time.After(defaultTestShortTimeout)`; the mutation is unchanged.

```sh
verify/repro/c1-4279a1cf/run.sh control
```

```console
=== RUN   Test/ChildExitIdleDuringBuild
    endpointsharding_ext_test.go:529: Timed out waiting for the builder to report IDLE
    endpointsharding_ext_test.go:522: UpdateClientConnState did not return after the builder was released
    grpctest.go:45: Leaked goroutine: goroutine 11 [sync.Mutex.Lock]:
    ...
--- FAIL: Test (20.08s)
    --- FAIL: Test/ChildExitIdleDuringBuild (20.08s)
FAIL	google.golang.org/grpc/balancer/endpointsharding	20.097s
```

With a bounded wait the same defect produces an ordinary `FAIL` in 20s instead of running into the binary timeout. The worker goroutine is still deadlocked, which is the mutation's doing and is now reported by the leak checker rather than hiding behind a hang. The hang is therefore caused by the bare receive.

Scope check on the other added test: the same mutation against `TestChildOperationsIndependent` does not hang, because in each of its failure paths the workers are waiting on `release` itself.

```sh
verify/repro/c1-4279a1cf/run.sh matrix
```

```console
        --- PASS: Test/ChildOperationsIndependent/UpdateClientConnState/ChildState (0.00s)
        --- FAIL: Test/ChildOperationsIndependent/UpdateClientConnState/Balancer (10.05s)
        --- PASS: Test/ChildOperationsIndependent/UpdateClientConnState/AutoReconnect (0.00s)
        --- PASS: Test/ChildOperationsIndependent/ResolverError/ChildState (0.00s)
        --- FAIL: Test/ChildOperationsIndependent/ResolverError/Balancer (10.05s)
        --- PASS: Test/ChildOperationsIndependent/ResolverError/AutoReconnect (0.00s)
        --- PASS: Test/ChildOperationsIndependent/Close/ChildState (0.00s)
        --- FAIL: Test/ChildOperationsIndependent/Close/Balancer (10.05s)
        --- PASS: Test/ChildOperationsIndependent/Close/AutoReconnect (0.00s)
        --- PASS: Test/ChildOperationsIndependent/ExitIdle/ChildState (0.00s)
        --- FAIL: Test/ChildOperationsIndependent/ExitIdle/Balancer (10.02s)
        --- PASS: Test/ChildOperationsIndependent/ExitIdle/AutoReconnect (0.00s)
real	0m42.737s
```

So on this branch the unbounded wait was observed in `TestChildExitIdleDuringBuild`. `TestChildOperationsIndependent` has the same bare-receive structure, but I did not find a failure that makes it hang; that part is read from source only.

Impact reasoning: the claim needs one test with an unbounded cleanup wait that is not preceded by observed worker completion. `TestChildExitIdleDuringBuild` has one, and it is reached by the exact regression the test was written to detect. The consequence is limited to test hygiene: a regression shows up as a package-wide timeout panic (default 10 minutes, taking every other test in the package down with it) instead of a 10-second named failure. It never affects a passing run. Verdict: CONFIRMED.

### C1 on `evalon/grpc-go-en-eb9cd093`

Suspected: a startup-timeout path exits without releasing a fixture barrier, leaving a worker waiting on it.

What the test file contains (`balancer/endpointsharding/endpointsharding_ext_test.go` at `5e07f940`):

```go
// fakeChild.UpdateClientConnState, lines 396-398
	if fc.ctrl.unblockUpdate != nil {
		<-fc.ctrl.unblockUpdate
	}
```

```go
// setupBlockingChildren, line 431
	t.Cleanup(es.Close)
```

```go
// blockChildUpdate, lines 469-489
	unblock := make(chan struct{})
	controls["b"].unblockUpdate = unblock
	updateDone := make(chan struct{})
	go func() {
		es.UpdateClientConnState(ccs)
		close(updateDone)
	}()
	select {
	case <-controls["b"].updateStarted:
	case <-ctx.Done():
		t.Fatalf("Timeout waiting for UpdateClientConnState on child %q", "b")
	}
	return func() {
		close(unblock)
		...
```

The only `close(unblock)` is inside the function that `blockChildUpdate` returns. On the `ctx.Done()` branch the helper calls `t.Fatalf` before returning, so the caller's `defer unblock()` (lines 499 and 525) is never registered and the barrier is never released. Both added tests go through this helper.

Baseline passes:

```sh
verify/repro/c1-eb9cd093/run.sh baseline
```

```console
=== RUN   Test/EndpointSharding_ChildStateExitIdleNotBlockedByOtherChild
--- PASS: Test (0.00s)
    --- PASS: Test/EndpointSharding_ChildStateExitIdleNotBlockedByOtherChild (0.00s)
ok  	google.golang.org/grpc/balancer/endpointsharding	1.015s
```

The startup timeout can only fire if the update reaches child "b" later than the 10s test deadline, so both probes below are scheduling probes: an 11s `time.Sleep` injected into production code in a throwaway worktree. The test file is unmodified in both.

Probe 1, `sched-probe-late-update.patch`: the second parent `UpdateClientConnState` sleeps 11s before doing anything.

```sh
verify/repro/c1-eb9cd093/run.sh leak
```

```console
=== RUN   Test/EndpointSharding_ChildStateExitIdleNotBlockedByOtherChild
    endpointsharding_ext_test.go:498: Timeout waiting for UpdateClientConnState on child "b"
    grpctest.go:45: Leaked goroutine: goroutine 11 [chan receive]:
        google.golang.org/grpc/balancer/endpointsharding_test.(*fakeChild).UpdateClientConnState(...)
        	.../balancer/endpointsharding/endpointsharding_ext_test.go:397 +0x239
        google.golang.org/grpc/balancer/endpointsharding.(*balancerWrapper).updateClientConnState(...)
        google.golang.org/grpc/balancer/endpointsharding.(*endpointSharding).UpdateClientConnState(...)
        google.golang.org/grpc/balancer/endpointsharding_test.blockChildUpdate.func1()
        	.../balancer/endpointsharding/endpointsharding_ext_test.go:473 +0x9e
    grpctest.go:77: Goroutine leak check disabled for future tests
--- FAIL: Test (20.02s)
    --- FAIL: Test/EndpointSharding_ChildStateExitIdleNotBlockedByOtherChild (20.02s)
```

The worker is left blocked at line 397, `<-fc.ctrl.unblockUpdate`, after the test has exited. Here `es.Close` ran before the worker took the child's mutex, so the test itself terminates; the cost is a permanently leaked goroutine and grpctest switching leak checking off for the rest of the package run.

Probe 2, `sched-probe-late-update-under-child-lock.patch`: the delay sits inside `balancerWrapper.updateClientConnState`, after the per-child mutex is taken, for child "b" only.

```sh
verify/repro/c1-eb9cd093/run.sh hang
```

```console
=== RUN   Test/EndpointSharding_ChildStateExitIdleNotBlockedByOtherChild
    endpointsharding_ext_test.go:498: Timeout waiting for UpdateClientConnState on child "b"
panic: test timed out after 1m0s
	running tests:
		Test (1m0s)
		Test/EndpointSharding_ChildStateExitIdleNotBlockedByOtherChild (1m0s)
...
goroutine 10 [sync.Mutex.Lock]:
google.golang.org/grpc/balancer/endpointsharding.(*balancerWrapper).close(...)
	.../balancer/endpointsharding/endpointsharding.go:412 +0x3a
google.golang.org/grpc/balancer/endpointsharding.(*endpointSharding).Close(...)
	.../balancer/endpointsharding/endpointsharding.go:241 +0x174
testing.(*common).Cleanup.func1()
testing.(*common).runCleanup(...)
runtime.Goexit()
testing.(*common).FailNow(...)
testing.(*common).Fatalf(...)
google.golang.org/grpc/balancer/endpointsharding_test.blockChildUpdate(...)
	.../balancer/endpointsharding/endpointsharding_ext_test.go:479 +0x35a
...
goroutine 11 [chan receive]:
google.golang.org/grpc/balancer/endpointsharding_test.(*fakeChild).UpdateClientConnState(...)
	.../balancer/endpointsharding/endpointsharding_ext_test.go:397 +0x239
google.golang.org/grpc/balancer/endpointsharding.(*balancerWrapper).updateClientConnState(...)
	.../balancer/endpointsharding/endpointsharding.go:407 +0x215
real	1m0.826s
```

Here the worker holds child "b"'s mutex while parked on the unreleased barrier, and the `t.Cleanup(es.Close)` registered at line 431 blocks on that mutex forever. The test never finishes; only the binary timeout ends it.

Control: `control-release-on-timeout.patch` adds one line, `close(unblock)`, before the `t.Fatalf` on the timeout branch. Probe 2 is unchanged.

```sh
verify/repro/c1-eb9cd093/run.sh control
```

```console
=== RUN   Test/EndpointSharding_ChildStateExitIdleNotBlockedByOtherChild
    endpointsharding_ext_test.go:499: Timeout waiting for UpdateClientConnState on child "b"
--- FAIL: Test (11.00s)
    --- FAIL: Test/EndpointSharding_ChildStateExitIdleNotBlockedByOtherChild (11.00s)
FAIL	google.golang.org/grpc/balancer/endpointsharding	11.019s
```

With the barrier released the same probe gives a clean failure in 11s, no hang and no leak report.

Impact reasoning: the suspected path exists exactly as described and both of its consequences were observed. It is reachable only when the blocked update arrives more than 10s late, which on the branch as committed means a stalled machine or a future change that slows or stalls delivery to the second child; I produced it with an injected sleep, not with a natural run. When it happens, the outcome is either a leaked goroutine that disables leak checking for later tests, or a hang until the package timeout. It never affects a passing run. Verdict: CONFIRMED.

## C2

Claim: at least one added concurrency test in `balancer/endpointsharding/endpointsharding_test.go` accepts a setup-generated signal as evidence that a later operation under test made progress. Target branch `evalon/grpc-go-en-89e1da1a`, commit `bac49fac71471cbaaa2b4be0d599b5abcce6c0a9`.

How the runs work: the target branch is not on `origin`; it is fetched from [kaitranntt-evals/grpc-go-endpointsharding-decouple-locking](https://github.com/kaitranntt-evals/grpc-go-endpointsharding-decouple-locking). `verify/repro/<dir>/run.sh <mode>` creates a clean detached worktree of the target branch under `~/verify-wt/`, applies that mode's patch(es) with `git apply`, and runs `go test -race`. Patches touch only the throwaway worktree. Run from the repository root of this branch. Environment: `go version go1.25.7 linux/amd64`, 8 CPUs. Tests on these branches run through `grpctest.RunSubTests`, so `TestFoo` is selected with `-run '^Test$/^Foo$'`; each excerpt shows the `=== RUN   Test/<name>` line to prove the test actually ran.

One discarded attempt: my first runs for this claim used a `-run` filter that matched nothing (`testing: warning: no tests to run`); none of that output is used.

Naming drift: on this branch `endpointsharding_test.go` is unchanged from the base commit. The added tests are in `balancer/endpointsharding/endpointsharding_locking_test.go`. `maintainedChild`, `enterUpdate`, `shouldBlock` and `TestDecoupledChildProgress` do not exist anywhere under `balancer/`; the nearest equivalents are `fakeChild` / `fakeChildController`. `TestAutoReconnect_NotBlockedByOtherChildUpdate`, `exitIdleCh` and `childB.reportIdle()` exist as named, and that test is what was adjudicated.

```sh
cd ~/verify-wt/89e1da1a   # any clean checkout of bac49fac
grep -rn "maintainedChild\|TestDecoupledChildProgress\|enterUpdate\|shouldBlock" balancer/
git diff --stat bf9e7cd3430df40d0732ba42eb88bd5f2cc63407 HEAD -- balancer/endpointsharding/endpointsharding_test.go
```

```console
(no output from either command; grep exit status 1)
```

Signal producers and consumers (`endpointsharding_locking_test.go`):

```go
// fakeChild.UpdateClientConnState, lines 67-79: every update reports IDLE
	if fc.ctrl.onUpdate != nil {
		fc.ctrl.onUpdate(fc.addr)
	}
	fc.reportIdle()
```

```go
// TestAutoReconnect_NotBlockedByOtherChildUpdate, lines 259-295
		onExitIdle: func(addr string) {
			select {
			case exitIdleCh <- addr:
			default:
			}
		},
	...
	es := NewBalancer(tcc, balancer.BuildOptions{}, ctrl.build, Options{})   // auto reconnect on
	if err := es.UpdateClientConnState(endpointsState("a", "b")); err != nil { ... }   // setup
	...
	go func() {
		es.UpdateClientConnState(endpointsState("a", "b"))   // blocks in child "a"
		close(updateDone)
	}()
	select {
	case <-updateBlocked:
	...
	// Drain ExitIdle calls triggered before child "a" blocked.
	for len(exitIdleCh) > 0 {
		<-exitIdleCh
	}
	childB.reportIdle()
	waitForExitIdle(ctx, t, exitIdleCh, "b")
```

Production side at `bac49fac`: `balancerWrapper.UpdateState` calls `bw.ExitIdle()`, which is `go bw.exitIdleSync()`. So the setup update leaves an asynchronous, un-awaited idle-exit goroutine for child "b". The blocking update can leave a second one if it reaches "b" before it parks in "a" (it did in two of the three traced runs below). Those goroutines write the same untagged `"b"` value to `exitIdleCh` as the operation under test does. The only thing separating them from the assertion is the `len(exitIdleCh) > 0` drain, which removes events that have already arrived and does nothing about ones still in flight.

Baseline passes:

```sh
verify/repro/c2-89e1da1a/run.sh baseline
```

```console
=== RUN   Test/AutoReconnect_NotBlockedByOtherChildUpdate
--- PASS: Test (0.00s)
    --- PASS: Test/AutoReconnect_NotBlockedByOtherChildUpdate (0.00s)
ok  	google.golang.org/grpc/balancer/endpointsharding	1.016s
```

### Probe A: remove the operation under test, production code untouched

`probeA-remove-reportIdle.patch` replaces the single line `childB.reportIdle()` with `_ = childB`. Nothing after the drain can now cause an idle exit on "b", so any pass is a stale event being accepted. 80 runs, 4 at a time, no injected delays:

```sh
verify/repro/c2-89e1da1a/run.sh probeA 80
```

```console
PASS: 29 of 80
FAIL (Timeout waiting for ExitIdle to be called on child "b"): 51 of 80
```

An earlier batch of 40 runs of the same probe gave 12 passes and 28 failures. Roughly a third of runs pass with the operation deleted.

### Probe B: a real regression plus a controlled schedule, test file untouched

`mutation-M2.patch` reintroduces the bug the task is about: every idle exit first waits for any in-flight parent update (`updateMu.Lock(); updateMu.Unlock()` before `exitIdleSync`). Under M2, child "b" cannot auto-reconnect while child "a"'s update is blocked, which is exactly what this test's name says it detects.

M2 is a genuine violation, shown by the sibling test that has no stale events (`DisableAutoReconnect: true`):

```sh
verify/repro/c2-89e1da1a/run.sh m2-sibling
```

```console
=== RUN   Test/ChildExitIdle_NotBlockedByOtherChildUpdate
    balancer.go:193: testutils.BalancerClientConn: UpdateState({IDLE 0xc000055b00})
    endpointsharding_locking_test.go:188: Timeout waiting for ExitIdle to be called on child "b"
panic: test timed out after 25s
```

The sibling reports the expected failure at line 188. It then does not return and the binary timeout fires; I did not investigate that further, since C1 is not targeted at this branch.

Unmodified `TestAutoReconnect_NotBlockedByOtherChildUpdate` under M2, natural scheduling:

```sh
verify/repro/c2-89e1da1a/run.sh m2 80
```

```console
PASS: 1 of 80
FAIL (Timeout waiting for ExitIdle to be called on child "b"): 79 of 80
```

So without any scheduling help the test passed once in 80 runs against the bug.

`mutation-M2-with-sched-probe-and-trace.patch` is M2 plus two sleeps that pin the ordering (20ms before a parent update takes `updateMu`; 50ms in the idle-exit goroutine after it passes the barrier) and a trace that numbers every idle-exit request and prints where it was requested from.

```sh
verify/repro/c2-89e1da1a/run.sh trace
```

`verify/logs/c2-89e1da1a-trace.log`, complete:

```console
=== RUN   Test
=== RUN   Test/AutoReconnect_NotBlockedByOtherChildUpdate
PROBE + 21.526ms UpdateClientConnState acquired updateMu
PROBE + 21.641ms exitIdle #1 child="a" REQUESTED, origin: inside a parent UpdateClientConnState call
PROBE + 21.696ms exitIdle #2 child="b" REQUESTED, origin: inside a parent UpdateClientConnState call
    balancer.go:193: testutils.BalancerClientConn: UpdateState({IDLE 0xc000053b40})
PROBE + 21.771ms UpdateClientConnState returning
PROBE + 21.794ms exitIdle #1 child="a" passed updateMu barrier
PROBE + 21.824ms exitIdle #2 child="b" passed updateMu barrier
PROBE + 42.077ms UpdateClientConnState acquired updateMu
PROBE + 42.175ms exitIdle #3 child="b" REQUESTED, origin: inside a parent UpdateClientConnState call
PROBE + 42.237ms exitIdle #4 child="b" REQUESTED, origin: child-initiated UpdateState outside any parent call (the test's childB.reportIdle())
PROBE + 72.863ms exitIdle #2 child="b" DELIVERED to child balancer
PROBE + 72.938ms exitIdle #5 child="a" REQUESTED, origin: inside a parent UpdateClientConnState call
    balancer.go:193: testutils.BalancerClientConn: UpdateState({IDLE 0xc000120100})
PROBE + 73.031ms UpdateClientConnState returning
PROBE + 73.046ms exitIdle #5 child="a" passed updateMu barrier
PROBE + 73.104ms exitIdle #1 child="a" DELIVERED to child balancer
PROBE + 73.124ms exitIdle #3 child="b" passed updateMu barrier
PROBE + 73.136ms exitIdle #4 child="b" passed updateMu barrier
PROBE +124.631ms exitIdle #3 child="b" DELIVERED to child balancer
PROBE +124.675ms exitIdle #5 child="a" DELIVERED to child balancer
PROBE +124.688ms exitIdle #4 child="b" DELIVERED to child balancer
--- PASS: Test (0.18s)
    --- PASS: Test/AutoReconnect_NotBlockedByOtherChildUpdate (0.17s)
=== RUN   Test
--- PASS: Test (0.00s)
PASS
ok  	google.golang.org/grpc/balancer/endpointsharding	1.190s
```

Reading the trace in order:

- `#2` is requested at 21.7ms during the setup update. It is the stale event.
- The blocking update starts at 42.1ms and parks in child "a". The test drains an empty channel and calls `childB.reportIdle()`, which is `#4` at 42.2ms, the operation under test.
- `#2` reaches the child at 72.86ms. The test's `waitForExitIdle` returns on it and closes `unblock`, which is why child "a" resumes at 72.94ms and the update returns at 73.03ms.
- `#4` passes the barrier only at 73.14ms, after the blocked update has returned, and is delivered at 124.7ms.

The assertion was satisfied by the setup-generated request while the request it is meant to observe was still stuck behind child "a"'s update. The test reports `PASS` against an implementation in which auto reconnect is blocked by another child's update. I ran this mode three times; all three passed with the same shape. In the other two runs the setup request for "b" was `#1`, delivered at 73.04ms and 72.71ms, and the test's own request (`#3` and `#4`) passed the barrier at 73.34ms and 72.91ms, after `UpdateClientConnState returning`, and was delivered at 123.75ms and 123.62ms. Three runs is a small sample; the sleeps are there to make the ordering repeatable, not to measure how often it occurs naturally (the 1-in-80 figure above is the natural rate observed).

Impact reasoning: stale and fresh events are indistinguishable on `exitIdleCh`, the stale producers are never joined, and the drain is a snapshot. Probe A shows the assertion passes about a third of the time with the operation removed on the branch's own production code; Probe B shows a complete ordering in which it passes while the intended operation is blocked. The practical effect is a regression test that can report success against the regression it names. Under natural scheduling against M2 that was rare (1 in 80, twice), so the test does usually catch that particular bug; the weakness is that it is not guaranteed to. The other two added tests set `DisableAutoReconnect: true`, produce no setup idle exits, and are not affected. Verdict: CONFIRMED.
