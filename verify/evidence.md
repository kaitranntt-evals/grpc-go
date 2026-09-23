## Evidence — behavioral audit of grpc-go-endpointsharding-decouple-locking (run v-6ebdc358)

Observations only; verdicts live in the (uncommitted) report. Environment used for every command below:
`go version go1.25.7 linux/amd64`, race detector on. The audited solution branch
(`grpc-go-endpointsharding-decouple-locking-perfect`, HEAD `81201fc0`) is checked out in the primary checkout;
each claim-target branch `evalon/grpc-go-en-<suffix>` was fetched from
`https://github.com/kaitranntt-evals/grpc-go-endpointsharding-decouple-locking` into its own worktree
`$WT/<suffix>` (`git worktree add $WT/<suffix> evalon/grpc-go-en-<suffix>`). Merge base of every target branch is
`bf9e7cd3` (xds: parse allowed_grpc_services ... #9194). Target-branch HEADs at audit time:

```console
08e6c604 a3d4509e  15d00ee8 2678ec6f  1b022fbb c408b76c  1eca0068 6f9d30dd  212e3582 6eec86ae  26ec74be dc5b2168
2805a453 1443e1fa  28870ca2 dd832f7a  349fa040 d0419acd  3d4a2ba9 88e0ae89  417e24dc 9f74e9df  6ab5ed6f 3b16a93d
71865894 e8aadd64  7935c7b4 9a5f647b  83039173 73a1e2c2  9849d323 ffac0346  a7641ce6 8d1ec410  ae479324 99797683
bd3553ea f1b96e61  bf5cd862 a8efa13b  dae0ce93 e9994edd  f0a4f7e8 b9ecef78  f1dbdc2b c2a842d5  f563f1eb 5d4c8e28
f93e7e52 8b5d90da  fd6b3403 4eb60b4b
```

The eval fixture was extracted byte-exactly from the attached `eval_tests.zip` to
`<extracted>/tests/eval_endpointsharding_test.go` and, where a claim needed it, copied to
`balancer/endpointsharding/eval_endpointsharding_test.go` in the relevant worktree (`cmp` against the archive is part
of the C4 driver output). All probes/mutations/fault injections were applied temporarily to worktrees and reverted
(`git status --short` empty in the primary checkout and all 26 worktrees before committing); the sources are kept
under `verify/repro/` (Go probes carry a `.go.txt` suffix so they are not compiled as maintained tests). Paths of the form
`~/verify_work/*.txt` are the audit machine's local scratch logs from which the verbatim excerpts were taken; they are not committed.

## C1

Claim: the changed concurrency tests on the target do not establish that one child's operation is *confirmed held*
while another child's operation *fully completes*.

Target: [evalon/grpc-go-en-28870ca2](https://github.com/kaitranntt-evals/grpc-go-endpointsharding-decouple-locking/tree/evalon/grpc-go-en-28870ca2) (HEAD `dd832f7a`).

```sh
cd $WT/28870ca2 && git diff bf9e7cd3 --stat && git diff bf9e7cd3 -- balancer/endpointsharding/endpointsharding_ext_test.go | grep '^+func'
```

```console
 balancer/endpointsharding/endpointsharding.go      | 124 ++++++------
 .../endpointsharding/endpointsharding_ext_test.go  | 211 +++++++++++++++++++++
 balancer/ringhash/ringhash.go                      |  13 +-
 3 files changed, 289 insertions(+), 59 deletions(-)
+func (b *blockingChild) UpdateClientConnState(balancer.ClientConnState) error {
+func (b *blockingChild) ResolverError(error)                                        {}
+func (b *blockingChild) UpdateSubConnState(balancer.SubConn, balancer.SubConnState) {}
+func (b *blockingChild) Close()                                                     {}
+func (b *blockingChild) ExitIdle() {
+func (s) TestEndpointShardingExitIdleDuringOtherChildUpdate(t *testing.T) {
+func (s) TestEndpointShardingSynchronousIdleDuringChildBuild(t *testing.T) {
```

The only changed test that involves two children with one operation held is
`TestEndpointShardingExitIdleDuringOtherChildUpdate` (the other new test exercises construction-time Idle for a single
child being built and has no held sibling operation). Its "other child is held" gate and its assertion are
(`endpointsharding_ext_test.go` lines 455-482 on the target):

```go
	otherChild.unblock = make(chan struct{})
	updateDone := make(chan error, 1)
	go func() {
		updateDone <- es.UpdateClientConnState(balancer.ClientConnState{
			ResolverState: resolver.State{Endpoints: []resolver.Endpoint{ep1, ep2}},
		})
	}()
	// Wait until the other child is blocked inside its update. Both children
	// receive the update in a random order, so wait for the blocked child.
	select {
	case <-otherChild.updateCalled:
	case <-ctx.Done():
		t.Fatal("Timed out waiting for the child update to start")
	}

	// An ExitIdle request for the idle child must be delivered even though the
	// other child's update is still blocked.
	childStates[0].ExitIdle()
	select {
	case <-idleChild.exitIdle:
	case <-ctx.Done():
		t.Fatal("Timed out waiting for ExitIdle while another child's update was blocked")
	}
```

`updateCalled` is declared `make(chan struct{}, 10)` (line 407) and `blockingChild.UpdateClientConnState` sends to it
on *every* call (line 370), including the initial one-endpoint `UpdateClientConnState` that created the children. So the
gate can be satisfied by the stale token from the initial update, before the background update has even started, and
nothing in the test confirms the update is actually held at the moment `ExitIdle()` is asserted to complete.

Method (mutation test): re-introduce a coarse parent-wide lock (`verify/repro/c1_coarse_lock_mutation.patch`, 6 added
lines in `endpointsharding.go`: `coarseMu` taken for the whole `UpdateClientConnState` and in
`balancerWrapper.exitIdle`), so that ExitIdle on child A *cannot* progress while child B's update is held. If the
changed test established "B's update is held, then A's ExitIdle completes", it would have to fail under this mutation
every time.

```sh
cd $WT/28870ca2 && bash verify/repro/c1_run.sh   # applies the patch, builds a race test binary, runs the changed test 20x, then reruns once with the stale signal drained; reverts everything on exit
```

Key output (first phase, verbatim):

```console
== mutated production code (git diff --stat):
 balancer/endpointsharding/endpointsharding.go | 6 ++++++
 1 file changed, 6 insertions(+)
run 1: FAIL (panic: test timed out after 10s)
run 2: PASS
run 3: PASS
run 4: PASS
run 5: PASS
run 6: FAIL (panic: test timed out after 10s)
run 7: PASS
run 8: PASS
run 9: PASS
run 10: PASS
run 11: FAIL (panic: test timed out after 10s)
run 12: PASS
run 13: PASS
run 14: PASS
run 15: PASS
run 16: PASS
run 17: PASS
run 18: PASS
run 19: PASS
run 20: FAIL (panic: test timed out after 10s)
== 20 runs with coarse lock: PASS=16 FAIL=4
```

The mutated (fully serialized) implementation passes the independent-progress test 16/20. An earlier identical 20-run
sample (same binary, `~/verify_work/c1_mutation_20runs.txt`) gave 13/20:

```console
run 1: PASS
run 2: PASS
run 3: PASS
run 4: PASS
run 5: FAIL()
run 6: FAIL()
run 7: PASS
run 8: PASS
run 9: FAIL()
run 10: PASS
run 11: PASS
run 12: FAIL()
run 13: PASS
run 14: FAIL()
run 15: PASS
run 16: PASS
run 17: PASS
run 18: FAIL()
run 19: PASS
run 20: FAIL()
```

Second phase of the same script: one test-only edit drains the stale token, then the test is rerun once under the
same mutation:

```console
== drained rerun:
=== RUN   Test
--- PASS: Test (0.00s)
=== RUN   Test
=== RUN   Test/EndpointShardingExitIdleDuringOtherChildUpdate
    balancer.go:193: testutils.BalancerClientConn: UpdateState({IDLE 0xc000051b40})
    endpointsharding_ext_test.go:481: Timed out waiting for ExitIdle while another child's update was blocked
panic: test timed out after 30s
	running tests:
		Test (30s)
		Test/EndpointShardingExitIdleDuringOtherChildUpdate (30s)
goroutine 18 [running]:
goroutine 1 [chan receive]:
	_testmain.go:49 +0x165
goroutine 9 [chan receive]:
google.golang.org/grpc/balancer/endpointsharding_test.Test(0xc0000bea80)
goroutine 10 [sync.Mutex.Lock]:
google.golang.org/grpc/balancer/endpointsharding.(*balancerWrapper).close(0xc000210b00)
google.golang.org/grpc/balancer/endpointsharding.(*endpointSharding).Close-range1(...)
google.golang.org/grpc/balancer/endpointsharding.(*endpointSharding).Close.(*EndpointMap[...]).All.func1(...)
google.golang.org/grpc/balancer/endpointsharding.(*endpointSharding).Close(0xc0002ce3c0)
google.golang.org/grpc/balancer/endpointsharding_test.s.TestEndpointShardingExitIdleDuringOtherChildUpdate({{}}, 0xc0000bec40)
goroutine 12 [chan receive]:
google.golang.org/grpc/balancer/endpointsharding_test.(*blockingChild).UpdateClientConnState(0xc000274fc0, {{{0x0, 0x0, 0x0}, {0xc000090f60, 0x1, 0x1}, 0x0, 0x0}, {0x0, ...}})
google.golang.org/grpc/balancer/endpointsharding.(*balancerWrapper).updateClientConnState(0xc000210b00, {{{0x0, 0x0, 0x0}, {0xc000090f60, 0x1, 0x1}, 0x0, 0x0}, {0x0, ...}})
google.golang.org/grpc/balancer/endpointsharding.(*endpointSharding).UpdateClientConnState(0xc0002ce3c0, {{{0x0, 0x0, 0x0}, {0xc000051b80, 0x2, 0x2}, 0x0, 0x0}, {0x0, ...}})
google.golang.org/grpc/balancer/endpointsharding_test.s.TestEndpointShardingExitIdleDuringOtherChildUpdate.func2()
goroutine 13 [sync.Mutex.Lock]:
google.golang.org/grpc/balancer/endpointsharding.(*balancerWrapper).exitIdle(0xc000210a80)
FAIL	google.golang.org/grpc/balancer/endpointsharding	30.102s
FAIL
```

With the stale token gone the same mutation is caught deterministically: the assertion at line 481 fails, and the
goroutine dump shows exactly the situation the test was meant to detect — the background update parked inside
`blockingChild.UpdateClientConnState` (goroutine 12), `balancerWrapper.exitIdle` blocked on the coarse lock
(goroutine 13), and then the deferred `es.Close()` blocked on that child's mutex (goroutine 10) until the go test
timeout. A `-count=10` run of the undrained test under the mutation also ended in `panic: test timed out after 2m0s`
(`~/verify_work/c1_mutation_count10_out.txt`), i.e. one of the 10 iterations lost the race the same way.

Reading: the changed test passes or fails depending on whether `childStates[0].ExitIdle()` happens to run before the
background `UpdateClientConnState` reaches the child; it does not first confirm that the other child's operation is
held, so it does not establish "held, then the other child's operation completes". The claim as worded is borne out.

Impact reasoning: the branch's only test for cross-child independence is satisfied 65-80% of the time by an
implementation with *no* such independence (a coarse lock), so a regression re-introducing head-of-line blocking
between children would surface, at best, as a flaky test.
Replay: `verify/repro/c1_run.sh` (needs `verify/repro/c1_coarse_lock_mutation.patch` alongside; reverts the production
file and the test edit on exit). Raw logs (local scratch, not committed): `~/verify_work/c1_run_out.txt`, `c1_mutation_20runs.txt`,
`c1_mutation_drained_out.txt`, `c1_mutation_count10_out.txt`.

## C2

Sixteen target branches, adjudicated independently. Method (fault injection into the *test's own fake child*, no
production change): for the changed "ExitIdle while another child's update is blocked" concurrency test on each
branch, `verify/repro/run_c2.py` inserts `<-make(chan struct{}) // INJECT(C2)` at the start of the fake child's
`ExitIdle` (anchors per branch are listed in the script), so the idle-exit worker spawned by the parent never finishes
and never signals the test. The test's ExitIdle assertion then fails on its own deadline, and we observe what the
test's cleanup does afterwards under a hard `go test -timeout`. A run that ends with `panic: test timed out after
40s` and a goroutine dump means cleanup did not terminate; the dump identifies the blocked goroutine and what it waits
on. Driver and summarizer:

```sh
C2_WT=$WT C2_OUT=./c2_out python3 verify/repro/run_c2.py          # all 17 cases (16 branches + a 2nd test on 3d4a2ba9)
C2_OUT=./c2_out python3 verify/repro/summarize_c2.py
```

Result overview (17 injected runs; 3d4a2ba9 has three logs, see its subsections):

```console
case                   result                                 cleanup goroutine observed at timeout
f1dbdc2b               hung until go -timeout (40s)           test goroutine in deferred <-updateDone; update worker blocked on bw.mu held by injected idle worker
2805a453               hung until go -timeout (40s)           deferred es.Close() -> balancerWrapper.close blocked on bw.mu held by injected idle worker
fd6b3403               hung until go -timeout (40s)           deferred Close -> balancerWrapper.close blocked on bw.mu
08e6c604               hung until go -timeout (40s)           deferred Close -> balancerWrapper.close blocked on bw.mu
f0a4f7e8               hung until go -timeout (40s)           deferred Close -> balancerWrapper.close blocked on bw.mu
417e24dc               hung until go -timeout (40s)           deferred Close -> balancerWrapper.close blocked on bw.mu
26ec74be               hung until go -timeout (40s)           deferred Close -> balancerWrapper.close blocked on bw.mu
bf5cd862               hung until go -timeout (40s)           deferred Close -> balancerWrapper.close blocked on bw.mu
f563f1eb               hung until go -timeout (40s)           deferred Close -> balancerWrapper.close blocked on bw.mu
1b022fbb               hung until go -timeout (40s)           deferred Close -> balancerWrapper.close blocked on bw.mu
ae479324               hung until go -timeout (40s)           deferred Close -> balancerWrapper.close blocked on bw.mu
71865894               hung until go -timeout (40s)           deferred Close -> balancerWrapper.close blocked on bw.mu
1eca0068               hung until go -timeout (40s)           deferred Close -> balancerWrapper.close blocked on bw.mu
dae0ce93               hung until go -timeout (40s)           deferred Close -> balancerWrapper.close blocked on bw.mu
3d4a2ba9 (40s)         go -timeout fired at 40s               inside the *bounded* 10s cleanup awaitEvent(done) of subtest 2 (not a hang; see 120s log)
3d4a2ba9 (120s)        terminated at 50.16s (no go timeout)   cleanup awaitEvent(done) timed out -> t.Fatal; Close skipped; leaked update+idle goroutines (this test terminates)
3d4a2ba9-serialized    hung until go -timeout (40s)           deferred b.Close() -> balancerWrapper.close blocked on bw.mu held by queued idle worker
f93e7e52               hung until go -timeout (40s)           deferred Close -> balancerWrapper.close blocked on bw.mu
```

In every hung run (all 16 branches; on 3d4a2ba9 via its `TestChildCallsSerialized` test) the lock owner is the injected idle-exit worker, which sits in `balancerWrapper.exitIdle` holding
`bw.mu` (the child-operation mutex) while inside the fake child's `ExitIdle`. Cleanup either calls `Close()`
(which takes `bw.mu` per child) or joins the update worker (which needs the same `bw.mu`), so with the idle worker
stuck the failure path cannot terminate. The one changed test whose cleanup *does* terminate (3d4a2ba9's
`TestChildExitIdleDuringOtherChildUpdate`, bounded `awaitEvent` + `t.Fatal` before `Close`) is documented in its
subsection; it does not change that branch's outcome because its sibling test `TestChildCallsSerialized` blocks in `Close`. Impact reasoning (all branches): a real regression that makes ExitIdle
hang would not produce a readable assertion failure in CI — the package run dies at the global `go test -timeout`
(7 minutes in `.github/workflows/testing.yml`) with a goroutine dump, masking which assertion failed and blocking every other test in
the package. Replay: `run_c2.py` then `summarize_c2.py`, or the per-branch commands below (each is run inside the
branch worktree after the one-line injection; the script reverts the test file afterwards).

Per-branch logs (`$C2_OUT/<case>.txt`). Frames outside the package are elided; `<wt>` is the worktree root.


### Log `c2_out/f1dbdc2b.txt` — branch [evalon/grpc-go-en-f1dbdc2b](https://github.com/kaitranntt-evals/grpc-go-endpointsharding-decouple-locking/tree/evalon/grpc-go-en-f1dbdc2b), HEAD `c2a842d5`

```sh
# cwd: the branch worktree, after run_c2.py injected `<-make(chan struct{})` into the fake child's ExitIdle
go test ./balancer/endpointsharding -run Test/EndpointShardingExitIdleNotBlockedByOtherChild$ -race -count=1 -timeout 40s -v
```

Key output (verbatim lines):

```console
endpointsharding_ext_test.go:537: Timed out waiting for ExitIdle to be called on child 1 while child 2's update is blocked
panic: test timed out after 40s
FAIL	google.golang.org/grpc/balancer/endpointsharding	40.115s
```

Goroutines at the Go test timeout (frames outside the package elided):

- test goroutine parked in the deferred cleanup's unbounded `<-updateDone` join (endpointsharding_ext_test.go:519-522: `defer func() { releaseChild2(); <-updateDone }()`), entered via t.Fatalf -> runtime.Goexit:

```console
goroutine 10 [chan receive]:
google.golang.org/grpc/balancer/endpointsharding_test.s.TestEndpointShardingExitIdleNotBlockedByOtherChild.func4()
	<wt>/f1dbdc2b/balancer/endpointsharding/endpointsharding_ext_test.go:520 +0x45
runtime.Goexit()
testing.(*common).FailNow(0xc0000bec40)
testing.(*common).Fatalf(0xc0000bec40, {0x123da60, 0x58}, {0x0, 0x0, 0x0})
```

- update worker blocked on child mutex (the join above waits for this goroutine, which waits for the idle worker's lock):

```console
goroutine 11 [sync.Mutex.Lock]:
google.golang.org/grpc/balancer/endpointsharding.(*balancerWrapper).updateClientConnState(0xc000212c00, {{{0x0, 0x0, 0x0}, {0xc000091040, 0x1, 0x1}, 0x0, 0x0}, {0x0, ...}})
	<wt>/f1dbdc2b/balancer/endpointsharding/endpointsharding.go:403 +0x56
google.golang.org/grpc/balancer/endpointsharding.(*endpointSharding).UpdateClientConnState(0xc0000fefc0, {{{0x0, 0x0, 0x0}, {0xc000051c40, 0x2, 0x2}, 0x0, 0x0}, {0x0, ...}})
	<wt>/f1dbdc2b/balancer/endpointsharding/endpointsharding.go:192 +0x985
google.golang.org/grpc/balancer/endpointsharding_test.s.TestEndpointShardingExitIdleNotBlockedByOtherChild.func2()
	<wt>/f1dbdc2b/balancer/endpointsharding/endpointsharding_ext_test.go:509 +0x283
	<wt>/f1dbdc2b/balancer/endpointsharding/endpointsharding_ext_test.go:507 +0xc11
```

- injected idle worker (holds child mutex):

```console
goroutine 12 [chan receive]:
google.golang.org/grpc/balancer/endpointsharding_test.s.TestEndpointShardingExitIdleNotBlockedByOtherChild.func1.2({0x120db3a, 0x5})
	<wt>/f1dbdc2b/balancer/endpointsharding/endpointsharding_ext_test.go:488 +0x45
google.golang.org/grpc/balancer/endpointsharding_test.(*controllableChild).ExitIdle(0xc000051bc0)
	<wt>/f1dbdc2b/balancer/endpointsharding/endpointsharding_ext_test.go:419 +0xf5
google.golang.org/grpc/balancer/endpointsharding.(*balancerWrapper).exitIdle(0xc000212c00)
	<wt>/f1dbdc2b/balancer/endpointsharding/endpointsharding.go:397 +0xcc
	<wt>/f1dbdc2b/balancer/endpointsharding/endpointsharding.go:386 +0x8b
```

Reading (f1dbdc2b): the test goroutine is parked in the deferred unbounded `<-updateDone` join; the update worker it waits for is blocked on `bw.mu`, which the injected idle-exit worker holds. Cleanup releases the test barrier (`releaseChild2()`) but joins the worker without a bound and never reaches `Close`; the run ended only at the go timeout.

### Log `c2_out/2805a453.txt` — branch [evalon/grpc-go-en-2805a453](https://github.com/kaitranntt-evals/grpc-go-endpointsharding-decouple-locking/tree/evalon/grpc-go-en-2805a453), HEAD `1443e1fa`

```sh
# cwd: the branch worktree, after run_c2.py injected `<-make(chan struct{})` into the fake child's ExitIdle
go test ./balancer/endpointsharding -run Test/EndpointShardingExitIdleNotBlockedByOtherChildUpdate$ -race -count=1 -timeout 40s -v
```

Key output (verbatim lines):

```console
endpointsharding_ext_test.go:462: Timed out waiting for ExitIdle to reach the child for ep1 while the child for ep2 was blocked in UpdateClientConnState
panic: test timed out after 40s
FAIL	google.golang.org/grpc/balancer/endpointsharding	40.043s
```

Goroutines at the Go test timeout (frames outside the package elided):

- Close blocked on child mutex:

```console
goroutine 23 [sync.Mutex.Lock]:
google.golang.org/grpc/balancer/endpointsharding.(*balancerWrapper).close(0xc00021ca80)
	<wt>/2805a453/balancer/endpointsharding/endpointsharding.go:428 +0x3a
google.golang.org/grpc/balancer/endpointsharding.(*endpointSharding).Close-range1(...)
	<wt>/2805a453/balancer/endpointsharding/endpointsharding.go:239
google.golang.org/grpc/balancer/endpointsharding.(*endpointSharding).Close.(*EndpointMap[...]).All.func1(...)
google.golang.org/grpc/balancer/endpointsharding.(*endpointSharding).Close(0xc00020afc0)
	<wt>/2805a453/balancer/endpointsharding/endpointsharding.go:238 +0x174
```

- injected idle worker (holds child mutex):

```console
goroutine 25 [chan receive]:
google.golang.org/grpc/balancer/endpointsharding_test.s.TestEndpointShardingExitIdleNotBlockedByOtherChildUpdate.func3(0xc00020b0e0)
	<wt>/2805a453/balancer/endpointsharding/endpointsharding_ext_test.go:419 +0x6c
google.golang.org/grpc/balancer/endpointsharding.(*balancerWrapper).exitIdle(0xc00021ca80)
	<wt>/2805a453/balancer/endpointsharding/endpointsharding.go:395 +0xe8
	<wt>/2805a453/balancer/endpointsharding/endpointsharding.go:384 +0x8b
```

Reading (2805a453): after the ExitIdle assertion's own deadline fires, `t.Fatal` -> `runtime.Goexit` runs the deferred `Close()`; `balancerWrapper.close` blocks acquiring `bw.mu`, which the injected idle-exit worker (in `balancerWrapper` idle-exit under `bw.mu`, inside the fake child's `ExitIdle`) still holds. Cleanup does not first wait, with a bound, for that worker, so the failure path never terminates on its own; the run ended only at `go test -timeout`.

### Log `c2_out/fd6b3403.txt` — branch [evalon/grpc-go-en-fd6b3403](https://github.com/kaitranntt-evals/grpc-go-endpointsharding-decouple-locking/tree/evalon/grpc-go-en-fd6b3403), HEAD `4eb60b4b`

```sh
# cwd: the branch worktree, after run_c2.py injected `<-make(chan struct{})` into the fake child's ExitIdle
go test ./balancer/endpointsharding -run Test/EndpointShardingExitIdleWithBlockedSibling$ -race -count=1 -timeout 40s -v
```

Key output (verbatim lines):

```console
endpointsharding_ext_test.go:493: Timed out waiting for ExitIdle to be delivered to the child while a sibling update is blocked
panic: test timed out after 40s
FAIL	google.golang.org/grpc/balancer/endpointsharding	40.116s
```

Goroutines at the Go test timeout (frames outside the package elided):

- Close blocked on child mutex:

```console
goroutine 10 [sync.Mutex.Lock]:
google.golang.org/grpc/balancer/endpointsharding.(*balancerWrapper).close(0xc000192b00)
	<wt>/fd6b3403/balancer/endpointsharding/endpointsharding.go:425 +0x3a
google.golang.org/grpc/balancer/endpointsharding.(*endpointSharding).Close-range1(...)
	<wt>/fd6b3403/balancer/endpointsharding/endpointsharding.go:245
google.golang.org/grpc/balancer/endpointsharding.(*endpointSharding).Close.(*EndpointMap[...]).All.func1(...)
google.golang.org/grpc/balancer/endpointsharding.(*endpointSharding).Close(0xc0000fefc0)
	<wt>/fd6b3403/balancer/endpointsharding/endpointsharding.go:244 +0x174
```

- update worker blocked on child mutex:

```console
goroutine 11 [sync.Mutex.Lock]:
google.golang.org/grpc/balancer/endpointsharding.(*balancerWrapper).updateClientConnState(0xc000192b00, {{{0x0, 0x0, 0x0}, {0xc000090fe0, 0x1, 0x1}, 0x0, 0x0}, {0x0, ...}})
	<wt>/fd6b3403/balancer/endpointsharding/endpointsharding.go:410 +0x56
google.golang.org/grpc/balancer/endpointsharding.(*endpointSharding).UpdateClientConnState(0xc0000fefc0, {{{0x0, 0x0, 0x0}, {0xc000051ac0, 0x2, 0x2}, 0x0, 0x0}, {0x0, ...}})
	<wt>/fd6b3403/balancer/endpointsharding/endpointsharding.go:196 +0x9a5
google.golang.org/grpc/balancer/endpointsharding_test.s.TestEndpointShardingExitIdleWithBlockedSibling.func3()
	<wt>/fd6b3403/balancer/endpointsharding/endpointsharding_ext_test.go:469 +0x13d
	<wt>/fd6b3403/balancer/endpointsharding/endpointsharding_ext_test.go:467 +0x112b
```

- injected idle worker (holds child mutex):

```console
goroutine 12 [chan receive]:
google.golang.org/grpc/balancer/endpointsharding_test.s.TestEndpointShardingExitIdleWithBlockedSibling.func2(0xc0000ff200)
	<wt>/fd6b3403/balancer/endpointsharding/endpointsharding_ext_test.go:436 +0xa5
google.golang.org/grpc/balancer/endpointsharding.(*balancerWrapper).exitIdle(0xc000192b00)
	<wt>/fd6b3403/balancer/endpointsharding/endpointsharding.go:404 +0xe8
	<wt>/fd6b3403/balancer/endpointsharding/endpointsharding.go:393 +0x8b
```

Reading (fd6b3403): after the ExitIdle assertion's own deadline fires, `t.Fatal` -> `runtime.Goexit` runs the deferred `Close()`; `balancerWrapper.close` blocks acquiring `bw.mu`, which the injected idle-exit worker (in `balancerWrapper` idle-exit under `bw.mu`, inside the fake child's `ExitIdle`) still holds. Cleanup does not first wait, with a bound, for that worker, so the failure path never terminates on its own; the run ended only at `go test -timeout`.

### Log `c2_out/08e6c604.txt` — branch [evalon/grpc-go-en-08e6c604](https://github.com/kaitranntt-evals/grpc-go-endpointsharding-decouple-locking/tree/evalon/grpc-go-en-08e6c604), HEAD `a3d4509e`

```sh
# cwd: the branch worktree, after run_c2.py injected `<-make(chan struct{})` into the fake child's ExitIdle
go test ./balancer/endpointsharding -run Test/EndpointShardingChildExitIdleNotBlockedByOtherChildUpdate$ -race -count=1 -timeout 40s -v
```

Key output (verbatim lines):

```console
endpointsharding_ext_test.go:535: Timed out waiting for ExitIdle on the first child while the second child's update is blocked
panic: test timed out after 40s
FAIL	google.golang.org/grpc/balancer/endpointsharding	40.125s
```

Goroutines at the Go test timeout (frames outside the package elided):

- Close blocked on child mutex:

```console
goroutine 22 [sync.Mutex.Lock]:
google.golang.org/grpc/balancer/endpointsharding.(*balancerWrapper).close(0xc00018ca00)
	<wt>/08e6c604/balancer/endpointsharding/endpointsharding.go:451 +0x3a
google.golang.org/grpc/balancer/endpointsharding.(*endpointSharding).Close-range1(...)
	<wt>/08e6c604/balancer/endpointsharding/endpointsharding.go:239
google.golang.org/grpc/balancer/endpointsharding.(*endpointSharding).Close.(*EndpointMap[...]).All.func1(...)
google.golang.org/grpc/balancer/endpointsharding.(*endpointSharding).Close(0xc00017afc0)
	<wt>/08e6c604/balancer/endpointsharding/endpointsharding.go:238 +0x174
```

- update worker blocked on child mutex:

```console
goroutine 23 [sync.Mutex.Lock]:
google.golang.org/grpc/balancer/endpointsharding.(*balancerWrapper).updateClientConnState(0xc00018ca00, {{{0x0, 0x0, 0x0}, {0xc0002ac000, 0x1, 0x1}, 0x0, 0x0}, {0x0, ...}})
	<wt>/08e6c604/balancer/endpointsharding/endpointsharding.go:427 +0x56
google.golang.org/grpc/balancer/endpointsharding.(*endpointSharding).UpdateClientConnState(0xc00017afc0, {{{0x0, 0x0, 0x0}, {0xc00012d980, 0x2, 0x2}, 0x0, 0x0}, {0x0, ...}})
	<wt>/08e6c604/balancer/endpointsharding/endpointsharding.go:190 +0x965
google.golang.org/grpc/balancer/endpointsharding_test.s.TestEndpointShardingChildExitIdleNotBlockedByOtherChildUpdate.func1()
	<wt>/08e6c604/balancer/endpointsharding/endpointsharding_ext_test.go:518 +0xfa
	<wt>/08e6c604/balancer/endpointsharding/endpointsharding_ext_test.go:517 +0x1087
```

- injected idle worker (holds child mutex):

```console
goroutine 24 [chan receive]:
google.golang.org/grpc/balancer/endpointsharding_test.(*testChildBalancer).ExitIdle(0xc0001f0ed0)
	<wt>/08e6c604/balancer/endpointsharding/endpointsharding_ext_test.go:448 +0xa5
google.golang.org/grpc/balancer/endpointsharding.(*balancerWrapper).exitIdleSync(0xc00018ca00)
	<wt>/08e6c604/balancer/endpointsharding/endpointsharding.go:418 +0xcc
	<wt>/08e6c604/balancer/endpointsharding/endpointsharding.go:406 +0x8b
```

Reading (08e6c604): after the ExitIdle assertion's own deadline fires, `t.Fatal` -> `runtime.Goexit` runs the deferred `Close()`; `balancerWrapper.close` blocks acquiring `bw.mu`, which the injected idle-exit worker (in `balancerWrapper` idle-exit under `bw.mu`, inside the fake child's `ExitIdle`) still holds. Cleanup does not first wait, with a bound, for that worker, so the failure path never terminates on its own; the run ended only at `go test -timeout`.

### Log `c2_out/f0a4f7e8.txt` — branch [evalon/grpc-go-en-f0a4f7e8](https://github.com/kaitranntt-evals/grpc-go-endpointsharding-decouple-locking/tree/evalon/grpc-go-en-f0a4f7e8), HEAD `b9ecef78`

```sh
# cwd: the branch worktree, after run_c2.py injected `<-make(chan struct{})` into the fake child's ExitIdle
go test ./balancer/endpointsharding -run Test/EndpointShardingExitIdleWhileOtherChildUpdateBlocked$ -race -count=1 -timeout 40s -v
```

Key output (verbatim lines):

```console
endpointsharding_ext_test.go:581: Timed out waiting for ExitIdle to be called on child for address "addr2"
panic: test timed out after 40s
FAIL	google.golang.org/grpc/balancer/endpointsharding	40.120s
```

Goroutines at the Go test timeout (frames outside the package elided):

- Close blocked on child mutex:

```console
goroutine 10 [sync.Mutex.Lock]:
google.golang.org/grpc/balancer/endpointsharding.(*balancerWrapper).close(0xc000192b80)
	<wt>/f0a4f7e8/balancer/endpointsharding/endpointsharding.go:412 +0x3a
google.golang.org/grpc/balancer/endpointsharding.(*endpointSharding).Close-range1(...)
	<wt>/f0a4f7e8/balancer/endpointsharding/endpointsharding.go:226
google.golang.org/grpc/balancer/endpointsharding.(*endpointSharding).Close.(*EndpointMap[...]).All.func1(...)
google.golang.org/grpc/balancer/endpointsharding.(*endpointSharding).Close(0xc0000fefc0)
	<wt>/f0a4f7e8/balancer/endpointsharding/endpointsharding.go:225 +0x174
```

- update worker blocked on child mutex:

```console
goroutine 11 [sync.Mutex.Lock]:
google.golang.org/grpc/balancer/endpointsharding.(*balancerWrapper).updateClientConnState(0xc000192b80, {{{0x0, 0x0, 0x0}, {0xc000090fe0, 0x1, 0x1}, 0x0, 0x0}, {0x0, ...}})
	<wt>/f0a4f7e8/balancer/endpointsharding/endpointsharding.go:394 +0x56
google.golang.org/grpc/balancer/endpointsharding.(*endpointSharding).UpdateClientConnState(0xc0000fefc0, {{{0x0, 0x0, 0x0}, {0xc000051a80, 0x2, 0x2}, 0x0, 0x0}, {0x0, ...}})
	<wt>/f0a4f7e8/balancer/endpointsharding/endpointsharding.go:177 +0x9a5
google.golang.org/grpc/balancer/endpointsharding_test.s.TestEndpointShardingExitIdleWhileOtherChildUpdateBlocked.func3()
	<wt>/f0a4f7e8/balancer/endpointsharding/endpointsharding_ext_test.go:571 +0x9e
	<wt>/f0a4f7e8/balancer/endpointsharding/endpointsharding_ext_test.go:571 +0xee9
```

- injected idle worker (holds child mutex):

```console
goroutine 12 [chan receive]:
google.golang.org/grpc/balancer/endpointsharding_test.(*testChildBalancer).ExitIdle(0xc000051b80)
	<wt>/f0a4f7e8/balancer/endpointsharding/endpointsharding_ext_test.go:465 +0x9b
google.golang.org/grpc/balancer/endpointsharding.(*balancerWrapper).exitIdleSync(0xc000192b80)
	<wt>/f0a4f7e8/balancer/endpointsharding/endpointsharding.go:384 +0xcc
	<wt>/f0a4f7e8/balancer/endpointsharding/endpointsharding.go:372 +0x8b
```

Reading (f0a4f7e8): after the ExitIdle assertion's own deadline fires, `t.Fatal` -> `runtime.Goexit` runs the deferred `Close()`; `balancerWrapper.close` blocks acquiring `bw.mu`, which the injected idle-exit worker (in `balancerWrapper` idle-exit under `bw.mu`, inside the fake child's `ExitIdle`) still holds. Cleanup does not first wait, with a bound, for that worker, so the failure path never terminates on its own; the run ended only at `go test -timeout`.

### Log `c2_out/417e24dc.txt` — branch [evalon/grpc-go-en-417e24dc](https://github.com/kaitranntt-evals/grpc-go-endpointsharding-decouple-locking/tree/evalon/grpc-go-en-417e24dc), HEAD `9f74e9df`

```sh
# cwd: the branch worktree, after run_c2.py injected `<-make(chan struct{})` into the fake child's ExitIdle
go test ./balancer/endpointsharding -run Test/EndpointShardingExitIdleWhileChildUpdateBlocked$ -race -count=1 -timeout 40s -v
```

Key output (verbatim lines):

```console
endpointsharding_ext_test.go:459: Timeout waiting for ExitIdle to be called on the idle child while another child is blocked in UpdateClientConnState
panic: test timed out after 40s
FAIL	google.golang.org/grpc/balancer/endpointsharding	40.106s
```

Goroutines at the Go test timeout (frames outside the package elided):

- Close blocked on child mutex:

```console
goroutine 23 [sync.Mutex.Lock]:
google.golang.org/grpc/balancer/endpointsharding.(*balancerWrapper).close(0xc000218a80)
	<wt>/417e24dc/balancer/endpointsharding/endpointsharding.go:424 +0x3a
google.golang.org/grpc/balancer/endpointsharding.(*endpointSharding).Close-range1(...)
	<wt>/417e24dc/balancer/endpointsharding/endpointsharding.go:246
google.golang.org/grpc/balancer/endpointsharding.(*endpointSharding).Close.(*EndpointMap[...]).All.func1(...)
google.golang.org/grpc/balancer/endpointsharding.(*endpointSharding).Close(0xc000202fc0)
	<wt>/417e24dc/balancer/endpointsharding/endpointsharding.go:245 +0x172
```

- update worker blocked on child mutex:

```console
goroutine 24 [sync.Mutex.Lock]:
google.golang.org/grpc/balancer/endpointsharding.(*balancerWrapper).updateClientConnState(0xc000218a80, {{{0x0, 0x0, 0x0}, {0xc0001a2000, 0x1, 0x1}, 0x0, 0x0}, {0x0, ...}})
	<wt>/417e24dc/balancer/endpointsharding/endpointsharding.go:406 +0x56
google.golang.org/grpc/balancer/endpointsharding.(*endpointSharding).UpdateClientConnState(0xc000202fc0, {{{0x0, 0x0, 0x0}, {0xc000135b40, 0x2, 0x2}, 0x0, 0x0}, {0x0, ...}})
	<wt>/417e24dc/balancer/endpointsharding/endpointsharding.go:199 +0x985
google.golang.org/grpc/balancer/endpointsharding_test.s.TestEndpointShardingExitIdleWhileChildUpdateBlocked.func3()
	<wt>/417e24dc/balancer/endpointsharding/endpointsharding_ext_test.go:443 +0x21d
	<wt>/417e24dc/balancer/endpointsharding/endpointsharding_ext_test.go:442 +0xf0e
```

- injected idle worker (holds child mutex):

```console
goroutine 25 [chan receive]:
google.golang.org/grpc/balancer/endpointsharding_test.s.TestEndpointShardingExitIdleWhileChildUpdateBlocked.func2(0xc00006b750?)
	<wt>/417e24dc/balancer/endpointsharding/endpointsharding_ext_test.go:411 +0x3a
google.golang.org/grpc/balancer/endpointsharding.(*balancerWrapper).exitIdle(0xc000218a80)
	<wt>/417e24dc/balancer/endpointsharding/endpointsharding.go:397 +0xcc
	<wt>/417e24dc/balancer/endpointsharding/endpointsharding.go:69 +0xbf
```

Reading (417e24dc): after the ExitIdle assertion's own deadline fires, `t.Fatal` -> `runtime.Goexit` runs the deferred `Close()`; `balancerWrapper.close` blocks acquiring `bw.mu`, which the injected idle-exit worker (in `balancerWrapper` idle-exit under `bw.mu`, inside the fake child's `ExitIdle`) still holds. Cleanup does not first wait, with a bound, for that worker, so the failure path never terminates on its own; the run ended only at `go test -timeout`.

### Log `c2_out/26ec74be.txt` — branch [evalon/grpc-go-en-26ec74be](https://github.com/kaitranntt-evals/grpc-go-endpointsharding-decouple-locking/tree/evalon/grpc-go-en-26ec74be), HEAD `dc5b2168`

```sh
# cwd: the branch worktree, after run_c2.py injected `<-make(chan struct{})` into the fake child's ExitIdle
go test ./balancer/endpointsharding -run Test/EndpointShardingExitIdleWithBlockedChildUpdate$ -race -count=1 -timeout 40s -v
```

Key output (verbatim lines):

```console
endpointsharding_ext_test.go:516: Timed out waiting for ExitIdle on child A while child B was blocked
panic: test timed out after 40s
FAIL	google.golang.org/grpc/balancer/endpointsharding	40.116s
```

Goroutines at the Go test timeout (frames outside the package elided):

- Close blocked on child mutex:

```console
goroutine 10 [sync.Mutex.Lock]:
google.golang.org/grpc/balancer/endpointsharding.(*balancerWrapper).close(0xc000190c00)
	<wt>/26ec74be/balancer/endpointsharding/endpointsharding.go:420 +0x3a
google.golang.org/grpc/balancer/endpointsharding.(*endpointSharding).Close-range1(...)
	<wt>/26ec74be/balancer/endpointsharding/endpointsharding.go:232
google.golang.org/grpc/balancer/endpointsharding.(*endpointSharding).Close.(*EndpointMap[...]).All.func1(...)
google.golang.org/grpc/balancer/endpointsharding.(*endpointSharding).Close(0xc0000fefc0)
	<wt>/26ec74be/balancer/endpointsharding/endpointsharding.go:231 +0x174
```

- update worker blocked on child mutex:

```console
goroutine 19 [sync.Mutex.Lock]:
google.golang.org/grpc/balancer/endpointsharding.(*balancerWrapper).updateClientConnState(0xc000190c00, {{{0x0, 0x0, 0x0}, {0xc00011e040, 0x1, 0x1}, 0x0, 0x0}, {0x0, ...}})
	<wt>/26ec74be/balancer/endpointsharding/endpointsharding.go:412 +0x56
google.golang.org/grpc/balancer/endpointsharding.(*endpointSharding).UpdateClientConnState(0xc0000fefc0, {{{0x0, 0x0, 0x0}, {0xc000051a80, 0x2, 0x2}, 0x0, 0x0}, {0x0, ...}})
	<wt>/26ec74be/balancer/endpointsharding/endpointsharding.go:184 +0x625
google.golang.org/grpc/balancer/endpointsharding_test.s.TestEndpointShardingExitIdleWithBlockedChildUpdate.func2()
	<wt>/26ec74be/balancer/endpointsharding/endpointsharding_ext_test.go:499 +0xfa
	<wt>/26ec74be/balancer/endpointsharding/endpointsharding_ext_test.go:498 +0xf7d
```

- injected idle worker (holds child mutex):

```console
goroutine 20 [chan receive]:
google.golang.org/grpc/balancer/endpointsharding_test.(*testChild).ExitIdle(0xc0001f5080)
	<wt>/26ec74be/balancer/endpointsharding/endpointsharding_ext_test.go:453 +0x3a
google.golang.org/grpc/balancer/endpointsharding.(*balancerWrapper).exitIdle(0xc000190c00)
	<wt>/26ec74be/balancer/endpointsharding/endpointsharding.go:406 +0xba
	<wt>/26ec74be/balancer/endpointsharding/endpointsharding.go:397 +0x8b
```

Reading (26ec74be): after the ExitIdle assertion's own deadline fires, `t.Fatal` -> `runtime.Goexit` runs the deferred `Close()`; `balancerWrapper.close` blocks acquiring `bw.mu`, which the injected idle-exit worker (in `balancerWrapper` idle-exit under `bw.mu`, inside the fake child's `ExitIdle`) still holds. Cleanup does not first wait, with a bound, for that worker, so the failure path never terminates on its own; the run ended only at `go test -timeout`.

### Log `c2_out/bf5cd862.txt` — branch [evalon/grpc-go-en-bf5cd862](https://github.com/kaitranntt-evals/grpc-go-endpointsharding-decouple-locking/tree/evalon/grpc-go-en-bf5cd862), HEAD `a8efa13b`

```sh
# cwd: the branch worktree, after run_c2.py injected `<-make(chan struct{})` into the fake child's ExitIdle
go test ./balancer/endpointsharding -run Test/ChildExitIdleIndependentOfOtherChildUpdate$ -race -count=1 -timeout 40s -v
```

Key output (verbatim lines):

```console
endpointsharding_ext_test.go:434: Idle exit did not finish while another child's update was blocked: context deadline exceeded
panic: test timed out after 40s
FAIL	google.golang.org/grpc/balancer/endpointsharding	40.017s
```

Goroutines at the Go test timeout (frames outside the package elided):

- Close blocked on child mutex:

```console
goroutine 10 [sync.Mutex.Lock]:
google.golang.org/grpc/balancer/endpointsharding.(*balancerWrapper).close(0xc000210900)
	<wt>/bf5cd862/balancer/endpointsharding/endpointsharding.go:373 +0x3a
google.golang.org/grpc/balancer/endpointsharding.(*endpointSharding).Close-range1(...)
	<wt>/bf5cd862/balancer/endpointsharding/endpointsharding.go:219
google.golang.org/grpc/balancer/endpointsharding.(*endpointSharding).Close.(*EndpointMap[...]).All.func1(...)
google.golang.org/grpc/balancer/endpointsharding.(*endpointSharding).Close(0xc0000fefc0)
	<wt>/bf5cd862/balancer/endpointsharding/endpointsharding.go:218 +0x174
```

- update worker blocked on child mutex:

```console
goroutine 11 [sync.Mutex.Lock]:
google.golang.org/grpc/balancer/endpointsharding.(*balancerWrapper).updateClientConnState(0xc000210900, {{{0x0, 0x0, 0x0}, {0xc0001a2000, 0x1, 0x1}, 0x0, 0x0}, {0x0, ...}})
	<wt>/bf5cd862/balancer/endpointsharding/endpointsharding.go:364 +0x56
google.golang.org/grpc/balancer/endpointsharding.(*endpointSharding).UpdateClientConnState(0xc0000fefc0, {{{0x0, 0x0, 0x0}, {0xc000051a00, 0x2, 0x2}, 0x0, 0x0}, {0x0, ...}})
	<wt>/bf5cd862/balancer/endpointsharding/endpointsharding.go:166 +0xa05
google.golang.org/grpc/balancer/endpointsharding_test.s.TestChildExitIdleIndependentOfOtherChildUpdate.func4()
	<wt>/bf5cd862/balancer/endpointsharding/endpointsharding_ext_test.go:426 +0xa2
	<wt>/bf5cd862/balancer/endpointsharding/endpointsharding_ext_test.go:426 +0xf85
```

- injected idle worker (holds child mutex):

```console
goroutine 12 [chan receive]:
google.golang.org/grpc/balancer/endpointsharding_test.s.TestChildExitIdleIndependentOfOtherChildUpdate.func3(0xc0000ff200)
	<wt>/bf5cd862/balancer/endpointsharding/endpointsharding_ext_test.go:396 +0xe5
google.golang.org/grpc/balancer/endpointsharding.(*balancerWrapper).exitIdle(0xc000210900)
	<wt>/bf5cd862/balancer/endpointsharding/endpointsharding.go:355 +0xba
	<wt>/bf5cd862/balancer/endpointsharding/endpointsharding.go:348 +0x8b
```

Reading (bf5cd862): after the ExitIdle assertion's own deadline fires, `t.Fatal` -> `runtime.Goexit` runs the deferred `Close()`; `balancerWrapper.close` blocks acquiring `bw.mu`, which the injected idle-exit worker (in `balancerWrapper` idle-exit under `bw.mu`, inside the fake child's `ExitIdle`) still holds. Cleanup does not first wait, with a bound, for that worker, so the failure path never terminates on its own; the run ended only at `go test -timeout`.

### Log `c2_out/f563f1eb.txt` — branch [evalon/grpc-go-en-f563f1eb](https://github.com/kaitranntt-evals/grpc-go-endpointsharding-decouple-locking/tree/evalon/grpc-go-en-f563f1eb), HEAD `5d4c8e28`

```sh
# cwd: the branch worktree, after run_c2.py injected `<-make(chan struct{})` into the fake child's ExitIdle
go test ./balancer/endpointsharding -run Test/ExitIdleDuringOtherChildUpdate$ -race -count=1 -timeout 40s -v
```

Key output (verbatim lines):

```console
concurrency_test.go:125: ExitIdle() reached child <nil>, error context deadline exceeded; want idle while the other update is blocked
panic: test timed out after 40s
FAIL	google.golang.org/grpc/balancer/endpointsharding	40.079s
```

Goroutines at the Go test timeout (frames outside the package elided):

- Close blocked on child mutex:

```console
goroutine 10 [sync.Mutex.Lock]:
google.golang.org/grpc/balancer/endpointsharding.(*balancerWrapper).close(0xc000190d00)
	<wt>/f563f1eb/balancer/endpointsharding/endpointsharding.go:369 +0x3a
google.golang.org/grpc/balancer/endpointsharding.(*endpointSharding).Close-range1(...)
	<wt>/f563f1eb/balancer/endpointsharding/endpointsharding.go:216
google.golang.org/grpc/balancer/endpointsharding.(*endpointSharding).Close.(*EndpointMap[...]).All.func1(...)
google.golang.org/grpc/balancer/endpointsharding.(*endpointSharding).Close(0xc0000fefc0)
	<wt>/f563f1eb/balancer/endpointsharding/endpointsharding.go:215 +0x174
```

- injected idle worker (holds child mutex):

```console
goroutine 12 [chan receive]:
google.golang.org/grpc/balancer/endpointsharding_test.s.TestExitIdleDuringOtherChildUpdate.func2(0xc0000ff200)
	<wt>/f563f1eb/balancer/endpointsharding/concurrency_test.go:89 +0x4c
google.golang.org/grpc/balancer/endpointsharding.(*balancerWrapper).exitIdle(0xc000190d00)
	<wt>/f563f1eb/balancer/endpointsharding/endpointsharding.go:352 +0xba
	<wt>/f563f1eb/balancer/endpointsharding/endpointsharding.go:345 +0x8b
```

Reading (f563f1eb): after the ExitIdle assertion's own deadline fires, `t.Fatal` -> `runtime.Goexit` runs the deferred `Close()`; `balancerWrapper.close` blocks acquiring `bw.mu`, which the injected idle-exit worker (in `balancerWrapper` idle-exit under `bw.mu`, inside the fake child's `ExitIdle`) still holds. Cleanup does not first wait, with a bound, for that worker, so the failure path never terminates on its own; the run ended only at `go test -timeout`.

### Log `c2_out/1b022fbb.txt` — branch [evalon/grpc-go-en-1b022fbb](https://github.com/kaitranntt-evals/grpc-go-endpointsharding-decouple-locking/tree/evalon/grpc-go-en-1b022fbb), HEAD `c408b76c`

```sh
# cwd: the branch worktree, after run_c2.py injected `<-make(chan struct{})` into the fake child's ExitIdle
go test ./balancer/endpointsharding -run Test/ChildExitIdleDuringUpdate -race -count=1 -timeout 40s -v
```

Key output (verbatim lines):

```console
endpointsharding_concurrency_test.go:142: Idle exit waited for an unrelated child's update
panic: test timed out after 40s
FAIL	google.golang.org/grpc/balancer/endpointsharding	40.033s
```

Goroutines at the Go test timeout (frames outside the package elided):

- parent test goroutine (waiting in t.Run for the subtest goroutine shown next):

```console
goroutine 10 [chan receive]:
google.golang.org/grpc/balancer/endpointsharding_test.s.TestChildExitIdleDuringUpdate({{}}, 0xc0000bec40)
	<wt>/1b022fbb/balancer/endpointsharding/endpointsharding_concurrency_test.go:74 +0x11f
```

- Close blocked on child mutex:

```console
goroutine 11 [sync.Mutex.Lock]:
google.golang.org/grpc/balancer/endpointsharding.(*balancerWrapper).close(0xc000112a80)
	<wt>/1b022fbb/balancer/endpointsharding/endpointsharding.go:376 +0x3a
google.golang.org/grpc/balancer/endpointsharding.(*endpointSharding).Close-range1(...)
	<wt>/1b022fbb/balancer/endpointsharding/endpointsharding.go:210
google.golang.org/grpc/balancer/endpointsharding.(*endpointSharding).Close.(*EndpointMap[...]).All.func1(...)
google.golang.org/grpc/balancer/endpointsharding.(*endpointSharding).Close(0xc0000fefc0)
	<wt>/1b022fbb/balancer/endpointsharding/endpointsharding.go:209 +0x174
```

- update worker blocked on child mutex:

```console
goroutine 12 [sync.Mutex.Lock]:
google.golang.org/grpc/balancer/endpointsharding.(*balancerWrapper).updateClientConnState(0xc000112a80, {{{0x0, 0x0, 0x0}, {0xc000090f40, 0x1, 0x1}, 0x0, 0x0}, {0x0, ...}})
	<wt>/1b022fbb/balancer/endpointsharding/endpointsharding.go:367 +0x56
google.golang.org/grpc/balancer/endpointsharding.(*endpointSharding).UpdateClientConnState(0xc0000fefc0, {{{0x0, 0x0, 0x0}, {0xc000051a40, 0x2, 0x2}, 0x0, 0x0}, {0x0, ...}})
	<wt>/1b022fbb/balancer/endpointsharding/endpointsharding.go:162 +0xa05
google.golang.org/grpc/balancer/endpointsharding_test.s.TestChildExitIdleDuringUpdate.func1.4()
	<wt>/1b022fbb/balancer/endpointsharding/endpointsharding_concurrency_test.go:127 +0x9e
	<wt>/1b022fbb/balancer/endpointsharding/endpointsharding_concurrency_test.go:127 +0xc89
```

- injected idle worker (holds child mutex):

```console
goroutine 13 [chan receive]:
google.golang.org/grpc/balancer/endpointsharding_test.s.TestChildExitIdleDuringUpdate.func1.3(0xc0000ff0e0)
	<wt>/1b022fbb/balancer/endpointsharding/endpointsharding_concurrency_test.go:100 +0x3f
google.golang.org/grpc/balancer/endpointsharding.(*balancerWrapper).exitIdle(0xc000112a80)
	<wt>/1b022fbb/balancer/endpointsharding/endpointsharding.go:358 +0xba
	<wt>/1b022fbb/balancer/endpointsharding/endpointsharding.go:351 +0x8b
```

Reading (1b022fbb): after the ExitIdle assertion's own deadline fires, `t.Fatal` -> `runtime.Goexit` runs the deferred `Close()`; `balancerWrapper.close` blocks acquiring `bw.mu`, which the injected idle-exit worker (in `balancerWrapper` idle-exit under `bw.mu`, inside the fake child's `ExitIdle`) still holds. Cleanup does not first wait, with a bound, for that worker, so the failure path never terminates on its own; the run ended only at `go test -timeout`.

### Log `c2_out/ae479324.txt` — branch [evalon/grpc-go-en-ae479324](https://github.com/kaitranntt-evals/grpc-go-endpointsharding-decouple-locking/tree/evalon/grpc-go-en-ae479324), HEAD `99797683`

```sh
# cwd: the branch worktree, after run_c2.py injected `<-make(chan struct{})` into the fake child's ExitIdle
go test ./balancer/endpointsharding -run Test/ChildExitIdleDuringOtherChildUpdate$ -race -count=1 -timeout 40s -v
```

Key output (verbatim lines):

```console
endpointsharding_concurrency_test.go:158: Timed out waiting for other child's idle exit: context deadline exceeded
panic: test timed out after 40s
FAIL	google.golang.org/grpc/balancer/endpointsharding	40.115s
```

Goroutines at the Go test timeout (frames outside the package elided):

- Close blocked on child mutex:

```console
goroutine 10 [sync.Mutex.Lock]:
google.golang.org/grpc/balancer/endpointsharding.(*balancerWrapper).close(0xc000192980)
	<wt>/ae479324/balancer/endpointsharding/endpointsharding.go:373 +0x3a
google.golang.org/grpc/balancer/endpointsharding.(*endpointSharding).Close-range1(...)
	<wt>/ae479324/balancer/endpointsharding/endpointsharding.go:224
google.golang.org/grpc/balancer/endpointsharding.(*endpointSharding).Close.(*EndpointMap[...]).All.func1(...)
google.golang.org/grpc/balancer/endpointsharding.(*endpointSharding).Close(0xc0000fefc0)
	<wt>/ae479324/balancer/endpointsharding/endpointsharding.go:223 +0x174
```

- injected idle worker (holds child mutex):

```console
goroutine 12 [chan receive]:
google.golang.org/grpc/balancer/endpointsharding_test.s.TestChildExitIdleDuringOtherChildUpdate.func3(0xc0000ff0e0)
	<wt>/ae479324/balancer/endpointsharding/endpointsharding_concurrency_test.go:124 +0x3f
google.golang.org/grpc/balancer/endpointsharding.(*balancerWrapper).exitIdle(0xc000192980)
	<wt>/ae479324/balancer/endpointsharding/endpointsharding.go:362 +0xba
	<wt>/ae479324/balancer/endpointsharding/endpointsharding.go:355 +0x8b
```

Reading (ae479324): after the ExitIdle assertion's own deadline fires, `t.Fatal` -> `runtime.Goexit` runs the deferred `Close()`; `balancerWrapper.close` blocks acquiring `bw.mu`, which the injected idle-exit worker (in `balancerWrapper` idle-exit under `bw.mu`, inside the fake child's `ExitIdle`) still holds. Cleanup does not first wait, with a bound, for that worker, so the failure path never terminates on its own; the run ended only at `go test -timeout`.

### Log `c2_out/71865894.txt` — branch [evalon/grpc-go-en-71865894](https://github.com/kaitranntt-evals/grpc-go-endpointsharding-decouple-locking/tree/evalon/grpc-go-en-71865894), HEAD `e8aadd64`

```sh
# cwd: the branch worktree, after run_c2.py injected `<-make(chan struct{})` into the fake child's ExitIdle
go test ./balancer/endpointsharding -run Test/ChildExitIdleDuringOtherChildUpdate$ -race -count=1 -timeout 40s -v
```

Key output (verbatim lines):

```console
endpointsharding_concurrency_test.go:172: idle child did not reconnect while the other child's update was blocked
panic: test timed out after 40s
FAIL	google.golang.org/grpc/balancer/endpointsharding	40.116s
```

Goroutines at the Go test timeout (frames outside the package elided):

- Close blocked on child mutex:

```console
goroutine 10 [sync.Mutex.Lock]:
google.golang.org/grpc/balancer/endpointsharding.(*balancerWrapper).close(0xc000210880)
	<wt>/71865894/balancer/endpointsharding/endpointsharding.go:376 +0x3a
google.golang.org/grpc/balancer/endpointsharding.(*endpointSharding).Close-range1(...)
	<wt>/71865894/balancer/endpointsharding/endpointsharding.go:222
google.golang.org/grpc/balancer/endpointsharding.(*endpointSharding).Close.(*EndpointMap[...]).All.func1(...)
google.golang.org/grpc/balancer/endpointsharding.(*endpointSharding).Close(0xc0000fefc0)
	<wt>/71865894/balancer/endpointsharding/endpointsharding.go:221 +0x174
```

- update worker blocked on child mutex:

```console
goroutine 11 [sync.Mutex.Lock]:
google.golang.org/grpc/balancer/endpointsharding.(*balancerWrapper).updateClientConnState(0xc000210900, {{{0x0, 0x0, 0x0}, {0xc000318000, 0x1, 0x1}, 0x0, 0x0}, {0x0, ...}})
	<wt>/71865894/balancer/endpointsharding/endpointsharding.go:367 +0x56
google.golang.org/grpc/balancer/endpointsharding.(*endpointSharding).UpdateClientConnState(0xc0000fefc0, {{{0x0, 0x0, 0x0}, {0xc0000519c0, 0x2, 0x2}, 0x0, 0x0}, {0x0, ...}})
	<wt>/71865894/balancer/endpointsharding/endpointsharding.go:169 +0xa05
google.golang.org/grpc/balancer/endpointsharding_test.s.TestChildExitIdleDuringOtherChildUpdate.func3()
	<wt>/71865894/balancer/endpointsharding/endpointsharding_concurrency_test.go:157 +0x9e
	<wt>/71865894/balancer/endpointsharding/endpointsharding_concurrency_test.go:157 +0x9c5
```

- injected idle worker (holds child mutex):

```console
goroutine 12 [chan receive]:
google.golang.org/grpc/balancer/endpointsharding_test.s.TestChildExitIdleDuringOtherChildUpdate.func2.3()
	<wt>/71865894/balancer/endpointsharding/endpointsharding_concurrency_test.go:126 +0x7b
google.golang.org/grpc/balancer/endpointsharding_test.(*callbackBalancer).ExitIdle(0xc000274e70)
	<wt>/71865894/balancer/endpointsharding/endpointsharding_concurrency_test.go:70 +0x3b
google.golang.org/grpc/balancer/endpointsharding.(*balancerWrapper).exitIdle(0xc000210880)
	<wt>/71865894/balancer/endpointsharding/endpointsharding.go:358 +0xba
	<wt>/71865894/balancer/endpointsharding/endpointsharding.go:351 +0x8b
```

Reading (71865894): after the ExitIdle assertion's own deadline fires, `t.Fatal` -> `runtime.Goexit` runs the deferred `Close()`; `balancerWrapper.close` blocks acquiring `bw.mu`, which the injected idle-exit worker (in `balancerWrapper` idle-exit under `bw.mu`, inside the fake child's `ExitIdle`) still holds. Cleanup does not first wait, with a bound, for that worker, so the failure path never terminates on its own; the run ended only at `go test -timeout`.

### Log `c2_out/1eca0068.txt` — branch [evalon/grpc-go-en-1eca0068](https://github.com/kaitranntt-evals/grpc-go-endpointsharding-decouple-locking/tree/evalon/grpc-go-en-1eca0068), HEAD `6f9d30dd`

```sh
# cwd: the branch worktree, after run_c2.py injected `<-make(chan struct{})` into the fake child's ExitIdle
go test ./balancer/endpointsharding -run Test/EndpointShardingChildExitIdleDuringUpdate$ -race -count=1 -timeout 40s -v
```

Key output (verbatim lines):

```console
endpointsharding_concurrency_test.go:168: Timed out waiting for callback: context deadline exceeded
endpointsharding_concurrency_test.go:162: Timed out waiting for callback: context deadline exceeded
panic: test timed out after 40s
FAIL	google.golang.org/grpc/balancer/endpointsharding	40.115s
```

Goroutines at the Go test timeout (frames outside the package elided):

- Close blocked on child mutex:

```console
goroutine 23 [sync.Mutex.Lock]:
google.golang.org/grpc/balancer/endpointsharding.(*balancerWrapper).close(0xc00021ea80)
	<wt>/1eca0068/balancer/endpointsharding/endpointsharding.go:375 +0x3a
google.golang.org/grpc/balancer/endpointsharding.(*endpointSharding).Close-range1(...)
	<wt>/1eca0068/balancer/endpointsharding/endpointsharding.go:221
google.golang.org/grpc/balancer/endpointsharding.(*endpointSharding).Close.(*EndpointMap[...]).All.func1(...)
google.golang.org/grpc/balancer/endpointsharding.(*endpointSharding).Close(0xc00020afc0)
	<wt>/1eca0068/balancer/endpointsharding/endpointsharding.go:220 +0x174
```

- update worker blocked on child mutex:

```console
goroutine 24 [sync.Mutex.Lock]:
google.golang.org/grpc/balancer/endpointsharding.(*balancerWrapper).updateClientConnState(0xc00021eb00, {{{0x0, 0x0, 0x0}, {0xc00031e000, 0x1, 0x1}, 0x0, 0x0}, {0x0, ...}})
	<wt>/1eca0068/balancer/endpointsharding/endpointsharding.go:366 +0x56
google.golang.org/grpc/balancer/endpointsharding.(*endpointSharding).UpdateClientConnState(0xc00020afc0, {{{0x0, 0x0, 0x0}, {0xc0001bd9c0, 0x2, 0x2}, 0x0, 0x0}, {0x0, ...}})
	<wt>/1eca0068/balancer/endpointsharding/endpointsharding.go:168 +0xa05
google.golang.org/grpc/balancer/endpointsharding_test.s.TestEndpointShardingChildExitIdleDuringUpdate.func3()
	<wt>/1eca0068/balancer/endpointsharding/endpointsharding_concurrency_test.go:156 +0x13d
	<wt>/1eca0068/balancer/endpointsharding/endpointsharding_concurrency_test.go:154 +0x94b
```

- injected idle worker (holds child mutex):

```console
goroutine 25 [chan receive]:
google.golang.org/grpc/balancer/endpointsharding_test.s.TestEndpointShardingChildExitIdleDuringUpdate.func2.2()
	<wt>/1eca0068/balancer/endpointsharding/endpointsharding_concurrency_test.go:132 +0x5d
google.golang.org/grpc/balancer/endpointsharding_test.(*callbackBalancer).ExitIdle(0xc0001bdac0)
	<wt>/1eca0068/balancer/endpointsharding/endpointsharding_concurrency_test.go:70 +0x54
google.golang.org/grpc/balancer/endpointsharding.(*balancerWrapper).exitIdle(0xc00021ea80)
	<wt>/1eca0068/balancer/endpointsharding/endpointsharding.go:358 +0xba
	<wt>/1eca0068/balancer/endpointsharding/endpointsharding.go:351 +0x8b
```

Reading (1eca0068): after the ExitIdle assertion's own deadline fires, `t.Fatal` -> `runtime.Goexit` runs the deferred `Close()`; `balancerWrapper.close` blocks acquiring `bw.mu`, which the injected idle-exit worker (in `balancerWrapper` idle-exit under `bw.mu`, inside the fake child's `ExitIdle`) still holds. Cleanup does not first wait, with a bound, for that worker, so the failure path never terminates on its own; the run ended only at `go test -timeout`.

### Log `c2_out/dae0ce93.txt` — branch [evalon/grpc-go-en-dae0ce93](https://github.com/kaitranntt-evals/grpc-go-endpointsharding-decouple-locking/tree/evalon/grpc-go-en-dae0ce93), HEAD `e9994edd`

```sh
# cwd: the branch worktree, after run_c2.py injected `<-make(chan struct{})` into the fake child's ExitIdle
go test ./balancer/endpointsharding -run Test/ChildExitIdleDuringOtherChildUpdate$ -race -count=1 -timeout 40s -v
```

Key output (verbatim lines):

```console
endpointsharding_concurrency_test.go:166: ExitIdle() reached child <nil>, err: context deadline exceeded; want independent child before releasing blocked update
endpointsharding_concurrency_test.go:157: Timed out waiting for configuration update to finish
panic: test timed out after 40s
FAIL	google.golang.org/grpc/balancer/endpointsharding	40.117s
```

Goroutines at the Go test timeout (frames outside the package elided):

- Close blocked on child mutex:

```console
goroutine 10 [sync.Mutex.Lock]:
google.golang.org/grpc/balancer/endpointsharding.(*balancerWrapper).close(0xc000190980)
	<wt>/dae0ce93/balancer/endpointsharding/endpointsharding.go:375 +0x3a
google.golang.org/grpc/balancer/endpointsharding.(*endpointSharding).Close-range1(...)
	<wt>/dae0ce93/balancer/endpointsharding/endpointsharding.go:220
google.golang.org/grpc/balancer/endpointsharding.(*endpointSharding).Close.(*EndpointMap[...]).All.func1(...)
google.golang.org/grpc/balancer/endpointsharding.(*endpointSharding).Close(0xc0000fefc0)
	<wt>/dae0ce93/balancer/endpointsharding/endpointsharding.go:219 +0x174
```

- injected idle worker (holds child mutex):

```console
goroutine 12 [chan receive]:
google.golang.org/grpc/balancer/endpointsharding_test.s.TestChildExitIdleDuringOtherChildUpdate.func1.2()
	<wt>/dae0ce93/balancer/endpointsharding/endpointsharding_concurrency_test.go:124 +0x5a
google.golang.org/grpc/balancer/endpointsharding_test.(*callbackBalancer).ExitIdle(0xc0001f4ed0)
	<wt>/dae0ce93/balancer/endpointsharding/endpointsharding_concurrency_test.go:77 +0x3b
google.golang.org/grpc/balancer/endpointsharding.(*balancerWrapper).exitIdle(0xc000190980)
	<wt>/dae0ce93/balancer/endpointsharding/endpointsharding.go:357 +0xba
	<wt>/dae0ce93/balancer/endpointsharding/endpointsharding.go:350 +0x8b
```

Reading (dae0ce93): after the ExitIdle assertion's own deadline fires, `t.Fatal` -> `runtime.Goexit` runs the deferred `Close()`; `balancerWrapper.close` blocks acquiring `bw.mu`, which the injected idle-exit worker (in `balancerWrapper` idle-exit under `bw.mu`, inside the fake child's `ExitIdle`) still holds. Cleanup does not first wait, with a bound, for that worker, so the failure path never terminates on its own; the run ended only at `go test -timeout`.

### Log `c2_out/3d4a2ba9.txt` — branch [evalon/grpc-go-en-3d4a2ba9](https://github.com/kaitranntt-evals/grpc-go-endpointsharding-decouple-locking/tree/evalon/grpc-go-en-3d4a2ba9), HEAD `88e0ae89`

```sh
# cwd: the branch worktree, after run_c2.py injected `<-make(chan struct{})` into the fake child's ExitIdle
go test ./balancer/endpointsharding -run Test/ChildExitIdleDuringOtherChildUpdate -race -count=1 -timeout 120s -v
```

Key output (verbatim lines):

```console
endpointsharding_concurrency_test.go:151: Timed out waiting for event
endpointsharding_concurrency_test.go:131: Timed out waiting for event
grpctest.go:45: Leaked goroutine: goroutine 12 [sync.Mutex.Lock]:
grpctest.go:45: Leaked goroutine: goroutine 13 [chan receive]:
grpctest.go:45: Leaked goroutine: goroutine 19 [sync.Mutex.Lock]:
grpctest.go:45: Leaked goroutine: goroutine 20 [chan receive]:
--- FAIL: Test (50.16s)
FAIL	google.golang.org/grpc/balancer/endpointsharding	50.177s
```

Reading: on this branch the changed progress test's cleanup is *bounded* — `defer func(){ release(); awaitEvent(t, done); b.Close() }()` uses `awaitEvent`, which has a 10s (`defaultTestTimeout`) `time.After`; when the configuration worker cannot finish (its `bw.mu` is held by the injected idle worker) the deferred `awaitEvent` calls `t.Fatal`, `runtime.Goexit` skips `b.Close()`, and the run terminates on its own (2 subtests x (10s assertion + 10s cleanup) ≈ 50s) with the leak checker reporting the stuck update+idle workers. So *this* test does not leave teardown blocked. The 40s log below is the same command with `-timeout 40s`: the go timeout fired while the second subtest was still inside that bounded 10s wait, which is why it looks like a hang — it is not one. The branch verdict rests on the third log (`TestChildCallsSerialized`), whose cleanup does reach `b.Close()`.

### Log `c2_out/3d4a2ba9_40s.txt` — branch [evalon/grpc-go-en-3d4a2ba9](https://github.com/kaitranntt-evals/grpc-go-endpointsharding-decouple-locking/tree/evalon/grpc-go-en-3d4a2ba9), HEAD `88e0ae89`

```sh
# cwd: the branch worktree, after run_c2.py injected `<-make(chan struct{})` into the fake child's ExitIdle
go test ./balancer/endpointsharding -run Test/ChildExitIdleDuringOtherChildUpdate -race -count=1 -timeout 40s -v
```

Key output (verbatim lines):

```console
endpointsharding_concurrency_test.go:151: Timed out waiting for event
endpointsharding_concurrency_test.go:131: Timed out waiting for event
panic: test timed out after 40s
FAIL	google.golang.org/grpc/balancer/endpointsharding	40.050s
```

Goroutines at the Go test timeout (frames outside the package elided):

- parent test goroutine (waiting in t.Run for the subtest goroutine shown next):

```console
goroutine 23 [chan receive]:
google.golang.org/grpc/balancer/endpointsharding_test.s.TestChildExitIdleDuringOtherChildUpdate({{}}, 0xc000103500)
	<wt>/3d4a2ba9/balancer/endpointsharding/endpointsharding_concurrency_test.go:82 +0xec
```

- test cleanup awaitEvent:

```console
goroutine 27 [select]:
google.golang.org/grpc/balancer/endpointsharding_test.awaitEvent[...](0xc000262000?, 0xc0001125b0)
	<wt>/3d4a2ba9/balancer/endpointsharding/endpointsharding_concurrency_test.go:64 +0xda
google.golang.org/grpc/balancer/endpointsharding_test.s.TestChildExitIdleDuringOtherChildUpdate.func1.4()
	<wt>/3d4a2ba9/balancer/endpointsharding/endpointsharding_concurrency_test.go:131 +0x65
google.golang.org/grpc/balancer/endpointsharding_test.awaitEvent[...](0xc000262000?, 0xc000260150)
	<wt>/3d4a2ba9/balancer/endpointsharding/endpointsharding_concurrency_test.go:68 +0x145
google.golang.org/grpc/balancer/endpointsharding_test.s.TestChildExitIdleDuringOtherChildUpdate.func1(0xc000262000)
```

- update worker blocked on child mutex:

```console
goroutine 25 [sync.Mutex.Lock]:
google.golang.org/grpc/balancer/endpointsharding.(*balancerWrapper).updateClientConnState(0xc000196b00, {{{0x0, 0x0, 0x0}, {0xc000090020, 0x1, 0x1}, 0x0, 0x0}, {0x0, ...}})
	<wt>/3d4a2ba9/balancer/endpointsharding/endpointsharding.go:358 +0x56
google.golang.org/grpc/balancer/endpointsharding.(*endpointSharding).UpdateClientConnState(0xc000182fc0, {{{0x0, 0x0, 0x0}, {0xc0001359c0, 0x2, 0x2}, 0x0, 0x0}, {0x0, ...}})
	<wt>/3d4a2ba9/balancer/endpointsharding/endpointsharding.go:167 +0xa05
google.golang.org/grpc/balancer/endpointsharding_test.s.TestChildExitIdleDuringOtherChildUpdate.func1.5()
	<wt>/3d4a2ba9/balancer/endpointsharding/endpointsharding_concurrency_test.go:136 +0x13d
	<wt>/3d4a2ba9/balancer/endpointsharding/endpointsharding_concurrency_test.go:134 +0xa6b
```

- injected idle worker (holds child mutex):

```console
goroutine 26 [chan receive]:
google.golang.org/grpc/balancer/endpointsharding_test.s.TestChildExitIdleDuringOtherChildUpdate.func1.3.2()
	<wt>/3d4a2ba9/balancer/endpointsharding/endpointsharding_concurrency_test.go:111 +0x5a
google.golang.org/grpc/balancer/endpointsharding_test.(*callbackBalancer).ExitIdle(0xc0001fb080)
	<wt>/3d4a2ba9/balancer/endpointsharding/endpointsharding_concurrency_test.go:53 +0x3b
google.golang.org/grpc/balancer/endpointsharding.(*balancerWrapper).exitIdle(0xc000196b00)
	<wt>/3d4a2ba9/balancer/endpointsharding/endpointsharding.go:352 +0xba
	<wt>/3d4a2ba9/balancer/endpointsharding/endpointsharding.go:164 +0x8b
```

### Log `c2_out/3d4a2ba9-serialized.txt` — branch [evalon/grpc-go-en-3d4a2ba9](https://github.com/kaitranntt-evals/grpc-go-endpointsharding-decouple-locking/tree/evalon/grpc-go-en-3d4a2ba9), HEAD `88e0ae89`

```sh
# cwd: the branch worktree, after run_c2.py injected `<-make(chan struct{})` into the fake child's ExitIdle
go test ./balancer/endpointsharding -run Test/ChildCallsSerialized/ResolverError$ -race -count=1 -timeout 40s -v
```

Key output (verbatim lines):

```console
endpointsharding_concurrency_test.go:268: Timed out waiting for event
panic: test timed out after 40s
FAIL	google.golang.org/grpc/balancer/endpointsharding	40.093s
```

Reading: `TestChildCallsSerialized` (also added by this branch) runs the contended method (`ResolverError`) in a worker and joins it (`awaitEvent(t, done)`) but never joins the idle-exit worker queued by `child.ExitIdle()`. After the line-268 assertion fails, the deferred cleanup's `release(); awaitEvent(t, done)` succeeds (the resolver worker finishes) and then `b.Close()` blocks on `bw.mu`, which the injected idle-exit worker still holds; the run only ends at the go timeout. This is the "joins configuration workers but not idle-exit workers before Close" pattern the claim names for this branch.

Goroutines at the Go test timeout (frames outside the package elided):

- parent test goroutine (waiting in t.Run for the subtest goroutine shown next):

```console
goroutine 10 [chan receive]:
google.golang.org/grpc/balancer/endpointsharding_test.s.TestChildCallsSerialized({{}}, 0xc0000bec40)
	<wt>/3d4a2ba9/balancer/endpointsharding/endpointsharding_concurrency_test.go:184 +0x10b
```

- Close blocked on child mutex:

```console
goroutine 11 [sync.Mutex.Lock]:
google.golang.org/grpc/balancer/endpointsharding.(*balancerWrapper).close(0xc000210c00)
	<wt>/3d4a2ba9/balancer/endpointsharding/endpointsharding.go:370 +0x3a
google.golang.org/grpc/balancer/endpointsharding.(*endpointSharding).Close-range1(...)
	<wt>/3d4a2ba9/balancer/endpointsharding/endpointsharding.go:220
google.golang.org/grpc/balancer/endpointsharding.(*endpointSharding).Close.(*EndpointMap[...]).All.func1(...)
google.golang.org/grpc/balancer/endpointsharding.(*endpointSharding).Close(0xc0000fefc0)
	<wt>/3d4a2ba9/balancer/endpointsharding/endpointsharding.go:219 +0x174
```

- injected idle worker (holds child mutex):

```console
goroutine 13 [chan receive]:
google.golang.org/grpc/balancer/endpointsharding_test.s.TestChildCallsSerialized.func1.4.3()
	<wt>/3d4a2ba9/balancer/endpointsharding/endpointsharding_concurrency_test.go:213 +0x3a
google.golang.org/grpc/balancer/endpointsharding_test.(*callbackBalancer).ExitIdle(0xc000275140)
	<wt>/3d4a2ba9/balancer/endpointsharding/endpointsharding_concurrency_test.go:53 +0x3b
google.golang.org/grpc/balancer/endpointsharding.(*balancerWrapper).exitIdle(0xc000210c00)
	<wt>/3d4a2ba9/balancer/endpointsharding/endpointsharding.go:352 +0xba
	<wt>/3d4a2ba9/balancer/endpointsharding/endpointsharding.go:164 +0x8b
```

### Log `c2_out/f93e7e52.txt` — branch [evalon/grpc-go-en-f93e7e52](https://github.com/kaitranntt-evals/grpc-go-endpointsharding-decouple-locking/tree/evalon/grpc-go-en-f93e7e52), HEAD `8b5d90da`

```sh
# cwd: the branch worktree, after run_c2.py injected `<-make(chan struct{})` into the fake child's ExitIdle
go test ./balancer/endpointsharding -run Test/ExitIdleDuringOtherChildUpdate -race -count=1 -timeout 40s -v
```

Key output (verbatim lines):

```console
concurrency_test.go:179: ExitIdle did not reach the idle child while the other child's update was blocked
panic: test timed out after 40s
FAIL	google.golang.org/grpc/balancer/endpointsharding	40.042s
```

Goroutines at the Go test timeout (frames outside the package elided):

- parent test goroutine (waiting in t.Run for the subtest goroutine shown next):

```console
goroutine 24 [chan receive]:
google.golang.org/grpc/balancer/endpointsharding_test.s.TestExitIdleDuringOtherChildUpdate({{}}, 0xc0001036c0)
	<wt>/f93e7e52/balancer/endpointsharding/concurrency_test.go:103 +0x11f
```

- Close blocked on child mutex:

```console
goroutine 25 [sync.Mutex.Lock]:
google.golang.org/grpc/balancer/endpointsharding.(*balancerWrapper).close(0xc000198d80)
	<wt>/f93e7e52/balancer/endpointsharding/endpointsharding.go:377 +0x3a
google.golang.org/grpc/balancer/endpointsharding.(*endpointSharding).Close-range1(...)
	<wt>/f93e7e52/balancer/endpointsharding/endpointsharding.go:221
google.golang.org/grpc/balancer/endpointsharding.(*endpointSharding).Close.(*EndpointMap[...]).All.func1(...)
google.golang.org/grpc/balancer/endpointsharding.(*endpointSharding).Close(0xc000182fc0)
	<wt>/f93e7e52/balancer/endpointsharding/endpointsharding.go:220 +0x174
```

- injected idle worker (holds child mutex):

```console
goroutine 27 [chan receive]:
google.golang.org/grpc/balancer/endpointsharding_test.s.TestExitIdleDuringOtherChildUpdate.func1.2.2()
	<wt>/f93e7e52/balancer/endpointsharding/concurrency_test.go:130 +0x5a
google.golang.org/grpc/balancer/endpointsharding_test.(*callbackBalancer).ExitIdle(0xc0001fd290)
	<wt>/f93e7e52/balancer/endpointsharding/concurrency_test.go:81 +0x3b
google.golang.org/grpc/balancer/endpointsharding.(*balancerWrapper).exitIdle(0xc000198d80)
	<wt>/f93e7e52/balancer/endpointsharding/endpointsharding.go:359 +0xba
	<wt>/f93e7e52/balancer/endpointsharding/endpointsharding.go:352 +0x8b
```


## C3

Claim: a changed synchronization comment forbids holding the child-operation mutex during synchronous parent
callbacks even though the implementation requires that lock ownership.

Target: [evalon/grpc-go-en-212e3582](https://github.com/kaitranntt-evals/grpc-go-endpointsharding-decouple-locking/tree/evalon/grpc-go-en-212e3582) (HEAD `6eec86ae`).

All changed comment lines in the production file:

```sh
cd $WT/212e3582 && git diff bf9e7cd3 -- balancer/endpointsharding/endpointsharding.go | grep -n '^[-+].*//'
```

```console
9:-	// Balancer exposes only the ExitIdler interface of the child LB policy.
10:-	// Other methods of the child policy are called only by endpointsharding.
12:+	// bw is the wrapper of the child LB policy. Only its ExitIdle method is
13:+	// exposed to consumers; other methods of the child policy are called only
14:+	// by endpointsharding.
18:-// ExitIdler provides access to only the ExitIdle method of the child balancer.
20:-	// ExitIdle instructs the LB policy to reconnect to backends / exit the
21:-	// IDLE state, if appropriate and possible.  Note that SubConns that enter
22:-	// the IDLE state will not reconnect until SubConn.Connect is called.
24:+// ExitIdle instructs the child LB policy to reconnect to backends / exit the
25:+// IDLE state, if appropriate and possible. Note that SubConns that enter the
26:+// IDLE state will not reconnect until SubConn.Connect is called. The call is
27:+// performed asynchronously and is serialized with all other calls into the
28:+// same child.
41:-	// childMu synchronizes calls to any single child. It must be held for all
42:-	// calls into a child. To avoid deadlocks, do not acquire childMu while
43:-	// holding mu.
45:+	// children is only replaced by UpdateClientConnState. Calls into a
46:+	// specific child are serialized by that child's balancerWrapper.mu; there
47:+	// is no lock shared across children, so an ExitIdle request for one child
48:+	// does not have to wait for an unrelated child's update to complete.
56:-	// deadlock. To avoid deadlocks, do not acquire childMu while holding mu.
57:+	// deadlock. To avoid deadlocks, do not acquire a balancerWrapper.mu while
58:+	// holding mu.
145:-	// child contains the wrapped balancer. Access its methods only through
146:-	// methods on balancerWrapper to ensure proper synchronization
152:+	// mu serializes all calls into the child balancer. It must not be held
153:+	// while holding es.mu. Synchronous callbacks from the child (UpdateState)
154:+	// may happen while mu is held, so those callbacks must not acquire mu.
156:+	// child contains the wrapped balancer. It is built lazily on the first
157:+	// updateClientConnState call so that synchronous state updates during
158:+	// construction are handled while mu is held. Access to child and isClosed
159:+	// is guarded by mu.
174:-// avoid deadlocks due to synchronous balancer state updates.
175:+// avoid deadlocks due to synchronous balancer state updates. Since the request
176:+// is serialized only with calls into this child, it is not delayed by updates
177:+// being delivered to other children.
189:-// updateClientConnStateLocked delivers the ClientConnState to the child
190:-// balancer. Callers must hold the child mutex of the parent endpointsharding
191:-// balancer.
193:+// exitIdle synchronously calls ExitIdle on the child balancer, if it has been
194:+// built and not yet closed.
203:+// updateClientConnState delivers the ClientConnState to the child balancer,
204:+// building the child first if this is the first update.
217:-// closeLocked closes the child balancer. Callers must hold the child mutext of
218:-// the parent endpointsharding balancer.
221:+// close closes the child balancer.
```

The only changed comment that speaks about the child-operation mutex (`balancerWrapper.mu`) and synchronous
parent callbacks is the one at diff lines 152-154: *"Synchronous callbacks from the child (UpdateState) may happen
while mu is held, so those callbacks must not acquire mu."* It states that `mu` **is** held during such callbacks and
forbids the *callback* from re-acquiring it; the two other changed lock comments (45-48, 57-58) only forbid taking a
`balancerWrapper.mu` while holding the parent `es.mu`. No changed comment forbids holding the child-operation mutex
during a synchronous parent callback.

Measured lock ownership (probe `verify/repro/c3_probe_test.go.txt`, in-package test; the fake child `TryLock`s
`bw.mu` and `bw.es.mu` from inside its `Build`, `UpdateClientConnState` and `ExitIdle`, then synchronously calls
`cc.UpdateState` and records whether it returned):

```sh
cd $WT/212e3582 && cp verify/repro/c3_probe_test.go.txt balancer/endpointsharding/zz_c3_probe_test.go && go test -v ./balancer/endpointsharding -run '^TestVerifyC3_' -race -count=1 ; rm balancer/endpointsharding/zz_c3_probe_test.go
```

```console
=== RUN   TestVerifyC3_ChildMutexHeldDuringSynchronousCallback
    balancer.go:193: testutils.BalancerClientConn: UpdateState({CONNECTING 0xc000051900})
    balancer.go:193: testutils.BalancerClientConn: UpdateState({CONNECTING 0xc000051980})
    zz_c3_probe_test.go:81: callback from construction          : bw.mu(child-operation mutex) held=true es.mu(parent mutex) held=false UpdateState returned=true
    zz_c3_probe_test.go:81: callback from UpdateClientConnState : bw.mu(child-operation mutex) held=true es.mu(parent mutex) held=false UpdateState returned=true
    zz_c3_probe_test.go:81: callback from ExitIdle              : bw.mu(child-operation mutex) held=true es.mu(parent mutex) held=false UpdateState returned=true
parent published state from synchronous child callback: CONNECTING
--- PASS: TestVerifyC3_ChildMutexHeldDuringSynchronousCallback (0.00s)
PASS
ok  	google.golang.org/grpc/balancer/endpointsharding	1.014s
```

Reading: the implementation holds `bw.mu` (the child-operation mutex) across every synchronous child->parent
`UpdateState` callback path (construction, configuration, idle exit), the parent mutex is not held, and the callback
returns. The changed comment describes exactly this ownership ("may happen while mu is held") rather than forbidding
it, so the comment and the measured lock ownership agree. Raw log: `~/verify_work/c3_probe_out.txt`.

## C4

Claim: `ChildState` does not provide consumers a directly callable `ExitIdle` that invokes the associated child's
idle-exit without accessing a separate balancer field. Twelve target branches, adjudicated independently.

Method, per branch (`verify/repro/run_c4.sh`): (1) copy the byte-exact fixture from the extracted archive to
`balancer/endpointsharding/eval_endpointsharding_test.go` and `cmp` it against the archive; (2) run the fixture's
`TestEval_ChildStateExitIdleCallback` (it reflects over `ChildState`'s fields, rejects any separate field whose type
exposes `ExitIdle`, then calls `ExitIdle` on the `ChildState` and waits for the instrumented child's `exitIdleCh`);
(3) run an *external-package* consumer probe (`verify/repro/c4_external_exitidle_test.go.txt`, package
`endpointsharding_test`) that takes `ChildStatesFromPicker(...)[0]` and calls `css[0].ExitIdle()` directly — no
reflection, no other field — and asserts the fake child's `ExitIdle` ran; (4) remove both copies and print
`git status --short`.

```sh
C4_WT=$WT FIXTURE=<extracted>/tests/eval_endpointsharding_test.go PROBE=$PWD/verify/repro/c4_external_exitidle_test.go.txt bash verify/repro/run_c4.sh
```

Key output (`~/verify_work/c4_output.txt`; the full per-branch block is shown for the first branch and is
line-for-line identical on the others apart from the branch header and the `ok` timing):

```console
=== branch evalon/grpc-go-en-2805a453 (1443e1fa)
fixture byte-identical to archive: yes
=== RUN   TestEval_ChildStateExitIdleCallback
    eval_endpointsharding_test.go:1683: Child received direct ExitIdle call successfully
--- PASS: TestEval_ChildStateExitIdleCallback (0.00s)
=== RUN   TestVerifyC4_ExternalConsumerDirectExitIdle
    zz_c4_external_exitidle_test.go:76: child ExitIdle ran via direct ChildState.ExitIdle()
--- PASS: TestVerifyC4_ExternalConsumerDirectExitIdle (0.00s)
PASS
ok  	google.golang.org/grpc/balancer/endpointsharding	1.015s
=== branch evalon/grpc-go-en-6ab5ed6f (3b16a93d)
fixture byte-identical to archive: yes
=== RUN   TestEval_ChildStateExitIdleCallback
=== RUN   TestVerifyC4_ExternalConsumerDirectExitIdle
ok  	google.golang.org/grpc/balancer/endpointsharding	1.014s
=== branch evalon/grpc-go-en-7935c7b4 (9a5f647b)
fixture byte-identical to archive: yes
=== RUN   TestEval_ChildStateExitIdleCallback
=== RUN   TestVerifyC4_ExternalConsumerDirectExitIdle
ok  	google.golang.org/grpc/balancer/endpointsharding	1.015s
=== branch evalon/grpc-go-en-28870ca2 (dd832f7a)
fixture byte-identical to archive: yes
=== RUN   TestEval_ChildStateExitIdleCallback
=== RUN   TestVerifyC4_ExternalConsumerDirectExitIdle
ok  	google.golang.org/grpc/balancer/endpointsharding	1.014s
=== branch evalon/grpc-go-en-349fa040 (d0419acd)
fixture byte-identical to archive: yes
=== RUN   TestEval_ChildStateExitIdleCallback
=== RUN   TestVerifyC4_ExternalConsumerDirectExitIdle
ok  	google.golang.org/grpc/balancer/endpointsharding	1.015s
=== branch evalon/grpc-go-en-212e3582 (6eec86ae)
fixture byte-identical to archive: yes
=== RUN   TestEval_ChildStateExitIdleCallback
=== RUN   TestVerifyC4_ExternalConsumerDirectExitIdle
ok  	google.golang.org/grpc/balancer/endpointsharding	1.016s
=== branch evalon/grpc-go-en-a7641ce6 (8d1ec410)
fixture byte-identical to archive: yes
=== RUN   TestEval_ChildStateExitIdleCallback
=== RUN   TestVerifyC4_ExternalConsumerDirectExitIdle
ok  	google.golang.org/grpc/balancer/endpointsharding	1.014s
=== branch evalon/grpc-go-en-83039173 (73a1e2c2)
fixture byte-identical to archive: yes
=== RUN   TestEval_ChildStateExitIdleCallback
=== RUN   TestVerifyC4_ExternalConsumerDirectExitIdle
ok  	google.golang.org/grpc/balancer/endpointsharding	1.015s
=== branch evalon/grpc-go-en-9849d323 (ffac0346)
fixture byte-identical to archive: yes
=== RUN   TestEval_ChildStateExitIdleCallback
=== RUN   TestVerifyC4_ExternalConsumerDirectExitIdle
ok  	google.golang.org/grpc/balancer/endpointsharding	1.015s
=== branch evalon/grpc-go-en-15d00ee8 (2678ec6f)
fixture byte-identical to archive: yes
=== RUN   TestEval_ChildStateExitIdleCallback
=== RUN   TestVerifyC4_ExternalConsumerDirectExitIdle
ok  	google.golang.org/grpc/balancer/endpointsharding	1.014s
=== branch evalon/grpc-go-en-bd3553ea (f1b96e61)
fixture byte-identical to archive: yes
=== RUN   TestEval_ChildStateExitIdleCallback
=== RUN   TestVerifyC4_ExternalConsumerDirectExitIdle
ok  	google.golang.org/grpc/balancer/endpointsharding	1.015s
=== branch evalon/grpc-go-en-fd6b3403 (4eb60b4b)
fixture byte-identical to archive: yes
=== RUN   TestEval_ChildStateExitIdleCallback
=== RUN   TestVerifyC4_ExternalConsumerDirectExitIdle
ok  	google.golang.org/grpc/balancer/endpointsharding	1.014s
```

```sh
grep -c '^--- PASS' ~/verify_work/c4_output.txt ; grep -c '^--- FAIL\|^FAIL' ~/verify_work/c4_output.txt ; grep -c 'leftover:' ~/verify_work/c4_output.txt
```

```console
24
0
0
```

Per-branch reading (identical evidence on each of the twelve): the fixture was byte-identical (`cmp`), the fixture's
reflective direct-call test passed (`Child received direct ExitIdle call successfully`), and an external-package
consumer invoked `ChildState.ExitIdle()` directly and the instrumented child's `ExitIdle` ran
(`child ExitIdle ran via direct ChildState.ExitIdle()`), with no separate balancer field accessed. On every branch
the claimed behaviour did not hold. Nothing was left behind in any worktree (no `leftover:` lines).

## C5

Claim (three parts) about the maintained tests on the audited solution branch
(`grpc-go-endpointsharding-decouple-locking-perfect`, HEAD `81201fc0`, primary checkout):
`TestSameChildMutualExclusion` in `balancer/endpointsharding/endpointsharding_test.go`.

Relevant test code (lines 404-471 on the audited branch):

```go
	releaseAndWait := func() bool {
		releaseOnce.Do(func() {
			close(updateHold)
		})
		select {
		case <-workerDone:
			return true
		case <-time.After(2 * time.Second):
			t.Error("worker goroutine timed out or deadlocked during release")
			return false
		}
	}
	defer func() {
		if releaseAndWait() {
			lb.Close()
		}
	}()
	...
	// 5. Invoke ExitIdle on the published handle while second update is held
	callDone := make(chan struct{})
	go func() {
		targetChildState.ExitIdle()
		close(callDone)
	}()
	...
	// 7. Release update and require ExitIdle finishes after release
	if !releaseAndWait() {
		t.Fatal("worker release timed out")
	}

	select {
	case <-exitDone:
		// Succeeded: finished after release
	case <-time.After(2 * time.Second):
		t.Fatal("ExitIdle never finished after update released")
	}

	<-callDone
```

and the production side (`endpointsharding.go` on the audited branch): `ChildState.ExitIdle` is set to
`func() { go epState.exitIdle() }` (line 267); `endpointState.exitIdle` and `endpointState.close` both take
`es.childMu` (lines 385-400). The initial configuration call is `lb.UpdateClientConnState(...)` at line 373 with no
surrounding goroutine, context, or timer.

Three fault injections (test-file-only patches under `verify/repro/`, each reverted with `git checkout` afterwards),
all run as:

```sh
cd <primary checkout> && git apply verify/repro/<patch> && go test ./balancer/endpointsharding -run 'Test/SameChildMutualExclusion$' -race -count=1 -timeout 20s -v ; git checkout -- balancer/endpointsharding/endpointsharding_test.go
```

Reading (f93e7e52): after the ExitIdle assertion's own deadline fires, `t.Fatal` -> `runtime.Goexit` runs the deferred `Close()`; `balancerWrapper.close` blocks acquiring `bw.mu`, which the injected idle-exit worker (in `balancerWrapper` idle-exit under `bw.mu`, inside the fake child's `ExitIdle`) still holds. Cleanup does not first wait, with a bound, for that worker, so the failure path never terminates on its own; the run ended only at `go test -timeout`.

### C5 worker_join — `<-callDone` after the idle-exit worker fails to finish

`c5_stall_after_done.patch`: the fake child's `ExitIdle` signals `doneExit` and then blocks forever, so the idle-exit
worker never finishes while the test still reaches step 7 successfully. Output (`~/verify_work/c5_workerjoin_out.txt`,
frames outside the package elided):

```console
=== RUN   Test
=== RUN   Test/SameChildMutualExclusion
panic: test timed out after 20s
	running tests:
		Test (20s)
		Test/SameChildMutualExclusion (20s)
goroutine 9 [sync.Mutex.Lock]:
google.golang.org/grpc/balancer/endpointsharding.(*endpointState).close(0xc000212c80)
google.golang.org/grpc/balancer/endpointsharding.(*endpointSharding).Close-range1(...)
google.golang.org/grpc/balancer/endpointsharding.(*endpointSharding).Close.(*EndpointMap[...]).All.func1(...)
google.golang.org/grpc/balancer/endpointsharding.(*endpointSharding).Close(0xc0000fefc0)
google.golang.org/grpc/balancer/endpointsharding.s.TestSameChildMutualExclusion.func3()
google.golang.org/grpc/balancer/endpointsharding.s.TestSameChildMutualExclusion({{}}, 0xc0000bea80)
goroutine 18 [chan receive]:
google.golang.org/grpc/balancer/endpointsharding.(*maintainedChild).ExitIdle(0xc000212d00)
google.golang.org/grpc/balancer/endpointsharding.(*endpointState).exitIdle(0xc000212c80)
FAIL	google.golang.org/grpc/balancer/endpointsharding	20.103s
```

Reading: no test-body failure was printed, i.e. steps 7 and the `<-callDone` receive completed — `ChildState.ExitIdle`
only spawns `go epState.exitIdle()` and returns, so `callDone` is closed immediately regardless of the worker. The
goroutine parked at the go-test timeout is the *deferred* `lb.Close()` (`func3`), not `<-callDone`. In the other
failure path (worker never signals `exitDone`) step 7 calls `t.Fatal` *before* `<-callDone` is reached, so the
receive is never executed. The described unbounded `callDone` wait could not be reproduced: this part did not hold.

### C5 teardown_with_idle_worker — deferred Close while the idle-exit worker holds `childMu`

`c5_stall_exitidle.patch`: the fake child's `ExitIdle` blocks forever at entry (never signals `doneExit`). Output
(`~/verify_work/c5_teardown_out.txt`):

```console
=== RUN   Test
=== RUN   Test/SameChildMutualExclusion
    endpointsharding_test.go:469: ExitIdle never finished after update released
panic: test timed out after 20s
	running tests:
		Test (20s)
		Test/SameChildMutualExclusion (20s)
goroutine 22 [sync.Mutex.Lock]:
google.golang.org/grpc/balancer/endpointsharding.(*endpointState).close(0xc000194c80)
google.golang.org/grpc/balancer/endpointsharding.(*endpointSharding).Close-range1(...)
google.golang.org/grpc/balancer/endpointsharding.(*endpointSharding).Close.(*EndpointMap[...]).All.func1(...)
google.golang.org/grpc/balancer/endpointsharding.(*endpointSharding).Close(0xc000182fc0)
google.golang.org/grpc/balancer/endpointsharding.s.TestSameChildMutualExclusion.func3()
google.golang.org/grpc/balancer/endpointsharding.s.TestSameChildMutualExclusion({{}}, 0xc000103340)
goroutine 26 [chan receive]:
google.golang.org/grpc/balancer/endpointsharding.(*maintainedChild).ExitIdle(0xc000194d00)
google.golang.org/grpc/balancer/endpointsharding.(*endpointState).exitIdle(0xc000194c80)
FAIL	google.golang.org/grpc/balancer/endpointsharding	20.104s
```

Reading: the step-7 deadline fires (`ExitIdle never finished after update released`, 2s), the deferred cleanup's
`releaseAndWait()` succeeds because it only joins the *update* worker (`workerDone`), and `lb.Close()` then blocks in
`endpointState.close` on `childMu`, which the idle-exit worker (goroutine 26, inside the child's `ExitIdle`) still
holds. Nothing in the cleanup joins or checks the idle-exit worker before `Close`; the test terminates only at the
go-test timeout. This part held.

### C5 initial_configuration_wait — synchronous initial `UpdateClientConnState` without a local deadline

`c5_stall_initial_config.patch`: the fake child blocks inside its first `UpdateClientConnState` (one-line change:
`shouldBlock: true`). Output (`~/verify_work/c5_initcfg_out.txt`):

```console
=== RUN   Test
=== RUN   Test/SameChildMutualExclusion
panic: test timed out after 20s
	running tests:
		Test (20s)
		Test/SameChildMutualExclusion (20s)
goroutine 9 [chan receive]:
google.golang.org/grpc/balancer/endpointsharding.(*maintainedChild).UpdateClientConnState(0xc000192d00, {{{0x0, 0x0, 0x0}, {0xc000090f40, 0x1, 0x1}, 0x0, 0x0}, {0x0, ...}})
google.golang.org/grpc/balancer/endpointsharding.(*endpointState).updateClientConnStateLocked(...)
google.golang.org/grpc/balancer/endpointsharding.(*endpointSharding).UpdateClientConnState(0xc0000fefc0, {{{0x0, 0x0, 0x0}, {0xc000090f00, 0x1, 0x1}, 0x0, 0x0}, {0x0, ...}})
google.golang.org/grpc/balancer/endpointsharding.s.TestSameChildMutualExclusion({{}}, 0xc0000bea80)
FAIL	google.golang.org/grpc/balancer/endpointsharding	20.042s
```

Reading: the test goroutine itself is parked inside the initial `lb.UpdateClientConnState` (test line 373); no test
message is printed and no local deadline path regains control — only the global go-test timeout ends the run. This
part held.

Impact reasoning (parts 2 and 3): a regression that stalls a child's `ExitIdle` or initial configuration turns the
maintained mutual-exclusion test into a package-level `go test -timeout` panic (7 minutes in
`.github/workflows/testing.yml`) instead of a readable assertion failure, and takes every other test in the package
down with it. Replay: the three patches above (each header carries the exact command); raw logs
`~/verify_work/c5_workerjoin_out.txt`, `c5_teardown_out.txt`, `c5_initcfg_out.txt`.

## C6

Claim (two parts): a construction-time Idle report loses its effective reconnect because the reconnect executes
before initial configuration supplies endpoints and is not retried afterwards.

Target: [evalon/grpc-go-en-ae479324](https://github.com/kaitranntt-evals/grpc-go-endpointsharding-decouple-locking/tree/evalon/grpc-go-en-ae479324) (HEAD `99797683`).

### C6 construction_configuration_gap — lock boundaries

`balancer/endpointsharding/endpointsharding.go` on the target, `UpdateClientConnState` (lines 158-171):

```go
			childBalancer = &balancerWrapper{
				childState: ChildState{Endpoint: endpoint},
				ClientConn: es.cc,
				es:         es,
			}
			childBalancer.childState.ExitIdle = childBalancer.ExitIdle
			// Build may report IDLE synchronously. Hold the child's mutex until
			// it is initialized so the resulting ExitIdle cannot run too early.
			childBalancer.mu.Lock()
			childBalancer.child = es.childBuilder(childBalancer, es.bOpts)
			childBalancer.mu.Unlock()
		}
		newChildren.Set(endpoint, childBalancer)
		if err := childBalancer.updateClientConnState(balancer.ClientConnState{
```

and the reconnect path (lines 346-362): `UpdateState` with `Idle && !DisableAutoReconnect` calls `bw.ExitIdle()`,
which is `go bw.exitIdle()`, and `exitIdle` does `bw.mu.Lock(); defer bw.mu.Unlock(); ... bw.child.ExitIdle()`.
`updateClientConnState` (line 367) takes `bw.mu` again. So the child mutex is released at line 168 and re-acquired
inside `updateClientConnState` — a window in which the queued `exitIdle` goroutine can win the lock and call the
child's `ExitIdle` before the child has received any endpoints. This part is established by the code and confirmed
by the observed early executions below.

### C6 construction_idle_delivery — observed ordering and no retry

Probe `verify/repro/c6_probe_test.go.txt` (external package): a child that reports Idle synchronously from `Build`,
records whether `ExitIdle` arrives before or after its first `UpdateClientConnState`, and counts a second `ExitIdle`
within 50ms after an early one as a retry; 300 iterations with auto-reconnect enabled.

```sh
cd $WT/ae479324 && cp verify/repro/c6_probe_test.go.txt balancer/endpointsharding/zz_c6_probe_test.go && go test ./balancer/endpointsharding -run TestC6ConstructionIdleReconnectOrdering -race -count=1 -v ; rm balancer/endpointsharding/zz_c6_probe_test.go
```

Captured run (`~/verify_work/c6_probe_out.txt`; the probe is written to fail when the ordering is observed):

```console
=== RUN   TestC6ConstructionIdleReconnectOrdering
    c6_probe_test.go:75: iterations=300 exitIdle-before-configuration(early)=2 exitIdle-after-configuration(late)=298 retried-after-early=0
    c6_probe_test.go:77: construction-time IDLE reconnect ran before endpoints were supplied in 2/300 iterations and was never retried afterward
--- FAIL: TestC6ConstructionIdleReconnectOrdering (0.12s)
FAIL
FAIL	google.golang.org/grpc/balancer/endpointsharding	0.130s
```

Three further invocations of the same command (summary lines only, appended to the same log):

```console
    c6_probe_test.go:75: iterations=300 exitIdle-before-configuration(early)=0 exitIdle-after-configuration(late)=300 retried-after-early=0
    c6_probe_test.go:75: iterations=300 exitIdle-before-configuration(early)=1 exitIdle-after-configuration(late)=299 retried-after-early=0
    c6_probe_test.go:75: iterations=300 exitIdle-before-configuration(early)=3 exitIdle-after-configuration(late)=297 retried-after-early=0
```

Caveat: the early ordering is a scheduling race and is rare (0-3 of 300 iterations per run in this sample; one run
saw none). Whenever it happened, `retried-after-early` was 0: the parent never re-issued `ExitIdle` after the
endpoints arrived. For contrast, the fixture's own construction test passes on this branch (it does not check this
ordering):

```sh
cd $WT/ae479324 && cp <extracted>/tests/eval_endpointsharding_test.go balancer/endpointsharding/ && go test ./balancer/endpointsharding -run '^TestEval_ConstructionIdleCallbackSafety$' -race -count=1 -v ; rm balancer/endpointsharding/eval_endpointsharding_test.go
```

```console
=== RUN   TestEval_ConstructionIdleCallbackSafety
--- PASS: TestEval_ConstructionIdleCallbackSafety (0.05s)
PASS
ok  	google.golang.org/grpc/balancer/endpointsharding	1.065s
```

Impact reasoning: with auto-reconnect (the default) a child that reports Idle from its constructor (e.g. pick_first
before it has addresses) can receive its one reconnect request before it has endpoints; that request is a no-op for
the child and is not repeated, so the child stays Idle until some later external event triggers another ExitIdle.
Rare per construction, but it is silent — no error, just a child that does not connect. Replay: probe command above;
raw logs `~/verify_work/c6_probe_out.txt`, `c6_eval_out.txt`.

## C7

Claim: the delivered ringhash changes introduce `balancer.ExitIdler` declarations that fail the repository's
configured Staticcheck SA1019 check.

Target: [evalon/grpc-go-en-212e3582](https://github.com/kaitranntt-evals/grpc-go-endpointsharding-decouple-locking/tree/evalon/grpc-go-en-212e3582) (HEAD `6eec86ae`).

The introduced declarations (`git diff bf9e7cd3 -- balancer/ringhash/ringhash.go` on the target; the merge base used
`endpointsharding.ExitIdler` at both places):

```console
-		var idleBalancer endpointsharding.ExitIdler
+		var idleBalancer balancer.ExitIdler
-	balancer endpointsharding.ExitIdler
+	balancer balancer.ExitIdler
```

`balancer/balancer.go` lines 374-376 on the target: `// Deprecated: All balancers must implement this interface. This
interface will be removed in a future release.` / `type ExitIdler interface {`. CI runs `./scripts/vet.sh -install &&
./scripts/vet.sh` (`.github/workflows/testing.yml` line 103); `vet.sh` runs `staticcheck -checks 'all'` and then
`noret_grep "(SA1019)" "${SC_OUT}" | not grep -Fv '<exclusion list>'` (lines 168-205) — any SA1019 line not matching
an exclusion fails the script. `balancer.ExitIdler is deprecated` matches none of the exclusion patterns (the list
contains `"google.golang.org/grpc`, `: grpc.`, `BalancerAttributes is deprecated:`, `balancer.ErrTransientFailure is
deprecated:`, ... but nothing for `ExitIdler`).

Replay script (`verify/repro/c7_staticcheck.sh`) installs the pinned tool from `test/tools/go.mod`, runs the same
invocation on `./balancer/ringhash/...`, and applies vet.sh's own exclusion list:

```sh
cd $WT/212e3582 && PATH=$PATH:$(go env GOPATH)/bin bash verify/repro/c7_staticcheck.sh
```

```console
== staticcheck -version: staticcheck 2026.1 (v0.7.0)
== pinned: 	honnef.co/go/tools v0.7.0
== raw SA1019 diagnostics in balancer/ringhash:
balancer/ringhash/ringhash.go:271:20: balancer.ExitIdler is deprecated: All balancers must implement this interface. This interface will be removed in a future release.  (SA1019)
balancer/ringhash/ringhash.go:403:11: balancer.ExitIdler is deprecated: All balancers must implement this interface. This interface will be removed in a future release.  (SA1019)
balancer/ringhash/ringhash_test.go:687:2: addr.BalancerAttributes is deprecated: when an Address is inside an Endpoint, this field should not be used, and it will eventually be removed entirely.  (SA1019)
balancer/ringhash/ringhash_test.go:687:28: addr.BalancerAttributes is deprecated: when an Address is inside an Endpoint, this field should not be used, and it will eventually be removed entirely.  (SA1019)
== SA1019 lines that survive scripts/vet.sh's exclusion filter (non-empty => vet.sh fails):
balancer/ringhash/ringhash.go:271:20: balancer.ExitIdler is deprecated: All balancers must implement this interface. This interface will be removed in a future release.  (SA1019)
balancer/ringhash/ringhash.go:403:11: balancer.ExitIdler is deprecated: All balancers must implement this interface. This interface will be removed in a future release.  (SA1019)
== exit of vet.sh stage (0 = would fail, 1 = clean): 0
```

Lines 271 and 403 of `balancer/ringhash/ringhash.go` on the target are exactly the two introduced declarations
(`var idleBalancer balancer.ExitIdler` and the `endpointState` field `balancer balancer.ExitIdler`). The
pre-existing `BalancerAttributes` diagnostics are excluded by the list; the two `ExitIdler` ones are not. A whole-repo
`staticcheck -checks all ./...` on the same worktree (`~/verify_work/c7_sc_full.txt`) contains the same two
`ringhash.go` lines.

Impact reasoning: the branch's `vet.sh` CI job fails on every run (deterministic, not flaky), blocking merge until the
declarations are changed (e.g. back to `endpointsharding.ExitIdler`, or an `ExitIdler` exclusion is added to
`vet.sh`). Replay: `verify/repro/c7_staticcheck.sh`; raw log `~/verify_work/c7_repro_out.txt`.

## C8

Claim (two parts): a child-state callback carries a stale publication decision across batch entry and publishes a
partial parent state while a configuration batch is in progress.

Target: [evalon/grpc-go-en-f1dbdc2b](https://github.com/kaitranntt-evals/grpc-go-endpointsharding-decouple-locking/tree/evalon/grpc-go-en-f1dbdc2b) (HEAD `c2a842d5`).

### C8 stale_publication_decision — code

`balancer/endpointsharding/endpointsharding.go` on the target:

```go
254:func (es *endpointSharding) updateState() {
255:	if es.inhibitChildUpdates.Load() {
256:		return
257:	}
258:	var readyPickers, connectingPickers, idlePickers, transientFailurePickers []balancer.Picker
259:
260:	es.mu.Lock()
261:	defer es.mu.Unlock()
```

`inhibitChildUpdates` is an `atomic.Bool` (line 128) written by `UpdateClientConnState`/`ResolverError` outside
`es.mu` (`Store(true)` at lines 160 and 223, `Store(false)` in their deferred functions). The check at line 255 is
outside `es.mu`, is not repeated after the lock is taken, and nothing sequences it with the batch-entry `Store(true)`,
so a callback that read `false` keeps publishing after a batch has begun. This part is established by the code and
demonstrated below.

### C8 publication_during_batch — observed partial publication

Instrumentation (`verify/repro/c8_hook.patch`, 7 added lines, no behaviour change unless the hook is set):

```diff
+// c8PauseAfterInhibitCheck is audit instrumentation (INJECT(C8)): called after
+// the inhibition check and before es.mu is taken.
+var c8PauseAfterInhibitCheck func()
+
 func (es *endpointSharding) updateState() {
 	if es.inhibitChildUpdates.Load() {
 		return
 	}
+	if c8PauseAfterInhibitCheck != nil {
+		c8PauseAfterInhibitCheck()
+	}
```

Probe `verify/repro/c8_probe_test.go.txt` (in-package): two children `a`,`b`; after the v1 batch has been published,
child `a`'s ClientConn re-reports READY and the hook pauses that callback after the inhibit check; a v2 batch is
started with child `a`'s update held (child `b` has already reported v2); the paused callback is resumed while the
batch is still in progress and the parent's next publication is inspected.

```sh
cd $WT/f1dbdc2b && git apply verify/repro/c8_hook.patch && cp verify/repro/c8_probe_test.go.txt balancer/endpointsharding/zz_c8_probe_test.go && go test -race -count=1 -run 'Test/C8' -v ./balancer/endpointsharding/ ; rm balancer/endpointsharding/zz_c8_probe_test.go && git checkout -- balancer/endpointsharding/endpointsharding.go
```

Output (`~/verify_work/c8_out.txt`; the probe is written to fail when it observes the partial publication):

```console
=== RUN   TestC8
=== RUN   TestC8/StaleInhibitDecisionPublishesPartialBatch
    c8_probe_test.go:109: after v1 batch: parent published READY children=[a:v1,b:v1]
    c8_probe_test.go:135: callback paused after inhibit check (decision: publish)
    c8_probe_test.go:146: batch v2 in progress: child "a" update held before reporting v2, other child already at v2
    c8_probe_test.go:161: PARENT PUBLISHED WHILE BATCH IN PROGRESS (child "a" still held): READY children=[a:v1-rereport,b:v2]
    c8_probe_test.go:163: partial batch state published: a:v1-rereport,b:v2
    c8_probe_test.go:186: final consolidated publication: READY children=[a:v2,b:v2]
--- FAIL: TestC8 (0.00s)
    --- FAIL: TestC8/StaleInhibitDecisionPublishesPartialBatch (0.00s)
FAIL
FAIL	google.golang.org/grpc/balancer/endpointsharding	0.014s
```

Reading: before the callback resumed, nothing had been published during the batch (the probe's `t.Fatalf("parent
published during batch before callback resumed")` did not fire). Resuming the callback produced a parent publication
with mixed generations (`a:v1-rereport,b:v2`) while child `a`'s v2 update was still held, followed later by the
consolidated `a:v2,b:v2` state. Both parts held.

Impact reasoning: any child state report that races with the start of a resolver update can publish a picker whose
child list mixes old and new configuration for a moment, which is exactly what `inhibitChildUpdates` exists to
prevent; observable to pickers as an extra, inconsistent update during ordinary resolver churn. Replay: patch + probe
command above; raw log `~/verify_work/c8_out.txt`.

## C9

Claim (three parts): the maintained endpointsharding test additions lack assertions for contended same-child
exclusion, retained-handle safety after removal/Close, and continuation after a child configuration error.

Target: [evalon/grpc-go-en-417e24dc](https://github.com/kaitranntt-evals/grpc-go-endpointsharding-decouple-locking/tree/evalon/grpc-go-en-417e24dc) (HEAD `9f74e9df`).

```sh
cd $WT/417e24dc && git diff bf9e7cd3 --stat && git diff bf9e7cd3 -- 'balancer/endpointsharding/*_test.go' | grep '^+func\|^+++ '
```

```console
 balancer/endpointsharding/endpointsharding.go      | 160 ++++++++-----
 .../endpointsharding/endpointsharding_ext_test.go  | 260 +++++++++++++++++++++
 balancer/ringhash/picker.go                        |   2 +-
 balancer/ringhash/picker_test.go                   |   2 +-
 balancer/ringhash/ringhash.go                      |  16 +-
 5 files changed, 370 insertions(+), 70 deletions(-)
+++ b/balancer/endpointsharding/endpointsharding_ext_test.go
+func newRecordingClientConn(t *testing.T) *recordingClientConn {
+func (cc *recordingClientConn) UpdateState(state balancer.State) {
+func (s) TestEndpointShardingExitIdleWhileChildUpdateBlocked(t *testing.T) {
+func (s) TestEndpointShardingSynchronousChildStateUpdates(t *testing.T) {
+func (s) TestEndpointShardingSingleUpdateForMultipleEndpoints(t *testing.T) {
```

The tests named in the claim's "where to look" do not exist on this branch (they belong to the audited solution
branch, not to this target):

```sh
grep -rn 'TestSameChildMutualExclusion\|TestClosedStateGuardCoverage\|TestBatchUpdateChildErrorConsolidation' balancer/ ; echo exit=$?
```

```console
exit=1
```

Every assertion added by the branch:

```sh
git diff bf9e7cd3 -- balancer/endpointsharding/endpointsharding_ext_test.go | grep '^+.*\(t\.Fatal\|t\.Error\)'
```

```console
+		t.Fatalf("UpdateClientConnState() returned error: %v", err)
+			t.Fatalf("Got %d child states from picker, want 1", len(childStates))
+		t.Fatal("Timeout waiting for a picker update")
+		t.Fatal("Timeout waiting for a child balancer to block in UpdateClientConnState")
+		t.Fatal("Timeout waiting for ExitIdle to be called on the idle child while another child is blocked in UpdateClientConnState")
+		t.Fatalf("UpdateClientConnState() returned %v before the blocked child was released", err)
+			t.Fatalf("UpdateClientConnState() returned error: %v", err)
+		t.Fatal("Timeout waiting for UpdateClientConnState to return")
+		t.Fatalf("UpdateClientConnState() returned error: %v", err)
+			t.Fatalf("Timeout waiting for ExitIdle call %d of %d on the child balancers", i+1, wantExitIdleCalls)
+		t.Fatalf("Got more than %d ExitIdle calls on the child balancers", wantExitIdleCalls)
+			t.Fatalf("UpdateClientConnState() #%d returned error: %v", i+1, err)
+			t.Fatalf("Timeout waiting for a state update after UpdateClientConnState() #%d", i+1)
+			t.Fatalf("Got aggregated connectivity state %v, want %v", got, want)
+			t.Fatalf("Got %d child states from picker, want %d", got, want)
+				t.Fatalf("Child %v has connectivity state %v, want %v", cs.Endpoint, got, want)
+			t.Fatalf("Got unexpected additional state update %v after UpdateClientConnState() #%d", state, i+1)
```

Supporting greps over the same diff: every stub child `UpdateClientConnState` in the added tests ends in `return nil`
(no injected configuration error, no `errors.New`); the only `Close` calls are `defer bal.Close()` (three, one per
test) with no use of a `ChildState` handle afterwards; there is no test that issues two operations against the same
child concurrently and checks overlap. Demonstration that the added tests run and pass as written
(`~/verify_work/c9_run_out.txt`):

```sh
cd $WT/417e24dc && go test ./balancer/endpointsharding -run 'Test/EndpointSharding(ExitIdleWhileChildUpdateBlocked|SynchronousChildStateUpdates|SingleUpdateForMultipleEndpoints)$' -race -count=1 -v
```

```console
=== RUN   Test
=== RUN   Test/EndpointShardingExitIdleWhileChildUpdateBlocked
=== RUN   Test/EndpointShardingSingleUpdateForMultipleEndpoints
=== RUN   Test/EndpointShardingSynchronousChildStateUpdates
--- PASS: Test (0.03s)
    --- PASS: Test/EndpointShardingExitIdleWhileChildUpdateBlocked (0.00s)
    --- PASS: Test/EndpointShardingSingleUpdateForMultipleEndpoints (0.02s)
    --- PASS: Test/EndpointShardingSynchronousChildStateUpdates (0.01s)
PASS
ok  	google.golang.org/grpc/balancer/endpointsharding	1.048s
```

Per part: *same_child_exclusion* — no added assertion forces two contending operations on one child or checks
non-overlap (the `ExitIdleWhileChildUpdateBlocked` test checks progress across *different* children); held.
*retained_handle_safety* — no added test calls a retained `ChildState.ExitIdle` after endpoint removal or after
`Close`; held. *configuration_error_continuation* — no added stub returns an error from `UpdateClientConnState` and
no assertion checks that other children were still configured; held.

Impact reasoning: the branch changes the per-child locking (`endpointsharding.go`, 160 lines) without tests for the
three behaviours most likely to regress under that change (same-child serialization, use of a handle after its child
is gone, error handling mid-batch); regressions there would ship silently. Next: add tests modelled on the audited
branch's `TestSameChildMutualExclusion`, `TestClosedStateGuardCoverage`, `TestBatchUpdateChildErrorConsolidation`.
Replay: `cd $WT/417e24dc && bash verify/repro/c9_coverage_check.sh` (runs the inspection commands above and the demonstration run); raw logs `~/verify_work/c9_grep_out.txt`, `c9_run_out.txt` (local scratch, not committed).

## C10

Claim: parent `ExitIdle` publishes intermediate parent state updates for synchronous child reports instead of
consolidating them into one final aggregate.

Target: [evalon/grpc-go-en-f563f1eb](https://github.com/kaitranntt-evals/grpc-go-endpointsharding-decouple-locking/tree/evalon/grpc-go-en-f563f1eb) (HEAD `5d4c8e28`).

`balancer/endpointsharding/endpointsharding.go` on the target, lines 220-224:

```go
func (es *endpointSharding) ExitIdle() {
	for _, bw := range es.children.Load().All() {
		bw.exitIdle()
	}
}
```

Unlike `UpdateClientConnState` and `ResolverError` (which set `es.inhibitChildUpdates = true` at lines 128/195 and
publish once in a deferred function), `ExitIdle` does not inhibit child updates, so each child's synchronous
`UpdateState` from inside its `ExitIdle` goes straight to `updateStateLocked` and the parent ClientConn.

Probe `verify/repro/c10_probe_test.go.txt` (external package; three stub children that report CONNECTING synchronously
from inside `ExitIdle`; a recording ClientConn counts parent publications):

```sh
cd $WT/f563f1eb && cp verify/repro/c10_probe_test.go.txt balancer/endpointsharding/zz_c10_probe_test.go && go test -race -count=1 -run 'Test/C10' -v ./balancer/endpointsharding/ ; rm balancer/endpointsharding/zz_c10_probe_test.go
```

Output (`~/verify_work/c10_out.txt`; the probe is written to fail when more than one publication is observed):

```console
=== RUN   Test
=== RUN   Test/C10ParentExitIdlePublications
    c10_probe_test.go:50: parent publications during UpdateClientConnState (batch): 1
    c10_probe_test.go:55: child ExitIdle calls: 3
    c10_probe_test.go:56: parent publications during a single parent ExitIdle(): 3
    c10_probe_test.go:62:   publication 1: aggregate=CONNECTING children=[a=CONNECTING b=IDLE c=IDLE]
    c10_probe_test.go:62:   publication 2: aggregate=CONNECTING children=[a=CONNECTING b=CONNECTING c=IDLE]
    c10_probe_test.go:62:   publication 3: aggregate=CONNECTING children=[c=CONNECTING a=CONNECTING b=CONNECTING]
    c10_probe_test.go:65: parent ExitIdle() produced 3 parent state publications, want 1 consolidated update
--- FAIL: Test (0.00s)
    --- FAIL: Test/C10ParentExitIdlePublications (0.00s)
FAIL
FAIL	google.golang.org/grpc/balancer/endpointsharding	0.014s
```

Reading: the configuration batch with the same three children produced exactly one publication, while one parent
`ExitIdle()` produced three, each reflecting one more child's report (`a` only, then `a,b`, then all three). The
claimed behaviour held.

Impact reasoning: every parent `ExitIdle` (e.g. the channel leaving idle) fans out N picker updates to the parent
ClientConn for N children instead of one, each with a partially-updated child list — extra picker churn and
transiently inconsistent child views on an everyday path. Next: wrap the loop in `ExitIdle` with the same
inhibit/publish-once pattern used by `UpdateClientConnState`. Replay: probe command above; raw log
`~/verify_work/c10_out.txt`.
