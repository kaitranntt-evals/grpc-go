## Setup

Observations only; verdict prose is delivered separately. Everything below was run on linux/amd64 with `go version go1.25.7`.

Audited implementation: `origin/grpc-go-transport-restrict-memory-overhead-perfect` @ `327a6ff993d9866ea656ed166b89744aadcbc940` (this branch = that commit + `verify/`). All four claims name their own target branch in a second repository, `kaitranntt-evals/grpc-go-transport-restrict-memory-overhead`; each was fetched by name and checked out detached in its own scratch worktree:

| Claim | Target branch | Commit adjudicated |
| --- | --- | --- |
| C1 | `evalon/grpc-go-tr-f5eba3f9` | `defac6c3f322eb3db9cf3df43b7112adf05b598d` |
| C2 | `evalon/grpc-go-tr-86436e27` | `eb1ab3e238cc060dd8aa5eef2ed8fac6929c709e` |
| C3 | `evalon/grpc-go-tr-8401755a` | `5418a807865423c63080b5f74bea945af4101602` |
| C4 | `evalon/grpc-go-tr-ad9c633e` | `2db5dd3c196fac4b01e4f0443b1ac76cbb03e026` |

Each `verify/repro/c*.sh` script does the fetch + scratch worktree itself (`verify/repro/_common.sh`), so every section below replays with one command from a checkout of this branch. `CLAIMS_URL` overrides the URL the claim branches are fetched from (default `https://github.com/kaitranntt-evals/grpc-go-transport-restrict-memory-overhead`). No production file is modified on this branch; mutations used as probes live in `verify/repro/*.patch` and are applied only inside the scratch worktrees.

Eval fixture, extracted byte-exact from the attached `eval_tests.zip`:

```sh
unzip -o eval_tests.zip -d ~/eval_tests
sha256sum ~/eval_tests/tests/eval_recv_buffer_compaction_test.go
```

```text
c5e26b9a77345b256970d87313e5527efde93dd7614305fa8fd02d795c23cbbe  /home/ubuntu/eval_tests/tests/eval_recv_buffer_compaction_test.go
```

Baseline on the audited implementation (fixture copied to `internal/transport/eval_recv_buffer_compaction_test.go`, removed again afterwards):

```sh
go test -v -run '^TestEval_' ./internal/transport -race -count=1 2>&1 | grep -E '^(--- |ok|FAIL|PASS)'
GRPC_GO_EXPERIMENTAL_ENABLE_RECEIVE_BUFFER_COMPACTION=false go test -v -run '^TestEval_RecvBufferCompactionDisabled$' google.golang.org/grpc/internal/transport -race -count=1 2>&1 | grep -E '^(--- |ok|FAIL|PASS)'
```

```text
--- PASS: TestEval_RecvBufferCompaction (0.00s)
--- PASS: TestEval_RecvBufferCompactionDisabled (0.00s)
--- PASS: TestEval_RecvBufferCompactionSkippedLargeBuffer (0.01s)
--- PASS: TestEval_RecvBufferErrorResetSafety (0.00s)
--- PASS: TestEval_RecvBufferCompaction_MixedFrames (0.00s)
--- PASS: TestEval_RecvBufferCompaction_MultiCycleMemoryBound (0.00s)
PASS
ok  	google.golang.org/grpc/internal/transport	1.037s
--- PASS: TestEval_RecvBufferCompactionDisabled (0.00s)
PASS
ok  	google.golang.org/grpc/internal/transport	1.016s
```

## C1

Claim: `TestRecvBufferZeroCopy` contains an exercised channel receive without an effective local bound that interrupts the operation if it stalls. Target: `evalon/grpc-go-tr-f5eba3f9` @ `defac6c3f322eb3db9cf3df43b7112adf05b598d`.

Naming drift: the claim points at `internal/transport/transport_test.go`; on this branch the test lives in `internal/transport/recv_buffer_test.go:153`.

Replay (about 2.5 minutes, dominated by the two deliberate `go test -timeout` expiries):

```sh
sh verify/repro/c1_zero_copy_unbounded_receive.sh
```

The test as it exists on the branch (`sed -n '153,180p' internal/transport/recv_buffer_test.go`):

```go
func (s) TestRecvBufferZeroCopy(t *testing.T) {
	for _, enabled := range []bool{true, false} {
		t.Run(fmt.Sprintf("compaction=%v", enabled), func(t *testing.T) {
			testutils.SetEnvConfig(t, &envconfig.EnableReceiveBufferCompaction, enabled)
			for _, size := range []int{1, recvBufferCompactionThreshold - 1, recvBufferCompactionThreshold, http2MaxFrameLen} {
				t.Run(fmt.Sprintf("size=%d", size), func(t *testing.T) {
					var b recvBuffer
					b.init()
					first := mem.Copy(make([]byte, size), mem.DefaultBufferPool())
					second := mem.Copy(make([]byte, size), mem.DefaultBufferPool())
					firstData, secondData := first.ReadOnlyData(), second.ReadOnlyData()
					b.put(recvMsg{buffer: first})
					b.put(recvMsg{buffer: second})
					got := <-b.get()
					if &got.buffer.ReadOnlyData()[0] != &firstData[0] {
						t.Error("immediately available buffer was copied")
					}
					got.buffer.Free()
					b.load()
					got = <-b.get()
					if (!enabled || size >= recvBufferCompactionThreshold) && &got.buffer.ReadOnlyData()[0] != &secondData[0] {
						t.Error("queued buffer on the zero-copy path was copied")
					}
					got.buffer.Free()
				})
			}
		})
	}
}
```

Trace of the two receives (lines 166 and 172): both are bare `<-b.get()` statements outside any `select`; `b.get()` returns the raw channel `b.c` (no helper, no context, no timer is involved); the test body creates no `context`, `time.After` or `time.Timer`. The sibling tests in the same file do carry a bound (`context.WithTimeout(context.Background(), defaultTestTimeout)` at lines 48, 229 and 261, consumed through `recvBufferReader`), which is the contrast exercised in step 3. The only thing that can end a stalled receive in `TestRecvBufferZeroCopy` is therefore the process-wide `go test -timeout` alarm (10 minutes by default), which is not local to the test: it panics the whole test binary.

Observed, full script output (the mutations are `verify/repro/c1_stall_load.patch`, which makes `load()` never promote the backlog to `b.c`, and `verify/repro/c1_stall_put.patch`, which makes `put()` never hand a message straight to `b.c`; both model "queue progress stalls"):

```text
== worktree /tmp/tmp.vz294jcU4F/wt = evalon/grpc-go-tr-f5eba3f9 @ defac6c3f322eb3db9cf3df43b7112adf05b598d
== 0. the receives under test
$ grep -n b.get() internal/transport/recv_buffer_test.go
166:					got := <-b.get()
172:					got = <-b.get()
$ sed -n 164,166p internal/transport/transport.go
func (b *recvBuffer) get() <-chan recvMsg {
	return b.c
}
$ grep -n defaultTestTimeout =  internal/transport/keepalive_test.go
47:const defaultTestTimeout = 10 * time.Second
== 1. baseline: the test is exercised and passes on the unmodified branch
--- PASS: Test (0.00s)
    --- PASS: Test/RecvBufferZeroCopy (0.00s)
--- PASS: TestConnectionError_Unwrap (0.00s)
--- PASS: Test (0.00s)
PASS
ok  	google.golang.org/grpc/internal/transport	0.006s
== 2. mutation c1_stall_load.patch (load() never promotes the backlog): second receive, recv_buffer_test.go:172
(wall clock: 47s, go test -timeout 45s)
panic: test timed out after 45s
		Test (45s)
		Test/RecvBufferZeroCopy (45s)
		Test/RecvBufferZeroCopy/compaction=true (45s)
		Test/RecvBufferZeroCopy/compaction=true/size=1 (45s)
FAIL	google.golang.org/grpc/internal/transport	45.063s
FAIL
goroutine 11 [chan receive]:
google.golang.org/grpc/internal/transport.s.TestRecvBufferZeroCopy.func1.1(0xc0000c1180)
	/tmp/tmp.vz294jcU4F/wt/internal/transport/recv_buffer_test.go:172 +0x2ec
== 3. contrast, same mutation: TestRecvBufferCompactionOrdering reads through a recvBufferReader with a defaultTestTimeout context and stops by itself
(wall clock: 20s, go test -timeout 45s)
--- FAIL: Test (20.04s)
    --- FAIL: Test/RecvBufferCompactionOrdering (20.04s)
        --- FAIL: Test/RecvBufferCompactionOrdering/EOF (10.04s)
            recv_buffer_test.go:237: Read() error = rpc error: code = DeadlineExceeded desc = context deadline exceeded, want EOF
        --- FAIL: Test/RecvBufferCompactionOrdering/stream_failed (10.00s)
            recv_buffer_test.go:237: Read() error = rpc error: code = DeadlineExceeded desc = context deadline exceeded, want stream failed
FAIL
FAIL	google.golang.org/grpc/internal/transport	20.049s
FAIL
== 4. mutation c1_stall_put.patch (put() never hands a message straight to b.c): first receive, recv_buffer_test.go:166
(wall clock: 31s, go test -timeout 30s)
panic: test timed out after 30s
		Test (30s)
		Test/RecvBufferZeroCopy (30s)
		Test/RecvBufferZeroCopy/compaction=true (30s)
		Test/RecvBufferZeroCopy/compaction=true/size=1 (30s)
FAIL	google.golang.org/grpc/internal/transport	30.106s
FAIL
goroutine 24 [chan receive]:
google.golang.org/grpc/internal/transport.s.TestRecvBufferZeroCopy.func1.1(0xc0001836c0)
	/tmp/tmp.vz294jcU4F/wt/internal/transport/recv_buffer_test.go:166 +0x207
$ git status --short
?? out.txt
```

