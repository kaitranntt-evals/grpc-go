## Setup (shared by every section; repeated where a section depends on it)

Audit run `v-f9743207`. Audited branch: `grpc-go-xds-certificate-provider-closure-race-perfect` (HEAD `3483b320`, base `cc234554fb363aea445a838b341bb8a65c8305b0`) checked out as `verify/grpc-go-xds-certificate-provider-closure-race-v-f9743207` in `~/repos/grpc-go`. Toolchain: `go1.25.7 linux/amd64`.

Claim-target branches live in the evaluation repository, not on `origin`; they were fetched from it and checked out into disposable worktrees:

```sh
cd ~/repos/grpc-go
git remote add evalrepo https://github.com/kaitranntt-evals/grpc-go-xds-certificate-provider-closure-race.git
for id in e209a3c1 7d3bd828 09997670 08e1c163 a827a5ed 8f85e742 2c037706 6683c715 bc2b4bfe 587a607c 81241111 0783ed60 e452584a 383689fc 41a22441 99b4229c 97bb17f0 aa94d3f0 b7c0f5e6 69c43750 29066cc7; do
  git fetch evalrepo evalon/grpc-go-xd-$id && git worktree add ~/wt/$id evalrepo/evalon/grpc-go-xd-$id
done
git worktree add ~/wt/perfect origin/grpc-go-xds-certificate-provider-closure-race-perfect
```

The eval fixtures (`eval_tests.zip`) were extracted byte-exact and copied (untracked, git-ignored) into the main checkout and each worktree: `.evaltools/candidate_test_inventory.go`, `internal/xds/balancer/clusterimpl/tests/eval_handshake_lifetime_test.go`, `test/run_eval_xds_test_group.sh`, `test/run_candidate_tests.sh`.

All probe/repro test files under `verify/repro/` carry `//go:build verifyrepro` so that `go build ./...` / `go vet ./...` on this branch ignore them; they are run by copying them into the package under test in a worktree of the target branch with `go test -tags verifyrepro ...` (see the `// Run:` header of each file / `run.sh`). Every `run.sh` reverts its temporary instrumentation (`git checkout -- <file>`) and deletes the copied test files; `git status --short` in every worktree was empty after each run. No production file on this branch was modified.

### Required commands on the audit branch (all exit 0)

```console
$ cd ~/repos/grpc-go && bash ./test/run_eval_xds_test_group.sh handshake-lifetime
{"Time":"2026-09-01T20:59:35.538736572Z","Action":"run","Package":"google.golang.org/grpc/internal/xds/balancer/clusterimpl/tests","Test":"TestEval_SecurityConfigUpdate_ActiveHandshakeKeepsProvider"}
{"Time":"2026-09-01T20:59:35.57899093Z","Action":"pass","Package":"google.golang.org/grpc/internal/xds/balancer/clusterimpl/tests","Test":"TestEval_SecurityConfigUpdate_ActiveHandshakeKeepsProvider","Elapsed":0.04}
{"Time":"2026-09-01T20:59:35.579813106Z","Action":"output","Package":"google.golang.org/grpc/internal/xds/balancer/clusterimpl/tests","Output":"ok  \tgoogle.golang.org/grpc/internal/xds/balancer/clusterimpl/tests\t0.047s\n"}
exit=0
$ go build ./...
exit=0
$ bash ./test/run_eval_xds_test_group.sh affected-test-compile
ok  	google.golang.org/grpc/credentials/tls/certprovider	0.002s [no tests to run]
ok  	google.golang.org/grpc/credentials/tls/certprovider/pemfile	0.004s [no tests to run]
ok  	google.golang.org/grpc/credentials/xds	0.003s [no tests to run]
ok  	google.golang.org/grpc/internal/credentials/xds	0.004s [no tests to run]
ok  	google.golang.org/grpc/internal/grpcsync	0.002s [no tests to run]
ok  	google.golang.org/grpc/internal/xds/balancer/clusterimpl	0.006s [no tests to run]
?   	google.golang.org/grpc/internal/xds/balancer/clusterimpl/internal	[no test files]
ok  	google.golang.org/grpc/internal/xds/balancer/clusterimpl/tests	0.008s [no tests to run]
ok  	google.golang.org/grpc/internal/xds/server	0.006s [no tests to run]
exit=0
$ bash ./test/run_candidate_tests.sh
==> Detected substantive agent-authored tests:
    credentials/xds/xds_client_test.go	s.TestClientCredsProviderSwitch
    ...
    internal/grpcsync/refcounted_test.go	s.TestRefCounted_TryIncrement
    ...
[PASS] TestSecurityConfigUpdate_ConcurrentHandshake        # 20 x [PASS], 0 x [FAIL]
exit=0
$ go vet ./...
exit=0
```

CI was not run; no pull request was opened.

## C1

**Claim:** a client handshake does not keep its selected validation-root provider usable when replacement occurs after snapshot selection but before KeyMaterial begins. Parts: *Snapshot ownership* (snapshot read before ownership is secured) and *Selected-root continuity* (after a failed acquisition the handshake continues unowned or switches roots). Adjudicated independently on all 16 branches.

**Method (identical on every branch).** `verify/repro/c1/run.sh <id>` inserts a 3-line test hook right after the *first* `hiPtr.Load()` of the client-handshake path of that branch (in `credentials/xds/xds.go`, or in `internal/credentials/xds/handshake_info.go` for `7d3bd828`, `09997670`, `08e1c163` whose selection lives in a helper there), copies `verify/repro/c1/common/c1_probe_common_test.go` plus `verify/repro/c1/branches/c1_probe_<id>_test.go` into `credentials/xds/`, runs them, and reverts. The probe builds two root providers through the certprovider store (`A` trusts the test server, `B` does not), publishes `hi1(A)` in the connection's `atomic.Pointer[HandshakeInfo]`, and inside the hook — i.e. after the handshake has read `hi1` but before it secures ownership — performs the exact replacement `clusterimpl` performs on that branch (close the old providers, store `hi2(B)`), then lets the handshake proceed against a real TLS server. Instrumented counters record whether `A` was closed, whether its `KeyMaterial` was ever called, and which roots the handshake ended up using. The probe fails (`t.Error`) when the handshake reaches a closed selected provider.

