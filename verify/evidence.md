# Evidence — audit run v-1d359f06

Audited branch: `grpc-go-xds-certificate-provider-closure-race-perfect` @ `4f781286`. Base: `cc234554fb363aea445a838b341bb8a65c8305b0`. Toolchain: `go1.25.7 linux/amd64`.
Claim-target branches were fetched from `https://github.com/kaitranntt-evals/grpc-go-xds-certificate-provider-closure-race` into separate detached worktrees (`/home/ubuntu/repos/wt-<suffix>`); all instrumentation/mutations were reverted after each run and are preserved under `verify/repro/`.

## C1

Target: `evalon/grpc-go-xd-e2cd665a` (commit `32d98b59`).

Changed test `TestSecurityConfigUpdate_ReplacedDuringHandshake` (internal/xds/balancer/clusterimpl/tests/clusterimpl_security_test.go) publishes a Cluster whose root instance loads `x509/server_ca_cert.pem` (trusts the server) with `block_loads: true`, then replaces the Cluster with a root instance loading `x509/client_ca_cert.pem` (does not trust the server), and after the first handshake completes forces a new connection and asserts:

```go
case <-untrustedRoot.loadStarted:            // replacement KeyMaterial was used for the follow-up attempt
...
if _, err := client.EmptyCall(ctx, &testpb.Empty{}); status.Code(err) != codes.Unavailable || !strings.Contains(err.Error(), "x509") {
```

Unmodified run:

```console
$ go test ./internal/xds/balancer/clusterimpl/tests/ -run 'Test/SecurityConfigUpdate_ReplacedDuringHandshake$' -count=1 -v
... Subchannel Connectivity change to TRANSIENT_FAILURE, last error: connection error: desc = "transport: authentication handshake failed: x509: certificate signed by unknown authority"
--- PASS: Test (0.13s)
    --- PASS: Test/SecurityConfigUpdate_ReplacedDuringHandshake (0.13s)
ok  	google.golang.org/grpc/internal/xds/balancer/clusterimpl/tests	0.136s
```

Challenge: make the replacement roots identical to the prior (trusting) roots (`verify/repro/c1_mutation_replacement_trusts.diff`, one-line change `client_ca_cert.pem` -> `server_ca_cert.pem` in the replacement instance config):

```console
$ git apply verify/repro/c1_mutation_replacement_trusts.diff && go test ./internal/xds/balancer/clusterimpl/tests/ -run 'Test/SecurityConfigUpdate_ReplacedDuringHandshake$' -count=1 -v
    clusterimpl_security_test.go:1058: Timed out waiting for state change.  got READY; want TRANSIENT_FAILURE
--- FAIL: Test (5.08s)
    --- FAIL: Test/SecurityConfigUpdate_ReplacedDuringHandshake (5.08s)
FAIL	google.golang.org/grpc/internal/xds/balancer/clusterimpl/tests	5.084s
```

Observation: the follow-up x509 failure occurs only with the replacement roots; with prior roots the same test fails. The test also asserts the replacement provider's `loadStarted` fired for the follow-up attempt.

## C2

Target: `evalon/grpc-go-xd-d0190a30` (commit `13832e25`).

```console
$ git diff --stat cc234554fb363aea445a838b341bb8a65c8305b0
 credentials/xds/provider_lifetime_test.go          | 201 +++++++++++++++++++++
 credentials/xds/xds.go                             |   3 +-
 credentials/xds/xds_client_test.go                 |   6 +-
 internal/credentials/xds/handshake_info.go         |  13 +-
 internal/credentials/xds/handshake_info_store.go   |  75 ++++++++
 .../credentials/xds/handshake_info_store_test.go   | 128 +++++++++++++
 internal/internal.go                               |   7 +-
 internal/xds/balancer/clusterimpl/clusterimpl.go   |  27 +--
 internal/xds/balancer/clusterimpl/security_test.go | 122 +++++++++++++
$ ./scripts/vet.sh ; echo EXIT=$?
+ git grep -e 'context.Background()' --or -e 'context.TODO()' -- '*_test.go'
+ grep -v benchmark/primitives/context_test.go
+ grep -v 'context.WithTimeout('
+ not grep -v 'context.WithCancel('
+ grep -v 'context.WithCancel('
internal/credentials/xds/handshake_info_store_test.go:				cfg, err := hi.ClientSideTLSConfig(context.Background(), "")
internal/xds/balancer/clusterimpl/security_test.go:			if _, err := selected.ClientSideTLSConfig(context.Background(), ""); err != nil {
+ cleanup
+ git reset --hard HEAD
EXIT=1
```

