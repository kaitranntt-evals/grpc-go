# Evidence for audit run v-fa6b21e4

Observations only; the verdict report is delivered separately. Every section is self-contained: it names the branch and commit it was run against, the exact commands, and the key output lines verbatim.

Conventions used in every section:

- Commands start from the root of a `grpc-go` checkout of this verify branch (`verify/grpc-go-transport-restrict-memory-overhead-v-fa6b21e4`), which is the audited branch [`grpc-go-transport-restrict-memory-overhead-perfect`](https://github.com/kaitranntt-evals/grpc-go/tree/grpc-go-transport-restrict-memory-overhead-perfect) at `327a6ff993d9866ea656ed166b89744aadcbc940` plus the `verify/` directory. No production file is modified on this branch.
- Claim-target branches live in a different repository and are fetched by URL into a throwaway worktree under `/tmp`. The probe files are copied into that worktree's `internal/transport` package (they need package-internal access); they add test files only.
- Toolchain: `go version go1.25.7 linux/amd64`, 8 CPUs.
- `verify/repro/go.mod` and `verify/instrumentation/go.mod` exist only to keep the probe files out of the root module's `./...` patterns; they are not used to build anything.
- `$V` is the absolute path of this checkout's `verify/` directory; each section sets it (`V=$(pwd)/verify`) before changing directory.
- `$FIXTURES` is the directory into which the supplied `eval_tests.zip` was extracted (`unzip eval_tests.zip -d $FIXTURES`); its `tests/eval_recv_buffer_compaction_test.go` has sha256 `eb9f51f263ce5736fb7b056718e49902a4281b55ebe24834b88c90327d792b24` and is not committed here.
- A probe that reproduces a suspected problem ends with a `PROBLEM REPRODUCED` line and a failing exit status; a probe whose observations contradict the claim passes.

## C1

**Claim:** receive-buffer compaction acquires destination buffers with capacities at or below 1,024 bytes from the transport's configured pool and leaves those acquisitions outstanding after the received data is consumed and freed.

**Branch:** [`evalon/grpc-go-tr-e7cea533`](https://github.com/kaitranntt-evals/grpc-go-transport-restrict-memory-overhead/tree/evalon/grpc-go-tr-e7cea533) at `fa2f1b25da94fdb7903cfdf0178fccc2ecbc3b2b`.

**Observed: the problem is real.** Probe: [`verify/repro/c1_c7_pool_leak_test.go`](repro/c1_c7_pool_leak_test.go). It installs a tracking `mem.BufferPool` that hands out exact-capacity buffers, records every `Get`/`Put` by pointer identity, and marks a `Get` as a compaction destination when the calling stack contains a `recvBuffer` method. `TestVerifyC1_SmallCompactionDestinationsNeverReturned` enqueues 2,000 one-byte frames without reading, then reads everything, frees every delivered buffer, and checks that the backlog and open chunk are empty before comparing `Get`s with `Put`s.

```sh
V=$(pwd)/verify
git fetch https://github.com/kaitranntt-evals/grpc-go-transport-restrict-memory-overhead evalon/grpc-go-tr-e7cea533
git worktree add --detach /tmp/wt-e7cea533 FETCH_HEAD
cp verify/repro/c1_c7_pool_leak_test.go /tmp/wt-e7cea533/internal/transport/zz_verify_c1_c7_test.go
cd /tmp/wt-e7cea533 && go test ./internal/transport -run 'TestVerifyC1|TestVerifyC7' -count=1 -v; echo "exit=$?"
```

```console
    zz_verify_c1_c7_test.go:178: after drain: backlog=0 chunk-nil=true
    zz_verify_c1_c7_test.go:183: destination cap=256    acquired=1    returned=0    outstanding=1    via transport.(*recvBuffer).compactLocked
    zz_verify_c1_c7_test.go:183: destination cap=512    acquired=1    returned=0    outstanding=1    via transport.(*recvBuffer).compactLocked
    zz_verify_c1_c7_test.go:183: destination cap=1024   acquired=1    returned=0    outstanding=1    via transport.(*recvBuffer).compactLocked
    zz_verify_c1_c7_test.go:183: destination cap=2048   acquired=1    returned=1    outstanding=0    via transport.(*recvBuffer).compactLocked
    zz_verify_c1_c7_test.go:185: C1 RESULT: compaction destinations acquired=4, outstanding(all caps)=3, outstanding(cap<=1024)=3
    zz_verify_c1_c7_test.go:187: PROBLEM REPRODUCED: 3 compaction destination(s) with cap<=1024 were acquired from the configured pool and never returned
--- FAIL: TestVerifyC1_SmallCompactionDestinationsNeverReturned (0.00s)
```

The same run drives the real HTTP/2 server transport (`TestVerifyC1C7_EndToEndServerTransport`: a raw HTTP/2 client sends 20 bursts of eight one-byte DATA frames to a server created with `NewServerTransport` and the tracking pool as `ServerConfig.BufferPool`; the handler reads each burst completely and the stream is closed):

```console
    zz_verify_c1_c7_test.go:346: destination cap=256    acquired=20   returned=0    outstanding=20   via transport.(*recvBuffer).compactLocked
    zz_verify_c1_c7_test.go:348: E2E RESULT: bursts=20 x 8 one-byte DATA frames; compaction destinations acquired=20 outstanding(all caps)=20 outstanding(cap<=1024)=20
    zz_verify_c1_c7_test.go:350: PROBLEM REPRODUCED end-to-end: 20 of 20 compaction destination acquisitions were never returned to the transport's configured pool
--- FAIL: TestVerifyC1C7_EndToEndServerTransport (3.07s)
FAIL
FAIL	google.golang.org/grpc/internal/transport	3.076s
FAIL
exit=1
```

Why (source on the target branch, read after observing the behaviour): `flushChunkLocked` wraps the chunk with `mem.NewBuffer(b.chunk, b.pool)`, and `mem.NewBuffer` returns a plain `SliceBuffer` — whose `Free` is a no-op — whenever `cap(*data)` is at or below the 1,024-byte pooling threshold. Chunk capacities start at 256 and double, so the 256-, 512- and 1,024-byte destinations are taken with `pool.Get` and can never be handed back; the 2,048-byte one is.

```console
$ cd /tmp/wt-e7cea533 && grep -n -A7 'func (b \*recvBuffer) flushChunkLocked' internal/transport/transport.go
195:func (b *recvBuffer) flushChunkLocked() {
196-	if b.chunk == nil {
197-		return
198-	}
199-	b.backlog = append(b.backlog, recvMsg{buffer: mem.NewBuffer(b.chunk, b.pool)})
200-	b.nextChunkSize = min(2*cap(*b.chunk), maxRecvCompactionChunkSize)
201-	b.chunk = nil
202-}
$ grep -n -A6 '^func NewBuffer' mem/buffers.go
107:func NewBuffer(data *[]byte, pool BufferPool) Buffer {
108-	// Use the buffer's capacity instead of the length, otherwise buffers may
109-	// not be reused under certain conditions. For example, if a large buffer
110-	// is acquired from the pool, but fewer bytes than the buffering threshold
111-	// are written to it, the buffer will not be returned to the pool.
112-	if pool == nil || IsBelowBufferPoolingThreshold(cap(*data)) {
113-		return (SliceBuffer)(*data)
114-	}
```

**Impact reasoning.** Each time a stream's backlog starts compacting it takes up to three buffers (256, 512, 1,024 bytes) from the configured pool that are never returned; this was observed on the bare receive buffer and through the real server transport. What was observed is the unmatched `Get`s; the data itself was delivered intact and in order. For a pool that recycles (the default tiered pool) this means the small tiers are drained by compaction and never refilled by it, so the pool gives no reuse for these buffers; for a user-supplied pool that accounts for or bounds outstanding buffers, the outstanding count grows by one to three per backlog episode and never comes back down. Heap growth with the default pool was not measured here. The trigger is ordinary: any stream that receives a handful of small DATA frames while its reader is behind. Setting `GRPC_GO_EXPERIMENTAL_ENABLE_RECEIVE_BUFFER_COMPACTION=false` avoids compaction altogether.

## C2

**Claim:** receiving a sustained backlog of tiny DATA frames in the solution's provided environment retains a separate queued buffer for each frame instead of consolidating the backlog.

**Branch:** [`evalon/grpc-go-tr-2d69d401`](https://github.com/kaitranntt-evals/grpc-go-transport-restrict-memory-overhead/tree/evalon/grpc-go-tr-2d69d401) at `9b446348c991ce405d1d9586245903a0fa7a7ed6`.

**Observed: the stated behaviour does not occur on the paths the solution wires up.** Probe: [`verify/instrumentation/c2_c3_c4_consolidation_test.go`](instrumentation/c2_c3_c4_consolidation_test.go). "Queued entries" below is `len(backlog)` plus the open compaction tail; `backing_cap` is the summed capacity of the storage holding the queued payload.

```sh
V=$(pwd)/verify
git fetch https://github.com/kaitranntt-evals/grpc-go-transport-restrict-memory-overhead evalon/grpc-go-tr-2d69d401
git worktree add --detach /tmp/wt-2d69d401 FETCH_HEAD
cp verify/instrumentation/c2_c3_c4_consolidation_test.go /tmp/wt-2d69d401/internal/transport/zz_verify_c2_c3_c4_test.go
cd /tmp/wt-2d69d401 && go test ./internal/transport -run 'TestVerifyC[234]' -count=1 -v; echo "exit=$?"
```

Unit level — 20,000 one-byte frames, nothing read, initialised the way the three transports initialise a stream (`initWithPool`), next to the same sequence through plain `init()`:

```console
    zz_verify_c2_c3_c4_test.go:116: C2 initWithPool(pool) [what the transports call]              frames=2      backlog_entries=1      open_tail=0 queued_payload=1      backing_cap=1
    zz_verify_c2_c3_c4_test.go:116: C2 initWithPool(pool) [what the transports call]              frames=16     backlog_entries=0      open_tail=1 queued_payload=15     backing_cap=512
    zz_verify_c2_c3_c4_test.go:116: C2 initWithPool(pool) [what the transports call]              frames=64     backlog_entries=0      open_tail=1 queued_payload=63     backing_cap=512
    zz_verify_c2_c3_c4_test.go:116: C2 initWithPool(pool) [what the transports call]              frames=512    backlog_entries=0      open_tail=1 queued_payload=511    backing_cap=512
    zz_verify_c2_c3_c4_test.go:116: C2 initWithPool(pool) [what the transports call]              frames=1024   backlog_entries=0      open_tail=1 queued_payload=1023   backing_cap=1024
    zz_verify_c2_c3_c4_test.go:116: C2 initWithPool(pool) [what the transports call]              frames=1026   backlog_entries=0      open_tail=1 queued_payload=1025   backing_cap=4096
    zz_verify_c2_c3_c4_test.go:116: C2 initWithPool(pool) [what the transports call]              frames=2048   backlog_entries=0      open_tail=1 queued_payload=2047   backing_cap=4096
    zz_verify_c2_c3_c4_test.go:116: C2 initWithPool(pool) [what the transports call]              frames=4096   backlog_entries=0      open_tail=1 queued_payload=4095   backing_cap=4096
    zz_verify_c2_c3_c4_test.go:116: C2 initWithPool(pool) [what the transports call]              frames=8192   backlog_entries=0      open_tail=1 queued_payload=8191   backing_cap=16384
    zz_verify_c2_c3_c4_test.go:116: C2 initWithPool(pool) [what the transports call]              frames=16384  backlog_entries=0      open_tail=1 queued_payload=16383  backing_cap=16384
    zz_verify_c2_c3_c4_test.go:116: C2 initWithPool(pool) [what the transports call]              frames=20000  backlog_entries=1      open_tail=1 queued_payload=19999  backing_cap=20480
    zz_verify_c2_c3_c4_test.go:116: C2 init() [what the eval fixture's helper falls through to]   frames=2      backlog_entries=1      open_tail=0 queued_payload=1      backing_cap=1
    zz_verify_c2_c3_c4_test.go:116: C2 init() [what the eval fixture's helper falls through to]   frames=16     backlog_entries=15     open_tail=0 queued_payload=15     backing_cap=15
    zz_verify_c2_c3_c4_test.go:116: C2 init() [what the eval fixture's helper falls through to]   frames=64     backlog_entries=63     open_tail=0 queued_payload=63     backing_cap=63
    zz_verify_c2_c3_c4_test.go:116: C2 init() [what the eval fixture's helper falls through to]   frames=512    backlog_entries=511    open_tail=0 queued_payload=511    backing_cap=511
    zz_verify_c2_c3_c4_test.go:116: C2 init() [what the eval fixture's helper falls through to]   frames=1024   backlog_entries=1023   open_tail=0 queued_payload=1023   backing_cap=1023
    zz_verify_c2_c3_c4_test.go:116: C2 init() [what the eval fixture's helper falls through to]   frames=1026   backlog_entries=1025   open_tail=0 queued_payload=1025   backing_cap=1025
    zz_verify_c2_c3_c4_test.go:116: C2 init() [what the eval fixture's helper falls through to]   frames=2048   backlog_entries=2047   open_tail=0 queued_payload=2047   backing_cap=2047
    zz_verify_c2_c3_c4_test.go:116: C2 init() [what the eval fixture's helper falls through to]   frames=4096   backlog_entries=4095   open_tail=0 queued_payload=4095   backing_cap=4095
    zz_verify_c2_c3_c4_test.go:116: C2 init() [what the eval fixture's helper falls through to]   frames=8192   backlog_entries=8191   open_tail=0 queued_payload=8191   backing_cap=8191
    zz_verify_c2_c3_c4_test.go:116: C2 init() [what the eval fixture's helper falls through to]   frames=16384  backlog_entries=16383  open_tail=0 queued_payload=16383  backing_cap=16383
    zz_verify_c2_c3_c4_test.go:116: C2 init() [what the eval fixture's helper falls through to]   frames=20000  backlog_entries=19999  open_tail=0 queued_payload=19999  backing_cap=19999
--- PASS: TestVerifyC2_Unit_SustainedTinyBacklog (0.01s)
```

End to end — a raw HTTP/2 client sends 30,000 one-byte DATA frames to a real `http2Server` (`NewServerTransport`) whose handler does not read; the stream's receive buffer is inspected at the listed frame counts, then everything is read back and compared:

```console
    zz_verify_c2_c3_c4_test.go:255: C2 e2e http2Server: DATA frames received=2      backlog_entries=1     open_tail=0 queued_payload=1      backing_cap=1
    zz_verify_c2_c3_c4_test.go:255: C2 e2e http2Server: DATA frames received=64     backlog_entries=0     open_tail=1 queued_payload=63     backing_cap=512
    zz_verify_c2_c3_c4_test.go:255: C2 e2e http2Server: DATA frames received=512    backlog_entries=0     open_tail=1 queued_payload=511    backing_cap=512
    zz_verify_c2_c3_c4_test.go:255: C2 e2e http2Server: DATA frames received=1024   backlog_entries=0     open_tail=1 queued_payload=1023   backing_cap=1024
    zz_verify_c2_c3_c4_test.go:255: C2 e2e http2Server: DATA frames received=1026   backlog_entries=0     open_tail=1 queued_payload=1025   backing_cap=4096
    zz_verify_c2_c3_c4_test.go:255: C2 e2e http2Server: DATA frames received=2048   backlog_entries=0     open_tail=1 queued_payload=2047   backing_cap=4096
    zz_verify_c2_c3_c4_test.go:255: C2 e2e http2Server: DATA frames received=4096   backlog_entries=0     open_tail=1 queued_payload=4095   backing_cap=4096
    zz_verify_c2_c3_c4_test.go:255: C2 e2e http2Server: DATA frames received=8192   backlog_entries=0     open_tail=1 queued_payload=8191   backing_cap=16384
    zz_verify_c2_c3_c4_test.go:255: C2 e2e http2Server: DATA frames received=16384  backlog_entries=0     open_tail=1 queued_payload=16383  backing_cap=16384
    zz_verify_c2_c3_c4_test.go:255: C2 e2e http2Server: DATA frames received=30000  backlog_entries=1     open_tail=1 queued_payload=29999  backing_cap=32768
    zz_verify_c2_c3_c4_test.go:264: C2 e2e: all 30000 bytes delivered in order
--- PASS: TestVerifyC2_E2E_SustainedTinyBacklog (0.76s)
```

From 16 frames up to 30,000 the queue is one or two entries (`backlog_entries` plus `open_tail`) and the backing capacity stays within the next pool size above the payload; neither grows with the frame count. Through `init()` the same sequence leaves one entry per frame.

Public-API probe — [`verify/instrumentation/grpc_e2e_heap/main.go`](instrumentation/grpc_e2e_heap/main.go) starts a stock `grpc.NewServer()` with a handler that never reads, sends 60,000 one-byte DATA frames over a real TCP connection, and reports live-heap growth after forced GCs:

```sh
cd /tmp/wt-2d69d401 && mkdir -p verifye2e && cp $V/instrumentation/grpc_e2e_heap/main.go verifye2e/main.go
go run ./verifye2e ; GRPC_GO_EXPERIMENTAL_ENABLE_RECEIVE_BUFFER_COMPACTION=false go run ./verifye2e
```

```console
GRPC_GO_EXPERIMENTAL_ENABLE_RECEIVE_BUFFER_COMPACTION="" frames=60000 payload_bytes=60000 live_heap_growth=77504 bytes (1.3 bytes/frame)
GRPC_GO_EXPERIMENTAL_ENABLE_RECEIVE_BUFFER_COMPACTION="false" frames=60000 payload_bytes=60000 live_heap_growth=3862368 bytes (64.4 bytes/frame)
```

**Why the eval fixture nevertheless fails here.** The archived eval fixture (`tests/eval_recv_buffer_compaction_test.go` from `eval_tests.zip`), copied byte-exact into `internal/transport/`, fails on this branch:

```console
$ cd /tmp/wt-2d69d401
$ cp $FIXTURES/tests/eval_recv_buffer_compaction_test.go internal/transport/ && sha256sum internal/transport/eval_recv_buffer_compaction_test.go
eb9f51f263ce5736fb7b056718e49902a4281b55ebe24834b88c90327d792b24  internal/transport/eval_recv_buffer_compaction_test.go
$ go test -v -run '^TestEval_' google.golang.org/grpc/internal/transport -race -count=1 2>&1 | grep -E '^(---|FAIL|ok|PASS)|eval_recv'
    eval_recv_buffer_compaction_test.go:85: Got backlog length 1025 after compaction, want <= 64
--- FAIL: TestEval_RecvBufferCompaction (0.00s)
    eval_recv_buffer_compaction_test.go:119: Got backlog length 1025, want <= 64 (compaction enabled)
--- FAIL: TestEval_RecvBufferCompactionDisabled (0.00s)
--- PASS: TestEval_RecvBufferCompactionSkippedLargeBuffer (0.01s)
--- PASS: TestEval_RecvBufferErrorResetSafety (0.00s)
    eval_recv_buffer_compaction_test.go:299: Got backlog length 1073 after mixed frame compaction, want <= 134
--- FAIL: TestEval_RecvBufferCompaction_MixedFrames (0.00s)
    eval_recv_buffer_compaction_test.go:339: Cycle 0: backlog length 1123 exceeded bound 512
--- FAIL: TestEval_RecvBufferCompaction_MultiCycleMemoryBound (0.00s)
FAIL
FAIL	google.golang.org/grpc/internal/transport	0.033s
FAIL
```

That failure measures something narrower than the claim. The fixture builds a bare `recvBuffer` through its own helper, which probes for `init(mem.BufferPool)`, `init(bool, mem.BufferPool)`, `init(mem.BufferPool, bool)`, `init(bool)` and finally `init()`. This branch keeps `init()` and adds `initWithPool(mem.BufferPool)`, so the helper lands on `init()`, which leaves the buffer without a pool and therefore without compaction. In non-test code `init()` is called only from inside `initWithPool`; all three transports initialise stream receive buffers with `initWithPool` (last command below).

With the single change of letting the helper call the initializer the transports call ([`verify/instrumentation/c2_c3_c4_fixture_initWithPool.diff`](instrumentation/c2_c3_c4_fixture_initWithPool.diff): three added lines, one changed, assertions untouched), every fixture test passes, including the disabled-by-environment one:

```console
$ cd /tmp/wt-2d69d401
$ patch -p1 < $V/instrumentation/c2_c3_c4_fixture_initWithPool.diff
patching file internal/transport/eval_recv_buffer_compaction_test.go
$ go test -v -run '^TestEval_' google.golang.org/grpc/internal/transport -race -count=1 2>&1 | grep -E '^(---|FAIL|ok|PASS)|eval_recv'
--- PASS: TestEval_RecvBufferCompaction (0.00s)
--- PASS: TestEval_RecvBufferCompactionDisabled (0.00s)
--- PASS: TestEval_RecvBufferCompactionSkippedLargeBuffer (0.01s)
--- PASS: TestEval_RecvBufferErrorResetSafety (0.00s)
--- PASS: TestEval_RecvBufferCompaction_MixedFrames (0.00s)
--- PASS: TestEval_RecvBufferCompaction_MultiCycleMemoryBound (0.00s)
PASS
ok  	google.golang.org/grpc/internal/transport	1.033s
$ GRPC_GO_EXPERIMENTAL_ENABLE_RECEIVE_BUFFER_COMPACTION=false go test -v -run '^TestEval_RecvBufferCompactionDisabled$' google.golang.org/grpc/internal/transport -race -count=1 2>&1 | grep -E '^(---|FAIL|ok|PASS)|eval_recv'
--- PASS: TestEval_RecvBufferCompactionDisabled (0.00s)
PASS
ok  	google.golang.org/grpc/internal/transport	1.015s
$ grep -rn 'initWithPool\|\.init()' --include='*.go' internal/transport | grep -v _test.go
internal/transport/handler_server.go:427:	s.Stream.buf.initWithPool(ht.bufferPool)
internal/transport/http2_client.go:503:	s.Stream.buf.initWithPool(t.bufferPool)
internal/transport/transport.go:114:// initWithPool is like init, but additionally enables compaction of small
internal/transport/transport.go:117:func (b *recvBuffer) initWithPool(pool mem.BufferPool) {
internal/transport/transport.go:118:	b.init()
internal/transport/http2_server.go:410:	s.Stream.buf.initWithPool(t.bufferPool)
```

So the per-frame backlog is real only for a `recvBuffer` built with the pool-less `init()`, which on this branch is reached that way from test code only; DATA frames received by the server transport — the path exercised end to end above — are consolidated, and the client and handler transports use the same initializer.

## C3

**Claim:** receiving mixed-size DATA frames in the solution's provided environment leaves the tiny-frame backlog unconsolidated.

**Branch:** [`evalon/grpc-go-tr-2d69d401`](https://github.com/kaitranntt-evals/grpc-go-transport-restrict-memory-overhead/tree/evalon/grpc-go-tr-2d69d401) at `9b446348c991ce405d1d9586245903a0fa7a7ed6`.

**Observed: the tiny-frame portion is consolidated despite the intervening larger frames.** Probe: [`verify/instrumentation/c2_c3_c4_consolidation_test.go`](instrumentation/c2_c3_c4_consolidation_test.go).

```sh
V=$(pwd)/verify
git fetch https://github.com/kaitranntt-evals/grpc-go-transport-restrict-memory-overhead evalon/grpc-go-tr-2d69d401
git worktree add --detach /tmp/wt-2d69d401 FETCH_HEAD
cp verify/instrumentation/c2_c3_c4_consolidation_test.go /tmp/wt-2d69d401/internal/transport/zz_verify_c2_c3_c4_test.go
cd /tmp/wt-2d69d401 && go test ./internal/transport -run 'TestVerifyC[234]' -count=1 -v; echo "exit=$?"
```

Unit level, initialised with `initWithPool` as the transports do. First line: the fixture's own mixed sequence (1,074 frames of 1–7 bytes). Then 5,000 tiny frames (1–7 bytes) with an 8 KiB frame after every 250th and a 16 KiB frame after every 1,000th (20 large frames in all), nothing read; the last lines classify every queued entry by buffer identity and compare the bytes read back:

```console
    zz_verify_c2_c3_c4_test.go:306: C3 fixture sequence (1074 frames of 1..7 bytes) via initWithPool: backlog_entries=0 open_tail=1 queued_payload=4289 backing_cap=16384 (fixture bound: <=134)
    zz_verify_c2_c3_c4_test.go:321: C3 mixed sequence via initWithPool: frames_put=252   backlog_entries=3    open_tail=0 queued_payload=9192    backing_cap=17414
    zz_verify_c2_c3_c4_test.go:321: C3 mixed sequence via initWithPool: frames_put=1005  backlog_entries=9    open_tail=0 queued_payload=44963   backing_cap=69639
    zz_verify_c2_c3_c4_test.go:321: C3 mixed sequence via initWithPool: frames_put=2510  backlog_entries=20   open_tail=0 queued_payload=108300  backing_cap=174080
    zz_verify_c2_c3_c4_test.go:321: C3 mixed sequence via initWithPool: frames_put=5020  backlog_entries=40   open_tail=0 queued_payload=224794  backing_cap=348160
    zz_verify_c2_c3_c4_test.go:338: C3 mixed sequence: sent tiny=5000 large=20 -> queued entries: large-intact=20 consolidated-chunks=20 un-consolidated-tiny=0
    zz_verify_c2_c3_c4_test.go:342: C3 mixed sequence: all 224795 bytes delivered in order
--- PASS: TestVerifyC3_Unit_MixedFrames (0.00s)
```

End to end — a raw HTTP/2 client sends 4,000 tiny DATA frames (1–7 bytes) with a 4,500-byte DATA frame after every 500th to a real `http2Server` whose handler does not read; afterwards everything is read back and compared:

```console
    zz_verify_c2_c3_c4_test.go:371: C3 e2e http2Server: tiny_frames=500   large_frames=1 backlog_entries=2    open_tail=0 queued_payload=6493   backing_cap=20480
    zz_verify_c2_c3_c4_test.go:371: C3 e2e http2Server: tiny_frames=1000  large_frames=2 backlog_entries=4    open_tail=0 queued_payload=12996  backing_cap=40960
    zz_verify_c2_c3_c4_test.go:371: C3 e2e http2Server: tiny_frames=1500  large_frames=3 backlog_entries=6    open_tail=0 queued_payload=19494  backing_cap=61440
    zz_verify_c2_c3_c4_test.go:371: C3 e2e http2Server: tiny_frames=2000  large_frames=4 backlog_entries=8    open_tail=0 queued_payload=25994  backing_cap=81920
    zz_verify_c2_c3_c4_test.go:371: C3 e2e http2Server: tiny_frames=2500  large_frames=5 backlog_entries=10   open_tail=0 queued_payload=32496  backing_cap=102400
    zz_verify_c2_c3_c4_test.go:371: C3 e2e http2Server: tiny_frames=3000  large_frames=6 backlog_entries=12   open_tail=0 queued_payload=38993  backing_cap=122880
    zz_verify_c2_c3_c4_test.go:371: C3 e2e http2Server: tiny_frames=3500  large_frames=7 backlog_entries=14   open_tail=0 queued_payload=45499  backing_cap=143360
    zz_verify_c2_c3_c4_test.go:371: C3 e2e http2Server: tiny_frames=4000  large_frames=8 backlog_entries=16   open_tail=0 queued_payload=51993  backing_cap=163840
    zz_verify_c2_c3_c4_test.go:381: C3 e2e: all 51994 bytes delivered in order
--- PASS: TestVerifyC3_E2E_MixedFrames (0.63s)
```

4,000 tiny frames plus 8 large frames occupy 16 queued entries (one consolidated chunk and one intact large buffer per round), and 5,000 tiny plus 20 large occupy 40 with zero tiny frames left unconsolidated.

**Why the eval fixture nevertheless fails here.** The archived eval fixture (`tests/eval_recv_buffer_compaction_test.go` from `eval_tests.zip`), copied byte-exact into `internal/transport/`, fails on this branch:

```console
$ cd /tmp/wt-2d69d401
$ cp $FIXTURES/tests/eval_recv_buffer_compaction_test.go internal/transport/ && sha256sum internal/transport/eval_recv_buffer_compaction_test.go
eb9f51f263ce5736fb7b056718e49902a4281b55ebe24834b88c90327d792b24  internal/transport/eval_recv_buffer_compaction_test.go
$ go test -v -run '^TestEval_' google.golang.org/grpc/internal/transport -race -count=1 2>&1 | grep -E '^(---|FAIL|ok|PASS)|eval_recv'
    eval_recv_buffer_compaction_test.go:85: Got backlog length 1025 after compaction, want <= 64
--- FAIL: TestEval_RecvBufferCompaction (0.00s)
    eval_recv_buffer_compaction_test.go:119: Got backlog length 1025, want <= 64 (compaction enabled)
--- FAIL: TestEval_RecvBufferCompactionDisabled (0.00s)
--- PASS: TestEval_RecvBufferCompactionSkippedLargeBuffer (0.01s)
--- PASS: TestEval_RecvBufferErrorResetSafety (0.00s)
    eval_recv_buffer_compaction_test.go:299: Got backlog length 1073 after mixed frame compaction, want <= 134
--- FAIL: TestEval_RecvBufferCompaction_MixedFrames (0.00s)
    eval_recv_buffer_compaction_test.go:339: Cycle 0: backlog length 1123 exceeded bound 512
--- FAIL: TestEval_RecvBufferCompaction_MultiCycleMemoryBound (0.00s)
FAIL
FAIL	google.golang.org/grpc/internal/transport	0.033s
FAIL
```

That failure measures something narrower than the claim. The fixture builds a bare `recvBuffer` through its own helper, which probes for `init(mem.BufferPool)`, `init(bool, mem.BufferPool)`, `init(mem.BufferPool, bool)`, `init(bool)` and finally `init()`. This branch keeps `init()` and adds `initWithPool(mem.BufferPool)`, so the helper lands on `init()`, which leaves the buffer without a pool and therefore without compaction. In non-test code `init()` is called only from inside `initWithPool`; all three transports initialise stream receive buffers with `initWithPool` (last command below).

With the single change of letting the helper call the initializer the transports call ([`verify/instrumentation/c2_c3_c4_fixture_initWithPool.diff`](instrumentation/c2_c3_c4_fixture_initWithPool.diff): three added lines, one changed, assertions untouched), every fixture test passes, including the disabled-by-environment one:

```console
$ cd /tmp/wt-2d69d401
$ patch -p1 < $V/instrumentation/c2_c3_c4_fixture_initWithPool.diff
patching file internal/transport/eval_recv_buffer_compaction_test.go
$ go test -v -run '^TestEval_' google.golang.org/grpc/internal/transport -race -count=1 2>&1 | grep -E '^(---|FAIL|ok|PASS)|eval_recv'
--- PASS: TestEval_RecvBufferCompaction (0.00s)
--- PASS: TestEval_RecvBufferCompactionDisabled (0.00s)
--- PASS: TestEval_RecvBufferCompactionSkippedLargeBuffer (0.01s)
--- PASS: TestEval_RecvBufferErrorResetSafety (0.00s)
--- PASS: TestEval_RecvBufferCompaction_MixedFrames (0.00s)
--- PASS: TestEval_RecvBufferCompaction_MultiCycleMemoryBound (0.00s)
PASS
ok  	google.golang.org/grpc/internal/transport	1.033s
$ GRPC_GO_EXPERIMENTAL_ENABLE_RECEIVE_BUFFER_COMPACTION=false go test -v -run '^TestEval_RecvBufferCompactionDisabled$' google.golang.org/grpc/internal/transport -race -count=1 2>&1 | grep -E '^(---|FAIL|ok|PASS)|eval_recv'
--- PASS: TestEval_RecvBufferCompactionDisabled (0.00s)
PASS
ok  	google.golang.org/grpc/internal/transport	1.015s
$ grep -rn 'initWithPool\|\.init()' --include='*.go' internal/transport | grep -v _test.go
internal/transport/handler_server.go:427:	s.Stream.buf.initWithPool(ht.bufferPool)
internal/transport/http2_client.go:503:	s.Stream.buf.initWithPool(t.bufferPool)
internal/transport/transport.go:114:// initWithPool is like init, but additionally enables compaction of small
internal/transport/transport.go:117:func (b *recvBuffer) initWithPool(pool mem.BufferPool) {
internal/transport/transport.go:118:	b.init()
internal/transport/http2_server.go:410:	s.Stream.buf.initWithPool(t.bufferPool)
```

The relevant fixture test is `TestEval_RecvBufferCompaction_MixedFrames` (`Got backlog length 1073 ... want <= 134` unmodified; `PASS` once the helper calls `initWithPool`).

## C4

**Claim:** repeated receive-and-read cycles of tiny DATA frames in the solution's provided environment fail to maintain a compacted, bounded receive backlog.

**Branch:** [`evalon/grpc-go-tr-2d69d401`](https://github.com/kaitranntt-evals/grpc-go-transport-restrict-memory-overhead/tree/evalon/grpc-go-tr-2d69d401) at `9b446348c991ce405d1d9586245903a0fa7a7ed6`.

**Observed: every cycle is consolidated at its peak and every pooled buffer is returned after each drain.** Probe: [`verify/instrumentation/c2_c3_c4_consolidation_test.go`](instrumentation/c2_c3_c4_consolidation_test.go). The pool in these tests counts `Get`s and `Put`s; "peak" is sampled after the cycle's burst with nothing read, "drain" after the burst has been read and freed.

```sh
V=$(pwd)/verify
git fetch https://github.com/kaitranntt-evals/grpc-go-transport-restrict-memory-overhead evalon/grpc-go-tr-2d69d401
git worktree add --detach /tmp/wt-2d69d401 FETCH_HEAD
cp verify/instrumentation/c2_c3_c4_consolidation_test.go /tmp/wt-2d69d401/internal/transport/zz_verify_c2_c3_c4_test.go
cd /tmp/wt-2d69d401 && go test ./internal/transport -run 'TestVerifyC[234]' -count=1 -v; echo "exit=$?"
```

Unit level, `initWithPool`, eight cycles of 1,124 frames of 1–5 bytes (the fixture's per-cycle frame count):

```console
    zz_verify_c2_c3_c4_test.go:403: C4 cycle 0 peak : frames=1124 backlog_entries=0 open_tail=1 queued_payload=3369 backing_cap=4096 pool_outstanding=1 (4096 bytes)
    zz_verify_c2_c3_c4_test.go:412: C4 cycle 0 drain: backlog_entries=0 open_tail=0 queued_payload=0 backing_cap=0 cap(backlog)=0 pool gets=2 puts=2 outstanding=0 (0 bytes)
    zz_verify_c2_c3_c4_test.go:403: C4 cycle 1 peak : frames=1124 backlog_entries=0 open_tail=1 queued_payload=3369 backing_cap=4096 pool_outstanding=1 (4096 bytes)
    zz_verify_c2_c3_c4_test.go:412: C4 cycle 1 drain: backlog_entries=0 open_tail=0 queued_payload=0 backing_cap=0 cap(backlog)=0 pool gets=4 puts=4 outstanding=0 (0 bytes)
    zz_verify_c2_c3_c4_test.go:403: C4 cycle 2 peak : frames=1124 backlog_entries=0 open_tail=1 queued_payload=3369 backing_cap=4096 pool_outstanding=1 (4096 bytes)
    zz_verify_c2_c3_c4_test.go:412: C4 cycle 2 drain: backlog_entries=0 open_tail=0 queued_payload=0 backing_cap=0 cap(backlog)=0 pool gets=6 puts=6 outstanding=0 (0 bytes)
    zz_verify_c2_c3_c4_test.go:403: C4 cycle 3 peak : frames=1124 backlog_entries=0 open_tail=1 queued_payload=3369 backing_cap=4096 pool_outstanding=1 (4096 bytes)
    zz_verify_c2_c3_c4_test.go:412: C4 cycle 3 drain: backlog_entries=0 open_tail=0 queued_payload=0 backing_cap=0 cap(backlog)=0 pool gets=8 puts=8 outstanding=0 (0 bytes)
    zz_verify_c2_c3_c4_test.go:403: C4 cycle 4 peak : frames=1124 backlog_entries=0 open_tail=1 queued_payload=3369 backing_cap=4096 pool_outstanding=1 (4096 bytes)
    zz_verify_c2_c3_c4_test.go:412: C4 cycle 4 drain: backlog_entries=0 open_tail=0 queued_payload=0 backing_cap=0 cap(backlog)=0 pool gets=10 puts=10 outstanding=0 (0 bytes)
    zz_verify_c2_c3_c4_test.go:403: C4 cycle 5 peak : frames=1124 backlog_entries=0 open_tail=1 queued_payload=3369 backing_cap=4096 pool_outstanding=1 (4096 bytes)
    zz_verify_c2_c3_c4_test.go:412: C4 cycle 5 drain: backlog_entries=0 open_tail=0 queued_payload=0 backing_cap=0 cap(backlog)=0 pool gets=12 puts=12 outstanding=0 (0 bytes)
    zz_verify_c2_c3_c4_test.go:403: C4 cycle 6 peak : frames=1124 backlog_entries=0 open_tail=1 queued_payload=3369 backing_cap=4096 pool_outstanding=1 (4096 bytes)
    zz_verify_c2_c3_c4_test.go:412: C4 cycle 6 drain: backlog_entries=0 open_tail=0 queued_payload=0 backing_cap=0 cap(backlog)=0 pool gets=14 puts=14 outstanding=0 (0 bytes)
    zz_verify_c2_c3_c4_test.go:403: C4 cycle 7 peak : frames=1124 backlog_entries=0 open_tail=1 queued_payload=3369 backing_cap=4096 pool_outstanding=1 (4096 bytes)
    zz_verify_c2_c3_c4_test.go:412: C4 cycle 7 drain: backlog_entries=0 open_tail=0 queued_payload=0 backing_cap=0 cap(backlog)=0 pool gets=16 puts=16 outstanding=0 (0 bytes)
--- PASS: TestVerifyC4_Unit_Cycles (0.00s)
```

End to end — six cycles of 3,000 DATA frames of 1–5 bytes sent by a raw HTTP/2 client to a real `http2Server`, each cycle read completely by the handler before the next begins:

```console
    zz_verify_c2_c3_c4_test.go:438: C4 e2e cycle 0 peak : DATA frames=3000 backlog_entries=0 open_tail=1 queued_payload=8999 backing_cap=16384 pool_outstanding=1 (16384 bytes)
    zz_verify_c2_c3_c4_test.go:449: C4 e2e cycle 0 drain: backlog_entries=0 open_tail=0 queued_payload=0 backing_cap=0 pool gets=4 puts=4 outstanding=0 (0 bytes)
    zz_verify_c2_c3_c4_test.go:438: C4 e2e cycle 1 peak : DATA frames=3000 backlog_entries=0 open_tail=1 queued_payload=8999 backing_cap=16384 pool_outstanding=1 (16384 bytes)
    zz_verify_c2_c3_c4_test.go:449: C4 e2e cycle 1 drain: backlog_entries=0 open_tail=0 queued_payload=0 backing_cap=0 pool gets=8 puts=8 outstanding=0 (0 bytes)
    zz_verify_c2_c3_c4_test.go:438: C4 e2e cycle 2 peak : DATA frames=3000 backlog_entries=0 open_tail=1 queued_payload=8999 backing_cap=16384 pool_outstanding=1 (16384 bytes)
    zz_verify_c2_c3_c4_test.go:449: C4 e2e cycle 2 drain: backlog_entries=0 open_tail=0 queued_payload=0 backing_cap=0 pool gets=12 puts=12 outstanding=0 (0 bytes)
    zz_verify_c2_c3_c4_test.go:438: C4 e2e cycle 3 peak : DATA frames=3000 backlog_entries=0 open_tail=1 queued_payload=8999 backing_cap=16384 pool_outstanding=1 (16384 bytes)
    zz_verify_c2_c3_c4_test.go:449: C4 e2e cycle 3 drain: backlog_entries=0 open_tail=0 queued_payload=0 backing_cap=0 pool gets=16 puts=16 outstanding=0 (0 bytes)
    zz_verify_c2_c3_c4_test.go:438: C4 e2e cycle 4 peak : DATA frames=3000 backlog_entries=0 open_tail=1 queued_payload=8999 backing_cap=16384 pool_outstanding=1 (16384 bytes)
    zz_verify_c2_c3_c4_test.go:449: C4 e2e cycle 4 drain: backlog_entries=0 open_tail=0 queued_payload=0 backing_cap=0 pool gets=20 puts=20 outstanding=0 (0 bytes)
    zz_verify_c2_c3_c4_test.go:438: C4 e2e cycle 5 peak : DATA frames=3000 backlog_entries=0 open_tail=1 queued_payload=8999 backing_cap=16384 pool_outstanding=1 (16384 bytes)
    zz_verify_c2_c3_c4_test.go:449: C4 e2e cycle 5 drain: backlog_entries=0 open_tail=0 queued_payload=0 backing_cap=0 pool gets=24 puts=24 outstanding=0 (0 bytes)
--- PASS: TestVerifyC4_E2E_Cycles (0.64s)
PASS
ok  	google.golang.org/grpc/internal/transport	2.047s
exit=0
```

At the equivalent point of every cycle the queue is a single open tail (4,096 bytes of backing for 3,369 payload bytes; 16,384 for 8,999 end to end), and after every drain `outstanding=0 (0 bytes)` with `gets == puts`; nothing accumulates across cycles.

**Why the eval fixture nevertheless fails here.** The archived eval fixture (`tests/eval_recv_buffer_compaction_test.go` from `eval_tests.zip`), copied byte-exact into `internal/transport/`, fails on this branch:

```console
$ cd /tmp/wt-2d69d401
$ cp $FIXTURES/tests/eval_recv_buffer_compaction_test.go internal/transport/ && sha256sum internal/transport/eval_recv_buffer_compaction_test.go
eb9f51f263ce5736fb7b056718e49902a4281b55ebe24834b88c90327d792b24  internal/transport/eval_recv_buffer_compaction_test.go
$ go test -v -run '^TestEval_' google.golang.org/grpc/internal/transport -race -count=1 2>&1 | grep -E '^(---|FAIL|ok|PASS)|eval_recv'
    eval_recv_buffer_compaction_test.go:85: Got backlog length 1025 after compaction, want <= 64
--- FAIL: TestEval_RecvBufferCompaction (0.00s)
    eval_recv_buffer_compaction_test.go:119: Got backlog length 1025, want <= 64 (compaction enabled)
--- FAIL: TestEval_RecvBufferCompactionDisabled (0.00s)
--- PASS: TestEval_RecvBufferCompactionSkippedLargeBuffer (0.01s)
--- PASS: TestEval_RecvBufferErrorResetSafety (0.00s)
    eval_recv_buffer_compaction_test.go:299: Got backlog length 1073 after mixed frame compaction, want <= 134
--- FAIL: TestEval_RecvBufferCompaction_MixedFrames (0.00s)
    eval_recv_buffer_compaction_test.go:339: Cycle 0: backlog length 1123 exceeded bound 512
--- FAIL: TestEval_RecvBufferCompaction_MultiCycleMemoryBound (0.00s)
FAIL
FAIL	google.golang.org/grpc/internal/transport	0.033s
FAIL
```

That failure measures something narrower than the claim. The fixture builds a bare `recvBuffer` through its own helper, which probes for `init(mem.BufferPool)`, `init(bool, mem.BufferPool)`, `init(mem.BufferPool, bool)`, `init(bool)` and finally `init()`. This branch keeps `init()` and adds `initWithPool(mem.BufferPool)`, so the helper lands on `init()`, which leaves the buffer without a pool and therefore without compaction. In non-test code `init()` is called only from inside `initWithPool`; all three transports initialise stream receive buffers with `initWithPool` (last command below).

With the single change of letting the helper call the initializer the transports call ([`verify/instrumentation/c2_c3_c4_fixture_initWithPool.diff`](instrumentation/c2_c3_c4_fixture_initWithPool.diff): three added lines, one changed, assertions untouched), every fixture test passes, including the disabled-by-environment one:

```console
$ cd /tmp/wt-2d69d401
$ patch -p1 < $V/instrumentation/c2_c3_c4_fixture_initWithPool.diff
patching file internal/transport/eval_recv_buffer_compaction_test.go
$ go test -v -run '^TestEval_' google.golang.org/grpc/internal/transport -race -count=1 2>&1 | grep -E '^(---|FAIL|ok|PASS)|eval_recv'
--- PASS: TestEval_RecvBufferCompaction (0.00s)
--- PASS: TestEval_RecvBufferCompactionDisabled (0.00s)
--- PASS: TestEval_RecvBufferCompactionSkippedLargeBuffer (0.01s)
--- PASS: TestEval_RecvBufferErrorResetSafety (0.00s)
--- PASS: TestEval_RecvBufferCompaction_MixedFrames (0.00s)
--- PASS: TestEval_RecvBufferCompaction_MultiCycleMemoryBound (0.00s)
PASS
ok  	google.golang.org/grpc/internal/transport	1.033s
$ GRPC_GO_EXPERIMENTAL_ENABLE_RECEIVE_BUFFER_COMPACTION=false go test -v -run '^TestEval_RecvBufferCompactionDisabled$' google.golang.org/grpc/internal/transport -race -count=1 2>&1 | grep -E '^(---|FAIL|ok|PASS)|eval_recv'
--- PASS: TestEval_RecvBufferCompactionDisabled (0.00s)
PASS
ok  	google.golang.org/grpc/internal/transport	1.015s
$ grep -rn 'initWithPool\|\.init()' --include='*.go' internal/transport | grep -v _test.go
internal/transport/handler_server.go:427:	s.Stream.buf.initWithPool(ht.bufferPool)
internal/transport/http2_client.go:503:	s.Stream.buf.initWithPool(t.bufferPool)
internal/transport/transport.go:114:// initWithPool is like init, but additionally enables compaction of small
internal/transport/transport.go:117:func (b *recvBuffer) initWithPool(pool mem.BufferPool) {
internal/transport/transport.go:118:	b.init()
internal/transport/http2_server.go:410:	s.Stream.buf.initWithPool(t.bufferPool)
```

The relevant fixture test is `TestEval_RecvBufferCompaction_MultiCycleMemoryBound` (`Cycle 0: backlog length 1123 exceeded bound 512` unmodified; `PASS` once the helper calls `initWithPool`).

## C5

**Claim:** alternating 1-byte and 2-KiB receive frames causes compaction to retain growing, nearly empty destination buffers, increasing live backing capacity substantially beyond the queued payload.

**Branch:** [`evalon/grpc-go-tr-ae5dffd3`](https://github.com/kaitranntt-evals/grpc-go-transport-restrict-memory-overhead/tree/evalon/grpc-go-tr-ae5dffd3) at `7969de31d6c559a0e02b32fa0728433ffc9f88ac`.

**Observed: the problem is real.** Probe: [`verify/repro/c5_alternating_frames_test.go`](repro/c5_alternating_frames_test.go). An exact-capacity tracking pool backs both the frames and the receive buffer. Thirty frames alternating 1 byte and 2,048 bytes are enqueued with nothing read; every queued entry is then listed with its payload length, its backing capacity and whether its storage is one of the original frame buffers or a buffer created by compaction. The same sequence is run with compaction disabled and enabled.

```sh
V=$(pwd)/verify
git fetch https://github.com/kaitranntt-evals/grpc-go-transport-restrict-memory-overhead evalon/grpc-go-tr-ae5dffd3
git worktree add --detach /tmp/wt-ae5dffd3 FETCH_HEAD
cp verify/repro/c5_alternating_frames_test.go /tmp/wt-ae5dffd3/internal/transport/zz_verify_c5_test.go
cd /tmp/wt-ae5dffd3 && go test ./internal/transport -run 'TestVerifyC5' -count=1 -v; echo "exit=$?"
```

Compaction disabled (the 29 `backlog[i]` lines between the first line and the summary are omitted; they alternate `payload=2048 backing_cap=2048` and `payload=1 backing_cap=1`, all `original frame N`):

```console
    zz_verify_c5_test.go:79: compaction=false: channel holds frame 0 (1 byte); backlog has 29 entries, open chunk=false
    zz_verify_c5_test.go:104: compaction=false SUMMARY: queued payload=30734 bytes; live backing capacity=30734 bytes (1.00x payload)
    zz_verify_c5_test.go:105: compaction=false SUMMARY: compaction destinations=0 holding 0 payload bytes in 0 bytes of capacity; original buffers hold 30734 payload bytes in 30734 bytes of capacity
    zz_verify_c5_test.go:106: compaction=false SUMMARY: exact-capacity pool: Get sizes=[2048 2048 2048 2048 2048 2048 2048 2048 2048 2048 2048 2048 2048 2048 2048]; live pool buffers=15 (30720 bytes)
```

Compaction enabled — every queued entry:

```console
    zz_verify_c5_test.go:79: compaction=true: channel holds frame 0 (1 byte); backlog has 29 entries, open chunk=false
    zz_verify_c5_test.go:93:   backlog[ 0] payload=2048  backing_cap=2048   original frame 1
    zz_verify_c5_test.go:93:   backlog[ 1] payload=1     backing_cap=256    DESTINATION (compaction-created)
    zz_verify_c5_test.go:93:   backlog[ 2] payload=2048  backing_cap=2048   original frame 3
    zz_verify_c5_test.go:93:   backlog[ 3] payload=1     backing_cap=512    DESTINATION (compaction-created)
    zz_verify_c5_test.go:93:   backlog[ 4] payload=2048  backing_cap=2048   original frame 5
    zz_verify_c5_test.go:93:   backlog[ 5] payload=1     backing_cap=1024   DESTINATION (compaction-created)
    zz_verify_c5_test.go:93:   backlog[ 6] payload=2048  backing_cap=2048   original frame 7
    zz_verify_c5_test.go:93:   backlog[ 7] payload=1     backing_cap=2048   DESTINATION (compaction-created)
    zz_verify_c5_test.go:93:   backlog[ 8] payload=2048  backing_cap=2048   original frame 9
    zz_verify_c5_test.go:93:   backlog[ 9] payload=1     backing_cap=4096   DESTINATION (compaction-created)
    zz_verify_c5_test.go:93:   backlog[10] payload=2048  backing_cap=2048   original frame 11
    zz_verify_c5_test.go:93:   backlog[11] payload=1     backing_cap=8192   DESTINATION (compaction-created)
    zz_verify_c5_test.go:93:   backlog[12] payload=2048  backing_cap=2048   original frame 13
    zz_verify_c5_test.go:93:   backlog[13] payload=1     backing_cap=16384  DESTINATION (compaction-created)
    zz_verify_c5_test.go:93:   backlog[14] payload=2048  backing_cap=2048   original frame 15
    zz_verify_c5_test.go:93:   backlog[15] payload=1     backing_cap=16384  DESTINATION (compaction-created)
    zz_verify_c5_test.go:93:   backlog[16] payload=2048  backing_cap=2048   original frame 17
    zz_verify_c5_test.go:93:   backlog[17] payload=1     backing_cap=16384  DESTINATION (compaction-created)
    zz_verify_c5_test.go:93:   backlog[18] payload=2048  backing_cap=2048   original frame 19
    zz_verify_c5_test.go:93:   backlog[19] payload=1     backing_cap=16384  DESTINATION (compaction-created)
    zz_verify_c5_test.go:93:   backlog[20] payload=2048  backing_cap=2048   original frame 21
    zz_verify_c5_test.go:93:   backlog[21] payload=1     backing_cap=16384  DESTINATION (compaction-created)
    zz_verify_c5_test.go:93:   backlog[22] payload=2048  backing_cap=2048   original frame 23
    zz_verify_c5_test.go:93:   backlog[23] payload=1     backing_cap=16384  DESTINATION (compaction-created)
    zz_verify_c5_test.go:93:   backlog[24] payload=2048  backing_cap=2048   original frame 25
    zz_verify_c5_test.go:93:   backlog[25] payload=1     backing_cap=16384  DESTINATION (compaction-created)
    zz_verify_c5_test.go:93:   backlog[26] payload=2048  backing_cap=2048   original frame 27
    zz_verify_c5_test.go:93:   backlog[27] payload=1     backing_cap=16384  DESTINATION (compaction-created)
    zz_verify_c5_test.go:93:   backlog[28] payload=2048  backing_cap=2048   original frame 29
    zz_verify_c5_test.go:104: compaction=true SUMMARY: queued payload=30734 bytes; live backing capacity=177920 bytes (5.79x payload)
    zz_verify_c5_test.go:105: compaction=true SUMMARY: compaction destinations=14 holding 14 payload bytes in 147200 bytes of capacity; original buffers hold 30720 payload bytes in 30720 bytes of capacity
    zz_verify_c5_test.go:106: compaction=true SUMMARY: exact-capacity pool: Get sizes=[2048 2048 2048 2048 2048 2048 4096 2048 8192 2048 16384 2048 16384 2048 16384 2048 16384 2048 16384 2048 16384 2048 16384 2048 16384 2048]; live pool buffers=26 (176128 bytes)
    zz_verify_c5_test.go:130: C5 RESULT: compaction disabled: capacity/payload = 30734/30734 = 1.00x; compaction enabled: 177920/30734 = 5.79x; enabled holds 147186 more bytes of backing capacity than disabled for the same queued payload
    zz_verify_c5_test.go:133: PROBLEM REPRODUCED: with compaction enabled the stream retains 177920 bytes of backing capacity for 30734 bytes of queued payload
--- FAIL: TestVerifyC5_AlternatingTinyAnd2KiBFrames (0.00s)
```

Each 2-KiB frame forces the open chunk to be flushed with a single byte in it, and each new chunk is twice the size of the previous one until it reaches 16,384 bytes: 256, 512, 1,024, 2,048, 4,096, 8,192, then eight chunks of 16,384, each holding one byte.

End to end — the same 30 DATA frames sent by a raw HTTP/2 client to a real `http2Server` (default buffer pool) whose handler does not read:

```console
    zz_verify_c5_test.go:241: e2e compaction=false: 29 backlog entries; queued payload=30734 bytes; live backing capacity=61454 bytes (2.00x); the 14 one-byte entries hold 14 bytes of capacity
    zz_verify_c5_test.go:241: e2e compaction=true: 29 backlog entries; queued payload=30734 bytes; live backing capacity=218880 bytes (7.12x); the 14 one-byte entries hold 157440 bytes of capacity
    zz_verify_c5_test.go:256: C5 E2E RESULT: same 30 DATA frames, same 30734 queued payload bytes: backing capacity 61454 bytes with compaction disabled vs 218880 bytes with compaction enabled (+157426)
    zz_verify_c5_test.go:258: PROBLEM REPRODUCED end-to-end: enabling compaction increases retained backing capacity from 61454 to 218880 bytes
--- FAIL: TestVerifyC5_EndToEndServerTransport (0.25s)
FAIL
FAIL	google.golang.org/grpc/internal/transport	0.250s
FAIL
exit=1
```

**Impact reasoning.** For the same 30,734 queued payload bytes, enabling compaction raised the storage pinned by the stream from 30,734 to 177,920 bytes with an exact-capacity pool (5.79x the payload) and from 61,454 to 218,880 bytes on the real server transport (+157,426 bytes). After the first six tiny frames every further 1-byte frame pins a 16,384-byte chunk until the reader catches up, so the overhead grows with the number of tiny frames — the per-frame growth this feature exists to remove, at a larger constant than the uncompacted path (one byte of backing per tiny frame with compaction disabled). The trigger is a sender that interleaves small and ordinary-sized DATA frames on a stream whose reader is behind; nothing unusual is required of the configuration, and the bytes delivered were correct. `GRPC_GO_EXPERIMENTAL_ENABLE_RECEIVE_BUFFER_COMPACTION=false` restores the uncompacted behaviour.

## C6

**Claim:** receive-buffer compaction copies homogeneous pooled 4-KiB DATA buffers into new destinations once a backlog develops instead of preserving their existing storage.

**Branch:** [`evalon/grpc-go-tr-8cebbc22`](https://github.com/kaitranntt-evals/grpc-go-transport-restrict-memory-overhead/tree/evalon/grpc-go-tr-8cebbc22) at `9afdb832484fd5f3a9d2e34284e4ec5e7e19f36f`.

**Observed: the problem is real.** Probe: [`verify/repro/c6_4kib_zero_copy_test.go`](repro/c6_4kib_zero_copy_test.go). A tracking pool numbers every buffer it hands out and logs each `Get`/`Put` with whether it was made from inside a `recvBuffer` method. Eight pooled 4,096-byte buffers are enqueued with nothing read; the queue is listed by buffer identity, then everything is read and freed. Run with compaction disabled and enabled.

```sh
V=$(pwd)/verify
git fetch https://github.com/kaitranntt-evals/grpc-go-transport-restrict-memory-overhead evalon/grpc-go-tr-8cebbc22
git worktree add --detach /tmp/wt-8cebbc22 FETCH_HEAD
cp verify/repro/c6_4kib_zero_copy_test.go /tmp/wt-8cebbc22/internal/transport/zz_verify_c6_test.go
cd /tmp/wt-8cebbc22 && go test ./internal/transport -run 'TestVerifyC6' -count=1 -v; echo "exit=$?"
```

Compaction disabled — queue contents, then the full pool event log:

```console
    zz_verify_c6_test.go:118: compaction=false: after enqueueing 8 pooled 4096-byte buffers with no reads: channel=1 msg, backlog=7 entries
    zz_verify_c6_test.go:126:   backlog[0] len=4096  cap=4096  buf#2  ORIGINAL source buffer
    zz_verify_c6_test.go:126:   backlog[1] len=4096  cap=4096  buf#3  ORIGINAL source buffer
    zz_verify_c6_test.go:126:   backlog[2] len=4096  cap=4096  buf#4  ORIGINAL source buffer
    zz_verify_c6_test.go:126:   backlog[3] len=4096  cap=4096  buf#5  ORIGINAL source buffer
    zz_verify_c6_test.go:126:   backlog[4] len=4096  cap=4096  buf#6  ORIGINAL source buffer
    zz_verify_c6_test.go:126:   backlog[5] len=4096  cap=4096  buf#7  ORIGINAL source buffer
    zz_verify_c6_test.go:126:   backlog[6] len=4096  cap=4096  buf#8  ORIGINAL source buffer
    zz_verify_c6_test.go:150:   pool: Get(4096) -> buf#1  [source DATA buffer]
    zz_verify_c6_test.go:150:   pool: Get(4096) -> buf#2  [source DATA buffer]
    zz_verify_c6_test.go:150:   pool: Get(4096) -> buf#3  [source DATA buffer]
    zz_verify_c6_test.go:150:   pool: Get(4096) -> buf#4  [source DATA buffer]
    zz_verify_c6_test.go:150:   pool: Get(4096) -> buf#5  [source DATA buffer]
    zz_verify_c6_test.go:150:   pool: Get(4096) -> buf#6  [source DATA buffer]
    zz_verify_c6_test.go:150:   pool: Get(4096) -> buf#7  [source DATA buffer]
    zz_verify_c6_test.go:150:   pool: Get(4096) -> buf#8  [source DATA buffer]
    zz_verify_c6_test.go:150:   pool: ---- all 8 frames enqueued; reader has consumed nothing yet ----
    zz_verify_c6_test.go:150:   pool: Put(buf#1)       by reader
    zz_verify_c6_test.go:150:   pool: Put(buf#2)       by reader
    zz_verify_c6_test.go:150:   pool: Put(buf#3)       by reader
    zz_verify_c6_test.go:150:   pool: Put(buf#4)       by reader
    zz_verify_c6_test.go:150:   pool: Put(buf#5)       by reader
    zz_verify_c6_test.go:150:   pool: Put(buf#6)       by reader
    zz_verify_c6_test.go:150:   pool: Put(buf#7)       by reader
    zz_verify_c6_test.go:150:   pool: Put(buf#8)       by reader
    zz_verify_c6_test.go:152: compaction=false RESULT: 8 of 8 original 4-KiB buffers reached the reader; recvBuffer acquired 0 destination buffers and released 0 source buffers itself
```

Compaction enabled — queue contents, then the full pool event log:

```console
    zz_verify_c6_test.go:118: compaction=true: after enqueueing 8 pooled 4096-byte buffers with no reads: channel=1 msg, backlog=3 entries
    zz_verify_c6_test.go:126:   backlog[0] len=4096  cap=4096  buf#2  ORIGINAL source buffer
    zz_verify_c6_test.go:126:   backlog[1] len=8192  cap=8192  buf#4  NEW compaction chunk (payload was copied)
    zz_verify_c6_test.go:126:   backlog[2] len=16384 cap=16384 buf#7  NEW compaction chunk (payload was copied)
    zz_verify_c6_test.go:150:   pool: Get(4096) -> buf#1  [source DATA buffer]
    zz_verify_c6_test.go:150:   pool: Get(4096) -> buf#2  [source DATA buffer]
    zz_verify_c6_test.go:150:   pool: Get(4096) -> buf#3  [source DATA buffer]
    zz_verify_c6_test.go:150:   pool: Get(8192) -> buf#4  by recvBuffer.openChunk  [compaction DESTINATION]
    zz_verify_c6_test.go:150:   pool: Put(buf#3)       by recvBuffer.compact  [source released inside put(), before any read]
    zz_verify_c6_test.go:150:   pool: Get(4096) -> buf#5  [source DATA buffer]
    zz_verify_c6_test.go:150:   pool: Put(buf#5)       by recvBuffer.compact  [source released inside put(), before any read]
    zz_verify_c6_test.go:150:   pool: Get(4096) -> buf#6  [source DATA buffer]
    zz_verify_c6_test.go:150:   pool: Get(16384) -> buf#7  by recvBuffer.openChunk  [compaction DESTINATION]
    zz_verify_c6_test.go:150:   pool: Put(buf#6)       by recvBuffer.compact  [source released inside put(), before any read]
    zz_verify_c6_test.go:150:   pool: Get(4096) -> buf#8  [source DATA buffer]
    zz_verify_c6_test.go:150:   pool: Put(buf#8)       by recvBuffer.compact  [source released inside put(), before any read]
    zz_verify_c6_test.go:150:   pool: Get(4096) -> buf#9  [source DATA buffer]
    zz_verify_c6_test.go:150:   pool: Put(buf#9)       by recvBuffer.compact  [source released inside put(), before any read]
    zz_verify_c6_test.go:150:   pool: Get(4096) -> buf#10  [source DATA buffer]
    zz_verify_c6_test.go:150:   pool: Put(buf#10)       by recvBuffer.compact  [source released inside put(), before any read]
    zz_verify_c6_test.go:150:   pool: ---- all 8 frames enqueued; reader has consumed nothing yet ----
    zz_verify_c6_test.go:150:   pool: Put(buf#1)       by reader
    zz_verify_c6_test.go:150:   pool: Put(buf#2)       by reader
    zz_verify_c6_test.go:150:   pool: Put(buf#4)       by reader
    zz_verify_c6_test.go:150:   pool: Put(buf#7)       by reader
    zz_verify_c6_test.go:152: compaction=true RESULT: 2 of 8 original 4-KiB buffers reached the reader; recvBuffer acquired 2 destination buffers and released 6 source buffers itself
    zz_verify_c6_test.go:159: C6 RESULT: originals delivered to reader: disabled=8/8 enabled=2/8; sources copied+released by recvBuffer before any read: disabled=0 enabled=6
    zz_verify_c6_test.go:161: PROBLEM REPRODUCED: with compaction enabled, 6 of 8 homogeneous pooled 4-KiB buffers were copied into new chunks and released before the reader consumed anything
--- FAIL: TestVerifyC6_Homogeneous4KiBBuffers (0.00s)
```

End to end — a raw HTTP/2 client sends twelve 4,096-byte DATA frames to a real `http2Server` (tracking pool as `ServerConfig.BufferPool`) whose handler does not read; the queue is classified by buffer identity before anything is read, then the payload is read back and compared:

```console
    zz_verify_c6_test.go:267: e2e compaction=false: 12 DATA frames of 4096 bytes received, none read yet: backlog (kind:len/cap)=[orig:4096/4096 orig:4096/4096 orig:4096/4096 orig:4096/4096 orig:4096/4096 orig:4096/4096 orig:4096/4096 orig:4096/4096 orig:4096/4096 orig:4096/4096 orig:4096/4096]; recvBuffer destination Gets=0; original frame buffers still queued=12; frames copied into chunks=0; 4-KiB source buffers already returned to the pool before any read=0
    zz_verify_c6_test.go:267: e2e compaction=true: 12 DATA frames of 4096 bytes received, none read yet: backlog (kind:len/cap)=[orig:4096/4096 chunk:8192/8192 chunk:16384/16384 chunk:16384/16384]; recvBuffer destination Gets=3; original frame buffers still queued=2; frames copied into chunks=10; 4-KiB source buffers already returned to the pool before any read=10
    zz_verify_c6_test.go:283: C6 E2E RESULT: 4-KiB DATA frames copied+released before the application read: compaction disabled=0, enabled=10 (of 12)
    zz_verify_c6_test.go:285: PROBLEM REPRODUCED end-to-end: 10 of 12 ordinary 4-KiB DATA frames lost their zero-copy path
--- FAIL: TestVerifyC6_EndToEndServerTransport (0.25s)
FAIL
FAIL	google.golang.org/grpc/internal/transport	0.251s
FAIL
exit=1
```

The compaction threshold on this branch explains the reach (read after observing the behaviour): everything smaller than half the maximum frame size (8,192 bytes) is eligible once a backlog or open chunk exists.

```console
$ cd /tmp/wt-8cebbc22 && grep -n 'recvBufferCompactionThreshold = ' internal/transport/transport.go && grep -n -A9 'func (b \*recvBuffer) compactable' internal/transport/transport.go
113:	recvBufferCompactionThreshold = http2MaxFrameLen / 2
171:func (b *recvBuffer) compactable(buf mem.Buffer) bool {
172-	if buf == nil {
173-		return false
174-	}
175-	n := buf.Len()
176-	if n == 0 || n >= recvBufferCompactionThreshold {
177-		return false
178-	}
179-	return b.chunk != nil || len(b.backlog) > 0
180-}
```

**Impact reasoning.** With compaction disabled all eight (unit) and all twelve (end to end) pooled 4-KiB frame buffers reach the reader untouched. With compaction enabled — the default — six of eight and ten of twelve were copied into newly acquired 8- and 16-KiB chunks and their original buffers returned to the pool before the reader consumed anything. Nothing about this traffic is tiny: 4 KiB frames carry no per-frame overhead worth removing, so the copy buys no memory and costs one extra memcpy of every backlogged byte plus additional pool traffic. It applies to any stream whose reader is momentarily behind while receiving frames under 8 KiB, which is ordinary traffic; the stated requirement to keep the zero-copy path for ordinary-sized traffic is not met on this branch. Payload bytes and ordering were correct in every run, so nothing fails visibly. `GRPC_GO_EXPERIMENTAL_ENABLE_RECEIVE_BUFFER_COMPACTION=false` restores zero-copy delivery.

## C7

**Claim:** compaction destination buffers borrowed from the configured pool remain outstanding after short receive bursts are fully consumed and freed.

**Branch:** [`evalon/grpc-go-tr-e7cea533`](https://github.com/kaitranntt-evals/grpc-go-transport-restrict-memory-overhead/tree/evalon/grpc-go-tr-e7cea533) at `fa2f1b25da94fdb7903cfdf0178fccc2ecbc3b2b`.

**Observed: the problem is real.** Probe: [`verify/repro/c1_c7_pool_leak_test.go`](repro/c1_c7_pool_leak_test.go). It installs an exact-capacity tracking `mem.BufferPool` that records every `Get`/`Put` by pointer identity and marks a `Get` as a compaction destination when the calling stack contains a `recvBuffer` method, so destinations are counted separately from source buffers. `TestVerifyC7_ShortBurstDestinationsNeverReturned` runs 50 bursts of eight one-byte frames; each burst is read completely and every delivered buffer freed before the next burst, and the receive buffer is checked to be empty at the end.

```sh
V=$(pwd)/verify
git fetch https://github.com/kaitranntt-evals/grpc-go-transport-restrict-memory-overhead evalon/grpc-go-tr-e7cea533
git worktree add --detach /tmp/wt-e7cea533 FETCH_HEAD
cp verify/repro/c1_c7_pool_leak_test.go /tmp/wt-e7cea533/internal/transport/zz_verify_c1_c7_test.go
cd /tmp/wt-e7cea533 && go test ./internal/transport -run 'TestVerifyC1|TestVerifyC7' -count=1 -v; echo "exit=$?"
```

```console
    zz_verify_c1_c7_test.go:214: after 50 bursts: backlog=0 chunk-nil=true
    zz_verify_c1_c7_test.go:219: destination cap=256    acquired=50   returned=0    outstanding=50   via transport.(*recvBuffer).compactLocked
    zz_verify_c1_c7_test.go:221: C7 RESULT: bursts=50 compaction destinations acquired=50 outstanding(all caps)=50 outstanding(cap<=1024)=50
    zz_verify_c1_c7_test.go:223: PROBLEM REPRODUCED: 50 of 50 compaction destination acquisitions have no matching pool return
--- FAIL: TestVerifyC7_ShortBurstDestinationsNeverReturned (0.00s)
```

End to end in the same run (`TestVerifyC1C7_EndToEndServerTransport`: a raw HTTP/2 client sends 20 bursts of eight one-byte DATA frames to a server created with `NewServerTransport` and the tracking pool as `ServerConfig.BufferPool`; the handler reads each burst completely and the stream is closed):

```console
    zz_verify_c1_c7_test.go:346: destination cap=256    acquired=20   returned=0    outstanding=20   via transport.(*recvBuffer).compactLocked
    zz_verify_c1_c7_test.go:348: E2E RESULT: bursts=20 x 8 one-byte DATA frames; compaction destinations acquired=20 outstanding(all caps)=20 outstanding(cap<=1024)=20
    zz_verify_c1_c7_test.go:350: PROBLEM REPRODUCED end-to-end: 20 of 20 compaction destination acquisitions were never returned to the transport's configured pool
--- FAIL: TestVerifyC1C7_EndToEndServerTransport (3.07s)
FAIL
FAIL	google.golang.org/grpc/internal/transport	3.076s
FAIL
exit=1
```

Why (source on the target branch, read after observing the behaviour): a short burst never fills the first 256-byte chunk; draining resets the next chunk size to 256, so every burst takes another 256-byte buffer from the pool. `flushChunkLocked` wraps the chunk with `mem.NewBuffer(b.chunk, b.pool)`, and `mem.NewBuffer` returns a `SliceBuffer` with a no-op `Free` for capacities at or below the 1,024-byte pooling threshold, so that buffer is never `Put` back.

```console
$ cd /tmp/wt-e7cea533 && grep -n -A7 'func (b \*recvBuffer) flushChunkLocked' internal/transport/transport.go
195:func (b *recvBuffer) flushChunkLocked() {
196-	if b.chunk == nil {
197-		return
198-	}
199-	b.backlog = append(b.backlog, recvMsg{buffer: mem.NewBuffer(b.chunk, b.pool)})
200-	b.nextChunkSize = min(2*cap(*b.chunk), maxRecvCompactionChunkSize)
201-	b.chunk = nil
202-}
$ grep -n -A2 'if pool == nil || IsBelowBufferPoolingThreshold' mem/buffers.go
112:	if pool == nil || IsBelowBufferPoolingThreshold(cap(*data)) {
113-		return (SliceBuffer)(*data)
114-	}
```

**Impact reasoning.** One pool acquisition is lost per short burst, indefinitely: 50 of 50 at unit level and 20 of 20 through the real server transport, with all data delivered correctly. Short bursts of small frames on a stream whose reader lags briefly are the common shape of small-message streaming, so this is the everyday path rather than a corner. The configured pool therefore never gets these buffers back: a recycling pool gains nothing from them, and a pool that tracks or limits outstanding buffers sees its outstanding count rise by one per burst without bound. Heap growth with the default pool was not measured here. `GRPC_GO_EXPERIMENTAL_ENABLE_RECEIVE_BUFFER_COMPACTION=false` avoids compaction altogether.

## C8

**Claim:** the repository's receive-buffer compaction regression test accepts an execution whose measurements do not cover the buffering or compaction workload.

**Branch:** [`evalon/grpc-go-tr-c92846ef`](https://github.com/kaitranntt-evals/grpc-go-transport-restrict-memory-overhead/tree/evalon/grpc-go-tr-c92846ef) at `7e65080f01682f4dc5380c79e84b9dc595c31dab`.

**Observed: the problem is real for the transport-level test; the unit-level sibling test in the same file is sound.** The branch adds `internal/transport/recv_buffer_test.go`. The test in it that exercises real DATA-frame reception is `TestClientTransport_TinyDataFramesSlowReader`, built on `measureClientTinyDataFrames`.

What the test asserts on the measurements (this claim is about what the test checks, so the assertion is quoted; all commands in this section run in the fetched worktree):

```sh
V=$(pwd)/verify
git fetch https://github.com/kaitranntt-evals/grpc-go-transport-restrict-memory-overhead evalon/grpc-go-tr-c92846ef
git worktree add --detach /tmp/wt-c92846ef FETCH_HEAD
cd /tmp/wt-c92846ef
```

```console
$ sed -n '351,363p' internal/transport/recv_buffer_test.go
func (s) TestClientTransport_TinyDataFramesSlowReader(t *testing.T) {
	// The payload fits in the default stream flow control window, so the
	// server can send it all before the client reads anything.
	const payloadLen = 60000

	disabled := measureClientTinyDataFrames(t, false, payloadLen)
	enabled := measureClientTinyDataFrames(t, true, payloadLen)
	t.Logf("Heap growth for %d unread 1-byte DATA frames: compaction disabled = %d bytes, compaction enabled = %d bytes", payloadLen, disabled, enabled)

	if enabled*5 > disabled {
		t.Errorf("Heap growth with compaction enabled (%d bytes) is not at least 5x smaller than with compaction disabled (%d bytes)", enabled, disabled)
	}
}
```

How the measurement is ordered: the baseline heap sample is taken after `cs.Header()` returns, while the fake server writes the response headers and then all one-byte DATA frames back to back, so the client is already receiving DATA when the baseline call starts. `liveHeapBytes` runs two full GCs before reading the heap, which gives reception further time to complete inside the baseline call. Nothing holds the DATA back until after the baseline, and a negative difference is clamped to zero:

```console
$ sed -n '314,323p' internal/transport/recv_buffer_test.go
	if _, err := cs.Header(); err != nil {
		t.Fatalf("Header() failed: %v", err)
	}
	before := liveHeapBytes()
	select {
	case <-processed:
	case <-ctx.Done():
		t.Fatalf("Timed out waiting for client to process all DATA frames")
	}
	growth := heapGrowth(before, liveHeapBytes())
$ sed -n '43,56p' internal/transport/recv_buffer_test.go
func liveHeapBytes() uint64 {
	runtime.GC()
	runtime.GC()
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	return ms.HeapAlloc
}

func heapGrowth(before, after uint64) uint64 {
	if after < before {
		return 0
	}
	return after - before
}
$ sed -n '232,241p' internal/transport/recv_buffer_test.go
	if err := framer.WriteHeaders(http2.HeadersFrameParam{StreamID: streamID, BlockFragment: hbuf.Bytes(), EndHeaders: true}); err != nil {
		t.Errorf("WriteHeaders() failed: %v", err)
		return
	}
	for i := range msg {
		if err := framer.WriteData(streamID, false, msg[i:i+1]); err != nil {
			t.Errorf("WriteData() failed: %v", err)
			return
		}
	}
```

With `0` recorded on both sides, `0*5 > 0` is false and the test passes; there is no backlog-length or other structural assertion in this test.

**Demonstration 1 — the unmodified test on the unmodified branch.** `-cpu 1` sets `GOMAXPROCS=1`. Each iteration prints two `--- PASS: Test` lines because two `Test` entry points match in the package directory (`transport_test.go` and `proxy_ext_test.go`); the `0.00s` one has no matching subtest.

```console
$ go test ./internal/transport -run '^Test$/^ClientTransport_TinyDataFramesSlowReader$' -count=6 -v -cpu 1 2>&1 | grep -E 'Heap growth|not at least|want <=|^(--- FAIL|--- PASS|ok|FAIL)'
    recv_buffer_test.go:358: Heap growth for 60000 unread 1-byte DATA frames: compaction disabled = 0 bytes, compaction enabled = 0 bytes
--- PASS: Test (0.19s)
--- PASS: Test (0.00s)
    recv_buffer_test.go:358: Heap growth for 60000 unread 1-byte DATA frames: compaction disabled = 0 bytes, compaction enabled = 0 bytes
--- PASS: Test (0.19s)
--- PASS: Test (0.00s)
    recv_buffer_test.go:358: Heap growth for 60000 unread 1-byte DATA frames: compaction disabled = 0 bytes, compaction enabled = 0 bytes
--- PASS: Test (0.15s)
--- PASS: Test (0.00s)
    recv_buffer_test.go:358: Heap growth for 60000 unread 1-byte DATA frames: compaction disabled = 0 bytes, compaction enabled = 0 bytes
--- PASS: Test (0.16s)
--- PASS: Test (0.00s)
    recv_buffer_test.go:358: Heap growth for 60000 unread 1-byte DATA frames: compaction disabled = 0 bytes, compaction enabled = 38464 bytes
    recv_buffer_test.go:361: Heap growth with compaction enabled (38464 bytes) is not at least 5x smaller than with compaction disabled (0 bytes)
--- FAIL: Test (0.21s)
--- PASS: Test (0.00s)
    recv_buffer_test.go:358: Heap growth for 60000 unread 1-byte DATA frames: compaction disabled = 0 bytes, compaction enabled = 0 bytes
--- PASS: Test (0.16s)
--- PASS: Test (0.00s)
FAIL
FAIL	google.golang.org/grpc/internal/transport	1.074s
FAIL
$ go test ./internal/transport -run '^Test$/^ClientTransport_TinyDataFramesSlowReader$' -count=6 -v -cpu 8 2>&1 | grep -E 'Heap growth|not at least|want <=|^(--- FAIL|--- PASS|ok|FAIL)'
    recv_buffer_test.go:358: Heap growth for 60000 unread 1-byte DATA frames: compaction disabled = 3733824 bytes, compaction enabled = 0 bytes
--- PASS: Test (0.15s)
--- PASS: Test (0.00s)
    recv_buffer_test.go:358: Heap growth for 60000 unread 1-byte DATA frames: compaction disabled = 3811328 bytes, compaction enabled = 0 bytes
--- PASS: Test (0.14s)
--- PASS: Test (0.00s)
    recv_buffer_test.go:358: Heap growth for 60000 unread 1-byte DATA frames: compaction disabled = 3746504 bytes, compaction enabled = 0 bytes
--- PASS: Test (0.14s)
--- PASS: Test (0.00s)
    recv_buffer_test.go:358: Heap growth for 60000 unread 1-byte DATA frames: compaction disabled = 3715256 bytes, compaction enabled = 0 bytes
--- PASS: Test (0.14s)
--- PASS: Test (0.00s)
    recv_buffer_test.go:358: Heap growth for 60000 unread 1-byte DATA frames: compaction disabled = 3783904 bytes, compaction enabled = 55776 bytes
--- PASS: Test (0.16s)
--- PASS: Test (0.00s)
    recv_buffer_test.go:358: Heap growth for 60000 unread 1-byte DATA frames: compaction disabled = 3820568 bytes, compaction enabled = 56800 bytes
--- PASS: Test (0.15s)
--- PASS: Test (0.00s)
ok  	google.golang.org/grpc/internal/transport	0.876s
```

On one CPU, five of six iterations recorded zero growth for both sides — including the side with compaction disabled, which holds 60,000 separately queued buffers — and passed. The remaining one recorded 0 for the disabled side against 38,464 for the enabled side and failed, on a branch whose compaction works. On eight CPUs the disabled side is measured at about 3.7 MB in every iteration.

**Demonstration 2 — where the DATA frames are when the baseline is taken.** Probe: [`verify/repro/c8_async_baseline_test.go`](repro/c8_async_baseline_test.go). It follows the branch's `measureClientTinyDataFrames` step for step, reusing the branch's own fake server, `liveHeapBytes` and `heapGrowth`, with read-only probes added: the stream's received-byte count immediately before and immediately after the baseline call, the backlog length, and the unclamped difference. Each round also measures a third run with compaction switched off standing in for the "enabled" side, i.e. what a build whose compaction does nothing would record.

```sh
cp $V/repro/c8_async_baseline_test.go internal/transport/zz_verify_c8_test.go
go test ./internal/transport -run 'TestVerifyC8' -count=1 -v -cpu 1; echo "exit=$?"
go test ./internal/transport -run 'TestVerifyC8' -count=1 -v -cpu 8; echo "exit=$?"
rm internal/transport/zz_verify_c8_test.go
```

`-cpu 1`:

```console
    zz_verify_c8_test.go:138: round 0 compaction=false:      DATA bytes received before baseline sampling started=11522/60005, by the time it returned=60005/60005 (backlog entries=60005); after-before=    -768; recorded growth=      0
    zz_verify_c8_test.go:138: round 0 compaction=true:       DATA bytes received before baseline sampling started=31100/60005, by the time it returned=60005/60005 (backlog entries=    5); after-before=    -680; recorded growth=      0
    zz_verify_c8_test.go:138: round 0 no-op compaction:      DATA bytes received before baseline sampling started=15219/60005, by the time it returned=60005/60005 (backlog entries=60005); after-before=-1877304; recorded growth=      0
    zz_verify_c8_test.go:145: round 0 branch assertion `enabled*5 > disabled => Errorf`: disabled=0 enabled=0 -> accepted=true; with a no-op compaction as the enabled side: disabled=0 enabled=0 -> accepted=true
    zz_verify_c8_test.go:138: round 1 compaction=false:      DATA bytes received before baseline sampling started=44847/60005, by the time it returned=60005/60005 (backlog entries=60005); after-before=    -760; recorded growth=      0
    zz_verify_c8_test.go:138: round 1 compaction=true:       DATA bytes received before baseline sampling started=28766/60005, by the time it returned=60005/60005 (backlog entries=    5); after-before=    -744; recorded growth=      0
    zz_verify_c8_test.go:138: round 1 no-op compaction:      DATA bytes received before baseline sampling started=17241/60005, by the time it returned=60005/60005 (backlog entries=60005); after-before=    -752; recorded growth=      0
    zz_verify_c8_test.go:145: round 1 branch assertion `enabled*5 > disabled => Errorf`: disabled=0 enabled=0 -> accepted=true; with a no-op compaction as the enabled side: disabled=0 enabled=0 -> accepted=true
    zz_verify_c8_test.go:138: round 2 compaction=false:      DATA bytes received before baseline sampling started=21194/60005, by the time it returned=60005/60005 (backlog entries=60005); after-before=-3368312; recorded growth=      0
    zz_verify_c8_test.go:138: round 2 compaction=true:       DATA bytes received before baseline sampling started=32686/60005, by the time it returned=60005/60005 (backlog entries=    5); after-before=    -824; recorded growth=      0
    zz_verify_c8_test.go:138: round 2 no-op compaction:      DATA bytes received before baseline sampling started=16921/60005, by the time it returned=60005/60005 (backlog entries=60005); after-before=    -752; recorded growth=      0
    zz_verify_c8_test.go:145: round 2 branch assertion `enabled*5 > disabled => Errorf`: disabled=0 enabled=0 -> accepted=true; with a no-op compaction as the enabled side: disabled=0 enabled=0 -> accepted=true
    zz_verify_c8_test.go:138: round 3 compaction=false:      DATA bytes received before baseline sampling started=39297/60005, by the time it returned=60005/60005 (backlog entries=60005); after-before=    -776; recorded growth=      0
    zz_verify_c8_test.go:138: round 3 compaction=true:       DATA bytes received before baseline sampling started=18215/60005, by the time it returned=60005/60005 (backlog entries=    5); after-before=    -712; recorded growth=      0
    zz_verify_c8_test.go:138: round 3 no-op compaction:      DATA bytes received before baseline sampling started=22499/60005, by the time it returned=60005/60005 (backlog entries=60005); after-before=-3368336; recorded growth=      0
    zz_verify_c8_test.go:145: round 3 branch assertion `enabled*5 > disabled => Errorf`: disabled=0 enabled=0 -> accepted=true; with a no-op compaction as the enabled side: disabled=0 enabled=0 -> accepted=true
    zz_verify_c8_test.go:138: round 4 compaction=false:      DATA bytes received before baseline sampling started=30598/60005, by the time it returned=60005/60005 (backlog entries=60005); after-before=    -704; recorded growth=      0
    zz_verify_c8_test.go:138: round 4 compaction=true:       DATA bytes received before baseline sampling started=19407/60005, by the time it returned=60005/60005 (backlog entries=    5); after-before=    -768; recorded growth=      0
    zz_verify_c8_test.go:138: round 4 no-op compaction:      DATA bytes received before baseline sampling started=13133/60005, by the time it returned=60005/60005 (backlog entries=60005); after-before=-4556512; recorded growth=      0
    zz_verify_c8_test.go:145: round 4 branch assertion `enabled*5 > disabled => Errorf`: disabled=0 enabled=0 -> accepted=true; with a no-op compaction as the enabled side: disabled=0 enabled=0 -> accepted=true
    zz_verify_c8_test.go:153: C8 RESULT: 5 of 5 rounds were accepted although every DATA frame had been received before the baseline heap sample was taken in both measurements; 5 of 5 rounds accepted a no-op compaction
    zz_verify_c8_test.go:155: PROBLEM REPRODUCED: the test's heap assertion accepted 5 round(s) whose measurements contain none of the receive workload
--- FAIL: TestVerifyC8_BaselineSampledAfterReception (1.22s)
FAIL
FAIL	google.golang.org/grpc/internal/transport	1.228s
FAIL
exit=1
```

In this run, in every round and for every measurement, all 60,005 bytes had been received by the time the baseline call returned; the unclamped difference is negative, zero is recorded, and the branch's assertion accepts — also when the "enabled" side is really running without compaction (`backlog entries=60005`).

The timing varies from run to run. Five further `-cpu 1` runs of the same probe (with the probe file still in place):

```console
$ for i in 1 2 3 4 5; do go test ./internal/transport -run 'TestVerifyC8' -count=1 -v -cpu 1 2>&1 | grep -E 'C8 RESULT|accepted=false'; done
    zz_verify_c8_test.go:153: C8 RESULT: 5 of 5 rounds were accepted although every DATA frame had been received before the baseline heap sample was taken in both measurements; 5 of 5 rounds accepted a no-op compaction
    zz_verify_c8_test.go:153: C8 RESULT: 4 of 5 rounds were accepted although every DATA frame had been received before the baseline heap sample was taken in both measurements; 5 of 5 rounds accepted a no-op compaction
    zz_verify_c8_test.go:153: C8 RESULT: 3 of 5 rounds were accepted although every DATA frame had been received before the baseline heap sample was taken in both measurements; 5 of 5 rounds accepted a no-op compaction
    zz_verify_c8_test.go:153: C8 RESULT: 5 of 5 rounds were accepted although every DATA frame had been received before the baseline heap sample was taken in both measurements; 5 of 5 rounds accepted a no-op compaction
    zz_verify_c8_test.go:153: C8 RESULT: 4 of 5 rounds were accepted although every DATA frame had been received before the baseline heap sample was taken in both measurements; 5 of 5 rounds accepted a no-op compaction
```

Across these runs 21 of 25 rounds had every DATA frame received before the baseline sample in both of the test's measurements, and all 25 rounds accepted a no-op compaction. In a round that is not counted in the first figure, reception was nearly but not entirely complete when the baseline call returned, and zero was still recorded and accepted; one such line from a further `-cpu 1` run of the same command:

```console
    zz_verify_c8_test.go:138: round 3 compaction=false:      DATA bytes received before baseline sampling started=14594/60005, by the time it returned=58626/60005 (backlog entries=58625); after-before=-4520976; recorded growth=      0
```

`-cpu 8` (the probe passes here: nothing accepted without coverage):

```console
    zz_verify_c8_test.go:138: round 0 compaction=false:      DATA bytes received before baseline sampling started=   41/60005, by the time it returned=  728/60005 (backlog entries=  727); after-before=+3719952; recorded growth=3719952
    zz_verify_c8_test.go:138: round 0 compaction=true:       DATA bytes received before baseline sampling started=  178/60005, by the time it returned=  350/60005 (backlog entries=    0); after-before=   -7992; recorded growth=      0
    zz_verify_c8_test.go:138: round 0 no-op compaction:      DATA bytes received before baseline sampling started=   10/60005, by the time it returned=  293/60005 (backlog entries=  292); after-before=+3766576; recorded growth=3766576
    zz_verify_c8_test.go:145: round 0 branch assertion `enabled*5 > disabled => Errorf`: disabled=3719952 enabled=0 -> accepted=true; with a no-op compaction as the enabled side: disabled=3719952 enabled=3766576 -> accepted=false
    zz_verify_c8_test.go:138: round 1 compaction=false:      DATA bytes received before baseline sampling started=   42/60005, by the time it returned=  563/60005 (backlog entries=  562); after-before=+3817648; recorded growth=3817648
    zz_verify_c8_test.go:138: round 1 compaction=true:       DATA bytes received before baseline sampling started=  178/60005, by the time it returned=  747/60005 (backlog entries=    0); after-before=  -16008; recorded growth=      0
    zz_verify_c8_test.go:138: round 1 no-op compaction:      DATA bytes received before baseline sampling started=  218/60005, by the time it returned=  485/60005 (backlog entries=  484); after-before=+3743136; recorded growth=3743136
    zz_verify_c8_test.go:145: round 1 branch assertion `enabled*5 > disabled => Errorf`: disabled=3817648 enabled=0 -> accepted=true; with a no-op compaction as the enabled side: disabled=3817648 enabled=3743136 -> accepted=false
    zz_verify_c8_test.go:138: round 2 compaction=false:      DATA bytes received before baseline sampling started=   12/60005, by the time it returned=  719/60005 (backlog entries=  718); after-before=+3714056; recorded growth=3714056
    zz_verify_c8_test.go:138: round 2 compaction=true:       DATA bytes received before baseline sampling started=   26/60005, by the time it returned=  417/60005 (backlog entries=    0); after-before=   -9824; recorded growth=      0
    zz_verify_c8_test.go:138: round 2 no-op compaction:      DATA bytes received before baseline sampling started=    9/60005, by the time it returned=  402/60005 (backlog entries=  401); after-before=+3811712; recorded growth=3811712
    zz_verify_c8_test.go:145: round 2 branch assertion `enabled*5 > disabled => Errorf`: disabled=3714056 enabled=0 -> accepted=true; with a no-op compaction as the enabled side: disabled=3714056 enabled=3811712 -> accepted=false
    zz_verify_c8_test.go:138: round 3 compaction=false:      DATA bytes received before baseline sampling started=   69/60005, by the time it returned=  468/60005 (backlog entries=  467); after-before=+3743752; recorded growth=3743752
    zz_verify_c8_test.go:138: round 3 compaction=true:       DATA bytes received before baseline sampling started=   17/60005, by the time it returned= 1925/60005 (backlog entries=    0); after-before=  -13056; recorded growth=      0
    zz_verify_c8_test.go:138: round 3 no-op compaction:      DATA bytes received before baseline sampling started=   75/60005, by the time it returned=  336/60005 (backlog entries=  335); after-before=+3747064; recorded growth=3747064
    zz_verify_c8_test.go:145: round 3 branch assertion `enabled*5 > disabled => Errorf`: disabled=3743752 enabled=0 -> accepted=true; with a no-op compaction as the enabled side: disabled=3743752 enabled=3747064 -> accepted=false
    zz_verify_c8_test.go:138: round 4 compaction=false:      DATA bytes received before baseline sampling started=  137/60005, by the time it returned=  597/60005 (backlog entries=  596); after-before=+3717456; recorded growth=3717456
    zz_verify_c8_test.go:138: round 4 compaction=true:       DATA bytes received before baseline sampling started=   26/60005, by the time it returned=  475/60005 (backlog entries=    0); after-before=  -13744; recorded growth=      0
    zz_verify_c8_test.go:138: round 4 no-op compaction:      DATA bytes received before baseline sampling started=  214/60005, by the time it returned=  491/60005 (backlog entries=  490); after-before=+3752912; recorded growth=3752912
    zz_verify_c8_test.go:145: round 4 branch assertion `enabled*5 > disabled => Errorf`: disabled=3717456 enabled=0 -> accepted=true; with a no-op compaction as the enabled side: disabled=3717456 enabled=3752912 -> accepted=false
    zz_verify_c8_test.go:153: C8 RESULT: 0 of 5 rounds were accepted although every DATA frame had been received before the baseline heap sample was taken in both measurements; 0 of 5 rounds accepted a no-op compaction
--- PASS: TestVerifyC8_BaselineSampledAfterReception (1.08s)
PASS
ok  	google.golang.org/grpc/internal/transport	1.089s
exit=0
```

On eight CPUs between 293 and 1,925 of the 60,005 bytes (0.5%–3.2%) had arrived when the baseline call returned, the disabled side is measured at about 3.7 MB, and a no-op compaction is rejected in every round. The enabled side is still recorded as `0` in every round, from a negative difference that the clamp hides.

**Demonstration 3 — a real mutation.** In the throwaway worktree only, compaction is switched off in the production code ([`verify/instrumentation/c8_noop_compaction_mutation.diff`](instrumentation/c8_noop_compaction_mutation.diff)); the unmodified test is run again and the worktree restored.

```console
$ patch -p1 < $V/instrumentation/c8_noop_compaction_mutation.diff
patching file internal/transport/transport.go
$ git diff -- internal/transport/transport.go
diff --git a/internal/transport/transport.go b/internal/transport/transport.go
index 9c310c18..66452a0e 100644
--- a/internal/transport/transport.go
+++ b/internal/transport/transport.go
@@ -99,7 +99,7 @@ const (
 // is embedded in another struct.
 func (b *recvBuffer) init() {
 	b.c = make(chan recvMsg, 1)
-	b.compact = envconfig.EnableReceiveBufferCompaction
+	b.compact = false && envconfig.EnableReceiveBufferCompaction // MUTATION (verify C8)
 }
 
 func (b *recvBuffer) put(r recvMsg) {
$ go test ./internal/transport -run '^Test$/^ClientTransport_TinyDataFramesSlowReader$' -count=6 -v -cpu 1 2>&1 | grep -E 'Heap growth|not at least|want <=|^(--- FAIL|--- PASS|ok|FAIL)'
    recv_buffer_test.go:358: Heap growth for 60000 unread 1-byte DATA frames: compaction disabled = 0 bytes, compaction enabled = 0 bytes
--- PASS: Test (0.18s)
--- PASS: Test (0.00s)
    recv_buffer_test.go:358: Heap growth for 60000 unread 1-byte DATA frames: compaction disabled = 0 bytes, compaction enabled = 0 bytes
--- PASS: Test (0.17s)
--- PASS: Test (0.00s)
    recv_buffer_test.go:358: Heap growth for 60000 unread 1-byte DATA frames: compaction disabled = 0 bytes, compaction enabled = 0 bytes
--- PASS: Test (0.19s)
--- PASS: Test (0.00s)
    recv_buffer_test.go:358: Heap growth for 60000 unread 1-byte DATA frames: compaction disabled = 0 bytes, compaction enabled = 0 bytes
--- PASS: Test (0.18s)
--- PASS: Test (0.00s)
    recv_buffer_test.go:358: Heap growth for 60000 unread 1-byte DATA frames: compaction disabled = 0 bytes, compaction enabled = 0 bytes
--- PASS: Test (0.22s)
--- PASS: Test (0.00s)
    recv_buffer_test.go:358: Heap growth for 60000 unread 1-byte DATA frames: compaction disabled = 0 bytes, compaction enabled = 0 bytes
--- PASS: Test (0.17s)
--- PASS: Test (0.00s)
ok  	google.golang.org/grpc/internal/transport	1.117s
$ go test ./internal/transport -run '^Test$/^ClientTransport_TinyDataFramesSlowReader$' -count=6 -v -cpu 8 2>&1 | grep -E 'Heap growth|not at least|want <=|^(--- FAIL|--- PASS|ok|FAIL)'
    recv_buffer_test.go:358: Heap growth for 60000 unread 1-byte DATA frames: compaction disabled = 3809112 bytes, compaction enabled = 3745856 bytes
    recv_buffer_test.go:361: Heap growth with compaction enabled (3745856 bytes) is not at least 5x smaller than with compaction disabled (3809112 bytes)
--- FAIL: Test (0.14s)
--- PASS: Test (0.00s)
    recv_buffer_test.go:358: Heap growth for 60000 unread 1-byte DATA frames: compaction disabled = 3782272 bytes, compaction enabled = 3813784 bytes
    recv_buffer_test.go:361: Heap growth with compaction enabled (3813784 bytes) is not at least 5x smaller than with compaction disabled (3782272 bytes)
--- FAIL: Test (0.17s)
--- PASS: Test (0.00s)
    recv_buffer_test.go:358: Heap growth for 60000 unread 1-byte DATA frames: compaction disabled = 3715200 bytes, compaction enabled = 3758224 bytes
    recv_buffer_test.go:361: Heap growth with compaction enabled (3758224 bytes) is not at least 5x smaller than with compaction disabled (3715200 bytes)
--- FAIL: Test (0.15s)
--- PASS: Test (0.00s)
    recv_buffer_test.go:358: Heap growth for 60000 unread 1-byte DATA frames: compaction disabled = 3741152 bytes, compaction enabled = 3767592 bytes
    recv_buffer_test.go:361: Heap growth with compaction enabled (3767592 bytes) is not at least 5x smaller than with compaction disabled (3741152 bytes)
--- FAIL: Test (0.15s)
--- PASS: Test (0.00s)
    recv_buffer_test.go:358: Heap growth for 60000 unread 1-byte DATA frames: compaction disabled = 3717432 bytes, compaction enabled = 3700544 bytes
    recv_buffer_test.go:361: Heap growth with compaction enabled (3700544 bytes) is not at least 5x smaller than with compaction disabled (3717432 bytes)
--- FAIL: Test (0.15s)
--- PASS: Test (0.00s)
    recv_buffer_test.go:358: Heap growth for 60000 unread 1-byte DATA frames: compaction disabled = 3747168 bytes, compaction enabled = 3715288 bytes
    recv_buffer_test.go:361: Heap growth with compaction enabled (3715288 bytes) is not at least 5x smaller than with compaction disabled (3747168 bytes)
--- FAIL: Test (0.16s)
--- PASS: Test (0.00s)
FAIL
FAIL	google.golang.org/grpc/internal/transport	0.933s
FAIL
$ go test ./internal/transport -run '^Test$/^RecvBuffer_TinyFramesCompaction$' -count=1 -v -cpu 1 2>&1 | grep -E 'Heap growth|not at least|want <=|^(--- FAIL|--- PASS|ok|FAIL)'
    recv_buffer_test.go:115: Heap growth for 65536 unread 1-byte frames: compaction disabled = 3989552 bytes, compaction enabled = 3989520 bytes
    recv_buffer_test.go:118: Heap growth with compaction enabled = 3989520 bytes, want <= 262144 bytes for 65536 unread payload bytes
    recv_buffer_test.go:121: Heap growth with compaction enabled (3989520 bytes) is not at least 10x smaller than with compaction disabled (3989552 bytes)
--- FAIL: Test (0.06s)
--- PASS: Test (0.00s)
FAIL
FAIL	google.golang.org/grpc/internal/transport	0.059s
FAIL
$ git checkout -- internal/transport/transport.go && git status --short | wc -l
0
```

With compaction disabled in the product, the transport-level test passed six times out of six on one CPU and failed six times out of six on eight CPUs. The unit-level sibling `TestRecvBuffer_TinyFramesCompaction`, whose baseline is taken synchronously before its writes, failed on one CPU as it should.

**Impact reasoning.** `TestClientTransport_TinyDataFramesSlowReader` is the test on this branch that sends real DATA frames through a transport and checks memory, and its only check is a ratio between two heap deltas whose baseline races with frame reception. When reception finishes, or all but finishes, before the baseline is read — the outcome of every single-CPU round of the probe here (30 of 30 accepted a no-op compaction), which is the situation on a one-core CI runner or a container limited to one CPU — both deltas are recorded as zero and the test passes without having measured any buffering; in that mode it passed six times out of six with compaction disabled in the production code. The same race also produced one failure in six iterations on the unmodified, working branch, so the test is unreliable in both directions on one CPU. On eight CPUs the mutation was caught every time, so the gap is timing-dependent rather than constant. The unit-level test in the same file covers `recvBuffer` compaction deterministically; what can regress unnoticed is the transport wiring (frames arriving through `http2Client`). No runtime behaviour of the library is affected; the consequence is a regression test that can report success for a transport-level regression, and occasionally failure for a correct build.

## C9

**Claim:** a three-byte receive burst allocates an additional compaction destination of at least 4,096 bytes when one byte occupies the receive channel and two bytes enter the backlog.

**Branch:** [`evalon/grpc-go-tr-19af14ab`](https://github.com/kaitranntt-evals/grpc-go-transport-restrict-memory-overhead/tree/evalon/grpc-go-tr-19af14ab) at `561bbc76341b5d77fbe98782df9c886e1cfa276e`.

**Observed: the problem is real.** Probe: [`verify/repro/c9_three_byte_burst_test.go`](repro/c9_three_byte_burst_test.go). With `runtime.MemProfileRate = 1` (every allocation recorded) it delivers three one-byte frames to a receive buffer whose channel is not drained, and reports every allocation whose stack passes through `recvBuffer.put`, grouped by the innermost transport function and line, plus the resulting queue state. Run with compaction disabled and enabled. The second test repeats the burst 100 times, reading and freeing each burst before the next.

```sh
V=$(pwd)/verify
git fetch https://github.com/kaitranntt-evals/grpc-go-transport-restrict-memory-overhead evalon/grpc-go-tr-19af14ab
git worktree add --detach /tmp/wt-19af14ab FETCH_HEAD
cp verify/repro/c9_three_byte_burst_test.go /tmp/wt-19af14ab/internal/transport/zz_verify_c9_test.go
cd /tmp/wt-19af14ab && go test ./internal/transport -run 'TestVerifyC9' -count=1 -v; echo "exit=$?"
```

```console
    zz_verify_c9_test.go:125: compaction=false: receive channel holds 1 msg (1 byte); backlog holds 2 entries with 2 payload bytes in 2 bytes of backing capacity
    zz_verify_c9_test.go:128: compaction=false:   alloc under recvBuffer.put: transport.(*recvBuffer).put (transport.go:115)       objects=2 bytes=96
    zz_verify_c9_test.go:131: compaction=false: total bytes allocated under recvBuffer.put for the 3 frames = 96
    zz_verify_c9_test.go:125: compaction=true: receive channel holds 1 msg (1 byte); backlog holds 1 entry with 2 payload bytes in 4096 bytes of backing capacity
    zz_verify_c9_test.go:128: compaction=true:   alloc under recvBuffer.put: transport.(*recvBuffer).compact (transport.go:139)   objects=2 bytes=4120
    zz_verify_c9_test.go:128: compaction=true:   alloc under recvBuffer.put: transport.(*recvBuffer).put (transport.go:115)       objects=1 bytes=32
    zz_verify_c9_test.go:131: compaction=true: total bytes allocated under recvBuffer.put for the 3 frames = 4152
    zz_verify_c9_test.go:134: C9 RESULT: three one-byte frames, channel undrained: backing capacity holding the 2 backlogged bytes: disabled=2 enabled=4096; bytes allocated under put: disabled=96 enabled=4152 (+4056)
    zz_verify_c9_test.go:136: PROBLEM REPRODUCED: only with compaction enabled, a 4096-byte compaction destination is allocated to hold the two backlogged bytes
--- FAIL: TestVerifyC9_ThreeOneByteFrames (0.01s)
    zz_verify_c9_test.go:147: compaction=false:   alloc under recvBuffer.put: transport.(*recvBuffer).put (transport.go:115)       objects=200 bytes=9600
    zz_verify_c9_test.go:147: compaction=true:   alloc under recvBuffer.put: transport.(*recvBuffer).compact (transport.go:139)   objects=200 bytes=412000
    zz_verify_c9_test.go:147: compaction=true:   alloc under recvBuffer.put: transport.(*recvBuffer).put (transport.go:115)       objects=100 bytes=3200
    zz_verify_c9_test.go:152: C9 RESULT (repeated): 100 bursts of three one-byte frames, each fully read before the next: bytes allocated under recvBuffer.put: disabled=9600 (96/burst) enabled=415200 (4152/burst)
    zz_verify_c9_test.go:154: PROBLEM REPRODUCED: every three-byte burst allocates a fresh compaction destination of at least 4096 bytes when compaction is enabled
--- FAIL: TestVerifyC9_RepeatedThreeByteBursts (0.02s)
FAIL
FAIL	google.golang.org/grpc/internal/transport	0.037s
FAIL
exit=1
```

The allocation site reported for the enabled path is the compaction destination (4,120 bytes in two objects is consistent with the 4,096-byte backing array plus the 24-byte slice header, which escapes through `&data`):

```console
$ cd /tmp/wt-19af14ab && sed -n '119,122p;139,142p' internal/transport/transport.go
const (
	recvBufferMaxSmallSize = 1024
	recvBufferCompactSize  = 4096
)
		data := make(mem.SliceBuffer, 0, recvBufferCompactSize)
		data = append(data, last.buffer.ReadOnlyData()...)
		last.buffer.Free()
		b.compacted = &data
```

**Impact reasoning.** With one byte in the channel and two in the backlog, the disabled path allocates 96 bytes (backlog slice growth) and holds the two bytes in two bytes of storage; the enabled path — the default — allocates 4,152 bytes and holds the same two bytes in a 4,096-byte buffer. The destination comes from `make`, not from the transport's buffer pool, and a new one is allocated every time a backlog of two small frames forms: 100 drained three-byte bursts allocated 415,200 bytes with compaction enabled against 9,600 without (4,152 versus 96 per burst). A stream on which a couple of small messages queue behind a reader is the ordinary case for small-message streaming, so this is paid on the everyday path, as allocation and GC work and as 4 KiB pinned per lightly backlogged stream, where the feature's purpose is to keep memory proportional to unread payload. Delivered bytes were correct. `GRPC_GO_EXPERIMENTAL_ENABLE_RECEIVE_BUFFER_COMPACTION=false` restores the 96-byte behaviour.

## C10

**Claim:** the delivered Go source files under `internal/transport` or `internal/envconfig` contain formatting or simplification differences reported by `gofmt -s`.

**Branch:** the audited branch [`grpc-go-transport-restrict-memory-overhead-perfect`](https://github.com/kaitranntt-evals/grpc-go/tree/grpc-go-transport-restrict-memory-overhead-perfect) at `327a6ff993d9866ea656ed166b89744aadcbc940` (this claim names no other branch).

**Observed: `gofmt -s` reports nothing.** Run at the repository root; neither command modifies files. The second form is the one the repository's own vet script uses (`fail_on_output` from `scripts/common.sh` exits non-zero if anything is printed). The last command is a positive control showing that the same pipeline does print a diff and exit 1 when a file needs formatting and simplification.

```console
$ git rev-parse HEAD
327a6ff993d9866ea656ed166b89744aadcbc940
$ git diff --name-only c92e985770b7194d4a4f433c84d42c6c195e8ce5 HEAD -- internal/transport internal/envconfig
internal/envconfig/envconfig.go
internal/transport/handler_server.go
internal/transport/http2_client.go
internal/transport/http2_server.go
internal/transport/transport.go
internal/transport/transport_test.go
$ gofmt -s -d -l internal/transport internal/envconfig; echo "exit=$?"
exit=0
$ gofmt -s -d -l internal/transport internal/envconfig 2>&1 | wc -c
0
$ bash -o pipefail -c 'source scripts/common.sh; gofmt -s -d -l internal/transport internal/envconfig 2>&1 | fail_on_output'; echo "exit=$?"
exit=0
$ find internal/transport internal/envconfig -name '*.go' | wc -l
32
$ mkdir -p /tmp/gofmt-control && printf 'package x\nvar a = []int{  1,2 }\nvar b = a[0:len(a)]\n' > /tmp/gofmt-control/x.go
$ bash -o pipefail -c 'source scripts/common.sh; gofmt -s -d -l /tmp/gofmt-control 2>&1 | fail_on_output'; echo "exit=$?"
/tmp/gofmt-control/x.go
diff /tmp/gofmt-control/x.go.orig /tmp/gofmt-control/x.go
--- /tmp/gofmt-control/x.go.orig
+++ /tmp/gofmt-control/x.go
@@ -1,3 +1,4 @@
 package x
-var a = []int{  1,2 }
-var b = a[0:len(a)]
+
+var a = []int{1, 2}
+var b = a[0:]
exit=1
```

All 32 Go files under the two directories, including the six the audited commit touches, are clean under `gofmt -s` (gofmt from go1.25.7).
