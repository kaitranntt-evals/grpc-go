Observations for audit run `v-1387f992`. Audited branch: [grpc-go-xds-rds-interceptor-lifecycle-leak-perfect](https://github.com/kaitranntt-evals/grpc-go/tree/grpc-go-xds-rds-interceptor-lifecycle-leak-perfect) at `614cb7398c43`. Everything below was run on linux/amd64 with go1.25.7. No production file was modified on any branch; probes were copied into the package under test for the duration of one `go test` run and removed again, and the two C2 probes that temporarily edit a file (`c2_part*.sh`) restore it and print the number of dirty tracked files afterwards.

## Setup

```sh
cd ~/repos/grpc-go
git fetch origin grpc-go-xds-rds-interceptor-lifecycle-leak-perfect
git checkout verify/grpc-go-xds-rds-interceptor-lifecycle-leak-v-1387f992   # = origin/grpc-go-xds-rds-interceptor-lifecycle-leak-perfect (614cb7398c43) + verify/
verify/repro/setup_worktrees.sh          # one detached worktree per claim-target branch under ~/wt/<short id>
git worktree add --detach ~/wt/base 4ee6ac46fada69c06576cee108b009689a000520   # the task's base commit, used as a control
export OUT=/tmp/verify-out               # raw logs; the filtered output of every run below is committed under verify/logs/
```

The claim-target branches live in a different repository than `origin` (`kaitranntt-evals/grpc-go-xds-rds-interceptor-lifecycle-leak`); `setup_worktrees.sh` adds it as remote `claims`. Commits that were checked out:

```console
20c60584  11f770c02b3d
2bdc2970  b95d7e76a111
2c8abc16  cd3479254c21
3d609e1d  ecf7373bab10
43ae1ade  63a7d9e80e57
4e0dad18  ed2325f25a82
52de0075  dcd17cf7ae08
545b9364  fc9cc30b9df6
55cd92cd  29cffcaa9592
5e4946c0  0f8a5ed43f5e
a0b82600  4be1804d178f
afc3b15e  7e4e6eb64645
b158dd8d  44025dd4aea9
b7d20b57  fef4a5ee4a74
c47f6009  cd1d4ee207d9
c5d0b2f7  d53dee74d436
cd6e69ae  861931f1af7e
d33210d2  1a858c3c0adb
d5ce5e46  866595f23422
dc4ed267  560ce6bc2276
df007aa8  9b671836042a
e14bf49d  41969e0a5b1f
e1dc717f  f2f6b0d2a51d
e5dfd84c  93f13e227fc5
ee3cc2af  232be719f4d6
base      4ee6ac46fada
audited   614cb7398c43
```

Helper scripts (all under `verify/repro/`, each starts with a `# Run:` line):

- `run_probe.sh <audited|short id> <package> <-run regex> <probe files...>` copies the probe test files into the package of that checkout, runs `go test -tags verify_audit -race -count=1 -v`, removes the files, and prints only the probe's own lines.
- `c2_part1_delay.sh`, `c2_part2_nonotify.sh`, `c2_part3_barrier.sh`, `c2_part4_cleanup.sh` are the four C2 probes; `c8_vet_context.sh` is the `scripts/vet.sh` predicate.

Baseline on the audited branch (the byte-exact fixture from `eval_tests.zip` copied to `test/xds/eval_xds_server_interceptor_leak_test.go`, then removed again):

```sh
cp ~/eval_tests/tests/eval_xds_server_interceptor_leak_test.go test/xds/
go test -race -count=1 -v -run '^Test$/^Eval_ServerSideXDS_' ./test/xds 2>&1 | grep -E '^\s*--- |^ok|^FAIL'
go test -race -count=1 ./internal/xds/server/... | tail -5
rm test/xds/eval_xds_server_interceptor_leak_test.go
```

```console
--- PASS: Test (0.16s)
    --- PASS: Test/Eval_ServerSideXDS_InterceptorLeak_MultiGenerationRDSUpdate (0.05s)
    --- PASS: Test/Eval_ServerSideXDS_InterceptorLeak_RDSUpdate (0.02s)
    --- PASS: Test/Eval_ServerSideXDS_InterceptorSwapOrder (0.03s)
    --- PASS: Test/Eval_ServerSideXDS_PartialRouteFailure_ClosesInterceptors (0.03s)
    --- PASS: Test/Eval_ServerSideXDS_PartialVirtualHostFailure_ClosesInterceptors (0.02s)
ok  	google.golang.org/grpc/test/xds	1.187s
exit=0
ok  	google.golang.org/grpc/internal/xds/server	1.089s
exit=0
```

## C1

Claim: a successful configuration replacement or shutdown releases a retired configuration's server filter references before all interceptors that depend on those filters finish closing. Targets: 13 branches.

Probe. `verify/repro/verify_harness_test.go` is a black-box probe: it registers its own HTTP filter through `httpfilter.Register`, starts a real `xds.NewGRPCServer` against the in-process e2e management server, and pushes Listener/RouteConfiguration resources through that management server exactly like the repo's own `test/xds` tests. The probe filter appends one line to an ordered log whenever the underlying `ServerFilter` is built or closed and whenever an interceptor is built, invoked (`AllowRPC`) or closed (`CLOSE-START` on entry to `Close`, `CLOSE-END` on return; in the disable/re-enable and shutdown tests the probe interceptor's `Close` takes 200ms so that the two are distinguishable). `filterClosed=` is whether the underlying filter's `Close` had already run at that moment, `selfClosed=` whether the interceptor's own `Close` had already returned, and `via` is the chain of `internal/xds/server` functions on the calling stack (innermost first). The file only uses public/e2e APIs, so the identical file runs on every branch.

`TestVerifyH_DisableThenReenable` serves one RouteConfiguration with probe filter `F` enabled (gen1), then publishes a replacement in which `F` is disabled on every route through `typed_per_filter_config` (a successful, ACKed update; the test waits until an RPC is served without `F`). `TestVerifyH_ShutdownOrder` serves gen1 and calls `Stop()`.

```sh
for b in 4e0dad18 b158dd8d df007aa8 b7d20b57 ee3cc2af a0b82600 5e4946c0 e14bf49d e1dc717f 2c8abc16 43ae1ade c47f6009 52de0075; do
  verify/repro/run_probe.sh $b test/xds '^Test$/^VerifyH_' verify_harness_test.go
done
```

Key output per branch (replacement log from the `>>> publishing` marker on, close-related lines only, then the shutdown observation; full output in `verify/logs/A.<id>.out`):

