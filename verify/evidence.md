# Evidence — audit run v-e1f2f310

Workspace: `~/repos/grpc-go`, branch `verify/grpc-go-xds-certificate-provider-closure-race-v-e1f2f310`
(checked out from `origin/grpc-go-xds-certificate-provider-closure-race-perfect`, HEAD `4f781286`).
Claim-target branches were fetched from the repository named in the claim URLs
(`https://github.com/kaitranntt-evals/grpc-go-xds-certificate-provider-closure-race`, remote `claims`)
and checked out as detached worktrees under `/home/ubuntu/wt/<id>`; every one of them is a single commit on top
of upstream `cc234554` ("xdsclient: store per-resource error in resource state ...").

Eval fixtures from `eval_tests.zip` were extracted byte-exact to `/home/ubuntu/eval_tests/tests/` and copied to the
documented destinations (`.evaltools/candidate_test_inventory.go`,
`internal/xds/balancer/clusterimpl/tests/eval_handshake_lifetime_test.go`, `test/run_eval_xds_test_group.sh`,
`test/run_candidate_tests.sh`) in the workspace and in each worktree. On the workspace branch:

```console
$ bash ./test/run_eval_xds_test_group.sh handshake-lifetime 2>&1 | grep -E '"Action":"(pass|fail)"|--- (PASS|FAIL)'
{"Time":"2026-09-28T15:48:31.420620412Z","Action":"output","Package":"google.golang.org/grpc/.evaltools/lifetime_djOBfi","Test":"TestEval_SecurityConfigUpdate_ActiveHandshakeKeepsProvider","Output":"--- PASS: TestEval_SecurityConfigUpdate_ActiveHandshakeKeepsProvider (0.04s)\n"}
{"Time":"2026-09-28T15:48:31.420634729Z","Action":"pass","Package":"google.golang.org/grpc/.evaltools/lifetime_djOBfi","Test":"TestEval_SecurityConfigUpdate_ActiveHandshakeKeepsProvider","Elapsed":0.04}
{"Time":"2026-09-28T15:48:31.421661825Z","Action":"pass","Package":"google.golang.org/grpc/.evaltools/lifetime_djOBfi","Elapsed":0.047}
```

The same fixture run (`bash ./test/run_eval_xds_test_group.sh handshake-lifetime`) also produced `"Action":"pass"` on
every claim-target worktree (8ac7b031, aeb669ac, 71955f29, 43420846, c15d4e27, ce71ca89, 3ed13f94), so all
candidate implementations behave correctly; the claims below are about the *evidence their tests provide*, not about
the implementations.

Note on `-run` patterns: these packages use `grpctest.RunSubTests`, so the candidate tests are subtests of `Test`
(`Test/<Name>` without the `Test` prefix). A pattern such as `Test/SecurityConfigUpdate_DuringHandshake$` also
matches the top-level `TestEval_...` fixture in the same package, which is why the fixture appears in some outputs
below; the repro scripts anchor with `^Test$/...`.

Mutation used for C2/C3 (**M5**, source in `verify/repro/lib/m5_stale_key_material.go.txt`, applied by
`verify/repro/lib/run_follow_up_roots_mutation.sh`): in `HandshakeInfo.ClientSideTLSConfig` the handshake still calls
`KeyMaterial()` on the root and identity providers of the HandshakeInfo it acquired, but the `tls.Config` is built from
the key material returned by the *first* successful call in the process:

```diff
 	km, err := rootProv.KeyMaterial(ctx)
+	km, err = evalM5Stale(&evalM5Roots, km, err)
 ...
 		km, err := idProv.KeyMaterial(ctx)
+		km, err = evalM5Stale(&evalM5Certs, km, err)
```

Under M5 every "the new provider's KeyMaterial was called" signal still fires, but a follow-up connection is **not**
governed by the replacement validation roots. A test with causal follow-up evidence must fail on this mutant; the eval
fixture does (`Follow-up RPC error = <nil>, want x509 unknown authority`). The mutation was applied to a backup copy
of `handshake_info.go` in each worktree and reverted afterwards (`git status` clean apart from the untracked fixtures).

## C1

