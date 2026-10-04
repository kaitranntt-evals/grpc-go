## Evidence notes — run v-76589dfa

Observations only. Environment: `go version go1.25.7 linux/amd64`, Ubuntu VM, repo `~/repos/grpc-go`.

Branches adjudicated (fetched from `https://github.com/kaitranntt-evals/grpc-go-transport-restrict-memory-overhead`, added as remote `evals`):

| branch | commit | worktree |
|---|---|---|
| `evalon/grpc-go-tr-f05303a3` (C1) | `6976305cb992c4dfa5b10bbaf25511b6879b994b` | `/home/ubuntu/wt/f05303a3` |
| `evalon/grpc-go-tr-af1f6104` (C1) | `fe922aaa09f4b8863f6a085927da5db5adb856cc` | `/home/ubuntu/wt/af1f6104` |
| `evalon/grpc-go-tr-5b5a89b6` (C2) | `0a0bcba92516e480e81e781c79c99fa4ed880817` | `/home/ubuntu/wt/5b5a89b6` |

Setup, replayable:

```sh
cd ~/repos/grpc-go
git remote add evals https://github.com/kaitranntt-evals/grpc-go-transport-restrict-memory-overhead
git fetch evals evalon/grpc-go-tr-f05303a3 evalon/grpc-go-tr-af1f6104 evalon/grpc-go-tr-5b5a89b6
for b in f05303a3 af1f6104 5b5a89b6; do git worktree add --detach /home/ubuntu/wt/$b evals/evalon/grpc-go-tr-$b; done
```

The eval fixture `tests/eval_recv_buffer_compaction_test.go` (sha256 `0e0abee10d1e86c6ee91b687bb5a33d2e6fcef5698b8ad861ab29a73c47252c8`) was extracted byte-exact from `eval_tests.zip`. It contains no listener or socket code (`grep -n "Accept\|net.Listen\|SetDeadline"` prints nothing), so C1 concerns only the tests the solutions themselves added.

## C1

**Claim:** added/modified tests execute newly authored server-side socket operations without effective local bounds that interrupt stalled operations. Adjudicated per branch.

### What the test servers are (both branches)

Each branch adds exactly one test with a raw HTTP/2 test server on a real TCP listener. Socket-relevant lines of the pristine branch files:

```sh
# f05303a3: (s) TestClientReceivesManyTinyDataFrames, internal/transport/transport_test.go
awk 'NR>=4440 && /Accept\(\)|Deadline|AfterFunc|WithTimeout|lis.Close|sconn.Close|ct.Close|ctx.Done|ReadFull|ReadFrame|Write(Settings|SettingsAck|Headers|Data)\(/ {print NR": "$0}' internal/transport/transport_test.go
# af1f6104: runTinyDataFramesClient, called by (s) TestClientTinyDataFramesBoundReceiveMemory, internal/transport/recv_buffer_test.go
awk 'NR>=425 && /Accept\(\)|Deadline|AfterFunc|WithTimeout|lis.Close|sconn.Close|ct.Close|ctx.Done|ctx.Err|ReadFull|ReadFrame|Write(Settings|SettingsAck|Headers|Data|Ping)\(|Flush\(/ {print NR": "$0}' internal/transport/recv_buffer_test.go
```

```console
--- f05303a3 (pristine) transport_test.go
4456: 			defer lis.Close()
4461: 					sconn, err := lis.Accept()
4465: 					defer sconn.Close()
4466: 					if _, err := io.ReadFull(sconn, make([]byte, len(clientPreface))); err != nil {
4470: 					if err := sfr.WriteSettings(); err != nil {
4473: 					if err := sfr.WriteSettingsAck(); err != nil {
4478: 						frame, err := sfr.ReadFrame()
4496: 					if err := sfr.WriteHeaders(http2.HeadersFrameParam{StreamID: 1, BlockFragment: hbuf.Bytes(), EndHeaders: true}); err != nil {
4501: 						if err := sfr.WriteData(1, false, []byte{byte(i)}); err != nil {
4513: 					if err := sfr.WriteHeaders(http2.HeadersFrameParam{StreamID: 1, BlockFragment: hbuf.Bytes(), EndHeaders: true, EndStream: true}); err != nil {
4518: 						if _, err := sfr.ReadFrame(); err != nil {
4525: 			ctx, cancel := context.WithTimeout(context.Background(), defaultTestTimeout)
4535: 			defer ct.Close(fmt.Errorf("closed manually by test"))
4549: 			case <-ctx.Done():
--- af1f6104 (pristine) recv_buffer_test.go
440: 	defer lis.Close()
449: 			sconn, err := lis.Accept()
453: 			defer sconn.Close()
454: 			if _, err := io.ReadFull(sconn, make([]byte, len(clientPreface))); err != nil {
461: 			if err := sfr.WriteSettings(); err != nil {
464: 			if err := sfr.WriteSettingsAck(); err != nil {
467: 			if err := w.Flush(); err != nil {
473: 				frame, err := sfr.ReadFrame()
494: 						if err := sfr.WriteHeaders(http2.HeadersFrameParam{StreamID: 1, BlockFragment: hbuf.Bytes(), EndHeaders: true}); err != nil {
499: 							if err := sfr.WriteData(1, false, want[i:i+1]); err != nil {
504: 						if err := w.Flush(); err != nil {
513: 						if err := sfr.WritePing(true, frame.Data); err == nil {
514: 							w.Flush()
523: 	ctx, cancel := context.WithTimeout(context.Background(), defaultTestTimeout)
533: 	defer ct.Close(errors.New("closed manually by test"))
538: 	case <-ctx.Done():
551: 	case <-ctx.Done():
562: 		if ctx.Err() != nil {
```

