# Evidence — run v-279d6442

Audited branch: `verify/grpc-go-server-unify-unary-stream-rpc-v-279d6442` (from `origin/grpc-go-server-unify-unary-stream-rpc-perfect`, HEAD `59fc13de`), base commit `0c51461d`. Claim-target branches were fetched from `https://github.com/kaitranntt-evals/grpc-go-server-unify-unary-stream-rpc` (remote `evalrepo`) into detached worktrees (`git worktree add ../wt-<id> evalrepo/evalon/grpc-go-se-<id> --detach`). "Base" runs use a worktree at `0c51461d`. Go toolchain: `go1.25.7 linux/amd64`.

## C1 — VERDICT: REFUTED (branch evalon/grpc-go-se-e4b8614a)

The branch's added test `test/server_unified_pipeline_test.go` compares fixed `map[string]int` values with `fmt.Sprint` and prints them with `%v` in `t.Errorf` (lines ~181–205). Probe replicating that formatting (`verify/repro/c1_map_format_probe.go`) across 20 separate processes × 1000 prints each:

```sh
cd ~/repos/grpc-go && for i in $(seq 1 20); do go run verify/repro/c1_map_format_probe.go; done | sort -u | wc -l
```

Output: `1` — the single unique line was

```console
map[/grpc.testing.TestService/FullDuplexCall:1 /grpc.testing.TestService/StreamingInputCall:1 /grpc.testing.TestService/StreamingOutputCall:1] map[/grpc.testing.TestService/EmptyCall:1]
```

Go's `fmt` prints maps in sorted key order (since Go 1.12), so identical maps always format identically. Repeated execution of the branch test is also stable:

```sh
cd ~/repos/wt-e4b8614a/test && go test -run 'Test/ServerUnifiedPipeline_InterceptorSegregation' -count=5 -v .
```

```console
--- PASS: Test (0.01s)   (x5)
ok  	google.golang.org/grpc/test	0.028s
```

## C2 — VERDICT: CONFIRMED (audited branch)

`server.go` `processRPC` (lines 1495–1498) logs but discards the final `statusOK` `WriteStatus` error; the named return `err` stays `nil`, so the deferred block (lines 1332–1349) records `stats.End{Error: nil}` and `incrCallsSucceeded()`.

Repro `verify/repro/c2_c11_writestatus_success_accounting_test.go`: a bidi handler blocks after `Recv`, the client hard-closes the TCP connection (`SetLinger(0)` + `Close`), the handler then returns `nil`, so only the final `statusOK` `WriteStatus` remains and it fails.

```sh
cp verify/repro/c2_c11_writestatus_success_accounting_test.go test/ && cd test && go test -v -run 'TestVerifyC2WriteStatusFailureRecordedAsSuccess' -count=1 .
```

Audited branch:

```console
c2_c11_writestatus_success_accounting_test.go:139: stats.End.Error = <nil>
c2_c11_writestatus_success_accounting_test.go:146: channelz: started=1 succeeded=1 failed=0
RESULT: RPC recorded as SUCCESS despite failed final WriteStatus (claim CONFIRMED)
```

Same test on base `0c51461d` (where `processStreamingRPC` ends with `return ss.s.WriteStatus(statusOK)`), proving the harness really forces a `WriteStatus` failure:

```console
c2_c11_writestatus_success_accounting_test.go:139: stats.End.Error = rpc error: code = Unavailable desc = transport is closing
c2_c11_writestatus_success_accounting_test.go:146: channelz: started=1 succeeded=0 failed=1
```

## C3 — VERDICT: CONFIRMED (audited branch)

Repro `verify/repro/c3_c12_unary_decode_error_message_test.go`: unary `EmptyCall` against a server whose codec `Unmarshal` always fails.

```sh
cp verify/repro/c3_c12_unary_decode_error_message_test.go test/ && cd test && go test -v -run 'TestVerifyC3UnaryDecodeErrorMessage' -count=1 .
```

Audited branch:

```console
client-visible unary decode error: rpc error: code = Internal desc = grpc: failed to unmarshal the received message: verify-decoding-failed
```

Base `0c51461d` (same test):

```console
client-visible unary decode error: rpc error: code = Internal desc = grpc: error unmarshalling request: verify-decoding-failed
```

The base string comes from base `server.go:1417` (`grpc: error unmarshalling request: %v`), which the audited branch deleted; the only remaining description is `rpc_util.go:1064` (`grpc: failed to unmarshal the received message: %v`). The solution also edited the pre-existing `encoding/encoding_test.go` `TestDecodeDoesntPanicOnServer` expectation from `"grpc: error unmarshalling request"` to `"grpc: failed to unmarshal the received message"` (visible in `git diff 0c51461d HEAD -- encoding/encoding_test.go`).

## C4 — VERDICT: CONFIRMED (branch evalon/grpc-go-se-b9676765)

```sh
cd ~/repos/wt-b9676765/gcp/observability && go test -v -run 'Test/ServerRPCEventsLogAll' -count=1 .
```

