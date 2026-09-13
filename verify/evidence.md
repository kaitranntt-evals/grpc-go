## Setup (shared by all sections)

Workspace: `~/repos/grpc-go` (origin = `github.com/kaitranntt-evals/grpc-go`), Go `go1.25.7 linux/amd64`.

```sh
cd ~/repos/grpc-go
git fetch origin grpc-go-xds-certificate-provider-closure-race-perfect
git checkout -b verify/grpc-go-xds-certificate-provider-closure-race-v-dfae0494 origin/grpc-go-xds-certificate-provider-closure-race-perfect
# claim-target branches live in a different repository
git remote add claims https://github.com/kaitranntt-evals/grpc-go-xds-certificate-provider-closure-race.git
git fetch claims evalon/grpc-go-xd-8220779b evalon/grpc-go-xd-dcaf16f3 evalon/grpc-go-xd-f2d332f0
for b in 8220779b dcaf16f3 f2d332f0; do git worktree add ~/wt/$b claims/evalon/grpc-go-xd-$b; done
# eval fixtures (byte-exact from eval_tests.zip) provisioned into every checkout
cp tests/candidate_test_inventory.go <checkout>/.evaltools/
cp tests/eval_handshake_lifetime_test.go <checkout>/internal/xds/balancer/clusterimpl/tests/
cp tests/run_eval_xds_test_group.sh tests/run_candidate_tests.sh <checkout>/test/
```

All `go test` commands below were run with `unset GOFLAGS GOTOOLCHAIN GOWORK GOCACHE GOENV; export GOENV=off GOWORK=off` (as the eval scripts do). Tests in these packages are `grpctest` sub-tests, so they are selected with `-run 'Test/<NameWithoutTestPrefix>$'`.

## C1

Branch: `claims/evalon/grpc-go-xd-8220779b` (worktree `~/wt/8220779b`). New/changed tests concerning replacement + follow-up handshake:

- `credentials/xds/xds_client_test.go: TestClientCredsSecurityConfigReplacedDuringHandshake` — the later connection is asserted only as
  ```go
  if _, _, err := creds.ClientHandshake(ctx, authority, conn2); err == nil {
      t.Fatal("ClientHandshake() succeeded with replacement roots that do not trust the server certificate, want failure")
  }
  ```
  (any non-nil error passes; `root2` is a plain `makeRootProvider`, so no KeyMaterial observation either).
- `internal/xds/balancer/clusterimpl/security_test.go: TestSecurityConfigReplacedDuringHandshake` — opens no connection; the test itself does `hiB := hiPtr.Load(); hiB.Acquire(); hiB.ClientSideTLSConfig(ctx, "")` and checks `res.cfg.RootCAs == providers[rootInstanceB].km.Roots`. It bypasses `credsImpl.ClientHandshake`, the production path that selects the HandshakeInfo for a connection.
- `credentials/xds/xds_client_test.go: TestClientCredsRetiredHandshakeInfo`, `internal/credentials/xds/handshake_info_test.go: TestHandshakeInfoReferenceCounting / TestHandshakeInfoReleaseWithoutProviders` — no later connection at all.

Baseline (unmodified branch):

```sh
cd ~/wt/8220779b
go test ./credentials/xds -run 'Test/(ClientCredsSecurityConfigReplacedDuringHandshake|ClientCredsRetiredHandshakeInfo)$' -count=1 -v
go test ./internal/xds/balancer/clusterimpl -run 'Test/SecurityConfigReplacedDuringHandshake$' -count=1 -v
```

```console
    --- PASS: Test/ClientCredsRetiredHandshakeInfo (0.05s)
    --- PASS: Test/ClientCredsSecurityConfigReplacedDuringHandshake (0.06s)
ok  	google.golang.org/grpc/credentials/xds	0.118s
    --- PASS: Test/SecurityConfigReplacedDuringHandshake (0.00s)
ok  	google.golang.org/grpc/internal/xds/balancer/clusterimpl	0.006s
```

