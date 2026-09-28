# Evidence — grpc-go-transport-restrict-memory-overhead, audit run v-cb1d9bcd

Environment: `go version go1.25.7 linux/amd64`; workspace `~/repos/grpc-go` on branch
`verify/grpc-go-transport-restrict-memory-overhead-v-cb1d9bcd` = `327a6ff9 restrict memory overhead of buffering small data frames`
(origin/grpc-go-transport-restrict-memory-overhead-perfect, base `c92e9857`). Each claim branch
`evalon/grpc-go-tr-<sha>` was fetched from remote `evalrepo` (github.com/kaitranntt-evals/grpc-go-transport-restrict-memory-overhead)
and checked out as a detached worktree at `/home/ubuntu/wt/<sha>`:

```sh
git remote add evalrepo https://github.com/kaitranntt-evals/grpc-go-transport-restrict-memory-overhead.git
git fetch evalrepo 'refs/heads/evalon/*:refs/remotes/evalrepo/evalon/*'
for b in 04d52ba7 086a937f 0f57a03a 0fe051bd 1493d957 2db669b5 5b73028f 60f32c9f 6ffaf0fa 80ac4b7e 87d4ca45 99413fdb d67a6723 e008f402 eb3cc7a4; do
  git worktree add /home/ubuntu/wt/$b evalrepo/evalon/grpc-go-tr-$b; done
```

The byte-exact eval fixture `eval_tests.zip:tests/eval_recv_buffer_compaction_test.go` was extracted to
`/home/ubuntu/eval_tests/tests/` and, where a fixture run is quoted, copied to
`<worktree>/internal/transport/eval_recv_buffer_compaction_test.go` for the run and deleted afterwards
(no fixture copy is committed to any implementation checkout). Production code was never modified;
the controlled stalls for C1/C10 were applied to a branch's *test* file by
`verify/repro/c1/run_variant.py`, which restores the file after the run (each worktree shows a clean `git status`).

Primary-branch sanity (all exit 0):

```
$ cd ~/repos/grpc-go && go build ./...
exit=0
$ cd ~/repos/grpc-go && go test -race ./internal/transport -skip '^Test$/.*(SkippedLargeBuffer|Disabled)|.*(SkippedLargeBuffer|Disabled)' -count=1
ok  	google.golang.org/grpc/internal/transport	12.700s
exit=0
$ cd ~/repos/grpc-go && go vet ./...
exit=0
$ cd ~/repos/grpc-go && gofmt -l .
exit=0
```

Exact fixture on the primary branch (fixture copied to `internal/transport/`, then removed):

```
$ go test -v -run '^TestEval_RecvBufferCompaction$' google.golang.org/grpc/internal/transport -race -count=1
--- PASS: TestEval_RecvBufferCompaction (0.00s)
$ go test -v -run '^TestEval_RecvBufferCompaction_MixedFrames$' google.golang.org/grpc/internal/transport -race -count=1
--- PASS: TestEval_RecvBufferCompaction_MixedFrames (0.00s)
$ go test -v -run '^TestEval_RecvBufferCompaction_MultiCycleMemoryBound$' google.golang.org/grpc/internal/transport -race -count=1
--- PASS: TestEval_RecvBufferCompaction_MultiCycleMemoryBound (0.00s)
$ go test -v -run '^TestEval_RecvBufferErrorResetSafety$' google.golang.org/grpc/internal/transport -race -count=1
--- PASS: TestEval_RecvBufferErrorResetSafety (0.00s)
$ go test -v -run '^TestEval_RecvBufferCompactionSkippedLargeBuffer$' google.golang.org/grpc/internal/transport -race -count=1
--- PASS: TestEval_RecvBufferCompactionSkippedLargeBuffer (0.01s)
$ GRPC_GO_EXPERIMENTAL_ENABLE_RECEIVE_BUFFER_COMPACTION=false go test -v -run '^TestEval_RecvBufferCompactionDisabled$' google.golang.org/grpc/internal/transport -race -count=1
--- PASS: TestEval_RecvBufferCompactionDisabled (0.00s)
```

## C1

Claim: at least one added/changed receive-buffer test leaves a blocking operation or background goroutine
without an effective local completion bound. Method for every branch: read the added test's blocking
operations, then inject a controlled stall or early failure into the *test file only* with
`verify/repro/c1/run_variant.py` (anchor-replace → `go test -run <test> ./internal/transport -count=1 -timeout 40s -v`
→ restore the file). The whole batch is `verify/repro/c1/run_c1_stalls.sh`. Reading of results:
`panic: test timed out after 40s` = the test only ended because of go test's global `-timeout`, i.e. no
local bound (CONFIRMED); a failure at ≈10 s (`defaultTestTimeout`) or sooner = a local deadline/cleanup
path released it (REFUTED). Full outputs of every run are quoted below (exit/wall lines come from the harness).

### e008f402 — CONFIRMED

`internal/transport/recv_buffer_test.go` `TestRecvBufferCompactionOwnershipAndErrors` (line 264) builds a reader
with no context and loops until it sees the terminal error:

```go
				r := recvBufferReader{recv: &b}          // recv_buffer_test.go:300 — no ctx / ctxDone
				read := func() ([]byte, error) { ... r.ReadMessageHeader(buf) ... r.Read(1) ... }
```

Stall: the sub-case's three trailing puts (the one carrying `terminalErr`, a following buffer, and the
"later error") are removed, so the terminal error is never enqueued.

```
$ python3 verify/repro/c1/run_variant.py /home/ubuntu/wt/e008f402 internal/transport/recv_buffer_test.go e008f402_stall_ownership_noTerminalErr 'Test/RecvBufferCompactionOwnershipAndErrors/error=EOF/header=false' 40s '[["\t\t\t\tb.put(recvMsg{buffer: newBuffer('"'"'x'"'"'), err: terminalErr})\n\t\t\t\tb.put(recvMsg{buffer: newBuffer('"'"'y'"'"')})\n\t\t\t\tb.put(recvMsg{err: errors.New(\"later error\")})\n", "\t\t\t\t// injected stall: the terminal error is never enqueued\n"]]'
# exit=1 wall=41.8s
panic: test timed out after 40s
goroutine 10 [select]:
	/home/ubuntu/wt/e008f402/internal/transport/transport.go:242 +0x85      (recvBufferReader.read: select on recv.get() with nil ctxDone)
	/home/ubuntu/wt/e008f402/internal/transport/transport.go:227 +0x67
	/home/ubuntu/wt/e008f402/internal/transport/recv_buffer_test.go:305 +0x7c
	/home/ubuntu/wt/e008f402/internal/transport/recv_buffer_test.go:320 +0x7d2
FAIL	google.golang.org/grpc/internal/transport	40.107s
```

Control (same branch, `TestRecvBufferCompactionConcurrentRead`, which *does* pass `ctx` to its reader): withholding
the producer's `io.EOF` ends at the 10 s context deadline, showing what a bounded test on this branch looks like:

```
$ python3 verify/repro/c1/run_variant.py /home/ubuntu/wt/e008f402 internal/transport/recv_buffer_test.go e008f402_stall_noEOF 'Test/RecvBufferCompactionConcurrentRead' 40s '[["\t\tb.put(recvMsg{err: io.EOF})\n\t}()\n\tr := recvBufferReader", "\t}()\n\tr := recvBufferReader"]]'
# exit=1 wall=12.0s
    recv_buffer_test.go:364: rpc error: code = DeadlineExceeded desc = context deadline exceeded
--- FAIL: Test (10.00s)
```

