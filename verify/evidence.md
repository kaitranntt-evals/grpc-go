## Setup

Workspace: `~/repos/grpc-go` on branch `verify/grpc-go-xds-certificate-provider-closure-race-v-4ad1e924` (from `origin/grpc-go-xds-certificate-provider-closure-race-perfect`, HEAD `4f781286`). Claim branches live in a second repository and were fetched into a second remote; each was checked out in its own worktree:

```sh
cd ~/repos/grpc-go
git remote add evalrepo https://github.com/kaitranntt-evals/grpc-go-xds-certificate-provider-closure-race
git fetch evalrepo evalon/grpc-go-xd-ec8b5774 evalon/grpc-go-xd-adeca5bd evalon/grpc-go-xd-c2757af1
git worktree add /home/ubuntu/wt/ec8b5774 evalrepo/evalon/grpc-go-xd-ec8b5774   # 021b4134
git worktree add /home/ubuntu/wt/adeca5bd evalrepo/evalon/grpc-go-xd-adeca5bd   # fe5ffff8
git worktree add /home/ubuntu/wt/c2757af1 evalrepo/evalon/grpc-go-xd-c2757af1   # 37429ba8
git worktree add /home/ubuntu/wt/base     cc234554fb363aea445a838b341bb8a65c8305b0  # task base commit
```

Eval fixtures (`eval_tests.zip`) were provisioned byte-exact into every worktree at the paths the assessment uses (`.evaltools/candidate_test_inventory.go`, `internal/xds/balancer/clusterimpl/tests/eval_handshake_lifetime_test.go`, `test/run_eval_xds_test_group.sh`). `go version go1.25.7 linux/amd64`. All grpc-go tests are `(s)` methods run under `grpctest`, so the `-run` selector is `^Test$/^<Name>$`.

Baseline: the fixture passes unmodified on all three claim branches and on the audited branch, and fails on the base commit:

```console
$ for d in base ec8b5774 adeca5bd c2757af1; do (cd /home/ubuntu/wt/$d && bash ./test/run_eval_xds_test_group.sh handshake-lifetime | grep -E '"Action":"(pass|fail)".*"Test"'); done
base:     eval_handshake_lifetime_test.go:314: Active RPC failed after the Cluster security configuration was replaced: ... xds: fetching trusted roots from CertificateProvider failed: provider instance is closed
          {"Action":"fail","Test":"TestEval_SecurityConfigUpdate_ActiveHandshakeKeepsProvider","Elapsed":0.01}
ec8b5774: {"Action":"pass","Test":"TestEval_SecurityConfigUpdate_ActiveHandshakeKeepsProvider","Elapsed":0.05}
adeca5bd: {"Action":"pass","Test":"TestEval_SecurityConfigUpdate_ActiveHandshakeKeepsProvider","Elapsed":0.05}
c2757af1: {"Action":"pass","Test":"TestEval_SecurityConfigUpdate_ActiveHandshakeKeepsProvider","Elapsed":0.05}
$ cd ~/repos/grpc-go && bash ./test/run_eval_xds_test_group.sh handshake-lifetime | grep -E '"Action":"(pass|fail)".*"Test"'
{"Action":"pass","Test":"TestEval_SecurityConfigUpdate_ActiveHandshakeKeepsProvider","Elapsed":0.04}
```

Mutations used below (patches in `verify/repro/`, production code in the worktrees only; nothing on the audited branch was changed):

- **M1** `m1_delay_replacement_{ec8b5774,base}.patch` — in `clusterImplBalancer.handleSecurityConfig`, after the new providers are built and before the replacement is applied (Swap+Close of the old `HandshakeInfo` on ec8b5774; `cachedRoot.Close()`/`Store` on base), insert `time.Sleep(500 * time.Millisecond)` plus a log line `MUTATION M1: applying replacement security config now`.
- **M2** `m2_sticky_handshakeinfo_{adeca5bd,c2757af1}.patch` — in `credentials/xds.(*credsImpl).ClientHandshake`, pin the first `*HandshakeInfo` ever loaded in a package-level `atomic.Pointer` and reuse it for every later handshake (a replacement configuration never governs any connection).
- **M3** `m3_sticky_roots_{adeca5bd,c2757af1}.patch` — in `(*HandshakeInfo).ClientSideTLSConfig`, after `km, err := rootProv.KeyMaterial(ctx)`, pin the first root pool ever fetched and substitute it into `cfg.RootCAs` and the `KeyMaterial` handed to `buildVerifyFunc` (the replacement provider is still invoked, but its roots never govern a handshake).

