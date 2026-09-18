## Setup

```sh
cd ~/repos/grpc-go
git fetch origin grpc-go-server-unify-unary-stream-rpc-perfect
git checkout -b verify/grpc-go-server-unify-unary-stream-rpc-v-b2fc80a9 origin/grpc-go-server-unify-unary-stream-rpc-perfect
git remote add evals https://github.com/kaitranntt-evals/grpc-go-server-unify-unary-stream-rpc.git
git fetch evals evalon/grpc-go-se-82014a80 evalon/grpc-go-se-2ea61f68 evalon/grpc-go-se-d4895981 evalon/grpc-go-se-f19ffc36
for b in 82014a80 2ea61f68 d4895981 f19ffc36; do git worktree add ../wt-$b evals/evalon/grpc-go-se-$b; done
git worktree add ../wt-base 0c51461d27177d997e14c642fe18c11668fc09a3   # task base commit, used as a control for C3
go version   # go version go1.25.7 linux/amd64
```

Each claim-target branch is a single commit on top of the base commit `0c51461d`. `internal/stubserver` and the `serverStream.RecvMsg` deferred `WriteStatus` are unchanged base code on every branch.

## C1

Claim: an added Go server test reads interceptor error records after a client-side RPC failure without a bounded completion signal that the interceptor has finished recording.

### Mechanism (both branches)

On both branches the test interceptors record *after* the handler returns and *before* the interceptor itself returns; `processRPC` writes the final status only after the interceptor returns. For handler-returned errors that is a valid completion dependency. But `serverStream.RecvMsg` (base code, unchanged on both branches) writes the status itself when a receive fails:

```sh
cd ~/repos/wt-2ea61f68 && sed -n 1897,1921p stream.go
```

```go
func (ss *serverStream) RecvMsg(m any) (err error) {
	defer func() {
		...
		if err != nil && err != io.EOF {
			st, _ := status.FromError(toRPCErr(err))
			ss.s.WriteStatus(st)
```

So for a streaming RPC whose handler `Recv` fails (oversized message after decompression → `ResourceExhausted`), the client observes the final status before the handler has even returned to the interceptor. Reading the interceptor's records after the client-side failure is therefore not ordered after the recording.

### Branch evalon/grpc-go-se-82014a80

Read site: `test/server_rpc_pipeline_test.go` `TestServerRPCPipeline_Failures`, table case `"client streaming oversized message after decompression"` (`interceptorRuns: 1`), read at lines 741-763:

```go
			trailer, err := test.run(ctx, ss.Client)      // client CloseAndRecv() -> ResourceExhausted
			...
			rec.mu.Lock()
			defer rec.mu.Unlock()
			...
				gotErrs = rec.streamErrs
			if len(gotErrs) != test.interceptorRuns {
				t.Fatalf("interceptor ran %d time(s), want %d", len(gotErrs), test.interceptorRuns)
```

Unmodified test is green, including under stress:

```sh
cd ~/repos/wt-82014a80
go test ./test -run 'Test/ServerRPCPipeline_Failures' -count=20 -race
go test ./test -run 'Test/ServerRPCPipeline_Failures/client_streaming' -count=300 -cpu 1,4
```

```console
ok  	google.golang.org/grpc/test	1.598s
ok  	google.golang.org/grpc/test	18.495s
```

Controlled scheduling: delay the test's own interceptor for 500 ms between `handler(...)` returning and the record append (patch `verify/instrumentation/c1-82014a80-delay-recording.patch`, test code only), and log how long the client waited for the RPC to complete:

```sh
cd ~/repos/wt-82014a80
git apply ~/repos/grpc-go/verify/instrumentation/c1-82014a80-delay-recording.patch
go test ./test -run 'Test/ServerRPCPipeline_Failures' -count=1 -race -v 2>&1 | grep -E "server_rpc_pipeline_test.go|--- "
git checkout -- test/server_rpc_pipeline_test.go
```