```sh
cd ~/repos/grpc-go
for id in e209a3c1 7d3bd828 09997670 08e1c163 a827a5ed 8f85e742 2c037706 6683c715 bc2b4bfe 587a607c 81241111 0783ed60 e452584a 383689fc 41a22441 99b4229c; do
  bash verify/repro/c1/run.sh $id        # instrumented worktree at ~/wt/$id; output: go test -tags verifyrepro ./credentials/xds -run 'Test/C1Probe' -count=1 -v
done
```

**Interpretation rule applied to every branch.** Reading the pointer is not, by itself, a "selection" of roots: a branch is REFUTED when, under the forced replacement, the handshake never touches `A` (no `KeyMaterial` call on `A`, no closed-provider error), fails its acquisition of `hi1` atomically, and re-selects the *current* configuration `hi2(B)` before reading any material — that is the acquire-or-retry contract (the same one the audited perfect branch implements with `TryIncrement`), and the handshake is then correctly bound to `B` for the rest of its life. A branch is CONFIRMED when the handshake proceeds with `hi1` after its provider was closed (uses a closed selected provider / fails before loading material), or when it acquires `A`'s material and later switches roots.

### e209a3c1 — CONFIRMED

Branch: https://github.com/kaitranntt-evals/grpc-go-xds-certificate-provider-closure-race/tree/evalon/grpc-go-xd-e209a3c1. `ClientHandshake` does `hi := HandshakeInfoFromAttributes(chi.Attributes).Load()` (xds.go:116) and only later `release := hi.Retain()` (xds.go:129); `Retain` holds the store wrappers but cannot fail or detect that they are already closed, and `clusterimpl` (unchanged from base) closes the old providers (clusterimpl.go:378/381) *before* storing the new HandshakeInfo (clusterimpl.go:386).

```console
$ bash verify/repro/c1/run.sh e209a3c1
    c1_probe_e209a3c1_test.go:63: C1PROBE handshake error: xds: fetching trusted roots from CertificateProvider failed: provider instance is closed
    c1_probe_e209a3c1_test.go:63: C1PROBE loads observed by hook: [hi1(A)]
    c1_probe_e209a3c1_test.go:63: C1PROBE rootA=A{closed=true closeCount=1 keyMaterialCalls=0 keyMaterialCallsAfterClose=0}
    c1_probe_e209a3c1_test.go:63: C1PROBE rootB=B{closed=false closeCount=0 keyMaterialCalls=0 keyMaterialCallsAfterClose=0}
    c1_probe_e209a3c1_test.go:63: C1PROBE RESULT: handshake proceeded with its CLOSED selected provider A and failed before loading material; error=xds: fetching trusted roots from CertificateProvider failed: provider instance is closed
    c1_probe_e209a3c1_test.go:87: C1PROBE loads observed by hook: [(no hook)]
    c1_probe_e209a3c1_test.go:87: C1PROBE RESULT: handshake proceeded with its CLOSED selected provider A and failed before loading material; error=xds: fetching trusted roots from CertificateProvider failed: provider instance is closed
--- FAIL: Test (0.10s)
    --- FAIL: Test/C1Probe_e209a3c1 (0.05s)
    --- FAIL: Test/C1Probe_e209a3c1_NoHook (0.05s)
FAIL	google.golang.org/grpc/credentials/xds	0.107s
```

Part *Snapshot ownership*: held (Load, then Retain, no atomic acquire). Part *Selected-root continuity*: held (handshake continues with the unowned, already-closed `hi1`). The `_NoHook` subtest needs no instrumentation: it merely starts a handshake in the real intermediate state of a `clusterimpl` replacement on this branch (old providers closed, new HandshakeInfo not yet stored) and gets the same `provider instance is closed` failure.

**Impact.** Any client connection whose TLS handshake starts while a Cluster security-configuration update is being applied can fail with `xds: fetching trusted roots from CertificateProvider failed: provider instance is closed` — a hard handshake failure on the everyday xDS path (a control-plane pushing a new Cluster with the same or new certificate-provider instance while the client is (re)connecting). The window is the whole interval from provider close to HandshakeInfo store plus the handshake's own Load→Retain gap; the failure is deterministic once the interleaving happens (no retry inside the handshake). This is exactly the flake `ClientSideXDS_WithValidAndInvalidSecurityConfigurationSPIFFE` was tracking. Workaround: none inside gRPC; the connection attempt fails and the channel reconnects.

### 09997670 — CONFIRMED

Branch: https://github.com/kaitranntt-evals/grpc-go-xds-certificate-provider-closure-race/tree/evalon/grpc-go-xd-09997670. `CurrentHandshakeInfo` (handshake_info.go:157-173) is a **bounded** retry: `for range 2 { hi = hiPtr.Load(); ...; if release, ok := hi.Hold(); ok { return hi, release } }` and then `return hi, noop` — after two failed `Hold` attempts it returns the last loaded snapshot **unheld**. `clusterimpl` on this branch still closes the old providers (clusterimpl.go:378/381) before storing the replacement (clusterimpl.go:386), so during that window both attempts load `hi1` whose store wrappers are closed, `Hold` fails twice, and the unheld closed snapshot is used.

```console
$ bash verify/repro/c1/run.sh 09997670
    c1_probe_09997670_test.go:61: C1PROBE loads observed by hook: [hi1(A) hi2(B)]
    c1_probe_09997670_test.go:61: C1PROBE RESULT: A was never read; handshake did not acquire A and proceeded with replacement B (error=x509: certificate signed by unknown authority)
    c1_probe_09997670_test.go:122: C1PROBE handshake error: xds: fetching trusted roots from CertificateProvider failed: provider instance is closed
    c1_probe_09997670_test.go:122: C1PROBE loads observed by hook: [(no hook)]
    c1_probe_09997670_test.go:122: C1PROBE RESULT: handshake proceeded with its CLOSED selected provider A and failed before loading material; error=xds: fetching trusted roots from CertificateProvider failed: provider instance is closed
    c1_probe_09997670_test.go:97: C1PROBE handshake error: xds: fetching trusted roots from CertificateProvider failed: provider instance is closed
    c1_probe_09997670_test.go:97: C1PROBE loads observed by hook: [hi1(A) hi1(A)]
    c1_probe_09997670_test.go:97: C1PROBE rootA=A{closed=true closeCount=1 keyMaterialCalls=0 keyMaterialCallsAfterClose=0}
    c1_probe_09997670_test.go:97: C1PROBE RESULT: handshake proceeded with its CLOSED selected provider A and failed before loading material; error=xds: fetching trusted roots from CertificateProvider failed: provider instance is closed
--- FAIL: Test (0.06s)
    --- PASS: Test/C1Probe_09997670_FullReplacementInWindow (0.01s)
    --- FAIL: Test/C1Probe_09997670_NoHook (0.05s)
    --- FAIL: Test/C1Probe_09997670_ProvidersClosedBeforeStore (0.00s)
FAIL	google.golang.org/grpc/credentials/xds	0.066s
```

