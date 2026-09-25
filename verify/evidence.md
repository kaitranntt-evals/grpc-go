# Verify evidence: grpc-go-endpointsharding-decouple-locking (run v-8e973b58)

Observations only. Toolchain: go1.25.7 linux/amd64. Claim branches are fetched from https://github.com/kaitranntt-evals/grpc-go-endpointsharding-decouple-locking (`evalon/grpc-go-en-<id>`), each in its own worktree.

## C1

**Method (instrumentation, not branch behavior).** None of the branch fixtures block a production worker forever on their own: every fixture barrier (`release()`, `unblock()`) is released by cleanup before `Close`. The claim needs a worker that stays blocked after release. I simulated that with a production stall: `verify/repro/c1_stall/verify_stall.go.txt` is copied into `balancer/endpointsharding/`, and `run_c1.sh` wraps each branch's `bw.child.UpdateClientConnState(ccs)` / `bw.child.ExitIdle()` so that the Nth call on one child does `select {}` while the per-child lock is held (`VERIFY_STALL=update:N`, `exitidle:N` = stall after the child call, `preexitidle:N` = stall before it). Each added test was run per mode with `-timeout 40s` (150–200s for re-runs). A goroutine dump that shows the test goroutine under `runtime.Goexit` (a failure path), blocked in `(*endpointSharding).Close` / `(*balancerWrapper).close`, or in an unbounded `<-ch` join, is a failure-path cleanup that waits with no deadline. `update:1` stalls usually block the synchronous initial update in the test body itself. Those are not cleanup hangs and are excluded.

```sh
verify/repro/c1_stall/run_c1.sh ~/wt/<id> <Subtest> <mode>
```

Baseline: without `VERIFY_STALL`, every instrumented branch's package passes (`ok google.golang.org/grpc/balancer/endpointsharding` for all 16).

Representative failure-path cleanup hang per branch (test goroutine has `runtime.Goexit`; the binary is killed by `panic: test timed out`):

