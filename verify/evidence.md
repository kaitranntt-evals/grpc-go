# Evidence — behavioral audit v-d0133543

Each section is self-contained: exact commands and key output verbatim. Claim-target branches were checked out into separate worktrees (`git fetch evalrepo <branch> && git worktree add ~/wt/<short-id> FETCH_HEAD`); commands below run from the root of the named branch's checkout unless stated otherwise. The pre-repair base commit is `cc234554`.

## C1

Claim: a client handshake that selected a validation-root provider can lose it when a Cluster security update closes it before KeyMaterial begins. Adjudicated per branch with deterministic probes (committed under `verify/repro/`). Verdict: CONFIRMED on all three branches.

### C1 — evalon/grpc-go-xd-7ac76612

`handleSecurityConfig` closes the old providers before storing the replacement HandshakeInfo (clusterimpl.go: `b.cachedRoot.Close()` at line 381 precedes `b.xdsHIPtr.Store(...)` at line 389), so a handshake can select a HandshakeInfo whose providers are already fully closed. Probe (`verify/repro/c1_7ac76612_probe_test.go` copied to `credentials/xds/`):

```console
$ go test ./credentials/xds/ -run TestVerifyC1 -count=1 -v
=== RUN   TestVerifyC1_ClosedBeforeKeyMaterial
    verify_c1_probe_test.go:68: ClientHandshake() error: xds: fetching trusted roots from CertificateProvider failed: xds: certificate provider is closed
    verify_c1_probe_test.go:70: CONFIRMED: handshake failed with a closed-provider error before KeyMaterial ever began
--- PASS: TestVerifyC1_ClosedBeforeKeyMaterial (0.00s)
```

Impact: any Cluster security update racing an in-flight connection attempt can fail that TLS handshake with `certificate provider is closed`, even though a valid configuration existed before and after the update — exactly the original flake.

### C1 — evalon/grpc-go-xd-16287bb4

`retainHandshakeInfo` retries once on a failed retain, but when the pointer still holds the same (fully released) HandshakeInfo it proceeds with the closed snapshot. Probe (`verify/repro/c1_16287bb4_probe_test.go` copied to `credentials/xds/`):

```console
$ go test ./credentials/xds/ -run TestVerifyC1 -count=1 -v
=== RUN   TestVerifyC1_FailedRetainUsesClosedSnapshot
    verify_c1_probe_test.go:73: Retain() on the selected provider fails (snapshot cannot be retained)
    verify_c1_probe_test.go:79: ClientHandshake() error: xds: fetching trusted roots from CertificateProvider failed: provider instance is closed
    verify_c1_probe_test.go:81: CONFIRMED: after the failed retain, the handshake proceeded with the closed snapshot and failed with a closed-provider error
--- PASS: TestVerifyC1_FailedRetainUsesClosedSnapshot (0.00s)
```

### C1 — evalon/grpc-go-xd-23014fd5

When the selected snapshot cannot be retained (`ErrStaleHandshakeInfo`), the retry loop in `ClientHandshake` reloads the pointer and completes the handshake under the replacement validation roots instead of the ones the handshake selected. Instrumentation: `verify/repro/c1_23014fd5_hook.patch` adds `TestOnlyBeforeAcquireProvidersHook` at the start of `acquireProviders` (no behavior change when unset). Probe (`verify/repro/c1_23014fd5_probe_test.go` copied to `credentials/xds/`) selects an old HandshakeInfo whose roots REJECT the test server, then (in the hook) publishes a replacement whose roots TRUST it and closes the old provider:

```console
$ go test ./credentials/xds/ -run TestVerifyC1 -count=1 -v
=== RUN   TestVerifyC1_SwitchesToReplacementRoots
    verify_c1_probe_test.go:79: CONFIRMED: handshake selected the old HandshakeInfo (roots that reject this server), failed to retain it, and completed successfully under the REPLACEMENT validation roots
--- PASS: TestVerifyC1_SwitchesToReplacementRoots (0.01s)
```

