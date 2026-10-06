## Setup used for all claims

Run ID `v-e47d78f0`. Audited branch: `grpc-go-endpointsharding-decouple-locking-perfect` (checked out as `verify/grpc-go-endpointsharding-decouple-locking-v-e47d78f0`, HEAD `0d0f6fee`). Every claim targets a different branch in the repository `kaitranntt-evals/grpc-go-endpointsharding-decouple-locking`; none of those branches exist on `origin`, so they were fetched from that repository through a second remote and checked out in separate worktrees. Task base commit: `bf9e7cd3430df40d0732ba42eb88bd5f2cc63407`. Toolchain: `go version go1.25.7 linux/amd64`, 8 CPUs.

```sh
cd ~/repos/grpc-go
git fetch origin grpc-go-endpointsharding-decouple-locking-perfect
git checkout -b verify/grpc-go-endpointsharding-decouple-locking-v-e47d78f0 origin/grpc-go-endpointsharding-decouple-locking-perfect
git remote add evals https://github.com/kaitranntt-evals/grpc-go-endpointsharding-decouple-locking
for b in f7299717 b139ba1f 2a7c48e0 78149e07 33ac8ac2; do git fetch evals evalon/grpc-go-en-$b:refs/remotes/evals/evalon/grpc-go-en-$b; done
git worktree add --detach ~/wt/c1 evals/evalon/grpc-go-en-f7299717   # b5149f65
git worktree add --detach ~/wt/c2 evals/evalon/grpc-go-en-b139ba1f   # 7be2bcb3
git worktree add --detach ~/wt/c3 evals/evalon/grpc-go-en-2a7c48e0   # 324f03a5
git worktree add --detach ~/wt/c4 evals/evalon/grpc-go-en-78149e07   # 70be432f
git worktree add --detach ~/wt/c5 evals/evalon/grpc-go-en-33ac8ac2   # aff9f7ba
git worktree add --detach ~/wt/base bf9e7cd3                         # control: task base commit
```

All five claim branches are a single commit on top of `bf9e7cd3`. Repro files live in `verify/repro/`; the Go test files carry a `.go.txt` suffix so that `go build ./...` / `go vet ./...` on this branch do not pick them up. `verify/repro/run.sh <claim>` fetches the claim branch into a throwaway worktree, copies the repro in and runs it (set `CLAIM_REPO=<remote or URL>` to override where the claim branches are fetched from). No production file is modified on this branch; the two `*.patch` files are mutations applied only inside throwaway worktrees.

## C1

Claim: the solution on `evalon/grpc-go-en-f7299717` changes Go files in `balancer/endpointsharding` whose formatting differs from gofmt output. Observed: it does not.

```sh
cd ~/wt/c1   # evalon/grpc-go-en-f7299717 @ b5149f65
git diff --name-only bf9e7cd3 HEAD -- balancer/endpointsharding
gofmt -l $(git diff --name-only bf9e7cd3 HEAD -- '*.go'); echo "rc=$?"
gofmt -d $(git diff --name-only bf9e7cd3 HEAD -- balancer/endpointsharding | grep '\.go$'); echo "rc=$?"
gofmt -s -d -l balancer/endpointsharding balancer/ringhash; echo "rc=$? (dirs)"     # the exact flags scripts/vet.sh:105 uses
```

```console
balancer/endpointsharding/endpointsharding.go
balancer/endpointsharding/endpointsharding_ext_test.go
--- gofmt -l
rc=0
--- gofmt -d
rc=0
rc=0 (dirs)
```

Every gofmt invocation printed nothing (empty diff, empty file list) for both changed files, including with `-s` as used by `scripts/vet.sh:105` (`gofmt -s -d -l . 2>&1 | fail_on_output`).

Positive control that this gofmt binary (`/usr/local/bin/gofmt`, go1.25.7) does flag misformatted input: replacing the first leading tab of a copy of the changed test file with two spaces makes `gofmt -l` list it.

```sh
cp balancer/endpointsharding/endpointsharding_ext_test.go /tmp/ctl.go && sed -i '0,/^\t/s//  /' /tmp/ctl.go && gofmt -l /tmp/ctl.go
```

```console
/tmp/ctl.go
```

Replay: `verify/repro/run.sh c1` prints the two changed files followed by `gofmt -d rc=0` and `gofmt -s -d -l rc=0` with no diff output.

## C2

Claim: on `evalon/grpc-go-en-b139ba1f` the removed-child ExitIdle regression passes its negative assertion after consuming a forbidden notification for that child. Naming drift: `TestClosedStateGuardCoverage` does not exist on this branch; the removed-child regression is `TestChildStateExitIdle_AfterChildRemoved` (`balancer/endpointsharding/endpointsharding_ext_test.go:605`) and the consuming helper is `awaitExitIdle` (same file, line 499).

### Test text (the two parts)

Part "notification retention" - `awaitExitIdle` reads from the shared notification channel and silently drops every address that is not the wanted one (no re-queue, no failure):

