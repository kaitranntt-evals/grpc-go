## Environment

All commands run on Ubuntu with `go version go1.25.7 linux/amd64`. Claim branches were fetched from
`https://github.com/kaitranntt-evals/grpc-go-xds-certificate-provider-closure-race` (added as remote `claims`)
and checked out into separate worktrees:

```sh
cd ~/repos/grpc-go
git remote add claims https://github.com/kaitranntt-evals/grpc-go-xds-certificate-provider-closure-race.git
git fetch claims evalon/grpc-go-xd-04ccfc62 evalon/grpc-go-xd-1e9d8011 evalon/grpc-go-xd-a31d723d
git worktree add -b audit/04ccfc62 ~/repos/wt-04ccfc62 claims/evalon/grpc-go-xd-04ccfc62   # HEAD f655ba9c
git worktree add -b audit/1e9d8011 ~/repos/wt-1e9d8011 claims/evalon/grpc-go-xd-1e9d8011   # HEAD a99a2b48
git worktree add -b audit/a31d723d ~/repos/wt-a31d723d claims/evalon/grpc-go-xd-a31d723d   # HEAD 54e78fe7
```

Eval fixtures were copied from the attached `eval_tests.zip` byte-exact to the paths the eval provisions
(`.evaltools/candidate_test_inventory.go`, `internal/xds/balancer/clusterimpl/tests/eval_handshake_lifetime_test.go`,
`test/run_eval_xds_test_group.sh`, `test/run_candidate_tests.sh`). Every `go` command below was run with the
eval scripts' environment normalization: `unset GOFLAGS GOTOOLCHAIN GOWORK GOCACHE GOENV; export GOENV=off GOWORK=off`.

## C1

Branch: `evalon/grpc-go-xd-04ccfc62` (worktree `~/repos/wt-04ccfc62`).

### New/changed tests on the branch

```sh
cd ~/repos/wt-04ccfc62
go run ./.evaltools/candidate_test_inventory.go cc234554fb363aea445a838b341bb8a65c8305b0 $(git diff --name-only cc234554fb363aea445a838b341bb8a65c8305b0 HEAD -- '*_test.go')
```

Relevant new entries (the rest are pre-existing tests of the touched packages):

```console
internal/credentials/xds/handshake_info_test.go	Test/TestAcquireHandshakeInfo_ReleasedByOwnerDuringHandshake
internal/credentials/xds/handshake_info_test.go	Test/TestAcquireHandshakeInfo_ReleasedWithoutReplacement
internal/xds/balancer/clusterimpl/tests/clusterimpl_security_test.go	Test/TestSecurityConfigUpdate_DuringHandshake
```

### Test 1: `TestAcquireHandshakeInfo_ReleasedByOwnerDuringHandshake` (internal/credentials/xds/handshake_info_test.go)

Code order (verbatim from the branch):

```go
hi, release, err := AcquireHandshakeInfo(&hiPtr)
...
// The owner replaces the security configuration before the handshake has
// started loading its trusted roots.
replacement := NewHandshakeInfo(nil, nil, nil, false, "", false, false)
hiPtr.Swap(replacement).Release()
...
go func() {
    _, err := hi.ClientSideTLSConfig(ctx, "")   // root load starts only here
    cfgCh <- err
}()
select { case <-rootProvider.loadStarted: ... }
close(rootProvider.unblockLoad)
```

The owner's replacement + release happens *before* the root load starts (the test's own comment says so).
Property "validation-root loading is already in progress when replacement is applied / provider retired" is absent.
`TestAcquireHandshakeInfo_ReleasedWithoutReplacement` has no handshake at all.

```sh
go test ./internal/credentials/xds -run '^Test$/^AcquireHandshakeInfo_' -count=1 -v
```

```console
    --- PASS: Test/AcquireHandshakeInfo_ReleasedByOwnerDuringHandshake (0.00s)
    --- PASS: Test/AcquireHandshakeInfo_ReleasedWithoutReplacement (0.00s)
ok  	google.golang.org/grpc/internal/credentials/xds	0.005s
```

