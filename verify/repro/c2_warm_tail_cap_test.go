// Run: sh verify/repro/c2_warm_tail_cap.sh (copies this file into internal/transport on evalon/grpc-go-tr-86436e27 and runs `go test -tags verify_repro -run 'TestVerifyC2' -v ./internal/transport`).

//go:build verify_repro

package transport

import (
	"fmt"
	"testing"

	"google.golang.org/grpc/mem"
)

// c2TracePool wraps a BufferPool and records every Get/Put so the test can
// attribute retained capacity to individual destination allocations.
type c2TracePool struct {
	inner mem.BufferPool
	live  map[*[]byte]int // outstanding pooled allocations -> capacity
	gets  []int           // requested sizes, in order
}

func newC2TracePool() *c2TracePool {
	return &c2TracePool{inner: mem.DefaultBufferPool(), live: map[*[]byte]int{}}
}

func (p *c2TracePool) Get(n int) *[]byte {
	b := p.inner.Get(n)
	p.live[b] = cap(*b)
	p.gets = append(p.gets, n)
	return b
}

func (p *c2TracePool) Put(b *[]byte) {
	delete(p.live, b)
	p.inner.Put(b)
}

func (p *c2TracePool) liveBytes() int {
	n := 0
	for _, c := range p.live {
		n += c
	}
	return n
}

// c2Entry describes one pending queue entry.
type c2Entry struct {
	where string
	n     int // payload bytes
	c     int // capacity of the backing allocation
}

// c2Pending lists every pending entry (channel slot first, then backlog,
// including the open compaction tail) without consuming anything. Every entry
// is a distinct backing allocation, so each is counted exactly once.
func c2Pending(b *recvBuffer) []c2Entry {
	var out []c2Entry
	select {
	case m := <-b.c:
		d := m.buffer.ReadOnlyData()
		out = append(out, c2Entry{"chan", len(d), cap(d)})
		b.c <- m
	default:
	}
	for i, m := range b.backlog {
		if m.buffer == nil {
			if b.tail == nil || i != len(b.backlog)-1 {
				panic("unexpected nil backlog buffer")
			}
			out = append(out, c2Entry{"backlog(open tail)", len(*b.tail), cap(*b.tail)})
			continue
		}
		d := m.buffer.ReadOnlyData()
		out = append(out, c2Entry{"backlog", len(d), cap(d)})
	}
	return out
}

func c2Totals(es []c2Entry) (payload, capacity int) {
	for _, e := range es {
		payload += e.n
		capacity += e.c
	}
	return
}

func c2Summarize(es []c2Entry) string {
	// Histogram of "payload/cap" classes in order of first appearance.
	type class struct{ n, c int }
	var order []class
	count := map[class]int{}
	for _, e := range es {
		k := class{e.n, e.c}
		if count[k] == 0 {
			order = append(order, k)
		}
		count[k]++
	}
	s := ""
	for _, k := range order {
		s += fmt.Sprintf(" %dx[%d/%d]", count[k], k.n, k.c)
	}
	return s
}

// c2Consume hands one entry to the "application" exactly like
// recvBufferReader does: receive from the channel, then load().
func c2Consume(b *recvBuffer) {
	m := <-b.c
	m.buffer.Free()
	b.load()
}

func c2PutPairs(t *testing.T, label string, b *recvBuffer, pool *c2TracePool, pairs int) {
	for i := 0; i < pairs; i++ {
		before := len(pool.gets)
		ntc := b.nextTailCap
		tailOpen := b.tail != nil
		b.put(recvMsg{buffer: mem.Copy([]byte{byte(i + 1)}, pool)})
		tinyGets := append([]int(nil), pool.gets[before:]...)
		tailCap := -1
		if b.tail != nil {
			tailCap = cap(*b.tail)
		}
		b.put(recvMsg{buffer: mem.Copy(make([]byte, 2048), pool)})
		if i < 3 || i == pairs-1 {
			t.Logf("%s pair %2d: nextTailCap=%d tailOpenBeforeTiny=%v pool.Get sizes while queuing tiny=%v destination cap for tiny=%d backlogLen=%d", label, i+1, ntc, tailOpen, tinyGets, tailCap, len(b.backlog))
		}
		if len(b.backlog) == 0 {
			t.Fatalf("%s: backlog became empty", label)
		}
	}
}

