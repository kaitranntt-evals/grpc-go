# Evidence — grpc-go xDS certificate-provider closure race (run v-485c3272)

Observations only. Every command below was run on this VM; outputs are verbatim (trimmed to key lines, HandshakeInfo addresses vary per run).

## Common setup (repeat before replaying any section)

```sh
cd ~/repos/grpc-go                                   # checkout of verify/grpc-go-xds-certificate-provider-closure-race-v-485c3272
git remote add evals https://github.com/kaitranntt-evals/grpc-go-xds-certificate-provider-closure-race
for b in 89cb2288 8069941c d3a76037 e709f9c1 48dd6ba3 55c54f73; do
  git fetch evals evalon/grpc-go-xd-$b && git worktree add ../wt-$b FETCH_HEAD
done
mkdir -p ~/eval && unzip -o eval_tests.zip -d ~/eval  # exact fixtures, byte-exact
V=~/repos/grpc-go/verify
```

Target heads observed: 89cb2288=cff2a77c, 8069941c=c168d087, d3a76037=a1ff4def, e709f9c1=42a292d2, 48dd6ba3=828fd86d, 55c54f73=097d75c3. Base = cc234554fb363aea445a838b341bb8a65c8305b0.

Instrumentation under `verify/instrument/` (never committed to any target branch; every script reverts itself):
- `apply_trace.sh` — stderr lines `VERIFY-TRACE ClientHandshake START (new connection attempt)` (one per new connection's client handshake), `VERIFY-TRACE ClientSideTLSConfig attempt hi= <addr>` (each TLS-config build and which HandshakeInfo it used), `VERIFY-TRACE clusterimpl publishes new HandshakeInfo`.
- `apply_post_replacement_mutant.sh` + `verify_mutant.go` — production mutant that makes every connection whose client handshake *starts* after the HandshakeInfo pointer changed fail with `VERIFY-MUTANT: connection initiated after security replacement refused`. Retries inside an already-running handshake are untouched.
- `mutant_sanity_test.go` — proves the mutant bites: a second connection after one replacement fails under the mutant and passes without it.
- `apply_no_final_close_mutant.sh` — removes `b.closeCachedProviders()` from `clusterImplBalancer.Close()` (provider cleanup on final release).

Baseline: `bash test/run_candidate_tests.sh` (fixtures `.evaltools/candidate_test_inventory.go`, `test/run_candidate_tests.sh` copied from `~/eval/tests`) passes every inventoried test on all six target worktrees, e.g.

```console
=== 89cb2288
[PASS] Test/TestClientCredsProviderSwitchDuringHandshake
[PASS] Test/TestSecurityConfigUpdate_ClosedProviders
=== e709f9c1
[PASS] Test/TestClientCredsProviderReplacedDuringHandshake
[PASS] Test/TestClientCredsProviderClosedDuringHandshake
[PASS] Test/TestSecurityConfigUpdate_ProvidersReplacedOnlyOnChange
[PASS] Test/TestSecurityConfigUpdate_DuringHandshake
...
```

## C1

Target: evalon/grpc-go-xd-89cb2288. Claim: the client handshake enters `KeyMaterial` on its selected root provider without securing ownership or copying roots.

Code observed (`internal/credentials/xds/handshake_info.go`, unchanged from base on this branch): `rootProv, idProv := hi.rootProvider, hi.identityProvider` then `km, err := rootProv.KeyMaterial(ctx)` — no reference/hold is taken. The branch's `credentials/xds/xds.go` only retries after an error when `hiPtr.Load() != hi`; `clusterimpl.go` publishes the replacement then calls `closeCachedProviders()` immediately.

Probe 1 — minimal repro `verify/repro/c1_selected_roots_lost_test.go` (provider A = trusted `server_ca` roots, blocks in `KeyMaterial`, errors once closed like store-wrapped providers; provider B = untrusted `client_ca` roots):

```sh
cd ~/repos/wt-89cb2288 && cp $V/repro/c1_selected_roots_lost_test.go credentials/xds/ && go test -tags verify_repro ./credentials/xds -run '^TestVerifyC1' -count=1 -v
```
```console
=== RUN   TestVerifyC1_SelectedRootsSurviveReplacement
    c1_selected_roots_lost_test.go:93: handshake is inside provider A KeyMaterial; publishing replacement B and closing A
    c1_selected_roots_lost_test.go:98: handshake that selected provider A lost that selection during replacement: x509: certificate signed by unknown authority
--- FAIL: TestVerifyC1_SelectedRootsSurviveReplacement (0.01s)
FAIL	google.golang.org/grpc/credentials/xds	0.010s
```

Probe 2 — exact eval fixture through the official runner:

```sh
cd ~/repos/wt-89cb2288 && cp ~/eval/tests/eval_handshake_lifetime_test.go internal/xds/balancer/clusterimpl/tests/ && cp ~/eval/tests/run_eval_xds_test_group.sh test/ && bash ./test/run_eval_xds_test_group.sh handshake-lifetime
```
```console
eval_handshake_lifetime_test.go:314: Active RPC failed after the Cluster security configuration was replaced: rpc error: code = Unavailable desc = connection error: desc = "transport: authentication handshake failed: x509: certificate signed by unknown authority"
```

Contrast — same runner on the audited branch `grpc-go-xds-certificate-provider-closure-race-perfect` (4f781286; `SetHandshakeInfo` takes `*atomic.Pointer[grpcsync.RefCounted[HandshakeInfo]]`, i.e. ownership is secured):

```console
--- PASS: TestEval_SecurityConfigUpdate_ActiveHandshakeKeepsProvider (0.06s)
ok  	google.golang.org/grpc/.evaltools/lifetime_Piosxw	0.065s
```

Impact reasoning: the in-flight handshake had already selected provider A. Because nothing held A, the balancer closed it; A's `KeyMaterial` then failed, the retry loop picked up B (whose roots do not trust the server) and the handshake/RPC failed with `x509: certificate signed by unknown authority`. Any Cluster security update that swaps the CA while a connection is mid-handshake fails that connection (and the active RPC) instead of completing it with the configuration it started with; the retry only masks the race when the replacement happens to trust the same peer.

## C2

Targets: evalon/grpc-go-xd-8069941c, evalon/grpc-go-xd-d3a76037. Claim: the changed tests prove neither cleanup of a replaced provider after its final dependent owner releases it, nor safe earlier cleanup via independently copied roots plus a successful handshake.

Observed production behaviour on both: `clusterimpl.go` closes replaced providers right after publishing the new HandshakeInfo (while the in-flight handshake still uses them); neither branch copies roots (`handshake_info.go` unchanged from base). Handshake success after replacement is obtained by the `xds.go` retry with the *replacement* HandshakeInfo.

Probe 1 — trace (`bash $V/instrument/apply_trace.sh`, then the changed tests):

8069941c `Test/SecurityConfigUpdate_ProvidersReplacedDuringHandshake`:
```console
VERIFY-TRACE clusterimpl publishes new HandshakeInfo
VERIFY-TRACE clusterimpl publishes new HandshakeInfo
VERIFY-TRACE ClientHandshake START (new connection attempt)
VERIFY-TRACE ClientSideTLSConfig attempt hi= 0xc000854900
VERIFY-TRACE clusterimpl publishes new HandshakeInfo
VERIFY-TRACE ClientSideTLSConfig attempt hi= 0xc000179f20
--- PASS: Test/SecurityConfigUpdate_ProvidersReplacedDuringHandshake (0.02s)
```
d3a76037 `Test/SecurityConfigUpdate_ProvidersReplacedDuringHandshake` (d3a's loop retries only when `ClientSideTLSConfig` returns an error, so the second attempt means the first, on the closed providers, failed):
```console
VERIFY-TRACE ClientHandshake START (new connection attempt)
VERIFY-TRACE ClientSideTLSConfig attempt hi= 0xc000660360
VERIFY-TRACE clusterimpl publishes new HandshakeInfo
VERIFY-TRACE ClientSideTLSConfig attempt hi= 0xc000344a20
--- PASS: Test/SecurityConfigUpdate_ProvidersReplacedDuringHandshake (0.02s)
```
The d3a test asserts two `closeCh` receives *before* releasing the handshake (i.e. it asserts early closure), and its fake `blockingCertProvider.KeyMaterial` ignores `Close()` entirely — no test compares roots obtained before vs. after closure.

Probe 2 — remove cleanup on final release (`verify/repro/c2_final_release_cleanup_untested.sh`):

```sh
cd ~/repos/wt-8069941c && bash $V/repro/c2_final_release_cleanup_untested.sh
```
```console
--- PASS: Test/SecurityConfigUpdate_ProvidersReplacedDuringHandshake (0.07s)
ok  	google.golang.org/grpc/internal/xds/balancer/clusterimpl/tests	0.080s
--- PASS: Test/ClientCredsProviderClosedDuringHandshake (0.05s)
--- PASS: Test/ClientCredsProviderReplacedDuringHandshake (0.02s)
ok  	google.golang.org/grpc/credentials/xds	0.091s
```
```sh
cd ~/repos/wt-d3a76037 && bash $V/repro/c2_final_release_cleanup_untested.sh
```
```console
    grpctest.go:45: Leaked goroutine: goroutine 55 [select]:
        created by google.golang.org/grpc/credentials/tls/certprovider/pemfile.newProvider in goroutine 7
--- FAIL: Test/SecurityConfigUpdate_ProvidersReplacedDuringHandshake (10.03s)
--- PASS: Test/ClientCredsProviderReplacedDuringHandshake (0.02s)
ok  	google.golang.org/grpc/credentials/xds	0.024s
```
On d3a76037 the only failure is the generic grpctest goroutine-leak checker catching the *replacement* (current) pemfile providers left open at channel shutdown; the replaced (blocking) providers were already closed at replacement time, so no assertion concerns the replaced provider's release after its last dependent handshake. Unmutated d3a passes (`go test ... -count=3` → `ok ... 0.104s`).

Impact reasoning: on 8069941c the whole changed suite is indifferent to provider cleanup on final release; on both branches the suites lock in "close replaced providers while a handshake still depends on them" and prove success only via retry with the replacement configuration — neither acceptable lifetime model (release after last owner, or early close backed by an independent root copy) is exercised, which is why both branches fail the eval lifetime fixture:
```console
eval_handshake_lifetime_test.go:314: Active RPC failed after the Cluster security configuration was replaced: rpc error: code = Unavailable desc = connection error: desc = "transport: authentication handshake failed: x509: certificate signed by unknown authority"
```

## C3

Targets: evalon/grpc-go-xd-8069941c, evalon/grpc-go-xd-e709f9c1, evalon/grpc-go-xd-48dd6ba3. Claim: the changed tests do not prove that replacement validation roots govern a connection initiated after the replacement.

Probe — `verify/repro/c3_post_replacement_roots_untested.sh` (trace, then post-replacement-connection mutant, then mutant sanity):

```sh
cd ~/repos/wt-<branch> && bash $V/repro/c3_post_replacement_roots_untested.sh
```
8069941c:
```console
## trace
VERIFY-TRACE clusterimpl publishes new HandshakeInfo
VERIFY-TRACE clusterimpl publishes new HandshakeInfo
VERIFY-TRACE ClientHandshake START (new connection attempt)
VERIFY-TRACE ClientSideTLSConfig attempt hi= 0xc0003e25a0
VERIFY-TRACE clusterimpl publishes new HandshakeInfo
VERIFY-TRACE ClientSideTLSConfig attempt hi= 0xc000626540
--- PASS: Test/SecurityConfigUpdate_ProvidersReplacedDuringHandshake (0.09s)
## mutant: refuse connections initiated after replacement
--- PASS: Test/SecurityConfigUpdate_ProvidersReplacedDuringHandshake (0.02s)
--- PASS: Test/ClientCredsProviderClosedDuringHandshake (0.05s)
--- PASS: Test/ClientCredsProviderReplacedDuringHandshake (0.02s)
## mutant sanity (must FAIL)
    mutant_sanity_test.go:41: connection 2 (after 1 replacements): err=VERIFY-MUTANT: connection initiated after security replacement refused
FAIL	google.golang.org/grpc/credentials/xds	0.017s
```
e709f9c1:
```console
VERIFY-TRACE ClientHandshake START (new connection attempt)
VERIFY-TRACE ClientSideTLSConfig attempt hi= 0xc000808c00
VERIFY-TRACE clusterimpl publishes new HandshakeInfo
VERIFY-TRACE ClientSideTLSConfig attempt hi= 0xc0005f7320
--- PASS: Test/SecurityConfigUpdate_DuringHandshake (0.02s)
## mutant: refuse connections initiated after replacement
--- PASS: Test/SecurityConfigUpdate_DuringHandshake (0.07s)
--- PASS: Test/ClientCredsProviderClosedDuringHandshake (0.00s)
--- PASS: Test/ClientCredsProviderReplacedDuringHandshake (0.01s)
## mutant sanity (must FAIL)
    mutant_sanity_test.go:41: connection 2 (after 1 replacements): err=VERIFY-MUTANT: connection initiated after security replacement refused
FAIL	google.golang.org/grpc/credentials/xds	0.013s
```
48dd6ba3:
```console
VERIFY-TRACE ClientHandshake START (new connection attempt)
VERIFY-TRACE ClientSideTLSConfig attempt hi= 0xc00054ed20
VERIFY-TRACE clusterimpl publishes new HandshakeInfo
VERIFY-TRACE ClientSideTLSConfig attempt hi= 0xc00073eae0
--- PASS: Test/SecurityConfigUpdate_DuringHandshake (0.01s)
## mutant: refuse connections initiated after replacement
--- PASS: Test/SecurityConfigUpdate_DuringHandshake (0.02s)
--- PASS: Test/ClientCredsProviderReplacedDuringHandshake (0.01s)
## mutant sanity (must FAIL)
    mutant_sanity_test.go:41: connection 2 (after 1 replacements): err=VERIFY-MUTANT: connection initiated after security replacement refused
FAIL	google.golang.org/grpc/credentials/xds	0.011s
```
Whole inventoried suite under the same mutant (`bash test/run_candidate_tests.sh`): on 8069941c all 13 PASS; on e709f9c1 and 48dd6ba3 every *added* test PASSes and the first failure is the pre-existing, unmodified `TestSecurityConfigUpdate_GoodToFallback` (a switch to insecure fallback, not validation roots). `git diff cc234554 HEAD -- '*_test.go'` shows no removed lines on e709f9c1/48dd6ba3 and only a `setupForSecurityTests` signature change on 8069941c.

Impact reasoning: in every changed test the only connection is the one whose handshake started before the replacement (single `START`, replacement published between its two `ClientSideTLSConfig` attempts). Refusing every post-replacement connection leaves all changed tests green, so nothing shows that a new connection is validated with the replacement roots (e.g. that a replacement CA that does not trust the peer yields `unknown authority`).

## C4

Targets: evalon/grpc-go-xd-e709f9c1, evalon/grpc-go-xd-55c54f73, evalon/grpc-go-xd-48dd6ba3. Claim: the changed tests add or strengthen no separate post-replacement connection attempt with an asserted outcome.

Probe — `verify/repro/c4_no_post_replacement_attempt.sh` (post-replacement-connection mutant over every added test, then mutant sanity):

```sh
cd ~/repos/wt-<branch> && bash $V/repro/c4_no_post_replacement_attempt.sh
```
e709f9c1:
```console
--- PASS: Test/SecurityConfigUpdate_DuringHandshake (0.02s)
--- PASS: Test/ClientCredsProviderClosedDuringHandshake (0.00s)
--- PASS: Test/ClientCredsProviderReplacedDuringHandshake (0.01s)
--- PASS: Test/SecurityConfigUpdate_ProvidersReplacedOnlyOnChange (0.00s)
## mutant sanity (must FAIL)
    mutant_sanity_test.go:41: connection 2 (after 1 replacements): err=VERIFY-MUTANT: connection initiated after security replacement refused
FAIL	google.golang.org/grpc/credentials/xds	0.010s
```
55c54f73:
```console
--- PASS: Test/SecurityConfigUpdate_DuringHandshake (0.02s)
--- PASS: Test/ClientCredsHandshakeWithConcurrentProviderReplacement/root_provider_replaced (0.01s)
--- PASS: Test/ClientCredsHandshakeWithConcurrentProviderReplacement/identity_provider_replaced (0.01s)
--- PASS: Test/ClientCredsHandshakeWithConcurrentProviderReplacement/providers_closed_without_replacement (0.00s)
--- PASS: Test/HandleSecurityConfig_NewHandshakeInfoPublishedBeforeClosingProviders (0.00s)
--- PASS: Test/HandleSecurityConfig_SwitchToFallback (0.00s)
--- PASS: Test/HandleSecurityConfig_UnchangedConfig (0.00s)
## mutant sanity (must FAIL)
    mutant_sanity_test.go:41: connection 2 (after 1 replacements): err=VERIFY-MUTANT: connection initiated after security replacement refused
FAIL	google.golang.org/grpc/credentials/xds	0.010s
```
48dd6ba3:
```console
--- PASS: Test/SecurityConfigUpdate_DuringHandshake (0.02s)
--- PASS: Test/ClientCredsProviderReplacedDuringHandshake (0.01s)
--- PASS: Test/SecurityConfigUpdate_CertProviderLifecycle (0.00s)
## mutant sanity (must FAIL)
    mutant_sanity_test.go:41: connection 2 (after 1 replacements): err=VERIFY-MUTANT: connection initiated after security replacement refused
FAIL	google.golang.org/grpc/credentials/xds	0.010s
```
Trace (`apply_trace.sh`) of each added e2e test shows exactly one `VERIFY-TRACE ClientHandshake START`, before the replacement publish; the added unit tests show one `START` per subtest; the balancer-level tests (`ProvidersReplacedOnlyOnChange`, `CertProviderLifecycle`, `HandleSecurityConfig_*`) make no connection at all (only `publishes` lines). Pre-existing tests are not modified (`git diff cc234554 HEAD -- '*_test.go'` removes no lines on e709f9c1/48dd6ba3; on 55c54f73 it only refactors the `hiPtr` setup of pre-existing unit tests). Under the mutant the inventoried suite's first failure is the untouched pre-existing `TestSecurityConfigUpdate_GoodToFallback`.

Impact reasoning: a regression that breaks every connection created after a Cluster security update (the ordinary reconnect path) would pass all tests these branches add.

## C5

Target: evalon/grpc-go-xd-89cb2288, `TestClientCredsProviderSwitchDuringHandshake` (`credentials/xds/xds_client_test.go`). Note: `internal/xds/balancer/clusterimpl/tests/concurrent_handshake_test.go` / `TestSecurityConfigUpdate_ConcurrentHandshake` do not exist on this branch (`git diff --stat` touches only xds.go, xds_client_test.go, balancer_test.go, clusterimpl.go).

Test structure observed: the initial HandshakeInfo is `NewHandshakeInfo(<blockingProvider as root, or as identity in the identity case>, ..., []matcher.StringMatcher{matcher.NewExactStringMatcher("wrong-SAN", false)}, ...)`; `blockingProvider` wraps an empty `certprovider.Distributor` (no `Set` call). The replacement uses `defaultTestCertSAN` (or fallback); the test then requires success.

Probe — `verify/repro/c5_original_config_unusable_test.go`:

```sh
cd ~/repos/wt-89cb2288 && cp $V/repro/c5_original_config_unusable_test.go credentials/xds/ && go test -tags verify_repro ./credentials/xds -run '^TestVerifyC5' -count=1 -v
```
```console
=== RUN   TestVerifyC5_Part1_BlockingProviderSuppliesNoMaterial
    c5_original_config_unusable_test.go:29: open blockingProvider.KeyMaterial (500ms budget): km=<nil> err=context deadline exceeded
    c5_original_config_unusable_test.go:35: closed blockingProvider.KeyMaterial: km=<nil> err=provider instance is closed
--- PASS: TestVerifyC5_Part1_BlockingProviderSuppliesNoMaterial (0.50s)
=== RUN   TestVerifyC5_Part2_WrongSANMatcherRejectsPeer
    c5_original_config_unusable_test.go:62: handshake with trusted roots + wrong-SAN matcher: err=xds: received SANs {DNSNames: [*.test.example.com], EmailAddresses: [], IPAddresses: [], URIs: []} do not match any of the accepted SANs
--- PASS: TestVerifyC5_Part2_WrongSANMatcherRejectsPeer (0.01s)
ok  	google.golang.org/grpc/credentials/xds	0.511s
```
```sh
openssl x509 -in testdata/x509/server1_cert.pem -noout -ext subjectAltName
```
```console
X509v3 Subject Alternative Name: 
    DNS:*.test.example.com
```
The test itself passes on the branch:
```console
    --- PASS: Test/ClientCredsProviderSwitchDuringHandshake/root (0.01s)
    --- PASS: Test/ClientCredsProviderSwitchDuringHandshake/identity (0.01s)
    --- PASS: Test/ClientCredsProviderSwitchDuringHandshake/multiple_updates (0.01s)
    --- PASS: Test/ClientCredsProviderSwitchDuringHandshake/fallback (0.01s)
```
Impact reasoning: both parts hold — the original provider never yields material (only an error after close) and the original matcher rejects the only peer, so the test can pass only by abandoning the pre-replacement configuration. It therefore encodes "switch to the replacement mid-handshake" as the required behaviour and cannot detect (indeed forbids) completing the in-flight handshake with the configuration it selected, which is the behaviour the eval lifetime fixture requires (`Active RPC failed ... x509: certificate signed by unknown authority` on this branch, PASS on the audited branch).
