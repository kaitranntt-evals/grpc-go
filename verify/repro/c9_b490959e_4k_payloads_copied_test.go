// How to run (branch evalon/grpc-go-tr-b490959e): cp this file to internal/transport/verify_c9_4k_payloads_copied_test.go && go test -v -run '^TestVerifyC9_' google.golang.org/grpc/internal/transport -race -count=1
//
// C9: are queued 4 KiB payloads that already sit in 4 KiB pooled buffers copied
// into compaction storage (losing their original backing array)?
package transport

import (
	"bytes"
	"sync"
	"testing"
	"unsafe"

	"google.golang.org/grpc/mem"
)

// vC9Pool wraps the default tiered pool and records every Get/Put by capacity.
type vC9Pool struct {
	mu   sync.Mutex
	base mem.BufferPool
	gets map[int]int
	puts map[int]int
}

func (p *vC9Pool) Get(n int) *[]byte {
	b := p.base.Get(n)
	p.mu.Lock()
	p.gets[cap(*b)]++
	p.mu.Unlock()
	return b
}

func (p *vC9Pool) Put(b *[]byte) {
	p.mu.Lock()
	p.puts[cap(*b)]++
	p.mu.Unlock()
	p.base.Put(b)
}

func vC9Run(t *testing.T, size, count int) {
	pool := &vC9Pool{base: mem.DefaultBufferPool(), gets: map[int]int{}, puts: map[int]int{}}
	b := &recvBuffer{}
	b.initWithPool(pool) // as http2_client.go / http2_server.go do: s.Stream.buf.initWithPool(t.bufferPool)

	var origPtrs []unsafe.Pointer
	var origCaps []int
	var want []byte
	for i := 0; i < count; i++ {
		payload := bytes.Repeat([]byte{byte(i + 1)}, size)
		want = append(want, payload...)
		buf := mem.Copy(payload, pool) // what a DATA frame of this size is: a buffer from the pool
		d := buf.ReadOnlyData()
		origPtrs = append(origPtrs, unsafe.Pointer(unsafe.SliceData(d)))
		origCaps = append(origCaps, cap(d))
		b.put(recvMsg{buffer: buf})
	}
	pool.mu.Lock()
	t.Logf("C9 size=%d: %d payloads queued, no reads. source buffer caps=%v", size, count, origCaps)
	t.Logf("C9 size=%d: pool gets by capacity=%v, puts by capacity (buffers already released while still unread)=%v", size, pool.gets, pool.puts)
	pool.mu.Unlock()
	b.mu.Lock()
	pendingLen := 0
	if b.pending != nil {
		pendingLen = len(*b.pending)
	}
	t.Logf("C9 size=%d: len(b.c)=%d len(b.backlog)=%d len(pending)=%d", size, len(b.c), len(b.backlog), pendingLen)
	b.mu.Unlock()

	isOrig := func(p unsafe.Pointer) int {
		for i, o := range origPtrs {
			if o == p {
				return i
			}
		}
		return -1
	}
	var got []byte
	delivered, zeroCopy := 0, 0
	for len(got) < len(want) {
		m := <-b.c
		b.load()
		d := m.buffer.ReadOnlyData()
		idx := isOrig(unsafe.Pointer(unsafe.SliceData(d)))
		t.Logf("C9 size=%d: delivered buffer #%d: len=%d cap=%d backing=%p -> original backing storage of payload: %d (-1 = none, i.e. a copy)", size, delivered, len(d), cap(d), unsafe.SliceData(d), idx)
		if idx >= 0 && len(d) == size {
			zeroCopy++
		}
		delivered++
		got = append(got, d...)
		m.buffer.Free()
	}
	if !bytes.Equal(got, want) {
		t.Errorf("delivered bytes differ from the queued payloads")
	}
	t.Logf("C9 size=%d: RESULT %d of %d payloads delivered from their original backing storage; %d buffers delivered in total", size, zeroCopy, count, delivered)
}

func TestVerifyC9_Homogeneous4KiBPayloads(t *testing.T) {
	t.Logf("recvCompactionMaxPayload=%d recvCompactionChunkSize=%d", recvCompactionMaxPayload, recvCompactionChunkSize)
	vC9Run(t, 4096, 9)
}

// Control: one byte over the threshold keeps the zero-copy path.
func TestVerifyC9_Control4097BytePayloads(t *testing.T) {
	vC9Run(t, 4097, 9)
}
