## Session facts

- Audited branch: `grpc-go-transport-restrict-memory-overhead-perfect` @ `327a6ff993d9866ea656ed166b89744aadcbc940` (repo `kaitranntt-evals/grpc-go`), checked out as `verify/grpc-go-transport-restrict-memory-overhead-v-0f27fd35`.
- Claim-target branches live in a second repository, `kaitranntt-evals/grpc-go-transport-restrict-memory-overhead`, fetched as remote `claims` into separate worktrees:
  - C1: `evalon/grpc-go-tr-73225c34` @ `cc2dd3ba9a2a73dd38cfccc89a9623c7902d1799` (`~/wt/c1`)
  - C3: `evalon/grpc-go-tr-247ab7e8` @ `a9f4fc9d2a1657a7be6f1ed27932698ab9a53f96` (`~/wt/c3`)
  - C4: `evalon/grpc-go-tr-9e4507fd` @ `acb8c878946a5f9d2e1e8cd87433af4f803072c0` (`~/wt/c4`)
- Toolchain: `go version go1.25.7 linux/amd64`.
- Eval fixture: `eval_tests.zip` → `tests/eval_recv_buffer_compaction_test.go` (28,647 bytes), copied byte-exact to `internal/transport/eval_recv_buffer_compaction_test.go` when run and removed afterwards.
- Fixture extraction: `mkdir -p ~/eval && cd ~/eval && unzip -o eval_tests.zip` (commands below refer to `~/eval/tests/...`).
- `verify/go.mod` only exists so that `go build ./...` / `go vet ./...` at the repo root skip the repro files (they are `package transport` test files meant to be copied into `internal/transport/`).
- Console blocks are the lines selected by the `grep` shown in the command or in the run script; `...` marks elided text inside a line.
- No production code is changed on this branch. Everything used to gather evidence is under `verify/repro/`; test files are copied into `internal/transport/` for a run and removed, patches are applied to a worktree for a run and reverted.

```sh
cd ~/repos/grpc-go
git fetch origin grpc-go-transport-restrict-memory-overhead-perfect
git checkout -b verify/grpc-go-transport-restrict-memory-overhead-v-0f27fd35 origin/grpc-go-transport-restrict-memory-overhead-perfect
git remote add claims https://github.com/kaitranntt-evals/grpc-go-transport-restrict-memory-overhead
for b in evalon/grpc-go-tr-73225c34 evalon/grpc-go-tr-247ab7e8 evalon/grpc-go-tr-9e4507fd; do git fetch claims $b:refs/remotes/claims/$b; done
git worktree add ~/wt/c1 claims/evalon/grpc-go-tr-73225c34
git worktree add ~/wt/c3 claims/evalon/grpc-go-tr-247ab7e8
git worktree add ~/wt/c4 claims/evalon/grpc-go-tr-9e4507fd
```

## C1

**Claim:** the solution's added/changed tests do not verify terminal-error or stream-reset handling while receive buffering is active. **Branch:** `evalon/grpc-go-tr-73225c34`. **Verdict: REFUTED.**

### What the tests assert (inspection)

The branch adds `internal/transport/recv_buffer_compaction_test.go` and a `tinyMessages` server handler in `transport_test.go`. `TestClientReceivesManyTinyFrames` (lines 227-301):

- the server handler writes 10,000 six-byte DATA frames and then `WriteStatus(OK)`;
- the client reads nothing until `<-s.Done()` (line 260), i.e. until the trailers (terminal `io.EOF`) were processed;
- it then reads all 10,000 messages asserting each header and payload byte in order (lines 281-294; `t.Fatalf("ReadMessageHeader() for message %d = %v, want <nil>")` at line 282);
- finally asserts the terminal error: `if _, err := s.readTo(make([]byte, 1)); err != io.EOF { t.Fatalf("read after the last message = %v, want io.EOF", err) }` (lines 296-297).

`TestRecvBufferCompactionPreservesByteStream` also writes `io.EOF`, but only after everything was read (buffer empty), so it does not count. No added test sends RST_STREAM; the claim's refute condition asks for "a terminal error **or** stream reset".

### Is buffering really active when the terminal error arrives? (execution)

Instrumentation patch `verify/repro/c1/trace_err_put.patch` prints the recvBuffer state inside `recvBuffer.put` whenever `r.err != nil`.

```sh
cd ~/wt/c1
git apply ~/repos/grpc-go/verify/repro/c1/trace_err_put.patch
go test -count=5 -v -run 'Test/ClientReceivesManyTinyFrames$' ./internal/transport 2>&1 | grep -E "VERIFY-C1|^(ok|FAIL)" | sort | uniq -c
git checkout -- internal/transport/transport.go
```

```console
      5 VERIFY-C1 put(err=EOF): compact=false chanLen=1 backlogLen=9999 pendingBytes=-1
      5 VERIFY-C1 put(err=EOF): compact=true chanLen=1 backlogLen=3 pendingBytes=10842
      1 ok  	google.golang.org/grpc/internal/transport	0.713s
```

With compaction enabled the terminal `io.EOF` is put while 1 message sits in the channel, 3 full 16 KiB chunks sit in the backlog and a partially filled compaction chunk (`pending`, 10,842 bytes) is open: receive buffering/compaction is active, deterministically (5/5 runs).

### Do the assertions depend on correct handling? (mutation of the solution)

Three production-code mutations of `recvBuffer.put` (patches in `verify/repro/c1/`), each run against the branch's own unmodified tests:

```sh
cd ~/wt/c1 && bash ~/repos/grpc-go/verify/repro/c1/run_c1.sh
```

Baseline (trace patch only, behaviour unchanged):

