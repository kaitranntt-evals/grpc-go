# Evidence for audit run v-2c42f975

Observations only. Conclusions live in the report delivered with the session.

All commands run from the repository root (`~/repos/grpc-go`, branch `verify/grpc-go-xds-certificate-provider-closure-race-v-2c42f975`, Go from the VM toolchain, `GOENV=off GOWORK=off`). `FIXTURES_DIR` is the `tests/` directory extracted from `eval_tests.zip` (here `~/eval/tests`). `verify/repro/setup_worktree.sh <id>` fetches `evalon/grpc-go-xd-<id>` from the claims repository into the detached worktree `~/wt/<id>` and copies the archived fixtures byte-exact to the paths assessment provisions them at (`.evaltools/candidate_test_inventory.go`, `internal/xds/balancer/clusterimpl/tests/eval_handshake_lifetime_test.go`, `test/run_eval_xds_test_group.sh`, `test/run_candidate_tests.sh`). The probes are evidence-only `fmt.Fprintf(os.Stderr, "VERIFY-PROBE ...")` lines patched into the worktree by `verify/instrument*.py` for the duration of one `go test` run and reverted with `git checkout` afterwards (each probe script has an `EXIT` trap); they do not change control flow unless a mutant flag is given.

Probe vocabulary: `load#N client root KeyMaterial START/END ... err=` is one call of the client validation-root load in `internal/credentials/xds/handshake_info.go` (the `KeyMaterial` call on the root provider of the `HandshakeInfo` the handshake selected) and its result; `balancer CLOSES replaced cached root provider` is the `Close()` of the replaced root provider in `internal/xds/balancer/clusterimpl/clusterimpl.go`; `ClientHandshake#N START/END` is one `credsImpl.ClientHandshake` call in `credentials/xds/xds.go`, i.e. one connection attempt; `store ...` lines are handle acquire/release events of the certificate-provider store in `credentials/tls/certprovider/store.go`.

Mutants (evidence only, never committed to production paths): `--defer-close` makes the balancer close a replaced root provider only after every validation-root load already running on it has returned (a reference model of "the selected load is preserved, the provider is cleaned up after its final dependent load"); `--no-close` makes the balancer never close a replaced root provider.

Archived fixture bytes used (first 16 hex digits of sha256):

```console
9a4211a5ca1ca5d0  candidate_test_inventory.go
7d6ba68539e56417  candidate_test_inventory_test.go
35adf5b1fcd50888  candidate_test_json_test.go
ad7ee39e67101a32  eval_handshake_lifetime_test.go
d521c3c4a3a8df9e  run_candidate_tests.sh
0a77a4e941f6b4e3  run_eval_xds_test_group.sh
```

Positive control for the archived fixture on the audited implementation branch (the base of this verify branch, commit `4f781286`): it passes 3 of 3 runs, so a failure on a claim branch is a property of that branch.

```console
$ cp ~/eval/tests/eval_handshake_lifetime_test.go internal/xds/balancer/clusterimpl/tests/
$ GOENV=off GOWORK=off go test ./internal/xds/balancer/clusterimpl/tests -run '^TestEval_SecurityConfigUpdate_ActiveHandshakeKeepsProvider$' -count=3 -v
--- PASS: TestEval_SecurityConfigUpdate_ActiveHandshakeKeepsProvider (0.04s)
--- PASS: TestEval_SecurityConfigUpdate_ActiveHandshakeKeepsProvider (0.04s)
--- PASS: TestEval_SecurityConfigUpdate_ActiveHandshakeKeepsProvider (0.04s)
PASS
ok  	google.golang.org/grpc/internal/xds/balancer/clusterimpl/tests	0.120s
$ rm internal/xds/balancer/clusterimpl/tests/eval_handshake_lifetime_test.go
```

What the archived fixture measures (`eval_handshake_lifetime_test.go`, `TestEval_SecurityConfigUpdate_ActiveHandshakeKeepsProvider`): an RPC's TLS handshake is blocked inside `KeyMaterial` of root provider A (plugin A); the Cluster security config is replaced by root provider B (a different plugin, hence a different store cache entry; B's roots do not validate the server); after the replacement is applied the fixture releases A's load and requires the active RPC to succeed (line 314), then requires A to be closed after the handshake finished (line 319). A's `KeyMaterial` returns `provider instance is closed` if A was closed while the load was blocked.

## C1

Claim: the new or changed Go tests contain no independently complete regression test that overlaps client validation-root loading with applied provider replacement (or focused retirement) and rejects premature invalidation of the selected load, rather than accepting eventual success through replacement-provider retries. 27 branches, adjudicated separately.

Setup and vocabulary (repeated here so this section stands alone): run from the repository root with `FIXTURES_DIR` pointing at the extracted `eval_tests.zip` `tests/` directory. `load#N client root KeyMaterial START/END ... err=` is one client validation-root load and its result; `balancer CLOSES replaced cached root provider` is the balancer closing the replaced root provider. `--defer-close` mutant: the balancer closes a replaced root provider only after the loads already running on it returned. `--no-close` mutant: the balancer never closes it. New/changed tests are enumerated with the archived inventory helper exactly as assessment does (`go run .evaltools/candidate_test_inventory.go cc234554fb363aea445a838b341bb8a65c8305b0 <changed *_test.go files>`), then each is run on its own.

### Command

```sh
$ FIXTURES_DIR=~/eval/tests verify/repro/c1_tests_accept_premature_invalidation.sh 95f90b92 9b0dc3f1 9094ac3e 8954d818 59c38840 83aab628 bced1091 5175e7e6 bfbe6be7 1d67b6b4 0ae111d1 1fc74c6c dbc087a7 64b6ec0e d553852a f521f13d b7a066a6 b7e8d61f d567dc9f 14b9e8f0 5069188f 437f5f4a c944dcde b2e2c879 7089d1aa 1c65da27 fee506e4
```

For each branch this runs `verify/probe_branch.sh <wt>` (tests as written), `verify/probe_branch.sh <wt> --no-close`, `verify/probe_branch.sh <wt> --defer-close`, `verify/probe_fixture.sh <wt>` and `verify/probe_fixture.sh <wt> --defer-close`, and prints one line per new/changed test. Column meaning: `as-written` = test verdict on the unmodified branch; `loads` / `failed-loads [err]` = validation-root loads performed during that test and how many returned an error; `balancer-close-during-load` = the balancer closed the replaced root provider between a load's START and END; `no-close-mutant` / `defer-close-mutant` = test verdict against the mutant.

### Output (complete, 27 branches, 74 new/changed tests)