```console
    Payload: observability.payload{
        ...
        MessageLength: 0,
  - 	Message:       nil,
  + 	Message:       []uint8{},
    },
--- FAIL: Test/ServerRPCEventsLogAll (0.00s)
FAIL	google.golang.org/grpc/gcp/observability	0.009s
```

go-cmp diff: got (`-`) is `nil`, want (`+`) is `[]uint8{}` — the empty unary response's binary-log payload is now nil. Same test on base `0c51461d`:

```console
ok  	google.golang.org/grpc/gcp/observability	0.060s
```

Branch cause: `stream.go:1855` logs `Message: data.Materialize()` in `serverStream.SendMsg`, which yields `nil` for an empty message. Repro: `verify/repro/c4_binlog_empty_payload.sh`.

## C5 — VERDICT: CONFIRMED — streaming part CONFIRMED, unary part REFUTED (branch evalon/grpc-go-se-7551960f)

Repro `verify/repro/c5_decode_error_descriptions_test.go` (unary `EmptyCall` + bidi `FullDuplexCall`, codec `Unmarshal` always fails):

```sh
cp verify/repro/c5_decode_error_descriptions_test.go <worktree>/test/ && cd <worktree>/test && go test -v -run 'TestVerifyC5DecodeErrorDescriptions' -count=1 .
```

Base `0c51461d` (pre-consolidation descriptions):

```console
UNARY decode error:     rpc error: code = Internal desc = grpc: error unmarshalling request: verify-decoding-failed
STREAMING decode error: rpc error: code = Internal desc = grpc: failed to unmarshal the received message: verify-decoding-failed
```

Branch `evalon/grpc-go-se-7551960f`:

```console
UNARY decode error:     rpc error: code = Internal desc = grpc: error unmarshalling request: verify-decoding-failed
STREAMING decode error: rpc error: code = Internal desc = grpc: error unmarshalling request: verify-decoding-failed
```

Unary is preserved exactly (part refuted); streaming changed from `grpc: failed to unmarshal the received message` to `grpc: error unmarshalling request` (part confirmed), so the claim as a whole ("at least one") is confirmed.

## C6 — VERDICT: CONFIRMED — both parts (branch evalon/grpc-go-se-c6b55476)

Registration structure — `server.go`:

```console
122:	methods     map[string]*MethodDesc
123:	streams     map[string]*StreamDesc
793:		methods:     make(map[string]*MethodDesc),
794:		streams:     make(map[string]*StreamDesc),
799:		info.methods[d.MethodName] = d
803:		info.streams[d.StreamName] = d
```

Dispatch selection — `server.go` (handleStream):

```console
1550:		if md, ok := srv.methods[method]; ok {
1551:			s.processRPC(ctx, stream, srv, md, nil, ti)
1554:		if sd, ok := srv.streams[method]; ok {
1555:			s.processRPC(ctx, stream, srv, nil, sd, ti)
```

`processRPC` itself branches on `isUnary := md != nil` (line 1264). Both RPC kinds exercised through these lookups via the eval fixtures:

```sh
cd ~/repos/wt-c6b55476 && cp ~/eval_tests/tests/eval_unary_roundtrip_test.go ~/eval_tests/tests/eval_interceptor_segregation_test.go test/ && cd test && go test -v -run 'TestEval_(UnaryRoundTrip|InterceptorSegregation)' -count=1 .
```

```console
--- PASS: TestEval_InterceptorSegregation (0.00s)
--- PASS: TestEval_UnaryRoundTrip (0.00s)
ok  	google.golang.org/grpc/test	0.008s
```

Repro: `verify/repro/c6_separate_unary_path.sh`.

## C7 — VERDICT: CONFIRMED — both parts (branch evalon/grpc-go-se-b1f3fdd3)

Branch `server.go` `processRPC`:

```console
1464:		ss.s.WriteStatus(appStatus)
1465:		// TODO: Should we log an error from WriteStatus here and below?
1482:	return ss.s.WriteStatus(statusOK)
```

Neither call is followed by any `channelz.Warningf`. Repro `verify/repro/c7_writestatus_warning_test.go` installs a capturing `grpclog` logger, forces terminal `WriteStatus` to fail (client hard-closes TCP while the handler is blocked; handler then returns an app error or nil), and scans all log lines:

```sh
cp verify/repro/c7_writestatus_warning_test.go <worktree>/test/ && cd <worktree>/test && go test -v -run 'TestVerifyC7WriteStatusWarning' -count=1 .
```

Branch `evalon/grpc-go-se-b1f3fdd3`:

```console
application-error-unary path: NO warning identifying the failed status write (23 log lines total)
application-error-streaming path: NO warning identifying the failed status write (23 log lines total)
success-streaming path: NO warning identifying the failed status write (23 log lines total)
```

Base `0c51461d` under the identical harness (proves the harness really makes `WriteStatus` fail, and that a warning used to be emitted on the unary app-error path):

```console
application-error-unary path: warning(s) found: ["[core][Server #1] grpc: Server.processUnaryRPC failed to write status: connection error: desc = \"transport is closing\""]
application-error-streaming path: NO warning identifying the failed status write (23 log lines total)
success-streaming path: NO warning identifying the failed status write (23 log lines total)
```