```console
VERIFY-C1 put(err=EOF): compact=true chanLen=1 backlogLen=3 pendingBytes=10842
    recv_buffer_compaction_test.go:267: 10000 six-byte frames queued in 4 buffers with 65536 bytes of capacity
VERIFY-C1 put(err=EOF): compact=false chanLen=1 backlogLen=9999 pendingBytes=-1
    recv_buffer_compaction_test.go:267: 10000 six-byte frames queued in 9999 buffers with 59994 bytes of capacity
VERIFY-C1 put(err=EOF): compact=true chanLen=0 backlogLen=0 pendingBytes=-1
VERIFY-C1 put(err=EOF): compact=false chanLen=0 backlogLen=0 pendingBytes=-1
--- PASS: Test (0.25s)
    --- PASS: Test/ClientReceivesManyTinyFrames (0.21s)
        --- PASS: Test/ClientReceivesManyTinyFrames/compaction=true (0.09s)
        --- PASS: Test/ClientReceivesManyTinyFrames/compaction=false (0.07s)
    --- PASS: Test/RecvBufferCompactionPreservesByteStream (0.00s)
    --- PASS: Test/RecvBufferCompactionTinyFramesMemory (0.04s)
ok  	google.golang.org/grpc/internal/transport	0.256s
```

(The two `chanLen=0 backlogLen=0` lines are `TestRecvBufferCompactionPreservesByteStream`: EOF with nothing buffered.)

`mutation1_error_overtakes_pending` (error is queued without first flushing the pending chunk, so it overtakes buffered data):

```console
    recv_buffer_compaction_test.go:282: ReadMessageHeader() for message 8193 = EOF, want <nil>
--- FAIL: Test (0.22s)
    --- FAIL: Test/ClientReceivesManyTinyFrames (0.16s)
        --- FAIL: Test/ClientReceivesManyTinyFrames/compaction=true (0.04s)
        --- PASS: Test/ClientReceivesManyTinyFrames/compaction=false (0.11s)
    --- PASS: Test/RecvBufferCompactionPreservesByteStream (0.00s)
    --- PASS: Test/RecvBufferCompactionTinyFramesMemory (0.05s)
FAIL	google.golang.org/grpc/internal/transport	0.225s
```

`mutation2_error_dropped_while_buffering` (terminal `io.EOF` is dropped when data is buffered): the test blocks at its EOF assertion and the run is killed by `-timeout 120s`:

```console
    recv_buffer_compaction_test.go:267: 10000 six-byte frames queued in 4 buffers with 65536 bytes of capacity
panic: test timed out after 2m0s
	/home/ubuntu/wt/c1/internal/transport/recv_buffer_compaction_test.go:296 +0xab6
FAIL	google.golang.org/grpc/internal/transport	120.106s
```

`mutation3_error_discards_pending` (compaction tracking state is thrown away when the error arrives):

```console
    recv_buffer_compaction_test.go:267: 10000 six-byte frames queued in 3 buffers with 49152 bytes of capacity
    recv_buffer_compaction_test.go:282: ReadMessageHeader() for message 8193 = EOF, want <nil>
--- FAIL: Test (0.20s)
    --- FAIL: Test/ClientReceivesManyTinyFrames (0.17s)
        --- FAIL: Test/ClientReceivesManyTinyFrames/compaction=true (0.08s)
        --- PASS: Test/ClientReceivesManyTinyFrames/compaction=false (0.09s)
FAIL	google.golang.org/grpc/internal/transport	0.209s
```

Only `TestClientReceivesManyTinyFrames/compaction=true` catches the mutations; the two unit tests stay green under mutations 1 and 3 (under mutation 2 the run is killed before they execute).

Fixture cross-check on this branch (not one of the solution's tests, listed in the claim's "where to look"):

```sh
cd ~/wt/c1 && cp ~/eval/tests/eval_recv_buffer_compaction_test.go internal/transport/
go test -v -run '^TestEval_RecvBufferErrorResetSafety$' ./internal/transport -race -count=1
rm internal/transport/eval_recv_buffer_compaction_test.go
```

```console
--- PASS: TestEval_RecvBufferErrorResetSafety (0.00s)
ok  	google.golang.org/grpc/internal/transport	1.015s
```

### Reasoning

One added test (`TestClientReceivesManyTinyFrames`, compaction=true) delivers a terminal error (`io.EOF` from trailers) while a compaction chunk is open and a backlog exists, and asserts both ordered delivery of all queued data and the terminal error afterwards; breaking ordering, error delivery, or pending-state handling in the solution turns it red. That meets the claim's refute condition. Residual gap, not enough to confirm the claim as worded: no added test exercises RST_STREAM or a non-EOF error while buffering is active.

## C2

**Claim:** queuing 512 consecutive 64-byte handler reads retains 512 separate 16-KiB backing allocations for 32 KiB of unread payload. **Branch:** `grpc-go-transport-restrict-memory-overhead-perfect`. **Verdict: CONFIRMED (both parts).**

### Commands

```sh
cd ~/repos/grpc-go
cp verify/repro/c2_c5_handler_backing_retention_test.go internal/transport/zz_verify_c2_c5_test.go
go test -v -count=1 -run '^TestVerifyC2C5' ./internal/transport
GRPC_GO_EXPERIMENTAL_ENABLE_RECEIVE_BUFFER_COMPACTION=false go test -v -count=1 -run '^TestVerifyC2C5_(HandleStreamsPipeBody|RealHTTP2Handler)$/^(size=64|size=1)$' ./internal/transport
rm internal/transport/zz_verify_c2_c5_test.go
```

The repro has three tests:

- `TestVerifyC2C5_HandleStreamsPipeBody` — runs the delivered `serverHandlerTransport.HandleStreams` with an `io.Pipe` request body, performs N body writes of S bytes (each becomes exactly one `req.Body.Read` of S bytes), never reads the gRPC stream, then inspects `s.buf` (delivery channel + backlog): distinct backing arrays by pointer, `cap()` of each, and the compaction tracking fields after every put.
- `TestVerifyC2C5_CapacityIgnoredByAccounting` — feeds a bare `recvBuffer` 512 64-byte payloads twice, backed by exact 64-byte slices vs. 16 KiB pooled allocations, and prints the tracking state.
- `TestVerifyC2C5_RealHTTP2Handler` — same workload through a real `golang.org/x/net/http2` server whose handler is `NewServerHandlerTransport(...).HandleStreams`, with a raw HTTP/2 client sending 512 DATA frames of 64 bytes.

### Output (compaction enabled, the default)

