## Evidence — behavioral audit v-4e52f94a

Audited branch: `origin/grpc-go-server-unify-unary-stream-rpc-perfect` (HEAD `e363acbc`), checked out as
`verify/grpc-go-server-unify-unary-stream-rpc-v-4e52f94a` in `~/repos/grpc-go`. Base commit `0c51461d`.
Claim-target branches were fetched from the second remote
`evalon = https://github.com/kaitranntt-evals/grpc-go-server-unify-unary-stream-rpc.git` and checked out
as detached worktrees under `~/wt/<8-char id>`:

```console
98b493c6 5e000c36440df367a90dddb7a083dd623323775a
58155825 7c142b5fc9d402a4f2236f56a1742dec48b9289e
14fb311d 073e38ce5998ed3b9017ba28b903b0977940a544
bf72a067 06e343cb6c91df7f6f5239473274a637155d9e93
e36ecf54 ef437eb4eb4afb0a439ce4cef6955ba1ac90bee9
92c7a4a1 11e3524706b11a5cefe14b3a1ce4b99255fdb609
851287e1 30f3d58dcb58ca2a50edd07c10d5955d345d9a94
2e8490f9 1dbafe03f5a891c9bf8fcf4cb3cbd22957231699
35f325ce 76e6ea7d25fce8730b427dd4aeca912fe594bafe
a84249aa 0869220538f0814256e3b0187d399c63a21a6209
a0b58cfd 1c053e20bc18a710d48bb6b9255a944f5c2a289e
9f5b3793 aaaa12253875c1eaca8722f32f231c8df5d281e6
51f968cd f6352a59f8163659cb287db3de10042c1d150373
d4d6777c 51d0be4f0d2025cae42387da27e0f20d8bdae49e
```

Toolchain: `go version go1.25.7 linux/amd64`, 8 CPUs, 31 GiB RAM. Eval fixtures were extracted byte-exact from
`eval_tests.zip` to `~/eval_tests/tests/`. All repro scripts referenced below live in `verify/repro/` and
restore every file they touch; each worktree was `git status --short`-clean after every run.

## C1

Claim: a changed RPC test leaves a created server live when an early assertion / listener-setup failure
terminates the test before `defer srv.Stop()` is registered. Adjudicated per branch with
`verify/repro/c1_server_leak_probe.py`, which (a) inserts, immediately **before** `grpc.NewServer(...)`, a
`t.Cleanup` probe that counts registered channelz servers (`internal/channelz.GetServers`, after
`channelz.TurnOn()`) before/after the test, and (b) in `mutate` mode forces the pre-`defer` assertion (or the
`net.Listen` call) to fail exactly where the delivered test would fail. `control` mode runs the same
instrumented test unmodified.

Static layout in each delivered test (line numbers from the delivered files):

| branch | file / test | `NewServer` | fatal assertion(s) before cleanup | `defer Stop()` |
|---|---|---|---|---|
| 98b493c6 | `test/server_test.go` `TestServer_HandwrittenServiceDesc_UnaryPrecedenceAndStreamFlags` | 891 | 901 (`GetServiceInfo` len), 918 (listener) | 921 |
| 58155825 | `test/server_pipeline_test.go` `TestServerPipeline_HandwrittenServiceDescRouting` | 410 | 462 (`GetServiceInfo` len), 470 (listener) | 473 |
| 14fb311d | `test/server_pipeline_test.go` `TestServerPipeline_HandwrittenDescDispatch` | 496 | 512/516/520 (`GetServiceInfo`), 525 (listener) | 528 |
| bf72a067 | `test/server_pipeline_test.go` `TestServer_HandwrittenServiceDescRouting` | 567 | 577/584/587 (`GetServiceInfo`), 592 (listener) | 595 |
| e36ecf54 | `test/server_pipeline_test.go` `TestServer_HandwrittenServiceDesc_Dispatch` | 546 | 556/559 (`GetServiceInfo`), 573 (listener) | 576 |
| 92c7a4a1 | `server_rpc_ext_test.go` `TestServer_HandwrittenServiceDesc` | 418 | 422 (listener `net.Listen` failure) | 425 |
| 851287e1 | `server_pipeline_ext_test.go` `TestServer_UnifiedPipeline` (per subtest) | 183 | 225 (`GetServiceInfo` len/metadata), 244 (listener) | 247 |