Static reading (not the verdict basis): on both branches the server goroutine sets **no deadline** on the listener or the accepted conn, and starts no timer of its own. The only things that can interrupt it are in the test's main goroutine: `defer lis.Close()`, and the client transport created with a `context.WithTimeout(..., defaultTestTimeout)` context (`defaultTestTimeout = 10 * time.Second`, `internal/transport/keepalive_test.go:47`), plus `defer ct.Close(...)`. Whether that is an *effective, independently timed* interruption is what the runs below measure.

### Part `execution` — the tests exercise these server operations (holds on both branches)

Unmodified branch tests, both pass and run the server:

```sh
cd /home/ubuntu/wt/f05303a3 && go test ./internal/transport -run 'Test/(ClientReceivesManyTinyDataFrames|RecvBufferCompaction)' -count=1 -v 2>&1 | grep -E "^\s*(--- |PASS|FAIL|ok)"
cd /home/ubuntu/wt/af1f6104 && go test ./internal/transport -run 'Test/(ClientTinyDataFramesBoundReceiveMemory|RecvBufferCompaction)' -count=1 -v 2>&1 | grep -E "^\s*(--- |PASS|FAIL|ok)"
```

```console
--- PASS: Test (0.03s)
    --- PASS: Test/ClientReceivesManyTinyDataFrames (0.02s)
        --- PASS: Test/ClientReceivesManyTinyDataFrames/compaction_enabled (0.01s)
        --- PASS: Test/ClientReceivesManyTinyDataFrames/compaction_disabled (0.01s)
    --- PASS: Test/RecvBufferCompactionMixedSizes (0.00s)
    --- PASS: Test/RecvBufferCompactionTinyMessages (0.01s)
ok  	google.golang.org/grpc/internal/transport	0.032s
--- PASS: Test (0.18s)
    --- PASS: Test/ClientTinyDataFramesBoundReceiveMemory (0.15s)
        --- PASS: Test/ClientTinyDataFramesBoundReceiveMemory/compaction=true (0.07s)
        --- PASS: Test/ClientTinyDataFramesBoundReceiveMemory/compaction=false (0.08s)
ok  	google.golang.org/grpc/internal/transport	0.184s
```

(output trimmed to the relevant lines). With the timestamping listener wrapper (below) and no stall, the server's `lis.Accept()` and conn reads are visibly executed by the test — see the `none` logs in the next section.

### Part `local_bound` — stall injection: is a stalled server operation interrupted, by what, and when?

Instrumentation (committed under `verify/instrumentation/`):

- `c1_stall_instrumentation_test.go.txt` — copied to `internal/transport/verify_stall_test.go`. Wraps the test's listener only to timestamp `Accept`/`Read`/`Write`/`Close`/`Set*Deadline` on the server side (the solution's server code is not changed), and provides a client-side `ConnectOptions.Dialer` that injects a stall selected by `VERIFY_STALL`.
- `c1-f05303a3.diff`, `c1-af1f6104.diff` — the two-line hook per branch (`lis = verifyWrapListener(lis)` and `Dialer: verifyDialer(),`).
- `c1-run.sh` — runs the five modes.

Stall modes: `accept` (client dials a black-hole listener, so the server's `lis.Accept()` never receives a connection); `read` (client connects, its writes are swallowed, so the server's first read on the accepted conn — the client preface — stalls); `stream` (handshake completes, client HEADERS frame is swallowed, so the server's `ReadFrame` loop stalls); `write` (client stops reading after 18 bytes, to try to stall the server's DATA writes).

```sh
I=~/repos/grpc-go/verify/instrumentation
for b in f05303a3 af1f6104; do
  cp $I/c1_stall_instrumentation_test.go.txt /home/ubuntu/wt/$b/internal/transport/verify_stall_test.go
  (cd /home/ubuntu/wt/$b && git apply $I/c1-$b.diff)
  sh $I/c1-run.sh /home/ubuntu/wt/$b $b /home/ubuntu/out
done
```

Full logs are committed in `verify/logs/<branch>-<mode>.log`. Verbatim (lines cut at 400 chars):

#### f05303a3 — `accept`

