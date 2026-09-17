## Setup

All commands were run on this machine with `go version go1.25.7 linux/amd64`.

```sh
cd ~/repos/grpc-go
git fetch origin grpc-go-server-unify-unary-stream-rpc-perfect
git checkout -b verify/grpc-go-server-unify-unary-stream-rpc-v-49780564 origin/grpc-go-server-unify-unary-stream-rpc-perfect
git remote add evalrepo https://github.com/kaitranntt-evals/grpc-go-server-unify-unary-stream-rpc.git
git fetch evalrepo evalon/grpc-go-se-82014a80 evalon/grpc-go-se-2ea61f68 evalon/grpc-go-se-d4895981 evalon/grpc-go-se-6233a68e evalon/grpc-go-se-2e6a7cdc
for b in 82014a80 2ea61f68 d4895981 6233a68e 2e6a7cdc; do git worktree add ~/wt/$b evalrepo/evalon/grpc-go-se-$b; done
```

Worktree heads: `82014a80` = `d5d70164`, `2ea61f68` = `21c5f64f`, `d4895981` = `3880d970`, `6233a68e` = `bd409814`, `2e6a7cdc` = `bd089249` (all "chore: apply eval changes").

Instrumentation used to gather evidence is stored as patches under `verify/repro/` and was applied to the target worktrees only for the duration of a run (`git apply` … `git checkout -- <file>`); `git status --short` was empty in every worktree afterwards.

## C1

### Mechanism (why the ordering matters)

For a streaming RPC whose failure originates in a receive error (oversized message after decompression), `serverStream.RecvMsg` writes the RPC status to the client from inside `RecvMsg`, before the handler returns and therefore before any post-handler code in a stream interceptor runs (`stream.go` on the audited branch, `RecvMsg` deferred block):

```go
if err != nil && err != io.EOF {
	st, _ := status.FromError(toRPCErr(err))
	ss.s.WriteStatus(st)
	// Non-user specified status was sent out. ...
	// This behavior is similar to an interceptor.
}
```

So a client-visible `ResourceExhausted` on `Recv`/`CloseAndRecv` does not imply the interceptor's post-handler append has happened. Both candidate branches append to their recorder after `handler(srv, ss)` returns and read the recorder immediately after the client sees the error, with only a mutex (no completion channel/WaitGroup/poll).

### Repro test (runs against any branch)

`verify/repro/c1_post_handler_record_race_test.go` contains two tests:

- `TestVerify_C1_ClientSeesRecvErrorBeforePostHandlerRecord` — the interceptor's post-handler step blocks on a channel the client closes only after `Recv` returned `ResourceExhausted` (bounded by 5 s). If the status could only reach the client after the interceptor returned, `Recv` would block until the timeout.
- `TestVerify_C1_DelayedRecordBreaksPostErrorAssertion` — reproduces the candidate recorder shape (mutex, append after handler return) with a 200 ms scheduling delay before the append, then performs the candidate-style read right after `CloseAndRecv`. It intentionally asserts the candidates' expectation (`want 1`) so the failure shows the recorder is still empty.

Run on the audited branch (`~/repos/grpc-go`, HEAD `e363acbc`):

```sh
cd ~/repos/grpc-go && cp verify/repro/c1_post_handler_record_race_test.go test/ && go test ./test -run 'Test/Verify_C1_' -count=1 -v; rm test/c1_post_handler_record_race_test.go
```

```console
=== RUN   Test/Verify_C1_ClientSeesRecvErrorBeforePostHandlerRecord
    c1_post_handler_record_race_test.go:87: client Recv() returned ResourceExhausted after 221.429µs
    c1_post_handler_record_race_test.go:89: interceptor: client observed the error BEFORE the post-handler record was appended
=== RUN   Test/Verify_C1_DelayedRecordBreaksPostErrorAssertion
    c1_post_handler_record_race_test.go:143: interceptor ran 0 time(s) as observed right after the client error, want 1 (record not yet appended)
--- FAIL: Test (0.21s)
    --- PASS: Test/Verify_C1_ClientSeesRecvErrorBeforePostHandlerRecord (0.00s)
    --- FAIL: Test/Verify_C1_DelayedRecordBreaksPostErrorAssertion (0.21s)
FAIL	google.golang.org/grpc/test	0.216s
```