```console
=== RUN   Test/ServerRPCPipeline_Failures/unary_handler_error
    server_rpc_pipeline_test.go:730: VERIFY: client observed RPC completion after 501.412264ms
=== RUN   Test/ServerRPCPipeline_Failures/bidirectional_streaming_handler_error
    server_rpc_pipeline_test.go:730: VERIFY: client observed RPC completion after 502.735441ms
=== RUN   Test/ServerRPCPipeline_Failures/unary_oversized_message_after_decompression
    server_rpc_pipeline_test.go:730: VERIFY: client observed RPC completion after 4.636144ms
=== RUN   Test/ServerRPCPipeline_Failures/client_streaming_oversized_message_after_decompression
    server_rpc_pipeline_test.go:730: VERIFY: client observed RPC completion after 3.708847ms
    server_rpc_pipeline_test.go:761: interceptor ran 0 time(s), want 1
        --- PASS: Test/ServerRPCPipeline_Failures/unary_handler_error (0.51s)
        --- PASS: Test/ServerRPCPipeline_Failures/bidirectional_streaming_handler_error (0.51s)
        --- PASS: Test/ServerRPCPipeline_Failures/unary_oversized_message_after_decompression (0.01s)
        --- FAIL: Test/ServerRPCPipeline_Failures/client_streaming_oversized_message_after_decompression (0.01s)
```

The two handler-error cases wait the full 500 ms (client completion is gated on the interceptor returning). The client-streaming oversized case completes in ~4 ms and the read of `rec.streamErrs` sees 0 entries while the recording is still pending.

Self-contained repro (parks the interceptor on a channel after the handler returns instead of sleeping; repro files carry a `verify_repro` build tag so they are inert unless `-tags verify_repro` is passed):

```sh
cd ~/repos/wt-82014a80
cp ~/repos/grpc-go/verify/repro/c1_recv_error_status_before_interceptor_test.go test/
go test ./test -run 'Test/VerifyC1_' -tags verify_repro -count=1 -race -v 2>&1 | grep -E "c1_recv_error|--- |^ok|^FAIL"
rm test/c1_recv_error_status_before_interceptor_test.go
```

```console
    c1_recv_error_status_before_interceptor_test.go:84: client observed final status ResourceExhausted after 768.519µs
    c1_recv_error_status_before_interceptor_test.go:89: server handler returned ResourceExhausted
    c1_recv_error_status_before_interceptor_test.go:98: client observed the RPC failure while the stream interceptor had recorded 0 error(s); post-handler recording was still pending (no completion signal)
    --- FAIL: Test/VerifyC1_RecvErrorStatusReachesClientBeforeInterceptorRecords (0.06s)
```

Verdict for this branch: CONFIRMED (the `streamErrs` read in the client-streaming oversized case; the handler-error cases are correctly gated).

### Branch evalon/grpc-go-se-2ea61f68

Read site: `test/server_rpc_pipeline_test.go` `TestServerRejectsOversizedDecompressedMessage`, lines 650-652, executed after the `"bidirectional streaming"` subtest's client `Recv()` returned `ResourceExhausted`:

```go
	if _, err, ok := rec.streamCall(testServiceFullDuplex); !ok || status.Code(err) != codes.ResourceExhausted {
		t.Errorf("stream interceptor observed %q with error %v (invoked: %v), want code %v", ...)
```

Unmodified test is green, including under stress:

```sh
cd ~/repos/wt-2ea61f68
go test ./test -run 'Test/(ServerRejectsOversizedDecompressedMessage|ServerStreamingRPCHandlerErrorPropagation)' -count=20 -race
go test ./test -run 'Test/ServerRejectsOversizedDecompressedMessage' -count=300 -cpu 1,4
```

```console
ok  	google.golang.org/grpc/test	1.729s
ok  	google.golang.org/grpc/test	19.072s
```

Controlled scheduling (patch `verify/instrumentation/c1-2ea61f68-delay-recording.patch`, test code only: 500 ms delay in the recorder between handler return and map write, plus client-side timing logs):