```console
RESULT size=64 reads=512: queued msgs=512 (backlog=511 + channel=1) payload=32768 B | distinct backing allocations=512, total backing capacity=8388608 B (8192.0 KiB, 256x payload) | peak during run: 512 allocations / 8388608 B | puts that left tracking reset (suffixLen=0,bytes=0): 511 of 511 backlog puts | HeapAlloc delta=8566016 B
RESULT size=100 reads=512: queued msgs=512 (backlog=511 + channel=1) payload=51200 B | distinct backing allocations=512, total backing capacity=8388608 B (8192.0 KiB, 164x payload) | ... | puts that left tracking reset (suffixLen=0,bytes=0): 511 of 511 backlog puts | HeapAlloc delta=8586496 B
RESULT size=128 reads=512: queued msgs=512 (backlog=511 + channel=1) payload=65536 B | distinct backing allocations=512, total backing capacity=8388608 B (8192.0 KiB, 128x payload) | ... | puts that left tracking reset (suffixLen=0,bytes=0): 511 of 511 backlog puts | HeapAlloc delta=8619824 B
RESULT size=57 reads=512: queued msgs=512 (backlog=511 + channel=1) payload=29184 B | distinct backing allocations=512, total backing capacity=8388608 B (8192.0 KiB, 287x payload) | ... | puts that left tracking reset (suffixLen=0,bytes=0): 511 of 511 backlog puts | HeapAlloc delta=8552560 B
RESULT size=56 reads=2048: queued msgs=2048 (backlog=2047 + channel=1) payload=114688 B | distinct backing allocations=2048, total backing capacity=33554432 B (32768.0 KiB, 293x payload) | ... | puts that left tracking reset (suffixLen=0,bytes=0): 2047 of 2047 backlog puts | HeapAlloc delta=34089760 B
RESULT size=1 reads=512: queued msgs=512 (backlog=511 + channel=1) payload=512 B | distinct backing allocations=512, total backing capacity=8388608 B (8192.0 KiB, 16384x payload) | ... | puts that left tracking reset (suffixLen=0,bytes=0): 0 of 511 backlog puts | HeapAlloc delta=8516136 B
RESULT size=1 reads=2048: queued msgs=1024 (backlog=1023 + channel=1) payload=2048 B | distinct backing allocations=1024, total backing capacity=16764928 B (16372.0 KiB, 8186x payload) | peak during run: 1025 allocations / 16793600 B | puts that left tracking reset (suffixLen=0,bytes=0): 1 of 2047 backlog puts | HeapAlloc delta=17014352 B
--- PASS: TestVerifyC2C5_HandleStreamsPipeBody (0.89s)
recvMsgSize=56 utilizationFactor=2 compactionThreshold=58368  => for a 64-byte put: backlogHeapSize=120, utilizationFactor*bytes=128, reset=true
RESULT pooled16KiB=false: msgs=512 backlog=511 payload=32768 distinct=512 backingCap=32768 trace: [put#0 backlog=0 suffixLen=0 suffixBytes=0] [put#1 backlog=1 suffixLen=0 suffixBytes=0] [put#2 backlog=2 suffixLen=0 suffixBytes=0] [put#3 backlog=3 suffixLen=0 suffixBytes=0] [put#511 backlog=511 suffixLen=0 suffixBytes=0]
RESULT pooled16KiB=true: msgs=512 backlog=511 payload=32768 distinct=512 backingCap=8388608 trace: [put#0 backlog=0 suffixLen=0 suffixBytes=0] [put#1 backlog=1 suffixLen=0 suffixBytes=0] [put#2 backlog=2 suffixLen=0 suffixBytes=0] [put#3 backlog=3 suffixLen=0 suffixBytes=0] [put#511 backlog=511 suffixLen=0 suffixBytes=0]
--- PASS: TestVerifyC2C5_CapacityIgnoredByAccounting (0.00s)
RESULT real net/http2 handler, 512 DATA frames x 64 B: queued msgs=512 (backlog=511) payload=32768 B | distinct backing allocations=512, total backing capacity=8388608 B (8192.0 KiB, 256x payload) | suffixLen=0 suffixBytes=0
--- PASS: TestVerifyC2C5_RealHTTP2Handler (0.41s)
ok  	google.golang.org/grpc/internal/transport	1.314s
```

### Output (`GRPC_GO_EXPERIMENTAL_ENABLE_RECEIVE_BUFFER_COMPACTION=false`, i.e. previous behaviour)

```console
RESULT size=64 reads=512: queued msgs=512 (backlog=511 + channel=1) payload=32768 B | distinct backing allocations=512, total backing capacity=8388608 B (8192.0 KiB, 256x payload) | ...
RESULT size=1 reads=512: queued msgs=512 (backlog=511 + channel=1) payload=512 B | distinct backing allocations=512, total backing capacity=8388608 B (8192.0 KiB, 16384x payload) | ...
RESULT size=1 reads=2048: queued msgs=2048 (backlog=2047 + channel=1) payload=2048 B | distinct backing allocations=2048, total backing capacity=33554432 B (32768.0 KiB, 16384x payload) | ...
RESULT real net/http2 handler, 512 DATA frames x 64 B: queued msgs=512 (backlog=511) payload=32768 B | distinct backing allocations=512, total backing capacity=8388608 B (8192.0 KiB, 256x payload) | suffixLen=0 suffixBytes=0
ok  	google.golang.org/grpc/internal/transport	0.518s
```

### Part: compaction accounting — holds

- `compactBacklogLocked` (`internal/transport/transport.go`) computes `backlogHeapSize := b.uncompactedSuffixLen*recvMsgSize + b.uncompactedBytes`, where `uncompactedBytes += r.buffer.Len()`; `cap()` never appears.
- Observed: `recvMsgSize=56`, so a 64-byte put gives `56+64=120 <= 2*64=128` → tracking reset. In the run, 511 of 511 backlog puts left `suffixLen=0, suffixBytes=0`, and the tracking trace is identical whether the 64 bytes sit in a 64-byte slice (`backingCap=32768`) or in a 16 KiB pooled allocation (`backingCap=8388608`).

### Part: handler input path — holds

- `HandleStreams` (`internal/transport/handler_server.go`) does `buf := ht.bufferPool.Get(http2MaxFrameLen)`, `n, err := req.Body.Read(*buf)`, `*buf = (*buf)[:n]`, `s.buf.put(recvMsg{buffer: mem.NewBuffer(buf, ht.bufferPool)})` — a fresh 16 KiB pool buffer per read, resliced to `n`.
- Observed through the real `HandleStreams` (pipe body and real net/http2 server): 512 queued messages, 512 distinct backing arrays, 8,388,608 bytes of capacity for 32,768 payload bytes; `HeapAlloc` grew by ~8.57 MB.

