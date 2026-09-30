Observations for audit run `v-ad8b5d75`. Every command below was run on this machine (`go version go1.25.7 linux/amd64`); output blocks are verbatim excerpts, and the complete raw logs are in `verify/logs/`. Nothing in the claim branches' production code was changed except by the mutant patches in `verify/repro/`, each of which was reverted after its run (`git status --short` empty in every worktree afterwards).

Shared setup used by all sections (repeat it to replay any section):

```sh
cd ~/repos/grpc-go
git fetch origin grpc-go-endpointsharding-decouple-locking-perfect
git checkout -b verify/grpc-go-endpointsharding-decouple-locking-v-ad8b5d75 origin/grpc-go-endpointsharding-decouple-locking-perfect
# The claim branches are not on origin; they live in the repository named by the claim URLs.
git remote add claims https://github.com/kaitranntt-evals/grpc-go-endpointsharding-decouple-locking
for id in dd885fa5 7fd337be f7aa6335 0fd80b14 30a9d20e; do
  git fetch claims evalon/grpc-go-en-$id:refs/remotes/claims/evalon/grpc-go-en-$id
  git worktree add --detach ~/wt/$id claims/evalon/grpc-go-en-$id
done
R=~/repos/grpc-go/verify/repro   # repro files from this branch
```

Commits audited:

| claim branch | commit |
|---|---|
| `evalon/grpc-go-en-dd885fa5` | `fddc0079e8dc0d9764fac7de7231df20431cd6d2` |
| `evalon/grpc-go-en-7fd337be` | `eb51b1b6e6e22417eeeb77a6a4fbd007b3200904` |
| `evalon/grpc-go-en-f7aa6335` | `64974427fd4405185a0eca0276added8989a3827` |
| `evalon/grpc-go-en-0fd80b14` | `554457514f1383ef1b97037d4c9a66ef5987c972` |
| `evalon/grpc-go-en-30a9d20e` | `681cf739286956ad8501978541186b860af09a16` |

## C1

Claim: an added or changed synchronization comment in `balancer/endpointsharding/` excludes a lock-acquisition order that occurs when a synchronous child callback acquires a parent mutex while its caller retains a child mutex. Adjudicated separately on `evalon/grpc-go-en-dd885fa5` and `evalon/grpc-go-en-7fd337be`. On both branches the parent mutex is `endpointSharding.mu` (`es.mu`) and the child mutex is the per-child `balancerWrapper.mu`.

### What the changed comments say

```sh
B=bf9e7cd3430df40d0732ba42eb88bd5f2cc63407
git -C ~/wt/dd885fa5 diff $B HEAD -- balancer/endpointsharding/endpointsharding.go
git -C ~/wt/7fd337be diff $B HEAD -- balancer/endpointsharding/endpointsharding.go
```

`evalon/grpc-go-en-dd885fa5` — comment added on the new field `balancerWrapper.mu` (all `+` lines in the diff):

```go
	// mu synchronizes calls into the child balancer. It must be held for all
	// calls into child. Since every child has its own mutex, a slow operation
	// on one child does not block operations on other children. To avoid
	// deadlocks, do not acquire mu while holding es.mu, and do not hold mu
	// while calling into es.cc.
	mu sync.Mutex
```

`evalon/grpc-go-en-7fd337be` — comment added on the new field `balancerWrapper.mu` (all `+` lines in the diff):

```go
	// mu synchronizes calls into the child balancer. It is specific to this
	// child, so a slow operation on one child never blocks operations on
	// another child. mu must not be held while calling into the parent
	// endpointsharding balancer's methods that acquire es.mu, and es.mu must
	// not be held while acquiring mu.
	mu sync.Mutex
```

Both branches also changed the comment on `endpointSharding.mu` from "do not acquire childMu while holding mu" to "do not acquire a balancerWrapper's mu while holding mu"; that sentence only forbids the parent-then-child order and is not contradicted by anything observed below.

Production code on both branches (identical shape; line numbers differ), which is what the probe exercises:

```go
func (bw *balancerWrapper) UpdateState(state balancer.State) {
	bw.es.mu.Lock()
	bw.childState.State = state
	bw.es.mu.Unlock()
	if state.ConnectivityState == connectivity.Idle && !bw.es.esOpts.DisableAutoReconnect {
		bw.ExitIdle()
	}
	bw.es.updateState()   // takes es.mu again and calls es.cc.UpdateState under it
}

func (bw *balancerWrapper) exitIdle() {
	bw.mu.Lock()
	defer bw.mu.Unlock()
	...
	bw.child.ExitIdle()
}

func (bw *balancerWrapper) updateClientConnState(ccs balancer.ClientConnState) error {
	bw.mu.Lock()
	defer bw.mu.Unlock()
	return bw.child.UpdateClientConnState(ccs)
}
```

### Runtime probe

`verify/repro/c1_lockorder_probe_test.go.txt` (stored with a `.txt` suffix so `go vet ./...` on this branch does not try to compile it outside its package) is an in-package test that adds no production code. It installs a child balancer that calls `cc.UpdateState` synchronously from `UpdateClientConnState` and from `ExitIdle` (as real children such as pick_first do), and uses `TryLock` on the production mutexes to report which are held:

- `TestVerifyC1_UpdateClientConnStateCallbackAcquiresParentMuUnderChildMu`: while the child is inside `UpdateClientConnState` the test goroutine takes `es.mu`; if the child's synchronous `cc.UpdateState` needs `es.mu` while `balancerWrapper.mu` is retained, the callback must block with the child mutex still locked until the test releases `es.mu`. The test fails if that is not what happens.
- `TestVerifyC1_ExitIdleCallbackReachesParentCCUnderBothMutexes`: calls `es.ExitIdle()` and, from inside the parent `ClientConn`'s `UpdateState` (`es.cc`), records whether `es.mu` and `balancerWrapper.mu` are locked at that instant. The test fails if either is not locked.

The same command was run in each worktree:

```sh
for id in dd885fa5 7fd337be; do
  cd ~/wt/$id
  cp $R/c1_lockorder_probe_test.go.txt balancer/endpointsharding/c1_lockorder_probe_test.go
  go test ./balancer/endpointsharding -run '^TestVerifyC1_' -race -count=1 -v
  rm balancer/endpointsharding/c1_lockorder_probe_test.go
done
```

### `evalon/grpc-go-en-dd885fa5` — observed

Full log: `verify/logs/c1_dd885fa5.txt`.

