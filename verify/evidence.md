## Evidence — run `v-3b236ead`

Audited branch: `grpc-go-xds-certificate-provider-closure-race-perfect` (HEAD `3483b320`, base `cc234554`), checked out as
`verify/grpc-go-xds-certificate-provider-closure-race-v-3b236ead`. Claim-target branches were fetched from
`https://github.com/kaitranntt-evals/grpc-go-xds-certificate-provider-closure-race.git` (added as remote `evalon`) into
separate worktrees under `~/wt/<suffix>`:

| worktree | branch | HEAD |
|---|---|---|
| `~/wt/d150a14b` | `evalon/grpc-go-xd-d150a14b` | `2a08965d` |
| `~/wt/808a7060` | `evalon/grpc-go-xd-808a7060` | `1d0b600a` |
| `~/wt/70cc0d6c` | `evalon/grpc-go-xd-70cc0d6c` | `4ed1b65f` |
| `~/wt/833acc28` | `evalon/grpc-go-xd-833acc28` | `4c77c262` |
| `~/wt/d030c2f0` | `evalon/grpc-go-xd-d030c2f0` | `0e0f77dc` |

Toolchain: `go version go1.25.7 linux/amd64`. All repro files live in `verify/repro/`; each Go test file carries a
`//go:build ignore` line so the module still builds, and its header comment says where to copy it and how to run it.

```sh
cd ~/repos/grpc-go
git fetch origin grpc-go-xds-certificate-provider-closure-race-perfect
git checkout -b verify/grpc-go-xds-certificate-provider-closure-race-v-3b236ead origin/grpc-go-xds-certificate-provider-closure-race-perfect
git remote add evalon https://github.com/kaitranntt-evals/grpc-go-xds-certificate-provider-closure-race.git
git fetch evalon evalon/grpc-go-xd-d150a14b evalon/grpc-go-xd-808a7060 evalon/grpc-go-xd-70cc0d6c evalon/grpc-go-xd-833acc28 evalon/grpc-go-xd-d030c2f0
for b in d150a14b 808a7060 70cc0d6c 833acc28 d030c2f0; do git worktree add ~/wt/$b evalon/evalon/grpc-go-xd-$b; done
```

---

## C1

Claim: after ownership acquisition for the selected validation-root state fails, the client handshake reaches its next
KeyMaterial read or terminates **without first reloading** the published handshake state.

### C1 — branch `evalon/grpc-go-xd-d150a14b` → REFUTED

Production path (`internal/credentials/xds/handshake_info.go`, `AcquireHandshakeInfo`): `hi := hiPtr.Load()`; if
`hi.tryRef()` fails it executes `if hiPtr.Load() == hi { return hi, noop }` and otherwise loops (re-load + re-try). The
owner (`clusterImplBalancer.storeHandshakeInfo`) does `old := b.xdsHIPtr.Swap(hi); old.Release()`.

**Observation 1 — reload is observable through behaviour.** Repro
`verify/repro/c1_d150a14b_reload_after_failed_hold_test.go` (copied to `internal/credentials/xds/`) races
`AcquireHandshakeInfo` against an owner doing `Swap` + `Release`, then reads KeyMaterial from the returned provider
(the provider reports an error once closed). It runs the same stress against a test-local variant `acquireNoReload`
that omits the re-read, to show the stress can tell the two apart.

```sh
cd ~/wt/d150a14b && go test ./internal/credentials/xds -run 'TestVerify_C1' -count=1 -v
```

```console
=== RUN   TestVerify_C1_d150_ReloadAfterFailedHold
    verify_c1_test.go:99: [AcquireHandshakeInfo (production)] acquisitions=240000 owner swaps=30751 KeyMaterial-read-from-CLOSED-provider=0
    verify_c1_test.go:99: [acquireNoReload (hypothesised bug)] acquisitions=240000 owner swaps=33381 KeyMaterial-read-from-CLOSED-provider=107
    verify_c1_test.go:116: production: 0 closed reads; mutant without reload: 107 closed reads -> reload after failed hold is observed
--- PASS: TestVerify_C1_d150_ReloadAfterFailedHold (0.06s)
=== RUN   TestVerify_C1_d150_OwnerClosedNoReplacement
    verify_c1_test.go:133: provider closed before acquire: true; returned same released hi: true
    verify_c1_test.go:135: ClientSideTLSConfig err = xds: fetching trusted roots from CertificateProvider failed: provider instance is closed; KeyMaterial calls after Close = 1
--- PASS: TestVerify_C1_d150_OwnerClosedNoReplacement (0.00s)
PASS
ok  	google.golang.org/grpc/internal/credentials/xds	0.067s
```