### Branch evalon/grpc-go-se-82014a80

Target test: `test/server_rpc_pipeline_test.go`, `TestServerRPCPipeline_Failures`, sub-case `"client streaming oversized message after decompression"` (`interceptorRuns: 1`, `wantCode: codes.ResourceExhausted`). Recorder (`serverInterceptorRecorder`) appends after the handler returns:

```go
grpc.StreamInterceptor(func(srv any, ss grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
	err := handler(srv, ss)
	r.mu.Lock()
	defer r.mu.Unlock()
	r.stream = append(r.stream, info)
	r.streamErrs = append(r.streamErrs, err)
	return err
}),
```

The test reads the recorder right after `test.run` (which ends with `stream.CloseAndRecv()`) returns the client error, with only `rec.mu.Lock()`:

```go
trailer, err := test.run(ctx, ss.Client)
...
rec.mu.Lock()
defer rec.mu.Unlock()
...
if len(gotErrs) != test.interceptorRuns {
	t.Fatalf("interceptor ran %d time(s), want %d", len(gotErrs), test.interceptorRuns)
}
```

Baseline (unmodified) run:

```sh
cd ~/wt/82014a80 && go test ./test -run 'Test/ServerRPCPipeline_Failures' -count=1 -v
```

```console
--- PASS: Test (0.01s)
    --- PASS: Test/ServerRPCPipeline_Failures (0.01s)
        --- PASS: Test/ServerRPCPipeline_Failures/unary_handler_error (0.00s)
        --- PASS: Test/ServerRPCPipeline_Failures/bidirectional_streaming_handler_error (0.00s)
        --- PASS: Test/ServerRPCPipeline_Failures/unary_oversized_message_after_decompression (0.00s)
        --- PASS: Test/ServerRPCPipeline_Failures/client_streaming_oversized_message_after_decompression (0.00s)
ok  	google.golang.org/grpc/test	0.012s
```

Mutation: `verify/repro/c1_82014a80_delay.patch` adds one line to the test file's interceptor, `time.Sleep(200 * time.Millisecond)` between `handler(srv, ss)` returning and the append (a scheduling delay; nothing else changes).

```sh
cd ~/wt/82014a80 && git apply ~/repos/grpc-go/verify/repro/c1_82014a80_delay.patch && go test ./test -run 'Test/ServerRPCPipeline_Failures' -count=1 -v; git checkout -- test/server_rpc_pipeline_test.go
```

```console
=== RUN   Test/ServerRPCPipeline_Failures/client_streaming_oversized_message_after_decompression
    server_rpc_pipeline_test.go:758: interceptor ran 0 time(s), want 1
--- FAIL: Test (0.41s)
    --- FAIL: Test/ServerRPCPipeline_Failures (0.41s)
        --- PASS: Test/ServerRPCPipeline_Failures/unary_handler_error (0.00s)
        --- PASS: Test/ServerRPCPipeline_Failures/bidirectional_streaming_handler_error (0.20s)
        --- PASS: Test/ServerRPCPipeline_Failures/unary_oversized_message_after_decompression (0.00s)
        --- FAIL: Test/ServerRPCPipeline_Failures/client_streaming_oversized_message_after_decompression (0.00s)
FAIL	google.golang.org/grpc/test	0.418s
```

The `bidirectional_streaming_handler_error` case keeps passing with the delay (it takes 0.20 s: its status is written after the interceptor returns, so the client-visible error does imply the append). Only the receive-error case is unsynchronized.

Repro test on this branch:

```sh
cd ~/wt/82014a80 && cp ~/repos/grpc-go/verify/repro/c1_post_handler_record_race_test.go test/ && go test ./test -run 'Test/Verify_C1_' -count=1 -v; rm test/c1_post_handler_record_race_test.go
```

