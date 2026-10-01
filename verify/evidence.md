Evidence for the behavioral audit of grpc-go-transport-restrict-memory-overhead, run `v-c4ba29c7`.

Observations only. Every claim targets a branch of [grpc-go-transport-restrict-memory-overhead](https://github.com/kaitranntt-evals/grpc-go-transport-restrict-memory-overhead) (remote `claims` below); each was
checked out in its own detached worktree under `~/wt/<suffix>`. Production code on the claim branches was never committed to;
all probes live under `verify/` and are copied or applied into a worktree for a run and reverted afterwards.

- Toolchain: `go version go1.25.7 linux/amd64`.
- Eval fixture: `~/eval/tests/eval_recv_buffer_compaction_test.go` extracted from `eval_tests.zip`, sha256 `b3dfb446be024dab6ec54dab6afe80130db00196bd3e1d199a91f84acbd3bf69`, copied byte-exact to `internal/transport/eval_recv_buffer_compaction_test.go` for fixture runs and removed afterwards.
- Layout: `verify/repro/` (one repro per substantiated claim), `verify/probes/` (probes and instrumentation for the other claims), `verify/logs/` (raw outputs of the runs quoted below).
- `verify/go.mod` exists only to keep these artifacts out of `go build ./...`, `go vet ./...` and `go test ./...` of the main module; the test files are meant to be copied into `internal/transport` of the claim branch named in their header.
- Output lines are verbatim; `…` marks a line cut for width or lines omitted (stack frames, repeated iterations).

## C1

Target: [evalon/grpc-go-tr-9841642e](https://github.com/kaitranntt-evals/grpc-go-transport-restrict-memory-overhead/tree/evalon/grpc-go-tr-9841642e) at `f97244434e6935ee415539215f81f6e0421b8cbf`.

```sh
cd ~/repos/grpc-go                       # checkout of the verify branch; V points at its verify/ directory
V=~/repos/grpc-go/verify
git remote add claims https://github.com/kaitranntt-evals/grpc-go-transport-restrict-memory-overhead
git fetch claims evalon/grpc-go-tr-9841642e
git worktree add --detach ~/wt/9841642e FETCH_HEAD    # f9724443
cd ~/wt/9841642e
```

Naming: the claim's `recvBuffer.compact()`, `recvBuffer.sealCompacted()`, `b.compacted` and `b.backlog[0].buffer` all exist under those names in `internal/transport/transport.go`.

Source trace (`internal/transport/transport.go`, `compact`): when the tail is a small payload, its bytes are copied into a new `b.compacted` and the tail is freed, but the backlog entry keeps pointing at it until `sealCompacted` runs:

```go
	if b.compacted == nil {
		tail := b.backlog[len(b.backlog)-1].buffer
		if isCompactable(tail) {
			tailData := tail.ReadOnlyData()
			b.compacted = make([]byte, 0, compactionCap(0, len(tailData)+len(data)))
			b.compacted = append(b.compacted, tailData...)
			tail.Free()
		} else {
```

`sealCompacted` (the only place the entry is rewritten: `b.backlog[len(b.backlog)-1].buffer = mem.SliceBuffer(b.compacted)`) is called from `put` only when a new entry is appended, and from `load` only when `len(b.backlog) == 1`.

Probe run (three one-byte puts, no reads, then white-box inspection under `b.mu`):

```sh
cp $V/repro/c1_9841642e_stale_backlog_ref_test.go internal/transport/verify_c1_stale_backlog_ref_test.go
go test -v -run '^TestVerifyC1_' google.golang.org/grpc/internal/transport -race -count=1
rm internal/transport/verify_c1_stale_backlog_ref_test.go
```

```console
=== RUN   TestVerifyC1_ThreeOneBytePutsNoReads
    verify_c1_stale_backlog_ref_test.go:41: after 3 one-byte puts, no reads: len(b.c)=1 len(b.backlog)=1
    verify_c1_stale_backlog_ref_test.go:42: b.compacted (consolidated payload) = b2 c3  (len=2 cap=256, backing 0xc00000f100)
    verify_c1_stale_backlog_ref_test.go:43: buffers already released via Free(): [put#2 put#3]
    verify_c1_stale_backlog_ref_test.go:48: b.backlog[0].buffer: type=transport.vC1TrackedBuffer payload=b2 backing=0xc0000155ff
    verify_c1_stale_backlog_ref_test.go:54:   -> b.backlog[0].buffer IS the original source buffer of put#2 (identical backing array 0xc0000155ff == 0xc0000155ff: true); already freed: true; its byte is also in b.compacted: true
    verify_c1_stale_backlog_ref_test.go:63: unread bytes behind the channel: 1 according to b.backlog entries, 2 according to b.compacted
    verify_c1_stale_backlog_ref_test.go:67: C1 OBSERVED: a queue entry still references a replaced (merged and freed) source buffer
    verify_c1_stale_backlog_ref_test.go:79: bytes delivered to a reader through get()/load(): a1 b2 c3
--- PASS: TestVerifyC1_ThreeOneBytePutsNoReads (0.00s)
=== RUN   TestVerifyC1_StaleEntryIsAFreedPooledBuffer
    verify_c1_stale_backlog_ref_test.go:105: len(b.backlog)=1 b.compacted=02 03
    verify_c1_stale_backlog_ref_test.go:109: C1 OBSERVED: reading b.backlog[0].buffer panics: Cannot read freed buffer
    verify_c1_stale_backlog_ref_test.go:117: C1 OBSERVED: freeing b.backlog[0].buffer (as the fixture teardown loops do) panics: Cannot free freed buffer
--- PASS: TestVerifyC1_StaleEntryIsAFreedPooledBuffer (0.00s)
PASS
ok  	google.golang.org/grpc/internal/transport	1.015s
```

What the output shows:

- After the three puts, `b.compacted` holds `b2 c3` (puts #2 and #3 merged; put #1 went straight to the channel), and both source buffers were already released via `Free()` (`[put#2 put#3]`).
- `b.backlog[0].buffer` is still the original put #2 buffer (same backing array, type `vC1TrackedBuffer`), i.e. a replaced source buffer whose byte already lives in `b.compacted` and which was already freed. The backlog entries account for 1 unread byte while `b.compacted` holds 2.
- The reader still receives `a1 b2 c3`: `load()` seals the entry before handing it out, so delivery is correct.
- Second test: when the small payload is a ref-counted pooled buffer (a 1-byte slice of a 4096-byte pooled buffer), the stale entry is a buffer that already went back to its pool; reading it panics with `Cannot read freed buffer` and freeing it panics with `Cannot free freed buffer`.

The byte-exact eval fixture stays green on this branch, so the fixture does not detect the stale reference:

```sh
cp ~/eval/tests/eval_recv_buffer_compaction_test.go internal/transport/eval_recv_buffer_compaction_test.go
go test -v -run '^TestEval_' google.golang.org/grpc/internal/transport -race -count=1
rm internal/transport/eval_recv_buffer_compaction_test.go
```

```console
$ go test -v -run '^TestEval_' google.golang.org/grpc/internal/transport -race -count=1
--- PASS: TestEval_RecvBufferCompaction (0.00s)
--- PASS: TestEval_RecvBufferCompactionDisabled (0.00s)
--- PASS: TestEval_RecvBufferCompactionSkippedLargeBuffer (0.02s)
--- PASS: TestEval_RecvBufferErrorResetSafety (0.00s)
--- PASS: TestEval_RecvBufferCompaction_MixedFrames (0.00s)
--- PASS: TestEval_RecvBufferCompaction_MultiCycleMemoryBound (0.00s)
ok  	google.golang.org/grpc/internal/transport	1.059s
```

Impact reasoning: between a compacting `put` and the next seal, `b.backlog[last].buffer` does not describe the queued data; it references a buffer that `compact` already freed. On the paths exercised here the reader is unaffected (bytes arrive in order, because `load`/`put` seal before hand-off), and on the live HTTP/2 path payloads below the pooling threshold are produced by `mem.Copy`, which returns a `mem.SliceBuffer` whose `Free` is a no-op, so no crash was observed through the transport. The hazard is to anything that walks `b.backlog` without sealing first (white-box tests, accounting, or future cleanup code): it undercounts unread bytes, and for ref-counted small buffers it touches freed memory and panics, as the second test shows. No production code path that does so was found on this branch.

## C2

Three target branches, adjudicated separately below. Method, identical on each: the branch's added transport test starts a fake HTTP/2 server goroutine that calls `lis.Accept()`, `io.ReadFull(conn, …)` (client preface), `Framer.ReadFrame()` and buffered writes on raw sockets. The probe (`verify/probes/c2/`, test files only) logs when the fake-server goroutine returns and can make one of its socket operations stall, selected by `VERIFY_STALL`:

- `none`: unmodified behaviour plus logging.
- `accept`: the client is pointed at a second listener nobody accepts on, so the fake server's `lis.Accept()` never gets a connection.
- `accept-main-blocked`: like `accept`, and the test goroutine is parked for 25 s just before `NewHTTP2Client` (after its 10 s contexts were created), so its deferred `lis.Close()` cannot run during that time.
- `headers`: once the client's HEADERS arrive, the fake server blocks in a socket read instead of answering.
- `headers-main-blocked`: like `headers`, and the test goroutine is parked for 25 s right after `NewStream`.
- `handshake`: the fake server accepts, then blocks in a socket read instead of sending SETTINGS.

Each test runs two subtests (compaction on/off), so every stall is observed twice per run.

Reading of the results (same on all three branches):

- No socket or dial deadline exists in the added test file (`grep -c` prints `0`), and no timer closes the listener or the accepted connection.
- `accept-main-blocked`: the stalled `lis.Accept()` stayed blocked for 25.0-25.1 s, well past the test's 10 s context deadline, and returned `use of closed network connection` only at the moment the parked test goroutine resumed, failed and ran its deferred `lis.Close()`. Nothing timed interrupts `Accept`; deferred cleanup is its only bound.
- `accept` (test goroutine not parked): `Accept` is released at 10.0 s, again with `use of closed network connection`, i.e. by the same deferred `lis.Close()` after `NewHTTP2Client` gave up on its connect context.
- `headers` / `headers-main-blocked` / `handshake`: the stalled socket read on the accepted connection ended at 10.0 s with `EOF` / `unexpected EOF`, even while the test goroutine was parked (`headers-main-blocked`). So the reads on the accepted connection are released when the peer (the client transport under test, driven by the test's 10 s context) drops the connection, not by any deadline on the fake server's socket.
- Unmodified (`none`) the tests pass in about 0.1 s; no hang occurs unless one of these operations actually stalls.

Impact reasoning: test-only. On every branch at least one exercised, potentially blocking socket operation of the added test (`lis.Accept()` in the fake server) has neither a socket/dial deadline nor an independent timed cancellation; it is released only by deferred cleanup, so it stays blocked for as long as the test goroutine takes to reach its defers (25 s in the probe, where the 10 s context had long expired). The fake server's reads and writes on the accepted connection also carry no local deadline; in the probe they were bounded only because the client transport under test closed the connection when the test context expired. In the unmodified tests the test goroutine's own waits are context-bounded, so the practical exposure is a goroutine/socket held until those waits time out and, if the test goroutine itself were ever to block, until the `go test` timeout.

### C2 on evalon/grpc-go-tr-c15666d0

Target: [evalon/grpc-go-tr-c15666d0](https://github.com/kaitranntt-evals/grpc-go-transport-restrict-memory-overhead/tree/evalon/grpc-go-tr-c15666d0) at `481647e20b9f144cd348274a74e42131a7c60eee`. Added test with a fake HTTP/2 server: `TestClientTransport_SlowReaderTinyDataFrames` in `internal/transport/recv_buffer_test.go`. The helper is `serveTinyDataFrames(lis, numFrames)`; the caller owns `defer lis.Close()` and `defer ct.Close(…)`.

```sh
cd ~/repos/grpc-go                       # checkout of the verify branch; V points at its verify/ directory
V=~/repos/grpc-go/verify
git remote add claims https://github.com/kaitranntt-evals/grpc-go-transport-restrict-memory-overhead
git fetch claims evalon/grpc-go-tr-c15666d0
git worktree add --detach ~/wt/c15666d0 FETCH_HEAD    # 481647e2
cd ~/wt/c15666d0
```

```sh
$V/repro/c2_unbounded_fake_server_socket_ops.sh ~/wt/c15666d0 c15666d0 none accept-main-blocked accept headers headers-main-blocked handshake
```

The script prints the deadline grep, applies `verify/probes/c2/` (test files only; the resulting diff is `verify/probes/c2/c2_c15666d0.patch`), runs each mode with the `go test` command shown on the `$` lines below, and reverts. Output per mode (key lines; the `exit=`/`wall=` lines come from the wrapper loop the runs were originally captured with):

```console
$ grep -c 'SetDeadline\|SetReadDeadline\|SetWriteDeadline\|DialTimeout\|AfterFunc' internal/transport/recv_buffer_test.go
0
$ VERIFY_STALL=none go test -v -run 'Test/ClientTransport_SlowReaderTinyDataFrames$' google.golang.org/grpc/internal/transport -count=1 -timeout 180s
VERIFY C2 [t+  0.0s] fake server goroutine returned 0.0s after it started: err=<nil>
VERIFY C2 [t+  0.0s] fake server goroutine returned 0.0s after it started: err=<nil>
    recv_buffer_test.go:430: stream footprint after 16384 one-byte DATA frames: compaction enabled: 5 entries / 22432 bytes; disabled: 16383 entries / 540639 bytes
--- PASS: Test (0.04s)
    --- PASS: Test/ClientTransport_SlowReaderTinyDataFrames (0.04s)
        --- PASS: Test/ClientTransport_SlowReaderTinyDataFrames/compaction_enabled (0.02s)
        --- PASS: Test/ClientTransport_SlowReaderTinyDataFrames/compaction_disabled (0.02s)
--- PASS: TestConnectionError_Unwrap (0.00s)
--- PASS: Test (0.00s)
PASS
ok  	google.golang.org/grpc/internal/transport	0.046s
exit=0 wall=.526365752s
$ VERIFY_STALL=accept-main-blocked go test -v -run 'Test/ClientTransport_SlowReaderTinyDataFrames$' google.golang.org/grpc/internal/transport -count=1 -timeout 180s
VERIFY C2 [t+  0.0s] client is pointed at a decoy listener; the fake server's lis.Accept() now stalls (no deadline)
VERIFY C2 [t+  0.0s] test goroutine: parked for 25s; no deferred Close can run until it wakes
VERIFY C2 [t+ 25.1s] test goroutine: resumed
    recv_buffer_test.go:371: NewHTTP2Client() = connection error: desc = "transport: Error while dialing: dial tcp 127.0.0.1:41573: i/o timeout"
VERIFY C2 [t+ 25.1s] fake server goroutine returned 25.1s after it started: err=accept tcp 127.0.0.1:34181: use of closed network connection
VERIFY C2 [t+ 25.1s] client is pointed at a decoy listener; the fake server's lis.Accept() now stalls (no deadline)
VERIFY C2 [t+ 25.1s] test goroutine: parked for 25s; no deferred Close can run until it wakes
VERIFY C2 [t+ 50.1s] test goroutine: resumed
    recv_buffer_test.go:371: NewHTTP2Client() = connection error: desc = "transport: Error while dialing: dial tcp 127.0.0.1:39933: i/o timeout"
VERIFY C2 [t+ 50.1s] fake server goroutine returned 25.1s after it started: err=accept tcp 127.0.0.1:38727: use of closed network connection
--- FAIL: Test (50.13s)
    --- FAIL: Test/ClientTransport_SlowReaderTinyDataFrames (50.13s)
        --- FAIL: Test/ClientTransport_SlowReaderTinyDataFrames/compaction_enabled (25.05s)
        --- FAIL: Test/ClientTransport_SlowReaderTinyDataFrames/compaction_disabled (25.07s)
--- PASS: TestConnectionError_Unwrap (0.00s)
--- PASS: Test (0.00s)
FAIL
FAIL	google.golang.org/grpc/internal/transport	50.140s
FAIL
exit=1 wall=52.821346246s
$ VERIFY_STALL=accept go test -v -run 'Test/ClientTransport_SlowReaderTinyDataFrames$' google.golang.org/grpc/internal/transport -count=1 -timeout 180s
VERIFY C2 [t+  0.0s] client is pointed at a decoy listener; the fake server's lis.Accept() now stalls (no deadline)
    recv_buffer_test.go:371: NewHTTP2Client() = connection error: desc = "error reading server preface: read tcp 127.0.0.1:37360->127.0.0.1:32799: use of closed network connection"
VERIFY C2 [t+ 10.0s] fake server goroutine returned 10.0s after it started: err=accept tcp 127.0.0.1:44773: use of closed network connection
VERIFY C2 [t+ 10.0s] client is pointed at a decoy listener; the fake server's lis.Accept() now stalls (no deadline)
    recv_buffer_test.go:371: NewHTTP2Client() = connection error: desc = "error reading server preface: read tcp 127.0.0.1:38558->127.0.0.1:35493: use of closed network connection"
VERIFY C2 [t+ 20.1s] fake server goroutine returned 10.0s after it started: err=accept tcp 127.0.0.1:34469: use of closed network connection
--- FAIL: Test (20.05s)
    --- FAIL: Test/ClientTransport_SlowReaderTinyDataFrames (20.05s)
        --- FAIL: Test/ClientTransport_SlowReaderTinyDataFrames/compaction_enabled (10.00s)
        --- FAIL: Test/ClientTransport_SlowReaderTinyDataFrames/compaction_disabled (10.05s)
--- PASS: TestConnectionError_Unwrap (0.00s)
--- PASS: Test (0.00s)
FAIL
FAIL	google.golang.org/grpc/internal/transport	20.057s
FAIL
exit=1 wall=20.410022822s
$ VERIFY_STALL=headers go test -v -run 'Test/ClientTransport_SlowReaderTinyDataFrames$' google.golang.org/grpc/internal/transport -count=1 -timeout 180s
VERIFY C2 [t+  0.0s] fake server: entering stalled socket read (instead of answering HEADERS), no deadline set
VERIFY C2 [t+ 10.0s] fake server: stalled socket read (instead of answering HEADERS) was interrupted after 10.0s: n=0 err=EOF
VERIFY C2 [t+ 10.0s] fake server goroutine returned 10.0s after it started: err=verify: stalled read ended: EOF
    recv_buffer_test.go:391: fake server failed: verify: stalled read ended: EOF
VERIFY C2 [t+ 10.0s] fake server: entering stalled socket read (instead of answering HEADERS), no deadline set
VERIFY C2 [t+ 20.0s] fake server: stalled socket read (instead of answering HEADERS) was interrupted after 10.0s: n=0 err=EOF
VERIFY C2 [t+ 20.0s] fake server goroutine returned 10.0s after it started: err=verify: stalled read ended: EOF
    recv_buffer_test.go:391: fake server failed: verify: stalled read ended: EOF
--- FAIL: Test (20.00s)
    --- FAIL: Test/ClientTransport_SlowReaderTinyDataFrames (20.00s)
        --- FAIL: Test/ClientTransport_SlowReaderTinyDataFrames/compaction_enabled (10.00s)
        --- FAIL: Test/ClientTransport_SlowReaderTinyDataFrames/compaction_disabled (10.00s)
--- PASS: TestConnectionError_Unwrap (0.00s)
--- PASS: Test (0.00s)
FAIL
FAIL	google.golang.org/grpc/internal/transport	20.009s
FAIL
exit=1 wall=20.388759017s
$ VERIFY_STALL=headers-main-blocked go test -v -run 'Test/ClientTransport_SlowReaderTinyDataFrames$' google.golang.org/grpc/internal/transport -count=1 -timeout 180s
VERIFY C2 [t+  0.0s] test goroutine: parked for 25s; no deferred Close can run until it wakes
VERIFY C2 [t+  0.0s] fake server: entering stalled socket read (instead of answering HEADERS), no deadline set
VERIFY C2 [t+ 10.0s] fake server: stalled socket read (instead of answering HEADERS) was interrupted after 10.0s: n=0 err=EOF
VERIFY C2 [t+ 10.0s] fake server goroutine returned 10.0s after it started: err=verify: stalled read ended: EOF
VERIFY C2 [t+ 25.0s] test goroutine: resumed
    recv_buffer_test.go:396: backlog holds 0 payload bytes, want 16383 (ctx error: context deadline exceeded)
VERIFY C2 [t+ 25.0s] test goroutine: parked for 25s; no deferred Close can run until it wakes
VERIFY C2 [t+ 25.0s] fake server: entering stalled socket read (instead of answering HEADERS), no deadline set
VERIFY C2 [t+ 35.0s] fake server: stalled socket read (instead of answering HEADERS) was interrupted after 10.0s: n=0 err=EOF
VERIFY C2 [t+ 35.0s] fake server goroutine returned 10.0s after it started: err=verify: stalled read ended: EOF
VERIFY C2 [t+ 50.0s] test goroutine: resumed
    recv_buffer_test.go:396: backlog holds 0 payload bytes, want 16383 (ctx error: context deadline exceeded)
--- FAIL: Test (50.04s)
    --- FAIL: Test/ClientTransport_SlowReaderTinyDataFrames (50.04s)
        --- FAIL: Test/ClientTransport_SlowReaderTinyDataFrames/compaction_enabled (25.02s)
        --- FAIL: Test/ClientTransport_SlowReaderTinyDataFrames/compaction_disabled (25.02s)
--- PASS: TestConnectionError_Unwrap (0.00s)
--- PASS: Test (0.00s)
FAIL
FAIL	google.golang.org/grpc/internal/transport	50.044s
FAIL
exit=1 wall=50.657741010s
$ VERIFY_STALL=handshake go test -v -run 'Test/ClientTransport_SlowReaderTinyDataFrames$' google.golang.org/grpc/internal/transport -count=1 -timeout 180s
VERIFY C2 [t+  0.0s] fake server: entering stalled socket read (instead of sending SETTINGS), no deadline set
    recv_buffer_test.go:371: NewHTTP2Client() = connection error: desc = "error reading server preface: read tcp 127.0.0.1:53180->127.0.0.1:38989: use of closed network connection"
VERIFY C2 [t+ 10.0s] fake server: stalled socket read (instead of sending SETTINGS) was interrupted after 10.0s: n=9 err=unexpected EOF
VERIFY C2 [t+ 10.0s] fake server goroutine returned 10.0s after it started: err=verify: stalled read ended: unexpected EOF
VERIFY C2 [t+ 10.0s] fake server: entering stalled socket read (instead of sending SETTINGS), no deadline set
    recv_buffer_test.go:371: NewHTTP2Client() = connection error: desc = "error reading server preface: read tcp 127.0.0.1:52182->127.0.0.1:37605: use of closed network connection"
VERIFY C2 [t+ 20.0s] fake server: stalled socket read (instead of sending SETTINGS) was interrupted after 10.0s: n=9 err=unexpected EOF
VERIFY C2 [t+ 20.0s] fake server goroutine returned 10.0s after it started: err=verify: stalled read ended: unexpected EOF
--- FAIL: Test (20.10s)
    --- FAIL: Test/ClientTransport_SlowReaderTinyDataFrames (20.10s)
        --- FAIL: Test/ClientTransport_SlowReaderTinyDataFrames/compaction_enabled (10.05s)
        --- FAIL: Test/ClientTransport_SlowReaderTinyDataFrames/compaction_disabled (10.00s)
--- PASS: TestConnectionError_Unwrap (0.00s)
--- PASS: Test (0.00s)
FAIL
FAIL	google.golang.org/grpc/internal/transport	20.102s
FAIL
exit=1 wall=20.472324809s
```

### C2 on evalon/grpc-go-tr-7b7bc477

Target: [evalon/grpc-go-tr-7b7bc477](https://github.com/kaitranntt-evals/grpc-go-transport-restrict-memory-overhead/tree/evalon/grpc-go-tr-7b7bc477) at `467b8c74fa2d842dead352bfb9dae43209ccf9d2`. Added test with a fake HTTP/2 server: `TestClientStream_ManyTinyDataFramesSlowReader` in `internal/transport/recvbuffer_test.go`. There is no `serveTinyDataFrames` on this branch; the fake server is an inline goroutine in `testClientStreamManyTinyDataFrames` (`lis.Accept()`, `io.ReadFull`, `sfr.ReadFrame()`, `io.Copy(io.Discard, sconn)`), with `defer lis.Close()`, `defer sconn.Close()` and `defer ct.Close(…)` as the only closers.

```sh
cd ~/repos/grpc-go                       # checkout of the verify branch; V points at its verify/ directory
V=~/repos/grpc-go/verify
git remote add claims https://github.com/kaitranntt-evals/grpc-go-transport-restrict-memory-overhead
git fetch claims evalon/grpc-go-tr-7b7bc477
git worktree add --detach ~/wt/7b7bc477 FETCH_HEAD    # 467b8c74
cd ~/wt/7b7bc477
```

```sh
$V/repro/c2_unbounded_fake_server_socket_ops.sh ~/wt/7b7bc477 7b7bc477 none accept-main-blocked accept headers headers-main-blocked handshake
```

The script prints the deadline grep, applies `verify/probes/c2/` (test files only; the resulting diff is `verify/probes/c2/c2_7b7bc477.patch`), runs each mode with the `go test` command shown on the `$` lines below, and reverts. Output per mode (key lines; the `exit=`/`wall=` lines come from the wrapper loop the runs were originally captured with):

```console
$ grep -c 'SetDeadline\|SetReadDeadline\|SetWriteDeadline\|DialTimeout\|AfterFunc' internal/transport/recvbuffer_test.go
0
$ VERIFY_STALL=none go test -v -run 'Test/ClientStream_ManyTinyDataFramesSlowReader$' google.golang.org/grpc/internal/transport -count=1 -timeout 180s
VERIFY C2 [t+  0.0s] fake server goroutine returned 0.0s after it started: err=<nil>
VERIFY C2 [t+  0.1s] fake server goroutine returned 0.0s after it started: err=<nil>
--- PASS: Test (0.07s)
    --- PASS: Test/ClientStream_ManyTinyDataFramesSlowReader (0.07s)
        --- PASS: Test/ClientStream_ManyTinyDataFramesSlowReader/compaction=true (0.03s)
        --- PASS: Test/ClientStream_ManyTinyDataFramesSlowReader/compaction=false (0.04s)
--- PASS: TestConnectionError_Unwrap (0.00s)
--- PASS: Test (0.00s)
PASS
ok  	google.golang.org/grpc/internal/transport	0.076s
exit=0 wall=.557437132s
$ VERIFY_STALL=accept-main-blocked go test -v -run 'Test/ClientStream_ManyTinyDataFramesSlowReader$' google.golang.org/grpc/internal/transport -count=1 -timeout 180s
VERIFY C2 [t+  0.0s] client is pointed at a decoy listener; the fake server's lis.Accept() now stalls (no deadline)
VERIFY C2 [t+  0.0s] test goroutine: parked for 25s; no deferred Close can run until it wakes
VERIFY C2 [t+ 25.1s] test goroutine: resumed
    recvbuffer_test.go:311: NewHTTP2Client() failed: connection error: desc = "transport: Error while dialing: dial tcp 127.0.0.1:40293: i/o timeout"
VERIFY C2 [t+ 25.1s] fake server goroutine returned 25.1s after it started: err=accept: accept tcp 127.0.0.1:46325: use of closed network connection
VERIFY C2 [t+ 25.1s] client is pointed at a decoy listener; the fake server's lis.Accept() now stalls (no deadline)
VERIFY C2 [t+ 25.1s] test goroutine: parked for 25s; no deferred Close can run until it wakes
VERIFY C2 [t+ 50.1s] test goroutine: resumed
    recvbuffer_test.go:311: NewHTTP2Client() failed: connection error: desc = "transport: Error while dialing: dial tcp 127.0.0.1:39889: i/o timeout"
VERIFY C2 [t+ 50.1s] fake server goroutine returned 25.0s after it started: err=accept: accept tcp 127.0.0.1:36411: use of closed network connection
--- FAIL: Test (50.08s)
    --- FAIL: Test/ClientStream_ManyTinyDataFramesSlowReader (50.08s)
        --- FAIL: Test/ClientStream_ManyTinyDataFramesSlowReader/compaction=true (25.08s)
        --- FAIL: Test/ClientStream_ManyTinyDataFramesSlowReader/compaction=false (25.00s)
--- PASS: TestConnectionError_Unwrap (0.00s)
--- PASS: Test (0.00s)
FAIL
FAIL	google.golang.org/grpc/internal/transport	50.085s
FAIL
exit=1 wall=52.706501342s
$ VERIFY_STALL=accept go test -v -run 'Test/ClientStream_ManyTinyDataFramesSlowReader$' google.golang.org/grpc/internal/transport -count=1 -timeout 180s
VERIFY C2 [t+  0.0s] client is pointed at a decoy listener; the fake server's lis.Accept() now stalls (no deadline)
    recvbuffer_test.go:311: NewHTTP2Client() failed: connection error: desc = "error reading server preface: read tcp 127.0.0.1:53204->127.0.0.1:42345: use of closed network connection"
VERIFY C2 [t+ 10.0s] fake server goroutine returned 10.0s after it started: err=accept: accept tcp 127.0.0.1:39437: use of closed network connection
VERIFY C2 [t+ 10.0s] client is pointed at a decoy listener; the fake server's lis.Accept() now stalls (no deadline)
    recvbuffer_test.go:311: NewHTTP2Client() failed: connection error: desc = "error reading server preface: read tcp 127.0.0.1:38000->127.0.0.1:33813: use of closed network connection"
VERIFY C2 [t+ 20.1s] fake server goroutine returned 10.0s after it started: err=accept: accept tcp 127.0.0.1:41183: use of closed network connection
--- FAIL: Test (20.08s)
    --- FAIL: Test/ClientStream_ManyTinyDataFramesSlowReader (20.08s)
        --- FAIL: Test/ClientStream_ManyTinyDataFramesSlowReader/compaction=true (10.03s)
        --- FAIL: Test/ClientStream_ManyTinyDataFramesSlowReader/compaction=false (10.05s)
--- PASS: TestConnectionError_Unwrap (0.00s)
--- PASS: Test (0.00s)
FAIL
FAIL	google.golang.org/grpc/internal/transport	20.085s
FAIL
exit=1 wall=20.450962533s
$ VERIFY_STALL=headers go test -v -run 'Test/ClientStream_ManyTinyDataFramesSlowReader$' google.golang.org/grpc/internal/transport -count=1 -timeout 180s
VERIFY C2 [t+  0.0s] fake server: entering stalled socket read (instead of answering HEADERS), no deadline set
    recvbuffer_test.go:327: Timed out waiting for the stream to complete
VERIFY C2 [t+ 10.0s] fake server: stalled socket read (instead of answering HEADERS) was interrupted after 10.0s: n=0 err=EOF
VERIFY C2 [t+ 10.0s] fake server goroutine returned 10.0s after it started: err=verify: stalled read ended: EOF
VERIFY C2 [t+ 10.0s] fake server: entering stalled socket read (instead of answering HEADERS), no deadline set
    recvbuffer_test.go:327: Timed out waiting for the stream to complete
VERIFY C2 [t+ 20.0s] fake server: stalled socket read (instead of answering HEADERS) was interrupted after 10.0s: n=0 err=EOF
VERIFY C2 [t+ 20.0s] fake server goroutine returned 10.0s after it started: err=verify: stalled read ended: EOF
--- FAIL: Test (20.06s)
    --- FAIL: Test/ClientStream_ManyTinyDataFramesSlowReader (20.06s)
        --- FAIL: Test/ClientStream_ManyTinyDataFramesSlowReader/compaction=true (10.01s)
        --- FAIL: Test/ClientStream_ManyTinyDataFramesSlowReader/compaction=false (10.00s)
--- PASS: TestConnectionError_Unwrap (0.00s)
--- PASS: Test (0.00s)
FAIL
FAIL	google.golang.org/grpc/internal/transport	20.066s
FAIL
exit=1 wall=20.463168335s
$ VERIFY_STALL=headers-main-blocked go test -v -run 'Test/ClientStream_ManyTinyDataFramesSlowReader$' google.golang.org/grpc/internal/transport -count=1 -timeout 180s
VERIFY C2 [t+  0.0s] test goroutine: parked for 25s; no deferred Close can run until it wakes
VERIFY C2 [t+  0.0s] fake server: entering stalled socket read (instead of answering HEADERS), no deadline set
VERIFY C2 [t+ 10.0s] fake server: stalled socket read (instead of answering HEADERS) was interrupted after 10.0s: n=0 err=EOF
VERIFY C2 [t+ 10.0s] fake server goroutine returned 10.0s after it started: err=verify: stalled read ended: EOF
VERIFY C2 [t+ 25.0s] test goroutine: resumed
    recvbuffer_test.go:327: Timed out waiting for the stream to complete
VERIFY C2 [t+ 25.0s] test goroutine: parked for 25s; no deferred Close can run until it wakes
VERIFY C2 [t+ 25.0s] fake server: entering stalled socket read (instead of answering HEADERS), no deadline set
VERIFY C2 [t+ 35.0s] fake server: stalled socket read (instead of answering HEADERS) was interrupted after 10.0s: n=0 err=EOF
VERIFY C2 [t+ 35.0s] fake server goroutine returned 10.0s after it started: err=verify: stalled read ended: EOF
VERIFY C2 [t+ 50.0s] test goroutine: resumed
    recvbuffer_test.go:325: Server failed: verify: stalled read ended: EOF
--- FAIL: Test (50.04s)
    --- FAIL: Test/ClientStream_ManyTinyDataFramesSlowReader (50.04s)
        --- FAIL: Test/ClientStream_ManyTinyDataFramesSlowReader/compaction=true (25.01s)
        --- FAIL: Test/ClientStream_ManyTinyDataFramesSlowReader/compaction=false (25.03s)
--- PASS: TestConnectionError_Unwrap (0.00s)
--- PASS: Test (0.00s)
FAIL
FAIL	google.golang.org/grpc/internal/transport	50.042s
FAIL
exit=1 wall=50.656522872s
$ VERIFY_STALL=handshake go test -v -run 'Test/ClientStream_ManyTinyDataFramesSlowReader$' google.golang.org/grpc/internal/transport -count=1 -timeout 180s
VERIFY C2 [t+  0.0s] fake server: entering stalled socket read (instead of sending SETTINGS), no deadline set
VERIFY C2 [t+ 10.0s] fake server: stalled socket read (instead of sending SETTINGS) was interrupted after 10.0s: n=9 err=unexpected EOF
    recvbuffer_test.go:311: NewHTTP2Client() failed: connection error: desc = "error reading server preface: read tcp 127.0.0.1:40878->127.0.0.1:40823: use of closed network connection"
VERIFY C2 [t+ 10.0s] fake server goroutine returned 10.0s after it started: err=verify: stalled read ended: unexpected EOF
VERIFY C2 [t+ 10.0s] fake server: entering stalled socket read (instead of sending SETTINGS), no deadline set
    recvbuffer_test.go:311: NewHTTP2Client() failed: connection error: desc = "error reading server preface: read tcp 127.0.0.1:60072->127.0.0.1:42285: use of closed network connection"
VERIFY C2 [t+ 20.0s] fake server: stalled socket read (instead of sending SETTINGS) was interrupted after 10.0s: n=9 err=unexpected EOF
VERIFY C2 [t+ 20.0s] fake server goroutine returned 10.0s after it started: err=verify: stalled read ended: unexpected EOF
--- FAIL: Test (20.05s)
    --- FAIL: Test/ClientStream_ManyTinyDataFramesSlowReader (20.05s)
        --- FAIL: Test/ClientStream_ManyTinyDataFramesSlowReader/compaction=true (10.05s)
        --- FAIL: Test/ClientStream_ManyTinyDataFramesSlowReader/compaction=false (10.00s)
--- PASS: TestConnectionError_Unwrap (0.00s)
--- PASS: Test (0.00s)
FAIL
FAIL	google.golang.org/grpc/internal/transport	20.054s
FAIL
exit=1 wall=20.424457987s
```

### C2 on evalon/grpc-go-tr-9841642e

Target: [evalon/grpc-go-tr-9841642e](https://github.com/kaitranntt-evals/grpc-go-transport-restrict-memory-overhead/tree/evalon/grpc-go-tr-9841642e) at `f97244434e6935ee415539215f81f6e0421b8cbf`. Added test with a fake HTTP/2 server: `TestClientTinyDataFramesReceiveMemory` in `internal/transport/recv_buffer_test.go`. The helper is `serveTinyDataFrames(lis, numFrames)`; the caller owns `defer lis.Close()` and `defer ct.Close(…)`.

```sh
cd ~/repos/grpc-go                       # checkout of the verify branch; V points at its verify/ directory
V=~/repos/grpc-go/verify
git remote add claims https://github.com/kaitranntt-evals/grpc-go-transport-restrict-memory-overhead
git fetch claims evalon/grpc-go-tr-9841642e
git worktree add --detach ~/wt/9841642e FETCH_HEAD    # f9724443
cd ~/wt/9841642e
```

```sh
$V/repro/c2_unbounded_fake_server_socket_ops.sh ~/wt/9841642e 9841642e none accept-main-blocked accept headers headers-main-blocked handshake
```

The script prints the deadline grep, applies `verify/probes/c2/` (test files only; the resulting diff is `verify/probes/c2/c2_9841642e.patch`), runs each mode with the `go test` command shown on the `$` lines below, and reverts. Output per mode (key lines; the `exit=`/`wall=` lines come from the wrapper loop the runs were originally captured with):

```console
$ grep -c 'SetDeadline\|SetReadDeadline\|SetWriteDeadline\|DialTimeout\|AfterFunc' internal/transport/recv_buffer_test.go
0
$ VERIFY_STALL=none go test -v -run 'Test/ClientTinyDataFramesReceiveMemory$' google.golang.org/grpc/internal/transport -count=1 -timeout 180s
VERIFY C2 [t+  0.1s] fake server goroutine returned 0.1s after it started: err=<nil>
VERIFY C2 [t+  0.1s] fake server goroutine returned 0.1s after it started: err=<nil>
--- PASS: Test (0.15s)
    --- PASS: Test/ClientTinyDataFramesReceiveMemory (0.15s)
        --- PASS: Test/ClientTinyDataFramesReceiveMemory/compaction=true (0.06s)
        --- PASS: Test/ClientTinyDataFramesReceiveMemory/compaction=false (0.09s)
--- PASS: TestConnectionError_Unwrap (0.00s)
--- PASS: Test (0.00s)
PASS
ok  	google.golang.org/grpc/internal/transport	0.152s
exit=0 wall=.633424699s
$ VERIFY_STALL=accept-main-blocked go test -v -run 'Test/ClientTinyDataFramesReceiveMemory$' google.golang.org/grpc/internal/transport -count=1 -timeout 180s
VERIFY C2 [t+  0.0s] client is pointed at a decoy listener; the fake server's lis.Accept() now stalls (no deadline)
VERIFY C2 [t+  0.0s] test goroutine: parked for 25s; no deferred Close can run until it wakes
VERIFY C2 [t+ 25.0s] test goroutine: resumed
    recv_buffer_test.go:300: Error while creating client transport: connection error: desc = "transport: Error while dialing: dial tcp 127.0.0.1:35261: i/o timeout"
VERIFY C2 [t+ 25.0s] fake server goroutine returned 25.0s after it started: err=error while accepting: accept tcp 127.0.0.1:44389: use of closed network connection
VERIFY C2 [t+ 25.0s] client is pointed at a decoy listener; the fake server's lis.Accept() now stalls (no deadline)
VERIFY C2 [t+ 25.0s] test goroutine: parked for 25s; no deferred Close can run until it wakes
VERIFY C2 [t+ 50.1s] test goroutine: resumed
    recv_buffer_test.go:300: Error while creating client transport: connection error: desc = "transport: Error while dialing: dial tcp 127.0.0.1:44813: i/o timeout"
VERIFY C2 [t+ 50.1s] fake server goroutine returned 25.1s after it started: err=error while accepting: accept tcp 127.0.0.1:43233: use of closed network connection
--- FAIL: Test (50.07s)
    --- FAIL: Test/ClientTinyDataFramesReceiveMemory (50.07s)
        --- FAIL: Test/ClientTinyDataFramesReceiveMemory/compaction=true (25.01s)
        --- FAIL: Test/ClientTinyDataFramesReceiveMemory/compaction=false (25.06s)
--- PASS: TestConnectionError_Unwrap (0.00s)
--- PASS: Test (0.00s)
FAIL
FAIL	google.golang.org/grpc/internal/transport	50.077s
FAIL
exit=1 wall=52.904000348s
$ VERIFY_STALL=accept go test -v -run 'Test/ClientTinyDataFramesReceiveMemory$' google.golang.org/grpc/internal/transport -count=1 -timeout 180s
VERIFY C2 [t+  0.0s] client is pointed at a decoy listener; the fake server's lis.Accept() now stalls (no deadline)
    recv_buffer_test.go:300: Error while creating client transport: connection error: desc = "error reading server preface: read tcp 127.0.0.1:53840->127.0.0.1:43095: use of closed network connection"
VERIFY C2 [t+ 10.1s] fake server goroutine returned 10.0s after it started: err=error while accepting: accept tcp 127.0.0.1:42159: use of closed network connection
VERIFY C2 [t+ 10.1s] client is pointed at a decoy listener; the fake server's lis.Accept() now stalls (no deadline)
    recv_buffer_test.go:300: Error while creating client transport: connection error: desc = "error reading server preface: read tcp 127.0.0.1:33982->127.0.0.1:37493: use of closed network connection"
VERIFY C2 [t+ 20.1s] fake server goroutine returned 10.0s after it started: err=error while accepting: accept tcp 127.0.0.1:45567: use of closed network connection
--- FAIL: Test (20.09s)
    --- FAIL: Test/ClientTinyDataFramesReceiveMemory (20.09s)
        --- FAIL: Test/ClientTinyDataFramesReceiveMemory/compaction=true (10.05s)
        --- FAIL: Test/ClientTinyDataFramesReceiveMemory/compaction=false (10.04s)
--- PASS: TestConnectionError_Unwrap (0.00s)
--- PASS: Test (0.00s)
FAIL
FAIL	google.golang.org/grpc/internal/transport	20.094s
FAIL
exit=1 wall=20.460791805s
$ VERIFY_STALL=headers go test -v -run 'Test/ClientTinyDataFramesReceiveMemory$' google.golang.org/grpc/internal/transport -count=1 -timeout 180s
VERIFY C2 [t+  0.0s] fake server: entering stalled socket read (instead of answering HEADERS), no deadline set
VERIFY C2 [t+ 10.0s] fake server: stalled socket read (instead of answering HEADERS) was interrupted after 10.0s: n=0 err=EOF
VERIFY C2 [t+ 10.0s] fake server goroutine returned 10.0s after it started: err=verify: stalled read ended: EOF
    recv_buffer_test.go:315: Timed out waiting for data: backlog has 0 entries holding 0 bytes, want 64534 bytes
VERIFY C2 [t+ 10.0s] fake server: entering stalled socket read (instead of answering HEADERS), no deadline set
    recv_buffer_test.go:315: Timed out waiting for data: backlog has 0 entries holding 0 bytes, want 64534 bytes
VERIFY C2 [t+ 20.0s] fake server: stalled socket read (instead of answering HEADERS) was interrupted after 10.0s: n=42 err=unexpected EOF
VERIFY C2 [t+ 20.0s] fake server goroutine returned 10.0s after it started: err=verify: stalled read ended: unexpected EOF
--- FAIL: Test (20.05s)
    --- FAIL: Test/ClientTinyDataFramesReceiveMemory (20.05s)
        --- FAIL: Test/ClientTinyDataFramesReceiveMemory/compaction=true (10.00s)
        --- FAIL: Test/ClientTinyDataFramesReceiveMemory/compaction=false (10.00s)
--- PASS: TestConnectionError_Unwrap (0.00s)
--- PASS: Test (0.00s)
FAIL
FAIL	google.golang.org/grpc/internal/transport	20.061s
FAIL
exit=1 wall=20.457653852s
$ VERIFY_STALL=headers-main-blocked go test -v -run 'Test/ClientTinyDataFramesReceiveMemory$' google.golang.org/grpc/internal/transport -count=1 -timeout 180s
VERIFY C2 [t+  0.0s] test goroutine: parked for 25s; no deferred Close can run until it wakes
VERIFY C2 [t+  0.0s] fake server: entering stalled socket read (instead of answering HEADERS), no deadline set
VERIFY C2 [t+ 10.0s] fake server: stalled socket read (instead of answering HEADERS) was interrupted after 10.0s: n=0 err=EOF
VERIFY C2 [t+ 10.0s] fake server goroutine returned 10.0s after it started: err=verify: stalled read ended: EOF
VERIFY C2 [t+ 25.1s] test goroutine: resumed
    recv_buffer_test.go:319: Server failed: verify: stalled read ended: EOF
VERIFY C2 [t+ 25.1s] test goroutine: parked for 25s; no deferred Close can run until it wakes
VERIFY C2 [t+ 25.1s] fake server: entering stalled socket read (instead of answering HEADERS), no deadline set
VERIFY C2 [t+ 35.1s] fake server: stalled socket read (instead of answering HEADERS) was interrupted after 10.0s: n=0 err=EOF
VERIFY C2 [t+ 35.1s] fake server goroutine returned 10.0s after it started: err=verify: stalled read ended: EOF
VERIFY C2 [t+ 50.1s] test goroutine: resumed
    recv_buffer_test.go:319: Server failed: verify: stalled read ended: EOF
--- FAIL: Test (50.12s)
    --- FAIL: Test/ClientTinyDataFramesReceiveMemory (50.12s)
        --- FAIL: Test/ClientTinyDataFramesReceiveMemory/compaction=true (25.08s)
        --- FAIL: Test/ClientTinyDataFramesReceiveMemory/compaction=false (25.05s)
--- PASS: TestConnectionError_Unwrap (0.00s)
--- PASS: Test (0.00s)
FAIL
FAIL	google.golang.org/grpc/internal/transport	50.130s
FAIL
exit=1 wall=50.738510370s
$ VERIFY_STALL=handshake go test -v -run 'Test/ClientTinyDataFramesReceiveMemory$' google.golang.org/grpc/internal/transport -count=1 -timeout 180s
VERIFY C2 [t+  0.0s] fake server: entering stalled socket read (instead of sending SETTINGS), no deadline set
    recv_buffer_test.go:300: Error while creating client transport: connection error: desc = "error reading server preface: read tcp 127.0.0.1:51322->127.0.0.1:34769: use of closed network connection"
VERIFY C2 [t+ 10.0s] fake server: stalled socket read (instead of sending SETTINGS) was interrupted after 10.0s: n=9 err=unexpected EOF
VERIFY C2 [t+ 10.0s] fake server goroutine returned 10.0s after it started: err=verify: stalled read ended: unexpected EOF
VERIFY C2 [t+ 10.0s] fake server: entering stalled socket read (instead of sending SETTINGS), no deadline set
VERIFY C2 [t+ 20.0s] fake server: stalled socket read (instead of sending SETTINGS) was interrupted after 10.0s: n=9 err=unexpected EOF
VERIFY C2 [t+ 20.0s] fake server goroutine returned 10.0s after it started: err=verify: stalled read ended: unexpected EOF
    recv_buffer_test.go:300: Error while creating client transport: connection error: desc = "error reading server preface: read tcp 127.0.0.1:41268->127.0.0.1:45087: use of closed network connection"
--- FAIL: Test (20.04s)
    --- FAIL: Test/ClientTinyDataFramesReceiveMemory (20.04s)
        --- FAIL: Test/ClientTinyDataFramesReceiveMemory/compaction=true (10.03s)
        --- FAIL: Test/ClientTinyDataFramesReceiveMemory/compaction=false (10.00s)
--- PASS: TestConnectionError_Unwrap (0.00s)
--- PASS: Test (0.00s)
FAIL
FAIL	google.golang.org/grpc/internal/transport	20.041s
FAIL
exit=1 wall=20.421302759s
```

## C3

Target: [evalon/grpc-go-tr-62b3e09b](https://github.com/kaitranntt-evals/grpc-go-transport-restrict-memory-overhead/tree/evalon/grpc-go-tr-62b3e09b) at `3ab604e5f663850346ee5b9cb6c5423c16f50e7e`.

```sh
cd ~/repos/grpc-go                       # checkout of the verify branch; V points at its verify/ directory
V=~/repos/grpc-go/verify
git remote add claims https://github.com/kaitranntt-evals/grpc-go-transport-restrict-memory-overhead
git fetch claims evalon/grpc-go-tr-62b3e09b
git worktree add --detach ~/wt/62b3e09b FETCH_HEAD    # 3ab604e5
cd ~/wt/62b3e09b
```

Naming: `appendToTail()` exists; "recvBuffer initialization" on this branch is two calls, `init()` followed by `enableCompaction(pool)`.

How the solution delivers receive buffering (non-test callers of `init`/`enableCompaction`):

```sh
grep -rn "enableCompaction\|buf.init()" internal/transport/*.go | grep -v _test
```

```console
internal/transport/handler_server.go:427:	s.Stream.buf.init()
internal/transport/handler_server.go:428:	s.Stream.buf.enableCompaction(ht.bufferPool)
internal/transport/http2_client.go:503:	s.Stream.buf.init()
internal/transport/http2_client.go:504:	s.Stream.buf.enableCompaction(t.bufferPool)
internal/transport/http2_server.go:410:	s.Stream.buf.init()
internal/transport/http2_server.go:411:	s.Stream.buf.enableCompaction(t.bufferPool)
internal/transport/transport.go:138:// enableCompaction enables coalescing of small payloads queued behind a slow
internal/transport/transport.go:141:func (b *recvBuffer) enableCompaction(pool mem.BufferPool) {
```

`init()` alone only creates the channel; `put` compacts only when `b.pool` was set by `enableCompaction` (which defaults a nil pool to `mem.DefaultBufferPool()` and is a no-op when the env var disables the feature).

Byte-exact fixture on this branch (the fixture's `initRecvBufferForTest` adapter tries `initWithPool(...)` and `init(...)` signatures and ends at bare `init()`, so it never calls `enableCompaction`):

```sh
cp ~/eval/tests/eval_recv_buffer_compaction_test.go internal/transport/eval_recv_buffer_compaction_test.go
go test -v -run '^TestEval_' google.golang.org/grpc/internal/transport -race -count=1
rm internal/transport/eval_recv_buffer_compaction_test.go
```

```console
$ go test -v -run '^TestEval_' google.golang.org/grpc/internal/transport -race -count=1
    eval_recv_buffer_compaction_test.go:91: Got backlog length 1025 after compaction, want <= 64
--- FAIL: TestEval_RecvBufferCompaction (0.00s)
    eval_recv_buffer_compaction_test.go:125: Got backlog length 1025, want <= 64 (compaction enabled)
--- FAIL: TestEval_RecvBufferCompactionDisabled (0.00s)
--- PASS: TestEval_RecvBufferCompactionSkippedLargeBuffer (0.02s)
--- PASS: TestEval_RecvBufferErrorResetSafety (0.00s)
    eval_recv_buffer_compaction_test.go:305: Got backlog length 1073 after mixed frame compaction, want <= 134
--- FAIL: TestEval_RecvBufferCompaction_MixedFrames (0.00s)
    eval_recv_buffer_compaction_test.go:345: Cycle 0: backlog length 1123 exceeded bound 512
--- FAIL: TestEval_RecvBufferCompaction_MultiCycleMemoryBound (0.00s)
FAIL	google.golang.org/grpc/internal/transport	0.042s
```

The same fixture with only its adapter changed to the two calls the transports make (`verify/probes/c345_62b3e09b_fixture_prodinit_test.go`; tests renamed `TestVerifyProdInitEval_*`):

```sh
cp $V/probes/c345_62b3e09b_fixture_prodinit_test.go internal/transport/verify_c345_fixture_prodinit_test.go
go test -v -run '^TestVerifyProdInitEval_' google.golang.org/grpc/internal/transport -race -count=1
GRPC_GO_EXPERIMENTAL_ENABLE_RECEIVE_BUFFER_COMPACTION=false go test -v -run '^TestVerifyProdInitEval_RecvBufferCompactionDisabled$' google.golang.org/grpc/internal/transport -race -count=1
rm internal/transport/verify_c345_fixture_prodinit_test.go
```

```console
$ go test -v -run '^TestVerifyProdInitEval_' google.golang.org/grpc/internal/transport -race -count=1
--- PASS: TestVerifyProdInitEval_RecvBufferCompaction (0.00s)
--- PASS: TestVerifyProdInitEval_RecvBufferCompactionDisabled (0.00s)
--- PASS: TestVerifyProdInitEval_RecvBufferCompactionSkippedLargeBuffer (0.02s)
--- PASS: TestVerifyProdInitEval_RecvBufferErrorResetSafety (0.00s)
--- PASS: TestVerifyProdInitEval_RecvBufferCompaction_MixedFrames (0.00s)
--- PASS: TestVerifyProdInitEval_RecvBufferCompaction_MultiCycleMemoryBound (0.00s)
ok  	google.golang.org/grpc/internal/transport	1.045s
$ GRPC_GO_EXPERIMENTAL_ENABLE_RECEIVE_BUFFER_COMPACTION=false go test -v -run '^TestVerifyProdInitEval_RecvBufferCompactionDisabled$' google.golang.org/grpc/internal/transport -race -count=1
--- PASS: TestVerifyProdInitEval_RecvBufferCompactionDisabled (0.00s)
ok  	google.golang.org/grpc/internal/transport	1.020s
```

Behaviour of the live transports (`verify/probes/c345_62b3e09b_live_probe_test.go`): a real `http2Client` stream created by `NewHTTP2Client`/`NewStream` against a raw HTTP/2 peer that writes the DATA frames, and a real `http2Server` stream fed by a raw HTTP/2 client; a PING/ack round trip guarantees all frames were processed before the stream's `recvBuffer` is inspected under its mutex. Each test also runs the same workload on a bare `init()` buffer for contrast.

```sh
cp $V/probes/c345_62b3e09b_live_probe_test.go internal/transport/verify_c345_live_probe_test.go
go test -v -run '^TestVerifyC[345]_' google.golang.org/grpc/internal/transport -race -count=1
rm internal/transport/verify_c345_live_probe_test.go
```

```console
$ go test -v -run '^TestVerifyC[345]_' google.golang.org/grpc/internal/transport -race -count=1
=== RUN   TestVerifyC3_BurstNoReads
    verify_c345_live_probe_test.go:227: C3 bare init() only (fixture path), 1026 one-byte puts, no reads: chan=1 backlogEntries=1025 tailBytes=0 unreadPayloadBytes=1026 distinctBackingArrays=1026 largestEntryLen=1
    verify_c345_live_probe_test.go:235: C3 live http2Client stream, 1026 one-byte DATA frames, no reads: chan=1 backlogEntries=0 tailBytes=1025 unreadPayloadBytes=1026 distinctBackingArrays=2 largestEntryLen=1025
    verify_c345_live_probe_test.go:248: C3 live stream delivered all 1026 bytes in order
--- PASS: TestVerifyC3_BurstNoReads (0.01s)
=== RUN   TestVerifyC3_LiveServerStream
    verify_c345_live_probe_test.go:446: C3 live http2Server stream, 1026 one-byte DATA frames, no reads: chan=1 backlogEntries=0 tailBytes=1025 unreadPayloadBytes=1026 distinctBackingArrays=2 largestEntryLen=1025
--- PASS: TestVerifyC3_LiveServerStream (0.01s)
ok  	google.golang.org/grpc/internal/transport	1.070s
```

What the output shows: the one-entry-per-payload state (`backlogEntries=1025`, `distinctBackingArrays=1026`) is reproduced only on a buffer initialized with bare `init()`, which is what the fixture's adapter does and what no transport in the solution does. On a live client stream and on a live server stream, 1026 one-byte DATA frames with no reads leave `backlogEntries=0` and a single 1025-byte pending chunk (2 distinct backing arrays in total, the first frame sitting in the channel), and all 1026 bytes are then delivered in order. The fixture failure measures the adapter/initializer mismatch, not the delivered buffering.

## C4

Target: [evalon/grpc-go-tr-62b3e09b](https://github.com/kaitranntt-evals/grpc-go-transport-restrict-memory-overhead/tree/evalon/grpc-go-tr-62b3e09b) at `3ab604e5f663850346ee5b9cb6c5423c16f50e7e`.

```sh
cd ~/repos/grpc-go                       # checkout of the verify branch; V points at its verify/ directory
V=~/repos/grpc-go/verify
git remote add claims https://github.com/kaitranntt-evals/grpc-go-transport-restrict-memory-overhead
git fetch claims evalon/grpc-go-tr-62b3e09b
git worktree add --detach ~/wt/62b3e09b FETCH_HEAD    # 3ab604e5
cd ~/wt/62b3e09b
```

Naming: `appendToTail()` exists; "recvBuffer initialization" on this branch is two calls, `init()` followed by `enableCompaction(pool)`.

How the solution delivers receive buffering (non-test callers of `init`/`enableCompaction`):

```sh
grep -rn "enableCompaction\|buf.init()" internal/transport/*.go | grep -v _test
```

```console
internal/transport/handler_server.go:427:	s.Stream.buf.init()
internal/transport/handler_server.go:428:	s.Stream.buf.enableCompaction(ht.bufferPool)
internal/transport/http2_client.go:503:	s.Stream.buf.init()
internal/transport/http2_client.go:504:	s.Stream.buf.enableCompaction(t.bufferPool)
internal/transport/http2_server.go:410:	s.Stream.buf.init()
internal/transport/http2_server.go:411:	s.Stream.buf.enableCompaction(t.bufferPool)
internal/transport/transport.go:138:// enableCompaction enables coalescing of small payloads queued behind a slow
internal/transport/transport.go:141:func (b *recvBuffer) enableCompaction(pool mem.BufferPool) {
```

`init()` alone only creates the channel; `put` compacts only when `b.pool` was set by `enableCompaction` (which defaults a nil pool to `mem.DefaultBufferPool()` and is a no-op when the env var disables the feature).

Byte-exact fixture on this branch (the fixture's `initRecvBufferForTest` adapter tries `initWithPool(...)` and `init(...)` signatures and ends at bare `init()`, so it never calls `enableCompaction`):

```sh
cp ~/eval/tests/eval_recv_buffer_compaction_test.go internal/transport/eval_recv_buffer_compaction_test.go
go test -v -run '^TestEval_' google.golang.org/grpc/internal/transport -race -count=1
rm internal/transport/eval_recv_buffer_compaction_test.go
```

```console
$ go test -v -run '^TestEval_' google.golang.org/grpc/internal/transport -race -count=1
    eval_recv_buffer_compaction_test.go:91: Got backlog length 1025 after compaction, want <= 64
--- FAIL: TestEval_RecvBufferCompaction (0.00s)
    eval_recv_buffer_compaction_test.go:125: Got backlog length 1025, want <= 64 (compaction enabled)
--- FAIL: TestEval_RecvBufferCompactionDisabled (0.00s)
--- PASS: TestEval_RecvBufferCompactionSkippedLargeBuffer (0.02s)
--- PASS: TestEval_RecvBufferErrorResetSafety (0.00s)
    eval_recv_buffer_compaction_test.go:305: Got backlog length 1073 after mixed frame compaction, want <= 134
--- FAIL: TestEval_RecvBufferCompaction_MixedFrames (0.00s)
    eval_recv_buffer_compaction_test.go:345: Cycle 0: backlog length 1123 exceeded bound 512
--- FAIL: TestEval_RecvBufferCompaction_MultiCycleMemoryBound (0.00s)
FAIL	google.golang.org/grpc/internal/transport	0.042s
```

The same fixture with only its adapter changed to the two calls the transports make (`verify/probes/c345_62b3e09b_fixture_prodinit_test.go`; tests renamed `TestVerifyProdInitEval_*`):

```sh
cp $V/probes/c345_62b3e09b_fixture_prodinit_test.go internal/transport/verify_c345_fixture_prodinit_test.go
go test -v -run '^TestVerifyProdInitEval_' google.golang.org/grpc/internal/transport -race -count=1
GRPC_GO_EXPERIMENTAL_ENABLE_RECEIVE_BUFFER_COMPACTION=false go test -v -run '^TestVerifyProdInitEval_RecvBufferCompactionDisabled$' google.golang.org/grpc/internal/transport -race -count=1
rm internal/transport/verify_c345_fixture_prodinit_test.go
```

```console
$ go test -v -run '^TestVerifyProdInitEval_' google.golang.org/grpc/internal/transport -race -count=1
--- PASS: TestVerifyProdInitEval_RecvBufferCompaction (0.00s)
--- PASS: TestVerifyProdInitEval_RecvBufferCompactionDisabled (0.00s)
--- PASS: TestVerifyProdInitEval_RecvBufferCompactionSkippedLargeBuffer (0.02s)
--- PASS: TestVerifyProdInitEval_RecvBufferErrorResetSafety (0.00s)
--- PASS: TestVerifyProdInitEval_RecvBufferCompaction_MixedFrames (0.00s)
--- PASS: TestVerifyProdInitEval_RecvBufferCompaction_MultiCycleMemoryBound (0.00s)
ok  	google.golang.org/grpc/internal/transport	1.045s
$ GRPC_GO_EXPERIMENTAL_ENABLE_RECEIVE_BUFFER_COMPACTION=false go test -v -run '^TestVerifyProdInitEval_RecvBufferCompactionDisabled$' google.golang.org/grpc/internal/transport -race -count=1
--- PASS: TestVerifyProdInitEval_RecvBufferCompactionDisabled (0.00s)
ok  	google.golang.org/grpc/internal/transport	1.020s
```

Behaviour of the live transports (`verify/probes/c345_62b3e09b_live_probe_test.go`): a real `http2Client` stream created by `NewHTTP2Client`/`NewStream` against a raw HTTP/2 peer that writes the DATA frames, and a real `http2Server` stream fed by a raw HTTP/2 client; a PING/ack round trip guarantees all frames were processed before the stream's `recvBuffer` is inspected under its mutex. Each test also runs the same workload on a bare `init()` buffer for contrast.

```sh
cp $V/probes/c345_62b3e09b_live_probe_test.go internal/transport/verify_c345_live_probe_test.go
go test -v -run '^TestVerifyC[345]_' google.golang.org/grpc/internal/transport -race -count=1
rm internal/transport/verify_c345_live_probe_test.go
```

```console
$ go test -v -run '^TestVerifyC[345]_' google.golang.org/grpc/internal/transport -race -count=1
=== RUN   TestVerifyC4_MixedSizes
    verify_c345_live_probe_test.go:271: C4 bare init() only (fixture path), 1074 mixed 1..7-byte puts: chan=1 backlogEntries=1073 tailBytes=0 unreadPayloadBytes=4290 distinctBackingArrays=1074 largestEntryLen=7
    verify_c345_live_probe_test.go:277: C4 live http2Client stream, 1074 mixed 1..7-byte DATA frames (4290 bytes): chan=1 backlogEntries=0 tailBytes=4289 unreadPayloadBytes=4290 distinctBackingArrays=2 largestEntryLen=4289
    verify_c345_live_probe_test.go:301: C4 live stream, 3 x (200 one-byte frames + one 8 KiB frame) = 603 frames, 25176 bytes: chan=1 backlogEntries=6 tailBytes=0 unreadPayloadBytes=25176 distinctBackingArrays=7 largestEntryLen=8192
    verify_c345_live_probe_test.go:311: C4 live streams delivered all bytes in order
--- PASS: TestVerifyC4_MixedSizes (0.01s)
ok  	google.golang.org/grpc/internal/transport	1.070s
```

What the output shows: with bare `init()` (the fixture path) 1074 mixed 1..7-byte payloads stay in 1073 separate entries. On the live client stream the same 1074 frames (4290 bytes) are consolidated into one 4289-byte pending chunk (`backlogEntries=0`, 2 distinct backing arrays). With 8 KiB frames interleaved (3 x [200 one-byte frames + one 8192-byte frame], 603 frames), the live stream holds 6 entries and 7 backing arrays instead of 602: the eligible tiny payloads between large frames are consolidated and the large frames are queued as is. All bytes are delivered in order.

## C5

Target: [evalon/grpc-go-tr-62b3e09b](https://github.com/kaitranntt-evals/grpc-go-transport-restrict-memory-overhead/tree/evalon/grpc-go-tr-62b3e09b) at `3ab604e5f663850346ee5b9cb6c5423c16f50e7e`.

```sh
cd ~/repos/grpc-go                       # checkout of the verify branch; V points at its verify/ directory
V=~/repos/grpc-go/verify
git remote add claims https://github.com/kaitranntt-evals/grpc-go-transport-restrict-memory-overhead
git fetch claims evalon/grpc-go-tr-62b3e09b
git worktree add --detach ~/wt/62b3e09b FETCH_HEAD    # 3ab604e5
cd ~/wt/62b3e09b
```

Naming: `appendToTail()` exists; "recvBuffer initialization" on this branch is two calls, `init()` followed by `enableCompaction(pool)`.

How the solution delivers receive buffering (non-test callers of `init`/`enableCompaction`):

```sh
grep -rn "enableCompaction\|buf.init()" internal/transport/*.go | grep -v _test
```

```console
internal/transport/handler_server.go:427:	s.Stream.buf.init()
internal/transport/handler_server.go:428:	s.Stream.buf.enableCompaction(ht.bufferPool)
internal/transport/http2_client.go:503:	s.Stream.buf.init()
internal/transport/http2_client.go:504:	s.Stream.buf.enableCompaction(t.bufferPool)
internal/transport/http2_server.go:410:	s.Stream.buf.init()
internal/transport/http2_server.go:411:	s.Stream.buf.enableCompaction(t.bufferPool)
internal/transport/transport.go:138:// enableCompaction enables coalescing of small payloads queued behind a slow
internal/transport/transport.go:141:func (b *recvBuffer) enableCompaction(pool mem.BufferPool) {
```

`init()` alone only creates the channel; `put` compacts only when `b.pool` was set by `enableCompaction` (which defaults a nil pool to `mem.DefaultBufferPool()` and is a no-op when the env var disables the feature).

Byte-exact fixture on this branch (the fixture's `initRecvBufferForTest` adapter tries `initWithPool(...)` and `init(...)` signatures and ends at bare `init()`, so it never calls `enableCompaction`):

```sh
cp ~/eval/tests/eval_recv_buffer_compaction_test.go internal/transport/eval_recv_buffer_compaction_test.go
go test -v -run '^TestEval_' google.golang.org/grpc/internal/transport -race -count=1
rm internal/transport/eval_recv_buffer_compaction_test.go
```

```console
$ go test -v -run '^TestEval_' google.golang.org/grpc/internal/transport -race -count=1
    eval_recv_buffer_compaction_test.go:91: Got backlog length 1025 after compaction, want <= 64
--- FAIL: TestEval_RecvBufferCompaction (0.00s)
    eval_recv_buffer_compaction_test.go:125: Got backlog length 1025, want <= 64 (compaction enabled)
--- FAIL: TestEval_RecvBufferCompactionDisabled (0.00s)
--- PASS: TestEval_RecvBufferCompactionSkippedLargeBuffer (0.02s)
--- PASS: TestEval_RecvBufferErrorResetSafety (0.00s)
    eval_recv_buffer_compaction_test.go:305: Got backlog length 1073 after mixed frame compaction, want <= 134
--- FAIL: TestEval_RecvBufferCompaction_MixedFrames (0.00s)
    eval_recv_buffer_compaction_test.go:345: Cycle 0: backlog length 1123 exceeded bound 512
--- FAIL: TestEval_RecvBufferCompaction_MultiCycleMemoryBound (0.00s)
FAIL	google.golang.org/grpc/internal/transport	0.042s
```

The same fixture with only its adapter changed to the two calls the transports make (`verify/probes/c345_62b3e09b_fixture_prodinit_test.go`; tests renamed `TestVerifyProdInitEval_*`):

```sh
cp $V/probes/c345_62b3e09b_fixture_prodinit_test.go internal/transport/verify_c345_fixture_prodinit_test.go
go test -v -run '^TestVerifyProdInitEval_' google.golang.org/grpc/internal/transport -race -count=1
GRPC_GO_EXPERIMENTAL_ENABLE_RECEIVE_BUFFER_COMPACTION=false go test -v -run '^TestVerifyProdInitEval_RecvBufferCompactionDisabled$' google.golang.org/grpc/internal/transport -race -count=1
rm internal/transport/verify_c345_fixture_prodinit_test.go
```

```console
$ go test -v -run '^TestVerifyProdInitEval_' google.golang.org/grpc/internal/transport -race -count=1
--- PASS: TestVerifyProdInitEval_RecvBufferCompaction (0.00s)
--- PASS: TestVerifyProdInitEval_RecvBufferCompactionDisabled (0.00s)
--- PASS: TestVerifyProdInitEval_RecvBufferCompactionSkippedLargeBuffer (0.02s)
--- PASS: TestVerifyProdInitEval_RecvBufferErrorResetSafety (0.00s)
--- PASS: TestVerifyProdInitEval_RecvBufferCompaction_MixedFrames (0.00s)
--- PASS: TestVerifyProdInitEval_RecvBufferCompaction_MultiCycleMemoryBound (0.00s)
ok  	google.golang.org/grpc/internal/transport	1.045s
$ GRPC_GO_EXPERIMENTAL_ENABLE_RECEIVE_BUFFER_COMPACTION=false go test -v -run '^TestVerifyProdInitEval_RecvBufferCompactionDisabled$' google.golang.org/grpc/internal/transport -race -count=1
--- PASS: TestVerifyProdInitEval_RecvBufferCompactionDisabled (0.00s)
ok  	google.golang.org/grpc/internal/transport	1.020s
```

Behaviour of the live transports (`verify/probes/c345_62b3e09b_live_probe_test.go`): a real `http2Client` stream created by `NewHTTP2Client`/`NewStream` against a raw HTTP/2 peer that writes the DATA frames, and a real `http2Server` stream fed by a raw HTTP/2 client; a PING/ack round trip guarantees all frames were processed before the stream's `recvBuffer` is inspected under its mutex. Each test also runs the same workload on a bare `init()` buffer for contrast.

```sh
cp $V/probes/c345_62b3e09b_live_probe_test.go internal/transport/verify_c345_live_probe_test.go
go test -v -run '^TestVerifyC[345]_' google.golang.org/grpc/internal/transport -race -count=1
rm internal/transport/verify_c345_live_probe_test.go
```

```console
$ go test -v -run '^TestVerifyC[345]_' google.golang.org/grpc/internal/transport -race -count=1
=== RUN   TestVerifyC5_EnqueueReadCycles
    verify_c345_live_probe_test.go:340: C5 bare init() only (fixture path), cycle 0 after enqueue: chan=1 backlogEntries=1123 tailBytes=0 unreadPayloadBytes=3370 distinctBackingArrays=1124 largestEntryLen=5
    verify_c345_live_probe_test.go:340: C5 bare init() only (fixture path), cycle 1 after enqueue: chan=0 backlogEntries=2247 tailBytes=0 unreadPayloadBytes=6739 distinctBackingArrays=2247 largestEntryLen=5
    verify_c345_live_probe_test.go:340: C5 bare init() only (fixture path), cycle 2 after enqueue: chan=0 backlogEntries=3370 tailBytes=0 unreadPayloadBytes=10107 distinctBackingArrays=3370 largestEntryLen=5
    verify_c345_live_probe_test.go:358: C5 live stream, cycle 0 after enqueueing 1124 frames (unread so far 3370 bytes): chan=1 backlogEntries=0 tailBytes=3369 unreadPayloadBytes=3370 distinctBackingArrays=2 largestEntryLen=3369
    verify_c345_live_probe_test.go:365: C5 live stream, cycle 0 after intermediate 7-byte read (unread 3363 bytes): chan=0 backlogEntries=0 tailBytes=0 unreadPayloadBytes=0 distinctBackingArrays=0 largestEntryLen=0
    verify_c345_live_probe_test.go:358: C5 live stream, cycle 1 after enqueueing 1124 frames (unread so far 6733 bytes): chan=1 backlogEntries=0 tailBytes=3369 unreadPayloadBytes=3370 distinctBackingArrays=2 largestEntryLen=3369
    verify_c345_live_probe_test.go:365: C5 live stream, cycle 1 after intermediate 7-byte read (unread 6726 bytes): chan=1 backlogEntries=0 tailBytes=3369 unreadPayloadBytes=3370 distinctBackingArrays=2 largestEntryLen=3369
    verify_c345_live_probe_test.go:358: C5 live stream, cycle 2 after enqueueing 1124 frames (unread so far 10096 bytes): chan=1 backlogEntries=0 tailBytes=6739 unreadPayloadBytes=6740 distinctBackingArrays=2 largestEntryLen=6739
    verify_c345_live_probe_test.go:365: C5 live stream, cycle 2 after intermediate 7-byte read (unread 10089 bytes): chan=1 backlogEntries=0 tailBytes=6739 unreadPayloadBytes=6740 distinctBackingArrays=2 largestEntryLen=6739
    verify_c345_live_probe_test.go:374: C5 live stream delivered all 10110 bytes in order across 3 cycles
--- PASS: TestVerifyC5_EnqueueReadCycles (0.02s)
ok  	google.golang.org/grpc/internal/transport	1.070s
```

What the output shows: with bare `init()` (the fixture path) every unread tiny payload keeps its own source buffer across cycles (1123, 2247, 3370 entries). On the live client stream, at each intermediate read boundary the unread tail is held in consolidated chunks: after cycle 0's enqueue one 3369-byte chunk; after the 7-byte intermediate read the chunk has been handed to the reader (nothing left queued in the buffer, the remainder sits in the reader's partially consumed buffer); in cycles 1 and 2 the newly arrived frames are again consolidated (`backlogEntries=0`, `distinctBackingArrays=2`) and stay consolidated after the intermediate read. All 10110 bytes are delivered in order across the 3 cycles.

## C6

Three target branches, adjudicated separately below. Shared fact: every branch's added tests read through the pre-existing test helper `Stream.readTo` in `internal/transport/transport_test.go`, which releases what it was delivered:

```go
func (s *Stream) readTo(p []byte) (int, error) {
	data, err := s.read(len(p))
	defer data.Free()
```

Instrumentation (`verify/probes/c6/`, test files only): a log line emitted right after that `data.Free()` describing the delivered buffers it released, and a counting pool (`verifyC6Pool`, wrapping `mem.DefaultBufferPool()`) substituted where the branch's tests pass `mem.DefaultBufferPool()`, so pool `Get`/`Put` calls made on behalf of those tests are counted. "puts before Free" / "puts after Free" are the pool's `Put` count immediately before and after `readTo`'s `data.Free()`.

### C6 on evalon/grpc-go-tr-6f20f646

Target: [evalon/grpc-go-tr-6f20f646](https://github.com/kaitranntt-evals/grpc-go-transport-restrict-memory-overhead/tree/evalon/grpc-go-tr-6f20f646) at `1f5290d9edf645a43d3168757a8c6b6e73e72933`.

```sh
cd ~/repos/grpc-go                       # checkout of the verify branch; V points at its verify/ directory
V=~/repos/grpc-go/verify
git remote add claims https://github.com/kaitranntt-evals/grpc-go-transport-restrict-memory-overhead
git fetch claims evalon/grpc-go-tr-6f20f646
git worktree add --detach ~/wt/6f20f646 FETCH_HEAD    # 1f5290d9
cd ~/wt/6f20f646
```

Added tests (`internal/transport/recv_buffer_compaction_test.go`): `TestRecvBuffer_TinyFramesMemory`, `TestRecvBuffer_CompactionPreservesOrderAndZeroCopy`, `TestRecvBuffer_CompactionNoCopyWhenReaderKeepsUp`, `TestServerTransport_TinyDataFramesMemory`. The last one is the pooled-transport test: `retainedServerMemoryForTinyFrames` builds the server transport with `ServerConfig{BufferPool: mem.DefaultBufferPool(), …}` and reads the queued payload with `stream.readTo(got)`.

```sh
python3 $V/probes/c6/c6_apply.py ~/wt/6f20f646 6f20f646      # test files only; resulting diff: verify/probes/c6/c6_6f20f646.patch
go test -v -run 'Test/ServerTransport_TinyDataFramesMemory$' google.golang.org/grpc/internal/transport -count=1
git checkout -- . && rm -f internal/transport/verify_c6_helpers_test.go
```

```console
$ go test -v -run 'Test/ServerTransport_TinyDataFramesMemory$' google.golang.org/grpc/internal/transport -count=1
VERIFY C6: Stream.readTo freed 9 delivered buffer(s) (0 ref-counted/pool-backed, 131072 bytes); counting pool: gets=0, puts before Free=0, puts after Free=0
VERIFY C6: Stream.readTo freed 131072 delivered buffer(s) (0 ref-counted/pool-backed, 131072 bytes); counting pool: gets=0, puts before Free=0, puts after Free=0
--- PASS: Test (0.42s)
    --- PASS: Test/ServerTransport_TinyDataFramesMemory (0.42s)
ok  	google.golang.org/grpc/internal/transport	0.423s
```

What the output shows: in the pooled-transport test the delivered buffers are released through `Stream.readTo` (9 buffers with compaction on, 131072 with it off), and the test passes. On this branch one-byte payloads and compaction chunks are plain heap `mem.SliceBuffer`s (compaction appends into `make([]byte, …)` and wraps it with `mem.SliceBuffer`), so the configured pool sees no `Get`/`Put` for this workload (`gets=0`); there is no assertion on pool recycling. Cleanup of delivered buffers through the reader helper is present.

### C6 on evalon/grpc-go-tr-3378298f

Target: [evalon/grpc-go-tr-3378298f](https://github.com/kaitranntt-evals/grpc-go-transport-restrict-memory-overhead/tree/evalon/grpc-go-tr-3378298f) at `acbb311031221ba638f780feb7fde2acbb64101a`.

```sh
cd ~/repos/grpc-go                       # checkout of the verify branch; V points at its verify/ directory
V=~/repos/grpc-go/verify
git remote add claims https://github.com/kaitranntt-evals/grpc-go-transport-restrict-memory-overhead
git fetch claims evalon/grpc-go-tr-3378298f
git worktree add --detach ~/wt/3378298f FETCH_HEAD    # acbb3110
cd ~/wt/3378298f
```

Added tests (`internal/transport/recv_buffer_test.go`): `TestRecvBuffer_TinyFramesCompaction`, `TestRecvBuffer_CompactionPreservesLargeFrames`, `TestRecvBuffer_CompactionWithConcurrentReader`, `TestServerWithTinyDataFrames` (server built with `&ServerConfig{BufferPool: mem.DefaultBufferPool()}`), plus a benchmark that calls `rb.init(mem.DefaultBufferPool())` and `m.buffer.Free()` directly. The unit tests use a stream whose buffer is initialized with `s.buf.init(nil)`, which selects the default pool. All of them read via `s.readTo(...)`.

```sh
python3 $V/probes/c6/c6_apply.py ~/wt/3378298f 3378298f      # test files only; resulting diff: verify/probes/c6/c6_3378298f.patch
go test -v -run 'Test/RecvBuffer_TinyFramesCompaction$' google.golang.org/grpc/internal/transport -count=1
go test -v -run 'Test/ServerWithTinyDataFrames$' google.golang.org/grpc/internal/transport -count=1
git checkout -- . && rm -f internal/transport/verify_c6_helpers_test.go
```

```console
$ go test -v -run 'Test/RecvBuffer_TinyFramesCompaction$' google.golang.org/grpc/internal/transport -count=1
VERIFY C6: Stream.readTo freed 3 delivered buffer(s) (2 ref-counted/pool-backed, 20000 bytes); counting pool: gets=3, puts before Free=1, puts after Free=3
VERIFY C6: Stream.readTo freed 20000 delivered buffer(s) (0 ref-counted/pool-backed, 20000 bytes); counting pool: gets=3, puts before Free=3, puts after Free=3
--- PASS: Test (0.01s)
    --- PASS: Test/RecvBuffer_TinyFramesCompaction (0.01s)
        --- PASS: Test/RecvBuffer_TinyFramesCompaction/enabled (0.00s)
        --- PASS: Test/RecvBuffer_TinyFramesCompaction/disabled (0.01s)
ok  	google.golang.org/grpc/internal/transport	0.013s
$ go test -v -run 'Test/ServerWithTinyDataFrames$' google.golang.org/grpc/internal/transport -count=1
VERIFY C6: Stream.readTo freed 3 delivered buffer(s) (2 ref-counted/pool-backed, 32767 bytes); counting pool: gets=3, puts before Free=1, puts after Free=3
--- PASS: Test (0.08s)
    --- PASS: Test/ServerWithTinyDataFrames (0.08s)
ok  	google.golang.org/grpc/internal/transport	0.086s
```

What the output shows: with compaction enabled, both the unit test and the pooled server-transport test were delivered 3 buffers of which 2 are ref-counted pool-backed compaction chunks; `readTo`'s `data.Free()` returned them to the pool (`puts` goes from 1 to 3 across the Free, matching `gets=3`). Pooled receive buffering is combined with release of the delivered buffers through the reader helper, and the buffers were observed going back to the pool.

### C6 on evalon/grpc-go-tr-3acb1605

Target: [evalon/grpc-go-tr-3acb1605](https://github.com/kaitranntt-evals/grpc-go-transport-restrict-memory-overhead/tree/evalon/grpc-go-tr-3acb1605) at `2f47bfc165f2700b63962a498924a1ef9f00d80d`.

```sh
cd ~/repos/grpc-go                       # checkout of the verify branch; V points at its verify/ directory
V=~/repos/grpc-go/verify
git remote add claims https://github.com/kaitranntt-evals/grpc-go-transport-restrict-memory-overhead
git fetch claims evalon/grpc-go-tr-3acb1605
git worktree add --detach ~/wt/3acb1605 FETCH_HEAD    # 2f47bfc1
cd ~/wt/3acb1605
```

Added tests (all in `internal/transport/transport_test.go`): `TestRecvBufferCompaction_TinyFrames`, `TestRecvBufferCompaction_LargeFramesUntouched` (`pool := mem.DefaultBufferPool()`, payloads from `pool.Get(http2MaxFrameLen)` wrapped with `mem.NewBuffer(buf, pool)`), `TestRecvBufferCompaction_PartialReads`, `TestRecvBufferCompaction_RetainedMemory`, `TestServerReceiveBufferCompaction_TinyDataFrames` (server built with `&ServerConfig{BufferPool: mem.DefaultBufferPool()}`). They read via `st.readTo(...)` / `sstream.readTo(got)`.

```sh
python3 $V/probes/c6/c6_apply.py ~/wt/3acb1605 3acb1605      # test files only; resulting diff: verify/probes/c6/c6_3acb1605.patch
go test -v -run 'Test/RecvBufferCompaction_LargeFramesUntouched$' google.golang.org/grpc/internal/transport -count=1
go test -v -run 'Test/ServerReceiveBufferCompaction_TinyDataFrames$' google.golang.org/grpc/internal/transport -count=1
git checkout -- . && rm -f internal/transport/verify_c6_helpers_test.go
```

```console
$ go test -v -run 'Test/RecvBufferCompaction_LargeFramesUntouched$' google.golang.org/grpc/internal/transport -count=1
VERIFY C6: Stream.readTo freed 5 delivered buffer(s) (2 ref-counted/pool-backed, 32772 bytes); counting pool: gets=2, puts before Free=0, puts after Free=2
    --- PASS: Test/RecvBufferCompaction_LargeFramesUntouched (0.00s)
ok  	google.golang.org/grpc/internal/transport	0.006s
$ go test -v -run 'Test/ServerReceiveBufferCompaction_TinyDataFrames$' google.golang.org/grpc/internal/transport -count=1
VERIFY C6: Stream.readTo freed 3 delivered buffer(s) (0 ref-counted/pool-backed, 30000 bytes); counting pool: gets=0, puts before Free=0, puts after Free=0
VERIFY C6: Stream.readTo freed 30000 delivered buffer(s) (0 ref-counted/pool-backed, 30000 bytes); counting pool: gets=0, puts before Free=0, puts after Free=0
--- PASS: Test (0.11s)
    --- PASS: Test/ServerReceiveBufferCompaction_TinyDataFrames (0.11s)
        --- PASS: Test/ServerReceiveBufferCompaction_TinyDataFrames/enabled (0.05s)
        --- PASS: Test/ServerReceiveBufferCompaction_TinyDataFrames/disabled (0.06s)
ok  	google.golang.org/grpc/internal/transport	0.118s
```

What the output shows: `TestRecvBufferCompaction_LargeFramesUntouched` queues pool-backed buffers on the receive buffer and reads them with `readTo`; of the 5 delivered buffers 2 are ref-counted pool-backed, and the Free in `readTo` returned both to the pool (`puts` 0 -> 2, `gets=2`). The pooled server-transport test releases its delivered buffers through `readTo` as well (3 buffers with compaction on, 30000 with it off; plain heap buffers on this branch, so the pool is not involved there). Pooled receive buffering is combined with release of delivered buffers through the existing reader helper.

## C7

Two target branches, adjudicated separately below.

### C7 on evalon/grpc-go-tr-8fc53528

Target: [evalon/grpc-go-tr-8fc53528](https://github.com/kaitranntt-evals/grpc-go-transport-restrict-memory-overhead/tree/evalon/grpc-go-tr-8fc53528) at `c7ca537ba98851c630325e6eb5578ee14059b87c`.

```sh
cd ~/repos/grpc-go                       # checkout of the verify branch; V points at its verify/ directory
V=~/repos/grpc-go/verify
git remote add claims https://github.com/kaitranntt-evals/grpc-go-transport-restrict-memory-overhead
git fetch claims evalon/grpc-go-tr-8fc53528
git worktree add --detach ~/wt/8fc53528 FETCH_HEAD    # c7ca537b
cd ~/wt/8fc53528
```

Test text (`internal/transport/recv_buffer_test.go`, `TestRecvBufferCompactionPartialChunk`): one `recvBuffer` (`queue`) and one reader are created before the loop; each of the 10 iterations puts six one-byte payloads on that same `queue`, reads them back with three `reader.Read(2)` calls, and asserts the bytes:

```go
	var queue recvBuffer
	queue.init()
	reader := recvBufferReader{ctx: ctx, ctxDone: ctx.Done(), recv: &queue}
	for i := 0; i < 10; i++ {
		want := []byte{byte(i), 1, 2, 3, 4, 5}
		for _, b := range want {
			queue.put(recvMsg{buffer: mem.SliceBuffer{b}})
		}
		var got []byte
		for len(got) < len(want) {
			buf, err := reader.Read(2)
			…
		}
		if !bytes.Equal(got, want) {
			t.Fatalf("Read() returned %v, want %v", got, want)
		}
	}
```

So from iteration 1 on, data is refilled on the same live buffer after earlier reads, and the equality assertion covers bytes that arrived after those reads. Demonstration run with trace logging only (`verify/probes/c7_8fc53528_partialchunk_trace.patch`, test file only):

```sh
git apply $V/probes/c7_8fc53528_partialchunk_trace.patch
go test -v -run 'Test/RecvBufferCompactionPartialChunk$' google.golang.org/grpc/internal/transport -count=1
```

```console
$ go test -v -run 'Test/RecvBufferCompactionPartialChunk$' google.golang.org/grpc/internal/transport -count=1   # branch test + trace logging only
    recv_buffer_test.go:286: VERIFY C7 iteration 0: before refill: queue=0xc00029efc0 chan=0 backlog=0 pendingLen=-1 (-1 = none)
    recv_buffer_test.go:290: VERIFY C7 iteration 0: after putting [0 1 2 3 4 5] on the same queue: queue=0xc00029efc0 chan=1 backlog=0 pendingLen=5 (-1 = none)
    recv_buffer_test.go:300: VERIFY C7 iteration 0: reads returned [0 1 2 3 4 5]; asserting == [0 1 2 3 4 5]; queue=0xc00029efc0 chan=0 backlog=0 pendingLen=-1 (-1 = none)
    recv_buffer_test.go:286: VERIFY C7 iteration 1: before refill: queue=0xc00029efc0 chan=0 backlog=0 pendingLen=-1 (-1 = none)
    recv_buffer_test.go:290: VERIFY C7 iteration 1: after putting [1 1 2 3 4 5] on the same queue: queue=0xc00029efc0 chan=1 backlog=0 pendingLen=5 (-1 = none)
    recv_buffer_test.go:300: VERIFY C7 iteration 1: reads returned [1 1 2 3 4 5]; asserting == [1 1 2 3 4 5]; queue=0xc00029efc0 chan=0 backlog=0 pendingLen=-1 (-1 = none)
    recv_buffer_test.go:286: VERIFY C7 iteration 2: before refill: queue=0xc00029efc0 chan=0 backlog=0 pendingLen=-1 (-1 = none)
    recv_buffer_test.go:290: VERIFY C7 iteration 2: after putting [2 1 2 3 4 5] on the same queue: queue=0xc00029efc0 chan=1 backlog=0 pendingLen=5 (-1 = none)
    recv_buffer_test.go:300: VERIFY C7 iteration 2: reads returned [2 1 2 3 4 5]; asserting == [2 1 2 3 4 5]; queue=0xc00029efc0 chan=0 backlog=0 pendingLen=-1 (-1 = none)
```

The same `queue` address is refilled after the reads of the previous iteration (`before refill: … chan=0 backlog=0`, then `after putting … chan=1 … pendingLen=5`), and the assertion is evaluated on the refilled data.

Does the assertion really span the read boundary? Mutant M_A (`verify/probes/c7_8fc53528_mutant_mA_stale_pending.patch`) removes `b.pending = nil` from `flushPending`, so compaction state survives the hand-off to the reader and leaks into data arriving after the read:

```sh
git apply $V/probes/c7_8fc53528_mutant_mA_stale_pending.patch      # on top of the trace patch
for t in RecvBufferCompactionMemory RecvBufferCompactionReads RecvBufferCompactionPartialChunk RecvBufferCompactionZeroCopy; do
  for i in 1 2 3 4 5; do go test -v -run "Test/$t\$" google.golang.org/grpc/internal/transport -count=1; done
done
git checkout -- .
```

Tally of the runs (the `mutant …: pass=/fail=` lines summarize the exit status of each run; indented lines are quoted from the first failing run):

```console
mutant M_A, Test/RecvBufferCompactionMemory x5: pass=0 fail=5
    recv_buffer_test.go:108: retained heap for 65535 unread payload bytes: 4000712 bytes
mutant M_A, Test/RecvBufferCompactionReads x5: pass=0 fail=5
mutant M_A, Test/RecvBufferCompactionPartialChunk x5: pass=0 fail=5
    recv_buffer_test.go:286: VERIFY C7 iteration 0: before refill: queue=0xc00021efc0 chan=0 backlog=0 pendingLen=-1 (-1 = none)
    recv_buffer_test.go:290: VERIFY C7 iteration 0: after putting [0 1 2 3 4 5] on the same queue: queue=0xc00021efc0 chan=1 backlog=0 pendingLen=5 (-1 = none)
    recv_buffer_test.go:300: VERIFY C7 iteration 0: reads returned [0 1 2 3 4 5]; asserting == [0 1 2 3 4 5]; queue=0xc00021efc0 chan=1 backlog=0 pendingLen=5 (-1 = none)
    recv_buffer_test.go:286: VERIFY C7 iteration 1: before refill: queue=0xc00021efc0 chan=1 backlog=0 pendingLen=5 (-1 = none)
    recv_buffer_test.go:290: VERIFY C7 iteration 1: after putting [1 1 2 3 4 5] on the same queue: queue=0xc00021efc0 chan=1 backlog=0 pendingLen=11 (-1 = none)
    recv_buffer_test.go:300: VERIFY C7 iteration 1: reads returned [1 2 3 4 5 1 2]; asserting == [1 1 2 3 4 5]; queue=0xc00021efc0 chan=1 backlog=0 pendingLen=11 (-1 = none)
    recv_buffer_test.go:302: Read() returned [1 2 3 4 5 1 2], want [1 1 2 3 4 5]
    --- FAIL: Test/RecvBufferCompactionPartialChunk (0.00s)
mutant M_A, Test/RecvBufferCompactionZeroCopy x5: pass=5 fail=0
```

`TestRecvBufferCompactionPartialChunk` passes iteration 0 and fails in iteration 1, the first iteration whose data arrives after earlier reads on the same buffer (`Read() returned [1 2 3 4 5 1 2], want [1 1 2 3 4 5]`): the test detects a fault that only manifests across the read/refill boundary.

### C7 on evalon/grpc-go-tr-3f8bbe5e

Target: [evalon/grpc-go-tr-3f8bbe5e](https://github.com/kaitranntt-evals/grpc-go-transport-restrict-memory-overhead/tree/evalon/grpc-go-tr-3f8bbe5e) at `6bd56c4392edc7e77591401a14a257645be78c23`.

```sh
cd ~/repos/grpc-go                       # checkout of the verify branch; V points at its verify/ directory
V=~/repos/grpc-go/verify
git remote add claims https://github.com/kaitranntt-evals/grpc-go-transport-restrict-memory-overhead
git fetch claims evalon/grpc-go-tr-3f8bbe5e
git worktree add --detach ~/wt/3f8bbe5e FETCH_HEAD    # 6bd56c43
cd ~/wt/3f8bbe5e
```

Naming: there is no `TestRecvBufferCompactionPartialChunk` on this branch; the added tests (`internal/transport/recv_buffer_test.go`) are `TestReceiveBufferCompactionMemory`, `TestReceiveBufferCompactionZeroCopy`, `TestReceiveBufferCompactionMixedReads` and `TestReceiveBufferCompactionConcurrent`.

Test text, `TestReceiveBufferCompactionZeroCopy` (sequential ordering): four put/Read pairs on one `recv`, then further puts on the same `recv`, then reads with an assertion on each delivered buffer:

```go
			for _, size := range []int{1, 1025, 4096, 16384} {
				…
				recv.put(recvMsg{buffer: data})
				got, err := reader.Read(size)
				…
				got.Free()
			}
			…
			for _, size := range sizes {
				data := mem.Copy(bytes.Repeat([]byte{42}, size), mem.DefaultBufferPool())
				originals = append(originals, &data.ReadOnlyData()[0])
				recv.put(recvMsg{buffer: data})
			}
			for i, size := range sizes {
				got, err := reader.Read(size)
				…
				if got.Len() != size || &got.ReadOnlyData()[0] != originals[i] {
					t.Errorf("queued delivery changed %d-byte buffer", size)
				}
```

Test text, `TestReceiveBufferCompactionConcurrent` (reads interleaved with a writer goroutine on one `recv`, 32768 one-byte payloads, then a terminal `io.EOF`): every byte read is checked (`t.Fatalf("byte %d = %d, want %d", i, v, byte(i))`), and after draining the test asserts `reader.Read(1)` returns `io.EOF`.

Demonstration run with trace logging / counters only (`verify/probes/c7_3f8bbe5e_trace.patch`, test file only):

```sh
git apply $V/probes/c7_3f8bbe5e_trace.patch
go test -v -run 'Test/ReceiveBufferCompactionZeroCopy$' google.golang.org/grpc/internal/transport -count=1
go test -v -run 'Test/ReceiveBufferCompactionConcurrent$' google.golang.org/grpc/internal/transport -count=20
```

```console
$ go test -v -run 'Test/ReceiveBufferCompactionZeroCopy$' google.golang.org/grpc/internal/transport -count=1   # branch test + trace logging only
    recv_buffer_test.go:148: VERIFY C7: put(1 bytes) on recv=0xc000261ae0
    recv_buffer_test.go:153: VERIFY C7: Read(1) on recv=0xc000261ae0 returned 1 bytes; asserting same backing storage as the put: true
    recv_buffer_test.go:148: VERIFY C7: put(1025 bytes) on recv=0xc000261ae0
    recv_buffer_test.go:153: VERIFY C7: Read(1025) on recv=0xc000261ae0 returned 1025 bytes; asserting same backing storage as the put: true
    recv_buffer_test.go:148: VERIFY C7: put(4096 bytes) on recv=0xc000261ae0
    recv_buffer_test.go:153: VERIFY C7: Read(4096) on recv=0xc000261ae0 returned 4096 bytes; asserting same backing storage as the put: true
    recv_buffer_test.go:148: VERIFY C7: put(16384 bytes) on recv=0xc000261ae0
    recv_buffer_test.go:153: VERIFY C7: Read(16384) on recv=0xc000261ae0 returned 16384 bytes; asserting same backing storage as the put: true
    recv_buffer_test.go:159: VERIFY C7: -- 4 reads done; more data now arrives on the same recv=0xc000261ae0 --
    recv_buffer_test.go:170: VERIFY C7: put(1 bytes) on recv=0xc000261ae0 -> chan=1 backlog=0
    recv_buffer_test.go:170: VERIFY C7: put(1025 bytes) on recv=0xc000261ae0 -> chan=1 backlog=1
    recv_buffer_test.go:170: VERIFY C7: put(4096 bytes) on recv=0xc000261ae0 -> chan=1 backlog=2
    recv_buffer_test.go:170: VERIFY C7: put(16384 bytes) on recv=0xc000261ae0 -> chan=1 backlog=3
    recv_buffer_test.go:170: VERIFY C7: put(1 bytes) on recv=0xc000261ae0 -> chan=1 backlog=4
    recv_buffer_test.go:170: VERIFY C7: put(10 bytes) on recv=0xc000261ae0 -> chan=1 backlog=5
    recv_buffer_test.go:170: VERIFY C7: put(100 bytes) on recv=0xc000261ae0 -> chan=1 backlog=6
    recv_buffer_test.go:178: VERIFY C7: Read(1) on recv=0xc000261ae0 returned 1 bytes; asserting len and same backing storage: true
    recv_buffer_test.go:178: VERIFY C7: Read(1025) on recv=0xc000261ae0 returned 1025 bytes; asserting len and same backing storage: true
    recv_buffer_test.go:178: VERIFY C7: Read(4096) on recv=0xc000261ae0 returned 4096 bytes; asserting len and same backing storage: true
    recv_buffer_test.go:178: VERIFY C7: Read(16384) on recv=0xc000261ae0 returned 16384 bytes; asserting len and same backing storage: true
    recv_buffer_test.go:178: VERIFY C7: Read(1) on recv=0xc000261ae0 returned 1 bytes; asserting len and same backing storage: true
    recv_buffer_test.go:178: VERIFY C7: Read(10) on recv=0xc000261ae0 returned 10 bytes; asserting len and same backing storage: true
    recv_buffer_test.go:178: VERIFY C7: Read(100) on recv=0xc000261ae0 returned 100 bytes; asserting len and same backing storage: true
… (second subtest, compaction=true, logs the same sequence on its own recvBuffer)
    --- PASS: Test/ReceiveBufferCompactionZeroCopy (0.00s)
        --- PASS: Test/ReceiveBufferCompactionZeroCopy/compaction=false (0.00s)
        --- PASS: Test/ReceiveBufferCompactionZeroCopy/compaction=true (0.00s)
ok  	google.golang.org/grpc/internal/transport	0.006s
$ go test -v -run 'Test/ReceiveBufferCompactionConcurrent$' google.golang.org/grpc/internal/transport -count=20   # branch test + counters only
    recv_buffer_test.go:289: VERIFY C7: 1168 reads; 1163 of them completed before later data arrived on the same recvBuffer; 50 of those had fully drained it first
    recv_buffer_test.go:289: VERIFY C7: 1491 reads; 1482 of them completed before later data arrived on the same recvBuffer; 159 of those had fully drained it first
    recv_buffer_test.go:289: VERIFY C7: 1579 reads; 1575 of them completed before later data arrived on the same recvBuffer; 194 of those had fully drained it first
    recv_buffer_test.go:289: VERIFY C7: 1480 reads; 1463 of them completed before later data arrived on the same recvBuffer; 149 of those had fully drained it first
    recv_buffer_test.go:289: VERIFY C7: 1656 reads; 1640 of them completed before later data arrived on the same recvBuffer; 224 of those had fully drained it first
    recv_buffer_test.go:289: VERIFY C7: 1394 reads; 1370 of them completed before later data arrived on the same recvBuffer; 147 of those had fully drained it first
    recv_buffer_test.go:289: VERIFY C7: 1516 reads; 1488 of them completed before later data arrived on the same recvBuffer; 184 of those had fully drained it first
… (14 more lines; full log under verify/logs/)
```

In `ZeroCopy`, four reads complete on `recv`, then seven more payloads arrive on the same `recv` and are asserted on. In `Concurrent`, across 20 runs every run had more than 1100 reads that completed before later data arrived on the same buffer, at least 50 of which had fully drained it first; the byte-order assertion is evaluated on the data that arrived afterwards.

Mutant M5 (`verify/probes/c7_3f8bbe5e_mutant_m5_no_detach_on_drain.patch`) removes the `b.compacted = nil` that `load()` performs when it hands the last chunk to the reader, a fault that only matters when more data arrives after that read:

```sh
git apply $V/probes/c7_3f8bbe5e_mutant_m5_no_detach_on_drain.patch      # on top of the trace patch
for t in ReceiveBufferCompactionMemory ReceiveBufferCompactionZeroCopy ReceiveBufferCompactionMixedReads ReceiveBufferCompactionConcurrent; do
  for i in $(seq 10); do go test -v -run "Test/$t\$" google.golang.org/grpc/internal/transport -count=1; done
done
git checkout -- .
```

Tally of the runs (the `mutant …: pass=/fail=` lines summarize the exit status of each run; indented lines are quoted from the first failing run):

```console
mutant M5, Test/ReceiveBufferCompactionMemory x10: pass=10 fail=0
mutant M5, Test/ReceiveBufferCompactionZeroCopy x10: pass=10 fail=0
mutant M5, Test/ReceiveBufferCompactionMixedReads x10: pass=10 fail=0
mutant M5, Test/ReceiveBufferCompactionConcurrent x10: pass=0 fail=10
panic: runtime error: index out of range [-1]
	/home/ubuntu/wt/3f8bbe5e/internal/transport/transport.go:119 +0x576
	/home/ubuntu/wt/3f8bbe5e/internal/transport/recv_buffer_test.go:239 +0x54
	/home/ubuntu/wt/3f8bbe5e/internal/transport/recv_buffer_test.go:248 +0x82
	/home/ubuntu/wt/3f8bbe5e/internal/transport/recv_buffer_test.go:245 +0x20e
```

`TestReceiveBufferCompactionConcurrent` fails 10 of 10 runs under M5 (panic in `put` when data arrives after the reader drained the buffer); the other three tests do not detect this particular mutant. At least one added test therefore establishes an intermediate read followed by later arrival on the same live buffer and checks behaviour across that boundary.

## C8

Target: [evalon/grpc-go-tr-b490959e](https://github.com/kaitranntt-evals/grpc-go-transport-restrict-memory-overhead/tree/evalon/grpc-go-tr-b490959e) at `6830f4764e5291d456171ff7fe2cba240b4ec1a8`.

```sh
cd ~/repos/grpc-go                       # checkout of the verify branch; V points at its verify/ directory
V=~/repos/grpc-go/verify
git remote add claims https://github.com/kaitranntt-evals/grpc-go-transport-restrict-memory-overhead
git fetch claims evalon/grpc-go-tr-b490959e
git worktree add --detach ~/wt/b490959e FETCH_HEAD    # 6830f476
cd ~/wt/b490959e
```

Naming: there is no `compactBacklogLocked` on this branch; consolidation happens in `recvBuffer.put` via `appendPending`/`flushPending`. `initWithPool`, `serverHandlerTransport.HandleStreams`, `s.Stream.buf.init()` and `ht.bufferPool` exist as named.

Source trace:

```sh
grep -n "buf.init\|initWithPool" internal/transport/*.go | grep -v _test
```

```console
internal/transport/handler_server.go:427:	s.Stream.buf.init()
internal/transport/http2_client.go:503:	s.Stream.buf.initWithPool(t.bufferPool)
internal/transport/http2_server.go:410:	s.Stream.buf.initWithPool(t.bufferPool)
internal/transport/transport.go:85:// If a pool is configured (via initWithPool), small payloads that cannot be
internal/transport/transport.go:113:// initWithPool is like init, but additionally enables coalescing of small
internal/transport/transport.go:115:func (b *recvBuffer) initWithPool(pool mem.BufferPool) {
```

`recvBuffer.put` (`internal/transport/transport.go:144`) gates consolidation on the pool: `if r.err == nil && b.pool != nil && r.buffer.Len() <= recvCompactionMaxPayload { b.appendPending(r.buffer); r.buffer.Free(); … }`.

Probe run:

```sh
cp $V/repro/c8_b490959e_handler_no_compaction_test.go internal/transport/verify_c8_handler_no_compaction_test.go
go test -v -run '^TestVerifyC8_' google.golang.org/grpc/internal/transport -race -count=1
rm internal/transport/verify_c8_handler_no_compaction_test.go
```

```console
=== RUN   TestVerifyC8_PartGate_NilPoolSkipsConsolidation
    verify_c8_handler_no_compaction_test.go:73: C8 gate: init() -> pool == nil                pool==nil:true  after 1000 one-byte puts: {chanMsgs:1 backlogEntries:999 pendingBytes:0 payload:1000 retainedCap:1000 distinct:1000}
    verify_c8_handler_no_compaction_test.go:73: C8 gate: initWithPool(DefaultBufferPool())    pool==nil:false after 1000 one-byte puts: {chanMsgs:1 backlogEntries:0 pendingBytes:999 payload:1000 retainedCap:1025 distinct:2}
--- PASS: TestVerifyC8_PartGate_NilPoolSkipsConsolidation (0.00s)
=== RUN   TestVerifyC8_PartHandlerInit_ShortBodyReadsStaySeparate
    verify_c8_handler_no_compaction_test.go:101: C8 handler init: serverHandlerTransport has a buffer pool: true; stream recvBuffer.pool == nil: true
    verify_c8_handler_no_compaction_test.go:124: C8 handler stream after 1000 one-byte request-body reads, application not reading: {chanMsgs:1 backlogEntries:999 pendingBytes:0 payload:1000 retainedCap:16384000 distinct:1000}
    verify_c8_handler_no_compaction_test.go:125: C8 handler stream: 1000 queue entries for 1000 unread bytes; 1000 distinct source buffers; 16384000 bytes of backing capacity retained (16384x the unread payload)
    verify_c8_handler_no_compaction_test.go:128: C8 OBSERVED: handler payloads are queued one entry / one source buffer per read (no compaction)
--- PASS: TestVerifyC8_PartHandlerInit_ShortBodyReadsStaySeparate (0.03s)
PASS
ok  	google.golang.org/grpc/internal/transport	1.044s
```

Part "Compaction gate" (observed): on a buffer initialized with `init()` the pool is nil and 1000 one-byte puts leave 999 backlog entries in 1000 distinct buffers; the same puts after `initWithPool(DefaultBufferPool())` leave 0 backlog entries and one 999-byte pending chunk. The part holds.

Part "Handler initialization" (observed): a real `serverHandlerTransport` (created by `NewServerHandlerTransport` with a buffer pool, driven through `HandleStreams` with a request body that yields one byte per `Read`) has `recvBuffer.pool == nil` on its stream although the transport itself holds a pool. After 1000 one-byte body reads with the application not reading, the stream holds 999 backlog entries plus one in the channel, 1000 distinct source buffers, and 16,384,000 bytes of backing capacity for 1000 unread bytes (each short read keeps its own 16 KiB read buffer). The part holds.

The byte-exact eval fixture passes on this branch (it initializes the buffer through `initWithPool`), so it does not exercise the handler transport:

```sh
cp ~/eval/tests/eval_recv_buffer_compaction_test.go internal/transport/eval_recv_buffer_compaction_test.go
go test -v -run '^TestEval_' google.golang.org/grpc/internal/transport -race -count=1
rm internal/transport/eval_recv_buffer_compaction_test.go
```

```console
$ go test -v -run '^TestEval_' google.golang.org/grpc/internal/transport -race -count=1
--- PASS: TestEval_RecvBufferCompaction (0.00s)
--- PASS: TestEval_RecvBufferCompactionDisabled (0.00s)
--- PASS: TestEval_RecvBufferCompactionSkippedLargeBuffer (0.01s)
--- PASS: TestEval_RecvBufferErrorResetSafety (0.00s)
--- PASS: TestEval_RecvBufferCompaction_MixedFrames (0.00s)
--- PASS: TestEval_RecvBufferCompaction_MultiCycleMemoryBound (0.00s)
ok  	google.golang.org/grpc/internal/transport	1.047s
```

Impact reasoning: servers that serve gRPC through `grpc.Server.ServeHTTP` (the handler transport) get none of the fix on this branch. A request body that arrives in short reads while the handler is slow to consume it is queued one entry per read, each pinning a full pooled read buffer: 1000 unread bytes held 16,384,000 bytes in the probe, the same shape of growth the task set out to remove, while the HTTP/2 client and server transports on the same branch do compact. There is no configuration that enables it for this path; the workaround is not to use the handler transport.

## C9

Target: [evalon/grpc-go-tr-b490959e](https://github.com/kaitranntt-evals/grpc-go-transport-restrict-memory-overhead/tree/evalon/grpc-go-tr-b490959e) at `6830f4764e5291d456171ff7fe2cba240b4ec1a8`.

```sh
cd ~/repos/grpc-go                       # checkout of the verify branch; V points at its verify/ directory
V=~/repos/grpc-go/verify
git remote add claims https://github.com/kaitranntt-evals/grpc-go-transport-restrict-memory-overhead
git fetch claims evalon/grpc-go-tr-b490959e
git worktree add --detach ~/wt/b490959e FETCH_HEAD    # 6830f476
cd ~/wt/b490959e
```

Naming: there is no `compactBacklogLocked()` on this branch; the copy is made by `recvBuffer.put` -> `appendPending`. `recvCompactionMaxPayload = 4 << 10` and `appendPending` exist as named.

Source trace (`internal/transport/transport.go`): `recvCompactionMaxPayload = 4 << 10`, `recvCompactionChunkSize = http2MaxFrameLen`, and in `put`: `if r.err == nil && b.pool != nil && r.buffer.Len() <= recvCompactionMaxPayload { b.appendPending(r.buffer); r.buffer.Free(); … }`. A 4096-byte payload satisfies `<=`.

Probe run (nine payloads created with `mem.Copy(data, pool)` through a counting wrapper around `mem.DefaultBufferPool()`, queued with no reads on a buffer initialized with `initWithPool`, then drained; a control repeats it with 4097-byte payloads):

```sh
cp $V/repro/c9_b490959e_4k_payloads_copied_test.go internal/transport/verify_c9_4k_payloads_copied_test.go
go test -v -run '^TestVerifyC9_' google.golang.org/grpc/internal/transport -race -count=1
rm internal/transport/verify_c9_4k_payloads_copied_test.go
```

```console
=== RUN   TestVerifyC9_Homogeneous4KiBPayloads
    verify_c9_4k_payloads_copied_test.go:98: recvCompactionMaxPayload=4096 recvCompactionChunkSize=16384
    verify_c9_4k_payloads_copied_test.go:57: C9 size=4096: 9 payloads queued, no reads. source buffer caps=[4096 4096 4096 4096 4096 4096 4096 4096 4096]
    verify_c9_4k_payloads_copied_test.go:58: C9 size=4096: pool gets by capacity=map[4096:11 16384:2], puts by capacity (buffers already released while still unread)=map[4096:10]
    verify_c9_4k_payloads_copied_test.go:65: C9 size=4096: len(b.c)=1 len(b.backlog)=1 len(pending)=16384
    verify_c9_4k_payloads_copied_test.go:83: C9 size=4096: delivered buffer #0: len=4096 cap=4096 backing=0xc0002f8000 -> original backing storage of payload: 0 (-1 = none, i.e. a copy)
    verify_c9_4k_payloads_copied_test.go:83: C9 size=4096: delivered buffer #1: len=16384 cap=16384 backing=0xc000304000 -> original backing storage of payload: -1 (-1 = none, i.e. a copy)
    verify_c9_4k_payloads_copied_test.go:83: C9 size=4096: delivered buffer #2: len=16384 cap=16384 backing=0xc000328000 -> original backing storage of payload: -1 (-1 = none, i.e. a copy)
    verify_c9_4k_payloads_copied_test.go:94: C9 size=4096: RESULT 1 of 9 payloads delivered from their original backing storage; 3 buffers delivered in total
--- PASS: TestVerifyC9_Homogeneous4KiBPayloads (0.00s)
=== RUN   TestVerifyC9_Control4097BytePayloads
    verify_c9_4k_payloads_copied_test.go:57: C9 size=4097: 9 payloads queued, no reads. source buffer caps=[16384 16384 16384 16384 16384 16384 16384 16384 16384]
    verify_c9_4k_payloads_copied_test.go:58: C9 size=4097: pool gets by capacity=map[16384:9], puts by capacity (buffers already released while still unread)=map[]
    verify_c9_4k_payloads_copied_test.go:65: C9 size=4097: len(b.c)=1 len(b.backlog)=8 len(pending)=0
    verify_c9_4k_payloads_copied_test.go:83: C9 size=4097: delivered buffer #0: len=4097 cap=16384 backing=0xc000304000 -> original backing storage of payload: 0 (-1 = none, i.e. a copy)
    verify_c9_4k_payloads_copied_test.go:83: C9 size=4097: delivered buffer #1: len=4097 cap=16384 backing=0xc000328000 -> original backing storage of payload: 1 (-1 = none, i.e. a copy)
    verify_c9_4k_payloads_copied_test.go:83: C9 size=4097: delivered buffer #2: len=4097 cap=16384 backing=0xc00036c000 -> original backing storage of payload: 2 (-1 = none, i.e. a copy)
    verify_c9_4k_payloads_copied_test.go:83: C9 size=4097: delivered buffer #3: len=4097 cap=16384 backing=0xc000370000 -> original backing storage of payload: 3 (-1 = none, i.e. a copy)
    verify_c9_4k_payloads_copied_test.go:83: C9 size=4097: delivered buffer #4: len=4097 cap=16384 backing=0xc000380000 -> original backing storage of payload: 4 (-1 = none, i.e. a copy)
    verify_c9_4k_payloads_copied_test.go:83: C9 size=4097: delivered buffer #5: len=4097 cap=16384 backing=0xc00038c000 -> original backing storage of payload: 5 (-1 = none, i.e. a copy)
    verify_c9_4k_payloads_copied_test.go:83: C9 size=4097: delivered buffer #6: len=4097 cap=16384 backing=0xc000390000 -> original backing storage of payload: 6 (-1 = none, i.e. a copy)
    verify_c9_4k_payloads_copied_test.go:83: C9 size=4097: delivered buffer #7: len=4097 cap=16384 backing=0xc0003a0000 -> original backing storage of payload: 7 (-1 = none, i.e. a copy)
    verify_c9_4k_payloads_copied_test.go:83: C9 size=4097: delivered buffer #8: len=4097 cap=16384 backing=0xc0003a4000 -> original backing storage of payload: 8 (-1 = none, i.e. a copy)
    verify_c9_4k_payloads_copied_test.go:94: C9 size=4097: RESULT 9 of 9 payloads delivered from their original backing storage; 9 buffers delivered in total
--- PASS: TestVerifyC9_Control4097BytePayloads (0.00s)
PASS
ok  	google.golang.org/grpc/internal/transport	1.016s
```

What the output shows: each 4096-byte payload sits in a 4096-capacity pooled buffer (`source buffer caps=[4096 …]`). While still unread, 10 of the 4 KiB pool buffers had already been returned to the pool and two 16 KiB compaction buffers had been taken (`gets … 16384:2`, `puts … 4096:10`). The reader was delivered 3 buffers: the first payload in its original backing array, then two 16384-byte compaction buffers that match none of the original backing arrays. 1 of 9 payloads arrived in its original storage; 8 were copied. The 4097-byte control is delivered 9 of 9 from original storage with no pool puts before delivery.

The byte-exact eval fixture passes on this branch, so it does not detect the copy at the 4 KiB tier:

```sh
cp ~/eval/tests/eval_recv_buffer_compaction_test.go internal/transport/eval_recv_buffer_compaction_test.go
go test -v -run '^TestEval_' google.golang.org/grpc/internal/transport -race -count=1
rm internal/transport/eval_recv_buffer_compaction_test.go
```

```console
$ go test -v -run '^TestEval_' google.golang.org/grpc/internal/transport -race -count=1
--- PASS: TestEval_RecvBufferCompaction (0.00s)
--- PASS: TestEval_RecvBufferCompactionDisabled (0.00s)
--- PASS: TestEval_RecvBufferCompactionSkippedLargeBuffer (0.01s)
--- PASS: TestEval_RecvBufferErrorResetSafety (0.00s)
--- PASS: TestEval_RecvBufferCompaction_MixedFrames (0.00s)
--- PASS: TestEval_RecvBufferCompaction_MultiCycleMemoryBound (0.00s)
ok  	google.golang.org/grpc/internal/transport	1.047s
```

Impact reasoning: payloads of exactly 4 KiB (and anything between the pooling threshold and 4096 bytes) already occupy a right-sized pooled buffer, so copying them saves no memory; it adds a memcpy per queued payload and replaces the original storage with compaction buffers, i.e. the zero-copy path is lost for that size class whenever the reader is behind. 4 KiB DATA frames are an ordinary size (not "tiny"), and the task statement asks that ordinary-sized traffic keep its existing zero-copy path. Data is delivered intact and in order; the cost is CPU and pool churn, not correctness.

## C10

Target: [evalon/grpc-go-tr-c15666d0](https://github.com/kaitranntt-evals/grpc-go-transport-restrict-memory-overhead/tree/evalon/grpc-go-tr-c15666d0) at `481647e20b9f144cd348274a74e42131a7c60eee`.

```sh
cd ~/repos/grpc-go                       # checkout of the verify branch; V points at its verify/ directory
V=~/repos/grpc-go/verify
git remote add claims https://github.com/kaitranntt-evals/grpc-go-transport-restrict-memory-overhead
git fetch claims evalon/grpc-go-tr-c15666d0
git worktree add --detach ~/wt/c15666d0 FETCH_HEAD    # 481647e2
cd ~/wt/c15666d0
```

Naming: `newChunk()`, `nextChunkSize` and `finalizeChunk()` exist as named; there are no `flushChunkLocked()`/`newChunkLocked()` on this branch.

Source trace (`internal/transport/transport.go`, `newChunk`): the next chunk's capacity is taken from `b.nextChunkSize`, which doubles on every new chunk and is only reset to the minimum at `transport.go:239`:

```go
	size := min(max(b.nextChunkSize, recvBufferCompactionMinChunkSize, minSize), recvBufferCompactionMaxChunkSize)
	…
	if b.pool != nil && !mem.IsBelowBufferPoolingThreshold(size) {
		chunk = b.pool.Get(size)
	…
	b.nextChunkSize = min(2*cap(*chunk), recvBufferCompactionMaxChunkSize)
```

Probe run (first test: one byte to the channel, then 16 x [1-byte frame, 2048-byte frame] with no reads, once with an exact-size recording pool and once with `mem.DefaultBufferPool()`; second test: retained bytes with compaction on vs off for two interleaved patterns):

```sh
cp $V/repro/c10_c15666d0_chunk_target_survives_interruptions_test.go internal/transport/verify_c10_chunk_target_test.go
go test -v -run '^TestVerifyC10_' google.golang.org/grpc/internal/transport -race -count=1
rm internal/transport/verify_c10_chunk_target_test.go
```

```console
=== RUN   TestVerifyC10_OneByteChunksAfter2KiBInterruptions
    verify_c10_chunk_target_test.go:91: sequence: 1 byte (to channel), then 16 x [1-byte frame, 2048-byte frame]; no reads
    verify_c10_chunk_target_test.go:95: exact-size pool: nextChunkSize after each 1-byte put: 512 1024 2048 4096 8192 16384 16384 16384 16384 16384 16384 16384 16384 16384 16384 16384
    verify_c10_chunk_target_test.go:96: exact-size pool: sizes requested from the pool for 1-byte chunks: 2048 4096 8192 16384 16384 16384 16384 16384 16384 16384 16384 16384 16384
    verify_c10_chunk_target_test.go:97: exact-size pool: payload bytes per tiny chunk: 1 1 1 1 1 1 1 1 1 1 1 1 1 1 1 1
    verify_c10_chunk_target_test.go:98: exact-size pool: backing capacity per tiny chunk: 256 512 1024 2048 4096 8192 16384 16384 16384 16384 16384 16384 16384 16384 16384 16384
    verify_c10_chunk_target_test.go:101: default pool:    nextChunkSize after each 1-byte put: 512 1024 2048 8192 16384 16384 16384 16384 16384 16384 16384 16384 16384 16384 16384 16384
    verify_c10_chunk_target_test.go:102: default pool:    payload bytes per tiny chunk: 1 1 1 1 1 1 1 1 1 1 1 1 1 1 1 1
    verify_c10_chunk_target_test.go:103: default pool:    backing capacity per tiny chunk: 256 512 1024 4096 16384 16384 16384 16384 16384 16384 16384 16384 16384 16384 16384 16384
    verify_c10_chunk_target_test.go:110: default pool:    12 of 16 one-byte chunks retain a 16384-byte backing buffer; tiny payload 16 bytes held in 202496 bytes (12656x)
    verify_c10_chunk_target_test.go:113: C10 OBSERVED: the enlarged target survives the larger-frame interruptions; one-byte chunks retain 16 KiB buffers
--- PASS: TestVerifyC10_OneByteChunksAfter2KiBInterruptions (0.00s)
=== RUN   TestVerifyC10_RetainedVsCompactionDisabled
    verify_c10_chunk_target_test.go:137: 1-byte + 2048-byte frames x 31 (unread payload 63519 bytes):
    verify_c10_chunk_target_test.go:138:   compaction enabled : 62 entries, tiny payload 31 bytes in 448256 bytes of buffers; total retained 575232 bytes (9.1x payload)
    verify_c10_chunk_target_test.go:140:   compaction disabled: 62 entries, tiny payload 31 bytes in 31 bytes of buffers; total retained 127007 bytes (2.0x payload)
    verify_c10_chunk_target_test.go:137: 5-byte gRPC message header frame + 1025-byte message frame x 63 (unread payload 64890 bytes):
    verify_c10_chunk_target_test.go:138:   compaction enabled : 126 entries, tiny payload 315 bytes in 972544 bytes of buffers; total retained 1230592 bytes (19.0x payload)
    verify_c10_chunk_target_test.go:140:   compaction disabled: 126 entries, tiny payload 315 bytes in 315 bytes of buffers; total retained 258363 bytes (4.0x payload)
--- PASS: TestVerifyC10_RetainedVsCompactionDisabled (0.01s)
PASS
ok  	google.golang.org/grpc/internal/transport	1.025s
```

What the output shows: every 2048-byte frame ends the current chunk, and the next one-byte frame starts a new chunk whose target keeps doubling (`nextChunkSize … 512 1024 2048 4096 8192 16384 16384 …`); it is never reset by the interruption. With the default pool, 12 of the 16 one-byte chunks hold a 16384-byte backing buffer: 16 bytes of tiny payload occupy 202,496 bytes. In the mixed patterns compaction makes retention worse than having it disabled: 575,232 vs 127,007 bytes for 63,519 unread bytes (1-byte + 2048-byte frames), and 1,230,592 vs 258,363 bytes for 64,890 unread bytes (5-byte gRPC message header frame followed by a 1025-byte message frame, 63 times).

The byte-exact eval fixture passes on this branch, so it does not detect this:

```sh
cp ~/eval/tests/eval_recv_buffer_compaction_test.go internal/transport/eval_recv_buffer_compaction_test.go
go test -v -run '^TestEval_' google.golang.org/grpc/internal/transport -race -count=1
rm internal/transport/eval_recv_buffer_compaction_test.go
```

```console
$ go test -v -run '^TestEval_' google.golang.org/grpc/internal/transport -race -count=1
--- PASS: TestEval_RecvBufferCompaction (0.00s)
--- PASS: TestEval_RecvBufferCompactionDisabled (0.00s)
--- PASS: TestEval_RecvBufferCompactionSkippedLargeBuffer (0.01s)
--- PASS: TestEval_RecvBufferErrorResetSafety (0.00s)
--- PASS: TestEval_RecvBufferCompaction_MixedFrames (0.00s)
--- PASS: TestEval_RecvBufferCompaction_MultiCycleMemoryBound (0.00s)
ok  	google.golang.org/grpc/internal/transport	1.037s
```

Impact reasoning: when small frames alternate with frames above the compaction threshold on a slow stream, each small frame ends up alone in a chunk sized by the historical peak, 16 KiB from the default pool. That is the traffic shape of a peer that writes the 5-byte gRPC message header and the message body as separate DATA frames. In that shape the feature increases memory held per unread byte (19.0x payload with compaction vs 4.0x without in the probe), the opposite of the stated goal that memory track unread payload. The only workaround is `GRPC_GO_EXPERIMENTAL_ENABLE_RECEIVE_BUFFER_COMPACTION=false`. Bytes are still delivered in order.

## C11

Target: [evalon/grpc-go-tr-b490959e](https://github.com/kaitranntt-evals/grpc-go-transport-restrict-memory-overhead/tree/evalon/grpc-go-tr-b490959e) at `6830f4764e5291d456171ff7fe2cba240b4ec1a8`.

```sh
cd ~/repos/grpc-go                       # checkout of the verify branch; V points at its verify/ directory
V=~/repos/grpc-go/verify
git remote add claims https://github.com/kaitranntt-evals/grpc-go-transport-restrict-memory-overhead
git fetch claims evalon/grpc-go-tr-b490959e
git worktree add --detach ~/wt/b490959e FETCH_HEAD    # 6830f476
cd ~/wt/b490959e
```

Source trace (`internal/transport/transport.go`, `recvBuffer.load`): the `recvMsg` operand of the send, including `mem.NewBuffer(b.pending, b.pool)`, is evaluated before the `select` picks a case, and the `default` branch does nothing with it:

```go
	if len(b.backlog) == 0 && b.pending != nil {
		select {
		case b.c <- recvMsg{buffer: mem.NewBuffer(b.pending, b.pool)}:
			b.pending = nil
		default:
		}
	} else if len(b.backlog) > 0 {
```

Probe run. `verify/repro/c11_b490959e_mem_instrumentation.patch` adds two counters to `mem/buffers.go` (ref-counted wrappers created by `NewBuffer`, root wrappers released through `Free`); no behaviour change:

```sh
git apply $V/repro/c11_b490959e_mem_instrumentation.patch
cp $V/repro/c11_b490959e_abandoned_wrapper_test.go internal/transport/verify_c11_abandoned_wrapper_test.go
go test -v -run '^TestVerifyC11_' google.golang.org/grpc/internal/transport -count=1
git checkout -- mem && rm internal/transport/verify_c11_abandoned_wrapper_test.go
```

```console
=== RUN   TestVerifyC11_FullChannelLoadAbandonsWrapper
    verify_c11_abandoned_wrapper_test.go:58: step 3: writer refilled the delivery channel before the reader's load(): len(b.c)=1
    verify_c11_abandoned_wrapper_test.go:63: step 4: pending chunk: len=3000 cap=4096 (pool-backed: true), len(b.backlog)=0, len(b.c)=1
    verify_c11_abandoned_wrapper_test.go:71: step 5: reader's load() with a full channel: wrappers created by this call=1, wrappers freed=0, b.pending still owned by recvBuffer=true, len(b.c)=1
    verify_c11_abandoned_wrapper_test.go:75: C11 OBSERVED: load() constructed a pooled-buffer wrapper, took the full-channel default branch, and dropped the wrapper without Free
    verify_c11_abandoned_wrapper_test.go:86: 100 further load() calls while the channel stays full: wrappers created=100, freed=0
    verify_c11_abandoned_wrapper_test.go:100: drain: bytes intact=true; wrappers created=1 freed=1; pool gets=1 puts=1
--- PASS: TestVerifyC11_FullChannelLoadAbandonsWrapper (0.00s)
=== RUN   TestVerifyC11_ConcurrentReaderWriter
    verify_c11_abandoned_wrapper_test.go:138: concurrent run, 2000000 one-byte puts fully read and freed: pooled wrappers created=43, freed=25, abandoned=18; pool gets=38 puts=38
--- PASS: TestVerifyC11_ConcurrentReaderWriter (1.37s)
PASS
ok  	google.golang.org/grpc/internal/transport	2.391s
```

Part "Failed-send allocation" (observed): with the delivery channel full and a pool-backed pending chunk, one `load()` call created 1 wrapper and freed 0; the pending chunk stayed owned by the `recvBuffer`. 100 further calls in the same state created 100 more and freed none. The part holds.

Part "Full-channel reachability" (observed): the first test reaches the state with the documented reader protocol (receive from `get()`, then call `load()`), with a writer `put` slipping in between: step 3 shows the writer refilling the channel before the reader's `load()`, step 4 shows pooled pending data (`len=3000 cap=4096 (pool-backed: true)`). The second test does not force the interleaving: one writer goroutine and one reader goroutine following the protocol over 2,000,000 one-byte puts ended with `created=43, freed=25, abandoned=18`. The part holds.

Impact reasoning: every unsuccessful delivery attempt in that state takes a wrapper (and its reference counter) from `mem`'s internal object pools and drops it without `Free`, so those objects are never recycled and are left to the garbage collector. The pooled byte slice itself is not lost: it stays in `b.pending`, is wrapped again on the next successful `load()`, and in both tests the buffer pool's gets and puts balance and all bytes arrive intact. The cost is a small allocation per lost race (18 across 2,000,000 puts in the concurrent run) rather than a memory or data leak; the trigger is ordinary concurrent use.

## C12

Target: [evalon/grpc-go-tr-ac4f7aec](https://github.com/kaitranntt-evals/grpc-go-transport-restrict-memory-overhead/tree/evalon/grpc-go-tr-ac4f7aec) at `1e847624dd48c2bcd51d3292c7886f1ccf4c797c`.

```sh
cd ~/repos/grpc-go                       # checkout of the verify branch; V points at its verify/ directory
V=~/repos/grpc-go/verify
git remote add claims https://github.com/kaitranntt-evals/grpc-go-transport-restrict-memory-overhead
git fetch claims evalon/grpc-go-tr-ac4f7aec
git worktree add --detach ~/wt/ac4f7aec FETCH_HEAD    # 1e847624
cd ~/wt/ac4f7aec
```

Ownership trace (`internal/transport/recvbuffer_test.go`): `sendTinyDataFrames` dials and returns the connection; its only caller, `runTinyDataFramesServer` (used by `TestServerTransport_TinyDataFramesMemory`), closes it with a plain call on the last line of the success path. Neither function registers a `defer` or `t.Cleanup` for it, and `t.Fatalf` calls sit between the dial and the close in both:

```sh
grep -n "sendTinyDataFrames\|conn.Close\|Cleanup\|defer \|net.Dial" internal/transport/recvbuffer_test.go | sed -n '/^20[0-9]:/,$p'
```

```console
202:// sendTinyDataFrames connects to the server at addr using a raw HTTP/2
206:func sendTinyDataFrames(t *testing.T, addr string, n int) net.Conn {
208:	conn, err := net.Dial("tcp", addr)
210:		t.Fatalf("net.Dial(%q) failed: %v", addr, err)
287:	defer cancel()
293:	defer lis.Close()
298:		defer close(serverDone)
312:		defer st.Close(io.EOF)
317:	conn := sendTinyDataFrames(t, lis.Addr().String(), numFrames)
343:	conn.Close()
```

(`defer st.Close(io.EOF)` at line 312 is inside the server goroutine and only runs after `HandleStreams` returns, which requires the client connection to close first; `t.Fatalf` calls after the dial are at lines 213-248 inside the helper and 329-339 in the caller.)

Baseline (unmodified test):

```sh
go test -v -run 'Test/ServerTransport_TinyDataFramesMemory$' google.golang.org/grpc/internal/transport -count=1
```

```console
--- PASS: Test (0.16s)
    --- PASS: Test/ServerTransport_TinyDataFramesMemory (0.16s)
--- PASS: TestConnectionError_Unwrap (0.00s)
--- PASS: Test (0.00s)
ok  	google.golang.org/grpc/internal/transport	0.168s
```

Forced assertion failure in the caller, after `sendTinyDataFrames` returned the open connection and before `conn.Close()` (`verify/repro/c12_ac4f7aec_forced_assertion_failure.patch`, test file only: the byte comparison is made to fail, and an observation-only `t.Cleanup` reports whether the server transport ever sees the client connection close):

```sh
git apply $V/repro/c12_ac4f7aec_forced_assertion_failure.patch
go test -v -run 'Test/ServerTransport_TinyDataFramesMemory$' google.golang.org/grpc/internal/transport -count=1
git checkout -- .
```

```console
    recvbuffer_test.go:350: Byte 0 = 0, want 0
    recvbuffer_test.go:305: VERIFY C12: 3s after the assertion failure the server transport is still blocked reading: the client connection was NOT closed by any cleanup
    grpctest.go:45: Leaked goroutine: goroutine 11 [IO wait]:
        google.golang.org/grpc/internal/transport.(*http2Server).HandleStreams(0xc0000ec000, {0xcf2110, 0xc0002c8700}, 0xc000046200)
        google.golang.org/grpc/internal/transport.runTinyDataFramesServer.func2()
        created by google.golang.org/grpc/internal/transport.runTinyDataFramesServer in goroutine 9
    grpctest.go:45: Leaked goroutine: goroutine 21 [select]:
    grpctest.go:45: Leaked goroutine: goroutine 22 [select]:
--- FAIL: Test (13.07s)
    --- FAIL: Test/ServerTransport_TinyDataFramesMemory (13.06s)
--- PASS: TestConnectionError_Unwrap (0.00s)
--- PASS: Test (0.00s)
FAIL	google.golang.org/grpc/internal/transport	13.070s
```

(Goroutine dumps trimmed to the frames that identify them; 3 leaked goroutines were reported. Full output: `verify/logs/c12_forced.txt`.)

Variant with the forced failure inside the helper, after `net.Dial` succeeded (`verify/probes/c12_ac4f7aec_forced_failure_inside_helper.patch`):

```sh
git apply $V/probes/c12_ac4f7aec_forced_failure_inside_helper.patch
go test -v -run 'Test/ServerTransport_TinyDataFramesMemory$' google.golang.org/grpc/internal/transport -count=1
git checkout -- .
```

```console
    recvbuffer_test.go:328: Failed to write ping: <nil>
    recvbuffer_test.go:305: VERIFY C12: 3s after the assertion failure the server transport is still blocked reading: the client connection was NOT closed by any cleanup
    grpctest.go:45: Leaked goroutine: goroutine 11 [IO wait]:
        google.golang.org/grpc/internal/transport.(*http2Server).HandleStreams(0xc0000ec000, {0xcf2110, 0xc000348700}, 0xc000046200)
        google.golang.org/grpc/internal/transport.runTinyDataFramesServer.func2()
        created by google.golang.org/grpc/internal/transport.runTinyDataFramesServer in goroutine 9
    grpctest.go:45: Leaked goroutine: goroutine 34 [select]:
    grpctest.go:45: Leaked goroutine: goroutine 35 [select]:
--- FAIL: Test (13.10s)
    --- FAIL: Test/ServerTransport_TinyDataFramesMemory (13.10s)
--- PASS: TestConnectionError_Unwrap (0.00s)
--- PASS: Test (0.00s)
FAIL	google.golang.org/grpc/internal/transport	13.105s
```

What the output shows: after the assertion failure the test function exits through `t.Fatalf`, the deferred `cancel()` and `lis.Close()` run, and 3 s later the server transport is still blocked in `HandleStreams` reading from the connection: nothing closed the client side. The package's leak check then waits for goroutines to exit (the run takes 13 s instead of 0.2 s) and reports the server transport's reader, loopy writer and keepalive goroutines as leaked. Same result when the failure happens inside `sendTinyDataFrames`.

Impact reasoning: test-only. When this test fails for any reason after the dial, the raw client connection is left open, the server transport goroutines stay blocked on it, and the failure output is followed by about 10 s of leak-check waiting and "Leaked goroutine" reports that obscure the original assertion; the socket stays open for the rest of the test binary's life. On the passing path the connection is closed normally.
