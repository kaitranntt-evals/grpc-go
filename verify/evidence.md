## Evidence — run v-618a6113

Workspace: `~/repos/grpc-go`, branch `verify/grpc-go-xds-certificate-provider-closure-race-v-618a6113` (from `origin/grpc-go-xds-certificate-provider-closure-race-perfect`, HEAD `4f781286`). Both claims target branches in a second repository, added as remote `claims`:

```sh
cd ~/repos/grpc-go
git remote add claims https://github.com/kaitranntt-evals/grpc-go-xds-certificate-provider-closure-race.git
git fetch claims evalon/grpc-go-xd-e2cd665a evalon/grpc-go-xd-d0190a30 cc234554fb363aea445a838b341bb8a65c8305b0
git worktree add ~/wt/c1 claims/evalon/grpc-go-xd-e2cd665a   # HEAD 32d98b59, parent cc234554 (eval base)
git worktree add ~/wt/c2 claims/evalon/grpc-go-xd-d0190a30   # HEAD 13832e25, parent cc234554 (eval base)
```

Toolchain: `go version go1.25.7 linux/amd64`.

## C1

Target branch: `claims/evalon/grpc-go-xd-e2cd665a` (worktree `~/wt/c1`). Files changed vs. base `cc234554`:

```console
$ git diff --stat cc234554fb363aea445a838b341bb8a65c8305b0 HEAD
 credentials/xds/xds.go                             |  13 +-
 internal/credentials/xds/handshake_info.go         | 109 +++++++-
 internal/credentials/xds/handshake_info_test.go    | 264 +++++++++++++++++++
 internal/xds/balancer/clusterimpl/clusterimpl.go   |  60 +++--
 .../clusterimpl/tests/clusterimpl_security_test.go | 284 +++++++++++++++++++++
```

The only new/modified test that exercises a follow-up connection after Cluster security replacement is `TestSecurityConfigUpdate_ReplacedDuringHandshake` in `internal/xds/balancer/clusterimpl/tests/clusterimpl_security_test.go` (the three new `TestHandshakeInfo_*` unit tests in `handshake_info_test.go` only cover the in-progress handshake). Its follow-up section (the Cluster is repointed at a second server that presents the *same* certificate, signed by `x509/server_ca_cert.pem`; the replacement root instance trusts `x509/client_ca_cert.pem`):

```go
testutils.AwaitState(ctx, t, cc, connectivity.TransientFailure)
select {
case <-untrustedRoot.loadStarted:
default:
	t.Fatal("New connection did not load roots from the replacement root provider")
}
if _, err := client.EmptyCall(ctx, &testpb.Empty{}); status.Code(err) != codes.Unavailable || !strings.Contains(err.Error(), "x509") {
	t.Fatalf("EmptyCall() = %v, want code %s with a certificate verification error", err, codes.Unavailable)
}
```

Note: tests in this package are `func (s) Test...` methods run through `grpctest.RunSubTests`, so the `-run` selector is `^Test$/^<NameWithoutTestPrefix>$`.

### 1. Solution test passes as delivered

```console
$ cd ~/wt/c1 && go test ./internal/xds/balancer/clusterimpl/tests/ -run '^Test$/^SecurityConfigUpdate_ReplacedDuringHandshake$' -count=3 -v 2>&1 | grep -E "^\s*(=== RUN|--- |PASS|FAIL|ok)"
=== RUN   Test/SecurityConfigUpdate_ReplacedDuringHandshake
    --- PASS: Test/SecurityConfigUpdate_ReplacedDuringHandshake (0.12s)
    --- PASS: Test/SecurityConfigUpdate_ReplacedDuringHandshake (0.12s)
    --- PASS: Test/SecurityConfigUpdate_ReplacedDuringHandshake (0.12s)
ok  	google.golang.org/grpc/internal/xds/balancer/clusterimpl/tests	0.368s
```

### 2. What the follow-up attempt observes under prior roots vs. replacement roots

Instrumentation `verify/repro/c1_followup_roots_test.go` (copied into the package as `verify_c1_followup_test.go`, `//go:build ignore` line removed) replays the solution's flow with three variants and logs the follow-up outcome:

```console
$ cd ~/wt/c1 && go test ./internal/xds/balancer/clusterimpl/tests/ -run '^Test$/^VerifyC1_' -count=1 -v 2>&1 | grep -E "^\s*(=== RUN|--- |PASS|FAIL|ok|.*OBS)"
=== RUN   Test/VerifyC1_NoReplacement_PriorRootsGovernFollowup
    verify_c1_followup_test.go:177: OBS follow-up RPC served by server2 127.0.0.1:32787
    verify_c1_followup_test.go:215: OBS follow-up RPC error under prior roots: <nil> (trustedRoot.closed=false)
=== RUN   Test/VerifyC1_ReplacementTrusted_FollowupSucceeds
    verify_c1_followup_test.go:230: OBS pre-trigger: trusted2.loadStarted closed=false
    verify_c1_followup_test.go:177: OBS follow-up RPC served by server2 127.0.0.1:45393
    verify_c1_followup_test.go:232: OBS follow-up RPC error under trusted replacement: <nil>; trusted2.loadStarted closed=true
=== RUN   Test/VerifyC1_ReplacementUntrusted_FollowupObservations
    verify_c1_followup_test.go:194: OBS pre-trigger: untrustedRoot.loadStarted closed=false, untrustedRoot.closed=false, trustedRoot.closed=true
    verify_c1_followup_test.go:199: OBS follow-up RPC error: code=Unavailable msg="rpc error: code = Unavailable desc = connection error: desc = \"transport: authentication handshake failed: x509: certificate signed by unknown authority\""
    verify_c1_followup_test.go:200: OBS post-trigger: untrustedRoot.loadStarted closed=true (was closed before trigger: false)
    --- PASS: Test/VerifyC1_NoReplacement_PriorRootsGovernFollowup (0.12s)
    --- PASS: Test/VerifyC1_ReplacementTrusted_FollowupSucceeds (0.12s)
    --- PASS: Test/VerifyC1_ReplacementUntrusted_FollowupObservations (0.12s)
ok  	google.golang.org/grpc/internal/xds/balancer/clusterimpl/tests	0.371s
```

Reading: in the success variants the RPC is only accepted once `grpc.Peer` reports the second server's address, so the follow-up really is a new connection. With the prior (trusted) roots still governing (no replacement), that new connection to the second server reaches READY and the RPC succeeds (`<nil>`); with a replacement carrying the same trusted CA it also succeeds, and `trusted2.loadStarted` flips from unset to set across the follow-up (the new handshake loaded the replacement's roots). Only the untrusted replacement yields `TransientFailure` + `Unavailable` + `x509: certificate signed by unknown authority`. So the failure the solution asserts (`x509` substring on an `Unavailable` RPC after `TransientFailure`) is a certificate trust failure that does **not** occur under the prior roots for this server certificate. The `untrustedRoot.loadStarted` signal was observed unset immediately before the endpoint trigger and set immediately after it — the solution's test does not itself assert the pre-trigger state, so the refutation rests on the trust-failure path, not on this signal.

### 3. Mutation: replacement roots never govern follow-up connections

