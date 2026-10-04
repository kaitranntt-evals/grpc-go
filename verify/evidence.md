## Setup used for every section

All commands were run on Linux, `go version go1.25.7 linux/amd64`, from the `grpc-go` checkout. Claim branches live in a second repository; each was fetched and checked out detached in its own worktree under `~/wt/<short-hash>`. Probe/repro files carry `//go:build verify_audit`, so they are inert for `go build ./...` / `go vet ./...` in this repo and are only compiled when copied into `internal/transport/` of a claim branch and run with `-tags verify_audit`. The eval fixture (`tests/eval_recv_buffer_compaction_test.go` from `eval_tests.zip`, sha256 `6b7e61387d453f8e5dcb0f040c4f89e39b0312e8203f826325860773c6ee707e`) was copied byte-exact to `internal/transport/eval_recv_buffer_compaction_test.go` where a section says so.

```sh
bash verify/setup_worktrees.sh                 # adds remote "claims", creates ~/wt/<hash> for all 16 claim branches
FIXTURE=/path/to/tests/eval_recv_buffer_compaction_test.go bash verify/run_all.sh   # replays everything below into verify/logs/
```

Branch heads audited:

```console
evalon/grpc-go-tr-2b0ddded 611fec512e9f99e80d7ec46c335805bb0f02f359
evalon/grpc-go-tr-5ccf77e7 81bf5de5308c7252f548c00ca073821089480722
evalon/grpc-go-tr-ae31c146 4210b350a3f31a777c539a10c9e084b87fcc8225
evalon/grpc-go-tr-2994fc5c 6d5c045abc947eeb75010f58a95d8dddb2ac8477
evalon/grpc-go-tr-d2e82310 fb184a6ff34579bb837e5c85a81c8d6cddb5db3b
evalon/grpc-go-tr-d0aa7e9e 66ae7d75f1452c3e90442992a0fc8ff8d1f24fc7
evalon/grpc-go-tr-4c52c410 e78fe589364411f329b77b47a6a1f682fb4211e0
evalon/grpc-go-tr-72e9069b d7f89284da61c490caf0f987573109ac8d10b445
evalon/grpc-go-tr-3e551c7b 9ff9c4f834b3a49d5001d407261dd17b47853af8
evalon/grpc-go-tr-17011fad 7ed96ec136363aeaaf06e90a19197fc4f6fa8fbe
evalon/grpc-go-tr-8b3e4b01 95c90395a6b181bfd5820bc422036fc6ec469827
evalon/grpc-go-tr-9253f1d4 e74a35b9d4966a83d7f2fbd484b6e64c33650b08
evalon/grpc-go-tr-a3b171be 06408714e307f57a5d3ce6fce96f035114d2b4a4
evalon/grpc-go-tr-718bb10b 9b6fe5f3087b952c710c314a9548967162af2f29
evalon/grpc-go-tr-37793f8a 76b1ff251c7a689e8cc26fc6a1a5966ea3e177f9
evalon/grpc-go-tr-d4a3af3a 1661a09864d15b1c0867e853133f8f6468ee4fc6
```

## C1

**Claim:** the added receive-memory regressions accept nonpositive heap-growth samples as evidence of memory reduction without an independent deterministic buffering bound. Ten branches, adjudicated independently. **Verdict: CONFIRMED on all ten** (on `ae31c146` only for zero samples; negative samples are rejected there by unsigned wrap-around).

**Method (same on every branch, driver: `verify/repro/c1_run.sh`, helper: `verify/repro/c1_inject_test.go`).** For the tests the branch added in `internal/transport/recv_buffer*_test.go`:

1. *baseline* - run unmodified.
2. *inject* - test-only patch: the heap-growth expression (`after.HeapAlloc - before.HeapAlloc`, `heapAlloc() - before`, ...) is wrapped in `c1Inject(...)`, which returns `$C1_INJECT` instead of the measured value whenever `envconfig.EnableReceiveBufferCompaction` is true. Run with `C1_INJECT=0` and `C1_INJECT=-1048576`. The assertions themselves are untouched. A passing run means the assertions accept that sample.
3. *neuter* - production patch: the compaction branch in `recvBuffer.put` is made unreachable (`envconfig.EnableReceiveBufferCompaction` -> `(envconfig.EnableReceiveBufferCompaction && false)`, or `if false && b.compact ...` on `ae31c146`) while the flag itself still reads "enabled". Run with real samples (expected: heap assertion fails) and with injected samples. If **every** added test passes with compaction neutered and a 0 / negative sample injected, then no queue-depth, buffer-count or consolidation assertion exists anywhere in the added tests: the heap sample is the only thing that bounds buffering.

```sh
for b in 2b0ddded 5ccf77e7 ae31c146 2994fc5c d2e82310 d0aa7e9e 4c52c410 72e9069b 3e551c7b 17011fad; do
  bash verify/repro/c1_run.sh ~/wt/$b verify/logs/c1/$b
done
```

Every `go test` inside the driver is `go test -tags verify_audit -v -run '<added tests>' google.golang.org/grpc/internal/transport -race -count=1`. Full logs and the exact patches applied (`inject.patch`, `neuter.patch`) are in `verify/logs/c1/<hash>/`.

No added test file on any of the ten branches references `len(b.backlog)` or compares a sample against `<= 0`:

```console
$ grep -n "len(.*backlog)\|<= 0\|< 0" ~/wt/{2b0ddded,5ccf77e7,ae31c146,2994fc5c,d2e82310,d0aa7e9e,4c52c410,72e9069b,3e551c7b,17011fad}/internal/transport/recv_buffer*_test.go
$ echo $?
1
```

### C1 on [evalon/grpc-go-tr-2b0ddded](https://github.com/kaitranntt-evals/grpc-go-transport-restrict-memory-overhead/tree/evalon/grpc-go-tr-2b0ddded)

Memory assertions: recv_buffer_test.go:146-151, 331-336 (signed `int64`): `enabled > 4*numMsgs` / `enabled > 8*numFrames` -> error; `enabled*4 > disabled` -> error. All are upper bounds on the enabled-mode sample; none has a lower bound.

```console
$ bash verify/repro/c1_run.sh ~/wt/2b0ddded verify/logs/c1/2b0ddded
branch HEAD: 611fec51  test file: internal/transport/recv_buffer_test.go
go test -run '^Test$/^(RecvBuffer_TinyMessagesPreserveOrder|RecvBuffer_TinyMessagesMemory|ClientTinyDataFramesMemory)$'
baseline                              : ok  	google.golang.org/grpc/internal/transport	1.931s
injection sites patched               : 2
inject 0        (solution intact)     : ok  	google.golang.org/grpc/internal/transport	1.894s
inject -1048576 (solution intact)     : ok  	google.golang.org/grpc/internal/transport	2.165s
compaction neutered, real samples     : FAIL	google.golang.org/grpc/internal/transport	1.515s
compaction neutered + inject 0        : ok  	google.golang.org/grpc/internal/transport	2.176s
compaction neutered + inject -1048576 : ok  	google.golang.org/grpc/internal/transport	2.353s
```

Baseline samples (real):

```console
    recv_buffer_test.go:329: Heap growth for 60000 unread 1-byte DATA frames: compaction enabled = 61912 bytes, disabled = 4762552 bytes
    recv_buffer_test.go:144: Heap growth for 65536 unread 1-byte messages: compaction enabled = 65776 bytes, disabled = 4972544 bytes
```

Compaction neutered, real samples (only the heap assertions notice):

```console
    recv_buffer_test.go:332: Heap growth with compaction enabled = 4756856 bytes, want <= 480000 bytes
    recv_buffer_test.go:335: Heap growth with compaction enabled = 4756856 bytes, want less than a quarter of heap growth with compaction disabled (4762360 bytes)
    recv_buffer_test.go:147: Heap growth with compaction enabled = 4972736 bytes, want <= 262144 bytes
    recv_buffer_test.go:150: Heap growth with compaction enabled = 4972736 bytes, want less than a quarter of heap growth with compaction disabled (4972640 bytes)
--- FAIL: Test (1.49s)
    --- FAIL: Test/ClientTinyDataFramesMemory (1.09s)
    --- FAIL: Test/RecvBuffer_TinyMessagesMemory (0.14s)
```

Compaction neutered **and** enabled-mode sample forced to 0 (every added test passes):

```console
    recv_buffer_test.go:329: Heap growth for 60000 unread 1-byte DATA frames: compaction enabled = 0 bytes, disabled = 4767976 bytes
    recv_buffer_test.go:144: Heap growth for 65536 unread 1-byte messages: compaction enabled = 0 bytes, disabled = 4972544 bytes
--- PASS: Test (1.15s)
    --- PASS: Test/ClientTinyDataFramesMemory (0.79s)
    --- PASS: Test/RecvBuffer_TinyMessagesMemory (0.12s)
    --- PASS: Test/RecvBuffer_TinyMessagesPreserveOrder (0.21s)
--- PASS: Test (0.00s)
ok  	google.golang.org/grpc/internal/transport	2.176s
```

Negative sample (`C1_INJECT=-1048576`) with compaction neutered:

```console
ok  	google.golang.org/grpc/internal/transport	2.353s
```

Per-branch verdict: **CONFIRMED** - zero and negative samples satisfy every memory assertion even while production buffering is uncompacted; no added test bounds queue depth, buffer count or consolidation.

### C1 on [evalon/grpc-go-tr-5ccf77e7](https://github.com/kaitranntt-evals/grpc-go-transport-restrict-memory-overhead/tree/evalon/grpc-go-tr-5ccf77e7)

Memory assertions: recv_buffer_compaction_test.go:247-252 (signed `int64`): `compacted > 8*numFrames` -> error; `compacted*4 > uncompacted` -> error. All are upper bounds on the enabled-mode sample; none has a lower bound.

```console
$ bash verify/repro/c1_run.sh ~/wt/5ccf77e7 verify/logs/c1/5ccf77e7
branch HEAD: 81bf5de5  test file: internal/transport/recv_buffer_compaction_test.go
go test -run '^Test$/^(RecvBuffer_DeliversAllBytesInOrder|ServerTransport_TinyDataFramesReceiveMemory)$'
baseline                              : ok  	google.golang.org/grpc/internal/transport	5.147s
injection sites patched               : 1
inject 0        (solution intact)     : ok  	google.golang.org/grpc/internal/transport	5.106s
inject -1048576 (solution intact)     : ok  	google.golang.org/grpc/internal/transport	5.000s
compaction neutered, real samples     : FAIL	google.golang.org/grpc/internal/transport	5.750s
compaction neutered + inject 0        : ok  	google.golang.org/grpc/internal/transport	6.093s
compaction neutered + inject -1048576 : ok  	google.golang.org/grpc/internal/transport	6.565s
```

Baseline samples (real):

```console
    recv_buffer_compaction_test.go:245: Retained heap for 262144 unread 1-byte DATA frames: compaction enabled = 531192 bytes, disabled = 19827408 bytes
```

Compaction neutered, real samples (only the heap assertions notice):

```console
    recv_buffer_compaction_test.go:248: Retained heap with compaction = 19827840 bytes, want <= 2097152 bytes (8x the unread payload)
    recv_buffer_compaction_test.go:251: Retained heap with compaction = 19827840 bytes, want at least 4x smaller than without compaction (19816592 bytes)
--- FAIL: Test (5.72s)
    --- FAIL: Test/ServerTransport_TinyDataFramesReceiveMemory (5.21s)
```

Compaction neutered **and** enabled-mode sample forced to 0 (every added test passes):

```console
    recv_buffer_compaction_test.go:245: Retained heap for 262144 unread 1-byte DATA frames: compaction enabled = 0 bytes, disabled = 19815096 bytes
--- PASS: Test (5.07s)
    --- PASS: Test/RecvBuffer_DeliversAllBytesInOrder (0.49s)
    --- PASS: Test/ServerTransport_TinyDataFramesReceiveMemory (4.57s)
--- PASS: Test (0.00s)
ok  	google.golang.org/grpc/internal/transport	6.093s
```

Negative sample (`C1_INJECT=-1048576`) with compaction neutered:

```console
ok  	google.golang.org/grpc/internal/transport	6.565s
```

Per-branch verdict: **CONFIRMED** - zero and negative samples satisfy every memory assertion even while production buffering is uncompacted; no added test bounds queue depth, buffer count or consolidation.

### C1 on [evalon/grpc-go-tr-ae31c146](https://github.com/kaitranntt-evals/grpc-go-transport-restrict-memory-overhead/tree/evalon/grpc-go-tr-ae31c146)

Memory assertions: recv_buffer_compaction_test.go:462-470 (**`uint64`**): `retained[true] > 2*payload+512KiB` -> error; `retained[true]*4 > retained[false]` -> error. All are upper bounds on the enabled-mode sample; none has a lower bound.

```console
$ bash verify/repro/c1_run.sh ~/wt/ae31c146 verify/logs/c1/ae31c146
branch HEAD: 4210b350  test file: internal/transport/recv_buffer_compaction_test.go
go test -run '^Test$/^(RecvBufferCompaction_PreservesDataAndOrder|RecvBufferCompaction_ErrorAfterCompactedData|ServerReceiveBufferCompaction_ManyTinyDataFrames|ClientReceiveBufferCompaction_ManyTinyDataFrames)$'
baseline                              : ok  	google.golang.org/grpc/internal/transport	5.924s
injection sites patched               : 1
inject 0        (solution intact)     : ok  	google.golang.org/grpc/internal/transport	6.390s
inject -1048576 (solution intact)     : FAIL	google.golang.org/grpc/internal/transport	5.947s
compaction neutered, real samples     : FAIL	google.golang.org/grpc/internal/transport	6.540s
compaction neutered + inject 0        : ok  	google.golang.org/grpc/internal/transport	8.025s
compaction neutered + inject -1048576 : FAIL	google.golang.org/grpc/internal/transport	6.817s
```

