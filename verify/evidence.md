## C1

Claim target: branch `evalon/grpc-go-xd-ab2ea750` of repository `kaitranntt-evals/grpc-go-xds-certificate-provider-closure-race` (a different repository from `origin`). Adjudicated in a separate worktree.

### Setup

```sh
cd ~/repos/grpc-go
git remote add claimrepo https://github.com/kaitranntt-evals/grpc-go-xds-certificate-provider-closure-race.git
git fetch claimrepo evalon/grpc-go-xd-ab2ea750
git worktree add ~/repos/claim-c1 claimrepo/evalon/grpc-go-xd-ab2ea750
cd ~/repos/claim-c1
git log --oneline cc234554fb363aea445a838b341bb8a65c8305b0..HEAD
# 594d8266 xds: keep replaced Cluster certificate providers alive until in-flight handshakes finish
git diff --stat cc234554fb363aea445a838b341bb8a65c8305b0 HEAD
#  credentials/xds/xds.go                             |  12 +-
#  internal/credentials/xds/handshake_info.go         |  89 +++++-
#  .../xds/handshake_info_ownership_test.go           | 253 +++++++++++++++
#  internal/xds/balancer/clusterimpl/clusterimpl.go   |  38 +--
#  internal/xds/balancer/clusterimpl/security_test.go | 347 +++++++++++++++++++++
#  .../clusterimpl/tests/clusterimpl_security_test.go | 112 +++++++
# Eval fixtures provisioned byte-exact from eval_tests.zip:
mkdir -p .evaltools
cp ~/eval_tests/tests/candidate_test_inventory.go .evaltools/
cp ~/eval_tests/tests/eval_handshake_lifetime_test.go internal/xds/balancer/clusterimpl/tests/
cp ~/eval_tests/tests/run_eval_xds_test_group.sh ~/eval_tests/tests/run_candidate_tests.sh test/
go build ./... && go vet ./internal/xds/balancer/clusterimpl/... ./internal/credentials/xds/... ./credentials/xds/...
# BUILD_OK / VET_OK
```

### Inventory of added or modified tests (from the eval's own inventory helper)

```console
$ bash ./test/run_candidate_tests.sh
==> Detected substantive agent-authored tests:
    internal/credentials/xds/handshake_info_ownership_test.go	Test/TestOwnedHandshakeInfo_ReplacedDuringLoad
    internal/credentials/xds/handshake_info_ownership_test.go	Test/TestOwnedHandshakeInfo_AcquireAfterRelease
    internal/credentials/xds/handshake_info_ownership_test.go	Test/TestUnownedHandshakeInfo_AcquireRelease
    internal/xds/balancer/clusterimpl/security_test.go	Test/TestHandleSecurityConfig_ReplacedDuringRootLoad
    internal/xds/balancer/clusterimpl/security_test.go	Test/TestHandleSecurityConfig_ReplacedBeforeRootLoad
    internal/xds/balancer/clusterimpl/tests/clusterimpl_security_test.go	Test/TestSecurityConfigUpdate_ReplacedRootsRejectServer
[PASS] Test/TestOwnedHandshakeInfo_ReplacedDuringLoad
[PASS] Test/TestOwnedHandshakeInfo_AcquireAfterRelease
[PASS] Test/TestUnownedHandshakeInfo_AcquireRelease
[PASS] Test/TestHandleSecurityConfig_ReplacedDuringRootLoad
[PASS] Test/TestHandleSecurityConfig_ReplacedBeforeRootLoad
[PASS] Test/TestSecurityConfigUpdate_ReplacedRootsRejectServer
```

Six tests total; only three of them involve a follow-up attempt after a replacement at all:

- `TestSecurityConfigUpdate_ReplacedRootsRejectServer` (e2e, `internal/xds/balancer/clusterimpl/tests/clusterimpl_security_test.go:791-895`). After a successful mTLS RPC it updates the Cluster to an untrusted-roots provider instance, then loops `EmptyCall` / `EnterIdleModeForTesting` until an RPC fails, asserting only `status.Code(err) == codes.Unavailable`, then `AwaitState(TransientFailure)`. No assertion on the error text, no per-attempt provider signal.
- `TestHandleSecurityConfig_ReplacedDuringRootLoad` (unit, `internal/xds/balancer/clusterimpl/security_test.go:219-299`). "Handshake" = `xds.AcquireHandshakeInfo` + `hi.ClientSideTLSConfig(ctx, "")` (helper only, no connection); providers are `testProvider`s returning an empty `&certprovider.KeyMaterial{}`; the resulting `*tls.Config` is discarded (`_, err = hi.ClientSideTLSConfig(...)`). Follow-up assertions: `r.err == nil` and two receives from a shared, provider-scoped `loadStarted` channel equal `newRoot.name`, `newIdentity.name`. The channel is never asserted empty before the follow-up is triggered.
- `TestOwnedHandshakeInfo_ReplacedDuringLoad` (unit, `internal/credentials/xds/handshake_info_ownership_test.go:100-183`). No Cluster replacement (manual `hiPtr.Swap` + `Release`); follow-up = `AcquireHandshakeInfo` + `ClientSideTLSConfig` (helper only), asserts `hi.rootProvider == newRoot` and `err == nil`; empty `KeyMaterial{}`; `*tls.Config` discarded.

The other three (`..._ReplacedBeforeRootLoad`, `..._AcquireAfterRelease`, `TestUnownedHandshakeInfo_AcquireRelease`) only assert provider close/refcount lifetime and never make a follow-up attempt after a replacement.

### Baseline: all six tests and the eval fixture pass on the branch

```console
$ go test -count=1 -v -run '^Test$/^(HandleSecurityConfig_ReplacedDuringRootLoad|HandleSecurityConfig_ReplacedBeforeRootLoad)$' ./internal/xds/balancer/clusterimpl/
    --- PASS: Test/HandleSecurityConfig_ReplacedBeforeRootLoad (0.00s)
    --- PASS: Test/HandleSecurityConfig_ReplacedDuringRootLoad (0.00s)
ok  	google.golang.org/grpc/internal/xds/balancer/clusterimpl	0.008s
$ go test -count=1 -v -run '^Test$/^(OwnedHandshakeInfo_ReplacedDuringLoad|OwnedHandshakeInfo_AcquireAfterRelease|UnownedHandshakeInfo_AcquireRelease)$' ./internal/credentials/xds/
    --- PASS: Test/OwnedHandshakeInfo_AcquireAfterRelease (0.00s)
    --- PASS: Test/OwnedHandshakeInfo_ReplacedDuringLoad (0.01s)
    --- PASS: Test/UnownedHandshakeInfo_AcquireRelease (0.00s)
ok  	google.golang.org/grpc/internal/credentials/xds	0.016s
$ go test -count=1 -v -run '^Test$/^SecurityConfigUpdate_ReplacedRootsRejectServer$' ./internal/xds/balancer/clusterimpl/tests/
    --- PASS: Test/SecurityConfigUpdate_ReplacedRootsRejectServer (0.03s)
ok  	google.golang.org/grpc/internal/xds/balancer/clusterimpl/tests	0.037s
$ go test -count=1 -v -run '^TestEval_SecurityConfigUpdate_ActiveHandshakeKeepsProvider$' ./internal/xds/balancer/clusterimpl/tests/
--- PASS: TestEval_SecurityConfigUpdate_ActiveHandshakeKeepsProvider (0.04s)
ok  	google.golang.org/grpc/internal/xds/balancer/clusterimpl/tests	0.052s
```

### What the e2e test actually observes but does not assert

Temporarily adding `t.Logf("OBSERVED follow-up RPC error: %v", err)` right after the `codes.Unavailable` check (test file restored afterwards):

```console
$ go test -count=1 -v -run '^Test$/^SecurityConfigUpdate_ReplacedRootsRejectServer$' ./internal/xds/balancer/clusterimpl/tests/ | grep -E 'OBSERVED|--- '
    clusterimpl_security_test.go:889: OBSERVED follow-up RPC error: rpc error: code = Unavailable desc = connection error: desc = "transport: authentication handshake failed: x509: certificate signed by unknown authority"
    --- PASS: Test/SecurityConfigUpdate_ReplacedRootsRejectServer (0.08s)
```

