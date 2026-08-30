# Audit evidence — run v-fcb7a988

Audited branch: `verify/grpc-go-server-unify-unary-stream-rpc-v-fcb7a988` (from `origin/grpc-go-server-unify-unary-stream-rpc-perfect`, HEAD `d68a8fe7`).
Base commit for comparisons: `0c51461d27177d997e14c642fe18c11668fc09a3` (worktree `~/wt/base`).
Claim-target branches were fetched from `github.com/kaitranntt-evals/grpc-go-server-unify-unary-stream-rpc` (remote `evals`) into worktrees:
`~/wt/73c` = `evalon/grpc-go-se-73c8337c` (f3b73428), `~/wt/078` = `evalon/grpc-go-se-0786edbe` (296a92a7), `~/wt/518` = `evalon/grpc-go-se-5181f5de` (007302a4), `~/wt/ddb` = `evalon/grpc-go-se-ddb07dab` (3e919b93), `~/wt/f21` = `evalon/grpc-go-se-f2151d7b` (3d2c61c5).

Repro test files live in `verify/repro/`; each is copied into the target worktree's `test/` directory and run with `-tags verify_repro`. (The runs below used the same file contents without the build tag.)

## C1

REFUTED (branch evalon/grpc-go-se-73c8337c).

Authored test files on the branch (only test changes vs base):

```console
$ cd ~/wt/73c && git diff --name-only 0c51461d HEAD
encoding/compressor_test.go
server.go
stream.go
test/server_test.go
```

`test/server_test.go` adds `TestServerUnifiedRPCPipelineInterceptorSelection`, which invokes all four shapes — unary `EmptyCall`, server-streaming `StreamingOutputCall` (success + `codes.DataLoss` failure), client-streaming `StreamingInputCall`, bidi `FullDuplexCall`, plus a flagless stream — and then asserts the exact per-invocation interceptor recordings:

```go
wantUnary := []string{testgrpc.TestService_EmptyCall_FullMethodName}
wantStream := []string{
    testgrpc.TestService_StreamingOutputCall_FullMethodName,
    testgrpc.TestService_StreamingInputCall_FullMethodName,
    testgrpc.TestService_FullDuplexCall_FullMethodName,
    flaglessMethod,
    testgrpc.TestService_StreamingOutputCall_FullMethodName,
}
```

plus `status.Code(err) != codes.DataLoss` for the streaming failure, and `encoding/compressor_test.go` adds a bidi `codes.ResourceExhausted` assertion. Every shape therefore checks an invocation-produced invariant (interceptor side effect recorded for that specific call; status codes for the failure paths), not just nil/EOF completion. Demonstration run:

```console
$ cd ~/wt/73c && go test -v ./test -run '^Test$/^ServerUnifiedRPCPipelineInterceptorSelection$' -count=1
--- PASS: Test (0.01s)
    --- PASS: Test/ServerUnifiedRPCPipelineInterceptorSelection (0.00s)
ok  	google.golang.org/grpc/test	0.014s
```

## C2

CONFIRMED (audited branch).

Repro: `verify/repro/verify_c2_empty_unary_test.go` — opens a client stream to the unary method `/grpc.testing.TestService/UnaryCall`, calls `CloseSend()` without sending a request, and logs the resulting status.

On the audited branch:

```console
$ cd ~/repos/grpc-go && go test -v ./test -run '^TestVerify_EmptyUnaryRequestStatus$' -count=1
    verify_c2_empty_unary_test.go:41: RecvMsg err=rpc error: code = Internal desc = cardinality violation: received no request message from non-client-streaming RPC code=Internal msg="cardinality violation: received no request message from non-client-streaming RPC"
--- PASS: TestVerify_EmptyUnaryRequestStatus (0.00s)
```

Same test on the base commit (behavior change vs base):

