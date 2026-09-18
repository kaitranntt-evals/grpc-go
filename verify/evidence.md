# Audit evidence — grpc-go-xds-certificate-provider-closure-race (run v-d422a467)

Environment: `go version go1.25.7 linux/amd64`, module `google.golang.org/grpc`, base commit
`cc234554fb363aea445a838b341bb8a65c8305b0`. Main checkout: `~/repos/grpc-go` on
`verify/grpc-go-xds-certificate-provider-closure-race-v-d422a467` (from
`origin/grpc-go-xds-certificate-provider-closure-race-perfect`, HEAD `4f781286`).

Claim-target branches live in a second repository; they were fetched and checked out into
detached worktrees:

```sh
cd ~/repos/grpc-go
git remote add evalrepo https://github.com/kaitranntt-evals/grpc-go-xds-certificate-provider-closure-race.git
for b in 04ccfc62 1e9d8011 332d40dd f55f758b 96fc0a0c 1f161fb4 c697e37b a31d723d; do
  git fetch evalrepo evalon/grpc-go-xd-$b
  git worktree add --detach ~/wt/$b FETCH_HEAD
done
```

Worktree HEADs: 04ccfc62=f655ba9c, 1e9d8011=a99a2b48, 332d40dd=07182d1e, f55f758b=5babeb80,
96fc0a0c=c10cb383, 1f161fb4=ac417f05, c697e37b=e2ea43ab, a31d723d=54e78fe7.

Every temporary instrumentation/mutation below was applied inside a worktree by a script under
`verify/repro/` and reverted with `git checkout -- .` (each section shows `git status --short`
empty afterwards). Standalone Go repros carry `//go:build ignore` so they never take part in the
repository build; the run line in each file strips that tag while copying the file into its
target package.

Eval fixtures (from `eval_tests.zip`, byte-exact, SHA-256 verified against the archive copies)
were provisioned at `.evaltools/candidate_test_inventory.go`,
`internal/xds/balancer/clusterimpl/tests/eval_handshake_lifetime_test.go`,
`test/run_eval_xds_test_group.sh`, `test/run_candidate_tests.sh` in the main checkout (left
uncommitted). On the audited branch:

```console
$ bash ./test/run_eval_xds_test_group.sh handshake-lifetime | tail -3
{"Action":"pass","Test":"TestEval_SecurityConfigUpdate_ActiveHandshakeKeepsProvider", ...}
ok  	google.golang.org/grpc/.evaltools/lifetime_...	0.052s
EXIT=0
$ go build ./... ; echo BUILD_EXIT=$?
BUILD_EXIT=0
$ bash ./test/run_eval_xds_test_group.sh affected-test-compile; echo EXIT=$?
EXIT=0
$ bash ./test/run_candidate_tests.sh | grep -E '^\[(PASS|FAIL)\]' | sort | uniq -c | head -3 ; echo EXIT=$?
   ...  [PASS] ...        # every detected candidate test passed, no [FAIL]
EXIT=0
```

---

## C1

**Claim:** the solution's new/changed Go tests do not include a client-handshake
validation-root-loading test that establishes replacement/retirement before unblocking the load,
detects premature closure, and asserts completion. **Branch:** `evalon/grpc-go-xd-04ccfc62`
(worktree `~/wt/04ccfc62`, HEAD f655ba9c). **Verdict: REFUTED.**

### New/changed tests

```console
$ cd ~/wt/04ccfc62 && BASE=$(git merge-base HEAD origin/master)
$ git diff --stat $BASE HEAD | tail -6
 credentials/xds/xds.go                             |   9 +-
 internal/credentials/xds/handshake_info.go         |  82 +++++-
 internal/credentials/xds/handshake_info_test.go    | 136 +++++++++
 internal/xds/balancer/clusterimpl/clusterimpl.go   |  45 ++-
 .../clusterimpl/tests/clusterimpl_security_test.go | 309 ++++++++++++++++++++-
$ git diff $BASE HEAD -- '*_test.go' | grep -E '^\+func \(s\) Test'
+func (s) TestAcquireHandshakeInfo_ReleasedByOwnerDuringHandshake(t *testing.T) {
+func (s) TestAcquireHandshakeInfo_ReleasedWithoutReplacement(t *testing.T) {
+func (s) TestSecurityConfigUpdate_DuringHandshake(t *testing.T) {
```

`TestAcquireHandshakeInfo_OwnerClosesDuringRootLoad` named in the claim does not exist on this
branch; the nearest equivalent is `TestAcquireHandshakeInfo_ReleasedByOwnerDuringHandshake`.

### Synchronization trace — `TestAcquireHandshakeInfo_ReleasedByOwnerDuringHandshake` (`internal/credentials/xds/handshake_info_test.go:670-731`)

Provider fake: `closeTrackingProvider.KeyMaterial` signals `loadStarted`, blocks on `unblockLoad`,
and returns `provider instance is closed` if `Close()` was called (lines 635-651).

1. l.678 `hi, release, err := AcquireHandshakeInfo(&hiPtr)` — handshake takes its reference.
2. l.686 `hiPtr.Swap(replacement).Release()` — the owner **retires** the selected `HandshakeInfo`
   (replacement published, owner reference released) while the root provider's load is still
   blocked (`unblockLoad` not yet closed).
