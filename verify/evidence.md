Observations for run `v-3c93b814`. Verdict words here are the raw outcome of each experiment.

## Setup

Claim branches live in a second repository, fetched as remote `claims`; each was checked out in its own worktree. The fixture `eval_recv_buffer_compaction_test.go` (sha256 `fc33c6d17056f6ac45f8c94c8f3459bac022c948e7885a6776c327d6761544f3`) was copied byte-exact from `eval_tests.zip` wherever it is used. Go 1.25.7, linux/amd64, 8 CPUs.

```sh
cd ~/repos/grpc-go
git remote add claims https://github.com/kaitranntt-evals/grpc-go-transport-restrict-memory-overhead
for s in 34099504 0e684571 64726da8 a4df28bb; do
  git fetch claims evalon/grpc-go-tr-$s:refs/remotes/claims/evalon/grpc-go-tr-$s
  git worktree add --detach ~/wt/$s claims/evalon/grpc-go-tr-$s
done
unzip eval_tests.zip -d ~/evalfx
```

Commits adjudicated: 34099504 → `f9188394`, 0e684571 → `b865cc0e`, 64726da8 → `00c344c5`, a4df28bb → `4a9231f0`. No production file was modified in any worktree; only test files under `internal/transport/` were added (the repros in `verify/repro/` and the eval fixture).

## C1