| branch | subtest.mode | assertion/timeout that reached cleanup | cleanup blocked in |
|---|---|---|---|
| 043bcc2f | `EndpointShardingExitIdleNotBlockedByOtherChild.update:2` | ["endpointsharding_ext_test.go:469: Timed out waiting for the blocked child's update to start"] | `Close` sync.Mutex.Lock at ['endpointsharding_ext_test.go:469 +0xe25'] |
| 05ce9086 | `EndpointShardingExitIdleDuringBlockedChildUpdate.update:2` | ["endpointsharding_ext_test.go:499: Timeout waiting for child B's UpdateClientConnState to block"] | `close` sync.Mutex.Lock at ['endpointsharding_ext_test.go:499 +0xcc5'] |
| 4039a10c | `ExitIdleDuringAnotherChildUpdate.update:2` | ['concurrency_test.go:156: Timed out waiting for child update to block', 'concurrency_test.go:154: Timed out waiting for configuration update to finish'] | `close` sync.Mutex.Lock at ['concurrency_test.go:81 +0xec', 'concurrency_test.go:154 +0x65', 'concurrency_test.go:81 +0xec'] |
| 450d28f3 | `EndpointShardingSynchronousIdle.update:1` | ['endpointsharding_concurrency_test.go:239: Timed out waiting for initial update to complete'] | `7()` chan receive at ['endpointsharding_concurrency_test.go:227 +0x2e', 'endpointsharding_concurrency_test.go:65 +0xef', 'endpointsharding_concurrency_test.go:239 +0x7a9'] |
| 5256b684 | `EndpointShardingExitIdleWithBlockedChildUpdate.update:2` | ['endpointsharding_ext_test.go:525: Timed out waiting for UpdateClientConnState to return'] | `close` sync.Mutex.Lock at ['endpointsharding_ext_test.go:525 +0xd1b'] |
| 58559ac1 | `ChildCallsAreSerialized.update:2` | ['endpointsharding_concurrency_test.go:284: timed out waiting for blocked operation completion'] | `close` sync.Mutex.Lock at ['endpointsharding_concurrency_test.go:74 +0xd9', 'endpointsharding_concurrency_test.go:284 +0x9b0'] |
| 5a311302 | `ExitIdleWhileAnotherChildUpdateIsBlocked.update:2` | ['endpointsharding_test.go:287: Timed out waiting for UpdateClientConnState to return'] | `close` sync.Mutex.Lock at ['endpointsharding_test.go:287 +0xcb4'] |
| 7442a345 | `ChildCallsSerialized.update:2` | ['endpointsharding_concurrency_test.go:368: UpdateClientConnState did not finish after releasing ExitIdle'] | `5()` chan receive at ['endpointsharding_concurrency_test.go:357 +0x2e', 'endpointsharding_concurrency_test.go:368 +0x7e7'] |
| b0806305 | `EndpointShardingExitIdleNotBlockedByUnrelatedChildUpdate.update:2` | ['endpointsharding_ext_test.go:500: Timed out waiting for the child to receive the second update'] | `close` sync.Mutex.Lock at ['endpointsharding_ext_test.go:500 +0x985'] |
| b24bb544 | `ExitIdleWhileOtherChildUpdating.update:2` | ['endpointsharding_concurrency_test.go:141: Timed out waiting for configuration update completion'] | `close` sync.Mutex.Lock at ['endpointsharding_concurrency_test.go:64 +0xd9', 'endpointsharding_concurrency_test.go:141 +0xa6a'] |
| d5658fb5 | `ChildCallsSerializedWithExitIdle.update:2` | ['concurrency_test.go:382: timeout waiting for parent operation completion', 'concurrency_test.go:373: timeout waiting for parent operation completion'] | `close` sync.Mutex.Lock at ['concurrency_test.go:119 +0xd9', 'concurrency_test.go:373 +0x45', 'concurrency_test.go:119 +0xd9'] |
| f5189b90 | `ChildCallsSerialized.update:2` | ['endpointsharding_concurrency_test.go:252: Timed out waiting for child operation: context deadline exceeded'] | `close` sync.Mutex.Lock at ['endpointsharding_concurrency_test.go:79 +0xf2', 'endpointsharding_concurrency_test.go:252 +0xbc5'] |
| fd65907a | `EndpointShardingExitIdleWhileChildUpdateBlocked.update:2` | ['endpointsharding_ext_test.go:545: Timeout waiting for UpdateClientConnState to return'] | `Close` sync.Mutex.Lock at ['endpointsharding_ext_test.go:545 +0xe18'] |
| 00f252d4 | (pre-call ExitIdle stall) | ChildCallsSerialized preexitidle:1 -> `endpointsharding_concurrency_test.go:358: Timed out waiting for queued ExitIdle`, then t.Cleanup at :337 blocks in `(*balancerWrapper).close` (goexit=True); `panic: test timed out after 2m30s` | |
| 15abc8ed | (pre-call ExitIdle stall) | EndpointShardingSynchronousIdle preexitidle:1 -> `endpointsharding_concurrency_test.go:272: Timed out waiting for queued automatic reconnection`, then deferred cleanup at :259 blocks in `(*balancerWrapper).close` (goexit=True); `panic: test timed out after 2m30s` | |
| 77c90c8d | (pre-call ExitIdle stall) | EndpointShardingIndependentExitIdle preexitidle:1 -> `endpointsharding_concurrency_test.go:154: Timed out waiting for child B to exit idle while child A's update is blocked`, then cleanup at :148 blocks in `(*balancerWrapper).close` (goexit=True); `panic: test timed out after 2m30s` | |
Example full dump (d5658fb5, `ChildExitIdleDuringOtherChildUpdate update:2`):
```console
    concurrency_test.go:185: timeout waiting for blocked child update
    concurrency_test.go:183: timeout waiting for resolver update completion
panic: test timed out after 40s
goroutine 10 [sync.Mutex.Lock]:
google.golang.org/grpc/balancer/endpointsharding.(*balancerWrapper).close(0xc000188a80)
	.../endpointsharding.go:375 +0x45
google.golang.org/grpc/balancer/endpointsharding.(*endpointSharding).Close(0xc0000bee00?)
	.../endpointsharding.go:218 +0x92
runtime.Goexit()
testing.(*common).Fatalf(...)
google.golang.org/grpc/balancer/endpointsharding_test.awaitSignal(...)
	.../concurrency_test.go:119 +0xd9
google.golang.org/grpc/balancer/endpointsharding_test.s.TestChildExitIdleDuringOtherChildUpdate.func4()
	.../concurrency_test.go:183 +0x45
goroutine 11 [select (no cases)]:
google.golang.org/grpc/balancer/endpointsharding.verifyUpdate(...)
google.golang.org/grpc/balancer/endpointsharding.(*balancerWrapper).updateClientConnState(0xc000188a80, ...)
	.../endpointsharding.go:371 +0x205
```