Commands (one control + one mutate run per branch):

```sh
cd ~/repos/grpc-go
for b in 98b493c6 58155825 14fb311d bf72a067 e36ecf54 92c7a4a1 851287e1; do
  python3 verify/repro/c1_server_leak_probe.py ~/wt/$b $b control
  python3 verify/repro/c1_server_leak_probe.py ~/wt/$b $b mutate
done
```

Key output (control → mutate), verbatim:

```console
########## 98b493c6
+ go test -count=1 -v ./test -run ^Test$/^Server_HandwrittenServiceDesc_UnaryPrecedenceAndStreamFlags$ (mode=control)
    server_test.go:896: C1PROBE registered channelz servers: before=0 after=0 (leaked=0)
    --- PASS: Test/Server_HandwrittenServiceDesc_UnaryPrecedenceAndStreamFlags (0.00s)
+ go test -count=1 -v ./test -run ^Test$/^Server_HandwrittenServiceDesc_UnaryPrecedenceAndStreamFlags$ (mode=mutate)
    server_test.go:908: GetServiceInfo()["handwritten.Service"].Methods = [{Name:Shared ...} {Name:Shared ...} {Name:NoFlags ...}], want 3 methods
    server_test.go:896: C1PROBE registered channelz servers: before=0 after=1 (leaked=1)
    --- FAIL: Test/Server_HandwrittenServiceDesc_UnaryPrecedenceAndStreamFlags (0.00s)
########## 58155825
    server_pipeline_test.go:415: C1PROBE registered channelz servers: before=0 after=0 (leaked=0)   (control, PASS)
    server_pipeline_test.go:469: GetServiceInfo() methods = [...], want [...]
    server_pipeline_test.go:415: C1PROBE registered channelz servers: before=0 after=1 (leaked=1)   (mutate, FAIL)
########## 14fb311d
    server_pipeline_test.go:501: C1PROBE registered channelz servers: before=0 after=0 (leaked=0)   (control, PASS)
    server_pipeline_test.go:523: GetServiceInfo() methods = [{Shared false false} {Shared false false} {NoDirectionFlags false false}]; want unary Shared first followed by two streams
    server_pipeline_test.go:501: C1PROBE registered channelz servers: before=0 after=1 (leaked=1)   (mutate, FAIL)
########## bf72a067
    server_pipeline_test.go:572: C1PROBE registered channelz servers: before=0 after=0 (leaked=0)   (control, PASS)
    server_pipeline_test.go:591: GetServiceInfo() methods = [...], want Both and NoDirection with no streaming flags
    server_pipeline_test.go:572: C1PROBE registered channelz servers: before=0 after=1 (leaked=1)   (mutate, FAIL)
########## e36ecf54
    server_pipeline_test.go:551: C1PROBE registered channelz servers: before=0 after=0 (leaked=0)   (control, PASS)
    server_pipeline_test.go:566: GetServiceInfo()["grpc.testing.PipelineDispatchService"].Methods is empty
    server_pipeline_test.go:551: C1PROBE registered channelz servers: before=0 after=1 (leaked=1)   (mutate, FAIL)
########## 92c7a4a1
+ go test -count=1 -v . -run ^Test$/^Server_HandwrittenServiceDesc$ (mode=control)
    server_rpc_ext_test.go:423: C1PROBE registered channelz servers: before=0 after=0 (leaked=0)
+ go test -count=1 -v . -run ^Test$/^Server_HandwrittenServiceDesc$ (mode=mutate)
    server_rpc_ext_test.go:429: net.Listen() failed: listen tcp: address -1: invalid port
    server_rpc_ext_test.go:423: C1PROBE registered channelz servers: before=0 after=1 (leaked=1)
    --- FAIL: Test/Server_HandwrittenServiceDesc (0.00s)
########## 851287e1
+ go test -count=1 -v . -run ^Test$/^Server_UnifiedPipeline$ (mode=control)
    server_pipeline_ext_test.go:188: C1PROBE registered channelz servers: before=0 after=0 (leaked=0)   (x17 subtests, PASS)
+ go test -count=1 -v . -run ^Test$/^Server_UnifiedPipeline$ (mode=mutate)
    server_pipeline_ext_test.go:188: C1PROBE registered channelz servers: before=0 after=1 (leaked=1)
    server_pipeline_ext_test.go:188: C1PROBE registered channelz servers: before=1 after=2 (leaked=1)
    ...
    server_pipeline_ext_test.go:188: C1PROBE registered channelz servers: before=16 after=17 (leaked=1)
    --- FAIL: Test/Server_UnifiedPipeline (0.00s)
```

