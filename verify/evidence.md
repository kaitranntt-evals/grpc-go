# Evidence — grpc-go-server-unify-unary-stream-rpc (run v-492edf2e)

Audited branch: `grpc-go-server-unify-unary-stream-rpc-perfect` (HEAD `5546a1ee`, base `0c51461d`).
Claim-target branches were fetched from `https://github.com/kaitranntt-evals/grpc-go-server-unify-unary-stream-rpc.git` (remote `evals`) and checked out into detached worktrees:
`~/repos/wt-222348fd` (evalon/grpc-go-se-222348fd), `~/repos/wt-4fa60574`, `~/repos/wt-581c1cfb`, `~/repos/wt-ac1d7e1c`, `~/repos/wt-6275649d`, `~/repos/wt-33a77a50`, `~/repos/wt-9262840f`, plus `~/repos/wt-base` at the base commit `0c51461d27177d997e14c642fe18c11668fc09a3`.
Archived fixtures were extracted byte-exact to `~/eval_tests/tests`. Go: `go1.25.7 linux/amd64`.

## C1

Target: evalon/grpc-go-se-222348fd. Source of the accesses (`test/server_rpc_pipeline_test.go`):

```console
$ cd ~/repos/wt-222348fd && grep -n "returnStatusErr" test/server_rpc_pipeline_test.go
227:	var returnStatusErr bool
233:			if returnStatusErr {
247:	returnStatusErr = true
257:	returnStatusErr = false
```

Both writes occur in the test goroutine strictly before/after *blocking* `ss.Client.UnaryCall(...)` invocations (lines 249 and 258); the handler read at line 233 happens only while such a call is in flight, so each write is ordered before the read by the RPC round trip (transport channel/mutex operations), and the read is ordered before the next write by the call's return. Focused race run:

```console
$ cd ~/repos/wt-222348fd/test && go test -race -run 'Test/^UnaryRPCServerStatusPropagation$' -count=30 .
ok  	google.golang.org/grpc/test	2.100s
```

No race reported across 30 runs under `-race`. Verdict: REFUTED — the accesses have a happens-before relationship via the blocking RPC calls, and the race detector observes no conflicting access.

## C2

Audited branch. Runtime probe (`verify/repro/c2_unary_error_text_test.go`) triggers a unary unmarshal failure (failing codec) and a unary response over `MaxSendMsgSize`:

```console
$ cd ~/repos/grpc-go && go test ./verify/repro -run 'TestC2' -v -count=1
    c2_unary_error_text_test.go:54: unary unmarshal failure status: code=Internal msg="grpc: failed to unmarshal the received message: decoding failed"
--- PASS: TestC2UnaryUnmarshalErrorText (0.00s)
    c2_unary_error_text_test.go:71: unary max-send-size failure status: code=ResourceExhausted msg="trying to send message larger than max (1030 vs. 16)"
--- PASS: TestC2UnaryMaxSendSizeErrorText (0.00s)
```

Base-commit behavior for the same probes (worktree `~/repos/wt-base`) produces the unary-specific texts `grpc: error unmarshalling request: ...` and `grpc: trying to send message larger than max (...)`.

- Part *unary receive error*: CONFIRMED — observed `grpc: failed to unmarshal the received message` instead of `grpc: error unmarshalling request`.
- Part *unary maximum-send-size error*: CONFIRMED — observed the generic `serverStream.SendMsg` text `trying to send message larger than max (...)` (no `grpc: ` prefix) instead of the former unary-specific description.

## C3

Audited branch. `server.go:processRPC` contains:

```go
if s.opts.streamInt == nil || (!sd.ClientStreams && !sd.ServerStreams) {
    appErr = sd.Handler(server, ss)
} else {
    appErr = s.opts.streamInt(server, ss, info, sd.Handler)
}
```

Runtime probe (`verify/repro/c3_c11_falsefalse_streamdesc_test.go`) registers a genuine `grpc.StreamDesc` with both flags false via `RegisterService` on a server configured with `grpc.StreamInterceptor(...)` that increments a counter, and invokes the method:

```console
$ cd ~/repos/grpc-go && go test ./verify/repro -run 'TestC3|TestC11' -v -count=1
    c3_c11_falsefalse_streamdesc_test.go:82: handler called: 1, stream interceptor called: 0
    c3_c11_falsefalse_streamdesc_test.go:84: RESULT: false-false StreamDesc handler ran WITHOUT the stream interceptor (claim behavior observed)
--- PASS: TestC3C11FalseFalseStreamDescBypassesStreamInterceptor (0.00s)
```

CONFIRMED — the handler ran; the configured stream interceptor was never invoked.

## C4

Target: evalon/grpc-go-se-222348fd.

Part *binary-log payload* — focused repository test:

```console
$ cd ~/repos/wt-222348fd/gcp/observability && go test -run 'Test/^ServerRPCEventsLogAll$' -count=1 .
--- FAIL: Test (0.01s)
    --- FAIL: Test/ServerRPCEventsLogAll (0.01s)
            - 			Message:       nil,
            + 			Message:       []uint8{},
FAIL
FAIL	google.golang.org/grpc/gcp/observability	0.013s
```

CONFIRMED — the test expects an empty non-nil payload (`[]uint8{}`) while unary logging through the unified send path produces `nil`.

Part *status-write warning source* — the emitted warning on this branch and any test expectations:

```console
$ cd ~/repos/wt-222348fd && grep -n "failed to write status" server.go
1511:		channelz.Warningf(logger, s.channelz, "grpc: Server.handleStream failed to write status: %v", err)
1604:		channelz.Warningf(logger, s.channelz, "grpc: Server.handleStream failed to write status: %v", err)
$ grep -rn "failed to write status" test/ gcp/
(no output — no test declares any expectation naming Server.processUnaryRPC or any status-write warning)
```

The previously declared `Server.processUnaryRPC failed to write status` noise expectations were removed on this branch, and the focused end2end tests in `test/` pass (`ok google.golang.org/grpc/test 0.164s`). REFUTED for this part — no test expects `Server.processUnaryRPC`, so no expectation/emission mismatch exists.

Claim verdict: CONFIRMED (1 of 2 parts held).

## C5

Target: evalon/grpc-go-se-4fa60574. Runtime probe (`verify/repro/c5_unmarshal_descriptions_test.go`) uses a codec whose `Unmarshal` fails during a real unary RPC and a real streaming RPC:

```console
$ cd ~/repos/wt-4fa60574 && go test ./verify/repro -run 'TestC5' -v -count=1
    c5_unmarshal_descriptions_test.go:44: unary server unmarshal failure: code=Internal msg="grpc: error unmarshalling request: decoding failed"
    c5_unmarshal_descriptions_test.go:54: streaming server unmarshal failure: code=Internal msg="grpc: error unmarshalling request: decoding failed"
--- PASS: TestC5UnmarshalDescriptions (0.00s)
```

Base-commit behavior for the same probe:

```console
$ cd ~/repos/wt-base && go test ./verify/repro -run 'TestC5' -v -count=1
    unary server unmarshal failure: code=Internal msg="grpc: error unmarshalling request: decoding failed"
    streaming server unmarshal failure: code=Internal msg="grpc: failed to unmarshal the received message: decoding failed"
```

- Part *unary unmarshal description*: REFUTED — the unary-specific `grpc: error unmarshalling request` text is retained.
- Part *streaming unmarshal description*: CONFIRMED — streaming failures now return the unary text `grpc: error unmarshalling request` instead of the streaming-specific `grpc: failed to unmarshal the received message`.

Claim verdict: CONFIRMED (1 of 2 parts held).

## C6

Target: evalon/grpc-go-se-581c1cfb. `server.go` keeps `serviceInfo.methods` and `serviceInfo.streams` as separate maps, and `handleStream` selects handlers through two separate lookup branches:

```go
if md, ok := srv.methods[method]; ok {
    s.processRPC(ctx, stream, srv, md, nil, ti)
    return
}
if sd, ok := srv.streams[method]; ok {
    s.processRPC(ctx, stream, srv, nil, sd, ti)
    return
}
```

Runtime observation: both lookup branches were instrumented with a stderr print (instrumentation applied and reverted by `verify/repro/c6_registry_probe.sh`) and the archived interceptor-segregation fixture was run:

```console
$ cd ~/repos/wt-581c1cfb && cp ~/eval_tests/tests/eval_interceptor_segregation_test.go test/ && go test ./test -run 'TestEval_InterceptorSegregation' -count=1 -v 2>&1 | grep -E "C6-PROBE|^--- |^ok"
--- PASS: TestEval_InterceptorSegregation (0.00s)
C6-PROBE: stream registry lookup hit for FullDuplexCall
C6-PROBE: stream registry lookup hit for StreamingInputCall
C6-PROBE: stream registry lookup hit for StreamingOutputCall
C6-PROBE: unary registry lookup hit for UnaryCall
ok  	google.golang.org/grpc/test	0.006s
```

CONFIRMED — unary methods resolve through `srv.methods` and streaming methods through `srv.streams`: two registries and two lookup branches, not one unified descriptor path.

## C7

Target: evalon/grpc-go-se-ac1d7e1c. `server.go:processRPC` warns on the application-error terminal path but not on the successful one:

```go
// application-error terminal status (line 1460):
if e := ss.s.WriteStatus(appStatus); e != nil {
    channelz.Warningf(logger, s.channelz, "grpc: Server.processRPC failed to write status: %v", e)
}
// successful terminal status (line 1472):
return ss.s.WriteStatus(statusOK)
```

`handleStream` discards `processRPC`'s return value (`s.processRPC(ctx, stream, srv, m, ti)` at line 1551), so the success-path failure is logged nowhere. Runtime probe (`verify/repro/c7_status_write_warning_test.go`) forces both terminal `WriteStatus` calls to fail by closing the client connection mid-RPC while capturing the grpclog output; `stats.End.Error` is derived from `processRPC`'s returned error:

```console
$ cd ~/repos/wt-ac1d7e1c && go test ./verify/repro -run 'TestC7' -v -count=1
    c7_status_write_warning_test.go:130: stats.End.Error: rpc error: code = Aborted desc = app error
    c7_status_write_warning_test.go:131: captured 'failed to write status' warnings: ["[core][Server #1] grpc: Server.processRPC failed to write status: connection error: desc = \"transport is closing\""]
--- PASS: TestC7AppErrorStatusWriteWarning (0.20s)
    c7_status_write_warning_test.go:136: stats.End.Error: rpc error: code = Unavailable desc = transport is closing
    c7_status_write_warning_test.go:137: captured 'failed to write status' warnings: []
--- PASS: TestC7SuccessStatusWriteWarning (0.20s)
```