```console
=== RUN   Test
=== RUN   Test/ClientReceivesManyTinyDataFrames
=== RUN   Test/ClientReceivesManyTinyDataFrames/compaction_enabled
[verify +  0.00s] client: dialing black-hole 127.0.0.1:46295 instead of the test server
[verify +  0.00s] server lis.Accept(): begin
    transport_test.go:4535: Error while creating client transport: connection error: desc = "error reading server preface: read tcp 127.0.0.1:53418->127.0.0.1:46295: use of closed network connection"
[verify + 10.00s] lis.Close() called by: runtime.Goexit:615 <- testing.(*common).FailNow:1013 <- testing.(*common).Fatalf:1219
[verify + 10.00s] server lis.Accept(): returned after 10.00s err=accept tcp 127.0.0.1:37179: use of closed network connection
--- FAIL: Test (10.00s)
    --- FAIL: Test/ClientReceivesManyTinyDataFrames (10.00s)
        --- FAIL: Test/ClientReceivesManyTinyDataFrames/compaction_enabled (10.00s)
=== RUN   Test
--- PASS: Test (0.00s)
FAIL
exit=1 wall=10.008358093s
```

#### f05303a3 — `read`

```console
=== RUN   Test
=== RUN   Test/ClientReceivesManyTinyDataFrames
=== RUN   Test/ClientReceivesManyTinyDataFrames/compaction_enabled
[verify +  0.00s] client: connected to test server; all client writes will be swallowed
[verify +  0.00s] server lis.Accept(): begin
[verify +  0.00s] server lis.Accept(): returned after 0.00s err=<nil>
[verify +  1.00s] server sconn.Read #1: STALLED >1s (no deadline set on conn)
[verify + 10.00s] client conn.Close(): closing socket, called by: transport.NewHTTP2Client.func4:260 <- runtime.goexit:1693
[verify + 10.00s] client conn.Close(): closing socket, called by: transport.(*http2Client).Close:1073 <- transport.NewHTTP2Client.func5:426 <- transport.NewHTTP2Client:488
[verify + 10.00s] client conn.Close(): closing socket, called by: transport.NewHTTP2Client.func2:236 <- transport.NewHTTP2Client:488 <- transport.s.TestClientReceivesManyTinyDataFrames.func1:4533
    transport_test.go:4535: Error while creating client transport: connection error: desc = "error reading server preface: read tcp 127.0.0.1:44080->127.0.0.1:38257: use of closed network connection"
[verify + 10.00s] lis.Close() called by: runtime.Goexit:615 <- testing.(*common).FailNow:1013 <- testing.(*common).Fatalf:1219
[verify + 10.00s] server sconn.Read #1: returned after 10.00s n=0 err=EOF
[verify + 10.00s] server sconn.Close() called by: transport.s.TestClientReceivesManyTinyDataFrames.func1.1.1:4468 <- transport.s.TestClientReceivesManyTinyDataFrames.func1.1:4523 <- runtime.goexit:1693
--- FAIL: Test (10.05s)
    --- FAIL: Test/ClientReceivesManyTinyDataFrames (10.05s)
        --- FAIL: Test/ClientReceivesManyTinyDataFrames/compaction_enabled (10.00s)
=== RUN   Test
--- PASS: Test (0.00s)
FAIL
exit=1 wall=10.059144189s
```

#### f05303a3 — `stream`

```console
=== RUN   Test
=== RUN   Test/ClientReceivesManyTinyDataFrames
=== RUN   Test/ClientReceivesManyTinyDataFrames/compaction_enabled
[verify +  0.00s] server lis.Accept(): begin
[verify +  0.00s] server lis.Accept(): returned after 0.00s err=<nil>
[verify +  0.00s] client: swallowing HEADERS frame and all later client writes
[verify +  1.00s] server sconn.Read #4: STALLED >1s (no deadline set on conn)
[verify + 10.04s] client conn.Close(): closing socket, called by: transport.NewHTTP2Client.func6:483 <- runtime.goexit:1693
[verify + 10.04s] client conn.Close(): closing socket, called by: transport.(*http2Client).Close:1073 <- transport.(*http2Client).reader.func1:1716 <- transport.(*http2Client).reader:1760
    transport_test.go:4552: Timed out waiting for the stream to complete
[verify + 10.04s] lis.Close() called by: runtime.Goexit:615 <- testing.(*common).FailNow:1013 <- testing.(*common).Fatalf:1219
[verify + 10.04s] server sconn.Read #4: returned after 10.04s n=0 err=EOF
[verify + 10.04s] server sconn.Close() called by: transport.s.TestClientReceivesManyTinyDataFrames.func1.1.1:4481 <- transport.s.TestClientReceivesManyTinyDataFrames.func1.1:4523 <- runtime.goexit:1693
--- FAIL: Test (10.04s)
    --- FAIL: Test/ClientReceivesManyTinyDataFrames (10.04s)
        --- FAIL: Test/ClientReceivesManyTinyDataFrames/compaction_enabled (10.04s)
=== RUN   Test
--- PASS: Test (0.00s)
FAIL
exit=1 wall=10.044087695s
```

#### f05303a3 — `write`