```sh
cd ~/repos/wt-2ea61f68
git apply ~/repos/grpc-go/verify/instrumentation/c1-2ea61f68-delay-recording.patch
go test ./test -run 'Test/(ServerRejectsOversizedDecompressedMessage|ServerStreamingRPCHandlerErrorPropagation)' -count=1 -race -v 2>&1 | grep -E "server_rpc_pipeline_test.go|--- "
git checkout -- test/server_rpc_pipeline_test.go
```

```console
    server_rpc_pipeline_test.go:652: VERIFY: client observed RPC completion after 4.304184ms
    server_rpc_pipeline_test.go:652: VERIFY: client observed RPC completion after 2.41992ms
    server_rpc_pipeline_test.go:659: stream interceptor observed "/grpc.testing.TestService/FullDuplexCall" with error <nil> (invoked: false), want code ResourceExhausted
    server_rpc_pipeline_test.go:588: VERIFY: client observed RPC completion after 502.212216ms
    --- FAIL: Test/ServerRejectsOversizedDecompressedMessage (0.52s)
        --- PASS: Test/ServerRejectsOversizedDecompressedMessage/unary (0.00s)
        --- PASS: Test/ServerRejectsOversizedDecompressedMessage/bidirectional_streaming (0.00s)
    --- PASS: Test/ServerStreamingRPCHandlerErrorPropagation (0.51s)
```

`TestServerStreamingRPCHandlerErrorPropagation` (handler-returned error) waits the full 500 ms and stays green: its read is properly gated. The decompression test's bidirectional case completes in ~2 ms and the record read finds the interceptor "not invoked".

Self-contained repro:

```sh
cd ~/repos/wt-2ea61f68
cp ~/repos/grpc-go/verify/repro/c1_recv_error_status_before_interceptor_test.go test/
go test ./test -run 'Test/VerifyC1_' -tags verify_repro -count=1 -race -v 2>&1 | grep -E "c1_recv_error|--- |^ok|^FAIL"
rm test/c1_recv_error_status_before_interceptor_test.go
```

```console
    c1_recv_error_status_before_interceptor_test.go:84: client observed final status ResourceExhausted after 638.64µs
    c1_recv_error_status_before_interceptor_test.go:89: server handler returned ResourceExhausted
    c1_recv_error_status_before_interceptor_test.go:98: client observed the RPC failure while the stream interceptor had recorded 0 error(s); post-handler recording was still pending (no completion signal)
    --- FAIL: Test/VerifyC1_RecvErrorStatusReachesClientBeforeInterceptorRecords (0.01s)
```

Verdict for this branch: CONFIRMED.

### Impact reasoning

The unsynchronized read only concerns the receive-failure (oversized-after-decompression) streaming cases; every read that follows a handler-returned error is ordered by `processRPC`'s `WriteStatus`. In 300 × 2 stress iterations per branch the tests never failed naturally, because the interceptor records within microseconds of the handler returning while the status travels through the loopback transport — the window is real but small. It opens whenever the server goroutine is descheduled between handler return and record write (loaded CI, `-race`, GC pause), producing a spurious "interceptor ran 0 time(s)" / "invoked: false" failure with no product bug behind it.

## C2

Claim: an added Go server test leaves an explicitly created server unstopped when listener setup fails before server cleanup is registered. Branch: evalon/grpc-go-se-d4895981.

Only explicit server creation site in the changed tests (`server_stream_pipeline_test.go`, `TestServerStreamPipeline`, lines 179-185):

```go
	server := grpc.NewServer(grpc.UnaryInterceptor(unaryInterceptor), grpc.StreamInterceptor(streamInterceptor), grpc.UnknownServiceHandler(streamHandler(true, true)))
	server.RegisterService(desc, impl)
	ss := &stubserver.StubServer{S: server}
	if err := ss.Start(nil); err != nil {
		t.Fatal(err)
	}
	defer ss.Stop()
```

