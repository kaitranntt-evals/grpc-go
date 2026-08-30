# Evidence — run v-07a86be8

Audited branch: `verify/grpc-go-server-unify-unary-stream-rpc-v-07a86be8` (from `origin/grpc-go-server-unify-unary-stream-rpc-perfect`), repo `kaitranntt-evals/grpc-go`, base commit `0c51461d27177d997e14c642fe18c11668fc09a3`. Claim-target branches were fetched from `kaitranntt-evals/grpc-go-server-unify-unary-stream-rpc` (remote `evalrepo`) and checked out as worktrees under `/home/ubuntu/wt/<suffix>`. Go 1.25.7.

```sh
cd ~/repos/grpc-go
git fetch origin grpc-go-server-unify-unary-stream-rpc-perfect
git checkout -b verify/grpc-go-server-unify-unary-stream-rpc-v-07a86be8 origin/grpc-go-server-unify-unary-stream-rpc-perfect
git remote add evalrepo https://github.com/kaitranntt-evals/grpc-go-server-unify-unary-stream-rpc
git fetch evalrepo evalon/grpc-go-se-{d9f9af3e,b93798bb,a2557f94,3c2b59dd,b18ff2e5,ffd9f4b6,1af21a7e,245dad65,f6c7d66c,6c7b7bc6,46230bde,4e5801e7}
for b in ...; do git worktree add /home/ubuntu/wt/$b evalrepo/evalon/grpc-go-se-$b; done
```

## C1 — REFUTED on all 5 branches (d9f9af3e, b93798bb, a2557f94, 3c2b59dd, b18ff2e5)

All five branches carry the identical test diff vs the base commit:

```sh
cd /home/ubuntu/wt/<branch> && git diff 0c51461d HEAD -- gcp/observability/logging_test.go
```

```diff
@@ -431,9 +431,7 @@ func (s) TestServerRPCEventsLogAll(t *testing.T) {
 			MethodName:  "UnaryCall",
 			Authority:   ss.Address,
 			SequenceID:  4,
-			Payload: payload{
-				Message: []uint8{},
-			},
+			Payload:     payload{},
 		},
```

The comparator (`cmpLoggingEntryList`, gcp/observability/logging_test.go:43-66) is `cmp.Diff` over the complete `grpcLogEntry` structs with only `CallID`, `Peer`, `IPPort`, `Type` (address) and `Timeout` ignored — no `cmpopts.EquateEmpty` — so nil and `[]uint8{}` are distinguished and `Message: nil` is *required*, not merely accepted; all other protocol/status/metadata assertions are untouched by the diff.

Test passes as committed on every branch:

```sh
cd /home/ubuntu/wt/<branch>/gcp/observability && go test -run 'Test/ServerRPCEventsLogAll' -count=1 -v .
```

```console
=== RUN   Test/ServerRPCEventsLogAll
--- PASS: Test (0.00s)
ok  	google.golang.org/grpc/gcp/observability
```

Mutation demonstrating exactness — reverting the expectation to the old non-nil empty message makes the test FAIL on every one of the 5 branches (so the live server event genuinely carries `Message: nil` now, with `MessageLength: 0` still asserted):

```sh
perl -0pi -e 's/SequenceID:  4,\n\t\t\tPayload:     payload\{\},/SequenceID:  4,\n\t\t\tPayload:     payload{Message: []uint8{}},/' logging_test.go
go test -run 'Test/ServerRPCEventsLogAll' -count=1 .
```

```console
--- FAIL: Test/ServerRPCEventsLogAll (0.00s)
              			MessageLength: 0,
            - 			Message:       nil,
            + 			Message:       []uint8{},
```

Identical output on d9f9af3e, b93798bb, a2557f94, 3c2b59dd and b18ff2e5. The comparison remains exact with nil required — the Refute condition.

## C2 — CONFIRMED (evalon/grpc-go-se-3c2b59dd)

Branch code (stream.go:1838): `ss.s.Write(hdr, payload, &transport.WriteOptions{Last: false})` — the only server data write; unary responses go through `serverStream.SendMsg`. Base commit's dedicated unary path used `Last: true` (`git show 0c51461d:server.go` line 1483: `opts := &transport.WriteOptions{Last: true}`).

