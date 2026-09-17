## Setup

Run ID `v-8c56cb18`. Workspace `~/repos/grpc-go`, branch `verify/grpc-go-server-unify-unary-stream-rpc-v-8c56cb18` created from `origin/grpc-go-server-unify-unary-stream-rpc-perfect` (HEAD `e363acbc`). Go `go1.25.7 linux/amd64`. Claim-target branches were fetched from a second remote and checked out as worktrees:

```sh
cd ~/repos/grpc-go
git remote add evalrepo https://github.com/kaitranntt-evals/grpc-go-server-unify-unary-stream-rpc.git
git fetch evalrepo evalon/grpc-go-se-82014a80 evalon/grpc-go-se-2ea61f68 evalon/grpc-go-se-d4895981 evalon/grpc-go-se-6233a68e evalon/grpc-go-se-2e6a7cdc
for b in 82014a80 2ea61f68 d4895981 6233a68e 2e6a7cdc; do git worktree add ~/wt/$b evalrepo/evalon/grpc-go-se-$b; done
```

Worktree HEADs: `82014a80` → `d5d70164`, `2ea61f68` → `21c5f64f`, `d4895981` → `3880d970`, `6233a68e` → `bd409814`, `2e6a7cdc` → `bd089249` (each is one commit "chore: apply eval changes" on top of base `0c51461d`).

## C1

Claim: an added streaming-failure test can inspect interceptor records after the client receives an error but before the interceptor appends its post-handler record.

### Mechanism (both branches)

On both branches the recorder appends its record *after* `handler(...)` returns (post-handler), and the assertion runs on the test goroutine once the client-side RPC returns an error. For a handler-returned status this is ordered: `processRPC` calls `s.opts.streamInt(...)` and only then `ss.s.WriteStatus(appStatus)` (`~/wt/82014a80/server.go:1450,1489`; `~/wt/2ea61f68/server.go:1459,1476`), so the client cannot observe the status before the append. But for a *receive* failure the status is written to the client from inside `serverStream.RecvMsg`'s deferred function, before the handler has even returned:

```console
$ sed -n 1885,1895p ~/wt/82014a80/stream.go
			ss.mu.Unlock()
		}
		if err != nil && err != io.EOF {
			st, _ := status.FromError(toRPCErr(err))
			ss.s.WriteStatus(st)
			// Non-user specified status was sent out. This should be an error
			// case (as a server side Cancel maybe).
$ grep -n 'Non-user specified status' ~/wt/2ea61f68/stream.go
1831:			// Non-user specified status was sent out. This should be an error
1914:			// Non-user specified status was sent out. This should be an error
```

So in the oversized-decompressed-message cases the client receives `ResourceExhausted` while the handler is still unwinding, and the interceptor's append races with the test's assertion. Only `rec.mu` guards the slice/map; nothing signals completion.

### Branch evalon/grpc-go-se-82014a80 — CONFIRMED

Test: `TestServerRPCPipeline_Failures`, sub-case `client streaming oversized message after decompression` (`~/wt/82014a80/test/server_rpc_pipeline_test.go:692-709`). Recorder appends after `handler(srv, ss)` returns (lines 72-79); the assertion at lines 741-763 requires `len(rec.streamErrs) == 1` right after `stream.CloseAndRecv()` returns the error.

Baseline (unmodified):

```console
$ cd ~/wt/82014a80 && go test ./test -run 'Test/ServerRPCPipeline_Failures' -count=3 -race -v 2>&1 | grep -E -- '--- |^PASS|^FAIL|^ok'
--- PASS: Test (0.04s)
--- PASS: Test (0.03s)
--- PASS: Test (0.04s)
PASS
ok  	google.golang.org/grpc/test	1.127s
```

Unmodified stress — the race is lost naturally, 1 run in 500:

```console
$ cd ~/wt/82014a80 && go test ./test -run 'Test/ServerRPCPipeline_Failures/client_streaming' -count=500 2>&1 | grep -E -- 'server_rpc_pipeline_test.go:|^PASS|^FAIL|^ok' | sort | uniq -c
      1             server_rpc_pipeline_test.go:756: interceptor ran 0 time(s), want 1
      2 FAIL
      1 FAIL	google.golang.org/grpc/test	3.391s
$ go test ./test -run 'Test/ServerRPCPipeline_Failures/client_streaming' -count=1000 -cpu 1,4 2>&1 | grep -E -- 'server_rpc_pipeline_test.go:|--- FAIL|^PASS|^FAIL|^ok' | sort | uniq -c
      1 ok  	google.golang.org/grpc/test	62.465s
```

