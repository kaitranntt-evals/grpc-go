# Evidence — run v-16817709

Observations only. Layout used by every command below:

- audited checkout: `~/repos/grpc-go`, branch `verify/grpc-go-transport-restrict-memory-overhead-v-16817709` created from `origin/grpc-go-transport-restrict-memory-overhead-perfect` (commit `327a6ff9`); production code untouched.
- claim branches: fetched from `https://github.com/kaitranntt-evals/grpc-go-transport-restrict-memory-overhead` and checked out as detached worktrees `~/wt/<hash>` for branch `evalon/grpc-go-tr-<hash>`.
- the supplied fixture `tests/eval_recv_buffer_compaction_test.go` is stored byte-exact as `verify/repro/fixture/eval_recv_buffer_compaction_test.go.txt`.
- every repro script copies a probe into the worktree's `internal/transport/`, runs `go test`, and removes it / restores the worktree (`git checkout -- .`). Mutations of production code exist only inside those throw-away worktrees while a script runs.

Setup to replay:

```sh
cd ~/repos/grpc-go
git remote add claims https://github.com/kaitranntt-evals/grpc-go-transport-restrict-memory-overhead
for h in 3e360711 b0ce3c55 90f7b5c9 046d52ec 7cca1653 0d4ee617 1c3ba5c3 0e402c6c 1ee92409 136a5793 5d2a316b; do
  git fetch claims evalon/grpc-go-tr-$h && git worktree add --detach ~/wt/$h FETCH_HEAD
done
```

## C1

Claim: the added receive-memory regression tests accept a nonpositive enabled-mode heap delta as evidence of memory reduction, with no independent deterministic bound on enabled-mode overhead or queue depth. Adjudicated per branch with `verify/repro/c1/run.sh <worktree>`, which runs every test in the branch's added `recv_buffer*_test.go` three times:

- **A** unmodified;
- **B** production compaction neutralised (`envconfig.EnableReceiveBufferCompaction &&` → `false && envconfig.EnableReceiveBufferCompaction &&` in `internal/transport/transport.go`), so "enabled" mode buffers exactly like legacy mode — shows which assertions notice missing compaction at all;
- **C** B plus `verify/repro/c1/zz_c1_ballast_test.go.txt`: the test's *before* heap snapshot is taken while a 512 MiB allocation is live and released before the *after* snapshot (only in enabled mode), which makes the enabled-mode `HeapAlloc` delta nonpositive. If the suite passes in C, a nonpositive sample is accepted as proof of memory reduction although compaction is not happening, and nothing deterministic bounds enabled-mode overhead or queue depth.

Output filter: `--- PASS`/`PASS` lines are dropped below; everything else the script prints is verbatim.

Assertion text on each branch (enabled-mode heap arithmetic and the only bounds applied to it):

```sh
cd ~/wt; for h in 3e360711 b0ce3c55 90f7b5c9 046d52ec 7cca1653 0d4ee617 1c3ba5c3 0e402c6c; do f=$(ls $h/internal/transport/recv_buffer*_test.go); echo "== $f"; grep -nE "HeapAlloc\)? ?-|return 0|after < before|want <=|want at most|> [48]\*|\*4 >=|4\*max|outstanding != 1" $f; done
```

```console
== 3e360711/internal/transport/recv_buffer_compaction_test.go
220:	if after < before {
221:		return 0
248:		t.Errorf("Retained heap with compaction = %d bytes, want at most %d bytes", compacted, 4*len(payload))
== b0ce3c55/internal/transport/recv_buffer_compaction_test.go
120:		t.Errorf("Heap growth with compaction = %d bytes, want <= %d bytes", got, limit)
122:	if on, off := growth[true], growth[false]; off < 4*max(on, payloadLen) {
== 90f7b5c9/internal/transport/recv_buffer_test.go
108:					retained := int64(after.HeapAlloc) - int64(before.HeapAlloc)
112:					if enabled && retained > 4*payloadSize {
113:						t.Errorf("retained heap = %d, want at most %d with compaction", retained, 4*payloadSize)
== 046d52ec/internal/transport/recv_buffer_test.go
88:			retained := int64(after.HeapAlloc) - int64(before.HeapAlloc)
94:				if retained > 4*payload {
95:					t.Errorf("retained heap = %d, want at most %d with compaction", retained, 4*payload)
== 7cca1653/internal/transport/recv_buffer_test.go
49:			if retained[1]*4 >= retained[0] {
133:	retained := int64(after.HeapAlloc) - int64(before.HeapAlloc)
135:	if envconfig.EnableReceiveBufferCompaction && retained > 4*payloadSize {
136:		t.Errorf("retained heap = %d, want <= %d", retained, 4*payloadSize)
== 0d4ee617/internal/transport/recv_buffer_test.go
121:			retained := int64(after.HeapAlloc) - int64(before.HeapAlloc)
126:				if retained > 4*payloadSize {
127:					t.Errorf("retained heap = %d, want <= %d", retained, 4*payloadSize)
262:	if pool.outstanding != 1 { // Only the unmodified channel entry remains.
== 1c3ba5c3/internal/transport/recv_buffer_test.go
114:					retained := int64(after.HeapAlloc) - int64(before.HeapAlloc)
120:						if retained > 4*payloadBytes {
121:							t.Errorf("compacted heap growth = %d, want <= %d", retained, 4*payloadBytes)
== 0e402c6c/internal/transport/recv_buffer_test.go
99:			retained := int64(after.HeapAlloc) - int64(before.HeapAlloc)
102:			if mode == "default" && retained > 8*payload {
103:				t.Errorf("retained heap = %d bytes, want <= %d with compaction", retained, 8*payload)
```

None of the eight files asserts on `len(backlog)` or on a buffer count for the tiny-frame workload (`grep -n 'len(.*backlog' ~/wt/<hash>/internal/transport/recv_buffer*_test.go` prints nothing on all eight). On 3e360711 the helper clamps (`if after < before { return 0 }`); the other seven use a signed `int64(after.HeapAlloc) - int64(before.HeapAlloc)` compared only against an upper bound.

### C1 on [evalon/grpc-go-tr-3e360711](https://github.com/kaitranntt-evals/grpc-go-transport-restrict-memory-overhead/tree/evalon/grpc-go-tr-3e360711)

```sh
cd ~/repos/grpc-go && verify/repro/c1/run.sh ~/wt/3e360711
```

```console
===== branch worktree ~/wt/3e360711 @ 0070a65d; test file internal/transport/recv_buffer_compaction_test.go
### A: unmodified
    recv_buffer_compaction_test.go:239: Retained heap for 65534 unread bytes in 1-byte DATA frames: 62832 bytes with compaction, 4970176 bytes without
ok  	google.golang.org/grpc/internal/transport	3.013s
 1 file changed, 1 insertion(+), 1 deletion(-)
### B: production compaction neutralised (enabled mode == legacy buffering)
    recv_buffer_compaction_test.go:239: Retained heap for 65534 unread bytes in 1-byte DATA frames: 4970312 bytes with compaction, 4925640 bytes without
    recv_buffer_compaction_test.go:248: Retained heap with compaction = 4970312 bytes, want at most 262136 bytes
--- FAIL: Test (2.26s)
    --- FAIL: Test/ClientStream_TinyDataFramesBoundedMemory (1.04s)
FAIL
FAIL	google.golang.org/grpc/internal/transport	2.292s
FAIL
-	before := heapInUse()
+	before := c1Before(heapInUse)
### C: B + enabled-mode heap sample made nonpositive (ballast released between snapshots)
    recv_buffer_compaction_test.go:239: Retained heap for 65534 unread bytes in 1-byte DATA frames: 0 bytes with compaction, 4969136 bytes without
ok  	google.golang.org/grpc/internal/transport	3.440s
```

A passes; B fails only the heap assertion; C passes with the clamped sample logged as `0 bytes with compaction` while compaction is off.

### C1 on [evalon/grpc-go-tr-b0ce3c55](https://github.com/kaitranntt-evals/grpc-go-transport-restrict-memory-overhead/tree/evalon/grpc-go-tr-b0ce3c55)

```sh
cd ~/repos/grpc-go && verify/repro/c1/run.sh ~/wt/b0ce3c55
```

```console
===== branch worktree ~/wt/b0ce3c55 @ 56214910; test file internal/transport/recv_buffer_compaction_test.go
### A: unmodified
    recv_buffer_compaction_test.go:113: Heap growth while buffering 40005 bytes as 1-byte DATA frames: 3099648 bytes
    recv_buffer_compaction_test.go:113: Heap growth while buffering 40005 bytes as 1-byte DATA frames: 38656 bytes
ok  	google.golang.org/grpc/internal/transport	1.489s
 1 file changed, 1 insertion(+), 1 deletion(-)
### B: production compaction neutralised (enabled mode == legacy buffering)
    recv_buffer_compaction_test.go:113: Heap growth while buffering 40005 bytes as 1-byte DATA frames: 3107368 bytes
    recv_buffer_compaction_test.go:113: Heap growth while buffering 40005 bytes as 1-byte DATA frames: 3087456 bytes
    recv_buffer_compaction_test.go:120: Heap growth with compaction = 3087456 bytes, want <= 160000 bytes
    recv_buffer_compaction_test.go:123: Heap growth with compaction = 3087456 bytes, without = 3107368 bytes; want at least a 4x reduction
--- FAIL: Test (0.66s)
    --- FAIL: Test/ServerTransport_TinyDataFramesMemoryOverhead (0.61s)
FAIL
FAIL	google.golang.org/grpc/internal/transport	0.677s
FAIL
-	before := liveHeapBytes()
+	before := c1Before(liveHeapBytes)
### C: B + enabled-mode heap sample made nonpositive (ballast released between snapshots)
    recv_buffer_compaction_test.go:113: Heap growth while buffering 40005 bytes as 1-byte DATA frames: 3101936 bytes
    recv_buffer_compaction_test.go:113: Heap growth while buffering 40005 bytes as 1-byte DATA frames: -533778576 bytes
ok  	google.golang.org/grpc/internal/transport	1.796s
```

A passes; B fails only the heap assertions; C passes with an enabled-mode sample of `-533778576 bytes` (both `want <= 160000` and the `4x reduction` comparison accept it).

### C1 on [evalon/grpc-go-tr-90f7b5c9](https://github.com/kaitranntt-evals/grpc-go-transport-restrict-memory-overhead/tree/evalon/grpc-go-tr-90f7b5c9)

```sh
cd ~/repos/grpc-go && verify/repro/c1/run.sh ~/wt/90f7b5c9
```