Instrumentation (verify/repro/c2_writeoptions_last.patch) logs the WriteOptions before the transport Write; ran the eval unary roundtrip fixture:

```sh
cd /home/ubuntu/wt/3c2b59dd && git apply c2_writeoptions_last.patch
cp eval_tests/tests/eval_unary_roundtrip_test.go test/
go test -v ./test -run '^TestEval_UnaryRoundTrip$' -count=1
```

```console
EVIDENCE_C2 serverStream.SendMsg method=/grpc.testing.TestService/UnaryCall WriteOptions.Last=false
--- PASS: TestEval_UnaryRoundTrip (0.00s)
```

The unary response data message is written with `WriteOptions.Last=false` — the Confirm condition.

## C3 — CONFIRMED (evalon/grpc-go-se-ffd9f4b6)

Branch code (stream.go:1853): `status.Errorf(codes.ResourceExhausted, "trying to send message larger than max (%d vs. %d)", ...)` — no `grpc:` prefix. Base unary path (`git show 0c51461d:server.go` line 1215) used `"grpc: trying to send message larger than max (%d vs. %d)"`.

Live probe (verify/repro/c3_max_send_prefix_test.go.txt): server with `grpc.MaxSendMsgSize(16)`, unary handler returns a 1 KiB response:

```sh
cd /home/ubuntu/wt/ffd9f4b6 && cp c3 test file to test/evidence_c3_test.go
go test -v ./test -run '^TestEvidenceC3MaxSendStatusMessage$' -count=1
```

```console
evidence_c3_test.go:31: EVIDENCE_C3 code=ResourceExhausted message="trying to send message larger than max (1030 vs. 16)"
```

Same test on the audited `verify/...v-07a86be8` branch (perfect solution) for the established text:

```console
evidence_c3_test.go:31: EVIDENCE_C3 code=ResourceExhausted message="grpc: trying to send message larger than max (1030 vs. 16)"
```

Client-visible message on ffd9f4b6 lacks the `grpc:` prefix — the Confirm condition.

## C4 — CONFIRMED (evalon/grpc-go-se-1af21a7e)

Branch `register` (server.go:813-824) fills one `info.methods` map: Methods first, then Streams — a same-name StreamDesc overwrites the unary entry, so the streaming descriptor wins.

Live probe (verify/repro/c4_dup_name_precedence_test.go.txt): ServiceDesc with `MethodDesc{MethodName: "Call"}` and `StreamDesc{StreamName: "Call"}`, both unary and stream interceptors registered, invoked via `cc.Invoke(ctx, "/dup.Svc/Call", ...)` with a raw codec:

```sh
cd /home/ubuntu/wt/1af21a7e && go test -v ./test -run '^TestEvidenceC4DupNamePrecedence$' -count=1
```

```console
evidence_c4_test.go:69: EVIDENCE_C4 invoke err=<nil>
evidence_c4_test.go:72: EVIDENCE_C4 event=streamInterceptor
evidence_c4_test.go:72: EVIDENCE_C4 event=streamHandler
```

Same test on the audited verify branch (baseline):

```console
evidence_c4_test.go:72: EVIDENCE_C4 event=unaryHandler
```

On 1af21a7e the duplicate name selects the streaming descriptor and streaming interceptor path — the Confirm condition (baseline selects unary).

## C5 — CONFIRMED (evalon/grpc-go-se-245dad65)

Branch `recv` (rpc_util.go:1063-1068) returns `"grpc: error unmarshalling request: %v"` for every server-side decode failure (`isServer` branch), including streaming. Base: streaming decode failures produced `"grpc: failed to unmarshal the received message: %v"` (`git show 0c51461d:rpc_util.go` line 1064); `"grpc: error unmarshalling request"` was the unary-only text (base server.go:1417).

Live probe (verify/repro/c5_streaming_decode_framing_test.go.txt): genuine client-streaming RPC (`StreamingInputCall`) where the client codec emits invalid proto wire bytes (`0xff 0xff 0xff 0xff`) under content-subtype `proto`, so the server-side proto unmarshal genuinely fails:

