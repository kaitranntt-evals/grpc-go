# Audit evidence — run v-a34ca4eb

Environment: `go1.25.7 linux/amd64`. Audited branch: `verify/grpc-go-server-unify-unary-stream-rpc-v-a34ca4eb` (created from `grpc-go-server-unify-unary-stream-rpc-perfect`). Base test commit used by the fixture scripts: `0c51461d27177d997e14c642fe18c11668fc09a3`. Claim-target branches were fetched from `https://github.com/kaitranntt-evals/grpc-go-server-unify-unary-stream-rpc` into worktrees under `/home/ubuntu/wt/<short-id>`. The eval fixtures from `eval_tests.zip` were copied byte-exact into their prescribed paths (`.evaltools/candidate_test_inventory.go`, `encoding/eval_*.go`, `eval_service_info_order_test.go`, `test/eval_*.go`, `test/run_*.sh`) before running any check script.

## C1

Claim: the target branches retain two handler-bearing descriptor stores used for dispatch/service information.

Source evidence (identical structure on all six branches d94dffcf, 88a87df0, 1356d378, e5ed98e5, 26b9c092, 6b6f6e1f):

```sh
grep -n -A6 'type serviceInfo struct' server.go
```

```go
type serviceInfo struct {
	serviceImpl any
	methods     map[string]*StreamDesc // *MethodDesc on some branches
	streams     map[string]*StreamDesc
	mdata       any
}
```

Registration populates both stores independently, and dispatch/`GetServiceInfo` consult both maps independently.

Behavioral probe (`verify/repro/c1_registry_stores_test.go` copied into each branch worktree root, then removed):

```sh
go test -run TestC1Probe -v .
```

Key output, identical on every one of the six branches:

```console
methods["U"] type=*grpc.StreamDesc handlerPresent=true
streams["St"] type=*grpc.StreamDesc handlerPresent=true
handler-bearing stores: methods=true streams=true
RESULT: TWO handler-bearing descriptor stores retained
ok   google.golang.org/grpc 0.003s
```

Impact: the unification goal was a single stream-based registry; keeping two handler-bearing stores means dispatch and `GetServiceInfo` must stay consistent across two parallel structures, and future registration changes must be duplicated — exactly the divergence risk the task was meant to remove.

## C2

Claim: the changed tests on the target branches do not exercise affected streaming failure behavior.

```sh
for b in 1356d378 174b7918 26e11cab 4c4409b0; do cd /home/ubuntu/wt/$b; \
  f=$(git diff --name-only 0c51461d27177d997e14c642fe18c11668fc09a3 HEAD -- '*_test.go' | grep -v 'gcp/'); \
  echo "== $b: $f"; grep -n 'ResourceExhausted\|CloseAndRecv\|status.Code' $f | head -8; done
```

```console
== 1356d378: test/server_unified_rpc_test.go
194:	if status.Code(err) != codes.ResourceExhausted {
195:		t.Fatalf("UnaryCall() error = %v, want code %v", err, codes.ResourceExhausted)
== 174b7918: test/server_unified_rpc_test.go
194:	if got := status.Code(err); got != codes.ResourceExhausted {
195:		t.Fatalf("UnaryCall() code = %v, want %v; error: %v", got, codes.ResourceExhausted, err)
== 26e11cab: test/server_unified_rpc_test.go
223:	if got := status.Code(err); got != codes.ResourceExhausted {
224:		t.Fatalf("unary RPC status = %v, want %v (err: %v)", got, codes.ResourceExhausted, err)
== 4c4409b0: test/server_unified_rpc_test.go
221:	if got, want := status.Code(err), codes.ResourceExhausted; got != want {
```

On these four branches the only failure-path assertion is the unary `ResourceExhausted`; every streaming assertion in the changed test files requires plain success (echoed responses, interceptor counters). No streaming decompression-limit, send-failure, or error-status-propagation assertion exists.

Branch 129b065f is the exception — its changed `test/server_unified_pipeline_test.go` contains:

```go
stream, err := ss.Client.StreamingInputCall(ctx, grpc.UseCompressor(gzip.Name))
...
if _, err := stream.CloseAndRecv(); status.Code(err) != codes.ResourceExhausted {
	t.Errorf("stream.CloseAndRecv() = _, %v, want _, error code %v", err, codes.ResourceExhausted)
}
```

plus a bidirectional `FullDuplexCall` error-status propagation assertion. C2 is therefore REFUTED on 129b065f and CONFIRMED on the other four branches.

Impact: the unified pipeline changed exactly the shared receive/decompress/limit path; leaving streaming failure behavior untested on those branches means a regression there (wrong code, missing limit enforcement on streams) would ship green.

## C3

Claim: a server-side response-compression failure is logged with the encoding-stage diagnostic.

Probe (`verify/repro/c3_compression_diagnostic_test.go` copied into the target worktree's `test/` directory, then removed): registers a compressor whose `Compress` returns `errors.New("c3 induced compression failure")` and makes a unary RPC with a nonempty response.

```sh
go test ./test -run '^TestC3CompressionFailureDiagnostic$' -v -count=1
```

Key output:

```console
grpc: server failed to encode response: rpc error: code = Internal desc = grpc: error while compressing: c3 induced compression failure
RESULT: compression failure emitted the encoding-stage diagnostic 'server failed to encode response'
--- PASS: TestC3CompressionFailureDiagnostic
```

Impact: operators triaging server logs are pointed at message encoding (codec/marshal problems) when the actual failure is the compressor; the historical, stage-accurate `server failed to compress response` diagnostic is lost.

## C4

Claim: server-side message-preparation logic is duplicated.

On all eight target branches (4c4409b0, 7b946a8c, c58a6c27, 88a87df0, d94dffcf, 1356d378, 26e11cab, e5ed98e5), `stream.go` contains both the shared `prepareMsg` used by the client paths and a server-only helper repeating the same decision tree:

```sh
bash verify/repro/c4_prepare_duplication.sh /home/ubuntu/wt/<short-id>
```

```go
func (ss *serverStream) prepareMsg(m any) (hdr []byte, data, payload mem.BufferSlice, pf payloadFormat, err error) {
	if preparedMsg, ok := m.(*PreparedMsg); ok {
		return preparedMsg.hdr, preparedMsg.encodedData, preparedMsg.payload, preparedMsg.pf, nil
	}
	data, err = encode(ss.codec, m)
	if err != nil {
		channelz.Error(logger, ss.channelz, "grpc: server failed to encode response: ", err)
		return nil, nil, nil, 0, err
	}
	compData, pf, err := compress(data, ss.compressorV0, ss.compressorV1, ss.p.bufferPool)
	if err != nil {
		data.Free()
		channelz.Error(logger, ss.channelz, "grpc: server failed to compress response: ", err)
		return nil, nil, nil, 0, err
	}
	hdr, payload = msgHeader(data, compData, pf)
	return hdr, data, payload, pf, nil
}
```

The shared `func prepareMsg(m any, codec baseCodec, cp Compressor, comp encoding.Compressor, pool mem.BufferPool)` implements the identical PreparedMsg/encode/compress/msgHeader sequence. Confirmed on every one of the eight branches.

Impact: the task's goal was one preparation path; the duplicate means encode/compress/framing fixes must be applied twice, and the two copies can silently diverge (the C3 diagnostic mismatch is an example of behavior drifting in exactly this duplicated region).

## C5

Claim: the changed descriptor-shape tests on e5ed98e5 rely on generic success for stream payloads.

Mutation (`verify/repro/c5_generic_success_mutation.sh`): in the branch's changed `test/server_unified_rpc_test.go`, replace every stream handler response with a corrupted payload:

```sh
sed -i 's/stream\.SendMsg(in)/stream.SendMsg(wrapperspb.Bytes([]byte("CORRUPTED")))/g' test/server_unified_rpc_test.go
go test ./test -run '^Test$/^ServerUnifiedRPCProcessing$' -v -count=1
```

Output — every subtest still green:

```console
=== RUN   Test/ServerUnifiedRPCProcessing
=== RUN   Test/ServerUnifiedRPCProcessing/FalseFlags
=== RUN   Test/ServerUnifiedRPCProcessing/ClientStream
=== RUN   Test/ServerUnifiedRPCProcessing/ServerStream
=== RUN   Test/ServerUnifiedRPCProcessing/BidiStream
--- PASS: Test (0.01s)
PASS
ok   google.golang.org/grpc/test 0.011s
```

The server-stream and bidi subtests only count received messages (`stream.RecvMsg(new(wrapperspb.BytesValue))`) and never compare contents, so arbitrarily corrupted stream responses pass. (The file's unary and client-stream subtests do assert concrete values.)

Impact: the streaming half of the branch's regression coverage cannot catch payload corruption introduced by the unified pipeline — the very data path the refactor rewires.

## C6

Claim: a failed unary outbound send with an empty response header omits the binary-log `ServerHeader` event.

Probe (`verify/repro/c6_binlog_serverheader_test.go`, temporarily placed in `binarylog/`): server responds with 1024 bytes against `grpc.MaxSendMsgSize(16)`, no header set.

```sh
go test ./binarylog -run '^TestC6UnaryFailedSendServerHeader$' -v -count=1
```

Audited implementation:

```console
server binlog entry 0: EVENT_TYPE_CLIENT_HEADER
server binlog entry 1: EVENT_TYPE_CLIENT_MESSAGE
server binlog entry 2: EVENT_TYPE_SERVER_TRAILER
RESULT: ServerHeader event OMITTED for failed unary send with empty header
```

Base commit `0c51461d27177d997e14c642fe18c11668fc09a3` (same probe):

```console
server binlog entry 0: EVENT_TYPE_CLIENT_HEADER
server binlog entry 1: EVENT_TYPE_CLIENT_MESSAGE
server binlog entry 2: EVENT_TYPE_SERVER_HEADER
server binlog entry 3: EVENT_TYPE_SERVER_TRAILER
RESULT: ServerHeader event WAS logged for failed unary send with empty header
```

Root cause in the audited `server.go` error path — the header is only logged when nonempty:

```go
if !ss.serverHeaderBinlogged {
	if h, _ := ss.s.Header(); h.Len() > 0 {
		sh := &binarylog.ServerHeader{Header: h}
		...
	}
}
```

Impact: binary-log consumers (e.g. observability pipelines reconstructing RPC timelines) see a different unary event sequence than upstream gRPC produces for the same wire behavior; tooling that requires ClientHeader→ServerHeader→ServerTrailer ordering breaks on failed unary sends.

## C7

Claim: the provided lifecycle tests do not assert RPC-shape-specific `ServerHeader` binary-log behavior on failed sends.

Part A — unary omission: the audited implementation demonstrably omits the empty unary `ServerHeader` (see C6), yet the full provided lifecycle group passes:

```sh
bash ./test/run_eval_server_test_group.sh lifecycle; echo "EXIT=$?"
```

```console
==> final status-write failure accounting
ok  	google.golang.org/grpc/test	0.005s
==> unary and streaming codec error compatibility
ok  	google.golang.org/grpc/encoding	0.006s
==> terminal metadata, malformed receive, channelz, and tracing
ok  	google.golang.org/grpc/test	0.335s
==> stats for unary and streaming success and error paths
ok  	google.golang.org/grpc/stats	0.014s
==> binary logs for unary and streaming success and error paths
ok  	google.golang.org/grpc/binarylog	0.011s
==> gcp observability binary logs
EXIT=0
```

Part B — streaming synthetic header: mutated `serverStream.SendMsg`'s error path to log a synthetic empty `ServerHeader` on streaming send failure (see `verify/repro/c7_lifecycle_serverheader_mutation.sh`). Sanity probe confirmed the mutation fires:

```console
server binlog entry 2: EVENT_TYPE_SERVER_HEADER
RESULT: synthetic ServerHeader logged on streaming send failure
```

Lifecycle group with the mutation in place:

```console
==> binary logs for unary and streaming success and error paths
ok  	google.golang.org/grpc/binarylog	0.013s
==> gcp observability binary logs
EXIT=0
```

Both alterations complete the provided lifecycle tests successfully — no assertion distinguishes the altered event sequences. CONFIRMED on both parts.

Impact: the eval's lifecycle gate cannot catch RPC-shape-specific binary-log regressions on failed sends, so the C6 defect (and its streaming dual) pass grading undetected.

## C8

Claim: the provided checks do not require preservation of the distinct encoding-stage and compression-stage server diagnostics.

Part A — encoding diagnostic removed on the audited branch:

```sh
sed -i 's#channelz.Error(logger, ss.channelz, "grpc: server failed to encode response: ", err)#_ = err#' stream.go
bash ./test/run_eval_server_test_group.sh compatibility && bash ./test/run_eval_server_test_group.sh lifecycle && \
  go test ./encoding -run '^TestEval_' -count=1 && go test ./test -run '^TestEval_' -count=1 && go test . -run '^TestEval_' -count=1; echo "EXIT=$?"
```

```console
==> server API and shutdown behavior
ok  	google.golang.org/grpc	0.513s
==> interceptor chains and repeated unary sends
ok  	google.golang.org/grpc/test	0.078s
... (all lifecycle groups ok) ...
ok  	google.golang.org/grpc/encoding	0.009s
ok  	google.golang.org/grpc/test	0.008s
ok  	google.golang.org/grpc	0.004s
EXIT=0
```

Part B — compression diagnostic removed (encoding diagnostic restored first): identical command set, identical all-green result, `EXIT=0`.

Corroboration on the claim's target branch [evalon/grpc-go-se-943e171b]: that branch removed both `server failed to encode/compress response` diagnostics entirely (`grep -rn 'server failed to' --include='*.go' .` over non-test files returns nothing). Its lifecycle group does fail, but only on unrelated wording assertions, not on the missing diagnostics:

```console
eval_codec_error_compatibility_test.go:55: status message = "grpc: failed to unmarshal the received message: eval decode failure", want "grpc: error unmarshalling request" and codec error
eval_codec_error_compatibility_test.go:96: status message = "trying to send message larger than max (1030 vs. 16)", want unary max-send prefix
```

No provided check fails because a stage-specific server diagnostic is absent. CONFIRMED on both parts.

Impact: the grading harness cannot distinguish an implementation that preserves the historical channelz diagnostics from one that silently drops them, so diagnostic regressions ship green.

## C9

Claim: the provided checks do not detect weakening `TestServerRPCEventsLogAll` from an explicit empty byte payload to `payload{}`.

The target branch [evalon/grpc-go-se-4c4409b0] contains exactly that weakening:

```diff
-			Payload: payload{
-				Message: []uint8{},
-			},
+			Payload:     payload{},
```

The weakened assertion passes and the provided checks accept it:

```sh
(cd gcp/observability && go test . -run '^Test$/^ServerRPCEventsLogAll$' -count=1 -v)
bash ./test/run_candidate_tests.sh
```

```console
--- PASS: Test/ServerRPCEventsLogAll (0.00s)
ok  	google.golang.org/grpc/gcp/observability	0.009s
==> Running s.TestServerRPCEventsLogAll in module .../gcp/observability for package .
[PASS] s.TestServerRPCEventsLogAll
CANDIDATE_EXIT=0
```

The lifecycle group's required submodule event also passes:

```console
{"Action":"pass","Package":"google.golang.org/grpc/gcp/observability","Test":"Test/ServerRPCEventsLogAll"}
```

Restoring the original explicit empty byte-payload assertion on the same branch fails — proving the branch's actual logged payload representation was altered and `payload{}` masks it:

```console
logging_test.go:454: error in logging entry list comparison got unexpected grpcLogEntry list, diff (-got, +want): ...
FAIL	google.golang.org/grpc/gcp/observability	0.009s
```

(The branch's lifecycle group also fails, but only on the unrelated codec-message-wording assertions quoted under C8, not because of the weakened payload expectation.)

Impact: the checks re-run the candidate-modified test itself, so weakening a repository assertion converts a real observability regression (empty message payload no longer logged as an explicit empty byte payload) into a green result.