Five more runs (`-count=5`): production 0/0/0/0/0 closed reads; no-reload variant 128/128/129/94/118. `-race` run of the
same tests: `ok  google.golang.org/grpc/internal/credentials/xds 1.609s`.

**Observation 2 — through the real `credsImpl.ClientHandshake`.** Repro
`verify/repro/c1_d150a14b_client_handshake_stress_test.go` (copied to `credentials/xds/`): 8 goroutines × 20000
`ClientHandshake` calls over `net.Pipe` against an owner doing `Swap`+`Release`; the provider returns a distinct error
depending on whether it was already closed when KeyMaterial was read.

```sh
cd ~/wt/d150a14b && go test ./credentials/xds -run 'TestVerify_C1_ClientHandshakeStress' -count=3 -v
```

```console
    verify_c1_stress_test.go:98: handshakes=160000 owner swaps=155926
    verify_c1_stress_test.go:99: KeyMaterial read from OPEN (held) provider: 160000
    verify_c1_stress_test.go:100: KeyMaterial read from CLOSED (unheld) provider: 0
--- PASS: TestVerify_C1_ClientHandshakeStress (0.12s)
    verify_c1_stress_test.go:98: handshakes=160000 owner swaps=226799
    verify_c1_stress_test.go:99: KeyMaterial read from OPEN (held) provider: 160000
    verify_c1_stress_test.go:100: KeyMaterial read from CLOSED (unheld) provider: 0
--- PASS: TestVerify_C1_ClientHandshakeStress (0.13s)
    verify_c1_stress_test.go:98: handshakes=160000 owner swaps=196631
    verify_c1_stress_test.go:99: KeyMaterial read from OPEN (held) provider: 160000
    verify_c1_stress_test.go:100: KeyMaterial read from CLOSED (unheld) provider: 0
--- PASS: TestVerify_C1_ClientHandshakeStress (0.12s)
ok  	google.golang.org/grpc/credentials/xds	0.383s
```

Reading: after a failed hold the handshake **does** re-read the pointer and picks up the replacement (0 closed reads in
720 000 acquisitions / 480 000 real handshakes, versus ~100 per 240 000 for the variant without the re-read). The only
path that reads KeyMaterial from a closed provider is the owner-shut-down case (`clusterImplBalancer.Close()` releases
without publishing a replacement, so the re-read finds the same pointer); that read happens *after* the re-read and the
handshake fails with `provider instance is closed`, which is the expected outcome on a closed balancer. The claim's
"without first reloading" does not hold on this branch.

### C1 — branch `evalon/grpc-go-xd-808a7060` → CONFIRMED

Production path: `credentials/xds/xds.go` `ClientHandshake` loops `hi = hiPtr.Load(); if release, ok = hi.Acquire(); ok
{ break }` (this first-level failure **does** reload). But `HandshakeInfo.ClientSideTLSConfig`
(`internal/credentials/xds/handshake_info.go:239-242`) performs a **second** `hi.Acquire()`, and
`Acquire` (`handshake_info.go:147`) returns `false` whenever `hi.retired && hi.hasProviders()`. `ClientSideTLSConfig` has no
pointer to reload; it returns `errors.New("xds: security configuration was replaced before the TLS handshake could
start")` and `ClientHandshake` returns that error. The owner (`swapHandshakeInfo`) sets `retired` via
`old := b.xdsHIPtr.Swap(hi); old.Close()`.

Repro `verify/repro/c2_808a7060_second_acquire_aborts_test.go` (copied to `internal/credentials/xds/`):

```sh
cd ~/wt/808a7060 && go test ./internal/credentials/xds -run 'TestVerify_' -count=1 -v
```