Baseline samples (real):

```console
    recv_buffer_compaction_test.go:453: Retained 274040 bytes for 262144 bytes of payload
    recv_buffer_compaction_test.go:453: Retained 19565536 bytes for 262144 bytes of payload
```

Compaction neutered, real samples (only the heap assertions notice):

```console
    recv_buffer_compaction_test.go:463: With compaction, server retained 19554936 bytes for 262144 bytes of unread payload; want at most 1048576
    recv_buffer_compaction_test.go:469: With compaction, server retained 19554936 bytes; without compaction it retained 19554768 bytes; want compaction to retain less than a quarter
--- FAIL: Test (6.51s)
    --- FAIL: Test/ServerReceiveBufferCompaction_ManyTinyDataFrames (4.11s)
```

Compaction neutered **and** enabled-mode sample forced to 0 (every added test passes):

```console
    recv_buffer_compaction_test.go:453: Retained 0 bytes for 262144 bytes of payload
    recv_buffer_compaction_test.go:453: Retained 19554528 bytes for 262144 bytes of payload
--- PASS: Test (7.00s)
    --- PASS: Test/ClientReceiveBufferCompaction_ManyTinyDataFrames (1.94s)
    --- PASS: Test/RecvBufferCompaction_ErrorAfterCompactedData (0.00s)
    --- PASS: Test/RecvBufferCompaction_PreservesDataAndOrder (0.42s)
    --- PASS: Test/ServerReceiveBufferCompaction_ManyTinyDataFrames (4.62s)
--- PASS: Test (0.01s)
ok  	google.golang.org/grpc/internal/transport	8.025s
```

Negative sample (`C1_INJECT=-1048576`): rejected on this branch only, because the delta is `uint64` and wraps:

```console
    recv_buffer_compaction_test.go:463: With compaction, server retained 18446744073708503040 bytes for 262144 bytes of unread payload; want at most 1048576
    recv_buffer_compaction_test.go:469: With compaction, server retained 18446744073708503040 bytes; without compaction it retained 19554496 bytes; want compaction to retain less than a quarter
--- FAIL: Test (5.92s)
FAIL	google.golang.org/grpc/internal/transport	5.947s
```

Per-branch verdict: **CONFIRMED** - a zero-growth sample satisfies both assertions while the production code buffers 262144 one-byte frames uncompacted, and no deterministic bound exists; the negative half of "nonpositive" does not hold here (wraps to ~1.8e19 and fails).

### C1 on [evalon/grpc-go-tr-2994fc5c](https://github.com/kaitranntt-evals/grpc-go-transport-restrict-memory-overhead/tree/evalon/grpc-go-tr-2994fc5c)

Memory assertions: recv_buffer_test.go:63-79 (signed `int64`): `enabled && retained > 8*payloadSize` -> error; ratio check skipped when `compacted == 0`. All are upper bounds on the enabled-mode sample; none has a lower bound.

```console
$ bash verify/repro/c1_run.sh ~/wt/2994fc5c verify/logs/c1/2994fc5c
branch HEAD: 6d5c045a  test file: internal/transport/recv_buffer_test.go
go test -run '^Test$/^(ReceiveBufferCompactionMemory|ReceiveBufferCompactionReads|ReceiveBufferCompactionConcurrentReads)$'
baseline                              : ok  	google.golang.org/grpc/internal/transport	2.586s
injection sites patched               : 1
inject 0        (solution intact)     : ok  	google.golang.org/grpc/internal/transport	2.583s
inject -1048576 (solution intact)     : ok  	google.golang.org/grpc/internal/transport	2.705s
compaction neutered, real samples     : FAIL	google.golang.org/grpc/internal/transport	1.800s
compaction neutered + inject 0        : ok  	google.golang.org/grpc/internal/transport	3.499s
compaction neutered + inject -1048576 : ok  	google.golang.org/grpc/internal/transport	3.087s
```

Baseline samples (real):

```console
    recv_buffer_test.go:60: 65535 unread payload bytes retain 67288 heap bytes
    recv_buffer_test.go:60: 65535 unread payload bytes retain 4984832 heap bytes
    recv_buffer_test.go:60: 65535 unread payload bytes retain 67288 heap bytes
    recv_buffer_test.go:60: 65535 unread payload bytes retain 67288 heap bytes
    recv_buffer_test.go:60: 65535 unread payload bytes retain 4973824 heap bytes
    recv_buffer_test.go:60: 65535 unread payload bytes retain 67288 heap bytes
```

Compaction neutered, real samples (only the heap assertions notice):

```console
    recv_buffer_test.go:64: compacted receive heap = 4973920 bytes, want at most 524280
    recv_buffer_test.go:64: compacted receive heap = 4973824 bytes, want at most 524280
    recv_buffer_test.go:78: compaction retained 4973824 bytes versus 4973824 disabled, want at least an 8x reduction
    recv_buffer_test.go:64: compacted receive heap = 4973824 bytes, want at most 524280
    recv_buffer_test.go:64: compacted receive heap = 4973920 bytes, want at most 524280
    recv_buffer_test.go:78: compaction retained 4973920 bytes versus 4973856 disabled, want at least an 8x reduction
--- FAIL: Test (1.78s)
    --- FAIL: Test/ReceiveBufferCompactionMemory (1.63s)
```

Compaction neutered **and** enabled-mode sample forced to 0 (every added test passes):

```console
    recv_buffer_test.go:60: 65535 unread payload bytes retain 0 heap bytes
    recv_buffer_test.go:60: 65535 unread payload bytes retain 4979424 heap bytes
    recv_buffer_test.go:60: 65535 unread payload bytes retain 0 heap bytes
    recv_buffer_test.go:60: 65535 unread payload bytes retain 0 heap bytes
    recv_buffer_test.go:60: 65535 unread payload bytes retain 4973824 heap bytes
    recv_buffer_test.go:60: 65535 unread payload bytes retain 0 heap bytes
--- PASS: Test (2.47s)
    --- PASS: Test/ReceiveBufferCompactionConcurrentReads (0.20s)
    --- PASS: Test/ReceiveBufferCompactionMemory (2.24s)
    --- PASS: Test/ReceiveBufferCompactionReads (0.01s)
--- PASS: Test (0.00s)
ok  	google.golang.org/grpc/internal/transport	3.499s
```

Negative sample (`C1_INJECT=-1048576`) with compaction neutered:

```console
ok  	google.golang.org/grpc/internal/transport	3.087s
```

Per-branch verdict: **CONFIRMED** - zero and negative samples satisfy every memory assertion even while production buffering is uncompacted; no added test bounds queue depth, buffer count or consolidation.

### C1 on [evalon/grpc-go-tr-d2e82310](https://github.com/kaitranntt-evals/grpc-go-transport-restrict-memory-overhead/tree/evalon/grpc-go-tr-d2e82310)

Memory assertions: recv_buffer_test.go:123-131 (signed `int64`): `retained > 4*totalBytes` -> error (enabled); `retained < 16*totalBytes` -> error (disabled). All are upper bounds on the enabled-mode sample; none has a lower bound.

```console
$ bash verify/repro/c1_run.sh ~/wt/d2e82310 verify/logs/c1/d2e82310
branch HEAD: fb184a6f  test file: internal/transport/recv_buffer_test.go
go test -run '^Test$/^(ReceiveBufferCompactionMemory|ReceiveBufferCompactionReads|ReceiveBufferCompactionConcurrent)$'
baseline                              : ok  	google.golang.org/grpc/internal/transport	2.452s
injection sites patched               : 1
inject 0        (solution intact)     : ok  	google.golang.org/grpc/internal/transport	2.636s
inject -1048576 (solution intact)     : ok  	google.golang.org/grpc/internal/transport	2.843s
compaction neutered, real samples     : FAIL	google.golang.org/grpc/internal/transport	3.135s
compaction neutered + inject 0        : ok  	google.golang.org/grpc/internal/transport	4.096s
compaction neutered + inject -1048576 : ok  	google.golang.org/grpc/internal/transport	3.454s
```

Baseline samples (real):

```console
    recv_buffer_test.go:122: compaction=true: retained 266024 heap bytes for 262144 unread payload bytes
    recv_buffer_test.go:122: compaction=true: retained 265624 heap bytes for 262144 unread payload bytes
```

Compaction neutered, real samples (only the heap assertions notice):

```console
    recv_buffer_test.go:127: retained heap = 19985784 bytes, want <= 1048576
    recv_buffer_test.go:127: retained heap = 19991832 bytes, want <= 1048576
--- FAIL: Test (3.11s)
    --- FAIL: Test/ReceiveBufferCompactionMemory (2.89s)
        --- FAIL: Test/ReceiveBufferCompactionMemory/client (1.17s)
        --- FAIL: Test/ReceiveBufferCompactionMemory/server (1.70s)
```

Compaction neutered **and** enabled-mode sample forced to 0 (every added test passes):

```console
    recv_buffer_test.go:122: compaction=true: retained 0 heap bytes for 262144 unread payload bytes
    recv_buffer_test.go:122: compaction=true: retained 0 heap bytes for 262144 unread payload bytes
--- PASS: Test (3.07s)
    --- PASS: Test/ReceiveBufferCompactionConcurrent (0.15s)
    --- PASS: Test/ReceiveBufferCompactionMemory (2.90s)
    --- PASS: Test/ReceiveBufferCompactionReads (0.01s)
--- PASS: Test (0.00s)
ok  	google.golang.org/grpc/internal/transport	4.096s
```

Negative sample (`C1_INJECT=-1048576`) with compaction neutered:

```console
ok  	google.golang.org/grpc/internal/transport	3.454s
```

Per-branch verdict: **CONFIRMED** - zero and negative samples satisfy every memory assertion even while production buffering is uncompacted; no added test bounds queue depth, buffer count or consolidation.

### C1 on [evalon/grpc-go-tr-d0aa7e9e](https://github.com/kaitranntt-evals/grpc-go-transport-restrict-memory-overhead/tree/evalon/grpc-go-tr-d0aa7e9e)

Memory assertions: recv_buffer_test.go:125-132 (signed `int64`): `retained > 4*payloadSize+256KiB` -> error (enabled). All are upper bounds on the enabled-mode sample; none has a lower bound.

```console
$ bash verify/repro/c1_run.sh ~/wt/d0aa7e9e verify/logs/c1/d0aa7e9e
branch HEAD: 66ae7d75  test file: internal/transport/recv_buffer_test.go
go test -run '^Test$/^(ReceiveBufferMemory|ReceiveBufferCompaction|ReceiveBufferCompactionOwnership|ReceiveBufferCompactionConcurrent)$'
baseline                              : ok  	google.golang.org/grpc/internal/transport	2.919s
injection sites patched               : 1
inject 0        (solution intact)     : ok  	google.golang.org/grpc/internal/transport	3.041s
inject -1048576 (solution intact)     : ok  	google.golang.org/grpc/internal/transport	2.689s
compaction neutered, real samples     : FAIL	google.golang.org/grpc/internal/transport	2.719s
compaction neutered + inject 0        : ok  	google.golang.org/grpc/internal/transport	4.078s
compaction neutered + inject -1048576 : ok  	google.golang.org/grpc/internal/transport	3.726s
```

Baseline samples (real):

```console
    recv_buffer_test.go:124: compaction=true: retained 279024 bytes for 262140 unread payload bytes
    recv_buffer_test.go:124: compaction=true: retained 279024 bytes for 262140 unread payload bytes
```

Compaction neutered, real samples (only the heap assertions notice):

```console
    recv_buffer_test.go:128: retained heap = 19891432 bytes, want at most 1310704
    recv_buffer_test.go:128: retained heap = 19891432 bytes, want at most 1310704
--- FAIL: Test (2.69s)
    --- FAIL: Test/ReceiveBufferMemory (2.49s)
        --- FAIL: Test/ReceiveBufferMemory/server=false (1.18s)
        --- FAIL: Test/ReceiveBufferMemory/server=true (1.32s)
```

Compaction neutered **and** enabled-mode sample forced to 0 (every added test passes):

```console
    recv_buffer_test.go:124: compaction=true: retained 0 bytes for 262140 unread payload bytes
    recv_buffer_test.go:124: compaction=true: retained 0 bytes for 262140 unread payload bytes
--- PASS: Test (3.06s)
    --- PASS: Test/ReceiveBufferCompaction (0.00s)
    --- PASS: Test/ReceiveBufferCompactionConcurrent (0.17s)
    --- PASS: Test/ReceiveBufferCompactionOwnership (0.00s)
    --- PASS: Test/ReceiveBufferMemory (2.87s)
--- PASS: Test (0.00s)
ok  	google.golang.org/grpc/internal/transport	4.078s
```

Negative sample (`C1_INJECT=-1048576`) with compaction neutered:

```console
ok  	google.golang.org/grpc/internal/transport	3.726s
```

Per-branch verdict: **CONFIRMED** - zero and negative samples satisfy every memory assertion even while production buffering is uncompacted; no added test bounds queue depth, buffer count or consolidation.

### C1 on [evalon/grpc-go-tr-4c52c410](https://github.com/kaitranntt-evals/grpc-go-transport-restrict-memory-overhead/tree/evalon/grpc-go-tr-4c52c410)

Memory assertions: recv_buffer_test.go:144-149, 184-186 (signed `int64`): `tc.compact && heap > 4*payloadSize+128KiB` -> error; `retained[1]*8 >= retained[0]` -> error. All are upper bounds on the enabled-mode sample; none has a lower bound.