`verify/repro/c1_mutation_replacement_not_published.diff` mutates `clusterImplBalancer.updateHandshakeInfo` so that, once a real security config is published, a replacement releases the old providers (so the solution's earlier "replaced provider closed" assertion still passes) but never publishes the replacement `HandshakeInfo`:

```console
$ cd ~/wt/c1 && patch -p1 < verify/repro/c1_mutation_replacement_not_published.diff
$ go test ./internal/xds/balancer/clusterimpl/tests/ -run '^Test$/^SecurityConfigUpdate_ReplacedDuringHandshake$' -count=1 -v 2>&1 | grep -E "^\s*(=== RUN|--- |PASS|FAIL|ok|panic|.*clusterimpl_security_test.go)"
=== RUN   Test/SecurityConfigUpdate_ReplacedDuringHandshake
    clusterimpl_security_test.go:1062: New connection did not load roots from the replacement root provider
panic: xds: HandshakeInfo released more times than it was acquired      # from balancer Close() during cleanup; mutation artifact
FAIL	google.golang.org/grpc/internal/xds/balancer/clusterimpl/tests	0.127s

$ go test ./internal/xds/balancer/clusterimpl/tests/ -run '^Test$/^VerifyC1_ReplacementUntrusted' -count=1 -v 2>&1 | grep OBS
    verify_c1_followup_test.go:176: OBS pre-trigger: untrustedRoot.loadStarted closed=false, untrustedRoot.closed=false, trustedRoot.closed=true
    verify_c1_followup_test.go:181: OBS follow-up RPC error: code=Unavailable msg="rpc error: code = Unavailable desc = connection error: desc = \"transport: authentication handshake failed: xds: security configuration for this connection has been released\""
    verify_c1_followup_test.go:182: OBS post-trigger: untrustedRoot.loadStarted closed=false (was closed before trigger: false)
$ git checkout -- internal/xds/balancer/clusterimpl/clusterimpl.go
```

Reading: when the follow-up connection is not governed by the replacement roots, the solution's follow-up assertions fail — the `loadStarted` check trips first, and the follow-up error (`security configuration ... has been released`) contains no `x509`, so the `strings.Contains(err.Error(), "x509")` assertion would also reject it. The `x509` match is therefore not a generic catch-all for handshake failures: it distinguishes a certificate trust failure from other handshake errors, and section 2 shows that trust failure cannot happen under the prior roots for this server certificate. A scenario in which the prior roots silently govern the follow-up while the test stays green is excluded by the test's own design: the solution asserts the prior provider is `closed` after the first handshake, and a closed `blockingRootProvider` returns `provider instance is closed` from `KeyMaterial`, which is not an `x509` error.

Verdict: REFUTED — the new test adds an assertion-based, distinguishable certificate trust failure (Unavailable + `x509` after TransientFailure) that is observed not to occur under the prior roots, and that rejects a non-trust handshake failure under mutation.

## C2

Target branch: `claims/evalon/grpc-go-xd-d0190a30` (worktree `~/wt/c2`). New test files vs. base `cc234554`:

```console
$ cd ~/wt/c2 && git diff --name-status cc234554fb363aea445a838b341bb8a65c8305b0 HEAD | grep _test.go
A	credentials/xds/provider_lifetime_test.go
M	credentials/xds/xds_client_test.go
A	internal/credentials/xds/handshake_info_store_test.go
A	internal/xds/balancer/clusterimpl/security_test.go
```

The predicate in `scripts/vet.sh` (line 78–80, unchanged on the branch; `not` is `! "$@"` from `scripts/common.sh`), and `.github/workflows/testing.yml` line 103 runs `./scripts/vet.sh` in the `vet` matrix job:

```sh
# - Ensure all context usages are done with timeout.
git grep -e 'context.Background()' --or -e 'context.TODO()' -- "*_test.go" | grep -v "benchmark/primitives/context_test.go" | grep -v 'context.WithTimeout(' | not grep -v 'context.WithCancel('
```

### 1. Predicate run verbatim on the delivered branch and on the base commit

`verify/repro/c2_vet_timeout_context_predicate.sh` sources `scripts/common.sh` and runs the line above, printing the matched (rejected) lines and the predicate's exit status:

```console
$ cd ~/wt/c2 && bash ~/repos/grpc-go/verify/repro/c2_vet_timeout_context_predicate.sh
internal/credentials/xds/handshake_info_store_test.go:				cfg, err := hi.ClientSideTLSConfig(context.Background(), "")
internal/xds/balancer/clusterimpl/security_test.go:			if _, err := selected.ClientSideTLSConfig(context.Background(), ""); err != nil {
predicate exit=1

$ git worktree add /tmp/base cc234554fb363aea445a838b341bb8a65c8305b0 && cd /tmp/base && bash ~/repos/grpc-go/verify/repro/c2_vet_timeout_context_predicate.sh
predicate exit=0
```

Both rejected lines are inside the provider-lifetime tests named by the claim (both files are newly added on the branch):

```console
$ grep -n "^func (s) Test" internal/credentials/xds/handshake_info_store_test.go internal/xds/balancer/clusterimpl/security_test.go
internal/credentials/xds/handshake_info_store_test.go:46:func (s) TestHandshakeInfoStoreRetainsSelectedProviders(t *testing.T) {
internal/xds/balancer/clusterimpl/security_test.go:48:func (s) TestSecurityConfigProviderLifetime(t *testing.T) {
$ grep -rn "ClientSideTLSConfig(context.Background()" --include=*_test.go .
./internal/credentials/xds/handshake_info_store_test.go:96:				cfg, err := hi.ClientSideTLSConfig(context.Background(), "")
./internal/xds/balancer/clusterimpl/security_test.go:102:			if _, err := selected.ClientSideTLSConfig(context.Background(), ""); err != nil {
```

(`credentials/xds/provider_lifetime_test.go` contains no `context.Background()`/`context.TODO()` call and is not matched.)

### 2. Full `scripts/vet.sh` aborts at that predicate

```console
$ cd ~/wt/c2 && git status --porcelain | wc -l
0
$ bash ./scripts/vet.sh 2>&1 | tail -15; echo "vet.sh exit=${PIPESTATUS[0]}"
+ git grep -e 'context.Background()' --or -e 'context.TODO()' -- '*_test.go'
+ grep -v benchmark/primitives/context_test.go
+ grep -v 'context.WithTimeout('
+ not grep -v 'context.WithCancel('
+ grep -v 'context.WithCancel('
internal/credentials/xds/handshake_info_store_test.go:				cfg, err := hi.ClientSideTLSConfig(context.Background(), "")
internal/xds/balancer/clusterimpl/security_test.go:			if _, err := selected.ClientSideTLSConfig(context.Background(), ""); err != nil {
+ cleanup
+ git reset --hard HEAD
HEAD is now at 13832e25 chore: apply eval changes
vet.sh exit=1
```

(All earlier vet.sh checks — copyright, `func Test` naming, `time.After`, `interface{}`, trailing spaces, etc. — passed before this line; the script exits at the timeout-context predicate via `set -e`.)

For contrast, plain `go vet` does not implement this check and is green on the affected packages:

```console
$ cd ~/wt/c2 && go vet ./internal/credentials/xds/ ./internal/xds/balancer/clusterimpl/ ./credentials/xds/; echo "c2 go vet exit=$?"
c2 go vet exit=0
```

Impact reasoning: the branch's own `scripts/vet.sh` — the lint gate wired into `.github/workflows/testing.yml` (`vet` matrix type) — fails on the delivered tree solely because of the two added `ClientSideTLSConfig(context.Background(), "")` calls in `TestHandshakeInfoStoreRetainsSelectedProviders` and `TestSecurityConfigProviderLifetime`; the same predicate is green on the base commit. The task's own lint guideline only names `gofmt`/`go vet`, both of which pass, so the failure is invisible unless `scripts/vet.sh` is run. Fix is mechanical: derive the context from `context.WithTimeout(context.Background(), defaultTestTimeout)` (with `defer cancel()`) in both tests.

Verdict: CONFIRMED.