```go
func awaitExitIdle(ctx context.Context, t *testing.T, ch <-chan string, wantAddr string) {
	t.Helper()
	for {
		select {
		case addr := <-ch:
			if addr == wantAddr {
				return
			}
		case <-ctx.Done():
			t.Fatalf("Timeout waiting for ExitIdle on child %q", wantAddr)
		}
	}
}
```

Part "assertion sequence" - the regression calls ExitIdle on the removed child A first, then runs that draining helper for B, and only afterwards performs the negative check on the same channel:

```go
	states[fakeChildAddrA].ExitIdle()
	states[fakeChildAddrB].ExitIdle()
	awaitExitIdle(ctx, t, hooks.exitIdleCh, fakeChildAddrB)
	sCtx, sCancel := context.WithTimeout(ctx, defaultTestShortTimeout)
	defer sCancel()
	select {
	case addr := <-hooks.exitIdleCh:
		t.Fatalf("ExitIdle unexpectedly called on child %q", addr)
	case <-sCtx.Done():
	}
```

### Demonstration 1 - direct injection, production code untouched (deterministic)

`verify/repro/c2_removed_child_exitidle_test.go.txt` is a line-for-line copy of the branch test using the branch's own helpers; the only change is one injected line `hooks.exitIdleCh <- fakeChildAddrA` (exactly what `fakeChild.ExitIdle` sends when the removed child is called) placed before `states[fakeChildAddrB].ExitIdle()`, plus log lines.

```sh
cd ~/wt/c2   # evalon/grpc-go-en-b139ba1f @ 7be2bcb3
cp ~/repos/grpc-go/verify/repro/c2_removed_child_exitidle_test.go.txt balancer/endpointsharding/c2_removed_child_exitidle_test.go
go test ./balancer/endpointsharding -run 'Test/ReproC2' -race -count=20 -v 2>&1 | grep -E "REPRO|--- (PASS|FAIL): Test/|^(ok|FAIL|PASS)" | sort | uniq -c
```

```console
     20     --- PASS: Test/ReproC2_ForbiddenExitIdleSwallowed (0.01s)
     20     c2_removed_child_exitidle_test.go:39: REPRO: injected forbidden "addr-a" notification; len(exitIdleCh)=1
     20     c2_removed_child_exitidle_test.go:42: REPRO: awaitExitIdle("addr-b") returned; len(exitIdleCh)=0 (forbidden notification already consumed)
     20     c2_removed_child_exitidle_test.go:49: REPRO: negative assertion PASSED although ExitIdle was reported for removed child "addr-a"
      1 PASS
      1 ok  	google.golang.org/grpc/balancer/endpointsharding	1.258s
```

20/20: the forbidden notification is in the channel before the wait (`len=1`), is gone after `awaitExitIdle("addr-b")` returns (`len=0`), no failure is reported, and the negative assertion passes.

### Demonstration 2 - mutated production code, branch test unmodified

`verify/repro/c2_mutation.patch` removes the `!bw.isClosed` guard from `balancerWrapper.exitIdleSync` (so a removed/closed child really receives ExitIdle) and prints a marker line when that happens. The branch's own `TestChildStateExitIdle_AfterChildRemoved` is run unmodified.

```sh
cd ~/wt/c2
go test ./balancer/endpointsharding -run 'Test/ChildStateExitIdle_AfterChildRemoved' -race -count=1 -v | tail -3      # unmutated baseline
git apply ~/repos/grpc-go/verify/repro/c2_mutation.patch
for i in $(seq 1 40); do go test ./balancer/endpointsharding -run 'Test/ChildStateExitIdle_AfterChildRemoved' -race -count=1 -v > /tmp/c2_run_$i.txt 2>&1; echo "run $i rc=$? mutant_lines=$(grep -c MUTANT /tmp/c2_run_$i.txt) $(grep -E -- '--- (PASS|FAIL): Test/' /tmp/c2_run_$i.txt)"; done
```

Baseline (unmutated): `--- PASS: Test/ChildStateExitIdle_AfterChildRemoved (0.01s)`.

With the mutant, all 40 runs printed `mutant_lines=1`; 19 runs PASSED (`rc=0`), 21 FAILED (`rc=1`). A passing run (run 1), verbatim tail:

```console
=== RUN   Test/ChildStateExitIdle_AfterChildRemoved
MUTANT: forbidden ExitIdle delivered to CLOSED child
--- PASS: Test (0.01s)
    --- PASS: Test/ChildStateExitIdle_AfterChildRemoved (0.01s)
PASS
ok  	google.golang.org/grpc/balancer/endpointsharding	1.031s
```

A failing run (run 10), verbatim tail:

```console
=== RUN   Test/ChildStateExitIdle_AfterChildRemoved
MUTANT: forbidden ExitIdle delivered to CLOSED child
    endpointsharding_ext_test.go:626: ExitIdle unexpectedly called on child "addr-a"
--- FAIL: Test (0.00s)
    --- FAIL: Test/ChildStateExitIdle_AfterChildRemoved (0.00s)
FAIL
FAIL	google.golang.org/grpc/balancer/endpointsharding	0.021s
FAIL
```