```console
=== RUN   TestVerify_C2_SecondAcquireRejectsRetiredHandshakeInfo
    verify_c2_test.go:52: after owner Close(): provider Close() calls = 0 (expect 0, handshake still holds a ref)
    verify_c2_test.go:56: ClientSideTLSConfig() -> cfg=false err=xds: security configuration was replaced before the TLS handshake could start
    verify_c2_test.go:57: root provider KeyMaterial() calls = 0
    verify_c2_test.go:64: CONFIRMED: handshake holding a valid ref aborted with "xds: security configuration was replaced before the TLS handshake could start" before reading KeyMaterial
--- PASS: TestVerify_C2_SecondAcquireRejectsRetiredHandshakeInfo (0.00s)
=== RUN   TestVerify_C1_808a_AbortWithoutReload
    verify_c2_test.go:94: ClientSideTLSConfig(old) err = xds: security configuration was replaced before the TLS handshake could start
    verify_c2_test.go:95: old KeyMaterial calls = 0, new KeyMaterial calls = 0
--- PASS: TestVerify_C1_808a_AbortWithoutReload (0.00s)
PASS
ok  	google.golang.org/grpc/internal/credentials/xds	0.003s
```

In `TestVerify_C1_808a_AbortWithoutReload` a fresh replacement `HandshakeInfo` *is* published in the pointer, yet the
handshake terminates with the error: neither the old nor the replacement provider's KeyMaterial is read, and the
published state is never re-read. The end-to-end frequency of this path through the real `ClientHandshake` is in the
C2 section below (22 614 of 160 000 handshakes).

---

## C2 → CONFIRMED (branch `evalon/grpc-go-xd-808a7060`)

Claim: a handshake that successfully acquired the old state aborts before reading its KeyMaterial when replacement
retires that state, because `ClientSideTLSConfig` performs another acquisition that rejects the retired state.

Unit-level demonstration: see `TestVerify_C2_SecondAcquireRejectsRetiredHandshakeInfo` output in the C1/808a7060 section
above — sequence `hi.Acquire()` (ok) → `hi.Close()` (owner retires; provider **not** closed, `Close() calls = 0`) →
`hi.ClientSideTLSConfig()` → error, `KeyMaterial() calls = 0`.

End-to-end through the production entry point. Repro `verify/repro/c2_808a7060_client_handshake_stress_test.go`
(copied to `credentials/xds/`): real `credsImpl.ClientHandshake` over `net.Pipe`, provider's KeyMaterial returns a
sentinel error so every handshake ends quickly; an owner goroutine keeps doing `hiPtr.Swap(new); old.Close()`.

```sh
cd ~/wt/808a7060 && go test ./credentials/xds -run 'TestVerify_C2_ClientHandshakeStress' -count=1 -v
```

```console
=== RUN   TestVerify_C2_ClientHandshakeStress
    verify_c2_stress_test.go:97: handshakes=160000  owner swaps=206991
    verify_c2_stress_test.go:98: reached KeyMaterial (sentinel err): 137386
    verify_c2_stress_test.go:99: aborted by second Acquire on retired state ("replaced before the TLS handshake could start"): 22614
    verify_c2_stress_test.go:100: other: 0
--- PASS: TestVerify_C2_ClientHandshakeStress (0.19s)
PASS
ok  	google.golang.org/grpc/credentials/xds	0.195s
```

Control — the branch's own tests and the eval fixture are all green on this branch, i.e. the defect is invisible to them
(the fixture blocks inside KeyMaterial, which is *after* the second Acquire):

```sh
cd ~/wt/808a7060 && cp ~/eval_tests/tests/eval_handshake_lifetime_test.go internal/xds/balancer/clusterimpl/tests/ \
  && go test ./internal/xds/balancer/clusterimpl/tests -run '^TestEval_SecurityConfigUpdate_ActiveHandshakeKeepsProvider$' -count=3 -v
go test ./internal/credentials/xds ./credentials/xds ./internal/xds/balancer/clusterimpl/... -count=1
```

```console
--- PASS: TestEval_SecurityConfigUpdate_ActiveHandshakeKeepsProvider (0.04s)
--- PASS: TestEval_SecurityConfigUpdate_ActiveHandshakeKeepsProvider (0.03s)
--- PASS: TestEval_SecurityConfigUpdate_ActiveHandshakeKeepsProvider (0.04s)
ok  	google.golang.org/grpc/internal/xds/balancer/clusterimpl/tests	0.119s
ok  	google.golang.org/grpc/internal/credentials/xds	0.027s
ok  	google.golang.org/grpc/credentials/xds	0.493s
ok  	google.golang.org/grpc/internal/xds/balancer/clusterimpl	0.010s
ok  	google.golang.org/grpc/internal/xds/balancer/clusterimpl/tests	3.907s
```