Replay scripts: `bash verify/repro/c1_c4_repro.sh`, `bash verify/repro/c2_c3_adeca5bd_repro.sh`, `bash verify/repro/c2_c2757af1_repro.sh` (from the repo root, with `EVAL_TESTS` pointing at the extracted `tests/` dir). The outputs quoted below are from those scripts (run 2026-09-29) and match the earlier manual runs.

## C1

Branch: `evalon/grpc-go-xd-ec8b5774` (worktree `/home/ubuntu/wt/ec8b5774`). New/changed tests: `TestSecurityConfigUpdate_DuringClientHandshake` (`internal/xds/balancer/clusterimpl/tests/clusterimpl_security_test.go`, +248 lines) and `TestHandshakeInfo_AcquireReleaseClose` (`internal/credentials/xds/handshake_info_test.go`, +41 lines). Named parts and what was observed:

1. **Blocks validation-root loading** — holds. `blockingRootCertProvider.KeyMaterial` closes `keyMaterialCalled` and blocks on `unblockKeyMaterial`; the test waits on `oldHooks.keyMaterialCalled` before issuing the update.
2. **Establishes applied replacement (or focused retirement) during the blocked load** — does not hold. After `mgmtServer.Update` the test waits only on `newHooks.built` (closed inside the provider *build* function, which the balancer calls *before* `b.updateHandshakeInfo(...)` swaps/closes) and then a fixed `defaultTestShortTimeout = 100 * time.Millisecond` timer; there is no `UpdateState`/post-application signal. `TestHandshakeInfo_AcquireReleaseClose` does retire while a reference is held but never calls `KeyMaterial` (0 occurrences).
3. **Asserts completion while detecting premature closure** — holds. Pre-release `select` on `oldHooks.closed` fails the test, then `EmptyCall` must succeed with mTLS peer info, then `oldHooks.closed` must fire.

Commands and output (`verify/repro/c1_c4_repro.sh`):

```console
### 1. ec8b5774 test, unmodified branch (expect PASS)
ok  	google.golang.org/grpc/internal/xds/balancer/clusterimpl/tests	0.181s
### 2. ec8b5774 test on the buggy base commit cc234554fb363aea445a838b341bb8a65c8305b0 (expect FAIL: it does detect the original premature close)
    clusterimpl_security_test.go:1008: Old root certificate provider closed while a client handshake is using it
--- FAIL: Test (0.06s)
    --- FAIL: Test/SecurityConfigUpdate_DuringClientHandshake (0.06s)
### 3. the lifetime unit test TestHandshakeInfo_AcquireReleaseClose never loads validation roots (KeyMaterial occurrences in the test body):
0
### 4. apply mutation M1: balancer sleeps 500ms before applying (swapping/closing) the replacement security config
### 5. ec8b5774 test + M1 on the branch (expect PASS; note SubChannel READY (handshake done) precedes the 2nd MUTATION line (replacement applied))
clientconn.go:1302 [core] [Channel #4 SubChannel #6] Subchannel Connectivity change to READY  (t=+1.778459ms)
clusterimpl.go:373 [xds] [xds-cluster-impl-lb 0xc0001ecb40] MUTATION M1: applying replacement security config now (after 500ms delay)  (t=+506.940127ms)
clientconn.go:1302 [core] [Channel #3 SubChannel #10] Subchannel Connectivity change to READY  (t=+615.328422ms)
clusterimpl.go:373 [xds] [xds-cluster-impl-lb 0xc0001ecb40] MUTATION M1: applying replacement security config now (after 500ms delay)  (t=+1.010472443s)
--- PASS: Test (1.06s)
    --- PASS: Test/SecurityConfigUpdate_DuringClientHandshake (1.06s)
### 6. ec8b5774 test + M1 on the BUGGY base (expect PASS: the test is green on code that still closes providers under a live handshake)
clientconn.go:1302 [core] [Channel #4 SubChannel #6] Subchannel Connectivity change to READY  (t=+1.803582ms)
clusterimpl.go:378 [xds] [xds-cluster-impl-lb 0xc000377900] MUTATION M1: applying replacement security config now (after 500ms delay)  (t=+506.353716ms)
clientconn.go:1302 [core] [Channel #3 SubChannel #10] Subchannel Connectivity change to READY  (t=+615.545096ms)
clusterimpl.go:378 [xds] [xds-cluster-impl-lb 0xc000377900] MUTATION M1: applying replacement security config now (after 500ms delay)  (t=+1.009641441s)
--- PASS: Test (1.01s)
    --- PASS: Test/SecurityConfigUpdate_DuringClientHandshake (1.01s)
### 7. eval fixture + M1 on the BUGGY base (expect FAIL: it waits for resolver.ClientConn.UpdateState to return before releasing the load)
    eval_handshake_lifetime_test.go:314: Active RPC failed after the Cluster security configuration was replaced: rpc error: code = Unavailable desc = connection error: desc = "transport: authentication handshake failed: xds: fetching trusted roots from CertificateProvider failed: provider instance is closed"
fixture TestEval_SecurityConfigUpdate_ActiveHandshakeKeepsProvider: fail
```