Target: [evalon/grpc-go-xd-8ac7b031](https://github.com/kaitranntt-evals/grpc-go-xds-certificate-provider-closure-race/tree/evalon/grpc-go-xd-8ac7b031)
(commit `211af8d5`, worktree `/home/ubuntu/wt/8ac7b031`). Changed tests:
`internal/xds/balancer/clusterimpl/tests/clusterimpl_security_test.go` (`TestSecurityConfigUpdate_DuringHandshake`)
and `internal/credentials/xds/handshake_info_test.go` (`TestHandshakeInfoAcquireRelease*`).

### What the test asserts (assertion text)

`blockingCertProvider.KeyMaterial` blocks until `Unblock()` and, once unblocked, refuses to serve after `Close()`
(line 846: `return nil, errors.New("provider instance is closed")`). Test flow (line numbers in the branch file):

- 1006–1015: waits for `root1`/`identity1` to be built and for `root1.keyMaterialCalled` — the handshake is blocked
  inside root loading.
- 1017–1025: `mgmtServer.Update(ctx, resourcesFor("cert2", ...))`, then `root2 := receiveBlockingCertProvider(ctx, t)`
  and `identity2 := ...` — the replacement's providers are built by `handleSecurityConfig`, which publishes the
  replacement HandshakeInfo right after building them.
- 1026–1035: 100 ms (`defaultTestShortTimeout`) window in which `root1.closed` / `identity1.closed` fail the test:
  `"Root certificate provider was closed while a handshake was still using it"` /
  `"Identity certificate provider was closed while a handshake was still using it"`.
- 1039–1055: `root1.Unblock()`; RPC must succeed on `server1Address` with mTLS; `root2.keyMaterialCalled` must **not**
  be closed (`"Handshake used the new root certificate provider, want the one it started with"`).
- 1058–1065: `root1.closed` and `identity1.closed` must fire (`"Timeout waiting for the replaced %s certificate
  provider to be closed"`).

### Run on the branch as committed

```console
$ cd /home/ubuntu/wt/8ac7b031
$ go test -race -count=3 -v -run 'Test/SecurityConfigUpdate_DuringHandshake$' ./internal/xds/balancer/clusterimpl/tests/ 2>&1 | grep -E '^(\s*---|ok|FAIL|PASS)'
--- PASS: Test (0.26s)
    --- PASS: Test/SecurityConfigUpdate_DuringHandshake (0.26s)
--- PASS: TestEval_SecurityConfigUpdate_ActiveHandshakeKeepsProvider (0.05s)
--- PASS: Test (0.23s)
    --- PASS: Test/SecurityConfigUpdate_DuringHandshake (0.23s)
--- PASS: TestEval_SecurityConfigUpdate_ActiveHandshakeKeepsProvider (0.06s)
--- PASS: Test (0.24s)
    --- PASS: Test/SecurityConfigUpdate_DuringHandshake (0.23s)
--- PASS: TestEval_SecurityConfigUpdate_ActiveHandshakeKeepsProvider (0.05s)
PASS
ok  	google.golang.org/grpc/internal/xds/balancer/clusterimpl/tests	1.918s
$ go test -race -count=3 -v -run 'Test/HandshakeInfo' ./internal/credentials/xds/ 2>&1 | grep -E '^(\s*---|ok|FAIL|PASS)'
--- PASS: Test (0.01s)
    --- PASS: Test/HandshakeInfoAcquireRelease (0.00s)
    --- PASS: Test/HandshakeInfoAcquireRelease_Concurrent (0.01s)
... (x3)
ok  	google.golang.org/grpc/internal/credentials/xds	1.037s
```

### Does the test detect premature closure? — the same test file on the pre-fix code

Worktree `/home/ubuntu/wt/base` at the branch's parent commit `cc234554` (original code that closes `cachedRoot` /
`cachedIdentity` on every security-config change), with only the branch's test file copied over:

```console
$ cd /home/ubuntu/wt/8ac7b031 && git worktree add /home/ubuntu/wt/base cc234554
$ cp internal/xds/balancer/clusterimpl/tests/clusterimpl_security_test.go /home/ubuntu/wt/base/internal/xds/balancer/clusterimpl/tests/clusterimpl_security_test.go
$ cd /home/ubuntu/wt/base && git status --short
 M internal/xds/balancer/clusterimpl/tests/clusterimpl_security_test.go
$ go test -race -count=1 -v -run 'Test/SecurityConfigUpdate_DuringHandshake$' ./internal/xds/balancer/clusterimpl/tests/ 2>&1 | grep -E '^(\s*---|ok|FAIL|PASS|\s+clusterimpl_security_test)'
    clusterimpl_security_test.go:1033: Identity certificate provider was closed while a handshake was still using it
--- FAIL: Test (0.03s)
    --- FAIL: Test/SecurityConfigUpdate_DuringHandshake (0.03s)
FAIL
FAIL	google.golang.org/grpc/internal/xds/balancer/clusterimpl/tests	0.055s
```

