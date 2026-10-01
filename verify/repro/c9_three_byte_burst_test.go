// Run (from the root of a grpc-go checkout of this verify branch): git fetch https://github.com/kaitranntt-evals/grpc-go-transport-restrict-memory-overhead evalon/grpc-go-tr-19af14ab && git worktree add --detach /tmp/wt-19af14ab FETCH_HEAD && cp verify/repro/c9_three_byte_burst_test.go /tmp/wt-19af14ab/internal/transport/zz_verify_c9_test.go && (cd /tmp/wt-19af14ab && go test ./internal/transport -run 'TestVerifyC9' -count=1 -v)

package transport

import (
	"fmt"
	"runtime"
	"sort"
	"strings"
	"testing"

	"google.golang.org/grpc/internal/envconfig"
	"google.golang.org/grpc/mem"
)

type c9Site struct {
	fn    string
	bytes int64
	objs  int64
}

// c9Trace delivers three one-byte frames to an undrained recvBuffer with the
// heap profiler sampling every allocation, and returns the allocations made
// on behalf of recvBuffer.put, grouped by the innermost transport function.
// With bursts > 1 every burst but the last is fully read and freed before the
// next one arrives; the last one is left undrained.
func c9Trace(t *testing.T, compaction bool, bursts int) (sites []c9Site, backlogLen, backlogPayload, backlogCap, chanLen int, largestAlloc int64) {
	old := envconfig.EnableReceiveBufferCompaction
	envconfig.EnableReceiveBufferCompaction = compaction
	defer func() { envconfig.EnableReceiveBufferCompaction = old }()

	oldRate := runtime.MemProfileRate
	runtime.MemProfileRate = 1
	defer func() { runtime.MemProfileRate = oldRate }()

	snapshot := func() map[string][2]int64 {
		runtime.GC() // publish pending profile records
		runtime.GC()
		n, _ := runtime.MemProfile(nil, true)
		recs := make([]runtime.MemProfileRecord, n+200)
		n, ok := runtime.MemProfile(recs, true)
		if !ok {
			t.Fatalf("MemProfile: buffer too small")
		}
		out := map[string][2]int64{}
		for _, r := range recs[:n] {
			frames := runtime.CallersFrames(r.Stack())
			var names []string
			viaPut := false
			for {
				f, more := frames.Next()
				names = append(names, fmt.Sprintf("%s (transport.go:%d)", f.Function, f.Line))
				if strings.HasSuffix(f.Function, "(*recvBuffer).put") && strings.HasSuffix(f.File, "transport.go") {
					viaPut = true
				}
				if !more {
					break
				}
			}
			if !viaPut {
				continue
			}
			// innermost transport-package frame
			key := ""
			for _, nm := range names {
				if strings.Contains(nm, "internal/transport.") {
					key = nm[strings.LastIndex(nm, "/")+1:]
					break
				}
			}
			v := out[key]
			v[0] += r.AllocBytes
			v[1] += r.AllocObjects
			out[key] = v
		}
		return out
	}

	b := &recvBuffer{}
	b.init()

	before := snapshot()
	for i := 0; i < bursts; i++ {
		b.put(recvMsg{buffer: mem.SliceBuffer{1}})
		b.put(recvMsg{buffer: mem.SliceBuffer{2}})
		b.put(recvMsg{buffer: mem.SliceBuffer{3}})
		if i == bursts-1 {
			break
		}
		for got := 0; got < 3; {
			m := <-b.c
			got += m.buffer.Len()
			m.buffer.Free()
			b.load()
		}
	}
	after := snapshot()

	for k, v := range after {
		d := c9Site{fn: k, bytes: v[0] - before[k][0], objs: v[1] - before[k][1]}
		if d.objs > 0 {
			sites = append(sites, d)
			if per := d.bytes / d.objs; per > largestAlloc {
				largestAlloc = per
			}
		}
	}
	sort.Slice(sites, func(i, j int) bool { return sites[i].fn < sites[j].fn })

	b.mu.Lock()
	backlogLen = len(b.backlog)
	for _, m := range b.backlog {
		backlogPayload += m.buffer.Len()
		backlogCap += cap(m.buffer.ReadOnlyData())
	}
	b.mu.Unlock()
	chanLen = len(b.c)
	return
}

func TestVerifyC9_ThreeOneByteFrames(t *testing.T) {
	totals, caps := map[bool]int64{}, map[bool]int{}
	for _, compaction := range []bool{false, true} {
		sites, bl, bp, bc, cl, _ := c9Trace(t, compaction, 1)
		t.Logf("compaction=%v: receive channel holds %d msg (1 byte); backlog holds %d entr%s with %d payload bytes in %d bytes of backing capacity", compaction, cl, bl, map[bool]string{true: "y", false: "ies"}[bl == 1], bp, bc)
		var total int64
		for _, s := range sites {
			t.Logf("compaction=%v:   alloc under recvBuffer.put: %-52s objects=%d bytes=%d", compaction, s.fn, s.objs, s.bytes)
			total += s.bytes
		}
		t.Logf("compaction=%v: total bytes allocated under recvBuffer.put for the 3 frames = %d", compaction, total)
		totals[compaction], caps[compaction] = total, bc
	}
	t.Logf("C9 RESULT: three one-byte frames, channel undrained: backing capacity holding the 2 backlogged bytes: disabled=%d enabled=%d; bytes allocated under put: disabled=%d enabled=%d (+%d)", caps[false], caps[true], totals[false], totals[true], totals[true]-totals[false])
	if caps[true] >= 4096 && caps[false] < 4096 && totals[true]-totals[false] >= 4000 {
		t.Errorf("PROBLEM REPRODUCED: only with compaction enabled, a %d-byte compaction destination is allocated to hold the two backlogged bytes", caps[true])
	}
}

func TestVerifyC9_RepeatedThreeByteBursts(t *testing.T) {
	const bursts = 100
	totals := map[bool]int64{}
	for _, compaction := range []bool{false, true} {
		sites, _, _, _, _, _ := c9Trace(t, compaction, bursts)
		var total int64
		for _, s := range sites {
			t.Logf("compaction=%v:   alloc under recvBuffer.put: %-52s objects=%d bytes=%d", compaction, s.fn, s.objs, s.bytes)
			total += s.bytes
		}
		totals[compaction] = total
	}
	t.Logf("C9 RESULT (repeated): %d bursts of three one-byte frames, each fully read before the next: bytes allocated under recvBuffer.put: disabled=%d (%d/burst) enabled=%d (%d/burst)", bursts, totals[false], totals[false]/bursts, totals[true], totals[true]/bursts)
	if totals[true]-totals[false] >= 4000*bursts {
		t.Errorf("PROBLEM REPRODUCED: every three-byte burst allocates a fresh compaction destination of at least 4096 bytes when compaction is enabled")
	}
}
