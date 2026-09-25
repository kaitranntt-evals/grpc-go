# Evidence — audit v-a5c8b0dd

Observations only. Verification branch `verify/grpc-go-xds-certificate-provider-closure-race-v-a5c8b0dd` was created from `origin/grpc-go-xds-certificate-provider-closure-race-perfect` (HEAD `4f781286`, base `cc234554fb363aea445a838b341bb8a65c8305b0`). Go `go1.25.7 linux/amd64`, 8 CPUs. No production code was modified on any branch; every mutant is applied by a script under `verify/repro/` and reverted by the same script (each run ends with `reverted`).

## Setup (shared by all sections)

```
cd ~/repos/grpc-go
git remote add evalrepo https://github.com/kaitranntt-evals/grpc-go-xds-certificate-provider-closure-race.git
for b in 18d5930f 4df8a32a b14e384a d5a6c3b1 e76d07e7; do
  git fetch evalrepo evalon/grpc-go-xd-$b && git worktree add ../wt-$b FETCH_HEAD
done
```

Worktree HEADs: 18d5930f=`ab780524`, 4df8a32a=`cf3b6094`, b14e384a=`da20fb35`, d5a6c3b1=`e970be25`, e76d07e7=`dda3af55`.
All suite tests use the `grpctest` `(s)` receiver, so `-run` patterns are `Test/<Name>`.

Fixture sanity (audited solution branch, archive bytes, `cmp` reported `byte-exact`): `bash ./test/run_eval_xds_test_group.sh handshake-lifetime` → exit 0, `"Action":"pass",...,"Test":"TestEval_SecurityConfigUpdate_ActiveHandshakeKeepsProvider"`; `bash ./test/run_eval_xds_test_group.sh affected-test-compile` → all `ok` (e.g. `ok google.golang.org/grpc/internal/credentials/xds 1.075s`, `ok google.golang.org/grpc/internal/grpcsync 1.169s`). Fixture copies were removed afterwards.

## C1

Branch: https://github.com/kaitranntt-evals/grpc-go-xds-certificate-provider-closure-race/tree/evalon/grpc-go-xd-4df8a32a (`cf3b6094`). `TestSecurityConfigUpdate_ConcurrentHandshake` does not exist on this branch (`git diff cc234554 HEAD -U0 -- '*_test.go' | grep '^+func .*Test'` lists only `TestHandshakeInfoAcquireReleaseClose`, `TestHandshakeInfoCloseWithoutReferences`, `TestSecurityConfigUpdate_ProviderKeptAliveDuringHandshake`, `TestSecurityConfigUpdate_ReplacedDuringRootLoad`). The two handshake_info tests never call `ClientSideTLSConfig`/`KeyMaterial` (Acquire/Release/Close counters only), so only the two overlap tests are candidates.

Baseline:
```
cd ../wt-4df8a32a
go test ./internal/xds/balancer/clusterimpl/ -run 'Test/SecurityConfigUpdate_ProviderKeptAliveDuringHandshake' -count=1 -v
    --- PASS: Test/SecurityConfigUpdate_ProviderKeptAliveDuringHandshake/replace_security_config (0.00s)
    --- PASS: Test/SecurityConfigUpdate_ProviderKeptAliveDuringHandshake/close_balancer (0.00s)
go test ./internal/xds/balancer/clusterimpl/tests/ -run 'Test/SecurityConfigUpdate_ReplacedDuringRootLoad' -count=1 -v
    --- PASS: Test/SecurityConfigUpdate_ReplacedDuringRootLoad (0.03s)
```

Focused test (balancer_test.go) — no load-entry event: `grep -n` shows `487: go func() {`, `488: _, err := hi.ClientSideTLSConfig(ctx, "")`, `493: test.retire(t, b, xdsC)`, `500: close(root1.unblock)`; its `blockingProvider` has only `unblock` and `closed` channels (lines 340-343), and no `loading`/`entered` identifier appears in the file. Runtime: `verify/repro/c3_load_entry_not_awaited_test.go` replays the same sequence with all original assertions and records entry:
```
cp verify/repro/c3_load_entry_not_awaited_test.go ../wt-4df8a32a/internal/xds/balancer/clusterimpl/
cd ../wt-4df8a32a && go test ./internal/xds/balancer/clusterimpl/ -run 'Test/VerifyC3' -count=1 -v
    c3_load_entry_not_awaited_test.go:106: delay=0s: iterations where validation-root load had entered before retirement=0, had NOT entered=20 (all original assertions passed)
    c3_load_entry_not_awaited_test.go:106: delay=200ms: iterations where validation-root load had entered before retirement=0, had NOT entered=20 (all original assertions passed)
    --- PASS: Test/VerifyC3_RetirementBeforeLoadEntryStillPasses (4.01s)
```
So the focused test passes when retirement happens entirely before load entry: it never establishes "load entry, then retirement".

