## Evidence — grpc-go-xds-certificate-provider-closure-race, run v-4cd25ece

Environment: `go1.25.7 linux/amd64`. Audited checkout: `~/repos/grpc-go` on
`verify/grpc-go-xds-certificate-provider-closure-race-v-4cd25ece` (from
`origin/grpc-go-xds-certificate-provider-closure-race-perfect`, base
`cc234554fb363aea445a838b341bb8a65c8305b0`). Claim-target branches live in a
second repository and were fetched as remote `evalrepo`
(`https://github.com/kaitranntt-evals/grpc-go-xds-certificate-provider-closure-race.git`);
each was checked out in its own worktree `~/wt/<suffix>` so the main checkout
stays clean:

```sh
cd ~/repos/grpc-go
git remote add evalrepo https://github.com/kaitranntt-evals/grpc-go-xds-certificate-provider-closure-race.git
for b in 381de4ee 3a8572bc 4dc02aec ce8a38bc 817a8ca3 c29df868 9085e702 d215d00f 7f9f59d2 9c0c4257 74048b3d; do
  git fetch evalrepo evalon/grpc-go-xd-$b
  git worktree add ~/wt/$b evalrepo/evalon/grpc-go-xd-$b
done
```

Every repro under `verify/repro/` is a Go test file (or shell script) whose
first line says where to copy it and how to run it. Repro files were copied
into the named package directory of the target worktree and run with
`-count=1`; the production code of each worktree was never edited.

---

## C1

**Claim:** a production certificate-provider wrapper admits new `Acquire` or
`KeyMaterial` work after `Close` has initiated shutdown. Adjudicated per branch.

### C1 on evalon/grpc-go-xd-381de4ee

Source facts (`internal/xds/balancer/clusterimpl/clusterimpl.go`, lines 170–198):

```go
func (p *refCountedProvider) Acquire() bool {
	for refs := p.refs.Load(); refs > 0; refs = p.refs.Load() {
		if p.refs.CompareAndSwap(refs, refs+1) {
			return true
		}
	}
	return false
}
func (p *refCountedProvider) Release() {
	if p.refs.Add(-1) == 0 {
		p.Provider.Close()
	}
}
func (p *refCountedProvider) KeyMaterial(ctx context.Context) (*certprovider.KeyMaterial, error) {
	if !p.Acquire() {
		return nil, fmt.Errorf("provider instance is closed")
	}
	defer p.Release()
	return p.Provider.KeyMaterial(ctx)
}
func (p *refCountedProvider) Close() {
	p.closeOnce.Do(p.Release)
}
```

`Close` is just a `Release` of the cache's reference; there is no "closing"
state, so `Acquire` keeps succeeding while any other reference exists.

Repro: `verify/repro/c1_c6_381de4ee_refcounted_after_close_test.go`
(creates a `refCountedProvider` over a recording provider, takes one extra
reference, calls `Close`, then starts a *fresh* `Acquire` and a *fresh*
`KeyMaterial`).

```sh
cp verify/repro/c1_c6_381de4ee_refcounted_after_close_test.go ~/wt/381de4ee/internal/xds/balancer/clusterimpl/
cd ~/wt/381de4ee && go test ./internal/xds/balancer/clusterimpl -run TestVerifyC1_ -v -count=1
```

```console
=== RUN   TestVerifyC1_AcquireAndKeyMaterialAdmittedAfterClose
    c1_c6_381de4ee_refcounted_after_close_test.go:31: after Close: refs=1 underlying.closed=false
    c1_c6_381de4ee_refcounted_after_close_test.go:34: fresh Acquire() after Close() -> true (refs=2)
    c1_c6_381de4ee_refcounted_after_close_test.go:39: fresh KeyMaterial() after Close() -> err=<nil> underlyingCalls=1
    c1_c6_381de4ee_refcounted_after_close_test.go:44: after last Release: underlying.closed=true
--- PASS: TestVerifyC1_AcquireAndKeyMaterialAdmittedAfterClose (0.00s)
=== RUN   TestVerifyC1_RefusedOnlyAfterLastRelease
    c1_c6_381de4ee_refcounted_after_close_test.go:53: KeyMaterial after Close with no other ref -> err=provider instance is closed acquire=false closed=true
--- PASS: TestVerifyC1_RefusedOnlyAfterLastRelease (0.00s)
PASS
ok  	google.golang.org/grpc/internal/xds/balancer/clusterimpl	0.006s
```