Totals across the log: 23 `leaked=1` lines (6 single-server tests + 17 subtests of 851287e1) in mutate mode, 23
`leaked=0` lines in control mode. Interpretation: in every branch the created `*grpc.Server` stays registered in
channelz when the test ends via the pre-`defer` fatal path, i.e. `Stop()` never runs and no `t.Cleanup`/owning
helper exists. Impact reasoning: in all 7 branches `Serve` is only called after the fatal assertions, so the leaked
server is an unstopped, never-served `*Server` (channelz entry, no goroutines/sockets); the effect is confined to
test-hygiene on failure paths and is not caught by the package leak checker. Verdict per branch: CONFIRMED (all 7).

## C2

### 98b493c6 — `TestServer_UnaryRPC_RespondsBeforeClientHalfClose` (`test/server_test.go:761-797`)

Delivered code (lines 777-797):

```go
stream, err := ss.CC.NewStream(ctx, desc, testgrpc.TestService_UnaryCall_FullMethodName)
if err != nil { t.Fatalf(...) }
...
if err := stream.RecvMsg(resp); err != io.EOF {      // block-scoped err
    t.Fatalf("stream.RecvMsg() = %v; want io.EOF", err)
}
if st := status.Convert(err); st.Code() != codes.OK {  // outer err == NewStream result
    t.Fatalf("stream status = %v, want OK", st)
}
```

Repro `verify/repro/c2_98b493c6_halfclose_sentinel.py` overwrites the outer `err` with a sentinel status right
after the NewStream nil-check (RPC untouched; the `RecvMsg -> io.EOF` check still passes) and logs the value fed to
`status.Convert`:

```sh
cd ~/repos/grpc-go && python3 verify/repro/c2_98b493c6_halfclose_sentinel.py ~/wt/98b493c6
```

```console
+ go test -count=1 -v ./test -run ^Test$/^Server_UnaryRPC_RespondsBeforeClientHalfClose$
    server_test.go:795: C2PROBE: err passed to status.Convert = rpc error: code = Internal desc = C2 sentinel: this is the NewStream-scoped err variable, not an RPC result
    server_test.go:797: stream status = rpc error: code = Internal desc = C2 sentinel: this is the NewStream-scoped err variable, not an RPC result, want OK
    --- FAIL: Test/Server_UnaryRPC_RespondsBeforeClientHalfClose (0.00s)
exit=1
```

The final-status assertion reads the NewStream setup variable (nil in the delivered test → always OK), not the RPC
result. Verdict: CONFIRMED.

### 2e8490f9 — changed diagnostic expectations

Delivered diff vs base (`git diff 0c51461d HEAD -- encoding/encoding_test.go test/end2end_test.go`):

```diff
-	if err == nil || ... || !strings.Contains(err.Error(), "grpc: error unmarshalling request") {
+	if err == nil || ... || !strings.Contains(err.Error(), "grpc: failed to unmarshal the received message") {
...
-	te.declareLogNoise("Server.processUnaryRPC failed to write status")
+	te.declareLogNoise("Server.handleStream failed to write status")      (x2: testClientRequestBodyErrorCloseAfterLength, testLargeTimeout)
```