```console
== 95f90b92
  xds_client_test.go::Test/TestClientCredsProviderReplacedDuringHandshake: as-written=PASS loads=2 failed-loads=1 [provider instance is closed] balancer-close-during-load=0 | no-close-mutant=PASS | defer-close-mutant=PASS
  hidden fixture as written : --- FAIL: TestEval_SecurityConfigUpdate_ActiveHandshakeKeepsProvider (0.03s) | selected load: END   err=provider instance is closed
  hidden fixture, defer-close: --- PASS: TestEval_SecurityConfigUpdate_ActiveHandshakeKeepsProvider (0.05s)
== 9b0dc3f1
  xds_client_test.go::Test/TestClientCredsProviderReplacedDuringHandshake: as-written=PASS loads=2 failed-loads=1 [provider instance is closed] balancer-close-during-load=0 | no-close-mutant=PASS | defer-close-mutant=PASS
  hidden fixture as written : --- FAIL: TestEval_SecurityConfigUpdate_ActiveHandshakeKeepsProvider (0.02s) | selected load: END   err=provider instance is closed
  hidden fixture, defer-close: --- PASS: TestEval_SecurityConfigUpdate_ActiveHandshakeKeepsProvider (0.04s)
== 9094ac3e
  xds_client_test.go::Test/TestClientCredsProviderReplacedDuringHandshake: as-written=PASS loads=2 failed-loads=1 [provider instance is closed] balancer-close-during-load=0 | no-close-mutant=PASS | defer-close-mutant=PASS
  hidden fixture as written : --- FAIL: TestEval_SecurityConfigUpdate_ActiveHandshakeKeepsProvider (0.02s) | selected load: END   err=provider instance is closed
  hidden fixture, defer-close: --- PASS: TestEval_SecurityConfigUpdate_ActiveHandshakeKeepsProvider (0.04s)
== 8954d818
  xds_client_test.go::Test/TestClientCredsProviderReplacedDuringHandshake: as-written=PASS loads=2 failed-loads=1 [provider instance is closed] balancer-close-during-load=0 | no-close-mutant=PASS | defer-close-mutant=PASS
  hidden fixture as written : --- FAIL: TestEval_SecurityConfigUpdate_ActiveHandshakeKeepsProvider (0.02s) | selected load: END   err=provider instance is closed
  hidden fixture, defer-close: --- PASS: TestEval_SecurityConfigUpdate_ActiveHandshakeKeepsProvider (0.04s)
== 59c38840
  xds_client_test.go::Test/TestClientCredsProviderReplacedDuringHandshake: as-written=PASS loads=2 failed-loads=1 [provider instance is closed] balancer-close-during-load=0 | no-close-mutant=PASS | defer-close-mutant=PASS
  hidden fixture as written : --- FAIL: TestEval_SecurityConfigUpdate_ActiveHandshakeKeepsProvider (0.02s) | selected load: END   err=provider instance is closed
  hidden fixture, defer-close: --- PASS: TestEval_SecurityConfigUpdate_ActiveHandshakeKeepsProvider (0.04s)
== 83aab628
  xds_client_test.go::Test/TestClientCredsProviderReplacedDuringHandshake: as-written=PASS loads=2 failed-loads=1 [provider closed] balancer-close-during-load=0 | no-close-mutant=PASS | defer-close-mutant=PASS
  hidden fixture as written : --- FAIL: TestEval_SecurityConfigUpdate_ActiveHandshakeKeepsProvider (0.02s) | selected load: END   err=provider instance is closed
  hidden fixture, defer-close: --- PASS: TestEval_SecurityConfigUpdate_ActiveHandshakeKeepsProvider (0.05s)
== bced1091
  xds_client_test.go::Test/TestClientCredsProviderReplacedDuringHandshake: as-written=PASS loads=2 failed-loads=1 [provider closed] balancer-close-during-load=0 | no-close-mutant=PASS | defer-close-mutant=PASS
  hidden fixture as written : --- FAIL: TestEval_SecurityConfigUpdate_ActiveHandshakeKeepsProvider (0.02s) | selected load: END   err=provider instance is closed
  hidden fixture, defer-close: --- PASS: TestEval_SecurityConfigUpdate_ActiveHandshakeKeepsProvider (0.04s)
== 5175e7e6
  xds_client_test.go::Test/TestClientCredsProviderReplacedDuringHandshake: as-written=PASS loads=2 failed-loads=1 [provider instance is closed] balancer-close-during-load=0 | no-close-mutant=PASS | defer-close-mutant=PASS
  hidden fixture as written : --- FAIL: TestEval_SecurityConfigUpdate_ActiveHandshakeKeepsProvider (0.02s) | selected load: END   err=provider instance is closed
  hidden fixture, defer-close: --- PASS: TestEval_SecurityConfigUpdate_ActiveHandshakeKeepsProvider (0.05s)
== bfbe6be7
  xds_client_test.go::Test/TestClientCredsProviderReplacedDuringHandshake: as-written=PASS loads=2 failed-loads=1 [provider instance is closed] balancer-close-during-load=0 | no-close-mutant=PASS | defer-close-mutant=PASS
  xds_client_test.go::Test/TestClientCredsProviderClosedWithoutReplacement: as-written=PASS loads=1 failed-loads=1 [provider instance is closed] balancer-close-during-load=0 | no-close-mutant=PASS | defer-close-mutant=PASS
  hidden fixture as written : --- FAIL: TestEval_SecurityConfigUpdate_ActiveHandshakeKeepsProvider (0.02s) | selected load: END   err=provider instance is closed
  hidden fixture, defer-close: --- PASS: TestEval_SecurityConfigUpdate_ActiveHandshakeKeepsProvider (0.06s)
== 1d67b6b4
  xds_client_test.go::Test/TestClientCredsProviderReplacedDuringHandshake: as-written=PASS loads=2 failed-loads=1 [provider instance is closed] balancer-close-during-load=0 | no-close-mutant=PASS | defer-close-mutant=PASS
  hidden fixture as written : --- FAIL: TestEval_SecurityConfigUpdate_ActiveHandshakeKeepsProvider (0.02s) | selected load: END   err=provider instance is closed
  hidden fixture, defer-close: --- PASS: TestEval_SecurityConfigUpdate_ActiveHandshakeKeepsProvider (0.05s)
== 0ae111d1
  xds_client_test.go::Test/TestClientCredsProviderClosedDuringHandshake: as-written=PASS loads=1 failed-loads=1 [provider instance is closed] balancer-close-during-load=0 | no-close-mutant=PASS | defer-close-mutant=PASS
  xds_client_test.go::Test/TestClientCredsProviderReplacedDuringHandshake: as-written=PASS loads=2 failed-loads=1 [provider instance is closed] balancer-close-during-load=0 | no-close-mutant=PASS | defer-close-mutant=PASS
  clusterimpl_security_test.go::Test/TestSecurityConfigUpdate_DuringHandshake: as-written=PASS loads=2 failed-loads=1 [provider instance is closed] balancer-close-during-load=1 | no-close-mutant=FAIL | defer-close-mutant=FAIL
  clusterimpl_security_test.go::Test/TestSecurityConfigUpdate_GoodToFallback: as-written=PASS loads=1 failed-loads=0 balancer-close-during-load=0 | no-close-mutant=FAIL | defer-close-mutant=PASS
  clusterimpl_security_test.go::Test/TestSystemRootCertsSecurityConfig: as-written=PASS loads=1 failed-loads=0 balancer-close-during-load=0 | no-close-mutant=PASS | defer-close-mutant=PASS
  clusterimpl_security_test.go::Test/TestAggregateClusterSecurityConfig: as-written=PASS loads=1 failed-loads=0 balancer-close-during-load=0 | no-close-mutant=FAIL | defer-close-mutant=PASS
  clusterimpl_security_test.go::Test/TestSecurityConfigWithoutXDSCreds: as-written=PASS loads=0 failed-loads=0 balancer-close-during-load=0 | no-close-mutant=PASS | defer-close-mutant=PASS
  clusterimpl_security_test.go::Test/TestSecurityConfigUpdate_BadToGood: as-written=PASS loads=1 failed-loads=0 balancer-close-during-load=0 | no-close-mutant=FAIL | defer-close-mutant=PASS
  clusterimpl_security_test.go::Test/TestSecurityConfigUpdate_GoodToBad: as-written=PASS loads=1 failed-loads=0 balancer-close-during-load=0 | no-close-mutant=FAIL | defer-close-mutant=PASS
  clusterimpl_security_test.go::Test/TestNoSecurityConfigWithXDSCreds: as-written=PASS loads=0 failed-loads=0 balancer-close-during-load=0 | no-close-mutant=PASS | defer-close-mutant=PASS
  clusterimpl_security_test.go::Test/TestGoodSecurityConfig: as-written=PASS loads=1 failed-loads=0 balancer-close-during-load=0 | no-close-mutant=FAIL | defer-close-mutant=PASS
  clusterimpl_security_test.go::Test/TestSecurityConfigNotFoundInBootstrap: as-written=PASS loads=0 failed-loads=0 balancer-close-during-load=0 | no-close-mutant=PASS | defer-close-mutant=PASS
  clusterimpl_security_test.go::Test/TestCertproviderStoreError: as-written=PASS loads=0 failed-loads=0 balancer-close-during-load=0 | no-close-mutant=PASS | defer-close-mutant=PASS
  hidden fixture as written : --- FAIL: TestEval_SecurityConfigUpdate_ActiveHandshakeKeepsProvider (0.02s) | selected load: END   err=provider instance is closed
  hidden fixture, defer-close: --- PASS: TestEval_SecurityConfigUpdate_ActiveHandshakeKeepsProvider (0.04s)
== 1fc74c6c
  xds_client_test.go::Test/TestClientCredsProviderFailureWithoutReplacement: as-written=PASS loads=1 failed-loads=1 [provider instance is closed] balancer-close-during-load=0 | no-close-mutant=PASS | defer-close-mutant=PASS
  xds_client_test.go::Test/TestClientCredsProviderReplacedDuringHandshake: as-written=PASS loads=4 failed-loads=1 [provider instance is closed] balancer-close-during-load=0 | no-close-mutant=PASS | defer-close-mutant=PASS
  security_test.go::Test/TestSecurityConfigUpdate_HandshakeInfoUpdatedBeforeProvidersClosed: as-written=PASS loads=6 failed-loads=2 [provider "root-instance-2" is closed] balancer-close-during-load=0 | no-close-mutant=FAIL | defer-close-mutant=PASS
  hidden fixture as written : --- FAIL: TestEval_SecurityConfigUpdate_ActiveHandshakeKeepsProvider (0.04s) | selected load: END   err=provider instance is closed
  hidden fixture, defer-close: --- PASS: TestEval_SecurityConfigUpdate_ActiveHandshakeKeepsProvider (0.05s)
== dbc087a7
  xds_client_test.go::Test/TestClientCredsProviderReplacedDuringHandshake: as-written=PASS loads=4 failed-loads=1 [provider instance is closed] balancer-close-during-load=0 | no-close-mutant=PASS | defer-close-mutant=PASS
  hidden fixture as written : --- FAIL: TestEval_SecurityConfigUpdate_ActiveHandshakeKeepsProvider (0.03s) | selected load: END   err=provider instance is closed
  hidden fixture, defer-close: --- PASS: TestEval_SecurityConfigUpdate_ActiveHandshakeKeepsProvider (0.06s)
== 64b6ec0e
  xds_client_test.go::Test/TestClientCredsProviderClosedDuringHandshake: as-written=PASS loads=1 failed-loads=1 [provider instance is closed] balancer-close-during-load=0 | no-close-mutant=PASS | defer-close-mutant=PASS
  xds_client_test.go::Test/TestClientCredsProviderReplacedDuringHandshake: as-written=PASS loads=2 failed-loads=1 [provider instance is closed] balancer-close-during-load=0 | no-close-mutant=PASS | defer-close-mutant=PASS
  balancer_test.go::Test/TestSecurityConfigUpdate_ProvidersReplacedOnlyOnChange: as-written=PASS loads=0 failed-loads=0 balancer-close-during-load=0 | no-close-mutant=FAIL | defer-close-mutant=PASS
  hidden fixture as written : --- FAIL: TestEval_SecurityConfigUpdate_ActiveHandshakeKeepsProvider (0.06s) | selected load: END   err=provider instance is closed
  hidden fixture, defer-close: --- PASS: TestEval_SecurityConfigUpdate_ActiveHandshakeKeepsProvider (0.05s)
== d553852a
  xds_client_test.go::Test/TestClientCredsProviderClosedDuringHandshake: as-written=PASS loads=1 failed-loads=1 [provider instance is closed] balancer-close-during-load=0 | no-close-mutant=PASS | defer-close-mutant=PASS
  xds_client_test.go::Test/TestClientCredsProviderReplacedDuringHandshake: as-written=PASS loads=2 failed-loads=1 [provider instance is closed] balancer-close-during-load=0 | no-close-mutant=PASS | defer-close-mutant=PASS
  balancer_test.go::Test/TestSecurityConfigUpdate_ProviderLifecycle: as-written=PASS loads=0 failed-loads=0 balancer-close-during-load=0 | no-close-mutant=FAIL | defer-close-mutant=PASS
  clusterimpl_security_test.go::Test/TestSecurityConfigUpdate_ProvidersReplacedDuringHandshake: as-written=PASS loads=2 failed-loads=1 [provider instance is closed] balancer-close-during-load=1 | no-close-mutant=FAIL | defer-close-mutant=FAIL
  clusterimpl_security_test.go::Test/TestSecurityConfigUpdate_GoodToFallback: as-written=PASS loads=1 failed-loads=0 balancer-close-during-load=0 | no-close-mutant=FAIL | defer-close-mutant=PASS
  clusterimpl_security_test.go::Test/TestSecurityConfigWithoutXDSCreds: as-written=PASS loads=0 failed-loads=0 balancer-close-during-load=0 | no-close-mutant=PASS | defer-close-mutant=PASS
  clusterimpl_security_test.go::Test/TestCertproviderStoreError: as-written=PASS loads=0 failed-loads=0 balancer-close-during-load=0 | no-close-mutant=PASS | defer-close-mutant=PASS
  clusterimpl_security_test.go::Test/TestSecurityConfigUpdate_GoodToBad: as-written=PASS loads=1 failed-loads=0 balancer-close-during-load=0 | no-close-mutant=FAIL | defer-close-mutant=PASS
  clusterimpl_security_test.go::Test/TestSystemRootCertsSecurityConfig: as-written=PASS loads=1 failed-loads=0 balancer-close-during-load=0 | no-close-mutant=PASS | defer-close-mutant=PASS
  clusterimpl_security_test.go::Test/TestAggregateClusterSecurityConfig: as-written=PASS loads=1 failed-loads=0 balancer-close-during-load=0 | no-close-mutant=FAIL | defer-close-mutant=PASS
  clusterimpl_security_test.go::Test/TestNoSecurityConfigWithXDSCreds: as-written=PASS loads=0 failed-loads=0 balancer-close-during-load=0 | no-close-mutant=PASS | defer-close-mutant=PASS
  clusterimpl_security_test.go::Test/TestSecurityConfigNotFoundInBootstrap: as-written=PASS loads=0 failed-loads=0 balancer-close-during-load=0 | no-close-mutant=PASS | defer-close-mutant=PASS
  clusterimpl_security_test.go::Test/TestGoodSecurityConfig: as-written=PASS loads=1 failed-loads=0 balancer-close-during-load=0 | no-close-mutant=FAIL | defer-close-mutant=PASS
  clusterimpl_security_test.go::Test/TestSecurityConfigUpdate_BadToGood: as-written=PASS loads=1 failed-loads=0 balancer-close-during-load=0 | no-close-mutant=FAIL | defer-close-mutant=PASS
  hidden fixture as written : --- FAIL: TestEval_SecurityConfigUpdate_ActiveHandshakeKeepsProvider (0.01s) | selected load: END   err=provider instance is closed
  hidden fixture, defer-close: --- PASS: TestEval_SecurityConfigUpdate_ActiveHandshakeKeepsProvider (0.04s)
== f521f13d
  xds_client_test.go::Test/TestClientCredsProviderClosedDuringHandshake: as-written=PASS loads=1 failed-loads=1 [provider instance is closed] balancer-close-during-load=0 | no-close-mutant=PASS | defer-close-mutant=PASS
  xds_client_test.go::Test/TestClientCredsProviderReplacedDuringHandshake: as-written=PASS loads=2 failed-loads=1 [provider instance is closed] balancer-close-during-load=0 | no-close-mutant=PASS | defer-close-mutant=PASS
  security_test.go::Test/TestHandleSecurityConfig_ProviderLifecycle: as-written=PASS loads=0 failed-loads=0 balancer-close-during-load=0 | no-close-mutant=FAIL | defer-close-mutant=PASS
  clusterimpl_security_provider_test.go::Test/TestSecurityConfigUpdate_ProvidersReplacedDuringHandshake: as-written=PASS loads=2 failed-loads=1 [provider instance is closed] balancer-close-during-load=1 | no-close-mutant=FAIL | defer-close-mutant=FAIL
  hidden fixture as written : --- FAIL: TestEval_SecurityConfigUpdate_ActiveHandshakeKeepsProvider (0.02s) | selected load: END   err=provider instance is closed
  hidden fixture, defer-close: --- PASS: TestEval_SecurityConfigUpdate_ActiveHandshakeKeepsProvider (0.05s)
== b7a066a6
  xds_client_test.go::Test/TestClientCredsProviderReplacementDuringHandshake: as-written=PASS loads=2 failed-loads=1 [provider instance is closed] balancer-close-during-load=0 | no-close-mutant=PASS | defer-close-mutant=PASS
  clusterimpl_security_test.go::Test/TestSecurityConfigUpdate_ProviderReplacementDuringHandshake: as-written=PASS loads=2 failed-loads=1 [provider instance is closed] balancer-close-during-load=1 | no-close-mutant=FAIL | defer-close-mutant=FAIL
  clusterimpl_security_test.go::Test/TestSecurityConfigWithoutXDSCreds: as-written=PASS loads=0 failed-loads=0 balancer-close-during-load=0 | no-close-mutant=PASS | defer-close-mutant=PASS
  clusterimpl_security_test.go::Test/TestCertproviderStoreError: as-written=PASS loads=0 failed-loads=0 balancer-close-during-load=0 | no-close-mutant=PASS | defer-close-mutant=PASS
  clusterimpl_security_test.go::Test/TestGoodSecurityConfig: as-written=PASS loads=1 failed-loads=0 balancer-close-during-load=0 | no-close-mutant=FAIL | defer-close-mutant=PASS
  clusterimpl_security_test.go::Test/TestSecurityConfigUpdate_GoodToFallback: as-written=PASS loads=1 failed-loads=0 balancer-close-during-load=0 | no-close-mutant=FAIL | defer-close-mutant=PASS
  clusterimpl_security_test.go::Test/TestSecurityConfigUpdate_GoodToBad: as-written=PASS loads=1 failed-loads=0 balancer-close-during-load=0 | no-close-mutant=FAIL | defer-close-mutant=PASS
  clusterimpl_security_test.go::Test/TestAggregateClusterSecurityConfig: as-written=PASS loads=1 failed-loads=0 balancer-close-during-load=0 | no-close-mutant=FAIL | defer-close-mutant=PASS
  clusterimpl_security_test.go::Test/TestNoSecurityConfigWithXDSCreds: as-written=PASS loads=0 failed-loads=0 balancer-close-during-load=0 | no-close-mutant=PASS | defer-close-mutant=PASS
  clusterimpl_security_test.go::Test/TestSecurityConfigNotFoundInBootstrap: as-written=PASS loads=0 failed-loads=0 balancer-close-during-load=0 | no-close-mutant=PASS | defer-close-mutant=PASS
  clusterimpl_security_test.go::Test/TestSecurityConfigUpdate_BadToGood: as-written=PASS loads=1 failed-loads=0 balancer-close-during-load=0 | no-close-mutant=FAIL | defer-close-mutant=PASS
  clusterimpl_security_test.go::Test/TestSystemRootCertsSecurityConfig: as-written=PASS loads=1 failed-loads=0 balancer-close-during-load=0 | no-close-mutant=PASS | defer-close-mutant=PASS
  hidden fixture as written : --- FAIL: TestEval_SecurityConfigUpdate_ActiveHandshakeKeepsProvider (0.01s) | selected load: END   err=provider instance is closed
  hidden fixture, defer-close: --- PASS: TestEval_SecurityConfigUpdate_ActiveHandshakeKeepsProvider (0.04s)
== b7e8d61f
  xds_client_test.go::Test/TestClientCredsProviderSwitchDuringHandshake: as-written=PASS loads=5 failed-loads=2 [provider instance is closed] balancer-close-during-load=0 | no-close-mutant=PASS | defer-close-mutant=PASS
  balancer_test.go::Test/TestSecurityConfigUpdateDuringHandshake: as-written=PASS loads=2 failed-loads=1 [provider instance is closed] balancer-close-during-load=1 | no-close-mutant=FAIL | defer-close-mutant=FAIL
  hidden fixture as written : --- FAIL: TestEval_SecurityConfigUpdate_ActiveHandshakeKeepsProvider (0.01s) | selected load: END   err=provider instance is closed
  hidden fixture, defer-close: --- PASS: TestEval_SecurityConfigUpdate_ActiveHandshakeKeepsProvider (0.04s)
== d567dc9f
  balancer_test.go::Test/TestSecurityConfigPublishesBeforeClosingProviders: as-written=PASS loads=2 failed-loads=0 balancer-close-during-load=0 | no-close-mutant=FAIL | defer-close-mutant=PASS
  xds_client_test.go::Test/TestClientCredsProviderSwitchDuringHandshake: as-written=PASS loads=6 failed-loads=3 [provider instance is closed] balancer-close-during-load=0 | no-close-mutant=PASS | defer-close-mutant=PASS
  hidden fixture as written : --- FAIL: TestEval_SecurityConfigUpdate_ActiveHandshakeKeepsProvider (0.02s) | selected load: END   err=provider instance is closed
  hidden fixture, defer-close: --- PASS: TestEval_SecurityConfigUpdate_ActiveHandshakeKeepsProvider (0.05s)
== 14b9e8f0
  security_test.go::Test/TestClientHandshakeDuringProviderUpdate: as-written=PASS loads=4 failed-loads=1 [provider instance is closed] balancer-close-during-load=1 | no-close-mutant=FAIL | defer-close-mutant=FAIL
  hidden fixture as written : --- FAIL: TestEval_SecurityConfigUpdate_ActiveHandshakeKeepsProvider (0.02s) | selected load: END   err=provider instance is closed
  hidden fixture, defer-close: --- PASS: TestEval_SecurityConfigUpdate_ActiveHandshakeKeepsProvider (0.05s)
== 5069188f
  security_test.go::Test/TestSecurityConfigUpdateDuringHandshake: as-written=PASS loads=4 failed-loads=1 [provider instance is closed] balancer-close-during-load=1 | no-close-mutant=FAIL | defer-close-mutant=FAIL
  hidden fixture as written : --- FAIL: TestEval_SecurityConfigUpdate_ActiveHandshakeKeepsProvider (0.02s) | selected load: END   err=provider instance is closed
  hidden fixture, defer-close: --- PASS: TestEval_SecurityConfigUpdate_ActiveHandshakeKeepsProvider (0.04s)
== 437f5f4a
  security_test.go::Test/TestSecurityConfigUpdateDuringHandshake: as-written=PASS loads=4 failed-loads=1 [provider instance is closed] balancer-close-during-load=1 | no-close-mutant=FAIL | defer-close-mutant=FAIL
  hidden fixture as written : --- FAIL: TestEval_SecurityConfigUpdate_ActiveHandshakeKeepsProvider (0.02s) | selected load: END   err=provider instance is closed
  hidden fixture, defer-close: --- PASS: TestEval_SecurityConfigUpdate_ActiveHandshakeKeepsProvider (0.04s)
== c944dcde
  security_test.go::Test/TestSecurityConfigUpdateDuringHandshake: as-written=PASS loads=4 failed-loads=1 [provider instance is closed] balancer-close-during-load=1 | no-close-mutant=FAIL | defer-close-mutant=FAIL
  hidden fixture as written : --- FAIL: TestEval_SecurityConfigUpdate_ActiveHandshakeKeepsProvider (0.01s) | selected load: END   err=provider instance is closed
  hidden fixture, defer-close: --- PASS: TestEval_SecurityConfigUpdate_ActiveHandshakeKeepsProvider (0.04s)
== b2e2c879
  security_test.go::Test/TestSecurityConfigUpdateDuringHandshake: as-written=PASS loads=4 failed-loads=1 [provider instance is closed] balancer-close-during-load=1 | no-close-mutant=FAIL | defer-close-mutant=FAIL
  hidden fixture as written : --- FAIL: TestEval_SecurityConfigUpdate_ActiveHandshakeKeepsProvider (0.02s) | selected load: END   err=provider instance is closed
  hidden fixture, defer-close: --- PASS: TestEval_SecurityConfigUpdate_ActiveHandshakeKeepsProvider (0.04s)
== 7089d1aa
  xds_client_test.go::Test/TestClientCredsProviderSwitchDuringHandshake: as-written=PASS loads=7 failed-loads=4 [provider instance is closed] balancer-close-during-load=0 | no-close-mutant=PASS | defer-close-mutant=PASS
  hidden fixture as written : --- FAIL: TestEval_SecurityConfigUpdate_ActiveHandshakeKeepsProvider (0.02s) | selected load: END   err=provider instance is closed
  hidden fixture, defer-close: --- PASS: TestEval_SecurityConfigUpdate_ActiveHandshakeKeepsProvider (0.04s)
== 1c65da27
  balancer_test.go::Test/TestSecurityConfigProviderReplacement: as-written=PASS loads=1 failed-loads=0 balancer-close-during-load=0 | no-close-mutant=FAIL | defer-close-mutant=PASS
  xds_client_test.go::Test/TestClientCredsProviderSwitchDuringHandshake: as-written=PASS loads=5 failed-loads=2 [provider instance is closed] balancer-close-during-load=0 | no-close-mutant=PASS | defer-close-mutant=PASS
  hidden fixture as written : --- FAIL: TestEval_SecurityConfigUpdate_ActiveHandshakeKeepsProvider (0.01s) | selected load: END   err=provider instance is closed
  hidden fixture, defer-close: --- PASS: TestEval_SecurityConfigUpdate_ActiveHandshakeKeepsProvider (0.04s)
== fee506e4
  security_test.go::Test/TestSecurityConfigUpdateDuringHandshake: as-written=PASS loads=4 failed-loads=1 [provider instance is closed] balancer-close-during-load=1 | no-close-mutant=FAIL | defer-close-mutant=FAIL
  hidden fixture as written : --- FAIL: TestEval_SecurityConfigUpdate_ActiveHandshakeKeepsProvider (0.02s) | selected load: END   err=provider instance is closed
  hidden fixture, defer-close: --- PASS: TestEval_SecurityConfigUpdate_ActiveHandshakeKeepsProvider (0.04s)
```