Observed: with another reference outstanding, `Close()` leaves the wrapper
fully open — a fresh `Acquire()` returns `true` and a fresh `KeyMaterial()`
is forwarded to the underlying provider (`err=<nil>`, `underlyingCalls=1`).
Refusal only begins once the reference count reaches zero. **Verdict: CONFIRMED**
(the Confirm condition — a newly started `Acquire`/`KeyMaterial` accepted after
`Close` while another owner remains — is exactly what was observed).

Impact: the retired provider is never put into a draining state; any code path
that still holds the old `HandshakeInfo` can keep starting new loads on it
indefinitely, and the provider closes only when the last such caller happens
to leave. The wrapper does refuse work once refs hit zero, so this is a
liveness/ownership-clarity issue, not a use-after-close.

### C1 on evalon/grpc-go-xd-3a8572bc

Source facts (`internal/credentials/xds/handshake_info.go`, lines 139–176):

```go
func NewSharedProvider(p certprovider.Provider) *SharedProvider {
	return &SharedProvider{provider: p, refs: 1}
}
func (p *SharedProvider) KeyMaterial(ctx context.Context) (*certprovider.KeyMaterial, error) {
	return p.provider.KeyMaterial(ctx)
}
func (p *SharedProvider) Close() {
	p.release()
}
func (p *SharedProvider) acquire() bool {
	p.mu.Lock(); defer p.mu.Unlock()
	if p.refs == 0 { return false }
	p.refs++
	return true
}
```

`KeyMaterial` does not consult the reference count at all — it always forwards
to the wrapped provider, even after `Close` and even after the count hit zero.

Repro: `verify/repro/c1_3a8572bc_sharedprovider_after_close_test.go`.

```sh
cp verify/repro/c1_3a8572bc_sharedprovider_after_close_test.go ~/wt/3a8572bc/internal/credentials/xds/
cd ~/wt/3a8572bc && go test ./internal/credentials/xds -run TestVerifyC1_ -v -count=1
```

```console
=== RUN   TestVerifyC1_SharedProviderAdmitsWorkAfterClose
    c1_3a8572bc_sharedprovider_after_close_test.go:31: after Close: refs=1 underlying.closed=false
    c1_3a8572bc_sharedprovider_after_close_test.go:34: fresh acquireProvider() after Close() -> true (refs=2)
    c1_3a8572bc_sharedprovider_after_close_test.go:36: fresh KeyMaterial() after Close() -> err=<nil> underlyingCalls=1
    c1_3a8572bc_sharedprovider_after_close_test.go:42: after last release: underlying.closed=true
--- PASS: TestVerifyC1_SharedProviderAdmitsWorkAfterClose (0.00s)
=== RUN   TestVerifyC1_SharedProviderKeyMaterialAfterFullClose
    c1_3a8572bc_sharedprovider_after_close_test.go:52: after full Close: underlying.closed=true acquire=false KeyMaterial err=<nil> underlyingCalls=1
    c1_3a8572bc_sharedprovider_after_close_test.go:54: CONFIRMED: KeyMaterial forwarded to an already-closed underlying provider
--- PASS: TestVerifyC1_SharedProviderKeyMaterialAfterFullClose (0.00s)
PASS
ok  	google.golang.org/grpc/internal/credentials/xds	0.003s
```

Observed: after `Close()` with one handshake reference outstanding, a fresh
`acquireProvider()` succeeds and a fresh `KeyMaterial()` is forwarded. Worse,
after the *last* reference is gone (`underlying.closed=true`, `acquire=false`),
`KeyMaterial()` is still forwarded to the closed underlying provider
(`underlyingCalls=1`). **Verdict: CONFIRMED.**

Impact: `SharedProvider.KeyMaterial` is an unguarded direct load. Any caller
holding a `*SharedProvider` (or a `HandshakeInfo` pointing at one) can load
from a provider whose underlying instance has already been closed; with the
real `certprovider` store that surfaces as a handshake error from a
`closedProvider`, with a provider whose cache survives `Close` it silently
returns stale material.

---

## C2

**Claim:** after holding the selected validation-root provider / handshake
snapshot fails, the client handshake invokes `KeyMaterial` on that selection
without ownership or a successful replacement retry.
Branch: evalon/grpc-go-xd-4dc02aec.

Source facts (`internal/credentials/xds/handshake_info.go`, lines 193–216):

```go
func AcquireHandshakeInfo(hiPtr *atomic.Pointer[HandshakeInfo]) (*HandshakeInfo, func()) {
	...
	for {
		hi := hiPtr.Load()
		if hi == nil { return nil, func() {} }
		if hi.acquire() { return hi, hi.Release }
		// ... If hiPtr still holds hi, the owner is done with it for
		// good (e.g. the LB policy was closed) and there is no replacement to
		// wait for: hand out hi as is and let its closed providers fail the
		// handshake.
		if hiPtr.Load() == hi { return hi, func() {} }
	}
}
```