Target: [evalon/grpc-go-tr-34099504](https://github.com/kaitranntt-evals/grpc-go-transport-restrict-memory-overhead/tree/evalon/grpc-go-tr-34099504). Verdict: **REFUTED**.

Fixture first — it fails, but it only measures "pool allocations outstanding after drain", not who owns them:

```sh
cp ~/evalfx/tests/eval_recv_buffer_compaction_test.go ~/wt/34099504/internal/transport/
cd ~/wt/34099504 && go test -v -run '^TestEval_RecvBufferConfiguredPoolAcquisition$' google.golang.org/grpc/internal/transport -race -count=1
```

```console
=== RUN   TestEval_RecvBufferConfiguredPoolAcquisition
=== RUN   TestEval_RecvBufferConfiguredPoolAcquisition/UnpooledConsolidation
    eval_recv_buffer_compaction_test.go:721: Tracking pool: 1 acquired destination buffers were abandoned and never returned
--- FAIL: TestEval_RecvBufferConfiguredPoolAcquisition (0.00s)
    --- FAIL: TestEval_RecvBufferConfiguredPoolAcquisition/UnpooledConsolidation (0.00s)
FAIL
FAIL	google.golang.org/grpc/internal/transport	0.015s
FAIL
```

Ownership trace ([verify/repro/c1_c2_ownership_test.go](repro/c1_c2_ownership_test.go)): a real `http2Client` stream on a tracing pool, the fixture's input (1026 one-byte payloads) plus 16385 and 50001, drained and freed with the stream open; then 500 more payloads; then EOF. For each outstanding pool allocation the test prints the acquiring call stack and whether it is the backing array of the stream's live `recvBuffer.compactBuf`.

```sh
cp verify/repro/c1_c2_ownership_test.go ~/wt/34099504/internal/transport/verify_c1_c2_ownership_test.go
cd ~/wt/34099504 && go test -v -run '^TestVerify_C[12]_' ./internal/transport -race -count=1
```

```console
=== RUN   TestVerify_C1_DrainedOpenStreamOwnership
=== RUN   TestVerify_C1_DrainedOpenStreamOwnership/1026
    verify_c1_c2_ownership_test.go:146: n=1026: stream state after drain: 0 (streamActive=0), recvBuffer.err=<nil>
    verify_c1_c2_ownership_test.go:147: after drain+free, stream open: pool gets=1 puts=0 outstanding=1 | recvBuffer.compactBuf live=true pending=[1025,1025) err=<nil>
    verify_c1_c2_ownership_test.go:147: after drain+free, stream open:   outstanding 0xc000324000 cap=16384 acquired by transport.(*recvBuffer).compact <- transport.(*recvBuffer).put <- transport.verifyC1Run <- transport.TestVerify_C1_DrainedOpenStreamOwnership.func1 | backs recvBuffer.compactBuf=true
    verify_c1_c2_ownership_test.go:160: subsequent 500 one-byte payloads: new pool gets=0 puts=0, same compaction destination=true
    verify_c1_c2_ownership_test.go:164: after second drain+free, stream open: pool gets=1 puts=0 outstanding=1 | recvBuffer.compactBuf live=true pending=[1524,1524) err=<nil>
    verify_c1_c2_ownership_test.go:164: after second drain+free, stream open:   outstanding 0xc000324000 cap=16384 acquired by transport.(*recvBuffer).compact <- transport.(*recvBuffer).put <- transport.verifyC1Run <- transport.TestVerify_C1_DrainedOpenStreamOwnership.func1 | backs recvBuffer.compactBuf=true
    verify_c1_c2_ownership_test.go:171: after stream termination (EOF): pool gets=1 puts=1 outstanding=0 | recvBuffer.compactBuf live=false pending=[0,0) err=EOF
    verify_c1_c2_ownership_test.go:173: RESULT C1 n=1026: after drain outstanding=1 ownedByLiveRecvBuffer=1 orphaned=0; after 2nd burst outstanding=1 owned=1 orphaned=0; outstanding after termination=0
=== RUN   TestVerify_C1_DrainedOpenStreamOwnership/16385
    verify_c1_c2_ownership_test.go:146: n=16385: stream state after drain: 0 (streamActive=0), recvBuffer.err=<nil>
    verify_c1_c2_ownership_test.go:147: after drain+free, stream open: pool gets=1 puts=0 outstanding=1 | recvBuffer.compactBuf live=true pending=[16384,16384) err=<nil>
    verify_c1_c2_ownership_test.go:147: after drain+free, stream open:   outstanding 0xc000276000 cap=16384 acquired by transport.(*recvBuffer).compact <- transport.(*recvBuffer).put <- transport.verifyC1Run <- transport.TestVerify_C1_DrainedOpenStreamOwnership.func1 | backs recvBuffer.compactBuf=true
    verify_c1_c2_ownership_test.go:160: subsequent 500 one-byte payloads: new pool gets=1 puts=1, same compaction destination=true
    verify_c1_c2_ownership_test.go:164: after second drain+free, stream open: pool gets=2 puts=1 outstanding=1 | recvBuffer.compactBuf live=true pending=[499,499) err=<nil>
    verify_c1_c2_ownership_test.go:164: after second drain+free, stream open:   outstanding 0xc000276000 cap=16384 acquired by transport.(*recvBuffer).compact <- transport.(*recvBuffer).put <- transport.verifyC1Run <- transport.TestVerify_C1_DrainedOpenStreamOwnership.func1 | backs recvBuffer.compactBuf=true
    verify_c1_c2_ownership_test.go:171: after stream termination (EOF): pool gets=2 puts=2 outstanding=0 | recvBuffer.compactBuf live=false pending=[0,0) err=EOF
    verify_c1_c2_ownership_test.go:173: RESULT C1 n=16385: after drain outstanding=1 ownedByLiveRecvBuffer=1 orphaned=0; after 2nd burst outstanding=1 owned=1 orphaned=0; outstanding after termination=0
=== RUN   TestVerify_C1_DrainedOpenStreamOwnership/50001
    verify_c1_c2_ownership_test.go:146: n=50001: stream state after drain: 0 (streamActive=0), recvBuffer.err=<nil>
    verify_c1_c2_ownership_test.go:147: after drain+free, stream open: pool gets=4 puts=3 outstanding=1 | recvBuffer.compactBuf live=true pending=[848,848) err=<nil>
    verify_c1_c2_ownership_test.go:147: after drain+free, stream open:   outstanding 0xc000422000 cap=16384 acquired by transport.(*recvBuffer).compact <- transport.(*recvBuffer).put <- transport.verifyC1Run <- transport.TestVerify_C1_DrainedOpenStreamOwnership.func1 | backs recvBuffer.compactBuf=true
    verify_c1_c2_ownership_test.go:160: subsequent 500 one-byte payloads: new pool gets=0 puts=0, same compaction destination=true
    verify_c1_c2_ownership_test.go:164: after second drain+free, stream open: pool gets=4 puts=3 outstanding=1 | recvBuffer.compactBuf live=true pending=[1347,1347) err=<nil>
    verify_c1_c2_ownership_test.go:164: after second drain+free, stream open:   outstanding 0xc000422000 cap=16384 acquired by transport.(*recvBuffer).compact <- transport.(*recvBuffer).put <- transport.verifyC1Run <- transport.TestVerify_C1_DrainedOpenStreamOwnership.func1 | backs recvBuffer.compactBuf=true
    verify_c1_c2_ownership_test.go:171: after stream termination (EOF): pool gets=4 puts=4 outstanding=0 | recvBuffer.compactBuf live=false pending=[0,0) err=EOF
    verify_c1_c2_ownership_test.go:173: RESULT C1 n=50001: after drain outstanding=1 ownedByLiveRecvBuffer=1 orphaned=0; after 2nd burst outstanding=1 owned=1 orphaned=0; outstanding after termination=0
--- PASS: TestVerify_C1_DrainedOpenStreamOwnership (0.04s)
```

Reading: after drain the stream is still `streamActive`, exactly one allocation is outstanding in every case, and it is the one backing the live `recvBuffer.compactBuf` (`orphaned=0`). The next burst is copied into that same destination with no new `Get` (n=1026, n=50001); when the destination is exactly full (n=16385) it is returned (`puts=1`) and replaced. Destinations that rolled over during the 50001 run were all returned (gets=4 puts=3). After EOF nothing is outstanding. So the storage is retained by a live receive-buffer owner for subsequent input, which is the claim's refute condition; the fixture's FAIL reflects that retention, not an ownerless allocation.

## C2

Target: [evalon/grpc-go-tr-34099504](https://github.com/kaitranntt-evals/grpc-go-transport-restrict-memory-overhead/tree/evalon/grpc-go-tr-34099504). Verdict: **CONFIRMED** (both parts).

Same repro file, server side: a real `http2Server` (`NewServerTransport`, `ServerConfig.BufferPool` = tracing pool) and a raw HTTP/2 client that sends HEADERS and 1026 one-byte DATA frames without END_STREAM. A PING/ack round trip guarantees the server processed every frame before the application reads. The application then reads and frees all 1026 bytes via `ServerStream.Read`, and the client sends `RST_STREAM(CANCEL)`. Control run: identical, but terminated with END_STREAM.

```sh
cp verify/repro/c1_c2_ownership_test.go ~/wt/34099504/internal/transport/verify_c1_c2_ownership_test.go
cd ~/wt/34099504 && go test -v -run '^TestVerify_C[12]_' ./internal/transport -race -count=1
```

```console
    --- PASS: TestVerify_C1_DrainedOpenStreamOwnership/1026 (0.00s)
    --- PASS: TestVerify_C1_DrainedOpenStreamOwnership/16385 (0.01s)
    --- PASS: TestVerify_C1_DrainedOpenStreamOwnership/50001 (0.03s)
=== RUN   TestVerify_C2_ServerCancelWithoutEOF
    verify_c1_c2_ownership_test.go:318: all DATA received, nothing read: pool gets=1 puts=0 outstanding=1 | recvBuffer.compactBuf live=true pending=[0,1025) err=<nil>
    verify_c1_c2_ownership_test.go:318: all DATA received, nothing read:   outstanding 0xc000466000 cap=16384 acquired by transport.(*recvBuffer).compact <- transport.(*recvBuffer).put <- transport.(*Stream).write <- transport.(*http2Server).handleData | backs recvBuffer.compactBuf=true
    verify_c1_c2_ownership_test.go:332: after reading+freeing all delivered buffers: pool gets=1 puts=0 outstanding=1 | recvBuffer.compactBuf live=true pending=[1025,1025) err=<nil>
    verify_c1_c2_ownership_test.go:332: after reading+freeing all delivered buffers:   outstanding 0xc000466000 cap=16384 acquired by transport.(*recvBuffer).compact <- transport.(*recvBuffer).put <- transport.(*Stream).write <- transport.(*http2Server).handleData | backs recvBuffer.compactBuf=true
    verify_c1_c2_ownership_test.go:355: after RST_STREAM: ctx.Err=context canceled state==streamDone:true inActiveStreams=false recvBuffer.err=<nil>
    verify_c1_c2_ownership_test.go:357: application Read after cancellation returns: rpc error: code = Canceled desc = context canceled
    verify_c1_c2_ownership_test.go:363: after rst termination: pool gets=1 puts=0 outstanding=1 | recvBuffer.compactBuf live=true pending=[1025,1025) err=<nil>
    verify_c1_c2_ownership_test.go:363: after rst termination:   outstanding 0xc000466000 cap=16384 acquired by transport.(*recvBuffer).compact <- transport.(*recvBuffer).put <- transport.(*Stream).write <- transport.(*http2Server).handleData | backs recvBuffer.compactBuf=true
    verify_c1_c2_ownership_test.go:367: after server transport Close: pool gets=1 puts=0 outstanding=1 | recvBuffer.compactBuf live=true pending=[1025,1025) err=<nil>
    verify_c1_c2_ownership_test.go:367: after server transport Close:   outstanding 0xc000466000 cap=16384 acquired by transport.(*recvBuffer).compact <- transport.(*recvBuffer).put <- transport.(*Stream).write <- transport.(*http2Server).handleData | backs recvBuffer.compactBuf=true
    verify_c1_c2_ownership_test.go:375: RESULT C2 (RST_STREAM, no EOF): outstanding afterReads=1 (owned by recvBuffer.compactBuf=1) afterCancel=1 afterTransportClose=1
    verify_c1_c2_ownership_test.go:377: 1 compaction destination(s) acquired from the configured pool were never returned after cancellation
--- FAIL: TestVerify_C2_ServerCancelWithoutEOF (0.41s)
=== RUN   TestVerify_C2_ControlEOF
    verify_c1_c2_ownership_test.go:318: all DATA received, nothing read: pool gets=1 puts=0 outstanding=1 | recvBuffer.compactBuf live=true pending=[0,1025) err=<nil>
    verify_c1_c2_ownership_test.go:318: all DATA received, nothing read:   outstanding 0xc000324000 cap=16384 acquired by transport.(*recvBuffer).compact <- transport.(*recvBuffer).put <- transport.(*Stream).write <- transport.(*http2Server).handleData | backs recvBuffer.compactBuf=true
    verify_c1_c2_ownership_test.go:332: after reading+freeing all delivered buffers: pool gets=1 puts=0 outstanding=1 | recvBuffer.compactBuf live=true pending=[1025,1025) err=<nil>
    verify_c1_c2_ownership_test.go:332: after reading+freeing all delivered buffers:   outstanding 0xc000324000 cap=16384 acquired by transport.(*recvBuffer).compact <- transport.(*recvBuffer).put <- transport.(*Stream).write <- transport.(*http2Server).handleData | backs recvBuffer.compactBuf=true
    verify_c1_c2_ownership_test.go:360: application Read after END_STREAM returns: EOF
    verify_c1_c2_ownership_test.go:363: after eof termination: pool gets=1 puts=1 outstanding=0 | recvBuffer.compactBuf live=false pending=[0,0) err=EOF
    verify_c1_c2_ownership_test.go:367: after server transport Close: pool gets=1 puts=1 outstanding=0 | recvBuffer.compactBuf live=false pending=[0,0) err=EOF
    verify_c1_c2_ownership_test.go:384: RESULT control (END_STREAM): outstanding afterReads=1 (owned by recvBuffer.compactBuf=1) afterEOF=0 afterTransportClose=0
--- PASS: TestVerify_C2_ControlEOF (0.41s)
FAIL
FAIL	google.golang.org/grpc/internal/transport	0.877s
FAIL
```

- *Retained destination ownership* — CONFIRMED: after every delivered buffer is freed, `pool gets=1 puts=0`; the outstanding 16384-byte allocation was acquired by `recvBuffer.compact <- recvBuffer.put <- Stream.write <- http2Server.handleData` and backs `recvBuffer.compactBuf` (`live=true`, empty pending segment `[1025,1025)`). That is a reference separate from the delivered slices.
- *Cancellation cleanup* — CONFIRMED: after RST_STREAM the stream context is canceled, state is `streamDone`, the stream is gone from `activeStreams`, and the application's `Read` returns `Canceled` — yet `recvBuffer.err=<nil>` (no error was delivered to the receive buffer) and `compactBuf` is still live. The allocation is still outstanding 200 ms later and after `http2Server.Close`. The END_STREAM control returns it (`puts=1 outstanding=0`).

Impact reasoning: a client cancelling an RPC (RST_STREAM, deadline, disconnect) is an everyday event. On this branch every server stream that compacted at least once and is then cancelled without END_STREAM never hands its 16 KiB compaction buffer back to the configured `mem.BufferPool`; the pool sees a `Get` with no matching `Put`. What I observed is the missing `Put`; I did not measure process memory. With the escape hatch the path is not taken:

```sh
cd ~/wt/34099504 && GRPC_GO_EXPERIMENTAL_ENABLE_RECEIVE_BUFFER_COMPACTION=false go test -v -run '^TestVerify_C2_ServerCancelWithoutEOF$' ./internal/transport -race -count=1
```

```console
    verify_c1_c2_ownership_test.go:375: RESULT C2 (RST_STREAM, no EOF): outstanding afterReads=0 (owned by recvBuffer.compactBuf=0) afterCancel=0 afterTransportClose=0
--- PASS: TestVerify_C2_ServerCancelWithoutEOF (0.41s)
ok  	google.golang.org/grpc/internal/transport	1.425s
```

The eval fixture has no server-cancel scenario (its pool checks all terminate with `io.EOF` or not at all), so it cannot see this.

## C3

Target: [evalon/grpc-go-tr-0e684571](https://github.com/kaitranntt-evals/grpc-go-transport-restrict-memory-overhead/tree/evalon/grpc-go-tr-0e684571). Verdict: **CONFIRMED** (both parts).

Worker creation and cleanup in the branch's `internal/transport/recv_buffer_test.go` (`startTinyDataFrameServer`):

```console
$ grep -n "t.Cleanup\|serverDone\|go func" internal/transport/recv_buffer_test.go
311:	t.Cleanup(func() { lis.Close() })
313:	serverDone := make(chan struct{})
314:	go func() {
315:		defer close(serverDone)
321:		t.Cleanup(func() { conn.Close() })
365:		go func() {
403:		case <-serverDone:
```

Two socket workers are started: the accept/writer goroutine (line 314) and a frame-reader goroutine (line 365). `serverDone` is only read by the returned `waitForServer` closure (line 403), which the test calls on the success path; the two `t.Cleanup` functions only close sockets. Nothing waits for the frame reader at all. The writer reports its errors with `t.Errorf`.

Experiment ([verify/repro/c3_worker_teardown_test.go](repro/c3_worker_teardown_test.go)): the branch's unmodified `startTinyDataFrameServer` is called inside a subtest that then fails the way the real test does; `t.Run` returns only after the subtest's cleanups have run, and the parent then dumps goroutines and lists those running a `startTinyDataFrameServer.func*` closure.

- `setup`: `NewHTTP2Client` fails (connect context already done) → the test's `t.Fatalf("NewHTTP2Client failed")` branch.
- `reception`: the peer stops reading, the writer blocks in `Write`, and the fixture's own `waitForServer` takes its `ctx.Done()` → `t.Fatalf` branch.
- `success`: control.

```sh
cp verify/repro/c3_worker_teardown_test.go ~/wt/0e684571/internal/transport/verify_c3_worker_teardown_test.go
cd ~/wt/0e684571 && go test -c -o /tmp/c3.test ./internal/transport
VERIFY_C3_SCENARIO=setup GOMAXPROCS=1 /tmp/c3.test -test.v -test.run '^TestVerify_C3_WorkerAfterCleanup$'
```

```console
=== RUN   TestVerify_C3_WorkerAfterCleanup
=== RUN   TestVerify_C3_WorkerAfterCleanup/fixture
    verify_c3_worker_teardown_test.go:76: NewHTTP2Client failed: connection error: desc = "transport: Error while dialing: dial tcp 127.0.0.1:44041: operation was canceled"
RESULT C3 scenario=setup
  worker alive at failure point (before cleanup): startTinyDataFrameServer.func2 [runnable] at google.golang.org/grpc/internal/transport.startTinyDataFrameServer.func2()
  workers alive after fixture cleanup completed: 1
  worker alive AFTER fixture cleanup completed: startTinyDataFrameServer.func2 [runnable] at google.golang.org/grpc/internal/transport.startTinyDataFrameServer.func2()
=== NAME  TestVerify_C3_WorkerAfterCleanup
    recv_buffer_test.go:318: Server failed to accept connection: accept tcp 127.0.0.1:44041: use of closed network connection
panic: Fail in goroutine after TestVerify_C3_WorkerAfterCleanup/fixture has completed

goroutine 11 [running]:
testing.(*common).Fail(0xc00017c700)
	/usr/local/go/src/testing/testing.go:960 +0xca
testing.(*common).Errorf(0xc00017c700, {0xc03f1f?, 0xcf4340?}, {0xc000065770?, 0xe26c7b?, 0xbe6734?})
	/usr/local/go/src/testing/testing.go:1205 +0x59
google.golang.org/grpc/internal/transport.startTinyDataFrameServer.func2()
	/home/ubuntu/wt/0e684571/internal/transport/recv_buffer_test.go:318 +0x175
```

One `reception` run (default `GOMAXPROCS=8`) in which the worker lost the race:

```sh
VERIFY_C3_SCENARIO=reception GOMAXPROCS=8 /tmp/c3.test -test.v -test.run '^TestVerify_C3_WorkerAfterCleanup$'
```

```console
=== RUN   TestVerify_C3_WorkerAfterCleanup
=== RUN   TestVerify_C3_WorkerAfterCleanup/fixture
    recv_buffer_test.go:402: Test timed out waiting for the server to finish writing
    recv_buffer_test.go:383: Server failed to write DATA frame: write tcp 127.0.0.1:38607->127.0.0.1:41950: write: connection reset by peer
RESULT C3 scenario=reception
  worker alive at failure point (before cleanup): startTinyDataFrameServer.func2 [IO wait] at internal/poll.runtime_pollWait(0x7c4484289c00, 0x77)
  worker alive at failure point (before cleanup): startTinyDataFrameServer.func2.2 [IO wait] at internal/poll.runtime_pollWait(0x7c4484289c00, 0x72)
  workers alive after fixture cleanup completed: 1
  worker alive AFTER fixture cleanup completed: startTinyDataFrameServer.func2 [runnable] at testing.(*common).Fail(0xc0000bea80)
panic: Fail in goroutine after TestVerify_C3_WorkerAfterCleanup/fixture has completed

goroutine 11 [running]:
testing.(*common).Fail(0xc0000bea80)
	/usr/local/go/src/testing/testing.go:960 +0xca
testing.(*common).Errorf(0xc0000bea80, {0xc02fc1?, 0xc000086f13?}, {0xc000086f70?, 0x100?, 0x0?})
	/usr/local/go/src/testing/testing.go:1205 +0x59
google.golang.org/grpc/internal/transport.startTinyDataFrameServer.func2()
	/home/ubuntu/wt/0e684571/internal/transport/recv_buffer_test.go:383 +0x9aa
created by google.golang.org/grpc/internal/transport.startTinyDataFrameServer in goroutine 9
	/home/ubuntu/wt/0e684571/internal/transport/recv_buffer_test.go:314 +0x188
```

Rates over repeated single-process runs:

```sh
for gmp in 1 8; do for sc in setup reception; do n=40; [ $sc = setup ] && n=200; alive=0; pan=0; for i in $(seq $n); do out=$(VERIFY_C3_SCENARIO=$sc GOMAXPROCS=$gmp /tmp/c3.test -test.run '^TestVerify_C3_WorkerAfterCleanup$' 2>&1); echo "$out" | grep -q "AFTER fixture cleanup" && alive=$((alive+1)); echo "$out" | grep -q "^panic: .*has completed" && pan=$((pan+1)); done; echo "scenario=$sc GOMAXPROCS=$gmp runs=$n worker_alive_after_cleanup=$alive panics_after_completion=$pan"; done; done
```

```console
scenario=setup GOMAXPROCS=1 runs=200 worker_alive_after_cleanup=200 panics_after_completion=200
scenario=reception GOMAXPROCS=1 runs=40 worker_alive_after_cleanup=0 panics_after_completion=0
scenario=setup GOMAXPROCS=8 runs=200 worker_alive_after_cleanup=8 panics_after_completion=9
scenario=reception GOMAXPROCS=8 runs=40 worker_alive_after_cleanup=5 panics_after_completion=7
```

- *Worker completion synchronization* — CONFIRMED: no cleanup waits for either goroutine (source lines above), and the runs show a worker alive after `t.Run` returned.
- *Failure-path teardown* — CONFIRMED: on both the setup-failure and reception-failure paths the writer worker was observed still running after cleanup completed, and its late `t.Errorf` crashed the test binary with `panic: Fail in goroutine after ... has completed`. It is a race: deterministic for `setup` on one CPU (200/200), 4–18% of runs otherwise on this machine. In the `success` control no worker was observed after cleanup.

Impact reasoning: test-only. When the transport test is already failing in setup or reception, the lingering worker can turn the ordinary failure into a process-wide panic, which aborts every other test in the `internal/transport` binary and hides the original failure message. It does not affect passing runs.

## C4

Target: [evalon/grpc-go-tr-64726da8](https://github.com/kaitranntt-evals/grpc-go-transport-restrict-memory-overhead/tree/evalon/grpc-go-tr-64726da8). Verdict: **CONFIRMED**.

[verify/repro/c4_homogeneous_copy_test.go](repro/c4_homogeneous_copy_test.go): one byte occupies the channel, then 8 homogeneous payloads backed by storage from a recording pool are `put`. The test compares each backlog entry's backing array against the input allocations, counts pool `Put`s issued during `put()`, and measures heap allocation across the puts (`runtime.MemStats`). Controls: 16384-byte and 8193-byte payloads (adjacent pair exceeds `http2MaxFrameLen`), and the same 4/8 KiB runs with compaction disabled.

```sh
cp verify/repro/c4_homogeneous_copy_test.go ~/wt/64726da8/internal/transport/verify_c4_homogeneous_copy_test.go
cd ~/wt/64726da8 && go test -v -run '^TestVerify_C4_' ./internal/transport -count=1
```

```console
=== RUN   TestVerify_C4_Homogeneous4KiB
    verify_c4_homogeneous_copy_test.go:95: compaction enabled=true payload=4096 bytes x 8 (adjacent pair total 8192, http2MaxFrameLen 16384)
    verify_c4_homogeneous_copy_test.go:96:   backlog entries=2 lens=[16384 16384]
    verify_c4_homogeneous_copy_test.go:97:   backing storage: entries on an original input allocation=0, entries on storage that is none of the inputs=2
    verify_c4_homogeneous_copy_test.go:98:   input pooled buffers released (pool.Put) during put()=8 of 8, still referenced=0, pool.Get during put()=0
    verify_c4_homogeneous_copy_test.go:99:   heap during put(): mallocs=18 bytes=81960 (payload bytes queued=32768)
    verify_c4_homogeneous_copy_test.go:100:   payload bytes intact and in order=true
    verify_c4_homogeneous_copy_test.go:101: RESULT C4 payload=4096: copied_into_new_storage=true (relocated entries=2, inputs freed at put=8, bytes allocated=81960)
--- PASS: TestVerify_C4_Homogeneous4KiB (0.00s)
=== RUN   TestVerify_C4_Homogeneous8KiB
    verify_c4_homogeneous_copy_test.go:95: compaction enabled=true payload=8192 bytes x 8 (adjacent pair total 16384, http2MaxFrameLen 16384)
    verify_c4_homogeneous_copy_test.go:96:   backlog entries=4 lens=[16384 16384 16384 16384]
    verify_c4_homogeneous_copy_test.go:97:   backing storage: entries on an original input allocation=0, entries on storage that is none of the inputs=4
    verify_c4_homogeneous_copy_test.go:98:   input pooled buffers released (pool.Put) during put()=8 of 8, still referenced=0, pool.Get during put()=0
    verify_c4_homogeneous_copy_test.go:99:   heap during put(): mallocs=15 bytes=67192 (payload bytes queued=65536)
    verify_c4_homogeneous_copy_test.go:100:   payload bytes intact and in order=true
    verify_c4_homogeneous_copy_test.go:101: RESULT C4 payload=8192: copied_into_new_storage=true (relocated entries=4, inputs freed at put=8, bytes allocated=67192)
--- PASS: TestVerify_C4_Homogeneous8KiB (0.00s)
=== RUN   TestVerify_C4_Control16KiB
    verify_c4_homogeneous_copy_test.go:95: compaction enabled=true payload=16384 bytes x 8 (adjacent pair total 32768, http2MaxFrameLen 16384)
    verify_c4_homogeneous_copy_test.go:96:   backlog entries=8 lens=[16384 16384 16384 16384 16384 16384 16384 16384]
    verify_c4_homogeneous_copy_test.go:97:   backing storage: entries on an original input allocation=8, entries on storage that is none of the inputs=0
    verify_c4_homogeneous_copy_test.go:98:   input pooled buffers released (pool.Put) during put()=0 of 8, still referenced=8, pool.Get during put()=0
    verify_c4_homogeneous_copy_test.go:99:   heap during put(): mallocs=4 bytes=480 (payload bytes queued=131072)
    verify_c4_homogeneous_copy_test.go:100:   payload bytes intact and in order=true
    verify_c4_homogeneous_copy_test.go:101: RESULT C4 payload=16384: copied_into_new_storage=false (relocated entries=0, inputs freed at put=0, bytes allocated=480)
--- PASS: TestVerify_C4_Control16KiB (0.00s)
=== RUN   TestVerify_C4_Control8KiBPlus1
    verify_c4_homogeneous_copy_test.go:95: compaction enabled=true payload=8193 bytes x 8 (adjacent pair total 16386, http2MaxFrameLen 16384)
    verify_c4_homogeneous_copy_test.go:96:   backlog entries=8 lens=[8193 8193 8193 8193 8193 8193 8193 8193]
    verify_c4_homogeneous_copy_test.go:97:   backing storage: entries on an original input allocation=8, entries on storage that is none of the inputs=0
    verify_c4_homogeneous_copy_test.go:98:   input pooled buffers released (pool.Put) during put()=0 of 8, still referenced=8, pool.Get during put()=0
    verify_c4_homogeneous_copy_test.go:99:   heap during put(): mallocs=4 bytes=480 (payload bytes queued=65544)
    verify_c4_homogeneous_copy_test.go:100:   payload bytes intact and in order=true
    verify_c4_homogeneous_copy_test.go:101: RESULT C4 payload=8193: copied_into_new_storage=false (relocated entries=0, inputs freed at put=0, bytes allocated=480)
--- PASS: TestVerify_C4_Control8KiBPlus1 (0.00s)
PASS
ok  	google.golang.org/grpc/internal/transport	0.007s
```

```sh
GRPC_GO_EXPERIMENTAL_ENABLE_RECEIVE_BUFFER_COMPACTION=false go test -v -run '^TestVerify_C4_Homogeneous' ./internal/transport -count=1
```

```console
    verify_c4_homogeneous_copy_test.go:101: RESULT C4 payload=4096: copied_into_new_storage=false (relocated entries=0, inputs freed at put=0, bytes allocated=480)
    verify_c4_homogeneous_copy_test.go:101: RESULT C4 payload=8192: copied_into_new_storage=false (relocated entries=0, inputs freed at put=0, bytes allocated=480)
```

Reading: with compaction on, eight 4 KiB payloads become two 16384-byte entries and eight 8 KiB payloads become four; none of the entries sits on an input allocation, all eight pooled inputs were released during `put()`, and the puts allocated 81960 bytes (4 KiB case, 2.5× the 32768 payload bytes because the destination grows by `append`) and 67192 bytes (8 KiB case) of fresh, non-pooled heap. Payload bytes stay intact and ordered. The copy stops exactly where the claim says: 8193-byte and 16384-byte payloads are preserved in place.

Impact reasoning: the task's problem is per-frame overhead of *tiny* frames; 4–8 KiB payloads already hold their bytes densely in pooled buffers. On this branch a slow reader receiving ordinary mid-sized DATA frames pays a full extra copy of every backlogged byte and trades pooled storage for garbage-collected heap, up to 2.5× the payload in transient allocation. No data is lost. `GRPC_GO_EXPERIMENTAL_ENABLE_RECEIVE_BUFFER_COMPACTION=false` avoids it (at the cost of the whole fix).

## C5

Target: [evalon/grpc-go-tr-a4df28bb](https://github.com/kaitranntt-evals/grpc-go-transport-restrict-memory-overhead/tree/evalon/grpc-go-tr-a4df28bb). Verdict: **CONFIRMED** (both parts).

The eval fixture passes on this branch, including the small-destination subtest:

```sh
cp ~/evalfx/tests/eval_recv_buffer_compaction_test.go ~/wt/a4df28bb/internal/transport/
cd ~/wt/a4df28bb && go test -v -run '^TestEval_RecvBufferConfiguredPoolRecyclingSafety$|^TestEval_RecvBufferConfiguredPoolAcquisition$' google.golang.org/grpc/internal/transport -race -count=1
```

```console
--- PASS: TestEval_RecvBufferConfiguredPoolAcquisition (0.00s)
    --- PASS: TestEval_RecvBufferConfiguredPoolAcquisition/UnpooledConsolidation (0.00s)
--- PASS: TestEval_RecvBufferConfiguredPoolRecyclingSafety (0.00s)
    --- PASS: TestEval_RecvBufferConfiguredPoolRecyclingSafety/SliceGrowthPoolOwnership (0.00s)
    --- PASS: TestEval_RecvBufferConfiguredPoolRecyclingSafety/ExactCapacitySmallDestination (0.00s)
PASS
```

That subtest sends ten 100-byte frames. Changing only the frame count to the claim's nine makes it fail:

```sh
sed 's/for i := range 10 {/for i := range 9 {/' ~/evalfx/tests/eval_recv_buffer_compaction_test.go > internal/transport/eval_recv_buffer_compaction_test.go
go test -v -run '^TestEval_RecvBufferConfiguredPoolRecyclingSafety$' google.golang.org/grpc/internal/transport -race -count=1
```

```console
    eval_recv_buffer_compaction_test.go:877: Exact capacity pool: 1 acquired destination buffers were abandoned and never returned to the pool
--- FAIL: TestEval_RecvBufferConfiguredPoolRecyclingSafety (0.00s)
    --- PASS: TestEval_RecvBufferConfiguredPoolRecyclingSafety/SliceGrowthPoolOwnership (0.00s)
    --- FAIL: TestEval_RecvBufferConfiguredPoolRecyclingSafety/ExactCapacitySmallDestination (0.00s)
```

Pool and capacity trace ([verify/repro/c5_small_destination_test.go](repro/c5_small_destination_test.go)): real `http2Client` stream, N 100-byte frames with the first confirmed to be in the channel, then flush/consume/`Free`, `closeStream(io.EOF)`, EOF consumed, transport closed.

```sh
cp verify/repro/c5_small_destination_test.go ~/wt/a4df28bb/internal/transport/verify_c5_small_destination_test.go
cd ~/wt/a4df28bb && go test -v -run '^TestVerify_C5_' ./internal/transport -race -count=1
```

```console
=== RUN   TestVerify_C5_NineFrames_ExactCapacityPool
    verify_c5_small_destination_test.go:89: first frame entered the channel (len(c)=1, backlog=0)
    verify_c5_small_destination_test.go:114: delivered buffers (each Free()d): [mem.SliceBuffer len=100 cap=100 mem.SliceBuffer len=800 cap=800]
    verify_c5_small_destination_test.go:123: stream terminated: state==streamDone:true recvBuffer.err=EOF pending==nil:true backlog=0
    verify_c5_small_destination_test.go:130:   pool trace: Get(100)->cap 100 by compactBuffer<-put
    verify_c5_small_destination_test.go:130:   pool trace: Get(200)->cap 200 by compactBuffer<-put
    verify_c5_small_destination_test.go:130:   pool trace: Put(cap 100) by compactBuffer<-put
    verify_c5_small_destination_test.go:130:   pool trace: Get(400)->cap 400 by compactBuffer<-put
    verify_c5_small_destination_test.go:130:   pool trace: Put(cap 200) by compactBuffer<-put
    verify_c5_small_destination_test.go:130:   pool trace: Get(800)->cap 800 by compactBuffer<-put
    verify_c5_small_destination_test.go:130:   pool trace: Put(cap 400) by compactBuffer<-put
    verify_c5_small_destination_test.go:133:   OUTSTANDING after free + stream termination + transport close: 0xc0000d2000 Get(800)->cap 800 by compactBuffer<-put
    verify_c5_small_destination_test.go:148: RESULT C5 nine 100-byte frames, exact-capacity pool: outstanding=1
--- PASS: TestVerify_C5_NineFrames_ExactCapacityPool (0.10s)
=== RUN   TestVerify_C5_TenFrames_ExactCapacityPool
    verify_c5_small_destination_test.go:89: first frame entered the channel (len(c)=1, backlog=0)
    verify_c5_small_destination_test.go:114: delivered buffers (each Free()d): [mem.SliceBuffer len=100 cap=100 *mem.buffer len=900 cap=1600]
    verify_c5_small_destination_test.go:123: stream terminated: state==streamDone:true recvBuffer.err=EOF pending==nil:true backlog=0
    verify_c5_small_destination_test.go:130:   pool trace: Get(100)->cap 100 by compactBuffer<-put
    verify_c5_small_destination_test.go:130:   pool trace: Get(200)->cap 200 by compactBuffer<-put
    verify_c5_small_destination_test.go:130:   pool trace: Put(cap 100) by compactBuffer<-put
    verify_c5_small_destination_test.go:130:   pool trace: Get(400)->cap 400 by compactBuffer<-put
    verify_c5_small_destination_test.go:130:   pool trace: Put(cap 200) by compactBuffer<-put
    verify_c5_small_destination_test.go:130:   pool trace: Get(800)->cap 800 by compactBuffer<-put
    verify_c5_small_destination_test.go:130:   pool trace: Put(cap 400) by compactBuffer<-put
    verify_c5_small_destination_test.go:130:   pool trace: Get(1600)->cap 1600 by compactBuffer<-put
    verify_c5_small_destination_test.go:130:   pool trace: Put(cap 800) by compactBuffer<-put
    verify_c5_small_destination_test.go:130:   pool trace: Put(cap 1600) by Free<-verifyC5Run
    verify_c5_small_destination_test.go:153: RESULT C5 ten 100-byte frames (the fixture's count), exact-capacity pool: outstanding=0
--- PASS: TestVerify_C5_TenFrames_ExactCapacityPool (0.10s)
=== RUN   TestVerify_C5_NineFrames_DefaultTiers
    verify_c5_small_destination_test.go:89: first frame entered the channel (len(c)=1, backlog=0)
    verify_c5_small_destination_test.go:114: delivered buffers (each Free()d): [mem.SliceBuffer len=100 cap=100 *mem.buffer len=800 cap=4096]
    verify_c5_small_destination_test.go:123: stream terminated: state==streamDone:true recvBuffer.err=EOF pending==nil:true backlog=0
    verify_c5_small_destination_test.go:130:   pool trace: Get(100)->cap 256 by compactBuffer<-put
    verify_c5_small_destination_test.go:130:   pool trace: Get(512)->cap 4096 by compactBuffer<-put
    verify_c5_small_destination_test.go:130:   pool trace: Put(cap 256) by compactBuffer<-put
    verify_c5_small_destination_test.go:130:   pool trace: Put(cap 4096) by Free<-verifyC5Run
    verify_c5_small_destination_test.go:158: RESULT C5 nine 100-byte frames, default-tier pool: outstanding=0
--- PASS: TestVerify_C5_NineFrames_DefaultTiers (0.10s)
=== RUN   TestVerify_C5_ThreeFrames_DefaultTiers
    verify_c5_small_destination_test.go:89: first frame entered the channel (len(c)=1, backlog=0)
    verify_c5_small_destination_test.go:114: delivered buffers (each Free()d): [mem.SliceBuffer len=100 cap=100 mem.SliceBuffer len=200 cap=256]
    verify_c5_small_destination_test.go:123: stream terminated: state==streamDone:true recvBuffer.err=EOF pending==nil:true backlog=0
    verify_c5_small_destination_test.go:130:   pool trace: Get(100)->cap 256 by compactBuffer<-put
    verify_c5_small_destination_test.go:133:   OUTSTANDING after free + stream termination + transport close: 0xc00000e700 Get(100)->cap 256 by compactBuffer<-put
    verify_c5_small_destination_test.go:163: RESULT C5 three 100-byte frames, default-tier pool: outstanding=1
--- PASS: TestVerify_C5_ThreeFrames_DefaultTiers (0.10s)
PASS
ok  	google.golang.org/grpc/internal/transport	1.422s
```

- *Short-burst allocation* — CONFIRMED: with an exact-capacity pool the nine-frame burst acquires destinations of 100, 200, 400 and 800 bytes via `compactBuffer <- put`; the final 800-byte one (≤ 1024) is what gets queued.
- *Small-destination release* — CONFIRMED: that destination reaches the reader as a `mem.SliceBuffer` (not a pooled `*mem.buffer`), its `Free()` issues no `Put`, and it is still outstanding after stream termination and transport close. Ten frames grow the destination to 1600 bytes, which is delivered as `*mem.buffer` and returned — which is why the fixture's ten-frame subtest passes.
- Pool dependence: with the default tier layout (256/4K/16K/32K/1M) nine frames end in a 4096-byte destination that *is* returned, so that exact burst is clean on `mem.DefaultBufferPool`-style pools; the same defect shows there with a shorter burst (three frames → 256-byte destination, never returned).

Impact reasoning: any stream whose backlog of compacted bytes is flushed while the destination capacity is still ≤ 1024 bytes (a handful of small frames arriving slightly faster than the reader — a very ordinary situation) takes a buffer from the configured pool that is never given back; the pool sees `Get` without `Put`. Observed is the missing `Put`, not process memory growth. With compaction disabled no destination is acquired:

```sh
cd ~/wt/a4df28bb && GRPC_GO_EXPERIMENTAL_ENABLE_RECEIVE_BUFFER_COMPACTION=false go test -v -run '^TestVerify_C5_' ./internal/transport -race -count=1
```

```console
    verify_c5_small_destination_test.go:148: RESULT C5 nine 100-byte frames, exact-capacity pool: outstanding=0
--- PASS: TestVerify_C5_NineFrames_ExactCapacityPool (0.10s)
    verify_c5_small_destination_test.go:153: RESULT C5 ten 100-byte frames (the fixture's count), exact-capacity pool: outstanding=0
--- PASS: TestVerify_C5_TenFrames_ExactCapacityPool (0.10s)
    verify_c5_small_destination_test.go:158: RESULT C5 nine 100-byte frames, default-tier pool: outstanding=0
--- PASS: TestVerify_C5_NineFrames_DefaultTiers (0.10s)
    verify_c5_small_destination_test.go:163: RESULT C5 three 100-byte frames, default-tier pool: outstanding=0
--- PASS: TestVerify_C5_ThreeFrames_DefaultTiers (0.10s)
ok  	google.golang.org/grpc/internal/transport	1.421s
```