Partial refutations on 00f252d4, 15abc8ed, 77c90c8d. When a configuration worker's join times out, cleanup skips `Close`, and the test ends as a bounded FAIL:
```console
00f252d4 ChildExitIdleDuringSiblingUpdate update:2 FAIL (30.09s)   endpointsharding_concurrency_test.go:146: Timed out waiting for resolver update to finish
15abc8ed EndpointShardingExitIdleIndependentChildren update:2 FAIL (30.15s)
77c90c8d EndpointShardingIndependentExitIdle update:2 FAIL (30.11s)   endpointsharding_concurrency_test.go:147: Timed out waiting for resolver update to finish during cleanup
```
On these three branches, though, the asynchronous reconnect / ExitIdle workers are never joined. A stall before the child's ExitIdle (`preexitidle:1`) causes an assertion failure, and cleanup then blocks in `Close` (last three table rows; re-run with `-timeout 150s`, `panic: test timed out after 2m30s`). Some 40s "hangs" on these branches were only chains of bounded waits. Re-runs at 200s showed 4 of them ending by themselves (50–111s). They are not counted.

Ring-hash: the only test changes on these branches are the 12-line `picker_test.go` edit (no concurrency or cleanup code). No ringhash cleanup path was found to exercise.

Summary: on all 16 branches there is at least one added test where, once a production worker stalls while holding a per-child lock, an assertion failure or join timeout leads to cleanup that calls `Close` (or does `<-updated` on 7442a345 / 450d28f3) with no deadline. The test then hangs until the go test binary timeout instead of failing on its own deadline. This needs a production defect (simulated here). The shipped code never stalls this way in these runs.

## C2

Audited branch `grpc-go-endpointsharding-decouple-locking-perfect` @ 108303326c7d75c37cb5362b9e73fa312e724902.

Fixture facts: `maintainedChild.ExitIdle` only does non-blocking `select` sends, so on its own it never holds `childMu`. The test's deferred cleanup only runs `lb.Close()` if `releaseBuildAndWait()` succeeds (construction finished). It does not check whether the reconnect finished.

Unmodified baseline:
```console
$ go test ./balancer/endpointsharding -run 'Test/SynchronousConstructionIdleCallback' -race -count=30 -v | grep -E -c "^\s*--- PASS: Test/"
30
```

**Instrumentation, not branch behavior:** `verify/repro/c2_stall_mutation.patch` adds `select {}` inside `endpointState.exitIdle` after `childMu.Lock()` when `VERIFY_C2_STALL` is set. It stands in for a production regression where the reconnect worker never releases `childMu`.
```console
$ git apply verify/repro/c2_stall_mutation.patch
$ VERIFY_C2_STALL=1 go test ./balancer/endpointsharding -run '^Test$/^SynchronousConstructionIdleCallback$' -count=1 -timeout 30s -v
    endpointsharding_test.go:571: reconnect was dropped or never delivered after construction-time Idle report
panic: test timed out after 30s
goroutine 21 [sync.Mutex.Lock]:
google.golang.org/grpc/balancer/endpointsharding.(*endpointState).close(0xc0001fad80)
	.../endpointsharding.go:387 +0x45
google.golang.org/grpc/balancer/endpointsharding.(*endpointSharding).Close(0xc0001e6fc0)
	.../endpointsharding.go:228 +0x93
google.golang.org/grpc/balancer/endpointsharding.s.TestSynchronousConstructionIdleCallback.func3()
	.../endpointsharding_test.go:534 +0x4f
runtime.Goexit()
...
google.golang.org/grpc/balancer/endpointsharding.s.TestSynchronousConstructionIdleCallback({{}}, 0xc00019ca80)
	.../endpointsharding_test.go:571 +0x752
goroutine 23 [select (no cases)]:
google.golang.org/grpc/balancer/endpointsharding.(*endpointState).exitIdle(0xc0001fad80)
	.../endpointsharding.go:398 +0x9e
```
With the patch applied but `VERIFY_C2_STALL` unset, the test passes (`ok ... 0.004s`), so the patch does nothing unless the env var is set.