`credentials/xds/xds.go`, lines 122–129: `ClientHandshake` calls
`AcquireHandshakeInfo`, `defer release()`, checks `hi == nil` / `UseFallbackCreds`
and then proceeds to `hi.ClientSideTLSConfig(ctx)` — no check that the hold
succeeded. The real trigger for "released without replacement" is
`clusterImplBalancer.Close` (`internal/xds/balancer/clusterimpl/clusterimpl.go`
line 512: `b.xdsHIPtr.Load().Release()` without swapping the pointer).

Repro: `verify/repro/c2_c5_4dc02aec_failed_hold_keymaterial_test.go`
(publishes a `HandshakeInfo` whose root provider records `KeyMaterial` calls
and whether `Close` preceded them; the owner releases its only reference
without replacing the publication; a client handshake then selects it).

```sh
cp verify/repro/c2_c5_4dc02aec_failed_hold_keymaterial_test.go ~/wt/4dc02aec/credentials/xds/
cd ~/wt/4dc02aec && go test ./credentials/xds -run '^Test$/^VerifyC2C5' -v -count=1
```

```console
=== RUN   Test/VerifyC2C5_FailedHoldStillInvokesKeyMaterial
    c2_c5_4dc02aec_failed_hold_keymaterial_test.go:61: after owner Release(): root.closed=true
    c2_c5_4dc02aec_failed_hold_keymaterial_test.go:65: AcquireHandshakeInfo after failed hold -> same dead hi=true
    c2_c5_4dc02aec_failed_hold_keymaterial_test.go:67: release() after failed hold is a no-op: root still closed=true, kmCalls=0
    c2_c5_4dc02aec_failed_hold_keymaterial_test.go:73: ClientHandshake err=<nil>; root.kmCalls=1 kmAfterClose=1
    c2_c5_4dc02aec_failed_hold_keymaterial_test.go:78: handshake SUCCEEDED using key material from an already-Closed provider
--- PASS: Test (0.01s)
    --- PASS: Test/VerifyC2C5_FailedHoldStillInvokesKeyMaterial (0.01s)
PASS
ok  	google.golang.org/grpc/credentials/xds	0.011s
```

Observed, part *Failed-hold handling*: `AcquireHandshakeInfo` returned the
same dead `HandshakeInfo` (`same dead hi=true`) with a no-op release.
Part *Handshake use*: `ClientHandshake` invoked `KeyMaterial` on that
selection after its provider had been closed (`kmCalls=1 kmAfterClose=1`),
and the TLS handshake completed successfully with that material.
**Verdict: CONFIRMED** (both parts).

Impact: a connection attempt that races the teardown of its cluster's LB
policy (channel close, cluster removal) is handed a `HandshakeInfo` whose
providers were already closed and proceeds to load from them. With a provider
whose cached material outlives `Close` the handshake silently succeeds on
retired trust material; with the stock `certprovider` store it fails with a
"provider instance is closed" error instead of a clean "configuration
released" error. The comment in the code documents this as intentional
("let its closed providers fail the handshake"), but nothing makes the closed
providers fail.

---

## C3

**Claim:** when root-provider construction succeeds and identity-provider
construction fails, `handleSecurityConfig` abandons the new root provider
without closing or transferring it. Adjudicated per branch.

Source facts — identical on all three branches
(`internal/xds/balancer/clusterimpl/clusterimpl.go`, ce8a38bc lines 363–370,
817a8ca3 lines 362–369, c29df868 lines 362–369):

```go
	var identityProvider certprovider.Provider
	if name, cert := config.IdentityInstanceName, config.IdentityCertName; name != "" {
		var err error
		identityProvider, err = buildProvider(cpc, name, cert, true, false)
		if err != nil {
			return err
		}
	}
```

`rootProvider` (built a few lines earlier) is neither closed nor stored on the
error return.

Repro: `verify/repro/c3_c7_root_provider_leak_test.go` (overrides the
package-level `buildProvider` hook so the `wantRoot` build returns a recording
provider and the identity build returns an error; calls
`handleSecurityConfig`; checks `root.closed` and whether any `HandshakeInfo`
was published). The test deliberately fails with `t.Fatal` when the leak is
present so the outcome is visible in the exit status.

```sh
for w in ce8a38bc 817a8ca3 c29df868; do
  cp verify/repro/c3_c7_root_provider_leak_test.go ~/wt/$w/internal/xds/balancer/clusterimpl/
  (cd ~/wt/$w && go test ./internal/xds/balancer/clusterimpl -run TestVerifyC3_ -v -count=1)
done
```