### Scan of every load in those 74 test runs

```console
$ python3 verify/scan_overlaps.py ~/eval/logs/95f90b92/probe.log ~/eval/logs/9b0dc3f1/probe.log ... (all 27 probe.log files)
validation-root loads observed: 128
loads that overlapped a balancer/store event or ended with an error: 47
  of which ended err=<nil>: 0
load results by root-provider type:
    4  *certprovider.singleCloseWrappedProvider  error
   22  *certprovider.singleCloseWrappedProvider  ok
    1  *clusterimpl.blockingCertProvider  error
    1  *clusterimpl.callbackCertificateProvider  error
    1  *clusterimpl.callbackCertificateProvider  ok
    3  *clusterimpl.closeCallbackProvider  ok
    2  *clusterimpl.fakeCertProvider  error
    4  *clusterimpl.fakeCertProvider  ok
    1  *clusterimpl.handshakeProvider  error
    3  *clusterimpl.handshakeProvider  ok
    2  *clusterimpl.handshakeTestProvider  error
    6  *clusterimpl.handshakeTestProvider  ok
    1  *clusterimpl.notifyingCertProvider  error
    3  *clusterimpl.notifyingCertProvider  ok
    1  *clusterimpl.testCertProvider  error
    3  *clusterimpl.testCertProvider  ok
   22  *xds.blockingProvider  error
    1  *xds.blockingRootProvider  error
    3  *xds.closableProvider  error
    1  *xds.closeableProvider  error
    4  *xds.closingProvider  error
    1  *xds.fakeProvider  error
   33  *xds.fakeProvider  ok
    2  *xds.replacedProvider  error
    3  clusterimpl.systemRootCertsProvider  ok
```

So on the 27 branches together, 47 validation-root loads either overlapped a balancer replacement/retirement event or ended with an error; none of them ended successfully, and every test containing one of them passed (all 74 lines above read `as-written=PASS`). Client-level tests in `credentials/xds` apply the replacement themselves (they store a new `HandshakeInfo`), which is not a probe event, so the tally by provider type covers them: every load on a blocking test provider type in `credentials/xds` (`blockingProvider`, `blockingRootProvider`, `closableProvider`, `closeableProvider`, `closingProvider`, `replacedProvider`) ended in an error, 33 of them in total; the only `credentials/xds` loads that succeeded were on the pre-existing non-blocking `fakeProvider`, whose `KeyMaterial` is unmodified on all 27 branches (`git diff cc234554 HEAD -- credentials/xds/xds_client_test.go | grep -c '^[-+].*fakeProvider) KeyMaterial'` prints 0 on each). The balancer-level test provider types show both results because those tests run further loads after the overlapped one; the overlapped one is always the one that failed (scan line 3).

### Raw trace, client-level overlap test (representative: 95f90b92)

```console
$ verify/probe_attempts.sh ~/wt/95f90b92 credentials/xds '^Test$/^ClientCredsProviderReplacedDuringHandshake$'
ClientHandshake#1 START conn=127.0.0.1:48880->127.0.0.1:41253
load#1 client root KeyMaterial START provider=*xds.closeableProvider
load#1 client root KeyMaterial END   err=provider instance is closed
load#2 client root KeyMaterial START provider=*xds.fakeProvider
load#2 client root KeyMaterial END   err=<nil>
ClientHandshake#1 END   err=<nil>
--- PASS: Test (0.01s)
    --- PASS: Test/ClientCredsProviderReplacedDuringHandshake (0.01s)
PASS
ok  	google.golang.org/grpc/credentials/xds	0.012s
```

