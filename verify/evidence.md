## Setup

Audit run `v-6750f0f3`. Main checkout `~/repos/grpc-go` is on
`verify/grpc-go-server-unify-unary-stream-rpc-v-6750f0f3`, created from
`origin/grpc-go-server-unify-unary-stream-rpc-perfect` (HEAD `e363acbc`). No
production file on that branch was modified; everything in this audit lives
under `verify/`.

The claim-target branches live in a different repository, added as remote
`evalrepo` and checked out as detached worktrees:

```
git remote add evalrepo https://github.com/kaitranntt-evals/grpc-go-server-unify-unary-stream-rpc.git
git fetch evalrepo evalon/grpc-go-se-e9ae7b4d evalon/grpc-go-se-d61f8cad evalon/grpc-go-se-0e6bd0f2 evalon/grpc-go-se-295197d7 evalon/grpc-go-se-2c2de906 evalon/grpc-go-se-e2d06da9 evalon/grpc-go-se-093900cf evalon/grpc-go-se-307e9c8d evalon/grpc-go-se-b4918163
for b in ...; do git worktree add --detach ~/wt/$b evalrepo/evalon/grpc-go-se-$b; done
```

```
/home/ubuntu/wt/093900cf    435ad85b (detached HEAD)
/home/ubuntu/wt/0e6bd0f2    fca591a0 (detached HEAD)
/home/ubuntu/wt/295197d7    f9c5b6b5 (detached HEAD)
/home/ubuntu/wt/2c2de906    9fa3db9a (detached HEAD)
/home/ubuntu/wt/307e9c8d    7995f02e (detached HEAD)
/home/ubuntu/wt/b4918163    61f70083 (detached HEAD)
/home/ubuntu/wt/d61f8cad    db9a4e2a (detached HEAD)
/home/ubuntu/wt/e2d06da9    d5469cca (detached HEAD)
/home/ubuntu/wt/e9ae7b4d    117e981e (detached HEAD)
```

Every instrumentation / mutation shown below was applied to a worktree only
for the duration of one `go test` run and then reverted (`git checkout -- .`);
the exact patches and helper tests are preserved under `verify/repro/` and
the `run.sh` scripts there replay every observation from a fresh worktree.
Observability of "server never stopped" uses channelz: `grpc.NewServer`
registers a channelz server entry and only `Server.Stop`/`GracefulStop`
removes it, so a lingering entry after the fixture returns is a server that was
never stopped (the `test/` package turns channelz on in `init`; for the root
package the helper test calls `channelz.TurnOn()`).

## C1

Claim: at least one added or substantively changed Go server test uses polling
for correctness ordering or leaves a created resource without cleanup on an
execution path. Each target branch below is adjudicated on its own.

Replay: `bash verify/repro/c1/run.sh [suffix ...]` (from the repo root, with the
`evalrepo` remote present).

### C1 — evalon/grpc-go-se-e9ae7b4d — CONFIRMED (polling)

`test/server_pipeline_test.go` (added by the branch) waits for the server-side
`stats.End` event with a busy poll loop:

```go
// test/server_pipeline_test.go:122-136
func (r *serverCallRecorder) waitForEnd(ctx context.Context) error {
	for {
		r.mu.Lock()
		n := len(r.ends)
		r.mu.Unlock()
		if n > 0 {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Millisecond):
		}
	}
}
```

It is used for correctness ordering: `verify` (line 141) and
`TestServerPipeline_StreamingError` (line 678) call it before asserting on the
recorded `stats.End`, because "the server emits stats.End after writing the
status, so a client may observe the RPC's completion slightly earlier" (comment
at lines 119-121).

Dynamic demonstration: `verify/repro/c1/e9ae7b4d_instrument.patch` adds a poll
counter that is printed when the loop exits and delays `HandleRPC` for the
`*stats.End` event by 20 ms (so the ordering gap the loop papers over is
visible). Test still passes; the loop spins ~20 times per RPC.

```
$ cd ~/wt/e9ae7b4d && git apply ~/repos/grpc-go/verify/repro/c1/e9ae7b4d_instrument.patch
$ go test ./test -run 'Test/ServerPipeline_RPCTypes' -count=1 -v 2>&1 | grep -E 'AUDIT|^ok|^FAIL'
AUDIT waitForEnd: returned after 20 poll iteration(s)
AUDIT waitForEnd: returned after 21 poll iteration(s)
AUDIT waitForEnd: returned after 20 poll iteration(s)
AUDIT waitForEnd: returned after 21 poll iteration(s)
ok  	google.golang.org/grpc/test	0.097s
```