Impact reasoning: the reconnect wait times out at line 571. The deferred cleanup at line 534 still calls `lb.Close()` because construction had finished. `Close` then blocks on the `childMu` that the stalled worker holds, until the go test binary timeout kills it (default 10m). The hang needs a production worker that never releases `childMu`, which the test fixture cannot cause by itself. So this is a diagnosability issue for regressions: the test hangs instead of failing on its 2s deadline. The shipped code does not hang.

## C3

Branch `evalon/grpc-go-en-eec658de` @ 6745096f3672d35cca4e329c8c66872445b7f775. `balancerWrapper.UpdateState` writes `bw.childState.State` under `es.mu`, releases the lock, and then calls `bw.childState.ExitIdle()`. That value-receiver call copies the whole `ChildState` (Endpoint, State, balancer) with no lock held. `UpdateClientConnState` writes `childBalancer.childState.Endpoint` under `es.mu` (line 173).

```console
$ cp verify/repro/c3_childstate_race_test.go.txt balancer/endpointsharding/c3_childstate_race_test.go
$ go test ./balancer/endpointsharding -run '^TestVerifyC3' -race -count=1 -v
WARNING: DATA RACE
Write at 0x00c000210638 by goroutine 9:
  google.golang.org/grpc/balancer/endpointsharding.(*endpointSharding).UpdateClientConnState()
      .../endpointsharding.go:173 +0x48f
Previous read at 0x00c000210638 by goroutine 10:
  google.golang.org/grpc/balancer/endpointsharding.(*balancerWrapper).UpdateState()
      .../endpointsharding.go:373 +0x16c
    testing.go:1617: race detected during execution of test
--- FAIL: TestVerifyC3AutoReconnectChildStateRace (0.01s)
```
Trigger: one child reports IDLE from its own goroutine (the automatic-reconnect path, `DisableAutoReconnect: false`) while the resolver re-sends the same address with changed endpoint attributes. Both are ordinary events. The race is a Go memory-model violation, so the copied `Endpoint` can be torn. It also fails any consumer test suite that runs with `-race`.

## C4

Branch `evalon/grpc-go-en-b0806305` @ 1d028b447979a79f37d833f08fa51e94150694b3. `newBalancerWrapper` holds `bw.mu` while the child is built. A construction-time IDLE queues `go exitIdleSync()`, which waits on `bw.mu`. Once construction returns and unlocks, that goroutine competes with `updateClientConnState` for `bw.mu`, and nothing orders the two.

Repro: the child drops `ExitIdle` until it has endpoints, and never reports IDLE again.
```console
$ cp verify/repro/c4_construction_reconnect_test.go.txt balancer/endpointsharding/c4_construction_reconnect_test.go
$ go test ./balancer/endpointsharding -run '^TestVerifyC4' -count=3 -v | grep iterations
    c4_construction_reconnect_test.go:81: 1/2000 iterations: construction-time reconnect delivered before initial configuration and never re-delivered
    c4_construction_reconnect_test.go:81: 1/2000 iterations: construction-time reconnect delivered before initial configuration and never re-delivered
    c4_construction_reconnect_test.go:81: 1/2000 iterations: construction-time reconnect delivered before initial configuration and never re-delivered
$ GOMAXPROCS=1 go test ./balancer/endpointsharding -run '^TestVerifyC4' -count=1 -v
    c4_construction_reconnect_test.go:83: construction-time reconnect lost in 2/2000 iterations
```
The eval fixture (archived bytes) on the same branch:
```console
$ cp <eval_tests.zip>/tests/eval_endpointsharding_test.go balancer/endpointsharding/
$ go test ./balancer/endpointsharding -run '^TestEval_ConstructionIdleCallbackSafety$' -race -count=20 -v
FAIL count: 4, PASS count: 16
      3     eval_endpointsharding_test.go:1790: 1/25 runs: automatic ExitIdle was not delivered after initial configuration completed
      1     eval_endpointsharding_test.go:1790: 3/25 runs: automatic ExitIdle was not delivered after initial configuration completed
```
Both parts held. Ordering: the reconnect sometimes runs before the initial configuration. Preservation: nothing retries it, so a lazy child stays IDLE. Frequency is about 1 in 2000 child constructions in my repro, and about 1 in 125 per fixture run. Rare but not contrived, since every new endpoint goes through this path.