Impact: if the code under test ever fails to deliver the terminal error (the exact regression this test
exists to catch), the ownership test hangs until the package-wide `go test` timeout (10 min by default)
instead of failing in 10 s with a useful message.

### 086a937f — CONFIRMED

`internal/transport/recv_buffer_test.go` `TestServerStream_ManySmallDataFrames` (line 286) drives a real server
transport from a raw client socket; the raw writes/flush have no socket deadline and the test's `ctx` is not
consulted by them (lines 366–375: `framer.WriteData(...)` in a loop, then `bw.Flush()`).

Stall: the server goroutine sets a 4 KiB receive buffer and blocks (`select {}`) instead of calling
`NewServerTransport`; the client socket gets a 4 KiB send buffer.

```
$ python3 verify/repro/c1/run_variant.py /home/ubuntu/wt/086a937f internal/transport/recv_buffer_test.go 086a937f_stall_serverNoRead 'Test/ServerStream_ManySmallDataFrames/compaction_enabled' 40s '[["\t\t\t\tst, err := NewServerTransport(conn, &ServerConfig{", "\t\t\t\tconn.(*net.TCPConn).SetReadBuffer(4096)\n\t\t\t\tselect {} // injected stall: server never reads\n\t\t\t\tst, err := NewServerTransport(conn, &ServerConfig{"], ["\t\t\tdefer conn.Close()\n\n\t\t\t// Discard frames from the server.", "\t\t\tdefer conn.Close()\n\t\t\tconn.(*net.TCPConn).SetWriteBuffer(4096)\n\n\t\t\t// Discard frames from the server."]]'
# exit=1 wall=42.1s
panic: test timed out after 40s
goroutine 10 [IO wait]:
	/usr/local/go/src/internal/poll/fd_unix.go:390 +0x2fc                    (net.Conn.Write)
	/usr/local/go/src/net/net.go:208 +0x45
	/home/ubuntu/wt/086a937f/internal/transport/recv_buffer_test.go:373 +0xc65   (bw.Flush())
goroutine 12 [select (no cases)]:
	/home/ubuntu/wt/086a937f/internal/transport/recv_buffer_test.go:319 +0xa5
FAIL	google.golang.org/grpc/internal/transport	40.011s
```

Impact: a server that stops reading (e.g. a flow-control or HandleStreams regression) turns this test into a
global-timeout hang; the `ctx` created by the test never interrupts the socket write.

### 5b73028f — REFUTED

`internal/transport/recv_buffer_test.go` `TestClientStream_ManySmallDataFrames`: the server writer is the
`serverFrames` callback run inside `setupRSTStreamOnEOSTest`'s server goroutine (lines 362–391); it writes
1-byte DATA frames with `t.Errorf(...); return` on error. Early failure injected while the writer is still
running (a `t.Fatalf` right before the `select { case <-stream.Done(): ... }` wait):

```
$ python3 verify/repro/c1/run_variant.py /home/ubuntu/wt/5b73028f internal/transport/recv_buffer_test.go 5b73028f_earlyfail_whileWriting 'Test/ClientStream_ManySmallDataFrames' 40s '[["\t\t// Read only after all frames have been received and queued.\n\t\tselect {", "\t\tt.Fatalf(\"injected early failure while the server writer is still running\")\n\t\t// Read only after all frames have been received and queued.\n\t\tselect {"]]'
# exit=1 wall=1.2s
    recv_buffer_test.go:398: injected early failure while the server writer is still running
    recv_buffer_test.go:379: Server failed to write data: write tcp 127.0.0.1:40241->127.0.0.1:39262: write: connection reset by peer
    transport_test.go:3693: Server reader goroutine failed to read frame: EOF
--- FAIL: Test (0.05s)
FAIL	google.golang.org/grpc/internal/transport	0.056s
```

Early failure after the stream finished (before `ReadMessageHeader`) likewise ends immediately:

```
$ python3 verify/repro/c1/run_variant.py /home/ubuntu/wt/5b73028f internal/transport/recv_buffer_test.go 5b73028f_earlyfail_afterDone 'Test/ClientStream_ManySmallDataFrames' 40s '[["\t\theader := make([]byte, 5)\n\t\tif err := stream.ReadMessageHeader(header); err != nil {\n\t\t\tt.Fatalf(\"stream.ReadMessageHeader() failed: %v\", err)", "\t\tif true {\n\t\t\tt.Fatalf(\"injected early failure after the stream finished\")\n\t\t}\n\t\theader := make([]byte, 5)\n\t\tif err := stream.ReadMessageHeader(header); err != nil {\n\t\t\tt.Fatalf(\"stream.ReadMessageHeader() failed: %v\", err)"]]'
# exit=1 wall=2.1s
    recv_buffer_test.go:411: injected early failure after the stream finished
--- FAIL: Test (0.07s)
```