### Precision note

Of the 512 messages, 511 are in `recvBuffer.backlog` and 1 is parked in the buffer's 1-slot delivery channel; all 512 allocations are retained by the stream's receive buffer. The claim's "backlog retains 512" is off by this one slot; the retained total (512 × 16 KiB = 8 MiB) is exactly as claimed.

### Impact reasoning

- Affected path: gRPC servers served through `grpc.Server.ServeHTTP` (`serverHandlerTransport`). The HTTP/2 client/server transports are not affected by this specific mechanism (the framer allocates right-sized buffers for payloads ≤ 1 KiB).
- A peer sending 64-byte DATA frames to a stream whose handler reads slowly pins 16 KiB per frame: 256× the unread payload. With compaction disabled the numbers are byte-identical, so on this path the fix delivers no improvement for reads of 56 bytes and above (measured: 56-byte reads, 2,048 of them → 2,048 allocations / 32 MiB, never compacted).
- Even reads small enough to be compacted (1 byte) accumulate up to ~1,024 16-KiB buffers (16 MiB for ≤ 1 KiB of payload) before the first compaction fires, because the threshold counts payload bytes + 56-byte headers, not capacity.
- All 8 top-level eval fixture tests pass on this branch (`cp ~/eval/tests/eval_recv_buffer_compaction_test.go internal/transport/ && go test -v -run '^TestEval_' ./internal/transport -race -count=1` → `--- PASS` for each, `ok  	google.golang.org/grpc/internal/transport	1.129s`), including `TestEval_RecvBufferCompaction/HandlerServerStream`, which only uses 1,026 one-byte handler-style buffers and checks backlog length, not capacity. Nothing in the fixture or in the solution's tests exercises 64/100/128-byte handler reads.
- The env toggle is not a workaround (same result either way); none was found.

## C3

**Claim:** repeated short-message assembly retains more than 4× the queued payload + 65,536 bytes of backing capacity in at least one of: 4,096 three-byte cycles (bound 114,688) or 256 1,800-byte cycles (bound 1,908,736). **Branch:** `evalon/grpc-go-tr-247ab7e8`. **Verdict: REFUTED.**

### Naming drift

This branch has no `appendPending` or `compactBacklogLocked`; the equivalents are `recvBuffer.compact` and `recvBuffer.flushCompactBuf` in `internal/transport/transport.go`, with `recvBufferCompactionSize = 4 << 10` and `recvBufferCompactionThreshold = 1 << 10`. `flushCompactBuf` hands the reader a right-sized `mem.Copy` when the compaction buffer holds ≤ 1,024 bytes, otherwise the pooled compaction buffer itself.

### Commands

```sh
cd ~/wt/c3
cp ~/repos/grpc-go/verify/repro/c3_assembly_retention_test.go internal/transport/zz_verify_c3_test.go
go test -v -count=1 -run '^TestVerifyC3' ./internal/transport
rm internal/transport/zz_verify_c3_test.go
```

- `TestVerifyC3_DirectPut` replays the fixture's `IncrementalMessageAssembly_*` loop (put 3 frames on a production client stream's `recvBuffer`, read the 3-frame message through `recvBufferReader.Read`, retain the buffers, measure with the fixture's distinct-backing-capacity method) with the claim's two workloads; pools: default and the fixture's `NewBinaryTieredBufferPool(12,14,15,20)`; inputs: `mem.Copy` (fixture) and framer-style exact allocation; plus a "never read" mode that measures what stays queued inside the `recvBuffer`.
- `TestVerifyC3_EndToEnd` runs both workloads through the delivered client transport: a raw HTTP/2 peer sends each cycle's three DATA frames, the real framer allocates them, the real `recvBuffer` compacts them, `ClientStream.Read` assembles each message and the test retains the result. The test waits until all three frames are queued before each read, and counts cycles whose 2-frame tail sat in the compaction buffer.

### Output