When the *whole* replacement (close + store) fits inside the window the retry does pick up `hi2` (`FullReplacementInWindow` passes). But in the real intermediate state of a `clusterimpl` update — providers closed, replacement not yet stored — the two `Hold` attempts both load `hi1` (`loads observed: [hi1(A) hi1(A)]`), both fail, and `CurrentHandshakeInfo` hands the handshake the closed unheld snapshot. Part *Snapshot ownership*: held. Part *Selected-root continuity*: held (continues unowned with a closed provider).

**Impact.** Same user-visible failure as `e209a3c1` (`provider instance is closed` handshake failure) for any handshake that starts while a Cluster security update is between "close old providers" and "store new HandshakeInfo"; the bounded retry only helps when the whole update completes within the handshake's two-attempt window. Everyday xDS path; no workaround inside the handshake.

### The other 14 branches — REFUTED

Each of these branches does load-then-acquire, but the acquire is a CAS/`TryIncrement`/`Acquire` that fails once the snapshot has been retired, and the handshake then re-loads the *current* pointer and acquires that instead, before reading any material. Under the forced replacement the observations are identical on all 14: `A` is closed exactly once by the replacement, `A.KeyMaterial` is **never** called (`keyMaterialCalls=0`), `B.KeyMaterial` is called once, and the handshake fails only because `B` legitimately does not trust the server (`x509: certificate signed by unknown authority`) — i.e. it is correctly bound to the configuration that is current at the time it secures ownership. Branches whose hook log shows only `[hi1(A)]` re-load into a different variable (`newHI`) on retry, which the hook does not intercept; the `B.keyMaterialCalls=1 / A.keyMaterialCalls=0` counters show the same behaviour.

```console
$ for id in 7d3bd828 08e1c163 a827a5ed 8f85e742 2c037706 6683c715 bc2b4bfe 587a607c 81241111 0783ed60 e452584a 383689fc 41a22441 99b4229c; do bash verify/repro/c1/run.sh $id; done
# 7d3bd828  loads observed by hook: [hi1(A) hi2(B)]  rootA=A{closed=true closeCount=1 keyMaterialCalls=0}  rootB=B{closed=false keyMaterialCalls=1}  RESULT: A was never read; handshake did not acquire A and proceeded with replacement B  --- PASS  ok  0.012s
# 08e1c163  loads observed by hook: [hi1(A)]         rootA=A{closed=true closeCount=1 keyMaterialCalls=0}  rootB=B{closed=false keyMaterialCalls=1}  RESULT: A was never read; ... proceeded with replacement B  --- PASS  ok  0.011s
# a827a5ed  loads observed by hook: [hi1(A) hi2(B)]  rootA=A{closed=true closeCount=1 keyMaterialCalls=0}  rootB=B{closed=false keyMaterialCalls=1}  RESULT: A was never read; ... proceeded with replacement B  --- PASS  ok  0.010s
# 8f85e742  loads observed by hook: [hi1(A)]         rootA=A{closed=true closeCount=1 keyMaterialCalls=0}  rootB=B{closed=false keyMaterialCalls=1}  RESULT: A was never read; ... proceeded with replacement B  --- PASS  ok  0.010s
# 2c037706  loads observed by hook: [hi1(A)]         rootA=A{closed=true closeCount=1 keyMaterialCalls=0}  rootB=B{closed=false keyMaterialCalls=1}  RESULT: A was never read; ... proceeded with replacement B  --- PASS  ok  0.010s
# 6683c715  loads observed by hook: [hi1(A) hi2(B)]  rootA=A{closed=true closeCount=1 keyMaterialCalls=0}  rootB=B{closed=false keyMaterialCalls=1}  RESULT: A was never read; ... proceeded with replacement B  --- PASS  ok  0.011s
# bc2b4bfe  loads observed by hook: [hi1(A)]         rootA=A{closed=true closeCount=1 keyMaterialCalls=0}  rootB=B{closed=false keyMaterialCalls=1}  RESULT: A was never read; ... proceeded with replacement B  --- PASS  ok  0.011s
# 587a607c  loads observed by hook: [hi1(A) hi2(B)]  rootA=A{closed=true closeCount=1 keyMaterialCalls=0}  rootB=B{closed=false keyMaterialCalls=1}  RESULT: A was never read; ... proceeded with replacement B  --- PASS  ok  0.011s
# 81241111  loads observed by hook: [hi1(A)]         rootA=A{closed=true closeCount=1 keyMaterialCalls=0}  rootB=B{closed=false keyMaterialCalls=1}  RESULT: A was never read; ... proceeded with replacement B  --- PASS  ok  0.010s
# 0783ed60  loads observed by hook: [hi1(A) hi2(B)]  rootA=A{closed=true closeCount=1 keyMaterialCalls=0}  rootB=B{closed=false keyMaterialCalls=1}  RESULT: A was never read; ... proceeded with replacement B  --- PASS  ok  0.010s
# e452584a  loads observed by hook: [hi1(A) hi2(B)]  rootA=A{closed=true closeCount=1 keyMaterialCalls=0}  rootB=B{closed=false keyMaterialCalls=1}  RESULT: A was never read; ... proceeded with replacement B  --- PASS  ok  0.010s
# 383689fc  loads observed by hook: [hi1(A) hi2(B)]  rootA=A{closed=true closeCount=1 keyMaterialCalls=0}  rootB=B{closed=false keyMaterialCalls=1}  RESULT: A was never read; ... proceeded with replacement B  --- PASS  ok  0.010s
# 41a22441  loads observed by hook: [hi1(A) hi2(B)]  rootA=A{closed=true closeCount=1 keyMaterialCalls=0}  rootB=B{closed=false keyMaterialCalls=1}  RESULT: A was never read; ... proceeded with replacement B  --- PASS  ok  0.010s
# 99b4229c  loads observed by hook: [hi1(A) hi2(B)]  rootA=A{closed=true closeCount=1 keyMaterialCalls=0}  rootB=B{closed=false keyMaterialCalls=1}  RESULT: A was never read; ... proceeded with replacement B  --- PASS  ok  0.010s
```