```sh
cd /home/ubuntu/wt/245dad65 && go test -v ./test -run '^TestEvidenceC5StreamingDecodeFailure$' -count=1
```

```console
evidence_c5_test.go:47: EVIDENCE_C5 code=Internal message="grpc: error unmarshalling request: proto: cannot parse invalid wire-format data"
```

Same test on the audited verify branch (baseline, established streaming framing):

```console
evidence_c5_test.go:47: EVIDENCE_C5 code=Internal message="grpc: failed to unmarshal the received message: proto: cannot parse invalid wire-format data"
```

Streaming decode failure on 245dad65 uses the unary framing — the Confirm condition.

## C6 — CONFIRMED, both parts (evalon/grpc-go-se-f6c7d66c)

Branch `register` (server.go:822-837): for Streams, `if _, ok := info.methods[d.StreamName]; ok { continue }` keeps the *first* duplicate handler, while `info.methodInfo = append(...)` runs for every entry, so `GetServiceInfo` (server.go:861-870) returns duplicates.

Live probe (verify/repro/c6_duplicate_streamdesc_test.go.txt): one ServiceDesc with two `StreamDesc{StreamName: "Call"}` entries (handlers labeled first/last), then GetServiceInfo inspection and a live client-streaming call:

```sh
cd /home/ubuntu/wt/f6c7d66c && go test -v ./test -run '^TestEvidenceC6DuplicateStreamDescs$' -count=1
```

```console
evidence_c6_test.go:40: EVIDENCE_C6 GetServiceInfo entries for Call: 2 (methods=[{Call true false} {Call true false}])
evidence_c6_test.go:68: EVIDENCE_C6 invoked handler: firstHandler
```

Same test on the audited verify branch (baseline):

```console
evidence_c6_test.go:40: EVIDENCE_C6 GetServiceInfo entries for Call: 1 (methods=[{Call true false}])
evidence_c6_test.go:68: EVIDENCE_C6 invoked handler: lastHandler
```

Handler-selection part: first descriptor retained (baseline: last). Presentation part: two service-info entries for the duplicate name (baseline: one). Both Confirm conditions hold.

## C7 — CONFIRMED (evalon/grpc-go-se-6c7b7bc6)

Branch `serverStream.SendMsg` (stream.go:1817-1821): any `prepareMsg` failure — encoding *or* compression — is logged as

```go
channelz.Error(logger, ss.channelz, "grpc: server failed to encode response: ", err)
```

Base distinguished the stages (`git show 0c51461d:server.go` lines 1191/1198: `"grpc: server failed to encode response: "` vs `"grpc: server failed to compress response: "`).

Live probe (verify/repro/c7_channelz_compress_label_test.go.txt): registered compressor `evidence-failcomp` whose writer always fails (encoding of the proto response succeeds first inside `prepareMsg`); handler calls `grpc.SetSendCompressor(ctx, "evidence-failcomp")`; grpclog captured via `grpclog.SetLoggerV2`:

```sh
cd /home/ubuntu/wt/6c7b7bc6 && go test -v ./test -run '^TestEvidenceC7CompressionFailureChannelzLabel$' -count=1
```

```console
EVIDENCE_C7 rpc err=rpc error: code = Internal desc = grpc: error while compressing: synthetic compression failure
EVIDENCE_C7 channelz log: [core] [Server #1] grpc: server failed to encode response: rpc error: code = Internal desc = grpc: error while compressing: synthetic compression failure
```

Same test on the audited verify branch (baseline):

```console
EVIDENCE_C7 channelz log: [core] [Server #1] grpc: server failed to compress response: rpc error: code = Internal desc = grpc: error while compressing: synthetic compression failure
```

A compression failure is labeled "failed to encode response" in the channelz diagnostic — the Confirm condition.

## C8 — CONFIRMED (evalon/grpc-go-se-46230bde)

`server_unified_pipeline_test.go` (repo root, 313 lines) contains a single top-level `TestUnifiedServerPipeline` with zero `t.Run` subtests (`grep -c 't.Run(' server_unified_pipeline_test.go` → 0) running unary success, client-streaming, server-streaming, bidi, false/false streaming failure (DataLoss), compressed-oversized ResourceExhausted, and interceptor-tally scenarios sequentially with `t.Fatalf` on each scenario's setup/IO steps.