What the output shows:

- Step 1: both receives are exercised on the unmodified branch (the test runs and passes).
- Step 2: with queue promotion stalled, the goroutine sits in `chan receive` at `recv_buffer_test.go:172` until the external 45 s alarm fires; wall clock tracks `-timeout` (47 s), i.e. nothing inside the test interrupted it. The result is `panic: test timed out`, not a `--- FAIL` for the test.
- Step 3: under the very same mutation, `TestRecvBufferCompactionOrdering` stops by itself after `defaultTestTimeout` (10 s per subtest) with an ordinary `--- FAIL` and a `context deadline exceeded` message. A local bound is what that looks like; `TestRecvBufferZeroCopy` has none.
- Step 4: the first receive (line 166) is equally unbounded: with a 30 s alarm the goroutine is parked at `recv_buffer_test.go:166` for the full 30 s.
- The scratch worktree is clean afterwards (`git status --short` shows only the captured `out.txt`).

Impact reasoning: this is a test-quality defect, not a production defect. On the unmodified branch the receives complete immediately because both messages are prequeued, so the test passes; the claim's own criterion says prequeued data does not count as a bound. The cost appears exactly when the test is supposed to be useful, i.e. when a change to `put()`/`load()` breaks hand-off: instead of a failure attributed to this test within seconds, the package's whole test binary hangs until the global alarm (10 minutes with the default `go test` timeout, observed here with shortened alarms) and then aborts with a goroutine dump, taking every other test in `internal/transport` scheduled after it down with it. Workaround for a developer: run with a short `-timeout`. Fix sketch: wrap each receive in a `select` with a `defaultTestTimeout` context/timer (as the sibling tests do through `recvBufferReader`).

## C2

Claim: a receive buffer warmed with 16,384 tiny frames retains more payload-backing capacity than a fresh buffer with the same pending payloads after traffic changes to twenty alternating tiny/2-KiB pairs without the backlog becoming empty, because destination sizing preserves earlier allocation sizes. Target: `evalon/grpc-go-tr-86436e27` @ `eb1ab3e238cc060dd8aa5eef2ed8fac6929c709e`.

Replay:

```sh
FIXTURE=~/eval_tests/tests/eval_recv_buffer_compaction_test.go sh verify/repro/c2_warm_tail_cap.sh
```

The script copies `verify/repro/c2_warm_tail_cap_test.go` into `internal/transport/` of the scratch worktree (build tag `verify_repro`, production files untouched) and runs `go test -tags verify_repro -run 'TestVerifyC2' -v ./internal/transport -count=1`. The test drives `recvBuffer.put`/`load` directly with one-byte frames and 2,048-byte frames, wraps the buffer pool to trace every `Get` size, and sums `cap()` of every distinct backing array reachable from the channel entry, the backlog and the open tail (each array counted once). "FRESH" is a new `recvBuffer` fed exactly the payload sequence that is still pending in the warmed one.

Mechanism on the branch (`internal/transport/transport.go`): `tryCompact` sizes each new compaction destination with `size := max(b.nextTailCap, n)` (line 182); `nextTailCap` doubles whenever a destination fills (line 178) and is reset to `recvBufferCompactionMinCap` (256) only when the backlog drains completely (line 233). A frame above the pooling threshold seals the open tail (`sealTail()` in `put`), so in alternating traffic every tiny frame opens a new destination at the historical size.

Observed (full output; `N x [payload/cap]` = N entries with that payload length and backing capacity):

