## Setup

Audited branch: `origin/grpc-go-xds-certificate-provider-closure-race-perfect` @ `3483b320` checked out as `verify/grpc-go-xds-certificate-provider-closure-race-v-db6eaa9f`. Base commit `cc234554`.

Claim-target branches were fetched from `https://github.com/kaitranntt-evals/grpc-go-xds-certificate-provider-closure-race.git` (remote `claims`) and checked out in worktrees `~/repos/wt-<suffix>`:

```console
$ git remote add claims https://github.com/kaitranntt-evals/grpc-go-xds-certificate-provider-closure-race.git
$ git fetch claims evalon/grpc-go-xd-0ce5f895 evalon/grpc-go-xd-6b4db8a0 evalon/grpc-go-xd-07f17f15 evalon/grpc-go-xd-eb179dd3 evalon/grpc-go-xd-fbf9efbf evalon/grpc-go-xd-34a8fdbb
$ for b in 0ce5f895 6b4db8a0 07f17f15 eb179dd3 fbf9efbf 34a8fdbb; do git worktree add -f ../wt-$b claims/evalon/grpc-go-xd-$b; done
$ git worktree add -f ../wt-base cc234554
0ce5f895 96f1971fb3dd7e7c0209b6756f1760c84b2fef20
6b4db8a0 e46b354e144bc763e47f305c150611f2b4e707ba
07f17f15 13a9a5bdf588df81a9b5c55acc03cbe4901ac8ef
eb179dd3 764565e7acb03fb10a849e19428201247985c1c5
fbf9efbf ce8a34ffa0b880da820d1922f7704f769984fa64
34a8fdbb ded05d609c36ea3948166c64a55392ac001fa6e1
```

Every branch has merge-base `cc234554` with the base commit. Toolchain: `go version go1.25.7 linux/amd64`.

Eval fixtures from `eval_tests.zip` were provisioned at the paths given in the prompt (`.evaltools/candidate_test_inventory.go`, `internal/xds/balancer/clusterimpl/tests/eval_handshake_lifetime_test.go`, `test/run_eval_xds_test_group.sh`, `test/run_candidate_tests.sh`); they are left untracked and are not committed.

Baseline on the audited branch:

```console
$ go build ./... ; echo BUILD_EXIT=$?
BUILD_EXIT=0
$ bash ./test/run_eval_xds_test_group.sh handshake-lifetime | tail -3
{"Action":"output","Test":"TestEval_SecurityConfigUpdate_ActiveHandshakeKeepsProvider","Output":"--- PASS: TestEval_SecurityConfigUpdate_ActiveHandshakeKeepsProvider (0.04s)\n"}
{"Action":"output","Output":"ok  \tgoogle.golang.org/grpc/.evaltools/lifetime_xg5zvW\t0.047s\n"}
$ go vet ./... ; echo VET=$?
VET=0
```

All Go repro files under `verify/repro/` carry a `//go:build verify_repro` constraint so they are inert for `go build ./...` / `go vet ./...`; run them with `-tags verify_repro` after copying into the target package directory as stated in each file's header.

## C1

Target branch: `claims/evalon/grpc-go-xd-0ce5f895` (`96f1971f`), worktree `~/repos/wt-0ce5f895`.

New/changed test files on that branch: `credentials/xds/xds_client_test.go`, `internal/credentials/xds/handshake_info_test.go`.

Tests claiming "load in progress before replacement" and how they synchronize:

1. `TestClientCredsProviderReplacedDuringHandshake` (`credentials/xds/xds_client_test.go`): `blockingRootProvider.KeyMaterial` closes `p.loading` on entry; the test waits `<-root1.loading` before `hiPtr.Store(hi2); hi1.Retire()`. This one waits for a KeyMaterial-path event.
2. `TestHandshakeInfoReleaseClosesProvidersAfterLastUser` (`internal/credentials/xds/handshake_info_test.go`). Test comment: "a validation root load which is in progress when the owner releases the HandshakeInfo completes using the still-open provider"; inline: "The owner replaces the configuration while the load is blocked." Synchronization:

```go
	if !hi.Acquire() { ... }
	kmCh := make(chan error, 1)
	go func() {
		_, err := root.KeyMaterial(ctx)
		kmCh <- err
	}()

	// The owner replaces the configuration while the load is blocked.
	hi.Retire()
```

