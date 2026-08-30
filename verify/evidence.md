# Audit evidence — run v-fa296f26

Observation-only log. Each section is self-contained: exact commands and verbatim key output lines. Conclusions live in verify/verdicts.md (uncommitted).

Worktrees: each claim branch was checked out from remote `evalrepo` (https://github.com/kaitranntt-evals/grpc-go-server-unify-unary-stream-rpc) into an isolated worktree `~/repos/wt-<suffix>`:

```sh
cd ~/repos/grpc-go
git remote add evalrepo https://github.com/kaitranntt-evals/grpc-go-server-unify-unary-stream-rpc
git fetch evalrepo
git worktree add ~/repos/wt-<suffix> evalrepo/evalon/grpc-go-se-<suffix>
```

## C1

Established baseline assertion in `gcp/observability/logging_test.go` (branch `grpc-go-server-unify-unary-stream-rpc-perfect`) for the server trailer entry of an empty-response RPC:

```go
Payload: payload{
    Message: []uint8{},
},
```

The comparison uses `cmp.Diff` and ignores only nondeterministic fields (`CallID`, `Peer`, `IPPort`, `Type`, `Timeout`), so `Message: []uint8{}` is a real assertion distinguishing a non-nil empty slice from nil.

Branch diffs (`git diff origin/grpc-go-server-unify-unary-stream-rpc-perfect...HEAD -- gcp/observability/logging_test.go` in each worktree):

- Branches `105181d0`, `9d6b90e7`, `e2fa5dad`, `a291a4cc` replace the block with `Payload: payload{},` (i.e. `Message: nil`).
- Branch `538c6009` replaces `Message: []uint8{},` with `Message: nil,`.

As-changed focused run on each branch (representative, wt-105181d0):

```sh
cd ~/repos/wt-105181d0/gcp/observability
go test -v -run 'Test/ServerRPCEventsLogAll' -count=1 .
```

```text
ok   google.golang.org/grpc/gcp/observability 0.010s
```

Restoring the original assertion on each branch (for `538c6009`: `sed -i '435s/Message: nil,/Message: []uint8{},/' logging_test.go`; for the other four: reinstating `Message: []uint8{}` inside the empty `payload{}`) and re-running the same command fails on all five branches:

```text
--- FAIL: Test (0.00s)
    --- FAIL: Test/ServerRPCEventsLogAll (0.00s)
                   MessageLength: 0,
        -           Message:       nil,
        +           Message:       []uint8{},
FAIL
FAIL
```

So on every branch the production code now emits a nil server-message payload where the established test asserted a non-nil empty slice, and the changed tests were weakened to accept it; no replacement assertion of equal specificity for the payload representation exists on any of the five branches (searched `gcp/observability`, `binarylog`, and `test` packages for `[]uint8{}`/`Message:` assertions on server messages). Impact: binary-log consumers relying on the documented non-nil empty payload observe a representation change that the test suite no longer guards.

## C2

Branch `evalon/grpc-go-se-eedbaf01`, changed test `TestUnifiedServerPipeline` (`test/server_test.go`) asserts only aggregate counters:

```go
if got := unaryInterceptions.Load(); got != 1 { ... }
if got := streamInterceptions.Load(); got != 5 { ... }
```

As-changed focused run:

```sh
cd ~/repos/wt-eedbaf01
go test -v ./test -run 'Test/UnifiedServerPipeline' -count=1
```

```text
--- PASS: Test (0.01s)
    --- PASS: Test/UnifiedServerPipeline (0.00s)
PASS
ok google.golang.org/grpc/test 0.010s
```

Temporary mutation of `server.go` `streamMethod` (see `verify/repro/c2_mutation.sh`): server-streaming RPCs (`ServerStreams && !ClientStreams`) bypass `s.opts.streamInt` entirely, while bidirectional RPCs invoke the stream interceptor three times (nested). Net stream-interceptor count stays 5 (client-streaming 1 + server-streaming 0 + bidi 3 + failing server-streaming 0 + false/false 1 = 5). Re-running the same command with the mutation applied:

```text
--- PASS: Test (0.01s)
    --- PASS: Test/UnifiedServerPipeline (0.00s)
PASS
ok google.golang.org/grpc/test 0.010s
```

The changed test passes with server-streaming RPCs receiving no stream interceptor and bidi RPCs receiving three, so it does not independently verify correct unary-versus-stream interceptor selection per shape. Impact: a category-routing regression for server-streaming (or double-invocation for bidi) ships undetected. Mutation reverted after the run (`git diff` clean).

## C3

Branch `evalon/grpc-go-se-8577a848`, changed test `TestUnifiedServerStreamPipeline` (`server_test.go`) covers one unary method plus streaming methods `ClientStream`, `ServerStream`, `BidiStream`, `DescriptorStream` (both flags false), and `FailingStream`. Its `callStream` helper sends the required messages, closes send, receives the expected responses, and requires final `io.EOF` — i.e. a successful result for each ordinary descriptor shape (unary, client-streaming, server-streaming, bidirectional).

```sh
cd ~/repos/wt-8577a848
go test -v . -run 'Test/UnifiedServerStreamPipeline' -count=1
```

```text
--- PASS: Test (0.00s)
    --- PASS: Test/UnifiedServerStreamPipeline (0.00s)
PASS
ok google.golang.org/grpc 0.007s
```

Successful-result coverage exists for every ordinary shape; no omission found.

## C4

Same branch and test as C3. The test asserts an ordered per-method event list:

```go
wantInterceptors := []string{
    "unary:" + fullMethod("Unary"),
    "stream:" + fullMethod("ClientStream"),
    "stream:" + fullMethod("ServerStream"),
    "stream:" + fullMethod("BidiStream"),
    "stream:" + fullMethod("DescriptorStream"),
    "stream:" + fullMethod("FailingStream"),
}
```

Sensitivity check — temporary mutation of `server.go` routing so server-streaming RPCs skip the stream interceptor:

```go
if !method.isStream || s.opts.streamInt == nil || (method.serverStreams && !method.clientStreams) {
    appErr = method.handler(server, ss)
} else { ... }
```

Re-running the focused test with the mutation:

```text
=== RUN   Test/UnifiedServerStreamPipeline
    server_test.go:403: interceptor calls mismatch (-want +got):
        - "stream:/grpc.testing.UnifiedPipeline/ServerStream",
--- FAIL: Test (0.00s)
    --- FAIL: Test/UnifiedServerStreamPipeline (0.00s)
```

The per-method ordered assertion detects category-routing changes for each ordinary shape, so segregation is proven per shape. Mutation reverted after the run.

## C5

Branch `evalon/grpc-go-se-55a5e139`, worktree `~/repos/wt-55a5e139`. Probe: `verify/repro/c568_audit_test.go` (`TestAuditC5_UnarySecondMessageDecodeWording`) opens a raw client stream to unary method `/audit.C568/EmptyUnary`, sends a valid empty proto message followed by malformed bytes `{0xFF}`, and captures the resulting status.

```sh
cd ~/repos/wt-55a5e139
cp ~/repos/grpc-go/verify/repro/c568_audit_test.go .
go test . -run 'TestAuditC5' -count=1 -v
```

```text
    c568_audit_test.go:208: decode error message: "grpc: failed to unmarshal the received message: proto:\u00a0cannot parse invalid wire-format data"
--- PASS: TestAuditC5_UnarySecondMessageDecodeWording (0.00s)
```

Source basis: `stream.go` `RecvMsg` uses the unary-specific format `"grpc: error unmarshalling request: %v"` only for the first receive; the second `recv` call (extra-message detection for non-client-streaming RPCs) passes no format, so `rpc_util.go` falls back to `"grpc: failed to unmarshal the received message: %v"`. Observed: the malformed second request on a unary method is reported with the streaming-specific wording, not the unary wording. Impact: unary callers and log scrapers keyed to the documented unary decode wording miss these failures.

## C6

Same branch/worktree; probe `TestAuditC6_SendDiagnostics` in `verify/repro/c568_audit_test.go` induces (a) a unary compression failure (registered compressor whose `Write` fails), (b) a streaming encoding failure (`SendMsg(42)`), and (c) a streaming compression failure, capturing all grpclog output.

```sh
cd ~/repos/wt-55a5e139
go test . -run 'TestAuditC6' -count=1 -v
```

```text
    c568_audit_test.go:228: unary compress-fail RPC error: rpc error: code = Internal desc = grpc: error while compressing: auditfailcomp: induced compression failure
    c568_audit_test.go:233: unary compression failure logged as: 'grpc: server failed to encode response: ... induced compression failure'
    c568_audit_test.go:245: streaming encode-fail RPC error: rpc error: code = Internal desc = grpc: error while marshaling: proto: failed to marshal, message is int, want proto.Message
    c568_audit_test.go:251: streaming encode failure produced NO encode/compress diagnostic (delta logs: "")
    c568_audit_test.go:263: streaming compress-fail RPC error: rpc error: code = Internal desc = grpc: error while compressing: auditfailcomp: induced compression failure
    c568_audit_test.go:269: streaming compress failure produced NO encode/compress diagnostic (delta logs: "")
--- PASS: TestAuditC6_SendDiagnostics (0.40s)
```

Observed: a unary compression failure is logged under the encoding-stage label `server failed to encode response` (stage misidentified), and streaming encoding/compression failures emit no server-side stage diagnostic at all (grpclog delta empty, distinguished from mislabeling). Source basis: `stream.go` `SendMsg` logs only `if ss.unary` and always with the encode wording. Impact: operators diagnosing response-preparation failures get the wrong stage for unary and nothing for streaming.

## C7

Branch `evalon/grpc-go-se-5cc804bc`, worktree `~/repos/wt-5cc804bc`. `server.go` `GetServiceInfo` (lines 854-872) builds `MethodInfo` from a single merged `srv.methods` map in map-iteration order; there is no unary-then-streaming grouping.

Probe: `verify/repro/c7_audit_test.go` registers a service with unary `U1..U3` and streaming `S1..S3`, invokes `GetServiceInfo` 1000 times, and counts orderings where a streaming descriptor precedes a unary one.

```sh
cd ~/repos/wt-5cc804bc
cp ~/repos/grpc-go/verify/repro/c7_audit_test.go .
go test . -run TestAuditC7 -count=1 -v
```

```text
    c7_audit_test.go:48: orderings with a streaming descriptor before a unary descriptor: 627/1000
    c7_audit_test.go:50: sample violating order: [{S3 true true} {U1 false false} {U2 false false} {U3 false false} {S1 false true} {S2 true false}]
--- PASS: TestAuditC7_GetServiceInfoOrdering (0.00s)
```

Observed: unary descriptors do not necessarily precede streaming descriptors; ordering is randomized per invocation. Impact: consumers that relied on the historical unary-before-streaming grouping (produced by iterating `md` then `sd` slices) see nondeterministic ordering.

## C8

Same branch/worktree as C5/C6; probe `TestAuditC8_EmptyUnaryBinlogPayload` installs a capturing binarylog logger and invokes unary `/audit.C568/EmptyUnary` (empty proto response).

```sh
cd ~/repos/wt-55a5e139
go test . -run 'TestAuditC8' -count=1 -v
```

```text
    c568_audit_test.go:294: binarylog ServerMessage payload: []uint8, isBytes=true, nil=true, len=0
--- PASS: TestAuditC8_EmptyUnaryBinlogPayload (0.00s)
```

Observed: the binary-log `ServerMessage.Message` for an empty unary response is a nil `[]byte`, not a non-nil zero-length slice. Source basis: `stream.go` builds `binarylog.ServerMessage{Message: data.Materialize()}` and `mem.BufferSlice.Materialize()` returns `nil` when the slice length is 0. This is the production behavior underlying the C1 test weakenings. Impact: consumers distinguishing "empty message logged" (non-nil empty) from "no payload" (nil) can no longer tell them apart.

## C9

Branch `evalon/grpc-go-se-8b49f661`, worktree `~/repos/wt-8b49f661`. `stream.go` `SendMsg` classifies preparation failures by human-readable status text:

```go
switch msg := status.Convert(err).Message(); {
case strings.HasPrefix(msg, "grpc: error while marshaling:"):
    channelz.Error(logger, ss.server.channelz, "grpc: server failed to encode response: ", err)
case strings.HasPrefix(msg, "grpc: error while compressing:"):
    channelz.Error(logger, ss.server.channelz, "grpc: server failed to compress response: ", err)
}
```

Probe: `verify/repro/c9_audit_test.go` induces three unary response-preparation failures inside `prepareMsg` — a codec marshal error, a compressor failure, and an oversize response (`make([]byte, 1<<32)`, hitting `encode`'s `"grpc: message too large"` path, i.e. identical provenance to the marshal case but different text) — capturing grpclog deltas per call.

```sh
cd ~/repos/wt-8b49f661
cp ~/repos/grpc-go/verify/repro/c9_audit_test.go .
go test . -run TestAuditC9 -count=1 -v
```

```text
    c9_audit_test.go:147: marshal-fail: rpc error = rpc error: code = Internal desc = grpc: error while marshaling: c9: induced marshal failure
    c9_audit_test.go:149: ... server log delta contains "grpc: server failed to encode response: ..."
    c9_audit_test.go:147: compress-fail: rpc error = rpc error: code = Internal desc = grpc: error while compressing: c9: induced compression failure
    c9_audit_test.go:149: compress-fail: server log delta = "[core] [Server #1] grpc: server failed to compress response: rpc error: code = Internal desc = grpc: error while compressing: c9: induced compression failure\n"
    c9_audit_test.go:147: oversize: rpc error = rpc error: code = ResourceExhausted desc = grpc: message too large (4294967296 bytes)
    c9_audit_test.go:149: oversize: server log delta = ""
    c9_audit_test.go:163: CONCLUSION: classification tracks the human-readable status text prefix; the equally real preparation failure with different text (oversize) produced no stage diagnostic
--- PASS: TestAuditC9_SendMsgClassification (0.91s)
```

Observed: classification tracks status-message text prefixes, not structured error provenance — a preparation failure from the same `prepareMsg`/`encode` code path whose text does not match either prefix is silently unclassified. Impact: diagnostics depend on error wording; any rewording or non-matching preparation error loses (or could miscategorize) its stage label.