// c2Fresh builds a brand-new buffer holding the same pending payload sequence.
func c2Fresh(t *testing.T, payloadSizes []int) (*recvBuffer, *c2TracePool) {
	pool := newC2TracePool()
	b := &recvBuffer{}
	b.init(pool)
	for _, n := range payloadSizes {
		b.put(recvMsg{buffer: mem.Copy(make([]byte, n), pool)})
	}
	return b, pool
}

func c2Sizes(es []c2Entry) []int {
	out := make([]int, len(es))
	for i, e := range es {
		out[i] = e.n
	}
	return out
}

func c2Compare(t *testing.T, label string, warm *recvBuffer, warmPool *c2TracePool) {
	t.Helper()
	wes := c2Pending(warm)
	wp, wc := c2Totals(wes)
	fresh, freshPool := c2Fresh(t, c2Sizes(wes))
	fes := c2Pending(fresh)
	fp, fc := c2Totals(fes)
	t.Logf("%s WARMED: entries=%d pendingPayload=%d retainedCap=%d pooledLiveBytes=%d nextTailCap=%d", label, len(wes), wp, wc, warmPool.liveBytes(), warm.nextTailCap)
	t.Logf("%s WARMED layout (count x [payload/cap]):%s", label, c2Summarize(wes))
	t.Logf("%s FRESH : entries=%d pendingPayload=%d retainedCap=%d pooledLiveBytes=%d nextTailCap=%d", label, len(fes), fp, fc, freshPool.liveBytes(), fresh.nextTailCap)
	t.Logf("%s FRESH  layout (count x [payload/cap]):%s", label, c2Summarize(fes))
	if wp != fp {
		t.Fatalf("%s: pending payload differs: warmed=%d fresh=%d", label, wp, fp)
	}
	t.Logf("%s RESULT: warmed-fresh retained capacity = %d bytes (%.1fx)", label, wc-fc, float64(wc)/float64(fc))
	if wc > fc {
		t.Errorf("%s: C2 CONFIRMED: warmed buffer retains %d bytes of payload-backing capacity, matched fresh buffer retains %d", label, wc, fc)
	}
}

// Scenario 1: reader partially catches up (backlog never empty), then traffic
// changes to twenty alternating tiny/2-KiB pairs.
func TestVerifyC2_WarmedThenAlternating_PartialDrain(t *testing.T) {
	pool := newC2TracePool()
	b := &recvBuffer{}
	b.init(pool)
	for i := 0; i < 16384; i++ {
		b.put(recvMsg{buffer: mem.Copy([]byte{byte(i)}, pool)})
	}
	t.Logf("after warm-up: nextTailCap=%d layout:%s", b.nextTailCap, c2Summarize(c2Pending(b)))
	for len(b.backlog) > 1 {
		c2Consume(b)
	}
	t.Logf("after partial drain: backlogLen=%d nextTailCap=%d layout:%s", len(b.backlog), b.nextTailCap, c2Summarize(c2Pending(b)))
	c2PutPairs(t, "warmed", b, pool, 20)
	c2Compare(t, "[snapshot A: right after the 20 pairs]", b, pool)

	// Consume the two remaining warm-up entries so only the alternating
	// traffic is pending. The backlog still never becomes empty.
	c2Consume(b)
	c2Consume(b)
	if len(b.backlog) == 0 {
		t.Fatal("backlog became empty")
	}
	c2Compare(t, "[snapshot B: only alternating traffic pending]", b, pool)
}