```console
=== RUN   Test
=== RUN   Test/ClientReceivesManyTinyDataFrames
=== RUN   Test/ClientReceivesManyTinyDataFrames/compaction_enabled
[verify +  0.00s] server lis.Accept(): begin
[verify +  0.00s] server lis.Accept(): returned after 0.00s err=<nil>
[verify +  0.00s] client: stops reading from the socket
[verify +  1.01s] server sconn.Read #6: STALLED >1s (no deadline set on conn)
[verify + 10.04s] client conn.Close(): closing socket, called by: transport.NewHTTP2Client.func6:483 <- runtime.goexit:1693
    transport_test.go:4552: Timed out waiting for the stream to complete
[verify + 10.04s] client conn.Close(): closing socket, called by: transport.(*http2Client).Close:1073 <- runtime.Goexit:615 <- testing.(*common).FailNow:1013
[verify + 10.04s] lis.Close() called by: runtime.Goexit:615 <- testing.(*common).FailNow:1013 <- testing.(*common).Fatalf:1219
[verify + 10.04s] server sconn.Read #6: returned after 10.03s n=0 err=read tcp 127.0.0.1:39491->127.0.0.1:54808: read: connection reset by peer
[verify + 10.04s] server sconn.Close() called by: transport.s.TestClientReceivesManyTinyDataFrames.func1.1.1:4520 <- transport.s.TestClientReceivesManyTinyDataFrames.func1.1:4523 <- runtime.goexit:1693
--- FAIL: Test (10.04s)
    --- FAIL: Test/ClientReceivesManyTinyDataFrames (10.04s)
        --- FAIL: Test/ClientReceivesManyTinyDataFrames/compaction_enabled (10.04s)
=== RUN   Test
--- PASS: Test (0.00s)
FAIL
exit=1 wall=10.044564730s
```

#### f05303a3 — `none` (no stall)

```console
=== RUN   Test
=== RUN   Test/ClientReceivesManyTinyDataFrames
=== RUN   Test/ClientReceivesManyTinyDataFrames/compaction_enabled
[verify +  0.00s] server lis.Accept(): begin
[verify +  0.00s] server lis.Accept(): returned after 0.00s err=<nil>
    transport_test.go:4556: received 8192 one-byte DATA frames: backlog entries = 2, retained bytes = 8256
[verify +  0.02s] server sconn.Read #14: returned after 0.00s n=0 err=EOF
[verify +  0.02s] server sconn.Close() called by: transport.s.TestClientReceivesManyTinyDataFrames.func1.1.1:4520 <- transport.s.TestClientReceivesManyTinyDataFrames.func1.1:4523 <- runtime.goexit:1693
[verify +  0.02s] lis.Close() called by: transport.s.TestClientReceivesManyTinyDataFrames.func1:4585 <- testing.tRunner:1934 <- runtime.goexit:1693
--- PASS: Test (0.02s)
    --- PASS: Test/ClientReceivesManyTinyDataFrames (0.02s)
        --- PASS: Test/ClientReceivesManyTinyDataFrames/compaction_enabled (0.02s)
=== RUN   Test
--- PASS: Test (0.00s)
PASS
exit=0 wall=.024990014s
```

#### af1f6104 — `accept`

```console
=== RUN   Test
=== RUN   Test/ClientTinyDataFramesBoundReceiveMemory
=== RUN   Test/ClientTinyDataFramesBoundReceiveMemory/compaction=true
[verify +  0.00s] server lis.Accept(): begin
[verify +  0.00s] client: dialing black-hole 127.0.0.1:33137 instead of the test server
    recv_buffer_test.go:422: Error while creating client transport: connection error: desc = "error reading server preface: read tcp 127.0.0.1:52516->127.0.0.1:33137: use of closed network connection"
[verify + 10.05s] lis.Close() called by: runtime.Goexit:615 <- testing.(*common).FailNow:1013 <- testing.(*common).Fatalf:1219
[verify + 10.05s] server lis.Accept(): returned after 10.05s err=accept tcp 127.0.0.1:37865: use of closed network connection
--- FAIL: Test (10.05s)
    --- FAIL: Test/ClientTinyDataFramesBoundReceiveMemory (10.05s)
        --- FAIL: Test/ClientTinyDataFramesBoundReceiveMemory/compaction=true (10.05s)
=== RUN   Test
--- PASS: Test (0.00s)
FAIL
exit=1 wall=10.058573463s
```

#### af1f6104 — `read`

```console
=== RUN   Test
=== RUN   Test/ClientTinyDataFramesBoundReceiveMemory
=== RUN   Test/ClientTinyDataFramesBoundReceiveMemory/compaction=true
[verify +  0.00s] server lis.Accept(): begin
[verify +  0.00s] client: connected to test server; all client writes will be swallowed
[verify +  0.00s] server lis.Accept(): returned after 0.00s err=<nil>
[verify +  1.00s] server sconn.Read #1: STALLED >1s (no deadline set on conn)
[verify + 10.04s] client conn.Close(): closing socket, called by: transport.NewHTTP2Client.func4:260 <- runtime.goexit:1693
[verify + 10.04s] server sconn.Read #1: returned after 10.04s n=0 err=EOF
[verify + 10.04s] client conn.Close(): closing socket, called by: transport.(*http2Client).Close:1073 <- transport.NewHTTP2Client.func5:426 <- transport.NewHTTP2Client:488
[verify + 10.04s] server sconn.Close() called by: transport.runTinyDataFramesClient.func1.1:456 <- transport.runTinyDataFramesClient.func1:521 <- runtime.goexit:1693
[verify + 10.04s] client conn.Close(): closing socket, called by: transport.NewHTTP2Client.func2:236 <- transport.NewHTTP2Client:488 <- transport.runTinyDataFramesClient:531
    recv_buffer_test.go:422: Error while creating client transport: connection error: desc = "error reading server preface: read tcp 127.0.0.1:53236->127.0.0.1:39743: use of closed network connection"
[verify + 10.04s] lis.Close() called by: runtime.Goexit:615 <- testing.(*common).FailNow:1013 <- testing.(*common).Fatalf:1219
--- FAIL: Test (10.05s)
    --- FAIL: Test/ClientTinyDataFramesBoundReceiveMemory (10.05s)
        --- FAIL: Test/ClientTinyDataFramesBoundReceiveMemory/compaction=true (10.04s)
=== RUN   Test
--- PASS: Test (0.00s)
FAIL
exit=1 wall=10.055275972s
```

