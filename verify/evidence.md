## Setup

All claims target branches in the sibling repository `kaitranntt-evals/grpc-go-server-unify-unary-stream-rpc`; each was checked out in its own worktree from that remote (added as `evals`). Base commit for all branches: `0c51461d`.

```sh
cd ~/repos/grpc-go
git remote add evals https://github.com/kaitranntt-evals/grpc-go-server-unify-unary-stream-rpc.git
git fetch evals evalon/grpc-go-se-1669c9fa evalon/grpc-go-se-c0b0d8e5 evalon/grpc-go-se-948dc826
git worktree add ~/wt/c1 evals/evalon/grpc-go-se-1669c9fa   # 87a7948f  (C1)
git worktree add ~/wt/c2 evals/evalon/grpc-go-se-c0b0d8e5   # 82455f5d  (C2, C4)
git worktree add ~/wt/c3 evals/evalon/grpc-go-se-948dc826   # fcc6fc02  (C3)
git worktree add ~/wt/base 0c51461d                          # base commit (control for C4)
```

Toolchain: `go version go1.25.7 linux/amd64`. Every mutation below was applied to a worktree, run, saved as a patch under `verify/repro/`, and reverted (`git checkout -- <files>`); the branches themselves were not modified.

## C1

Branch: `evalon/grpc-go-se-1669c9fa`. The only added test asserting unary completion without client half-close is `TestServer_UnaryRespondsBeforeHalfCloseAndPropagatesMetadata` in `test/server_pipeline_test.go` (lines 406-468). It opens the stream with:

```go
desc := &grpc.StreamDesc{StreamName: "UnaryCall"}   // ClientStreams == false, ServerStreams == false
stream, err := ss.CC.NewStream(ctx, desc, unaryCallMethod)
stream.SendMsg(&testpb.SimpleRequest{})
stream.RecvMsg(&testpb.SimpleResponse{})
```

Client-side send behavior for that descriptor (`stream.go`, unchanged on the branch): `if !cs.desc.ClientStreams { cs.sentLast = true }` (line 1053) and `a.transportStream.Write(hdr, payld, &transport.WriteOptions{Last: !cs.desc.ClientStreams})` (line 1239) — i.e. SendMsg carries END_STREAM.

### Probe: does that descriptor half-close on SendMsg?

`verify/repro/c1_halfclose_probe_test.go` starts a bare server whose `UnknownServiceHandler` receives one message and immediately performs a second `RecvMsg`; an instant `io.EOF` means END_STREAM already arrived.

```sh
cp verify/repro/c1_halfclose_probe_test.go ~/wt/c1/test/verify_c1_halfclose_probe_test.go
cd ~/wt/c1 && go test ./test -run '^TestVerifyC1_' -count=1 -v
```

```console
=== RUN   TestVerifyC1_CandidateDescriptorHalfClosesOnSend
    verify_c1_halfclose_probe_test.go:83: ClientStreams=false: first RecvMsg err=<nil>; second RecvMsg err=EOF after 1.043µs
--- PASS: TestVerifyC1_CandidateDescriptorHalfClosesOnSend (0.00s)
=== RUN   TestVerifyC1_ClientStreamsDescriptorKeepsRequestOpen
    verify_c1_halfclose_probe_test.go:100: ClientStreams=true: first RecvMsg err=<nil>; second RecvMsg err=rpc error: code = Canceled desc = context canceled after 2.999827193s
--- PASS: TestVerifyC1_ClientStreamsDescriptorKeepsRequestOpen (3.00s)
```

With the candidate's descriptor the server sees EOF (half-close) 1µs after the request; only `ClientStreams: true` keeps the request side open.

### Mutation: server that waits for half-close still passes the candidate test

`verify/repro/c1_server_wait_for_halfclose_mutation.patch` changes `rpcDesc.handle` in `server.go` so the unary path drains the request stream to `io.EOF` before invoking the handler (i.e. the exact regression the test claims to guard against).