The load the handshake selected (load#1) returns the closure error; the test passes because a second load on the replacement provider succeeds inside the same `ClientHandshake` call.

### Raw trace, balancer-level overlap test as written and against `--defer-close` (representative: 14b9e8f0)

```console
$ verify/probe_branch.sh ~/wt/14b9e8f0
##### internal/xds/balancer/clusterimpl/security_test.go :: Test/TestClientHandshakeDuringProviderUpdate   (go test ./internal/xds/balancer/clusterimpl -run '^Test$/^ClientHandshakeDuringProviderUpdate$' -count=1 -v -timeout 60s )
load#1 client root KeyMaterial START provider=*clusterimpl.handshakeTestProvider
balancer CLOSES replaced cached root provider
load#1 client root KeyMaterial END   err=provider instance is closed
load#2 client root KeyMaterial START provider=*clusterimpl.handshakeTestProvider
load#2 client root KeyMaterial END   err=<nil>
load#3 client root KeyMaterial START provider=*clusterimpl.handshakeTestProvider
load#3 client root KeyMaterial END   err=<nil>
balancer CLOSES replaced cached root provider
load#4 client root KeyMaterial START provider=*clusterimpl.handshakeTestProvider
load#4 client root KeyMaterial END   err=<nil>
--- PASS: Test (0.04s)
    --- PASS: Test/ClientHandshakeDuringProviderUpdate (0.04s)
        --- PASS: Test/ClientHandshakeDuringProviderUpdate/root (0.01s)
        --- PASS: Test/ClientHandshakeDuringProviderUpdate/identity (0.02s)
PASS
ok  	google.golang.org/grpc/internal/xds/balancer/clusterimpl	0.050s
$ verify/probe_branch.sh ~/wt/14b9e8f0 --defer-close
##### internal/xds/balancer/clusterimpl/security_test.go :: Test/TestClientHandshakeDuringProviderUpdate   (go test ./internal/xds/balancer/clusterimpl -run '^Test$/^ClientHandshakeDuringProviderUpdate$' -count=1 -v -timeout 60s --defer-close)
load#1 client root KeyMaterial START provider=*clusterimpl.handshakeTestProvider
balancer retires replaced root provider: Close DEFERRED, a validation-root load is still running on it
load#1 client root KeyMaterial END   err=context deadline exceeded
deferred Close of replaced root provider runs now (its last running load returned)
    security_test.go:174: Timed out waiting for the client handshake
load#2 client root KeyMaterial START provider=*clusterimpl.handshakeTestProvider
load#2 client root KeyMaterial END   err=<nil>
balancer retires replaced root provider: Close DEFERRED, a validation-root load is still running on it
deferred Close of replaced root provider runs now (its last running load returned)
load#3 client root KeyMaterial START provider=*clusterimpl.handshakeTestProvider
load#3 client root KeyMaterial END   err=<nil>
--- FAIL: Test (5.02s)
    --- FAIL: Test/ClientHandshakeDuringProviderUpdate (5.02s)
        --- FAIL: Test/ClientHandshakeDuringProviderUpdate/root (5.01s)
        --- PASS: Test/ClientHandshakeDuringProviderUpdate/identity (0.02s)
FAIL
FAIL	google.golang.org/grpc/internal/xds/balancer/clusterimpl	5.034s
FAIL
```

When the selected provider is kept usable until its load returns, this test does not pass: it needs the provider to be invalidated during the load.

### Raw trace, archived fixture as written and against `--defer-close` (representative: 95f90b92; all 27 branches print the same two verdicts, see the complete output above)

```console
$ verify/probe_fixture.sh ~/wt/95f90b92
load#1 client root KeyMaterial START provider=*certprovider.singleCloseWrappedProvider
balancer CLOSES replaced cached root provider
load#1 client root KeyMaterial END   err=provider instance is closed
load#2 client root KeyMaterial START provider=*certprovider.singleCloseWrappedProvider
load#2 client root KeyMaterial END   err=<nil>
    eval_handshake_lifetime_test.go:314: Active RPC failed after the Cluster security configuration was replaced: rpc error: code = Unavailable desc = connection error: desc = "transport: authentication handshake failed: x509: certificate signed by unknown authority"
balancer CLOSES replaced cached root provider
--- FAIL: TestEval_SecurityConfigUpdate_ActiveHandshakeKeepsProvider (0.03s)
FAIL
FAIL	google.golang.org/grpc/.evaltools/lifetime_GFaRHK	0.048s
FAIL
$ verify/probe_fixture.sh ~/wt/95f90b92 --defer-close
load#1 client root KeyMaterial START provider=*certprovider.singleCloseWrappedProvider
balancer retires replaced root provider: Close DEFERRED, a validation-root load is still running on it
load#1 client root KeyMaterial END   err=<nil>
deferred Close of replaced root provider runs now (its last running load returned)
balancer CLOSES replaced cached root provider (no load running on it)
load#2 client root KeyMaterial START provider=*certprovider.singleCloseWrappedProvider
load#2 client root KeyMaterial END   err=<nil>
balancer CLOSES replaced cached root provider (no load running on it)
--- PASS: TestEval_SecurityConfigUpdate_ActiveHandshakeKeepsProvider (0.05s)
PASS
ok  	google.golang.org/grpc/.evaltools/lifetime_hQ0A4k	0.083s
```

Fixture failure text on the 27 branches (identical on all of them after masking addresses):

```console
     27 eval_handshake_lifetime_test.go:314: Active RPC failed after the Cluster security configuration was replaced: rpc error: code = Unavailable desc = connection error: desc = "transport: authentication handshake failed: x509: certificate signed by unknown authority"
```

### Per-branch reading

| Branch | New/changed tests run | Tests whose selected load was invalidated, and passed | Tests that fail unless the provider is invalidated during the load (`--defer-close`) | Archived fixture as written / `--defer-close` |
|---|---|---|---|---|
| [evalon/grpc-go-xd-95f90b92](https://github.com/kaitranntt-evals/grpc-go-xds-certificate-provider-closure-race/tree/evalon/grpc-go-xd-95f90b92) | 1 | `TestClientCredsProviderReplacedDuringHandshake` (provider instance is closed) | none | FAIL / PASS |
| [evalon/grpc-go-xd-9b0dc3f1](https://github.com/kaitranntt-evals/grpc-go-xds-certificate-provider-closure-race/tree/evalon/grpc-go-xd-9b0dc3f1) | 1 | `TestClientCredsProviderReplacedDuringHandshake` (provider instance is closed) | none | FAIL / PASS |
| [evalon/grpc-go-xd-9094ac3e](https://github.com/kaitranntt-evals/grpc-go-xds-certificate-provider-closure-race/tree/evalon/grpc-go-xd-9094ac3e) | 1 | `TestClientCredsProviderReplacedDuringHandshake` (provider instance is closed) | none | FAIL / PASS |
| [evalon/grpc-go-xd-8954d818](https://github.com/kaitranntt-evals/grpc-go-xds-certificate-provider-closure-race/tree/evalon/grpc-go-xd-8954d818) | 1 | `TestClientCredsProviderReplacedDuringHandshake` (provider instance is closed) | none | FAIL / PASS |
| [evalon/grpc-go-xd-59c38840](https://github.com/kaitranntt-evals/grpc-go-xds-certificate-provider-closure-race/tree/evalon/grpc-go-xd-59c38840) | 1 | `TestClientCredsProviderReplacedDuringHandshake` (provider instance is closed) | none | FAIL / PASS |
| [evalon/grpc-go-xd-83aab628](https://github.com/kaitranntt-evals/grpc-go-xds-certificate-provider-closure-race/tree/evalon/grpc-go-xd-83aab628) | 1 | `TestClientCredsProviderReplacedDuringHandshake` (provider closed) | none | FAIL / PASS |
| [evalon/grpc-go-xd-bced1091](https://github.com/kaitranntt-evals/grpc-go-xds-certificate-provider-closure-race/tree/evalon/grpc-go-xd-bced1091) | 1 | `TestClientCredsProviderReplacedDuringHandshake` (provider closed) | none | FAIL / PASS |
| [evalon/grpc-go-xd-5175e7e6](https://github.com/kaitranntt-evals/grpc-go-xds-certificate-provider-closure-race/tree/evalon/grpc-go-xd-5175e7e6) | 1 | `TestClientCredsProviderReplacedDuringHandshake` (provider instance is closed) | none | FAIL / PASS |
| [evalon/grpc-go-xd-bfbe6be7](https://github.com/kaitranntt-evals/grpc-go-xds-certificate-provider-closure-race/tree/evalon/grpc-go-xd-bfbe6be7) | 2 | `TestClientCredsProviderReplacedDuringHandshake`, `TestClientCredsProviderClosedWithoutReplacement` (provider instance is closed) | none | FAIL / PASS |
| [evalon/grpc-go-xd-1d67b6b4](https://github.com/kaitranntt-evals/grpc-go-xds-certificate-provider-closure-race/tree/evalon/grpc-go-xd-1d67b6b4) | 1 | `TestClientCredsProviderReplacedDuringHandshake` (provider instance is closed) | none | FAIL / PASS |
| [evalon/grpc-go-xd-0ae111d1](https://github.com/kaitranntt-evals/grpc-go-xds-certificate-provider-closure-race/tree/evalon/grpc-go-xd-0ae111d1) | 13 | `TestClientCredsProviderClosedDuringHandshake`, `TestClientCredsProviderReplacedDuringHandshake`, `TestSecurityConfigUpdate_DuringHandshake` (provider instance is closed) | `TestSecurityConfigUpdate_DuringHandshake` | FAIL / PASS |
| [evalon/grpc-go-xd-1fc74c6c](https://github.com/kaitranntt-evals/grpc-go-xds-certificate-provider-closure-race/tree/evalon/grpc-go-xd-1fc74c6c) | 3 | `TestClientCredsProviderFailureWithoutReplacement`, `TestClientCredsProviderReplacedDuringHandshake`, `TestSecurityConfigUpdate_HandshakeInfoUpdatedBeforeProvidersClosed` (provider "root-instance-2" is closed; provider instance is closed) | none | FAIL / PASS |
| [evalon/grpc-go-xd-dbc087a7](https://github.com/kaitranntt-evals/grpc-go-xds-certificate-provider-closure-race/tree/evalon/grpc-go-xd-dbc087a7) | 1 | `TestClientCredsProviderReplacedDuringHandshake` (provider instance is closed) | none | FAIL / PASS |
| [evalon/grpc-go-xd-64b6ec0e](https://github.com/kaitranntt-evals/grpc-go-xds-certificate-provider-closure-race/tree/evalon/grpc-go-xd-64b6ec0e) | 3 | `TestClientCredsProviderClosedDuringHandshake`, `TestClientCredsProviderReplacedDuringHandshake` (provider instance is closed) | none | FAIL / PASS |
| [evalon/grpc-go-xd-d553852a](https://github.com/kaitranntt-evals/grpc-go-xds-certificate-provider-closure-race/tree/evalon/grpc-go-xd-d553852a) | 14 | `TestClientCredsProviderClosedDuringHandshake`, `TestClientCredsProviderReplacedDuringHandshake`, `TestSecurityConfigUpdate_ProvidersReplacedDuringHandshake` (provider instance is closed) | `TestSecurityConfigUpdate_ProvidersReplacedDuringHandshake` | FAIL / PASS |
| [evalon/grpc-go-xd-f521f13d](https://github.com/kaitranntt-evals/grpc-go-xds-certificate-provider-closure-race/tree/evalon/grpc-go-xd-f521f13d) | 4 | `TestClientCredsProviderClosedDuringHandshake`, `TestClientCredsProviderReplacedDuringHandshake`, `TestSecurityConfigUpdate_ProvidersReplacedDuringHandshake` (provider instance is closed) | `TestSecurityConfigUpdate_ProvidersReplacedDuringHandshake` | FAIL / PASS |
| [evalon/grpc-go-xd-b7a066a6](https://github.com/kaitranntt-evals/grpc-go-xds-certificate-provider-closure-race/tree/evalon/grpc-go-xd-b7a066a6) | 12 | `TestClientCredsProviderReplacementDuringHandshake`, `TestSecurityConfigUpdate_ProviderReplacementDuringHandshake` (provider instance is closed) | `TestSecurityConfigUpdate_ProviderReplacementDuringHandshake` | FAIL / PASS |
| [evalon/grpc-go-xd-b7e8d61f](https://github.com/kaitranntt-evals/grpc-go-xds-certificate-provider-closure-race/tree/evalon/grpc-go-xd-b7e8d61f) | 2 | `TestClientCredsProviderSwitchDuringHandshake`, `TestSecurityConfigUpdateDuringHandshake` (provider instance is closed) | `TestSecurityConfigUpdateDuringHandshake` | FAIL / PASS |
| [evalon/grpc-go-xd-d567dc9f](https://github.com/kaitranntt-evals/grpc-go-xds-certificate-provider-closure-race/tree/evalon/grpc-go-xd-d567dc9f) | 2 | `TestClientCredsProviderSwitchDuringHandshake` (provider instance is closed) | none | FAIL / PASS |
| [evalon/grpc-go-xd-14b9e8f0](https://github.com/kaitranntt-evals/grpc-go-xds-certificate-provider-closure-race/tree/evalon/grpc-go-xd-14b9e8f0) | 1 | `TestClientHandshakeDuringProviderUpdate` (provider instance is closed) | `TestClientHandshakeDuringProviderUpdate` | FAIL / PASS |
| [evalon/grpc-go-xd-5069188f](https://github.com/kaitranntt-evals/grpc-go-xds-certificate-provider-closure-race/tree/evalon/grpc-go-xd-5069188f) | 1 | `TestSecurityConfigUpdateDuringHandshake` (provider instance is closed) | `TestSecurityConfigUpdateDuringHandshake` | FAIL / PASS |
| [evalon/grpc-go-xd-437f5f4a](https://github.com/kaitranntt-evals/grpc-go-xds-certificate-provider-closure-race/tree/evalon/grpc-go-xd-437f5f4a) | 1 | `TestSecurityConfigUpdateDuringHandshake` (provider instance is closed) | `TestSecurityConfigUpdateDuringHandshake` | FAIL / PASS |
| [evalon/grpc-go-xd-c944dcde](https://github.com/kaitranntt-evals/grpc-go-xds-certificate-provider-closure-race/tree/evalon/grpc-go-xd-c944dcde) | 1 | `TestSecurityConfigUpdateDuringHandshake` (provider instance is closed) | `TestSecurityConfigUpdateDuringHandshake` | FAIL / PASS |
| [evalon/grpc-go-xd-b2e2c879](https://github.com/kaitranntt-evals/grpc-go-xds-certificate-provider-closure-race/tree/evalon/grpc-go-xd-b2e2c879) | 1 | `TestSecurityConfigUpdateDuringHandshake` (provider instance is closed) | `TestSecurityConfigUpdateDuringHandshake` | FAIL / PASS |
| [evalon/grpc-go-xd-7089d1aa](https://github.com/kaitranntt-evals/grpc-go-xds-certificate-provider-closure-race/tree/evalon/grpc-go-xd-7089d1aa) | 1 | `TestClientCredsProviderSwitchDuringHandshake` (provider instance is closed) | none | FAIL / PASS |
| [evalon/grpc-go-xd-1c65da27](https://github.com/kaitranntt-evals/grpc-go-xds-certificate-provider-closure-race/tree/evalon/grpc-go-xd-1c65da27) | 2 | `TestClientCredsProviderSwitchDuringHandshake` (provider instance is closed) | none | FAIL / PASS |
| [evalon/grpc-go-xd-fee506e4](https://github.com/kaitranntt-evals/grpc-go-xds-certificate-provider-closure-race/tree/evalon/grpc-go-xd-fee506e4) | 1 | `TestSecurityConfigUpdateDuringHandshake` (provider instance is closed) | `TestSecurityConfigUpdateDuringHandshake` | FAIL / PASS |

Checks made against a possible refutation on each branch: (1) a test that blocks a selected load, applies replacement, and then lets the original load finish successfully would appear in the scan as an overlapped load ending `err=<nil>`; there are 0 across the 27 branches. (2) Tests with `failed-loads=0` either perform no load at all (`loads=0`, balancer lifecycle tests driven without a handshake) or perform loads strictly before or after the balancer's close (`balancer-close-during-load=0`), so they do not overlap a load with replacement. (3) Tests that fail against `--no-close` but pass against `--defer-close` and have `failed-loads=0` in `clusterimpl/tests` are pre-existing end-to-end tests living in a changed file; their `--no-close` failure is the pre-existing `grpctest` goroutine leak check (`grpctest.go:45: Leaked goroutine ... pemfile.(*watcher).run`), not an assertion about an overlapped load.

### Impact reasoning

On every one of the 27 branches the production behaviour the task is about is still wrong in a way the branch's own green tests cannot see: a handshake that selected provider A and is loading roots from it gets that load invalidated when the Cluster security config is replaced, then silently re-reads the `HandshakeInfo` and validates the server against the replacement roots. With the fixture's configuration (replacement roots that do not validate the server the in-flight connection is talking to) the active RPC fails with `Unavailable ... x509: certificate signed by unknown authority` on all 27 branches, while the same fixture passes once the selected load is preserved (`--defer-close`) and passes on the audited implementation branch. The branch tests pass in both worlds or, for the balancer-level ones, only in the wrong world.

## C2

Claim: the client validation-root path lacks protection established before the selected provider's `KeyMaterial` call that preserves the selected load when replacement retires its last configuration owner. Three branches, adjudicated separately: 03538fd6, c9d55e2d, 49f3c746.

Setup and vocabulary (repeated so this section stands alone): run from the repository root with `FIXTURES_DIR` pointing at the extracted `eval_tests.zip` `tests/` directory. `store Build NEW/REUSES entry{...}: refCount now N` is a handle being acquired on a store cache entry, `store RELEASE handle ...: refCount now N` a handle being closed, `store refCount==0 -> underlying provider ... Close() called` the store closing the real provider; `load#N ... START/END err=` is the client validation-root load; `balancer CLOSES replaced cached root provider` is the Cluster security-update retirement path. The scenario is the archived fixture: replacement uses a different plugin, i.e. a different cache entry, and the balancer's handle is the only configuration owner of the original entry.

### Command

```sh
$ FIXTURES_DIR=~/eval/tests verify/repro/c2_selected_load_not_preserved.sh 03538fd6 c9d55e2d 49f3c746
```

(per branch: `VERIFY_STORE=1 verify/probe_fixture.sh ~/wt/<id>`, which runs `go test ./.evaltools/lifetime_XXXXXX -run '^TestEval_SecurityConfigUpdate_ActiveHandshakeKeepsProvider$' -count=1 -v -timeout 60s` on a copy of the archived fixture with store, load and balancer probes applied.)

### Output

```console
##### 03538fd6
store Build NEW entry{name="handshake-lifetime-a-18e362e5-af34-4788-8f84-29dae8ed6a0c" cert=""}: refCount now 1
load#1 client root KeyMaterial START provider=*certprovider.singleCloseWrappedProvider
store Build NEW entry{name="handshake-lifetime-b-102f5f0d-7f88-40b9-8111-4f2b858f3b71" cert=""}: refCount now 1
balancer CLOSES replaced cached root provider
store RELEASE handle on entry{name="handshake-lifetime-a-18e362e5-af34-4788-8f84-29dae8ed6a0c" cert=""}: refCount now 0
store refCount==0 -> underlying provider *clusterimpl_test.evalHandshakeLifetimeRootProvider.Close() called, entry{name="handshake-lifetime-a-18e362e5-af34-4788-8f84-29dae8ed6a0c" cert=""} deleted
load#1 client root KeyMaterial END   err=provider instance is closed
    eval_handshake_lifetime_test.go:314: Active RPC failed after the Cluster security configuration was replaced: rpc error: code = Unavailable desc = connection error: desc = "transport: authentication handshake failed: xds: fetching trusted roots from CertificateProvider failed: provider instance is closed"
balancer CLOSES replaced cached root provider
store RELEASE handle on entry{name="handshake-lifetime-b-102f5f0d-7f88-40b9-8111-4f2b858f3b71" cert=""}: refCount now 0
store refCount==0 -> underlying provider *clusterimpl_test.evalHandshakeLifetimeRootProvider.Close() called, entry{name="handshake-lifetime-b-102f5f0d-7f88-40b9-8111-4f2b858f3b71" cert=""} deleted
--- FAIL: TestEval_SecurityConfigUpdate_ActiveHandshakeKeepsProvider (0.01s)
FAIL
FAIL	google.golang.org/grpc/.evaltools/lifetime_iqFMLb	0.014s
FAIL
##### c9d55e2d
store Build NEW entry{name="handshake-lifetime-a-c0038431-604e-43c5-8130-4d02bc5c1086" cert=""}: refCount now 1
load#1 client root KeyMaterial START provider=*certprovider.singleCloseWrappedProvider
store Build NEW entry{name="handshake-lifetime-b-63d343a0-d3b4-4258-aaad-773476504239" cert=""}: refCount now 1
balancer CLOSES replaced cached root provider
store RELEASE handle on entry{name="handshake-lifetime-a-c0038431-604e-43c5-8130-4d02bc5c1086" cert=""}: refCount now 0
store refCount==0 -> underlying provider *clusterimpl_test.evalHandshakeLifetimeRootProvider.Close() called, entry{name="handshake-lifetime-a-c0038431-604e-43c5-8130-4d02bc5c1086" cert=""} deleted
load#1 client root KeyMaterial END   err=provider instance is closed
    eval_handshake_lifetime_test.go:314: Active RPC failed after the Cluster security configuration was replaced: rpc error: code = Unavailable desc = connection error: desc = "transport: authentication handshake failed: xds: fetching trusted roots from CertificateProvider failed: provider instance is closed"
balancer CLOSES replaced cached root provider
store RELEASE handle on entry{name="handshake-lifetime-b-63d343a0-d3b4-4258-aaad-773476504239" cert=""}: refCount now 0
store refCount==0 -> underlying provider *clusterimpl_test.evalHandshakeLifetimeRootProvider.Close() called, entry{name="handshake-lifetime-b-63d343a0-d3b4-4258-aaad-773476504239" cert=""} deleted
--- FAIL: TestEval_SecurityConfigUpdate_ActiveHandshakeKeepsProvider (0.01s)
FAIL
FAIL	google.golang.org/grpc/.evaltools/lifetime_Z0laup	0.017s
FAIL
##### 49f3c746
store Build NEW entry{name="handshake-lifetime-a-e11063b5-519a-4e72-9d31-097eec9db686" cert=""}: refCount now 1
load#1 client root KeyMaterial START provider=*certprovider.singleCloseWrappedProvider
store Build NEW entry{name="handshake-lifetime-b-8239db6d-e76c-45b5-b57e-2c494cbb7cf0" cert=""}: refCount now 1
balancer CLOSES replaced cached root provider
store RELEASE handle on entry{name="handshake-lifetime-a-e11063b5-519a-4e72-9d31-097eec9db686" cert=""}: refCount now 0
store refCount==0 -> underlying provider *clusterimpl_test.evalHandshakeLifetimeRootProvider.Close() called, entry{name="handshake-lifetime-a-e11063b5-519a-4e72-9d31-097eec9db686" cert=""} deleted
load#1 client root KeyMaterial END   err=provider instance is closed
    eval_handshake_lifetime_test.go:314: Active RPC failed after the Cluster security configuration was replaced: rpc error: code = Unavailable desc = connection error: desc = "transport: authentication handshake failed: xds: fetching trusted roots from CertificateProvider failed: provider instance is closed"
balancer CLOSES replaced cached root provider
store RELEASE handle on entry{name="handshake-lifetime-b-8239db6d-e76c-45b5-b57e-2c494cbb7cf0" cert=""}: refCount now 0
store refCount==0 -> underlying provider *clusterimpl_test.evalHandshakeLifetimeRootProvider.Close() called, entry{name="handshake-lifetime-b-8239db6d-e76c-45b5-b57e-2c494cbb7cf0" cert=""} deleted
--- FAIL: TestEval_SecurityConfigUpdate_ActiveHandshakeKeepsProvider (0.01s)
FAIL
FAIL	google.golang.org/grpc/.evaltools/lifetime_WQep76	0.015s
FAIL
```

### Reading, per branch

- 03538fd6: entry A is created with `refCount now 1` (the balancer's handle). The handshake starts load#1 on it; no `store Build` line for entry A appears between the handshake start and the `KeyMaterial` call, so the handshake acquires no handle of its own. The balancer's close takes `refCount` to 0, the underlying provider's `Close()` runs while load#1 is still running, load#1 ends `provider instance is closed`, and the active RPC fails (`Unavailable ... fetching trusted roots from CertificateProvider failed: provider instance is closed`). Production diff on this branch is confined to `credentials/tls/certprovider/store.go` (`git diff --stat cc234554 HEAD`: `store.go`, `store_test.go`, `xds_client_test.go`); the wrapper now keeps delegating after its own `Close`, which only helps while another handle keeps the same entry alive.
- c9d55e2d: identical event sequence and identical RPC failure. Production diff: `store.go` and `clusterimpl.go` (`git diff --stat`: `store.go`, `store_test.go`, `xds_client_test.go`, `balancer_test.go`, `clusterimpl.go`).
- 49f3c746: identical event sequence and identical RPC failure. Production diff: `store.go` and `clusterimpl.go`.

On all three, nothing independently usable was preserved for the selected load: the load returned an error and the RPC failed rather than completing on copied roots. No later retirement check was involved; the load was simply invalidated.

### Impact reasoning

A client RPC whose TLS handshake is waiting on the root provider at the moment a Cluster update switches the validation context to a different certificate-provider instance fails with `Unavailable`. The window is the duration of the root `KeyMaterial` call (long when the provider has no material yet, short otherwise). The branches' own tests replace with the same cache entry, where the replacement handle keeps the provider alive, so they stay green (see C7 for c9d55e2d).

## C3

Claim: a production certificate-provider wrapper with a changed load-admission mechanism allows final-owner `Close` to invalidate its underlying provider while an already admitted `KeyMaterial` call is still running. Branch 03538fd6.

### The wrapper and what changed

`certprovider.BuildableConfig.Build` returns `*certprovider.singleCloseWrappedProvider` (production code in `credentials/tls/certprovider/store.go`, exposes both `KeyMaterial` and `Close`). Its admission mechanism is changed on this branch:

```diff
$ git -C ~/wt/03538fd6 diff cc234554fb363aea445a838b341bb8a65c8305b0 HEAD -- credentials/tls/certprovider/store.go | grep -v '^[+-]\s*//'
-type closedProvider struct{}
-
-func (c closedProvider) KeyMaterial(context.Context) (*KeyMaterial, error) {
-	return nil, errProviderClosed
-}
-
-func (c closedProvider) Close() {
-}
-
 type singleCloseWrappedProvider struct {
-	provider atomic.Pointer[Provider]
+	provider  Provider
+	closeOnce sync.Once
 }
 
 // store is a collection of provider instances, safe for concurrent access.
@@ -93,26 +93,22 @@ func (wp *wrappedProvider) Close() {
 	}
 }
 
 func (w *singleCloseWrappedProvider) Close() {
-	newProvider := Provider(closedProvider{})
-	oldProvider := w.provider.Swap(&newProvider)
-	(*oldProvider).Close()
+	w.closeOnce.Do(w.provider.Close)
 }
 
 // Callers are expected to use the returned value as read-only.
 func (w *singleCloseWrappedProvider) KeyMaterial(ctx context.Context) (*KeyMaterial, error) {
-	return (*w.provider.Load()).KeyMaterial(ctx)
+	return w.provider.KeyMaterial(ctx)
 }
 
 func newSingleCloseWrappedProvider(provider Provider) *singleCloseWrappedProvider {
-	w := &singleCloseWrappedProvider{}
-	w.provider.Store(&provider)
-	return w
+	return &singleCloseWrappedProvider{provider: provider}
 }
 
 // BuildableConfig wraps parsed provider configuration and functionality to
```

Before: `KeyMaterial` loads an atomic pointer that `Close` swaps to a `closedProvider`. After: `KeyMaterial` delegates unconditionally to the shared store entry and `Close` is a `sync.Once` around the entry's reference release.

### Command

```sh
$ FIXTURES_DIR=~/eval/tests verify/repro/c3_final_close_during_keymaterial.sh
```

(copies `verify/repro/c3_final_close_during_keymaterial_test.go` into `~/wt/03538fd6/.evaltools/c3/` and runs `go test -tags verify_repro ./.evaltools/c3 -run TestC3 -count=1 -v`. The tests obtain the wrapper from `certprovider.NewBuildableConfig(...).Build(...)`, start `handle.KeyMaterial(ctx)`, wait until the call has entered the underlying provider, then call `handle.Close()` on the only handle. The underlying providers are test doubles only in the sense that they report when they are entered and closed; the object under test is the production wrapper. One underlying provider is built on the production `certprovider.Distributor`.)

### Output

```console
=== RUN   TestC3FinalOwnerCloseFailsAdmittedDistributorLoad
    c3_final_close_during_keymaterial_test.go:69: production wrapper under test: *certprovider.singleCloseWrappedProvider
    c3_final_close_during_keymaterial_test.go:79: OBSERVED: admitted KeyMaterial call returned err=provider instance is closed
--- PASS: TestC3FinalOwnerCloseFailsAdmittedDistributorLoad (0.05s)
=== RUN   TestC3FinalOwnerCloseDuringAdmittedKeyMaterial
    c3_final_close_during_keymaterial_test.go:92: production wrapper under test: *certprovider.singleCloseWrappedProvider
    c3_final_close_during_keymaterial_test.go:104: OBSERVED: underlying provider Close() ran while the admitted KeyMaterial call was still blocked
    c3_final_close_during_keymaterial_test.go:117: OBSERVED: admitted KeyMaterial call returned err=underlying provider was closed during the admitted load
--- PASS: TestC3FinalOwnerCloseDuringAdmittedKeyMaterial (0.00s)
PASS
ok  	google.golang.org/grpc/.evaltools/c3	0.053s
```

The tests are written to pass when the problem is present and to fail with `NOT REPRODUCED` otherwise. Observed: the underlying provider's `Close()` ran while the admitted call was still blocked, and the admitted call then returned the closure error (`provider instance is closed` for the `Distributor`-backed provider, even though key material was set right after the close).

### Control: same repro on the audited implementation branch (store.go identical to the base commit)

```console
$ mkdir -p .evaltools/c3 && cp verify/repro/c3_final_close_during_keymaterial_test.go .evaltools/c3/ && GOENV=off GOWORK=off go test -tags verify_repro ./.evaltools/c3 -run TestC3 -count=1 -v; rm -rf .evaltools/c3
=== RUN   TestC3FinalOwnerCloseFailsAdmittedDistributorLoad
    c3_final_close_during_keymaterial_test.go:69: production wrapper under test: *certprovider.singleCloseWrappedProvider
    c3_final_close_during_keymaterial_test.go:79: OBSERVED: admitted KeyMaterial call returned err=provider instance is closed
--- PASS: TestC3FinalOwnerCloseFailsAdmittedDistributorLoad (0.05s)
=== RUN   TestC3FinalOwnerCloseDuringAdmittedKeyMaterial
    c3_final_close_during_keymaterial_test.go:92: production wrapper under test: *certprovider.singleCloseWrappedProvider
    c3_final_close_during_keymaterial_test.go:104: OBSERVED: underlying provider Close() ran while the admitted KeyMaterial call was still blocked
    c3_final_close_during_keymaterial_test.go:117: OBSERVED: admitted KeyMaterial call returned err=underlying provider was closed during the admitted load
--- PASS: TestC3FinalOwnerCloseDuringAdmittedKeyMaterial (0.00s)
PASS
ok  	google.golang.org/grpc/.evaltools/c3	0.053s
```

The base wrapper shows the same interleaving result, so the changed admission mechanism on 03538fd6 did not introduce this property; it also did not remove it. The audited implementation branch passes the archived fixture by other means (positive control at the top of this file).

### End-to-end consequence on 03538fd6

```console
$ VERIFY_STORE=1 verify/probe_fixture.sh ~/wt/03538fd6
store Build NEW entry{name="handshake-lifetime-a-a0bce3bf-8c5d-4a4c-88a0-c3d1c8efcb61" cert=""}: refCount now 1
load#1 client root KeyMaterial START provider=*certprovider.singleCloseWrappedProvider
store Build NEW entry{name="handshake-lifetime-b-5cb73b28-e76a-4237-8b21-302dd082be53" cert=""}: refCount now 1
balancer CLOSES replaced cached root provider
store RELEASE handle on entry{name="handshake-lifetime-a-a0bce3bf-8c5d-4a4c-88a0-c3d1c8efcb61" cert=""}: refCount now 0
store refCount==0 -> underlying provider *clusterimpl_test.evalHandshakeLifetimeRootProvider.Close() called, entry{name="handshake-lifetime-a-a0bce3bf-8c5d-4a4c-88a0-c3d1c8efcb61" cert=""} deleted
load#1 client root KeyMaterial END   err=provider instance is closed
    eval_handshake_lifetime_test.go:314: Active RPC failed after the Cluster security configuration was replaced: rpc error: code = Unavailable desc = connection error: desc = "transport: authentication handshake failed: xds: fetching trusted roots from CertificateProvider failed: provider instance is closed"
balancer CLOSES replaced cached root provider
store RELEASE handle on entry{name="handshake-lifetime-b-5cb73b28-e76a-4237-8b21-302dd082be53" cert=""}: refCount now 0
store refCount==0 -> underlying provider *clusterimpl_test.evalHandshakeLifetimeRootProvider.Close() called, entry{name="handshake-lifetime-b-5cb73b28-e76a-4237-8b21-302dd082be53" cert=""} deleted
--- FAIL: TestEval_SecurityConfigUpdate_ActiveHandshakeKeepsProvider (0.01s)
FAIL
FAIL	google.golang.org/grpc/.evaltools/lifetime_pJwOlt	0.016s
FAIL
```

### Impact reasoning

Whoever holds the last handle to a store entry can make an in-flight `KeyMaterial` call on that same handle fail by closing it. In the xDS client path the last holder is the cluster balancer and the in-flight caller is a TLS handshake, which is how the archived fixture's active RPC fails on this branch.

## C4

Claim: the new or changed Go tests do not assert cleanup of a replaced certificate provider after its final dependent load or owner releases it, including the case where independently copied roots allow the handshake to outlive the provider. Branch b7a066a6.

Vocabulary (repeated so this section stands alone): `load#N client root KeyMaterial START/END ... err=` is one client validation-root load; `balancer CLOSES replaced cached root provider` is the balancer closing the replaced root provider. `--defer-close` mutant: the balancer closes a replaced root provider only after the loads already running on it returned (cleanup after the final dependent load). `--no-close` mutant: the balancer never closes it.

### Which new/changed tests observe `Close` of a replaced provider

```console
$ git -C ~/wt/b7a066a6 diff --stat cc234554fb363aea445a838b341bb8a65c8305b0 HEAD -- '*_test.go'
 credentials/xds/xds_client_test.go                 | 113 ++++++++++
 .../clusterimpl/tests/clusterimpl_security_test.go | 247 ++++++++++++++++++++-
 2 files changed, 357 insertions(+), 3 deletions(-)
$ git -C ~/wt/b7a066a6 diff cc234554fb363aea445a838b341bb8a65c8305b0 HEAD -- '*_test.go' | grep '^+' | grep -n 'func (s) Test\|closed\|Close()'
5:+// test when KeyMaterial() is first invoked, and once closed, it fails all
12:+	closed  atomic.Bool
30:+	if b.closed.Load() {
31:+		return nil, errors.New("provider instance is closed")
36:+func (b *blockingProvider) Close() {
37:+	b.closed.Store(true)
47:+func (s) TestClientCredsProviderReplacementDuringHandshake(t *testing.T) {
63:+	defer conn.Close()
95:+	// the pending KeyMaterial() call since it has been closed.
98:+	oldRoot.Close()
161:+			closed:            grpcsync.NewEvent(),
176:+// first called and when the provider is closed.
181:+	closed            *grpcsync.Event
196:+func (p *blockingProvider) Close() {
197:+	p.Provider.Close()
198:+	p.closed.Fire()
253:+func (s) TestSecurityConfigUpdate_ProviderReplacementDuringHandshake(t *testing.T) {
330:+	// being closed by the LB policy, while the handshake is still in progress.
338:+		case <-p.closed.Done():
340:+			t.Fatal("Timeout waiting for the old certificate providers to be closed")
345:+	// fails since the provider has been closed, and the handshake is expected
```

Two tests are added. `TestClientCredsProviderReplacementDuringHandshake` (`credentials/xds/xds_client_test.go`) closes the old root itself (`oldRoot.Close()`) and never asserts a closure. `TestSecurityConfigUpdate_ProviderReplacementDuringHandshake` (`internal/xds/balancer/clusterimpl/tests/clusterimpl_security_test.go`) is the only one that observes closure of a replaced provider:

```go
624-	if err := mgmtServer.Update(ctx, resources); err != nil {
625-		t.Fatalf("Failed to update management server with updated security config: %v", err)
626-	}
627-	awaitBlockingProviders(ctx, t, built, 2)
628:	for _, p := range oldProviders {
629-		select {
630-		case <-p.closed.Done():
631-		case <-ctx.Done():
632-			t.Fatal("Timeout waiting for the old certificate providers to be closed")
633-		}
634-	}
635-
636-	// Unblock the KeyMaterial() calls. The pending call on the old provider
637-	// fails since the provider has been closed, and the handshake is expected
638-	// to complete using the new providers instead.
639-	close(release)
640-
641-	// Verify that a successful RPC can be made over a secure connection. The
642-	// RPC is not made with WaitForReady, so it fails if the handshake fails
643-	// and the channel moves to TRANSIENT_FAILURE.
644-	client := testgrpc.NewTestServiceClient(cc)
645-	peer := &peer.Peer{}
646-	if _, err := client.EmptyCall(ctx, &testpb.Empty{}, grpc.Peer(peer)); err != nil {
647-		t.Fatalf("EmptyCall() failed: %v", err)
648-	}
649-	verifySecurityInformationFromPeer(t, peer, e2e.SecurityLevelMTLS)
650-}
```

It waits for the old providers to be closed while their `KeyMaterial` calls are still blocked, and only then lets the handshake proceed.

### Command

```sh
$ FIXTURES_DIR=~/eval/tests verify/repro/c4_cleanup_assertion_requires_close_during_load.sh
```

(runs `go test ./internal/xds/balancer/clusterimpl/tests -run '^Test$/^SecurityConfigUpdate_ProviderReplacementDuringHandshake$' -count=1 -v -timeout 60s` on b7a066a6 as written, then against `--defer-close`, then against `--no-close`.)

### Output

```console
##### as written
load#1 client root KeyMaterial START provider=*certprovider.singleCloseWrappedProvider
balancer CLOSES replaced cached root provider
load#1 client root KeyMaterial END   err=provider instance is closed
load#2 client root KeyMaterial START provider=*certprovider.singleCloseWrappedProvider
load#2 client root KeyMaterial END   err=<nil>
balancer CLOSES replaced cached root provider
    --- PASS: Test/SecurityConfigUpdate_ProviderReplacementDuringHandshake (0.06s)
##### --defer-close mutant
load#1 client root KeyMaterial START provider=*certprovider.singleCloseWrappedProvider
balancer retires replaced root provider: Close DEFERRED, a validation-root load is still running on it
    clusterimpl_security_test.go:632: Timeout waiting for the old certificate providers to be closed
load#1 client root KeyMaterial END   err=context deadline exceeded
deferred Close of replaced root provider runs now (its last running load returned)
load#2 client root KeyMaterial START provider=*certprovider.singleCloseWrappedProvider
load#2 client root KeyMaterial END   err=context deadline exceeded
balancer CLOSES replaced cached root provider (no load running on it)
    --- FAIL: Test/SecurityConfigUpdate_ProviderReplacementDuringHandshake (20.02s)
##### --no-close mutant
load#1 client root KeyMaterial START provider=*certprovider.singleCloseWrappedProvider
balancer SKIPS Close of replaced cached root provider (no-close mutant)
    clusterimpl_security_test.go:632: Timeout waiting for the old certificate providers to be closed
load#1 client root KeyMaterial END   err=context deadline exceeded
load#2 client root KeyMaterial START provider=*certprovider.singleCloseWrappedProvider
load#2 client root KeyMaterial END   err=context deadline exceeded
balancer SKIPS Close of replaced cached root provider (no-close mutant)
    --- FAIL: Test/SecurityConfigUpdate_ProviderReplacementDuringHandshake (30.07s)
```

As written, closure is observed while load#1 is running (before the final dependent load released the provider), and the handshake then succeeds on load#2, i.e. on the replacement provider's roots, not on independently copied roots of the original provider (load#1 returned an error, it produced no roots). Against `--defer-close`, where the replaced provider is closed only after its final dependent load returned, the test fails at its closure wait (`Timeout waiting for the old certificate providers to be closed`). So the only closure assertion accepts, and in fact requires, closure before the final dependent load finished.

### The other new test, and all 12 new/changed tests

```console
$ FIXTURES_DIR=~/eval/tests verify/repro/c1_tests_accept_premature_invalidation.sh b7a066a6
== b7a066a6
  xds_client_test.go::Test/TestClientCredsProviderReplacementDuringHandshake: as-written=PASS loads=2 failed-loads=1 [provider instance is closed] balancer-close-during-load=0 | no-close-mutant=PASS | defer-close-mutant=PASS
  clusterimpl_security_test.go::Test/TestSecurityConfigUpdate_ProviderReplacementDuringHandshake: as-written=PASS loads=2 failed-loads=1 [provider instance is closed] balancer-close-during-load=1 | no-close-mutant=FAIL | defer-close-mutant=FAIL
  clusterimpl_security_test.go::Test/TestSecurityConfigWithoutXDSCreds: as-written=PASS loads=0 failed-loads=0 balancer-close-during-load=0 | no-close-mutant=PASS | defer-close-mutant=PASS
  clusterimpl_security_test.go::Test/TestCertproviderStoreError: as-written=PASS loads=0 failed-loads=0 balancer-close-during-load=0 | no-close-mutant=PASS | defer-close-mutant=PASS
  clusterimpl_security_test.go::Test/TestGoodSecurityConfig: as-written=PASS loads=1 failed-loads=0 balancer-close-during-load=0 | no-close-mutant=FAIL | defer-close-mutant=PASS
  clusterimpl_security_test.go::Test/TestSecurityConfigUpdate_GoodToFallback: as-written=PASS loads=1 failed-loads=0 balancer-close-during-load=0 | no-close-mutant=FAIL | defer-close-mutant=PASS
  clusterimpl_security_test.go::Test/TestSecurityConfigUpdate_GoodToBad: as-written=PASS loads=1 failed-loads=0 balancer-close-during-load=0 | no-close-mutant=FAIL | defer-close-mutant=PASS
  clusterimpl_security_test.go::Test/TestAggregateClusterSecurityConfig: as-written=PASS loads=1 failed-loads=0 balancer-close-during-load=0 | no-close-mutant=FAIL | defer-close-mutant=PASS
  clusterimpl_security_test.go::Test/TestNoSecurityConfigWithXDSCreds: as-written=PASS loads=0 failed-loads=0 balancer-close-during-load=0 | no-close-mutant=PASS | defer-close-mutant=PASS
  clusterimpl_security_test.go::Test/TestSecurityConfigNotFoundInBootstrap: as-written=PASS loads=0 failed-loads=0 balancer-close-during-load=0 | no-close-mutant=PASS | defer-close-mutant=PASS
  clusterimpl_security_test.go::Test/TestSecurityConfigUpdate_BadToGood: as-written=PASS loads=1 failed-loads=0 balancer-close-during-load=0 | no-close-mutant=FAIL | defer-close-mutant=PASS
  clusterimpl_security_test.go::Test/TestSystemRootCertsSecurityConfig: as-written=PASS loads=1 failed-loads=0 balancer-close-during-load=0 | no-close-mutant=PASS | defer-close-mutant=PASS
  hidden fixture as written : --- FAIL: TestEval_SecurityConfigUpdate_ActiveHandshakeKeepsProvider (0.01s) | selected load: END   err=provider instance is closed
  hidden fixture, defer-close: --- PASS: TestEval_SecurityConfigUpdate_ActiveHandshakeKeepsProvider (0.04s)
```

The pre-existing tests in the changed file that fail against `--no-close` fail in the pre-existing `grpctest` goroutine leak check at test teardown, not in an assertion that was added or changed:

```console
$ python3 verify/instrument.py ~/wt/b7a066a6 --no-close; (cd ~/wt/b7a066a6 && go test ./internal/xds/balancer/clusterimpl/tests -run '^Test$/^GoodSecurityConfig$' -count=1 -v -timeout 60s | grep -A6 -m1 Leaked; git checkout -q -- internal/credentials/xds/handshake_info.go internal/xds/balancer/clusterimpl/clusterimpl.go)
    grpctest.go:45: Leaked goroutine: goroutine 71 [select]:
        google.golang.org/grpc/credentials/tls/certprovider/pemfile.(*watcher).run(0xc00071d930, {0x16c5288, 0xc00069ad20})
        	/home/ubuntu/wt/b7a066a6/credentials/tls/certprovider/pemfile/watcher.go:257 +0x95
        created by google.golang.org/grpc/credentials/tls/certprovider/pemfile.newProvider in goroutine 69
        	/home/ubuntu/wt/b7a066a6/credentials/tls/certprovider/pemfile/watcher.go:124 +0x1ec
    grpctest.go:77: Goroutine leak check disabled for future tests
--- FAIL: Test (10.04s)
```

### Impact reasoning

Nothing in the branch's added tests would notice if a replaced provider were closed too early (it is, during the load) or pin down that it is closed once its last dependent load is done. The one closure assertion encodes the premature closure as the expected behaviour, so a later fix that preserves the selected load would turn this test red.

## C5

Claim: the changed Go tests do not add or materially strengthen causal proof that replacement validation roots govern a follow-up connection initiated after Cluster security replacement. Four branches, adjudicated separately: 0ae111d1, d553852a, b7a066a6, 7089d1aa.

Vocabulary (repeated so this section stands alone): `ClientHandshake#N START conn=<local>-><remote>` / `END err=` is one `credsImpl.ClientHandshake` call, i.e. one connection attempt, identified by its TCP 4-tuple; `load#N client root KeyMaterial START/END provider=<type> err=` is one validation-root load; `balancer CLOSES replaced cached root provider` marks the Cluster security replacement in balancer-level tests. In the client-level tests (`credentials/xds`) the replacement is done by the test itself between the first load's START and END (it stores a new `HandshakeInfo` and closes the old provider), which is what makes the blocked load return.

### What changed in test files

```console
$ for id in 0ae111d1 d553852a b7a066a6 7089d1aa; do echo "=== $id"; git -C ~/wt/$id diff --stat cc234554fb363aea445a838b341bb8a65c8305b0 HEAD -- '*_test.go' | cat; git -C ~/wt/$id diff cc234554fb363aea445a838b341bb8a65c8305b0 HEAD -- '*_test.go' | grep -E '^-[^-]'; done
=== 0ae111d1
 credentials/xds/xds_client_test.go                 | 157 +++++++++++++++
 .../clusterimpl/tests/clusterimpl_security_test.go | 210 ++++++++++++++++++++-
 2 files changed, 366 insertions(+), 1 deletion(-)
-	case e2e.SecurityLevelMTLS:
=== d553852a
 credentials/xds/xds_client_test.go                 | 164 ++++++++++++++++++++
 internal/xds/balancer/clusterimpl/balancer_test.go | 165 +++++++++++++++++++++
 .../clusterimpl/tests/clusterimpl_security_test.go | 165 +++++++++++++++++++++
 3 files changed, 494 insertions(+)
=== b7a066a6
 credentials/xds/xds_client_test.go                 | 113 ++++++++++
 .../clusterimpl/tests/clusterimpl_security_test.go | 247 ++++++++++++++++++++-
 2 files changed, 357 insertions(+), 3 deletions(-)
-	_ "google.golang.org/grpc/credentials/tls/certprovider/pemfile" // Register the file watcher certificate provider plugin.
-	_ "google.golang.org/grpc/internal/xds/httpfilter/router"       // Register the router filter.
-	_ "google.golang.org/grpc/internal/xds/resolver"                // Register the xds resolver
=== 7089d1aa
 credentials/xds/xds_client_test.go | 114 +++++++++++++++++++++++++++++++++++++
 1 file changed, 114 insertions(+)
```

Apart from import lines, the only modified pre-existing line is on 0ae111d1: `verifySecurityInformationFromPeer` now also accepts `e2e.SecurityLevelTLS` in the branch that checks the peer certificate; no pre-existing connection scenario was changed, and the only caller passing that level is the added test (`git -C ~/wt/0ae111d1 grep -n 'verifySecurityInformationFromPeer(.*SecurityLevelTLS' -- '*_test.go'` prints only `clusterimpl_security_test.go:983`). Everything else is added tests. None of the added lines mentions a trust-failure outcome:

```console
$ for id in 0ae111d1 d553852a b7a066a6 7089d1aa; do echo "$id ...: $(git -C ~/wt/$id diff cc234554fb363aea445a838b341bb8a65c8305b0 HEAD -- '*_test.go' | grep '^+' | grep -ci 'UnknownAuthority\|CertificateInvalid\|unknown authority')"; done
0ae111d1 UnknownAuthorityError/CertificateInvalidError/unknown authority mentions in added test lines: 0
d553852a UnknownAuthorityError/CertificateInvalidError/unknown authority mentions in added test lines: 0
b7a066a6 UnknownAuthorityError/CertificateInvalidError/unknown authority mentions in added test lines: 0
7089d1aa UnknownAuthorityError/CertificateInvalidError/unknown authority mentions in added test lines: 0
```

### Command

```sh
$ FIXTURES_DIR=~/eval/tests verify/repro/c5_no_follow_up_connection.sh
```

(per scenario: `verify/probe_attempts.sh ~/wt/<id> <package> <selector>`, i.e. `go test ./<package> -run <selector> -count=1 -v -timeout 60s` with attempt, load and balancer probes.)

### Output

```console
##### 0ae111d1 internal/xds/balancer/clusterimpl/tests
ClientHandshake#1 START conn=127.0.0.1:53582->127.0.0.1:34703
load#1 client root KeyMaterial START provider=*certprovider.singleCloseWrappedProvider
balancer CLOSES replaced cached root provider
load#1 client root KeyMaterial END   err=provider instance is closed
load#2 client root KeyMaterial START provider=*certprovider.singleCloseWrappedProvider
load#2 client root KeyMaterial END   err=<nil>
ClientHandshake#1 END   err=<nil>
balancer CLOSES replaced cached root provider
--- PASS: Test (0.02s)
    --- PASS: Test/SecurityConfigUpdate_DuringHandshake (0.02s)
ok  	google.golang.org/grpc/internal/xds/balancer/clusterimpl/tests	0.025s
##### 0ae111d1 credentials/xds
ClientHandshake#1 START conn=127.0.0.1:49430->127.0.0.1:40283
load#1 client root KeyMaterial START provider=*xds.blockingProvider
load#1 client root KeyMaterial END   err=provider instance is closed
ClientHandshake#1 END   err=xds: fetching trusted roots from CertificateProvider failed: provider instance is closed
ClientHandshake#2 START conn=127.0.0.1:60724->127.0.0.1:40289
load#2 client root KeyMaterial START provider=*xds.blockingProvider
load#2 client root KeyMaterial END   err=provider instance is closed
load#3 client root KeyMaterial START provider=*xds.fakeProvider
load#3 client root KeyMaterial END   err=<nil>
ClientHandshake#2 END   err=<nil>
--- PASS: Test (0.06s)
    --- PASS: Test/ClientCredsProviderClosedDuringHandshake (0.05s)
    --- PASS: Test/ClientCredsProviderReplacedDuringHandshake (0.01s)
ok  	google.golang.org/grpc/credentials/xds	0.064s
##### d553852a internal/xds/balancer/clusterimpl/tests
ClientHandshake#1 START conn=127.0.0.1:57660->127.0.0.1:34903
load#1 client root KeyMaterial START provider=*certprovider.singleCloseWrappedProvider
balancer CLOSES replaced cached root provider
load#1 client root KeyMaterial END   err=provider instance is closed
load#2 client root KeyMaterial START provider=*certprovider.singleCloseWrappedProvider
load#2 client root KeyMaterial END   err=<nil>
ClientHandshake#1 END   err=<nil>
balancer CLOSES replaced cached root provider
--- PASS: Test (0.07s)
    --- PASS: Test/SecurityConfigUpdate_ProvidersReplacedDuringHandshake (0.07s)
ok  	google.golang.org/grpc/internal/xds/balancer/clusterimpl/tests	0.077s
##### d553852a credentials/xds
ClientHandshake#1 START conn=127.0.0.1:37984->127.0.0.1:34905
load#1 client root KeyMaterial START provider=*xds.blockingProvider
load#1 client root KeyMaterial END   err=provider instance is closed
ClientHandshake#1 END   err=xds: fetching trusted roots from CertificateProvider failed: provider instance is closed
ClientHandshake#2 START conn=127.0.0.1:50020->127.0.0.1:36955
load#2 client root KeyMaterial START provider=*xds.blockingProvider
load#2 client root KeyMaterial END   err=provider instance is closed
load#3 client root KeyMaterial START provider=*xds.fakeProvider
load#3 client root KeyMaterial END   err=<nil>
ClientHandshake#2 END   err=<nil>
--- PASS: Test (0.06s)
    --- PASS: Test/ClientCredsProviderClosedDuringHandshake (0.05s)
    --- PASS: Test/ClientCredsProviderReplacedDuringHandshake (0.01s)
ok  	google.golang.org/grpc/credentials/xds	0.065s
##### d553852a clusterimpl
balancer CLOSES replaced cached root provider
balancer CLOSES replaced cached root provider
--- PASS: Test (0.00s)
    --- PASS: Test/SecurityConfigUpdate_ProviderLifecycle (0.00s)
ok  	google.golang.org/grpc/internal/xds/balancer/clusterimpl	0.008s
##### b7a066a6 internal/xds/balancer/clusterimpl/tests
ClientHandshake#1 START conn=127.0.0.1:46454->127.0.0.1:45773
load#1 client root KeyMaterial START provider=*certprovider.singleCloseWrappedProvider
balancer CLOSES replaced cached root provider
load#1 client root KeyMaterial END   err=provider instance is closed
load#2 client root KeyMaterial START provider=*certprovider.singleCloseWrappedProvider
load#2 client root KeyMaterial END   err=<nil>
ClientHandshake#1 END   err=<nil>
balancer CLOSES replaced cached root provider
--- PASS: Test (0.02s)
    --- PASS: Test/SecurityConfigUpdate_ProviderReplacementDuringHandshake (0.02s)
ok  	google.golang.org/grpc/internal/xds/balancer/clusterimpl/tests	0.024s
##### b7a066a6 credentials/xds
ClientHandshake#1 START conn=127.0.0.1:37614->127.0.0.1:45509
load#1 client root KeyMaterial START provider=*xds.blockingProvider
load#1 client root KeyMaterial END   err=provider instance is closed
load#2 client root KeyMaterial START provider=*xds.fakeProvider
load#2 client root KeyMaterial END   err=<nil>
ClientHandshake#1 END   err=<nil>
--- PASS: Test (0.01s)
    --- PASS: Test/ClientCredsProviderReplacementDuringHandshake (0.01s)
ok  	google.golang.org/grpc/credentials/xds	0.011s
##### 7089d1aa credentials/xds
ClientHandshake#1 START conn=127.0.0.1:38498->127.0.0.1:43321
load#1 client root KeyMaterial START provider=*xds.blockingProvider
load#1 client root KeyMaterial END   err=provider instance is closed
load#2 client root KeyMaterial START provider=*xds.fakeProvider
load#2 client root KeyMaterial END   err=<nil>
ClientHandshake#1 END   err=<nil>
ClientHandshake#2 START conn=127.0.0.1:49434->127.0.0.1:45967
load#3 client root KeyMaterial START provider=*xds.fakeProvider
load#3 client root KeyMaterial END   err=<nil>
load#4 client root KeyMaterial START provider=*xds.fakeProvider
load#4 client root KeyMaterial END   err=<nil>
ClientHandshake#2 END   err=<nil>
ClientHandshake#3 START conn=127.0.0.1:45254->127.0.0.1:38813
load#5 client root KeyMaterial START provider=*xds.blockingProvider
load#5 client root KeyMaterial END   err=provider instance is closed
ClientHandshake#3 END   err=<nil>
ClientHandshake#4 START conn=127.0.0.1:37526->127.0.0.1:40253
load#6 client root KeyMaterial START provider=*xds.blockingProvider
load#6 client root KeyMaterial END   err=provider instance is closed
load#7 client root KeyMaterial START provider=*xds.fakeProvider
load#7 client root KeyMaterial END   err=replacement provider error
ClientHandshake#4 END   err=xds: fetching trusted roots from CertificateProvider failed: replacement provider error
--- PASS: Test (0.07s)
    --- PASS: Test/ClientCredsProviderSwitchDuringHandshake (0.07s)
        --- PASS: Test/ClientCredsProviderSwitchDuringHandshake/root (0.01s)
        --- PASS: Test/ClientCredsProviderSwitchDuringHandshake/identity (0.01s)
        --- PASS: Test/ClientCredsProviderSwitchDuringHandshake/fallback (0.01s)
        --- PASS: Test/ClientCredsProviderSwitchDuringHandshake/replacement_error (0.00s)
ok  	google.golang.org/grpc/credentials/xds	0.077s
```

### Reading, per branch

- 0ae111d1: `TestSecurityConfigUpdate_DuringHandshake` has exactly one connection attempt (`ClientHandshake#1`), started before the replacement; the replacement provider's load (load#2) happens inside that attempt. The test additionally asserts that the RPC used that same connection: `clusterimpl_security_test.go` fails with `EmptyCall() was made over a connection with local address %q, want it to be made over the original connection with address %q` otherwise. The two client-level tests each have one attempt that starts before the test swaps the provider (`ClientHandshake#1` for the closed-without-replacement case, which ends in an error, and `ClientHandshake#2` for the replaced case, where load#2 on the old provider fails and load#3 on the replacement succeeds inside the same attempt).
- d553852a: `TestSecurityConfigUpdate_ProvidersReplacedDuringHandshake` has one attempt, started before the replacement, recovered by load#2 inside it. `TestSecurityConfigUpdate_ProviderLifecycle` performs no connection attempt at all. The two client-level tests behave as on 0ae111d1.
- b7a066a6: `TestSecurityConfigUpdate_ProviderReplacementDuringHandshake` has one attempt, started before the replacement, recovered by load#2 inside it. `TestClientCredsProviderReplacementDuringHandshake` likewise.
- 7089d1aa: only `TestClientCredsProviderSwitchDuringHandshake` is added (four subtests, one attempt each: `ClientHandshake#1`..`#4`). In each subtest the attempt starts, the first load blocks on `*xds.blockingProvider`, the test swaps the `HandshakeInfo` and the blocked load ends `provider instance is closed`; the attempt then finishes on the replacement (`root`, `identity`), on fallback credentials (`fallback`, no second root load), or with the replacement's error (`replacement_error`). No subtest starts a connection attempt after the swap.

On none of the four branches does a changed test start a connection attempt after the replacement and tie its outcome to replacement roots, either by a distinguishable trust outcome or by replacement `KeyMaterial` supplied for that attempt. The pre-existing follow-up-connection tests (`TestSecurityConfigUpdate_BadToGood`, `_GoodToBad`, `_GoodToFallback`) are unmodified.

### Impact reasoning

The added tests demonstrate recovery of the handshake that was already in flight. They leave the pre-existing level of proof for "a connection started after the replacement uses the replacement roots" exactly where it was.

## C6

Claim: the validation-root overlap test in `credentials/xds/xds_client_test.go` accepts premature invalidation of its original selected load when retrying with replacement roots makes the RPC succeed. Branch 5175e7e6.

Naming note: the claim's pointer list mentions `concurrent_handshake_test.go`; that file does not exist on 5175e7e6 (`git -C ~/wt/5175e7e6 ls-files '*concurrent_handshake*' | wc -l` prints 0; it exists on the audited implementation branch). The test the claim describes on this branch is `TestClientCredsProviderReplacedDuringHandshake` in `credentials/xds/xds_client_test.go`, which drives `creds.ClientHandshake` directly, so "RPC success" is handshake success here.

### The test double and the test's release and success conditions

```go
// closingProvider is an implementation of the certprovider.Provider interface
// whose KeyMaterial() method blocks until the provider is closed, and then
// fails, mimicking a provider which is closed while a handshake is using it.
type closingProvider struct {
	kmCalled chan struct{}
	closed   chan struct{}
}

func (p *closingProvider) KeyMaterial(ctx context.Context) (*certprovider.KeyMaterial, error) {
	select {
	case p.kmCalled <- struct{}{}:
	default:
	}
	select {
	case <-p.closed:
		return nil, errors.New("provider instance is closed")
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (p *closingProvider) Close() {
	close(p.closed)
}

	// Replace the root provider the same way the clusterimpl balancer does:
	// publish the new HandshakeInfo and then close the old provider.
	newRoot := makeRootProvider(t, "x509/server_ca_cert.pem")
	hiPtr.Store(xdsinternal.NewHandshakeInfo(newRoot, nil, sms, false, "", false, false))
	oldRoot.Close()

	var res result
	select {
	case res = <-resCh:
	case <-ctx.Done():
		t.Fatal("Timeout waiting for ClientHandshake() to return")
	}
	if res.err != nil {
		t.Fatalf("ClientHandshake() failed during concurrent provider replacement: %v", res.err)
	}
	if err := compareAuthInfo(ctx, ts, res.ai); err != nil {
		t.Fatal(err)
```

`closingProvider.KeyMaterial` can only return by failing: it blocks until `Close()` and then returns `provider instance is closed` (or the context error). The test's only release for the blocked load is `oldRoot.Close()`, and its only success condition is that `ClientHandshake` returns no error and the auth info matches.

### Command

```sh
$ FIXTURES_DIR=~/eval/tests verify/repro/c6_overlap_test_accepts_invalidation.sh
```

(step 1: `verify/probe_attempts.sh ~/wt/5175e7e6 credentials/xds '^Test$/^ClientCredsProviderReplacedDuringHandshake$'`; step 2: deletes the line `oldRoot.Close()` from the test, reruns `go test ./credentials/xds -run '^Test$/^ClientCredsProviderReplacedDuringHandshake$' -count=1 -v -timeout 60s`, restores the file.)

### Output

```console
##### as written
ClientHandshake#1 START conn=127.0.0.1:52556->127.0.0.1:34657
load#1 client root KeyMaterial START provider=*xds.closingProvider
load#1 client root KeyMaterial END   err=provider instance is closed
load#2 client root KeyMaterial START provider=*xds.fakeProvider
load#2 client root KeyMaterial END   err=<nil>
ClientHandshake#1 END   err=<nil>
--- PASS: Test (0.01s)
    --- PASS: Test/ClientCredsProviderReplacedDuringHandshake (0.01s)
PASS
ok  	google.golang.org/grpc/credentials/xds	0.011s
##### test mutation: oldRoot.Close() removed
735:	oldRoot.Close()
    xds_client_test.go:741: Timeout waiting for ClientHandshake() to return
--- FAIL: Test (1.00s)
    --- FAIL: Test/ClientCredsProviderReplacedDuringHandshake (1.00s)
FAIL
FAIL	google.golang.org/grpc/credentials/xds	1.008s
FAIL
```

As written, the original selected load (load#1, on `*xds.closingProvider`) returns `provider instance is closed`, a second load on the replacement (`*xds.fakeProvider`) succeeds inside the same handshake attempt, and the test passes; no assertion rejects the invalidation of load#1. With the invalidation removed, the test cannot pass: it times out waiting for `ClientHandshake()` to return. The test therefore does not merely tolerate premature invalidation of the selected load, it depends on it.

### Impact reasoning

This is the branch's only test of the overlap, and it is green exactly when the behaviour the task set out to prevent occurs. On the same branch the archived fixture fails (`selected load: END err=provider instance is closed`, `--- FAIL: TestEval_SecurityConfigUpdate_ActiveHandshakeKeepsProvider`) and passes against `--defer-close`:

```console
$ FIXTURES_DIR=~/eval/tests verify/repro/c1_tests_accept_premature_invalidation.sh 5175e7e6
== 5175e7e6
  xds_client_test.go::Test/TestClientCredsProviderReplacedDuringHandshake: as-written=PASS loads=2 failed-loads=1 [provider instance is closed] balancer-close-during-load=0 | no-close-mutant=PASS | defer-close-mutant=PASS
  hidden fixture as written : --- FAIL: TestEval_SecurityConfigUpdate_ActiveHandshakeKeepsProvider (0.02s) | selected load: END   err=provider instance is closed
  hidden fixture, defer-close: --- PASS: TestEval_SecurityConfigUpdate_ActiveHandshakeKeepsProvider (0.05s)
```

## C7

Claim: the regression tests do not exercise replacement during an active client validation-root load with a distinct underlying provider that leaves no replacement-owned handle retaining the original provider's cache entry. Branch c9d55e2d.

Naming note: the claim's pointer list mentions `concurrent_handshake_test.go` and `providerA`/`providerB` builder registration; neither exists in the tracked files of c9d55e2d (`git -C ~/wt/c9d55e2d grep -nw 'providerA\|providerB' -- '*_test.go' | wc -l` prints 0 and `git -C ~/wt/c9d55e2d ls-files '*concurrent_handshake*' | wc -l` prints 0; the file exists on the audited implementation branch). The nearest equivalents on this branch are the three added tests below.

Vocabulary (repeated so this section stands alone): `store Build NEW entry{name cert}: refCount now 1` is the store creating a new underlying provider for a cache key; `store Build REUSES entry{...}: refCount now N` is another handle onto the same underlying provider; `store RELEASE handle ...: refCount now N` is a handle being closed; `store refCount==0 -> underlying provider ... Close() called` is the real provider being closed. `ClientHandshake#N` is a connection attempt, `load#N client root KeyMaterial START/END` a validation-root load.

### New/changed tests on the branch

```console
$ git -C ~/wt/c9d55e2d diff --stat cc234554fb363aea445a838b341bb8a65c8305b0 HEAD -- '*_test.go'
 credentials/tls/certprovider/store_test.go         |  69 +++++++
 credentials/xds/xds_client_test.go                 | 198 +++++++++++++++++++++
 internal/xds/balancer/clusterimpl/balancer_test.go | 152 ++++++++++++++++
 3 files changed, 419 insertions(+)
$ git -C ~/wt/c9d55e2d diff cc234554fb363aea445a838b341bb8a65c8305b0 HEAD -- '*_test.go' | grep '^+func (s) Test'
+func (s) TestStoreProviderReplacement(t *testing.T) {
+func (s) TestClientCredsProviderReplacementDuringHandshake(t *testing.T) {
+func (s) TestSecurityConfigUnchanged_ProvidersNotRebuilt(t *testing.T) {
```

### Command

```sh
$ FIXTURES_DIR=~/eval/tests verify/repro/c7_no_distinct_provider_overlap.sh
```

(three `VERIFY_STORE=1 verify/probe_attempts.sh ~/wt/c9d55e2d <package> <selector>` runs, then `verify/probe_fixture.sh ~/wt/c9d55e2d`.)

### Output

```console
##### credentials/xds TestClientCredsProviderReplacementDuringHandshake
store Build NEW entry{name="store-backed-fake-certificate-provider" cert="root"}: refCount now 1
store Build NEW entry{name="store-backed-fake-certificate-provider" cert="identity"}: refCount now 1
ClientHandshake#1 START conn=127.0.0.1:39144->127.0.0.1:38643
load#1 client root KeyMaterial START provider=*certprovider.singleCloseWrappedProvider
store Build REUSES entry{name="store-backed-fake-certificate-provider" cert="root"}: refCount now 2
store Build REUSES entry{name="store-backed-fake-certificate-provider" cert="identity"}: refCount now 2
store RELEASE handle on entry{name="store-backed-fake-certificate-provider" cert="root"}: refCount now 1
store RELEASE handle on entry{name="store-backed-fake-certificate-provider" cert="identity"}: refCount now 1
load#1 client root KeyMaterial END   err=<nil>
ClientHandshake#1 END   err=<nil>
ClientHandshake#2 START conn=127.0.0.1:39148->127.0.0.1:38643
load#2 client root KeyMaterial START provider=*certprovider.singleCloseWrappedProvider
load#2 client root KeyMaterial END   err=<nil>
ClientHandshake#2 END   err=<nil>
store RELEASE handle on entry{name="store-backed-fake-certificate-provider" cert="identity"}: refCount now 0
store refCount==0 -> underlying provider *xds.distributorProvider.Close() called, entry{name="store-backed-fake-certificate-provider" cert="identity"} deleted
store RELEASE handle on entry{name="store-backed-fake-certificate-provider" cert="root"}: refCount now 0
store refCount==0 -> underlying provider *xds.distributorProvider.Close() called, entry{name="store-backed-fake-certificate-provider" cert="root"} deleted
--- PASS: Test (0.04s)
    --- PASS: Test/ClientCredsProviderReplacementDuringHandshake (0.03s)
PASS
ok  	google.golang.org/grpc/credentials/xds	0.039s
##### clusterimpl TestSecurityConfigUnchanged_ProvidersNotRebuilt
balancer CLOSES replaced cached root provider
balancer CLOSES replaced cached root provider
--- PASS: Test (0.00s)
    --- PASS: Test/SecurityConfigUnchanged_ProvidersNotRebuilt (0.00s)
PASS
ok  	google.golang.org/grpc/internal/xds/balancer/clusterimpl	0.008s
##### certprovider TestStoreProviderReplacement
store Build NEW entry{name="fake-certificate-provider-1" cert="foo"}: refCount now 1
store Build REUSES entry{name="fake-certificate-provider-1" cert="foo"}: refCount now 2
store RELEASE handle on entry{name="fake-certificate-provider-1" cert="foo"}: refCount now 1
store RELEASE handle on entry{name="fake-certificate-provider-1" cert="foo"}: refCount now 0
store refCount==0 -> underlying provider *certprovider.fakeProvider.Close() called, entry{name="fake-certificate-provider-1" cert="foo"} deleted
store Build NEW entry{name="fake-certificate-provider-1" cert="foo"}: refCount now 1
store RELEASE handle on entry{name="fake-certificate-provider-1" cert="foo"}: refCount now 0
store refCount==0 -> underlying provider *certprovider.fakeProvider.Close() called, entry{name="fake-certificate-provider-1" cert="foo"} deleted
--- PASS: Test (0.01s)
    --- PASS: Test/StoreProviderReplacement (0.01s)
PASS
ok  	google.golang.org/grpc/credentials/tls/certprovider	0.014s
##### archived hidden fixture (distinct provider A -> B)
store Build NEW entry{name="handshake-lifetime-a-ce1d2c4c-1aaf-4d60-bc20-c7735e6354da" cert=""}: refCount now 1
load#1 client root KeyMaterial START provider=*certprovider.singleCloseWrappedProvider
store Build NEW entry{name="handshake-lifetime-b-1af58c8b-530a-4c31-8fa5-38fd2e7a0eb5" cert=""}: refCount now 1
balancer CLOSES replaced cached root provider
store RELEASE handle on entry{name="handshake-lifetime-a-ce1d2c4c-1aaf-4d60-bc20-c7735e6354da" cert=""}: refCount now 0
store refCount==0 -> underlying provider *clusterimpl_test.evalHandshakeLifetimeRootProvider.Close() called, entry{name="handshake-lifetime-a-ce1d2c4c-1aaf-4d60-bc20-c7735e6354da" cert=""} deleted
load#1 client root KeyMaterial END   err=provider instance is closed
    eval_handshake_lifetime_test.go:314: Active RPC failed after the Cluster security configuration was replaced: rpc error: code = Unavailable desc = connection error: desc = "transport: authentication handshake failed: xds: fetching trusted roots from CertificateProvider failed: provider instance is closed"
balancer CLOSES replaced cached root provider
store RELEASE handle on entry{name="handshake-lifetime-b-1af58c8b-530a-4c31-8fa5-38fd2e7a0eb5" cert=""}: refCount now 0
store refCount==0 -> underlying provider *clusterimpl_test.evalHandshakeLifetimeRootProvider.Close() called, entry{name="handshake-lifetime-b-1af58c8b-530a-4c31-8fa5-38fd2e7a0eb5" cert=""} deleted
--- FAIL: TestEval_SecurityConfigUpdate_ActiveHandshakeKeepsProvider (0.01s)
FAIL
FAIL	google.golang.org/grpc/.evaltools/lifetime_UsM3xz	0.016s
FAIL
```

### Reading

- `TestClientCredsProviderReplacementDuringHandshake` is the only added test that overlaps an active validation-root load (load#1) with a replacement. The replacement is built from the same config and options, so it lands on the same cache entry: `store Build REUSES entry{name="store-backed-fake-certificate-provider" cert="root"}: refCount now 2`. Closing the old handles leaves `refCount now 1`, held by the replacement handle, so the underlying provider is never closed during the load and load#1 ends `err=<nil>`. The test itself asserts the reuse (`A new root provider instance was created when an existing one should have been reused`).
- `TestSecurityConfigUnchanged_ProvidersNotRebuilt` is the test that uses distinct underlying providers (the balancer closes replaced providers twice), and it performs no connection attempt and no validation-root load.
- `TestStoreProviderReplacement` exercises the store only (same cache key, no client load).

### Whole-package scan on c9d55e2d (pre-existing tests included)

```console
$ for p in credentials/xds internal/credentials/xds internal/xds/balancer/clusterimpl internal/xds/balancer/clusterimpl/tests credentials/tls/certprovider; do echo "##### $p"; VERIFY_STORE=1 verify/probe_attempts.sh ~/wt/c9d55e2d $p '.'; done > ~/eval/logs/c9d55e2d/c7_allpkgs.log 2>&1
$ grep -E '^(ok|FAIL|--- FAIL|#####)' ~/eval/logs/c9d55e2d/c7_allpkgs.log
##### credentials/xds
ok  	google.golang.org/grpc/credentials/xds	0.363s
##### internal/credentials/xds
ok  	google.golang.org/grpc/internal/credentials/xds	0.159s
##### internal/xds/balancer/clusterimpl
ok  	google.golang.org/grpc/internal/xds/balancer/clusterimpl	0.011s
##### internal/xds/balancer/clusterimpl/tests
--- FAIL: TestEval_SecurityConfigUpdate_ActiveHandshakeKeepsProvider (0.00s)
FAIL
FAIL	google.golang.org/grpc/internal/xds/balancer/clusterimpl/tests	3.967s
FAIL
##### credentials/tls/certprovider
ok  	google.golang.org/grpc/credentials/tls/certprovider	0.158s
$ python3 verify/scan_overlaps.py -v ~/eval/logs/c9d55e2d/c7_allpkgs.log
credentials/xds load#8 END err=root provider error
credentials/xds load#10 END err=<nil>
     during the load: store Build REUSES entry{name="store-backed-fake-certificate-provider" cert="root"}: refCount now 2
     during the load: store Build REUSES entry{name="store-backed-fake-certificate-provider" cert="identity"}: refCount now 2
     during the load: store RELEASE handle on entry{name="store-backed-fake-certificate-provider" cert="root"}: refCount now 1
     during the load: store RELEASE handle on entry{name="store-backed-fake-certificate-provider" cert="identity"}: refCount now 1
internal/xds/balancer/clusterimpl/tests load#9 END err=provider instance is closed
     during the load: store Build NEW entry{name="handshake-lifetime-b-5a0b96ac-8619-47d3-ad0a-09b7883fe344" cert=""}: refCount now 1
     during the load: balancer CLOSES replaced cached root provider
     during the load: store RELEASE handle on entry{name="handshake-lifetime-a-8c07a786-95df-41a7-87d2-73595471ea5e" cert=""}: refCount now 0
     during the load: store refCount==0 -> underlying provider *clusterimpl_test.evalHandshakeLifetimeRootProvider.Close() called, entry{name="handshake-lifetime-a-8c07a786-95df-41a7-87d2-7359
validation-root loads observed: 32
loads that overlapped a balancer/store event or ended with an error: 3
  of which ended err=<nil>: 1
load results by root-provider type:
    1  *certprovider.singleCloseWrappedProvider  error
    9  *certprovider.singleCloseWrappedProvider  ok
    1  *xds.fakeProvider  error
   17  *xds.fakeProvider  ok
    1  *xds.testCertProviderWithKeyMaterial  ok
    2  *xds.testProviderWithRoots  ok
    1  clusterimpl.systemRootCertsProvider  ok
```

Across every test in those five packages, 32 validation-root loads ran. Exactly two of them overlapped a store or balancer event: the branch's own same-entry test (load ends `err=<nil>` because the replacement handle retains the entry), and the archived fixture, which is provisioned by the audit and is not one of the branch's tests (distinct provider A replaced by B, entry A goes to `refCount now 0`, load ends `provider instance is closed`, test fails). The third listed load (`root provider error`) is a pre-existing provider-failure test with no overlap. The only `FAIL` in the scan is the archived fixture.

### Impact reasoning

The case the branch leaves untested is the one in which its production behaviour fails: with a distinct replacement provider and no replacement-owned handle on the original entry, the active RPC fails with `Unavailable ... provider instance is closed`. The branch's green overlap test passes only because the replacement shares the cache entry.
