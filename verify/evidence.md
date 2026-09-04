# Evidence — behavioral audit `v-7838fce0`

Audited branch: `origin/grpc-go-xds-certificate-provider-closure-race-perfect` (HEAD `3483b320`), checked out as `verify/grpc-go-xds-certificate-provider-closure-race-v-7838fce0`. Base commit `cc234554fb363aea445a838b341bb8a65c8305b0`. Go `go1.25.7 linux/amd64`.

Claim-target branches live in a second repository and were fetched as remote `evalrepo` (`https://github.com/kaitranntt-evals/grpc-go-xds-certificate-provider-closure-race.git`) and checked out as detached worktrees under `~/wt/<suffix>`:

```sh
git remote add evalrepo https://github.com/kaitranntt-evals/grpc-go-xds-certificate-provider-closure-race.git
for b in 002b71aa 1b97259d d0ead149 99352433 8e86a861 cfdc9c00 baea696f b86f6a10 4518237f 3b5d9214 ab232d20 53e1cbd8; do
  git fetch evalrepo evalon/grpc-go-xd-$b && git worktree add --detach ~/wt/$b FETCH_HEAD
done
```

| worktree | branch `evalon/grpc-go-xd-…` | HEAD |
|---|---|---|
| `~/wt/002b71aa` | 002b71aa | `0a7805b5cb9c6ce1f8ffd4d608adaca2d647e58e` |
| `~/wt/1b97259d` | 1b97259d | `a6e376eb7c64a86ce61273aabb415240e1793a44` |
| `~/wt/d0ead149` | d0ead149 | `b8c1a756fab19d52cfe7bfc0a9f85506ec986dd8` |
| `~/wt/99352433` | 99352433 | `640de09d75d6387ff8d6b7787570cb38d497f193` |
| `~/wt/8e86a861` | 8e86a861 | `d993aa7f27594c175612597873bcd45a6c6549ab` |
| `~/wt/cfdc9c00` | cfdc9c00 | `924ebd273b171b1167c6920656ff3795a9669174` |
| `~/wt/baea696f` | baea696f | `19195c579649523f1710fc3dbbc55dc440f14a53` |
| `~/wt/b86f6a10` | b86f6a10 | `619a0d43721cb032733a0dc7d6c8d377856249d4` |
| `~/wt/4518237f` | 4518237f | `a3079c29b65eda3538c88f573af09af280aee3f4` |
| `~/wt/3b5d9214` | 3b5d9214 | `f54b0463c452f0148744c4ecffdfda0d7f38cf71` |
| `~/wt/ab232d20` | ab232d20 | `1afaf58a8b52590d646d432090d48635513d873f` |
| `~/wt/53e1cbd8` | 53e1cbd8 | `8a420eff0523d45aff98c89dde5b7732e415800f` |

All repro files under `verify/repro/` are copied *into* a worktree only for the duration of a run and removed afterwards (`git status --short` in each worktree was empty after every run); no production code was modified anywhere.

## C1

**Claim.** A Cluster security update that successfully creates a root provider and then fails to create the identity provider returns without closing or releasing the new root provider. Seven branches.

**Method.** `verify/repro/c1_root_provider_leak_test.go` is dropped into package `clusterimpl` and drives the production `clusterImplBalancer.UpdateClientConnState` → `handleSecurityConfig` path with the package-level `buildProvider` hook overridden: the root build returns a close-counting provider, the identity build returns an error. It then reports how many times the root provider's `Close()` was called (a) after the failed update returned and (b) after `balancer.Close()`.

```sh
for b in 002b71aa 1b97259d d0ead149 99352433 8e86a861 cfdc9c00 baea696f; do
  echo "== $b ($(cd ~/wt/$b && git rev-parse --short HEAD))"
  (cd ~/wt/$b && cp ~/repos/grpc-go/verify/repro/c1_root_provider_leak_test.go internal/xds/balancer/clusterimpl/zz_verify_c1_test.go \
     && go test ./internal/xds/balancer/clusterimpl -run 'Test/VerifyC1_' -count=1 -v 2>&1 | grep -E 'zz_verify_c1|^(--- |ok|FAIL|PASS)'
   rm internal/xds/balancer/clusterimpl/zz_verify_c1_test.go)
done
```

**Observed (identical on all seven branches; shown once, headers listed):**