Earlier counted runs of the same mutant (guard removed, without the marker line), 300 iterations each:

```console
flags='-race' GOMAXPROCS='1' PASS=142 FAIL=158
flags='-race' GOMAXPROCS='4' PASS=131 FAIL=169
flags='-race' GOMAXPROCS='' PASS=152 FAIL=148
flags='' GOMAXPROCS='1' PASS=0 FAIL=300
flags='' GOMAXPROCS='4' PASS=3 FAIL=297
flags='' GOMAXPROCS='' PASS=1 FAIL=299
```

Replay: `verify/repro/run.sh c2` and `verify/repro/run.sh c2-mutant`. A second, independent 40-run pass through `run.sh c2-mutant` printed:

```console
     22 mutant_lines=1     --- FAIL: Test/ChildStateExitIdle_AfterChildRemoved
     18 mutant_lines=1     --- PASS: Test/ChildStateExitIdle_AfterChildRemoved
```

### Impact reasoning

The regression exists to prove "ExitIdle is a no-op once the child has been removed". Both ExitIdle calls are asynchronous (`go bw.exitIdleSync()`), so if the guard regresses, the removed child's notification and the live child's notification race into the same channel. Whenever A's notification arrives first, `awaitExitIdle(B)` discards it and the negative select sees an empty channel. Under `-race` (the mode the task and the eval commands require) the guard-less mutant passes this test about half the time (19/40, 142-152/300); without `-race` it is almost always caught. So the test is a flaky detector rather than a guard: a single green `-race` run says little about the closed-child guard. Production behavior on this branch is correct (the unmutated guard is present and the baseline passes); the defect is in the test only. Minimal fix: make the helper fail (or record) on any unexpected address, or use per-child channels, and assert on A before draining for B.

## C3

Claim: on `evalon/grpc-go-en-2a7c48e0` a balancer constructed by `balancer/ringhash/ringhash_test.go:setupTest` remains unclosed on a test-exit path where its caller discards the returned balancer.

### Ownership trace

```sh
cd ~/wt/c3   # evalon/grpc-go-en-2a7c48e0 @ 324f03a5
git diff --stat bf9e7cd3 HEAD -- balancer/ringhash/ringhash_test.go | wc -l
grep -n "setupTest(\|b\.Close\|Cleanup" balancer/ringhash/ringhash_test.go
```

```console
0
55:func setupTest(t *testing.T, endpoints []resolver.Endpoint) (*testutils.BalancerClientConn, balancer.Balancer, balancer.Picker) {
110:	cc, b, p1 := setupTest(t, endpoints)
140:	cc, _, p0 := setupTest(t, []resolver.Endpoint{{Addresses: []resolver.Address{wantAddr1}}})
199:	cc, _, p0 := setupTest(t, endpoints)
340:	cc, _, p0 := setupTest(t, wantEndpoints)
441:	cc, b, p0 := setupTest(t, endpoints)
533:	cc, _, p0 := setupTest(t, wantEndpoints)
707:	cc, b, p0 := setupTest(t, wantEndpoints1)
```

`setupTest` builds the balancer with `balancer.Get(Name).Build(cc, balancer.BuildOptions{})` and returns it; the file contains no `t.Cleanup` and no `b.Close` anywhere (the grep shows only the definition and the seven call sites), four callers discard the balancer (`TestOneEndpoint` line 140, `TestThreeSubConnsAffinity` 199, `TestThreeBackendsAffinityMultiple` 340, `TestAutoConnectEndpointOnTransientFailure` 533). The file is byte-identical to the base commit (`git diff --stat` empty), i.e. the solution did not add cleanup and did not introduce the gap either.

### Instrumented run

`verify/repro/c3_setuptest_close_instrumentation_test.go.txt` re-registers a wrapping builder under the ringhash name from a test file (production code untouched), so every balancer `setupTest` obtains reports its `Close`. It then runs the package's real, unmodified test methods as subtests (`s.TestOneEndpoint` etc.) and counts Build/Close after each subtest has completely finished (defers and `t.Cleanup` functions included), plus two early-exit paths after construction and a positive control.

```sh
cp ~/repos/grpc-go/verify/repro/c3_setuptest_close_instrumentation_test.go.txt balancer/ringhash/zz_c3_setuptest_close_instrumentation_test.go
go test ./balancer/ringhash -run '^TestReproC3_' -race -count=1 -v 2>&1 | grep -E "C3-INSTR|UNCLOSED|^--- |^(ok|FAIL|PASS)|UpdateClientConnState returned|panic"
```