Integration test (clusterimpl_security_test.go) — construction, not application: it waits `972: case <-trustedProvider.loading:`, calls `982: mgmtServer.Update`, waits `987: case untrustedProvider = <-blockingRootProviderCh:` (sent from the builder func, `814: blockingRootProviderCh <- p`), then `1003: close(trustedProvider.unblock)`. In production `handleSecurityConfig` builds the provider at `363: rp, err := buildProvider(...)` before `384: hi := xds.NewHandshakeInfo(...)`, `385: b.xdsHIPtr.Store(hi)`, `387: b.cachedHI.Close()`, so the awaited signal precedes application/retirement. Mutant run (eager close of the replaced root provider inside handleSecurityConfig, i.e. the original bug, optionally delayed after construction):
```
bash verify/repro/c1_c5_mutant.sh ~/repos/wt-4df8a32a
##### VERIFY_MUTANT_EAGER_CLOSE=0
    --- PASS: Test/SecurityConfigUpdate_ProviderKeptAliveDuringHandshake/replace_security_config
    --- PASS: Test/SecurityConfigUpdate_ReplacedDuringRootLoad (0.02s)
##### VERIFY_MUTANT_EAGER_CLOSE=1 VERIFY_MUTANT_DELAY_MS=0
    balancer_test.go:495: root provider closed while a handshake using it was still in progress
        --- FAIL: Test/SecurityConfigUpdate_ProviderKeptAliveDuringHandshake/replace_security_config (0.00s)
    clusterimpl_security_test.go:1000: Replaced root provider was closed while a handshake using it was still in progress
    --- FAIL: Test/SecurityConfigUpdate_ReplacedDuringRootLoad (20.03s)
##### VERIFY_MUTANT_EAGER_CLOSE=1 VERIFY_MUTANT_DELAY_MS=300
    balancer_test.go:495: root provider closed while a handshake using it was still in progress
        --- FAIL: Test/SecurityConfigUpdate_ProviderKeptAliveDuringHandshake/replace_security_config (0.60s)
    --- PASS: Test/SecurityConfigUpdate_ReplacedDuringRootLoad (0.93s)
reverted
```
With a 300 ms gap between provider construction and application, the integration test releases the load before the (buggy) premature close happens and passes against a mutant that reintroduces the closure race. The focused test does detect premature closure (synchronous `UpdateClientConnState`), but lacks observed load entry. Also note `close_balancer` subtest passes under the mutant because the mutant only affects replacement. Neither test holds all four properties.

Impact: the branch can regress to closing a replaced provider under an in-flight root load while its integration regression test stays green whenever config application lags provider construction; the focused test cannot tell whether the load had started.

## C2

Changed follow-up assertions per branch (grep):
- 18d5930f `internal/xds/balancer/clusterimpl/tests/clusterimpl_security_test.go`: `1036: if _, err := client.EmptyCall(ctx, &testpb.Empty{}); status.Code(err) != codes.Unavailable {` (code only, after `testutils.AwaitState(... TransientFailure)`); load signal `loadStarted` is only awaited for the trusted provider (`991: case <-trusted.loadStarted:`). Other changed tests (`TestSecurityConfigUpdate_BeforeRootLoadStarts`, `_DuringBlockedRootLoad`, `TestSecurityConfigClose_DuringBlockedRootLoad`, `TestHandshakeInfoReferenceCounting`) make no connection.
- d5a6c3b1 `test/xds/xds_client_certificate_providers_test.go`: `649: const wantErr = "x509"`, `650: ... status.Code(err) != codes.Unavailable || !strings.Contains(err.Error(), wantErr)`; no replacement-provider KeyMaterial signal (file-watcher providers).
- b14e384a `credentials/xds/xds_client_test.go`: `819: if _, _, err := creds.ClientHandshake(hsCtx, authority, conn2); err == nil {` / `820: t.Fatal("ClientHandshake() succeeded with replacement roots ...")` (any error accepted).