Pausing the post-handler append (patch `verify/repro/c1_82014a80_pause_append.patch`: `time.Sleep(500ms)` between `handler(srv, ss)` and the `r.mu.Lock()`/append in the stream interceptor; test-file-only change) makes the race deterministic. The handler-error case, whose status is written after the interceptor returns, still passes (taking the 0.50s pause); the receive-failure case fails every run:

```console
$ cd ~/wt/82014a80 && git apply ~/repos/grpc-go/verify/repro/c1_82014a80_pause_append.patch
$ go test ./test -run 'Test/ServerRPCPipeline_Failures' -count=3 -race -v 2>&1 | grep -E -- '--- |^PASS|^FAIL|^ok'
--- FAIL: Test (1.05s)
    --- FAIL: Test/ServerRPCPipeline_Failures (1.04s)
        --- PASS: Test/ServerRPCPipeline_Failures/unary_handler_error (0.01s)
        --- PASS: Test/ServerRPCPipeline_Failures/bidirectional_streaming_handler_error (0.50s)
        --- PASS: Test/ServerRPCPipeline_Failures/unary_oversized_message_after_decompression (0.01s)
        --- FAIL: Test/ServerRPCPipeline_Failures/client_streaming_oversized_message_after_decompression (0.01s)
[... identical for runs 2 and 3 ...]
FAIL
FAIL	google.golang.org/grpc/test	3.167s
$ go test ./test -run 'Test/ServerRPCPipeline_Failures/client_streaming' -count=1 -race -v 2>&1 | grep -E 'server_rpc_pipeline_test.go|--- '
    server_rpc_pipeline_test.go:761: interceptor ran 0 time(s), want 1
--- FAIL: Test (0.54s)
    --- FAIL: Test/ServerRPCPipeline_Failures (0.52s)
        --- FAIL: Test/ServerRPCPipeline_Failures/client_streaming_oversized_message_after_decompression (0.01s)
$ git checkout -- test/
```

Note the failing sub-case finishes in 0.01s — the client got its error and the assertion ran while the interceptor was still inside its 500ms pause, i.e. before the post-handler record was appended. `-race` reports no data race because `rec.mu` serialises the accesses; the mutex does not provide ordering.

Impact: `TestServerRPCPipeline_Failures/client_streaming_oversized_message_after_decompression` is flaky (observed 1/500 unmodified); any scheduling delay on the server goroutine between `RecvMsg` failing and the interceptor returning produces `interceptor ran 0 time(s), want 1`.

### Branch evalon/grpc-go-se-2ea61f68 — CONFIRMED

Test: `TestServerRejectsOversizedDecompressedMessage` (`~/wt/2ea61f68/test/server_rpc_pipeline_test.go:600-653`). The stream interceptor records `streamInfos`/`streamErrs` after `handler(srv, ss)` returns (lines 79-86); after the `bidirectional streaming` sub-test observes `ResourceExhausted` from `stream.Recv()`, line 650 asserts `rec.streamCall(testServiceFullDuplex)` returned `ok` with a `ResourceExhausted` error. `TestServerStreamingRPCHandlerErrorPropagation` (lines 557-595) also inspects post-handler state but its error is handler-returned, so it is ordered by `WriteStatus` following the interceptor.

Baseline (unmodified):

```console
$ cd ~/wt/2ea61f68 && go test ./test -run 'Test/(ServerStreamingRPCHandlerErrorPropagation|ServerRejectsOversizedDecompressedMessage)' -count=3 -race -v 2>&1 | grep -E -- '--- |^PASS|^FAIL|^ok'
--- PASS: Test (0.05s)
    --- PASS: Test/ServerRejectsOversizedDecompressedMessage (0.01s)
        --- PASS: Test/ServerRejectsOversizedDecompressedMessage/unary (0.00s)
        --- PASS: Test/ServerRejectsOversizedDecompressedMessage/bidirectional_streaming (0.00s)
    --- PASS: Test/ServerStreamingRPCHandlerErrorPropagation (0.00s)
[... x3 ...]
ok  	google.golang.org/grpc/test	1.138s
$ go test ./test -run 'Test/ServerRejectsOversizedDecompressedMessage' -count=500 2>&1 | grep -E -- 'server_rpc_pipeline_test.go:|^PASS|^FAIL|^ok' | sort | uniq -c
      1 ok  	google.golang.org/grpc/test	3.235s
$ go test ./test -run 'Test/ServerRejectsOversizedDecompressedMessage' -count=1000 -cpu 1,4 2>&1 | grep -E -- 'server_rpc_pipeline_test.go:|--- FAIL|^PASS|^FAIL|^ok' | sort | uniq -c
      1 ok  	google.golang.org/grpc/test	61.971s
```