The `encoding_test.go` replacement is emitted by the same scenario (`rpc_util.go:1064`, test passes asserting the
string): `go test -count=1 -v ./encoding -run '^Test$/^DecodeDoesntPanicOnServer$'` → `--- PASS: Test/DecodeDoesntPanicOnServer`.

The `declareLogNoise` replacement is not. Repro `verify/repro/c2_2e8490f9_lognoise_probe.sh` runs the two tests
with warning-level, unfiltered logging on base and on the branch, then instruments the branch's unary-completion
`WriteStatus` to show the failure stage is reached:

```sh
cd ~/repos/grpc-go && bash verify/repro/c2_2e8490f9_lognoise_probe.sh ~/wt/2e8490f9 ~/wt/base
```

```console
== base commit (old phrase is emitted):
      1     tlogger.go:129: WARNING server.go:1402 [core] [Server #1] grpc: Server.processUnaryRPC failed to write status: connection error: desc = "transport is closing"
      1     tlogger.go:129: WARNING server.go:1402 [core] [Server #4] grpc: Server.processUnaryRPC failed to write status: connection error: desc = "transport is closing"
      1     tlogger.go:129: WARNING server.go:1402 [core] [Server #7] grpc: Server.processUnaryRPC failed to write status: connection error: desc = "transport is closing"
== 2e8490f9 as delivered:
      1     --- PASS: Test/ClientRequestBodyErrorCloseAfterLength (0.03s)
      1     --- PASS: Test/LargeTimeout (0.07s)
      (no "failed to write status" line at all; repeated 3x with identical result)
== 2e8490f9 with probe after 'err = ss.s.WriteStatus(appStatus)':
      1     tlogger.go:129: WARNING server.go:1496 [core] [Server #10] C2PROBE: final WriteStatus failed at unary completion stage: connection error: desc = "transport is closing"
      1     tlogger.go:129: WARNING server.go:1496 [core] [Server #4] C2PROBE: final WriteStatus failed at unary completion stage: connection error: desc = "transport is closing"
== emission sites of the replacement phrase on 2e8490f9:
1513:		channelz.Warningf(logger, s.channelz, "grpc: Server.handleStream failed to write status: %v", err)   (handleMalformedMethodName)
1607:		channelz.Warningf(logger, s.channelz, "grpc: Server.handleStream failed to write status: %v", err)   (unknown service/method path)
```

Same scenario, same failure stage (final status write fails with "transport is closing") — the branch emits
nothing there; the only production emitters of the new phrase are the malformed-method-name and unknown-method
paths, which these tests never reach. Verdict: CONFIRMED (the `declareLogNoise` expectations name a diagnostic
no reachable path emits; the `encoding_test.go` expectation is fine).

## C3

Branch 35f325ce. `serverStream.SendMsg` (stream.go:1822) now calls a new method `serverStream.prepareMsg`
(stream.go:1955-1972) instead of the shared package-level `prepareMsg` (stream.go:1985-2002, still live for
`clientStream` at 1058 and `addrConn` streams at 1555). Both implement the `m.(*PreparedMsg)` short-circuit,
`encode` → `compress` → `data.Free()`-on-error → `msgHeader` in full; the method adds only two `channelz.Error` lines.

Repro `verify/repro/c3_35f325ce_preparemsg_divergence.sh` disables the `PreparedMsg` branch of the shared
`prepareMsg` and runs the two preloader end-to-end tests (client-side PreparedMsg send vs. server-side):

```sh
cd ~/repos/grpc-go && bash verify/repro/c3_35f325ce_preparemsg_divergence.sh ~/wt/35f325ce
```