```console
    zz_c3_setuptest_close_instrumentation_test.go:99: C3-INSTR TestOneEndpoint(discards)                     built=1 closed=0 UNCLOSED=1
    zz_c3_setuptest_close_instrumentation_test.go:99: C3-INSTR TestThreeSubConnsAffinity(discards)           built=1 closed=0 UNCLOSED=1
    zz_c3_setuptest_close_instrumentation_test.go:99: C3-INSTR TestThreeBackendsAffinityMultiple(discards)   built=1 closed=0 UNCLOSED=1
    zz_c3_setuptest_close_instrumentation_test.go:99: C3-INSTR TestAutoConnectEndpointOnTransientFailure(discards) built=1 closed=0 UNCLOSED=1
    zz_c3_setuptest_close_instrumentation_test.go:99: C3-INSTR TestUpdateClientConnState_NewRingSize(keeps b) built=1 closed=0 UNCLOSED=1
    zz_c3_setuptest_close_instrumentation_test.go:99: C3-INSTR TestAddrWeightChange(keeps b)                 built=1 closed=0 UNCLOSED=1
    zz_c3_setuptest_close_instrumentation_test.go:99: C3-INSTR TestAddrBalancerAttributesChange(keeps b)     built=1 closed=0 UNCLOSED=1
    zz_c3_setuptest_close_instrumentation_test.go:103: UNCLOSED: 7 balancer(s) constructed by setupTest never received Close after their test finished
--- FAIL: TestReproC3_NormalCompletion (0.10s)
    zz_c3_setuptest_close_instrumentation_test.go:113: C3-INSTR caller-exits-early-after-setupTest            built=1 closed=0 UNCLOSED=1
    zz_c3_setuptest_close_instrumentation_test.go:118: UNCLOSED: 1 balancer(s) constructed by setupTest never received Close on the caller's early-exit path
--- FAIL: TestReproC3_EarlyExitInCaller (0.01s)
    zz_c3_setuptest_close_instrumentation_test.go:129: UpdateClientConnState returned err: bad resolver state
    zz_c3_setuptest_close_instrumentation_test.go:128: C3-INSTR setupTest-t.Fatalf-after-Build                built=1 closed=0 UNCLOSED=1
    zz_c3_setuptest_close_instrumentation_test.go:133: UNCLOSED: 1 balancer(s) constructed by setupTest never received Close on setupTest's own t.Fatalf path
--- FAIL: TestReproC3_EarlyExitInsideSetupTest (0.00s)
    zz_c3_setuptest_close_instrumentation_test.go:141: C3-INSTR control-caller-calls-b.Close                  built=1 closed=1 UNCLOSED=0
--- PASS: TestReproC3_ControlExplicitCloseIsObserved (0.01s)
FAIL
FAIL	google.golang.org/grpc/balancer/ringhash	0.148s
FAIL
```

(The seven inner subtests, i.e. the real test bodies, all reported `--- PASS`; the outer repro tests fail on purpose to flag the unclosed balancers. `TestReproC3_EarlyExitInsideSetupTest` drives `setupTest`'s own `t.Fatalf` path after `Build` by passing no endpoints.)

- Normal completion, discarding callers: `TestOneEndpoint`, `TestThreeSubConnsAffinity`, `TestThreeBackendsAffinityMultiple`, `TestAutoConnectEndpointOnTransientFailure` each built 1 balancer and it received 0 Close calls.
- Normal completion, non-discarding callers: also 0 Close calls (they never call `b.Close()` either).
- Early exit in the caller right after `setupTest` returned (Goexit via `t.SkipNow`, same unwinding as `t.Fatalf`): 0 Close calls.
- Early exit inside `setupTest` after `Build` (`t.Fatalf("UpdateClientConnState returned err: bad resolver state")`): 0 Close calls.
- Control: when a caller does call `b.Close()`, the instrumentation reports `closed=1`, so the zeros are not an instrumentation artifact.

Same repro on the task base commit (`~/wt/base`, `bf9e7cd3`) gives the identical output (7 + 1 + 1 unclosed, control closed=1): the gap is pre-existing and unchanged by the solution.

The branch's own suite does not notice: `go test ./balancer/ringhash -run 'Test/OneEndpoint' -race -count=1 -v` prints `--- PASS: Test/OneEndpoint (0.01s)` (grpctest's leak check only looks at goroutines, and an unclosed ringhash balancer with lazy pick-first children owns none).

Replay: `verify/repro/run.sh c3`.

### Impact reasoning

Only test code is affected: every `setupTest`-built ringhash balancer (and its endpointsharding child plus per-endpoint wrappers) is abandoned without `Close` on every exit path, normal or early. Nothing fails today because no goroutine is left behind, so the leak checker stays green. The practical cost is that `ringhashBalancer.Close` -> `endpointSharding.Close` -> per-child close is never exercised by these unit tests (only by the separate e2e tests) - which matters for this task because the refactor rewrote exactly that close/locking path - and any future change that makes the balancer own a goroutine or timer would start tripping the leak checker in seven tests at once. Fix: `t.Cleanup(b.Close)` in `setupTest` right after the nil check.