Reading the timestamps in steps 5/6: the first `MUTATION` line (+506ms) is the *initial* configuration being applied; the data subchannel (`Channel #3 SubChannel #10`) goes READY at +615ms — i.e. the blocked handshake was released ~100ms after `newHooks.built` and completed — and the *replacement* is applied only at +1.01s, ~395ms after the handshake finished. The test still passes, on both the fixed branch and the buggy base commit, because its release is gated on a construction signal plus a 100ms timer rather than on applied replacement. The eval fixture, which waits for the wrapped `resolver.ClientConn.UpdateState` to return before `releaseA()`, correctly fails on the same buggy code.

Impact reasoning: the test does catch the original bug at today's timings (step 2), but it does not *establish* the ordering the task asks for; whenever replacement application is slower than 100ms after provider construction (slow provider build, loaded CI, `-race`), the test degrades to a plain "handshake succeeds" check and stays green against a regression of the fix (step 6).

Verdict: CONFIRMED (part 2 failed; parts 1 and 3 held).

## C2

Both branches were adjudicated independently.

### C2 — `evalon/grpc-go-xd-adeca5bd`

Changed test lines: `internal/xds/balancer/clusterimpl/balancer_test.go` (+294: `TestSecurityConfigUpdate_HandshakeInProgress`, `TestHandshakeSafeProvider`, `fakeCertProvider`, `loadRoots`, `clientConnStateWithSecurityConfig`). The only "follow-up" after the replacement is `hiPtr.Load().ClientSideTLSConfig(ctx, "")` with `cfg.RootCAs.Equal(roots2)` — a helper result, no connection, no trust failure. Commands and output (`verify/repro/c2_c3_adeca5bd_repro.sh`):