Second mutant, on the branch's own code: the handshake drops its reference before handshaking
(`credentials/xds/xds.go`, `defer hi.Release()` → `hi.Release()`), so the balancer's `publishHandshakeInfo` closes the
providers while the handshake is blocked:

```console
$ cd /home/ubuntu/wt/8ac7b031 && sed -i 's/^\tdefer hi.Release()$/\thi.Release()/' credentials/xds/xds.go
$ go test -race -count=1 -v -run 'Test/SecurityConfigUpdate_DuringHandshake$' ./internal/xds/balancer/clusterimpl/tests/ 2>&1 | grep -E '^(\s*---|ok|FAIL|PASS|\s+clusterimpl_security_test.go:[0-9]+: [A-Z])'
    clusterimpl_security_test.go:1033: Identity certificate provider was closed while a handshake was still using it
--- FAIL: Test (0.03s)
    --- FAIL: Test/SecurityConfigUpdate_DuringHandshake (0.03s)
--- FAIL: TestEval_SecurityConfigUpdate_ActiveHandshakeKeepsProvider (0.01s)
FAIL
FAIL	google.golang.org/grpc/internal/xds/balancer/clusterimpl/tests	0.075s
```

(`xds.go` restored from a backup copy afterwards; `git status --short` shows only the untracked fixtures.)

### Is the replacement established before the blocked load is released?

Temporary `fmt.Fprintf(os.Stderr, ...)` instrumentation (reverted afterwards) in
`clusterImplBalancer.publishHandshakeInfo` (`clusterimpl.go`) and right after `rootProv.KeyMaterial(ctx)` returns in
`HandshakeInfo.ClientSideTLSConfig` (`handshake_info.go`), 5 iterations. Each iteration has two publishes (initial
config, then the `cert2` replacement) and two root loads (the blocked handshake released by `root1.Unblock()`, then the
follow-up connection):

```console
$ go test -race -count=5 -v -run 'Test/SecurityConfigUpdate_DuringHandshake$' ./internal/xds/balancer/clusterimpl/tests/ 2>&1 | grep -E '^(AUDIT|\s*--- (PASS|FAIL): Test/|ok|FAIL)'
AUDIT 15:42:25.864421 publishHandshakeInfo: new HandshakeInfo stored, old released
AUDIT 15:42:25.866941 publishHandshakeInfo: new HandshakeInfo stored, old released
AUDIT 15:42:25.967967 ClientSideTLSConfig: root KeyMaterial returned (provider=0xc000512130 err=<nil>)
AUDIT 15:42:25.990929 ClientSideTLSConfig: root KeyMaterial returned (provider=0xc00070e2f0 err=<nil>)
    --- PASS: Test/SecurityConfigUpdate_DuringHandshake (0.25s)
AUDIT 15:42:26.153728 publishHandshakeInfo: new HandshakeInfo stored, old released
AUDIT 15:42:26.156529 publishHandshakeInfo: new HandshakeInfo stored, old released
AUDIT 15:42:26.257277 ClientSideTLSConfig: root KeyMaterial returned (provider=0xc0001a61c8 err=<nil>)
AUDIT 15:42:26.277808 ClientSideTLSConfig: root KeyMaterial returned (provider=0xc000512138 err=<nil>)
    --- PASS: Test/SecurityConfigUpdate_DuringHandshake (0.23s)
... (3 more iterations with the identical ordering)
ok  	google.golang.org/grpc/internal/xds/balancer/clusterimpl/tests	2.466s
```

In all 5 iterations the replacement HandshakeInfo was published ~100 ms *before* the blocked root load returned
(the test's 100 ms "must not be closed" window sits between them), the blocked handshake then completed on the old
provider (`root2.keyMaterialCalled` not closed, asserted at line 1053) and the old providers closed afterwards.

### Conclusion for C1

The changed test independently demonstrates the property: it holds the handshake inside root loading, applies the
replacement Cluster config and waits until the replacement's providers are built (the LB policy publishes the new
HandshakeInfo immediately after, as the instrumentation shows), asserts the old providers are still open, releases the
load, asserts success on the old configuration and closure afterwards. Premature closure is detected two ways (the
`closed` channel within the window, and `KeyMaterial` returning "provider instance is closed" after unblocking), and
the pre-fix code fails the test deterministically. **C1 REFUTED.**