```text
== worktree /tmp/tmp.BpBgDpFXaK/wt = evalon/grpc-go-tr-86436e27 @ eb1ab3e238cc060dd8aa5eef2ed8fac6929c709e
== 1. warmed vs matched fresh buffer, allocation tracing, counterfactual, fixture bound replay
=== RUN   TestVerifyC2_WarmedThenAlternating_PartialDrain
    zz_verify_c2_test.go:183: after warm-up: nextTailCap=8192 layout: 1x[1/1] 1x[256/256] 1x[512/512] 1x[1024/1024] 1x[4096/4096] 1x[10495/16384]
    zz_verify_c2_test.go:187: after partial drain: backlogLen=1 nextTailCap=8192 layout: 1x[4096/4096] 1x[10495/16384]
    zz_verify_c2_test.go:127: warmed pair  1: nextTailCap=8192 tailOpenBeforeTiny=true pool.Get sizes while queuing tiny=[] destination cap for tiny=16384 backlogLen=2
    zz_verify_c2_test.go:127: warmed pair  2: nextTailCap=8192 tailOpenBeforeTiny=false pool.Get sizes while queuing tiny=[8192] destination cap for tiny=16384 backlogLen=4
    zz_verify_c2_test.go:127: warmed pair  3: nextTailCap=8192 tailOpenBeforeTiny=false pool.Get sizes while queuing tiny=[8192] destination cap for tiny=16384 backlogLen=6
    zz_verify_c2_test.go:127: warmed pair 20: nextTailCap=8192 tailOpenBeforeTiny=false pool.Get sizes while queuing tiny=[8192] destination cap for tiny=16384 backlogLen=40
    zz_verify_c2_test.go:189: [snapshot A: right after the 20 pairs] WARMED: entries=41 pendingPayload=55571 retainedCap=413696 pooledLiveBytes=413696 nextTailCap=8192
    zz_verify_c2_test.go:189: [snapshot A: right after the 20 pairs] WARMED layout (count x [payload/cap]): 1x[4096/4096] 1x[10496/16384] 20x[2048/4096] 19x[1/16384]
    zz_verify_c2_test.go:189: [snapshot A: right after the 20 pairs] FRESH : entries=41 pendingPayload=55571 retainedCap=107264 pooledLiveBytes=102400 nextTailCap=256
    zz_verify_c2_test.go:189: [snapshot A: right after the 20 pairs] FRESH  layout (count x [payload/cap]): 1x[4096/4096] 1x[10496/16384] 20x[2048/4096] 19x[1/256]
    zz_verify_c2_test.go:189: [snapshot A: right after the 20 pairs] RESULT: warmed-fresh retained capacity = 306432 bytes (3.9x)
    zz_verify_c2_test.go:189: [snapshot A: right after the 20 pairs]: C2 CONFIRMED: warmed buffer retains 413696 bytes of payload-backing capacity, matched fresh buffer retains 107264
    zz_verify_c2_test.go:198: [snapshot B: only alternating traffic pending] WARMED: entries=39 pendingPayload=40979 retainedCap=393216 pooledLiveBytes=393216 nextTailCap=8192
    zz_verify_c2_test.go:198: [snapshot B: only alternating traffic pending] WARMED layout (count x [payload/cap]): 20x[2048/4096] 19x[1/16384]
    zz_verify_c2_test.go:198: [snapshot B: only alternating traffic pending] FRESH : entries=39 pendingPayload=40979 retainedCap=86784 pooledLiveBytes=81920 nextTailCap=256
    zz_verify_c2_test.go:198: [snapshot B: only alternating traffic pending] FRESH  layout (count x [payload/cap]): 20x[2048/4096] 19x[1/256]
    zz_verify_c2_test.go:198: [snapshot B: only alternating traffic pending] RESULT: warmed-fresh retained capacity = 306432 bytes (4.5x)
    zz_verify_c2_test.go:198: [snapshot B: only alternating traffic pending]: C2 CONFIRMED: warmed buffer retains 393216 bytes of payload-backing capacity, matched fresh buffer retains 86784
--- FAIL: TestVerifyC2_WarmedThenAlternating_PartialDrain (0.00s)
=== RUN   TestVerifyC2_WarmedThenAlternating_NoDrain
    zz_verify_c2_test.go:127: warmed pair  1: nextTailCap=8192 tailOpenBeforeTiny=true pool.Get sizes while queuing tiny=[] destination cap for tiny=16384 backlogLen=6
    zz_verify_c2_test.go:127: warmed pair  2: nextTailCap=8192 tailOpenBeforeTiny=false pool.Get sizes while queuing tiny=[8192] destination cap for tiny=16384 backlogLen=8
    zz_verify_c2_test.go:127: warmed pair  3: nextTailCap=8192 tailOpenBeforeTiny=false pool.Get sizes while queuing tiny=[8192] destination cap for tiny=16384 backlogLen=10
    zz_verify_c2_test.go:127: warmed pair 20: nextTailCap=8192 tailOpenBeforeTiny=false pool.Get sizes while queuing tiny=[8192] destination cap for tiny=16384 backlogLen=44
    zz_verify_c2_test.go:210: [no drain] WARMED: entries=45 pendingPayload=57364 retainedCap=415489 pooledLiveBytes=413696 nextTailCap=8192
    zz_verify_c2_test.go:210: [no drain] WARMED layout (count x [payload/cap]): 1x[1/1] 1x[256/256] 1x[512/512] 1x[1024/1024] 1x[4096/4096] 1x[10496/16384] 20x[2048/4096] 19x[1/16384]
    zz_verify_c2_test.go:210: [no drain] FRESH : entries=45 pendingPayload=57364 retainedCap=123649 pooledLiveBytes=102400 nextTailCap=1024
    zz_verify_c2_test.go:210: [no drain] FRESH  layout (count x [payload/cap]): 1x[1/1] 1x[256/256] 1x[512/512] 1x[1024/1024] 1x[4096/4096] 1x[10496/16384] 20x[2048/4096] 19x[1/1024]
    zz_verify_c2_test.go:210: [no drain] RESULT: warmed-fresh retained capacity = 291840 bytes (3.4x)
    zz_verify_c2_test.go:210: [no drain]: C2 CONFIRMED: warmed buffer retains 415489 bytes of payload-backing capacity, matched fresh buffer retains 123649
--- FAIL: TestVerifyC2_WarmedThenAlternating_NoDrain (0.00s)
=== RUN   TestVerifyC2_Counterfactual_ResetNextTailCap
    zz_verify_c2_test.go:227: nextTailCap after warm-up=8192; test forces it to 256
    zz_verify_c2_test.go:127: warmed+reset pair  1: nextTailCap=256 tailOpenBeforeTiny=true pool.Get sizes while queuing tiny=[] destination cap for tiny=16384 backlogLen=2
    zz_verify_c2_test.go:127: warmed+reset pair  2: nextTailCap=256 tailOpenBeforeTiny=false pool.Get sizes while queuing tiny=[] destination cap for tiny=256 backlogLen=4
    zz_verify_c2_test.go:127: warmed+reset pair  3: nextTailCap=256 tailOpenBeforeTiny=false pool.Get sizes while queuing tiny=[] destination cap for tiny=256 backlogLen=6
    zz_verify_c2_test.go:127: warmed+reset pair 20: nextTailCap=256 tailOpenBeforeTiny=false pool.Get sizes while queuing tiny=[] destination cap for tiny=256 backlogLen=40
    zz_verify_c2_test.go:232: [counterfactual, only alternating traffic pending] WARMED: entries=39 pendingPayload=40979 retainedCap=86784 pooledLiveBytes=81920 nextTailCap=256
    zz_verify_c2_test.go:232: [counterfactual, only alternating traffic pending] WARMED layout (count x [payload/cap]): 20x[2048/4096] 19x[1/256]
    zz_verify_c2_test.go:232: [counterfactual, only alternating traffic pending] FRESH : entries=39 pendingPayload=40979 retainedCap=86784 pooledLiveBytes=81920 nextTailCap=256
    zz_verify_c2_test.go:232: [counterfactual, only alternating traffic pending] FRESH  layout (count x [payload/cap]): 20x[2048/4096] 19x[1/256]
    zz_verify_c2_test.go:232: [counterfactual, only alternating traffic pending] RESULT: warmed-fresh retained capacity = 0 bytes (1.0x)
--- PASS: TestVerifyC2_Counterfactual_ResetNextTailCap (0.00s)
=== RUN   TestVerifyC2_FreshAlternatingControl
    zz_verify_c2_test.go:127: fresh  pair  1: nextTailCap=256 tailOpenBeforeTiny=false pool.Get sizes while queuing tiny=[] destination cap for tiny=256 backlogLen=2
    zz_verify_c2_test.go:127: fresh  pair  2: nextTailCap=256 tailOpenBeforeTiny=false pool.Get sizes while queuing tiny=[] destination cap for tiny=256 backlogLen=4
    zz_verify_c2_test.go:127: fresh  pair  3: nextTailCap=256 tailOpenBeforeTiny=false pool.Get sizes while queuing tiny=[] destination cap for tiny=256 backlogLen=6
    zz_verify_c2_test.go:127: fresh  pair 20: nextTailCap=256 tailOpenBeforeTiny=false pool.Get sizes while queuing tiny=[] destination cap for tiny=256 backlogLen=40
    zz_verify_c2_test.go:244: fresh control: pendingPayload=43028 retainedCap=91136 layout: 21x[2048/4096] 20x[1/256]
--- PASS: TestVerifyC2_FreshAlternatingControl (0.00s)
=== RUN   TestVerifyC2_EvalFixtureAlternatingBound
=== RUN   TestVerifyC2_EvalFixtureAlternatingBound/warmFrames=0
    zz_verify_c2_test.go:286: warmFrames=0 payload=49178 retained=104448 fixtureBound=262248
=== RUN   TestVerifyC2_EvalFixtureAlternatingBound/warmFrames=16384
    zz_verify_c2_test.go:286: warmFrames=16384 payload=65562 retained=497408 fixtureBound=327784
    zz_verify_c2_test.go:288: Alternating frames: retained capacity 497408 exceeds bound 327784 (payload=65562)
--- FAIL: TestVerifyC2_EvalFixtureAlternatingBound (0.00s)
    --- PASS: TestVerifyC2_EvalFixtureAlternatingBound/warmFrames=0 (0.00s)
    --- FAIL: TestVerifyC2_EvalFixtureAlternatingBound/warmFrames=16384 (0.00s)
FAIL
FAIL	google.golang.org/grpc/internal/transport	0.009s
FAIL
== 2. the eval fixture (byte-exact from eval_tests.zip, pass its path as FIXTURE=...) on this branch
$ sha256sum /home/ubuntu/eval_tests/tests/eval_recv_buffer_compaction_test.go
c5e26b9a77345b256970d87313e5527efde93dd7614305fa8fd02d795c23cbbe  /home/ubuntu/eval_tests/tests/eval_recv_buffer_compaction_test.go
--- PASS: TestEval_RecvBufferCompaction (0.00s)
--- PASS: TestEval_RecvBufferCompactionDisabled (0.00s)
--- PASS: TestEval_RecvBufferCompactionSkippedLargeBuffer (0.02s)
--- PASS: TestEval_RecvBufferErrorResetSafety (0.00s)
--- PASS: TestEval_RecvBufferCompaction_MixedFrames (0.00s)
--- PASS: TestEval_RecvBufferCompaction_MultiCycleMemoryBound (0.00s)
PASS
ok  	google.golang.org/grpc/internal/transport	1.052s
```

What the output shows:

- After the 16,384-frame warm-up `nextTailCap=8192`; the pool rounds that request up, so each destination has `cap=16384`.
- Partial drain keeps the backlog nonempty (`backlogLen=1`), so `nextTailCap` stays 8192. Allocation trace for the twenty pairs: `pool.Get sizes while queuing tiny=[8192] destination cap for tiny=16384` for every pair that opens a new destination (pair 1 reuses the tail left open by the warm-up). The fresh buffer allocates `cap=256` for the same one-byte frame.
- Snapshot B (warm-up payload fully consumed, only the alternating traffic pending, backlog never empty): identical pending payload (39 entries, 40,979 bytes) retains **393,216 bytes** in the warmed buffer vs **86,784 bytes** in the matched fresh buffer: +306,432 bytes, 4.5x. Layout: `19x[1/16384]` vs `19x[1/256]`; the 2-KiB frames are identical (`20x[2048/4096]`) in both.
- Snapshot A and the no-drain variant show the same excess (+306,432 and +291,840 bytes).
- Counterfactual: the only difference from the first run is that the test forces `nextTailCap` back to 256 after the warm-up; the excess disappears completely (86,784 vs 86,784, difference 0). That isolates the historical `nextTailCap` as the cause.
- Fixture replay: the eval fixture's alternating-frame bound (`maxAllowedCap := 4*len(wantAltPayload) + 64*1024` over `b.backlog`, from `TestEval_RecvBufferCompaction_MixedFrames`; 327,784 bytes here) holds on a fresh buffer (104,448) and is exceeded once the same traffic follows the warm-up (497,408). The fixture itself, run byte-exact on this branch, passes all six tests, i.e. the existing checks never warm the buffer first and do not catch this.