```console
===== branch worktree ~/wt/90f7b5c9 @ 2a13b07f; test file internal/transport/recv_buffer_test.go
### A: unmodified
    recv_buffer_test.go:109: retained heap: 270824 bytes for 262140 unread payload bytes (1.03x)
    recv_buffer_test.go:109: retained heap: 270824 bytes for 262140 unread payload bytes (1.03x)
    recv_buffer_test.go:109: retained heap: 19911656 bytes for 262140 unread payload bytes (75.96x)
    recv_buffer_test.go:109: retained heap: 270568 bytes for 262140 unread payload bytes (1.03x)
    recv_buffer_test.go:109: retained heap: 270536 bytes for 262140 unread payload bytes (1.03x)
    recv_buffer_test.go:109: retained heap: 19894664 bytes for 262140 unread payload bytes (75.89x)
ok  	google.golang.org/grpc/internal/transport	5.836s
 1 file changed, 1 insertion(+), 1 deletion(-)
### B: production compaction neutralised (enabled mode == legacy buffering)
    recv_buffer_test.go:109: retained heap: 19906248 bytes for 262140 unread payload bytes (75.94x)
    recv_buffer_test.go:113: retained heap = 19906248, want at most 1048560 with compaction
    recv_buffer_test.go:109: retained heap: 19895048 bytes for 262140 unread payload bytes (75.89x)
    recv_buffer_test.go:113: retained heap = 19895048, want at most 1048560 with compaction
    recv_buffer_test.go:109: retained heap: 19894952 bytes for 262140 unread payload bytes (75.89x)
    recv_buffer_test.go:109: retained heap: 19894792 bytes for 262140 unread payload bytes (75.89x)
    recv_buffer_test.go:113: retained heap = 19894792, want at most 1048560 with compaction
    recv_buffer_test.go:109: retained heap: 19894664 bytes for 262140 unread payload bytes (75.89x)
    recv_buffer_test.go:113: retained heap = 19894664, want at most 1048560 with compaction
    recv_buffer_test.go:109: retained heap: 19894664 bytes for 262140 unread payload bytes (75.89x)
--- FAIL: Test (6.27s)
    --- FAIL: Test/ReceiveBufferCompactionMemory (6.19s)
        --- FAIL: Test/ReceiveBufferCompactionMemory/client (3.14s)
            --- FAIL: Test/ReceiveBufferCompactionMemory/client/default (1.09s)
            --- FAIL: Test/ReceiveBufferCompactionMemory/client/enabled (1.03s)
        --- FAIL: Test/ReceiveBufferCompactionMemory/server (2.94s)
            --- FAIL: Test/ReceiveBufferCompactionMemory/server/default (0.98s)
            --- FAIL: Test/ReceiveBufferCompactionMemory/server/enabled (1.00s)
FAIL
FAIL	google.golang.org/grpc/internal/transport	6.292s
FAIL
-					runtime.ReadMemStats(&before)
+					c1ReadBefore(&before)
### C: B + enabled-mode heap sample made nonpositive (ballast released between snapshots)
    recv_buffer_test.go:109: retained heap: -516964952 bytes for 262140 unread payload bytes (-1972.09x)
    recv_buffer_test.go:109: retained heap: -516975960 bytes for 262140 unread payload bytes (-1972.14x)
    recv_buffer_test.go:109: retained heap: 19894968 bytes for 262140 unread payload bytes (75.89x)
    recv_buffer_test.go:109: retained heap: -516976152 bytes for 262140 unread payload bytes (-1972.14x)
    recv_buffer_test.go:109: retained heap: -516976264 bytes for 262140 unread payload bytes (-1972.14x)
    recv_buffer_test.go:109: retained heap: 19894856 bytes for 262140 unread payload bytes (75.89x)
ok  	google.golang.org/grpc/internal/transport	7.866s
```

A passes; B fails only `ReceiveBufferCompactionMemory`; C passes with samples around `-516964952`.

### C1 on [evalon/grpc-go-tr-046d52ec](https://github.com/kaitranntt-evals/grpc-go-transport-restrict-memory-overhead/tree/evalon/grpc-go-tr-046d52ec)

```sh
cd ~/repos/grpc-go && verify/repro/c1/run.sh ~/wt/046d52ec
```

```console
===== branch worktree ~/wt/046d52ec @ 0fa05921; test file internal/transport/recv_buffer_test.go
### A: unmodified
ok  	google.golang.org/grpc/internal/transport	1.139s
            recv_buffer_test.go:90: 524288 one-byte DATA frames: 524288 unread payload bytes, 537680 retained heap bytes (1.03x)
            recv_buffer_test.go:90: 524288 one-byte DATA frames: 524288 unread payload bytes, 556568 retained heap bytes (1.06x)
            recv_buffer_test.go:90: 524288 one-byte DATA frames: 524288 unread payload bytes, 39999344 retained heap bytes (76.29x)
            recv_buffer_test.go:90: 524288 one-byte DATA frames: 524288 unread payload bytes, 39985000 retained heap bytes (76.27x)
ok  	google.golang.org/grpc/internal/transport	9.481s
 1 file changed, 1 insertion(+), 1 deletion(-)
### B: production compaction neutralised (enabled mode == legacy buffering)
ok  	google.golang.org/grpc/internal/transport	1.099s
            recv_buffer_test.go:90: 524288 one-byte DATA frames: 524288 unread payload bytes, 39999120 retained heap bytes (76.29x)
            recv_buffer_test.go:95: retained heap = 39999120, want at most 2097152 with compaction
            recv_buffer_test.go:90: 524288 one-byte DATA frames: 524288 unread payload bytes, 39990312 retained heap bytes (76.28x)
            recv_buffer_test.go:95: retained heap = 39990312, want at most 2097152 with compaction
        --- FAIL: TestReceiveBufferCompactionMemory (3.91s)
            --- FAIL: TestReceiveBufferCompactionMemory/client (1.79s)
            --- FAIL: TestReceiveBufferCompactionMemory/server (2.09s)
        FAIL
            recv_buffer_test.go:90: 524288 one-byte DATA frames: 524288 unread payload bytes, 39999920 retained heap bytes (76.29x)
            recv_buffer_test.go:90: 524288 one-byte DATA frames: 524288 unread payload bytes, 39990312 retained heap bytes (76.28x)
--- FAIL: TestReceiveBufferCompactionMemory (8.71s)
    --- FAIL: TestReceiveBufferCompactionMemory/default (3.93s)
FAIL
FAIL	google.golang.org/grpc/internal/transport	8.719s
FAIL
-			runtime.ReadMemStats(&before)
+			c1ReadBefore(&before)
### C: B + enabled-mode heap sample made nonpositive (ballast released between snapshots)
ok  	google.golang.org/grpc/internal/transport	1.102s
            recv_buffer_test.go:90: 524288 one-byte DATA frames: 524288 unread payload bytes, -496866048 retained heap bytes (-947.70x)
            recv_buffer_test.go:90: 524288 one-byte DATA frames: 524288 unread payload bytes, -496883528 retained heap bytes (-947.73x)
            recv_buffer_test.go:90: 524288 one-byte DATA frames: 524288 unread payload bytes, 40004904 retained heap bytes (76.30x)
            recv_buffer_test.go:90: 524288 one-byte DATA frames: 524288 unread payload bytes, 39984904 retained heap bytes (76.27x)
ok  	google.golang.org/grpc/internal/transport	13.315s
```

A passes; B fails only `TestReceiveBufferCompactionMemory`; C passes with samples around `-496866048`.

### C1 on [evalon/grpc-go-tr-7cca1653](https://github.com/kaitranntt-evals/grpc-go-transport-restrict-memory-overhead/tree/evalon/grpc-go-tr-7cca1653)

```sh
cd ~/repos/grpc-go && verify/repro/c1/run.sh ~/wt/7cca1653
```

```console
===== branch worktree ~/wt/7cca1653 @ ca628084; test file internal/transport/recv_buffer_test.go
### A: unmodified
    recv_buffer_test.go:46: 262144 unread payload bytes retain 20006408 heap bytes
    recv_buffer_test.go:46: 262144 unread payload bytes retain 267408 heap bytes
    recv_buffer_test.go:46: 262144 unread payload bytes retain 19989992 heap bytes
    recv_buffer_test.go:46: 262144 unread payload bytes retain 267408 heap bytes
ok  	google.golang.org/grpc/internal/transport	5.296s
 1 file changed, 1 insertion(+), 1 deletion(-)
### B: production compaction neutralised (enabled mode == legacy buffering)
    recv_buffer_test.go:46: 262144 unread payload bytes retain 19989992 heap bytes
    recv_buffer_test.go:46: 262144 unread payload bytes retain 19989896 heap bytes
    recv_buffer_test.go:46: retained heap = 19989896, want <= 1048576
    recv_buffer_test.go:50: retained heap with compaction = 19989896, without = 19989992; want at least 4x reduction
    recv_buffer_test.go:46: 262144 unread payload bytes retain 19989800 heap bytes
    recv_buffer_test.go:46: 262144 unread payload bytes retain 19989896 heap bytes
    recv_buffer_test.go:46: retained heap = 19989896, want <= 1048576
    recv_buffer_test.go:50: retained heap with compaction = 19989896, without = 19989800; want at least 4x reduction
--- FAIL: Test (8.74s)
    --- FAIL: Test/ReceiveBufferTinyFramesMemory (8.44s)
        --- FAIL: Test/ReceiveBufferTinyFramesMemory/server=false (5.14s)
            --- FAIL: Test/ReceiveBufferTinyFramesMemory/server=false/compact=true (2.66s)
        --- FAIL: Test/ReceiveBufferTinyFramesMemory/server=true (3.30s)
            --- FAIL: Test/ReceiveBufferTinyFramesMemory/server=true/compact=true (1.20s)
FAIL
FAIL	google.golang.org/grpc/internal/transport	8.772s
FAIL
-	runtime.ReadMemStats(&before)
+	c1ReadBefore(&before)
### C: B + enabled-mode heap sample made nonpositive (ballast released between snapshots)
    recv_buffer_test.go:46: 262144 unread payload bytes retain 19989800 heap bytes
    recv_buffer_test.go:46: 262144 unread payload bytes retain -516881112 heap bytes
    recv_buffer_test.go:46: 262144 unread payload bytes retain 19989816 heap bytes
    recv_buffer_test.go:46: 262144 unread payload bytes retain -516881112 heap bytes
ok  	google.golang.org/grpc/internal/transport	6.028s
```

A passes; B fails only `ReceiveBufferTinyFramesMemory`; C passes with `-516881112`.

### C1 on [evalon/grpc-go-tr-0d4ee617](https://github.com/kaitranntt-evals/grpc-go-transport-restrict-memory-overhead/tree/evalon/grpc-go-tr-0d4ee617)

```sh
cd ~/repos/grpc-go && verify/repro/c1/run.sh ~/wt/0d4ee617
```

```console
===== branch worktree ~/wt/0d4ee617 @ 40f1b2c9; test file internal/transport/recv_buffer_test.go
### A: unmodified
    recv_buffer_test.go:122: compaction=true: retained 267408 bytes for 262144 unread payload bytes
    recv_buffer_test.go:122: compaction=true: retained 267408 bytes for 262144 unread payload bytes
ok  	google.golang.org/grpc/internal/transport	2.416s
 1 file changed, 1 insertion(+), 1 deletion(-)
### B: production compaction neutralised (enabled mode == legacy buffering)
    recv_buffer_test.go:263: outstanding pooled buffers = 3, want 1
    recv_buffer_test.go:122: compaction=true: retained 20006280 bytes for 262144 unread payload bytes
    recv_buffer_test.go:127: retained heap = 20006280, want <= 1048576
    recv_buffer_test.go:122: compaction=true: retained 19989896 bytes for 262144 unread payload bytes
    recv_buffer_test.go:127: retained heap = 19989896, want <= 1048576
--- FAIL: Test (1.74s)
    --- FAIL: Test/ReceiveBufferCompactionOwnership (0.00s)
    --- FAIL: Test/ReceiveBufferTinyFrames (1.66s)
        --- FAIL: Test/ReceiveBufferTinyFrames/client (0.76s)
        --- FAIL: Test/ReceiveBufferTinyFrames/server (0.79s)
FAIL
FAIL	google.golang.org/grpc/internal/transport	1.762s
FAIL
-			runtime.ReadMemStats(&before)
+			c1ReadBefore(&before)
### C: B + enabled-mode heap sample made nonpositive (ballast released between snapshots)
    recv_buffer_test.go:263: outstanding pooled buffers = 3, want 1
    recv_buffer_test.go:122: compaction=true: retained -516875608 bytes for 262144 unread payload bytes
    recv_buffer_test.go:122: compaction=true: retained -516881112 bytes for 262144 unread payload bytes
--- FAIL: Test (2.26s)
    --- FAIL: Test/ReceiveBufferCompactionOwnership (0.00s)
FAIL
FAIL	google.golang.org/grpc/internal/transport	2.302s
FAIL
```