```console
### 1. adeca5bd tests, unmodified (expect PASS)
ok  	google.golang.org/grpc/internal/xds/balancer/clusterimpl	0.010s
### 2. eval fixture on unmodified adeca5bd (expect pass)
fixture TestEval_SecurityConfigUpdate_ActiveHandshakeKeepsProvider: pass
### 3. connection-attempt vocabulary in the ADDED test lines of internal/xds/balancer/clusterimpl/balancer_test.go (ClientHandshake/Dial/NewClient/net.Pipe/tls.Client/EmptyCall/UnknownAuthority):
0
### 3b. what the follow-up assertions actually are:
    191:+		cfg, err := hi.ClientSideTLSConfig(ctx, "")
    236:+		if !res.cfg.RootCAs.Equal(roots1) {
    253:+	cfg, err := hiPtr.Load().ClientSideTLSConfig(ctx, "")
    257:+	if !cfg.RootCAs.Equal(roots2) {
    258:+		t.Fatal("ClientSideTLSConfig() returned roots that do not match the roots from the new configuration")
### 4. apply mutation M2: credentials/xds ClientHandshake reuses the first HandshakeInfo it ever saw (replacement never governs any connection)
### 5. adeca5bd tests + M2 (expect PASS: the tests never go through ClientHandshake, so they cannot notice)
--- PASS: Test (0.01s)
    --- PASS: Test/HandshakeSafeProvider (0.00s)
    --- PASS: Test/SecurityConfigUpdate_HandshakeInProgress (0.00s)
ok  	google.golang.org/grpc/internal/xds/balancer/clusterimpl	0.015s
### 6. eval fixture + M2 (expect FAIL: the follow-up RPC never reaches the replacement provider)
    eval_handshake_lifetime_test.go:353: timed out waiting for replacement provider KeyMaterial on the follow-up RPC: context deadline exceeded
fixture TestEval_SecurityConfigUpdate_ActiveHandshakeKeepsProvider: fail
### 7. revert M2, apply mutation M3: ClientSideTLSConfig pins the first root pool ever fetched
### 8. adeca5bd tests + M3 (expect FAIL at the RootCAs.Equal(roots2) helper check: the helper-equality assertion does see root substitution, but through no connection)
    balancer_test.go:583: ClientSideTLSConfig() returned roots that do not match the roots from the new configuration
    --- FAIL: Test/SecurityConfigUpdate_HandshakeInProgress (0.00s)
```

With M2 the replacement roots govern *no* connection at all, yet both new tests stay green (step 5) while the fixture fails (step 6). Step 8 shows the helper-equality check does notice root substitution inside `ClientSideTLSConfig`, but that is exactly the "helper result" the claim excludes: no connection attempt and no attempt-specific outcome exist in the changed lines (step 3: zero hits for any connection vocabulary).

Verdict (adeca5bd): CONFIRMED.

### C2 — `evalon/grpc-go-xd-c2757af1`

Changed test lines: new `internal/xds/balancer/clusterimpl/security_test.go` (+203: `TestSecurityConfigUpdateDuringHandshake` with `fallback=false|true` subtests, `handshakeTestProvider`) and small edits to `credentials/xds/xds_client_test.go` (type change for `grpcsync.RefCounted`). The follow-up is a real `creds.ClientHandshake(ctx, "test.example.com", nextClientConn)` on a fresh `net.Pipe` with no peer; for `fallback=false` the replacement providers were pre-set to `Set(nil, errors.New("replacement provider has no key material"))` and the assertion is that this error string comes back; for `fallback=true` the assertion is `ai.AuthType() == "insecure"`. Commands and output (`verify/repro/c2_c2757af1_repro.sh`):