```console
$ bash verify/repro/c1_run.sh ~/wt/4c52c410 verify/logs/c1/4c52c410
branch HEAD: e78fe589  test file: internal/transport/recv_buffer_test.go
go test -run '^Test$/^(ReceiveBufferCompactionMemory|ReceiveBufferCompactionOwnership)$'
baseline                              : ok  	google.golang.org/grpc/internal/transport	2.783s
injection sites patched               : 1
inject 0        (solution intact)     : ok  	google.golang.org/grpc/internal/transport	2.826s
inject -1048576 (solution intact)     : ok  	google.golang.org/grpc/internal/transport	3.116s
compaction neutered, real samples     : FAIL	google.golang.org/grpc/internal/transport	2.265s
compaction neutered + inject 0        : ok  	google.golang.org/grpc/internal/transport	3.286s
compaction neutered + inject -1048576 : ok  	google.golang.org/grpc/internal/transport	3.262s
```

Baseline samples (real):

```console
    recv_buffer_test.go:143: retained heap for 65532 unread payload bytes: 4993032 bytes
    recv_buffer_test.go:143: retained heap for 65532 unread payload bytes: 67912 bytes
    recv_buffer_test.go:143: retained heap for 65532 unread payload bytes: 67912 bytes
    recv_buffer_test.go:143: retained heap for 65532 unread payload bytes: 4981528 bytes
    recv_buffer_test.go:143: retained heap for 65532 unread payload bytes: 67912 bytes
    recv_buffer_test.go:143: retained heap for 65532 unread payload bytes: 67912 bytes
```

Compaction neutered, real samples (only the heap assertions notice):

```console
    recv_buffer_test.go:145: compacted receive heap = 4981896, want at most four times payload plus 128 KiB
    recv_buffer_test.go:145: compacted receive heap = 4982088, want at most four times payload plus 128 KiB
    recv_buffer_test.go:185: compaction retained 4981896 bytes vs 4998504 without compaction, want at least 8x reduction
    recv_buffer_test.go:145: compacted receive heap = 4981992, want at most four times payload plus 128 KiB
    recv_buffer_test.go:145: compacted receive heap = 4981896, want at most four times payload plus 128 KiB
    recv_buffer_test.go:185: compaction retained 4981992 bytes vs 4982184 without compaction, want at least 8x reduction
--- FAIL: Test (2.24s)
    --- FAIL: Test/ReceiveBufferCompactionMemory (1.92s)
```

Compaction neutered **and** enabled-mode sample forced to 0 (every added test passes):

```console
    recv_buffer_test.go:143: retained heap for 65532 unread payload bytes: 5004040 bytes
    recv_buffer_test.go:143: retained heap for 65532 unread payload bytes: 0 bytes
    recv_buffer_test.go:143: retained heap for 65532 unread payload bytes: 0 bytes
    recv_buffer_test.go:143: retained heap for 65532 unread payload bytes: 4982088 bytes
    recv_buffer_test.go:143: retained heap for 65532 unread payload bytes: 0 bytes
    recv_buffer_test.go:143: retained heap for 65532 unread payload bytes: 0 bytes
--- PASS: Test (2.26s)
    --- PASS: Test/ReceiveBufferCompactionMemory (1.93s)
    --- PASS: Test/ReceiveBufferCompactionOwnership (0.31s)
--- PASS: Test (0.00s)
ok  	google.golang.org/grpc/internal/transport	3.286s
```

Negative sample (`C1_INJECT=-1048576`) with compaction neutered:

```console
ok  	google.golang.org/grpc/internal/transport	3.262s
```

Per-branch verdict: **CONFIRMED** - zero and negative samples satisfy every memory assertion even while production buffering is uncompacted; no added test bounds queue depth, buffer count or consolidation.

### C1 on [evalon/grpc-go-tr-72e9069b](https://github.com/kaitranntt-evals/grpc-go-transport-restrict-memory-overhead/tree/evalon/grpc-go-tr-72e9069b)

Memory assertions: recv_buffer_test.go:129-134 (signed `int64`): `wantCompaction && retained > 4*payloadSize` -> error. All are upper bounds on the enabled-mode sample; none has a lower bound.

```console
$ bash verify/repro/c1_run.sh ~/wt/72e9069b verify/logs/c1/72e9069b
branch HEAD: d7f89284  test file: internal/transport/recv_buffer_test.go
go test -run '^Test$/^(ReceiveBufferCompactionMemory|ReceiveBufferCompactionOwnership|ReceiveBufferCompactionReaderOwnership|ReceiveBufferCompactionConcurrent)$'
baseline                              : ok  	google.golang.org/grpc/internal/transport	2.924s
injection sites patched               : 1
inject 0        (solution intact)     : ok  	google.golang.org/grpc/internal/transport	3.041s
inject -1048576 (solution intact)     : ok  	google.golang.org/grpc/internal/transport	2.906s
compaction neutered, real samples     : FAIL	google.golang.org/grpc/internal/transport	2.788s
compaction neutered + inject 0        : ok  	google.golang.org/grpc/internal/transport	4.014s
compaction neutered + inject -1048576 : ok  	google.golang.org/grpc/internal/transport	3.736s
```

Baseline samples (real):

```console
    recv_buffer_test.go:128: retained heap = 134528 bytes for 131072 unread payload bytes
    recv_buffer_test.go:128: retained heap = 10005568 bytes for 131072 unread payload bytes
    recv_buffer_test.go:128: retained heap = 134528 bytes for 131072 unread payload bytes
    recv_buffer_test.go:128: retained heap = 10000064 bytes for 131072 unread payload bytes
```

Compaction neutered, real samples (only the heap assertions notice):

```console
    recv_buffer_test.go:130: compacted receive heap = 9994656, want <= 524288
    recv_buffer_test.go:130: compacted receive heap = 9994560, want <= 524288
--- FAIL: Test (2.76s)
    --- FAIL: Test/ReceiveBufferCompactionMemory (2.60s)
        --- FAIL: Test/ReceiveBufferCompactionMemory/server=false/disable=false (0.62s)
        --- FAIL: Test/ReceiveBufferCompactionMemory/server=true/disable=false (0.77s)
```

Compaction neutered **and** enabled-mode sample forced to 0 (every added test passes):

```console
    recv_buffer_test.go:128: retained heap = 0 bytes for 131072 unread payload bytes
    recv_buffer_test.go:128: retained heap = 9994560 bytes for 131072 unread payload bytes
    recv_buffer_test.go:128: retained heap = 0 bytes for 131072 unread payload bytes
    recv_buffer_test.go:128: retained heap = 9994656 bytes for 131072 unread payload bytes
--- PASS: Test (3.00s)
    --- PASS: Test/ReceiveBufferCompactionConcurrent (0.10s)
    --- PASS: Test/ReceiveBufferCompactionMemory (2.87s)
    --- PASS: Test/ReceiveBufferCompactionOwnership (0.01s)
    --- PASS: Test/ReceiveBufferCompactionReaderOwnership (0.00s)
--- PASS: Test (0.00s)
ok  	google.golang.org/grpc/internal/transport	4.014s
```

Negative sample (`C1_INJECT=-1048576`) with compaction neutered:

```console
ok  	google.golang.org/grpc/internal/transport	3.736s
```

Per-branch verdict: **CONFIRMED** - zero and negative samples satisfy every memory assertion even while production buffering is uncompacted; no added test bounds queue depth, buffer count or consolidation.

### C1 on [evalon/grpc-go-tr-3e551c7b](https://github.com/kaitranntt-evals/grpc-go-transport-restrict-memory-overhead/tree/evalon/grpc-go-tr-3e551c7b)

Memory assertions: recv_buffer_test.go:125-133 (signed `int64`): `retained > 4*unread` -> error (enabled). All are upper bounds on the enabled-mode sample; none has a lower bound.

```console
$ bash verify/repro/c1_run.sh ~/wt/3e551c7b verify/logs/c1/3e551c7b
branch HEAD: 9ff9c4f8  test file: internal/transport/recv_buffer_test.go
go test -run '^Test$/^(ReceiveBufferTinyFrames|RecvBufferCompactionOrdering|RecvBufferCompactionConcurrentReader)$'
baseline                              : ok  	google.golang.org/grpc/internal/transport	3.520s
injection sites patched               : 1
inject 0        (solution intact)     : ok  	google.golang.org/grpc/internal/transport	3.633s
inject -1048576 (solution intact)     : ok  	google.golang.org/grpc/internal/transport	3.605s
compaction neutered, real samples     : FAIL	google.golang.org/grpc/internal/transport	5.910s
compaction neutered + inject 0        : ok  	google.golang.org/grpc/internal/transport	5.471s
compaction neutered + inject -1048576 : ok  	google.golang.org/grpc/internal/transport	5.830s
```

Baseline samples (real):

```console
    recv_buffer_test.go:124: 524280 one-byte DATA frames: 524280 unread bytes, 540152 retained heap bytes (1.03x)
    recv_buffer_test.go:124: 524280 one-byte DATA frames: 524280 unread bytes, 534744 retained heap bytes (1.02x)
```

Compaction neutered, real samples (only the heap assertions notice):

```console
    recv_buffer_test.go:129: retained heap = 39793512, want at most 2097120 bytes
    recv_buffer_test.go:129: retained heap = 39785880, want at most 2097120 bytes
--- FAIL: Test (5.88s)
    --- FAIL: Test/ReceiveBufferTinyFrames (5.83s)
        --- FAIL: Test/ReceiveBufferTinyFrames/client (2.70s)
        --- FAIL: Test/ReceiveBufferTinyFrames/server (3.13s)
```

Compaction neutered **and** enabled-mode sample forced to 0 (every added test passes):

```console
    recv_buffer_test.go:124: 524280 one-byte DATA frames: 524280 unread bytes, 0 retained heap bytes (0.00x)
    recv_buffer_test.go:124: 524280 one-byte DATA frames: 524280 unread bytes, 0 retained heap bytes (0.00x)
--- PASS: Test (4.44s)
    --- PASS: Test/ReceiveBufferTinyFrames (4.39s)
    --- PASS: Test/RecvBufferCompactionConcurrentReader (0.02s)
    --- PASS: Test/RecvBufferCompactionOrdering (0.01s)
--- PASS: Test (0.00s)
ok  	google.golang.org/grpc/internal/transport	5.471s
```

Negative sample (`C1_INJECT=-1048576`) with compaction neutered:

```console
ok  	google.golang.org/grpc/internal/transport	5.830s
```

Per-branch verdict: **CONFIRMED** - zero and negative samples satisfy every memory assertion even while production buffering is uncompacted; no added test bounds queue depth, buffer count or consolidation.

### C1 on [evalon/grpc-go-tr-17011fad](https://github.com/kaitranntt-evals/grpc-go-transport-restrict-memory-overhead/tree/evalon/grpc-go-tr-17011fad)

Memory assertions: recv_buffer_test.go:57-66 (signed `int64`): `configured > disabled/8 || configured > 4*payloadSize` -> error. All are upper bounds on the enabled-mode sample; none has a lower bound.

```console
$ bash verify/repro/c1_run.sh ~/wt/17011fad verify/logs/c1/17011fad
branch HEAD: 7ed96ec1  test file: internal/transport/recv_buffer_test.go
go test -run '^Test$/^(ReceiveBufferTinyFrames|ReceiveBufferCompactionOwnership|ReceiveBufferCompactionConcurrent)$'
baseline                              : ok  	google.golang.org/grpc/internal/transport	2.370s
injection sites patched               : 1
inject 0        (solution intact)     : ok  	google.golang.org/grpc/internal/transport	2.040s
inject -1048576 (solution intact)     : ok  	google.golang.org/grpc/internal/transport	2.131s
compaction neutered, real samples     : FAIL	google.golang.org/grpc/internal/transport	1.659s
compaction neutered + inject 0        : ok  	google.golang.org/grpc/internal/transport	2.354s
compaction neutered + inject -1048576 : ok  	google.golang.org/grpc/internal/transport	2.409s
```

Baseline samples (real):

```console
    recv_buffer_test.go:56: retained heap for 64000 unread bytes: configured=66912, disabled=4859384
    recv_buffer_test.go:56: retained heap for 64000 unread bytes: configured=66928, disabled=4853880
```

Compaction neutered, real samples (only the heap assertions notice):

```console
    recv_buffer_test.go:65: compaction retained 4853992 bytes, want <= 256000 and at least 8x less than disabled (4853880)
    recv_buffer_test.go:65: compaction retained 4853976 bytes, want <= 256000 and at least 8x less than disabled (4853976)
--- FAIL: Test (1.64s)
    --- FAIL: Test/ReceiveBufferTinyFrames (1.42s)
        --- FAIL: Test/ReceiveBufferTinyFrames/client (0.71s)
        --- FAIL: Test/ReceiveBufferTinyFrames/server (0.70s)
```

Compaction neutered **and** enabled-mode sample forced to 0 (every added test passes):

```console
    recv_buffer_test.go:56: retained heap for 64000 unread bytes: configured=0, disabled=4853896
    recv_buffer_test.go:56: retained heap for 64000 unread bytes: configured=0, disabled=4853976
--- PASS: Test (1.32s)
    --- PASS: Test/ReceiveBufferCompactionConcurrent (0.16s)
    --- PASS: Test/ReceiveBufferCompactionOwnership (0.02s)
    --- PASS: Test/ReceiveBufferTinyFrames (1.11s)
--- PASS: Test (0.00s)
ok  	google.golang.org/grpc/internal/transport	2.354s
```

Negative sample (`C1_INJECT=-1048576`) with compaction neutered:

```console
ok  	google.golang.org/grpc/internal/transport	2.409s
```

Per-branch verdict: **CONFIRMED** - zero and negative samples satisfy every memory assertion even while production buffering is uncompacted; no added test bounds queue depth, buffer count or consolidation.