```sh
cd ~/wt/c1 && git apply ~/repos/grpc-go/verify/repro/c1_server_wait_for_halfclose_mutation.patch
cp ~/eval_tests/tests/eval_unary_halfclose_test.go test/
go test ./test -run 'Test/Server_UnaryRespondsBeforeHalfCloseAndPropagatesMetadata' -count=1 -v
```

```console
--- PASS: Test (0.01s)
    --- PASS: Test/Server_UnaryRespondsBeforeHalfCloseAndPropagatesMetadata (0.00s)
=== RUN   TestEval_UnaryWithoutHalfClose
    eval_unary_halfclose_test.go:63: DEADLOCK: Server did not respond within 2s; it is waiting for client half-close!
--- FAIL: TestEval_UnaryWithoutHalfClose (2.00s)
```

The candidate test passes against a server that requires half-close; the eval fixture (which uses `ClientStreams: true`) catches it. Reverted with `git checkout -- server.go && rm test/eval_unary_halfclose_test.go`.

**Verdict: CONFIRMED.** The qualifying test's descriptor makes `SendMsg` emit END_STREAM, so its assertions never exercise completion with the request side open.

## C2

Branch: `evalon/grpc-go-se-c0b0d8e5`. The only added test that creates a server and asserts on `GetServiceInfo` is `TestServerHandwrittenServiceDesc_Dispatch` (`test/server_pipeline_test.go` lines 415-522). Ordering in the file:

```go
469:  srv := grpc.NewServer(grpc.UnaryInterceptor(rec.unary), grpc.StreamInterceptor(rec.stream))
470:  srv.RegisterService(&serviceDesc, nil)
477:  info, ok := srv.GetServiceInfo()[serviceName]
479:      t.Fatalf("GetServiceInfo() missing service %q", serviceName)
482:      t.Fatalf("GetServiceInfo()[%q].Methods = %v, want unary method %v first among %v", ...)
490:      t.Fatalf("GetServiceInfo()[%q] streaming methods mismatch (-want +got):\n%s", ...)
493:  go srv.Serve(lis)
494:  defer srv.Stop()
```

No `t.Cleanup`, `Stop`, or `GracefulStop` exists between line 469 and line 494.

### Instrumented run of the candidate test with the fatal forced

`verify/repro/c2_candidate_test_forced_fatal_instrumentation.patch` (a) registers a `t.Cleanup` right after `NewServer` that checks whether the server is still registered in channelz (`grpc.NewServer` calls `channelz.RegisterServer`; `Stop`/`GracefulStop` remove it) and (b) changes `wantInfo[0].Name` to `"UnaryCall-VERIFY-MUTATED"` so the line-482 fatal fires.

```sh
cd ~/wt/c2 && git apply ~/repos/grpc-go/verify/repro/c2_candidate_test_forced_fatal_instrumentation.patch
go test ./test -run '^Test$/^ServerHandwrittenServiceDesc_Dispatch$' -count=1 -v 2>&1 | grep -v tlogger
```

```console
=== RUN   Test/ServerHandwrittenServiceDesc_Dispatch
    server_pipeline_test.go:495: GetServiceInfo()["grpc.testing.TestService"].Methods = [{UnaryCall false false} {UnaryCall false true} {EmptyCall false false}], want unary method {UnaryCall-VERIFY-MUTATED false false} first among [...]
    server_pipeline_test.go:478: VERIFY-OBSERVED: server channelz id 1 still registered at test exit -> Stop() never called
--- FAIL: Test (0.01s)
    --- FAIL: Test/ServerHandwrittenServiceDesc_Dispatch (0.00s)
```

Unmodified the test passes (`--- PASS: Test/ServerHandwrittenServiceDesc_Dispatch (0.00s)`), confirming the instrumentation changed nothing on the success path. Reverted with `git checkout -- test/server_pipeline_test.go`.

### Standalone repro with control

`verify/repro/c2_fatal_before_cleanup_test.go` mirrors the same ordering and adds a control that registers `t.Cleanup(srv.Stop)` immediately after `NewServer`.

