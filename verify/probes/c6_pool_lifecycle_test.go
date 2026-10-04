//go:build verify_audit

// C6 probe: copy to internal/transport/ on evalon/grpc-go-tr-a3b171be, then: go test -tags verify_audit -v -run '^TestC6_' google.golang.org/grpc/internal/transport -race -count=1

package transport

// Probe for C6 on evalon/grpc-go-tr-a3b171be: compaction-destination
// acquisition and release against the public mem.BufferPool contract.
// Run: go test -tags verify_audit -v -run '^TestC6_' google.golang.org/grpc/internal/transport -race -count=1

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"sync"
	"testing"
	"time"
	"unsafe"

	"google.golang.org/grpc/mem"
)

// c6Track wraps a pool and tracks every Get/Put by backing-array pointer.
type c6Track struct {
	inner    mem.BufferPool
	mu       sync.Mutex
	out      map[uintptr]int
	gets     []string
	puts     int
	foreign  int
	shortLen int // Gets where len != requested
}

func (p *c6Track) Get(n int) *[]byte {
	b := p.inner.Get(n)
	p.mu.Lock()
	p.out[uintptr(unsafe.Pointer(unsafe.SliceData(*b)))] = cap(*b)
	if len(p.gets) < 4 {
		p.gets = append(p.gets, fmt.Sprintf("Get(%d)->len=%d,cap=%d", n, len(*b), cap(*b)))
	}
	if len(*b) != n {
		p.shortLen++
	}
	p.mu.Unlock()
	return b
}

func (p *c6Track) Put(b *[]byte) {
	ptr := uintptr(unsafe.Pointer(unsafe.SliceData(*b)))
	p.mu.Lock()
	if _, ok := p.out[ptr]; ok {
		delete(p.out, ptr)
		p.puts++
		p.mu.Unlock()
		p.inner.Put(b)
		return
	}
	p.foreign++
	p.mu.Unlock()
}

// c6ExactPool is contract compliant: Get(n) returns a buffer of LENGTH n (cap n).
type c6ExactPool struct{}

func (c6ExactPool) Get(n int) *[]byte { b := make([]byte, n); return &b }
func (c6ExactPool) Put(*[]byte)       {}

// c6ZeroLenPool reproduces the eval fixture's evalExactCapacityPool.Get:
// make([]byte, 0, n) -- length 0, capacity n.
type c6ZeroLenPool struct {
	gets  int
	limit int
}

func (p *c6ZeroLenPool) Get(n int) *[]byte {
	p.gets++
	if p.limit > 0 && p.gets > p.limit {
		panic("c6: Get call limit reached")
	}
	b := make([]byte, 0, n)
	return &b
}
func (*c6ZeroLenPool) Put(*[]byte) {}

func TestC6_Contract(t *testing.T) {
	tiered, _ := mem.NewBinaryTieredBufferPool(8, 12, 14, 15, 20)
	pools := []struct {
		name string
		p    mem.BufferPool
	}{
		{"mem.DefaultBufferPool()", mem.DefaultBufferPool()},
		{"mem.NewBinaryTieredBufferPool(8,12,14,15,20)", tiered},
		{"mem.NewTieredBufferPool(256,4096,16384)", mem.NewTieredBufferPool(256, 4096, 16384)},
		{"mem.NopBufferPool{}", mem.NopBufferPool{}},
		{"c6ExactPool (probe, compliant)", c6ExactPool{}},
		{"zero-length pool (= fixture evalExactCapacityPool.Get)", &c6ZeroLenPool{}},
	}
	for _, pc := range pools {
		var s string
		for _, n := range []int{100, 1000, 16384} {
			b := pc.p.Get(n)
			s += fmt.Sprintf(" Get(%d)->len=%d,cap=%d;", n, len(*b), cap(*b))
		}
		// mem.Copy is mem's own consumer of the Get contract.
		src := bytes.Repeat([]byte{7}, 2000)
		cp := mem.Copy(src, pc.p)
		t.Logf("C6 contract %-55s:%s mem.Copy(2000 bytes).Len()=%d", pc.name, s, cp.Len())
		cp.Free()
	}
}

type c6Result struct {
	want, got []byte
}

func c6Drain(t *testing.T, b *recvBuffer, n int) []byte {
	t.Helper()
	var got []byte
	deadline := time.Now().Add(5 * time.Second)
	for len(got) < n {
		if time.Now().After(deadline) {
			t.Fatalf("drain timeout %d/%d", len(got), n)
		}
		b.load()
		select {
		case m := <-b.c:
			if m.buffer != nil {
				got = append(got, m.buffer.ReadOnlyData()...)
				m.buffer.Free()
			}
		default:
		}
	}
	return got
}