## C2

Same setup for all five branches: `credentials/xds` unit tests use `defaultTestShortTimeout = 10ms`,
`internal/xds/balancer/clusterimpl/tests` e2e tests use `100ms`. Every branch's changed tests pass as committed
(`-race -count=3`). The question is what each test asserts about the *follow-up* connection after the replacement, and
whether that assertion is tied to the replacement validation roots. M5 (described at the top) is the discriminating
mutant: KeyMaterial is still requested from the replacement providers, but the follow-up connection uses the first
handshake's material.

### C2 — evalon/grpc-go-xd-aeb669ac

Changed test: `credentials/xds/xds_client_test.go` `TestClientCredsSecurityConfigReplacedDuringHandshake`.
Both providers load the same roots: `root1 := newBlockingRootProvider(t, "x509/server_ca_cert.pem", true)` and
`root2 := newBlockingRootProvider(t, "x509/server_ca_cert.pem", false)`. Follow-up assertions (lines 793–814):

```go
_, ai, err := creds.ClientHandshake(hsCtx, authority, conn2)
if err != nil { t.Fatalf("ClientHandshake() failed: %v", err) }
if err := compareAuthInfo(ctx, ts, ai); err != nil { t.Fatal(err) }
select {
case <-root2.kmCalled:
default:
	t.Fatal("New root provider not used for handshake after security configuration update")
}
select {
case <-root1.kmCalled:
	t.Fatal("Replaced root provider used for handshake after security configuration update")
default:
}
```

`kmCalled` is a buffered channel (`make(chan struct{}, 10)`) that receives on *every* KeyMaterial call; the assertion
is only "root2.KeyMaterial has been called at some point", and the trust result of the follow-up is identical whichever
provider's roots are used.

```console
$ cd /home/ubuntu/wt/aeb669ac
$ go test -race -count=3 -v -run 'Test/ClientCredsSecurityConfigReplacedDuringHandshake$' ./credentials/xds/ 2>&1 | grep -E '^(\s*---|ok|FAIL|PASS|\s+xds_client)'
--- PASS: Test (0.04s)
    --- PASS: Test/ClientCredsSecurityConfigReplacedDuringHandshake (0.03s)
... (x3)
ok  	google.golang.org/grpc/credentials/xds	1.122s
$ bash /home/ubuntu/audit/apply_m5.sh /home/ubuntu/wt/aeb669ac      # same edit as verify/repro/lib/run_follow_up_roots_mutation.sh
+	km, err = evalM5Stale(&evalM5Roots, km, err)
+		km, err = evalM5Stale(&evalM5Certs, km, err)
$ go test -race -count=1 -v -run 'Test/ClientCredsSecurityConfigReplacedDuringHandshake$' ./credentials/xds/ 2>&1 | grep -E '^(\s*---|ok|FAIL|PASS|\s+xds_client)'
--- PASS: Test (0.04s)
    --- PASS: Test/ClientCredsSecurityConfigReplacedDuringHandshake (0.03s)
PASS
ok  	google.golang.org/grpc/credentials/xds	1.047s
```

Replayable repro (`bash verify/repro/c2_aeb669ac_follow_up_roots.sh`), key output:

```console
== under mutation M5
-- branch test: go test -race -count=1 -v -run '^Test$/ClientCredsSecurityConfigReplacedDuringHandshake$' ./credentials/xds/
--- PASS: Test (0.04s)
    --- PASS: Test/ClientCredsSecurityConfigReplacedDuringHandshake (0.03s)
ok  	google.golang.org/grpc/credentials/xds	1.048s
-- eval fixture: go test -race -count=1 -v -run '^TestEval_SecurityConfigUpdate_ActiveHandshakeKeepsProvider$' ./internal/xds/balancer/clusterimpl/tests/
    eval_handshake_lifetime_test.go:357: Follow-up RPC error = <nil>, want x509 unknown authority
--- FAIL: TestEval_SecurityConfigUpdate_ActiveHandshakeKeepsProvider (0.06s)
== result
WEAKNESS REPRODUCED: the branch test still PASSES when the follow-up connection is not governed by the replacement roots; the eval fixture FAILS on the same mutant.
```