A passes. B and C both still fail `TestReceiveBufferCompactionOwnership` (`outstanding pooled buffers = 3, want 1`): that test counts pooled buffers after three small puts, so a *total* absence of compaction is caught deterministically. The heap test itself passes in C with `-516875608`. See the second experiment below for whether that 3-buffer count bounds memory overhead.

Second experiment on this branch — `recvBufferCompactionLimit` 4096 → 11, so compaction still merges the ownership test's two small buffers but chunks never exceed 11 bytes (D1), then the same mutant with the nonpositive sample (D2):

```sh
cd ~/repos/grpc-go && verify/repro/c1/run_0d4ee617_partial.sh ~/wt/0d4ee617
```

```console
-const recvBufferCompactionLimit = 4 * 1024
+const recvBufferCompactionLimit = 11
### D1: compaction chunks capped at 11 bytes
    recv_buffer_test.go:122: compaction=true: retained 1741392 bytes for 262144 unread payload bytes
    recv_buffer_test.go:127: retained heap = 1741392, want <= 1048576
    recv_buffer_test.go:122: compaction=true: retained 1741392 bytes for 262144 unread payload bytes
    recv_buffer_test.go:127: retained heap = 1741392, want <= 1048576
--- FAIL: Test (1.42s)
    --- PASS: Test/ReceiveBufferCompactionConcurrent (0.07s)
        --- PASS: Test/ReceiveBufferCompactionConcurrent/enabled=true (0.04s)
        --- PASS: Test/ReceiveBufferCompactionConcurrent/enabled=false (0.03s)
    --- PASS: Test/ReceiveBufferCompactionOwnership (0.00s)
    --- FAIL: Test/ReceiveBufferTinyFrames (1.33s)
        --- FAIL: Test/ReceiveBufferTinyFrames/client (0.62s)
        --- FAIL: Test/ReceiveBufferTinyFrames/server (0.58s)
--- PASS: Test (0.00s)
FAIL
FAIL	google.golang.org/grpc/internal/transport	1.434s
FAIL
-			runtime.ReadMemStats(&before)
+			c1ReadBefore(&before)
### D2: D1 + enabled-mode heap sample made nonpositive (ballast released between snapshots)
    recv_buffer_test.go:122: compaction=true: retained -535129520 bytes for 262144 unread payload bytes
    recv_buffer_test.go:122: compaction=true: retained -535124016 bytes for 262144 unread payload bytes
--- PASS: Test (1.77s)
    --- PASS: Test/ReceiveBufferCompactionConcurrent (0.06s)
        --- PASS: Test/ReceiveBufferCompactionConcurrent/enabled=true (0.03s)
        --- PASS: Test/ReceiveBufferCompactionConcurrent/enabled=false (0.03s)
    --- PASS: Test/ReceiveBufferCompactionOwnership (0.00s)
    --- PASS: Test/ReceiveBufferTinyFrames (1.70s)
        --- PASS: Test/ReceiveBufferTinyFrames/client (0.88s)
        --- PASS: Test/ReceiveBufferTinyFrames/server (0.72s)
--- PASS: Test (0.00s)
PASS
ok  	google.golang.org/grpc/internal/transport	2.808s
```