The distinguishing `x509: certificate signed by unknown authority` text is available on the error but the test only checks the status code. (The eval fixture asserts `strings.Contains(err.Error(), "x509: certificate signed by unknown authority")` and drains `providerB.entered` before the follow-up attempt — `eval_handshake_lifetime_test.go:327-361`.)

### The e2e test's failing follow-up connection comes from a freshly built balancer, not from the in-place replacement

Instrumentation patch `verify/repro/instr_log_handle_security_config.patch` (prints balancer pointer, whether the previous HandshakeInfo was the fallback one, and the root instance name on every `handleSecurityConfig`), plus stderr prints in the test loop (test file restored afterwards):

```console
$ git apply verify/repro/instr_log_handle_security_config.patch
$ go test -count=1 -v -run '^Test$/^SecurityConfigUpdate_ReplacedRootsRejectServer$' ./internal/xds/balancer/clusterimpl/tests/ 2>&1 | grep -E 'INSTR|--- '
INSTR handleSecurityConfig balancer=0xc000412ea0 prevIsFallback=true root=client-side-certificate-provider-instance
INSTR follow-up EmptyCall attempt
INSTR follow-up EmptyCall succeeded; entering idle
INSTR follow-up EmptyCall attempt
INSTR handleSecurityConfig balancer=0xc0001785a0 prevIsFallback=true root=untrusted-roots-certificate-provider-instance
INSTR follow-up EmptyCall error: rpc error: code = Unavailable desc = connection error: desc = "transport: authentication handshake failed: x509: certificate signed by unknown authority"
    --- PASS: Test/SecurityConfigUpdate_ReplacedRootsRejectServer (0.08s)
$ go test -count=5 -v -run '^Test$/^SecurityConfigUpdate_ReplacedRootsRejectServer$' ./internal/xds/balancer/clusterimpl/tests/ 2>&1 | grep INSTR
INSTR handleSecurityConfig balancer=0xc000728fc0 prevIsFallback=true root=client-side-certificate-provider-instance
INSTR handleSecurityConfig balancer=0xc000914a20 prevIsFallback=true root=untrusted-roots-certificate-provider-instance
INSTR handleSecurityConfig balancer=0xc0003939e0 prevIsFallback=true root=client-side-certificate-provider-instance
INSTR handleSecurityConfig balancer=0xc0007925a0 prevIsFallback=true root=untrusted-roots-certificate-provider-instance
INSTR handleSecurityConfig balancer=0xc0007285a0 prevIsFallback=true root=client-side-certificate-provider-instance
INSTR handleSecurityConfig balancer=0xc0007285a0 prevIsFallback=false root=untrusted-roots-certificate-provider-instance
INSTR handleSecurityConfig balancer=0xc000914b40 prevIsFallback=true root=untrusted-roots-certificate-provider-instance
INSTR handleSecurityConfig balancer=0xc0006d8480 prevIsFallback=true root=client-side-certificate-provider-instance
INSTR handleSecurityConfig balancer=0xc000393680 prevIsFallback=true root=untrusted-roots-certificate-provider-instance
INSTR handleSecurityConfig balancer=0xc000728ea0 prevIsFallback=true root=client-side-certificate-provider-instance
INSTR handleSecurityConfig balancer=0xc000792a20 prevIsFallback=true root=untrusted-roots-certificate-provider-instance
$ git apply -R verify/repro/instr_log_handle_security_config.patch
```

In 4 of 5 runs the original `cluster_impl` balancer never received the replacement at all (only one `handleSecurityConfig` per balancer pointer, each with `prevIsFallback=true`); in the remaining run it did (`prevIsFallback=false`), but the connection that fails still belongs to a new balancer (`0xc000914b40`) created by `EnterIdleModeForTesting`, which gets the untrusted config as its *first* config. The follow-up attempt is therefore never tied to a replacement of an existing security configuration.

### Mutation M1 — replacement publishes a config with no root provider (non-trust failure)

`verify/repro/m1_replacement_breaks_handshake.patch`: in `clusterimpl.handleSecurityConfig`, every security config after the first one in the process is published with `rootProvider = nil`; handshakes then fail with `xds: CertificateProvider to fetch trusted roots is missing`, which is not a certificate trust failure and never consults the replacement roots.