**Impact reasoning (C1).** On all ten branches the only evidence of "memory improved" is a signed (or, on one branch, unsigned) `HeapAlloc` delta checked against upper bounds. Real samples today are comfortably positive (60 KiB - 540 KiB, see baselines), so the tests are not flaky and do catch a plain removal of compaction (the "neutered, real samples" row fails everywhere). The gap is that a sample of 0 or below - which is what a GC that frees more than the workload allocated between the two `ReadMemStats` calls produces - is treated as a pass instead of as "measurement inconclusive", and nothing deterministic (e.g. `len(b.backlog)`, number of delivered buffers) backs it up. The combined mutation shows this concretely: with compaction disabled in production and a nonpositive sample, the whole added suite is green.

## C2

**Claim:** an added receive-buffer test performs a deferred writer-goroutine join that can remain blocked indefinitely despite cancellation. Branch: [evalon/grpc-go-tr-8b3e4b01](https://github.com/kaitranntt-evals/grpc-go-transport-restrict-memory-overhead/tree/evalon/grpc-go-tr-8b3e4b01). **Verdict: CONFIRMED** (latent: needs `recvBuffer.put` to block).

The join is in `TestReceiveBufferCompactionConcurrent`, `internal/transport/recv_buffer_test.go:240-257`:

```go
	done := make(chan struct{})
	go func() {
		defer close(done)
		sizes := []int{1, 7, http2MaxFrameLen, 2, 4096}
		for offset, frame := 3, 0; offset < len(want); frame++ {
			if ctx.Err() != nil {
				return
			}
			end := min(offset+sizes[frame%len(sizes)], len(want))
			queue.put(recvMsg{buffer: mem.Copy(want[offset:end], mem.DefaultBufferPool())})
			offset = end
		}
		queue.put(recvMsg{err: io.EOF})
	}()
	defer func() {
		cancel()
		<-done
	}()
```

`<-done` is a bare receive: no `select`, no timer. Cancellation is only observed by the writer *between* `put` calls; a writer parked inside `put` (on `b.mu`) never sees it. No helper adds a bound: `internal/grpctest` only has a 10 s leak-check timer in `Teardown`, which runs after the test function returns.

```console
$ grep -n "WithTimeout\|time\." internal/grpctest/grpctest.go
69:	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
```

Repro (`verify/repro/c2_join_test.go`): the setup, writer goroutine and deferred join are copied verbatim from the test; the reader loop is replaced by `t.Fatal`. In the "stalled" variant the test goroutine holds `queue.mu` so the writer's first `put` cannot make progress.

```sh
cd ~/wt/8b3e4b01 && cp <repo>/verify/repro/c2_join_test.go internal/transport/
go test -v -run '^Test$/^ReceiveBufferCompactionConcurrent$' google.golang.org/grpc/internal/transport -race -count=3   # original, unmodified
go test -tags verify_audit -v -run '^TestC2_JoinWithHealthyWriter$' google.golang.org/grpc/internal/transport -count=1 -timeout 40s
time go test -tags verify_audit -v -run '^TestC2_JoinWithStalledWriter$' google.golang.org/grpc/internal/transport -count=1 -timeout 40s
```

Original test, unmodified (healthy code: passes, join returns):

```console
--- PASS: Test (0.04s)
    --- PASS: Test/ReceiveBufferCompactionConcurrent (0.03s)
--- PASS: Test (0.00s)
--- PASS: Test (0.02s)
    --- PASS: Test/ReceiveBufferCompactionConcurrent (0.02s)
--- PASS: Test (0.00s)
--- PASS: Test (0.02s)
    --- PASS: Test/ReceiveBufferCompactionConcurrent (0.01s)
--- PASS: Test (0.00s)
PASS
ok  	google.golang.org/grpc/internal/transport	1.119s
```

Control - healthy writer, reader fails: join returns immediately after `cancel()`:

```console
    c2_join_test.go:68: C2: simulated reader-side failure (stands in for any t.Fatal in the read loop)
    c2_join_test.go:62: C2: cancel() called at +100ms; now waiting on <-done (stallWriter=false)
    c2_join_test.go:64: C2: join returned at +100ms
--- FAIL: TestC2_JoinWithHealthyWriter (0.10s)
FAIL
FAIL	google.golang.org/grpc/internal/transport	0.106s
FAIL
```

Stalled writer: `cancel()` is called at +101 ms, the join never returns, and the process is only ended by the global `go test -timeout` (40 s here; 10 min by default):

```console
    c2_join_test.go:68: C2: simulated reader-side failure (stands in for any t.Fatal in the read loop)
    c2_join_test.go:62: C2: cancel() called at +101ms; now waiting on <-done (stallWriter=true)
panic: test timed out after 40s
		TestC2_JoinWithStalledWriter (40s)
goroutine 1 [chan receive]:
goroutine 8 [chan receive]:
google.golang.org/grpc/internal/transport.c2Body.func2()
goroutine 9 [sync.Mutex.Lock]:
google.golang.org/grpc/internal/transport.(*recvBuffer).put(0xc000051840, {{0xcf5020?, 0xc0000129f0?}, {0x0?, 0x0?}})
google.golang.org/grpc/internal/transport.c2Body.func1()
FAIL	google.golang.org/grpc/internal/transport	40.080s
FAIL
real	0m40.529s
```

(goroutine 8 is the test goroutine parked in the deferred `<-done` after `t.Fatal`; goroutine 9 is the writer parked in `recvBuffer.put` on `sync.Mutex.Lock`. Full dump: `verify/logs/c2_stalled_writer_full.log`.)

**Impact reasoning (C2).** With the solution as shipped, `put` never blocks for long, so the join returns and the test passes (3/3 above). The defect is in how the test fails: if a regression makes `put` block while the reader side gives up (the reader's own `ctx` does time out after 10 s and it then calls `t.Fatal`), the deferred join turns a clean 10 s failure into a hang until the package-wide `go test` timeout, taking every other `internal/transport` test result in that process with it. That is exactly the class of bug (locking around the mutable backlog tail) this `-race` test exists to catch.

## C3

**Claim:** default production receive buffering does not consolidate 1,025 queued one-byte payloads behind an occupied delivery channel. Branch: [evalon/grpc-go-tr-9253f1d4](https://github.com/kaitranntt-evals/grpc-go-transport-restrict-memory-overhead/tree/evalon/grpc-go-tr-9253f1d4). **Verdict: REFUTED.**

The eval fixture fails on this branch, but only because of how the fixture builds its buffer. `initRecvBufferForTest` probes for `initWithPool`/`init(bool, pool)`/`init(pool)`/`enableCompaction` signatures; this branch keeps `recvBuffer.init()` and enables compaction with a field assignment in the production constructors (`s.Stream.buf.compact = envconfig.EnableReceiveBufferCompaction` in `http2Client.newStream` and `http2Server.operateHeaders`), so the fixture ends up with `init()` only, i.e. a buffer with compaction off. The fixture's own `ProductionStream` subtest, which goes through `client.newStream`, passes.

```sh
cd ~/wt/9253f1d4 && cp $FIXTURE internal/transport/eval_recv_buffer_compaction_test.go
go test -v -run '^TestEval_' google.golang.org/grpc/internal/transport -race -count=1
```

```console
    eval_recv_buffer_compaction_test.go:150: Got backlog length 1025 after compaction, want <= 64
--- FAIL: TestEval_RecvBufferCompaction (0.00s)
    eval_recv_buffer_compaction_test.go:189: ComponentBuffer: got backlog length 1025, want <= 64 (compaction enabled)
--- FAIL: TestEval_RecvBufferCompactionDisabled (0.01s)
    --- PASS: TestEval_RecvBufferCompactionDisabled/ProductionStream (0.00s)
    --- FAIL: TestEval_RecvBufferCompactionDisabled/ComponentBuffer (0.00s)
--- PASS: TestEval_RecvBufferCompactionSkippedLargeBuffer (0.03s)
    --- PASS: TestEval_RecvBufferCompactionSkippedLargeBuffer/PoolOwnership_SliceGrowth (0.00s)
--- PASS: TestEval_RecvBufferErrorResetSafety (0.00s)
    eval_recv_buffer_compaction_test.go:416: Got backlog length 1073 after mixed frame compaction, want <= 134
--- FAIL: TestEval_RecvBufferCompaction_MixedFrames (0.00s)
    eval_recv_buffer_compaction_test.go:496: Cycle 0: backlog length 1123 exceeded bound 512
--- FAIL: TestEval_RecvBufferCompaction_MultiCycleMemoryBound (0.02s)
    --- FAIL: TestEval_RecvBufferCompaction_MultiCycleMemoryBound/MultiCycleBursts (0.01s)
    --- PASS: TestEval_RecvBufferCompaction_MultiCycleMemoryBound/IncrementalMessageAssembly (0.01s)
    eval_recv_buffer_compaction_test.go:627: Compaction destination buffer was never acquired from the configured buffer pool
--- FAIL: TestEval_RecvBufferConfiguredPoolAcquisition (0.00s)
    --- FAIL: TestEval_RecvBufferConfiguredPoolAcquisition/UnpooledConsolidation (0.00s)
    --- PASS: TestEval_RecvBufferConfiguredPoolAcquisition/SliceGrowthPoolOwnership (0.00s)
    --- PASS: TestEval_RecvBufferConfiguredPoolAcquisition/ExactCapacitySmallDestination (0.00s)
FAIL
FAIL	google.golang.org/grpc/internal/transport	0.081s
FAIL
```

Same fixture, same workloads and bounds, with the buffer obtained from the production constructor instead (`verify/probes/c345_prod_construction_fixture_test.go`: every `b := &recvBuffer{}; initRecvBufferForTest(b, pool)` replaced by `b := &(&http2Client{bufferPool: pool}).newStream(context.Background(), &CallHdr{}, nil).buf`; `Eval`->`Prod` renames; gofmt; nothing else):

```sh
rm internal/transport/eval_recv_buffer_compaction_test.go
cp <repo>/verify/probes/c345_prod_construction_fixture_test.go <repo>/verify/probes/c345_measure_test.go internal/transport/
go test -tags verify_audit -v -run '^TestProd_' google.golang.org/grpc/internal/transport -race -count=1
go test -tags verify_audit -v -run '^TestC345_' google.golang.org/grpc/internal/transport -race -count=1
```

```console
--- PASS: TestProd_RecvBufferCompaction (0.00s)
--- PASS: TestProd_RecvBufferCompactionDisabled (0.00s)
    --- PASS: TestProd_RecvBufferCompactionDisabled/ProductionStream (0.00s)
    --- PASS: TestProd_RecvBufferCompactionDisabled/ComponentBuffer (0.00s)
--- PASS: TestProd_RecvBufferCompactionSkippedLargeBuffer (0.02s)
    --- PASS: TestProd_RecvBufferCompactionSkippedLargeBuffer/PoolOwnership_SliceGrowth (0.00s)
--- PASS: TestProd_RecvBufferErrorResetSafety (0.00s)
--- PASS: TestProd_RecvBufferCompaction_MixedFrames (0.00s)
--- PASS: TestProd_RecvBufferCompaction_MultiCycleMemoryBound (0.01s)
    --- PASS: TestProd_RecvBufferCompaction_MultiCycleMemoryBound/MultiCycleBursts (0.00s)
    --- PASS: TestProd_RecvBufferCompaction_MultiCycleMemoryBound/IncrementalMessageAssembly (0.01s)
    c345_prod_construction_fixture_test.go:624: Compaction destination buffer was never acquired from the configured buffer pool
--- FAIL: TestProd_RecvBufferConfiguredPoolAcquisition (0.00s)
    --- FAIL: TestProd_RecvBufferConfiguredPoolAcquisition/UnpooledConsolidation (0.00s)
    --- PASS: TestProd_RecvBufferConfiguredPoolAcquisition/SliceGrowthPoolOwnership (0.00s)
    --- PASS: TestProd_RecvBufferConfiguredPoolAcquisition/ExactCapacitySmallDestination (0.00s)
FAIL
FAIL	google.golang.org/grpc/internal/transport	0.058s
FAIL
```

(The one remaining failure, `UnpooledConsolidation`, asserts that the destination comes from the configured pool; this branch allocates the tail with `make`. It is not a consolidation or memory-bound check and is outside C3/C4/C5.)

Direct measurement (`verify/probes/c345_measure_test.go`, `TestC345_C3_...`): 1,026 one-byte payloads are `put` on a buffer built by `http2Client.newStream` and on one built by `http2Server.operateHeaders` (a real server transport accepting a real client stream). The first payload occupies `b.c`; the remaining 1,025 are inspected:

```console
    c345_measure_test.go:117: C3 client(newStream): chan=1 len(backlog)=0 queuedBuffers(backlog+tail)=1 queuedPayload=1025 retainedCap=16384 (fixture bound: backlog<=64)
    c345_measure_test.go:117: C3 server(operateHeaders): chan=1 len(backlog)=0 queuedBuffers(backlog+tail)=1 queuedPayload=1025 retainedCap=16384 (fixture bound: backlog<=64)
--- PASS: TestC345_C3_1025OneByteBehindOccupiedChannel (0.00s)
```

1,025 payload bytes sit in a single 16 KiB tail buffer (`queuedBuffers=1`, `len(backlog)=0`), and are drained in order (the test compares bytes). They are consolidated.

Scope note: the third production constructor, `serverHandlerTransport.HandleStreams` (the `grpc.Server.ServeHTTP` path), does not enable compaction on this branch. That is the subject of C9 and is recorded there; the measurements in this section are for the `http2Client` and `http2Server` transports.

## C4

**Claim:** default production receive buffering exceeds the specified compaction bounds for a workload of 1-7-byte frames or a workload alternating small frames with 2-KiB frames. Branch: [evalon/grpc-go-tr-9253f1d4](https://github.com/kaitranntt-evals/grpc-go-transport-restrict-memory-overhead/tree/evalon/grpc-go-tr-9253f1d4). **Verdict: REFUTED.**

"Specified bounds" are the fixture's (`TestEval_RecvBufferCompaction_MixedFrames`): `len(backlog) <= numMessages/8` (= 134) for 1,074 frames of 1-7 bytes, and retained backlog capacity `<= 4*payload + 64 KiB` (= 262,248) for 2x1 B + 24x(1 B, 2 KiB).

The eval fixture fails on this branch, but only because of how the fixture builds its buffer. `initRecvBufferForTest` probes for `initWithPool`/`init(bool, pool)`/`init(pool)`/`enableCompaction` signatures; this branch keeps `recvBuffer.init()` and enables compaction with a field assignment in the production constructors (`s.Stream.buf.compact = envconfig.EnableReceiveBufferCompaction` in `http2Client.newStream` and `http2Server.operateHeaders`), so the fixture ends up with `init()` only, i.e. a buffer with compaction off. The fixture's own `ProductionStream` subtest, which goes through `client.newStream`, passes.

```sh
cd ~/wt/9253f1d4 && cp $FIXTURE internal/transport/eval_recv_buffer_compaction_test.go
go test -v -run '^TestEval_' google.golang.org/grpc/internal/transport -race -count=1
```

```console
    eval_recv_buffer_compaction_test.go:150: Got backlog length 1025 after compaction, want <= 64
--- FAIL: TestEval_RecvBufferCompaction (0.00s)
    eval_recv_buffer_compaction_test.go:189: ComponentBuffer: got backlog length 1025, want <= 64 (compaction enabled)
--- FAIL: TestEval_RecvBufferCompactionDisabled (0.01s)
    --- PASS: TestEval_RecvBufferCompactionDisabled/ProductionStream (0.00s)
    --- FAIL: TestEval_RecvBufferCompactionDisabled/ComponentBuffer (0.00s)
--- PASS: TestEval_RecvBufferCompactionSkippedLargeBuffer (0.03s)
    --- PASS: TestEval_RecvBufferCompactionSkippedLargeBuffer/PoolOwnership_SliceGrowth (0.00s)
--- PASS: TestEval_RecvBufferErrorResetSafety (0.00s)
    eval_recv_buffer_compaction_test.go:416: Got backlog length 1073 after mixed frame compaction, want <= 134
--- FAIL: TestEval_RecvBufferCompaction_MixedFrames (0.00s)
    eval_recv_buffer_compaction_test.go:496: Cycle 0: backlog length 1123 exceeded bound 512
--- FAIL: TestEval_RecvBufferCompaction_MultiCycleMemoryBound (0.02s)
    --- FAIL: TestEval_RecvBufferCompaction_MultiCycleMemoryBound/MultiCycleBursts (0.01s)
    --- PASS: TestEval_RecvBufferCompaction_MultiCycleMemoryBound/IncrementalMessageAssembly (0.01s)
    eval_recv_buffer_compaction_test.go:627: Compaction destination buffer was never acquired from the configured buffer pool
--- FAIL: TestEval_RecvBufferConfiguredPoolAcquisition (0.00s)
    --- FAIL: TestEval_RecvBufferConfiguredPoolAcquisition/UnpooledConsolidation (0.00s)
    --- PASS: TestEval_RecvBufferConfiguredPoolAcquisition/SliceGrowthPoolOwnership (0.00s)
    --- PASS: TestEval_RecvBufferConfiguredPoolAcquisition/ExactCapacitySmallDestination (0.00s)
FAIL
FAIL	google.golang.org/grpc/internal/transport	0.081s
FAIL
```

Same fixture, same workloads and bounds, with the buffer obtained from the production constructor instead (`verify/probes/c345_prod_construction_fixture_test.go`: every `b := &recvBuffer{}; initRecvBufferForTest(b, pool)` replaced by `b := &(&http2Client{bufferPool: pool}).newStream(context.Background(), &CallHdr{}, nil).buf`; `Eval`->`Prod` renames; gofmt; nothing else):

```sh
rm internal/transport/eval_recv_buffer_compaction_test.go
cp <repo>/verify/probes/c345_prod_construction_fixture_test.go <repo>/verify/probes/c345_measure_test.go internal/transport/
go test -tags verify_audit -v -run '^TestProd_' google.golang.org/grpc/internal/transport -race -count=1
go test -tags verify_audit -v -run '^TestC345_' google.golang.org/grpc/internal/transport -race -count=1
```

```console
--- PASS: TestProd_RecvBufferCompaction (0.00s)
--- PASS: TestProd_RecvBufferCompactionDisabled (0.00s)
    --- PASS: TestProd_RecvBufferCompactionDisabled/ProductionStream (0.00s)
    --- PASS: TestProd_RecvBufferCompactionDisabled/ComponentBuffer (0.00s)
--- PASS: TestProd_RecvBufferCompactionSkippedLargeBuffer (0.02s)
    --- PASS: TestProd_RecvBufferCompactionSkippedLargeBuffer/PoolOwnership_SliceGrowth (0.00s)
--- PASS: TestProd_RecvBufferErrorResetSafety (0.00s)
--- PASS: TestProd_RecvBufferCompaction_MixedFrames (0.00s)
--- PASS: TestProd_RecvBufferCompaction_MultiCycleMemoryBound (0.01s)
    --- PASS: TestProd_RecvBufferCompaction_MultiCycleMemoryBound/MultiCycleBursts (0.00s)
    --- PASS: TestProd_RecvBufferCompaction_MultiCycleMemoryBound/IncrementalMessageAssembly (0.01s)
    c345_prod_construction_fixture_test.go:624: Compaction destination buffer was never acquired from the configured buffer pool
--- FAIL: TestProd_RecvBufferConfiguredPoolAcquisition (0.00s)
    --- FAIL: TestProd_RecvBufferConfiguredPoolAcquisition/UnpooledConsolidation (0.00s)
    --- PASS: TestProd_RecvBufferConfiguredPoolAcquisition/SliceGrowthPoolOwnership (0.00s)
    --- PASS: TestProd_RecvBufferConfiguredPoolAcquisition/ExactCapacitySmallDestination (0.00s)
FAIL
FAIL	google.golang.org/grpc/internal/transport	0.058s
FAIL
```

(The one remaining failure, `UnpooledConsolidation`, asserts that the destination comes from the configured pool; this branch allocates the tail with `make`. It is not a consolidation or memory-bound check and is outside C3/C4/C5.)

`TestProd_RecvBufferCompaction_MixedFrames` above is that fixture test on a `newStream` buffer: PASS. Direct measurement on client- and server-constructed buffers (`TestC345_C4_SmallAndAlternating`; the tail buffer, which this branch keeps outside `backlog`, is counted too):

```console
    c345_measure_test.go:143: C4 small(1-7B) client(newStream): frames=1074 payload=4290 len(backlog)=0 queuedBuffers=1 queuedPayload=4289 retainedCap=16384 (bound: backlog<=134)
    c345_measure_test.go:170: C4 alternating(1B/2KiB) client(newStream): payload=49178 len(backlog)=3 queuedBuffers=4 queuedPayload=49177 retainedCap(backlog+tail)=65536 (bound: retained<=262248)
    c345_measure_test.go:143: C4 small(1-7B) server(operateHeaders): frames=1074 payload=4290 len(backlog)=0 queuedBuffers=1 queuedPayload=4289 retainedCap=16384 (bound: backlog<=134)
    c345_measure_test.go:170: C4 alternating(1B/2KiB) server(operateHeaders): payload=49178 len(backlog)=3 queuedBuffers=4 queuedPayload=49177 retainedCap(backlog+tail)=65536 (bound: retained<=262248)
--- PASS: TestC345_C4_SmallAndAlternating (0.01s)
```

Small frames: 1 queued buffer against a bound of 134. Alternating: 65,536 bytes retained against a bound of 262,248 (payload 49,178). Both workloads are delivered in order. Both bounds hold on both transports.

Scope note: the third production constructor, `serverHandlerTransport.HandleStreams` (the `grpc.Server.ServeHTTP` path), does not enable compaction on this branch. That is the subject of C9 and is recorded there; the measurements in this section are for the `http2Client` and `http2Server` transports.

## C5

**Claim:** default production receive buffering exceeds the specified retained-memory bounds during repeated burst or incremental-retention cycles. Branch: [evalon/grpc-go-tr-9253f1d4](https://github.com/kaitranntt-evals/grpc-go-transport-restrict-memory-overhead/tree/evalon/grpc-go-tr-9253f1d4). **Verdict: REFUTED.**

"Specified bounds" are the fixture's (`TestEval_RecvBufferCompaction_MultiCycleMemoryBound`): `len(backlog) <= 512` after each of 3 bursts of 1,124 frames of 1-5 bytes with one read between bursts; and distinct retained capacity `<= 4*payload + 64 KiB` (= 77,824) after 1,024 cycles of put-3/read-3 with every delivered buffer retained.

The eval fixture fails on this branch, but only because of how the fixture builds its buffer. `initRecvBufferForTest` probes for `initWithPool`/`init(bool, pool)`/`init(pool)`/`enableCompaction` signatures; this branch keeps `recvBuffer.init()` and enables compaction with a field assignment in the production constructors (`s.Stream.buf.compact = envconfig.EnableReceiveBufferCompaction` in `http2Client.newStream` and `http2Server.operateHeaders`), so the fixture ends up with `init()` only, i.e. a buffer with compaction off. The fixture's own `ProductionStream` subtest, which goes through `client.newStream`, passes.

```sh
cd ~/wt/9253f1d4 && cp $FIXTURE internal/transport/eval_recv_buffer_compaction_test.go
go test -v -run '^TestEval_' google.golang.org/grpc/internal/transport -race -count=1
```

```console
    eval_recv_buffer_compaction_test.go:150: Got backlog length 1025 after compaction, want <= 64
--- FAIL: TestEval_RecvBufferCompaction (0.00s)
    eval_recv_buffer_compaction_test.go:189: ComponentBuffer: got backlog length 1025, want <= 64 (compaction enabled)
--- FAIL: TestEval_RecvBufferCompactionDisabled (0.01s)
    --- PASS: TestEval_RecvBufferCompactionDisabled/ProductionStream (0.00s)
    --- FAIL: TestEval_RecvBufferCompactionDisabled/ComponentBuffer (0.00s)
--- PASS: TestEval_RecvBufferCompactionSkippedLargeBuffer (0.03s)
    --- PASS: TestEval_RecvBufferCompactionSkippedLargeBuffer/PoolOwnership_SliceGrowth (0.00s)
--- PASS: TestEval_RecvBufferErrorResetSafety (0.00s)
    eval_recv_buffer_compaction_test.go:416: Got backlog length 1073 after mixed frame compaction, want <= 134
--- FAIL: TestEval_RecvBufferCompaction_MixedFrames (0.00s)
    eval_recv_buffer_compaction_test.go:496: Cycle 0: backlog length 1123 exceeded bound 512
--- FAIL: TestEval_RecvBufferCompaction_MultiCycleMemoryBound (0.02s)
    --- FAIL: TestEval_RecvBufferCompaction_MultiCycleMemoryBound/MultiCycleBursts (0.01s)
    --- PASS: TestEval_RecvBufferCompaction_MultiCycleMemoryBound/IncrementalMessageAssembly (0.01s)
    eval_recv_buffer_compaction_test.go:627: Compaction destination buffer was never acquired from the configured buffer pool
--- FAIL: TestEval_RecvBufferConfiguredPoolAcquisition (0.00s)
    --- FAIL: TestEval_RecvBufferConfiguredPoolAcquisition/UnpooledConsolidation (0.00s)
    --- PASS: TestEval_RecvBufferConfiguredPoolAcquisition/SliceGrowthPoolOwnership (0.00s)
    --- PASS: TestEval_RecvBufferConfiguredPoolAcquisition/ExactCapacitySmallDestination (0.00s)
FAIL
FAIL	google.golang.org/grpc/internal/transport	0.081s
FAIL
```

Same fixture, same workloads and bounds, with the buffer obtained from the production constructor instead (`verify/probes/c345_prod_construction_fixture_test.go`: every `b := &recvBuffer{}; initRecvBufferForTest(b, pool)` replaced by `b := &(&http2Client{bufferPool: pool}).newStream(context.Background(), &CallHdr{}, nil).buf`; `Eval`->`Prod` renames; gofmt; nothing else):

```sh
rm internal/transport/eval_recv_buffer_compaction_test.go
cp <repo>/verify/probes/c345_prod_construction_fixture_test.go <repo>/verify/probes/c345_measure_test.go internal/transport/
go test -tags verify_audit -v -run '^TestProd_' google.golang.org/grpc/internal/transport -race -count=1
go test -tags verify_audit -v -run '^TestC345_' google.golang.org/grpc/internal/transport -race -count=1
```

```console
--- PASS: TestProd_RecvBufferCompaction (0.00s)
--- PASS: TestProd_RecvBufferCompactionDisabled (0.00s)
    --- PASS: TestProd_RecvBufferCompactionDisabled/ProductionStream (0.00s)
    --- PASS: TestProd_RecvBufferCompactionDisabled/ComponentBuffer (0.00s)
--- PASS: TestProd_RecvBufferCompactionSkippedLargeBuffer (0.02s)
    --- PASS: TestProd_RecvBufferCompactionSkippedLargeBuffer/PoolOwnership_SliceGrowth (0.00s)
--- PASS: TestProd_RecvBufferErrorResetSafety (0.00s)
--- PASS: TestProd_RecvBufferCompaction_MixedFrames (0.00s)
--- PASS: TestProd_RecvBufferCompaction_MultiCycleMemoryBound (0.01s)
    --- PASS: TestProd_RecvBufferCompaction_MultiCycleMemoryBound/MultiCycleBursts (0.00s)
    --- PASS: TestProd_RecvBufferCompaction_MultiCycleMemoryBound/IncrementalMessageAssembly (0.01s)
    c345_prod_construction_fixture_test.go:624: Compaction destination buffer was never acquired from the configured buffer pool
--- FAIL: TestProd_RecvBufferConfiguredPoolAcquisition (0.00s)
    --- FAIL: TestProd_RecvBufferConfiguredPoolAcquisition/UnpooledConsolidation (0.00s)
    --- PASS: TestProd_RecvBufferConfiguredPoolAcquisition/SliceGrowthPoolOwnership (0.00s)
    --- PASS: TestProd_RecvBufferConfiguredPoolAcquisition/ExactCapacitySmallDestination (0.00s)
FAIL
FAIL	google.golang.org/grpc/internal/transport	0.058s
FAIL
```

(The one remaining failure, `UnpooledConsolidation`, asserts that the destination comes from the configured pool; this branch allocates the tail with `make`. It is not a consolidation or memory-bound check and is outside C3/C4/C5.)

`TestProd_RecvBufferCompaction_MultiCycleMemoryBound` (both subtests) above is that fixture test on a `newStream` buffer: PASS. Direct measurement at every checkpoint on client- and server-constructed buffers (`TestC345_C5_Cycles`):

```console
    c345_measure_test.go:195: C5 burst client(newStream) cycle 0: len(backlog)=0 queuedBuffers=1 queuedPayload=3369 retainedCap=16384 (bounds: backlog<=512; 4*payload+64KiB=79012)
    c345_measure_test.go:195: C5 burst client(newStream) cycle 1: len(backlog)=1 queuedBuffers=2 queuedPayload=6739 retainedCap=19840 (bounds: backlog<=512; 4*payload+64KiB=92492)
    c345_measure_test.go:195: C5 burst client(newStream) cycle 2: len(backlog)=0 queuedBuffers=1 queuedPayload=6740 retainedCap=16384 (bounds: backlog<=512; 4*payload+64KiB=92496)
    c345_measure_test.go:254: C5 incremental client(newStream): payload=3072 retainedBuffers=2048 distinctRetainedCap=9216 maxSingleCap=8 (bound: <=77824) inOrder=true
    c345_measure_test.go:195: C5 burst server(operateHeaders) cycle 0: len(backlog)=0 queuedBuffers=1 queuedPayload=3369 retainedCap=16384 (bounds: backlog<=512; 4*payload+64KiB=79012)
    c345_measure_test.go:195: C5 burst server(operateHeaders) cycle 1: len(backlog)=1 queuedBuffers=2 queuedPayload=6739 retainedCap=19840 (bounds: backlog<=512; 4*payload+64KiB=92492)
    c345_measure_test.go:195: C5 burst server(operateHeaders) cycle 2: len(backlog)=0 queuedBuffers=1 queuedPayload=6740 retainedCap=16384 (bounds: backlog<=512; 4*payload+64KiB=92496)
    c345_measure_test.go:254: C5 incremental server(operateHeaders): payload=3072 retainedBuffers=2048 distinctRetainedCap=9216 maxSingleCap=8 (bound: <=77824) inOrder=true
--- PASS: TestC345_C5_Cycles (0.03s)
```

Bursts: at most 2 queued buffers (bound 512) and at most 19,840 bytes retained for 6,739 queued payload bytes. Incremental retention: 9,216 bytes of distinct retained capacity for 3,072 payload bytes (bound 77,824), largest single buffer 8 bytes - underfilled tails are copied to an exact-size slice on flush (`flushTail`: `if len(data) <= cap(data)/2 { data = append([]byte(nil), data...) }`). All bounds hold on both transports.

Scope note: the third production constructor, `serverHandlerTransport.HandleStreams` (the `grpc.Server.ServeHTTP` path), does not enable compaction on this branch. That is the subject of C9 and is recorded there; the measurements in this section are for the `http2Client` and `http2Server` transports.

## C6

**Claim:** receive-buffer compaction fails the configured memory pool's destination-storage ownership lifecycle - (a) *destination_acquisition*: fails to obtain usable destination storage through the pool's public allocation contract; (b) *destination_release*: fails to return acquired destination storage through the pool's public release contract. Branch: [evalon/grpc-go-tr-a3b171be](https://github.com/kaitranntt-evals/grpc-go-transport-restrict-memory-overhead/tree/evalon/grpc-go-tr-a3b171be). **Verdict: REFUTED** (both parts).

**The public contract** (`mem/buffer_pool.go`):

```go
type BufferPool interface {
	// Get returns a buffer with specified length from the pool.
	Get(length int) *[]byte

	// Put returns a buffer to the pool.
	//
	// The provided pointer must hold a prefix of the buffer obtained via
	// BufferPool.Get to ensure the buffer's entire capacity can be re-used.
	Put(*[]byte)
}
```

The solution relies on exactly that: `b.tail = pool.Get(recvBufferCompactionChunkSize)`, writes into `(*b.tail)[b.tailLen:]`, and on flush hands `b.tail` to `mem.NewBuffer(b.tail, pool)`, whose `Free()` calls `pool.Put`.

Probe: `verify/probes/c6_pool_lifecycle_test.go`.

```sh
cd ~/wt/a3b171be && cp <repo>/verify/probes/c6_pool_lifecycle_test.go internal/transport/
go test -tags verify_audit -v -run '^TestC6_' google.golang.org/grpc/internal/transport -race -count=1 -timeout 120s
```

**Contract as implemented by every public pool** (`TestC6_Contract`): `len == requested length` everywhere, and `mem.Copy` itself depends on it. The last line is the pool the fixture's `ExactCapacitySmallDestination` subtest uses (`make([]byte, 0, n)`): length 0 - `mem.Copy` through it silently yields an empty buffer, i.e. it does not satisfy "returns a buffer with specified length".

```console
    c6_pool_lifecycle_test.go:108: C6 contract mem.DefaultBufferPool()                                : Get(100)->len=100,cap=256; Get(1000)->len=1000,cap=4096; Get(16384)->len=16384,cap=16384; mem.Copy(2000 bytes).Len()=2000
    c6_pool_lifecycle_test.go:108: C6 contract mem.NewBinaryTieredBufferPool(8,12,14,15,20)           : Get(100)->len=100,cap=256; Get(1000)->len=1000,cap=4096; Get(16384)->len=16384,cap=16384; mem.Copy(2000 bytes).Len()=2000
    c6_pool_lifecycle_test.go:108: C6 contract mem.NewTieredBufferPool(256,4096,16384)                : Get(100)->len=100,cap=256; Get(1000)->len=1000,cap=4096; Get(16384)->len=16384,cap=16384; mem.Copy(2000 bytes).Len()=2000
    c6_pool_lifecycle_test.go:108: C6 contract mem.NopBufferPool{}                                    : Get(100)->len=100,cap=100; Get(1000)->len=1000,cap=1000; Get(16384)->len=16384,cap=16384; mem.Copy(2000 bytes).Len()=2000
    c6_pool_lifecycle_test.go:108: C6 contract c6ExactPool (probe, compliant)                         : Get(100)->len=100,cap=100; Get(1000)->len=1000,cap=1000; Get(16384)->len=16384,cap=16384; mem.Copy(2000 bytes).Len()=2000
    c6_pool_lifecycle_test.go:108: C6 contract zero-length pool (= fixture evalExactCapacityPool.Get) : Get(100)->len=0,cap=100; Get(1000)->len=0,cap=1000; Get(16384)->len=0,cap=16384; mem.Copy(2000 bytes).Len()=0
--- PASS: TestC6_Contract (0.00s)
```

**(a) acquisition and (b) release with contract-compliant pools** (`TestC6_CompliantPools`): streams built with `(&http2Client{bufferPool: tracking}).newStream(...)`; the tracking pool records every `Get`/`Put` by backing-array address. Four pools x four workloads (the fixture's three `ConfiguredPoolAcquisition` workloads plus 40,000 one-byte frames followed by EOF, consumed through `recvBufferReader` with split reads and with every payload buffer held until the end). `outstanding` = acquired and not returned after all `Free()` calls; `foreign` = returned without having been acquired.

```console
    c6_pool_lifecycle_test.go:224: C6 pool=NopBufferPool                workload=1026x1B SliceBuffer (fixture UnpooledConsolidation)        inOrder=true bytes=1026 gets(first)=[Get(16384)->len=16384,cap=16384] totalPuts=1 outstanding=0 foreign=0 getsWithLen!=n=0
    c6_pool_lifecycle_test.go:224: C6 pool=NopBufferPool                workload=occupy+8KiB+5x1B pooled (fixture SliceGrowthPoolOwnership) inOrder=true bytes=8203 gets(first)=[Get(8192)->len=8192,cap=8192 Get(16384)->len=16384,cap=16384] totalPuts=2 outstanding=0 foreign=0 getsWithLen!=n=0
    c6_pool_lifecycle_test.go:224: C6 pool=NopBufferPool                workload=10x100B SliceBuffer (fixture ExactCapacitySmallDestination) inOrder=true bytes=1000 gets(first)=[Get(16384)->len=16384,cap=16384] totalPuts=1 outstanding=0 foreign=0 getsWithLen!=n=0
    c6_pool_lifecycle_test.go:224: C6 pool=NopBufferPool                workload=40000x1B then EOF, partial reads via recvBufferReader      inOrder=true bytes=40000 gets(first)=[Get(16384)->len=16384,cap=16384 Get(16384)->len=16384,cap=16384 Get(16384)->len=16384,cap=16384] totalPuts=3 outstanding=0 foreign=0 getsWithLen!=n=0
    c6_pool_lifecycle_test.go:224: C6 pool=BinaryTiered(8,12,14,15,20)  workload=1026x1B SliceBuffer (fixture UnpooledConsolidation)        inOrder=true bytes=1026 gets(first)=[Get(16384)->len=16384,cap=16384] totalPuts=1 outstanding=0 foreign=0 getsWithLen!=n=0
    c6_pool_lifecycle_test.go:224: C6 pool=BinaryTiered(8,12,14,15,20)  workload=occupy+8KiB+5x1B pooled (fixture SliceGrowthPoolOwnership) inOrder=true bytes=8203 gets(first)=[Get(8192)->len=8192,cap=16384 Get(16384)->len=16384,cap=16384] totalPuts=2 outstanding=0 foreign=0 getsWithLen!=n=0
    c6_pool_lifecycle_test.go:224: C6 pool=BinaryTiered(8,12,14,15,20)  workload=10x100B SliceBuffer (fixture ExactCapacitySmallDestination) inOrder=true bytes=1000 gets(first)=[Get(16384)->len=16384,cap=16384] totalPuts=1 outstanding=0 foreign=0 getsWithLen!=n=0
    c6_pool_lifecycle_test.go:224: C6 pool=BinaryTiered(8,12,14,15,20)  workload=40000x1B then EOF, partial reads via recvBufferReader      inOrder=true bytes=40000 gets(first)=[Get(16384)->len=16384,cap=16384 Get(16384)->len=16384,cap=16384 Get(16384)->len=16384,cap=16384] totalPuts=3 outstanding=0 foreign=0 getsWithLen!=n=0
    c6_pool_lifecycle_test.go:224: C6 pool=DefaultBufferPool            workload=1026x1B SliceBuffer (fixture UnpooledConsolidation)        inOrder=true bytes=1026 gets(first)=[Get(16384)->len=16384,cap=16384] totalPuts=1 outstanding=0 foreign=0 getsWithLen!=n=0
    c6_pool_lifecycle_test.go:224: C6 pool=DefaultBufferPool            workload=occupy+8KiB+5x1B pooled (fixture SliceGrowthPoolOwnership) inOrder=true bytes=8203 gets(first)=[Get(8192)->len=8192,cap=16384 Get(16384)->len=16384,cap=16384] totalPuts=2 outstanding=0 foreign=0 getsWithLen!=n=0
    c6_pool_lifecycle_test.go:224: C6 pool=DefaultBufferPool            workload=10x100B SliceBuffer (fixture ExactCapacitySmallDestination) inOrder=true bytes=1000 gets(first)=[Get(16384)->len=16384,cap=16384] totalPuts=1 outstanding=0 foreign=0 getsWithLen!=n=0
    c6_pool_lifecycle_test.go:224: C6 pool=DefaultBufferPool            workload=40000x1B then EOF, partial reads via recvBufferReader      inOrder=true bytes=40000 gets(first)=[Get(16384)->len=16384,cap=16384 Get(16384)->len=16384,cap=16384 Get(16384)->len=16384,cap=16384] totalPuts=3 outstanding=0 foreign=0 getsWithLen!=n=0
    c6_pool_lifecycle_test.go:224: C6 pool=ExactLen(make([]byte,n))     workload=1026x1B SliceBuffer (fixture UnpooledConsolidation)        inOrder=true bytes=1026 gets(first)=[Get(16384)->len=16384,cap=16384] totalPuts=1 outstanding=0 foreign=0 getsWithLen!=n=0
    c6_pool_lifecycle_test.go:224: C6 pool=ExactLen(make([]byte,n))     workload=occupy+8KiB+5x1B pooled (fixture SliceGrowthPoolOwnership) inOrder=true bytes=8203 gets(first)=[Get(8192)->len=8192,cap=8192 Get(16384)->len=16384,cap=16384] totalPuts=2 outstanding=0 foreign=0 getsWithLen!=n=0
    c6_pool_lifecycle_test.go:224: C6 pool=ExactLen(make([]byte,n))     workload=10x100B SliceBuffer (fixture ExactCapacitySmallDestination) inOrder=true bytes=1000 gets(first)=[Get(16384)->len=16384,cap=16384] totalPuts=1 outstanding=0 foreign=0 getsWithLen!=n=0
    c6_pool_lifecycle_test.go:224: C6 pool=ExactLen(make([]byte,n))     workload=40000x1B then EOF, partial reads via recvBufferReader      inOrder=true bytes=40000 gets(first)=[Get(16384)->len=16384,cap=16384 Get(16384)->len=16384,cap=16384 Get(16384)->len=16384,cap=16384] totalPuts=3 outstanding=0 foreign=0 getsWithLen!=n=0
--- PASS: TestC6_CompliantPools (0.17s)
```

Every run: destination acquired from the configured pool with `len=16384`, payload delivered in order, `outstanding=0`, `foreign=0`.

**The fixture on this branch.** Two of the three subtests pass; the third does not terminate:

```sh
cp $FIXTURE internal/transport/eval_recv_buffer_compaction_test.go
go test -c -race -o /tmp/c6_fixture.test ./internal/transport
( ulimit -v 6000000; GOMEMLIMIT=1GiB timeout 20 /tmp/c6_fixture.test -test.v -test.run '^TestEval_RecvBufferConfiguredPoolAcquisition$/^<sub>$' -test.timeout 15s; echo exit=$? )
```

```console
### TestEval_RecvBufferConfiguredPoolAcquisition/UnpooledConsolidation (ulimit -v 6000000, timeout 20s)
--- PASS: TestEval_RecvBufferConfiguredPoolAcquisition (0.00s)
    --- PASS: TestEval_RecvBufferConfiguredPoolAcquisition/UnpooledConsolidation (0.00s)
PASS
exit=0
### TestEval_RecvBufferConfiguredPoolAcquisition/SliceGrowthPoolOwnership (ulimit -v 6000000, timeout 20s)
--- PASS: TestEval_RecvBufferConfiguredPoolAcquisition (0.00s)
    --- PASS: TestEval_RecvBufferConfiguredPoolAcquisition/SliceGrowthPoolOwnership (0.00s)
PASS
exit=0
### TestEval_RecvBufferConfiguredPoolAcquisition/ExactCapacitySmallDestination (ulimit -v 6000000, timeout 20s)
fatal error: too many address space collisions for -race mode
exit=2
```

(Without the `ulimit` the same subtest ends in `signal: killed` after ~21 s; in another run under the limit the message was `ThreadSanitizer failed to allocate 0x2000000 (33554432) bytes ... (errno: 12)`, exit 66.)

Cause, isolated in `TestC6_ZeroLengthPoolLivelock` with a call cap on `Get`: with a zero-length buffer `copy` moves 0 bytes, `b.tailLen == len(*b.tail)` (0 == 0) flushes an empty chunk, and the loop asks for another:

```console
    c6_pool_lifecycle_test.go:108: C6 contract zero-length pool (= fixture evalExactCapacityPool.Get) : Get(100)->len=0,cap=100; Get(1000)->len=0,cap=1000; Get(16384)->len=0,cap=16384; mem.Copy(2000 bytes).Len()=0
    c6_pool_lifecycle_test.go:243: C6 zero-length pool: second put() of 100 bytes: recovered=c6: Get call limit reached after 100001 Get(16384) calls; len(backlog)=100000 (all empty buffers)
--- PASS: TestC6_ZeroLengthPoolLivelock (1.46s)
```

So the hang is real, but it is triggered only by a pool whose `Get(n)` returns `len != n`. That is outside the public contract (and such a pool already breaks `mem.Copy`), so it is not evidence for part (a), which is conditioned on contract-compliant pool behaviour. Part (b): no acquired destination was left unreturned in any compliant-pool run.

Not part of C6, observed in passing: `TestEval_RecvBufferCompaction_MultiCycleMemoryBound/IncrementalMessageAssembly` fails on this branch (`Distinct retained capacity 16778240 exceeded limit 77824 (payload: 3072 bytes)`) - each flush of an underfilled tail pins a whole 16 KiB pool buffer. That is a retention issue, not a pool acquire/release one.

## C7

**Claim:** sustained receive consumption that always leaves one message queued retains 16-KiB compaction destinations for individual one-byte payloads - (a) *capacity_management*: `load()` flushes underfilled destinations without resetting `nextPendingCap`, whose reset depends on a direct-channel `put()`; (b) *sustained_backlog_trigger*: such a schedule reaches 16-KiB destinations for one-byte payloads and retains them during message assembly. Branch: [evalon/grpc-go-tr-718bb10b](https://github.com/kaitranntt-evals/grpc-go-transport-restrict-memory-overhead/tree/evalon/grpc-go-tr-718bb10b). **Verdict: CONFIRMED** (both parts).

Relevant code (`internal/transport/transport.go` on the branch): `nextPendingCap` is set to 0 in exactly one place - the direct hand-off in `put` (`case b.c <- r: b.nextPendingCap = 0`); `newPendingBuffer` sets `b.nextPendingCap = min(2*cap(*buf), recvBufferCompactionMaxCap)`; `load()` calls `flushPending()` when the backlog is empty and does not touch `nextPendingCap`.

Repro: `verify/repro/c7_sustained_backlog_test.go`. Streams are built with `(&http2Client{bufferPool: mem.DefaultBufferPool()}).newStream(...)`.

```sh
cd ~/wt/718bb10b && cp <repo>/verify/repro/c7_sustained_backlog_test.go internal/transport/
go test -tags verify_audit -v -run '^TestC7_' google.golang.org/grpc/internal/transport -race -count=1
GRPC_GO_EXPERIMENTAL_ENABLE_RECEIVE_BUFFER_COMPACTION=false go test -tags verify_audit -v -run '^TestC7_' google.golang.org/grpc/internal/transport -race -count=1
```

**(a) capacity management** - `TestC7_SustainedBacklogDefault` prints `recvBuffer` state after each `put` in the schedule "put 2, then repeat {put 1; `recvBufferReader.Read(1)`}". Every `Read` empties `b.c` and its `load()` flushes the (1-2 byte) pending buffer; no `put` ever goes straight to the channel. The capacity doubles through those flushes until it reaches 16,384 and stays there:

```console
    c7_sustained_backlog_test.go:63: C7 trace env=: after put #2: len(backlog)=1 pendingCap=2 nextPendingCap=4
    c7_sustained_backlog_test.go:63: C7 trace env=: after put #3: len(backlog)=0 pendingCap=2 nextPendingCap=4
    c7_sustained_backlog_test.go:63: C7 trace env=: after put #4: len(backlog)=0 pendingCap=4 nextPendingCap=8
    c7_sustained_backlog_test.go:63: C7 trace env=: after put #5: len(backlog)=0 pendingCap=8 nextPendingCap=16
    c7_sustained_backlog_test.go:63: C7 trace env=: after put #6: len(backlog)=0 pendingCap=8 nextPendingCap=16
    c7_sustained_backlog_test.go:63: C7 trace env=: after put #7: len(backlog)=0 pendingCap=16 nextPendingCap=32
    c7_sustained_backlog_test.go:63: C7 trace env=: after put #8: len(backlog)=0 pendingCap=32 nextPendingCap=64
    c7_sustained_backlog_test.go:63: C7 trace env=: after put #9: len(backlog)=0 pendingCap=32 nextPendingCap=64
    c7_sustained_backlog_test.go:63: C7 trace env=: after put #10: len(backlog)=0 pendingCap=64 nextPendingCap=128
    c7_sustained_backlog_test.go:63: C7 trace env=: after put #11: len(backlog)=0 pendingCap=128 nextPendingCap=256
    c7_sustained_backlog_test.go:63: C7 trace env=: after put #12: len(backlog)=0 pendingCap=128 nextPendingCap=256
    c7_sustained_backlog_test.go:63: C7 trace env=: after put #13: len(backlog)=0 pendingCap=256 nextPendingCap=512
    c7_sustained_backlog_test.go:63: C7 trace env=: after put #14: len(backlog)=0 pendingCap=512 nextPendingCap=1024
    c7_sustained_backlog_test.go:63: C7 trace env=: after put #15: len(backlog)=0 pendingCap=512 nextPendingCap=1024
    c7_sustained_backlog_test.go:63: C7 trace env=: after put #16: len(backlog)=0 pendingCap=1024 nextPendingCap=2048
    c7_sustained_backlog_test.go:63: C7 trace env=: after put #17: len(backlog)=0 pendingCap=4096 nextPendingCap=8192
    c7_sustained_backlog_test.go:63: C7 trace env=: after put #18: len(backlog)=0 pendingCap=4096 nextPendingCap=8192
    c7_sustained_backlog_test.go:63: C7 trace env=: after put #19: len(backlog)=0 pendingCap=16384 nextPendingCap=16384
    c7_sustained_backlog_test.go:63: C7 trace env=: after put #20: len(backlog)=0 pendingCap=16384 nextPendingCap=16384
```

**(b) reachability, retained buffers** - same test, all 1,024 delivered buffers retained (as `Stream.read` does while assembling a message), capacity counted once per distinct backing array:

```console
    c7_sustained_backlog_test.go:71: C7 env=: payload=1024 bytes in 1024 one-byte frames; inOrder=true; retained buffers=1024; >=16KiB arrays holding <=4 payload bytes=670; distinct retained capacity=10983424 bytes (10726x payload)
--- PASS: TestC7_SustainedBacklogDefault (0.02s)
```

Through the real assembly loop - `TestC7_StreamReadAssembly` calls `Stream.read(1024)` on the `newStream`-built stream; the next one-byte payload is `put` from the reader's window-update hook, i.e. it arrives while the reader is accounting for the buffer it just consumed:

```console
    c7_sustained_backlog_test.go:216: C7 Stream.read env="": 1024-byte message in 1024 one-byte payloads; inOrder=true; buffers in assembled message=1024; distinct backing arrays=1024; >=16KiB arrays holding <=4 payload bytes=1011; distinct retained capacity=16570368 bytes (16182x payload)
--- PASS: TestC7_StreamReadAssembly (0.02s)
```

1,011 of the 1,024 one-byte payloads each pin a >= 16 KiB pool buffer: 16.5 MB held for a 1 KiB message. With the escape hatch (`=false`), identical schedules:

```console
    c7_sustained_backlog_test.go:71: C7 env=false: payload=1024 bytes in 1024 one-byte frames; inOrder=true; retained buffers=1024; >=16KiB arrays holding <=4 payload bytes=0; distinct retained capacity=1024 bytes (1x payload)
    c7_sustained_backlog_test.go:216: C7 Stream.read env="false": 1024-byte message in 1024 one-byte payloads; inOrder=true; buffers in assembled message=1024; distinct backing arrays=1024; >=16KiB arrays holding <=4 payload bytes=0; distinct retained capacity=1024 bytes (1x payload)
```

So on this schedule the fix makes receive memory ~16,000x worse than the behaviour it replaces.

**How ordinary is the schedule?** `TestC7_EndToEndMessageAssembly` runs a real `http2Server` over loopback with a raw HTTP/2 client sending 4,000 one-byte DATA frames as fast as it can while the server application is blocked in `ServerStream.Read(4000)`. Free-running on this machine it did **not** fall into the pattern (direct hand-offs happen often enough to reset the capacity):

```console
    c7_sustained_backlog_test.go:177: C7 e2e env="": 4000-byte message in 4000 one-byte DATA frames; inOrder=true; buffers in assembled message=1921; distinct backing arrays=1921; >=16KiB arrays holding <=4 payload bytes=0; distinct retained capacity=5829 bytes (1x payload)
--- PASS: TestC7_EndToEndMessageAssembly (0.03s)
```

The trigger therefore needs the reader to stay about one frame behind the peer for roughly 17 consecutive frames without ever fully catching up (the trace above reaches 16,384 at put #19) (after that every further frame costs 16 KiB until a direct hand-off happens). I reproduced that deterministically at the `recvBuffer`/`Stream.read` level with production-constructed streams, not with free-running socket timing.

**The eval fixture does not see it.** Its `IncrementalMessageAssembly` schedule (put 3, read 3) lets the first `put` of every cycle go straight to the channel, which resets `nextPendingCap`:

```sh
cp $FIXTURE internal/transport/eval_recv_buffer_compaction_test.go
go test -v -run '^TestEval_RecvBufferCompaction_MultiCycleMemoryBound$' google.golang.org/grpc/internal/transport -race -count=1
```

```console
--- PASS: TestEval_RecvBufferCompaction_MultiCycleMemoryBound (0.01s)
    --- PASS: TestEval_RecvBufferCompaction_MultiCycleMemoryBound/MultiCycleBursts (0.00s)
    --- PASS: TestEval_RecvBufferCompaction_MultiCycleMemoryBound/IncrementalMessageAssembly (0.01s)
PASS
ok  	google.golang.org/grpc/internal/transport	1.024s
```

**Impact reasoning (C7).** Compaction is on by default. A stream whose reader trails the sender by one small frame for a short while ends up holding one 16 KiB pooled buffer per frame for as long as the application retains what it read - which gRPC does for the whole message while assembling it. Memory is then bounded by 16 KiB x (number of tiny frames in the message) rather than by the message size: the opposite of the task's goal, in a state where every byte is still delivered correctly and every existing check (the branch's own tests and the eval fixture) is green. The only workaround is the escape-hatch env var.

## C8

**Claim:** `internal/transport/recv_buffer_compaction_test.go` differs from the output of `gofmt -s`. Branch: [evalon/grpc-go-tr-37793f8a](https://github.com/kaitranntt-evals/grpc-go-transport-restrict-memory-overhead/tree/evalon/grpc-go-tr-37793f8a). **Verdict: REFUTED.**

```sh
cd ~/wt/37793f8a   # clean detached checkout of the branch head
```

```console
$ go version
go version go1.25.7 linux/amd64
$ git rev-parse HEAD; git status --short | wc -l
76b1ff251c7a689e8cc26fc6a1a5966ea3e177f9
0
$ gofmt -s -d -l internal/transport/recv_buffer_compaction_test.go; echo exit=$?
exit=0
$ gofmt -s -d -l internal/transport/recv_buffer_compaction_test.go | wc -c
0
$ git show HEAD:internal/transport/recv_buffer_compaction_test.go | gofmt -s -d | wc -c
0
$ gofmt -s -l internal/transport internal/envconfig internal/mem mem | wc -l
0
$ grep -n 'putTiny(100)\|put(large(' internal/transport/recv_buffer_compaction_test.go
103:	putTiny(100)                                          // Chunk 1 holds tiny messages 3 through 100.
104:	put(large(recvBufferCompactionMaxPayloadSize+1, 'A')) // Too large: passes through, closing chunk 1.
105:	putTiny(100)                                          // Chunk 2 holds all 100 tiny messages.
107:	put(large(recvBufferCompactionMaxPayloadSize, 'B'))
109:	put(large(recvBufferCompactionMaxPayloadSize, 'C'))
111:	put(large(recvBufferCompactionChunkSize, 'E')) // Too large: passes through, closing chunk 3.
$ # positive control: de-align the trailing comments on the putTiny lines in a scratch copy
$ gofmt -s -d -l /tmp/c8/ctl_test.go
/tmp/c8/ctl_test.go
diff /tmp/c8/ctl_test.go.orig /tmp/c8/ctl_test.go
--- /tmp/c8/ctl_test.go.orig
+++ /tmp/c8/ctl_test.go
@@ -100,9 +100,9 @@
 	// The first message is handed straight to the channel and the second one
 	// starts the backlog without being compacted. Everything after that is
 	// compacted while it fits the eligibility threshold.
-	putTiny(100) // Chunk 1 holds tiny messages 3 through 100.
+	putTiny(100)                                          // Chunk 1 holds tiny messages 3 through 100.
 	put(large(recvBufferCompactionMaxPayloadSize+1, 'A')) // Too large: passes through, closing chunk 1.
-	putTiny(100) // Chunk 2 holds all 100 tiny messages.
+	putTiny(100)                                          // Chunk 2 holds all 100 tiny messages.
 	// Exactly the eligibility threshold and fits in the remainder of chunk 2.
 	put(large(recvBufferCompactionMaxPayloadSize, 'B'))
 	// Does not fit in the remainder of chunk 2: opens chunk 3.
```

`gofmt -s -d -l` prints nothing for the committed file (0 bytes of diff, both from the working tree and from the blob piped via stdin), and nothing for the four affected package directories. The consecutive `putTiny(100)` / `put(large(...))` lines the claim points at (103-105) already have their trailing comments aligned the way gofmt wants. The positive control at the end shows the same gofmt binary does report a diff as soon as that alignment is disturbed.

## C9

**Claim:** default production stream construction leaves receive-buffer compaction disabled for at least one supported transport path. Branch: [evalon/grpc-go-tr-9253f1d4](https://github.com/kaitranntt-evals/grpc-go-transport-restrict-memory-overhead/tree/evalon/grpc-go-tr-9253f1d4). **Verdict: CONFIRMED** (the `serverHandlerTransport` path).

The branch enables compaction by assigning a field after `init()` in two of the three constructors:

```console
$ cd ~/wt/9253f1d4 && grep -n "buf.init()\|buf.compact" internal/transport/http2_client.go internal/transport/http2_server.go internal/transport/handler_server.go
internal/transport/http2_client.go:503:	s.Stream.buf.init()
internal/transport/http2_client.go:504:	s.Stream.buf.compact = envconfig.EnableReceiveBufferCompaction
internal/transport/http2_server.go:410:	s.Stream.buf.init()
internal/transport/http2_server.go:411:	s.Stream.buf.compact = envconfig.EnableReceiveBufferCompaction
internal/transport/handler_server.go:427:	s.Stream.buf.init()
```

`handler_server.go` is not touched by the branch (`git diff --stat c92e9857 HEAD` lists only envconfig.go, http2_client.go, http2_server.go, the new test and transport.go).

Repro: `verify/repro/c9_paths_test.go` instantiates a stream through each path under the default environment and queues 1,026 one-byte payloads: (1) `http2Client.newStream`; (2) a real `http2Server` accepting a stream from a real `http2Client` (`operateHeaders`); (3) `serverHandlerTransport.HandleStreams` via `NewServerHandlerTransport`, once with direct `put`s and once end-to-end by writing 1,026 one-byte chunks into the HTTP request body so that the transport's own `Body.Read` goroutine does the `put`s.

```sh
cd ~/wt/9253f1d4 && cp <repo>/verify/repro/c9_paths_test.go internal/transport/
go test -tags verify_audit -v -run '^TestC9_' google.golang.org/grpc/internal/transport -race -count=1
```

```console
    c9_paths_test.go:39: C9 http2Client.newStream: env="" queued=1026 one-byte payloads -> len(backlog)=0
--- PASS: TestC9_ClientNewStream (0.00s)
    c9_paths_test.go:75: C9 http2Server.operateHeaders: env="" queued=1026 one-byte payloads -> len(backlog)=0
--- PASS: TestC9_HTTP2ServerOperateHeaders (0.00s)
    c9_paths_test.go:91: C9 serverHandlerTransport.HandleStreams(direct put): env="" queued=1026 one-byte payloads -> len(backlog)=1025
    c9_paths_test.go:92: handler path NOT compacting: backlog=1025
--- FAIL: TestC9_HandlerServerDirectPut (0.00s)
    c9_paths_test.go:132: C9 serverHandlerTransport via request body: wrote 1026 one-byte chunks -> len(backlog)=1025
    c9_paths_test.go:134: handler path NOT compacting: backlog=1025
--- FAIL: TestC9_HandlerServerViaRequestBody (0.12s)
FAIL
FAIL	google.golang.org/grpc/internal/transport	0.147s
FAIL
```

For comparison, the same run with `GRPC_GO_EXPERIMENTAL_ENABLE_RECEIVE_BUFFER_COMPACTION=false` - the handler path under the default environment is indistinguishable from the escape hatch:

```console
    c9_paths_test.go:39: C9 http2Client.newStream: env="false" queued=1026 one-byte payloads -> len(backlog)=1025
    c9_paths_test.go:75: C9 http2Server.operateHeaders: env="false" queued=1026 one-byte payloads -> len(backlog)=1025
    c9_paths_test.go:91: C9 serverHandlerTransport.HandleStreams(direct put): env="false" queued=1026 one-byte payloads -> len(backlog)=1025
    c9_paths_test.go:132: C9 serverHandlerTransport via request body: wrote 1026 one-byte chunks -> len(backlog)=1025
```

**Impact reasoning (C9).** `serverHandlerTransport` is what `(*grpc.Server).ServeHTTP` uses, i.e. gRPC served through a `net/http` server. On that path a client that sends its request body in tiny pieces while the handler reads slowly still produces one backlog entry per piece (1,025 entries for 1,025 bytes above), which is precisely the behaviour the task set out to remove; "on by default" does not hold there. Nothing fails or logs; the branch's own tests and the fixture only build client/`http2Server` streams or bare `recvBuffer`s, so the gap is invisible. The fix is one line next to `s.Stream.buf.init()` in `HandleStreams`.

## C10

**Claim:** receive-buffer compaction acquires a capacity-1024 destination from the configured pool that is not returned when the resulting message buffer is freed. Branch: [evalon/grpc-go-tr-d4a3af3a](https://github.com/kaitranntt-evals/grpc-go-transport-restrict-memory-overhead/tree/evalon/grpc-go-tr-d4a3af3a). **Verdict: CONFIRMED** (for configured pools whose `Get(1024)` yields capacity <= 1024; not with the default pool).

Path: `openCompactionBuffer()` does `b.compactHandle = b.pool.Get(size)` with `size` starting at `recvBufferCompactionMinSize = 1 << 10`; `sealCompactionBuffer()` wraps it with `mem.NewBuffer(b.compactHandle, b.pool)`; `mem.NewBuffer` (`mem/buffers.go`) returns a plain `SliceBuffer` - whose `Free()` is a no-op - whenever `IsBelowBufferPoolingThreshold(cap(*data))`, i.e. `cap <= 1024`:

```go
	if pool == nil || IsBelowBufferPoolingThreshold(cap(*data)) {
		return (SliceBuffer)(*data)
	}
```

Repro: `verify/repro/c10_cap1024_test.go`. Streams are built with `(&http2Client{bufferPool: tracking}).newStream(...)`; the tracking pool wraps a contract-compliant pool and records every `Get`/`Put` by backing-array address; each delivered buffer is read and `Free()`d.

```sh
cd ~/wt/d4a3af3a && cp <repo>/verify/repro/c10_cap1024_test.go internal/transport/
go test -tags verify_audit -v -run '^TestC10_' google.golang.org/grpc/internal/transport -race -count=1
```

```console
    c10_cap1024_test.go:86: C10 pool=mem.NewTieredBufferPool(1024,4096,16384) frames=10x100B inOrder=true
              acquisitions=[Get(1024)->len=1024,cap=1024]
              delivered=[mem.SliceBuffer(len=100,cap=100) mem.SliceBuffer(len=100,cap=100) mem.SliceBuffer(len=800,cap=1024)]
              returned caps=[]
              NOT returned after Free() (caps)=[1024]
    c10_cap1024_test.go:94: tiered pool with a 1KiB tier: 1 acquired destination(s) never returned
    c10_cap1024_test.go:86: C10 pool=exact-length pool (make([]byte,n)) frames=10x100B inOrder=true
              acquisitions=[Get(1024)->len=1024,cap=1024]
              delivered=[mem.SliceBuffer(len=100,cap=100) mem.SliceBuffer(len=100,cap=100) mem.SliceBuffer(len=800,cap=1024)]
              returned caps=[]
              NOT returned after Free() (caps)=[1024]
    c10_cap1024_test.go:97: exact pool: 1 acquired destination(s) never returned
    c10_cap1024_test.go:86: C10 pool=mem.NewTieredBufferPool(1024,4096,16384) [3000 bytes] frames=30x100B inOrder=true
              acquisitions=[Get(1024)->len=1024,cap=1024 Get(2048)->len=2048,cap=4096]
              delivered=[mem.SliceBuffer(len=100,cap=100) mem.SliceBuffer(len=100,cap=100) mem.SliceBuffer(len=1024,cap=1024) *mem.buffer(len=1776,cap=4096)]
              returned caps=[4096]
              NOT returned after Free() (caps)=[1024]
    c10_cap1024_test.go:101: tiered pool, 3000 bytes: 1 acquired destination(s) never returned
--- FAIL: TestC10_Cap1024Destination (0.00s)
    c10_cap1024_test.go:86: C10 pool=mem.DefaultBufferPool() frames=10x100B inOrder=true
              acquisitions=[Get(1024)->len=1024,cap=4096]
              delivered=[mem.SliceBuffer(len=100,cap=100) mem.SliceBuffer(len=100,cap=100) *mem.buffer(len=800,cap=4096)]
              returned caps=[4096]
              NOT returned after Free() (caps)=[]
--- PASS: TestC10_DefaultPoolControl (0.00s)
FAIL
FAIL	google.golang.org/grpc/internal/transport	0.027s
FAIL
```

With `mem.NewTieredBufferPool(1024, 4096, 16384)` (public constructor) or an exact-size pool, the 1 KiB destination is acquired from the pool, delivered as a `mem.SliceBuffer`, and never `Put` back; the next (2 KiB request -> 4 KiB capacity) destination is returned normally. With `mem.DefaultBufferPool()` the smallest tier that fits 1,024 bytes is 4,096, the delivered buffer is a pooled `*mem.buffer`, and it is returned - so default configurations are unaffected.

The eval fixture reports the same thing on this branch:

```sh
cp $FIXTURE internal/transport/eval_recv_buffer_compaction_test.go
go test -v -run '^TestEval_RecvBufferConfiguredPoolAcquisition$' google.golang.org/grpc/internal/transport -race -count=1
```

```console
    eval_recv_buffer_compaction_test.go:757: Exact capacity pool: 1 acquired destination buffers <= 1024 bytes were abandoned and never returned to the pool (mem.NewBuffer SliceBuffer leak)
--- FAIL: TestEval_RecvBufferConfiguredPoolAcquisition (0.00s)
    --- PASS: TestEval_RecvBufferConfiguredPoolAcquisition/UnpooledConsolidation (0.00s)
    --- PASS: TestEval_RecvBufferConfiguredPoolAcquisition/SliceGrowthPoolOwnership (0.00s)
    --- FAIL: TestEval_RecvBufferConfiguredPoolAcquisition/ExactCapacitySmallDestination (0.00s)
FAIL
FAIL	google.golang.org/grpc/internal/transport	0.015s
FAIL
```

**Impact reasoning (C10).** Only applications that install a custom pool (`experimental.WithBufferPool` / `experimental.BufferPool`) with a size class of exactly <= 1 KiB capacity are affected: for them the first compaction destination of every backlog episode is taken from the pool and dropped to the GC instead of being recycled, so that tier never gets reuse from this path and any pool that accounts for outstanding buffers drifts upward by one per episode. Nothing is corrupted and the Go GC still reclaims the memory. `mem.Copy` avoids the same trap by not asking the pool at all below the threshold; the solution's sibling branches do likewise (e.g. `if b.pool == nil || mem.IsBelowBufferPoolingThreshold(size) { make(...) }`).