```console
=== RUN   TestVerifyC1_UpdateClientConnStateCallbackAcquiresParentMuUnderChildMu
    c1_lockorder_probe_test.go:128: inside child.UpdateClientConnState: child mutex (balancerWrapper.mu) held by caller = true
    c1_lockorder_probe_test.go:142: synchronous cc.UpdateState callback blocked while test held es.mu = true
    c1_lockorder_probe_test.go:143: child mutex still held while callback was waiting for es.mu = true
    c1_lockorder_probe_test.go:144: stack of the goroutine waiting for es.mu:
        sync.(*Mutex).Lock(0xc0000f0e94)
        	/usr/local/go/src/sync/mutex.go:46 +0x29
        google.golang.org/grpc/balancer/endpointsharding.(*balancerWrapper).UpdateState(0xc0001d4870, {0xc000085b48?, {0x1354280?, 0xc00011c000?}})
        	/home/ubuntu/wt/dd885fa5/balancer/endpointsharding/endpointsharding.go:394 +0x65
        google.golang.org/grpc/balancer/endpointsharding.(*c1Child).UpdateClientConnState(0xc0001ea810, {{{0x0, 0x0, 0x0}, {0xc000090ce0, 0x1, 0x1}, 0x0, 0x0}, {0x0, ...}})
        	/home/ubuntu/wt/dd885fa5/balancer/endpointsharding/c1_lockorder_probe_test.go:47 +0x10e
        google.golang.org/grpc/balancer/endpointsharding.(*balancerWrapper).updateClientConnState(0xc0001d4870, {{{0x0, 0x0, 0x0}, {0xc000090ce0, 0x1, 0x1}, 0x0, 0x0}, {0x0, ...}})
        	/home/ubuntu/wt/dd885fa5/balancer/endpointsharding/endpointsharding.go:424 +0x119
        google.golang.org/grpc/balancer/endpointsharding.(*endpointSharding).UpdateClientConnState(0xc0000f0d80, {{{0x0, 0x0, 0x0}, {0xc000090c60, 0x1, 0x1}, 0x0, 0x0}, {0x0, ...}})
        	/home/ubuntu/wt/dd885fa5/balancer/endpointsharding/endpointsharding.go:196 +0x7a6
    c1_lockorder_probe_test.go:157: OBSERVED: callback acquires parent mutex es.mu while its caller retains child mutex balancerWrapper.mu
--- PASS: TestVerifyC1_UpdateClientConnStateCallbackAcquiresParentMuUnderChildMu (0.50s)
=== RUN   TestVerifyC1_ExitIdleCallbackReachesParentCCUnderBothMutexes
    c1_lockorder_probe_test.go:179: child.ExitIdle calls=1, es.cc.UpdateState calls=1
    c1_lockorder_probe_test.go:180: inside es.cc.UpdateState: parent mutex es.mu held = true, child mutex balancerWrapper.mu held = true
    c1_lockorder_probe_test.go:181: stack inside es.cc.UpdateState:
        google.golang.org/grpc/balancer/endpointsharding.(*c1ParentCC).UpdateState(0xc0001302a0, {0x1?, {0x0?, 0xc000080000?}})
        	/home/ubuntu/wt/dd885fa5/balancer/endpointsharding/c1_lockorder_probe_test.go:32 +0x56
        google.golang.org/grpc/balancer/endpointsharding.(*endpointSharding).updateState(0xc000152000)
        	/home/ubuntu/wt/dd885fa5/balancer/endpointsharding/endpointsharding.go:314 +0xcfc
        google.golang.org/grpc/balancer/endpointsharding.(*balancerWrapper).UpdateState(0xc000156000, {0x4c8be9?, {0x1354280?, 0xc00011c100?}})
        	/home/ubuntu/wt/dd885fa5/balancer/endpointsharding/endpointsharding.go:400 +0x13d
        google.golang.org/grpc/balancer/endpointsharding.(*c1Child).ExitIdle(0xc000124180)
        	/home/ubuntu/wt/dd885fa5/balancer/endpointsharding/c1_lockorder_probe_test.go:59 +0x105
        google.golang.org/grpc/balancer/endpointsharding.(*balancerWrapper).exitIdle(0xc000156000)
        	/home/ubuntu/wt/dd885fa5/balancer/endpointsharding/endpointsharding.go:417 +0xcc
    c1_lockorder_probe_test.go:185: OBSERVED: es.updateState() acquired es.mu and called into es.cc while the caller retained balancerWrapper.mu
--- PASS: TestVerifyC1_ExitIdleCallbackReachesParentCCUnderBothMutexes (0.00s)
PASS
ok  	google.golang.org/grpc/balancer/endpointsharding	1.518s
```

(Stack excerpts here and in the next block omit the goroutine header and the runtime/testing frames; the complete stacks are in the log. A second, later replay of the same command gave the same lines with different pointer values: `verify/logs/c1_dd885fa5_replay.txt`, `verify/logs/c1_7fd337be_replay.txt`.)

Reading for this branch: the comment's rule "do not hold mu while calling into es.cc" is broken by the branch's own code on every synchronous child state update outside an inhibited batch — `es.cc.UpdateState` runs with `balancerWrapper.mu` held, and it gets there by acquiring `es.mu` underneath that child mutex (`exitIdle` → child `ExitIdle` → `balancerWrapper.UpdateState` → `es.updateState` → `es.cc.UpdateState`). Wording note: this branch's comment is phrased as a ban on reaching `es.cc` under the child mutex rather than literally as "do not acquire es.mu under mu"; the file's only explicit call on `es.cc` is `es.cc.UpdateState` in `es.updateState` (`grep -n 'es\.cc\.' balancer/endpointsharding/endpointsharding.go` prints only line 314 and the comment itself on line 359), and it is made with `es.mu` held, so for a child's state report the rule excludes exactly the nested parent-mutex section that was observed. (Other `balancer.ClientConn` methods reach `es.cc` through the wrapper's embedded `ClientConn` without `es.mu`; they were not probed.) The other clause ("do not acquire mu while holding es.mu") was not seen violated.

### `evalon/grpc-go-en-7fd337be` — observed

Full log: `verify/logs/c1_7fd337be.txt`.