## C4

Claim: on `evalon/grpc-go-en-78149e07` endpoint sharding publishes a parent state update from a completed child's callback while ResolverError delivery to another child remains blocked. Naming drift: `inhibitUpdatesFromChildren` / `allowUpdatesFromChildren` do not exist on this branch; suppression is the per-wrapper `balancerWrapper.inhibitUpdates atomic.Bool`.

### Source (suppression scope and callback routing on this branch)

```go
func (es *endpointSharding) ResolverError(err error) {
	es.updateMu.Lock()
	defer es.updateMu.Unlock()
	defer es.updateState()
	children := es.children.Load()
	for _, child := range children.All() {
		child.resolverError(err)
	}
}

func (bw *balancerWrapper) resolverError(err error) {
	bw.mu.Lock()
	defer bw.mu.Unlock()
	bw.inhibitUpdates.Store(true)
	defer bw.inhibitUpdates.Store(false)
	bw.child.ResolverError(err)
}

func (bw *balancerWrapper) UpdateState(state balancer.State) {
	...
	if bw.inhibitUpdates.Load() {
		return
	}
	bw.es.updateState()
}
```

There is no balancer-wide suppression flag on this branch (`grep -n inhibit balancer/endpointsharding/endpointsharding.go` lists only `inhibitUpdates` on `balancerWrapper`, lines 187-189, 364-368, 381, 407-408, 423-424), whereas the base commit has `es.inhibitChildUpdates` set for the whole `ResolverError`/`UpdateClientConnState` interval.

### Behavioral run (both parts)

`verify/repro/c4_resolver_error_early_publish_test.go.txt` (external test package): two stub children; the first child endpointsharding calls `ResolverError` on returns immediately, the second one is held. While it is held, the completed child calls `cc.UpdateState(TRANSIENT_FAILURE)`; a recording parent ClientConn counts publications and captures the publishing stack. `verify/repro/c4_suppression_scope_internal_test.go.txt` (internal package) reads each wrapper's `inhibitUpdates` at the same moment.

```sh
cd ~/wt/c4   # evalon/grpc-go-en-78149e07 @ 70be432f
cp ~/repos/grpc-go/verify/repro/c4_resolver_error_early_publish_test.go.txt balancer/endpointsharding/c4_resolver_error_early_publish_test.go
cp ~/repos/grpc-go/verify/repro/c4_suppression_scope_internal_test.go.txt balancer/endpointsharding/c4_suppression_scope_internal_test.go
go test ./balancer/endpointsharding -run '^TestReproC4' -race -count=1 -v
```

```console
=== RUN   TestReproC4Internal_SuppressionEndsPerChild
    c4_suppression_scope_internal_test.go:75: TRACE mid fan-out: child addr-a inhibitUpdates=true
    c4_suppression_scope_internal_test.go:75: TRACE mid fan-out: child addr-b inhibitUpdates=false
    c4_suppression_scope_internal_test.go:83: SUPPRESSION LIFTED EARLY: 1 of 2 children have inhibitUpdates=false while ResolverError fan-out is still in progress (1 still inhibited)
--- FAIL: TestReproC4Internal_SuppressionEndsPerChild (0.00s)
=== RUN   TestReproC4_ParentUpdatePublishedDuringResolverErrorFanout
    c4_resolver_error_early_publish_test.go:96: TRACE parent updates after initial UpdateClientConnState: 1 [CONNECTING]
    c4_resolver_error_early_publish_test.go:112: TRACE ResolverError returned for child "addr-a"; ResolverError HELD inside child "addr-b"; parent updates so far: 1
    c4_resolver_error_early_publish_test.go:122: TRACE callback UpdateState(TRANSIENT_FAILURE) from completed child "addr-a" returned
    c4_resolver_error_early_publish_test.go:134: TRACE parent updates published while child "addr-b" ResolverError is still held: 1
    c4_resolver_error_early_publish_test.go:137: TRACE early publication state=CONNECTING, published from:
        goroutine 36 [running]:
        runtime/debug.Stack()
        	/usr/local/go/src/runtime/debug/stack.go:26 +0x68
        google.golang.org/grpc/balancer/endpointsharding_test.(*c4ParentCC).UpdateState(0xc000134050, {0x1?, {0x13583a0?, 0xc000130180?}})
        	/home/ubuntu/wt/c4/balancer/endpointsharding/c4_resolver_error_early_publish_test.go:32 +0x1a7
        google.golang.org/grpc/balancer/endpointsharding.(*endpointSharding).updateState(0xc00013a000)
        	/home/ubuntu/wt/c4/balancer/endpointsharding/endpointsharding.go:313 +0xd54
        google.golang.org/grpc/balancer/endpointsharding.(*balancerWrapper).UpdateState(0xc00013e000, {0xc00013a000?, {0x13583e0?, 0xc0001101a0?}})
        	/home/ubuntu/wt/c4/balancer/endpointsharding/endpointsharding.go:384 +0x15a
        google.golang.org/grpc/balancer/endpointsharding_test.TestReproC4_ParentUpdatePublishedDuringResolverErrorFanout.func3()
        	/home/ubuntu/wt/c4/balancer/endpointsharding/c4_resolver_error_early_publish_test.go:117 +0xf4
        created by google.golang.org/grpc/balancer/endpointsharding_test.TestReproC4_ParentUpdatePublishedDuringResolverErrorFanout in goroutine 34
        	/home/ubuntu/wt/c4/balancer/endpointsharding/c4_resolver_error_early_publish_test.go:116 +0xa5d
    c4_resolver_error_early_publish_test.go:139: EARLY PUBLICATION: parent received 1 UpdateState call(s) before ResolverError fan-out finished, want 0
    c4_resolver_error_early_publish_test.go:148: TRACE parent updates published by the whole ResolverError operation: 2 (want exactly 1 consolidated update)
--- FAIL: TestReproC4_ParentUpdatePublishedDuringResolverErrorFanout (0.10s)
FAIL
FAIL	google.golang.org/grpc/balancer/endpointsharding	0.138s
FAIL
```