Every one of these 14 runs also printed `handshake error: x509: certificate signed by unknown authority` (roots `B`) and `keyMaterialCallsAfterClose=0` for `A`. Part *Snapshot ownership*: the raw pointer read does precede the acquire, but the read value is never used unless the acquire succeeds, so no unowned selection exists. Part *Selected-root continuity*: refuted — the handshake neither continues unowned nor switches roots after selecting (acquiring) them. Verdict per branch: REFUTED.

The hidden fixture also passed on all 16 branches (`bash ./test/run_eval_xds_test_group.sh handshake-lifetime` in each worktree, `"Action":"pass"` for `TestEval_SecurityConfigUpdate_ActiveHandshakeKeepsProvider`); it is event-controlled at the KeyMaterial level and therefore cannot see the Load→acquire window that the C1 probe forces.

## C2

Branch: https://github.com/kaitranntt-evals/grpc-go-xds-certificate-provider-closure-race/tree/evalon/grpc-go-xd-97bb17f0 (worktree `~/wt/97bb17f0`). Changed tests vs base: `internal/credentials/xds/shared_provider_test.go` (+269) and `internal/xds/balancer/clusterimpl/tests/clusterimpl_security_test.go` (+296).

```console
$ cd ~/wt/97bb17f0 && git diff cc234554fb363aea445a838b341bb8a65c8305b0 --stat -- '*_test.go'
 internal/credentials/xds/shared_provider_test.go   | 269 +++++++++++++++++++
 .../clusterimpl/tests/clusterimpl_security_test.go | 296 +++++++++++++++++++++
$ grep -n '^func (s) TestSharedProvider' internal/credentials/xds/shared_provider_test.go
105:func (s) TestSharedProvider_ReplacementDuringBlockedLoad(t *testing.T) {
183:func (s) TestSharedProvider_ReplacementBeforeLoadStarts(t *testing.T) {
215:func (s) TestSharedProvider_CloseWithoutHandshakes(t *testing.T) {
233:func (s) TestSharedProvider_ReplacementRootsUsedByLaterHandshake(t *testing.T) {
$ go test ./internal/credentials/xds -run 'Test/SharedProvider' -count=1 -v
=== RUN   Test/SharedProvider_CloseWithoutHandshakes
=== RUN   Test/SharedProvider_ReplacementBeforeLoadStarts
=== RUN   Test/SharedProvider_ReplacementDuringBlockedLoad
=== RUN   Test/SharedProvider_ReplacementRootsUsedByLaterHandshake
--- PASS: Test (0.11s)
    --- PASS: Test/SharedProvider_ReplacementRootsUsedByLaterHandshake (0.00s)
ok  	google.golang.org/grpc/internal/credentials/xds	0.109s
```

`TestSharedProvider_ReplacementRootsUsedByLaterHandshake` (shared_provider_test.go:233-269) builds `oldHI` (roots `server_ca_cert.pem`, which trust the server cert), asserts `oldCfg.VerifyPeerCertificate(serverCert, nil)` succeeds, then builds `newHI` with roots `client_ca_cert.pem`, calls `sharedOldRoot.Close()` (the replacement), obtains `newCfg` from `newHI.ClientSideTLSConfig` and asserts:

```go
if err := newCfg.VerifyPeerCertificate(serverCert, nil); err == nil {
	t.Fatal("VerifyPeerCertificate() succeeded with validation roots that do not trust the server certificate, want failure")
}
```

That is a replacement followed by an assertion that the later handshake object follows the replacement roots and rejects a certificate they do not trust. At the balancer level, `TestSecurityConfigUpdate_ReplacementDuringBlockedLoad` (clusterimpl_security_test.go:879) pushes a replacement Cluster security config, waits for the replacement provider to be built (`t.Fatal("Timed out waiting for the replacement certificate provider to be built")` at :964) and for the replaced provider to be closed (:995), and the byte-exact hidden fixture `TestEval_SecurityConfigUpdate_ActiveHandshakeKeepsProvider` — run on this branch with `bash ./test/run_eval_xds_test_group.sh handshake-lifetime` → `"Action":"pass"` — performs a Cluster replacement and then a follow-up RPC that must load material from the *replacement* provider. Mutation check performed in the worktree (reverted afterwards): making the balancer skip publishing the replacement HandshakeInfo made that fixture fail with `timed out waiting for replacement provider KeyMaterial on the follow-up RPC: context deadline exceeded`, confirming the later-connection assertion is live.

Verdict: REFUTED — the changed tests do replace the security configuration and assert that a subsequent handshake/connection uses the replacement validation roots.

## C3

Branch: https://github.com/kaitranntt-evals/grpc-go-xds-certificate-provider-closure-race/tree/evalon/grpc-go-xd-aa94d3f0 (worktree `~/wt/aa94d3f0`). Changed concurrency regressions: `credentials/tls/certprovider/store_test.go:TestStoreCloseWhileLoadInProgress` and `credentials/xds/xds_client_test.go` (the root-load-vs-replacement test). The latter is event-based (`oldFake.loadStarted.Receive(ctx)` before `oldProv.Close()`; its `time.After(defaultTestShortTimeout)` is only used *after* that ordering is established to assert that no close happened). The former is not:

```go
resCh := make(chan kmResult, 1)
go func() {
	km, err := prov.KeyMaterial(ctx)
	resCh <- kmResult{km: km, err: err}
}()

time.Sleep(defaultTestShortTimeout)   // 10ms — the only thing standing between "goroutine started" and Close
prov.Close()
```

Repro `verify/repro/c3/run.sh` prints the ordering line, runs the test unmutated, then simulates a late-scheduled load goroutine (20ms delay before `prov.KeyMaterial`) and runs again:

```console
$ bash verify/repro/c3/run.sh
== ordering evidence in the changed regression (only a sleep precedes Close):
440:	time.Sleep(defaultTestShortTimeout)
== unmutated run
=== RUN   Test/StoreCloseWhileLoadInProgress
--- PASS: Test (0.01s)
ok  	google.golang.org/grpc/credentials/tls/certprovider	0.014s
== mutation applied:
+		time.Sleep(2 * defaultTestShortTimeout) // C3 mutation: late scheduling of the load goroutine
 		km, err := prov.KeyMaterial(ctx)
== mutated run
    store_test.go:450: KeyMaterial() failed after the provider was closed: provider instance is closed
--- FAIL: Test (0.02s)
    --- FAIL: Test/StoreCloseWhileLoadInProgress (0.02s)
FAIL	google.golang.org/grpc/credentials/tls/certprovider	0.023s
```

The test's premise ("KeyMaterial entered before Close") is established solely by the 10ms sleep: a goroutine scheduled later than that (the mutation models a loaded CI machine) makes the regression fail even though the production code is unchanged. An event (e.g. a `loadStarted` channel closed by the fake provider inside `KeyMaterial`) would be immune.

Verdict: CONFIRMED. **Impact:** the regression protecting the very race this repository is fixing is itself timing-dependent — it can flake red on slow runners (false alarm) and, symmetrically, it can pass without ever having exercised the in-progress-load path if the sleep elapses before the goroutine runs, so it does not reliably guard the fix.

## C4

