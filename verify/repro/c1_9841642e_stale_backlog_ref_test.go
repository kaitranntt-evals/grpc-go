// How to run (branch evalon/grpc-go-tr-9841642e): cp this file to internal/transport/verify_c1_stale_backlog_ref_test.go && go test -v -run '^TestVerifyC1_' google.golang.org/grpc/internal/transport -race -count=1
//
// C1: after three one-byte puts with no reads, does a backlog entry still
// reference the source buffer whose bytes were already merged into b.compacted?
package transport

import (
	"fmt"
	"testing"
	"unsafe"

	"google.golang.org/grpc/mem"
)

func vC1Addr(d []byte) unsafe.Pointer { return unsafe.Pointer(unsafe.SliceData(d)) }

// vC1TrackedBuffer is a small mem.Buffer that records Free calls, so the probe
// can tell whether a buffer that is still referenced was already released.
type vC1TrackedBuffer struct {
	mem.SliceBuffer
	name  string
	freed *[]string
}

func (b vC1TrackedBuffer) Free() { *b.freed = append(*b.freed, b.name) }

func TestVerifyC1_ThreeOneBytePutsNoReads(t *testing.T) {
	var freed []string
	mk := func(name string, v byte) vC1TrackedBuffer {
		return vC1TrackedBuffer{SliceBuffer: mem.SliceBuffer{v}, name: name, freed: &freed}
	}
	p1, p2, p3 := mk("put#1", 0xA1), mk("put#2", 0xB2), mk("put#3", 0xC3)

	b := &recvBuffer{}
	b.init()
	b.put(recvMsg{buffer: p1})
	b.put(recvMsg{buffer: p2})
	b.put(recvMsg{buffer: p3})

	b.mu.Lock()
	t.Logf("after 3 one-byte puts, no reads: len(b.c)=%d len(b.backlog)=%d", len(b.c), len(b.backlog))
	t.Logf("b.compacted (consolidated payload) = % x  (len=%d cap=%d, backing %p)", b.compacted, len(b.compacted), cap(b.compacted), vC1Addr(b.compacted))
	t.Logf("buffers already released via Free(): %v", freed)
	stale := false
	for i, m := range b.backlog {
		d := m.buffer.ReadOnlyData()
		tb, isSrc := m.buffer.(vC1TrackedBuffer)
		t.Logf("b.backlog[%d].buffer: type=%T payload=% x backing=%p", i, m.buffer, d, vC1Addr(d))
		if isSrc {
			wasFreed := false
			for _, n := range freed {
				wasFreed = wasFreed || n == tb.name
			}
			t.Logf("  -> b.backlog[%d].buffer IS the original source buffer of %s (identical backing array %p == %p: %v); already freed: %v; its byte is also in b.compacted: %v",
				i, tb.name, vC1Addr(d), vC1Addr(p2.SliceBuffer), vC1Addr(d) == vC1Addr(p2.SliceBuffer), wasFreed, len(b.compacted) > 0 && b.compacted[0] == d[0])
			stale = stale || wasFreed
		}
	}
	queuedViaBacklog := 0
	for _, m := range b.backlog {
		queuedViaBacklog += m.buffer.Len()
	}
	t.Logf("unread bytes behind the channel: %d according to b.backlog entries, %d according to b.compacted", queuedViaBacklog, len(b.compacted))
	b.mu.Unlock()

	if stale {
		t.Logf("C1 OBSERVED: a queue entry still references a replaced (merged and freed) source buffer")
	} else {
		t.Logf("C1 NOT OBSERVED: no queue entry references a replaced source buffer")
	}

	// What the reader actually receives (load() seals the entry before handing it out).
	var got []byte
	for len(got) < 3 {
		m := <-b.c
		got = append(got, m.buffer.ReadOnlyData()...)
		b.load()
	}
	t.Logf("bytes delivered to a reader through get()/load(): % x", got)
	if fmt.Sprintf("% x", got) != "a1 b2 c3" {
		t.Errorf("reader got % x, want a1 b2 c3", got)
	}
}

// With a pooled (ref-counted) tail of <= 1 KiB, the stale entry is a buffer that
// was already returned to its pool: any white-box consumer of b.backlog
// (as the eval fixture's teardown loops are) trips over it.
func TestVerifyC1_StaleEntryIsAFreedPooledBuffer(t *testing.T) {
	pool := mem.DefaultBufferPool()
	mkPooled := func(v byte) mem.Buffer {
		raw := pool.Get(4096) // pooled backing array ...
		(*raw)[0] = v
		full := mem.NewBuffer(raw, pool)
		small, rest := mem.SplitUnsafe(full, 1) // ... exposing a 1-byte payload
		rest.Free()
		return small
	}
	b := &recvBuffer{}
	b.init()
	b.put(recvMsg{buffer: mkPooled(1)})
	b.put(recvMsg{buffer: mkPooled(2)})
	b.put(recvMsg{buffer: mkPooled(3)})
	b.mu.Lock()
	defer b.mu.Unlock()
	t.Logf("len(b.backlog)=%d b.compacted=% x", len(b.backlog), b.compacted)
	func() {
		defer func() {
			if r := recover(); r != nil {
				t.Logf("C1 OBSERVED: reading b.backlog[0].buffer panics: %v", r)
			}
		}()
		t.Logf("b.backlog[0].buffer payload = % x", b.backlog[0].buffer.ReadOnlyData())
	}()
	func() {
		defer func() {
			if r := recover(); r != nil {
				t.Logf("C1 OBSERVED: freeing b.backlog[0].buffer (as the fixture teardown loops do) panics: %v", r)
			}
		}()
		b.backlog[0].buffer.Free()
		t.Logf("freeing b.backlog[0].buffer did not panic")
	}()
}