Impact: the test's correctness depends on a 1 ms poll racing a server-side
callback instead of a synchronisation primitive (channel / `sync.Cond`), so it
is timing-dependent (slow CI → longer spin; a very slow `HandleRPC` → flaky
`ctx` timeout) and burns CPU while waiting. This is exactly the anti-pattern
the claim describes.

### C1 — evalon/grpc-go-se-d61f8cad — CONFIRMED (no cleanup on failure path)

`test/server_stream_pipeline_test.go` (added) builds the `grpc.Server` first
and registers the cleanup only after a fallible `Start`:

```go
// test/server_stream_pipeline_test.go:171-180
ss.S = grpc.NewServer(
	grpc.UnaryInterceptor(rec.unaryInterceptor),
	grpc.StreamInterceptor(rec.streamInterceptor),
	grpc.UnknownServiceHandler(echoEmptyStreamHandler),
)
ss.S.RegisterService(pipelineTestServiceDesc(), nil)
if err := ss.Start(nil); err != nil {
	t.Fatalf("Error starting stub server: %v", err)
}
t.Cleanup(ss.Stop)
```

`stubserver.StubServer.Start` returns the `net.Listen` error without stopping a
caller-provided `ss.S` (`internal/stubserver/stubserver.go`: `StartServer`
fails before `ss.S.Serve`; `Stop` is only invoked by `Start` when `StartClient`
fails). Forced the failure path with a one-line patch
(`verify/repro/c1/d61f8cad_instrument.patch`, `ss.Address =
"256.256.256.256:1"`) and ran the fixture from a helper test
(`verify/repro/c1/d61f8cad_audit_test.go.txt`) that counts channelz servers
before/after:

```
$ cd ~/wt/d61f8cad && git apply .../d61f8cad_instrument.patch && cp .../d61f8cad_audit_test.go.txt test/audit_c1_test.go
$ go test ./test -run 'Test/AuditC1' -count=1 -v
    audit_c1_test.go:14: channelz servers before: 0
    audit_c1_test.go:20: Error starting stub server: net.Listen("tcp", "256.256.256.256:1") = listen tcp: lookup 256.256.256.256: no such host
    audit_c1_test.go:22: subtest passed=false (expected false: startup was forced to fail)
    audit_c1_test.go:25: channelz servers after: 1
    audit_c1_test.go:28: lingering channelz server entry: id=1 ref=""
    audit_c1_test.go:30: grpc.Server created by the fixture was never stopped: 1 channelz server entr(ies) leaked
--- FAIL: Test (0.00s)
FAIL	google.golang.org/grpc/test	0.008s
```

Impact: on the `Start` failure path the `grpc.Server` (its channelz entry,
service map, interceptor closures) is never stopped. In the shared `test`
package this leaks channelz state into later tests (channelz-based tests count
server entries) and is the "created resource without cleanup on an execution
path" pattern the claim describes.

### C1 — evalon/grpc-go-se-0e6bd0f2 — CONFIRMED (no cleanup on every path)

`server_test.go` `TestGetServiceInfo_MethodGrouping` (added, lines 137+)
creates a server and never stops it on any path:

```go
// server_test.go:154-155
server := NewServer()
server.RegisterService(&testSd, &testServer{})
// ... GetServiceInfo assertions; no server.Stop()/GracefulStop()/defer/Cleanup
```

(Contrast: the neighbouring pre-existing `TestGetServiceInfo` at line 78 calls
`server.GracefulStop()`.) Helper test
`verify/repro/c1/0e6bd0f2_audit_test.go.txt` runs the branch's test as a
subtest with channelz on:

```
$ cd ~/wt/0e6bd0f2 && cp .../0e6bd0f2_audit_test.go.txt audit_c1_test.go
$ go test . -run 'Test/AuditC1' -count=1 -v
    audit_c1_test.go:14: channelz servers before: 0
    audit_c1_test.go:19: subtest passed=true
    audit_c1_test.go:22: channelz servers after: 1
    audit_c1_test.go:25: lingering channelz server entry: id=1 ref=""
    audit_c1_test.go:27: server created by TestGetServiceInfo_MethodGrouping was never stopped: 1 channelz server entr(ies) leaked
--- FAIL: Test (0.00s)
FAIL	google.golang.org/grpc	0.005s
```

Impact: the added test leaks a `grpc.Server` (channelz registration, service
map) on the *success* path — the unconditional, strongest form of the claimed
problem.

### C1 — evalon/grpc-go-se-295197d7 — CONFIRMED (no cleanup on failure path, ×14)