```console
== 002b71aa (0a7805b5)
== 1b97259d (a6e376eb)
== d0ead149 (b8c1a756)
== 99352433 (640de09d)
== 8e86a861 (d993aa7f)
== cfdc9c00 (924ebd27)
== baea696f (19195c57)
    zz_verify_c1_test.go:97: UpdateClientConnState() returned: received Cluster resource that contains invalid security config: verify-c1: injected identity provider build failure
    zz_verify_c1_test.go:108: root provider Close() calls after failed UpdateClientConnState(): 0
    zz_verify_c1_test.go:112: root provider Close() calls after balancer Close(): 0
    zz_verify_c1_test.go:115: LEAK: root provider built by the failed security update was not closed before UpdateClientConnState() returned (Close() calls=0); after balancer Close() calls=0
--- FAIL: Test (0.00s)
FAIL	google.golang.org/grpc/internal/xds/balancer/clusterimpl	0.007s
```

**Contrast — same repro on the audited branch (`3483b320`):**

```console
== audited branch (3483b320)
    zz_verify_c1_test.go:97: UpdateClientConnState() returned: received Cluster resource that contains invalid security config: verify-c1: injected identity provider build failure
    zz_verify_c1_test.go:108: root provider Close() calls after failed UpdateClientConnState(): 1
    zz_verify_c1_test.go:112: root provider Close() calls after balancer Close(): 1
--- PASS: Test (0.00s)
ok  	google.golang.org/grpc/internal/xds/balancer/clusterimpl	0.007s
```

**Source context (all seven target branches share this shape in `internal/xds/balancer/clusterimpl/clusterimpl.go`):**

```go
rp, err := buildProvider(cpc, config.RootInstanceName, config.RootCertName, false, true)
if err != nil { return err }
rootProvider = rp
...
identityProvider, err = buildProvider(cpc, name, cert, true, false)
if err != nil {
    return err            // rootProvider is neither closed nor stored anywhere
}
```

