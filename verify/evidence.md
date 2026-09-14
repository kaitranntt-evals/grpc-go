## Setup (shared by all claims)

All twelve claims target the same branch, `evalon/grpc-go-se-364b59cc` of
`kaitranntt-evals/grpc-go-server-unify-unary-stream-rpc` (commit `4ba2a074`,
"chore: apply eval changes", on top of base `0c51461d`). It was checked out in a
separate worktree so the main checkout stayed clean; every `cd ~/repos/grpc-go`
in the claim text was executed as `cd /home/ubuntu/wt-364b59cc`.

```sh
cd ~/repos/grpc-go
git remote add claims https://github.com/kaitranntt-evals/grpc-go-server-unify-unary-stream-rpc.git
git fetch claims evalon/grpc-go-se-364b59cc
git worktree add /home/ubuntu/wt-364b59cc claims/evalon/grpc-go-se-364b59cc
mkdir -p /home/ubuntu/eval && cd /home/ubuntu/eval && unzip -o eval_tests.zip   # fixtures, byte-exact
cp -r /home/ubuntu/eval/tests/. /home/ubuntu/wt-364b59cc/                        # same relative layout
cd /home/ubuntu/wt-364b59cc && go build ./... && go vet . ./test ./encoding
go version   # go1.25.7 linux/amd64
```

Key production lines on the target branch (context for the probes; not evidence by themselves):

```go
// server.go (processRPC), lines 1433-1450
appStatus, ok := status.FromError(appErr)   // appErr == nil  =>  (nil, true)
...
writeErr := ss.s.WriteStatus(appStatus)     // nil *status.Status on every successful RPC

// internal/transport/http2_server.go writeStatus, lines 1103-1106
Value: strconv.Itoa(int(st.Code()))
Value: encodeGrpcMessage(st.Message())
if p := istatus.RawStatusProto(st); len(p.GetDetails()) > 0 {
```

### Instrumentation used for the "nil status reaches WriteStatus / no dereference" observations

`verify/instrumentation/writestatus_nil_probe.patch` adds two `stderr` prints
around `transport.ServerStream.WriteStatus` (before and after `s.st.writeStatus`)
that fire only when `st == nil`. It was applied to the worktree for the probe
runs and reverted afterwards (`git apply -R`); production code is untouched.

```sh
cd /home/ubuntu/wt-364b59cc
git apply  ~/repos/grpc-go/verify/instrumentation/writestatus_nil_probe.patch   # instrument
# ... probe runs below ...
git apply -R ~/repos/grpc-go/verify/instrumentation/writestatus_nil_probe.patch # revert
git status --short | grep -v '^??'     # (empty: no tracked file modified)
```

### Direct probe of the exact expressions writeStatus applies to a nil status

`verify/probes/nil_status_probe_test.go` (committed) evaluates `st.Code()`,
`st.Message()`, `istatus.RawStatusProto(st)`, `p.GetDetails()` and `st.Proto()`
on a nil `*status.Status`, then performs a real unary and a real bidi RPC over
HTTP/2 through `processRPC` and checks response + trailer + `io.EOF`.

```sh
cp -r ~/repos/grpc-go/verify/probes /home/ubuntu/wt-364b59cc/verify/
cd /home/ubuntu/wt-364b59cc && go test -v ./verify/probes -count=1
```

```console
=== RUN   TestNilStatusAccessorsUsedByWriteStatus
    nil_status_probe_test.go:42: nil status: Code()=0 Message()="" RawStatusProto()=<nil> details=0 Proto()=<nil>
--- PASS: TestNilStatusAccessorsUsedByWriteStatus (0.00s)
=== RUN   TestSuccessfulRPCsCompleteCleanly
    nil_status_probe_test.go:88: unary: resp="echo:hi" err=<nil> status.Code(err)=OK trailer probe-trailer=[ok]
    nil_status_probe_test.go:108: stream: msg="stream-ok" final Recv err=EOF trailer probe-trailer=[ok]
--- PASS: TestSuccessfulRPCsCompleteCleanly (0.00s)
PASS
ok  	google.golang.org/grpc/verify/probes	0.005s
```