`server_pipeline_ext_test.go` `TestServer_StreamPipeline` (added) is
table-driven; each case creates its own server before `Start` and defers `Stop`
only after `Start` succeeds:

```go
// server_pipeline_ext_test.go:223-229
srv := grpc.NewServer(grpc.UnaryInterceptor(unaryInt), grpc.StreamInterceptor(streamInt), grpc.StatsHandler(h), grpc.MaxRecvMsgSize(1024), grpc.UnknownServiceHandler(streamHandler(true, true)))
srv.RegisterService(desc, nil)
ss := &stubserver.StubServer{S: srv}
if err := ss.Start(nil); err != nil {
	t.Fatalf("Start() failed: %v", err)
}
defer ss.Stop()
```

Forced `net.Listen` to fail (`verify/repro/c1/295197d7_instrument.patch`:
`Address: "256.256.256.256:1"`) and ran the test under the channelz-counting
helper (`verify/repro/c1/295197d7_audit_test.go.txt`):

```
$ cd ~/wt/295197d7 && git apply .../295197d7_instrument.patch && cp .../295197d7_audit_test.go.txt audit_c1_test.go
$ go test . -run 'Test/AuditC1' -count=1 -v
    audit_c1_test.go:15: channelz servers before: 0
    audit_c1_test.go:20: subtest passed=false (expected false: startup was forced to fail)
    audit_c1_test.go:23: channelz servers after: 14
    audit_c1_test.go:26: lingering channelz server entry: id=1 ref=""
    ... (ids 2..13) ...
    audit_c1_test.go:26: lingering channelz server entry: id=14 ref=""
    audit_c1_test.go:28: grpc.Server(s) created before ss.Start were never stopped: 14 channelz server entr(ies) leaked
--- FAIL: Test (0.00s)
FAIL	google.golang.org/grpc	0.007s
```

Impact: one unstopped `grpc.Server` per table case (14) whenever listening
fails — every server object, its stats handler and channelz entry outlive the
test.

### C1 — evalon/grpc-go-se-2c2de906 — CONFIRMED (no cleanup on failure path)

`test/server_stream_pipeline_test.go` (added):

```go
// test/server_stream_pipeline_test.go:150,181-185
server := grpc.NewServer( ... )
server.RegisterService(desc, nil)
ss := &stubserver.StubServer{S: server}
if err := ss.Start(nil); err != nil {
	t.Fatal(err)
}
defer ss.Stop()
```

```
$ cd ~/wt/2c2de906 && git apply .../2c2de906_instrument.patch && cp .../2c2de906_audit_test.go.txt test/audit_c1_test.go
$ go test ./test -run 'Test/AuditC1' -count=1 -v
    audit_c1_test.go:14: channelz servers before: 0
    server_stream_pipeline_test.go:183: net.Listen("tcp", "256.256.256.256:1") = listen tcp: lookup 256.256.256.256: no such host
    audit_c1_test.go:19: subtest passed=false (expected false: startup was forced to fail)
    audit_c1_test.go:22: channelz servers after: 1
    audit_c1_test.go:25: lingering channelz server entry: id=1 ref=""
    audit_c1_test.go:27: grpc.Server created before ss.Start was never stopped: 1 channelz server entr(ies) leaked
--- FAIL: Test (0.00s)
FAIL	google.golang.org/grpc/test	0.008s
```

Impact: same failure-path leak as d61f8cad/295197d7 — the pre-built server is
abandoned when `Start` fails, polluting channelz for the rest of the `test`
package.

### C1 — evalon/grpc-go-se-e2d06da9 — CONFIRMED (no cleanup on failure path)

`server_pipeline_test.go` (added, root package):

```go
// server_pipeline_test.go:263,279-283
server := grpc.NewServer( ... )
server.RegisterService(desc, nil)
ss := &stubserver.StubServer{S: server}
if err := ss.Start(nil); err != nil {
	t.Fatal(err)
}
defer ss.Stop()
```

```
$ cd ~/wt/e2d06da9 && git apply .../e2d06da9_instrument.patch && cp .../e2d06da9_audit_test.go.txt audit_c1_test.go
$ go test . -run 'Test/AuditC1' -count=1 -v
    audit_c1_test.go:15: channelz servers before: 0
    server_pipeline_test.go:281: net.Listen("tcp", "256.256.256.256:1") = listen tcp: lookup 256.256.256.256: no such host
    audit_c1_test.go:20: subtest passed=false (expected false: startup was forced to fail)
    audit_c1_test.go:23: channelz servers after: 1
    audit_c1_test.go:26: lingering channelz server entry: id=1 ref=""
    audit_c1_test.go:28: grpc.Server created before ss.Start was never stopped: 1 channelz server entr(ies) leaked
--- FAIL: Test (0.00s)
FAIL	google.golang.org/grpc	0.006s
```