Baselines (all pass):
```
cd ../wt-18d5930f && go test ./internal/xds/balancer/clusterimpl/ ./internal/xds/balancer/clusterimpl/tests/ ./internal/credentials/xds/ -run 'Test/(SecurityConfig|HandshakeInfoReferenceCounting)' -count=1 -v
    --- PASS: Test/SecurityConfigUpdate_DuringHandshake (0.08s)
cd ../wt-d5a6c3b1 && go test ./test/xds/ ./internal/credentials/xds/ -run 'Test/(ClientSideXDS_SecurityConfigReplacedWithUntrustedRoots|HandshakeInfo_)' -count=1 -v
    --- PASS: Test/ClientSideXDS_SecurityConfigReplacedWithUntrustedRoots (0.04s)
cd ../wt-b14e384a && go test ./credentials/xds/ ./internal/xds/balancer/clusterimpl/ ./internal/credentials/xds/ -run 'Test/(ClientCredsHandshakeInfo|SecurityConfigUpdate_ProvidersRetainedByInProgressHandshake|HandshakeInfo_|AcquireHandshakeInfo_|ClientSideTLSConfig_ProvidersReplaced)' -count=1 -v
    --- PASS: Test/ClientCredsHandshakeInfoReplacedDuringRootsLoad (0.10s)
```

Mutant (`verify/repro/c2_mutant.sh`): after `defer hi.Release()` in `credentials/xds/xds.go`, every client handshake after the first in the process returns `VERIFY_MUTANT_ERR` without ever calling `ClientSideTLSConfig`, i.e. replacement roots are never consulted. Two messages: a generic non-trust error, and an x509 hostname (non-trust) error.
```
bash verify/repro/c2_mutant.sh ~/repos/wt-18d5930f
##### VERIFY_MUTANT_ERR="mutant: follow-up handshake failed for an unrelated non-trust reason"
ok  	google.golang.org/grpc/internal/xds/balancer/clusterimpl/tests	0.095s
ok  	google.golang.org/grpc/internal/xds/balancer/clusterimpl	0.027s
ok  	google.golang.org/grpc/internal/credentials/xds	0.007s
##### VERIFY_MUTANT_ERR="x509: certificate is valid for other.example.com, not test.example.com"
ok  	google.golang.org/grpc/internal/xds/balancer/clusterimpl/tests	0.044s
reverted

bash verify/repro/c2_mutant.sh ~/repos/wt-d5a6c3b1
##### VERIFY_MUTANT_ERR="mutant: follow-up handshake failed for an unrelated non-trust reason"
    xds_client_certificate_providers_test.go:651: EmptyCall() = rpc error: code = Unavailable desc = connection error: desc = "transport: authentication handshake failed: mutant: follow-up handshake failed for an unrelated non-trust reason", want code Unavailable and error containing "x509"
FAIL	google.golang.org/grpc/test/xds	0.144s
##### VERIFY_MUTANT_ERR="x509: certificate is valid for other.example.com, not test.example.com"
ok  	google.golang.org/grpc/test/xds	0.112s
ok  	google.golang.org/grpc/internal/credentials/xds	0.020s
reverted

bash verify/repro/c2_mutant.sh ~/repos/wt-b14e384a
##### VERIFY_MUTANT_ERR="mutant: follow-up handshake failed for an unrelated non-trust reason"
ok  	google.golang.org/grpc/credentials/xds	0.145s
ok  	google.golang.org/grpc/internal/xds/balancer/clusterimpl	0.027s
ok  	google.golang.org/grpc/internal/credentials/xds	0.022s
##### VERIFY_MUTANT_ERR="x509: certificate is valid for other.example.com, not test.example.com"
ok  	google.golang.org/grpc/credentials/xds	0.038s
reverted
```
Per branch: 18d5930f and b14e384a accept any follow-up failure (even a non-x509 error that never loaded the replacement roots); d5a6c3b1 only requires the substring `x509`, which a non-trust x509 failure that never consulted the replacement roots also satisfies. No changed test ties the follow-up outcome to replacement KeyMaterial for that attempt.

Impact: a regression where follow-up connections fail for a reason unrelated to the replacement roots (or never load them) keeps every changed test green on all three branches.

## C3