Pausing the post-handler record (patch `verify/repro/c1_2ea61f68_pause_append.patch`: `time.Sleep(500ms)` between `handler(srv, ss)` and the `r.mu.Lock()` in `streamInterceptor`) fails deterministically; the handler-error test still passes after absorbing the pause:

```console
$ cd ~/wt/2ea61f68 && git apply ~/repos/grpc-go/verify/repro/c1_2ea61f68_pause_append.patch
$ go test ./test -run 'Test/(ServerStreamingRPCHandlerErrorPropagation|ServerRejectsOversizedDecompressedMessage)' -count=3 -race -v 2>&1 | grep -E -- 'server_rpc_pipeline_test.go:|--- |^PASS|^FAIL|^ok'
    server_rpc_pipeline_test.go:656: stream interceptor observed "/grpc.testing.TestService/FullDuplexCall" with error <nil> (invoked: false), want code ResourceExhausted
--- FAIL: Test (1.06s)
    --- FAIL: Test/ServerRejectsOversizedDecompressedMessage (0.53s)
        --- PASS: Test/ServerRejectsOversizedDecompressedMessage/unary (0.01s)
        --- PASS: Test/ServerRejectsOversizedDecompressedMessage/bidirectional_streaming (0.00s)
    --- PASS: Test/ServerStreamingRPCHandlerErrorPropagation (0.51s)
[... identical for runs 2 and 3 ...]
FAIL
FAIL	google.golang.org/grpc/test	3.190s
$ git checkout -- test/
```

(Line 656 in the patched file is line 650 in the unmodified file.) The `bidirectional_streaming` sub-test completes in 0.00s and the assertion at the end of the parent test runs while the interceptor is still paused: `invoked: false`.

Impact: `TestServerRejectsOversizedDecompressedMessage` is timing-dependent; no natural flake was observed in 2500 runs on this machine, but the pause shows the assertion has no completion barrier and fails as soon as the server goroutine is delayed by more than the client's local round trip.

## C2

Claim: an added test explicitly creates a server, then registers its cleanup only after fallible startup, leaving the server unstopped on an early setup failure.

Relevant `StubServer` behaviour (`internal/stubserver/stubserver.go`, unchanged on both branches): `setupServer` calls `net.Listen` at line 155 and returns the error at 157 *before* `ss.cleanups = append(ss.cleanups, ss.S.Stop)` at line 172; `Start` (117-126) only calls `ss.Stop()` itself when `StartClient` fails, i.e. after the listener exists. `Stop` (271-276) runs only the recorded cleanups. Hence on a `net.Listen` failure a pre-created `S` is never stopped by `StubServer`.

Repro `verify/repro/c2_server_leak_on_listen_failure_test.go` replays each branch's startup sequence with `Network: "bogus-network"` to force `net.Listen` to fail, and uses `channelz` (turned on) to observe whether the `grpc.Server` created by `grpc.NewServer` was stopped (`Server.Stop` removes the channelz entry, `server.go:1682`).

```console
$ cd ~/wt/d4895981 && cp ~/repos/grpc-go/verify/repro/c2_server_leak_on_listen_failure_test.go ./zz_verify_c2_test.go && go test . -run '^TestVerifyC2_' -count=1 -v 2>&1 | grep -v tlogger
=== RUN   TestVerifyC2_d4895981_ListenFailureLeavesServerUnstopped
    zz_verify_c2_test.go:58: grpc.NewServer() registered channelz server 1
    zz_verify_c2_test.go:65: ss.Start(nil) = net.Listen("bogus-network", "localhost:0") = listen bogus-network: unknown network bogus-network (the audited test calls t.Fatal here, before `defer ss.Stop()` is reached)
    zz_verify_c2_test.go:70: after failed Start: server 1 still registered in channelz -> not stopped
    zz_verify_c2_test.go:79: after ss.Stop(): server 1 still registered in channelz -> StubServer.Stop did not stop it
    zz_verify_c2_test.go:85: after server.Stop(): server 1 removed from channelz
    zz_verify_c2_test.go:86: explicitly created server was left unstopped by the audited startup sequence on net.Listen failure
--- FAIL: TestVerifyC2_d4895981_ListenFailureLeavesServerUnstopped (0.00s)
=== RUN   TestVerifyC2_6233a68e_CleanupRegisteredBeforeStart
    zz_verify_c2_test.go:114: ss.Start(nil) = net.Listen("bogus-network", "localhost:0") = listen bogus-network: unknown network bogus-network; t.Cleanup(ss.Stop) was registered before Start
    zz_verify_c2_test.go:103: after registered ss.Stop cleanup ran: server 2 still registered in channelz (StubServer.Stop has no S.Stop cleanup on the net.Listen failure path)
--- PASS: TestVerifyC2_6233a68e_CleanupRegisteredBeforeStart (0.00s)
FAIL
FAIL	google.golang.org/grpc	0.004s
$ rm zz_verify_c2_test.go
```

