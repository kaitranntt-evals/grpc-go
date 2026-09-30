# Evidence for audit run v-173762e7

Observations only. Every command below was run on this machine (go1.25.7 linux/amd64). Claim-target branches live in
[kaitranntt-evals/grpc-go-transport-restrict-memory-overhead](https://github.com/kaitranntt-evals/grpc-go-transport-restrict-memory-overhead);
`verify/repro/setup_worktrees.sh` fetches each one and checks it out at `/tmp/claims/<suffix>`. All commands are run from the root of this
repository on the `verify/grpc-go-transport-restrict-memory-overhead-v-173762e7` branch unless a `cd` is shown. Raw logs of the runs quoted
here are in `verify/logs/`. No production file of this branch is modified; every mutation/instrumentation is applied to a claim worktree
by a script or patch under `verify/repro/` and reverted afterwards (`git checkout -- .`).

`verify/go.mod` exists only to keep the `*_test.go` repro files in `verify/repro/` (which compile only after being copied into a claim
worktree) out of `go build ./...` / `go vet ./...` of the grpc module.

## C1

Claim: the added or changed tests do not verify receive-buffer or stream behavior across a consumption step followed by additional
reception or a terminal transition on the same live buffer or stream. Nine target branches.

### Method

`verify/repro/c1_mutant.py` instruments `recvBuffer` in a claim worktree (`internal/transport/transport.go`, reverted afterwards):

- *Detection.* Only a reader removes messages from `b.c`, so finding `b.c` empty on entry to `put()` or `load()` after an earlier `put()`
  means a message was consumed. From then on the buffer is "post-consumption".
- *Mutation.* Every data `put()` on a post-consumption buffer has its payload replaced by `0xEE` bytes; every terminal `put()` has its
  error replaced by `errC1Mutant`. The first such put of each kind per buffer prints `C1-MUTANT-HIT ... kind=data|terminal`.
  A test that asserts payload / terminal state across *receive -> consume -> receive-or-terminal* on one live buffer must therefore fail
  whenever that sequence occurs, and a run with zero hits is a run in which no reception or terminal transition followed any consumption.
- *Schedule hook.* With `C1_READER_DELAY_MS=300`, the first `Read`/`ReadMessageHeader` on each `recvBufferReader` sleeps 300 ms. This is a
  legal schedule (the reader goroutine is descheduled before its first read); it does not change any test's synchronization.

`verify/repro/c1_run.sh <worktree>` runs every `func (s) Test...` that the branch adds in `internal/transport/recv_buffer_test.go`
(the only test file each of the nine branches adds; `git diff c92e9857 HEAD --stat -- '*_test.go'` shows nothing else except a
two-line `s.buf.init()` -> `s.buf.init(nil)` signature change in `transport_test.go` on d0520117, whose two touched tests are passed as the
extra argument there): unmutated once, then under the mutant x10 (default GOMAXPROCS), x10 (`-cpu 1`), x5 with the reader delay, and
finally the eval fixture (byte-exact archive copy, sha256 `eb9f51f263ce5736fb7b056718e49902a4281b55ebe24834b88c90327d792b24`) under the mutant.
`verify/repro/c1_attr.py` attributes hits and PASS/FAIL to each test. Below, for each branch, the lines for the branch's concurrent test and
every line with a non-zero hit count are quoted; all other added tests report `data=0 terminal=0` in every mode (full tables:
`verify/logs/c1/<suffix>/summary.txt`).

```sh
verify/repro/setup_worktrees.sh
export OUT=$PWD/verify/logs/c1 EVAL_FIXTURE=/home/ubuntu/eval/tests/eval_recv_buffer_compaction_test.go
for s in fec69d29 86a3fb3a 3472ebaa 1b13d423 698fb805 ba102f71 5ccaf207 8f035afd; do verify/repro/c1_run.sh /tmp/claims/$s; done
verify/repro/c1_run.sh /tmp/claims/d0520117 'ReadGivesSameErrorAfterAnyErrorOccurs|ReadMessageHeaderMultipleBuffers'
```

### What the tables show (same on all nine branches)

1. In every mode, every added test other than the concurrent one has `data=0 terminal=0`: all of its puts (including the terminal
   one) happen before its first read. No added deterministic test performs reception after consumption, nor a terminal transition after
   consumption.
2. The concurrent test is the only added test in which a put ever follows a consumption. Under the normal schedules it does
   (10/10 runs, both GOMAXPROCS settings) and then the mutant makes it fail - so its assertions are sensitive to the sequence *when the
   scheduler happens to produce it*.
3. Nothing in the test requires the scheduler to produce it. Each concurrent test starts one free-running writer goroutine
   (`go func() { ... put/write ...; put(EOF) }()`) and reads on the test goroutine; none has a channel, WaitGroup or other handshake that
   makes a read precede a later write (two branches call `runtime.Gosched()` in the writer, which is a hint, not an ordering). With the
   reader's first read delayed 300 ms the writer finishes first: the mutant records **zero** post-consumption puts (`data=0 terminal=0`)
   and the test **passes 5/5 under the mutant**. I.e. the execution "all reception and the terminal put finish, then consumption begins"
   is accepted by every assertion of the concurrent test, on every branch.
4. An earlier run of the same script on 1b13d423 hit that schedule without the delay hook: `-cpu 1` gave
   `Test/RecvBuffer_ConcurrentReadWrite: PASS=1 FAIL=9 post-consumption-put buffers: data=9 terminal=9` - one of ten runs had no
   post-consumption put and passed under the mutant (`verify/logs/c1/1b13d423/mutant_cpu1_earlier_run.log`).
5. Contrast: the eval fixture's `TestEval_RecvBufferCompaction_MultiCycleMemoryBound` (not part of any branch) performs the sequence
   deterministically - `data=1` hit and FAIL under the mutant on every branch
   (`eval_recv_buffer_compaction_test.go:360: Multi-cycle payload mismatch: got 10110 bytes, want 10110 bytes`). That is what a test that
   *requires* the sequence looks like under this mutant; no branch-added test behaves that way.
6. d0520117 only: the pre-existing `TestReadGivesSameErrorAfterAnyErrorOccurs` (touched only by the `init(nil)` signature change) shows
   hits in every mode and still passes under the mutant. Its first put is already terminal (`recvMsg{buffer, err: testErr}`), so the
   buffer is no longer live when it is read, the later puts are dropped by `put()`, and its assertions (count 0, first error) are
   satisfied by the reader's sticky `r.err` whatever is put afterwards. It does not assert payload, queue state, suffix tracking or
   compaction behavior across the sequence.

Example of the mutant firing in the normal schedule (fec69d29, `verify/logs/c1/fec69d29/mutant_default.log`):

```console
C1-MUTANT-HIT post-consumption put on this buffer, kind=data
C1-MUTANT-HIT post-consumption put on this buffer, kind=terminal
    recv_buffer_test.go:345: Read() failed: C1 mutant: terminal put after consumption
    --- FAIL: Test/RecvBuffer_CompactionConcurrentReader (0.01s)
```

Concurrent-test synchronization as written (writer lines / reader lines, from `internal/transport/recv_buffer_test.go` of each branch):

```console
fec69d29 TestRecvBuffer_CompactionConcurrentReader (321-356): go func() { rb.put(...)xN; rb.put(recvMsg{err: io.EOF}) }() ; loop r.Read(97)
86a3fb3a TestRecvBuffer_ConcurrentWritesAndReads (317-358): go func() { s.write(...)xN; s.write(recvMsg{err: io.EOF}) }() ; loop s.read ; s.read(1) == io.EOF
3472ebaa TestRecvBuffer_ConcurrentReadWrite (128-164): wg.Add(1); go func() { defer wg.Done(); st.buf.put(...)xN; st.buf.put(EOF) }() ; loop st.read ; wg.Wait() after the reads
1b13d423 TestRecvBuffer_ConcurrentReadWrite (312-352): go func() { st.write(...)xN; st.write(EOF) }() ; readAll ; st.readTo == io.EOF
698fb805 TestRecvBuffer_CompactionConcurrentReadWrite (245-291): wg.Add(1); go func() { defer wg.Done(); st.write(...)xN; st.write(EOF) }() ; loop st.readTo ; wg.Wait()
ba102f71 TestRecvBuffer_ConcurrentReadAndCompaction (323-348): go func() { rb.put(...)xN; rb.put(EOF) }() ; reader loop
d0520117 TestRecvBuffer_ConcurrentSmallFrames (313-360): go func() { st.write(...)xN with runtime.Gosched(); st.write(EOF) }() ; loop st.readTo
5ccaf207 TestReceiveBufferCompactionConcurrentReads (278-319): b.put(...) prefill; go func() { defer close(done); b.put(...)xN; b.put(EOF) }() ; defer <-done ; loop reader.Read(17)
8f035afd TestRecvBufferCompactionConcurrent (309-353): b.put(...) prefill; go func() { defer close(done); b.put(...)xN with runtime.Gosched(); b.put(EOF) }() ; defer <-done ; loop r.Read(17)
```

### C1 on evalon/grpc-go-tr-fec69d29

```console
$ EVAL_FIXTURE=<archive>/tests/eval_recv_buffer_compaction_test.go verify/repro/c1_run.sh /tmp/claims/fec69d29
BRANCH evalon/grpc-go-tr-fec69d29 @ aeff3cad
TESTS: RecvBuffer_SmallMessagesCompaction|RecvBuffer_SmallMessagesBoundedMemory|RecvBuffer_SmallMessagesAmortizedAllocations|RecvBuffer_LargeMessagesBypassCompaction|RecvBuffer_CompactionWithError|RecvBuffer_CompactionConcurrentReader|ServerStream_TinyDataFramesCompacted
### baseline (unmutated), -count=1: exit=0
  Test/RecvBuffer_CompactionConcurrentReader: PASS=1 FAIL=0 post-consumption-put buffers: data=0 terminal=0
### mutant, default GOMAXPROCS, -count=10: exit=1
  Test/RecvBuffer_CompactionConcurrentReader: PASS=0 FAIL=10 post-consumption-put buffers: data=10 terminal=10
### mutant, -cpu 1, -count=10: exit=1
  Test/RecvBuffer_CompactionConcurrentReader: PASS=0 FAIL=10 post-consumption-put buffers: data=10 terminal=10
### mutant, C1_READER_DELAY_MS=300 (reader's first read delayed), -count=5: exit=0
  Test/RecvBuffer_CompactionConcurrentReader: PASS=5 FAIL=0 post-consumption-put buffers: data=0 terminal=0
### mutant, eval fixture (archive copy), -count=1: exit=1
  TestEval_RecvBufferCompaction_MultiCycleMemoryBound: PASS=0 FAIL=1 post-consumption-put buffers: data=1 terminal=0
```

### C1 on evalon/grpc-go-tr-86a3fb3a

```console
$ EVAL_FIXTURE=<archive>/tests/eval_recv_buffer_compaction_test.go verify/repro/c1_run.sh /tmp/claims/86a3fb3a
BRANCH evalon/grpc-go-tr-86a3fb3a @ 3d898d51
TESTS: RecvBuffer_CompactsSmallBuffers|RecvBuffer_SmallBuffersHeapUsageBounded|RecvBuffer_LargeBuffersBypassCompaction|RecvBuffer_CompactionReleasesPooledBuffers|RecvBuffer_ErrorAfterCompactedData|RecvBuffer_ConcurrentWritesAndReads|ServerStream_TinyDataFramesCompacted
### baseline (unmutated), -count=1: exit=0
  Test/RecvBuffer_ConcurrentWritesAndReads: PASS=1 FAIL=0 post-consumption-put buffers: data=0 terminal=0
### mutant, default GOMAXPROCS, -count=10: exit=1
  Test/RecvBuffer_ConcurrentWritesAndReads: PASS=0 FAIL=10 post-consumption-put buffers: data=10 terminal=10
### mutant, -cpu 1, -count=10: exit=1
  Test/RecvBuffer_ConcurrentWritesAndReads: PASS=0 FAIL=10 post-consumption-put buffers: data=10 terminal=10
### mutant, C1_READER_DELAY_MS=300 (reader's first read delayed), -count=5: exit=0
  Test/RecvBuffer_ConcurrentWritesAndReads: PASS=5 FAIL=0 post-consumption-put buffers: data=0 terminal=0
### mutant, eval fixture (archive copy), -count=1: exit=1
  TestEval_RecvBufferCompaction_MultiCycleMemoryBound: PASS=0 FAIL=1 post-consumption-put buffers: data=1 terminal=0
```

### C1 on evalon/grpc-go-tr-3472ebaa

```console
$ EVAL_FIXTURE=<archive>/tests/eval_recv_buffer_compaction_test.go verify/repro/c1_run.sh /tmp/claims/3472ebaa
BRANCH evalon/grpc-go-tr-3472ebaa @ 234a558d
TESTS: RecvBuffer_CompactsSmallFrames|RecvBuffer_CompactionDisabled|RecvBuffer_ConcurrentReadWrite|RecvBuffer_LargeFramesBypassCompaction|RecvBuffer_CompactionReleasesPooledBuffers|ServerStream_CompactsTinyDataFrames
### baseline (unmutated), -count=1: exit=0
  Test/RecvBuffer_ConcurrentReadWrite: PASS=1 FAIL=0 post-consumption-put buffers: data=0 terminal=0
### mutant, default GOMAXPROCS, -count=10: exit=1
  Test/RecvBuffer_ConcurrentReadWrite: PASS=0 FAIL=10 post-consumption-put buffers: data=10 terminal=10
### mutant, -cpu 1, -count=10: exit=1
  Test/RecvBuffer_ConcurrentReadWrite: PASS=0 FAIL=10 post-consumption-put buffers: data=10 terminal=10
### mutant, C1_READER_DELAY_MS=300 (reader's first read delayed), -count=5: exit=0
  Test/RecvBuffer_ConcurrentReadWrite: PASS=5 FAIL=0 post-consumption-put buffers: data=0 terminal=0
### mutant, eval fixture (archive copy), -count=1: exit=1
  TestEval_RecvBufferCompaction_MultiCycleMemoryBound: PASS=0 FAIL=1 post-consumption-put buffers: data=1 terminal=0
```

### C1 on evalon/grpc-go-tr-1b13d423

```console
$ EVAL_FIXTURE=<archive>/tests/eval_recv_buffer_compaction_test.go verify/repro/c1_run.sh /tmp/claims/1b13d423
BRANCH evalon/grpc-go-tr-1b13d423 @ 8c38a3d0
TESTS: RecvBuffer_SmallBuffersOnSlowStream|RecvBuffer_SmallBuffersMemoryBounded|RecvBuffer_LargeBuffersBypassCompaction|RecvBuffer_ErrorAfterCompactedData|RecvBuffer_ConcurrentReadWrite|ClientStream_ManySmallDataFramesCompacted
### baseline (unmutated), -count=1: exit=0
  Test/RecvBuffer_ConcurrentReadWrite: PASS=1 FAIL=0 post-consumption-put buffers: data=0 terminal=0
### mutant, default GOMAXPROCS, -count=10: exit=1
  Test/RecvBuffer_ConcurrentReadWrite: PASS=0 FAIL=10 post-consumption-put buffers: data=10 terminal=10
### mutant, -cpu 1, -count=10: exit=1
  Test/RecvBuffer_ConcurrentReadWrite: PASS=0 FAIL=10 post-consumption-put buffers: data=10 terminal=10
### mutant, C1_READER_DELAY_MS=300 (reader's first read delayed), -count=5: exit=0
  Test/RecvBuffer_ConcurrentReadWrite: PASS=5 FAIL=0 post-consumption-put buffers: data=0 terminal=0
### mutant, eval fixture (archive copy), -count=1: exit=1
  TestEval_RecvBufferCompaction_MultiCycleMemoryBound: PASS=0 FAIL=1 post-consumption-put buffers: data=1 terminal=0
```

### C1 on evalon/grpc-go-tr-698fb805

```console
$ EVAL_FIXTURE=<archive>/tests/eval_recv_buffer_compaction_test.go verify/repro/c1_run.sh /tmp/claims/698fb805
BRANCH evalon/grpc-go-tr-698fb805 @ 267b4cb7
TESTS: RecvBuffer_CompactsSmallMessages|RecvBuffer_LargeMessagesBypassCompaction|RecvBuffer_CompactionPreservesErrorOrdering|RecvBuffer_CompactionConcurrentReadWrite|RecvBuffer_CompactionDisabled|ServerCompactsTinyDataFramesOnSlowStream
### baseline (unmutated), -count=1: exit=0
  Test/RecvBuffer_CompactionConcurrentReadWrite: PASS=1 FAIL=0 post-consumption-put buffers: data=0 terminal=0
### mutant, default GOMAXPROCS, -count=10: exit=1
  Test/RecvBuffer_CompactionConcurrentReadWrite: PASS=0 FAIL=10 post-consumption-put buffers: data=10 terminal=10
### mutant, -cpu 1, -count=10: exit=1
  Test/RecvBuffer_CompactionConcurrentReadWrite: PASS=0 FAIL=10 post-consumption-put buffers: data=10 terminal=10
### mutant, C1_READER_DELAY_MS=300 (reader's first read delayed), -count=5: exit=0
  Test/RecvBuffer_CompactionConcurrentReadWrite: PASS=5 FAIL=0 post-consumption-put buffers: data=0 terminal=0
### mutant, eval fixture (archive copy), -count=1: exit=1
  TestEval_RecvBufferCompaction_MultiCycleMemoryBound: PASS=0 FAIL=1 post-consumption-put buffers: data=1 terminal=0
```

### C1 on evalon/grpc-go-tr-ba102f71

```console
$ EVAL_FIXTURE=<archive>/tests/eval_recv_buffer_compaction_test.go verify/repro/c1_run.sh /tmp/claims/ba102f71
BRANCH evalon/grpc-go-tr-ba102f71 @ 7fb14b10
TESTS: RecvBuffer_ManySmallFrames|RecvBuffer_CompactionReleasesPooledBuffers|RecvBuffer_LargeAndLoneFramesBypassCompaction|RecvBuffer_ErrorAfterCompaction|RecvBuffer_ConcurrentReadAndCompaction|ClientTransport_ManySmallDataFrames
### baseline (unmutated), -count=1: exit=0
  Test/RecvBuffer_ConcurrentReadAndCompaction: PASS=1 FAIL=0 post-consumption-put buffers: data=0 terminal=0
### mutant, default GOMAXPROCS, -count=10: exit=1
  Test/RecvBuffer_ConcurrentReadAndCompaction: PASS=0 FAIL=10 post-consumption-put buffers: data=10 terminal=10
### mutant, -cpu 1, -count=10: exit=1
  Test/RecvBuffer_ConcurrentReadAndCompaction: PASS=0 FAIL=10 post-consumption-put buffers: data=10 terminal=10
### mutant, C1_READER_DELAY_MS=300 (reader's first read delayed), -count=5: exit=0
  Test/RecvBuffer_ConcurrentReadAndCompaction: PASS=5 FAIL=0 post-consumption-put buffers: data=0 terminal=0
### mutant, eval fixture (archive copy), -count=1: exit=1
  TestEval_RecvBufferCompaction_MultiCycleMemoryBound: PASS=0 FAIL=1 post-consumption-put buffers: data=1 terminal=0
```

### C1 on evalon/grpc-go-tr-d0520117

```console
$ EVAL_FIXTURE=<archive>/tests/eval_recv_buffer_compaction_test.go verify/repro/c1_run.sh /tmp/claims/d0520117 'ReadGivesSameErrorAfterAnyErrorOccurs|ReadMessageHeaderMultipleBuffers'
BRANCH evalon/grpc-go-tr-d0520117 @ 48827689
TESTS: RecvBuffer_ManySmallFrames|RecvBuffer_SmallFramesMemoryBounded|RecvBuffer_LargeFramesBypassCompaction|RecvBuffer_ErrorAfterCompactedData|RecvBuffer_ConcurrentSmallFrames|ServerReceivesManyTinyDataFrames|ReadGivesSameErrorAfterAnyErrorOccurs|ReadMessageHeaderMultipleBuffers
### baseline (unmutated), -count=1: exit=0
  Test/RecvBuffer_ConcurrentSmallFrames: PASS=1 FAIL=0 post-consumption-put buffers: data=0 terminal=0
### mutant, default GOMAXPROCS, -count=10: exit=1
  Test/ReadGivesSameErrorAfterAnyErrorOccurs: PASS=10 FAIL=0 post-consumption-put buffers: data=10 terminal=10
  Test/RecvBuffer_ConcurrentSmallFrames: PASS=0 FAIL=10 post-consumption-put buffers: data=10 terminal=10
### mutant, -cpu 1, -count=10: exit=1
  Test/ReadGivesSameErrorAfterAnyErrorOccurs: PASS=10 FAIL=0 post-consumption-put buffers: data=10 terminal=10
  Test/RecvBuffer_ConcurrentSmallFrames: PASS=0 FAIL=10 post-consumption-put buffers: data=10 terminal=10
### mutant, C1_READER_DELAY_MS=300 (reader's first read delayed), -count=5: exit=0
  Test/ReadGivesSameErrorAfterAnyErrorOccurs: PASS=5 FAIL=0 post-consumption-put buffers: data=5 terminal=5
  Test/RecvBuffer_ConcurrentSmallFrames: PASS=5 FAIL=0 post-consumption-put buffers: data=0 terminal=0
### mutant, eval fixture (archive copy), -count=1: exit=1
  TestEval_RecvBufferCompaction_MultiCycleMemoryBound: PASS=0 FAIL=1 post-consumption-put buffers: data=1 terminal=0
```

### C1 on evalon/grpc-go-tr-5ccaf207

```console
$ EVAL_FIXTURE=<archive>/tests/eval_recv_buffer_compaction_test.go verify/repro/c1_run.sh /tmp/claims/5ccaf207
BRANCH evalon/grpc-go-tr-5ccaf207 @ 26053afb
TESTS: ReceiveBufferSmallFrames|ReceiveBufferCompactionOwnership|ReceiveBufferCompactionAppend|ReceiveBufferCompactionConcurrentReads
### baseline (unmutated), -count=1: exit=0
  Test/ReceiveBufferCompactionConcurrentReads: PASS=1 FAIL=0 post-consumption-put buffers: data=0 terminal=0
### mutant, default GOMAXPROCS, -count=10: exit=1
  Test/ReceiveBufferCompactionConcurrentReads: PASS=0 FAIL=10 post-consumption-put buffers: data=10 terminal=10
### mutant, -cpu 1, -count=10: exit=1
  Test/ReceiveBufferCompactionConcurrentReads: PASS=0 FAIL=10 post-consumption-put buffers: data=10 terminal=10
### mutant, C1_READER_DELAY_MS=300 (reader's first read delayed), -count=5: exit=0
  Test/ReceiveBufferCompactionConcurrentReads: PASS=5 FAIL=0 post-consumption-put buffers: data=0 terminal=0
### mutant, eval fixture (archive copy), -count=1: exit=1
  TestEval_RecvBufferCompaction_MultiCycleMemoryBound: PASS=0 FAIL=1 post-consumption-put buffers: data=1 terminal=0
```

### C1 on evalon/grpc-go-tr-8f035afd

```console
$ EVAL_FIXTURE=<archive>/tests/eval_recv_buffer_compaction_test.go verify/repro/c1_run.sh /tmp/claims/8f035afd
BRANCH evalon/grpc-go-tr-8f035afd @ 5dfd941c
TESTS: ReceiveBufferCompaction|RecvBufferCompactionCopies|RecvBufferCompactionReleaseAndErrors|RecvBufferCompactionConcurrent
### baseline (unmutated), -count=1: exit=0
  Test/RecvBufferCompactionConcurrent: PASS=1 FAIL=0 post-consumption-put buffers: data=0 terminal=0
### mutant, default GOMAXPROCS, -count=10: exit=1
  Test/RecvBufferCompactionConcurrent: PASS=0 FAIL=10 post-consumption-put buffers: data=10 terminal=10
### mutant, -cpu 1, -count=10: exit=1
  Test/RecvBufferCompactionConcurrent: PASS=0 FAIL=10 post-consumption-put buffers: data=10 terminal=10
### mutant, C1_READER_DELAY_MS=300 (reader's first read delayed), -count=5: exit=0
  Test/RecvBufferCompactionConcurrent: PASS=5 FAIL=0 post-consumption-put buffers: data=0 terminal=0
### mutant, eval fixture (archive copy), -count=1: exit=1
  TestEval_RecvBufferCompaction_MultiCycleMemoryBound: PASS=0 FAIL=1 post-consumption-put buffers: data=1 terminal=0
```


### Impact reasoning (all nine branches)

The only branch-added coverage of "consume, then receive more / terminate on the same live buffer" - the path where compaction must
not append into a chunk the reader already took, and where the terminal entry must be queued behind partially drained data - comes from
an unsynchronized writer/reader race. It is exercised in the usual schedule on this machine (10/10), but the tests accept the schedule
in which it is never exercised, so on a loaded or single-core CI runner a regression in that path can pass the branch's whole added
suite (observed: mutant passes 5/5 when the reader starts late, and 1/10 naturally on 1b13d423 with `-cpu 1`). The deterministic
multi-cycle sequence is only covered by the external eval fixture.

## C2

Claim: added or behaviorally changed tests execute a potentially blocking operation without an effective local bound that interrupts the
operation if it stalls. Four target branches. `verify/repro/c2_run.sh <suffix>` performs the induced stall for each branch and restores
the worktree; `defaultTestTimeout` is 10 s on all branches, so a locally bounded operation fails at about 10 s, an unbounded one only
when `go test -timeout` kills the binary.

```sh
verify/repro/setup_worktrees.sh
export OUT=$PWD/verify/logs/c2
for s in 8ac37769 80a05d05 6fe210bc f70d0754; do verify/repro/c2_run.sh $s; done
```

### C2 on evalon/grpc-go-tr-f70d0754

Added test `TestServerCompactsSmallDataFrames` (`internal/transport/recv_buffer_test.go:369-378`):

```go
	conn, err := net.Dial("tcp", server.lis.Addr().String())
	if err != nil {
		t.Fatalf("Client failed to dial: %v", err)
	}
	defer conn.Close()
	if err := conn.SetDeadline(time.Now().Add(defaultTestTimeout)); err != nil {
```

`net.Dial` has no timeout/context; the deadline is installed only after it returns. Induced stall: the prebuilt test binary is run in a
private network namespace whose loopback drops every TCP SYN (`verify/repro/c2_blackhole.sh`), so the connection attempt stalls.

```console
$ (cd /tmp/claims/f70d0754 && go test -c -o /tmp/f70d0754.test ./internal/transport)
$ time verify/repro/c2_blackhole.sh /tmp/f70d0754.test '^Test$/^ServerCompactsSmallDataFrames$' 45s
=== RUN   Test
=== RUN   Test/ServerCompactsSmallDataFrames
panic: test timed out after 45s
	running tests:
		Test (45s)
		Test/ServerCompactsSmallDataFrames (45s)
...
goroutine 9 [IO wait]:
...
net.(*netFD).connect(0xc000368380, {0xcf4738, 0x1221820}, {0x41e9d4?, 0x0?}, {0xced2e0?, 0xc0002f3820?})
...
net.Dial({0xbe6ad2?, 0x0?}, {0xc00036e4b0?, 0x1?})
	/usr/local/go/src/net/dial.go:471 +0x77
google.golang.org/grpc/internal/transport.s.TestServerCompactsSmallDataFrames({{}}, 0xc0000b6a80)
	/tmp/claims/f70d0754/internal/transport/recv_buffer_test.go:372 +0x129
...
real	0m45.051s
```

With a 10-minute test timeout the same run is released only by the kernel's SYN retry limit, 134 s in
(`verify/logs/c2/c2_f70d0754_blackhole_notimeout.log`, `c2_blackhole.sh <bin> '^Test$/^ServerCompactsSmallDataFrames$' 600s`):

```console
    recv_buffer_test.go:374: Client failed to dial: dial tcp 127.0.0.1:45585: connect: connection timed out
--- FAIL: Test (134.27s)
real	2m14.289s
```

Control on 6fe210bc in the same blackhole: that branch's e2e test dials through `NewHTTP2Client(ctx, ...)` with a 10 s context and fails
locally at 10.05 s (`transport_test.go:3731: NewHTTP2Client failed: connection error: desc = "transport: Error while dialing: dial tcp 127.0.0.1:42253: i/o timeout"`),
so the namespace itself is not what makes f70d0754 hang (`verify/logs/c2/c2_6fe210bc_blackhole_control.log`).

Impact: if the listener cannot be reached promptly (accept backlog full, firewalled/odd loopback in a sandbox), this test sits in
`net.Dial` for the kernel connect timeout (about 2 min here) or until the global `go test -timeout` panics the whole package binary,
instead of failing itself after `defaultTestTimeout`. On a healthy loopback the dial returns immediately, so this only bites in a
degraded environment.

### C2 on evalon/grpc-go-tr-80a05d05

Added test `TestClientReceivesManySmallDataFrames` (`internal/transport/transport_test.go:3745-3804`) registers `defer server.stop()`
and adds a new stream handler `handleStreamManySmallDataFrames` (`transport_test.go:272-284`). `server.stop()` (`transport_test.go:599-608`)
ends with a bare `<-s.servingTasksDone`, which is closed only after `wg.Wait()` over the handler goroutines (`transport_test.go:430-433`):

```go
func (s *server) stop() {
	s.lis.Close()
	...
	<-s.servingTasksDone
}
```

Induced stall: `verify/repro/c2_80a05d05_handler_stall.patch` makes `ServerStream.WriteStatus` (the last call of the new handler) block
forever when `VERIFY_C2_STALL_HANDLER` is set.

```console
$ cd /tmp/claims/80a05d05 && go test ./internal/transport -run '^Test$/^ClientReceivesManySmallDataFrames$' -count=1 -v -timeout 45s
--- PASS: Test (0.12s)
    --- PASS: Test/ClientReceivesManySmallDataFrames (0.12s)
ok  	google.golang.org/grpc/internal/transport	0.127s
$ git apply <repo>/verify/repro/c2_80a05d05_handler_stall.patch
$ time VERIFY_C2_STALL_HANDLER=1 go test ./internal/transport -run '^Test$/^ClientReceivesManySmallDataFrames$' -count=1 -v -timeout 45s
=== RUN   Test/ClientReceivesManySmallDataFrames/compaction=true
    transport_test.go:3797: readTo() after all data = connection error: desc = "error reading from server: read tcp 127.0.0.1:38410->127.0.0.1:40153: use of closed network connection", want io.EOF
panic: test timed out after 45s
	running tests:
		Test (45s)
		Test/ClientReceivesManySmallDataFrames (45s)
		Test/ClientReceivesManySmallDataFrames/compaction=true (45s)
...
goroutine 10 [chan receive]:
google.golang.org/grpc/internal/transport.(*server).stop(0xc000036900)
	/tmp/claims/80a05d05/internal/transport/transport_test.go:607 +0x16b
runtime.Goexit()
testing.(*common).FailNow(0xc0000bee00)
testing.(*common).Fatalf(0xc0000bee00, {0xc047a6?, 0x1?}, {0xc00027ff28?, 0x0?, 0x1?})
google.golang.org/grpc/internal/transport.s.TestClientReceivesManySmallDataFrames.func1(0xc0000bee00)
	/tmp/claims/80a05d05/internal/transport/transport_test.go:3797 +0x78f

goroutine 11 [sync.WaitGroup.Wait]:
sync.(*WaitGroup).Wait(0xc00022cc20)
google.golang.org/grpc/internal/transport.(*server).start.func1()
	/tmp/claims/80a05d05/internal/transport/transport_test.go:432 +0x25

goroutine 22 [select (no cases)]:
google.golang.org/grpc/internal/transport.(*ServerStream).WriteStatus(0xc0003261a0, 0xc000316008)
	/tmp/claims/80a05d05/internal/transport/server_stream.go:79 +0x57
google.golang.org/grpc/internal/transport.(*testStreamHandler).handleStreamManySmallDataFrames(0xc000322048, 0xc0003261a0)
	/tmp/claims/80a05d05/internal/transport/transport_test.go:283 +0x199
real	0m46.584s
$ git checkout -- .
```

The test body's own waits are bounded (it failed by itself via `t.Fatalf`), but the deferred cleanup then waited on the stalled handler
until the 45 s global timeout. (An earlier run of the same patch failed first at `transport_test.go:3770: timed out waiting for the server
to finish the stream` and then hung at the same `transport_test.go:607`: `verify/logs/c2/c2_80a05d05_stall_earlier_run.log`.)

Impact: when the new handler goroutine does not finish (e.g. a transport regression that blocks its status write), the test's useful
local failure is followed by a hang in `server.stop()`; the package binary is killed by the global timeout and later tests in the package
do not run. `server.stop()` is a pre-existing helper with the same property for older tests; the new test and handler newly route through
it.

### C2 on evalon/grpc-go-tr-6fe210bc

Added test `TestClientReceivesManySmallDataFrames` (`internal/transport/recv_buffer_test.go:340-402`) creates a 10 s context, hands it to
the pre-existing helper `setupRSTStreamOnEOSTest(ctx, t, serverFrames)` (`transport_test.go:3621-3756`), and then waits with a bare receive:

```go
	ctx, cancel := context.WithTimeout(context.Background(), defaultTestTimeout)
	defer cancel()
	stream, waitForServer := setupRSTStreamOnEOSTest(ctx, t, serverFrames)
	defer waitForServer()

	// Wait for the client to have received all frames, which is signalled by
	// the stream being closed due to END_STREAM in the last DATA frame.
	<-stream.Done()
```

The helper uses `ctx` only in its own selects (manual-server goroutine and `waitForServerDone`); nothing connects `ctx` to
`stream.Done()`. The only way the helper's timeout reaches that wait is indirect: its server goroutine returns on `ctx.Done()`, its
deferred `conn.Close()` closes the connection, and the client transport's read loop then tears the stream down.

(a) Induced stall of the client read loop (`verify/repro/c2_6fe210bc_reader_stall.patch`: the 100th `recvBuffer.put` on a buffer never
returns when `VERIFY_C2_STALL_PUT` is set):

```console
$ cd /tmp/claims/6fe210bc && git apply <repo>/verify/repro/c2_6fe210bc_reader_stall.patch
$ time VERIFY_C2_STALL_PUT=1 go test ./internal/transport -run '^Test$/^ClientReceivesManySmallDataFrames$' -count=1 -v -timeout 40s
=== RUN   Test/ClientReceivesManySmallDataFrames
C2-STALL: recvBuffer.put #100 stalls forever
    transport_test.go:3717: Test timed out when waiting for a RST_STREAM frame from client
    transport_test.go:3693: Server reader goroutine failed to read frame: read tcp 127.0.0.1:43317->127.0.0.1:57118: read: connection reset by peer
panic: test timed out after 40s
	running tests:
		Test (40s)
		Test/ClientReceivesManySmallDataFrames (40s)
...
goroutine 9 [chan receive]:
google.golang.org/grpc/internal/transport.s.TestClientReceivesManySmallDataFrames({{}}, 0x494ed8?)
	/tmp/claims/6fe210bc/internal/transport/recv_buffer_test.go:376 +0x19c
...
goroutine 19 [select (no cases)]:
google.golang.org/grpc/internal/transport.(*recvBuffer).put(0xc000320070, {{0xcf55e0?, 0xc000013458?}, {0x0?, 0x0?}})
	/tmp/claims/6fe210bc/internal/transport/transport.go:129 +0x372
google.golang.org/grpc/internal/transport.(*http2Client).handleData(0xc000128008, 0xc000124038)
	/tmp/claims/6fe210bc/internal/transport/http2_client.go:1282 +0x305
real	0m42.255s
$ git checkout -- .
```

The helper's 10 s context fired (its `Test timed out ...` errors are printed) and the helper closed the connection, yet the test stayed in
`<-stream.Done()` at `recv_buffer_test.go:376` until the 40 s global timeout.

(b) Different stall, same wait: the peer never sends END_STREAM (`verify/repro/c2_6fe210bc_peer_stall_test.go`, same helper and same
bare `<-stream.Done()`). Here the indirect path works and the wait is released at the context deadline:

```console
$ cp verify/repro/c2_6fe210bc_peer_stall_test.go /tmp/claims/6fe210bc/internal/transport/ && (cd /tmp/claims/6fe210bc && go test ./internal/transport -run '^Test$/^VerifyC2_PeerWithholdsEndStream$' -count=1 -v -timeout 60s)
    c2_6fe210bc_peer_stall_test.go:35: <-stream.Done() returned after 10.05s; ctx.Err()=context deadline exceeded status=rpc error: code = Unavailable desc = connection error: desc = "error reading from server: read tcp 127.0.0.1:60580->127.0.0.1:44741: use of closed network connection"
    transport_test.go:3751: Test timed out when waiting for server to be done
--- FAIL: Test (10.10s)
```

(c) Terminal message lost (`verify/repro/c2_lose_terminal_mutant.py`: with `VERIFY_C2_LOSE_TERMINAL=1`, `recvBuffer.put` drops the first
terminal message and everything after it). The branch's four buffer-level tests pass a 10 s context into `newTestStream(ctx, pool)` and fail
locally; the added `BenchmarkRecvBufferSmallFrames` (`recv_buffer_test.go:406-432`) passes `context.WithCancel(context.Background())` into
the same helper and blocks until killed (`go test` stops its `-timeout` alarm before running benchmarks, so `-timeout 25s` does not apply;
the run was ended by `timeout -s QUIT 60s`):

```console
$ cd /tmp/claims/6fe210bc && python3 <repo>/verify/repro/c2_lose_terminal_mutant.py internal/transport/transport.go
$ VERIFY_C2_LOSE_TERMINAL=1 go test ./internal/transport -run '^Test$/^RecvBufferCompaction_' -count=1 -v -timeout 120s
    recv_buffer_test.go:333: Stream.read(1) returned error rpc error: code = DeadlineExceeded desc = context deadline exceeded, want test error
    recv_buffer_test.go:256: Stream.read(1) at end of stream returned error rpc error: code = DeadlineExceeded desc = context deadline exceeded, want test error
    recv_buffer_test.go:186: Stream.read(1) at end of stream returned error rpc error: code = DeadlineExceeded desc = context deadline exceeded, want EOF
    --- FAIL: Test/RecvBufferCompaction_DataAfterError (10.00s)
    --- FAIL: Test/RecvBufferCompaction_LargeFramesBypassCompaction (10.05s)
$ time VERIFY_C2_LOSE_TERMINAL=1 timeout -s QUIT 60s go test ./internal/transport -run '^$' -bench 'RecvBufferSmallFrames' -benchtime 1x -timeout 25s
SIGQUIT: quit
...
google.golang.org/grpc/internal/transport.(*recvBufferReader).read(...)
	/tmp/claims/6fe210bc/internal/transport/transport.go:323 +0x85
...
google.golang.org/grpc/internal/transport.BenchmarkRecvBufferSmallFrames.func1(...)
	/tmp/claims/6fe210bc/internal/transport/recv_buffer_test.go:423 +0x1b4
FAIL	google.golang.org/grpc/internal/transport	59.687s
real	1m0.015s
$ git checkout -- .
```

Impact: the e2e test's central wait is a bare channel receive whose only bound is a side effect of the helper tearing down the peer
connection. That releases it when the peer stalls (b) but not when the client read loop itself stalls (a) - which is the component this
change modifies (`recvBuffer.put` runs on that loop) - so a deadlock there shows up as a whole-package `go test -timeout` panic rather
than this test's own timeout message. The added benchmark has no bound at all (c).

### C2 on evalon/grpc-go-tr-8ac37769

Added test `TestReceiveBufferCompactionOwnership` (`internal/transport/recv_buffer_test.go:175-230`) builds its reader without any
context and reads five times plus two terminal reads:

```go
				var queue recvBuffer
				queue.init()
				r := recvBufferReader{recv: &queue}
				...
					b, err := r.Read(16384)
				...
				if _, err := r.Read(1); err != terminalErr {
```

`recvBufferReader.read` selects on `r.ctxDone` (nil here, so never ready) and `r.recv.get()`. The sibling test
`TestReceiveBufferCompactionReadWhileWriting` builds `recvBufferReader{recv: &queue, ctx: ctx, ctxDone: ctx.Done()}` with a 10 s context.
Induced stall: terminal message lost (`verify/repro/c2_lose_terminal_mutant.py`, `VERIFY_C2_LOSE_TERMINAL=1`).

```console
$ cd /tmp/claims/8ac37769 && python3 <repo>/verify/repro/c2_lose_terminal_mutant.py internal/transport/transport.go
$ time VERIFY_C2_LOSE_TERMINAL=1 go test ./internal/transport -run '^Test$/^ReceiveBufferCompactionOwnership$' -count=1 -v -timeout 40s
=== RUN   Test/ReceiveBufferCompactionOwnership/enabled_true/EOF
panic: test timed out after 40s
	running tests:
		Test (40s)
		Test/ReceiveBufferCompactionOwnership (40s)
		Test/ReceiveBufferCompactionOwnership/enabled_true/EOF (40s)
...
goroutine 23 [select]:
google.golang.org/grpc/internal/transport.(*recvBufferReader).read(0xc000080de8, 0x1)
	/tmp/claims/8ac37769/internal/transport/transport.go:247 +0x85
google.golang.org/grpc/internal/transport.(*recvBufferReader).Read(0xc000080de8, 0x0?)
	/tmp/claims/8ac37769/internal/transport/transport.go:232 +0x67
google.golang.org/grpc/internal/transport.s.TestReceiveBufferCompactionOwnership.func1(0xc0001036c0)
	/tmp/claims/8ac37769/internal/transport/recv_buffer_test.go:218 +0x885
real	0m42.316s
$ time VERIFY_C2_LOSE_TERMINAL=1 go test ./internal/transport -run '^Test$/^ReceiveBufferCompactionReadWhileWriting$' -count=1 -v -timeout 40s
    recv_buffer_test.go:301: Read() = rpc error: code = DeadlineExceeded desc = context deadline exceeded, want EOF
--- FAIL: Test (10.05s)
real	0m10.410s
$ git checkout -- .
```

Impact: if compaction ever withholds or loses a queued entry or the terminal entry (exactly the class of bug the ownership test exists
to catch), the ownership test does not report it; it blocks in `r.Read` until the global timeout kills the package binary, whereas the
sibling test with a timed context reports `DeadlineExceeded` after 10 s. With a correct buffer every read is satisfied by a prior put, so
the hang needs a regression to appear.

## C3

Claim: receive-buffer compaction loses pool-return ownership of capacity-1024 destinations acquired from the transport's configured pool.
Target branch evalon/grpc-go-tr-d0520117 (48827689).

Code under test (`internal/transport/transport.go:223-232`, `mem/buffers.go`):

```go
func (b *recvBuffer) newChunk(minSize int) {
	handle := b.pool.Get(max(b.nextChunkSize, minSize))
	*handle = (*handle)[:cap(*handle)]
	b.chunkRoot = mem.NewBuffer(handle, b.pool)
```

```go
func NewBuffer(data *[]byte, pool BufferPool) Buffer {
	if pool == nil || IsBelowBufferPoolingThreshold(cap(*data)) {
		return (SliceBuffer)(*data)
	}
```

`IsBelowBufferPoolingThreshold(size)` is `size <= 1<<10`. Repro tests wrap the configured pool with allocation-identity tracking
(pointer of every `Get`, whether the same pointer is later `Put`, and whether the `Get` came from `recvBuffer.newChunk` via
`runtime.Callers`), push one-byte frames, read everything, free everything, and close the transport.

```console
$ verify/repro/setup_worktrees.sh
$ cp verify/repro/c3_c4_chunk_pool_return_test.go verify/repro/c3_public_api_e2e_test.go /tmp/claims/d0520117/internal/transport/
$ (cd /tmp/claims/d0520117 && go test ./internal/transport -run '^TestVerifyC3|^TestVerifyC4' -count=1 -v)
=== RUN   TestVerifyC3_NewBufferDropsPoolReturnAtCap1024
    c3_c4_chunk_pool_return_test.go:133: binary tiered pool with 2^10 tier: Get(1024) -> cap=1024; mem.NewBuffer -> mem.SliceBuffer; after Free returnedToPool=false
    c3_c4_chunk_pool_return_test.go:133: binary tiered pool with 2^10 tier: Get(1025) -> cap=4096; mem.NewBuffer -> *mem.buffer; after Free returnedToPool=true
    c3_c4_chunk_pool_return_test.go:133: mem.NewTieredBufferPool(256,1024,4096): Get(1024) -> cap=1024; mem.NewBuffer -> mem.SliceBuffer; after Free returnedToPool=false
    c3_c4_chunk_pool_return_test.go:133: mem.NewTieredBufferPool(256,1024,4096): Get(1025) -> cap=4096; mem.NewBuffer -> *mem.buffer; after Free returnedToPool=true
=== RUN   TestVerifyC3_CompactionChunkFromTieredPoolNeverReturned
    c3_c4_chunk_pool_return_test.go:187: configured pool with 1 KiB tier (NewBinaryTieredBufferPool(8,10,12,14,15,20))
    c3_c4_chunk_pool_return_test.go:191:   burst=100: newChunk acquisition #0: Get(1024) -> cap=1024 returnedToPool=false
    c3_c4_chunk_pool_return_test.go:187: control: mem.DefaultBufferPool()
    c3_c4_chunk_pool_return_test.go:191:   burst=100: newChunk acquisition #0: Get(1024) -> cap=4096 returnedToPool=true
=== RUN   TestVerifyC3_EndToEndServerTransport/configured_pool_with_1_KiB_tier
    c3_c4_chunk_pool_return_test.go:303: server stream backlog entries before reading: 2 (for 600 one-byte DATA frames)
    c3_c4_chunk_pool_return_test.go:307:   server stream: newChunk acquisition #0: Get(1024) -> cap=1024 returnedToPool=false
=== RUN   TestVerifyC3_EndToEndServerTransport/control:_mem.DefaultBufferPool()
    c3_c4_chunk_pool_return_test.go:307:   server stream: newChunk acquisition #0: Get(1024) -> cap=4096 returnedToPool=true
=== RUN   TestVerifyC3Public_GRPCServerWithConfiguredPool/mem.NewBinaryTieredBufferPool(8,10,12,14,15,20)
    c3_public_api_e2e_test.go:210: newChunk acquisition #0: Get(1024) -> cap=1024 returnedToPool=false (after message delivered, RPC finished, server stopped)
=== RUN   TestVerifyC3Public_GRPCServerWithConfiguredPool/mem.NewTieredBufferPool(256,1024,4096,16384,32768,1048576)
    c3_public_api_e2e_test.go:210: newChunk acquisition #0: Get(1024) -> cap=1024 returnedToPool=false (after message delivered, RPC finished, server stopped)
=== RUN   TestVerifyC3Public_GRPCServerWithConfiguredPool/control:_mem.DefaultBufferPool()
    c3_public_api_e2e_test.go:210: newChunk acquisition #0: Get(1024) -> cap=4096 returnedToPool=true (after message delivered, RPC finished, server stopped)
PASS
ok  	google.golang.org/grpc/internal/transport	0.616s
$ rm /tmp/claims/d0520117/internal/transport/c3_*_test.go
```

Part *Pool-return ownership*: held. A capacity-1024 allocation from the configured pool wrapped by `mem.NewBuffer` is a `mem.SliceBuffer`
and `Free` does not `Put` it; a capacity-4096 one is a `*mem.buffer` and is `Put`.

Part *Compaction exposure*: held. The live path (`recvBuffer.newChunk`, reached from a real HTTP/2 server transport and from
`grpc.NewServer(experimental.BufferPool(pool))`) requests `Get(1024)`, receives capacity 1024 from a pool with a 1 KiB tier, and that
allocation is never returned after the message is delivered, the RPC finished and the server stopped.

The eval fixture passes on this branch (`go test ./internal/transport -run '^TestEval_' -count=1 -v` with the archive copy in place:
six `--- PASS`, `ok`), i.e. it does not detect this.

Impact: only pools that hand back exactly 1024 bytes of capacity for `Get(1024)` are affected - a tiered pool with a 1 KiB tier
(`mem.NewTieredBufferPool(..., 1024, ...)`, `mem.NewBinaryTieredBufferPool` with exponent 10) configured through
`experimental.BufferPool` / `ServerConfig.BufferPool` / `ConnectOptions.BufferPool`. With `mem.DefaultBufferPool()` the same `Get(1024)`
returns capacity 4096 and the chunk is returned, so default configurations are unaffected. On an affected pool, the first compaction chunk
of a stream that queues small frames behind an unread one is taken from the pool and never put back (observed once per stream/burst in every probe): the memory is reclaimed by the GC (the
data is delivered correctly), but that buffer is not recycled into the 1 KiB tier, and any pool that accounts
for outstanding buffers sees a permanent imbalance. Workaround: a pool without a tier of exactly 1024 bytes.

## C4

Claim: receive-buffer compaction leaves acquired destinations unreturned after consumption when a configured exact-capacity pool supplies
storage at or below the 1024-byte small-buffer cutoff. Target branch evalon/grpc-go-tr-d0520117 (48827689). (The names
`compactBacklogLocked` and `outstandingPooled` from the claim do not exist on this branch; the equivalents are `recvBuffer.put` /
`recvBuffer.newChunk` and the identity-tracking pool in the repro.)

The exact-capacity pool is `mem.NopBufferPool` (returns `make([]byte, n)`, capacity == request) wrapped with allocation-identity tracking;
the probe queues N one-byte payloads without reading, then reads and frees everything.

```console
$ verify/repro/setup_worktrees.sh
$ cp verify/repro/c3_c4_chunk_pool_return_test.go verify/repro/c3_public_api_e2e_test.go /tmp/claims/d0520117/internal/transport/
$ (cd /tmp/claims/d0520117 && go test ./internal/transport -run '^TestVerifyC3|^TestVerifyC4' -count=1 -v)
=== RUN   TestVerifyC3_NewBufferDropsPoolReturnAtCap1024
    c3_c4_chunk_pool_return_test.go:133: exact-capacity mem.NopBufferPool: Get(512) -> cap=512; mem.NewBuffer -> mem.SliceBuffer; after Free returnedToPool=false
    c3_c4_chunk_pool_return_test.go:133: exact-capacity mem.NopBufferPool: Get(1024) -> cap=1024; mem.NewBuffer -> mem.SliceBuffer; after Free returnedToPool=false
    c3_c4_chunk_pool_return_test.go:133: exact-capacity mem.NopBufferPool: Get(1025) -> cap=1025; mem.NewBuffer -> *mem.buffer; after Free returnedToPool=true
    c3_c4_chunk_pool_return_test.go:133: exact-capacity mem.NopBufferPool: Get(2048) -> cap=2048; mem.NewBuffer -> *mem.buffer; after Free returnedToPool=true
=== RUN   TestVerifyC4_ExactCapacityPoolBursts
    c3_c4_chunk_pool_return_test.go:213:   queued=1: newChunk acquisition #0: Get(1024) -> cap=1024 returnedToPool=false
    c3_c4_chunk_pool_return_test.go:214: queued=1: chunks=1 unreturned(cap<=1024)=1 unreturned(cap>1024)=0 returned(cap>1024)=0
    c3_c4_chunk_pool_return_test.go:213:   queued=2: newChunk acquisition #0: Get(1024) -> cap=1024 returnedToPool=false
    c3_c4_chunk_pool_return_test.go:214: queued=2: chunks=1 unreturned(cap<=1024)=1 unreturned(cap>1024)=0 returned(cap>1024)=0
    c3_c4_chunk_pool_return_test.go:213:   queued=9: newChunk acquisition #0: Get(1024) -> cap=1024 returnedToPool=false
    c3_c4_chunk_pool_return_test.go:214: queued=9: chunks=1 unreturned(cap<=1024)=1 unreturned(cap>1024)=0 returned(cap>1024)=0
    c3_c4_chunk_pool_return_test.go:213:   queued=1025: newChunk acquisition #0: Get(1024) -> cap=1024 returnedToPool=false
    c3_c4_chunk_pool_return_test.go:213:   queued=1025: newChunk acquisition #1: Get(2048) -> cap=2048 returnedToPool=true
    c3_c4_chunk_pool_return_test.go:214: queued=1025: chunks=2 unreturned(cap<=1024)=1 unreturned(cap>1024)=0 returned(cap>1024)=1
    c3_c4_chunk_pool_return_test.go:213:   queued=3999: newChunk acquisition #0: Get(1024) -> cap=1024 returnedToPool=false
    c3_c4_chunk_pool_return_test.go:213:   queued=3999: newChunk acquisition #1: Get(2048) -> cap=2048 returnedToPool=true
    c3_c4_chunk_pool_return_test.go:213:   queued=3999: newChunk acquisition #2: Get(4096) -> cap=4096 returnedToPool=true
    c3_c4_chunk_pool_return_test.go:214: queued=3999: chunks=3 unreturned(cap<=1024)=1 unreturned(cap>1024)=0 returned(cap>1024)=2
PASS
ok  	google.golang.org/grpc/internal/transport	0.616s
$ rm /tmp/claims/d0520117/internal/transport/c3_*_test.go
```

("queued=N" counts payloads behind the first one, which is delivered to the channel uncompacted.)

Part *Small-buffer release mechanism*: held. For capacities 512 and 1024 `mem.NewBuffer` yields a `SliceBuffer` whose `Free` performs no
`Put`; for 1025 and 2048 it yields a `*mem.buffer` that is `Put`.

Part *Compaction exposure*: held. For short bursts (1, 2, 9 queued) and longer ones (1025, 3999 queued), the first compaction chunk is
`Get(1024)` -> capacity 1024 -> never returned after complete consumption and release; the larger chunks (2048, 4096) of the same bursts
are returned normally.

Impact: with an exact-capacity pool every probed burst - from a single queued byte to 3999 - left exactly one pool acquisition (its first,
1024-byte chunk) without a matching `Put`, so the imbalance tracks the number of compacting streams rather than the amount of data. The
payload is delivered correctly and the GC reclaims the memory; what breaks is the pool contract (Get/Put balance), which matters for pools
that recycle, cap or account for outstanding buffers. `mem.DefaultBufferPool()` rounds `Get(1024)` up to 4096 and is unaffected. Fix
location: `recvBuffer.newChunk` (either do not take chunks at or below the pooling threshold from the pool, as evalon/grpc-go-tr-6fe210bc
does with `mem.IsBelowBufferPoolingThreshold(size)`, or request more than 1024).

## C5

Claim: the `readAll` test helper replaces its unexpected-data diagnostic with a panic by querying a pooled buffer's length after releasing
it. Target branch evalon/grpc-go-tr-97f9441e (93d70bcf).

Helper as written (`internal/transport/recv_buffer_test.go`, the diagnostic is line 99):

```go
	data, err := s.read(1)
	if err == nil {
		data.Free()
		return got, fmt.Errorf("read %d unexpected bytes after %d bytes", data.Len(), n)
	}
```

`mem.BufferSlice.Len` sums `b.Len()`; `(*mem.buffer).Len` calls `ReadOnlyData()`, which panics with `Cannot read freed buffer` once the
last reference is gone. The repro feeds a stream one byte more than `readAll` is told to expect.

```console
$ verify/repro/setup_worktrees.sh
$ cp verify/repro/c5_readall_freed_len_test.go /tmp/claims/97f9441e/internal/transport/
$ (cd /tmp/claims/97f9441e && go test ./internal/transport -run '^TestVerifyC5' -count=1 -v)
=== RUN   TestVerifyC5_ReadAllUnexpectedData/surplus_byte_in_own_pooled_buffer
    c5_readall_freed_len_test.go:74: readAll returned len(got)=0 err=<nil> panic=Cannot read freed buffer
=== RUN   TestVerifyC5_ReadAllUnexpectedData/surplus_byte_is_tail_of_pooled_frame
    c5_readall_freed_len_test.go:74: readAll returned len(got)=0 err=<nil> panic=Cannot read freed buffer
=== RUN   TestVerifyC5_ReadAllUnexpectedData/control_surplus_byte_in_slice_buffer
    c5_readall_freed_len_test.go:74: readAll returned len(got)=0 err=read 1 unexpected bytes after 0 bytes panic=<nil>
=== RUN   TestVerifyC5_ReadAllUnexpectedData/control_no_surplus
    c5_readall_freed_len_test.go:74: readAll returned len(got)=2000 err=EOF panic=<nil>
ok  	google.golang.org/grpc/internal/transport	0.004s
$ (cd /tmp/claims/97f9441e && VERIFY_C5_UNCAUGHT=1 go test ./internal/transport -run '^TestVerifyC5_Uncaught|^TestVerifyC5_After' -count=1 -v)
=== RUN   TestVerifyC5_Uncaught
--- FAIL: TestVerifyC5_Uncaught (0.00s)
panic: Cannot read freed buffer [recovered, repanicked]
...
google.golang.org/grpc/mem.(*buffer).ReadOnlyData(...)
	/tmp/claims/97f9441e/mem/buffers.go:144
google.golang.org/grpc/mem.(*buffer).Len(0xc000051900?)
	/tmp/claims/97f9441e/mem/buffers.go:184 +0x32
google.golang.org/grpc/mem.BufferSlice.Len(...)
	/tmp/claims/97f9441e/mem/buffer_slice.go:56
google.golang.org/grpc/internal/transport.readAll(0xc0000e2900, 0x7cf, 0x3e8)
	/tmp/claims/97f9441e/internal/transport/recv_buffer_test.go:99 +0x1d3
FAIL	google.golang.org/grpc/internal/transport	0.006s
$ rm /tmp/claims/97f9441e/internal/transport/c5_readall_freed_len_test.go
```

(The recovered cases print `len(got)=0 err=<nil>` because the panic unwinds `readAll` before it returns; `TestVerifyC5_After`, selected in
the second run, never starts.)

Part *Diagnostic access order*: held - `data.Free()` precedes `data.Len()`.

Part *Failure-path trigger*: held - with the surplus byte backed by a pooled buffer (its own buffer, or the tail of a pooled frame) the
length query panics; with a `SliceBuffer`-backed surplus byte the intended `read 1 unexpected bytes after 0 bytes` diagnostic is produced.

Impact: test-only. `readAll`'s failure path is the one that fires when the receive buffer delivers extra bytes (duplicate/over-delivery
regressions). With pooled data (the branch's tests build pooled buffers with `mem.NewBuffer(buf, pool)`, `recv_buffer_test.go:80`, and one caller reads
a real client stream, line 370) the helper panics instead of returning its error, so the developer gets `panic: Cannot read freed buffer` with a stack in `mem`, the test binary
aborts and the remaining tests of the package do not run (observed: `TestVerifyC5_After` not run, package `FAIL`). The passing path is
unaffected. Fix: take `n := data.Len()` before `data.Free()`.