Impact: the handshake silently changes trust anchors mid-flight — it does not keep the selected roots usable until the handshake finishes, violating the task's success criterion.

## C2

Claim: successful root construction followed by identity construction failure leaks the root provider. Probe `verify/repro/c2_c7_identity_failure_probe_test.go` (copied to `internal/xds/balancer/clusterimpl/`) overrides `buildProvider` so the root instance succeeds (with a close counter) and the identity instance fails, then invokes `handleSecurityConfig` on a real `clusterImplBalancer`. Verdict: CONFIRMED on all six branches — identical output on `81ee173d`, `62fdf90d`, `7ac76612`, `b84d4465`, `0b0b9a79`, `16b031f9`:

```console
$ go test ./internal/xds/balancer/clusterimpl/ -run TestVerifyC2 -count=1 -v
=== RUN   TestVerifyC2_IdentityBuildFailureRootProviderClose
    verify_c2_probe_test.go:62: handleSecurityConfig() returned error as expected: verify: identity provider construction failure
    verify_c2_probe_test.go:65: root provider Close() call count after identity build failure: 0
    verify_c2_probe_test.go:67: VERDICT: root provider was NOT closed after identity construction failure (leak confirmed)
--- FAIL: TestVerifyC2_IdentityBuildFailureRootProviderClose (0.00s)
```

(The probe fails deliberately when it observes the leak; the `FAIL` is the confirmation.) Impact: every malformed mTLS update whose identity instance is bad leaks a live root provider (file watchers keep running); repeated bad updates accumulate goroutines and file handles.

## C3

Claim: the added tests do not distinguish whether the later connection uses the old or the replacement validation roots. Target: `evalon/grpc-go-xd-70c131e8`, test `TestClientSideXDS_SecurityConfigurationReplacement` in `test/xds/xds_client_certificate_providers_test.go`. Verdict: CONFIRMED.

The test passes as written:

```console
$ go test ./test/xds/ -run 'Test/ClientSideXDS_SecurityConfigurationReplacement' -count=1 -v
--- PASS: Test (0.02s)
    --- PASS: Test/ClientSideXDS_SecurityConfigurationReplacement (0.02s)
ok  	google.golang.org/grpc/test/xds	0.032s
```

Mutation: make the "replacement" cluster keep the ORIGINAL trusted roots (no real root replacement):

```console
$ sed -i '628s/untrustedRootsInstance/trustedRootsInstance/' test/xds/xds_client_certificate_providers_test.go
$ go test ./test/xds/ -run 'Test/ClientSideXDS_SecurityConfigurationReplacement' -count=1 -v
--- PASS: Test (0.07s)
    --- PASS: Test/ClientSideXDS_SecurityConfigurationReplacement (0.07s)
ok  	google.golang.org/grpc/test/xds	0.079s
```

The test still passes when no untrusted replacement roots exist at all, so its later-connection assertion does not identify which root configuration governed the connection. Repro: `verify/repro/c3_70c131e8_mutation.sh`.

## C4

Claim: the new/changed tests never exercise provider cleanup with at least two simultaneous owners or overlapping handshakes/updates. Verdict: CONFIRMED on all three branches. All named tests run and pass, and inspection of each shows exactly one active handshake/owner overlapping exactly one update, with no assertion that cleanup happens exactly once after the last of multiple simultaneous owners releases.

### C4 — evalon/grpc-go-xd-8f1458a1

```console
$ go test ./credentials/xds/ ./internal/credentials/xds/ -run 'Test/(ClientCredsProviderSwitchGoodToBad|HandshakeInfoStoreProviderLifetime)' -count=1 -v
    --- PASS: Test/ClientCredsProviderSwitchGoodToBad (0.02s)
    --- PASS: Test/HandshakeInfoStoreProviderLifetime (0.00s)
        --- PASS: Test/HandshakeInfoStoreProviderLifetime/replacement_before_root_load (0.00s)
        --- PASS: Test/HandshakeInfoStoreProviderLifetime/replacement_during_root_load (0.00s)
```

