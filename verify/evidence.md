Observations for audit run `v-a332a9ce` of [grpc-go-xds-rds-interceptor-lifecycle-leak-perfect](https://github.com/kaitranntt-evals/grpc-go/tree/grpc-go-xds-rds-interceptor-lifecycle-leak-perfect). Every block below is a command that was run and its output; long test logs are filtered to the lines printed by the audit repro (the filter is part of the command or stated next to it). `<wt>` is the directory holding one checkout per branch.

## Setup

```sh
cd ~/repos/grpc-go
git fetch origin grpc-go-xds-rds-interceptor-lifecycle-leak-perfect
git checkout -b verify/grpc-go-xds-rds-interceptor-lifecycle-leak-v-a332a9ce origin/grpc-go-xds-rds-interceptor-lifecycle-leak-perfect
unzip -o eval_tests.zip -d ~/eval          # -> ~/eval/tests/eval_xds_server_interceptor_leak_test.go (46975 bytes)
verify/repro/setup_worktrees.sh ~/wt       # one detached worktree per claim-target branch, plus perfect (audited branch) and base
verify/repro/replay_all.sh ~/wt ~/eval/tests/eval_xds_server_interceptor_leak_test.go ~/logs/final
```

```console
$ go version
go version go1.25.7 linux/amd64
$ git rev-parse HEAD origin/grpc-go-xds-rds-interceptor-lifecycle-leak-perfect      # audited branch
614cb7398c43bf270878ac15f34b68f5a9a4159d
614cb7398c43bf270878ac15f34b68f5a9a4159d
$ sha256sum ~/eval/tests/eval_xds_server_interceptor_leak_test.go
7309195ff8bc07c6e4850e7277332fbb4d56920fdb841d727b9a16a07b3bc04f  eval_xds_server_interceptor_leak_test.go
$ for b in ~/wt/*; do echo "$(basename $b) $(git -C $b rev-parse --short HEAD) mb=$(git -C $b merge-base HEAD 4ee6ac46 | cut -c1-8) n=$(git -C $b rev-list --count 4ee6ac46..HEAD)"; done
0723d2f4 eb765f2e mb=4ee6ac46 n=1
0ed1f32c 10d73679 mb=4ee6ac46 n=1
2a01d623 0ccf0147 mb=4ee6ac46 n=1
2ffad480 784e2b0b mb=4ee6ac46 n=1
311db1b4 5dc5968c mb=4ee6ac46 n=1
37fb43a6 2b0c4d7c mb=4ee6ac46 n=1
3e0a44dd 68d87d63 mb=4ee6ac46 n=1
415a74da f70da5b8 mb=4ee6ac46 n=1
57bb4302 328f47dc mb=4ee6ac46 n=1
6851db1c a1200e55 mb=4ee6ac46 n=1
6cf08267 1b50e1dd mb=4ee6ac46 n=1
74924778 e4739699 mb=4ee6ac46 n=1
79540ac1 a01cc52b mb=4ee6ac46 n=1
98faa6aa 303f98bc mb=4ee6ac46 n=1
b7bc0d7f 6ee04f40 mb=4ee6ac46 n=1
base 4ee6ac46 mb=4ee6ac46 n=0
bdc42e7c 21b002fc mb=4ee6ac46 n=1
cf01ba8b 0ce3e898 mb=4ee6ac46 n=1
dab66e4f 3133831c mb=4ee6ac46 n=1
dad62956 30fef0af mb=4ee6ac46 n=1
e03d1e42 fc9cb150 mb=4ee6ac46 n=1
eababd58 de70f1b0 mb=4ee6ac46 n=1
eb19a38b 7c0ac8b4 mb=4ee6ac46 n=1
eb9c66bf 0a3344da mb=4ee6ac46 n=1
perfect 614cb739 mb=4ee6ac46 n=3
$ go build ./... && echo build-ok; go vet ./internal/xds/server/... ./test/xds/... && echo vet-ok      # audited branch
build-ok
vet-ok
```

Claim-target branches live in `kaitranntt-evals/grpc-go-xds-rds-interceptor-lifecycle-leak` (remote `claims`; in this session the remote URL was the workspace's git proxy for that repository, `setup_worktrees.sh` uses the public URL); each is one commit on top of base `4ee6ac46`. The audited branch is three commits on top of the same base.

The eval fixture, provisioned byte-exact (`cp` + `cmp`) at `test/xds/eval_xds_server_interceptor_leak_test.go`, passes on the audited branch and on the three single-branch claim targets whose claims name it:

```console
$ cp ~/eval/tests/eval_xds_server_interceptor_leak_test.go test/xds/ && cmp ~/eval/tests/eval_xds_server_interceptor_leak_test.go test/xds/eval_xds_server_interceptor_leak_test.go && echo "cmp: identical" && go test -race -count=1 -v -run '^Test$/^Eval_' ./test/xds | grep -E '^\s+--- (PASS|FAIL): Test/|^(ok|FAIL)'
# perfect
cmp: identical
    --- PASS: Test/Eval_ServerSideXDS_InterceptorLeak_MultiGenerationRDSUpdate (0.06s)
    --- PASS: Test/Eval_ServerSideXDS_InterceptorLeak_RDSUpdate (0.04s)
    --- PASS: Test/Eval_ServerSideXDS_InterceptorSwapOrder (0.03s)
    --- PASS: Test/Eval_ServerSideXDS_PartialRouteFailure_ClosesInterceptors (0.03s)
    --- PASS: Test/Eval_ServerSideXDS_PartialVirtualHostFailure_ClosesInterceptors (0.03s)
ok  	google.golang.org/grpc/test/xds	1.225s
# b7bc0d7f
cmp: identical
    --- PASS: Test/Eval_ServerSideXDS_InterceptorLeak_MultiGenerationRDSUpdate (0.11s)
    --- PASS: Test/Eval_ServerSideXDS_InterceptorLeak_RDSUpdate (0.03s)
    --- PASS: Test/Eval_ServerSideXDS_InterceptorSwapOrder (0.03s)
    --- PASS: Test/Eval_ServerSideXDS_PartialRouteFailure_ClosesInterceptors (0.03s)
    --- PASS: Test/Eval_ServerSideXDS_PartialVirtualHostFailure_ClosesInterceptors (0.03s)
ok  	google.golang.org/grpc/test/xds	1.243s
# 57bb4302
cmp: identical
    --- PASS: Test/Eval_ServerSideXDS_InterceptorLeak_MultiGenerationRDSUpdate (0.06s)
    --- PASS: Test/Eval_ServerSideXDS_InterceptorLeak_RDSUpdate (0.03s)
    --- PASS: Test/Eval_ServerSideXDS_InterceptorSwapOrder (0.03s)
    --- PASS: Test/Eval_ServerSideXDS_PartialRouteFailure_ClosesInterceptors (0.03s)
    --- PASS: Test/Eval_ServerSideXDS_PartialVirtualHostFailure_ClosesInterceptors (0.03s)
ok  	google.golang.org/grpc/test/xds	1.210s
# 37fb43a6
cmp: identical
    --- PASS: Test/Eval_ServerSideXDS_InterceptorLeak_MultiGenerationRDSUpdate (0.05s)
    --- PASS: Test/Eval_ServerSideXDS_InterceptorLeak_RDSUpdate (0.03s)
    --- PASS: Test/Eval_ServerSideXDS_InterceptorSwapOrder (0.08s)
    --- PASS: Test/Eval_ServerSideXDS_PartialRouteFailure_ClosesInterceptors (0.03s)
    --- PASS: Test/Eval_ServerSideXDS_PartialVirtualHostFailure_ClosesInterceptors (0.03s)
ok  	google.golang.org/grpc/test/xds	1.245s
```

Audit instrumentation (all under `verify/instrumentation/`, applied only to throw-away worktrees, never committed into production paths):

- `apply_trace.sh` adds `verify_trace.go` and one hook call at the top of `refCountedServerFilter.incRef` and `refCountedServerFilter.Close` (no-op unless a test installs the hook).
- `apply_sched_probes.sh` adds `verify_probe.go` and one `time.Sleep` at the top of `interceptorList.Close` (`VERIFY_CLOSE_DELAY`) and of `constructUsableRouteConfiguration` (`VERIFY_CONSTRUCT_DELAY`); no-ops when the variables are unset. The sleeps do not reorder any operation, they only widen windows that already exist.
- `verify/go.mod` is a nested module whose only purpose is to keep `verify/` out of the repository's `./...` patterns (without it `go list ./...` in the repository root reports `found packages xds (c1_filter_ref_order_test.go) and server (c5_c11_cache_lookup_test.go) in /home/ubuntu/repos/grpc-go/verify/repro`).

## C1

Claim: during active configuration replacement or shutdown, retired server filter references are released before all interceptors that depend on those filters finish closing. Adjudicated independently on 14 branches.

Method. `verify/repro/c1_filter_ref_order_test.go` runs a live `xds.NewGRPCServer` with one filter chain and one traced HTTP filter, and logs a global sequence number for `ref-acquire`/`ref-release` (the hook in `refCountedServerFilter.incRef`/`Close`, with the reference count *before* the operation), `icpt-build`/`icpt-close` (interceptor instances of the traced filter) and `filter-build`/`filter-close` (the underlying `ServerFilter`). Each event carries its call chain (`via ...`). Phases: `initial`, `rds-replace-filter-kept` (in-place RDS replacement, filter still used), `rds-replace-filter-disabled-on-every-route` (in-place RDS replacement, filter disabled on every route), `lds-replace` (Listener replacement) and `server-stop`. Per phase the test prints `RELEASE_BEFORE_CLOSE` (a `ref-release` precedes the `icpt-close` of an interceptor that was open at phase start) and `FILTER_CLOSED_BEFORE_INTERCEPTORS`. The `rds-replace-filter-disabled-on-every-route` phase disables the filter with a virtual-host-level override, which applies to every route; `FilterConfig{disabled: true}` overrides are honored only when `GRPC_EXPERIMENTAL_XDS_EXT_PROC_ON_CLIENT=true` (`envconfig.XDSClientExtProcEnabled`, default false; the test sets it with `testutils.SetEnvConfig`). A third test, `VerifyC1_ReplaceKeep_Then_NoVirtualHosts`, therefore repeats the two RDS phases with default settings (no flag): phases `noflag-rds-replace-filter-kept` and `noflag-rds-replace-no-virtual-hosts`, where the replacement is a RouteConfiguration of the same name with no virtual hosts, which also removes every use of the filter. All replacements in the trace complete successfully, so no event is cleanup of an unpublished partial construction: every `ref-release` is the release of a reference held by the published, now-retired configuration (the reference count before the first release is `old + new`).

Command, per branch (`<id>` is the branch id; `c1_c8_trace.sh` applies `apply_trace.sh`, copies the test to `test/xds/` and runs `go test -race -count=1 -v -run '^Test$/^VerifyC1_' ./test/xds`, keeping the `PHASE` and result lines). The per-branch outputs below were taken when the test file had two tests; the no-flag test was added afterwards and its outputs (a second run of the same script on every branch) are in their own subsection:

```sh
verify/repro/c1_c8_trace.sh ~/wt/<id>
```

Result summary (one line per branch, taken from the outputs below):

| branch | HEAD | RDS replacement, filter kept | RDS replacement, filter disabled on every route | LDS replacement | Server.Stop |
|---|---|---|---|---|---|
| eababd58 | de70f1b0 | ref-release #7, last icpt-close #8 → **release first** | ref-release #9, last icpt-close #13, filter-close #11 → **release first**, **filter closed first** | ref-release #6, last icpt-close #5 → interceptors first | ref-release #8, last icpt-close #7, filter-close #9 → interceptors first |
| b7bc0d7f | 6ee04f40 | ref-release #7, last icpt-close #8 → **release first** | ref-release #9, last icpt-close #13, filter-close #11 → **release first**, **filter closed first** | ref-release #6, last icpt-close #5 → interceptors first | ref-release #8, last icpt-close #7, filter-close #9 → interceptors first |
| dab66e4f | 3133831c | ref-release #7, last icpt-close #8 → **release first** | ref-release #9, last icpt-close #13, filter-close #11 → **release first**, **filter closed first** | ref-release #6, last icpt-close #5 → interceptors first | ref-release #8, last icpt-close #7, filter-close #9 → interceptors first |
| 415a74da | f70da5b8 | ref-release #7, last icpt-close #8 → **release first** | ref-release #9, last icpt-close #13, filter-close #11 → **release first**, **filter closed first** | ref-release #6, last icpt-close #5 → interceptors first | ref-release #8, last icpt-close #7, filter-close #9 → interceptors first |
| 0723d2f4 | eb765f2e | ref-release #7, last icpt-close #8 → **release first** | ref-release #9, last icpt-close #13, filter-close #11 → **release first**, **filter closed first** | ref-release #6, last icpt-close #5 → interceptors first | ref-release #8, last icpt-close #7, filter-close #9 → interceptors first |
| 0ed1f32c | 10d73679 | ref-release #7, last icpt-close #8 → **release first** | ref-release #9, last icpt-close #13, filter-close #11 → **release first**, **filter closed first** | ref-release #6, last icpt-close #5 → interceptors first | ref-release #8, last icpt-close #7, filter-close #9 → interceptors first |
| eb9c66bf | 0a3344da | ref-release #7, last icpt-close #8 → **release first** | ref-release #9, last icpt-close #13, filter-close #11 → **release first**, **filter closed first** | ref-release #6, last icpt-close #5 → interceptors first | ref-release #8, last icpt-close #7, filter-close #9 → interceptors first |
| e03d1e42 | fc9cb150 | ref-release #7, last icpt-close #8 → **release first** | ref-release #9, last icpt-close #13, filter-close #11 → **release first**, **filter closed first** | ref-release #6, last icpt-close #5 → interceptors first | ref-release #8, last icpt-close #7, filter-close #9 → interceptors first |
| 98faa6aa | 303f98bc | ref-release #7, last icpt-close #8 → **release first** | ref-release #9, last icpt-close #13, filter-close #11 → **release first**, **filter closed first** | ref-release #6, last icpt-close #5 → interceptors first | ref-release #8, last icpt-close #7, filter-close #9 → interceptors first |
| 2ffad480 | 784e2b0b | ref-release #7, last icpt-close #8 → **release first** | ref-release #9, last icpt-close #13, filter-close #11 → **release first**, **filter closed first** | ref-release #6, last icpt-close #5 → interceptors first | ref-release #8, last icpt-close #7, filter-close #9 → interceptors first |
| dad62956 | 30fef0af | ref-release #7, last icpt-close #8 → **release first** | ref-release #9, last icpt-close #13, filter-close #11 → **release first**, **filter closed first** | ref-release #6, last icpt-close #5 → interceptors first | ref-release #8, last icpt-close #7, filter-close #9 → interceptors first |
| 6851db1c | a1200e55 | ref-release #7, last icpt-close #8 → **release first** | ref-release #9, last icpt-close #13, filter-close #11 → **release first**, **filter closed first** | ref-release #6, last icpt-close #5 → interceptors first | ref-release #8, last icpt-close #7, filter-close #9 → interceptors first |
| 3e0a44dd | 68d87d63 | ref-release #7, last icpt-close #8 → **release first** | ref-release #9, last icpt-close #13, filter-close #11 → **release first**, **filter closed first** | ref-release #6, last icpt-close #5 → interceptors first | ref-release #8, last icpt-close #7, filter-close #9 → interceptors first |
| eb19a38b | 7c0ac8b4 | ref-release #7, last icpt-close #8 → **release first** | ref-release #9, last icpt-close #13, filter-close #11 → **release first**, **filter closed first** | ref-release #6, last icpt-close #5 → interceptors first | ref-release #8, last icpt-close #7, filter-close #9 → interceptors first |
| audited branch (control) | 614cb739 | ref-release #8, last icpt-close #7 → interceptors first | ref-release #11, last icpt-close #10, filter-close #13 → interceptors first | ref-release #6, last icpt-close #5 → interceptors first | ref-release #8, last icpt-close #7, filter-close #9 → interceptors first |


On all 14 claim branches the in-place RDS replacement releases the retired configuration's filter reference (event #07, from `constructUsableRouteConfiguration`) before the retired interceptor is closed (event #08, from code that runs after construction has returned — `setUsableRouteConfiguration`, `updateUsableRouteConfiguration`, `swapUsableRouteConfiguration` or `handleRDSUpdate` itself, depending on the branch); when the replacement disables the filter on every route the final reference is released and the filter's `Close` runs (#11) before the two retired interceptors close (#12, #13). Listener replacement and `Server.Stop` close interceptors before releasing references on every branch. The audited branch (control, not a C1 target) closes interceptors first in every phase.

Impact reasoning. An interceptor is built by, and may depend on, its `ServerFilter`. With the order observed here the filter's reference is dropped while its interceptors are still open; whenever that was the last reference (a replacement that removes every use of the filter: a disabled override under the experimental flag, or with default settings a RouteConfiguration without virtual hosts) the filter's `Close` completes before its interceptors' `Close` runs, so an interceptor's `Close` runs against an already-closed parent filter. When the new configuration still uses the filter the count stays above zero and the misordering has no visible effect. No workaround exists short of never removing every use of a filter through RDS.

Full output of the two RDS phases plus the `SUMMARY` line of every phase, per branch:

### C1 · [evalon/grpc-go-xd-eababd58](https://github.com/kaitranntt-evals/grpc-go-xds-rds-interceptor-lifecycle-leak/tree/evalon/grpc-go-xd-eababd58) (HEAD de70f1b0)

```console
$ verify/repro/c1_c8_trace.sh ~/wt/eababd58
PHASE lds-replace: SUMMARY first ref-release=#6 first filter-close=#-1 last retired icpt-close=#5 retired-interceptors-never-closed=0 RELEASE_BEFORE_CLOSE=false FILTER_CLOSED_BEFORE_INTERCEPTORS=false
PHASE server-stop: SUMMARY first ref-release=#8 first filter-close=#9 last retired icpt-close=#7 retired-interceptors-never-closed=0 RELEASE_BEFORE_CLOSE=false FILTER_CLOSED_BEFORE_INTERCEPTORS=false
PHASE initial: SUMMARY first ref-release=#-1 first filter-close=#-1 last retired icpt-close=#-1 retired-interceptors-never-closed=0 RELEASE_BEFORE_CLOSE=false FILTER_CLOSED_BEFORE_INTERCEPTORS=false
PHASE rds-replace-filter-kept: 1 interceptor(s) open at phase start
PHASE rds-replace-filter-kept: #03 ref-acquire   F1 (refCnt before=1)     via getOrCreateServerFilterWithMap < (*listenerWrapper).getOrCreateServerFilterLocked < (*filterChain).newInterceptor < (*filterChain).convertVirtualHost < (*filterChain).constructUsableRouteConfiguration < (*listenerWrapper).handleRDSUpdate < (*rdsWatcher).ResourceChanged
PHASE rds-replace-filter-kept: #04 icpt-build    F1/I2                    via (*filterChain).newInterceptor < (*filterChain).convertVirtualHost < (*filterChain).constructUsableRouteConfiguration < (*listenerWrapper).handleRDSUpdate < (*rdsWatcher).ResourceChanged
PHASE rds-replace-filter-kept: #05 ref-acquire   F1 (refCnt before=2)     via getOrCreateServerFilterWithMap < (*listenerWrapper).getOrCreateServerFilterLocked < (*filterChain).newInterceptor < (*filterChain).convertVirtualHost < (*filterChain).constructUsableRouteConfiguration < (*listenerWrapper).handleRDSUpdate < (*rdsWatcher).ResourceChanged
PHASE rds-replace-filter-kept: #06 icpt-build    F1/I3                    via (*filterChain).newInterceptor < (*filterChain).convertVirtualHost < (*filterChain).constructUsableRouteConfiguration < (*listenerWrapper).handleRDSUpdate < (*rdsWatcher).ResourceChanged
PHASE rds-replace-filter-kept: #07 ref-release   F1 (refCnt before=3)     via (*filterChain).constructUsableRouteConfiguration < (*listenerWrapper).handleRDSUpdate < (*rdsWatcher).ResourceChanged
PHASE rds-replace-filter-kept: #08 icpt-close    F1/I1                    via (*usableRouteConfiguration).closeInterceptors < swapUsableRouteConfiguration < (*listenerWrapper).handleRDSUpdate < (*rdsWatcher).ResourceChanged
PHASE rds-replace-filter-kept: SUMMARY first ref-release=#7 first filter-close=#-1 last retired icpt-close=#8 retired-interceptors-never-closed=0 RELEASE_BEFORE_CLOSE=true FILTER_CLOSED_BEFORE_INTERCEPTORS=false
PHASE rds-replace-filter-disabled-on-every-route: 2 interceptor(s) open at phase start
PHASE rds-replace-filter-disabled-on-every-route: #09 ref-release   F1 (refCnt before=2)     via (*filterChain).constructUsableRouteConfiguration < (*listenerWrapper).handleRDSUpdate < (*rdsWatcher).ResourceChanged
PHASE rds-replace-filter-disabled-on-every-route: #10 ref-release   F1 (refCnt before=1)     via (*filterChain).constructUsableRouteConfiguration < (*listenerWrapper).handleRDSUpdate < (*rdsWatcher).ResourceChanged
PHASE rds-replace-filter-disabled-on-every-route: #11 filter-close  F1                       via (*filterChain).constructUsableRouteConfiguration < (*listenerWrapper).handleRDSUpdate < (*rdsWatcher).ResourceChanged
PHASE rds-replace-filter-disabled-on-every-route: #12 icpt-close    F1/I2                    via (*usableRouteConfiguration).closeInterceptors < swapUsableRouteConfiguration < (*listenerWrapper).handleRDSUpdate < (*rdsWatcher).ResourceChanged
PHASE rds-replace-filter-disabled-on-every-route: #13 icpt-close    F1/I3                    via (*usableRouteConfiguration).closeInterceptors < swapUsableRouteConfiguration < (*listenerWrapper).handleRDSUpdate < (*rdsWatcher).ResourceChanged
PHASE rds-replace-filter-disabled-on-every-route: SUMMARY first ref-release=#9 first filter-close=#11 last retired icpt-close=#13 retired-interceptors-never-closed=0 RELEASE_BEFORE_CLOSE=true FILTER_CLOSED_BEFORE_INTERCEPTORS=true
--- PASS: Test (4.13s)
--- PASS: Test/VerifyC1_LDSReplace_Then_Stop (2.05s)
--- PASS: Test/VerifyC1_ReplaceKeep_Then_DisableAll (2.08s)
ok  	google.golang.org/grpc/test/xds	5.162s
```

### C1 · [evalon/grpc-go-xd-b7bc0d7f](https://github.com/kaitranntt-evals/grpc-go-xds-rds-interceptor-lifecycle-leak/tree/evalon/grpc-go-xd-b7bc0d7f) (HEAD 6ee04f40)

```console
$ verify/repro/c1_c8_trace.sh ~/wt/b7bc0d7f
PHASE lds-replace: SUMMARY first ref-release=#6 first filter-close=#-1 last retired icpt-close=#5 retired-interceptors-never-closed=0 RELEASE_BEFORE_CLOSE=false FILTER_CLOSED_BEFORE_INTERCEPTORS=false
PHASE server-stop: SUMMARY first ref-release=#8 first filter-close=#9 last retired icpt-close=#7 retired-interceptors-never-closed=0 RELEASE_BEFORE_CLOSE=false FILTER_CLOSED_BEFORE_INTERCEPTORS=false
PHASE initial: SUMMARY first ref-release=#-1 first filter-close=#-1 last retired icpt-close=#-1 retired-interceptors-never-closed=0 RELEASE_BEFORE_CLOSE=false FILTER_CLOSED_BEFORE_INTERCEPTORS=false
PHASE rds-replace-filter-kept: 1 interceptor(s) open at phase start
PHASE rds-replace-filter-kept: #03 ref-acquire   F1 (refCnt before=1)     via getOrCreateServerFilterWithMap < (*listenerWrapper).getOrCreateServerFilterLocked < (*filterChain).newInterceptor < (*filterChain).convertVirtualHost < (*filterChain).constructUsableRouteConfiguration < (*listenerWrapper).handleRDSUpdate < (*rdsWatcher).ResourceChanged
PHASE rds-replace-filter-kept: #04 icpt-build    F1/I2                    via (*filterChain).newInterceptor < (*filterChain).convertVirtualHost < (*filterChain).constructUsableRouteConfiguration < (*listenerWrapper).handleRDSUpdate < (*rdsWatcher).ResourceChanged
PHASE rds-replace-filter-kept: #05 ref-acquire   F1 (refCnt before=2)     via getOrCreateServerFilterWithMap < (*listenerWrapper).getOrCreateServerFilterLocked < (*filterChain).newInterceptor < (*filterChain).convertVirtualHost < (*filterChain).constructUsableRouteConfiguration < (*listenerWrapper).handleRDSUpdate < (*rdsWatcher).ResourceChanged
PHASE rds-replace-filter-kept: #06 icpt-build    F1/I3                    via (*filterChain).newInterceptor < (*filterChain).convertVirtualHost < (*filterChain).constructUsableRouteConfiguration < (*listenerWrapper).handleRDSUpdate < (*rdsWatcher).ResourceChanged
PHASE rds-replace-filter-kept: #07 ref-release   F1 (refCnt before=3)     via (*filterChain).constructUsableRouteConfiguration < (*listenerWrapper).handleRDSUpdate < (*rdsWatcher).ResourceChanged
PHASE rds-replace-filter-kept: #08 icpt-close    F1/I1                    via (*virtualHostWithInterceptors).closeInterceptors < (*usableRouteConfiguration).closeInterceptors < (*filterChain).setUsableRouteConfiguration < (*listenerWrapper).handleRDSUpdate < (*rdsWatcher).ResourceChanged
PHASE rds-replace-filter-kept: SUMMARY first ref-release=#7 first filter-close=#-1 last retired icpt-close=#8 retired-interceptors-never-closed=0 RELEASE_BEFORE_CLOSE=true FILTER_CLOSED_BEFORE_INTERCEPTORS=false
PHASE rds-replace-filter-disabled-on-every-route: 2 interceptor(s) open at phase start
PHASE rds-replace-filter-disabled-on-every-route: #09 ref-release   F1 (refCnt before=2)     via (*filterChain).constructUsableRouteConfiguration < (*listenerWrapper).handleRDSUpdate < (*rdsWatcher).ResourceChanged
PHASE rds-replace-filter-disabled-on-every-route: #10 ref-release   F1 (refCnt before=1)     via (*filterChain).constructUsableRouteConfiguration < (*listenerWrapper).handleRDSUpdate < (*rdsWatcher).ResourceChanged
PHASE rds-replace-filter-disabled-on-every-route: #11 filter-close  F1                       via (*filterChain).constructUsableRouteConfiguration < (*listenerWrapper).handleRDSUpdate < (*rdsWatcher).ResourceChanged
PHASE rds-replace-filter-disabled-on-every-route: #12 icpt-close    F1/I2                    via (*virtualHostWithInterceptors).closeInterceptors < (*usableRouteConfiguration).closeInterceptors < (*filterChain).setUsableRouteConfiguration < (*listenerWrapper).handleRDSUpdate < (*rdsWatcher).ResourceChanged
PHASE rds-replace-filter-disabled-on-every-route: #13 icpt-close    F1/I3                    via (*virtualHostWithInterceptors).closeInterceptors < (*usableRouteConfiguration).closeInterceptors < (*filterChain).setUsableRouteConfiguration < (*listenerWrapper).handleRDSUpdate < (*rdsWatcher).ResourceChanged
PHASE rds-replace-filter-disabled-on-every-route: SUMMARY first ref-release=#9 first filter-close=#11 last retired icpt-close=#13 retired-interceptors-never-closed=0 RELEASE_BEFORE_CLOSE=true FILTER_CLOSED_BEFORE_INTERCEPTORS=true
--- PASS: Test (4.17s)
--- PASS: Test/VerifyC1_LDSReplace_Then_Stop (2.09s)
--- PASS: Test/VerifyC1_ReplaceKeep_Then_DisableAll (2.08s)
ok  	google.golang.org/grpc/test/xds	5.194s
```

### C1 · [evalon/grpc-go-xd-dab66e4f](https://github.com/kaitranntt-evals/grpc-go-xds-rds-interceptor-lifecycle-leak/tree/evalon/grpc-go-xd-dab66e4f) (HEAD 3133831c)

```console
$ verify/repro/c1_c8_trace.sh ~/wt/dab66e4f
PHASE lds-replace: SUMMARY first ref-release=#6 first filter-close=#-1 last retired icpt-close=#5 retired-interceptors-never-closed=0 RELEASE_BEFORE_CLOSE=false FILTER_CLOSED_BEFORE_INTERCEPTORS=false
PHASE server-stop: SUMMARY first ref-release=#8 first filter-close=#9 last retired icpt-close=#7 retired-interceptors-never-closed=0 RELEASE_BEFORE_CLOSE=false FILTER_CLOSED_BEFORE_INTERCEPTORS=false
PHASE initial: SUMMARY first ref-release=#-1 first filter-close=#-1 last retired icpt-close=#-1 retired-interceptors-never-closed=0 RELEASE_BEFORE_CLOSE=false FILTER_CLOSED_BEFORE_INTERCEPTORS=false
PHASE rds-replace-filter-kept: 1 interceptor(s) open at phase start
PHASE rds-replace-filter-kept: #03 ref-acquire   F1 (refCnt before=1)     via getOrCreateServerFilterWithMap < (*listenerWrapper).getOrCreateServerFilterLocked < (*filterChain).newInterceptor < (*filterChain).convertVirtualHost < (*filterChain).constructUsableRouteConfiguration < (*listenerWrapper).handleRDSUpdate < (*rdsWatcher).ResourceChanged
PHASE rds-replace-filter-kept: #04 icpt-build    F1/I2                    via (*filterChain).newInterceptor < (*filterChain).convertVirtualHost < (*filterChain).constructUsableRouteConfiguration < (*listenerWrapper).handleRDSUpdate < (*rdsWatcher).ResourceChanged
PHASE rds-replace-filter-kept: #05 ref-acquire   F1 (refCnt before=2)     via getOrCreateServerFilterWithMap < (*listenerWrapper).getOrCreateServerFilterLocked < (*filterChain).newInterceptor < (*filterChain).convertVirtualHost < (*filterChain).constructUsableRouteConfiguration < (*listenerWrapper).handleRDSUpdate < (*rdsWatcher).ResourceChanged
PHASE rds-replace-filter-kept: #06 icpt-build    F1/I3                    via (*filterChain).newInterceptor < (*filterChain).convertVirtualHost < (*filterChain).constructUsableRouteConfiguration < (*listenerWrapper).handleRDSUpdate < (*rdsWatcher).ResourceChanged
PHASE rds-replace-filter-kept: #07 ref-release   F1 (refCnt before=3)     via (*filterChain).constructUsableRouteConfiguration < (*listenerWrapper).handleRDSUpdate < (*rdsWatcher).ResourceChanged
PHASE rds-replace-filter-kept: #08 icpt-close    F1/I1                    via (*usableRouteConfiguration).closeInterceptors < (*filterChain).setUsableRouteConfiguration < (*listenerWrapper).handleRDSUpdate < (*rdsWatcher).ResourceChanged
PHASE rds-replace-filter-kept: SUMMARY first ref-release=#7 first filter-close=#-1 last retired icpt-close=#8 retired-interceptors-never-closed=0 RELEASE_BEFORE_CLOSE=true FILTER_CLOSED_BEFORE_INTERCEPTORS=false
PHASE rds-replace-filter-disabled-on-every-route: 2 interceptor(s) open at phase start
PHASE rds-replace-filter-disabled-on-every-route: #09 ref-release   F1 (refCnt before=2)     via (*filterChain).constructUsableRouteConfiguration < (*listenerWrapper).handleRDSUpdate < (*rdsWatcher).ResourceChanged
PHASE rds-replace-filter-disabled-on-every-route: #10 ref-release   F1 (refCnt before=1)     via (*filterChain).constructUsableRouteConfiguration < (*listenerWrapper).handleRDSUpdate < (*rdsWatcher).ResourceChanged
PHASE rds-replace-filter-disabled-on-every-route: #11 filter-close  F1                       via (*filterChain).constructUsableRouteConfiguration < (*listenerWrapper).handleRDSUpdate < (*rdsWatcher).ResourceChanged
PHASE rds-replace-filter-disabled-on-every-route: #12 icpt-close    F1/I2                    via (*usableRouteConfiguration).closeInterceptors < (*filterChain).setUsableRouteConfiguration < (*listenerWrapper).handleRDSUpdate < (*rdsWatcher).ResourceChanged
PHASE rds-replace-filter-disabled-on-every-route: #13 icpt-close    F1/I3                    via (*usableRouteConfiguration).closeInterceptors < (*filterChain).setUsableRouteConfiguration < (*listenerWrapper).handleRDSUpdate < (*rdsWatcher).ResourceChanged
PHASE rds-replace-filter-disabled-on-every-route: SUMMARY first ref-release=#9 first filter-close=#11 last retired icpt-close=#13 retired-interceptors-never-closed=0 RELEASE_BEFORE_CLOSE=true FILTER_CLOSED_BEFORE_INTERCEPTORS=true
--- PASS: Test (4.19s)
--- PASS: Test/VerifyC1_LDSReplace_Then_Stop (2.09s)
--- PASS: Test/VerifyC1_ReplaceKeep_Then_DisableAll (2.09s)
ok  	google.golang.org/grpc/test/xds	5.219s
```

### C1 · [evalon/grpc-go-xd-415a74da](https://github.com/kaitranntt-evals/grpc-go-xds-rds-interceptor-lifecycle-leak/tree/evalon/grpc-go-xd-415a74da) (HEAD f70da5b8)

```console
$ verify/repro/c1_c8_trace.sh ~/wt/415a74da
PHASE lds-replace: SUMMARY first ref-release=#6 first filter-close=#-1 last retired icpt-close=#5 retired-interceptors-never-closed=0 RELEASE_BEFORE_CLOSE=false FILTER_CLOSED_BEFORE_INTERCEPTORS=false
PHASE server-stop: SUMMARY first ref-release=#8 first filter-close=#9 last retired icpt-close=#7 retired-interceptors-never-closed=0 RELEASE_BEFORE_CLOSE=false FILTER_CLOSED_BEFORE_INTERCEPTORS=false
PHASE initial: SUMMARY first ref-release=#-1 first filter-close=#-1 last retired icpt-close=#-1 retired-interceptors-never-closed=0 RELEASE_BEFORE_CLOSE=false FILTER_CLOSED_BEFORE_INTERCEPTORS=false
PHASE rds-replace-filter-kept: 1 interceptor(s) open at phase start
PHASE rds-replace-filter-kept: #03 ref-acquire   F1 (refCnt before=1)     via getOrCreateServerFilterWithMap < (*listenerWrapper).getOrCreateServerFilterLocked < (*filterChain).newInterceptor < (*filterChain).convertVirtualHost < (*filterChain).constructUsableRouteConfiguration < (*listenerWrapper).handleRDSUpdate < (*rdsWatcher).ResourceChanged
PHASE rds-replace-filter-kept: #04 icpt-build    F1/I2                    via (*filterChain).newInterceptor < (*filterChain).convertVirtualHost < (*filterChain).constructUsableRouteConfiguration < (*listenerWrapper).handleRDSUpdate < (*rdsWatcher).ResourceChanged
PHASE rds-replace-filter-kept: #05 ref-acquire   F1 (refCnt before=2)     via getOrCreateServerFilterWithMap < (*listenerWrapper).getOrCreateServerFilterLocked < (*filterChain).newInterceptor < (*filterChain).convertVirtualHost < (*filterChain).constructUsableRouteConfiguration < (*listenerWrapper).handleRDSUpdate < (*rdsWatcher).ResourceChanged
PHASE rds-replace-filter-kept: #06 icpt-build    F1/I3                    via (*filterChain).newInterceptor < (*filterChain).convertVirtualHost < (*filterChain).constructUsableRouteConfiguration < (*listenerWrapper).handleRDSUpdate < (*rdsWatcher).ResourceChanged
PHASE rds-replace-filter-kept: #07 ref-release   F1 (refCnt before=3)     via (*filterChain).constructUsableRouteConfiguration < (*listenerWrapper).handleRDSUpdate < (*rdsWatcher).ResourceChanged
PHASE rds-replace-filter-kept: #08 icpt-close    F1/I1                    via (*virtualHostWithInterceptors).closeInterceptors < (*usableRouteConfiguration).closeInterceptors < (*filterChain).setUsableRouteConfiguration < (*listenerWrapper).handleRDSUpdate < (*rdsWatcher).ResourceChanged
PHASE rds-replace-filter-kept: SUMMARY first ref-release=#7 first filter-close=#-1 last retired icpt-close=#8 retired-interceptors-never-closed=0 RELEASE_BEFORE_CLOSE=true FILTER_CLOSED_BEFORE_INTERCEPTORS=false
PHASE rds-replace-filter-disabled-on-every-route: 2 interceptor(s) open at phase start
PHASE rds-replace-filter-disabled-on-every-route: #09 ref-release   F1 (refCnt before=2)     via (*filterChain).constructUsableRouteConfiguration < (*listenerWrapper).handleRDSUpdate < (*rdsWatcher).ResourceChanged
PHASE rds-replace-filter-disabled-on-every-route: #10 ref-release   F1 (refCnt before=1)     via (*filterChain).constructUsableRouteConfiguration < (*listenerWrapper).handleRDSUpdate < (*rdsWatcher).ResourceChanged
PHASE rds-replace-filter-disabled-on-every-route: #11 filter-close  F1                       via (*filterChain).constructUsableRouteConfiguration < (*listenerWrapper).handleRDSUpdate < (*rdsWatcher).ResourceChanged
PHASE rds-replace-filter-disabled-on-every-route: #12 icpt-close    F1/I2                    via (*virtualHostWithInterceptors).closeInterceptors < (*usableRouteConfiguration).closeInterceptors < (*filterChain).setUsableRouteConfiguration < (*listenerWrapper).handleRDSUpdate < (*rdsWatcher).ResourceChanged
PHASE rds-replace-filter-disabled-on-every-route: #13 icpt-close    F1/I3                    via (*virtualHostWithInterceptors).closeInterceptors < (*usableRouteConfiguration).closeInterceptors < (*filterChain).setUsableRouteConfiguration < (*listenerWrapper).handleRDSUpdate < (*rdsWatcher).ResourceChanged
PHASE rds-replace-filter-disabled-on-every-route: SUMMARY first ref-release=#9 first filter-close=#11 last retired icpt-close=#13 retired-interceptors-never-closed=0 RELEASE_BEFORE_CLOSE=true FILTER_CLOSED_BEFORE_INTERCEPTORS=true
--- PASS: Test (4.13s)
--- PASS: Test/VerifyC1_LDSReplace_Then_Stop (2.05s)
--- PASS: Test/VerifyC1_ReplaceKeep_Then_DisableAll (2.08s)
ok  	google.golang.org/grpc/test/xds	5.163s
```

### C1 · [evalon/grpc-go-xd-0723d2f4](https://github.com/kaitranntt-evals/grpc-go-xds-rds-interceptor-lifecycle-leak/tree/evalon/grpc-go-xd-0723d2f4) (HEAD eb765f2e)

```console
$ verify/repro/c1_c8_trace.sh ~/wt/0723d2f4
PHASE lds-replace: SUMMARY first ref-release=#6 first filter-close=#-1 last retired icpt-close=#5 retired-interceptors-never-closed=0 RELEASE_BEFORE_CLOSE=false FILTER_CLOSED_BEFORE_INTERCEPTORS=false
PHASE server-stop: SUMMARY first ref-release=#8 first filter-close=#9 last retired icpt-close=#7 retired-interceptors-never-closed=0 RELEASE_BEFORE_CLOSE=false FILTER_CLOSED_BEFORE_INTERCEPTORS=false
PHASE initial: SUMMARY first ref-release=#-1 first filter-close=#-1 last retired icpt-close=#-1 retired-interceptors-never-closed=0 RELEASE_BEFORE_CLOSE=false FILTER_CLOSED_BEFORE_INTERCEPTORS=false
PHASE rds-replace-filter-kept: 1 interceptor(s) open at phase start
PHASE rds-replace-filter-kept: #03 ref-acquire   F1 (refCnt before=1)     via getOrCreateServerFilterWithMap < (*listenerWrapper).getOrCreateServerFilterLocked < (*filterChain).newInterceptor < (*filterChain).convertVirtualHost < (*filterChain).constructUsableRouteConfiguration < (*listenerWrapper).handleRDSUpdate < (*rdsWatcher).ResourceChanged
PHASE rds-replace-filter-kept: #04 icpt-build    F1/I2                    via (*filterChain).newInterceptor < (*filterChain).convertVirtualHost < (*filterChain).constructUsableRouteConfiguration < (*listenerWrapper).handleRDSUpdate < (*rdsWatcher).ResourceChanged
PHASE rds-replace-filter-kept: #05 ref-acquire   F1 (refCnt before=2)     via getOrCreateServerFilterWithMap < (*listenerWrapper).getOrCreateServerFilterLocked < (*filterChain).newInterceptor < (*filterChain).convertVirtualHost < (*filterChain).constructUsableRouteConfiguration < (*listenerWrapper).handleRDSUpdate < (*rdsWatcher).ResourceChanged
PHASE rds-replace-filter-kept: #06 icpt-build    F1/I3                    via (*filterChain).newInterceptor < (*filterChain).convertVirtualHost < (*filterChain).constructUsableRouteConfiguration < (*listenerWrapper).handleRDSUpdate < (*rdsWatcher).ResourceChanged
PHASE rds-replace-filter-kept: #07 ref-release   F1 (refCnt before=3)     via (*filterChain).constructUsableRouteConfiguration < (*listenerWrapper).handleRDSUpdate < (*rdsWatcher).ResourceChanged
PHASE rds-replace-filter-kept: #08 icpt-close    F1/I1                    via (*virtualHostWithInterceptors).closeInterceptors < (*usableRouteConfiguration).closeInterceptors < (*listenerWrapper).handleRDSUpdate < (*rdsWatcher).ResourceChanged
PHASE rds-replace-filter-kept: SUMMARY first ref-release=#7 first filter-close=#-1 last retired icpt-close=#8 retired-interceptors-never-closed=0 RELEASE_BEFORE_CLOSE=true FILTER_CLOSED_BEFORE_INTERCEPTORS=false
PHASE rds-replace-filter-disabled-on-every-route: 2 interceptor(s) open at phase start
PHASE rds-replace-filter-disabled-on-every-route: #09 ref-release   F1 (refCnt before=2)     via (*filterChain).constructUsableRouteConfiguration < (*listenerWrapper).handleRDSUpdate < (*rdsWatcher).ResourceChanged
PHASE rds-replace-filter-disabled-on-every-route: #10 ref-release   F1 (refCnt before=1)     via (*filterChain).constructUsableRouteConfiguration < (*listenerWrapper).handleRDSUpdate < (*rdsWatcher).ResourceChanged
PHASE rds-replace-filter-disabled-on-every-route: #11 filter-close  F1                       via (*filterChain).constructUsableRouteConfiguration < (*listenerWrapper).handleRDSUpdate < (*rdsWatcher).ResourceChanged
PHASE rds-replace-filter-disabled-on-every-route: #12 icpt-close    F1/I2                    via (*virtualHostWithInterceptors).closeInterceptors < (*usableRouteConfiguration).closeInterceptors < (*listenerWrapper).handleRDSUpdate < (*rdsWatcher).ResourceChanged
PHASE rds-replace-filter-disabled-on-every-route: #13 icpt-close    F1/I3                    via (*virtualHostWithInterceptors).closeInterceptors < (*usableRouteConfiguration).closeInterceptors < (*listenerWrapper).handleRDSUpdate < (*rdsWatcher).ResourceChanged
PHASE rds-replace-filter-disabled-on-every-route: SUMMARY first ref-release=#9 first filter-close=#11 last retired icpt-close=#13 retired-interceptors-never-closed=0 RELEASE_BEFORE_CLOSE=true FILTER_CLOSED_BEFORE_INTERCEPTORS=true
--- PASS: Test (4.20s)
--- PASS: Test/VerifyC1_LDSReplace_Then_Stop (2.10s)
--- PASS: Test/VerifyC1_ReplaceKeep_Then_DisableAll (2.10s)
ok  	google.golang.org/grpc/test/xds	5.228s
```

### C1 · [evalon/grpc-go-xd-0ed1f32c](https://github.com/kaitranntt-evals/grpc-go-xds-rds-interceptor-lifecycle-leak/tree/evalon/grpc-go-xd-0ed1f32c) (HEAD 10d73679)

```console
$ verify/repro/c1_c8_trace.sh ~/wt/0ed1f32c
PHASE lds-replace: SUMMARY first ref-release=#6 first filter-close=#-1 last retired icpt-close=#5 retired-interceptors-never-closed=0 RELEASE_BEFORE_CLOSE=false FILTER_CLOSED_BEFORE_INTERCEPTORS=false
PHASE server-stop: SUMMARY first ref-release=#8 first filter-close=#9 last retired icpt-close=#7 retired-interceptors-never-closed=0 RELEASE_BEFORE_CLOSE=false FILTER_CLOSED_BEFORE_INTERCEPTORS=false
PHASE initial: SUMMARY first ref-release=#-1 first filter-close=#-1 last retired icpt-close=#-1 retired-interceptors-never-closed=0 RELEASE_BEFORE_CLOSE=false FILTER_CLOSED_BEFORE_INTERCEPTORS=false
PHASE rds-replace-filter-kept: 1 interceptor(s) open at phase start
PHASE rds-replace-filter-kept: #03 ref-acquire   F1 (refCnt before=1)     via getOrCreateServerFilterWithMap < (*listenerWrapper).getOrCreateServerFilterLocked < (*filterChain).newInterceptor < (*filterChain).convertVirtualHost < (*filterChain).constructUsableRouteConfiguration < (*listenerWrapper).handleRDSUpdate < (*rdsWatcher).ResourceChanged
PHASE rds-replace-filter-kept: #04 icpt-build    F1/I2                    via (*filterChain).newInterceptor < (*filterChain).convertVirtualHost < (*filterChain).constructUsableRouteConfiguration < (*listenerWrapper).handleRDSUpdate < (*rdsWatcher).ResourceChanged
PHASE rds-replace-filter-kept: #05 ref-acquire   F1 (refCnt before=2)     via getOrCreateServerFilterWithMap < (*listenerWrapper).getOrCreateServerFilterLocked < (*filterChain).newInterceptor < (*filterChain).convertVirtualHost < (*filterChain).constructUsableRouteConfiguration < (*listenerWrapper).handleRDSUpdate < (*rdsWatcher).ResourceChanged
PHASE rds-replace-filter-kept: #06 icpt-build    F1/I3                    via (*filterChain).newInterceptor < (*filterChain).convertVirtualHost < (*filterChain).constructUsableRouteConfiguration < (*listenerWrapper).handleRDSUpdate < (*rdsWatcher).ResourceChanged
PHASE rds-replace-filter-kept: #07 ref-release   F1 (refCnt before=3)     via (*filterChain).constructUsableRouteConfiguration < (*listenerWrapper).handleRDSUpdate < (*rdsWatcher).ResourceChanged
PHASE rds-replace-filter-kept: #08 icpt-close    F1/I1                    via (*usableRouteConfiguration).closeInterceptors < (*listenerWrapper).handleRDSUpdate < (*rdsWatcher).ResourceChanged
PHASE rds-replace-filter-kept: SUMMARY first ref-release=#7 first filter-close=#-1 last retired icpt-close=#8 retired-interceptors-never-closed=0 RELEASE_BEFORE_CLOSE=true FILTER_CLOSED_BEFORE_INTERCEPTORS=false
PHASE rds-replace-filter-disabled-on-every-route: 2 interceptor(s) open at phase start
PHASE rds-replace-filter-disabled-on-every-route: #09 ref-release   F1 (refCnt before=2)     via (*filterChain).constructUsableRouteConfiguration < (*listenerWrapper).handleRDSUpdate < (*rdsWatcher).ResourceChanged
PHASE rds-replace-filter-disabled-on-every-route: #10 ref-release   F1 (refCnt before=1)     via (*filterChain).constructUsableRouteConfiguration < (*listenerWrapper).handleRDSUpdate < (*rdsWatcher).ResourceChanged
PHASE rds-replace-filter-disabled-on-every-route: #11 filter-close  F1                       via (*filterChain).constructUsableRouteConfiguration < (*listenerWrapper).handleRDSUpdate < (*rdsWatcher).ResourceChanged
PHASE rds-replace-filter-disabled-on-every-route: #12 icpt-close    F1/I2                    via (*usableRouteConfiguration).closeInterceptors < (*listenerWrapper).handleRDSUpdate < (*rdsWatcher).ResourceChanged
PHASE rds-replace-filter-disabled-on-every-route: #13 icpt-close    F1/I3                    via (*usableRouteConfiguration).closeInterceptors < (*listenerWrapper).handleRDSUpdate < (*rdsWatcher).ResourceChanged
PHASE rds-replace-filter-disabled-on-every-route: SUMMARY first ref-release=#9 first filter-close=#11 last retired icpt-close=#13 retired-interceptors-never-closed=0 RELEASE_BEFORE_CLOSE=true FILTER_CLOSED_BEFORE_INTERCEPTORS=true
--- PASS: Test (4.16s)
--- PASS: Test/VerifyC1_LDSReplace_Then_Stop (2.07s)
--- PASS: Test/VerifyC1_ReplaceKeep_Then_DisableAll (2.08s)
ok  	google.golang.org/grpc/test/xds	5.184s
```

### C1 · [evalon/grpc-go-xd-eb9c66bf](https://github.com/kaitranntt-evals/grpc-go-xds-rds-interceptor-lifecycle-leak/tree/evalon/grpc-go-xd-eb9c66bf) (HEAD 0a3344da)

```console
$ verify/repro/c1_c8_trace.sh ~/wt/eb9c66bf
PHASE lds-replace: SUMMARY first ref-release=#6 first filter-close=#-1 last retired icpt-close=#5 retired-interceptors-never-closed=0 RELEASE_BEFORE_CLOSE=false FILTER_CLOSED_BEFORE_INTERCEPTORS=false
PHASE server-stop: SUMMARY first ref-release=#8 first filter-close=#9 last retired icpt-close=#7 retired-interceptors-never-closed=0 RELEASE_BEFORE_CLOSE=false FILTER_CLOSED_BEFORE_INTERCEPTORS=false
PHASE initial: SUMMARY first ref-release=#-1 first filter-close=#-1 last retired icpt-close=#-1 retired-interceptors-never-closed=0 RELEASE_BEFORE_CLOSE=false FILTER_CLOSED_BEFORE_INTERCEPTORS=false
PHASE rds-replace-filter-kept: 1 interceptor(s) open at phase start
PHASE rds-replace-filter-kept: #03 ref-acquire   F1 (refCnt before=1)     via getOrCreateServerFilterWithMap < (*listenerWrapper).getOrCreateServerFilterLocked < (*filterChain).newInterceptor < (*filterChain).convertVirtualHost < (*filterChain).constructUsableRouteConfiguration < (*listenerWrapper).handleRDSUpdate < (*rdsWatcher).ResourceChanged
PHASE rds-replace-filter-kept: #04 icpt-build    F1/I2                    via (*filterChain).newInterceptor < (*filterChain).convertVirtualHost < (*filterChain).constructUsableRouteConfiguration < (*listenerWrapper).handleRDSUpdate < (*rdsWatcher).ResourceChanged
PHASE rds-replace-filter-kept: #05 ref-acquire   F1 (refCnt before=2)     via getOrCreateServerFilterWithMap < (*listenerWrapper).getOrCreateServerFilterLocked < (*filterChain).newInterceptor < (*filterChain).convertVirtualHost < (*filterChain).constructUsableRouteConfiguration < (*listenerWrapper).handleRDSUpdate < (*rdsWatcher).ResourceChanged
PHASE rds-replace-filter-kept: #06 icpt-build    F1/I3                    via (*filterChain).newInterceptor < (*filterChain).convertVirtualHost < (*filterChain).constructUsableRouteConfiguration < (*listenerWrapper).handleRDSUpdate < (*rdsWatcher).ResourceChanged
PHASE rds-replace-filter-kept: #07 ref-release   F1 (refCnt before=3)     via (*filterChain).constructUsableRouteConfiguration < (*listenerWrapper).handleRDSUpdate < (*rdsWatcher).ResourceChanged
PHASE rds-replace-filter-kept: #08 icpt-close    F1/I1                    via (*usableRouteConfiguration).closeInterceptors < (*listenerWrapper).handleRDSUpdate < (*rdsWatcher).ResourceChanged
PHASE rds-replace-filter-kept: SUMMARY first ref-release=#7 first filter-close=#-1 last retired icpt-close=#8 retired-interceptors-never-closed=0 RELEASE_BEFORE_CLOSE=true FILTER_CLOSED_BEFORE_INTERCEPTORS=false
PHASE rds-replace-filter-disabled-on-every-route: 2 interceptor(s) open at phase start
PHASE rds-replace-filter-disabled-on-every-route: #09 ref-release   F1 (refCnt before=2)     via (*filterChain).constructUsableRouteConfiguration < (*listenerWrapper).handleRDSUpdate < (*rdsWatcher).ResourceChanged
PHASE rds-replace-filter-disabled-on-every-route: #10 ref-release   F1 (refCnt before=1)     via (*filterChain).constructUsableRouteConfiguration < (*listenerWrapper).handleRDSUpdate < (*rdsWatcher).ResourceChanged
PHASE rds-replace-filter-disabled-on-every-route: #11 filter-close  F1                       via (*filterChain).constructUsableRouteConfiguration < (*listenerWrapper).handleRDSUpdate < (*rdsWatcher).ResourceChanged
PHASE rds-replace-filter-disabled-on-every-route: #12 icpt-close    F1/I2                    via (*usableRouteConfiguration).closeInterceptors < (*listenerWrapper).handleRDSUpdate < (*rdsWatcher).ResourceChanged
PHASE rds-replace-filter-disabled-on-every-route: #13 icpt-close    F1/I3                    via (*usableRouteConfiguration).closeInterceptors < (*listenerWrapper).handleRDSUpdate < (*rdsWatcher).ResourceChanged
PHASE rds-replace-filter-disabled-on-every-route: SUMMARY first ref-release=#9 first filter-close=#11 last retired icpt-close=#13 retired-interceptors-never-closed=0 RELEASE_BEFORE_CLOSE=true FILTER_CLOSED_BEFORE_INTERCEPTORS=true
--- PASS: Test (4.14s)
--- PASS: Test/VerifyC1_LDSReplace_Then_Stop (2.05s)
--- PASS: Test/VerifyC1_ReplaceKeep_Then_DisableAll (2.08s)
ok  	google.golang.org/grpc/test/xds	5.165s
```

### C1 · [evalon/grpc-go-xd-e03d1e42](https://github.com/kaitranntt-evals/grpc-go-xds-rds-interceptor-lifecycle-leak/tree/evalon/grpc-go-xd-e03d1e42) (HEAD fc9cb150)

```console
$ verify/repro/c1_c8_trace.sh ~/wt/e03d1e42
PHASE lds-replace: SUMMARY first ref-release=#6 first filter-close=#-1 last retired icpt-close=#5 retired-interceptors-never-closed=0 RELEASE_BEFORE_CLOSE=false FILTER_CLOSED_BEFORE_INTERCEPTORS=false
PHASE server-stop: SUMMARY first ref-release=#8 first filter-close=#9 last retired icpt-close=#7 retired-interceptors-never-closed=0 RELEASE_BEFORE_CLOSE=false FILTER_CLOSED_BEFORE_INTERCEPTORS=false
PHASE initial: SUMMARY first ref-release=#-1 first filter-close=#-1 last retired icpt-close=#-1 retired-interceptors-never-closed=0 RELEASE_BEFORE_CLOSE=false FILTER_CLOSED_BEFORE_INTERCEPTORS=false
PHASE rds-replace-filter-kept: 1 interceptor(s) open at phase start
PHASE rds-replace-filter-kept: #03 ref-acquire   F1 (refCnt before=1)     via getOrCreateServerFilterWithMap < (*listenerWrapper).getOrCreateServerFilterLocked < (*filterChain).newInterceptor < (*filterChain).convertVirtualHost < (*filterChain).constructUsableRouteConfiguration < (*listenerWrapper).handleRDSUpdate < (*rdsWatcher).ResourceChanged
PHASE rds-replace-filter-kept: #04 icpt-build    F1/I2                    via (*filterChain).newInterceptor < (*filterChain).convertVirtualHost < (*filterChain).constructUsableRouteConfiguration < (*listenerWrapper).handleRDSUpdate < (*rdsWatcher).ResourceChanged
PHASE rds-replace-filter-kept: #05 ref-acquire   F1 (refCnt before=2)     via getOrCreateServerFilterWithMap < (*listenerWrapper).getOrCreateServerFilterLocked < (*filterChain).newInterceptor < (*filterChain).convertVirtualHost < (*filterChain).constructUsableRouteConfiguration < (*listenerWrapper).handleRDSUpdate < (*rdsWatcher).ResourceChanged
PHASE rds-replace-filter-kept: #06 icpt-build    F1/I3                    via (*filterChain).newInterceptor < (*filterChain).convertVirtualHost < (*filterChain).constructUsableRouteConfiguration < (*listenerWrapper).handleRDSUpdate < (*rdsWatcher).ResourceChanged
PHASE rds-replace-filter-kept: #07 ref-release   F1 (refCnt before=3)     via (*filterChain).constructUsableRouteConfiguration < (*listenerWrapper).handleRDSUpdate < (*rdsWatcher).ResourceChanged
PHASE rds-replace-filter-kept: #08 icpt-close    F1/I1                    via (*usableRouteConfiguration).close < (*filterChain).setUsableRouteConfiguration < (*listenerWrapper).handleRDSUpdate < (*rdsWatcher).ResourceChanged
PHASE rds-replace-filter-kept: SUMMARY first ref-release=#7 first filter-close=#-1 last retired icpt-close=#8 retired-interceptors-never-closed=0 RELEASE_BEFORE_CLOSE=true FILTER_CLOSED_BEFORE_INTERCEPTORS=false
PHASE rds-replace-filter-disabled-on-every-route: 2 interceptor(s) open at phase start
PHASE rds-replace-filter-disabled-on-every-route: #09 ref-release   F1 (refCnt before=2)     via (*filterChain).constructUsableRouteConfiguration < (*listenerWrapper).handleRDSUpdate < (*rdsWatcher).ResourceChanged
PHASE rds-replace-filter-disabled-on-every-route: #10 ref-release   F1 (refCnt before=1)     via (*filterChain).constructUsableRouteConfiguration < (*listenerWrapper).handleRDSUpdate < (*rdsWatcher).ResourceChanged
PHASE rds-replace-filter-disabled-on-every-route: #11 filter-close  F1                       via (*filterChain).constructUsableRouteConfiguration < (*listenerWrapper).handleRDSUpdate < (*rdsWatcher).ResourceChanged
PHASE rds-replace-filter-disabled-on-every-route: #12 icpt-close    F1/I2                    via (*usableRouteConfiguration).close < (*filterChain).setUsableRouteConfiguration < (*listenerWrapper).handleRDSUpdate < (*rdsWatcher).ResourceChanged
PHASE rds-replace-filter-disabled-on-every-route: #13 icpt-close    F1/I3                    via (*usableRouteConfiguration).close < (*filterChain).setUsableRouteConfiguration < (*listenerWrapper).handleRDSUpdate < (*rdsWatcher).ResourceChanged
PHASE rds-replace-filter-disabled-on-every-route: SUMMARY first ref-release=#9 first filter-close=#11 last retired icpt-close=#13 retired-interceptors-never-closed=0 RELEASE_BEFORE_CLOSE=true FILTER_CLOSED_BEFORE_INTERCEPTORS=true
--- PASS: Test (4.16s)
--- PASS: Test/VerifyC1_LDSReplace_Then_Stop (2.06s)
--- PASS: Test/VerifyC1_ReplaceKeep_Then_DisableAll (2.10s)
ok  	google.golang.org/grpc/test/xds	5.190s
```

### C1 · [evalon/grpc-go-xd-98faa6aa](https://github.com/kaitranntt-evals/grpc-go-xds-rds-interceptor-lifecycle-leak/tree/evalon/grpc-go-xd-98faa6aa) (HEAD 303f98bc)

```console
$ verify/repro/c1_c8_trace.sh ~/wt/98faa6aa
PHASE lds-replace: SUMMARY first ref-release=#6 first filter-close=#-1 last retired icpt-close=#5 retired-interceptors-never-closed=0 RELEASE_BEFORE_CLOSE=false FILTER_CLOSED_BEFORE_INTERCEPTORS=false
PHASE server-stop: SUMMARY first ref-release=#8 first filter-close=#9 last retired icpt-close=#7 retired-interceptors-never-closed=0 RELEASE_BEFORE_CLOSE=false FILTER_CLOSED_BEFORE_INTERCEPTORS=false
PHASE initial: SUMMARY first ref-release=#-1 first filter-close=#-1 last retired icpt-close=#-1 retired-interceptors-never-closed=0 RELEASE_BEFORE_CLOSE=false FILTER_CLOSED_BEFORE_INTERCEPTORS=false
PHASE rds-replace-filter-kept: 1 interceptor(s) open at phase start
PHASE rds-replace-filter-kept: #03 ref-acquire   F1 (refCnt before=1)     via getOrCreateServerFilterWithMap < (*listenerWrapper).getOrCreateServerFilterLocked < (*filterChain).newInterceptor < (*filterChain).convertVirtualHost < (*filterChain).constructUsableRouteConfiguration < (*listenerWrapper).handleRDSUpdate < (*rdsWatcher).ResourceChanged
PHASE rds-replace-filter-kept: #04 icpt-build    F1/I2                    via (*filterChain).newInterceptor < (*filterChain).convertVirtualHost < (*filterChain).constructUsableRouteConfiguration < (*listenerWrapper).handleRDSUpdate < (*rdsWatcher).ResourceChanged
PHASE rds-replace-filter-kept: #05 ref-acquire   F1 (refCnt before=2)     via getOrCreateServerFilterWithMap < (*listenerWrapper).getOrCreateServerFilterLocked < (*filterChain).newInterceptor < (*filterChain).convertVirtualHost < (*filterChain).constructUsableRouteConfiguration < (*listenerWrapper).handleRDSUpdate < (*rdsWatcher).ResourceChanged
PHASE rds-replace-filter-kept: #06 icpt-build    F1/I3                    via (*filterChain).newInterceptor < (*filterChain).convertVirtualHost < (*filterChain).constructUsableRouteConfiguration < (*listenerWrapper).handleRDSUpdate < (*rdsWatcher).ResourceChanged
PHASE rds-replace-filter-kept: #07 ref-release   F1 (refCnt before=3)     via (*filterChain).constructUsableRouteConfiguration < (*listenerWrapper).handleRDSUpdate < (*rdsWatcher).ResourceChanged
PHASE rds-replace-filter-kept: #08 icpt-close    F1/I1                    via (*usableRouteConfiguration).close < (*filterChain).setUsableRouteConfiguration < (*listenerWrapper).handleRDSUpdate < (*rdsWatcher).ResourceChanged
PHASE rds-replace-filter-kept: SUMMARY first ref-release=#7 first filter-close=#-1 last retired icpt-close=#8 retired-interceptors-never-closed=0 RELEASE_BEFORE_CLOSE=true FILTER_CLOSED_BEFORE_INTERCEPTORS=false
PHASE rds-replace-filter-disabled-on-every-route: 2 interceptor(s) open at phase start
PHASE rds-replace-filter-disabled-on-every-route: #09 ref-release   F1 (refCnt before=2)     via (*filterChain).constructUsableRouteConfiguration < (*listenerWrapper).handleRDSUpdate < (*rdsWatcher).ResourceChanged
PHASE rds-replace-filter-disabled-on-every-route: #10 ref-release   F1 (refCnt before=1)     via (*filterChain).constructUsableRouteConfiguration < (*listenerWrapper).handleRDSUpdate < (*rdsWatcher).ResourceChanged
PHASE rds-replace-filter-disabled-on-every-route: #11 filter-close  F1                       via (*filterChain).constructUsableRouteConfiguration < (*listenerWrapper).handleRDSUpdate < (*rdsWatcher).ResourceChanged
PHASE rds-replace-filter-disabled-on-every-route: #12 icpt-close    F1/I2                    via (*usableRouteConfiguration).close < (*filterChain).setUsableRouteConfiguration < (*listenerWrapper).handleRDSUpdate < (*rdsWatcher).ResourceChanged
PHASE rds-replace-filter-disabled-on-every-route: #13 icpt-close    F1/I3                    via (*usableRouteConfiguration).close < (*filterChain).setUsableRouteConfiguration < (*listenerWrapper).handleRDSUpdate < (*rdsWatcher).ResourceChanged
PHASE rds-replace-filter-disabled-on-every-route: SUMMARY first ref-release=#9 first filter-close=#11 last retired icpt-close=#13 retired-interceptors-never-closed=0 RELEASE_BEFORE_CLOSE=true FILTER_CLOSED_BEFORE_INTERCEPTORS=true
--- PASS: Test (4.17s)
--- PASS: Test/VerifyC1_LDSReplace_Then_Stop (2.09s)
--- PASS: Test/VerifyC1_ReplaceKeep_Then_DisableAll (2.08s)
ok  	google.golang.org/grpc/test/xds	5.201s
```

### C1 · [evalon/grpc-go-xd-2ffad480](https://github.com/kaitranntt-evals/grpc-go-xds-rds-interceptor-lifecycle-leak/tree/evalon/grpc-go-xd-2ffad480) (HEAD 784e2b0b)

```console
$ verify/repro/c1_c8_trace.sh ~/wt/2ffad480
PHASE lds-replace: SUMMARY first ref-release=#6 first filter-close=#-1 last retired icpt-close=#5 retired-interceptors-never-closed=0 RELEASE_BEFORE_CLOSE=false FILTER_CLOSED_BEFORE_INTERCEPTORS=false
PHASE server-stop: SUMMARY first ref-release=#8 first filter-close=#9 last retired icpt-close=#7 retired-interceptors-never-closed=0 RELEASE_BEFORE_CLOSE=false FILTER_CLOSED_BEFORE_INTERCEPTORS=false
PHASE initial: SUMMARY first ref-release=#-1 first filter-close=#-1 last retired icpt-close=#-1 retired-interceptors-never-closed=0 RELEASE_BEFORE_CLOSE=false FILTER_CLOSED_BEFORE_INTERCEPTORS=false
PHASE rds-replace-filter-kept: 1 interceptor(s) open at phase start
PHASE rds-replace-filter-kept: #03 ref-acquire   F1 (refCnt before=1)     via getOrCreateServerFilterWithMap < (*listenerWrapper).getOrCreateServerFilterLocked < (*filterChain).newInterceptor < (*filterChain).convertVirtualHost < (*filterChain).constructUsableRouteConfiguration < (*listenerWrapper).handleRDSUpdate < (*rdsWatcher).ResourceChanged
PHASE rds-replace-filter-kept: #04 icpt-build    F1/I2                    via (*filterChain).newInterceptor < (*filterChain).convertVirtualHost < (*filterChain).constructUsableRouteConfiguration < (*listenerWrapper).handleRDSUpdate < (*rdsWatcher).ResourceChanged
PHASE rds-replace-filter-kept: #05 ref-acquire   F1 (refCnt before=2)     via getOrCreateServerFilterWithMap < (*listenerWrapper).getOrCreateServerFilterLocked < (*filterChain).newInterceptor < (*filterChain).convertVirtualHost < (*filterChain).constructUsableRouteConfiguration < (*listenerWrapper).handleRDSUpdate < (*rdsWatcher).ResourceChanged
PHASE rds-replace-filter-kept: #06 icpt-build    F1/I3                    via (*filterChain).newInterceptor < (*filterChain).convertVirtualHost < (*filterChain).constructUsableRouteConfiguration < (*listenerWrapper).handleRDSUpdate < (*rdsWatcher).ResourceChanged
PHASE rds-replace-filter-kept: #07 ref-release   F1 (refCnt before=3)     via (*filterChain).constructUsableRouteConfiguration < (*listenerWrapper).handleRDSUpdate < (*rdsWatcher).ResourceChanged
PHASE rds-replace-filter-kept: #08 icpt-close    F1/I1                    via (*usableRouteConfiguration).close < (*filterChain).updateUsableRouteConfiguration < (*listenerWrapper).handleRDSUpdate < (*rdsWatcher).ResourceChanged
PHASE rds-replace-filter-kept: SUMMARY first ref-release=#7 first filter-close=#-1 last retired icpt-close=#8 retired-interceptors-never-closed=0 RELEASE_BEFORE_CLOSE=true FILTER_CLOSED_BEFORE_INTERCEPTORS=false
PHASE rds-replace-filter-disabled-on-every-route: 2 interceptor(s) open at phase start
PHASE rds-replace-filter-disabled-on-every-route: #09 ref-release   F1 (refCnt before=2)     via (*filterChain).constructUsableRouteConfiguration < (*listenerWrapper).handleRDSUpdate < (*rdsWatcher).ResourceChanged
PHASE rds-replace-filter-disabled-on-every-route: #10 ref-release   F1 (refCnt before=1)     via (*filterChain).constructUsableRouteConfiguration < (*listenerWrapper).handleRDSUpdate < (*rdsWatcher).ResourceChanged
PHASE rds-replace-filter-disabled-on-every-route: #11 filter-close  F1                       via (*filterChain).constructUsableRouteConfiguration < (*listenerWrapper).handleRDSUpdate < (*rdsWatcher).ResourceChanged
PHASE rds-replace-filter-disabled-on-every-route: #12 icpt-close    F1/I2                    via (*usableRouteConfiguration).close < (*filterChain).updateUsableRouteConfiguration < (*listenerWrapper).handleRDSUpdate < (*rdsWatcher).ResourceChanged
PHASE rds-replace-filter-disabled-on-every-route: #13 icpt-close    F1/I3                    via (*usableRouteConfiguration).close < (*filterChain).updateUsableRouteConfiguration < (*listenerWrapper).handleRDSUpdate < (*rdsWatcher).ResourceChanged
PHASE rds-replace-filter-disabled-on-every-route: SUMMARY first ref-release=#9 first filter-close=#11 last retired icpt-close=#13 retired-interceptors-never-closed=0 RELEASE_BEFORE_CLOSE=true FILTER_CLOSED_BEFORE_INTERCEPTORS=true
--- PASS: Test (4.16s)
--- PASS: Test/VerifyC1_LDSReplace_Then_Stop (2.05s)
--- PASS: Test/VerifyC1_ReplaceKeep_Then_DisableAll (2.11s)
ok  	google.golang.org/grpc/test/xds	5.190s
```

### C1 · [evalon/grpc-go-xd-dad62956](https://github.com/kaitranntt-evals/grpc-go-xds-rds-interceptor-lifecycle-leak/tree/evalon/grpc-go-xd-dad62956) (HEAD 30fef0af)

```console
$ verify/repro/c1_c8_trace.sh ~/wt/dad62956
PHASE lds-replace: SUMMARY first ref-release=#6 first filter-close=#-1 last retired icpt-close=#5 retired-interceptors-never-closed=0 RELEASE_BEFORE_CLOSE=false FILTER_CLOSED_BEFORE_INTERCEPTORS=false
PHASE server-stop: SUMMARY first ref-release=#8 first filter-close=#9 last retired icpt-close=#7 retired-interceptors-never-closed=0 RELEASE_BEFORE_CLOSE=false FILTER_CLOSED_BEFORE_INTERCEPTORS=false
PHASE initial: SUMMARY first ref-release=#-1 first filter-close=#-1 last retired icpt-close=#-1 retired-interceptors-never-closed=0 RELEASE_BEFORE_CLOSE=false FILTER_CLOSED_BEFORE_INTERCEPTORS=false
PHASE rds-replace-filter-kept: 1 interceptor(s) open at phase start
PHASE rds-replace-filter-kept: #03 ref-acquire   F1 (refCnt before=1)     via getOrCreateServerFilterWithMap < (*listenerWrapper).getOrCreateServerFilterLocked < (*filterChain).newInterceptor < (*filterChain).convertVirtualHost < (*filterChain).constructUsableRouteConfiguration < (*listenerWrapper).handleRDSUpdate < (*rdsWatcher).ResourceChanged
PHASE rds-replace-filter-kept: #04 icpt-build    F1/I2                    via (*filterChain).newInterceptor < (*filterChain).convertVirtualHost < (*filterChain).constructUsableRouteConfiguration < (*listenerWrapper).handleRDSUpdate < (*rdsWatcher).ResourceChanged
PHASE rds-replace-filter-kept: #05 ref-acquire   F1 (refCnt before=2)     via getOrCreateServerFilterWithMap < (*listenerWrapper).getOrCreateServerFilterLocked < (*filterChain).newInterceptor < (*filterChain).convertVirtualHost < (*filterChain).constructUsableRouteConfiguration < (*listenerWrapper).handleRDSUpdate < (*rdsWatcher).ResourceChanged
PHASE rds-replace-filter-kept: #06 icpt-build    F1/I3                    via (*filterChain).newInterceptor < (*filterChain).convertVirtualHost < (*filterChain).constructUsableRouteConfiguration < (*listenerWrapper).handleRDSUpdate < (*rdsWatcher).ResourceChanged
PHASE rds-replace-filter-kept: #07 ref-release   F1 (refCnt before=3)     via (*filterChain).constructUsableRouteConfiguration < (*listenerWrapper).handleRDSUpdate < (*rdsWatcher).ResourceChanged
PHASE rds-replace-filter-kept: #08 icpt-close    F1/I1                    via (*usableRouteConfiguration).close < (*filterChain).updateUsableRouteConfiguration < (*listenerWrapper).handleRDSUpdate < (*rdsWatcher).ResourceChanged
PHASE rds-replace-filter-kept: SUMMARY first ref-release=#7 first filter-close=#-1 last retired icpt-close=#8 retired-interceptors-never-closed=0 RELEASE_BEFORE_CLOSE=true FILTER_CLOSED_BEFORE_INTERCEPTORS=false
PHASE rds-replace-filter-disabled-on-every-route: 2 interceptor(s) open at phase start
PHASE rds-replace-filter-disabled-on-every-route: #09 ref-release   F1 (refCnt before=2)     via (*filterChain).constructUsableRouteConfiguration < (*listenerWrapper).handleRDSUpdate < (*rdsWatcher).ResourceChanged
PHASE rds-replace-filter-disabled-on-every-route: #10 ref-release   F1 (refCnt before=1)     via (*filterChain).constructUsableRouteConfiguration < (*listenerWrapper).handleRDSUpdate < (*rdsWatcher).ResourceChanged
PHASE rds-replace-filter-disabled-on-every-route: #11 filter-close  F1                       via (*filterChain).constructUsableRouteConfiguration < (*listenerWrapper).handleRDSUpdate < (*rdsWatcher).ResourceChanged
PHASE rds-replace-filter-disabled-on-every-route: #12 icpt-close    F1/I2                    via (*usableRouteConfiguration).close < (*filterChain).updateUsableRouteConfiguration < (*listenerWrapper).handleRDSUpdate < (*rdsWatcher).ResourceChanged
PHASE rds-replace-filter-disabled-on-every-route: #13 icpt-close    F1/I3                    via (*usableRouteConfiguration).close < (*filterChain).updateUsableRouteConfiguration < (*listenerWrapper).handleRDSUpdate < (*rdsWatcher).ResourceChanged
PHASE rds-replace-filter-disabled-on-every-route: SUMMARY first ref-release=#9 first filter-close=#11 last retired icpt-close=#13 retired-interceptors-never-closed=0 RELEASE_BEFORE_CLOSE=true FILTER_CLOSED_BEFORE_INTERCEPTORS=true
--- PASS: Test (4.17s)
--- PASS: Test/VerifyC1_LDSReplace_Then_Stop (2.07s)
--- PASS: Test/VerifyC1_ReplaceKeep_Then_DisableAll (2.09s)
ok  	google.golang.org/grpc/test/xds	5.199s
```

### C1 · [evalon/grpc-go-xd-6851db1c](https://github.com/kaitranntt-evals/grpc-go-xds-rds-interceptor-lifecycle-leak/tree/evalon/grpc-go-xd-6851db1c) (HEAD a1200e55)

```console
$ verify/repro/c1_c8_trace.sh ~/wt/6851db1c
PHASE lds-replace: SUMMARY first ref-release=#6 first filter-close=#-1 last retired icpt-close=#5 retired-interceptors-never-closed=0 RELEASE_BEFORE_CLOSE=false FILTER_CLOSED_BEFORE_INTERCEPTORS=false
PHASE server-stop: SUMMARY first ref-release=#8 first filter-close=#9 last retired icpt-close=#7 retired-interceptors-never-closed=0 RELEASE_BEFORE_CLOSE=false FILTER_CLOSED_BEFORE_INTERCEPTORS=false
PHASE initial: SUMMARY first ref-release=#-1 first filter-close=#-1 last retired icpt-close=#-1 retired-interceptors-never-closed=0 RELEASE_BEFORE_CLOSE=false FILTER_CLOSED_BEFORE_INTERCEPTORS=false
PHASE rds-replace-filter-kept: 1 interceptor(s) open at phase start
PHASE rds-replace-filter-kept: #03 ref-acquire   F1 (refCnt before=1)     via getOrCreateServerFilterWithMap < (*listenerWrapper).getOrCreateServerFilterLocked < (*filterChain).newInterceptor < (*filterChain).convertVirtualHost < (*filterChain).constructUsableRouteConfiguration < (*listenerWrapper).handleRDSUpdate < (*rdsWatcher).ResourceChanged
PHASE rds-replace-filter-kept: #04 icpt-build    F1/I2                    via (*filterChain).newInterceptor < (*filterChain).convertVirtualHost < (*filterChain).constructUsableRouteConfiguration < (*listenerWrapper).handleRDSUpdate < (*rdsWatcher).ResourceChanged
PHASE rds-replace-filter-kept: #05 ref-acquire   F1 (refCnt before=2)     via getOrCreateServerFilterWithMap < (*listenerWrapper).getOrCreateServerFilterLocked < (*filterChain).newInterceptor < (*filterChain).convertVirtualHost < (*filterChain).constructUsableRouteConfiguration < (*listenerWrapper).handleRDSUpdate < (*rdsWatcher).ResourceChanged
PHASE rds-replace-filter-kept: #06 icpt-build    F1/I3                    via (*filterChain).newInterceptor < (*filterChain).convertVirtualHost < (*filterChain).constructUsableRouteConfiguration < (*listenerWrapper).handleRDSUpdate < (*rdsWatcher).ResourceChanged
PHASE rds-replace-filter-kept: #07 ref-release   F1 (refCnt before=3)     via (*filterChain).constructUsableRouteConfiguration < (*listenerWrapper).handleRDSUpdate < (*rdsWatcher).ResourceChanged
PHASE rds-replace-filter-kept: #08 icpt-close    F1/I1                    via (*usableRouteConfiguration).close < (*filterChain).setUsableRouteConfiguration < (*listenerWrapper).handleRDSUpdate < (*rdsWatcher).ResourceChanged
PHASE rds-replace-filter-kept: SUMMARY first ref-release=#7 first filter-close=#-1 last retired icpt-close=#8 retired-interceptors-never-closed=0 RELEASE_BEFORE_CLOSE=true FILTER_CLOSED_BEFORE_INTERCEPTORS=false
PHASE rds-replace-filter-disabled-on-every-route: 2 interceptor(s) open at phase start
PHASE rds-replace-filter-disabled-on-every-route: #09 ref-release   F1 (refCnt before=2)     via (*filterChain).constructUsableRouteConfiguration < (*listenerWrapper).handleRDSUpdate < (*rdsWatcher).ResourceChanged
PHASE rds-replace-filter-disabled-on-every-route: #10 ref-release   F1 (refCnt before=1)     via (*filterChain).constructUsableRouteConfiguration < (*listenerWrapper).handleRDSUpdate < (*rdsWatcher).ResourceChanged
PHASE rds-replace-filter-disabled-on-every-route: #11 filter-close  F1                       via (*filterChain).constructUsableRouteConfiguration < (*listenerWrapper).handleRDSUpdate < (*rdsWatcher).ResourceChanged
PHASE rds-replace-filter-disabled-on-every-route: #12 icpt-close    F1/I2                    via (*usableRouteConfiguration).close < (*filterChain).setUsableRouteConfiguration < (*listenerWrapper).handleRDSUpdate < (*rdsWatcher).ResourceChanged
PHASE rds-replace-filter-disabled-on-every-route: #13 icpt-close    F1/I3                    via (*usableRouteConfiguration).close < (*filterChain).setUsableRouteConfiguration < (*listenerWrapper).handleRDSUpdate < (*rdsWatcher).ResourceChanged
PHASE rds-replace-filter-disabled-on-every-route: SUMMARY first ref-release=#9 first filter-close=#11 last retired icpt-close=#13 retired-interceptors-never-closed=0 RELEASE_BEFORE_CLOSE=true FILTER_CLOSED_BEFORE_INTERCEPTORS=true
--- PASS: Test (4.15s)
--- PASS: Test/VerifyC1_LDSReplace_Then_Stop (2.07s)
--- PASS: Test/VerifyC1_ReplaceKeep_Then_DisableAll (2.08s)
ok  	google.golang.org/grpc/test/xds	5.180s
```

### C1 · [evalon/grpc-go-xd-3e0a44dd](https://github.com/kaitranntt-evals/grpc-go-xds-rds-interceptor-lifecycle-leak/tree/evalon/grpc-go-xd-3e0a44dd) (HEAD 68d87d63)

```console
$ verify/repro/c1_c8_trace.sh ~/wt/3e0a44dd
PHASE lds-replace: SUMMARY first ref-release=#6 first filter-close=#-1 last retired icpt-close=#5 retired-interceptors-never-closed=0 RELEASE_BEFORE_CLOSE=false FILTER_CLOSED_BEFORE_INTERCEPTORS=false
PHASE server-stop: SUMMARY first ref-release=#8 first filter-close=#9 last retired icpt-close=#7 retired-interceptors-never-closed=0 RELEASE_BEFORE_CLOSE=false FILTER_CLOSED_BEFORE_INTERCEPTORS=false
PHASE initial: SUMMARY first ref-release=#-1 first filter-close=#-1 last retired icpt-close=#-1 retired-interceptors-never-closed=0 RELEASE_BEFORE_CLOSE=false FILTER_CLOSED_BEFORE_INTERCEPTORS=false
PHASE rds-replace-filter-kept: 1 interceptor(s) open at phase start
PHASE rds-replace-filter-kept: #03 ref-acquire   F1 (refCnt before=1)     via getOrCreateServerFilterWithMap < (*listenerWrapper).getOrCreateServerFilterLocked < (*filterChain).newInterceptor < (*filterChain).convertVirtualHost < (*filterChain).constructUsableRouteConfiguration < (*listenerWrapper).handleRDSUpdate < (*rdsWatcher).ResourceChanged
PHASE rds-replace-filter-kept: #04 icpt-build    F1/I2                    via (*filterChain).newInterceptor < (*filterChain).convertVirtualHost < (*filterChain).constructUsableRouteConfiguration < (*listenerWrapper).handleRDSUpdate < (*rdsWatcher).ResourceChanged
PHASE rds-replace-filter-kept: #05 ref-acquire   F1 (refCnt before=2)     via getOrCreateServerFilterWithMap < (*listenerWrapper).getOrCreateServerFilterLocked < (*filterChain).newInterceptor < (*filterChain).convertVirtualHost < (*filterChain).constructUsableRouteConfiguration < (*listenerWrapper).handleRDSUpdate < (*rdsWatcher).ResourceChanged
PHASE rds-replace-filter-kept: #06 icpt-build    F1/I3                    via (*filterChain).newInterceptor < (*filterChain).convertVirtualHost < (*filterChain).constructUsableRouteConfiguration < (*listenerWrapper).handleRDSUpdate < (*rdsWatcher).ResourceChanged
PHASE rds-replace-filter-kept: #07 ref-release   F1 (refCnt before=3)     via (*filterChain).constructUsableRouteConfiguration < (*listenerWrapper).handleRDSUpdate < (*rdsWatcher).ResourceChanged
PHASE rds-replace-filter-kept: #08 icpt-close    F1/I1                    via (*usableRouteConfiguration).close < (*filterChain).updateUsableRouteConfiguration < (*listenerWrapper).handleRDSUpdate < (*rdsWatcher).ResourceChanged
PHASE rds-replace-filter-kept: SUMMARY first ref-release=#7 first filter-close=#-1 last retired icpt-close=#8 retired-interceptors-never-closed=0 RELEASE_BEFORE_CLOSE=true FILTER_CLOSED_BEFORE_INTERCEPTORS=false
PHASE rds-replace-filter-disabled-on-every-route: 2 interceptor(s) open at phase start
PHASE rds-replace-filter-disabled-on-every-route: #09 ref-release   F1 (refCnt before=2)     via (*filterChain).constructUsableRouteConfiguration < (*listenerWrapper).handleRDSUpdate < (*rdsWatcher).ResourceChanged
PHASE rds-replace-filter-disabled-on-every-route: #10 ref-release   F1 (refCnt before=1)     via (*filterChain).constructUsableRouteConfiguration < (*listenerWrapper).handleRDSUpdate < (*rdsWatcher).ResourceChanged
PHASE rds-replace-filter-disabled-on-every-route: #11 filter-close  F1                       via (*filterChain).constructUsableRouteConfiguration < (*listenerWrapper).handleRDSUpdate < (*rdsWatcher).ResourceChanged
PHASE rds-replace-filter-disabled-on-every-route: #12 icpt-close    F1/I2                    via (*usableRouteConfiguration).close < (*filterChain).updateUsableRouteConfiguration < (*listenerWrapper).handleRDSUpdate < (*rdsWatcher).ResourceChanged
PHASE rds-replace-filter-disabled-on-every-route: #13 icpt-close    F1/I3                    via (*usableRouteConfiguration).close < (*filterChain).updateUsableRouteConfiguration < (*listenerWrapper).handleRDSUpdate < (*rdsWatcher).ResourceChanged
PHASE rds-replace-filter-disabled-on-every-route: SUMMARY first ref-release=#9 first filter-close=#11 last retired icpt-close=#13 retired-interceptors-never-closed=0 RELEASE_BEFORE_CLOSE=true FILTER_CLOSED_BEFORE_INTERCEPTORS=true
--- PASS: Test (4.13s)
--- PASS: Test/VerifyC1_LDSReplace_Then_Stop (2.07s)
--- PASS: Test/VerifyC1_ReplaceKeep_Then_DisableAll (2.06s)
ok  	google.golang.org/grpc/test/xds	5.159s
```

### C1 · [evalon/grpc-go-xd-eb19a38b](https://github.com/kaitranntt-evals/grpc-go-xds-rds-interceptor-lifecycle-leak/tree/evalon/grpc-go-xd-eb19a38b) (HEAD 7c0ac8b4)

```console
$ verify/repro/c1_c8_trace.sh ~/wt/eb19a38b
PHASE lds-replace: SUMMARY first ref-release=#6 first filter-close=#-1 last retired icpt-close=#5 retired-interceptors-never-closed=0 RELEASE_BEFORE_CLOSE=false FILTER_CLOSED_BEFORE_INTERCEPTORS=false
PHASE server-stop: SUMMARY first ref-release=#8 first filter-close=#9 last retired icpt-close=#7 retired-interceptors-never-closed=0 RELEASE_BEFORE_CLOSE=false FILTER_CLOSED_BEFORE_INTERCEPTORS=false
PHASE initial: SUMMARY first ref-release=#-1 first filter-close=#-1 last retired icpt-close=#-1 retired-interceptors-never-closed=0 RELEASE_BEFORE_CLOSE=false FILTER_CLOSED_BEFORE_INTERCEPTORS=false
PHASE rds-replace-filter-kept: 1 interceptor(s) open at phase start
PHASE rds-replace-filter-kept: #03 ref-acquire   F1 (refCnt before=1)     via getOrCreateServerFilterWithMap < (*listenerWrapper).getOrCreateServerFilterLocked < (*filterChain).newInterceptor < (*filterChain).convertVirtualHost < (*filterChain).constructUsableRouteConfiguration < (*listenerWrapper).handleRDSUpdate < (*rdsWatcher).ResourceChanged
PHASE rds-replace-filter-kept: #04 icpt-build    F1/I2                    via (*filterChain).newInterceptor < (*filterChain).convertVirtualHost < (*filterChain).constructUsableRouteConfiguration < (*listenerWrapper).handleRDSUpdate < (*rdsWatcher).ResourceChanged
PHASE rds-replace-filter-kept: #05 ref-acquire   F1 (refCnt before=2)     via getOrCreateServerFilterWithMap < (*listenerWrapper).getOrCreateServerFilterLocked < (*filterChain).newInterceptor < (*filterChain).convertVirtualHost < (*filterChain).constructUsableRouteConfiguration < (*listenerWrapper).handleRDSUpdate < (*rdsWatcher).ResourceChanged
PHASE rds-replace-filter-kept: #06 icpt-build    F1/I3                    via (*filterChain).newInterceptor < (*filterChain).convertVirtualHost < (*filterChain).constructUsableRouteConfiguration < (*listenerWrapper).handleRDSUpdate < (*rdsWatcher).ResourceChanged
PHASE rds-replace-filter-kept: #07 ref-release   F1 (refCnt before=3)     via (*filterChain).constructUsableRouteConfiguration < (*listenerWrapper).handleRDSUpdate < (*rdsWatcher).ResourceChanged
PHASE rds-replace-filter-kept: #08 icpt-close    F1/I1                    via (*usableRouteConfiguration).closeInterceptors < (*filterChain).updateUsableRouteConfiguration < (*listenerWrapper).handleRDSUpdate < (*rdsWatcher).ResourceChanged
PHASE rds-replace-filter-kept: SUMMARY first ref-release=#7 first filter-close=#-1 last retired icpt-close=#8 retired-interceptors-never-closed=0 RELEASE_BEFORE_CLOSE=true FILTER_CLOSED_BEFORE_INTERCEPTORS=false
PHASE rds-replace-filter-disabled-on-every-route: 2 interceptor(s) open at phase start
PHASE rds-replace-filter-disabled-on-every-route: #09 ref-release   F1 (refCnt before=2)     via (*filterChain).constructUsableRouteConfiguration < (*listenerWrapper).handleRDSUpdate < (*rdsWatcher).ResourceChanged
PHASE rds-replace-filter-disabled-on-every-route: #10 ref-release   F1 (refCnt before=1)     via (*filterChain).constructUsableRouteConfiguration < (*listenerWrapper).handleRDSUpdate < (*rdsWatcher).ResourceChanged
PHASE rds-replace-filter-disabled-on-every-route: #11 filter-close  F1                       via (*filterChain).constructUsableRouteConfiguration < (*listenerWrapper).handleRDSUpdate < (*rdsWatcher).ResourceChanged
PHASE rds-replace-filter-disabled-on-every-route: #12 icpt-close    F1/I2                    via (*usableRouteConfiguration).closeInterceptors < (*filterChain).updateUsableRouteConfiguration < (*listenerWrapper).handleRDSUpdate < (*rdsWatcher).ResourceChanged
PHASE rds-replace-filter-disabled-on-every-route: #13 icpt-close    F1/I3                    via (*usableRouteConfiguration).closeInterceptors < (*filterChain).updateUsableRouteConfiguration < (*listenerWrapper).handleRDSUpdate < (*rdsWatcher).ResourceChanged
PHASE rds-replace-filter-disabled-on-every-route: SUMMARY first ref-release=#9 first filter-close=#11 last retired icpt-close=#13 retired-interceptors-never-closed=0 RELEASE_BEFORE_CLOSE=true FILTER_CLOSED_BEFORE_INTERCEPTORS=true
--- PASS: Test (4.20s)
--- PASS: Test/VerifyC1_LDSReplace_Then_Stop (2.10s)
--- PASS: Test/VerifyC1_ReplaceKeep_Then_DisableAll (2.10s)
ok  	google.golang.org/grpc/test/xds	5.224s
```

### C1 · default settings (no experimental flag), all branches

Same script, second run with the added `VerifyC1_ReplaceKeep_Then_NoVirtualHosts` test; shown are events #07/#08 of `noflag-rds-replace-filter-kept`, every event of `noflag-rds-replace-no-virtual-hosts`, the two `SUMMARY` lines and the result lines.

```console
$ verify/repro/c1_c8_trace.sh ~/wt/eababd58 | grep -E 'noflag-rds|--- |^(ok|FAIL)'      # evalon/grpc-go-xd-eababd58
PHASE noflag-rds-replace-filter-kept: #07 ref-release   F1 (refCnt before=3)     via (*filterChain).constructUsableRouteConfiguration < (*listenerWrapper).handleRDSUpdate < (*rdsWatcher).ResourceChanged
PHASE noflag-rds-replace-filter-kept: #08 icpt-close    F1/I1                    via (*usableRouteConfiguration).closeInterceptors < swapUsableRouteConfiguration < (*listenerWrapper).handleRDSUpdate < (*rdsWatcher).ResourceChanged
PHASE noflag-rds-replace-filter-kept: SUMMARY first ref-release=#7 first filter-close=#-1 last retired icpt-close=#8 retired-interceptors-never-closed=0 RELEASE_BEFORE_CLOSE=true FILTER_CLOSED_BEFORE_INTERCEPTORS=false
PHASE noflag-rds-replace-no-virtual-hosts: #09 ref-release   F1 (refCnt before=2)     via (*filterChain).constructUsableRouteConfiguration < (*listenerWrapper).handleRDSUpdate < (*rdsWatcher).ResourceChanged
PHASE noflag-rds-replace-no-virtual-hosts: #10 ref-release   F1 (refCnt before=1)     via (*filterChain).constructUsableRouteConfiguration < (*listenerWrapper).handleRDSUpdate < (*rdsWatcher).ResourceChanged
PHASE noflag-rds-replace-no-virtual-hosts: #11 filter-close  F1                       via (*filterChain).constructUsableRouteConfiguration < (*listenerWrapper).handleRDSUpdate < (*rdsWatcher).ResourceChanged
PHASE noflag-rds-replace-no-virtual-hosts: #12 icpt-close    F1/I2                    via (*usableRouteConfiguration).closeInterceptors < swapUsableRouteConfiguration < (*listenerWrapper).handleRDSUpdate < (*rdsWatcher).ResourceChanged
PHASE noflag-rds-replace-no-virtual-hosts: #13 icpt-close    F1/I3                    via (*usableRouteConfiguration).closeInterceptors < swapUsableRouteConfiguration < (*listenerWrapper).handleRDSUpdate < (*rdsWatcher).ResourceChanged
PHASE noflag-rds-replace-no-virtual-hosts: SUMMARY first ref-release=#9 first filter-close=#11 last retired icpt-close=#13 retired-interceptors-never-closed=0 RELEASE_BEFORE_CLOSE=true FILTER_CLOSED_BEFORE_INTERCEPTORS=true
--- PASS: Test (6.25s)
--- PASS: Test/VerifyC1_LDSReplace_Then_Stop (2.07s)
--- PASS: Test/VerifyC1_ReplaceKeep_Then_DisableAll (2.10s)
--- PASS: Test/VerifyC1_ReplaceKeep_Then_NoVirtualHosts (2.08s)
ok  	google.golang.org/grpc/test/xds	7.276s
$ verify/repro/c1_c8_trace.sh ~/wt/b7bc0d7f | grep -E 'noflag-rds|--- |^(ok|FAIL)'      # evalon/grpc-go-xd-b7bc0d7f
PHASE noflag-rds-replace-filter-kept: #07 ref-release   F1 (refCnt before=3)     via (*filterChain).constructUsableRouteConfiguration < (*listenerWrapper).handleRDSUpdate < (*rdsWatcher).ResourceChanged
PHASE noflag-rds-replace-filter-kept: #08 icpt-close    F1/I1                    via (*virtualHostWithInterceptors).closeInterceptors < (*usableRouteConfiguration).closeInterceptors < (*filterChain).setUsableRouteConfiguration < (*listenerWrapper).handleRDSUpdate < (*rdsWatcher).ResourceChanged
PHASE noflag-rds-replace-filter-kept: SUMMARY first ref-release=#7 first filter-close=#-1 last retired icpt-close=#8 retired-interceptors-never-closed=0 RELEASE_BEFORE_CLOSE=true FILTER_CLOSED_BEFORE_INTERCEPTORS=false
PHASE noflag-rds-replace-no-virtual-hosts: #09 ref-release   F1 (refCnt before=2)     via (*filterChain).constructUsableRouteConfiguration < (*listenerWrapper).handleRDSUpdate < (*rdsWatcher).ResourceChanged
PHASE noflag-rds-replace-no-virtual-hosts: #10 ref-release   F1 (refCnt before=1)     via (*filterChain).constructUsableRouteConfiguration < (*listenerWrapper).handleRDSUpdate < (*rdsWatcher).ResourceChanged
PHASE noflag-rds-replace-no-virtual-hosts: #11 filter-close  F1                       via (*filterChain).constructUsableRouteConfiguration < (*listenerWrapper).handleRDSUpdate < (*rdsWatcher).ResourceChanged
PHASE noflag-rds-replace-no-virtual-hosts: #12 icpt-close    F1/I2                    via (*virtualHostWithInterceptors).closeInterceptors < (*usableRouteConfiguration).closeInterceptors < (*filterChain).setUsableRouteConfiguration < (*listenerWrapper).handleRDSUpdate < (*rdsWatcher).ResourceChanged
PHASE noflag-rds-replace-no-virtual-hosts: #13 icpt-close    F1/I3                    via (*virtualHostWithInterceptors).closeInterceptors < (*usableRouteConfiguration).closeInterceptors < (*filterChain).setUsableRouteConfiguration < (*listenerWrapper).handleRDSUpdate < (*rdsWatcher).ResourceChanged
PHASE noflag-rds-replace-no-virtual-hosts: SUMMARY first ref-release=#9 first filter-close=#11 last retired icpt-close=#13 retired-interceptors-never-closed=0 RELEASE_BEFORE_CLOSE=true FILTER_CLOSED_BEFORE_INTERCEPTORS=true
--- PASS: Test (6.23s)
--- PASS: Test/VerifyC1_LDSReplace_Then_Stop (2.06s)
--- PASS: Test/VerifyC1_ReplaceKeep_Then_DisableAll (2.07s)
--- PASS: Test/VerifyC1_ReplaceKeep_Then_NoVirtualHosts (2.09s)
ok  	google.golang.org/grpc/test/xds	7.256s
$ verify/repro/c1_c8_trace.sh ~/wt/dab66e4f | grep -E 'noflag-rds|--- |^(ok|FAIL)'      # evalon/grpc-go-xd-dab66e4f
PHASE noflag-rds-replace-filter-kept: #07 ref-release   F1 (refCnt before=3)     via (*filterChain).constructUsableRouteConfiguration < (*listenerWrapper).handleRDSUpdate < (*rdsWatcher).ResourceChanged
PHASE noflag-rds-replace-filter-kept: #08 icpt-close    F1/I1                    via (*usableRouteConfiguration).closeInterceptors < (*filterChain).setUsableRouteConfiguration < (*listenerWrapper).handleRDSUpdate < (*rdsWatcher).ResourceChanged
PHASE noflag-rds-replace-filter-kept: SUMMARY first ref-release=#7 first filter-close=#-1 last retired icpt-close=#8 retired-interceptors-never-closed=0 RELEASE_BEFORE_CLOSE=true FILTER_CLOSED_BEFORE_INTERCEPTORS=false
PHASE noflag-rds-replace-no-virtual-hosts: #09 ref-release   F1 (refCnt before=2)     via (*filterChain).constructUsableRouteConfiguration < (*listenerWrapper).handleRDSUpdate < (*rdsWatcher).ResourceChanged
PHASE noflag-rds-replace-no-virtual-hosts: #10 ref-release   F1 (refCnt before=1)     via (*filterChain).constructUsableRouteConfiguration < (*listenerWrapper).handleRDSUpdate < (*rdsWatcher).ResourceChanged
PHASE noflag-rds-replace-no-virtual-hosts: #11 filter-close  F1                       via (*filterChain).constructUsableRouteConfiguration < (*listenerWrapper).handleRDSUpdate < (*rdsWatcher).ResourceChanged
PHASE noflag-rds-replace-no-virtual-hosts: #12 icpt-close    F1/I2                    via (*usableRouteConfiguration).closeInterceptors < (*filterChain).setUsableRouteConfiguration < (*listenerWrapper).handleRDSUpdate < (*rdsWatcher).ResourceChanged
PHASE noflag-rds-replace-no-virtual-hosts: #13 icpt-close    F1/I3                    via (*usableRouteConfiguration).closeInterceptors < (*filterChain).setUsableRouteConfiguration < (*listenerWrapper).handleRDSUpdate < (*rdsWatcher).ResourceChanged
PHASE noflag-rds-replace-no-virtual-hosts: SUMMARY first ref-release=#9 first filter-close=#11 last retired icpt-close=#13 retired-interceptors-never-closed=0 RELEASE_BEFORE_CLOSE=true FILTER_CLOSED_BEFORE_INTERCEPTORS=true
--- PASS: Test (6.23s)
--- PASS: Test/VerifyC1_LDSReplace_Then_Stop (2.07s)
--- PASS: Test/VerifyC1_ReplaceKeep_Then_DisableAll (2.09s)
--- PASS: Test/VerifyC1_ReplaceKeep_Then_NoVirtualHosts (2.06s)
ok  	google.golang.org/grpc/test/xds	7.259s
$ verify/repro/c1_c8_trace.sh ~/wt/415a74da | grep -E 'noflag-rds|--- |^(ok|FAIL)'      # evalon/grpc-go-xd-415a74da
PHASE noflag-rds-replace-filter-kept: #07 ref-release   F1 (refCnt before=3)     via (*filterChain).constructUsableRouteConfiguration < (*listenerWrapper).handleRDSUpdate < (*rdsWatcher).ResourceChanged
PHASE noflag-rds-replace-filter-kept: #08 icpt-close    F1/I1                    via (*virtualHostWithInterceptors).closeInterceptors < (*usableRouteConfiguration).closeInterceptors < (*filterChain).setUsableRouteConfiguration < (*listenerWrapper).handleRDSUpdate < (*rdsWatcher).ResourceChanged
PHASE noflag-rds-replace-filter-kept: SUMMARY first ref-release=#7 first filter-close=#-1 last retired icpt-close=#8 retired-interceptors-never-closed=0 RELEASE_BEFORE_CLOSE=true FILTER_CLOSED_BEFORE_INTERCEPTORS=false
PHASE noflag-rds-replace-no-virtual-hosts: #09 ref-release   F1 (refCnt before=2)     via (*filterChain).constructUsableRouteConfiguration < (*listenerWrapper).handleRDSUpdate < (*rdsWatcher).ResourceChanged
PHASE noflag-rds-replace-no-virtual-hosts: #10 ref-release   F1 (refCnt before=1)     via (*filterChain).constructUsableRouteConfiguration < (*listenerWrapper).handleRDSUpdate < (*rdsWatcher).ResourceChanged
PHASE noflag-rds-replace-no-virtual-hosts: #11 filter-close  F1                       via (*filterChain).constructUsableRouteConfiguration < (*listenerWrapper).handleRDSUpdate < (*rdsWatcher).ResourceChanged
PHASE noflag-rds-replace-no-virtual-hosts: #12 icpt-close    F1/I2                    via (*virtualHostWithInterceptors).closeInterceptors < (*usableRouteConfiguration).closeInterceptors < (*filterChain).setUsableRouteConfiguration < (*listenerWrapper).handleRDSUpdate < (*rdsWatcher).ResourceChanged
PHASE noflag-rds-replace-no-virtual-hosts: #13 icpt-close    F1/I3                    via (*virtualHostWithInterceptors).closeInterceptors < (*usableRouteConfiguration).closeInterceptors < (*filterChain).setUsableRouteConfiguration < (*listenerWrapper).handleRDSUpdate < (*rdsWatcher).ResourceChanged
PHASE noflag-rds-replace-no-virtual-hosts: SUMMARY first ref-release=#9 first filter-close=#11 last retired icpt-close=#13 retired-interceptors-never-closed=0 RELEASE_BEFORE_CLOSE=true FILTER_CLOSED_BEFORE_INTERCEPTORS=true
--- PASS: Test (6.24s)
--- PASS: Test/VerifyC1_LDSReplace_Then_Stop (2.09s)
--- PASS: Test/VerifyC1_ReplaceKeep_Then_DisableAll (2.06s)
--- PASS: Test/VerifyC1_ReplaceKeep_Then_NoVirtualHosts (2.09s)
ok  	google.golang.org/grpc/test/xds	7.271s
$ verify/repro/c1_c8_trace.sh ~/wt/0723d2f4 | grep -E 'noflag-rds|--- |^(ok|FAIL)'      # evalon/grpc-go-xd-0723d2f4
PHASE noflag-rds-replace-filter-kept: #07 ref-release   F1 (refCnt before=3)     via (*filterChain).constructUsableRouteConfiguration < (*listenerWrapper).handleRDSUpdate < (*rdsWatcher).ResourceChanged
PHASE noflag-rds-replace-filter-kept: #08 icpt-close    F1/I1                    via (*virtualHostWithInterceptors).closeInterceptors < (*usableRouteConfiguration).closeInterceptors < (*listenerWrapper).handleRDSUpdate < (*rdsWatcher).ResourceChanged
PHASE noflag-rds-replace-filter-kept: SUMMARY first ref-release=#7 first filter-close=#-1 last retired icpt-close=#8 retired-interceptors-never-closed=0 RELEASE_BEFORE_CLOSE=true FILTER_CLOSED_BEFORE_INTERCEPTORS=false
PHASE noflag-rds-replace-no-virtual-hosts: #09 ref-release   F1 (refCnt before=2)     via (*filterChain).constructUsableRouteConfiguration < (*listenerWrapper).handleRDSUpdate < (*rdsWatcher).ResourceChanged
PHASE noflag-rds-replace-no-virtual-hosts: #10 ref-release   F1 (refCnt before=1)     via (*filterChain).constructUsableRouteConfiguration < (*listenerWrapper).handleRDSUpdate < (*rdsWatcher).ResourceChanged
PHASE noflag-rds-replace-no-virtual-hosts: #11 filter-close  F1                       via (*filterChain).constructUsableRouteConfiguration < (*listenerWrapper).handleRDSUpdate < (*rdsWatcher).ResourceChanged
PHASE noflag-rds-replace-no-virtual-hosts: #12 icpt-close    F1/I2                    via (*virtualHostWithInterceptors).closeInterceptors < (*usableRouteConfiguration).closeInterceptors < (*listenerWrapper).handleRDSUpdate < (*rdsWatcher).ResourceChanged
PHASE noflag-rds-replace-no-virtual-hosts: #13 icpt-close    F1/I3                    via (*virtualHostWithInterceptors).closeInterceptors < (*usableRouteConfiguration).closeInterceptors < (*listenerWrapper).handleRDSUpdate < (*rdsWatcher).ResourceChanged
PHASE noflag-rds-replace-no-virtual-hosts: SUMMARY first ref-release=#9 first filter-close=#11 last retired icpt-close=#13 retired-interceptors-never-closed=0 RELEASE_BEFORE_CLOSE=true FILTER_CLOSED_BEFORE_INTERCEPTORS=true
--- PASS: Test (6.21s)
--- PASS: Test/VerifyC1_LDSReplace_Then_Stop (2.05s)
--- PASS: Test/VerifyC1_ReplaceKeep_Then_DisableAll (2.10s)
--- PASS: Test/VerifyC1_ReplaceKeep_Then_NoVirtualHosts (2.06s)
ok  	google.golang.org/grpc/test/xds	7.238s
$ verify/repro/c1_c8_trace.sh ~/wt/0ed1f32c | grep -E 'noflag-rds|--- |^(ok|FAIL)'      # evalon/grpc-go-xd-0ed1f32c
PHASE noflag-rds-replace-filter-kept: #07 ref-release   F1 (refCnt before=3)     via (*filterChain).constructUsableRouteConfiguration < (*listenerWrapper).handleRDSUpdate < (*rdsWatcher).ResourceChanged
PHASE noflag-rds-replace-filter-kept: #08 icpt-close    F1/I1                    via (*usableRouteConfiguration).closeInterceptors < (*listenerWrapper).handleRDSUpdate < (*rdsWatcher).ResourceChanged
PHASE noflag-rds-replace-filter-kept: SUMMARY first ref-release=#7 first filter-close=#-1 last retired icpt-close=#8 retired-interceptors-never-closed=0 RELEASE_BEFORE_CLOSE=true FILTER_CLOSED_BEFORE_INTERCEPTORS=false
PHASE noflag-rds-replace-no-virtual-hosts: #09 ref-release   F1 (refCnt before=2)     via (*filterChain).constructUsableRouteConfiguration < (*listenerWrapper).handleRDSUpdate < (*rdsWatcher).ResourceChanged
PHASE noflag-rds-replace-no-virtual-hosts: #10 ref-release   F1 (refCnt before=1)     via (*filterChain).constructUsableRouteConfiguration < (*listenerWrapper).handleRDSUpdate < (*rdsWatcher).ResourceChanged
PHASE noflag-rds-replace-no-virtual-hosts: #11 filter-close  F1                       via (*filterChain).constructUsableRouteConfiguration < (*listenerWrapper).handleRDSUpdate < (*rdsWatcher).ResourceChanged
PHASE noflag-rds-replace-no-virtual-hosts: #12 icpt-close    F1/I2                    via (*usableRouteConfiguration).closeInterceptors < (*listenerWrapper).handleRDSUpdate < (*rdsWatcher).ResourceChanged
PHASE noflag-rds-replace-no-virtual-hosts: #13 icpt-close    F1/I3                    via (*usableRouteConfiguration).closeInterceptors < (*listenerWrapper).handleRDSUpdate < (*rdsWatcher).ResourceChanged
PHASE noflag-rds-replace-no-virtual-hosts: SUMMARY first ref-release=#9 first filter-close=#11 last retired icpt-close=#13 retired-interceptors-never-closed=0 RELEASE_BEFORE_CLOSE=true FILTER_CLOSED_BEFORE_INTERCEPTORS=true
--- PASS: Test (6.23s)
--- PASS: Test/VerifyC1_LDSReplace_Then_Stop (2.07s)
--- PASS: Test/VerifyC1_ReplaceKeep_Then_DisableAll (2.09s)
--- PASS: Test/VerifyC1_ReplaceKeep_Then_NoVirtualHosts (2.07s)
ok  	google.golang.org/grpc/test/xds	7.255s
$ verify/repro/c1_c8_trace.sh ~/wt/eb9c66bf | grep -E 'noflag-rds|--- |^(ok|FAIL)'      # evalon/grpc-go-xd-eb9c66bf
PHASE noflag-rds-replace-filter-kept: #07 ref-release   F1 (refCnt before=3)     via (*filterChain).constructUsableRouteConfiguration < (*listenerWrapper).handleRDSUpdate < (*rdsWatcher).ResourceChanged
PHASE noflag-rds-replace-filter-kept: #08 icpt-close    F1/I1                    via (*usableRouteConfiguration).closeInterceptors < (*listenerWrapper).handleRDSUpdate < (*rdsWatcher).ResourceChanged
PHASE noflag-rds-replace-filter-kept: SUMMARY first ref-release=#7 first filter-close=#-1 last retired icpt-close=#8 retired-interceptors-never-closed=0 RELEASE_BEFORE_CLOSE=true FILTER_CLOSED_BEFORE_INTERCEPTORS=false
PHASE noflag-rds-replace-no-virtual-hosts: #09 ref-release   F1 (refCnt before=2)     via (*filterChain).constructUsableRouteConfiguration < (*listenerWrapper).handleRDSUpdate < (*rdsWatcher).ResourceChanged
PHASE noflag-rds-replace-no-virtual-hosts: #10 ref-release   F1 (refCnt before=1)     via (*filterChain).constructUsableRouteConfiguration < (*listenerWrapper).handleRDSUpdate < (*rdsWatcher).ResourceChanged
PHASE noflag-rds-replace-no-virtual-hosts: #11 filter-close  F1                       via (*filterChain).constructUsableRouteConfiguration < (*listenerWrapper).handleRDSUpdate < (*rdsWatcher).ResourceChanged
PHASE noflag-rds-replace-no-virtual-hosts: #12 icpt-close    F1/I2                    via (*usableRouteConfiguration).closeInterceptors < (*listenerWrapper).handleRDSUpdate < (*rdsWatcher).ResourceChanged
PHASE noflag-rds-replace-no-virtual-hosts: #13 icpt-close    F1/I3                    via (*usableRouteConfiguration).closeInterceptors < (*listenerWrapper).handleRDSUpdate < (*rdsWatcher).ResourceChanged
PHASE noflag-rds-replace-no-virtual-hosts: SUMMARY first ref-release=#9 first filter-close=#11 last retired icpt-close=#13 retired-interceptors-never-closed=0 RELEASE_BEFORE_CLOSE=true FILTER_CLOSED_BEFORE_INTERCEPTORS=true
--- PASS: Test (6.25s)
--- PASS: Test/VerifyC1_LDSReplace_Then_Stop (2.06s)
--- PASS: Test/VerifyC1_ReplaceKeep_Then_DisableAll (2.09s)
--- PASS: Test/VerifyC1_ReplaceKeep_Then_NoVirtualHosts (2.09s)
ok  	google.golang.org/grpc/test/xds	7.280s
$ verify/repro/c1_c8_trace.sh ~/wt/e03d1e42 | grep -E 'noflag-rds|--- |^(ok|FAIL)'      # evalon/grpc-go-xd-e03d1e42
PHASE noflag-rds-replace-filter-kept: #07 ref-release   F1 (refCnt before=3)     via (*filterChain).constructUsableRouteConfiguration < (*listenerWrapper).handleRDSUpdate < (*rdsWatcher).ResourceChanged
PHASE noflag-rds-replace-filter-kept: #08 icpt-close    F1/I1                    via (*usableRouteConfiguration).close < (*filterChain).setUsableRouteConfiguration < (*listenerWrapper).handleRDSUpdate < (*rdsWatcher).ResourceChanged
PHASE noflag-rds-replace-filter-kept: SUMMARY first ref-release=#7 first filter-close=#-1 last retired icpt-close=#8 retired-interceptors-never-closed=0 RELEASE_BEFORE_CLOSE=true FILTER_CLOSED_BEFORE_INTERCEPTORS=false
PHASE noflag-rds-replace-no-virtual-hosts: #09 ref-release   F1 (refCnt before=2)     via (*filterChain).constructUsableRouteConfiguration < (*listenerWrapper).handleRDSUpdate < (*rdsWatcher).ResourceChanged
PHASE noflag-rds-replace-no-virtual-hosts: #10 ref-release   F1 (refCnt before=1)     via (*filterChain).constructUsableRouteConfiguration < (*listenerWrapper).handleRDSUpdate < (*rdsWatcher).ResourceChanged
PHASE noflag-rds-replace-no-virtual-hosts: #11 filter-close  F1                       via (*filterChain).constructUsableRouteConfiguration < (*listenerWrapper).handleRDSUpdate < (*rdsWatcher).ResourceChanged
PHASE noflag-rds-replace-no-virtual-hosts: #12 icpt-close    F1/I2                    via (*usableRouteConfiguration).close < (*filterChain).setUsableRouteConfiguration < (*listenerWrapper).handleRDSUpdate < (*rdsWatcher).ResourceChanged
PHASE noflag-rds-replace-no-virtual-hosts: #13 icpt-close    F1/I3                    via (*usableRouteConfiguration).close < (*filterChain).setUsableRouteConfiguration < (*listenerWrapper).handleRDSUpdate < (*rdsWatcher).ResourceChanged
PHASE noflag-rds-replace-no-virtual-hosts: SUMMARY first ref-release=#9 first filter-close=#11 last retired icpt-close=#13 retired-interceptors-never-closed=0 RELEASE_BEFORE_CLOSE=true FILTER_CLOSED_BEFORE_INTERCEPTORS=true
--- PASS: Test (6.23s)
--- PASS: Test/VerifyC1_LDSReplace_Then_Stop (2.07s)
--- PASS: Test/VerifyC1_ReplaceKeep_Then_DisableAll (2.08s)
--- PASS: Test/VerifyC1_ReplaceKeep_Then_NoVirtualHosts (2.08s)
ok  	google.golang.org/grpc/test/xds	7.257s
$ verify/repro/c1_c8_trace.sh ~/wt/98faa6aa | grep -E 'noflag-rds|--- |^(ok|FAIL)'      # evalon/grpc-go-xd-98faa6aa
PHASE noflag-rds-replace-filter-kept: #07 ref-release   F1 (refCnt before=3)     via (*filterChain).constructUsableRouteConfiguration < (*listenerWrapper).handleRDSUpdate < (*rdsWatcher).ResourceChanged
PHASE noflag-rds-replace-filter-kept: #08 icpt-close    F1/I1                    via (*usableRouteConfiguration).close < (*filterChain).setUsableRouteConfiguration < (*listenerWrapper).handleRDSUpdate < (*rdsWatcher).ResourceChanged
PHASE noflag-rds-replace-filter-kept: SUMMARY first ref-release=#7 first filter-close=#-1 last retired icpt-close=#8 retired-interceptors-never-closed=0 RELEASE_BEFORE_CLOSE=true FILTER_CLOSED_BEFORE_INTERCEPTORS=false
PHASE noflag-rds-replace-no-virtual-hosts: #09 ref-release   F1 (refCnt before=2)     via (*filterChain).constructUsableRouteConfiguration < (*listenerWrapper).handleRDSUpdate < (*rdsWatcher).ResourceChanged
PHASE noflag-rds-replace-no-virtual-hosts: #10 ref-release   F1 (refCnt before=1)     via (*filterChain).constructUsableRouteConfiguration < (*listenerWrapper).handleRDSUpdate < (*rdsWatcher).ResourceChanged
PHASE noflag-rds-replace-no-virtual-hosts: #11 filter-close  F1                       via (*filterChain).constructUsableRouteConfiguration < (*listenerWrapper).handleRDSUpdate < (*rdsWatcher).ResourceChanged
PHASE noflag-rds-replace-no-virtual-hosts: #12 icpt-close    F1/I2                    via (*usableRouteConfiguration).close < (*filterChain).setUsableRouteConfiguration < (*listenerWrapper).handleRDSUpdate < (*rdsWatcher).ResourceChanged
PHASE noflag-rds-replace-no-virtual-hosts: #13 icpt-close    F1/I3                    via (*usableRouteConfiguration).close < (*filterChain).setUsableRouteConfiguration < (*listenerWrapper).handleRDSUpdate < (*rdsWatcher).ResourceChanged
PHASE noflag-rds-replace-no-virtual-hosts: SUMMARY first ref-release=#9 first filter-close=#11 last retired icpt-close=#13 retired-interceptors-never-closed=0 RELEASE_BEFORE_CLOSE=true FILTER_CLOSED_BEFORE_INTERCEPTORS=true
--- PASS: Test (6.20s)
--- PASS: Test/VerifyC1_LDSReplace_Then_Stop (2.07s)
--- PASS: Test/VerifyC1_ReplaceKeep_Then_DisableAll (2.07s)
--- PASS: Test/VerifyC1_ReplaceKeep_Then_NoVirtualHosts (2.06s)
ok  	google.golang.org/grpc/test/xds	7.229s
$ verify/repro/c1_c8_trace.sh ~/wt/2ffad480 | grep -E 'noflag-rds|--- |^(ok|FAIL)'      # evalon/grpc-go-xd-2ffad480
PHASE noflag-rds-replace-filter-kept: #07 ref-release   F1 (refCnt before=3)     via (*filterChain).constructUsableRouteConfiguration < (*listenerWrapper).handleRDSUpdate < (*rdsWatcher).ResourceChanged
PHASE noflag-rds-replace-filter-kept: #08 icpt-close    F1/I1                    via (*usableRouteConfiguration).close < (*filterChain).updateUsableRouteConfiguration < (*listenerWrapper).handleRDSUpdate < (*rdsWatcher).ResourceChanged
PHASE noflag-rds-replace-filter-kept: SUMMARY first ref-release=#7 first filter-close=#-1 last retired icpt-close=#8 retired-interceptors-never-closed=0 RELEASE_BEFORE_CLOSE=true FILTER_CLOSED_BEFORE_INTERCEPTORS=false
PHASE noflag-rds-replace-no-virtual-hosts: #09 ref-release   F1 (refCnt before=2)     via (*filterChain).constructUsableRouteConfiguration < (*listenerWrapper).handleRDSUpdate < (*rdsWatcher).ResourceChanged
PHASE noflag-rds-replace-no-virtual-hosts: #10 ref-release   F1 (refCnt before=1)     via (*filterChain).constructUsableRouteConfiguration < (*listenerWrapper).handleRDSUpdate < (*rdsWatcher).ResourceChanged
PHASE noflag-rds-replace-no-virtual-hosts: #11 filter-close  F1                       via (*filterChain).constructUsableRouteConfiguration < (*listenerWrapper).handleRDSUpdate < (*rdsWatcher).ResourceChanged
PHASE noflag-rds-replace-no-virtual-hosts: #12 icpt-close    F1/I2                    via (*usableRouteConfiguration).close < (*filterChain).updateUsableRouteConfiguration < (*listenerWrapper).handleRDSUpdate < (*rdsWatcher).ResourceChanged
PHASE noflag-rds-replace-no-virtual-hosts: #13 icpt-close    F1/I3                    via (*usableRouteConfiguration).close < (*filterChain).updateUsableRouteConfiguration < (*listenerWrapper).handleRDSUpdate < (*rdsWatcher).ResourceChanged
PHASE noflag-rds-replace-no-virtual-hosts: SUMMARY first ref-release=#9 first filter-close=#11 last retired icpt-close=#13 retired-interceptors-never-closed=0 RELEASE_BEFORE_CLOSE=true FILTER_CLOSED_BEFORE_INTERCEPTORS=true
--- PASS: Test (6.25s)
--- PASS: Test/VerifyC1_LDSReplace_Then_Stop (2.07s)
--- PASS: Test/VerifyC1_ReplaceKeep_Then_DisableAll (2.09s)
--- PASS: Test/VerifyC1_ReplaceKeep_Then_NoVirtualHosts (2.08s)
ok  	google.golang.org/grpc/test/xds	7.278s
$ verify/repro/c1_c8_trace.sh ~/wt/dad62956 | grep -E 'noflag-rds|--- |^(ok|FAIL)'      # evalon/grpc-go-xd-dad62956
PHASE noflag-rds-replace-filter-kept: #07 ref-release   F1 (refCnt before=3)     via (*filterChain).constructUsableRouteConfiguration < (*listenerWrapper).handleRDSUpdate < (*rdsWatcher).ResourceChanged
PHASE noflag-rds-replace-filter-kept: #08 icpt-close    F1/I1                    via (*usableRouteConfiguration).close < (*filterChain).updateUsableRouteConfiguration < (*listenerWrapper).handleRDSUpdate < (*rdsWatcher).ResourceChanged
PHASE noflag-rds-replace-filter-kept: SUMMARY first ref-release=#7 first filter-close=#-1 last retired icpt-close=#8 retired-interceptors-never-closed=0 RELEASE_BEFORE_CLOSE=true FILTER_CLOSED_BEFORE_INTERCEPTORS=false
PHASE noflag-rds-replace-no-virtual-hosts: #09 ref-release   F1 (refCnt before=2)     via (*filterChain).constructUsableRouteConfiguration < (*listenerWrapper).handleRDSUpdate < (*rdsWatcher).ResourceChanged
PHASE noflag-rds-replace-no-virtual-hosts: #10 ref-release   F1 (refCnt before=1)     via (*filterChain).constructUsableRouteConfiguration < (*listenerWrapper).handleRDSUpdate < (*rdsWatcher).ResourceChanged
PHASE noflag-rds-replace-no-virtual-hosts: #11 filter-close  F1                       via (*filterChain).constructUsableRouteConfiguration < (*listenerWrapper).handleRDSUpdate < (*rdsWatcher).ResourceChanged
PHASE noflag-rds-replace-no-virtual-hosts: #12 icpt-close    F1/I2                    via (*usableRouteConfiguration).close < (*filterChain).updateUsableRouteConfiguration < (*listenerWrapper).handleRDSUpdate < (*rdsWatcher).ResourceChanged
PHASE noflag-rds-replace-no-virtual-hosts: #13 icpt-close    F1/I3                    via (*usableRouteConfiguration).close < (*filterChain).updateUsableRouteConfiguration < (*listenerWrapper).handleRDSUpdate < (*rdsWatcher).ResourceChanged
PHASE noflag-rds-replace-no-virtual-hosts: SUMMARY first ref-release=#9 first filter-close=#11 last retired icpt-close=#13 retired-interceptors-never-closed=0 RELEASE_BEFORE_CLOSE=true FILTER_CLOSED_BEFORE_INTERCEPTORS=true
--- PASS: Test (6.23s)
--- PASS: Test/VerifyC1_LDSReplace_Then_Stop (2.06s)
--- PASS: Test/VerifyC1_ReplaceKeep_Then_DisableAll (2.08s)
--- PASS: Test/VerifyC1_ReplaceKeep_Then_NoVirtualHosts (2.08s)
ok  	google.golang.org/grpc/test/xds	7.254s
$ verify/repro/c1_c8_trace.sh ~/wt/6851db1c | grep -E 'noflag-rds|--- |^(ok|FAIL)'      # evalon/grpc-go-xd-6851db1c
PHASE noflag-rds-replace-filter-kept: #07 ref-release   F1 (refCnt before=3)     via (*filterChain).constructUsableRouteConfiguration < (*listenerWrapper).handleRDSUpdate < (*rdsWatcher).ResourceChanged
PHASE noflag-rds-replace-filter-kept: #08 icpt-close    F1/I1                    via (*usableRouteConfiguration).close < (*filterChain).setUsableRouteConfiguration < (*listenerWrapper).handleRDSUpdate < (*rdsWatcher).ResourceChanged
PHASE noflag-rds-replace-filter-kept: SUMMARY first ref-release=#7 first filter-close=#-1 last retired icpt-close=#8 retired-interceptors-never-closed=0 RELEASE_BEFORE_CLOSE=true FILTER_CLOSED_BEFORE_INTERCEPTORS=false
PHASE noflag-rds-replace-no-virtual-hosts: #09 ref-release   F1 (refCnt before=2)     via (*filterChain).constructUsableRouteConfiguration < (*listenerWrapper).handleRDSUpdate < (*rdsWatcher).ResourceChanged
PHASE noflag-rds-replace-no-virtual-hosts: #10 ref-release   F1 (refCnt before=1)     via (*filterChain).constructUsableRouteConfiguration < (*listenerWrapper).handleRDSUpdate < (*rdsWatcher).ResourceChanged
PHASE noflag-rds-replace-no-virtual-hosts: #11 filter-close  F1                       via (*filterChain).constructUsableRouteConfiguration < (*listenerWrapper).handleRDSUpdate < (*rdsWatcher).ResourceChanged
PHASE noflag-rds-replace-no-virtual-hosts: #12 icpt-close    F1/I2                    via (*usableRouteConfiguration).close < (*filterChain).setUsableRouteConfiguration < (*listenerWrapper).handleRDSUpdate < (*rdsWatcher).ResourceChanged
PHASE noflag-rds-replace-no-virtual-hosts: #13 icpt-close    F1/I3                    via (*usableRouteConfiguration).close < (*filterChain).setUsableRouteConfiguration < (*listenerWrapper).handleRDSUpdate < (*rdsWatcher).ResourceChanged
PHASE noflag-rds-replace-no-virtual-hosts: SUMMARY first ref-release=#9 first filter-close=#11 last retired icpt-close=#13 retired-interceptors-never-closed=0 RELEASE_BEFORE_CLOSE=true FILTER_CLOSED_BEFORE_INTERCEPTORS=true
--- PASS: Test (6.21s)
--- PASS: Test/VerifyC1_LDSReplace_Then_Stop (2.05s)
--- PASS: Test/VerifyC1_ReplaceKeep_Then_DisableAll (2.07s)
--- PASS: Test/VerifyC1_ReplaceKeep_Then_NoVirtualHosts (2.08s)
ok  	google.golang.org/grpc/test/xds	7.235s
$ verify/repro/c1_c8_trace.sh ~/wt/3e0a44dd | grep -E 'noflag-rds|--- |^(ok|FAIL)'      # evalon/grpc-go-xd-3e0a44dd
PHASE noflag-rds-replace-filter-kept: #07 ref-release   F1 (refCnt before=3)     via (*filterChain).constructUsableRouteConfiguration < (*listenerWrapper).handleRDSUpdate < (*rdsWatcher).ResourceChanged
PHASE noflag-rds-replace-filter-kept: #08 icpt-close    F1/I1                    via (*usableRouteConfiguration).close < (*filterChain).updateUsableRouteConfiguration < (*listenerWrapper).handleRDSUpdate < (*rdsWatcher).ResourceChanged
PHASE noflag-rds-replace-filter-kept: SUMMARY first ref-release=#7 first filter-close=#-1 last retired icpt-close=#8 retired-interceptors-never-closed=0 RELEASE_BEFORE_CLOSE=true FILTER_CLOSED_BEFORE_INTERCEPTORS=false
PHASE noflag-rds-replace-no-virtual-hosts: #09 ref-release   F1 (refCnt before=2)     via (*filterChain).constructUsableRouteConfiguration < (*listenerWrapper).handleRDSUpdate < (*rdsWatcher).ResourceChanged
PHASE noflag-rds-replace-no-virtual-hosts: #10 ref-release   F1 (refCnt before=1)     via (*filterChain).constructUsableRouteConfiguration < (*listenerWrapper).handleRDSUpdate < (*rdsWatcher).ResourceChanged
PHASE noflag-rds-replace-no-virtual-hosts: #11 filter-close  F1                       via (*filterChain).constructUsableRouteConfiguration < (*listenerWrapper).handleRDSUpdate < (*rdsWatcher).ResourceChanged
PHASE noflag-rds-replace-no-virtual-hosts: #12 icpt-close    F1/I2                    via (*usableRouteConfiguration).close < (*filterChain).updateUsableRouteConfiguration < (*listenerWrapper).handleRDSUpdate < (*rdsWatcher).ResourceChanged
PHASE noflag-rds-replace-no-virtual-hosts: #13 icpt-close    F1/I3                    via (*usableRouteConfiguration).close < (*filterChain).updateUsableRouteConfiguration < (*listenerWrapper).handleRDSUpdate < (*rdsWatcher).ResourceChanged
PHASE noflag-rds-replace-no-virtual-hosts: SUMMARY first ref-release=#9 first filter-close=#11 last retired icpt-close=#13 retired-interceptors-never-closed=0 RELEASE_BEFORE_CLOSE=true FILTER_CLOSED_BEFORE_INTERCEPTORS=true
--- PASS: Test (6.25s)
--- PASS: Test/VerifyC1_LDSReplace_Then_Stop (2.07s)
--- PASS: Test/VerifyC1_ReplaceKeep_Then_DisableAll (2.10s)
--- PASS: Test/VerifyC1_ReplaceKeep_Then_NoVirtualHosts (2.08s)
ok  	google.golang.org/grpc/test/xds	7.280s
$ verify/repro/c1_c8_trace.sh ~/wt/eb19a38b | grep -E 'noflag-rds|--- |^(ok|FAIL)'      # evalon/grpc-go-xd-eb19a38b
PHASE noflag-rds-replace-filter-kept: #07 ref-release   F1 (refCnt before=3)     via (*filterChain).constructUsableRouteConfiguration < (*listenerWrapper).handleRDSUpdate < (*rdsWatcher).ResourceChanged
PHASE noflag-rds-replace-filter-kept: #08 icpt-close    F1/I1                    via (*usableRouteConfiguration).closeInterceptors < (*filterChain).updateUsableRouteConfiguration < (*listenerWrapper).handleRDSUpdate < (*rdsWatcher).ResourceChanged
PHASE noflag-rds-replace-filter-kept: SUMMARY first ref-release=#7 first filter-close=#-1 last retired icpt-close=#8 retired-interceptors-never-closed=0 RELEASE_BEFORE_CLOSE=true FILTER_CLOSED_BEFORE_INTERCEPTORS=false
PHASE noflag-rds-replace-no-virtual-hosts: #09 ref-release   F1 (refCnt before=2)     via (*filterChain).constructUsableRouteConfiguration < (*listenerWrapper).handleRDSUpdate < (*rdsWatcher).ResourceChanged
PHASE noflag-rds-replace-no-virtual-hosts: #10 ref-release   F1 (refCnt before=1)     via (*filterChain).constructUsableRouteConfiguration < (*listenerWrapper).handleRDSUpdate < (*rdsWatcher).ResourceChanged
PHASE noflag-rds-replace-no-virtual-hosts: #11 filter-close  F1                       via (*filterChain).constructUsableRouteConfiguration < (*listenerWrapper).handleRDSUpdate < (*rdsWatcher).ResourceChanged
PHASE noflag-rds-replace-no-virtual-hosts: #12 icpt-close    F1/I2                    via (*usableRouteConfiguration).closeInterceptors < (*filterChain).updateUsableRouteConfiguration < (*listenerWrapper).handleRDSUpdate < (*rdsWatcher).ResourceChanged
PHASE noflag-rds-replace-no-virtual-hosts: #13 icpt-close    F1/I3                    via (*usableRouteConfiguration).closeInterceptors < (*filterChain).updateUsableRouteConfiguration < (*listenerWrapper).handleRDSUpdate < (*rdsWatcher).ResourceChanged
PHASE noflag-rds-replace-no-virtual-hosts: SUMMARY first ref-release=#9 first filter-close=#11 last retired icpt-close=#13 retired-interceptors-never-closed=0 RELEASE_BEFORE_CLOSE=true FILTER_CLOSED_BEFORE_INTERCEPTORS=true
--- PASS: Test (6.22s)
--- PASS: Test/VerifyC1_LDSReplace_Then_Stop (2.05s)
--- PASS: Test/VerifyC1_ReplaceKeep_Then_DisableAll (2.10s)
--- PASS: Test/VerifyC1_ReplaceKeep_Then_NoVirtualHosts (2.06s)
ok  	google.golang.org/grpc/test/xds	7.246s
$ verify/repro/c1_c8_trace.sh ~/wt/perfect | grep -E 'noflag-rds|--- |^(ok|FAIL)'      # audited branch (control)
PHASE noflag-rds-replace-filter-kept: #07 icpt-close    F1/I1                    via (*usableRouteConfiguration).stop < (*filterChain).applyConfiguration < (*filterChain).updateUsableRouteConfiguration < (*listenerWrapper).handleRDSUpdate < (*rdsWatcher).ResourceChanged
PHASE noflag-rds-replace-filter-kept: #08 ref-release   F1 (refCnt before=3)     via (*filterChain).applyConfiguration < (*filterChain).updateUsableRouteConfiguration < (*listenerWrapper).handleRDSUpdate < (*rdsWatcher).ResourceChanged
PHASE noflag-rds-replace-filter-kept: SUMMARY first ref-release=#8 first filter-close=#-1 last retired icpt-close=#7 retired-interceptors-never-closed=0 RELEASE_BEFORE_CLOSE=false FILTER_CLOSED_BEFORE_INTERCEPTORS=false
PHASE noflag-rds-replace-no-virtual-hosts: #09 icpt-close    F1/I2                    via (*usableRouteConfiguration).stop < (*filterChain).applyConfiguration < (*filterChain).updateUsableRouteConfiguration < (*listenerWrapper).handleRDSUpdate < (*rdsWatcher).ResourceChanged
PHASE noflag-rds-replace-no-virtual-hosts: #10 icpt-close    F1/I3                    via (*usableRouteConfiguration).stop < (*filterChain).applyConfiguration < (*filterChain).updateUsableRouteConfiguration < (*listenerWrapper).handleRDSUpdate < (*rdsWatcher).ResourceChanged
PHASE noflag-rds-replace-no-virtual-hosts: #11 ref-release   F1 (refCnt before=2)     via (*filterChain).applyConfiguration < (*filterChain).updateUsableRouteConfiguration < (*listenerWrapper).handleRDSUpdate < (*rdsWatcher).ResourceChanged
PHASE noflag-rds-replace-no-virtual-hosts: #12 ref-release   F1 (refCnt before=1)     via (*filterChain).applyConfiguration < (*filterChain).updateUsableRouteConfiguration < (*listenerWrapper).handleRDSUpdate < (*rdsWatcher).ResourceChanged
PHASE noflag-rds-replace-no-virtual-hosts: #13 filter-close  F1                       via (*filterChain).applyConfiguration < (*filterChain).updateUsableRouteConfiguration < (*listenerWrapper).handleRDSUpdate < (*rdsWatcher).ResourceChanged
PHASE noflag-rds-replace-no-virtual-hosts: SUMMARY first ref-release=#11 first filter-close=#13 last retired icpt-close=#10 retired-interceptors-never-closed=0 RELEASE_BEFORE_CLOSE=false FILTER_CLOSED_BEFORE_INTERCEPTORS=false
--- PASS: Test (6.19s)
--- PASS: Test/VerifyC1_LDSReplace_Then_Stop (2.07s)
--- PASS: Test/VerifyC1_ReplaceKeep_Then_DisableAll (2.08s)
--- PASS: Test/VerifyC1_ReplaceKeep_Then_NoVirtualHosts (2.04s)
ok  	google.golang.org/grpc/test/xds	7.225s
```

On all 14 claim branches the no-flag replacement shows the same order as the flagged one: `ref-release` (#09, #10) and `filter-close` (#11) from `constructUsableRouteConfiguration` precede `icpt-close` (#12, #13); `RELEASE_BEFORE_CLOSE=true FILTER_CLOSED_BEFORE_INTERCEPTORS=true`. The audited branch closes interceptors (#09, #10) before releasing (#11, #12) and closing the filter (#13).

### C1 · control: audited branch (HEAD 614cb739)

```console
$ verify/repro/c1_c8_trace.sh ~/wt/perfect
PHASE lds-replace: 1 interceptor(s) open at phase start
PHASE lds-replace: #03 ref-acquire   F1 (refCnt before=1)     via getOrCreateServerFilterWithMap < (*listenerWrapper).getOrCreateServerFilterLocked < (*filterChain).newInterceptor < (*filterChain).convertVirtualHost < (*filterChain).updateUsableRouteConfiguration < (*listenerWrapper).instantiateFilterChainRoutingConfigurationsLocked < (*listenerWrapper).maybeUpdateFilterChains < (*ldsWatcher).ResourceChanged
PHASE lds-replace: #04 icpt-build    F1/I2                    via (*filterChain).newInterceptor < (*filterChain).convertVirtualHost < (*filterChain).updateUsableRouteConfiguration < (*listenerWrapper).instantiateFilterChainRoutingConfigurationsLocked < (*listenerWrapper).maybeUpdateFilterChains < (*ldsWatcher).ResourceChanged
PHASE lds-replace: #05 icpt-close    F1/I1                    via (*usableRouteConfiguration).stop < (*filterChainManager).stop < (*listenerWrapper).maybeUpdateFilterChains < (*ldsWatcher).ResourceChanged
PHASE lds-replace: #06 ref-release   F1 (refCnt before=2)     via (*filterChain).stop < (*filterChainManager).stop < (*listenerWrapper).maybeUpdateFilterChains < (*ldsWatcher).ResourceChanged
PHASE lds-replace: SUMMARY first ref-release=#6 first filter-close=#-1 last retired icpt-close=#5 retired-interceptors-never-closed=0 RELEASE_BEFORE_CLOSE=false FILTER_CLOSED_BEFORE_INTERCEPTORS=false
PHASE server-stop: 1 interceptor(s) open at phase start
PHASE server-stop: #07 icpt-close    F1/I2                    via (*usableRouteConfiguration).stop < (*filterChainManager).stop < (*listenerWrapper).Close
PHASE server-stop: #08 ref-release   F1 (refCnt before=1)     via (*filterChain).stop < (*filterChainManager).stop < (*listenerWrapper).Close
PHASE server-stop: #09 filter-close  F1                       via (*filterChain).stop < (*filterChainManager).stop < (*listenerWrapper).Close
PHASE server-stop: SUMMARY first ref-release=#8 first filter-close=#9 last retired icpt-close=#7 retired-interceptors-never-closed=0 RELEASE_BEFORE_CLOSE=false FILTER_CLOSED_BEFORE_INTERCEPTORS=false
PHASE initial: SUMMARY first ref-release=#-1 first filter-close=#-1 last retired icpt-close=#-1 retired-interceptors-never-closed=0 RELEASE_BEFORE_CLOSE=false FILTER_CLOSED_BEFORE_INTERCEPTORS=false
PHASE rds-replace-filter-kept: 1 interceptor(s) open at phase start
PHASE rds-replace-filter-kept: #03 ref-acquire   F1 (refCnt before=1)     via getOrCreateServerFilterWithMap < (*listenerWrapper).getOrCreateServerFilterLocked < (*filterChain).newInterceptor < (*filterChain).convertVirtualHost < (*filterChain).updateUsableRouteConfiguration < (*listenerWrapper).handleRDSUpdate < (*rdsWatcher).ResourceChanged
PHASE rds-replace-filter-kept: #04 icpt-build    F1/I2                    via (*filterChain).newInterceptor < (*filterChain).convertVirtualHost < (*filterChain).updateUsableRouteConfiguration < (*listenerWrapper).handleRDSUpdate < (*rdsWatcher).ResourceChanged
PHASE rds-replace-filter-kept: #05 ref-acquire   F1 (refCnt before=2)     via getOrCreateServerFilterWithMap < (*listenerWrapper).getOrCreateServerFilterLocked < (*filterChain).newInterceptor < (*filterChain).convertVirtualHost < (*filterChain).updateUsableRouteConfiguration < (*listenerWrapper).handleRDSUpdate < (*rdsWatcher).ResourceChanged
PHASE rds-replace-filter-kept: #06 icpt-build    F1/I3                    via (*filterChain).newInterceptor < (*filterChain).convertVirtualHost < (*filterChain).updateUsableRouteConfiguration < (*listenerWrapper).handleRDSUpdate < (*rdsWatcher).ResourceChanged
PHASE rds-replace-filter-kept: #07 icpt-close    F1/I1                    via (*usableRouteConfiguration).stop < (*filterChain).applyConfiguration < (*filterChain).updateUsableRouteConfiguration < (*listenerWrapper).handleRDSUpdate < (*rdsWatcher).ResourceChanged
PHASE rds-replace-filter-kept: #08 ref-release   F1 (refCnt before=3)     via (*filterChain).applyConfiguration < (*filterChain).updateUsableRouteConfiguration < (*listenerWrapper).handleRDSUpdate < (*rdsWatcher).ResourceChanged
PHASE rds-replace-filter-kept: SUMMARY first ref-release=#8 first filter-close=#-1 last retired icpt-close=#7 retired-interceptors-never-closed=0 RELEASE_BEFORE_CLOSE=false FILTER_CLOSED_BEFORE_INTERCEPTORS=false
PHASE rds-replace-filter-disabled-on-every-route: 2 interceptor(s) open at phase start
PHASE rds-replace-filter-disabled-on-every-route: #09 icpt-close    F1/I2                    via (*usableRouteConfiguration).stop < (*filterChain).applyConfiguration < (*filterChain).updateUsableRouteConfiguration < (*listenerWrapper).handleRDSUpdate < (*rdsWatcher).ResourceChanged
PHASE rds-replace-filter-disabled-on-every-route: #10 icpt-close    F1/I3                    via (*usableRouteConfiguration).stop < (*filterChain).applyConfiguration < (*filterChain).updateUsableRouteConfiguration < (*listenerWrapper).handleRDSUpdate < (*rdsWatcher).ResourceChanged
PHASE rds-replace-filter-disabled-on-every-route: #11 ref-release   F1 (refCnt before=2)     via (*filterChain).applyConfiguration < (*filterChain).updateUsableRouteConfiguration < (*listenerWrapper).handleRDSUpdate < (*rdsWatcher).ResourceChanged
PHASE rds-replace-filter-disabled-on-every-route: #12 ref-release   F1 (refCnt before=1)     via (*filterChain).applyConfiguration < (*filterChain).updateUsableRouteConfiguration < (*listenerWrapper).handleRDSUpdate < (*rdsWatcher).ResourceChanged
PHASE rds-replace-filter-disabled-on-every-route: #13 filter-close  F1                       via (*filterChain).applyConfiguration < (*filterChain).updateUsableRouteConfiguration < (*listenerWrapper).handleRDSUpdate < (*rdsWatcher).ResourceChanged
PHASE rds-replace-filter-disabled-on-every-route: SUMMARY first ref-release=#11 first filter-close=#13 last retired icpt-close=#10 retired-interceptors-never-closed=0 RELEASE_BEFORE_CLOSE=false FILTER_CLOSED_BEFORE_INTERCEPTORS=false
--- PASS: Test (4.18s)
--- PASS: Test/VerifyC1_LDSReplace_Then_Stop (2.08s)
--- PASS: Test/VerifyC1_ReplaceKeep_Then_DisableAll (2.10s)
ok  	google.golang.org/grpc/test/xds	5.204s
```

## C2

Claim: the added or changed tests contain lifecycle waits that (part 1, *lifecycle synchronization*) fail to establish completion before assertions, or (part 2, *bounded waits*) fail to terminate within a bounded interval. Either part establishes the violation. Adjudicated independently on 14 branches.

Method.

- Part 1 — controlled scheduling probe. `verify/repro/c2_sched_probe.sh <checkout> close|construct [delay] [count]` runs the tests the branch added (names taken from `git diff 4ee6ac46 HEAD -- '*_test.go'`) with `go test -race -v` in `./internal/xds/server ./test/xds`, first unperturbed, then with one sleep enabled: `VERIFY_CLOSE_DELAY` (top of `interceptorList.Close`: a retired interceptor finishes closing 150ms later) or `VERIFY_CONSTRUCT_DELAY` (top of `constructUsableRouteConfiguration`: each filter chain is updated 150ms later). The sleeps do not change the order of any production operation. A test that waits for the transitions it asserts on still passes (it only gets slower); a test whose assertion can run before those transitions finish fails. The source lines of the wait and the assertion are quoted for each branch.
- Part 2 — absent notification / absent connection. `verify/repro/c2_absent_notification.sh <checkout> 60s` makes `interceptorList.AllowRPC` return without invoking any interceptor (the tests' "interceptor saw the new path" notification never arrives) and runs the branch's added `test/xds` tests with a 60s binary timeout: a bounded wait fails on the test's own 10s context, an unbounded one hangs until the binary timeout. For the two branches suspected of unbounded waits there are dedicated scripts (`c2_unbounded_wait_2a01d623.sh`, `c2_unbounded_wait_311db1b4.sh`, 40s binary timeout).
- A source scan of the added test lines for blocking operations (`Accept()`, bare channel receives, `Wait()`, `select` cases) on all 14 branches:

```console
$ for b in eababd58 dab66e4f 415a74da 0ed1f32c eb9c66bf e03d1e42 98faa6aa 2ffad480 74924778 dad62956 79540ac1 eb19a38b 2a01d623 311db1b4; do echo "== $b"; git -C ~/wt/$b diff 4ee6ac46 HEAD -- '*_test.go' | grep -E '^\+' | grep -nE '\.Accept\(\)|^\+\s*<-[a-zA-Z.]+\s*$|:?= <-[a-zA-Z.]+\b|\.Wait\(\)|case .*<-' | grep -vE 'ctx.Done|time.After' | cut -c1-150; done
== eababd58
205:+			case <-done:
224:+	wg.Wait()
441:+			case <-servingCh:
455:+			case got := <-pathCh:
== dab66e4f
270:+	case <-servingCh:
284:+			case got := <-pathCh:
== 415a74da
208:+	case <-servingCh:
221:+			case got := <-pathCh:
== 0ed1f32c
122:+	case <-servingCh:
137:+			case got := <-pathCh:
== eb9c66bf
255:+	case <-servingCh:
268:+			case got := <-pathCh:
== e03d1e42
372:+	case <-servingCh:
384:+	case cfg := <-pathCh:
422:+			case cfg := <-pathCh:
== 98faa6aa
328:+	case <-servingCh:
340:+	case cfg := <-pathCh:
375:+			case cfg := <-pathCh:
== 2ffad480
194:+		case servingCh <- struct{}{}:
223:+	case <-servingCh:
422:+	case <-servingCh:
434:+	case cfg := <-pathCh:
470:+			case cfg := <-pathCh:
== 74924778
284:+	case <-servingCh:
296:+	case cfg := <-pathCh:
331:+			case cfg := <-pathCh:
== dad62956
279:+			case modeCh <- mode:
312:+		case mode = <-modeCh:
496:+	case <-servingCh:
508:+	case cfg := <-pathCh:
542:+			case cfg := <-pathCh:
== 79540ac1
297:+		case path := <-pathCh:
430:+	case <-servingCh:
== eb19a38b
310:+	case <-servingCh:
323:+	case cfg := <-pathCh:
360:+			case cfg := <-pathCh:
== 2a01d623
250:+				case <-resume:
259:+			case <-entered:
281:+			case err := <-done:
318:+		accepted, acceptErr = l.Accept()
327:+	case <-acceptedDone:
428:+			case <-serving:
438:+			if got := <-pathCh; got == path {
== 311db1b4
71:+		case <-i.resume:
191:+	conn, err := l.Accept()
314:+			case <-oldInterceptor.entered:
335:+			case err := <-done:
405:+	case <-servingCh:
428:+		case path := <-pathCh:
```

Only two hits are blocking operations on the test goroutine outside a `select`: `if got := <-pathCh; got == path {` on 2a01d623 and `conn, err := l.Accept()` on 311db1b4 (the `l.Accept()` on 2a01d623 runs in a goroutine whose completion is awaited by `select { case <-acceptedDone: case <-ctx.Done(): ... }`). Whether the `select`-based waits of the `test/xds` tests are bounded is shown per branch by the absent-notification probe.

Result summary:

| branch | HEAD | part 1: assertion vs. lifecycle completion (probe) | part 2: blocking waits (absent event) |
|---|---|---|---|
| eababd58 | de70f1b0 | **fails** under `VERIFY_CLOSE_DELAY=150ms`: `xds_server_rds_update_test.go:197: After RDS update 1, 2 interceptor instances are alive (created: 2, destroyed: 0), want 1` | bounded: fails after 10s with `xds_server_rds_update_test.go:191: Timeout waiting for RDS update 0 to be applied: context deadline exceeded` |
| dab66e4f | 3133831c | **fails** under `VERIFY_CONSTRUCT_DELAY=150ms`: `xds_server_filter_state_retention_test.go:840: Created 21 interceptor instances, want: 22` | bounded: fails after 10s with `xds_server_filter_state_retention_test.go:819: Timeout waiting for interceptor to see path "path-0"` |
| 415a74da | f70da5b8 | **fails** under `VERIFY_CLOSE_DELAY=150ms`: `xds_server_filter_state_retention_test.go:792: After RDS update 1, destroyed 0 interceptor instances, want: 1` | bounded: fails after 10s with `xds_server_filter_state_retention_test.go:760: Timeout waiting for interceptor to be invoked with path "path-0"` |
| 0ed1f32c | 10d73679 | **fails** under `VERIFY_CLOSE_DELAY=150ms`: `xds_server_filter_state_retention_test.go:790: After 1 RouteConfiguration updates, got 3 live interceptor instances, want: 2` | bounded: fails after 10s with `xds_server_filter_state_retention_test.go:773: Timeout waiting for interceptor to be invoked with path "path-0"` |
| eb9c66bf | 0a3344da | **fails** under `VERIFY_CLOSE_DELAY=150ms`: `xds_server_route_config_update_test.go:211: Live interceptors = 2 (created: 2, destroyed: 0), want: 1` | bounded: fails after 10s with `xds_server_route_config_update_test.go:187: Timeout waiting for route configuration with path "path-0" to be applied` |
| e03d1e42 | fc9cb150 | **fails** under `VERIFY_CLOSE_DELAY=150ms`: `xds_server_filter_state_retention_test.go:848: After RouteConfiguration update 1: created 3 interceptor instances, want: 4` | bounded: fails after 10s with `xds_server_filter_state_retention_test.go:796: Timeout waiting for interceptor to be invoked` |
| 98faa6aa | 303f98bc | **fails** under `VERIFY_CLOSE_DELAY=150ms`: `xds_server_filter_state_retention_test.go:821: After update 1: destroyed 0 interceptor instances, want: 1` | bounded: fails after 10s with `xds_server_filter_state_retention_test.go:769: Timeout waiting for interceptor to be invoked` |
| 2ffad480 | 784e2b0b | **fails** under `VERIFY_CLOSE_DELAY=150ms`: `xds_server_filter_state_retention_test.go:840: Created 3 interceptor instances after update 1, want: 4` | bounded: fails after 10s with `xds_server_filter_state_retention_test.go:796: Timeout waiting for interceptor to be invoked` |
| 74924778 | e4739699 | **fails** under `VERIFY_CLOSE_DELAY=150ms`: `xds_server_filter_state_retention_test.go:848: After update 1: created 3 interceptor instances, want: 4` | bounded: fails after 10s with `xds_server_filter_state_retention_test.go:799: Timeout waiting for interceptor to be invoked` |
| dad62956 | 30fef0af | **fails** under `VERIFY_CLOSE_DELAY=150ms`: `xds_server_filter_state_retention_test.go:827: Destroyed 0 interceptor instances after update 1, want: 1` | bounded: fails after 10s with `xds_server_filter_state_retention_test.go:779: Timeout waiting for interceptor to be invoked` |
| 79540ac1 | a01cc52b | **fails** under `VERIFY_CLOSE_DELAY=150ms`: `xds_server_filter_state_retention_test.go:811: After route config update 1: destroyed 0 interceptor instances, want: 1` | bounded: fails after 10s with `xds_server_filter_state_retention_test.go:776: Timeout waiting for interceptor to report path "path-0": context deadline exceeded` |
| eb19a38b | 7c0ac8b4 | **fails** under `VERIFY_CLOSE_DELAY=150ms`: `xds_server_filter_state_retention_test.go:863: After RouteConfiguration update 1: created 3 interceptor instances, want: 4` | bounded: fails after 10s with `xds_server_filter_state_retention_test.go:810: Timeout waiting for interceptor to be invoked` |
| 2a01d623 | 0ccf0147 | passes under both `VERIFY_CLOSE_DELAY=150ms` and `VERIFY_CONSTRUCT_DELAY=150ms` (not reproduced) | **hangs**: bare `<-pathCh` (line 246) blocked until the binary timeout |
| 311db1b4 | 5dc5968c | passes under both `VERIFY_CLOSE_DELAY=150ms` and `VERIFY_CONSTRUCT_DELAY=150ms` (not reproduced) | **hangs**: bare `l.Accept()` (`filter_chain_lifecycle_test.go:190`) blocked until the binary timeout; its `test/xds` test is bounded |


Unperturbed, the same tests pass (first block of every probe output below), and five of the affected `test/xds` tests were also run 40 times in a row without any perturbation:

```console
$ cd ~/wt/<id> && go test -race -count=40 -timeout 600s -v -run "^Test\$/^(<added test/xds tests>)\$" ./test/xds     # no env var set
eababd58 natural (no probe) x40: exit=0 pass=40 fail=0
2ffad480 natural (no probe) x40: exit=0 pass=40 fail=0
eb19a38b natural (no probe) x40: exit=0 pass=40 fail=0
dab66e4f natural (no probe) x40: exit=0 pass=40 fail=0
0ed1f32c natural (no probe) x40: exit=0 pass=40 fail=0
```

Control: the audited branch's own tests pass under both probes (it is not a C2 target; this shows the probes do not fail correctly synchronized tests):


```console
$ verify/repro/c2_sched_probe.sh ~/wt/perfect close 150ms 1
--- unperturbed
--- PASS: Test/HandleRDSUpdate_ErrorAfterSuccess (0.00s)
--- PASS: Test/HandleRDSUpdate_ErrorBeforeUpdate (0.00s)
--- PASS: Test/HandleRDSUpdate_Success (0.00s)
ok  	google.golang.org/grpc/internal/xds/server	1.039s
--- PASS: Test/ServerSideXDS_InterceptorLeak_RDSUpdate (0.04s)
ok  	google.golang.org/grpc/test/xds	1.093s
--- VERIFY_CLOSE_DELAY=150ms
--- PASS: Test/HandleRDSUpdate_ErrorAfterSuccess (0.15s)
--- PASS: Test/HandleRDSUpdate_ErrorBeforeUpdate (0.00s)
--- PASS: Test/HandleRDSUpdate_Success (0.00s)
ok  	google.golang.org/grpc/internal/xds/server	1.183s
--- PASS: Test/ServerSideXDS_InterceptorLeak_RDSUpdate (0.96s)
ok  	google.golang.org/grpc/test/xds	2.043s
$ verify/repro/c2_sched_probe.sh ~/wt/perfect construct 150ms 3 | grep -E '^--- (unp|VER)|^(ok|FAIL)'
--- unperturbed
ok  	google.golang.org/grpc/internal/xds/server	1.060s
ok  	google.golang.org/grpc/test/xds	1.171s
--- VERIFY_CONSTRUCT_DELAY=150ms
ok  	google.golang.org/grpc/internal/xds/server	2.868s
ok  	google.golang.org/grpc/test/xds	2.998s
```

Impact reasoning. Part 1: on the 12 branches whose test fails under the probe, the test treats "an RPC was served by the new route configuration" as proof that (a) the superseded interceptor has finished closing and/or (b) every filter chain of the listener has been updated. In the production code of those branches the new configuration is published first and the old interceptors are closed (and the next filter chain is updated) afterwards on the xDS callback goroutine, so the assertion races with that goroutine. The window is microseconds wide in an idle run (40/40 passes) and the probe shows the assertion has no synchronization with the transition it measures: anything that slows interceptor `Close` or route construction (a loaded CI machine, a real interceptor whose `Close` does I/O) turns the test red although the production behavior is correct. Part 2: on 2a01d623 and 311db1b4 a regression that suppresses the awaited event does not fail the test with a message; it hangs the whole package's test binary until the global `go test` timeout (10 minutes by default) and reports a goroutine dump instead of the assertion.

Per-branch evidence:

### C2 · [evalon/grpc-go-xd-eababd58](https://github.com/kaitranntt-evals/grpc-go-xds-rds-interceptor-lifecycle-leak/tree/evalon/grpc-go-xd-eababd58) (HEAD de70f1b0)

Source (`test/xds/xds_server_rds_update_test.go`, lines 177–198):

```go
177		WaitForUpdatedConfig:
178			for ; ctx.Err() == nil; <-time.After(defaultTestShortTimeout) {
179				if _, err := client.EmptyCall(ctx, &testpb.Empty{}); err != nil {
180					t.Fatalf("EmptyCall() failed after RDS update %d: %v", i, err)
181				}
182				select {
183				case got := <-pathCh:
184					if got == wantPath {
185						break WaitForUpdatedConfig
186					}
187				case <-ctx.Done():
188				}
189			}
190			if ctx.Err() != nil {
191				t.Fatalf("Timeout waiting for RDS update %d to be applied: %v", i, ctx.Err())
192			}
193	
194			// Only the interceptor for the currently served configuration must be
195			// alive.
196			if got := interceptorsCreated.Load() - interceptorsDestroyed.Load(); got != 1 {
197				t.Fatalf("After RDS update %d, %d interceptor instances are alive (created: %d, destroyed: %d), want 1", i, got, interceptorsCreated.Load(), interceptorsDestroyed.Load())
198			}
```

```console
$ verify/repro/c2_sched_probe.sh ~/wt/eababd58 close 150ms 1
added tests: ConstructUsableRouteConfiguration_FailureReleasesInterceptors|ListenerWrapper_RDSUpdates_ConcurrentRouting|ListenerWrapper_RDSUpdates_ReleaseSupersededInterceptors|ServerSideXDS_RDSUpdates_ReleaseSupersededInterceptors
--- unperturbed
--- PASS: Test/ConstructUsableRouteConfiguration_FailureReleasesInterceptors (0.00s)
--- PASS: Test/ListenerWrapper_RDSUpdates_ConcurrentRouting (0.00s)
--- PASS: Test/ListenerWrapper_RDSUpdates_ReleaseSupersededInterceptors (0.00s)
ok  	google.golang.org/grpc/internal/xds/server	1.037s
--- PASS: Test/ServerSideXDS_RDSUpdates_ReleaseSupersededInterceptors (0.25s)
ok  	google.golang.org/grpc/test/xds	1.294s
--- VERIFY_CLOSE_DELAY=150ms
--- PASS: Test/ConstructUsableRouteConfiguration_FailureReleasesInterceptors (0.45s)
--- PASS: Test/ListenerWrapper_RDSUpdates_ConcurrentRouting (15.24s)
--- PASS: Test/ListenerWrapper_RDSUpdates_ReleaseSupersededInterceptors (45.73s)
ok  	google.golang.org/grpc/internal/xds/server	62.461s
xds_server_rds_update_test.go:197: After RDS update 1, 2 interceptor instances are alive (created: 2, destroyed: 0), want 1
--- FAIL: Test/ServerSideXDS_RDSUpdates_ReleaseSupersededInterceptors (0.38s)
FAIL
FAIL	google.golang.org/grpc/test/xds	0.421s
FAIL
$ verify/repro/c2_absent_notification.sh ~/wt/eababd58 60s
522:	return nil // audit mutation: notification absent
xds_server_rds_update_test.go:191: Timeout waiting for RDS update 0 to be applied: context deadline exceeded
--- FAIL: Test/ServerSideXDS_RDSUpdates_ReleaseSupersededInterceptors (10.06s)
FAIL
FAIL	google.golang.org/grpc/test/xds	10.092s
FAIL
elapsed=12s (binary timeout 60s)
```

Part 1 observed: the only wait before the assertion is for an RPC to be served by the new configuration; the count asserted at the failing line is changed by the xDS callback goroutine after that point (`xds_server_rds_update_test.go:197: After RDS update 1, 2 interceptor instances are alive (created: 2, destroyed: 0), want 1`). Part 2 not observed (wait is bounded).

### C2 · [evalon/grpc-go-xd-dab66e4f](https://github.com/kaitranntt-evals/grpc-go-xds-rds-interceptor-lifecycle-leak/tree/evalon/grpc-go-xd-dab66e4f) (HEAD 3133831c)

Source (`test/xds/xds_server_filter_state_retention_test.go`, lines 807–841):

```go
807		// waitForLiveInterceptors waits for the number of interceptors that have
808		// been created but not yet closed to reach want.
809		waitForLiveInterceptors := func(want int32) {
810			t.Helper()
811			for ; ctx.Err() == nil; <-time.After(time.Millisecond) {
812				if interceptorsCreated.Load()-interceptorsDestroyed.Load() == want {
813					return
814				}
815			}
816			t.Fatalf("Live interceptors = %d (created: %d, destroyed: %d), want %d", interceptorsCreated.Load()-interceptorsDestroyed.Load(), interceptorsCreated.Load(), interceptorsDestroyed.Load(), want)
817		}
818	
819		waitForPath("path-0")
820		waitForLiveInterceptors(2)
821	
822		// Push a number of RDS updates for the route configuration currently in
823		// use, and verify that the interceptors for the superseded configuration
824		// are released each time while RPCs continue to succeed.
825		const numUpdates = 10
826		for i := 1; i <= numUpdates; i++ {
827			path := fmt.Sprintf("path-%d", i)
828			resources.Routes = append(slices.Clone(clientRoutes), serverRouteConfig(path))
829			if err := managementServer.Update(ctx, resources); err != nil {
830				t.Fatal(err)
831			}
832			waitForPath(path)
833			waitForLiveInterceptors(2)
834		}
835	
836		if got, want := filtersCreated.Load(), int32(1); got != want {
837			t.Fatalf("Created %d filter instances, want: %d", got, want)
838		}
839		if got, want := interceptorsCreated.Load(), int32(2*(numUpdates+1)); got != want {
840			t.Fatalf("Created %d interceptor instances, want: %d", got, want)
841		}
```

```console
$ verify/repro/c2_sched_probe.sh ~/wt/dab66e4f close 150ms 1
added tests: ServerSideXDS_FilterStateRetention_AcrossRDSUpdates|UsableRouteConfiguration_InterceptorsReleased
--- unperturbed
--- PASS: Test/UsableRouteConfiguration_InterceptorsReleased (0.00s)
ok  	google.golang.org/grpc/internal/xds/server	1.031s
--- PASS: Test/ServerSideXDS_FilterStateRetention_AcrossRDSUpdates (0.15s)
ok  	google.golang.org/grpc/test/xds	1.189s
--- VERIFY_CLOSE_DELAY=150ms
--- PASS: Test/UsableRouteConfiguration_InterceptorsReleased (1.81s)
ok  	google.golang.org/grpc/internal/xds/server	2.837s
--- PASS: Test/ServerSideXDS_FilterStateRetention_AcrossRDSUpdates (3.37s)
ok  	google.golang.org/grpc/test/xds	4.409s
$ verify/repro/c2_sched_probe.sh ~/wt/dab66e4f construct 150ms 3
added tests: ServerSideXDS_FilterStateRetention_AcrossRDSUpdates|UsableRouteConfiguration_InterceptorsReleased
--- unperturbed
--- PASS: Test/UsableRouteConfiguration_InterceptorsReleased (0.00s)
--- PASS: Test/UsableRouteConfiguration_InterceptorsReleased (0.00s)
--- PASS: Test/UsableRouteConfiguration_InterceptorsReleased (0.00s)
ok  	google.golang.org/grpc/internal/xds/server	1.037s
--- PASS: Test/ServerSideXDS_FilterStateRetention_AcrossRDSUpdates (0.16s)
--- PASS: Test/ServerSideXDS_FilterStateRetention_AcrossRDSUpdates (0.13s)
--- PASS: Test/ServerSideXDS_FilterStateRetention_AcrossRDSUpdates (0.14s)
ok  	google.golang.org/grpc/test/xds	1.482s
--- VERIFY_CONSTRUCT_DELAY=150ms
--- PASS: Test/UsableRouteConfiguration_InterceptorsReleased (0.91s)
--- PASS: Test/UsableRouteConfiguration_InterceptorsReleased (0.90s)
--- PASS: Test/UsableRouteConfiguration_InterceptorsReleased (0.91s)
ok  	google.golang.org/grpc/internal/xds/server	3.751s
xds_server_filter_state_retention_test.go:840: Created 21 interceptor instances, want: 22
--- FAIL: Test/ServerSideXDS_FilterStateRetention_AcrossRDSUpdates (3.33s)
xds_server_filter_state_retention_test.go:840: Created 21 interceptor instances, want: 22
--- FAIL: Test/ServerSideXDS_FilterStateRetention_AcrossRDSUpdates (3.33s)
xds_server_filter_state_retention_test.go:840: Created 21 interceptor instances, want: 22
--- FAIL: Test/ServerSideXDS_FilterStateRetention_AcrossRDSUpdates (3.33s)
FAIL
FAIL	google.golang.org/grpc/test/xds	10.041s
FAIL
$ verify/repro/c2_absent_notification.sh ~/wt/dab66e4f 60s
541:	return nil // audit mutation: notification absent
xds_server_filter_state_retention_test.go:819: Timeout waiting for interceptor to see path "path-0"
--- FAIL: Test/ServerSideXDS_FilterStateRetention_AcrossRDSUpdates (10.07s)
FAIL
FAIL	google.golang.org/grpc/test/xds	10.102s
FAIL
elapsed=12s (binary timeout 60s)
```

Part 1 observed: `waitForPath` + `waitForLiveInterceptors(2)` are satisfied as soon as the first of the two filter chains has been updated (created +1, destroyed +1, live = 2), so after the last update the loop exits between the two filter-chain updates and the total-created assertion at line 839 runs before the second filter chain builds its interceptor (21 instead of 22, 3 of 3 runs). Part 2 not observed (wait is bounded).

### C2 · [evalon/grpc-go-xd-415a74da](https://github.com/kaitranntt-evals/grpc-go-xds-rds-interceptor-lifecycle-leak/tree/evalon/grpc-go-xd-415a74da) (HEAD f70da5b8)

Source (`test/xds/xds_server_filter_state_retention_test.go`, lines 775–793; helper at 744–759):

```go
744		waitForPath := func(want string) {
745			t.Helper()
746			for ; ctx.Err() == nil; <-time.After(defaultTestShortTimeout) {
747				if _, err := client.EmptyCall(ctx, &testpb.Empty{}); err != nil {
748					t.Fatalf("EmptyCall() failed: %v", err)
749				}
750				select {
751				case got := <-pathCh:
752					if got == want {
753						return
754					}
755				case <-ctx.Done():
756				}
757			}
758			t.Fatalf("Timeout waiting for interceptor to be invoked with path %q", want)
759		}
...
775			path := fmt.Sprintf("path-%d", i)
776			resources.Routes = append(clientRoutes, serverRouteConfig(path))
777			if err := managementServer.Update(ctx, resources); err != nil {
778				t.Fatal(err)
779			}
780			waitForPath(path)
781	
782			if got, want := filtersCreated.Load(), int32(1); got != want {
783				t.Fatalf("After RDS update %d, created %d filter instances, want: %d", i, got, want)
784			}
785			if got, want := filtersDestroyed.Load(), int32(0); got != want {
786				t.Fatalf("After RDS update %d, destroyed %d filter instances, want: %d", i, got, want)
787			}
788			if got, want := interceptorsCreated.Load(), int32(i+1); got != want {
789				t.Fatalf("After RDS update %d, created %d interceptor instances, want: %d", i, got, want)
790			}
791			if got, want := interceptorsDestroyed.Load(), int32(i); got != want {
792				t.Fatalf("After RDS update %d, destroyed %d interceptor instances, want: %d", i, got, want)
793			}
```

```console
$ verify/repro/c2_sched_probe.sh ~/wt/415a74da close 150ms 1
added tests: FilterChain_RouteConfigUpdatesReleaseResources|ServerSideXDS_FilterStateRetention_AcrossRDSUpdates
--- unperturbed
--- PASS: Test/FilterChain_RouteConfigUpdatesReleaseResources (0.00s)
ok  	google.golang.org/grpc/internal/xds/server	1.026s
--- PASS: Test/ServerSideXDS_FilterStateRetention_AcrossRDSUpdates (0.10s)
ok  	google.golang.org/grpc/test/xds	1.147s
--- VERIFY_CLOSE_DELAY=150ms
--- PASS: Test/FilterChain_RouteConfigUpdatesReleaseResources (3.02s)
ok  	google.golang.org/grpc/internal/xds/server	4.047s
xds_server_filter_state_retention_test.go:792: After RDS update 1, destroyed 0 interceptor instances, want: 1
--- FAIL: Test/ServerSideXDS_FilterStateRetention_AcrossRDSUpdates (0.38s)
FAIL
FAIL	google.golang.org/grpc/test/xds	0.424s
FAIL
$ verify/repro/c2_absent_notification.sh ~/wt/415a74da 60s
537:	return nil // audit mutation: notification absent
xds_server_filter_state_retention_test.go:760: Timeout waiting for interceptor to be invoked with path "path-0"
--- FAIL: Test/ServerSideXDS_FilterStateRetention_AcrossRDSUpdates (10.08s)
FAIL
FAIL	google.golang.org/grpc/test/xds	10.112s
FAIL
elapsed=12s (binary timeout 60s)
```

Part 1 observed: the only wait before the assertion is for an RPC to be served by the new configuration; the count asserted at the failing line is changed by the xDS callback goroutine after that point (`xds_server_filter_state_retention_test.go:792: After RDS update 1, destroyed 0 interceptor instances, want: 1`). Part 2 not observed (wait is bounded).

### C2 · [evalon/grpc-go-xd-0ed1f32c](https://github.com/kaitranntt-evals/grpc-go-xds-rds-interceptor-lifecycle-leak/tree/evalon/grpc-go-xd-0ed1f32c) (HEAD 10d73679)

Source (`test/xds/xds_server_filter_state_retention_test.go`, lines 782–791; helper at 750–765):

```go
750		waitForPath := func(want string) {
751			t.Helper()
752			for ; ctx.Err() == nil; <-time.After(defaultTestShortTimeout) {
753				if _, err := client.EmptyCall(ctx, &testpb.Empty{}); err != nil {
754					t.Fatalf("EmptyCall() failed: %v", err)
755				}
756				select {
757				case got := <-pathCh:
758					if got == want {
759						return
760					}
761				case <-ctx.Done():
762				}
763			}
764			t.Fatalf("Timeout waiting for interceptor to be invoked with path %q", want)
765		}
...
782		for i := 1; i <= numUpdates; i++ {
783			path := fmt.Sprintf("path-%d", i)
784			resources.Routes = append(clientRoutes, serverRouteConfig(path))
785			if err := managementServer.Update(ctx, resources); err != nil {
786				t.Fatal(err)
787			}
788			waitForPath(path)
789			if got, want := liveInterceptors(), numFilterChains; got != want {
790				t.Fatalf("After %d RouteConfiguration updates, got %d live interceptor instances, want: %d", i, got, want)
791			}
```

```console
$ verify/repro/c2_sched_probe.sh ~/wt/0ed1f32c close 150ms 1
added tests: ServerSideXDS_FilterStateRetention_AcrossRDSUpdates
--- unperturbed
ok  	google.golang.org/grpc/internal/xds/server	1.024s [no tests to run]
--- PASS: Test/ServerSideXDS_FilterStateRetention_AcrossRDSUpdates (0.16s)
ok  	google.golang.org/grpc/test/xds	1.194s
--- VERIFY_CLOSE_DELAY=150ms
ok  	google.golang.org/grpc/internal/xds/server	1.025s [no tests to run]
xds_server_filter_state_retention_test.go:790: After 1 RouteConfiguration updates, got 3 live interceptor instances, want: 2
--- FAIL: Test/ServerSideXDS_FilterStateRetention_AcrossRDSUpdates (0.64s)
FAIL
FAIL	google.golang.org/grpc/test/xds	0.669s
FAIL
$ verify/repro/c2_absent_notification.sh ~/wt/0ed1f32c 60s
516:	return nil // audit mutation: notification absent
xds_server_filter_state_retention_test.go:773: Timeout waiting for interceptor to be invoked with path "path-0"
--- FAIL: Test/ServerSideXDS_FilterStateRetention_AcrossRDSUpdates (10.05s)
FAIL
FAIL	google.golang.org/grpc/test/xds	10.084s
FAIL
elapsed=12s (binary timeout 60s)
```

Part 1 observed: the only wait before the assertion is for an RPC to be served by the new configuration; the count asserted at the failing line is changed by the xDS callback goroutine after that point (`xds_server_filter_state_retention_test.go:790: After 1 RouteConfiguration updates, got 3 live interceptor instances, want: 2`). Part 2 not observed (wait is bounded).

### C2 · [evalon/grpc-go-xd-eb9c66bf](https://github.com/kaitranntt-evals/grpc-go-xds-rds-interceptor-lifecycle-leak/tree/evalon/grpc-go-xd-eb9c66bf) (HEAD 0a3344da)

Source (`test/xds/xds_server_route_config_update_test.go`, lines 203–212; helper at 171–197):

```go
171		waitForPath := func(want string) {
172			t.Helper()
173			for ; ctx.Err() == nil; <-time.After(defaultTestShortTimeout) {
174				if _, err := client.EmptyCall(ctx, &testpb.Empty{}); err != nil {
175					t.Fatalf("EmptyCall() failed: %v", err)
176				}
177				select {
178				case got := <-pathCh:
179					if got == want {
180						return
181					}
182				case <-ctx.Done():
183				}
184			}
185			t.Fatalf("Timeout waiting for route configuration with path %q to be applied", want)
186		}
187		waitForPath("path-0")
188	
189		// The server has a single filter chain with a single route, so exactly one
190		// interceptor should be live at any point in time.
191		verifyLiveInterceptors := func(want int32) {
192			t.Helper()
193			created, destroyed := interceptorsCreated.Load(), interceptorsDestroyed.Load()
194			if got := created - destroyed; got != want {
195				t.Fatalf("Live interceptors = %d (created: %d, destroyed: %d), want: %d", got, created, destroyed, want)
196			}
197		}
...
203		const numUpdates = 10
204		for i := 1; i <= numUpdates; i++ {
205			path := fmt.Sprintf("path-%d", i)
206			resources.Routes = append(clientRoutes, serverRouteConfig(path))
207			if err := managementServer.Update(ctx, resources); err != nil {
208				t.Fatal(err)
209			}
210			waitForPath(path)
211			verifyLiveInterceptors(1)
212		}
```

```console
$ verify/repro/c2_sched_probe.sh ~/wt/eb9c66bf close 150ms 1
added tests: ConstructUsableRouteConfiguration_ReleasesInterceptors|ServerSideXDS_RouteConfigurationUpdates_ReleaseSupersededInterceptors
--- unperturbed
--- PASS: Test/ConstructUsableRouteConfiguration_ReleasesInterceptors (0.00s)
ok  	google.golang.org/grpc/internal/xds/server	1.041s
--- PASS: Test/ServerSideXDS_RouteConfigurationUpdates_ReleaseSupersededInterceptors (0.22s)
ok  	google.golang.org/grpc/test/xds	1.288s
--- VERIFY_CLOSE_DELAY=150ms
--- PASS: Test/ConstructUsableRouteConfiguration_ReleasesInterceptors (0.75s)
ok  	google.golang.org/grpc/internal/xds/server	1.785s
xds_server_route_config_update_test.go:211: Live interceptors = 2 (created: 2, destroyed: 0), want: 1
--- FAIL: Test/ServerSideXDS_RouteConfigurationUpdates_ReleaseSupersededInterceptors (0.38s)
FAIL
FAIL	google.golang.org/grpc/test/xds	0.428s
FAIL
$ verify/repro/c2_absent_notification.sh ~/wt/eb9c66bf 60s
522:	return nil // audit mutation: notification absent
xds_server_route_config_update_test.go:187: Timeout waiting for route configuration with path "path-0" to be applied
--- FAIL: Test/ServerSideXDS_RouteConfigurationUpdates_ReleaseSupersededInterceptors (10.07s)
FAIL
FAIL	google.golang.org/grpc/test/xds	10.105s
FAIL
elapsed=12s (binary timeout 60s)
```

Part 1 observed: the only wait before the assertion is for an RPC to be served by the new configuration; the count asserted at the failing line is changed by the xDS callback goroutine after that point (`xds_server_route_config_update_test.go:211: Live interceptors = 2 (created: 2, destroyed: 0), want: 1`). Part 2 not observed (wait is bounded).

### C2 · [evalon/grpc-go-xd-e03d1e42](https://github.com/kaitranntt-evals/grpc-go-xds-rds-interceptor-lifecycle-leak/tree/evalon/grpc-go-xd-e03d1e42) (HEAD fc9cb150)

Source (`test/xds/xds_server_filter_state_retention_test.go`, lines 824–849):

```go
824			for ; ctx.Err() == nil; <-time.After(defaultTestShortTimeout) {
825				if _, err := client.EmptyCall(ctx, &testpb.Empty{}); err != nil {
826					t.Fatalf("EmptyCall() failed: %v", err)
827				}
828				select {
829				case cfg := <-pathCh:
830					if cfg == wantPath {
831						break WaitForUpdatedConfig
832					}
833				case <-ctx.Done():
834					t.Fatalf("Timeout waiting for interceptor get updated config")
835				}
836			}
837			if ctx.Err() != nil {
838				t.Fatalf("Timeout when waiting for updated config to be applied: %v", ctx.Err())
839			}
840	
841			if got, want := filtersCreated.Load(), int32(1); got != want {
842				t.Fatalf("After RouteConfiguration update %d: created %d filter instances, want: %d", i, got, want)
843			}
844			if got, want := filtersDestroyed.Load(), int32(0); got != want {
845				t.Fatalf("After RouteConfiguration update %d: destroyed %d filter instances, want: %d", i, got, want)
846			}
847			if got, want := interceptorsCreated.Load(), int32((i+1)*interceptorsPerUpdate); got != want {
848				t.Fatalf("After RouteConfiguration update %d: created %d interceptor instances, want: %d", i, got, want)
849			}
```

```console
$ verify/repro/c2_sched_probe.sh ~/wt/e03d1e42 close 150ms 1
added tests: FilterChainManager_StopReleasesActiveRouteConfiguration|FilterChain_SupersededRouteConfigurationIsReleased|ServerSideXDS_FilterStateRetention_AcrossUpdates_RouteConfigChange
--- unperturbed
--- PASS: Test/FilterChainManager_StopReleasesActiveRouteConfiguration (0.00s)
--- PASS: Test/FilterChain_SupersededRouteConfigurationIsReleased (0.00s)
ok  	google.golang.org/grpc/internal/xds/server	1.036s
--- PASS: Test/ServerSideXDS_FilterStateRetention_AcrossUpdates_RouteConfigChange (0.12s)
ok  	google.golang.org/grpc/test/xds	1.163s
--- VERIFY_CLOSE_DELAY=150ms
--- PASS: Test/FilterChainManager_StopReleasesActiveRouteConfiguration (0.15s)
--- PASS: Test/FilterChain_SupersededRouteConfigurationIsReleased (1.51s)
ok  	google.golang.org/grpc/internal/xds/server	2.686s
xds_server_filter_state_retention_test.go:848: After RouteConfiguration update 1: created 3 interceptor instances, want: 4
--- FAIL: Test/ServerSideXDS_FilterStateRetention_AcrossUpdates_RouteConfigChange (0.63s)
FAIL
FAIL	google.golang.org/grpc/test/xds	0.672s
FAIL
$ verify/repro/c2_absent_notification.sh ~/wt/e03d1e42 60s
542:	return nil // audit mutation: notification absent
xds_server_filter_state_retention_test.go:796: Timeout waiting for interceptor to be invoked
--- FAIL: Test/ServerSideXDS_FilterStateRetention_AcrossUpdates_RouteConfigChange (10.06s)
FAIL
FAIL	google.golang.org/grpc/test/xds	10.095s
FAIL
elapsed=12s (binary timeout 60s)
```

Part 1 observed: the only wait before the assertion is for an RPC to be served by the new configuration; the count asserted at the failing line is changed by the xDS callback goroutine after that point (`xds_server_filter_state_retention_test.go:848: After RouteConfiguration update 1: created 3 interceptor instances, want: 4`). Part 2 not observed (wait is bounded).

### C2 · [evalon/grpc-go-xd-98faa6aa](https://github.com/kaitranntt-evals/grpc-go-xds-rds-interceptor-lifecycle-leak/tree/evalon/grpc-go-xd-98faa6aa) (HEAD 303f98bc)

Source (`test/xds/xds_server_filter_state_retention_test.go`, lines 793–822):

```go
793		WaitForUpdatedConfig:
794			for ; ctx.Err() == nil; <-time.After(defaultTestShortTimeout) {
795				if _, err := client.EmptyCall(ctx, &testpb.Empty{}); err != nil {
796					t.Fatalf("EmptyCall() failed: %v", err)
797				}
798				select {
799				case cfg := <-pathCh:
800					if cfg == wantPath {
801						break WaitForUpdatedConfig
802					}
803				case <-ctx.Done():
804					t.Fatalf("Timeout waiting for interceptor get updated config")
805				}
806			}
807			if ctx.Err() != nil {
808				t.Fatalf("Timeout when waiting for updated config to be applied: %v", ctx.Err())
809			}
810	
811			if got, want := filtersCreated.Load(), int32(1); got != want {
812				t.Fatalf("After update %d: created %d filter instances, want: %d", i, got, want)
813			}
814			if got, want := filtersDestroyed.Load(), int32(0); got != want {
815				t.Fatalf("After update %d: destroyed %d filter instances, want: %d", i, got, want)
816			}
817			if got, want := interceptorsCreated.Load(), int32(i+1); got != want {
818				t.Fatalf("After update %d: created %d interceptor instances, want: %d", i, got, want)
819			}
820			if got, want := interceptorsDestroyed.Load(), int32(i); got != want {
821				t.Fatalf("After update %d: destroyed %d interceptor instances, want: %d", i, got, want)
822			}
```

```console
$ verify/repro/c2_sched_probe.sh ~/wt/98faa6aa close 150ms 1
added tests: FilterChainManagerStop_ReleasesResources|HandleRDSUpdate_ReleasesSupersededRouteConfiguration|ServerSideXDS_FilterStateRetention_AcrossUpdates_RouteConfigChange
--- unperturbed
--- PASS: Test/FilterChainManagerStop_ReleasesResources (0.00s)
--- PASS: Test/HandleRDSUpdate_ReleasesSupersededRouteConfiguration (0.00s)
ok  	google.golang.org/grpc/internal/xds/server	1.029s
--- PASS: Test/ServerSideXDS_FilterStateRetention_AcrossUpdates_RouteConfigChange (0.09s)
ok  	google.golang.org/grpc/test/xds	1.133s
--- VERIFY_CLOSE_DELAY=150ms
--- PASS: Test/FilterChainManagerStop_ReleasesResources (0.30s)
--- PASS: Test/HandleRDSUpdate_ReleasesSupersededRouteConfiguration (2.26s)
ok  	google.golang.org/grpc/internal/xds/server	3.593s
xds_server_filter_state_retention_test.go:821: After update 1: destroyed 0 interceptor instances, want: 1
--- FAIL: Test/ServerSideXDS_FilterStateRetention_AcrossUpdates_RouteConfigChange (0.33s)
FAIL
FAIL	google.golang.org/grpc/test/xds	0.375s
FAIL
$ verify/repro/c2_absent_notification.sh ~/wt/98faa6aa 60s
534:	return nil // audit mutation: notification absent
xds_server_filter_state_retention_test.go:769: Timeout waiting for interceptor to be invoked
--- FAIL: Test/ServerSideXDS_FilterStateRetention_AcrossUpdates_RouteConfigChange (10.06s)
FAIL
FAIL	google.golang.org/grpc/test/xds	10.096s
FAIL
elapsed=13s (binary timeout 60s)
```

Part 1 observed: the only wait before the assertion is for an RPC to be served by the new configuration; the count asserted at the failing line is changed by the xDS callback goroutine after that point (`xds_server_filter_state_retention_test.go:821: After update 1: destroyed 0 interceptor instances, want: 1`). Part 2 not observed (wait is bounded).

### C2 · [evalon/grpc-go-xd-2ffad480](https://github.com/kaitranntt-evals/grpc-go-xds-rds-interceptor-lifecycle-leak/tree/evalon/grpc-go-xd-2ffad480) (HEAD 784e2b0b)

Source (`test/xds/xds_server_filter_state_retention_test.go`, lines 819–841):

```go
819			// Wait for the updated config to be applied on the gRPC server. RPCs
820			// must keep succeeding while the update is being applied.
821		WaitForUpdatedConfig:
822			for ; ctx.Err() == nil; <-time.After(defaultTestShortTimeout) {
823				if _, err := client.EmptyCall(ctx, &testpb.Empty{}); err != nil {
824					t.Fatalf("EmptyCall() failed: %v", err)
825				}
826				select {
827				case cfg := <-pathCh:
828					if cfg == wantPath {
829						break WaitForUpdatedConfig
830					}
831				case <-ctx.Done():
832					t.Fatalf("Timeout waiting for interceptor get updated config")
833				}
834			}
835			if ctx.Err() != nil {
836				t.Fatalf("Timeout when waiting for updated config to be applied: %v", ctx.Err())
837			}
838	
839			if got, want := interceptorsCreated.Load(), int32(2*(i+1)); got != want {
840				t.Fatalf("Created %d interceptor instances after update %d, want: %d", got, i, want)
841			}
```

```console
$ verify/repro/c2_sched_probe.sh ~/wt/2ffad480 close 150ms 1
added tests: ListenerWrapper_RouteConfigUpdatesReleaseInterceptors|ServerSideXDS_FilterStateRetention_AcrossUpdates_RouteConfigChange
--- unperturbed
--- PASS: Test/ListenerWrapper_RouteConfigUpdatesReleaseInterceptors (0.12s)
ok  	google.golang.org/grpc/internal/xds/server	1.145s
--- PASS: Test/ServerSideXDS_FilterStateRetention_AcrossUpdates_RouteConfigChange (0.07s)
ok  	google.golang.org/grpc/test/xds	1.101s
--- VERIFY_CLOSE_DELAY=150ms
--- PASS: Test/ListenerWrapper_RouteConfigUpdatesReleaseInterceptors (0.94s)
ok  	google.golang.org/grpc/internal/xds/server	1.980s
xds_server_filter_state_retention_test.go:840: Created 3 interceptor instances after update 1, want: 4
--- FAIL: Test/ServerSideXDS_FilterStateRetention_AcrossUpdates_RouteConfigChange (0.69s)
FAIL
FAIL	google.golang.org/grpc/test/xds	0.730s
FAIL
$ verify/repro/c2_absent_notification.sh ~/wt/2ffad480 60s
545:	return nil // audit mutation: notification absent
xds_server_filter_state_retention_test.go:796: Timeout waiting for interceptor to be invoked
--- FAIL: Test/ServerSideXDS_FilterStateRetention_AcrossUpdates_RouteConfigChange (10.01s)
FAIL
FAIL	google.golang.org/grpc/test/xds	10.047s
FAIL
elapsed=12s (binary timeout 60s)
```

Part 1 observed: the only wait before the assertion is for an RPC to be served by the new configuration; the count asserted at the failing line is changed by the xDS callback goroutine after that point (`xds_server_filter_state_retention_test.go:840: Created 3 interceptor instances after update 1, want: 4`). Part 2 not observed (wait is bounded).

### C2 · [evalon/grpc-go-xd-74924778](https://github.com/kaitranntt-evals/grpc-go-xds-rds-interceptor-lifecycle-leak/tree/evalon/grpc-go-xd-74924778) (HEAD e4739699)

Source (`test/xds/xds_server_filter_state_retention_test.go`, lines 823–849):

```go
823		WaitForUpdatedConfig:
824			for ; ctx.Err() == nil; <-time.After(defaultTestShortTimeout) {
825				if _, err := client.EmptyCall(ctx, &testpb.Empty{}); err != nil {
826					t.Fatalf("EmptyCall() failed: %v", err)
827				}
828				select {
829				case cfg := <-pathCh:
830					if cfg == wantPath {
831						break WaitForUpdatedConfig
832					}
833				case <-ctx.Done():
834					t.Fatalf("Timeout waiting for interceptor get updated config")
835				}
836			}
837			if ctx.Err() != nil {
838				t.Fatalf("Timeout when waiting for updated config to be applied: %v", ctx.Err())
839			}
840	
841			if got, want := filtersCreated.Load(), int32(1); got != want {
842				t.Fatalf("After update %d: created %d filter instances, want: %d", i, got, want)
843			}
844			if got, want := filtersDestroyed.Load(), int32(0); got != want {
845				t.Fatalf("After update %d: destroyed %d filter instances, want: %d", i, got, want)
846			}
847			if got, want := interceptorsCreated.Load(), int32(numFilterChains*(i+1)); got != want {
848				t.Fatalf("After update %d: created %d interceptor instances, want: %d", i, got, want)
849			}
```

```console
$ verify/repro/c2_sched_probe.sh ~/wt/74924778 close 150ms 1
added tests: FilterChain_RouteConfigurationUpdateReleasesResources|ServerSideXDS_FilterStateRetention_AcrossUpdates_RouteConfigChange
--- unperturbed
--- PASS: Test/FilterChain_RouteConfigurationUpdateReleasesResources (0.00s)
ok  	google.golang.org/grpc/internal/xds/server	1.028s
--- PASS: Test/ServerSideXDS_FilterStateRetention_AcrossUpdates_RouteConfigChange (0.07s)
ok  	google.golang.org/grpc/test/xds	1.108s
--- VERIFY_CLOSE_DELAY=150ms
--- PASS: Test/FilterChain_RouteConfigurationUpdateReleasesResources (2.11s)
ok  	google.golang.org/grpc/internal/xds/server	3.150s
xds_server_filter_state_retention_test.go:848: After update 1: created 3 interceptor instances, want: 4
--- FAIL: Test/ServerSideXDS_FilterStateRetention_AcrossUpdates_RouteConfigChange (0.69s)
FAIL
FAIL	google.golang.org/grpc/test/xds	0.737s
FAIL
$ verify/repro/c2_absent_notification.sh ~/wt/74924778 60s
569:	return nil // audit mutation: notification absent
xds_server_filter_state_retention_test.go:799: Timeout waiting for interceptor to be invoked
--- FAIL: Test/ServerSideXDS_FilterStateRetention_AcrossUpdates_RouteConfigChange (10.06s)
FAIL
FAIL	google.golang.org/grpc/test/xds	10.093s
FAIL
elapsed=12s (binary timeout 60s)
```

Part 1 observed: the only wait before the assertion is for an RPC to be served by the new configuration; the count asserted at the failing line is changed by the xDS callback goroutine after that point (`xds_server_filter_state_retention_test.go:848: After update 1: created 3 interceptor instances, want: 4`). Part 2 not observed (wait is bounded).

### C2 · [evalon/grpc-go-xd-dad62956](https://github.com/kaitranntt-evals/grpc-go-xds-rds-interceptor-lifecycle-leak/tree/evalon/grpc-go-xd-dad62956) (HEAD 30fef0af)

Source (`test/xds/xds_server_filter_state_retention_test.go`, lines 800–828):

```go
800	
801			// Wait for the updated config to be applied on the gRPC server.
802		WaitForUpdatedConfig:
803			for ; ctx.Err() == nil; <-time.After(defaultTestShortTimeout) {
804				if _, err := client.EmptyCall(ctx, &testpb.Empty{}); err != nil {
805					t.Fatalf("EmptyCall() failed: %v", err)
806				}
807				select {
808				case cfg := <-pathCh:
809					if cfg == wantPath {
810						break WaitForUpdatedConfig
811					}
812				case <-ctx.Done():
813					t.Fatalf("Timeout waiting for interceptor get updated config")
814				}
815			}
816			if ctx.Err() != nil {
817				t.Fatalf("Timeout when waiting for updated config to be applied: %v", ctx.Err())
818			}
819	
820			if got, want := filtersCreated.Load(), int32(1); got != want {
821				t.Fatalf("Created %d filter instances after update %d, want: %d", got, i, want)
822			}
823			if got, want := interceptorsCreated.Load(), int32(i+1); got != want {
824				t.Fatalf("Created %d interceptor instances after update %d, want: %d", got, i, want)
825			}
826			if got, want := interceptorsDestroyed.Load(), int32(i); got != want {
827				t.Fatalf("Destroyed %d interceptor instances after update %d, want: %d", got, i, want)
828			}
```

```console
$ verify/repro/c2_sched_probe.sh ~/wt/dad62956 close 150ms 1
added tests: FilterChainManager_ReleasesSupersededRouteConfigResources|ListenerWrapper_RouteConfigUpdates_ReleaseSupersededResources|ServerSideXDS_FilterStateRetention_AcrossUpdates_RouteConfigChange
--- unperturbed
--- PASS: Test/FilterChainManager_ReleasesSupersededRouteConfigResources (0.00s)
--- PASS: Test/ListenerWrapper_RouteConfigUpdates_ReleaseSupersededResources (0.07s)
ok  	google.golang.org/grpc/internal/xds/server	1.099s
--- PASS: Test/ServerSideXDS_FilterStateRetention_AcrossUpdates_RouteConfigChange (0.08s)
ok  	google.golang.org/grpc/test/xds	1.119s
--- VERIFY_CLOSE_DELAY=150ms
--- PASS: Test/FilterChainManager_ReleasesSupersededRouteConfigResources (1.51s)
--- PASS: Test/ListenerWrapper_RouteConfigUpdates_ReleaseSupersededResources (1.09s)
ok  	google.golang.org/grpc/internal/xds/server	3.628s
xds_server_filter_state_retention_test.go:827: Destroyed 0 interceptor instances after update 1, want: 1
--- FAIL: Test/ServerSideXDS_FilterStateRetention_AcrossUpdates_RouteConfigChange (0.33s)
FAIL
FAIL	google.golang.org/grpc/test/xds	0.367s
FAIL
$ verify/repro/c2_absent_notification.sh ~/wt/dad62956 60s
539:	return nil // audit mutation: notification absent
xds_server_filter_state_retention_test.go:779: Timeout waiting for interceptor to be invoked
--- FAIL: Test/ServerSideXDS_FilterStateRetention_AcrossUpdates_RouteConfigChange (10.06s)
FAIL
FAIL	google.golang.org/grpc/test/xds	10.096s
FAIL
elapsed=12s (binary timeout 60s)
```

Part 1 observed: the only wait before the assertion is for an RPC to be served by the new configuration; the count asserted at the failing line is changed by the xDS callback goroutine after that point (`xds_server_filter_state_retention_test.go:827: Destroyed 0 interceptor instances after update 1, want: 1`). Part 2 not observed (wait is bounded).

### C2 · [evalon/grpc-go-xd-79540ac1](https://github.com/kaitranntt-evals/grpc-go-xds-rds-interceptor-lifecycle-leak/tree/evalon/grpc-go-xd-79540ac1) (HEAD a01cc52b)

Source (`test/xds/xds_server_filter_state_retention_test.go`, lines 799–812; helper at 627–643):

```go
627	func waitForInterceptorPath(ctx context.Context, t *testing.T, client testgrpc.TestServiceClient, pathCh chan string, wantPath string) {
628		t.Helper()
629	
630		for ; ctx.Err() == nil; <-time.After(defaultTestShortTimeout) {
631			if _, err := client.EmptyCall(ctx, &testpb.Empty{}); err != nil {
632				t.Fatalf("EmptyCall() failed: %v", err)
633			}
634			select {
635			case path := <-pathCh:
636				if path == wantPath {
637					return
638				}
639			case <-ctx.Done():
640			}
641		}
642		t.Fatalf("Timeout waiting for interceptor to report path %q: %v", wantPath, ctx.Err())
643	}
...
799			waitForInterceptorPath(ctx, t, client, pathCh, path)
800	
801			if got, want := filtersCreated.Load(), int32(1); got != want {
802				t.Fatalf("After route config update %d: created %d filter instances, want: %d", i, got, want)
803			}
804			if got, want := filtersDestroyed.Load(), int32(0); got != want {
805				t.Fatalf("After route config update %d: destroyed %d filter instances, want: %d", i, got, want)
806			}
807			if got, want := interceptorsCreated.Load(), int32(i+1); got != want {
808				t.Fatalf("After route config update %d: created %d interceptor instances, want: %d", i, got, want)
809			}
810			if got, want := interceptorsDestroyed.Load(), int32(i); got != want {
811				t.Fatalf("After route config update %d: destroyed %d interceptor instances, want: %d", i, got, want)
812			}
```

```console
$ verify/repro/c2_sched_probe.sh ~/wt/79540ac1 close 150ms 1
added tests: ListenerWrapper_Close_ReleasesFiltersAfterRouteConfigError|ListenerWrapper_RouteConfigUpdate_ReleasesSupersededResources|ServerSideXDS_FilterStateRetention_AcrossUpdates_RouteConfigChange
--- unperturbed
--- PASS: Test/ListenerWrapper_Close_ReleasesFiltersAfterRouteConfigError (0.00s)
--- PASS: Test/ListenerWrapper_RouteConfigUpdate_ReleasesSupersededResources (0.00s)
ok  	google.golang.org/grpc/internal/xds/server	1.032s
--- PASS: Test/ServerSideXDS_FilterStateRetention_AcrossUpdates_RouteConfigChange (0.10s)
ok  	google.golang.org/grpc/test/xds	1.139s
--- VERIFY_CLOSE_DELAY=150ms
--- PASS: Test/ListenerWrapper_Close_ReleasesFiltersAfterRouteConfigError (0.30s)
--- PASS: Test/ListenerWrapper_RouteConfigUpdate_ReleasesSupersededResources (2.72s)
ok  	google.golang.org/grpc/internal/xds/server	4.048s
xds_server_filter_state_retention_test.go:811: After route config update 1: destroyed 0 interceptor instances, want: 1
--- FAIL: Test/ServerSideXDS_FilterStateRetention_AcrossUpdates_RouteConfigChange (0.33s)
FAIL
FAIL	google.golang.org/grpc/test/xds	0.370s
FAIL
$ verify/repro/c2_absent_notification.sh ~/wt/79540ac1 60s
539:	return nil // audit mutation: notification absent
xds_server_filter_state_retention_test.go:776: Timeout waiting for interceptor to report path "path-0": context deadline exceeded
--- FAIL: Test/ServerSideXDS_FilterStateRetention_AcrossUpdates_RouteConfigChange (10.13s)
FAIL
FAIL	google.golang.org/grpc/test/xds	10.172s
FAIL
elapsed=12s (binary timeout 60s)
```

Part 1 observed: the only wait before the assertion is for an RPC to be served by the new configuration; the count asserted at the failing line is changed by the xDS callback goroutine after that point (`xds_server_filter_state_retention_test.go:811: After route config update 1: destroyed 0 interceptor instances, want: 1`). Part 2 not observed (wait is bounded).

### C2 · [evalon/grpc-go-xd-eb19a38b](https://github.com/kaitranntt-evals/grpc-go-xds-rds-interceptor-lifecycle-leak/tree/evalon/grpc-go-xd-eb19a38b) (HEAD 7c0ac8b4)

Source (`test/xds/xds_server_filter_state_retention_test.go`, lines 836–864):

```go
836		WaitForUpdatedConfig:
837			for ; ctx.Err() == nil; <-time.After(defaultTestShortTimeout) {
838				if _, err := client.EmptyCall(ctx, &testpb.Empty{}); err != nil {
839					t.Fatalf("EmptyCall() failed: %v", err)
840				}
841				select {
842				case cfg := <-pathCh:
843					if cfg == wantPath {
844						break WaitForUpdatedConfig
845					}
846				case <-ctx.Done():
847					t.Fatalf("Timeout waiting for interceptor get updated config")
848				}
849			}
850			if ctx.Err() != nil {
851				t.Fatalf("Timeout when waiting for updated config to be applied: %v", ctx.Err())
852			}
853	
854			// The filter instance must be retained across RouteConfiguration
855			// updates, while the interceptor instances get replaced.
856			if got, want := filtersCreated.Load(), int32(1); got != want {
857				t.Fatalf("After RouteConfiguration update %d: created %d filter instances, want: %d", i, got, want)
858			}
859			if got, want := filtersDestroyed.Load(), int32(0); got != want {
860				t.Fatalf("After RouteConfiguration update %d: destroyed %d filter instances, want: %d", i, got, want)
861			}
862			if got, want := interceptorsCreated.Load(), int32(2*(i+1)); got != want {
863				t.Fatalf("After RouteConfiguration update %d: created %d interceptor instances, want: %d", i, got, want)
864			}
```

```console
$ verify/repro/c2_sched_probe.sh ~/wt/eb19a38b close 150ms 1
added tests: FilterChain_UpdateUsableRouteConfiguration_ClosesSupersededInterceptors|ServerSideXDS_FilterStateRetention_AcrossUpdates_RouteConfigChange
--- unperturbed
--- PASS: Test/FilterChain_UpdateUsableRouteConfiguration_ClosesSupersededInterceptors (0.00s)
ok  	google.golang.org/grpc/internal/xds/server	1.033s
--- PASS: Test/ServerSideXDS_FilterStateRetention_AcrossUpdates_RouteConfigChange (0.07s)
ok  	google.golang.org/grpc/test/xds	1.110s
--- VERIFY_CLOSE_DELAY=150ms
--- PASS: Test/FilterChain_UpdateUsableRouteConfiguration_ClosesSupersededInterceptors (1.81s)
ok  	google.golang.org/grpc/internal/xds/server	2.840s
xds_server_filter_state_retention_test.go:863: After RouteConfiguration update 1: created 3 interceptor instances, want: 4
--- FAIL: Test/ServerSideXDS_FilterStateRetention_AcrossUpdates_RouteConfigChange (0.68s)
FAIL
FAIL	google.golang.org/grpc/test/xds	0.720s
FAIL
$ verify/repro/c2_absent_notification.sh ~/wt/eb19a38b 60s
540:	return nil // audit mutation: notification absent
xds_server_filter_state_retention_test.go:810: Timeout waiting for interceptor to be invoked
--- FAIL: Test/ServerSideXDS_FilterStateRetention_AcrossUpdates_RouteConfigChange (10.02s)
FAIL
FAIL	google.golang.org/grpc/test/xds	10.050s
FAIL
elapsed=12s (binary timeout 60s)
```

Part 1 observed: the only wait before the assertion is for an RPC to be served by the new configuration; the count asserted at the failing line is changed by the xDS callback goroutine after that point (`xds_server_filter_state_retention_test.go:863: After RouteConfiguration update 1: created 3 interceptor instances, want: 4`). Part 2 not observed (wait is bounded).

### C2 · [evalon/grpc-go-xd-2a01d623](https://github.com/kaitranntt-evals/grpc-go-xds-rds-interceptor-lifecycle-leak/tree/evalon/grpc-go-xd-2a01d623) (HEAD 0ccf0147)

Source (`test/xds/xds_server_filter_state_retention_test.go`, lines 241–249):

```go
241			// RPCs made while the update is being applied must also succeed.
242			for {
243				if _, err := client.EmptyCall(ctx, &testpb.Empty{}); err != nil {
244					t.Fatalf("EmptyCall() during update %d failed: %v", update, err)
245				}
246				if got := <-pathCh; got == path {
247					break
248				}
249			}
```

```console
$ verify/repro/c2_sched_probe.sh ~/wt/2a01d623 close 150ms 1
added tests: RouteConfigurationCleanup|RouteConfigurationCleanupAfterDraining|RouteConfigurationCleanupDuringRPC|ServerSideXDS_FilterStateRetention_AcrossRouteUpdates
--- unperturbed
--- PASS: Test/RouteConfigurationCleanup (0.00s)
--- PASS: Test/RouteConfigurationCleanupAfterDraining (0.00s)
--- PASS: Test/RouteConfigurationCleanupDuringRPC (0.00s)
ok  	google.golang.org/grpc/internal/xds/server	1.034s
--- PASS: Test/ServerSideXDS_FilterStateRetention_AcrossRouteUpdates (0.03s)
ok  	google.golang.org/grpc/test/xds	1.068s
--- VERIFY_CLOSE_DELAY=150ms
--- PASS: Test/RouteConfigurationCleanup (2.72s)
--- PASS: Test/RouteConfigurationCleanupAfterDraining (0.30s)
--- PASS: Test/RouteConfigurationCleanupDuringRPC (0.46s)
ok  	google.golang.org/grpc/internal/xds/server	4.514s
--- PASS: Test/ServerSideXDS_FilterStateRetention_AcrossRouteUpdates (3.09s)
ok  	google.golang.org/grpc/test/xds	4.128s
$ verify/repro/c2_sched_probe.sh ~/wt/2a01d623 construct 150ms 3
added tests: RouteConfigurationCleanup|RouteConfigurationCleanupAfterDraining|RouteConfigurationCleanupDuringRPC|ServerSideXDS_FilterStateRetention_AcrossRouteUpdates
--- unperturbed
--- PASS: Test/RouteConfigurationCleanup (0.00s)
--- PASS: Test/RouteConfigurationCleanupAfterDraining (0.00s)
--- PASS: Test/RouteConfigurationCleanupDuringRPC (0.00s)
--- PASS: Test/RouteConfigurationCleanup (0.00s)
--- PASS: Test/RouteConfigurationCleanupAfterDraining (0.00s)
--- PASS: Test/RouteConfigurationCleanupDuringRPC (0.00s)
--- PASS: Test/RouteConfigurationCleanup (0.01s)
--- PASS: Test/RouteConfigurationCleanupAfterDraining (0.00s)
--- PASS: Test/RouteConfigurationCleanupDuringRPC (0.00s)
ok  	google.golang.org/grpc/internal/xds/server	1.059s
--- PASS: Test/ServerSideXDS_FilterStateRetention_AcrossRouteUpdates (0.05s)
--- PASS: Test/ServerSideXDS_FilterStateRetention_AcrossRouteUpdates (0.03s)
--- PASS: Test/ServerSideXDS_FilterStateRetention_AcrossRouteUpdates (0.02s)
ok  	google.golang.org/grpc/test/xds	1.159s
--- VERIFY_CONSTRUCT_DELAY=150ms
--- PASS: Test/RouteConfigurationCleanup (3.03s)
--- PASS: Test/RouteConfigurationCleanupAfterDraining (0.30s)
--- PASS: Test/RouteConfigurationCleanupDuringRPC (0.45s)
--- PASS: Test/RouteConfigurationCleanup (3.02s)
--- PASS: Test/RouteConfigurationCleanupAfterDraining (0.30s)
--- PASS: Test/RouteConfigurationCleanupDuringRPC (0.46s)
--- PASS: Test/RouteConfigurationCleanup (3.02s)
--- PASS: Test/RouteConfigurationCleanupAfterDraining (0.30s)
--- PASS: Test/RouteConfigurationCleanupDuringRPC (0.45s)
ok  	google.golang.org/grpc/internal/xds/server	12.388s
--- PASS: Test/ServerSideXDS_FilterStateRetention_AcrossRouteUpdates (3.05s)
--- PASS: Test/ServerSideXDS_FilterStateRetention_AcrossRouteUpdates (3.04s)
--- PASS: Test/ServerSideXDS_FilterStateRetention_AcrossRouteUpdates (3.04s)
ok  	google.golang.org/grpc/test/xds	10.177s
$ verify/repro/c2_absent_notification.sh ~/wt/2a01d623 60s
551:	return nil // audit mutation: notification absent
panic: test timed out after 1m0s
running tests:
<wt>/2a01d623/test/xds/xds_client_integration_test.go:50 +0x35
<wt>/2a01d623/test/xds/xds_server_filter_state_retention_test.go:246 +0x2532
<wt>/2a01d623/test/xds/xds_server_integration_test.go:75 +0x82
FAIL	google.golang.org/grpc/test/xds	60.106s
FAIL
elapsed=61s (binary timeout 60s)
$ verify/repro/c2_unbounded_wait_2a01d623.sh ~/wt/2a01d623
551:	return nil // audit mutation: notification absent
panic: test timed out after 40s
	running tests:
		Test/ServerSideXDS_FilterStateRetention_AcrossRouteUpdates (40s)
goroutine 1 [chan receive]:
goroutine 36 [chan receive]:
goroutine 45 [chan receive]:
	<wt>/2a01d623/test/xds/xds_server_filter_state_retention_test.go:246 +0x2532
goroutine 48 [chan receive]:
goroutine 49 [chan receive]:
goroutine 66 [chan receive]:
goroutine 67 [chan receive]:
goroutine 68 [chan receive]:
goroutine 5 [chan receive]:
goroutine 6 [chan receive]:
goroutine 7 [chan receive]:
FAIL	google.golang.org/grpc/test/xds	40.129s
FAIL
elapsed=42s (test context deadline is 10s; binary timeout 40s)
```

Part 1 not reproduced (tests pass under both probes). Part 2 observed: with the notification absent the test's context expires after 10s but the bare receive `<-pathCh` at line 246 keeps the test blocked until the binary timeout (goroutine 45 `[chan receive]` at `xds_server_filter_state_retention_test.go:246`).

### C2 · [evalon/grpc-go-xd-311db1b4](https://github.com/kaitranntt-evals/grpc-go-xds-rds-interceptor-lifecycle-leak/tree/evalon/grpc-go-xd-311db1b4) (HEAD 5dc5968c)

Source (`internal/xds/server/filter_chain_lifecycle_test.go`, lines 174–194):

```go
174	func (s) TestFilterChainCleanupAfterConnectionClose(t *testing.T) {
175		l, builder := newLifecycleListener()
176		lis, err := net.Listen("tcp", "127.0.0.1:0")
177		if err != nil {
178			t.Fatal(err)
179		}
180		l.Listener = lis
181		defer l.Close()
182		l.mode = connectivity.ServingModeServing
183		l.conns = make(map[*connWrapper]bool)
184		l.handleRDSUpdate("route", rdsWatcherUpdate{data: lifecycleRouteConfig()})
185		client, err := net.Dial("tcp", lis.Addr().String())
186		if err != nil {
187			t.Fatal(err)
188		}
189		defer client.Close()
190		conn, err := l.Accept()
191		if err != nil {
192			t.Fatal(err)
193		}
194		defer conn.Close()
```

```console
$ verify/repro/c2_sched_probe.sh ~/wt/311db1b4 close 150ms 1
added tests: FilterChainCleanupAfterConnectionClose|RouteConfigurationCleanupDuringRPC|RouteConfigurationConstructionFailureCleanup|RouteConfigurationUpdateAfterClose|RouteConfigurationUpdateCleanupAndRecovery|ServerSideXDS_FilterStateRetention_AcrossUpdates_RouteConfigChange
--- unperturbed
--- PASS: Test/FilterChainCleanupAfterConnectionClose (0.00s)
--- PASS: Test/RouteConfigurationCleanupDuringRPC (0.00s)
--- PASS: Test/RouteConfigurationConstructionFailureCleanup (0.01s)
--- PASS: Test/RouteConfigurationUpdateAfterClose (0.00s)
--- PASS: Test/RouteConfigurationUpdateCleanupAndRecovery (0.00s)
ok  	google.golang.org/grpc/internal/xds/server	1.046s
--- PASS: Test/ServerSideXDS_FilterStateRetention_AcrossUpdates_RouteConfigChange (0.05s)
ok  	google.golang.org/grpc/test/xds	1.099s
--- VERIFY_CLOSE_DELAY=150ms
--- PASS: Test/FilterChainCleanupAfterConnectionClose (0.15s)
--- PASS: Test/RouteConfigurationCleanupDuringRPC (0.45s)
--- PASS: Test/RouteConfigurationConstructionFailureCleanup (0.91s)
--- PASS: Test/RouteConfigurationUpdateAfterClose (0.15s)
--- PASS: Test/RouteConfigurationUpdateCleanupAndRecovery (1.06s)
ok  	google.golang.org/grpc/internal/xds/server	3.760s
--- PASS: Test/ServerSideXDS_FilterStateRetention_AcrossUpdates_RouteConfigChange (3.35s)
ok  	google.golang.org/grpc/test/xds	4.397s
$ verify/repro/c2_sched_probe.sh ~/wt/311db1b4 construct 150ms 3
added tests: FilterChainCleanupAfterConnectionClose|RouteConfigurationCleanupDuringRPC|RouteConfigurationConstructionFailureCleanup|RouteConfigurationUpdateAfterClose|RouteConfigurationUpdateCleanupAndRecovery|ServerSideXDS_FilterStateRetention_AcrossUpdates_RouteConfigChange
--- unperturbed
--- PASS: Test/FilterChainCleanupAfterConnectionClose (0.00s)
--- PASS: Test/RouteConfigurationCleanupDuringRPC (0.00s)
--- PASS: Test/RouteConfigurationConstructionFailureCleanup (0.00s)
--- PASS: Test/RouteConfigurationUpdateAfterClose (0.00s)
--- PASS: Test/RouteConfigurationUpdateCleanupAndRecovery (0.00s)
--- PASS: Test/FilterChainCleanupAfterConnectionClose (0.00s)
--- PASS: Test/RouteConfigurationCleanupDuringRPC (0.00s)
--- PASS: Test/RouteConfigurationConstructionFailureCleanup (0.00s)
--- PASS: Test/RouteConfigurationUpdateAfterClose (0.00s)
--- PASS: Test/RouteConfigurationUpdateCleanupAndRecovery (0.00s)
--- PASS: Test/FilterChainCleanupAfterConnectionClose (0.00s)
--- PASS: Test/RouteConfigurationCleanupDuringRPC (0.00s)
--- PASS: Test/RouteConfigurationConstructionFailureCleanup (0.00s)
--- PASS: Test/RouteConfigurationUpdateAfterClose (0.00s)
--- PASS: Test/RouteConfigurationUpdateCleanupAndRecovery (0.00s)
ok  	google.golang.org/grpc/internal/xds/server	1.063s
--- PASS: Test/ServerSideXDS_FilterStateRetention_AcrossUpdates_RouteConfigChange (0.05s)
--- PASS: Test/ServerSideXDS_FilterStateRetention_AcrossUpdates_RouteConfigChange (0.04s)
--- PASS: Test/ServerSideXDS_FilterStateRetention_AcrossUpdates_RouteConfigChange (0.03s)
ok  	google.golang.org/grpc/test/xds	1.215s
--- VERIFY_CONSTRUCT_DELAY=150ms
--- PASS: Test/FilterChainCleanupAfterConnectionClose (0.15s)
--- PASS: Test/RouteConfigurationCleanupDuringRPC (0.45s)
--- PASS: Test/RouteConfigurationConstructionFailureCleanup (0.76s)
--- PASS: Test/RouteConfigurationUpdateAfterClose (0.15s)
--- PASS: Test/RouteConfigurationUpdateCleanupAndRecovery (1.21s)
--- PASS: Test/FilterChainCleanupAfterConnectionClose (0.15s)
--- PASS: Test/RouteConfigurationCleanupDuringRPC (0.45s)
--- PASS: Test/RouteConfigurationConstructionFailureCleanup (0.76s)
--- PASS: Test/RouteConfigurationUpdateAfterClose (0.15s)
--- PASS: Test/RouteConfigurationUpdateCleanupAndRecovery (1.21s)
--- PASS: Test/FilterChainCleanupAfterConnectionClose (0.15s)
--- PASS: Test/RouteConfigurationCleanupDuringRPC (0.46s)
--- PASS: Test/RouteConfigurationConstructionFailureCleanup (0.76s)
--- PASS: Test/RouteConfigurationUpdateAfterClose (0.15s)
--- PASS: Test/RouteConfigurationUpdateCleanupAndRecovery (1.21s)
ok  	google.golang.org/grpc/internal/xds/server	9.221s
--- PASS: Test/ServerSideXDS_FilterStateRetention_AcrossUpdates_RouteConfigChange (3.35s)
--- PASS: Test/ServerSideXDS_FilterStateRetention_AcrossUpdates_RouteConfigChange (3.34s)
--- PASS: Test/ServerSideXDS_FilterStateRetention_AcrossUpdates_RouteConfigChange (3.35s)
ok  	google.golang.org/grpc/test/xds	11.107s
$ verify/repro/c2_absent_notification.sh ~/wt/311db1b4 60s
563:	return nil // audit mutation: notification absent
xds_server_filter_state_retention_test.go:248: Timeout waiting for interceptor to be invoked
--- FAIL: Test/ServerSideXDS_FilterStateRetention_AcrossUpdates_RouteConfigChange (10.01s)
FAIL
FAIL	google.golang.org/grpc/test/xds	10.046s
FAIL
elapsed=12s (binary timeout 60s)
$ verify/repro/c2_unbounded_wait_311db1b4.sh ~/wt/311db1b4
304:		if true || l.mode == connectivity.ServingModeNotServing { // audit mutation: connection absent
panic: test timed out after 40s
	running tests:
		Test/FilterChainCleanupAfterConnectionClose (40s)
google.golang.org/grpc/internal/xds/server.(*listenerWrapper).Accept(0xc0003e3d40)
	<wt>/311db1b4/internal/xds/server/filter_chain_lifecycle_test.go:190 +0x773
FAIL	google.golang.org/grpc/internal/xds/server	40.036s
FAIL
elapsed=41s (binary timeout 40s)
```

Part 1 not reproduced (tests pass under both probes). Part 2 observed: `TestFilterChainCleanupAfterConnectionClose` calls `l.Accept()` directly on the test goroutine with no deadline, no context and no concurrent closer (`defer l.Close()` only runs after the test returns); when the wrapper does not deliver the dialed connection the test is blocked in `(*listenerWrapper).Accept` until the binary timeout.

## C3

Claim: the added or changed Go tests fail to compile in the solution's provided environment because of their syntax, imports, or API usage. Adjudicated on two branches.

Command, per branch (`verify/instrumentation/c3_compile_check.sh <checkout> <fixture>` prints each command it runs):

### C3 · [evalon/grpc-go-xd-bdc42e7c](https://github.com/kaitranntt-evals/grpc-go-xds-rds-interceptor-lifecycle-leak/tree/evalon/grpc-go-xd-bdc42e7c) (HEAD 21b002fc)

```console
$ verify/instrumentation/c3_compile_check.sh ~/wt/bdc42e7c ~/eval/tests/eval_xds_server_interceptor_leak_test.go
HEAD 21b002fc; go 1.25.0; go version go1.25.7 linux/amd64
added/changed test files: internal/xds/server/filter_chain_manager_test.go test/xds/xds_server_filter_state_retention_test.go
$ gofmt -l internal/xds/server/filter_chain_manager_test.go test/xds/xds_server_filter_state_retention_test.go
gofmt exit=0
$ go vet ./internal/xds/server/... ./test/xds/...
vet exit=0
$ go test -race -count=1 -run '^$' ./internal/xds/server/... ./test/xds/...   (compile only)
ok  	google.golang.org/grpc/internal/xds/server	1.027s [no tests to run]
ok  	google.golang.org/grpc/test/xds	1.027s [no tests to run]
compile exit=0
$ go test -race -count=1 -v -run '^Test$/^(FilterChain_RouteConfigurationUpdatesReleaseResources|ServerSideXDS_FilterStateRetention_AcrossRDSUpdates)$' ./internal/xds/server ./test/xds | grep -E '^\s+--- |^(ok|FAIL)'
    --- PASS: Test/FilterChain_RouteConfigurationUpdatesReleaseResources (0.00s)
ok  	google.golang.org/grpc/internal/xds/server	1.031s
    --- PASS: Test/ServerSideXDS_FilterStateRetention_AcrossRDSUpdates (0.16s)
ok  	google.golang.org/grpc/test/xds	1.202s
run exit=0
$ cp <fixture> test/xds/eval_xds_server_interceptor_leak_test.go && cmp && go test -race -count=1 -run '^$' ./test/xds
cmp: identical
ok  	google.golang.org/grpc/test/xds	1.024s [no tests to run]
compile-with-fixture exit=0
```

### C3 · [evalon/grpc-go-xd-cf01ba8b](https://github.com/kaitranntt-evals/grpc-go-xds-rds-interceptor-lifecycle-leak/tree/evalon/grpc-go-xd-cf01ba8b) (HEAD 0ce3e898)

```console
$ verify/instrumentation/c3_compile_check.sh ~/wt/cf01ba8b ~/eval/tests/eval_xds_server_interceptor_leak_test.go
HEAD 0ce3e898; go 1.25.0; go version go1.25.7 linux/amd64
added/changed test files: internal/xds/server/route_configuration_test.go test/xds/xds_server_filter_state_retention_test.go
$ gofmt -l internal/xds/server/route_configuration_test.go test/xds/xds_server_filter_state_retention_test.go
gofmt exit=0
$ go vet ./internal/xds/server/... ./test/xds/...
vet exit=0
$ go test -race -count=1 -run '^$' ./internal/xds/server/... ./test/xds/...   (compile only)
ok  	google.golang.org/grpc/internal/xds/server	1.022s [no tests to run]
ok  	google.golang.org/grpc/test/xds	1.025s [no tests to run]
compile exit=0
$ go test -race -count=1 -v -run '^Test$/^(RouteConfigurationConstructionFailureCleanup|RouteConfigurationResourceErrorCleanup|RouteConfigurationUpdateDuringRPC|ServerSideXDS_FilterStateRetention_RouteUpdates)$' ./internal/xds/server ./test/xds | grep -E '^\s+--- |^(ok|FAIL)'
    --- PASS: Test/RouteConfigurationConstructionFailureCleanup (0.00s)
        --- PASS: Test/RouteConfigurationConstructionFailureCleanup/first_route (0.00s)
        --- PASS: Test/RouteConfigurationConstructionFailureCleanup/later_route (0.00s)
        --- PASS: Test/RouteConfigurationConstructionFailureCleanup/later_virtual_host (0.00s)
        --- PASS: Test/RouteConfigurationConstructionFailureCleanup/nil_interceptor (0.00s)
    --- PASS: Test/RouteConfigurationResourceErrorCleanup (0.00s)
    --- PASS: Test/RouteConfigurationUpdateDuringRPC (0.00s)
ok  	google.golang.org/grpc/internal/xds/server	1.036s
    --- PASS: Test/ServerSideXDS_FilterStateRetention_RouteUpdates (0.07s)
ok  	google.golang.org/grpc/test/xds	1.104s
run exit=0
$ cp <fixture> test/xds/eval_xds_server_interceptor_leak_test.go && cmp && go test -race -count=1 -run '^$' ./test/xds
cmp: identical
ok  	google.golang.org/grpc/test/xds	1.024s [no tests to run]
compile-with-fixture exit=0
```

On both branches the added/changed test files are gofmt-clean, `go vet` exits 0, both affected packages compile under `go test -race` (`ok ... [no tests to run]` with `-run '^$'`), every added test runs and passes, and the package still compiles with the eval fixture provisioned byte-exact. No compilation error was observed on either branch (toolchain go1.25.7, `go.mod` `go 1.25.0`).

## C4

Claim: an RPC invokes an already-closed interceptor when an in-place RDS replacement retires the configuration selected by that RPC. Target: the audited branch [grpc-go-xds-rds-interceptor-lifecycle-leak-perfect](https://github.com/kaitranntt-evals/grpc-go/tree/grpc-go-xds-rds-interceptor-lifecycle-leak-perfect) (HEAD 614cb739).

Method. `verify/repro/c4_c10_closed_interceptor_invoked_test.go` runs a live `xds.NewGRPCServer` whose HTTP connection manager has two filters ahead of the router, `gate` and `probe`. One RPC is paused inside the `gate` interceptor's `AllowRPC`, i.e. after `RouteAndProcess` has selected the current `usableRouteConfiguration` and before the `probe` interceptor of that configuration is invoked. The RouteConfiguration is then replaced in place through RDS (same name, Listener untouched); the test waits until the replacement is applied, records whether the old `probe` interceptor's `Close()` has completed, resumes the RPC and records whether the old `probe` interceptor's `AllowRPC` runs after its `Close()` completed. No production code is modified.

Source of the two code paths involved (audited branch):

```go
// internal/xds/server/routing.go — RouteAndProcess loads the configuration once and keeps no claim on it
40		conn := transport.GetConnection(ctx)
41		cw, ok := conn.(*connWrapper)
42		if !ok {
43			return errors.New("missing virtual hosts in incoming context")
44		}
45	
46		rc := cw.urc.Load()
47		// Error out at routing l7 level with a status code UNAVAILABLE, represents
48		// an nack before usable route configuration or resource not found for RDS
49		// or error combining LDS + RDS (Shouldn't happen).
50		if rc.err != nil {
...
93			return rc.statusErrWithNodeID(codes.Unavailable, "the incoming RPC did not match a configured Route")
94		}
95		if err := rwi.interceptor.AllowRPC(ctx); err != nil {
96			return rc.statusErrWithNodeID(codes.PermissionDenied, "Incoming RPC is not allowed: %v", err)
97		}

// internal/xds/server/filter_chain_manager.go — the RDS path closes the previous configuration's interceptors unconditionally
449	func (fc *filterChain) applyConfiguration(urc *usableRouteConfiguration, serverFilters []httpfilter.ServerFilter) {
450		// Swap in the new configuration first so new RPCs use it immediately.
451		oldURC := fc.usableRouteConfiguration.Swap(urc)
452		oldFilters := fc.serverFilters
453		fc.serverFilters = serverFilters
454	
455		// Stop the old interceptors before releasing the filters they might depend on.
456		if oldURC != nil {
457			oldURC.stop()
458		}
459	
460		// Release references to old server filters.
461		for _, sf := range oldFilters {
462			sf.Close()
463		}
464	}
```

Run on the audited branch:

```console
$ cd ~/wt/perfect && cp ~/repos/grpc-go/verify/repro/c4_c10_closed_interceptor_invoked_test.go test/xds/verify_c4_c10_closed_interceptor_invoked_test.go && go test -race -count=1 -v -run '^Test$/^VerifyC4C10_' ./test/xds   # lines printed by the repro + result lines
verify_c4_c10_closed_interceptor_invoked_test.go:229: RPC paused inside the gate interceptor of the old configuration (probe interceptor #1 not yet invoked for it)
verify_c4_c10_closed_interceptor_invoked_test.go:119: probe interceptor #1: Close() completed
verify_c4_c10_closed_interceptor_invoked_test.go:244: replacement applied: 3 probe interceptors built in total
verify_c4_c10_closed_interceptor_invoked_test.go:250: OWNERSHIP: old probe interceptor #1 closed while the RPC that selected its configuration is still paused: true
verify_c4_c10_closed_interceptor_invoked_test.go:111: probe interceptor #1: AllowRPC invoked AFTER its Close() completed
verify_c4_c10_closed_interceptor_invoked_test.go:255: paused RPC finished with err=<nil>
verify_c4_c10_closed_interceptor_invoked_test.go:261: CONCURRENT INVOCATION: resumed RPC invoked old probe interceptor #1 1 time(s); 1 of them after its Close() completed
verify_c4_c10_closed_interceptor_invoked_test.go:263: selecting a configuration did not retain its interceptors: probe interceptor #1 was closed while an RPC using its configuration was in flight
verify_c4_c10_closed_interceptor_invoked_test.go:266: already-closed interceptor #1 was invoked by the in-flight RPC 1 time(s)
verify_c4_c10_closed_interceptor_invoked_test.go:119: probe interceptor #2: Close() completed
verify_c4_c10_closed_interceptor_invoked_test.go:119: probe interceptor #3: Close() completed
--- FAIL: Test (0.10s)
--- FAIL: Test/VerifyC4C10_InPlaceRDSReplacement_ClosesInterceptorOfInFlightRPC (0.10s)
FAIL
FAIL	google.golang.org/grpc/test/xds	0.130s
FAIL
exit=1
```

Same repro on the base commit `4ee6ac46` (control — before the change the superseded interceptor is never closed by an RDS update, which is the leak the task fixes, so the in-flight RPC invokes an open interceptor):

```console
$ cd ~/wt/base && cp ~/repos/grpc-go/verify/repro/c4_c10_closed_interceptor_invoked_test.go test/xds/verify_c4_c10_closed_interceptor_invoked_test.go && go test -race -count=1 -v -run '^Test$/^VerifyC4C10_' ./test/xds
verify_c4_c10_closed_interceptor_invoked_test.go:229: RPC paused inside the gate interceptor of the old configuration (probe interceptor #1 not yet invoked for it)
verify_c4_c10_closed_interceptor_invoked_test.go:244: replacement applied: 3 probe interceptors built in total
verify_c4_c10_closed_interceptor_invoked_test.go:250: OWNERSHIP: old probe interceptor #1 closed while the RPC that selected its configuration is still paused: false
verify_c4_c10_closed_interceptor_invoked_test.go:255: paused RPC finished with err=<nil>
verify_c4_c10_closed_interceptor_invoked_test.go:261: CONCURRENT INVOCATION: resumed RPC invoked old probe interceptor #1 1 time(s); 0 of them after its Close() completed
verify_c4_c10_closed_interceptor_invoked_test.go:119: probe interceptor #2: Close() completed
verify_c4_c10_closed_interceptor_invoked_test.go:119: probe interceptor #3: Close() completed
--- PASS: Test (5.09s)
--- PASS: Test/VerifyC4C10_InPlaceRDSReplacement_ClosesInterceptorOfInFlightRPC (5.09s)
ok  	google.golang.org/grpc/test/xds	6.120s
exit=0
```

The eval fixture does not exercise this interleaving; it stays green on the audited branch:

```console
$ cd ~/wt/perfect && cp ~/eval/tests/eval_xds_server_interceptor_leak_test.go test/xds/ && cmp ... && go test -race -count=1 -v -run '^Test$/^Eval_' ./test/xds | grep -E '^\s+--- (PASS|FAIL): Test/|^(ok|FAIL)'
cmp: identical
    --- PASS: Test/Eval_ServerSideXDS_InterceptorLeak_MultiGenerationRDSUpdate (0.06s)
    --- PASS: Test/Eval_ServerSideXDS_InterceptorLeak_RDSUpdate (0.04s)
    --- PASS: Test/Eval_ServerSideXDS_InterceptorSwapOrder (0.03s)
    --- PASS: Test/Eval_ServerSideXDS_PartialRouteFailure_ClosesInterceptors (0.03s)
    --- PASS: Test/Eval_ServerSideXDS_PartialVirtualHostFailure_ClosesInterceptors (0.03s)
ok  	google.golang.org/grpc/test/xds	1.225s
```

Part *Configuration ownership* — observed: `OWNERSHIP: old probe interceptor #1 closed while the RPC that selected its configuration is still paused: true`. Selecting the configuration in `RouteAndProcess` (`rc := cw.urc.Load()`) did not defer `applyConfiguration` → `oldURC.stop()`.

Part *Concurrent invocation* — observed: `probe interceptor #1: AllowRPC invoked AFTER its Close() completed`; `CONCURRENT INVOCATION: resumed RPC invoked old probe interceptor #1 1 time(s); 1 of them after its Close() completed`.

Impact reasoning. Any RPC that is between configuration selection and its last interceptor call when an RDS update for its route configuration lands — a window that is as long as the slowest earlier interceptor (e.g. an authz interceptor doing a remote check) — has the remaining interceptors closed underneath it and then invokes them. This breaks the documented contract of `iresolver.ServerInterceptor.Close` (`internal/resolver/config_selector.go`: "Close closes the interceptor. Once called, no new calls to NewStream are accepted. Ongoing calls to NewStream are allowed to complete."): the server itself makes a new call into an interceptor it has closed. What happens then is filter-specific (an interceptor that released state in `Close` decides on released state). The RPC in the repro returned `err=<nil>`: nothing surfaces the misuse. RDS updates on a serving listener are the everyday path the task is about, and the behavior is introduced by the change (the base commit never closes the superseded interceptor). There is no configuration workaround; the fix needs the selected configuration to be retained (reference count / in-flight tracking) until routing releases it, and closure deferred until then.

## C5

Claim: reenabling a filter through RDS after disabling it on every route reuses a closed server filter instance when LDS remains unchanged. Target: the audited branch [grpc-go-xds-rds-interceptor-lifecycle-leak-perfect](https://github.com/kaitranntt-evals/grpc-go/tree/grpc-go-xds-rds-interceptor-lifecycle-leak-perfect) (HEAD 614cb739).

Method. Two repros, no production code modified.

- `verify/repro/c5_c11_cache_lookup_test.go` (package `server`) calls `getOrCreateServerFilterWithMap` on an empty map, releases the returned reference (`Close()`), and calls the lookup again with the same key, printing the builder invocation count, the entry's `refCnt` and the filter's close count at each step.
- `verify/repro/c5_c11_closed_filter_reuse_test.go` (package `xds_test`) runs a live `xds.NewGRPCServer`; the Listener resource is sent once and never changed. The RouteConfiguration of the same name is updated three times through RDS: filter enabled → every use removed → enabled again. Two variants of the middle step: `EnableDisableEnable` disables the filter on every route with a virtual-host-level `FilterConfig{disabled: true}` override (`FilterConfig{disabled: true}` overrides are honored only when `GRPC_EXPERIMENTAL_XDS_EXT_PROC_ON_CLIENT=true` (`envconfig.XDSClientExtProcEnabled`, default false; the test sets it with `testutils.SetEnvConfig`)); `EnableEmptyEnable` uses default settings and sends a RouteConfiguration without virtual hosts. The test logs every `BuildServerFilter`, `BuildServerInterceptor` and `Close` of the filter instance.

Source (audited branch):

```go
409	func getOrCreateServerFilterWithMap(httpFilters map[serverFilterKey]*refCountedServerFilter, builder httpfilter.ServerFilterBuilder, key serverFilterKey) httpfilter.ServerFilter {
410		serverFilter, ok := httpFilters[key]
411		if ok {
412			serverFilter.incRef()
413			return serverFilter
414		}
415	
416		sf := builder.BuildServerFilter()
417		serverFilter = &refCountedServerFilter{ServerFilter: sf}
418		httpFilters[key] = serverFilter
419		serverFilter.incRef()
420		return serverFilter
421	}
...
578	func (rsf *refCountedServerFilter) incRef() {
579		rsf.refCnt.Add(1)
580	}
581	
582	func (rsf *refCountedServerFilter) Close() {
583		if rsf.refCnt.Add(-1) == 0 {
584			rsf.ServerFilter.Close()
585		}
586	}
```

Cache lookup:

```console
$ cd ~/wt/perfect && cp ~/repos/grpc-go/verify/repro/c5_c11_cache_lookup_test.go internal/xds/server/verify_c5_c11_cache_lookup_test.go && go test -race -count=1 -v -run '^Test$/^VerifyC5C11_' ./internal/xds/server
verify_c5_c11_cache_lookup_test.go:43: after 1st lookup: built=1 refCnt=1 closes=0
verify_c5_c11_cache_lookup_test.go:47: after releasing the last reference: refCnt=0 closes=1 stillCached=true
verify_c5_c11_cache_lookup_test.go:50: after 2nd lookup: built=1 sameEntry=true refCnt=1 closes(of returned filter)=1
verify_c5_c11_cache_lookup_test.go:52: lookup returned the cached entry whose filter was already closed (builder invoked 1 time(s), want 2)
--- FAIL: Test (0.00s)
--- FAIL: Test/VerifyC5C11_CacheLookupReturnsClosedZeroRefEntry (0.00s)
FAIL
FAIL	google.golang.org/grpc/internal/xds/server	0.028s
FAIL
exit=1
```

RDS enable → disable → enable under unchanged LDS (first block of events is `EnableDisableEnable`, second is `EnableEmptyEnable`):

```console
$ cd ~/wt/perfect && cp ~/repos/grpc-go/verify/repro/c5_c11_closed_filter_reuse_test.go test/xds/verify_c5_c11_closed_filter_reuse_test.go && go test -race -count=1 -v -run '^Test$/^VerifyC5C11_' ./test/xds
verify_c5_c11_closed_filter_reuse_test.go:205: STEP 1: RDS enables the filter
verify_c5_c11_closed_filter_reuse_test.go:71: EVENT filter F1 built
verify_c5_c11_closed_filter_reuse_test.go:96: EVENT filter F1: BuildServerInterceptor #1
verify_c5_c11_closed_filter_reuse_test.go:228: STEP 2: RDS update of the same name removes every use of the filter (LDS unchanged)
verify_c5_c11_closed_filter_reuse_test.go:102: EVENT filter F1: Close() completed (close #1)
verify_c5_c11_closed_filter_reuse_test.go:231: after step 2: F1 closes=1, filter instances built=1
verify_c5_c11_closed_filter_reuse_test.go:233: STEP 3: RDS update of the same name enables the filter again (LDS unchanged)
verify_c5_c11_closed_filter_reuse_test.go:94: EVENT filter F1: BuildServerInterceptor #2 called AFTER the filter's Close() completed
verify_c5_c11_closed_filter_reuse_test.go:251: RESULT: filter instances built in total=1; F1 closes=1; interceptors built on F1 after its Close()=1; RPCs allowed by interceptors of closed F1=1; RPC after re-enable err=<nil>
verify_c5_c11_closed_filter_reuse_test.go:254: re-enabling reused the ServerFilter instance closed during disabling: 1 interceptor(s) built on closed filter F1 and no new filter instance was built (instances=1)
verify_c5_c11_closed_filter_reuse_test.go:102: EVENT filter F1: Close() completed (close #2)
verify_c5_c11_closed_filter_reuse_test.go:205: STEP 1: RDS enables the filter
verify_c5_c11_closed_filter_reuse_test.go:71: EVENT filter F1 built
verify_c5_c11_closed_filter_reuse_test.go:96: EVENT filter F1: BuildServerInterceptor #1
verify_c5_c11_closed_filter_reuse_test.go:228: STEP 2: RDS update of the same name removes every use of the filter (LDS unchanged)
verify_c5_c11_closed_filter_reuse_test.go:102: EVENT filter F1: Close() completed (close #1)
verify_c5_c11_closed_filter_reuse_test.go:231: after step 2: F1 closes=1, filter instances built=1
verify_c5_c11_closed_filter_reuse_test.go:233: STEP 3: RDS update of the same name enables the filter again (LDS unchanged)
verify_c5_c11_closed_filter_reuse_test.go:94: EVENT filter F1: BuildServerInterceptor #2 called AFTER the filter's Close() completed
verify_c5_c11_closed_filter_reuse_test.go:251: RESULT: filter instances built in total=1; F1 closes=1; interceptors built on F1 after its Close()=1; RPCs allowed by interceptors of closed F1=1; RPC after re-enable err=<nil>
verify_c5_c11_closed_filter_reuse_test.go:254: re-enabling reused the ServerFilter instance closed during disabling: 1 interceptor(s) built on closed filter F1 and no new filter instance was built (instances=1)
verify_c5_c11_closed_filter_reuse_test.go:102: EVENT filter F1: Close() completed (close #2)
--- FAIL: Test (0.10s)
--- FAIL: Test/VerifyC5C11_EnableDisableEnable (0.06s)
--- FAIL: Test/VerifyC5C11_EnableEmptyEnable (0.04s)
FAIL
FAIL	google.golang.org/grpc/test/xds	0.126s
FAIL
exit=1
```

Same repros on the base commit `4ee6ac46` (the behavior predates the change; the audited branch leaves it in place):

```console
$ cd ~/wt/base && ... go test -race -count=1 -v -run '^Test$/^VerifyC5C11_' ./internal/xds/server
verify_c5_c11_cache_lookup_test.go:43: after 1st lookup: built=1 refCnt=1 closes=0
verify_c5_c11_cache_lookup_test.go:47: after releasing the last reference: refCnt=0 closes=1 stillCached=true
verify_c5_c11_cache_lookup_test.go:50: after 2nd lookup: built=1 sameEntry=true refCnt=1 closes(of returned filter)=1
verify_c5_c11_cache_lookup_test.go:52: lookup returned the cached entry whose filter was already closed (builder invoked 1 time(s), want 2)
--- FAIL: Test (0.00s)
--- FAIL: Test/VerifyC5C11_CacheLookupReturnsClosedZeroRefEntry (0.00s)
FAIL
FAIL	google.golang.org/grpc/internal/xds/server	0.027s
FAIL
exit=1
$ cd ~/wt/base && ... go test -race -count=1 -v -run '^Test$/^VerifyC5C11_' ./test/xds | grep -E 'RESULT|--- |^(ok|FAIL)'
verify_c5_c11_closed_filter_reuse_test.go:251: RESULT: filter instances built in total=1; F1 closes=1; interceptors built on F1 after its Close()=1; RPCs allowed by interceptors of closed F1=1; RPC after re-enable err=<nil>
verify_c5_c11_closed_filter_reuse_test.go:251: RESULT: filter instances built in total=1; F1 closes=1; interceptors built on F1 after its Close()=1; RPCs allowed by interceptors of closed F1=1; RPC after re-enable err=<nil>
--- FAIL: Test (0.10s)
--- FAIL: Test/VerifyC5C11_EnableDisableEnable (0.05s)
--- FAIL: Test/VerifyC5C11_EnableEmptyEnable (0.04s)
FAIL
FAIL	google.golang.org/grpc/test/xds	0.122s
FAIL
exit=1
```

Part *Cache lookup* — observed: `after releasing the last reference: refCnt=0 closes=1 stillCached=true`, then `after 2nd lookup: built=1 sameEntry=true refCnt=1 closes(of returned filter)=1`: the zero-reference, closed entry is returned without reconstruction (the builder ran once).

Part *RDS update sequence* — observed in both variants: `EVENT filter F1: Close() completed (close #1)` during the disabling update, then `EVENT filter F1: BuildServerInterceptor #2 called AFTER the filter's Close() completed` during the re-enabling update; `filter instances built in total=1`.

Impact reasoning. A control plane that removes every use of a filter from a RouteConfiguration and later restores it — with default settings: the RouteConfiguration temporarily has no virtual hosts; with the experimental flag: the filter is disabled on every route and re-enabled, e.g. a rollout/rollback — leaves the server building all new interceptors on a `ServerFilter` whose `Close()` has already completed, for as long as the Listener resource stays the same; RPCs are then admitted by those interceptors (`RPCs allowed by interceptors of closed F1=1`, `RPC after re-enable err=<nil>`). Nothing reports it: the filter is not rebuilt, no error is logged, and the instance is later closed a second time (`close #2`) when its count returns to zero again. What a closed filter does is filter-specific (resources released in `Close` are gone). No workaround was tested. The eval fixture (`fixture_perfect` above in Setup: 5 of 5 PASS) does not cover the sequence. Minimal fix: in `getOrCreateServerFilterWithMap`, do not hand out an entry whose count reached zero (remove the entry from the map when the count drops to zero, or rebuild the filter on lookup).

## C6

Claim: `Server.Stop` deadlocks instead of canceling an active RPC whose interceptor waits for RPC context cancellation while holding a routing read lock. Target: [evalon/grpc-go-xd-57bb4302](https://github.com/kaitranntt-evals/grpc-go-xds-rds-interceptor-lifecycle-leak/tree/evalon/grpc-go-xd-57bb4302) (HEAD 328f47dc).

Method. `verify/repro/c6_stop_deadlock_test.go` runs a live `xds.NewGRPCServer` with one HTTP filter whose interceptor's `AllowRPC` blocks until the RPC context is cancelled and whose `Close` is a no-op. While one RPC is inside that `AllowRPC` the test calls `Server.Stop()` on another goroutine and observes for 5s whether `Stop` returns and whether the RPC context is cancelled; it then dumps the goroutines involved and releases the interceptor through an escape channel so the binary can exit. No production code is modified.

Source on the target branch (lock held across `AllowRPC`; shutdown needs the write lock before transports are closed):

```console
$ cd ~/wt/57bb4302 && grep -n 'routingMu' internal/xds/server/*.go | grep -v _test
internal/xds/server/filter_chain_manager.go:169:	// routingMu prevents a route configuration from being closed while an RPC
internal/xds/server/filter_chain_manager.go:171:	routingMu                sync.RWMutex
internal/xds/server/filter_chain_manager.go:402:	fc.routingMu.Lock()
internal/xds/server/filter_chain_manager.go:403:	defer fc.routingMu.Unlock()
internal/xds/server/filter_chain_manager.go:408:	fc.routingMu.Lock()
internal/xds/server/filter_chain_manager.go:409:	defer fc.routingMu.Unlock()
internal/xds/server/routing.go:47:	fc.routingMu.RLock()
internal/xds/server/routing.go:48:	defer fc.routingMu.RUnlock()
```

```go
// internal/xds/server/routing.go (RouteAndProcess)
46		fc := cw.filterChain
47		fc.routingMu.RLock()
48		defer fc.routingMu.RUnlock()
49		rc := fc.usableRouteConfiguration
...
98		if err := rwi.interceptor.AllowRPC(ctx); err != nil {

// internal/xds/server/filter_chain_manager.go
401	func (fc *filterChain) stop() {
402		fc.routingMu.Lock()
403		defer fc.routingMu.Unlock()
404		fc.usableRouteConfiguration.close()
405	}

// internal/xds/server/listener_wrapper.go (Close, reached from grpc.(*Server).stop -> closeListenersLocked before server transports are closed)
352	func (l *listenerWrapper) Close() error {
353		l.closed.Fire()
354		l.Listener.Close()
355		if l.cancelWatch != nil {
356			l.cancelWatch()
357		}
358		l.rdsHandler.close()
359		l.mu.Lock()
360		if l.activeFilterChainManager != nil {
361			l.activeFilterChainManager.stop()
362		}
363		if l.pendingFilterChainManager != nil {
364			l.pendingFilterChainManager.stop()
365		}
366		l.mu.Unlock()
367		return nil
368	}
```

Run on the target branch:

```console
$ cd ~/wt/57bb4302 && cp ~/repos/grpc-go/verify/repro/c6_stop_deadlock_test.go test/xds/verify_c6_stop_deadlock_test.go && go test -race -count=1 -v -run '^Test$/^VerifyC6_' ./test/xds
verify_c6_stop_deadlock_test.go:188: RPC is inside AllowRPC, waiting for its context to be cancelled
verify_c6_stop_deadlock_test.go:216: RESULT after 5s bound: Stop returned=false, RPC context cancelled=false
verify_c6_stop_deadlock_test.go:218: goroutines in the wait cycle:
goroutine 45 [select]:
google.golang.org/grpc/test/xds_test.(*vc6Icpt).AllowRPC(0xc0001a22c0, {0x1f649f0, 0xc0004cef90})
google.golang.org/grpc/internal/xds/server.(*interceptorList).AllowRPC(0xc0001c5ce0, {0x1f649f0, 0xc0004cef90})
google.golang.org/grpc/internal/xds/server.RouteAndProcess({0x1f649f0, 0xc0004cef90})
google.golang.org/grpc/xds.xdsUnaryInterceptor({0x1f649f0, 0xc0004cef90}, {0x1b98a40, 0xc0004cefc0}, 0xc0004cef90?, 0xc0001e4300)
google.golang.org/grpc/interop/grpc_testing._TestService_EmptyCall_Handler({0x1cc55a0, 0xc0000f8000}, {0x1f649f0, 0xc0004cef90}, 0xc0004cd900, 0x1dc15d8)
google.golang.org/grpc.(*Server).processUnaryRPC(0xc0004bf208, {0x1f649f0, 0xc0004cef00}, 0xc0005bc4e0, 0xc000352ba0, 0x2b02ca0, 0x0)
google.golang.org/grpc.(*Server).handleStream(0xc0004bf208, {0x1f699d8, 0xc000578b60}, 0xc0005bc4e0)
google.golang.org/grpc.(*Server).serveStreams.func2.1()
created by google.golang.org/grpc.(*Server).serveStreams.func2 in goroutine 31

goroutine 46 [sync.RWMutex.Lock]:
sync.runtime_SemacquireRWMutex(0xc0004ba28c?, 0x1?, 0x1499cc5?)
sync.(*RWMutex).Lock(0xc0004ba278)
google.golang.org/grpc/internal/xds/server.(*filterChain).stop(0xc0004ba240)
google.golang.org/grpc/internal/xds/server.(*filterChainManager).stop(...)
google.golang.org/grpc/internal/xds/server.(*listenerWrapper).Close(0xc000472000)
google.golang.org/grpc.(*listenSocket).Close(0xc000012270)
google.golang.org/grpc.(*Server).closeListenersLocked(...)
google.golang.org/grpc.(*Server).stop(0xc0004bf208, 0x0)
google.golang.org/grpc.(*Server).Stop(0xc0004bf208)
google.golang.org/grpc/xds.(*GRPCServer).Stop(0xc0004c8680)
google.golang.org/grpc/test/xds_test.setupGRPCServer.func4()
google.golang.org/grpc/test/xds_test.s.TestVerifyC6_StopWithCancellationWaitingInterceptor.func3()
created by google.golang.org/grpc/test/xds_test.s.TestVerifyC6_StopWithCancellationWaitingInterceptor in goroutine 9
verify_c6_stop_deadlock_test.go:219: Server.Stop() deadlocked: after 5s Stop returned=false and the RPC context was cancelled=false
verify_c6_stop_deadlock_test.go:225: after releasing the interceptor through the test escape hatch, Server.Stop() returned (5.028s after it was called)
verify_c6_stop_deadlock_test.go:233: client RPC finished with: rpc error: code = PermissionDenied desc = [xDS node id: 01922168-1c6a-44b0-8781-ad573df60801]: Incoming RPC is not allowed: released by test escape hatch
--- FAIL: Test (5.06s)
--- FAIL: Test/VerifyC6_StopWithCancellationWaitingInterceptor (5.06s)
FAIL
FAIL	google.golang.org/grpc/test/xds	5.088s
FAIL
exit=1
```

Same repro on the audited branch (control):

```console
$ cd ~/wt/perfect && cp ~/repos/grpc-go/verify/repro/c6_stop_deadlock_test.go test/xds/verify_c6_stop_deadlock_test.go && go test -race -count=1 -v -run '^Test$/^VerifyC6_' ./test/xds
verify_c6_stop_deadlock_test.go:188: RPC is inside AllowRPC, waiting for its context to be cancelled
verify_c6_stop_deadlock_test.go:211: RPC context was cancelled 1ms after Stop() was called
verify_c6_stop_deadlock_test.go:208: Server.Stop() returned after 1ms
verify_c6_stop_deadlock_test.go:216: RESULT after 5s bound: Stop returned=true, RPC context cancelled=true
verify_c6_stop_deadlock_test.go:233: client RPC finished with: rpc error: code = Unavailable desc = error reading from server: EOF
--- PASS: Test (0.03s)
--- PASS: Test/VerifyC6_StopWithCancellationWaitingInterceptor (0.03s)
ok  	google.golang.org/grpc/test/xds	1.059s
exit=0
```

The eval fixture passes on the target branch (it has no cancellation-waiting interceptor):

```console
$ cd ~/wt/57bb4302 && cp ~/eval/tests/eval_xds_server_interceptor_leak_test.go test/xds/ && cmp ... && go test -race -count=1 -v -run '^Test$/^Eval_' ./test/xds | grep -E '^\s+--- (PASS|FAIL): Test/|^(ok|FAIL)'
cmp: identical
    --- PASS: Test/Eval_ServerSideXDS_InterceptorLeak_MultiGenerationRDSUpdate (0.06s)
    --- PASS: Test/Eval_ServerSideXDS_InterceptorLeak_RDSUpdate (0.03s)
    --- PASS: Test/Eval_ServerSideXDS_InterceptorSwapOrder (0.03s)
    --- PASS: Test/Eval_ServerSideXDS_PartialRouteFailure_ClosesInterceptors (0.03s)
    --- PASS: Test/Eval_ServerSideXDS_PartialVirtualHostFailure_ClosesInterceptors (0.03s)
ok  	google.golang.org/grpc/test/xds	1.210s
```

Part *Shutdown lock dependency* — observed: goroutine 45 is in `(*vc6Icpt).AllowRPC` ← `(*interceptorList).AllowRPC` ← `RouteAndProcess` (which holds `fc.routingMu.RLock()` via `defer ... RUnlock()`), and goroutine 46 is in `sync.(*RWMutex).Lock` ← `(*filterChain).stop` ← `(*filterChainManager).stop` ← `(*listenerWrapper).Close` ← `(*listenSocket).Close` ← `(*Server).closeListenersLocked` ← `(*Server).stop` ← `(*Server).Stop`: `Stop` is still closing listeners and has not reached the point where it closes server transports (which is what cancels RPC contexts).

Part *Cancellation-dependent RPC* — observed: `RESULT after 5s bound: Stop returned=false, RPC context cancelled=false`; `Stop` returned only after the test released the interceptor by hand (`Server.Stop() returned (5.028s after it was called)`). On the audited branch the same repro shows `RPC context was cancelled 1ms after Stop() was called` and `Server.Stop() returned after 1ms`.

Impact reasoning. An interceptor that blocks until its RPC context ends is legitimate (`AllowRPC(ctx)` is given the RPC context precisely so that it can wait on it — e.g. an authz interceptor blocked on a remote call with that context). On this branch one such in-flight RPC makes `Server.Stop()` hang forever: the RPC waits for cancellation that only `Stop` can deliver, and `Stop` waits for the read lock the RPC holds. Process shutdown then needs an external kill. From source only (not exercised): `updateRouteConfiguration` takes the same lock for writing (line 408), so an RDS update would wait behind a slow `AllowRPC` in the same way. No workaround at the application level. Fix direction: do not hold `routingMu` across `AllowRPC` (retain the selected configuration by reference instead), as the audited branch does.

## C7

Claim: listener replacement or `GracefulStop` rejects an already-admitted router-only RPC with `Unavailable` by retiring its routing state before connection draining finishes. Target: [evalon/grpc-go-xd-37fb43a6](https://github.com/kaitranntt-evals/grpc-go-xds-rds-interceptor-lifecycle-leak/tree/evalon/grpc-go-xd-37fb43a6) (HEAD 2b0c4d7c).

Method. `verify/repro/c7_draining_rpc_unavailable_test.go` runs a live `xds.NewGRPCServer` whose HTTP connection manager contains only the router filter. A `stats.Handler` pauses one RPC at `stats.Begin`, i.e. after the server admitted the stream and before the xDS server interceptor calls `RouteAndProcess`. Then either `GracefulStop()` is called (variant 1; the test waits until the listening socket refuses connections, so the listener wrapper has been closed and `GracefulStop` is waiting for the stream) or the Listener resource is replaced (variant 2; the test waits until a new client connection has been dialed, so the old connection is draining). The RPC is then resumed and its status recorded. No production code is modified.

Source on the target branch:

```go
413	func (fc *filterChain) stop() {
414		fc.updateRouteConfiguration(&usableRouteConfiguration{err: errors.New("filter chain stopped")})
415	}
```

Run on the target branch:

```console
$ cd ~/wt/37fb43a6 && cp ~/repos/grpc-go/verify/repro/c7_draining_rpc_unavailable_test.go test/xds/verify_c7_draining_rpc_unavailable_test.go && go test -race -count=1 -v -run '^Test$/^VerifyC7_' ./test/xds
verify_c7_draining_rpc_unavailable_test.go:160: baseline RPC succeeded (connections dialed so far: 1)
verify_c7_draining_rpc_unavailable_test.go:171: RPC admitted by the server and paused before RouteAndProcess
verify_c7_draining_rpc_unavailable_test.go:189: listening socket is closed (dial tcp 127.0.0.1:40655: connect: connection refused); GracefulStop is now waiting for the admitted RPC
verify_c7_draining_rpc_unavailable_test.go:229: RESULT [GracefulStop]: admitted RPC resumed during draining finished with code=Unavailable desc="error from xDS configuration for matched route configuration: filter chain stopped"
verify_c7_draining_rpc_unavailable_test.go:231: [GracefulStop] already-admitted router-only RPC was rejected during connection draining: rpc error: code = Unavailable desc = error from xDS configuration for matched route configuration: filter chain stopped
verify_c7_draining_rpc_unavailable_test.go:160: baseline RPC succeeded (connections dialed so far: 1)
verify_c7_draining_rpc_unavailable_test.go:171: RPC admitted by the server and paused before RouteAndProcess
verify_c7_draining_rpc_unavailable_test.go:218: listener replacement applied: client connections dialed=2 (old connection is draining with the paused RPC on it)
verify_c7_draining_rpc_unavailable_test.go:229: RESULT [ListenerReplacement]: admitted RPC resumed during draining finished with code=Unavailable desc="error from xDS configuration for matched route configuration: filter chain stopped"
verify_c7_draining_rpc_unavailable_test.go:231: [ListenerReplacement] already-admitted router-only RPC was rejected during connection draining: rpc error: code = Unavailable desc = error from xDS configuration for matched route configuration: filter chain stopped
--- FAIL: Test (0.70s)
--- FAIL: Test/VerifyC7_GracefulStop (0.35s)
--- FAIL: Test/VerifyC7_ListenerReplacement (0.35s)
FAIL
FAIL	google.golang.org/grpc/test/xds	0.729s
FAIL
exit=1
```

Same repro on the audited branch (control):

```console
$ cd ~/wt/perfect && cp ~/repos/grpc-go/verify/repro/c7_draining_rpc_unavailable_test.go test/xds/verify_c7_draining_rpc_unavailable_test.go && go test -race -count=1 -v -run '^Test$/^VerifyC7_' ./test/xds
verify_c7_draining_rpc_unavailable_test.go:160: baseline RPC succeeded (connections dialed so far: 1)
verify_c7_draining_rpc_unavailable_test.go:171: RPC admitted by the server and paused before RouteAndProcess
verify_c7_draining_rpc_unavailable_test.go:189: listening socket is closed (dial tcp 127.0.0.1:45697: i/o timeout); GracefulStop is now waiting for the admitted RPC
verify_c7_draining_rpc_unavailable_test.go:229: RESULT [GracefulStop]: admitted RPC resumed during draining finished with code=OK desc=""
verify_c7_draining_rpc_unavailable_test.go:160: baseline RPC succeeded (connections dialed so far: 1)
verify_c7_draining_rpc_unavailable_test.go:171: RPC admitted by the server and paused before RouteAndProcess
verify_c7_draining_rpc_unavailable_test.go:218: listener replacement applied: client connections dialed=2 (old connection is draining with the paused RPC on it)
verify_c7_draining_rpc_unavailable_test.go:229: RESULT [ListenerReplacement]: admitted RPC resumed during draining finished with code=OK desc=""
--- PASS: Test (0.94s)
--- PASS: Test/VerifyC7_GracefulStop (0.53s)
--- PASS: Test/VerifyC7_ListenerReplacement (0.40s)
ok  	google.golang.org/grpc/test/xds	1.985s
exit=0
```

The eval fixture passes on the target branch (it has no RPC paused across a drain):

```console
$ cd ~/wt/37fb43a6 && cp ~/eval/tests/eval_xds_server_interceptor_leak_test.go test/xds/ && cmp ... && go test -race -count=1 -v -run '^Test$/^Eval_' ./test/xds | grep -E '^\s+--- (PASS|FAIL): Test/|^(ok|FAIL)'
cmp: identical
    --- PASS: Test/Eval_ServerSideXDS_InterceptorLeak_MultiGenerationRDSUpdate (0.05s)
    --- PASS: Test/Eval_ServerSideXDS_InterceptorLeak_RDSUpdate (0.03s)
    --- PASS: Test/Eval_ServerSideXDS_InterceptorSwapOrder (0.08s)
    --- PASS: Test/Eval_ServerSideXDS_PartialRouteFailure_ClosesInterceptors (0.03s)
    --- PASS: Test/Eval_ServerSideXDS_PartialVirtualHostFailure_ClosesInterceptors (0.03s)
ok  	google.golang.org/grpc/test/xds	1.245s
```

Part *Routing-state retirement* — observed: the error returned to the client is the one `filterChain.stop` publishes (`errors.New("filter chain stopped")`, line 414), delivered while the admitted RPC was still on a draining connection, in both variants.

Part *Admitted-request routing* — observed in both variants: `RESULT [GracefulStop]: admitted RPC resumed during draining finished with code=Unavailable desc="error from xDS configuration for matched route configuration: filter chain stopped"` and the identical line for `[ListenerReplacement]`. On the audited branch both variants finish with `code=OK`.

Impact reasoning. `GracefulStop` and Listener updates are the two operations whose contract is "let in-flight RPCs finish". On this branch an RPC that the server has already admitted but that has not yet reached `RouteAndProcess` when the filter chain is stopped — a window every stream passes through between admission and routing — is failed with `Unavailable` instead of being served, even with a router-only filter chain that has no interceptors to retire. Any RPC in that window is failed instead of served; client retries are the only workaround. Fix direction: on stop, close interceptors/release filters only after the chain's connections have drained, and keep the published configuration usable until then (the audited branch does not replace the configuration with an error on stop).

## C8

Claim: replacing routes with a configuration that disables every use of a filter destroys that filter before the retired interceptors that depend on it finish closing. Target: [evalon/grpc-go-xd-b7bc0d7f](https://github.com/kaitranntt-evals/grpc-go-xds-rds-interceptor-lifecycle-leak/tree/evalon/grpc-go-xd-b7bc0d7f) (HEAD 6ee04f40).

Method. Same trace as C1: `verify/repro/c1_filter_ref_order_test.go` on a live `xds.NewGRPCServer` with one filter chain and one traced HTTP filter; `ref-acquire`/`ref-release` come from the hook that `verify/instrumentation/apply_trace.sh` adds to `refCountedServerFilter.incRef`/`Close` (count shown is the value before the operation), `icpt-build`/`icpt-close` and `filter-build`/`filter-close` from the test's filter. Phase `rds-replace-filter-kept` is an in-place RDS replacement that still uses the filter on its two routes; phase `rds-replace-filter-disabled-on-every-route` is an in-place RDS replacement whose virtual host carries a `FilterConfig{disabled: true}` override for the filter, which applies to every route; `FilterConfig{disabled: true}` overrides are honored only when `GRPC_EXPERIMENTAL_XDS_EXT_PROC_ON_CLIENT=true` (`envconfig.XDSClientExtProcEnabled`, default false; the test sets it with `testutils.SetEnvConfig`). Phases `noflag-rds-replace-filter-kept` and `noflag-rds-replace-no-virtual-hosts` (test `VerifyC1_ReplaceKeep_Then_NoVirtualHosts`) repeat the sequence with default settings, the last replacement being a RouteConfiguration with no virtual hosts. All replacements succeed (nothing below is cleanup of a failed construction).

```console
$ verify/repro/c1_c8_trace.sh ~/wt/b7bc0d7f
PHASE lds-replace: SUMMARY first ref-release=#6 first filter-close=#-1 last retired icpt-close=#5 retired-interceptors-never-closed=0 RELEASE_BEFORE_CLOSE=false FILTER_CLOSED_BEFORE_INTERCEPTORS=false
PHASE server-stop: SUMMARY first ref-release=#8 first filter-close=#9 last retired icpt-close=#7 retired-interceptors-never-closed=0 RELEASE_BEFORE_CLOSE=false FILTER_CLOSED_BEFORE_INTERCEPTORS=false
PHASE initial: 0 interceptor(s) open at phase start
PHASE initial: #00 filter-build  F1                       via getOrCreateServerFilterWithMap < (*listenerWrapper).getOrCreateServerFilterLocked < (*filterChain).newInterceptor < (*filterChain).convertVirtualHost < (*filterChain).constructUsableRouteConfiguration < (*listenerWrapper).instantiateFilterChainRoutingConfigurationsLocked < (*listenerWrapper).maybeUpdateFilterChains < (*listenerWrapper).handleRDSUpdate < (*rdsWatcher).ResourceChanged
PHASE initial: #01 ref-acquire   F1 (refCnt before=0)     via getOrCreateServerFilterWithMap < (*listenerWrapper).getOrCreateServerFilterLocked < (*filterChain).newInterceptor < (*filterChain).convertVirtualHost < (*filterChain).constructUsableRouteConfiguration < (*listenerWrapper).instantiateFilterChainRoutingConfigurationsLocked < (*listenerWrapper).maybeUpdateFilterChains < (*listenerWrapper).handleRDSUpdate < (*rdsWatcher).ResourceChanged
PHASE initial: #02 icpt-build    F1/I1                    via (*filterChain).newInterceptor < (*filterChain).convertVirtualHost < (*filterChain).constructUsableRouteConfiguration < (*listenerWrapper).instantiateFilterChainRoutingConfigurationsLocked < (*listenerWrapper).maybeUpdateFilterChains < (*listenerWrapper).handleRDSUpdate < (*rdsWatcher).ResourceChanged
PHASE initial: SUMMARY first ref-release=#-1 first filter-close=#-1 last retired icpt-close=#-1 retired-interceptors-never-closed=0 RELEASE_BEFORE_CLOSE=false FILTER_CLOSED_BEFORE_INTERCEPTORS=false
PHASE rds-replace-filter-kept: 1 interceptor(s) open at phase start
PHASE rds-replace-filter-kept: #03 ref-acquire   F1 (refCnt before=1)     via getOrCreateServerFilterWithMap < (*listenerWrapper).getOrCreateServerFilterLocked < (*filterChain).newInterceptor < (*filterChain).convertVirtualHost < (*filterChain).constructUsableRouteConfiguration < (*listenerWrapper).handleRDSUpdate < (*rdsWatcher).ResourceChanged
PHASE rds-replace-filter-kept: #04 icpt-build    F1/I2                    via (*filterChain).newInterceptor < (*filterChain).convertVirtualHost < (*filterChain).constructUsableRouteConfiguration < (*listenerWrapper).handleRDSUpdate < (*rdsWatcher).ResourceChanged
PHASE rds-replace-filter-kept: #05 ref-acquire   F1 (refCnt before=2)     via getOrCreateServerFilterWithMap < (*listenerWrapper).getOrCreateServerFilterLocked < (*filterChain).newInterceptor < (*filterChain).convertVirtualHost < (*filterChain).constructUsableRouteConfiguration < (*listenerWrapper).handleRDSUpdate < (*rdsWatcher).ResourceChanged
PHASE rds-replace-filter-kept: #06 icpt-build    F1/I3                    via (*filterChain).newInterceptor < (*filterChain).convertVirtualHost < (*filterChain).constructUsableRouteConfiguration < (*listenerWrapper).handleRDSUpdate < (*rdsWatcher).ResourceChanged
PHASE rds-replace-filter-kept: #07 ref-release   F1 (refCnt before=3)     via (*filterChain).constructUsableRouteConfiguration < (*listenerWrapper).handleRDSUpdate < (*rdsWatcher).ResourceChanged
PHASE rds-replace-filter-kept: #08 icpt-close    F1/I1                    via (*virtualHostWithInterceptors).closeInterceptors < (*usableRouteConfiguration).closeInterceptors < (*filterChain).setUsableRouteConfiguration < (*listenerWrapper).handleRDSUpdate < (*rdsWatcher).ResourceChanged
PHASE rds-replace-filter-kept: SUMMARY first ref-release=#7 first filter-close=#-1 last retired icpt-close=#8 retired-interceptors-never-closed=0 RELEASE_BEFORE_CLOSE=true FILTER_CLOSED_BEFORE_INTERCEPTORS=false
PHASE rds-replace-filter-disabled-on-every-route: 2 interceptor(s) open at phase start
PHASE rds-replace-filter-disabled-on-every-route: #09 ref-release   F1 (refCnt before=2)     via (*filterChain).constructUsableRouteConfiguration < (*listenerWrapper).handleRDSUpdate < (*rdsWatcher).ResourceChanged
PHASE rds-replace-filter-disabled-on-every-route: #10 ref-release   F1 (refCnt before=1)     via (*filterChain).constructUsableRouteConfiguration < (*listenerWrapper).handleRDSUpdate < (*rdsWatcher).ResourceChanged
PHASE rds-replace-filter-disabled-on-every-route: #11 filter-close  F1                       via (*filterChain).constructUsableRouteConfiguration < (*listenerWrapper).handleRDSUpdate < (*rdsWatcher).ResourceChanged
PHASE rds-replace-filter-disabled-on-every-route: #12 icpt-close    F1/I2                    via (*virtualHostWithInterceptors).closeInterceptors < (*usableRouteConfiguration).closeInterceptors < (*filterChain).setUsableRouteConfiguration < (*listenerWrapper).handleRDSUpdate < (*rdsWatcher).ResourceChanged
PHASE rds-replace-filter-disabled-on-every-route: #13 icpt-close    F1/I3                    via (*virtualHostWithInterceptors).closeInterceptors < (*usableRouteConfiguration).closeInterceptors < (*filterChain).setUsableRouteConfiguration < (*listenerWrapper).handleRDSUpdate < (*rdsWatcher).ResourceChanged
PHASE rds-replace-filter-disabled-on-every-route: SUMMARY first ref-release=#9 first filter-close=#11 last retired icpt-close=#13 retired-interceptors-never-closed=0 RELEASE_BEFORE_CLOSE=true FILTER_CLOSED_BEFORE_INTERCEPTORS=true
--- PASS: Test (4.17s)
--- PASS: Test/VerifyC1_LDSReplace_Then_Stop (2.09s)
--- PASS: Test/VerifyC1_ReplaceKeep_Then_DisableAll (2.08s)
ok  	google.golang.org/grpc/test/xds	5.194s
```

Same trace on the audited branch (control):

```console
$ verify/repro/c1_c8_trace.sh ~/wt/perfect
PHASE lds-replace: SUMMARY first ref-release=#6 first filter-close=#-1 last retired icpt-close=#5 retired-interceptors-never-closed=0 RELEASE_BEFORE_CLOSE=false FILTER_CLOSED_BEFORE_INTERCEPTORS=false
PHASE server-stop: SUMMARY first ref-release=#8 first filter-close=#9 last retired icpt-close=#7 retired-interceptors-never-closed=0 RELEASE_BEFORE_CLOSE=false FILTER_CLOSED_BEFORE_INTERCEPTORS=false
PHASE initial: SUMMARY first ref-release=#-1 first filter-close=#-1 last retired icpt-close=#-1 retired-interceptors-never-closed=0 RELEASE_BEFORE_CLOSE=false FILTER_CLOSED_BEFORE_INTERCEPTORS=false
PHASE rds-replace-filter-kept: 1 interceptor(s) open at phase start
PHASE rds-replace-filter-kept: #03 ref-acquire   F1 (refCnt before=1)     via getOrCreateServerFilterWithMap < (*listenerWrapper).getOrCreateServerFilterLocked < (*filterChain).newInterceptor < (*filterChain).convertVirtualHost < (*filterChain).updateUsableRouteConfiguration < (*listenerWrapper).handleRDSUpdate < (*rdsWatcher).ResourceChanged
PHASE rds-replace-filter-kept: #04 icpt-build    F1/I2                    via (*filterChain).newInterceptor < (*filterChain).convertVirtualHost < (*filterChain).updateUsableRouteConfiguration < (*listenerWrapper).handleRDSUpdate < (*rdsWatcher).ResourceChanged
PHASE rds-replace-filter-kept: #05 ref-acquire   F1 (refCnt before=2)     via getOrCreateServerFilterWithMap < (*listenerWrapper).getOrCreateServerFilterLocked < (*filterChain).newInterceptor < (*filterChain).convertVirtualHost < (*filterChain).updateUsableRouteConfiguration < (*listenerWrapper).handleRDSUpdate < (*rdsWatcher).ResourceChanged
PHASE rds-replace-filter-kept: #06 icpt-build    F1/I3                    via (*filterChain).newInterceptor < (*filterChain).convertVirtualHost < (*filterChain).updateUsableRouteConfiguration < (*listenerWrapper).handleRDSUpdate < (*rdsWatcher).ResourceChanged
PHASE rds-replace-filter-kept: #07 icpt-close    F1/I1                    via (*usableRouteConfiguration).stop < (*filterChain).applyConfiguration < (*filterChain).updateUsableRouteConfiguration < (*listenerWrapper).handleRDSUpdate < (*rdsWatcher).ResourceChanged
PHASE rds-replace-filter-kept: #08 ref-release   F1 (refCnt before=3)     via (*filterChain).applyConfiguration < (*filterChain).updateUsableRouteConfiguration < (*listenerWrapper).handleRDSUpdate < (*rdsWatcher).ResourceChanged
PHASE rds-replace-filter-kept: SUMMARY first ref-release=#8 first filter-close=#-1 last retired icpt-close=#7 retired-interceptors-never-closed=0 RELEASE_BEFORE_CLOSE=false FILTER_CLOSED_BEFORE_INTERCEPTORS=false
PHASE rds-replace-filter-disabled-on-every-route: 2 interceptor(s) open at phase start
PHASE rds-replace-filter-disabled-on-every-route: #09 icpt-close    F1/I2                    via (*usableRouteConfiguration).stop < (*filterChain).applyConfiguration < (*filterChain).updateUsableRouteConfiguration < (*listenerWrapper).handleRDSUpdate < (*rdsWatcher).ResourceChanged
PHASE rds-replace-filter-disabled-on-every-route: #10 icpt-close    F1/I3                    via (*usableRouteConfiguration).stop < (*filterChain).applyConfiguration < (*filterChain).updateUsableRouteConfiguration < (*listenerWrapper).handleRDSUpdate < (*rdsWatcher).ResourceChanged
PHASE rds-replace-filter-disabled-on-every-route: #11 ref-release   F1 (refCnt before=2)     via (*filterChain).applyConfiguration < (*filterChain).updateUsableRouteConfiguration < (*listenerWrapper).handleRDSUpdate < (*rdsWatcher).ResourceChanged
PHASE rds-replace-filter-disabled-on-every-route: #12 ref-release   F1 (refCnt before=1)     via (*filterChain).applyConfiguration < (*filterChain).updateUsableRouteConfiguration < (*listenerWrapper).handleRDSUpdate < (*rdsWatcher).ResourceChanged
PHASE rds-replace-filter-disabled-on-every-route: #13 filter-close  F1                       via (*filterChain).applyConfiguration < (*filterChain).updateUsableRouteConfiguration < (*listenerWrapper).handleRDSUpdate < (*rdsWatcher).ResourceChanged
PHASE rds-replace-filter-disabled-on-every-route: SUMMARY first ref-release=#11 first filter-close=#13 last retired icpt-close=#10 retired-interceptors-never-closed=0 RELEASE_BEFORE_CLOSE=false FILTER_CLOSED_BEFORE_INTERCEPTORS=false
--- PASS: Test (4.18s)
--- PASS: Test/VerifyC1_LDSReplace_Then_Stop (2.08s)
--- PASS: Test/VerifyC1_ReplaceKeep_Then_DisableAll (2.10s)
ok  	google.golang.org/grpc/test/xds	5.204s
```

Default settings (no experimental flag), target branch and audited branch (control):

```console
$ verify/repro/c1_c8_trace.sh ~/wt/b7bc0d7f | grep -E 'noflag-rds|--- |^(ok|FAIL)'
PHASE noflag-rds-replace-filter-kept: #07 ref-release   F1 (refCnt before=3)     via (*filterChain).constructUsableRouteConfiguration < (*listenerWrapper).handleRDSUpdate < (*rdsWatcher).ResourceChanged
PHASE noflag-rds-replace-filter-kept: #08 icpt-close    F1/I1                    via (*virtualHostWithInterceptors).closeInterceptors < (*usableRouteConfiguration).closeInterceptors < (*filterChain).setUsableRouteConfiguration < (*listenerWrapper).handleRDSUpdate < (*rdsWatcher).ResourceChanged
PHASE noflag-rds-replace-filter-kept: SUMMARY first ref-release=#7 first filter-close=#-1 last retired icpt-close=#8 retired-interceptors-never-closed=0 RELEASE_BEFORE_CLOSE=true FILTER_CLOSED_BEFORE_INTERCEPTORS=false
PHASE noflag-rds-replace-no-virtual-hosts: #09 ref-release   F1 (refCnt before=2)     via (*filterChain).constructUsableRouteConfiguration < (*listenerWrapper).handleRDSUpdate < (*rdsWatcher).ResourceChanged
PHASE noflag-rds-replace-no-virtual-hosts: #10 ref-release   F1 (refCnt before=1)     via (*filterChain).constructUsableRouteConfiguration < (*listenerWrapper).handleRDSUpdate < (*rdsWatcher).ResourceChanged
PHASE noflag-rds-replace-no-virtual-hosts: #11 filter-close  F1                       via (*filterChain).constructUsableRouteConfiguration < (*listenerWrapper).handleRDSUpdate < (*rdsWatcher).ResourceChanged
PHASE noflag-rds-replace-no-virtual-hosts: #12 icpt-close    F1/I2                    via (*virtualHostWithInterceptors).closeInterceptors < (*usableRouteConfiguration).closeInterceptors < (*filterChain).setUsableRouteConfiguration < (*listenerWrapper).handleRDSUpdate < (*rdsWatcher).ResourceChanged
PHASE noflag-rds-replace-no-virtual-hosts: #13 icpt-close    F1/I3                    via (*virtualHostWithInterceptors).closeInterceptors < (*usableRouteConfiguration).closeInterceptors < (*filterChain).setUsableRouteConfiguration < (*listenerWrapper).handleRDSUpdate < (*rdsWatcher).ResourceChanged
PHASE noflag-rds-replace-no-virtual-hosts: SUMMARY first ref-release=#9 first filter-close=#11 last retired icpt-close=#13 retired-interceptors-never-closed=0 RELEASE_BEFORE_CLOSE=true FILTER_CLOSED_BEFORE_INTERCEPTORS=true
--- PASS: Test (6.23s)
--- PASS: Test/VerifyC1_LDSReplace_Then_Stop (2.06s)
--- PASS: Test/VerifyC1_ReplaceKeep_Then_DisableAll (2.07s)
--- PASS: Test/VerifyC1_ReplaceKeep_Then_NoVirtualHosts (2.09s)
ok  	google.golang.org/grpc/test/xds	7.256s
$ verify/repro/c1_c8_trace.sh ~/wt/perfect | grep -E 'noflag-rds|--- |^(ok|FAIL)'
PHASE noflag-rds-replace-filter-kept: #07 icpt-close    F1/I1                    via (*usableRouteConfiguration).stop < (*filterChain).applyConfiguration < (*filterChain).updateUsableRouteConfiguration < (*listenerWrapper).handleRDSUpdate < (*rdsWatcher).ResourceChanged
PHASE noflag-rds-replace-filter-kept: #08 ref-release   F1 (refCnt before=3)     via (*filterChain).applyConfiguration < (*filterChain).updateUsableRouteConfiguration < (*listenerWrapper).handleRDSUpdate < (*rdsWatcher).ResourceChanged
PHASE noflag-rds-replace-filter-kept: SUMMARY first ref-release=#8 first filter-close=#-1 last retired icpt-close=#7 retired-interceptors-never-closed=0 RELEASE_BEFORE_CLOSE=false FILTER_CLOSED_BEFORE_INTERCEPTORS=false
PHASE noflag-rds-replace-no-virtual-hosts: #09 icpt-close    F1/I2                    via (*usableRouteConfiguration).stop < (*filterChain).applyConfiguration < (*filterChain).updateUsableRouteConfiguration < (*listenerWrapper).handleRDSUpdate < (*rdsWatcher).ResourceChanged
PHASE noflag-rds-replace-no-virtual-hosts: #10 icpt-close    F1/I3                    via (*usableRouteConfiguration).stop < (*filterChain).applyConfiguration < (*filterChain).updateUsableRouteConfiguration < (*listenerWrapper).handleRDSUpdate < (*rdsWatcher).ResourceChanged
PHASE noflag-rds-replace-no-virtual-hosts: #11 ref-release   F1 (refCnt before=2)     via (*filterChain).applyConfiguration < (*filterChain).updateUsableRouteConfiguration < (*listenerWrapper).handleRDSUpdate < (*rdsWatcher).ResourceChanged
PHASE noflag-rds-replace-no-virtual-hosts: #12 ref-release   F1 (refCnt before=1)     via (*filterChain).applyConfiguration < (*filterChain).updateUsableRouteConfiguration < (*listenerWrapper).handleRDSUpdate < (*rdsWatcher).ResourceChanged
PHASE noflag-rds-replace-no-virtual-hosts: #13 filter-close  F1                       via (*filterChain).applyConfiguration < (*filterChain).updateUsableRouteConfiguration < (*listenerWrapper).handleRDSUpdate < (*rdsWatcher).ResourceChanged
PHASE noflag-rds-replace-no-virtual-hosts: SUMMARY first ref-release=#11 first filter-close=#13 last retired icpt-close=#10 retired-interceptors-never-closed=0 RELEASE_BEFORE_CLOSE=false FILTER_CLOSED_BEFORE_INTERCEPTORS=false
--- PASS: Test (6.19s)
--- PASS: Test/VerifyC1_LDSReplace_Then_Stop (2.07s)
--- PASS: Test/VerifyC1_ReplaceKeep_Then_DisableAll (2.08s)
--- PASS: Test/VerifyC1_ReplaceKeep_Then_NoVirtualHosts (2.04s)
ok  	google.golang.org/grpc/test/xds	7.225s
```

The eval fixture passes on the target branch (its swap-order test does not remove every use of the filter):

```console
$ cd ~/wt/b7bc0d7f && cp ~/eval/tests/eval_xds_server_interceptor_leak_test.go test/xds/ && cmp ... && go test -race -count=1 -v -run '^Test$/^Eval_' ./test/xds | grep -E '^\s+--- (PASS|FAIL): Test/|^(ok|FAIL)'
cmp: identical
    --- PASS: Test/Eval_ServerSideXDS_InterceptorLeak_MultiGenerationRDSUpdate (0.11s)
    --- PASS: Test/Eval_ServerSideXDS_InterceptorLeak_RDSUpdate (0.03s)
    --- PASS: Test/Eval_ServerSideXDS_InterceptorSwapOrder (0.03s)
    --- PASS: Test/Eval_ServerSideXDS_PartialRouteFailure_ClosesInterceptors (0.03s)
    --- PASS: Test/Eval_ServerSideXDS_PartialVirtualHostFailure_ClosesInterceptors (0.03s)
ok  	google.golang.org/grpc/test/xds	1.243s
```

Part *Replacement release ordering* — observed: in `rds-replace-filter-kept`, `#07 ref-release F1 (refCnt before=3) via (*filterChain).constructUsableRouteConfiguration < (*listenerWrapper).handleRDSUpdate` precedes `#08 icpt-close F1/I1 via ... (*filterChain).setUsableRouteConfiguration < (*listenerWrapper).handleRDSUpdate`: the old reference is released inside replacement construction, the retired interceptor is closed afterwards by the setter (`RELEASE_BEFORE_CLOSE=true`).

Part *Final-reference destruction* — observed: in `rds-replace-filter-disabled-on-every-route`, `#09`/`#10 ref-release` (counts 2 → 0) and `#11 filter-close F1` all come from `constructUsableRouteConfiguration`, and only then `#12 icpt-close F1/I2` and `#13 icpt-close F1/I3` run (`FILTER_CLOSED_BEFORE_INTERCEPTORS=true`); `noflag-rds-replace-no-virtual-hosts` shows the identical order without the flag. On the audited branch the order is `#09`, `#10 icpt-close`, then `#11`, `#12 ref-release`, then `#13 filter-close`.

Impact reasoning. Removing every use of a filter through RDS needs either the experimental disabled override or a RouteConfiguration without virtual hosts; both are valid updates and both show the same order. On this branch the filter's `Close` completes while two of its interceptors are still open (they are closed afterwards by the setter), so interceptor `Close` runs against an already-closed parent filter. Effects are filter-specific (shared resources released by the filter's `Close` are gone while interceptors still use them). No workaround other than never removing every use of a filter through RDS. Fix: move the release of `fc.serverFilters` after the retired configuration's interceptors have been closed (as `applyConfiguration` does on the audited branch).

## C9

Claim: `internal/xds/server/route_configuration_test.go` passes a bare `context.Background()` to `transport.SetConnection` rather than a deadline-bounded test context. Target: [evalon/grpc-go-xd-6cf08267](https://github.com/kaitranntt-evals/grpc-go-xds-rds-interceptor-lifecycle-leak/tree/evalon/grpc-go-xd-6cf08267) (HEAD 1b50e1dd). This is a claim about test-file text; the text plus a demonstration run of the eval's own check is the evidence.

```console
$ verify/repro/c9_bare_context.sh ~/wt/6cf08267
$ git grep -n 'SetConnection(' -- internal/xds/server/route_configuration_test.go
internal/xds/server/route_configuration_test.go:233:	ctx = transport.SetConnection(ctx, &connWrapper{urc: fc.usableRouteConfiguration})
internal/xds/server/route_configuration_test.go:288:	ctx := transport.SetConnection(context.Background(), conn)
$ ! git grep -e 'context.Background()' --or -e 'context.TODO()' -- 'internal/xds/server/*_test.go' 'test/xds/*_test.go' | grep -v 'context.WithTimeout(' | grep -v 'context.WithCancel(' | grep .
internal/xds/server/route_configuration_test.go:	ctx := transport.SetConnection(context.Background(), conn)
check exit=1 (0 = no bare context, 1 = bare context present)
$ same check at the base commit 4ee6ac46
base check exit=0
$ git diff 4ee6ac46 HEAD --stat -- internal/xds/server/route_configuration_test.go
 internal/xds/server/route_configuration_test.go | 298 ++++++++++++++++++++++++
 1 file changed, 298 insertions(+)
$ go test -race -count=1 -run '^Test$' ./internal/xds/server
ok  	google.golang.org/grpc/internal/xds/server	1.094s
```

Context of the call (target branch):

```go
270	// Listener updates retire filter chains before their connections finish
271	// draining. RPCs on those connections must still be able to use the old table.
272	func (s) TestRouteConfigurationDrainingConnection(t *testing.T) {
273		b := &routeLifecycleFilterBuilder{}
274		fc := &filterChain{
275			httpFilters:              []xdsresource.HTTPFilter{{Name: "filter", Filter: b}},
276			usableRouteConfiguration: &atomic.Pointer[usableRouteConfiguration]{},
277			conns:                    1,
278		}
279		filters := make(map[serverFilterKey]*refCountedServerFilter)
280		provider := func(f xdsresource.HTTPFilter) (httpfilter.ServerFilter, error) {
281			return getOrCreateServerFilterWithMap(filters, b, newServerFilterKey(&f)), nil
282		}
283		fc.updateUsableRouteConfiguration(fc.constructUsableRouteConfiguration(lifecycleRouteConfig(), provider))
284		rawConn, peer := net.Pipe()
285		defer peer.Close()
286		conn := &connWrapper{Conn: rawConn, filterChain: fc, parent: &listenerWrapper{}, urc: fc.usableRouteConfiguration}
287		defer conn.Close()
288		ctx := transport.SetConnection(context.Background(), conn)
289		ctx = grpc.NewContextWithServerTransportStream(ctx, routeTestStream{})
290		ctx = metadata.NewIncomingContext(ctx, metadata.Pairs(":authority", "server"))
291		fc.stop()
292		if err := RouteAndProcess(ctx); err != nil {
293			t.Fatalf("RPC on draining connection failed: %v", err)
294		}
295		conn.Close()
296		conn.Close()
297		b.checkClosed(t)
298	}
```

Observed: line 288 is `ctx := transport.SetConnection(context.Background(), conn)` — the argument is `context.Background()` itself, with no `context.WithTimeout`/`context.WithCancel` wrapper on that line or before it in the test (the other `SetConnection` call in the file, line 233, receives an existing `ctx`). The environment's bare-context check (`! git grep ... | grep -v 'context.WithTimeout(' | grep -v 'context.WithCancel(' | grep .`) exits 1 on this branch and prints exactly that line; it exits 0 on the base commit, and the file is new on the branch (298 insertions), so the violation is introduced by the branch. The test itself passes.

Impact reasoning. The repository convention (enforced by the check listed in the environment's build/test commands) is that tests derive their contexts from a deadline-bounded test context; this call hands a context that can never expire to `RouteAndProcess`, so if routing or an interceptor in that test ever blocks on the context the test hangs until the global `go test` timeout instead of failing at its own deadline, and the convention check fails for the branch. Fix: create `ctx, cancel := context.WithTimeout(context.Background(), defaultTestTimeout)` and pass that `ctx` to `transport.SetConnection`.

## C10

Claim: concurrent configuration replacement closes an interceptor before an active request that selected the old configuration invokes it. Target: the audited branch [grpc-go-xds-rds-interceptor-lifecycle-leak-perfect](https://github.com/kaitranntt-evals/grpc-go/tree/grpc-go-xds-rds-interceptor-lifecycle-leak-perfect) (HEAD 614cb739).

Method. `verify/repro/c4_c10_closed_interceptor_invoked_test.go` runs a live `xds.NewGRPCServer` whose HTTP connection manager has two filters ahead of the router, `gate` and `probe`. One RPC is paused inside the `gate` interceptor's `AllowRPC`, i.e. after `RouteAndProcess` has selected the current `usableRouteConfiguration` and before the `probe` interceptor of that configuration is invoked. The RouteConfiguration is then replaced in place through RDS (same name, Listener untouched); the test waits until the replacement is applied, records whether the old `probe` interceptor's `Close()` has completed, resumes the RPC and records whether the old `probe` interceptor's `AllowRPC` runs after its `Close()` completed. No production code is modified.

Source of the two code paths involved (audited branch):

```go
// internal/xds/server/routing.go — RouteAndProcess loads the configuration once and keeps no claim on it
40		conn := transport.GetConnection(ctx)
41		cw, ok := conn.(*connWrapper)
42		if !ok {
43			return errors.New("missing virtual hosts in incoming context")
44		}
45	
46		rc := cw.urc.Load()
47		// Error out at routing l7 level with a status code UNAVAILABLE, represents
48		// an nack before usable route configuration or resource not found for RDS
49		// or error combining LDS + RDS (Shouldn't happen).
50		if rc.err != nil {
...
93			return rc.statusErrWithNodeID(codes.Unavailable, "the incoming RPC did not match a configured Route")
94		}
95		if err := rwi.interceptor.AllowRPC(ctx); err != nil {
96			return rc.statusErrWithNodeID(codes.PermissionDenied, "Incoming RPC is not allowed: %v", err)
97		}

// internal/xds/server/filter_chain_manager.go — the RDS path closes the previous configuration's interceptors unconditionally
449	func (fc *filterChain) applyConfiguration(urc *usableRouteConfiguration, serverFilters []httpfilter.ServerFilter) {
450		// Swap in the new configuration first so new RPCs use it immediately.
451		oldURC := fc.usableRouteConfiguration.Swap(urc)
452		oldFilters := fc.serverFilters
453		fc.serverFilters = serverFilters
454	
455		// Stop the old interceptors before releasing the filters they might depend on.
456		if oldURC != nil {
457			oldURC.stop()
458		}
459	
460		// Release references to old server filters.
461		for _, sf := range oldFilters {
462			sf.Close()
463		}
464	}
```

Run on the audited branch:

```console
$ cd ~/wt/perfect && cp ~/repos/grpc-go/verify/repro/c4_c10_closed_interceptor_invoked_test.go test/xds/verify_c4_c10_closed_interceptor_invoked_test.go && go test -race -count=1 -v -run '^Test$/^VerifyC4C10_' ./test/xds   # lines printed by the repro + result lines
verify_c4_c10_closed_interceptor_invoked_test.go:229: RPC paused inside the gate interceptor of the old configuration (probe interceptor #1 not yet invoked for it)
verify_c4_c10_closed_interceptor_invoked_test.go:119: probe interceptor #1: Close() completed
verify_c4_c10_closed_interceptor_invoked_test.go:244: replacement applied: 3 probe interceptors built in total
verify_c4_c10_closed_interceptor_invoked_test.go:250: OWNERSHIP: old probe interceptor #1 closed while the RPC that selected its configuration is still paused: true
verify_c4_c10_closed_interceptor_invoked_test.go:111: probe interceptor #1: AllowRPC invoked AFTER its Close() completed
verify_c4_c10_closed_interceptor_invoked_test.go:255: paused RPC finished with err=<nil>
verify_c4_c10_closed_interceptor_invoked_test.go:261: CONCURRENT INVOCATION: resumed RPC invoked old probe interceptor #1 1 time(s); 1 of them after its Close() completed
verify_c4_c10_closed_interceptor_invoked_test.go:263: selecting a configuration did not retain its interceptors: probe interceptor #1 was closed while an RPC using its configuration was in flight
verify_c4_c10_closed_interceptor_invoked_test.go:266: already-closed interceptor #1 was invoked by the in-flight RPC 1 time(s)
verify_c4_c10_closed_interceptor_invoked_test.go:119: probe interceptor #2: Close() completed
verify_c4_c10_closed_interceptor_invoked_test.go:119: probe interceptor #3: Close() completed
--- FAIL: Test (0.10s)
--- FAIL: Test/VerifyC4C10_InPlaceRDSReplacement_ClosesInterceptorOfInFlightRPC (0.10s)
FAIL
FAIL	google.golang.org/grpc/test/xds	0.130s
FAIL
exit=1
```

Same repro on the base commit `4ee6ac46` (control — before the change the superseded interceptor is never closed by an RDS update, which is the leak the task fixes, so the in-flight RPC invokes an open interceptor):

```console
$ cd ~/wt/base && cp ~/repos/grpc-go/verify/repro/c4_c10_closed_interceptor_invoked_test.go test/xds/verify_c4_c10_closed_interceptor_invoked_test.go && go test -race -count=1 -v -run '^Test$/^VerifyC4C10_' ./test/xds
verify_c4_c10_closed_interceptor_invoked_test.go:229: RPC paused inside the gate interceptor of the old configuration (probe interceptor #1 not yet invoked for it)
verify_c4_c10_closed_interceptor_invoked_test.go:244: replacement applied: 3 probe interceptors built in total
verify_c4_c10_closed_interceptor_invoked_test.go:250: OWNERSHIP: old probe interceptor #1 closed while the RPC that selected its configuration is still paused: false
verify_c4_c10_closed_interceptor_invoked_test.go:255: paused RPC finished with err=<nil>
verify_c4_c10_closed_interceptor_invoked_test.go:261: CONCURRENT INVOCATION: resumed RPC invoked old probe interceptor #1 1 time(s); 0 of them after its Close() completed
verify_c4_c10_closed_interceptor_invoked_test.go:119: probe interceptor #2: Close() completed
verify_c4_c10_closed_interceptor_invoked_test.go:119: probe interceptor #3: Close() completed
--- PASS: Test (5.09s)
--- PASS: Test/VerifyC4C10_InPlaceRDSReplacement_ClosesInterceptorOfInFlightRPC (5.09s)
ok  	google.golang.org/grpc/test/xds	6.120s
exit=0
```

The eval fixture does not exercise this interleaving; it stays green on the audited branch:

```console
$ cd ~/wt/perfect && cp ~/eval/tests/eval_xds_server_interceptor_leak_test.go test/xds/ && cmp ... && go test -race -count=1 -v -run '^Test$/^Eval_' ./test/xds | grep -E '^\s+--- (PASS|FAIL): Test/|^(ok|FAIL)'
cmp: identical
    --- PASS: Test/Eval_ServerSideXDS_InterceptorLeak_MultiGenerationRDSUpdate (0.06s)
    --- PASS: Test/Eval_ServerSideXDS_InterceptorLeak_RDSUpdate (0.04s)
    --- PASS: Test/Eval_ServerSideXDS_InterceptorSwapOrder (0.03s)
    --- PASS: Test/Eval_ServerSideXDS_PartialRouteFailure_ClosesInterceptors (0.03s)
    --- PASS: Test/Eval_ServerSideXDS_PartialVirtualHostFailure_ClosesInterceptors (0.03s)
ok  	google.golang.org/grpc/test/xds	1.225s
```

Part *Selected-configuration ownership* — observed: `replacement applied: 3 probe interceptors built in total` followed by `OWNERSHIP: old probe interceptor #1 closed while the RPC that selected its configuration is still paused: true` (the `Close() completed` line for interceptor #1 is logged before the RPC is resumed). `RouteAndProcess` selected the old configuration (`rc := cw.urc.Load()`) and that selection did not defer closure by `applyConfiguration` → `oldURC.stop()`.

Part *Concurrent invocation* — observed: the request was paused inside its first interceptor (`gate`), the configuration was replaced, and the later interceptor (`probe` #1) was then invoked after its `Close` completed: `probe interceptor #1: AllowRPC invoked AFTER its Close() completed`; `CONCURRENT INVOCATION: resumed RPC invoked old probe interceptor #1 1 time(s); 1 of them after its Close() completed`.

Impact reasoning. Any RPC that is between configuration selection and its last interceptor call when an RDS update for its route configuration lands — a window that is as long as the slowest earlier interceptor (e.g. an authz interceptor doing a remote check) — has the remaining interceptors closed underneath it and then invokes them. This breaks the documented contract of `iresolver.ServerInterceptor.Close` (`internal/resolver/config_selector.go`: "Close closes the interceptor. Once called, no new calls to NewStream are accepted. Ongoing calls to NewStream are allowed to complete."): the server itself makes a new call into an interceptor it has closed. What happens then is filter-specific (an interceptor that released state in `Close` decides on released state). The RPC in the repro returned `err=<nil>`: nothing surfaces the misuse. RDS updates on a serving listener are the everyday path the task is about, and the behavior is introduced by the change (the base commit never closes the superseded interceptor). There is no configuration workaround; the fix needs the selected configuration to be retained (reference count / in-flight tracking) until routing releases it, and closure deferred until then.

## C11

Claim: same-name RDS enable-disable-enable updates under an unchanged listener reuse a server filter instance that was closed when its last reference was released. Target: the audited branch [grpc-go-xds-rds-interceptor-lifecycle-leak-perfect](https://github.com/kaitranntt-evals/grpc-go/tree/grpc-go-xds-rds-interceptor-lifecycle-leak-perfect) (HEAD 614cb739).

Method. Two repros, no production code modified.

- `verify/repro/c5_c11_cache_lookup_test.go` (package `server`) calls `getOrCreateServerFilterWithMap` on an empty map, releases the returned reference (`Close()`), and calls the lookup again with the same key, printing the builder invocation count, the entry's `refCnt` and the filter's close count at each step.
- `verify/repro/c5_c11_closed_filter_reuse_test.go` (package `xds_test`) runs a live `xds.NewGRPCServer`; the Listener resource is sent once and never changed. The RouteConfiguration of the same name is updated three times through RDS: filter enabled → every use removed → enabled again. Two variants of the middle step: `EnableDisableEnable` disables the filter on every route with a virtual-host-level `FilterConfig{disabled: true}` override (`FilterConfig{disabled: true}` overrides are honored only when `GRPC_EXPERIMENTAL_XDS_EXT_PROC_ON_CLIENT=true` (`envconfig.XDSClientExtProcEnabled`, default false; the test sets it with `testutils.SetEnvConfig`)); `EnableEmptyEnable` uses default settings and sends a RouteConfiguration without virtual hosts. The test logs every `BuildServerFilter`, `BuildServerInterceptor` and `Close` of the filter instance.

Source (audited branch):

```go
409	func getOrCreateServerFilterWithMap(httpFilters map[serverFilterKey]*refCountedServerFilter, builder httpfilter.ServerFilterBuilder, key serverFilterKey) httpfilter.ServerFilter {
410		serverFilter, ok := httpFilters[key]
411		if ok {
412			serverFilter.incRef()
413			return serverFilter
414		}
415	
416		sf := builder.BuildServerFilter()
417		serverFilter = &refCountedServerFilter{ServerFilter: sf}
418		httpFilters[key] = serverFilter
419		serverFilter.incRef()
420		return serverFilter
421	}
...
578	func (rsf *refCountedServerFilter) incRef() {
579		rsf.refCnt.Add(1)
580	}
581	
582	func (rsf *refCountedServerFilter) Close() {
583		if rsf.refCnt.Add(-1) == 0 {
584			rsf.ServerFilter.Close()
585		}
586	}
```

Cache lookup:

```console
$ cd ~/wt/perfect && cp ~/repos/grpc-go/verify/repro/c5_c11_cache_lookup_test.go internal/xds/server/verify_c5_c11_cache_lookup_test.go && go test -race -count=1 -v -run '^Test$/^VerifyC5C11_' ./internal/xds/server
verify_c5_c11_cache_lookup_test.go:43: after 1st lookup: built=1 refCnt=1 closes=0
verify_c5_c11_cache_lookup_test.go:47: after releasing the last reference: refCnt=0 closes=1 stillCached=true
verify_c5_c11_cache_lookup_test.go:50: after 2nd lookup: built=1 sameEntry=true refCnt=1 closes(of returned filter)=1
verify_c5_c11_cache_lookup_test.go:52: lookup returned the cached entry whose filter was already closed (builder invoked 1 time(s), want 2)
--- FAIL: Test (0.00s)
--- FAIL: Test/VerifyC5C11_CacheLookupReturnsClosedZeroRefEntry (0.00s)
FAIL
FAIL	google.golang.org/grpc/internal/xds/server	0.028s
FAIL
exit=1
```

RDS enable → disable → enable under unchanged LDS (first block of events is `EnableDisableEnable`, second is `EnableEmptyEnable`):

```console
$ cd ~/wt/perfect && cp ~/repos/grpc-go/verify/repro/c5_c11_closed_filter_reuse_test.go test/xds/verify_c5_c11_closed_filter_reuse_test.go && go test -race -count=1 -v -run '^Test$/^VerifyC5C11_' ./test/xds
verify_c5_c11_closed_filter_reuse_test.go:205: STEP 1: RDS enables the filter
verify_c5_c11_closed_filter_reuse_test.go:71: EVENT filter F1 built
verify_c5_c11_closed_filter_reuse_test.go:96: EVENT filter F1: BuildServerInterceptor #1
verify_c5_c11_closed_filter_reuse_test.go:228: STEP 2: RDS update of the same name removes every use of the filter (LDS unchanged)
verify_c5_c11_closed_filter_reuse_test.go:102: EVENT filter F1: Close() completed (close #1)
verify_c5_c11_closed_filter_reuse_test.go:231: after step 2: F1 closes=1, filter instances built=1
verify_c5_c11_closed_filter_reuse_test.go:233: STEP 3: RDS update of the same name enables the filter again (LDS unchanged)
verify_c5_c11_closed_filter_reuse_test.go:94: EVENT filter F1: BuildServerInterceptor #2 called AFTER the filter's Close() completed
verify_c5_c11_closed_filter_reuse_test.go:251: RESULT: filter instances built in total=1; F1 closes=1; interceptors built on F1 after its Close()=1; RPCs allowed by interceptors of closed F1=1; RPC after re-enable err=<nil>
verify_c5_c11_closed_filter_reuse_test.go:254: re-enabling reused the ServerFilter instance closed during disabling: 1 interceptor(s) built on closed filter F1 and no new filter instance was built (instances=1)
verify_c5_c11_closed_filter_reuse_test.go:102: EVENT filter F1: Close() completed (close #2)
verify_c5_c11_closed_filter_reuse_test.go:205: STEP 1: RDS enables the filter
verify_c5_c11_closed_filter_reuse_test.go:71: EVENT filter F1 built
verify_c5_c11_closed_filter_reuse_test.go:96: EVENT filter F1: BuildServerInterceptor #1
verify_c5_c11_closed_filter_reuse_test.go:228: STEP 2: RDS update of the same name removes every use of the filter (LDS unchanged)
verify_c5_c11_closed_filter_reuse_test.go:102: EVENT filter F1: Close() completed (close #1)
verify_c5_c11_closed_filter_reuse_test.go:231: after step 2: F1 closes=1, filter instances built=1
verify_c5_c11_closed_filter_reuse_test.go:233: STEP 3: RDS update of the same name enables the filter again (LDS unchanged)
verify_c5_c11_closed_filter_reuse_test.go:94: EVENT filter F1: BuildServerInterceptor #2 called AFTER the filter's Close() completed
verify_c5_c11_closed_filter_reuse_test.go:251: RESULT: filter instances built in total=1; F1 closes=1; interceptors built on F1 after its Close()=1; RPCs allowed by interceptors of closed F1=1; RPC after re-enable err=<nil>
verify_c5_c11_closed_filter_reuse_test.go:254: re-enabling reused the ServerFilter instance closed during disabling: 1 interceptor(s) built on closed filter F1 and no new filter instance was built (instances=1)
verify_c5_c11_closed_filter_reuse_test.go:102: EVENT filter F1: Close() completed (close #2)
--- FAIL: Test (0.10s)
--- FAIL: Test/VerifyC5C11_EnableDisableEnable (0.06s)
--- FAIL: Test/VerifyC5C11_EnableEmptyEnable (0.04s)
FAIL
FAIL	google.golang.org/grpc/test/xds	0.126s
FAIL
exit=1
```

Same repros on the base commit `4ee6ac46` (the behavior predates the change; the audited branch leaves it in place):

```console
$ cd ~/wt/base && ... go test -race -count=1 -v -run '^Test$/^VerifyC5C11_' ./internal/xds/server
verify_c5_c11_cache_lookup_test.go:43: after 1st lookup: built=1 refCnt=1 closes=0
verify_c5_c11_cache_lookup_test.go:47: after releasing the last reference: refCnt=0 closes=1 stillCached=true
verify_c5_c11_cache_lookup_test.go:50: after 2nd lookup: built=1 sameEntry=true refCnt=1 closes(of returned filter)=1
verify_c5_c11_cache_lookup_test.go:52: lookup returned the cached entry whose filter was already closed (builder invoked 1 time(s), want 2)
--- FAIL: Test (0.00s)
--- FAIL: Test/VerifyC5C11_CacheLookupReturnsClosedZeroRefEntry (0.00s)
FAIL
FAIL	google.golang.org/grpc/internal/xds/server	0.027s
FAIL
exit=1
$ cd ~/wt/base && ... go test -race -count=1 -v -run '^Test$/^VerifyC5C11_' ./test/xds | grep -E 'RESULT|--- |^(ok|FAIL)'
verify_c5_c11_closed_filter_reuse_test.go:251: RESULT: filter instances built in total=1; F1 closes=1; interceptors built on F1 after its Close()=1; RPCs allowed by interceptors of closed F1=1; RPC after re-enable err=<nil>
verify_c5_c11_closed_filter_reuse_test.go:251: RESULT: filter instances built in total=1; F1 closes=1; interceptors built on F1 after its Close()=1; RPCs allowed by interceptors of closed F1=1; RPC after re-enable err=<nil>
--- FAIL: Test (0.10s)
--- FAIL: Test/VerifyC5C11_EnableDisableEnable (0.05s)
--- FAIL: Test/VerifyC5C11_EnableEmptyEnable (0.04s)
FAIL
FAIL	google.golang.org/grpc/test/xds	0.122s
FAIL
exit=1
```

Part *Zero-reference cache reuse* — observed: `after releasing the last reference: refCnt=0 closes=1 stillCached=true`, then `after 2nd lookup: built=1 sameEntry=true refCnt=1 closes(of returned filter)=1`: `getOrCreateServerFilterWithMap` incremented and returned the cached closed entry (count 0 → 1) without recreating its filter.

Part *Same-name RDS reenabling* — observed in both variants: the disabling update closes the instance (`EVENT filter F1: Close() completed (close #1)`, `after step 2: F1 closes=1, filter instances built=1`), and the re-enabling update of the same RouteConfiguration name, with the Listener never re-sent, builds its interceptor on that same instance (`EVENT filter F1: BuildServerInterceptor #2 called AFTER the filter's Close() completed`; `filter instances built in total=1`).

Impact reasoning. A control plane that removes every use of a filter from a RouteConfiguration and later restores it — with default settings: the RouteConfiguration temporarily has no virtual hosts; with the experimental flag: the filter is disabled on every route and re-enabled, e.g. a rollout/rollback — leaves the server building all new interceptors on a `ServerFilter` whose `Close()` has already completed, for as long as the Listener resource stays the same; RPCs are then admitted by those interceptors (`RPCs allowed by interceptors of closed F1=1`, `RPC after re-enable err=<nil>`). Nothing reports it: the filter is not rebuilt, no error is logged, and the instance is later closed a second time (`close #2`) when its count returns to zero again. What a closed filter does is filter-specific (resources released in `Close` are gone). No workaround was tested. The eval fixture (`fixture_perfect` above in Setup: 5 of 5 PASS) does not cover the sequence. Minimal fix: in `getOrCreateServerFilterWithMap`, do not hand out an entry whose count reached zero (remove the entry from the map when the count drops to zero, or rebuild the filter on lookup).