```console
    c1_post_handler_record_race_test.go:87: client Recv() returned ResourceExhausted after 188.216µs
    c1_post_handler_record_race_test.go:89: interceptor: client observed the error BEFORE the post-handler record was appended
    c1_post_handler_record_race_test.go:143: interceptor ran 0 time(s) as observed right after the client error, want 1 (record not yet appended)
    --- PASS: Test/Verify_C1_ClientSeesRecvErrorBeforePostHandlerRecord (0.00s)
    --- FAIL: Test/Verify_C1_DelayedRecordBreaksPostErrorAssertion (0.21s)
```

Verdict for this branch: CONFIRMED.

### Branch evalon/grpc-go-se-2ea61f68

Target test: `test/server_rpc_pipeline_test.go`, `TestServerRejectsOversizedDecompressedMessage` (sub-case `"bidirectional streaming"` sends an oversized compressed payload over `FullDuplexCall`, then the parent test asserts the stream interceptor recorded `ResourceExhausted`). Recorder (`interceptorRecorder`, map + mutex) records after the handler returns:

```go
func (r *interceptorRecorder) streamInterceptor(srv any, ss grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
	err := handler(srv, ss)
	r.mu.Lock()
	defer r.mu.Unlock()
	r.streamInfos[info.FullMethod] = info
	r.streamErrs[info.FullMethod] = err
	return err
}
```

The assertion runs right after the subtests, with `rec.streamCall` taking only the mutex:

```go
for _, tc := range tests {
	t.Run(tc.name, func(t *testing.T) {
		...
		if err := tc.call(ctx, ss.Client); status.Code(err) != codes.ResourceExhausted {
			t.Fatalf("RPC returned error %v, want code %v", err, codes.ResourceExhausted)
		}
	})
}
if _, err, ok := rec.streamCall(testServiceFullDuplex); !ok || status.Code(err) != codes.ResourceExhausted {
	t.Errorf("stream interceptor observed %q with error %v (invoked: %v), want code %v", testServiceFullDuplex, err, ok, codes.ResourceExhausted)
}
```

(`TestServerStreamingRPCHandlerErrorPropagation` also reads the recorder after a streaming failure, but its failure is a handler-returned error, whose status is written after the interceptor returns; it is included in the runs below to show that it is not affected.)

Baseline (unmodified) run:

```sh
cd ~/wt/2ea61f68 && go test ./test -run 'Test/ServerRejectsOversizedDecompressedMessage|Test/ServerStreamingRPCHandlerErrorPropagation' -count=1 -v
```

```console
--- PASS: Test (0.01s)
    --- PASS: Test/ServerRejectsOversizedDecompressedMessage (0.00s)
        --- PASS: Test/ServerRejectsOversizedDecompressedMessage/unary (0.00s)
        --- PASS: Test/ServerRejectsOversizedDecompressedMessage/bidirectional_streaming (0.00s)
    --- PASS: Test/ServerStreamingRPCHandlerErrorPropagation (0.00s)
ok  	google.golang.org/grpc/test	0.014s
```

Mutation: `verify/repro/c1_2ea61f68_delay.patch` adds one line, `time.Sleep(200 * time.Millisecond)`, between `handler(srv, ss)` returning and the map insert in `streamInterceptor`.

```sh
cd ~/wt/2ea61f68 && git apply ~/repos/grpc-go/verify/repro/c1_2ea61f68_delay.patch && go test ./test -run 'Test/ServerRejectsOversizedDecompressedMessage|Test/ServerStreamingRPCHandlerErrorPropagation' -count=1 -v; git checkout -- test/server_rpc_pipeline_test.go
```

```console
=== RUN   Test/ServerRejectsOversizedDecompressedMessage/bidirectional_streaming
    server_rpc_pipeline_test.go:653: stream interceptor observed "/grpc.testing.TestService/FullDuplexCall" with error <nil> (invoked: false), want code ResourceExhausted
--- FAIL: Test (0.47s)
    --- FAIL: Test/ServerRejectsOversizedDecompressedMessage (0.21s)
        --- PASS: Test/ServerRejectsOversizedDecompressedMessage/unary (0.00s)
        --- PASS: Test/ServerRejectsOversizedDecompressedMessage/bidirectional_streaming (0.00s)
    --- PASS: Test/ServerStreamingRPCHandlerErrorPropagation (0.25s)
FAIL	google.golang.org/grpc/test	0.471s
```