`StubServer.setupServer` (base code) calls `net.Listen` first and only appends `ss.S.Stop` to its cleanups afterwards (`internal/stubserver/stubserver.go` lines 152-172), so on a listen failure `Start` returns before anything can stop the caller-supplied server, and no cleanup was registered by the test before `Start`.

Induced failure in the actual test (patch `verify/instrumentation/c2-d4895981-induce-listen-failure.patch`: sets `Network: "bogus-network"` on the StubServer and registers a `t.Cleanup` right after `grpc.NewServer` that calls `server.Serve` on a fresh listener — a stopped server returns `ErrServerStopped` immediately, an unstopped one blocks):

```sh
cd ~/repos/wt-d4895981
git apply ~/repos/grpc-go/verify/instrumentation/c2-d4895981-induce-listen-failure.patch
go test . -run 'Test/ServerStreamPipeline$' -count=1 -v 2>&1 | grep -E "server_stream_pipeline_test.go|--- |^ok|^FAIL"
git checkout -- server_stream_pipeline_test.go
```

```console
    server_stream_pipeline_test.go:202: net.Listen("bogus-network", "localhost:0") = listen bogus-network: unknown network bogus-network
    server_stream_pipeline_test.go:194: VERIFY: server.Serve still running after 500ms: explicitly created server was never stopped
--- FAIL: Test (0.50s)
    --- FAIL: Test/ServerStreamPipeline (0.50s)
```

Self-contained repro mirroring the same sequence:

```sh
cd ~/repos/wt-d4895981
cp ~/repos/grpc-go/verify/repro/c2_unstopped_server_on_listen_failure_test.go .
go test . -run 'Test/VerifyC2_' -tags verify_repro -count=1 -v 2>&1 | grep -E "c2_unstopped|--- |^ok|^FAIL"
rm c2_unstopped_server_on_listen_failure_test.go
```

```console
    c2_unstopped_server_on_listen_failure_test.go:46: ss.Start(nil) = net.Listen("bogus-network", "localhost:0") = listen bogus-network: unknown network bogus-network; cleanup registered: false
    c2_unstopped_server_on_listen_failure_test.go:69: explicitly created server was NOT stopped after listener setup failure: Serve is still running 500ms later
    --- FAIL: Test/VerifyC2_ExplicitServerLeftUnstoppedWhenListenFails (0.50s)
```

Verdict: CONFIRMED.

### Impact reasoning

The unstopped server has never served, so with default options it owns no goroutines, sockets or worker channels; what leaks is the `grpc.Server` object and its channelz ID for the remainder of the test binary, and only on a path that already fails the test with `t.Fatal`. `grpctest`'s goroutine leak checker does not observe it. Test-hygiene issue, not a product defect.

## C3

Claim: a streaming RPC leaves its receive buffer unreleased when `codec.Unmarshal` panics in `serverStream.RecvMsg` and a streaming interceptor recovers the panic. Branch: evalon/grpc-go-se-f19ffc36.

Code under test (`stream.go` `serverStream.RecvMsg` on the branch; the base commit used `recv()` with `defer data.Free()`):

```go
	data, err := recvAndDecompress(&ss.p, ss.s, ss.decompressorV0, ss.maxReceiveMessageSize, payInfo, ss.decompressorV1, true)
	if err == nil {
		err = ss.codec.Unmarshal(data, m)
		data.Free()
```

Repro: bidirectional streaming RPC, `grpc.ForceServerCodecV2` with a codec whose `Unmarshal` panics, `grpc.StreamInterceptor` that `recover()`s and returns `codes.Internal`, `experimental.BufferPool` with a Get/Put counting pool, 8 KiB request so the transport takes the request bytes from the pool. Control run: same setup but `Unmarshal` returns an error.

```sh
cd ~/repos/wt-f19ffc36
cp ~/repos/grpc-go/verify/repro/c3_recv_buffer_leak_on_unmarshal_panic_test.go test/
go test ./test -run 'Test/VerifyC3_' -tags verify_repro -count=1 -race -v 2>&1 | grep -E "c3_recv_buffer|--- |^ok|^FAIL|never freed"
rm test/c3_recv_buffer_leak_on_unmarshal_panic_test.go
```