Impact reasoning: the task requires that "a client handshake that selected the prior Cluster security configuration
can finish after that configuration is replaced, including when replacement happens before validation-root loading
starts". On this branch a handshake that selected (and correctly holds) the prior configuration fails with
`authentication handshake failed: xds: security configuration was replaced before the TLS handshake could start`
whenever the Cluster update lands between the two `Acquire` calls — a window of a few instructions, so the failure is
rare in practice (14 % under a tight artificial race, far less in production) and the subchannel simply retries with
backoff; but it is exactly the "replacement before validation-root loading starts" case the task singles out, and the
branch's own tests plus the eval fixture stay green.

---

## C3

Claim: new/materially changed Go tests do not causally demonstrate that a later connection uses the replacement
validation-root configuration. Method: run each branch's new tests unmodified (green), then apply a small mutant to the
**production** code that makes the later connection fail *without* proving anything about replacement roots, and
observe whether the tests stay green. The mutant diffs are in `verify/repro/c3_*.diff`, driver
`verify/repro/c3_run_mutants.sh`. The eval fixture `eval_handshake_lifetime_test.go` — which asserts
`x509: certificate signed by unknown authority` *and* the replacement provider's KeyMaterial for the follow-up RPC — is
run under the same mutants as a contrast.

New test functions per branch (`git diff cc234554 <branch> -- '*_test.go' | grep '^+func'`):

```console
== 70cc0d6c
+func (s) TestClientCredsHandshakeInfoReplacedDuringHandshake(t *testing.T) {   # credentials/xds/xds_client_test.go
+func (s) TestHandshakeInfoRelease_DuringBlockedRootLoad(t *testing.T) {         # internal/credentials/xds (unit, no connection)
+func (s) TestHandshakeInfoRelease_BeforeRootLoadStarts(t *testing.T) {          # internal/credentials/xds (unit, no connection)
+func (s) TestAcquireHandshakeInfo(t *testing.T) {                               # internal/credentials/xds (unit, no connection)
+func (s) TestClientSideXDS_SecurityConfigReplacedDuringHandshake(t *testing.T) { # test/xds/xds_client_certificate_providers_test.go
== 833acc28
+func (s) TestClientCredsProviderReplacedDuringHandshake(t *testing.T) {         # credentials/xds/xds_client_test.go
+func (s) TestHandshakeInfoReferenceCounting(t *testing.T) {                     # internal/credentials/xds (unit, no connection)
+func (s) TestHandshakeInfoCloseWithoutHandshakes(t *testing.T) {                # internal/credentials/xds (unit, no connection)
+func (s) TestAcquireHandshakeInfo(t *testing.T) {                               # internal/credentials/xds (unit, no connection)
+func (s) TestSecurityConfigUpdate_ReplacedDuringHandshake(t *testing.T) {       # internal/xds/balancer/clusterimpl/tests/clusterimpl_security_test.go
```

Later-connection assertions as written:

* 70cc0d6c `TestClientCredsHandshakeInfoReplacedDuringHandshake`: `if _, _, err := creds.ClientHandshake(ctx, authority, conn2); err == nil { t.Fatal(...) }` — any error passes.
* 70cc0d6c `TestClientSideXDS_SecurityConfigReplacedDuringHandshake`: `testutils.AwaitState(ctx, t, cc, connectivity.TransientFailure)` then `status.Code(err) != codes.Unavailable || !strings.Contains(err.Error(), "authentication handshake failed")` — the wrapper string every failed TLS handshake carries; `provider2.loadStarted` is never checked.
* 833acc28 `TestClientCredsProviderReplacedDuringHandshake`: `err == nil → t.Fatal` — any error passes; `root2` is a plain fake with no KeyMaterial observation.
* 833acc28 `TestSecurityConfigUpdate_ReplacedDuringHandshake`: `<-untrustedProvider.loadStarted` (fired by *whoever* first calls KeyMaterial) then `testutils.AwaitState(ctx, t, cc, connectivity.TransientFailure)` (any failure reason); no per-connection error, no `grpc.Peer`, no x509 text.