The subtest itself passes (the client did get `ResourceExhausted`); the parent's post-error recorder read finds no record (`invoked: false`). `TestServerStreamingRPCHandlerErrorPropagation` still passes with the delay.

Repro test on this branch:

```sh
cd ~/wt/2ea61f68 && cp ~/repos/grpc-go/verify/repro/c1_post_handler_record_race_test.go test/ && go test ./test -run 'Test/Verify_C1_' -count=1 -v; rm test/c1_post_handler_record_race_test.go
```

```console
    c1_post_handler_record_race_test.go:87: client Recv() returned ResourceExhausted after 334.08µs
    c1_post_handler_record_race_test.go:89: interceptor: client observed the error BEFORE the post-handler record was appended
    c1_post_handler_record_race_test.go:143: interceptor ran 0 time(s) as observed right after the client error, want 1 (record not yet appended)
    --- PASS: Test/Verify_C1_ClientSeesRecvErrorBeforePostHandlerRecord (0.00s)
    --- FAIL: Test/Verify_C1_DelayedRecordBreaksPostErrorAssertion (0.21s)
```

Verdict for this branch: CONFIRMED.

### Impact reasoning

- The unmodified tests pass on both branches because the interceptor goroutine normally wins the race by microseconds; the mutex prevents a data race but provides no ordering. Any scheduling hiccup (loaded CI, `-race`, `-cpu 1`, GC pause) between `handler()` returning and the append makes the assertion fail with "interceptor ran 0 time(s)" / "invoked: false" — a flaky failure that looks like a real interceptor regression in the unified pipeline.
- Conversely, the assertion cannot catch a regression where the post-handler record is dropped: an empty recorder is indistinguishable from "not yet appended".
- Fix: signal completion from the interceptor (close a channel / `sync.WaitGroup.Done` after the append) and wait for it with a bounded timeout before reading the recorder, or poll the recorder under the mutex until the expected count is reached or the context deadline expires.

## C2

### Mechanism

`internal/stubserver/stubserver.go` (identical on both branches and on the audited branch):

```go
func (ss *StubServer) setupServer(sopts ...grpc.ServerOption) (net.Listener, error) {
	...
	lis := ss.Listener
	if lis == nil {
		var err error
		lis, err = net.Listen(ss.Network, ss.Address)
		if err != nil {
			return nil, fmt.Errorf("net.Listen(%q, %q) = %v", ss.Network, ss.Address, err)
		}
	}
	...
	if ss.S == nil {
		ss.S = grpc.NewServer(sopts...)
	}
	...
	ss.cleanups = append(ss.cleanups, ss.S.Stop)
	return lis, nil
}

func (ss *StubServer) Stop() {
	for i := len(ss.cleanups) - 1; i >= 0; i-- {
		ss.cleanups[i]()
	}
	ss.cleanups = nil
}
```

`ss.S.Stop` is only appended to `cleanups` after `net.Listen` succeeds. When a caller supplies its own `grpc.NewServer` through `StubServer.S` and `net.Listen` fails, `StubServer.Start` returns the error with `cleanups` still empty, so nothing in the StubServer will ever call `Stop` on that server.

Observation method: `channelz.TurnOn()` and count `channelz.GetServers(0, 1<<20)` before `grpc.NewServer` and at test exit; `grpc.NewServer` registers the server in channelz and `Server.Stop` removes it, so a count that stays at +1 at test exit means the server was never stopped. Listener failure is induced by setting `Network: "bogus-network"` on the StubServer (nothing else in the test changes). The instrumentation's own `t.Cleanup` records the count and only then calls `server.Stop()` itself so the test binary does not leak.

### Branch evalon/grpc-go-se-d4895981

Target test: `server_stream_pipeline_test.go`, `TestServerStreamPipeline`:

```go
server := grpc.NewServer(grpc.UnaryInterceptor(unaryInterceptor), grpc.StreamInterceptor(streamInterceptor), grpc.UnknownServiceHandler(streamHandler(true, true)))
server.RegisterService(desc, impl)
ss := &stubserver.StubServer{S: server}
if err := ss.Start(nil); err != nil {
	t.Fatal(err)
}
defer ss.Stop()
```

Cleanup (`defer ss.Stop()`) is arranged only after the fallible `ss.Start(nil)`; `t.Fatal` on the failure path exits with neither `ss.Stop` nor `server.Stop` registered.

Baseline (unmodified) run:

```sh
cd ~/wt/d4895981 && go test . -run 'Test/ServerStreamPipeline$' -count=1 -v
```

```console
--- PASS: Test (0.01s)
    --- PASS: Test/ServerStreamPipeline (0.01s)
        --- PASS: Test/ServerStreamPipeline//pipeline.Service/Unary (0.00s)
        ...
        --- PASS: Test/ServerStreamPipeline//unknown.Service/UnknownMethod (0.00s)
ok  	google.golang.org/grpc	0.013s
```

Induced listener failure: `verify/repro/c2_d4895981_induce_listen_failure.patch`.

```sh
cd ~/wt/d4895981 && git apply ~/repos/grpc-go/verify/repro/c2_d4895981_induce_listen_failure.patch && go test . -run 'Test/ServerStreamPipeline$' -count=1 -v; git checkout -- server_stream_pipeline_test.go
```

```console
=== RUN   Test/ServerStreamPipeline
    server_stream_pipeline_test.go:194: net.Listen("bogus-network", "localhost:0") = listen bogus-network: unknown network bogus-network
    server_stream_pipeline_test.go:188: VERIFY C2: channelz servers before NewServer=0, at test exit=1; server stopped by test: false
--- FAIL: Test (0.00s)
    --- FAIL: Test/ServerStreamPipeline (0.00s)
FAIL	google.golang.org/grpc	0.005s
```

Verdict for this branch: CONFIRMED — the explicitly created server is still registered at test exit; no cleanup of any kind ran for it.

### Branch evalon/grpc-go-se-6233a68e

Target helper: `server_unified_test.go`, `startPipelineServer` (used by `TestServerUnifiedPipeline` for both the `Serve` and `ServeHTTP` variants):

```go
func startPipelineServer(t *testing.T, handlerTransport bool, desc *grpc.ServiceDesc, opts ...grpc.ServerOption) *stubserver.StubServer {
	t.Helper()
	ss := &stubserver.StubServer{S: grpc.NewServer(opts...)}
	ss.S.RegisterService(desc, struct{}{})
	t.Cleanup(ss.Stop)
	if handlerTransport {
		if err := ss.StartHandlerServer(); err != nil {
			t.Fatal(err)
		}
		if err := ss.StartClient(); err != nil {
			t.Fatal(err)
		}
	} else if err := ss.Start(nil); err != nil {
		t.Fatal(err)
	}
	return ss
}
```

Here `t.Cleanup(ss.Stop)` is registered before the fallible start, but on the `net.Listen` failure path `ss.cleanups` is empty, so `ss.Stop` runs and does nothing; the explicitly created server is never stopped.

Baseline (unmodified) run:

```sh
cd ~/wt/6233a68e && go test . -run 'Test/ServerUnifiedPipeline$' -count=1 -v
```

```console
--- PASS: Test (0.01s)
    --- PASS: Test/ServerUnifiedPipeline (0.01s)
        --- PASS: Test/ServerUnifiedPipeline/Serve (0.00s)
        ...
        --- PASS: Test/ServerUnifiedPipeline/ServeHTTP (0.00s)
        ...
ok  	google.golang.org/grpc	0.013s
```

Induced listener failure: `verify/repro/c2_6233a68e_induce_listen_failure.patch` (also wraps the existing `t.Cleanup(ss.Stop)` in a log line to prove it is invoked).

```sh
cd ~/wt/6233a68e && git apply ~/repos/grpc-go/verify/repro/c2_6233a68e_induce_listen_failure.patch && go test . -run 'Test/ServerUnifiedPipeline$' -count=1 -v; git checkout -- server_unified_test.go
```

