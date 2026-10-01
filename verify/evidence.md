Observations for run `v-34edfcaa`. Everything below was produced by `verify/run.sh` on go1.25.7 linux/amd64 (8 CPUs); the raw `go test` commands are repeated in each section so a section can be replayed on its own.

```sh
cd ~/repos/grpc-go
git checkout verify/grpc-go-transport-restrict-memory-overhead-v-34edfcaa
bash verify/run.sh            # all claims
bash verify/run.sh C4 C6      # selected claims
```

`run.sh` fetches each claim's target branch from the claim's repository into a `claims` remote, checks it out as a detached worktree under `~/wt/<id>`, copies the probe in as `internal/transport/zz_verify_*_test.go` (build tag `verify`), runs it, and removes the probe / reverts any mutation patch again. Production code on every branch is untouched. Output lines below are verbatim apart from stripped `file.go:NN:` prefixes and timings; a leading count is `sort | uniq -c`.

## C1

Target: [`evalon/grpc-go-tr-fbdde71c`](https://github.com/kaitranntt-evals/grpc-go-transport-restrict-memory-overhead/tree/evalon/grpc-go-tr-fbdde71c) at `2af4e3ca`. Replay: `bash verify/run.sh C1`.

The branch adds one test file with three tests. The raw commands, run in a worktree of the branch:

```sh
cp verify/repro/c1_read_then_put_test.go <wt>/internal/transport/zz_verify_c1_test.go
GOMAXPROCS=1 go test -tags verify -count=10 -v -run '^Test$/^VerifyC1_ConcurrentReadsSchedule$' ./internal/transport
go test -tags verify -count=1 -v -run '^Test$/^(VerifyC1_SequentialReadThenPut|RecvBuffer)' ./internal/transport
git apply verify/repro/c1_stale_tail_mutation.patch
go test -tags verify -count=1 -v -run '^Test$/^VerifyC1_SequentialReadThenPut$' ./internal/transport
rm internal/transport/zz_verify_c1_test.go
GOMAXPROCS=1 go test -count=30 -v -run '^Test$/^RecvBuffer' ./internal/transport   # also GOMAXPROCS=2 and default
git checkout -- internal/transport
```

```console
>>> C1 @ 2af4e3ca transport: compact small backlogged receive buffers
>> tests added/changed by the branch
 internal/transport/recv_buffer_test.go | 298 +++++++++++++++++++++++++++++++++
 1 file changed, 298 insertions(+)
41:func (s) TestRecvBufferTinyFramesMemory(t *testing.T) {
160:func (s) TestRecvBufferCompaction(t *testing.T) {
249:func (s) TestRecvBufferCompactionConcurrentReads(t *testing.T) {
>> every producer/consumer coordination point and every put/read call in that file
95:					handle(df)
101:				streams[i].buf.put(recvMsg{err: io.EOF})
123:					buf, err := r.Read(257)
199:				q.put(recvMsg{buffer: buf})
209:			q.put(recvMsg{buffer: mem.NewBuffer(pool.Get(1), pool), err: terminalErr})
210:			q.put(recvMsg{buffer: mem.NewBuffer(pool.Get(1), pool)})
211:			q.put(recvMsg{err: io.EOF})
217:				buf, err := r.Read(32 * 1024)
239:			if _, err := r.Read(1); err != terminalErr {
260:	ready, done := make(chan struct{}), make(chan struct{})
262:		defer close(done)
264:			q.put(recvMsg{buffer: mem.SliceBuffer{v}})
266:				close(ready)
269:		q.put(recvMsg{err: io.EOF})
271:	defer func() { <-done }()
272:	<-ready
278:		n, err := r.ReadMessageHeader(header)
283:			t.Fatalf("ReadMessageHeader() failed: %v", err)
286:		buf, err := r.Read(17)
>> unmutated branch: where is the producer when TestRecvBufferCompactionConcurrentReads' consumer issues its first read? GOMAXPROCS=1 x10
      1 SCHEDULE putsBeforeFirstRead=33045/65536 putsAfterFirstRead=32491 eofPutBeforeFirstRead=false
      1 SCHEDULE putsBeforeFirstRead=42337/65536 putsAfterFirstRead=23199 eofPutBeforeFirstRead=false
      8 SCHEDULE putsBeforeFirstRead=65536/65536 putsAfterFirstRead=0 eofPutBeforeFirstRead=true
      1 ok  	google.golang.org/grpc/internal/transport
>> unmutated branch: where is the producer when TestRecvBufferCompactionConcurrentReads' consumer issues its first read? GOMAXPROCS=default x10
      1 SCHEDULE putsBeforeFirstRead=1970/65536 putsAfterFirstRead=63566 eofPutBeforeFirstRead=false
      1 SCHEDULE putsBeforeFirstRead=2307/65536 putsAfterFirstRead=63229 eofPutBeforeFirstRead=false
      1 SCHEDULE putsBeforeFirstRead=2422/65536 putsAfterFirstRead=63114 eofPutBeforeFirstRead=false
      1 SCHEDULE putsBeforeFirstRead=2560/65536 putsAfterFirstRead=62976 eofPutBeforeFirstRead=false
      1 SCHEDULE putsBeforeFirstRead=2570/65536 putsAfterFirstRead=62966 eofPutBeforeFirstRead=false
      1 SCHEDULE putsBeforeFirstRead=2740/65536 putsAfterFirstRead=62796 eofPutBeforeFirstRead=false
      1 SCHEDULE putsBeforeFirstRead=4183/65536 putsAfterFirstRead=61353 eofPutBeforeFirstRead=false
      1 SCHEDULE putsBeforeFirstRead=4453/65536 putsAfterFirstRead=61083 eofPutBeforeFirstRead=false
      1 SCHEDULE putsBeforeFirstRead=4830/65536 putsAfterFirstRead=60706 eofPutBeforeFirstRead=false
      1 SCHEDULE putsBeforeFirstRead=6235/65536 putsAfterFirstRead=59301 eofPutBeforeFirstRead=false
      1 ok  	google.golang.org/grpc/internal/transport
>> unmutated branch: sequential read-then-put probe + the branch's own tests
    --- PASS: Test/RecvBufferCompaction
    --- PASS: Test/RecvBufferCompactionConcurrentReads
    --- PASS: Test/RecvBufferTinyFramesMemory
    --- PASS: Test/VerifyC1_SequentialReadThenPut
ok  	google.golang.org/grpc/internal/transport
>> apply mutation: put() appends to a stale tail chunk after the reader consumed it (repro/c1_stale_tail_mutation.patch)
 internal/transport/transport.go | 11 +++++++----
 1 file changed, 7 insertions(+), 4 deletions(-)
>> mutated: sequential read-then-put probe
payload across intermediate reads = "abc", want "abcd"
    --- FAIL: Test/VerifyC1_SequentialReadThenPut
FAIL
FAIL	google.golang.org/grpc/internal/transport
FAIL
>> mutated: the branch's own tests, unmodified, GOMAXPROCS=1 x30
     30     --- PASS: Test/RecvBufferCompaction
     30     --- PASS: Test/RecvBufferCompactionConcurrentReads
     30     --- PASS: Test/RecvBufferTinyFramesMemory
      1 ok  	google.golang.org/grpc/internal/transport
>> mutated: the branch's own tests, unmodified, GOMAXPROCS=2 x30
     30     --- FAIL: Test/RecvBufferCompactionConcurrentReads
     30     --- PASS: Test/RecvBufferCompaction
     30     --- PASS: Test/RecvBufferTinyFramesMemory
      2 FAIL
      1 FAIL	google.golang.org/grpc/internal/transport
>> mutated: the branch's own tests, unmodified, GOMAXPROCS=default x30
     30     --- FAIL: Test/RecvBufferCompactionConcurrentReads
     30     --- PASS: Test/RecvBufferCompaction
     30     --- PASS: Test/RecvBufferTinyFramesMemory
      2 FAIL
      1 FAIL	google.golang.org/grpc/internal/transport
```

What the listing of coordination points shows, test by test:

- `TestRecvBufferTinyFramesMemory` (lines 41-137): every DATA frame is handled (line 95) and `io.EOF` is put (line 101) before the first `Read` (line 123). No read precedes any put.
- `TestRecvBufferCompaction` (lines 160-244): every `put`, including the terminal error (lines 199-211), happens before the first `Read` (line 217). No read precedes any put.
- `TestRecvBufferCompactionConcurrentReads` (lines 249-298): the only synchronization is `ready`, closed by the producer after its 1025th put (line 266); the consumer waits for it (line 272) and the producer never waits for the consumer. Nothing orders a read before the remaining 64511 puts or before the `io.EOF` put.

Observed consequences:

- `VerifyC1_ConcurrentReadsSchedule` is that third test with two counters added. At `GOMAXPROCS=1`, 8 of 10 runs had the producer finish all 65536 puts **and** the `io.EOF` put before the consumer's first read (`putsAfterFirstRead=0 eofPutBeforeFirstRead=true`), i.e. the test degenerated to put-everything-then-read. On 8 CPUs the first read happened after about 2000-6000 puts.
- `verify/repro/c1_stale_tail_mutation.patch` injects a bug that only manifests when data is put after a read has consumed the current chunk (the buffer keeps appending to the already-delivered chunk, so later bytes are lost). The added sequential probe `VerifyC1_SequentialReadThenPut` (put a, put b, read, read, put c, put d, put EOF, read to EOF; asserts the payload) passes on the branch as delivered and fails on the mutant with `payload across intermediate reads = "abc", want "abcd"`.
- With the mutation applied and the branch's three tests unmodified: 30/30 passes for all three at `GOMAXPROCS=1`; at `GOMAXPROCS=2` and default `TestRecvBufferCompactionConcurrentReads` fails 30/30. The two sequential tests pass in every configuration.

Impact reasoning: the production code on this branch handled put-after-read correctly in every run (the sequential probe passes unmutated), so this is a coverage gap, not a product defect. The gap is that the read-then-more-data boundary and the read-then-terminal boundary are only exercised when the scheduler happens to interleave the two goroutines; on a single-CPU runner a regression there ships green, as the mutant shows.

## C2

Target: [`evalon/grpc-go-tr-5acb5ef2`](https://github.com/kaitranntt-evals/grpc-go-transport-restrict-memory-overhead/tree/evalon/grpc-go-tr-5acb5ef2) at `bd96b35f`. Replay: `bash verify/run.sh C2`.

```sh
cp verify/probes/c2_env_optout_probe_test.go <wt>/internal/transport/zz_verify_c2_test.go
GRPC_GO_EXPERIMENTAL_ENABLE_RECEIVE_BUFFER_COMPACTION=false go test -tags verify -race -count=1 -v -run '^Test$/^VerifyC2_' ./internal/transport
env -u GRPC_GO_EXPERIMENTAL_ENABLE_RECEIVE_BUFFER_COMPACTION go test -tags verify -race -count=1 -v -run '^Test$/^VerifyC2_' ./internal/transport
rm internal/transport/zz_verify_c2_test.go
cp ~/eval/tests/eval_recv_buffer_compaction_test.go <wt>/internal/transport/
GRPC_GO_EXPERIMENTAL_ENABLE_RECEIVE_BUFFER_COMPACTION=false go test -v -run '^TestEval_RecvBufferCompactionDisabled$' google.golang.org/grpc/internal/transport -race -count=1
```

The probe starts a real `http2Server` and a real `http2Client` over TCP, sends 3000 one-byte DATA frames in each direction to a stream whose application is not reading, then inspects the `recvBuffer` that production code created for that stream (`NewHTTP2Client` -> `newStream`, `NewServerTransport` -> `operateHeaders`, and `NewServerHandlerTransport` -> `HandleStreams`), and finally reads everything back.

```console
>>> C2 @ bd96b35f chore: apply eval changes
>> GRPC_GO_EXPERIMENTAL_ENABLE_RECEIVE_BUFFER_COMPACTION=false: production-created client/server streams
process env GRPC_GO_EXPERIMENTAL_ENABLE_RECEIVE_BUFFER_COMPACTION="false" (set=true); envconfig.EnableReceiveBufferCompaction=false
CLIENT  http2Client stream after 3000 one-byte DATA frames + trailers, unread: buf.pool==nil:true buf.compacted==nil:true backlog entries=3000 (one-byte entries=2999, largest entry=1 bytes)
CLIENT  all 3000 bytes delivered in order
SERVER  http2Server stream after 3000 one-byte DATA frames, unread: buf.pool==nil:true buf.compacted==nil:true backlog entries=2999 (one-byte entries=2999, largest entry=1 bytes)
HANDLER serverHandlerTransport stream: buf.pool==nil:true
VERDICT: opt-out honoured - production receive buffers are legacy FIFO (one backlog entry per DATA frame, no compaction pool)
--- PASS: Test
    --- PASS: Test/VerifyC2_ProductionRecvBuffers
--- PASS: Test
ok  	google.golang.org/grpc/internal/transport
>> control, variable unset
process env GRPC_GO_EXPERIMENTAL_ENABLE_RECEIVE_BUFFER_COMPACTION="" (set=false); envconfig.EnableReceiveBufferCompaction=true
CLIENT  http2Client stream after 3000 one-byte DATA frames + trailers, unread: buf.pool==nil:false buf.compacted==nil:true backlog entries=2 (one-byte entries=0, largest entry=2999 bytes)
CLIENT  all 3000 bytes delivered in order
SERVER  http2Server stream after 3000 one-byte DATA frames, unread: buf.pool==nil:false buf.compacted==nil:false backlog entries=0 (one-byte entries=0, largest entry=0 bytes)
HANDLER serverHandlerTransport stream: buf.pool==nil:false
VERDICT: compaction active (control run)
--- PASS: Test
    --- PASS: Test/VerifyC2_ProductionRecvBuffers
--- PASS: Test
ok  	google.golang.org/grpc/internal/transport
>> eval fixture (byte-exact from eval_tests.zip), env=false
Got backlog length 0, want 1025 (compaction disabled)
--- FAIL: TestEval_RecvBufferCompactionDisabled
FAIL
FAIL	google.golang.org/grpc/internal/transport
FAIL
>> the branch's own both-settings tests
    --- PASS: Test/RecvBufferCompaction_ManyTinyBuffers
    --- PASS: Test/RecvBufferCompaction_OrderAndZeroCopy
    --- PASS: Test/RecvBufferCompaction_ReleasesCompactedBuffers
ok  	google.golang.org/grpc/internal/transport
```

Observations:

- With the variable set to `false`, the client stream's receive buffer held 3000 backlog entries for 3000 frames (2999 one-byte data entries plus the trailer's EOF entry), the server stream's held 2999 one-byte entries (the first frame sits in the channel), the largest entry was 1 byte, no compaction buffer existed on either stream, and `buf.pool` was nil on all three transports. All 3000 bytes were delivered in order. That is the pre-change FIFO shape: one entry per DATA frame.
- In the control run with the variable unset, the same workload produced 2 backlog entries (largest 2999 bytes) on the client and a live compaction buffer on the server.
- The eval fixture `TestEval_RecvBufferCompactionDisabled` **fails** on this branch with `Got backlog length 0, want 1025 (compaction disabled)`. The fixture does not go through a transport: its `initRecvBufferForTest` finds this branch's `init(mem.BufferPool)` signature and calls `b.init(pool)` with a non-nil pool. On this branch the opt-out is applied one level up - `recvBufferCompactionPool()` returns nil when `envconfig.EnableReceiveBufferCompaction` is false and every transport passes that result to `init` - so a hand-built buffer given a pool compacts regardless of the variable, while no production path hands a pool to `init` when the variable is `false`. The fixture's failure therefore measures the direct-`init(pool)` call it makes, not what production-created buffers do.
- The branch's own both-settings tests pass.

## C3

Target: [`evalon/grpc-go-tr-8d87df46`](https://github.com/kaitranntt-evals/grpc-go-transport-restrict-memory-overhead/tree/evalon/grpc-go-tr-8d87df46) at `d712f270`. Replay: `bash verify/run.sh C3`.

```sh
cp verify/repro/verify_helpers_test.go <wt>/internal/transport/zz_verify_helpers_test.go
cp verify/repro/c3_4kib_frames_copied_test.go <wt>/internal/transport/zz_verify_claim_test.go
go test -tags verify -race -count=1 -v -run '^Test$/^VerifyC3' ./internal/transport
```

Two probes, each run with compaction on and off. The first block of output is `VerifyC3_Client4KiBFrames`: a real `http2Client` receives nine 4096-byte DATA frames from a raw HTTP/2 server while the application is not reading; the transport's buffer pool is wrapped to record every `Get`/`Put` and whether a `recvBuffer` method was on the call stack. The second block is `VerifyC3_Unit4KiBFrames`: nine buffers taken with `pool.Get(4096)` from the default pool (the probe asserts `cap == 4096`, the exact-size tier) are put on a `recvBuffer` set up as `newStream` does, recording each backing array's address at enqueue, in the backlog, and at delivery.

```console
>>> C3 @ d712f270 transport: compact small buffered DATA payloads on slow-reading streams
    client received 9 x 4096-byte DATA frames, unread:
      framer took 9 exact-size 4096 buffers from the pool; 1 still held by the stream
      recvBuffer backlog entry lengths (negative = unsealed compaction tail): [16384 16384]
      pool.Get calls made by recvBuffer itself: [16384 16384] (still outstanding: 2 buffers, 32768 bytes)
    RESULT compaction=true: 8 of 9 received 4 KiB frames were copied out of their pool buffer; all 36864 bytes delivered in order
    client received 9 x 4096-byte DATA frames, unread:
      framer took 9 exact-size 4096 buffers from the pool; 9 still held by the stream
      recvBuffer backlog entry lengths (negative = unsealed compaction tail): [4096 4096 4096 4096 4096 4096 4096 4096]
      pool.Get calls made by recvBuffer itself: [] (still outstanding: 0 buffers, 0 bytes)
    RESULT compaction=false: 0 of 9 received 4 KiB frames were copied out of their pool buffer; all 36864 bytes delivered in order
    queued 9 x 4096-byte frames (each cap 4096 from the default pool), unread:
      backlog entries: [{len=16384 cap=16384 original=false} {len=16384 cap=16384 original=false}]
      original 4 KiB buffers still held (not returned to the pool): 1 of 9
      pool.Get calls made by recvBuffer itself: [16384 16384]
      delivered: 1 buffers with original backing storage, 2 buffers with copied storage; all 36864 bytes in order
    RESULT compaction=true: 8 of 9 queued 4 KiB frames had their storage replaced by a copy
    queued 9 x 4096-byte frames (each cap 4096 from the default pool), unread:
      backlog entries: [{len=4096 cap=4096 original=true} {len=4096 cap=4096 original=true} {len=4096 cap=4096 original=true} {len=4096 cap=4096 original=true} {len=4096 cap=4096 original=true} {len=4096 cap=4096 original=true} {len=4096 cap=4096 original=true} {len=4096 cap=4096 original=true}]
      original 4 KiB buffers still held (not returned to the pool): 9 of 9
      pool.Get calls made by recvBuffer itself: []
      delivered: 9 buffers with original backing storage, 0 buffers with copied storage; all 36864 bytes in order
    RESULT compaction=false: 0 of 9 queued 4 KiB frames had their storage replaced by a copy
--- PASS: Test
--- PASS: Test
PASS
ok  	google.golang.org/grpc/internal/transport
```

Observations:

- Compaction on: of nine queued 4 KiB frames, eight had their exact-size pool buffer returned to the pool and their bytes copied into two fresh 16384-byte buffers requested by `recvBuffer` itself (`pool.Get calls made by recvBuffer itself: [16384 16384]`); only the first frame, which went straight to the reader channel, kept its original storage. Delivery handed the application 1 original buffer and 2 copies.
- Compaction off: all nine frames kept their original backing arrays from enqueue through delivery; `recvBuffer` requested nothing from the pool.
- All 36864 bytes arrived in order in every case, on both the unit and the live-client path.

Impact reasoning: the branch compacts every payload below `recvBufferCompactionThreshold = http2MaxFrameLen / 2` (8192 bytes). A 4 KiB DATA frame is not a tiny frame: its buffer already comes from the pool's exact 4096-byte tier, so nothing is wasted per frame and copying it saves no memory - in the run above 36864 payload bytes that sat in 9 x 4096 = 36864 bytes of pool memory were moved into 4096 + 2 x 16384 = 36864 bytes. What it costs is one extra copy of every byte of every frame under 8 KiB whenever the reader is even one frame behind, on a path the task statement says must keep "their existing zero-copy path" for "ordinary-sized traffic". Data stays correct; the regression is CPU and memory bandwidth on streams whose peer uses frames of 1-8 KiB. Setting the environment variable to `false` avoids it (and the fix).

## C4

Target: [`evalon/grpc-go-tr-53b22674`](https://github.com/kaitranntt-evals/grpc-go-transport-restrict-memory-overhead/tree/evalon/grpc-go-tr-53b22674) at `04051176`. Replay: `bash verify/run.sh C4`.

```sh
cp verify/repro/verify_helpers_test.go <wt>/internal/transport/zz_verify_helpers_test.go
cp verify/repro/c4_alternating_frames_16kib_test.go <wt>/internal/transport/zz_verify_claim_test.go
go test -tags verify -race -count=1 -v -run '^Test$/^VerifyC4' ./internal/transport
```

The first two blocks of output are `VerifyC4_ClientAlternating` (compaction on, then off): a real `http2Client` receives `2048, (1, 2048) x 30` byte DATA frames - 63518 bytes, inside the default 64 KiB stream window - while the application is not reading. The remaining four blocks are `VerifyC4_UnitAlternating`: the same pattern with 200 pairs put on a `recvBuffer` initialised as production does (`init(pool)`), with the default pool and with a nil pool, compaction on and off. For every one-byte backlog entry the probe records the capacity and address of its backing array; the first 8 are treated as warm-up.

```console
>>> C4 @ 04051176 chore: apply eval changes
    client received 2048 + 30 x (1 byte, 2048 bytes) = 63518 payload bytes of DATA frames, unread:
      backlog entries of length 1: 30
      backlog entries of length 2048: 30
      backing capacity of the one-byte entries, in arrival order (first 12 of 30): [1 256 512 1024 4096 16384 16384 16384 16384 16384 16384 16384]
      pool.Get calls made by recvBuffer (request size -> count): map[2048:1 8192:1 16384:24]; still outstanding: 26 buffers, 413696 bytes
    RESULT compaction=true: of 22 one-byte payloads after a 8-payload warm-up, 22 sit alone in a backing array of >= 16384 bytes (22 distinct arrays); compaction buffers pin 413696 bytes for 63518 unread payload bytes
    client received 2048 + 30 x (1 byte, 2048 bytes) = 63518 payload bytes of DATA frames, unread:
      backlog entries of length 1: 30
      backlog entries of length 2048: 30
      backing capacity of the one-byte entries, in arrival order (first 12 of 30): [1 1 1 1 1 1 1 1 1 1 1 1]
      pool.Get calls made by recvBuffer (request size -> count): map[]; still outstanding: 0 buffers, 0 bytes
    RESULT compaction=false: of 22 one-byte payloads after a 8-payload warm-up, 0 sit alone in a backing array of >= 16384 bytes (22 distinct arrays); compaction buffers pin 0 bytes for 63518 unread payload bytes
    queued 2048 + 200 x (1 byte, 2048 bytes) = 411848 payload bytes, unread:
      backlog entries of length 1: 200
      backlog entries of length 2048: 200
      backing capacity of the one-byte entries, in arrival order (first 12 of 200): [1 256 512 1024 4096 16384 16384 16384 16384 16384 16384 16384]
      pool.Get calls made by recvBuffer (request size -> count): map[2048:1 8192:1 16384:194]; still outstanding: 196 buffers, 3198976 bytes
      live heap retained by the queue: 3695992 bytes = 9.0x the unread payload
    RESULT compaction=true pool=default: of 192 one-byte payloads after a 8-payload warm-up, 192 sit alone in a backing array of >= 16384 bytes (192 distinct arrays)
    queued 2048 + 200 x (1 byte, 2048 bytes) = 411848 payload bytes, unread:
      backlog entries of length 1: 200
      backlog entries of length 2048: 200
      backing capacity of the one-byte entries, in arrival order (first 12 of 200): [1 256 512 1024 2048 4096 8192 16384 16384 16384 16384 16384]
      live heap retained by the queue: 3618120 bytes = 8.8x the unread payload
    RESULT compaction=true pool=nil: of 192 one-byte payloads after a 8-payload warm-up, 192 sit alone in a backing array of >= 16384 bytes (192 distinct arrays)
    queued 2048 + 200 x (1 byte, 2048 bytes) = 411848 payload bytes, unread:
      backlog entries of length 1: 200
      backlog entries of length 2048: 200
      backing capacity of the one-byte entries, in arrival order (first 12 of 200): [1 1 1 1 1 1 1 1 1 1 1 1]
      pool.Get calls made by recvBuffer (request size -> count): map[]; still outstanding: 0 buffers, 0 bytes
      live heap retained by the queue: 443048 bytes = 1.1x the unread payload
    RESULT compaction=false pool=default: of 192 one-byte payloads after a 8-payload warm-up, 0 sit alone in a backing array of >= 16384 bytes (192 distinct arrays)
    queued 2048 + 200 x (1 byte, 2048 bytes) = 411848 payload bytes, unread:
      backlog entries of length 1: 200
      backlog entries of length 2048: 200
      backing capacity of the one-byte entries, in arrival order (first 12 of 200): [1 1 1 1 1 1 1 1 1 1 1 1]
      live heap retained by the queue: 443048 bytes = 1.1x the unread payload
    RESULT compaction=false pool=nil: of 192 one-byte payloads after a 8-payload warm-up, 0 sit alone in a backing array of >= 16384 bytes (192 distinct arrays)
--- PASS: Test
--- PASS: Test
PASS
ok  	google.golang.org/grpc/internal/transport
```

Observations:

- Compaction on, live client: the capacities of the one-byte entries' backing arrays in arrival order are `1 256 512 1024 4096 16384 16384 ...`; all 22 one-byte payloads after the warm-up sit alone in a 16384-byte array, 22 distinct arrays. `recvBuffer` made 24 `pool.Get(16384)` calls and its compaction buffers pin 413696 bytes for a stream holding 63518 unread bytes.
- Compaction on, unit, 200 pairs: 192 of 192 post-warm-up one-byte payloads each sit alone in a distinct >= 16384-byte array, with the default pool and with a nil pool. Live heap retained by the queue is 3.70 MB / 3.62 MB for 411848 unread bytes: 9.0x / 8.8x the payload.
- Compaction off, same workloads: every one-byte entry has capacity 1, nothing is requested from the pool, and the queue retains 443048 bytes: 1.1x the payload.
- All bytes arrived in order in every case.

Impact reasoning: on this branch `newPendingBuffer` doubles `nextPendingCap` every time a pending buffer is *created*, not when one fills, and the cap is only reset once the reader has fully caught up. Any payload above the pooling threshold flushes the pending buffer, so in a stream that mixes tiny and medium frames while the reader is behind, every tiny frame opens a new pending buffer and after at most seven of them each one is 16 KiB. The fix was meant to make memory "track its unread payload, not its frame count"; for this traffic shape it does the opposite - the stream holds about 8x more memory with the fix enabled than with it disabled (9.0x vs 1.1x of the unread payload), and the amount again scales with the number of frames (16 KiB per tiny frame) rather than with bytes. The pattern needs nothing exotic: a peer that sends a small frame followed by a larger one (for example a message header or a short message between larger messages) to a reader that is momentarily behind produces it, and a peer that wants to inflate memory can produce it deliberately within flow-control limits. The only workaround is the environment opt-out, which brings back the original tiny-frame problem.

## C5

Target: [`evalon/grpc-go-tr-c63d3773`](https://github.com/kaitranntt-evals/grpc-go-transport-restrict-memory-overhead/tree/evalon/grpc-go-tr-c63d3773) at `89a176f2`. Replay: `bash verify/run.sh C5`.

```sh
cp verify/repro/verify_helpers_test.go <wt>/internal/transport/zz_verify_helpers_test.go
cp verify/repro/c5_short_burst_16kib_destination_test.go <wt>/internal/transport/zz_verify_claim_test.go
go test -tags verify -race -count=1 -v -run '^Test$/^VerifyC5' ./internal/transport
```

The first four blocks are `VerifyC5_ClientShortBursts`: a real `http2Client` receives a burst of two, then three, one-byte DATA frames while the application is not reading (compaction on, then off). The remaining blocks are `VerifyC5_UnitShortBursts`: the same bursts put on a `recvBuffer` initialised as production does (`init(pool)`). The pool wrapper records every `Get` made from a `recvBuffer` method and which of those buffers are still outstanding. The claim's names `compactBacklogLocked` / `compactionThreshold` do not exist on this branch; the equivalents are `compactLocked`, `flushChunkLocked` and `recvBufferCompactionChunkSize`.

```console
>>> C5 @ 89a176f2 chore: apply eval changes
    client received a burst of 2 one-byte DATA frames:
      before any read/flush: backlog entries=0, unflushed compaction chunk: cap=16384 bytes holding 1 payload byte(s)
      pool.Get calls made by recvBuffer: [Get(16384)->cap 16384]; still outstanding: 1 buffers, 16384 bytes
    RESULT compaction=true burst=2: largest compaction destination requested = 16384 bytes (> 1024: true)
    client received a burst of 3 one-byte DATA frames:
      before any read/flush: backlog entries=0, unflushed compaction chunk: cap=16384 bytes holding 2 payload byte(s)
      pool.Get calls made by recvBuffer: [Get(16384)->cap 16384]; still outstanding: 1 buffers, 16384 bytes
    RESULT compaction=true burst=3: largest compaction destination requested = 16384 bytes (> 1024: true)
    client received a burst of 2 one-byte DATA frames:
      before any read/flush: backlog entries=1, unflushed compaction chunk: cap=0 bytes holding 0 payload byte(s)
      pool.Get calls made by recvBuffer: []; still outstanding: 0 buffers, 0 bytes
    RESULT compaction=false burst=2: largest compaction destination requested = 0 bytes (> 1024: false)
    client received a burst of 3 one-byte DATA frames:
      before any read/flush: backlog entries=2, unflushed compaction chunk: cap=0 bytes holding 0 payload byte(s)
      pool.Get calls made by recvBuffer: []; still outstanding: 0 buffers, 0 bytes
    RESULT compaction=false burst=3: largest compaction destination requested = 0 bytes (> 1024: false)
    queued a burst of 2 one-byte payloads:
      before any read/flush: backlog entries=0, unflushed compaction chunk: cap=16384 bytes holding 1 payload byte(s)
      pool.Get calls made by recvBuffer: [Get(16384)->cap 16384]; still outstanding: 1 buffers, 16384 bytes
    RESULT compaction=true burst=2: largest compaction destination requested = 16384 bytes (> 1024: true)
      after delivering all 2 bytes in order: outstanding recvBuffer pool buffers: 0 (0 bytes)
    queued a burst of 3 one-byte payloads:
      before any read/flush: backlog entries=0, unflushed compaction chunk: cap=16384 bytes holding 2 payload byte(s)
      pool.Get calls made by recvBuffer: [Get(16384)->cap 16384]; still outstanding: 1 buffers, 16384 bytes
    RESULT compaction=true burst=3: largest compaction destination requested = 16384 bytes (> 1024: true)
      after delivering all 3 bytes in order: outstanding recvBuffer pool buffers: 0 (0 bytes)
    queued a burst of 2 one-byte payloads:
      before any read/flush: backlog entries=1, unflushed compaction chunk: cap=0 bytes holding 0 payload byte(s)
      pool.Get calls made by recvBuffer: []; still outstanding: 0 buffers, 0 bytes
    RESULT compaction=false burst=2: largest compaction destination requested = 0 bytes (> 1024: false)
      after delivering all 2 bytes in order: outstanding recvBuffer pool buffers: 0 (0 bytes)
    queued a burst of 3 one-byte payloads:
      before any read/flush: backlog entries=2, unflushed compaction chunk: cap=0 bytes holding 0 payload byte(s)
      pool.Get calls made by recvBuffer: []; still outstanding: 0 buffers, 0 bytes
    RESULT compaction=false burst=3: largest compaction destination requested = 0 bytes (> 1024: false)
      after delivering all 3 bytes in order: outstanding recvBuffer pool buffers: 0 (0 bytes)
--- PASS: Test
--- PASS: Test
PASS
ok  	google.golang.org/grpc/internal/transport
```

Observations:

- Compaction on: for both the two-frame and the three-frame burst, on both the live client and the unit path, `recvBuffer` requested exactly one buffer, `Get(16384)`, and before any read or flush that 16384-byte chunk was outstanding while holding 1 (burst of 2) or 2 (burst of 3) payload bytes. The first frame of each burst is handed to the reader channel and is not compacted.
- After the application read the data the chunk was back in the pool (`outstanding recvBuffer pool buffers: 0`): on flush a chunk that is at most half full is copied into a right-sized buffer and the chunk is returned.
- Compaction off: no pool request at all; the frames sit in the backlog as 1-byte entries.

Impact reasoning: on this branch the compaction destination is always a full `recvBufferCompactionChunkSize = http2MaxFrameLen` (16384-byte) pool buffer, allocated as soon as a second small payload arrives before the first is read. A stream that is one small frame behind therefore pins 16 KiB for one or two bytes until its reader catches up, where the pre-change code held two or three 1-byte slices. The buffer is pooled and is released on the next read, so for a handful of streams this is noise; it matters for servers or clients with many concurrently lagging streams that each carry a few small frames (16 KiB x number of such streams, against a few dozen bytes each before), which is a per-stream overhead proportional to stream count rather than to unread payload.

## C6

Target: [`evalon/grpc-go-tr-91caeccb`](https://github.com/kaitranntt-evals/grpc-go-transport-restrict-memory-overhead/tree/evalon/grpc-go-tr-91caeccb) at `86906912`. Replay: `bash verify/run.sh C6`.

Naming drift: on this branch the tests live in `internal/transport/recv_buffer_test.go` (not `transport_test.go`) and the test using `measureClientTinyDataFrames` is `TestClientStream_TinyDataFramesMemory` (not `TestRecvBufferCompaction`). `measureClientTinyDataFrames` and `heapGrowth` exist under those names.

```sh
GOMAXPROCS=1 go test -count=15 -v -run '^Test$/^ClientStream_TinyDataFramesMemory$' ./internal/transport      # and without GOMAXPROCS
cp verify/repro/c6_baseline_after_workload_test.go <wt>/internal/transport/zz_verify_claim_test.go
GOMAXPROCS=1 go test -tags verify -count=3 -v -run '^Test$/^VerifyC6_BaselineVsWorkload$' ./internal/transport  # and without GOMAXPROCS
go test -tags verify -count=1 -v -run '^Test$/^VerifyC6_ZeroVersusZero$' ./internal/transport
rm internal/transport/zz_verify_claim_test.go
git apply verify/repro/c6_disable_compaction_mutation.patch
GOMAXPROCS=1 go test -count=15 -v -run '^Test$/^(ClientStream_TinyDataFramesMemory|RecvBuffer_TinyMessagesMemory)$' ./internal/transport   # and without GOMAXPROCS
git checkout -- internal/transport
```

```console
>>> C6 @ 86906912 transport: compact small buffered DATA frames on slow-reading streams
>> the measurement harness and the assertion, as delivered (internal/transport/recv_buffer_test.go)
49:func heapGrowth(before, after uint64) uint64 {
51:		return 0
92:	before := heapAllocAfterGC()
100:	after := heapAllocAfterGC()
134:	if compacted*4 > uncompacted {
265:			case *http2.HeadersFrame:
266:				streamID = f.StreamID
285:			if err := framer.WriteData(streamID, false, []byte{byte(i)}); err != nil {
307:func measureClientTinyDataFrames(t *testing.T, numFrames int) uint64 {
318:	stream, err := ct.NewStream(ctx, &CallHdr{}, nil)
323:	before := heapAllocAfterGC()
325:	case <-pingAcked:
329:	after := heapAllocAfterGC()
362:	if compacted*4 > uncompacted {
>> unmodified branch test TestClientStream_TinyDataFramesMemory, GOMAXPROCS=1 x15
      1     --- FAIL: Test/ClientStream_TinyDataFramesMemory
     14     --- PASS: Test/ClientStream_TinyDataFramesMemory
      2 FAIL
      1 FAIL	google.golang.org/grpc/internal/transport
     11 Heap growth for 65535 unread bytes in 65535 DATA frames: 0 bytes with compaction, 0 bytes without
      3 Heap growth for 65535 unread bytes in 65535 DATA frames: 0 bytes with compaction, >0 bytes without
      1 Heap growth for 65535 unread bytes in 65535 DATA frames: >0 bytes with compaction, 0 bytes without
      1 Heap growth with compaction = 53040 bytes, without = 0 bytes; want at least a 4x reduction
>> unmodified branch test TestClientStream_TinyDataFramesMemory, GOMAXPROCS=default x15
     15     --- PASS: Test/ClientStream_TinyDataFramesMemory
     15 Heap growth for 65535 unread bytes in 65535 DATA frames: >0 bytes with compaction, >0 bytes without
      1 ok  	google.golang.org/grpc/internal/transport
>> instrumented copy of the harness: DATA bytes already received when the baseline sample returns, GOMAXPROCS=1 x3
BASELINE compaction=true:  DATA bytes already received when baseline sampling started=0, when it returned=65535 of 65535; reported growth=0
BASELINE compaction=false: DATA bytes already received when baseline sampling started=0, when it returned=37122 of 65535; reported growth=382248
ASSERTION compacted=0 uncompacted=382248: branch check `compacted*4 > uncompacted` is false => comparison ACCEPTED as a >=4x reduction
BASELINE compaction=true:  DATA bytes already received when baseline sampling started=0, when it returned=65535 of 65535; reported growth=0
BASELINE compaction=false: DATA bytes already received when baseline sampling started=0, when it returned=30250 of 65535; reported growth=0
ASSERTION compacted=0 uncompacted=0: branch check `compacted*4 > uncompacted` is false => comparison ACCEPTED as a >=4x reduction
BASELINE compaction=true:  DATA bytes already received when baseline sampling started=0, when it returned=65535 of 65535; reported growth=0
BASELINE compaction=false: DATA bytes already received when baseline sampling started=0, when it returned=29450 of 65535; reported growth=0
ASSERTION compacted=0 uncompacted=0: branch check `compacted*4 > uncompacted` is false => comparison ACCEPTED as a >=4x reduction
ok  	google.golang.org/grpc/internal/transport
>> instrumented copy of the harness: DATA bytes already received when the baseline sample returns, GOMAXPROCS=default x3
BASELINE compaction=true:  DATA bytes already received when baseline sampling started=0, when it returned=328 of 65535; reported growth=54056
BASELINE compaction=false: DATA bytes already received when baseline sampling started=0, when it returned=405 of 65535; reported growth=3961264
ASSERTION compacted=54056 uncompacted=3961264: branch check `compacted*4 > uncompacted` is false => comparison ACCEPTED as a >=4x reduction
BASELINE compaction=true:  DATA bytes already received when baseline sampling started=0, when it returned=409 of 65535; reported growth=61080
BASELINE compaction=false: DATA bytes already received when baseline sampling started=0, when it returned=168 of 65535; reported growth=3965168
ASSERTION compacted=61080 uncompacted=3965168: branch check `compacted*4 > uncompacted` is false => comparison ACCEPTED as a >=4x reduction
BASELINE compaction=true:  DATA bytes already received when baseline sampling started=0, when it returned=220 of 65535; reported growth=57416
BASELINE compaction=false: DATA bytes already received when baseline sampling started=0, when it returned=343 of 65535; reported growth=3940968
ASSERTION compacted=57416 uncompacted=3940968: branch check `compacted*4 > uncompacted` is false => comparison ACCEPTED as a >=4x reduction
ok  	google.golang.org/grpc/internal/transport
>> zero-versus-zero fed to the branch's heapGrowth and assertion directly
heapGrowth(5000000, 4900000)=0 heapGrowth(9000000, 5000000)=0
ASSERTION compacted=0 uncompacted=0: branch check `compacted*4 > uncompacted` is false => comparison ACCEPTED as a >=4x reduction
ok  	google.golang.org/grpc/internal/transport
>> apply mutation: compaction never happens even when enabled (repro/c6_disable_compaction_mutation.patch)
 internal/transport/transport.go | 2 +-
 1 file changed, 1 insertion(+), 1 deletion(-)
>> mutated: unmodified branch tests, GOMAXPROCS=1 x15
      2     --- FAIL: Test/ClientStream_TinyDataFramesMemory
     15     --- FAIL: Test/RecvBuffer_TinyMessagesMemory
     13     --- PASS: Test/ClientStream_TinyDataFramesMemory
      2 FAIL
      1 FAIL	google.golang.org/grpc/internal/transport
      9 Heap growth for 65535 unread bytes in 65535 DATA frames: 0 bytes with compaction, 0 bytes without
>> mutated: unmodified branch tests, GOMAXPROCS=default x15
     15     --- FAIL: Test/ClientStream_TinyDataFramesMemory
     15     --- FAIL: Test/RecvBuffer_TinyMessagesMemory
      2 FAIL
      1 FAIL	google.golang.org/grpc/internal/transport
```

The harness as delivered: the test server writes all 65535 one-byte DATA frames as soon as it has read the client's HEADERS frame (lines 265-285); the client side calls `ct.NewStream` (line 318), then takes the baseline `before := heapAllocAfterGC()` (line 323: two `runtime.GC()` calls then `ReadMemStats`), then waits for the PING ack (line 325), then samples `after` (line 329). Nothing makes the server wait for the baseline. `heapGrowth` returns 0 when `after < before` (lines 49-51) and the only assertion is `if compacted*4 > uncompacted { t.Errorf(...) }` (line 362).

**Part measurement_window** (no ordering puts the baseline before the workload's receive-buffer allocations) and **part workload_timing** (a permitted execution allocates them before the baseline):

- `VerifyC6_BaselineVsWorkload` is `measureClientTinyDataFrames` statement for statement plus two reads of the stream's `inFlow.pendingData` (DATA bytes the client transport has already queued on the stream). At `GOMAXPROCS=1`, by the time the baseline sample returned the client had already received 65535 of 65535 bytes in the compaction-enabled run in 3 of 3 runs, and 29450-37122 of 65535 in the compaction-disabled run. On 8 CPUs 168-409 bytes had already been received. In every run the count was 0 when baseline sampling started, so the workload ran during the baseline's two GC cycles.
- The unmodified branch test shows the consequence in its own log line: at `GOMAXPROCS=1`, 11 of 15 runs logged `Heap growth for 65535 unread bytes in 65535 DATA frames: 0 bytes with compaction, 0 bytes without` - the entire workload's allocations were excluded from both measurements - and 14 of 15 runs passed. The one failure was a healthy tree being rejected (`with compaction = 53040 bytes, without = 0 bytes`), i.e. the same race also makes the test flaky. On 8 CPUs all 15 runs measured non-zero growth on both sides.

**Part comparison_acceptance** (zero versus zero is accepted):

- `VerifyC6_ZeroVersusZero` calls the branch's `heapGrowth` with two shrinking heaps and gets `0` and `0`; the branch's expression `compacted*4 > uncompacted` is then `false`, so no error is reported. The same `ASSERTION compacted=0 uncompacted=0 ... ACCEPTED` line appears in the live `GOMAXPROCS=1` runs above.
- With `verify/repro/c6_disable_compaction_mutation.patch` applied (compaction never happens, so there is no memory improvement at all), the unmodified `TestClientStream_TinyDataFramesMemory` still passed 13 of 15 runs at `GOMAXPROCS=1`, 9 of them on the 0-versus-0 measurement. On 8 CPUs it failed 15 of 15. The branch's unit-level `TestRecvBuffer_TinyMessagesMemory`, whose baseline is taken sequentially before the puts, failed 15 of 15 in both configurations.

Impact reasoning: the end-to-end memory-reduction assertion is the branch's "runnable Go regression coverage demonstrating the memory improvement" over a real transport, and on a single-CPU runner it accepts a tree with compaction removed most of the time and occasionally rejects a healthy tree, because what it compares depends on how much of the workload landed before the baseline. On multi-core machines the window is small (a few hundred of 65535 bytes) and the test behaves as intended, and the sequential unit test on the same branch catches the same regression deterministically, so the product is not exposed - but the end-to-end test's verdict cannot be trusted on constrained CI.

## C7

Target: the audited branch [`grpc-go-transport-restrict-memory-overhead-perfect`](https://github.com/kaitranntt-evals/grpc-go/tree/grpc-go-transport-restrict-memory-overhead-perfect) at `327a6ff9` (the delivered repository). Replay: `bash verify/run.sh C7`.

The check is run in a pristine detached worktree of the delivered branch so that this audit's own `verify/` files are not part of what is formatted.

```sh
git worktree add --detach ~/wt/delivered origin/grpc-go-transport-restrict-memory-overhead-perfect && cd ~/wt/delivered
bash -o pipefail -c 'source scripts/common.sh; gofmt -s -d -l . 2>&1 | fail_on_output'; echo "exit=$?"
gofmt -s -l .; echo "exit=$?"
git diff --name-only c92e985770b7194d4a4f433c84d42c6c195e8ce5 HEAD -- '*.go' | xargs gofmt -s -l; echo "exit=$?"
```

```console
>>> C7 @ 327a6ff9 restrict memory overhead of buffering small data frames
go version go1.25.7 linux/amd64
99:# - gofmt, goimports, go vet, go mod tidy.
105:  gofmt -s -d -l . 2>&1 | fail_on_output
>> the claim's command
exit=0
>> gofmt -s -l . (listing only)
exit=0 listed=0 of 1053 tracked .go files
>> the files the solution changed
exit=0
>> positive control: the same command on a deliberately unsimplified file
zz_verify_control.go
diff zz_verify_control.go.orig zz_verify_control.go
--- zz_verify_control.go.orig
+++ zz_verify_control.go
@@ -1,4 +1,4 @@
 package x
 
 var a = []int{1, 2}
-var s = a[0:len(a)]
+var s = a[0:]
exit=1
```

Observations:

- The claim's command printed nothing and exited 0 (`fail_on_output` exits non-zero when its input has any line). `gofmt -s -l .` listed 0 of 1053 tracked `.go` files, and the nine `.go` files the solution changed produce no listing.
- Positive control: with one deliberately unsimplified file added (`a[0:len(a)]`), the identical command printed the file name and a diff and exited 1, so the command does detect `gofmt -s` differences in this tree with this toolchain. The control file was removed afterwards (`git status --short` is empty).