### Test 2: `TestSecurityConfigUpdate_DuringHandshake` (internal/xds/balancer/clusterimpl/tests/clusterimpl_security_test.go)

Synchronization between the Cluster update and the release of the blocked load (verbatim):

```go
if err := mgmtServer.Update(ctx, resources); err != nil { t.Fatal(err) }
for {                               // waits only for the replacement root provider to be BUILT
    ...
    case ev = <-st.built:
    ...
    if ev.instance == untrustedInstance && ev.opts.WantRoot { break }
}
sCtx, sCancel := context.WithTimeout(ctx, defaultTestShortTimeout)   // 100ms quiet window
select {
case ev := <-st.closed: t.Fatalf("... closed while a handshake was still using it")
case <-sCtx.Done():
}
unblockRootLoad()
```

There is no post-application signal (no `UpdateState` return, no observed swap/retirement). `built` fires from the
provider builder inside `handleSecurityConfig`, *before* `storeHandshakeInfo` swaps `xdsHIPtr` and releases the old
HandshakeInfo (`internal/xds/balancer/clusterimpl/clusterimpl.go` lines 369-388 on the branch). The eval fixture, by
contrast, wraps `resolver.ClientConn.UpdateState` and waits for it to return with the replacement config
(`eval_handshake_lifetime_test.go:151-173,308`).

Baseline (unmodified branch):

```sh
go test ./internal/xds/balancer/clusterimpl/tests -run '^Test$/^SecurityConfigUpdate_DuringHandshake$' -count=1 -v
bash ./test/run_eval_xds_test_group.sh handshake-lifetime
```

```console
    --- PASS: Test/SecurityConfigUpdate_DuringHandshake (0.18s)
ok  	google.golang.org/grpc/internal/xds/balancer/clusterimpl/tests	0.188s
{"Action":"pass","Package":"google.golang.org/grpc/.evaltools/lifetime_DTQcmG","Test":"TestEval_SecurityConfigUpdate_ActiveHandshakeKeepsProvider","Elapsed":0.05}
EXIT=0
```

Mutation A (control) — revert the fix so `Release()` closes providers immediately (`verify/repro/c1_eager_close.patch`):

```sh
git apply ~/repos/grpc-go/verify/repro/c1_eager_close.patch
go test ./internal/xds/balancer/clusterimpl/tests -run '^Test$/^SecurityConfigUpdate_DuringHandshake$' -count=1 -v
bash ./test/run_eval_xds_test_group.sh handshake-lifetime
```

```console
    clusterimpl_security_test.go:1045: Provider instance "trusted-roots" (opts {CertName: WantRoot:true WantIdentity:false}) closed while a handshake was still using it
    --- FAIL: Test/SecurityConfigUpdate_DuringHandshake (20.04s)
EXIT=1
... "Output":"    eval_handshake_lifetime_test.go:314: Active RPC failed after the Cluster security configuration was replaced: ... provider instance is closed\n"
... "Output":"--- FAIL: TestEval_SecurityConfigUpdate_ActiveHandshakeKeepsProvider (0.01s)\n"
EXIT=1
```

Both tests catch the reverted fix when the swap happens promptly after construction.

Mutation A + B — same reverted fix, plus the balancer applies (swaps) the replacement 300ms after building it
(`verify/repro/c1_delayed_swap.patch`, a `time.Sleep(300*time.Millisecond)` at the top of `storeHandshakeInfo`):

```sh
git apply ~/repos/grpc-go/verify/repro/c1_delayed_swap.patch
for i in 1 2 3; do go test ./internal/xds/balancer/clusterimpl/tests -run '^Test$/^SecurityConfigUpdate_DuringHandshake$' -count=1 -v | grep -E -- '--- (PASS|FAIL)|^ok|^FAIL'; done
bash ./test/run_eval_xds_test_group.sh handshake-lifetime
```