#### af1f6104 — `stream`

```console
=== RUN   Test
=== RUN   Test/ClientTinyDataFramesBoundReceiveMemory
=== RUN   Test/ClientTinyDataFramesBoundReceiveMemory/compaction=true
[verify +  0.00s] server lis.Accept(): begin
[verify +  0.00s] server lis.Accept(): returned after 0.00s err=<nil>
[verify +  0.00s] client: swallowing HEADERS frame and all later client writes
[verify +  1.00s] server sconn.Read #4: STALLED >1s (no deadline set on conn)
[verify + 10.02s] client conn.Close(): closing socket, called by: transport.NewHTTP2Client.func6:483 <- runtime.goexit:1693
[verify + 10.02s] client conn.Close(): closing socket, called by: transport.(*http2Client).Close:1073 <- transport.(*http2Client).reader.func1:1716 <- transport.(*http2Client).reader:1760
    recv_buffer_test.go:422: Timed out waiting for the server to send all DATA frames
[verify + 10.02s] lis.Close() called by: runtime.Goexit:615 <- testing.(*common).FailNow:1013 <- testing.(*common).Fatalf:1219
[verify + 10.02s] server sconn.Read #4: returned after 10.02s n=0 err=EOF
[verify + 10.02s] server sconn.Close() called by: transport.runTinyDataFramesClient.func1.1:477 <- transport.runTinyDataFramesClient.func1:521 <- runtime.goexit:1693
--- FAIL: Test (10.03s)
    --- FAIL: Test/ClientTinyDataFramesBoundReceiveMemory (10.03s)
        --- FAIL: Test/ClientTinyDataFramesBoundReceiveMemory/compaction=true (10.02s)
=== RUN   Test
--- PASS: Test (0.00s)
FAIL
exit=1 wall=10.034801017s
```

#### af1f6104 — `write`

```console
=== RUN   Test
=== RUN   Test/ClientTinyDataFramesBoundReceiveMemory
=== RUN   Test/ClientTinyDataFramesBoundReceiveMemory/compaction=true
[verify +  0.00s] server lis.Accept(): begin
[verify +  0.00s] server lis.Accept(): returned after 0.00s err=<nil>
[verify +  0.00s] client: stops reading from the socket
[verify +  1.01s] server sconn.Read #6: STALLED >1s (no deadline set on conn)
[verify + 10.00s] client conn.Close(): closing socket, called by: transport.NewHTTP2Client.func6:483 <- runtime.goexit:1693
    recv_buffer_test.go:422: Timed out waiting for the client to queue all data; queued 0 bytes, want 65535
[verify + 10.00s] lis.Close() called by: runtime.Goexit:615 <- testing.(*common).FailNow:1013 <- testing.(*common).Fatalf:1219
[verify + 10.00s] server sconn.Read #6: returned after 10.00s n=0 err=read tcp 127.0.0.1:45991->127.0.0.1:40480: read: connection reset by peer
[verify + 10.00s] client conn.Close(): closing socket, called by: transport.(*http2Client).Close:1073 <- transport.(*http2Client).reader.func1:1716 <- transport.(*http2Client).reader:1760
[verify + 10.00s] server sconn.Close() called by: transport.runTinyDataFramesClient.func1.1:477 <- transport.runTinyDataFramesClient.func1:521 <- runtime.goexit:1693
--- FAIL: Test (10.01s)
    --- FAIL: Test/ClientTinyDataFramesBoundReceiveMemory (10.01s)
        --- FAIL: Test/ClientTinyDataFramesBoundReceiveMemory/compaction=true (10.00s)
=== RUN   Test
--- PASS: Test (0.00s)
FAIL
exit=1 wall=10.012296694s
```

#### af1f6104 — `none` (no stall)