`status.FromError(nil)` is `(nil, true)`; the nil receiver is handled by
`internal/status.(*Status).Code/Message/Proto` (`if s == nil || s.s == nil`) and
`RawStatusProto` (`if s == nil { return nil }`), so `writeStatus` emits
`grpc-status: 0`, empty `grpc-message`, no details — i.e. a normal OK trailer.

## C1

Part 1 (nil status supplied to WriteStatus) and part 2 (HTTP/2 transport dereferences it, unary RPC does not complete cleanly).

```sh
cd /home/ubuntu/wt-364b59cc && go test -v ./test -run '^TestEval_UnaryRoundTrip$' -count=1
```

```console
=== RUN   TestEval_UnaryRoundTrip
--- PASS: TestEval_UnaryRoundTrip (0.00s)
PASS
ok  	google.golang.org/grpc/test	0.006s
```

Same command with the WriteStatus probe applied (`go test -v ./test -run '^(TestEval_UnaryRoundTrip|...)$' -count=1`, output for this test):

```console
=== RUN   TestEval_UnaryRoundTrip
[verify-probe] WriteStatus(nil *status.Status) method=/grpc.testing.TestService/UnaryCall transport=*transport.http2Server
[verify-probe] writeStatus(nil) returned err=<nil> method=/grpc.testing.TestService/UnaryCall (no panic)
--- PASS: TestEval_UnaryRoundTrip (0.00s)
```

Direct probe (`go test -v ./verify/probes -count=1`):

```console
nil_status_probe_test.go:42: nil status: Code()=0 Message()="" RawStatusProto()=<nil> details=0 Proto()=<nil>
nil_status_probe_test.go:88: unary: resp="echo:hi" err=<nil> status.Code(err)=OK trailer probe-trailer=[ok]
```

Observed: part 1 holds (nil `*status.Status` is passed to `WriteStatus` on the
`http2Server` transport); part 2 does not (writeStatus returns `nil`, no panic,
response and OK trailer reach the client, fixture passes).

## C2

Part 1 (interceptor-enabled unary) and part 2 (interceptor-enabled streaming).

```sh
cd /home/ubuntu/wt-364b59cc && go test -v ./test -run '^(TestEval_InterceptorSegregation|TestEval_FalseFalseStreamDescInterceptor)$' -count=1
```

```console
=== RUN   TestEval_FalseFalseStreamDescInterceptor
--- PASS: TestEval_FalseFalseStreamDescInterceptor (0.00s)
=== RUN   TestEval_InterceptorSegregation
--- PASS: TestEval_InterceptorSegregation (0.00s)
PASS
ok  	google.golang.org/grpc/test	0.007s
```

Same tests with the WriteStatus probe applied:

```console
=== RUN   TestEval_FalseFalseStreamDescInterceptor
[verify-probe] WriteStatus(nil *status.Status) method=/eval.FalseFalseService/Call transport=*transport.http2Server
[verify-probe] writeStatus(nil) returned err=<nil> method=/eval.FalseFalseService/Call (no panic)
--- PASS: TestEval_FalseFalseStreamDescInterceptor (0.00s)
=== RUN   TestEval_InterceptorSegregation
[verify-probe] WriteStatus(nil *status.Status) method=/grpc.testing.TestService/UnaryCall transport=*transport.http2Server
[verify-probe] writeStatus(nil) returned err=<nil> method=/grpc.testing.TestService/UnaryCall (no panic)
[verify-probe] WriteStatus(nil *status.Status) method=/grpc.testing.TestService/StreamingInputCall transport=*transport.http2Server
[verify-probe] writeStatus(nil) returned err=<nil> method=/grpc.testing.TestService/StreamingInputCall (no panic)
--- PASS: TestEval_InterceptorSegregation (0.00s)
```

Observed: nil status reaches `WriteStatus` for the unary call, the client-streaming
call and the false/false stream-descriptor call; each `writeStatus(nil)` returned
`nil` without panicking and both fixtures (which assert interceptor invocation
and successful responses) passed.

## C3

Part 1 (below-limit compressed unary) and part 2 (below-limit compressed streaming).

```sh
cd /home/ubuntu/wt-364b59cc && go test -v ./encoding -run '^TestEval_DecompressionLimits$' -count=1
```

