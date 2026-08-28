# Evidence — audit v-3b6e27f2

Audited branch: `grpc-go-server-unify-unary-stream-rpc-perfect` (commit `59fc13de`, base `0c51461d`).
Claim-target branches were fetched from `https://github.com/kaitranntt-evals/grpc-go-server-unify-unary-stream-rpc` into detached worktrees `~/repos/wt-<suffix>`.
Toolchain: `go1.25.7 linux/amd64`.

## C1

Verdict: REFUTED (branch `evalon/grpc-go-se-e4b8614a`).

The recorder maps asserted by the solution's test are string-keyed:

```sh
cd ~/repos/wt-e4b8614a && grep -n "unaryCalls\|streamCalls\|fmt.Sprint" test/server_unified_pipeline_test.go
```

```console
47:	unaryCalls  map[string]int
48:	streamCalls map[string]int
195:		if fmt.Sprint(rec.unaryCalls) != fmt.Sprint(wantUnary) {
198:		if fmt.Sprint(rec.streamCalls) != fmt.Sprint(wantStream) {
```

Since Go 1.12, `fmt` prints maps with sorted keys, so `map[string]int` formatting is deterministic regardless of insertion or iteration order. Demonstration probe (two maps built in opposite insertion orders, compared 1000 times):

```sh
go run main.go   # probe building map[string]int in opposite insertion orders, comparing fmt.Sprint 1000x
```

```console
deterministic: true map[/svc/A:1 /svc/B:1 /svc/C:1]
```

The test itself passes repeatedly in the provided Go environment:

```sh
cd ~/repos/wt-e4b8614a && go test ./test -run 'Test/ServerUnifiedPipeline_InterceptorSegregation' -count=5 -v
```

```console
--- PASS: Test (0.06s)
    --- PASS: Test/ServerUnifiedPipeline_InterceptorSegregation (0.05s)
--- PASS: Test (0.01s)
    --- PASS: Test/ServerUnifiedPipeline_InterceptorSegregation (0.00s)
--- PASS: Test (0.01s)
    --- PASS: Test/ServerUnifiedPipeline_InterceptorSegregation (0.00s)
```

The asserted map key type (`string`) is deterministically ordered by `fmt`, so assertion output does not depend on map iteration order. REFUTED.

## C2

Verdict: CONFIRMED (audited branch).

`processRPC`'s final status write uses a shadowing initializer, so the write failure never reaches the named `err` result:

```sh
cd ~/repos/grpc-go && grep -n -A 3 'WriteStatus(statusOK)' server.go
```

```console
1495:	if err := ss.s.WriteStatus(statusOK); err != nil {
1496:		channelz.Warningf(logger, s.channelz, "grpc: Server.processRPC failed to write status: %v", err)
1497:	}
1498:	return err
```

Line 1495's `err :=` shadows the named result; line 1498 returns the outer `err`, still `nil`. Runtime probe (bidi RPC whose handler returns success after the client TCP connection is killed, so `WriteStatus(statusOK)` is the only failing write):

```sh
cd ~/repos/grpc-go && go test ./verify/repro -run TestVerifyC2C12StatusOKWriteFailure -count=1 -v
```

```console
    verify_c2_c12_test.go:124: stats.End.Error = <nil>
    verify_c2_c12_test.go:131: channelz server 1: started=1 succeeded=1 failed=0
        2026/08/28 16:09:07 WARNING: [core] [Server #1] grpc: Server.processRPC failed to write status: connection error: desc = "transport is closing"
--- PASS: TestVerifyC2C12StatusOKWriteFailure (1.51s)
```

The warning proves `WriteStatus(statusOK)` did fail, yet the deferred stats handler received `stats.End.Error = <nil>` — the deferred logic reads the named `err`, which the shadowed initializer left unchanged, so `processRPC` returned the unchanged outer result. Impact: any caller-side accounting keyed on `processRPC`'s return (stats, channelz, tracing) treats an RPC whose status never reached the client as fully successful; the trigger is an ordinary client disconnect racing the server's completion. CONFIRMED.

## C3

Verdict: CONFIRMED (audited branch).

Runtime probe: a unary RPC with a server codec whose `Unmarshal` always fails:

```sh
cd ~/repos/grpc-go && go test ./verify/repro -run TestVerifyC3UnaryUnmarshalText -count=1 -v
```

```console
    verify_c3_test.go:50: code=Internal message="grpc: failed to unmarshal the received message: boom: forced unmarshal failure"
--- PASS: TestVerifyC3UnaryUnmarshalText (0.00s)
```

