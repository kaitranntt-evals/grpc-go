# Evidence — audit run v-18bc142f (endpointsharding decoupled locking)

Environment: Go toolchain from the repository (`go test` with `-race`), Staticcheck `2026.1 (v0.7.0)` (the version pinned by `test/tools/go.mod`, `honnef.co/go/tools v0.7.0`). Each claim-target branch was fetched from the claim repository remote `evalon` (`https://github.com/kaitranntt-evals/grpc-go-endpointsharding-decouple-locking.git`) into its own worktree under `~/wt/<8-char-id>` (e.g. `~/wt/a9c81aee` = `evalon/grpc-go-en-a9c81aee`). "Main checkout" below is the audited branch (`origin/grpc-go-endpointsharding-decouple-locking-perfect`, commit `81201fc0`) in `~/repos/grpc-go`. The eval fixture `tests/eval_endpointsharding_test.go` from `eval_tests.zip` was copied byte-exactly (verified with `cmp`) to `balancer/endpointsharding/eval_endpointsharding_test.go` wherever it was run. Repro files live in `verify/repro/` and are build-tagged `verifyrepro` so they never compile into normal test runs; each has a one-line run instruction at the top.

Sanity check on the main checkout (all committed tests and the fixture pass, and the full build succeeds):

```sh
cd ~/repos/grpc-go
go test ./balancer/endpointsharding ./balancer/ringhash -race -count=1
go build ./... && echo BUILD_OK
```

```console
ok  	google.golang.org/grpc/balancer/endpointsharding	2.030s
ok  	google.golang.org/grpc/balancer/ringhash	13.127s
BUILD_OK
```

---

## C1

**Claim:** changed concurrency tests in `balancer/endpointsharding` or `balancer/ringhash` can hang during cleanup after an assertion/deadline failure because cleanup waits for a still-blocked background worker. Adjudicated independently on 17 branches.

**Scope check.** On every C1 branch the only changed `balancer/ringhash` test file is `picker_test.go`, and the change is a pure API adaptation (`fakeExitIdler`/`balancer:` → `exitIdle: testSC.Connect` or equivalent) with no background workers, so the only concurrency tests to exercise are the endpointsharding ones:

```sh
cd ~/wt; for b in 749f0e47 dbf8f9ce f18f8972 d2baf15c aa68b6dc 20dcda4c 74259263 aa3a022f a9c81aee 3c1b9630 4df3f0b6 b1c8c194 7d05cdff 3d710a4c 995cebaa 66007695 9481a94a; do echo "== $b: $(git -C $b diff --name-only $(git -C $b merge-base HEAD origin/master) -- balancer/ringhash balancer/endpointsharding | grep _test.go | tr '\n' ' ')"; done
```

```console
== 749f0e47: balancer/endpointsharding/endpointsharding_concurrency_test.go balancer/ringhash/picker_test.go
== dbf8f9ce: balancer/endpointsharding/endpointsharding_ext_test.go
== f18f8972: balancer/endpointsharding/endpointsharding_ext_test.go
== d2baf15c: balancer/endpointsharding/endpointsharding_ext_test.go balancer/ringhash/picker_test.go
== aa68b6dc: balancer/endpointsharding/endpointsharding_ext_test.go balancer/ringhash/picker_test.go
== 20dcda4c: balancer/endpointsharding/endpointsharding_ext_test.go
== 74259263: balancer/endpointsharding/endpointsharding_ext_test.go
== aa3a022f: balancer/endpointsharding/endpointsharding_child_test.go
== a9c81aee: balancer/endpointsharding/endpointsharding_concurrency_test.go balancer/ringhash/picker_test.go
== 3c1b9630: balancer/endpointsharding/endpointsharding_concurrency_test.go balancer/ringhash/picker_test.go
== 4df3f0b6: balancer/endpointsharding/endpointsharding_ext_test.go
== b1c8c194: balancer/endpointsharding/concurrency_test.go balancer/ringhash/picker_test.go
== 7d05cdff: balancer/endpointsharding/concurrency_ext_test.go balancer/ringhash/picker_test.go
== 3d710a4c: balancer/endpointsharding/concurrency_test.go balancer/ringhash/picker_test.go
== 995cebaa: balancer/endpointsharding/endpointsharding_concurrency_test.go balancer/ringhash/picker_test.go
== 66007695: balancer/endpointsharding/endpointsharding_concurrency_test.go balancer/ringhash/picker_test.go
== 9481a94a: balancer/endpointsharding/concurrency_test.go
```

**Method (behavioral).** For each branch, the "ExitIdle while another child's update is blocked" test was located, and a `t.Fatal(...)` was injected immediately after the line where the test has confirmed that the background `UpdateClientConnState` worker is parked inside the blocked child (i.e. the exact state the claim describes: assertion failure while a worker is still blocked). The test was then run with `-race -timeout 60s`. A cleanup hang would surface as `panic: test timed out after 60s`; a safe cleanup shows `FAIL` within milliseconds. The file was restored afterwards (`git status --short` empty). Helper scripts (kept outside the repo, reproduced here):

```sh
# ~/inject.sh <branch-dir> <test-file> <line-after-which-to-insert> <go-test-run-pattern> [timeout]
set -u; b=$1; f=$2; line=$3; pat=$4; to=${5:-60s}
cd ~/wt/$b || exit 1
cp "$f" /tmp/inject_backup.go
sed -i "${line}a\\	t.Fatal(\"INJECTED: assertion failure while a worker is still blocked\")" "$f"
echo "--- injected after line $line:"; sed -n "$((line-1)),$((line+1))p" "$f"
start=$(date +%s)
go test ./$(dirname "$f") -run "$pat" -race -count=1 -timeout "$to" -v 2>&1 | grep -vE '^\s*$' | grep -E "INJECTED|^--- |^FAIL|^ok|^PASS|panic: test timed out|goroutine .*\[|endpointsharding.*\.go:[0-9]+|\.Close|childMu|\.Lock" | head -60
echo "--- elapsed: $(( $(date +%s) - start ))s"
cp /tmp/inject_backup.go "$f"; git status --short
```