// Scenario 2: nothing is consumed at all.
func TestVerifyC2_WarmedThenAlternating_NoDrain(t *testing.T) {
	pool := newC2TracePool()
	b := &recvBuffer{}
	b.init(pool)
	for i := 0; i < 16384; i++ {
		b.put(recvMsg{buffer: mem.Copy([]byte{byte(i)}, pool)})
	}
	c2PutPairs(t, "warmed", b, pool, 20)
	c2Compare(t, "[no drain]", b, pool)
}

// Counterfactual isolating the cause: identical to scenario 1, except the test
// resets nextTailCap (the remembered destination size) to its initial value
// before the alternating traffic. If the excess disappears, it is attributable
// to the historical destination size and nothing else.
func TestVerifyC2_Counterfactual_ResetNextTailCap(t *testing.T) {
	pool := newC2TracePool()
	b := &recvBuffer{}
	b.init(pool)
	for i := 0; i < 16384; i++ {
		b.put(recvMsg{buffer: mem.Copy([]byte{byte(i)}, pool)})
	}
	for len(b.backlog) > 1 {
		c2Consume(b)
	}
	t.Logf("nextTailCap after warm-up=%d; test forces it to %d", b.nextTailCap, recvBufferCompactionMinCap)
	b.nextTailCap = recvBufferCompactionMinCap
	c2PutPairs(t, "warmed+reset", b, pool, 20)
	c2Consume(b)
	c2Consume(b)
	c2Compare(t, "[counterfactual, only alternating traffic pending]", b, pool)
}

// Control: the same twenty pairs on a fresh buffer, with allocation tracing.
func TestVerifyC2_FreshAlternatingControl(t *testing.T) {
	pool := newC2TracePool()
	b := &recvBuffer{}
	b.init(pool)
	b.put(recvMsg{buffer: mem.Copy(make([]byte, 2048), pool)}) // occupies the channel slot
	c2PutPairs(t, "fresh ", b, pool, 20)
	es := c2Pending(b)
	p, c := c2Totals(es)
	t.Logf("fresh control: pendingPayload=%d retainedCap=%d layout:%s", p, c, c2Summarize(es))
}

// The eval fixture's own alternating-traffic bound (TestEval_RecvBufferCompaction_MixedFrames,
// second half: retained <= 4*payload + 64 KiB over b.backlog), replayed verbatim
// except that the buffer is first warmed with 16,384 one-byte frames. The
// warm-up bytes are counted as payload, which only loosens the bound.
func TestVerifyC2_EvalFixtureAlternatingBound(t *testing.T) {
	for _, warm := range []int{0, 16384} {
		t.Run(fmt.Sprintf("warmFrames=%d", warm), func(t *testing.T) {
			pool := mem.DefaultBufferPool()
			bAlternating := &recvBuffer{}
			bAlternating.init(pool)

			var wantAltPayload []byte
			putAlt := func(p []byte) {
				wantAltPayload = append(wantAltPayload, p...)
				bAlternating.put(recvMsg{buffer: mem.Copy(p, pool)})
			}
			for i := 0; i < warm; i++ {
				putAlt([]byte{byte(i)})
			}

			putAlt([]byte{0x01})
			putAlt([]byte{0x02})

			for i := range 24 {
				putAlt([]byte{byte(i + 10)})
				p2k := make([]byte, 2048)
				for j := range p2k {
					p2k[j] = byte((i*17 + j) % 251)
				}
				putAlt(p2k)
			}

			var totalRetained int
			for _, m := range bAlternating.backlog {
				if m.buffer != nil {
					totalRetained += cap(m.buffer.ReadOnlyData())
				}
			}
			maxAllowedCap := 4*len(wantAltPayload) + 64*1024
			t.Logf("warmFrames=%d payload=%d retained=%d fixtureBound=%d", warm, len(wantAltPayload), totalRetained, maxAllowedCap)
			if totalRetained > maxAllowedCap {
				t.Errorf("Alternating frames: retained capacity %d exceeds bound %d (payload=%d)", totalRetained, maxAllowedCap, len(wantAltPayload))
			}
		})
	}
}