```console
=== RUN   Test
=== RUN   Test/ClientTinyDataFramesBoundReceiveMemory
=== RUN   Test/ClientTinyDataFramesBoundReceiveMemory/compaction=true
[verify +  0.00s] server lis.Accept(): begin
[verify +  0.00s] server lis.Accept(): returned after 0.00s err=<nil>
    recv_buffer_test.go:422: client retained 127240 bytes while 65535 bytes were queued unread in 8 entries
[verify +  0.06s] lis.Close() called by: transport.runTinyDataFramesClient:580 <- transport.s.TestClientTinyDataFramesBoundReceiveMemory.func1:422 <- testing.tRunner:1934
=== NAME  Test/ClientTinyDataFramesBoundReceiveMemory
    recv_buffer_test.go:426: client retained 127240 bytes with compaction enabled and 0 bytes with it disabled, want at least a 4x reduction
[verify +  0.06s] server sconn.Read #26: returned after 0.00s n=0 err=EOF
[verify +  0.06s] server sconn.Close() called by: transport.runTinyDataFramesClient.func1.1:477 <- transport.runTinyDataFramesClient.func1:521 <- runtime.goexit:1693
--- FAIL: Test (0.07s)
    --- FAIL: Test/ClientTinyDataFramesBoundReceiveMemory (0.07s)
        --- PASS: Test/ClientTinyDataFramesBoundReceiveMemory/compaction=true (0.06s)
=== RUN   Test
--- PASS: Test (0.00s)
FAIL
exit=1 wall=.075735647s
```

(The `FAIL` in the af1f6104 `none` run is an artifact of running only the `compaction=true` subtest: the parent compares it with the `compaction=false` subtest that was filtered out — "0 bytes with it disabled". The unfiltered run above passes.)

### Observations (identical on both branches)