Impact: identical failure-path leak; the created `grpc.Server` is never
stopped when listening fails.

## C2

Claim: a new or substantively changed stream-result assertion does not
distinguish the intended receive / terminal-status outcome from an
incompatible outcome. Each branch adjudicated independently.

Replay: `bash verify/repro/c2/run.sh [093900cf|307e9c8d]`.

### C2 — evalon/grpc-go-se-093900cf — CONFIRMED

`test/server_pipeline_test.go` `TestServerPipeline_UnaryRespondsWithoutHalfClose`
(added) ends with a terminal-status assertion that inspects the wrong variable:

```go
// test/server_pipeline_test.go:376-395
stream, err := ss.CC.NewStream(ctx, desc, "/grpc.testing.TestService/UnaryCall")
if err != nil {
	t.Fatalf("NewStream() failed: %v", err)
}
...
if err := stream.RecvMsg(resp); err != io.EOF {          // shadowed err
	t.Fatalf("second stream.RecvMsg() = %v, want io.EOF", err)
}
if st := status.Code(err); st != codes.OK {              // outer err == NewStream() error, always nil here
	t.Fatalf("stream status = %v, want %v", st, codes.OK)
}
```

The `stream status` assertion reads the outer `err` from `NewStream`, which is
already known to be `nil` (line 377-379 would have `Fatalf`'d otherwise), so
`status.Code(err)` is `codes.OK` no matter how the RPC terminated. Two runs
show this:

1. Handler-only mutation (server returns `FailedPrecondition`): the test fails,
   but at the *earlier* `RecvMsg` check (line 385), never at the status
   assertion:

```
$ cd ~/wt/093900cf && sed -i 's|return &testpb.SimpleResponse{Payload: &testpb.Payload{Body: \[\]byte("pong")}}, nil|return nil, status.Error(codes.FailedPrecondition, "audit: non-OK terminal status")|' test/server_pipeline_test.go
$ go test ./test -run 'Test/ServerPipeline_UnaryRespondsWithoutHalfClose' -count=1 -v
    server_pipeline_test.go:385: stream.RecvMsg() = rpc error: code = FailedPrecondition desc = audit: non-OK terminal status, want <nil>
--- FAIL: Test (0.01s)
FAIL	google.golang.org/grpc/test	0.012s
```

2. Same handler mutation with the status assertion isolated (the preceding
   `RecvMsg` checks replaced by a log of the actual result —
   `verify/repro/c2/093900cf_mutation.patch`): the RPC terminates with
   `FailedPrecondition`, the "stream status ... want OK" assertion still
   passes:

```
$ cd ~/wt/093900cf && git apply ~/repos/grpc-go/verify/repro/c2/093900cf_mutation.patch
$ go test ./test -run 'Test/ServerPipeline_UnaryRespondsWithoutHalfClose' -count=1 -v
    server_pipeline_test.go:386: AUDIT: RecvMsg() = rpc error: code = FailedPrecondition desc = audit: non-OK terminal status (status code FailedPrecondition); NewStream() err = <nil>
--- PASS: Test (0.01s)
ok  	google.golang.org/grpc/test	0.012s
```

Impact: the assertion that is labelled as the terminal-status check
(`stream status = %v, want OK`) cannot distinguish OK from any non-OK terminal
status — it is dead code asserting on `NewStream`'s error. The test currently
detects a non-OK status only incidentally via the `!= io.EOF` check two lines
above; any edit to those lines (e.g. relaxing the EOF check) silently removes
all terminal-status coverage while the misleading assertion keeps reading as if
it were still present.

### C2 — evalon/grpc-go-se-307e9c8d — CONFIRMED

`test/server_rpc_pipeline_test.go` `TestServerRPCPipeline_StreamingFailures`,
subtest `receive failure` (added). Server side and client assertion:

```go
// test/server_rpc_pipeline_test.go:413-419
// Receiving into a message of the wrong type makes the server's
// receive path fail; that failure must be surfaced to the client
// with an Internal status.
if err := stream.RecvMsg(&testpb.StreamingOutputCallRequest{}); err != nil {
	return err
}
return status.Error(codes.Internal, "expected RecvMsg to fail")

// test/server_rpc_pipeline_test.go:476-479
_, err = stream.CloseAndRecv()
if status.Code(err) != codes.Internal {
	t.Errorf("stream.CloseAndRecv() = %v, want code %v", err, codes.Internal)
}
```

Both branches of the handler produce `codes.Internal` (the real receive-failure
path surfaces `Internal`; the fallback explicitly returns `Internal`), so the
client-side assertion cannot tell "receive failed as intended" from "receive
succeeded". Two runs, instrumented with `t.Logf` only
(`verify/repro/c2/307e9c8d_mutation.patch` minus the type swap) and then with
the receive type changed to the correct `StreamingInputCallRequest` so the
receive must succeed:

```
$ cd ~/wt/307e9c8d   # branch code, logging only
$ go test ./test -run 'Test/ServerRPCPipeline_StreamingFailures/receive_failure' -count=1 -v
    server_rpc_pipeline_test.go:420: AUDIT: server RecvMsg SUCCEEDED; returning fallback Internal status
    server_rpc_pipeline_test.go:479: AUDIT: client CloseAndRecv() = rpc error: code = Internal desc = expected RecvMsg to fail
--- PASS: Test (0.01s)
ok  	google.golang.org/grpc/test	0.013s

$ git apply ~/repos/grpc-go/verify/repro/c2/307e9c8d_mutation.patch   # RecvMsg(&testpb.StreamingInputCallRequest{})
$ go test ./test -run 'Test/ServerRPCPipeline_StreamingFailures/receive_failure' -count=1 -v
    server_rpc_pipeline_test.go:420: AUDIT: server RecvMsg SUCCEEDED; returning fallback Internal status
    server_rpc_pipeline_test.go:479: AUDIT: client CloseAndRecv() = rpc error: code = Internal desc = expected RecvMsg to fail
--- PASS: Test (0.01s)
ok  	google.golang.org/grpc/test	0.012s
```

Impact: as written on the branch the "receive failure" case never exercises a
receive failure at all — protobuf decoding of a `StreamingInputCallRequest`
payload into `StreamingOutputCallRequest` succeeds (mismatched fields become
unknown fields), the fallback `Internal` status is what the client sees, and
the assertion is green. The test therefore provides zero coverage of the
server's receive-error → `Internal` conversion it claims to verify, and it
would keep passing if that conversion were broken.

## C3

Claim: in `server.go`, `processRPC` retains a defer-rationale cross-reference to
`processUnaryRPC` that no longer resolves to an existing explanation of stack
usage and tracing/stats/channelz cleanup order. Target:
evalon/grpc-go-se-b4918163.

Replay: `bash verify/repro/c3/run.sh` (exit 1 == stale reference confirmed).

```
$ cd ~/repos/grpc-go && bash verify/repro/c3/run.sh
--- cross-reference in processRPC on evalon/grpc-go-se-b4918163:
evalrepo/evalon/grpc-go-se-b4918163:server.go:1319:		// See comment in processUnaryRPC on defers.
--- func processUnaryRPC definitions on evalon/grpc-go-se-b4918163 (expect none):
(none)
--- defer-rationale text on evalon/grpc-go-se-b4918163 (expect none):
(none)
--- the same rationale at base 0c51461d27177d997e14c642fe18c11668fc09a3 (what the reference used to point at):
0c51461d27177d997e14c642fe18c11668fc09a3:server.go:1279:		// combined into one function to reduce stack usage -- a defer takes ~56-64
0c51461d27177d997e14c642fe18c11668fc09a3:server.go:1284:		// should be executed in the reverse order. That is, tracing first, stats
RESULT: stale processUnaryRPC defer-rationale reference CONFIRMED
```

Source on the branch (`~/wt/b4918163/server.go:1318-1320`):

```go
if sh != nil || trInfo != nil || channelz.IsOn() {
	// See comment in processUnaryRPC on defers.
	defer func() {
```

The referenced explanation, present at the base commit and deleted along with
`processUnaryRPC` on this branch:

```go
// base 0c51461d server.go:1278-1285 (inside processUnaryRPC)
// The deferred error handling for tracing, stats handler and channelz are
// combined into one function to reduce stack usage -- a defer takes ~56-64
// bytes on the stack, so overflowing the stack will require a stack
// re-allocation, which is expensive.
//
// To maintain behavior similar to separate deferred statements, statements
// should be executed in the reverse order. That is, tracing first, stats
// handler second, and channelz last.
```

Impact: the only remaining rationale for (a) folding three defers into one and
(b) the mandatory tracing → stats → channelz order is a pointer to a function
that no longer exists; a maintainer following the comment finds nothing, and
the ordering constraint is now undocumented in the unified pipeline.
