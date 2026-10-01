# Evidence for audit run v-5072bdd4

Observations only; the verdict report is delivered separately and is not part of this branch.

Common setup for every section:

- Workspace `~/repos/grpc-go`, branch `verify/grpc-go-transport-restrict-memory-overhead-v-5072bdd4`, created from `origin/grpc-go-transport-restrict-memory-overhead-perfect` (`327a6ff993d9866ea656ed166b89744aadcbc940`).
- `go version go1.25.7 linux/amd64`, 8 CPUs.
- Every claim targets a branch of a different repository, [kaitranntt-evals/grpc-go-transport-restrict-memory-overhead](https://github.com/kaitranntt-evals/grpc-go-transport-restrict-memory-overhead). In this workspace that repository is the git remote `claims` (`git remote add claims <workspace git proxy>/github.com/kaitranntt-evals/grpc-go-transport-restrict-memory-overhead`), which is why the commands below pass `CLAIM_REPO=claims`. Without that variable the scripts fetch from the GitHub URL directly.
- Each script under `verify/repro/` fetches the claim branch, checks it out at the audited commit in a fresh detached worktree under `mktemp -d`, adds test-only instrumentation from `verify/repro/testdata/`, and runs `go test`. No production file of any branch is modified. The worktrees can be removed afterwards with `git worktree remove --force <path>`.
- All claim branches have the same parent, `c92e985770b7194d4a4f433c84d42c6c195e8ce5` ("base" below).
- The eval fixture was extracted from the attached `eval_tests.zip` to `/home/ubuntu/eval_tests/tests/eval_recv_buffer_compaction_test.go` (sha256 `eb9f51f263ce5736fb7b056718e49902a4281b55ebe24834b88c90327d792b24`) and used unmodified.
- Console blocks quote the output lines of each run verbatim. Routine lines (`=== RUN`, the trailing empty legacy `Test`, some per-subtest `--- PASS` lines) are left out in places, and very long `queued entries` lines are shortened with `…`.

| Claim | Target branch | Audited commit |
| --- | --- | --- |
| C1 | `evalon/grpc-go-tr-09960403` | `ed7b3532e1dd13dd0bbcb644ba2fd9a42be45799` |
| C2 | `evalon/grpc-go-tr-92749eaf` | `75122757d6130c546a3d3ed7448391ee9d842c1e` |
| C3 | `evalon/grpc-go-tr-eebab2d1` | `ea19718738ad4259aaa9c9693291fc1babdce279` |
| C4 | `evalon/grpc-go-tr-93d15123` | `fd919bf37d244d9e9fb4addd0c561a1259eb77c1` |
| C5 | `evalon/grpc-go-tr-92749eaf` | `75122757d6130c546a3d3ed7448391ee9d842c1e` |

## C1

Claim: `TestRecvBuffer_SmallBuffersHeapUsage` finishes without releasing all pooled buffers it retains for heap measurement.

Branch `evalon/grpc-go-tr-09960403` at `ed7b3532`. Observed: holds.

What the test does (`internal/transport/recv_buffer_test.go`, lines 170-213 on the branch): for compaction on and off it builds a stream with a private `&countingPool{}`, writes 65,536 one-byte buffers, measures `HeapAlloc`, calls `runtime.KeepAlive(st)` and returns. It never reads from the stream, never calls `Free` on anything it queued and registers no cleanup. `countingPool.Get` increments `outstanding`, `countingPool.Put` decrements it; in the delivered test the pool is created inline, so nothing can inspect it afterwards.

Instrumentation (`verify/repro/testdata/c1_recv_buffer_test.patch`, test file only): keep that same pool in a variable and log `pool.stats()` from a `t.Cleanup`, which runs after the subtest body and its defers have finished.

```sh
cd ~/repos/grpc-go
CLAIM_REPO=claims sh verify/repro/c1_heap_test_pool_accounting.sh
```

```console
== worktree /tmp/tmp.WB9u5zT9Ly/09960403 at ed7b3532e1dd13dd0bbcb644ba2fd9a42be45799 (evalon/grpc-go-tr-09960403); go version go1.25.7 linux/amd64
 internal/transport/recv_buffer_test.go | 8 +++++++-
 1 file changed, 7 insertions(+), 1 deletion(-)
+ go test ./internal/transport -count=1 -v -run ^Test$/^RecvBuffer_SmallBuffersHeapUsage$
=== RUN   Test
=== RUN   Test/RecvBuffer_SmallBuffersHeapUsage
=== RUN   Test/RecvBuffer_SmallBuffersHeapUsage/compaction=true
    recv_buffer_test.go:201: Heap grew by 73392 bytes after queuing 65536 1-byte buffers
    recv_buffer_test.go:185: VERIFY-C1 at subtest teardown: pool.Get calls not matched by pool.Put = 6 (bytes handed out by pool = 63488)
=== RUN   Test/RecvBuffer_SmallBuffersHeapUsage/compaction=false
    recv_buffer_test.go:201: Heap grew by 3995136 bytes after queuing 65536 1-byte buffers
    recv_buffer_test.go:185: VERIFY-C1 at subtest teardown: pool.Get calls not matched by pool.Put = 0 (bytes handed out by pool = 0)
--- PASS: Test (0.02s)
    --- PASS: Test/RecvBuffer_SmallBuffersHeapUsage (0.02s)
        --- PASS: Test/RecvBuffer_SmallBuffersHeapUsage/compaction=true (0.00s)
        --- PASS: Test/RecvBuffer_SmallBuffersHeapUsage/compaction=false (0.01s)
PASS
ok  	google.golang.org/grpc/internal/transport	0.024s
```

With compaction enabled, six chunks (63,488 bytes) obtained from the pool are still outstanding when the subtest has been torn down. With compaction disabled the pool is never used (the 65,536 queued `mem.SliceBuffer`s are not pooled), so the count is 0.

Contrast, same script: the sibling test that reads and frees what it queued asserts that nothing is outstanding at its end, and passes.

```console
+ go test ./internal/transport -count=1 -v -run ^Test$/^RecvBuffer_CompactionReleasesBuffers$
--- PASS: Test (0.00s)
    --- PASS: Test/RecvBuffer_CompactionReleasesBuffers (0.00s)
PASS
ok  	google.golang.org/grpc/internal/transport	0.009s
```

Counterfactual, same script (`verify/repro/testdata/c1_default_pool_counterfactual.patch`, test file only): hand the stream `mem.DefaultBufferPool()` instead of the private pool. That is the pool the package's own leak checker (`internal/leakcheck`, run by `grpctest` at teardown) tracks, and it reports the unreleased chunks, attributing them to this test:

```console
+ go test ./internal/transport -count=1 -v -run ^Test$/^RecvBuffer_SmallBuffersHeapUsage$
=== NAME  Test/RecvBuffer_SmallBuffersHeapUsage
    grpctest.go:40: WARNING 5 allocated buffers never freed:
        google.golang.org/grpc/internal/leakcheck.(*swappableBufferPool).Get
        	/tmp/tmp.WB9u5zT9Ly/09960403/internal/leakcheck/leakcheck.go:65
        google.golang.org/grpc/internal/transport.(*recvBuffer).newChunk
        	/tmp/tmp.WB9u5zT9Ly/09960403/internal/transport/transport.go:187
        google.golang.org/grpc/internal/transport.(*recvBuffer).compact
        	/tmp/tmp.WB9u5zT9Ly/09960403/internal/transport/transport.go:173
        google.golang.org/grpc/internal/transport.(*recvBuffer).put
        	/tmp/tmp.WB9u5zT9Ly/09960403/internal/transport/transport.go:144
        google.golang.org/grpc/internal/transport.(*Stream).write
        	/tmp/tmp.WB9u5zT9Ly/09960403/internal/transport/transport.go:464
        google.golang.org/grpc/internal/transport.s.TestRecvBuffer_SmallBuffersHeapUsage.func1
        	/tmp/tmp.WB9u5zT9Ly/09960403/internal/transport/recv_buffer_test.go:196
        testing.tRunner
        	/usr/local/go/src/testing/testing.go:1934
    grpctest.go:40: 1% of buffers never freed
--- PASS: Test (0.02s)
```

(5 rather than 6 because the two pools round sizes differently; the checker only warns, the test still passes.)

Impact reasoning: the statement in the claim is true, and its consequences are confined to the test. The unreleased chunks come from a pool private to the subtest whose `Get` is a plain `make` and whose `Put` only decrements a counter, so they are garbage once the subtest returns: no other test sees them, and the test's own result does not depend on them (it passes, and both heap figures are on the expected side of its limit). What is lost is coverage and tidiness: the test queues 65,536 buffers and walks away, so it cannot notice a release regression on this path, and it only stays out of the package's buffer-leak report because it uses a private pool rather than the tracked default one. The release path itself is covered by `TestRecvBuffer_CompactionReleasesBuffers`, which passes.

## C2

Claim: the added integration test exercises an unbounded goroutine-completion wait during server cleanup. Parts: `cleanup_mechanism`, `test_reachability`.

Branch `evalon/grpc-go-tr-92749eaf` at `75122757`. Observed: both parts hold. The helper is not part of the solution's diff.

Instrumentation (test files only): `verify/repro/testdata/c2_transport_test.patch` prints the caller of `server.stop()` immediately before and after the completion-channel receive; `verify/repro/testdata/verify_c2_stop_test.go` adds `TestVerifyC2_StopWaitHasNoLocalBound`.

```sh
cd ~/repos/grpc-go
CLAIM_REPO=claims sh verify/repro/c2_stop_wait.sh
```

The helper and its use by the added test, as delivered (first part of the script output):

```console
== worktree /tmp/tmp.qGJnttfMQw/92749eaf at 75122757d6130c546a3d3ed7448391ee9d842c1e (evalon/grpc-go-tr-92749eaf); go version go1.25.7 linux/amd64
== server.stop() as delivered
563:func (s *server) stop() {
564-	s.lis.Close()
565-	s.mu.Lock()
566-	for c := range s.conns {
567-		c.Close(errors.New("server Stop called"))
568-	}
569-	s.conns = nil
570-	s.mu.Unlock()
571-	<-s.servingTasksDone
572-}
== lines of transport_test.go changed by the solution relative to its base c92e985770b7194d4a4f433c84d42c6c195e8ce5: 0
== calls to server.stop() in the base test file: 31
== cleanup of the added integration test
291-// complete message followed by EOF.
292:func (s) TestServerStreamReceivesManyTinyDataFrames(t *testing.T) {
293-	const numFrames = 10000
294-	server, client, cancel := setUp(t, 0, suspended)
295-	defer cancel()
296-	defer server.stop()
297-	defer client.Close(fmt.Errorf("closed manually by test"))
```

`servingTasksDone` is closed in exactly one place, `server.start` (line 409), after the accept loop has ended and `wg.Wait()` has returned for every per-connection serving goroutine.

### test_reachability

The added test's deferred `server.stop()` (it returns at `recv_buffer_test.go:362`, the closing brace of the test) arrives at the receive:

```console
+ go test ./internal/transport -count=1 -v -run ^Test$/^ServerStreamReceivesManyTinyDataFrames$
=== RUN   Test
=== RUN   Test/ServerStreamReceivesManyTinyDataFrames
VERIFY-C2 server.stop() called from recv_buffer_test.go:362: entering bare receive <-s.servingTasksDone
VERIFY-C2 server.stop() called from recv_buffer_test.go:362: receive completed after 6.275µs (us=6)
--- PASS: Test (0.11s)
    --- PASS: Test/ServerStreamReceivesManyTinyDataFrames (0.10s)
PASS
ok  	google.golang.org/grpc/internal/transport	0.112s
```

### cleanup_mechanism

`TestVerifyC2_StopWaitHasNoLocalBound` starts the same kind of server as the added test, connects a raw HTTP/2 client that keeps its connection open, removes the resulting server transport from `server.conns` so that `stop()` does not close it (that is, one serving goroutine that does not finish by itself), and calls `stop()` in a goroutine. The only timeout the helper configures anywhere is `defaultTestTimeout` (10s) on the per-connection context; the probe waits three times that.

```console
+ go test ./internal/transport -count=1 -v -run ^Test$/^VerifyC2_StopWaitHasNoLocalBound$
=== RUN   Test
=== RUN   Test/VerifyC2_StopWaitHasNoLocalBound
VERIFY-C2 server.stop() called from verify_c2_stop_test.go:78: entering bare receive <-s.servingTasksDone
    verify_c2_stop_test.go:90: VERIFY-C2 server.stop() still blocked after 30.1s (defaultTestTimeout = 10s). Goroutine:
        goroutine 13 [chan receive]:
        google.golang.org/grpc/internal/transport.(*server).stop(0xc0000368a0)
        	/tmp/tmp.qGJnttfMQw/92749eaf/internal/transport/transport_test.go:576 +0x238
        google.golang.org/grpc/internal/transport.s.TestVerifyC2_StopWaitHasNoLocalBound.func2()
        	/tmp/tmp.qGJnttfMQw/92749eaf/internal/transport/verify_c2_stop_test.go:78 +0x25
        created by google.golang.org/grpc/internal/transport.s.TestVerifyC2_StopWaitHasNoLocalBound in goroutine 9
        	/tmp/tmp.qGJnttfMQw/92749eaf/internal/transport/verify_c2_stop_test.go:77 +0x617
VERIFY-C2 server.stop() called from verify_c2_stop_test.go:78: receive completed after 30.100835672s (us=30100835)
    verify_c2_stop_test.go:97: VERIFY-C2 server.stop() returned 310µs after the serving goroutine was allowed to exit (total blocked: 30.101s)
--- PASS: Test (30.16s)
    --- PASS: Test/VerifyC2_StopWaitHasNoLocalBound (30.15s)
PASS
ok  	google.golang.org/grpc/internal/transport	30.160s
```

(Line 576 of the instrumented file is line 571 of the delivered file, the receive.)

`stop()` was still parked in `chan receive` 30.1s in, three times the helper's only timeout, and came back 310µs after the probe closed the client connection. Nothing in `stop()` bounds or cancels the receive: it ends when the last serving goroutine ends, and not before.

An earlier form of the probe, using the helper's normal `setUp` client instead of a raw connection, had `stop()` return after about 10s: that client is closed when its own 10s context expires, which ends the serving goroutine. That is the serving goroutine finishing, not a bound on the receive, and it is why the probe above uses a client the helper does not control.

### How the added test behaves in practice

```console
+ go test -race ./internal/transport -count=100 -v -run ^Test$/^ServerStreamReceivesManyTinyDataFrames$
ok  	google.golang.org/grpc/internal/transport	14.846s
== 100 runs under -race: entered the receive 100 times, completed 100 times, longest wait 86 microseconds
```

Impact reasoning: both parts of the claim are true as stated, and neither is a defect the solution introduced. `server.stop()` is a pre-existing test helper: the solution changed 0 lines of `transport_test.go`, and the base commit already calls `server.stop()` 31 times, so the added test cleans up the way the tests around it do. In the added test `stop()` first closes every registered connection, which is what ends the serving goroutines; in 100 consecutive runs under `-race` the receive was entered 100 times and completed 100 times, the longest wait being 86µs. The exposure is the generic one of that helper: if a serving goroutine ever failed to exit (for example a handler stuck after a regression), this test would hang in cleanup until something outside `stop()` ended the process, instead of failing with a message. I did not observe that happening in the added test.

## C3

Claim: the `internal/transport` test suite does not complete successfully with race detection enabled in the solution's provided environment.

Branch `evalon/grpc-go-tr-eebab2d1` at `ea197187`. Observed: holds, deterministically, because of an assertion in the solution's own new test; the race detector reports nothing.

```sh
cd ~/repos/grpc-go
CLAIM_REPO=claims sh verify/repro/c3_race_suite.sh
```

Step 1, the command from the claim on the unmodified delivered tree:

```console
== worktree /tmp/tmp.oHbHhB2qOC/eebab2d1 at ea19718738ad4259aaa9c9693291fc1babdce279 (evalon/grpc-go-tr-eebab2d1); go version go1.25.7 linux/amd64
+ go test -race ./internal/transport -count=1
--- FAIL: Test (11.86s)
    --- FAIL: Test/RecvBufferCompaction (0.00s)
        --- FAIL: Test/RecvBufferCompaction/enabled (0.00s)
            transport_test.go:3565: buffers returned to the pool after reading = 1, want 2
FAIL
FAIL	google.golang.org/grpc/internal/transport	11.938s
FAIL
+ rc
+ echo exit status: 1
exit status: 1
```

There is no `WARNING: DATA RACE` anywhere in the output. Steps 2 and 3, the failing test alone, three times under `-race` and once without it (same assertion every time):

```console
+ go test -race ./internal/transport -count=3 -v -run ^Test$/^RecvBufferCompaction$
=== RUN   Test
=== RUN   Test/RecvBufferCompaction
=== RUN   Test/RecvBufferCompaction/enabled
    transport_test.go:3565: buffers returned to the pool after reading = 1, want 2
=== RUN   Test/RecvBufferCompaction/disabled
--- FAIL: Test (0.01s)
    --- FAIL: Test/RecvBufferCompaction (0.00s)
        --- FAIL: Test/RecvBufferCompaction/enabled (0.00s)
        --- PASS: Test/RecvBufferCompaction/disabled (0.00s)
[... the same block two more times ...]
FAIL
FAIL	google.golang.org/grpc/internal/transport	0.043s
FAIL
exit status: 1
+ go test ./internal/transport -count=1 -run ^Test$/^RecvBufferCompaction$
--- FAIL: Test (0.00s)
    --- FAIL: Test/RecvBufferCompaction (0.00s)
        --- FAIL: Test/RecvBufferCompaction/enabled (0.00s)
            transport_test.go:3565: buffers returned to the pool after reading = 1, want 2
FAIL
FAIL	google.golang.org/grpc/internal/transport	0.006s
FAIL
exit status: 1
```

Steps 4 and 5, attribution: everything else in the package passes under `-race` on the delivered tree, and the whole package passes under `-race` on the base commit, so the environment is able to run the suite and the failure belongs to the test the solution added.

```console
+ go test -race ./internal/transport -count=1 -skip ^Test$/^RecvBufferCompaction$
ok  	google.golang.org/grpc/internal/transport	12.686s
+ git worktree add --quiet --detach /tmp/tmp.oHbHhB2qOC/eebab2d1-base c92e985770b7194d4a4f433c84d42c6c195e8ce5
+ cd /tmp/tmp.oHbHhB2qOC/eebab2d1-base
+ go test -race ./internal/transport -count=1
ok  	google.golang.org/grpc/internal/transport	13.573s
```

Step 6, why the assertion fails. The delivered test (`internal/transport/transport_test.go`, `TestRecvBufferCompaction`) creates one pooled `large` buffer, calls `large.Ref()` before queueing it in each case, reads everything back, frees what it read and then expects `wantPoolPuts+1` buffers in the pool, which for the `enabled` case is 2: the small pooled `"ef"` buffer released by compaction, plus `large`. `verify/repro/testdata/verify_c3_rootcause_test.go` replays that sequence with and without the test's own extra reference:

```console
+ go test -race ./internal/transport -count=1 -v -run ^Test$/^VerifyC3_LargeBufferPoolReturn$
=== RUN   Test
=== RUN   Test/VerifyC3_LargeBufferPoolReturn
    verify_c3_rootcause_test.go:54: VERIFY-C3 test-owned large.Ref()=true: pool puts after queueing=1, after reading and freeing everything=1
    verify_c3_rootcause_test.go:57: VERIFY-C3 test-owned large.Ref()=true: pool puts after the test also drops its own reference=2
    verify_c3_rootcause_test.go:54: VERIFY-C3 test-owned large.Ref()=false: pool puts after queueing=1, after reading and freeing everything=2
--- PASS: Test (0.01s)
    --- PASS: Test/VerifyC3_LargeBufferPoolReturn (0.00s)
PASS
ok  	google.golang.org/grpc/internal/transport	1.026s
```

While the test holds its own reference, the reader's `Free` cannot return `large` to the pool, so the count stays at 1; once that reference is dropped, or if it is never taken, the count is 2. The receive path releases the large buffer correctly; the expected value in the test is wrong for the references the test itself holds. In the `disabled` case the same expression evaluates to 1 (`"ef"` freed by the reader), which is why only `enabled` fails.

Impact reasoning: anyone running the listed evaluation command `go test -race ./internal/transport -count=1` on this branch gets exit status 1 on every run, with or without `-race`, from a test the solution itself added; the same package is green on the base commit. That means the branch was delivered with a red package suite, and its own regression test for the large-buffer path cannot pass as written. The probe shows no leak in the product code, so the fix is in the test (drop the test-owned reference before the final assertion, or expect `wantPoolPuts` there).

## C4

Claim: receive-buffer compaction retains oversized backing storage under mixed-size traffic or after nearly complete consumption of compacted bursts. Parts: `mixed_traffic_capacity`, `partial_read_retention`.

Branch `evalon/grpc-go-tr-93d15123` at `fd919bf3`. Observed: `mixed_traffic_capacity` holds; `partial_read_retention` does not.

Instrumentation: one added test file, `verify/repro/testdata/verify_c4_capacity_test.go`. "Backing capacity" below is the sum of `cap(buffer.ReadOnlyData())` over everything the `recvBuffer` holds (the entry parked in its channel, the backlog, and the compaction tail); the file also wraps the default pool to count the buffers obtained from it and not yet returned.

```sh
cd ~/repos/grpc-go
CLAIM_REPO=claims sh verify/repro/c4_mixed_capacity.sh
```

### mixed_traffic_capacity

31 pairs of (1-byte payload, 2,048-byte payload) are queued and nothing is read; payload is 31 x 2,049 = 63,519 bytes (62.0 KiB). 590 KiB is 604,160 bytes.

Variant A is the claim's configuration on a bare `recvBuffer`: `recvBuffer.pool` is the default pool and incoming payloads are allocated the way the framer allocates them (`pool.Get(len)` above the pooling threshold). D is A with compaction disabled.

```console
== worktree /tmp/tmp.51vcwEEiqA/93d15123 at fd919bf37d244d9e9fb4addd0c561a1259eb77c1 (evalon/grpc-go-tr-93d15123); go version go1.25.7 linux/amd64
+ go test ./internal/transport -count=1 -v -run ^Test$/^VerifyC4_(MixedTrafficCapacity|MixedTrafficCapacity_ServerTransport|PartialReadRetention)$
=== RUN   Test/VerifyC4_MixedTrafficCapacity/A_default_pool_everywhere_(what_a_transport_does)
    verify_c4_capacity_test.go:166: VERIFY-C4 mixed [A default pool everywhere (what a transport does)] 31 pairs of (1 byte, 2048 bytes)
    verify_c4_capacity_test.go:167: VERIFY-C4   queued entries (len/cap): 1/1 2048/4096 1/4096 2048/4096 1/16384 2048/4096 1/16384 2048/4096 1/16384 2048/4096 … 1/16384 2048/4096
    verify_c4_capacity_test.go:168: VERIFY-C4   entries=62 payload=63519 B = 62.0 KiB backing capacity=606209 B = 592.0 KiB (9.54x payload); of which outstanding from the default pool: 61 buffers, 606208 B = 592.0 KiB
    verify_c4_capacity_test.go:170: VERIFY-C4   backing capacity > 590 KiB (604160 B)? true
=== RUN   Test/VerifyC4_MixedTrafficCapacity/D_default_pool_everywhere,_compaction_disabled_(baseline)
    verify_c4_capacity_test.go:167: VERIFY-C4   queued entries (len/cap): 1/1 2048/4096 1/1 2048/4096 1/1 2048/4096 … 1/1 2048/4096
    verify_c4_capacity_test.go:168: VERIFY-C4   entries=62 payload=63519 B = 62.0 KiB backing capacity=127007 B = 124.0 KiB (2.00x payload); of which outstanding from the default pool: 31 buffers, 126976 B = 124.0 KiB
    verify_c4_capacity_test.go:170: VERIFY-C4   backing capacity > 590 KiB (604160 B)? false
```

The same workload sent as real DATA frames over TCP to an `http2Server` using the default pool, measured on the server stream's `recvBuffer` once all frames have arrived:

```console
=== RUN   Test/VerifyC4_MixedTrafficCapacity_ServerTransport/compaction_enabled=true
    verify_c4_capacity_test.go:274: VERIFY-C4 mixed e2e [http2Server, default pool, compaction=true]
    verify_c4_capacity_test.go:275: VERIFY-C4   queued entries (len/cap): 1/1 2048/4096 1/4096 2048/4096 1/16384 2048/4096 1/16384 2048/4096 … 1/16384 2048/4096
    verify_c4_capacity_test.go:276: VERIFY-C4   entries=62 payload=63519 B = 62.0 KiB backing capacity=606209 B = 592.0 KiB (9.54x payload)
    verify_c4_capacity_test.go:277: VERIFY-C4   backing capacity > 590 KiB (604160 B)? true
=== RUN   Test/VerifyC4_MixedTrafficCapacity_ServerTransport/compaction_enabled=false
    verify_c4_capacity_test.go:276: VERIFY-C4   entries=62 payload=63519 B = 62.0 KiB backing capacity=127007 B = 124.0 KiB (2.00x payload)
    verify_c4_capacity_test.go:277: VERIFY-C4   backing capacity > 590 KiB (604160 B)? false
```

So 606,209 bytes (592.0 KiB) are retained for 63,519 bytes of payload, above the claim's 590 KiB, in the claim's configuration and end to end. The entry list shows where it goes: from the third pair on, every 1-byte payload sits alone in its own 16,384-byte chunk from the pool. `allocTail` sizes a new chunk from the stream's total queued bytes (`min(max(b.queuedBytes+n, minCompactionBufferSize), maxCompactionBufferSize)`), and every payload above `maxCompactedPayloadSize` (1,024) flushes the current chunk, so a chunk sized for the whole backlog is sealed holding one byte, once per pair.

How sensitive the number is to the setup (same test, other variants):

```console
=== RUN   Test/VerifyC4_MixedTrafficCapacity/B_default_pool_for_compaction,_exactly_sized_payload_slices
    verify_c4_capacity_test.go:168: VERIFY-C4   entries=62 payload=63519 B = 62.0 KiB backing capacity=542728 B = 530.0 KiB (8.54x payload); of which outstanding from the default pool: 30 buffers, 479232 B = 468.0 KiB
    verify_c4_capacity_test.go:170: VERIFY-C4   backing capacity > 590 KiB (604160 B)? false
=== RUN   Test/VerifyC4_MixedTrafficCapacity/C_recvBuffer.pool_unset_(how_the_eval_fixture_initialises_it)
    verify_c4_capacity_test.go:167: VERIFY-C4   queued entries (len/cap): 1/1 2048/4096 1/2049 2048/4096 1/4098 2048/4096 1/6147 2048/4096 1/8196 2048/4096 1/10245 2048/4096 1/12294 2048/4096 1/14343 2048/4096 1/16384 2048/4096 … 1/16384 2048/4096
    verify_c4_capacity_test.go:168: VERIFY-C4   entries=62 payload=63519 B = 62.0 KiB backing capacity=561181 B = 548.0 KiB (8.83x payload); of which outstanding from the default pool: 31 buffers, 126976 B = 124.0 KiB
    verify_c4_capacity_test.go:170: VERIFY-C4   backing capacity > 590 KiB (604160 B)? false
=== RUN   Test/VerifyC4_MixedTrafficCapacity/E_like_A,_63_pairs_of_1_byte_+_1025_bytes
    verify_c4_capacity_test.go:168: VERIFY-C4   entries=126 payload=64638 B = 63.1 KiB backing capacity=1236993 B = 1208.0 KiB (19.14x payload); of which outstanding from the default pool: 125 buffers, 1236992 B = 1208.0 KiB
=== RUN   Test/VerifyC4_MixedTrafficCapacity/F_like_E,_compaction_disabled_(baseline)
    verify_c4_capacity_test.go:168: VERIFY-C4   entries=126 payload=64638 B = 63.1 KiB backing capacity=258111 B = 252.1 KiB (3.99x payload); of which outstanding from the default pool: 63 buffers, 258048 B = 252.0 KiB
```

- B: if the 2-KiB payloads arrive in exactly sized slices instead of pooled ones, the total is 530.0 KiB, below 590 KiB; of the 592.0 KiB in A, 124.0 KiB is the default pool rounding each 2,048-byte payload up to 4,096, which is there without compaction too (D).
- C: with `recvBuffer.pool` unset, which is how the eval fixture builds its `recvBuffer` and which no transport does, the total is 548.0 KiB.
- What compaction adds over the compaction-disabled baseline D: 479,202 bytes (468 KiB) in A, 434,174 bytes (424 KiB) in C.
- E/F: with the smallest payload that bypasses compaction (1,025 bytes) and as many pairs as fit a 65,535-byte window, compaction holds 1,208.0 KiB for 63.1 KiB of payload, against 252.1 KiB with compaction disabled.

The escape hatch, through the real environment variable (last two commands of the script; this test does not override the setting in-process):

```console
+ go test ./internal/transport -count=1 -v -run ^Test$/^VerifyC4_MixedTrafficCapacity_ProcessEnv$
    verify_c4_capacity_test.go:397: VERIFY-C4 mixed [process environment: GRPC_GO_EXPERIMENTAL_ENABLE_RECEIVE_BUFFER_COMPACTION="", envconfig.EnableReceiveBufferCompaction=true]
    verify_c4_capacity_test.go:399: VERIFY-C4   entries=62 payload=63519 B = 62.0 KiB backing capacity=606209 B = 592.0 KiB (9.54x payload)
+ GRPC_GO_EXPERIMENTAL_ENABLE_RECEIVE_BUFFER_COMPACTION=false go test ./internal/transport -count=1 -v -run ^Test$/^VerifyC4_MixedTrafficCapacity_ProcessEnv$
    verify_c4_capacity_test.go:397: VERIFY-C4 mixed [process environment: GRPC_GO_EXPERIMENTAL_ENABLE_RECEIVE_BUFFER_COMPACTION="false", envconfig.EnableReceiveBufferCompaction=false]
    verify_c4_capacity_test.go:399: VERIFY-C4   entries=62 payload=63519 B = 62.0 KiB backing capacity=127007 B = 124.0 KiB (2.00x payload)
```

The eval fixture, byte-exact from the archive, passes on this branch, including its mixed-frames check, so the fixture does not detect this:

```sh
cd <worktree of evalon/grpc-go-tr-93d15123 at fd919bf3>
cp /home/ubuntu/eval_tests/tests/eval_recv_buffer_compaction_test.go internal/transport/eval_recv_buffer_compaction_test.go
go test -v -run '^TestEval_' google.golang.org/grpc/internal/transport -race -count=1
```

```console
--- PASS: TestEval_RecvBufferCompaction (0.00s)
--- PASS: TestEval_RecvBufferCompactionDisabled (0.00s)
--- PASS: TestEval_RecvBufferCompactionSkippedLargeBuffer (0.01s)
--- PASS: TestEval_RecvBufferErrorResetSafety (0.00s)
--- PASS: TestEval_RecvBufferCompaction_MixedFrames (0.00s)
--- PASS: TestEval_RecvBufferCompaction_MultiCycleMemoryBound (0.00s)
PASS
ok  	google.golang.org/grpc/internal/transport	1.033s
```

### partial_read_retention

200,000 one-byte messages are queued (the burst spans 13 pooled chunks), then 199,999 bytes are read through a `recvBufferReader` in 1,000-byte reads, each returned buffer being freed, as the gRPC layer does. Run once with the default pool on `recvBuffer.pool` and once with it unset.

```console
=== RUN   Test/VerifyC4_PartialReadRetention/recvBuffer.pool_set=true
    verify_c4_capacity_test.go:326: VERIFY-C4 partial: after queueing 200000 1-byte messages: entries=19 payload=200000 B = 195.3 KiB backing capacity=201744 B = 197.0 KiB; outstanding from pool: 13 buffers, 200704 B = 196.0 KiB; heap in use grew by 206328 B = 201.5 KiB
    verify_c4_capacity_test.go:328: VERIFY-C4 partial:   queued entries (len/cap): 1/1 64/64 65/65 130/130 260/260 520/520 4096/4096 16384/16384 16384/16384 16384/16384 16384/16384 16384/16384 16384/16384 16384/16384 16384/16384 16384/16384 16384/16384 16384/16384 14640/16384
    verify_c4_capacity_test.go:361: VERIFY-C4 partial: after reading 199999 of 200000 bytes (99.9995%): entries left in recvBuffer=0 (capacity 0 B = 0.0 KiB), remainder held by the reader: 1745 B = 1.7 KiB; outstanding from pool: 1 buffers, 16384 B = 16.0 KiB (pool gets=13 puts=12); heap in use vs. empty: 19512 B = 19.1 KiB
    verify_c4_capacity_test.go:363: VERIFY-C4 partial:   still held (len/cap): reader.last 1/1745
    verify_c4_capacity_test.go:365: VERIFY-C4 partial: pooled backing storage still retained is 8.2% of the full burst's (16384 B of 200704 B)
    verify_c4_capacity_test.go:368: VERIFY-C4 partial: heap still in use is 9.5% of what the full burst held (19512 B of 206328 B)
=== RUN   Test/VerifyC4_PartialReadRetention/recvBuffer.pool_set=false
    verify_c4_capacity_test.go:326: VERIFY-C4 partial: after queueing 200000 1-byte messages: entries=22 payload=200000 B = 195.3 KiB backing capacity=213248 B = 208.2 KiB; outstanding from pool: 0 buffers, 0 B = 0.0 KiB; heap in use grew by 217224 B = 212.1 KiB
    verify_c4_capacity_test.go:361: VERIFY-C4 partial: after reading 199999 of 200000 bytes (99.9995%): entries left in recvBuffer=0 (capacity 0 B = 0.0 KiB), remainder held by the reader: 13249 B = 12.9 KiB; outstanding from pool: 0 buffers, 0 B = 0.0 KiB (pool gets=0 puts=0); heap in use vs. empty: 18264 B = 17.8 KiB
    verify_c4_capacity_test.go:368: VERIFY-C4 partial: heap still in use is 8.4% of what the full burst held (18264 B of 217224 B)
--- PASS: Test (0.03s)
PASS
ok  	google.golang.org/grpc/internal/transport	0.039s
```

After all but one byte is consumed the `recvBuffer` holds no entries; 12 of the 13 pooled chunks have gone back to the pool (`gets=13 puts=12`) and the only storage left is the single chunk the reader is in the middle of. Storage for consumed chunks is released chunk by chunk; the full burst's storage is not retained.

Impact reasoning (for the part that holds): on this branch a stream whose peer interleaves payloads of at most 1 KiB with payloads above 1 KiB, while the application is not reading, pins one 16 KiB pooled chunk per small frame. Measured through a real server transport that is 592 KiB for a 62 KiB backlog, 9.5 times the payload and 4.8 times what the same backlog holds with compaction turned off (124 KiB); with 1,025-byte large frames it is 1,208 KiB against 252 KiB. The feature was asked to keep memory for queued small frames bounded relative to buffered payload bytes; for this traffic shape it makes memory worse than the behavior it replaces, while all-small traffic (the partial-read run: 197 KiB of capacity for 195 KiB of payload) and the attached eval fixture look fine, so nothing flags it. The trigger needs no special configuration, only that traffic shape within the default 64 KiB stream window. Setting `GRPC_GO_EXPERIMENTAL_ENABLE_RECEIVE_BUFFER_COMPACTION=false` in the process environment restores the 124 KiB figure (observed above).

## C5

Claim: the receive-buffer compaction comments claim logarithmic queue-entry growth after chunk capacity reaches its fixed maximum, although additional fixed-cap chunks make queue-entry growth linear in compacted bytes.

Branch `evalon/grpc-go-tr-92749eaf` at `75122757`. Observed: holds. The claim names `compactBacklogLocked()`, which does not exist on this branch; the nearest equivalent, and the function carrying the comment, is `compactLocked()`.

Instrumentation: one added test file, `verify/repro/testdata/verify_c5_entry_growth_test.go`. It queues N one-byte buffers behind a reader that is not reading, for N doubling from 2^10 to 2^23, and counts queue entries (channel + backlog + pending compaction buffer).

```sh
cd ~/repos/grpc-go
CLAIM_REPO=claims sh verify/repro/c5_entry_growth.sh
```

The comment and the allocation logic as delivered (first part of the script output). The comment states the logarithmic bound without qualification; the code caps the doubling at `maxCompactionBufferSize` and then starts a new buffer of that same size each time one fills.

```console
== worktree /tmp/tmp.muTJq1WCI4/92749eaf at 75122757d6130c546a3d3ed7448391ee9d842c1e (evalon/grpc-go-tr-92749eaf); go version go1.25.7 linux/amd64
== the comment and the allocation logic as delivered
150-// compactLocked copies the contents of buf into the pending compaction buffer
151-// and releases buf. A new compaction buffer is started if buf doesn't fit in
152-// the current one. Consecutive compaction buffers grow geometrically, so that
153-// the number of queued entries stays logarithmic in the number of compacted
154-// bytes, and previously compacted bytes are never copied again.
155-//
156-// b.mu must be held.
157:func (b *recvBuffer) compactLocked(buf mem.Buffer) {
158-	data := buf.ReadOnlyData()
159-	if len(b.pending)+len(data) > cap(b.pending) {
160-		size := minCompactionBufferSize
161-		if b.pending != nil {
162-			size = min(2*cap(b.pending), maxCompactionBufferSize)
163-			b.flushPendingLocked()
164-		}
165-		b.allocPendingLocked(max(size, len(data)))
166-	}
167-	b.pending = append(b.pending, data...)
168-	buf.Free()
169-}
78:	minCompactionBufferSize = 2 * maxCompactedFrameSize
79:	maxCompactionBufferSize = http2MaxFrameLen
```

The comment on the constants (lines 73-77) describes only the bounds and the half-full property and does not restrict the statement above.

```console
+ go test ./internal/transport -count=1 -v -run ^Test$/^VerifyC5_QueueEntryGrowth$
=== RUN   Test
=== RUN   Test/VerifyC5_QueueEntryGrowth
    verify_c5_entry_growth_test.go:35: VERIFY-C5 [default pool] maxCompactionBufferSize=16384 minCompactionBufferSize=512
    verify_c5_entry_growth_test.go:36: VERIFY-C5 [default pool]   bytes(N)  entries  log2(N)    N/16384 entries(N)-entries(N/2)  capacities of first entries
    verify_c5_entry_growth_test.go:59: VERIFY-C5 [default pool]       1024        3       10          0              3  [512 ...]
    verify_c5_entry_growth_test.go:59: VERIFY-C5 [default pool]       2048        4       11          0              1  [512 1024 ...]
    verify_c5_entry_growth_test.go:59: VERIFY-C5 [default pool]       4096        4       12          0              0  [512 1024 ...]
    verify_c5_entry_growth_test.go:59: VERIFY-C5 [default pool]       8192        5       13          0              1  [512 1024 4096 ...]
    verify_c5_entry_growth_test.go:59: VERIFY-C5 [default pool]      16384        5       14          1              0  [512 1024 4096 ...]
    verify_c5_entry_growth_test.go:59: VERIFY-C5 [default pool]      32768        6       15          2              1  [512 1024 4096 16384 ...]
    verify_c5_entry_growth_test.go:59: VERIFY-C5 [default pool]      65536        8       16          4              2  [512 1024 4096 16384 16384 16384 ...]
    verify_c5_entry_growth_test.go:59: VERIFY-C5 [default pool]     131072       12       17          8              4  [512 1024 4096 16384 16384 16384 16384 16384 ...]
    verify_c5_entry_growth_test.go:59: VERIFY-C5 [default pool]     262144       20       18         16              8  [512 1024 4096 16384 16384 16384 16384 16384 ...]
    verify_c5_entry_growth_test.go:59: VERIFY-C5 [default pool]     524288       36       19         32             16  [512 1024 4096 16384 16384 16384 16384 16384 ...]
    verify_c5_entry_growth_test.go:59: VERIFY-C5 [default pool]    1048576       68       20         64             32  [512 1024 4096 16384 16384 16384 16384 16384 ...]
    verify_c5_entry_growth_test.go:59: VERIFY-C5 [default pool]    2097152      132       21        128             64  [512 1024 4096 16384 16384 16384 16384 16384 ...]
    verify_c5_entry_growth_test.go:59: VERIFY-C5 [default pool]    4194304      260       22        256            128  [512 1024 4096 16384 16384 16384 16384 16384 ...]
    verify_c5_entry_growth_test.go:59: VERIFY-C5 [default pool]    8388608      516       23        512            256  [512 1024 4096 16384 16384 16384 16384 16384 ...]
```

With an exact-size pool (so the default pool's rounding is not a factor) the last rows are `4194304 → 262` and `8388608 → 518`, the same shape.

Up to 32 KiB the count grows by at most one per doubling, which is the logarithmic regime. From there on each doubling of N doubles the increment (2, 4, 8, … 256) and the count is N/16384 + 4: one more queue entry for every 16,384 compacted bytes, which is linear. At 8 MiB the count is 516 where log2(N) is 23.

Impact reasoning: this is an inaccuracy in a code comment, with no behavioral effect observed. The entry count the code actually produces, one entry per 16 KiB, is still a 16,384-fold reduction against one entry per 1-byte frame, and compacted data never exceeds what flow control lets a peer queue, so nothing here suggests a memory or CPU problem. The cost is to a reader who relies on the comment: the stated bound stops being true at about 32 KiB of compacted backlog, which is below the default 64 KiB stream window (8 entries at 65,536 bytes in the table), so the comment is already wrong for an ordinary full window.
