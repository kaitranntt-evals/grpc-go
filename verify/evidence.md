## C1

Claim: the new or modified tests provide no added or materially strengthened causal evidence that replacement validation roots govern a distinct follow-up connection after Cluster security replacement. Adjudicated independently on four branches of [grpc-go-xds-certificate-provider-closure-race](https://github.com/kaitranntt-evals/grpc-go-xds-certificate-provider-closure-race).

Everything below is observation: commands, and their output verbatim (long lines cut at 300 columns; ports and pointer values differ per run).

### Method

For each branch, in a throwaway worktree, `verify/repro/c1_replay.sh` does the following:

1. Provisions the eval fixtures byte-exact (`.evaltools/candidate_test_inventory.go`, `test/run_candidate_tests.sh`, `test/run_eval_xds_test_group.sh`) and runs `bash ./test/run_candidate_tests.sh` with `CHANGED_TEST_FILES` set to the branch's changed `*_test.go` files. This is the eval's own list of "new or modified tests" for the branch, and its own pass/fail result.
2. (1b, context only) Runs the eval fixture `eval_handshake_lifetime_test.go` through `bash ./test/run_eval_xds_test_group.sh handshake-lifetime`.
3. Applies env-gated instrumentation to `credentials/xds` (`verify/repro/apply_instrumentation.sh` + `verify/repro/instrument/verify_hook.go.txt`). With no `VERIFY_*` variable set it changes nothing. `VERIFY_TRACE=1` prints one line per `ClientHandshake` attempt (attempt number, TCP 4-tuple, `HandshakeInfo` pointer at entry and when the TLS config is built, roots pool pointer, result). `VERIFY_MUTATE=pin-first-roots` seeds a defect: the validation roots of the first handshake that verified its peer are remembered per `HandshakeInfo` atomic pointer (i.e. per cluster) and silently reused for every later `ClientHandshake` call. A handshake that is in flight during a replacement is unaffected (nothing is pinned yet); a distinct follow-up connection is no longer governed by the replacement roots.
4. Runs an independent probe, `verify/repro/followup_probe/followup_probe_test.go` (`TestVerify_FollowUpConnectionUsesReplacementRoots`). It reuses the helpers of the eval fixture and its follow-up phase unchanged, but lets connection 1 complete under roots A (trusting the server) before the Cluster is replaced with roots B (not trusting the server), then forces a new connection to a second backend and requires `x509: certificate signed by unknown authority`. Run without the mutation (must pass) and with it (must fail).
5. Re-runs `bash ./test/run_candidate_tests.sh` with `VERIFY_MUTATE=pin-first-roots`.
6. Runs only the test functions the branch adds (`+func (s) Test...` in the diff against the base) with `VERIFY_TRACE=1 VERIFY_MUTATE=pin-first-roots` to list every `ClientHandshake` attempt they cause.

Base commit for all diffs: `cc234554fb363aea445a838b341bb8a65c8305b0` (the `BASE_COMMIT` default in the fixtures).

Fixture bytes used (extracted from `eval_tests.zip`, `tests/` layout):

```console
$ sha256sum tests/*

9a4211a5ca1ca5d06891087413822951f555dc608f88d7ab03ade4cafa0a5d53  tests/candidate_test_inventory.go
7d6ba68539e56417bb9392c4beb5fcd7c0cb3e377aa4f49cd437e32e610ffbd0  tests/candidate_test_inventory_test.go
35adf5b1fcd508887ef3a48ba94b1d7634b568155c538608890b71f39f0f95a8  tests/candidate_test_json_test.go
ad7ee39e67101a322b8b5cdbdfa590a575b60ca08600c7adf0d379aa1f58790b  tests/eval_handshake_lifetime_test.go
d521c3c4a3a8df9e7ea6f60ce8bc852152cef94f371c048a21c8829225290837  tests/run_candidate_tests.sh
0a77a4e941f6b4e3d5b9b34fc2e9450fd3ba403957905b81b27530c08bc5e6ec  tests/run_eval_xds_test_group.sh
```

Setup commands (run once, from `~/repos/grpc-go` on the verify branch):

```sh
git remote add claims https://github.com/kaitranntt-evals/grpc-go-xds-certificate-provider-closure-race
unzip -o eval_tests.zip -d ~/eval_tests          # gives ~/eval_tests/tests/...
for b in fb96a820 6959f0d0 a1703b1b ce9652b1; do
  bash verify/repro/c1_replay.sh "$b" ~/eval_tests/tests; echo "$b replay exit=$?"
done
```

```console
fb96a820 replay exit=0
6959f0d0 replay exit=0
a1703b1b replay exit=0
ce9652b1 replay exit=0
```

Exit 0 means: the branch's tests pass unmodified, the probe passes unmodified, the probe fails under the seeded defect, and every new-or-modified test still passes under the seeded defect.

Environment: `go version go1.25.7 linux/amd64`.

### What the seeded defect is, and that it is a real follow-up-roots defect

The probe on the audited verify branch itself (no instrumentation needed, the hook is only needed for the trace/mutation):

```console
$ go test -count=1 -v -run '^TestVerify_FollowUpConnectionUsesReplacementRoots$' ./verify/repro/followup_probe/

    followup_probe_test.go:293: connection 1 succeeded under prior roots A
    followup_probe_test.go:344: follow-up connection rejected under replacement roots B: rpc error: code = Unavailable desc = connection error: desc = "transport: authentication handshake failed: x509: certificate signed by unknown authority"
--- PASS: TestVerify_FollowUpConnectionUsesReplacementRoots (0.04s)
ok  	google.golang.org/grpc/verify/repro/followup_probe	0.052s
```

On every claim branch the same probe passes unmodified and fails under `VERIFY_MUTATE=pin-first-roots` (sections 3 and 4 of each transcript below): attempt 2 is a different TCP connection, the solution builds its TLS config from the replacement roots pool, the mutation swaps the pool back to the one pinned by attempt 1, and the follow-up RPC succeeds instead of failing with `x509: certificate signed by unknown authority`.

### Impact reasoning

- The four implementations behave correctly for a follow-up connection (probe green on all four without the mutation). The problem C1 describes is in the tests, not in the shipped behaviour.
- On each branch, every test the eval inventory classifies as new or modified passes both with and without a defect that makes follow-up connections keep validating against the pre-replacement roots. A test that asserted a follow-up connection's trust outcome under replacement roots would have to turn red under that defect; none does.
- The attempt trace shows why: each added test (or subtest) drives at most one `ClientHandshake` attempt on one TCP connection, the one that is in flight during the replacement, or drives none. No added test opens a second connection after the replacement.
- Reading the added assertions agrees: where a replacement root provider is used it carries the same CA as the prior one (`x509/server_ca_cert.pem`) and the assertion is success; the only asserted failures are provider fetch errors (`provider instance is closed`, `new root provider error`, `replacement provider error`), not certificate trust failures.
- Consequence: a regression that pinned or cached validation roots across a Cluster security replacement (stale trust after CA rotation) would pass each branch's own regression suite.

### Context: the eval fixture on these branches

`eval_handshake_lifetime_test.go` contains a follow-up phase (lines 320-361: second backend, `providerB.entered` drained right before the follow-up RPC, `Follow-up RPC error = %v, want x509 unknown authority`). On all four branches the fixture stops earlier, at line 314, so that phase never executes there (section 1b of each transcript). That failure measures the in-flight handshake (the fixture expects it to finish under provider A; these branches re-read the replacement `HandshakeInfo`), which is not what C1 is about, so no verdict rests on it. It is the reason an independent probe was used for the follow-up behaviour.


### Branch [fb96a820](https://github.com/kaitranntt-evals/grpc-go-xds-certificate-provider-closure-race/tree/evalon/grpc-go-xd-fb96a820)

Tip audited: `4ce78e0bedec051f313b7e3f237bee9449ff4356`.

Changed test files against the base:

```console
$ git diff --numstat cc234554fb363aea445a838b341bb8a65c8305b0 claims/evalon/grpc-go-xd-fb96a820 -- '*_test.go'
103	0	credentials/xds/xds_client_test.go
263	0	internal/xds/balancer/clusterimpl/tests/clusterimpl_security_test.go
$ git diff -U0 cc234554fb363aea445a838b341bb8a65c8305b0 claims/evalon/grpc-go-xd-fb96a820 -- '*_test.go' | grep -E '^[+-]func '
+func newBlockingProvider() *blockingProvider {
+func (p *blockingProvider) KeyMaterial(ctx context.Context) (*certprovider.KeyMaterial, error) {
+func (p *blockingProvider) Close() { close(p.closed) }
+func (s) TestClientCredsProviderReplacedDuringHandshake(t *testing.T) {
+func newBlockingCertProviderController(t *testing.T) *blockingCertProviderController {
+func (blockingCertProviderBuilder) ParseConfig(any) (*certprovider.BuildableConfig, error) {
+func (blockingCertProviderBuilder) Name() string {
+func (p *blockingCertProvider) KeyMaterial(ctx context.Context) (*certprovider.KeyMaterial, error) {
+func (p *blockingCertProvider) Close() {
+func init() {
+func mtlsClusterWithCertName(t *testing.T, cluster *v3clusterpb.Cluster, certName string) *v3clusterpb.Cluster {
+func (s) TestSecurityConfigUpdate_ProviderReplacedDuringHandshake(t *testing.T) {
```

What the added tests assert (read from `git diff cc234554fb363aea445a838b341bb8a65c8305b0 claims/evalon/grpc-go-xd-fb96a820 -- '*_test.go'`):

- `credentials/xds/xds_client_test.go` `TestClientCredsProviderReplacedDuringHandshake` (new): one `net.Dial`, one `creds.ClientHandshake` call. The prior root provider (`blockingProvider`) never returns KeyMaterial; the replacement `root2 := makeRootProvider(t, "x509/server_ca_cert.pem")` trusts the server. Only assertions: `ClientHandshake() failed: %v` must not happen, then `compareAuthInfo`. No second connection, no trust failure.
- `internal/xds/balancer/clusterimpl/tests/clusterimpl_security_test.go` `TestSecurityConfigUpdate_ProviderReplacedDuringHandshake` (new): one `EmptyCall`; the Cluster update only changes `CertificateName` to `"new-cert-name"`; old and new provider instances hand out the same `ctrl.roots` (`x509/server_ca_cert.pem`). Assertions: providers built with `new-cert-name`, `KeyMaterial() called on provider with certificate name ... want "new-cert-name"` (a provider-scoped channel signal observed while the original handshake is still blocked), `EmptyCall() failed: %v` must not happen, `verifySecurityInformationFromPeer(..., SecurityLevelMTLS)`. The RPC that completes is the original in-progress one; no later connection is made.
- The other 10 tests the inventory lists for this file are pre-existing functions with unchanged bodies (the file diff is `263 0`, additions only).

Transcript:

```console
$ bash verify/repro/c1_replay.sh fb96a820 ~/eval_tests/tests; echo "replay exit=$?"
### [fb96a820] 1. new-or-modified tests, pristine branch
    credentials/xds/xds_client_test.go	Test/TestClientCredsProviderReplacedDuringHandshake
    internal/xds/balancer/clusterimpl/tests/clusterimpl_security_test.go	Test/TestSecurityConfigUpdate_ProviderReplacedDuringHandshake
    internal/xds/balancer/clusterimpl/tests/clusterimpl_security_test.go	Test/TestSecurityConfigWithoutXDSCreds
    internal/xds/balancer/clusterimpl/tests/clusterimpl_security_test.go	Test/TestNoSecurityConfigWithXDSCreds
    internal/xds/balancer/clusterimpl/tests/clusterimpl_security_test.go	Test/TestSecurityConfigNotFoundInBootstrap
    internal/xds/balancer/clusterimpl/tests/clusterimpl_security_test.go	Test/TestGoodSecurityConfig
    internal/xds/balancer/clusterimpl/tests/clusterimpl_security_test.go	Test/TestSecurityConfigUpdate_BadToGood
    internal/xds/balancer/clusterimpl/tests/clusterimpl_security_test.go	Test/TestAggregateClusterSecurityConfig
    internal/xds/balancer/clusterimpl/tests/clusterimpl_security_test.go	Test/TestCertproviderStoreError
    internal/xds/balancer/clusterimpl/tests/clusterimpl_security_test.go	Test/TestSecurityConfigUpdate_GoodToFallback
    internal/xds/balancer/clusterimpl/tests/clusterimpl_security_test.go	Test/TestSecurityConfigUpdate_GoodToBad
    internal/xds/balancer/clusterimpl/tests/clusterimpl_security_test.go	Test/TestSystemRootCertsSecurityConfig
[PASS] Test/TestClientCredsProviderReplacedDuringHandshake
[PASS] Test/TestSecurityConfigUpdate_ProviderReplacedDuringHandshake
[PASS] Test/TestSecurityConfigWithoutXDSCreds
[PASS] Test/TestNoSecurityConfigWithXDSCreds
[PASS] Test/TestSecurityConfigNotFoundInBootstrap
[PASS] Test/TestGoodSecurityConfig
[PASS] Test/TestSecurityConfigUpdate_BadToGood
[PASS] Test/TestAggregateClusterSecurityConfig
[PASS] Test/TestCertproviderStoreError
[PASS] Test/TestSecurityConfigUpdate_GoodToFallback
[PASS] Test/TestSecurityConfigUpdate_GoodToBad
[PASS] Test/TestSystemRootCertsSecurityConfig
exit=0
### [fb96a820] 1b. (context, not gating) eval fixture eval_handshake_lifetime_test.go, pristine branch
eval_handshake_lifetime_test.go:314: Active RPC failed after the Cluster security configuration was replaced: rpc error: code = Unavailable desc = connection error: desc = \"transport: authentication handshake failed: x509: certificate signed by unknown authority\"\n"
--- FAIL: TestEval_SecurityConfigUpdate_ActiveHandshakeKeepsProvider (0.02s)
exit=1
### [fb96a820] 2. apply instrumentation + probe
### [fb96a820] 3. probe, no mutation (expect PASS)
VERIFY-TRACE attempt=1 begin conn=127.0.0.1:49584->127.0.0.1:41983 hi@begin=0xc0004f8360
VERIFY-TRACE attempt=1 tls-config-built hi@tls=0xc0004f8360 roots=0xc0003eefc0
VERIFY-TRACE attempt=1 end err=<nil>
    followup_probe_test.go:293: connection 1 succeeded under prior roots A
VERIFY-TRACE attempt=2 begin conn=127.0.0.1:50354->127.0.0.1:42015 hi@begin=0xc0003d4360
VERIFY-TRACE attempt=2 tls-config-built hi@tls=0xc0003d4360 roots=0xc0003ef380
VERIFY-TRACE attempt=2 end err=x509: certificate signed by unknown authority
    followup_probe_test.go:344: follow-up connection rejected under replacement roots B: rpc error: code = Unavailable desc = connection error: desc = "transport: authentication handshake failed: x509: certificate signed by unknown authority"
--- PASS: TestVerify_FollowUpConnectionUsesReplacementRoots (0.04s)
ok  	google.golang.org/grpc/verify/repro/followup_probe	0.053s
exit=0
### [fb96a820] 4. probe, VERIFY_MUTATE=pin-first-roots (expect FAIL)
VERIFY-TRACE attempt=1 begin conn=127.0.0.1:37126->127.0.0.1:35129 hi@begin=0xc00059d560
VERIFY-TRACE attempt=1 tls-config-built hi@tls=0xc00059d560 roots=0xc0003eefc0
VERIFY-TRACE attempt=1 MUTATION pinned roots=0xc0003eefc0 for all later attempts on this HandshakeInfo pointer
VERIFY-TRACE attempt=1 end err=<nil>
    followup_probe_test.go:293: connection 1 succeeded under prior roots A
VERIFY-TRACE attempt=2 begin conn=127.0.0.1:43254->127.0.0.1:36117 hi@begin=0xc000647440
VERIFY-TRACE attempt=2 tls-config-built hi@tls=0xc000647440 roots=0xc0003ef380
VERIFY-TRACE attempt=2 MUTATION-ACTIVE current roots ignored; reusing roots=0xc0003eefc0 pinned by attempt=1
VERIFY-TRACE attempt=2 end err=<nil>
    followup_probe_test.go:342: Follow-up RPC error = <nil>, want x509 unknown authority
--- FAIL: TestVerify_FollowUpConnectionUsesReplacementRoots (0.04s)
FAIL
FAIL	google.golang.org/grpc/verify/repro/followup_probe	0.053s
FAIL
exit=1
### [fb96a820] 5. new-or-modified tests, VERIFY_MUTATE=pin-first-roots (C1 predicts: all still PASS)
[PASS] Test/TestClientCredsProviderReplacedDuringHandshake
[PASS] Test/TestSecurityConfigUpdate_ProviderReplacedDuringHandshake
[PASS] Test/TestSecurityConfigWithoutXDSCreds
[PASS] Test/TestNoSecurityConfigWithXDSCreds
[PASS] Test/TestSecurityConfigNotFoundInBootstrap
[PASS] Test/TestGoodSecurityConfig
[PASS] Test/TestAggregateClusterSecurityConfig
[PASS] Test/TestCertproviderStoreError
[PASS] Test/TestSecurityConfigUpdate_BadToGood
[PASS] Test/TestSecurityConfigUpdate_GoodToFallback
[PASS] Test/TestSecurityConfigUpdate_GoodToBad
[PASS] Test/TestSystemRootCertsSecurityConfig
exit=0
### [fb96a820] 6. per-attempt trace of the tests added on the branch (seeded defect active)
=== RUN   Test
=== RUN   Test/ClientCredsProviderReplacedDuringHandshake
VERIFY-TRACE attempt=1 begin conn=127.0.0.1:49388->127.0.0.1:41015 hi@begin=0xc000024ba0
VERIFY-TRACE attempt=1 tls-config-built hi@tls=0xc000024e40 roots=0xc000254210
VERIFY-TRACE attempt=1 MUTATION pinned roots=0xc000254210 for all later attempts on this HandshakeInfo pointer
VERIFY-TRACE attempt=1 end err=<nil>
--- PASS: Test (0.01s)
    --- PASS: Test/ClientCredsProviderReplacedDuringHandshake (0.01s)
ok  	google.golang.org/grpc/credentials/xds	0.015s
=== RUN   Test
=== RUN   Test/SecurityConfigUpdate_ProviderReplacedDuringHandshake
VERIFY-TRACE attempt=1 begin conn=127.0.0.1:50244->127.0.0.1:39969 hi@begin=0xc000790f60
VERIFY-TRACE attempt=1 tls-config-built hi@tls=0xc0004aecc0 roots=0xc0002603f0
VERIFY-TRACE attempt=1 MUTATION pinned roots=0xc0002603f0 for all later attempts on this HandshakeInfo pointer
VERIFY-TRACE attempt=1 end err=<nil>
--- PASS: Test (0.02s)
    --- PASS: Test/SecurityConfigUpdate_ProviderReplacedDuringHandshake (0.02s)
ok  	google.golang.org/grpc/internal/xds/balancer/clusterimpl/tests	0.030s
### [fb96a820] C1 REPRODUCED: the seeded follow-up-roots defect is real (probe red) yet every new-or-modified test stays green
replay exit=0
```


### Branch [6959f0d0](https://github.com/kaitranntt-evals/grpc-go-xds-certificate-provider-closure-race/tree/evalon/grpc-go-xd-6959f0d0)

Tip audited: `8cb2c07474faf186dc799b336550fdd11b983ece`.

Changed test files against the base:

```console
$ git diff --numstat cc234554fb363aea445a838b341bb8a65c8305b0 claims/evalon/grpc-go-xd-6959f0d0 -- '*_test.go'
163	0	credentials/xds/xds_client_test.go
158	0	internal/xds/balancer/clusterimpl/balancer_test.go
$ git diff -U0 cc234554fb363aea445a838b341bb8a65c8305b0 claims/evalon/grpc-go-xd-6959f0d0 -- '*_test.go' | grep -E '^[+-]func '
+func (p *blockingProvider) KeyMaterial(ctx context.Context) (*certprovider.KeyMaterial, error) {
+func (p *blockingProvider) Close() { p.closed.Store(true) }
+func (s) TestClientCredsProviderReplacedDuringHandshake(t *testing.T) {
+func (s) TestClientCredsProviderClosedWithoutReplacement(t *testing.T) {
+func (p *recordingProvider) KeyMaterial(context.Context) (*certprovider.KeyMaterial, error) {
+func (p *recordingProvider) Close() {
+func (s) TestSecurityConfigUpdatePublishesNewHandshakeInfoBeforeClosingProviders(t *testing.T) {
```

What the added tests assert (read from `git diff cc234554fb363aea445a838b341bb8a65c8305b0 claims/evalon/grpc-go-xd-6959f0d0 -- '*_test.go'`):

- `credentials/xds/xds_client_test.go` `TestClientCredsProviderReplacedDuringHandshake` (new): one `net.Dial`, one `creds.ClientHandshake`; prior and replacement root providers both carry `x509/server_ca_cert.pem`; asserts success + `compareAuthInfo`.
- `credentials/xds/xds_client_test.go` `TestClientCredsProviderClosedWithoutReplacement` (new): one `ClientHandshake`, no replacement at all; asserts the error contains `provider instance is closed` (a provider error, not a certificate trust failure).
- `internal/xds/balancer/clusterimpl/balancer_test.go` `TestSecurityConfigUpdatePublishesNewHandshakeInfoBeforeClosingProviders` (new): no connection is ever made. `recordingProvider.KeyMaterial` returns an empty `&certprovider.KeyMaterial{}` (no roots); `Close()` calls `hi.ClientSideTLSConfig(ctx, "")` directly and the test asserts the provider *names* consulted equal `["root:root-cert-2", "identity:identity-cert"]`. That is a configuration-helper call, not a connection outcome.

Transcript:

```console
$ bash verify/repro/c1_replay.sh 6959f0d0 ~/eval_tests/tests; echo "replay exit=$?"
### [6959f0d0] 1. new-or-modified tests, pristine branch
    internal/xds/balancer/clusterimpl/balancer_test.go	Test/TestSecurityConfigUpdatePublishesNewHandshakeInfoBeforeClosingProviders
    credentials/xds/xds_client_test.go	Test/TestClientCredsProviderReplacedDuringHandshake
    credentials/xds/xds_client_test.go	Test/TestClientCredsProviderClosedWithoutReplacement
[PASS] Test/TestSecurityConfigUpdatePublishesNewHandshakeInfoBeforeClosingProviders
[PASS] Test/TestClientCredsProviderReplacedDuringHandshake
[PASS] Test/TestClientCredsProviderClosedWithoutReplacement
exit=0
### [6959f0d0] 1b. (context, not gating) eval fixture eval_handshake_lifetime_test.go, pristine branch
eval_handshake_lifetime_test.go:314: Active RPC failed after the Cluster security configuration was replaced: rpc error: code = Unavailable desc = connection error: desc = \"transport: authentication handshake failed: x509: certificate signed by unknown authority\"\n"
--- FAIL: TestEval_SecurityConfigUpdate_ActiveHandshakeKeepsProvider (0.02s)
exit=1
### [6959f0d0] 2. apply instrumentation + probe
### [6959f0d0] 3. probe, no mutation (expect PASS)
VERIFY-TRACE attempt=1 begin conn=127.0.0.1:35448->127.0.0.1:42767 hi@begin=0xc000172360
VERIFY-TRACE attempt=1 tls-config-built hi@tls=0xc000172360 roots=0xc0003eefc0
VERIFY-TRACE attempt=1 end err=<nil>
    followup_probe_test.go:293: connection 1 succeeded under prior roots A
VERIFY-TRACE attempt=2 begin conn=127.0.0.1:40106->127.0.0.1:39805 hi@begin=0xc0004761e0
VERIFY-TRACE attempt=2 tls-config-built hi@tls=0xc0004761e0 roots=0xc0003ef380
VERIFY-TRACE attempt=2 end err=x509: certificate signed by unknown authority
    followup_probe_test.go:344: follow-up connection rejected under replacement roots B: rpc error: code = Unavailable desc = connection error: desc = "transport: authentication handshake failed: x509: certificate signed by unknown authority"
--- PASS: TestVerify_FollowUpConnectionUsesReplacementRoots (0.04s)
ok  	google.golang.org/grpc/verify/repro/followup_probe	0.052s
exit=0
### [6959f0d0] 4. probe, VERIFY_MUTATE=pin-first-roots (expect FAIL)
VERIFY-TRACE attempt=1 begin conn=127.0.0.1:39334->127.0.0.1:35983 hi@begin=0xc0007187e0
VERIFY-TRACE attempt=1 tls-config-built hi@tls=0xc0007187e0 roots=0xc0003ecfc0
VERIFY-TRACE attempt=1 MUTATION pinned roots=0xc0003ecfc0 for all later attempts on this HandshakeInfo pointer
VERIFY-TRACE attempt=1 end err=<nil>
    followup_probe_test.go:293: connection 1 succeeded under prior roots A
VERIFY-TRACE attempt=2 begin conn=127.0.0.1:55506->127.0.0.1:40273 hi@begin=0xc00061cf00
VERIFY-TRACE attempt=2 tls-config-built hi@tls=0xc00061cf00 roots=0xc0003ed380
VERIFY-TRACE attempt=2 MUTATION-ACTIVE current roots ignored; reusing roots=0xc0003ecfc0 pinned by attempt=1
VERIFY-TRACE attempt=2 end err=<nil>
    followup_probe_test.go:342: Follow-up RPC error = <nil>, want x509 unknown authority
--- FAIL: TestVerify_FollowUpConnectionUsesReplacementRoots (0.04s)
FAIL
FAIL	google.golang.org/grpc/verify/repro/followup_probe	0.052s
FAIL
exit=1
### [6959f0d0] 5. new-or-modified tests, VERIFY_MUTATE=pin-first-roots (C1 predicts: all still PASS)
[PASS] Test/TestClientCredsProviderReplacedDuringHandshake
[PASS] Test/TestClientCredsProviderClosedWithoutReplacement
[PASS] Test/TestSecurityConfigUpdatePublishesNewHandshakeInfoBeforeClosingProviders
exit=0
### [6959f0d0] 6. per-attempt trace of the tests added on the branch (seeded defect active)
=== RUN   Test
=== RUN   Test/ClientCredsProviderClosedWithoutReplacement
VERIFY-TRACE attempt=1 begin conn=127.0.0.1:40488->127.0.0.1:42357 hi@begin=0xc000024cc0
VERIFY-TRACE attempt=1 end err=xds: fetching trusted roots from CertificateProvider failed: provider instance is closed
=== RUN   Test/ClientCredsProviderReplacedDuringHandshake
VERIFY-TRACE attempt=2 begin conn=127.0.0.1:54080->127.0.0.1:41003 hi@begin=0xc00011a300
VERIFY-TRACE attempt=2 tls-config-built hi@tls=0xc000508120 roots=0xc000114ff0
VERIFY-TRACE attempt=2 MUTATION pinned roots=0xc000114ff0 for all later attempts on this HandshakeInfo pointer
VERIFY-TRACE attempt=2 end err=<nil>
--- PASS: Test (0.06s)
    --- PASS: Test/ClientCredsProviderClosedWithoutReplacement (0.05s)
    --- PASS: Test/ClientCredsProviderReplacedDuringHandshake (0.01s)
ok  	google.golang.org/grpc/credentials/xds	0.068s
=== RUN   Test
=== RUN   Test/SecurityConfigUpdatePublishesNewHandshakeInfoBeforeClosingProviders
--- PASS: Test (0.00s)
    --- PASS: Test/SecurityConfigUpdatePublishesNewHandshakeInfoBeforeClosingProviders (0.00s)
ok  	google.golang.org/grpc/internal/xds/balancer/clusterimpl	0.008s
### [6959f0d0] C1 REPRODUCED: the seeded follow-up-roots defect is real (probe red) yet every new-or-modified test stays green
replay exit=0
```


### Branch [a1703b1b](https://github.com/kaitranntt-evals/grpc-go-xds-certificate-provider-closure-race/tree/evalon/grpc-go-xd-a1703b1b)

Tip audited: `df054d0f6a55de6dfead366a607d806fef9f9718`.

Changed test files against the base:

```console
$ git diff --numstat cc234554fb363aea445a838b341bb8a65c8305b0 claims/evalon/grpc-go-xd-a1703b1b -- '*_test.go'
160	0	credentials/xds/xds_client_test.go
159	0	internal/xds/balancer/clusterimpl/balancer_test.go
$ git diff -U0 cc234554fb363aea445a838b341bb8a65c8305b0 claims/evalon/grpc-go-xd-a1703b1b -- '*_test.go' | grep -E '^[+-]func '
+func newBlockingProvider(km *certprovider.KeyMaterial) *blockingProvider {
+func (p *blockingProvider) KeyMaterial(ctx context.Context) (*certprovider.KeyMaterial, error) {
+func (p *blockingProvider) Close() { p.closed.Store(true) }
+func (s) TestClientCredsProviderReplacedDuringHandshake(t *testing.T) {
+func (xdsCredsForTesting) UsesXDS() bool { return true }
+func (p *testCertProvider) KeyMaterial(context.Context) (*certprovider.KeyMaterial, error) {
+func (p *testCertProvider) Close() { p.onClose() }
+func securityTestClientConnState(xdsC xdsclient.XDSClient, secCfg *xdsresource.SecurityConfig) balancer.ClientConnState {
+func (s) TestSecurityConfigUpdate_HandshakeInfoPublishedBeforeProvidersClosed(t *testing.T) {
```

What the added tests assert (read from `git diff cc234554fb363aea445a838b341bb8a65c8305b0 claims/evalon/grpc-go-xd-a1703b1b -- '*_test.go'`):

- `credentials/xds/xds_client_test.go` `TestClientCredsProviderReplacedDuringHandshake` (new, 3 subtests): each subtest makes one `net.Dial` and one `creds.ClientHandshake`. `new providers are good`: replacement root is `x509/server_ca_cert.pem`, same CA as the prior provider, asserts success + `compareAuthInfo`. `new handshake info uses fallback creds`: asserts success via fallback. `new providers are bad`: asserts error contains `new root provider error` (a provider fetch error, not a certificate trust failure).
- `internal/xds/balancer/clusterimpl/balancer_test.go` `TestSecurityConfigUpdate_HandshakeInfoPublishedBeforeProvidersClosed` (new): no connection is made; `testCertProvider.KeyMaterial` returns an empty `&certprovider.KeyMaterial{}`; assertions compare published `HandshakeInfo` pointers/`Equal` and close ordering (`Provider %d was closed before the new HandshakeInfo was published`).

Transcript:

```console
$ bash verify/repro/c1_replay.sh a1703b1b ~/eval_tests/tests; echo "replay exit=$?"
### [a1703b1b] 1. new-or-modified tests, pristine branch
    credentials/xds/xds_client_test.go	Test/TestClientCredsProviderReplacedDuringHandshake
    internal/xds/balancer/clusterimpl/balancer_test.go	Test/TestSecurityConfigUpdate_HandshakeInfoPublishedBeforeProvidersClosed
[PASS] Test/TestClientCredsProviderReplacedDuringHandshake
[PASS] Test/TestSecurityConfigUpdate_HandshakeInfoPublishedBeforeProvidersClosed
exit=0
### [a1703b1b] 1b. (context, not gating) eval fixture eval_handshake_lifetime_test.go, pristine branch
eval_handshake_lifetime_test.go:314: Active RPC failed after the Cluster security configuration was replaced: rpc error: code = Unavailable desc = connection error: desc = \"transport: authentication handshake failed: x509: certificate signed by unknown authority\"\n"
--- FAIL: TestEval_SecurityConfigUpdate_ActiveHandshakeKeepsProvider (0.02s)
exit=1
### [a1703b1b] 2. apply instrumentation + probe
### [a1703b1b] 3. probe, no mutation (expect PASS)
VERIFY-TRACE attempt=1 begin conn=127.0.0.1:45960->127.0.0.1:46081 hi@begin=0xc0006686c0
VERIFY-TRACE attempt=1 tls-config-built hi@tls=0xc0006686c0 roots=0xc0003ecfc0
VERIFY-TRACE attempt=1 end err=<nil>
    followup_probe_test.go:293: connection 1 succeeded under prior roots A
VERIFY-TRACE attempt=2 begin conn=127.0.0.1:49576->127.0.0.1:43537 hi@begin=0xc0005a5680
VERIFY-TRACE attempt=2 tls-config-built hi@tls=0xc0005a5680 roots=0xc0003ed380
VERIFY-TRACE attempt=2 end err=x509: certificate signed by unknown authority
    followup_probe_test.go:344: follow-up connection rejected under replacement roots B: rpc error: code = Unavailable desc = connection error: desc = "transport: authentication handshake failed: x509: certificate signed by unknown authority"
--- PASS: TestVerify_FollowUpConnectionUsesReplacementRoots (0.04s)
ok  	google.golang.org/grpc/verify/repro/followup_probe	0.052s
exit=0
### [a1703b1b] 4. probe, VERIFY_MUTATE=pin-first-roots (expect FAIL)
VERIFY-TRACE attempt=1 begin conn=127.0.0.1:49334->127.0.0.1:40633 hi@begin=0xc000383020
VERIFY-TRACE attempt=1 tls-config-built hi@tls=0xc000383020 roots=0xc0003eefc0
VERIFY-TRACE attempt=1 MUTATION pinned roots=0xc0003eefc0 for all later attempts on this HandshakeInfo pointer
VERIFY-TRACE attempt=1 end err=<nil>
    followup_probe_test.go:293: connection 1 succeeded under prior roots A
VERIFY-TRACE attempt=2 begin conn=127.0.0.1:50018->127.0.0.1:34633 hi@begin=0xc0005f6a20
VERIFY-TRACE attempt=2 tls-config-built hi@tls=0xc0005f6a20 roots=0xc0003ef380
VERIFY-TRACE attempt=2 MUTATION-ACTIVE current roots ignored; reusing roots=0xc0003eefc0 pinned by attempt=1
VERIFY-TRACE attempt=2 end err=<nil>
    followup_probe_test.go:342: Follow-up RPC error = <nil>, want x509 unknown authority
--- FAIL: TestVerify_FollowUpConnectionUsesReplacementRoots (0.04s)
FAIL
FAIL	google.golang.org/grpc/verify/repro/followup_probe	0.054s
FAIL
exit=1
### [a1703b1b] 5. new-or-modified tests, VERIFY_MUTATE=pin-first-roots (C1 predicts: all still PASS)
[PASS] Test/TestSecurityConfigUpdate_HandshakeInfoPublishedBeforeProvidersClosed
[PASS] Test/TestClientCredsProviderReplacedDuringHandshake
exit=0
### [a1703b1b] 6. per-attempt trace of the tests added on the branch (seeded defect active)
=== RUN   Test
=== RUN   Test/ClientCredsProviderReplacedDuringHandshake
=== RUN   Test/ClientCredsProviderReplacedDuringHandshake/new_providers_are_good
VERIFY-TRACE attempt=1 begin conn=127.0.0.1:56650->127.0.0.1:34219 hi@begin=0xc000024d20
VERIFY-TRACE attempt=1 tls-config-built hi@tls=0xc000024fc0 roots=0xc0002667e0
VERIFY-TRACE attempt=1 MUTATION pinned roots=0xc0002667e0 for all later attempts on this HandshakeInfo pointer
VERIFY-TRACE attempt=1 end err=<nil>
=== RUN   Test/ClientCredsProviderReplacedDuringHandshake/new_handshake_info_uses_fallback_creds
VERIFY-TRACE attempt=2 begin conn=127.0.0.1:40364->127.0.0.1:43097 hi@begin=0xc00028b380
VERIFY-TRACE attempt=2 end err=<nil>
=== RUN   Test/ClientCredsProviderReplacedDuringHandshake/new_providers_are_bad
VERIFY-TRACE attempt=3 begin conn=127.0.0.1:40092->127.0.0.1:40181 hi@begin=0xc000116420
VERIFY-TRACE attempt=3 end err=xds: fetching trusted roots from CertificateProvider failed: new root provider error
--- PASS: Test (0.07s)
    --- PASS: Test/ClientCredsProviderReplacedDuringHandshake (0.07s)
        --- PASS: Test/ClientCredsProviderReplacedDuringHandshake/new_providers_are_good (0.01s)
        --- PASS: Test/ClientCredsProviderReplacedDuringHandshake/new_handshake_info_uses_fallback_creds (0.01s)
        --- PASS: Test/ClientCredsProviderReplacedDuringHandshake/new_providers_are_bad (0.00s)
ok  	google.golang.org/grpc/credentials/xds	0.076s
=== RUN   Test
=== RUN   Test/SecurityConfigUpdate_HandshakeInfoPublishedBeforeProvidersClosed
--- PASS: Test (0.00s)
    --- PASS: Test/SecurityConfigUpdate_HandshakeInfoPublishedBeforeProvidersClosed (0.00s)
ok  	google.golang.org/grpc/internal/xds/balancer/clusterimpl	0.009s
### [a1703b1b] C1 REPRODUCED: the seeded follow-up-roots defect is real (probe red) yet every new-or-modified test stays green
replay exit=0
```


### Branch [ce9652b1](https://github.com/kaitranntt-evals/grpc-go-xds-certificate-provider-closure-race/tree/evalon/grpc-go-xd-ce9652b1)

Tip audited: `433512b3ed79bcbafe025d9e78b6661f58c7fb24`.

Changed test files against the base:

```console
$ git diff --numstat cc234554fb363aea445a838b341bb8a65c8305b0 claims/evalon/grpc-go-xd-ce9652b1 -- '*_test.go'
120	0	credentials/xds/xds_client_test.go
$ git diff -U0 cc234554fb363aea445a838b341bb8a65c8305b0 claims/evalon/grpc-go-xd-ce9652b1 -- '*_test.go' | grep -E '^[+-]func '
+func (p *blockingProvider) KeyMaterial(ctx context.Context) (*certprovider.KeyMaterial, error) {
+func (p *blockingProvider) Close() {
+func (s) TestClientCredsProviderSwitchDuringHandshake(t *testing.T) {
```

What the added tests assert (read from `git diff cc234554fb363aea445a838b341bb8a65c8305b0 claims/evalon/grpc-go-xd-ce9652b1 -- '*_test.go'`):

- `credentials/xds/xds_client_test.go` `TestClientCredsProviderSwitchDuringHandshake` (new, 4 subtests): each subtest makes one `net.Dial` and one `creds.ClientHandshake`, with the `HandshakeInfo` swapped while that call is blocked. `root_provider` / `identity_provider`: replacement root is `x509/server_ca_cert.pem` (trusts the server), asserts `ClientHandshake() failed during provider replacement: %v` must not happen + `compareAuthInfo`. `fallback`: asserts success via fallback. `replacement_provider_error`: asserts error contains `replacement provider error` (a provider fetch error, not a certificate trust failure).
- No other test file is changed on this branch.

Transcript:

```console
$ bash verify/repro/c1_replay.sh ce9652b1 ~/eval_tests/tests; echo "replay exit=$?"
### [ce9652b1] 1. new-or-modified tests, pristine branch
    credentials/xds/xds_client_test.go	Test/TestClientCredsProviderSwitchDuringHandshake
[PASS] Test/TestClientCredsProviderSwitchDuringHandshake
exit=0
### [ce9652b1] 1b. (context, not gating) eval fixture eval_handshake_lifetime_test.go, pristine branch
eval_handshake_lifetime_test.go:314: Active RPC failed after the Cluster security configuration was replaced: rpc error: code = Unavailable desc = connection error: desc = \"transport: authentication handshake failed: x509: certificate signed by unknown authority\"\n"
--- FAIL: TestEval_SecurityConfigUpdate_ActiveHandshakeKeepsProvider (0.02s)
exit=1
### [ce9652b1] 2. apply instrumentation + probe
### [ce9652b1] 3. probe, no mutation (expect PASS)
VERIFY-TRACE attempt=1 begin conn=127.0.0.1:56508->127.0.0.1:39827 hi@begin=0xc0005093e0
VERIFY-TRACE attempt=1 tls-config-built hi@tls=0xc0005093e0 roots=0xc0003eefc0
VERIFY-TRACE attempt=1 end err=<nil>
    followup_probe_test.go:293: connection 1 succeeded under prior roots A
VERIFY-TRACE attempt=2 begin conn=127.0.0.1:34272->127.0.0.1:39375 hi@begin=0xc0005f7200
VERIFY-TRACE attempt=2 tls-config-built hi@tls=0xc0005f7200 roots=0xc0003ef380
VERIFY-TRACE attempt=2 end err=x509: certificate signed by unknown authority
    followup_probe_test.go:344: follow-up connection rejected under replacement roots B: rpc error: code = Unavailable desc = connection error: desc = "transport: authentication handshake failed: x509: certificate signed by unknown authority"
--- PASS: TestVerify_FollowUpConnectionUsesReplacementRoots (0.05s)
ok  	google.golang.org/grpc/verify/repro/followup_probe	0.057s
exit=0
### [ce9652b1] 4. probe, VERIFY_MUTATE=pin-first-roots (expect FAIL)
VERIFY-TRACE attempt=1 begin conn=127.0.0.1:54874->127.0.0.1:42899 hi@begin=0xc000052660
VERIFY-TRACE attempt=1 tls-config-built hi@tls=0xc000052660 roots=0xc0003f8fc0
VERIFY-TRACE attempt=1 MUTATION pinned roots=0xc0003f8fc0 for all later attempts on this HandshakeInfo pointer
VERIFY-TRACE attempt=1 end err=<nil>
    followup_probe_test.go:293: connection 1 succeeded under prior roots A
VERIFY-TRACE attempt=2 begin conn=127.0.0.1:37982->127.0.0.1:38111 hi@begin=0xc00090e180
VERIFY-TRACE attempt=2 tls-config-built hi@tls=0xc00090e180 roots=0xc0003f9380
VERIFY-TRACE attempt=2 MUTATION-ACTIVE current roots ignored; reusing roots=0xc0003f8fc0 pinned by attempt=1
VERIFY-TRACE attempt=2 end err=<nil>
    followup_probe_test.go:342: Follow-up RPC error = <nil>, want x509 unknown authority
--- FAIL: TestVerify_FollowUpConnectionUsesReplacementRoots (0.04s)
FAIL
FAIL	google.golang.org/grpc/verify/repro/followup_probe	0.052s
FAIL
exit=1
### [ce9652b1] 5. new-or-modified tests, VERIFY_MUTATE=pin-first-roots (C1 predicts: all still PASS)
[PASS] Test/TestClientCredsProviderSwitchDuringHandshake
exit=0
### [ce9652b1] 6. per-attempt trace of the tests added on the branch (seeded defect active)
=== RUN   Test
=== RUN   Test/ClientCredsProviderSwitchDuringHandshake
=== RUN   Test/ClientCredsProviderSwitchDuringHandshake/root_provider
VERIFY-TRACE attempt=1 begin conn=127.0.0.1:35644->127.0.0.1:36467 hi@begin=0xc000181140
VERIFY-TRACE attempt=1 tls-config-built hi@tls=0xc0001811a0 roots=0xc0002acc30
VERIFY-TRACE attempt=1 MUTATION pinned roots=0xc0002acc30 for all later attempts on this HandshakeInfo pointer
VERIFY-TRACE attempt=1 end err=<nil>
=== RUN   Test/ClientCredsProviderSwitchDuringHandshake/identity_provider
VERIFY-TRACE attempt=2 begin conn=127.0.0.1:53868->127.0.0.1:34523 hi@begin=0xc000024840
VERIFY-TRACE attempt=2 tls-config-built hi@tls=0xc0000248a0 roots=0xc000098f60
VERIFY-TRACE attempt=2 MUTATION pinned roots=0xc000098f60 for all later attempts on this HandshakeInfo pointer
VERIFY-TRACE attempt=2 end err=<nil>
=== RUN   Test/ClientCredsProviderSwitchDuringHandshake/fallback
VERIFY-TRACE attempt=3 begin conn=127.0.0.1:34214->127.0.0.1:46647 hi@begin=0xc00033a900
VERIFY-TRACE attempt=3 end err=<nil>
=== RUN   Test/ClientCredsProviderSwitchDuringHandshake/replacement_provider_error
VERIFY-TRACE attempt=4 begin conn=127.0.0.1:38622->127.0.0.1:42041 hi@begin=0xc00033a7e0
VERIFY-TRACE attempt=4 end err=xds: fetching trusted roots from CertificateProvider failed: replacement provider error
--- PASS: Test (0.05s)
    --- PASS: Test/ClientCredsProviderSwitchDuringHandshake (0.05s)
        --- PASS: Test/ClientCredsProviderSwitchDuringHandshake/root_provider (0.02s)
        --- PASS: Test/ClientCredsProviderSwitchDuringHandshake/identity_provider (0.02s)
        --- PASS: Test/ClientCredsProviderSwitchDuringHandshake/fallback (0.01s)
        --- PASS: Test/ClientCredsProviderSwitchDuringHandshake/replacement_provider_error (0.00s)
ok  	google.golang.org/grpc/credentials/xds	0.051s
### [ce9652b1] C1 REPRODUCED: the seeded follow-up-roots defect is real (probe red) yet every new-or-modified test stays green
replay exit=0
```