```console
### 1. c2757af1 test, unmodified (expect PASS)
ok  	google.golang.org/grpc/internal/xds/balancer/clusterimpl	0.031s
### 2. eval fixture on unmodified c2757af1 (expect pass)
fixture TestEval_SecurityConfigUpdate_ActiveHandshakeKeepsProvider: pass
### 3. the follow-up assertions in internal/xds/balancer/clusterimpl/security_test.go:
    100:			const newProviderError = "replacement provider has no key material"
    102:				providers[name].Set(nil, errors.New(newProviderError))
    190:			_, ai, err := creds.ClientHandshake(ctx, "test.example.com", nextClientConn)
    195:				if ai.AuthType() != "insecure" {
    198:			} else if err == nil || !strings.Contains(err.Error(), newProviderError) {
    199:				t.Fatalf("Handshake with replacement configuration returned %v, want %q", err, newProviderError)
### 4. apply mutation M2: ClientHandshake reuses the first HandshakeInfo it ever saw
### 5. c2757af1 test + M2 (expect FAIL: the follow-up ClientHandshake does reach the replacement provider - provider invocation IS established)
--- FAIL: Test (0.01s)
        --- FAIL: Test/SecurityConfigUpdateDuringHandshake/fallback=false (0.01s)
panic: close of closed channel [recovered, repanicked]
	/tmp/verify_c2b_69pDWN/branch/internal/xds/balancer/clusterimpl/security_test.go:58 +0x46
	/tmp/verify_c2b_69pDWN/branch/internal/xds/balancer/clusterimpl/security_test.go:190 +0x1b13
### 6. revert M2, apply mutation M3: ClientSideTLSConfig pins the first root pool ever fetched (replacement roots never govern a handshake)
### 7. c2757af1 test + M3 (expect PASS: the replacement provider errors before any roots are used, so root substitution is invisible)
--- PASS: Test (0.03s)
    --- PASS: Test/SecurityConfigUpdateDuringHandshake (0.03s)
        --- PASS: Test/SecurityConfigUpdateDuringHandshake/fallback=false (0.01s)
        --- PASS: Test/SecurityConfigUpdateDuringHandshake/fallback=true (0.01s)
ok  	google.golang.org/grpc/internal/xds/balancer/clusterimpl	0.033s
### 8. eval fixture + M3 (expect FAIL: follow-up RPC succeeds instead of x509 unknown authority)
    eval_handshake_lifetime_test.go:357: Follow-up RPC error = <nil>, want x509 unknown authority
fixture TestEval_SecurityConfigUpdate_ActiveHandshakeKeepsProvider: fail
```

Step 5 (M2 breaks the test: the follow-up `ClientHandshake` re-enters the *old* root provider, whose `close(p.started)` panics) shows the test does establish that the follow-up attempt consults the replacement provider — the "provider invocation" the claim concedes. Step 7/8 show the gap the claim asserts: the replacement provider only ever returns an error, so no replacement `KeyMaterial` is supplied for the attempt and no TLS handshake runs under replacement roots; when replacement roots are silently discarded (M3) the test stays green while the fixture's `x509: certificate signed by unknown authority` assertion fails. The `fallback=true` subtest asserts an insecure `AuthType`, which involves no roots.

Verdict (c2757af1): CONFIRMED.

## C3

Branch: `evalon/grpc-go-xd-adeca5bd`. Same changed test lines and runs as C2/adeca5bd; the relevant observations, repeated here so this section stands alone (`verify/repro/c2_c3_adeca5bd_repro.sh`):

```console
### 1. adeca5bd tests, unmodified (expect PASS)
ok  	google.golang.org/grpc/internal/xds/balancer/clusterimpl	0.010s
### 3. connection-attempt vocabulary in the ADDED test lines of internal/xds/balancer/clusterimpl/balancer_test.go (ClientHandshake/Dial/NewClient/net.Pipe/tls.Client/EmptyCall/UnknownAuthority):
0
### 3b. what the follow-up assertions actually are:
    253:+	cfg, err := hiPtr.Load().ClientSideTLSConfig(ctx, "")
    257:+	if !cfg.RootCAs.Equal(roots2) {
### 4. apply mutation M2: credentials/xds ClientHandshake reuses the first HandshakeInfo it ever saw (replacement never governs any connection)
### 5. adeca5bd tests + M2 (expect PASS: the tests never go through ClientHandshake, so they cannot notice)
    --- PASS: Test/HandshakeSafeProvider (0.00s)
    --- PASS: Test/SecurityConfigUpdate_HandshakeInProgress (0.00s)
ok  	google.golang.org/grpc/internal/xds/balancer/clusterimpl	0.015s
### 6. eval fixture + M2 (expect FAIL: the follow-up RPC never reaches the replacement provider)
    eval_handshake_lifetime_test.go:353: timed out waiting for replacement provider KeyMaterial on the follow-up RPC: context deadline exceeded
fixture TestEval_SecurityConfigUpdate_ActiveHandshakeKeepsProvider: fail
```