The unary decode failure surfaces the streaming-style text instead of the established unary text `grpc: error unmarshalling request`. Source of the message on the audited branch (single shared decode path with no unary re-framing):

```sh
cd ~/repos/grpc-go && grep -n -B 1 -A 2 'failed to unmarshal the received message' rpc_util.go
```

```console
1063:	if err := c.Unmarshal(data, m); err != nil {
1064:		return status.Errorf(codes.Internal, "grpc: failed to unmarshal the received message: %v", err)
1065:	}
```

Impact: clients, tests, and log/alert matchers that key on the long-established unary status text `grpc: error unmarshalling request` silently stop matching; every malformed unary request hits this. CONFIRMED.

## C4

Verdict: CONFIRMED (branch `evalon/grpc-go-se-b1f3fdd3`) — both parts held.

Running the committed GCP observability binary-log test on the target branch (module has `replace google.golang.org/grpc => ../..`):

```sh
cd ~/repos/wt-b1f3fdd3/gcp/observability && go test . -run 'Test/ServerRPCEventsLogAll' -count=1
```

```console
--- FAIL: Test (0.05s)
    --- FAIL: Test/ServerRPCEventsLogAll (0.05s)
              			MessageLength: 0,
            - 			Message:       nil,
            + 			Message:       []uint8{},
FAIL
FAIL	google.golang.org/grpc/gcp/observability	0.062s
```

Produced representation (`- Message: nil`): the unified unary response handling materializes the empty encoded response as a nil byte slice (`data.Materialize()` on an empty `mem.BufferSlice` yields `nil`). Integration expectation (`+ Message: []uint8{}`): the committed test requires a non-nil empty byte slice. The two disagree and the test fails. Impact: the solution's own committed integration suite is red on an everyday empty-response unary RPC; any CI running the gcp/observability module fails. CONFIRMED.

## C5

Verdict: CONFIRMED (branch `evalon/grpc-go-se-7551960f`).

Runtime probe: a streaming RPC whose server codec `Unmarshal` fails:

```sh
cd ~/repos/wt-7551960f && go test ./test -run 'Test/VerifyC5' -count=1 -v
```

```console
    verify_c5_test.go:57: code=Internal message="grpc: error unmarshalling request: boom: forced unmarshal failure"
--- PASS: Test (0.01s)
    --- PASS: Test/VerifyC5StreamingUnmarshalText (0.00s)
```

The streaming decode failure begins with the unary text `grpc: error unmarshalling request`, not the established streaming text. Source of the rewrite on the target branch:

```sh
cd ~/repos/wt-7551960f && grep -n -B 2 -A 4 'error unmarshalling request' rpc_util.go
```

```console
	if err := c.Unmarshal(data, m); err != nil {
		if isServer {
			return status.Errorf(codes.Internal, "grpc: error unmarshalling request: %v", err)
		}
		return status.Errorf(codes.Internal, "grpc: failed to unmarshal the received message: %v", err)
	}
```

The `isServer` branch applies the unary wording to every server-side decode failure, including streaming receives. Impact: clients and monitoring that match the streaming prefix `grpc: failed to unmarshal the received message` stop matching for all streaming RPCs with malformed messages. CONFIRMED.

## C6

Verdict: CONFIRMED (branch `evalon/grpc-go-se-c53e1aa0`).

`handleStream` performs two separate map lookups before invoking the common processor:

```sh
cd ~/repos/wt-c53e1aa0 && grep -n -A 3 'if md, ok := srv.methods\[method\]; ok' server.go && grep -n -A 3 'if sd, ok := srv.streams\[method\]; ok' server.go && grep -n 'func (s \*Server) processRPC' server.go
```

```console
1576:		if md, ok := srv.methods[method]; ok {
1577:			s.processRPC(ctx, stream, srv, md, nil, ti)
1578:			return
1579:		}
1580:		if sd, ok := srv.streams[method]; ok {
1581:			s.processRPC(ctx, stream, srv, nil, sd, ti)
1582:			return
1583:		}
1270:func (s *Server) processRPC(ctx context.Context, stream *transport.ServerStream, info *serviceInfo, md *MethodDesc, sd *StreamDesc, trInfo *traceInfo) (err error)
```

Behavioral accompaniment — both dispatch paths function (the eval interceptor-segregation fixture exercises unary plus all three streaming shapes end-to-end on this branch):

```sh
cd ~/repos/wt-c53e1aa0 && cp ~/eval_tests/tests/eval_interceptor_segregation_test.go test/ && go test ./test -run 'Test/Eval_InterceptorSegregation' -count=1 -v
```