Output on evalon/grpc-go-xd-ce8a38bc:

```console
=== RUN   TestVerifyC3_RootProviderLeakOnIdentityBuildFailure
    c3_c7_root_provider_leak_test.go:33: buildProvider(root instance "root-instance") -> success
    c3_c7_root_provider_leak_test.go:36: buildProvider(identity instance "identity-instance") -> forced error
    c3_c7_root_provider_leak_test.go:50: handleSecurityConfig returned err=forced identity provider build failure
    c3_c7_root_provider_leak_test.go:51: root.closed=false publishedHandshakeInfo=false
    c3_c7_root_provider_leak_test.go:56: CONFIRMED: root provider neither closed nor transferred to a published owner on identity build failure
--- FAIL: TestVerifyC3_RootProviderLeakOnIdentityBuildFailure (0.00s)
FAIL
FAIL	google.golang.org/grpc/internal/xds/balancer/clusterimpl	0.006s
```

Output on evalon/grpc-go-xd-817a8ca3 and evalon/grpc-go-xd-c29df868: identical
line for line to the above (verified with `diff` after normalising the
duration): `root.closed=false publishedHandshakeInfo=false`, `--- FAIL`.

Control run of the same repro on evalon/grpc-go-xd-4dc02aec, whose
`handleSecurityConfig` closes the root on the error path:

```console
=== RUN   TestVerifyC3_RootProviderLeakOnIdentityBuildFailure
    c3_c7_root_provider_leak_test.go:33: buildProvider(root instance "root-instance") -> success
    c3_c7_root_provider_leak_test.go:36: buildProvider(identity instance "identity-instance") -> forced error
    c3_c7_root_provider_leak_test.go:50: handleSecurityConfig returned err=forced identity provider build failure
    c3_c7_root_provider_leak_test.go:51: root.closed=true publishedHandshakeInfo=false
--- PASS: TestVerifyC3_RootProviderLeakOnIdentityBuildFailure (0.00s)
PASS
```

The probe distinguishes a closing implementation from a leaking one.
**Verdict: CONFIRMED on all three branches** (ce8a38bc, 817a8ca3, c29df868).

Impact: each Cluster update whose identity-provider instance fails to build
(e.g. a bootstrap cert-provider misconfiguration or a plugin that fails on
`Build`) leaks one `certprovider` store reference for the root instance; the
root provider (file watcher goroutine and its refcount in the global store)
is never closed for the life of the process. Repeated NACK/retry cycles
accumulate leaked instances.

---

## C4

**Claim:** the changed client handshake path panics when the snapshot holder
exists but its current snapshot is nil. Adjudicated per branch.

Source facts. evalon/grpc-go-xd-3a8572bc, `credentials/xds/xds.go` lines
116–126:

```go
	hiPtr := xdsinternal.HandshakeInfoFromAttributes(chi.Attributes)
	if hiPtr == nil {
		return c.fallback.ClientHandshake(ctx, authority, rawConn)
	}
	hi := hiPtr.Load()
	if hi == nil {
		return c.fallback.ClientHandshake(ctx, authority, rawConn)
	}
	if hi.UseFallbackCreds() {
```

evalon/grpc-go-xd-9085e702, `credentials/xds/xds.go` lines 122–129 route
through `AcquireHandshakeInfo`, which returns `(nil, nil)` for a nil current
snapshot; `defer hi.Release()` and `hi.UseFallbackCreds()` are both nil-safe
(`internal/credentials/xds/handshake_info.go` lines 183–186 and 230–235:
`if hi == nil { return }` / `if hi == nil { return true }`).

Repro: `verify/repro/c4_nil_snapshot_no_panic_test.go` (publishes an
`atomic.Pointer[HandshakeInfo]` holder with no value, runs `ClientHandshake`
against a TLS test server under `recover()`).

```sh
for w in 3a8572bc 9085e702; do
  cp verify/repro/c4_nil_snapshot_no_panic_test.go ~/wt/$w/credentials/xds/
  (cd ~/wt/$w && go test ./credentials/xds -run '^Test$/^VerifyC4' -v -count=1)
done
```

Output — identical on both branches:

```console
=== RUN   Test/VerifyC4_NilCurrentSnapshotDoesNotPanic
    c4_nil_snapshot_no_panic_test.go:44: holder non-nil, Load()==nil: panicked=<nil> err=<nil>
    c4_nil_snapshot_no_panic_test.go:52: REFUTED: handshake completed via fallback credentials without panic
--- PASS: Test (0.01s)
    --- PASS: Test/VerifyC4_NilCurrentSnapshotDoesNotPanic (0.01s)
PASS
ok  	google.golang.org/grpc/credentials/xds	0.011s
```