## C5

Branch `evalon/grpc-go-en-5256b684` @ 7064d26e1ec7e6d2c72edaa4c109a8e62779aa6b.
```console
$ gofmt -l balancer/endpointsharding/endpointsharding_ext_test.go; echo "exit=$?"
exit=0
$ gofmt -d balancer/endpointsharding/endpointsharding_ext_test.go | wc -l
0
$ gofmt -l balancer/
(no output)
$ sed -n 396,398p balancer/endpointsharding/endpointsharding_ext_test.go
func (c *fakeChild) ResolverError(error)                                        {}
func (c *fakeChild) UpdateSubConnState(balancer.SubConn, balancer.SubConnState) {}
func (c *fakeChild) Close()                                                     {}
```
The column alignment of the one-line method bodies is gofmt's own output (go1.25.7). gofmt reports no diff.

## C6

Branch `evalon/grpc-go-en-fd65907a` @ 41fb7a184f8da9db7b7c823bcaf4e8fbca7f1747. `updateState()` checks `inhibitChildUpdates.Load()` before it takes `es.mu`, and it does not check again after taking the lock.

**Instrumentation, not branch behavior:** `verify/repro/c6_instrumentation.patch` adds a nil-by-default hook, `verifyAfterInhibitCheck`, right after that check. It only widens the existing unsynchronized window. It changes no logic.
```console
$ git apply verify/repro/c6_instrumentation.patch
$ cp verify/repro/c6_partial_publication_test.go.txt balancer/endpointsharding/c6_partial_publication_test.go
$ go test ./balancer/endpointsharding -run '^TestVerifyC6' -count=3 -v
    c6_partial_publication_test.go:93: initial publications: [agg=IDLE a=IDLE b=IDLE y=IDLE]
    c6_partial_publication_test.go:136: publications while batch in progress (b blocked): [agg=READY a=READY b=IDLE y=CONNECTING]
    c6_partial_publication_test.go:137: all publications: [agg=IDLE a=IDLE b=IDLE y=IDLE agg=READY a=READY b=IDLE y=CONNECTING agg=READY a=READY b=READY y=READY]
    c6_partial_publication_test.go:140: stale callback published partially-updated aggregate during batch: agg=READY a=READY b=IDLE y=CONNECTING
--- FAIL: TestVerifyC6CallbackPublishesPartialBatch (0.00s)   (3/3 runs)
```
Sequence: child y's callback passes the inhibit check and pauses. A batch starts: a reports READY (suppressed), and b blocks. The callback resumes and publishes `a=READY b=IDLE`, which is a partial aggregate, while the batch is still running. Both parts held. The eval fixture does not catch this:
```console
$ go test ./balancer/endpointsharding -run '^TestEval_BatchUpdateInhibition$' -race -count=5
ok  	google.golang.org/grpc/balancer/endpointsharding	1.016s
```
Without the hook the window is only a few instructions, so it is hard to hit naturally. I did not observe it without instrumentation.

## C7

Branch `evalon/grpc-go-en-7442a345` @ ada3067c682e7d7941bf9f30ac3ba71656181e3c. The only added test file is `endpointsharding_concurrency_test.go`. Every `updateClientConnState` fixture in it returns `nil`. No test updates away an endpoint and then uses its retained `ChildState`. `grep -n -i "error\|remov"` only matches the `errors` import, callback signatures, `t.Errorf` calls, and one `b.ResolverError(errors.New(...))` serialization case.