3. l.687 `if rootProvider.isClosed() || identityProvider.isClosed() { t.Fatal(...) }` — premature
   closure check #1.
4. l.694-702 `go hi.ClientSideTLSConfig(ctx, "")` … `<-rootProvider.loadStarted` — the
   validation-root load is started and is observed blocked inside `KeyMaterial`.
5. l.703 `if rootProvider.isClosed() { t.Fatal("Root provider closed while a handshake was loading from it") }`
   — premature closure check #2, before unblocking.
6. l.706 `close(rootProvider.unblockLoad)`; l.707-714 asserts `ClientSideTLSConfig` returned `nil`
   error (a closed provider would have returned `provider instance is closed`).
7. l.717-720 `release()` then asserts both providers closed; l.723-730 asserts a fresh acquire
   returns `replacement`.

Ordering nuance: retirement (step 2) happens before the load *starts* (step 4) rather than in the
middle of it; the task statement explicitly allows "retire or close the selected provider while
loading remains blocked", and the load is blocked from its first instruction until step 6.

### Synchronization trace — `TestSecurityConfigUpdate_DuringHandshake` (`clusterimpl_security_test.go:924-1086`)

l.1011-1018 waits `<-st.rootLoadStarted` (load blocked in the trusted provider) → l.1024
`mgmtServer.Update` with the replacement Cluster → l.1027-1037 waits for the replacement root
provider `built` event → l.1041-1047 fails on any `st.closed` event within `defaultTestShortTimeout`
→ l.1051 `unblockRootLoad()` → l.1052-1060 asserts RPC success + mTLS peer → l.1063-1073 waits for
the old root provider's `closed` event → l.1082 later connection ends in `TransientFailure`.
This test synchronizes on provider *construction* + elapsed time, not on application; instrumented
runs show application nevertheless preceded the unblock by ~100 ms in every observed run:

```console
$ # instrumentation: print in storeHandshakeInfo after old.Release(), and in unblockRootLoad
$ go test ./internal/xds/balancer/clusterimpl/tests/ -run 'Test/SecurityConfigUpdate_DuringHandshake' -count=5 -v 2>&1 | grep -E 'AUDIT|^--- |^ok'
AUDIT storeHandshakeInfo released old at 10:58:57.875896035   # initial config replaces placeholder
AUDIT storeHandshakeInfo released old at 10:58:57.877042434   # replacement applied (old released)
AUDIT test unblocking root load at 10:58:57.977888914
AUDIT storeHandshakeInfo released old at 10:58:57.987836033   # later endpoint-only updates
AUDIT storeHandshakeInfo released old at 10:58:57.996412158
--- PASS: Test (0.13s)
(same pattern in all 5 runs)
$ git checkout -- . && git status --short
```

### Demonstration runs

```console
$ go test ./internal/credentials/xds/ -run 'Test/AcquireHandshakeInfo' -count=3 -v 2>&1 | grep -E '^\s*--- |^ok' | sort | uniq -c
      3     --- PASS: Test/AcquireHandshakeInfo_ReleasedByOwnerDuringHandshake (0.00s)
      3     --- PASS: Test/AcquireHandshakeInfo_ReleasedWithoutReplacement (0.00s)
      1 ok  	google.golang.org/grpc/internal/credentials/xds	0.007s
$ go test ./internal/xds/balancer/clusterimpl/tests/ -run 'Test/SecurityConfigUpdate_DuringHandshake' -count=10
ok  	google.golang.org/grpc/internal/xds/balancer/clusterimpl/tests	1.306s
```

### Sensitivity (does the test detect premature closure?)