```sh
cp verify/repro/c2_fatal_before_cleanup_test.go ~/wt/c2/test/ && cd ~/wt/c2 && go test ./test -run '^TestVerifyC2_' -count=1 -v
```

```console
=== RUN   TestVerifyC2_FatalBeforeDeferLeavesServerRunning
    verify_c2_fatal_before_cleanup_test.go:83: GetServiceInfo().Methods[0] = {UnaryCall false false}, want {NotTheRealFirstMethod false false} (forced failure: this fatal precedes `defer srv.Stop()`)
    verify_c2_fatal_before_cleanup_test.go:59: OBSERVED: server (channelz id 1) still registered at test exit -> Stop()/GracefulStop() was never called
--- FAIL: TestVerifyC2_FatalBeforeDeferLeavesServerRunning (0.00s)
=== RUN   TestVerifyC2_Control_CleanupBeforeFatalStopsServer
    verify_c2_fatal_before_cleanup_test.go:114: GetServiceInfo().Methods[0] = {UnaryCall false false}, want {NotTheRealFirstMethod false false} (forced failure)
    verify_c2_fatal_before_cleanup_test.go:97: OBSERVED: server (channelz id 2) was stopped before test exit
--- FAIL: TestVerifyC2_Control_CleanupBeforeFatalStopsServer (0.00s)
```

(Both fail by design — the point is the `OBSERVED` line.)

**Verdict: CONFIRMED.** Three reachable `t.Fatalf` calls on `GetServiceInfo` results precede `defer srv.Stop()`; on that path the created server is never stopped. Impact is limited: at that point `Serve` has not been called, so the leak is a channelz entry and the `*grpc.Server` object, not a listener or goroutines.

## C3

Branch: `evalon/grpc-go-se-948dc826`. The substantively changed test is `testClientRequestBodyErrorUnexpectedEOF` in `test/end2end_test.go` (diff vs `0c51461d`). The new synchronization:

```go
st.writeData(1, true, []byte{0, 0, 0, 0, 5})
for {
    f := st.wantAnyFrame()
    hf, ok := f.(*http2.MetaHeadersFrame)
    if !ok { continue }
    ...
    if !hf.StreamEnded() { continue }
    ... // check grpc-status / grpc-message, then return
}
```

`wantAnyFrame` (`test/servertester.go:207-213`) is `f, err := st.fr.ReadFrame(); if err != nil { st.t.Fatal(err) }` — a bare blocking read. The connection comes from `te.e.dialer(te.srvAddr, 10*time.Second)` in `withServerTester` (`end2end_test.go:872-887`): the 10s is a dial timeout only; no read deadline is set anywhere. By contrast the sibling helper `readFrame()` (`servertester.go:70-89`) wraps `ReadFrame` in a 2s timer (`errors.New("timeout waiting for frame")`). The added test file `server_pipeline_ext_test.go` contains no frame reads (`grep -n ReadFrame|http2\. server_pipeline_ext_test.go` → no matches).

### Baseline (unmutated)

```sh
cd ~/wt/c3 && go test ./test -run '^Test$/^ClientRequestBodyErrorUnexpectedEOF$' -count=1 -v
```

```console
--- PASS: Test (0.11s)
ok  	google.golang.org/grpc/test	0.114s
```

### Mutation: server never writes terminal status for a truncated body