```console
=== RUN   Test/ServerUnifiedPipeline/Serve
    server_unified_test.go:185: net.Listen("bogus-network", "localhost:0") = listen bogus-network: unknown network bogus-network
    server_unified_test.go:71: VERIFY C2: t.Cleanup(ss.Stop) invoked
    server_unified_test.go:67: VERIFY C2: channelz servers before NewServer=0, at test exit (after t.Cleanup(ss.Stop) ran)=1; server stopped by test: false
=== RUN   Test/ServerUnifiedPipeline/ServeHTTP
    server_unified_test.go:185: net.Listen("bogus-network", "localhost:0") = listen bogus-network: unknown network bogus-network
    server_unified_test.go:71: VERIFY C2: t.Cleanup(ss.Stop) invoked
    server_unified_test.go:67: VERIFY C2: channelz servers before NewServer=0, at test exit (after t.Cleanup(ss.Stop) ran)=1; server stopped by test: false
--- FAIL: Test (0.00s)
    --- FAIL: Test/ServerUnifiedPipeline (0.00s)
        --- FAIL: Test/ServerUnifiedPipeline/Serve (0.00s)
        --- FAIL: Test/ServerUnifiedPipeline/ServeHTTP (0.00s)
FAIL	google.golang.org/grpc	0.005s
```