Impact: the test passes while the follow-up handshake is validated with the *old* roots; it adds no distinguishable
trust result and no outcome tied to the replacement KeyMaterial for that attempt. The pre-existing
`TestClientCredsProviderSwitch` already covered "new provider's roots are used after a pointer swap" with
distinguishable roots (client CA → failure, then server CA → success), so nothing is strengthened. **CONFIRMED.**

### C2 — evalon/grpc-go-xd-71955f29

Changed test: `credentials/xds/xds_client_test.go` `TestClientCredsSecurityConfigReplacedDuringHandshake` (plus
`TestClientCredsReleasedSecurityConfig`, which has no follow-up connection). Both providers load
`"x509/server_ca_cert.pem"`. Follow-up assertions (lines 806–819):

```go
_, ai, err := creds.ClientHandshake(hsCtx, authority, conn2)
if err != nil { t.Fatalf("ClientHandshake() after security configuration update failed: %v", err) }
if err := compareAuthInfo(ctx, ts, ai); err != nil { t.Fatal(err) }
if got := len(newRoot.kmCalled); got != 1 {
	t.Fatalf("New root provider KeyMaterial() called %d times, want 1", got)
}
if got := len(oldRoot.kmCalled); got != 0 {
	t.Fatalf("Old root provider KeyMaterial() called %d more times after update, want 0", got)
}
```

```console
$ cd /home/ubuntu/wt/71955f29
$ go test -race -count=3 -v -run 'Test/(ClientCredsSecurityConfigReplacedDuringHandshake|ClientCredsReleasedSecurityConfig)$' ./credentials/xds/ 2>&1 | grep -E '^(\s*---|ok|FAIL|PASS|\s+xds_client)'
--- PASS: Test (0.04s)
    --- PASS: Test/ClientCredsReleasedSecurityConfig (0.00s)
    --- PASS: Test/ClientCredsSecurityConfigReplacedDuringHandshake (0.03s)
... (x3)
ok  	google.golang.org/grpc/credentials/xds	1.118s
$ bash /home/ubuntu/audit/apply_m5.sh /home/ubuntu/wt/71955f29
+	km, err = evalM5Stale(&evalM5Roots, km, err)
+		km, err = evalM5Stale(&evalM5Certs, km, err)
$ go test -race -count=1 -v -run 'Test/ClientCredsSecurityConfigReplacedDuringHandshake$' ./credentials/xds/ 2>&1 | grep -E '^(\s*---|ok|FAIL|PASS|\s+xds_client)'
--- PASS: Test (0.03s)
    --- PASS: Test/ClientCredsSecurityConfigReplacedDuringHandshake (0.03s)
PASS
ok  	google.golang.org/grpc/credentials/xds	1.046s
```

Replayable repro (`bash verify/repro/c2_71955f29_follow_up_roots.sh`), key output:

```console
== under mutation M5
-- branch test: go test -race -count=1 -v -run '^Test$/ClientCredsSecurityConfigReplacedDuringHandshake$' ./credentials/xds/
--- PASS: Test (0.03s)
    --- PASS: Test/ClientCredsSecurityConfigReplacedDuringHandshake (0.03s)
ok  	google.golang.org/grpc/credentials/xds	1.046s
-- eval fixture: ...
    eval_handshake_lifetime_test.go:357: Follow-up RPC error = <nil>, want x509 unknown authority
--- FAIL: TestEval_SecurityConfigUpdate_ActiveHandshakeKeepsProvider (0.06s)
== result
WEAKNESS REPRODUCED: the branch test still PASSES when the follow-up connection is not governed by the replacement roots; the eval fixture FAILS on the same mutant.
```

Impact: as for aeb669ac — a KeyMaterial call count with identical roots; the follow-up's trust outcome carries no
information about which roots governed it. **CONFIRMED.**

### C2 — evalon/grpc-go-xd-43420846

Changed tests: `credentials/xds/xds_client_test.go` `TestClientCredsProviderSwitchDuringHandshake`,
`internal/credentials/xds/handshake_info_test.go`, `internal/xds/balancer/clusterimpl/balancer_test.go`
`TestSecurityConfigUpdate_ProviderLifecycle`. The replacement root provider is
`root2 := &fakeProvider{err: root2Err}` with `root2Err := errors.New("root provider from updated security
configuration")` (lines 757–758), and the follow-up asserts (lines 800–801):