```console
$ git apply verify/repro/m1_replacement_breaks_handshake.patch && go build ./...
$ go test -count=1 -v -run '^Test$/^SecurityConfigUpdate_ReplacedRootsRejectServer$' ./internal/xds/balancer/clusterimpl/tests/ | grep -E 'OBSERVED|--- '   # with the temporary t.Logf
    clusterimpl_security_test.go:889: OBSERVED follow-up RPC error: rpc error: code = Unavailable desc = connection error: desc = "transport: authentication handshake failed: xds: CertificateProvider to fetch trusted roots is missing, cannot perform TLS handshake. Please check configuration on the management server"
    --- PASS: Test/SecurityConfigUpdate_ReplacedRootsRejectServer (0.03s)
$ go test -count=1 -v -run '^TestEval_SecurityConfigUpdate_ActiveHandshakeKeepsProvider$' ./internal/xds/balancer/clusterimpl/tests/
    eval_handshake_lifetime_test.go:353: timed out waiting for replacement provider KeyMaterial on the follow-up RPC: context deadline exceeded
--- FAIL: TestEval_SecurityConfigUpdate_ActiveHandshakeKeepsProvider (3.02s)
$ go test -count=1 -v -run '^Test$/^HandleSecurityConfig_ReplacedDuringRootLoad$' ./internal/xds/balancer/clusterimpl/
    security_test.go:259: Handshake with new security configuration failed: xds: CertificateProvider to fetch trusted roots is missing, cannot perform TLS handshake. Please check configuration on the management server
    --- FAIL: Test/HandleSecurityConfig_ReplacedDuringRootLoad (0.00s)
$ go test -count=1 -v -run '^Test$/^HandleSecurityConfig_ReplacedBeforeRootLoad$' ./internal/xds/balancer/clusterimpl/
    --- PASS: Test/HandleSecurityConfig_ReplacedBeforeRootLoad (0.00s)
$ go test -count=1 -v -run '^Test$/^(OwnedHandshakeInfo_ReplacedDuringLoad|OwnedHandshakeInfo_AcquireAfterRelease|UnownedHandshakeInfo_AcquireRelease)$' ./internal/credentials/xds/
    --- PASS: Test/OwnedHandshakeInfo_AcquireAfterRelease (0.00s)
    --- PASS: Test/OwnedHandshakeInfo_ReplacedDuringLoad (0.01s)
    --- PASS: Test/UnownedHandshakeInfo_AcquireRelease (0.00s)
$ git apply -R verify/repro/m1_replacement_breaks_handshake.patch
```

The e2e test stays green although the follow-up connection never used the replacement roots — its `codes.Unavailable` assertion does not distinguish a certificate trust failure from any other handshake failure. The eval fixture catches it. `TestHandleSecurityConfig_ReplacedDuringRootLoad` also fails under M1 (its follow-up helper call needs a root provider), but see M2 for what it cannot see.

### Mutation M2 — validation roots pinned to the first load (replacement providers consulted but never govern)

`verify/repro/m2_pin_first_roots.patch`: in `HandshakeInfo.ClientSideTLSConfig`, the `KeyMaterial` returned by the first root-provider load in the process is cached and used for `RootCAs`/`VerifyPeerCertificate` on every later handshake; the replacement provider's `KeyMaterial` is still called.