```console
$ cd ~/wt/base && go test -v ./test -run '^TestVerify_EmptyUnaryRequestStatus$' -count=1
    verify_c2_empty_unary_test.go:41: RecvMsg err=rpc error: code = Unknown desc = EOF code=Unknown msg="EOF"
--- PASS: TestVerify_EmptyUnaryRequestStatus (0.00s)
```

Source: `stream.go:1925` on the audited branch returns `status.Error(codes.Internal, "cardinality violation: received no request message from non-client-streaming RPC")` when the first `recv` yields `io.EOF` on a non-client-streaming descriptor.

## C3

CONFIRMED, both parts (branch evalon/grpc-go-se-73c8337c).

Repro: `verify/repro/verify_c3_unary_eof_test.go` — unary handler returns `io.EOF`; test enables channelz, makes the call with a 3s deadline, and logs the client-observed status plus the server's channelz call counters.

On the claim branch:

```console
$ cd ~/wt/73c && go test -v ./test -run '^TestVerify_UnaryHandlerReturnsEOF$' -count=1
    verify_c3_unary_eof_test.go:33: client observed: err=rpc error: code = DeadlineExceeded desc = context deadline exceeded code=DeadlineExceeded msg="context deadline exceeded"
    verify_c3_unary_eof_test.go:38: channelz server metrics: started=1 succeeded=1 failed=0
--- PASS: TestVerify_UnaryHandlerReturnsEOF (3.12s)
```

- Terminal status framing: no converted non-OK status is ever written — the client hangs until its deadline (`DeadlineExceeded` is client-generated).
- Lifecycle accounting: channelz records the call as succeeded (`succeeded=1 failed=0`).

Same test on the base commit:

```console
$ cd ~/wt/base && go test -v ./test -run '^TestVerify_UnaryHandlerReturnsEOF$' -count=1
    verify_c3_unary_eof_test.go:33: client observed: err=rpc error: code = Unknown desc = EOF code=Unknown msg="EOF"
    verify_c3_unary_eof_test.go:38: channelz server metrics: started=1 succeeded=0 failed=1
--- PASS: TestVerify_UnaryHandlerReturnsEOF (0.10s)
```

Source: `~/wt/73c/server.go` `processRPC` contains `if !md.isStreaming && appErr == io.EOF { return appErr }` before any `WriteStatus`, and the deferred accounting treats `err == io.EOF` as success (`if err != nil && err != io.EOF { s.incrCallsFailed() } else { s.incrCallsSucceeded() }`).

## C4

CONFIRMED (branch evalon/grpc-go-se-0786edbe).

Instrumentation: `verify/repro/c4_instrumentation.patch` adds a print of `opts.Last` to `internal/transport.(*ServerStream).Write`. Repro test: `verify/repro/verify_c4_last_flag_test.go` (one unary call, one server-streaming call with two sends).

On the claim branch:

```console
$ cd ~/wt/078 && go test -v ./test -run '^TestVerify_UnaryWriteLastFlag$' -count=1
VERIFY-C4: ServerStream.Write method=/grpc.testing.TestService/UnaryCall Last=false
VERIFY-C4: ServerStream.Write method=/grpc.testing.TestService/StreamingOutputCall Last=false
VERIFY-C4: ServerStream.Write method=/grpc.testing.TestService/StreamingOutputCall Last=false
--- PASS: TestVerify_UnaryWriteLastFlag (0.00s)
```

Same instrumentation + test on the base commit:

```console
$ cd ~/wt/base && go test -v ./test -run '^TestVerify_UnaryWriteLastFlag$' -count=1
VERIFY-C4: ServerStream.Write method=/grpc.testing.TestService/UnaryCall Last=true
VERIFY-C4: ServerStream.Write method=/grpc.testing.TestService/StreamingOutputCall Last=false
VERIFY-C4: ServerStream.Write method=/grpc.testing.TestService/StreamingOutputCall Last=false
--- PASS: TestVerify_UnaryWriteLastFlag (0.00s)
```