```go
if _, _, err := creds.ClientHandshake(ctx, authority, conn2); err == nil || !strings.Contains(err.Error(), root2Err.Error()) {
	t.Fatalf("ClientHandshake() with updated security configuration returned error %v, want error containing %q", err, root2Err.Error())
}
```

The follow-up attempt's result is required to carry the replacement root provider's KeyMaterial error — an outcome
that can only come from the replacement provider governing *that* attempt.

```console
$ cd /home/ubuntu/wt/43420846
$ go test -race -count=3 -v -run 'Test/ClientCredsProviderSwitchDuringHandshake$' ./credentials/xds/ 2>&1 | grep -E '^(\s*---|ok|FAIL|PASS|\s+xds_client)'
--- PASS: Test (0.07s)
    --- PASS: Test/ClientCredsProviderSwitchDuringHandshake (0.07s)
... (x3)
ok  	google.golang.org/grpc/credentials/xds	1.290s
$ bash /home/ubuntu/audit/apply_m5.sh /home/ubuntu/wt/43420846
+	km, err = evalM5Stale(&evalM5Roots, km, err)
+		km, err = evalM5Stale(&evalM5Certs, km, err)
$ go test -race -count=1 -v -run 'Test/ClientCredsProviderSwitchDuringHandshake$' ./credentials/xds/ 2>&1 | grep -E '^(\s*---|ok|FAIL|PASS|\s+xds_client)'
    xds_client_test.go:801: ClientHandshake() with updated security configuration returned error <nil>, want error containing "root provider from updated security configuration"
--- FAIL: Test (0.04s)
    --- FAIL: Test/ClientCredsProviderSwitchDuringHandshake (0.04s)
FAIL
FAIL	google.golang.org/grpc/credentials/xds	0.052s
```

The test fails as soon as the follow-up is not governed by the replacement provider's KeyMaterial. **REFUTED.**

### C2 — evalon/grpc-go-xd-c15d4e27

Changed tests: `credentials/xds/xds_client_test.go` `TestClientCredsSecurityConfigReplacedDuringHandshake`,
`internal/credentials/xds/handshake_info_test.go`, and
`internal/xds/balancer/clusterimpl/tests/clusterimpl_security_test.go` `TestSecurityConfigUpdate_DuringHandshake`.

Unit test: the replacement uses mismatched roots, `root2 := makeRootProvider(t, "x509/client_ca_cert.pem")` (line
751), and the follow-up asserts a distinguishable trust result (lines 787–790):

```go
if _, _, err := creds.ClientHandshake(hsCtx, authority, conn2); err == nil {
	t.Fatal("ClientHandshake() succeeded with new security configuration, want failure due to mismatched trust roots")
}
if _, ok := root1.entered.ReceiveOrFail(); ok {
	t.Fatal("Handshake requested trust roots from the old root certificate provider after it was replaced")
}
```

The e2e test, by contrast, uses the same `server_ca_cert.pem` roots for both instances and only asserts
`newProvider.rootsRequested.Receive(ctx)` (line 1062) — signal-only, like aeb669ac/71955f29.

```console
$ cd /home/ubuntu/wt/c15d4e27
$ go test -race -count=3 -v -run 'Test/ClientCredsSecurityConfigReplacedDuringHandshake$' ./credentials/xds/ 2>&1 | grep -E '^(\s*---|ok|FAIL|PASS|\s+xds_client)'
--- PASS: Test (0.09s)
    --- PASS: Test/ClientCredsSecurityConfigReplacedDuringHandshake (0.08s)
... (x3)
ok  	google.golang.org/grpc/credentials/xds	1.268s
$ bash /home/ubuntu/audit/apply_m5.sh /home/ubuntu/wt/c15d4e27
+	km, err = evalM5Stale(&evalM5Roots, km, err)
+		km, err = evalM5Stale(&evalM5Certs, km, err)
$ go test -race -count=1 -v -run 'Test/ClientCredsSecurityConfigReplacedDuringHandshake$' ./credentials/xds/ 2>&1 | grep -E '^(\s*---|ok|FAIL|PASS|\s+xds_client)'
    xds_client_test.go:788: ClientHandshake() succeeded with new security configuration, want failure due to mismatched trust roots
--- FAIL: Test (0.04s)
    --- FAIL: Test/ClientCredsSecurityConfigReplacedDuringHandshake (0.03s)
FAIL
FAIL	google.golang.org/grpc/credentials/xds	0.047s
$ go test -race -count=1 -v -run 'Test/SecurityConfigUpdate_DuringHandshake$' ./internal/xds/balancer/clusterimpl/tests/ 2>&1 | grep -E '^(\s*---|ok|FAIL|PASS|\s+clusterimpl_security_test.go:[0-9]+: [A-Z])'
--- PASS: Test (0.16s)
    --- PASS: Test/SecurityConfigUpdate_DuringHandshake (0.16s)
--- FAIL: TestEval_SecurityConfigUpdate_ActiveHandshakeKeepsProvider (0.05s)
FAIL
```