Branch: https://github.com/kaitranntt-evals/grpc-go-xds-certificate-provider-closure-race/tree/evalon/grpc-go-xd-4df8a32a (`cf3b6094`), `internal/xds/balancer/clusterimpl/balancer_test.go`.

Assertion text (grep): `487: go func() {`, `488: _, err := hi.ClientSideTLSConfig(ctx, "")`, `493: test.retire(t, b, xdsC)`, `500: close(root1.unblock)`. Between 488 and 493 there is no receive; `blockingProvider` (340-343) has fields `unblock` and `closed` only, and `KeyMaterial` (349) signals nothing on entry.

Baseline: `go test ./internal/xds/balancer/clusterimpl/ -run 'Test/SecurityConfigUpdate_ProviderKeptAliveDuringHandshake' -count=1 -v` → `--- PASS: .../replace_security_config (0.00s)`, `--- PASS: .../close_balancer (0.00s)`.

Demonstration (same sequence, all original assertions, entry recorded at retirement time):
```
cp verify/repro/c3_load_entry_not_awaited_test.go ../wt-4df8a32a/internal/xds/balancer/clusterimpl/
cd ../wt-4df8a32a && go test ./internal/xds/balancer/clusterimpl/ -run 'Test/VerifyC3' -count=1 -v
    c3_load_entry_not_awaited_test.go:106: delay=0s: iterations where validation-root load had entered before retirement=0, had NOT entered=20 (all original assertions passed)
    c3_load_entry_not_awaited_test.go:106: delay=200ms: iterations where validation-root load had entered before retirement=0, had NOT entered=20 (all original assertions passed)
    --- PASS: Test/VerifyC3_RetirementBeforeLoadEntryStillPasses (4.01s)
```
In 40/40 iterations retirement completed before `KeyMaterial` was entered, and every assertion of the original test still passed. The test therefore exercises "retire, then start loading" rather than "loading, then retire".

Impact: the focused test cannot catch a regression that only closes providers when retirement races an already-entered root load (e.g. close-on-retire gated on load state).

## C4

Branch: https://github.com/kaitranntt-evals/grpc-go-xds-certificate-provider-closure-race/tree/evalon/grpc-go-xd-e76d07e7 (`dda3af55`).

### Acquisition retry limit
`credentials/xds/xds.go` (grep): `117: hi := hiPtr.Load()`, `126: if !hi.Acquire() {`, `127: hi = hiPtr.Load()`, `131: if !hi.Acquire() {`, `132: return nil, nil, errors.New("xds: security configuration is no longer in use, cannot perform TLS handshake")` — one reload, no loop. Runtime, replacement driven with publishHandshakeInfo's exact operations (`old := hiPtr.Swap(new); old.Retire()`):
```
cp verify/repro/c4_acquire_retry_limit_test.go ../wt-e76d07e7/internal/xds/balancer/clusterimpl/
cd ../wt-e76d07e7 && go test ./internal/xds/balancer/clusterimpl/ -run 'Test/VerifyC4_SwapRetireScheduleDirect' -count=1 -v
    c4_acquire_retry_limit_test.go:208: swap+retire replacements=1232180 handshakes=7533 acquisition-errors=5; errors immediately followed by an acquirable published config=5
    c4_acquire_retry_limit_test.go:210: ClientHandshake aborted 5 times with "xds: security configuration is no longer in use, cannot perform TLS handshake" while a usable configuration was published
    --- FAIL: Test/VerifyC4_SwapRetireScheduleDirect (0.39s)
```