### C3 — branch `evalon/grpc-go-xd-70cc0d6c` → CONFIRMED

Baseline (unmodified branch):

```sh
cd ~/wt/70cc0d6c
go test ./credentials/xds -run 'Test/ClientCredsHandshakeInfoReplacedDuringHandshake' -count=1
go test ./test/xds -run 'Test/ClientSideXDS_SecurityConfigReplacedDuringHandshake' -count=1 -v | grep -E '^(=== RUN|--- |ok|FAIL)'
```

```console
ok  	google.golang.org/grpc/credentials/xds	0.016s
=== RUN   Test
=== RUN   Test/ClientSideXDS_SecurityConfigReplacedDuringHandshake
--- PASS: Test (0.07s)
ok  	google.golang.org/grpc/test/xds	0.081s
```

**Mutant A** (`verify/repro/c3_70cc0d6c_mutantA_stale_config.diff`, `credentials/xds/xds.go`): later handshakes keep
using the first `HandshakeInfo` the credentials ever selected (stale-config bug); the replacement provider is never
consulted by any connection. It prints `VERIFY-MUTANT(C3): later handshake ignoring replacement HandshakeInfo, using
stale one` when it fires.

```sh
git apply verify/repro/c3_70cc0d6c_mutantA_stale_config.diff
for i in 1 2 3; do go test ./test/xds -run 'Test/ClientSideXDS_SecurityConfigReplacedDuringHandshake' -count=1 -v 2>&1 | grep -E '^(--- |ok|FAIL|VERIFY)'; done
go test ./credentials/xds -run 'Test/ClientCredsHandshakeInfoReplacedDuringHandshake' -count=1 -v 2>&1 | grep -E '^(--- |ok|FAIL|VERIFY|\s+xds_client_test)'
```

```console
VERIFY-MUTANT(C3): later handshake ignoring replacement HandshakeInfo, using stale one
--- PASS: Test (0.07s)
ok  	google.golang.org/grpc/test/xds	0.077s
--- PASS: Test (0.07s)
ok  	google.golang.org/grpc/test/xds	0.076s
--- PASS: Test (0.07s)
ok  	google.golang.org/grpc/test/xds	0.076s
    xds_client_test.go:800: testServer failed to return handshake result: context deadline exceeded
--- FAIL: Test (1.06s)
FAIL	google.golang.org/grpc/credentials/xds	1.059s
```

The e2e test stays green although no connection ever used the replacement roots. The unit test fails only
incidentally (its server-side drain times out because the stale, closed provider made the client fail before sending
any TLS bytes) — its own later-connection assertion (`err == nil`) passed.

**Mutant B** (`verify/repro/c3_70cc0d6c_mutantB_nontrust_failure.diff`, `credentials/xds/xds.go`): later handshakes do load
the replacement KeyMaterial but then fail for an unrelated reason (`cfg.MaxVersion = tls.VersionTLS10`; the server
rejects the protocol version before any certificate is verified).

```sh
git checkout credentials/xds/xds.go && git apply verify/repro/c3_70cc0d6c_mutantB_nontrust_failure.diff
go test ./credentials/xds -run 'Test/ClientCredsHandshakeInfoReplacedDuringHandshake' -count=1 -v 2>&1 | grep -E '^(--- |ok|FAIL|VERIFY)'
go test ./test/xds -run 'Test/ClientSideXDS_SecurityConfigReplacedDuringHandshake' -count=1 -v 2>&1 | grep -E '^(--- |ok|FAIL|VERIFY)'
cp ~/eval_tests/tests/eval_handshake_lifetime_test.go internal/xds/balancer/clusterimpl/tests/
go test ./internal/xds/balancer/clusterimpl/tests -run '^TestEval_SecurityConfigUpdate_ActiveHandshakeKeepsProvider$' -count=1 -v 2>&1 | grep -E '^(--- |ok|FAIL|VERIFY|\s+eval_)'
```