```sh
# ~/c1_matrix.sh — <branch> <file> <inject-after-line> <run pattern>
while read -r b f line pat; do echo "########## branch $b  file $f  inject-after-line $line  run $pat"; ~/inject.sh "$b" "$f" "$line" "$pat" 60s; done <<'EOF'
749f0e47 balancer/endpointsharding/endpointsharding_concurrency_test.go 168 Test/ExitIdleDuringOtherChildUpdate
dbf8f9ce balancer/endpointsharding/endpointsharding_ext_test.go 479 Test/EndpointShardingExitIdleWhileOtherChildUpdateBlocked
f18f8972 balancer/endpointsharding/endpointsharding_ext_test.go 463 Test/EndpointShardingExitIdleDuringUnrelatedChildUpdate
d2baf15c balancer/endpointsharding/endpointsharding_ext_test.go 488 Test/EndpointShardingExitIdleWhileOtherChildBlocked
aa68b6dc balancer/endpointsharding/endpointsharding_ext_test.go 516 Test/EndpointShardingExitIdleNotBlockedByOtherChildUpdate
20dcda4c balancer/endpointsharding/endpointsharding_ext_test.go 467 Test/EndpointShardingExitIdleWhileOtherChildBlocked
74259263 balancer/endpointsharding/endpointsharding_ext_test.go 507 Test/EndpointShardingExitIdleNotBlockedByOtherChildUpdate
aa3a022f balancer/endpointsharding/endpointsharding_child_test.go 237 Test/EndpointShardingExitIdleWhileOtherChildUpdateBlocked
a9c81aee balancer/endpointsharding/endpointsharding_concurrency_test.go 118 Test/ExitIdleDuringOtherChildUpdate
3c1b9630 balancer/endpointsharding/endpointsharding_concurrency_test.go 134 Test/EndpointShardingExitIdleDuringUpdate
4df3f0b6 balancer/endpointsharding/endpointsharding_ext_test.go 444 Test/EndpointShardingExitIdleDuringOtherChildUpdate
b1c8c194 balancer/endpointsharding/concurrency_test.go 145 Test/ChildExitIdleWhileOtherChildUpdateBlocked
7d05cdff balancer/endpointsharding/concurrency_ext_test.go 133 Test/ChildExitIdleDuringOtherChildUpdate
3d710a4c balancer/endpointsharding/concurrency_test.go 140 Test/ChildExitIdleDuringOtherChildUpdate
995cebaa balancer/endpointsharding/endpointsharding_concurrency_test.go 140 Test/ChildExitIdleWhileAnotherChildUpdateBlocked
66007695 balancer/endpointsharding/endpointsharding_concurrency_test.go 128 Test/ChildExitIdleWhileOtherChildUpdating
9481a94a balancer/endpointsharding/concurrency_test.go 169 Test/ExitIdleDuringOtherChildUpdate
EOF
```

Example of the injection context (branch `749f0e47`; the injected line sits right after the test has received the "worker is blocked" signal):

```go
		t.Fatal("timed out waiting for a child's update to block")
	}
	t.Fatal("INJECTED: assertion failure while a worker is still blocked")
```

**Result (`~/c1_matrix.sh 2>&1 | tee ~/c1_matrix.log`).** Every one of the 17 runs failed on the injected assertion and the package finished in ≈15 ms wall time; none reached the 60 s timeout, and no `panic: test timed out` or goroutine dump appeared anywhere in the log:

```console
########## branch 749f0e47 ...  endpointsharding_concurrency_test.go:169: INJECTED ...  FAIL	google.golang.org/grpc/balancer/endpointsharding	0.015s  --- elapsed: 0s
########## branch dbf8f9ce ...  endpointsharding_ext_test.go:480: INJECTED ...          FAIL	google.golang.org/grpc/balancer/endpointsharding	0.016s  --- elapsed: 1s
########## branch f18f8972 ...  endpointsharding_ext_test.go:464: INJECTED ...          FAIL	google.golang.org/grpc/balancer/endpointsharding	0.015s  --- elapsed: 0s
########## branch d2baf15c ...  endpointsharding_ext_test.go:489: INJECTED ...          FAIL	google.golang.org/grpc/balancer/endpointsharding	0.066s  --- elapsed: 1s
########## branch aa68b6dc ...  endpointsharding_ext_test.go:517: INJECTED ...          FAIL	google.golang.org/grpc/balancer/endpointsharding	0.015s  --- elapsed: 0s
########## branch 20dcda4c ...  endpointsharding_ext_test.go:468: INJECTED ...          FAIL	google.golang.org/grpc/balancer/endpointsharding	0.015s  --- elapsed: 1s
########## branch 74259263 ...  endpointsharding_ext_test.go:508: INJECTED ...          FAIL	google.golang.org/grpc/balancer/endpointsharding	0.014s  --- elapsed: 0s
########## branch aa3a022f ...  endpointsharding_child_test.go:238: INJECTED ...        FAIL	google.golang.org/grpc/balancer/endpointsharding	0.017s  --- elapsed: 1s
########## branch a9c81aee ...  endpointsharding_concurrency_test.go:119: INJECTED ...  FAIL	google.golang.org/grpc/balancer/endpointsharding	0.016s  --- elapsed: 0s
########## branch 3c1b9630 ...  endpointsharding_concurrency_test.go:135: INJECTED ...  FAIL	google.golang.org/grpc/balancer/endpointsharding	0.016s  --- elapsed: 1s
########## branch 4df3f0b6 ...  endpointsharding_ext_test.go:445: INJECTED ...          FAIL	google.golang.org/grpc/balancer/endpointsharding	0.016s  --- elapsed: 0s
########## branch b1c8c194 ...  concurrency_test.go:146: INJECTED ...                   FAIL	google.golang.org/grpc/balancer/endpointsharding	0.016s  --- elapsed: 1s
########## branch 7d05cdff ...  concurrency_ext_test.go:134: INJECTED ...               FAIL	google.golang.org/grpc/balancer/endpointsharding	0.016s  --- elapsed: 0s
########## branch 3d710a4c ...  concurrency_test.go:141: INJECTED ...                   FAIL	google.golang.org/grpc/balancer/endpointsharding	0.015s  --- elapsed: 1s
########## branch 995cebaa ...  endpointsharding_concurrency_test.go:141: INJECTED ...  FAIL	google.golang.org/grpc/balancer/endpointsharding	0.014s  --- elapsed: 0s
########## branch 66007695 ...  endpointsharding_concurrency_test.go:129: INJECTED ...  FAIL	google.golang.org/grpc/balancer/endpointsharding	0.015s  --- elapsed: 1s
########## branch 9481a94a ...  concurrency_test.go:170: INJECTED ...                   FAIL	google.golang.org/grpc/balancer/endpointsharding	0.014s  --- elapsed: 0s
```

```sh
grep -c "panic: test timed out" ~/c1_matrix.log   # -> 0
```

**Why cleanup does not hang (per branch, from the test source near the injection point).** Most branches release the blocked worker unconditionally on the way out (`sync.OnceFunc(func(){ close(release) })` invoked from `defer`/`t.Cleanup` — e.g. `749f0e47:114`, `f18f8972:409`, `aa68b6dc:467-468`, `20dcda4c:391-392`, `74259263:489`, `a9c81aee:68`, `3c1b9630:75,124`, `4df3f0b6:388`, `b1c8c194:140-141`, `7d05cdff:67`, `3d710a4c:85`, `995cebaa:71`, `66007695:81`, `9481a94a:109`; `d2baf15c:509 close(unblock)`). On the two branches whose test only has a `defer es.Close()` (`dbf8f9ce:416`, `aa3a022f:216`) the observed run still terminated in 16–17 ms, i.e. on those implementations `Close` does not need the lock the blocked worker holds (per-child locking), so the deferred `Close` is not dependent on the blocked worker. Either way, the *observed* behavior on all 17 branches is prompt termination after the failure.