```console
=== RUN   TestEval_DecompressionLimits/BelowLimit/Unary
=== RUN   TestEval_DecompressionLimits/BelowLimit/Streaming
--- PASS: TestEval_DecompressionLimits (0.00s)
    --- PASS: TestEval_DecompressionLimits/BelowLimit (0.00s)
        --- PASS: TestEval_DecompressionLimits/BelowLimit/Unary (0.00s)
        --- PASS: TestEval_DecompressionLimits/BelowLimit/Streaming (0.00s)
    --- PASS: TestEval_DecompressionLimits/Unary (0.00s)
    --- PASS: TestEval_DecompressionLimits/Streaming (0.00s)
PASS
ok  	google.golang.org/grpc/encoding	0.008s
```

Same command with the WriteStatus probe applied:

```console
[verify-probe] WriteStatus(nil *status.Status) method=/grpc.testing.TestService/UnaryCall transport=*transport.http2Server
[verify-probe] writeStatus(nil) returned err=<nil> method=/grpc.testing.TestService/UnaryCall (no panic)
[verify-probe] WriteStatus(nil *status.Status) method=/grpc.testing.TestService/FullDuplexCall transport=*transport.http2Server
[verify-probe] writeStatus(nil) returned err=<nil> method=/grpc.testing.TestService/FullDuplexCall (no panic)
ok  	google.golang.org/grpc/encoding	0.007s
```

Observed: exactly one nil-status `WriteStatus` per below-limit call (unary and
full-duplex), neither panicked, both subtests passed; the over-limit subtests
(non-nil `ResourceExhausted` status) did not trigger the probe.

## C4

Part 1 (descriptor-routed unary) and part 2 (descriptor-routed streaming).

```sh
cd /home/ubuntu/wt-364b59cc && go test -v ./test -run '^TestEval_DescriptorToHandlerRouting$' -count=1
```

```console
=== RUN   TestEval_DescriptorToHandlerRouting
--- PASS: TestEval_DescriptorToHandlerRouting (0.00s)
PASS
ok  	google.golang.org/grpc/test	0.006s
```

Same test with the WriteStatus probe applied:

```console
=== RUN   TestEval_DescriptorToHandlerRouting
[verify-probe] WriteStatus(nil *status.Status) method=/grpc.testing.RoutingService/UnaryAlpha transport=*transport.http2Server
[verify-probe] writeStatus(nil) returned err=<nil> method=/grpc.testing.RoutingService/UnaryAlpha (no panic)
[verify-probe] WriteStatus(nil *status.Status) method=/grpc.testing.RoutingService/UnaryBeta transport=*transport.http2Server
[verify-probe] writeStatus(nil) returned err=<nil> method=/grpc.testing.RoutingService/UnaryBeta (no panic)
[verify-probe] WriteStatus(nil *status.Status) method=/grpc.testing.RoutingService/StreamGamma transport=*transport.http2Server
[verify-probe] writeStatus(nil) returned err=<nil> method=/grpc.testing.RoutingService/StreamGamma (no panic)
[verify-probe] WriteStatus(nil *status.Status) method=/grpc.testing.RoutingService/StreamDelta transport=*transport.http2Server
[verify-probe] writeStatus(nil) returned err=<nil> method=/grpc.testing.RoutingService/StreamDelta (no panic)
--- PASS: TestEval_DescriptorToHandlerRouting (0.00s)
```

Observed: two unary and two streaming routed handlers each finished with a
nil-status `WriteStatus` that returned `nil` and did not panic; the fixture's
handler-selection and response assertions passed.

## C5

Part 1 (nil status supplied to WriteStatus) and part 2 (client cannot receive response followed by clean EOF).

```sh
cd /home/ubuntu/wt-364b59cc && go test -v ./test -run '^TestEval_UnaryWithoutHalfClose$' -count=1
```

```console
=== RUN   TestEval_UnaryWithoutHalfClose
--- PASS: TestEval_UnaryWithoutHalfClose (0.00s)
PASS
ok  	google.golang.org/grpc/test	0.005s
```

Same test with the WriteStatus probe applied:

```console
=== RUN   TestEval_UnaryWithoutHalfClose
[verify-probe] WriteStatus(nil *status.Status) method=/grpc.testing.TestService/UnaryCall transport=*transport.http2Server
[verify-probe] writeStatus(nil) returned err=<nil> method=/grpc.testing.TestService/UnaryCall (no panic)
--- PASS: TestEval_UnaryWithoutHalfClose (0.00s)
```

Fixture assertions (`test/eval_unary_halfclose_test.go`): opens a raw stream to
`/grpc.testing.TestService/UnaryCall`, sends one request without `CloseSend`,
requires `RecvMsg` to return the response and the next `RecvMsg` to return `io.EOF`.
Observed: part 1 holds (nil status passed), part 2 does not (response then `io.EOF`
received, no panic).

## C6

Part 1 (interceptor scenarios) and part 2 (repeated-send scenarios).

```sh
cd /home/ubuntu/wt-364b59cc && bash ./test/run_eval_server_test_group.sh interceptor-and-send; echo EXIT $?
```

```console
==> interceptor chains and repeated unary sends
{"Action":"output","Package":"google.golang.org/grpc/test","Test":"Test/ChainStreamServerInterceptor","Output":"--- PASS: Test/ChainStreamServerInterceptor (0.05s)\n"}
{"Action":"output","Package":"google.golang.org/grpc/test","Test":"Test/ChainUnaryServerInterceptor","Output":"--- PASS: Test/ChainUnaryServerInterceptor (0.00s)\n"}
{"Action":"output","Package":"google.golang.org/grpc/test","Test":"Test/StreamServerInterceptor","Output":"--- PASS: Test/StreamServerInterceptor (0.05s)\n"}
{"Action":"output","Package":"google.golang.org/grpc/test","Test":"Test/UnaryRPC_ClientCallSendMsgTwice","Output":"--- PASS: Test/UnaryRPC_ClientCallSendMsgTwice (0.00s)\n"}
{"Action":"output","Package":"google.golang.org/grpc/test","Test":"Test/UnaryRPC_ServerCallSendMsgTwice","Output":"--- PASS: Test/UnaryRPC_ServerCallSendMsgTwice (0.00s)\n"}
{"Action":"output","Package":"google.golang.org/grpc/test","Test":"Test/UnaryServerInterceptor","Output":"--- PASS: Test/UnaryServerInterceptor (0.04s)\n"}
{"Action":"output","Package":"google.golang.org/grpc/test","Test":"Test","Output":"--- PASS: Test (0.17s)\n"}
EXIT 0
```

Same group with the WriteStatus probe applied (probe lines extracted from the `go test -json` output):

```console
      2 verify-probe] WriteStatus(nil *status.Status) method=/grpc.testing.TestService/UnaryCall transport=*transport.http2Server
      2 verify-probe] writeStatus(nil) returned err=<nil> method=/grpc.testing.TestService/UnaryCall (no panic)
      1 verify-probe] WriteStatus(nil *status.Status) method=/grpc.testing.TestService/FullDuplexCall transport=*transport.http2Server
      1 verify-probe] writeStatus(nil) returned err=<nil> method=/grpc.testing.TestService/FullDuplexCall (no panic)
EXIT 0
```