```console
--- PASS: Test (0.00s)
--- PASS: TestEval_InterceptorSegregation (0.01s)
ok  	google.golang.org/grpc/test	0.011s
```

Registration keeps unary descriptors in `srv.methods` and streaming descriptors in `srv.streams`, and `handleStream` selects via two separate lookups/branches (passing mutually-exclusive `md`/`sd` pointers) before calling the shared `processRPC` — the dispatch layer was not unified into one descriptor collection with one lookup. Impact: the refactor's goal of a single dispatch path is not met on this branch; the processor's dual-pointer signature keeps unary/streaming special-casing alive downstream. CONFIRMED.

## C7

Verdict: CONFIRMED (branch `evalon/grpc-go-se-c53e1aa0`) — both parts held.

Source on the target branch: the application-status write's error is discarded, and the success-status write's error is returned as the lifecycle result:

```sh
cd ~/repos/wt-c53e1aa0 && grep -n -A 1 'ss.s.WriteStatus(appStatus)' server.go && grep -n 'return ss.s.WriteStatus(statusOK)' server.go
```

```console
	ss.s.WriteStatus(appStatus)
	// TODO: Should we log an error from WriteStatus here and below?
	return appErr
	return ss.s.WriteStatus(statusOK)
```

Runtime probe (bidi RPC; the client TCP connection is killed before the handler returns, so the terminal `WriteStatus` is the failing write; grpclog captured; channelz on):

```sh
cd ~/repos/wt-c53e1aa0 && go test ./test -run 'Test/VerifyC7' -count=1 -v
```

```console
=== RUN   Test/VerifyC7AppStatusWriteFailure
    verify_c7_test.go:136: stats.End.Error = rpc error: code = Internal desc = app boom
    verify_c7_test.go:137: log contains 'failed to write status': false
=== RUN   Test/VerifyC7StatusOKWriteFailure
    verify_c7_test.go:142: stats.End.Error = rpc error: code = Unavailable desc = transport is closing
    verify_c7_test.go:143: channelz succeeded=0 failed=1
    verify_c7_test.go:144: log contains 'failed to write status': false
--- PASS: Test (3.02s)
```

Application-error part: the failed `WriteStatus(appStatus)` produced no contextual server-side diagnostic (`log contains 'failed to write status': false`). Successful-completion part: the handler returned success, yet the lifecycle result became the transport-write error (`stats.End.Error = ... transport is closing`, `channelz succeeded=0 failed=1`), again with no contextual diagnostic. Impact: operators get no signal when terminal statuses fail to reach clients, and a successful handler run is mis-accounted as a failed RPC on an ordinary client disconnect. CONFIRMED (both parts).

## C8

Verdict: CONFIRMED (branch `evalon/grpc-go-se-7551960f`).

Structural inspection command and output (SendMsg body vs prepareMsg body in the same `stream.go`):

```sh
cd ~/repos/wt-7551960f && awk '/func \(ss \*serverStream\) SendMsg/,/^}/' stream.go | grep -n -E 'PreparedMsg|encode\(|compress\(|msgHeader\(' && awk '/^func prepareMsg/,/^}/' stream.go | grep -n -E 'PreparedMsg|encode\(|compress\(|msgHeader\('
```

```console
38:	if preparedMsg, ok := m.(*PreparedMsg); ok {
42:		data, err = encode(ss.codec, m)
47:		compData, cpf, err := compress(data, ss.compressorV0, ss.compressorV1, ss.p.bufferPool)
53:		hdr, payload = msgHeader(data, compData, cpf)
2:	if preparedMsg, ok := m.(*PreparedMsg); ok {
7:	data, err = encode(codec, m)
11:	compData, pf, err := compress(data, cp, comp, pool)
16:	hdr, payload = msgHeader(data, compData, pf)
```