Observed: `panicked=<nil>`, `err=<nil>` — the handshake completed via the
fallback credentials. **Verdict: REFUTED on both branches.**

---

## C5

**Claim:** a client handshake invokes provider `KeyMaterial` on an unchanged
published snapshot after acquiring ownership of that snapshot fails.
Branch: evalon/grpc-go-xd-4dc02aec. Same run as C2; key lines repeated here
so this section stands alone.

Source facts (`internal/credentials/xds/handshake_info.go`, lines 202–214):

```go
		if hi.acquire() {
			return hi, hi.Release
		}
		...
		if hiPtr.Load() == hi {
			return hi, func() {}
		}
```

`credentials/xds/xds.go` line 122–129: `ClientHandshake` uses the returned
`hi` unconditionally (`hi == nil` and `UseFallbackCreds` are the only guards)
and continues into `hi.ClientSideTLSConfig(ctx)` which calls
`rootProvider.KeyMaterial`.

```sh
cp verify/repro/c2_c5_4dc02aec_failed_hold_keymaterial_test.go ~/wt/4dc02aec/credentials/xds/
cd ~/wt/4dc02aec && go test ./credentials/xds -run '^Test$/^VerifyC2C5' -v -count=1
```

```console
    c2_c5_4dc02aec_failed_hold_keymaterial_test.go:61: after owner Release(): root.closed=true
    c2_c5_4dc02aec_failed_hold_keymaterial_test.go:65: AcquireHandshakeInfo after failed hold -> same dead hi=true
    c2_c5_4dc02aec_failed_hold_keymaterial_test.go:67: release() after failed hold is a no-op: root still closed=true, kmCalls=0
    c2_c5_4dc02aec_failed_hold_keymaterial_test.go:73: ClientHandshake err=<nil>; root.kmCalls=1 kmAfterClose=1
    c2_c5_4dc02aec_failed_hold_keymaterial_test.go:78: handshake SUCCEEDED using key material from an already-Closed provider
--- PASS: Test (0.01s)
```

Part *Snapshot acquisition*: `AcquireHandshakeInfo` returned the unchanged
dead snapshot with a no-op release (`same dead hi=true`; `release()` left
`kmCalls=0` and changed nothing). Part *Provider access*: `ClientHandshake`
called the root provider's `KeyMaterial` once, after `Close`
(`kmCalls=1 kmAfterClose=1`). **Verdict: CONFIRMED** (both parts).

Impact: as for C2 — a handshake racing LB-policy teardown loads from closed
providers instead of stopping; the outcome depends on what the provider does
after `Close` rather than on the ownership protocol.

---

## C6

**Claim:** a production provider wrapper continues admitting fresh `Acquire`
or `KeyMaterial` operations after `Close` initiates shutdown when another
reference remains. Branch: evalon/grpc-go-xd-381de4ee. Same run as C1
(381de4ee); key lines repeated here.

Source facts (`internal/xds/balancer/clusterimpl/clusterimpl.go` lines
170–198): `Acquire` succeeds for any `refs > 0`; `Close` is
`closeOnce.Do(p.Release)`; `KeyMaterial` does `Acquire`/`defer Release`
around the underlying call. The solution's own test
`TestRefCountedProviderRetirement` (`balancer_test.go`) exercises the
refcount but only asserts that the underlying provider closes after the last
release; it does not assert refusal of new work after `Close`.

```sh
cp verify/repro/c1_c6_381de4ee_refcounted_after_close_test.go ~/wt/381de4ee/internal/xds/balancer/clusterimpl/
cd ~/wt/381de4ee && go test ./internal/xds/balancer/clusterimpl -run TestVerifyC1_ -v -count=1
```

```console
    c1_c6_381de4ee_refcounted_after_close_test.go:31: after Close: refs=1 underlying.closed=false
    c1_c6_381de4ee_refcounted_after_close_test.go:34: fresh Acquire() after Close() -> true (refs=2)
    c1_c6_381de4ee_refcounted_after_close_test.go:39: fresh KeyMaterial() after Close() -> err=<nil> underlyingCalls=1
    c1_c6_381de4ee_refcounted_after_close_test.go:44: after last Release: underlying.closed=true
--- PASS: TestVerifyC1_AcquireAndKeyMaterialAdmittedAfterClose (0.00s)
```

Observed: with one extra reference held, `Close()` was followed by a fresh
`Acquire()` returning `true` and a fresh `KeyMaterial()` forwarded to the
underlying provider. **Verdict: CONFIRMED.**