`TestClientCredsProviderSwitchGoodToBad` performs two strictly sequential handshakes. `TestHandshakeInfoStoreProviderLifetime` acquires the store exactly once (`selected := store.Acquire()`) and overlaps that single owner with a single `store.Store(newHI)`.

### C4 — evalon/grpc-go-xd-48212db3

```console
$ go test ./credentials/xds/ -run 'Test/ClientCredsProviderReplacementDuringRootLoad' -count=1 -v
    --- PASS: Test/ClientCredsProviderReplacementDuringRootLoad (0.01s)
```

One goroutine runs one handshake; one `hiPtr.Swap(newHI); replaced.Retire()` overlaps it. Never two handshakes or two owners at once.

### C4 — evalon/grpc-go-xd-4114f548

```console
$ go test ./credentials/xds/ ./internal/credentials/xds/ -run 'Test/(ClientCredsProviderSwitchDuringRootLoad|HandshakeInfoPointerRetainsSelectedProviders)' -count=1 -v
    --- PASS: Test/ClientCredsProviderSwitchDuringRootLoad (0.01s)
    --- PASS: Test/HandshakeInfoPointerRetainsSelectedProviders (0.00s)
```

`TestHandshakeInfoPointerRetainsSelectedProviders` does a single `ptr.Acquire()` overlapping a single `ptr.Store(...)`; `TestClientCredsProviderSwitchDuringRootLoad` runs one blocked handshake across one store. No multi-owner exactly-once-cleanup assertion exists on any branch.

## C5

Claim: a handshake through a stale xDS address after its Cluster balancer shuts down invokes fallback credentials instead of failing closed. Target: `evalon/grpc-go-xd-a34d4566`. Verdict: CONFIRMED.

Teardown publication (source): `clusterImplBalancer.Close()` publishes an empty HandshakeInfo to the shared pointer embedded in address attributes — `internal/xds/balancer/clusterimpl/clusterimpl.go:508: b.setHandshakeInfo(xds.NewHandshakeInfo(nil, nil, nil, false, "", false, false))` — and `UseFallbackCreds()` returns true for it (`handshake_info.go:210`).

Stale-address behavior (observed): probe `verify/repro/c5_a34d4566_probe_test.go` (copied to `credentials/xds/`) publishes a live HandshakeInfo to a pointer, stores the same empty HandshakeInfo the balancer's `Close()` stores, then starts a handshake through the stale address with counting fallback credentials:

```console
$ go test ./credentials/xds/ -run TestVerifyC5 -count=1 -v
=== RUN   TestVerifyC5_StaleAddressInvokesFallback
    verify_c5_probe_test.go:71: CONFIRMED: fallback credentials were invoked 1 time(s) through the stale post-shutdown address (handshake err=<nil>)
--- PASS: TestVerifyC5_StaleAddressInvokesFallback (0.01s)
```

Impact: after balancer teardown a racing connection attempt silently downgrades to fallback credentials (insecure creds in the common xDS bootstrap fallback configuration) instead of failing closed.

## C6

Claim: rapid successive replacements between snapshot selection and ownership acquisition exhaust the fixed acquisition attempts and hand the handshake a closed HandshakeInfo. Target: `evalon/grpc-go-xd-f0677079`. Verdict: CONFIRMED.

`AcquireHandshakeInfo` (internal/credentials/xds/handshake_info.go) makes exactly two attempts; the second attempt's failure is discarded (`release, _ := hi.acquireProviders()`). Instrumentation: `verify/repro/c6_f0677079_hook.patch` adds `TestOnlyBeforeReacquireHook` between the failed first attempt and the second. Probe (`verify/repro/c6_f0677079_probe_test.go` copied to `credentials/xds/`) retires the selected snapshot, then in the hook publishes-and-retires the replacement (second rapid replacement), then resumes:

```console
$ go test ./credentials/xds/ -run TestVerifyC6 -count=1 -v
=== RUN   TestVerifyC6_RapidReplacementsReturnClosedHandshakeInfo
    verify_c6_probe_test.go:80: CONFIRMED: after two rapid replacements exhausted the fixed acquisition attempts, ClientHandshake() reached a closed provider: xds: CertificateProviders are closed, cannot perform TLS handshake
--- PASS: TestVerifyC6_RapidReplacementsReturnClosedHandshakeInfo (0.00s)
```