Mutation run (`verify/repro/c7_mutations.sh`):
```console
== baseline
ok  	google.golang.org/grpc/balancer/endpointsharding	1.189s
== M1 (child UpdateClientConnState errors never returned)
 balancer/endpointsharding/endpointsharding.go | 2 +-
ok  	google.golang.org/grpc/balancer/endpointsharding	1.176s
== M2 (ExitIdle on a retained handle reaches closed/removed child)
 balancer/endpointsharding/endpointsharding.go | 4 +---
ok  	google.golang.org/grpc/balancer/endpointsharding	1.174s
```
Both parts held. The whole committed suite stays green with `-race` when child config errors are silently dropped (M1), and when the closed-child guard on `ExitIdle` is removed (M2).

## C8

Branch `evalon/grpc-go-en-5a311302` @ dd6f5e9c6741d3ca3815245f6ff4776e36b70f95. `testutils.BalancerClientConn.UpdateState` keeps only the latest picker in `NewPickerCh` (buffer 1). The test reads one picker, waits for four ExitIdle signals, and then its loop always calls `waitForChildStates` (a blocking receive) before it checks for both-CONNECTING.

The repro is an identical copy of the test with a 200ms sleep before the first read:
```console
$ cp verify/repro/c8_coalesced_picker_test.go.txt balancer/endpointsharding/c8_coalesced_picker_test.go
$ go test ./balancer/endpointsharding -run '^Test$/^VerifyC8DelayedFirstRead$' -count=1 -v
    c8_coalesced_picker_test.go:34: NewPickerCh buffered pickers before first read: 1
    c8_coalesced_picker_test.go:57: first picker child states: addr1=CONNECTING addr2=CONNECTING; pickers still queued: 0
    c8_coalesced_picker_test.go:61: Timed out waiting for a picker update from the endpointsharding balancer
    --- FAIL: Test/VerifyC8DelayedFirstRead (10.03s)
```
Unmodified test under stress:
```console
$ go test ./balancer/endpointsharding -run '^Test$/^ChildReportsStateDuringBuildAndUpdate$' -count=300
ok  	google.golang.org/grpc/balancer/endpointsharding	0.325s
$ go test ./balancer/endpointsharding -run '^Test$/^ChildReportsStateDuringBuildAndUpdate$' -race -count=100
    --- FAIL: Test/ChildReportsStateDuringBuildAndUpdate (10.05s)
        endpointsharding_test.go:358: Timed out waiting for a picker update from the endpointsharding balancer
FAIL	google.golang.org/grpc/balancer/endpointsharding	10.226s
```
Both parts held. The flake happens naturally at about 1 in 100 runs under `-race` (0 in 300 without it).

## C9

Branch `evalon/grpc-go-en-4039a10c` @ 23651a9ab2f2c84f3bf23c7b874c9657e315a034. `endpointSharding.ExitIdle` calls `bw.exitIdle()` synchronously for each child and never sets `inhibitChildUpdates`.
```console
$ cp verify/repro/c9_exitidle_intermediate_test.go.txt balancer/endpointsharding/c9_exitidle_intermediate_test.go
$ go test ./balancer/endpointsharding -run '^TestVerifyC9' -count=5 -v
    parent notification 1 during/after ExitIdle: agg=CONNECTING children=[a=IDLE b=CONNECTING c=IDLE] childExitIdleCompleted=0
    parent notification 2 during/after ExitIdle: agg=CONNECTING children=[a=IDLE b=CONNECTING c=CONNECTING] childExitIdleCompleted=1
    parent notification 3 during/after ExitIdle: agg=CONNECTING children=[a=CONNECTING b=CONNECTING c=CONNECTING] childExitIdleCompleted=2
    parent ExitIdle produced 3 parent notifications, want 1 consolidated notification
--- FAIL: TestVerifyC9ParentExitIdlePublications (0.00s)   (5/5 runs)
```
Context: the same repro on the pre-task base commit bf9e7cd3 also gives 3 notifications. The behavior already existed, and the task's consolidation guarantee only names `UpdateClientConnState`. Impact: the parent sees N-1 transient pickers whose `ChildStates` show a mix of IDLE and CONNECTING. The aggregate state is the same, so the effect is picker churn only.
