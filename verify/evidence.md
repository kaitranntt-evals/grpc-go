# Evidence — audit run `v-d97b5cf4`

Independent behavioral audit of the endpoint-sharding "decouple child locking" solution
(https://github.com/kaitranntt-evals/grpc-go/tree/grpc-go-endpointsharding-decouple-locking-perfect)
against six claims from an automated static review. Everything below was executed on
Ubuntu with Go 1.25 (`go version` in `/home/ubuntu/repos/grpc-go`); all commands are
replayable from this file plus the files in `verify/repro/`.

## Setup

```sh
cd /home/ubuntu/repos/grpc-go                       # branch verify/grpc-go-endpointsharding-decouple-locking-v-d97b5cf4
git remote add evalon https://github.com/kaitranntt-evals/grpc-go-endpointsharding-decouple-locking
# one detached worktree per claim branch, e.g. for 93bc5f0b:
git fetch evalon evalon/grpc-go-en-93bc5f0b && git worktree add --detach /home/ubuntu/wt/93bc5f0b FETCH_HEAD
# byte-exact eval fixture (from eval_tests.zip) provisioned into each worktree:
unzip -o eval_tests.zip -d /home/ubuntu/eval_tests
cp /home/ubuntu/eval_tests/tests/eval_endpointsharding_test.go /home/ubuntu/wt/<id>/balancer/endpointsharding/
```

Worktrees used (`git worktree list`): 06ef8655 9225f17e, 16469eb7 53489e72, 182fc00e 25eaa24d, 38c57664 ca3ab423,
3ef33691 72849274, 4af83566 b2bd1cc5, 4e7ec0bb 9ec8181f, 507dbc51 cbdadb32, 5491d39c 1e4772bd, 5a85e94a ffbfb779,
726fefa0 499c774e, 73e3caec 8c0af327, 80522cf2 fabf6594, 8442e70e 4315b976, 90f0333c 7e6a4f83, 93bc5f0b 22306a58,
a17565eb 905910e0, cbc87f53 bafd4992, d4096381 a8ad978c, db20107b 10110afc, db4d0728 1ecb34a3, e4ee1715 ffc85778.

Production code was never modified on the audit branch. Instrumentation and mutations were applied only to
scratch copies of the claim-branch worktrees and are preserved as patches/tests under `verify/repro/`.

## C1

**Claim:** the modified concurrency tests do not ensure bounded termination when a worker times out /
an assertion fails. Parts: (1) unbounded cleanup joins; (2) `Close` invoked before a lock-acquiring worker
completed a bounded join. Adjudicated separately on all 22 branches.

**Method.** For each branch, `verify/repro/c1_table.txt` lists the changed/added concurrency test(s), the
test file and the line(s) where the stub child waits on the test's release barrier. `verify/repro/c1_never_completing_worker.sh`
copies the worktree to a scratch dir, rewrites that receive to `_ = <chan>; select {}` (a worker that never
completes, i.e. the worker-timeout path that also forces every assertion-failure path in the test), then runs the
single test with `go test -timeout 45s -v`. Bounded termination shows up as a normal `--- FAIL` with the test's
own message; unbounded termination shows up as `panic: test timed out after 45s` with goroutine stacks.
`verify/repro/c1_summarize.py` condenses each run to: whether it timed out, the test's own log lines, and the
frames of the goroutines blocked inside `balancer/endpointsharding` (test line ← production line).

```sh
while read b f lines name; do /home/ubuntu/c1/run_one.sh "$b" "$f" "$lines" "$name"; done < verify/repro/c1_table.txt
python3 verify/repro/c1_summarize.py > verify/repro/c1_run_summary.txt
```

**Result:** every one of the 30 mutated test runs across the 22 branches ended in `panic: test timed out after 45s`
(`timed_out=True` for every entry in `verify/repro/c1_run_summary.txt`), except `73e3caec`, whose generic exercise
terminated in a bounded way and needed a second exercise (below). Two failure shapes were observed:

*Shape A — unbounded cleanup join (part 1 held, part 2 not reached):* a deferred `<-done` with no deadline;
`Close` is deferred *before* it (so it runs after the join) and is therefore never reached.
Key lines from `c1_run_summary.txt` (goroutine frames are `test line <- production line`):

```text
===== 93bc5f0b-...: GOROUTINE 11 [chan receive]: | endpointsharding_ext_test.go:432 <- ...
===== 726fefa0-...: GOROUTINE 11 [chan receive]: | endpointsharding_ext_test.go:429 <- ...
===== 3ef33691-...: GOROUTINE 11 [chan receive]: | endpointsharding_ext_test.go:457 <- endpointsharding_ext_test.go:486
===== 182fc00e-...: GOROUTINE 11 [chan receive]: | endpointsharding_ext_test.go:129 <- endpointsharding_ext_test.go:188
===== 8442e70e-...: GOROUTINE 11 [chan receive]: | endpointsharding_ext_test.go:464 <- ...
===== 4e7ec0bb-...: GOROUTINE 11 [chan receive]: | endpointsharding_ext_test.go:461 <- ...
===== 90f0333c-...: GOROUTINE 11 [chan receive]: | endpointsharding_ext_test.go:174 <- ...
```

*Shape B — `Close` invoked while the worker has not completed (part 2 held, part 1 refuted):* the cleanup wait
is bounded (the test's own `Timeout waiting ...`/`Timed out ...` message is logged), but the deferred /
`t.Cleanup` `Close()` then blocks forever on the child mutex still held by the wedged worker
(`sync.Mutex.Lock` inside `balancerWrapper.close`):

```text
===== 06ef8655: LOG endpointsharding_ext_test.go:479: Timeout waiting for blocked UpdateClientConnState to complete
                GOROUTINE 10 [sync.Mutex.Lock]: | endpointsharding.go:430 <- endpointsharding.go:244 <- endpointsharding.go:243 <- endpointsharding_ext_test.go:479
===== 16469eb7: LOG endpointsharding_concurrency_ext_test.go:236: Timed out waiting for UpdateClientConnState() to return after unblocking the child
                GOROUTINE 23 [sync.Mutex.Lock]: | endpointsharding.go:406 <- endpointsharding.go:243 <- endpointsharding.go:242 <- endpointsharding_concurrency_ext_test.go:162
===== 38c57664: LOG endpointsharding_ext_test.go:525: Timed out waiting for ExitIdle on the slow child after it was unblocked
                GOROUTINE 10 [sync.Mutex.Lock]: | endpointsharding.go:248 <- endpointsharding_ext_test.go:525
===== 4af83566: LOG endpointsharding_ext_test.go:529: Timeout waiting for UpdateClientConnState to complete
                GOROUTINE 10 [sync.Mutex.Lock]: | endpointsharding.go:409 <- endpointsharding.go:249 <- endpointsharding.go:248
===== d4096381: LOG endpointsharding_ext_test.go:445: Timeout waiting for blocked UpdateClientConnState to return
                GOROUTINE ... [sync.Mutex.Lock]: | endpointsharding.go:234 <- endpointsharding_ext_test.go:401
===== e4ee1715: LOG endpointsharding_ext_test.go:475 (bounded t.Errorf) ; GOROUTINE [sync.Mutex.Lock]: | endpointsharding.go:245 (deferred es.Close)
```

The same shape (bounded wait message followed by a `Close` blocked in `sync.Mutex.Lock` inside
`balancer/endpointsharding/endpointsharding.go`) was recorded for `507dbc51` (`:528` → `go:406`),
`5491d39c` (`:525`/`:581`/`:646` → `go:236`/`:419`, three tests), `5a85e94a` (`:505` → `go:401`),
`80522cf2` (`:228`/`:276` → `go:398`), `a17565eb` (`:556` → `go:405`), `cbc87f53` (`:524`/`:549` → `go:254`/`:419`),
`db20107b` (`:258` → `go:250`) and `db4d0728` (`:505`/`:530` → `go:242`/`:410`). Full per-run text is in
`verify/repro/c1_run_summary.txt`.

*73e3caec (special case).* The generic exercise with a long enough `go test -timeout` terminated in a bounded way:

```text
$ go test ./balancer/endpointsharding -run '^Test$/^ChildExitIdleWhileAnotherChildBlocked$' -count=1 -timeout 240s -v   # mutated line 371
    endpointsharding_ext_test.go:420: UpdateClientConnState did not finish after unblocking the child
    endpointsharding_ext_test.go:420: ResolverError did not finish after unblocking the child
    endpointsharding_ext_test.go:420: Close did not finish after unblocking the child
    endpointsharding_ext_test.go:420: ExitIdle did not finish after unblocking the child
--- FAIL: Test (50.28s)
FAIL	google.golang.org/grpc/balancer/endpointsharding	50.282s
```

so part 1 is refuted there and the cleanup skips `Close` when the *parent operation* did not finish. However the test
calls `slowState.ExitIdle()` on the blocked child, which on this branch is
`childState.ExitIdle = func() { go childBalancer.ExitIdle() }` (`endpointsharding.go:158`) — a lock-acquiring
goroutine that cleanup never joins before `b.Close()` (`endpointsharding_ext_test.go:429`). Exercising that queued
worker (`verify/repro/c1_73e3caec_queued_exitidle_never_completes.patch` makes the stub's `ExitIdle` never return
once `unblock` is closed):

```text
$ go test ./balancer/endpointsharding -run '^Test$/^ChildExitIdleWhileAnotherChildBlocked$/^ResolverError$' -count=1 -timeout 40s -v
panic: test timed out after 40s
goroutine 11 [sync.Mutex.Lock]:
google.golang.org/grpc/balancer/endpointsharding.(*balancerWrapper).close(0xc000188c00)
	balancer/endpointsharding/endpointsharding.go:368 +0x45
google.golang.org/grpc/balancer/endpointsharding.(*endpointSharding).Close-range1(...)
	balancer/endpointsharding/endpointsharding.go:215
goroutine 15 [select (no cases)]:
google.golang.org/grpc/balancer/endpointsharding_test.s.TestChildExitIdleWhileAnotherChildBlocked.func1.5(0xc0000f70e0)
	balancer/endpointsharding/endpointsharding_ext_test.go:395 +0x87
google.golang.org/grpc/balancer/endpointsharding.(*balancerWrapper).ExitIdle(0x0?)
	balancer/endpointsharding/endpointsharding.go:355 +0x82
created by google.golang.org/grpc/balancer/endpointsharding.(*endpointSharding).UpdateClientConnState.func2 in goroutine 14
	balancer/endpointsharding/endpointsharding.go:158 +0x56
```

(`UpdateClientConnState`/`Close` variants passed 6/6 because `Close` happened to win the `childMu` race; the
`ExitIdle` variant took the bounded no-`Close` path.) Full text: `verify/repro/c1_73e3caec_runs.txt`.

**Verdicts (per branch):** CONFIRMED on all 22 branches — part 1 (unbounded join) on
182fc00e, 3ef33691, 4e7ec0bb, 726fefa0, 8442e70e, 90f0333c, 93bc5f0b; part 2 (`Close` before the worker completed)
on 06ef8655, 16469eb7, 38c57664, 4af83566, 507dbc51, 5491d39c, 5a85e94a, 73e3caec, 80522cf2, a17565eb, cbc87f53,
d4096381, db20107b, db4d0728, e4ee1715. On every branch exactly one of the two parts held.

## C2

**Claim** ([grpc-go-en-db4d0728]): the new `balancerWrapper.mu` comment says "To avoid deadlocks, do not acquire
es.mu while holding mu" (`endpointsharding.go:356-360`), yet the synchronous child callback path
`updateClientConnState` (`:399-400`, holds `bw.mu`) → `child.UpdateClientConnState` → `bw.UpdateState` (`:371-372`,
`bw.es.mu.Lock()`) acquires `es.mu` while `mu` is held.

**Command** (`verify/repro/verify_c2_internal_test.go` copied into `balancer/endpointsharding/` of a scratch copy of the
db4d0728 worktree; the test holds `es.mu` from another goroutine for 300 ms and measures how long the stub child's
synchronous `bd.ClientConn.UpdateState` blocks while the child's `bw.mu` is verifiably held via `TryLock`):

```sh
go test ./balancer/endpointsharding -run '^TestVerifyC2' -race -count=1 -v
```

```text
    verify_c2_internal_test.go:59: inside synchronous child UpdateClientConnState callback: child bw.mu held=true; bd.ClientConn.UpdateState blocked 301ms waiting for es.mu (held elsewhere for 300ms)
--- PASS: TestVerifyC2_SyncCallbackAcquiresParentMuWhileChildMuHeld (0.30s)
```

(`verify/repro/c2_run.txt`). **Verdict: CONFIRMED** — the acquisition the comment prohibits happens on the primary
callback path.

## C3

**Claim** ([grpc-go-en-d4096381]): no changed test proves child B completes its operation, including its synchronous
state callback, while child A is held.

**Test under adjudication:** `TestEndpointShardingChildOperationsDoNotBlockEachOther`
(`balancer/endpointsharding/endpointsharding_ext_test.go:365-473`). Child A is held inside the stub's
`UpdateClientConnState` (`<-unblockUpdate`, blocking the parent `UpdateClientConnState`, which holds `es.opMu` and A's
`bw.mu`). While A is held the test (a) calls `otherChild.ExitIdle()` and requires `exitIdleCalled` (`:452-458`), then
(b) requires a picker from `tcc.NewPickerCh` in which the other child is `READY` (`:461-470`). That picker is produced
inside B's synchronous callback: stub `ExitIdle` → `bd.ClientConn.UpdateState(READY)` → `balancerWrapper.UpdateState`
(`endpointsharding.go:367-378`) → `es.updateState()` → `es.cc.UpdateState`; `bw.es.updateState()` is the last statement
of `UpdateState`, so the only callback work remaining after the observed signal is the return itself. `unblockUpdate`
is closed only in the deferred cleanup, i.e. after both assertions.

```sh
cd /home/ubuntu/wt/d4096381
go test ./balancer/endpointsharding -run '^Test$/^EndpointShardingChildOperationsDoNotBlockEachOther$' -race -count=3 -v
```

```text
ok  	google.golang.org/grpc/balancer/endpointsharding	1.036s          (3/3 PASS)
```

Mutation 1 (`verify/repro/c3_mutation_1_exitidle_blocked_behind_other_child.patch`: B's `exitIdle` first waits on
`es.opMu`, i.e. B's operation blocks behind A):

```text
    endpointsharding_ext_test.go:458: Timeout waiting for ExitIdle on a child while a different child's update is blocked
--- FAIL: Test/EndpointShardingChildOperationsDoNotBlockEachOther (10.05s)
```

Mutation 2 (`verify/repro/c3_mutation_2_ready_callback_blocked_behind_other_child.patch`: B's READY callback does not
complete within the deadline while A is held):

```text
    endpointsharding_ext_test.go:470: Timeout waiting for Ready state update from a child while a different child's update is blocked
--- FAIL: Test/EndpointShardingChildOperationsDoNotBlockEachOther (15.03s)
```

(`verify/repro/c3_mutation_runs.txt`). **Verdict: REFUTED** — the changed test holds A on the contested production
path, requires B's operation and B's synchronous callback publication before A is released, and fails when either is
blocked.

## C4

**Claim** ([grpc-go-en-93bc5f0b]): a queued automatic reconnect can invoke a newly constructed child before its
initial configuration. Parts: (1) child-mutex gap between construction and initial configuration; (2) reconnect
reachability through that gap.

Production code (`endpointsharding.go:160-168`, `:355-358`): `childMu` is locked around `es.childBuilder(...)`, unlocked
at `:165`, and re-acquired only inside `childBalancer.updateClientConnState` (`:356`); `childState.ExitIdle` is
`go childBalancer.exitIdle()` (`:160`) and `UpdateState(IDLE)` triggers it when auto-reconnect is enabled.

Direct mutex probe (`verify/repro/verify_c4_gap_internal_test.go`, a goroutine spinning on `childMu.TryLock` while the
parent builds/configures the child):

```sh
cd /home/ubuntu/wt/93bc5f0b && go test ./balancer/endpointsharding -run '^TestVerifyC4Gap' -race -count=1 -v
    verify_c4_gap_internal_test.go:88: childMu was acquirable between construction and initial configuration in 0/20 iterations
```

— a polling probe did not catch the window (it is a handful of instructions wide), so part 1 is not settled by that
probe alone. Reachability probe (`verify/repro/verify_c4_ext_test.go`: stub child whose builder reports IDLE
synchronously, records whether `ExitIdle` arrives before its first `UpdateClientConnState`; auto-reconnect enabled):

```sh
VERIFY_C4_ITERS=200 go test ./balancer/endpointsharding -run '^TestVerifyC4_' -race -count=1 -v
```

```text
run 1: buildDelay=0s:  ExitIdle reached the child BEFORE its initial UpdateClientConnState in 3/200 runs
       buildDelay=5ms: ExitIdle reached the child BEFORE its initial UpdateClientConnState in 1/200 runs
run 2: buildDelay=0s: 1/200   buildDelay=5ms: 3/200
run 3: buildDelay=0s: 3/200   buildDelay=5ms: 0/200
no -race: buildDelay=0s: 0/200   buildDelay=5ms: 2/200
    verify_c4_ext_test.go:82: automatic reconnect invoked an unconfigured child in 3/200 runs
--- FAIL: TestVerifyC4_AutoReconnectBeforeInitialConfig
```

(`verify/repro/c4_reconnect_runs.txt`). Because `exitIdle` must hold `childMu` to call `child.ExitIdle()`, each observed
premature `ExitIdle` is itself an observed acquisition of `childMu` between the `:165` release and the `:356`
re-acquisition, which settles part 1 as well. The byte-exact fixture `TestEval_ConstructionIdleCallbackSafety` passes
on this branch (`--- PASS (2.57s)`), i.e. it does not detect this. **Verdict: CONFIRMED** (2 of 2 parts).

## C5

**Claim** ([grpc-go-en-93bc5f0b]): `updateState` reads `inhibitChildUpdates` before taking `es.mu`
(`endpointsharding.go:231-238`), so a callback that passed the check can publish intermediate state during a batch
that begins afterwards. Parts: (1) pre-lock decision crosses batch entry; (2) intermediate publication while the batch
is still held.

Instrumentation (`verify/repro/c5_instrumentation.patch`, applied to a scratch copy of the 93bc5f0b worktree): a
nil-by-default `testHookAfterInhibitCheck` invoked immediately after the `inhibitChildUpdates` read and before
`es.mu.Lock()` — it only adds a pause point, no logic change. Test `verify/repro/verify_c5_internal_test.go` pauses child
"a"'s callback at the hook, starts a two-child `UpdateClientConnState` batch and holds it inside child "a"'s
`UpdateClientConnState` after child "b" reported CONNECTING, then resumes the paused callback.

```sh
go test ./balancer/endpointsharding -run '^TestVerifyC5' -race -count=1 -v
```

```text
    verify_c5_internal_test.go:71: pre-batch picker: b=IDLE,a=IDLE
    verify_c5_internal_test.go:102: child callback for "a" passed the inhibitChildUpdates check (inhibit=false) and is paused before es.mu.Lock()
    verify_c5_internal_test.go:117: batch active: first child reported CONNECTING, child "a" held inside UpdateClientConnState, inhibitChildUpdates=true
    verify_c5_internal_test.go:122: no publication while the batch is held and the callback is paused (inhibition working so far)
    verify_c5_internal_test.go:141: INTERMEDIATE PUBLICATION DURING ACTIVE BATCH: aggregate=CONNECTING children=[b=CONNECTING,a=IDLE] (batch still held on child "a", inhibitChildUpdates=true)
    verify_c5_internal_test.go:159: final (end-of-batch) picker: b=CONNECTING,a=READY
--- FAIL: TestVerifyC5_PreBatchDecisionPublishesDuringBatch (0.20s)
```

(`verify/repro/c5_run.txt`). The fixture `TestEval_BatchUpdateInhibition` passes on this branch (`--- PASS (0.00s)`).
**Verdict: CONFIRMED** (2 of 2 parts).

## C6

**Claim** ([grpc-go-en-5a85e94a]): the two new `balancer.ExitIdler` declarations in ringhash cause non-exempt
Staticcheck SA1019 findings. The branch diff adds `var idleBalancer balancer.ExitIdler` (`ringhash.go:271`) and
`exitIdler balancer.ExitIdler` (`:405`); `balancer.ExitIdler` carries `// Deprecated:` (`balancer/balancer.go:374-376`).
`scripts/vet.sh` installs the pinned tool from `test/tools` (`go install honnef.co/go/tools/cmd/staticcheck`) and runs
`staticcheck -checks 'all' ./...` (`vet.sh:129`), then applies an SA1019 exemption list; `verify/repro/c6_vet_sa1019_filter.sh`
is that filter copied verbatim.

```sh
cd /home/ubuntu/wt/5a85e94a && PATH="$HOME/go/bin:$PATH"
staticcheck -version                                    # staticcheck 2026.1 (v0.7.0)
staticcheck -checks 'all' ./... > /home/ubuntu/repro/c6_staticcheck.out   # exit=1
grep ExitIdler /home/ubuntu/repro/c6_staticcheck.out
SC_OUT=/home/ubuntu/repro/c6_staticcheck.out bash verify/repro/c6_vet_sa1019_filter.sh; echo "filter exit=$?"
```

```text
balancer/ringhash/ringhash.go:271:20: balancer.ExitIdler is deprecated: All balancers must implement this interface. This interface will be removed in a future release.  (SA1019)
balancer/ringhash/ringhash.go:405:12: balancer.ExitIdler is deprecated: All balancers must implement this interface. This interface will be removed in a future release.  (SA1019)
filter exit=1
```

(`verify/repro/c6_run.txt`). **Verdict: CONFIRMED.**

[grpc-go-en-db4d0728]: https://github.com/kaitranntt-evals/grpc-go-endpointsharding-decouple-locking/tree/evalon/grpc-go-en-db4d0728
[grpc-go-en-d4096381]: https://github.com/kaitranntt-evals/grpc-go-endpointsharding-decouple-locking/tree/evalon/grpc-go-en-d4096381
[grpc-go-en-93bc5f0b]: https://github.com/kaitranntt-evals/grpc-go-endpointsharding-decouple-locking/tree/evalon/grpc-go-en-93bc5f0b
[grpc-go-en-5a85e94a]: https://github.com/kaitranntt-evals/grpc-go-endpointsharding-decouple-locking/tree/evalon/grpc-go-en-5a85e94a