Impact: identical to C1/381de4ee — `Close` does not start a drain; the
wrapper stays fully open for new work until the count independently reaches
zero.

---

## C7

**Claim:** `handleSecurityConfig` leaks a root provider when root-provider
creation succeeds and identity-provider creation fails before publication.
Branch: evalon/grpc-go-xd-d215d00f.

Source facts (`internal/xds/balancer/clusterimpl/clusterimpl.go` lines
368–374):

```go
	if name, cert := config.IdentityInstanceName, config.IdentityCertName; name != "" {
		var err error
		identityProvider, err = buildProvider(cpc, name, cert, true, false)
		if err != nil {
			return err
		}
	}
```

Repro: `verify/repro/c3_c7_root_provider_leak_test.go` (same probe as C3).

```sh
cp verify/repro/c3_c7_root_provider_leak_test.go ~/wt/d215d00f/internal/xds/balancer/clusterimpl/
cd ~/wt/d215d00f && go test ./internal/xds/balancer/clusterimpl -run TestVerifyC3_ -v -count=1
```

```console
=== RUN   TestVerifyC3_RootProviderLeakOnIdentityBuildFailure
    c3_c7_root_provider_leak_test.go:33: buildProvider(root instance "root-instance") -> success
    c3_c7_root_provider_leak_test.go:36: buildProvider(identity instance "identity-instance") -> forced error
    c3_c7_root_provider_leak_test.go:50: handleSecurityConfig returned err=forced identity provider build failure
    c3_c7_root_provider_leak_test.go:51: root.closed=false publishedHandshakeInfo=false
    c3_c7_root_provider_leak_test.go:56: CONFIRMED: root provider neither closed nor transferred to a published owner on identity build failure
--- FAIL: TestVerifyC3_RootProviderLeakOnIdentityBuildFailure (0.00s)
FAIL
FAIL	google.golang.org/grpc/internal/xds/balancer/clusterimpl	0.006s
```

Control (same file on evalon/grpc-go-xd-4dc02aec): `root.closed=true
publishedHandshakeInfo=false`, `--- PASS`. **Verdict: CONFIRMED.**

Impact: as for C3 — every identity-build failure leaks a live root provider
instance (store refcount + watcher) for the life of the process.

---

## C8

**Claim:** `HandshakeInfo` invokes certificate-provider `Close` callbacks while
holding its ownership mutex. Branch: evalon/grpc-go-xd-7f9f59d2.

Source facts (`internal/credentials/xds/handshake_info.go` lines 167–200):

```go
func (hi *HandshakeInfo) Release() {
	hi.mu.Lock()
	defer hi.mu.Unlock()
	hi.activeHandshakes--
	hi.maybeCloseProvidersLocked()
}
func (hi *HandshakeInfo) Close() {
	...
	hi.mu.Lock()
	defer hi.mu.Unlock()
	hi.closed = true
	hi.maybeCloseProvidersLocked()
}
func (hi *HandshakeInfo) maybeCloseProvidersLocked() {
	if !hi.closed || hi.activeHandshakes > 0 || hi.providersClosed { return }
	hi.providersClosed = true
	if hi.rootProvider != nil { hi.rootProvider.Close() }
	if hi.identityProvider != nil { hi.identityProvider.Close() }
}
```

Repro: `verify/repro/c8_7f9f59d2_close_under_mutex_test.go` (providers whose
`Close` calls `hi.mu.TryLock()`; if the lock cannot be taken, `mu` is held by
the caller).

```sh
cp verify/repro/c8_7f9f59d2_close_under_mutex_test.go ~/wt/7f9f59d2/internal/credentials/xds/
cd ~/wt/7f9f59d2 && go test ./internal/credentials/xds -run TestVerifyC8_ -v -count=1
```

```console
=== RUN   TestVerifyC8_CloseInvokesProviderCloseUnderMu
    c8_7f9f59d2_close_under_mutex_test.go:37: Close path: root.Close calls=1 muHeld=true; identity.Close calls=1 muHeld=true
--- PASS: TestVerifyC8_CloseInvokesProviderCloseUnderMu (0.00s)
=== RUN   TestVerifyC8_FinalReleaseInvokesProviderCloseUnderMu
    c8_7f9f59d2_close_under_mutex_test.go:55: after Close with active handshake: root.Close calls=0
    c8_7f9f59d2_close_under_mutex_test.go:57: Release path: root.Close calls=1 muHeld=true; identity.Close calls=1 muHeld=true
--- PASS: TestVerifyC8_FinalReleaseInvokesProviderCloseUnderMu (0.00s)
PASS
ok  	google.golang.org/grpc/internal/credentials/xds	0.003s
```

