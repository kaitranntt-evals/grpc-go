Observations collected while auditing `grpc-go-server-unify-unary-stream-rpc-perfect`
(HEAD `a46198c6`, base `0c51461d`) and the claim-target branches. Every section is
self-contained: commands, key output lines (verbatim, `tlogger` noise removed) and,
for confirmed claims, the impact reasoning. Go 1.25.7 on Ubuntu Linux. Each target
branch was checked out in its own worktree under `~/wt/<suffix>` from
`https://github.com/kaitranntt-evals/grpc-go-server-unify-unary-stream-rpc`
(`~/wt/base` is `0c51461d`). The exact fixtures from `eval_tests.zip` were extracted
to `~/eval_tests/tests/` and copied byte-exact into the package directories before
each fixture run, then removed.

## C1

Claim: a changed raw-frame RPC test repeatedly reads frames until a matching trailer
arrives without a finite read bound. Adjudicated per branch. The changed test on all
three branches is `testClientRequestBodyErrorUnexpectedEOF` in `test/end2end_test.go`;
the two helpers it can use live in `test/servertester.go`, which is byte-identical to
base on all three branches (`git diff 0c51461d HEAD --stat -- test/servertester.go`
prints nothing):

```go
// test/servertester.go:70  (bounded: 2s timer)
func (st *serverTester) readFrame() (http2.Frame, error) {
	go func() { fr, err := st.fr.ReadFrame(); ... }()
	t := time.NewTimer(2 * time.Second)
	defer t.Stop()
	select {
	case f := <-st.frc:      return f, nil
	case err := <-st.frErrc: return nil, err
	case <-t.C:              return nil, errors.New("timeout waiting for frame")
	}
}
// test/servertester.go:207  (unbounded: blocks in Framer.ReadFrame)
func (st *serverTester) wantAnyFrame() http2.Frame {
	f, err := st.fr.ReadFrame()
	if err != nil { st.t.Fatal(err) }
	return f
}
```

Loop shape on each branch (`git show HEAD:test/end2end_test.go`):

- `evalon/grpc-go-se-91d5a035` (HEAD `d1d8f876`): `for { f, err := st.readFrame(); if err != nil { t.Fatalf("%s: reading status frame: %v", ...) } ... }` — uses the 2 s-bounded helper.
- `evalon/grpc-go-se-3c6bea32` (HEAD `c2dc0488`): `for { switch frame := st.wantAnyFrame().(type) { ... } }` — the nearest equivalent of the claim's `st.readFrame()` on this branch is `st.wantAnyFrame()`, which has no bound.
- `evalon/grpc-go-se-0649e9b2` (HEAD `af3a5ca3`): `for { frame := st.wantAnyFrame(); ... }` — same unbounded helper.

Missing-trailer probe: in each worktree the streaming handler of the changed test was
replaced by one that blocks on a channel closed in `t.Cleanup` (so the peer never sends
the trailer for stream 1), and a `t.Logf` was added inside the loop. Exact instrumentation
diffs (applied with `git apply`, worktree-only, not committed):

```diff
# evalon/grpc-go-se-91d5a035
+	c1Unblock := make(chan struct{})
+	t.Cleanup(func() { close(c1Unblock) })
 	}, streamingInputCall: func(stream testgrpc.TestService_StreamingInputCallServer) error {
-		if _, err := stream.Recv(); err != nil {
-			return err
-		}
-		t.Error("received a truncated request without an error")
+		t.Logf("C1 probe: handler blocking without Recv so the peer never sends the trailer")
+		<-c1Unblock
 		return nil
 	}}
@@
 				if err != nil {
 					t.Fatalf("%s: reading status frame: %v", tc.method, err)
 				}
+				t.Logf("C1 probe: %s got frame %T %+v at %s", tc.method, f, f, time.Now().Format(time.RFC3339Nano))
```

```diff
# evalon/grpc-go-se-3c6bea32
+	c1Unblock := make(chan struct{})
+	t.Cleanup(func() { close(c1Unblock) })
 		streamingInputCall: func(stream testgrpc.TestService_StreamingInputCallServer) error {
-			err := stream.RecvMsg(&testpb.StreamingInputCallRequest{})
-			if err == nil {
-				t.Error("RecvMsg accepted a truncated request")
-			}
-			return err
+			t.Logf("C1 probe: handler blocking without Recv so the peer never sends the trailer")
+			<-c1Unblock
+			return nil
 		},
@@
 			for {
+				t.Logf("C1 probe: %s calling wantAnyFrame at %s", tc.method, time.Now().Format(time.RFC3339Nano))
 				switch frame := st.wantAnyFrame().(type) {
```

```diff
# evalon/grpc-go-se-0649e9b2
+	c1Unblock := make(chan struct{})
+	t.Cleanup(func() { close(c1Unblock) })
 		fullDuplexCall: func(stream testgrpc.TestService_FullDuplexCallServer) error {
-			_, err := stream.Recv()
-			return err
+			t.Logf("C1 probe: handler blocking without Recv so the peer never sends the trailer")
+			<-c1Unblock
+			return nil
 		},
@@
 			for {
+				t.Logf("C1 probe: %s calling wantAnyFrame at %s", tc.method, time.Now().Format(time.RFC3339Nano))
 				frame := st.wantAnyFrame()
```