Source: `~/wt/078/stream.go:1851` — `serverStream.SendMsg` always calls `ss.s.Write(hdr, payload, &transport.WriteOptions{Last: false})` with no unary special case; base `server.go:1483` used `opts := &transport.WriteOptions{Last: true}` for unary responses.

## C5

CONFIRMED, both parts (branch evalon/grpc-go-se-73c8337c).

The extra `errStage string` return value and its call sites (grep of the branch that the executed C1/C3 test runs above compiled, so the signature is live code):

```console
$ cd ~/wt/73c && grep -n "prepareMsg" stream.go
stream.go:1058:	hdr, data, payload, pf, _, err := prepareMsg(m, cs.codec, cs.compressorV0, cs.compressorV1, cs.cc.dopts.copts.BufferPool)
stream.go:1555:	hdr, data, payload, pf, _, err := prepareMsg(m, as.codec, as.sendCompressorV0, as.sendCompressorV1, as.ac.dopts.copts.BufferPool)
stream.go:1817:	hdr, data, payload, pf, errStage, err := prepareMsg(m, ss.codec, ss.compressorV0, ss.compressorV1, ss.p.bufferPool)
stream.go:1980:func prepareMsg(m any, codec baseCodec, cp Compressor, comp encoding.Compressor, pool mem.BufferPool) (hdr []byte, data, payload mem.BufferSlice, pf payloadFormat, errStage string, err error) {
```

- Shared helper API: `prepareMsg` returns a separate `errStage string` (line 1980).
- Client call-site plumbing: both client send paths — `clientStream.SendMsg` (line 1058) and `addrConnStream.SendMsg` (line 1555) — bind the diagnostic-stage value to `_` and discard it; only the server path (line 1817) consumes it. `go test ./test` compilations above (C1/C3 runs) demonstrate this is the built signature.

## C6

CONFIRMED (audited branch).

Same experiment and outputs as C2 (identical behavior claim). Repro `verify/repro/verify_c2_empty_unary_test.go`:

```console
$ cd ~/repos/grpc-go && go test -v ./test -run '^TestVerify_EmptyUnaryRequestStatus$' -count=1
    verify_c2_empty_unary_test.go:41: RecvMsg err=rpc error: code = Internal desc = cardinality violation: received no request message from non-client-streaming RPC code=Internal msg="cardinality violation: received no request message from non-client-streaming RPC"
--- PASS: TestVerify_EmptyUnaryRequestStatus (0.00s)
$ cd ~/wt/base && go test -v ./test -run '^TestVerify_EmptyUnaryRequestStatus$' -count=1
    verify_c2_empty_unary_test.go:41: RecvMsg err=rpc error: code = Unknown desc = EOF code=Unknown msg="EOF"
--- PASS: TestVerify_EmptyUnaryRequestStatus (0.00s)
```

The empty unary request is reframed from `codes.Unknown`/`EOF` (base) to `codes.Internal` cardinality violation (audited branch).

## C7

CONFIRMED, both parts (branch evalon/grpc-go-se-5181f5de).

Repro: `verify/repro/verify_c7_collision_test.go` — registers a hand-written `ServiceDesc` whose `Methods` and `Streams` both contain name `Call`, installs unary and stream interceptors, invokes `/verify.Collide/Call`, and records which handler/interceptor ran.

On the claim branch:

```console
$ cd ~/wt/518 && go test -v ./test -run '^TestVerify_SameNameCollisionDispatch$' -count=1
    verify_c7_collision_test.go:75: unaryHandler=false streamHandler=true unaryInterceptor=false streamInterceptor=true
--- PASS: TestVerify_SameNameCollisionDispatch (0.00s)
```

Same test on the base commit (unary wins there):

```console
$ cd ~/wt/base && go test -v ./test -run '^TestVerify_SameNameCollisionDispatch$' -count=1
    verify_c7_collision_test.go:75: unaryHandler=true streamHandler=false unaryInterceptor=true streamInterceptor=false
--- PASS: TestVerify_SameNameCollisionDispatch (0.00s)
```