Mutation (`verify/repro/c1_stale_handshake_info_mutation.patch`): `credsImpl` remembers the first HandshakeInfo it selected and keeps using it for every later connection, so a later connection is never governed by the replacement roots (it fails with the generic `"xds: security configuration for this connection is no longer available"` error once the first HandshakeInfo is retired).

```sh
cd ~/wt/8220779b && git apply ~/repos/grpc-go/verify/repro/c1_stale_handshake_info_mutation.patch
go test ./credentials/xds -run 'Test/(ClientCredsSecurityConfigReplacedDuringHandshake|ClientCredsRetiredHandshakeInfo)$' -count=1 -v
go test ./internal/xds/balancer/clusterimpl -run 'Test/SecurityConfigReplacedDuringHandshake$' -count=1 -v
```

```console
    --- PASS: Test/ClientCredsRetiredHandshakeInfo (0.05s)
    --- PASS: Test/ClientCredsSecurityConfigReplacedDuringHandshake (0.06s)
ok  	google.golang.org/grpc/credentials/xds	0.113s
    --- PASS: Test/SecurityConfigReplacedDuringHandshake (0.00s)
ok  	google.golang.org/grpc/internal/xds/balancer/clusterimpl	0.007s
```

Both solution tests stay green under the mutation. Probe showing what the later connection actually experiences (`verify/repro/c1_later_connection_probe_test.go`, copied to `credentials/xds/` with the `//go:build ignore` line removed) and the eval fixture, both with the mutation applied:

```sh
go test ./credentials/xds -run 'Test/VerifyC1LaterConnectionError$' -count=1 -v
tmp=$(mktemp -d .evaltools/lifetime_XXXXXX); cp internal/xds/balancer/clusterimpl/tests/eval_handshake_lifetime_test.go "$tmp/"
go test "./$tmp" -run '^TestEval_SecurityConfigUpdate_ActiveHandshakeKeepsProvider$' -count=1 -v
```

```console
    c1_later_connection_probe_test.go:63: later connection ClientHandshake error: xds: security configuration for this connection is no longer available
    c1_later_connection_probe_test.go:65: later connection error = xds: security configuration for this connection is no longer available, want x509 unknown authority (replacement roots)
    --- FAIL: Test/VerifyC1LaterConnectionError (0.06s)
FAIL	google.golang.org/grpc/credentials/xds	0.061s
    eval_handshake_lifetime_test.go:353: timed out waiting for replacement provider KeyMaterial on the follow-up RPC: context deadline exceeded
--- FAIL: TestEval_SecurityConfigUpdate_ActiveHandshakeKeepsProvider (3.02s)
FAIL	google.golang.org/grpc/.evaltools/lifetime_ALfUvH	3.023s
```

Control, mutation reverted (`git checkout -- credentials/xds/xds.go`):

```console
    c1_later_connection_probe_test.go:63: later connection ClientHandshake error: x509: certificate signed by unknown authority
    --- PASS: Test/VerifyC1LaterConnectionError (0.06s)
ok  	google.golang.org/grpc/credentials/xds	0.067s
ok  	google.golang.org/grpc/.evaltools/lifetime_TKZue9	0.047s   # eval fixture passes on the unmodified branch
```

Impact reasoning: the only solution test that runs a later connection through the production `ClientHandshake` path accepts any error, and the only test that checks the replacement roots picks the HandshakeInfo by hand without a connection; a regression in which later connections keep using the pre-replacement configuration passes every new test on the branch. Verdict: **CONFIRMED**.

## C2

Branch: `claims/evalon/grpc-go-xd-dcaf16f3` (worktree `~/wt/dcaf16f3`). Naming note: `grpcsync.RefCounted` exists in the tree but this branch does not use it; the client snapshot (`internal/credentials/xds.HandshakeInfo`) carries its own `refs atomic.Int32` with

```go
func (hi *HandshakeInfo) Release() {
	if hi == nil { return }
	if hi.refs.Add(-1) != 0 { return }
	...close providers...
}
```

Repro `verify/repro/c2_extra_release_test.go` (copied to `internal/credentials/xds/`, `//go:build ignore` line removed): creator's final release, then one extra release, capturing grpclog/log output and panics.