```console
    --- PASS: Test/SecurityConfigUpdate_DuringHandshake (1.22s)
ok  	google.golang.org/grpc/internal/xds/balancer/clusterimpl/tests	1.231s
EXIT=0
    --- PASS: Test/SecurityConfigUpdate_DuringHandshake (1.23s)
ok  	google.golang.org/grpc/internal/xds/balancer/clusterimpl/tests	1.238s
EXIT=0
    --- PASS: Test/SecurityConfigUpdate_DuringHandshake (1.22s)
ok  	google.golang.org/grpc/internal/xds/balancer/clusterimpl/tests	1.233s
EXIT=0
... "Output":"    eval_handshake_lifetime_test.go:314: Active RPC failed after the Cluster security configuration was replaced: rpc error: code = Unavailable desc = connection error: desc = \"transport: authentication handshake failed: xds: fetching trusted roots from CertificateProvider failed: provider instance is closed\"\n"
... "Output":"--- FAIL: TestEval_SecurityConfigUpdate_ActiveHandshakeKeepsProvider (0.91s)\n"
EXIT=1
```

With the fix reverted, the candidate test passes 3/3 as soon as application lags construction by more than its
100ms quiet window: it releases the blocked load before the replacement is applied, the handshake completes on the
still-owned old provider, and the later close/failed-connection checks pass. The eval fixture, which waits for
`UpdateState` to return, fails on the same mutation. So the candidate test does not establish that the replacement
is applied before the blocked load is released; it only establishes replacement-provider construction plus a timer.

Cleanup: `git checkout -- internal/credentials/xds/handshake_info.go internal/xds/balancer/clusterimpl/clusterimpl.go`.

### Impact reasoning

The production fix on the branch is correct under the eval fixture, but the branch's own regression coverage is
timing-based: a slow CI box (GC pause, scheduler stall > 100ms between provider construction and the `xdsHIPtr`
swap) turns the e2e test into a no-op that would pass even if the refcount fix regressed, and the unit test never
overlaps a load with retirement at all. Nobody hits this in production; it weakens regression protection only.

## C2

Branch: `evalon/grpc-go-xd-1e9d8011` (HEAD `a99a2b481ac8d627361f7e0c04c32b333c9e7118`). `go.mod`/`go.sum` are unchanged
vs base (`git diff --stat cc234554 claims/evalon/grpc-go-xd-1e9d8011` lists only 6 `.go` files).

Run 1 — worktree, eval-normalized environment (first run, downloads modules):

```sh
cd ~/repos/wt-1e9d8011
unset GOFLAGS GOTOOLCHAIN GOWORK GOCACHE GOENV; export GOENV=off GOWORK=off
go vet ./... ; echo EXIT=$?
```

```console
go: downloading google.golang.org/protobuf v1.36.11
go: downloading github.com/google/go-cmp v0.7.0
... (40 "go: downloading" lines, no diagnostics)
EXIT=0
```

Run 2 — from `~/repos/grpc-go` exactly as the claim states, branch checked out detached, default shell environment:

```sh
cd ~/repos/grpc-go
git checkout --detach claims/evalon/grpc-go-xd-1e9d8011      # HEAD a99a2b481ac8d627361f7e0c04c32b333c9e7118
go vet ./... ; echo EXIT=$?
git checkout verify/grpc-go-xds-certificate-provider-closure-race-v-b62eec79
```

```console
EXIT=0
```

`go vet ./...` produced no diagnostics and exited 0 in both runs (`wc -l /tmp/c2_vet_main.log` → 1 line, the `EXIT=0` marker).

## C3

Branch: `evalon/grpc-go-xd-a31d723d` (worktree `~/repos/wt-a31d723d`). There is no
`internal/xds/balancer/clusterimpl/tests/concurrent_handshake_test.go` on the branch (`ls` shows only
`balancer_test.go clusterimpl_security_test.go`); the concurrent handshake-lifetime test with `cfgCh`, `isClosed()` and a
deferred release is `TestAcquireHandshakeInfo_OwnerClosesDuringRootLoad` in
`internal/credentials/xds/handshake_info_test.go` (lines 670-741).

Relevant code (verbatim):