```console
== live preparation implementations:
1058:	hdr, data, payload, pf, err := prepareMsg(m, cs.codec, cs.compressorV0, cs.compressorV1, cs.cc.dopts.copts.BufferPool)
1555:	hdr, data, payload, pf, err := prepareMsg(m, as.codec, as.sendCompressorV0, as.sendCompressorV1, as.ac.dopts.copts.BufferPool)
1822:	hdr, data, payload, pf, err := ss.prepareMsg(m)
1955:func (ss *serverStream) prepareMsg(m any) (hdr []byte, data, payload mem.BufferSlice, pf payloadFormat, err error) {
1956:	if preparedMsg, ok := m.(*PreparedMsg); ok {
1985:func prepareMsg(m any, codec baseCodec, cp Compressor, comp encoding.Compressor, pool mem.BufferPool) (hdr []byte, data, payload mem.BufferSlice, pf payloadFormat, err error) {
1986:	if preparedMsg, ok := m.(*PreparedMsg); ok {
== with shared prepareMsg's PreparedMsg branch disabled:
      1     --- FAIL: Test/PreloaderClientSend (0.00s)
      1     --- PASS: Test/PreloaderSenderSend (0.00s)
```

The server send path is unaffected by a change to the shared helper's PreparedMsg decision → it has its own copy.
On base, `serverStream.SendMsg` delegated to `prepareMsg(m, ss.codec, ...)` (diff `git diff 0c51461d HEAD -- stream.go`).
Verdict: CONFIRMED.

## C4

Branch a84249aa, worktree `~/wt/a84249aa` (clean, no injected files). Command run three times back-to-back:

```sh
cd ~/wt/a84249aa && time go test -count=1 -cpu 1,4 -timeout 7m . ./encoding ./test; echo exit=$?
```

```console
# run 1
ok  	google.golang.org/grpc	62.619s
ok  	google.golang.org/grpc/encoding	0.504s
ok  	google.golang.org/grpc/test	79.398s
real	1m22.004s
exit=0
# run 2
ok  	google.golang.org/grpc	62.372s
ok  	google.golang.org/grpc/encoding	0.502s
ok  	google.golang.org/grpc/test	79.926s
real	1m20.381s
exit=0
# run 3
ok  	google.golang.org/grpc	62.566s
ok  	google.golang.org/grpc/encoding	0.498s
ok  	google.golang.org/grpc/test	78.582s
real	1m19.022s
exit=0
```

Completes successfully, well inside the 7m timeout, 3/3. Verdict: REFUTED.

## C5

Branch a0b58cfd. Mechanism site and context plumbing:

```console
$ grep -n "serverFromContext(ss.ctx)" stream.go
1821:		channelz.Error(logger, serverFromContext(ss.ctx).channelz, "grpc: server failed to encode response: ", err)
$ grep -n "contextWithServer(ctx, s)\|TagRPC(ctx" server.go
1503:	ctx = contextWithServer(ctx, s)
1542:		ctx = s.statsHandler.TagRPC(ctx, &stats.RPCTagInfo{FullMethodName: stream.Method()})
```

`serverFromContext` returns `nil` when the value is absent (server.go:1751-1754); the `.channelz` field access is
unguarded. Trigger exercised with the eval fixture `tests/eval_stats_context_encode_test.go` (TagRPC returns
`context.Background()`, codec `Marshal` fails), via `verify/repro/c5_a0b58cfd_fresh_tagrpc_panic.sh`:

```sh
cd ~/repos/grpc-go && bash verify/repro/c5_a0b58cfd_fresh_tagrpc_panic.sh ~/wt/a0b58cfd ~/eval_tests/tests/eval_stats_context_encode_test.go
```