The branch's e2e test does not distinguish, but its unit test does (mismatched replacement roots → follow-up must
fail), so the branch as a whole adds causal follow-up evidence. **REFUTED.**

### C2 — evalon/grpc-go-xd-ce71ca89

Changed test: `internal/xds/balancer/clusterimpl/tests/clusterimpl_security_test.go`
`TestSecurityConfigUpdate_DuringHandshake`. Both provider instances serve the same roots (`server_ca_cert.pem`, line
845) but different identities: `oldInstance` → `client1_cert.pem`, `newInstance` → `client2_cert.pem` (line 1033). The
backend records the CN of the client certificate on the connection that served each `EmptyCall`, and the follow-up
asserts (lines 1145–1147):

```go
if got, want := backend2ClientCN(), "test-client2"; got != want {
	t.Fatalf("Client certificate presented on the connection to the new backend has CommonName %q, want %q", got, want)
}
```

This is an outcome of the follow-up attempt that can only be produced by the replacement instance's KeyMaterial
(published in the same HandshakeInfo as its root provider). The roots themselves are indistinguishable, so the linkage
is through the identity half of the replacement KeyMaterial.

```console
$ cd /home/ubuntu/wt/ce71ca89
$ go test -race -count=3 -v -run 'Test/SecurityConfigUpdate_DuringHandshake$' ./internal/xds/balancer/clusterimpl/tests/ 2>&1 | grep -E '^(\s*---|ok|FAIL|PASS)'
--- PASS: Test (0.17s)
    --- PASS: Test/SecurityConfigUpdate_DuringHandshake (0.17s)
--- PASS: TestEval_SecurityConfigUpdate_ActiveHandshakeKeepsProvider (0.05s)
... (x3)
ok  	google.golang.org/grpc/internal/xds/balancer/clusterimpl/tests	1.671s
$ bash /home/ubuntu/audit/apply_m5.sh /home/ubuntu/wt/ce71ca89
+	km, err = evalM5Stale(&evalM5Roots, km, err)
+		km, err = evalM5Stale(&evalM5Certs, km, err)
$ go test -race -count=1 -v -run 'Test/SecurityConfigUpdate_DuringHandshake$' ./internal/xds/balancer/clusterimpl/tests/ 2>&1 | grep -E '^(\s*---|ok|FAIL|PASS|\s+clusterimpl_security_test.go:[0-9]+: [A-Z])'
    clusterimpl_security_test.go:1146: Client certificate presented on the connection to the new backend has CommonName "test-client1", want "test-client2"
--- FAIL: Test (0.23s)
    --- FAIL: Test/SecurityConfigUpdate_DuringHandshake (0.23s)
--- FAIL: TestEval_SecurityConfigUpdate_ActiveHandshakeKeepsProvider (0.05s)
FAIL
FAIL	google.golang.org/grpc/internal/xds/balancer/clusterimpl/tests	0.317s
```

The test fails on the same mutant the fixture fails on, via an asserted outcome of the follow-up connection linked to
the replacement KeyMaterial. **REFUTED.**

## C3