`closeTrackingProvider.KeyMaterial` emits no event on entry (it only selects on `closed`/`ctx.Done()`/`proceed`), and there is no wait between `go root.KeyMaterial(ctx)` and `hi.Retire()`. The ordering rests on goroutine scheduling.

`TestClientCredsProviderReleasedBeforeHandshake` and `TestHandshakeInfoAcquireReleaseKeepsOwnerReference` do not claim an in-progress load at replacement time.

Runtime measurement (repro `verify/repro/c1_retire_before_load_test.go` mirrors the test's Acquire → `go KeyMaterial` → `Retire` sequence and records whether KeyMaterial had been entered when `Retire()` ran):

```console
$ cp ~/repos/grpc-go/verify/repro/c1_retire_before_load_test.go ~/repos/wt-0ce5f895/internal/credentials/xds/
$ cd ~/repos/wt-0ce5f895 && go test -tags verify_repro ./internal/credentials/xds/ -run '^Test$/^VerifyC1_RetireBeforeLoad$' -count=1 -v
=== RUN   Test/VerifyC1_RetireBeforeLoad
    c1_retire_before_load_test.go:73: Retire() ran BEFORE KeyMaterial() was entered in 2000 of 2000 iterations (100.0%)
--- PASS: Test (0.00s)
ok  	google.golang.org/grpc/internal/credentials/xds	0.007s
```

The branch's own tests pass (so the misordering is invisible to them):

```console
$ cd ~/repos/wt-0ce5f895 && go test ./internal/credentials/xds/ -run '^Test$/^HandshakeInfo(ReleaseClosesProvidersAfterLastUser|AcquireReleaseKeepsOwnerReference)$' -count=1 -v | grep -E '^(--- |ok|\s+--- )'
    --- PASS: Test/HandshakeInfoAcquireReleaseKeepsOwnerReference (0.00s)
    --- PASS: Test/HandshakeInfoReleaseClosesProvidersAfterLastUser (0.00s)
ok  	google.golang.org/grpc/internal/credentials/xds	0.004s
$ go test ./credentials/xds/ -run '^Test$/^ClientCredsProvider(ReplacedDuringHandshake|ReleasedBeforeHandshake)$' -count=1 -v | grep -E '^(--- |ok|\s+--- )'
    --- PASS: Test/ClientCredsProviderReleasedBeforeHandshake (0.01s)
    --- PASS: Test/ClientCredsProviderReplacedDuringHandshake (0.01s)
ok  	google.golang.org/grpc/credentials/xds	0.025s
```

Impact reasoning: on this machine the "load in progress when the owner releases" ordering that `TestHandshakeInfoReleaseClosesProvidersAfterLastUser` describes never actually held (0/2000); the test in fact exercises "Retire, then the load starts on an already-retired-but-still-held HandshakeInfo". It still passes because the handshake holds a reference, so the test does not prove what its comment states, and a regression that only breaks the true overlap (load already inside KeyMaterial when Retire runs) would not be caught by it. The other in-progress-load test on the branch (`TestClientCredsProviderReplacedDuringHandshake`) does synchronize correctly, so coverage of the scenario is not entirely absent.

Verdict: CONFIRMED.

## C2

Adjudicated independently on each of three branches. In all three, `credentials/xds/xds.go` `ClientHandshake` is changed to `hi, release := xdsinternal.HandshakeInfoFromAttributes(chi.Attributes).Acquire(); defer release()`.

### Branch evalon/grpc-go-xd-6b4db8a0 (`e46b354e`)

`internal/credentials/xds/handshake_info_manager.go`:

```go
func (m *HandshakeInfoManager) Update(hi *HandshakeInfo) {
	var next *refCountedHandshakeInfo
	if hi != nil { next = &refCountedHandshakeInfo{HandshakeInfo: hi, refs: 1} }
	m.mu.Lock()
	prev := m.current
	m.current = next
	m.mu.Unlock()
	m.release(prev)
}
func (m *HandshakeInfoManager) Acquire() (*HandshakeInfo, func()) {
	if m == nil { return nil, func() {} }
	m.mu.Lock()
	hi := m.current
	if hi != nil { hi.refs++ }
	m.mu.Unlock()
	if hi == nil { return nil, func() {} }
	return hi.HandshakeInfo, func() { m.release(hi) }
}
func (m *HandshakeInfoManager) release(hi *refCountedHandshakeInfo) { ... m.mu.Lock(); hi.refs--; closeProviders := hi.refs == 0; m.mu.Unlock(); ... }
```

Selection (`m.current`) and retention (`refs++`) happen under the same mutex that `Update` uses to swap `current` and that `release` uses to decrement; the owner's reference (`refs: 1`) is dropped only after the swap, so a published snapshot always has `refs >= 1` while it is `current`. There is no failure return from `Acquire` other than `current == nil` (absent configuration). It is mutex-protected, not lock-free atomic as the claim's suspicion text says.

Stress probe (`verify/repro/c2_acquire_stress_manager_test.go`: 16 goroutines acquire/load/release while the publisher replaces the configuration continuously for 2s; counts nil holds and closed-provider holds):

```console
$ cp ~/repos/grpc-go/verify/repro/c2_acquire_stress_manager_test.go ~/repos/wt-6b4db8a0/internal/credentials/xds/
$ cd ~/repos/wt-6b4db8a0 && go test -race -tags verify_repro ./internal/credentials/xds/ -run '^Test$/^VerifyC2_AcquireNeverFailsWhilePublished$' -count=1 -v
    c2_acquire_stress_manager_test.go:78: replacements=55856 acquires=1081122 nilHolds=0 closedAtAcquire=0 closedBeforeRelease=0
--- PASS: Test (2.00s)
ok  	google.golang.org/grpc/internal/credentials/xds	3.013s
```

Verdict for this branch: REFUTED.

### Branch evalon/grpc-go-xd-07f17f15 (`13a9a5bd`)

`internal/credentials/xds/handshake_info.go`:

```go
type HandshakeInfoPointer struct { mu sync.Mutex; hi *grpcsync.RefCounted[*HandshakeInfo] }
func (p *HandshakeInfoPointer) Store(hi *HandshakeInfo) {
	... next, _ = grpcsync.NewRefCounted(hi, func() { close providers })
	p.mu.Lock(); prev := p.hi; p.hi = next; p.mu.Unlock()
	if prev != nil { prev.Decrement() }
}
func (p *HandshakeInfoPointer) Acquire() (*HandshakeInfo, func()) {
	if p == nil { return nil, func() {} }
	p.mu.Lock(); defer p.mu.Unlock()
	if p.hi == nil { return nil, func() {} }
	p.hi.Increment()
	return p.hi.Value(), p.hi.Decrement
}
```

`Increment()` (not `TryIncrement`) is called under `p.mu` while `p.hi` is still the published pointer, whose owner reference is dropped by `Store` only after the swap under the same mutex; the hold cannot fail. Only `p.hi == nil` (absent configuration) yields nil.

```console
$ cp ~/repos/grpc-go/verify/repro/c2_acquire_stress_pointer_test.go ~/repos/wt-07f17f15/internal/credentials/xds/
$ cd ~/repos/wt-07f17f15 && go test -race -tags verify_repro ./internal/credentials/xds/ -run '^Test$/^VerifyC2_AcquireNeverFailsWhilePublished$' -count=1 -v
    c2_acquire_stress_pointer_test.go:79: replacements=69135 acquires=1682543 nilHolds=0 closedAtAcquire=0 closedBeforeRelease=0
--- PASS: Test (2.00s)
ok  	google.golang.org/grpc/internal/credentials/xds	3.015s
```

No `Resource already closed or dead` / `Refcount cannot be negative` log lines from `grpcsync` appeared in the output.

Verdict for this branch: REFUTED.

### Branch evalon/grpc-go-xd-eb179dd3 (`764565e7`)

`internal/credentials/xds/handshake_info.go` defines the same `HandshakeInfoPointer` shape as 07f17f15 (mutex + `grpcsync.RefCounted`; `Store` swaps under `p.mu` then `prev.Decrement()`; `Acquire` does `p.hi.Increment()` under `p.mu` and returns `p.hi.Value(), p.hi.Decrement`; nil only when `p.hi == nil`).

```console
$ cp ~/repos/grpc-go/verify/repro/c2_acquire_stress_pointer_test.go ~/repos/wt-eb179dd3/internal/credentials/xds/
$ cd ~/repos/wt-eb179dd3 && go test -race -tags verify_repro ./internal/credentials/xds/ -run '^Test$/^VerifyC2_AcquireNeverFailsWhilePublished$' -count=1 -v
    c2_acquire_stress_pointer_test.go:79: replacements=69698 acquires=1722693 nilHolds=0 closedAtAcquire=0 closedBeforeRelease=0
--- PASS: Test (2.00s)
ok  	google.golang.org/grpc/internal/credentials/xds	3.014s
```

Verdict for this branch: REFUTED.

## C3

Target branch: `claims/evalon/grpc-go-xd-fbf9efbf` (`ce8a34ff`), worktree `~/repos/wt-fbf9efbf`. Changed test files: `credentials/xds/xds_client_test.go`, `internal/credentials/xds/handshake_info_test.go`, `test/xds/xds_client_certificate_providers_test.go`.

Follow-up assertions after replacement:

- `TestClientCredsHandshakeInfoReplacedDuringHandshake` (`credentials/xds/xds_client_test.go`, new): initial roots `x509/server_ca_cert.pem` (trust the test server); replacement roots `x509/client_ca_cert.pem` (do not). After the in-progress handshake completes and `trustedRoot.closed.HasFired()`, a fresh `conn2` handshake asserts:

```go
	const wantErr = "x509: certificate signed by unknown authority"
	if _, _, err := creds.ClientHandshake(hsCtx, authority, conn2); err == nil || !strings.Contains(err.Error(), wantErr) {
		t.Fatalf("ClientHandshake() with replacement roots returned error %v, want error containing %q", err, wantErr)
	}
```

  Under prior roots the same handshake succeeds (that is what the first, in-progress handshake asserts via `compareAuthInfo`), so the x509 unknown-authority cause is distinguishable from the prior-root outcome.
- `TestClientSideXDS_SecurityConfigReplacedDuringHandshake` (`test/xds/...`, new): follow-up asserts only `codes.Unavailable` + `"authentication handshake failed"` (generic).
- Pre-existing `TestClientCredsProviderSwitch` (base) goes untrusted → trusted and asserts only success on the follow-up; the base had no follow-up x509 trust-cause assertion for a trusted → untrusted replacement.

Demonstration run and discrimination check (mutate a copy so the "replacement" carries the prior roots; the x509 assertion must then fail):

```console
$ cd ~/repos/wt-fbf9efbf && go test ./credentials/xds/ -run '^Test$/^ClientCredsHandshakeInfo(ReplacedDuringHandshake|ClosedWithoutReplacement)$' -count=1 -v | grep -E '^(--- |ok|\s+--- )'
    --- PASS: Test/ClientCredsHandshakeInfoClosedWithoutReplacement (0.05s)
    --- PASS: Test/ClientCredsHandshakeInfoReplacedDuringHandshake (0.07s)
ok  	google.golang.org/grpc/credentials/xds	0.130s
$ go test ./test/xds/ -run '^Test$/^ClientSideXDS_SecurityConfigReplacedDuringHandshake$' -count=1 -v | grep -E '^(--- |ok|\s+--- )'
    --- PASS: Test/ClientSideXDS_SecurityConfigReplacedDuringHandshake (0.04s)
ok  	google.golang.org/grpc/test/xds	0.047s

$ # mutation: in TestClientCredsHandshakeInfoReplacedDuringHandshake, build the replacement
$ # HandshakeInfo from the PRIOR (trusted) roots, i.e. change this one line:
$ #   untrustedRoot := makeRootProvider(t, "x509/client_ca_cert.pem")
$ # to
$ #   untrustedRoot := makeRootProvider(t, "x509/server_ca_cert.pem")
$ cp credentials/xds/xds_client_test.go /tmp/xds_client_test.go.orig && sed -i 's|untrustedRoot := makeRootProvider(t, "x509/client_ca_cert.pem")|untrustedRoot := makeRootProvider(t, "x509/server_ca_cert.pem")|' credentials/xds/xds_client_test.go
$ go test ./credentials/xds/ -run '^Test$/^ClientCredsHandshakeInfoReplacedDuringHandshake$' -count=1 -v | grep -E '^(--- |ok|FAIL|\s+--- )|xds_client_test.go:[0-9]+:'
    xds_client_test.go:817: ClientHandshake() with replacement roots returned error <nil>, want error containing "x509: certificate signed by unknown authority"
    --- FAIL: Test/ClientCredsHandshakeInfoReplacedDuringHandshake (0.07s)
FAIL	google.golang.org/grpc/credentials/xds	0.078s
$ cp /tmp/xds_client_test.go.orig credentials/xds/xds_client_test.go && git status --short   # clean
```

The new follow-up assertion fails when the follow-up attempt sees the prior roots and passes when it sees the replacement roots, so it is an added x509 trust-cause assertion that distinguishes replacement roots from prior roots.

Verdict: REFUTED.

## C4

Audited branch, test `TestSecurityConfigUpdate_ConcurrentHandshake` in `internal/xds/balancer/clusterimpl/tests/concurrent_handshake_test.go`.

Synchronization in the test before the blocked KeyMaterial call is released:

```go
	resources.Clusters[0] = calPerfectClientTLSCluster(t, clusterName, serviceName, instanceB)
	if err := mgmtServer.Update(ctx, resources); err != nil { ... }
	calPerfectWaitForChan(ctx, t, builtB, "timed out waiting for replacement provider to be built")

	releaseA()
```

`builtB` is signalled from `calPerfectProviderBuilder.ParseConfig`'s build function, which runs inside `clusterImplBalancer.buildProviders` → `buildProvider`, i.e. before `handleSecurityConfig` reaches:

```go
	newHI := xds.NewRefCountedHandshakeInfo(rootProvider, identityProvider, ...)
	b.securityConfig = config
	oldHI := b.xdsHIPtr.Swap(newHI)
	if oldHI != nil { oldHI.Decrement() }
```

No other wait precedes `releaseA()`. Runtime demonstration (`verify/repro/c4_release_before_swap_test.go`: same fixture and same builtB → releaseA synchronization, but provider B's build function parks after signalling `builtB`, so the Swap/Decrement provably has not happened while it is parked; provider A's KeyMaterial reports when it returns):

```console
$ cd ~/repos/grpc-go && cp verify/repro/c4_release_before_swap_test.go internal/xds/balancer/clusterimpl/tests/
$ go test -tags verify_repro ./internal/xds/balancer/clusterimpl/tests/ -run '^TestVerifyC4_ReleaseBeforeSwap$' -count=3 -v | grep -E 'event:|observed:|^(--- |ok|FAIL|PASS)'
    c4_release_before_swap_test.go:171: event: builtB received (provider B build function entered, still parked)
    c4_release_before_swap_test.go:174: event: releaseA() called
    c4_release_before_swap_test.go:176: event: blocked provider A KeyMaterial call resumed and RETURNED
    c4_release_before_swap_test.go:187: observed: provider A NOT closed, handleSecurityConfig still parked inside buildProviders -> xdsHIPtr not yet swapped, previous owner not yet released
    c4_release_before_swap_test.go:191: event: provider B build unparked
    c4_release_before_swap_test.go:198: event: active RPC completed successfully
    c4_release_before_swap_test.go:203: observed: provider A closed only after the build returned (swap + previous-owner release happened after KeyMaterial had already returned)
--- PASS: TestVerifyC4_ReleaseBeforeSwap (0.01s)
    (identical sequence in runs 2 and 3)
ok  	google.golang.org/grpc/internal/xds/balancer/clusterimpl/tests	0.039s
$ rm internal/xds/balancer/clusterimpl/tests/c4_release_before_swap_test.go
```

Note: an earlier draft of this repro waited for the RPC to finish while the build was parked; that deadlocks because the picker update is serialized behind `handleSecurityConfig`, which is why the observable is the KeyMaterial return.

The audited test as written still passes on the audited branch and fails on the base commit (it does catch the original bug, but through a race it happens to win, not through enforced ordering):

```console
$ cd ~/repos/grpc-go && go test ./internal/xds/balancer/clusterimpl/tests/ -run '^TestSecurityConfigUpdate_ConcurrentHandshake$' -count=5
ok  	google.golang.org/grpc/internal/xds/balancer/clusterimpl/tests	0.093s
$ cp internal/xds/balancer/clusterimpl/tests/concurrent_handshake_test.go ~/repos/wt-base/internal/xds/balancer/clusterimpl/tests/
$ cd ~/repos/wt-base && go test ./internal/xds/balancer/clusterimpl/tests/ -run '^TestSecurityConfigUpdate_ConcurrentHandshake$' -count=30 2>&1 | grep -E '^(--- |ok|FAIL)|concurrent_handshake_test.go:2[0-9][0-9]' | sed 's/([0-9.]*s)//' | sort | uniq -c
     30     concurrent_handshake_test.go:251: Active RPC failed after the Cluster security configuration was replaced: rpc error: code = Unavailable desc = connection error: desc = "transport: authentication handshake failed: xds: fetching trusted roots from CertificateProvider failed: provider instance is closed"
     30 --- FAIL: TestSecurityConfigUpdate_ConcurrentHandshake
      1 FAIL	google.golang.org/grpc/internal/xds/balancer/clusterimpl/tests	0.092s
```

Impact reasoning: the test's only gate before releasing the blocked load is provider B being built, which happens strictly before `handleSecurityConfig` swaps `xdsHIPtr` and decrements the previous owner. The blocked KeyMaterial call can therefore resume — and the whole TLS handshake can complete — before the replacement is published, in which case the test exercises "handshake finishes, then replacement" rather than the intended overlap. It currently fails on the base because the base closes the provider within microseconds of building B while the test goroutine is still waking up; an implementation that delayed the close slightly (or a slower machine) could pass this test without protecting in-flight handshakes. The eval fixture `eval_handshake_lifetime_test.go` instead waits for the replacement Cluster config to be applied via a resolver `UpdateState` hook, which is after the balancer update.

Verdict: CONFIRMED.

## C5

Audited branch. File is tracked and starts with the package clause; `scripts/vet.sh` line 40 requires a `Copyright <year> gRPC authors` line in all tracked non-generated `.go` files, and CI runs `./scripts/vet.sh` (`.github/workflows/testing.yml` line 101-103).

```console
$ cd ~/repos/grpc-go && git ls-files '*concurrent_handshake_test.go'
internal/xds/balancer/clusterimpl/tests/concurrent_handshake_test.go
$ head -3 internal/xds/balancer/clusterimpl/tests/concurrent_handshake_test.go
package clusterimpl_test

import (
$ grep -c Copyright internal/xds/balancer/clusterimpl/tests/concurrent_handshake_test.go
0
$ sed -n 37,40p scripts/vet.sh
# - Ensure all source files contain a copyright message.
# (Done in two parts because Darwin "git grep" has broken support for compound
# exclusion matches.)
(grep -L "DO NOT EDIT" $(git grep -L "\(Copyright [0-9]\{4,\} gRPC authors\)" -- '*.go') || true) | fail_on_output
$ # the exact check from vet.sh, run standalone:
$ (grep -L "DO NOT EDIT" $(git grep -L "\(Copyright [0-9]\{4,\} gRPC authors\)" -- '*.go') || true)
internal/xds/balancer/clusterimpl/tests/concurrent_handshake_test.go
$ grep -n 'vet.sh' .github/workflows/testing.yml
101:      - name: Run vet.sh
103:        run: ./scripts/vet.sh -install && ./scripts/vet.sh
```

The check's output is non-empty (exactly this file), which `fail_on_output` turns into a vet.sh failure. (Untracked eval fixtures are not seen by `git grep` and are not counted.)

The same check packaged as `verify/repro/c5_copyright_check.sh` (run after the verify commit, whose own repro files carry headers):

```console
$ bash verify/repro/c5_copyright_check.sh; echo EXIT=$?
files without a gRPC copyright header (scripts/vet.sh would fail):
  internal/xds/balancer/clusterimpl/tests/concurrent_handshake_test.go
EXIT=1
```

Impact reasoning: the repository's own lint gate (`scripts/vet.sh`, run in CI) fails on this branch solely because of the missing header on the new test file; `go vet ./...` and `gofmt -l .` do not catch it.

Verdict: CONFIRMED.

## C6

Target branch: `claims/evalon/grpc-go-xd-34a8fdbb` (`ded05d60`), worktree `~/repos/wt-34a8fdbb`.

`internal/xds/server/conn_wrapper.go` on that branch:

```go
func (c *connWrapper) Close() error {
	c.handshakeInfo.Release()   // line 156: read of c.handshakeInfo
	c.handshakeInfo = nil       // line 157: write of c.handshakeInfo
	c.parent.removeConn(c)
	return c.Conn.Close()
}
```

`handshakeInfo` is a plain `*xdsinternal.HandshakeInfo` field; neither `connWrapper.mu` nor any other lock is taken around these accesses (`XDSHandshakeInfo()` also writes the field without a lock). On the base commit `Close` only called `Close()` on the provider fields and did not write any field.

Race detector run (`verify/repro/c6_concurrent_close_race_test.go`: 200 iterations, two goroutines call `Close` on the same initialized `connWrapper` backed by `net.Pipe`):

```console
$ cp ~/repos/grpc-go/verify/repro/c6_concurrent_close_race_test.go ~/repos/wt-34a8fdbb/internal/xds/server/
$ cd ~/repos/wt-34a8fdbb && go test -race -tags verify_repro ./internal/xds/server/ -run '^Test$/^VerifyC6_ConcurrentConnWrapperClose$' -count=1 -v
=== RUN   Test/VerifyC6_ConcurrentConnWrapperClose
==================
WARNING: DATA RACE
Read at 0x00c0004e5590 by goroutine 11:
  google.golang.org/grpc/internal/xds/server.(*connWrapper).Close()
      /home/ubuntu/repos/wt-34a8fdbb/internal/xds/server/conn_wrapper.go:156 +0x39
Previous write at 0x00c0004e5590 by goroutine 12:
  google.golang.org/grpc/internal/xds/server.(*connWrapper).Close()
      /home/ubuntu/repos/wt-34a8fdbb/internal/xds/server/conn_wrapper.go:157 +0x51
==================
==================
WARNING: DATA RACE
Write at 0x00c000512f00 by goroutine 250:
  google.golang.org/grpc/internal/xds/server.(*connWrapper).Close()
      /home/ubuntu/repos/wt-34a8fdbb/internal/xds/server/conn_wrapper.go:157 +0x51
Previous write at 0x00c000512f00 by goroutine 249:
  google.golang.org/grpc/internal/xds/server.(*connWrapper).Close()
      /home/ubuntu/repos/wt-34a8fdbb/internal/xds/server/conn_wrapper.go:157 +0x51
==================
    testing.go:1617: race detected during execution of test
    --- FAIL: Test/VerifyC6_ConcurrentConnWrapperClose (0.01s)
--- FAIL: Test (0.01s)
FAIL	google.golang.org/grpc/internal/xds/server	0.030s
```

(Two `WARNING: DATA RACE` reports per run: read@156 vs write@157, and write@157 vs write@157.)

Control on the base commit (same probe with `cw.rootProvider, cw.identityProvider = verifyC6Provider{}, verifyC6Provider{}` instead of the `handshakeInfo` assignment, since the field does not exist there):

```console
$ cd ~/repos/wt-base && go test -race ./internal/xds/server/ -run '^Test$/^VerifyC6_ConcurrentConnWrapperClose$' -count=1 -v | grep -E 'DATA RACE|^(--- |ok|FAIL|PASS)'
--- PASS: Test (0.01s)
PASS
ok  	google.golang.org/grpc/internal/xds/server	1.030s
```

Impact reasoning (source inspection, not runtime-observed): the `connWrapper` is the `net.Conn` handed to the HTTP/2 server transport, which has two independent `t.conn.Close()` call sites — `internal/transport/http2_server.go:368` (loopy-writer goroutine, after a non-I/O error and a 1s/readerDone wait) and `:1299` (`http2Server.Close`, called from the reader goroutine, keepalive, or `grpc.Server.Stop`). `net.Conn.Close` is expected to be safe for concurrent use, and the base implementation of `connWrapper.Close` was (provider `Close()` calls are idempotent and no field is written). The branch introduces an unsynchronized read+write of `c.handshakeInfo` on that path, so concurrent closes now constitute a data race; the observable consequence is a `-race` failure (and undefined behavior per the Go memory model), though `HandshakeInfo.Release` itself tolerates a double decrement, so a double provider close is not the failure mode.

Verdict: CONFIRMED.