```console
=== TestEval_StatsHandlerFreshContextEncodeStreaming
panic: runtime error: invalid memory address or nil pointer dereference
[signal SIGSEGV: segmentation violation code=0x1 addr=0x1f0 pc=0x9aa3a5]

goroutine 23 [running]:
google.golang.org/grpc.(*serverStream).SendMsg(0xc0002aa100, {0xe3e520, 0xc0001306c0})
	/home/ubuntu/wt/a0b58cfd/stream.go:1821 +0x345
google.golang.org/grpc.(*GenericServerStream[...]).Send(...)
	/home/ubuntu/wt/a0b58cfd/stream_interfaces.go:219
google.golang.org/grpc/test.TestEval_StatsHandlerFreshContextEncodeStreaming.func1({0x101fe98, 0xc0002a0620})
	/home/ubuntu/wt/a0b58cfd/test/eval_stats_context_encode_test.go:101 +0xc4
FAIL	google.golang.org/grpc/test	0.007s
=== TestEval_StatsHandlerFreshContextEncode   (unary case, same site)
panic: runtime error: invalid memory address or nil pointer dereference
google.golang.org/grpc.(*serverStream).SendMsg(...)  /home/ubuntu/wt/a0b58cfd/stream.go:1821 +0x345
google.golang.org/grpc.(*Server).register.unaryStreamHandler.func1  /home/ubuntu/wt/a0b58cfd/server.go:144
google.golang.org/grpc.(*Server).processRPC  /home/ubuntu/wt/a0b58cfd/server.go:1428
FAIL	google.golang.org/grpc/test	0.009s
```

Both parts hold: the diagnostic dereferences `serverFromContext(ss.ctx).channelz` unguarded, and a streaming (and
unary) RPC whose `TagRPC` returns a fresh context and whose response encoding fails reaches it with a nil `*Server`,
crashing the whole server process (SIGSEGV in the handler goroutine, not recoverable by the RPC). Impact reasoning:
`stats.Handler.TagRPC` is documented to return the context used for later stats callbacks and is allowed to derive
a new one; any handler that does so turns a per-RPC encode error into a process crash. Verdict: CONFIRMED.

## C6

Branch 9f5b3793, test `TestServerPipeline_UnaryRespondsBeforeHalfClose` (`test/server_pipeline_test.go:350-383`)
uses `desc := &grpc.StreamDesc{StreamName: "UnaryCall"}` (ClientStreams=false) and comments "Deliberately do not
call CloseSend". Client write path: `stream.go:1239 a.transportStream.Write(hdr, payld, &transport.WriteOptions{Last: !cs.desc.ClientStreams})`.

Repro `verify/repro/c6_9f5b3793_halfclose_mutation.sh` (1) logs the `Last` flag of the test's SendMsg, (2) mutates
the server so unary `RecvMsg` again waits for the client half-close (`if ss.desc.ClientStreams || ss.unary` →
`if ss.desc.ClientStreams`) and runs the authored test and the eval fixture `TestEval_UnaryWithoutHalfClose`
(which uses `ClientStreams: true`) against that mutant:

```sh
cd ~/repos/grpc-go && bash verify/repro/c6_9f5b3793_halfclose_mutation.sh ~/wt/9f5b3793 ~/eval_tests/tests/eval_unary_halfclose_test.go
```

```console
== probe only: what does the authored test's SendMsg write?
    tlogger.go:129: WARNING stream.go:1239 [core] C6PROBE client SendMsg method=/grpc.testing.TestService/UnaryCall ClientStreams=false -> transport write Last=true
    --- PASS: Test/ServerPipeline_UnaryRespondsBeforeHalfClose (0.00s)
== mutation: server unary RecvMsg waits for half-close; authored test:
    --- PASS: Test/ServerPipeline_UnaryRespondsBeforeHalfClose (0.00s)
ok  	google.golang.org/grpc/test	0.015s
== same mutation; eval fixture TestEval_UnaryWithoutHalfClose (ClientStreams:true):
    eval_unary_halfclose_test.go:63: DEADLOCK: Server did not respond within 2s; it is waiting for client half-close!
--- FAIL: TestEval_UnaryWithoutHalfClose (2.01s)
```

The authored test's single `SendMsg` carries `Last=true` (END_STREAM), so the request side is already closed before
any response assertion; a server that waits for half-close still passes it, while the fixture that really keeps the
request open detects the wait. Verdict: CONFIRMED.

## C7

Branch e36ecf54, `TestServer_AllRPCTypes_InterceptorSegregation` (`test/server_pipeline_test.go:224-449`). Table
entries have `run func(ctx context.Context) pipelineRPCResult`; the callbacks call `t.Fatalf(...)` where `t` is the
enclosing test's `*testing.T`, and are invoked from `t.Run(test.name, func(t *testing.T) { ... res := test.run(ctx) ... })`.