[evalon/grpc-go-xd-4e0dad18](https://github.com/kaitranntt-evals/grpc-go-xds-rds-interceptor-lifecycle-leak/tree/evalon/grpc-go-xd-4e0dad18) (`ed2325f25a82`):

```console
  [03] TEST: >>> publishing RDS with F disabled on every route
  [05] FILTER F#1 CLOSED (underlying ServerFilter.Close ran) via (*refCountedServerFilter).Close < (*filterChain).constructUsableRouteConfiguration < (*listenerWrapper).handleRDSUpdate < (*rdsWatcher).ResourceChanged
  [06] INTERCEPTOR F#1/i1[gen1] CLOSE-START filterClosed=true via (*interceptorList).Close < (*virtualHostWithInterceptors).close < (*usableRouteConfiguration).close < (*listenerWrapper).handleRDSUpdate < (*rdsWatcher).ResourceChanged
  [08] INTERCEPTOR F#1/i1[gen1] CLOSE-END filterClosed=true
OBSERVATION(C1/C9 replacement): FILTER-RELEASED-BEFORE-INTERCEPTOR-CLOSE: underlying filter Close at [05] precedes retired interceptor CLOSE-END at [08]
OBSERVATION(C1 shutdown): INTERCEPTOR-CLOSED-BEFORE-FILTER-RELEASE: interceptor CLOSE-END [05] < filter Close [06]
exit=0
```

[evalon/grpc-go-xd-b158dd8d](https://github.com/kaitranntt-evals/grpc-go-xds-rds-interceptor-lifecycle-leak/tree/evalon/grpc-go-xd-b158dd8d) (`44025dd4aea9`):

```console
  [03] TEST: >>> publishing RDS with F disabled on every route
  [05] FILTER F#1 CLOSED (underlying ServerFilter.Close ran) via (*refCountedServerFilter).Close < (*filterChain).constructUsableRouteConfiguration < (*listenerWrapper).handleRDSUpdate < (*rdsWatcher).ResourceChanged
  [06] INTERCEPTOR F#1/i1[gen1] CLOSE-START filterClosed=true via (*interceptorList).Close < (*usableRouteConfiguration).closeInterceptors < (*filterChain).setUsableRouteConfiguration < (*listenerWrapper).handleRDSUpdate < (*rdsWatcher).ResourceChanged
  [08] INTERCEPTOR F#1/i1[gen1] CLOSE-END filterClosed=true
OBSERVATION(C1/C9 replacement): FILTER-RELEASED-BEFORE-INTERCEPTOR-CLOSE: underlying filter Close at [05] precedes retired interceptor CLOSE-END at [08]
OBSERVATION(C1 shutdown): INTERCEPTOR-CLOSED-BEFORE-FILTER-RELEASE: interceptor CLOSE-END [05] < filter Close [06]
exit=0
```

[evalon/grpc-go-xd-df007aa8](https://github.com/kaitranntt-evals/grpc-go-xds-rds-interceptor-lifecycle-leak/tree/evalon/grpc-go-xd-df007aa8) (`9b671836042a`):

```console
  [03] TEST: >>> publishing RDS with F disabled on every route
  [05] FILTER F#1 CLOSED (underlying ServerFilter.Close ran) via (*refCountedServerFilter).Close < (*filterChain).constructUsableRouteConfiguration < (*listenerWrapper).handleRDSUpdate < (*rdsWatcher).ResourceChanged
  [06] INTERCEPTOR F#1/i1[gen1] CLOSE-START filterClosed=true via (*interceptorList).Close < (*usableRouteConfiguration).closeInterceptors < (*usableRouteConfiguration).retire < (*filterChain).setUsableRouteConfiguration < (*listenerWrapper).handleRDSUpdate
  [08] INTERCEPTOR F#1/i1[gen1] CLOSE-END filterClosed=true
OBSERVATION(C1/C9 replacement): FILTER-RELEASED-BEFORE-INTERCEPTOR-CLOSE: underlying filter Close at [05] precedes retired interceptor CLOSE-END at [08]
OBSERVATION(C1 shutdown): INTERCEPTOR-CLOSED-BEFORE-FILTER-RELEASE: interceptor CLOSE-END [05] < filter Close [06]
exit=0
```

[evalon/grpc-go-xd-b7d20b57](https://github.com/kaitranntt-evals/grpc-go-xds-rds-interceptor-lifecycle-leak/tree/evalon/grpc-go-xd-b7d20b57) (`fef4a5ee4a74`):

```console
  [03] TEST: >>> publishing RDS with F disabled on every route
  [05] FILTER F#1 CLOSED (underlying ServerFilter.Close ran) via (*refCountedServerFilter).Close < (*filterChain).constructUsableRouteConfiguration < (*listenerWrapper).handleRDSUpdate < (*rdsWatcher).ResourceChanged
  [06] INTERCEPTOR F#1/i1[gen1] CLOSE-START filterClosed=true via (*interceptorList).Close < (*usableRouteConfiguration).closeInterceptors < (*listenerWrapper).handleRDSUpdate < (*rdsWatcher).ResourceChanged
  [08] INTERCEPTOR F#1/i1[gen1] CLOSE-END filterClosed=true
OBSERVATION(C1/C9 replacement): FILTER-RELEASED-BEFORE-INTERCEPTOR-CLOSE: underlying filter Close at [05] precedes retired interceptor CLOSE-END at [08]
OBSERVATION(C1 shutdown): INTERCEPTOR-CLOSED-BEFORE-FILTER-RELEASE: interceptor CLOSE-END [05] < filter Close [06]
exit=0
```

[evalon/grpc-go-xd-ee3cc2af](https://github.com/kaitranntt-evals/grpc-go-xds-rds-interceptor-lifecycle-leak/tree/evalon/grpc-go-xd-ee3cc2af) (`232be719f4d6`):

```console
  [03] TEST: >>> publishing RDS with F disabled on every route
  [05] FILTER F#1 CLOSED (underlying ServerFilter.Close ran) via (*refCountedServerFilter).Close < (*filterChain).constructUsableRouteConfiguration < (*listenerWrapper).handleRDSUpdate < (*rdsWatcher).ResourceChanged
  [06] INTERCEPTOR F#1/i1[gen1] CLOSE-START filterClosed=true via (*interceptorList).Close < (*virtualHostWithInterceptors).closeInterceptors < (*usableRouteConfiguration).closeInterceptors < (*filterChain).setUsableRouteConfiguration < (*listenerWrapper).handleRDSUpdate
  [08] INTERCEPTOR F#1/i1[gen1] CLOSE-END filterClosed=true
OBSERVATION(C1/C9 replacement): FILTER-RELEASED-BEFORE-INTERCEPTOR-CLOSE: underlying filter Close at [05] precedes retired interceptor CLOSE-END at [08]
OBSERVATION(C1 shutdown): INTERCEPTOR-CLOSED-BEFORE-FILTER-RELEASE: interceptor CLOSE-END [05] < filter Close [06]
exit=0
```

[evalon/grpc-go-xd-a0b82600](https://github.com/kaitranntt-evals/grpc-go-xds-rds-interceptor-lifecycle-leak/tree/evalon/grpc-go-xd-a0b82600) (`4be1804d178f`):

```console
  [03] TEST: >>> publishing RDS with F disabled on every route
  [05] FILTER F#1 CLOSED (underlying ServerFilter.Close ran) via (*refCountedServerFilter).Close < (*filterChain).constructUsableRouteConfiguration < (*listenerWrapper).handleRDSUpdate < (*rdsWatcher).ResourceChanged
  [06] INTERCEPTOR F#1/i1[gen1] CLOSE-START filterClosed=true via (*interceptorList).Close < (*usableRouteConfiguration).closeInterceptors < (*filterChain).updateUsableRouteConfiguration < (*listenerWrapper).handleRDSUpdate < (*rdsWatcher).ResourceChanged
  [08] INTERCEPTOR F#1/i1[gen1] CLOSE-END filterClosed=true
OBSERVATION(C1/C9 replacement): FILTER-RELEASED-BEFORE-INTERCEPTOR-CLOSE: underlying filter Close at [05] precedes retired interceptor CLOSE-END at [08]
OBSERVATION(C1 shutdown): INTERCEPTOR-CLOSED-BEFORE-FILTER-RELEASE: interceptor CLOSE-END [05] < filter Close [06]
exit=0
```

[evalon/grpc-go-xd-5e4946c0](https://github.com/kaitranntt-evals/grpc-go-xds-rds-interceptor-lifecycle-leak/tree/evalon/grpc-go-xd-5e4946c0) (`0f8a5ed43f5e`):

```console
  [03] TEST: >>> publishing RDS with F disabled on every route
  [04] FILTER F#1 CLOSED (underlying ServerFilter.Close ran) via (*refCountedServerFilter).Close < (*filterChain).constructUsableRouteConfiguration < (*listenerWrapper).handleRDSUpdate < (*rdsWatcher).ResourceChanged
  [05] INTERCEPTOR F#1/i1[gen1] CLOSE-START filterClosed=true via (*interceptorList).Close < closeVirtualHostInterceptors < (*usableRouteConfiguration).closeInterceptors < (*filterChain).setUsableRouteConfiguration < (*listenerWrapper).handleRDSUpdate
  [07] INTERCEPTOR F#1/i1[gen1] CLOSE-END filterClosed=true
OBSERVATION(C1/C9 replacement): FILTER-RELEASED-BEFORE-INTERCEPTOR-CLOSE: underlying filter Close at [04] precedes retired interceptor CLOSE-END at [07]
OBSERVATION(C1 shutdown): INTERCEPTOR-CLOSED-BEFORE-FILTER-RELEASE: interceptor CLOSE-END [05] < filter Close [06]
exit=0
```

[evalon/grpc-go-xd-e14bf49d](https://github.com/kaitranntt-evals/grpc-go-xds-rds-interceptor-lifecycle-leak/tree/evalon/grpc-go-xd-e14bf49d) (`41969e0a5b1f`):

```console
  [03] TEST: >>> publishing RDS with F disabled on every route
  [05] FILTER F#1 CLOSED (underlying ServerFilter.Close ran) via (*refCountedServerFilter).Close < (*filterChain).constructUsableRouteConfiguration < (*listenerWrapper).handleRDSUpdate < (*rdsWatcher).ResourceChanged
  [06] INTERCEPTOR F#1/i1[gen1] CLOSE-START filterClosed=true via (*interceptorList).Close < (*usableRouteConfiguration).close < (*filterChain).updateUsableRouteConfiguration < (*listenerWrapper).handleRDSUpdate < (*rdsWatcher).ResourceChanged
  [08] INTERCEPTOR F#1/i1[gen1] CLOSE-END filterClosed=true
OBSERVATION(C1/C9 replacement): FILTER-RELEASED-BEFORE-INTERCEPTOR-CLOSE: underlying filter Close at [05] precedes retired interceptor CLOSE-END at [08]
OBSERVATION(C1 shutdown): INTERCEPTOR-CLOSED-BEFORE-FILTER-RELEASE: interceptor CLOSE-END [05] < filter Close [06]
exit=0
```

[evalon/grpc-go-xd-e1dc717f](https://github.com/kaitranntt-evals/grpc-go-xds-rds-interceptor-lifecycle-leak/tree/evalon/grpc-go-xd-e1dc717f) (`f2f6b0d2a51d`):

```console
  [03] TEST: >>> publishing RDS with F disabled on every route
  [05] FILTER F#1 CLOSED (underlying ServerFilter.Close ran) via (*refCountedServerFilter).Close < (*filterChain).constructUsableRouteConfiguration < (*listenerWrapper).handleRDSUpdate < (*rdsWatcher).ResourceChanged
  [06] INTERCEPTOR F#1/i1[gen1] CLOSE-START filterClosed=true via (*interceptorList).Close < (*filterChain).storeUsableRouteConfiguration.(*usableRouteConfiguration).close.func1 < (*usableRouteConfiguration).close < (*filterChain).storeUsableRouteConfiguration < (*listenerWrapper).handleRDSUpdate
  [08] INTERCEPTOR F#1/i1[gen1] CLOSE-END filterClosed=true
OBSERVATION(C1/C9 replacement): FILTER-RELEASED-BEFORE-INTERCEPTOR-CLOSE: underlying filter Close at [05] precedes retired interceptor CLOSE-END at [08]
OBSERVATION(C1 shutdown): INTERCEPTOR-CLOSED-BEFORE-FILTER-RELEASE: interceptor CLOSE-END [05] < filter Close [06]
exit=0
```

[evalon/grpc-go-xd-2c8abc16](https://github.com/kaitranntt-evals/grpc-go-xds-rds-interceptor-lifecycle-leak/tree/evalon/grpc-go-xd-2c8abc16) (`cd3479254c21`):

```console
  [03] TEST: >>> publishing RDS with F disabled on every route
  [05] FILTER F#1 CLOSED (underlying ServerFilter.Close ran) via (*refCountedServerFilter).Close < (*filterChain).constructUsableRouteConfiguration < (*listenerWrapper).handleRDSUpdate < (*rdsWatcher).ResourceChanged
  [06] INTERCEPTOR F#1/i1[gen1] CLOSE-START filterClosed=true via (*interceptorList).Close < (*usableRouteConfiguration).close < (*filterChain).updateUsableRouteConfiguration < (*listenerWrapper).handleRDSUpdate < (*rdsWatcher).ResourceChanged
  [08] INTERCEPTOR F#1/i1[gen1] CLOSE-END filterClosed=true
OBSERVATION(C1/C9 replacement): FILTER-RELEASED-BEFORE-INTERCEPTOR-CLOSE: underlying filter Close at [05] precedes retired interceptor CLOSE-END at [08]
OBSERVATION(C1 shutdown): INTERCEPTOR-CLOSED-BEFORE-FILTER-RELEASE: interceptor CLOSE-END [05] < filter Close [06]
exit=0
```

[evalon/grpc-go-xd-43ae1ade](https://github.com/kaitranntt-evals/grpc-go-xds-rds-interceptor-lifecycle-leak/tree/evalon/grpc-go-xd-43ae1ade) (`63a7d9e80e57`):

```console
  [03] TEST: >>> publishing RDS with F disabled on every route
  [05] FILTER F#1 CLOSED (underlying ServerFilter.Close ran) via (*refCountedServerFilter).Close < (*filterChain).constructUsableRouteConfiguration < (*listenerWrapper).handleRDSUpdate < (*rdsWatcher).ResourceChanged
  [06] INTERCEPTOR F#1/i1[gen1] CLOSE-START filterClosed=true via (*interceptorList).Close < (*usableRouteConfiguration).close < (*filterChain).updateUsableRouteConfiguration < (*listenerWrapper).handleRDSUpdate < (*rdsWatcher).ResourceChanged
  [08] INTERCEPTOR F#1/i1[gen1] CLOSE-END filterClosed=true
OBSERVATION(C1/C9 replacement): FILTER-RELEASED-BEFORE-INTERCEPTOR-CLOSE: underlying filter Close at [05] precedes retired interceptor CLOSE-END at [08]
OBSERVATION(C1 shutdown): INTERCEPTOR-CLOSED-BEFORE-FILTER-RELEASE: interceptor CLOSE-END [05] < filter Close [06]
exit=0
```

[evalon/grpc-go-xd-c47f6009](https://github.com/kaitranntt-evals/grpc-go-xds-rds-interceptor-lifecycle-leak/tree/evalon/grpc-go-xd-c47f6009) (`cd1d4ee207d9`):

```console
  [03] TEST: >>> publishing RDS with F disabled on every route
  [05] FILTER F#1 CLOSED (underlying ServerFilter.Close ran) via (*refCountedServerFilter).Close < (*filterChain).constructUsableRouteConfiguration < (*listenerWrapper).handleRDSUpdate < (*rdsWatcher).ResourceChanged
  [06] INTERCEPTOR F#1/i1[gen1] CLOSE-START filterClosed=true via (*interceptorList).Close < (*usableRouteConfiguration).close < (*filterChain).storeUsableRouteConfiguration < (*listenerWrapper).handleRDSUpdate < (*rdsWatcher).ResourceChanged
  [08] INTERCEPTOR F#1/i1[gen1] CLOSE-END filterClosed=true
OBSERVATION(C1/C9 replacement): FILTER-RELEASED-BEFORE-INTERCEPTOR-CLOSE: underlying filter Close at [05] precedes retired interceptor CLOSE-END at [08]
OBSERVATION(C1 shutdown): INTERCEPTOR-CLOSED-BEFORE-FILTER-RELEASE: interceptor CLOSE-END [05] < filter Close [06]
exit=0
```

[evalon/grpc-go-xd-52de0075](https://github.com/kaitranntt-evals/grpc-go-xds-rds-interceptor-lifecycle-leak/tree/evalon/grpc-go-xd-52de0075) (`dcd17cf7ae08`):

```console
  [03] TEST: >>> publishing RDS with F disabled on every route
  [05] FILTER F#1 CLOSED (underlying ServerFilter.Close ran) via (*refCountedServerFilter).Close < (*filterChain).constructUsableRouteConfiguration < (*listenerWrapper).handleRDSUpdate < (*rdsWatcher).ResourceChanged
  [06] INTERCEPTOR F#1/i1[gen1] CLOSE-START filterClosed=true via (*interceptorList).Close < (*usableRouteConfiguration).close < (*filterChain).updateUsableRouteConfiguration < (*listenerWrapper).handleRDSUpdate < (*rdsWatcher).ResourceChanged
  [08] INTERCEPTOR F#1/i1[gen1] CLOSE-END filterClosed=true
OBSERVATION(C1/C9 replacement): FILTER-RELEASED-BEFORE-INTERCEPTOR-CLOSE: underlying filter Close at [05] precedes retired interceptor CLOSE-END at [08]
OBSERVATION(C1 shutdown): INTERCEPTOR-CLOSED-BEFORE-FILTER-RELEASE: interceptor CLOSE-END [05] < filter Close [06]
exit=0
```

Reading: on all 13 branches the underlying `ServerFilter.Close` runs from `(*refCountedServerFilter).Close < (*filterChain).constructUsableRouteConfiguration`, i.e. while the replacement configuration is still being constructed, and only afterwards does the retired gen1 interceptor enter `Close` (`CLOSE-START filterClosed=true`). The retired configuration's last filter reference is therefore released before its interceptor has even started closing. The shutdown path is ordered correctly on all 13 (`INTERCEPTOR-CLOSED-BEFORE-FILTER-RELEASE`), so the defect is on the replacement path only.

Control, same probe and same command with `audited` instead of a branch id, on the audited branch [grpc-go-xds-rds-interceptor-lifecycle-leak-perfect](https://github.com/kaitranntt-evals/grpc-go/tree/grpc-go-xds-rds-interceptor-lifecycle-leak-perfect) (the ordering is the other way round there, so the probe is able to tell the difference):

```console
  [03] TEST: >>> publishing RDS with F disabled on every route
  [05] INTERCEPTOR F#1/i1[gen1] CLOSE-START filterClosed=false via (*interceptorList).Close < (*usableRouteConfiguration).stop < (*filterChain).applyConfiguration < (*filterChain).updateUsableRouteConfiguration < (*listenerWrapper).handleRDSUpdate
  [07] INTERCEPTOR F#1/i1[gen1] CLOSE-END filterClosed=false
  [08] FILTER F#1 CLOSED (underlying ServerFilter.Close ran) via (*refCountedServerFilter).Close < (*filterChain).applyConfiguration < (*filterChain).updateUsableRouteConfiguration < (*listenerWrapper).handleRDSUpdate < (*rdsWatcher).ResourceChanged
OBSERVATION(C1/C9 replacement): INTERCEPTOR-CLOSED-BEFORE-FILTER-RELEASE: retired interceptor CLOSE-END at [07] precedes underlying filter Close at [08]
OBSERVATION(C1 shutdown): INTERCEPTOR-CLOSED-BEFORE-FILTER-RELEASE: interceptor CLOSE-END [05] < filter Close [06]
```

Per-branch result: the release-before-close ordering was observed on every one of the 13 branches.

Impact reasoning. An interceptor is built from its `ServerFilter` (`BuildServerInterceptor`) and may use filter-owned state in its own `Close` (deregistering itself, flushing into a shared client, and so on). On these branches such an interceptor runs its `Close` against a filter whose `Close` has already returned. The early release only reaches zero, and so only becomes visible, when the replacement no longer references the filter at all, i.e. when the update disables the filter on every route of the filter chain; when the filter stays enabled the replacement configuration holds its own reference: in the same harness's `TestVerifyH_PausedRequestReplacement`, whose replacement keeps both probe filters enabled, no filter was closed on any of the 13 branches (0 `FILTER ... CLOSED` lines in that log section of each `verify/logs/A.<id>.out`). That makes it a silent ordering bug behind a less common configuration change. Whether any in-tree filter's interceptor actually touches its filter in `Close` was not exercised; the observation was made with the probe filter.

## C2

Claim: the added or changed tests contain lifecycle synchronization defects that permit premature closure assertions or failure paths without bounded, safe teardown. Four named parts; 16 target branches. "Added tests" below means the `test/xds` tests each branch added on top of the base commit `4ee6ac46fada` (computed by `added_tests` in `verify/repro/common.sh` from `git diff 4ee6ac46 HEAD -- 'test/xds/*_test.go'`).

### Part 1: closure assertion synchronization (controlled scheduling)

`c2_part1_delay.sh <id>` runs the branch's added tests twice with `-race`: unmodified ("baseline"), and with the test's own `trackingInterceptor.Close` made to take 300ms before it counts the interceptor as destroyed ("delayed"). Nothing else changes, in particular the server still closes every retired interceptor. A test that asserts on destruction counts right after it has seen traffic served by the replacement configuration then fails, because the assertion runs while the retired interceptor's `Close` is still in progress; a test that waits for closure completion still passes.

```sh
for b in e14bf49d 4e0dad18 df007aa8 ee3cc2af d33210d2 2bdc2970 d5ce5e46 e5dfd84c 2c8abc16 3d609e1d c47f6009 52de0075 55cd92cd 545b9364 afc3b15e c5d0b2f7; do verify/repro/c2_part1_delay.sh $b; done
verify/repro/c2_part1_delay.sh 3d609e1d every-second      # two filter chains: slow down only every second Close
CLOSE_DELAY_MS=50 verify/repro/c2_part1_delay.sh d5ce5e46  # shorter delay, see note below
```

[evalon/grpc-go-xd-e14bf49d](https://github.com/kaitranntt-evals/grpc-go-xds-rds-interceptor-lifecycle-leak/tree/evalon/grpc-go-xd-e14bf49d):

```console
branch=e14bf49d tests=ServerSideXDS_RouteConfigUpdates_ReleaseSupersededResources mutated-file=test/xds/xds_server_filter_state_retention_test.go
ok  	google.golang.org/grpc/test/xds	1.123s
baseline exit=0
    xds_server_rds_update_test.go:271: After RDS update #1: destroyed 0 interceptor instances, want: 1
    --- FAIL: Test/ServerSideXDS_RouteConfigUpdates_ReleaseSupersededResources (0.68s)
delayed exit=1
worktree dirty files after restore: 0
```

[evalon/grpc-go-xd-4e0dad18](https://github.com/kaitranntt-evals/grpc-go-xds-rds-interceptor-lifecycle-leak/tree/evalon/grpc-go-xd-4e0dad18):

```console
branch=4e0dad18 tests=ServerSideXDS_FilterStateRetention_AcrossRDSUpdates_ReleasesSupersededInterceptors mutated-file=test/xds/xds_server_filter_state_retention_test.go
ok  	google.golang.org/grpc/test/xds	1.173s
baseline exit=0
    xds_server_filter_state_retention_test.go:790: After 1 RDS updates: 2 interceptors created, 0 destroyed; got 2 live interceptors, want 1
    --- FAIL: Test/ServerSideXDS_FilterStateRetention_AcrossRDSUpdates_ReleasesSupersededInterceptors (0.63s)
delayed exit=1
worktree dirty files after restore: 0
```

[evalon/grpc-go-xd-df007aa8](https://github.com/kaitranntt-evals/grpc-go-xds-rds-interceptor-lifecycle-leak/tree/evalon/grpc-go-xd-df007aa8):

```console
branch=df007aa8 tests=ServerSideXDS_RDSUpdates_ReleaseSupersededInterceptors mutated-file=test/xds/xds_server_filter_state_retention_test.go
ok  	google.golang.org/grpc/test/xds	1.194s
baseline exit=0
    xds_server_rds_update_cleanup_test.go:253: After 1 RDS updates: 3 interceptors live (created: 3, destroyed: 0), want: 2
    --- FAIL: Test/ServerSideXDS_RDSUpdates_ReleaseSupersededInterceptors (1.28s)
delayed exit=1
worktree dirty files after restore: 0
```

[evalon/grpc-go-xd-ee3cc2af](https://github.com/kaitranntt-evals/grpc-go-xds-rds-interceptor-lifecycle-leak/tree/evalon/grpc-go-xd-ee3cc2af):

```console
branch=ee3cc2af tests=ServerSideXDS_FilterStateRetention_AcrossRouteConfigurationUpdates mutated-file=test/xds/xds_server_filter_state_retention_test.go
ok  	google.golang.org/grpc/test/xds	1.157s
baseline exit=0
    xds_server_filter_state_retention_test.go:778: After RouteConfiguration update 1: 3 interceptors created, 0 destroyed; got 3 live interceptors, want 2
    --- FAIL: Test/ServerSideXDS_FilterStateRetention_AcrossRouteConfigurationUpdates (1.28s)
delayed exit=1
worktree dirty files after restore: 0
```

[evalon/grpc-go-xd-d33210d2](https://github.com/kaitranntt-evals/grpc-go-xds-rds-interceptor-lifecycle-leak/tree/evalon/grpc-go-xd-d33210d2):

```console
branch=d33210d2 tests=ServerSideXDS_FilterStateRetention_RDSUpdatesReleaseInterceptors mutated-file=test/xds/xds_server_filter_state_retention_test.go
ok  	google.golang.org/grpc/test/xds	1.162s
baseline exit=0
    xds_server_filter_state_retention_test.go:779: After 1 RouteConfiguration updates, 2 interceptors are open (created: 2, destroyed: 0), want 1
    --- FAIL: Test/ServerSideXDS_FilterStateRetention_RDSUpdatesReleaseInterceptors (0.68s)
delayed exit=1
worktree dirty files after restore: 0
```

[evalon/grpc-go-xd-2bdc2970](https://github.com/kaitranntt-evals/grpc-go-xds-rds-interceptor-lifecycle-leak/tree/evalon/grpc-go-xd-2bdc2970):

```console
branch=2bdc2970 tests=ServerSideXDS_RouteConfigurationUpdates_ReleaseSupersededInterceptors mutated-file=test/xds/xds_server_filter_state_retention_test.go
ok  	google.golang.org/grpc/test/xds	1.188s
baseline exit=0
    xds_server_filter_state_retention_test.go:787: After 1 route configuration updates, 2 interceptors created and 0 destroyed; want exactly 1 live interceptor, got 2
    --- FAIL: Test/ServerSideXDS_RouteConfigurationUpdates_ReleaseSupersededInterceptors (0.64s)
delayed exit=1
worktree dirty files after restore: 0
```

[evalon/grpc-go-xd-d5ce5e46](https://github.com/kaitranntt-evals/grpc-go-xds-rds-interceptor-lifecycle-leak/tree/evalon/grpc-go-xd-d5ce5e46):

```console
branch=d5ce5e46 tests=ServerSideXDS_RouteConfigurationUpdates_ReleaseSupersededResources|ServerSideXDS_RouteConfigurationUpdate_InFlightRPCKeepsInterceptor mutated-file=test/xds/xds_server_filter_state_retention_test.go
ok  	google.golang.org/grpc/test/xds	1.156s
baseline exit=0
    xds_server_rds_update_test.go:235: Timeout waiting for interceptor counts; got (created: 36, destroyed: 33), want (created: 36, destroyed: 34)
    --- PASS: Test/ServerSideXDS_RouteConfigurationUpdate_InFlightRPCKeepsInterceptor (1.30s)
    --- FAIL: Test/ServerSideXDS_RouteConfigurationUpdates_ReleaseSupersededResources (10.88s)
delayed exit=1
worktree dirty files after restore: 0
```

Same branch with a 50ms delay. This branch's test polls for the expected counts (`waitForInterceptorCounts`) instead of asserting once; the 300ms failure above is that poll running out of its 10s budget because a few dozen serial closes at 300ms each no longer fit, not an assertion that fired early:

```console
branch=d5ce5e46 tests=ServerSideXDS_RouteConfigurationUpdates_ReleaseSupersededResources|ServerSideXDS_RouteConfigurationUpdate_InFlightRPCKeepsInterceptor mutated-file=test/xds/xds_server_filter_state_retention_test.go
ok  	google.golang.org/grpc/test/xds	1.197s
baseline exit=0
    --- PASS: Test/ServerSideXDS_RouteConfigurationUpdate_InFlightRPCKeepsInterceptor (0.29s)
    --- PASS: Test/ServerSideXDS_RouteConfigurationUpdates_ReleaseSupersededResources (2.19s)
ok  	google.golang.org/grpc/test/xds	3.515s
delayed exit=0
worktree dirty files after restore: 0
```

[evalon/grpc-go-xd-e5dfd84c](https://github.com/kaitranntt-evals/grpc-go-xds-rds-interceptor-lifecycle-leak/tree/evalon/grpc-go-xd-e5dfd84c):

```console
branch=e5dfd84c tests=ServerSideXDS_FilterStateRetention_AcrossRouteConfigUpdates mutated-file=test/xds/xds_server_filter_state_retention_test.go
ok  	google.golang.org/grpc/test/xds	1.111s
baseline exit=0
    xds_server_filter_state_retention_test.go:818: After 1 route config updates, destroyed 0 interceptor instances, want: 1
    --- FAIL: Test/ServerSideXDS_FilterStateRetention_AcrossRouteConfigUpdates (0.64s)
delayed exit=1
worktree dirty files after restore: 0
```

[evalon/grpc-go-xd-2c8abc16](https://github.com/kaitranntt-evals/grpc-go-xds-rds-interceptor-lifecycle-leak/tree/evalon/grpc-go-xd-2c8abc16):

```console
branch=2c8abc16 tests=ServerSideXDS_FilterStateRetention_AcrossUpdates_RouteConfigChange mutated-file=test/xds/xds_server_filter_state_retention_test.go
ok  	google.golang.org/grpc/test/xds	1.124s
baseline exit=0
    xds_server_filter_state_retention_test.go:835: After update 1: destroyed 0 interceptor instances, want: 1
    --- FAIL: Test/ServerSideXDS_FilterStateRetention_AcrossUpdates_RouteConfigChange (0.64s)
delayed exit=1
worktree dirty files after restore: 0
```

[evalon/grpc-go-xd-3d609e1d](https://github.com/kaitranntt-evals/grpc-go-xds-rds-interceptor-lifecycle-leak/tree/evalon/grpc-go-xd-3d609e1d):

```console
branch=3d609e1d tests=ServerSideXDS_RouteConfigUpdate_ReleasesSupersededResources mutated-file=test/xds/xds_server_filter_state_retention_test.go
ok  	google.golang.org/grpc/test/xds	1.117s
baseline exit=0
    xds_server_route_config_update_test.go:274: After route configuration update 2: created 3 interceptor instances, want: 4
    --- FAIL: Test/ServerSideXDS_RouteConfigUpdate_ReleasesSupersededResources (1.24s)
delayed exit=1
worktree dirty files after restore: 0
```

Same branch, `every-second` mode (with the plain delay the test trips over its creation-count assertion first, because the second filter chain has not built its interceptor yet when replacement traffic is seen on the first; slowing only every second `Close` reaches the destruction assertion):

```console
branch=3d609e1d tests=ServerSideXDS_RouteConfigUpdate_ReleasesSupersededResources mutated-file=test/xds/xds_server_filter_state_retention_test.go
ok  	google.golang.org/grpc/test/xds	1.161s
baseline exit=0
    xds_server_route_config_update_test.go:277: After route configuration update 2: destroyed 1 interceptor instances, want: 2
    --- FAIL: Test/ServerSideXDS_RouteConfigUpdate_ReleasesSupersededResources (0.68s)
delayed exit=1
worktree dirty files after restore: 0
```

[evalon/grpc-go-xd-c47f6009](https://github.com/kaitranntt-evals/grpc-go-xds-rds-interceptor-lifecycle-leak/tree/evalon/grpc-go-xd-c47f6009):

```console
branch=c47f6009 tests=ServerSideXDS_RouteConfigUpdate_ReleasesSupersededInterceptors mutated-file=test/xds/xds_server_filter_state_retention_test.go
ok  	google.golang.org/grpc/test/xds	1.127s
baseline exit=0
    xds_server_route_config_update_test.go:255: After update 1: destroyed 0 interceptor instances, want: 1
    --- FAIL: Test/ServerSideXDS_RouteConfigUpdate_ReleasesSupersededInterceptors (0.64s)
delayed exit=1
worktree dirty files after restore: 0
```

[evalon/grpc-go-xd-52de0075](https://github.com/kaitranntt-evals/grpc-go-xds-rds-interceptor-lifecycle-leak/tree/evalon/grpc-go-xd-52de0075):

```console
branch=52de0075 tests=ServerSideXDS_FilterStateRetention_AcrossUpdates_RouteConfigChange mutated-file=test/xds/xds_server_filter_state_retention_test.go
ok  	google.golang.org/grpc/test/xds	1.122s
baseline exit=0
    xds_server_filter_state_retention_test.go:826: After 1 RouteConfiguration updates: destroyed 0 interceptor instances, want: 1
    --- FAIL: Test/ServerSideXDS_FilterStateRetention_AcrossUpdates_RouteConfigChange (0.63s)
delayed exit=1
worktree dirty files after restore: 0
```

[evalon/grpc-go-xd-55cd92cd](https://github.com/kaitranntt-evals/grpc-go-xds-rds-interceptor-lifecycle-leak/tree/evalon/grpc-go-xd-55cd92cd):

```console
branch=55cd92cd tests=ServerSideXDS_FilterStateRetention_AcrossRDSUpdates mutated-file=test/xds/xds_server_filter_state_retention_test.go
ok  	google.golang.org/grpc/test/xds	1.108s
baseline exit=0
    --- PASS: Test/ServerSideXDS_FilterStateRetention_AcrossRDSUpdates (3.35s)
ok  	google.golang.org/grpc/test/xds	4.394s
delayed exit=0
worktree dirty files after restore: 0
```

[evalon/grpc-go-xd-545b9364](https://github.com/kaitranntt-evals/grpc-go-xds-rds-interceptor-lifecycle-leak/tree/evalon/grpc-go-xd-545b9364):

```console
branch=545b9364 tests=ServerSideXDS_FilterStateRetention_RDSUpdates mutated-file=test/xds/xds_server_filter_state_retention_test.go
ok  	google.golang.org/grpc/test/xds	1.154s
baseline exit=0
    --- PASS: Test/ServerSideXDS_FilterStateRetention_RDSUpdates (3.40s)
ok  	google.golang.org/grpc/test/xds	4.432s
delayed exit=0
worktree dirty files after restore: 0
```

[evalon/grpc-go-xd-afc3b15e](https://github.com/kaitranntt-evals/grpc-go-xds-rds-interceptor-lifecycle-leak/tree/evalon/grpc-go-xd-afc3b15e):

```console
branch=afc3b15e tests=ServerSideXDS_FilterStateRetention_RDSUpdates mutated-file=test/xds/xds_server_filter_state_retention_test.go
ok  	google.golang.org/grpc/test/xds	1.059s
baseline exit=0
    xds_server_filter_state_retention_test.go:273: After update 1, closed 0 interceptors, want 4
    --- FAIL: Test/ServerSideXDS_FilterStateRetention_RDSUpdates (2.47s)
delayed exit=1
worktree dirty files after restore: 0
```

[evalon/grpc-go-xd-c5d0b2f7](https://github.com/kaitranntt-evals/grpc-go-xds-rds-interceptor-lifecycle-leak/tree/evalon/grpc-go-xd-c5d0b2f7):

```console
branch=c5d0b2f7 tests=ServerSideXDS_FilterStateRetention_RDSUpdates mutated-file=test/xds/xds_server_filter_state_retention_test.go
ok  	google.golang.org/grpc/test/xds	1.062s
baseline exit=0
    --- PASS: Test/ServerSideXDS_FilterStateRetention_RDSUpdates (9.95s)
ok  	google.golang.org/grpc/test/xds	10.985s
delayed exit=0
worktree dirty files after restore: 0
```

The assertion that fires, with the synchronization that precedes it, for each branch where the delayed run failed (source of the branch's own test, last lines before the failing line reported above):

[evalon/grpc-go-xd-e14bf49d](https://github.com/kaitranntt-evals/grpc-go-xds-rds-interceptor-lifecycle-leak/tree/evalon/grpc-go-xd-e14bf49d), `test/xds/xds_server_rds_update_test.go` lines 262-271:

```go
		// The interceptor built for the superseded configuration must have
		// been closed before the new configuration became visible to RPCs.
		if got, want := filtersCreated.Load(), int32(1); got != want {
			t.Fatalf("After RDS update #%d: created %d filter instances, want: %d", i, got, want)
		}
		if got, want := interceptorsCreated.Load(), int32(i+1); got != want {
			t.Fatalf("After RDS update #%d: created %d interceptor instances, want: %d", i, got, want)
		}
		if got, want := interceptorsDestroyed.Load(), int32(i); got != want {
			t.Fatalf("After RDS update #%d: destroyed %d interceptor instances, want: %d", i, got, want)
```

[evalon/grpc-go-xd-4e0dad18](https://github.com/kaitranntt-evals/grpc-go-xds-rds-interceptor-lifecycle-leak/tree/evalon/grpc-go-xd-4e0dad18), `test/xds/xds_server_filter_state_retention_test.go` lines 781-790:

```go
		resources.Routes = append(clientRoutes, serverRouteConfig(path))
		if err := managementServer.Update(ctx, resources); err != nil {
			t.Fatal(err)
		}
		waitForPath(path)

		created, destroyed := interceptorsCreated.Load(), interceptorsDestroyed.Load()
		if got, want := created-destroyed, int32(1); got != want {
			t.Fatalf("After %d RDS updates: %d interceptors created, %d destroyed; got %d live interceptors, want %d", i, created, destroyed, got, want)
		}
```

[evalon/grpc-go-xd-df007aa8](https://github.com/kaitranntt-evals/grpc-go-xds-rds-interceptor-lifecycle-leak/tree/evalon/grpc-go-xd-df007aa8), `test/xds/xds_server_rds_update_cleanup_test.go` lines 244-253:

```go
		path := fmt.Sprintf("path-%d", i)
		resources.Routes[len(resources.Routes)-1] = serverRouteConfigWithFilterOverride(t, serverRouteName, filterName, testFilterTypeURL, path)
		if err := managementServer.Update(ctx, resources); err != nil {
			t.Fatal(err)
		}
		waitForPath(path)

		created, destroyed := interceptorsCreated.Load(), interceptorsDestroyed.Load()
		if live := created - destroyed; live != numFilterChains {
			t.Fatalf("After %d RDS updates: %d interceptors live (created: %d, destroyed: %d), want: %d", i, live, created, destroyed, numFilterChains)
```

[evalon/grpc-go-xd-ee3cc2af](https://github.com/kaitranntt-evals/grpc-go-xds-rds-interceptor-lifecycle-leak/tree/evalon/grpc-go-xd-ee3cc2af), `test/xds/xds_server_filter_state_retention_test.go` lines 769-778:

```go
			}
		}
		if ctx.Err() != nil {
			t.Fatalf("Timeout when waiting for RouteConfiguration %q to be applied: %v", path, ctx.Err())
		}

		created, destroyed := interceptorsCreated.Load(), interceptorsDestroyed.Load()
		if live := created - destroyed; live != wantLiveInterceptors {
			t.Fatalf("After RouteConfiguration update %d: %d interceptors created, %d destroyed; got %d live interceptors, want %d", i, created, destroyed, live, wantLiveInterceptors)
		}
```

[evalon/grpc-go-xd-d33210d2](https://github.com/kaitranntt-evals/grpc-go-xds-rds-interceptor-lifecycle-leak/tree/evalon/grpc-go-xd-d33210d2), `test/xds/xds_server_filter_state_retention_test.go` lines 770-779:

```go
		resources.Routes = append(clientRoutes, serverRouteConfig(path))
		if err := managementServer.Update(ctx, resources); err != nil {
			t.Fatal(err)
		}
		waitForPath(path)

		created, destroyed := interceptorsCreated.Load(), interceptorsDestroyed.Load()
		if live := created - destroyed; live != 1 {
			t.Fatalf("After %d RouteConfiguration updates, %d interceptors are open (created: %d, destroyed: %d), want 1", i, live, created, destroyed)
		}
```

[evalon/grpc-go-xd-2bdc2970](https://github.com/kaitranntt-evals/grpc-go-xds-rds-interceptor-lifecycle-leak/tree/evalon/grpc-go-xd-2bdc2970), `test/xds/xds_server_filter_state_retention_test.go` lines 778-787:

```go
		resources.Routes = append(clientRoutes, routeConfigWithPath(path))
		if err := managementServer.Update(ctx, resources); err != nil {
			t.Fatal(err)
		}
		waitForPath(path)

		created, destroyed := interceptorsCreated.Load(), interceptorsDestroyed.Load()
		if live := created - destroyed; live != 1 {
			t.Fatalf("After %d route configuration updates, %d interceptors created and %d destroyed; want exactly 1 live interceptor, got %d", i, created, destroyed, live)
		}
```

[evalon/grpc-go-xd-e5dfd84c](https://github.com/kaitranntt-evals/grpc-go-xds-rds-interceptor-lifecycle-leak/tree/evalon/grpc-go-xd-e5dfd84c), `test/xds/xds_server_filter_state_retention_test.go` lines 809-818:

```go
		if ctx.Err() != nil {
			t.Fatalf("Timeout when waiting for updated config to be applied: %v", ctx.Err())
		}

		if got, want := interceptorsCreated.Load(), int32(i+1); got != want {
			t.Fatalf("After %d route config updates, created %d interceptor instances, want: %d", i, got, want)
		}
		if got, want := interceptorsDestroyed.Load(), int32(i); got != want {
			t.Fatalf("After %d route config updates, destroyed %d interceptor instances, want: %d", i, got, want)
		}
```

[evalon/grpc-go-xd-2c8abc16](https://github.com/kaitranntt-evals/grpc-go-xds-rds-interceptor-lifecycle-leak/tree/evalon/grpc-go-xd-2c8abc16), `test/xds/xds_server_filter_state_retention_test.go` lines 826-835:

```go
		}
		if got, want := filtersDestroyed.Load(), int32(0); got != want {
			t.Fatalf("After update %d: destroyed %d filter instances, want: %d", i, got, want)
		}
		if got, want := interceptorsCreated.Load(), int32(1+i); got != want {
			t.Fatalf("After update %d: created %d interceptor instances, want: %d", i, got, want)
		}
		if got, want := interceptorsDestroyed.Load(), int32(i); got != want {
			t.Fatalf("After update %d: destroyed %d interceptor instances, want: %d", i, got, want)
		}
```

[evalon/grpc-go-xd-3d609e1d](https://github.com/kaitranntt-evals/grpc-go-xds-rds-interceptor-lifecycle-leak/tree/evalon/grpc-go-xd-3d609e1d), `test/xds/xds_server_route_config_update_test.go` lines 268-277:

```go
			t.Fatalf("After route configuration update %d: created %d filter instances, want: %d", i, got, want)
		}
		if got, want := filtersDestroyed.Load(), int32(0); got != want {
			t.Fatalf("After route configuration update %d: destroyed %d filter instances, want: %d", i, got, want)
		}
		if got, want := interceptorsCreated.Load(), int32(numFilterChains*i); got != want {
			t.Fatalf("After route configuration update %d: created %d interceptor instances, want: %d", i, got, want)
		}
		if got, want := interceptorsDestroyed.Load(), int32(numFilterChains*(i-1)); got != want {
			t.Fatalf("After route configuration update %d: destroyed %d interceptor instances, want: %d", i, got, want)
```

[evalon/grpc-go-xd-c47f6009](https://github.com/kaitranntt-evals/grpc-go-xds-rds-interceptor-lifecycle-leak/tree/evalon/grpc-go-xd-c47f6009), `test/xds/xds_server_route_config_update_test.go` lines 246-255:

```go
		}
		if ctx.Err() != nil {
			t.Fatalf("Timeout when waiting for updated config %q to be applied: %v", wantPath, ctx.Err())
		}

		if got, want := interceptorsCreated.Load(), int32(i+1); got != want {
			t.Fatalf("After update %d: created %d interceptor instances, want: %d", i, got, want)
		}
		if got, want := interceptorsDestroyed.Load(), int32(i); got != want {
			t.Fatalf("After update %d: destroyed %d interceptor instances, want: %d", i, got, want)
```

[evalon/grpc-go-xd-52de0075](https://github.com/kaitranntt-evals/grpc-go-xds-rds-interceptor-lifecycle-leak/tree/evalon/grpc-go-xd-52de0075), `test/xds/xds_server_filter_state_retention_test.go` lines 817-826:

```go
		}
		if got, want := filtersDestroyed.Load(), int32(0); got != want {
			t.Fatalf("After %d RouteConfiguration updates: destroyed %d filter instances, want: %d", i, got, want)
		}
		if got, want := interceptorsCreated.Load(), int32(i+1); got != want {
			t.Fatalf("After %d RouteConfiguration updates: created %d interceptor instances, want: %d", i, got, want)
		}
		if got, want := interceptorsDestroyed.Load(), int32(i); got != want {
			t.Fatalf("After %d RouteConfiguration updates: destroyed %d interceptor instances, want: %d", i, got, want)
		}
```

[evalon/grpc-go-xd-afc3b15e](https://github.com/kaitranntt-evals/grpc-go-xds-rds-interceptor-lifecycle-leak/tree/evalon/grpc-go-xd-afc3b15e), `test/xds/xds_server_filter_state_retention_test.go` lines 264-273:

```go
			if got == path {
				break
			}
		}
		if got, want := interceptorsCreated.Load(), int32((i+1)*interceptorsPerConfig); got != want {
			t.Fatalf("After update %d, created %d interceptors, want %d", i, got, want)
		}
		if got, want := interceptorsDestroyed.Load(), int32(i*interceptorsPerConfig); got != want {
			t.Fatalf("After update %d, closed %d interceptors, want %d", i, got, want)
		}
```

Part 1 result per branch: the destruction assertion was reached while retired-interceptor closure was unfinished on [evalon/grpc-go-xd-e14bf49d](https://github.com/kaitranntt-evals/grpc-go-xds-rds-interceptor-lifecycle-leak/tree/evalon/grpc-go-xd-e14bf49d), [evalon/grpc-go-xd-4e0dad18](https://github.com/kaitranntt-evals/grpc-go-xds-rds-interceptor-lifecycle-leak/tree/evalon/grpc-go-xd-4e0dad18), [evalon/grpc-go-xd-df007aa8](https://github.com/kaitranntt-evals/grpc-go-xds-rds-interceptor-lifecycle-leak/tree/evalon/grpc-go-xd-df007aa8), [evalon/grpc-go-xd-ee3cc2af](https://github.com/kaitranntt-evals/grpc-go-xds-rds-interceptor-lifecycle-leak/tree/evalon/grpc-go-xd-ee3cc2af), [evalon/grpc-go-xd-d33210d2](https://github.com/kaitranntt-evals/grpc-go-xds-rds-interceptor-lifecycle-leak/tree/evalon/grpc-go-xd-d33210d2), [evalon/grpc-go-xd-2bdc2970](https://github.com/kaitranntt-evals/grpc-go-xds-rds-interceptor-lifecycle-leak/tree/evalon/grpc-go-xd-2bdc2970), [evalon/grpc-go-xd-e5dfd84c](https://github.com/kaitranntt-evals/grpc-go-xds-rds-interceptor-lifecycle-leak/tree/evalon/grpc-go-xd-e5dfd84c), [evalon/grpc-go-xd-2c8abc16](https://github.com/kaitranntt-evals/grpc-go-xds-rds-interceptor-lifecycle-leak/tree/evalon/grpc-go-xd-2c8abc16), [evalon/grpc-go-xd-3d609e1d](https://github.com/kaitranntt-evals/grpc-go-xds-rds-interceptor-lifecycle-leak/tree/evalon/grpc-go-xd-3d609e1d), [evalon/grpc-go-xd-c47f6009](https://github.com/kaitranntt-evals/grpc-go-xds-rds-interceptor-lifecycle-leak/tree/evalon/grpc-go-xd-c47f6009), [evalon/grpc-go-xd-52de0075](https://github.com/kaitranntt-evals/grpc-go-xds-rds-interceptor-lifecycle-leak/tree/evalon/grpc-go-xd-52de0075), [evalon/grpc-go-xd-afc3b15e](https://github.com/kaitranntt-evals/grpc-go-xds-rds-interceptor-lifecycle-leak/tree/evalon/grpc-go-xd-afc3b15e) (12 branches; baseline passes, delayed run fails at the destruction/live-count assertion). It was not reachable on [evalon/grpc-go-xd-55cd92cd](https://github.com/kaitranntt-evals/grpc-go-xds-rds-interceptor-lifecycle-leak/tree/evalon/grpc-go-xd-55cd92cd), [evalon/grpc-go-xd-545b9364](https://github.com/kaitranntt-evals/grpc-go-xds-rds-interceptor-lifecycle-leak/tree/evalon/grpc-go-xd-545b9364), [evalon/grpc-go-xd-c5d0b2f7](https://github.com/kaitranntt-evals/grpc-go-xds-rds-interceptor-lifecycle-leak/tree/evalon/grpc-go-xd-c5d0b2f7) (delayed run passes) and [evalon/grpc-go-xd-d5ce5e46](https://github.com/kaitranntt-evals/grpc-go-xds-rds-interceptor-lifecycle-leak/tree/evalon/grpc-go-xd-d5ce5e46) (passes at 50ms, polls for the counts).

### Part 2: bounded callback waits (injected failure)

`c2_part2_nonotify.sh <id> [control test]` temporarily changes one line of `internal/xds/server/routing.go` so that routing no longer calls the interceptor (`if err := rwi.interceptor.AllowRPC(ctx); err != nil` becomes `if err := error(nil); err != nil`). RPCs still succeed, but the notification the tests wait for (interceptor invoked / path observed) never arrives. The added tests are run with the test binary's timeout set to 35s; a test whose receive has a local deadline fails by itself after its 10s test context, a test with a bare receive is only ended by the 35s binary timeout. `routing.go` is restored afterwards.

```sh
for b in e14bf49d 4e0dad18 df007aa8 ee3cc2af d33210d2 2bdc2970 d5ce5e46 e5dfd84c 2c8abc16 3d609e1d c47f6009 52de0075 545b9364 afc3b15e; do verify/repro/c2_part2_nonotify.sh $b; done
for b in 55cd92cd c5d0b2f7; do verify/repro/c2_part2_nonotify.sh $b ServerSideXDS_FilterStateRetention_AcrossUpdates_FilterConfigChange; done
```

[evalon/grpc-go-xd-55cd92cd](https://github.com/kaitranntt-evals/grpc-go-xds-rds-interceptor-lifecycle-leak/tree/evalon/grpc-go-xd-55cd92cd) (the second half is a pre-existing test of the same file under the same mutation, as a control):

```console
branch=55cd92cd tests=ServerSideXDS_FilterStateRetention_AcrossRDSUpdates
panic: test timed out after 35s
exit=1
worktree dirty files after restore: 0
leaked goroutines reported: 0
google.golang.org/grpc/test/xds_test.s.TestServerSideXDS_FilterStateRetention_AcrossRDSUpdates({{}}, 0xc000002000)
	/home/ubuntu/wt/55cd92cd/test/xds/xds_server_filter_state_retention_test.go:236 +0x1025
control=ServerSideXDS_FilterStateRetention_AcrossUpdates_FilterConfigChange
    xds_server_filter_state_retention_test.go:433: Timeout waiting for interceptor to be invoked
    --- FAIL: Test/ServerSideXDS_FilterStateRetention_AcrossUpdates_FilterConfigChange (10.10s)
exit=1
```

[evalon/grpc-go-xd-c5d0b2f7](https://github.com/kaitranntt-evals/grpc-go-xds-rds-interceptor-lifecycle-leak/tree/evalon/grpc-go-xd-c5d0b2f7) (the second half is a pre-existing test of the same file under the same mutation, as a control):

```console
branch=c5d0b2f7 tests=ServerSideXDS_FilterStateRetention_RDSUpdates
panic: test timed out after 35s
exit=1
worktree dirty files after restore: 0
leaked goroutines reported: 0
google.golang.org/grpc/test/xds_test.s.TestServerSideXDS_FilterStateRetention_RDSUpdates({{}}, 0xc000602380)
	/home/ubuntu/wt/c5d0b2f7/test/xds/xds_server_filter_state_retention_test.go:254 +0x176d
control=ServerSideXDS_FilterStateRetention_AcrossUpdates_FilterConfigChange
    xds_server_filter_state_retention_test.go:440: Timeout waiting for interceptor to be invoked
    --- FAIL: Test/ServerSideXDS_FilterStateRetention_AcrossUpdates_FilterConfigChange (10.01s)
exit=1
```

The receives the two timed-out test goroutines are parked on (branch source):

[evalon/grpc-go-xd-55cd92cd](https://github.com/kaitranntt-evals/grpc-go-xds-rds-interceptor-lifecycle-leak/tree/evalon/grpc-go-xd-55cd92cd), `test/xds/xds_server_filter_state_retention_test.go` lines 233-238:

```go
	if _, err := client.EmptyCall(ctx, &testpb.Empty{}); err != nil {
		t.Fatalf("Initial EmptyCall() failed: %v", err)
	}
	if path := <-pathCh; path != "initial" {
		t.Fatalf("Initial RPC used configuration %q, want initial", path)
	}
```

[evalon/grpc-go-xd-c5d0b2f7](https://github.com/kaitranntt-evals/grpc-go-xds-rds-interceptor-lifecycle-leak/tree/evalon/grpc-go-xd-c5d0b2f7), `test/xds/xds_server_filter_state_retention_test.go` lines 250-257:

```go
		for {
			if _, err := client.EmptyCall(ctx, &testpb.Empty{}); err != nil {
				t.Fatalf("EmptyCall() during update %d failed: %v", update, err)
			}
			if got := <-pathCh; got == wantPath {
				break
			}
		}
```

The other 14 branches under the same mutation (every added test ends through its own timeout after about 10s):

```console
branch=e14bf49d tests=ServerSideXDS_RouteConfigUpdates_ReleaseSupersededResources
    xds_server_rds_update_test.go:218: Timeout waiting for interceptor to be invoked
    --- FAIL: Test/ServerSideXDS_RouteConfigUpdates_ReleaseSupersededResources (10.01s)
exit=1
worktree dirty files after restore: 0
leaked goroutines reported: 0
branch=4e0dad18 tests=ServerSideXDS_FilterStateRetention_AcrossRDSUpdates_ReleasesSupersededInterceptors
    xds_server_filter_state_retention_test.go:772: Timeout waiting for route configuration with path "path-0" to be applied
    --- FAIL: Test/ServerSideXDS_FilterStateRetention_AcrossRDSUpdates_ReleasesSupersededInterceptors (10.07s)
exit=1
worktree dirty files after restore: 0
leaked goroutines reported: 0
branch=df007aa8 tests=ServerSideXDS_RDSUpdates_ReleaseSupersededInterceptors
    xds_server_rds_update_cleanup_test.go:230: Timeout waiting for route configuration with path "path-0" to be applied
    --- FAIL: Test/ServerSideXDS_RDSUpdates_ReleaseSupersededInterceptors (10.06s)
exit=1
worktree dirty files after restore: 0
leaked goroutines reported: 0
branch=ee3cc2af tests=ServerSideXDS_FilterStateRetention_AcrossRouteConfigurationUpdates
    xds_server_filter_state_retention_test.go:768: Timeout waiting for interceptor to be invoked
    --- FAIL: Test/ServerSideXDS_FilterStateRetention_AcrossRouteConfigurationUpdates (10.00s)
exit=1
worktree dirty files after restore: 0
leaked goroutines reported: 0
branch=d33210d2 tests=ServerSideXDS_FilterStateRetention_RDSUpdatesReleaseInterceptors
    xds_server_filter_state_retention_test.go:763: Timeout waiting for RPCs to be processed with path "path-0"
    --- FAIL: Test/ServerSideXDS_FilterStateRetention_RDSUpdatesReleaseInterceptors (10.10s)
exit=1
worktree dirty files after restore: 0
leaked goroutines reported: 0
branch=2bdc2970 tests=ServerSideXDS_RouteConfigurationUpdates_ReleaseSupersededInterceptors
    xds_server_filter_state_retention_test.go:770: Timeout waiting for route configuration with path "path-0" to be applied
    --- FAIL: Test/ServerSideXDS_RouteConfigurationUpdates_ReleaseSupersededInterceptors (10.05s)
exit=1
worktree dirty files after restore: 0
leaked goroutines reported: 0
branch=d5ce5e46 tests=ServerSideXDS_RouteConfigurationUpdates_ReleaseSupersededResources|ServerSideXDS_RouteConfigurationUpdate_InFlightRPCKeepsInterceptor
    xds_server_rds_update_test.go:313: Timeout waiting for the RPC to reach the interceptor
    xds_server_rds_update_test.go:217: Timeout waiting for interceptor to be invoked
    --- FAIL: Test/ServerSideXDS_RouteConfigurationUpdate_InFlightRPCKeepsInterceptor (10.09s)
    --- FAIL: Test/ServerSideXDS_RouteConfigurationUpdates_ReleaseSupersededResources (10.04s)
exit=1
worktree dirty files after restore: 0
leaked goroutines reported: 0
branch=e5dfd84c tests=ServerSideXDS_FilterStateRetention_AcrossRouteConfigUpdates
    xds_server_filter_state_retention_test.go:775: Timeout waiting for interceptor to be invoked
    --- FAIL: Test/ServerSideXDS_FilterStateRetention_AcrossRouteConfigUpdates (10.05s)
exit=1
worktree dirty files after restore: 0
leaked goroutines reported: 0
branch=2c8abc16 tests=ServerSideXDS_FilterStateRetention_AcrossUpdates_RouteConfigChange
    xds_server_filter_state_retention_test.go:781: Timeout waiting for interceptor to be invoked
    --- FAIL: Test/ServerSideXDS_FilterStateRetention_AcrossUpdates_RouteConfigChange (10.05s)
exit=1
worktree dirty files after restore: 0
leaked goroutines reported: 0
branch=3d609e1d tests=ServerSideXDS_RouteConfigUpdate_ReleasesSupersededResources
    xds_server_route_config_update_test.go:243: Timeout waiting for interceptor to be invoked with path "path-1"
    --- FAIL: Test/ServerSideXDS_RouteConfigUpdate_ReleasesSupersededResources (10.06s)
exit=1
worktree dirty files after restore: 0
leaked goroutines reported: 0
branch=c47f6009 tests=ServerSideXDS_RouteConfigUpdate_ReleasesSupersededInterceptors
    xds_server_route_config_update_test.go:206: Timeout waiting for interceptor to be invoked
    --- FAIL: Test/ServerSideXDS_RouteConfigUpdate_ReleasesSupersededInterceptors (10.05s)
exit=1
worktree dirty files after restore: 0
leaked goroutines reported: 0
branch=52de0075 tests=ServerSideXDS_FilterStateRetention_AcrossUpdates_RouteConfigChange
    xds_server_filter_state_retention_test.go:772: Timeout waiting for interceptor to be invoked
    --- FAIL: Test/ServerSideXDS_FilterStateRetention_AcrossUpdates_RouteConfigChange (10.06s)
exit=1
worktree dirty files after restore: 0
leaked goroutines reported: 0
branch=545b9364 tests=ServerSideXDS_FilterStateRetention_RDSUpdates
    xds_server_filter_state_retention_test.go:267: Timeout waiting for streaming RPC to reach interceptor
    --- FAIL: Test/ServerSideXDS_FilterStateRetention_RDSUpdates (10.00s)
exit=1
worktree dirty files after restore: 0
leaked goroutines reported: 0
branch=afc3b15e tests=ServerSideXDS_FilterStateRetention_RDSUpdates
    xds_server_filter_state_retention_test.go:262: Timeout waiting for interceptor to be invoked
    --- FAIL: Test/ServerSideXDS_FilterStateRetention_RDSUpdates (10.09s)
exit=1
worktree dirty files after restore: 0
leaked goroutines reported: 0
```

Part 2 result per branch: an unbounded receive (test only ended by the binary timeout, goroutine parked on a bare `<-pathCh`) on [evalon/grpc-go-xd-55cd92cd](https://github.com/kaitranntt-evals/grpc-go-xds-rds-interceptor-lifecycle-leak/tree/evalon/grpc-go-xd-55cd92cd) and [evalon/grpc-go-xd-c5d0b2f7](https://github.com/kaitranntt-evals/grpc-go-xds-rds-interceptor-lifecycle-leak/tree/evalon/grpc-go-xd-c5d0b2f7); bounded on the other 14 branches.

### Part 3: failure teardown synchronization (injected failure)

Exercised on [evalon/grpc-go-xd-d5ce5e46](https://github.com/kaitranntt-evals/grpc-go-xds-rds-interceptor-lifecycle-leak/tree/evalon/grpc-go-xd-d5ce5e46), whose added test holds a server-side RPC behind a test-controlled barrier (`blockingInterceptor.AllowRPC` blocks on `releaseCh`, which the test closes only on its success path, `test/xds/xds_server_rds_update_test.go:343`). `c2_part3_barrier.sh` injects a `t.Fatal` at the point where that RPC is parked (directly after `env.waitForInterceptorCounts(ctx, t, 2*perConfig, perConfig-1)`, the same place as the test's own `t.Fatalf("Destroyed %d interceptor instances while an RPC is using one of them, want %d", ...)`), and wraps the registered `stopServer` cleanup so that its duration is logged.

```sh
verify/repro/c2_part3_barrier.sh
```

```console
mutation lines applied: 4
    xds_server_rds_update_test.go:336: VERIFY-MUTATION: injected failure while the RPC is parked behind releaseCh
    xds_server_rds_update_test.go:113: VERIFY-TIMING: stopServer returned after 0s
    grpctest.go:45: Leaked goroutine: goroutine 44 [chan receive]:
        google.golang.org/grpc/test/xds_test.(*blockingInterceptor).AllowRPC(0xc000627038, {0x16dfe18, 0xc000740780})
        	/home/ubuntu/wt/d5ce5e46/test/xds/xds_server_rds_update_test.go:295 +0x45
    grpctest.go:77: Goroutine leak check disabled for future tests
--- FAIL: Test (10.04s)
    --- FAIL: Test/ServerSideXDS_RouteConfigurationUpdate_InFlightRPCKeepsInterceptor (10.03s)
exit=1
worktree dirty files after restore: 0
```

The barrier and the interceptor that waits on it (branch source, `test/xds/xds_server_rds_update_test.go` lines 283-287; the stack above says line 295 because the probe added 10 lines above it):

```go
func (i *blockingInterceptor) AllowRPC(ctx context.Context) error {
	i.parent.enteredCh <- struct{}{}
	<-i.parent.releaseCh
	return i.ServerInterceptor.AllowRPC(ctx)
}
```

Part 3 result on [evalon/grpc-go-xd-d5ce5e46](https://github.com/kaitranntt-evals/grpc-go-xds-rds-interceptor-lifecycle-leak/tree/evalon/grpc-go-xd-d5ce5e46): the failure path leaves `releaseCh` unreleased, and nothing else releases the RPC (the interceptor ignores its context). The registered `stopServer` cleanup itself does not wait (`stopServer returned after 0s`). What waits is the suite teardown: grpctest's leak check polls for the goroutine parked behind the barrier for its full 10s window (test duration 10.03s), then reports `Leaked goroutine ... (*blockingInterceptor).AllowRPC` and prints `Goroutine leak check disabled for future tests`. So the wait is bounded, but the teardown is not clean: a second error is reported for the same failure, the handler goroutine stays parked for the rest of the test binary, and leak checking is switched off for every later test in the package. Not exercised on the other 15 branches: a search of each branch's added `test/xds` test code for a receive from a release/block/unblock/proceed/resume channel finds one only on this branch, so there was no barrier to inject a failure behind.

```sh
for b in e14bf49d 4e0dad18 df007aa8 ee3cc2af d33210d2 2bdc2970 d5ce5e46 e5dfd84c 2c8abc16 3d609e1d c47f6009 52de0075 55cd92cd 545b9364 afc3b15e c5d0b2f7; do echo "$b $(git -C ~/wt/$b diff 4ee6ac46 HEAD -- 'test/xds/*_test.go' | grep -cE '^\+.*(<-[a-zA-Z.]*(release|block|unblock|proceed|resume)[A-Za-z]*)')"; done | paste -sd' '
```

```console
e14bf49d 0 4e0dad18 0 df007aa8 0 ee3cc2af 0 d33210d2 0 2bdc2970 0 d5ce5e46 1 e5dfd84c 0 2c8abc16 0 3d609e1d 0 c47f6009 0 52de0075 0 55cd92cd 0 545b9364 0 afc3b15e 0 c5d0b2f7 0
```

### Part 4: resource cleanup registration (injected failure)

Exercised on [evalon/grpc-go-xd-545b9364](https://github.com/kaitranntt-evals/grpc-go-xds-rds-interceptor-lifecycle-leak/tree/evalon/grpc-go-xd-545b9364). In its added test the server is created with `xds.NewGRPCServer(...)` at line 187, two fatal checks follow (lines 193 and 197), and the server's cleanup is only registered at line 216 (`defer stopServer()`):

```go
	server, err := xds.NewGRPCServer(xds.BootstrapContentsForTesting(bootstrapContents), xds.ServingModeCallback(func(_ net.Addr, args xds.ServingModeChangeArgs) {
		if args.Mode == connectivity.ServingModeServing {
			close(servingCh)
		}
	}))
	if err != nil {
		t.Fatal(err)
	}
	lis, err := testutils.LocalTCPListener()
	if err != nil {
		t.Fatal(err)
	}
	stubserver.StartTestService(t, &stubserver.StubServer{
		S: server, Listener: lis,
		// ... lines 201-213 (the stub handlers) omitted ...
	})
	stopServer := server.Stop
	defer stopServer()
```

`c2_part4_cleanup.sh` makes the `LocalTCPListener` error check at line 196 fire (mode `inject`), and repeats the same injected failure with `defer server.Stop()` added directly after the server is created (mode `control`).

```sh
verify/repro/c2_part4_cleanup.sh
```

```console
mode=inject mutation lines applied: 1
    xds_server_filter_state_retention_test.go:201: VERIFY-MUTATION: injected LocalTCPListener failure
    grpctest.go:45: Leaked goroutine: goroutine 21 [chan receive]:
    grpctest.go:45: Leaked goroutine: goroutine 22 [chan receive]:
    grpctest.go:45: Found 1 leaked async reporters:
        --- Leaked Async Reporter Registration ---
--- FAIL: Test (10.05s)
    --- FAIL: Test/ServerSideXDS_FilterStateRetention_RDSUpdates (10.05s)
exit=1
leaked goroutines reported: 2
mode=control mutation lines applied: 2
    xds_server_filter_state_retention_test.go:202: VERIFY-MUTATION: injected LocalTCPListener failure
--- FAIL: Test (0.00s)
    --- FAIL: Test/ServerSideXDS_FilterStateRetention_RDSUpdates (0.00s)
exit=1
leaked goroutines reported: 0
worktree dirty files after restore: 0
```

The two leaked goroutines in mode `inject` (from `verify/logs/c2p4.545b9364.inject.log`):

```console
    grpctest.go:45: Leaked goroutine: goroutine 21 [chan receive]:
        google.golang.org/grpc/internal/xds/clients/internal/syncutil.(*CallbackSerializer).run(0xc0005b2f60, {0x16de358, 0xc0005a79a0})
        	/home/ubuntu/wt/545b9364/internal/xds/clients/internal/syncutil/callback_serializer.go:88 +0xe5
        created by google.golang.org/grpc/internal/xds/clients/internal/syncutil.NewCallbackSerializer in goroutine 18
        	/home/ubuntu/wt/545b9364/internal/xds/clients/internal/syncutil/callback_serializer.go:52 +0x11a
    grpctest.go:45: Leaked goroutine: goroutine 22 [chan receive]:
        google.golang.org/grpc/internal/xds/clients/internal/syncutil.(*CallbackSerializer).run(0xc0005b2fa0, {0x16de358, 0xc0005a79f0})
        	/home/ubuntu/wt/545b9364/internal/xds/clients/internal/syncutil/callback_serializer.go:88 +0xe5
        created by google.golang.org/grpc/internal/xds/clients/internal/syncutil.NewCallbackSerializer in goroutine 18
        	/home/ubuntu/wt/545b9364/internal/xds/clients/internal/syncutil/callback_serializer.go:52 +0x11a
    grpctest.go:77: Goroutine leak check disabled for future tests
```

Part 4 result on [evalon/grpc-go-xd-545b9364](https://github.com/kaitranntt-evals/grpc-go-xds-rds-interceptor-lifecycle-leak/tree/evalon/grpc-go-xd-545b9364): a fatal exit between server creation and `defer stopServer()` leaves the xDS server's goroutines running (2 leaked goroutines plus a leaked async metrics reporter, leak check then disabled for later tests); with the cleanup registered right after creation the same failure leaks nothing. Not exercised on the other 15 branches.

### Per-branch summary

| branch | part 1 (premature closure assertion) | part 2 (unbounded receive) | part 3 (barrier left on failure) | part 4 (cleanup registered late) |
|---|---|---|---|---|
| [evalon/grpc-go-xd-e14bf49d](https://github.com/kaitranntt-evals/grpc-go-xds-rds-interceptor-lifecycle-leak/tree/evalon/grpc-go-xd-e14bf49d) | observed | not observed | not exercised | not exercised |
| [evalon/grpc-go-xd-4e0dad18](https://github.com/kaitranntt-evals/grpc-go-xds-rds-interceptor-lifecycle-leak/tree/evalon/grpc-go-xd-4e0dad18) | observed | not observed | not exercised | not exercised |
| [evalon/grpc-go-xd-df007aa8](https://github.com/kaitranntt-evals/grpc-go-xds-rds-interceptor-lifecycle-leak/tree/evalon/grpc-go-xd-df007aa8) | observed | not observed | not exercised | not exercised |
| [evalon/grpc-go-xd-ee3cc2af](https://github.com/kaitranntt-evals/grpc-go-xds-rds-interceptor-lifecycle-leak/tree/evalon/grpc-go-xd-ee3cc2af) | observed | not observed | not exercised | not exercised |
| [evalon/grpc-go-xd-d33210d2](https://github.com/kaitranntt-evals/grpc-go-xds-rds-interceptor-lifecycle-leak/tree/evalon/grpc-go-xd-d33210d2) | observed | not observed | not exercised | not exercised |
| [evalon/grpc-go-xd-2bdc2970](https://github.com/kaitranntt-evals/grpc-go-xds-rds-interceptor-lifecycle-leak/tree/evalon/grpc-go-xd-2bdc2970) | observed | not observed | not exercised | not exercised |
| [evalon/grpc-go-xd-d5ce5e46](https://github.com/kaitranntt-evals/grpc-go-xds-rds-interceptor-lifecycle-leak/tree/evalon/grpc-go-xd-d5ce5e46) | not observed | not observed | observed | not exercised |
| [evalon/grpc-go-xd-e5dfd84c](https://github.com/kaitranntt-evals/grpc-go-xds-rds-interceptor-lifecycle-leak/tree/evalon/grpc-go-xd-e5dfd84c) | observed | not observed | not exercised | not exercised |
| [evalon/grpc-go-xd-2c8abc16](https://github.com/kaitranntt-evals/grpc-go-xds-rds-interceptor-lifecycle-leak/tree/evalon/grpc-go-xd-2c8abc16) | observed | not observed | not exercised | not exercised |
| [evalon/grpc-go-xd-3d609e1d](https://github.com/kaitranntt-evals/grpc-go-xds-rds-interceptor-lifecycle-leak/tree/evalon/grpc-go-xd-3d609e1d) | observed | not observed | not exercised | not exercised |
| [evalon/grpc-go-xd-c47f6009](https://github.com/kaitranntt-evals/grpc-go-xds-rds-interceptor-lifecycle-leak/tree/evalon/grpc-go-xd-c47f6009) | observed | not observed | not exercised | not exercised |
| [evalon/grpc-go-xd-52de0075](https://github.com/kaitranntt-evals/grpc-go-xds-rds-interceptor-lifecycle-leak/tree/evalon/grpc-go-xd-52de0075) | observed | not observed | not exercised | not exercised |
| [evalon/grpc-go-xd-55cd92cd](https://github.com/kaitranntt-evals/grpc-go-xds-rds-interceptor-lifecycle-leak/tree/evalon/grpc-go-xd-55cd92cd) | not observed | observed | not exercised | not exercised |
| [evalon/grpc-go-xd-545b9364](https://github.com/kaitranntt-evals/grpc-go-xds-rds-interceptor-lifecycle-leak/tree/evalon/grpc-go-xd-545b9364) | not observed | not observed | not exercised | observed |
| [evalon/grpc-go-xd-afc3b15e](https://github.com/kaitranntt-evals/grpc-go-xds-rds-interceptor-lifecycle-leak/tree/evalon/grpc-go-xd-afc3b15e) | observed | not observed | not exercised | not exercised |
| [evalon/grpc-go-xd-c5d0b2f7](https://github.com/kaitranntt-evals/grpc-go-xds-rds-interceptor-lifecycle-leak/tree/evalon/grpc-go-xd-c5d0b2f7) | not observed | observed | not exercised | not exercised |


Every one of the 16 branches shows at least one of the four defects.

Impact reasoning. These are defects in the branches' regression tests, not in the server. Part 1 makes the regression test timing-dependent: it passes today only because the server happens to finish closing the retired interceptor before the client's next RPC is observed, and it fails spuriously as soon as an interceptor's `Close` is slow or the scheduler is unlucky (the window in an unmodified run is small; the baseline runs above all passed). Part 2 turns a missing notification, which is exactly what a regression in this area produces, into a hang until the binary timeout instead of a targeted failure message. Parts 3 and 4 only matter once the test is already failing: they add leaked goroutines, a second error, and disabled leak checking for the rest of the package run.

## C3

Claim: after an RDS error retires a previously successful route configuration, the underlying server filter instances associated with that configuration remain live while the server serves the error state. Targets: [evalon/grpc-go-xd-dc4ed267](https://github.com/kaitranntt-evals/grpc-go-xds-rds-interceptor-lifecycle-leak/tree/evalon/grpc-go-xd-dc4ed267) and [evalon/grpc-go-xd-20c60584](https://github.com/kaitranntt-evals/grpc-go-xds-rds-interceptor-lifecycle-leak/tree/evalon/grpc-go-xd-20c60584).

Probe. `verify/repro/verify_c3_rds_error_test.go` (package `server`) constructs a `listenerWrapper` around a filter chain manager with one filter chain that references an RDS route name and lists one probe HTTP filter, then drives it through the same entry point the RDS watcher uses (`handleRDSUpdate`): successful RouteConfiguration, then a resource error (`rdsWatcherUpdate` with only `err` set), then a successful recovery, then an error again, then `Close()` of the listener. After each step it prints how many times the underlying `ServerFilter.Close` ran, how many interceptors were built and closed, and the listener-level filter cache entry with its reference count; it also routes one RPC through `RouteAndProcess` to show what the server is serving.

```sh
for b in dc4ed267 20c60584; do
  verify/repro/run_probe.sh $b internal/xds/server '^Test$/^VerifyC3_' verify_c3_rds_error_test.go
done
```

[evalon/grpc-go-xd-dc4ed267](https://github.com/kaitranntt-evals/grpc-go-xds-rds-interceptor-lifecycle-leak/tree/evalon/grpc-go-xd-dc4ed267) (`560ce6bc2276`):

```console
C3 RouteAndProcess after successful RDS: err=<nil>
C3 after successful RDS:        filter#1 underlyingCloseCalls=0 interceptorsBuilt=1 interceptorsClosed=0 | LDS cache entry present=true refCnt=2
C3 RouteAndProcess while in RDS error state: err=rpc error: code = Unavailable desc = error from xDS configuration for matched route configuration: verify: route resource removed
C3 while serving RDS error:     filter#1 underlyingCloseCalls=0 interceptorsBuilt=1 interceptorsClosed=1 | LDS cache entry present=true refCnt=1
C3 OBSERVATION: FILTER-LIVE-DURING-ERROR-STATE: retired config's interceptors closed=1/1 but underlying ServerFilter.Close calls=0
C3 RouteAndProcess after recovery RDS: err=<nil>
C3 after recovery RDS:          filter#1 underlyingCloseCalls=0 interceptorsBuilt=2 interceptorsClosed=1 | LDS cache entry present=true refCnt=2
C3 error state again:           filter#1 underlyingCloseCalls=0 interceptorsBuilt=2 interceptorsClosed=2 | LDS cache entry present=true refCnt=1
C3 after listener Close:        filter#1 underlyingCloseCalls=1 interceptorsBuilt=2 interceptorsClosed=2 | LDS cache entry present=false refCnt=-1
--- PASS: Test (0.00s)
    --- PASS: Test/VerifyC3_RDSErrorFilterLiveness (0.00s)
ok  	google.golang.org/grpc/internal/xds/server	1.030s
exit=0
```

[evalon/grpc-go-xd-20c60584](https://github.com/kaitranntt-evals/grpc-go-xds-rds-interceptor-lifecycle-leak/tree/evalon/grpc-go-xd-20c60584) (`11f770c02b3d`):

```console
C3 RouteAndProcess after successful RDS: err=<nil>
C3 after successful RDS:        filter#1 underlyingCloseCalls=0 interceptorsBuilt=1 interceptorsClosed=0 | LDS cache entry present=true refCnt=2
C3 RouteAndProcess while in RDS error state: err=rpc error: code = Unavailable desc = error from xDS configuration for matched route configuration: verify: route resource removed
C3 while serving RDS error:     filter#1 underlyingCloseCalls=0 interceptorsBuilt=1 interceptorsClosed=1 | LDS cache entry present=true refCnt=1
C3 OBSERVATION: FILTER-LIVE-DURING-ERROR-STATE: retired config's interceptors closed=1/1 but underlying ServerFilter.Close calls=0
C3 RouteAndProcess after recovery RDS: err=<nil>
C3 after recovery RDS:          filter#1 underlyingCloseCalls=0 interceptorsBuilt=2 interceptorsClosed=1 | LDS cache entry present=true refCnt=2
C3 error state again:           filter#1 underlyingCloseCalls=0 interceptorsBuilt=2 interceptorsClosed=2 | LDS cache entry present=true refCnt=1
C3 after listener Close:        filter#1 underlyingCloseCalls=1 interceptorsBuilt=2 interceptorsClosed=2 | LDS cache entry present=false refCnt=-1
--- PASS: Test (0.00s)
    --- PASS: Test/VerifyC3_RDSErrorFilterLiveness (0.00s)
ok  	google.golang.org/grpc/internal/xds/server	1.026s
exit=0
```

Reading, identical on both branches: while the server is serving the error state (`RouteAndProcess ... Unavailable`), the retired configuration's interceptor has been closed (`interceptorsClosed=1`) but the underlying filter has not (`underlyingCloseCalls=0`) and its entry is still in the listener-level cache with `refCnt=1`. The successful configuration held the second reference (`refCnt=2` before the error); the remaining one is held at listener level. The instance is reused by the recovery (`interceptorsBuilt=2`, still `underlyingCloseCalls=0`) and is closed exactly once when the listener is closed (`underlyingCloseCalls=1`, entry gone).

The integration harness shows the same ownership on these two branches from the outside: after a successful update that disables the filter on every route, its observation is `FILTER-STILL-LIVE` and the later re-enable uses a live filter.

```sh
for b in dc4ed267 20c60584; do verify/repro/run_probe.sh $b test/xds '^Test$/^VerifyH_DisableThenReenable$' verify_harness_test.go | grep OBSERVATION; done
```

[evalon/grpc-go-xd-dc4ed267](https://github.com/kaitranntt-evals/grpc-go-xds-rds-interceptor-lifecycle-leak/tree/evalon/grpc-go-xd-dc4ed267):

```console
OBSERVATION(C1/C9 replacement): FILTER-STILL-LIVE: retired interceptor CLOSE-END at [06]; underlying filter F#1 not closed while disabled
OBSERVATION(C5/C11 re-enable): BuildServerFilter calls=1; "INTERCEPTOR F#1/i2[gen3] BUILT from FILTER F#1 filterClosed=false"; "INTERCEPTOR F#1/i2[gen3] AllowRPC selfClosed=false filterClosed=false"
OBSERVATION(C5/C11 re-enable): LIVE-FILTER-USED: re-enabled interceptor was built from a live filter instance
```

[evalon/grpc-go-xd-20c60584](https://github.com/kaitranntt-evals/grpc-go-xds-rds-interceptor-lifecycle-leak/tree/evalon/grpc-go-xd-20c60584):

```console
OBSERVATION(C1/C9 replacement): FILTER-STILL-LIVE: retired interceptor CLOSE-END at [07]; underlying filter F#1 not closed while disabled
OBSERVATION(C5/C11 re-enable): BuildServerFilter calls=1; "INTERCEPTOR F#1/i2[gen3] BUILT from FILTER F#1 filterClosed=false"; "INTERCEPTOR F#1/i2[gen3] AllowRPC selfClosed=false filterClosed=false"
OBSERVATION(C5/C11 re-enable): LIVE-FILTER-USED: re-enabled interceptor was built from a live filter instance
```

Per-branch result: on both branches the underlying filter instance stays live (not closed, cached, reference count 1) for as long as the error state is served.

Impact reasoning. What is retained is one filter instance per HTTP filter listed in the Listener, for as long as that Listener is in use; the count does not grow over repeated error/recovery cycles (two cycles above, one instance, zero closes until the listener closes). Whatever that filter instance holds (caches, clients, goroutines) is kept while no route configuration uses it. It is also what makes the recovery, and a disable/re-enable sequence, get a live instance instead of a closed one on these two branches. Whether a filter that is still listed in the Listener should be closed while its route configuration is in error is a design decision; the observation is that these branches do not close it.

## C4

Claim: route configuration replacement permits an admitted request to invoke an interceptor after that interceptor has been closed (parts: reader lifetime protection; replacement trigger). Target: the audited branch [grpc-go-xds-rds-interceptor-lifecycle-leak-perfect](https://github.com/kaitranntt-evals/grpc-go/tree/grpc-go-xds-rds-interceptor-lifecycle-leak-perfect).

Probe. `verify/repro/verify_harness_test.go` is a black-box probe: it registers its own HTTP filter through `httpfilter.Register`, starts a real `xds.NewGRPCServer` against the in-process e2e management server, and pushes Listener/RouteConfiguration resources through that management server exactly like the repo's own `test/xds` tests. The probe filter appends one line to an ordered log whenever the underlying `ServerFilter` is built or closed and whenever an interceptor is built, invoked (`AllowRPC`) or closed (`CLOSE-START` on entry to `Close`, `CLOSE-END` on return; in the disable/re-enable and shutdown tests the probe interceptor's `Close` takes 200ms so that the two are distinguishable). `filterClosed=` is whether the underlying filter's `Close` had already run at that moment, `selfClosed=` whether the interceptor's own `Close` had already returned, and `via` is the chain of `internal/xds/server` functions on the calling stack (innermost first). The file only uses public/e2e APIs, so the identical file runs on every branch.

`TestVerifyH_PausedRequestReplacement` configures two probe filters `A` and `B` (in that order) on the served route. One RPC is admitted and parked inside `A[gen1].AllowRPC`, so the routing call has already acquired the gen1 configuration and has `B[gen1].AllowRPC` still to invoke. The test then publishes a replacement RouteConfiguration (gen2) through the management server, the normal RDS update path, waits (up to 5s) for `B[gen1]`'s `Close` to return, and only then lets the parked RPC continue.

```sh
verify/repro/run_probe.sh audited test/xds '^Test$/^VerifyH_PausedRequestReplacement$' verify_harness_test.go
```

```console
---- event log: paused request across replacement ----
  [00] FILTER A#1 BUILT (BuildServerFilter)
  [01] INTERCEPTOR A#1/i1[gen1] BUILT from FILTER A#1 filterClosed=false
  [02] FILTER B#1 BUILT (BuildServerFilter)
  [03] INTERCEPTOR B#1/i1[gen1] BUILT from FILTER B#1 filterClosed=false
  [04] INTERCEPTOR A#1/i1[gen1] AllowRPC selfClosed=false filterClosed=false
  [05] TEST: request admitted and paused inside A[gen1].AllowRPC (B[gen1].AllowRPC not yet invoked)
  [06] TEST: >>> publishing replacement RDS (gen2) through the management server
  [07] INTERCEPTOR A#1/i2[gen2] BUILT from FILTER A#1 filterClosed=false
  [08] INTERCEPTOR B#1/i2[gen2] BUILT from FILTER B#1 filterClosed=false
  [09] INTERCEPTOR A#1/i1[gen1] CLOSE-START filterClosed=false via (*interceptorList).Close < (*usableRouteConfiguration).stop < (*filterChain).applyConfiguration < (*filterChain).updateUsableRouteConfiguration < (*listenerWrapper).handleRDSUpdate
  [10] INTERCEPTOR A#1/i1[gen1] CLOSE-END filterClosed=false
  [11] INTERCEPTOR B#1/i1[gen1] CLOSE-START filterClosed=false via (*interceptorList).Close < (*usableRouteConfiguration).stop < (*filterChain).applyConfiguration < (*filterChain).updateUsableRouteConfiguration < (*listenerWrapper).handleRDSUpdate
  [12] INTERCEPTOR B#1/i1[gen1] CLOSE-END filterClosed=false
  [13] TEST: B[gen1] Close finished while request still paused = true; resuming request
  [14] INTERCEPTOR B#1/i1[gen1] AllowRPC selfClosed=true filterClosed=false
  [15] TEST: paused RPC returned err=<nil>
OBSERVATION(C4/C10): CLOSED-INTERCEPTOR-INVOKED: INTERCEPTOR B#1/i1[gen1] AllowRPC selfClosed=true filterClosed=false (after its CLOSE-END at [12])
```

Part "reader lifetime protection": lines [05] to [12] show retirement completing while a routing call still holds the old configuration. The request is parked in `A[gen1].AllowRPC` at [05] and has not returned; `A[gen1]` and `B[gen1]` are closed at [09] to [12] via `(*usableRouteConfiguration).stop < (*filterChain).applyConfiguration`, and line [13] records that `B[gen1]`'s `Close` had finished while the request was still paused. Retirement did not wait for the reader. Observed.

Part "replacement trigger": line [14], `INTERCEPTOR B#1/i1[gen1] AllowRPC selfClosed=true`, is the resumed request invoking `B[gen1]` after its `CLOSE-END` at [12]; the replacement was an ordinary RDS update. Observed.

Controls with the identical probe (`run_probe.sh <id> ...` instead of `audited`), to show the probe can produce the other outcome:

```console
base      OBSERVATION(C4/C10): LIVE-INTERCEPTOR-INVOKED: INTERCEPTOR B#1/i1[gen1] AllowRPC selfClosed=false filterClosed=false (closedWhilePaused=false)
d5ce5e46  OBSERVATION(C4/C10): LIVE-INTERCEPTOR-INVOKED: INTERCEPTOR B#1/i1[gen1] AllowRPC selfClosed=false filterClosed=false (closedWhilePaused=false)
55cd92cd  OBSERVATION(C4/C10): LIVE-INTERCEPTOR-INVOKED: INTERCEPTOR B#1/i1[gen1] AllowRPC selfClosed=false filterClosed=false (closedWhilePaused=false)
dc4ed267  OBSERVATION(C4/C10): LIVE-INTERCEPTOR-INVOKED: INTERCEPTOR B#1/i1[gen1] AllowRPC selfClosed=false filterClosed=false (closedWhilePaused=false)
```

`base` is the task's base commit `4ee6ac46fada` (it never closes interceptors, which is the leak the task is about); the other three are claim-target branches ([evalon/grpc-go-xd-d5ce5e46](https://github.com/kaitranntt-evals/grpc-go-xds-rds-interceptor-lifecycle-leak/tree/evalon/grpc-go-xd-d5ce5e46), [evalon/grpc-go-xd-55cd92cd](https://github.com/kaitranntt-evals/grpc-go-xds-rds-interceptor-lifecycle-leak/tree/evalon/grpc-go-xd-55cd92cd), [evalon/grpc-go-xd-dc4ed267](https://github.com/kaitranntt-evals/grpc-go-xds-rds-interceptor-lifecycle-leak/tree/evalon/grpc-go-xd-dc4ed267)) on which closure waited for the parked request (`closedWhilePaused=false`).

Impact reasoning. `resolver.ServerInterceptor` documents `Close` as "Close closes the interceptor. Once called, no new calls ... are accepted" (`internal/resolver/config_selector.go`). On the audited branch a route update closes the retired configuration's interceptors as soon as the new configuration is published, without regard to RPCs that already picked up the old configuration, so such an RPC goes on to call `AllowRPC` on an interceptor whose `Close` has returned. The trigger is the most ordinary one there is (any RouteConfiguration update while RPCs are arriving); the window per RPC is the time between routing picking up the configuration and its last `AllowRPC`, which is short unless an interceptor blocks, and the probe widens it by parking the request. What the RPC then experiences depends on the filter: the probe interceptor keeps answering after `Close` and the RPC returned `err=<nil>`, so nothing is visibly wrong; an interceptor that releases state in `Close` would be used after release. The eval fixture (all five tests pass on this branch, see Setup) does not cover this schedule.

## C5

Claim: successfully disabling and then re-enabling a route filter causes the server to reuse a closed cached filter instance (parts: cached instance reuse; successful update trigger). Target: the audited branch [grpc-go-xds-rds-interceptor-lifecycle-leak-perfect](https://github.com/kaitranntt-evals/grpc-go/tree/grpc-go-xds-rds-interceptor-lifecycle-leak-perfect).

### Part "cached instance reuse"

`verify/repro/verify_c5_c11_cache_test.go` (package `server`) calls the lookup function the listener uses, `getOrCreateServerFilterWithMap`, on a cache map: first lookup, release of the only reference through the wrapper's `Close`, second lookup with the same key.

```sh
verify/repro/run_probe.sh audited internal/xds/server '^Test$/^VerifyC5C11_' verify_c5_c11_cache_test.go
```

```console
C5/C11 after 1st lookup:  BuildServerFilter calls=1 refCnt=1 underlying Close calls=0
C5/C11 after last release: refCnt=0 underlying Close calls=1 entry still in cache=true
C5/C11 after 2nd lookup:  BuildServerFilter calls=1 sameWrapper=true sameUnderlying=true refCnt=1 underlying Close calls=1
C5/C11 OBSERVATION: CLOSED-WRAPPER-RETURNED: lookup incremented (refCnt 0 -> 1) and returned the wrapper whose underlying filter was already closed
C5/C11 after releasing 2nd: refCnt=0 underlying Close calls=2
--- PASS: Test (0.00s)
    --- PASS: Test/VerifyC5C11_ClosedCacheEntryLookup (0.00s)
ok  	google.golang.org/grpc/internal/xds/server	1.027s
exit=0
```

Observed: after the last release the wrapper is at `refCnt=0`, the underlying filter's `Close` has run once, and the entry is still in the cache. The second lookup does not call `BuildServerFilter` again (`calls=1`); it increments the same wrapper from 0 to 1 and returns it, with the same, already closed, underlying filter. Releasing that second reference runs the underlying `Close` a second time (`Close calls=2`).

### Part "successful update trigger"

`verify/repro/verify_harness_test.go` is a black-box probe: it registers its own HTTP filter through `httpfilter.Register`, starts a real `xds.NewGRPCServer` against the in-process e2e management server, and pushes Listener/RouteConfiguration resources through that management server exactly like the repo's own `test/xds` tests. The probe filter appends one line to an ordered log whenever the underlying `ServerFilter` is built or closed and whenever an interceptor is built, invoked (`AllowRPC`) or closed (`CLOSE-START` on entry to `Close`, `CLOSE-END` on return; in the disable/re-enable and shutdown tests the probe interceptor's `Close` takes 200ms so that the two are distinguishable). `filterClosed=` is whether the underlying filter's `Close` had already run at that moment, `selfClosed=` whether the interceptor's own `Close` had already returned, and `via` is the chain of `internal/xds/server` functions on the calling stack (innermost first). The file only uses public/e2e APIs, so the identical file runs on every branch.

`TestVerifyH_DisableThenReenable` serves a RouteConfiguration with probe filter `F` enabled (gen1), publishes a successful update that disables `F` on every route (`typed_per_filter_config`), waits until an RPC is served without `F`, then publishes a successful update that re-enables it (gen3) and sends an RPC.

```sh
verify/repro/run_probe.sh audited test/xds '^Test$/^VerifyH_DisableThenReenable$' verify_harness_test.go
```

```console
---- event log: after re-enable ----
  [00] FILTER F#1 BUILT (BuildServerFilter)
  [01] INTERCEPTOR F#1/i1[gen1] BUILT from FILTER F#1 filterClosed=false
  [02] INTERCEPTOR F#1/i1[gen1] AllowRPC selfClosed=false filterClosed=false
  [03] TEST: >>> publishing RDS with F disabled on every route
  [04] INTERCEPTOR F#1/i1[gen1] AllowRPC selfClosed=false filterClosed=false
  [05] INTERCEPTOR F#1/i1[gen1] CLOSE-START filterClosed=false via (*interceptorList).Close < (*usableRouteConfiguration).stop < (*filterChain).applyConfiguration < (*filterChain).updateUsableRouteConfiguration < (*listenerWrapper).handleRDSUpdate
  [06] TEST: RPC served by the replacement (F disabled) config
  [07] INTERCEPTOR F#1/i1[gen1] CLOSE-END filterClosed=false
  [08] FILTER F#1 CLOSED (underlying ServerFilter.Close ran) via (*refCountedServerFilter).Close < (*filterChain).applyConfiguration < (*filterChain).updateUsableRouteConfiguration < (*listenerWrapper).handleRDSUpdate < (*rdsWatcher).ResourceChanged
  [09] TEST: >>> publishing RDS with F re-enabled (gen3)
  [10] INTERCEPTOR F#1/i2[gen3] BUILT from FILTER F#1 filterClosed=true
  [11] INTERCEPTOR F#1/i2[gen3] AllowRPC selfClosed=false filterClosed=true
OBSERVATION(C5/C11 re-enable): BuildServerFilter calls=1; "INTERCEPTOR F#1/i2[gen3] BUILT from FILTER F#1 filterClosed=true"; "INTERCEPTOR F#1/i2[gen3] AllowRPC selfClosed=false filterClosed=true"
OBSERVATION(C5/C11 re-enable): CLOSED-FILTER-REUSED: re-enabled interceptor was built from an already-closed filter instance
```

Observed: the only filter instance ever built (`BuildServerFilter calls=1`) is closed at [08] when the disabling update drops its last reference; the re-enabling update builds interceptor `i2[gen3]` from that same instance (`BUILT from FILTER F#1 filterClosed=true`) and RPCs are then served through it ([11]). All three updates were accepted and served. At shutdown the same instance is closed a second time (lines [12] to [14] of the "after server Stop" log in `verify/logs/A.audited.out`: `FILTER F#1 CLOSED ... via (*refCountedServerFilter).Close < (*filterChain).stop`).

Controls with the identical probe:

```console
base      OBSERVATION(C5/C11 re-enable): BuildServerFilter calls=1; "INTERCEPTOR F#1/i2[gen3] BUILT from FILTER F#1 filterClosed=true"; "INTERCEPTOR F#1/i2[gen3] AllowRPC selfClosed=false filterClosed=true"
dc4ed267  OBSERVATION(C5/C11 re-enable): BuildServerFilter calls=1; "INTERCEPTOR F#1/i2[gen3] BUILT from FILTER F#1 filterClosed=false"; "INTERCEPTOR F#1/i2[gen3] AllowRPC selfClosed=false filterClosed=false"
55cd92cd  OBSERVATION(C5/C11 re-enable): BuildServerFilter calls=2; "INTERCEPTOR F#2/i1[gen3] BUILT from FILTER F#2 filterClosed=false"; "INTERCEPTOR F#2/i1[gen3] AllowRPC selfClosed=false filterClosed=false"
```

`base` is the task's base commit `4ee6ac46fada`: the reuse of a closed filter instance already exists there, so the audited change did not introduce it, it left it in place. [evalon/grpc-go-xd-dc4ed267](https://github.com/kaitranntt-evals/grpc-go-xds-rds-interceptor-lifecycle-leak/tree/evalon/grpc-go-xd-dc4ed267) and [evalon/grpc-go-xd-55cd92cd](https://github.com/kaitranntt-evals/grpc-go-xds-rds-interceptor-lifecycle-leak/tree/evalon/grpc-go-xd-55cd92cd) are claim-target branches on which the same sequence ends with a live filter.

Impact reasoning. `httpfilter.ServerFilter.Close` is documented as "Close is called when the filter is no longer needed". On the audited branch a filter instance that has been told so is put back into service by the next route update that enables it again, and is later closed a second time. Nothing fails at the xDS level (updates are ACKed, RPCs are routed), so the server looks healthy; what happens inside depends on the filter: one whose `Close` releases resources is asked to build interceptors and authorize RPCs after releasing them, and must tolerate a double `Close`. The trigger needs a filter to be disabled on every route of a filter chain (per-route override) and enabled again later without the Listener changing in between; that is a legitimate but not everyday rollout pattern. No workaround was exercised.

## C6

Claim: graceful LDS listener replacement causes admitted routing calls on existing connections to fail with Unavailable because their retained routing state is changed to a stopped-chain error (parts: stop-time routing state; LDS replacement trigger). Target: [evalon/grpc-go-xd-cd6e69ae](https://github.com/kaitranntt-evals/grpc-go-xds-rds-interceptor-lifecycle-leak/tree/evalon/grpc-go-xd-cd6e69ae) (`861931f1af7e`).

### Part "stop-time routing state"

`verify/repro/verify_c6_stop_state_test.go` (package `server`) creates a filter chain with a usable route configuration, hands a connection the chain's routing-state pointer the way `listenerWrapper.Accept` does, calls `filterChain.stop()`, and prints the error stored in the state the connection still holds.

```sh
verify/repro/run_probe.sh cd6e69ae internal/xds/server '^Test$/^VerifyC6_StopState$' verify_c6_stop_state_test.go
verify/repro/run_probe.sh audited  internal/xds/server '^Test$/^VerifyC6_StopState$' verify_c6_stop_state_test.go
```

[evalon/grpc-go-xd-cd6e69ae](https://github.com/kaitranntt-evals/grpc-go-xds-rds-interceptor-lifecycle-leak/tree/evalon/grpc-go-xd-cd6e69ae):

```console
C6 before filterChain.stop(): routing state held by the existing connection: err=<nil>
C6 after  filterChain.stop(): routing state held by the existing connection: err=filter chain stopped
C6 OBSERVATION: STOPPED-CHAIN-ERROR-PUBLISHED: "filter chain stopped"
--- PASS: Test (0.00s)
    --- PASS: Test/VerifyC6_StopState (0.00s)
ok  	google.golang.org/grpc/internal/xds/server	1.025s
exit=0
```

Audited branch [grpc-go-xds-rds-interceptor-lifecycle-leak-perfect](https://github.com/kaitranntt-evals/grpc-go/tree/grpc-go-xds-rds-interceptor-lifecycle-leak-perfect) (control):

```console
C6 before filterChain.stop(): routing state held by the existing connection: err=<nil>
C6 after  filterChain.stop(): routing state held by the existing connection: err=<nil>
C6 OBSERVATION: ROUTING-STATE-UNCHANGED-BY-STOP
--- PASS: Test (0.00s)
    --- PASS: Test/VerifyC6_StopState (0.00s)
ok  	google.golang.org/grpc/internal/xds/server	1.026s
exit=0
```

Observed on [evalon/grpc-go-xd-cd6e69ae](https://github.com/kaitranntt-evals/grpc-go-xds-rds-interceptor-lifecycle-leak/tree/evalon/grpc-go-xd-cd6e69ae): `stop()` publishes an error configuration (`filter chain stopped`) into the routing state retained by the existing connection. The branch source is `func (fc *filterChain) stop() { fc.updateRouteConfiguration(&usableRouteConfiguration{err: errors.New("filter chain stopped")}) ... }` (`internal/xds/server/filter_chain_manager.go:435`).

### Part "LDS replacement trigger"

`verify/repro/verify_c6_lds_replacement_test.go` (uses the harness helpers) starts an `xds.NewGRPCServer` with a `stats.Handler` whose `TagRPC` can park one server stream; `TagRPC` runs after the stream has been accepted on the connection and before the xDS routing interceptor. Sequence: a warm-up RPC succeeds on the connection; a second RPC on the same connection is admitted and parked; a replacement Listener (same address, changed filter chain) is published through the management server; the test waits until the replacement is active; the parked RPC is released; a follow-up RPC is sent. Two variants: the served Listener has the probe HTTP filter plus router and the replacement changes the probe filter's config; or the served Listener is router-only and the replacement adds the probe filter (so that the test can tell when the replacement is active).

```sh
verify/repro/run_probe.sh cd6e69ae test/xds '^Test$/^VerifyC6_' verify_harness_test.go verify_c6_lds_replacement_test.go
verify/repro/run_probe.sh audited  test/xds '^Test$/^VerifyC6_' verify_harness_test.go verify_c6_lds_replacement_test.go
```

[evalon/grpc-go-xd-cd6e69ae](https://github.com/kaitranntt-evals/grpc-go-xds-rds-interceptor-lifecycle-leak/tree/evalon/grpc-go-xd-cd6e69ae):

```console
---- event log: admitted RPC across LDS replacement ----
  [00] TEST: warm-up RPC ok on existing connection
  [01] TEST: RPC admitted on the existing connection (client 127.0.0.1:38134); server stream parked before the routing interceptor
  [02] TEST: >>> publishing replacement Listener (LDS) through the management server
  [03] FILTER F#1 BUILT (BuildServerFilter)
  [04] INTERCEPTOR F#1/i1[lds2] BUILT from FILTER F#1 filterClosed=false
  [05] TEST: replacement Listener active; letting the admitted RPC proceed to routing
  [06] TEST: admitted RPC returned code=Unavailable err=rpc error: code = Unavailable desc = error from xDS configuration for matched route configuration: filter chain stopped
  [07] INTERCEPTOR F#1/i1[lds2] AllowRPC selfClosed=false filterClosed=false
  [08] TEST: follow-up RPC ok
OBSERVATION(C6 withFilter=false): ADMITTED-RPC-FAILED-STOPPED-CHAIN: code=Unavailable err=rpc error: code = Unavailable desc = error from xDS configuration for matched route configuration: filter chain stopped
---- event log: admitted RPC across LDS replacement ----
  [00] FILTER F#1 BUILT (BuildServerFilter)
  [01] INTERCEPTOR F#1/i1[lds1] BUILT from FILTER F#1 filterClosed=false
  [02] INTERCEPTOR F#1/i1[lds1] AllowRPC selfClosed=false filterClosed=false
  [03] TEST: warm-up RPC ok on existing connection
  [04] TEST: RPC admitted on the existing connection (client 127.0.0.1:40564); server stream parked before the routing interceptor
  [05] TEST: >>> publishing replacement Listener (LDS) through the management server
  [06] INTERCEPTOR F#1/i2[lds2] BUILT from FILTER F#1 filterClosed=false
  [07] INTERCEPTOR F#1/i1[lds1] CLOSE-START filterClosed=false via (*interceptorList).Close < virtualHostWithInterceptors.closeInterceptors < (*usableRouteConfiguration).closeResources < (*usableRouteConfiguration).close < (*filterChain).updateRouteConfiguration
  [08] INTERCEPTOR F#1/i1[lds1] CLOSE-END filterClosed=false
  [09] TEST: replacement Listener active; letting the admitted RPC proceed to routing
  [10] TEST: admitted RPC returned code=Unavailable err=rpc error: code = Unavailable desc = error from xDS configuration for matched route configuration: filter chain stopped
  [11] INTERCEPTOR F#1/i2[lds2] AllowRPC selfClosed=false filterClosed=false
  [12] TEST: follow-up RPC ok
OBSERVATION(C6 withFilter=true): ADMITTED-RPC-FAILED-STOPPED-CHAIN: code=Unavailable err=rpc error: code = Unavailable desc = error from xDS configuration for matched route configuration: filter chain stopped
--- PASS: Test (0.67s)
    --- PASS: Test/VerifyC6_AdmittedRPCAcrossLDSReplacement_RouterOnly (0.34s)
    --- PASS: Test/VerifyC6_AdmittedRPCAcrossLDSReplacement_WithFilter (0.33s)
ok  	google.golang.org/grpc/test/xds	1.693s
exit=0
```

Audited branch [grpc-go-xds-rds-interceptor-lifecycle-leak-perfect](https://github.com/kaitranntt-evals/grpc-go/tree/grpc-go-xds-rds-interceptor-lifecycle-leak-perfect) (control):

```console
---- event log: admitted RPC across LDS replacement ----
  [00] TEST: warm-up RPC ok on existing connection
  [01] TEST: RPC admitted on the existing connection (client 127.0.0.1:46556); server stream parked before the routing interceptor
  [02] TEST: >>> publishing replacement Listener (LDS) through the management server
  [03] FILTER F#1 BUILT (BuildServerFilter)
  [04] INTERCEPTOR F#1/i1[lds2] BUILT from FILTER F#1 filterClosed=false
  [05] TEST: replacement Listener active; letting the admitted RPC proceed to routing
  [06] TEST: admitted RPC returned code=OK err=<nil>
  [07] INTERCEPTOR F#1/i1[lds2] AllowRPC selfClosed=false filterClosed=false
  [08] TEST: follow-up RPC ok
OBSERVATION(C6 withFilter=false): ADMITTED-RPC-OK: the admitted RPC on the existing connection completed successfully across the LDS replacement
---- event log: admitted RPC across LDS replacement ----
  [00] FILTER F#1 BUILT (BuildServerFilter)
  [01] INTERCEPTOR F#1/i1[lds1] BUILT from FILTER F#1 filterClosed=false
  [02] INTERCEPTOR F#1/i1[lds1] AllowRPC selfClosed=false filterClosed=false
  [03] TEST: warm-up RPC ok on existing connection
  [04] TEST: RPC admitted on the existing connection (client 127.0.0.1:36788); server stream parked before the routing interceptor
  [05] TEST: >>> publishing replacement Listener (LDS) through the management server
  [06] INTERCEPTOR F#1/i2[lds2] BUILT from FILTER F#1 filterClosed=false
  [07] INTERCEPTOR F#1/i1[lds1] CLOSE-START filterClosed=false via (*interceptorList).Close < (*usableRouteConfiguration).stop < (*filterChainManager).stop < (*listenerWrapper).maybeUpdateFilterChains < (*ldsWatcher).ResourceChanged
  [08] INTERCEPTOR F#1/i1[lds1] CLOSE-END filterClosed=false
  [09] TEST: replacement Listener active; letting the admitted RPC proceed to routing
  [10] INTERCEPTOR F#1/i1[lds1] AllowRPC selfClosed=true filterClosed=false
  [11] TEST: admitted RPC returned code=OK err=<nil>
  [12] INTERCEPTOR F#1/i2[lds2] AllowRPC selfClosed=false filterClosed=false
  [13] TEST: follow-up RPC ok
OBSERVATION(C6 withFilter=true): ADMITTED-RPC-OK: the admitted RPC on the existing connection completed successfully across the LDS replacement
--- PASS: Test (0.67s)
    --- PASS: Test/VerifyC6_AdmittedRPCAcrossLDSReplacement_RouterOnly (0.34s)
    --- PASS: Test/VerifyC6_AdmittedRPCAcrossLDSReplacement_WithFilter (0.33s)
ok  	google.golang.org/grpc/test/xds	1.697s
exit=0
```

Observed on [evalon/grpc-go-xd-cd6e69ae](https://github.com/kaitranntt-evals/grpc-go-xds-rds-interceptor-lifecycle-leak/tree/evalon/grpc-go-xd-cd6e69ae): in both variants the RPC that had been admitted on the existing connection before the Listener was replaced returns `code = Unavailable desc = error from xDS configuration for matched route configuration: filter chain stopped`; the router-only variant shows that no custom filter needs to be on the connection's filter chain. On the audited branch the same schedule returns `code=OK` in both variants.

Impact reasoning. The task statement asks that superseded resources be released "without dropping requests on active listeners". On this branch every Listener update that replaces the filter chains fails the RPCs that are already admitted on existing connections but have not been routed yet, with `Unavailable`, instead of letting them finish on the old configuration while the connection drains. A Listener update on a serving listener is an ordinary control-plane event; the affected RPCs are those inside the admission-to-routing window at that moment (the probe parks one there to make it deterministic). The failure is loud (a status the client sees) and retryable by clients that retry `Unavailable`.

## C7

Claim: forceful server Stop deadlocks when an interceptor waits for RPC cancellation because listener teardown blocks the transport cancellation needed to release that interceptor (parts: routing lock dependency; forceful shutdown trigger). Target: [evalon/grpc-go-xd-55cd92cd](https://github.com/kaitranntt-evals/grpc-go-xds-rds-interceptor-lifecycle-leak/tree/evalon/grpc-go-xd-55cd92cd) (`29cffcaa9592`).

Probe. `verify/repro/verify_c7_forceful_stop_test.go` (uses the harness helpers) serves a route with the probe filter whose interceptor's `AllowRPC` blocks until its RPC context is done. One RPC is started (client context without deadline) and reaches `AllowRPC`; the test then calls the server's forceful `Stop()` in a goroutine and waits 8s for it to return. If it has not, the goroutine stacks of the listener teardown and of the routing call are captured (function names only), the RPC is canceled from the client side, and the time until `Stop()` returns is logged.

```sh
verify/repro/run_probe.sh 55cd92cd test/xds '^Test$/^VerifyC7_' verify_harness_test.go verify_c7_forceful_stop_test.go
verify/repro/run_probe.sh audited  test/xds '^Test$/^VerifyC7_' verify_harness_test.go verify_c7_forceful_stop_test.go
```

[evalon/grpc-go-xd-55cd92cd](https://github.com/kaitranntt-evals/grpc-go-xds-rds-interceptor-lifecycle-leak/tree/evalon/grpc-go-xd-55cd92cd):

```console
STACK (listener teardown):
    goroutine 83 [sync.RWMutex.Lock]:
    sync.runtime_SemacquireRWMutex
    sync.(*RWMutex).Lock
    google.golang.org/grpc/internal/xds/server.(*filterChain).stop
    google.golang.org/grpc/internal/xds/server.(*filterChainManager).stop
    google.golang.org/grpc/internal/xds/server.(*listenerWrapper).Close
    google.golang.org/grpc.(*listenSocket).Close
    google.golang.org/grpc.(*Server).closeListenersLocked
    google.golang.org/grpc.(*Server).stop
    google.golang.org/grpc.(*Server).Stop
    google.golang.org/grpc/xds.(*GRPCServer).Stop
    google.golang.org/grpc/test/xds_test.setupGRPCServer.func4
    sync.(*Once).doSlow
    sync.(*Once).Do
STACK (routing call):
    goroutine 130 [chan receive]:
    google.golang.org/grpc/test/xds_test.s.TestVerifyC7_ForcefulStopWithCancellationWaitingInterceptor.func1
    google.golang.org/grpc/test/xds_test.(*vfyInterceptor).AllowRPC
    google.golang.org/grpc/internal/xds/server.(*interceptorList).AllowRPC
    google.golang.org/grpc/internal/xds/server.RouteAndProcess
    google.golang.org/grpc/xds.xdsUnaryInterceptor
    google.golang.org/grpc/interop/grpc_testing._TestService_EmptyCall_Handler
    google.golang.org/grpc.(*Server).processUnaryRPC
    google.golang.org/grpc.(*Server).handleStream
    google.golang.org/grpc.(*Server).serveStreams.func2.1
---- event log: forceful Stop with cancellation-waiting interceptor ----
  [00] FILTER F#1 BUILT (BuildServerFilter)
  [01] INTERCEPTOR F#1/i1[gen1] BUILT from FILTER F#1 filterClosed=false
  [02] INTERCEPTOR F#1/i1[gen1] AllowRPC selfClosed=false filterClosed=false
  [03] TEST: RPC is inside AllowRPC, waiting for its context to be canceled
  [04] TEST: >>> calling forceful Stop()
  [05] TEST: Stop() has NOT returned after 8s; AllowRPC released=false
  [06] TEST: canceling the RPC from the client side to break the wait
  [07] INTERCEPTOR F#1/i1[gen1] AllowRPC released by RPC context cancellation: context canceled
  [08] INTERCEPTOR F#1/i1[gen1] CLOSE-START filterClosed=false via (*interceptorList).Close < virtualHostWithInterceptors.close < (*filterChain).stop.(*usableRouteConfiguration).close.func1 < (*usableRouteConfiguration).close < (*filterChain).stop
  [09] INTERCEPTOR F#1/i1[gen1] CLOSE-END filterClosed=false
  [10] FILTER F#1 CLOSED (underlying ServerFilter.Close ran) via (*refCountedServerFilter).Close < (*filterChain).stop.(*usableRouteConfiguration).close.func1 < (*usableRouteConfiguration).close < (*filterChain).stop < (*filterChainManager).stop
  [11] TEST: Stop() returned only after the client-side cancel (total 8.039s)
OBSERVATION(C7): STOP-BLOCKED: forceful Stop() did not return within 8s while AllowRPC waited for cancellation
client RPC result: rpc error: code = Canceled desc = context canceled
--- PASS: Test (8.13s)
    --- PASS: Test/VerifyC7_ForcefulStopWithCancellationWaitingInterceptor (8.12s)
ok  	google.golang.org/grpc/test/xds	9.156s
exit=0
```

Audited branch [grpc-go-xds-rds-interceptor-lifecycle-leak-perfect](https://github.com/kaitranntt-evals/grpc-go/tree/grpc-go-xds-rds-interceptor-lifecycle-leak-perfect) (control):

```console
---- event log: forceful Stop with cancellation-waiting interceptor ----
  [00] FILTER F#1 BUILT (BuildServerFilter)
  [01] INTERCEPTOR F#1/i1[gen1] BUILT from FILTER F#1 filterClosed=false
  [02] INTERCEPTOR F#1/i1[gen1] AllowRPC selfClosed=false filterClosed=false
  [03] TEST: RPC is inside AllowRPC, waiting for its context to be canceled
  [04] TEST: >>> calling forceful Stop()
  [05] INTERCEPTOR F#1/i1[gen1] CLOSE-START filterClosed=false via (*interceptorList).Close < (*usableRouteConfiguration).stop < (*filterChainManager).stop < (*listenerWrapper).Close
  [06] INTERCEPTOR F#1/i1[gen1] CLOSE-END filterClosed=false
  [07] FILTER F#1 CLOSED (underlying ServerFilter.Close ran) via (*refCountedServerFilter).Close < (*filterChain).stop < (*filterChainManager).stop < (*listenerWrapper).Close
  [08] INTERCEPTOR F#1/i1[gen1] AllowRPC released by RPC context cancellation: context canceled
  [09] TEST: Stop() returned after 1ms
OBSERVATION(C7): STOP-COMPLETED in 2ms; interceptor released by cancellation=true
client RPC result: rpc error: code = Unavailable desc = error reading from server: EOF
--- PASS: Test (0.09s)
    --- PASS: Test/VerifyC7_ForcefulStopWithCancellationWaitingInterceptor (0.08s)
ok  	google.golang.org/grpc/test/xds	1.111s
exit=0
```

Branch source for the lock (`grep -n routingMu internal/xds/server/*.go` in the [evalon/grpc-go-xd-55cd92cd](https://github.com/kaitranntt-evals/grpc-go-xds-rds-interceptor-lifecycle-leak/tree/evalon/grpc-go-xd-55cd92cd) worktree):

```console
internal/xds/server/routing.go:46:	cw.filterChain.routingMu.RLock()
internal/xds/server/routing.go:47:	defer cw.filterChain.routingMu.RUnlock()
internal/xds/server/filter_chain_manager.go:171:	// routingMu prevents a route configuration from being closed while an RPC
internal/xds/server/filter_chain_manager.go:173:	routingMu sync.RWMutex
internal/xds/server/filter_chain_manager.go:403:	fc.routingMu.Lock()
internal/xds/server/filter_chain_manager.go:404:	defer fc.routingMu.Unlock()
internal/xds/server/filter_chain_manager.go:411:	fc.routingMu.Lock()
internal/xds/server/filter_chain_manager.go:412:	defer fc.routingMu.Unlock()
```

Part "routing lock dependency": observed. With `AllowRPC` waiting for cancellation, the routing goroutine sits in `RouteAndProcess > (*interceptorList).AllowRPC` (which holds `routingMu.RLock()`, `routing.go:46`), and `(*filterChain).stop` is parked in `sync.(*RWMutex).Lock`; the write lock is not obtained until `AllowRPC` returns ([07] precedes [08] to [10]).

Part "forceful shutdown trigger": observed. The teardown stack is `(*Server).Stop > (*Server).stop > closeListenersLocked > (*listenSocket).Close > (*listenerWrapper).Close > (*filterChainManager).stop > (*filterChain).stop > RWMutex.Lock`, i.e. `Stop()` is still closing listeners and has not yet reached the point where it closes the transports, which is what would cancel the RPC context the interceptor waits for. Nothing on the server side broke the cycle in 8s (`Stop() has NOT returned after 8s; AllowRPC released=false`); it ended only when the test canceled the RPC from the client (`Stop() returned only after the client-side cancel (total 8.039s)`). On the audited branch the same schedule completes in 1 to 2ms and the interceptor is released by the server-side cancellation.

Impact reasoning. On this branch a forceful `Stop()` cannot finish while any RPC is inside an interceptor's `AllowRPC`, and it also cannot cancel that RPC, because cancellation is behind the listener teardown that is waiting for it. Whether that is a hang forever or a delay depends on something outside the server ending the RPC: a client deadline, a client cancel, or the connection dropping. With a client that has no deadline (the probe) the server did not stop until the test intervened. The trigger needs an interceptor that can block in `AllowRPC` (the interface passes a context for exactly that purpose, and the TODO on it mentions rate limiting); in-tree interceptors that return immediately do not expose it, which makes this a loud failure behind a less common configuration.

## C8

Claim: an added test passes bare `context.Background()` to `AllowRPC`, leaving that invocation without a bounded context deadline and matching the repository's prohibited test-context pattern. Target: [evalon/grpc-go-xd-c5d0b2f7](https://github.com/kaitranntt-evals/grpc-go-xds-rds-interceptor-lifecycle-leak/tree/evalon/grpc-go-xd-c5d0b2f7) (`d53dee74d436`).

The predicate, verbatim from `scripts/vet.sh` line 80 (preceded there by the comments `# - Ensure all context usages are done with timeout.` and `# Context tests under benchmark are excluded as they are testing the performance of context.Background() and context.TODO().`):

```sh
git grep -e 'context.Background()' --or -e 'context.TODO()' -- "*_test.go" | grep -v "benchmark/primitives/context_test.go" | grep -v 'context.WithTimeout(' | not grep -v 'context.WithCancel('
```

`verify/repro/c8_vet_context.sh` sources the repo's own `scripts/common.sh` (for `not`) and runs exactly that line under `set -eo pipefail`, like `vet.sh` does.

```sh
(cd ~/wt/c5d0b2f7 && bash ~/repos/grpc-go/verify/repro/c8_vet_context.sh; echo "exit=$?")
(cd ~/repos/grpc-go && bash verify/repro/c8_vet_context.sh; echo "exit=$?")
```

[evalon/grpc-go-xd-c5d0b2f7](https://github.com/kaitranntt-evals/grpc-go-xds-rds-interceptor-lifecycle-leak/tree/evalon/grpc-go-xd-c5d0b2f7):

```console
internal/xds/server/route_configuration_cleanup_test.go:						if err := r.interceptor.AllowRPC(context.Background()); err != nil {
exit=1
```

Audited branch [grpc-go-xds-rds-interceptor-lifecycle-leak-perfect](https://github.com/kaitranntt-evals/grpc-go/tree/grpc-go-xds-rds-interceptor-lifecycle-leak-perfect) (control):

```console
vet context check: PASSED
exit=0
```

That the file is one the branch added, and where the call sits:

```sh
cd ~/wt/c5d0b2f7 && git diff --name-status 4ee6ac46 HEAD && grep -n 'context.Background()\|^func (s) Test' internal/xds/server/route_configuration_cleanup_test.go
```

```console
M	internal/xds/server/filter_chain_manager.go
M	internal/xds/server/listener_wrapper.go
A	internal/xds/server/route_configuration_cleanup_test.go
M	test/xds/xds_server_filter_state_retention_test.go
109:func (s) TestRouteConfigurationConstructionFailureCleanup(t *testing.T) {
160:						if err := r.interceptor.AllowRPC(context.Background()); err != nil {
185:func (s) TestRouteConfigurationResourceErrorCleanup(t *testing.T) {
```

Observed: on [evalon/grpc-go-xd-c5d0b2f7](https://github.com/kaitranntt-evals/grpc-go-xds-rds-interceptor-lifecycle-leak/tree/evalon/grpc-go-xd-c5d0b2f7) the predicate prints one offending line and fails (exit 1): `r.interceptor.AllowRPC(context.Background())` at line 160 of the added file `internal/xds/server/route_configuration_cleanup_test.go`, inside `TestRouteConfigurationConstructionFailureCleanup` (lines 109 to 184). The context has no deadline or cancel. The claim names that test and that expression exactly. On the audited branch the same predicate passes.

Impact reasoning. `scripts/vet.sh` would fail on this branch at the "context usages are done with timeout" step, so the branch does not pass the repository's own static checks (the task's lint guideline asks for clean static checks). Runtime risk is nil here because the interceptor called is a test double that returns immediately; this is a convention/CI failure, not a behavioral one. The fix is a one-line change to pass a `context.WithTimeout` context.

## C9

Claim: a successful route replacement that disables a filter on every route destroys the filter before retired interceptors depending on it finish closing (parts: replacement release ordering; all-routes-disabled trigger). Target: [evalon/grpc-go-xd-ee3cc2af](https://github.com/kaitranntt-evals/grpc-go-xds-rds-interceptor-lifecycle-leak/tree/evalon/grpc-go-xd-ee3cc2af) (`232be719f4d6`).

Probe. `verify/repro/verify_harness_test.go` is a black-box probe: it registers its own HTTP filter through `httpfilter.Register`, starts a real `xds.NewGRPCServer` against the in-process e2e management server, and pushes Listener/RouteConfiguration resources through that management server exactly like the repo's own `test/xds` tests. The probe filter appends one line to an ordered log whenever the underlying `ServerFilter` is built or closed and whenever an interceptor is built, invoked (`AllowRPC`) or closed (`CLOSE-START` on entry to `Close`, `CLOSE-END` on return; in the disable/re-enable and shutdown tests the probe interceptor's `Close` takes 200ms so that the two are distinguishable). `filterClosed=` is whether the underlying filter's `Close` had already run at that moment, `selfClosed=` whether the interceptor's own `Close` had already returned, and `via` is the chain of `internal/xds/server` functions on the calling stack (innermost first). The file only uses public/e2e APIs, so the identical file runs on every branch.

`TestVerifyH_DisableThenReenable` serves one RouteConfiguration with probe filter `F` enabled (gen1), then publishes a replacement in which `F` is disabled on every route through `typed_per_filter_config` (a successful, ACKed update; the test waits until an RPC is served without `F`).

```sh
verify/repro/run_probe.sh ee3cc2af test/xds '^Test$/^VerifyH_DisableThenReenable$' verify_harness_test.go
verify/repro/run_probe.sh audited  test/xds '^Test$/^VerifyH_DisableThenReenable$' verify_harness_test.go
```

[evalon/grpc-go-xd-ee3cc2af](https://github.com/kaitranntt-evals/grpc-go-xds-rds-interceptor-lifecycle-leak/tree/evalon/grpc-go-xd-ee3cc2af):

```console
---- event log: after disable-on-every-route replacement ----
  [00] FILTER F#1 BUILT (BuildServerFilter)
  [01] INTERCEPTOR F#1/i1[gen1] BUILT from FILTER F#1 filterClosed=false
  [02] INTERCEPTOR F#1/i1[gen1] AllowRPC selfClosed=false filterClosed=false
  [03] TEST: >>> publishing RDS with F disabled on every route
  [04] INTERCEPTOR F#1/i1[gen1] AllowRPC selfClosed=false filterClosed=false
  [05] FILTER F#1 CLOSED (underlying ServerFilter.Close ran) via (*refCountedServerFilter).Close < (*filterChain).constructUsableRouteConfiguration < (*listenerWrapper).handleRDSUpdate < (*rdsWatcher).ResourceChanged
  [06] INTERCEPTOR F#1/i1[gen1] CLOSE-START filterClosed=true via (*interceptorList).Close < (*virtualHostWithInterceptors).closeInterceptors < (*usableRouteConfiguration).closeInterceptors < (*filterChain).setUsableRouteConfiguration < (*listenerWrapper).handleRDSUpdate
  [07] TEST: RPC served by the replacement (F disabled) config
  [08] INTERCEPTOR F#1/i1[gen1] CLOSE-END filterClosed=true
OBSERVATION(C1/C9 replacement): FILTER-RELEASED-BEFORE-INTERCEPTOR-CLOSE: underlying filter Close at [05] precedes retired interceptor CLOSE-END at [08]
```

Audited branch [grpc-go-xds-rds-interceptor-lifecycle-leak-perfect](https://github.com/kaitranntt-evals/grpc-go/tree/grpc-go-xds-rds-interceptor-lifecycle-leak-perfect) (control):

```console
---- event log: after disable-on-every-route replacement ----
  [00] FILTER F#1 BUILT (BuildServerFilter)
  [01] INTERCEPTOR F#1/i1[gen1] BUILT from FILTER F#1 filterClosed=false
  [02] INTERCEPTOR F#1/i1[gen1] AllowRPC selfClosed=false filterClosed=false
  [03] TEST: >>> publishing RDS with F disabled on every route
  [04] INTERCEPTOR F#1/i1[gen1] AllowRPC selfClosed=false filterClosed=false
  [05] INTERCEPTOR F#1/i1[gen1] CLOSE-START filterClosed=false via (*interceptorList).Close < (*usableRouteConfiguration).stop < (*filterChain).applyConfiguration < (*filterChain).updateUsableRouteConfiguration < (*listenerWrapper).handleRDSUpdate
  [06] TEST: RPC served by the replacement (F disabled) config
  [07] INTERCEPTOR F#1/i1[gen1] CLOSE-END filterClosed=false
  [08] FILTER F#1 CLOSED (underlying ServerFilter.Close ran) via (*refCountedServerFilter).Close < (*filterChain).applyConfiguration < (*filterChain).updateUsableRouteConfiguration < (*listenerWrapper).handleRDSUpdate < (*rdsWatcher).ResourceChanged
OBSERVATION(C1/C9 replacement): INTERCEPTOR-CLOSED-BEFORE-FILTER-RELEASE: retired interceptor CLOSE-END at [07] precedes underlying filter Close at [08]
```

Branch source that the `via` chains point at (`internal/xds/server/filter_chain_manager.go` in the [evalon/grpc-go-xd-ee3cc2af](https://github.com/kaitranntt-evals/grpc-go-xds-rds-interceptor-lifecycle-leak/tree/evalon/grpc-go-xd-ee3cc2af) worktree, end of `constructUsableRouteConfiguration`, lines 424-429, and `setUsableRouteConfiguration`, lines 386-390):

```go
	for _, sf := range fc.serverFilters {
		sf.Close()
	}
	fc.serverFilters = serverFilters

	return &usableRouteConfiguration{vhs: vhs}
...
func (fc *filterChain) setUsableRouteConfiguration(urc *usableRouteConfiguration) {
	if old := fc.usableRouteConfiguration.Swap(urc); old != nil {
		old.closeInterceptors()
	}
}
```

Part "replacement release ordering": observed. The old configuration's filter reference is released at [05] from `(*refCountedServerFilter).Close < (*filterChain).constructUsableRouteConfiguration`, i.e. during construction of the replacement; the retired interceptor only starts closing at [06] from `(*usableRouteConfiguration).closeInterceptors < (*filterChain).setUsableRouteConfiguration` and finishes at [08].

Part "all-routes-disabled trigger": observed. Because the replacement disables `F` on every route, the reference released at [05] is the last one: the underlying `ServerFilter.Close` runs and returns at [05], and the dependent gen1 interceptor runs its whole `Close` against a closed filter (`CLOSE-START filterClosed=true` at [06], `CLOSE-END` at [08]).

On the audited branch the order is reversed (interceptor `CLOSE-END` at [07], filter closed at [08]).

Impact reasoning. An interceptor is built from its `ServerFilter` and may use filter-owned state in its own `Close`. On this branch, whenever a route update takes a filter out of use on every route of a filter chain, the filter is closed first and its interceptors are closed afterwards against the closed filter. Nothing fails at the xDS level, so this is silent; it needs a per-route "disabled" override on all routes, which is a legitimate but less common change. Whether an in-tree filter's interceptor touches its filter in `Close` was not exercised; the observation was made with the probe filter. A fix is to keep the old `fc.serverFilters` until the old configuration's interceptors are closed (release them after `old.closeInterceptors()`), which is the order the audited branch uses.

## C10

Claim: configuration replacement allows an admitted routing request to invoke an interceptor that replacement has already closed (parts: reader ownership; admitted-request trigger). Target: the audited branch [grpc-go-xds-rds-interceptor-lifecycle-leak-perfect](https://github.com/kaitranntt-evals/grpc-go/tree/grpc-go-xds-rds-interceptor-lifecycle-leak-perfect).

Probe. `verify/repro/verify_harness_test.go` is a black-box probe: it registers its own HTTP filter through `httpfilter.Register`, starts a real `xds.NewGRPCServer` against the in-process e2e management server, and pushes Listener/RouteConfiguration resources through that management server exactly like the repo's own `test/xds` tests. The probe filter appends one line to an ordered log whenever the underlying `ServerFilter` is built or closed and whenever an interceptor is built, invoked (`AllowRPC`) or closed (`CLOSE-START` on entry to `Close`, `CLOSE-END` on return; in the disable/re-enable and shutdown tests the probe interceptor's `Close` takes 200ms so that the two are distinguishable). `filterClosed=` is whether the underlying filter's `Close` had already run at that moment, `selfClosed=` whether the interceptor's own `Close` had already returned, and `via` is the chain of `internal/xds/server` functions on the calling stack (innermost first). The file only uses public/e2e APIs, so the identical file runs on every branch.

`TestVerifyH_PausedRequestReplacement` configures two probe filters `A` and `B` (in that order) on the served route. One RPC is admitted and parked inside `A[gen1].AllowRPC`, so the routing call has already acquired the gen1 configuration and has `B[gen1].AllowRPC` still to invoke. The test then publishes a replacement RouteConfiguration (gen2) through the management server, the normal RDS update path, waits (up to 5s) for `B[gen1]`'s `Close` to return, and only then lets the parked RPC continue.

```sh
verify/repro/run_probe.sh audited test/xds '^Test$/^VerifyH_PausedRequestReplacement$' verify_harness_test.go
```

```console
---- event log: paused request across replacement ----
  [00] FILTER A#1 BUILT (BuildServerFilter)
  [01] INTERCEPTOR A#1/i1[gen1] BUILT from FILTER A#1 filterClosed=false
  [02] FILTER B#1 BUILT (BuildServerFilter)
  [03] INTERCEPTOR B#1/i1[gen1] BUILT from FILTER B#1 filterClosed=false
  [04] INTERCEPTOR A#1/i1[gen1] AllowRPC selfClosed=false filterClosed=false
  [05] TEST: request admitted and paused inside A[gen1].AllowRPC (B[gen1].AllowRPC not yet invoked)
  [06] TEST: >>> publishing replacement RDS (gen2) through the management server
  [07] INTERCEPTOR A#1/i2[gen2] BUILT from FILTER A#1 filterClosed=false
  [08] INTERCEPTOR B#1/i2[gen2] BUILT from FILTER B#1 filterClosed=false
  [09] INTERCEPTOR A#1/i1[gen1] CLOSE-START filterClosed=false via (*interceptorList).Close < (*usableRouteConfiguration).stop < (*filterChain).applyConfiguration < (*filterChain).updateUsableRouteConfiguration < (*listenerWrapper).handleRDSUpdate
  [10] INTERCEPTOR A#1/i1[gen1] CLOSE-END filterClosed=false
  [11] INTERCEPTOR B#1/i1[gen1] CLOSE-START filterClosed=false via (*interceptorList).Close < (*usableRouteConfiguration).stop < (*filterChain).applyConfiguration < (*filterChain).updateUsableRouteConfiguration < (*listenerWrapper).handleRDSUpdate
  [12] INTERCEPTOR B#1/i1[gen1] CLOSE-END filterClosed=false
  [13] TEST: B[gen1] Close finished while request still paused = true; resuming request
  [14] INTERCEPTOR B#1/i1[gen1] AllowRPC selfClosed=true filterClosed=false
  [15] TEST: paused RPC returned err=<nil>
OBSERVATION(C4/C10): CLOSED-INTERCEPTOR-INVOKED: INTERCEPTOR B#1/i1[gen1] AllowRPC selfClosed=true filterClosed=false (after its CLOSE-END at [12])
```

Part "reader ownership": lines [05] to [12] show the configuration's interceptors being closed while a routing reader that acquired that configuration still has an invocation to perform. The request is parked in `A[gen1].AllowRPC` at [05]; `A[gen1]` and `B[gen1]` are closed at [09] to [12] via `(*usableRouteConfiguration).stop < (*filterChain).applyConfiguration`; line [13] records that `B[gen1]`'s `Close` had finished while the request was still paused. Nothing protected the reader's configuration. Observed.

Part "admitted-request trigger": line [14], `INTERCEPTOR B#1/i1[gen1] AllowRPC selfClosed=true`, is the resumed request invoking `B[gen1]` after its `CLOSE-END` at [12]; the replacement was an ordinary RDS update. Observed.

Controls with the identical probe (`run_probe.sh <id> ...` instead of `audited`), to show the probe can produce the other outcome:

```console
base      OBSERVATION(C4/C10): LIVE-INTERCEPTOR-INVOKED: INTERCEPTOR B#1/i1[gen1] AllowRPC selfClosed=false filterClosed=false (closedWhilePaused=false)
d5ce5e46  OBSERVATION(C4/C10): LIVE-INTERCEPTOR-INVOKED: INTERCEPTOR B#1/i1[gen1] AllowRPC selfClosed=false filterClosed=false (closedWhilePaused=false)
55cd92cd  OBSERVATION(C4/C10): LIVE-INTERCEPTOR-INVOKED: INTERCEPTOR B#1/i1[gen1] AllowRPC selfClosed=false filterClosed=false (closedWhilePaused=false)
dc4ed267  OBSERVATION(C4/C10): LIVE-INTERCEPTOR-INVOKED: INTERCEPTOR B#1/i1[gen1] AllowRPC selfClosed=false filterClosed=false (closedWhilePaused=false)
```

`base` is the task's base commit `4ee6ac46fada` (it never closes interceptors, which is the leak the task is about); the other three are claim-target branches ([evalon/grpc-go-xd-d5ce5e46](https://github.com/kaitranntt-evals/grpc-go-xds-rds-interceptor-lifecycle-leak/tree/evalon/grpc-go-xd-d5ce5e46), [evalon/grpc-go-xd-55cd92cd](https://github.com/kaitranntt-evals/grpc-go-xds-rds-interceptor-lifecycle-leak/tree/evalon/grpc-go-xd-55cd92cd), [evalon/grpc-go-xd-dc4ed267](https://github.com/kaitranntt-evals/grpc-go-xds-rds-interceptor-lifecycle-leak/tree/evalon/grpc-go-xd-dc4ed267)) on which closure waited for the parked request (`closedWhilePaused=false`).

Impact reasoning. `resolver.ServerInterceptor` documents `Close` as "Close closes the interceptor. Once called, no new calls ... are accepted" (`internal/resolver/config_selector.go`). On the audited branch a route update closes the retired configuration's interceptors as soon as the new configuration is published, without regard to RPCs that already picked up the old configuration, so such an RPC goes on to call `AllowRPC` on an interceptor whose `Close` has returned. The trigger is the most ordinary one there is (any RouteConfiguration update while RPCs are arriving); the window per RPC is the time between routing picking up the configuration and its last `AllowRPC`, which is short unless an interceptor blocks, and the probe widens it by parking the request. What the RPC then experiences depends on the filter: the probe interceptor keeps answering after `Close` and the RPC returned `err=<nil>`, so nothing is visibly wrong; an interceptor that releases state in `Close` would be used after release. The eval fixture (all five tests pass on this branch, see Setup) does not cover this schedule.

## C11

Claim: successful route updates that disable and then re-enable a filter cause interceptor construction to reuse a closed cached filter instance (parts: closed cache entry lookup; disable-and-re-enable trigger). Target: the audited branch [grpc-go-xds-rds-interceptor-lifecycle-leak-perfect](https://github.com/kaitranntt-evals/grpc-go/tree/grpc-go-xds-rds-interceptor-lifecycle-leak-perfect).

### Part "closed cache entry lookup"

`verify/repro/verify_c5_c11_cache_test.go` (package `server`) calls the lookup function the listener uses, `getOrCreateServerFilterWithMap`, on a cache map: first lookup, release of the only reference through the wrapper's `Close`, second lookup with the same key.

```sh
verify/repro/run_probe.sh audited internal/xds/server '^Test$/^VerifyC5C11_' verify_c5_c11_cache_test.go
```

```console
C5/C11 after 1st lookup:  BuildServerFilter calls=1 refCnt=1 underlying Close calls=0
C5/C11 after last release: refCnt=0 underlying Close calls=1 entry still in cache=true
C5/C11 after 2nd lookup:  BuildServerFilter calls=1 sameWrapper=true sameUnderlying=true refCnt=1 underlying Close calls=1
C5/C11 OBSERVATION: CLOSED-WRAPPER-RETURNED: lookup incremented (refCnt 0 -> 1) and returned the wrapper whose underlying filter was already closed
C5/C11 after releasing 2nd: refCnt=0 underlying Close calls=2
--- PASS: Test (0.00s)
    --- PASS: Test/VerifyC5C11_ClosedCacheEntryLookup (0.00s)
ok  	google.golang.org/grpc/internal/xds/server	1.027s
exit=0
```

Observed: after the last release the wrapper is at `refCnt=0`, the underlying filter's `Close` has run once, and the entry is still in the cache. The second lookup does not call `BuildServerFilter` again (`calls=1`); it increments the same wrapper from 0 to 1 and returns it, with the same, already closed, underlying filter. Releasing that second reference runs the underlying `Close` a second time (`Close calls=2`).

### Part "disable-and-re-enable trigger"

`verify/repro/verify_harness_test.go` is a black-box probe: it registers its own HTTP filter through `httpfilter.Register`, starts a real `xds.NewGRPCServer` against the in-process e2e management server, and pushes Listener/RouteConfiguration resources through that management server exactly like the repo's own `test/xds` tests. The probe filter appends one line to an ordered log whenever the underlying `ServerFilter` is built or closed and whenever an interceptor is built, invoked (`AllowRPC`) or closed (`CLOSE-START` on entry to `Close`, `CLOSE-END` on return; in the disable/re-enable and shutdown tests the probe interceptor's `Close` takes 200ms so that the two are distinguishable). `filterClosed=` is whether the underlying filter's `Close` had already run at that moment, `selfClosed=` whether the interceptor's own `Close` had already returned, and `via` is the chain of `internal/xds/server` functions on the calling stack (innermost first). The file only uses public/e2e APIs, so the identical file runs on every branch.

`TestVerifyH_DisableThenReenable` serves a RouteConfiguration with probe filter `F` enabled (gen1), publishes a successful update that disables `F` on every route (`typed_per_filter_config`), waits until an RPC is served without `F`, then publishes a successful update that re-enables it (gen3) and sends an RPC.

```sh
verify/repro/run_probe.sh audited test/xds '^Test$/^VerifyH_DisableThenReenable$' verify_harness_test.go
```

```console
---- event log: after re-enable ----
  [00] FILTER F#1 BUILT (BuildServerFilter)
  [01] INTERCEPTOR F#1/i1[gen1] BUILT from FILTER F#1 filterClosed=false
  [02] INTERCEPTOR F#1/i1[gen1] AllowRPC selfClosed=false filterClosed=false
  [03] TEST: >>> publishing RDS with F disabled on every route
  [04] INTERCEPTOR F#1/i1[gen1] AllowRPC selfClosed=false filterClosed=false
  [05] INTERCEPTOR F#1/i1[gen1] CLOSE-START filterClosed=false via (*interceptorList).Close < (*usableRouteConfiguration).stop < (*filterChain).applyConfiguration < (*filterChain).updateUsableRouteConfiguration < (*listenerWrapper).handleRDSUpdate
  [06] TEST: RPC served by the replacement (F disabled) config
  [07] INTERCEPTOR F#1/i1[gen1] CLOSE-END filterClosed=false
  [08] FILTER F#1 CLOSED (underlying ServerFilter.Close ran) via (*refCountedServerFilter).Close < (*filterChain).applyConfiguration < (*filterChain).updateUsableRouteConfiguration < (*listenerWrapper).handleRDSUpdate < (*rdsWatcher).ResourceChanged
  [09] TEST: >>> publishing RDS with F re-enabled (gen3)
  [10] INTERCEPTOR F#1/i2[gen3] BUILT from FILTER F#1 filterClosed=true
  [11] INTERCEPTOR F#1/i2[gen3] AllowRPC selfClosed=false filterClosed=true
OBSERVATION(C5/C11 re-enable): BuildServerFilter calls=1; "INTERCEPTOR F#1/i2[gen3] BUILT from FILTER F#1 filterClosed=true"; "INTERCEPTOR F#1/i2[gen3] AllowRPC selfClosed=false filterClosed=true"
OBSERVATION(C5/C11 re-enable): CLOSED-FILTER-REUSED: re-enabled interceptor was built from an already-closed filter instance
```

Observed: the only filter instance ever built (`BuildServerFilter calls=1`) is closed at [08] when the disabling update drops its last reference; the re-enabling update builds interceptor `i2[gen3]` from that same instance (`BUILT from FILTER F#1 filterClosed=true`) and RPCs are then served through it ([11]). All three updates were accepted and served. At shutdown the same instance is closed a second time (lines [12] to [14] of the "after server Stop" log in `verify/logs/A.audited.out`: `FILTER F#1 CLOSED ... via (*refCountedServerFilter).Close < (*filterChain).stop`).

Controls with the identical probe:

```console
base      OBSERVATION(C5/C11 re-enable): BuildServerFilter calls=1; "INTERCEPTOR F#1/i2[gen3] BUILT from FILTER F#1 filterClosed=true"; "INTERCEPTOR F#1/i2[gen3] AllowRPC selfClosed=false filterClosed=true"
dc4ed267  OBSERVATION(C5/C11 re-enable): BuildServerFilter calls=1; "INTERCEPTOR F#1/i2[gen3] BUILT from FILTER F#1 filterClosed=false"; "INTERCEPTOR F#1/i2[gen3] AllowRPC selfClosed=false filterClosed=false"
55cd92cd  OBSERVATION(C5/C11 re-enable): BuildServerFilter calls=2; "INTERCEPTOR F#2/i1[gen3] BUILT from FILTER F#2 filterClosed=false"; "INTERCEPTOR F#2/i1[gen3] AllowRPC selfClosed=false filterClosed=false"
```

`base` is the task's base commit `4ee6ac46fada`: the reuse of a closed filter instance already exists there, so the audited change did not introduce it, it left it in place. [evalon/grpc-go-xd-dc4ed267](https://github.com/kaitranntt-evals/grpc-go-xds-rds-interceptor-lifecycle-leak/tree/evalon/grpc-go-xd-dc4ed267) and [evalon/grpc-go-xd-55cd92cd](https://github.com/kaitranntt-evals/grpc-go-xds-rds-interceptor-lifecycle-leak/tree/evalon/grpc-go-xd-55cd92cd) are claim-target branches on which the same sequence ends with a live filter.

Impact reasoning. `httpfilter.ServerFilter.Close` is documented as "Close is called when the filter is no longer needed". On the audited branch a filter instance that has been told so is put back into service by the next route update that enables it again, and is later closed a second time. Nothing fails at the xDS level (updates are ACKed, RPCs are routed), so the server looks healthy; what happens inside depends on the filter: one whose `Close` releases resources is asked to build interceptors and authorize RPCs after releasing them, and must tolerate a double `Close`. The trigger needs a filter to be disabled on every route of a filter chain (per-route override) and enabled again later without the Listener changing in between; that is a legitimate but not everyday rollout pattern. No workaround was exercised.