`serverStream.SendMsg` contains its own `PreparedMsg` type-switch, `encode`, `compress`, and `msgHeader` sequence — the identical four-stage pipeline `prepareMsg` (defined in the same file) already implements — instead of delegating to it. The duplication is observed in the build that produced the passing runtime probes above (same worktree, same `stream.go` compiled by `go test` in C5's run — `ok google.golang.org/grpc/test`). Impact: two copies of the response-preparation pipeline can drift (e.g. a fix to compression error handling in one is missed in the other). CONFIRMED.

## C9

Verdict: CONFIRMED (branch `evalon/grpc-go-se-c02bf7c2`).

Inventory and run of the solution-authored tests using the eval's exact discovery harness (fixtures `candidate_test_inventory.go` and `run_candidate_tests.sh` from the archive placed at `.evaltools/` and `test/`):

```sh
cd ~/repos/wt-c02bf7c2 && bash test/run_candidate_tests.sh
```

```console
==> Detected substantive agent-authored tests:
    test/server_test.go	s.TestServerInterceptorSegregation
==> Running s.TestServerInterceptorSegregation in module /home/ubuntu/repos/wt-c02bf7c2 for package ./test
[PASS] s.TestServerInterceptorSegregation
```

The full solution test surface on the branch:

```sh
cd ~/repos/wt-c02bf7c2 && git diff --name-only 0c51461d27177d997e14c642fe18c11668fc09a3 HEAD -- '*_test.go' '**/*_test.go'
```

```console
test/server_test.go
```

The single authored test, `TestServerInterceptorSegregation`, drives one unary call and three streaming calls that all succeed, asserts interceptor routing, metadata/deadline propagation, and normal `io.EOF` stream termination — every RPC in it completes successfully. It exercises no decompression rejection, no malformed receive, no application-status failure, nor any other failure path of the refactored pipeline. Impact: the refactor's error-handling paths (the ones confirmed broken in C5/C7 on sibling branches) ship untested on this branch. CONFIRMED.

## C10

Verdict: CONFIRMED (branch `evalon/grpc-go-se-b1f3fdd3`).

`GetServiceInfo` on the target branch builds the method list from a single combined map with no grouping:

```sh
cd ~/repos/wt-b1f3fdd3 && grep -n -A 8 'methods := make(\[\]MethodInfo' server.go
```

```console
	methods := make([]MethodInfo, 0, len(srv.methods))
	for m, d := range srv.methods {
		methods = append(methods, MethodInfo{
			Name:           m,
			IsClientStream: d.clientStreams,
			IsServerStream: d.serverStreams,
		})
	}
```

Runtime probe registering unary and streaming methods and sampling `GetServiceInfo` across 200 fresh servers:

```sh
cd ~/repos/wt-b1f3fdd3 && go test ./test -run 'Test/VerifyC10' -count=1 -v
```

```console
    verify_c10_test.go:28: iteration 0: streaming method precedes unary method: [{Name:UnaryCall IsClientStream:false IsServerStream:false} {Name:CacheableUnaryCall IsClientStream:false IsServerStream:false} {Name:UnimplementedCall IsClientStream:false IsServerStream:false} {Name:StreamingOutputCall IsClientStream:false IsServerStream:true} {Name:StreamingInputCall IsClientStream:true IsServerStream:false} {Name:FullDuplexCall IsClientStream:true IsServerStream:true} {Name:HalfDuplexCall IsClientStream:true IsServerStream:true} {Name:EmptyCall IsClientStream:false IsServerStream:false}]
    verify_c10_test.go:35: interleaved orderings observed: 176/200
--- PASS: Test (0.00s)
```

In 176 of 200 samples a streaming descriptor preceded a unary descriptor (e.g. `StreamingOutputCall` before `EmptyCall` above), because Go map iteration order is randomized and nothing groups unary methods first. Impact: consumers of `GetServiceInfo` (reflection tooling, gateway generators, tests) that relied on the upstream unary-then-streaming grouping now see a randomly interleaved order that changes between process runs. CONFIRMED.

## C11

Verdict: CONFIRMED (branch `evalon/grpc-go-se-008dcfa6`) — both parts held.

Source on the target branch — send-path classification and receive-path rewriting both key on human-readable text:

```sh
cd ~/repos/wt-008dcfa6 && grep -n 'strings.Contains\|strings.HasPrefix' stream.go
```

```console
1848:			if strings.Contains(status.Convert(err).Message(), "marshaling") {
1954:			if strings.HasPrefix(st.Message(), prefix) {
```

Runtime probe:

```sh
cd ~/repos/wt-008dcfa6 && go test ./test -run 'Test/VerifyC11' -count=1 -v
```

```console
    verify_c11_test.go:86: unary status message: "grpc: error unmarshalling request: boom"
    verify_c11_test.go:123: [contains-marshaling] encode-classified=true compress-classified=false
    verify_c11_test.go:123: [plain] encode-classified=false compress-classified=true
--- PASS: Test (0.41s)
    --- PASS: Test/VerifyC11RecvRewriteByText (0.00s)
    --- PASS: Test/VerifyC11SendClassifyByText (0.40s)
```

Send path: the same compression-stage failure was logged as `grpc: server failed to encode response` when its error text contained the word "marshaling" (`encode-classified=true`) and as `grpc: server failed to compress response` when it did not (`compress-classified=true`) — classification flipped solely because the wording changed (line 1848's `strings.Contains(..., "marshaling")`). Receive path: a plain codec `Unmarshal` error (`boom`) was rewritten to `grpc: error unmarshalling request: boom` because the wrapped message matched the fixed prefix `grpc: failed to unmarshal the received message: ` (line 1954's `strings.HasPrefix` plus `strings.TrimPrefix`); the rewrite exists only while that exact wrapper wording holds. Impact: diagnostics and client-visible status text silently change when unrelated error wording changes; misclassification occurs for any compression error mentioning "marshaling". CONFIRMED (both parts).

## C12

Verdict: CONFIRMED (audited branch).

Same probe as C2 (bidi RPC returning success after the client TCP connection is killed, forcing only the final `WriteStatus(statusOK)` to fail):

```sh
cd ~/repos/grpc-go && go test ./verify/repro -run TestVerifyC2C12StatusOKWriteFailure -count=1 -v
```

```console
    verify_c2_c12_test.go:124: stats.End.Error = <nil>
    verify_c2_c12_test.go:131: channelz server 1: started=1 succeeded=1 failed=0
        2026/08/28 16:09:07 WARNING: [core] [Server #1] grpc: Server.processRPC failed to write status: connection error: desc = "transport is closing"
--- PASS: TestVerifyC2C12StatusOKWriteFailure (1.51s)
```

The warning proves the final `WriteStatus(statusOK)` failed, yet deferred stats reported `stats.End.Error = <nil>` and channelz counted the call as succeeded (`succeeded=1 failed=0`). Cause on the audited branch (`server.go`):

```console
1495:	if err := ss.s.WriteStatus(statusOK); err != nil {
1496:		channelz.Warningf(logger, s.channelz, "grpc: Server.processRPC failed to write status: %v", err)
1497:	}
1498:	return err
```

The `if err := ...` initializer shadows `processRPC`'s named `err` result; the deferred stats/channelz block reads the named result, which stays `nil`. Impact: stats-based SLO accounting and channelz both count RPCs whose status never reached the client as successes; the trigger is an everyday client disconnect. CONFIRMED.

## Eval fixture runs on the audited branch

Fixtures from `eval_tests.zip` were placed byte-exact (`test/eval_unary_roundtrip_test.go`, `test/eval_interceptor_segregation_test.go`, `test/run_eval_server_test_group.sh`, `encoding/eval_decompression_limits_test.go`, `.evaltools/candidate_test_inventory.go`, `test/run_candidate_tests.sh`).

```sh
cd ~/repos/grpc-go && go test -v ./test -run '^TestEval_UnaryRoundTrip$' -count=1
cd ~/repos/grpc-go && go test -v ./test -run '^TestEval_InterceptorSegregation$' -count=1
cd ~/repos/grpc-go && go test -v ./encoding -run '^TestEval_DecompressionLimits$' -count=1
cd ~/repos/grpc-go && bash ./test/run_eval_server_test_group.sh compatibility
cd ~/repos/grpc-go && bash ./test/run_eval_server_test_group.sh lifecycle
cd ~/repos/grpc-go && bash ./test/run_candidate_tests.sh
cd ~/repos/grpc-go && go vet ./...
```

```console
--- PASS: TestEval_UnaryRoundTrip (0.00s)
--- PASS: TestEval_InterceptorSegregation (0.00s)
    --- PASS: TestEval_DecompressionLimits/Unary (0.00s)
    --- PASS: TestEval_DecompressionLimits/Streaming (0.00s)
==> server API and shutdown behavior
--- PASS: Test (0.51s)
==> interceptor chains and repeated unary sends
--- PASS: Test (0.14s)
==> terminal metadata, malformed receive, channelz, and tracing
--- PASS: Test (0.24s)
==> stats for unary and streaming success and error paths
--- PASS: Test (0.02s)
==> binary logs for unary and streaming success and error paths
--- PASS: Test (0.02s)
[PASS] s.TestStreamingDecompressionExceedsMaxMessageSize
[PASS] s.TestEncodeDoesntPanicOnServer
[PASS] s.TestDecodeDoesntPanicOnServer
[PASS] s.TestServerRPCEventsLogAll
[PASS] s.TestInterceptorSegregation
VET_EXIT=0
```

All eval fixtures pass on the audited branch; `go vet ./...` is clean.