## C7

Claim: identity-provider construction failure leaves the already constructed root provider unclosed. Target: `evalon/grpc-go-xd-70c131e8`. Verdict: CONFIRMED. Same probe as C2 (`verify/repro/c2_c7_identity_failure_probe_test.go` copied to `internal/xds/balancer/clusterimpl/`):

```console
$ go test ./internal/xds/balancer/clusterimpl/ -run TestVerifyC2 -count=1 -v
=== RUN   TestVerifyC2_IdentityBuildFailureRootProviderClose
    verify_c2_probe_test.go:62: handleSecurityConfig() returned error as expected: verify: identity provider construction failure
    verify_c2_probe_test.go:65: root provider Close() call count after identity build failure: 0
    verify_c2_probe_test.go:67: VERDICT: root provider was NOT closed after identity construction failure (leak confirmed)
--- FAIL: TestVerifyC2_IdentityBuildFailureRootProviderClose (0.00s)
```

The root provider received no owner (the HandshakeInfo is never stored on the error path) and no Close call before `handleSecurityConfig` returned.

## C8

Claim: repeated delivery of an unchanged effective Cluster security configuration rebuilds providers and replaces HandshakeInfo snapshots. Target: `evalon/grpc-go-xd-0b0b9a79`. Verdict: CONFIRMED. `handleSecurityConfig` has no equality check against the previous config. Probe `verify/repro/c8_0b0b9a79_probe_test.go` (copied to `internal/xds/balancer/clusterimpl/`) counts provider builds/closes across two deliveries of the identical `SecurityConfig`:

```console
$ go test ./internal/xds/balancer/clusterimpl/ -run TestVerifyC8 -count=1 -v
=== RUN   TestVerifyC8_UnchangedConfigRebuildsProviders
    verify_c8_probe_test.go:68: provider builds: after first update = 1, after identical second update = 2
    verify_c8_probe_test.go:69: HandshakeInfo replaced by identical update: true
    verify_c8_probe_test.go:74: provider Close() calls after identical second update: 1
    verify_c8_probe_test.go:77: VERDICT CONFIRMED: identical update caused 1 additional provider build(s), HandshakeInfo replaced=true, 1 provider closure(s)
--- FAIL: TestVerifyC8_UnchangedConfigRebuildsProviders (0.00s)
```

(The probe fails deliberately on confirmation.) Impact: every re-delivered identical Cluster update churns providers and retires snapshots, needlessly widening the very replacement race the solution is meant to fix.

## C9

Claim: no added/changed test exercises a live xDS-managed handshake during a Cluster update through the complete production path plus a later connection. Target: `evalon/grpc-go-xd-7ac76612`. Verdict: CONFIRMED.

Inventory:

```console
$ git diff cc234554 HEAD | grep '^+func (s) Test'
+func (s) TestRefCountedProvider_ReplacedWhileHandshakeInProgress(t *testing.T) {
+func (s) TestRefCountedProvider_ReplacedBeforeLoad(t *testing.T) {
+func (s) TestRefCountedProvider_ClosedBeforeAnyLoad(t *testing.T) {
+func (s) TestSecurityConfigUpdate_ValidationRootsReplaced(t *testing.T) {
```

Runs:

```console
$ go test ./internal/xds/balancer/clusterimpl/tests/ -run 'Test/SecurityConfigUpdate_ValidationRootsReplaced' -count=1 -v
    --- PASS: Test/SecurityConfigUpdate_ValidationRootsReplaced (0.02s)
$ go test ./internal/credentials/xds/ -run 'Test/RefCountedProvider' -count=1 -v
    --- PASS: Test/RefCountedProvider_ClosedBeforeAnyLoad (0.00s)
    --- PASS: Test/RefCountedProvider_ReplacedBeforeLoad (0.00s)
    --- PASS: Test/RefCountedProvider_ReplacedWhileHandshakeInProgress (0.40s)
```