```console
$ git apply verify/repro/m2_pin_first_roots.patch && go build ./...
$ go test -count=1 -v -run '^Test$/^(HandleSecurityConfig_ReplacedDuringRootLoad|HandleSecurityConfig_ReplacedBeforeRootLoad)$' ./internal/xds/balancer/clusterimpl/
    --- PASS: Test/HandleSecurityConfig_ReplacedBeforeRootLoad (0.00s)
    --- PASS: Test/HandleSecurityConfig_ReplacedDuringRootLoad (0.00s)
ok  	google.golang.org/grpc/internal/xds/balancer/clusterimpl	0.009s
$ go test -count=1 -v -run '^Test$/^(OwnedHandshakeInfo_ReplacedDuringLoad|OwnedHandshakeInfo_AcquireAfterRelease|UnownedHandshakeInfo_AcquireRelease)$' ./internal/credentials/xds/
    --- PASS: Test/OwnedHandshakeInfo_AcquireAfterRelease (0.00s)
    --- PASS: Test/OwnedHandshakeInfo_ReplacedDuringLoad (0.01s)
    --- PASS: Test/UnownedHandshakeInfo_AcquireRelease (0.00s)
ok  	google.golang.org/grpc/internal/credentials/xds	0.018s
$ go test -count=1 -v -run '^Test$/^SecurityConfigUpdate_ReplacedRootsRejectServer$' ./internal/xds/balancer/clusterimpl/tests/
    clusterimpl_security_test.go:887: EmptyCall() failed with code DeadlineExceeded, want Unavailable
    --- FAIL: Test/SecurityConfigUpdate_ReplacedRootsRejectServer (5.01s)
$ go test -count=1 -v -run '^TestEval_SecurityConfigUpdate_ActiveHandshakeKeepsProvider$' ./internal/xds/balancer/clusterimpl/tests/
    eval_handshake_lifetime_test.go:357: Follow-up RPC error = <nil>, want x509 unknown authority
--- FAIL: TestEval_SecurityConfigUpdate_ActiveHandshakeKeepsProvider (0.04s)
$ git apply -R verify/repro/m2_pin_first_roots.patch
```

All five unit tests stay green while replacement roots demonstrably do not govern anything: their providers return an empty `KeyMaterial{}` and the produced `*tls.Config` is discarded, so they can only observe that a provider method was invoked and that no error came back. The e2e test and the eval fixture both catch M2.

### Combined replay

`bash verify/repro/c1_test_strength.sh` from the claim-branch checkout (with the fixture copied in) reproduces baseline + M1 + M2 in one go; per-test results per mutation:

| test | baseline | M1 (non-trust failure) | M2 (roots pinned) |
|---|---|---|---|
| `TestSecurityConfigUpdate_ReplacedRootsRejectServer` (e2e) | PASS | **PASS** | FAIL |
| `TestHandleSecurityConfig_ReplacedDuringRootLoad` | PASS | FAIL | **PASS** |
| `TestHandleSecurityConfig_ReplacedBeforeRootLoad` | PASS | PASS | PASS |
| `TestOwnedHandshakeInfo_ReplacedDuringLoad` | PASS | PASS | PASS |
| `TestOwnedHandshakeInfo_AcquireAfterRelease` | PASS | PASS | PASS |
| `TestUnownedHandshakeInfo_AcquireRelease` | PASS | PASS | PASS |
| `TestEval_SecurityConfigUpdate_ActiveHandshakeKeepsProvider` (fixture, for contrast) | PASS | FAIL | FAIL |

### Adjudication

Confirm condition: no individual added/modified test strengthens either form of proof.

- Form (a) — distinguishable certificate trust failure that would not occur under the prior roots: the only test with a real connection (`TestSecurityConfigUpdate_ReplacedRootsRejectServer`) asserts `codes.Unavailable` + `TransientFailure`, a generic transport outcome; M1 shows it stays green when the follow-up fails for a reason unrelated to the replacement roots, and the instrumentation shows the failing connection is made by a fresh balancer bootstrapped with the new config, not a connection after an in-place Cluster security replacement. No unit test performs a connection or has real roots.
- Form (b) — follow-up connection outcome tied to replacement KeyMaterial for that same attempt: the two unit tests that make a follow-up attempt do so through helper-only configuration loading (`ClientSideTLSConfig`), with empty `KeyMaterial{}` and the returned config discarded; M2 shows they stay green when replacement roots never govern. The provider-scoped `loadStarted` channel in `TestHandleSecurityConfig_ReplacedDuringRootLoad` is not asserted empty/reset before the follow-up trigger. `TestOwnedHandshakeInfo_ReplacedDuringLoad` has attempt identity (`hi.rootProvider == newRoot`) but no Cluster replacement, no connection, and empty KeyMaterial.

Verdict: **CONFIRMED**. Impact reasoning: the solution itself is not shown wrong here (the eval fixture passes at baseline), but the added tests would keep passing under a regression where a Cluster security update breaks every later handshake for an unrelated reason (M1, e2e test) or where replacement roots are loaded but ignored (M2, all unit tests); a reader cannot use them as evidence that "a later connection rejects a server certificate the replacement roots do not trust".
