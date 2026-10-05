## Setup

Observations only; verdict reasoning is summarised per claim. Environment: `go version go1.25.7 linux/amd64`.

- Audited branch [grpc-go-transport-restrict-memory-overhead-perfect](https://github.com/kaitranntt-evals/grpc-go/tree/grpc-go-transport-restrict-memory-overhead-perfect) at `327a6ff993d9866ea656ed166b89744aadcbc940` (checked out as `verify/grpc-go-transport-restrict-memory-overhead-v-45bd1fe2`).
- Claim-target branches are not on `origin`; they were fetched from the repository named in the claim URLs and checked out as detached worktrees:
  - [evalon/grpc-go-tr-db66491b](https://github.com/kaitranntt-evals/grpc-go-transport-restrict-memory-overhead/tree/evalon/grpc-go-tr-db66491b) at `c24b5caee8e66bad5ebe695da215c059d9765a4a`
  - [evalon/grpc-go-tr-95bcb5fc](https://github.com/kaitranntt-evals/grpc-go-transport-restrict-memory-overhead/tree/evalon/grpc-go-tr-95bcb5fc) at `eeacfe3c0a278890cf63c217c8811332bc42ff26`
  - [evalon/grpc-go-tr-51f38834](https://github.com/kaitranntt-evals/grpc-go-transport-restrict-memory-overhead/tree/evalon/grpc-go-tr-51f38834) at `e6ea56b5a180e3c822604bf3d416954349d99246`
  - [evalon/grpc-go-tr-5401c189](https://github.com/kaitranntt-evals/grpc-go-transport-restrict-memory-overhead/tree/evalon/grpc-go-tr-5401c189) at `5576a622562b8b457c9d7121a5d687e0960b3661`
- Eval fixture used byte-exact from `eval_tests.zip` (`sha256 fb82bd7de9cf6cfd6b6928a245052e0a4e781a6a049b67e68691971f645354ca`), copied to `internal/transport/eval_recv_buffer_compaction_test.go` only for the duration of a run.
- Production code on every branch is untouched in what is pushed; mutations/fault injections were applied in throw-away worktrees and are kept as patches under `verify/repro/`.

```sh
cd ~/repos/grpc-go
git fetch origin grpc-go-transport-restrict-memory-overhead-perfect
git checkout -b verify/grpc-go-transport-restrict-memory-overhead-v-45bd1fe2 origin/grpc-go-transport-restrict-memory-overhead-perfect
git remote add claims https://github.com/kaitranntt-evals/grpc-go-transport-restrict-memory-overhead
for b in db66491b 95bcb5fc 51f38834 5401c189; do
  git fetch claims evalon/grpc-go-tr-$b:refs/remotes/claims/$b
  git worktree add ~/wt/$b claims/$b
done
mkdir -p ~/fx && (cd ~/fx && unzip -o eval_tests.zip)   # -> ~/fx/tests/eval_recv_buffer_compaction_test.go
```

Probe files (all under `verify/repro/`):

- `verify_probe_test.go` + `run_probe.sh` — branch-neutral probes (`TestVerifyProbe_*`) in package `transport`. They wrap the configured `mem.BufferPool` in a tracing pool that records, for every `Get`, the innermost `internal/transport` function on the call stack and whether a `recvBuffer` method is on the stack, and tracks which buffers were never `Put` back. The server-side probes use the real `NewServerTransport` on a TCP listener and a raw `golang.org/x/net/http2` framer as the peer.
- `c4_c6_handler_reads_test.go` — handler-transport (`serverHandlerTransport.HandleStreams`) probe for the audited branch.
- `c4_c6_e2e/main.go` — end-to-end program: real `grpc.Server` behind `net/http` HTTP/2 (`ServeHTTP`), raw HTTP/2 peer.
- `c1_db66491b_mutation.patch`, `c1_95bcb5fc_fault_injection.patch` — product mutations used to test whether test blocking operations are locally bounded.

## C1

Claim: added/modified tests execute a blocking operation without an effective local bound. Method: make the blocking operation actually block (by mutating product code in a throw-away worktree) and observe whether the test fails on its own within `defaultTestTimeout` (10s) or hangs until the global `go test -timeout`.

### [evalon/grpc-go-tr-db66491b](https://github.com/kaitranntt-evals/grpc-go-transport-restrict-memory-overhead/tree/evalon/grpc-go-tr-db66491b) — CONFIRMED

The added test `TestRecvBuffer_NoCompactionWhenReaderIsCaughtUp` (`internal/transport/recv_buffer_test.go:222-234`) performs a bare channel receive, with no `select`, timeout or context:

```go
	for i := 0; i < 10; i++ {
		buf := mem.SliceBuffer([]byte{byte(i)})
		b.put(recvMsg{buffer: buf})
		m := <-b.get()          // recv_buffer_test.go:229
		b.load()
```
Unmutated, the test passes:

```console
$ cd ~/wt/db66491b && go test ./internal/transport -run 'Test/RecvBuffer_NoCompactionWhenReaderIsCaughtUp$' -count=1 -timeout 40s -v
--- PASS: Test (0.00s)
    --- PASS: Test/RecvBuffer_NoCompactionWhenReaderIsCaughtUp (0.00s)
ok  	google.golang.org/grpc/internal/transport	0.006s
```
Mutation (`verify/repro/c1_db66491b_mutation.patch`): a one-line regression in `recvBuffer.put` so that it never hands a message directly to the reader channel — exactly the behaviour this test exists to detect:

```diff
-	if len(b.backlog) == 0 && b.compacted == nil {
+	if false && len(b.backlog) == 0 && b.compacted == nil { // VERIFY MUTATION: never hand off directly
```
Result: the test does not fail; it hangs for the entire global timeout (4x `defaultTestTimeout`) and is killed by the `go test` alarm, with the test goroutine parked on line 229:

```console
$ cd ~/wt/db66491b && git apply ~/repos/grpc-go/verify/repro/c1_db66491b_mutation.patch
$ time go test ./internal/transport -run 'Test/RecvBuffer_NoCompactionWhenReaderIsCaughtUp$' -count=1 -timeout 40s -v
=== RUN   Test
=== RUN   Test/RecvBuffer_NoCompactionWhenReaderIsCaughtUp
panic: test timed out after 40s
	running tests:
		Test (40s)
		Test/RecvBuffer_NoCompactionWhenReaderIsCaughtUp (40s)
...
goroutine 24 [chan receive]:
google.golang.org/grpc/internal/transport.s.TestRecvBuffer_NoCompactionWhenReaderIsCaughtUp({{}}, 0xc0001036c0)
	/home/ubuntu/wt/db66491b/internal/transport/recv_buffer_test.go:229 +0x19e
google.golang.org/grpc/internal/grpctest.RunSubTests.func1(0xc0001036c0)
	/home/ubuntu/wt/db66491b/internal/grpctest/grpctest.go:132 +0xb5
testing.tRunner(0xc0001036c0, 0xc000326460)
	/usr/local/go/src/testing/testing.go:1934 +0xea
created by testing.(*T).Run in goroutine 23
	/usr/local/go/src/testing/testing.go:1997 +0x465
FAIL	google.golang.org/grpc/internal/transport	40.086s
FAIL

real	0m42.074s
$ git checkout internal/transport/transport.go
```

Not excluded: the test and the receive are newly added by this branch (not an unchanged fixture, constructor update, benchmark or deferred server teardown). It is not non-blocking by construction: whether the receive completes depends on the product behaviour under test.

Impact reasoning: a regression in the exact behaviour the test guards turns into a hung package run (default 10 minutes, killing every other test in `internal/transport` with a goroutine dump) rather than one targeted failure message. All other blocking operations inspected on this branch (stream reads through contexts with `defaultTestTimeout`, `select`s on `ctx.Done()`) are bounded.

### [evalon/grpc-go-tr-95bcb5fc](https://github.com/kaitranntt-evals/grpc-go-transport-restrict-memory-overhead/tree/evalon/grpc-go-tr-95bcb5fc) — REFUTED

The added tests (`internal/transport/recv_buffer_test.go`) contain no bare receive: `drainRecvBuffer` selects on a 10s context; `tinyFramesRecvMemory` bounds dial, `NewStream`, `<-str.Done()` and the server join with a 10s context. The raw test server `tinyFramesServer` uses `lis.Accept`, `io.ReadFull`, `fr.ReadFrame` and buffered writes with no socket deadlines of their own, so each was forced to block via fault injection in product code (`verify/repro/c1_95bcb5fc_fault_injection.patch`, env-gated by `VERIFY_FAULT`), and the test run was timed with a 120s global timeout:

| `VERIFY_FAULT` | what it forces | observed |
|---|---|---|
| `none` | baseline | pass, 0.146s |
| `nodial` | client never connects; server goroutine parked in `lis.Accept()` | fails in 0.005s; Accept interrupted by the deferred `lis.Close()` |
| `nostream` | client connects but never sends HEADERS; server parked in `fr.ReadFrame()` | fails in 0.020s; ReadFrame gets EOF from the deferred `ct.Close` |
| `stall` | client stops reading at the first DATA frame; stream never completes | each subtest fails at 10.0s on its own context (`Timed out waiting for the stream to receive trailers`), teardown join bounded |
| `dropdata` | `recvBuffer.put` drops all data | fails in 0.08s |
| `drop` (unit tests only) | `recvBuffer.put` drops everything incl. errors | each `drainRecvBuffer` fails at 10.0s on its own context |

```console
$ cd ~/wt/95bcb5fc && git apply ~/repos/grpc-go/verify/repro/c1_95bcb5fc_fault_injection.patch
$ VERIFY_FAULT=none go test ./internal/transport -run 'Test/(RecvBuffer_|ClientRecvMemoryWithManyTinyDataFrames)' -count=1 -timeout 120s -v
    recv_buffer_test.go:386: 32767 one-byte DATA frames retained 58328 bytes of heap
    recv_buffer_test.go:386: 32767 one-byte DATA frames retained 1933472 bytes of heap
    recv_buffer_test.go:140: compaction=true: 21007 payloads delivered as 19 messages
    recv_buffer_test.go:140: compaction=false: 21007 payloads delivered as 21007 messages
    --- PASS: Test/ClientRecvMemoryWithManyTinyDataFrames (0.11s)
        --- PASS: Test/ClientRecvMemoryWithManyTinyDataFrames/compaction=true (0.05s)
        --- PASS: Test/ClientRecvMemoryWithManyTinyDataFrames/compaction=false (0.06s)
    --- PASS: Test/RecvBuffer_CompactionErrorAfterData (0.00s)
    --- PASS: Test/RecvBuffer_CompactionInterleavedReadsAndWrites (0.01s)
    --- PASS: Test/RecvBuffer_CompactionPreservesDataAndOrder (0.01s)
        --- PASS: Test/RecvBuffer_CompactionPreservesDataAndOrder/compaction=true (0.00s)
        --- PASS: Test/RecvBuffer_CompactionPreservesDataAndOrder/compaction=false (0.01s)
ok  	google.golang.org/grpc/internal/transport	0.146s

$ VERIFY_FAULT=nodial go test ./internal/transport -run 'Test/(RecvBuffer_|ClientRecvMemoryWithManyTinyDataFrames)' -count=1 -timeout 120s -v     # wall 0.34s
    recv_buffer_test.go:385: Error while creating client transport: verify: nodial fault
    recv_buffer_test.go:385: Error while creating client transport: verify: nodial fault
    recv_buffer_test.go:217: Error while accepting: accept tcp 127.0.0.1:45495: use of closed network connection
FAIL	google.golang.org/grpc/internal/transport	0.005s

$ VERIFY_FAULT=nostream go test ... (same command)     # wall 0.34s
    recv_buffer_test.go:385: Error while creating stream: verify: nostream fault
    recv_buffer_test.go:243: Error while reading frame: EOF
    recv_buffer_test.go:385: Error while creating stream: verify: nostream fault
    recv_buffer_test.go:243: Error while reading frame: EOF
    recv_buffer_test.go:140: compaction=true: 21007 payloads delivered as 19 messages
    recv_buffer_test.go:140: compaction=false: 21007 payloads delivered as 21007 messages
    --- FAIL: Test/ClientRecvMemoryWithManyTinyDataFrames (0.00s)
        --- FAIL: Test/ClientRecvMemoryWithManyTinyDataFrames/compaction=true (0.00s)
        --- FAIL: Test/ClientRecvMemoryWithManyTinyDataFrames/compaction=false (0.00s)
    --- PASS: Test/RecvBuffer_CompactionErrorAfterData (0.00s)
    --- PASS: Test/RecvBuffer_CompactionInterleavedReadsAndWrites (0.01s)
    --- PASS: Test/RecvBuffer_CompactionPreservesDataAndOrder (0.01s)
        --- PASS: Test/RecvBuffer_CompactionPreservesDataAndOrder/compaction=true (0.00s)
        --- PASS: Test/RecvBuffer_CompactionPreservesDataAndOrder/compaction=false (0.00s)
FAIL	google.golang.org/grpc/internal/transport	0.020s

$ VERIFY_FAULT=stall go test ... (same command)     # wall 20.4s
    recv_buffer_test.go:385: Timed out waiting for the stream to receive trailers
    recv_buffer_test.go:329: Timed out waiting for the server to exit
    recv_buffer_test.go:385: Timed out waiting for the stream to receive trailers
    recv_buffer_test.go:329: Timed out waiting for the server to exit
    recv_buffer_test.go:140: compaction=true: 21007 payloads delivered as 19 messages
    recv_buffer_test.go:140: compaction=false: 21007 payloads delivered as 21007 messages
    --- FAIL: Test/ClientRecvMemoryWithManyTinyDataFrames (20.05s)
        --- FAIL: Test/ClientRecvMemoryWithManyTinyDataFrames/compaction=true (10.05s)
        --- FAIL: Test/ClientRecvMemoryWithManyTinyDataFrames/compaction=false (10.00s)
    --- PASS: Test/RecvBuffer_CompactionErrorAfterData (0.00s)
    --- PASS: Test/RecvBuffer_CompactionInterleavedReadsAndWrites (0.01s)
    --- PASS: Test/RecvBuffer_CompactionPreservesDataAndOrder (0.01s)
        --- PASS: Test/RecvBuffer_CompactionPreservesDataAndOrder/compaction=true (0.00s)
        --- PASS: Test/RecvBuffer_CompactionPreservesDataAndOrder/compaction=false (0.01s)
FAIL	google.golang.org/grpc/internal/transport	20.072s

$ VERIFY_FAULT=dropdata go test ... (same command)     # wall 1.2s (the multi-KB %q payload dumps on lines 169 are truncated here)
    recv_buffer_test.go:385: Error while reading 32767 bytes from the stream: EOF
    recv_buffer_test.go:385: Error while reading 32767 bytes from the stream: EOF
    recv_buffer_test.go:169: recvBuffer delivered "", want "\x00\x01\x02\x01\x02\x03\x02\x03\x04\x03\x04\x05\x04\x05\x06\x05\x06\a\x06\a\b\a\b\t\b\t\n\t\n\v\n\v
    recv_buffer_test.go:203: recvBuffer delivered 0 bytes that differ from the 150000 bytes put into it
    recv_buffer_test.go:129: recvBuffer delivered 0 bytes that differ from the 123278 bytes put into it
    recv_buffer_test.go:129: recvBuffer delivered 0 bytes that differ from the 123278 bytes put into it
    --- FAIL: Test/ClientRecvMemoryWithManyTinyDataFrames (0.07s)
        --- FAIL: Test/ClientRecvMemoryWithManyTinyDataFrames/compaction=true (0.03s)
        --- FAIL: Test/ClientRecvMemoryWithManyTinyDataFrames/compaction=false (0.03s)
    --- FAIL: Test/RecvBuffer_CompactionErrorAfterData (0.00s)
    --- FAIL: Test/RecvBuffer_CompactionInterleavedReadsAndWrites (0.00s)
    --- FAIL: Test/RecvBuffer_CompactionPreservesDataAndOrder (0.00s)
        --- FAIL: Test/RecvBuffer_CompactionPreservesDataAndOrder/compaction=true (0.00s)
        --- FAIL: Test/RecvBuffer_CompactionPreservesDataAndOrder/compaction=false (0.00s)
FAIL	google.golang.org/grpc/internal/transport	0.081s

$ VERIFY_FAULT=drop go test ./internal/transport -run 'Test/RecvBuffer_' -count=1 -timeout 120s -v     # wall 40.5s
    recv_buffer_test.go:164: Timed out waiting for a message from the recvBuffer
    recv_buffer_test.go:198: Timed out waiting for a message from the recvBuffer
    recv_buffer_test.go:124: Timed out waiting for a message from the recvBuffer
    recv_buffer_test.go:124: Timed out waiting for a message from the recvBuffer
    --- FAIL: Test/RecvBuffer_CompactionErrorAfterData (10.05s)
    --- FAIL: Test/RecvBuffer_CompactionInterleavedReadsAndWrites (10.05s)
    --- FAIL: Test/RecvBuffer_CompactionPreservesDataAndOrder (20.05s)
        --- FAIL: Test/RecvBuffer_CompactionPreservesDataAndOrder/compaction=true (10.00s)
        --- FAIL: Test/RecvBuffer_CompactionPreservesDataAndOrder/compaction=false (10.05s)
FAIL	google.golang.org/grpc/internal/transport	40.162s
$ git checkout internal/transport
```

One caveat, recorded for completeness: an earlier variant of the `drop` fault that also swallowed terminal error messages, run against `Test/ClientRecvMemoryWithManyTinyDataFrames`, did hang until the 120s global timeout:

```console
$ VERIFY_FAULT=drop go test ./internal/transport -run 'Test/(RecvBuffer_|ClientRecvMemoryWithManyTinyDataFrames)' -count=1 -timeout 120s -v
panic: test timed out after 2m0s
	running tests:
		Test (2m0s)
		Test/ClientRecvMemoryWithManyTinyDataFrames (2m0s)
		Test/ClientRecvMemoryWithManyTinyDataFrames/compaction=true (2m0s)
goroutine 10 [chan receive]:
google.golang.org/grpc/internal/transport.(*recvBufferReader).readClient(0xc0003380e8, 0x7fff)
	/home/ubuntu/wt/95bcb5fc/internal/transport/transport.go:397 +0x13c
...
google.golang.org/grpc/internal/transport.(*Stream).readTo(0xc000343e08?, {0xc00001a000, 0x7fff, 0x7fff})
	/home/ubuntu/wt/95bcb5fc/internal/transport/transport_test.go:85 +0x58
google.golang.org/grpc/internal/transport.tinyFramesRecvMemory(0xc0000bee00, 0x7fff)
	/home/ubuntu/wt/95bcb5fc/internal/transport/recv_buffer_test.go:355 +0x5d9
FAIL	google.golang.org/grpc/internal/transport	120.039s
```

That hang is inside pre-existing product code (`transport.go:397`, `m := <-r.recv.get()` *after* the stream context's deadline has already fired and `clientStream.Close` has been called); the test's own bound (the 10s stream context) did fire. It required a fault that breaks the transport's own cancellation delivery, so it is not counted as a test-authored unbounded operation. With every fault that leaves cancellation delivery intact, all blocking operations in the added tests — including server acceptance and teardown — terminated on a local timeout or on a close triggered by one.

## C2

Branch [evalon/grpc-go-tr-5401c189](https://github.com/kaitranntt-evals/grpc-go-transport-restrict-memory-overhead/tree/evalon/grpc-go-tr-5401c189). Claim: large frames are copied into compaction storage instead of bypassing compaction.

Probe `TestVerifyProbe_C2_LargeFrames`: real `NewServerTransport` with a tracing pool; a raw HTTP/2 peer sends `"occupy"` (6 bytes), one 1-byte frame, then three DATA frames of the given size on one stream while the handler does not read. For every queued `recvMsg` the probe reports which function acquired the pool buffer its bytes live in.

```console
$ verify/repro/run_probe.sh ~/wt/5401c189 'TestVerifyProbe_C2'
      queue[0]: len=6 -> heap (not from pool)
      queue[1]: len=1 -> pool buffer acquired by (*recvBuffer).newChunkLocked (Get(16384), cap=16384, entry starts at offset 0)
      queue[2]: len=16384 -> pool buffer acquired by (*framer).readDataFrame (Get(16384), cap=16384, entry starts at offset 0)
      queue[3]: len=16384 -> pool buffer acquired by (*framer).readDataFrame (Get(16384), cap=16384, entry starts at offset 0)
      queue[4]: len=16384 -> pool buffer acquired by (*framer).readDataFrame (Get(16384), cap=16384, entry starts at offset 0)
      pool.Get calls: 4 [(*framer).readDataFrame cap=16384 x3; (*recvBuffer).newChunkLocked cap=16384 x1]
    RESULT frame=16384: large frames queued in their ORIGINAL frame-reader storage: 3 of 3; copied into recvBuffer compaction storage: 0; recvBuffer.chunk!=nil:true; payload intact=true
      queue[0]: len=6 -> heap (not from pool)
      queue[1]: len=1 -> pool buffer acquired by (*recvBuffer).newChunkLocked (Get(16384), cap=16384, entry starts at offset 0)
      queue[2]: len=12000 -> pool buffer acquired by (*framer).readDataFrame (Get(12000), cap=16384, entry starts at offset 0)
      queue[3]: len=12000 -> pool buffer acquired by (*framer).readDataFrame (Get(12000), cap=16384, entry starts at offset 0)
      queue[4]: len=12000 -> pool buffer acquired by (*framer).readDataFrame (Get(12000), cap=16384, entry starts at offset 0)
      pool.Get calls: 4 [(*framer).readDataFrame cap=16384 x3; (*recvBuffer).newChunkLocked cap=16384 x1]
    RESULT frame=12000: large frames queued in their ORIGINAL frame-reader storage: 3 of 3; copied into recvBuffer compaction storage: 0; recvBuffer.chunk!=nil:true; payload intact=true
      queue[0]: len=6 -> heap (not from pool)
      queue[1]: len=1 -> pool buffer acquired by (*recvBuffer).newChunkLocked (Get(16384), cap=16384, entry starts at offset 0)
      queue[2]: len=8193 -> pool buffer acquired by (*framer).readDataFrame (Get(8193), cap=16384, entry starts at offset 0)
      queue[3]: len=8193 -> pool buffer acquired by (*framer).readDataFrame (Get(8193), cap=16384, entry starts at offset 0)
      queue[4]: len=8193 -> pool buffer acquired by (*framer).readDataFrame (Get(8193), cap=16384, entry starts at offset 0)
      pool.Get calls: 4 [(*framer).readDataFrame cap=16384 x3; (*recvBuffer).newChunkLocked cap=16384 x1]
    RESULT frame=8193: large frames queued in their ORIGINAL frame-reader storage: 3 of 3; copied into recvBuffer compaction storage: 0; recvBuffer.chunk!=nil:true; payload intact=true
      (+8193 bytes still pending inside the recvBuffer's open compaction buffer, not yet a queue entry)
      queue[0]: len=6 -> heap (not from pool)
      queue[1]: len=16384 -> pool buffer acquired by (*recvBuffer).newChunkLocked (Get(16384), cap=16384, entry starts at offset 0)
      pool.Get calls: 5 [(*framer).readDataFrame cap=16384 x3; (*recvBuffer).newChunkLocked cap=16384 x2]
    RESULT frame=8192: large frames queued in their ORIGINAL frame-reader storage: 0 of 3; copied into recvBuffer compaction storage: 3; recvBuffer.chunk!=nil:true; payload intact=true
      (+12289 bytes still pending inside the recvBuffer's open compaction buffer, not yet a queue entry)
      queue[0]: len=6 -> heap (not from pool)
      pool.Get calls: 4 [(*framer).readDataFrame cap=4096 x3; (*recvBuffer).newChunkLocked cap=16384 x1]
    RESULT frame=4096: large frames queued in their ORIGINAL frame-reader storage: 0 of 3; copied into recvBuffer compaction storage: 3; recvBuffer.chunk!=nil:true; payload intact=true
      (+6145 bytes still pending inside the recvBuffer's open compaction buffer, not yet a queue entry)
      queue[0]: len=6 -> heap (not from pool)
      pool.Get calls: 4 [(*framer).readDataFrame cap=4096 x3; (*recvBuffer).newChunkLocked cap=16384 x1]
    RESULT frame=2048: large frames queued in their ORIGINAL frame-reader storage: 0 of 3; copied into recvBuffer compaction storage: 3; recvBuffer.chunk!=nil:true; payload intact=true
--- PASS: TestVerifyProbe_C2_LargeFrames (0.02s)
    --- PASS: TestVerifyProbe_C2_LargeFrames/frame=16384 (0.00s)
    --- PASS: TestVerifyProbe_C2_LargeFrames/frame=12000 (0.00s)
    --- PASS: TestVerifyProbe_C2_LargeFrames/frame=8193 (0.00s)
    --- PASS: TestVerifyProbe_C2_LargeFrames/frame=8192 (0.00s)
    --- PASS: TestVerifyProbe_C2_LargeFrames/frame=4096 (0.00s)
    --- PASS: TestVerifyProbe_C2_LargeFrames/frame=2048 (0.00s)
PASS
ok  	google.golang.org/grpc/internal/transport	0.021s
```

Observed: frames of 16384, 12000 and 8193 bytes stay in the buffer acquired by `(*framer).readDataFrame` (original storage, offset 0, no `recvBuffer` acquisition for them). Frames of 8192 bytes and below are copied into the `(*recvBuffer).newChunkLocked` chunk — the branch's documented cut-off is `recvCompactionMaxPayloadSize = http2MaxFrameLen/2 = 8192`.

The eval fixture's own large-frame checks (1 MiB, 16384, 65536 — pointer identity of the backing array) pass on this branch; the fixture test fails later, at line 359, on a different assertion (a pool buffer still held after drain — see C7):

```console
$ cp ~/fx/tests/eval_recv_buffer_compaction_test.go ~/wt/5401c189/internal/transport/ && cd ~/wt/5401c189
$ go test -v -run '^(TestEval_RecvBufferCompactionSkippedLargeBuffer|TestEval_RecvBufferConfiguredPoolAcquisition)$' ./internal/transport -count=1
    eval_recv_buffer_compaction_test.go:359: Tracking pool: 1 acquired destination buffers were abandoned and never returned to the pool
--- FAIL: TestEval_RecvBufferCompactionSkippedLargeBuffer (0.00s)
    --- FAIL: TestEval_RecvBufferCompactionSkippedLargeBuffer/PoolOwnership_SliceGrowth (0.00s)
    eval_recv_buffer_compaction_test.go:686: Tracking pool: 1 acquired destination buffers were abandoned and never returned
--- FAIL: TestEval_RecvBufferConfiguredPoolAcquisition (0.00s)
    --- FAIL: TestEval_RecvBufferConfiguredPoolAcquisition/UnpooledConsolidation (0.00s)
FAIL
FAIL	google.golang.org/grpc/internal/transport	0.007s
FAIL
```

(The `t.Fatalf`s at fixture lines 247/252/255/258/290/293 — "Large buffer backing array was reallocated or recopied" etc. — did not fire; execution reached line 359.)

Verdict basis: REFUTED — full-size and >8 KiB frames keep their original backing storage on the real transport. Frames at or below half the maximum frame size (<= 8192 bytes) are intentionally compacted; that is the branch's design threshold, not large-frame copying.

## C3

Claim: compaction obtains its storage without acquiring it from the configured buffer pool. Probes: `TestVerifyProbe_C3_ServerStreamOverWire` (3000 one-byte DATA frames to a real `NewServerTransport` whose `ServerConfig.BufferPool` is the tracing pool, handler not reading) and `TestVerifyProbe_C3_ClientStream` (1026 one-byte puts on a stream created (a) by a client from `NewHTTP2Client(ConnectOptions{BufferPool: pool})` and (b) by the fixture-style struct literal `&http2Client{bufferPool: pool}`).

### [evalon/grpc-go-tr-51f38834](https://github.com/kaitranntt-evals/grpc-go-transport-restrict-memory-overhead/tree/evalon/grpc-go-tr-51f38834) — REFUTED

```console
$ verify/repro/run_probe.sh ~/wt/51f38834 'TestVerifyProbe_C3'
      (+2999 bytes still pending inside the recvBuffer's open compaction buffer, not yet a queue entry)
    server stream (NewServerTransport, configured pool traced), 3000 one-byte DATA frames, handler not reading:
      queue entries=1 ; backed by configured-pool buffers=0 map[] ; backed by heap=1
      configured pool Get calls while frames were queued: 2 [(*recvBuffer).compactLocked cap=256 x1; (*recvBuffer).growCompactLocked cap=4096 x1]
    RESULT: pool acquisitions made from recvBuffer code and still held by the queue: 1 [(*recvBuffer).growCompactLocked cap=4096 x1]
--- PASS: TestVerifyProbe_C3_ServerStreamOverWire (0.01s)
    client stream from NewHTTP2Client(ConnectOptions{BufferPool: pool}): after 1026 one-byte puts: backlog=0 ; configured pool Get calls=2 [(*recvBuffer).compactLocked cap=256 x1; (*recvBuffer).growCompactLocked cap=4096 x1]
    client stream from NewHTTP2Client(ConnectOptions{BufferPool: pool}): drained 1026/1026 bytes in order=true ; recvBuffer-acquired pool buffers still outstanding after drain: 0 [] ; recvBuffer.compact!=nil:false
    RESULT client stream from NewHTTP2Client(ConnectOptions{BufferPool: pool}): compaction storage acquired from configured pool = true
    client stream from fixture-style literal &http2Client{bufferPool: pool}: after 1026 one-byte puts: backlog=1025 ; configured pool Get calls=0 []
    client stream from fixture-style literal &http2Client{bufferPool: pool}: drained 1026/1026 bytes in order=true ; recvBuffer-acquired pool buffers still outstanding after drain: 0 [] ; recvBuffer.compact!=nil:false
    RESULT client stream from fixture-style literal &http2Client{bufferPool: pool}: compaction storage acquired from configured pool = false
--- PASS: TestVerifyProbe_C3_ClientStream (0.00s)
PASS
ok  	google.golang.org/grpc/internal/transport	0.015s
```

Observed: on streams created by the delivered transports (server over the wire, client via `NewHTTP2Client`) the compaction storage is acquired with `pool.Get` from `(*recvBuffer).compactLocked` / `(*recvBuffer).growCompactLocked` on the configured pool, and returned after drain (0 outstanding). The fixture reports "never acquired" only because it builds its client as a struct literal, which skips `NewHTTP2Client` where this branch sets `recvCompactionPool`; on such a stream compaction is simply off (backlog=1025, zero pool calls) — nothing is compacted, so no compaction storage is obtained from anywhere:

```console
$ cp ~/fx/tests/eval_recv_buffer_compaction_test.go ~/wt/51f38834/internal/transport/ && cd ~/wt/51f38834
$ go test -v -run '^(TestEval_RecvBufferCompactionSkippedLargeBuffer|TestEval_RecvBufferConfiguredPoolAcquisition)$' ./internal/transport -count=1
--- PASS: TestEval_RecvBufferCompactionSkippedLargeBuffer (0.00s)
    --- PASS: TestEval_RecvBufferCompactionSkippedLargeBuffer/PoolOwnership_SliceGrowth (0.00s)
    eval_recv_buffer_compaction_test.go:683: Compaction destination buffer was never acquired from the configured buffer pool
--- FAIL: TestEval_RecvBufferConfiguredPoolAcquisition (0.00s)
    --- FAIL: TestEval_RecvBufferConfiguredPoolAcquisition/UnpooledConsolidation (0.00s)
FAIL
FAIL	google.golang.org/grpc/internal/transport	0.010s
FAIL
```

### [evalon/grpc-go-tr-5401c189](https://github.com/kaitranntt-evals/grpc-go-transport-restrict-memory-overhead/tree/evalon/grpc-go-tr-5401c189) — REFUTED

```console
$ verify/repro/run_probe.sh ~/wt/5401c189 'TestVerifyProbe_C3'
      (+2999 bytes still pending inside the recvBuffer's open compaction buffer, not yet a queue entry)
    server stream (NewServerTransport, configured pool traced), 3000 one-byte DATA frames, handler not reading:
      queue entries=1 ; backed by configured-pool buffers=0 map[] ; backed by heap=1
      configured pool Get calls while frames were queued: 1 [(*recvBuffer).newChunkLocked cap=16384 x1]
    RESULT: pool acquisitions made from recvBuffer code and still held by the queue: 1 [(*recvBuffer).newChunkLocked cap=16384 x1]
--- PASS: TestVerifyProbe_C3_ServerStreamOverWire (0.02s)
    client stream from NewHTTP2Client(ConnectOptions{BufferPool: pool}): after 1026 one-byte puts: backlog=0 ; configured pool Get calls=1 [(*recvBuffer).newChunkLocked cap=16384 x1]
    client stream from NewHTTP2Client(ConnectOptions{BufferPool: pool}): drained 1026/1026 bytes in order=true ; recvBuffer-acquired pool buffers still outstanding after drain: 1 [(*recvBuffer).newChunkLocked cap=16384 x1] ; recvBuffer.chunk!=nil:true
    RESULT client stream from NewHTTP2Client(ConnectOptions{BufferPool: pool}): compaction storage acquired from configured pool = true
    client stream from fixture-style literal &http2Client{bufferPool: pool}: after 1026 one-byte puts: backlog=0 ; configured pool Get calls=1 [(*recvBuffer).newChunkLocked cap=16384 x1]
    client stream from fixture-style literal &http2Client{bufferPool: pool}: drained 1026/1026 bytes in order=true ; recvBuffer-acquired pool buffers still outstanding after drain: 1 [(*recvBuffer).newChunkLocked cap=16384 x1] ; recvBuffer.chunk!=nil:true
    RESULT client stream from fixture-style literal &http2Client{bufferPool: pool}: compaction storage acquired from configured pool = true
--- PASS: TestVerifyProbe_C3_ClientStream (0.00s)
PASS
ok  	google.golang.org/grpc/internal/transport	0.022s
```

Observed: every compaction chunk is acquired by `(*recvBuffer).newChunkLocked` via `Get(16384)` on the configured pool, on server, client and fixture-style streams alike. The fixture's `TestEval_RecvBufferConfiguredPoolAcquisition` fails on this branch at line 686 ("1 acquired destination buffers were abandoned and never returned"), i.e. the acquisition check at line 683 passed; the failure is the retained chunk (C7), not a pool bypass:

```console
$ go test -v -run '^(TestEval_RecvBufferCompactionSkippedLargeBuffer|TestEval_RecvBufferConfiguredPoolAcquisition)$' ./internal/transport -count=1   # in ~/wt/5401c189 with the fixture copied in
    eval_recv_buffer_compaction_test.go:359: Tracking pool: 1 acquired destination buffers were abandoned and never returned to the pool
--- FAIL: TestEval_RecvBufferCompactionSkippedLargeBuffer (0.00s)
    --- FAIL: TestEval_RecvBufferCompactionSkippedLargeBuffer/PoolOwnership_SliceGrowth (0.00s)
    eval_recv_buffer_compaction_test.go:686: Tracking pool: 1 acquired destination buffers were abandoned and never returned
--- FAIL: TestEval_RecvBufferConfiguredPoolAcquisition (0.00s)
    --- FAIL: TestEval_RecvBufferConfiguredPoolAcquisition/UnpooledConsolidation (0.00s)
FAIL
FAIL	google.golang.org/grpc/internal/transport	0.007s
FAIL
```

## C4

Branch [grpc-go-transport-restrict-memory-overhead-perfect](https://github.com/kaitranntt-evals/grpc-go/tree/grpc-go-transport-restrict-memory-overhead-perfect). Claim: 512 queued 64-byte handler reads stay in separate 16-KiB pooled buffers (~8 MiB of backing storage for 32 KiB of payload) on a 64-bit build.

Unit-level probe (`verify/repro/c4_c6_handler_reads_test.go`): the real `serverHandlerTransport.HandleStreams` reader goroutine, request body = `io.Pipe` fed with separate 64-byte writes (one `req.Body.Read` each), handler not reading, transport's pool wrapped by a tracking pool. The same run also includes a 48-byte control where the utilization check does not reset tracking.

```console
$ cd ~/repos/grpc-go && cp verify/repro/c4_c6_handler_reads_test.go internal/transport/zz_c4_c6_handler_reads_test.go
$ go test -v -run 'TestVerify_HandlerReads' ./internal/transport -count=1 ; rm internal/transport/zz_c4_c6_handler_reads_test.go
    GOARCH=amd64 uintptr=8 bytes recvMsgSize=56 utilizationFactor=2 compactionThreshold=58368 EnableReceiveBufferCompaction=true
    utilization comparison for one read: recvMsgSize+payload = 120 ; utilizationFactor*payload = 128 ; reset(no tracking) = true
    reads=512 readSize=64 total payload=32768 bytes (32 KiB)
    queued recvMsgs=512 (backlog=511 + chan=1); backlog payload=32704 bytes; backlog backing cap=8372224 bytes
    tracking after reads: uncompactedSuffixLen=0 uncompactedBytes=0
    pool.Get calls by requested size: map[16384:513]
    RETAINED pool buffers (Get without Put)=513, distinct backing arrays=513, total backing cap=8404992 bytes (8.02 MiB) [includes 1 buffer held by the blocked reader goroutine]
--- PASS: TestVerify_HandlerReads_C4_512x64 (0.06s)
    GOARCH=amd64 uintptr=8 bytes recvMsgSize=56 utilizationFactor=2 compactionThreshold=58368 EnableReceiveBufferCompaction=true
    utilization comparison for one read: recvMsgSize+payload = 120 ; utilizationFactor*payload = 128 ; reset(no tracking) = true
    reads=1024 readSize=64 total payload=65536 bytes (64 KiB)
    queued recvMsgs=1024 (backlog=1023 + chan=1); backlog payload=65472 bytes; backlog backing cap=16760832 bytes
    tracking after reads: uncompactedSuffixLen=0 uncompactedBytes=0
    pool.Get calls by requested size: map[16384:1025]
    RETAINED pool buffers (Get without Put)=1025, distinct backing arrays=1025, total backing cap=16793600 bytes (16.02 MiB) [includes 1 buffer held by the blocked reader goroutine]
--- PASS: TestVerify_HandlerReads_C6_1024x64 (0.06s)
    GOARCH=amd64 uintptr=8 bytes recvMsgSize=56 utilizationFactor=2 compactionThreshold=58368 EnableReceiveBufferCompaction=true
    utilization comparison for one read: recvMsgSize+payload = 104 ; utilizationFactor*payload = 96 ; reset(no tracking) = false
    reads=1024 readSize=48 total payload=49152 bytes (48 KiB)
    queued recvMsgs=463 (backlog=462 + chan=1); backlog payload=49104 bytes; backlog backing cap=7585792 bytes
    tracking after reads: uncompactedSuffixLen=461 uncompactedBytes=22128
    pool.Get calls by requested size: map[16384:1025 26976:1]
    RETAINED pool buffers (Get without Put)=464, distinct backing arrays=464, total backing cap=7618560 bytes (7.27 MiB) [includes 1 buffer held by the blocked reader goroutine]
--- PASS: TestVerify_HandlerReads_Control_1024x48 (0.06s)
PASS
ok  	google.golang.org/grpc/internal/transport	0.175s
```

Observed for 512 x 64 bytes: `recvMsgSize`=56 on amd64, so `recvMsgSize + 64 = 120 <= utilizationFactor*64 = 128`; `compactBacklogLocked` resets its tracking on every read (`uncompactedSuffixLen=0 uncompactedBytes=0` at the end), no compaction `Get` is ever made (only `Get(16384)` from the reader), and 512+1 distinct 16384-byte pool buffers remain un-returned (512 queued + 1 held by the reader blocked in `Read`): 8404992 bytes for 32768 payload bytes.

With the escape hatch set the result is identical, i.e. the fix has no effect on this path:

```console
$ GRPC_GO_EXPERIMENTAL_ENABLE_RECEIVE_BUFFER_COMPACTION=false go test -v -run 'TestVerify_HandlerReads_C6' ./internal/transport -count=1   # same probe file copied in
    GOARCH=amd64 uintptr=8 bytes recvMsgSize=56 utilizationFactor=2 compactionThreshold=58368 EnableReceiveBufferCompaction=false
    RETAINED pool buffers (Get without Put)=1025, distinct backing arrays=1025, total backing cap=16793600 bytes (16.02 MiB) [includes 1 buffer held by the blocked reader goroutine]
```

End-to-end (`verify/repro/c4_c6_e2e/main.go`): a real `grpc.Server` served by `net/http`'s HTTP/2 server (`httptest` + `ServeHTTP`, i.e. the handler transport), `experimental.BufferPool` = tracking pool, a raw HTTP/2 peer sending paced 64-byte DATA frames, RPC handler not reading. Live heap is measured with `runtime.GC` + `MemStats.HeapAlloc` before and after:

```console
$ cd ~/repos/grpc-go
$ go run ./verify/repro/c4_c6_e2e 512
GOARCH=amd64 GRPC_GO_EXPERIMENTAL_ENABLE_RECEIVE_BUFFER_COMPACTION=""
sent 512 DATA frames x 64 bytes = 32768 payload bytes (32 KiB) to grpc.Server.ServeHTTP; handler not reading
server buffer pool Get calls by requested size: map[16384:512]
server pool buffers retained (Get without Put): 512, total backing capacity 8388608 bytes (8.00 MiB)
process live heap growth while frames were queued: 8461528 bytes (8.07 MiB)
$ go run ./verify/repro/c4_c6_e2e 1024
GOARCH=amd64 GRPC_GO_EXPERIMENTAL_ENABLE_RECEIVE_BUFFER_COMPACTION=""
sent 1024 DATA frames x 64 bytes = 65536 payload bytes (64 KiB) to grpc.Server.ServeHTTP; handler not reading
server buffer pool Get calls by requested size: map[16384:1021]
server pool buffers retained (Get without Put): 1021, total backing capacity 16728064 bytes (15.95 MiB)
process live heap growth while frames were queued: 16880064 bytes (16.10 MiB)
$ go run ./verify/repro/c4_c6_e2e 1024 48
GOARCH=amd64 GRPC_GO_EXPERIMENTAL_ENABLE_RECEIVE_BUFFER_COMPACTION=""
sent 1024 DATA frames x 48 bytes = 49152 payload bytes (48 KiB) to grpc.Server.ServeHTTP; handler not reading
server buffer pool Get calls by requested size: map[16384:1021 27072:1]
server pool buffers retained (Get without Put): 462, total backing capacity 7585792 bytes (7.23 MiB)
process live heap growth while frames were queued: 7649024 bytes (7.29 MiB)
$ GRPC_GO_EXPERIMENTAL_ENABLE_RECEIVE_BUFFER_COMPACTION=false go run ./verify/repro/c4_c6_e2e 1024
GOARCH=amd64 GRPC_GO_EXPERIMENTAL_ENABLE_RECEIVE_BUFFER_COMPACTION="false"
sent 1024 DATA frames x 64 bytes = 65536 payload bytes (64 KiB) to grpc.Server.ServeHTTP; handler not reading
server buffer pool Get calls by requested size: map[16384:1022]
server pool buffers retained (Get without Put): 1022, total backing capacity 16744448 bytes (15.97 MiB)
process live heap growth while frames were queued: 16901320 bytes (16.12 MiB)
```

Impact reasoning: any server that serves gRPC through `grpc.Server.ServeHTTP` (the `net/http` integration) is affected. A peer that sends small DATA frames to a stream whose handler is slow makes the server hold one 16 KiB buffer per frame — a 256x amplification at 64 bytes (8 MiB of live heap for 32 KiB of payload, measured end to end), and the handler transport's reader goroutine queued every frame while the RPC handler was idle (all N reads were queued in both probes). The delivered compaction does not help: at >= 56 bytes per read the utilization test (`backlogHeapSize <= 2*payload`) counts only payload length, not the 16 KiB capacity actually pinned, so tracking resets on every read; behaviour is byte-for-byte the same with `GRPC_GO_EXPERIMENTAL_ENABLE_RECEIVE_BUFFER_COMPACTION=false`. Even at 48 bytes per read, where compaction does trigger once, 7.2 MiB stays pinned for 48 KiB. The fixture suite stays green on this branch (it only feeds exact-size buffers), so nothing guards this path. No user-side workaround other than not using `ServeHTTP` or reading promptly.

## C6

Branch [grpc-go-transport-restrict-memory-overhead-perfect](https://github.com/kaitranntt-evals/grpc-go/tree/grpc-go-transport-restrict-memory-overhead-perfect). Claim: 1024 queued 64-byte handler reads stay in separate 16-KiB pooled buffers (~16 MiB of backing storage for 64 KiB of payload) on a 64-bit build.

Unit-level probe (`verify/repro/c4_c6_handler_reads_test.go`): the real `serverHandlerTransport.HandleStreams` reader goroutine, request body = `io.Pipe` fed with separate 64-byte writes (one `req.Body.Read` each), handler not reading, transport's pool wrapped by a tracking pool. The same run also includes a 48-byte control where the utilization check does not reset tracking.

```console
$ cd ~/repos/grpc-go && cp verify/repro/c4_c6_handler_reads_test.go internal/transport/zz_c4_c6_handler_reads_test.go
$ go test -v -run 'TestVerify_HandlerReads' ./internal/transport -count=1 ; rm internal/transport/zz_c4_c6_handler_reads_test.go
    GOARCH=amd64 uintptr=8 bytes recvMsgSize=56 utilizationFactor=2 compactionThreshold=58368 EnableReceiveBufferCompaction=true
    utilization comparison for one read: recvMsgSize+payload = 120 ; utilizationFactor*payload = 128 ; reset(no tracking) = true
    reads=512 readSize=64 total payload=32768 bytes (32 KiB)
    queued recvMsgs=512 (backlog=511 + chan=1); backlog payload=32704 bytes; backlog backing cap=8372224 bytes
    tracking after reads: uncompactedSuffixLen=0 uncompactedBytes=0
    pool.Get calls by requested size: map[16384:513]
    RETAINED pool buffers (Get without Put)=513, distinct backing arrays=513, total backing cap=8404992 bytes (8.02 MiB) [includes 1 buffer held by the blocked reader goroutine]
--- PASS: TestVerify_HandlerReads_C4_512x64 (0.06s)
    GOARCH=amd64 uintptr=8 bytes recvMsgSize=56 utilizationFactor=2 compactionThreshold=58368 EnableReceiveBufferCompaction=true
    utilization comparison for one read: recvMsgSize+payload = 120 ; utilizationFactor*payload = 128 ; reset(no tracking) = true
    reads=1024 readSize=64 total payload=65536 bytes (64 KiB)
    queued recvMsgs=1024 (backlog=1023 + chan=1); backlog payload=65472 bytes; backlog backing cap=16760832 bytes
    tracking after reads: uncompactedSuffixLen=0 uncompactedBytes=0
    pool.Get calls by requested size: map[16384:1025]
    RETAINED pool buffers (Get without Put)=1025, distinct backing arrays=1025, total backing cap=16793600 bytes (16.02 MiB) [includes 1 buffer held by the blocked reader goroutine]
--- PASS: TestVerify_HandlerReads_C6_1024x64 (0.06s)
    GOARCH=amd64 uintptr=8 bytes recvMsgSize=56 utilizationFactor=2 compactionThreshold=58368 EnableReceiveBufferCompaction=true
    utilization comparison for one read: recvMsgSize+payload = 104 ; utilizationFactor*payload = 96 ; reset(no tracking) = false
    reads=1024 readSize=48 total payload=49152 bytes (48 KiB)
    queued recvMsgs=463 (backlog=462 + chan=1); backlog payload=49104 bytes; backlog backing cap=7585792 bytes
    tracking after reads: uncompactedSuffixLen=461 uncompactedBytes=22128
    pool.Get calls by requested size: map[16384:1025 26976:1]
    RETAINED pool buffers (Get without Put)=464, distinct backing arrays=464, total backing cap=7618560 bytes (7.27 MiB) [includes 1 buffer held by the blocked reader goroutine]
--- PASS: TestVerify_HandlerReads_Control_1024x48 (0.06s)
PASS
ok  	google.golang.org/grpc/internal/transport	0.175s
```

Observed for 1024 x 64 bytes: `recvMsgSize`=56 on amd64, so `recvMsgSize + 64 = 120 <= utilizationFactor*64 = 128`; `compactBacklogLocked` resets its tracking on every read (`uncompactedSuffixLen=0 uncompactedBytes=0` at the end), no compaction `Get` is ever made (only `Get(16384)` from the reader), and 1024+1 distinct 16384-byte pool buffers remain un-returned (1024 queued + 1 held by the reader blocked in `Read`): 16793600 bytes for 65536 payload bytes.

With the escape hatch set the result is identical, i.e. the fix has no effect on this path:

```console
$ GRPC_GO_EXPERIMENTAL_ENABLE_RECEIVE_BUFFER_COMPACTION=false go test -v -run 'TestVerify_HandlerReads_C6' ./internal/transport -count=1   # same probe file copied in
    GOARCH=amd64 uintptr=8 bytes recvMsgSize=56 utilizationFactor=2 compactionThreshold=58368 EnableReceiveBufferCompaction=false
    RETAINED pool buffers (Get without Put)=1025, distinct backing arrays=1025, total backing cap=16793600 bytes (16.02 MiB) [includes 1 buffer held by the blocked reader goroutine]
```

End-to-end (`verify/repro/c4_c6_e2e/main.go`): a real `grpc.Server` served by `net/http`'s HTTP/2 server (`httptest` + `ServeHTTP`, i.e. the handler transport), `experimental.BufferPool` = tracking pool, a raw HTTP/2 peer sending paced 64-byte DATA frames, RPC handler not reading. Live heap is measured with `runtime.GC` + `MemStats.HeapAlloc` before and after:

```console
$ cd ~/repos/grpc-go
$ go run ./verify/repro/c4_c6_e2e 512
GOARCH=amd64 GRPC_GO_EXPERIMENTAL_ENABLE_RECEIVE_BUFFER_COMPACTION=""
sent 512 DATA frames x 64 bytes = 32768 payload bytes (32 KiB) to grpc.Server.ServeHTTP; handler not reading
server buffer pool Get calls by requested size: map[16384:512]
server pool buffers retained (Get without Put): 512, total backing capacity 8388608 bytes (8.00 MiB)
process live heap growth while frames were queued: 8461528 bytes (8.07 MiB)
$ go run ./verify/repro/c4_c6_e2e 1024
GOARCH=amd64 GRPC_GO_EXPERIMENTAL_ENABLE_RECEIVE_BUFFER_COMPACTION=""
sent 1024 DATA frames x 64 bytes = 65536 payload bytes (64 KiB) to grpc.Server.ServeHTTP; handler not reading
server buffer pool Get calls by requested size: map[16384:1021]
server pool buffers retained (Get without Put): 1021, total backing capacity 16728064 bytes (15.95 MiB)
process live heap growth while frames were queued: 16880064 bytes (16.10 MiB)
$ go run ./verify/repro/c4_c6_e2e 1024 48
GOARCH=amd64 GRPC_GO_EXPERIMENTAL_ENABLE_RECEIVE_BUFFER_COMPACTION=""
sent 1024 DATA frames x 48 bytes = 49152 payload bytes (48 KiB) to grpc.Server.ServeHTTP; handler not reading
server buffer pool Get calls by requested size: map[16384:1021 27072:1]
server pool buffers retained (Get without Put): 462, total backing capacity 7585792 bytes (7.23 MiB)
process live heap growth while frames were queued: 7649024 bytes (7.29 MiB)
$ GRPC_GO_EXPERIMENTAL_ENABLE_RECEIVE_BUFFER_COMPACTION=false go run ./verify/repro/c4_c6_e2e 1024
GOARCH=amd64 GRPC_GO_EXPERIMENTAL_ENABLE_RECEIVE_BUFFER_COMPACTION="false"
sent 1024 DATA frames x 64 bytes = 65536 payload bytes (64 KiB) to grpc.Server.ServeHTTP; handler not reading
server buffer pool Get calls by requested size: map[16384:1022]
server pool buffers retained (Get without Put): 1022, total backing capacity 16744448 bytes (15.97 MiB)
process live heap growth while frames were queued: 16901320 bytes (16.12 MiB)
```

Impact reasoning: any server that serves gRPC through `grpc.Server.ServeHTTP` (the `net/http` integration) is affected. A peer that sends small DATA frames to a stream whose handler is slow makes the server hold one 16 KiB buffer per frame — a 256x amplification at 64 bytes (16 MiB of live heap for 64 KiB of payload, measured end to end), and the handler transport's reader goroutine queued every frame while the RPC handler was idle (all N reads were queued in both probes). The delivered compaction does not help: at >= 56 bytes per read the utilization test (`backlogHeapSize <= 2*payload`) counts only payload length, not the 16 KiB capacity actually pinned, so tracking resets on every read; behaviour is byte-for-byte the same with `GRPC_GO_EXPERIMENTAL_ENABLE_RECEIVE_BUFFER_COMPACTION=false`. Even at 48 bytes per read, where compaction does trigger once, 7.2 MiB stays pinned for 48 KiB. The fixture suite stays green on this branch (it only feeds exact-size buffers), so nothing guards this path. No user-side workaround other than not using `ServeHTTP` or reading promptly.

## C5

Branch [evalon/grpc-go-tr-51f38834](https://github.com/kaitranntt-evals/grpc-go-transport-restrict-memory-overhead/tree/evalon/grpc-go-tr-51f38834). Claim: with a supported 4-KiB-minimum pool, a message assembled from 1,024 separately drained three-byte bursts retains one pool-sized compaction allocation per burst (>= 4 MiB for 3 KiB).

Probe `TestVerifyProbe_C5_BurstAssembly`: the eval fixture's `IncrementalMessageAssembly` workload verbatim (1,024 cycles of three 1-byte puts followed by `recvBufferReader.Read` until 3 bytes are read, delivered buffers retained until the end) on a stream created by `NewHTTP2Client`, with the pool swapped for the public `mem.NewBinaryTieredBufferPool(12, 14, 15, 20)` (smallest tier 4 KiB), plus the default pool as control. `TestVerifyProbe_C5_OverWire`: the same shape over a real connection — the server application calls `Stream.read(3072)` once while a raw HTTP/2 peer sends 1,024 paced bursts of three 1-byte DATA frames (timing-dependent).

```console
$ verify/repro/run_probe.sh ~/wt/51f38834 'TestVerifyProbe_C5'
    4KiB-min pool: payload=3072 bytes in order=true ; delivered buffers retained=2048 ; distinct backing arrays=2048 (pool-backed=1024) ; distinct retained backing capacity=4195328 bytes (4.00 MiB)
    4KiB-min pool: pool Get calls during workload=1024 [(*recvBuffer).compactLocked cap=4096 x1024]
    RESULT 4KiB-min pool: recvBuffer-acquired pool buffers still outstanding (retained by delivered payloads)=1024, 4194304 bytes (4.00 MiB) [(*recvBuffer).compactLocked cap=4096 x1024]
    default pool: payload=3072 bytes in order=true ; delivered buffers retained=2048 ; distinct backing arrays=2048 (pool-backed=0) ; distinct retained backing capacity=3072 bytes (0.00 MiB)
    default pool: pool Get calls during workload=1024 [(*recvBuffer).compactLocked cap=256 x1024]
    RESULT default pool: recvBuffer-acquired pool buffers still outstanding (retained by delivered payloads)=0, 0 bytes (0.00 MiB) []
--- PASS: TestVerifyProbe_C5_BurstAssembly (0.02s)
    --- PASS: TestVerifyProbe_C5_BurstAssembly/pool=4KiB-minimum_NewBinaryTieredBufferPool(12,14,15,20) (0.02s)
    --- PASS: TestVerifyProbe_C5_BurstAssembly/pool=default (0.00s)
    RESULT over-the-wire: message of 3072 bytes assembled (in order=true) from 3045 buffers ; pool Get calls=977 ; recvBuffer-acquired pool buffers retained by the unfinished message=977, 4001792 bytes (3.82 MiB) [(*recvBuffer).compactLocked cap=4096 x977]
    after freeing the message: recvBuffer-acquired pool buffers outstanding=0
--- PASS: TestVerifyProbe_C5_OverWire (0.94s)
PASS
ok  	google.golang.org/grpc/internal/transport	0.970s
```

Observed: with the 4-KiB-minimum pool, each burst triggers one `(*recvBuffer).compactLocked` `Get` that returns a 4096-byte buffer holding 2 bytes; all 1,024 stay outstanding while the message is being assembled: 4,194,304 bytes of pool storage (+1,024 one-byte heap buffers) for 3,072 payload bytes. Over the wire, 977 of 1,024 bursts did the same (3.82 MiB). With the default pool (smallest tier 256 bytes, below the 1 KiB pooling threshold, so the branch copies out and returns the buffer) nothing is retained.

Controls — same probe with compaction disabled on the same branch, and on the audited branch:

```console
$ verify/repro/run_probe.sh ~/wt/51f38834 'TestVerifyProbe_C5' GRPC_GO_EXPERIMENTAL_ENABLE_RECEIVE_BUFFER_COMPACTION=false | grep RESULT
    RESULT 4KiB-min pool: recvBuffer-acquired pool buffers still outstanding (retained by delivered payloads)=0, 0 bytes (0.00 MiB) []
    RESULT default pool: recvBuffer-acquired pool buffers still outstanding (retained by delivered payloads)=0, 0 bytes (0.00 MiB) []
    RESULT over-the-wire: message of 3072 bytes assembled (in order=true) from 3072 buffers ; pool Get calls=0 ; recvBuffer-acquired pool buffers retained by the unfinished message=0, 0 bytes (0.00 MiB) []

$ verify/repro/run_probe.sh ~/repos/grpc-go 'TestVerifyProbe_C5' | grep RESULT      # audited branch
    RESULT 4KiB-min pool: recvBuffer-acquired pool buffers still outstanding (retained by delivered payloads)=0, 0 bytes (0.00 MiB) []
    RESULT default pool: recvBuffer-acquired pool buffers still outstanding (retained by delivered payloads)=0, 0 bytes (0.00 MiB) []
    RESULT over-the-wire: message of 3072 bytes assembled (in order=true) from 3072 buffers ; pool Get calls=0 ; recvBuffer-acquired pool buffers retained by the unfinished message=0, 0 bytes (0.00 MiB) []
```

Impact reasoning: on this branch, compaction makes memory *worse* than no compaction in this configuration — 4 MiB instead of ~3 KiB (a ~1,365x amplification of payload) while one message is being assembled from trickled tiny frames with the reader keeping up between bursts. The trigger needs a non-default but supported pool whose smallest tier is >= 4 KiB (`mem.NewBinaryTieredBufferPool` / `mem.NewTieredBufferPool` via `experimental.WithBufferPool` / `experimental.BufferPool`); with the default pool the effect is absent. The cause is `compactLocked` calling `pool.Get(len(data))` for a fresh compaction buffer per burst and `flushCompactLocked` handing the whole pool-sized buffer to the reader whenever its capacity is above the pooling threshold. The eval fixture's `IncrementalMessageAssembly` passes on this branch because it uses the default pool. Workaround: default pool, or `GRPC_GO_EXPERIMENTAL_ENABLE_RECEIVE_BUFFER_COMPACTION=false`.

## C7

Branch [evalon/grpc-go-tr-5401c189](https://github.com/kaitranntt-evals/grpc-go-transport-restrict-memory-overhead/tree/evalon/grpc-go-tr-5401c189). Claim: resetting a fully drained server stream leaves its compaction allocation un-returned to the pool. Parts: (1) retention after draining; (2) no release on server reset.

Probe `TestVerifyProbe_C7_ResetAfterDrain`: real `NewServerTransport` with tracing pool; raw HTTP/2 peer sends 100 one-byte DATA frames; the server application then reads all 100 bytes with `Stream.readTo` (which frees every delivered buffer); then the peer sends `RST_STREAM` (control: an empty DATA frame with `END_STREAM`, then the application reads the EOF). The probe waits until the stream is gone from `http2Server.activeStreams`, sleeps 300ms, runs GC, and lists pool buffers acquired under a `recvBuffer` method that were never `Put`.

```console
$ verify/repro/run_probe.sh ~/wt/5401c189 'TestVerifyProbe_C7'
      (+99 bytes still pending inside the recvBuffer's open compaction buffer, not yet a queue entry)
    1. 100 one-byte frames queued, nothing read: recvBuffer-acquired pool buffers outstanding=1 [(*recvBuffer).newChunkLocked cap=16384 x1]
    2. fully drained (100 bytes, in order=true) and all delivered buffers freed: backlog=0 chan=0 ; recvBuffer-acquired pool buffers outstanding=1 (16384 bytes) [(*recvBuffer).newChunkLocked cap=16384 x1] ; recvBuffer.chunk!=nil:true
    3. after RST_STREAM: stream still in activeStreams=false ; stream ctx err=context canceled ; state=3(streamDone=true)
    RESULT RST_STREAM: recvBuffer-acquired pool buffers NOT returned to the pool after termination=1 (16384 bytes) [(*recvBuffer).newChunkLocked cap=16384 x1] ; pool.Put calls total=0 ; recvBuffer.chunk!=nil:true
      (+99 bytes still pending inside the recvBuffer's open compaction buffer, not yet a queue entry)
    1. 100 one-byte frames queued, nothing read: recvBuffer-acquired pool buffers outstanding=1 [(*recvBuffer).newChunkLocked cap=16384 x1]
    2. fully drained (100 bytes, in order=true) and all delivered buffers freed: backlog=0 chan=0 ; recvBuffer-acquired pool buffers outstanding=1 (16384 bytes) [(*recvBuffer).newChunkLocked cap=16384 x1] ; recvBuffer.chunk!=nil:true
       read after END_STREAM -> EOF
    RESULT END_STREAM(control): recvBuffer-acquired pool buffers NOT returned to the pool after termination=0 (0 bytes) [] ; pool.Put calls total=1 ; recvBuffer.chunk!=nil:false
--- PASS: TestVerifyProbe_C7_ResetAfterDrain (0.61s)
    --- PASS: TestVerifyProbe_C7_ResetAfterDrain/RST_STREAM (0.31s)
    --- PASS: TestVerifyProbe_C7_ResetAfterDrain/END_STREAM(control) (0.31s)
PASS
ok  	google.golang.org/grpc/internal/transport	0.617s
```

- **Part 1, retention after draining — holds.** Step 2: backlog=0, chan=0, every delivered buffer freed, yet one 16384-byte buffer acquired by `(*recvBuffer).newChunkLocked` is still outstanding and `recvBuffer.chunk != nil`.
- **Part 2, release on server reset — holds.** After `RST_STREAM` the stream is removed (`activeStreams=false`, ctx `context canceled`, state `streamDone`), `pool.Put` was called 0 times and the chunk is still outstanding with `recvBuffer.chunk != nil`. In the `END_STREAM` control the terminal `io.EOF` `recvMsg` releases it (`Put` total=1, outstanding=0).

Control on the audited branch (no retained compaction state):

```console
$ verify/repro/run_probe.sh ~/repos/grpc-go 'TestVerifyProbe_C7' | grep RESULT
    RESULT RST_STREAM: recvBuffer-acquired pool buffers NOT returned to the pool after termination=0 (0 bytes) [] ; pool.Put calls total=0 ; (no retained-compaction field on this branch)
    RESULT END_STREAM(control): recvBuffer-acquired pool buffers NOT returned to the pool after termination=0 (0 bytes) [] ; pool.Put calls total=0 ; (no retained-compaction field on this branch)
```

The eval fixture observes the same retained buffer on this branch (`eval_recv_buffer_compaction_test.go:359`/`:686`/`:754`: "1 acquired destination buffers were abandoned and never returned").

Impact reasoning: on this branch every stream that ever compacted keeps a 16 KiB pool buffer pinned for as long as the stream lives, even when it holds no unread data, and when the stream ends by reset/cancellation (a routine path: client cancels, deadline expiry) instead of a clean end-of-stream, that buffer is never handed back to the pool — it is dropped for the garbage collector. It is not an unbounded leak (GC reclaims it once the stream is unreachable), but it defeats pool reuse on cancelled streams (the eval fixture's tracking pool flags it as abandoned), and adds 16 KiB of idle retention per long-lived stream. Fix location: release `b.chunk` when it has no pending bytes and the backlog is empty, and/or from `http2Server.closeStream` / the stream cancel path.

## C8

Branch [grpc-go-transport-restrict-memory-overhead-perfect](https://github.com/kaitranntt-evals/grpc-go/tree/grpc-go-transport-restrict-memory-overhead-perfect). Claim: delivered Go sources in `internal/transport` or `internal/envconfig` have `gofmt -s -d -l` differences. Run on the pristine checkout (the untracked `verify/` directory stashed away, no fixture or probe file copied in):

```console
$ cd ~/repos/grpc-go && git rev-parse HEAD
327a6ff993d9866ea656ed166b89744aadcbc940
$ git stash -u -q && git status --short        # (empty: clean tree)
$ bash -o pipefail -c 'source scripts/common.sh; gofmt -s -d -l internal/transport internal/envconfig 2>&1 | fail_on_output'; echo "exit=$?"
exit=0
$ gofmt -s -d -l internal/transport internal/envconfig | wc -c
0
$ printf 'package x\nfunc  f( ){ }\n' > /tmp/bad.go      # positive control: the same pipeline does flag a misformatted file
$ bash -o pipefail -c 'source scripts/common.sh; gofmt -s -d -l /tmp/bad.go 2>&1 | fail_on_output'; echo "control exit=$?"
/tmp/bad.go
diff /tmp/bad.go.orig /tmp/bad.go
--- /tmp/bad.go.orig
+++ /tmp/bad.go
@@ -1,2 +1,3 @@
 package x
-func  f( ){ }
+
+func f() {}
control exit=1
$ git stash pop -q
```

Observed: no output and exit 0 for the delivered directories; the control shows the pipeline reports and fails on a misformatted file. REFUTED.