Observed: all six group tests passed and the runner exited 0; the successful
interceptor and repeated-send calls reached `WriteStatus` with a nil status and
returned without panic. (`TestEval_SendPreparationDiagnostics`, named in "where to
look", is covered under C8; its failure concerns a log-message string, not this group.)

## C7

Part 1 (runner executes root-package successful RPC scenarios) and part 2 (a root-package successful RPC dereferences nil status, command exits unsuccessfully).

```sh
cd /home/ubuntu/wt-364b59cc && bash ./test/run_candidate_tests.sh; echo EXIT $?
```

```console
==> Detected candidate test packages:
    server_pipeline_ext_test.go	@package
==> Running candidate test suite in module /home/ubuntu/wt-364b59cc for package .
[PASS] package . (all tests passed)
EXIT 0
```

Root-package scenarios run verbosely, with the WriteStatus probe applied:

```sh
cd /home/ubuntu/wt-364b59cc && go test -v . -run '^Test$/^ServerUnifiedPipeline' -count=1
```

```console
=== RUN   Test/ServerUnifiedPipeline//test.Pipeline/Unary
[verify-probe] WriteStatus(nil *status.Status) method=/test.Pipeline/Unary transport=*transport.http2Server
[verify-probe] writeStatus(nil) returned err=<nil> method=/test.Pipeline/Unary (no panic)
[verify-probe] WriteStatus(nil *status.Status) method=/test.Pipeline/Collision transport=*transport.http2Server
[verify-probe] writeStatus(nil) returned err=<nil> method=/test.Pipeline/Collision (no panic)
[verify-probe] WriteStatus(nil *status.Status) method=/test.Pipeline/Client transport=*transport.http2Server
[verify-probe] WriteStatus(nil *status.Status) method=/test.Pipeline/Server transport=*transport.http2Server
[verify-probe] WriteStatus(nil *status.Status) method=/test.Pipeline/Bidi transport=*transport.http2Server
[verify-probe] WriteStatus(nil *status.Status) method=/test.Pipeline/Neither transport=*transport.http2Server
[verify-probe] WriteStatus(nil *status.Status) method=/test.Pipeline/Unknown transport=*transport.http2Server
[verify-probe] WriteStatus(nil *status.Status) method=/test.Unknown/Unknown transport=*transport.http2Server
--- PASS: Test (0.01s)
    --- PASS: Test/ServerUnifiedPipeline (0.01s)
        --- PASS: Test/ServerUnifiedPipeline//test.Pipeline/Unary (0.00s)
        --- PASS: Test/ServerUnifiedPipeline//test.Pipeline/Collision (0.00s)
        --- PASS: Test/ServerUnifiedPipeline//test.Pipeline/Client (0.00s)
        --- PASS: Test/ServerUnifiedPipeline//test.Pipeline/Server (0.00s)
        --- PASS: Test/ServerUnifiedPipeline//test.Pipeline/Bidi (0.00s)
        --- PASS: Test/ServerUnifiedPipeline//test.Pipeline/Neither (0.00s)
        --- PASS: Test/ServerUnifiedPipeline//test.Pipeline/Unknown (0.00s)
        --- PASS: Test/ServerUnifiedPipeline//test.Unknown/Unknown (0.00s)
        --- PASS: Test/ServerUnifiedPipeline//test.Pipeline/Failure (0.00s)
    --- PASS: Test/ServerUnifiedPipelineDecompressionLimit (0.01s)
ok  	google.golang.org/grpc	0.010s
```

(Every `WriteStatus(nil ...)` line above was paired with a `writeStatus(nil) returned err=<nil> ... (no panic)` line; duplicates elided.)
Observed: part 1 holds (the runner detects and runs the root-package
`server_pipeline_ext_test.go`); part 2 does not (nil status passed for each
successful root scenario, no panic, `[PASS]`, exit 0).

## C8

Part 1 (a package suite executes a successful RPC through the HTTP/2 terminal-status path) and part 2 (that RPC dereferences nil status and fails its package suite).

```sh
cd /home/ubuntu/wt-364b59cc && go test -count=1 -cpu 1,4 -timeout 7m . ./encoding ./test; echo EXIT $?
```

```console
--- FAIL: TestEval_GetServiceInfoDeterministic (0.00s)
panic: Log in goroutine after Test/UnaryClient_ServerStreamingMismatch has completed: INFO server.go:741 [core] [Server #360] Server created  (t=+53.270201ms)
	 [recovered, repanicked]
	/home/ubuntu/wt-364b59cc/internal/grpctest/tlogger.go:247 +0x2a
FAIL	google.golang.org/grpc	52.017s
ok  	google.golang.org/grpc/encoding	0.563s
--- FAIL: Test (46.04s)
    --- FAIL: Test/Eval_FinalStatusWriteFailureDiagnostics (0.05s)
        tlogger.go:212: Expected warning 'failed to write status' not encountered
        tlogger.go:212: Expected warning 'failed to write status' not encountered
--- FAIL: TestEval_SendPreparationDiagnostics (0.10s)
    --- FAIL: TestEval_SendPreparationDiagnostics/SendPreparationCompressionFailureDiagnostic (0.05s)
        tlogger.go:125: ERROR stream.go:1820 [core] [Server #4450] grpc: server failed to encode response: rpc error: code = Internal desc = grpc: error while compressing: synthetic marshal encoding failure during compress  (t=+370.578µs)
        tlogger.go:207: Expected error 'grpc: server failed to compress response' not encountered
FAIL	google.golang.org/grpc/test	78.440s
FAIL
EXIT 1
```

```sh
grep -c "nil pointer dereference\|invalid memory address" /home/ubuntu/evidence/c8_full.txt   # -> 0
grep -n "^panic:" /home/ubuntu/evidence/c8_full.txt
# 2:panic: Log in goroutine after Test/UnaryClient_ServerStreamingMismatch has completed: ...
```

The one panic is `testing.T.Log` being called from `grpctest.TLogger` after the
`-cpu 1` pass of `Test` finished (the root fixture `TestEval_GetServiceInfoDeterministic`
constructs a `grpc.Server` in the `-cpu 4` pass while the logger still points at
the completed subtest). It is not a nil-pointer dereference and it reproduces
identically on the reference solution branch:

```sh
cd ~/repos/grpc-go   # branch grpc-go-server-unify-unary-stream-rpc-perfect + same fixtures
go test -count=1 -cpu 1,4 -timeout 7m . ./encoding ./test; echo EXIT $?
```

```console
--- FAIL: TestEval_GetServiceInfoDeterministic (0.00s)
panic: Log in goroutine after Test/UnaryClient_ServerStreamingMismatch has completed: INFO server.go:739 [core] [Server #343] Server created  (t=+52.006406ms)
FAIL	google.golang.org/grpc	51.985s
ok  	google.golang.org/grpc/encoding	0.564s
ok  	google.golang.org/grpc/test	78.637s
FAIL
EXIT 1
```

The two `./test` failures that are specific to the target branch are
log-diagnostic expectations: the target's `processRPC` does not log
`"... failed to write status: %v"` when `WriteStatus` returns an error, and its
`SendMsg` logs every preparation failure as `"grpc: server failed to encode
response"` (it never emits `"grpc: server failed to compress response"`):

```sh
cd /home/ubuntu/wt-364b59cc && grep -n 'failed to write status\|failed to compress\|failed to encode' server.go stream.go
# server.go:1486:  channelz.Warningf(... "grpc: Server.handleStream failed to write status: %v", err)   (unknown-service path only)
# server.go:1579:  channelz.Warningf(... "grpc: Server.handleStream failed to write status: %v", err)   (unknown-method path only)
# stream.go:1820:  channelz.Error(logger, ss.channelz, "grpc: server failed to encode response: ", err)
```

Both failing tests exercise error paths (a forced write failure / a forced
compression failure) — neither is a successful RPC, and neither involves a nil status.

Successful HTTP/2 RPCs in these suites, run with the WriteStatus probe applied
(focused runs of the same packages, see C1–C7, C9–C12):

```console
[verify-probe] WriteStatus(nil *status.Status) method=/grpc.testing.TestService/UnaryCall transport=*transport.http2Server
[verify-probe] writeStatus(nil) returned err=<nil> method=/grpc.testing.TestService/UnaryCall (no panic)
[verify-probe] WriteStatus(nil *status.Status) method=/test.Pipeline/Unary transport=*transport.http2Server
[verify-probe] writeStatus(nil) returned err=<nil> method=/test.Pipeline/Unary (no panic)
ok  	google.golang.org/grpc	0.010s
ok  	google.golang.org/grpc/encoding	0.007s
ok  	google.golang.org/grpc/test	0.010s
```

Observed: part 1 holds (all three packages execute successful RPCs through
`http2Server.writeStatus` with a nil status); part 2 does not — the command
exits 1, but the root failure is a test-logger ordering panic shared with the
reference branch, and the `./test` failures are missing log strings on error
paths; no nil-status dereference, `nil pointer dereference`, or
`invalid memory address` appears anywhere in the run.

## C9

Part 1 (unary trailer scenario) and part 2 (streaming trailer scenario).

```sh
cd /home/ubuntu/wt-364b59cc && bash ./test/run_eval_server_test_group.sh metadata; echo EXIT $?
```

```console
==> terminal metadata and malformed receive
{"Action":"output","Package":"google.golang.org/grpc/test","Test":"Test/ClientRequestBodyErrorUnexpectedEOF","Output":"--- PASS: Test/ClientRequestBodyErrorUnexpectedEOF (0.08s)\n"}
{"Action":"output","Package":"google.golang.org/grpc/test","Test":"Test/MultipleSetHeaderStreamingRPCError","Output":"--- PASS: Test/MultipleSetHeaderStreamingRPCError (0.03s)\n"}
{"Action":"output","Package":"google.golang.org/grpc/test","Test":"Test/MultipleSetHeaderUnaryRPCError","Output":"--- PASS: Test/MultipleSetHeaderUnaryRPCError (0.03s)\n"}
{"Action":"output","Package":"google.golang.org/grpc/test","Test":"Test/MultipleSetTrailerStreamingRPC","Output":"--- PASS: Test/MultipleSetTrailerStreamingRPC (0.08s)\n"}
{"Action":"output","Package":"google.golang.org/grpc/test","Test":"Test/MultipleSetTrailerUnaryRPC","Output":"--- PASS: Test/MultipleSetTrailerUnaryRPC (0.04s)\n"}
{"Action":"output","Package":"google.golang.org/grpc/test","Test":"Test","Output":"--- PASS: Test (0.26s)\n"}
EXIT 0
```

Same group with the WriteStatus probe applied (counts of probe lines across all test environments):

```console
      5 verify-probe] WriteStatus(nil *status.Status) method=/grpc.testing.TestService/UnaryCall transport=*transport.http2Server
      1 verify-probe] WriteStatus(nil *status.Status) method=/grpc.testing.TestService/UnaryCall transport=*transport.serverHandlerTransport
      5 verify-probe] WriteStatus(nil *status.Status) method=/grpc.testing.TestService/FullDuplexCall transport=*transport.http2Server
      1 verify-probe] WriteStatus(nil *status.Status) method=/grpc.testing.TestService/FullDuplexCall transport=*transport.serverHandlerTransport
EXIT 0
```

(Each was followed by a matching `writeStatus(nil) returned err=<nil> ... (no panic)` line.)
Observed: `MultipleSetTrailerUnaryRPC` and `MultipleSetTrailerStreamingRPC` (which
assert the merged trailers arrive at the client) passed in every environment,
including the `serverHandlerTransport` one; nil status reached `WriteStatus`
each time and returned without panic; runner exit 0.

## C10

Part 1 (unary accounting) and part 2 (full-duplex accounting).

```sh
cd /home/ubuntu/wt-364b59cc && bash ./test/run_eval_server_test_group.sh accounting; echo EXIT $?
```

```console
==> channelz accounting
{"Action":"output","Package":"google.golang.org/grpc/test","Test":"Test/CZServerMetrics","Output":"--- PASS: Test/CZServerMetrics (0.06s)\n"}
==> stats for unary and streaming success and error paths
{"Action":"output","Package":"google.golang.org/grpc/stats","Test":"Test/ServerStatsFullDuplexRPC","Output":"--- PASS: Test/ServerStatsFullDuplexRPC (0.01s)\n"}
{"Action":"output","Package":"google.golang.org/grpc/stats","Test":"Test/ServerStatsFullDuplexRPCError","Output":"--- PASS: Test/ServerStatsFullDuplexRPCError (0.00s)\n"}
{"Action":"output","Package":"google.golang.org/grpc/stats","Test":"Test/ServerStatsUnaryRPC","Output":"--- PASS: Test/ServerStatsUnaryRPC (0.00s)\n"}
{"Action":"output","Package":"google.golang.org/grpc/stats","Test":"Test/ServerStatsUnaryRPCError","Output":"--- PASS: Test/ServerStatsUnaryRPCError (0.00s)\n"}
EXIT 0
```

Same group with the WriteStatus probe applied:

```console
      1 verify-probe] WriteStatus(nil *status.Status) method=/grpc.testing.TestService/EmptyCall transport=*transport.http2Server
      2 verify-probe] WriteStatus(nil *status.Status) method=/grpc.testing.TestService/FullDuplexCall transport=*transport.http2Server
      1 verify-probe] WriteStatus(nil *status.Status) method=/grpc.testing.TestService/UnaryCall transport=*transport.http2Server
EXIT 0
```

(Each was followed by a matching `writeStatus(nil) returned err=<nil> ... (no panic)` line.)
Observed: `ServerStatsUnaryRPC` and `ServerStatsFullDuplexRPC` (successful paths,
asserting the `stats.End` event with nil error) and `CZServerMetrics`
(`CallsSucceeded` counters) passed; nil status reached `WriteStatus` and returned
without panic; both `go test` invocations in the runner exited 0.

## C11

Part 1 (unary handler selected, returns with nil error, nil status supplied) and part 2 (transport dereferences it, RPC does not complete cleanly).

```sh
cd /home/ubuntu/wt-364b59cc && go test -v ./test -run ^TestEval_SameNameMethodAndStreamDispatchPrecedence -count=1
```

```console
=== RUN   TestEval_SameNameMethodAndStreamDispatchPrecedence
--- PASS: TestEval_SameNameMethodAndStreamDispatchPrecedence (0.00s)
PASS
ok  	google.golang.org/grpc/test	0.005s
```

Same test with the WriteStatus probe applied:

```console
=== RUN   TestEval_SameNameMethodAndStreamDispatchPrecedence
[verify-probe] WriteStatus(nil *status.Status) method=/grpc.testing.TestService/UnaryCall transport=*transport.http2Server
[verify-probe] writeStatus(nil) returned err=<nil> method=/grpc.testing.TestService/UnaryCall (no panic)
--- PASS: TestEval_SameNameMethodAndStreamDispatchPrecedence (0.00s)
```

Root-package collision scenario (same probe):

```console
[verify-probe] WriteStatus(nil *status.Status) method=/test.Pipeline/Collision transport=*transport.http2Server
[verify-probe] writeStatus(nil) returned err=<nil> method=/test.Pipeline/Collision (no panic)
        --- PASS: Test/ServerUnifiedPipeline//test.Pipeline/Collision (0.00s)
```

Observed: part 1 holds (unary handler selected — the fixture asserts the unary
handler, not the stream handler, produced the response — and nil status passed);
part 2 does not (no panic, response delivered, fixture passed).

## C12

Part 1 (unknown-service handler sends a response, returns nil, nil status supplied) and part 2 (transport dereferences it before the client sees clean EOF).

```sh
cd /home/ubuntu/wt-364b59cc && go test -v ./test -run ^TestEval_UnknownServiceHandler -count=1
```

```console
=== RUN   TestEval_UnknownServiceHandler
--- PASS: TestEval_UnknownServiceHandler (0.00s)
PASS
ok  	google.golang.org/grpc/test	0.006s
```

Same test with the WriteStatus probe applied:

```console
=== RUN   TestEval_UnknownServiceHandler
[verify-probe] WriteStatus(nil *status.Status) method=/unregistered.Service/UnregisteredMethod transport=*transport.http2Server
[verify-probe] writeStatus(nil) returned err=<nil> method=/unregistered.Service/UnregisteredMethod (no panic)
--- PASS: TestEval_UnknownServiceHandler (0.00s)
```

Root-package unknown-service scenarios (same probe):

```console
[verify-probe] WriteStatus(nil *status.Status) method=/test.Pipeline/Unknown transport=*transport.http2Server
[verify-probe] writeStatus(nil) returned err=<nil> method=/test.Pipeline/Unknown (no panic)
[verify-probe] WriteStatus(nil *status.Status) method=/test.Unknown/Unknown transport=*transport.http2Server
[verify-probe] writeStatus(nil) returned err=<nil> method=/test.Unknown/Unknown (no panic)
        --- PASS: Test/ServerUnifiedPipeline//test.Pipeline/Unknown (0.00s)
        --- PASS: Test/ServerUnifiedPipeline//test.Unknown/Unknown (0.00s)
```

Fixture assertions (`test/eval_unknown_service_handler_test.go`): the
`UnknownServiceHandler` sends one message and returns nil; the client must
receive that message and then `io.EOF` from `RecvMsg`. Observed: part 1 holds
(nil status passed to `WriteStatus`), part 2 does not (response then `io.EOF`,
no panic, fixture passed).