```console
VERIFY-MUTANT(C3-B): later handshake will fail for a non-trust reason
--- PASS: Test (0.01s)
ok  	google.golang.org/grpc/credentials/xds	0.012s
VERIFY-MUTANT(C3-B): later handshake will fail for a non-trust reason
--- PASS: Test (0.07s)
ok  	google.golang.org/grpc/test/xds	0.077s
VERIFY-MUTANT(C3-B): later handshake will fail for a non-trust reason
VERIFY-MUTANT(C3-B): later handshake will fail for a non-trust reason
    eval_handshake_lifetime_test.go:357: Follow-up RPC error = replacement roots were not observed after 100 follow-up RPC attempts, want x509 unknown authority
--- FAIL: TestEval_SecurityConfigUpdate_ActiveHandshakeKeepsProvider (2.55s)
FAIL	google.golang.org/grpc/internal/xds/balancer/clusterimpl/tests	2.555s
```

Both branch tests stay green under a mutant where the later connection's failure has nothing to do with the
replacement roots; the eval fixture (which demands the x509 trust error) catches it. Clean-tree control for the fixture
on this branch: `--- PASS: TestEval_SecurityConfigUpdate_ActiveHandshakeKeepsProvider (0.04s)`.

### C3 — branch `evalon/grpc-go-xd-833acc28` → CONFIRMED

Baseline:

```sh
cd ~/wt/833acc28
go test ./credentials/xds -run 'Test/ClientCredsProviderReplacedDuringHandshake' -count=1 -v 2>&1 | grep -E '^(--- |ok|FAIL)'
go test ./internal/xds/balancer/clusterimpl/tests -run 'Test/SecurityConfigUpdate_ReplacedDuringHandshake' -count=1 -v 2>&1 | grep -E '^(=== RUN|--- |ok|FAIL)'
```

```console
--- PASS: Test (0.02s)
ok  	google.golang.org/grpc/credentials/xds	0.028s
=== RUN   Test
=== RUN   Test/SecurityConfigUpdate_ReplacedDuringHandshake
--- PASS: Test (0.17s)
ok  	google.golang.org/grpc/internal/xds/balancer/clusterimpl/tests	0.183s
```

**Mutant B** (`verify/repro/c3_833acc28_mutantB_nontrust_failure.diff`; same non-trust failure as above):

```sh
git apply verify/repro/c3_833acc28_mutantB_nontrust_failure.diff
go test ./credentials/xds -run 'Test/ClientCredsProviderReplacedDuringHandshake' -count=1 -v 2>&1 | grep -E '^(--- |ok|FAIL|VERIFY)'
go test ./internal/xds/balancer/clusterimpl/tests -run 'Test/SecurityConfigUpdate_ReplacedDuringHandshake' -count=1 -v 2>&1 | grep -E '^(--- |ok|FAIL|VERIFY)'
```

```console
VERIFY-MUTANT(C3-B): later handshake will fail for a non-trust reason
--- PASS: Test (0.02s)
ok  	google.golang.org/grpc/credentials/xds	0.022s
VERIFY-MUTANT(C3-B): later handshake will fail for a non-trust reason
--- PASS: Test (0.12s)
ok  	google.golang.org/grpc/internal/xds/balancer/clusterimpl/tests	0.127s
```

**Mutant C** (`verify/repro/c3_833acc28_mutantC_prefetch_plus_stale.diff`) targets the "same connection" link
specifically: (1) `clusterimpl.go` — on a security-configuration *replacement* the balancer reads the new root
provider's KeyMaterial once itself (a "warm-up", not a connection); (2) `credentials/xds/xds.go` — later handshakes
keep using the stale first `HandshakeInfo`, whose provider is already closed, so they fail with
`provider instance is closed`. No connection ever loads or uses the replacement roots.

```sh
git checkout credentials/xds/xds.go && git apply verify/repro/c3_833acc28_mutantC_prefetch_plus_stale.diff
go test ./internal/xds/balancer/clusterimpl/tests -run '^Test$/^SecurityConfigUpdate_ReplacedDuringHandshake$' -count=1 -v 2>&1 | grep -E '^(--- |ok|FAIL|VERIFY)'
go test ./credentials/xds -run '^Test$/^ClientCredsProviderReplacedDuringHandshake$' -count=1 -v 2>&1 | grep -E '^(--- |ok|FAIL|VERIFY|\s+xds_client)'
cp ~/eval_tests/tests/eval_handshake_lifetime_test.go internal/xds/balancer/clusterimpl/tests/
go test ./internal/xds/balancer/clusterimpl/tests -run '^TestEval_SecurityConfigUpdate_ActiveHandshakeKeepsProvider$' -count=1 -v 2>&1 | grep -E '^(--- |ok|FAIL|VERIFY|\s+eval_)'
```