Application-error part: confirmed (regression vs. base for unary RPCs). Successful-completion part: confirmed (no warning on the branch; base had none on that path either).

## C8 — VERDICT: CONFIRMED (branch evalon/grpc-go-se-b1f3fdd3)

Branch `stream.go` `serverStream.SendMsg`:

```console
1823:			if strings.Contains(err.Error(), "error while compressing") {
1824:				operation = "compress"
```

Behavioral demonstration (`verify/repro/c8_sendmsg_text_classification_test.go`): a codec whose `Marshal` (encode) failure message merely contains the phrase is misclassified as a compression failure:

```sh
cp verify/repro/c8_sendmsg_text_classification_test.go <worktree>/test/ && cd <worktree>/test && go test -v -run 'TestVerifyC8SendMsgTextClassification' -count=1 .
```

```console
client error: rpc error: code = Internal desc = grpc: error while marshaling: proto marshal failed (error while compressing field table)
server log: [core][Server #1] grpc: server failed to compress response: rpc error: code = Internal desc = grpc: error while marshaling: proto marshal failed (error while compressing field table)
RESULT: encode failure misclassified as COMPRESS via err.Error() text match (claim CONFIRMED)
```

## C9 — VERDICT: CONFIRMED (branch evalon/grpc-go-se-c02bf7c2)

Inventory of the solution's own tests via the eval fixture:

```sh
cd ~/repos/wt-c02bf7c2 && mkdir -p .evaltools && cp ~/eval_tests/tests/candidate_test_inventory.go .evaltools/ && cp ~/eval_tests/tests/run_candidate_tests.sh test/ && bash ./test/run_candidate_tests.sh
```

```console
==> Detected substantive agent-authored tests:
    test/server_test.go	s.TestServerInterceptorSegregation
==> Running s.TestServerInterceptorSegregation in module /home/ubuntu/repos/wt-c02bf7c2 for package ./test
[PASS] s.TestServerInterceptorSegregation
```

The single candidate test contains zero references to compression or receive-size limits:

```sh
grep -c "Compress\|gzip\|RecvMsgSize\|ResourceExhausted" test/server_test.go
```

Output: `0`. `TestStreamingDecompressionExceedsMaxMessageSize` does not exist anywhere on the branch (`grep -rn` finds nothing). No solution test sends a compressed RPC exceeding `MaxRecvMsgSize` after decompression or asserts `ResourceExhausted`. Repro: `verify/repro/c9_no_compressed_oversize_test.sh`.

## C10 — VERDICT: CONFIRMED (branch evalon/grpc-go-se-2238ef47)

`test/server_pipeline_test.go` `TestUnaryRPCPipelineResponseCompression` sends a 1 KiB payload with `grpc.UseCompressor(gzip.Name)` and asserts only `proto.Equal` on the echoed payload — it never checks that the response was compressed.

Mutation (`verify/repro/c10_compression_test_mutation.sh`): remove the branch of `processRPC` in `server.go` that negotiates a response compressor (`ss.compressorV1 = encoding.GetCompressor(rc); ss.sendCompressorName = rc`), so the server can never compress responses, then run the test:

```console
=== RUN   Test/UnaryRPCPipelineResponseCompression
    --- PASS: Test/UnaryRPCPipelineResponseCompression (0.00s)
ok  	google.golang.org/grpc/test	0.012s
```

The test passes against an implementation that returns the correct protobuf response without compressing it. (`git diff --stat` during the run showed `server.go | 6 ------` as the only change; the mutation was reverted afterwards.)

## C11 — VERDICT: CONFIRMED (audited branch)

Same defect and same repro as C2 (`verify/repro/c2_c11_writestatus_success_accounting_test.go`). On the audited branch, after the client hard-closes the connection and the streaming handler returns nil (successful handler return), the failed final `statusOK` `WriteStatus` is swallowed:

```console
c2_c11_writestatus_success_accounting_test.go:139: stats.End.Error = <nil>
c2_c11_writestatus_success_accounting_test.go:146: channelz: started=1 succeeded=1 failed=0
RESULT: RPC recorded as SUCCESS despite failed final WriteStatus (claim CONFIRMED)
```

On base `0c51461d` the identical scenario records the transport failure:

```console
c2_c11_writestatus_success_accounting_test.go:139: stats.End.Error = rpc error: code = Unavailable desc = transport is closing
c2_c11_writestatus_success_accounting_test.go:146: channelz: started=1 succeeded=0 failed=1
```

## C12 — VERDICT: CONFIRMED (audited branch)

Same repro as C3 (`verify/repro/c3_c12_unary_decode_error_message_test.go`). Audited branch:

```console
client-visible unary decode error: rpc error: code = Internal desc = grpc: failed to unmarshal the received message: verify-decoding-failed
```

Base `0c51461d`:

```console
client-visible unary decode error: rpc error: code = Internal desc = grpc: error unmarshalling request: verify-decoding-failed
```

The legacy description `grpc: error unmarshalling request` is no longer produced for unary codec-decode failures; clients now see `grpc: failed to unmarshal the received message`.