```console
=== RUN   TestVerifyC1_UpdateClientConnStateCallbackAcquiresParentMuUnderChildMu
    c1_lockorder_probe_test.go:128: inside child.UpdateClientConnState: child mutex (balancerWrapper.mu) held by caller = true
    c1_lockorder_probe_test.go:142: synchronous cc.UpdateState callback blocked while test held es.mu = true
    c1_lockorder_probe_test.go:143: child mutex still held while callback was waiting for es.mu = true
    c1_lockorder_probe_test.go:144: stack of the goroutine waiting for es.mu:
        sync.(*Mutex).Lock(0xc0000fee94)
        	/usr/local/go/src/sync/mutex.go:46 +0x29
        google.golang.org/grpc/balancer/endpointsharding.(*balancerWrapper).UpdateState(0xc000112600, {0xc000085a00?, {0x1355240?, 0xc000046e20?}})
        	/home/ubuntu/wt/7fd337be/balancer/endpointsharding/endpointsharding.go:369 +0x65
        google.golang.org/grpc/balancer/endpointsharding.(*c1Child).UpdateClientConnState(0xc000176810, {{{0x0, 0x0, 0x0}, {0xc000090ce0, 0x1, 0x1}, 0x0, 0x0}, {0x0, ...}})
        	/home/ubuntu/wt/7fd337be/balancer/endpointsharding/c1_lockorder_probe_test.go:47 +0x10e
        google.golang.org/grpc/balancer/endpointsharding.(*balancerWrapper).updateClientConnState(0xc000112600, {{{0x0, 0x0, 0x0}, {0xc000090ce0, 0x1, 0x1}, 0x0, 0x0}, {0x0, ...}})
        	/home/ubuntu/wt/7fd337be/balancer/endpointsharding/endpointsharding.go:399 +0x118
        google.golang.org/grpc/balancer/endpointsharding.(*endpointSharding).UpdateClientConnState(0xc0000fed80, {{{0x0, 0x0, 0x0}, {0xc000090c60, 0x1, 0x1}, 0x0, 0x0}, {0x0, ...}})
        	/home/ubuntu/wt/7fd337be/balancer/endpointsharding/endpointsharding.go:192 +0xb06
    c1_lockorder_probe_test.go:157: OBSERVED: callback acquires parent mutex es.mu while its caller retains child mutex balancerWrapper.mu
--- PASS: TestVerifyC1_UpdateClientConnStateCallbackAcquiresParentMuUnderChildMu (0.50s)
=== RUN   TestVerifyC1_ExitIdleCallbackReachesParentCCUnderBothMutexes
    c1_lockorder_probe_test.go:179: child.ExitIdle calls=1, es.cc.UpdateState calls=1
    c1_lockorder_probe_test.go:180: inside es.cc.UpdateState: parent mutex es.mu held = true, child mutex balancerWrapper.mu held = true
    c1_lockorder_probe_test.go:181: stack inside es.cc.UpdateState:
        google.golang.org/grpc/balancer/endpointsharding.(*c1ParentCC).UpdateState(0xc000012a80, {0x1?, {0x0?, 0x112f940?}})
        	/home/ubuntu/wt/7fd337be/balancer/endpointsharding/c1_lockorder_probe_test.go:32 +0x56
        google.golang.org/grpc/balancer/endpointsharding.(*endpointSharding).updateState(0xc0000feea0)
        	/home/ubuntu/wt/7fd337be/balancer/endpointsharding/endpointsharding.go:310 +0xd5c
        google.golang.org/grpc/balancer/endpointsharding.(*balancerWrapper).UpdateState(0xc000112680, {0x4c8be9?, {0x1355240?, 0xc000046ef0?}})
        	/home/ubuntu/wt/7fd337be/balancer/endpointsharding/endpointsharding.go:375 +0x13d
        google.golang.org/grpc/balancer/endpointsharding.(*c1Child).ExitIdle(0xc000176a50)
        	/home/ubuntu/wt/7fd337be/balancer/endpointsharding/c1_lockorder_probe_test.go:59 +0x105
        google.golang.org/grpc/balancer/endpointsharding.(*balancerWrapper).exitIdle(0xc000112680)
        	/home/ubuntu/wt/7fd337be/balancer/endpointsharding/endpointsharding.go:392 +0xc7
    c1_lockorder_probe_test.go:185: OBSERVED: es.updateState() acquired es.mu and called into es.cc while the caller retained balancerWrapper.mu
--- PASS: TestVerifyC1_ExitIdleCallbackReachesParentCCUnderBothMutexes (0.00s)
PASS
ok  	google.golang.org/grpc/balancer/endpointsharding	1.522s
```

Reading for this branch: the comment says `mu` "must not be held while calling into the parent endpointsharding balancer's methods that acquire es.mu". `es.updateState` is such a method and it was observed running — with `es.mu` acquired — while the caller still held `balancerWrapper.mu`, on both the `UpdateClientConnState` path and the `ExitIdle` path. The comment therefore rules out a nested acquisition (child mutex, then parent mutex) that the branch performs on every synchronous child state update.

### Impact reasoning

- Runtime behavior is unaffected: the child-then-parent order is the only nesting observed, the opposite order was not observed, and both branches' packages pass with `-race`:

  ```sh
  cd ~/wt/dd885fa5 && go test ./balancer/endpointsharding ./balancer/ringhash -race -count=1
  cd ~/wt/7fd337be && go test ./balancer/endpointsharding ./balancer/ringhash -race -count=1
  ```

  ```console
  ok  	google.golang.org/grpc/balancer/endpointsharding	1.241s
  ok  	google.golang.org/grpc/balancer/ringhash	12.392s
  ok  	google.golang.org/grpc/balancer/endpointsharding	1.180s
  ok  	google.golang.org/grpc/balancer/ringhash	12.379s
  ```

  Nothing deadlocks because of this.