```console
VERIFY-MUTANT(C3-C): balancer prefetching replacement root KeyMaterial
VERIFY-MUTANT(C3-C): balancer prefetching replacement root KeyMaterial
VERIFY-MUTANT(C3-C): later handshake ignoring replacement HandshakeInfo, using stale one
--- PASS: Test (0.17s)
ok  	google.golang.org/grpc/internal/xds/balancer/clusterimpl/tests	0.175s
VERIFY-MUTANT(C3-C): later handshake ignoring replacement HandshakeInfo, using stale one
    xds_client_test.go:820: testServer failed to return handshake result: context deadline exceeded
--- FAIL: Test (1.01s)
FAIL	google.golang.org/grpc/credentials/xds	1.010s
VERIFY-MUTANT(C3-C): balancer prefetching replacement root KeyMaterial
VERIFY-MUTANT(C3-C): balancer prefetching replacement root KeyMaterial
VERIFY-MUTANT(C3-C): later handshake ignoring replacement HandshakeInfo, using stale one
VERIFY-MUTANT(C3-C): later handshake ignoring replacement HandshakeInfo, using stale one
VERIFY-MUTANT(C3-C): later handshake ignoring replacement HandshakeInfo, using stale one
    eval_handshake_lifetime_test.go:357: Follow-up RPC error = replacement roots were not observed after 100 follow-up RPC attempts, want x509 unknown authority
--- FAIL: TestEval_SecurityConfigUpdate_ActiveHandshakeKeepsProvider (2.54s)
FAIL	google.golang.org/grpc/internal/xds/balancer/clusterimpl/tests	2.552s
```

The e2e test `TestSecurityConfigUpdate_ReplacedDuringHandshake` stays green: its KeyMaterial event came from the
balancer, its TransientFailure came from a closed stale provider, and nothing ties either to the later connection. The
unit test's own later-connection assertion (`err == nil`) also passed; the run fails only on the incidental server-side
drain (as with Mutant A on 70cc0d6c) and stays fully green under Mutant B. The eval fixture fails under the same mutant.
(Mutants use a process-global sticky pointer, hence `-count=1`; a `-count=3` run reuses state and is not meaningful.)

---

## C4 → CONFIRMED (branch `evalon/grpc-go-xd-d030c2f0`)

Claim parts: (release mechanism) `clusterImplBalancer.Close` releases the owner loaded from `xdsHIPtr` without clearing
the pointer; (repeated invocation) the same instance receives multiple `Close` calls, including explicit + deferred in
the security-replacement test.

Source (`internal/xds/balancer/clusterimpl/clusterimpl.go:510-512`):

```go
	if hi := b.xdsHIPtr.Load(); hi != nil {
		hi.Release()
	}
```

`HandshakeInfo.Release` is `if hi.refs.Add(-1) != 0 { return }; close providers` — no idempotence guard. The branch's
own test `internal/xds/balancer/clusterimpl/balancer_test.go` has `defer b.Close()` (line 469) **and** `b.Close()`
(line 547) on the same instance in `TestSecurityConfigReplacedDuringHandshake`, and passes (`--- PASS: Test (0.00s)`)
because no handshake is outstanding at that point.