**Verdict per branch:** REFUTED on all 17 branches (`749f0e47`, `dbf8f9ce`, `f18f8972`, `d2baf15c`, `aa68b6dc`, `20dcda4c`, `74259263`, `aa3a022f`, `a9c81aee`, `3c1b9630`, `4df3f0b6`, `b1c8c194`, `7d05cdff`, `3d710a4c`, `995cebaa`, `66007695`, `9481a94a`). Note the deadline-expiry path of each test is the same code path as the injected `t.Fatal` (the injected call sits at/after the test's own `t.Fatal("timed out waiting …")`), so the same cleanup runs.

---

## C2

**Claim:** on `evalon/grpc-go-en-385e4622` the per-child mutex comment prohibits simultaneous child+parent mutex ownership, yet the synchronous child `UpdateState` callback path acquires the parent mutex while the child mutex is held.

**Comment text (source, `~/wt/385e4622/balancer/endpointsharding/endpointsharding.go`):**

```go
// (endpointSharding, lines 110-115)
	// mu synchronizes access to the state stored in balancerWrappers in the
	// children field. mu must not be held during calls into a child since
	// synchronous calls back from the child may require taking mu, causing a
	// deadlock. To avoid deadlocks, do not acquire a balancerWrapper's mu
	// while holding mu.
	mu sync.Mutex
```

```go
// (balancerWrapper, lines 329-333)
	// mu synchronizes all calls into the child balancer. It is per-child so
	// that a call into one child (e.g. ExitIdle) never waits for an unrelated
	// child's call to complete. mu must not be held while holding es.mu, and
	// callbacks from the child (UpdateState) must not acquire it.
	mu sync.Mutex
```

```go
// (lines 347-355 and 365-377)
func (bw *balancerWrapper) UpdateState(state balancer.State) {
	bw.es.mu.Lock()
	bw.childState.State = state
	bw.es.mu.Unlock()
	...
}
func (bw *balancerWrapper) exitIdle() {
	bw.mu.Lock()            // child mu held while calling into the child
	...
func (bw *balancerWrapper) updateClientConnState(ccs balancer.ClientConnState) error {
	bw.mu.Lock()            // child mu held while calling child.UpdateClientConnState
```

The comment says "`mu` [child] must not be held while holding `es.mu`", but every call into the child (`updateClientConnState`, `exitIdle`, …) holds `bw.mu`, and a child that calls `cc.UpdateState(...)` synchronously from inside that call runs `bw.UpdateState`, which takes `bw.es.mu` — so `bw.mu` is held while `es.mu` is acquired.

**Behavioral probe** (`verify/repro/c2_lock_nesting_probe_test.go`; a stub child calls `cc.UpdateState` synchronously from `UpdateClientConnState`; inside the callback the probe checks `bw.mu.TryLock()` (false ⇒ held) and `es.mu.TryLock()` (true ⇒ free, then released) and then lets `bw.UpdateState` run):

```sh
cd ~/wt/385e4622 && cp ~/repos/grpc-go/verify/repro/c2_lock_nesting_probe_test.go balancer/endpointsharding/ && go test -tags verifyrepro ./balancer/endpointsharding -run '^TestC2LockNestingProbe$' -race -count=1 -v
```

```console
=== RUN   TestC2LockNestingProbe
    c2_lock_nesting_probe_test.go:59: during synchronous child UpdateState callback: balancerWrapper.mu held = true, es.mu free before callback = true
    c2_lock_nesting_probe_test.go:65: bw.UpdateState acquired es.mu while bw.mu was held (nested ownership bw.mu -> es.mu observed)
--- PASS: TestC2LockNestingProbe (0.00s)
PASS
ok  	google.golang.org/grpc/balancer/endpointsharding	1.014s
```

(The probe "passes" when it observes the nesting; it is written to fail if the nesting were absent.) The `-race` run also shows the nesting `bw.mu -> es.mu` is the *required* ordering for the synchronous callback to complete (the callback does complete; there is no deadlock because `es.mu` is never held while calling into a child). So the implementation is internally consistent, but the comment's prohibition ("mu must not be held while holding es.mu, and callbacks from the child (UpdateState) must not acquire it") contradicts the ownership sequence that `UpdateState` necessarily performs.

**Impact reasoning.** The comment is the lock-order documentation future maintainers will rely on; it states the opposite of the real invariant (real: `es.mu` must never be held while calling into a child / `bw.mu` may be held when `es.mu` is taken). A maintainer who "fixes" code to obey the comment (e.g. dropping `bw.mu` before a child call, or refusing to take `es.mu` in `UpdateState`) would introduce real races.

**Verdict:** CONFIRMED.

---

## C3

**Claim:** on `evalon/grpc-go-en-a9c81aee` an automatic ExitIdle generated by a synchronous Idle report during child construction can execute before the child's initial configuration and is not retried afterwards.

**Relevant source (`~/wt/a9c81aee/balancer/endpointsharding/endpointsharding.go:161-172`).** The child mutex is released *between* construction and initial configuration:

```go
			childBalancer.childState.ExitIdle = childBalancer.ExitIdle
			// Build may synchronously report IDLE, queuing an ExitIdle call.
			// Keep it blocked until the child has been assigned.
			childBalancer.childMu.Lock()
			childBalancer.child = es.childBuilder(childBalancer, es.bOpts)
			childBalancer.childMu.Unlock()          // <-- gap: queued ExitIdle goroutine can run here
		}
		newChildren.Set(endpoint, childBalancer)
		if err := childBalancer.updateClientConnState(balancer.ClientConnState{
```

For comparison, the main checkout keeps `childMu` held through the initial configuration (`epState.childMu.Lock()` … `updateClientConnStateLocked(ccs)` … `epState.childMu.Unlock()`, `endpointsharding.go:164-180`), so no such gap exists there.

**Part 1 — Premature dispatch (deterministic).** Instrumentation `verify/repro/c3_construction_gap_hook_a9c81aee.patch` adds a test-only hook `testHookAfterChildBuilt` invoked right after `childMu.Unlock()`; the probe `verify/repro/c3_construction_gap_deterministic_test.go` uses a child that reports Idle synchronously from its builder and records `configured` at each `ExitIdle`, and inside the hook waits (≤2 s) for the queued ExitIdle to arrive:

```sh
cd ~/wt/a9c81aee && git apply ~/repos/grpc-go/verify/repro/c3_construction_gap_hook_a9c81aee.patch && cp ~/repos/grpc-go/verify/repro/c3_construction_gap_deterministic_test.go balancer/endpointsharding/ && go test -tags verifyrepro ./balancer/endpointsharding -run '^TestC3ConstructionGapDeterministic$' -race -count=3 -v
```

```console
=== RUN   TestC3ConstructionGapDeterministic
    c3_construction_gap_deterministic_test.go:50: in construction gap: child.ExitIdle() delivered with configured=false
    c3_construction_gap_deterministic_test.go:79: construction-time ExitIdle executed before initial configuration (no-op on unconfigured child) and was NOT retried after configuration
--- FAIL: TestC3ConstructionGapDeterministic (2.01s)
=== RUN   TestC3ConstructionGapDeterministic
    c3_construction_gap_deterministic_test.go:50: in construction gap: child.ExitIdle() delivered with configured=false
    c3_construction_gap_deterministic_test.go:79: construction-time ExitIdle executed before initial configuration (no-op on unconfigured child) and was NOT retried after configuration
--- FAIL: TestC3ConstructionGapDeterministic (2.01s)
=== RUN   TestC3ConstructionGapDeterministic
    c3_construction_gap_deterministic_test.go:50: in construction gap: child.ExitIdle() delivered with configured=false
    c3_construction_gap_deterministic_test.go:79: construction-time ExitIdle executed before initial configuration (no-op on unconfigured child) and was NOT retried after configuration
--- FAIL: TestC3ConstructionGapDeterministic (2.01s)
FAIL
FAIL	google.golang.org/grpc/balancer/endpointsharding	6.042s
```

3/3: `ExitIdle` reached the child with `configured=false` while the parent was paused in the gap. (Afterwards `git checkout -- balancer/endpointsharding/endpointsharding.go` restored the worktree; `git status --short` empty.)

**Part 2 — Lost reconnection request.** Same run: after the parent completed `UpdateClientConnState` (child `configured=true`), no second `ExitIdle` arrived within 2 s (`... and was NOT retried after configuration`). Nothing in `updateClientConnState`/`updateClientConnStateLocked` on this branch re-issues a pending ExitIdle.

**Uninstrumented confirmation (no patch, scheduler-driven).** `verify/repro/c3_construction_exitidle_probe_test.go` runs 200 iterations of the same scenario with a 5 ms sleep between construction and configuration and tallies outcomes:

```sh
cd ~/wt/a9c81aee && cp ~/repos/grpc-go/verify/repro/c3_construction_exitidle_probe_test.go balancer/endpointsharding/ && go test -tags verifyrepro ./balancer/endpointsharding -run '^TestC3ConstructionExitIdleProbe$' -race -count=3 -v
```

```console
    c3_construction_exitidle_probe_test.go:81: iterations=200 ExitIdle-after-config=199 ExitIdle-before-config-not-retried=1 ExitIdle-before-config-then-retried=0 no-ExitIdle=0
    c3_construction_exitidle_probe_test.go:84: 1/200 runs: automatic ExitIdle ran on the unconfigured child and was never retried after configuration
--- FAIL: TestC3ConstructionExitIdleProbe (21.21s)
    c3_construction_exitidle_probe_test.go:81: iterations=200 ExitIdle-after-config=200 ExitIdle-before-config-not-retried=0 ExitIdle-before-config-then-retried=0 no-ExitIdle=0
--- PASS: TestC3ConstructionExitIdleProbe (21.21s)
    c3_construction_exitidle_probe_test.go:81: iterations=200 ExitIdle-after-config=199 ExitIdle-before-config-not-retried=1 ExitIdle-before-config-then-retried=0 no-ExitIdle=0
    c3_construction_exitidle_probe_test.go:84: 1/200 runs: automatic ExitIdle ran on the unconfigured child and was never retried after configuration
--- FAIL: TestC3ConstructionExitIdleProbe (21.21s)
FAIL
FAIL	google.golang.org/grpc/balancer/endpointsharding	63.651s
```

Together with an earlier identical run (`... ExitIdle-before-config-not-retried=1 ...`), 3 of 800 uninstrumented iterations on the target lost the reconnect. Same probe on the main checkout (`cd ~/repos/grpc-go && cp verify/repro/c3_construction_exitidle_probe_test.go balancer/endpointsharding/ && go test -tags verifyrepro ./balancer/endpointsharding -run '^TestC3ConstructionExitIdleProbe$' -race -count=1 -v`):

```console
    c3_construction_exitidle_probe_test.go:81: iterations=200 ExitIdle-after-config=200 ExitIdle-before-config-not-retried=0 ExitIdle-before-config-then-retried=0 no-ExitIdle=0
--- PASS: TestC3ConstructionExitIdleProbe
```

**Fixture check.** The eval fixture's `TestEval_ConstructionIdleCallbackSafety` passes 5/5 on the target (`cd ~/wt/a9c81aee && cp ~/eval_tests/tests/eval_endpointsharding_test.go balancer/endpointsharding/ && go test ./balancer/endpointsharding -run '^TestEval_ConstructionIdleCallbackSafety$' -race -count=5 -v` → `--- PASS: TestEval_ConstructionIdleCallbackSafety (0.05s)` ×5, `ok`). That fixture only asserts that the automatic reconnect is *delivered at some point* (`case <-reconnectCalled`) and that calls do not overlap; its child reconnects regardless of configuration, so it does not detect a pre-configuration no-op — consistent with the probe results.

**Impact reasoning.** A child balancer (e.g. pick_first) that reports IDLE synchronously from `Build` and cannot connect until it has addresses would, in the losing schedule, receive `ExitIdle()` with no addresses (no-op) and never receive another one; the endpoint stays IDLE until some later external event (a resolver update or a picker-triggered ExitIdle) happens. The window is narrow (3/800 uninstrumented on a loaded box) but structurally present and reachable on every child construction.

**Verdict:** CONFIRMED (both parts).

---

## C4

**Claim:** on `evalon/grpc-go-en-aa3a022f` the added `balancer.ExitIdler` references in `balancer/ringhash/ringhash.go` produce SA1019 under the Staticcheck configuration used by `scripts/vet.sh`.

**Added references (`git diff $(git merge-base HEAD origin/master) -- balancer/ringhash/ringhash.go | grep ExitIdler`):**

```diff
-		var idleBalancer endpointsharding.ExitIdler
+		var idleBalancer balancer.ExitIdler
-	balancer endpointsharding.ExitIdler
+	balancer balancer.ExitIdler
```

**Tool version** — `scripts/vet.sh` installs `honnef.co/go/tools/cmd/staticcheck` from `test/tools` (`test/tools/go.mod:10: honnef.co/go/tools v0.7.0`):

```sh
cd ~/wt/aa3a022f/test/tools && go install honnef.co/go/tools/cmd/staticcheck && ~/go/bin/staticcheck -version
```

```console
staticcheck 2026.1 (v0.7.0)
```

**Run exactly as vet.sh does (`staticcheck -checks 'all'`), scoped to the ringhash package:**

```sh
cd ~/wt/aa3a022f && ~/go/bin/staticcheck -checks 'all' ./balancer/ringhash/... > ~/c4_sc_ringhash_pinned.log; cat ~/c4_sc_ringhash_pinned.log
```

```console
balancer/ringhash/ringhash.go:271:20: balancer.ExitIdler is deprecated: All balancers must implement this interface. This interface will be removed in a future release.  (SA1019)
balancer/ringhash/ringhash.go:405:11: balancer.ExitIdler is deprecated: All balancers must implement this interface. This interface will be removed in a future release.  (SA1019)
balancer/ringhash/ringhash_e2e_test.go:62:2: package "google.golang.org/grpc/interop/grpc_testing" is being imported more than once (ST1019)
	balancer/ringhash/ringhash_e2e_test.go:63:2: other import of "google.golang.org/grpc/interop/grpc_testing"
balancer/ringhash/ringhash_test.go:687:2: addr.BalancerAttributes is deprecated: when an Address is inside an Endpoint, this field should not be used, and it will eventually be removed entirely.  (SA1019)
balancer/ringhash/ringhash_test.go:687:28: addr.BalancerAttributes is deprecated: when an Address is inside an Endpoint, this field should not be used, and it will eventually be removed entirely.  (SA1019)
```

Lines 271 and 405 are the two added references (`var idleBalancer balancer.ExitIdler`, `balancer balancer.ExitIdler`).

**Apply vet.sh's SA1019 exclusion list** (vet.sh line 168: `noret_grep "(SA1019)" "${SC_OUT}" | not grep -Fv '<allow-list>'` — any line printed by the filter fails vet). `~/c4_filter.sh` extracts that allow-list verbatim from the branch's `scripts/vet.sh` and applies the same `grep -Fv`:

```sh
# ~/c4_filter.sh <staticcheck-output>
grep "(SA1019)" "$1" | grep -Fv "$(sed -n '/^  noret_grep "(SA1019)" "${SC_OUT}" | not grep -Fv /,/^XXXXX PleaseIgnoreUnused.$/p' scripts/vet.sh | sed '1s/.*grep -Fv .//' | sed '$s/.$//')"
```

```sh
cd ~/wt/aa3a022f && ~/c4_filter.sh ~/c4_sc_ringhash_pinned.log; echo "rc=$?"
```

```console
balancer/ringhash/ringhash.go:271:20: balancer.ExitIdler is deprecated: All balancers must implement this interface. This interface will be removed in a future release.  (SA1019)
balancer/ringhash/ringhash.go:405:11: balancer.ExitIdler is deprecated: All balancers must implement this interface. This interface will be removed in a future release.  (SA1019)
rc=0
```

Both added-reference diagnostics survive the exclusion list (the `addr.BalancerAttributes` test lines are on the allow-list and are filtered out), so `scripts/vet.sh` would fail on this branch.

**Control — main checkout** (`cd ~/repos/grpc-go && ~/go/bin/staticcheck -checks 'all' ./balancer/ringhash/... > ~/c4_sc_ringhash_perfect.log; cd ~/wt/aa3a022f && ~/c4_filter.sh ~/c4_sc_ringhash_perfect.log; echo rc=$?`):

```console
balancer/ringhash/ringhash_e2e_test.go:62:2: package "google.golang.org/grpc/interop/grpc_testing" is being imported more than once (ST1019)
	balancer/ringhash/ringhash_e2e_test.go:63:2: other import of "google.golang.org/grpc/interop/grpc_testing"
balancer/ringhash/ringhash_test.go:689:2: addr.BalancerAttributes is deprecated: ...  (SA1019)
balancer/ringhash/ringhash_test.go:689:28: addr.BalancerAttributes is deprecated: ...  (SA1019)
rc=1
```

(`rc=1` = zero unexcluded SA1019 lines; no `ExitIdler` diagnostics at all on the main checkout.)

**Packaged repro.** `verify/repro/c4_staticcheck_sa1019.sh` installs the pinned staticcheck from `test/tools`, runs it on `./balancer/ringhash/...` and applies vet.sh's SA1019 allow-list exactly as vet.sh does:

```sh
cd ~/wt/aa3a022f && bash ~/repos/grpc-go/verify/repro/c4_staticcheck_sa1019.sh; echo EXIT=$?
```

```text
staticcheck: staticcheck 2026.1 (v0.7.0)
balancer/ringhash/ringhash.go:271:20: balancer.ExitIdler is deprecated: All balancers must implement this interface. This interface will be removed in a future release.  (SA1019)
balancer/ringhash/ringhash.go:405:11: balancer.ExitIdler is deprecated: All balancers must implement this interface. This interface will be removed in a future release.  (SA1019)
FAIL: unexcluded SA1019 diagnostics above would fail scripts/vet.sh
EXIT=1
```

Same script on the main checkout (`cd ~/repos/grpc-go && bash verify/repro/c4_staticcheck_sa1019.sh`): `OK: no unexcluded SA1019 diagnostics in balancer/ringhash`, `EXIT=0`.

**Impact reasoning.** `scripts/vet.sh` is the CI lint gate (`.github/workflows/testing.yml` runs it); this branch would fail that gate on every CI run until the references are either removed, replaced with a non-deprecated interface, or added to the allow-list.

**Verdict:** CONFIRMED.

---

## C5

**Claim:** on `evalon/grpc-go-en-f18f8972` child-state publication is not serialized with configuration-batch boundaries: (entry) a callback that passed the inhibition check before a batch starts can publish a partial aggregate while the batch has a blocked child; (completion) a callback whose state is already in the consolidated update can publish it again afterwards.

**Instrumentation** (`verify/repro/c5_inhibit_hooks_f18f8972.patch`, test-only scheduling hooks around the existing inhibition check in `endpointsharding.go`; no logic change):

```go
var testHookBeforeInhibitCheck, testHookAfterInhibitCheck func()
...
	if testHookBeforeInhibitCheck != nil {
		testHookBeforeInhibitCheck()
	}
	if es.inhibitChildUpdates.Load() {
		return
	}
	if testHookAfterInhibitCheck != nil {
		testHookAfterInhibitCheck()
	}
```

**Probe** (`verify/repro/c5_batch_boundary_probe_test.go`): two stub children A and B. *BatchEntry:* B's callback is paused by `testHookAfterInhibitCheck` right after it passed the (not-yet-inhibited) check; the test then starts `UpdateClientConnState` (which sets `inhibitChildUpdates`) with A's child update blocked, releases B's callback, and records parent publications while A is still blocked. *BatchCompletion:* B's callback is paused by `testHookBeforeInhibitCheck` after recording its state but before the check; the batch completes and publishes the consolidated state (which includes B's state); then B's callback is released and publications are recorded.

```sh
cd ~/wt/f18f8972 && git apply ~/repos/grpc-go/verify/repro/c5_inhibit_hooks_f18f8972.patch && cp ~/repos/grpc-go/verify/repro/c5_batch_boundary_probe_test.go balancer/endpointsharding/ && go test -tags verifyrepro ./balancer/endpointsharding -run '^TestC5BatchBoundaryProbe$' -race -count=1 -v
```

```console
=== RUN   TestC5BatchBoundaryProbe
=== RUN   TestC5BatchBoundaryProbe/BatchEntry
    c5_batch_boundary_probe_test.go:158: publications while batch still blocked on A: 1 -> [READY{ B=READY A=IDLE }]
    c5_batch_boundary_probe_test.go:161: all publications since batch start: [READY{ B=READY A=IDLE } READY{ A=IDLE B=READY }]
    c5_batch_boundary_probe_test.go:163: BATCH-ENTRY RACE: 1 aggregate publication(s) during an inhibited batch with A still blocked: [READY{ B=READY A=IDLE }]
=== RUN   TestC5BatchBoundaryProbe/BatchCompletion
    c5_batch_boundary_probe_test.go:197: consolidated publication(s) at batch completion: [READY{ B=READY A=IDLE }]
    c5_batch_boundary_probe_test.go:206: publications after releasing held callback: [READY{ B=READY A=IDLE } READY{ B=READY A=IDLE }]
    c5_batch_boundary_probe_test.go:208: BATCH-COMPLETION RACE: held callback re-published state already included in the consolidated update: [READY{ B=READY A=IDLE }]
--- FAIL: TestC5BatchBoundaryProbe (0.00s)
    --- FAIL: TestC5BatchBoundaryProbe/BatchEntry (0.00s)
    --- FAIL: TestC5BatchBoundaryProbe/BatchCompletion (0.00s)
FAIL
FAIL	google.golang.org/grpc/balancer/endpointsharding	0.017s
```

Afterwards the worktree was restored (`git checkout -- balancer/endpointsharding/endpointsharding.go`; `git status --short` empty).

- *Batch-entry boundary:* one aggregate publication (`READY{ B=READY A=IDLE }`) reached the parent ClientConn while the batch was in progress with A's update still blocked — a partial-batch publication. → holds.
- *Batch-completion boundary:* the consolidated publication already contained B=READY; releasing B's held callback produced a second, identical publication. → holds.

**Fixture check.** The eval fixture's `TestEval_BatchUpdateInhibition` passes on this branch (`cd ~/wt/f18f8972 && cp ~/eval_tests/tests/eval_endpointsharding_test.go balancer/endpointsharding/ && go test ./balancer/endpointsharding -run '^TestEval_BatchUpdateInhibition$' -race -count=5 -v` → 5× `--- PASS: TestEval_BatchUpdateInhibition`, `ok`), so the committed/eval checks do not exercise these boundary interleavings.

**Impact reasoning.** Consumers of the aggregate picker (e.g. ringhash/xds parents, the ClientConn) can observe a transient aggregate that reflects half of a resolver update, followed by the same state twice. In everyday use the extra publication is idempotent (same picker contents) and the partial state is quickly superseded, so the user-visible effect is extra/out-of-order picker updates rather than a wrong steady state; it does contradict the "one consolidated update per batch" property the inhibition flag exists to provide.

**Verdict:** CONFIRMED (both parts).

---

## C6

**Claim:** on `evalon/grpc-go-en-f18f8972` the committed endpointsharding regression suite omits assertion coverage for at least one of: same-child contention, retained closed handles, continuation after a child configuration error, synchronous ResolverError callbacks.

**Committed tests (source inventory):**

```sh
cd ~/wt/f18f8972 && ls balancer/endpointsharding/ && grep -n "^func (s) Test\|^func Test" balancer/endpointsharding/*_test.go
```

```console
endpointsharding.go
endpointsharding_ext_test.go
endpointsharding_test.go
balancer/endpointsharding/endpointsharding_ext_test.go:66:func Test(t *testing.T) {
balancer/endpointsharding/endpointsharding_ext_test.go:136:func (s) TestEndpointShardingBasic(t *testing.T) {
balancer/endpointsharding/endpointsharding_ext_test.go:212:func (s) TestEndpointShardingReconnectDisabled(t *testing.T) {
balancer/endpointsharding/endpointsharding_ext_test.go:297:func (s) TestEndpointShardingExitIdle(t *testing.T) {
balancer/endpointsharding/endpointsharding_ext_test.go:400:func (s) TestEndpointShardingExitIdleDuringUnrelatedChildUpdate(t *testing.T) {
balancer/endpointsharding/endpointsharding_ext_test.go:488:func (s) TestEndpointShardingSynchronousIdleReportDuringChildInitialization(t *testing.T) {
balancer/endpointsharding/endpointsharding_ext_test.go:555:func (s) TestEndpointShardingSingleUpdateForMultipleEndpoints(t *testing.T) {
balancer/endpointsharding/endpointsharding_test.go:33:func Test(t *testing.T) {
balancer/endpointsharding/endpointsharding_test.go:37:func (s) TestRotateEndpoints(t *testing.T) {
```

None of the names in the claim's *Where to look* (`TestSameChildMutualExclusion`, `TestClosedStateGuardCoverage`, `TestSynchronousLifecycleCallbackSafety`) exist. Keyword search across the committed test files:

```sh
grep -n -i "ResolverError\|MutualExclusion\|ClosedStateGuard\|SynchronousLifecycle\|errors.New\|fmt.Errorf\|return err" balancer/endpointsharding/*_test.go
```

```console
balancer/endpointsharding/endpointsharding_ext_test.go:109:		return fmt.Errorf("UpdateClientConnState wants two endpoints, got: %v", el)
balancer/endpointsharding/endpointsharding_ext_test.go:120:		logger.Fatal(fmt.Errorf("length of child states received: %v, want 2", len(childStates)))
balancer/endpointsharding/endpointsharding_ext_test.go:191:	mr.CC().ReportError(errors.New("test error"))
```

Per part:

- *Same-child contention:* no test issues two concurrent operations on the **same** child and asserts they do not overlap. The only concurrency test (`TestEndpointShardingExitIdleDuringUnrelatedChildUpdate`, line 400) blocks child ep2's update and asserts ExitIdle on a **different** child ep1 is delivered — the opposite scenario. → omission holds.
- *Retained closed handles:* no test calls a retained `ChildState.ExitIdle` (or any child handle) after `Close()` and asserts it is a no-op; the only `Close()` calls in tests are `defer mr.Close()/cc.Close()/bal.Close()` and `bd.ChildBalancer.Close()` inside stub `Close` hooks (lines 143, 172, 221, 233, 252, 302, 314, 335, 431). → omission holds.
- *Child-error continuation:* the only child `UpdateClientConnState` error in the suite is `fakePetiole` (line 109), a sanity guard that fires only if the endpoint count is wrong and is never triggered on purpose; no test makes one child return an error and asserts the remaining children are still configured. → omission holds.
- *Synchronous resolver-error callbacks:* the string `ResolverError` appears in no test file; the only error-reporting path is the e2e `mr.CC().ReportError(errors.New("test error"))` (line 191), which asserts the *picker* returns the error and does not make a child call `UpdateState` synchronously from `ResolverError`, nor assert absence of deadlock. → omission holds.

**Demonstration run (committed suite):**

```sh
cd ~/wt/f18f8972 && go test ./balancer/endpointsharding -run '^Test$' -race -count=1 -v 2>&1 | grep -E "^(=== RUN|--- |ok|FAIL|PASS)"
```

```console
=== RUN   Test
=== RUN   Test/RotateEndpoints
...
=== RUN   Test
=== RUN   Test/EndpointShardingBasic
=== RUN   Test/EndpointShardingExitIdle
=== RUN   Test/EndpointShardingExitIdleDuringUnrelatedChildUpdate
=== RUN   Test/EndpointShardingReconnectDisabled
=== RUN   Test/EndpointShardingSingleUpdateForMultipleEndpoints
=== RUN   Test/EndpointShardingSynchronousIdleReportDuringChildInitialization
--- PASS: Test (0.08s)
PASS
ok  	google.golang.org/grpc/balancer/endpointsharding	1.099s
```

The behaviors themselves are present on the branch — the (uncommitted) eval fixture covering them passes:

```sh
cd ~/wt/f18f8972 && cp ~/eval_tests/tests/eval_endpointsharding_test.go balancer/endpointsharding/ && cmp balancer/endpointsharding/eval_endpointsharding_test.go ~/eval_tests/tests/eval_endpointsharding_test.go && go test ./balancer/endpointsharding -run '^Test/Eval_' -race -count=1 -v -timeout 120s 2>&1 | grep -E "^\s*(--- |ok|FAIL|PASS|panic)"
```

```console
--- PASS: TestEval_ChildExitIdleWhileAnotherChildBlocked (0.00s)
--- PASS: TestEval_SameChildMutualExclusion (0.00s)
--- PASS: TestEval_SynchronousChildUpdateDeadlock (0.00s)
--- PASS: TestEval_ConstructionIdleCallbackSafety (0.05s)
--- PASS: TestEval_ChildStateExitIdleCallback (0.00s)
--- PASS: TestEval_BatchUpdateInhibition (0.00s)
--- PASS: TestEval_ResolverErrorConsolidatedNotification (0.00s)
--- PASS: TestEval_PickerStateAggregationPrecedence (0.00s)
PASS
ok  	google.golang.org/grpc/balancer/endpointsharding	1.069s
```

So this is a coverage gap in what the branch *commits*, not (by these fixtures) a behavioral defect.

**Packaged repro.** `verify/repro/c6_committed_suite_inventory.sh` lists the committed tests and greps them for identifiers a test of each of the four behaviors would have to contain:

```sh
cd ~/wt/f18f8972 && bash ~/repos/grpc-go/verify/repro/c6_committed_suite_inventory.sh; echo EXIT=$?
```

```text
== committed tests in balancer/endpointsharding ==
balancer/endpointsharding/endpointsharding_ext_test.go:66:func Test(t *testing.T) {
balancer/endpointsharding/endpointsharding_ext_test.go:136:func (s) TestEndpointShardingBasic(t *testing.T) {
balancer/endpointsharding/endpointsharding_ext_test.go:212:func (s) TestEndpointShardingReconnectDisabled(t *testing.T) {
balancer/endpointsharding/endpointsharding_ext_test.go:297:func (s) TestEndpointShardingExitIdle(t *testing.T) {
balancer/endpointsharding/endpointsharding_ext_test.go:400:func (s) TestEndpointShardingExitIdleDuringUnrelatedChildUpdate(t *testing.T) {
balancer/endpointsharding/endpointsharding_ext_test.go:488:func (s) TestEndpointShardingSynchronousIdleReportDuringChildInitialization(t *testing.T) {
balancer/endpointsharding/endpointsharding_ext_test.go:555:func (s) TestEndpointShardingSingleUpdateForMultipleEndpoints(t *testing.T) {
balancer/endpointsharding/endpointsharding_test.go:33:func Test(t *testing.T) {
balancer/endpointsharding/endpointsharding_test.go:37:func (s) TestRotateEndpoints(t *testing.T) {
MISSING : same-child mutual exclusion (two ops on ONE child racing)  (no match for /SameChild|sameChild|same child|MutualExclusion/ in balancer/endpointsharding/*_test.go)
MISSING : ExitIdle on a retained ChildState after Close is guarded  (no match for /AfterClose|afterClose|closed.*ExitIdle|ExitIdle.*closed/ in balancer/endpointsharding/*_test.go)
MISSING : sibling children still configured after one child's config error  (no match for /ChildError|childErr|errChild|config(uration)? error|returns? an? error/ in balancer/endpointsharding/*_test.go)
MISSING : synchronous callback from inside ResolverError  (no match for /ResolverError/ in balancer/endpointsharding/*_test.go)
EXIT=1
```

**Impact reasoning.** The four behaviors are exactly the invariants the per-child locking refactor is meant to guarantee; with no committed assertion for them, a future regression (e.g. dropping the closed-guard or the same-child mutex) would pass the branch's own suite.

**Verdict:** CONFIRMED (all 4 omissions hold).

---

## C7

**Claim:** on `evalon/grpc-go-en-a1497860`, the independent-child progress regression (`TestEndpointShardingExitIdleNotBlockedByOtherChildUpdate`) hangs in deferred cleanup under coarse locking after its deadline expires, instead of terminating with an assertion failure.

**Cleanup-path source (target test, `endpointsharding_ext_test.go`):**

```go
	defer es.Close()                                   // line 440 — unconditional
	...
	// child stub:                                     // lines 421-424
			if addr == ep2.Addresses[0].Addr && blockEP2.Load() {
				close(ep2Blocked)
				<-releaseEP2                           // worker parks here
			}
	...
	ep1State.Balancer.ExitIdle()                        // line 463 (adapted; see below)
	select {
	case got := <-exitIdleCh: ...
	case <-ctx.Done():
		t.Fatalf("Timed out waiting for ExitIdle on child %q while another child's update is blocked", ...)   // line 470
	}
	...
	close(releaseEP2)                                  // line 478 — only reached on the success path
```

`releaseEP2` is closed only at line 478; there is no `defer`/`t.Cleanup` that closes it, so a `t.Fatalf` at line 470 runs `defer es.Close()` with the ep2 worker still parked on `<-releaseEP2` (**Cleanup dependency** part).

**Coarse-locking run.** Worktree `~/wt/a1497860-base` is checked out at the target's parent commit `bf9e7cd3` ("xds: parse allowed_grpc_services from the bootstrap config (A102) (#9194)"), i.e. the pre-refactor coarse-locking implementation where `Close()` and `ExitIdle` take the single `es.childMu`. The target's version of `endpointsharding_ext_test.go` was placed there with exactly one change, because coarse-locking `ChildState` exposes `Balancer ExitIdler` instead of an `ExitIdle()` method:

```sh
cd ~/wt/a1497860-base && git apply ~/repos/grpc-go/verify/repro/c7_coarse_locking_test_adaptation.patch
diff <(git -C ~/wt/a1497860 show HEAD:balancer/endpointsharding/endpointsharding_ext_test.go) balancer/endpointsharding/endpointsharding_ext_test.go
go vet ./balancer/endpointsharding/
```

```console
463c463
< 	ep1State.ExitIdle()
---
> 	ep1State.Balancer.ExitIdle()
```

(`go vet` clean.) Then:

```sh
cd ~/wt/a1497860-base && go test ./balancer/endpointsharding -run '^Test/EndpointShardingExitIdleNotBlockedByOtherChildUpdate$' -race -count=1 -v -timeout 40s 2>&1 | tee ~/c7_coarse_run.log
```

```console
=== RUN   Test/EndpointShardingExitIdleNotBlockedByOtherChildUpdate
    endpointsharding_ext_test.go:470: Timed out waiting for ExitIdle on child "addr1" while another child's update is blocked
panic: test timed out after 40s
...
goroutine 10 [sync.Mutex.Lock]:
sync.(*Mutex).Lock(0xc0002324c8)
google.golang.org/grpc/balancer/endpointsharding.(*endpointSharding).Close(0xc0002323c0)
	/home/ubuntu/wt/a1497860-base/balancer/endpointsharding/endpointsharding.go:224 +0x4f
runtime.Goexit()
testing.(*common).FailNow(0xc0000bf500)
testing.(*common).Fatalf(0xc0000bf500, ...)
google.golang.org/grpc/balancer/endpointsharding_test.s.TestEndpointShardingExitIdleNotBlockedByOtherChildUpdate({{}}, 0xc0000bf500)
	/home/ubuntu/wt/a1497860-base/balancer/endpointsharding/endpointsharding_ext_test.go:470 +0x1105

goroutine 11 [chan receive]:
google.golang.org/grpc/balancer/endpointsharding_test.s.TestEndpointShardingExitIdleNotBlockedByOtherChildUpdate.func1(...)
	/home/ubuntu/wt/a1497860-base/balancer/endpointsharding/endpointsharding_ext_test.go:423 +0x1a5
google.golang.org/grpc/internal/balancer/stub.(*bal).UpdateClientConnState(...)
google.golang.org/grpc/balancer/endpointsharding.(*balancerWrapper).updateClientConnStateLocked(...)
	/home/ubuntu/wt/a1497860-base/balancer/endpointsharding/endpointsharding.go:376
google.golang.org/grpc/balancer/endpointsharding.(*endpointSharding).UpdateClientConnState(0xc0002323c0, ...)
	/home/ubuntu/wt/a1497860-base/balancer/endpointsharding/endpointsharding.go:175 +0xbc2

goroutine 18 [sync.Mutex.Lock]:
sync.(*Mutex).Lock(0xc0002324c8)
google.golang.org/grpc/balancer/endpointsharding.(*balancerWrapper).ExitIdle.func1()
	/home/ubuntu/wt/a1497860-base/balancer/endpointsharding/endpointsharding.go:364 +0x50
FAIL	google.golang.org/grpc/balancer/endpointsharding	40.064s
```

Reading the dump: the test's own 10 s deadline fired (`ext_test.go:470: Timed out waiting for ExitIdle …`) — the **Coarse-locking trigger** part — and `t.Fatalf` → `FailNow` → `Goexit` ran the deferred `es.Close()`, which is parked in `sync.Mutex.Lock` on `es.childMu` (`endpointsharding.go:224`). That mutex is held by goroutine 11 — the `UpdateClientConnState` worker — which is inside the ep2 stub at `ext_test.go:423 <-releaseEP2`, never released because `close(releaseEP2)` (line 478) was skipped by the fatal. Goroutine 18 (`ExitIdle.func1`, `endpointsharding.go:364`) is also waiting on the same mutex. The test therefore did not terminate on its assertion failure; it was killed 30 s later by the `-timeout 40s` harness (`panic: test timed out after 40s`). Without the `-timeout` flag, the default 10 min harness timeout would apply.

Both parts hold: the deadline failure leaves `releaseEP2` unreleased and deferred `es.Close()` waits on the resulting blocked work.

**Impact reasoning.** The regression test is meant to *fail* on the coarse-locking implementation it guards against; instead it hangs until the harness kills the package, taking down every other test in the package run with it and producing a 40 s+ (default 10 min) stall rather than a readable assertion. Fix is a one-liner: `defer` a `sync.OnceFunc(func(){ close(releaseEP2) })` (or `t.Cleanup`) before `defer es.Close()`.

**Verdict:** CONFIRMED (both parts).

---

## C8

**Claim:** on `evalon/grpc-go-en-3c1b9630` a parent `endpointSharding.ExitIdle()` publishes intermediate aggregate states separately when multiple children synchronously report state changes during idle-exit fan-out, instead of one consolidated publication.

**Probe** (`verify/repro/c8_exitidle_fanout_probe_test.go`): two stub children A and B configured via `UpdateClientConnState` (both report IDLE, auto-reconnect disabled so that configuration does not trigger ExitIdle); each child's `ExitIdle` synchronously calls `cc.UpdateState(CONNECTING)`. Parent publications to a recording ClientConn are cleared, then one `es.ExitIdle()` is called, the probe waits (≤3 s) for the first publication and a further 200 ms for any trailing ones, and reports everything received.

```sh
cd ~/wt/3c1b9630 && cp ~/repos/grpc-go/verify/repro/c8_exitidle_fanout_probe_test.go balancer/endpointsharding/ && go test -tags verifyrepro ./balancer/endpointsharding -run '^TestC8ExitIdleFanoutProbe$' -race -count=1 -v
```

```console
=== RUN   TestC8ExitIdleFanoutProbe
    c8_exitidle_fanout_probe_test.go:76: publications from initial configuration: [IDLE{ A=IDLE B=IDLE }]
    c8_exitidle_fanout_probe_test.go:89: publications caused by one parent ExitIdle call: 2 -> [CONNECTING{ A=IDLE B=CONNECTING } CONNECTING{ A=CONNECTING B=CONNECTING }]
    c8_exitidle_fanout_probe_test.go:91: want exactly 1 consolidated publication after ExitIdle fan-out, got 2: [CONNECTING{ A=IDLE B=CONNECTING } CONNECTING{ A=CONNECTING B=CONNECTING }]
--- FAIL: TestC8ExitIdleFanoutProbe (0.20s)
FAIL
FAIL	google.golang.org/grpc/balancer/endpointsharding	0.215s
```

Three separate `-count=1` runs on the target all reported `2` publications, each including an intermediate aggregate in which only one child had left IDLE (e.g. `CONNECTING{ B=IDLE A=CONNECTING }` in one run, `CONNECTING{ A=IDLE B=CONNECTING }` in another — order depends on the random child rotation).

**Control — main checkout** (`cd ~/repos/grpc-go && cp verify/repro/c8_exitidle_fanout_probe_test.go balancer/endpointsharding/ && go test -tags verifyrepro ./balancer/endpointsharding -run '^TestC8ExitIdleFanoutProbe$' -race -count=1 -v`, three runs):

```console
    c8_exitidle_fanout_probe_test.go:89: publications caused by one parent ExitIdle call: 1 -> [CONNECTING{ A=CONNECTING B=CONNECTING }]
--- PASS: TestC8ExitIdleFanoutProbe
```

The main checkout wraps the fan-out in the same inhibit/publish-once mechanism used for `UpdateClientConnState`, giving one consolidated update; the target's `endpointSharding.ExitIdle` iterates children and calls `bw.ExitIdle()` without inhibiting child updates, so each child's synchronous `UpdateState` publishes its own aggregate.

**Impact reasoning.** Every parent-driven ExitIdle (e.g. a ClientConn leaving IDLE, or ringhash requesting a connect) on an N-child shard produces up to N picker updates where one would do, and the first N-1 are intermediate aggregates (some children still IDLE) that briefly mislead any consumer that acts on the aggregate connectivity state. It is not a correctness failure of the final state — the last publication is the consolidated one — but it contradicts the single-consolidated-notification behavior the branch claims elsewhere and that the audited implementation provides.

**Verdict:** CONFIRMED.