**Verdict: CONFIRMED on all seven branches.** The freshly built root provider is never closed — not when the update fails, and not when the balancer is later closed (it was never stored in `cachedRoot`/the HandshakeInfo). Impact reasoning: `buildProvider` goes through `certprovider.BuildableConfig.Build`, i.e. the process-wide `certprovider` store, which hands out ref-counted wrappers keyed by (plugin, config, cert name, want-identity/root). An orphaned wrapper reference therefore keeps the underlying plugin instance (for `file_watcher`, a polling goroutine holding file handles) alive for the life of the process, even after a later successful update for the same root instance is closed by the balancer (that Close only decrements the store's count back to the leaked 1). The trigger is an identity-provider `Build()` error after a successful root build on a Cluster update — uncommon (bootstrap instance names are validated by the xDS client), but each occurrence leaks one provider and a NACKed/retried update repeats it. No user-side workaround other than restarting the process.

## C2

**Claim.** The new/changed Go tests do not causally establish that a follow-up connection is governed by the replacement validation roots. Four branches: b86f6a10, baea696f, 4518237f, 3b5d9214.

**Test inventory (fixture tool, byte-exact copy from `eval_tests.zip`):**

```sh
for b in b86f6a10 baea696f 4518237f 3b5d9214; do echo "== $b"; (cd ~/wt/$b && files=$(git diff --name-only cc234554fb363aea445a838b341bb8a65c8305b0 HEAD -- '*_test.go') && go run ~/eval_tests/tests/candidate_test_inventory.go cc234554fb363aea445a838b341bb8a65c8305b0 $files); done
```

```console
== b86f6a10
internal/xds/balancer/clusterimpl/tests/clusterimpl_security_test.go	s.TestSecurityConfigUpdate_DuringHandshake
credentials/xds/xds_client_test.go	s.TestClientCredsProviderReplacedDuringHandshake
credentials/xds/xds_client_test.go	s.TestClientCredsHandshakeInfoClosedBeforeHandshake
== baea696f
internal/xds/balancer/clusterimpl/tests/clusterimpl_security_test.go	s.TestSecurityConfigUpdate_DuringHandshake
credentials/xds/xds_client_test.go	s.TestClientCredsProviderSwitchDuringHandshake
internal/credentials/xds/handshake_info_test.go	s.TestAcquireHandshakeInfo_ReleasedWithoutReplacement
internal/credentials/xds/handshake_info_test.go	s.TestHandshakeInfo_ReplacedBeforeRootLoad
internal/credentials/xds/handshake_info_test.go	s.TestHandshakeInfo_ReplacedDuringRootLoad
== 4518237f
credentials/xds/xds_client_test.go	s.TestClientCredsProviderReplacedDuringHandshake
internal/credentials/xds/handshake_info_test.go	s.TestHandshakeInfo_ClosedWithoutReplacement
internal/credentials/xds/handshake_info_test.go	s.TestHandshakeInfo_ProviderReplacedDuringHandshake
internal/credentials/xds/handshake_info_test.go	s.TestHandshakeInfo_AcquireNil
internal/xds/balancer/clusterimpl/tests/clusterimpl_security_test.go	s.TestSecurityConfigUpdate_ProviderReplacedDuringHandshake
== 3b5d9214
internal/credentials/xds/handshake_info_test.go	s.TestAcquireHandshakeInfo_Nil
internal/credentials/xds/handshake_info_test.go	s.TestHandshakeInfo_ReleaseAfterAcquireBeforeLoad
internal/credentials/xds/handshake_info_test.go	s.TestHandshakeInfo_ReleaseWithoutHandshakes
internal/xds/balancer/clusterimpl/tests/clusterimpl_security_provider_lifetime_test.go	s.TestSecurityConfigUpdate_DuringBlockedRootsLoad
credentials/xds/xds_client_test.go	s.TestClientCredsProviderSwitchDuringHandshake
```

**What the follow-up assertions say (verbatim from the branches).** In every branch the first handshake uses `x509/server_ca_cert.pem` (trusts the test server) and the replacement provider uses `x509/client_ca_cert.pem` (does not). E.g. b86f6a10 `credentials/xds/xds_client_test.go`:

```go
root1 := newBlockingRootProvider(t, "x509/server_ca_cert.pem")
...
root2 := makeRootProvider(t, "x509/client_ca_cert.pem")
...
if _, _, err := creds.ClientHandshake(hsCtx, authority, conn); err == nil {
    t.Fatal("ClientHandshake() succeeded with untrusted validation roots, want failure")
}
```

and the clusterimpl integration tests end with `testutils.AwaitState(ctx, t, cc, connectivity.TransientFailure)` (4518237f additionally `status.Code(err) != codes.Unavailable`). The assertion is indeed a generic `err == nil` / TransientFailure check, so the causal question was settled by mutation.

**Method.** `verify/repro/c2_replacement_roots_mutation.sh` runs, per branch, the credentials test and the clusterimpl integration test (1) as written, (2) with the follow-up error logged, (3) with the *replacement* provider's roots changed to the *prior* roots (`client_ca_cert.pem → server_ca_cert.pem`, files restored afterwards). If the tests still pass in (3), the asserted failure does not depend on the replacement roots (claim holds); if they now fail, the asserted outcome distinguishes replacement roots from prior roots (claim refuted).

```sh
cd ~/repos/grpc-go
bash verify/repro/c2_replacement_roots_mutation.sh ~/wt/b86f6a10 'Test/ClientCredsProviderReplacedDuringHandshake$' ./internal/xds/balancer/clusterimpl/tests 'Test/SecurityConfigUpdate_DuringHandshake$' internal/xds/balancer/clusterimpl/tests/clusterimpl_security_test.go
bash verify/repro/c2_replacement_roots_mutation.sh ~/wt/baea696f 'Test/ClientCredsProviderSwitchDuringHandshake$' ./internal/xds/balancer/clusterimpl/tests 'Test/SecurityConfigUpdate_DuringHandshake$' internal/xds/balancer/clusterimpl/tests/clusterimpl_security_test.go
bash verify/repro/c2_replacement_roots_mutation.sh ~/wt/4518237f 'Test/ClientCredsProviderReplacedDuringHandshake$' ./internal/xds/balancer/clusterimpl/tests 'Test/SecurityConfigUpdate_ProviderReplacedDuringHandshake$' internal/xds/balancer/clusterimpl/tests/clusterimpl_security_test.go
bash verify/repro/c2_replacement_roots_mutation.sh ~/wt/3b5d9214 'Test/ClientCredsProviderSwitchDuringHandshake$' ./internal/xds/balancer/clusterimpl/tests 'Test/SecurityConfigUpdate_DuringBlockedRootsLoad$' internal/xds/balancer/clusterimpl/tests/clusterimpl_security_provider_lifetime_test.go
```

### C2 — b86f6a10

```console
===== 1. baseline
--- PASS: Test (0.02s)      # credentials/xds Test/ClientCredsProviderReplacedDuringHandshake
--- PASS: Test (0.32s)      # clusterimpl/tests Test/SecurityConfigUpdate_DuringHandshake
===== 2. instrumented (log follow-up error)
    xds_client_test.go:798: VERIFY follow-up handshake err: x509: certificate signed by unknown authority
    clusterimpl_security_test.go:909: VERIFY follow-up RPC err: rpc error: code = Unavailable desc = connection error: desc = "transport: authentication handshake failed: x509: certificate signed by unknown authority"
===== 3. mutated (replacement roots == prior roots: client_ca_cert.pem -> server_ca_cert.pem in replacement provider)
--- mutated lines:
> 	root2 := makeRootProvider(t, "x509/server_ca_cert.pem")
> 			untrustedInstance: controllableRootProviderConfig(untrustedName, testdata.Path("x509/server_ca_cert.pem"), false),
    xds_client_test.go:799: ClientHandshake() succeeded with untrusted validation roots, want failure
--- FAIL: Test (0.07s)
    clusterimpl_security_test.go:908: Timed out waiting for state change.  got READY; want TRANSIENT_FAILURE
--- FAIL: Test (5.08s)
```

### C2 — baea696f

```console
===== 1. baseline
--- PASS: Test (0.01s)      # Test/ClientCredsProviderSwitchDuringHandshake
--- PASS: Test (0.13s)      # Test/SecurityConfigUpdate_DuringHandshake
===== 2. instrumented (log follow-up error)
    xds_client_test.go:818: VERIFY follow-up handshake err: x509: certificate signed by unknown authority
    clusterimpl_security_test.go:1036: VERIFY follow-up RPC err: rpc error: code = Unavailable desc = connection error: desc = "transport: authentication handshake failed: x509: certificate signed by unknown authority"
===== 3. mutated (replacement roots == prior roots: client_ca_cert.pem -> server_ca_cert.pem in replacement provider)
--- mutated lines:
> 	root2 := makeRootProvider(t, "x509/server_ca_cert.pem")
> 		rootsFile = "x509/server_ca_cert.pem"
    xds_client_test.go:819: ClientHandshake() succeeded with replacement roots that do not trust the server certificate
--- FAIL: Test (0.01s)
    clusterimpl_security_test.go:1034: Timed out waiting for state change.  got READY; want TRANSIENT_FAILURE
--- FAIL: Test (5.03s)
```

### C2 — 4518237f

```console
===== 1. baseline
--- PASS: Test (0.02s)      # Test/ClientCredsProviderReplacedDuringHandshake
--- PASS: Test (0.12s)      # Test/SecurityConfigUpdate_ProviderReplacedDuringHandshake
===== 2. instrumented (log follow-up error)
    xds_client_test.go:792: VERIFY follow-up handshake err: x509: certificate signed by unknown authority
    clusterimpl_security_test.go:1021: VERIFY follow-up RPC err: rpc error: code = Unavailable desc = connection error: desc = "transport: authentication handshake failed: x509: certificate signed by unknown authority"
===== 3. mutated (replacement roots == prior roots: client_ca_cert.pem -> server_ca_cert.pem in replacement provider)
--- mutated lines:
> 	root2 := makeRootProvider(t, "x509/server_ca_cert.pem")
> 			untrustedRootsInstance:             blockingRootsProviderConfig(testdata.Path("x509/server_ca_cert.pem")),
    xds_client_test.go:793: ClientHandshake() succeeded when expected to fail with replacement roots
--- FAIL: Test (0.07s)
    clusterimpl_security_test.go:985: Timeout when waiting for a "blocking-roots-cert-provider" certificate provider to be built
--- FAIL: Test (20.09s)
```

Note: on 4518237f the mutated integration test fails earlier for an unrelated reason (both cert-provider instances now have byte-identical configs, so the test's per-instance provider bookkeeping no longer sees a second build). That run is therefore not usable as evidence either way; the credentials-level test alone is the distinguishing evidence for this branch.

### C2 — 3b5d9214

```console
===== 1. baseline
--- PASS: Test (0.02s)      # Test/ClientCredsProviderSwitchDuringHandshake
--- PASS: Test (0.17s)      # Test/SecurityConfigUpdate_DuringBlockedRootsLoad
===== 2. instrumented (log follow-up error)
    xds_client_test.go:795: VERIFY follow-up handshake err: x509: certificate signed by unknown authority
    clusterimpl_security_provider_lifetime_test.go:379: VERIFY follow-up RPC err: rpc error: code = Unavailable desc = connection error: desc = "transport: authentication handshake failed: x509: certificate signed by unknown authority"
===== 3. mutated (replacement roots == prior roots: client_ca_cert.pem -> server_ca_cert.pem in replacement provider)
--- mutated lines:
> 	root2 := makeRootProvider(t, "x509/server_ca_cert.pem")
> 			untrustedRootsCertName: loadRoots(t, "x509/server_ca_cert.pem"),
    xds_client_test.go:796: ClientHandshake() succeeded with replacement trust roots that do not trust the server certificate
--- FAIL: Test (0.07s)
    clusterimpl_security_provider_lifetime_test.go:377: Timed out waiting for state change.  got READY; want TRANSIENT_FAILURE
--- FAIL: Test (5.03s)
```

**Verdict: REFUTED on all four branches.** On each branch at least one changed test (the `credentials/xds` test on every branch; also the clusterimpl integration test on b86f6a10, baea696f, 3b5d9214) asserts a follow-up connection outcome that flips from *fail* to *succeed* when the replacement provider is given the prior roots — i.e. the asserted outcome distinguishes replacement roots from prior roots, and the logged failure is the certificate-validation error `x509: certificate signed by unknown authority`. The refute condition ("at least one follow-up connection whose asserted outcome distinguishes the replacement roots from the prior roots") is met.

## C3

**Claim.** Same as C2, on branch ab232d20 (`TestClientCredsHandshakeInfoClosedBeforeAcquire` named).

**Inventory:**

```console
== ab232d20
credentials/xds/xds_client_test.go	s.TestClientCredsProviderReplacedDuringHandshake
credentials/xds/xds_client_test.go	s.TestClientCredsHandshakeInfoClosedBeforeAcquire
```

**Assertions (verbatim, `~/wt/ab232d20/credentials/xds/xds_client_test.go`).** `TestClientCredsHandshakeInfoClosedBeforeAcquire`: prior roots `root1 := makeRootProvider(t, "x509/client_ca_cert.pem")` (untrusted), the first handshake must fail with `ErrHandshakeInfoClosed`; then `root2 := makeRootProvider(t, "x509/server_ca_cert.pem")` is published and the follow-up handshake must *succeed* (`if err != nil { t.Fatalf("ClientHandshake() failed: %v", err) }` + `compareAuthInfo`). `TestClientCredsProviderReplacedDuringHandshake`: `root1` server CA (blocking), `root2` client CA, follow-up must fail (`err == nil` → `t.Fatal(...)`).

**Method.** `verify/repro/c3_replacement_roots_mutation.sh` runs both tests as written, logs the follow-up error, then swaps the replacement provider's roots for the prior roots inside each test (one at a time, file restored in between).

```sh
cd ~/repos/grpc-go && bash verify/repro/c3_replacement_roots_mutation.sh ~/wt/ab232d20
```

```console
===== 1. baseline (tests as written)
    --- PASS: Test/ClientCredsHandshakeInfoClosedBeforeAcquire (0.10s)
    --- PASS: Test/ClientCredsProviderReplacedDuringHandshake (0.08s)
===== 2. instrumented: log the follow-up handshake error in ProviderReplacedDuringHandshake
    xds_client_test.go:793: VERIFY follow-up handshake err: x509: certificate signed by unknown authority
    --- PASS: Test/ClientCredsProviderReplacedDuringHandshake (0.07s)
===== 3. mutated ClosedBeforeAcquire: replacement roots := prior roots (server_ca -> client_ca)
--- mutated line:
> 	root2 := makeRootProvider(t, "x509/client_ca_cert.pem")
    xds_client_test.go:852: ClientHandshake() failed: x509: certificate signed by unknown authority
    --- FAIL: Test/ClientCredsHandshakeInfoClosedBeforeAcquire (0.01s)
===== 4. mutated ProviderReplacedDuringHandshake: replacement roots := prior roots (client_ca -> server_ca)
--- mutated line:
> 	root2 := makeRootProvider(t, "x509/server_ca_cert.pem")
    xds_client_test.go:794: ClientHandshake() succeeded with replacement roots that do not trust the server
    --- FAIL: Test/ClientCredsProviderReplacedDuringHandshake (0.03s)
```

**Verdict: REFUTED.** `TestClientCredsHandshakeInfoClosedBeforeAcquire` asserts a follow-up handshake that *succeeds* with the replacement roots and *fails* (`x509: certificate signed by unknown authority`) with the prior roots — exactly the refute condition. `TestClientCredsProviderReplacedDuringHandshake` shows the mirror-image distinction.

## C4

**Claim.** On 002b71aa, `HandshakeInfo.Release` lets a release after the count reaches zero drive the count negative without reporting or rejecting it.

**Source (`~/wt/002b71aa/internal/credentials/xds/handshake_info.go`):**

```go
func (hi *HandshakeInfo) Release() {
	if hi == nil {
		return
	}
	if hi.refs.Add(-1) != 0 {
		return
	}
	hi.rootProvider.Close() ...
}
```

`internal/grpcsync.RefCounted` is not used by this branch (`grep -n grpcsync internal/credentials/xds/handshake_info.go` → no output; `git diff --stat cc234554 HEAD -- internal/grpcsync/` → empty).

**Method.** `verify/repro/c4_release_negative_refcount_test.go` (package `xds`, so it can read `hi.refs`) creates a HandshakeInfo with a close-counting provider, swaps grpclog for a buffer, releases the only valid reference, then performs one extra `Release()`.

```sh
cd ~/wt/002b71aa && cp ~/repos/grpc-go/verify/repro/c4_release_negative_refcount_test.go internal/credentials/xds/zz_verify_c4_test.go && go test ./internal/credentials/xds -run 'TestVerifyC4_' -count=1 -v; rm internal/credentials/xds/zz_verify_c4_test.go
```

```console
=== RUN   TestVerifyC4_ReleaseBelowZero
    zz_verify_c4_test.go:36: after NewHandshakeInfo: refs=1
    zz_verify_c4_test.go:39: after 1st Release (all valid refs released): refs=0 rootProvider.Close() calls=1
    zz_verify_c4_test.go:51: after extra Release: refs=-1 rootProvider.Close() calls=1 panicked=false grpclog output=""
    zz_verify_c4_test.go:52: Acquire() after extra Release: false
    zz_verify_c4_test.go:55: CONFIRMED: reference count fell to -1 after an unmatched Release with no panic, error, log diagnostic, or rejection
--- FAIL: TestVerifyC4_ReleaseBelowZero (0.00s)
```

**Contrast (audited branch, `internal/grpcsync/refcounted.go`):** `Decrement` logs `logger.Errorf("Refcount cannot be negative")` when the count goes below zero. Branch 53e1cbd8's `Release` panics (`"xds: HandshakeInfo.Release called without a reference"`). 002b71aa does neither.

**Verdict: CONFIRMED.** Impact reasoning: the count silently becomes `-1`; nothing is logged, returned or panicked. The observable consequence in this design is that an accounting bug elsewhere cannot be noticed: a stray `Release` while a handshake still holds a reference would close the providers underneath that handshake (count reaches 0 one step early) with no trace, and a stray release after zero leaves a permanently negative count that makes any later `Acquire` fail (`n <= 0`). It is a missing-diagnostic defect rather than a directly user-visible failure on the code paths as written (the production callers on this branch — `credentials/xds/xds.go:126 defer hi.Release()` and `clusterimpl.go:388/518` — appear balanced).

## C5

**Claim.** On b86f6a10, the server-side `XDSHandshakeInfo` lifecycle leaves the initial owner reference created by `NewHandshakeInfo` unreleased. Parts: (a) constructor ownership, (b) server lifecycle.

**Source (`~/wt/b86f6a10`).** `internal/credentials/xds/handshake_info.go`: `NewHandshakeInfo` … `hi.refs.Store(1)`; doc comment: "holds one reference on behalf of the caller, who owns the providers and must call Release to close them". `internal/xds/server/conn_wrapper.go`: `XDSHandshakeInfo()` builds the providers, stores them in `c.identityProvider`/`c.rootProvider` and returns `xdsinternal.NewHandshakeInfo(...)`; `connWrapper.Close()` calls `c.identityProvider.Close()` / `c.rootProvider.Close()` directly. `credentials/xds/xds.go` `ServerHandshake`: `hi, err := hiConn.XDSHandshakeInfo()` … uses `hi.ServerSideTLSConfig(ctx)`; the only `Release`/`Acquire` in the file are in `ClientHandshake` (`grep -nE 'XDSHandshakeInfo|Release|Acquire' credentials/xds/xds.go` → lines 116, 119, 126 (client) and 192, 197 (server, no Release)).

**Method.** `verify/repro/c5_server_owner_ref_unreleased_test.go` (package `server`) registers a close-counting `certprovider` plugin, builds a `connWrapper` with a filter chain carrying a security config, calls `XDSHandshakeInfo()` exactly as `ServerHandshake` does, runs the existing teardown `connWrapper.Close()`, then probes the HandshakeInfo with `Acquire()` (returns true only if a reference — the constructor's — is still held).

```sh
cd ~/wt/b86f6a10 && cp ~/repos/grpc-go/verify/repro/c5_server_owner_ref_unreleased_test.go internal/xds/server/zz_verify_c5_test.go && go test ./internal/xds/server -run 'TestVerifyC5_' -count=1 -v; rm internal/xds/server/zz_verify_c5_test.go
```

```console
=== RUN   TestVerifyC5_ConstructorOwnerReference
    zz_verify_c5_test.go:62: Acquire() right after NewHandshakeInfo: true (true => constructor created a live owner reference)
    zz_verify_c5_test.go:64: provider Close() calls with only the constructor's reference outstanding: 0
    zz_verify_c5_test.go:66: provider Close() calls after the explicit owner Release(): 1
--- PASS: TestVerifyC5_ConstructorOwnerReference (0.00s)
=== RUN   TestVerifyC5_ServerLifecycleLeavesOwnerRef
    zz_verify_c5_test.go:107: providers built by XDSHandshakeInfo(): 2
    zz_verify_c5_test.go:114: after connWrapper.Close(): provider[0].Close() calls = 1
    zz_verify_c5_test.go:114: after connWrapper.Close(): provider[1].Close() calls = 1
    zz_verify_c5_test.go:118: hi.Acquire() after teardown = true (true => the initial owner reference from NewHandshakeInfo is still held)
    zz_verify_c5_test.go:123: after releasing the leftover owner reference: underlying provider[0].Close() calls = 1 (a second Close() on the certprovider store wrapper only decrements its refCount, so the underlying count does not change)
    zz_verify_c5_test.go:123: after releasing the leftover owner reference: underlying provider[1].Close() calls = 1 (...)
    zz_verify_c5_test.go:125: CONFIRMED: server-side lifecycle (XDSHandshakeInfo -> connWrapper.Close) left the HandshakeInfo owner reference unreleased after teardown
--- FAIL: TestVerifyC5_ServerLifecycleLeavesOwnerRef (0.00s)
```

**Verdict: CONFIRMED (both parts).** (a) `NewHandshakeInfo` creates a live owner reference: `Acquire()` succeeds immediately and providers are only closed by an explicit `Release()`. (b) After `XDSHandshakeInfo()` + `connWrapper.Close()`, `Acquire()` still succeeds, i.e. the owner reference was never released; the server path closes providers by bypassing the reference count entirely. Impact reasoning: the reference-counting contract documented on the constructor is violated on every xDS server connection, so the count provides no protection on the server side (a `connWrapper.Close()` racing an in-flight `ServerHandshake` still closes providers underneath it, exactly the class of bug the branch set out to fix on the client side), and a future caller that does honour the contract and calls `Release()` would issue a second `Close()` to the store wrapper, driving *its* refCount negative (observed: underlying Close count stayed at 1 because the wrapper absorbed it). No resource leak was observed — the providers are closed and the HandshakeInfo is garbage-collected.

## C6

**Claim.** On 53e1cbd8, `ClientHandshake` enters an unbounded tight retry loop when ownership acquisition fails while the same retired HandshakeInfo snapshot remains published. Parts: (a) retry mechanism, (b) persistent trigger.

**Source (`~/wt/53e1cbd8/credentials/xds/xds.go:119-127`):**

```go
for {
	hi = hiPtr.Load()
	if hi == nil {
		return c.fallback.ClientHandshake(ctx, authority, rawConn)
	}
	if hi.Acquire() {
		break
	}
}
```

`HandshakeInfo.Acquire()` returns false once `Close()` has set `hi.closed` (`internal/credentials/xds/handshake_info.go:149-157`). The only production publisher is `clusterimpl.updateHandshakeInfo` (`if old := b.xdsHIPtr.Swap(hi); old != nil { old.Close() }`) and `clusterImplBalancer.Close` (`if hi := b.xdsHIPtr.Swap(nil); hi != nil { hi.Close() }`).

### C6(a) retry mechanism

`verify/repro/c6_tight_retry_loop_test.go` (package `xds` in `credentials/xds`) publishes a HandshakeInfo, calls `Close()` on it while leaving it published, then invokes `ClientHandshake` with a 300 ms context.

```sh
cd ~/wt/53e1cbd8 && cp ~/repos/grpc-go/verify/repro/c6_tight_retry_loop_test.go credentials/xds/zz_verify_c6_test.go && go test ./credentials/xds -run 'TestVerifyC6_' -count=1 -v -timeout 60s; rm credentials/xds/zz_verify_c6_test.go
```

```console
=== RUN   TestVerifyC6_RetiredSnapshotStillPublished
    zz_verify_c6_test.go:44: retired snapshot still published: hiPtr.Load()==hi is true; hi.Acquire()=false
    zz_verify_c6_test.go:79: ClientHandshake has NOT returned 2.008394762s after start (ctx.Err()=context deadline exceeded); user CPU consumed by the process in that window: 2.013s
    zz_verify_c6_test.go:80: handshake goroutine stack:
        goroutine 9 [runnable]:
        google.golang.org/grpc/internal/credentials/xds.(*HandshakeInfo).Acquire(0xc00019f730?)
        	/home/ubuntu/wt/53e1cbd8/internal/credentials/xds/handshake_info.go:153 +0x88
        google.golang.org/grpc/credentials/xds.(*credsImpl).ClientHandshake(0xc000012450, ...)
        	/home/ubuntu/wt/53e1cbd8/credentials/xds/xds.go:126 +0x3aa
    zz_verify_c6_test.go:81: CONFIRMED: ClientHandshake did not return within 2s although the context expired after 300ms; it is spinning on hiPtr.Load()/Acquire()
    zz_verify_c6_test.go:87: after hiPtr.Store(nil), ClientHandshake returned: err=context deadline exceeded
--- FAIL: TestVerifyC6_RetiredSnapshotStillPublished (2.01s)
```

Observed: no return, no wait (2.013 s of user CPU in a 2.008 s window = one core spinning), no reaction to the expired context; the call only returns once the publication changes (`hiPtr.Store(nil)`). **Part (a): CONFIRMED.**

### C6(b) persistent trigger

`verify/repro/c6_publisher_retire_order_test.go` (package `clusterimpl`) drives the production publisher through two security-config updates and `Close()` and checks after each step whether the just-retired snapshot is still the published one.

```sh
cd ~/wt/53e1cbd8 && cp ~/repos/grpc-go/verify/repro/c6_publisher_retire_order_test.go internal/xds/balancer/clusterimpl/zz_verify_c6b_test.go && go test ./internal/xds/balancer/clusterimpl -run 'Test/VerifyC6_' -count=1 -v; rm internal/xds/balancer/clusterimpl/zz_verify_c6b_test.go
```

```console
    zz_verify_c6b_test.go:77: after update 1: published hi1 Acquire()=true
    zz_verify_c6b_test.go:86: after update 2: published==hi1 is false; hi1.Acquire()=false (false => retired); published hi2 Acquire()=true
    zz_verify_c6b_test.go:93: after balancer Close(): published==nil is true; hi2.Acquire()=false
--- PASS: Test (0.01s)
ok  	google.golang.org/grpc/internal/xds/balancer/clusterimpl	0.020s
```

Observed: the production publisher always unpublishes (`Swap`) *before* it retires (`Close`), so through `clusterimpl` a retired snapshot is never left published; a handshake that loses the race re-`Load`s and gets the live successor or `nil`. Nothing in the `HandshakeInfo` API prevents a caller from `Close()`-ing a published snapshot (as C6(a) does directly), but no production caller on this branch does so. **Part (b): REFUTED** for the production publisher.

**Verdict: CONFIRMED (1 of 2 parts held).** The Confirm procedure ("keep a retired snapshot published, make acquisition fail") reproduces an unbounded, context-ignoring busy loop in `ClientHandshake`; the defect is real and latent in `credentials/xds`. Its only production publisher does not currently trigger it, so the impact today is a fragility (one misordered `Close`/`Swap` in a future change, or any other publisher, turns into a 100 %-CPU hang of every handshake goroutine that ignores its deadline) rather than an observed outage.
