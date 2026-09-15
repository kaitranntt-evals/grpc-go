## Evidence — audit v-eb32fab9

Environment: `go version go1.25.7 linux/amd64`. Primary checkout: `grpc-go-server-unify-unary-stream-rpc-perfect` at `8d853f9d`, base `0c51461d`. Claim-target branches were fetched from `https://github.com/kaitranntt-evals/grpc-go-server-unify-unary-stream-rpc.git` into detached worktrees (`~/wt/<suffix>`), one per branch; `~/wt/base` is a worktree at `0c51461d`.

All repro test files under `verify/repro/` carry the `//go:build verify_repro` constraint so they are invisible to `go build ./...` / `go vet ./...`; they are run by copying them into `test/` of the target checkout and passing `-tags verify_repro` (exact command in each file's first line). After every run the copied file was removed and `git status --porcelain` of the worktree was empty (production code untouched).

## C1

Claim: at least one changed RPC test lacks a bound on raw-frame/socket reads, or leaves a test-created server live. Adjudicated per branch.

### C1 — branch `evalon/grpc-go-se-146be32f` (HEAD `ee64c16f`)

Changed test files vs. base:

```console
$ git diff --stat 0c51461d..HEAD -- '*_test.go' 'test/servertester.go'
 server_pipeline_ext_test.go | 382 ++++++++++++++++++++++++++++++++++++++++++++
 test/end2end_test.go        |  65 ++++++--
```

The changed `testClientRequestBodyErrorUnexpectedEOF` (test/end2end_test.go:4527-4581) now loops on the raw-frame helper:

```go
te.withServerTester(func(st *serverTester) {
    st.writeHeadersGRPC(1, "/grpc.testing.TestService/"+tc.method, false)
    st.writeData(1, true, []byte{0, 0, 0, 0, 5})
    for {
        f := st.wantAnyFrame()
        switch f := f.(type) {
        case *http2.MetaHeadersFrame:
            if !f.StreamEnded() { continue }
            ...
            return
        case *http2.DataFrame, *http2.RSTStreamFrame:
            t.Fatalf(...)
        }
    }
})
```

The helper it uses (test/servertester.go:206-213, unchanged) reads with no deadline, and `withServerTester` (test/end2end_test.go:872-887) only closes the conn via `defer c.Close()` *after* the callback returns, so no cancellation path can unblock a read in progress; no `SetReadDeadline`/`SetDeadline` exists in either file:

```go
func (st *serverTester) wantAnyFrame() http2.Frame {
	f, err := st.fr.ReadFrame()
	if err != nil {
		st.t.Fatal(err)
	}
	return f
}
```

```console
$ grep -n "SetReadDeadline\|SetDeadline" test/servertester.go test/end2end_test.go
(no output)
```

Demonstration run — the repro replays the same loop against a handler that never completes the stream (`verify/repro/c1_146be32f_unbounded_wantanyframe_test.go`):

```console
$ cp verify/repro/c1_146be32f_unbounded_wantanyframe_test.go test/ && go test -tags verify_repro ./test -run 'Test/^Verify_C1_UnboundedWantAnyFrame$' -count=1 -v -timeout 8s
=== RUN   Test/Verify_C1_UnboundedWantAnyFrame
    c1_146be32f_unbounded_wantanyframe_test.go:48: t=1ms got frame *http2.WindowUpdateFrame
    c1_146be32f_unbounded_wantanyframe_test.go:48: t=1ms got frame *http2.PingFrame
panic: test timed out after 8s
...
goroutine 18 [IO wait]:
...
net.(*conn).Read(0xc000422dc0, {0xc0001d0044?, 0x0?, 0x0?})
	/usr/local/go/src/net/net.go:196 +0x45
io.ReadAtLeast(...)
golang.org/x/net/http2.readFrameHeader(...)
golang.org/x/net/http2.(*Framer).ReadFrameHeader(0xc0001d0000)
golang.org/x/net/http2.(*Framer).ReadFrame(0xc0001d0000)
	/home/ubuntu/go/pkg/mod/golang.org/x/net@v0.57.0/http2/frame.go:572 +0x18
google.golang.org/grpc/test.(*serverTester).wantAnyFrame(0xc000017b20)
	/home/ubuntu/wt/146be32f/test/servertester.go:208 +0x25
FAIL	google.golang.org/grpc/test	8.047s
exit=1
```

The read is only terminated by the `go test` process-level timeout: the goroutine is parked in `net.(*conn).Read` under `wantAnyFrame` with the connection still open. There is no read deadline, the loop has no iteration limit, and nothing closes the connection on cancellation. **Read-bounds part holds → CONFIRMED on this branch.**

Impact reasoning: if the server under test ever fails to send trailers (regression, hang, deadlock in the new unified `processRPC`), this test does not fail with a diagnostic — it wedges the whole `./test` package until the global `go test` timeout (10 min by default) kills the binary with a goroutine dump, taking every other test in the package with it.

### C1 — branch `evalon/grpc-go-se-54ea6760` (HEAD `9b83a566`)

Changed test files vs. base:

```console
$ git diff --stat 0c51461d..HEAD -- '*_test.go'
 test/server_rpc_pipeline_test.go | 677 +++++++++++++++++++++++++++++++++++++++
```

No raw-frame/socket reads in the file (`grep -n "net.Dial\|ReadFrame\|\.Read(\|wantAnyFrame\|withServerTester" test/server_rpc_pipeline_test.go` → no output). Served servers use `defer ss.Stop()` or `t.Cleanup(srv.Stop)` (`startPipelineDescServer`, lines 526-540). However `TestServerPipeline_HandwrittenServiceDesc` also constructs a second, reflection-only server that is never stopped (lines 616-618):

```go
	srv := grpc.NewServer()
	srv.RegisterService(&pipelineServiceDesc, &pipelineDescService{})
	got := srv.GetServiceInfo()["pipeline.Service"].Methods
```

`grpc.NewServer` registers the server in channelz (`server.go:722 channelz: channelz.RegisterServer("")`) and only `Server.Stop` removes it (`server.go:1676 channelzRemoveOnce.Do(func() { channelz.RemoveEntry(s.channelz.ID) })`). Demonstration run — the repro queries the channelz server registry right after that test (grpctest runs subtests in name order):

```console
$ cp verify/repro/c1_54ea6760_server_left_live_test.go test/ && go test -tags verify_repro ./test -run 'Test/^(ServerPipeline_HandwrittenServiceDesc|Verify_C1_ChannelzServersAfterHandwrittenServiceDesc)$' -count=1 -v
=== RUN   Test/ServerPipeline_HandwrittenServiceDesc
    tlogger.go:133: INFO server.go:736 [core] [Server #1] Server created  (t=+276.92µs)
    tlogger.go:133: INFO server.go:932 [core] [Server #1 ListenSocket #3] ListenSocket created  (t=+430.956µs)
    tlogger.go:133: INFO server.go:736 [core] [Server #7] Server created  (t=+1.926338ms)
    tlogger.go:133: INFO server.go:868 [core] [Server #1 ListenSocket #3] ListenSocket deleted  (t=+2.124384ms)
=== RUN   Test/Verify_C1_ChannelzServersAfterHandwrittenServiceDesc
    c1_54ea6760_server_left_live_test.go:21: channelz servers still registered after preceding tests: 1
    c1_54ea6760_server_left_live_test.go:23:   live Server #7 (listen sockets: 0)
    c1_54ea6760_server_left_live_test.go:26: 1 grpc.Server(s) left live (never Stop()ped) by preceding tests
    --- PASS: Test/ServerPipeline_HandwrittenServiceDesc (0.00s)
    --- FAIL: Test/Verify_C1_ChannelzServersAfterHandwrittenServiceDesc (0.00s)
exit=1
```

Controls (same probe, same command shape):

```console
$ go test -tags verify_repro ./test -run 'Test/^Verify_C1_ChannelzServersAfterHandwrittenServiceDesc$' -count=1 -v
    channelz servers still registered after preceding tests: 0
    --- PASS
$ go test -tags verify_repro ./test -run 'Test/^(ServerPipeline_AllRPCTypes|ServerPipeline_StreamingRPCFailure|ServerPipeline_OversizedDecompressedMessage|ServerPipeline_UnaryRespondsBeforeHalfClose|ServerPipeline_UnknownServiceHandler|Verify_C1_ChannelzServersAfterHandwrittenServiceDesc)$' -count=1 -v
    channelz servers still registered after preceding tests: 0
    --- PASS
```

Only `TestServerPipeline_HandwrittenServiceDesc` leaves a server registered after it finishes: `Server #7`, the reflection-only `grpc.NewServer()` at line 616 (Server #1 is the served one and is stopped by `t.Cleanup`). **Server-cleanup part holds → CONFIRMED on this branch.**

Impact reasoning: the leak is a channelz entity (and the server's option/codec state), not a listener or goroutine, so the package's goroutine leak checker does not flag it; each run of the test package accumulates one orphan `Server` in the channelz registry for the process lifetime, which pollutes any later channelz-based assertions in the same binary.

## C2

Branch: primary (`grpc-go-server-unify-unary-stream-rpc-perfect`, HEAD `8d853f9d`).

Repro `verify/repro/c2_stats_payload_method_test.go`: a `stats.Handler` whose `TagRPC` returns its input unchanged records `grpc.Method(ctx)` and `grpc.ServerTransportStreamFromContext(ctx) != nil` for every server-side callback, then runs one `UnaryCall` (asserted) and one `FullDuplexCall` (logged for comparison).

```console
$ cp verify/repro/c2_stats_payload_method_test.go test/ && go test -tags verify_repro ./test -run 'Test/^Verify_C2_StatsPayloadMethod$' -count=1 -v
    c2_stats_payload_method_test.go:91: unary InHeader   grpc.Method(ctx) = method|ok|hasServerTransportStream = |false|false
    c2_stats_payload_method_test.go:91: unary Begin      grpc.Method(ctx) = method|ok|hasServerTransportStream = |false|false
    c2_stats_payload_method_test.go:91: unary InPayload  grpc.Method(ctx) = method|ok|hasServerTransportStream = |false|false
    c2_stats_payload_method_test.go:91: unary OutPayload grpc.Method(ctx) = method|ok|hasServerTransportStream = |false|false
    c2_stats_payload_method_test.go:91: unary End        grpc.Method(ctx) = method|ok|hasServerTransportStream = /grpc.testing.TestService/UnaryCall|true|true
    c2_stats_payload_method_test.go:95: unary InPayload: grpc.Method unavailable / ServerTransportStream missing: |false|false
    c2_stats_payload_method_test.go:95: unary OutPayload: grpc.Method unavailable / ServerTransportStream missing: |false|false
    c2_stats_payload_method_test.go:114: stream InPayload  grpc.Method(ctx) = method|ok|hasServerTransportStream = |false|false
    c2_stats_payload_method_test.go:114: stream OutPayload grpc.Method(ctx) = method|ok|hasServerTransportStream = |false|false
--- FAIL: Test (0.01s)
FAIL	google.golang.org/grpc/test	0.011s
exit=1
```

Same repro at the base commit `0c51461d` (worktree `~/wt/base`):

```console
$ cp verify/repro/c2_stats_payload_method_test.go test/ && go test -tags verify_repro ./test -run 'Test/^Verify_C2_StatsPayloadMethod$' -count=1 -v
    c2_stats_payload_method_test.go:91: unary InPayload  grpc.Method(ctx) = method|ok|hasServerTransportStream = /grpc.testing.TestService/UnaryCall|true|true
    c2_stats_payload_method_test.go:91: unary OutPayload grpc.Method(ctx) = method|ok|hasServerTransportStream = /grpc.testing.TestService/UnaryCall|true|true
    c2_stats_payload_method_test.go:91: unary End        grpc.Method(ctx) = method|ok|hasServerTransportStream = /grpc.testing.TestService/UnaryCall|true|true
    c2_stats_payload_method_test.go:114: stream InPayload  grpc.Method(ctx) = method|ok|hasServerTransportStream = |false|false
    c2_stats_payload_method_test.go:114: stream OutPayload grpc.Method(ctx) = method|ok|hasServerTransportStream = |false|false
--- PASS: Test (0.01s)
ok  	google.golang.org/grpc/test	0.010s
exit=0
```

Context trace (audited branch): `handleStream` calls `TagRPC` and then `stream.SetContext(ctx)` (server.go:1569, 1581) — the transport stream's context never receives the server-transport-stream value. `processRPC` attaches it only to the local `ctx` passed into `serverStream.ctx` (server.go:1303 `ctx = NewContextWithServerTransportStream(ctx, stream)`), while both unary payload callbacks are emitted from the transport stream's context, not `ss.ctx`:

```console
$ grep -n "ss.s.Context()" stream.go
stream.go:1880:		ss.statsHandler.HandleRPC(ss.s.Context(), outPayload(false, m, dataLen, payloadLen, time.Now()))
stream.go:1936:		ss.statsHandler.HandleRPC(ss.s.Context(), &stats.InPayload{
```

Verdict: **CONFIRMED** — for a unary RPC both `InPayload` and `OutPayload` receive a context without the server transport stream, so `grpc.Method` returns `("", false)`; at base both callbacks reported the full method. (Streaming payload callbacks lacked it at base as well; the unary regression is what the claim asserts.)

Impact reasoning: any stats handler that keys unary payload metrics by `grpc.Method(ctx)` (the documented way to obtain the method inside `HandleRPC`) silently loses the method for every unary RPC after this change — the callbacks still fire, so nothing errors; metrics simply get attributed to `""`.

## C3

Branch: `evalon/grpc-go-se-cb95c9da` (HEAD `86135dea`).

Code path under test (stream.go:1818-1822):

```go
	hdr, data, payload, pf, err := prepareMsg(m, ss.codec, ss.compressorV0, ss.compressorV1, ss.p.bufferPool)
	if err != nil {
		channelz.Error(logger, serverFromContext(ss.ctx).channelz, "grpc: server failed to encode response: ", err)
		return err
	}
```

`serverFromContext` (server.go:1740-1743) returns `nil` when the private `serverKey{}` value is absent; `handleStream` replaces `ctx` with the return value of `TagRPC` (server.go:1528) before `processRPC` builds `ss.ctx` from it (server.go:1290-1292).

Repro `verify/repro/c3_tagrpc_fresh_ctx_panic_test.go`: `TagRPC` returns `context.Background()`; the server codec's `Marshal` fails; one `UnaryCall`.

```console
$ cp verify/repro/c3_tagrpc_fresh_ctx_panic_test.go test/ && go test -tags verify_repro ./test -run 'Test/^Verify_C3_TagRPCFreshContextEncodeFailure$' -count=1 -v
=== RUN   Test/Verify_C3_TagRPCFreshContextEncodeFailure
    tlogger.go:133: INFO clientconn.go:619 [core] [Channel #2] Channel Connectivity change to READY  (t=+1.510072ms)
panic: runtime error: invalid memory address or nil pointer dereference
[signal SIGSEGV: segmentation violation code=0x1 addr=0x1f0 pc=0x9aa2e5]

goroutine 82 [running]:
google.golang.org/grpc.(*serverStream).SendMsg(0xc000344000, {0xe715e0, 0xc000328080})
	/home/ubuntu/wt/cb95c9da/stream.go:1820 +0x345
google.golang.org/grpc.(*Server).handleStream.unaryStreamHandler.func1({0xe88460, 0xc0000e0000}, {0x101c5f0, 0xc000344000})
	/home/ubuntu/wt/cb95c9da/server.go:1233 +0xe7
google.golang.org/grpc.(*Server).processRPC(0xc000198008, ...)
	/home/ubuntu/wt/cb95c9da/server.go:1416 +0x11e6
google.golang.org/grpc.(*Server).handleStream(0xc000198008, {0x1019698, 0xc0000ec000}, 0xc000338000)
	/home/ubuntu/wt/cb95c9da/server.go:1561 +0xb36
FAIL	google.golang.org/grpc/test	0.013s
exit=1
```

Same repro at base `0c51461d` (the base logs with `channelz.Error(logger, s.channelz, ...)` from `processUnaryRPC`, never via the context):

```console
$ cp verify/repro/c3_tagrpc_fresh_ctx_panic_test.go test/ && go test -tags verify_repro ./test -run 'Test/^Verify_C3_TagRPCFreshContextEncodeFailure$' -count=1 -v
    c3_tagrpc_fresh_ctx_panic_test.go:63: UnaryCall error: rpc error: code = Internal desc = grpc: error while marshaling: verify: synthetic marshal failure
--- PASS: Test (0.01s)
ok  	google.golang.org/grpc/test	0.011s
exit=0
```

Verdict: **CONFIRMED** — the panic is at stream.go:1820, the `serverFromContext(ss.ctx).channelz` dereference (`addr=0x1f0`, a small offset from a nil pointer, consistent with a field load on a nil `*Server`), in the response-preparation-error diagnostic, on the server's RPC goroutine (no recover in `serveStreams` → whole server process crashes).

Impact reasoning: `stats.Handler.TagRPC` is documented to return "a context used for the rest of the RPC"; handlers that build a fresh context (e.g. deadline-stripping or tracing wrappers that derive from a different root) are legal. On this branch such a handler turns any encoding/compression failure of a response — an ordinary `codes.Internal` RPC error at base — into a process-wide crash.

## C4

Branch: `evalon/grpc-go-se-f9de5b8a` (HEAD `19c32b56`). Worktree was clean (`git status --porcelain` → empty) and contained no probe files when the formatter ran.

```console
$ gofmt -s -d -l .
(no output)
$ echo exit=$?
exit=0
$ git diff --name-only 0c51461d..HEAD
rpc_util.go
server.go
server_pipeline_ext_test.go
stream.go
$ git diff --name-only 0c51461d..HEAD -- '*.go' | xargs gofmt -s -l ; echo exit=$?
exit=0
$ gofmt -s -l server_pipeline_ext_test.go ; echo exit=$?
exit=0
```

`server_pipeline_ext_test.go` (the file named by the claim, including the `{Name: "Neither"}: true` map entries) and every other delivered Go file are reported as already formatted by `gofmt -s`. Verdict: **REFUTED**.

## C5

Branch: `evalon/grpc-go-se-880ceb42` (HEAD `62797fe3`).

Logging calls on this branch vs. base:

```console
$ grep -n "failed to encode\|failed to compress" stream.go server.go          # 880ceb42
stream.go:1823:		logger.Errorf("grpc: server failed to encode response: %v", err)
$ grep -n "failed to encode\|failed to compress" server.go stream.go          # base 0c51461d
server.go:1191:		channelz.Error(logger, s.channelz, "grpc: server failed to encode response: ", err)
server.go:1198:		channelz.Error(logger, s.channelz, "grpc: server failed to compress response: ", err)
```

`channelz.Error` → `AddTraceEvent` prefixes the line with the entity (`internal/channelz/trace.go:197 d := fmt.Sprintf("[%s] %s", e, desc.Desc)`, `Server.String()` = `Server #%d`). The single `logger.Errorf` on this branch (the only preparation-failure branch left; encode and compress failures both flow through `prepareMsg` into it) has no entity argument.

Repro `verify/repro/c5_send_preparation_diagnostic_test.go`: captures grpclog into a buffer, triggers a `Marshal` failure and a `Compress` failure for a unary and a bidi RPC each, and prints every diagnostic line.

```console
$ cp verify/repro/c5_send_preparation_diagnostic_test.go test/ && go test -tags verify_repro ./test -run 'Test/^Verify_C5_SendPreparationDiagnostics$' -count=1 -v
=== RUN   Test/Verify_C5_SendPreparationDiagnostics/MarshalFailure
    c5_send_preparation_diagnostic_test.go:82: unary  status: rpc error: code = Internal desc = grpc: error while marshaling: verify: synthetic marshal failure
    c5_send_preparation_diagnostic_test.go:91: stream status: rpc error: code = Internal desc = grpc: error while marshaling: verify: synthetic marshal failure
    c5_send_preparation_diagnostic_test.go:106: diagnostic: 2026/09/15 22:50:50 ERROR: [core] grpc: server failed to encode response: rpc error: code = Internal desc = grpc: error while marshaling: verify: synthetic marshal failure
    c5_send_preparation_diagnostic_test.go:108: diagnostic lacks owning server identity ([Server #N]): "2026/09/15 22:50:50 ERROR: [core] grpc: server failed to encode response: rpc error: code = Internal desc = grpc: error while marshaling: verify: synthetic marshal failure"
=== RUN   Test/Verify_C5_SendPreparationDiagnostics/CompressFailure
    c5_send_preparation_diagnostic_test.go:82: unary  status: rpc error: code = Internal desc = grpc: error while compressing: verify: synthetic compress failure
    c5_send_preparation_diagnostic_test.go:91: stream status: rpc error: code = Internal desc = grpc: error while compressing: verify: synthetic compress failure
    c5_send_preparation_diagnostic_test.go:106: diagnostic: 2026/09/15 22:50:50 ERROR: [core] grpc: server failed to encode response: rpc error: code = Internal desc = grpc: error while compressing: verify: synthetic compress failure
    c5_send_preparation_diagnostic_test.go:108: diagnostic lacks owning server identity ([Server #N]): "2026/09/15 22:50:50 ERROR: [core] grpc: server failed to encode response: rpc error: code = Internal desc = grpc: error while compressing: verify: synthetic compress failure"
    --- FAIL: Test/Verify_C5_SendPreparationDiagnostics (0.00s)
exit=1
```

(Each sub-test printed the diagnostic twice — once for the unary RPC, once for the streaming RPC; all four lines lack `[Server #N]`.)

Same repro at base `0c51461d`:

```console
    diagnostic: 2026/09/15 22:43:22 ERROR: [core] [Server #1] grpc: server failed to encode response: rpc error: code = Internal desc = grpc: error while marshaling: verify: synthetic marshal failure
    diagnostic: 2026/09/15 22:43:22 ERROR: [core] [Server #7] grpc: server failed to compress response: rpc error: code = Internal desc = grpc: error while compressing: verify: synthetic compress failure
--- PASS: TestVerify_C5_SendPreparationDiagnostics (0.01s)
```

Corroboration with the exact assessment fixture `tests/test/eval_send_preparation_diagnostic_test.go` copied to `test/` on this branch:

```console
$ go test -v ./test -run '^TestEval_SendPreparationDiagnostics$' -count=1
=== RUN   TestEval_SendPreparationDiagnostics/SendPreparationCompressionFailureDiagnostic
    tlogger.go:125: ERROR stream.go:1823 [core] grpc: server failed to encode response: rpc error: code = Internal desc = grpc: error while compressing: synthetic marshal encoding failure during compress  (t=+1.565443ms)
    tlogger.go:207: Expected error 'grpc: server failed to compress response' not encountered
=== RUN   TestEval_SendPreparationDiagnostics/SendPreparationEncodeFailureDiagnostic
    tlogger.go:123: ERROR stream.go:1823 [core] grpc: server failed to encode response: rpc error: code = Internal desc = grpc: error while marshaling: synthetic compression failure during marshal  (t=+881.502µs)
    --- FAIL: TestEval_SendPreparationDiagnostics/SendPreparationCompressionFailureDiagnostic (0.00s)
    --- FAIL: TestEval_SendPreparationDiagnostics/SendPreparationCompressionFailureStreamingDiagnostic (0.00s)
    --- PASS: TestEval_SendPreparationDiagnostics/SendPreparationEncodeFailureDiagnostic (0.00s)
    --- PASS: TestEval_SendPreparationDiagnostics/SendPreparationEncodeFailureStreamingDiagnostic (0.00s)
```

Every emitted line is `[core] grpc: server failed to encode response: ...` with no `[Server #N]` entity; the source is `stream.go:1823 logger.Errorf(...)`. Verdict: **CONFIRMED**.

Impact reasoning: in a process hosting several `grpc.Server`s (common: serving + admin/health servers), an operator reading `server failed to encode response` can no longer tell which server produced it, and channelz trace events for the server no longer record the failure at all (`channelz.Error` also feeds `db.traceEvent`; `logger.Errorf` does not). The compress-failure message text also collapsed into "failed to encode response", which is what the fixture's two failing sub-tests measure.

## C6

Branch: `evalon/grpc-go-se-32eb5f50` (HEAD `b1b67a36`).

The test under examination, test/server_pipeline_test.go:386-414 `TestServerPipeline_UnaryRespondsWithoutHalfClose`, claims "a unary RPC is executed and responded to ... even if the client never half-closes the stream" and opens the stream with:

```go
	desc := &grpc.StreamDesc{StreamName: "UnaryCall"}
	stream, err := ss.CC.NewStream(ctx, desc, "/grpc.testing.TestService/UnaryCall")
	...
	if err := stream.SendMsg(&testpb.SimpleRequest{...}); err != nil { ... }
	// Deliberately do not call CloseSend: the server must respond anyway.
	resp := &testpb.SimpleResponse{}
	if err := stream.RecvMsg(resp); err != nil { ... }
```

Client transport write on this branch (stream.go:1239):

```go
	if err := a.transportStream.Write(hdr, payld, &transport.WriteOptions{Last: !cs.desc.ClientStreams}); err != nil {
```

With `ClientStreams` unset (false), `Last` is `true` on the request DATA frame. Demonstration run — repro `verify/repro/c6_implicit_halfclose_test.go` replays the test's exact client sequence against an `UnknownServiceHandler` that, after reading the request, attempts a second `RecvMsg` (which returns `io.EOF` immediately iff the client has already half-closed) *before* sending the response; a second sub-test uses `ClientStreams: true` as control:

```console
$ cp verify/repro/c6_implicit_halfclose_test.go test/ && go test -tags verify_repro ./test -run 'Test/^Verify_C6_' -count=1 -v
=== RUN   Test/Verify_C6_ClientStreamsDescDoesNotHalfClose
    c6_implicit_halfclose_test.go:80: desc={StreamName:UnaryCall Handler:<nil> ServerStreams:false ClientStreams:true}: server observed client half-close before responding = false
=== RUN   Test/Verify_C6_RegressionTestDescImplicitlyHalfCloses
    c6_implicit_halfclose_test.go:71: desc={StreamName:UnaryCall Handler:<nil> ServerStreams:false ClientStreams:false}: server observed client half-close before responding = true
    c6_implicit_halfclose_test.go:73: request SendMsg with ClientStreams=false implicitly half-closed the stream (transport Last=true); the regression test never exercises response-before-half-close
    --- PASS: Test/Verify_C6_ClientStreamsDescDoesNotHalfClose (0.50s)
    --- FAIL: Test/Verify_C6_RegressionTestDescImplicitlyHalfCloses (0.00s)
exit=1
```

The repository test itself passes on the branch (`go test ./test -run 'Test/^ServerPipeline_UnaryRespondsWithoutHalfClose$' -count=1 -v` → `--- PASS`), which is consistent with the server having already seen END_STREAM. Verdict: **CONFIRMED** — with the `StreamDesc` the test uses, the request `SendMsg` half-closes the request side before the response is received, so the test does not exercise the behavior its name and comment claim.

Impact reasoning: the branch's only regression test for "unary responds before half-close" is green regardless of whether the unified server path waits for half-close, i.e. the exact regression the eval targets would go undetected by it (the fixture `eval_unary_halfclose_test.go` is the assessment's independent check).

## C7

Branch: `evalon/grpc-go-se-315cd083` (HEAD `6b630600`).

Assertion text, test/server_test.go:950-970 `TestServerUnaryRespondsBeforeHalfClose`:

```go
	stream, err := ss.CC.NewStream(ctx, desc, unaryCallMethod)          // 951: outer err
	if err != nil { ... }
	if err := stream.SendMsg(&testpb.SimpleRequest{ResponseSize: 5}); err != nil { ... } // 955: if-scoped
	resp := new(testpb.SimpleResponse)
	if err := stream.RecvMsg(resp); err != nil { ... }                   // 959: if-scoped
	...
	if err := stream.RecvMsg(resp); err != io.EOF {                     // 965: if-scoped
		t.Fatalf("second RecvMsg() = %v, want io.EOF", err)
	}
	if st, ok := status.FromError(err); ok && st.Code() != codes.OK {    // 968
		t.Fatalf("stream finished with status %v, want OK", st)
	}
```

go/types resolution of every `err` identifier in the function (`verify/repro/c7_resolve_err_binding/main.go`):

```console
$ go run -tags verify_repro ./verify/repro/c7_resolve_err_binding test/server_test.go TestServerUnaryRespondsBeforeHalfClose
test/server_test.go:951:10  DEF  err (scope function scope ...)
test/server_test.go:955:5  DEF  err (scope if scope ...)
test/server_test.go:959:5  DEF  err (scope if scope ...)
test/server_test.go:965:5  DEF  err (scope if scope ...)
test/server_test.go:965:34  USE  err -> declared at test/server_test.go:965:5
test/server_test.go:966:50  USE  err -> declared at test/server_test.go:965:5
test/server_test.go:968:32  USE  err -> declared at test/server_test.go:951:10
```

The `err` at 968:32 (the `status.FromError(err)` argument) binds to the `NewStream` result declared at 951:10; every `RecvMsg` error is an if-scoped variable that is out of scope at line 968.

Demonstration run — repro `verify/repro/c7_stale_err_binding_test.go` replays the test verbatim and logs the value seen at the final check:

```console
$ cp verify/repro/c7_stale_err_binding_test.go test/ && go test -tags verify_repro ./test -run 'Test/^(ServerUnaryRespondsBeforeHalfClose|Verify_C7_StaleErrBinding)$' -count=1 -v
=== RUN   Test/ServerUnaryRespondsBeforeHalfClose
=== RUN   Test/Verify_C7_StaleErrBinding
    c7_stale_err_binding_test.go:60: at final check: err=<nil>  (err == NewStream err: true; err == second RecvMsg err (io.EOF): false)
    c7_stale_err_binding_test.go:61: status.FromError(err) -> st=rpc error: code = OK desc =  ok=true code=OK
    c7_stale_err_binding_test.go:68: final status.FromError(err) evaluates the stale NewStream error (nil), not the RPC completion error; the assertion cannot fail
    --- PASS: Test/ServerUnaryRespondsBeforeHalfClose (0.00s)
    --- FAIL: Test/Verify_C7_StaleErrBinding (0.00s)
exit=1
```

Verdict: **CONFIRMED** — the completion-status assertion consumes the successful (`nil`) `NewStream` error; `status.FromError(nil)` yields `ok=true, Code()==OK`, so the check can never fire.

Impact reasoning: the assertion is dead code; it does not weaken the test's real check (line 965 already requires `io.EOF`), but it reads as if the final RPC status were verified when it is not, and would silently pass if the second `RecvMsg` were changed to return a status error alongside `io.EOF`.