Observed: on both the owner-`Close` path and the final-`Release` path, both
provider `Close` callbacks ran with `hi.mu` held (`muHeld=true`).
**Verdict: CONFIRMED.**

Impact: provider `Close` implementations run on the caller's goroutine
under `hi.mu`. Any provider whose `Close` blocks (e.g. waits for a watcher
goroutine that is itself inside `KeyMaterial` → `hi` accessors) or re-enters
`HandshakeInfo` methods deadlocks the handshake path. Stock providers'
`Close` is non-blocking, so it is a latent hazard rather than an observed
failure in the shipped configuration.

---

## C9

**Claim:** provider-replacement handshake tests use expiration of a fixed
`time.After` window to assert that the old provider remains open.
Branch: evalon/grpc-go-xd-9c0c4257. This claim is about what the named tests
assert, so the assertion text plus a demonstration run is the evidence.

Repro: `verify/repro/c9_9c0c4257_time_after_assertion.sh`.

```sh
cd ~/wt/9c0c4257 && bash ~/repos/grpc-go/verify/repro/c9_9c0c4257_time_after_assertion.sh
```

```console
== defaultTestShortTimeout values
credentials/xds/xds_client_test.go:49:	defaultTestShortTimeout = 10 * time.Millisecond
internal/xds/balancer/clusterimpl/tests/balancer_test.go:80:	defaultTestShortTimeout = 100 * time.Millisecond
== time.After used as 'still open' evidence
credentials/xds/xds_client_test.go-779-	// must not be closed yet.
credentials/xds/xds_client_test.go-780-	select {
credentials/xds/xds_client_test.go-781-	case <-root1.closed:
credentials/xds/xds_client_test.go-782-		t.Fatalf("Replaced root provider was closed while a handshake was still using it")
credentials/xds/xds_client_test.go:783:	case <-time.After(defaultTestShortTimeout):
credentials/xds/xds_client_test.go-784-	}
--
internal/xds/balancer/clusterimpl/tests/clusterimpl_security_test.go-1039-	// not have been closed.
internal/xds/balancer/clusterimpl/tests/clusterimpl_security_test.go-1040-	select {
internal/xds/balancer/clusterimpl/tests/clusterimpl_security_test.go-1041-	case <-blockedRoot.closed:
internal/xds/balancer/clusterimpl/tests/clusterimpl_security_test.go-1042-		t.Fatal("Replaced root provider was closed while a handshake was still loading roots from it")
internal/xds/balancer/clusterimpl/tests/clusterimpl_security_test.go:1043:	case <-time.After(defaultTestShortTimeout):
internal/xds/balancer/clusterimpl/tests/clusterimpl_security_test.go-1044-	}
== demonstration runs
=== RUN   Test/ClientCredsProviderReplacedDuringHandshake
--- PASS: Test (0.02s)
    --- PASS: Test/ClientCredsProviderReplacedDuringHandshake (0.02s)
ok  	google.golang.org/grpc/credentials/xds	0.027s
=== RUN   Test/SecurityConfigUpdate_ReplacedDuringHandshake
--- PASS: Test (0.13s)
    --- PASS: Test/SecurityConfigUpdate_ReplacedDuringHandshake (0.12s)
ok  	google.golang.org/grpc/internal/xds/balancer/clusterimpl/tests	0.133s
```

Observed: `TestClientCredsProviderReplacedDuringHandshake`
(`credentials/xds/xds_client_test.go:725`, assertion at line 780–784) and
`TestSecurityConfigUpdate_ReplacedDuringHandshake`
(`internal/xds/balancer/clusterimpl/tests/clusterimpl_security_test.go:943`,
assertion at line 1040–1044) both contain a `select` whose success branch is
`<-time.After(defaultTestShortTimeout)` — 10 ms and 100 ms respectively —
i.e. "the provider is still open because `closed` did not fire within the
window". Both tests pass. **Verdict: CONFIRMED.**

Nuance recorded for fairness: in both tests the timed check is followed by an
operation-result check (after unblocking, the handshake / RPC must succeed
using the old provider's roots, and the blocking provider returns an error
from `KeyMaterial` if closed), so a premature close would also be caught by
the later assertion. The fixed-window `select` is therefore redundant rather
than the sole evidence — but it is present and, on a slow CI machine, a
10 ms/100 ms window can expire before a buggy close would have happened,
making that particular check pass vacuously.

---

## C10