func TestC6_CompliantPools(t *testing.T) {
	mk := map[string]func() mem.BufferPool{
		"BinaryTiered(8,12,14,15,20)": func() mem.BufferPool { p, _ := mem.NewBinaryTieredBufferPool(8, 12, 14, 15, 20); return p },
		"DefaultBufferPool":           func() mem.BufferPool { return mem.DefaultBufferPool() },
		"ExactLen(make([]byte,n))":    func() mem.BufferPool { return c6ExactPool{} },
		"NopBufferPool":               func() mem.BufferPool { return mem.NopBufferPool{} },
	}
	workloads := []struct {
		name string
		run  func(t *testing.T, s *ClientStream, tp *c6Track) (want, got []byte)
	}{
		{"1026x1B SliceBuffer (fixture UnpooledConsolidation)", func(t *testing.T, s *ClientStream, tp *c6Track) (want, got []byte) {
			for i := 0; i < 1026; i++ {
				p := []byte{byte((i*7)%251 + 1)}
				want = append(want, p...)
				s.buf.put(recvMsg{buffer: mem.SliceBuffer(p)})
			}
			return want, c6Drain(t, &s.buf, len(want))
		}},
		{"occupy+8KiB+5x1B pooled (fixture SliceGrowthPoolOwnership)", func(t *testing.T, s *ClientStream, tp *c6Track) (want, got []byte) {
			put := func(p []byte) { want = append(want, p...); s.buf.put(recvMsg{buffer: mem.Copy(p, tp)}) }
			put([]byte("occupy"))
			p8 := make([]byte, 8192)
			for i := range p8 {
				p8[i] = byte(i%251 + 1)
			}
			put(p8)
			for i := 0; i < 5; i++ {
				put([]byte{2})
			}
			return want, c6Drain(t, &s.buf, len(want))
		}},
		{"10x100B SliceBuffer (fixture ExactCapacitySmallDestination)", func(t *testing.T, s *ClientStream, tp *c6Track) (want, got []byte) {
			for i := 0; i < 10; i++ {
				p := make([]byte, 100)
				for j := range p {
					p[j] = byte((i*11+j)%251 + 1)
				}
				want = append(want, p...)
				s.buf.put(recvMsg{buffer: mem.SliceBuffer(p)})
			}
			return want, c6Drain(t, &s.buf, len(want))
		}},
		{"40000x1B then EOF, partial reads via recvBufferReader", func(t *testing.T, s *ClientStream, tp *c6Track) (want, got []byte) {
			for i := 0; i < 40000; i++ {
				p := []byte{byte(i%251 + 1)}
				want = append(want, p...)
				s.buf.put(recvMsg{buffer: mem.Copy(p, tp)})
			}
			s.buf.put(recvMsg{err: io.EOF})
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			r := &recvBufferReader{ctx: ctx, ctxDone: ctx.Done(), recv: &s.buf}
			var held mem.BufferSlice
			for i := 0; ; i++ {
				var buf mem.Buffer
				var err error
				if i%2 == 0 {
					var hdr [5]byte
					var n int
					n, err = r.ReadMessageHeader(hdr[:])
					got = append(got, hdr[:n]...)
				} else {
					buf, err = r.Read(777)
					if buf != nil {
						got = append(got, buf.ReadOnlyData()...)
						held = append(held, buf) // hold every payload until the end
					}
				}
				if err != nil {
					if err != io.EOF {
						t.Errorf("read err: %v", err)
					}
					break
				}
			}
			held.Free()
			return want, got
		}},
	}
	for pname, mkp := range mk {
		for _, w := range workloads {
			tp := &c6Track{inner: mkp(), out: map[uintptr]int{}}
			s := (&http2Client{bufferPool: tp}).newStream(context.Background(), &CallHdr{}, nil)
			want, got := w.run(t, s, tp)
			tp.mu.Lock()
			t.Logf("C6 pool=%-28s workload=%-58s inOrder=%v bytes=%d gets(first)=%v totalPuts=%d outstanding=%d foreign=%d getsWithLen!=n=%d",
				pname, w.name, bytes.Equal(want, got), len(got), tp.gets, tp.puts, len(tp.out), tp.foreign, tp.shortLen)
			if !bytes.Equal(want, got) || len(tp.out) != 0 || tp.foreign != 0 || len(tp.gets) == 0 {
				t.Errorf("C6 lifecycle violated: pool=%s workload=%s", pname, w.name)
			}
			tp.mu.Unlock()
		}
	}
}

// Out-of-contract pool (len 0, cap n), as used by the fixture's
// ExactCapacitySmallDestination subtest: a single put never terminates.
func TestC6_ZeroLengthPoolLivelock(t *testing.T) {
	zp := &c6ZeroLenPool{limit: 100000}
	s := (&http2Client{bufferPool: zp}).newStream(context.Background(), &CallHdr{}, nil)
	s.buf.put(recvMsg{buffer: mem.SliceBuffer(make([]byte, 100))}) // occupies b.c
	func() {
		defer func() {
			r := recover()
			t.Logf("C6 zero-length pool: second put() of 100 bytes: recovered=%v after %d Get(16384) calls; len(backlog)=%d (all empty buffers)", r, zp.gets, len(s.buf.backlog))
		}()
		s.buf.put(recvMsg{buffer: mem.SliceBuffer(make([]byte, 100))})
		t.Logf("C6 zero-length pool: put returned normally")
	}()
}