Audited branch (`~/wt/perfect`, same code as this branch's HEAD `3483b320`): https://github.com/kaitranntt-evals/grpc-go/tree/grpc-go-xds-certificate-provider-closure-race-perfect. `ClientSideTLSConfig` (`internal/credentials/xds/handshake_info.go`) does `hiRC := hiPtr.Load()`, then `if !hiRC.TryIncrement() { if hiPtr.Load() != hiRC { continue }; return error }`, reads material only after the increment succeeds, and releases via the returned `done` (`hiRC.Decrement`).

Repro `verify/repro/c4/run.sh` inserts a hook right after `hiRC := hiPtr.Load()`, copies `verify/repro/c4/c4_probe_test.go` into `internal/credentials/xds/`, runs it, and reverts. Two scenarios: (1) the hook performs the full Cluster replacement (`rc1.Decrement()` retiring `A`'s HandshakeInfo, store `rc2(B)`) between `Load` and `TryIncrement`; (2) the replacement happens while `A.KeyMaterial` is executing, i.e. after ownership.

```console
$ bash verify/repro/c4/run.sh
    c4_probe_test.go:105: C4PROBE loads=[rc1(A) rc2(B)] useFallback=false err=<nil>
    c4_probe_test.go:106: C4PROBE A: keyMaterialCalls=0 closed=true; B: keyMaterialCalls=1 closed=false
    c4_probe_test.go:110: C4PROBE roots in returned tls.Config: B (server cert rejected: x509: certificate signed by unknown authority)
    c4_probe_test.go:114: C4PROBE RESULT: A was never read (no roots selected before ownership); handshake bound to B after retry
    c4_probe_test.go:128: C4PROBE useFallback=false err=<nil>
    c4_probe_test.go:129: C4PROBE A: keyMaterialCalls=1 closed=false; B: keyMaterialCalls=0 closed=false
    c4_probe_test.go:134: C4PROBE roots in returned tls.Config: A (server cert accepted)
    c4_probe_test.go:138: C4PROBE RESULT: handshake remained bound to the selected provider A
    c4_probe_test.go:141: C4PROBE after done(): A closed=true
--- PASS: Test (0.00s)
    --- PASS: Test/C4Probe_ReplaceAfterMaterialRead (0.00s)
    --- PASS: Test/C4Probe_ReplaceBetweenLoadAndTryIncrement (0.00s)
ok  	google.golang.org/grpc/internal/credentials/xds	0.005s
```

Part *Acquisition sequence*: the load does precede `TryIncrement`, but nothing is read from the loaded object before the increment succeeds (`A.keyMaterialCalls=0`). Part *Replacement interleaving*: refuted — when the update lands between the two operations the handshake "has not yet selected validation roots" and binds to the current configuration `B`; once material has been selected (scenario 2) it stays bound to `A` and `A` is only closed after `done()`. It cannot switch roots after selection.

Verdict: REFUTED.

## C5

Branch: https://github.com/kaitranntt-evals/grpc-go-xds-certificate-provider-closure-race/tree/evalon/grpc-go-xd-81241111 (worktree `~/wt/81241111`). Repro `verify/repro/c5/c5_probe_test.go` builds a `credentials.ClientHandshakeInfo` whose attributes carry a non-nil `*atomic.Pointer[HandshakeInfo]` whose `Load()` is nil, runs a full `ClientHandshake` against a TLS test server under `recover()`, and reports.

```console
$ cp verify/repro/c5/c5_probe_test.go ~/wt/81241111/credentials/xds/ && (cd ~/wt/81241111 && go test -tags verifyrepro ./credentials/xds -run 'Test/C5Probe' -count=1 -v)
    c5_probe_test.go:48: C5PROBE panic=<nil> err=<nil>
    c5_probe_test.go:55: C5PROBE RESULT: no panic; handshake completed via fallback credentials (authType=tls)
--- PASS: Test (0.01s)
    --- PASS: Test/C5Probe_NilHandshakeInfoInAtomicPointer (0.01s)
ok  	google.golang.org/grpc/credentials/xds	0.011s
```

Verdict: REFUTED — a nil current HandshakeInfo yields the controlled fallback-credentials path, no nil dereference.

## C6

Branch: https://github.com/kaitranntt-evals/grpc-go-xds-certificate-provider-closure-race/tree/evalon/grpc-go-xd-e452584a (worktree `~/wt/e452584a`). `ClientHandshake` (`credentials/xds/xds.go:121-129`): `for { hi = hiPtr.Load(); if hi == nil { fallback }; if hi.Acquire() { break } }; defer hi.Release()`. The forced interleaving is the C1 probe for this branch (`verify/repro/c1/branches/c1_probe_e452584a_test.go`): a hook right after the first `hiPtr.Load()` performs the replacement (close `A`'s providers via `clusterimpl`'s ordering, store `hi2(B)`), i.e. the snapshot is replaced and retired between selection and acquisition.

```console
$ bash verify/repro/c1/run.sh e452584a
    c1_probe_e452584a_test.go:46: C1PROBE handshake error: x509: certificate signed by unknown authority
    c1_probe_e452584a_test.go:46: C1PROBE loads observed by hook: [hi1(A) hi2(B)]
    c1_probe_e452584a_test.go:46: C1PROBE rootA=A{closed=true closeCount=1 keyMaterialCalls=0 keyMaterialCallsAfterClose=0}
    c1_probe_e452584a_test.go:46: C1PROBE rootB=B{closed=false closeCount=0 keyMaterialCalls=1 keyMaterialCallsAfterClose=0}
    c1_probe_e452584a_test.go:46: C1PROBE RESULT: A was never read; handshake did not acquire A and proceeded with replacement B (error=x509: certificate signed by unknown authority)
--- PASS: Test (0.01s)
    --- PASS: Test/C1Probe_e452584a (0.01s)
ok  	google.golang.org/grpc/credentials/xds	0.010s
```

Part *Ownership ordering*: the bare pointer is loaded before `Acquire`, but `Acquire` on the retired `hi1` fails and the loaded value is discarded — it never becomes a usable snapshot (`A.keyMaterialCalls=0`, no closed-provider access). Part *Replacement outcome*: refuted — the handshake does not continue unowned and does not switch roots after selecting them; it selects (acquires) the current `hi2(B)` before reading any material and stays on `B`. The hidden fixture also passed on this branch.

Verdict: REFUTED.

## C7

Branch: https://github.com/kaitranntt-evals/grpc-go-xds-certificate-provider-closure-race/tree/evalon/grpc-go-xd-b7c0f5e6 (worktree `~/wt/b7c0f5e6`). `credentials/tls/certprovider/store.go`:

```go
func (w *singleCloseWrappedProvider) Close() {
	w.mu.Lock()
	if w.loads > 0 { w.closePending = true; w.mu.Unlock(); return }
	w.mu.Unlock()          // <-- interval: lock released, provider not yet closed
	w.closeProvider()
}
func (w *singleCloseWrappedProvider) KeyMaterial(ctx context.Context) (*KeyMaterial, error) {
	w.mu.Lock(); provider := *w.provider.Load(); w.loads++; w.mu.Unlock()
	km, err := provider.KeyMaterial(ctx)
	...
```

Repro `verify/repro/c7/run.sh` inserts a hook between that `w.mu.Unlock()` and `w.closeProvider()`, copies `c7_probe_test.go` into `credentials/tls/certprovider/` (part *Wrapper synchronization*) and `c7_server_exposure_probe_test.go` into `internal/xds/server/` (part *Handshake exposure*), runs both, reverts.

```console
$ bash verify/repro/c7/run.sh
== go test -tags verifyrepro ./credentials/tls/certprovider ./internal/xds/server -run 'Test/C7Probe' -count=1 -v
    c7_probe_test.go:66: C7PROBE after Close() returned: underlying provider closed=true while KeyMaterial still in progress
    c7_probe_test.go:69: C7PROBE admitted KeyMaterial returned km=<nil> err=underlying provider was closed while this KeyMaterial call was in progress
    c7_probe_test.go:71: C7PROBE wrapper state: loads=0 closePending=false
    c7_probe_test.go:74: C7PROBE RESULT: a KeyMaterial call admitted after Close observed zero loads had its provider closed underneath it
--- FAIL: Test (0.00s)
    --- FAIL: Test/C7Probe_CloseAdmitsLoadThenClosesUnderneath (0.00s)
FAIL	google.golang.org/grpc/credentials/tls/certprovider	0.003s
    c7_server_exposure_probe_test.go:92: C7PROBE after connWrapper.Close() returned: underlying provider closed=true while the server handshake's KeyMaterial was in progress
    c7_server_exposure_probe_test.go:99: C7PROBE server handshake ServerSideTLSConfig err=xds: fetching identity certificates from CertificateProvider failed: underlying provider was closed while this KeyMaterial call was in progress
    c7_server_exposure_probe_test.go:101: C7PROBE RESULT: production server handshake reached the admission-vs-close interval unprotected; provider closed underneath its load
--- FAIL: Test (0.00s)
    --- FAIL: Test/C7Probe_ServerHandshakeExposure (0.00s)
FAIL	google.golang.org/grpc/internal/xds/server	0.007s
```

Part *Wrapper synchronization*: held — a `KeyMaterial` call that enters after `Close` has observed `loads == 0` and unlocked is admitted (`loads++`) and the underlying provider is closed while its load is in flight; `closePending` never gets set so nothing defers the close. Part *Handshake exposure*: held on the production **server** path — `connWrapper.Close()` (`internal/xds/server/conn_wrapper.go:150-158`) closes the store-built providers directly with no HandshakeInfo/RefCounted ownership around them, and `HandshakeInfo.ServerSideTLSConfig` → `wrapper.KeyMaterial` running concurrently gets its provider closed underneath it (`fetching identity certificates ... failed`). (The client path on this branch does hold HandshakeInfo ownership around the load, so the exposure is the server side.)

Verdict: CONFIRMED. **Impact:** on an xDS-enabled server, a connection being torn down (`connWrapper.Close`, e.g. on Listener/filter-chain update or client disconnect) while its TLS handshake is loading certificates fails the handshake with a provider-closed error instead of finishing with the material it was loading; the wrapper's "defer close until loads drain" promise is silently void for any load admitted in the unlock→close gap. Ordinary path (updates + concurrent handshakes), timing-dependent, no workaround.

## C8

Branch: https://github.com/kaitranntt-evals/grpc-go-xds-certificate-provider-closure-race/tree/evalon/grpc-go-xd-7d3bd828 (worktree `~/wt/7d3bd828`). `internal/xds/balancer/clusterimpl/clusterimpl.go:handleSecurityConfig` builds `rootProvider` via `buildProvider(...)`, then builds the identity provider and on error does `return err` without closing `rootProvider` or publishing it. (The audited perfect branch, by contrast, has `if err != nil { if rootProvider != nil { rootProvider.Close() }; return nil, nil, err }` in `buildProviders`.)

Repro `verify/repro/c8/c8_probe_test.go` overrides the package-level `buildProvider` so the root build succeeds (returning a counting provider) and the identity build fails, calls `handleSecurityConfig`, and inspects the root provider and the published HandshakeInfo.

```console
$ cp verify/repro/c8/c8_probe_test.go ~/wt/7d3bd828/internal/xds/balancer/clusterimpl/ && (cd ~/wt/7d3bd828 && go test -tags verifyrepro ./internal/xds/balancer/clusterimpl -run 'Test/C8Probe' -count=1 -v)
    c8_probe_test.go:58: C8PROBE handleSecurityConfig err=c8: identity provider build failure
    c8_probe_test.go:67: C8PROBE root provider root-inst/root-cert: closeCount=0; published HandshakeInfo=<nil>
    c8_probe_test.go:69: C8PROBE RESULT: root provider built during the failed update was neither closed nor published (leaked)
--- FAIL: Test (0.00s)
    --- FAIL: Test/C8Probe_RootProviderLeakOnIdentityBuildFailure (0.00s)
FAIL	google.golang.org/grpc/internal/xds/balancer/clusterimpl	0.007s
```

Verdict: CONFIRMED. **Impact:** every Cluster update whose root certificate-provider instance is valid but whose identity instance fails to build (misconfigured/absent identity plugin instance in the bootstrap — a plausible operator error that is also a documented NACK path) leaks one store-referenced root provider: its watcher goroutines, file polling and memory persist for the process lifetime, and repeated re-pushes of the bad config leak one more per push. Silent (no error other than the identity failure); no workaround short of fixing the config and restarting.

## C9

Branch: https://github.com/kaitranntt-evals/grpc-go-xds-certificate-provider-closure-race/tree/evalon/grpc-go-xd-69c43750 (worktree `~/wt/69c43750`). `credentials/tls/certprovider/store.go:singleCloseWrappedProvider.KeyMaterial`: when `w.provider == nil` (closed) it returns `w.km` (the last loaded material) if non-nil instead of `errProviderClosed`.

Repro `verify/repro/c9/c9_probe_test.go`: load once through the wrapper, `Close()` with no load in progress (underlying closes immediately), then a brand-new caller calls `KeyMaterial`.

```console
$ cp verify/repro/c9/c9_probe_test.go ~/wt/69c43750/credentials/tls/certprovider/ && (cd ~/wt/69c43750 && go test -tags verifyrepro ./credentials/tls/certprovider -run 'Test/C9Probe' -count=1 -v)
    c9_probe_test.go:42: C9PROBE after Close(): underlying closed=true (Close completed, no load in progress)
    c9_probe_test.go:45: C9PROBE new KeyMaterial call after Close: km=0xc000195e90 err=<nil> (errProviderClosed=false); underlying KeyMaterial calls=1
    c9_probe_test.go:48: C9PROBE RESULT: wrapper served cached key material to a new caller after Close completed
--- FAIL: Test (0.00s)
    --- FAIL: Test/C9Probe_CachedMaterialServedAfterClose (0.00s)
FAIL	google.golang.org/grpc/credentials/tls/certprovider	0.003s
```

Verdict: CONFIRMED. **Impact:** a handshake that reaches a *replaced* (closed) provider — the C1-style race, or any stale HandshakeInfo — silently succeeds with the **old** validation roots / identity certificate instead of failing closed, so a security-configuration replacement intended to stop trusting a CA (or to rotate a compromised identity) can be bypassed by connections that hit the stale wrapper; the material is also never refreshed (the underlying provider is gone), so long-lived stale users keep pre-rotation certs. "State that looks correct but isn't": handshakes pass, nothing is logged.

## C10

Branch: https://github.com/kaitranntt-evals/grpc-go-xds-certificate-provider-closure-race/tree/evalon/grpc-go-xd-a827a5ed (worktree `~/wt/a827a5ed`). `internal/credentials/xds/handshake_info_test.go:TestHandshakeInfoRefs_CreatorReleasesWhileLoadIsBlocked` starts `hi.ClientSideTLSConfig` in a goroutine, calls `hi.ReleaseRef()`, then asserts "still blocked" with

```go
select {
case res := <-resultCh:
	t.Fatalf("hi.ClientSideTLSConfig() returned (%v, %v) while the root load was expected to be blocked", res.cfg, res.err)
case <-time.After(10 * time.Millisecond):
}
```

with no event proving the goroutine entered `KeyMaterial`. Repro `verify/repro/c10/run.sh` runs it unmutated, then delays the goroutine 50ms before it calls `ClientSideTLSConfig` (so nothing has entered `KeyMaterial` when the 10ms timer fires) and runs it 5 times:

```console
$ bash verify/repro/c10/run.sh
== the 'blocked' assertion in the changed regression:
734:	case <-time.After(10 * time.Millisecond):
== unmutated run
    --- PASS: Test/HandshakeInfoRefs_CreatorReleasesWhileLoadIsBlocked (0.01s)
ok  	google.golang.org/grpc/internal/credentials/xds	0.014s
== mutation applied:
+		time.Sleep(50 * time.Millisecond) // C10 mutation: the load has not started when the 10ms timer fires
 		cfg, err := hi.ClientSideTLSConfig(ctx, "")
== mutated run (x5)
    --- PASS: Test/HandshakeInfoRefs_CreatorReleasesWhileLoadIsBlocked (0.05s)
    --- PASS: Test/HandshakeInfoRefs_CreatorReleasesWhileLoadIsBlocked (0.05s)
    --- PASS: Test/HandshakeInfoRefs_CreatorReleasesWhileLoadIsBlocked (0.05s)
    --- PASS: Test/HandshakeInfoRefs_CreatorReleasesWhileLoadIsBlocked (0.05s)
    --- PASS: Test/HandshakeInfoRefs_CreatorReleasesWhileLoadIsBlocked (0.05s)
ok  	google.golang.org/grpc/internal/credentials/xds	0.261s
```

The "blocked inside the root load" assertion is satisfied by a goroutine that has not even started loading; the timer, not an entry event, is what the test relies on.

Verdict: CONFIRMED. **Impact:** the regression meant to prove "creator releases while a load is blocked keeps the provider open" can pass vacuously under scheduler delay (the situation it is guarding against then goes untested), and the test is sensitive to machine speed in both directions; the fix it protects has no reliable guard.

## C11

Branch: https://github.com/kaitranntt-evals/grpc-go-xds-certificate-provider-closure-race/tree/evalon/grpc-go-xd-8f85e742 (worktree `~/wt/8f85e742`). `internal/credentials/xds/handshake_info.go`: `Acquire` is `for { rc := refCount.Load(); if rc == 0 { return false }; if CAS(rc, rc+1) { return true } }`, `Release` is `if refCount.Add(-1) != 0 { return }; close providers` — no guard against a negative count.

```console
$ cp verify/repro/c11/c11_probe_test.go ~/wt/8f85e742/internal/credentials/xds/ && (cd ~/wt/8f85e742 && go test -tags verifyrepro ./internal/credentials/xds -run 'Test/C11Probe' -count=1 -v)
    c11_probe_test.go:27: C11PROBE initial refCount=1
    c11_probe_test.go:30: C11PROBE after owner Release: refCount=0 rootProvider.closeCount=1
    c11_probe_test.go:32: C11PROBE Acquire at refCount 0 -> false (refCount now 0)
    c11_probe_test.go:35: C11PROBE after extra Release (underflow): refCount=-1 rootProvider.closeCount=1
    c11_probe_test.go:37: C11PROBE Acquire after underflow -> true (refCount now 0)
    c11_probe_test.go:39: C11PROBE RESULT: Acquire succeeded on a HandshakeInfo whose providers were already closed (closeCount=1) after a refcount underflow
--- FAIL: Test (0.00s)
    --- FAIL: Test/C11Probe_UnderflowThenAcquire (0.00s)
FAIL	google.golang.org/grpc/internal/credentials/xds	0.003s
```

Verdict: CONFIRMED. **Impact:** after any double release (a bug class the primitive exists to make impossible to exploit), `Acquire` reports ownership of a HandshakeInfo whose providers are already closed — the handshake then proceeds against closed providers (`provider instance is closed`) or, combined with a C9-style wrapper, with stale material. Contrived trigger (needs a caller bug), but the primitive's contract ("acquisition after cleanup fails") does not hold, and no panic/log makes the underflow visible.

## C12

Branch: https://github.com/kaitranntt-evals/grpc-go-xds-certificate-provider-closure-race/tree/evalon/grpc-go-xd-29066cc7 (worktree `~/wt/29066cc7`). Custom state machine in `internal/credentials/xds/handshake_info.go`: `refs atomic.Uint64` with `retiredBit = 1<<63`; `acquire()` (CAS increment, rejects when retired), `release()` (`Add(^0)`, closes providers when the result is exactly `retiredBit`), `retire()` (`Or(retiredBit)`, closes when the previous value was 0), `AcquireHandshakeInfo`/`UpdateHandshakeInfo` wrappers. Changed files vs base: `credentials/xds/xds.go`, `credentials/xds/xds_client_test.go` (+118, adds `TestClientCredsProviderSwitchDuringRootLoad`), `internal/credentials/xds/handshake_info.go` (+68), `internal/xds/balancer/clusterimpl/clusterimpl.go`. No test file was added or changed in `internal/credentials/xds/`.

Repro `verify/repro/c12/run.sh` inventories the tests and applies two mutations any focused unit test of the primitive would catch:

```console
$ bash verify/repro/c12/run.sh
== inventory: test files referencing the state machine API
./credentials/xds/xds_client_test.go
== inventory: dedicated tests in internal/credentials/xds
internal/credentials/xds/handshake_info_test.go          # pre-existing, unchanged from base; does not reference acquire/release/retire/retiredBit
== inventory: tests changed vs base cc234554 mentioning retire/acquire/release/underflow/concurrent
(no direct assertions on these states)
== baseline run
ok  	google.golang.org/grpc/internal/credentials/xds	0.067s
ok  	google.golang.org/grpc/credentials/xds	0.282s
ok  	google.golang.org/grpc/internal/xds/balancer/clusterimpl	0.010s
ok  	google.golang.org/grpc/internal/xds/balancer/clusterimpl/tests	3.937s
== M1: acquire() accepts retired HandshakeInfo
-		if refs&retiredBit != 0 {
+		if false { // C12 mutation M1
ok  	google.golang.org/grpc/internal/credentials/xds	0.074s
ok  	google.golang.org/grpc/credentials/xds	0.237s
ok  	google.golang.org/grpc/internal/xds/balancer/clusterimpl	0.010s
ok  	google.golang.org/grpc/internal/xds/balancer/clusterimpl/tests	3.848s
== M2: release() closes providers on every post-retirement release
-	if refs == retiredBit {
+	if refs&retiredBit != 0 { // C12 mutation M2
ok  	google.golang.org/grpc/internal/credentials/xds	0.034s
ok  	google.golang.org/grpc/credentials/xds	0.447s
ok  	google.golang.org/grpc/internal/xds/balancer/clusterimpl	0.014s
ok  	google.golang.org/grpc/internal/xds/balancer/clusterimpl/tests	3.936s
```

(`internal/xds/balancer/clusterimpl/tests` above includes the byte-exact hidden fixture, present in the worktree.) A third mutation tried during the investigation — removing the `closeProviders()` call from `retire()` — did make the integrated tests fail (`--- FAIL: Test/AggregateClusterSecurityConfig (10.03s)` waiting for a provider close), which shows the integrated lifecycle *is* covered; but "failed acquisition after retirement" (M1) and "exactly-once cleanup" (M2) are not asserted anywhere, and there are no tests for the transition boundary (`refs == retiredBit` vs `refs&retiredBit`), concurrent acquire/release/retire, or underflow (by inspection, `release()` on `refs == 0` wraps to `2^64-1`, which has the retired bit set: the object looks retired forever and `closeProviders()` never runs — untested).

Verdict: CONFIRMED. **Impact:** the only thing guarding the hand-rolled lock-free ownership bit-packing is an end-to-end provider-switch test; two of the four correctness properties it must uphold can be broken with the whole suite green, so a future refactor (or a subtle off-by-one in the bit arithmetic) would ship unnoticed and re-introduce exactly the closed-provider/stale-roots class of bug this change set exists to fix.