**Claim:** direct-owned server `HandshakeInfo` objects receive an initial
snapshot-retention reference that the server lifecycle never releases.
Branch: evalon/grpc-go-xd-74048b3d. The claim names
`NewRefCountedHandshakeInfo`; no such function exists on the branch — the
nearest equivalent is `NewHandshakeInfo` itself, which now embeds the
reference count.

Source facts. `internal/credentials/xds/handshake_info.go` (diff vs base):

```go
type HandshakeInfo struct {
	...
	refs *grpcsync.RefCounted[struct{}]
}
func NewHandshakeInfo(rootProvider, identityProvider certprovider.Provider, ...) *HandshakeInfo {
	hi := &HandshakeInfo{...}
	hi.refs, _ = grpcsync.NewRefCounted(struct{}{}, func() {
		if rootProvider != nil { rootProvider.Close() }
		if identityProvider != nil { identityProvider.Close() }
	})
	return hi
}
func (hi *HandshakeInfo) Acquire() bool { return hi.refs.TryIncrement() }
func (hi *HandshakeInfo) Release()      { hi.refs.Decrement() }
```

`internal/grpcsync/refcounted.go`: `NewRefCounted` starts with
`rc.refCount.Store(1)`. `internal/xds/server/conn_wrapper.go`:
`XDSHandshakeInfo()` (line 122) returns `NewHandshakeInfo(c.rootProvider,
c.identityProvider, ...)`; `connWrapper.Close()` (lines 150–158) closes
`c.identityProvider` / `c.rootProvider` directly and never touches the
`HandshakeInfo`. `credentials/xds/xds.go` `ServerHandshake` never calls
`Acquire`/`Release`. `grep -rn "\.Release()"` over the non-test production
files finds only the client (`credentials/xds/xds.go:130`) and the clusterimpl
balancer (`clusterimpl.go:379,509`).

Repro: `verify/repro/c10_74048b3d_server_hi_retention_test.go` (registers a
recording `certprovider` plugin, builds a real bootstrap config referencing it,
constructs a `connWrapper` over a `net.Pipe` with an mTLS `securityCfg`,
calls `XDSHandshakeInfo()` then `connWrapper.Close()`, and probes the
`HandshakeInfo` reference count with `Acquire`/`Release`).

```sh
cp verify/repro/c10_74048b3d_server_hi_retention_test.go ~/wt/74048b3d/internal/xds/server/
cd ~/wt/74048b3d && go test ./internal/xds/server -run '^TestC10ServerHandshakeInfoRetentionRefNeverReleased$' -v -count=1
```

```console
=== RUN   TestC10ServerHandshakeInfoRetentionRefNeverReleased
    c10_74048b3d_server_hi_retention_test.go:83: after XDSHandshakeInfo(): provider Close calls=0
    c10_74048b3d_server_hi_retention_test.go:89: after connWrapper.Close(): provider Close calls=2
    c10_74048b3d_server_hi_retention_test.go:92: hi.Acquire() after connWrapper.Close() -> true (retention ref still outstanding)
    c10_74048b3d_server_hi_retention_test.go:101: after manually releasing the initial ref: provider Close calls=2 (onZero ran only now; its second Close is absorbed by singleCloseWrappedProvider)
    c10_74048b3d_server_hi_retention_test.go:105: hi.Acquire() after final release -> false
    c10_74048b3d_server_hi_retention_test.go:110: CONFIRMED: server HandshakeInfo keeps its initial retention reference after connWrapper.Close(); refcount does not reflect provider liveness
--- PASS: TestC10ServerHandshakeInfoRetentionRefNeverReleased (0.00s)
PASS
ok  	google.golang.org/grpc/internal/xds/server	0.007s
```

Observed, part *Constructor semantics*: the server-created `HandshakeInfo`
carries an outstanding reference — `hi.Acquire()` succeeds, and a single
manual `Release()` drives the count to zero (subsequent `Acquire()` → `false`),
so exactly one retention reference was handed out by `NewHandshakeInfo`.
Part *Server lifecycle*: after `connWrapper.Close()` — the end of the server
connection lifecycle — the providers had been closed directly (`Close calls=2`)
yet `hi.Acquire()` still returned `true`; the retention reference was never
released by the server path. **Verdict: CONFIRMED** (both parts).

Impact: no resource leak — `connWrapper.Close` closes the providers directly
and the `HandshakeInfo` is garbage-collected. The defect is that the
ownership model is dead on the server side: a server `HandshakeInfo` reports
itself as live (`Acquire()==true`) after its providers are closed, so any
future server-side caller that adopts the client's `Acquire`/`Release`
protocol would be told the snapshot is usable when it is not. The
documented contract on `NewHandshakeInfo` ("owns the providers until its
initial reference … released") is not honoured by the server owner.