The changed tests drive `clusterImplBalancer.UpdateClientConnState` directly with a `testutils.NewBalancerClientConn` (no transport, no `grpc.NewClient`, no server), pull the `HandshakeInfo` out of the `NewSubConn` address attributes and call `ClientSideTLSConfig` on it. Replacement is published through the production `xdsHIPtr` path, but no connection attempt is ever initiated afterwards and therefore no attempt outcome is asserted; M2 (which makes every real connection ignore the replacement) leaves the tests green.

Verdict: CONFIRMED.

## C4

Branch: `evalon/grpc-go-xd-ec8b5774`. Synchronization trace of the two tests that touch provider lifetime:

- `TestSecurityConfigUpdate_DuringClientHandshake` (`clusterimpl_security_test.go`): wait `oldHooks.keyMaterialCalled` → `mgmtServer.Update(replacement)` → wait `newHooks.built` (closed inside the `certprovider.NewBuildableConfig` build func, i.e. during `buildProvider`, which runs *before* `b.updateHandshakeInfo` → `xdsHIPtr.Swap` → `old.Close()`) → `select` on `oldHooks.closed` vs a `defaultTestShortTimeout` (100ms) timer → `unblock()` (release). No `resolver.ClientConn.UpdateState` wrapper, no `replacementApplied`-style channel, nothing observed after the swap before release.
- `TestHandshakeInfo_AcquireReleaseClose` (`handshake_info_test.go`): `Acquire` → `Close` ×2 → assert not closed → `Acquire` → `Release` ×2 → assert closed once → `Acquire` false. It establishes focused retirement while a reference is held but performs no validation-root loading (`KeyMaterial` occurrences: 0).

Commands and output (`verify/repro/c1_c4_repro.sh`, same run as C1, repeated here):

```console
### 3. the lifetime unit test TestHandshakeInfo_AcquireReleaseClose never loads validation roots (KeyMaterial occurrences in the test body):
0
### 4. apply mutation M1: balancer sleeps 500ms before applying (swapping/closing) the replacement security config
### 5. ec8b5774 test + M1 on the branch (expect PASS; note SubChannel READY (handshake done) precedes the 2nd MUTATION line (replacement applied))
clientconn.go:1302 [core] [Channel #3 SubChannel #10] Subchannel Connectivity change to READY  (t=+615.328422ms)
clusterimpl.go:373 [xds] [xds-cluster-impl-lb 0xc0001ecb40] MUTATION M1: applying replacement security config now (after 500ms delay)  (t=+1.010472443s)
    --- PASS: Test/SecurityConfigUpdate_DuringClientHandshake (1.06s)
### 6. ec8b5774 test + M1 on the BUGGY base (expect PASS: the test is green on code that still closes providers under a live handshake)
clientconn.go:1302 [core] [Channel #3 SubChannel #10] Subchannel Connectivity change to READY  (t=+615.545096ms)
clusterimpl.go:378 [xds] [xds-cluster-impl-lb 0xc000377900] MUTATION M1: applying replacement security config now (after 500ms delay)  (t=+1.009641441s)
    --- PASS: Test/SecurityConfigUpdate_DuringClientHandshake (1.01s)
### 7. eval fixture + M1 on the BUGGY base (expect FAIL: it waits for resolver.ClientConn.UpdateState to return before releasing the load)
    eval_handshake_lifetime_test.go:314: Active RPC failed after the Cluster security configuration was replaced: rpc error: code = Unavailable desc = connection error: desc = "transport: authentication handshake failed: xds: fetching trusted roots from CertificateProvider failed: provider instance is closed"
fixture TestEval_SecurityConfigUpdate_ActiveHandshakeKeepsProvider: fail
```

The blocked root load is released (subchannel READY at +615ms) roughly 395ms before the replacement is applied (+1.01s) and the test passes — including on the base commit where the old provider is still closed synchronously on application. Release is therefore not conditioned on applied replacement; the only test that establishes retirement-before-release (`TestHandshakeInfo_AcquireReleaseClose`) never loads roots.

Impact reasoning: identical to C1 — coverage of the intended race is timing-dependent (100ms window after construction), so the regression guard silently weakens whenever application is slower than that.

Verdict: CONFIRMED.