Repro `verify/repro/c4_d030c2f0_double_close_test.go` (copied to `internal/xds/balancer/clusterimpl/`; reuses the
branch's `closeTrackingProvider` and `securityConfigClientConnState` helpers): build the balancer with a tracked root
provider, simulate one in-flight handshake with `hi.Acquire()` (refs = owner + handshake = 2), call `b.Close()` twice.

```sh
cd ~/wt/d030c2f0 && go test ./internal/xds/balancer/clusterimpl -run 'Test/Verify_C4' -count=1 -v 2>&1 | grep -E 'verify_c4|^(--- |ok|FAIL)'
```

```console
    verify_c4_test.go:83: after 1st Close: provider closed = false
    verify_c4_test.go:85: after 2nd Close (same instance, handshake still active): provider closed = true
    verify_c4_test.go:92: in-flight handshake ClientSideTLSConfig() err = xds: fetching trusted roots from CertificateProvider failed: provider instance is closed
    verify_c4_test.go:94: C4 CONFIRMED: second Close released the owner reference again and closed the provider under an active handshake
    verify_c4_test.go:62: after 1x Close with 1 in-flight handshake: provider closed = false (want false)
    verify_c4_test.go:67: after handshake Release: provider closed = true (want true)
--- FAIL: Test (0.00s)
    --- FAIL: Test/Verify_C4_DoubleClose_ReleasesOwnerTwice (0.00s)
    --- PASS: Test/Verify_C4_SingleClose_Control (0.00s)
FAIL	google.golang.org/grpc/internal/xds/balancer/clusterimpl	0.007s
```

(`Verify_C4_DoubleClose_ReleasesOwnerTwice` is written to FAIL when the double release is observed.) Control: one
`Close` with one in-flight handshake leaves the provider open until the handshake's own `Release` — correct. Two
`Close` calls decrement the single owner reference twice, stealing the handshake's reference: the provider is closed
under the live handshake and its root load fails.

Impact reasoning: in production the parent policy calls `Close` once, so the everyday path is not affected; but the
balancer's `Close` is not idempotent and the branch's own test exercises the double-Close path, so any future caller
(or test) that closes twice while a handshake is in flight fails that handshake — the very failure this branch set out
to fix. Minimal fix: make `Close` release exactly once — e.g. `old := b.xdsHIPtr.Swap(<fresh empty HandshakeInfo>);
old.Release()` as the 808a7060 branch does, or a `sync.Once`/`closed` flag — and turn the repro into a regression test.

---

## C5 → CONFIRMED (audited branch `grpc-go-xds-certificate-provider-closure-race-perfect`, HEAD `3483b320`)

Repository-declared static-analysis workflow: `scripts/vet.sh` (`-install` first). Run in a clean detached worktree of
the audited HEAD (`vet.sh` refuses a dirty tree and runs `git reset --hard HEAD` on exit). Note: `fail_on_output` uses
`tee /dev/stderr`, so redirecting stderr straight to a file garbles the log; pipe through `cat` instead.

```sh
git worktree add ~/wt/perfect 3483b320 --detach
cd ~/wt/perfect && ./scripts/vet.sh -install            # exit 0 (goimports, staticcheck, misspell, revive installed)
./scripts/vet.sh 2>&1 | cat > ~/vet_run3.log; echo "exit=${PIPESTATUS[0]}"
grep -a -v '^+ grep -L' ~/vet_run3.log
```

```console
exit=1
+ go version
go version go1.25.7 linux/amd64
+ fail_on_output
++ git grep -L '\(Copyright [0-9]\{4,\} gRPC authors\)' -- '*.go'
+ tee /dev/stderr
+ not read
+ read
internal/xds/balancer/clusterimpl/tests/concurrent_handshake_test.go
+ cleanup
+ git reset --hard HEAD
HEAD is now at 3483b320 fix: keep perfect calibration on client provider lifetime
```

The very first gate ("Ensure all source files contain a copyright message") fails: the new test file
`internal/xds/balancer/clusterimpl/tests/concurrent_handshake_test.go` has no `Copyright <year> gRPC authors` header
(`grep -c Copyright` → `0`) and no `DO NOT EDIT` marker, so `fail_on_output` exits 1 and the script stops there.

Isolation of the cause — with only the standard Apache header prepended to that file (temporary commit in the throwaway
worktree, never pushed), the full workflow (misspell, `go vet -all`, gofmt, goimports, `go mod tidy`, revive,
staticcheck for every module) passes:

```sh
# in ~/wt/perfect, after prepending the standard header and committing locally
./scripts/vet.sh 2>&1 | cat > ~/vet_run4_with_header.log; echo "exit=${PIPESTATUS[0]}" >> ~/vet_run4_with_header.log
tail -6 ~/vet_run4_with_header.log
```

```console
+ echo SUCCESS
SUCCESS
+ cleanup
+ git reset --hard HEAD
HEAD is now at 58dfcb2e verify(tmp): add copyright header to probe remaining vet checks
exit=0
```

So the repository does not pass its static-analysis workflow as committed; the missing header is the sole violation.
