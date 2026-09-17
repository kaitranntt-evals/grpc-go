## Setup

Run ID `v-e316f72f`. Work branch `verify/grpc-go-xds-certificate-provider-closure-race-v-e316f72f` created from `origin/grpc-go-xds-certificate-provider-closure-race-perfect` (HEAD `4f781286`). Claim-target branches were fetched from the repository named in each claim URL and checked out into separate worktrees:

```sh
cd ~/repos/grpc-go
git remote add evals https://github.com/kaitranntt-evals/grpc-go-xds-certificate-provider-closure-race.git
git fetch evals evalon/grpc-go-xd-7c3674ad evalon/grpc-go-xd-c46d6b0a
git worktree add /home/ubuntu/wt/c1 evals/evalon/grpc-go-xd-7c3674ad   # HEAD c6a0eac0
git worktree add /home/ubuntu/wt/c2 evals/evalon/grpc-go-xd-c46d6b0a   # HEAD 502ade5f
go version   # go1.25.7 linux/amd64
```

Both target branches are a single commit (`chore: apply eval changes`) on top of the base `cc234554`.

## C1

Target: `evalon/grpc-go-xd-7c3674ad` (`c6a0eac0`). Naming drift: the branch has no `concurrent_handshake_test.go`, `replacementApplied`, or `releaseA()`; the overlap test is `TestSecurityConfigUpdate_WhileHandshakeLoadingRoots` in `internal/xds/balancer/clusterimpl/tests/clusterimpl_security_test.go:972`. (`replacementApplied`/post-`UpdateState` callback/`releaseA()` are the eval fixture's names for the same synchronization points.)

### What the test awaits

```console
$ grep -n "ev.built.Fire()\|replacementEvents.built\|initialEvents.closed.HasFired\|close(initialEvents.unblock)" \
    internal/xds/balancer/clusterimpl/tests/clusterimpl_security_test.go
873:			ev.built.Fire()                       # inside the provider Build func, before it returns
1046:	case <-replacementEvents.built.Done():   # the only event awaited after the replacement Update
1053:	if initialEvents.closed.HasFired() {
1059:	close(initialEvents.unblock)             # releases the blocked KeyMaterial() load
```

`ev.built.Fire()` runs inside the `BuildableConfig` build func, which `handleSecurityConfig` calls via `buildProvider` *before* it reaches `b.publishHandshakeInfo(...)` (`internal/xds/balancer/clusterimpl/clusterimpl.go:374`, `publishHandshakeInfo` at `:385` does `xdsHIPtr.Swap(hi)` then `old.Release()`). Nothing in the test waits for the update to be applied (no post-`UpdateState` hook, no `clientConnUpdateHook`, no observation of the swap).

### Baseline: branch test passes as written

```console
$ cd /home/ubuntu/wt/c1 && go test ./internal/xds/balancer/clusterimpl/tests -run '^Test/SecurityConfigUpdate_WhileHandshakeLoadingRoots$' -count=1 -v 2>&1 | tail -4
    --- PASS: Test/SecurityConfigUpdate_WhileHandshakeLoadingRoots (0.02s)
PASS
ok  	google.golang.org/grpc/internal/xds/balancer/clusterimpl/tests	0.032s
```

### Repro 1 (deterministic): `built` does not imply "applied"

`verify/repro/c1_overlap_ordering_test.go` reuses the test's helpers and its exact synchronization (await `built`, then `close(unblock)`), with three observation points that do not change any production code: `buildReturned` fires when the replacement provider's Build func returns (strictly before `publishHandshakeInfo`), `applied` fires from a resolver `ClientConn` wrapper after `UpdateState` returns (the fixture's technique), and the server's TLS creds are wrapped to fire `serverHandshakeDone` when a server-side handshake succeeds. In `TestVerifyC1_BuiltDoesNotImplyApplied` the replacement Build func is additionally held open after firing `built` (a scheduling delay the test's synchronization does not exclude).

```console
$ cp verify/repro/c1_overlap_ordering_test.go /home/ubuntu/wt/c1/internal/xds/balancer/clusterimpl/tests/
$ cd /home/ubuntu/wt/c1 && go test -tags verify_c1 ./internal/xds/balancer/clusterimpl/tests -run '^Test/VerifyC1_BuiltDoesNotImplyApplied$' -count=1 -v 2>&1 | grep -v tlogger | tail -8
    c1_overlap_ordering_test.go:335: replacement.built fired; buildReturned=false applied=false
    c1_overlap_ordering_test.go:350: TLS handshake completed with initial roots; buildReturned=false applied=false initialClosed=false
    c1_overlap_ordering_test.go:361: FINDING: blocked root load resumed and the TLS handshake finished BEFORE the replacement security config was applied (built fired, Build not returned, UpdateState not completed)
    c1_overlap_ordering_test.go:385: after releasing Build: applied=true rpcOK=true initialClosed=true
--- FAIL: Test (0.02s)
    --- FAIL: Test/VerifyC1_BuiltDoesNotImplyApplied (0.01s)
FAIL
FAIL	google.golang.org/grpc/internal/xds/balancer/clusterimpl/tests	0.022s
```

The `FAIL` is the intentional `t.Errorf("FINDING: ...")`. Following the branch test's own step order, the blocked load resumed and the TLS handshake completed with the initial roots while the replacement Build func had not returned, the replacement HandshakeInfo had therefore not been published, and the resolver update had not completed. After the Build func was released, the update applied, the RPC succeeded, and the initial provider closed — i.e. every assertion of the branch test (RPC ok, mTLS peer, initial provider eventually closed) still holds in this non-overlapping ordering, so the test cannot tell it apart from a true overlap.

### Repro 2 (unperturbed): natural ordering, 25 iterations

`TestVerifyC1_NaturalOrdering` performs the branch test's exact steps with no delay and records, at the instant of `close(unblock)`, whether the update had completed.

```console
$ cd /home/ubuntu/wt/c1 && go test -tags verify_c1 ./internal/xds/balancer/clusterimpl/tests -run '^Test/VerifyC1_NaturalOrdering$' -count=1 -v 2>&1 | grep -E "SUMMARY|^ok"
    c1_overlap_ordering_test.go:444: SUMMARY: 25/25 iterations released the blocked load before the replacement update had been applied; 0/25 before the replacement Build function had even returned
ok  	google.golang.org/grpc/internal/xds/balancer/clusterimpl/tests	0.257s
```

`applied` here is the post-`UpdateState` signal (which fires after the whole balancer tree finished the update, so it is a conservative upper bound on "published"); it had not fired in any iteration at the moment the test released the load. The Build func had already returned in all 25 natural runs, so in practice the window between `built` and the swap is small — but it is not closed by any synchronization, as Repro 1 shows.

### Verdict reasoning

The test's only wait after pushing the replacement is `replacementEvents.built`, which is emitted from inside the replacement provider's construction — before `handleSecurityConfig` publishes the new HandshakeInfo and releases the old one. Repro 1 shows that ordering admits the handshake finishing entirely before the replacement is applied, with the test still green. **C1: CONFIRMED.**

## C2

Target: `evalon/grpc-go-xd-c46d6b0a` (`502ade5f`). On this branch the reference-count decrement path for HandshakeInfo is `(*HandshakeInfo).Release` in `internal/credentials/xds/handshake_info.go:186`; `internal/grpcsync/refcounted.go` is unchanged from base and is not used by the HandshakeInfo (it is a comparison point only).

```console
$ grep -n "refs < 0" -A5 /home/ubuntu/wt/c2/internal/credentials/xds/handshake_info.go
194:	if refs < 0 {
195-		// This indicates a reference counting bug in the caller. Reset the
196-		// counter so that the providers are not closed more than once.
197-		hi.refs.Store(0)
198-		return
199-	}
$ grep -n "logger\|grpclog" /home/ubuntu/wt/c2/internal/credentials/xds/handshake_info.go
(no output — the file has no logger)
```

### Repro: decrement below zero while capturing grpclog

`verify/repro/c2_release_negative_refcount_test.go` routes all grpclog output (verbosity 99, info/warning/error) into a buffer, then calls `Release()` on a fresh HandshakeInfo twice (1→0, then 0→-1). A second test performs the same misuse on `grpcsync.RefCounted` for comparison.

```console
$ cp verify/repro/c2_release_negative_refcount_test.go /home/ubuntu/wt/c2/internal/credentials/xds/
$ cd /home/ubuntu/wt/c2 && go test -tags verify_c2 ./internal/credentials/xds -run '^Test/VerifyC2_' -count=1 -v 2>&1 | tail -14
=== RUN   Test/VerifyC2_GrpcsyncDecrementLogsForComparison
    c2_release_negative_refcount_test.go:73: grpclog output from grpcsync.RefCounted.Decrement below zero: "2026/09/17 18:31:27 ERROR: [grpcsync] Refcount cannot be negative\n2026/09/17 18:31:27 ERROR: [grpcsync] Refcount cannot be negative\n2026/09/17 18:31:27 ERROR: [grpcsync] Refcount cannot be negative\n"
=== RUN   Test/VerifyC2_ReleaseBelowZeroEmitsNoDiagnostic
    c2_release_negative_refcount_test.go:48: refs after extra Release = 0 (reset to 0 by the refs<0 branch)
    c2_release_negative_refcount_test.go:49: providers closed 1/1 times (no double close)
    c2_release_negative_refcount_test.go:52: grpclog output emitted by the extra Release(): ""
    c2_release_negative_refcount_test.go:59: FINDING: HandshakeInfo.Release() took the count below zero (detected: counter reset to 0, no double close) but emitted NO diagnostic; grpclog captured 0 bytes
--- FAIL: Test (0.00s)
    --- PASS: Test/VerifyC2_GrpcsyncDecrementLogsForComparison (0.00s)
    --- FAIL: Test/VerifyC2_ReleaseBelowZeroEmitsNoDiagnostic (0.00s)
FAIL
FAIL	google.golang.org/grpc/internal/credentials/xds	0.004s
```

(The grpcsync line appears three times because `NewLoggerV2WithVerbosity` fans an ERROR record out to the info, warning and error writers, all of which are the same buffer.)

The `FAIL` is the intentional `t.Errorf("FINDING: ...")`. The extra `Release()` was detected (counter reset from -1 to 0, providers not closed a second time) and produced zero bytes of log output, whereas the codebase's generic `grpcsync.RefCounted.Decrement` emits `ERROR: [grpcsync] Refcount cannot be negative` for the same misuse.

### Impact reasoning

A caller that over-releases a HandshakeInfo (e.g. a future code path in which the balancer both replaces and `Close()`-releases the same HandshakeInfo) is a real ownership bug: with one handshake in flight (refs=2), the double release drops 2→1→0 and closes the certificate providers out from under the active handshake; the handshake's own later `Release()` then drops 0→-1, which is the first point where the misuse is detectable — and the branch resets the counter and returns silently. Nothing in logs points at it; the only symptom is a handshake failing with a "provider closed" error far from the cause. This is a diagnostics gap on an internal API, not a user-visible functional failure on the normal path (the normal path — one owner plus balanced Acquire/Release — never reaches `refs < 0`). **C2: CONFIRMED.**