- Part "suppression scope": mid fan-out, the completed child's `inhibitUpdates` is already `false` while the held child's is `true`.
- Part "callback reachability": the completed child's callback goes `balancerWrapper.UpdateState` (endpointsharding.go:384) -> `endpointSharding.updateState` (endpointsharding.go:313) -> parent `UpdateState`, while the other child's `ResolverError` is still held; the ResolverError operation as a whole produced 2 parent updates instead of 1. (Which address is "completed" vs "held" varies with endpoint-map iteration order; the outcome is the same either way.)

### Controls: same behavioral test on the base commit and on the audited branch

```sh
for d in ~/wt/base ~/repos/grpc-go; do cd $d; cp ~/repos/grpc-go/verify/repro/c4_resolver_error_early_publish_test.go.txt balancer/endpointsharding/c4_resolver_error_early_publish_test.go; go test ./balancer/endpointsharding -run '^TestReproC4_' -race -count=1 -v 2>&1 | grep -E "TRACE (parent|ResolverError|callback)|EARLY|^(---|ok|FAIL|PASS)"; rm balancer/endpointsharding/c4_resolver_error_early_publish_test.go; done
```

```console
== control in /home/ubuntu/wt/base (bf9e7cd3)
    c4_resolver_error_early_publish_test.go:96: TRACE parent updates after initial UpdateClientConnState: 1 [CONNECTING]
    c4_resolver_error_early_publish_test.go:112: TRACE ResolverError returned for child "addr-b"; ResolverError HELD inside child "addr-a"; parent updates so far: 1
    c4_resolver_error_early_publish_test.go:122: TRACE callback UpdateState(TRANSIENT_FAILURE) from completed child "addr-b" returned
    c4_resolver_error_early_publish_test.go:134: TRACE parent updates published while child "addr-a" ResolverError is still held: 0
    c4_resolver_error_early_publish_test.go:148: TRACE parent updates published by the whole ResolverError operation: 1 (want exactly 1 consolidated update)
--- PASS: TestReproC4_ParentUpdatePublishedDuringResolverErrorFanout (0.10s)
PASS
ok  	google.golang.org/grpc/balancer/endpointsharding	1.116s
== control in /home/ubuntu/repos/grpc-go (0d0f6fee)
    c4_resolver_error_early_publish_test.go:96: TRACE parent updates after initial UpdateClientConnState: 1 [CONNECTING]
    c4_resolver_error_early_publish_test.go:112: TRACE ResolverError returned for child "addr-b"; ResolverError HELD inside child "addr-a"; parent updates so far: 1
    c4_resolver_error_early_publish_test.go:122: TRACE callback UpdateState(TRANSIENT_FAILURE) from completed child "addr-b" returned
    c4_resolver_error_early_publish_test.go:134: TRACE parent updates published while child "addr-a" ResolverError is still held: 0
    c4_resolver_error_early_publish_test.go:148: TRACE parent updates published by the whole ResolverError operation: 1 (want exactly 1 consolidated update)
--- PASS: TestReproC4_ParentUpdatePublishedDuringResolverErrorFanout (0.10s)
PASS
ok  	google.golang.org/grpc/balancer/endpointsharding	1.127s
```

Both the pre-change code and the audited `-perfect` branch publish 0 updates mid fan-out and exactly 1 consolidated update; the claim branch publishes 1 early and 2 in total.

### Eval fixture on the claim branch

```sh
cd ~/wt/c4
cp ~/eval/tests/eval_endpointsharding_test.go balancer/endpointsharding/     # byte-exact from eval_tests.zip
go test ./balancer/endpointsharding -run 'TestEval_' -race -count=1 2>&1 | tail -30
go test ./balancer/endpointsharding -run '^TestEval_ResolverErrorConsolidatedNotification$' -race -count=1 -v 2>&1 | tail -4
```