Repro `verify/repro/c7_e36ecf54_parent_fatalf.py` gives only the "client streaming" callback's
`client.StreamingInputCall` an already-cancelled context so the existing
`t.Fatalf("StreamingInputCall() failed: %v", err)` (line 275) fires:

```sh
cd ~/repos/grpc-go && python3 verify/repro/c7_e36ecf54_parent_fatalf.py ~/wt/e36ecf54
```

```console
=== RUN   Test/Server_AllRPCTypes_InterceptorSegregation/unary
=== RUN   Test/Server_AllRPCTypes_InterceptorSegregation/client_streaming
=== NAME  Test/Server_AllRPCTypes_InterceptorSegregation
    server_pipeline_test.go:275: StreamingInputCall() failed: rpc error: code = Canceled desc = context canceled
=== NAME  Test/Server_AllRPCTypes_InterceptorSegregation/client_streaming
    testing.go:1811: test executed panic(nil) or runtime.Goexit: subtest may have called FailNow on a parent test
--- FAIL: Test (0.06s)
    --- FAIL: Test/Server_AllRPCTypes_InterceptorSegregation (0.05s)
        --- PASS: Test/Server_AllRPCTypes_InterceptorSegregation/unary (0.00s)
        --- FAIL: Test/Server_AllRPCTypes_InterceptorSegregation/client_streaming (0.00s)
exit=1
```

The failure message is attributed to the parent test (`=== NAME Test/Server_AllRPCTypes_InterceptorSegregation`),
the subtest is reported via Go's "subtest may have called FailNow on a parent test" diagnostic, and the remaining
table cases (server streaming, bidi, error, oversized) never ran. Verdict: CONFIRMED.

## C8

Branch 51f968cd, `TestServerStreamingRPC_ErrorFraming` (`test/server_rpc_pipeline_test.go:592-660`). The
malformed-input scenario sends a `SimpleRequest{ResponseSize:1, Payload:...}` on `/StreamingOutputCall` and asserts
only `status.Code(err) != codes.Internal` (line 650-652); `StreamingOutputCallF` itself returns
`status.Error(codes.Internal, "streaming handler must not run when the request cannot be decoded")` (line 605-607).

Repro `verify/repro/c8_51f968cd_handler_ran_probe.py` adds two `t.Logf` probes (no assertion changes):

```sh
cd ~/repos/grpc-go && python3 verify/repro/c8_51f968cd_handler_ran_probe.py ~/wt/51f968cd
```

```console
+ go test -count=1 -v ./test -run ^Test$/^ServerStreamingRPC_ErrorFraming$
    server_rpc_pipeline_test.go:606: C8PROBE StreamingOutputCallF RAN; decoded request = payload:{body:"not a streaming request"}  2:1
    server_rpc_pipeline_test.go:651: C8PROBE malformed-input scenario got: rpc error: code = Internal desc = streaming handler must not run when the request cannot be decoded
    --- PASS: Test/ServerStreamingRPC_ErrorFraming (0.00s)
ok  	google.golang.org/grpc/test	0.011s
exit=0
```

Even the delivered test (no variant needed) decodes the "undecodable" bytes successfully (field 2's wire-type
mismatch is kept as unknown field `2:1`), runs the handler, receives the handler's own Internal status, and passes.
Verdict: CONFIRMED.

## C9

Branch d4d6777c, `serverStream.RecvMsg` (stream.go:1876-1974): `data, err := recvAndDecompress(...)` (1907), then
`defer data.Free()` (1930) before `ss.codec.Unmarshal(data, m)`; for `!ss.unary && !ss.desc.ClientStreams` the method
continues into the cardinality lookahead `recv(...)` (1966) while the deferred `Free` is still pending. On base the
same path went through `recv()` in rpc_util.go, which frees inside `recv` right after `Unmarshal`.