Impact reasoning: this is production behavior in the default configuration (compaction enabled). Once a stream has been backed up with enough small frames to grow `nextTailCap`, every later small frame that follows a larger frame pins a fresh 16 KiB pooled buffer for as long as the backlog is never fully drained: observed 16,384 bytes of capacity for 1 byte of payload, 4.5x the retained capacity of the same pending data on a fresh stream. The feature exists to keep receive memory proportional to unread payload; here memory depends on the stream's history instead. Triggering it needs a slow reader plus mixed small/large frames after a small-frame burst (the pattern is entirely under the sending peer's control; not measured here: how often ordinary traffic produces it). It is bounded per tiny frame (16 KiB = `http2MaxFrameLen`) and clears as soon as the backlog empties. Workaround: `GRPC_GO_EXPERIMENTAL_ENABLE_RECEIVE_BUFFER_COMPACTION=false`. Fix sketch: reset or shrink `nextTailCap` when a tail is sealed by a non-compactable message (or size the next destination from the bytes currently pending rather than the last destination's capacity), and add a warm-then-alternate case to the tests.

## C3

Claim: the receive-buffer memory-regression tests treat invalid process-wide heap measurements as valid evidence for their memory assertions. Two named parts: (a) invalid zero measurement accepted; (b) unsigned heap-delta wraparound fails the upper-bound assertion. Target: `evalon/grpc-go-tr-8401755a` @ `5418a807865423c63080b5f74bea945af4101602`.

Naming drift: the claim mentions `TestRecvBufferCompaction`; the memory-regression tests on this branch are `TestRecvBuffer_CompactsTinyPayloads` (`internal/transport/recv_buffer_test.go:82`) and `TestServerReceiveBufferCompaction_ManyTinyDataFrames` (`internal/transport/transport_test.go`). `heapAllocBytes()` and `growth := heapAllocBytes() - before` exist under exactly those names.

Replay (`FULL_RUNS=12` adds twelve whole-package runs of the unmodified branch, about 11 s each):

```sh
FULL_RUNS=12 sh verify/repro/c3_heap_delta.sh
```

The arithmetic on the branch (excerpts of `internal/transport/recv_buffer_test.go` lines 32-42, 87 and 97-107):

```go
// heapAllocBytes forces a full garbage collection and returns the number of
// bytes of allocated heap objects that are still live.
func heapAllocBytes() uint64 {
	// Two collections are required so that objects held in a sync.Pool's
	// victim cache are also released.
	runtime.GC()
	runtime.GC()
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	return ms.HeapAlloc
}
	before := heapAllocBytes()
	growth := heapAllocBytes() - before
	t.Logf("Heap grew by %d bytes while queuing %d one-byte payloads", growth, numFrames)
	...
	if maxGrowth := uint64(4*numFrames + 64*1024); growth > maxGrowth {
		t.Fatalf("Heap grew by %d bytes while queuing %d one-byte payloads, want at most %d", growth, numFrames, maxGrowth)
	}
```

`transport_test.go:4369`/`4397`/`4404` use the same helper and the same pattern with `maxGrowth := uint64(1024 * 1024)`. Both operands are `uint64`; there is no ordering check, no validity check on either sample and no lower bound on `growth`; the only memory assertion is `growth > maxGrowth`.

How the probes work: step 2 uses the branch files unmodified and adds only `verify/repro/c3_natural_heap_drop_test.go`, which swaps the default buffer pool for one whose first `Get` drops an unrelated 8 MiB allocation, so the process-wide heap legitimately shrinks between the two samples. Steps 3 and 4 apply `verify/repro/c3_inject_heap_samples.patch` (a verify-only hook inside the test helper `heapAllocBytes()`; this shifts `recv_buffer_test.go` line numbers by 7) and run the branch's two tests unmodified through `verify/repro/c3_injected_samples_test.go`, which scripts the returned samples. Step 4 additionally applies `verify/repro/c3_disable_compaction.patch`, a deliberate regression that makes `put()` never compact.

Observed (full script output):

```text
== worktree /tmp/tmp.aLyFrA2zI0/wt = evalon/grpc-go-tr-8401755a @ 5418a807865423c63080b5f74bea945af4101602
== 0. the arithmetic under test
$ grep -n heapAllocBytes()\|maxGrowth :=\|func heapAllocBytes\|return ms.HeapAlloc internal/transport/recv_buffer_test.go internal/transport/transport_test.go
internal/transport/recv_buffer_test.go:34:func heapAllocBytes() uint64 {
internal/transport/recv_buffer_test.go:41:	return ms.HeapAlloc
internal/transport/recv_buffer_test.go:87:	before := heapAllocBytes()
internal/transport/recv_buffer_test.go:97:	growth := heapAllocBytes() - before
internal/transport/recv_buffer_test.go:105:	if maxGrowth := uint64(4*numFrames + 64*1024); growth > maxGrowth {
internal/transport/transport_test.go:4369:	before := heapAllocBytes()
internal/transport/transport_test.go:4397:	growth := heapAllocBytes() - before
internal/transport/transport_test.go:4404:	if maxGrowth := uint64(1024 * 1024); growth > maxGrowth {
== 1. baseline: both memory tests pass on the unmodified branch
    recv_buffer_test.go:98: Heap grew by 59976 bytes while queuing 50000 one-byte payloads
    transport_test.go:4398: Server heap grew by 66904 bytes while holding 60000 unread one-byte DATA frames
--- PASS: Test (0.07s)
    --- PASS: Test/RecvBuffer_CompactsTinyPayloads (0.00s)
    --- PASS: Test/ServerReceiveBufferCompaction_ManyTinyDataFrames (0.07s)
--- PASS: TestConnectionError_Unwrap (0.00s)
--- PASS: Test (0.00s)
ok  	google.golang.org/grpc/internal/transport	0.077s
== 2. natural trigger, branch files unmodified: 8 MiB of unrelated heap is freed between the two samples
$ git status --short
?? internal/transport/zz_verify_c3_natural_test.go
    recv_buffer_test.go:98: Heap grew by 59496 bytes while queuing 50000 one-byte payloads
    zz_verify_c3_natural_test.go:85: OBSERVED TestRecvBuffer_CompactsTinyPayloads/unrelated_heap_freed_between_samples=none: ballast released inside measurement window=false, branch test passed=true
    recv_buffer_test.go:98: Heap grew by 18446744073701216904 bytes while queuing 50000 one-byte payloads
    recv_buffer_test.go:106: Heap grew by 18446744073701216904 bytes while queuing 50000 one-byte payloads, want at most 265536
    zz_verify_c3_natural_test.go:85: OBSERVED TestRecvBuffer_CompactsTinyPayloads/unrelated_heap_freed_between_samples=8MiB: ballast released inside measurement window=true, branch test passed=false
    transport_test.go:4398: Server heap grew by 65240 bytes while holding 60000 unread one-byte DATA frames
    zz_verify_c3_natural_test.go:85: OBSERVED TestServerReceiveBufferCompaction_ManyTinyDataFrames/unrelated_heap_freed_between_samples=none: ballast released inside measurement window=false, branch test passed=true
    transport_test.go:4398: Server heap grew by 18446744073701229288 bytes while holding 60000 unread one-byte DATA frames
    transport_test.go:4405: Server heap grew by 18446744073701229288 bytes while holding 60000 unread one-byte DATA frames, want at most 1048576
    zz_verify_c3_natural_test.go:85: OBSERVED TestServerReceiveBufferCompaction_ManyTinyDataFrames/unrelated_heap_freed_between_samples=8MiB: ballast released inside measurement window=true, branch test passed=false
--- FAIL: TestVerifyC3Natural (0.16s)
    --- PASS: TestVerifyC3Natural/TestRecvBuffer_CompactsTinyPayloads/unrelated_heap_freed_between_samples=none (0.01s)
    --- FAIL: TestVerifyC3Natural/TestRecvBuffer_CompactsTinyPayloads/unrelated_heap_freed_between_samples=8MiB (0.01s)
    --- PASS: TestVerifyC3Natural/TestServerReceiveBufferCompaction_ManyTinyDataFrames/unrelated_heap_freed_between_samples=none (0.07s)
    --- FAIL: TestVerifyC3Natural/TestServerReceiveBufferCompaction_ManyTinyDataFrames/unrelated_heap_freed_between_samples=8MiB (0.06s)
FAIL
FAIL	google.golang.org/grpc/internal/transport	0.161s
FAIL
== 3. injected samples through a verify-only hook in heapAllocBytes()
$ git diff --stat
 internal/transport/recv_buffer_test.go | 7 +++++++
 1 file changed, 7 insertions(+)
    recv_buffer_test.go:105: Heap grew by 59400 bytes while queuing 50000 one-byte payloads
    zz_verify_c3_inject_test.go:80: OBSERVED TestRecvBuffer_CompactsTinyPayloads / control_no_injection: heap samples=[798040 857440] uint64(second-first)=59400 -> branch test PASSED (measurement accepted)
    recv_buffer_test.go:105: Heap grew by 18446744073709486080 bytes while queuing 50000 one-byte payloads
    recv_buffer_test.go:113: Heap grew by 18446744073709486080 bytes while queuing 50000 one-byte payloads, want at most 265536
    zz_verify_c3_inject_test.go:80: OBSERVED TestRecvBuffer_CompactsTinyPayloads / second_sample_64KiB_lower_than_first: heap samples=[803816 738280] uint64(second-first)=18446744073709486080 -> branch test FAILED (measurement rejected)
    recv_buffer_test.go:105: Heap grew by 18446744073709551615 bytes while queuing 50000 one-byte payloads
    recv_buffer_test.go:113: Heap grew by 18446744073709551615 bytes while queuing 50000 one-byte payloads, want at most 265536
    zz_verify_c3_inject_test.go:80: OBSERVED TestRecvBuffer_CompactsTinyPayloads / second_sample_1_byte_lower_than_first: heap samples=[815240 815239] uint64(second-first)=18446744073709551615 -> branch test FAILED (measurement rejected)
    recv_buffer_test.go:105: Heap grew by 18446744073708735944 bytes while queuing 50000 one-byte payloads
    recv_buffer_test.go:113: Heap grew by 18446744073708735944 bytes while queuing 50000 one-byte payloads, want at most 265536
    zz_verify_c3_inject_test.go:80: OBSERVED TestRecvBuffer_CompactsTinyPayloads / second_sample_invalid_zero: heap samples=[815672 0] uint64(second-first)=18446744073708735944 -> branch test FAILED (measurement rejected)
    recv_buffer_test.go:105: Heap grew by 869664 bytes while queuing 50000 one-byte payloads
    recv_buffer_test.go:113: Heap grew by 869664 bytes while queuing 50000 one-byte payloads, want at most 265536
    zz_verify_c3_inject_test.go:80: OBSERVED TestRecvBuffer_CompactsTinyPayloads / first_sample_invalid_zero: heap samples=[0 869664] uint64(second-first)=869664 -> branch test FAILED (measurement rejected)
    recv_buffer_test.go:105: Heap grew by 0 bytes while queuing 50000 one-byte payloads
    zz_verify_c3_inject_test.go:80: OBSERVED TestRecvBuffer_CompactsTinyPayloads / both_samples_invalid_zero: heap samples=[0 0] uint64(second-first)=0 -> branch test PASSED (measurement accepted)
    transport_test.go:4398: Server heap grew by 66504 bytes while holding 60000 unread one-byte DATA frames
    zz_verify_c3_inject_test.go:80: OBSERVED TestServerReceiveBufferCompaction_ManyTinyDataFrames / control_no_injection: heap samples=[936088 1002592] uint64(second-first)=66504 -> branch test PASSED (measurement accepted)
    transport_test.go:4398: Server heap grew by 18446744073709486080 bytes while holding 60000 unread one-byte DATA frames
    transport_test.go:4405: Server heap grew by 18446744073709486080 bytes while holding 60000 unread one-byte DATA frames, want at most 1048576
    zz_verify_c3_inject_test.go:80: OBSERVED TestServerReceiveBufferCompaction_ManyTinyDataFrames / second_sample_64KiB_lower_than_first: heap samples=[939080 873544] uint64(second-first)=18446744073709486080 -> branch test FAILED (measurement rejected)
    transport_test.go:4398: Server heap grew by 18446744073709551615 bytes while holding 60000 unread one-byte DATA frames
    transport_test.go:4405: Server heap grew by 18446744073709551615 bytes while holding 60000 unread one-byte DATA frames, want at most 1048576
    zz_verify_c3_inject_test.go:80: OBSERVED TestServerReceiveBufferCompaction_ManyTinyDataFrames / second_sample_1_byte_lower_than_first: heap samples=[941104 941103] uint64(second-first)=18446744073709551615 -> branch test FAILED (measurement rejected)
    transport_test.go:4398: Server heap grew by 18446744073708609312 bytes while holding 60000 unread one-byte DATA frames
    transport_test.go:4405: Server heap grew by 18446744073708609312 bytes while holding 60000 unread one-byte DATA frames, want at most 1048576
    zz_verify_c3_inject_test.go:80: OBSERVED TestServerReceiveBufferCompaction_ManyTinyDataFrames / second_sample_invalid_zero: heap samples=[942304 0] uint64(second-first)=18446744073708609312 -> branch test FAILED (measurement rejected)
    transport_test.go:4398: Server heap grew by 1008232 bytes while holding 60000 unread one-byte DATA frames
    zz_verify_c3_inject_test.go:80: OBSERVED TestServerReceiveBufferCompaction_ManyTinyDataFrames / first_sample_invalid_zero: heap samples=[0 1008232] uint64(second-first)=1008232 -> branch test PASSED (measurement accepted)
    transport_test.go:4398: Server heap grew by 0 bytes while holding 60000 unread one-byte DATA frames
    zz_verify_c3_inject_test.go:80: OBSERVED TestServerReceiveBufferCompaction_ManyTinyDataFrames / both_samples_invalid_zero: heap samples=[0 0] uint64(second-first)=0 -> branch test PASSED (measurement accepted)
--- FAIL: TestVerifyC3Inject (0.40s)
    --- PASS: TestVerifyC3Inject/TestRecvBuffer_CompactsTinyPayloads/control_no_injection (0.00s)
    --- FAIL: TestVerifyC3Inject/TestRecvBuffer_CompactsTinyPayloads/second_sample_64KiB_lower_than_first (0.00s)
    --- FAIL: TestVerifyC3Inject/TestRecvBuffer_CompactsTinyPayloads/second_sample_1_byte_lower_than_first (0.00s)
    --- FAIL: TestVerifyC3Inject/TestRecvBuffer_CompactsTinyPayloads/second_sample_invalid_zero (0.00s)
    --- FAIL: TestVerifyC3Inject/TestRecvBuffer_CompactsTinyPayloads/first_sample_invalid_zero (0.00s)
    --- PASS: TestVerifyC3Inject/TestRecvBuffer_CompactsTinyPayloads/both_samples_invalid_zero (0.00s)
    --- PASS: TestVerifyC3Inject/TestServerReceiveBufferCompaction_ManyTinyDataFrames/control_no_injection (0.06s)
    --- FAIL: TestVerifyC3Inject/TestServerReceiveBufferCompaction_ManyTinyDataFrames/second_sample_64KiB_lower_than_first (0.06s)
    --- FAIL: TestVerifyC3Inject/TestServerReceiveBufferCompaction_ManyTinyDataFrames/second_sample_1_byte_lower_than_first (0.06s)
    --- FAIL: TestVerifyC3Inject/TestServerReceiveBufferCompaction_ManyTinyDataFrames/second_sample_invalid_zero (0.06s)
    --- PASS: TestVerifyC3Inject/TestServerReceiveBufferCompaction_ManyTinyDataFrames/first_sample_invalid_zero (0.07s)
    --- PASS: TestVerifyC3Inject/TestServerReceiveBufferCompaction_ManyTinyDataFrames/both_samples_invalid_zero (0.07s)
FAIL
FAIL	google.golang.org/grpc/internal/transport	0.409s
FAIL
== 4. same injection with a real regression present (c3_disable_compaction.patch: queued tiny payloads are never compacted)
$ git diff --stat
 internal/transport/recv_buffer_test.go | 7 +++++++
 internal/transport/transport.go        | 2 +-
 2 files changed, 8 insertions(+), 1 deletion(-)
    recv_buffer_test.go:105: Heap grew by 3142528 bytes while queuing 50000 one-byte payloads
    recv_buffer_test.go:113: Heap grew by 3142528 bytes while queuing 50000 one-byte payloads, want at most 265536
    zz_verify_c3_inject_test.go:80: OBSERVED TestRecvBuffer_CompactsTinyPayloads / control_no_injection: heap samples=[800080 3942608] uint64(second-first)=3142528 -> branch test FAILED (measurement rejected)
    recv_buffer_test.go:105: Heap grew by 0 bytes while queuing 50000 one-byte payloads
    recv_buffer_test.go:124: Draining 50000 compacted payloads took 50000 reads, want far fewer
    zz_verify_c3_inject_test.go:80: OBSERVED TestRecvBuffer_CompactsTinyPayloads / both_samples_invalid_zero: heap samples=[0 0] uint64(second-first)=0 -> branch test FAILED (measurement rejected)
    transport_test.go:4398: Server heap grew by 3850216 bytes while holding 60000 unread one-byte DATA frames
    transport_test.go:4405: Server heap grew by 3850216 bytes while holding 60000 unread one-byte DATA frames, want at most 1048576
    zz_verify_c3_inject_test.go:80: OBSERVED TestServerReceiveBufferCompaction_ManyTinyDataFrames / control_no_injection: heap samples=[939264 4789480] uint64(second-first)=3850216 -> branch test FAILED (measurement rejected)
    transport_test.go:4398: Server heap grew by 0 bytes while holding 60000 unread one-byte DATA frames
    zz_verify_c3_inject_test.go:80: OBSERVED TestServerReceiveBufferCompaction_ManyTinyDataFrames / both_samples_invalid_zero: heap samples=[0 0] uint64(second-first)=0 -> branch test PASSED (measurement accepted)
    --- FAIL: TestVerifyC3Inject/TestRecvBuffer_CompactsTinyPayloads/control_no_injection (0.01s)
    --- FAIL: TestVerifyC3Inject/TestRecvBuffer_CompactsTinyPayloads/both_samples_invalid_zero (0.01s)
    --- FAIL: TestVerifyC3Inject/TestServerReceiveBufferCompaction_ManyTinyDataFrames/control_no_injection (0.07s)
    --- PASS: TestVerifyC3Inject/TestServerReceiveBufferCompaction_ManyTinyDataFrames/both_samples_invalid_zero (0.07s)
FAIL	google.golang.org/grpc/internal/transport	0.168s
== 5. unmodified branch, whole package, 12 runs (set FULL_RUNS=12 to replay; ~11 s per run)
$ git status --short
run 1
    recv_buffer_test.go:98: Heap grew by 54440 bytes while queuing 50000 one-byte payloads
    transport_test.go:4398: Server heap grew by 66760 bytes while holding 60000 unread one-byte DATA frames
ok  	google.golang.org/grpc/internal/transport	12.309s
run 2
    recv_buffer_test.go:98: Heap grew by 54440 bytes while queuing 50000 one-byte payloads
    transport_test.go:4398: Server heap grew by 66760 bytes while holding 60000 unread one-byte DATA frames
ok  	google.golang.org/grpc/internal/transport	11.351s
run 3
    recv_buffer_test.go:98: Heap grew by 54440 bytes while queuing 50000 one-byte payloads
    transport_test.go:4398: Server heap grew by 66728 bytes while holding 60000 unread one-byte DATA frames
ok  	google.golang.org/grpc/internal/transport	11.380s
run 4
    recv_buffer_test.go:98: Heap grew by 54440 bytes while queuing 50000 one-byte payloads
    transport_test.go:4398: Server heap grew by 65560 bytes while holding 60000 unread one-byte DATA frames
ok  	google.golang.org/grpc/internal/transport	12.248s
run 5
    recv_buffer_test.go:98: Heap grew by 54440 bytes while queuing 50000 one-byte payloads
    transport_test.go:4398: Server heap grew by 66760 bytes while holding 60000 unread one-byte DATA frames
ok  	google.golang.org/grpc/internal/transport	12.332s
run 6
    recv_buffer_test.go:98: Heap grew by 54440 bytes while queuing 50000 one-byte payloads
    transport_test.go:4398: Server heap grew by 66760 bytes while holding 60000 unread one-byte DATA frames
ok  	google.golang.org/grpc/internal/transport	11.264s
run 7
    recv_buffer_test.go:98: Heap grew by 54440 bytes while queuing 50000 one-byte payloads
    transport_test.go:4398: Server heap grew by 65336 bytes while holding 60000 unread one-byte DATA frames
ok  	google.golang.org/grpc/internal/transport	12.305s
run 8
    recv_buffer_test.go:98: Heap grew by 54440 bytes while queuing 50000 one-byte payloads
    transport_test.go:4398: Server heap grew by 66760 bytes while holding 60000 unread one-byte DATA frames
ok  	google.golang.org/grpc/internal/transport	11.369s
run 9
    recv_buffer_test.go:98: Heap grew by 54440 bytes while queuing 50000 one-byte payloads
    transport_test.go:4398: Server heap grew by 65336 bytes while holding 60000 unread one-byte DATA frames
ok  	google.golang.org/grpc/internal/transport	12.186s
run 10
    recv_buffer_test.go:98: Heap grew by 54440 bytes while queuing 50000 one-byte payloads
    transport_test.go:4398: Server heap grew by 66824 bytes while holding 60000 unread one-byte DATA frames
ok  	google.golang.org/grpc/internal/transport	11.156s
run 11
    recv_buffer_test.go:98: Heap grew by 54440 bytes while queuing 50000 one-byte payloads
    transport_test.go:4398: Server heap grew by 66760 bytes while holding 60000 unread one-byte DATA frames
ok  	google.golang.org/grpc/internal/transport	12.537s
run 12
    recv_buffer_test.go:98: Heap grew by 54440 bytes while queuing 50000 one-byte payloads
    transport_test.go:4398: Server heap grew by 65528 bytes while holding 60000 unread one-byte DATA frames
ok  	google.golang.org/grpc/internal/transport	11.209s
```

One additional observation, made before the twelve runs above with the same command on the same unmodified commit (scratch worktree clean, `git status --short` empty; note the unshifted line numbers 98 and 4398/4405):

```sh
for i in 1 2 3; do go test ./internal/transport -count=1 -v 2>&1 | grep -E "eap grew|^(ok|FAIL)"; done
```

```text
    recv_buffer_test.go:98: Heap grew by 54440 bytes while queuing 50000 one-byte payloads
    transport_test.go:4398: Server heap grew by 66472 bytes while holding 60000 unread one-byte DATA frames
ok  	google.golang.org/grpc/internal/transport	11.482s
    recv_buffer_test.go:98: Heap grew by 54440 bytes while queuing 50000 one-byte payloads
    transport_test.go:4398: Server heap grew by 66728 bytes while holding 60000 unread one-byte DATA frames
ok  	google.golang.org/grpc/internal/transport	11.341s
    recv_buffer_test.go:98: Heap grew by 54440 bytes while queuing 50000 one-byte payloads
    transport_test.go:4398: Server heap grew by 18446744073709542016 bytes while holding 60000 unread one-byte DATA frames
    transport_test.go:4405: Server heap grew by 18446744073709542016 bytes while holding 60000 unread one-byte DATA frames, want at most 1048576
FAIL
FAIL	google.golang.org/grpc/internal/transport	11.156s
FAIL
```

And eight runs of the eval's own package command (`go test -race ./internal/transport -count=1`, with `-v` added to see the log lines) on the same unmodified commit:

```sh
for i in 1 2 3 4 5 6 7 8; do echo "run $i"; go test -race ./internal/transport -count=1 -v 2>&1 | grep -E "eap grew|^(ok|FAIL)\s"; done
```

```text
run 1
    recv_buffer_test.go:98: Heap grew by 54440 bytes while queuing 50000 one-byte payloads
    transport_test.go:4398: Server heap grew by 66736 bytes while holding 60000 unread one-byte DATA frames
ok  	google.golang.org/grpc/internal/transport	13.962s
run 2
    recv_buffer_test.go:98: Heap grew by 54440 bytes while queuing 50000 one-byte payloads
    transport_test.go:4398: Server heap grew by 66736 bytes while holding 60000 unread one-byte DATA frames
ok  	google.golang.org/grpc/internal/transport	14.122s
run 3
    recv_buffer_test.go:98: Heap grew by 54440 bytes while queuing 50000 one-byte payloads
    transport_test.go:4398: Server heap grew by 66760 bytes while holding 60000 unread one-byte DATA frames
ok  	google.golang.org/grpc/internal/transport	12.840s
run 4
    recv_buffer_test.go:98: Heap grew by 54440 bytes while queuing 50000 one-byte payloads
    transport_test.go:4398: Server heap grew by 66720 bytes while holding 60000 unread one-byte DATA frames
ok  	google.golang.org/grpc/internal/transport	12.969s
run 5
    recv_buffer_test.go:98: Heap grew by 54440 bytes while queuing 50000 one-byte payloads
    transport_test.go:4398: Server heap grew by 65536 bytes while holding 60000 unread one-byte DATA frames
ok  	google.golang.org/grpc/internal/transport	14.262s
run 6
    recv_buffer_test.go:98: Heap grew by 54440 bytes while queuing 50000 one-byte payloads
    transport_test.go:4398: Server heap grew by 66736 bytes while holding 60000 unread one-byte DATA frames
ok  	google.golang.org/grpc/internal/transport	13.012s
run 7
    recv_buffer_test.go:98: Heap grew by 54440 bytes while queuing 50000 one-byte payloads
    transport_test.go:4398: Server heap grew by 65536 bytes while holding 60000 unread one-byte DATA frames
ok  	google.golang.org/grpc/internal/transport	12.838s
run 8
    recv_buffer_test.go:98: Heap grew by 54440 bytes while queuing 50000 one-byte payloads
    transport_test.go:4398: Server heap grew by 60592 bytes while holding 60000 unread one-byte DATA frames
ok  	google.golang.org/grpc/internal/transport	12.768s
```

### Part (b): unsigned heap-delta wraparound

- Injected (step 3): a second sample 1 byte below the first gives `uint64(second-first)=18446744073709551615` and both tests fail their upper-bound assertion (`Heap grew by 18446744073709551615 bytes ..., want at most 265536`; `Server heap grew by 18446744073709551615 bytes ..., want at most 1048576`). 64 KiB lower gives `18446744073709486080` (= 2^64 - 65,536) with the same failures. The control case (no injection) passes.
- Natural, no test-file changes (step 2): freeing 8 MiB of unrelated heap inside the measurement window gives `Heap grew by 18446744073701216904 bytes` / `Server heap grew by 18446744073701228104 bytes` and both tests fail, while the same harness without the release passes. The buffer under test was compacting correctly in every case.
- Natural, nothing added at all: in 1 of 15 whole-package runs of the unmodified branch (`go test ./internal/transport -count=1 -v`), `TestServerReceiveBufferCompaction_ManyTinyDataFrames` failed with `Server heap grew by 18446744073709542016 bytes ..., want at most 1048576` (= 2^64 - 9,600: the second sample was 9,600 bytes below the first because unrelated objects from earlier tests in the package were collected in between). The other 14 runs passed with growth around 65 to 67 KiB, and all 8 `-race` runs passed, so the observed rate is 1 failure in 23 whole-package runs.

### Part (a): invalid zero measurement

- Injected (step 3), both samples zero: `growth` is 0, `Heap grew by 0 bytes` / `Server heap grew by 0 bytes` is logged, the assertion `growth > maxGrowth` is false and both tests pass (`both_samples_invalid_zero ... -> branch test PASSED (measurement accepted)`). Nothing rejects a zero sample or a zero delta.
- Injected (step 3), first sample zero: the server test passes with `heap samples=[0 1007464]` (the whole process heap happens to be under the 1 MiB bound), i.e. a sample that is plainly not a valid baseline is accepted there too; the unit test fails only because 869,584 exceeds its tighter 265,536 bound.
- Does the zero matter, or do other assertions carry the test (step 4)? With compaction really disabled, the control fails as it should (`Heap grew by 3142528 bytes`, `Server heap grew by 3850216 bytes`). With zero samples, `TestServerReceiveBufferCompaction_ManyTinyDataFrames` **passes** although the server is holding about 3.8 MB for 60,000 bytes: its memory assertion is the only thing in that test that detects the regression, and it accepted the zero. `TestRecvBuffer_CompactsTinyPayloads` also passes its memory assertion on the zero (`Heap grew by 0 bytes`, no `want at most` failure) and is only caught later by an unrelated read-count check (`Draining 50000 compacted payloads took 50000 reads, want far fewer`).
- Limit of this part: on this branch `heapAllocBytes()` returns the raw `ms.HeapAlloc` with no clamping, so a zero does not arise on its own; it was only ever seen when injected. A second sample of exactly zero with a nonzero first sample falls under part (b) (wraps, test fails).

Impact reasoning: test-quality defect in the regression coverage, not production behavior. Part (b) makes the two memory tests flaky in the false-failure direction whenever anything else in the process frees more than the buffer under test retains between the two samples; the failure was observed once in 23 unmodified whole-package runs (1 of 15 without `-race`, 0 of 8 with `-race`), with a message ("heap grew by 18446744073709542016 bytes") that sends the reader looking for a leak that is not there. Part (a) is latent on this branch (needs a zero from somewhere, e.g. a later change that clamps negative deltas to zero to cure the flake), but when it occurs the server-level test has no other line of defense. Workaround: rerun. Fix sketch: assert on direct backlog observations (entry count and summed `cap()` of the queued buffers) instead of process-wide `HeapAlloc`; if a heap delta is kept, compute it signed, treat `after < before` as "measurement invalid, retry" rather than as pass or fail, and never accept a zero/clamped delta as evidence.

## C4

Claim: a fresh receive buffer retains more than 1 KiB of payload-backing capacity after receiving three one-byte frames without consumption. Target: `evalon/grpc-go-tr-ad9c633e` @ `2db5dd3c196fac4b01e4f0443b1ac76cbb03e026`.

Replay:

```sh
FIXTURE=~/eval_tests/tests/eval_recv_buffer_compaction_test.go sh verify/repro/c4_three_one_byte_frames.sh
```

The script copies `verify/repro/c4_three_one_byte_frames_test.go` into `internal/transport/` of the scratch worktree (build tag `verify_repro`, production files untouched) and runs `go test -tags verify_repro -run 'TestVerifyC4' -v ./internal/transport -count=1`. The test puts three separately allocated one-byte frames into a fresh `recvBuffer` without reading, then walks the channel entry and every backlog entry, printing payload length, `cap()` and the backing array address, and sums the capacity of each distinct backing array once. It repeats that through the production `http2Server.handleData` path, with compaction disabled, and with two frames; a last test measures live heap across 10,000 such buffers.

Mechanism on the branch (`internal/transport/transport.go:122-146`): `recvBufferCompactionSize = 4 * 1024`, and `compact()` starts a compaction run with `buf := make(mem.SliceBuffer, 0, recvBufferCompactionSize)` as soon as a small message arrives while the last backlog entry is also small, regardless of how many bytes are pending. Frame 1 goes straight to the channel, frame 2 becomes the first backlog entry, frame 3 triggers the allocation.

Observed (full script output):

```text
== worktree /tmp/tmp.wygCKU5wJz/wt = evalon/grpc-go-tr-ad9c633e @ 2db5dd3c196fac4b01e4f0443b1ac76cbb03e026
== 1. retained capacity after three unread one-byte frames (component level, production handleData path, live heap)
=== RUN   TestVerifyC4_ThreeOneByteFrames_RecvBuffer
=== RUN   TestVerifyC4_ThreeOneByteFrames_RecvBuffer/frames=3/compaction=true
    zz_verify_c4_test.go:34:   chan       payload=1 cap=1 backing=0xc00011aca0 alreadyCounted=false
    zz_verify_c4_test.go:34:   backlog[0] payload=2 cap=4096 backing=0xc0002f5000 alreadyCounted=false
    zz_verify_c4_test.go:64: RESULT frames=3 compaction=true: pendingPayload=3 bytes, retained payload-backing capacity=4097 bytes (1 KiB = 1024)
    zz_verify_c4_test.go:66: C4 CONFIRMED: 3 one-byte frames retain 4097 bytes of backing capacity (> 1024)
=== RUN   TestVerifyC4_ThreeOneByteFrames_RecvBuffer/frames=3/compaction=false
    zz_verify_c4_test.go:34:   chan       payload=1 cap=1 backing=0xc00011ad20 alreadyCounted=false
    zz_verify_c4_test.go:34:   backlog[0] payload=1 cap=1 backing=0xc00011ad21 alreadyCounted=false
    zz_verify_c4_test.go:34:   backlog[1] payload=1 cap=1 backing=0xc00011ad22 alreadyCounted=false
    zz_verify_c4_test.go:64: RESULT frames=3 compaction=false: pendingPayload=3 bytes, retained payload-backing capacity=3 bytes (1 KiB = 1024)
=== RUN   TestVerifyC4_ThreeOneByteFrames_RecvBuffer/frames=2/compaction=true
    zz_verify_c4_test.go:34:   chan       payload=1 cap=1 backing=0xc00011ad90 alreadyCounted=false
    zz_verify_c4_test.go:34:   backlog[0] payload=1 cap=1 backing=0xc00011ad91 alreadyCounted=false
    zz_verify_c4_test.go:64: RESULT frames=2 compaction=true: pendingPayload=2 bytes, retained payload-backing capacity=2 bytes (1 KiB = 1024)
--- FAIL: TestVerifyC4_ThreeOneByteFrames_RecvBuffer (0.00s)
    --- FAIL: TestVerifyC4_ThreeOneByteFrames_RecvBuffer/frames=3/compaction=true (0.00s)
    --- PASS: TestVerifyC4_ThreeOneByteFrames_RecvBuffer/frames=3/compaction=false (0.00s)
    --- PASS: TestVerifyC4_ThreeOneByteFrames_RecvBuffer/frames=2/compaction=true (0.00s)
=== RUN   TestVerifyC4_ThreeOneByteFrames_ServerHandleData
=== RUN   TestVerifyC4_ThreeOneByteFrames_ServerHandleData/compaction=true
    zz_verify_c4_test.go:34:   chan       payload=1 cap=1 backing=0xc00011ae38 alreadyCounted=false
    zz_verify_c4_test.go:34:   backlog[0] payload=2 cap=4096 backing=0xc00030a000 alreadyCounted=false
    zz_verify_c4_test.go:106: RESULT handleData compaction=true: pendingPayload=3 bytes, retained payload-backing capacity=4097 bytes
    zz_verify_c4_test.go:108: C4 CONFIRMED on the production path: 3 one-byte DATA frames retain 4097 bytes of backing capacity (> 1024)
=== RUN   TestVerifyC4_ThreeOneByteFrames_ServerHandleData/compaction=false
    zz_verify_c4_test.go:34:   chan       payload=1 cap=1 backing=0xc00011aee8 alreadyCounted=false
    zz_verify_c4_test.go:34:   backlog[0] payload=1 cap=1 backing=0xc00011aee9 alreadyCounted=false
    zz_verify_c4_test.go:34:   backlog[1] payload=1 cap=1 backing=0xc00011aeea alreadyCounted=false
    zz_verify_c4_test.go:106: RESULT handleData compaction=false: pendingPayload=3 bytes, retained payload-backing capacity=3 bytes
--- FAIL: TestVerifyC4_ThreeOneByteFrames_ServerHandleData (0.00s)
    --- FAIL: TestVerifyC4_ThreeOneByteFrames_ServerHandleData/compaction=true (0.00s)
    --- PASS: TestVerifyC4_ThreeOneByteFrames_ServerHandleData/compaction=false (0.00s)
=== RUN   TestVerifyC4_ThreeOneByteFrames_HeapPerBuffer
=== RUN   TestVerifyC4_ThreeOneByteFrames_HeapPerBuffer/compaction=true
    zz_verify_c4_test.go:140: RESULT compaction=true: 10000 buffers x 3 unread one-byte frames (30000 payload bytes) retain 41790032 live heap bytes = 4179 bytes per buffer
=== RUN   TestVerifyC4_ThreeOneByteFrames_HeapPerBuffer/compaction=false
    zz_verify_c4_test.go:140: RESULT compaction=false: 10000 buffers x 3 unread one-byte frames (30000 payload bytes) retain 1389984 live heap bytes = 138 bytes per buffer
--- PASS: TestVerifyC4_ThreeOneByteFrames_HeapPerBuffer (0.04s)
    --- PASS: TestVerifyC4_ThreeOneByteFrames_HeapPerBuffer/compaction=true (0.03s)
    --- PASS: TestVerifyC4_ThreeOneByteFrames_HeapPerBuffer/compaction=false (0.01s)
FAIL
FAIL	google.golang.org/grpc/internal/transport	0.044s
FAIL
== 2. the eval fixture (byte-exact from eval_tests.zip, pass its path as FIXTURE=...) on this branch
$ sha256sum /home/ubuntu/eval_tests/tests/eval_recv_buffer_compaction_test.go
c5e26b9a77345b256970d87313e5527efde93dd7614305fa8fd02d795c23cbbe  /home/ubuntu/eval_tests/tests/eval_recv_buffer_compaction_test.go
--- PASS: TestEval_RecvBufferCompaction (0.00s)
--- PASS: TestEval_RecvBufferCompactionDisabled (0.00s)
--- PASS: TestEval_RecvBufferCompactionSkippedLargeBuffer (0.01s)
--- PASS: TestEval_RecvBufferErrorResetSafety (0.00s)
--- PASS: TestEval_RecvBufferCompaction_MixedFrames (0.00s)
--- PASS: TestEval_RecvBufferCompaction_MultiCycleMemoryBound (0.00s)
PASS
ok  	google.golang.org/grpc/internal/transport	1.036s
```

What the output shows:

- Fresh buffer, three one-byte frames, nothing consumed, compaction enabled (the default): the channel holds frame 1 (`payload=1 cap=1`), the backlog holds one entry with frames 2 and 3 (`payload=2 cap=4096`); the two backing arrays are distinct (`alreadyCounted=false`), total **4,097 bytes** of retained payload-backing capacity for 3 bytes of payload, which is more than 1,024.
- Same result through the production DATA-frame path (`http2Server.handleData`): 4,097 bytes.
- Controls: with compaction disabled the same three frames retain 3 bytes (three `cap=1` arrays); with two frames and compaction enabled, 2 bytes. The third unread frame is what triggers the 4 KiB allocation.
- Live heap, 10,000 buffers each holding three unread one-byte frames: 41,790,032 bytes (4,179 per buffer) with compaction enabled vs 1,389,984 bytes (138 per buffer) with it disabled, so in this situation the feature costs about 30x more memory than the behavior it replaces.
- The eval fixture, run byte-exact on this branch, passes all six tests: its bounds are checked at tens of thousands of frames, where a 4 KiB floor is invisible.

Impact reasoning: production behavior in the default configuration, on the everyday path. Any stream whose reader is three small messages behind (three unread DATA frames of at most 1,024 bytes) pins a 4 KiB heap buffer for a handful of bytes; nothing unusual is required of the peer. For one stream that is negligible; across many concurrently backed-up streams it is a regression against the pre-change behavior the task set out to improve (observed 41.8 MB vs 1.4 MB for 10,000 streams with 3 bytes pending each), and it contradicts the stated goal of memory proportional to unread payload. It is bounded (4 KiB per compaction run, released when the entry is read). Workaround: `GRPC_GO_EXPERIMENTAL_ENABLE_RECEIVE_BUFFER_COMPACTION=false`. Fix sketch: start small and grow (size the first destination from the pending bytes, e.g. 64 to 256 bytes doubling up to the cap) or defer compaction until the queued small payload justifies a 4 KiB buffer, and add a few-frames capacity bound to the tests.