```console
--- FAIL: TestEval_ChildStateExitIdleCallback (0.00s)
    eval_endpointsharding_test.go:1842: Expected direct callable ExitIdle on ChildState: ChildState retains facade wrapper field "Balancer" of type endpointsharding.ExitIdler exposing ExitIdle
--- FAIL: TestEval_BatchUpdateInhibition (0.00s)
    --- FAIL: TestEval_BatchUpdateInhibition/SuccessfulBatchConsolidatedNotification (0.00s)
        eval_endpointsharding_test.go:1984: Expected 0 parent updates after concurrent mid-batch child callback from 10.0.0.2, got 1 (intermediate callback leaked mid-batch)
        eval_endpointsharding_test.go:2004: Expected exactly 1 consolidated update after batch completion, got 2
        eval_endpointsharding_test.go:2022: Expected exactly 2 updates after post-batch child state change, got 3
FAIL
FAIL	google.golang.org/grpc/balancer/endpointsharding	0.810s
FAIL
```

```console
=== RUN   TestEval_ResolverErrorConsolidatedNotification
--- PASS: TestEval_ResolverErrorConsolidatedNotification (0.00s)
PASS
ok  	google.golang.org/grpc/balancer/endpointsharding	1.016s
```

The fixture catches the same per-child-suppression defect on the `UpdateClientConnState` path (`TestEval_BatchUpdateInhibition`: "intermediate callback leaked mid-batch"), but its ResolverError check stays green on this branch: `TestEval_ResolverErrorConsolidatedNotification` only issues child callbacks from inside each child's own `ResolverError` call (when that child is still inhibited), never from an already-completed child, so it does not measure the behavior this claim is about. The verdict rests on the direct behavioral run above, not on the fixture.

Replay: `verify/repro/run.sh c4`.

### Impact reasoning

`ResolverError` (and, per the fixture, `UpdateClientConnState`) is documented on this very branch as sending "a single synchronous update of the childStates at the end of the ResolverError operation". With per-child suppression, any child that reports state after its own call returned - which is the normal case for real children such as pick-first, whose SubConn state changes arrive asynchronously - makes endpointsharding publish an intermediate aggregated picker built from a half-processed child set, and the parent (ringhash, weighted round robin, etc.) sees extra picker churn and a transient aggregate that mixes pre-error and post-error children. This needs only two endpoints and one slow child; no contrived configuration. It is a behavior regression relative to the base commit, where suppression spans the whole fan-out. Fix: keep a balancer-wide inhibit flag for the duration of `UpdateClientConnState`/`ResolverError` (as on the base and on the audited branch), independent of per-child locking, and add a regression that issues a callback from a completed child while another child's call is held.

## C5

Claim: on `evalon/grpc-go-en-33ac8ac2` the child-update contention regression satisfies its update-entry barrier with a setup notification before the later update reaches the contested path. Naming drift: `maintainedChild`, `enterUpdate`, `TestDecoupledChildProgress` do not exist on this branch; the regression is `TestChildStateExitIdle_NotBlockedByOtherChildUpdate` and the helpers are `newBlockingChildBalancers` / `awaitEntered` / `updateEntered` in `balancer/endpointsharding/endpointsharding_ext_test.go`.

### Test text (the two parts)

Part "stale signal production" - every `UpdateClientConnState` on every child, including the initial setup update, sends on the 10-slot buffered `updateEntered` channel (line 386), and nothing drains it after setup:

```go
		updateEntered:   make(chan string, 10),
...
		UpdateClientConnState: func(bd *stub.BalancerData, ccs balancer.ClientConnState) error {
			addr := ccs.ResolverState.Endpoints[0].Addresses[0].Addr
			b.updateEntered <- addr
			if addr == b.blockedAddr && b.blockUpdates.Load() {
				<-b.unblock
			}
```

Part "stale signal consumption" - the test does the setup update (2 notifications: "blocked", "unblocked"), then starts the contending update in a goroutine and immediately waits on the same channel (line 504):

```go
	if err := es.UpdateClientConnState(ccs); err != nil {          // setup
	...
	children.blockUpdates.Store(true)
	updateDone := make(chan error, 1)
	go func() { updateDone <- es.UpdateClientConnState(ccs) }()    // later, contending update
	awaitEntered(ctx, t, children.updateEntered, "blocked")        // barrier
	states["unblocked"].ExitIdle()
	awaitEntered(ctx, t, children.exitIdleEntered, "unblocked")
```

### Demonstration 1 - hold the later update, production code untouched (deterministic)

`verify/repro/c5_stale_update_barrier_test.go.txt` is a copy of the branch test using the branch's own helpers. Only change: the goroutine issuing the later `UpdateClientConnState` waits on a `holdLater` channel before calling into endpointsharding, and is released only after the barrier and the ExitIdle assertion have completed (invocation-specific tracing via `laterCalled`).