- The defect is that the only written statement of the lock hierarchy is wrong about the path that matters most. The triggering path is not exotic: every child that reports state synchronously from `UpdateClientConnState` or `ExitIdle` (the probe's stack shows both) takes `es.mu` under `balancerWrapper.mu`.
- A maintainer who trusts the comment is misled in one of two directions: (a) believing the child mutex is never held when `es.mu` is taken / `es.cc` is called, they may add code under `es.mu` (or in an `es.cc` callback) that takes a `balancerWrapper.mu` — that is the real deadlock the comment is trying to prevent, and the comment hides that the reverse edge already exists; or (b) treating the existing nested acquisition as a bug, they may "fix" it by releasing the child mutex around callbacks, which would drop the per-child serialization the change exists to provide.
- Workaround: none needed at runtime; the fix is to the comment (state the actual order: `balancerWrapper.mu` may be held while acquiring `es.mu`, never the reverse).

Verdicts: `evalon/grpc-go-en-dd885fa5` CONFIRMED; `evalon/grpc-go-en-7fd337be` CONFIRMED.

## C2

Claim: a changed or added concurrency test in `balancer/endpointsharding/` or `balancer/ringhash/` performs an unbounded cleanup wait for a potentially deadlocked production worker after an assertion failure or timeout. Adjudicated separately on `evalon/grpc-go-en-f7aa6335` and `evalon/grpc-go-en-0fd80b14`.

### The cleanup paths (tests added by each branch)

`evalon/grpc-go-en-f7aa6335`, `balancer/endpointsharding/endpointsharding_ext_test.go`, `TestChildOperationsIndependent` (`nl -ba … | sed -n '442,487p'`):

```go
   442					done := make(chan struct{})
   443					go func() {
   444						defer close(done)
   445						switch operation {
   446						case "UpdateClientConnState":
   447							if err := b.UpdateClientConnState(ccs); err != nil {
   ...
   459					}()
   460					var exitDone chan struct{}
   461					defer func() {
   462						close(release)
   463						<-done
   464						if exitDone != nil {
   465							<-exitDone
   466						}
   467					}()
   ...
   483					select {
   484					case <-exited:
   485					case <-ctx.Done():
   486						t.Fatalf("Other child did not exit idle while %s was blocked", operation)
   487					}
```

`evalon/grpc-go-en-0fd80b14`, `balancer/endpointsharding/endpointsharding_ext_test.go`, `TestChildExitIdleIndependent` (`nl -ba … | sed -n '435,467p'`):

```go
   435				done := make(chan error, 1)
   436				defer func() {
   437					unblock()
   438					if err := <-done; err != nil {
   439						t.Errorf("%s failed: %v", operation, err)
   440					}
   441				}()
   442				go func() {
   443					var err error
   444					switch operation {
   445					case "UpdateClientConnState":
   446						err = b.UpdateClientConnState(state)
   ...
   455					done <- err
   456				}()
   ...
   463				select {
   464				case <-fastExitedIdle:
   465				case <-ctx.Done():
   466					t.Fatal("Fast child's ExitIdle blocked behind the slow child")
   467				}
```

In both, the worker goroutine runs a production call (`b.UpdateClientConnState`, `b.ResolverError`, `ChildState.ExitIdle`), the deferred cleanup releases the fixture barrier (`close(release)` / `unblock()`) and then receives from `done` with no `select`, no context and no timer. `t.Fatal` runs deferred functions, so this receive is on the assertion-timeout path.

### Baseline: tests pass on the unmodified branches

```sh
cd ~/wt/f7aa6335 && go test ./balancer/endpointsharding -run 'Test/ChildOperationsIndependent' -race -count=1
cd ~/wt/0fd80b14 && go test ./balancer/endpointsharding -run 'Test/ChildExitIdleIndependent' -race -count=1
```

```console
ok  	google.golang.org/grpc/balancer/endpointsharding	1.018s
ok  	google.golang.org/grpc/balancer/endpointsharding	1.015s
```

(grpc-go nests these under `Test/…` via `grpctest.RunSubTests`, hence the `-run` patterns.)

### Deadlocked production worker: cleanup never returns

To make the production worker actually deadlock — the regression class these tests exist to catch — `verify/repro/c2_<id>_deadlock_mutant.patch` adds two lines to the branch's `balancerWrapper.ExitIdle` so that `es.mu` is held across the call into the child:

```diff
+		bw.es.mu.Lock()
+		defer bw.es.mu.Unlock()
 		if !bw.isClosed {
 			bw.child.ExitIdle()
```

A child that reports state synchronously from `ExitIdle` then blocks in `balancerWrapper.UpdateState` on `es.mu` forever, and so does every later `es.mu` user, including the test's worker once the barrier is released. `verify/repro/c2_cleanup_hang.sh` applies the patch, runs the branch's own test, prints elapsed time, and reverts the patch:

```sh
cd ~/wt/f7aa6335 && sh $R/c2_cleanup_hang.sh f7aa6335
# = git apply $R/c2_f7aa6335_deadlock_mutant.patch
#   go test ./balancer/endpointsharding -run 'Test/ChildOperationsIndependent/UpdateClientConnState/exitAll=false' -race -count=1 -v -timeout 45s
#   git apply -R $R/c2_f7aa6335_deadlock_mutant.patch
cd ~/wt/0fd80b14 && sh $R/c2_cleanup_hang.sh 0fd80b14
# = git apply $R/c2_0fd80b14_deadlock_mutant.patch
#   go test ./balancer/endpointsharding -run 'Test/ChildExitIdleIndependent/UpdateClientConnState' -race -count=1 -v -timeout 45s
#   git apply -R $R/c2_0fd80b14_deadlock_mutant.patch
```

#### `evalon/grpc-go-en-f7aa6335` — observed

Full log: `verify/logs/c2_f7aa6335.txt`. The test's own assertion fires (10 s `defaultTestTimeout`), but the subtest never ends; the binary is killed by `go test`'s global timeout 35 s later:

```console
=== RUN   Test/ChildOperationsIndependent/UpdateClientConnState/exitAll=false
    balancer.go:193: testutils.BalancerClientConn: UpdateState({IDLE 0xc000113c00})
    endpointsharding_ext_test.go:486: Other child did not exit idle while UpdateClientConnState was blocked
panic: test timed out after 45s
	running tests:
		Test (45s)
		Test/ChildOperationsIndependent (45s)
		Test/ChildOperationsIndependent/UpdateClientConnState/exitAll=false (45s)
```

The test goroutine is parked in the deferred cleanup receive at line 463 (`<-done`), reached from `t.Fatalf` at line 486:

```console
goroutine 26 [chan receive]:
google.golang.org/grpc/balancer/endpointsharding_test.s.TestChildOperationsIndependent.func1.8()
	/home/ubuntu/wt/f7aa6335/balancer/endpointsharding/endpointsharding_ext_test.go:463 +0x49
runtime.Goexit()
	/usr/local/go/src/runtime/panic.go:615 +0x5e
testing.(*common).FailNow(0xc000103c00)
	/usr/local/go/src/testing/testing.go:1013 +0x7b
testing.(*common).Fatalf(0xc000103c00, {0x123337c, 0x32}, {0xc000179d18, 0x1, 0x1})
	/usr/local/go/src/testing/testing.go:1219 +0x99
google.golang.org/grpc/balancer/endpointsharding_test.s.TestChildOperationsIndependent.func1(0xc000103c00)
	/home/ubuntu/wt/f7aa6335/balancer/endpointsharding/endpointsharding_ext_test.go:486 +0x1b16
```

The production worker it is waiting for (started at line 443, inside `b.UpdateClientConnState` at line 447) is past the released fixture barrier — it is at the stub's `bd.ClientConn.UpdateState` call on line 389, which comes after `block()` — and is stuck in production code on `es.mu`:

```console
goroutine 27 [sync.Mutex.Lock]:
...
sync.(*Mutex).Lock(0xc0002030d4)
	/usr/local/go/src/sync/mutex.go:46 +0x29
google.golang.org/grpc/balancer/endpointsharding.(*balancerWrapper).UpdateState(0xc000214d80, {0x0?, {0x1353c80?, 0xc0001111c0?}})
	/home/ubuntu/wt/f7aa6335/balancer/endpointsharding/endpointsharding.go:339 +0x65
google.golang.org/grpc/balancer/endpointsharding_test.s.TestChildOperationsIndependent.func1.3(0xc0002030e0, {{{0x0, 0x0, 0x0}, {0xc000136f60, 0x1, 0x1}, 0x0, 0x0}, {0x0, ...}})
	/home/ubuntu/wt/f7aa6335/balancer/endpointsharding/endpointsharding_ext_test.go:389 +0x210
google.golang.org/grpc/internal/balancer/stub.(*bal).UpdateClientConnState(0xc000113b80, {{{0x0, 0x0, 0x0}, {0xc000136f60, 0x1, 0x1}, 0x0, 0x0}, {0x0, ...}})
	/home/ubuntu/wt/f7aa6335/internal/balancer/stub/stub.go:63 +0xda
google.golang.org/grpc/balancer/endpointsharding.(*balancerWrapper).updateClientConnState(0xc000214d80, {{{0x0, 0x0, 0x0}, {0xc000136f60, 0x1, 0x1}, 0x0, 0x0}, {0x0, ...}})
	/home/ubuntu/wt/f7aa6335/balancer/endpointsharding/endpointsharding.go:369 +0x118
google.golang.org/grpc/balancer/endpointsharding.(*endpointSharding).UpdateClientConnState(0xc000202fc0, {{{0x0, 0x0, 0x0}, {0xc000113a80, 0x2, 0x2}, 0x0, 0x0}, {0x0, ...}})
	/home/ubuntu/wt/f7aa6335/balancer/endpointsharding/endpointsharding.go:166 +0xba6
google.golang.org/grpc/balancer/endpointsharding_test.s.TestChildOperationsIndependent.func1.7()
	/home/ubuntu/wt/f7aa6335/balancer/endpointsharding/endpointsharding_ext_test.go:447 +0x4a6
created by google.golang.org/grpc/balancer/endpointsharding_test.s.TestChildOperationsIndependent.func1 in goroutine 26
	/home/ubuntu/wt/f7aa6335/balancer/endpointsharding/endpointsharding_ext_test.go:443 +0x1625
```

```console
FAIL	google.golang.org/grpc/balancer/endpointsharding	45.076s
FAIL
go test exit=1 after 46s (test's own assertion deadline is 10s; go test -timeout was 45s)
```

The wait tracks whatever global timeout is given, i.e. it is unbounded, and without `-v` the assertion message is never printed at all (full log `verify/logs/c2_f7aa6335_nov.txt`):

```sh
cd ~/wt/f7aa6335 && git apply $R/c2_f7aa6335_deadlock_mutant.patch
time go test ./balancer/endpointsharding -run 'Test/ChildOperationsIndependent/UpdateClientConnState/exitAll=false' -race -count=1 -timeout 100s
git apply -R $R/c2_f7aa6335_deadlock_mutant.patch
```

```console
panic: test timed out after 1m40s
	running tests:
		Test (1m40s)
		Test/ChildOperationsIndependent (1m40s)
		Test/ChildOperationsIndependent/UpdateClientConnState/exitAll=false (1m40s)
...
	/home/ubuntu/wt/f7aa6335/balancer/endpointsharding/endpointsharding_ext_test.go:463 +0x49
...
FAIL	google.golang.org/grpc/balancer/endpointsharding	100.077s
FAIL

real	1m40.548s
```

`grep -c "did not exit idle" verify/logs/c2_f7aa6335_nov.txt` prints `0`.

Control — a regression that serializes children but does not deadlock (`verify/repro/c2_f7aa6335_serialized_control_mutant.patch`, one global mutex around `ExitIdle` and `updateClientConnState`): the worker finishes once the barrier is released, so the same cleanup returns and the test fails cleanly at its own deadline. This shows the hang above is specifically the cleanup waiting on an unfinished worker:

```sh
cd ~/wt/f7aa6335 && git apply $R/c2_f7aa6335_serialized_control_mutant.patch
go test ./balancer/endpointsharding -run 'Test/ChildOperationsIndependent/UpdateClientConnState/exitAll=false' -race -count=1 -v -timeout 45s
git apply -R $R/c2_f7aa6335_serialized_control_mutant.patch
```

```console
    endpointsharding_ext_test.go:486: Other child did not exit idle while UpdateClientConnState was blocked
    balancer.go:193: testutils.BalancerClientConn: UpdateState({IDLE 0xc000051cc0})
    balancer.go:193: testutils.BalancerClientConn: UpdateState({CONNECTING 0xc000304000})
--- FAIL: Test (10.05s)
    --- FAIL: Test/ChildOperationsIndependent (10.05s)
        --- FAIL: Test/ChildOperationsIndependent/UpdateClientConnState/exitAll=false (10.05s)
FAIL
FAIL	google.golang.org/grpc/balancer/endpointsharding	10.067s
```

#### `evalon/grpc-go-en-0fd80b14` — observed

Full log: `verify/logs/c2_0fd80b14.txt`. Same shape: the assertion fires at 10 s, the subtest never ends, the global timeout kills the binary at 45 s:

```console
=== RUN   Test/ChildExitIdleIndependent/UpdateClientConnState
    balancer.go:193: testutils.BalancerClientConn: UpdateState({IDLE 0xc0001b5b40})
    endpointsharding_ext_test.go:466: Fast child's ExitIdle blocked behind the slow child
panic: test timed out after 45s
	running tests:
		Test (45s)
		Test/ChildExitIdleIndependent (45s)
		Test/ChildExitIdleIndependent/UpdateClientConnState (45s)
```

The test goroutine is parked in the deferred cleanup receive at line 438 (`<-done`), reached from `t.Fatal` at line 466, i.e. after `unblock()` on line 437 already released the fixture barrier:

```console
goroutine 40 [chan receive]:
google.golang.org/grpc/balancer/endpointsharding_test.s.TestChildExitIdleIndependent.func1.7()
	/home/ubuntu/wt/0fd80b14/balancer/endpointsharding/endpointsharding_ext_test.go:438 +0x79
runtime.Goexit()
	/usr/local/go/src/runtime/panic.go:615 +0x5e
testing.(*common).FailNow(0xc000183500)
	/usr/local/go/src/testing/testing.go:1013 +0x7b
testing.(*common).Fatal(0xc000183500, {0xc0001f9d00, 0x1, 0x1})
	/usr/local/go/src/testing/testing.go:1212 +0x85
google.golang.org/grpc/balancer/endpointsharding_test.s.TestChildExitIdleIndependent.func1(0xc000183500)
	/home/ubuntu/wt/0fd80b14/balancer/endpointsharding/endpointsharding_ext_test.go:466 +0x1b25
```

The production worker (started at line 442, inside `b.UpdateClientConnState` at line 446) is past the barrier — at the stub's `bd.ClientConn.UpdateState` on line 382, which follows `block(...)` — and is stuck in production code on `es.mu`, so it never reaches `done <- err`:

```console
goroutine 41 [sync.Mutex.Lock]:
...
sync.(*Mutex).Lock(0xc0002030d4)
	/usr/local/go/src/sync/mutex.go:46 +0x29
google.golang.org/grpc/balancer/endpointsharding.(*balancerWrapper).UpdateState(0xc000214b00, {0xc000214b00?, {0x13501a0?, 0xc000090020?}})
	/home/ubuntu/wt/0fd80b14/balancer/endpointsharding/endpointsharding.go:335 +0x65
google.golang.org/grpc/balancer/endpointsharding_test.s.TestChildExitIdleIndependent.func1.3(0xc000203320, {{{0x0, 0x0, 0x0}, {0xc0001b6f00, 0x1, 0x1}, 0x0, 0x0}, {0x0, ...}})
	/home/ubuntu/wt/0fd80b14/balancer/endpointsharding/endpointsharding_ext_test.go:382 +0x235
google.golang.org/grpc/internal/balancer/stub.(*bal).UpdateClientConnState(0xc0001b5b00, {{{0x0, 0x0, 0x0}, {0xc0001b6f00, 0x1, 0x1}, 0x0, 0x0}, {0x0, ...}})
	/home/ubuntu/wt/0fd80b14/internal/balancer/stub/stub.go:63 +0xda
google.golang.org/grpc/balancer/endpointsharding.(*balancerWrapper).updateClientConnState(0xc000214b00, {{{0x0, 0x0, 0x0}, {0xc0001b6f00, 0x1, 0x1}, 0x0, 0x0}, {0x0, ...}})
	/home/ubuntu/wt/0fd80b14/balancer/endpointsharding/endpointsharding.go:363 +0x119
google.golang.org/grpc/balancer/endpointsharding.(*endpointSharding).UpdateClientConnState(0xc000202fc0, {{{0x0, 0x0, 0x0}, {0xc0001b59c0, 0x2, 0x2}, 0x0, 0x0}, {0x0, ...}})
	/home/ubuntu/wt/0fd80b14/balancer/endpointsharding/endpointsharding.go:166 +0xbc6
google.golang.org/grpc/balancer/endpointsharding_test.s.TestChildExitIdleIndependent.func1.8()
	/home/ubuntu/wt/0fd80b14/balancer/endpointsharding/endpointsharding_ext_test.go:446 +0x3dd
created by google.golang.org/grpc/balancer/endpointsharding_test.s.TestChildExitIdleIndependent.func1 in goroutine 40
	/home/ubuntu/wt/0fd80b14/balancer/endpointsharding/endpointsharding_ext_test.go:442 +0x1931
```

```console
FAIL	google.golang.org/grpc/balancer/endpointsharding	45.115s
FAIL
go test exit=1 after 46s (test's own assertion deadline is 10s; go test -timeout was 45s)
```

With a 100 s global timeout and no `-v` (full log `verify/logs/c2_0fd80b14_nov.txt`):

```sh
cd ~/wt/0fd80b14 && git apply $R/c2_0fd80b14_deadlock_mutant.patch
time go test ./balancer/endpointsharding -run 'Test/ChildExitIdleIndependent/UpdateClientConnState' -race -count=1 -timeout 100s
git apply -R $R/c2_0fd80b14_deadlock_mutant.patch
```

```console
panic: test timed out after 1m40s
	running tests:
		Test (1m40s)
		Test/ChildExitIdleIndependent (1m40s)
		Test/ChildExitIdleIndependent/UpdateClientConnState (1m40s)
...
	/home/ubuntu/wt/0fd80b14/balancer/endpointsharding/endpointsharding_ext_test.go:438 +0x79
...
FAIL	google.golang.org/grpc/balancer/endpointsharding	100.103s
FAIL

real	1m40.583s
```

`grep -c "blocked behind" verify/logs/c2_0fd80b14_nov.txt` prints `0`.

Control — serialized-but-not-deadlocked regression (`verify/repro/c2_0fd80b14_serialized_control_mutant.patch`): the worker finishes after `unblock()`, cleanup returns, clean failure at the test's own deadline:

```sh
cd ~/wt/0fd80b14 && git apply $R/c2_0fd80b14_serialized_control_mutant.patch
go test ./balancer/endpointsharding -run 'Test/ChildExitIdleIndependent/UpdateClientConnState' -race -count=1 -v -timeout 45s
git apply -R $R/c2_0fd80b14_serialized_control_mutant.patch
```

```console
    endpointsharding_ext_test.go:466: Fast child's ExitIdle blocked behind the slow child
    balancer.go:193: testutils.BalancerClientConn: UpdateState({IDLE 0xc000324000})
    balancer.go:193: testutils.BalancerClientConn: UpdateState({READY 0xc000324040})
--- FAIL: Test (10.00s)
    --- FAIL: Test/ChildExitIdleIndependent (10.00s)
        --- FAIL: Test/ChildExitIdleIndependent/UpdateClientConnState (10.00s)
FAIL
FAIL	google.golang.org/grpc/balancer/endpointsharding	10.017s
```

### Impact reasoning

- On the unmodified branches the tests pass in about a second; the defect is dormant until production code regresses.
- These tests exist to catch locking regressions in `endpointsharding`, and a lock regression that deadlocks (rather than merely serializing) is an ordinary member of that class — the mutant is two lines. In exactly that situation the test does not fail at its 10 s deadline: its deferred cleanup waits on the stuck production worker until `go test`'s global timeout (10 minutes by default; observed here at both 45 s and 100 s, i.e. whatever is configured).
- What the developer or CI sees is then a `panic: test timed out` goroutine dump for the whole package test binary instead of a `--- FAIL` line, and without `-v` the assertion message that explains the failure is never printed (0 matches in the non-verbose logs on both branches). Because the panic ends the test binary, tests that would run after it in the package do not get a result (standard `go test` behavior; not separately exercised here).
- Releasing the fixture barrier does not help: in both dumps the worker is already past the barrier and blocked inside production code on `es.mu`.
- Workaround: run with `-v` and a short `-timeout` and read the dump. Fix: bound the cleanup receive (`select` on `done` and a timer/context, reporting a leak instead of blocking).
- Scope: only the `UpdateClientConnState` subtest of each table was executed under the mutant; the other subtests share the same deferred cleanup. `balancer/ringhash` tests were not needed to settle the claim and were not examined.

Verdicts: `evalon/grpc-go-en-f7aa6335` CONFIRMED; `evalon/grpc-go-en-0fd80b14` CONFIRMED.

## C4

Claim: on `evalon/grpc-go-en-30a9d20e` the removed-child ExitIdle test passes when a forbidden event from the removed child arrives before the expected event from another child, because its waiting helper discards the forbidden event. Two named parts: *Event retention* and *Assertion outcome*.

Naming note: `TestEndpointSharding_ChildExitIdleAfterRemoval`, `awaitAddr`, `blockingChildAddrB` and the negative receive exist on the branch under those names. `TestClosedStateGuardCoverage` and `c1.enterExit` from the claim's "where to look" do not exist on the branch (`grep -rn "TestClosedStateGuardCoverage\|enterExit" balancer/` matches nothing there); the attached fixture has an `enterExitIdle` hook instead. Neither is needed for either part.

Code under audit (`balancer/endpointsharding/endpointsharding_ext_test.go` on the branch, added by the branch):

```go
// awaitAddr waits for addr to be received on ch, ignoring other addresses.
func awaitAddr(ctx context.Context, t *testing.T, ch <-chan string, addr, desc string) {
	t.Helper()
	for {
		select {
		case got := <-ch:
			if got == addr {
				return
			}
		case <-ctx.Done():
			t.Fatalf("Timeout waiting for %s on child %q", desc, addr)
		}
	}
}
```

```go
   619		states[blockingChildAddrA].ExitIdle()
   620		states[blockingChildAddrB].ExitIdle()
   621		awaitAddr(ctx, t, ctrl.exitIdleCalled, blockingChildAddrB, "ExitIdle")
   622		sCtx, sCancel := context.WithTimeout(ctx, defaultTestShortTimeout)
   623		defer sCancel()
   624		select {
   625		case addr := <-ctrl.exitIdleCalled:
   626			t.Fatalf("ExitIdle called on child %q, want no calls", addr)
   627		case <-sCtx.Done():
   628		}
```

### Part 1 — Event retention

`verify/repro/c4_awaitaddr_probe_test.go.txt` (stored with a `.txt` suffix so `go vet ./...` on this branch does not try to compile it outside its package) calls the branch's unmodified `awaitAddr` on a channel of the same shape as `exitIdleCalled`, with A's event queued before B's, then performs the same negative receive as the test. It fails if A's event is preserved or reported.

```sh
cd ~/wt/30a9d20e
cp $R/c4_awaitaddr_probe_test.go.txt balancer/endpointsharding/c4_awaitaddr_probe_test.go
go test ./balancer/endpointsharding -run '^TestVerifyC4_AwaitAddrDiscardsForbiddenEvent$' -count=1 -v
rm balancer/endpointsharding/c4_awaitaddr_probe_test.go
```

```console
=== RUN   TestVerifyC4_AwaitAddrDiscardsForbiddenEvent
    c4_awaitaddr_probe_test.go:24: before awaitAddr: len(ch)=2 (A then B)
    c4_awaitaddr_probe_test.go:27: after awaitAddr(B): len(ch)=0, t.Failed()=false
    c4_awaitaddr_probe_test.go:36: negative receive saw nothing: A's ExitIdle event was consumed and discarded by awaitAddr
--- PASS: TestVerifyC4_AwaitAddrDiscardsForbiddenEvent (0.01s)
PASS
ok  	google.golang.org/grpc/balancer/endpointsharding	0.014s
```

Both events are gone after `awaitAddr(B)`, no failure is recorded, and the following negative receive sees nothing. Part verdict: CONFIRMED.

### Part 2 — Assertion outcome

The behavior the test is meant to forbid is "ExitIdle reaches removed child A". To make that happen for real, `verify/repro/c4_guard_lost_pure_mutant.patch` removes the single production line that makes a closed child ignore ExitIdle:

```diff
@@ -420,7 +420,7 @@ func (bw *balancerWrapper) close() {
 		return
 	}
 	bw.child.Close()
-	bw.isClosed = true
+	// VERIFY PURE MUTANT (C4): bw.isClosed = true removed
 }
```

With this mutant, `states[A].ExitIdle()` on line 619 reaches removed child A on every run; whether A's event or B's is queued first depends on which of the two asynchronous ExitIdle goroutines runs first.

(a) No instrumentation, branch's own test binary built with `-race` (the mode the task requires), 100 runs each of unmutated and mutant (`verify/repro/c4_pure_mutant_passrate.sh`; log `verify/logs/c4_pure.txt`):

```sh
cd ~/wt/30a9d20e && sh $R/c4_pure_mutant_passrate.sh 100
# = go test -c -race -o /tmp/c4_unmutated.test ./balancer/endpointsharding
#   100 x /tmp/c4_unmutated.test -test.run 'Test/EndpointSharding_ChildExitIdleAfterRemoval' -test.count=1
#   git apply $R/c4_guard_lost_pure_mutant.patch; go test -c -race -o /tmp/c4_pure_mutant.test ./balancer/endpointsharding; git apply -R …
#   100 x /tmp/c4_pure_mutant.test -test.run 'Test/EndpointSharding_ChildExitIdleAfterRemoval' -test.count=1
```

```console
unmutated branch (removed child A never receives ExitIdle): runs=100 PASS=100 FAIL=0
 balancer/endpointsharding/endpointsharding.go | 2 +-
 1 file changed, 1 insertion(+), 1 deletion(-)
guard-lost mutant (removed child A receives ExitIdle on every run): runs=100 PASS=41 FAIL=59
```

The test reported PASS on 41 of 100 runs in which the removed child did receive ExitIdle. Earlier, larger samples of the same mutant binary gave the same picture and show the dependence on scheduling (logs `verify/logs/c4_pure_matrix_200.txt`, `verify/logs/c4_pure_fullpkg_40.txt`; same loop as above with 200 runs per mode, and 40 runs of the whole package binary with `-test.v -test.count=1`):

```console
pure guard-lost mutant | -race -v | 200 runs: PASS(false negative)=92 FAIL(caught)=108
pure guard-lost mutant | -race (no -v) | 200 runs: PASS(false negative)=91 FAIL(caught)=109
pure guard-lost mutant | no race, -v | 200 runs: PASS(false negative)=0 FAIL(caught)=200
pure guard-lost mutant | no race (no -v) | 200 runs: PASS(false negative)=0 FAIL(caught)=200
```

```console
pure guard-lost mutant | whole package test binary (-race -v) | 40 runs: all-green=20 red=20
     20     --- FAIL: Test/EndpointSharding_ChildExitIdleAfterRemoval
```

So under `-race` roughly 4 to 5 runs in 10 pass with the guard removed (whole-package run fully green 20 of 40 times); without the race detector B's goroutine never lost the race in 400 runs on this machine and the mutant was always caught.

(b) Tying PASS to the event order. `verify/repro/c4_guard_lost_mutant.patch` is the same mutant plus instrumentation in `exitIdle` that numbers each delivery on stderr (it holds a global mutex across `child.ExitIdle()` and the print, so the printed order is the order in which the events were queued). `verify/repro/c4_false_pass.sh` builds it and `verify/repro/c4_classify.sh` tabulates "first delivery" against the test's own result (log `verify/logs/c4_false_pass.txt`):

```sh
cd ~/wt/30a9d20e && sh $R/c4_false_pass.sh 100
# = (part 1 probe), then: git apply $R/c4_guard_lost_mutant.patch
#   go test -c -race -o /tmp/c4_mutant.test ./balancer/endpointsharding; git apply -R …
#   sh $R/c4_classify.sh /tmp/c4_mutant.test 100
```

```console
     33 first=addr-a removedA_got_ExitIdle=1 result=PASS
     67 first=addr-b removedA_got_ExitIdle=1 result=FAIL
```

An earlier 300-run sample of the same binaries (`verify/logs/c4_instrumented_race_300.txt`, and built without `-race` in `verify/logs/c4_instrumented_norace_300.txt`):

```console
    116 first=addr-a removedA_got_ExitIdle=1 result=PASS
    184 first=addr-b removedA_got_ExitIdle=1 result=FAIL
```

```console
      1 first=addr-a removedA_got_ExitIdle=1 result=PASS
    299 first=addr-b removedA_got_ExitIdle=1 result=FAIL
```

The correlation is total in all 700 instrumented runs: removed child A received ExitIdle in every run; the test passed in every run where A's event was delivered before B's (150 runs) and failed in every run where B's came first (550 runs). No run had A first with a FAIL, or B first with a PASS.

Control — the same delivery numbering with the guard left intact (`verify/repro/c4_instrumentation_only_control.patch`; log `verify/logs/c4_instrumentation_only_control_100.txt`):

```sh
cd ~/wt/30a9d20e && git apply $R/c4_instrumentation_only_control.patch
go test -c -race -o /tmp/c4_ctrl.test ./balancer/endpointsharding
git apply -R $R/c4_instrumentation_only_control.patch
sh $R/c4_classify.sh /tmp/c4_ctrl.test 100
```

```console
    100 first=addr-b removedA_got_ExitIdle=0 result=PASS
```

With the guard intact A never receives ExitIdle and the test passes for the right reason; the PASS rows in the mutant tables are therefore passes despite the forbidden delivery.

Part verdict: CONFIRMED.

### Attached evaluation fixture (context only)

The attached `tests/eval_endpointsharding_test.go` (sha256 `e16b4f9e4dc51e1a4100e93767cb9bc4d489a716374a4d7207e1318abdcb9166`), copied byte-exact to `balancer/endpointsharding/eval_endpointsharding_test.go`, was run against the unmutated branch and against the pure mutant (log `verify/logs/c4_fixture.txt`):

```sh
cd ~/wt/30a9d20e
cp ~/eval_tests/tests/eval_endpointsharding_test.go balancer/endpointsharding/eval_endpointsharding_test.go
go test ./balancer/endpointsharding -run '^TestEval_' -race -count=1 -v 2>&1 | grep -E '^(--- |FAIL|ok|    eval_)'
git apply $R/c4_guard_lost_pure_mutant.patch
go test ./balancer/endpointsharding -run '^TestEval_' -race -count=1 -v 2>&1 | grep -E '^(--- |FAIL|ok|    eval_)'
git apply -R $R/c4_guard_lost_pure_mutant.patch; rm balancer/endpointsharding/eval_endpointsharding_test.go
```

Mutant run, relevant lines:

```console
    eval_endpointsharding_test.go:1285: ExitIdle called child after Close finished (queued call on closed child)
--- FAIL: TestEval_SameChildMutualExclusion (0.63s)
```

Unmutated run: `TestEval_SameChildMutualExclusion` passed. So the fixture has its own check that noticed the removed guard in this run; the unreliable detector is the branch's own `TestEndpointSharding_ChildExitIdleAfterRemoval`, which is what C4 is about. (Separately, and not part of any claim here: on the unmutated branch `TestEval_ChildStateExitIdleCallback` failed in both runs and `TestEval_ConstructionIdleCallbackSafety` failed in one of them; those lines are in the log and were not investigated.)

### Impact reasoning

- On the branch as submitted the guard is present and the test passes for the right reason (100/100, A never receives ExitIdle).
- The test is documented on the branch as the check that "calling ExitIdle on a ChildState whose child has been removed is a no-op". If that guard is lost, the test is close to a coin flip under `-race` — the mode the task statement requires — passing 41–46% of single runs here, with the whole package green in 20 of 40 runs. A single CI run therefore has a substantial chance of accepting the regression, and a red run looks like a flaky test.
- The cause is deterministic and independent of scheduling: `awaitAddr` drops every event that is not the awaited address (part 1), so the forbidden event is destroyed whenever it arrives first. Scheduling only decides how often it arrives first.
- What slips through is a closed child balancer receiving `ExitIdle` after `Close()` (observed in every mutant run: `removedA_got_ExitIdle=1`). The trigger is calling `ExitIdle()` on a `ChildState` obtained before the endpoint was removed, which is what the test itself does on line 619; how often production callers such as ringhash do this was not measured.
- Fix: make `awaitAddr` fail (or return) on any unexpected address instead of skipping it, or assert on removed child A directly (per-child counter/channel) rather than through a shared channel.

Verdict: CONFIRMED (both parts).