The writer goroutine is torn down through the client connection being closed (its write fails with
"connection reset by peer" and it returns), and its error is reported inside the test (no "Fail in goroutine
after test completed" panic). Cleanup is bounded.

### 0fe051bd — REFUTED

`internal/transport/recv_buffer_test.go` `TestServerReceivesMessageInTinyDataFrames` (lines 298–306):

```go
	mconn, err := net.Dial("tcp", server.lis.Addr().String())
	...
	defer mconn.Close()
	if err := mconn.SetDeadline(time.Now().Add(defaultTestTimeout)); err != nil {
```

`net.Dial` has no explicit timeout, but it dials a listener the test itself created; a TCP connect to a
listening socket completes in the kernel backlog even if nobody calls `Accept`. Stall: the test dials a
listener whose `Accept` is delayed 2 s and whose peer then never reads (4 KiB buffers):

```
$ python3 verify/repro/c1/run_variant.py /home/ubuntu/wt/0fe051bd internal/transport/recv_buffer_test.go 0fe051bd_stall_serverNoRead 'Test/ServerReceivesMessageInTinyDataFrames' 40s '<edit: see verify/repro/c1/run_c1_stalls.sh, label 0fe051bd_stall_serverNoRead>'
# exit=1 wall=22.0s
    recv_buffer_test.go:314: net.Dial returned after 347.541µs (Accept delayed by 2s)
    recv_buffer_test.go:356: Error while reading frame: read tcp 127.0.0.1:46606->127.0.0.1:41283: i/o timeout
    grpctest.go:45: Leaked goroutine: goroutine 13 [select (no cases)]:      (the injected `select {}` peer, not test code)
--- FAIL: Test (20.04s)
```

Dial returned in 347 µs without an accepting peer, and every subsequent socket operation was bounded by the
`SetDeadline` (i/o timeout at 10 s; the remaining 10 s is grpctest's leak-check wait for the injected goroutine).

### 04d52ba7 — REFUTED

`internal/transport/recv_buffer_compaction_test.go` `TestServerReceivesManySmallDataFrames` (lines 409–415) uses
the same pattern with `mconn.SetWriteDeadline(time.Now().Add(defaultTestTimeout))`; the reader goroutine is
released by `mconn.Close()` (deferred, and explicitly before `<-readerDone` at line 509–510). Same stall:

```
$ python3 verify/repro/c1/run_variant.py /home/ubuntu/wt/04d52ba7 internal/transport/recv_buffer_compaction_test.go 04d52ba7_stall_serverNoRead 'Test/ServerReceivesManySmallDataFrames' 40s '<edit: see verify/repro/c1/run_c1_stalls.sh, label 04d52ba7_stall_serverNoRead>'
# exit=1 wall=22.2s
    recv_buffer_compaction_test.go:425: net.Dial returned after 111.042µs (Accept delayed by 2s)
    recv_buffer_compaction_test.go:489: Error while writing DATA frame 4030: write tcp 127.0.0.1:60222->127.0.0.1:37861: i/o timeout
    grpctest.go:45: Leaked goroutine: goroutine 26 [select (no cases)]:
--- FAIL: Test (20.08s)
```

Connection establishment completed immediately; the blocked write failed at the 10 s write deadline.

### 60f32c9f — CONFIRMED

`internal/transport/recv_buffer_compaction_test.go` `TestServerReceivesManySmallDataFrames` (line 451) creates
`ctx` (line 477) but the raw client writes (lines 540–545) use a plain `net.Conn` with no deadline:

```go
				writeMu.Lock()
				err := framer.WriteData(1, i == len(payloads)-1, p)
				writeMu.Unlock()
```

Stall: the server's stream handler blocks after handing over the stream (`streamCh <- s; select {}`), which
stops `HandleStreams`' frame-read loop, so the server stops reading the socket after HEADERS; 4 KiB socket buffers.

```
$ python3 verify/repro/c1/run_variant.py /home/ubuntu/wt/60f32c9f internal/transport/recv_buffer_compaction_test.go 60f32c9f_stall_handlerBlocksReader 'Test/ServerReceivesManySmallDataFrames/enabled' 40s '<edit: see verify/repro/c1/run_c1_stalls.sh, label 60f32c9f_stall_handlerBlocksReader>'
# exit=1 wall=41.6s
panic: test timed out after 40s
goroutine 23 [IO wait]:
	/usr/local/go/src/internal/poll/fd_unix.go:390 +0x2fc                    (net.Conn.Write)
	/usr/local/go/src/net/net.go:208 +0x45
	/home/ubuntu/wt/60f32c9f/internal/transport/recv_buffer_compaction_test.go:542 +0x9ee   (framer.WriteData)
goroutine 25 [select (no cases)]:
	/home/ubuntu/wt/60f32c9f/internal/transport/recv_buffer_compaction_test.go:504 +0x2a
FAIL	google.golang.org/grpc/internal/transport	40.008s
```

Control: when the server never creates a transport at all, the test's `ctx`-bounded wait for the stream releases it at 10 s
(`recv_buffer_compaction_test.go:536: Timed out waiting for the server to create the stream`, `--- FAIL: Test (20.08s)`,
label `60f32c9f_stall_serverNoRead`). The raw DATA writes themselves are the unbounded operation.

Impact: a server-side read stall converts this regression test into a global-timeout hang.

### d67a6723 — CONFIRMED

`internal/transport/recv_buffer_compaction_test.go` `TestClientReceivesManySmallDataFrames` (lines 326–340):

```go
	testDone := make(chan struct{})
	serverDone := make(chan struct{})
	go func() {
		defer close(serverDone)
		conn, err := lis.Accept()
		...
		<-testDone
	}()
	defer func() {
		close(testDone)
		<-serverDone          // no select on ctx.Done(), lis is not closed first
	}()
	ct, err := NewHTTP2Client(ctx, ctx, ...)
```

Early failure injected just before `NewHTTP2Client` (so nothing ever connects to `lis`):

```
$ python3 verify/repro/c1/run_variant.py /home/ubuntu/wt/d67a6723 internal/transport/recv_buffer_compaction_test.go d67a6723_earlyfail_beforeClient 'Test/ClientReceivesManySmallDataFrames' 40s '<edit: see verify/repro/c1/run_c1_stalls.sh, label d67a6723_earlyfail_beforeClient>'
# exit=1 wall=42.0s
    recv_buffer_compaction_test.go:406: injected early failure before the client transport is created
panic: test timed out after 40s
goroutine 9 [chan receive]:
	/home/ubuntu/wt/d67a6723/internal/transport/recv_buffer_compaction_test.go:402 +0x31   (deferred <-serverDone)
	/home/ubuntu/wt/d67a6723/internal/transport/recv_buffer_compaction_test.go:406 +0x28e
goroutine 11 [IO wait]:
	/usr/local/go/src/internal/poll/fd_unix.go:613 +0x28c                    (Accept)
	/home/ubuntu/wt/d67a6723/internal/transport/recv_buffer_compaction_test.go:330 +0x98
FAIL	google.golang.org/grpc/internal/transport	40.107s
```

Impact: any assertion failure between `net.Listen` and a successful dial (the ordinary "setup failed" case)
hangs the test at cleanup until the global timeout.

### 80ac4b7e — REFUTED

`internal/transport/recv_buffer_test.go` `TestReceiveBufferCompactionConcurrent` (lines 238–266): the reader
carries `ctx`/`ctxDone` (10 s); the producer goroutine only calls `queue.put` (non-blocking: `put` appends to
the backlog when the channel is full) and is joined by `defer func() { <-done }()`. Stall: the producer's
`io.EOF` is withheld.

```
$ python3 verify/repro/c1/run_variant.py /home/ubuntu/wt/80ac4b7e internal/transport/recv_buffer_test.go 80ac4b7e_stall_noEOF 'Test/ReceiveBufferCompactionConcurrent$' 40s '[["\t\tqueue.put(recvMsg{err: io.EOF})\n\t}()\n\tdefer func() { <-done }()", "\t}()\n\tdefer func() { <-done }()"]]'
# exit=1 wall=12.0s
    recv_buffer_test.go:263: Read() failed: rpc error: code = DeadlineExceeded desc = context deadline exceeded
--- FAIL: Test (10.00s)
FAIL	google.golang.org/grpc/internal/transport	10.006s
```

The reader is released by its context at 10.00 s and the deferred join returns at once because the producer's
finite, non-blocking loop had already completed. Bounded.

### eb3cc7a4 — CONFIRMED

`internal/transport/recv_buffer_test.go` `TestRecvBufferCompactionOwnership` (line 174) reads with a
`recvBufferReader` that has no context until it observes the terminal error. Stall: the put that carries
`terminalErr` is changed to carry no error.

```
$ python3 verify/repro/c1/run_variant.py /home/ubuntu/wt/eb3cc7a4 internal/transport/recv_buffer_test.go eb3cc7a4_stall_ownership_noErr 'Test/RecvBufferCompactionOwnership' 40s '[["\t\t\tput([]byte{103}, terminalErr)", "\t\t\tput([]byte{103}, nil) // injected stall: terminal error never queued"]]'
# exit=1 wall=41.3s
    recv_buffer_test.go:206: released 104 buffers before reading, want 103
    recv_buffer_test.go:223: read 3: payload mismatch
panic: test timed out after 40s
goroutine 10 [select]:
	/home/ubuntu/wt/eb3cc7a4/internal/transport/transport.go:240 +0x85      (recvBufferReader.read with nil ctxDone)
	/home/ubuntu/wt/eb3cc7a4/internal/transport/transport.go:225 +0x67
	/home/ubuntu/wt/eb3cc7a4/internal/transport/recv_buffer_test.go:231 +0xb27
FAIL	google.golang.org/grpc/internal/transport	40.114s
```

The concurrent test on the same branch (`TestRecvBufferCompactionConcurrentRead`, reader with `ctx`) is
bounded when EOF is withheld: `recv_buffer_test.go:310: ... context deadline exceeded`, `--- FAIL: Test (10.00s)`
(label `eb3cc7a4_stall_noEOF_concurrent`).

Impact: the ownership test hangs to the global timeout precisely when the error-delivery behaviour it checks regresses.

### 99413fdb — REFUTED

`internal/transport/recv_buffer_test.go` `TestReceiveBufferCompactionConcurrentReads` (lines 290–314): producer
of non-blocking `queue.put` calls joined by `defer func() { <-done }()`; reader has `ctx`/`ctxDone`. EOF withheld:

```
$ python3 verify/repro/c1/run_variant.py /home/ubuntu/wt/99413fdb internal/transport/recv_buffer_test.go 99413fdb_stall_noEOF 'Test/ReceiveBufferCompactionConcurrentReads' 40s '[["\t\tqueue.put(recvMsg{err: io.EOF})\n\t}()\n\tdefer func() { <-done }()", "\t}()\n\tdefer func() { <-done }()"]]'
# exit=1 wall=12.0s
    recv_buffer_test.go:312: rpc error: code = DeadlineExceeded desc = context deadline exceeded
--- FAIL: Test (10.05s)
FAIL	google.golang.org/grpc/internal/transport	10.056s
```

Released at the 10 s context deadline; the producer join is effectively bounded by the producer's finite non-blocking loop.

### 2db669b5 — CONFIRMED

`internal/transport/recv_buffer_test.go` `TestReceiveBufferCompactionNoRecopy` (lines 187–192) reads with a bare
channel receive and no deadline:

```go
	recv.put(recvMsg{err: io.EOF})
	var got []byte
	for {
		msg := <-recv.get()
		recv.load()
		if msg.err != nil {
```

Stall: the `io.EOF` put is removed.

```
$ python3 verify/repro/c1/run_variant.py /home/ubuntu/wt/2db669b5 internal/transport/recv_buffer_test.go 2db669b5_stall_norecopy_noEOF 'Test/ReceiveBufferCompactionNoRecopy' 40s '[["\trecv.put(recvMsg{err: io.EOF})\n\tvar got []byte\n\tfor {\n\t\tmsg := <-recv.get()", "\tvar got []byte // injected stall: EOF never queued\n\tfor {\n\t\tmsg := <-recv.get()"]]'
# exit=1 wall=43.9s
panic: test timed out after 40s
goroutine 9 [chan receive]:
	/home/ubuntu/wt/2db669b5/internal/transport/recv_buffer_test.go:189 +0x5f8
FAIL	google.golang.org/grpc/internal/transport	40.105s
```

The branch's concurrent test (`TestReceiveBufferCompactionConcurrentRead`, reader with `ctx`) is bounded:
`recv_buffer_test.go:326: ... context deadline exceeded`, `--- FAIL: Test (10.05s)` (label `2db669b5_stall_noEOF_concurrent`).

Impact: if compaction ever drops or reorders the terminal message, `TestReceiveBufferCompactionNoRecopy` hangs to the global timeout.

## C2

Claim: receiving many small frames does not compact the queued receive buffers as required. Measured with the
byte-exact fixture `TestEval_RecvBufferCompaction` (1,026 one-byte pooled buffers via `put`, asserts
`len(b.backlog) <= 64`, then drains and compares the payload) on each branch.

### 87d4ca45 — CONFIRMED

The fixture's constructor shim only recognises `init(mem.BufferPool)` or `init()`:

```go
func initRecvBufferForTest(b *recvBuffer, pool mem.BufferPool) {
	if bi, ok := any(b).(interface{ init(mem.BufferPool) }); ok {
		bi.init(pool)
	} else if bi, ok := any(b).(interface{ init() }); ok {
		bi.init()
	} else if b.c == nil {
		b.c = make(chan recvMsg, 1)
	}
}
```

This branch's receive buffer is `func (b *recvBuffer) init(compact bool)` (`internal/transport/transport.go:122`),
compaction is a per-buffer opt-in (`b.compact = compact`), and a buffer that is not explicitly opted in never
compacts. Fixture (copied byte-exact into `internal/transport/`, run from `/home/ubuntu/wt/87d4ca45`):

```
$ go test -v -run '^TestEval_RecvBufferCompaction$' google.golang.org/grpc/internal/transport -race -count=1
=== RUN   TestEval_RecvBufferCompaction
    eval_recv_buffer_compaction_test.go:78: Got backlog length 1025 after compaction, want <= 64
--- FAIL: TestEval_RecvBufferCompaction (0.00s)
FAIL	google.golang.org/grpc/internal/transport	0.015s
```

Replay: `bash verify/repro/c2_c3_c4_87d4ca45/run.sh` from the branch checkout (copies the fixture in, runs
the C2/C3/C4 commands, removes the copy). Output of that replay, verbatim:

```
$ bash /home/ubuntu/repos/grpc-go/verify/repro/c2_c3_c4_87d4ca45/run.sh
=== RUN   TestEval_RecvBufferCompaction
    eval_recv_buffer_compaction_test.go:78: Got backlog length 1025 after compaction, want <= 64
--- FAIL: TestEval_RecvBufferCompaction (0.00s)
=== RUN   TestEval_RecvBufferCompaction_MixedFrames
    eval_recv_buffer_compaction_test.go:263: Got backlog length 1073 after mixed frame compaction, want <= 134
--- FAIL: TestEval_RecvBufferCompaction_MixedFrames (0.00s)
=== RUN   TestEval_RecvBufferCompaction_MultiCycleMemoryBound
    eval_recv_buffer_compaction_test.go:303: Cycle 0: backlog length 1123 exceeded bound 512
--- FAIL: TestEval_RecvBufferCompaction_MultiCycleMemoryBound (0.00s)
exit=1
```

Scope of what this shows: a `recvBuffer` that is constructed without an explicit `init(true)` — which is what
the eval's construction path does — keeps one backlog entry per one-byte frame (1,025 for 1,026 frames).
The production constructors on this branch do opt in (`http2_client.go:508 s.Stream.buf.init(t.compactRecvBuffers)`,
`http2_server.go:415`, `handler_server.go:433`, with `compactRecvBuffers: envconfig.EnableReceiveBufferCompaction`),
and with a temporary fixture copy whose shim was changed to call `b.init(true)` all six fixture tests pass
(`--- PASS: TestEval_RecvBufferCompaction (0.00s)` …); the branch's own transport-level tests also pass:

```
$ cd /home/ubuntu/wt/87d4ca45 && go test -v -run "Test/RecvBufferCompaction_|Test/ClientReceivesManySmallDataFrames|Test/ServerReceivesManySmallDataFrames" ./internal/transport -race -count=1
    --- PASS: Test/ClientReceivesManySmallDataFrames/compaction=true (0.01s)
    --- PASS: Test/RecvBufferCompaction_ManySmallMessages/enabled (0.00s)
    --- PASS: Test/ServerReceivesManySmallDataFrames (0.02s)
ok  	google.golang.org/grpc/internal/transport	1.083s
```

Impact reasoning: the required behaviour is compaction *by default*; on this branch the default state of a
receive buffer is "no compaction" and it is only switched on where a constructor remembers to pass `true`.
The eval's check — and any receive buffer constructed the way the check constructs it — therefore buffers
one entry per frame, exactly the memory growth the task set out to remove. The three exact fixture checks
for this claim family (C2/C3/C4) fail on this branch and pass on every other branch audited.

### 6ffaf0fa — REFUTED

The branch defines its own `drainRecvBuffer(t, *recvBuffer) ([]byte, error)`, so the byte-exact fixture does
not compile alongside it (`drainRecvBuffer redeclared` / `assignment mismatch ... want (*testing.T, *recvBuffer, []byte)`).
The fixture was rerun with only its helper renamed (`sed 's/\bdrainRecvBuffer\b/evalDrainRecvBuffer/g'`), no other change:

```
$ cd /home/ubuntu/wt/6ffaf0fa && go test -v -run '^TestEval_RecvBufferCompaction$' google.golang.org/grpc/internal/transport -race -count=1
--- PASS: TestEval_RecvBufferCompaction (0.00s)
ok  	google.golang.org/grpc/internal/transport	1.018s
```

Backlog stayed ≤ 64 entries for 1,026 one-byte frames and the drained payload matched byte-for-byte.

### 0f57a03a — REFUTED

Same helper-name clash, same rename-only rerun:

```
$ cd /home/ubuntu/wt/0f57a03a && go test -v -run '^TestEval_RecvBufferCompaction$' google.golang.org/grpc/internal/transport -race -count=1
--- PASS: TestEval_RecvBufferCompaction (0.00s)
ok  	google.golang.org/grpc/internal/transport	1.014s
```

### d67a6723 — REFUTED

Same helper-name clash, same rename-only rerun:

```
$ cd /home/ubuntu/wt/d67a6723 && go test -v -run '^TestEval_RecvBufferCompaction$' google.golang.org/grpc/internal/transport -race -count=1
--- PASS: TestEval_RecvBufferCompaction (0.00s)
ok  	google.golang.org/grpc/internal/transport	1.015s
```

## C3

Claim: mixed-size small frames (1–7 bytes) are not compacted as required. Measured with the byte-exact fixture
`TestEval_RecvBufferCompaction_MixedFrames` (1,074 frames of 1–7 bytes, asserts `len(b.backlog) <= 134`, then
drains and compares).

### 87d4ca45 — CONFIRMED

The fixture's `initRecvBufferForTest` cannot call this branch's `init(compact bool)` (see C2), so the buffer's
default no-compaction state applies:

```
$ cd /home/ubuntu/wt/87d4ca45 && go test -v -run '^TestEval_RecvBufferCompaction_MixedFrames$' google.golang.org/grpc/internal/transport -race -count=1
=== RUN   TestEval_RecvBufferCompaction_MixedFrames
    eval_recv_buffer_compaction_test.go:263: Got backlog length 1073 after mixed frame compaction, want <= 134
--- FAIL: TestEval_RecvBufferCompaction_MixedFrames (0.00s)
FAIL	google.golang.org/grpc/internal/transport	0.015s
```

Replay: `bash verify/repro/c2_c3_c4_87d4ca45/run.sh` from the branch checkout (second test in the script; verbatim output in C2).
With the shim changed to `b.init(true)` the same test passes (`--- PASS: TestEval_RecvBufferCompaction_MixedFrames (0.00s)`),
i.e. the compaction logic exists but is off unless a caller opts the buffer in.

Impact: one backlog entry (56-byte `recvMsg` + a pooled buffer) is retained per 1–7-byte frame for any receive
buffer not explicitly opted in; 1,073 entries for ~4 KiB of payload.

### 6ffaf0fa — REFUTED

```
$ cd /home/ubuntu/wt/6ffaf0fa && go test -v -run '^TestEval_RecvBufferCompaction_MixedFrames$' google.golang.org/grpc/internal/transport -race -count=1
--- PASS: TestEval_RecvBufferCompaction_MixedFrames (0.00s)
ok  	google.golang.org/grpc/internal/transport	1.017s
```

(fixture with its `drainRecvBuffer` helper renamed to `evalDrainRecvBuffer` to avoid a redeclaration with the branch's helper; no other change.)

### 0f57a03a — REFUTED

```
$ cd /home/ubuntu/wt/0f57a03a && go test -v -run '^TestEval_RecvBufferCompaction_MixedFrames$' google.golang.org/grpc/internal/transport -race -count=1
--- PASS: TestEval_RecvBufferCompaction_MixedFrames (0.00s)
ok  	google.golang.org/grpc/internal/transport	1.015s
```

(same rename-only fixture copy.)

### d67a6723 — REFUTED

```
$ cd /home/ubuntu/wt/d67a6723 && go test -v -run '^TestEval_RecvBufferCompaction_MixedFrames$' google.golang.org/grpc/internal/transport -race -count=1
--- PASS: TestEval_RecvBufferCompaction_MixedFrames (0.00s)
ok  	google.golang.org/grpc/internal/transport	1.016s
```

(same rename-only fixture copy.)

## C4

Claim: memory is not bounded across repeated fill/partial-drain cycles. Measured with the byte-exact fixture
`TestEval_RecvBufferCompaction_MultiCycleMemoryBound` (3 cycles × 1,124 frames of 1–5 bytes, one `load()` +
channel receive per cycle, asserts `len(b.backlog) <= 512` after each cycle, then drains and compares).

### 87d4ca45 — CONFIRMED

```
$ cd /home/ubuntu/wt/87d4ca45 && go test -v -run '^TestEval_RecvBufferCompaction_MultiCycleMemoryBound$' google.golang.org/grpc/internal/transport -race -count=1
=== RUN   TestEval_RecvBufferCompaction_MultiCycleMemoryBound
    eval_recv_buffer_compaction_test.go:303: Cycle 0: backlog length 1123 exceeded bound 512
--- FAIL: TestEval_RecvBufferCompaction_MultiCycleMemoryBound (0.00s)
FAIL	google.golang.org/grpc/internal/transport	0.015s
```

Cause is the same as C2: this branch's `recvBuffer.init(compact bool)` makes compaction a per-buffer opt-in
and the fixture's shim (which knows `init(mem.BufferPool)` / `init()`) leaves the buffer in its default
no-compaction state. Replay: `bash verify/repro/c2_c3_c4_87d4ca45/run.sh` (third test; verbatim output in C2).
With the shim changed to `b.init(true)` the test passes (`--- PASS: TestEval_RecvBufferCompaction_MultiCycleMemoryBound (0.00s)`).

Impact: the backlog grows by one entry per frame in every cycle (1,123 entries after the first 1,124 frames) — the
unbounded per-frame overhead the task was meant to remove — for any buffer that is not explicitly opted in.

### 6ffaf0fa — REFUTED

```
$ cd /home/ubuntu/wt/6ffaf0fa && go test -v -run '^TestEval_RecvBufferCompaction_MultiCycleMemoryBound$' google.golang.org/grpc/internal/transport -race -count=1
--- PASS: TestEval_RecvBufferCompaction_MultiCycleMemoryBound (0.00s)
ok  	google.golang.org/grpc/internal/transport	1.016s
```

(fixture with `drainRecvBuffer` renamed to `evalDrainRecvBuffer`; no other change.)

### 0f57a03a — REFUTED

```
$ cd /home/ubuntu/wt/0f57a03a && go test -v -run '^TestEval_RecvBufferCompaction_MultiCycleMemoryBound$' google.golang.org/grpc/internal/transport -race -count=1
--- PASS: TestEval_RecvBufferCompaction_MultiCycleMemoryBound (0.00s)
ok  	google.golang.org/grpc/internal/transport	1.017s
```

(same rename-only fixture copy.)

### d67a6723 — REFUTED

```
$ cd /home/ubuntu/wt/d67a6723 && go test -v -run '^TestEval_RecvBufferCompaction_MultiCycleMemoryBound$' google.golang.org/grpc/internal/transport -race -count=1
--- PASS: TestEval_RecvBufferCompaction_MultiCycleMemoryBound (0.00s)
ok  	google.golang.org/grpc/internal/transport	1.017s
```

(same rename-only fixture copy.)

## C5

Claim: an error queued behind compacted small frames breaks ordering / repeated error reads. Measured with the
byte-exact fixture `TestEval_RecvBufferErrorResetSafety` (small frames, then `put(recvMsg{err: ...})`, then more
frames; drains and checks that all pre-error payload is delivered, the error is observed at the right point, and
repeated reads keep returning the error). On all three branches the fixture (with only its `drainRecvBuffer`
helper renamed to `evalDrainRecvBuffer`, because each branch defines a helper of that name with a different
signature) passes:

```
$ cd /home/ubuntu/wt/6ffaf0fa && go test -v -run '^TestEval_RecvBufferErrorResetSafety$' google.golang.org/grpc/internal/transport -race -count=1
--- PASS: TestEval_RecvBufferErrorResetSafety (0.00s)
ok  	google.golang.org/grpc/internal/transport	1.017s
$ cd /home/ubuntu/wt/0f57a03a && go test -v -run '^TestEval_RecvBufferErrorResetSafety$' google.golang.org/grpc/internal/transport -race -count=1
--- PASS: TestEval_RecvBufferErrorResetSafety (0.00s)
ok  	google.golang.org/grpc/internal/transport	1.025s
$ cd /home/ubuntu/wt/d67a6723 && go test -v -run '^TestEval_RecvBufferErrorResetSafety$' google.golang.org/grpc/internal/transport -race -count=1
--- PASS: TestEval_RecvBufferErrorResetSafety (0.00s)
ok  	google.golang.org/grpc/internal/transport	1.015s
```

Verdict: REFUTED on 6ffaf0fa, 0f57a03a and d67a6723.

## C6

Claim: the environment escape hatch / default does not behave as required. Measured with the byte-exact fixture
`TestEval_RecvBufferCompactionDisabled`, which reads `GRPC_GO_EXPERIMENTAL_ENABLE_RECEIVE_BUFFER_COMPACTION`
itself: with `=false` it requires exactly one backlog entry per frame (1,025), otherwise it requires ≤ 64.
Run both ways on each branch (fixture with only `drainRecvBuffer` renamed to `evalDrainRecvBuffer`):

```
$ cd /home/ubuntu/wt/6ffaf0fa && GRPC_GO_EXPERIMENTAL_ENABLE_RECEIVE_BUFFER_COMPACTION=false go test -v -run '^TestEval_RecvBufferCompactionDisabled$' google.golang.org/grpc/internal/transport -race -count=1
--- PASS: TestEval_RecvBufferCompactionDisabled (0.00s)
$ cd /home/ubuntu/wt/6ffaf0fa && go test -v -run '^TestEval_RecvBufferCompactionDisabled$' google.golang.org/grpc/internal/transport -race -count=1
--- PASS: TestEval_RecvBufferCompactionDisabled (0.00s)
$ cd /home/ubuntu/wt/0f57a03a && GRPC_GO_EXPERIMENTAL_ENABLE_RECEIVE_BUFFER_COMPACTION=false go test -v -run '^TestEval_RecvBufferCompactionDisabled$' google.golang.org/grpc/internal/transport -race -count=1
--- PASS: TestEval_RecvBufferCompactionDisabled (0.00s)
$ cd /home/ubuntu/wt/0f57a03a && go test -v -run '^TestEval_RecvBufferCompactionDisabled$' google.golang.org/grpc/internal/transport -race -count=1
--- PASS: TestEval_RecvBufferCompactionDisabled (0.00s)
$ cd /home/ubuntu/wt/d67a6723 && GRPC_GO_EXPERIMENTAL_ENABLE_RECEIVE_BUFFER_COMPACTION=false go test -v -run '^TestEval_RecvBufferCompactionDisabled$' google.golang.org/grpc/internal/transport -race -count=1
--- PASS: TestEval_RecvBufferCompactionDisabled (0.00s)
$ cd /home/ubuntu/wt/d67a6723 && go test -v -run '^TestEval_RecvBufferCompactionDisabled$' google.golang.org/grpc/internal/transport -race -count=1
--- PASS: TestEval_RecvBufferCompactionDisabled (0.00s)
```

`=false` restores one-entry-per-frame buffering and the default (unset) compacts on all three branches.
Verdict: REFUTED on 6ffaf0fa, 0f57a03a and d67a6723.

## C7

Claim: a qualifying large buffer arriving after small frames is copied instead of keeping its backing array.
Measured with the byte-exact fixture `TestEval_RecvBufferCompactionSkippedLargeBuffer` (small pooled frames,
then a large pooled buffer; compares the backing-array pointer of the queued entry with the original and
drains). Fixture with only `drainRecvBuffer` renamed to `evalDrainRecvBuffer`:

```
$ cd /home/ubuntu/wt/6ffaf0fa && go test -v -run '^TestEval_RecvBufferCompactionSkippedLargeBuffer$' google.golang.org/grpc/internal/transport -race -count=1
--- PASS: TestEval_RecvBufferCompactionSkippedLargeBuffer (0.01s)
ok  	google.golang.org/grpc/internal/transport	1.030s
$ cd /home/ubuntu/wt/0f57a03a && go test -v -run '^TestEval_RecvBufferCompactionSkippedLargeBuffer$' google.golang.org/grpc/internal/transport -race -count=1
--- PASS: TestEval_RecvBufferCompactionSkippedLargeBuffer (0.01s)
ok  	google.golang.org/grpc/internal/transport	1.027s
$ cd /home/ubuntu/wt/d67a6723 && go test -v -run '^TestEval_RecvBufferCompactionSkippedLargeBuffer$' google.golang.org/grpc/internal/transport -race -count=1
--- PASS: TestEval_RecvBufferCompactionSkippedLargeBuffer (0.01s)
ok  	google.golang.org/grpc/internal/transport	1.025s
```

The branches' own large-buffer tests agree (`Test/RecvBuffer_LargeBufferQueuedWithoutCopy` on 6ffaf0fa,
`Test/RecvBuffer_LargeFramesQueuedWithoutCopy` on 0f57a03a — both `PASS` in the C9 runs below).
Verdict: REFUTED on 6ffaf0fa, 0f57a03a and d67a6723.

## C8

Branch 1493d957. Claim: no runnable added test drives many small receive frames and asserts a
fragmentation-related buffering bound. `go.mod` on the branch: `go 1.25.0`; toolchain used: go1.25.7 (the
integer `for i := range numFrames` loops compile). `internal/transport/recv_buffer_test.go`
`TestClientTransport_ManySmallDataFrames` (line 242) drives a real `NewHTTP2Client` transport with
`const numFrames = 20000` one-byte DATA frames written by the raw server in `setupRSTStreamOnEOSTest`, then asserts:

```go
			wantMaxBuffer: maxCompactedBacklogLen(numFrames) + 1, // +1 for io.EOF.   (line 258)
			<-stream.Done()                                                          (line 297)
			n := backlogLen(&stream.buf)                                             (line 299)
			if n > tc.wantMaxBuffer || n < tc.wantMinBuffer {
```

and `TestRecvBuffer_SmallMessagesAreCompacted` (line ~95) asserts
`if got, max := backlogLen(&st.buf), maxCompactedBacklogLen(numMsgs); got > max { t.Fatalf("len(backlog) = %d after %d 1-byte messages, want <= %d", ...) }`.
Demonstration run:

```
$ cd /home/ubuntu/wt/1493d957 && go test -v -run 'Test/RecvBuffer|Test/ClientReceives|Test/ServerReceives|Test/ClientReads|Test/ServerReads|Test/ClientTransport_|Test/StreamReads' ./internal/transport -race -count=1
    --- PASS: Test/ClientTransport_ManySmallDataFrames (0.20s)
        --- PASS: Test/ClientTransport_ManySmallDataFrames/enabled (0.08s)
        --- PASS: Test/ClientTransport_ManySmallDataFrames/disabled (0.12s)
    --- PASS: Test/RecvBuffer_CompactionDisabled (0.00s)
    --- PASS: Test/RecvBuffer_ErrorAfterCompactedData (0.00s)
    --- PASS: Test/RecvBuffer_LargeMessageAfterSmallMessages (0.00s)
    --- PASS: Test/RecvBuffer_SmallMessagesAreCompacted (0.03s)
ok  	google.golang.org/grpc/internal/transport	1.269s
```

A runnable test that drives 20,000 small frames through the transport and asserts a backlog-count bound exists
and passes. Verdict: REFUTED.

## C9

Claim: no added test verifies behaviour across a read/drain boundary (successful loading and consumption of
compacted data, or suffix-tracking decrements). For each branch: the assertion text of an added test that
compacts and then reads/drains the compacted data and compares it to what was written, plus a demonstration run
(`go test -v -run 'Test/RecvBuffer|Test/ClientReceives|Test/ServerReceives|Test/ClientReads|Test/ServerReads|Test/ClientTransport_|Test/StreamReads' ./internal/transport -race -count=1` in each worktree).

### 6ffaf0fa — REFUTED

`internal/transport/recv_buffer_compaction_test.go` `TestRecvBuffer_SmallBufferCompaction` (lines 138–176): after
putting the frames it asserts the backlog/capacity bound, then

```go
			got, err := drainRecvBuffer(t, &b)
			if err != nil {
				t.Fatalf("Reading from recvBuffer returned error %v, want <nil>", err)
			}
			if want := expectedSmallFramesPayload(numFrames); !bytes.Equal(got, want) {
				t.Fatalf("Reading from recvBuffer returned %d bytes that differ from the %d bytes written (first mismatch at %d)", ...)
			}
```

```
    --- PASS: Test/RecvBuffer_SmallBufferCompaction/compaction_enabled (0.04s)
    --- PASS: Test/RecvBuffer_CompactionAppendsInPlace (0.03s)
    --- PASS: Test/ClientReadsManySmallDataFrames/compaction_enabled (0.02s)
ok  	google.golang.org/grpc/internal/transport	1.417s
```

### 1493d957 — REFUTED

`internal/transport/recv_buffer_test.go` `TestRecvBuffer_SmallMessagesAreCompacted` (lines 100–121):

```go
	if got, max := backlogLen(&st.buf), maxCompactedBacklogLen(numMsgs); got > max { t.Fatalf(...) }
	got := make([]byte, numMsgs)
	if _, err := st.readTo(got); err != nil { t.Fatalf("readTo() failed: %v", err) }
	if !bytes.Equal(got, want) { t.Fatal("Data read from stream does not match data written") }
	if _, err := st.readTo(make([]byte, 1)); err != io.EOF { t.Fatalf("readTo() after reading all data returned error %v, want %v", err, io.EOF) }
```

`TestClientTransport_ManySmallDataFrames` (lines 297–312) does the same through a real client stream
(`stream.readTo(got)` then `io.EOF`).

```
    --- PASS: Test/RecvBuffer_SmallMessagesAreCompacted (0.03s)
    --- PASS: Test/ClientTransport_ManySmallDataFrames/enabled (0.08s)
ok  	google.golang.org/grpc/internal/transport	1.269s
```

### 0f57a03a — REFUTED

`internal/transport/recv_buffer_test.go` `TestRecvBuffer_CompactsSmallFrames` (lines 115–146): asserts
`len(backlog) = %d after %d small frames, want %d` and the merged buffer's length/capacity, then

```go
	got, err := drainRecvBuffer(t, &b)
	if err != io.EOF { t.Fatalf("drainRecvBuffer() returned error %v, want %v", err, io.EOF) }
	if !bytes.Equal(got, payload) { t.Fatalf("drainRecvBuffer() returned %d bytes that differ from the %d bytes put", len(got), len(payload)) }
```

```
    --- PASS: Test/RecvBuffer_CompactsSmallFrames (0.00s)
    --- PASS: Test/RecvBuffer_CompactionLimit (0.02s)
    --- PASS: Test/StreamReadsMessageFromSmallFrames (0.00s)
ok  	google.golang.org/grpc/internal/transport	1.111s
```

### 60f32c9f — REFUTED

`internal/transport/recv_buffer_compaction_test.go` helper `readAllInPieces` (lines 114–132), used by
`TestRecvBufferCompaction_ManySmallMessages`, reads the compacted stream with interleaved read sizes
`{1, 3, 7, 100, 4095, 4096, 4097, 10000}` and asserts:

```go
		if _, err := s.readTo(p); err != nil { t.Fatalf("s.readTo(len=%d) after %d bytes failed: %v", n, len(got), err) }
	...
	if !bytes.Equal(got, want) { t.Fatalf("Read %d bytes from stream, want %d bytes; first mismatch at %d", ...) }
	if _, err := s.readTo(make([]byte, 1)); err != io.EOF { t.Fatalf("s.readTo() after all data = %v, want io.EOF", err) }
```

`TestRecvBufferCompaction_ErrorAfterCoalescedData` (lines 351–359) additionally checks the error is delivered
after the coalesced data and repeats on subsequent reads.

```
    --- PASS: Test/RecvBufferCompaction_ManySmallMessages/enabled (0.01s)
    --- PASS: Test/RecvBufferCompaction_ErrorAfterCoalescedData (0.00s)
    --- PASS: Test/ServerReceivesManySmallDataFrames/enabled (0.08s)
ok  	google.golang.org/grpc/internal/transport	1.429s
```

## C10

Branch 1493d957. Claim: the stream receive in `TestClientTransport_ManySmallDataFrames` has no effective local
bound. The receive is `<-stream.Done()` (`recv_buffer_test.go:297`) after `setupRSTStreamOnEOSTest(ctx, t, serverFrames)`
with `ctx, cancel := context.WithTimeout(context.Background(), defaultTestTimeout)` (line 290). Stall: the raw
server never sets END_STREAM on the last DATA frame, so `stream.Done()` is never closed by the data path.

```
$ python3 verify/repro/c1/run_variant.py /home/ubuntu/wt/1493d957 internal/transport/recv_buffer_test.go 1493d957_stall_noEndStream 'Test/ClientTransport_ManySmallDataFrames/enabled' 90s '[["framer.WriteData(streamID, i == numFrames-1, want[i:i+1]); err != nil {", "framer.WriteData(streamID, false, want[i:i+1]); err != nil { // injected stall: END_STREAM never sent"]]'
# exit=1 wall=12.0s
    recv_buffer_test.go:312: stream.readTo() after reading all data returned error connection error: desc = "error reading from server: read tcp 127.0.0.1:53890->127.0.0.1:33455: use of closed network connection", want EOF
    transport_test.go:3751: Test timed out when waiting for server to be done
    transport_test.go:3717: Test timed out when waiting for a RST_STREAM frame from client
    transport_test.go:3722: Test timed out when waiting for server to send frames
    transport_test.go:3693: Server reader goroutine failed to read frame: EOF
panic: Fail in goroutine after Test/ClientTransport_ManySmallDataFrames/enabled has completed
FAIL	google.golang.org/grpc/internal/transport	10.058s
```

The receive was released at the 10 s `defaultTestTimeout`: the helper's server goroutine selects on
`ctx.Done()` (`transport_test.go:3717/3722`), closes the server-side connection, the client transport sees
`use of closed network connection` and closes the stream, and `<-stream.Done()` returns; the test then fails
with the diagnostic on line 312 and the deferred `waitForServerDone` (`transport_test.go:3751`) also selects on
`ctx.Done()`. Bounded indirectly through server-connection cleanup. (The trailing `Fail in goroutine after ...
has completed` panic comes from the helper's un-joined reader goroutine reporting after `t.Fatalf` — a
different hygiene issue than the one claimed.) Verdict: REFUTED.

## C11

Primary branch. Claim, two parts: (a) `compactBacklogLocked` copies an efficiently stored 16 KiB buffer when it
consolidates a suffix containing it; (b) on 64-bit, 1,024 queued one-byte buffers followed by a 16 KiB buffer
selects suffix consolidation. Direct probe `verify/repro/c11/c11_probe_test.go` (package `transport`, build tag
`c11probe`): `init(pool)`, one `put` to occupy the channel, N one-byte `put`s, then `put` of a fully-used 16 KiB
pooled `mem.Buffer` (len = cap = 16384, `IsBelowBufferPoolingThreshold` = false, i.e. "efficiently stored"),
comparing `&ReadOnlyData()[0]` before and after.

```
$ bash verify/repro/c11/run.sh
=== RUN   TestC11Probe
recvMsgSize=56 compactionThreshold=58368 utilizationFactor=2
n=1023: before large put: len(backlog)=1023 uncompactedSuffixLen=1023 uncompactedBytes=1023 heapEstimate=58311
n=1023: large buffer len=16384 cap=16384 belowPoolingThreshold=false
n=1023: after large put: len(backlog)=1 lastEntryLen=17407 lastEntrySharesBackingArray=false uncompactedSuffixLen=0 uncompactedBytes=0
n=1024: before large put: len(backlog)=1024 uncompactedSuffixLen=1024 uncompactedBytes=1024 heapEstimate=58368
n=1024: large buffer len=16384 cap=16384 belowPoolingThreshold=false
n=1024: after large put: len(backlog)=1 lastEntryLen=17408 lastEntrySharesBackingArray=false uncompactedSuffixLen=0 uncompactedBytes=0
    c11_probe_test.go:54: n=1024: the 16 KiB buffer was copied during compaction (len(backlog)=1)
--- FAIL: TestC11Probe (0.00s)
FAIL	google.golang.org/grpc/internal/transport	0.004s
exit=1
```

Part (b) trigger — CONFIRMED: with 1,024 one-byte entries `uncompactedSuffixLen*recvMsgSize + uncompactedBytes`
= 1024·56 + 1024 = 58,368 = `compactionThreshold`, and the put of the 16 KiB buffer collapses the backlog from
1,024 entries to 1 (suffix consolidation ran; it also runs at 1,023 because the large buffer's own bytes are
added before the check). Part (a) copy — CONFIRMED: the single surviving entry is 17,408 bytes long and
`lastEntrySharesBackingArray=false`, i.e. the 16 KiB payload was copied into a new pooled buffer
(`internal/transport/transport.go` `compactBacklogLocked`: `newBuf := b.bufPool.Get(b.uncompactedBytes)` then
`copy((*newBuf)[start:], m.buffer.ReadOnlyData()); m.buffer.Free()` for every entry of the suffix). Contrast:
the fixture `TestEval_RecvBufferCompactionSkippedLargeBuffer` passes on this branch because it does not reach
the threshold before the large put, so the large buffer is only spared when no consolidation is pending.

Impact reasoning: the task's success criterion says a large frame whose payload is already stored efficiently
must be queued as received, *including one that arrives after a run of small frames*. Here the ordinary
"slow reader, chatty peer, then a normal 16 KiB frame" sequence copies 16 KiB (memmove + a second pooled
allocation, transiently ~34 KiB live) once per compaction event; the original 16 KiB pooled buffer is freed
and a fresh 17,408-byte one allocated. Reads remain correct (fixture and unit tests pass), so nothing fails
loudly — it is a silent violation of the no-copy requirement, proportional to the number of large frames that
land on a fragmented suffix.

## C12

Branch 60f32c9f. `internal/transport/recv_buffer_compaction_test.go:734` inside `BenchmarkRecvBufferManySmallMessages`:

```go
				s := newTestRecvStream(context.Background(), mem.DefaultBufferPool())
```

`scripts/vet.sh` test-context rule (its only exception is `benchmark/primitives/context_test.go`, plus lines
that contain `context.WithTimeout(` / `context.WithCancel(`):

```sh
git grep -e 'context.Background()' --or -e 'context.TODO()' -- "*_test.go" |
  grep -v "benchmark/primitives/context_test.go" |
  grep -v 'context.WithTimeout(' |
  grep -v 'context.WithCancel(' |
  not grep -v 'context.WithCancel('
```

Executed on the branch (replay: `bash verify/repro/c12/run.sh` from the branch checkout):

```
$ cd /home/ubuntu/wt/60f32c9f && bash /home/ubuntu/repos/grpc-go/verify/repro/c12/run.sh
internal/transport/recv_buffer_compaction_test.go:				s := newTestRecvStream(context.Background(), mem.DefaultBufferPool())
exit=1
```

Same rule against the base commit `c92e9857` (`git grep` given the base tree so no checkout is needed):

```
$ cd ~/repos/grpc-go && git grep -e "context.Background()" --or -e "context.TODO()" c92e985770b7194d4a4f433c84d42c6c195e8ce5 -- "*_test.go" | grep -v "benchmark/primitives/context_test.go" | grep -v "context.WithTimeout(" | grep -v "context.WithCancel(" | not grep -v "context.WithCancel("
exit=0
```

The rule rejects exactly the new benchmark line and nothing else; on base it passes. Verdict: CONFIRMED.

Impact reasoning: `scripts/vet.sh` is the repository's static-check gate (`vet.sh` runs in CI for every PR);
this branch fails it, so the change cannot land as-is. Fix: create the context with
`context.WithTimeout(context.Background(), defaultTestTimeout)` (plus `defer cancel()`) inside the benchmark.