## C6

Claim: the `newChunk` comment promises logarithmic queue-entry growth even though queued payload beyond the `http2MaxFrameLen`
chunk-capacity cap requires linearly many queue entries. Target branch evalon/grpc-go-tr-6fe210bc (1133e3e1).

Comment and policy (`internal/transport/transport.go:189-197`; `recvBufferMaxChunkSize = http2MaxFrameLen` = 16384):

```go
// newChunk allocates a new compaction chunk with zero length and a capacity
// of at least min(n, recvBufferMaxChunkSize). The capacity grows with the
// number of bytes already queued, so that the memory held by a stream stays
// within a constant factor of the buffered payload bytes while the number of
// queue entries grows only logarithmically with them.
//
// Caller must hold b.mu.
func (b *recvBuffer) newChunk(n int) *[]byte {
	size := min(max(recvBufferMinChunkSize, b.backlogBytes, n), recvBufferMaxChunkSize)
```

The repro queues one-byte payloads without reading and prints `len(backlog)` at each power of two of queued bytes (with and without a
pool).

```console
$ verify/repro/setup_worktrees.sh
$ cp verify/repro/c6_queue_entry_growth_test.go /tmp/claims/6fe210bc/internal/transport/
$ (cd /tmp/claims/6fe210bc && go test ./internal/transport -run '^TestVerifyC6' -count=1 -v)
=== RUN   TestVerifyC6_QueueEntryGrowth
    c6_queue_entry_growth_test.go:26: pool=<nil> recvBufferMaxChunkSize=16384 (http2MaxFrameLen=16384)
    c6_queue_entry_growth_test.go:27:    queuedB  entries    log2(B)   B/maxChunk  delta-entries
    c6_queue_entry_growth_test.go:36:       1024        3         10            0              3
    c6_queue_entry_growth_test.go:36:       2048        4         11            0              1
    c6_queue_entry_growth_test.go:36:       4096        5         12            0              1
    c6_queue_entry_growth_test.go:36:       8192        6         13            0              1
    c6_queue_entry_growth_test.go:36:      16384        7         14            1              1
    c6_queue_entry_growth_test.go:36:      32768        8         15            2              1
    c6_queue_entry_growth_test.go:36:      65536       10         16            4              2
    c6_queue_entry_growth_test.go:36:     131072       14         17            8              4
    c6_queue_entry_growth_test.go:36:     262144       22         18           16              8
    c6_queue_entry_growth_test.go:36:     524288       38         19           32             16
    c6_queue_entry_growth_test.go:36:    1048576       70         20           64             32
    c6_queue_entry_growth_test.go:36:    2097152      134         21          128             64
    c6_queue_entry_growth_test.go:36:    4194304      262         22          256            128
    c6_queue_entry_growth_test.go:36:    8388608      518         23          512            256
    c6_queue_entry_growth_test.go:26: pool=*leakcheck.swappableBufferPool recvBufferMaxChunkSize=16384 (http2MaxFrameLen=16384)
    c6_queue_entry_growth_test.go:36:    8388608      517         23          512            256
--- PASS: TestVerifyC6_QueueEntryGrowth (0.67s)
$ rm /tmp/claims/6fe210bc/internal/transport/c6_queue_entry_growth_test.go
```

