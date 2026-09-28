# Audit evidence — grpc-go receive-buffer compaction ("restrict memory overhead of buffering small data frames")

Audited checkout: `~/repos/grpc-go`, branch `verify/grpc-go-transport-restrict-memory-overhead-v-0089d46d`; delivered implementation commit `327a6ff9`, base `c92e9857`. Remote: `https://github.com/kaitranntt-evals/grpc-go-transport-restrict-memory-overhead`. Claim-target branches were checked out as worktrees under `/home/ubuntu/wt/<suffix>` (`git worktree add /home/ubuntu/wt/<suffix> evalon/grpc-go-tr-<suffix>`). Probe sources live in `verify/repro/`; each is tagged `//go:build ignore` and must be copied into `internal/transport/` (dropping nothing else) before running the command in its header comment. All Go probe files are package `transport`, so they see the private `recvBuffer` API.

## C1

Claim: added/modified tests contain blocking operations with no effective local deadline/timeout/cancellation. Adjudicated per branch. Test diffs were extracted with `git diff c92e9857 evalon/grpc-go-tr-<suffix> -- '*_test.go' > /home/ubuntu/evalfix/c1_<suffix>.diff`.

### Method

1. Inspect every blocking construct in the branch's test diff (channel receives, `recvBufferReader.Read`, `Stream.readTo/read/readAll`, `net.Dial`, raw HTTP/2 writes, goroutine joins/`wg.Wait`, deferred cleanup).
2. For a construct with no bound, write a controlled-stall probe that reproduces the exact construct with one message withheld (the failure path a regression or lost frame would produce) and run it with `go test -timeout 20s` (the tests' intended bound is `defaultTestTimeout = 10s`). A `panic: test timed out after 20s` with the goroutine parked in that construct confirms.
3. For branches whose reads are bounded by a `context.WithTimeout(…, defaultTestTimeout)` context, run failure-path probes of the remaining raw-socket constructs (`verify/repro/c1_refute_probe_test.go`) and the branch's own tests, and check they terminate promptly.

Common probe command (per worktree):

```sh
cp verify/repro/c1_<suffix>_stall_probe_test.go /home/ubuntu/wt/<suffix>/internal/transport/verify_c1_probe_test.go
sed -i '1,2d' /home/ubuntu/wt/<suffix>/internal/transport/verify_c1_probe_test.go   # drop the //go:build ignore line
cd /home/ubuntu/wt/<suffix> && go test -v -run '^TestVerifyC1' -timeout 20s -count=1 ./internal/transport
```

### CONFIRMED branches

**63dff0f9** — `internal/transport/receive_buffer_test.go`: `TestReceiveBufferCompactionOwnership`/`Pool` use bare `msg := <-queue.get()` loops (diff lines 210, 389, 438) and `TestReceiveBufferCompactionErrors` builds `reader := recvBufferReader{recv: &queue}` with no `ctx`/`ctxDone` (line 238), so `Read` can only return when a message arrives. Probe (`c1_63dff0f9_stall_probe_test.go`) mirrors that reader and withholds the second message:

```console
    verify_c1_probe_test.go:18: first Read: len=1 err=<nil>
    verify_c1_probe_test.go:21: second Read with the next message withheld (simulated lost frame/EOF); test's intended bound is defaultTestTimeout=10s
panic: test timed out after 20s
google.golang.org/grpc/internal/transport.(*recvBufferReader).read(0xc000085ec8, 0x1)
google.golang.org/grpc/internal/transport.(*recvBufferReader).Read(0xc000085ec8, 0xc19f7c?)
google.golang.org/grpc/internal/transport.TestVerifyC1Stall(0xc0000be700)
FAIL	google.golang.org/grpc/internal/transport	20.024s
```

**6a056399** — `recv_buffer_test.go`: helper `newTestRecvBufferStream()` (diff line 71) builds `recvBufferReader{recv: &s.buf}` with no ctx (line 75); every `s.readTo(...)` in the unit tests (lines 98, 101) is unbounded. Probe uses the branch's own helper:

```console
    verify_c1_probe_test.go:14: s.readTo(1 byte) with the frame withheld (simulated lost frame/EOF); intended bound defaultTestTimeout=10s
panic: test timed out after 20s
google.golang.org/grpc/internal/transport.(*Stream).readTo(0xc000102fc0?, {0xc000080eef, 0x1, 0x1})
google.golang.org/grpc/internal/transport.TestVerifyC1Stall(0xc000102fc0)
FAIL	google.golang.org/grpc/internal/transport	20.083s
```

**9eec450b** — `recv_buffer_test.go` `TestServerStream_ReadsMessageSplitIntoTinyFrames`: `defer lis.Close()` (line 257) is registered before `defer wg.Wait()` (line 269), so on a `t.Fatalf` failure path `wg.Wait()` runs first while the accept goroutine is still parked in `lis.Accept()`, and nothing closes the listener. Probe reproduces the defer order with a failing `net.Dial`:

```console
    verify_c1_probe_test.go:35: net.Dial() failed: dial tcp 127.0.0.1:45977: connect: connection refused -- now running deferred wg.Wait() before lis.Close()
panic: test timed out after 20s
sync.(*WaitGroup).Wait(0xc000014f60)
google.golang.org/grpc/internal/transport.TestVerifyC1Stall(0xc0000be700)
internal/poll.(*pollDesc).waitRead(...)      <- accept goroutine still in lis.Accept()
FAIL	google.golang.org/grpc/internal/transport	20.106s
```

**c70b9e5f** — `recv_buffer_test.go` `newTestRecvStream()` (line 63) sets `ctx: context.Background()` (line 65); `ctxDone` never fires, so `s.read(1)` (line 98) and friends are unbounded:

```console
    verify_c1_probe_test.go:14: s.read(1) with the frame/EOF withheld; intended bound defaultTestTimeout=10s
panic: test timed out after 20s
google.golang.org/grpc/internal/transport.(*Stream).read(0xc0001f2580, 0x1)
FAIL	google.golang.org/grpc/internal/transport	20.106s
```

**a45fad05** — `transport_test.go` `TestStreamReadManySmallFrames` (line 272) builds `recvBufferReader{ recv: &s.buf }` with no ctx (lines 283–284); `s.readTo` (lines 315, 324) is unbounded:

```console
    verify_c1_probe_test.go:16: s.readTo(1 byte) with the frame withheld; intended bound defaultTestTimeout=10s
panic: test timed out after 20s
google.golang.org/grpc/internal/transport.(*Stream).readTo(0xc0000be700?, {0xc000085eef, 0x1, 0x1})
FAIL	google.golang.org/grpc/internal/transport	20.105s
```

**19409e91** — `recv_buffer_test.go` `newRecvBufferTestStream()` (line 46) builds `recvBufferReader{recv: &s.buf}` with no ctx (line 50); `st.readAll(...)` (lines 146, 179, 186, 252, 276) is unbounded:

```console
    verify_c1_probe_test.go:14: st.readAll(1) with the frame withheld; intended bound defaultTestTimeout=10s
panic: test timed out after 20s
google.golang.org/grpc/internal/transport.(*Stream).readAll(0xc0000f8580, 0x1)
FAIL	google.golang.org/grpc/internal/transport	20.105s
```

**36805c4b** — `recv_buffer_test.go` `TestRecvBufferCompactionDoesNotRecopy` drains with a bare `m := <-queue.get()` loop (line 298) with no `select`/ctx branch. Probe runs the loop one receive past the delivered count:

```console
    verify_c1_probe_test.go:19: receive 0
    verify_c1_probe_test.go:19: receive 1
panic: test timed out after 20s
google.golang.org/grpc/internal/transport.TestVerifyC1Stall(0xc000102fc0)
FAIL	google.golang.org/grpc/internal/transport	20.059s
```

**cc649db0** — `recv_buffer_test.go` `TestReceiveBufferCompactionChunkStorage` (line 343) and `…Concurrent` (line 400) drain with bare `m := <-queue.get()`:

```console
    verify_c1_probe_test.go:19: receive 0
    verify_c1_probe_test.go:19: receive 1
panic: test timed out after 20s
FAIL	google.golang.org/grpc/internal/transport	20.105s
```

**f547726c** — `recv_buffer_test.go` `TestReceiveBufferCompactionDataFrames` receives directly from the recvBuffer channel, `m := <-stream.buf.c` (line 125), with no bound:

```console
    verify_c1_probe_test.go:15: <-stream.buf.c with the first frame not delivered to the channel (simulated regression); intended bound defaultTestTimeout=10s
panic: test timed out after 20s
FAIL	google.golang.org/grpc/internal/transport	20.105s
```

**6babb9b5** — `recv_buffer_test.go` `TestReceiveBufferCompaction_BufferOwnership` uses bare `msg := <-b.get()` (lines 205, 212) and `r := recvBufferReader{recv: &b}` with no ctx (lines 230, 278):

```console
    verify_c1_probe_test.go:21: r.Read(100) with the next frame/EOF withheld; intended bound defaultTestTimeout=10s
panic: test timed out after 20s
google.golang.org/grpc/internal/transport.(*recvBufferReader).Read(0xc000085eb0, 0xc16e8c?)
FAIL	google.golang.org/grpc/internal/transport	20.104s
```

### REFUTED branches

Inspection: on all eight branches below every `recvBufferReader` is built with `ctx`/`ctxDone` from `context.WithTimeout(context.Background(), defaultTestTimeout)` (helpers `newTestRecvStream(ctx)`, `newRecvBufferTestStream(ctx)`, `newRecvTestStream(ctx, pool)`, `newTestRecvBufferReader(ctx)`; e.g. ae28a1c4 diff lines 50–59, f0f841e2 60–69, 9dc4bb9a 47–56, bd3f54f4 81–91, fd3461ea 107–110, eb8d8e69 51–60, c25e344e 59–68), all channel waits are `select { case …: case <-ctx.Done(): }`, and integration-test reads happen only after a ctx-bounded wait established that the data plus EOF/trailers are already queued. Client transports are created with `NewHTTP2Client(ctx, ctx, …)` and streams with `NewStream(ctx, …)`. The remaining suspects — `net.Dial` without timeout (ae28a1c4, f0f841e2, bd3f54f4, fd3461ea, eb8d8e69, c25e344e), raw HTTP/2 writes without socket deadlines (≤ 32 845 one-byte DATA frames ≈ 330 KB on the wire, f0f841e2/bd3f54f4/fd3461ea/eb8d8e69/c25e344e), and `<-serverDone`/`<-readerDone` joins after `ct.Close()`/`mconn.Close()` (9dc4bb9a line 349, c25e344e lines 416/585, bd3f54f4 via `setupRSTStreamOnEOSTest`'s `waitForServer`, whose server goroutine `select`s on `ctx.Done()`) — were probed on each worktree with `verify/repro/c1_refute_probe_test.go`:

```sh
cp verify/repro/c1_refute_probe_test.go /home/ubuntu/wt/<suffix>/internal/transport/ && sed -i '1,2d' /home/ubuntu/wt/<suffix>/internal/transport/c1_refute_probe_test.go
cd /home/ubuntu/wt/<suffix> && go test -v -run '^TestVerifyC1Refute' -timeout 60s -count=1 ./internal/transport
```

Output on ae28a1c4, f0f841e2, 9dc4bb9a, bd3f54f4, fd3461ea, eb8d8e69, c25e344e (identical shape; 9dc4bb9a shown):

```console
    verify_c1_refute_probe_test.go:21: Dial to listening-but-never-accepting peer: err=<nil> after 129.342µs
    verify_c1_refute_probe_test.go:29: Dial to closed listener: err=dial tcp 127.0.0.1:45833: connect: connection refused after 48.417µs
--- PASS: TestVerifyC1Refute_DialToLoopback (0.00s)
    verify_c1_refute_probe_test.go:59: 8192 one-byte DATA frames (81920 wire bytes) written to a never-reading peer in 3.423522ms (cumulative)
    verify_c1_refute_probe_test.go:59: 10000 one-byte DATA frames (100000 wire bytes) written to a never-reading peer in 4.123997ms (cumulative)
    verify_c1_refute_probe_test.go:59: 32768 one-byte DATA frames (327680 wire bytes) written to a never-reading peer in 13.473742ms (cumulative)
    verify_c1_refute_probe_test.go:59: 32845 one-byte DATA frames (328450 wire bytes) written to a never-reading peer in 13.370393ms (cumulative)
    verify_c1_refute_probe_test.go:59: 65536 one-byte DATA frames (655360 wire bytes) written to a never-reading peer in 26.04558ms (cumulative)
--- PASS: TestVerifyC1Refute_RawWritesToNonReadingPeer (0.06s)
    verify_c1_refute_probe_test.go:98: <-serverDone returned 78.187µs after closing the client conn
--- PASS: TestVerifyC1Refute_ReadFrameUnblocksOnPeerClose (0.10s)
ok  	google.golang.org/grpc/internal/transport	0.168s
```

Loopback connection establishment cannot stall (immediate SYN-ACK or RST), the largest raw write volume any of these tests produce fits in the loopback socket buffers even against a peer that never reads, and a `framer.ReadFrame()` loop exits within ~100 µs of the peer closing, which is what every `<-serverDone`/`<-readerDone` join depends on. The branches' own added tests all pass well inside the bound:

```console
######## ae28a1c4  ok  google.golang.org/grpc/internal/transport 0.195s  wall=0.63s
######## f0f841e2  ok  google.golang.org/grpc/internal/transport 0.195s  wall=0.59s
######## 9dc4bb9a  ok  google.golang.org/grpc/internal/transport 0.190s  wall=0.60s
######## bd3f54f4  ok  google.golang.org/grpc/internal/transport 0.196s  wall=0.61s
######## fd3461ea  ok  google.golang.org/grpc/internal/transport 0.190s  wall=0.57s
######## eb8d8e69  ok  google.golang.org/grpc/internal/transport 0.191s  wall=0.56s
######## c25e344e  ok  google.golang.org/grpc/internal/transport 0.191s  wall=0.56s
```

**2f986a40** — suspected: a test joins a writer goroutine without a bound. `recv_buffer_test.go` `TestReceiveBufferCompactionConcurrent` does `defer func() { <-done }()` where the goroutine only calls `b.put(...)` (non-blocking: `put` never waits on the reader; it appends to the backlog). All reads use `recvBufferReader{ctx: ctx, ctxDone: ctx.Done(), …}` with a `defaultTestTimeout` context (lines 77, 179, 272). Probe `c1_2f986a40_stall_probe_test.go` mirrors the join on the failure path where the reader bails out immediately:

```console
    verify_c1_probe_test.go:27: join <-done returned after 2.808744ms (writer only performs non-blocking put calls; nothing a stalled reader can block)
--- PASS: TestVerifyC1Join (0.00s)
ok  	google.golang.org/grpc/internal/transport	0.007s
```

## C2

Claim: compaction on branch `evalon/grpc-go-tr-2f986a40` allocates consolidated buffers from the global default pool, not the transport's configured pool.

Source (`/home/ubuntu/wt/2f986a40/internal/transport/transport.go`):

```console
133:		b.pending = mem.DefaultBufferPool().Get(recvBufferCompactionSize)
144:		b.backlog = append(b.backlog, recvMsg{buffer: mem.NewBuffer(b.pending, mem.DefaultBufferPool())})
```

while the transport carries its configured pool (`http2_server.go:128 bufferPool mem.BufferPool`, `:277 bufferPool: config.BufferPool`). Probe `verify/repro/c2_2f986a40_pool_probe_test.go` builds a real `http2Server` with a counting pool as `ServerConfig.BufferPool`, feeds 16 384 one-byte DATA frames through `handleData` on a stream nobody reads, then counts pool calls:

```sh
cp verify/repro/c2_2f986a40_pool_probe_test.go /home/ubuntu/wt/2f986a40/internal/transport/ && sed -i '1,2d' /home/ubuntu/wt/2f986a40/internal/transport/c2_2f986a40_pool_probe_test.go
cd /home/ubuntu/wt/2f986a40 && go test -v -run '^TestVerifyC2' -count=1 ./internal/transport
```

```console
    verify_c2_probe_test.go:77: queued 16384 one-byte DATA frames: compacted chunks in backlog=3 (cap 12288 bytes) + pending cap=4096
    verify_c2_probe_test.go:78: transport-configured pool (http2Server.bufferPool): Get calls=0 Put calls=0
    verify_c2_probe_test.go:79: framer pool (source of DATA frame buffers): Get calls=0 Put calls=0
    verify_c2_probe_test.go:81: RESULT: 3 compaction chunks were allocated but none came from the transport's configured pool
--- PASS: TestVerifyC2_CompactionPoolSelection (0.00s)
ok  	google.golang.org/grpc/internal/transport	0.007s
```

Impact: a user who configures `grpc.WithSharedWriteBuffer`/custom `mem.BufferPool` (e.g. for accounting or a no-op pool) still gets 4 KiB chunks from the global default pool for every compacted batch; the configured pool is silently bypassed for the receive path.

## C3

Claim: on the audited branch, suffix compaction copies an efficiently stored 16 KiB frame that follows 1 024 queued one-byte payloads.

Source (`internal/transport/transport.go`): `recvMsgSize = 56`, `compactionThreshold = imem.BufferPoolingThreshold*(recvMsgSize+1) = 58368`, `utilizationFactor = 2`. `compactBacklogLocked` (lines 140–195) adds the new message to the suffix (`uncompactedSuffixLen++`, `uncompactedBytes += r.buffer.Len()`), skips only if `backlogHeapSize <= 2*uncompactedBytes` or `<= compactionThreshold`, and otherwise copies **every** suffix entry, including the just-appended large one, into one pooled buffer (`start += copy((*newBuf)[start:], m.buffer.ReadOnlyData()); m.buffer.Free()`).

Probe `verify/repro/c3_c4_c5_c8_probe_test.go` (`TestVerifyC3_LargeFrameAfterSmallFrames`), run on the audited branch:

```sh
cp verify/repro/c3_c4_c5_c8_probe_test.go internal/transport/ && sed -i '1,2d' internal/transport/c3_c4_c5_c8_probe_test.go
go test -v -run '^TestVerifyC3' -count=1 ./internal/transport
```

```console
    verify_probe_test.go:31: before large: backlog=1024 suffixLen=1024 suffixBytes=1024 heapEstimate=58368 compactionThreshold=58368 recvMsgSize=56
    verify_probe_test.go:40: projected heapEstimate with large frame=74808 (threshold 58368, utilization check limit 34816)
    verify_probe_test.go:43: after large: backlog=1 lastLen=17408 lastPtrSameAsLarge=false large.frees=1
    verify_probe_test.go:48: RESULT: large frame was COPIED into consolidated storage
--- PASS: TestVerifyC3_LargeFrameAfterSmallFrames (0.00s)
```

1 024 one-byte messages sit exactly at the threshold (58 368); the 16 KiB frame pushes the estimate to 74 808 > 58 368 while `74808 > 2*17408 = 34816`, so compaction fires and the 16 KiB payload is memcpy'd into a new 17 408-byte buffer and its original pooled buffer freed (`frees=1`, pointer changed). Impact: an extra 16 KiB copy + a pool round-trip for a frame that was already efficiently stored, on the ordinary path of a slow reader receiving a burst of tiny frames followed by a normal-size frame.

## C4

Claim: `recvBuffer.put()` panics when a payload-free terminal message arrives after the buffer already holds an error (existing-error path calls `Free` on a nil buffer).

Source (`internal/transport/transport.go` lines 118–128):

```go
	if b.err != nil {
		// drop the buffer on the floor. ...
		r.buffer.Free()
		return
	}
```

`r.buffer` is a `mem.Buffer` interface; for `recvMsg{err: …}` it is nil. Probe `TestVerifyC4_DoubleErrorPut` (`verify/repro/c3_c4_c5_c8_probe_test.go`):

```sh
go test -v -run '^TestVerifyC4' -count=1 ./internal/transport
```

```console
    verify_probe_test.go:81: RESULT: second put(recvMsg{err}) PANICKED: runtime error: invalid memory address or nil pointer dereference
--- PASS: TestVerifyC4_DoubleErrorPut (0.00s)
```

Context: the same `r.buffer.Free()` line exists in the base (`git show c92e9857:internal/transport/transport.go`, line 89), so this is pre-existing rather than introduced; production callers (`http2_client.go:823/983` inside `closeStream`, which is guarded by `swapState(streamDone)`, `http2_server.go:827`, `handler_server.go:449`) currently emit at most one terminal message per stream, so the panic is latent.

## C5

Claim: compaction replaces a terminal message carrying a non-nil buffer with a data-only message, discarding its error.

Source: `compactBacklogLocked` (lines 140–195) only excludes messages with `r.buffer == nil`; a message with both `buffer` and `err` is counted in the suffix and replaced by `recvMsg{buffer: mem.NewBuffer(newBuf, b.bufPool)}` — no `err`. `recvBufferReader.read` learns of the terminal error only from the `recvMsg.err` it dequeues, not from `b.err`. Probe `TestVerifyC5_ErrorWithBufferCompacted`:

```sh
go test -v -run '^TestVerifyC5' -count=1 ./internal/transport
```

```console
    verify_probe_test.go:98: before: backlog=1024 suffixLen=1024
    verify_probe_test.go:100: after: backlog=1 b.err=EOF
    verify_probe_test.go:102: backlog[0]: len=1025 err=<nil>
    verify_probe_test.go:120: consumed 1026 bytes (want 1026 incl. the byte attached to the error message); final read error = rpc error: code = DeadlineExceeded desc = context deadline exceeded
    verify_probe_test.go:124: RESULT: terminal io.EOF LOST; reader observed rpc error: code = DeadlineExceeded desc = context deadline exceeded instead
--- PASS: TestVerifyC5_ErrorWithBufferCompacted (2.01s)
```

The queued `recvMsg{buffer: [2], err: io.EOF}` was merged into a 1 025-byte data-only entry; the reader consumed all bytes as ordinary data and then blocked until its 2 s context expired instead of receiving `io.EOF`. Impact: latent — no current transport caller emits a message with both fields set (see C4 caller list), but any future caller that does would silently lose the stream's terminal status and hang the reader until its context deadline.

## C6

Claim: `newTestRecvStream` in `internal/transport/recv_buffer_test.go` (branch `evalon/grpc-go-tr-c70b9e5f`) uses bare `context.Background()` prohibited by `scripts/vet.sh`.

```sh
cd /home/ubuntu/wt/c70b9e5f && sed -n 55,60p internal/transport/recv_buffer_test.go && bash verify/repro/c6_c70b9e5f_vet_context_rule.sh
```

```console
func newTestRecvStream() *Stream {
	s := &Stream{
		ctx:           context.Background(),
		readRequester: &fakeReadRequester{},
	}
== scripts/vet.sh rule:
# - Ensure all context usages are done with timeout.
# Context tests under benchmark are excluded as they are testing the performance of context.Background() and context.TODO().
git grep -e 'context.Background()' --or -e 'context.TODO()' -- "*_test.go" | grep -v "benchmark/primitives/context_test.go" | grep -v 'context.WithTimeout(' | not grep -v 'context.WithCancel('
== violations:
internal/transport/recv_buffer_test.go:		ctx:           context.Background(),
RESULT: vet.sh context rule FAILS
exit=1
```

The same pipeline on the base worktree (`/home/ubuntu/wt/base`, c92e9857) prints nothing (`exit=1` from the empty grep), so the violation is introduced by this branch. Impact: `scripts/vet.sh` (CI's vet step) fails on this branch; and the bare context is also what makes the branch's reads unbounded (C1/c70b9e5f).

## C7

Claim: on `evalon/grpc-go-tr-561ce78f`, after >16 KiB is queued, each alternating one-byte + 2 KiB frame pair retains ~18 KiB because a nearly empty 16 KiB tail is sealed per pair.

Source (`/home/ubuntu/wt/561ce78f/internal/transport/transport.go` lines 145–172): `compact()` allocates `b.tail = make([]byte, 0, min(max(b.backlogBytes, minCompactionChunkSize), maxCompactionChunkSize))` — 16 KiB once `backlogBytes >= 16 KiB` — and `sealTail()` publishes the tail as-is when a non-compacted (2 KiB pooled) frame arrives, so a 1-byte tail with 16 KiB capacity is retained per pair. Probe `verify/repro/c7_561ce78f_alternating_probe_test.go`:

```sh
cp verify/repro/c7_561ce78f_alternating_probe_test.go /home/ubuntu/wt/561ce78f/internal/transport/ && sed -i '1,2d' /home/ubuntu/wt/561ce78f/internal/transport/c7_561ce78f_alternating_probe_test.go
cd /home/ubuntu/wt/561ce78f && go test -v -run '^TestVerifyC7' -count=1 ./internal/transport
```

```console
    verify_c7_probe_test.go:31: after 20 KiB large frame: backlogBytes=20480 retainedCap=32768
    verify_c7_probe_test.go:38: pair  1: entries=3 payloadBytes=22529 retainedCap=53248 deltaThisPair=20480 tailCap=0 tailLen=0 ratio=2.36x
    verify_c7_probe_test.go:38: pair  2: entries=5 payloadBytes=24578 retainedCap=73728 deltaThisPair=20480 tailCap=0 tailLen=0 ratio=3.00x
    verify_c7_probe_test.go:38: pair  4: entries=9 payloadBytes=28676 retainedCap=114688 deltaThisPair=20480 tailCap=0 tailLen=0 ratio=4.00x
    verify_c7_probe_test.go:38: pair  8: entries=17 payloadBytes=36872 retainedCap=196608 deltaThisPair=20480 tailCap=0 tailLen=0 ratio=5.33x
    verify_c7_probe_test.go:38: pair 12: entries=25 payloadBytes=45068 retainedCap=278528 deltaThisPair=20480 tailCap=0 tailLen=0 ratio=6.18x
--- PASS: TestVerifyC7_AlternatingTinyAnd2KiBFrames (0.00s)
```

Each pair adds exactly 20 480 bytes of unique backing capacity (16 384-byte sealed tail holding 1 byte + 4 096-byte pooled buffer holding 2 048 bytes) for 2 049 payload bytes; retained/payload ratio grows without bound (6.18× after 12 pairs). Impact: any slow-reading stream whose peer interleaves tiny frames with ≥1 KiB frames (e.g. gRPC 5-byte message headers sent as their own DATA frame before each 2 KiB message) retains ~10× the payload; a workaround is disabling compaction via `GRPC_EXPERIMENTAL_ENABLE_RECEIVE_BUFFER_COMPACTION=false`-style envconfig if exposed on that branch.

## C8

Claim: backing storage + queue metadata (excluding pool caches) are not bounded by unread payload plus fixed slack across fragmentation, partial reads, full drains and refills (audited branch).

Probe `TestVerifyC8_RetainedStorageBounded` (`verify/repro/c3_c4_c5_c8_probe_test.go`) measures unique backing capacity (`&data[:cap][0]` de-duplicated), entries, `cap(backlog)*recvMsgSize`, and `runtime.MemStats.HeapAlloc` delta after `runtime.GC()` for 256 KiB payload split into 1-, 3- and 7-byte frames, over 4 fill → partial drain → full drain cycles, then at increasing volumes:

```sh
go test -v -run '^TestVerifyC8' -count=1 ./internal/transport
```

```console
    verify_probe_test.go:183: frag=3 cycle=0 after fill(262144 bytes unread): entries=350 uniqueCap=361229 metaCap=66472 heapDelta=426744
    verify_probe_test.go:186: frag=3 cycle=0 after partial drain (174763 unread): entries=319 uniqueCap=234253 metaCap=84280 heapDelta=428928
    verify_probe_test.go:189: frag=3 cycle=0 after full drain (0 unread): entries=0 uniqueCap=0 metaCap=66472 heapDelta=302880
    verify_probe_test.go:183: frag=3 cycle=3 after fill(262144 bytes unread): entries=350 uniqueCap=361229 metaCap=86016 heapDelta=426744
    verify_probe_test.go:189: frag=3 cycle=3 after full drain (0 unread): entries=0 uniqueCap=0 metaCap=66472 heapDelta=302880
    verify_probe_test.go:183: frag=7 cycle=0 after fill(262144 bytes unread): entries=410 uniqueCap=657937 metaCap=57288 heapDelta=704312
    verify_probe_test.go:186: frag=7 cycle=0 after partial drain (174763 unread): entries=395 uniqueCap=412177 metaCap=56448 heapDelta=706880
    verify_probe_test.go:189: frag=7 cycle=3 after full drain (0 unread): entries=0 uniqueCap=0 metaCap=34384 heapDelta=482112
    verify_probe_test.go:201: volume=65536 one-byte frames: entries=1024 uniqueCap=259008 (3.95x payload) metaCap=86016
    verify_probe_test.go:201: volume=262144 one-byte frames: entries=1024 uniqueCap=1045248 (3.99x payload) metaCap=86016
    verify_probe_test.go:201: volume=1048576 one-byte frames: entries=1024 uniqueCap=4190208 (4.00x payload) metaCap=129024
--- PASS: TestVerifyC8_RetainedStorageBounded (0.17s)
```

Observed bound: unique live capacity ≤ ~4× unread payload (pooled-chunk rounding of 256-byte consolidated payloads into 1 KiB pool tiers is the worst case, converging to 4.00×), independent of volume (64 KiB → 1 MiB) and of previously drained volume (cycle 0 and cycle 3 identical); after a full drain unique capacity is 0 and only `cap(backlog)` metadata (≤ 129 024 B, i.e. ≤ 2 304 `recvMsg` slots, constant across cycles) remains. The residual `heapDelta` after a full drain (~300–480 KB) does not grow across cycles and is attributable to `sync.Pool` caches, which the claim excludes. No component scaling with drained volume was found by code inspection either (`compactBacklogLocked` only ever references the suffix `[len(backlog)-uncompactedSuffixLen, len(backlog))`; `load()` re-slices `backlog[1:]`).

## C9

Claim: the solution's added/modified transport regression tests do not detect the unfixed base behaviour via an executed buffering assertion.

Delivered tests (`internal/transport/transport_test.go`, from `git diff c92e9857 327a6ff9 -- internal/transport/transport_test.go`): `TestRecvBufferCompaction` (line 4244), `TestRecvBufferErrorResetsCounters` (4327), `TestRecvBufferCompactionSkippedLargeBuffer` (4388), `TestRecvBufferCompactionDisabled` (4442). Base worktree `/home/ubuntu/wt/base` (c92e9857) received only that test diff plus a compatibility shim in `transport.go` (fields `uncompactedSuffixLen`, `uncompactedBytes` never updated; `init(_ mem.BufferPool)` accepting and ignoring the pool; `var recvMsgSize = int(unsafe.Sizeof(recvMsg{}))`) — no buffering assertion was altered. The eval fixture `eval_recv_buffer_compaction_test.go` (from `eval_tests.zip`, copied byte-exact into `internal/transport/`) was also run.

```sh
cd /home/ubuntu/wt/base && go test -v -run 'Test/.*RecvBuffer|.*RecvBuffer' -timeout 120s -count=1 ./internal/transport 2>&1 | grep -E 'RecvBuffer|^(ok|FAIL|PASS)|_test.go:[0-9]+:'
```

```console
    eval_recv_buffer_compaction_test.go:85: Got backlog length 1025 after compaction, want <= 64
--- FAIL: TestEval_RecvBufferCompaction (0.00s)
    eval_recv_buffer_compaction_test.go:119: Got backlog length 1025, want <= 64 (compaction enabled)
--- FAIL: TestEval_RecvBufferCompactionDisabled (0.00s)
--- PASS: TestEval_RecvBufferCompactionSkippedLargeBuffer (0.00s)
--- PASS: TestEval_RecvBufferErrorResetSafety (0.00s)
    eval_recv_buffer_compaction_test.go:270: Got backlog length 1073 after mixed frame compaction, want <= 134
--- FAIL: TestEval_RecvBufferCompaction_MixedFrames (0.00s)
    eval_recv_buffer_compaction_test.go:310: Cycle 0: backlog length 1123 exceeded bound 512
--- FAIL: TestEval_RecvBufferCompaction_MultiCycleMemoryBound (0.00s)
    transport_test.go:4288: Got backlog length 1025 after compaction, want 1
    transport_test.go:4340: Got uncompactedSuffixLen 0, want 2
    --- FAIL: Test/RecvBufferCompaction (0.00s)
    --- PASS: Test/RecvBufferCompactionDisabled (0.00s)
    --- PASS: Test/RecvBufferCompactionSkippedLargeBuffer (0.00s)
    --- FAIL: Test/RecvBufferErrorResetsCounters (0.00s)
FAIL	google.golang.org/grpc/internal/transport	0.009s
```

Same command on the delivered checkout (`~/repos/grpc-go`, with the fixture copied in):

```console
--- PASS: TestEval_RecvBufferCompaction (0.00s)
--- PASS: TestEval_RecvBufferCompactionDisabled (0.00s)
--- PASS: TestEval_RecvBufferCompactionSkippedLargeBuffer (0.00s)
--- PASS: TestEval_RecvBufferErrorResetSafety (0.00s)
--- PASS: TestEval_RecvBufferCompaction_MixedFrames (0.00s)
--- PASS: TestEval_RecvBufferCompaction_MultiCycleMemoryBound (0.00s)
    --- PASS: Test/RecvBufferCompaction (0.00s)
    --- PASS: Test/RecvBufferCompactionDisabled (0.00s)
    --- PASS: Test/RecvBufferCompactionSkippedLargeBuffer (0.00s)
    --- PASS: Test/RecvBufferErrorResetsCounters (0.00s)
ok  	google.golang.org/grpc/internal/transport	2.350s
```

The delivered `TestRecvBufferCompaction` buffering assertion (`transport_test.go:4288: Got backlog length 1025 after compaction, want 1`) executes and fails on base and passes on the delivered code, as do 4 of the 6 fixture tests.

## C10

Claim: the delivered implementation fails `internal/envconfig`, `internal/mem`, `mem` under the race detector with `-cpu 1,4`.

```sh
cd ~/repos/grpc-go && go test -race -cpu 1,4 -timeout 7m ./internal/envconfig ./internal/mem ./mem -count=1
```

Run 1 (`/home/ubuntu/evalfix/c10_run1.log`) and run 2 (`c10_run2.log`), both:

```console
ok  	google.golang.org/grpc/internal/envconfig	1.020s
ok  	google.golang.org/grpc/internal/mem	1.016s
ok  	google.golang.org/grpc/mem	1.060s
```

No failures, races, panics or timeouts.