Mutation of `internal/credentials/xds/handshake_info.go` `Release()`:
`if hi.refs.Add(-1) != 0 { return }` → `if hi.refs.Add(-1) < 0 { return }` (closes providers on the
owner's release even though the handshake still holds a reference):

```console
$ sed -i 's/if hi.refs.Add(-1) != 0 {/if hi.refs.Add(-1) < 0 {/' internal/credentials/xds/handshake_info.go
$ go test ./internal/credentials/xds/ -run 'Test/AcquireHandshakeInfo_ReleasedByOwnerDuringHandshake' -count=1 -v 2>&1 | grep -E '_test.go:|^\s*--- |^FAIL'
    handshake_info_test.go:688: Providers closed while a handshake still holds a reference to the HandshakeInfo
--- FAIL: Test (0.00s)
    --- FAIL: Test/AcquireHandshakeInfo_ReleasedByOwnerDuringHandshake (0.00s)
FAIL	google.golang.org/grpc/internal/credentials/xds	0.003s
$ git checkout -- . && git status --short
```

**Conclusion:** at least one new test (`TestAcquireHandshakeInfo_ReleasedByOwnerDuringHandshake`)
starts client-handshake validation-root loading via `ClientSideTLSConfig`, establishes retirement of
the selected `HandshakeInfo`/provider before the load is unblocked, checks the provider is not
closed before unblocking (and the fake would surface a closed provider as a load error), and asserts
the load completes. The claim's confirm condition ("every proposed overlap test either releases the
load before establishing replacement or retirement, does not exercise validation-root loading, or
cannot detect premature provider closure before unblocking") does not hold → REFUTED.

---

## C2

**Claim:** `go vet ./...` from the repository root does not complete successfully.
**Branch:** `evalon/grpc-go-xd-1e9d8011` (worktree `~/wt/1e9d8011`, HEAD a99a2b48).
**Verdict: REFUTED.**

```console
$ cd ~/wt/1e9d8011 && git status --short | wc -l
0
$ go version
go version go1.25.7 linux/amd64
$ (time go vet ./...); echo EXIT=$?
real	0m2.290s        # cached; the first, uncached run earlier in the session also printed nothing and exited 0
user	0m11.175s
sys	0m2.810s
EXIT=0
```

No diagnostics were printed, and no dependency retrieval failure occurred (all modules were
already present in the module cache; the first run of the session completed its downloads and
also exited 0). The tool completed with exit status zero → REFUTED.

---

## C3

**Claim:** the added `hi1.ClientSideTLSConfig(context.Background(), "")` call in
`internal/xds/balancer/clusterimpl/balancer_test.go` is rejected by the test-context rule in
`scripts/vet.sh`. **Branch:** `evalon/grpc-go-xd-332d40dd` (worktree `~/wt/332d40dd`, HEAD
07182d1e). **Verdict: CONFIRMED.** Repro: `verify/repro/c3_context_rule.sh`.

The rule (`scripts/vet.sh:80`, `not()` is `! "$@"` from `scripts/common.sh:8-12`):

```sh
git grep -e 'context.Background()' --or -e 'context.TODO()' -- "*_test.go" | grep -v "benchmark/primitives/context_test.go" | grep -v 'context.WithTimeout(' | not grep -v 'context.WithCancel('
```

The call is an addition of this branch:

```console
$ cd ~/wt/332d40dd && git diff $(git merge-base HEAD origin/master) HEAD -- internal/xds/balancer/clusterimpl/balancer_test.go | grep -n '^+.*context.Background'
160:+	if _, err := hi1.ClientSideTLSConfig(context.Background(), ""); err != nil {
```

Executing the rule verbatim on the branch and on the base commit:

```console
$ bash ~/repos/grpc-go/verify/repro/c3_context_rule.sh
== added ClientSideTLSConfig call(s) in balancer_test.go:
496:	if _, err := hi1.ClientSideTLSConfig(context.Background(), ""); err != nil {
== scripts/vet.sh context rule on the working tree:
internal/xds/balancer/clusterimpl/balancer_test.go:	if _, err := hi1.ClientSideTLSConfig(context.Background(), ""); err != nil {
RULE_EXIT=1
== same rule on the base commit cc234554fb363aea445a838b341bb8a65c8305b0:
BASE_RULE_EXIT=0
```

The rule passes on the base commit (no offenders) and fails on the branch with exactly one
offending line — the added call. `scripts/vet.sh` runs under `set -ex`, so this line aborts the
whole vet script → CONFIRMED.

Impact reasoning: `scripts/vet.sh` is the repository's pre-submit static check (`vet.sh` is what
grpc-go CI runs); the branch as submitted cannot pass it. Fix is one line: use a
`context.WithTimeout(context.Background(), defaultTestTimeout)` context in the test.

---

## C5

**Claim:** applying a security configuration equivalent to the active one rebuilds the handshake
ownership state instead of preserving provider handles and the snapshot.
**Branch:** `evalon/grpc-go-xd-f55f758b` (worktree `~/wt/f55f758b`, HEAD 5babeb80).
**Verdict: CONFIRMED.** Repro: `verify/repro/c5_equivalent_security_config_test.go`.

Source trace: `handleSecurityConfig` (`internal/xds/balancer/clusterimpl/clusterimpl.go:334-382`)
has no `config.Equal(b.securityConfig)` short-circuit on this branch (`grep -n securityConfig
clusterimpl.go` returns nothing); every call with a non-nil config runs `buildProvider` for the
root (and identity) instance and ends in `b.setHandshakeInfo(xds.NewHandshakeInfo(...))` (l.381),
which swaps the published snapshot and releases the previous one.

The repro overrides the package-level `buildProvider` hook with a fake that counts handles and
records `Close()`, applies a Cluster with `RootInstanceName: "root-instance"` twice (identical
`SecurityConfig`, `Equal` = true) and inspects the balancer's `xdsHIPtr` snapshot after each update:

```console
$ cd ~/wt/f55f758b && sed '/^\/\/go:build ignore$/d' ~/repos/grpc-go/verify/repro/c5_equivalent_security_config_test.go > internal/xds/balancer/clusterimpl/audit_c5_test.go
$ go test ./internal/xds/balancer/clusterimpl/ -run 'Test/AuditC5' -count=1 -v 2>&1 | grep -E 'AUDIT|RESULT|^\s*--- |^ok'
AUDIT buildProvider("root-instance") -> handle #1
AUDIT after update #1: handles built=1, HandshakeInfo=0xc000053bc0
AUDIT SecurityConfig.Equal(update #1, update #2) = true
AUDIT buildProvider("root-instance") -> handle #2
AUDIT after equivalent update #2: handles built=2, HandshakeInfo=0xc000053d40 (same as #1: false), handle #1 closed=true
    audit_c5_test.go:137: RESULT: equivalent update REBUILT ownership state: 1 new handle(s) acquired, snapshot replaced=true, previous handle closed=true (claim C5 confirmed)
--- PASS: Test (0.00s)
    --- PASS: Test/AuditC5_EquivalentSecurityConfigRebuildsOwnership (0.00s)
ok  	google.golang.org/grpc/internal/xds/balancer/clusterimpl	0.010s
$ rm internal/xds/balancer/clusterimpl/audit_c5_test.go && git status --short
```