```console
    c3_recv_buffer_leak_on_unmarshal_panic_test.go:141: panic run (Unmarshal panics, interceptor recovers): status=Internal msg="recovered: verify: codec.Unmarshal panics" pool gets=1 puts=0
    c3_recv_buffer_leak_on_unmarshal_panic_test.go:146: receive_buffer_release: 1 buffer(s) taken from the pool were never returned after codec.Unmarshal panicked (gets=1 puts=0)
    grpctest.go:40: WARNING 1 allocated buffers never freed:
        google.golang.org/grpc/internal/leakcheck.(*swappableBufferPool).Get
        google.golang.org/grpc/test.(*countingPool).Get
        google.golang.org/grpc/internal/transport.(*framer).readDataFrame
        	/home/ubuntu/repos/wt-f19ffc36/internal/transport/http_util.go:551
        google.golang.org/grpc/internal/transport.(*http2Server).HandleStreams
    c3_recv_buffer_leak_on_unmarshal_panic_test.go:130: control (Unmarshal returns error): status=Internal msg="grpc: failed to unmarshal the received message: verify: codec.Unmarshal fails" pool gets=1 puts=1
    --- FAIL: Test/VerifyC3_RecvBufferLeakWhenUnmarshalPanics (0.21s)
    --- PASS: Test/VerifyC3_RecvBufferReleasedWhenUnmarshalFails_Control (0.20s)
```

- *recoverable_streaming_rpc_path*: the handler was reached (`reached` channel), `stream.Recv()` entered `serverStream.RecvMsg`, the codec panicked, the stream interceptor recovered, and the client received `Internal` with message `recovered: verify: codec.Unmarshal panics`. Part holds.
- *receive_buffer_release*: pool `gets=1 puts=0` after RPC completion and `ss.Stop()`; grpctest's own buffer leak checker independently reports `1 allocated buffers never freed` allocated in `framer.readDataFrame`. Control run with a plain `Unmarshal` error releases the buffer (`gets=1 puts=1`). Part holds.

Regression check against the task base commit (which uses `defer data.Free()`):

```sh
cd ~/repos/wt-base
cp ~/repos/grpc-go/verify/repro/c3_recv_buffer_leak_on_unmarshal_panic_test.go test/
go test ./test -run 'Test/VerifyC3_' -tags verify_repro -count=1 -race -v 2>&1 | grep -E "c3_recv_buffer|--- |^ok|^FAIL|never freed"
rm test/c3_recv_buffer_leak_on_unmarshal_panic_test.go
```

```console
    c3_recv_buffer_leak_on_unmarshal_panic_test.go:141: panic run (Unmarshal panics, interceptor recovers): status=Internal msg="recovered: verify: codec.Unmarshal panics" pool gets=1 puts=1
    c3_recv_buffer_leak_on_unmarshal_panic_test.go:130: control (Unmarshal returns error): status=Internal msg="grpc: failed to unmarshal the received message: verify: codec.Unmarshal fails" pool gets=1 puts=1
    --- PASS: Test/VerifyC3_RecvBufferLeakWhenUnmarshalPanics (0.21s)
    --- PASS: Test/VerifyC3_RecvBufferReleasedWhenUnmarshalFails_Control (0.20s)
```

Verdict: CONFIRMED (both parts). The branch regresses base behavior.

### Impact reasoning

Requires a codec whose `Unmarshal` panics *and* a stream interceptor that recovers panics — the latter is common (panic-recovery middleware is standard in production gRPC servers), the former is a codec bug. When both hold, each such RPC leaks one pooled receive buffer (≥ 1 KiB messages) back to the GC instead of the pool: no unbounded growth, but pool hit rate degrades under a repeating fault. The same pattern also affects a codec that panics on well-formed input (e.g. a nil-pointer bug), where the recovering interceptor would otherwise make the server fully resilient. Fix: `defer data.Free()` immediately after a successful `recvAndDecompress`, as the base `recv()` did.