```sh
cd ~/wt/dcaf16f3
sed '/^\/\/go:build ignore$/d' ~/repos/grpc-go/verify/repro/c2_extra_release_test.go > internal/credentials/xds/c2_extra_release_test.go
go test ./internal/credentials/xds -run 'Test/VerifyC2ExtraReleaseGoesNegative$' -count=1 -v
```

```console
    c2_extra_release_test.go:57: after extra Release: refs=-1 closes=1 panicked=false acquireOK=false logged=""
    c2_extra_release_test.go:59: extra Release returned normally with refs=-1 and no misuse report
    --- FAIL: Test/VerifyC2ExtraReleaseGoesNegative (0.00s)
FAIL	google.golang.org/grpc/internal/credentials/xds	0.003s
```

For comparison, the pre-existing `internal/grpcsync/refcounted.go` on the same branch does report: `if v := rc.refCount.Add(-1); v < 0 { logger.Errorf("Refcount cannot be negative") }`.

Impact reasoning: the extra release returns normally, leaves `refs == -1`, closes nothing twice (providers were already closed), logs nothing and does not panic. No production double-release path was found on this branch (the balancer releases once on replace and once on `Close`), so the observed consequence is silent loss of the misuse signal rather than a visible failure. Verdict: **CONFIRMED**.

## C3

Branch: `claims/evalon/grpc-go-xd-dcaf16f3`. Naming note: no test named `TestSecurityConfigUpdate_ConcurrentHandshake` exists on this branch; the concurrency tests are `credentials/xds: TestClientCredsProviderReplacedDuringHandshake`, `internal/credentials/xds: TestHandshakeInfoReleaseDuringRootLoad`, `TestHandshakeInfoReleaseBeforeRootLoad` and `TestAcquireHandshakeInfoRetriesOnStaleValue`.

`TestAcquireHandshakeInfoRetriesOnStaleValue` claims (name + comment) "retries with the replacement HandshakeInfo when the value it initially loaded has already been fully released". Its body stores `newHI` into `hiPtr` *before* calling `AcquireHandshakeInfo(&hiPtr)`; `oldHI` is never in the pointer, so the first `Load` returns the live replacement and no retry happens. Coverage trace (`verify/repro/c3_retry_branch_coverage.sh`):

```sh
cd ~/wt/dcaf16f3 && bash ~/repos/grpc-go/verify/repro/c3_retry_branch_coverage.sh
```

```console
    --- PASS: Test/AcquireHandshakeInfoRetriesOnStaleValue (0.00s)
ok  	google.golang.org/grpc/internal/credentials/xds	0.004s	coverage: 15.2% of statements
== coverage blocks of AcquireHandshakeInfo (last column = execution count)
194:func AcquireHandshakeInfo(hiPtr *atomic.Pointer[HandshakeInfo]) *HandshakeInfo {
google.golang.org/grpc/internal/credentials/xds/handshake_info.go:199.33,201.16 2 1
google.golang.org/grpc/internal/credentials/xds/handshake_info.go:201.16,203.4 1 1
google.golang.org/grpc/internal/credentials/xds/handshake_info.go:204.3,204.11 1 0     # line 204 = `hi = cur` (retry with replacement): never executed
google.golang.org/grpc/internal/credentials/xds/handshake_info.go:206.2,206.11 1 1
== mutate 'hi = cur' -> 'return nil' and re-run the test
    --- PASS: Test/AcquireHandshakeInfoRetriesOnStaleValue (0.00s)
ok  	google.golang.org/grpc/internal/credentials/xds	0.004s
    --- PASS: Test/ClientCredsProviderReplacedDuringHandshake (0.01s)
ok  	google.golang.org/grpc/credentials/xds	0.018s
```

The loop body at lines 199-203 is entered only by the test's second half (`cur == hi` → return nil, i.e. "released without replacement"); the retry statement is dead for this test, and replacing it with `return nil` (retry never happens) keeps the test green.

Whole-branch check — no test in any of the three affected suites executes the retry statement:

```sh
go test ./credentials/xds ./internal/credentials/xds ./internal/xds/balancer/clusterimpl/... -count=1 -coverpkg=google.golang.org/grpc/internal/credentials/xds -coverprofile=/tmp/c3_all.cov
grep -E 'handshake_info.go:204\.' /tmp/c3_all.cov
```

```console
ok  	google.golang.org/grpc/credentials/xds	0.484s	coverage: 72.1% of statements in google.golang.org/grpc/internal/credentials/xds
ok  	google.golang.org/grpc/internal/credentials/xds	0.051s	coverage: 69.7% of statements in google.golang.org/grpc/internal/credentials/xds
ok  	google.golang.org/grpc/internal/xds/balancer/clusterimpl	0.012s	coverage: 7.3% of statements in google.golang.org/grpc/internal/credentials/xds
ok  	google.golang.org/grpc/internal/xds/balancer/clusterimpl/tests	3.837s	coverage: 42.4% of statements in google.golang.org/grpc/internal/credentials/xds
google.golang.org/grpc/internal/credentials/xds/handshake_info.go:204.3,204.11 1 0
google.golang.org/grpc/internal/credentials/xds/handshake_info.go:204.3,204.11 1 0
google.golang.org/grpc/internal/credentials/xds/handshake_info.go:204.3,204.11 1 0
google.golang.org/grpc/internal/credentials/xds/handshake_info.go:204.3,204.11 1 0
```

The other concurrency test, `TestClientCredsProviderReplacedDuringHandshake`, does establish its claimed overlap: it waits on `root1.loading` (handshake inside `KeyMaterial`) before `hiPtr.Swap(hi2).Release()` and asserts `!root1.isClosed()` immediately after, which can only hold if the handshake's `Acquire` preceded the owner's `Release`; it passed in every run above. The claim needs only one mis-described test. Verdict: **CONFIRMED**.

## C4

Branch: `claims/evalon/grpc-go-xd-f2d332f0` (worktree `~/wt/f2d332f0`). Relevant production code, `credentials/xds/xds.go: ClientHandshake`:

```go
hi, err := acquireHandshakeInfo(hiPtr)   // takes a reference on success
if err != nil { return nil, nil, err }
if hi == nil || hi.UseFallbackCreds() {
    return c.fallback.ClientHandshake(ctx, authority, rawConn)   // acquired reference never released
}
defer hi.Release()
```

`clusterimpl.handleSecurityConfig` publishes `xds.NewHandshakeInfo(nil, nil, nil, false, "", false, false)` (for which `UseFallbackCreds()` is true) whenever the Cluster has no security configuration, so every handshake on such a cluster takes this path.

Repro `verify/repro/c4_fallback_leak_test.go` (package `clusterimpl`, copied to `internal/xds/balancer/clusterimpl/` with the `//go:build ignore` line removed): builds the real cluster_impl balancer with xDS-flavoured dial creds, pushes a Cluster with `SecurityCfg: nil`, reads the published `HandshakeInfo` pointer from the SubConn address attributes, runs 3 handshakes through the production `xdscreds.NewClientCredentials(...).ClientHandshake` (insecure fallback, `net.Pipe`), closes the balancer (its only legitimate reference), runs one more handshake, then counts references still outstanding.

```sh
cd ~/wt/f2d332f0
sed '/^\/\/go:build ignore$/d' ~/repos/grpc-go/verify/repro/c4_fallback_leak_test.go > internal/xds/balancer/clusterimpl/c4_fallback_leak_test.go
go test ./internal/xds/balancer/clusterimpl -run 'Test/VerifyC4FallbackHandshakeLeaksReference$' -count=1 -v
```

```console
    c4_fallback_leak_test.go:107: ClientHandshake after balancer Close: err=<nil>
    c4_fallback_leak_test.go:120: handshakes via fallback = 3, references still outstanding after balancer Close = 4
    c4_fallback_leak_test.go:122: fallback handshakes leaked 4 HandshakeInfo reference(s), want 0
    --- FAIL: Test/VerifyC4FallbackHandshakeLeaksReference (0.00s)
FAIL	google.golang.org/grpc/internal/xds/balancer/clusterimpl	0.007s
```