Verdict for this branch: CONFIRMED as to the suspected problem (the explicitly created server is left unstopped after an early `net.Listen` failure — channelz still shows it after the test's cleanup ran). Note the nuance against the claim's wording: this branch *does* register `t.Cleanup(ss.Stop)` before the fallible setup, and that cleanup *is* invoked; it is ineffective because `StubServer.Stop` only runs cleanups appended by a successful `setupServer`, and a server supplied via `StubServer.S` is never added to that list on the failure path. The ordering is right; the cleanup target is wrong (`ss.Stop` instead of `ss.S.Stop`).

### Impact reasoning

- Trigger: `net.Listen("tcp", "localhost:0")` failing in a test run — uncommon (fd exhaustion, sandbox without loopback), and the test is already failing via `t.Fatal` at that point, so the practical effect is a leaked `*grpc.Server` (channelz entry, registered services, options) for the remainder of the test binary, not a wrong pass/fail result. Servers created by `grpc.NewServer` alone spawn no goroutines, so the leak is memory/channelz-only unless `NumStreamWorkers` is set.
- Fix on `d4895981`: register `t.Cleanup(server.Stop)` (or `defer server.Stop()`) immediately after `grpc.NewServer`, before `ss.Start`. Fix on `6233a68e`: `t.Cleanup(ss.S.Stop)` in addition to (or instead of) `t.Cleanup(ss.Stop)` — or let StubServer create the server (`ss.Start(opts)`) so `setupServer` owns its lifetime. Alternatively, fix `internal/stubserver` to append `ss.S.Stop` before `net.Listen` when `ss.S` was supplied by the caller.

## C3

Target: `server_rpc_ext_test.go` on `evalon/grpc-go-se-2e6a7cdc`, `interceptorRecorder.check`:

```go
func (r *interceptorRecorder) check(t *testing.T, wantUnary, wantStream []interceptorRecord) {
	t.Helper()
	r.mu.Lock()
	defer r.mu.Unlock()
	sortRecords := cmpopts.SortSlices(func(a, b interceptorRecord) bool {
		if a.Method != b.Method {
			return a.Method < b.Method
		}
		if a.IsClientStream != b.IsClientStream {
			return !a.IsClientStream
		}
		return !a.IsServerStream
	})
	if diff := cmp.Diff(wantUnary, r.unary, sortRecords, cmpopts.EquateEmpty()); diff != "" { ... }
	if diff := cmp.Diff(wantStream, r.stream, sortRecords, cmpopts.EquateEmpty()); diff != "" { ... }
}
```

With `a == b`, the first two branches are skipped and the result is `!a.IsServerStream`, i.e. `true` whenever `IsServerStream == false`.

The contract being violated — `cmpopts.SortSlices` doc in the module used by this branch (`github.com/google/go-cmp v0.7.0`, `$(go env GOMODCACHE)/github.com/google/go-cmp@v0.7.0/cmp/cmpopts/sort.go`):

```go
// A less function must be:
//   - Deterministic: less(x, y) == less(x, y)
//   - Irreflexive: !less(x, x)
//   - Transitive: if !less(x, y) and !less(y, z), then !less(x, z)
```

Direct exercise: `verify/repro/c3_comparator_irreflexive_test.go` copies the comparator verbatim (`verifyC3Less`) and calls it with the same record as both operands for all four flag combinations.

```sh
cd ~/wt/2e6a7cdc && cp ~/repos/grpc-go/verify/repro/c3_comparator_irreflexive_test.go . && go test . -run 'Test/Verify_C3_' -count=1 -v; rm c3_comparator_irreflexive_test.go
```

```console
=== RUN   Test/Verify_C3_ComparatorIsIrreflexive
    c3_comparator_irreflexive_test.go:43: less(x, x) for {Method:/grpc.testing.TestService/StreamingInputCall IsClientStream:true IsServerStream:false} = true
    c3_comparator_irreflexive_test.go:45: comparator reports {Method:/grpc.testing.TestService/StreamingInputCall IsClientStream:true IsServerStream:false} as less than itself (IsServerStream=false); a strict ordering must return false
    c3_comparator_irreflexive_test.go:43: less(x, x) for {Method:/grpc.testing.TestService/UnaryCall IsClientStream:false IsServerStream:false} = true
    c3_comparator_irreflexive_test.go:45: comparator reports {Method:/grpc.testing.TestService/UnaryCall IsClientStream:false IsServerStream:false} as less than itself (IsServerStream=false); a strict ordering must return false
    c3_comparator_irreflexive_test.go:43: less(x, x) for {Method:/grpc.testing.TestService/StreamingOutputCall IsClientStream:false IsServerStream:true} = false
    c3_comparator_irreflexive_test.go:43: less(x, x) for {Method:/grpc.testing.TestService/FullDuplexCall IsClientStream:true IsServerStream:true} = false
--- FAIL: Test (0.00s)
    --- FAIL: Test/Verify_C3_ComparatorIsIrreflexive (0.00s)
FAIL	google.golang.org/grpc	0.004s
```

`less(x, x) == true` for both records with `IsServerStream=false`; `false` for the two with `IsServerStream=true`.

The candidate tests that call `check` currently pass unmodified (the flaw does not surface with the record sets they use):

```sh
cd ~/wt/2e6a7cdc && go test . -run 'Test/Server_RPCPipeline_' -count=1 -v
```

```console
--- PASS: Test (0.07s)
    --- PASS: Test/Server_RPCPipeline_AllCallTypes (0.00s)
    --- PASS: Test/Server_RPCPipeline_HandwrittenServiceDesc (0.00s)
    --- PASS: Test/Server_RPCPipeline_StreamingFailures (0.00s)
    --- PASS: Test/Server_RPCPipeline_UnaryFailure (0.05s)
    --- PASS: Test/Server_RPCPipeline_UnaryRespondsBeforeHalfClose (0.00s)
    --- PASS: Test/Server_RPCPipeline_UnknownServiceHandler (0.00s)
ok  	google.golang.org/grpc	0.071s
```

Verdict: CONFIRMED.

### Impact reasoning

- The comparator breaks the documented irreflexivity requirement of `cmpopts.SortSlices`; go-cmp's behaviour for such a comparator is undefined by contract. In practice, `sort.SliceStable` still terminates and, for the record sets these tests use (distinct methods, or same method with differing flags), produces a usable order, which is why the six `Server_RPCPipeline_*` tests pass. The risk is latent: with duplicate records (e.g. a method invoked twice, or a retried RPC) equal elements are reported as strictly ordered, so `cmpopts`' post-sort consistency check and stable-sort guarantees no longer mean what the test author assumes, and a future change to go-cmp's sorter could make `check` panic ("incomparable values detected") or mis-diff.
- Fix: make the last comparison a real tie-break — `if a.IsServerStream != b.IsServerStream { return !a.IsServerStream }; return false` — or supply a compare function returning `0` for equal records.