### Successive replacement interleaving
`internal/xds/balancer/clusterimpl/clusterimpl.go`: `378: func (b *clusterImplBalancer) publishHandshakeInfo(hi *xds.HandshakeInfo) {`, `379: if old := b.xdsHIPtr.Swap(hi); old != nil {`, `380: old.Retire()`. `HandshakeInfo.Acquire` returns false once `retired` is set (handshake_info.go 153-161). The only shared state between the balancer and `ClientHandshake` is the atomic pointer and each HandshakeInfo's own mutex; nothing orders a handshake's Load→Acquire pair against a Swap→Retire, and replacements are only serialized among themselves. So: handshake Loads A; balancer Swaps B, Retires A; Acquire(A) fails; handshake Loads B; balancer Swaps C, Retires B; Acquire(B) fails → error, while C is published and live. The direct run above hits exactly this (every error was followed by a successful `Acquire` of the current publication). Through the real balancer entry point (`UpdateClientConnState` → `handleSecurityConfig` → `publishHandshakeInfo`) the window is much narrower (15 s run: no hit):
```
cd ../wt-e76d07e7 && go test ./internal/xds/balancer/clusterimpl/ -run 'Test/VerifyC4_ClientHandshakeAbortsWhileUsableConfigPublished' -count=1 -v
    c4_acquire_retry_limit_test.go:143: security replacements=468532 handshakes=289043 acquisition-errors=0; published config acquirable after run=true
    --- PASS: Test/VerifyC4_ClientHandshakeAbortsWhileUsableConfigPublished (15.24s)
```
The longer run (stops at first error) reproduced it through the production entry point after ~30 s:
```
VERIFY_C4_SECONDS=240 go test ./internal/xds/balancer/clusterimpl/ -run 'Test/VerifyC4_ClientHandshakeAbortsWhileUsableConfigPublished' -count=1 -timeout 15m -v
    c4_acquire_retry_limit_test.go:149: security replacements=634761 handshakes=387576 acquisition-errors=1; published config acquirable after run=true
    c4_acquire_retry_limit_test.go:151: ClientHandshake aborted 1 times with "xds: security configuration is no longer in use, cannot perform TLS handshake" although the balancer always publishes a live replacement before retiring the old one
    --- FAIL: Test/VerifyC4_ClientHandshakeAbortsWhileUsableConfigPublished (30.72s)
```

Impact: under back-to-back Cluster security updates, a new connection can fail its handshake with "security configuration is no longer in use" although a valid configuration is published; the subchannel goes to TRANSIENT_FAILURE and backs off instead of connecting. Rare in practice (needs two replacements inside two ~ns windows of one handshake), but permitted by the synchronization.

## C5

Branch: https://github.com/kaitranntt-evals/grpc-go-xds-certificate-provider-closure-race/tree/evalon/grpc-go-xd-4df8a32a (`cf3b6094`). Overlap tests on this branch: `TestSecurityConfigUpdate_ProviderKeptAliveDuringHandshake` and `TestSecurityConfigUpdate_ReplacedDuringRootLoad` (`TestSecurityConfigUpdate_ConcurrentHandshake`, `providerA.entered`, `replacementApplied`, `releaseA()` exist only on the solution branch, not here).

Ordering 1 (entry before retirement) — focused test: `488: _, err := hi.ClientSideTLSConfig(ctx, "")` in a goroutine, then `493: test.retire(t, b, xdsC)` with no entry receive. Runtime:
```
cd ../wt-4df8a32a && go test ./internal/xds/balancer/clusterimpl/ -run 'Test/VerifyC3' -count=1 -v
    c3_load_entry_not_awaited_test.go:106: delay=0s: iterations where validation-root load had entered before retirement=0, had NOT entered=20 (all original assertions passed)
    c3_load_entry_not_awaited_test.go:106: delay=200ms: iterations where validation-root load had entered before retirement=0, had NOT entered=20 (all original assertions passed)
```

Ordering 2 (application before release) — integration test: `972: case <-trustedProvider.loading:` → `982: mgmtServer.Update` → `987: case untrustedProvider = <-blockingRootProviderCh:` (provider construction, from builder `814`) → `1003: close(trustedProvider.unblock)`; production applies after construction (`363` buildProvider, `385` Store, `387` cachedHI.Close()). Runtime:
```
bash verify/repro/c1_c5_mutant.sh ~/repos/wt-4df8a32a
##### VERIFY_MUTANT_EAGER_CLOSE=1 VERIFY_MUTANT_DELAY_MS=0
    clusterimpl_security_test.go:1000: Replaced root provider was closed while a handshake using it was still in progress
    --- FAIL: Test/SecurityConfigUpdate_ReplacedDuringRootLoad (20.03s)
##### VERIFY_MUTANT_EAGER_CLOSE=1 VERIFY_MUTANT_DELAY_MS=300
    balancer_test.go:495: root provider closed while a handshake using it was still in progress
    --- PASS: Test/SecurityConfigUpdate_ReplacedDuringRootLoad (0.93s)
reverted
```
With application delayed 300 ms after construction, the integration test releases the load before application and passes even though the mutant closes the replaced provider on application. Each test establishes at most one of the two orderings.

Impact: no single regression test on this branch pins the full entry → application/retirement → release sequence, so the race can regress with one of the two tests still green depending on scheduling.