Target: [evalon/grpc-go-xd-3ed13f94](https://github.com/kaitranntt-evals/grpc-go-xds-certificate-provider-closure-race/tree/evalon/grpc-go-xd-3ed13f94)
(commit `512bcd84`, worktree `/home/ubuntu/wt/3ed13f94`). Changed test:
`internal/xds/balancer/clusterimpl/tests/clusterimpl_security_test.go` `TestSecurityConfigUpdate_ReplacedDuringHandshake`.

Both provider instances (`initialInstance`, `updatedInstance`) are built by the same `observableCertProvider`, which
serves `"x509/server_ca_cert.pem"` as roots (line 920) and `client1_cert.pem` as identity (line 928) for every
instance. After moving the endpoint to `server2`, the follow-up assertions are (file lines 1107–1108):

```go
verifySecurityInformationFromPeer(t, peer, e2e.SecurityLevelMTLS)
awaitEvents(ctx, t, updatedEvents.rootsRequested, 1, "roots requested from updated instance")
```

`rootsRequested` is a buffered channel (`eventsBufferSize = 10`) signalled on every `KeyMaterial()` call of a root
provider of that instance; the assertion is "some root provider of the updated instance has been asked for roots at
some point", and mTLS with the same roots/identity looks identical whichever instance's material was used.

```console
$ cd /home/ubuntu/wt/3ed13f94
$ go test -race -count=3 -v -run 'Test/SecurityConfigUpdate_ReplacedDuringHandshake$' ./internal/xds/balancer/clusterimpl/tests/ 2>&1 | grep -E '^(\s*---|ok|FAIL|PASS)'
--- PASS: Test (0.16s)
    --- PASS: Test/SecurityConfigUpdate_ReplacedDuringHandshake (0.16s)
--- PASS: TestEval_SecurityConfigUpdate_ActiveHandshakeKeepsProvider (0.05s)
... (x3)
ok  	google.golang.org/grpc/internal/xds/balancer/clusterimpl/tests	1.638s
$ bash /home/ubuntu/audit/apply_m5.sh /home/ubuntu/wt/3ed13f94
+	km, err = evalM5Stale(&evalM5Roots, km, err)
+		km, err = evalM5Stale(&evalM5Certs, km, err)
$ go test -race -count=1 -v -run 'Test/SecurityConfigUpdate_ReplacedDuringHandshake$' ./internal/xds/balancer/clusterimpl/tests/ 2>&1 | grep -E '^(\s*---|ok|FAIL|PASS|\s+clusterimpl_security_test.go:[0-9]+: [A-Z])'
--- PASS: Test (0.16s)
    --- PASS: Test/SecurityConfigUpdate_ReplacedDuringHandshake (0.16s)
--- FAIL: TestEval_SecurityConfigUpdate_ActiveHandshakeKeepsProvider (0.05s)
FAIL
$ go test -race -count=1 -v -run 'TestEval_SecurityConfigUpdate_ActiveHandshakeKeepsProvider$' ./internal/xds/balancer/clusterimpl/tests/ 2>&1 | grep -E '^(\s*---|ok|FAIL|PASS|\s+eval_handshake_lifetime_test.go:[0-9]+: [A-Z])'
    eval_handshake_lifetime_test.go:357: Follow-up RPC error = <nil>, want x509 unknown authority
--- FAIL: TestEval_SecurityConfigUpdate_ActiveHandshakeKeepsProvider (0.06s)
FAIL
FAIL	google.golang.org/grpc/internal/xds/balancer/clusterimpl/tests	0.091s
```

Replayable repro (`bash verify/repro/c3_3ed13f94_follow_up_roots.sh`), key output:

```console
== under mutation M5
-- branch test: go test -race -count=1 -v -run '^Test$/SecurityConfigUpdate_ReplacedDuringHandshake$' ./internal/xds/balancer/clusterimpl/tests/
--- PASS: Test (0.16s)
    --- PASS: Test/SecurityConfigUpdate_ReplacedDuringHandshake (0.16s)
ok  	google.golang.org/grpc/internal/xds/balancer/clusterimpl/tests	1.186s
-- eval fixture: go test -race -count=1 -v -run '^TestEval_SecurityConfigUpdate_ActiveHandshakeKeepsProvider$' ./internal/xds/balancer/clusterimpl/tests/
    eval_handshake_lifetime_test.go:357: Follow-up RPC error = <nil>, want x509 unknown authority
--- FAIL: TestEval_SecurityConfigUpdate_ActiveHandshakeKeepsProvider (0.06s)
== result
WEAKNESS REPRODUCED: the branch test still PASSES when the follow-up connection is not governed by the replacement roots; the eval fixture FAILS on the same mutant.
```

Impact: in the same package, on the same mutant, the eval fixture (which gives the replacement instance untrusted roots
and requires `x509: certificate signed by unknown authority` on the follow-up RPC) fails while the branch's test passes;
the branch's follow-up evidence is a KeyMaterial-call signal with indistinguishable roots and identity, so it neither
adds a distinguishable trust result nor an outcome tied to the replacement KeyMaterial for that attempt.
**C3 CONFIRMED.**