| stalled server operation | interrupted after | by | timer source |
|---|---|---|---|
| `lis.Accept()` | 10.00 s (f05303a3) / 10.05 s (af1f6104) | `lis.Close()` from the test's `defer`, run by `t.Fatalf` | `NewHTTP2Client` fails when its 10 s `connectCtx` expires |
| first read on accepted conn (`io.ReadFull` client preface) | 10.00 s / 10.04 s, `err=EOF` | client socket closed by `NewHTTP2Client.func4` (the `connectCtx` watchdog goroutine, `http2_client.go:260`) | same 10 s context |
| `sfr.ReadFrame()` waiting for HEADERS | 10.04 s / 10.02 s, `err=EOF` | client socket closed by `NewHTTP2Client.func6` (`http2_client.go:483`, the client's loopy-writer goroutine closing the conn, logged at the instant the 10 s context expired) and by the deferred `ct.Close` run by `t.Fatalf("Timed out ...")` | same 10 s context |
| trailing `sfr.ReadFrame()` (mode `write`) | 10.03 s / 10.00 s, `connection reset by peer` | same as above | same 10 s context |

- No `SetDeadline`/`SetReadDeadline`/`SetWriteDeadline` call was ever logged on the server conn: there is no *prior deadline* on any of these operations (matches the static reading).
- In every stall the operation was nevertheless interrupted at ~10 s by a path driven by the test's `context.WithTimeout(context.Background(), defaultTestTimeout)` timer, which runs independently of the stalled server goroutine. The test failed in ~10 s (`wall=10.0xs`, `exit=1`); it never ran into the `-test.timeout 120s` guard, and the logs contain no goroutine-leak report from the per-test leak check (`grpctest.Tester.Teardown`): the server goroutine returned (`server sconn.Close()` / `Accept(): returned` lines).
- Mode `write` could not make a server *write* stall: 8192 (f05303a3) / 65535 (af1f6104) ten-byte frames fit in loopback socket buffers, so the server ran on to its trailing read, which is the operation that stalled and was interrupted. No `server sconn.Write: STALLED` line was ever logged.
- A first attempt at a "peer never closes" double fault (client `Close()` suppressed entirely) hung for the full 120 s, but the hang was inside the client's own `NewHTTP2Client` reader, i.e. caused by the injected fault rather than by the test server; that mode was removed and is not used as evidence.

### Per-part result

- `execution`: holds on both branches — the added tests do run the listener accept and accepted-connection operations.
- `local_bound`: does not hold on either branch — no operation has a prior deadline, but every exercised accept / accepted-conn operation that was stalled was interrupted at ~10 s by an independently timed action (context deadline → listener close / peer socket close). The bound lives in the test function (context + deferred cleanup), not inside the server goroutine.

## C2

**Claim:** handler receive-buffer compaction leaves pooled short-read buffers unmerged because its source-type restriction excludes them. Target branch `evalon/grpc-go-tr-5b5a89b6` (worktree `/home/ubuntu/wt/5b5a89b6`).

### Code under test (context for the runs)

`internal/transport/transport.go` on the branch — `recvBuffer.compact` (the branch has no `compactBacklogLocked`; `compact` is the equivalent):

```go
	in, ok := r.buffer.(mem.SliceBuffer)
	if !ok {
		return false
	}
	...
	prev, ok := tail.buffer.(mem.SliceBuffer)
	if !ok {
		return false
	}
```

`internal/transport/handler_server.go:440-444` (unchanged by the branch):

```go
			buf := ht.bufferPool.Get(http2MaxFrameLen)
			n, err := req.Body.Read(*buf)
			if n > 0 {
				*buf = (*buf)[:n]
				s.buf.put(recvMsg{buffer: mem.NewBuffer(buf, ht.bufferPool)})
```

`mem.NewBuffer` returns a `mem.SliceBuffer` only when `pool == nil || IsBelowBufferPoolingThreshold(cap(*data))`; here the capacity is 16384, so it returns a pooled `*mem.buffer`.

### Repro files

- `verify/repro/c2_unit_pooled_vs_unpooled_test.go.txt` → `internal/transport/verify_c2_unit_test.go`
- `verify/repro/c2_handler_short_reads_test.go.txt` → `internal/transport/verify_c2_handler_test.go`

```sh
cd /home/ubuntu/wt/5b5a89b6
cp ~/repos/grpc-go/verify/repro/c2_unit_pooled_vs_unpooled_test.go.txt internal/transport/verify_c2_unit_test.go
cp ~/repos/grpc-go/verify/repro/c2_handler_short_reads_test.go.txt internal/transport/verify_c2_handler_test.go
go test ./internal/transport -run '^TestVerifyC2_' -count=1 -v
```

### Part `compaction_type_handling` — pooled buffers are excluded, equivalent unpooled buffers merge (holds)

`TestVerifyC2_UnitPooledVsUnpooled` queues the same 1026 one-byte payloads into a fresh `recvBuffer` three ways; only the buffer's dynamic type differs. Verbatim:

```console
=== RUN   TestVerifyC2_UnitPooledVsUnpooled
    verify_c2_unit_test.go:50: unpooled mem.SliceBuffer                                           type=mem.SliceBuffer  backlog entries=   1 (of 1025 queued behind the channel slot), backing capacity held=2048 bytes
    verify_c2_unit_test.go:50: fixture input: mem.Copy(1 byte, pool)                              type=mem.SliceBuffer  backlog entries=   1 (of 1025 queued behind the channel slot), backing capacity held=2048 bytes
    verify_c2_unit_test.go:50: handler input: NewBuffer(pool.Get(http2MaxFrameLen)[:1], pool)     type=*mem.buffer      backlog entries=1025 (of 1025 queued behind the channel slot), backing capacity held=16793600 bytes
    verify_c2_unit_test.go:68: C2: pooled handler-shaped short reads were NOT merged: 1025 backlog entries for 1026 one-byte payloads, want <= 64 (unpooled got 1)
--- FAIL: TestVerifyC2_UnitPooledVsUnpooled (0.00s)
```

Unpooled `mem.SliceBuffer` payloads (and the fixture's `mem.Copy(1 byte, pool)` inputs, which are also `mem.SliceBuffer`) collapse to 1 backlog entry holding 2048 bytes; the handler-shaped pooled `*mem.buffer` payloads stay at 1025 entries pinning 1025 × 16384 = 16,793,600 bytes. All bytes were still delivered in order in every shape (no `payload mismatch` error).

### Part `handler_receive_path` — the real handler supplies exactly those pooled buffers (holds)

`TestVerifyC2_HandlerHandleStreams` runs the unmodified `serverHandlerTransport.HandleStreams` reader goroutine (via the repo's own `newHandleStreamTest`, pool = `mem.DefaultBufferPool()`) and writes the request body one byte per `Body.Read`. `TestVerifyC2_HandlerOverRealHTTP2` does the same through a real `net/http` HTTP/2 TLS server (`NewServerHandlerTransport(w, r, nil, mem.DefaultBufferPool())` + `HandleStreams`, as `grpc.Server.ServeHTTP` does) with an HTTP/2 client uploading one byte per DATA frame. The application does not read; the test then inspects `s.buf.backlog`. Compaction enabled (default), verbatim:

```console
=== RUN   TestVerifyC2_HandlerHandleStreams
    verify_c2_handler_test.go:92: compaction enabled=true: 1026 one-byte body reads -> backlog entries=1025, entry types=map[*mem.buffer:1025], backing capacity held=16793600 bytes (16.0 MiB) for 1025 unread payload bytes, heap grew 16961760 bytes
    verify_c2_handler_test.go:95: C2: handler receive path left 1025 backlog entries for 1026 one-byte reads with compaction ENABLED, want <= 64 (the bound the eval fixture applies to its HandlerServerStream subtest)
--- FAIL: TestVerifyC2_HandlerHandleStreams (0.01s)
=== RUN   TestVerifyC2_HandlerOverRealHTTP2
    verify_c2_handler_test.go:92: compaction enabled=true: 1026 one-byte body reads -> backlog entries=1025, entry types=map[*mem.buffer:1025], backing capacity held=16793600 bytes (16.0 MiB) for 1025 unread payload bytes, heap grew 16951040 bytes
    verify_c2_handler_test.go:95: C2: handler receive path left 1025 backlog entries for 1026 one-byte reads with compaction ENABLED, want <= 64 (the bound the eval fixture applies to its HandlerServerStream subtest)
--- FAIL: TestVerifyC2_HandlerOverRealHTTP2 (1.26s)
```

Compaction disabled — identical retention, i.e. the feature changes nothing on this path:

```sh
GRPC_GO_EXPERIMENTAL_ENABLE_RECEIVE_BUFFER_COMPACTION=false go test ./internal/transport -run '^TestVerifyC2_Handler' -count=1 -v
```

```console
=== RUN   TestVerifyC2_HandlerHandleStreams
    verify_c2_handler_test.go:92: compaction enabled=false: 1026 one-byte body reads -> backlog entries=1025, entry types=map[*mem.buffer:1025], backing capacity held=16793600 bytes (16.0 MiB) for 1025 unread payload bytes, heap grew 16967120 bytes
--- PASS: TestVerifyC2_HandlerHandleStreams (0.01s)
=== RUN   TestVerifyC2_HandlerOverRealHTTP2
    verify_c2_handler_test.go:92: compaction enabled=false: 1026 one-byte body reads -> backlog entries=1025, entry types=map[*mem.buffer:1025], backing capacity held=16793600 bytes (16.0 MiB) for 1025 unread payload bytes, heap grew 16951264 bytes
--- PASS: TestVerifyC2_HandlerOverRealHTTP2 (1.26s)
PASS
ok  	google.golang.org/grpc/internal/transport	1.276s
```

Stability: `go test ./internal/transport -run '^TestVerifyC2_' -count=3 2>&1 | grep -c "C2:"` printed `9` (3 failing assertions × 3 runs).

### The eval fixture does not see it

The fixture's `TestEval_RecvBufferCompaction/HandlerServerStream` subtest calls `s.buf.put(recvMsg{buffer: mem.Copy(payload, pool)})` with a one-byte payload from inside the `HandleStreams` callback — a `mem.SliceBuffer`, not what the handler's reader goroutine produces. On this branch it stays green:

```sh
cp /home/ubuntu/eval/tests/eval_recv_buffer_compaction_test.go internal/transport/eval_recv_buffer_compaction_test.go
go test -v -run '^TestEval_RecvBufferCompaction$' google.golang.org/grpc/internal/transport -race -count=1
```

```console
=== RUN   TestEval_RecvBufferCompaction
=== RUN   TestEval_RecvBufferCompaction/HandlerServerStream
--- PASS: TestEval_RecvBufferCompaction (0.00s)
    --- PASS: TestEval_RecvBufferCompaction/HandlerServerStream (0.00s)
PASS
ok  	google.golang.org/grpc/internal/transport	1.016s
```

(For completeness, of the eight `TestEval_*` commands only `TestEval_RecvBufferConfiguredPoolAcquisition` fails on this branch: `eval_recv_buffer_compaction_test.go:700: Compaction destination buffer was never acquired from the configured buffer pool` — a different property from C2.)

### Comparison: same handler test on the audited reference branch

```sh
cd ~/repos/grpc-go   # verify/... branch = origin/grpc-go-transport-restrict-memory-overhead-perfect + verify/
cp verify/repro/c2_handler_short_reads_test.go.txt internal/transport/verify_c2_handler_test.go
go test ./internal/transport -run '^TestVerifyC2_Handler' -count=1 -v; rm internal/transport/verify_c2_handler_test.go
```

```console
=== RUN   TestVerifyC2_HandlerHandleStreams
    verify_c2_handler_test.go:92: compaction enabled=true: 1026 one-byte body reads -> backlog entries=1, entry types=map[*mem.buffer:1], backing capacity held=4096 bytes (0.0 MiB) for 1025 unread payload bytes, heap grew 81776 bytes
--- PASS: TestVerifyC2_HandlerHandleStreams (0.02s)
=== RUN   TestVerifyC2_HandlerOverRealHTTP2
    verify_c2_handler_test.go:92: compaction enabled=true: 1026 one-byte body reads -> backlog entries=1, entry types=map[*mem.buffer:1], backing capacity held=4096 bytes (0.0 MiB) for 1025 unread payload bytes, heap grew 72400 bytes
--- PASS: TestVerifyC2_HandlerOverRealHTTP2 (1.34s)
PASS
ok  	google.golang.org/grpc/internal/transport	1.365s
```

So the bound is achievable on this path; the test is not demanding something the handler transport cannot do.

### Impact reasoning

- Who is affected: gRPC servers served through `grpc.Server.ServeHTTP` (the `net/http` handler transport), when a client uploads many tiny DATA frames and the application reads slowly — the exact scenario of the task, on one of the three transports in `internal/transport`.
- What they get: with compaction enabled, 1026 unread payload bytes are held in 1025 backlog entries pinning 16,793,600 bytes of pooled 16 KiB buffers (measured heap growth ≈ 16.95 MB), byte-for-byte the same as with `GRPC_GO_EXPERIMENTAL_ENABLE_RECEIVE_BUFFER_COMPACTION=false`. That is ~16 KiB retained per unread byte; the reference implementation holds 4096 bytes for the same input.
- Correctness is not affected: every byte is delivered in order.
- Scope limits: the HTTP/2 client and server transports were not the subject of this claim and were not measured here; the retention on the handler path also exists before the change (the disabled run), so this is a gap in the fix's coverage rather than a regression.
- Why it went unnoticed: the branch's own tests (`internal/transport/recv_buffer_test.go`) have no handler-transport case (`grep -n "HandleStreams\|newHandleStreamTest" internal/transport/recv_buffer_test.go` prints nothing), and the eval fixture feeds the handler stream `mem.SliceBuffer` inputs directly, never the pooled buffers the handler's reader goroutine really produces.
- Workaround: none via configuration; the application reading promptly avoids the backlog.