The three `RefCountedProvider` tests are unit-level (no management server, resolver, or balancer). The one live-xDS test completes its first `EmptyCall` (and hence its handshake) BEFORE `mgmtServer.Update` delivers the replacement — no handshake is in flight during the update, and it asserts no provider cleanup. Repro: `verify/repro/c9_7ac76612_inventory.sh`.

## C10

Claim: replaying the solution's focused regression tests against the premature-close implementation stops on missing symbols before behavioral assertions execute. Target: `evalon/grpc-go-xd-b84d4465`, replayed against base `cc234554`. Verdict: CONFIRMED.

```console
$ cp <b84d4465>/internal/credentials/xds/handshake_info_acquire_test.go <b84d4465>/internal/credentials/xds/provider_test.go internal/credentials/xds/
$ go test ./internal/credentials/xds/ -run 'Test/(AcquireHandshakeInfo|SharedProvider)' -count=1
# google.golang.org/grpc/internal/credentials/xds [google.golang.org/grpc/internal/credentials/xds.test]
internal/credentials/xds/handshake_info_acquire_test.go:68:15: undefined: NewSharedProvider
internal/credentials/xds/handshake_info_acquire_test.go:78:18: undefined: AcquireHandshakeInfo
...
FAIL	google.golang.org/grpc/internal/credentials/xds [build failed]
```

Compilation stops on the newly introduced symbols; no behavioral assertion ever executes, so these tests cannot detect the original premature-close bug on the pre-repair tree. Repro: `verify/repro/c10_b84d4465_replay.sh`.

## C11

Claim: the post-update assertion in `clusterimpl_security_test.go` continuously retries `EmptyCall` with no synchronization, backoff, or bound. Target: `evalon/grpc-go-xd-62fdf90d`, test `TestSecurityConfigUpdate_ReplacementRootsDoNotTrustServer`. Verdict: CONFIRMED.

The post-update assertion (verbatim from the test):

```go
for ctx.Err() == nil {
	_, err := client.EmptyCall(ctx, &testpb.Empty{})
	if err == nil {
		continue
	}
	if strings.Contains(err.Error(), "certificate signed by unknown authority") {
		return
	}
	t.Logf("EmptyCall() failed with unexpected error: %v", err)
}
t.Fatal("Timed out waiting for RPCs to fail because the server certificate is not trusted by the new validation roots")
```

There is no event wait, sleep, backoff, or attempt bound — after any unexpected outcome (including `err == nil`) it immediately re-invokes `EmptyCall` until the context deadline. The test runs and passes:

```console
$ go test ./internal/xds/balancer/clusterimpl/tests/ -run 'Test/SecurityConfigUpdate_ReplacementRootsDoNotTrustServer' -count=1 -v
    --- PASS: Test/SecurityConfigUpdate_ReplacementRootsDoNotTrustServer (0.03s)
```

Repro: `verify/repro/c11_62fdf90d_trace.sh`.

## C12

Claim: the solution exposes an exported `certprovider.Retain` API used only for internal xDS handshake lifetime management. Target: `evalon/grpc-go-xd-16287bb4`. Verdict: CONFIRMED.

```console
$ go doc google.golang.org/grpc/credentials/tls/certprovider Retain
func Retain(provider Provider) (release func(), ok bool)
    Retain acquires a reference on the given provider on behalf of a user which
    needs it to remain usable independently of whoever built it releasing it,
    e.g. an in-progress TLS handshake which has already picked this provider.

$ grep -rn 'certprovider.Retain(' --include='*.go' . | grep -v _test.go
./internal/credentials/xds/handshake_info.go:156:		releaseProvider, ok := certprovider.Retain(provider)
```

The declaration is exported in `credentials/tls/certprovider/store.go:187` (marked Experimental) and its sole production call site is internal xDS handshake lifetime management — public API surface added for a purely internal need. Repro: `verify/repro/c12_16287bb4_api.sh`.