Up to the cap (16 KiB queued) each doubling of queued bytes adds one entry (logarithmic). Beyond it each doubling doubles the added
entries (1, 2, 4, ... 256): entries = queued/16384 + 6, i.e. linear, one entry per 16384 queued bytes. At 8 MiB queued there are 518
entries where a logarithmic bound would give about 24. The comment states the logarithmic property without restricting it to the region
below the cap.

Impact: documentation only - behavior, memory bound (constant factor of payload) and tests are unaffected, and 16 KiB per entry is still
a 16384x reduction over one entry per one-byte frame. A maintainer relying on the comment would under-estimate backlog length (and the
per-entry `recvMsg` slice growth) for streams with more than 16 KiB unread. Fix: reword the comment ("logarithmically up to
recvBufferMaxChunkSize queued bytes, then one entry per recvBufferMaxChunkSize bytes").

## C7

Claim: for a two-frame burst of one-byte payloads, enabled receive-buffer compaction retains a 1024-byte destination for the second frame
where disabled compaction retains only that frame's original one-byte backing storage. Target branch evalon/grpc-go-tr-5368e0cb (1e48c8df).

Code (`internal/transport/transport.go:121-127`, `recvBufferCompactionSize = 1024`, `recvBufferSmallFrameThreshold = 256`):

```go
	if envconfig.EnableReceiveBufferCompaction && r.err == nil && r.buffer != nil && r.buffer.Len() < recvBufferSmallFrameThreshold {
		if b.pending == nil || r.buffer.Len() > cap(*b.pending)-len(*b.pending) {
			b.pending = new(mem.SliceBuffer)
			*b.pending = make(mem.SliceBuffer, 0, recvBufferCompactionSize)
			b.backlog = append(b.backlog, recvMsg{buffer: b.pending})
		}
		*b.pending = append(*b.pending, r.buffer.ReadOnlyData()...)
```

The repro puts N separately allocated one-byte payloads with no read in between, under `envconfig.EnableReceiveBufferCompaction` true and
false, then inspects each backlog entry's backing array (type, len, cap, identity with the original frame's array) and measures retained
heap per stream over many streams.