```console
pool default: Get(recvBufferCompactionSize=4096) -> cap 4096
pool tiered(12,14,15,20): Get(recvBufferCompactionSize=4096) -> cap 4096
RESULT 4096x3B pool=default input=mem.Copy(fixture) mode=read-each-cycle-and-retain: payload=12288 B, distinct backing allocations=8192, retained capacity=12288 B, bound=114688 B -> WITHIN
RESULT 4096x3B pool=default input=mem.Copy(fixture) mode=never-read(left queued in recvBuffer): payload=12288 B, distinct backing allocations=4, retained capacity=12289 B, bound=114688 B -> WITHIN
RESULT 4096x3B pool=default input=framer(readDataFrame: make+SliceBuffer below 1KiB) mode=read-each-cycle-and-retain: payload=12288 B, distinct backing allocations=8192, retained capacity=12288 B, bound=114688 B -> WITHIN
RESULT 4096x3B pool=default input=framer(readDataFrame: make+SliceBuffer below 1KiB) mode=never-read(left queued in recvBuffer): payload=12288 B, distinct backing allocations=4, retained capacity=12289 B, bound=114688 B -> WITHIN
RESULT 4096x3B pool=tiered(12,14,15,20) input=mem.Copy(fixture) mode=read-each-cycle-and-retain: payload=12288 B, distinct backing allocations=8192, retained capacity=12288 B, bound=114688 B -> WITHIN
RESULT 4096x3B pool=tiered(12,14,15,20) input=mem.Copy(fixture) mode=never-read(left queued in recvBuffer): payload=12288 B, distinct backing allocations=4, retained capacity=12289 B, bound=114688 B -> WITHIN
RESULT 4096x3B pool=tiered(12,14,15,20) input=framer(readDataFrame: make+SliceBuffer below 1KiB) mode=read-each-cycle-and-retain: payload=12288 B, distinct backing allocations=8192, retained capacity=12288 B, bound=114688 B -> WITHIN
RESULT 4096x3B pool=tiered(12,14,15,20) input=framer(readDataFrame: make+SliceBuffer below 1KiB) mode=never-read(left queued in recvBuffer): payload=12288 B, distinct backing allocations=4, retained capacity=12289 B, bound=114688 B -> WITHIN
RESULT 256x1800B pool=default input=mem.Copy(fixture) mode=read-each-cycle-and-retain: payload=460800 B, distinct backing allocations=512, retained capacity=1202176 B, bound=1908736 B -> WITHIN
RESULT 256x1800B pool=default input=mem.Copy(fixture) mode=never-read(left queued in recvBuffer): payload=460800 B, distinct backing allocations=129, retained capacity=524888 B, bound=1908736 B -> WITHIN
RESULT 256x1800B pool=default input=framer(readDataFrame: make+SliceBuffer below 1KiB) mode=read-each-cycle-and-retain: payload=460800 B, distinct backing allocations=512, retained capacity=1202176 B, bound=1908736 B -> WITHIN
RESULT 256x1800B pool=default input=framer(readDataFrame: make+SliceBuffer below 1KiB) mode=never-read(left queued in recvBuffer): payload=460800 B, distinct backing allocations=129, retained capacity=524888 B, bound=1908736 B -> WITHIN
RESULT 256x1800B pool=tiered(12,14,15,20) input=mem.Copy(fixture) mode=read-each-cycle-and-retain: payload=460800 B, distinct backing allocations=512, retained capacity=1202176 B, bound=1908736 B -> WITHIN
RESULT 256x1800B pool=tiered(12,14,15,20) input=mem.Copy(fixture) mode=never-read(left queued in recvBuffer): payload=460800 B, distinct backing allocations=129, retained capacity=524888 B, bound=1908736 B -> WITHIN
RESULT 256x1800B pool=tiered(12,14,15,20) input=framer(readDataFrame: make+SliceBuffer below 1KiB) mode=read-each-cycle-and-retain: payload=460800 B, distinct backing allocations=512, retained capacity=1202176 B, bound=1908736 B -> WITHIN
RESULT 256x1800B pool=tiered(12,14,15,20) input=framer(readDataFrame: make+SliceBuffer below 1KiB) mode=never-read(left queued in recvBuffer): payload=460800 B, distinct backing allocations=129, retained capacity=524888 B, bound=1908736 B -> WITHIN
--- PASS: TestVerifyC3_DirectPut (0.04s)
RESULT e2e 4096x3B: cycles whose 2-frame tail sat in the compaction buffer=4096/4096, payload=12288 B, distinct backing allocations=8192, retained capacity=12288 B, bound=114688 B -> WITHIN
RESULT e2e 256x1800B: cycles whose 2-frame tail sat in the compaction buffer=256/256, payload=460800 B, distinct backing allocations=512, retained capacity=1202176 B, bound=1908736 B -> WITHIN
--- PASS: TestVerifyC3_EndToEnd (1.35s)
INFO handler-style 16KiB-backed inputs 4096x3B: payload=12288 B, distinct backing allocations=8192, retained capacity=67117056 B, bound=114688 B
INFO handler-style 16KiB-backed inputs 256x1800B: payload=460800 B, distinct backing allocations=512, retained capacity=5242880 B, bound=1908736 B
--- PASS: TestVerifyC3_HandlerStyleInputsInfo (0.04s)
PASS
ok  	google.golang.org/grpc/internal/transport	1.441s
```

All 18 `RESULT` lines (16 direct-put combinations + 2 end-to-end) report `WITHIN`; none reports `EXCEEDS`.

Fixture on this branch:

```sh
cd ~/wt/c3 && cp ~/eval/tests/eval_recv_buffer_compaction_test.go internal/transport/
go test -v -run '^TestEval_' ./internal/transport -race -count=1
rm internal/transport/eval_recv_buffer_compaction_test.go
```

```console
--- PASS: TestEval_RecvBufferCompaction_MultiCycleMemoryBound (0.02s)
    --- PASS: TestEval_RecvBufferCompaction_MultiCycleMemoryBound/MultiCycleBursts (0.00s)
    --- PASS: TestEval_RecvBufferCompaction_MultiCycleMemoryBound/IncrementalMessageAssembly_TieredPool (0.01s)
    --- PASS: TestEval_RecvBufferCompaction_MultiCycleMemoryBound/IncrementalMessageAssembly_257ByteFrames (0.01s)
    --- PASS: TestEval_RecvBufferCompaction_MultiCycleMemoryBound/SustainedBacklogTrailingReader (0.00s)
ok  	google.golang.org/grpc/internal/transport	1.065s
```

(all other `TestEval_*` tests also pass on this branch.)

### Reasoning

- Three-byte cycles: the first 1-byte frame goes straight to the reader in its own 1-byte allocation; the 2-byte tail is compacted, and because it is ≤ 1,024 bytes `flushCompactBuf` hands out a right-sized 2-byte `mem.Copy` and returns the 4 KiB buffer to the pool. Retained: 12,288 bytes for 12,288 payload bytes (bound 114,688).
- 1,800-byte cycles: the first 600-byte frame is retained as-is (600 bytes); the 1,200-byte tail exceeds 1,024 and is handed out inside the compaction buffer, which is a **4 KiB** allocation on this branch (`Get(4096) -> cap 4096` on both pools), not 16 KiB. Retained: 256 × (600 + 4,096) = 1,202,176 bytes (bound 1,908,736).
- The claim's hint "1,200-byte tails backed by 16-KiB allocations" does not match this branch's 4 KiB compaction buffer.

### Out-of-scope observation (disclosed, does not change the verdict)

The `INFO` lines show that if the *input* buffers are shaped like `serverHandlerTransport.HandleStreams` produces them (a short slice of a 16 KiB pooled read buffer), the first frame of each cycle is delivered to the reader before any compaction applies and keeps its 16 KiB allocation: 67 MB / 5.2 MB retained. That is the handler read-buffer sizing issue adjudicated under C2/C5 on the audited branch, it predates compaction, and it is not in the functions or fixture measurement this claim names; the claim as scoped (compaction/assembly paths with `mem.Copy`/framer-allocated frames) does not hold.

## C4

**Claim:** an early setup failure in the receive-buffer regression tests leaves a test-created server worker blocked after cleanup completes. **Branch:** `evalon/grpc-go-tr-9e4507fd`. **Verdict: CONFIRMED (both parts).**