Part results: *Reference balancing* — each fallback handshake left exactly one reference outstanding (4 handshakes → 4 references after the owner released). *Production reachability* — the leaking path was reached from the real balancer's published HandshakeInfo through the real `ClientHandshake`; no test-only hooks were involved.

Impact reasoning: a fallback HandshakeInfo owns no certificate providers, so nothing is left unclosed; the observable consequence is that the retired snapshot never reaches zero, so after the balancer is closed a handshake still succeeds on fallback credentials (`err=<nil>` above) where the code's own comment says post-Close handshakes are meant to fail rather than fall back. Verdict: **CONFIRMED** (both parts).

## C5

Branch: audited `verify/grpc-go-xds-certificate-provider-closure-race-v-dfae0494` (= `origin/grpc-go-xds-certificate-provider-closure-race-perfect`, HEAD `3483b320`). Eval fixture from `eval_tests.zip` provisioned as the scripts expect; same steps as `run_eval_xds_test_group.sh handshake-lifetime` with `-race` added.

```sh
cd ~/repos/grpc-go
unset GOFLAGS GOTOOLCHAIN GOWORK GOCACHE GOENV; export GOENV=off GOWORK=off
go run ./.evaltools/candidate_test_inventory.go process-control cc234554fb363aea445a838b341bb8a65c8305b0; echo "process-control exit=$?"
tmp=$(mktemp -d .evaltools/lifetime_XXXXXX); cp ./internal/xds/balancer/clusterimpl/tests/eval_handshake_lifetime_test.go "$tmp/"
go test -race -json "./$tmp" -run '^TestEval_SecurityConfigUpdate_ActiveHandshakeKeepsProvider$' -count=1 > /home/ubuntu/audit/c5_race.json; echo "go test -race exit=$?"
go run ./.evaltools/candidate_test_inventory.go json-pass TestEval_SecurityConfigUpdate_ActiveHandshakeKeepsProvider < /home/ubuntu/audit/c5_race.json; echo "json-pass exit=$?"
grep -E '"Action":"(pass|fail)"' /home/ubuntu/audit/c5_race.json; grep -ci "DATA RACE" /home/ubuntu/audit/c5_race.json
```

```console
process-control exit=0
go test -race exit=0
json-pass exit=0
{"Time":"2026-09-13T21:40:44.936961195Z","Action":"pass","Package":"google.golang.org/grpc/.evaltools/lifetime_V0L1Dz","Test":"TestEval_SecurityConfigUpdate_ActiveHandshakeKeepsProvider","Elapsed":0.06}
{"Time":"2026-09-13T21:40:45.940055938Z","Action":"pass","Package":"google.golang.org/grpc/.evaltools/lifetime_V0L1Dz","Elapsed":1.086}
0
```

Repeated with `-count=10` under `-race`, plus the eval script itself and the solution's own test package under `-race`:

```sh
go test -race -json "./$tmp" -run '^TestEval_SecurityConfigUpdate_ActiveHandshakeKeepsProvider$' -count=10 > /home/ubuntu/audit/c5_race10.json; echo "exit=$?"
grep -E '"Action":"(pass|fail)"' /home/ubuntu/audit/c5_race10.json | grep -c '"Test":"TestEval'; grep -ci "DATA RACE" /home/ubuntu/audit/c5_race10.json
bash ./test/run_eval_xds_test_group.sh handshake-lifetime > /home/ubuntu/audit/c5_script.log 2>&1; echo "script exit=$?"
go test -race ./internal/xds/balancer/clusterimpl/tests -count=1 -v | grep -E '^(--- |ok|FAIL|WARNING: DATA RACE)'
```

```console
go test -race -count=10 exit=0
10          # ten "Action":"pass" events for TestEval_SecurityConfigUpdate_ActiveHandshakeKeepsProvider
0           # no race report
script exit=0
--- PASS: Test (4.10s)
--- PASS: TestSecurityConfigUpdate_ConcurrentHandshake (0.04s)
--- PASS: TestEval_SecurityConfigUpdate_ActiveHandshakeKeepsProvider (0.05s)
```

Successful completion, zero exit, no race report in 11 race-instrumented runs. Verdict: **REFUTED**.