Both offending lines are added by the delivered diff:

```console
$ git diff cc234554fb363aea445a838b341bb8a65c8305b0 -- internal/credentials/xds/handshake_info_store_test.go internal/xds/balancer/clusterimpl/security_test.go | grep -n '^+.*context.Background()'
102:+				cfg, err := hi.ClientSideTLSConfig(context.Background(), "")
242:+			if _, err := selected.ClientSideTLSConfig(context.Background(), ""); err != nil {
```

Same script on the unchanged base in the same worktree/environment:

```console
$ git checkout -q cc234554fb363aea445a838b341bb8a65c8305b0 && ./scripts/vet.sh ; echo BASE_VET_EXIT=$?
BASE_VET_EXIT=0
```

Standalone repro of the rule (`verify/repro/c2_vet_context_rule.sh`):

```console
$ bash verify/repro/c2_vet_context_rule.sh ; echo EXIT=$?
internal/credentials/xds/handshake_info_store_test.go:96:				cfg, err := hi.ClientSideTLSConfig(context.Background(), "")
internal/xds/balancer/clusterimpl/security_test.go:102:			if _, err := selected.ClientSideTLSConfig(context.Background(), ""); err != nil {
EXIT=1
```

Impact reasoning: `scripts/vet.sh` is the repository's static-check gate (run in `.github/workflows/testing.yml`); the base passes it and the delivered branch fails it at the test-context rule, so the change as delivered cannot pass the repo's vet CI job. Runtime behavior is unaffected; fix is mechanical (use the test's `ctx` with timeout).

## C3

Target: `evalon/grpc-go-xd-1f4e4c50` (commit `55e47919`).

`handleSecurityConfig` has no equality short-circuit; the only `Equal` calls in clusterimpl.go are for other fields:

```console
$ grep -n 'Equal' internal/xds/balancer/clusterimpl/clusterimpl.go
171:	if !b.lrsReportEndpointMetrics.Equal(clusterUpdate.LRSReportEndpointMetrics) {
186:		if !slices.Equal(b.dropCategories, newDrops) {
276:		if !b.lrsServer.Equal(clusterUpdate.LRSServerConfig) {
$ grep -n 'func (b \*clusterImplBalancer) handleSecurityConfig' -A30 internal/xds/balancer/clusterimpl/clusterimpl.go | grep -n buildProvider
26:353-		rp, err := buildProvider(cpc, config.RootInstanceName, config.RootCertName, false, true)
```

Repro `verify/repro/c3_repeated_security_update_test.go` overrides `buildProvider` with a counting fake, then sends (1) an initial update, (2) the identical update again, (3) an endpoint-only update (new endpoint address, same security config):

```console
$ cp verify/repro/c3_repeated_security_update_test.go internal/xds/balancer/clusterimpl/ && go test ./internal/xds/balancer/clusterimpl/ -run 'Test/VerifyC3' -count=1 -v
    c3_repeated_security_update_test.go:40: after initial update: providers built = 1
    c3_repeated_security_update_test.go:46: after repeated identical update: providers built = 2
    c3_repeated_security_update_test.go:54: after endpoint-only update: providers built = 3
    c3_repeated_security_update_test.go:56: provider[0] closed=true
    c3_repeated_security_update_test.go:56: provider[1] closed=true
    c3_repeated_security_update_test.go:56: provider[2] closed=false
    c3_repeated_security_update_test.go:59: root provider constructed 3 times for an unchanged security configuration, want 1 (unchanged updates reacquire providers)
--- FAIL: Test/VerifyC3_UnchangedSecurityConfigReacquiresProviders (0.00s)
FAIL	google.golang.org/grpc/internal/xds/balancer/clusterimpl	0.007s
```

Impact reasoning: every CDS re-delivery or EDS/endpoint-only update (routine in xDS: periodic resends, endpoint churn) builds a new provider handle and retires the previous one. Handshakes already in flight stay valid (old handle closes on last release), but each update churns provider instances (for pemfile providers: new watcher/refcount acquisition), and the previously cached key material is discarded, so the next handshake after every endpoint update must re-load roots. No functional break observed; cost is unnecessary churn. Upstream behavior skipped this via `config.Equal(b.securityConfig)`.

## C4

Target: `evalon/grpc-go-xd-7afd1e54` (commit `1f5a1476`).

Test synchronization in `TestSecurityConfigUpdate_ReplacedDuringHandshake` (internal/xds/balancer/clusterimpl/tests/clusterimpl_security_update_test.go):

```go
	setRootCertProviderInstance(t, resources.Clusters[0], untrustingInstance)
	if err := mgmtServer.Update(ctx, resources); err != nil { ... }
	select {
	case <-untrustingHooks.built.Done():       // fired inside the builder, before the balancer swaps/closes
	...
	select {
	case <-trustingHooks.closed.Done():
		t.Fatal("Certificate provider closed while a handshake using it was in progress")
	case <-time.After(defaultTestShortTimeout):   // elapsed time only
	}
	trustingHooks.unblock.Fire()
```

`hooks.built.Fire()` is at line 142 inside the provider builder; there is no event tied to the balancer publishing the new HandshakeInfo. Instrumentation `verify/repro/c4_ordering_instrumentation.diff` prints a timestamp after the balancer's `replaceHandshakeInfo` swap+close completes, prints when the test fires unblock, and (only with `VERIFY_C4_DELAY` set) sleeps 500ms between provider construction and the swap:

```console
$ git apply verify/repro/c4_ordering_instrumentation.diff
$ go test ./internal/xds/balancer/clusterimpl/tests/ -run 'Test/SecurityConfigUpdate_ReplacedDuringHandshake$' -count=1 -v 2>&1 | grep -E 'VERIFY-C4|--- '
VERIFY-C4 09:44:08.318110 replacement applied (swap+close done)
VERIFY-C4 09:44:08.318897 replacement applied (swap+close done)
VERIFY-C4 09:44:08.419201 test fires handshake unblock
VERIFY-C4 09:44:08.427891 replacement applied (swap+close done)
--- PASS: Test (0.18s)
$ VERIFY_C4_DELAY=1 go test ./internal/xds/balancer/clusterimpl/tests/ -run 'Test/SecurityConfigUpdate_ReplacedDuringHandshake$' -count=1 -v 2>&1 | grep -E 'VERIFY-C4|--- '
VERIFY-C4 09:44:09.778008 replacement applied (swap+close done)
VERIFY-C4 09:44:09.880059 test fires handshake unblock
VERIFY-C4 09:44:10.279554 replacement applied (swap+close done)
VERIFY-C4 09:44:10.783083 replacement applied (swap+close done)
--- PASS: Test (1.52s)
    --- PASS: Test/SecurityConfigUpdate_ReplacedDuringHandshake (1.52s)
ok  	google.golang.org/grpc/internal/xds/balancer/clusterimpl/tests	1.530s
```

Observation: with the delay the handshake is unblocked (09:44:09.880) ~400ms before the replacement is applied (09:44:10.279), and the test still passes; the "old provider not closed during handshake" check therefore ran before any close could have happened.

Impact reasoning: the regression is the only thing guarding the task's core race (old provider closed mid-handshake). Because ordering is established only by a builder callback plus a `defaultTestShortTimeout` sleep, a regression that closes the old provider during the swap would be caught only when the swap happens to land inside the short window; on a slow/loaded CI runner the test degrades to a no-op and still passes (a false green, the same flake class the task was about). No user-facing runtime impact; it is a test-coverage gap.

## C5

Target: `evalon/grpc-go-xd-b2906ada` (commit `5033f43e`).

Repro `verify/repro/c5_integration_gap.sh`:

```console
$ bash verify/repro/c5_integration_gap.sh
== added regressions
+func (s) TestClientCredsProviderReplacedDuringHandshake(t *testing.T) {
+func (s) TestClientCredsHandshakeInfoReleasedBeforeHandshake(t *testing.T) {
+func (s) TestSecurityConfigUpdateKeepsProvidersOpenForInProgressHandshake(t *testing.T) {
== management-server usages in added test code: 0
== credential regression replaces via raw pointer swap:
110:+	if old := hiPtr.Swap(hi2); old != nil {
== balancer regression provider KeyMaterial never blocks:
+func (p *closeTrackingProvider) KeyMaterial(context.Context) (*certprovider.KeyMaterial, error) {
+	if p.closed.Load() {
+		return nil, errors.New("provider instance is closed")
+	}
+	return &certprovider.KeyMaterial{}, nil
+}
--- PASS: Test (0.06s)
    --- PASS: Test/ClientCredsHandshakeInfoReleasedBeforeHandshake (0.05s)
    --- PASS: Test/ClientCredsProviderReplacedDuringHandshake (0.01s)
ok  	google.golang.org/grpc/credentials/xds	0.069s
--- PASS: Test (0.00s)
    --- PASS: Test/SecurityConfigUpdateKeepsProvidersOpenForInProgressHandshake (0.00s)
ok  	google.golang.org/grpc/internal/xds/balancer/clusterimpl	0.008s
```

Observations: the credential regression blocks a real `creds.ClientHandshake` but performs "replacement" with `hiPtr.Swap(hi2)` + `Release()` on a test-owned `atomic.Pointer`, not via a Cluster update through `clusterImplBalancer`. The balancer regression drives `UpdateClientConnState` with two security configs but its providers' `KeyMaterial` never block and it calls `hi1.ClientSideTLSConfig(ctx, "")` directly with no TLS handshake. No added test starts a management server.

Impact reasoning: the production wiring between the balancer's swap (`clusterimpl.go`) and the credential's acquire (`credentials/xds/xds.go`) — the actual path of the flaky `ClientSideXDS_WithValidAndInvalidSecurityConfigurationSPIFFE` — is covered only by pre-existing e2e tests that do not block the handshake. A regression where the balancer releases/closes the old HandshakeInfo differently from what the unit tests model (e.g. closing providers directly instead of releasing the owner reference) would not be caught by the added tests. Test-coverage gap, no runtime impact observed.

## C6

Target: `evalon/grpc-go-xd-19e33a54` (commit `a635b721`).

Production ownership primitive: `HandshakeInfo.Acquire`/`Release` in internal/credentials/xds/handshake_info.go (`credentials/xds/xds.go:131: if hi.Acquire() {`); `grpcsync.RefCounted` is not used.

```go
func (hi *HandshakeInfo) Acquire() bool {
	for {
		n := hi.refs.Load()
		if n <= 0 {
			return false
		}
		if hi.refs.CompareAndSwap(n, n+1) {
			return true
		}
	}
}
func (hi *HandshakeInfo) Release() {
	if hi.refs.Add(-1) != 0 {
		return
	}
	... Close providers
}
```

Committed tests touching it:

```console
$ git diff cc234554fb363aea445a838b341bb8a65c8305b0 -- '*_test.go' | grep -n '^+func (s) Test\|Acquire\|Release()\|go func\|WaitGroup'
51:+func (s) TestClientCredsHandshakeInfoReplacedDuringRootLoad(t *testing.T) {
84:+	go func() {
99:+	hiPtr.Swap(newHI).Release()
137:+func (s) TestClientCredsHandshakeInfoReleased(t *testing.T) {
153:+	hi.Release()
268:+func (s) TestSecurityConfigReplacedDuringHandshake(t *testing.T) {
336:+	if !hi.Acquire() {
340:+	go func() {
380:+	hi.Release()
$ go test ./credentials/xds/ ./internal/xds/balancer/clusterimpl/ ./internal/credentials/xds/ -run 'Test/(ClientCredsHandshakeInfoReplacedDuringRootLoad|ClientCredsHandshakeInfoReleased|SecurityConfigReplacedDuringHandshake)$' -count=1 -v
    --- PASS: Test/ClientCredsHandshakeInfoReleased (0.00s)
    --- PASS: Test/ClientCredsHandshakeInfoReplacedDuringRootLoad (0.01s)
ok  	google.golang.org/grpc/credentials/xds	0.018s
    --- PASS: Test/SecurityConfigReplacedDuringHandshake (0.00s)
ok  	google.golang.org/grpc/internal/xds/balancer/clusterimpl	0.007s
ok  	google.golang.org/grpc/internal/credentials/xds	0.004s [no tests to run]
```

No test runs concurrent Acquire/Release loops, and none counts Close invocations (only boolean `closed` flags). `TestClientCredsHandshakeInfoReleased` asserts that `ClientHandshake` errors after release, not that `Acquire` returns false.

Mutation 1 (`verify/repro/c6_mutation_nonatomic.diff`: replace CAS/Add with non-atomic Load+Store in both Acquire and Release):

```console
$ git apply verify/repro/c6_mutation_nonatomic.diff && go test ./internal/credentials/xds/ ./credentials/xds/ ./internal/xds/balancer/clusterimpl/... ./internal/grpcsync/ -count=1
ok  	google.golang.org/grpc/internal/credentials/xds	0.044s
ok  	google.golang.org/grpc/credentials/xds	0.436s
ok  	google.golang.org/grpc/internal/xds/balancer/clusterimpl	0.011s
?   	google.golang.org/grpc/internal/xds/balancer/clusterimpl/internal	[no test files]
ok  	google.golang.org/grpc/internal/xds/balancer/clusterimpl/tests	3.754s
ok  	google.golang.org/grpc/internal/grpcsync	0.201s
```

Mutation 2 (`verify/repro/c6_mutation_no_refusal.diff`: delete the `if n <= 0 { return false }` guard so Acquire resurrects a released HandshakeInfo):

```console
$ git apply verify/repro/c6_mutation_no_refusal.diff && go test ./internal/credentials/xds/ ./credentials/xds/ ./internal/xds/balancer/clusterimpl/... -count=1
ok  	google.golang.org/grpc/internal/credentials/xds	0.044s
ok  	google.golang.org/grpc/credentials/xds	1.356s
ok  	google.golang.org/grpc/internal/xds/balancer/clusterimpl	0.011s
ok  	google.golang.org/grpc/internal/xds/balancer/clusterimpl/tests	3.971s
```

Both mutants survive. All three named parts (competing operations, exactly-once cleanup under contention, refusal after final release) lack a test that exercises and asserts them.

Impact reasoning: the current implementation is correct by inspection, but nothing in the committed suite would fail if the refcount lost its atomicity (double-close or never-close under concurrent handshakes during an update) or if a handshake could resurrect an already-closed HandshakeInfo (use of closed providers). Test-coverage gap; no runtime failure observed on this branch.

## C7

Target: `evalon/grpc-go-xd-abb83a21` (commit `4e62c998`).

Production release (internal/credentials/xds/handshake_info.go):

```go
func (hi *HandshakeInfo) Release() {
	if hi == nil {
		return
	}
	if hi.refs.Add(-1) != 0 {
		return
	}
	... Close providers
}
```

`grpcsync.RefCounted` (which logs "Refcount cannot be negative") exists on this branch but is not the production handshake primitive:

```console
$ grep -rn 'grpcsync.RefCounted\|NewRefCounted' --include=*.go . | grep -v _test
./internal/grpcsync/refcounted.go:38:// NewRefCounted creates a new RefCounted instance wrapping the given value with
./internal/grpcsync/refcounted.go:47:func NewRefCounted[V any](val V, onZero func()) (*RefCounted[V], error) {
```

Repro `verify/repro/c7_unmatched_release_test.go` (captures all grpclog output at verbosity 99 and recovers panics):

```console
$ cp verify/repro/c7_unmatched_release_test.go internal/credentials/xds/ && go test ./internal/credentials/xds/ -run 'Test/VerifyC7' -count=1 -v
    c7_unmatched_release_test.go:30: after matched Release: refs=0 closes=1
    c7_unmatched_release_test.go:37: after unmatched Release: refs=-1 closes=1 panic=<nil> log=""
    c7_unmatched_release_test.go:38: Acquire() after unmatched Release = false
    c7_unmatched_release_test.go:41: unmatched Release drove refs to -1 with no log, panic, or error
--- FAIL: Test/VerifyC7_UnmatchedReleaseGoesNegativeSilently (0.00s)
FAIL	google.golang.org/grpc/internal/credentials/xds	0.004s
```

Impact reasoning: an ownership bug elsewhere (double Release on a code path) is invisible: the count silently goes to -1, providers are not closed twice (so no crash), and Acquire keeps refusing. However, a double Release while other holders are still live (e.g. refs 2 -> 0 instead of 2 -> 1) would close providers under an in-flight handshake with no diagnostic pointing at the cause — exactly the failure mode the task fixes. Low likelihood today (no double-release observed in production paths); cost is debuggability.

## C8

Target: `evalon/grpc-go-xd-a0c92a72` (commit `3491a7c5`).

Error path in `handleSecurityConfig`:

```console
$ grep -n 'func (b \*clusterImplBalancer) handleSecurityConfig' -A55 internal/xds/balancer/clusterimpl/clusterimpl.go | grep -n 'buildProvider\|return err\|Close\|rootProvider'
22:353-	var rootProvider certprovider.Provider
24:355-		rootProvider = systemRootCertsProvider{}
26:357-		rp, err := buildProvider(cpc, config.RootInstanceName, config.RootCertName, false, true)
28:359-			return err
30:361-		rootProvider = rp
37:368-		identityProvider, err = buildProvider(cpc, name, cert, true, false)
39:370-			return err
43:374-	b.replaceHandshakeInfo(xds.NewHandshakeInfo(rootProvider, identityProvider, ...))
```

The identity-failure `return err` (line 370) does not close `rootProvider`.

Repro `verify/repro/c8_identity_build_failure_test.go` (fake `buildProvider`: root succeeds, identity returns an injected error):

```console
$ cp verify/repro/c8_identity_build_failure_test.go internal/xds/balancer/clusterimpl/ && go test ./internal/xds/balancer/clusterimpl/ -run 'Test/VerifyC8' -count=1 -v
    c8_identity_build_failure_test.go:68: UpdateClientConnState() error = received Cluster resource that contains invalid security config: injected identity provider build failure
    c8_identity_build_failure_test.go:72: root provider closed when the operation returned: false
    c8_identity_build_failure_test.go:74: root provider closed after balancer Close(): false
    c8_identity_build_failure_test.go:76: root provider built for the failed update was never released
--- FAIL: Test/VerifyC8_RootProviderLeakedOnIdentityBuildFailure (0.00s)
FAIL	google.golang.org/grpc/internal/xds/balancer/clusterimpl	0.009s
```

Impact reasoning: a Cluster whose identity instance name is not in the bootstrap (a config error the management server can send; the update is NACKed) leaks one root-provider reference per such update, never released even when the balancer closes. For the real certprovider store, the leaked reference keeps the underlying provider (e.g. pemfile file watcher goroutine) alive for the process lifetime. Triggered by misconfiguration rather than everyday traffic; repeated bad updates accumulate leaks.

## C9

Target: audited branch `grpc-go-xds-certificate-provider-closure-race-perfect` @ `4f781286` (repo root `/home/ubuntu/repos/grpc-go`).

```console
$ go test -count=1 -cpu 1,4 -timeout 7m ./credentials/tls/certprovider/... ./credentials/xds ./internal/credentials/xds ./internal/grpcsync ./internal/xds/balancer/clusterimpl/... ./internal/xds/server ./test/xds ; echo EXIT=$?
ok  	google.golang.org/grpc/credentials/tls/certprovider	0.285s
ok  	google.golang.org/grpc/credentials/tls/certprovider/pemfile	5.219s
ok  	google.golang.org/grpc/credentials/xds	0.603s
ok  	google.golang.org/grpc/internal/credentials/xds	0.098s
ok  	google.golang.org/grpc/internal/grpcsync	0.391s
ok  	google.golang.org/grpc/internal/xds/balancer/clusterimpl	0.075s
?   	google.golang.org/grpc/internal/xds/balancer/clusterimpl/internal	[no test files]
ok  	google.golang.org/grpc/internal/xds/balancer/clusterimpl/tests	8.947s
ok  	google.golang.org/grpc/internal/xds/server	0.128s
ok  	google.golang.org/grpc/test/xds	14.734s
EXIT=0
$ go test -count=1 -race -cpu 1,4 -timeout 7m ./credentials/tls/certprovider/... ./credentials/xds ./internal/credentials/xds ./internal/grpcsync ./internal/xds/balancer/clusterimpl/... ./internal/xds/server ./test/xds ; echo EXIT=$?
ok  	google.golang.org/grpc/credentials/tls/certprovider	1.373s
ok  	google.golang.org/grpc/credentials/tls/certprovider/pemfile	6.179s
ok  	google.golang.org/grpc/credentials/xds	3.710s
ok  	google.golang.org/grpc/internal/credentials/xds	1.150s
ok  	google.golang.org/grpc/internal/grpcsync	1.386s
ok  	google.golang.org/grpc/internal/xds/balancer/clusterimpl	1.047s
?   	google.golang.org/grpc/internal/xds/balancer/clusterimpl/internal	[no test files]
ok  	google.golang.org/grpc/internal/xds/balancer/clusterimpl/tests	10.969s
ok  	google.golang.org/grpc/internal/xds/server	1.376s
ok  	google.golang.org/grpc/test/xds	25.764s
EXIT=0
```

Archived eval fixtures, byte-exact (`cmp` against the extracted zip), in a clean worktree of the same commit (`/home/ubuntu/repos/wt-audit`):

```console
$ mkdir -p .evaltools && cp tests/candidate_test_inventory.go .evaltools/ && cp tests/eval_handshake_lifetime_test.go internal/xds/balancer/clusterimpl/tests/ && cp tests/run_eval_xds_test_group.sh tests/run_candidate_tests.sh test/
$ bash test/run_eval_xds_test_group.sh handshake-lifetime ; echo EXIT=$?
{"Action":"output",...,"Test":"TestEval_SecurityConfigUpdate_ActiveHandshakeKeepsProvider","Output":"--- PASS: TestEval_SecurityConfigUpdate_ActiveHandshakeKeepsProvider (0.04s)\n"}
{"Action":"output","Package":"google.golang.org/grpc/.evaltools/lifetime_M65Myc","Output":"ok  \tgoogle.golang.org/grpc/.evaltools/lifetime_M65Myc\t0.052s\n"}
EXIT=0
$ bash test/run_eval_xds_test_group.sh affected-test-compile ; echo EXIT=$?
ok  	google.golang.org/grpc/credentials/xds	0.016s [no tests to run]
ok  	google.golang.org/grpc/internal/credentials/xds	0.007s [no tests to run]
ok  	google.golang.org/grpc/internal/xds/balancer/clusterimpl/tests	0.007s [no tests to run]
ok  	google.golang.org/grpc/internal/xds/server	0.007s [no tests to run]
EXIT=0
$ bash test/run_candidate_tests.sh ; echo EXIT=$?
[PASS] Test/TestRefCounted_TryIncrement
[PASS] Test/TestRefCounted_Concurrent
[PASS] Test/TestRefCounted_DecrementNegative
[PASS] Test/TestRefCounted_IncrementDead
[PASS] TestSecurityConfigUpdate_ConcurrentHandshake
EXIT=0
```

No failures or data races were observed in either mode, so no base comparison was needed.