### Inspection

`internal/transport/recv_buffer_test.go`, `TestClientTransport_TinyDataFramesSlowReader`:

- line 166 `startData := make(chan struct{})`; lines 169-171 start `go func() { serverErr <- serveTinyDataFrames(lis, numFrames, startData, dataSent) }()`;
- lines 179-191 are setup that can `t.Fatalf`: `NewHTTP2Client`, `ct.NewStream`, `stream.Header()`;
- line 194 `close(startData)` is the only release, and it comes after those;
- `serveTinyDataFrames` line 284: a bare `<-startData` — no `select`, no context, no done channel;
- deferred cleanup: `cancel()`, `lis.Close()`, `connectCancel()`, `ct.Close(...)`. None of them touches `startData`; the worker's own `defer conn.Close()` cannot run while it is parked.

### Execution

`verify/repro/c4/inject_early_failure.patch` is a test-only patch (no production code) adding an env switch `VERIFY_C4_INJECT` to the branch's test, plus a marker printed by the first-registered `defer` (so it prints after all other deferred cleanup). `verify/repro/c4/zz_verify_c4_observer_test.go` is a top-level test that runs after the grpctest suite and dumps any goroutine still inside `serveTinyDataFrames` 3 s later. `serveTinyDataFrames` itself is untouched.

- `none` — unmodified behaviour.
- `after-connect` — `t.Fatalf` right after `NewHTTP2Client` (worker has not reached the wait yet).
- `header-forced` — `stream.Header()` succeeded but is treated as failed.
- `header-real` — `ConnectOptions.MaxHeaderListSize = 1`, so the client rejects the response headers and `stream.Header()` genuinely returns an error.

```sh
cd ~/wt/c4 && bash ~/repos/grpc-go/verify/repro/c4/run_c4.sh
```

```console
=================== VERIFY_C4_INJECT=none
VERIFY-C4 all deferred cleanup of the test body has finished
VERIFY-C4 all deferred cleanup of the test body has finished
    recv_buffer_test.go:240: Retained heap for 262144 one-byte DATA frames: 265792 bytes with compaction, 15634712 bytes without
--- PASS: Test (0.83s)
    --- PASS: Test/ClientTransport_TinyDataFramesSlowReader (0.83s)
    zz_verify_c4_observer_test.go:25: VERIFY-C4 RESULT: 0 serveTinyDataFrames goroutine(s) alive 3s after suite end
--- PASS: TestZZVerifyC4_Observer (3.01s)
--- PASS: Test (0.00s)
PASS
ok  	google.golang.org/grpc/internal/transport	3.849s
=================== VERIFY_C4_INJECT=after-connect
    recv_buffer_test.go:196: VERIFY-C4 injected setup failure right after NewHTTP2Client()
VERIFY-C4 all deferred cleanup of the test body has finished
--- FAIL: Test (0.00s)
    --- FAIL: Test/ClientTransport_TinyDataFramesSlowReader (0.00s)
    zz_verify_c4_observer_test.go:25: VERIFY-C4 RESULT: 0 serveTinyDataFrames goroutine(s) alive 3s after suite end
--- PASS: TestZZVerifyC4_Observer (3.01s)
--- PASS: Test (0.00s)
FAIL
FAIL	google.golang.org/grpc/internal/transport	3.026s
FAIL
=================== VERIFY_C4_INJECT=header-forced
    recv_buffer_test.go:208: stream.Header() failed: VERIFY-C4 injected error
VERIFY-C4 all deferred cleanup of the test body has finished
    grpctest.go:45: Leaked goroutine: goroutine 11 [chan receive]:
        google.golang.org/grpc/internal/transport.serveTinyDataFrames({0xcf03e0?, 0xc0002327c0?}, 0x40000, 0xc0000a4380, 0xc0000a43f0)
        	/home/ubuntu/wt/c4/internal/transport/recv_buffer_test.go:302 +0x688
        	/home/ubuntu/wt/c4/internal/transport/recv_buffer_test.go:175 +0x31
        	/home/ubuntu/wt/c4/internal/transport/recv_buffer_test.go:174 +0x2ef
--- FAIL: Test (10.04s)
    --- FAIL: Test/ClientTransport_TinyDataFramesSlowReader (10.03s)
    zz_verify_c4_observer_test.go:22: VERIFY-C4 worker still alive after the suite finished:
        goroutine 11 [chan receive]:
        google.golang.org/grpc/internal/transport.serveTinyDataFrames({0xcf03e0?, 0xc0002327c0?}, 0x40000, 0xc0000a4380, 0xc0000a43f0)
        	/home/ubuntu/wt/c4/internal/transport/recv_buffer_test.go:302 +0x688
        	/home/ubuntu/wt/c4/internal/transport/recv_buffer_test.go:175 +0x31
        	/home/ubuntu/wt/c4/internal/transport/recv_buffer_test.go:174 +0x2ef
    zz_verify_c4_observer_test.go:25: VERIFY-C4 RESULT: 1 serveTinyDataFrames goroutine(s) alive 3s after suite end
--- PASS: TestZZVerifyC4_Observer (3.02s)
--- PASS: Test (0.00s)
FAIL
FAIL	google.golang.org/grpc/internal/transport	13.063s
FAIL
=================== VERIFY_C4_INJECT=header-real
    recv_buffer_test.go:208: stream.Header() failed: rpc error: code = Unavailable desc = connection error: desc = "error reading from server: connection error: PROTOCOL_ERROR"
VERIFY-C4 all deferred cleanup of the test body has finished
    grpctest.go:45: Leaked goroutine: goroutine 11 [chan receive]:
        google.golang.org/grpc/internal/transport.serveTinyDataFrames({0xcf03e0?, 0xc0002b27c0?}, 0x40000, 0xc0000a4380, 0xc0000a43f0)
        	/home/ubuntu/wt/c4/internal/transport/recv_buffer_test.go:302 +0x688
        	/home/ubuntu/wt/c4/internal/transport/recv_buffer_test.go:175 +0x31
        	/home/ubuntu/wt/c4/internal/transport/recv_buffer_test.go:174 +0x2ef
--- FAIL: Test (10.05s)
    --- FAIL: Test/ClientTransport_TinyDataFramesSlowReader (10.05s)
    zz_verify_c4_observer_test.go:22: VERIFY-C4 worker still alive after the suite finished:
        goroutine 11 [chan receive]:
        google.golang.org/grpc/internal/transport.serveTinyDataFrames({0xcf03e0?, 0xc0002b27c0?}, 0x40000, 0xc0000a4380, 0xc0000a43f0)
        	/home/ubuntu/wt/c4/internal/transport/recv_buffer_test.go:302 +0x688
        	/home/ubuntu/wt/c4/internal/transport/recv_buffer_test.go:175 +0x31
        	/home/ubuntu/wt/c4/internal/transport/recv_buffer_test.go:174 +0x2ef
    zz_verify_c4_observer_test.go:25: VERIFY-C4 RESULT: 1 serveTinyDataFrames goroutine(s) alive 3s after suite end
--- PASS: TestZZVerifyC4_Observer (3.02s)
--- PASS: Test (0.00s)
FAIL
FAIL	google.golang.org/grpc/internal/transport	13.075s
FAIL
```