D1: the mutant retains 1,741,392 bytes for 262,144 payload bytes (6.6x, above the test's own 4x limit) and only the heap assertion notices. D2: with a nonpositive sample every added test passes, including `ReceiveBufferCompactionOwnership`. The 3-buffer ownership count therefore does not bound enabled-mode memory overhead or queue depth for the tiny-frame workload; the heap sample is the only evidence of the size of the reduction and it accepts `-535129520`.

### C1 on [evalon/grpc-go-tr-1c3ba5c3](https://github.com/kaitranntt-evals/grpc-go-transport-restrict-memory-overhead/tree/evalon/grpc-go-tr-1c3ba5c3)

```sh
cd ~/repos/grpc-go && verify/repro/c1/run.sh ~/wt/1c3ba5c3
```

```console
===== branch worktree ~/wt/1c3ba5c3 @ 25d933ce; test file internal/transport/recv_buffer_test.go
### A: unmodified
    recv_buffer_test.go:116: retained heap: 20011232 bytes for 262144 unread payload bytes
    recv_buffer_test.go:116: retained heap: 264880 bytes for 262144 unread payload bytes
    recv_buffer_test.go:116: retained heap: 19989120 bytes for 262144 unread payload bytes
    recv_buffer_test.go:116: retained heap: 264848 bytes for 262144 unread payload bytes
ok  	google.golang.org/grpc/internal/transport	4.622s
 1 file changed, 1 insertion(+), 1 deletion(-)
### B: production compaction neutralised (enabled mode == legacy buffering)
    recv_buffer_test.go:116: retained heap: 19994752 bytes for 262144 unread payload bytes
    recv_buffer_test.go:116: retained heap: 19989216 bytes for 262144 unread payload bytes
    recv_buffer_test.go:121: compacted heap growth = 19989216, want <= 1048576
    recv_buffer_test.go:116: retained heap: 19989120 bytes for 262144 unread payload bytes
    recv_buffer_test.go:116: retained heap: 19989408 bytes for 262144 unread payload bytes
    recv_buffer_test.go:121: compacted heap growth = 19989408, want <= 1048576
--- FAIL: Test (5.04s)
    --- FAIL: Test/ReceiveBufferCompactionMemory (4.86s)
        --- FAIL: Test/ReceiveBufferCompactionMemory/client=false (2.55s)
            --- FAIL: Test/ReceiveBufferCompactionMemory/client=false/enabled=true (1.12s)
        --- FAIL: Test/ReceiveBufferCompactionMemory/client=true (2.29s)
            --- FAIL: Test/ReceiveBufferCompactionMemory/client=true/enabled=true (1.27s)
FAIL
FAIL	google.golang.org/grpc/internal/transport	5.064s
FAIL
-					runtime.ReadMemStats(&before)
+					c1ReadBefore(&before)
### C: B + enabled-mode heap sample made nonpositive (ballast released between snapshots)
    recv_buffer_test.go:116: retained heap: 19989152 bytes for 262144 unread payload bytes
    recv_buffer_test.go:116: retained heap: -516881792 bytes for 262144 unread payload bytes
    recv_buffer_test.go:116: retained heap: 19989120 bytes for 262144 unread payload bytes
    recv_buffer_test.go:116: retained heap: -516881792 bytes for 262144 unread payload bytes
ok  	google.golang.org/grpc/internal/transport	5.748s
```

A passes; B fails only `ReceiveBufferCompactionMemory`; C passes with `-516881792`.

### C1 on [evalon/grpc-go-tr-0e402c6c](https://github.com/kaitranntt-evals/grpc-go-transport-restrict-memory-overhead/tree/evalon/grpc-go-tr-0e402c6c)

```sh
cd ~/repos/grpc-go && verify/repro/c1/run.sh ~/wt/0e402c6c
```

```console
===== branch worktree ~/wt/0e402c6c @ c500b9bf; test file internal/transport/recv_buffer_test.go
### A: unmodified
            recv_buffer_test.go:101: default: 262140 unread payload bytes, 269784 retained heap bytes (1.0x)
            recv_buffer_test.go:101: default: 262140 unread payload bytes, 269752 retained heap bytes (1.0x)
            recv_buffer_test.go:101: disabled: 262140 unread payload bytes, 19916120 retained heap bytes (76.0x)
            recv_buffer_test.go:101: disabled: 262140 unread payload bytes, 19893896 retained heap bytes (75.9x)
ok  	google.golang.org/grpc/internal/transport	6.976s
 1 file changed, 1 insertion(+), 1 deletion(-)
### B: production compaction neutralised (enabled mode == legacy buffering)
            recv_buffer_test.go:101: default: 262140 unread payload bytes, 19921912 retained heap bytes (76.0x)
            recv_buffer_test.go:103: retained heap = 19921912 bytes, want <= 2097120 with compaction
            recv_buffer_test.go:101: default: 262140 unread payload bytes, 19894072 retained heap bytes (75.9x)
            recv_buffer_test.go:103: retained heap = 19894072 bytes, want <= 2097120 with compaction
        --- FAIL: Test (2.62s)
            --- FAIL: Test/ReceiveBufferCompactionMemory (2.61s)
                --- FAIL: Test/ReceiveBufferCompactionMemory/client (1.37s)
                --- FAIL: Test/ReceiveBufferCompactionMemory/server (1.24s)
        FAIL
            recv_buffer_test.go:101: disabled: 262140 unread payload bytes, 19921832 retained heap bytes (76.0x)
            recv_buffer_test.go:101: disabled: 262140 unread payload bytes, 19894072 retained heap bytes (75.9x)
--- FAIL: Test (6.82s)
    --- FAIL: Test/ReceiveBufferCompactionMemory (6.60s)
        --- FAIL: Test/ReceiveBufferCompactionMemory/default (2.64s)
FAIL
FAIL	google.golang.org/grpc/internal/transport	6.847s
FAIL
-			runtime.ReadMemStats(&before)
+			c1ReadBefore(&before)
### C: B + enabled-mode heap sample made nonpositive (ballast released between snapshots)
            recv_buffer_test.go:101: default: 262140 unread payload bytes, -516965912 retained heap bytes (-1972.1x)
            recv_buffer_test.go:101: default: 262140 unread payload bytes, -516977144 retained heap bytes (-1972.1x)
            recv_buffer_test.go:101: disabled: 262140 unread payload bytes, 19916424 retained heap bytes (76.0x)
            recv_buffer_test.go:101: disabled: 262140 unread payload bytes, 19893864 retained heap bytes (75.9x)
ok  	google.golang.org/grpc/internal/transport	8.947s
```

A passes; B fails only `ReceiveBufferCompactionMemory`; C passes with samples around `-516965912`.

Impact reasoning: on all eight branches the only assertion that speaks to enabled-mode memory is `heapDelta <= k*payload` on a `runtime.MemStats.HeapAlloc` difference. A delta that is zero or negative (unrelated garbage or pooled memory released between the two snapshots — `runtime.GC()` runs between them in every test) satisfies it trivially, so the regression test can pass while proving nothing; experiment C demonstrates a pass with compaction switched off (seven branches) or badly degraded (0d4ee617). There is no cheap deterministic companion such as `len(backlog) <= N` or a retained-capacity sum. The production fix is not affected; the risk is a silently vacuous regression test.

## C2

Target: [evalon/grpc-go-tr-1ee92409](https://github.com/kaitranntt-evals/grpc-go-transport-restrict-memory-overhead/tree/evalon/grpc-go-tr-1ee92409). Claim: added receive-buffer tests execute stream reads with no effective local deadline / independently timed cancellation.

Trace: the branch's helper `newTestRecvStream()` builds the reader with `context.Background()` and no `ctxDone`; `TestRecvBuffer_CompactionPreservesData` and `TestRecvBuffer_CompactionReducesMemory` read through it with `st.readTo(...)`. (`TestClientTransport_ManyTinyDataFrames` instead uses a real client stream bound to a `defaultTestTimeout` context.)

```sh
cd ~/wt/1ee92409 && grep -n '^func newTestRecvStream' -A8 internal/transport/recv_buffer_compaction_test.go && grep -n 'newTestRecvStream()\|st.readTo\|stream.readTo\|WithTimeout' internal/transport/recv_buffer_compaction_test.go
```

```console
73:func newTestRecvStream() *Stream {
74-	s := &Stream{readRequester: &fakeReadRequester{}}
75-	s.buf.init()
76-	s.trReader = transportReader{
77-		reader:        recvBufferReader{ctx: context.Background(), recv: &s.buf},
78-		windowHandler: &mockWindowUpdater{f: func(int) {}},
79-	}
80-	return s
81-}
73:func newTestRecvStream() *Stream {
98:			st := newTestRecvStream()
135:			if _, err := st.readTo(got); err != nil {
141:			if _, err := st.readTo(make([]byte, 1)); !errors.Is(err, io.EOF) {
157:	st := newTestRecvStream()
183:		if _, err := st.readTo(got); err != nil {
243:			ctx, cancel := context.WithTimeout(context.Background(), defaultTestTimeout)
265:			if _, err := stream.readTo(got); err != nil {
266:				t.Fatalf("stream.readTo() failed: %v", err)
271:			if _, err := stream.readTo(make([]byte, 1)); !errors.Is(err, io.EOF) {
272:				t.Fatalf("stream.readTo() = %v, want %v", err, io.EOF)
```

Observed behaviour — `verify/repro/c2/run.sh` (1) runs a probe on the helper stream that reads one byte more than was queued, and (2) makes production compaction drop one payload byte and runs the branch's own tests with `go test -timeout 30s`:

```sh
cd ~/repos/grpc-go && verify/repro/c2/run.sh ~/wt/1ee92409
```

```console
### 1: probe
    zz_c2_probe_test.go:18: reader ctx=context.Background hasDeadline=false ctx.Done()==nil:true reader.ctxDone==nil:true
    zz_c2_probe_test.go:31: readTo still blocked after 3s with nothing able to interrupt it
--- PASS: TestC2Probe_HelperStreamReadHasNoDeadline (3.01s)
PASS
ok  	google.golang.org/grpc/internal/transport	3.018s
-	b.compacted = append(b.compacted, buf.ReadOnlyData()...)
+	d := buf.ReadOnlyData()
+	if len(b.compacted) == 4096 {
+		d = d[:0] // C2 MUTATION: lose one payload
+	}
+	b.compacted = append(b.compacted, d...)
### 2a: TestRecvBuffer_CompactionReducesMemory with a lost byte (-timeout 30s)
panic: test timed out after 30s
	running tests:
		Test/RecvBuffer_CompactionReducesMemory (30s)
google.golang.org/grpc/internal/transport.(*recvBufferReader).read(0xc0001f20e0, 0xefff)
google.golang.org/grpc/internal/transport.(*Stream).readTo(0x10000?, {0xc00048f780, 0x10000, 0x10000})
google.golang.org/grpc/internal/transport.s.TestRecvBuffer_CompactionReducesMemory.func1(0x0?)
	~/wt/1ee92409/internal/transport/recv_buffer_compaction_test.go:183 +0x7c
google.golang.org/grpc/internal/transport.s.TestRecvBuffer_CompactionReducesMemory({{}}, 0xc000182fc0)
	~/wt/1ee92409/internal/transport/recv_buffer_compaction_test.go:195 +0x62
FAIL	google.golang.org/grpc/internal/transport	30.107s
FAIL

real	0m31.890s
user	0m3.949s
sys	0m0.488s
### 2b: TestClientTransport_ManyTinyDataFrames/compaction=true with a lost byte (-timeout 30s)
    recv_buffer_compaction_test.go:255: Backlog length after 60000 one-byte DATA frames: 2
    recv_buffer_compaction_test.go:266: stream.readTo() failed: EOF
--- FAIL: Test (0.16s)
    --- FAIL: Test/ClientTransport_ManyTinyDataFrames (0.15s)
        --- FAIL: Test/ClientTransport_ManyTinyDataFrames/compaction=true (0.15s)
--- PASS: Test (0.00s)
FAIL
FAIL	google.golang.org/grpc/internal/transport	0.164s
FAIL

real	0m0.612s
user	0m0.813s
sys	0m0.358s
```

(1) the helper stream's reader context is `context.Background` with `hasDeadline=false`, `ctx.Done()==nil` and `reader.ctxDone==nil`; a read short of data is still blocked after 3 s. (2a) with one byte lost, `TestRecvBuffer_CompactionReducesMemory` does not fail locally: it blocks in `Stream.readTo` (`recv_buffer_compaction_test.go:183`) until the *global* go-test timeout panics the binary after 30 s (10 minutes with the default `-timeout`). (2b) for contrast, the context-bound `TestClientTransport_ManyTinyDataFrames` fails locally in 0.16 s.

Impact reasoning: a data-loss regression in compaction turns two of the three added tests into a hang that is only ended by the package-wide timeout, which kills every other test in `internal/transport` and reports a goroutine dump instead of an assertion. Ordinary trigger (any bug that loses or withholds bytes); workaround is `-timeout`.

## C3

Target: [evalon/grpc-go-tr-136a5793](https://github.com/kaitranntt-evals/grpc-go-transport-restrict-memory-overhead/tree/evalon/grpc-go-tr-136a5793). Claim: repeated three-byte receive/drain cycles retain a separate 256-byte backing allocation per cycle's delivered output.


Direct probe (`TestProbeC3_ThreeByteCycles` in `verify/repro/probes/zz_probe_test.go.txt`): 1,024 cycles of three one-byte `put`s followed by `recvBufferReader.Read` until the three bytes are delivered, all delivered buffers retained; run on a stream from a transport built by `NewHTTP2Client` (package helper `setUp`) + `NewStream`, and on `http2Client.newStream`:

```sh
cd ~/repos/grpc-go && verify/repro/probes/run.sh ~/wt/136a5793 '^TestProbeC3_'
```

```console
    zz_probe_test.go:288: NewHTTP2Client+NewStream: 1024 cycles x 3 one-byte frames (3072 payload bytes), 3072 delivered buffers retained; distinct backing arrays by capacity: cap=1:2048 cap=256:1024; total retained capacity=264192 bytes
    zz_probe_test.go:288: http2Client.newStream: 1024 cycles x 3 one-byte frames (3072 payload bytes), 3072 delivered buffers retained; distinct backing arrays by capacity: cap=1:2048 cap=256:1024; total retained capacity=264192 bytes
--- PASS: TestProbeC3_ThreeByteCycles (0.00s)
```

Fixture on this branch (`verify/repro/fixture/run.sh ~/wt/136a5793 rename`). The byte-exact fixture does not compile there because the branch's own `recv_buffer_test.go` already declares `newTestRecvBuffer`; the second run renames that one helper inside the fixture copy (`sed -i s/newTestRecvBuffer/evalNewTestRecvBuffer/g`), nothing else changed:

```console
$ go test -v -run '^TestEval_' google.golang.org/grpc/internal/transport -race -count=1
internal/transport/recv_buffer_test.go:54:6: newTestRecvBuffer redeclared in this block
	internal/transport/eval_recv_buffer_compaction_test.go:832:6: other declaration of newTestRecvBuffer
FAIL	google.golang.org/grpc/internal/transport [build failed]
FAIL
$ (after sed -i s/newTestRecvBuffer/evalNewTestRecvBuffer/g on the fixture copy) same command
--- PASS: TestEval_RecvBufferCompaction (0.00s)
    eval_recv_buffer_compaction_test.go:186: ProductionStream constructor already verified
--- PASS: TestEval_RecvBufferCompactionDisabled (0.00s)
    --- PASS: TestEval_RecvBufferCompactionDisabled/ProductionStream (0.00s)
    --- SKIP: TestEval_RecvBufferCompactionDisabled/ComponentBuffer (0.00s)
--- PASS: TestEval_RecvBufferCompactionSkippedLargeBuffer (0.02s)
    --- PASS: TestEval_RecvBufferCompactionSkippedLargeBuffer/PoolOwnership_SliceGrowth (0.00s)
--- PASS: TestEval_RecvBufferErrorResetSafety (0.00s)
--- PASS: TestEval_RecvBufferCompaction_MixedFrames (0.00s)
    eval_recv_buffer_compaction_test.go:573: Distinct retained capacity 264192 exceeded limit 77824 (payload: 3072 bytes)
--- FAIL: TestEval_RecvBufferCompaction_MultiCycleMemoryBound (0.02s)
    --- PASS: TestEval_RecvBufferCompaction_MultiCycleMemoryBound/MultiCycleBursts (0.01s)
    --- FAIL: TestEval_RecvBufferCompaction_MultiCycleMemoryBound/IncrementalMessageAssembly (0.01s)
    eval_recv_buffer_compaction_test.go:629: Compaction destination buffer was never acquired from the configured buffer pool
--- FAIL: TestEval_RecvBufferConfiguredPoolAcquisition (0.00s)
    --- FAIL: TestEval_RecvBufferConfiguredPoolAcquisition/UnpooledConsolidation (0.00s)
    --- PASS: TestEval_RecvBufferConfiguredPoolAcquisition/SliceGrowthPoolOwnership (0.00s)
    --- PASS: TestEval_RecvBufferConfiguredPoolAcquisition/ExactCapacitySmallDestination (0.00s)
FAIL
FAIL	google.golang.org/grpc/internal/transport	0.059s
FAIL
$ GRPC_GO_EXPERIMENTAL_ENABLE_RECEIVE_BUFFER_COMPACTION=false go test -v -run '^TestEval_RecvBufferCompactionDisabled$' google.golang.org/grpc/internal/transport -race -count=1
    eval_recv_buffer_compaction_test.go:186: ProductionStream constructor already verified
--- PASS: TestEval_RecvBufferCompactionDisabled (0.00s)
    --- PASS: TestEval_RecvBufferCompactionDisabled/ProductionStream (0.00s)
    --- SKIP: TestEval_RecvBufferCompactionDisabled/ComponentBuffer (0.00s)
PASS
ok  	google.golang.org/grpc/internal/transport	1.015s
```

Each cycle delivers three buffers: two one-byte inputs and one buffer whose backing array has capacity 256 (the minimum compaction block, `recvBufferMinCompactionBlockSize = 256`, allocated when the third byte arrives behind a non-empty backlog and sealed with one byte in it when the reader drains). 1,024 cycles → 1,024 distinct 256-byte arrays, 264,192 bytes retained for 3,072 payload bytes (86x); the fixture's `IncrementalMessageAssembly` bound (77,824) fails with the same number.

Impact reasoning: a reader that keeps delivered buffers alive while receiving a trickle of tiny frames (a message assembled from many small DATA frames, read slightly slower than they arrive) pins a 256-byte block per 1-byte tail, i.e. the overhead compaction was meant to remove reappears for the "reader almost keeps up" pattern. Bounded per cycle (256 B), released when the buffers are freed.

## C4

Target: [evalon/grpc-go-tr-136a5793](https://github.com/kaitranntt-evals/grpc-go-transport-restrict-memory-overhead/tree/evalon/grpc-go-tr-136a5793). Claim: consolidating unpooled small buffers bypasses the configured buffer pool for destination storage.


Direct probe (`TestProbeC4_UnpooledConsolidation`): `http2Client{bufferPool: <recording pool>}.newStream`, then N one-byte `mem.SliceBuffer` (unpooled) messages; the pool records every `Get`:

```sh
cd ~/repos/grpc-go && verify/repro/probes/run.sh ~/wt/136a5793 '^TestProbeC4_'
```

```console
    zz_probe_test.go:303: n=1026 unpooled one-byte puts: len(backlog)=3, delivered 5 buffers with lengths [2x1 256 257 511]; configured-pool Get calls=0 sizes=[]
    zz_probe_test.go:303: n=1033 unpooled one-byte puts: len(backlog)=4, delivered 6 buffers with lengths [2x1 256 257 514 4]; configured-pool Get calls=1 sizes=[1028]
    zz_probe_test.go:303: n=6000 unpooled one-byte puts: len(backlog)=5, delivered 7 buffers with lengths [2x1 256 257 514 4096 875]; configured-pool Get calls=2 sizes=[1028 5124]
--- PASS: TestProbeC4_UnpooledConsolidation (0.00s)
```

Fixture (`TestEval_RecvBufferConfiguredPoolAcquisition/UnpooledConsolidation`, `verify/repro/fixture/run.sh ~/wt/136a5793 rename`):

```console
$ go test -v -run '^TestEval_' google.golang.org/grpc/internal/transport -race -count=1
internal/transport/recv_buffer_test.go:54:6: newTestRecvBuffer redeclared in this block
	internal/transport/eval_recv_buffer_compaction_test.go:832:6: other declaration of newTestRecvBuffer
FAIL	google.golang.org/grpc/internal/transport [build failed]
$ (after sed -i s/newTestRecvBuffer/evalNewTestRecvBuffer/g on the fixture copy) same command
    eval_recv_buffer_compaction_test.go:629: Compaction destination buffer was never acquired from the configured buffer pool
--- FAIL: TestEval_RecvBufferConfiguredPoolAcquisition (0.00s)
    --- FAIL: TestEval_RecvBufferConfiguredPoolAcquisition/UnpooledConsolidation (0.00s)
    --- PASS: TestEval_RecvBufferConfiguredPoolAcquisition/SliceGrowthPoolOwnership (0.00s)
    --- PASS: TestEval_RecvBufferConfiguredPoolAcquisition/ExactCapacitySmallDestination (0.00s)
$ GRPC_GO_EXPERIMENTAL_ENABLE_RECEIVE_BUFFER_COMPACTION=false go test -v -run '^TestEval_RecvBufferCompactionDisabled$' google.golang.org/grpc/internal/transport -race -count=1
```

With the claim's workload (1,026 one-byte unpooled messages) the backlog is consolidated to 3 entries (blocks of 256, 257, 511 bytes) and the configured pool sees **0** `Get` calls: `newTail` uses `make([]byte, size)` whenever `mem.IsBelowBufferPoolingThreshold(size)` (size ≤ 1024). The pool is used only once a block larger than 1,024 bytes is needed (n=1033 → one `Get(1028)`; n=6000 → `Get(1028)`, `Get(5124)`).

Impact reasoning: for streams whose backlog stays under ~1 KiB, consolidation memory comes from the Go heap rather than the user-configured `mem.BufferPool`, so a custom pool used for accounting or reuse does not see it. This mirrors the mem package's own "don't pool ≤1 KiB" convention, so it is a deliberate trade-off rather than a leak; it matters only where the configured pool is expected to see every destination allocation (the fixture's expectation).

## C5

Targets: [evalon/grpc-go-tr-5d2a316b](https://github.com/kaitranntt-evals/grpc-go-transport-restrict-memory-overhead/tree/evalon/grpc-go-tr-5d2a316b) and [evalon/grpc-go-tr-136a5793](https://github.com/kaitranntt-evals/grpc-go-transport-restrict-memory-overhead/tree/evalon/grpc-go-tr-136a5793). Claim: a burst of small frames totalling 1,025 queued bytes stays unconsolidated.


Direct probe (`TestProbeC5_Burst1025`): the transport is built by the production constructor `NewHTTP2Client` (via the package's `setUp` helper), the stream by `ct.NewStream`; 1,025 one-byte messages are `put` and the backlog inspected, then drained.

### C5 on [evalon/grpc-go-tr-5d2a316b](https://github.com/kaitranntt-evals/grpc-go-transport-restrict-memory-overhead/tree/evalon/grpc-go-tr-5d2a316b)

```sh
cd ~/repos/grpc-go && verify/repro/probes/run.sh ~/wt/5d2a316b '^TestProbeC5_'
cd ~/repos/grpc-go && verify/repro/probes/run.sh ~/wt/5d2a316b '^TestProbeC5_' GRPC_GO_EXPERIMENTAL_ENABLE_RECEIVE_BUFFER_COMPACTION=false   # contrast
```

```console
    zz_probe_test.go:110: env="" after 1025 one-byte puts: len(backlog)=2 len(chan)=1
    zz_probe_test.go:112: drained 4 message buffers for 1025 payload bytes; lengths in delivery order: [1 256 512 256]
    zz_probe_test.go:113: drained bytes == input bytes: true
--- PASS: TestProbeC5_Burst1025 (0.00s)
    zz_probe_test.go:110: env="false" after 1025 one-byte puts: len(backlog)=1024 len(chan)=1
    zz_probe_test.go:112: drained 1025 message buffers for 1025 payload bytes; lengths in delivery order: [1025x1]
    zz_probe_test.go:113: drained bytes == input bytes: true
--- PASS: TestProbeC5_Burst1025 (0.00s)
```

### C5 on [evalon/grpc-go-tr-136a5793](https://github.com/kaitranntt-evals/grpc-go-transport-restrict-memory-overhead/tree/evalon/grpc-go-tr-136a5793)

```sh
cd ~/repos/grpc-go && verify/repro/probes/run.sh ~/wt/136a5793 '^TestProbeC5_'
cd ~/repos/grpc-go && verify/repro/probes/run.sh ~/wt/136a5793 '^TestProbeC5_' GRPC_GO_EXPERIMENTAL_ENABLE_RECEIVE_BUFFER_COMPACTION=false   # contrast
```

```console
    zz_probe_test.go:110: env="" after 1025 one-byte puts: len(backlog)=3 len(chan)=1
    zz_probe_test.go:112: drained 5 message buffers for 1025 payload bytes; lengths in delivery order: [2x1 256 257 510]
    zz_probe_test.go:113: drained bytes == input bytes: true
--- PASS: TestProbeC5_Burst1025 (0.00s)
    zz_probe_test.go:110: env="false" after 1025 one-byte puts: len(backlog)=1024 len(chan)=1
    zz_probe_test.go:112: drained 1025 message buffers for 1025 payload bytes; lengths in delivery order: [1025x1]
    zz_probe_test.go:113: drained bytes == input bytes: true
--- PASS: TestProbeC5_Burst1025 (0.00s)
```

5d2a316b: 1,025 bytes are held in 1 channel entry + 2 backlog chunks (+ an open tail), delivered as 4 buffers `[1 256 512 256]`. 136a5793: 3 backlog entries, delivered as `[1 1 256 257 510]`. Both: bytes equal to input; with the env var set to `false` the same burst leaves 1,024 individual backlog entries.


Fixture on 5d2a316b (`verify/repro/fixture/run.sh ~/wt/5d2a316b`) — it *does* fail there, which is the likely origin of the claim:

```console
$ go test -v -run '^TestEval_' google.golang.org/grpc/internal/transport -race -count=1
    eval_recv_buffer_compaction_test.go:149: Got backlog length 1025 after compaction, want <= 64
--- FAIL: TestEval_RecvBufferCompaction (0.00s)
    eval_recv_buffer_compaction_test.go:179: ProductionStream: got backlog length 1025, want <= 64 (compaction enabled)
    eval_recv_buffer_compaction_test.go:186: ProductionStream constructor already verified
--- FAIL: TestEval_RecvBufferCompactionDisabled (0.00s)
    --- FAIL: TestEval_RecvBufferCompactionDisabled/ProductionStream (0.00s)
    --- SKIP: TestEval_RecvBufferCompactionDisabled/ComponentBuffer (0.00s)
--- PASS: TestEval_RecvBufferCompactionSkippedLargeBuffer (0.01s)
    --- PASS: TestEval_RecvBufferCompactionSkippedLargeBuffer/PoolOwnership_SliceGrowth (0.00s)
--- PASS: TestEval_RecvBufferErrorResetSafety (0.00s)
    eval_recv_buffer_compaction_test.go:419: Got backlog length 1073 after mixed frame compaction, want <= 134
--- FAIL: TestEval_RecvBufferCompaction_MixedFrames (0.00s)
    eval_recv_buffer_compaction_test.go:498: Cycle 0: backlog length 1123 exceeded bound 512
    eval_recv_buffer_compaction_test.go:573: Distinct retained capacity 263168 exceeded limit 77824 (payload: 3072 bytes)
--- FAIL: TestEval_RecvBufferCompaction_MultiCycleMemoryBound (0.00s)
    --- FAIL: TestEval_RecvBufferCompaction_MultiCycleMemoryBound/MultiCycleBursts (0.00s)
    --- FAIL: TestEval_RecvBufferCompaction_MultiCycleMemoryBound/IncrementalMessageAssembly (0.00s)
    eval_recv_buffer_compaction_test.go:629: Compaction destination buffer was never acquired from the configured buffer pool
--- FAIL: TestEval_RecvBufferConfiguredPoolAcquisition (0.00s)
    --- FAIL: TestEval_RecvBufferConfiguredPoolAcquisition/UnpooledConsolidation (0.00s)
    --- PASS: TestEval_RecvBufferConfiguredPoolAcquisition/SliceGrowthPoolOwnership (0.00s)
    --- PASS: TestEval_RecvBufferConfiguredPoolAcquisition/ExactCapacitySmallDestination (0.00s)
FAIL
FAIL	google.golang.org/grpc/internal/transport	0.038s
FAIL
$ GRPC_GO_EXPERIMENTAL_ENABLE_RECEIVE_BUFFER_COMPACTION=false go test -v -run '^TestEval_RecvBufferCompactionDisabled$' google.golang.org/grpc/internal/transport -race -count=1
    eval_recv_buffer_compaction_test.go:186: ProductionStream constructor already verified
--- PASS: TestEval_RecvBufferCompactionDisabled (0.00s)
    --- PASS: TestEval_RecvBufferCompactionDisabled/ProductionStream (0.00s)
    --- SKIP: TestEval_RecvBufferCompactionDisabled/ComponentBuffer (0.00s)
PASS
ok  	google.golang.org/grpc/internal/transport	1.015s
```

The fixture builds its stream from a struct literal, `client := &http2Client{bufferPool: pool}; client.newStream(...)`. On 5d2a316b the enable flag is a transport field copied from `envconfig.EnableReceiveBufferCompaction` inside `NewHTTP2Client` / `NewServerTransport` / `NewServerHandlerTransport` (`compactRecvBuffer: envconfig.EnableReceiveBufferCompaction`), so a hand-built literal has it `false` and never compacts. Through the delivered constructors (the probe above) the burst is consolidated. The fixture failure is an artefact of bypassing the constructor, not the delivered behaviour.

```sh
cd ~/wt/5d2a316b && grep -n 'compactRecvBuffer' internal/transport/*.go | grep -v _test
```

```console
internal/transport/handler_server.go:103:		compactRecvBuffer: envconfig.EnableReceiveBufferCompaction,
internal/transport/handler_server.go:179:	// compactRecvBuffer enables coalescing of small request body reads
internal/transport/handler_server.go:181:	compactRecvBuffer bool
internal/transport/handler_server.go:432:	s.Stream.buf.init(ht.bufferPool, ht.compactRecvBuffer)
internal/transport/http2_client.go:156:	// compactRecvBuffer enables coalescing of small DATA frame payloads
internal/transport/http2_client.go:158:	compactRecvBuffer bool
internal/transport/http2_client.go:366:		compactRecvBuffer:     envconfig.EnableReceiveBufferCompaction,
internal/transport/http2_client.go:507:	s.Stream.buf.init(t.bufferPool, t.compactRecvBuffer)
internal/transport/http2_server.go:129:	// compactRecvBuffer enables coalescing of small DATA frame payloads
internal/transport/http2_server.go:131:	compactRecvBuffer bool
internal/transport/http2_server.go:281:		compactRecvBuffer: envconfig.EnableReceiveBufferCompaction,
internal/transport/http2_server.go:414:	s.Stream.buf.init(t.bufferPool, t.compactRecvBuffer)
```

## C6

Targets: [evalon/grpc-go-tr-5d2a316b](https://github.com/kaitranntt-evals/grpc-go-transport-restrict-memory-overhead/tree/evalon/grpc-go-tr-5d2a316b) and [evalon/grpc-go-tr-136a5793](https://github.com/kaitranntt-evals/grpc-go-transport-restrict-memory-overhead/tree/evalon/grpc-go-tr-136a5793). Parts: *mixed_frame_consolidation* (small payloads stay unconsolidated) and *mixed_frame_ordering* (drained bytes differ from input order).


Direct probe (`TestProbeC6_Mixed`), transport from `NewHTTP2Client` + `NewStream`: (a) the fixture's mix of 1,074 frames of 1–7 bytes; (b) 900 frames of 1/3/7 bytes interleaved with 2 KiB, 20 KiB and 300 KiB frames. Reports backlog length, delivered buffer lengths, and whether the drained byte sequence equals the concatenated input.

### C6 on [evalon/grpc-go-tr-5d2a316b](https://github.com/kaitranntt-evals/grpc-go-transport-restrict-memory-overhead/tree/evalon/grpc-go-tr-5d2a316b)

```sh
cd ~/repos/grpc-go && verify/repro/probes/run.sh ~/wt/5d2a316b '^TestProbeC6_'
```

```console
    zz_probe_test.go:134: a/1..7-byte-mix: put 1074 frames (1074 of them <=64 bytes, 4290 bytes total): len(backlog)=3 len(chan)=1
    zz_probe_test.go:136: a/1..7-byte-mix: drained 5 message buffers; lengths in delivery order: [1 256 512 1024 2497]
    zz_probe_test.go:137: a/1..7-byte-mix: drained byte sequence == concatenated input: true (4290 vs 4290 bytes)
    zz_probe_test.go:134: b/small+2KiB+20KiB+300KiB: put 909 frames (900 of them <=64 bytes, 969444 bytes total): len(backlog)=15 len(chan)=1
    zz_probe_test.go:136: b/small+2KiB+20KiB+300KiB: drained 17 message buffers; lengths in delivery order: [1 256 512 1024 816 20000 330 300000 2818 20000 330 300000 2818 20000 330 300000 209]
    zz_probe_test.go:137: b/small+2KiB+20KiB+300KiB: drained byte sequence == concatenated input: true (969444 vs 969444 bytes)
--- PASS: TestProbeC6_Mixed (0.01s)
```

### C6 on [evalon/grpc-go-tr-136a5793](https://github.com/kaitranntt-evals/grpc-go-transport-restrict-memory-overhead/tree/evalon/grpc-go-tr-136a5793)

```sh
cd ~/repos/grpc-go && verify/repro/probes/run.sh ~/wt/136a5793 '^TestProbeC6_'
```

```console
    zz_probe_test.go:134: a/1..7-byte-mix: put 1074 frames (1074 of them <=64 bytes, 4290 bytes total): len(backlog)=4 len(chan)=1
    zz_probe_test.go:136: a/1..7-byte-mix: drained 6 message buffers; lengths in delivery order: [1 2 256 258 516 3257]
    zz_probe_test.go:137: a/1..7-byte-mix: drained byte sequence == concatenated input: true (4290 vs 4290 bytes)
    zz_probe_test.go:134: b/small+2KiB+20KiB+300KiB: put 909 frames (900 of them <=64 bytes, 969444 bytes total): len(backlog)=16 len(chan)=1
    zz_probe_test.go:136: b/small+2KiB+20KiB+300KiB: drained 18 message buffers; lengths in delivery order: [1 3 256 259 518 1572 20000 330 300000 2818 20000 330 300000 2818 20000 330 300000 209]
    zz_probe_test.go:137: b/small+2KiB+20KiB+300KiB: drained byte sequence == concatenated input: true (969444 vs 969444 bytes)
--- PASS: TestProbeC6_Mixed (0.01s)
```

Contrast on 5d2a316b with `GRPC_GO_EXPERIMENTAL_ENABLE_RECEIVE_BUFFER_COMPACTION=false` (same command plus the env assignment): `len(backlog)=1073` and `908`, order still equal.

```console
    zz_probe_test.go:134: a/1..7-byte-mix: put 1074 frames (1074 of them <=64 bytes, 4290 bytes total): len(backlog)=1073 len(chan)=1
    zz_probe_test.go:136: a/1..7-byte-mix: drained 1074 message buffers; lengths in delivery order: [1 2 3 4 5 6 7 1 2 3 4 5 6 7 1 2 3 4 5 6 7 1 2 3 4 5 6 7 1 2 3 4 5 6 7 1 2 3 4 5 6 7 1 2 3 4 5 6 7 1 …[line truncated]
    zz_probe_test.go:137: a/1..7-byte-mix: drained byte sequence == concatenated input: true (4290 vs 4290 bytes)
    zz_probe_test.go:134: b/small+2KiB+20KiB+300KiB: put 909 frames (900 of them <=64 bytes, 969444 bytes total): len(backlog)=908 len(chan)=1
    zz_probe_test.go:136: b/small+2KiB+20KiB+300KiB: drained 909 message buffers; lengths in delivery order: [1 3 7 1 3 7 1 3 7 1 3 7 1 3 7 1 3 7 1 3 7 1 3 7 1 3 7 1 3 7 1 3 7 1 3 7 1 3 7 1 3 7 1 3 7  …[line truncated]
    zz_probe_test.go:137: b/small+2KiB+20KiB+300KiB: drained byte sequence == concatenated input: true (969444 vs 969444 bytes)
```

Both parts, both branches: small payloads are consolidated (1,074 frames → 3–4 backlog entries; 909 frames → 15–16 entries with the 20 KiB / 300 KiB frames passed through intact between consolidated chunks) and the complete drained byte sequence equals the concatenated input. The fixture's `TestEval_RecvBufferCompaction_MixedFrames` passes on 136a5793 and fails on 5d2a316b (`Got backlog length 1073 after mixed frame compaction, want <= 134`) only because of the struct-literal construction: the fixture builds `&http2Client{bufferPool: pool}` by hand, and on 5d2a316b the enable flag is only set inside `NewHTTP2Client` / `NewServerTransport` / `NewServerHandlerTransport` (`verify/repro/fixture/run.sh ~/wt/5d2a316b`).

## C7

Target: [evalon/grpc-go-tr-136a5793](https://github.com/kaitranntt-evals/grpc-go-transport-restrict-memory-overhead/tree/evalon/grpc-go-tr-136a5793). Claim: EOF or a terminal error becomes observable before all previously queued payload bytes are delivered.


Direct probe (`TestProbeC7_Terminal`): N one-byte payloads then `io.EOF` or a non-EOF error, on `http2Client.newStream`; drained (1) from the raw channel/backlog, recording the position of the terminal message, and (2) through `recvBufferReader.Read`, the reader production uses:

```sh
cd ~/repos/grpc-go && verify/repro/probes/run.sh ~/wt/136a5793 '^TestProbeC7_'
```

```console
    zz_probe_test.go:352: n=1 term=EOF | channel: terminal is message 2 of 2, payload bytes delivered before it=1/1, equal=true | reader: 1/1 bytes (equal=true) then err=EOF
    zz_probe_test.go:352: n=2 term=EOF | channel: terminal is message 3 of 3, payload bytes delivered before it=2/2, equal=true | reader: 2/2 bytes (equal=true) then err=EOF
    zz_probe_test.go:352: n=3 term=EOF | channel: terminal is message 4 of 4, payload bytes delivered before it=3/3, equal=true | reader: 3/3 bytes (equal=true) then err=EOF
    zz_probe_test.go:352: n=300 term=EOF | channel: terminal is message 5 of 5, payload bytes delivered before it=300/300, equal=true | reader: 300/300 bytes (equal=true) then err=EOF
    zz_probe_test.go:352: n=1074 term=EOF | channel: terminal is message 7 of 7, payload bytes delivered before it=1074/1074, equal=true | reader: 1074/1074 bytes (equal=true) then err=EOF
    zz_probe_test.go:352: n=5000 term=EOF | channel: terminal is message 7 of 7, payload bytes delivered before it=5000/5000, equal=true | reader: 5000/5000 bytes (equal=true) then err=EOF
    zz_probe_test.go:352: n=3 term=probe terminal error | channel: terminal is message 4 of 4, payload bytes delivered before it=3/3, equal=true | reader: 3/3 bytes (equal=true) then err=probe terminal error
    zz_probe_test.go:352: n=1074 term=probe terminal error | channel: terminal is message 7 of 7, payload bytes delivered before it=1074/1074, equal=true | reader: 1074/1074 bytes (equal=true) then err=probe terminal error
    zz_probe_test.go:352: n=5000 term=probe terminal error | channel: terminal is message 7 of 7, payload bytes delivered before it=5000/5000, equal=true | reader: 5000/5000 bytes (equal=true) then err=probe terminal error
--- PASS: TestProbeC7_Terminal (0.00s)
```

Fixture `TestEval_RecvBufferErrorResetSafety` on this branch (run with `verify/repro/fixture/run.sh ~/wt/136a5793 rename`; the fixture's `newTestRecvBuffer` helper is renamed because the branch's own test file declares the same name): 

```console
$ (after sed -i s/newTestRecvBuffer/evalNewTestRecvBuffer/g on the fixture copy) same command
--- PASS: TestEval_RecvBufferErrorResetSafety (0.00s)
```

In all nine cases the terminal is the last message and every queued byte (N/N, contents equal) is delivered before it, on both drain paths.

## C8

Target: [evalon/grpc-go-tr-136a5793](https://github.com/kaitranntt-evals/grpc-go-transport-restrict-memory-overhead/tree/evalon/grpc-go-tr-136a5793). Claim: `GRPC_GO_EXPERIMENTAL_ENABLE_RECEIVE_BUFFER_COMPACTION=false` does not disable consolidation.


Direct probe (`TestProbeC8_EnvDisabled`, plus `TestProbeC5_Burst1025`): process started with the variable set to `false`; transport from `NewHTTP2Client`, stream from `NewStream`; 1,026 one-byte messages; reports backlog length and how many delivered buffers are the *original* input buffers at the same FIFO position. Second run without the variable for contrast.

```sh
cd ~/repos/grpc-go && verify/repro/probes/run.sh ~/wt/136a5793 '^TestProbeC(8|5)_' GRPC_GO_EXPERIMENTAL_ENABLE_RECEIVE_BUFFER_COMPACTION=false
cd ~/repos/grpc-go && verify/repro/probes/run.sh ~/wt/136a5793 '^TestProbeC8_'
```

```console
    zz_probe_test.go:110: env="false" after 1025 one-byte puts: len(backlog)=1024 len(chan)=1
    zz_probe_test.go:112: drained 1025 message buffers for 1025 payload bytes; lengths in delivery order: [1025x1]
    zz_probe_test.go:113: drained bytes == input bytes: true
--- PASS: TestProbeC5_Burst1025 (0.00s)
    zz_probe_test.go:170: env="false" after 1026 one-byte puts: len(backlog)=1025 len(chan)=1
    zz_probe_test.go:178: drained 1026 message buffers; 1026 of them are the original input buffer at the same FIFO position; lengths: [1026x1]
--- PASS: TestProbeC8_EnvDisabled (0.00s)
PASS
ok  	google.golang.org/grpc/internal/transport	0.008s
    zz_probe_test.go:170: env="" after 1026 one-byte puts: len(backlog)=3 len(chan)=1
    zz_probe_test.go:178: drained 5 message buffers; 2 of them are the original input buffer at the same FIFO position; lengths: [2x1 256 257 511]
--- PASS: TestProbeC8_EnvDisabled (0.00s)
```

Fixture command from the claim (run with `verify/repro/fixture/run.sh ~/wt/136a5793 rename`; the fixture's `newTestRecvBuffer` helper is renamed because the branch's own test file declares the same name):

```console
$ GRPC_GO_EXPERIMENTAL_ENABLE_RECEIVE_BUFFER_COMPACTION=false go test -v -run '^TestEval_RecvBufferCompactionDisabled$' google.golang.org/grpc/internal/transport -race -count=1
    eval_recv_buffer_compaction_test.go:186: ProductionStream constructor already verified
--- PASS: TestEval_RecvBufferCompactionDisabled (0.00s)
    --- PASS: TestEval_RecvBufferCompactionDisabled/ProductionStream (0.00s)
    --- SKIP: TestEval_RecvBufferCompactionDisabled/ComponentBuffer (0.00s)
PASS
ok  	google.golang.org/grpc/internal/transport	1.015s
```

With the variable `false`: `len(backlog)=1025` (+1 in the channel) and all 1,026 delivered buffers are the original one-byte input buffers in FIFO order. Without it: 3 backlog entries. Compaction is disabled by the variable.

## C9

Target: [evalon/grpc-go-tr-136a5793](https://github.com/kaitranntt-evals/grpc-go-transport-restrict-memory-overhead/tree/evalon/grpc-go-tr-136a5793). Parts: *large_buffer_preservation* and *growth_pool_ownership*.


Direct probes on `http2Client.newStream`: `TestProbeC9_LargePreserved` queues 2 or 1,025 small frames, then a 16 KiB / 64 KiB / 1 MiB pooled buffer, then one more small frame, and checks the delivered large message is the same `mem.Buffer` with the same backing pointer and contents. `TestProbeC9_GrowthOwnership` uses a pool that records every `Get`/`Put` by backing pointer (foreign puts, capacity-mismatched puts, buffers delivered to the reader after already being returned, and buffers never returned) over four destination-growth workloads.

```sh
cd ~/repos/grpc-go && verify/repro/probes/run.sh ~/wt/136a5793 '^TestProbeC9_'
```

```console
    zz_probe_test.go:303: n=1026 unpooled one-byte puts: len(backlog)=3, delivered 5 buffers with lengths [2x1 256 257 511]; configured-pool Get calls=0 sizes=[]
    zz_probe_test.go:303: n=1033 unpooled one-byte puts: len(backlog)=4, delivered 6 buffers with lengths [2x1 256 257 514 4]; configured-pool Get calls=1 sizes=[1028]
    zz_probe_test.go:303: n=6000 unpooled one-byte puts: len(backlog)=5, delivered 7 buffers with lengths [2x1 256 257 514 4096 875]; configured-pool Get calls=2 sizes=[1028 5124]
    zz_probe_test.go:383: 2 small frames queued, then 16384-byte buffer: delivered as one 16384-byte message=true, same mem.Buffer and backing pointer=true, contents intact=true
    zz_probe_test.go:383: 2 small frames queued, then 65536-byte buffer: delivered as one 65536-byte message=true, same mem.Buffer and backing pointer=true, contents intact=true
    zz_probe_test.go:383: 2 small frames queued, then 1048576-byte buffer: delivered as one 1048576-byte message=true, same mem.Buffer and backing pointer=true, contents intact=true
    zz_probe_test.go:383: 1025 small frames queued, then 16384-byte buffer: delivered as one 16384-byte message=true, same mem.Buffer and backing pointer=true, contents intact=true
    zz_probe_test.go:383: 1025 small frames queued, then 65536-byte buffer: delivered as one 65536-byte message=true, same mem.Buffer and backing pointer=true, contents intact=true
    zz_probe_test.go:383: 1025 small frames queued, then 1048576-byte buffer: delivered as one 1048576-byte message=true, same mem.Buffer and backing pointer=true, contents intact=true
--- PASS: TestProbeC9_LargePreserved (0.00s)
    zz_probe_test.go:436: 6000x1B: Get calls=2 (size:count map[1028:1 5124:1]) Put calls=2 outstanding after all delivered buffers freed=0 foreign puts=0 cap-mismatched puts=0 delivered-while-already-returned=0; 7 buffers delivered, bytes equal=true
    zz_probe_test.go:436: occupy+8KiB+5x1B: Get calls=2 (size:count map[8192:2]) Put calls=2 outstanding after all delivered buffers freed=0 foreign puts=0 cap-mismatched puts=0 delivered-while-already-returned=0; 3 buffers delivered, bytes equal=true
    zz_probe_test.go:436: 20000 mixed 1..700B: Get calls=425 (size:count map[1032:1 5128:1 16384:423]) Put calls=425 outstanding after all delivered buffers freed=0 foreign puts=0 cap-mismatched puts=0 delivered-while-already-returned=0; 430 buffers delivered, bytes equal=true
    zz_probe_test.go:436: 3000x1B then terminal error (undrained): Get calls=1 (size:count map[1028:1]) Put calls=1 outstanding after all delivered buffers freed=0 foreign puts=0 cap-mismatched puts=0 delivered-while-already-returned=0; 7 buffers delivered, bytes equal=true
--- PASS: TestProbeC9_GrowthOwnership (0.03s)
```

Fixture (`TestEval_RecvBufferCompactionSkippedLargeBuffer` incl. `PoolOwnership_SliceGrowth`, and `SliceGrowthPoolOwnership` / `ExactCapacitySmallDestination`; fixture helper `newTestRecvBuffer` renamed via `verify/repro/fixture/run.sh ~/wt/136a5793 rename`):

```console
$ (after sed -i s/newTestRecvBuffer/evalNewTestRecvBuffer/g on the fixture copy) same command
--- PASS: TestEval_RecvBufferCompactionSkippedLargeBuffer (0.02s)
    --- PASS: TestEval_RecvBufferCompactionSkippedLargeBuffer/PoolOwnership_SliceGrowth (0.00s)
    --- PASS: TestEval_RecvBufferConfiguredPoolAcquisition/SliceGrowthPoolOwnership (0.00s)
    --- PASS: TestEval_RecvBufferConfiguredPoolAcquisition/ExactCapacitySmallDestination (0.00s)
```

*large_buffer_preservation*: all three sizes are delivered as the original buffer (same object, same backing pointer, contents intact) — payloads above `recvBufferCompactionThreshold = 4096` bypass compaction. *growth_pool_ownership*: in every workload `Get calls == Put calls`, outstanding = 0, foreign = 0, capacity-mismatched = 0, delivered-while-already-returned = 0 (Get counts include pooled input buffers created by `mem.Copy` for payloads > 1 KiB).

## C10

Target: [evalon/grpc-go-tr-046d52ec](https://github.com/kaitranntt-evals/grpc-go-transport-restrict-memory-overhead/tree/evalon/grpc-go-tr-046d52ec). Claim (about what the added tests assert): no asserted sequential or explicitly synchronized interleaving in which a read precedes later data on the same live receive buffer, or precedes a terminal transition while unread data remains.


Assertion text — `TestReceiveBufferCompactionReads` reuses one `recvBuffer` across three put → read-everything → put rounds, asserting payload bytes on every read, then queues `abc`/`def`/`ghi` + EOF on the same buffer and asserts delivery before EOF:

```sh
cd ~/wt/046d52ec && sed -n '180,181p;190,191p;203,205p;208,223p;241,246p;260,262p' internal/transport/recv_buffer_test.go
```

```go
			var recv recvBuffer
			recv.init()
			for round := 0; round < 3; round++ {
				want = want[:0]
					recv.put(recvMsg{buffer: buf.Slice(0, size)})
				}
				for offset := 0; offset < len(want); {
						header := make([]byte, min(5, len(want)-offset))
						n, err := reader.ReadMessageHeader(header)
						if err != nil || !bytes.Equal(header[:n], want[offset:offset+n]) {
							t.Fatalf("ReadMessageHeader at %d = %v, %v", offset, header[:n], err)
						}
						offset += n
					} else {
						buf, err := reader.Read(min(137, len(want)-offset))
						if err != nil {
							t.Fatal(err)
						}
						n := buf.Len()
						if !bytes.Equal(buf.ReadOnlyData(), want[offset:offset+n]) {
							t.Fatalf("Read at %d returned incorrect data", offset)
						}
						buf.Free()
			recv.put(recvMsg{buffer: mem.SliceBuffer("abc")})
			recv.put(recvMsg{buffer: mem.SliceBuffer("def")})
			recv.put(recvMsg{buffer: mem.SliceBuffer("ghi")})
			recv.put(recvMsg{err: io.EOF})
			// Later data and errors must be discarded, without losing data
			// queued before the terminal error.
				buf.Free()
			}
			if string(got) != "abcdefghi" {
```

Demonstration (`verify/repro/c10/run.sh`): run the test unmodified, then with a production mutant that zeroes only payload bytes compacted *after the first `load()`* — i.e. only data that arrives after a read on the same live buffer. Rounds 2 and 3 are the only place such data exists in this test.

```sh
cd ~/repos/grpc-go && verify/repro/c10/run.sh ~/wt/046d52ec
```

```console
### 1: unmodified
--- PASS: Test (0.02s)
    --- PASS: Test/ReceiveBufferCompactionReads (0.01s)
        --- PASS: Test/ReceiveBufferCompactionReads/enabled=true (0.00s)
        --- PASS: Test/ReceiveBufferCompactionReads/enabled=false (0.00s)
--- PASS: Test (0.00s)
PASS
ok  	google.golang.org/grpc/internal/transport	1.031s
+	c10ReadSeen      bool // C10 MUTATION
-			tail.buffer = mem.SliceBuffer(append(data, r.buffer.ReadOnlyData()...))
+			add := r.buffer.ReadOnlyData()
+			if b.c10ReadSeen {
+				add = make([]byte, len(add)) // C10 MUTATION: corrupt data compacted after a read
+			}
+			tail.buffer = mem.SliceBuffer(append(data, add...))
+	b.c10ReadSeen = true // C10 MUTATION
### 2: mutant (only data put after a read is corrupted)
    recv_buffer_test.go:221: Read at 1 returned incorrect data
        	~/wt/046d52ec/internal/transport/recv_buffer_test.go:193
--- FAIL: Test (0.02s)
    --- FAIL: Test/ReceiveBufferCompactionReads (0.01s)
        --- FAIL: Test/ReceiveBufferCompactionReads/enabled=true (0.00s)
        --- PASS: Test/ReceiveBufferCompactionReads/enabled=false (0.00s)
--- PASS: Test (0.00s)
FAIL
FAIL	google.golang.org/grpc/internal/transport	0.034s
FAIL
```

The mutant is caught by the sequential-interleaving assertion (`recv_buffer_test.go:221: Read at 1 returned incorrect data`, `enabled=true` only). The enclosing `for round := 0; round < 3; round++` loop is a sequential read-then-later-data interleaving on one live buffer with asserted payload delivery (successive cycles qualify per the claim's own refute wording), and the trailing block asserts a terminal transition with unread data queued. (`TestReceiveBufferCompactionConcurrent` is unsynchronized apart from `Gosched`, so it does not by itself establish an ordering.)

## C11

Target: the audited implementation ([grpc-go-transport-restrict-memory-overhead-perfect](https://github.com/kaitranntt-evals/grpc-go/tree/grpc-go-transport-restrict-memory-overhead-perfect), commit `327a6ff9`). Parts: *backing_storage_accounting* and *handler_short_read_trigger*.


Source of the estimate and of the handler read loop:

```sh
cd ~/repos/grpc-go && grep -n 'uncompactedBytes += r.buffer.Len()\|backlogHeapSize := \|if backlogHeapSize <= compactionThreshold\|compactionThreshold = ' internal/transport/transport.go && grep -n 'ht.bufferPool.Get(http2MaxFrameLen)' -A4 internal/transport/handler_server.go
```

```console
73:	compactionThreshold = imem.BufferPoolingThreshold * (recvMsgSize + 1)
152:	b.uncompactedBytes += r.buffer.Len()
153:	backlogHeapSize := b.uncompactedSuffixLen*recvMsgSize + b.uncompactedBytes
165:	if backlogHeapSize <= compactionThreshold {
440:			buf := ht.bufferPool.Get(http2MaxFrameLen)
441-			n, err := req.Body.Read(*buf)
442-			if n > 0 {
443-				*buf = (*buf)[:n]
444-				s.buf.put(recvMsg{buffer: mem.NewBuffer(buf, ht.bufferPool)})
```

Probes (`verify/repro/c11/run.sh`, test file `verify/repro/c11/zz_c11_probe_test.go.txt`): `TestProbeC11_Accounting` puts one-byte `[:1]` slices of separate 16 KiB pool allocations straight into a `recvBuffer` and prints the estimate next to the real backing capacity. `TestProbeC11_HandlerShortReads` drives the real `NewServerHandlerTransport(...).HandleStreams` with an `http.Request` body that returns one byte per `Read`, an RPC handler that does not read, and a recording pool.

```sh
cd ~/repos/grpc-go && verify/repro/c11/run.sh
```

```console
    zz_c11_probe_test.go:85: recvMsgSize=56 compactionThreshold=58368
    zz_c11_probe_test.go:93: after    2 puts: backlog entries=1 payload len sum=1 backing cap sum=16384 (1 entries with cap 16384) | compactBacklogLocked estimate=57 (threshold 58368, compacts when estimate > threshold: false) | pool buffers outstanding=2 (32768 bytes)
    zz_c11_probe_test.go:93: after  600 puts: backlog entries=599 payload len sum=599 backing cap sum=9814016 (599 entries with cap 16384) | compactBacklogLocked estimate=34143 (threshold 58368, compacts when estimate > threshold: false) | pool buffers outstanding=600 (9830400 bytes)
    zz_c11_probe_test.go:93: after 1024 puts: backlog entries=1023 payload len sum=1023 backing cap sum=16760832 (1023 entries with cap 16384) | compactBacklogLocked estimate=58311 (threshold 58368, compacts when estimate > threshold: false) | pool buffers outstanding=1024 (16777216 bytes)
    zz_c11_probe_test.go:93: after 1025 puts: backlog entries=1024 payload len sum=1024 backing cap sum=16777216 (1024 entries with cap 16384) | compactBacklogLocked estimate=58368 (threshold 58368, compacts when estimate > threshold: false) | pool buffers outstanding=1025 (16793600 bytes)
    zz_c11_probe_test.go:93: after 1100 puts: backlog entries=75 payload len sum=1099 backing cap sum=1213441 (74 entries with cap 16384) | compactBacklogLocked estimate=4218 (threshold 58368, compacts when estimate > threshold: false) | pool buffers outstanding=76 (1229825 bytes)
--- PASS: TestProbeC11_Accounting (0.00s)
    zz_c11_probe_test.go:166: after  600 one-byte body reads (Read was offered len(p)=16384): backlog entries=599 (+1 in channel) payload=599 bytes backing cap sum=9814016 (599 entries with cap 16384) | estimate=34143 threshold=58368 | pool: Get(16384) calls=601 other Gets=map[] Put calls=0 outstanding=601 buffers (9846784 bytes)
    zz_c11_probe_test.go:166: after 1024 one-byte body reads (Read was offered len(p)=16384): backlog entries=1023 (+1 in channel) payload=1023 bytes backing cap sum=16760832 (1023 entries with cap 16384) | estimate=58311 threshold=58368 | pool: Get(16384) calls=1025 other Gets=map[] Put calls=0 outstanding=1025 buffers (16793600 bytes)
    zz_c11_probe_test.go:166: after 1025 one-byte body reads (Read was offered len(p)=16384): backlog entries=1024 (+1 in channel) payload=1024 bytes backing cap sum=16777216 (1024 entries with cap 16384) | estimate=58368 threshold=58368 | pool: Get(16384) calls=1026 other Gets=map[] Put calls=0 outstanding=1026 buffers (16809984 bytes)
    zz_c11_probe_test.go:166: after 1100 one-byte body reads (Read was offered len(p)=16384): backlog entries=75 (+1 in channel) payload=1099 bytes backing cap sum=1213441 (74 entries with cap 16384) | estimate=4218 threshold=58368 | pool: Get(16384) calls=1101 other Gets=map[1025:1] Put calls=1025 outstanding=77 buffers (1246209 bytes)
--- PASS: TestProbeC11_HandlerShortReads (0.01s)
PASS
ok  	google.golang.org/grpc/internal/transport	0.018s
```

*backing_storage_accounting*: the estimate is `suffixLen*recvMsgSize(56) + Σ buffer.Len()`. For 600 queued one-byte slices it is 34,143 while the backlog actually pins 599 × 16,384 = 9,814,016 bytes; capacity never enters the calculation and compaction only fires when the estimate exceeds 58,368 — i.e. on the 1,026th message, at 16.8 MB retained.

*handler_short_read_trigger*: through `HandleStreams`, after 600 one-byte body reads there are 601 `Get(16384)` calls and 0 `Put`s; 599 backlog entries + 1 channel entry each hold a distinct 16 KiB buffer (9.8 MB for 600 payload bytes), estimate 34,143 < 58,368, no consolidation. Consolidation happens only after the 1,025th queued entry (after 1,100 reads: one `Get(1025)`, 1,025 `Put`s, 75 backlog entries).

Impact reasoning: on the `ServeHTTP`/handler transport every `req.Body.Read` gets a fresh 16 KiB pool buffer that is queued sliced to the bytes read, so a peer (or an intermediary) that delivers the body in tiny reads to a stalled handler makes each unread byte pin 16 KiB — up to ~16.8 MB per stream before the length-based trigger fires, and again for every further ~1,024 small reads' worth between compactions. That is a larger per-byte amplification than the tiny-DATA-frame case the change set out to fix (≈76x), and it is on by default for handler-transport users. The 16 KiB-per-read pattern predates this change; the change's accounting simply does not see it. No configuration workaround other than not using the handler transport.

## C12

Target: [evalon/grpc-go-tr-3e360711](https://github.com/kaitranntt-evals/grpc-go-transport-restrict-memory-overhead/tree/evalon/grpc-go-tr-3e360711). Parts: *per_cycle_allocation* and *repeated_cycle_retention*.


Probe (`verify/repro/c12/zz_c12_probe_test.go.txt`): `http2Client.newStream`; 2,048 cycles of three one-byte `put`s then `recvBufferReader.Read` until three bytes are delivered; delivered buffers retained; distinct backing arrays summed by capacity (the fixture's `IncrementalMessageAssembly` method, whose limit `4*payload + 64 KiB` is 90,112 at 2,048 cycles). Cycle 0 is traced.

```sh
cd ~/repos/grpc-go && PROBE_FILE=../c12/zz_c12_probe_test.go.txt verify/repro/probes/run.sh ~/wt/3e360711 '^TestProbeC12_'
```

```console
    zz_probe_test.go:53: cycle 0 after put #1: len(chan)=1 len(backlog)=0 compacted(len=0,cap=0)
    zz_probe_test.go:53: cycle 0 after put #2: len(chan)=1 len(backlog)=1 compacted(len=0,cap=0)
    zz_probe_test.go:53: cycle 0 after put #3: len(chan)=1 len(backlog)=1 compacted(len=2,cap=64)
    zz_probe_test.go:64: cycle 0 read: len=1 cap=1 isInputBuffer#1=true isInputBuffer#2=false isInputBuffer#3=false
    zz_probe_test.go:64: cycle 0 read: len=2 cap=64 isInputBuffer#1=false isInputBuffer#2=false isInputBuffer#3=false
    zz_probe_test.go:43: after 1 cycles (3 payload bytes): 2 delivered buffers retained; distinct backing arrays [len=1,cap=1:1 len=2,cap=64:1]; total backing capacity=65 bytes; 4*payload+64KiB=65548; exceeds 90112: false
    zz_probe_test.go:43: after 1024 cycles (3072 payload bytes): 2048 delivered buffers retained; distinct backing arrays [len=1,cap=1:1024 len=2,cap=64:1024]; total backing capacity=66560 bytes; 4*payload+64KiB=77824; exceeds 90112: false
    zz_probe_test.go:43: after 2048 cycles (6144 payload bytes): 4096 delivered buffers retained; distinct backing arrays [len=1,cap=1:2048 len=2,cap=64:2048]; total backing capacity=133120 bytes; 4*payload+64KiB=90112; exceeds 90112: true
--- PASS: TestProbeC12_ThreeByteCycles (0.00s)
PASS
ok  	google.golang.org/grpc/internal/transport	0.006s
```

Source lines traced:

```sh
cd ~/wt/3e360711 && grep -n 'recvBufferMinCompactedCap = \|b.compacted = make(\|mem.SliceBuffer(b.compacted)' internal/transport/transport.go
```

```console
92:	recvBufferMinCompactedCap = 64
163:		b.compacted = make([]byte, 0, max(recvBufferMinCompactedCap, 2*(tailLen+bufLen)))
185:	b.backlog[len(b.backlog)-1].buffer = mem.SliceBuffer(b.compacted)
```

The unmodified fixture (1,024 cycles) passes on this branch (`verify/repro/fixture/run.sh ~/wt/3e360711`):

```console
$ go test -v -run '^TestEval_' google.golang.org/grpc/internal/transport -race -count=1
--- PASS: TestEval_RecvBufferCompaction_MultiCycleMemoryBound (0.01s)
    --- PASS: TestEval_RecvBufferCompaction_MultiCycleMemoryBound/MultiCycleBursts (0.00s)
    --- PASS: TestEval_RecvBufferCompaction_MultiCycleMemoryBound/IncrementalMessageAssembly (0.00s)
```

*per_cycle_allocation*: put #1 goes to the channel, put #2 to the backlog, put #3 makes `compact` allocate `compacted` with cap 64 (`max(recvBufferMinCompactedCap, 2*(1+1))`) holding bytes 2–3; the reader then receives the original one-byte input buffer #1 (`len=1 cap=1`) and, via `flushCompacted`, `mem.SliceBuffer(b.compacted)` un-shrunk (`len=2 cap=64`). *repeated_cycle_retention*: 1,024 cycles → 66,560 bytes (under the fixture's 77,824, hence the fixture pass); 2,048 cycles → 2,048 × (64 + 1) = 133,120 bytes > 90,112.

Impact reasoning: same pattern as C3 with a smaller constant — a reader retaining delivered buffers under a trickle of tiny frames keeps 65 bytes of backing storage per 3 payload bytes (≈22x), growing linearly with the number of cycles; the supplied fixture only notices past ~1,400 cycles because of its fixed 64 KiB allowance.