### Branch evalon/grpc-go-se-d4895981 — CONFIRMED

`TestServerStreamPipeline`, `~/wt/d4895981/server_stream_pipeline_test.go:179-185`, the only explicit `grpc.NewServer` in the file (`grep -n 'grpc.NewServer' server_stream_pipeline_test.go` → line 179 only):

```go
server := grpc.NewServer(grpc.UnaryInterceptor(unaryInterceptor), grpc.StreamInterceptor(streamInterceptor), grpc.UnknownServiceHandler(streamHandler(true, true)))
server.RegisterService(desc, impl)
ss := &stubserver.StubServer{S: server}
if err := ss.Start(nil); err != nil {
	t.Fatal(err)
}
defer ss.Stop()
```

Creation (179) precedes the fallible `ss.Start` (182); `defer ss.Stop()` (185) is registered only after `Start` succeeds; `t.Fatal` on the failure path exits with no `Stop`/`GracefulStop` registered or executed for `server`. The repro output above (`TestVerifyC2_d4895981_ListenFailureLeavesServerUnstopped`) shows that on a `net.Listen` failure the server is still registered after `Start` fails *and* after `ss.Stop()`, and only `server.Stop()` removes it — so even hoisting `defer ss.Stop()` above `Start` would not fix this path; the test needs `defer server.Stop()` (or `t.Cleanup(server.Stop)`) directly after `grpc.NewServer`. The `StartClient` failure path is covered (`Start` calls `ss.Stop()`, whose cleanups include `S.Stop` by then).

Unmodified test passes normally:

```console
$ cd ~/wt/d4895981 && go test . -run 'Test/ServerStreamPipeline$' -count=1 -v 2>&1 | grep -E -- '--- |^ok|^FAIL'
    --- PASS: Test/ServerStreamPipeline (0.06s)
        --- PASS: Test/ServerStreamPipeline//pipeline.Service/Unary (0.00s)
        [...]
        --- PASS: Test/ServerStreamPipeline//pipeline.Service/UnknownMethod (0.00s)
```

Impact: only on a failing test run (listen error → `t.Fatal`), an un-served `grpc.Server` (and its channelz entry) is leaked for the process lifetime; no goroutines are started by `NewServer`, so the leak is a channelz/registration leak, not a goroutine leak.

### Branch evalon/grpc-go-se-6233a68e — REFUTED

`startPipelineServer`, `~/wt/6233a68e/server_unified_test.go:55-71` (the only `grpc.NewServer`/`t.Cleanup` in the file: `grep -n 'grpc.NewServer\|net.Listen\|t.Cleanup\|\.Stop()' server_unified_test.go` → lines 57, 59):

```go
ss := &stubserver.StubServer{S: grpc.NewServer(opts...)}
ss.S.RegisterService(desc, struct{}{})
t.Cleanup(ss.Stop)
if handlerTransport {
	if err := ss.StartHandlerServer(); err != nil { t.Fatal(err) }
	if err := ss.StartClient(); err != nil { t.Fatal(err) }
} else if err := ss.Start(nil); err != nil {
	t.Fatal(err)
}
```

Cleanup (`t.Cleanup(ss.Stop)`, line 59) is registered *before* the fallible `StartHandlerServer`/`Start` calls (61-68), so the claimed creation → fallible setup → late cleanup registration sequence does not exist on this branch, and the suspicion "exits before cleanup is registered" is false. Residual observation (not the claimed defect): as shown by `TestVerifyC2_6233a68e_CleanupRegisteredBeforeStart`, the registered `ss.Stop` does run but does not stop `S` on a `net.Listen` failure, because `StubServer` never recorded `S.Stop` — that is a `StubServer` limitation shared by every pre-created-`S` user, not a cleanup-ordering problem in this test. Both tests using the helper pass:

```console
$ cd ~/wt/6233a68e && go test . -run 'Test/(ServerUnified|UnifiedServer|Pipeline)' -count=1 -v 2>&1 | grep -E -- '^    --- |^ok|^FAIL'
    --- PASS: Test/ServerUnifiedPipeline (0.01s)
    --- PASS: Test/ServerUnifiedPipelineErrors (0.06s)
ok  	google.golang.org/grpc	0.071s
```

## C3

Claim: the comparator in `server_rpc_ext_test.go`'s `interceptorRecorder.check` returns true for `less(x, x)` when `IsServerStream` is false.

Branch evalon/grpc-go-se-2e6a7cdc, `~/wt/2e6a7cdc/server_rpc_ext_test.go:88-107`:

```go
func (r *interceptorRecorder) check(t *testing.T, wantUnary, wantStream []interceptorRecord) {
	...
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

The `go.mod` pins `github.com/google/go-cmp v0.7.0`, whose `SortSlices` doc requires the less function to be "Irreflexive: !less(x, x)" (`$(go env GOMODCACHE)/github.com/google/go-cmp@v0.7.0/cmp/cmpopts/sort.go:22-25`). The repro copies the comparator verbatim and evaluates `less(x, x)`:

```console
$ cd ~/wt/2e6a7cdc && cp ~/repos/grpc-go/verify/repro/c3_comparator_irreflexive_test.go ./zz_verify_c3_test.go && go test . -run '^TestVerifyC3_' -count=1 -v
=== RUN   TestVerifyC3_ComparatorIrreflexive
    zz_verify_c3_test.go:41: less(x, x) for {Method:/grpc.testing.TestService/UnaryCall IsClientStream:false IsServerStream:false} = true
    zz_verify_c3_test.go:43: less(x, x) = true for {Method:/grpc.testing.TestService/UnaryCall IsClientStream:false IsServerStream:false}; cmpopts.SortSlices requires !less(x, x)
    zz_verify_c3_test.go:41: less(x, x) for {Method:/grpc.testing.TestService/StreamingInputCall IsClientStream:true IsServerStream:false} = true
    zz_verify_c3_test.go:43: less(x, x) = true for {Method:/grpc.testing.TestService/StreamingInputCall IsClientStream:true IsServerStream:false}; cmpopts.SortSlices requires !less(x, x)
    zz_verify_c3_test.go:41: less(x, x) for {Method:/grpc.testing.TestService/StreamingOutputCall IsClientStream:false IsServerStream:true} = false
    zz_verify_c3_test.go:41: less(x, x) for {Method:/grpc.testing.TestService/FullDuplexCall IsClientStream:true IsServerStream:true} = false
--- FAIL: TestVerifyC3_ComparatorIrreflexive (0.00s)
FAIL
FAIL	google.golang.org/grpc	0.004s
$ rm zz_verify_c3_test.go
```

`less(x, x)` is `true` whenever `IsServerStream` is false (including every unary record, which always has both flags false), violating the irreflexivity contract. The nearest test name to the claim's `TestInterceptorSegregation` is `TestServer_RPCPipeline_AllCallTypes` (line 233) and the other `TestServer_RPCPipeline_*` tests that call `check`. Practical effect today is nil: for `a != b` the comparator is consistent, and go-cmp v0.7.0's `checkSort` (`sort.go:73-86`) only panics when adjacent elements are mutually ordered after sorting, which cannot happen for identical structs, so all callers pass:

```console
$ cd ~/wt/2e6a7cdc && go test . -run 'Test/Server_RPCPipeline_' -count=1 -v 2>&1 | grep -E -- '--- |^ok|^FAIL'
--- PASS: Test (0.06s)
    --- PASS: Test/Server_RPCPipeline_AllCallTypes (0.00s)
    --- PASS: Test/Server_RPCPipeline_HandwrittenServiceDesc (0.00s)
    --- PASS: Test/Server_RPCPipeline_StreamingFailures (0.00s)
    --- PASS: Test/Server_RPCPipeline_UnaryFailure (0.05s)
    --- PASS: Test/Server_RPCPipeline_UnaryRespondsBeforeHalfClose (0.00s)
    --- PASS: Test/Server_RPCPipeline_UnknownServiceHandler (0.00s)
ok  	google.golang.org/grpc	0.067s
```

Verdict: CONFIRMED (contract violation is real; impact latent — a future go-cmp that enforces irreflexivity, or a sort implementation sensitive to `less(x, x)`, would surface it).