```go
cfgCh := make(chan error, 1)
go func() {
    defer release()                          // final provider-reference release: runs AFTER the send below
    _, err := hi.ClientSideTLSConfig(ctx, "")
    cfgCh <- err                             // result notification
}()
...
close(oldRoot.unblock)
select {
case err := <-cfgCh: ...                     // test wakes here
case <-ctx.Done(): ...
}
if !oldRoot.isClosed() || !oldID.isClosed() {   // closure assertion (line 727), no wait
    t.Fatalf("Replaced providers not closed after the last handshake released them: ...")
}
```

The result is sent on `cfgCh` before the deferred `release()` runs, and the assertion at line 727 is a non-blocking
`isClosed()` check, so nothing orders the final release before the assertion.

Natural flake (unmodified branch):

```sh
cd ~/repos/wt-a31d723d
go test ./internal/credentials/xds -run '^Test$/^AcquireHandshakeInfo_OwnerClosesDuringRootLoad$' -count=1 -v
go test ./internal/credentials/xds -run '^Test$/^AcquireHandshakeInfo_OwnerClosesDuringRootLoad$' -count=500 -v > /tmp/c3_stress.log; echo EXIT=$?
grep -c "^    --- PASS" /tmp/c3_stress.log; grep -c "^    --- FAIL" /tmp/c3_stress.log
grep "handshake_info_test.go:727" /tmp/c3_stress.log | sort | uniq -c
```

```console
    --- PASS: Test/AcquireHandshakeInfo_OwnerClosesDuringRootLoad (0.01s)
ok  	google.golang.org/grpc/internal/credentials/xds	0.014s
EXIT=1
498
2
      1     handshake_info_test.go:727: Replaced providers not closed after the last handshake released them: root closed = false, identity closed = false
      1     handshake_info_test.go:727: Replaced providers not closed after the last handshake released them: root closed = true, identity closed = false
```

2 of 500 iterations fail at the closure assertion on an unmodified branch; the `root closed = true, identity closed = false`
variant shows the assertion running while `release()` was mid-way through closing the providers.

Controlled schedule (`verify/repro/c3_paused_release_test.go`): identical test, except the goroutine sleeps 50ms between
`cfgCh <- err` and `release()`:

```sh
cp ~/repos/grpc-go/verify/repro/c3_paused_release_test.go internal/credentials/xds/
go test ./internal/credentials/xds -run '^Test$/^C3PausedRelease_OwnerClosesDuringRootLoad$' -count=5 -v
rm internal/credentials/xds/c3_paused_release_test.go
```

```console
    c3_paused_release_test.go:125: Replaced providers not closed after the last handshake released them: root closed = false, identity closed = false
--- FAIL: Test (0.06s)
    --- FAIL: Test/C3PausedRelease_OwnerClosesDuringRootLoad (0.06s)
    (5 of 5 iterations fail identically)
FAIL	google.golang.org/grpc/internal/credentials/xds	0.319s
EXIT=1
```

Pausing release lets the assertion execute first every time — the refute condition ("pausing release also prevents the
assertion from proceeding") does not hold.

For contrast, the branch's other new concurrent test `TestClientCredsHandshakeInfoReplacedDuringRootLoad`
(`credentials/xds/xds_client_test.go`) releases inside `creds.ClientHandshake` (`defer release()` in `credentials/xds/xds.go`)
before the result is sent, and waits on `<-oldRoot.closed` with a context timeout:

```sh
go test ./credentials/xds -run '^Test$/^ClientCredsHandshakeInfoReplacedDuringRootLoad$' -count=200
```

```console
ok  	google.golang.org/grpc/credentials/xds	8.974s
EXIT=0
```

### Impact reasoning

The production change is not at fault; the unit test is racy. In `go test -count=500` it fails ~0.4% of the time on
an idle machine, and any scheduling pause between the result send and the deferred release makes it fail, so it will
flake in CI. Fix: move `release()` before `cfgCh <- err` in the goroutine (or wait on `oldRoot.closed` / `oldID.closed`
with a timeout instead of the instantaneous `isClosed()` check).