```sh
cd ~/wt/c5   # evalon/grpc-go-en-33ac8ac2 @ aff9f7ba
cp ~/repos/grpc-go/verify/repro/c5_stale_update_barrier_test.go.txt balancer/endpointsharding/c5_stale_update_barrier_test.go
go test ./balancer/endpointsharding -run 'Test/ReproC5' -race -count=20 -v 2>&1 | grep -E "TRACE|--- (PASS|FAIL): Test/|^(ok|FAIL|PASS)" | sort | uniq -c
```

```console
     20     --- PASS: Test/ReproC5_UpdateBarrierSatisfiedBySetup (0.00s)
     20     c5_stale_update_barrier_test.go:46: TRACE after SETUP update only: len(updateEntered)=2 (stale setup notifications left in the channel)
     11     c5_stale_update_barrier_test.go:63: TRACE barrier awaitEntered(updateEntered, "blocked") RETURNED while the later UpdateClientConnState has not even been called; len(updateEntered)=0
      9     c5_stale_update_barrier_test.go:63: TRACE barrier awaitEntered(updateEntered, "blocked") RETURNED while the later UpdateClientConnState has not even been called; len(updateEntered)=1
     20     c5_stale_update_barrier_test.go:68: TRACE ExitIdle assertion completed with no update in progress on any child (nothing was contended)
      1 PASS
      1 ok  	google.golang.org/grpc/balancer/endpointsharding	1.049s
```

20/20: setup leaves 2 notifications in `updateEntered`; the barrier returns while the later update has not been issued at all, so it can only have consumed a setup notification (the channel length drops from 2 to 1 or 0 depending on the randomized endpoint order); the "not blocked" assertion then passes with nothing blocked.

### Demonstration 2 - re-coupled production code, branch test unmodified

`verify/repro/c5_mutation_global_lock.patch` makes `balancerWrapper.exitIdle` take the balancer-wide `es.updateMu` (the pre-fix coupling: an ExitIdle for any child waits behind an in-flight `UpdateClientConnState`). A regression that really waited for the later update to be blocked inside child "blocked" would fail 100% of the time against this mutant. The branch's own test, unmodified:

```sh
cd ~/wt/c5
go test ./balancer/endpointsharding -run 'Test/ChildStateExitIdle_NotBlockedByOtherChildUpdate' -race -count=20 | tail -1     # unmutated baseline: ok
git apply ~/repos/grpc-go/verify/repro/c5_mutation_global_lock.patch
go test -c -race -o /tmp/c5mut.test ./balancer/endpointsharding
seq 1 48 | xargs -P 8 -I{} sh -c '/tmp/c5mut.test -test.run "Test/ChildStateExitIdle_NotBlockedByOtherChildUpdate" -test.count=1 -test.v -test.timeout=25s > /tmp/c5m_{}.txt 2>&1; echo "rc=$?"' | sort | uniq -c
grep -h -E "Timeout waiting|--- (PASS|FAIL): Test/|panic: test timed out" /tmp/c5m_*.txt | sed 's/ ([0-9.]*s)//' | sort | uniq -c
```

```console
     31 rc=0
     17 rc=2
     31     --- PASS: Test/ChildStateExitIdle_NotBlockedByOtherChildUpdate
     17     endpointsharding_ext_test.go:507: Timeout waiting for child "unblocked" to be called
     17 panic: test timed out after 25s
```

31 of 48 runs PASS against an implementation in which children are coupled again: the stale barrier lets the test's ExitIdle goroutine race ahead of the later update's lock acquisition. (Side observation from the 17 detecting runs: after `t.Fatalf` the deferred `es.Close()` blocks on `updateMu` forever because `children.unblock` is never closed on the failure path, so a detected failure hangs until the test binary's timeout - 25s here, 10 minutes by default.)

Replay: `verify/repro/run.sh c5` and `verify/repro/run.sh c5-mutant`. A second, independent 48-run pass through `run.sh c5-mutant` printed:

```console
     29     --- PASS: Test/ChildStateExitIdle_NotBlockedByOtherChildUpdate
     19     endpointsharding_ext_test.go:507: Timeout waiting for child "unblocked" to be called
     19 panic: test timed out after 25s
```

### Impact reasoning

This test is the task's required regression coverage ("showing that one child can make progress while another child's operation is blocked"). Because its barrier is satisfied by leftovers from setup, the test does not establish that anything is blocked when it asserts progress; it passes with the contending update not even started, and it passes roughly three times out of five (31/48, 29/48) against a build where the original cross-child blocking is reintroduced. A regression of the very behavior the task fixes would therefore usually go unnoticed in CI. The sibling test `TestChildStateExitIdle_NotBlockedByOtherChildExitIdle` uses a separate channel (`exitIdleEntered`) that has no setup traffic and is not affected. Fix: drain `updateEntered` after the setup update (or only signal once `blockUpdates` is set / signal on a dedicated "blocked" channel right before `<-b.unblock`), and close `children.unblock` in a deferred `sync.Once` so a detected failure does not hang.