`verify/repro/c3_server_never_writes_status_mutation.patch` suppresses `WriteStatus` when the RPC error is `io.ErrUnexpectedEOF` in both places the branch writes it (`serverStream.RecvMsg`'s deferred write in `stream.go`, and `processRPC` in `server.go`). The client then receives WINDOW_UPDATE and PING but never a HEADERS/END_STREAM frame.

```sh
cd ~/wt/c3 && git apply ~/repos/grpc-go/verify/repro/c3_server_never_writes_status_mutation.patch
time go test ./test -run '^Test$/^ClientRequestBodyErrorUnexpectedEOF$/^UnaryCall#02$' -count=1 -v -timeout 20s
```

```console
=== RUN   Test/ClientRequestBodyErrorUnexpectedEOF/UnaryCall#02
    end2end_test.go:4548: Running test in tcp-clear environment...
    end2end_test.go:4556: VERIFY-FRAME *http2.WindowUpdateFrame [FrameHeader WINDOW_UPDATE len=4]
    end2end_test.go:4556: VERIFY-FRAME *http2.PingFrame [FrameHeader PING len=8]
panic: test timed out after 20s
golang.org/x/net/http2.(*Framer).ReadFrame(0xc00011e000)
	/home/ubuntu/go/pkg/mod/golang.org/x/net@v0.57.0/http2/frame.go:572 +0x18
google.golang.org/grpc/test.(*serverTester).wantAnyFrame(0xc0001180e0)
	/home/ubuntu/wt/c3/test/servertester.go:208 +0x25
google.golang.org/grpc/test.testClientRequestBodyErrorUnexpectedEOF.func1.3(0xc0001180e0)
	/home/ubuntu/wt/c3/test/end2end_test.go:4555 +0xb2
google.golang.org/grpc/test.(*test).withServerTester(0xc000212908, 0xc000201f28)
	/home/ubuntu/wt/c3/test/end2end_test.go:886 +0x2ef
FAIL	google.golang.org/grpc/test	20.057s
real	0m21.961s
```

(The `VERIFY-FRAME` log line was a temporary `t.Logf` added to the loop for this run.) The loop sits in `ReadFrame` until the `go test` process deadline kills it (default 10m); the deferred `te.tearDown()` is never reached.

### Contrast: same mutation, loop read swapped for the bounded `readFrame()`

`verify/repro/c3_test_bounded_readFrame_contrast.patch` replaces `st.wantAnyFrame()` in the loop with `st.readFrame()`.

```sh
cd ~/wt/c3 && git apply ~/repos/grpc-go/verify/repro/c3_test_bounded_readFrame_contrast.patch
time go test ./test -run '^Test$/^ClientRequestBodyErrorUnexpectedEOF$/^UnaryCall#02$' -count=1 -v -timeout 60s
```

```console
    end2end_test.go:4559: VERIFY-FRAME *http2.WindowUpdateFrame [FrameHeader WINDOW_UPDATE len=4]
    end2end_test.go:4559: VERIFY-FRAME *http2.PingFrame [FrameHeader PING len=8]
    end2end_test.go:4557: readFrame: timeout waiting for frame
--- FAIL: Test (2.02s)
FAIL	google.golang.org/grpc/test	2.022s
real	0m3.301s
```

Reverted with `git checkout -- server.go stream.go test/end2end_test.go`.

**Verdict: CONFIRMED (both parts).**
- *repeated_completion_inspection*: completion is detected by looping over `wantAnyFrame()` until a `MetaHeadersFrame` with END_STREAM is seen; no channel/WaitGroup is involved.
- *unbounded_frame_read*: each loop read blocks in `Framer.ReadFrame` on a connection with no read deadline; when the terminal frame never arrives the only release is the whole-process `go test -timeout` (observed 20s panic vs. 2s release with the bounded helper).

## C4

Branch: `evalon/grpc-go-se-c0b0d8e5`. `server.go` `register` (lines 814-828):

```go
for i := range sd.Methods {
    d := &sd.Methods[i]
    info.methods[d.MethodName] = d
    info.rpcs[d.MethodName] = &rpcDesc{desc: s.unaryStreamDesc(d), unary: true}
}
for i := range sd.Streams {
    d := &sd.Streams[i]
    info.streams[d.StreamName] = d          // last writer wins -> used by GetServiceInfo
    if _, ok := info.rpcs[d.StreamName]; ok {
        continue                            // first writer wins -> used by handleStream dispatch
    }
    info.rpcs[d.StreamName] = &rpcDesc{desc: d}
}
```

`handleStream` dispatches via `srv.rpcs[method]` (line 1593); `GetServiceInfo` reports flags from `srv.streams` (lines 879-885). Registration does not reject duplicate stream names.

### Repro

`verify/repro/c4_duplicate_stream_test.go` registers `verify.DupService` with two `Dup` streams — FIRST `{ClientStreams: true}` and FINAL `{ServerStreams: true}` — each handler reporting its identity, then invokes `/verify.DupService/Dup` and compares with `GetServiceInfo`.

```sh
cp verify/repro/c4_duplicate_stream_test.go ~/wt/c2/test/ && cd ~/wt/c2 && go test ./test -run '^TestVerifyC4_' -count=1 -v
```

```console
=== RUN   TestVerifyC4_DuplicateStreamNameDispatchVsServiceInfo
    verify_c4_duplicate_stream_test.go:62: GetServiceInfo reports for Dup: [{Name:Dup IsClientStream:false IsServerStream:true}]
    verify_c4_duplicate_stream_test.go:92: DISPATCHED handler: FIRST(ClientStreams=true)
    verify_c4_duplicate_stream_test.go:99: dispatch_retention: dispatch used FIRST(ClientStreams=true) instead of the final descriptor
    verify_c4_duplicate_stream_test.go:111: reflection_dispatch_inconsistency: GetServiceInfo reports {Name:Dup IsClientStream:false IsServerStream:true} but dispatch used descriptor with flags {Name:Dup IsClientStream:true IsServerStream:false}
--- FAIL: TestVerifyC4_DuplicateStreamNameDispatchVsServiceInfo (0.00s)
```

### Control: base commit `0c51461d`

```sh
cp verify/repro/c4_duplicate_stream_test.go ~/wt/base/test/ && cd ~/wt/base && go test ./test -run '^TestVerifyC4_' -count=1 -v
```

```console
    verify_c4_duplicate_stream_test.go:62: GetServiceInfo reports for Dup: [{Name:Dup IsClientStream:false IsServerStream:true}]
    verify_c4_duplicate_stream_test.go:92: DISPATCHED handler: FINAL(ServerStreams=true)
--- PASS: TestVerifyC4_DuplicateStreamNameDispatchVsServiceInfo (0.00s)
```

On the base commit both dispatch and `GetServiceInfo` use the final descriptor (consistent); the branch regresses dispatch to first-wins while reflection stays last-wins.

### Eval fixtures do not cover this

```sh
cd ~/wt/c2 && cp ~/eval_tests/tests/eval_service_info_deterministic_test.go . && cp ~/eval_tests/tests/eval_collision_precedence_test.go ~/eval_tests/tests/eval_descriptor_to_handler_routing_test.go test/
go test . -run '^(TestEval_GetServiceInfoDeterministic|TestEval_SameNameMethodAndStreamDescriptorRegistration)$' -count=1
go test ./test -run '^(TestEval_SameNameMethodAndStreamDispatchPrecedence|TestEval_DescriptorToHandlerRouting)' -count=1
```

```console
ok  	google.golang.org/grpc	0.005s
ok  	google.golang.org/grpc/test	0.008s
```

**Verdict: CONFIRMED (both parts).**
- *dispatch_retention*: the FIRST descriptor's handler runs.
- *reflection_dispatch_inconsistency*: `GetServiceInfo` reports `IsServerStream:true` (the FINAL descriptor) while the dispatched descriptor is `IsClientStream:true`.

Impact reasoning: duplicate stream names in one `ServiceDesc` are unusual (generated code never produces them), but the branch silently turns a previously consistent last-wins policy into a split policy where `GetServiceInfo`/reflection advertise flags that do not match the handler actually executed, and any stats/interceptor consumer reading `StreamServerInfo` flags will see the first descriptor's flags while reflection consumers see the last's. Minimal fix: in `register`, either replace `info.rpcs[d.StreamName]` when the existing entry is a non-unary stream (keep unary precedence only), or `logger.Fatalf` on duplicate stream names as is already done for duplicate services.