```console
$ verify/repro/setup_worktrees.sh
$ cp verify/repro/c7_two_frame_burst_test.go /tmp/claims/5368e0cb/internal/transport/
$ (cd /tmp/claims/5368e0cb && go test ./internal/transport -run '^TestVerifyC7' -count=1 -v)
=== RUN   TestVerifyC7_BurstRetainedStorage
    c7_two_frame_burst_test.go:46: frames=2 compaction=true: len(b.c)=1 len(b.backlog)=1
    c7_two_frame_burst_test.go:57:   backlog[0]: type=*mem.SliceBuffer len=1 cap=1024 sameStorageAsOriginalFrame=-1
    c7_two_frame_burst_test.go:67:   total backing capacity retained by backlog = 1024 bytes for 1 queued payload byte(s)
    c7_two_frame_burst_test.go:46: frames=2 compaction=false: len(b.c)=1 len(b.backlog)=1
    c7_two_frame_burst_test.go:57:   backlog[0]: type=mem.SliceBuffer len=1 cap=1 sameStorageAsOriginalFrame=1
    c7_two_frame_burst_test.go:67:   total backing capacity retained by backlog = 1 bytes for 1 queued payload byte(s)
    c7_two_frame_burst_test.go:46: frames=4 compaction=true: len(b.c)=1 len(b.backlog)=1
    c7_two_frame_burst_test.go:57:   backlog[0]: type=*mem.SliceBuffer len=3 cap=1024 sameStorageAsOriginalFrame=-1
    c7_two_frame_burst_test.go:67:   total backing capacity retained by backlog = 1024 bytes for 3 queued payload byte(s)
    c7_two_frame_burst_test.go:46: frames=4 compaction=false: len(b.c)=1 len(b.backlog)=3
    c7_two_frame_burst_test.go:67:   total backing capacity retained by backlog = 3 bytes for 3 queued payload byte(s)
=== RUN   TestVerifyC7_HeapRetained
    c7_two_frame_burst_test.go:100: burst of 1 one-byte frames, unread: heap retained per stream: compaction enabled=25 B, disabled=25 B
    c7_two_frame_burst_test.go:100: burst of 2 one-byte frames, unread: heap retained per stream: compaction enabled=1105 B, disabled=82 B
    c7_two_frame_burst_test.go:100: burst of 4 one-byte frames, unread: heap retained per stream: compaction enabled=1107 B, disabled=228 B
    c7_two_frame_burst_test.go:100: burst of 8 one-byte frames, unread: heap retained per stream: compaction enabled=1111 B, disabled=456 B
    c7_two_frame_burst_test.go:100: burst of 16 one-byte frames, unread: heap retained per stream: compaction enabled=1119 B, disabled=912 B
    c7_two_frame_burst_test.go:100: burst of 32 one-byte frames, unread: heap retained per stream: compaction enabled=1119 B, disabled=1952 B
    c7_two_frame_burst_test.go:100: burst of 64 one-byte frames, unread: heap retained per stream: compaction enabled=1119 B, disabled=3904 B
ok  	google.golang.org/grpc/internal/transport	0.492s
$ rm /tmp/claims/5368e0cb/internal/transport/c7_two_frame_burst_test.go
```

`sameStorageAsOriginalFrame=1` means the backlog entry's backing array is the array of original frame #1 (the second frame);
`-1` means it is none of the original frames' arrays.

Enabled: the second frame is copied into a freshly made 1024-capacity `SliceBuffer` that the backlog retains (1024 bytes of backing for
one payload byte). Disabled: the backlog retains the second frame's own one-byte array. The eval fixture passes on this branch
(six `--- PASS`, `ok`), so it does not cover this regime.

Impact: for short unread bursts the feature increases retained memory instead of reducing it - measured 1105 B vs 82 B per stream for two
one-byte frames (about 13x), 1107 B vs 228 B for four, still worse at 16 (1119 B vs 912 B) and better only from somewhere between 17
and 32 frames (1119 B vs 1952 B at 32). Two to sixteen small unread frames on a stream is an ordinary situation (a small message split over
a few DATA frames, many mostly idle streams), and compaction is on by default, so a server with many such streams retains about 1 KiB per
stream that it did not before. Workaround: `GRPC_GO_EXPERIMENTAL_ENABLE_RECEIVE_BUFFER_COMPACTION=false` (loses the benefit for long
bursts). Fix direction: start compacting only once enough small entries are queued, or size the first destination to the data.