- Part *application-error terminal status*: REFUTED — the focused `grpc: Server.processRPC failed to write status` warning is emitted.
- Part *successful terminal status*: CONFIRMED — `WriteStatus(statusOK)` failed (its `transport is closing` error surfaced as `stats.End.Error` through `processRPC`'s return value), yet no `failed to write status` warning was emitted.

Claim verdict: CONFIRMED (1 of 2 parts held).

## C8

Target: evalon/grpc-go-se-ac1d7e1c. Inventory of the tests the solution committed (diff vs the base commit):

```console
$ cd ~/repos/wt-ac1d7e1c && git diff --name-only $(git merge-base HEAD 0c51461d27177d997e14c642fe18c11668fc09a3)..HEAD -- '*_test.go'
server_ext_test.go
test/end2end_test.go
$ git diff $(git merge-base HEAD 0c51461d...)..HEAD -- '*_test.go' | grep -E '^\+func '
+func (s) TestServer_UnifiedRPCPipeline(t *testing.T) {
$ grep -n "Compress\|MaxRecv\|ResourceExhausted" server_ext_test.go
(only FullDuplexCall plumbing lines — no compression, no MaxRecvMsgSize, no ResourceExhausted assertion)
$ grep -rn "TestStreamingDecompressionExceedsMaxMessageSize" encoding/
(no output — the test named in the claim does not exist; the only decompression-limit test is unary:)
$ grep -n -A3 "func (s) TestDecompressionExceedsMaxMessageSize" encoding/compressor_test.go
215:func (s) TestDecompressionExceedsMaxMessageSize(t *testing.T) {   # UnaryCallF only, MaxRecvMsgSize(99), asserts ResourceExhausted
```

Running the committed test:

```console
$ go test . -run 'Test/^Server_UnifiedRPCPipeline$' -v -count=1
=== RUN   Test/Server_UnifiedRPCPipeline
--- PASS: Test (0.00s)
ok  	google.golang.org/grpc	0.008s
```

CONFIRMED — the committed tests exercise plain (uncompressed) full-duplex RPCs only; no committed test performs a compressed full-duplex RPC whose decompressed request exceeds `MaxRecvMsgSize` and asserts `ResourceExhausted`. (The archived fixture `eval_decompression_limits_test.go` does cover it and passes on this branch — `--- PASS: TestEval_DecompressionLimits/Streaming (0.00s)` — so the behavior works but is untested by the solution's committed tests.)

## C9

Target: evalon/grpc-go-se-6275649d. `stream.go` contains both a method `func (ss *serverStream) prepareMsg(m any) (...)` and the package-level `func prepareMsg(m any, codec baseCodec, cp Compressor, comp encoding.Compressor, pool mem.BufferPool) (...)`; each independently handles `PreparedMsg`, encoding, compression, message-header construction, and buffer ownership, and `serverStream.SendMsg` calls the method, not the package-level function.

Runtime observation: both implementations were instrumented with a stderr print (applied and reverted by `verify/repro/c9_preparemsg_probe.sh`) and one archived unary round trip was run:

```console
$ cd ~/repos/wt-6275649d && cp ~/eval_tests/tests/eval_unary_roundtrip_test.go test/ && go test ./test -run 'TestEval_UnaryRoundTrip' -count=1 -v
C9-PROBE: package-level prepareMsg called
C9-PROBE: serverStream.prepareMsg called
C9-PROBE: package-level prepareMsg called
--- PASS: TestEval_UnaryRoundTrip (0.00s)
ok  	google.golang.org/grpc/test	0.006s
```

CONFIRMED — in a single RPC the server response is prepared by the independent `serverStream.prepareMsg` re-implementation while the client uses the package-level `prepareMsg`: two parallel implementations of the same preparation sequence are live.

## C10

Target: evalon/grpc-go-se-33a77a50. `stream.go:serverStream.SendMsg` (line 1825):

```go
if strings.Contains(status.Convert(err).Message(), "error while compressing") {
    channelz.Error(logger, ss.channelz, "grpc: server failed to compress response: ", err)
} else {
    channelz.Error(logger, ss.channelz, "grpc: server failed to encode response: ", err)
}
```

Runtime probe (`verify/repro/c10_substring_classification_test.go`) triggers an *encoding* failure whose error text merely contains the compression substring, with a capturing logger:

```console
$ cd ~/repos/wt-33a77a50 && go test ./verify/repro -run 'TestC10' -v -count=1
    c10_substring_classification_test.go:85: client error: rpc error: code = Internal desc = grpc: error while marshaling: synthetic encoding failure: error while compressing lookalike text
    c10_substring_classification_test.go:89: compress-classified logs: 1 [[core][Server #1] grpc: server failed to compress response: rpc error: ...]
    c10_substring_classification_test.go:90: encode-classified logs: 0 []
    c10_substring_classification_test.go:92: RESULT: encoding failure misclassified as compression failure via substring match (claim behavior observed)
--- PASS: TestC10SubstringClassification (0.00s)
```

Genuine compression failures carry the text produced by `rpc_util.go:compress` (`status.Errorf(codes.Internal, "grpc: error while compressing: %v", ...)`, line 842), which is what the substring match keys on. CONFIRMED — branch selection depends on `strings.Contains` over human-readable status text, and an encoding failure containing the substring is misclassified as a compression failure.

## C11

Audited branch. Same probe and run as C3 (`verify/repro/c3_c11_falsefalse_streamdesc_test.go`): a genuine `StreamDesc{ClientStreams: false, ServerStreams: false}` registered via `RegisterService`, server configured with a recording `grpc.StreamInterceptor`:

```console
$ cd ~/repos/grpc-go && go test ./verify/repro -run 'TestC3|TestC11' -v -count=1
    c3_c11_falsefalse_streamdesc_test.go:82: handler called: 1, stream interceptor called: 0
    c3_c11_falsefalse_streamdesc_test.go:84: RESULT: false-false StreamDesc handler ran WITHOUT the stream interceptor (claim behavior observed)
--- PASS: TestC3C11FalseFalseStreamDescBypassesStreamInterceptor (0.00s)
```

CONFIRMED — `processRPC`'s `!sd.ClientStreams && !sd.ServerStreams` guard routes the genuine stream descriptor around the configured stream interceptor; the handler ran with the interceptor record empty.

## C12

Target: evalon/grpc-go-se-9262840f. On this branch `GetServiceInfo` builds `ServiceInfo.Methods` by ranging over the `srv.methods` registry map with no ordering step (naming drift from the claim's grouping description: descriptors live in one map and are emitted in map-iteration order). Runtime probe (`verify/repro/c12_getserviceinfo_order_test.go`) registers three unary and three streaming descriptors and queries `GetServiceInfo` 200 times:

```console
$ cd ~/repos/wt-9262840f && go test ./verify/repro -run 'TestC12' -v -count=1
    c12_getserviceinfo_order_test.go:47: iteration 198: streaming descriptor precedes unary descriptor: [{Name:S2 IsClientStream:true ...} {Name:S3 ...} {Name:U1 ...} ...]
    c12_getserviceinfo_order_test.go:52: stream-before-unary orderings observed in 200 queries: 128
--- PASS: TestC12GetServiceInfoOrdering (0.00s)
```

Base-commit behavior for the same probe: `stream-before-unary orderings observed in 200 queries: 0`.

The branch's own pre-existing repository test fails intermittently on the same nondeterminism:

```console
$ cd ~/repos/wt-9262840f && go test . -run 'Test/^GetServiceInfo$' -count=5
    server_test.go:130: GetServiceInfo() = map[grpc.testing.EmptyService:{Methods:[{Name:EmptyStream IsClientStream:true ...} {Name:EmptyCall ...}] ...}], want map[... {Name:EmptyCall ...} {Name:EmptyStream ...} ...]
--- FAIL: Test/GetServiceInfo (0.00s)
FAIL	google.golang.org/grpc	0.014s
```

CONFIRMED — 128 of 200 queries returned a streaming descriptor before a unary descriptor for the same service; the base commit never does.
