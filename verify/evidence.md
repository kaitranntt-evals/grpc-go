# Evidence for audit run v-7fc530bf

Observations only. Every command was run in this session; output is verbatim (long lines kept). Worktrees were detached checkouts of the claim-target branches; all instrumentation lives in `verify/repro/` and was copied into a worktree only for the run, then removed. No production file on any branch was committed in a modified state.

Common setup used by every section:

```sh
git remote add claims https://github.com/kaitranntt-evals/grpc-go-transport-restrict-memory-overhead   # claim-target branches are not on origin (this session reached the same repository through its git proxy URL)
git fetch claims 'refs/heads/evalon/*:refs/remotes/claims/evalon/*'
for h in 1e8d9a91 8c775707 786c4c95 0973d83c e11b3799 bf9e3569 73d2e0f0 87b780a7; do git worktree add --detach ~/wt/$h claims/evalon/grpc-go-tr-$h; done
go version   # go version go1.25.7 linux/amd64 in this session
```

Branch tips audited:

- [evalon/grpc-go-tr-1e8d9a91](https://github.com/kaitranntt-evals/grpc-go-transport-restrict-memory-overhead/tree/evalon/grpc-go-tr-1e8d9a91) at `88262511898ec53c1fdb2a1fedeff8a770deb167`
- [evalon/grpc-go-tr-8c775707](https://github.com/kaitranntt-evals/grpc-go-transport-restrict-memory-overhead/tree/evalon/grpc-go-tr-8c775707) at `8a5027e87a4d7857747a029eed1b5f8470070eee`
- [evalon/grpc-go-tr-786c4c95](https://github.com/kaitranntt-evals/grpc-go-transport-restrict-memory-overhead/tree/evalon/grpc-go-tr-786c4c95) at `c46d7d73dc388d863f630d1ce5507fd3c7c064b9`
- [evalon/grpc-go-tr-0973d83c](https://github.com/kaitranntt-evals/grpc-go-transport-restrict-memory-overhead/tree/evalon/grpc-go-tr-0973d83c) at `82a769205b7680e06eda6fb9724b266e8a9fb5ff`
- [evalon/grpc-go-tr-e11b3799](https://github.com/kaitranntt-evals/grpc-go-transport-restrict-memory-overhead/tree/evalon/grpc-go-tr-e11b3799) at `ab88b88b9ae643169385956f74938d0f1419789c`
- [evalon/grpc-go-tr-bf9e3569](https://github.com/kaitranntt-evals/grpc-go-transport-restrict-memory-overhead/tree/evalon/grpc-go-tr-bf9e3569) at `0aaa488f74b246dcf8d370d73edfd1bd9c685765`
- [evalon/grpc-go-tr-73d2e0f0](https://github.com/kaitranntt-evals/grpc-go-transport-restrict-memory-overhead/tree/evalon/grpc-go-tr-73d2e0f0) at `b7aa70dba3b05c204c3ab25cdc255f109f9ef2b6`
- [evalon/grpc-go-tr-87b780a7](https://github.com/kaitranntt-evals/grpc-go-transport-restrict-memory-overhead/tree/evalon/grpc-go-tr-87b780a7) at `36f0b2bdfa178e9eb33b67677c10dc0fdc711c1c`

## C1

Claim: the added/modified tests exercise at least one potentially blocking authored operation with no timeout, deadline, or independent timed cancellation that interrupts it if stalled (five branches, adjudicated independently).

Method, identical on every branch: (1) list the authored blocking operation and every deadline/timeout/close near it; (2) run the authored test unmodified as a control; (3) *stall* the operation and watch what, if anything, interrupts it. Stalls are produced either by a probe that calls the authored helper directly, or by a fault injected under the authored test (the test itself is never edited). A watchdog (`c1_stall_watch_test.go.txt`) only prints goroutines parked in the named frames. The package-wide `defaultTestTimeout` is 10s, so every probe waits 12s.

Replay (one command per branch; the script restores the worktree on exit):

```sh
verify/repro/c1_blocking_ops.sh 1e8d9a91 ~/wt/1e8d9a91
verify/repro/c1_blocking_ops.sh 8c775707 ~/wt/8c775707
verify/repro/c1_blocking_ops.sh 786c4c95 ~/wt/786c4c95
verify/repro/c1_blocking_ops.sh 0973d83c ~/wt/0973d83c
verify/repro/c1_blocking_ops.sh e11b3799 ~/wt/e11b3799
```

### 1e8d9a91

Branch [evalon/grpc-go-tr-1e8d9a91](https://github.com/kaitranntt-evals/grpc-go-transport-restrict-memory-overhead/tree/evalon/grpc-go-tr-1e8d9a91). Suspected: an added helper leaves `ctxDone` nil and its callers read from the stream with no other local bound.

Observed: `newTestRecvStream()` builds `recvBufferReader{recv: &s.buf}` — `ctx` and `ctxDone` are nil (printed by the probe). `recvBufferReader.read` selects on `r.ctxDone` and the buffer channel, so a nil `ctxDone` can never fire. Three added tests read through this helper (`TestRecvBuffer_DataDeliveredInOrder` and `TestRecvBuffer_TinyPayloadsMemoryOverhead` via `readAllFromStream`, `TestRecvBuffer_ErrorAfterCompactedData` via `st.read`) and none creates a context or timer; the only `WithTimeout` in the file (line 242) belongs to the fourth test, which reads from a real client stream. Probe: a stalled `s.read(2)` on the helper's stream was still blocked after 12s and ended only when the probe supplied the missing byte. Fault injection: when `recvBuffer.put` loses the terminal error (the kind of regression these tests exist to catch), the unmodified authored test does not fail — it parks in `readAllFromStream` → `s.read(1)` (recv_buffer_test.go:77) until the *process-level* `go test -timeout` kills the binary with a panic (30s here because `-timeout 30s` was passed; the default is 10 minutes).

```console
$ verify/repro/c1_blocking_ops.sh 1e8d9a91 ~/wt/1e8d9a91
### branch tip: 88262511898ec53c1fdb2a1fedeff8a770deb167 transport: compact tiny DATA payloads buffered on slow-reading streams
## authored helper
58:func newTestRecvStream() *Stream {
59-	s := &Stream{readRequester: &fakeReadRequester{}}
60-	s.buf.init()
61-	s.trReader = transportReader{
62-		reader:        recvBufferReader{recv: &s.buf},
63-		windowHandler: &mockWindowUpdater{f: func(int) {}},
64-	}
65-	return s
66-}
## callers of the helper / of readAllFromStream
58:func newTestRecvStream() *Stream {
69:func readAllFromStream(t *testing.T, s *Stream, n int) []byte {
91:			st := newTestRecvStream()
121:			if got := readAllFromStream(t, st, len(want)); !bytes.Equal(got, want) {
133:	st := newTestRecvStream()
140:	data, err := st.read(3)
142:		t.Fatalf("st.read(3) failed: %v", err)
145:		t.Fatalf("st.read(3) = %q, want %q", got, "abc")
148:	if _, err := st.read(1); err != wantErr {
149:		t.Fatalf("st.read(1) returned error %v, want %v", err, wantErr)
161:		st := newTestRecvStream()
171:		got := readAllFromStream(t, st, numPayloads)
242:		ctx, cancel := context.WithTimeout(context.Background(), defaultTestTimeout)
261:		if got := readAllFromStream(t, &stream.Stream, numFrames); !bytes.Equal(got, want) {
## probe: stall the read on the authored helper's stream
+ go test -count=1 -timeout 60s -v -run ^TestVerifyC1_ ./internal/transport
=== RUN   TestVerifyC1_StalledReadOnNewTestRecvStream
    verify_c1_probe_test.go:17: newTestRecvStream(): reader.ctx == nil: true, reader.ctxDone == nil: true, defaultTestTimeout=10s
    verify_c1_probe_test.go:41: RESULT stalled s.read(2) still blocked after 12s; nothing local interrupted it
    verify_c1_probe_test.go:46: RESULT released only by supplying the missing byte: n=2 err=<nil>
--- PASS: TestVerifyC1_StalledReadOnNewTestRecvStream (12.06s)
PASS
ok  	google.golang.org/grpc/internal/transport	12.063s
real	0m12.401s
## fault injection: recvBuffer.put loses the terminal error and everything after it (end-of-stream never delivered), authored test run unmodified
diff --git a/internal/transport/transport.go b/internal/transport/transport.go
index c8207cd5..05b7126a 100644
--- a/internal/transport/transport.go
+++ b/internal/transport/transport.go
@@ -22,6 +22,7 @@
 package transport
 
 import (
+	"os"
 	"context"
 	"errors"
 	"fmt"
@@ -93,11 +94,21 @@ const (
 // init allows a recvBuffer to be initialized in-place, which is useful
 // for resetting a buffer or for avoiding a heap allocation when the buffer
 // is embedded in another struct.
+var verifyC1Lost bool
+
 func (b *recvBuffer) init() {
 	b.c = make(chan recvMsg, 1)
 }
 
 func (b *recvBuffer) put(r recvMsg) {
+	if os.Getenv("VERIFY_C1_DROP_ERR") != "" {
+		if r.err != nil {
+			verifyC1Lost = true
+		}
+		if verifyC1Lost {
+			return
+		}
+	}
 	b.mu.Lock()
 	if b.err != nil {
 		// drop the buffer on the floor. Since b.err is not nil, any subsequent reads
+ go test -count=1 -timeout 60s -run ^Test$/^RecvBuffer_DataDeliveredInOrder$ ./internal/transport
ok  	google.golang.org/grpc/internal/transport	0.024s
real	0m0.366s
(next command's output is filtered to the test progress lines, the timeout panic and the authored frames of the blocked goroutine)
+ VERIFY_C1_DROP_ERR=1 go test -count=1 -timeout 30s -v -run ^Test$/^RecvBuffer_DataDeliveredInOrder$ ./internal/transport
=== RUN   Test
=== RUN   Test/RecvBuffer_DataDeliveredInOrder
=== RUN   Test/RecvBuffer_DataDeliveredInOrder/compaction=true
panic: test timed out after 30s
	running tests:
	/home/ubuntu/wt/1e8d9a91/internal/transport/recv_buffer_test.go:88 +0xb1
google.golang.org/grpc/internal/transport.(*recvBufferReader).read(0xc0000fa660, 0x1)
	/home/ubuntu/wt/1e8d9a91/internal/transport/recv_buffer_test.go:77 +0x189
	/home/ubuntu/wt/1e8d9a91/internal/transport/recv_buffer_test.go:121 +0x53f
FAIL	google.golang.org/grpc/internal/transport	30.106s
FAIL
real	0m30.431s
```

### 8c775707

Branch [evalon/grpc-go-tr-8c775707](https://github.com/kaitranntt-evals/grpc-go-transport-restrict-memory-overhead/tree/evalon/grpc-go-tr-8c775707). Suspected: an authored test performs a blocking `Accept` with no effective local bound and relies on indirect cleanup to release it.

Observed: `tinyDataFramesServer` calls `lis.Accept()` (line 126) in a goroutine with no deadline on the listener. The helper does start `go func(){ <-ctx.Done(); conn.Close() }()` but only *after* `Accept` returns, so it covers the accepted connection, not the `Accept`. The only thing that can release a stalled `Accept` is `defer lis.Close()` in the caller `measureTinyDataFramesRetainedHeap` (line 212). Fault injection (client never connects): `Accept` was still parked at 3s, 6s and 9s; it was released only after the caller's `NewHTTP2Client(ctx, …)` failed on its own 10s context and the deferred `lis.Close()` ran, surfacing as `Server failed to accept connection: … use of closed network connection`. So nothing bounds the `Accept` itself; in the stall that was exercised the release arrived at ~10s because every call on the test's main goroutine is context-bound.

```console
$ verify/repro/c1_blocking_ops.sh 8c775707 ~/wt/8c775707
### branch tip: 8a5027e87a4d7857747a029eed1b5f8470070eee transport: compact small buffered DATA payloads on slowly read streams
## authored blocking socket operations and every deadline/timeout/close near them in internal/transport/recv_buffer_compaction_test.go
64:			ctx, cancel := context.WithTimeout(context.Background(), defaultTestTimeout)
126:		conn, err := lis.Accept()
133:			conn.Close()
135:		if _, err := io.ReadFull(conn, make([]byte, len(clientPreface))); err != nil {
146:			frame, err := framer.ReadFrame()
168:				frame, err := framer.ReadFrame()
205:	ctx, cancel := context.WithTimeout(context.Background(), defaultTestTimeout)
212:	defer lis.Close()
## control: authored test, unmodified tree
+ go test -count=1 -timeout 120s -run ^Test$/^ClientTransport_TinyDataFramesReceiveMemory$ ./internal/transport
ok  	google.golang.org/grpc/internal/transport	0.159s
real	0m1.968s
## fault injection: client never connects, authored test run unmodified, watchdog reports goroutines parked in Accept
 internal/transport/http2_client.go | 3 +++
 1 file changed, 3 insertions(+)
+ VERIFY_C1_STALL_DIAL=1 VERIFY_C1_WATCH=\.Accept\( go test -count=1 -timeout 120s -v -run ^Test$/^ClientTransport_TinyDataFramesReceiveMemory$ ./internal/transport
=== RUN   Test
=== RUN   Test/ClientTransport_TinyDataFramesReceiveMemory
WATCH t= 3.0s still blocked: goroutine 25 [IO wait]: | internal/poll.(*FD).Accept(0xc00034c180) | net.(*TCPListener).Accept(0xc0003520c0) | /home/ubuntu/wt/8c775707/internal/transport/recv_buffer_compaction_test.go:126 +0x7b | /home/ubuntu/wt/8c775707/internal/transport/recv_buffer_compaction_test.go:125 +0x14b
WATCH t= 6.0s still blocked: goroutine 25 [IO wait]: | internal/poll.(*FD).Accept(0xc00034c180) | net.(*TCPListener).Accept(0xc0003520c0) | /home/ubuntu/wt/8c775707/internal/transport/recv_buffer_compaction_test.go:126 +0x7b | /home/ubuntu/wt/8c775707/internal/transport/recv_buffer_compaction_test.go:125 +0x14b
WATCH t= 9.0s still blocked: goroutine 25 [IO wait]: | internal/poll.(*FD).Accept(0xc00034c180) | net.(*TCPListener).Accept(0xc0003520c0) | /home/ubuntu/wt/8c775707/internal/transport/recv_buffer_compaction_test.go:126 +0x7b | /home/ubuntu/wt/8c775707/internal/transport/recv_buffer_compaction_test.go:125 +0x14b
    recv_buffer_compaction_test.go:218: NewHTTP2Client failed: connection error: desc = "transport: Error while dialing: context deadline exceeded"
    recv_buffer_compaction_test.go:128: Server failed to accept connection: accept tcp 127.0.0.1:38783: use of closed network connection
WATCH t=12.0s no goroutine matches "\\.Accept\\(" any more; watchdog exits
--- FAIL: Test (12.04s)
    --- FAIL: Test/ClientTransport_TinyDataFramesReceiveMemory (12.04s)
=== RUN   Test
--- PASS: Test (0.00s)
FAIL
FAIL	google.golang.org/grpc/internal/transport	12.046s
FAIL
real	0m13.523s
```

### 786c4c95

Branch [evalon/grpc-go-tr-786c4c95](https://github.com/kaitranntt-evals/grpc-go-transport-restrict-memory-overhead/tree/evalon/grpc-go-tr-786c4c95). Suspected: an authored test performs a potentially blocking socket operation without a timeout or I/O deadline established before it begins.

Observed: `TestClientStream_ManyTinyDataFrames` runs an inline raw server goroutine: `lis.Accept()` (line 4344), `io.ReadFull(sconn, …)` (4350), `sfr.ReadFrame()` (4361) and the `WriteData` loop. No `SetDeadline`/`SetReadDeadline`/`SetWriteDeadline` exists anywhere in the added code and, unlike 8c775707, no goroutine watches `ctx` to close the listener or the connection; release comes only from `defer lis.Close()` (4339), `defer sconn.Close()` and the client closing its end. Fault injection (client never connects): in each of the two subtests the `Accept` was parked at every watchdog tick (3/6/9s and 12/15/18s) and was released only when the subtest's `NewHTTP2Client(ctx, …)` timed out at 10s and the deferred `lis.Close()` ran. Nothing bounds the socket operations themselves; in the stall exercised the release arrived at ~10s per subtest via the main goroutine's context.

```console
$ verify/repro/c1_blocking_ops.sh 786c4c95 ~/wt/786c4c95
### branch tip: c46d7d73dc388d863f630d1ce5507fd3c7c064b9 transport: compact small queued DATA payloads to bound receive memory
## authored blocking socket operations and every deadline/timeout/close near them in internal/transport/transport_test.go
4248:	ctx, cancel := context.WithTimeout(context.Background(), defaultTestTimeout)
4332:			ctx, cancel := context.WithTimeout(context.Background(), defaultTestTimeout)
4339:			defer lis.Close()
4344:				sconn, err := lis.Accept()
4346:					serverErr <- fmt.Errorf("lis.Accept() failed: %v", err)
4349:				defer sconn.Close()
4350:				if _, err := io.ReadFull(sconn, make([]byte, len(clientPreface))); err != nil {
4361:					frame, err := sfr.ReadFrame()
## control: authored test, unmodified tree
+ go test -count=1 -timeout 120s -run ^Test$/^ClientStream_ManyTinyDataFrames$ ./internal/transport
ok  	google.golang.org/grpc/internal/transport	0.044s
real	0m1.921s
## fault injection: client never connects, authored test run unmodified, watchdog reports goroutines parked in Accept
 internal/transport/http2_client.go | 3 +++
 1 file changed, 3 insertions(+)
+ VERIFY_C1_STALL_DIAL=1 VERIFY_C1_WATCH=\.Accept\( go test -count=1 -timeout 120s -v -run ^Test$/^ClientStream_ManyTinyDataFrames$ ./internal/transport
=== RUN   Test
=== RUN   Test/ClientStream_ManyTinyDataFrames
=== RUN   Test/ClientStream_ManyTinyDataFrames/compaction=false
WATCH t= 3.0s still blocked: goroutine 13 [IO wait]: | internal/poll.(*FD).Accept(0xc0002b2080) | net.(*TCPListener).Accept(0xc0002ae0c0) | /home/ubuntu/wt/786c4c95/internal/transport/transport_test.go:4344 +0x52 | /home/ubuntu/wt/786c4c95/internal/transport/transport_test.go:4343 +0x230
WATCH t= 6.0s still blocked: goroutine 13 [IO wait]: | internal/poll.(*FD).Accept(0xc0002b2080) | net.(*TCPListener).Accept(0xc0002ae0c0) | /home/ubuntu/wt/786c4c95/internal/transport/transport_test.go:4344 +0x52 | /home/ubuntu/wt/786c4c95/internal/transport/transport_test.go:4343 +0x230
WATCH t= 9.0s still blocked: goroutine 13 [IO wait]: | internal/poll.(*FD).Accept(0xc0002b2080) | net.(*TCPListener).Accept(0xc0002ae0c0) | /home/ubuntu/wt/786c4c95/internal/transport/transport_test.go:4344 +0x52 | /home/ubuntu/wt/786c4c95/internal/transport/transport_test.go:4343 +0x230
    transport_test.go:4404: NewHTTP2Client() failed: connection error: desc = "transport: Error while dialing: context deadline exceeded"
=== RUN   Test/ClientStream_ManyTinyDataFrames/compaction=true
WATCH t=12.0s still blocked: goroutine 37 [IO wait]: | internal/poll.(*FD).Accept(0xc0002b2200) | net.(*TCPListener).Accept(0xc0000504c0) | /home/ubuntu/wt/786c4c95/internal/transport/transport_test.go:4344 +0x52 | /home/ubuntu/wt/786c4c95/internal/transport/transport_test.go:4343 +0x230
WATCH t=15.1s still blocked: goroutine 37 [IO wait]: | internal/poll.(*FD).Accept(0xc0002b2200) | net.(*TCPListener).Accept(0xc0000504c0) | /home/ubuntu/wt/786c4c95/internal/transport/transport_test.go:4344 +0x52 | /home/ubuntu/wt/786c4c95/internal/transport/transport_test.go:4343 +0x230
WATCH t=18.1s still blocked: goroutine 37 [IO wait]: | internal/poll.(*FD).Accept(0xc0002b2200) | net.(*TCPListener).Accept(0xc0000504c0) | /home/ubuntu/wt/786c4c95/internal/transport/transport_test.go:4344 +0x52 | /home/ubuntu/wt/786c4c95/internal/transport/transport_test.go:4343 +0x230
    transport_test.go:4404: NewHTTP2Client() failed: connection error: desc = "transport: Error while dialing: context deadline exceeded"
WATCH t=21.1s no goroutine matches "\\.Accept\\(" any more; watchdog exits
--- FAIL: Test (21.08s)
    --- FAIL: Test/ClientStream_ManyTinyDataFrames (21.08s)
        --- FAIL: Test/ClientStream_ManyTinyDataFrames/compaction=false (10.00s)
        --- FAIL: Test/ClientStream_ManyTinyDataFrames/compaction=true (10.01s)
=== RUN   Test
--- PASS: Test (0.00s)
FAIL
FAIL	google.golang.org/grpc/internal/transport	21.086s
FAIL
real	0m22.539s
```

### 0973d83c

Branch [evalon/grpc-go-tr-0973d83c](https://github.com/kaitranntt-evals/grpc-go-transport-restrict-memory-overhead/tree/evalon/grpc-go-tr-0973d83c). Suspected: an authored test relies on deferred socket cleanup instead of a local timeout or deadline that interrupts its potentially blocking socket operation.

Observed: `startTinyDataFrameServer` calls `lis.Accept()` (line 256), `io.ReadFull` (262) and `sfr.ReadFrame()` (321) with no deadline; the caller registers `defer lis.Close()` (370) *before* it creates the 10s context (373), and the context is never wired to the listener or connection. Fault injection (client never connects): `Accept` parked at 3s, 6s, 9s; released only after `NewHTTP2Client(ctx, …)` failed at 10s and the deferred `lis.Close()` ran, surfacing as `Error while accepting: … use of closed network connection`. This is exactly the described reliance on deferred cleanup; in the stall exercised, that cleanup arrived at ~10s because the main goroutine is context-bound.

```console
$ verify/repro/c1_blocking_ops.sh 0973d83c ~/wt/0973d83c
### branch tip: 82a769205b7680e06eda6fb9724b266e8a9fb5ff chore: apply eval changes
## authored blocking socket operations and every deadline/timeout/close near them in internal/transport/recv_buffer_compaction_test.go
154:	ctx, cancel := context.WithTimeout(context.Background(), defaultTestTimeout)
218:	ctx, cancel := context.WithTimeout(context.Background(), defaultTestTimeout)
256:		sconn, err := lis.Accept()
261:		defer sconn.Close()
262:		if _, err := io.ReadFull(sconn, make([]byte, len(clientPreface))); err != nil {
321:			frame, err := sfr.ReadFrame()
370:		defer lis.Close()
373:		ctx, cancel := context.WithTimeout(context.Background(), defaultTestTimeout)
## control: authored test, unmodified tree
+ go test -count=1 -timeout 120s -run ^Test$/^ClientRecvBufferCompaction_TinyDataFrames$ ./internal/transport
ok  	google.golang.org/grpc/internal/transport	0.080s
real	0m1.552s
## fault injection: client never connects, authored test run unmodified, watchdog reports goroutines parked in Accept
 internal/transport/http2_client.go | 3 +++
 1 file changed, 3 insertions(+)
+ VERIFY_C1_STALL_DIAL=1 VERIFY_C1_WATCH=\.Accept\( go test -count=1 -timeout 120s -v -run ^Test$/^ClientRecvBufferCompaction_TinyDataFrames$ ./internal/transport
=== RUN   Test
=== RUN   Test/ClientRecvBufferCompaction_TinyDataFrames
WATCH t= 3.0s still blocked: goroutine 25 [IO wait]: | internal/poll.(*FD).Accept(0xc00021da00) | net.(*TCPListener).Accept(0xc0001ade80) | /home/ubuntu/wt/0973d83c/internal/transport/recv_buffer_compaction_test.go:256 +0x49 | /home/ubuntu/wt/0973d83c/internal/transport/recv_buffer_compaction_test.go:255 +0x8e
WATCH t= 6.0s still blocked: goroutine 25 [IO wait]: | internal/poll.(*FD).Accept(0xc00021da00) | net.(*TCPListener).Accept(0xc0001ade80) | /home/ubuntu/wt/0973d83c/internal/transport/recv_buffer_compaction_test.go:256 +0x49 | /home/ubuntu/wt/0973d83c/internal/transport/recv_buffer_compaction_test.go:255 +0x8e
WATCH t= 9.0s still blocked: goroutine 25 [IO wait]: | internal/poll.(*FD).Accept(0xc00021da00) | net.(*TCPListener).Accept(0xc0001ade80) | /home/ubuntu/wt/0973d83c/internal/transport/recv_buffer_compaction_test.go:256 +0x49 | /home/ubuntu/wt/0973d83c/internal/transport/recv_buffer_compaction_test.go:255 +0x8e
    recv_buffer_compaction_test.go:381: Error while creating client transport: connection error: desc = "transport: Error while dialing: context deadline exceeded"
    recv_buffer_compaction_test.go:258: Error while accepting: accept tcp 127.0.0.1:33873: use of closed network connection
WATCH t=12.0s no goroutine matches "\\.Accept\\(" any more; watchdog exits
--- FAIL: Test (12.04s)
    --- FAIL: Test/ClientRecvBufferCompaction_TinyDataFrames (12.04s)
=== RUN   Test
--- PASS: Test (0.00s)
FAIL
FAIL	google.golang.org/grpc/internal/transport	12.046s
FAIL
real	0m13.266s
```

### e11b3799

Branch [evalon/grpc-go-tr-e11b3799](https://github.com/kaitranntt-evals/grpc-go-transport-restrict-memory-overhead/tree/evalon/grpc-go-tr-e11b3799). Suspected: an added fixture performs a goroutine join during cleanup with no timeout, deadline, or independent timed cancellation.

Observed: the added fixture `tinyFrameServer.stop()` is `ts.lis.Close(); <-ts.done` (lines 4499-4502); `done` is closed only when `serve` returns, and `serve` blocks in `Accept`, `io.ReadFull` and `ReadFrame` with no deadline. `tinyFrameServer` is new in this branch (it is not the pre-existing `server.stop()` helper that the claim excludes) and is joined via `defer server.stop()` in `measureTinyFrameReceiveMemory` (4521). Probe: with one silent peer connected, `stop()` was still blocked after 12s although the listener was already closed (a fresh dial was refused); it returned only when the probe closed the peer connection. Fault injection (client never connects): the authored test's `Accept` parked for 10s and was then released by the same `stop()` closing the listener, so in that particular stall the join completed. The join is therefore unbounded by construction and hangs whenever `serve` is parked on an accepted connection that stays open; in the authored flow the deferred `ct.Close` normally closes that connection first.

```console
$ verify/repro/c1_blocking_ops.sh e11b3799 ~/wt/e11b3799
### branch tip: ab88b88b9ae643169385956f74938d0f1419789c chore: apply eval changes
## authored fixture (all added in this branch: 6 added lines mention tinyFrameServer)
4499:func (ts *tinyFrameServer) stop() {
4500-	ts.lis.Close()
4501-	<-ts.done
4502-}
4503-
4415:func newTinyFrameServer(t *testing.T, numFrames int) *tinyFrameServer {
4416-	t.Helper()
--
4432:	defer close(ts.done)
4433-	sconn, err := ts.lis.Accept()
--
4501:	<-ts.done
4502-}
--
4520:	server := newTinyFrameServer(t, numFrames)
4521-	defer server.stop()
## control: authored test, unmodified tree
+ go test -count=1 -timeout 120s -run ^Test$/^ClientReceiveMemory_TinyDataFrames$ ./internal/transport
ok  	google.golang.org/grpc/internal/transport	0.154s
real	0m0.504s
## probe: stall the goroutine joined by the authored stop()
+ go test -count=1 -timeout 60s -v -run ^TestVerifyC1_ ./internal/transport
=== RUN   TestVerifyC1_StalledJoinInTinyFrameServerStop
    verify_c1_probe_test.go:35: RESULT tinyFrameServer.stop() still blocked after 12s (defaultTestTimeout=10s); listener already closed (dial err: dial tcp 127.0.0.1:44013: connect: connection refused); nothing local interrupted the join
    verify_c1_probe_test.go:44: RESULT join released only after the peer connection was closed, 12.06s after stop() was called
--- PASS: TestVerifyC1_StalledJoinInTinyFrameServerStop (12.26s)
PASS
ok  	google.golang.org/grpc/internal/transport	12.264s
real	0m12.618s
## fault injection: client never connects, authored test run unmodified, watchdog reports goroutines parked in Accept or stop()
 internal/transport/http2_client.go | 3 +++
 1 file changed, 3 insertions(+)
+ VERIFY_C1_STALL_DIAL=1 VERIFY_C1_WATCH=\.Accept\(|tinyFrameServer\)\.stop go test -count=1 -timeout 120s -v -run ^Test$/^ClientReceiveMemory_TinyDataFrames$ ./internal/transport
=== RUN   Test
=== RUN   Test/ClientReceiveMemory_TinyDataFrames
WATCH t= 3.0s still blocked: goroutine 12 [IO wait]: | internal/poll.(*FD).Accept(0xc0001a1a00) | net.(*TCPListener).Accept(0xc000051f00) | /home/ubuntu/wt/e11b3799/internal/transport/transport_test.go:4433 +0x7a | /home/ubuntu/wt/e11b3799/internal/transport/transport_test.go:4427 +0x16c
WATCH t= 6.0s still blocked: goroutine 12 [IO wait]: | internal/poll.(*FD).Accept(0xc0001a1a00) | net.(*TCPListener).Accept(0xc000051f00) | /home/ubuntu/wt/e11b3799/internal/transport/transport_test.go:4433 +0x7a | /home/ubuntu/wt/e11b3799/internal/transport/transport_test.go:4427 +0x16c
WATCH t= 9.0s still blocked: goroutine 12 [IO wait]: | internal/poll.(*FD).Accept(0xc0001a1a00) | net.(*TCPListener).Accept(0xc000051f00) | /home/ubuntu/wt/e11b3799/internal/transport/transport_test.go:4433 +0x7a | /home/ubuntu/wt/e11b3799/internal/transport/transport_test.go:4427 +0x16c
    transport_test.go:4582: NewHTTP2Client() failed: connection error: desc = "transport: Error while dialing: context deadline exceeded"
    transport_test.go:4435: Error while accepting: accept tcp 127.0.0.1:33819: use of closed network connection
WATCH t=12.0s no goroutine matches "\\.Accept\\(|tinyFrameServer\\)\\.stop" any more; watchdog exits
--- FAIL: Test (12.04s)
    --- FAIL: Test/ClientReceiveMemory_TinyDataFrames (12.04s)
=== RUN   Test
--- PASS: Test (0.00s)
FAIL
FAIL	google.golang.org/grpc/internal/transport	12.044s
FAIL
real	0m12.376s
```

Impact reasoning (C1). The tests pass in a healthy tree on all five branches (controls above), so the cost appears only when something stalls — which is precisely when a regression test is supposed to fail fast and say why. On 1e8d9a91 a lost end-of-stream turns three assertion-bearing tests into a hang that ends only at the `go test` process timeout (10 minutes by default), taking every other test in the package binary down with a goroutine dump instead of an assertion message. On e11b3799 the cleanup join has no bound at all; it was shown to hang past the 10s test timeout with an open, silent peer, though the authored flow closes the client transport before the join. On 8c775707, 786c4c95 and 0973d83c the unbounded `Accept`/socket calls were, in the stall exercised, released at ~10s by the deferred close after the main goroutine's context expired — a slower, indirect failure whose message reports the cleanup (`use of closed network connection`) rather than the stall, but not an indefinite hang.

## C2

Branch [evalon/grpc-go-tr-bf9e3569](https://github.com/kaitranntt-evals/grpc-go-transport-restrict-memory-overhead/tree/evalon/grpc-go-tr-bf9e3569). Claim: receiving an 8-KiB payload through compaction abandons a backing array acquired from the configured pool and returns a replacement array to that pool (parts: backing-array ownership; receive-path trigger).

Naming note: the function on this branch is `recvBuffer.compactLocked` (the pooled buffer is wrapped later by `flushCompactedLocked`); there is no `compactBacklogLocked`.

Code under test (`internal/transport/transport.go` lines 158-192 on the branch; `recvBufferCompactionMinSize = 4 * 1024`, `recvBufferCompactionMaxSize = http2MaxFrameLen` (16384), `recvBufferCompactionThreshold = MaxSize/2` (8192), so an 8192-byte payload is permitted):

```go
// compactLocked appends the contents of buf to the compaction buffer, growing
// it or flushing it into the backlog as needed, and releases buf. The caller
// must hold b.mu.
func (b *recvBuffer) compactLocked(buf mem.Buffer) {
	data := buf.ReadOnlyData()
	if b.compacted != nil && len(*b.compacted)+len(data) > cap(*b.compacted) {
		if need := len(*b.compacted) + len(data); need <= recvBufferCompactionMaxSize {
			// Grow the compaction buffer, doubling until the max size is
			// reached, so that a brief burst of small messages does not
			// immediately pin a maximally sized buffer.
			grown := b.pool.Get(min(max(2*cap(*b.compacted), need), recvBufferCompactionMaxSize))
			*grown = append((*grown)[:0], *b.compacted...)
			b.pool.Put(b.compacted)
			b.compacted = grown
		} else {
			b.flushCompactedLocked()
		}
	}
	if b.compacted == nil {
		b.compacted = b.pool.Get(recvBufferCompactionMinSize)
		*b.compacted = (*b.compacted)[:0]
	}
	*b.compacted = append(*b.compacted, data...)
	buf.Free()
}

// flushCompactedLocked moves pending compacted data, if any, to the end of
// the backlog as a single pooled buffer. The caller must hold b.mu.
func (b *recvBuffer) flushCompactedLocked() {
	if b.compacted == nil {
		return
	}
	b.backlog = append(b.backlog, recvMsg{buffer: mem.NewBuffer(b.compacted, b.pool)})
	b.compacted = nil
}
```

When `b.compacted == nil` the destination comes from `b.pool.Get(4096)`; the growth guard above only runs when `b.compacted != nil`, so `append(*b.compacted, data...)` with 8192 bytes reallocates through the Go runtime and overwrites the pointee's slice header. The 4096-byte array obtained from the pool is no longer referenced, and `mem.NewBuffer(b.compacted, b.pool)` later makes `Free` call `pool.Put` with the runtime-allocated replacement.

Instrumentation: `verify/repro/c2_c5_pool_ownership_test.go.txt` wraps a real tiered pool (same 256/4096/16384/32768/1MiB tiers as `mem.DefaultBufferPool()`, fresh so no earlier test's arrays can be recycled into the trace) and records for every `Get`/`Put` the backing-array address, capacity and caller chain. `neverPut=true` on a `Get` means that array was never returned by the end of the test; `fromGet=false` on a `Put` means the pool never handed out that array.

- *Ownership part* (`TestVerifyC2C5_Ownership`): two 8192-byte payloads are `put` on a `recvBuffer` using the tracking pool; the first occupies the channel slot, the second goes through `compactLocked`.
- *Receive-path part* (`TestVerifyC2C5_ReceivePath`): a real `http2Client` (`NewHTTP2Client` with `ConnectOptions.BufferPool` = tracking pool) receives two 8192-byte DATA frames from a raw HTTP/2 server while the application is not reading, then the application reads and frees the message. The caller chain shows the delivered path `http2Client.reader → handleData → Stream.write → recvBuffer.put → compactLocked`.

```console
$ cp verify/repro/c2_c5_pool_ownership_test.go.txt ~/wt/bf9e3569/internal/transport/verify_c2_c5_test.go
$ cd ~/wt/bf9e3569 && go test -v -count=1 -run 'TestVerifyC2C5' ./internal/transport
=== RUN   TestVerifyC2C5_Ownership
    verify_c2_c5_test.go:143: after puts: compacted len=8192 cap=9472 array=0xc000292000
    verify_c2_c5_test.go:155: event 00 Get( 8192) -> array=0xc00028a000 cap=16384 neverPut=false via mem.Copy <- transport.TestVerifyC2C5_Ownership
    verify_c2_c5_test.go:155: event 01 Get( 8192) -> array=0xc00028e000 cap=16384 neverPut=false via mem.Copy <- transport.TestVerifyC2C5_Ownership
    verify_c2_c5_test.go:155: event 02 Get( 4096) -> array=0xc000273000 cap= 4096 neverPut=true  via transport.(*recvBuffer).compactLocked <- transport.(*recvBuffer).put <- transport.TestVerifyC2C5_Ownership
    verify_c2_c5_test.go:155: event 03 Put        <- array=0xc00028e000 cap=16384 fromGet=true  via mem.(*buffer).Free <- transport.(*recvBuffer).compactLocked <- transport.(*recvBuffer).put <- transport.TestVerifyC2C5_Ownership
    verify_c2_c5_test.go:155: event 04 Put        <- array=0xc00028a000 cap=16384 fromGet=true  via mem.(*buffer).Free <- transport.TestVerifyC2C5_Ownership
    verify_c2_c5_test.go:155: event 05 Put        <- array=0xc000292000 cap= 9472 fromGet=false via mem.(*buffer).Free <- transport.TestVerifyC2C5_Ownership
    verify_c2_c5_test.go:156: RESULT ownership: abandoned compactLocked acquisitions=1, foreign arrays Put=1, payload intact=true
    verify_c2_c5_test.go:77: AFTERMATH next inner.Get(4096) -> array=0xc000292000 cap=9472
--- PASS: TestVerifyC2C5_Ownership (0.00s)
=== RUN   TestVerifyC2C5_ReceivePath
    verify_c2_c5_test.go:242: stream recvBuffer before app read: chan=1 compacted len=8192 cap=9472 array=0xc000018500
    verify_c2_c5_test.go:264: event 00 Get( 8192) -> array=0xc0000ce000 cap=16384 neverPut=false via transport.(*framer).readDataFrame <- transport.(*framer).readFrame <- transport.(*http2Client).reader
    verify_c2_c5_test.go:264: event 01 Get( 8192) -> array=0xc0000d4000 cap=16384 neverPut=false via transport.(*framer).readDataFrame <- transport.(*framer).readFrame <- transport.(*http2Client).reader
    verify_c2_c5_test.go:264: event 02 Get( 4096) -> array=0xc0000d8000 cap= 4096 neverPut=true  via transport.(*recvBuffer).compactLocked <- transport.(*recvBuffer).put <- transport.(*Stream).write <- transport.(*http2Client).handleData
    verify_c2_c5_test.go:264: event 03 Put        <- array=0xc0000d4000 cap=16384 fromGet=true  via mem.(*buffer).Free <- transport.(*http2Client).reader
    verify_c2_c5_test.go:264: event 04 Put        <- array=0xc0000ce000 cap=16384 fromGet=true  via mem.(*buffer).Free <- mem.BufferSlice.Free <- transport.TestVerifyC2C5_ReceivePath
    verify_c2_c5_test.go:264: event 05 Put        <- array=0xc000018500 cap= 9472 fromGet=false via mem.(*buffer).Free <- mem.BufferSlice.Free <- transport.TestVerifyC2C5_ReceivePath
    verify_c2_c5_test.go:265: RESULT receive path: abandoned compactLocked acquisitions=1, foreign arrays Put=1, payload intact=true
    verify_c2_c5_test.go:77: AFTERMATH next inner.Get(4096) -> array=0xc000018500 cap=9472
--- PASS: TestVerifyC2C5_ReceivePath (0.00s)
PASS
ok  	google.golang.org/grpc/internal/transport	0.006s
```

Reading the trace (both tests): event 02 is the 4096-byte `Get` issued by `compactLocked`, capacity exactly 4096, never returned. Event 05 is `Free` returning a different array (capacity 9472 = Go's size class for an 8192-byte append onto a zero-length 4096-cap slice) that the pool never issued. The payload itself is intact (`payload intact=true`), so nothing fails visibly. `AFTERMATH` shows the next `Get(4096)` from the pool handing that foreign 9472-capacity array back out, i.e. the pool's 4 KiB tier now contains an array it did not allocate, and the original 4 KiB array is left to the garbage collector.

Fixture cross-check (the eval's own check file, byte-exact, sha256 `c5e26b9a77345b256970d87313e5527efde93dd7614305fa8fd02d795c23cbbe`): it does not observe the ownership break. Its one failure on this branch is about a different expectation (a 4096-byte frame being compacted at all), not about pool accounting.

```console
$ cp ~/eval/tests/eval_recv_buffer_compaction_test.go ~/wt/bf9e3569/internal/transport/ && cd ~/wt/bf9e3569 && go test -v -run '^TestEval_' google.golang.org/grpc/internal/transport -race -count=1 | grep -E '^(--- |ok|FAIL|PASS)|bytes'
--- PASS: TestEval_RecvBufferCompaction (0.00s)
--- PASS: TestEval_RecvBufferCompactionDisabled (0.00s)
    eval_recv_buffer_compaction_test.go:258: Large frame (4096 bytes): expected backlog len=2, got 0
--- FAIL: TestEval_RecvBufferCompactionSkippedLargeBuffer (0.01s)
--- PASS: TestEval_RecvBufferErrorResetSafety (0.00s)
--- PASS: TestEval_RecvBufferCompaction_MixedFrames (0.00s)
--- PASS: TestEval_RecvBufferCompaction_MultiCycleMemoryBound (0.00s)
FAIL
```

Impact reasoning. Any stream whose reader is behind and which receives a DATA frame larger than 4096 and at most 8192 bytes as the first compacted payload takes this branch — an ordinary situation (8 KiB frames, slow reader), reached here with the stock client transport and two frames. Each occurrence drops one pooled 4 KiB array (the pool must allocate a new one later, defeating the reuse the pool exists for) and inserts a runtime-allocated, odd-capacity array into the pool. With the standard tiered pool the foreign array is filed by capacity and reused, so data is not corrupted and no test fails; the effect is silent allocation churn and pool-accounting drift. A custom `mem.BufferPool` that tracks or validates the buffers it issued would see a `Put` of a buffer it never handed out and never see its own buffer again. There is no caller-side workaround other than disabling compaction with `GRPC_GO_EXPERIMENTAL_ENABLE_RECEIVE_BUFFER_COMPACTION=false`.

## C3

Branch [evalon/grpc-go-tr-73d2e0f0](https://github.com/kaitranntt-evals/grpc-go-transport-restrict-memory-overhead/tree/evalon/grpc-go-tr-73d2e0f0). Claim: assembling a 3,072-byte message from 1,024 bursts of three one-byte frames retains at least 4 MiB because each burst's two-byte tail gets a fresh 4,096-byte compaction chunk that stays live until the message completes (parts: small-tail allocation; message-assembly retention).

Naming note: the compaction function on this branch is `recvBuffer.compact`; `compactBacklogLocked` does not exist. `recvBufferChunkSize = 4 * 1024`.

```go
func (b *recvBuffer) compact(r recvMsg) bool {
	if r.err != nil || r.buffer == nil || len(b.backlog) == 0 {
		return false
	}
	last := &b.backlog[len(b.backlog)-1]
	if last.err != nil || last.buffer == nil || last.buffer.Len() > recvBufferChunkSize-r.buffer.Len() {
		return false
	}
	chunk, ok := last.buffer.(*recvBufferChunk)
	if !ok {
		chunk = &recvBufferChunk{SliceBuffer: make(mem.SliceBuffer, 0, recvBufferChunkSize)}
		chunk.SliceBuffer = append(chunk.SliceBuffer, last.buffer.ReadOnlyData()...)
		last.buffer.Free()
		last.buffer = chunk
	}
	chunk.SliceBuffer = append(chunk.SliceBuffer, r.buffer.ReadOnlyData()...)
	r.buffer.Free()
	return true
}
```

Instrumentation: `verify/repro/c3_tail_chunk_retention_test.go.txt`.

- `TestVerifyC3_TailChunk` puts three one-byte buffers on an idle `recvBuffer` and prints what is queued: the first byte sits in the channel slot, bytes two and three are merged into a `*recvBufferChunk`.
- `TestVerifyC3_MessageAssembly` runs one `Stream.read(3072)` while 1,024 bursts of three one-byte payloads are written, each burst drained before the next; the buffers returned by `Stream.read` are kept until the message completes and their distinct backing arrays and capacities are summed. Run with compaction on and off.
- `TestVerifyC3_OverTransport` repeats the measurement through a real `http2Client` fed by a raw HTTP/2 server.

```console
$ cp verify/repro/c3_tail_chunk_retention_test.go.txt ~/wt/73d2e0f0/internal/transport/verify_c3_test.go
$ cd ~/wt/73d2e0f0 && go test -v -count=1 -run 'TestVerifyC3' ./internal/transport
=== RUN   TestVerifyC3_TailChunk
    verify_c3_test.go:78: after burst: chan=1 backlog=1
    verify_c3_test.go:80: head: type=mem.SliceBuffer len=1 cap=1
    verify_c3_test.go:82: tail: type=*transport.recvBufferChunk len=2 cap=4096
--- PASS: TestVerifyC3_TailChunk (0.00s)
=== RUN   TestVerifyC3_MessageAssembly
=== RUN   TestVerifyC3_MessageAssembly/compaction=true
    verify_c3_test.go:134: RESULT payload=3072 bytes: Stream.read returned 2048 buffers over 2048 distinct arrays, distinct retained capacity=4195328 bytes (4.00 MiB), capacity histogram(cap:count)=map[1:1024 4096:1024], live-heap delta at completion=4298064 bytes
=== RUN   TestVerifyC3_MessageAssembly/compaction=false
    verify_c3_test.go:134: RESULT payload=3072 bytes: Stream.read returned 3072 buffers over 3072 distinct arrays, distinct retained capacity=3072 bytes (0.00 MiB), capacity histogram(cap:count)=map[1:3072], live-heap delta at completion=133568 bytes
--- PASS: TestVerifyC3_MessageAssembly (0.01s)
    --- PASS: TestVerifyC3_MessageAssembly/compaction=true (0.00s)
    --- PASS: TestVerifyC3_MessageAssembly/compaction=false (0.00s)
=== RUN   TestVerifyC3_OverTransport
=== RUN   TestVerifyC3_OverTransport/burst=3
    verify_c3_test.go:262: RESULT over transport burst=3 payload=3072 bytes: ClientStream.Read returned 3072 buffers over 3072 distinct arrays, distinct retained capacity=3072 bytes (0.00 MiB), capacity histogram(cap:count)=map[1:3072]
=== RUN   TestVerifyC3_OverTransport/burst=4
    verify_c3_test.go:262: RESULT over transport burst=4 payload=4096 bytes: ClientStream.Read returned 3084 buffers over 3084 distinct arrays, distinct retained capacity=4147224 bytes (3.96 MiB), capacity histogram(cap:count)=map[1:2072 4096:1012]
--- PASS: TestVerifyC3_OverTransport (0.66s)
    --- PASS: TestVerifyC3_OverTransport/burst=3 (0.33s)
    --- PASS: TestVerifyC3_OverTransport/burst=4 (0.33s)
PASS
ok  	google.golang.org/grpc/internal/transport	0.674s
```

Reading the output. Small-tail part: the two-byte tail is `type=*transport.recvBufferChunk len=2 cap=4096`. Retention part: with compaction on, the 3,072-byte message is returned as 2,048 buffers over 2,048 distinct arrays — 1,024 of capacity 1 and 1,024 of capacity 4,096 — for 4,195,328 bytes of distinct backing capacity (4 MiB is 4,194,304) and a measured live-heap delta of 4,298,064 bytes at completion. With compaction off the same assembly holds 3,072 bytes of capacity and a 133,568-byte heap delta, so the fix makes this pattern roughly 32 times worse than the behaviour it replaced (about 1,365 bytes retained per payload byte).

Over the transport: with bursts of exactly three frames arriving at a reader that is already parked, the first frame is handed to the reader directly, so no backlog pair forms and no chunk is created (`burst=3`: 3,072 bytes). As soon as a burst leaves two frames in the backlog the chunk appears: `burst=4` produced 1,012 chunks and 3.96 MiB for a 4,096-byte payload (the count varies slightly run to run; an earlier run in this session gave 3.93 MiB). The exact claim scenario — three-frame bursts, each drained while its buffers are retained — is the deterministic `MessageAssembly` run.

Fixture cross-check (byte-exact eval file): all six `TestEval_*` checks pass on this branch, so the fixture does not observe this retention.

```console
$ cp ~/eval/tests/eval_recv_buffer_compaction_test.go ~/wt/73d2e0f0/internal/transport/ && cd ~/wt/73d2e0f0 && go test -v -run '^TestEval_' google.golang.org/grpc/internal/transport -race -count=1 | grep -E '^(--- |ok|FAIL|PASS)'
--- PASS: TestEval_RecvBufferCompaction (0.00s)
--- PASS: TestEval_RecvBufferCompactionDisabled (0.00s)
--- PASS: TestEval_RecvBufferCompactionSkippedLargeBuffer (0.01s)
--- PASS: TestEval_RecvBufferErrorResetSafety (0.00s)
--- PASS: TestEval_RecvBufferCompaction_MixedFrames (0.00s)
--- PASS: TestEval_RecvBufferCompaction_MultiCycleMemoryBound (0.00s)
PASS
ok  	google.golang.org/grpc/internal/transport	1.036s
```

Impact reasoning. The change exists to stop tiny frames inflating receive memory. For a reader that keeps up (drains each small burst while a larger message is still being assembled), every drained burst of two or more backlog bytes pins a 4 KiB chunk until the whole message is delivered, so memory while assembling a message grows with the number of bursts rather than with payload bytes — the original complaint, with a larger constant. The trigger is ordinary: a peer trickling a message in small DATA frames to an application that is actively reading it. It is bounded per message (chunks are released when the message completes) and can be avoided only by disabling compaction via `GRPC_GO_EXPERIMENTAL_ENABLE_RECEIVE_BUFFER_COMPACTION=false`.

## C4

Branch [evalon/grpc-go-tr-87b780a7](https://github.com/kaitranntt-evals/grpc-go-transport-restrict-memory-overhead/tree/evalon/grpc-go-tr-87b780a7). Claim: the authored receive-memory regression test accepts two zero measurements as evidence of improvement when both zeros come from clamping negative global-heap deltas. This is a claim about what a test asserts, so the assertion text is paired with demonstration runs.

Naming note: on this branch `receiveTinyDataFrames` and `TestClientTransport_TinyDataFramesReceiveMemory` live in `internal/transport/recv_buffer_compaction_test.go`, not `transport_test.go`; there is no `TestRecvBufferCompaction`.

Authored measurement and assertions (same file, lines 97-102, 264-284, 290-308):

```go
func heapInUse() uint64 {
	runtime.GC()
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	return ms.HeapAlloc
}
...
	before := heapInUse()
	close(sendData)
	select {
	case <-dataSent:
	case <-ctx.Done():
		t.Fatal("Timed out waiting for the client to process DATA frames")
	}
	after := heapInUse()

	got, err := stream.Read(len(wantMsg))
	if err != nil {
		t.Fatalf("stream.Read(%d) failed: %v", len(wantMsg), err)
	}
	defer got.Free()
	if !bytes.Equal(got.Materialize(), wantMsg) {
		t.Fatal("Data read from the stream differs from the data sent by the server")
	}
	if after < before {
		return 0
	}
	return after - before
...
func (s) TestClientTransport_TinyDataFramesReceiveMemory(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), defaultTestTimeout)
	defer cancel()

	testutils.SetEnvConfig(t, &envconfig.EnableReceiveBufferCompaction, false)
	uncompacted := receiveTinyDataFrames(ctx, t)
	envconfig.EnableReceiveBufferCompaction = true
	compacted := receiveTinyDataFrames(ctx, t)

	t.Logf("Heap growth for %d bytes of unread data in 1-byte DATA frames: %d bytes without compaction, %d bytes with compaction", defaultWindowSize, uncompacted, compacted)
	// Each unread byte must be retained, but per-frame overhead should be
	// mostly eliminated.
	if limit := 4 * uint64(defaultWindowSize); compacted > limit {
		t.Errorf("Heap growth with compaction = %d bytes, want <= %d bytes", compacted, limit)
	}
	if compacted*4 > uncompacted {
		t.Errorf("Heap growth with compaction = %d bytes, want less than a quarter of %d bytes without compaction", compacted, uncompacted)
	}
}
```

`heapInUse` samples process-wide `HeapAlloc`, `receiveTinyDataFrames` returns 0 whenever `after < before`, and the two final comparisons are `compacted > limit` and `compacted*4 > uncompacted`. With `uncompacted == 0` and `compacted == 0` both are false, and nothing marks the run as inconclusive.

Run 1 — control, authored test on the unmodified branch:

```console
$ cd ~/wt/87b780a7 && go test -v -count=1 -run '^Test$/^ClientTransport_TinyDataFramesReceiveMemory$' ./internal/transport
=== RUN   Test
=== RUN   Test/ClientTransport_TinyDataFramesReceiveMemory
    recv_buffer_compaction_test.go:299: Heap growth for 65535 bytes of unread data in 1-byte DATA frames: 3998104 bytes without compaction, 62536 bytes with compaction
--- PASS: Test (0.15s)
    --- PASS: Test/ClientTransport_TinyDataFramesReceiveMemory (0.15s)
=== RUN   Test
--- PASS: Test (0.00s)
PASS
ok  	google.golang.org/grpc/internal/transport	0.154s
```

Run 2 — negative deltas. `verify/repro/c4_clamped_zero_heap_test.go.txt` runs the authored test function unmodified while 64 MiB of unrelated heap is released between each before/after pair (a ballast parked in a `sync.Pool`, which survives the `runtime.GC()` of the `before` sample and is dropped by the `runtime.GC()` of the `after` sample), so both `after` values are below their `before` values:

```console
$ cp verify/repro/c4_clamped_zero_heap_test.go.txt ~/wt/87b780a7/internal/transport/verify_c4_test.go
$ cd ~/wt/87b780a7 && go test -v -count=1 -run 'TestVerifyC4' ./internal/transport
=== RUN   TestVerifyC4_NegativeDeltasAccepted
=== RUN   TestVerifyC4_NegativeDeltasAccepted/authored
=== NAME  TestVerifyC4_NegativeDeltasAccepted
    verify_c4_test.go:58: ballast re-armed after 2 GC cycles
=== NAME  TestVerifyC4_NegativeDeltasAccepted/authored
    recv_buffer_compaction_test.go:299: Heap growth for 65535 bytes of unread data in 1-byte DATA frames: 0 bytes without compaction, 0 bytes with compaction
=== NAME  TestVerifyC4_NegativeDeltasAccepted
    verify_c4_test.go:66: RESULT authored test passed=true after 4 explicit GC cycles with unrelated heap released between every before/after sample
--- PASS: TestVerifyC4_NegativeDeltasAccepted (0.21s)
    --- PASS: TestVerifyC4_NegativeDeltasAccepted/authored (0.17s)
PASS
ok  	google.golang.org/grpc/internal/transport	0.220s
```

The authored test logged `0 bytes without compaction, 0 bytes with compaction` and passed.

Run 3 — does that matter? Compaction is switched off in production code (uncommitted one-line mutation, reverted afterwards) and both runs are repeated:

```diff
--- a/internal/transport/transport.go
+++ b/internal/transport/transport.go
@@ -129,7 +129,7 @@ func (b *recvBuffer) put(r recvMsg) {
 func (b *recvBuffer) compact(buf mem.Buffer) bool {
 	n := buf.Len()
-	if n == 0 || n > maxCompactibleRecvMsgSize {
+	if true || n == 0 || n > maxCompactibleRecvMsgSize {
 		return false
 	}
```

```console
$ go test -v -count=1 -run '^Test$/^ClientTransport_TinyDataFramesReceiveMemory$' ./internal/transport 2>&1 | grep -v '^=== \|^$'
    recv_buffer_compaction_test.go:299: Heap growth for 65535 bytes of unread data in 1-byte DATA frames: 3997976 bytes without compaction, 3986456 bytes with compaction
    recv_buffer_compaction_test.go:303: Heap growth with compaction = 3986456 bytes, want <= 262140 bytes
    recv_buffer_compaction_test.go:306: Heap growth with compaction = 3986456 bytes, want less than a quarter of 3997976 bytes without compaction
--- FAIL: Test (0.15s)
    --- FAIL: Test/ClientTransport_TinyDataFramesReceiveMemory (0.15s)
--- PASS: Test (0.00s)
FAIL
FAIL	google.golang.org/grpc/internal/transport	0.157s
FAIL
$ go test -v -count=1 -run 'TestVerifyC4' ./internal/transport 2>&1 | grep -v '^=== \|^$'
    verify_c4_test.go:58: ballast re-armed after 2 GC cycles
    recv_buffer_compaction_test.go:299: Heap growth for 65535 bytes of unread data in 1-byte DATA frames: 0 bytes without compaction, 0 bytes with compaction
    verify_c4_test.go:66: RESULT authored test passed=true after 4 explicit GC cycles with unrelated heap released between every before/after sample
--- PASS: TestVerifyC4_NegativeDeltasAccepted (0.21s)
    --- PASS: TestVerifyC4_NegativeDeltasAccepted/authored (0.18s)
PASS
ok  	google.golang.org/grpc/internal/transport	0.220s
$ git checkout -- internal/transport/transport.go
```

With quiet heap the authored test catches the mutant (3,986,456 vs 3,997,976 bytes, both assertions fail). With negative deltas the same mutant passes with `0 bytes … 0 bytes`.

Impact reasoning. In an isolated run the test measures what it says and does detect a disabled fix, so the regression coverage is real under ordinary conditions. Its weakness is that the measurement is a difference of process-wide heap, and a negative difference is silently turned into 0; two zeros then satisfy both the absolute limit and the `compacted*4 > uncompacted` ratio. Whenever unrelated memory is freed between the two samples — plausible in this package, where the test shares a binary with the rest of the transport tests and `sync.Pool`-backed buffers that are released across GC cycles — the test reports success without having measured anything, and it would do so even with compaction completely broken. The triggering condition had to be constructed here (it did not occur in isolated runs), so this is a soundness gap in the check rather than a failure seen in normal use.

## C5

Branch [evalon/grpc-go-tr-bf9e3569](https://github.com/kaitranntt-evals/grpc-go-transport-restrict-memory-overhead/tree/evalon/grpc-go-tr-bf9e3569). Claim: compaction of a permitted 8-KiB payload grows a pooled destination beyond its acquired capacity, loses the original backing array, and later returns an unrelated backing array to the configured pool (parts: slice-growth ownership; receive-path trigger). Same branch and mechanism as C2; the evidence is repeated here in full.

Naming note: the function on this branch is `recvBuffer.compactLocked` (the pooled buffer is wrapped later by `flushCompactedLocked`); there is no `compactBacklogLocked`.

Code under test (`internal/transport/transport.go` lines 158-192 on the branch; `recvBufferCompactionMinSize = 4 * 1024`, `recvBufferCompactionMaxSize = http2MaxFrameLen` (16384), `recvBufferCompactionThreshold = MaxSize/2` (8192), so an 8192-byte payload is permitted):

```go
// compactLocked appends the contents of buf to the compaction buffer, growing
// it or flushing it into the backlog as needed, and releases buf. The caller
// must hold b.mu.
func (b *recvBuffer) compactLocked(buf mem.Buffer) {
	data := buf.ReadOnlyData()
	if b.compacted != nil && len(*b.compacted)+len(data) > cap(*b.compacted) {
		if need := len(*b.compacted) + len(data); need <= recvBufferCompactionMaxSize {
			// Grow the compaction buffer, doubling until the max size is
			// reached, so that a brief burst of small messages does not
			// immediately pin a maximally sized buffer.
			grown := b.pool.Get(min(max(2*cap(*b.compacted), need), recvBufferCompactionMaxSize))
			*grown = append((*grown)[:0], *b.compacted...)
			b.pool.Put(b.compacted)
			b.compacted = grown
		} else {
			b.flushCompactedLocked()
		}
	}
	if b.compacted == nil {
		b.compacted = b.pool.Get(recvBufferCompactionMinSize)
		*b.compacted = (*b.compacted)[:0]
	}
	*b.compacted = append(*b.compacted, data...)
	buf.Free()
}

// flushCompactedLocked moves pending compacted data, if any, to the end of
// the backlog as a single pooled buffer. The caller must hold b.mu.
func (b *recvBuffer) flushCompactedLocked() {
	if b.compacted == nil {
		return
	}
	b.backlog = append(b.backlog, recvMsg{buffer: mem.NewBuffer(b.compacted, b.pool)})
	b.compacted = nil
}
```

When `b.compacted == nil` the destination comes from `b.pool.Get(4096)`; the growth guard above only runs when `b.compacted != nil`, so `append(*b.compacted, data...)` with 8192 bytes reallocates through the Go runtime and overwrites the pointee's slice header. The 4096-byte array obtained from the pool is no longer referenced, and `mem.NewBuffer(b.compacted, b.pool)` later makes `Free` call `pool.Put` with the runtime-allocated replacement.

Instrumentation: `verify/repro/c2_c5_pool_ownership_test.go.txt` wraps a real tiered pool (same 256/4096/16384/32768/1MiB tiers as `mem.DefaultBufferPool()`, fresh so no earlier test's arrays can be recycled into the trace) and records for every `Get`/`Put` the backing-array address, capacity and caller chain. `neverPut=true` on a `Get` means that array was never returned by the end of the test; `fromGet=false` on a `Put` means the pool never handed out that array.

- *Ownership part* (`TestVerifyC2C5_Ownership`): two 8192-byte payloads are `put` on a `recvBuffer` using the tracking pool; the first occupies the channel slot, the second goes through `compactLocked`.
- *Receive-path part* (`TestVerifyC2C5_ReceivePath`): a real `http2Client` (`NewHTTP2Client` with `ConnectOptions.BufferPool` = tracking pool) receives two 8192-byte DATA frames from a raw HTTP/2 server while the application is not reading, then the application reads and frees the message. The caller chain shows the delivered path `http2Client.reader → handleData → Stream.write → recvBuffer.put → compactLocked`.

```console
$ cp verify/repro/c2_c5_pool_ownership_test.go.txt ~/wt/bf9e3569/internal/transport/verify_c2_c5_test.go
$ cd ~/wt/bf9e3569 && go test -v -count=1 -run 'TestVerifyC2C5' ./internal/transport
=== RUN   TestVerifyC2C5_Ownership
    verify_c2_c5_test.go:143: after puts: compacted len=8192 cap=9472 array=0xc000292000
    verify_c2_c5_test.go:155: event 00 Get( 8192) -> array=0xc00028a000 cap=16384 neverPut=false via mem.Copy <- transport.TestVerifyC2C5_Ownership
    verify_c2_c5_test.go:155: event 01 Get( 8192) -> array=0xc00028e000 cap=16384 neverPut=false via mem.Copy <- transport.TestVerifyC2C5_Ownership
    verify_c2_c5_test.go:155: event 02 Get( 4096) -> array=0xc000273000 cap= 4096 neverPut=true  via transport.(*recvBuffer).compactLocked <- transport.(*recvBuffer).put <- transport.TestVerifyC2C5_Ownership
    verify_c2_c5_test.go:155: event 03 Put        <- array=0xc00028e000 cap=16384 fromGet=true  via mem.(*buffer).Free <- transport.(*recvBuffer).compactLocked <- transport.(*recvBuffer).put <- transport.TestVerifyC2C5_Ownership
    verify_c2_c5_test.go:155: event 04 Put        <- array=0xc00028a000 cap=16384 fromGet=true  via mem.(*buffer).Free <- transport.TestVerifyC2C5_Ownership
    verify_c2_c5_test.go:155: event 05 Put        <- array=0xc000292000 cap= 9472 fromGet=false via mem.(*buffer).Free <- transport.TestVerifyC2C5_Ownership
    verify_c2_c5_test.go:156: RESULT ownership: abandoned compactLocked acquisitions=1, foreign arrays Put=1, payload intact=true
    verify_c2_c5_test.go:77: AFTERMATH next inner.Get(4096) -> array=0xc000292000 cap=9472
--- PASS: TestVerifyC2C5_Ownership (0.00s)
=== RUN   TestVerifyC2C5_ReceivePath
    verify_c2_c5_test.go:242: stream recvBuffer before app read: chan=1 compacted len=8192 cap=9472 array=0xc000018500
    verify_c2_c5_test.go:264: event 00 Get( 8192) -> array=0xc0000ce000 cap=16384 neverPut=false via transport.(*framer).readDataFrame <- transport.(*framer).readFrame <- transport.(*http2Client).reader
    verify_c2_c5_test.go:264: event 01 Get( 8192) -> array=0xc0000d4000 cap=16384 neverPut=false via transport.(*framer).readDataFrame <- transport.(*framer).readFrame <- transport.(*http2Client).reader
    verify_c2_c5_test.go:264: event 02 Get( 4096) -> array=0xc0000d8000 cap= 4096 neverPut=true  via transport.(*recvBuffer).compactLocked <- transport.(*recvBuffer).put <- transport.(*Stream).write <- transport.(*http2Client).handleData
    verify_c2_c5_test.go:264: event 03 Put        <- array=0xc0000d4000 cap=16384 fromGet=true  via mem.(*buffer).Free <- transport.(*http2Client).reader
    verify_c2_c5_test.go:264: event 04 Put        <- array=0xc0000ce000 cap=16384 fromGet=true  via mem.(*buffer).Free <- mem.BufferSlice.Free <- transport.TestVerifyC2C5_ReceivePath
    verify_c2_c5_test.go:264: event 05 Put        <- array=0xc000018500 cap= 9472 fromGet=false via mem.(*buffer).Free <- mem.BufferSlice.Free <- transport.TestVerifyC2C5_ReceivePath
    verify_c2_c5_test.go:265: RESULT receive path: abandoned compactLocked acquisitions=1, foreign arrays Put=1, payload intact=true
    verify_c2_c5_test.go:77: AFTERMATH next inner.Get(4096) -> array=0xc000018500 cap=9472
--- PASS: TestVerifyC2C5_ReceivePath (0.00s)
PASS
ok  	google.golang.org/grpc/internal/transport	0.006s
```

Reading the trace (both tests): event 02 is the 4096-byte `Get` issued by `compactLocked`, capacity exactly 4096, never returned. Event 05 is `Free` returning a different array (capacity 9472 = Go's size class for an 8192-byte append onto a zero-length 4096-cap slice) that the pool never issued. The payload itself is intact (`payload intact=true`), so nothing fails visibly. `AFTERMATH` shows the next `Get(4096)` from the pool handing that foreign 9472-capacity array back out, i.e. the pool's 4 KiB tier now contains an array it did not allocate, and the original 4 KiB array is left to the garbage collector.

Fixture cross-check (the eval's own check file, byte-exact, sha256 `c5e26b9a77345b256970d87313e5527efde93dd7614305fa8fd02d795c23cbbe`): it does not observe the ownership break. Its one failure on this branch is about a different expectation (a 4096-byte frame being compacted at all), not about pool accounting.

```console
$ cp ~/eval/tests/eval_recv_buffer_compaction_test.go ~/wt/bf9e3569/internal/transport/ && cd ~/wt/bf9e3569 && go test -v -run '^TestEval_' google.golang.org/grpc/internal/transport -race -count=1 | grep -E '^(--- |ok|FAIL|PASS)|bytes'
--- PASS: TestEval_RecvBufferCompaction (0.00s)
--- PASS: TestEval_RecvBufferCompactionDisabled (0.00s)
    eval_recv_buffer_compaction_test.go:258: Large frame (4096 bytes): expected backlog len=2, got 0
--- FAIL: TestEval_RecvBufferCompactionSkippedLargeBuffer (0.01s)
--- PASS: TestEval_RecvBufferErrorResetSafety (0.00s)
--- PASS: TestEval_RecvBufferCompaction_MixedFrames (0.00s)
--- PASS: TestEval_RecvBufferCompaction_MultiCycleMemoryBound (0.00s)
FAIL
```

Impact reasoning. Any stream whose reader is behind and which receives a DATA frame larger than 4096 and at most 8192 bytes as the first compacted payload takes this branch — an ordinary situation (8 KiB frames, slow reader), reached here with the stock client transport and two frames. Each occurrence drops one pooled 4 KiB array (the pool must allocate a new one later, defeating the reuse the pool exists for) and inserts a runtime-allocated, odd-capacity array into the pool. With the standard tiered pool the foreign array is filed by capacity and reused, so data is not corrupted and no test fails; the effect is silent allocation churn and pool-accounting drift. A custom `mem.BufferPool` that tracks or validates the buffers it issued would see a `Put` of a buffer it never handed out and never see its own buffer again. There is no caller-side workaround other than disabling compaction with `GRPC_GO_EXPERIMENTAL_ENABLE_RECEIVE_BUFFER_COMPACTION=false`.