Line 302 of the patched file is the worker's wait (the patch adds 18 lines above original line 284):

```sh
cd ~/wt/c4 && git apply ~/repos/grpc-go/verify/repro/c4/inject_early_failure.patch && sed -n '302p' internal/transport/recv_buffer_test.go; git checkout -- internal/transport/recv_buffer_test.go
```

```console
	<-startData
```

Knock-on effect, running the leaking test together with another suite test:

```sh
cd ~/wt/c4 && git apply ~/repos/grpc-go/verify/repro/c4/inject_early_failure.patch
VERIFY_C4_INJECT=header-forced go test -count=1 -v -run '^Test$/^(ClientTransport_TinyDataFramesSlowReader|RecvBuffer_TinyPayloadsMemory)$' ./internal/transport 2>&1 | grep -E "disabled for future|^\s*(---|ok|FAIL)|Leaked goroutine|stream.Header"
git checkout -- internal/transport/recv_buffer_test.go
```

```console
    recv_buffer_test.go:208: stream.Header() failed: VERIFY-C4 injected error
    grpctest.go:45: Leaked goroutine: goroutine 25 [chan receive]:
    grpctest.go:77: Goroutine leak check disabled for future tests
--- FAIL: Test (10.15s)
    --- FAIL: Test/ClientTransport_TinyDataFramesSlowReader (10.05s)
    --- PASS: Test/RecvBuffer_TinyPayloadsMemory (0.10s)
FAIL	google.golang.org/grpc/internal/transport	10.163s
```

### Part: worker wait — holds

The worker is observed parked in `chan receive` at `<-startData` 10 s (leak checker) and a further 3 s (observer) after the test body's deferred cleanup finished, including in `header-real` where the client transport and the listener were closed.

### Part: early-failure cleanup — holds, with a boundary