Repro: `verify/repro/c9_d4d6777c_recv_buffer_retention_test.go` (handwritten `StreamDesc{ServerStreams:true}`
= non-client-streaming registration; client `NewStream` with `ClientStreams:true` so the request side stays open;
64 KiB request so buffers are pooled; tracking `mem.BufferPool` via `experimental.BufferPool`; no stats handler,
no binlog, default proto codec). Driver `verify/repro/c9_d4d6777c_run.sh` runs it as delivered (A) and with `Free`
moved to immediately after `Unmarshal` (B):

```sh
cd ~/repos/grpc-go && bash verify/repro/c9_d4d6777c_run.sh ~/wt/d4d6777c
```

```console
== A: as delivered (defer data.Free() at RecvMsg level)
1930:	defer data.Free()
    c9_verify_ext_test.go:92: C9PROBE RecvMsg still BLOCKED after 1.5s with request side open (cardinality lookahead)
    c9_verify_ext_test.go:94: C9PROBE pooled buffers while blocked: gets=4 puts=0 outstanding=4
    c9_verify_ext_test.go:109: C9PROBE pooled buffers after CloseSend+response: gets=4 puts=4 outstanding=0
--- PASS: TestC9_RecvMsgRetainsBufferDuringLookahead (1.70s)
== B: mutated to free immediately after Unmarshal
    c9_verify_ext_test.go:92: C9PROBE RecvMsg still BLOCKED after 1.5s with request side open (cardinality lookahead)
    c9_verify_ext_test.go:94: C9PROBE pooled buffers while blocked: gets=4 puts=4 outstanding=0
    c9_verify_ext_test.go:109: C9PROBE pooled buffers after CloseSend+response: gets=4 puts=4 outstanding=0
--- PASS: TestC9_RecvMsgRetainsBufferDuringLookahead (1.71s)
```

Both parts hold: with the request side open, `RecvMsg` waits in the lookahead (handler still inside `RecvMsg` after
1.5 s, returns only after `CloseSend`), and during that wait all 4 pooled buffers of the already-unmarshalled request
remain outstanding (A) — they are returned during the wait once `Free` is moved after `Unmarshal` (B). Impact
reasoning: for non-client-streaming stream descriptors called by clients that delay half-close, each in-flight RPC
pins its decoded request bytes for the whole wait even though nothing (no stats, binlog, or retaining codec) needs
them; this is a regression from base, which freed inside `recv()`. Verdict: CONFIRMED.

## C10

Audited branch, clean tree (`git status --short` empty, no fixtures copied in):

```sh
cd ~/repos/grpc-go && bash -o pipefail -c 'source scripts/common.sh; gofmt -s -d -l . 2>&1 | fail_on_output'; echo "exit=$?"
gofmt -s -l . ; echo "gofmt -s -l exit=$?"
```

```console
exit=0
gofmt -s -l exit=0
```

(No file listed, no diff.) Sanity check that the pipeline does fail on unformatted input:

```console
$ printf 'package x\nfunc  f( ) {}\n' > /tmp/x.go; bash -o pipefail -c 'source scripts/common.sh; gofmt -s -d -l /tmp/x.go 2>&1 | fail_on_output'; echo "sanity exit=$?"
/tmp/x.go
diff /tmp/x.go.orig /tmp/x.go
...
sanity exit=1
```

Verdict: REFUTED.

## C11

Audited branch, clean tree, no fixtures copied in:

```sh
cd ~/repos/grpc-go && time go test -race -cpu 1,4 -timeout 7m . ./encoding ./test; echo exit=$?
```

```console
ok  	google.golang.org/grpc	62.794s
ok  	google.golang.org/grpc/encoding	1.633s
ok  	google.golang.org/grpc/test	96.920s
real	1m51.475s
exit=0
```

Second uncached run (`-count=1` added only to defeat the test cache):

```console
ok  	google.golang.org/grpc	57.641s
ok  	google.golang.org/grpc/encoding	1.518s
ok  	google.golang.org/grpc/test	97.514s
real	1m38.099s
exit=0
```

Verdict: REFUTED.