Source: `~/wt/518/server.go:810-822` — registration inserts `sd.Methods` into `info.methods` first, then `sd.Streams` into the same map, so a same-name stream overwrites the unary entry.

## C8

CONFIRMED (branch evalon/grpc-go-se-ddb07dab).

The assertion at `~/wt/ddb/test/server_unified_rpc_test.go:212` inside `TestServerUnaryContextAndMetadata`:

```go
md, ok := metadata.FromIncomingContext(ctx)
if !ok || md.Get("k")[0] != "v" {
```

`||` short-circuits only on `!ok`; when metadata exists but `"k"` is absent, `md.Get("k")` returns an empty slice and `[0]` panics. Demonstration: `verify/repro/verify_c8_metadata_panic_test.go` copies the identical expression into a handler and sends metadata `other:v` (present, but no `k`):

```console
$ cd ~/wt/ddb && go test -v ./test -run '^Test$/^(ServerUnaryContextAndMetadata|Verify_UnaryMetadataAssertionKeyAbsent)$' -count=1
=== RUN   Test/ServerUnaryContextAndMetadata
=== RUN   Test/Verify_UnaryMetadataAssertionKeyAbsent
panic: runtime error: index out of range [0] with length 0
google.golang.org/grpc/test.s.TestVerify_UnaryMetadataAssertionKeyAbsent.func1(...)
	/home/ubuntu/wt/ddb/test/verify_c8_metadata_panic_test.go:21 +0xe5
FAIL	google.golang.org/grpc/test	0.020s
```

The original test passes under its happy path (`Test/ServerUnaryContextAndMetadata` ran before the panic without failing); under the claimed condition the same expression panics instead of reporting an ordinary assertion failure.

## C9

CONFIRMED, both parts (branch evalon/grpc-go-se-f2151d7b).

Repro: `verify/repro/verify_c9_channelz_diag_test.go` — part 1 forces a response-encoding failure via `grpc.ForceServerCodecV2` with a codec whose `Marshal` fails only for `*testpb.SimpleResponse`; part 2 forces a response-compression failure via `grpc.SetSendCompressor(ctx, "verify-bad")` with a registered compressor whose writer errors.

On the claim branch — RPCs fail as expected but **no** channelz diagnostic is emitted for either part:

```console
$ cd ~/wt/f21 && go test -v ./test -run '^TestVerify_ServerSendFailureDiagnostics$' -count=1
    verify_c9_channelz_diag_test.go:59: encoding-failure RPC err=rpc error: code = Internal desc = grpc: error while marshaling: verify-c9 marshal failure
    verify_c9_channelz_diag_test.go:76: compression-failure RPC err=rpc error: code = Internal desc = grpc: error while compressing: verify-c9 compress failure
--- PASS: TestVerify_ServerSendFailureDiagnostics (0.00s)
```

Same test on the base commit — both distinct diagnostics appear (validating the probe):

```console
$ cd ~/wt/base && go test -v ./test -run '^TestVerify_ServerSendFailureDiagnostics$' -count=1
2026/08/30 21:30:40 ERROR: [core] [Server #1] grpc: server failed to encode response: rpc error: code = Internal desc = grpc: error while marshaling: verify-c9 marshal failure
2026/08/30 21:30:40 ERROR: [core] [Server #7] grpc: server failed to compress response: rpc error: code = Internal desc = grpc: error while compressing: verify-c9 compress failure
--- PASS: TestVerify_ServerSendFailureDiagnostics (0.00s)
```

Source: on the claim branch `grep -rn "failed to encode response\|failed to compress response" --include=*.go .` (excluding tests) returns nothing; `serverStream.SendMsg` returns `prepareMsg` errors bare (`if err != nil { return err }`). Base `server.go:1191/1198` emitted the two distinct `channelz.Error` diagnostics.