Observed: a second, equivalent update acquires a new provider handle, publishes a new
`HandshakeInfo` pointer, and closes the previous handle → CONFIRMED.

Impact reasoning: any xDS Cluster push that leaves the security config unchanged (endpoint-only
churn is the everyday case — the C1/C6 traces above show two extra `storeHandshakeInfo` releases
per endpoint update) re-acquires certificate-provider handles and replaces the snapshot. With the
real `certprovider` store this is a ref-count churn on the shared provider (and, with the
solution's lifetime model, an extra retire/close cycle per push); in-flight handshakes are
protected by the acquire/release counting, so the effect is wasted work and unnecessary provider
churn, not a correctness failure observed in this audit.

---

## C6

**Claim:** `TestSecurityConfigUpdate_DuringHandshake` can unblock validation-root loading before
the client applies the replacement configuration, because waiting for provider construction plus
elapsed time does not establish application completion.
**Branch:** `evalon/grpc-go-xd-332d40dd` (worktree `~/wt/332d40dd`, HEAD 07182d1e).
**Verdict: CONFIRMED.** Repro: `verify/repro/c6_delay_instrumentation.py`.

Synchronization trace (`internal/xds/balancer/clusterimpl/tests/clusterimpl_security_test.go`,
test `TestSecurityConfigUpdate_DuringHandshake`): after `mgmtServer.Update(ctx, resources)` the
test loops on the provider `built` channel until `ev.name == "untrusted" && ev.opts.WantRoot &&
!ev.closed`, then waits `<-time.After(defaultTestShortTimeout)` (10 ms), then
`close(ctrl.rootLoadUnblock)`. Nothing between the update and the unblock observes
`resolver.ClientConn.UpdateState`, the balancer's `replaceHandshakeInfo`, or the retirement of the
old provider. Replacement application on this branch is
`replaceHandshakeInfo(hi)` = `old := b.xdsHIPtr.Swap(hi); old.Release()`.

The instrumentation script prints a nanosecond timestamp after `replaceHandshakeInfo` applies and
when the test closes `rootLoadUnblock`, and can inject a sleep immediately before
`replaceHandshakeInfo` (simulating a slow balancer-goroutine schedule):

```console
$ cd ~/wt/332d40dd && python3 ~/repos/grpc-go/verify/repro/c6_delay_instrumentation.py 0 && go test ./internal/xds/balancer/clusterimpl/tests/ -run 'Test/SecurityConfigUpdate_DuringHandshake' -count=3 -v 2>&1 | grep -E 'AUDIT|^--- |^ok'
instrumented with delay_ms=0
AUDIT replaceHandshakeInfo applied (old released) at 1789729247641592120   # initial config
AUDIT replaceHandshakeInfo applied (old released) at 1789729247642457520   # replacement applied
AUDIT test unblocking root load at 1789729247742586896                     # +100 ms later
AUDIT replaceHandshakeInfo applied (old released) at 1789729247753636076   # endpoint update
--- PASS: Test (0.13s)
(same ordering in runs 2 and 3)
ok  	google.golang.org/grpc/internal/xds/balancer/clusterimpl/tests	0.389s
$ git checkout -- .
$ python3 ~/repos/grpc-go/verify/repro/c6_delay_instrumentation.py 500 && go test ./internal/xds/balancer/clusterimpl/tests/ -run 'Test/SecurityConfigUpdate_DuringHandshake' -count=3 -v 2>&1 | grep -E 'AUDIT|^--- |^ok'
instrumented with delay_ms=500
AUDIT replaceHandshakeInfo applied (old released) at 1789729249425839179   # initial config
AUDIT test unblocking root load at 1789729249527811940                     # UNBLOCK ...
AUDIT replaceHandshakeInfo applied (old released) at 1789729249927348002   # ... 400 ms BEFORE replacement applied
AUDIT replaceHandshakeInfo applied (old released) at 1789729250431033911
--- PASS: Test (1.52s)
(same ordering in runs 2 and 3)
ok  	google.golang.org/grpc/internal/xds/balancer/clusterimpl/tests	4.576s
$ git checkout -- . && git status --short
```

With the delay, the test unblocks the root load ~400 ms before the replacement is applied and
still **passes**: the overlap it is meant to cover never happened in those runs. Without the
delay the ordering happens to be right only because application is fast (~1 ms) relative to the
10 ms grace and the ~100 ms provider-build path.

Sensitivity check on the same branch (that the assertion would notice a real bug when the ordering
is right): mutating `Release()` to close providers on the owner's release
(`hi.refs.Add(-1) != 0` → `< 0`) makes the un-delayed test fail:

```console
$ sed -i 's/if hi.refs.Add(-1) != 0 {/if hi.refs.Add(-1) < 0 {/' internal/credentials/xds/handshake_info.go
$ go test ./internal/xds/balancer/clusterimpl/tests/ -run 'Test/SecurityConfigUpdate_DuringHandshake' -count=1 -v 2>&1 | grep -E '_test.go:10|^\s*--- |^FAIL'
    clusterimpl_security_test.go:1033: Provider {CertName: WantRoot:true WantIdentity:false} was closed while a handshake using it was in progress
--- FAIL: Test (20.06s)
    --- FAIL: Test/SecurityConfigUpdate_DuringHandshake (20.06s)
FAIL	google.golang.org/grpc/internal/xds/balancer/clusterimpl/tests	20.069s
$ git checkout -- . && git status --short
```

Impact reasoning: on a slow/loaded CI machine (exactly where the original flake lives), the
regression test can silently degrade into "replace after the handshake finished" and pass without
exercising the race, so a future regression of the provider-lifetime fix could go unnoticed. The
eval fixture `eval_handshake_lifetime_test.go` shows the fix: wrap the balancer's
`resolver.ClientConn` and wait on an `applied` channel signalled from `UpdateState` (or wait for the
old provider's retirement) before unblocking.

---

## C7

**Claim:** when identity-provider construction fails after root-provider acquisition, the
unpublished root-provider handle is leaked.
**Branch:** `evalon/grpc-go-xd-96fc0a0c` (worktree `~/wt/96fc0a0c`, HEAD c10cb383).
**Verdict: CONFIRMED.** Repro: `verify/repro/c7_identity_build_failure_leak_test.go`.

Source trace (`internal/xds/balancer/clusterimpl/clusterimpl.go:360-374`):

```go
rp, err := buildProvider(cpc, config.RootInstanceName, config.RootCertName, false, true)
if err != nil {
	return err
}
rootProvider = rp
...
identityProvider, err = buildProvider(cpc, name, cert, true, false)
if err != nil {
	return err          // rootProvider is neither closed nor stored anywhere
}
```

The repro overrides `buildProvider`: the root build returns a close-tracking fake, the identity
build returns an injected error. It then applies a Cluster with both instance names and checks the
root handle after the failed update and after tearing the balancer down:

```console
$ cd ~/wt/96fc0a0c && sed '/^\/\/go:build ignore$/d' ~/repos/grpc-go/verify/repro/c7_identity_build_failure_leak_test.go > internal/xds/balancer/clusterimpl/audit_c7_test.go
$ go test ./internal/xds/balancer/clusterimpl/ -run 'Test/AuditC7' -count=1 -v 2>&1 | grep -E 'AUDIT|RESULT|^\s*--- |^ok'
AUDIT buildProvider("root-instance", root=true) -> handle #1 acquired
AUDIT buildProvider("identity-instance", identity) -> injected error
AUDIT UpdateClientConnState error: received Cluster resource that contains invalid security config: audit: injected identity provider construction failure
AUDIT root provider "root-instance" closed after failed update: false
AUDIT root provider "root-instance" closed after balancer Close(): false
    audit_c7_test.go:122: RESULT: root provider handle "root-instance" LEAKED (never closed) after identity provider construction failed (claim C7 confirmed)
--- PASS: Test (0.00s)
    --- PASS: Test/AuditC7_IdentityBuildFailureLeaksRootProvider (0.00s)
ok  	google.golang.org/grpc/internal/xds/balancer/clusterimpl	0.009s
$ rm internal/xds/balancer/clusterimpl/audit_c7_test.go && git status --short
```

The root handle is never closed — not on the error return and not even when the balancer is
closed → CONFIRMED.

Impact reasoning: each Cluster update whose identity provider fails to build (bad/unknown
`certificate_provider_instance` name or plugin config for the identity cert while the root
instance is valid — a plausible operator misconfiguration, and every re-push of that Cluster
repeats it) leaks one reference on the shared root `certprovider` store entry, so that provider
instance is never released. Fix: `rootProvider.Close()` in the identity error branch (or build both
before acquiring).

---

## C9

**Claim:** calling `HandshakeInfo.Close` twice while a validation-root load holds a reference
consumes that active reference and closes the provider before the load finishes, contrary to the
documented idempotence. **Branch:** `evalon/grpc-go-xd-1e9d8011` (worktree `~/wt/1e9d8011`, HEAD
a99a2b48). **Verdict: CONFIRMED.** Repro: `verify/repro/c9_double_close_during_load_test.go`.

Source (`internal/credentials/xds/handshake_info.go:157-172`):

```go
// ... Close is idempotent.
func (hi *HandshakeInfo) Close() {
	hi.mu.Lock()
	if hi.refs == 0 {
		hi.mu.Unlock()
		return
	}
	hi.refs--              // decrements whatever reference is left, including a load's
	last := hi.refs == 0
	hi.mu.Unlock()
	if last {
		hi.closeProviders()
	}
}
```

The repro builds a `HandshakeInfo` over a root provider whose `KeyMaterial` blocks, starts
`ClientSideTLSConfig` (which acquires the load reference), waits for the load to start, calls
`Close()` twice, then unblocks the load:

```console
$ cd ~/wt/1e9d8011 && sed '/^\/\/go:build ignore$/d' ~/repos/grpc-go/verify/repro/c9_double_close_during_load_test.go > internal/credentials/xds/audit_c9_test.go
$ go test ./internal/credentials/xds/ -run 'Test/AuditC9' -count=1 -v 2>&1 | grep -E 'AUDIT|RESULT|^\s*--- |^ok'
AUDIT root load in progress; root provider closed=false
AUDIT after Close() #1 (owner release): root provider closed=false
AUDIT after Close() #2 (redundant): root provider closed=true
AUDIT ClientSideTLSConfig returned err=<nil>
AUDIT after load release: root provider closed=true
    audit_c9_test.go:84: RESULT: second Close() closed the root provider while a load still held a reference (claim C9 confirmed)
--- PASS: Test (0.00s)
    --- PASS: Test/AuditC9_DoubleCloseDuringRootLoad (0.00s)
ok  	google.golang.org/grpc/internal/credentials/xds	0.004s
$ rm internal/credentials/xds/audit_c9_test.go && git status --short
```

The second `Close()` consumed the load's reference and closed the provider while the load was
still blocked inside it → CONFIRMED. (`ClientSideTLSConfig` still returned `nil` here only because
the fake provider had already been handed its key material request before closing; a real
provider closed mid-load returns an error/stale material.)

Impact reasoning: `Close` is documented idempotent, and the balancer's `Close()` plus a later
`handleSecurityConfig(nil)`/replacement path can both reach `Close` on the same object; any such
double call during an in-flight handshake re-introduces the exact premature-closure race the
solution is meant to fix. Fix: make the owner reference a separate `closed` flag (or `sync.Once`)
so repeated `Close` calls do not decrement handshake references.

---

## C10

**Claim:** the repository's tests do not exercise contending acquisitions and releases against
the ownership counter used by `HandshakeInfo`.
**Branch:** `evalon/grpc-go-xd-1f161fb4` (worktree `~/wt/1f161fb4`, HEAD ac417f05).
**Verdict: CONFIRMED.** Repro: `verify/repro/c10_contention_probe.py`.

Ownership implementation (`internal/credentials/xds/handshake_info.go`): `HandshakeInfo` carries
its own `refs atomic.Int32`; `Acquire()` is a `Load`/`CompareAndSwap` loop that fails when
`n <= 0`, `Release()` does `refs.Add(-1)` and closes both providers when it reaches 0. It does
**not** use `internal/grpcsync.RefCounted`:

```console
$ cd ~/wt/1f161fb4 && grep -n "RefCounted\|grpcsync" internal/credentials/xds/handshake_info.go | wc -l
0
$ grep -c HandshakeInfo internal/grpcsync/refcounted_test.go
0
$ grep -rn "func (s) Test.*Concurren" --include=*_test.go internal/credentials/xds credentials/xds internal/xds/balancer/clusterimpl internal/grpcsync
internal/grpcsync/callback_serializer_test.go:105:func (s) TestCallbackSerializer_Schedule_Concurrent(t *testing.T) {
internal/grpcsync/refcounted_test.go:120:func (s) TestRefCounted_Concurrent(t *testing.T) {
```

`TestRefCounted_Concurrent` hammers `grpcsync.RefCounted`, a different type that `HandshakeInfo`
does not use on this branch, so it does not exercise this ownership state.

Dynamic probe: the script wraps `Acquire`/`Release` with a per-`HandshakeInfo` in-flight counter
(plus a 50 µs sleep inside the call to make genuinely concurrent callers overlap observably) and
prints one line per call; the awk summary reports the total and the maximum concurrent callers on
any single `HandshakeInfo`. Run over every test package that reaches this code:

```console
$ python3 ~/repos/grpc-go/verify/repro/c10_contention_probe.py && go test ./credentials/xds/ ./internal/credentials/xds/ ./internal/xds/balancer/clusterimpl/... -count=1 -v 2>&1 | awk '/^AUDIT C10/{n++; if($5+0>max)max=$5+0} /^(ok|FAIL|---)/{print} END{print "AUDIT C10 total Acquire/Release calls=" n ", max concurrent callers on one HandshakeInfo=" max}'
instrumented internal/credentials/xds/handshake_info.go
--- PASS: Test (0.53s)
ok  	google.golang.org/grpc/credentials/xds	0.538s
--- PASS: Test (0.10s)
ok  	google.golang.org/grpc/internal/credentials/xds	0.103s
--- PASS: Test (0.01s)
ok  	google.golang.org/grpc/internal/xds/balancer/clusterimpl	0.015s
--- PASS: Test (3.86s)
ok  	google.golang.org/grpc/internal/xds/balancer/clusterimpl/tests	3.876s
AUDIT C10 total Acquire/Release calls=98, max concurrent callers on one HandshakeInfo=1
$ go test ./test/xds/... -count=1 -v 2>&1 | awk '/^AUDIT C10/{n++; if($5+0>max)max=$5+0} /^(ok|FAIL)/{print} END{print "AUDIT C10 total Acquire/Release calls=" n ", max concurrent callers on one HandshakeInfo=" max}'
ok  	google.golang.org/grpc/test/xds	6.594s
AUDIT C10 total Acquire/Release calls=158, max concurrent callers on one HandshakeInfo=1
$ git checkout -- . && git status --short
```

256 probed `Acquire`/`Release` calls across the credential, balancer and xDS end-to-end suites; at
no point were two callers inside the counter of the same `HandshakeInfo` → no repository test
creates contending acquisitions/releases against this ownership state → CONFIRMED.

Impact reasoning: the CAS loop in `Acquire` and the `Add(-1)`-then-close in `Release` are exactly
the paths where a lost-update or acquire-after-zero bug would hide, and they are only ever run
single-file by the suite; a `-race` run of the suite therefore cannot vouch for them. Next: add a
unit test that runs N goroutines doing `Acquire`/`Release` against one `HandshakeInfo` while the
owner releases, asserting providers close exactly once and only after the last release.

---

## C11

**Claim:** `TestSecurityConfigUpdate_ProviderReplacedDuringHandshake` fails at its
`AwaitState(IDLE)` expectation when automatic reconnection moves the channel to
`TRANSIENT_FAILURE`. **Branch:** `evalon/grpc-go-xd-c697e37b` (worktree `~/wt/c697e37b`, HEAD
e2ea43ab). **Verdict: CONFIRMED.** Repro: `verify/repro/c11_awaitstate_idle_race.sh`.

Test source (`internal/xds/balancer/clusterimpl/tests/clusterimpl_security_test.go:1106-1112`):

```go
rlis.Stop()
rlis.Restart()
testutils.AwaitState(ctx, t, cc, connectivity.Idle)
_, err = client.EmptyCall(ctx, &testpb.Empty{})
if status.Code(err) != codes.Unavailable || !strings.Contains(err.Error(), "authentication handshake failed") {
```

Runs (unmodified test, `-race`, `-cpu 1,2,4`, 30 iterations each = 90 runs):

```console
$ cd ~/wt/c697e37b && bash ~/repos/grpc-go/verify/repro/c11_awaitstate_idle_race.sh 30 | tail -4
== summary
runs:   90
failed: 15
'got TRANSIENT_FAILURE; want IDLE' diagnostics: 15
$ grep 'want IDLE' /tmp/c11_out.txt | sort | uniq -c
     15     clusterimpl_security_test.go:1109: Timed out waiting for state change.  got TRANSIENT_FAILURE; want IDLE
$ grep -E '^\s*--- FAIL' /tmp/c11_out.txt | head -2
--- FAIL: Test (5.06s)
    --- FAIL: Test/SecurityConfigUpdate_ProviderReplacedDuringHandshake (5.06s)
```

Same without the race detector (90 runs):

```console
$ go test ./internal/xds/balancer/clusterimpl/tests/ -run 'Test/SecurityConfigUpdate_ProviderReplacedDuringHandshake' -count=30 -cpu 1,2,4 -v 2>&1 | grep -E '^\s*--- (PASS|FAIL): Test/|want IDLE' | sed -E 's/\([0-9.]+s\)//' | sort | uniq -c
      2     --- FAIL: Test/SecurityConfigUpdate_ProviderReplacedDuringHandshake
     88     --- PASS: Test/SecurityConfigUpdate_ProviderReplacedDuringHandshake
      2     clusterimpl_security_test.go:1109: Timed out waiting for state change.  got TRANSIENT_FAILURE; want IDLE
```

Connectivity trace of a failing run (temporary `t.Logf("AUDIT calling AwaitState(IDLE); current
state=%v", cc.GetState())` inserted before l.1109 — which shifts the assertion to l.1110 — with
`GRPC_GO_LOG_SEVERITY_LEVEL=info GRPC_GO_LOG_VERBOSITY_LEVEL=2`; reverted afterwards; key lines):

```console
$ GRPC_GO_LOG_SEVERITY_LEVEL=info GRPC_GO_LOG_VERBOSITY_LEVEL=2 go test -race ./internal/xds/balancer/clusterimpl/tests/ -run 'Test/SecurityConfigUpdate_ProviderReplacedDuringHandshake' -count=30 -cpu 1,2,4 -v > /tmp/c11_trace.log 2>&1; echo EXIT=$?; grep -c 'want IDLE' /tmp/c11_trace.log
EXIT=1
14
$ # first failing subtest, channel-level lines only (prefix `tlogger.go:133: INFO clientconn.go:NNN [core]` stripped):
    [Channel #88] Channel Connectivity change to CONNECTING  (t=+2.598453ms)
    [Channel #88] Channel Connectivity change to READY  (t=+38.899771ms)
    clusterimpl_security_test.go:1109: AUDIT calling AwaitState(IDLE); current state=READY
    [Channel #88] Channel Connectivity change to IDLE  (t=+40.534033ms)
    [Channel #88] Channel Connectivity change to CONNECTING  (t=+40.764037ms)
    [Channel #88 SubChannel #94] Subchannel Connectivity change to TRANSIENT_FAILURE, last error: connection error: desc = "transport: authentication handshake failed: x509: certificate signed by unknown authority"  (t=+53.306174ms)
    [Channel #88] Channel Connectivity change to TRANSIENT_FAILURE  (t=+53.714029ms)
    clusterimpl_security_test.go:1110: Timed out waiting for state change.  got TRANSIENT_FAILURE; want IDLE
$ git checkout -- . && git status --short
```

The channel was `IDLE` for only ~230 µs (t=+40.53 ms → +40.76 ms) before the automatic reconnect
moved it to `CONNECTING` and then `TRANSIENT_FAILURE` (handshake rejected by the untrusted roots
— the behaviour the test wants to prove), so `AwaitState(IDLE)` never observes `IDLE`.

`AwaitState` polls `cc.GetState()`/`WaitForStateChange`; when the channel's automatic reconnect
leaves `IDLE` for `CONNECTING`→`TRANSIENT_FAILURE` before the poll observes `IDLE` (which is what
the connection drop triggers, since the pending RPC/keepalive reconnects immediately), the wait
never sees `IDLE` and times out after `defaultTestTimeout` (5 s) → CONFIRMED (17 % failure rate
under `-race`, ~2 % without).

Impact reasoning: this is a flaky regression test in the solution — the very class of problem the
task set out to fix — that will fail in CI at a double-digit rate under the race detector; the
`TRANSIENT_FAILURE` it observes is in fact the *desired* end state (untrusted roots rejected).
Fix: await `TransientFailure` (or issue the RPC and check the `Unavailable`/handshake error
directly) instead of `Idle`, or observe `Idle` through a state subscriber before restarting the
listener.

---

## C12

**Claim:** `TestAcquireHandshakeInfo_OwnerClosesDuringRootLoad` checks provider closure after
receiving a result sent before the sender's deferred ownership release, without synchronization
ordering release completion before the assertion.
**Branch:** `evalon/grpc-go-xd-a31d723d` (worktree `~/wt/a31d723d`, HEAD 54e78fe7).
**Verdict: CONFIRMED.** Repro: `verify/repro/c12_pause_before_release.py`.

Source trace (`internal/credentials/xds/handshake_info_test.go`, test at l.670):

```go
cfgCh := make(chan error, 1)
go func() {
	defer release()                      // l.688 — runs AFTER the send below
	_, err := hi.ClientSideTLSConfig(ctx, "")
	cfgCh <- err                         // l.690 — result sent first
}()
...
close(oldRoot.unblock)
select {
case err := <-cfgCh:                     // l.719 — main goroutine resumes here
	...
}
if !oldRoot.isClosed() || !oldID.isClosed() {   // l.727 — asserts closure; no edge to release()
	t.Fatalf("Replaced providers not closed after the last handshake released them: ...")
}
```

The only happens-before edge is send→receive on `cfgCh`; `release()` executes after the send, so
nothing orders it before the `isClosed()` reads.

Unmodified test — it already flakes on this machine:

```console
$ cd ~/wt/a31d723d && git status --short | wc -l
0
$ go test ./internal/credentials/xds/ -run 'Test/AcquireHandshakeInfo_OwnerClosesDuringRootLoad' -count=50 -v 2>&1 | grep -E '^\s*--- FAIL|_test.go:|^ok|^FAIL' | sort | uniq -c
      1     --- FAIL: Test/AcquireHandshakeInfo_OwnerClosesDuringRootLoad (0.01s)
      1     handshake_info_test.go:727: Replaced providers not closed after the last handshake released them: root closed = false, identity closed = false
      1 FAIL	google.golang.org/grpc/internal/credentials/xds	0.560s
$ go test ./internal/credentials/xds/ -run 'Test/AcquireHandshakeInfo_OwnerClosesDuringRootLoad' -count=200 -cpu 1,2,4 -v 2>&1 | grep -E '^\s*--- (PASS|FAIL): Test/|_test.go:' | sed -E 's/\([0-9.]+s\)//' | sort | uniq -c
      5     --- FAIL: Test/AcquireHandshakeInfo_OwnerClosesDuringRootLoad
    595     --- PASS: Test/AcquireHandshakeInfo_OwnerClosesDuringRootLoad
      5     handshake_info_test.go:727: Replaced providers not closed after the last handshake released them: root closed = false, identity closed = false
```

(An earlier `-count=20` run on an idle machine passed 20/20; the failures above occurred while
another `go test -race` job was running, i.e. under ordinary CPU contention.)

Controlled schedule — pause the sender for 200 ms after the send, before the deferred release
(only the test file is edited):

```console
$ python3 ~/repos/grpc-go/verify/repro/c12_pause_before_release.py && go test ./internal/credentials/xds/ -run 'Test/AcquireHandshakeInfo_OwnerClosesDuringRootLoad' -count=3 -v 2>&1 | grep -E '^\s*--- |^ok|^FAIL|_test.go:'
instrumented internal/credentials/xds/handshake_info_test.go
    handshake_info_test.go:730: Replaced providers not closed after the last handshake released them: root closed = false, identity closed = false
--- FAIL: Test (0.21s)
    --- FAIL: Test/AcquireHandshakeInfo_OwnerClosesDuringRootLoad (0.21s)
(identical in runs 2 and 3)
FAIL	google.golang.org/grpc/internal/credentials/xds	0.651s
$ git checkout -- . && git status --short
```

Pausing before the release makes the assertion fail deterministically (3/3), and the unmodified
test fails on its own under load (6 of 650 runs) → CONFIRMED.

Impact reasoning: the test is flaky in the repository as submitted (fails ~1 % of runs under
load), and its failure message accuses the production code of not closing providers, so a real
regression and this scheduling flake are indistinguishable to a maintainer. Fix: signal after
`release()` (e.g. `release(); cfgCh <- err`, or a separate `done` channel closed after the deferred
release) and wait on that before asserting `isClosed()`.