Baseline and mutation run (verify/repro/c8_fatal_ordering.sh):

```sh
cd /home/ubuntu/wt/46230bde
go test -v . -run '^TestUnifiedServerPipeline$' -count=1     # --- PASS
sed -i 's|cc.Invoke(ctx, method("Unary"), wrapperspb.String("request")|cc.Invoke(ctx, method("UnaryMissing"), wrapperspb.String("request")|' server_unified_pipeline_test.go
go test -v . -run '^TestUnifiedServerPipeline$' -count=1
```

```console
=== RUN   TestUnifiedServerPipeline
    server_unified_pipeline_test.go:200: unary Invoke() failed: rpc error: code = Unimplemented desc = unknown method UnaryMissing for service grpc.testing.UnifiedPipelineService
--- FAIL: TestUnifiedServerPipeline (0.00s)
```

The first scenario's Fatalf is the only reported failure — none of the later streaming/status/compression/interceptor scenarios executed or reported. The Confirm condition holds.

## C9 — CONFIRMED (evalon/grpc-go-se-f6c7d66c)

The lifecycle defer in `processRPC` (server.go:1307-1342, the consolidated tracing/stats/channelz completion defer) carries exactly one comment:

```sh
cd /home/ubuntu/wt/f6c7d66c && sed -n '1307,1311p' server.go
```

```console
	if sh != nil || trInfo != nil || channelz.IsOn() {
		// Tracing, stats handling, and channelz accounting share one defer to
		// reduce stack usage. A defer takes ~56-64 bytes on the stack, so using
		// one here avoids an otherwise unnecessary stack growth.
		defer func() {
```

Scanning the whole function (server.go:1278-1475) for any other defer/ordering/panic commentary:

```sh
awk 'NR>=1278 && NR<=1475 && (/defer/ || /\/\//)' server.go | grep -n 'defer\|order\|panic\|reverse'
```

```console
1:		// Tracing, stats handling, and channelz accounting share one defer to
2:		// reduce stack usage. A defer takes ~56-64 bytes on the stack, so using
4:		defer func() {
```

Only stack-size context; nothing documents reverse execution ordering among tracing/stats/channelz/terminal accounting or handler-panic behavior. The Confirm condition holds.

## C10 — CONFIRMED, both parts (evalon/grpc-go-se-4e5801e7)

Representation: `methodDesc` (server.go:130-136) stores `handler any`; `processRPC` dispatches via unchecked (single-result, panic-on-mismatch) assertions `md.handler.(StreamHandler)` (server.go:1411) and `md.handler.(MethodHandler)` (server.go:1424).

Reachability: instrumentation (verify/repro/c10_any_assertions.patch) prints immediately before each assertion; ran the eval fixtures exercising ordinary registered unary and streaming RPCs:

```sh
cd /home/ubuntu/wt/4e5801e7 && git apply c10_any_assertions.patch
cp eval_tests tests/eval_unary_roundtrip_test.go tests/eval_interceptor_segregation_test.go into test/
go test -v ./test -run '^(TestEval_UnaryRoundTrip|TestEval_InterceptorSegregation)$' -count=1
```

```console
EVIDENCE_C10 unary dispatch: asserting md.handler.(MethodHandler) for /grpc.testing.TestService/UnaryCall (dynamic type grpc.MethodHandler)
EVIDENCE_C10 stream dispatch: asserting md.handler.(StreamHandler) for /grpc.testing.TestService/StreamingInputCall (dynamic type grpc.StreamHandler)
EVIDENCE_C10 stream dispatch: asserting md.handler.(StreamHandler) for /grpc.testing.TestService/StreamingOutputCall (dynamic type grpc.StreamHandler)
EVIDENCE_C10 stream dispatch: asserting md.handler.(StreamHandler) for /grpc.testing.TestService/FullDuplexCall (dynamic type grpc.StreamHandler)
--- PASS: TestEval_UnaryRoundTrip (0.00s)
--- PASS: TestEval_InterceptorSegregation (0.00s)
```

Both parts of the Confirm condition hold: handlers are stored as `any` and the unchecked assertions execute during ordinary live dispatch.