- A setup failure at `stream.Header()` (or anywhere after the client's HEADERS reached the worker and before line 194) exits before `close(startData)`, and deferred cleanup does not release the wait: worker leaked.
- A setup failure *before* the worker reaches the wait (`after-connect`: `NewHTTP2Client`/pre-HEADERS) does **not** leak: closing the client connection makes the worker's `ReadFrame` fail and it returns (`0 serveTinyDataFrames goroutine(s) alive`).

### Impact reasoning

Test-only, and only on a path where the test is already failing: no effect on a green run (`none` → 0 workers alive) and none on production code. When `stream.Header()` does fail, the failing test takes an extra 10 s (leak-check timeout), prints a secondary "Leaked goroutine" error that obscures the primary failure, leaves the worker and its server-side TCP connection alive for the rest of the package's test process, and grpctest switches the goroutine leak check off for all later tests in the suite (`Goroutine leak check disabled for future tests`).

## C5

**Claim:** the handler receive path retains 8 MiB of backing allocations for 32 KiB of unread payload after 512 consecutive 64-byte pooled reads. **Branch:** `grpc-go-transport-restrict-memory-overhead-perfect`. **Verdict: CONFIRMED (both parts).**

### Commands

```sh
cd ~/repos/grpc-go
cp verify/repro/c2_c5_handler_backing_retention_test.go internal/transport/zz_verify_c2_c5_test.go
go test -v -count=1 -run '^TestVerifyC2C5' ./internal/transport
GRPC_GO_EXPERIMENTAL_ENABLE_RECEIVE_BUFFER_COMPACTION=false go test -v -count=1 -run '^TestVerifyC2C5_(HandleStreamsPipeBody|RealHTTP2Handler)$/^(size=64|size=1)$' ./internal/transport
rm internal/transport/zz_verify_c2_c5_test.go
cp ~/eval/tests/eval_recv_buffer_compaction_test.go internal/transport/
go test -v -run '^TestEval_' ./internal/transport -race -count=1
rm internal/transport/eval_recv_buffer_compaction_test.go
```

`TestVerifyC2C5_HandleStreamsPipeBody` drives the delivered `serverHandlerTransport.HandleStreams` (pool: `mem.DefaultBufferPool()`, as wired by `NewServerHandlerTransport`) with N consecutive short body reads, never reads the stream, and measures distinct backing arrays held by the stream's `recvBuffer`. `TestVerifyC2C5_RealHTTP2Handler` does the same behind a real `golang.org/x/net/http2` server fed 512 legal 64-byte DATA frames by a raw HTTP/2 client. `TestVerifyC2C5_CapacityIgnoredByAccounting` isolates the accounting.

### Output (compaction enabled, the default)

```console
RESULT size=64 reads=512: queued msgs=512 (backlog=511 + channel=1) payload=32768 B | distinct backing allocations=512, total backing capacity=8388608 B (8192.0 KiB, 256x payload) | peak during run: 512 allocations / 8388608 B | puts that left tracking reset (suffixLen=0,bytes=0): 511 of 511 backlog puts | HeapAlloc delta=8566016 B
RESULT size=100 reads=512: queued msgs=512 (backlog=511 + channel=1) payload=51200 B | distinct backing allocations=512, total backing capacity=8388608 B (8192.0 KiB, 164x payload) | ... | puts that left tracking reset (suffixLen=0,bytes=0): 511 of 511 backlog puts | HeapAlloc delta=8586496 B
RESULT size=128 reads=512: queued msgs=512 (backlog=511 + channel=1) payload=65536 B | distinct backing allocations=512, total backing capacity=8388608 B (8192.0 KiB, 128x payload) | ... | puts that left tracking reset (suffixLen=0,bytes=0): 511 of 511 backlog puts | HeapAlloc delta=8619824 B
RESULT size=1 reads=512: queued msgs=512 (backlog=511 + channel=1) payload=512 B | distinct backing allocations=512, total backing capacity=8388608 B (8192.0 KiB, 16384x payload) | ... | puts that left tracking reset (suffixLen=0,bytes=0): 0 of 511 backlog puts | HeapAlloc delta=8516136 B
RESULT size=1 reads=2048: queued msgs=1024 (backlog=1023 + channel=1) payload=2048 B | distinct backing allocations=1024, total backing capacity=16764928 B (16372.0 KiB, 8186x payload) | peak during run: 1025 allocations / 16793600 B | puts that left tracking reset (suffixLen=0,bytes=0): 1 of 2047 backlog puts | HeapAlloc delta=17014352 B
recvMsgSize=56 utilizationFactor=2 compactionThreshold=58368  => for a 64-byte put: backlogHeapSize=120, utilizationFactor*bytes=128, reset=true
RESULT pooled16KiB=false: msgs=512 backlog=511 payload=32768 distinct=512 backingCap=32768 trace: [put#0 backlog=0 suffixLen=0 suffixBytes=0] [put#1 backlog=1 suffixLen=0 suffixBytes=0] ... [put#511 backlog=511 suffixLen=0 suffixBytes=0]
RESULT pooled16KiB=true: msgs=512 backlog=511 payload=32768 distinct=512 backingCap=8388608 trace: [put#0 backlog=0 suffixLen=0 suffixBytes=0] [put#1 backlog=1 suffixLen=0 suffixBytes=0] ... [put#511 backlog=511 suffixLen=0 suffixBytes=0]
RESULT real net/http2 handler, 512 DATA frames x 64 B: queued msgs=512 (backlog=511) payload=32768 B | distinct backing allocations=512, total backing capacity=8388608 B (8192.0 KiB, 256x payload) | suffixLen=0 suffixBytes=0
ok  	google.golang.org/grpc/internal/transport	1.314s
```

With `GRPC_GO_EXPERIMENTAL_ENABLE_RECEIVE_BUFFER_COMPACTION=false`:

```console
RESULT size=64 reads=512: queued msgs=512 (backlog=511 + channel=1) payload=32768 B | distinct backing allocations=512, total backing capacity=8388608 B (8192.0 KiB, 256x payload) | ...
RESULT real net/http2 handler, 512 DATA frames x 64 B: queued msgs=512 (backlog=511) payload=32768 B | distinct backing allocations=512, total backing capacity=8388608 B (8192.0 KiB, 256x payload) | suffixLen=0 suffixBytes=0
ok  	google.golang.org/grpc/internal/transport	0.518s
```

Eval fixture on the audited branch (all green, so the fixture does not detect this):

```console
--- PASS: TestEval_RecvBufferCompaction (0.03s)
    --- PASS: TestEval_RecvBufferCompaction/HandlerServerStream (0.02s)
--- PASS: TestEval_RecvBufferCompactionDisabled (0.00s)
--- PASS: TestEval_RecvBufferCompactionSkippedLargeBuffer (0.02s)
--- PASS: TestEval_RecvBufferErrorResetSafety (0.00s)
--- PASS: TestEval_RecvBufferCompaction_MixedFrames (0.00s)
--- PASS: TestEval_RecvBufferCompaction_MultiCycleMemoryBound (0.03s)
--- PASS: TestEval_RecvBufferConfiguredPoolAcquisition (0.00s)
--- PASS: TestEval_RecvBufferConfiguredPoolRecyclingSafety (0.00s)
ok  	google.golang.org/grpc/internal/transport	1.129s
```

### Part: backing-capacity accounting — holds

`compactBacklogLocked` tracks `r.buffer.Len()` only (`backlogHeapSize := uncompactedSuffixLen*recvMsgSize + uncompactedBytes`). Observed `recvMsgSize=56`: for a 64-byte input `56+64=120 <= 2*64=128`, so tracking is reset on every put (511 of 511 backlog puts ended with `suffixLen=0, suffixBytes=0`; the same holds for 100- and 128-byte reads). The trace is identical for 64-byte-capacity and 16 KiB-capacity backing, i.e. capacity has no influence on the decision.

### Part: pooled handler inputs — holds

`HandleStreams` takes `ht.bufferPool.Get(http2MaxFrameLen)` per read, reslices it with `*buf = (*buf)[:n]` and queues `mem.NewBuffer(buf, ht.bufferPool)`; nothing copies short reads into a smaller buffer. Observed: 512 distinct backing arrays × 16,384 bytes = 8,388,608 bytes (8 MiB) retained for 32,768 bytes of unread payload, both with the pipe body and with a real net/http2 server; `HeapAlloc` grew ~8.57 MB.

### Precision note

511 of the 512 messages are in `recvBuffer.backlog`, 1 is in the buffer's 1-slot delivery channel; all 512 allocations are held by the handler stream's receive buffer.

### Impact reasoning

- Affected path: servers running gRPC through `grpc.Server.ServeHTTP` (`serverHandlerTransport`); the native HTTP/2 transports allocate right-sized buffers for small frames and are not hit by this mechanism.
- Each unread short handler read pins a 16 KiB pool buffer: 256× (64 B), 164× (100 B), 128× (128 B) the unread payload. Identical with the escape hatch set to `false`, so the delivered fix does not improve this path for reads ≥ 56 bytes; growth was linear in the number of reads in every run (2,048 × 56 B → 32 MiB).
- For reads that are compacted (1 byte), up to ~1,024 16-KiB buffers (16 MiB) are held before the first compaction, since the trigger is expressed in payload + header bytes.
- Not caught by the solution's tests or by the eval fixture, whose only handler check (`HandlerServerStream`) uses 1-byte payloads and asserts backlog length, not capacity.
- No workaround found; the env toggle does not change the result.