Command (same in each worktree; 20 s is well above the test's intended duration):

```sh
go test ./test -run '^Test$/^ClientRequestBodyErrorUnexpectedEOF$' -count=1 -v -timeout 20s
```

`evalon/grpc-go-se-91d5a035` — loop terminates after the helper's 2 s bound (REFUTED there):

```console
    end2end_test.go:4560: C1 probe: StreamingInputCall got frame *http2.WindowUpdateFrame [FrameHeader WINDOW_UPDATE len=4] at 2026-09-11T04:08:52.794248503Z
    end2end_test.go:4560: C1 probe: StreamingInputCall got frame *http2.PingFrame [FrameHeader PING len=8] at 2026-09-11T04:08:52.794271417Z
    end2end_test.go:4558: StreamingInputCall: reading status frame: timeout waiting for frame
--- FAIL: Test (2.01s)
    --- FAIL: Test/ClientRequestBodyErrorUnexpectedEOF (2.00s)
FAIL	google.golang.org/grpc/test	2.012s
exit=1
```

`evalon/grpc-go-se-3c6bea32` — loop never terminates; the process is killed by the
`go test` alarm with the loop parked inside `wantAnyFrame` → `Framer.ReadFrame` (CONFIRMED there):

```console
    end2end_test.go:4558: C1 probe: StreamingInputCall calling wantAnyFrame at 2026-09-11T04:08:52.786770605Z
panic: test timed out after 20s
golang.org/x/net/http2.(*Framer).ReadFrame(0xc00041c000)
google.golang.org/grpc/test.(*serverTester).wantAnyFrame(0xc0003bba40)
	/home/ubuntu/wt/3c6bea32/test/servertester.go:208 +0x25
FAIL	google.golang.org/grpc/test	20.057s
exit=1
```

`evalon/grpc-go-se-0649e9b2` — same unbounded behaviour (CONFIRMED there):

```console
    end2end_test.go:4558: C1 probe: FullDuplexCall calling wantAnyFrame at 2026-09-11T04:08:52.789938335Z
panic: test timed out after 20s
golang.org/x/net/http2.(*Framer).ReadFrame(0xc00041e0e0)
google.golang.org/grpc/test.(*serverTester).wantAnyFrame(0xc0004187e0)
	/home/ubuntu/wt/0649e9b2/test/servertester.go:208 +0x25
FAIL	google.golang.org/grpc/test	20.056s
exit=1
```

Controlled repro `verify/repro/c1_unbounded_frame_read_loop_test.go` (does not rely on the
process-level test timeout: it runs both loop shapes against a handler that withholds the
trailer, dumps the reader's stack after 5 s and then releases the handler):

```sh
cp verify/repro/c1_unbounded_frame_read_loop_test.go test/ && go test -tags verifyrepro ./test -run '^Test$/^C1Repro' -count=1 -v; rm test/c1_unbounded_frame_read_loop_test.go
```

Output on the audited branch (identical shape in `~/wt/3c6bea32`, `exit=1`):

```console
    c1_unbounded_frame_read_loop_test.go:88: loop terminated after 2.009402752s: readFrame returned error: timeout waiting for frame
    c1_unbounded_frame_read_loop_test.go:96: blocked reader: golang.org/x/net/http2.(*Framer).ReadFrame(0xc0000000e0)
    c1_unbounded_frame_read_loop_test.go:96: blocked reader: /home/ubuntu/repos/grpc-go/test/servertester.go:208 +0x25
    c1_unbounded_frame_read_loop_test.go:101: C1 CONFIRMED: frame read loop still blocked 5s after the last frame; no finite read bound
    c1_unbounded_frame_read_loop_test.go:103: loop terminated after 5.024821537s: trailer for stream 1 received
--- FAIL: Test/C1Repro_FrameReadLoopBound (7.04s)
FAIL	google.golang.org/grpc/test	7.045s
```

Impact (3c6bea32, 0649e9b2): the test's only termination path for a missing or
mismatched trailer is the whole-package `go test` timeout (10 min by default), which
kills every other test in `./test` with it and reports a panic stack instead of a
targeted failure; a server regression that drops the trailer for a truncated request
turns into a CI hang rather than an assertion. No user-facing impact — the production
server is unchanged by this test — but it is a real test-suite robustness defect.
Verdict: 91d5a035 REFUTED, 3c6bea32 CONFIRMED, 0649e9b2 CONFIRMED.

## C2

Claim: when writing a successful terminal status fails, deferred RPC statistics do not
receive the transport write error. Target: `evalon/grpc-go-se-cb6444a3` (HEAD `92dd62cb`,
worktree `~/wt/cb6444a3`).

Source on the target: `processRPC` returns the status-write error through its named return
and the deferred stats handler converts that named return:

```console
$ grep -n "end.Error = toRPCErr(err)\|return ss.s.WriteStatus(statusOK)" server.go
server.go:1337:	end.Error = toRPCErr(err)
server.go:1491:	return ss.s.WriteStatus(statusOK)
$ grep -n -A1 "case transport.ConnectionError:" rpc_util.go
rpc_util.go:1149:	case transport.ConnectionError:
rpc_util.go:1150:		return status.Error(codes.Unavailable, e.Desc)
```

Exact fixture (`~/eval_tests/tests/eval_final_status_write_test.go` copied to `test/`).
Its helper `runFinalStatusWriteFailure` closes the transport before the handler returns so
`WriteStatus(statusOK)` fails with `transport.ConnectionError`, and the accounting test
asserts `stats.End.Error != nil`:

```sh
go test -v ./test -run '^TestEval_FinalStatusWriteFailureAccounting$' -count=1
```

```console
--- PASS: TestEval_FinalStatusWriteFailureAccounting (0.00s)
ok  	google.golang.org/grpc/test	0.009s
exit=0
```

Direct probe of the exact error value (worktree-only file `test/zz_c2_probe_test.go`
that calls the fixture's helper and prints `stats.End.Error`):

```go
func (s) TestC2Probe_FinalStatusWriteError(t *testing.T) {
	end := runFinalStatusWriteFailure(t, nil)
	t.Logf("C2 probe: stats.End.Error = %#v", end.Error)
	t.Logf("C2 probe: stats.End.Error.Error() = %q", errString(end.Error))
}
```

```sh
go test -v ./test -run '^Test$/^C2Probe_FinalStatusWriteError$' -count=1
```

```console
    zz_c2_probe_test.go:11: C2 probe: stats.End.Error = &status.Error{s:(*status.Status)(0xc0003b8058)}
    zz_c2_probe_test.go:12: C2 probe: stats.End.Error.Error() = "rpc error: code = Unavailable desc = transport is closing"
--- PASS: Test/C2Probe_FinalStatusWriteError
ok  	google.golang.org/grpc/test
```

The same probe on base `0c51461d` prints the identical value
(`"rpc error: code = Unavailable desc = transport is closing"`), so the target preserves
the pre-existing accounting behaviour. The deferred `stats.End` receives the transport
write error (`transport is closing`, mapped to `codes.Unavailable` exactly as on base),
not nil and not an unrelated error. Verdict: REFUTED.

Side observation (not this claim): on the target the fixture
`Test/Eval_FinalStatusWriteFailureDiagnostics` fails with
`tlogger.go:212: Expected warning 'failed to write status' not encountered` — the
target reports the failure through stats but no longer logs the warning. It also fails on
base for the same reason.

## C3

Claim: unary dispatch waits for client half-close after receiving one complete unary
request. Audited branch `grpc-go-server-unify-unary-stream-rpc-perfect` (`a46198c6`),
compared with base `0c51461d` in `~/wt/base`.

Source: unary methods run through a stream adapter whose decode function calls
`serverStream.RecvMsg`; for non-client-streaming descriptors `RecvMsg` performs a second
`recv` (expecting `io.EOF`) before returning the first message:

```go
// server.go:1277
func (s *Server) wrapUnaryHandler(md *MethodDesc) StreamHandler {
	return func(srv any, stream ServerStream) error {
		df := func(v any) error { return stream.RecvMsg(v) }
		reply, err := md.Handler(srv, stream.Context(), df, s.opts.unaryInt)
		...
// stream.go:1953-1964 (inside serverStream.RecvMsg, after the first recv succeeded)
	if ss.desc.ClientStreams {
		return nil
	}
	// Special handling for non-client-stream rpcs.
	// This recv expects EOF or errors, so we don't collect inPayload.
	if err := recv(&ss.p, ss.codec, ss.s, ss.decompressorV0, m, ss.maxReceiveMessageSize, nil, ss.decompressorV1, true, ss.unmarshalErrorDescription()); err == io.EOF {
		return nil
	} else if err != nil {
		return err
	}
	return status.Error(codes.Internal, "cardinality violation: received multiple request messages for non-client-streaming RPC")
```

Probe `verify/repro/c3_c5_unary_dispatch_waits_for_half_close_test.go`: a raw HTTP/2
client (`serverTester`) sends HEADERS + one complete DATA frame for `UnaryCall`
*without* END_STREAM, records when the unary handler runs and when response trailers
arrive, dumps the server goroutine stack after a 3 s client deadline, then sends
END_STREAM (half-close).

```sh
cp verify/repro/c3_c5_unary_dispatch_waits_for_half_close_test.go test/ && go test -tags verifyrepro ./test -run '^Test$/^C3C5Repro' -count=1 -v; rm test/c3_c5_unary_dispatch_waits_for_half_close_test.go
```

Audited branch:

```console
    c3_c5_unary_dispatch_waits_for_half_close_test.go:75: before half-close: handler dispatched=false, response trailers received=false (client deadline 3s)
    c3_c5_unary_dispatch_waits_for_half_close_test.go:86: server goroutine: google.golang.org/grpc.(*parser).recvMsg(0xc00036a018, 0x400000)
    c3_c5_unary_dispatch_waits_for_half_close_test.go:86: server goroutine: google.golang.org/grpc.recvAndDecompress(...)
    c3_c5_unary_dispatch_waits_for_half_close_test.go:86: server goroutine: google.golang.org/grpc.recv(...)
    c3_c5_unary_dispatch_waits_for_half_close_test.go:86: server goroutine: /home/ubuntu/repos/grpc-go/rpc_util.go:1054 +0xab
    c3_c5_unary_dispatch_waits_for_half_close_test.go:86: server goroutine: google.golang.org/grpc.(*serverStream).RecvMsg(0xc00036a000, {0xe8ac20, 0xc000356070})
    c3_c5_unary_dispatch_waits_for_half_close_test.go:86: server goroutine: /home/ubuntu/repos/grpc-go/stream.go:1959 +0x7c5
    c3_c5_unary_dispatch_waits_for_half_close_test.go:86: server goroutine: google.golang.org/grpc.(*Server).register.(*Server).wrapUnaryHandler.func1.1({0xe8ac20?, 0xc000356070?})
    c3_c5_unary_dispatch_waits_for_half_close_test.go:86: server goroutine: /home/ubuntu/repos/grpc-go/server.go:1279 +0x2f
    c3_c5_unary_dispatch_waits_for_half_close_test.go:94: client half-closed at +3.01630886s
    c3_c5_unary_dispatch_waits_for_half_close_test.go:100: unary handler dispatched at +3.016580085s (after half-close)
    c3_c5_unary_dispatch_waits_for_half_close_test.go:103: response trailers received at +3.016752881s (after half-close)
    c3_c5_unary_dispatch_waits_for_half_close_test.go:111: C3/C5 CONFIRMED: unary dispatch waited for client half-close (handler dispatched before half-close=false, response before half-close=false; base commit gives true, true)
--- FAIL: Test/C3C5Repro_UnaryDispatchWaitsForHalfClose (3.02s)
FAIL	google.golang.org/grpc/test	3.025s
```

`stream.go:1959` is the second `recv(...)` shown above, reached from the unary decode
function (`server.go:1279`) — i.e. the adapted receive path requests another inbound
message before returning the first complete request (receive-lifecycle part), and no
response is produced until the client half-closes (client-interaction part).

Base `0c51461d` (`~/wt/base`, same command):

```console
    c3_c5_unary_dispatch_waits_for_half_close_test.go:64: unary handler dispatched at +1.312869ms while client send side still open
    c3_c5_unary_dispatch_waits_for_half_close_test.go:68: response trailers received at +1.461583ms while client send side still open
    c3_c5_unary_dispatch_waits_for_half_close_test.go:75: before half-close: handler dispatched=true, response trailers received=true (client deadline 3s)
    c3_c5_unary_dispatch_waits_for_half_close_test.go:94: client half-closed at +1.542294ms
--- PASS: Test/C3C5Repro_UnaryDispatchWaitsForHalfClose (0.05s)
ok  	google.golang.org/grpc/test	0.061s
```

Impact: a behavioural change relative to base for any unary client that sends its one
request and defers END_STREAM (grpc-go's own client half-closes immediately, so the
stock Go client is unaffected; hand-rolled HTTP/2 clients, proxies that forward
END_STREAM late, or clients that pipeline the half-close after the request body are
affected). For such peers a unary call that answered in ~1 ms on base now sits until
half-close or the client deadline, then fails with `DeadlineExceeded` from the client's
perspective. Workaround: send END_STREAM with (or immediately after) the request DATA
frame. Both parts of the claim held. Verdict: CONFIRMED.

## C4

Claim: a unary response send that fails before payload writing omits the empty
`ServerHeader` binary-log event before `ServerTrailer`. Target: `evalon/grpc-go-se-0fb77679`
(HEAD `9dd16cce`, worktree `~/wt/0fb77679`), compared with base `0c51461d`.

Source on the target: `serverStream.SendMsg` returns the max-size error before `ss.s.Write`
and before `ss.logServerHeader()`; `processRPC` then only logs `ServerHeader` when headers
were explicitly set, so a trailers-only failure has no `SERVER_HEADER` event:

```go
// stream.go (serverStream.SendMsg)
	if payloadLen > ss.maxSendMessageSize {
		format := "trying to send message larger than max (%d vs. %d)"
		if ss.isUnary {
			format = "grpc: " + format
		}
		return status.Errorf(codes.ResourceExhausted, format, payloadLen, ss.maxSendMessageSize)
	}
	if err := ss.s.Write(hdr, payload, &transport.WriteOptions{Last: false}); err != nil {
		return toRPCErr(err)
	}
	if len(ss.binlogs) != 0 {
		ss.logServerHeader()
// server.go (processRPC, after the handler returned)
	if len(ss.binlogs) != 0 {
		if h, _ := stream.Header(); h.Len() > 0 {
			ss.logServerHeader()
		}
		st := &binarylog.ServerTrailer{Trailer: stream.Trailer(), Err: appErr}
```

Base `processUnaryRPC`, same situation (`sendResponse` fails), always logs the pair:

```go
		if len(binlogs) != 0 {
			h, _ := stream.Header()
			sh := &binarylog.ServerHeader{Header: h}
			st := &binarylog.ServerTrailer{Trailer: stream.Trailer(), Err: appErr}
			for _, binlog := range binlogs {
				binlog.Log(ctx, sh)
				binlog.Log(ctx, st)
			}
		}
```

Probe `verify/repro/c4_binlog_missing_empty_server_header_test.go`: enables the binary
logger with a capturing sink, sets `grpc.MaxSendMsgSize(8)` on the server, calls
`UnaryCall` whose reply is 68 bytes (send fails in max-size validation, before any payload
write), then prints the server-side event sequence.

```sh
cp verify/repro/c4_binlog_missing_empty_server_header_test.go binarylog/ && go test -tags verifyrepro ./binarylog -run '^Test$/^C4Repro' -count=1 -v; rm binarylog/c4_binlog_missing_empty_server_header_test.go
```

Target `evalon/grpc-go-se-0fb77679`:

```console
    c4_binlog_missing_empty_server_header_test.go:38: C4: UnaryCall err = rpc error: code = ResourceExhausted desc = grpc: trying to send message larger than max (68 vs. 8)
    c4_binlog_missing_empty_server_header_test.go:52: C4: SERVER_TRAILER status code = 8 msg = "grpc: trying to send message larger than max (68 vs. 8)"
    c4_binlog_missing_empty_server_header_test.go:55: C4: server binlog sequence = [EVENT_TYPE_CLIENT_HEADER EVENT_TYPE_CLIENT_MESSAGE EVENT_TYPE_SERVER_TRAILER]
    c4_binlog_missing_empty_server_header_test.go:56: C4: empty SERVER_HEADER event present = false
    c4_binlog_missing_empty_server_header_test.go:58: C4 CONFIRMED: no empty SERVER_HEADER event before SERVER_TRAILER; sequence = [EVENT_TYPE_CLIENT_HEADER EVENT_TYPE_CLIENT_MESSAGE EVENT_TYPE_SERVER_TRAILER]
--- FAIL: Test/C4Repro_UnarySendFailurePrePayloadServerHeaderBinlog (0.20s)
FAIL	google.golang.org/grpc/binarylog	0.207s
```

Base `0c51461d` (`~/wt/base`, same command):

```console
    c4_binlog_missing_empty_server_header_test.go:38: C4: UnaryCall err = rpc error: code = ResourceExhausted desc = grpc: trying to send message larger than max (68 vs. 8)
    c4_binlog_missing_empty_server_header_test.go:46: C4: SERVER_HEADER metadata entries = 0
    c4_binlog_missing_empty_server_header_test.go:52: C4: SERVER_TRAILER status code = 0 msg = ""
    c4_binlog_missing_empty_server_header_test.go:55: C4: server binlog sequence = [EVENT_TYPE_CLIENT_HEADER EVENT_TYPE_CLIENT_MESSAGE EVENT_TYPE_SERVER_HEADER EVENT_TYPE_SERVER_TRAILER]
    c4_binlog_missing_empty_server_header_test.go:56: C4: empty SERVER_HEADER event present = true
--- PASS: Test/C4Repro_UnarySendFailurePrePayloadServerHeaderBinlog (0.20s)
ok  	google.golang.org/grpc/binarylog	0.207s
```

Impact: the wire-visible RPC outcome is identical (client gets `ResourceExhausted`), but the
server-side binary log for a unary RPC whose response fails max-size validation changes
shape from `CLIENT_HEADER, CLIENT_MESSAGE, SERVER_HEADER, SERVER_TRAILER` to
`CLIENT_HEADER, CLIENT_MESSAGE, SERVER_TRAILER`. Any binlog consumer that pairs
`SERVER_HEADER` with `SERVER_TRAILER` per unary RPC (or diffs logs across versions) sees a
missing event for this path. Triggering situation: a unary handler returning a reply larger
than `MaxSendMsgSize` with binary logging enabled — an ordinary misconfiguration, not a
contrived one. Note the target's trailer carries the real status (code 8) whereas base
logged code 0 for the same failure; that part is an improvement, but the header event
regression stands. Verdict: CONFIRMED.

## C5

Claim: the server does not dispatch a unary handler after one complete request until the
client half-closes its send side. Audited branch `grpc-go-server-unify-unary-stream-rpc-perfect`
(`a46198c6`), compared with base `0c51461d` in `~/wt/base`. Same probe as C3 — the
artifact is shared because the single run observes both the server receive behaviour
(part 1) and the open-send-side interaction (part 2).

Source: the unary adapter's decode function calls `serverStream.RecvMsg`, which for
non-client-streaming descriptors performs a second `recv` (expecting EOF) before returning
the first message to the handler:

```go
// server.go:1277
func (s *Server) wrapUnaryHandler(md *MethodDesc) StreamHandler {
	return func(srv any, stream ServerStream) error {
		df := func(v any) error { return stream.RecvMsg(v) }
		reply, err := md.Handler(srv, stream.Context(), df, s.opts.unaryInt)
// stream.go:1959 (serverStream.RecvMsg, non-ClientStreams branch)
	if err := recv(&ss.p, ss.codec, ss.s, ss.decompressorV0, m, ss.maxReceiveMessageSize, nil, ss.decompressorV1, true, ss.unmarshalErrorDescription()); err == io.EOF {
		return nil
```

```sh
cp verify/repro/c3_c5_unary_dispatch_waits_for_half_close_test.go test/ && go test -tags verifyrepro ./test -run '^Test$/^C3C5Repro' -count=1 -v; rm test/c3_c5_unary_dispatch_waits_for_half_close_test.go
```

Audited branch — after HEADERS + one complete DATA frame (no END_STREAM) the server
goroutine is parked in the second receive and the handler has not run; it runs only after
the client half-closes at the 3 s deadline:

```console
    c3_c5_unary_dispatch_waits_for_half_close_test.go:75: before half-close: handler dispatched=false, response trailers received=false (client deadline 3s)
    c3_c5_unary_dispatch_waits_for_half_close_test.go:86: server goroutine: google.golang.org/grpc.(*parser).recvMsg(0xc00036a018, 0x400000)
    c3_c5_unary_dispatch_waits_for_half_close_test.go:86: server goroutine: google.golang.org/grpc.recv(...)
    c3_c5_unary_dispatch_waits_for_half_close_test.go:86: server goroutine: /home/ubuntu/repos/grpc-go/rpc_util.go:1054 +0xab
    c3_c5_unary_dispatch_waits_for_half_close_test.go:86: server goroutine: google.golang.org/grpc.(*serverStream).RecvMsg(0xc00036a000, {0xe8ac20, 0xc000356070})
    c3_c5_unary_dispatch_waits_for_half_close_test.go:86: server goroutine: /home/ubuntu/repos/grpc-go/stream.go:1959 +0x7c5
    c3_c5_unary_dispatch_waits_for_half_close_test.go:86: server goroutine: google.golang.org/grpc.(*Server).register.(*Server).wrapUnaryHandler.func1.1({0xe8ac20?, 0xc000356070?})
    c3_c5_unary_dispatch_waits_for_half_close_test.go:86: server goroutine: /home/ubuntu/repos/grpc-go/server.go:1279 +0x2f
    c3_c5_unary_dispatch_waits_for_half_close_test.go:94: client half-closed at +3.01630886s
    c3_c5_unary_dispatch_waits_for_half_close_test.go:100: unary handler dispatched at +3.016580085s (after half-close)
    c3_c5_unary_dispatch_waits_for_half_close_test.go:103: response trailers received at +3.016752881s (after half-close)
    c3_c5_unary_dispatch_waits_for_half_close_test.go:111: C3/C5 CONFIRMED: unary dispatch waited for client half-close (handler dispatched before half-close=false, response before half-close=false; base commit gives true, true)
--- FAIL: Test/C3C5Repro_UnaryDispatchWaitsForHalfClose (3.02s)
FAIL	google.golang.org/grpc/test	3.025s
```

Base `0c51461d` — handler dispatched and response returned ~1 ms after the request while
the client's send side is still open:

```console
    c3_c5_unary_dispatch_waits_for_half_close_test.go:64: unary handler dispatched at +1.312869ms while client send side still open
    c3_c5_unary_dispatch_waits_for_half_close_test.go:68: response trailers received at +1.461583ms while client send side still open
    c3_c5_unary_dispatch_waits_for_half_close_test.go:75: before half-close: handler dispatched=true, response trailers received=true (client deadline 3s)
--- PASS: Test/C3C5Repro_UnaryDispatchWaitsForHalfClose (0.05s)
ok  	google.golang.org/grpc/test	0.061s
```

The exact fixture `eval_unary_handler_eof_test.go` (`go test -v ./test -run
'^TestEval_UnaryHandlerEOFStatus$' -count=1`) passes on both the audited branch and base
(`ok  	google.golang.org/grpc/test`); it exercises the EOF-without-message case and does
not cover the open-send-side interaction.

Impact: unary handler dispatch is now gated on client half-close. With grpc-go's own
client (which half-closes immediately) nothing changes; a raw HTTP/2 or third-party client
that sends the request and keeps its send side open until it has a response deadlocks
until its deadline and gets no reply. Server-side handler latency, per-RPC timeouts and
interceptor timing observed by such peers all shift by the half-close delay. Workaround:
clients must send END_STREAM with the request. Both parts held. Verdict: CONFIRMED.

## C6

Claim: the candidate-test runner selects packages from changed test locations and can omit
package suites affected by changes to `server.go`, `stream.go`, or `rpc_util.go`. Target:
`evalon/grpc-go-se-c075c936` (HEAD `74a82587`, worktree `~/wt/c075c936`). Fixture runner:
`~/eval_tests/tests/run_candidate_tests.sh` (+ `candidate_test_inventory.go`).

Selection logic in the fixture runner (only `*_test.go` paths are consulted; a package is
selected because a changed test file lives in it):

```console
$ grep -n "git diff\|git ls-files\|dirname\|BASE_COMMIT=" ~/eval_tests/tests/run_candidate_tests.sh
5:BASE_COMMIT="0c51461d27177d997e14c642fe18c11668fc09a3"
31:  git diff --name-only --diff-filter=ACMR "$BASE_COMMIT" HEAD -- "*_test.go" "**/*_test.go"
32:  git diff --name-only --diff-filter=ACMR HEAD -- "*_test.go" "**/*_test.go"
33:  git ls-files --others --exclude-standard -- "*_test.go" "**/*_test.go"
52:  dir=$(dirname "$abs_f")
```

Instrumented run, `verify/repro/c6_candidate_runner_omits_affected_packages.sh`, which
lists the production files changed on the target, re-executes the runner's own three git
commands, derives the selected packages, lists the affected packages with existing suites
that are not selected, and finally runs the exact fixture runner:

```sh
cd ~/wt/c075c936 && bash ~/repos/grpc-go/verify/repro/c6_candidate_runner_omits_affected_packages.sh ~/eval_tests/tests
```

```console
==> HEAD: 74a82587
==> production (non-test) files changed since BASE_COMMIT:
    rpc_util.go
    server.go
    stream.go
==> test files the runner looks at (same git commands as run_candidate_tests.sh):
    test/server_pipeline_test.go
==> packages the runner would select (directory of each changed test file):
    ./test
==> affected packages with existing suites that are NOT selected:
    .  (omitted; 95 test functions in package)
    ./encoding  (omitted; 11 test functions in package)
    ./binarylog  (omitted; 19 test functions in package)
==> running the real fixture runner from /home/ubuntu/eval_tests/tests
==> Detected candidate test packages:
    test/server_pipeline_test.go	@package
==> Running candidate test suite in module /home/ubuntu/wt/c075c936 for package ./test
[PASS] package ./test (all tests passed)
exit=0
```

Impact: the change set on this branch rewrites the root package's server receive/send path
(`server.go`, `stream.go`, `rpc_util.go`) but authors its tests only under `test/`, so the
runner executes `./test` alone and never runs the root package suite (95 tests, including
`server_test.go`/`stream_test.go` unit tests of the very code changed), `./encoding`
(compressor/max-size behaviour) or `./binarylog` (event-sequence tests). A regression in any
of those suites would be reported as `[PASS]` by the candidate runner. This is the ordinary
shape of a change on this task (production change in root, tests in `test/`), not a
contrived layout. Verdict: CONFIRMED.

## C7

Claim: an oversized streaming response returns a `ResourceExhausted` description with the
unary-only `"grpc:"` prefix. Target: `evalon/grpc-go-se-97870765` (HEAD `bb3cb8d9`,
worktree `~/wt/97870765`), compared with base `0c51461d`.

Source on the target — `serverStream.SendMsg` unconditionally uses the prefixed text
(`stream.go:1837`), whereas the client-side stream paths keep the legacy unprefixed text:

```console
$ grep -n "larger than max" stream.go
stream.go:1076:		return status.Errorf(codes.ResourceExhausted, "trying to send message larger than max (%d vs. %d)", payloadLen, *cs.callInfo.maxSendMessageSize)
stream.go:1571:		return status.Errorf(codes.ResourceExhausted, "trying to send message larger than max (%d vs. %d)", payload.Len(), *as.callInfo.maxSendMessageSize)
stream.go:1837:		return status.Errorf(codes.ResourceExhausted, "grpc: trying to send message larger than max (%d vs. %d)", payloadLen, ss.maxSendMessageSize)
```

Probe `verify/repro/c7_streaming_max_send_grpc_prefix_test.go`: server with
`grpc.MaxSendMsgSize(16)`, a `StreamingOutputCall` handler that sends a 1030-byte message,
and the same for `UnaryCall` for comparison; captures both statuses.

```sh
cp verify/repro/c7_streaming_max_send_grpc_prefix_test.go encoding/ && go test -tags verifyrepro ./encoding -run '^TestC7Repro' -count=1 -v; rm encoding/c7_streaming_max_send_grpc_prefix_test.go
```

Target `evalon/grpc-go-se-97870765`:

```console
    c7_streaming_max_send_grpc_prefix_test.go:50: C7: streaming code=ResourceExhausted desc="grpc: trying to send message larger than max (1030 vs. 16)"
    c7_streaming_max_send_grpc_prefix_test.go:52: C7 CONFIRMED: streaming ResourceExhausted description uses the unary-only "grpc:" prefix: "grpc: trying to send message larger than max (1030 vs. 16)"
    c7_streaming_max_send_grpc_prefix_test.go:55: C7: unary     code=ResourceExhausted desc="grpc: trying to send message larger than max (1030 vs. 16)"
--- FAIL: TestC7Repro_StreamingMaxSendSizeDescription (0.00s)
FAIL	google.golang.org/grpc/encoding	0.005s
```

Base `0c51461d` (`~/wt/base`, same command):

```console
    c7_streaming_max_send_grpc_prefix_test.go:50: C7: streaming code=ResourceExhausted desc="trying to send message larger than max (1030 vs. 16)"
    c7_streaming_max_send_grpc_prefix_test.go:55: C7: unary     code=ResourceExhausted desc="grpc: trying to send message larger than max (1030 vs. 16)"
--- PASS: TestC7Repro_StreamingMaxSendSizeDescription (0.00s)
ok  	google.golang.org/grpc/encoding	0.005s
```

The exact fixture `eval_codec_error_compatibility_test.go` on this target
(`go test -v ./encoding -run '^(TestEval_CodecErrorCompatibility|TestEval_UnaryMaxSendSizeCompatibility)$' -count=1`)
reports `--- FAIL: TestEval_CodecErrorCompatibility` and
`--- FAIL: TestEval_UnaryMaxSendSizeCompatibility` (`exit=1`); both pass on the audited
branch and on base.

Impact: the status *code* is unchanged, so clients that switch on `codes.ResourceExhausted`
are fine, but the status *message* for every oversized streaming server response changes
from `trying to send message larger than max (N vs. M)` to `grpc: trying to send message
larger than max (N vs. M)`. Tests, log scrapers, and alerting rules that match the legacy
streaming wording (the same wording grpc-go clients still emit) break; the behaviour is
triggered by an everyday misconfiguration (`MaxSendMsgSize` below a streamed reply).
Verdict: CONFIRMED.

## C8

Claim: `serverStream.RecvMsg` independently duplicates the shared receive path instead of
delegating decompression, buffer ownership, codec unmarshalling and error conversion to the
existing `recv` helper. Target: `evalon/grpc-go-se-5fd20000` (HEAD `599c012c`, worktree
`~/wt/5fd20000`), compared with base `0c51461d`.

Source on the target — the first receive in `RecvMsg` calls `recvAndDecompress` directly,
frees the buffer itself, calls `ss.codec.Unmarshal` itself and converts errors itself;
only the second (EOF-check) receive goes through `recv`:

```go
// stream.go, serverStream.RecvMsg (first receive)
	d, err := recvAndDecompress(&ss.p, ss.s, ss.decompressorV0, ss.maxReceiveMessageSize, payInfo, ss.decompressorV1, true)
	if err != nil {
		if err == io.EOF {
			...
			if !ss.desc.ClientStreams && !ss.recvFirstMsg {
				return status.Error(codes.Internal, "cardinality violation: received no request message from non-client-streaming RPC")
			}
			return err
		}
		if err == io.ErrUnexpectedEOF {
			err = status.Error(codes.Internal, io.ErrUnexpectedEOF.Error())
		}
		return toRPCErr(err)
	}
	// If the codec wants its own reference to the data, it can get it.
	// Otherwise, always free the buffers.
	defer d.Free()
	if err := ss.codec.Unmarshal(d, m); err != nil {
		return status.Errorf(codes.Internal, "grpc: error unmarshalling request: %v", err)
	}
	...
// second receive (EOF check) does use the helper:
	if err := recv(&ss.p, ss.codec, ss.s, ss.decompressorV0, m, ss.maxReceiveMessageSize, nil, ss.decompressorV1, true); err == io.EOF {
```

Trace probe `verify/repro/c8_recvmsg_bypasses_shared_recv_test.go`: registers a codec whose
`Unmarshal` fails with `c8 decode failure` and captures `runtime.Stack` at the moment the
server calls it, then issues a streaming RPC and prints the status plus the caller chain.

```sh
cp verify/repro/c8_recvmsg_bypasses_shared_recv_test.go encoding/ && go test -tags verifyrepro ./encoding -run '^TestC8Repro' -count=1 -v; rm encoding/c8_recvmsg_bypasses_shared_recv_test.go
```

Target `evalon/grpc-go-se-5fd20000` — `grpc.recv` is absent from the server-side
`Unmarshal` call stack and the error text is the target's own:

```console
    c8_recvmsg_bypasses_shared_recv_test.go:71: C8: streaming Recv status code=Internal desc="grpc: error unmarshalling request: c8 decode failure"
    c8_recvmsg_bypasses_shared_recv_test.go:82: C8: server Unmarshal caller: google.golang.org/grpc.(*serverStream).RecvMsg(0xc000372100, {0xb3c840, 0xc00031aa20})
    c8_recvmsg_bypasses_shared_recv_test.go:82: C8: server Unmarshal caller: google.golang.org/grpc.(*GenericServerStream[...]).Recv(0xc84ce0)
    c8_recvmsg_bypasses_shared_recv_test.go:82: C8: server Unmarshal caller: google.golang.org/grpc.(*Server).processRPC(0xc0000fc6c8, {0xc80a48, 0xc000317170}, 0xc00038e000, 0xc0001f2ed0, 0x11321c0, 0x0, 0x0)
    c8_recvmsg_bypasses_shared_recv_test.go:85: C8: server-side Unmarshal reached through shared grpc.recv helper = false
    c8_recvmsg_bypasses_shared_recv_test.go:87: C8 CONFIRMED: serverStream.RecvMsg unmarshals/converts errors itself (via recv=false, desc="grpc: error unmarshalling request: c8 decode failure") instead of delegating to grpc.recv
--- FAIL: TestC8Repro_ServerStreamRecvMsgBypassesSharedRecv (0.00s)
FAIL	google.golang.org/grpc/encoding	0.006s
```

Base `0c51461d` (`~/wt/base`, same command) — `grpc.recv` is on the stack and the error
text is the helper's:

```console
    c8_recvmsg_bypasses_shared_recv_test.go:71: C8: streaming Recv status code=Internal desc="grpc: failed to unmarshal the received message: c8 decode failure"
    c8_recvmsg_bypasses_shared_recv_test.go:82: C8: server Unmarshal caller: google.golang.org/grpc.recv(0xc00029a878?, {0x700d2a632540, 0xc00011c260}, {0xc7d620?, 0xc0000f6000?}, {0x0?, 0x0?}, {0xb3faa0, 0xc000036360}, 0x400000, ...)
    c8_recvmsg_bypasses_shared_recv_test.go:82: C8: server Unmarshal caller: google.golang.org/grpc.(*serverStream).RecvMsg(0xc0000f8000, {0xb3faa0, 0xc000036360})
    c8_recvmsg_bypasses_shared_recv_test.go:85: C8: server-side Unmarshal reached through shared grpc.recv helper = true
--- PASS: TestC8Repro_ServerStreamRecvMsgBypassesSharedRecv (0.00s)
ok  	google.golang.org/grpc/encoding	0.005s
```

The exact fixture `eval_codec_error_compatibility_test.go` on this target
(`go test -v ./encoding -run '^(TestEval_CodecErrorCompatibility|TestEval_UnaryMaxSendSizeCompatibility)$' -count=1`)
reports `--- FAIL: TestEval_CodecErrorCompatibility` (`exit=1`); it passes on the audited
branch and on base.

Impact: the first receive of every server stream (and, via the unary adapter, every unary
request) now has its own copy of the decompress → free → unmarshal → status-convert
sequence. Observable consequence today: the streaming unmarshal-failure message diverges
from the shared helper's (`grpc: error unmarshalling request: …` vs `grpc: failed to
unmarshal the received message: …`), which is what the compatibility fixture catches.
Structural consequence: future fixes to `recv` (buffer-ownership rules for codecs that keep
a reference, new error mappings, stats accounting) will not apply to the server's first
receive, so the two paths will drift. Ordinary path — every RPC goes through it. Verdict:
CONFIRMED.

## C9

Claim: the added `server_rpc_ext_test.go` and `test/server_pipeline_test.go` suites contain
end-to-end cases that duplicate the setup, stimulus and behavioural assertions of
pre-existing tests. Target: `evalon/grpc-go-se-604865b6` (HEAD `8c21138c`, worktree
`~/wt/604865b6`).

Behaviour matrix (every end-to-end case in the two added files, compared with pre-existing
tests using the same RPC setup and stimulus):

| added case | setup | stimulus | assertion | pre-existing equivalent |
|---|---|---|---|---|
| `test.TestServerPipeline_MaxReceiveMessageSizeAfterDecompression/unary/oversized after decompression` | `stubserver.StubServer` + `grpc.MaxRecvMsgSize` | `UnaryCall` with gzip payload that decompresses above the limit | `status.Code == ResourceExhausted` | `encoding.TestDecompressionExceedsMaxMessageSize` (same setup, stimulus, assertion) — **duplicate** |
| `test.TestServerPipeline_MaxReceiveMessageSizeAfterDecompression/client_streaming/oversized after decompression` | same | `StreamingInputCall`, same payload | `ResourceExhausted` | `grpc.TestServer_RecvLimitAppliesAfterDecompression/client_streaming` in the *same change set* (`server_rpc_ext_test.go`) — duplicate within the added suites |
| `test.TestServerPipeline_MaxReceiveMessageSizeAfterDecompression/*/within limit` | same | payload under limit | `OK` | `grpc.TestServer_RecvLimitAppliesAfterDecompression` within-limit cases (same change set) |
| `grpc.TestServer_RecvLimitAppliesAfterDecompression/{unary,client_streaming,bidirectional_streaming}` | stubserver + `MaxRecvMsgSize` | compressed request over limit | `ResourceExhausted` | `encoding.TestDecompressionExceedsMaxMessageSize` (unary); the other two mirror `test.TestServerPipeline_MaxReceiveMessageSizeAfterDecompression` |
| `test.TestServerPipeline_HandwrittenServiceDesc/unary method wins over stream of the same name` | handwritten `grpc.ServiceDesc` with a unary method and a stream of the same name | invoke the shared name | unary handler ran, unary interceptor only | `grpc.TestServer_HandwrittenServiceDesc//grpc.testing.HandwrittenService/Shared` (same change set) — duplicate |
| `test.TestServerPipeline_HandwrittenServiceDesc/stream desc without streaming flags stays streaming` | same descriptor with a flagless stream | invoke `Flagless` | body `flagless`, stream interceptor ran | `grpc.TestServer_HandwrittenServiceDesc//grpc.testing.HandwrittenService/Flagless` (same change set) — duplicate |
| `grpc.TestServer_RPCKinds_InterceptorsAndMetadata/{unary,client streaming,server streaming,bidirectional streaming}` | stubserver echoing metadata + recording unary/stream interceptors | one RPC of each kind with request metadata | interceptor kind + `StreamServerInfo{FullMethod,IsClientStream,IsServerStream}`, header/trailer echo | `test.TestServerPipeline_AllRPCKinds/{unary,client streaming,server streaming,bidirectional streaming}` (same change set; same recorder pattern, same `StreamServerInfo` assertion) — duplicate; pre-existing `test.TestUnaryServerInterceptor`/`TestStreamServerInterceptor` cover interceptor invocation but not the `StreamServerInfo` flags |
| `grpc.TestServer_RPCKinds_InterceptorsAndMetadata/unknown method` | same + `grpc.UnknownServiceHandler` | invoke an unregistered method | unknown handler ran via stream interceptor | `test.TestServerPipeline_UnknownServiceHandler` (same change set, two unknown method names) — duplicate; pre-existing `test.TestUnknownHandler` (`test/healthcheck_test.go`) covers the handler being reached but not the interceptor info |
| `grpc.TestServer_HandlerErrorsReachClient/{unary,server streaming}` | stubserver whose handlers return `codes.FailedPrecondition` with details | one RPC of each kind | code, message and `status.Details` round-trip | pre-existing `test.TestTapStatusDetails` (status details round-trip, unary only); `test.TestServerPipeline_StreamingErrors/server streaming status error after a message` (same change set) asserts the same server-streaming `FailedPrecondition` status — partial duplicate |
| `test.TestServerPipeline_StreamingErrors/bidirectional non-status error becomes Unknown` | stubserver, bidi handler returns `errors.New` | bidi RPC | client sees `codes.Unknown` with the error text | no exact pre-existing equivalent found (nearest: `test.TestClientStreamingError`) — not a duplicate |
| `test.TestServerPipeline_StreamingErrors/client streaming context error keeps its code` | stubserver, client-streaming handler returns ctx error | client-streaming RPC with cancelled ctx | status code preserved | no exact pre-existing equivalent found — not a duplicate |

The two added files duplicate each other for four behaviours (RPC-kind interceptor info,
unknown-service handler, handwritten-descriptor precedence, decompression limit) and
duplicate a pre-existing repository test for the decompression-limit behaviour; the claim
requires one or more equivalent pairs, and the matrix identifies several. Repro
`verify/repro/c9_duplicate_end2end_cases.sh` prints the matching setup/stimulus/assertion
lines of the pre-existing and added tests side by side and runs all of them:

```sh
cd ~/wt/604865b6 && bash ~/repos/grpc-go/verify/repro/c9_duplicate_end2end_cases.sh
```

```console
==> PAIR 1: pre-existing encoding.TestDecompressionExceedsMaxMessageSize
    7:	ss := &stubserver.StubServer{
    12:	if err := ss.Start([]grpc.ServerOption{grpc.MaxRecvMsgSize(messageLen - 1)}); err != nil {
    21:	_, err := ss.Client.UnaryCall(ctx, req, grpc.UseCompressor(compressor.Name()))
    22:	if got, want := status.Code(err), codes.ResourceExhausted; got != want {
==> PAIR 1: added test.TestServerPipeline_MaxReceiveMessageSizeAfterDecompression (unary/oversized after decompression)
    3:	ss := &stubserver.StubServer{
    18:	if err := ss.Start([]grpc.ServerOption{grpc.MaxRecvMsgSize(maxRecvSize)}); err != nil {
    41:		{name: "oversized after decompression", payload: largePayload, wantCode: codes.ResourceExhausted},
    48:			_, err := ss.Client.UnaryCall(ctx, &testpb.SimpleRequest{Payload: tc.payload}, grpc.UseCompressor(gzip.Name))
==> PAIR 2 (inside the change set): same-name handwritten-descriptor cases in both added files
    server_rpc_ext_test.go:430:			{StreamName: "Flagless", Handler: handwrittenStreamHandler("flagless")},
    test/server_pipeline_test.go:438:			name:         "unary method wins over stream of the same name",
    test/server_pipeline_test.go:444:			name:         "stream desc without streaming flags stays streaming",
==> running the pre-existing test and the added cases
    --- PASS: Test/DecompressionExceedsMaxMessageSize (0.00s)
ok  	google.golang.org/grpc/encoding	0.007s
        --- PASS: Test/ServerPipeline_MaxReceiveMessageSizeAfterDecompression/unary/oversized_after_decompression (0.00s)
        --- PASS: Test/ServerPipeline_MaxReceiveMessageSizeAfterDecompression/client_streaming/oversized_after_decompression (0.00s)
ok  	google.golang.org/grpc/test	0.016s
        --- PASS: Test/Server_HandwrittenServiceDesc//grpc.testing.HandwrittenService/Shared (0.00s)
        --- PASS: Test/Server_HandwrittenServiceDesc//grpc.testing.HandwrittenService/Flagless (0.00s)
        --- PASS: Test/Server_RecvLimitAppliesAfterDecompression/unary (0.00s)
        --- PASS: Test/Server_RecvLimitAppliesAfterDecompression/client_streaming (0.00s)
ok  	google.golang.org/grpc	0.014s
        --- PASS: Test/ServerPipeline_HandwrittenServiceDesc/unary_method_wins_over_stream_of_the_same_name (0.00s)
        --- PASS: Test/ServerPipeline_HandwrittenServiceDesc/stream_desc_without_streaming_flags_stays_streaming (0.00s)
ok  	google.golang.org/grpc/test	0.013s
```

Impact: the decompression-limit behaviour is now asserted three times (pre-existing
`encoding` test, root `server_rpc_ext_test.go`, `test/server_pipeline_test.go`) and the
handwritten-descriptor precedence behaviour twice, with the same stub server, the same
stimulus and the same status-code assertion. A future behaviour change has to be updated
in three places, review effort on the change set is inflated, and none of the duplicates
adds coverage the pre-existing test lacks. Verdict: CONFIRMED.

## C10

Claim: the delivered repository does not pass `./scripts/vet.sh` in a clean worktree after
the required tools are installed. Audited branch `grpc-go-server-unify-unary-stream-rpc-perfect`
(`a46198c6`) checked out in a dedicated clean worktree `~/wt/vet`.

```sh
cd ~/wt/vet && git status --short && git rev-parse --short HEAD   # prints only: a46198c6
./scripts/vet.sh -install
./scripts/vet.sh; echo exit=$?
```

`-install` completed with exit 0 (pinned `goimports`, `staticcheck`, `misspell`, `revive`
from `test/tools`, per `scripts/vet.sh`). Key output of the second command (three runs,
identical):

```console
+ revive -set_exit_status=1 -exclude testdata/grpc_testing_not_regenerated/ -exclude '**/*.pb.go' -formatter plain -config /home/ubuntu/wt/vet/scripts/revive.toml ./...
+ staticcheck -checks all ./...
...
+ echo SUCCESS
SUCCESS
exit=0
```

No diagnostic attributable to repository content was printed (the only `XXXXX …
deprecation errors` lines are the script's own allow-list of expected SA1019 notices) and
the worktree was still clean afterwards (`git status --short` empty). Verdict: REFUTED.
